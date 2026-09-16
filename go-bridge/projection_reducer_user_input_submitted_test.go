package gobridge

// projection_reducer_user_input_submitted_test.go 锁定 §4.4（设计 v6）的 reducer
// 状态机与历史副本收敛：
//   - 全转移表（requested/submitted/resolved/terminal 两两排列）；
//   - submitted：清 diagnostic、双 capability=false、幂等、迟到丢弃；
//   - terminal：pending/submitted → pending+turn_terminated、resolved 不动、幂等；
//   - 迟到 resolved supersede terminal；
//   - 历史 dual-turn 副本合并删除（一 interaction 一卡）；
//   - selector 优先级 resolved > terminal > submitted > pending。
//
// ev/requestedEv/uiQuestionsWire 复用 projection_reducer_user_input_test.go 的既有 helper。

import (
	"testing"
)

func requestedEvFor(seq int, backend, interaction, turnID, status string, canRespond, canReject bool) EventMessage {
	return EventMessage{
		BackendID: backend, SessionID: "s1", PerSessionSeq: seq,
		Event: "user_input_requested",
		Data: map[string]interface{}{
			"turnId":        turnID,
			"interactionId": interaction,
			"status":        status,
			"questions":     uiQuestionsWire(),
			"canRespond":    canRespond,
			"canReject":     canReject,
		},
	}
}

func userEv(seq int, backend, event, interaction, turnID string, extra map[string]interface{}) EventMessage {
	data := map[string]interface{}{
		"turnId":        turnID,
		"interactionId": interaction,
	}
	for k, v := range extra {
		data[k] = v
	}
	return EventMessage{
		BackendID: backend, SessionID: "s1", PerSessionSeq: seq,
		Event: event, Data: data,
	}
}

// findSingleUserInputPart 在投影中找 interaction 的唯一 part（多副本时返回 false）。
func findSingleUserInputPart(t *testing.T, r *ProjectionReducer, backend, interaction string) (ProjectionPart, string) {
	t.Helper()
	projection, ok := r.Snapshot(backend, "s1")
	if !ok {
		t.Fatal("missing projection")
	}
	var part ProjectionPart
	turnID := ""
	count := 0
	for _, turn := range projection.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, p := range turn.Assistant.Parts {
			if p.Type == "user_input" && p.UserInputInteractionID == interaction {
				count++
				part = p
				turnID = turn.TurnID
			}
		}
	}
	if count != 1 {
		t.Fatalf("interaction projected %d times, want exactly one", count)
	}
	return part, turnID
}

// TestReducerSubmittedClearsCapsAndDiagnostic：interactive pending → submitted：
// 双 capability=false、清 diagnostic。
func TestReducerSubmittedClearsCapsAndDiagnostic(t *testing.T) {
	r := newTestReducer()
	r.Apply(requestedEvFor(1, "claude", "ui-sub", "T1", "pending", true, true))
	// requested 带 observe_only 诊断码（门未翻开的 live 路径形状）——用 diagnosticCode 覆盖。
	r.Apply(userEv(2, "claude", "user_input_requested", "ui-sub", "T1", map[string]interface{}{
		"status": "pending", "diagnosticCode": "observe_only", "canRespond": true, "canReject": true,
		"questions": uiQuestionsWire(),
	}))
	r.Apply(userEv(3, "claude", "user_input_submitted", "ui-sub", "T1", nil))

	part, _ := findSingleUserInputPart(t, r, "claude", "ui-sub")
	if part.UserInputStatus != "submitted" {
		t.Fatalf("status=%s, want submitted", part.UserInputStatus)
	}
	if part.UserInputCanRespond || part.UserInputCanReject {
		t.Fatalf("submitted 后 capability 应全 false：%v/%v", part.UserInputCanRespond, part.UserInputCanReject)
	}
	if part.UserInputDiagnosticCode != "" {
		t.Fatalf("submitted 应清 diagnostic，实际 %q", part.UserInputDiagnosticCode)
	}
}

