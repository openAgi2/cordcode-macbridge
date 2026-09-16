package claudecode

// user_input_identity_test.go 锁定 Claude AskUserQuestion 双 identity 契约（设计 v6 §2.1/§4.1，R5-P0-1）。
//
// 证据基础：三份真实 Claude Code 2.1.209 配对 fixture（testdata/structured_user_input/）
// 均同时携带外层 control request_id 与内层 request.tool_use_id，且三组都不相等。
// 本文件断言：
//  1. 真实 fixture 的 request_id != tool_use_id（不得互相 fallback）；
//  2. live requested、transcript requested（mapper 派生）、submitted、transcript resolved
//     四个 producer 全部以 tool_use_id 派生同一 interactionId（一 interaction 一卡的前提）；
//  3. 缺 tool_use_id / 缺 request_id 均 fail closed：不发 canonical requested，requestID
//     可用时回 deny，绝不回退用另一 identity 猜测；
//  4. registry byRequest 索引仍按 request_id 配对 control response（写回路径不断）。

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestClaudePairedFixtureDualIdentityDistinct：三份真实 2.1.209 fixture 的
// request_id 与 tool_use_id 都存在且互不相等——双 identity 不可互换的实证。
func TestClaudePairedFixtureDualIdentityDistinct(t *testing.T) {
	for _, name := range []string{"single_paired.json", "multi_select_paired.json", "multi_question_paired.json"} {
		paired := loadClaudePaired(t, name)
		req, _ := paired["control_request"].(map[string]any)
		requestID, _ := req["request_id"].(string)
		request, _ := req["request"].(map[string]any)
		toolUseID, _ := request["tool_use_id"].(string)
		if requestID == "" || toolUseID == "" {
			t.Fatalf("%s 双 identity 缺失：request_id=%q tool_use_id=%q", name, requestID, toolUseID)
		}
		if requestID == toolUseID {
			t.Fatalf("%s request_id 与 tool_use_id 相等（%q）——与真实样本矛盾", name, requestID)
		}
	}
}

// TestClaudeFourProducersShareToolUseIdentity：live requested（handleAskUserQuestionV2）、
// transcript requested（DeriveStructuredUserInputInteractionID，transcript mapper 同一函数）、
// submitted、transcript resolved 四个 producer 都以 tool_use_id 派生同一 interactionId。
// submitted/resolved 的 identity 由 registry entry / resolve 路径携带——它们与 live
// requested 同源即证明不会分叉成两张卡。
func TestClaudeFourProducersShareToolUseIdentity(t *testing.T) {
	for _, name := range []string{"single_paired.json", "multi_select_paired.json", "multi_question_paired.json"} {
		paired := loadClaudePaired(t, name)
		req, _ := paired["control_request"].(map[string]any)
		requestID, _ := req["request_id"].(string)
		request, _ := req["request"].(map[string]any)
		toolUseID, _ := request["tool_use_id"].(string)

		// transcript mapper 派生（cold/hydrate requested 与 resolved 的 key）。
		transcriptIdentity := DeriveStructuredUserInputInteractionID(toolUseID)

		// live requested 派生：跑真实 handler，取事件里的 interactionId。
		cs, _ := newAskTestSession(t)
		cs.handleControlRequest(makeAskControlRequestWithToolUse(requestID, toolUseID, pairedQuestions(paired)))
		ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
		if ev == nil || ev.UserInput == nil {
			t.Fatalf("%s live requested 未发出", name)
		}
		liveIdentity := ev.UserInput.InteractionID
		if liveIdentity != transcriptIdentity {
			t.Fatalf("%s live requested identity %q != transcript mapper identity %q（identity 分叉）",
				name, liveIdentity, transcriptIdentity)
		}
		if ev.ItemID != toolUseID {
			t.Fatalf("%s live requested ItemID=%q，应为 tool_use_id %q（与 transcript mapper 对齐）",
				name, ev.ItemID, toolUseID)
		}

		// registry：live 注册的 entry 必须能被 transcript 派生的 interactionId 命中
		//（oracle / submitted / resolved 的查询键），且 byRequest 仍按 request_id 配对。
		if got := cs.claudeUserInputReg.Status(transcriptIdentity); got != claudeUIPending {
			t.Fatalf("%s registry 按 tool-use identity 查询=%v，应为 pending", name, got)
		}
		if snap, status := cs.claudeUserInputReg.SnapshotByRequest(requestID); status != claudeUIPending || snap == nil || snap.requestID != requestID {
			t.Fatalf("%s registry byRequest 配对断裂：status=%v snap=%+v", name, status, snap)
		}
		if snap, _ := cs.claudeUserInputReg.SnapshotByRequest(requestID); snap.toolUseID != toolUseID {
			t.Fatalf("%s registry entry toolUseID=%q，应为 %q", name, snap.toolUseID, toolUseID)
		}

		// submitted / resolved producer：resolve 路径（control write 成功后）携带的
		// interactionId 与 turn/item 归属必须同键。这里直接验证 registry entry 的
		// interactionID 字段（submitted 事件与 resolved 事件的 identity 来源）。
		if snap, _ := cs.claudeUserInputReg.SnapshotByRequest(requestID); snap.interactionID != transcriptIdentity {
			t.Fatalf("%s registry entry interactionID=%q != transcript identity %q",
				name, snap.interactionID, transcriptIdentity)
		}
	}
}

