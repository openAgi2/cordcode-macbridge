package gobridge

import (
	"testing"
	"time"
)

// waitForProjection polls the reducer until the check passes or the deadline elapses. The Codex
// file-relay scans the rollout asynchronously (poll interval compressed by withFastCodexFileRelay);
// the reducer is fed by PublishLogical regardless of whether a client drains the websocket.
func waitForProjection(t *testing.T, h *Handlers, backend, session string, check func(SessionProjection) bool) SessionProjection {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if proj, ok := h.eventPublisher.ProjectionSnapshot(backend, session); ok && check(proj) {
			return proj
		}
		time.Sleep(10 * time.Millisecond)
	}
	proj, _ := h.eventPublisher.ProjectionSnapshot(backend, session)
	t.Fatalf("projection never satisfied condition; current = %+v", proj)
	return proj
}

// TestPhase1GateRealRolloutScanToReducePullPush is the Phase 1 exit gate (design §10.1 item 6 +
// §6.4). It feeds a REAL Codex rollout JSONL (lifecycle + user_message + assistant text + tool
// call/output + completion) through the actual scanCodexTranscriptRelayEvents → file-relay →
// PublishLogical → ProjectionReducer.Apply path, then asserts:
//   - parts attribute to the correct turn via itemId (text/reasoning itemId == lifecycle turn_id;
//     user itemId == response_item.id; tool itemId == call_id) — the §18.3 rollout attribution;
//   - syncRev is monotonic (== perSessionSeq);
//   - completion → status=completed + completedAt, execution=idle;
//   - pull == push: get_session_projection (Snapshot) returns the same authoritative state push
//     reads.
//
// Per §18.3 this does NOT exercise DeltaBatcher (the rollout path bypasses it).
func TestPhase1GateRealRolloutScanToReducePullPush(t *testing.T) {
	const sessionID = "gate-rollout"
	// Start the relay with a RUNNING turn (task_started only). Starting with task_complete
	// already in the file takes the idle-startup path, which broadcasts only completion and
	// skips content; we append the content while the turn is running so it is reduced.
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	defer waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
	_ = readEventNames(t, client, 2) // drain turn_started + session_state_changed (running startup)

	appendCodexRollout(t, agent.transcriptPath,
		`{"timestamp":"2026-07-01T07:37:47.626Z","type":"response_item","payload":{"type":"message","role":"user","id":"msg_1","content":[{"type":"input_text","text":"what is 2+2"}]}}`,
		`{"timestamp":"2026-07-01T07:37:48.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Let me compute"}]}}`,
		`{"timestamp":"2026-07-01T07:37:49.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call_1","arguments":"{}"}}`,
		`{"timestamp":"2026-07-01T07:37:50.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"4"}}`,
		codexRolloutEvent("task_complete"),
	)

	proj := waitForProjection(t, handlers, "codex", sessionID, func(p SessionProjection) bool {
		return len(p.Turns) > 0 && p.Turns[0].Status == "completed" &&
			p.Turns[0].User != nil && p.Turns[0].Assistant != nil &&
			len(p.Turns[0].Assistant.Parts) >= 2 // text + tool
	})

	if len(proj.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(proj.Turns))
	}
	tu := proj.Turns[0]

	// turnId from the rollout lifecycle event_msg.turn_id ("turn-1").
	if tu.TurnID != "turn-1" {
		t.Fatalf("turnId = %q, want turn-1", tu.TurnID)
	}
	if tu.Status != "completed" || tu.CompletedAt == 0 {
		t.Fatalf("turn not completed: status=%q completedAt=%d", tu.Status, tu.CompletedAt)
	}

	// user message: id == response_item.id, text preserved.
	if tu.User.ID != "msg_1" {
		t.Fatalf("user msg id = %q, want msg_1", tu.User.ID)
	}
	if got := tu.User.Parts[0].Text; got != "what is 2+2" {
		t.Fatalf("user text = %q", got)
	}

	// assistant text attributes to the turn via itemId == lifecycle turn_id.
	var aText string
	for _, p := range tu.Assistant.Parts {
		if p.Type == "text" {
			aText += p.Text
		}
	}
	if aText != "Let me compute" {
		t.Fatalf("assistant text = %q, want %q", aText, "Let me compute")
	}

	// tool call attributes by call_id, status completed.
	var tool *ProjectionPart
	for i := range tu.Assistant.Parts {
		if tu.Assistant.Parts[i].Type == "tool" && tu.Assistant.Parts[i].ItemID == "call_1" {
			tool = &tu.Assistant.Parts[i]
		}
	}
	if tool == nil {
		t.Fatalf("tool call_1 missing; parts = %+v", tu.Assistant.Parts)
	}
	if tool.ToolStatus != "completed" {
		t.Fatalf("tool status = %q, want completed", tool.ToolStatus)
	}

	// execution reflects idle after completion (G4 isExecuting = phase ∈ {running, requires_action}).
	if proj.Execution.Phase != "idle" {
		t.Fatalf("execution phase = %q, want idle", proj.Execution.Phase)
	}

	// syncRev monotonic and > 0 (== perSessionSeq, advanced once per event).
	if proj.SyncRev <= 0 {
		t.Fatalf("syncRev = %d, must be > 0", proj.SyncRev)
	}

	// pull == push: a second pull returns the same authoritative state (the single source both
	// push FlushPatch and pull Snapshot read).
	again, _ := handlers.eventPublisher.ProjectionSnapshot("codex", sessionID)
	if again.SyncRev != proj.SyncRev || len(again.Turns) != len(proj.Turns) ||
		again.Turns[0].TurnID != proj.Turns[0].TurnID || again.Turns[0].Status != proj.Turns[0].Status {
		t.Fatalf("pull != pull (non-idempotent authoritative read): %+v vs %+v", again, proj)
	}
}
