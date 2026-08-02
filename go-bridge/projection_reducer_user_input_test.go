package gobridge

import "testing"

// projection_reducer_user_input_test.go 锁定结构化用户输入（design §10）的 reducer 行为与
// SSV2 红线：单 part 原地 upsert（不产生第二张 answered 卡）、phase 推导（requires_action↔running↔idle）、
// 幂等重放、identityless 帧丢弃、FlushPatch upsert_user_input PartOp、snapshot/restore 深拷贝。
//
// 这些测试只驱动 ProjectionReducer.Apply/Snapshot/FlushPatch/Restore，不依赖任何 adapter；
// events.go 的 wire 映射由 projection_live_turnid_test.go / 端到端测试覆盖。

// uiQuestionsWire 是 reducer 经 data["questions"] 收到的典型 wire 形态（与 events.go
// userInputQuestionsToWire 输出一致）：[]any 的 map，含 id/prompt/answerMode/options/...。
func uiQuestionsWire() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"id":                 "ui_x_q_0",
			"prompt":             "Pick a color",
			"answerMode":         "single",
			"header":             "Color",
			"options":            []interface{}{map[string]interface{}{"id": "ui_x_q_0_o_0", "label": "Red"}},
			"required":           true,
			"isSecret":           false,
			"allowsCustomAnswer": false,
		},
	}
}

// requestedEv 构造一次 user_input_requested EventMessage（pending 或 failed）。
func requestedEv(seq int, status string, canRespond, canReject bool) EventMessage {
	return ev(seq, "codex", "s1", "user_input_requested", map[string]interface{}{
		"turnId":        "T1",
		"interactionId": "ui_x",
		"status":        status,
		"questions":     uiQuestionsWire(),
		"canRespond":    canRespond,
		"canReject":     canReject,
		"expiresAt":     int64(1700000010000),
	})
}

// TestReducerUserInputRequestedProjectsPartAndRequiresAction：requested(pending) 在该 turn 的
// assistant message 上落一个 user_input part，status=pending，保留 questions/canRespond/canReject，
// execution.phase=requires_action。
func TestReducerUserInputRequestedProjectsPartAndRequiresAction(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(requestedEv(2, "pending", true, true))

	proj, ok := r.Snapshot("codex", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	if proj.Execution.Phase != "requires_action" || proj.Execution.ActiveTurnID != "T1" {
		t.Fatalf("execution = %+v, want requires_action/T1", proj.Execution)
	}
	turn := proj.Turns[0]
	idx := findUserInputPart(turn.Assistant, "ui_x")
	if idx < 0 {
		t.Fatalf("missing user_input part; parts=%+v", turn.Assistant.Parts)
	}
	p := turn.Assistant.Parts[idx]
	if p.Type != "user_input" || p.UserInputStatus != "pending" {
		t.Fatalf("part = %+v, want user_input/pending", p)
	}
	if !p.UserInputCanRespond || !p.UserInputCanReject {
		t.Fatalf("canRespond=%v canReject=%v, want both true", p.UserInputCanRespond, p.UserInputCanReject)
	}
	if p.UserInputExpiresAt != 1700000010000 {
		t.Fatalf("expiresAt = %d, want 1700000010000", p.UserInputExpiresAt)
	}
	if p.UserInputQuestions == nil {
		t.Fatal("questions not preserved on part")
	}
}

func TestReducerUserInputInteractionIdentityPreventsHydrateLivePhantomTurn(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "claude", "s1", "turn_started", map[string]interface{}{"turnId": "hydrate-user-turn"}))
	r.Apply(ev(2, "claude", "s1", "user_input_requested", map[string]interface{}{
		"turnId": "hydrate-user-turn", "interactionId": "ui_same", "status": "pending",
		"questions": uiQuestionsWire(), "canRespond": false, "canReject": false,
	}))
	// The pending live event for the same tool_use carries Claude's assistant message id.
	r.Apply(ev(3, "claude", "s1", "user_input_requested", map[string]interface{}{
		"turnId": "assistant-message-id", "interactionId": "ui_same", "status": "pending",
		"questions": uiQuestionsWire(), "canRespond": true, "canReject": true,
	}))
	// Resolution reuses the adapter-captured assistant identity, but must close the existing part.
	r.Apply(ev(4, "claude", "s1", "user_input_resolved", map[string]interface{}{
		"turnId": "assistant-message-id", "interactionId": "ui_same", "status": "answered", "source": "ios",
	}))

	projection, ok := r.Snapshot("claude", "s1")
	if !ok {
		t.Fatal("missing projection")
	}
	parts := 0
	for _, turn := range projection.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type == "user_input" && part.UserInputInteractionID == "ui_same" {
				parts++
				if turn.TurnID != "hydrate-user-turn" || part.UserInputStatus != "answered" || !part.UserInputCanRespond {
					t.Fatalf("interaction not updated in place: turn=%s part=%+v", turn.TurnID, part)
				}
			}
		}
	}
	if parts != 1 {
		t.Fatalf("interaction projected %d times, want exactly one", parts)
	}
}

