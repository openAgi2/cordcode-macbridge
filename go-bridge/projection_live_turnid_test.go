package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestLiveDriverTurnIDPlumbsExecutionPhaseIdle proves the Phase-3 contract the K4
// contamination rollback depends on: live driver lifecycle frames carry TurnID through
// mapAgentEvent into the reducer, and turn_completed flips execution.phase to idle so a
// projection_patch can be flushed (design §6.4 rule 3 / §7.4).
func TestLiveDriverTurnIDPlumbsExecutionPhaseIdle(t *testing.T) {
	r := newTestReducer()

	startName, startData, startDone := mapAgentEvent(core.Event{
		Type:   core.EventTurnStarted,
		TurnID: "turn-live-contract",
	})
	if startName != "turn_started" || startDone {
		t.Fatalf("turn_started map = %q/%v", startName, startDone)
	}
	r.Apply(ev(1, "codex", "s-live", startName, startData.(map[string]interface{})))

	mid, ok := r.Snapshot("codex", "s-live")
	if !ok || mid.Execution.Phase != "running" || mid.Execution.ActiveTurnID != "turn-live-contract" {
		t.Fatalf("after start phase/active = %+v", mid.Execution)
	}

	endName, endData, endDone := mapAgentEvent(core.Event{
		Type:   core.EventResult,
		Done:   true,
		TurnID: "turn-live-contract",
		Content: "done",
	})
	if endName != "turn_completed" || !endDone {
		t.Fatalf("turn_completed map = %q/%v", endName, endDone)
	}
	payload := endData.(map[string]interface{})
	if payload["turnId"] != "turn-live-contract" {
		t.Fatalf("turn_completed payload missing turnId: %#v", payload)
	}
	r.Apply(ev(2, "codex", "s-live", endName, payload))

	proj, ok := r.Snapshot("codex", "s-live")
	if !ok {
		t.Fatal("missing projection after complete")
	}
	if proj.Execution.Phase != "idle" {
		t.Fatalf("phase = %q, want idle", proj.Execution.Phase)
	}
	if proj.Execution.ActiveTurnID != "" {
		t.Fatalf("activeTurnId = %q, want empty", proj.Execution.ActiveTurnID)
	}

	patch, flushOk := r.FlushPatch("codex", "s-live")
	if !flushOk {
		t.Fatal("expected projection_patch flush after lifecycle reduce")
	}
	if patch.Execution == nil || patch.Execution.Phase != "idle" {
		t.Fatalf("patch execution = %+v, want phase idle", patch.Execution)
	}
}
