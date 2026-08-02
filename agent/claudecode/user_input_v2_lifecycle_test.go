package claudecode

// user_input_v2_lifecycle_test.go 是 P2-regression：驱动 Claude v2 结构化用户输入的完整生命周期，
// 覆盖单元测试之外「集成」层面：go-bridge 可达性、事件/control_response 全序、多交互隔离、
// v1↔v2 运行时切换共存。
//
// 与 user_input_v2_test.go 的区别：本文件强调「端到端时序」与「跨边界可达性」：
//   - 编译期断言 (*claudeSession) 实现 core.UserInputResponder —— go-bridge handleResolveUserInput
//     通过 sess.(core.UserInputResponder) 类型断言可达 Claude（P1 已建 handler，P2 接通目标）；
//   - 完整 answer/reject 生命周期的事件 + control_response 全序；
//   - 同一 session 上两次连续交互互不串扰（registry 按 interactionId 隔离）；
//   - 同一 session 运行时翻转 structuredUserInputV2 标志：OFF 走 v1、ON 走 v2，证明 P6 可安全翻标志。
//
// 依据：docs/2026-08-01-codex-claude-structured-user-input-design.md §9 / §14 P2。

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 编译期断言：claudeSession 实现 core.UserInputResponder。
// go-bridge/handlers.go handleResolveUserInput 用 sess.(core.UserInputResponder) 类型断言取 resolver；
// 此断言保证 Claude adapter 已满足该接口，resolve_user_input RPC 可达 Claude（非仅 Codex）。
var _ core.UserInputResponder = (*claudeSession)(nil)

// drainUserInputEvents 排空事件并按类型分桶返回 requested/resolved 列表（保持抵达顺序）。
func drainUserInputEvents(cs *claudeSession) (requested, resolved []*core.Event) {
	for {
		select {
		case ev := <-cs.events:
			switch ev.Type {
			case core.EventUserInputRequested:
				requested = append(requested, &ev)
			case core.EventUserInputResolved:
				resolved = append(resolved, &ev)
			}
		default:
			return requested, resolved
		}
	}
}

// fullAnswerLifecycle 在给定 session 上跑一次 control_request→pending→answer→resolved 全链路，
// 返回 (pending event, resolved event)，并在中途断言 control_response allow 写回 + answers 正确。
func fullAnswerLifecycle(t *testing.T, cs *claudeSession, stdin *captureStdin, requestID, qText, optLabel string) (*core.Event, *core.Event) {
	t.Helper()
	cs.handleControlRequest(makeAskControlRequest(requestID, []any{
		singleQuestionMap(qText, "", false, [2]string{optLabel, ""}, [2]string{"other", ""}),
	}))
	reqs, _ := drainUserInputEvents(cs)
	if len(reqs) != 1 || reqs[0].UserInput.Status != core.UserInputStatusPending {
		t.Fatalf("[%s] 应恰好 1 个 pending requested，实际 %+v", requestID, reqs)
	}
	pending := reqs[0]
	iid := pending.UserInput.InteractionID
	qid := pending.UserInput.Questions[0].ID
	optID := pending.UserInput.Questions[0].Options[0].ID // 第一个 option = optLabel

	if _, err := cs.ResolveUserInput(t.Context(), iid, "client-"+requestID, core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: optID}}}}); err != nil {
		t.Fatalf("[%s] answer 失败: %v", requestID, err)
	}
	_, resolved := drainUserInputEvents(cs)
	if len(resolved) != 1 || resolved[0].UserInput.Status != core.UserInputStatusAnswered || resolved[0].UserInput.ResolutionSource != "ios" {
		t.Fatalf("[%s] 应 1 个 resolved(answered,ios)，实际 %+v", requestID, resolved)
	}
	// control_response allow + answers[qText]=optLabel。
	resp := respInner(t, stdin)
	if b, _ := resp["behavior"].(string); b != "allow" {
		t.Fatalf("[%s] behavior=%q want allow", requestID, b)
	}
	answers, _ := resp["updatedInput"].(map[string]any)["answers"].(map[string]any)
	if answers[qText] != optLabel {
		t.Fatalf("[%s] answers[%q]=%v want %q", requestID, qText, answers[qText], optLabel)
	}
	return pending, resolved[0]
}

// TestLifecycle_V2_FullAnswerRoundTrip：端到端 answer 全序。
func TestLifecycle_V2_FullAnswerRoundTrip(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	fullAnswerLifecycle(t, cs, stdin, "life-ans", "Color?", "Red")
}

