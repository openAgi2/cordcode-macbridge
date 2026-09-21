package gobridge

import (
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Parity plan v2.12 slice P2+S1 (§4.8 gate ⑨ transport e2e): the structured
// display payloads (fileDisplay / editRegions / detailUnavailable) and the
// official per-file counts (additions / deletions) must survive every hop —
// wire serialization, reducer ingest + merge + clone, hydrate copy, and the
// turn budget (double-inline accounting).

func intPtr(v int) *int { return &v }

// TestFileChangesToWireOfficialCounts: present values (including a legitimate
// 0) are written verbatim; nil stays omitted.
func TestFileChangesToWireOfficialCounts(t *testing.T) {
	wire := fileChangesToWire([]core.FileChange{
		{Path: "zero.go", Kind: "edit", Additions: intPtr(0), Deletions: intPtr(0)},
		{Path: "nil.go", Kind: "edit"},
		{Path: "mixed.go", Kind: "edit", Additions: intPtr(2)},
	})
	if len(wire) != 3 {
		t.Fatalf("wire = %d entries, want 3", len(wire))
	}
	if wire[0]["additions"] != 0 || wire[0]["deletions"] != 0 {
		t.Fatalf("zero counts = %#v / %#v, want verbatim 0 / 0", wire[0]["additions"], wire[0]["deletions"])
	}
	if _, present := wire[1]["additions"]; present {
		t.Fatalf("nil additions unexpectedly present: %#v", wire[1]["additions"])
	}
	if _, present := wire[1]["deletions"]; present {
		t.Fatalf("nil deletions unexpectedly present: %#v", wire[1]["deletions"])
	}
	if wire[2]["additions"] != 2 {
		t.Fatalf("mixed additions = %#v, want 2", wire[2]["additions"])
	}
	if _, present := wire[2]["deletions"]; present {
		t.Fatalf("mixed deletions unexpectedly present: %#v", wire[2]["deletions"])
	}
}

// TestReducerToolEventStructuredDisplayFields (gate ②): tool events carrying
// the three structured payloads on Data must land on the Snapshot part.
func TestReducerToolEventStructuredDisplayFields(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "opencode", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "opencode", "s1", "tool_started", map[string]interface{}{
		"itemId":   "tool-1",
		"toolName": "read",
		"fileDisplay": map[string]interface{}{
			"path": "a.txt", "text": "hello\n", "lineStart": 1, "lineEnd": 1, "totalLines": 1,
		},
		"editRegions": []interface{}{
			map[string]interface{}{"path": "b.txt", "newText": "x"},
		},
		"detailUnavailable": map[string]interface{}{"path": "c.txt"},
	}))
	proj, ok := r.Snapshot("opencode", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	part := findToolPart(t, proj, "tool-1")
	if part.FileDisplay == nil {
		t.Fatalf("FileDisplay dropped by reducer: %+v", part)
	}
	if part.EditRegions == nil {
		t.Fatalf("EditRegions dropped by reducer: %+v", part)
	}
	if part.DetailUnavailable == nil {
		t.Fatalf("DetailUnavailable dropped by reducer: %+v", part)
	}
}

// TestMergeToolPartDisplayPayloadNonNil (gate ⑦): non-nil wins — a later
// tool_finished upsert without the payloads must not erase what tool_started
// carried; a later upsert WITH them overwrites.
func TestMergeToolPartDisplayPayloadNonNil(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "opencode", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "opencode", "s1", "tool_started", map[string]interface{}{
		"itemId":     "tool-1",
		"toolName":  "read",
		"fileDisplay": map[string]interface{}{"path": "a.txt", "text": "one\n", "lineStart": 1, "lineEnd": 1, "totalLines": 1},
		"detailUnavailable": map[string]interface{}{"path": "c.txt"},
	}))
	// finished without the payloads: must NOT reset.
	r.Apply(ev(3, "opencode", "s1", "tool_finished", map[string]interface{}{
		"itemId":     "tool-1",
		"toolStatus": "completed",
		"toolResult": map[string]interface{}{"kind": "inline", "text": "raw"},
	}))
	proj, _ := r.Snapshot("opencode", "s1")
	part := findToolPart(t, proj, "tool-1")
	if part.FileDisplay == nil {
		t.Fatal("merge reset FileDisplay (non-nil semantics violated)")
	}
	if part.DetailUnavailable == nil {
		t.Fatal("merge reset DetailUnavailable (non-nil semantics violated)")
	}
	// finished WITH a new payload: overwrites.
	r.Apply(ev(4, "opencode", "s1", "tool_finished", map[string]interface{}{
		"itemId":     "tool-1",
		"toolStatus": "completed",
		"fileDisplay": map[string]interface{}{"path": "z.txt", "text": "two\n", "lineStart": 1, "lineEnd": 1, "totalLines": 1},
	}))
	proj2, _ := r.Snapshot("opencode", "s1")
	part2 := findToolPart(t, proj2, "tool-1")
	display, ok := part2.FileDisplay.(map[string]interface{})
	if !ok || display["path"] != "z.txt" {
		t.Fatalf("later non-nil FileDisplay did not win: %#v", part2.FileDisplay)
	}
}

