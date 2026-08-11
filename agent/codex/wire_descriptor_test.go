package codex

import "testing"

// TestWireDescriptor (§6.2) asserts codex's self-described static wire attributes.
// Uses a zero-value Agent — WireDescriptor is a pure struct literal, no CLI/env needed,
// so this is robust in CI (unlike New(), which probes PATH).
func TestWireDescriptor(t *testing.T) {
	d := (&Agent{}).WireDescriptor()
	if d == nil {
		t.Fatal("WireDescriptor() = nil, want non-nil")
	}
	if d.Kind != "codex" {
		t.Errorf("Kind = %q, want codex", d.Kind)
	}
	if d.DisplayName != "Codex" {
		t.Errorf("DisplayName = %q, want Codex", d.DisplayName)
	}
	if d.LiveEventModel != "session_process" {
		t.Errorf("LiveEventModel = %q, want session_process (broadcast is a runtime app_server override, not static)", d.LiveEventModel)
	}
	if d.RequiresExternalTurnPolling {
		t.Error("RequiresExternalTurnPolling = true, want false (transcript relay authoritative)")
	}
	// external_turn_streaming is static; compression/question_reply/structured_user_input_v1
	// are codex app_server mode-conditional and must NOT appear here (guarded by
	// go-bridge TestCodexNonAppServerNoModeCaps).
	want := map[string]bool{"external_turn_streaming": true}
	for _, c := range d.StaticCapabilities {
		delete(want, c)
	}
	if len(want) > 0 {
		t.Errorf("StaticCapabilities missing %v (got %v)", want, d.StaticCapabilities)
	}
	for _, c := range d.StaticCapabilities {
		switch c {
		case "compression", "question_reply", "structured_user_input_v1":
			t.Errorf("StaticCapabilities must not carry mode-conditional %q (got %v)", c, d.StaticCapabilities)
		}
	}
}
