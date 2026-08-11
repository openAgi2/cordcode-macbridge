package gobridge

import (
	"encoding/json"
	"testing"
)

// Guardrail 12 / ChatGPT-style activity rows: title + fileChanges on tool event Data must
// land in ProjectionPart and survive Snapshot() so iOS cold-start can map them. Prior to this
// test, hydrateToolEventsFromStep passed the fields through but the reducer dropped them.
func TestReducerToolEvents_TitleAndFileChangesSurviveSnapshot(t *testing.T) {
	r := newTestReducer()
	const backendID = "codex"
	const sessionID = "s-title-fc"
	const turnID = "turn-1"
	const callID = "call-patch-1"

	// Own a turn so tool_started attaches.
	r.Apply(ev(1, backendID, sessionID, "user_message", map[string]interface{}{
		"itemId": turnID, "turnId": turnID, "text": "edit a file",
	}))
	r.Apply(ev(2, backendID, sessionID, "tool_started", map[string]interface{}{
		"itemId":    callID,
		"toolName":  "Patch",
		"title":     "src/a.swift",
		"toolInput": map[string]interface{}{"path": "src/a.swift"},
	}))
	changes := []interface{}{
		map[string]interface{}{
			"path": "src/a.swift",
			"kind": "edit",
			"diff": "+line\n-old\n",
		},
	}
	r.Apply(ev(3, backendID, sessionID, "tool_finished", map[string]interface{}{
		"itemId":      callID,
		"toolName":    "Patch",
		"toolStatus":  "completed",
		"toolResult":  "ok",
		"title":       "src/a.swift",
		"fileChanges": changes,
	}))

	snap, ok := r.Snapshot(backendID, sessionID)
	if !ok {
		t.Fatal("Snapshot missing")
	}
	if len(snap.Turns) != 1 || snap.Turns[0].Assistant == nil {
		t.Fatalf("want 1 turn with assistant, got %+v", snap.Turns)
	}
	var tool *ProjectionPart
	for i := range snap.Turns[0].Assistant.Parts {
		p := &snap.Turns[0].Assistant.Parts[i]
		if p.Type == "tool" && p.ItemID == callID {
			tool = p
			break
		}
	}
	if tool == nil {
		t.Fatal("tool part missing from snapshot")
	}
	if tool.Title != "src/a.swift" {
		t.Errorf("part.Title = %q, want src/a.swift (reducer must retain title)", tool.Title)
	}
	if tool.FileChanges == nil {
		t.Fatal("part.FileChanges is nil (reducer must retain fileChanges)")
	}
	// Round-trip through JSON like get_session_projection wire does.
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SessionProjection
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var wireTool *ProjectionPart
	for i := range decoded.Turns[0].Assistant.Parts {
		p := &decoded.Turns[0].Assistant.Parts[i]
		if p.Type == "tool" && p.ItemID == callID {
			wireTool = p
			break
		}
	}
	if wireTool == nil {
		t.Fatal("tool part missing after JSON round-trip")
	}
	if wireTool.Title != "src/a.swift" {
		t.Errorf("JSON title = %q, want src/a.swift", wireTool.Title)
	}
	if wireTool.FileChanges == nil {
		t.Error("JSON fileChanges nil after round-trip")
	}
}

func TestReducerToolEvents_AbsentTitleFileChangesNotFabricated(t *testing.T) {
	r := newTestReducer()
	const backendID = "claude"
	const sessionID = "s-no-fc"
	const turnID = "turn-bash"
	const callID = "call-bash"

	r.Apply(ev(1, backendID, sessionID, "user_message", map[string]interface{}{
		"itemId": turnID, "turnId": turnID, "text": "run",
	}))
	r.Apply(ev(2, backendID, sessionID, "tool_started", map[string]interface{}{
		"itemId": callID, "toolName": "Bash", "toolInput": "echo hi",
	}))
	r.Apply(ev(3, backendID, sessionID, "tool_finished", map[string]interface{}{
		"itemId": callID, "toolName": "Bash", "toolStatus": "completed", "toolResult": "hi",
	}))
	snap, ok := r.Snapshot(backendID, sessionID)
	if !ok {
		t.Fatal("Snapshot missing")
	}
	if snap.Turns[0].Assistant == nil || len(snap.Turns[0].Assistant.Parts) == 0 {
		t.Fatal("missing tool part")
	}
	part := snap.Turns[0].Assistant.Parts[0]
	if part.Title != "" {
		t.Errorf("Title fabricated = %q, want empty", part.Title)
	}
	if part.FileChanges != nil {
		t.Errorf("FileChanges fabricated = %#v, want nil", part.FileChanges)
	}
}
