package gobridge

import (
	"testing"
)

// Turn-level official net file diffs (owner round-4, 2026-09-22): opencode
// user-message summary.diffs ride the live turn_file_changes event / the
// hydrate user_message event and land on TurnProjection.FileChanges. The
// reducer is fail-closed on unknown turn ids (no phantom turns); present
// fileChanges win in upsertTurn (the live summary converges step by step).

func turnFileChangesPayload() []interface{} {
	return []interface{}{
		map[string]interface{}{"path": "CHANGELOG.md", "kind": "edit", "additions": 1, "deletions": 1},
		map[string]interface{}{"path": "docs/new.md", "kind": "create", "additions": 31, "deletions": 0},
	}
}

// TestReducerTurnFileChangesLiveUpsert: turn_file_changes attaches to the
// OWNING turn created by user_message; a later (step-converged) emission
// replaces the held value.
func TestReducerTurnFileChangesLiveUpsert(t *testing.T) {
	r := newTestReducer()
	const backendID = "opencode-web"
	const sessionID = "s-turn-fc"
	const turnID = "msg_user_1"

	r.Apply(ev(1, backendID, sessionID, "user_message", map[string]interface{}{
		"itemId": turnID, "turnId": turnID, "text": "fix it",
	}))
	r.Apply(ev(2, backendID, sessionID, "turn_file_changes", map[string]interface{}{
		"turnId":      turnID,
		"fileChanges": turnFileChangesPayload(),
	}))

	snap, ok := r.Snapshot(backendID, sessionID)
	if !ok {
		t.Fatal("Snapshot missing")
	}
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1 (no phantom turns)", len(snap.Turns))
	}
	turn := snap.Turns[0]
	if turn.FileChanges == nil {
		t.Fatal("turn.FileChanges missing")
	}
	entries, ok := turn.FileChanges.([]interface{})
	if !ok || len(entries) != 2 {
		t.Fatalf("turn.FileChanges = %#v, want 2 entries", turn.FileChanges)
	}

	// Step-converged replacement wins (present beats held).
	replacement := []interface{}{
		map[string]interface{}{"path": "CHANGELOG.md", "kind": "edit", "additions": 2, "deletions": 1},
		map[string]interface{}{"path": "docs/new.md", "kind": "create", "additions": 31, "deletions": 0},
		map[string]interface{}{"path": "src/a.swift", "kind": "edit", "additions": 51, "deletions": 0},
	}
	r.Apply(ev(3, backendID, sessionID, "turn_file_changes", map[string]interface{}{
		"turnId":      turnID,
		"fileChanges": replacement,
	}))
	snap2, _ := r.Snapshot(backendID, sessionID)
	entries2, ok := snap2.Turns[0].FileChanges.([]interface{})
	if !ok || len(entries2) != 3 {
		t.Fatalf("converged turn.FileChanges = %#v, want 3 entries", snap2.Turns[0].FileChanges)
	}
}

// TestReducerTurnFileChangesUnknownTurnIgnored: a turn_file_changes for a turn
// the kernel does not hold is dropped (fail-closed attribution; cold hydrate
// owns absent turns).
func TestReducerTurnFileChangesUnknownTurnIgnored(t *testing.T) {
	r := newTestReducer()
	const backendID = "opencode-web"
	const sessionID = "s-turn-fc-unknown"

	r.Apply(ev(1, backendID, sessionID, "turn_file_changes", map[string]interface{}{
		"turnId":      "msg_never_seen",
		"fileChanges": turnFileChangesPayload(),
	}))

	snap, ok := r.Snapshot(backendID, sessionID)
	if !ok {
		t.Fatal("Snapshot missing")
	}
	if len(snap.Turns) != 0 {
		t.Fatalf("turns = %d, want 0 (no phantom turns)", len(snap.Turns))
	}
}

// TestReducerUserMessageCarriesTurnFileChanges: the hydrate user_message with
// fileChanges writes the turn-level field; a later user_message re-emission
// WITHOUT fileChanges keeps the held value (absent never clears).
func TestReducerUserMessageCarriesTurnFileChanges(t *testing.T) {
	r := newTestReducer()
	const backendID = "opencode-web"
	const sessionID = "s-turn-fc-hydrate"
	const turnID = "msg_user_2"

	r.Apply(ev(1, backendID, sessionID, "user_message", map[string]interface{}{
		"itemId":      turnID,
		"turnId":      turnID,
		"text":        "fix it",
		"fileChanges": turnFileChangesPayload(),
	}))
	snap, ok := r.Snapshot(backendID, sessionID)
	if !ok {
		t.Fatal("Snapshot missing")
	}
	entries, ok := snap.Turns[0].FileChanges.([]interface{})
	if !ok || len(entries) != 2 {
		t.Fatalf("hydrate turn.FileChanges = %#v, want 2 entries", snap.Turns[0].FileChanges)
	}

	// Re-emission without fileChanges keeps the settled summary.
	r.Apply(ev(2, backendID, sessionID, "user_message", map[string]interface{}{
		"itemId": turnID, "turnId": turnID, "text": "fix it",
	}))
	snap2, _ := r.Snapshot(backendID, sessionID)
	entries2, ok := snap2.Turns[0].FileChanges.([]interface{})
	if !ok || len(entries2) != 2 {
		t.Fatalf("re-emitted user_message cleared fileChanges: %#v", snap2.Turns[0].FileChanges)
	}
}