// TestReducerSubmittedIdempotentAndLateDropped：submitted 幂等；
// terminal/resolved 后的迟到 submitted 丢弃（不回退权威态）。
func TestReducerSubmittedIdempotentAndLateDropped(t *testing.T) {
	// 幂等。
	r := newTestReducer()
	r.Apply(requestedEvFor(1, "claude", "ui-idem", "T1", "pending", true, true))
	r.Apply(userEv(2, "claude", "user_input_submitted", "ui-idem", "T1", nil))
	r.Apply(userEv(3, "claude", "user_input_submitted", "ui-idem", "T1", nil))
	part, _ := findSingleUserInputPart(t, r, "claude", "ui-idem")
	if part.UserInputStatus != "submitted" {
		t.Fatalf("重复 submitted 应幂等，实际 %s", part.UserInputStatus)
	}

	// terminal 后迟到 submitted 丢弃。
	r2 := newTestReducer()
	r2.Apply(requestedEvFor(1, "claude", "ui-late", "T1", "pending", true, true))
	r2.Apply(userEv(2, "claude", "turn_aborted", "ui-late", "T1", map[string]interface{}{"reason": "process_death"}))
	r2.Apply(userEv(3, "claude", "user_input_submitted", "ui-late", "T1", nil))
	part2, _ := findSingleUserInputPart(t, r2, "claude", "ui-late")
	if part2.UserInputStatus != "pending" || part2.UserInputDiagnosticCode != "turn_terminated" {
		t.Fatalf("terminal 后迟到 submitted 应丢弃，实际 status=%s diag=%q", part2.UserInputStatus, part2.UserInputDiagnosticCode)
	}

	// resolved 后迟到 submitted 丢弃。
	r3 := newTestReducer()
	r3.Apply(requestedEvFor(1, "claude", "ui-late2", "T1", "pending", true, true))
	r3.Apply(userEv(2, "claude", "user_input_resolved", "ui-late2", "T1", map[string]interface{}{"status": "answered", "source": "ios"}))
	r3.Apply(userEv(3, "claude", "user_input_submitted", "ui-late2", "T1", nil))
	part3, _ := findSingleUserInputPart(t, r3, "claude", "ui-late2")
	if part3.UserInputStatus != "answered" {
		t.Fatalf("resolved 后迟到 submitted 应丢弃，实际 %s", part3.UserInputStatus)
	}
}

// TestReducerTerminalMarksPendingAndSubmitted：turn 权威终止时 pending/submitted
// user_input → pending+turn_terminated；resolved 不动；幂等。
func TestReducerTerminalMarksPendingAndSubmitted(t *testing.T) {
	r := newTestReducer()
	r.Apply(requestedEvFor(1, "claude", "ui-t1", "T1", "pending", true, true))
	r.Apply(requestedEvFor(2, "claude", "ui-t2", "T2", "pending", true, true))
	r.Apply(userEv(3, "claude", "user_input_submitted", "ui-t2", "T2", nil))
	r.Apply(requestedEvFor(4, "claude", "ui-t3", "T3", "pending", true, true))
	r.Apply(userEv(5, "claude", "user_input_resolved", "ui-t3", "T3", map[string]interface{}{"status": "answered", "source": "backend"}))

	// 三个 turn 分别终止。
	r.Apply(userEv(6, "claude", "turn_aborted", "", "T1", map[string]interface{}{"reason": "process_death"}))
	r.Apply(userEv(7, "claude", "turn_error", "", "T2", map[string]interface{}{"message": "boom"}))
	r.Apply(userEv(8, "claude", "turn_completed", "", "T3", nil))

	p1, _ := findSingleUserInputPart(t, r, "claude", "ui-t1")
	if p1.UserInputStatus != "pending" || p1.UserInputDiagnosticCode != "turn_terminated" {
		t.Fatalf("pending → terminal 失败：status=%s diag=%q", p1.UserInputStatus, p1.UserInputDiagnosticCode)
	}
	if p1.UserInputCanRespond || p1.UserInputCanReject {
		t.Fatal("terminal 后 capability 应全 false")
	}
	p2, _ := findSingleUserInputPart(t, r, "claude", "ui-t2")
	if p2.UserInputStatus != "pending" || p2.UserInputDiagnosticCode != "turn_terminated" {
		t.Fatalf("submitted → terminal 失败：status=%s diag=%q", p2.UserInputStatus, p2.UserInputDiagnosticCode)
	}
	p3, _ := findSingleUserInputPart(t, r, "claude", "ui-t3")
	if p3.UserInputStatus != "answered" {
		t.Fatalf("terminal 不得覆盖 resolved，实际 %s", p3.UserInputStatus)
	}
}

