package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestReducerContextCompactionIsVisibleWithoutUserAndDoesNotRegress(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex-remote", "thread", "turn_started", map[string]interface{}{"turnId": "turn-c"}))
	r.Apply(ev(2, "codex-remote", "thread", "context_compressing", map[string]interface{}{
		"turnId": "turn-c", "itemId": "item-c",
	}))
	proj, ok := r.Snapshot("codex-remote", "thread")
	if !ok || len(proj.Turns) != 1 || proj.Turns[0].User != nil || proj.Turns[0].System == nil {
		t.Fatalf("compaction-only turn = %+v", proj)
	}
	part := proj.Turns[0].System.Parts[0]
	if part.Type != "context_compaction" || part.ItemID != "item-c" || part.ContextCompactionStatus != "running" {
		t.Fatalf("running part = %+v", part)
	}

	beforeDuplicate := proj.SyncRev
	r.Apply(ev(3, "codex-remote", "thread", "context_compressing", map[string]interface{}{
		"turnId": "turn-c", "itemId": "item-c",
	}))
	proj, _ = r.Snapshot("codex-remote", "thread")
	if proj.SyncRev != beforeDuplicate {
		t.Fatalf("duplicate started advanced sync rev: %d -> %d", beforeDuplicate, proj.SyncRev)
	}

	r.Apply(ev(4, "codex-remote", "thread", "context_compressed", map[string]interface{}{
		"turnId": "turn-c", "itemId": "item-c",
	}))
	r.Apply(ev(5, "codex-remote", "thread", "context_compressing", map[string]interface{}{
		"turnId": "turn-c", "itemId": "item-c",
	}))
	proj, _ = r.Snapshot("codex-remote", "thread")
	part = proj.Turns[0].System.Parts[0]
	if part.ContextCompactionStatus != "completed" {
		t.Fatalf("late started regressed part = %+v", part)
	}

	r.Apply(ev(6, "codex-remote", "thread", "turn_completed", map[string]interface{}{"turnId": "turn-c"}))
	proj, _ = r.Snapshot("codex-remote", "thread")
	if proj.Turns[0].Status != "completed" || proj.Execution.Phase != "idle" {
		t.Fatalf("terminal projection = %+v", proj)
	}
}

func TestTurnScopedHistoryMapsCompactionToSameProjectionPart(t *testing.T) {
	events := turnScopedHistoryTurnToProjectionEvents([]core.TurnScopedHistoryTurn{{
		TurnID: "turn-c", Status: "completed", Parts: []map[string]any{{
			"type": "context_compaction", "itemId": "item-c", "status": "completed",
		}},
	}})
	if len(events) != 2 || events[0].Event != "context_compressed" || events[1].Event != "turn_completed" {
		t.Fatalf("history events = %+v", events)
	}
	r := newTestReducer()
	for i, event := range events {
		r.Apply(ev(i+1, "codex-remote", "thread", event.Event, event.Data))
	}
	proj, _ := r.Snapshot("codex-remote", "thread")
	part := proj.Turns[0].System.Parts[0]
	if part.Type != "context_compaction" || part.ItemID != "item-c" || part.ContextCompactionStatus != "completed" {
		t.Fatalf("cold part = %+v", part)
	}
}

func TestContextCompactionSnapshotPatchAndWindowParity(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex-remote", "thread", "context_compressed", map[string]interface{}{
		"turnId": "turn-c", "itemId": "item-c",
	}))
	snapshot, ok := r.Snapshot("codex-remote", "thread")
	if !ok || len(snapshot.Turns) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	patch, ok := r.FlushPatch("codex-remote", "thread")
	if !ok || len(patch.UpsertTurns) != 1 {
		t.Fatalf("patch = %+v", patch)
	}
	window, err := sliceProjectionWindow("codex-remote", "thread", windowTestEpoch, snapshot, GetSessionProjectionWindowParams{
		Direction: "window_0",
	})
	if err != nil || len(window.Turns) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	for name, turn := range map[string]TurnProjection{
		"snapshot": snapshot.Turns[0], "patch": patch.UpsertTurns[0], "window": window.Turns[0],
	} {
		if turn.User != nil || turn.System == nil || len(turn.System.Parts) != 1 {
			t.Fatalf("%s turn = %+v", name, turn)
		}
		part := turn.System.Parts[0]
		if part.Type != "context_compaction" || part.ItemID != "item-c" || part.ContextCompactionStatus != "completed" {
			t.Fatalf("%s part = %+v", name, part)
		}
	}
}
