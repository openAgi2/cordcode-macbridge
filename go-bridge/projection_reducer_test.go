package gobridge

import (
	"testing"
)

// ev builds a stamped EventMessage shaped like the Codex rollout path feeds into PublishLogical.
func ev(seq int, backend, session, event string, data map[string]interface{}) EventMessage {
	return EventMessage{
		BackendID:     backend,
		SessionID:     session,
		Event:         event,
		Data:          data,
		PerSessionSeq: seq,
		BridgeEpoch:   "epoch-test",
	}
}

func newTestReducer() *ProjectionReducer {
	r := NewProjectionReducer()
	r.now = func() int64 { return 1700000000000 } // deterministic epoch-ms
	return r
}

// TestReducerTurnLifecycleAppendComplete: turn_started + text deltas + turn_completed yields a
// completed turn whose assistant text is the concatenation of the deltas, and execution=idle.
func TestReducerTurnLifecycleAppendComplete(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "Hello"}))
	r.Apply(ev(3, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": " world"}))
	r.Apply(ev(4, "codex", "s1", "turn_completed", map[string]interface{}{"turnId": "T1"}))

	proj, ok := r.Snapshot("codex", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	if proj.SyncRev != 3 {
		t.Fatalf("SyncRev = %d, want 3 (turn_started no longer commits)", proj.SyncRev)
	}
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
		t.Fatalf("turns = %+v", proj.Turns)
	}
	tu := proj.Turns[0]
	if tu.Status != "completed" {
		t.Fatalf("status = %q, want completed", tu.Status)
	}
	if tu.Assistant == nil || len(tu.Assistant.Parts) == 0 {
		t.Fatalf("missing assistant parts: %+v", tu.Assistant)
	}
	var text string
	for _, p := range tu.Assistant.Parts {
		if p.Type == "text" {
			text += p.Text
		}
	}
	if text != "Hello world" {
		t.Fatalf("assistant text = %q, want %q", text, "Hello world")
	}
	if tu.Assistant.Parts[0].Presentation != "final" {
		t.Fatalf("terminal text presentation = %q, want final", tu.Assistant.Parts[0].Presentation)
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("execution phase = %q, want idle", proj.Execution.Phase)
	}
	if proj.Execution.ActiveTurnID != "" {
		t.Fatalf("activeTurnId = %q, want empty after completion", proj.Execution.ActiveTurnID)
	}
}

func TestReducerCanonicalTextPartsAndOrphanToolResult(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{
		"itemId": "T1", "delta": "progress", "newPart": true,
	}))
	r.Apply(ev(3, "codex", "s1", "tool_finished", map[string]interface{}{
		"itemId": "orphan", "toolResult": "ignored",
	}))
	r.Apply(ev(4, "codex", "s1", "text_delta", map[string]interface{}{
		"itemId": "T1", "delta": "final", "newPart": true,
	}))
	r.Apply(ev(5, "codex", "s1", "turn_completed", map[string]interface{}{"turnId": "T1"}))

	projection, _ := r.Snapshot("codex", "s1")
	assistant := projection.Turns[0].Assistant
	if assistant == nil || len(assistant.Parts) != 2 {
		t.Fatalf("assistant parts = %+v, want exactly two canonical text parts", assistant)
	}
	if assistant.Parts[0].Presentation != "progress" ||
		assistant.Parts[1].Presentation != "final" {
		t.Fatalf("text presentations = %+v", assistant.Parts)
	}
	for _, part := range assistant.Parts {
		if part.Type == "tool" {
			t.Fatalf("orphan tool result materialized a tool part: %+v", part)
		}
	}
	if projection.SyncRev != 3 {
		t.Fatalf("orphan output must not advance projection revision; rev=%d, want 3", projection.SyncRev)
	}
}