// TestClaudeMissingToolUseIDFailsClosed：缺 tool_use_id 时 fail closed——
// 不发 canonical requested（无 user_input 事件），requestID 可用时回 deny；
// 不得回退用 request_id 派生 identity。
func TestClaudeMissingToolUseIDFailsClosed(t *testing.T) {
	cs, stdin := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequestWithToolUse("req-no-tool", "", []any{
		singleQuestionMap("Pick a color", "Color", false, [2]string{"red", "r"}),
	}))

	evs := drainAllEvents(cs)
	if ev := findUserInputEvent(evs, core.EventUserInputRequested); ev != nil {
		t.Fatalf("缺 tool_use_id 不得发 canonical requested，实际发出 interactionId=%q", ev.UserInput.InteractionID)
	}
	// deny 回执必须落在 request_id 上（避免 CLI 永久等待）。
	deny := stdin.lastJSONLine(t)
	if deny["type"] != "control_response" {
		t.Fatalf("缺 tool_use_id 应回 control_response，实际 %v", deny["type"])
	}
	inner, _ := deny["response"].(map[string]any)
	if rid, _ := inner["request_id"].(string); rid != "req-no-tool" {
		t.Fatalf("deny 回执 response.request_id=%v，应为 req-no-tool", inner["request_id"])
	}
	perm, _ := inner["response"].(map[string]any)
	if perm["behavior"] != "deny" {
		t.Fatalf("deny 回执 behavior=%v，应为 deny", perm["behavior"])
	}
	// registry 不得出现以 request_id 派生的 interaction（不得 fallback）。
	fallback := deriveClaudeInteractionID("req-no-tool")
	if got := cs.claudeUserInputReg.Status(fallback); got != claudeUIAbsent {
		t.Fatalf("不得以 request_id 派生 identity 注册 entry，实际 status=%v", got)
	}
}

// TestClaudeMissingRequestIDFailsClosed：缺 request_id（无法回 control response）时
// 不发 canonical requested、不注册 entry——无法作答的 Ask 只留 transcript 侧投影。
func TestClaudeMissingRequestIDFailsClosed(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequestWithToolUse("", "toolu-no-req", []any{
		singleQuestionMap("Pick a color", "Color", false, [2]string{"red", "r"}),
	}))

	if ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested); ev != nil {
		t.Fatalf("缺 request_id 不得发 canonical requested，实际发出 interactionId=%q", ev.UserInput.InteractionID)
	}
	identity := DeriveStructuredUserInputInteractionID("toolu-no-req")
	if got := cs.claudeUserInputReg.Status(identity); got != claudeUIAbsent {
		t.Fatalf("缺 request_id 不得注册 entry，实际 status=%v", got)
	}
}

// TestClaudeLiveRequestedObserveOnlyUntilGate：证据门（claudeAskAnsweringEnabled 默认
// false）未翻转前，live requested 保持 observe_only（canRespond/canReject=false）；
// 门翻转后同一 registry pending + session alive 才可答。
func TestClaudeLiveRequestedObserveOnlyUntilGate(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-gate", []any{
		singleQuestionMap("Pick a color", "Color", false, [2]string{"red", "r"}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev == nil || ev.UserInput == nil {
		t.Fatal("expected canonical requested event")
	}
	if ev.UserInput.CanRespond || ev.UserInput.CanReject {
		t.Fatalf("证据门未过：canRespond/canReject 应为 false，实际 %v/%v",
			ev.UserInput.CanRespond, ev.UserInput.CanReject)
	}
	if ev.UserInput.DiagnosticCode != "observe_only" {
		t.Fatalf("诊断码应为 observe_only，实际 %q", ev.UserInput.DiagnosticCode)
	}

	// 门翻转后（测试专用翻转）：仍 pending + alive → 可答。
	withClaudeAskAnswering(true, func() {
		cs.handleControlRequest(makeAskControlRequest("req-gate2", []any{
			singleQuestionMap("Pick again", "Color", false, [2]string{"red", "r"}),
		}))
	})
	ev2 := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev2 == nil || ev2.UserInput == nil {
		t.Fatal("expected canonical requested event after gate flip")
	}
	if !ev2.UserInput.CanRespond || !ev2.UserInput.CanReject {
		t.Fatalf("门翻转 + pending + alive：canRespond/canReject 应为 true，实际 %v/%v",
			ev2.UserInput.CanRespond, ev2.UserInput.CanReject)
	}
}