// TestCloneProjectionPartDisplayPayloadDeepCopy: Snapshot clones must not alias
// the reducer's maps — later reduce mutations must not leak into delivered
// snapshots.
func TestCloneProjectionPartDisplayPayloadDeepCopy(t *testing.T) {
	source := ProjectionPart{
		Type: "tool",
		FileDisplay: map[string]interface{}{
			"path": "a.txt",
			"nested": map[string]interface{}{"text": "x"},
		},
		EditRegions: []interface{}{
			map[string]interface{}{"path": "b.txt", "newText": "y"},
		},
		DetailUnavailable: map[string]interface{}{"path": "c.txt"},
	}
	clone := cloneProjectionPart(source)
	display := clone.FileDisplay.(map[string]interface{})
	display["path"] = "mutated"
	display["nested"].(map[string]interface{})["text"] = "mutated"
	regions := clone.EditRegions.([]interface{})
	regions[0].(map[string]interface{})["newText"] = "mutated"
	clone.DetailUnavailable.(map[string]interface{})["path"] = "mutated"

	original := source.FileDisplay.(map[string]interface{})
	if original["path"] != "a.txt" {
		t.Fatalf("clone aliased FileDisplay: %#v", source.FileDisplay)
	}
	if original["nested"].(map[string]interface{})["text"] != "x" {
		t.Fatalf("clone aliased nested FileDisplay map")
	}
	if source.EditRegions.([]interface{})[0].(map[string]interface{})["newText"] != "y" {
		t.Fatalf("clone aliased EditRegions")
	}
	if source.DetailUnavailable.(map[string]interface{})["path"] != "c.txt" {
		t.Fatalf("clone aliased DetailUnavailable")
	}
}

// TestHydrateToolEventsFromStepStructuredFields (gate ①): hydrate emits the
// structured payloads on BOTH tool_started and tool_finished so the completed
// activity row keeps them.
func TestHydrateToolEventsFromStepStructuredFields(t *testing.T) {
	step := map[string]any{
		"id":       "tool-9",
		"toolName": "read",
		"status":   "completed",
		"fileDisplay": map[string]any{
			"path": "a.txt", "text": "hi\n", "lineStart": 1, "lineEnd": 1, "totalLines": 1,
		},
		"editRegions": []any{
			map[string]any{"path": "b.txt", "newText": "n"},
		},
		"detailUnavailable": map[string]any{"path": "c.txt"},
	}
	events := hydrateToolEventsFromStep(step)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	for _, event := range events {
		if event.Event != "tool_started" && event.Event != "tool_finished" {
			t.Fatalf("unexpected event %q", event.Event)
		}
		for _, field := range []string{"fileDisplay", "editRegions", "detailUnavailable"} {
			if _, ok := event.Data[field]; !ok {
				t.Fatalf("%s missing %s: %+v", event.Event, field, event.Data)
			}
		}
	}
}

// TestProjectionTurnExceedsCountsDisplayPayloads (§4.8-11 double-inline): the
// budget consumes the structured display payloads — clean field and raw output
// both count (R51 double-inline accounting).
func TestProjectionTurnExceedsCountsDisplayPayloads(t *testing.T) {
	turn := &TurnProjection{
		TurnID: "T1",
		Status: "completed",
		Assistant: &MessageProjection{
			Role: "assistant",
			Parts: []ProjectionPart{
				{
					Type: "tool",
					ToolResult: map[string]interface{}{
						"kind": "inline",
						"text": strings.Repeat("x", 100),
					},
					FileDisplay: map[string]interface{}{
						"path":       "a.txt",
						"text":       strings.Repeat("y", 100),
						"lineStart":  1,
						"lineEnd":    1,
						"totalLines": 1,
					},
					EditRegions: []interface{}{
						map[string]interface{}{"path": "b.txt", "newText": strings.Repeat("z", 100)},
					},
					DetailUnavailable: map[string]interface{}{"path": "c.txt"},
				},
			},
		},
	}
	// 100 (toolResult) + 100 (fileDisplay text) + ~10 (display keys) +
	// 100 (editRegions newText) + ~20 (region/display keys) ≈ 330+ bytes.
	if !projectionTurnExceeds(turn, 300) {
		t.Fatal("budget did not consume display payloads (double-inline violated)")
	}
	// A generous budget must not trip.
	if projectionTurnExceeds(turn, 100000) {
		t.Fatal("generous budget tripped")
	}
}

func findToolPart(t *testing.T, proj SessionProjection, itemID string) ProjectionPart {
	t.Helper()
	for _, turn := range proj.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type == "tool" && part.ItemID == itemID {
				return part
			}
		}
	}
	t.Fatalf("tool part %q not found in projection", itemID)
	return ProjectionPart{}
}