// TestReducerRevMonotonicAndIdempotent: SyncRev never decreases, and re-applying a past event
// does not duplicate content (idempotency at the same per-session seq).
func TestReducerRevMonotonicAndIdempotent(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "ab"}))
	// Replay the SAME stamped event (same PerSessionSeq) — must be a no-op.
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "ab"}))
	r.Apply(ev(3, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "c"}))

	proj, _ := r.Snapshot("codex", "s1")
	if proj.SyncRev != 2 {
		t.Fatalf("SyncRev = %d, want 2 (turn_started no longer commits)", proj.SyncRev)
	}
	var text string
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "text" {
			text += p.Text
		}
	}
	if text != "abc" {
		t.Fatalf("text = %q, want %q (idempotent replay must not duplicate)", text, "abc")
	}
	// An older seq arriving out of order is ignored.
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "ZZ"}))
	proj, _ = r.Snapshot("codex", "s1")
	if proj.SyncRev != 2 {
		t.Fatalf("SyncRev changed after stale seq: %d", proj.SyncRev)
	}
}

// TestReducerUserMessageAttribution: user_message attributes to the turn via turnId and carries
// the response_item.id as the user message id.
func TestReducerUserMessageAttribution(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "user_message", map[string]interface{}{"itemId": "msg_001", "turnId": "T1", "text": "hi"}))
	proj, _ := r.Snapshot("codex", "s1")
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
		t.Fatalf("turns = %+v", proj.Turns)
	}
	u := proj.Turns[0].User
	if u == nil || u.ID != "msg_001" || u.Role != "user" {
		t.Fatalf("user msg = %+v", u)
	}
	if len(u.Parts) != 1 || u.Parts[0].Type != "text" || u.Parts[0].Text != "hi" {
		t.Fatalf("user parts = %+v", u.Parts)
	}
	if proj.Execution.Phase != "running" || proj.Execution.ActiveTurnID != "T1" {
		t.Fatalf("user_message must arm execution.running, got %+v", proj.Execution)
	}
}

// TestReducerConsecutiveTurnsKeepBothUserMessages covers the live sequence that
// regressed on device: after the first turn completes, the second userMessage
// must create a distinct turn instead of relying on a later cold hydrate.
func TestReducerConsecutiveTurnsKeepBothUserMessages(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex-web", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex-web", "s1", "user_message", map[string]interface{}{"itemId": "u1", "turnId": "T1", "text": "问题1"}))
	r.Apply(ev(3, "codex-web", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "回复1"}))
	r.Apply(ev(4, "codex-web", "s1", "turn_completed", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(5, "codex-web", "s1", "turn_started", map[string]interface{}{"turnId": "T2"}))
	r.Apply(ev(6, "codex-web", "s1", "user_message", map[string]interface{}{"itemId": "u2", "turnId": "T2", "text": "问题2"}))
	r.Apply(ev(7, "codex-web", "s1", "text_delta", map[string]interface{}{"itemId": "T2", "delta": "回复2"}))
	r.Apply(ev(8, "codex-web", "s1", "turn_completed", map[string]interface{}{"turnId": "T2"}))

	proj, ok := r.Snapshot("codex-web", "s1")
	if !ok || len(proj.Turns) != 2 {
		t.Fatalf("projection turns = %+v, ok=%v; want exactly two", proj.Turns, ok)
	}
	want := []struct {
		turnID, userID, userText, assistantText string
	}{
		{"T1", "u1", "问题1", "回复1"},
		{"T2", "u2", "问题2", "回复2"},
	}
	for i, expected := range want {
		turn := proj.Turns[i]
		if turn.TurnID != expected.turnID || turn.User == nil || turn.User.ID != expected.userID ||
			len(turn.User.Parts) != 1 || turn.User.Parts[0].Text != expected.userText {
			t.Fatalf("turn[%d] user = %+v; want turn=%s user=%s text=%q", i, turn, expected.turnID, expected.userID, expected.userText)
		}
		var assistantText string
		if turn.Assistant != nil {
			for _, part := range turn.Assistant.Parts {
				if part.Type == "text" {
					assistantText += part.Text
				}
			}
		}
		if assistantText != expected.assistantText {
			t.Fatalf("turn[%d] assistant text = %q, want %q", i, assistantText, expected.assistantText)
		}
	}
}

