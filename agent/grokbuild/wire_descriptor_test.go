package grokbuild

import "testing"

// TestWireDescriptor (§6.2) asserts grokbuild's self-described static wire attributes.
// Uses a zero-value Agent — WireDescriptor is a pure struct literal, no CLI/env needed,
// so this is robust in CI (unlike New(), which probes PATH).
func TestWireDescriptor(t *testing.T) {
	d := (&Agent{}).WireDescriptor()
	if d == nil {
		t.Fatal("WireDescriptor() = nil, want non-nil")
	}
	if d.Kind != "grokbuild" {
		t.Errorf("Kind = %q, want grokbuild", d.Kind)
	}
	if d.DisplayName != "Grok Build" {
		t.Errorf("DisplayName = %q, want Grok Build", d.DisplayName)
	}
	if d.LiveEventModel != "session_process" {
		t.Errorf("LiveEventModel = %q, want session_process", d.LiveEventModel)
	}
	if !d.RequiresExternalTurnPolling {
		t.Error("RequiresExternalTurnPolling = false, want true")
	}
	// No A-class static positive capabilities today. nil is the honest self-description.
	if len(d.StaticCapabilities) != 0 {
		t.Errorf("StaticCapabilities = %v, want empty (no A-class caps yet)", d.StaticCapabilities)
	}
}
