package gobridge

import (
	"strings"
	"testing"
)

// TestCoalesceMixedContentInOnePatch: multiple deltas of different kinds (text, reasoning, tool)
// accumulated before a flush collapse into a SINGLE projection_patch with one partOp per kind.
// This is the coalesce contract (design §5.4). Phase 1 flushes per-event in production; the
// 80ms time-window ticker that would widen this window is a Phase 2 bandwidth optimization
// (design §2.3 — "带宽优化 … Phase 2+"). The batching machinery itself is delivered + tested here.
func TestCoalesceMixedContentInOnePatch(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "ab"}))
	r.Apply(ev(3, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "cd"}))
	r.Apply(ev(4, "codex", "s1", "reasoning_delta", map[string]interface{}{"itemId": "T1", "delta": "th"}))
	r.Apply(ev(5, "codex", "s1", "reasoning_delta", map[string]interface{}{"itemId": "T1", "delta": "ink"}))
	r.Apply(ev(6, "codex", "s1", "tool_started", map[string]interface{}{"itemId": "call_1", "toolName": "shell"}))

	patch, ok := r.FlushPatch("codex", "s1")
	if !ok {
		t.Fatal("expected a coalesced patch")
	}
	if patch.SyncRev != 5 {
		t.Fatalf("syncRev = %d, want 5 (turn_started no longer commits skeleton)", patch.SyncRev)
	}

	// One append_text carrying concatenated text deltas.
	var appendText string
	thinkText := ""
	toolOps := 0
	for _, op := range patch.PartOps {
		switch op.Op {
		case "append_text":
			appendText += op.Text
		case "set_thinking":
			thinkText = op.Text
		case "upsert_tool":
			toolOps++
		}
	}
	if appendText != "abcd" {
		t.Fatalf("coalesced append_text = %q, want %q", appendText, "abcd")
	}
	if thinkText != "think" {
		t.Fatalf("coalesced set_thinking = %q, want %q", thinkText, "think")
	}
	if toolOps != 1 {
		t.Fatalf("tool upserts = %d, want 1", toolOps)
	}
}

// TestCoalesceBufferIsInternalNotDistributed: the reducer-internal projection buffer (design
// §6.2 option b) surfaces ONLY via FlushPatch / Snapshot — there is no second outbound path.
// Asserted structurally: the projection state after the deltas equals what Snapshot returns,
// and FlushPatch is the sole producer of patches (a second FlushPatch with no new activity is empty).
func TestCoalesceBufferIsInternalNotDistributed(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "x"}))

	first, ok := r.FlushPatch("codex", "s1")
	if !ok || first.SyncRev != 1 {
		t.Fatalf("first flush = %+v ok=%v", first, ok)
	}
	// No new activity → second flush is empty (buffer was cleared; no separate distribution).
	if _, ok := r.FlushPatch("codex", "s1"); ok {
		t.Fatal("second flush with no activity must be empty (buffer cleared by first flush)")
	}
	// Snapshot still returns the authoritative full state (buffer is internal, projection persists).
	proj, _ := r.Snapshot("codex", "s1")
	if proj.SyncRev != 1 || len(proj.Turns) != 1 {
		t.Fatalf("snapshot after flush = %+v", proj)
	}
}

// TestCoalesceControlEventOrdersWithContent: a turn_completed after content is included in the
// same flush as the preceding content (control + content coexist in one patch), and the
// projection reflects completion.
func TestCoalesceControlEventOrdersWithContent(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "hi"}))
	r.Apply(ev(3, "codex", "s1", "turn_completed", map[string]interface{}{"turnId": "T1"}))

	patch, ok := r.FlushPatch("codex", "s1")
	if !ok {
		t.Fatal("expected a patch")
	}
	// The patch carries both the turn-completed upsert AND the content partOp.
	foundComplete := false
	foundText := false
	for _, tu := range patch.UpsertTurns {
		if tu.TurnID == "T1" && tu.Status == "completed" {
			foundComplete = true
		}
	}
	for _, op := range patch.PartOps {
		if op.Op == "append_text" && op.Text == "hi" {
			foundText = true
		}
	}
	if !foundComplete || !foundText {
		t.Fatalf("patch missing completion(%v) or text(%v): upserts=%+v partOps=%+v", foundComplete, foundText, patch.UpsertTurns, patch.PartOps)
	}
	// Execution reflects idle after completion.
	proj, _ := r.Snapshot("codex", "s1")
	if proj.Execution.Phase != "idle" {
		t.Fatalf("execution phase = %q, want idle", proj.Execution.Phase)
	}
}

// When no v2 observer is online, live projection state remains authoritative but
// its unsent patch accumulator must not grow for the duration of a turn. A later
// observer receives the current snapshot, then starts a fresh delta base.
func TestDropPendingPatchWithoutObserverRetainsSnapshot(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "before"}))
	if !r.DropPendingPatch("codex", "s1") {
		t.Fatal("expected pending patch to be discarded")
	}
	if _, ok := r.FlushPatch("codex", "s1"); ok {
		t.Fatal("discarded patch must not be emitted later")
	}
	projection, ok := r.Snapshot("codex", "s1")
	if !ok || projection.SyncRev != 1 {
		t.Fatalf("authoritative snapshot = %+v ok=%v", projection, ok)
	}

	r.Apply(ev(3, "codex", "s1", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "after"}))
	patch, ok := r.FlushPatch("codex", "s1")
	if !ok || patch.BaseRev != 1 || patch.SyncRev != 2 {
		t.Fatalf("fresh patch after discard = %+v ok=%v", patch, ok)
	}
	if len(patch.PartOps) != 1 || patch.PartOps[0].Text != "after" {
		t.Fatalf("fresh patch contains stale content: %+v", patch.PartOps)
	}
}

func TestPendingPatchExceedsDetectsLargeToolTurn(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex", "s-large", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "s-large", "tool_started", map[string]interface{}{
		"itemId": "call-1", "toolResult": strings.Repeat("x", 1024),
	}))
	if !r.PendingPatchExceeds("codex", "s-large", 512) {
		t.Fatal("large pending tool turn should exceed the no-observer threshold")
	}
}
