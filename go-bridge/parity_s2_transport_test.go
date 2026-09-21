package gobridge

import (
	"strings"
	"testing"
)

// Parity plan v2.12 slice S2 (§4.8 discipline — transport e2e, not just the
// driver map): the claude-code structuredPatch fileChanges (path / kind /
// hunk-rendered diff / counted additions·deletions, and the create shape
// that carries NO counts) must survive every hop — hydrate copy and
// reducer ingest into the Snapshot part.

// claudeS2UpdateChanges mirrors what agent/claudecode's
// claudeFileChangesFromToolUseResult produces for an Edit with one hunk.
func claudeS2UpdateChanges() []map[string]any {
	return []map[string]any{{
		"path":      "src/Foo.swift",
		"kind":      "edit",
		"diff":      "@@ -1,1 +1,2 @@\n keep\n-x\n+y\n",
		"additions": 1,
		"deletions": 1,
	}}
}

// claudeS2CreateChanges mirrors the Write/create shape: path-bearing entry
// without counts (nil stats — never 0/0; R22).
func claudeS2CreateChanges() []map[string]any {
	return []map[string]any{{
		"path": "docs/new.md",
		"kind": "create",
	}}
}

// TestS2HydrateClaudeFileChangesSurvives (gate ①): the rich-history step map
// built by the claude transcript path carries fileChanges; hydrate must copy
// them onto BOTH tool_started and tool_finished — including the create shape
// whose counts are legitimately absent.
func TestS2HydrateClaudeFileChangesSurvives(t *testing.T) {
	for _, stepChanges := range [][]map[string]any{claudeS2UpdateChanges(), claudeS2CreateChanges()} {
		step := map[string]any{
			"id":           "tool-s2",
			"toolName":     "Edit",
			"status":       "completed",
			"fileChanges":  stepChanges,
		}
		events := hydrateToolEventsFromStep(step)
		if len(events) != 2 {
			t.Fatalf("events = %d, want 2", len(events))
		}
		for _, event := range events {
			carried, ok := event.Data["fileChanges"].([]map[string]any)
			if !ok || len(carried) != 1 {
				t.Fatalf("%s dropped claude fileChanges: %+v", event.Event, event.Data)
			}
			if carried[0]["path"] != stepChanges[0]["path"] {
				t.Fatalf("%s carried wrong path: %#v", event.Event, carried[0])
			}
			if _, hasCounts := carried[0]["additions"]; hasCounts != (stepChanges[0]["additions"] != nil) {
				t.Fatalf("%s counts presence mismatch: %#v", event.Event, carried[0])
			}
		}
	}
}

// TestS2ReducerClaudeFileChangesSnapshot (gate ②): a tool event carrying the
// claude-shaped fileChanges must land intact on the Snapshot part — counts,
// diff, and the count-less create shape all survive.
func TestS2ReducerClaudeFileChangesSnapshot(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "claude-code", "s2", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "claude-code", "s2", "tool_started", map[string]interface{}{
		"itemId":       "tool-s2-upd",
		"toolName":     "Edit",
		"fileChanges":  claudeS2UpdateChanges(),
	}))
	r.Apply(ev(3, "claude-code", "s2", "tool_started", map[string]interface{}{
		"itemId":       "tool-s2-create",
		"toolName":     "Write",
		"fileChanges":  claudeS2CreateChanges(),
	}))
	proj, ok := r.Snapshot("claude-code", "s2")
	if !ok {
		t.Fatal("no projection")
	}

	upd := findToolPart(t, proj, "tool-s2-upd")
	changes, ok := upd.FileChanges.([]map[string]any)
	if !ok || len(changes) != 1 {
		t.Fatalf("update part.FileChanges = %#v, want the claude entry", upd.FileChanges)
	}
	if changes[0]["additions"] != 1 || changes[0]["deletions"] != 1 {
		t.Fatalf("update counts = %#v / %#v, want 1 / 1", changes[0]["additions"], changes[0]["deletions"])
	}
	if diff, _ := changes[0]["diff"].(string); !strings.Contains(diff, "+y") {
		t.Fatalf("update diff = %q, want rendered hunk", diff)
	}

	create := findToolPart(t, proj, "tool-s2-create")
	createChanges, ok := create.FileChanges.([]map[string]any)
	if !ok || len(createChanges) != 1 {
		t.Fatalf("create part.FileChanges = %#v, want the path-bearing entry", create.FileChanges)
	}
	if _, has := createChanges[0]["additions"]; has {
		t.Fatalf("create entry must carry no counts (nil, never 0/0): %#v", createChanges[0])
	}
	if createChanges[0]["kind"] != "create" {
		t.Fatalf("create kind = %#v, want create", createChanges[0]["kind"])
	}
}