func TestReducerSystemMessageDoesNotArmExecution(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "claudecode", "s1", "system_message", map[string]interface{}{
		"itemId":          "compact-1",
		"turnId":          "compact-1",
		"text":            "已压缩对话 · 节省 160.7k tokens",
		"timestampMillis": int64(1_722_244_000_000),
	}))
	proj, ok := r.Snapshot("claudecode", "s1")
	if !ok || len(proj.Turns) != 1 {
		t.Fatalf("projection = %+v, ok=%v", proj, ok)
	}
	turn := proj.Turns[0]
	if turn.Status != "completed" || turn.StartedAt != 1_722_244_000_000 || turn.CompletedAt != 1_722_244_000_000 {
		t.Fatalf("system turn lifecycle = %+v", turn)
	}
	if turn.System == nil || turn.System.Role != "system" || len(turn.System.Parts) != 1 ||
		turn.System.Parts[0].Text != "已压缩对话 · 节省 160.7k tokens" ||
		turn.System.Parts[0].Presentation != "final" {
		t.Fatalf("system message = %+v", turn.System)
	}
	if proj.Execution.Phase != "idle" || proj.Execution.ActiveTurnID != "" {
		t.Fatalf("system_message must not arm execution: %+v", proj.Execution)
	}
	if !r.HasContentTurn("claudecode", "s1") {
		t.Fatal("system_message must count as projection content")
	}
}

// TestReducerContentRearmsAfterCompletedIdle: cold hydrate / prior turn_completed leaves
// phase=idle; a new user_message + reasoning without relying on turn_started must re-arm.
func TestReducerContentRearmsAfterCompletedIdle(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T0"}))
	r.Apply(ev(2, "codex", "s1", "turn_completed", map[string]interface{}{"turnId": "T0"}))
	proj, _ := r.Snapshot("codex", "s1")
	if proj.Execution.Phase != "idle" {
		t.Fatalf("after complete phase=%q", proj.Execution.Phase)
	}
	r.Apply(ev(3, "codex", "s1", "user_message", map[string]interface{}{"itemId": "u2", "turnId": "T1", "text": "第五轮测试"}))
	r.Apply(ev(4, "codex", "s1", "reasoning_delta", map[string]interface{}{"itemId": "T1", "delta": "thinking…"}))
	proj, _ = r.Snapshot("codex", "s1")
	if proj.Execution.Phase != "running" || proj.Execution.ActiveTurnID != "T1" {
		t.Fatalf("expected running T1 after new content, got %+v", proj.Execution)
	}
}

// TestReducerReasoningAccumulates: consecutive reasoning deltas accumulate into one reasoning part.
func TestReducerReasoningAccumulates(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "reasoning_delta", map[string]interface{}{"itemId": "T1", "delta": "think-"}))
	r.Apply(ev(3, "codex", "s1", "reasoning_delta", map[string]interface{}{"itemId": "T1", "delta": "ing"}))
	proj, _ := r.Snapshot("codex", "s1")
	var reasoning string
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "reasoning" {
			reasoning = p.Text
		}
	}
	if reasoning != "think-ing" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "think-ing")
	}
}

// TestReducerToolUpsert: tool_started then tool_finished upsert one tool part by call_id, with
// status progressing running -> completed.
func TestReducerToolUpsert(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "tool_started", map[string]interface{}{"itemId": "call_9", "toolName": "shell", "toolInput": "ls"}))
	// Mid-flight the tool part should be running.
	proj, _ := r.Snapshot("codex", "s1")
	if part := findTool(proj, "call_9"); part == nil || part.ToolStatus != "running" {
		t.Fatalf("mid-flight tool = %+v", part)
	}
	r.Apply(ev(3, "codex", "s1", "tool_finished", map[string]interface{}{"itemId": "call_9", "toolResult": "out"}))
	proj, _ = r.Snapshot("codex", "s1")
	part := findTool(proj, "call_9")
	if part == nil {
		t.Fatal("tool part missing after finish")
	}
	if part.ToolStatus != "completed" {
		t.Fatalf("tool status = %q, want completed", part.ToolStatus)
	}
	if part.ToolName != "shell" {
		t.Fatalf("tool name not preserved across finish: %q", part.ToolName)
	}
	// Exactly one tool part for call_9 (upsert, not append).
	count := 0
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "tool" && p.ItemID == "call_9" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("tool part count = %d, want 1 (upsert)", count)
	}
}