// TestReducerUserInputResolvedUpsertsInPlaceNoSecondCard：requested→resolved(answered) 只产生一个
// part（status=answered, source, resolvedAt），不追加第二张卡；turn 仍 running 时 phase 回到 running。
func TestReducerUserInputResolvedUpsertsInPlaceNoSecondCard(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(requestedEv(2, "pending", true, true))
	r.Apply(ev(3, "codex", "s1", "user_input_resolved", map[string]interface{}{
		"turnId":        "T1",
		"interactionId": "ui_x",
		"status":        "answered",
		"source":        "ios",
		"resolvedAt":    int64(1700000020000),
	}))

	proj, _ := r.Snapshot("codex", "s1")
	turn := proj.Turns[0]
	// 反双写红线：恰好一个 user_input part。
	count := 0
	for _, p := range turn.Assistant.Parts {
		if p.Type == "user_input" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("user_input part count = %d, want 1 (anti-double-write)", count)
	}
	idx := findUserInputPart(turn.Assistant, "ui_x")
	p := turn.Assistant.Parts[idx]
	if p.UserInputStatus != "answered" {
		t.Fatalf("status = %q, want answered", p.UserInputStatus)
	}
	if p.UserInputResolutionSource != "ios" {
		t.Fatalf("source = %q, want ios", p.UserInputResolutionSource)
	}
	if p.UserInputResolvedAt != 1700000020000 {
		t.Fatalf("resolvedAt = %d, want 1700000020000", p.UserInputResolvedAt)
	}
	if proj.Execution.Phase != "running" {
		t.Fatalf("phase = %q, want running after resolve on live turn", proj.Execution.Phase)
	}
}

// TestReducerUserInputResolvedOnCompletedTurnLeavesIdle：resolved 到达时 turn 已 completed（重放/补收），
// phase 保持 idle，不复活执行态。
func TestReducerUserInputResolvedOnCompletedTurnLeavesIdle(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(requestedEv(2, "pending", true, true))
	r.Apply(ev(3, "codex", "s1", "user_input_resolved", map[string]interface{}{
		"turnId": "T1", "interactionId": "ui_x", "status": "answered", "source": "ios",
	}))
	r.Apply(ev(4, "codex", "s1", "turn_completed", map[string]interface{}{"turnId": "T1"}))
	// 重放 resolved（无 new info）：phase 必须保持 idle。
	r.Apply(ev(5, "codex", "s1", "user_input_resolved", map[string]interface{}{
		"turnId": "T1", "interactionId": "ui_x", "status": "answered", "source": "ios",
	}))

	proj, _ := r.Snapshot("codex", "s1")
	if proj.Execution.Phase != "idle" {
		t.Fatalf("phase = %q, want idle on completed turn", proj.Execution.Phase)
	}
}

// TestReducerUserInputReplayRequestedIsIdempotent：同一 interactionId 的 requested 重放（重连/补发）
// 原地 upsert，不产生第二张 pending 卡，也不降级已 resolved 的交互。
func TestReducerUserInputReplayRequestedIsIdempotent(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(requestedEv(2, "pending", true, true))
	before, _ := r.Snapshot("codex", "s1")
	r.Apply(requestedEv(3, "pending", true, true)) // 重放
	after, _ := r.Snapshot("codex", "s1")

	count := 0
	for _, p := range after.Turns[0].Assistant.Parts {
		if p.Type == "user_input" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("replay produced %d parts, want 1", count)
	}
	// 重放 requested 不应回退已 resolved（这里仍是 pending，但验证不重复）。
	if before.Turns[0].Assistant.Parts[findUserInputPart(before.Turns[0].Assistant, "ui_x")].UserInputStatus !=
		after.Turns[0].Assistant.Parts[findUserInputPart(after.Turns[0].Assistant, "ui_x")].UserInputStatus {
		t.Fatal("replay changed status unexpectedly")
	}
}