// TestReducerLateResolvedSupersedesTerminal：terminal 后迟到 transcript resolved
// 可 supersede（resolved 是唯一耐久证据）。
func TestReducerLateResolvedSupersedesTerminal(t *testing.T) {
	r := newTestReducer()
	r.Apply(requestedEvFor(1, "claude", "ui-sup", "T1", "pending", true, true))
	r.Apply(userEv(2, "claude", "turn_aborted", "", "T1", map[string]interface{}{"reason": "process_death"}))
	r.Apply(userEv(3, "claude", "user_input_resolved", "ui-sup", "T1", map[string]interface{}{"status": "answered", "source": "backend"}))

	part, _ := findSingleUserInputPart(t, r, "claude", "ui-sup")
	if part.UserInputStatus != "answered" || part.UserInputResolutionSource != "backend" {
		t.Fatalf("迟到 resolved 应 supersede terminal：status=%s source=%q", part.UserInputStatus, part.UserInputResolutionSource)
	}
	if part.UserInputDiagnosticCode != "" {
		t.Fatalf("resolved 应清 diagnostic，实际 %q", part.UserInputDiagnosticCode)
	}
}

// TestReducerRequestedMonotonicCapsAndAuthority：requested 重放对 pending 单调 OR
// capability；对 terminal/submitted/resolved 不降级。
func TestReducerRequestedMonotonicCapsAndAuthority(t *testing.T) {
	// pending：caps 单调 OR（第二次 requested caps=false 不清掉第一次的 true）。
	r := newTestReducer()
	r.Apply(requestedEvFor(1, "claude", "ui-mono", "T1", "pending", true, true))
	r.Apply(requestedEvFor(2, "claude", "ui-mono", "T1", "pending", false, false))
	part, _ := findSingleUserInputPart(t, r, "claude", "ui-mono")
	if !part.UserInputCanRespond || !part.UserInputCanReject {
		t.Fatalf("caps 应单调 OR：canRespond=%v canReject=%v", part.UserInputCanRespond, part.UserInputCanReject)
	}

	// terminal：requested 重放保持 terminal。
	r2 := newTestReducer()
	r2.Apply(requestedEvFor(1, "claude", "ui-keep", "T1", "pending", false, false))
	r2.Apply(userEv(2, "claude", "turn_aborted", "", "T1", nil))
	r2.Apply(requestedEvFor(3, "claude", "ui-keep", "T1", "pending", true, true))
	part2, _ := findSingleUserInputPart(t, r2, "claude", "ui-keep")
	if part2.UserInputDiagnosticCode != "turn_terminated" || part2.UserInputCanRespond {
		t.Fatalf("terminal 后 requested 不得复活：diag=%q canRespond=%v", part2.UserInputDiagnosticCode, part2.UserInputCanRespond)
	}

	// submitted：requested 重放保持 submitted。
	r3 := newTestReducer()
	r3.Apply(requestedEvFor(1, "claude", "ui-keep2", "T1", "pending", true, true))
	r3.Apply(userEv(2, "claude", "user_input_submitted", "ui-keep2", "T1", nil))
	r3.Apply(requestedEvFor(3, "claude", "ui-keep2", "T1", "pending", true, true))
	part3, _ := findSingleUserInputPart(t, r3, "claude", "ui-keep2")
	if part3.UserInputStatus != "submitted" {
		t.Fatalf("submitted 后 requested 不得降级，实际 %s", part3.UserInputStatus)
	}
}