func findTool(proj SessionProjection, callID string) *ProjectionPart {
	if len(proj.Turns) == 0 || proj.Turns[0].Assistant == nil {
		return nil
	}
	for i := range proj.Turns[0].Assistant.Parts {
		if proj.Turns[0].Assistant.Parts[i].Type == "tool" && proj.Turns[0].Assistant.Parts[i].ItemID == callID {
			return &proj.Turns[0].Assistant.Parts[i]
		}
	}
	return nil
}

// TestReducerSkipsDriverPathIdentitylessEvents: events without rollout identity (driver/agent-event
// path: turn_started hardcodes turnId:"", text_delta lacks itemId) MUST NOT create projection
// state — Phase 1 reduce stays scoped to the rollout path (design §6.3 / §18.3).
func TestReducerSkipsDriverPathIdentitylessEvents(t *testing.T) {
	r := newTestReducer()
	// Driver-path turn_started (events.go hardcodes turnId:"").
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": ""}))
	// Driver-path text_delta (DeltaBatcher.emit strips Data to {"delta":text} — no itemId).
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"delta": "no identity"}))
	proj, ok := r.Snapshot("codex", "s1")
	if ok && len(proj.Turns) > 0 {
		t.Fatalf("driver-path events must not produce turns; got %+v", proj.Turns)
	}
}

// TestReducerSnapshotIsIndependent: mutating the projection after Snapshot does not alter the
// returned snapshot (pull must be a stable copy).
func TestReducerSnapshotIsIndependent(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	snap, _ := r.Snapshot("codex", "s1")
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "more"}))
	if snap.Turns[0].Assistant != nil {
		t.Fatalf("snapshot mutated after later Apply: %+v", snap.Turns[0].Assistant)
	}
}

// TestReducerFlushPatchCoalescesAndDeltas: FlushPatch emits a coalesced patch whose baseRev
// advances, and clears pending so the next flush is a clean delta.
func TestReducerFlushPatchCoalescesAndDeltas(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "a"}))
	r.Apply(ev(3, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "b"}))

	p1, ok := r.FlushPatch("codex", "s1")
	if !ok {
		t.Fatal("first flush produced no patch")
	}
	if p1.BaseRev != 0 || p1.SyncRev != 2 {
		t.Fatalf("patch1 base/sync = %d/%d, want 0/2", p1.BaseRev, p1.SyncRev)
	}
	// The two text deltas are coalesced into a single append_text op.
	if len(p1.PartOps) != 1 || p1.PartOps[0].Op != "append_text" || p1.PartOps[0].Text != "ab" {
		t.Fatalf("patch1 partOps = %+v", p1.PartOps)
	}
	// The turn upsert lands with the first content event (markRunning carries the
	// content-bearing turn). A bare skeleton turn must never publish (owner 2026-08-04
	// fence fix) — assert the upsert carries assistant content.
	if len(p1.UpsertTurns) != 1 || p1.UpsertTurns[0].TurnID != "T1" || p1.UpsertTurns[0].Assistant == nil {
		t.Fatalf("patch1 upsertTurns = %+v, want T1 with content", p1.UpsertTurns)
	}

	// No new activity: second flush is empty.
	if _, ok := r.FlushPatch("codex", "s1"); ok {
		t.Fatal("second flush with no activity should be empty")
	}

	// New activity produces a delta whose baseRev == previous syncRev.
	r.Apply(ev(4, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "c"}))
	p2, ok := r.FlushPatch("codex", "s1")
	if !ok {
		t.Fatal("third flush produced no patch")
	}
	if p2.BaseRev != 2 || p2.SyncRev != 3 {
		t.Fatalf("patch2 base/sync = %d/%d, want 2/3", p2.BaseRev, p2.SyncRev)
	}
	if len(p2.PartOps) != 1 || p2.PartOps[0].Text != "c" {
		t.Fatalf("patch2 partOps = %+v", p2.PartOps)
	}
}