// TestLifecycle_V2_FullRejectRoundTrip：端到端 reject 全序——control_response deny + resolved(rejected)。
func TestLifecycle_V2_FullRejectRoundTrip(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("life-rj", []any{
		singleQuestionMap("Skip?", "", false, [2]string{"a", ""}),
	}))
	reqs, _ := drainUserInputEvents(cs)
	if len(reqs) != 1 {
		t.Fatalf("应 1 pending，实际 %+v", reqs)
	}
	iid := reqs[0].UserInput.InteractionID

	if _, err := cs.ResolveUserInput(t.Context(), iid, "client-life-rj", core.UserInputActionReject, nil); err != nil {
		t.Fatalf("reject 失败: %v", err)
	}
	_, resolved := drainUserInputEvents(cs)
	if len(resolved) != 1 || resolved[0].UserInput.Status != core.UserInputStatusRejected || resolved[0].UserInput.ResolutionSource != "ios" {
		t.Fatalf("应 resolved(rejected,ios)，实际 %+v", resolved)
	}
	resp := respInner(t, stdin)
	if b, _ := resp["behavior"].(string); b != "deny" {
		t.Fatalf("reject behavior=%q want deny", b)
	}
}

// TestLifecycle_V2_TwoInteractionsDoNotCollide：同一 session 两次连续交互（不同 requestId）
// 各自独立完成 answer 生命周期，registry 按 interactionId 隔离，互不串扰。
func TestLifecycle_V2_TwoInteractionsDoNotCollide(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	p1, _ := fullAnswerLifecycle(t, cs, stdin, "seq-1", "First?", "A")
	p2, _ := fullAnswerLifecycle(t, cs, stdin, "seq-2", "Second?", "B")
	if p1.UserInput.InteractionID == p2.UserInput.InteractionID {
		t.Fatal("两次不同 requestId 的 interactionId 不应相同（registry 隔离失败）")
	}
	// 两次交互结束后 registry 里都应是 resolved。
	if cs.claudeUserInputReg.Status(p1.UserInput.InteractionID) != claudeUIResolved {
		t.Fatal("seq-1 应 resolved")
	}
	if cs.claudeUserInputReg.Status(p2.UserInput.InteractionID) != claudeUIResolved {
		t.Fatal("seq-2 应 resolved")
	}
}

// TestLifecycle_LegacyAndV2CompeteForOneClaim：单题同时有 canonical 与 legacy presentation，
// legacy 先回答后，同一个 v2 interaction 必须已终结，不能二次写 Claude stdin。
func TestLifecycle_LegacyAndV2CompeteForOneClaim(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("pre-flag", []any{
		singleQuestionMap("V1 question?", "", false, [2]string{"x", ""}, [2]string{"y", ""}),
	}))
	events := drainAllEvents(cs)
	if len(events) != 2 || events[0].Type != core.EventUserInputRequested || events[1].Type != core.EventQuestionAsked {
		t.Fatalf("应先 canonical 后 legacy，实际 %+v", events)
	}
	iid := events[0].UserInput.InteractionID
	qid := events[0].UserInput.Questions[0].ID
	optID := events[0].UserInput.Questions[0].Options[0].ID
	if err := cs.RespondQuestion("pre-flag", []string{"pre-flag:option-1"}); err != nil {
		t.Fatalf("legacy RespondQuestion 失败: %v", err)
	}
	resolvedEvents := drainAllEvents(cs)
	if len(resolvedEvents) != 2 || resolvedEvents[0].Type != core.EventUserInputResolved || resolvedEvents[1].Type != core.EventQuestionResolved {
		t.Fatalf("legacy answer 应先 canonical resolved 后 legacy resolved，实际 %+v", resolvedEvents)
	}
	writes := stdin.linesWritten()
	resolution, err := cs.ResolveUserInput(t.Context(), iid, "client-after-legacy", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: optID}}}})
	if err != nil || resolution.Outcome != core.UserInputOutcomeAlreadyResolved {
		t.Fatalf("v2 second writer = %+v, %v; want already_resolved", resolution, err)
	}
	if stdin.linesWritten() != writes {
		t.Fatal("second writer must not write another control_response")
	}
}

// TestLifecycle_V2_ResolvedInteractionNotReopenedByReplay：已 resolved 的 interaction 收到重放
// control_request 时不降级回 pending（幂等 upsert），registry 保持 resolved。
func TestLifecycle_V2_ResolvedInteractionNotReopenedByReplay(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	pending, _ := fullAnswerLifecycle(t, cs, stdin, "replay-1", "Once?", "Yes")
	iid := pending.UserInput.InteractionID

	// 重放同一 control_request（Claude 不应有此场景，但 registry 必须幂等）。
	cs.handleControlRequest(makeAskControlRequest("replay-1", []any{
		singleQuestionMap("Once?", "", false, [2]string{"Yes", ""}),
	}))
	reqs, _ := drainUserInputEvents(cs)
	// 已 resolved → 不再重发 pending（不降级）。
	for _, e := range reqs {
		if e.UserInput.Status == core.UserInputStatusPending {
			t.Fatal("已 resolved 的 interaction 重放不得降级回 pending")
		}
	}
	if cs.claudeUserInputReg.Status(iid) != claudeUIResolved {
		t.Fatalf("registry 应保持 resolved，实际 %v", cs.claudeUserInputReg.Status(iid))
	}
}