// TestReducerUserInputFailedProjectsOnce：malformed questions 的 failed requested 仍投影一次，
// canRespond=false，但不创建可点击 UI（reducer 不区分，由消费端按 canRespond 渲染）。
func TestReducerUserInputFailedProjectsOnce(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "user_input_requested", map[string]interface{}{
		"turnId":         "T1",
		"interactionId":  "ui_bad",
		"status":         "failed",
		"canRespond":     false,
		"canReject":      false,
		"diagnosticCode": "invalid_backend_request",
	}))

	proj, _ := r.Snapshot("codex", "s1")
	idx := findUserInputPart(proj.Turns[0].Assistant, "ui_bad")
	if idx < 0 {
		t.Fatal("failed interaction should still project once")
	}
	p := proj.Turns[0].Assistant.Parts[idx]
	if p.UserInputStatus != "failed" || p.UserInputCanRespond {
		t.Fatalf("failed part = %+v, want status=failed/canRespond=false", p)
	}
	if p.UserInputDiagnosticCode != "invalid_backend_request" {
		t.Fatalf("diagnosticCode = %q", p.UserInputDiagnosticCode)
	}
	// failed 不阻塞执行：无 pending（pending 专指 status=pending）→ turn running。
	if proj.Execution.Phase != "running" {
		t.Fatalf("phase = %q, want running (failed is not pending)", proj.Execution.Phase)
	}
}

// TestReducerMultipleInteractionsRequireActionUntilAllResolved：同一 turn 上多个 pending interaction，
// 逐一 resolve，全部 resolved 前保持 requires_action，最后一个 resolve 后回到 running。
func TestReducerMultipleInteractionsRequireActionUntilAllResolved(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "user_input_requested", map[string]interface{}{
		"turnId": "T1", "interactionId": "ui_a", "status": "pending", "canRespond": true, "canReject": true,
	}))
	r.Apply(ev(3, "codex", "s1", "user_input_requested", map[string]interface{}{
		"turnId": "T1", "interactionId": "ui_b", "status": "pending", "canRespond": true, "canReject": true,
	}))
	proj, _ := r.Snapshot("codex", "s1")
	if proj.Execution.Phase != "requires_action" {
		t.Fatalf("phase = %q, want requires_action with 2 pending", proj.Execution.Phase)
	}
	// 解掉第一个，仍有一个 pending → requires_action。
	r.Apply(ev(4, "codex", "s1", "user_input_resolved", map[string]interface{}{
		"turnId": "T1", "interactionId": "ui_a", "status": "answered", "source": "ios",
	}))
	proj, _ = r.Snapshot("codex", "s1")
	if proj.Execution.Phase != "requires_action" {
		t.Fatalf("phase = %q, want requires_action with 1 pending", proj.Execution.Phase)
	}
	// 全部解掉 → running（turn 仍 running）。
	r.Apply(ev(5, "codex", "s1", "user_input_resolved", map[string]interface{}{
		"turnId": "T1", "interactionId": "ui_b", "status": "answered", "source": "ios",
	}))
	proj, _ = r.Snapshot("codex", "s1")
	if proj.Execution.Phase != "running" {
		t.Fatalf("phase = %q, want running after all resolved", proj.Execution.Phase)
	}
}

// TestReducerUserInputIdentitylessRequestedSkipped：缺 turnId 或 interactionId 的 requested 帧是
// identityless 的，必须被丢弃，不创建幽灵 turn、不提交 revision（SSV2 红线：没有第二路径）。
func TestReducerUserInputIdentitylessRequestedSkipped(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	before, _ := r.Snapshot("codex", "s1")
	// 缺 interactionId。
	r.Apply(ev(2, "codex", "s1", "user_input_requested", map[string]interface{}{
		"turnId": "T1", "status": "pending",
	}))
	// 缺 turnId。
	r.Apply(ev(3, "codex", "s1", "user_input_requested", map[string]interface{}{
		"interactionId": "ui_x", "status": "pending",
	}))
	after, _ := r.Snapshot("codex", "s1")
	if after.SyncRev != before.SyncRev {
		t.Fatalf("identityless requested bumped SyncRev %d→%d (should not commit)", before.SyncRev, after.SyncRev)
	}
	if findUserInputPart(after.Turns[0].Assistant, "ui_x") >= 0 {
		t.Fatal("identityless requested created a part")
	}
}