// TestPublishLogicalMountsProjectionReducer proves the §6.2 mount: PublishLogical allocates
// perSessionSeq and the stamped EventMessage reaches the reducer, so SyncRev advances and parts
// attribute — without the dispatch/buffer layer (that is exercised in WP5/WP6).
func TestPublishLogicalMountsProjectionReducer(t *testing.T) {
	ep := NewEventPublisher("epoch-mount")
	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": "hi"}, Broadcast: true})

	proj, ok := ep.projection.Snapshot("codex", "s1")
	if !ok {
		t.Fatal("PublishLogical did not populate the projection reducer (mount broken)")
	}
	if proj.SyncRev != 1 {
		t.Fatalf("SyncRev = %d, want 1 (turn_started no longer commits)", proj.SyncRev)
	}
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
		t.Fatalf("turns = %+v", proj.Turns)
	}
	var text string
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "text" {
			text += p.Text
		}
	}
	if text != "hi" {
		t.Fatalf("assistant text = %q, want %q", text, "hi")
	}
}

// TestReducerTurnCompletedFallsBackToActiveTurnID: live driver frames may omit turnId on
// turn_completed; if ActiveTurnID was armed by turn_started, completion must still flip phase idle.
func TestReducerTurnCompletedFallsBackToActiveTurnID(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T-live"}))
	r.Apply(ev(2, "codex", "s1", "turn_completed", map[string]interface{}{"done": true})) // no turnId
	proj, ok := r.Snapshot("codex", "s1")
	if !ok {
		t.Fatal("missing projection")
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("phase = %q, want idle after turn_completed fallback", proj.Execution.Phase)
	}
	if proj.Execution.ActiveTurnID != "" {
		t.Fatalf("activeTurnId = %q, want empty", proj.Execution.ActiveTurnID)
	}
}

// TestReducerNewTurnSettlesSupersededRunningTurn: Codex may start turn B without completing
// turn A. Projection SoT must not keep A status=running, or observers OR turnStillLive forever.
func TestReducerNewTurnSettlesSupersededRunningTurn(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "A"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "A", "delta": "partial"}))
	r.Apply(ev(3, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "B"}))
	r.Apply(ev(4, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "B", "delta": "next"}))
	r.Apply(ev(5, "codex", "s1", "turn_completed", map[string]interface{}{"turnId": "B"}))

	proj, ok := r.Snapshot("codex", "s1")
	if !ok {
		t.Fatal("missing projection")
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("phase = %q, want idle", proj.Execution.Phase)
	}
	byID := map[string]TurnProjection{}
	for _, turn := range proj.Turns {
		byID[turn.TurnID] = turn
	}
	if byID["A"].Status != "completed" {
		t.Fatalf("superseded turn A status = %q, want completed", byID["A"].Status)
	}
	if byID["B"].Status != "completed" {
		t.Fatalf("turn B status = %q, want completed", byID["B"].Status)
	}
	for _, turn := range proj.Turns {
		if turn.Status == "running" || turn.Status == "pending" {
			t.Fatalf("zombie non-settled turn after complete: %+v", turn)
		}
	}
}

// TestReducerRestoreHealsZombieRunningTurnsWhenIdle: stale checkpoints may restore phase=idle
// with older turns still status=running. Heal on Restore so rehydrate does not re-poison SoT.
func TestReducerRestoreHealsZombieRunningTurnsWhenIdle(t *testing.T) {
	r := newTestReducer()
	r.Restore("codex", "s1", SessionProjection{
		SessionID: "s1",
		SyncRev:   12,
		Execution: ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{
			{TurnID: "A", Status: "running"},
			{TurnID: "B", Status: "completed"},
		},
	})
	proj, ok := r.Snapshot("codex", "s1")
	if !ok {
		t.Fatal("missing projection")
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("phase = %q, want idle", proj.Execution.Phase)
	}
	for _, turn := range proj.Turns {
		if turn.Status == "running" || turn.Status == "pending" {
			t.Fatalf("zombie non-settled turn after idle restore: %+v", turn)
		}
	}
}

