package codexremote

import (
	"reflect"
	"testing"
)

// turn_detail_lazy_v1 descriptor declaration (unified-bridge-protocol §11.7 /
// lazy-history plan Phase 3): the iOS「加载详细过程」entry gates on THIS static
// capability claim (§6.2 A-class self-description), not on a hello echo — legacy
// codex-remote sessions answer request-level unsupported_capability and iOS marks
// the session detail-unsupported on first answer. The declaration must stay an
// exact singleton: session_sync_v2 / projection_window_v1 are bridge-negotiated
// capabilities and must NOT appear here.
func TestWireDescriptorDeclaresTurnDetailLazyV1(t *testing.T) {
	descriptor := (&Agent{}).WireDescriptor()
	if descriptor == nil {
		t.Fatal("WireDescriptor must self-describe")
	}
	want := []string{"turn_detail_lazy_v1"}
	if got := descriptor.StaticCapabilities; !reflect.DeepEqual(got, want) {
		t.Fatalf("StaticCapabilities = %v, want exactly %v (bridge-negotiated capabilities must not leak into the static list)", got, want)
	}
}