// TestReducerUserInputResolvedWithoutRequestDropped：没有对应 requested part 的 resolved 是陈旧/
// 不可归因的，必须丢弃，不凭空制造 part（SSV2 红线：无第二裁判/不补洞）。
func TestReducerUserInputResolvedWithoutRequestDropped(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "user_input_resolved", map[string]interface{}{
		"turnId": "T1", "interactionId": "ghost", "status": "answered", "source": "ios",
	}))
	proj, _ := r.Snapshot("codex", "s1")
	if findUserInputPart(proj.Turns[0].Assistant, "ghost") >= 0 {
		t.Fatal("stale resolved fabricated a user_input part")
	}
}

// TestReducerFlushPatchEmitsUserInputPartOpAndClears：requested 后 FlushPatch 产出一个
// upsert_user_input PartOp（turnId/messageId = owning turn），accumulator 清空使第二次 flush 为空。
func TestReducerFlushPatchEmitsUserInputPartOpAndClears(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(requestedEv(2, "pending", true, true))

	patch, ok := r.FlushPatch("codex", "s1")
	if !ok {
		t.Fatal("first flush should produce a patch")
	}
	var op *PartOp
	for i := range patch.PartOps {
		if patch.PartOps[i].Op == "upsert_user_input" {
			op = &patch.PartOps[i]
			break
		}
	}
	if op == nil {
		t.Fatalf("no upsert_user_input PartOp in patch: %+v", patch.PartOps)
	}
	if op.TurnID != "T1" || op.MessageID != "T1" {
		t.Fatalf("PartOp turn/message = %q/%q, want T1/T1", op.TurnID, op.MessageID)
	}
	if op.Part == nil || op.Part.UserInputInteractionID != "ui_x" {
		t.Fatalf("PartOp part = %+v", op.Part)
	}
	// execution 变化（requires_action）也应在 patch 里。
	if patch.Execution == nil || patch.Execution.Phase != "requires_action" {
		t.Fatalf("patch execution = %+v, want requires_action", patch.Execution)
	}
	// 第二次 flush（无新事件）应为空。
	if _, ok := r.FlushPatch("codex", "s1"); ok {
		t.Fatal("second flush with no new events should be empty")
	}
}

// TestReducerSnapshotDeepCopiesUserInputQuestions：Snapshot 深拷贝 questions，外部修改快照不影响
// reducer 后续状态（避免别名导致后续 reduce 污染已发出的快照/patch）。
func TestReducerSnapshotDeepCopiesUserInputQuestions(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(requestedEv(2, "pending", true, true))

	snap1, _ := r.Snapshot("codex", "s1")
	idx := findUserInputPart(snap1.Turns[0].Assistant, "ui_x")
	// 篡改快照里的 questions。
	if qs, ok := snap1.Turns[0].Assistant.Parts[idx].UserInputQuestions.([]interface{}); ok && len(qs) > 0 {
		if qm, ok := qs[0].(map[string]interface{}); ok {
			qm["prompt"] = "TAMPERED"
		}
	}
	// 再次 snapshot，questions 必须未被污染。
	snap2, _ := r.Snapshot("codex", "s1")
	idx2 := findUserInputPart(snap2.Turns[0].Assistant, "ui_x")
	if qm, _ := snap2.Turns[0].Assistant.Parts[idx2].UserInputQuestions.([]interface{})[0].(map[string]interface{}); qm["prompt"] == "TAMPERED" {
		t.Fatal("Snapshot did not deep-copy questions; reducer state aliased snapshot")
	}
}

// TestReducerRestorePreservesUserInputParts：Restore 一个含 user_input part 的 checkpoint 后，part
// 完整存活（冷启动/重启诚实显示最后状态，design §10.3）。
func TestReducerRestorePreservesUserInputParts(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(requestedEv(2, "pending", true, true))
	checkpoint, _ := r.Snapshot("codex", "s1")

	// 模拟进程重启：新 reducer，Restore checkpoint。
	r2 := newTestReducer()
	r2.Restore("codex", "s1", checkpoint)
	restored, ok := r2.Snapshot("codex", "s1")
	if !ok {
		t.Fatal("restored projection missing")
	}
	idx := findUserInputPart(restored.Turns[0].Assistant, "ui_x")
	if idx < 0 {
		t.Fatal("user_input part lost across Restore/checkpoint")
	}
	p := restored.Turns[0].Assistant.Parts[idx]
	if p.UserInputStatus != "pending" || !p.UserInputCanRespond {
		t.Fatalf("restored part = %+v, want pending/canRespond=true", p)
	}
}