// TestReducerToolConvergesByItemIdAcrossRelaySources proves the Issue 3 design property the
// 3-way review (agent + two independent analysts) converged on: when BOTH the Claude file relay
// (transcript tool_use.id) and the agent relay sidecar (Anthropic tool_use.id → RequestID) report
// the SAME tool call for a local turn, the reducer upsert-merges by itemId — exactly ONE tool part
// results, never a duplicate. Running the agent relay as a sidecar alongside the file relay
// therefore cannot double-write tool state into the projection (guardrails #3/#4).
func TestReducerToolConvergesByItemIdAcrossRelaySources(t *testing.T) {
	r := newTestReducer()
	const backend, sid, turn = "claudecode", "s-tool-conv", "turn-tool"
	const tool = "toolu_CONV"
	r.Apply(ev(1, backend, sid, "turn_started", map[string]interface{}{"turnId": turn}))
	// Both relay sources report the same tool started (file relay from transcript tool_use.id,
	// agent relay from driver tool_use.id). Must upsert-merge into one part, not append a duplicate.
	r.Apply(ev(2, backend, sid, "tool_started", map[string]interface{}{"itemId": tool, "toolName": "bash", "toolInput": map[string]interface{}{"cmd": "ls"}}))
	r.Apply(ev(3, backend, sid, "tool_started", map[string]interface{}{"itemId": tool, "toolName": "bash"}))
	// Both relay sources report completion; final status must settle to completed.
	r.Apply(ev(4, backend, sid, "tool_finished", map[string]interface{}{"itemId": tool, "toolResult": "ok"}))
	r.Apply(ev(5, backend, sid, "tool_finished", map[string]interface{}{"itemId": tool, "toolResult": "ok"}))
	r.Apply(ev(6, backend, sid, "turn_completed", map[string]interface{}{"turnId": turn}))

	proj, ok := r.Snapshot(backend, sid)
	if !ok {
		t.Fatal("missing projection")
	}
	var tools []ProjectionPart
	for _, tu := range proj.Turns {
		if tu.Assistant == nil {
			continue
		}
		for _, p := range tu.Assistant.Parts {
			if p.Type == "tool" && p.ItemID == tool {
				tools = append(tools, p)
			}
		}
	}
	if len(tools) != 1 {
		t.Fatalf("expected exactly 1 tool part for %s across relay sources, got %d: %+v", tool, len(tools), tools)
	}
	if tools[0].ToolStatus != "completed" {
		t.Fatalf("final tool status = %q, want completed", tools[0].ToolStatus)
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("execution phase = %q, want idle after turn_completed", proj.Execution.Phase)
	}
}

// TestReducerSkipsIdentitylessAgentRelayContent proves the second half of the Issue 3 safety
// argument: the Claude agent relay's EventText/EventThinking carry NO itemId/turnId (the driver
// only tracks the Anthropic message.id, not the transcript UUID the reducer keys on), and a
// turn_completed with no armed ActiveTurnID has nothing to complete. The reducer must SKIP all
// such identityless frames so the sidecar agent relay cannot inject a phantom turn or content
// into the projection — the file relay stays the sole UUID-keyed content source (guardrail #3).
func TestReducerSkipsIdentitylessAgentRelayContent(t *testing.T) {
	r := newTestReducer()
	const backend, sid = "claudecode", "s-skip"
	r.Apply(ev(1, backend, sid, "text_delta", map[string]interface{}{"delta": "no identity"}))      // empty itemId
	r.Apply(ev(2, backend, sid, "reasoning_delta", map[string]interface{}{"delta": "no identity"})) // empty itemId
	r.Apply(ev(3, backend, sid, "turn_started", map[string]interface{}{}))                          // empty turnId
	r.Apply(ev(4, backend, sid, "turn_completed", map[string]interface{}{}))                        // empty turnId, no active turn

	if r.TurnCount(backend, sid) != 0 {
		proj, _ := r.Snapshot(backend, sid)
		t.Fatalf("identityless agent-relay frames must not create a turn; TurnCount=%d proj=%+v", r.TurnCount(backend, sid), proj)
	}
	if r.HasContentTurn(backend, sid) {
		t.Fatal("identityless agent-relay frames must not register content")
	}
}