// TestReducerHistoricalDualTurnCopiesConverge：同 interaction 的 dual-turn 副本
//（live turn + 冷读 turn）在 requested 后收敛：canonical turn 保留合并字段，
// 另一 turn 删除副本——一 interaction 一卡。
func TestReducerHistoricalDualTurnCopiesConverge(t *testing.T) {
	r := newTestReducer()
	// 冷读 turn 的 requested（observe_only）。
	r.Apply(requestedEvFor(1, "claude", "ui-dual", "cold-turn", "pending", false, false))
	// live turn 的 requested（可答）。
	r.Apply(requestedEvFor(2, "claude", "ui-dual", "live-turn", "pending", true, true))

	// 收敛：一 interaction 一卡，caps 单调 OR。
	part, turnID := findSingleUserInputPart(t, r, "claude", "ui-dual")
	if turnID != "cold-turn" && turnID != "live-turn" {
		t.Fatalf("canonical turn 异常：%s", turnID)
	}
	if !part.UserInputCanRespond || !part.UserInputCanReject {
		t.Fatalf("合并后 caps 应 OR：canRespond=%v canReject=%v", part.UserInputCanRespond, part.UserInputCanReject)
	}

	// 高权威副本胜出：冷读 turn resolved + live turn 仍 pending → 收敛为 answered。
	r2 := newTestReducer()
	r2.Apply(requestedEvFor(1, "claude", "ui-dual2", "cold-turn", "pending", false, false))
	r2.Apply(requestedEvFor(2, "claude", "ui-dual2", "live-turn", "pending", true, true))
	r2.Apply(userEv(3, "claude", "user_input_resolved", "ui-dual2", "cold-turn", map[string]interface{}{"status": "answered", "source": "backend"}))
	part2, _ := findSingleUserInputPart(t, r2, "claude", "ui-dual2")
	if part2.UserInputStatus != "answered" {
		t.Fatalf("合并应取 resolved 副本，实际 %s", part2.UserInputStatus)
	}
}

// TestReducerRestoreConvergesBaselineCopies：hydrate Restore（baseline 含双副本）
// 后收敛——冷开双副本快照合并为一卡。
func TestReducerRestoreConvergesBaselineCopies(t *testing.T) {
	r := newTestReducer()
	baseline := SessionProjection{
		SessionID: "s1",
		Execution: ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{
			{
				TurnID: "turn-a", Status: "completed",
				Assistant: &MessageProjection{ID: "turn-a", Role: "assistant", Parts: []ProjectionPart{
					{Type: "user_input", UserInputInteractionID: "ui-restore", UserInputStatus: "answered", UserInputResolutionSource: "backend"},
				}},
			},
			{
				TurnID: "turn-b", Status: "completed",
				Assistant: &MessageProjection{ID: "turn-b", Role: "assistant", Parts: []ProjectionPart{
					{Type: "user_input", UserInputInteractionID: "ui-restore", UserInputStatus: "pending"},
				}},
			},
		},
	}
	r.Restore("claude", "s1", baseline)

	part, turnID := findSingleUserInputPart(t, r, "claude", "ui-restore")
	if part.UserInputStatus != "answered" {
		t.Fatalf("Restore 收敛应取 resolved 副本，实际 %s", part.UserInputStatus)
	}
	if turnID != "turn-a" {
		t.Fatalf("canonical turn 应为首个携带 turn，实际 %s", turnID)
	}
}

// TestUserInputPartRankPriority：selector 优先级 resolved > terminal > submitted > pending。
func TestUserInputPartRankPriority(t *testing.T) {
	cases := []struct {
		part ProjectionPart
		want int
	}{
		{ProjectionPart{UserInputStatus: "answered"}, 4},
		{ProjectionPart{UserInputStatus: "rejected"}, 4},
		{ProjectionPart{UserInputStatus: "pending", UserInputDiagnosticCode: "turn_terminated"}, 3},
		{ProjectionPart{UserInputStatus: "submitted"}, 2},
		{ProjectionPart{UserInputStatus: "pending"}, 1},
		{ProjectionPart{UserInputStatus: "failed"}, 4},
	}
	for i, c := range cases {
		if got := userInputPartRank(c.part); got != c.want {
			t.Fatalf("case %d: rank(%s/%s)=%d, want %d", i, c.part.UserInputStatus, c.part.UserInputDiagnosticCode, got, c.want)
		}
	}
}
