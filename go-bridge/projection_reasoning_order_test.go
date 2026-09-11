package gobridge

import "testing"

func TestReducerPreservesDistinctReasoningItemOrder(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex-remote", "s", "turn_started", map[string]interface{}{"turnId": "T"}))
	r.Apply(ev(2, "codex-remote", "s", "reasoning_delta", map[string]interface{}{"turnId": "T", "itemId": "r1", "delta": "先思考"}))
	r.Apply(ev(3, "codex-remote", "s", "text_delta", map[string]interface{}{"turnId": "T", "itemId": "p1", "delta": "先说明", "presentation": "progress"}))
	r.Apply(ev(4, "codex-remote", "s", "reasoning_delta", map[string]interface{}{"turnId": "T", "itemId": "r2", "delta": "再思考"}))
	r.Apply(ev(5, "codex-remote", "s", "text_delta", map[string]interface{}{"turnId": "T", "itemId": "f1", "delta": "最终答案", "presentation": "final"}))

	projection, ok := r.Snapshot("codex-remote", "s")
	if !ok || len(projection.Turns) != 1 || projection.Turns[0].Assistant == nil {
		t.Fatalf("projection = %+v", projection)
	}
	parts := projection.Turns[0].Assistant.Parts
	if len(parts) != 4 {
		t.Fatalf("parts = %+v", parts)
	}
	wantTypes := []string{"reasoning", "text", "reasoning", "text"}
	wantIDs := []string{"r1", "p1", "r2", "f1"}
	for index := range wantTypes {
		if parts[index].Type != wantTypes[index] || parts[index].ItemID != wantIDs[index] {
			t.Fatalf("part %d = %+v, want %s/%s", index, parts[index], wantTypes[index], wantIDs[index])
		}
	}

	patch, ok := r.FlushPatch("codex-remote", "s")
	if !ok {
		t.Fatal("missing patch")
	}
	seen := map[string]string{}
	for _, op := range patch.PartOps {
		if op.Op == "set_thinking" {
			seen[op.ItemID] = op.Text
		}
	}
	if seen["r1"] != "先思考" || seen["r2"] != "再思考" {
		t.Fatalf("reasoning ops = %+v", seen)
	}
}