// TestReducerToolMatchesPopulated (P0-1, 护栏12 owning-仓 G1): the reducer MUST populate
// part.Matches from data["matches"] for tool events. Producer = Claude session driver
// (session.go EventToolResult.ToolMatches → events.go payload["matches"]). Before P0-1 the
// reducer set ToolName/Input/Result/Status but never Matches → projection matches 恒空.
// This test proves the reducer no longer drops matches, decoupled from any consumer test.
func TestReducerToolMatchesPopulated(t *testing.T) {
	// Three wire-decoded ToolMatches shapes (events.go writes payload["matches"] = ev.ToolMatches).
	shapes := []struct {
		name    string
		matches map[string]interface{}
		kind    string
	}{
		{
			name:    "paths",
			matches: map[string]interface{}{"kind": "paths", "paths": []interface{}{"src/a.ts", "src/b.go"}},
			kind:    "paths",
		},
		{
			name:    "count",
			matches: map[string]interface{}{"kind": "count", "count": int64(42)},
			kind:    "count",
		},
		{
			name: "detailed",
			matches: map[string]interface{}{
				"kind": "detailed",
				"items": []interface{}{
					map[string]interface{}{"path": "src/x.ts", "line": int64(10)},
				},
			},
			kind: "detailed",
		},
	}

	for _, tc := range shapes {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestReducer()
			r.Apply(ev(1, "claudecode", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
			// tool_started carries matches (some producers attach at start).
			r.Apply(ev(2, "claudecode", "s1", "tool_started", map[string]interface{}{
				"itemId":   "call_glob",
				"toolName": "Glob",
				"matches":  tc.matches,
			}))
			proj, _ := r.Snapshot("claudecode", "s1")
			part := findTool(proj, "call_glob")
			if part == nil {
				t.Fatal("tool part missing")
			}
			m, ok := part.Matches.(map[string]interface{})
			if !ok || m["kind"] != tc.kind {
				t.Fatalf("tool_started matches = %+v (%T), want kind=%s", part.Matches, part.Matches, tc.kind)
			}

			// tool_finished WITHOUT matches must NOT clobber the matches set at started
			// (mergeToolPart carries Matches only when src non-nil; result-only finish keeps prior).
			r.Apply(ev(3, "claudecode", "s1", "tool_finished", map[string]interface{}{
				"itemId":     "call_glob",
				"toolResult": "done",
			}))
			proj, _ = r.Snapshot("claudecode", "s1")
			part = findTool(proj, "call_glob")
			m, ok = part.Matches.(map[string]interface{})
			if !ok || m["kind"] != tc.kind {
				t.Fatalf("matches lost after matches-less tool_finished = %+v (%T)", part.Matches, part.Matches)
			}
			if part.ToolStatus != "completed" {
				t.Fatalf("status = %q, want completed", part.ToolStatus)
			}
		})
	}
}

// TestReducerToolMatchesOnFinishedUpsert (P0-1, 护栏12): matches arriving on tool_finished
// (the common Glob result path) must upsert onto the part created at tool_started via
// mergeToolPart. Without the Matches carry in mergeToolPart, finished-only matches were dropped.
func TestReducerToolMatchesOnFinishedUpsert(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "claudecode", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "claudecode", "s1", "tool_started", map[string]interface{}{
		"itemId":   "call_grep",
		"toolName": "Grep",
	}))
	r.Apply(ev(3, "claudecode", "s1", "tool_finished", map[string]interface{}{
		"itemId":     "call_grep",
		"toolResult": "3 matches",
		"matches":    map[string]interface{}{"kind": "count", "count": int64(3)},
	}))
	proj, _ := r.Snapshot("claudecode", "s1")
	part := findTool(proj, "call_grep")
	if part == nil {
		t.Fatal("tool part missing")
	}
	m, ok := part.Matches.(map[string]interface{})
	if !ok {
		t.Fatalf("matches missing after finished-only attach = %+v (%T)", part.Matches, part.Matches)
	}
	if m["kind"] != "count" || m["count"] != int64(3) {
		t.Fatalf("finished-attached matches = %+v, want kind=count count=3", m)
	}
}

