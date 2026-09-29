package opencode

import "testing"

// TestWireDescriptor (§6.2) asserts opencode's self-described static wire attributes.
// Uses a zero-value Agent — WireDescriptor is a pure struct literal, no CLI/env needed,
// so this is robust in CI (unlike New(), which probes PATH).
func TestWireDescriptor(t *testing.T) {
	d := (&Agent{}).WireDescriptor()
	if d == nil {
		t.Fatal("WireDescriptor() = nil, want non-nil")
	}
	if d.Kind != "opencode" {
		t.Errorf("Kind = %q, want opencode", d.Kind)
	}
	if d.DisplayName != "OpenCode" {
		t.Errorf("DisplayName = %q, want OpenCode", d.DisplayName)
	}
	if d.LiveEventModel != "broadcast" {
		t.Errorf("LiveEventModel = %q, want broadcast (SSE push-native)", d.LiveEventModel)
	}
	if !d.RequiresExternalTurnPolling {
		t.Error("RequiresExternalTurnPolling = false, want true")
	}
	// todos 兜底: opencode does not implement TodoProvider but must advertise todos.
	want := map[string]bool{"todos": true}
	for _, c := range d.StaticCapabilities {
		delete(want, c)
	}
	if len(want) > 0 {
		t.Errorf("StaticCapabilities missing %v (got %v)", want, d.StaticCapabilities)
	}
	for _, c := range d.StaticCapabilities {
		switch c {
		case "external_turn_streaming", "question_reply", "content_chunking":
			t.Errorf("StaticCapabilities must not carry %q (got %v)", c, d.StaticCapabilities)
		}
	}
}
