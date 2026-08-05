package claudecode

import "testing"

// TestWireDescriptor (§6.2) asserts claudecode's self-described static wire attributes.
// Uses a zero-value Agent — WireDescriptor is a pure struct literal, no CLI/env needed,
// so this is robust in CI (unlike New(), which probes PATH).
func TestWireDescriptor(t *testing.T) {
	d := (&Agent{}).WireDescriptor()
	if d == nil {
		t.Fatal("WireDescriptor() = nil, want non-nil")
	}
	if d.Kind != "claude_code" {
		t.Errorf("Kind = %q, want claude_code", d.Kind)
	}
	if d.DisplayName != "Claude Code" {
		t.Errorf("DisplayName = %q, want Claude Code", d.DisplayName)
	}
	if d.LiveEventModel != "session_process" {
		t.Errorf("LiveEventModel = %q, want session_process", d.LiveEventModel)
	}
	if !d.RequiresExternalTurnPolling {
		t.Error("RequiresExternalTurnPolling = false, want true")
	}
	want := map[string]bool{"content_chunking": true, "question_reply": true, "external_turn_streaming": true}
	for _, c := range d.StaticCapabilities {
		delete(want, c)
	}
	if len(want) > 0 {
		t.Errorf("StaticCapabilities missing %v (got %v)", want, d.StaticCapabilities)
	}
}