// TestReducerToolMatchesAbsentIsNil (P0-1): events without a "matches" key (the overwhelming
// majority of tool/text events) leave part.Matches nil — additive, no spurious empty payload.
func TestReducerToolMatchesAbsentIsNil(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "tool_started", map[string]interface{}{"itemId": "c1", "toolName": "shell"}))
	r.Apply(ev(3, "codex", "s1", "tool_finished", map[string]interface{}{"itemId": "c1", "toolResult": "ok"}))
	proj, _ := r.Snapshot("codex", "s1")
	part := findTool(proj, "c1")
	if part == nil {
		t.Fatal("tool part missing")
	}
	if part.Matches != nil {
		t.Fatalf("matches should be nil when no matches key; got %+v", part.Matches)
	}
}

// TestTurnStartedDoesNotPublishSkeletonTurn: a bare turn_started must NOT advance SyncRev —
// committing it publishes a skeleton projection (running turn without user/assistant content)
// that breaks client local-send optimistic paint fences (owner 2026-08-04 真机: 发送后
// user bubble 消失几秒再出现,因为半成品 patch rev > baseline 但无 user row)。
func TestTurnStartedDoesNotPublishSkeletonTurn(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	patch, ok := r.FlushPatch("codex", "s1")
	if ok {
		t.Fatalf("bare turn_started must not publish a patch, got %+v", patch)
	}
	proj, _ := r.Snapshot("codex", "s1")
	if proj.SyncRev != 0 {
		t.Fatalf("SyncRev = %d, want 0 (turn_started must not commit)", proj.SyncRev)
	}
	// Execution stays armed for tool attach (ActiveTurnID) even without commit.
	if proj.Execution.Phase != "running" || proj.Execution.ActiveTurnID != "T1" {
		t.Fatalf("execution = %+v, want running/T1 (markRunning must stay)", proj.Execution)
	}
}

// TestTurnStartedUserMessageLandTogether: turn_started followed by user_message commits ONE
// patch carrying the user part — the frame a client sees after its send already contains the
// user row, so the optimistic paint fence can release safely without blanking the bubble.
func TestTurnStartedUserMessageLandTogether(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "user_message", map[string]interface{}{"turnId": "T1", "itemId": "u1", "text": "问题1"}))
	patch, ok := r.FlushPatch("codex", "s1")
	if !ok {
		t.Fatal("expected a patch after user_message")
	}
	if patch.SyncRev != 1 {
		t.Fatalf("SyncRev = %d, want 1 (only user_message commits)", patch.SyncRev)
	}
	proj, _ := r.Snapshot("codex", "s1")
	if len(proj.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(proj.Turns))
	}
	turn := proj.Turns[0]
	if turn.User == nil || len(turn.User.Parts) == 0 || turn.User.Parts[0].Text != "问题1" {
		t.Fatalf("turn user part missing: %+v", turn.User)
	}
	if turn.Status != "running" || proj.Execution.Phase != "running" {
		t.Fatalf("turn/execution must stay running: turn=%+v exec=%+v", turn, proj.Execution)
	}
}

// TestTurnStartedRepeatedNoExtraCommit: a duplicated turn_started (two entry points relay the
// same lifecycle frame) must not advance SyncRev either — idempotent by construction.
func TestTurnStartedRepeatedNoExtraCommit(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(3, "codex", "s1", "user_message", map[string]interface{}{"turnId": "T1", "itemId": "u1", "text": "hi"}))
	patch, ok := r.FlushPatch("codex", "s1")
	if !ok {
		t.Fatal("expected a patch")
	}
	if patch.SyncRev != 1 {
		t.Fatalf("SyncRev = %d, want 1 (duplicated turn_started must not commit)", patch.SyncRev)
	}
}
