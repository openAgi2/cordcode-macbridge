package gobridge

import (
	"context"
	"testing"
)

// Re-handshake must move session subscriptions to the new Connection and must
// NOT wipe device-scoped observation (full_stream). The previous
// unregisterConnection→RemoveDevice path left a window where only durable
// milestones were deliverable until iOS renewed set_observation_scope.
func TestReplaceConnectionPreservesObservationAndTransfersSubscriptions(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	device := &TrustedDeviceRecord{DeviceID: "dev-replace"}
	old := &relayBroadcastCaptureConn{device: device}
	next := &relayBroadcastCaptureConn{device: device}

	h.registerConnection(old)
	key := SubscriptionKey{BackendID: "codex", SessionID: "sess-live"}
	h.broadcaster.Subscribe(old, key)
	h.observation.SetScope("dev-replace", ObservationScope{
		BackendID:    "codex",
		SessionIDs:   []string{"sess-live"},
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 45,
	})

	h.replaceConnection(old, next)

	if h.observation.GetScope("dev-replace", "codex") == nil {
		t.Fatal("observation scope cleared on replaceConnection (must keep full_stream)")
	}
	if !h.observation.ShouldSendEvent("dev-replace", "codex", "sess-live", "text_delta") {
		t.Fatal("text_delta must still pass after replaceConnection")
	}

	targets := h.broadcaster.Targets("codex", "sess-live", "")
	if len(targets) != 1 || targets[0] != next {
		t.Fatalf("targets after transfer = %#v, want [next]", targets)
	}

	// Old connection must no longer be targeted (avoids SendJSON on closed conn).
	for _, conn := range targets {
		if conn == old {
			t.Fatal("old connection still in targets after transfer")
		}
	}
}

func TestUnregisterLastConnectionPreservesObservationForPathSwitch(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	device := &TrustedDeviceRecord{DeviceID: "dev-gone"}
	conn := &relayBroadcastCaptureConn{device: device}
	h.registerConnection(conn)
	h.observation.SetScope("dev-gone", ObservationScope{
		BackendID: "codex", SessionIDs: []string{"sess-live"}, DeliveryMode: scopeFullStream, LeaseSeconds: 45,
	})
	h.unregisterConnection(conn)
	// Observation must survive the zero-connection gap of a path switch so the next
	// registerConnection can rebind session subscriptions without waiting for RPC.
	if h.observation.GetScope("dev-gone", "codex") == nil {
		t.Fatal("last disconnect must preserve observation for reconnect rebind")
	}
}

// registerConnection after a hard disconnect must re-Subscribe sessions from the
// surviving device observation — without requiring set_observation_scope first.
func TestRegisterConnectionResubscribesFromObservation(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	device := &TrustedDeviceRecord{DeviceID: "dev-rebind"}
	old := &relayBroadcastCaptureConn{device: device}
	h.registerConnection(old)
	h.observation.SetScope("dev-rebind", ObservationScope{
		BackendID:    "codex",
		SessionIDs:   []string{"sess-a", "sess-b"},
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 90,
	})
	h.broadcaster.Subscribe(old, SubscriptionKey{BackendID: "codex", SessionID: "sess-a"})
	h.unregisterConnection(old)

	// Gap: zero connections, observation retained.
	if h.broadcaster.HasSessionSubscriber("codex", "sess-a") {
		t.Fatal("old conn unsubscribed; no live subscriber expected in the gap")
	}

	next := &relayBroadcastCaptureConn{device: device}
	h.registerConnection(next)

	if !h.broadcaster.HasSessionSubscriber("codex", "sess-a") {
		t.Fatal("registerConnection must resubscribe sess-a from observation")
	}
	if !h.broadcaster.HasSessionSubscriber("codex", "sess-b") {
		t.Fatal("registerConnection must resubscribe sess-b from observation")
	}
	targets := h.broadcaster.Targets("codex", "sess-a", "")
	if len(targets) != 1 || targets[0] != next {
		t.Fatalf("targets after register rebind = %#v, want [next]", targets)
	}
	// Live text must still pass observation (full_stream retained).
	if !h.observation.ShouldSendEvent("dev-rebind", "codex", "sess-a", "text_delta") {
		t.Fatal("text_delta must pass after path-switch rebind")
	}
}

// rebindLiveTargetsForSession must re-Subscribe open device conns when the
// broadcaster lost session keys (zero-target mid-turn recovery).
func TestRebindLiveTargetsForSessionRestoresSubscriber(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	device := &TrustedDeviceRecord{DeviceID: "dev-rebind-zero"}
	conn := &relayBroadcastCaptureConn{device: device}
	// Registry has the conn, but broadcaster has no session key (simulates thrash).
	globalDeviceConnRegistry.Register("dev-rebind-zero", conn)
	defer globalDeviceConnRegistry.Unregister("dev-rebind-zero", conn)
	h.observation.SetScope("dev-rebind-zero", ObservationScope{
		BackendID: "codex", SessionIDs: []string{"sess-z"}, DeliveryMode: scopeFullStream, LeaseSeconds: 90,
	})
	if h.broadcaster.HasSessionSubscriber("codex", "sess-z") {
		t.Fatal("precondition: no subscriber yet")
	}
	n := h.rebindLiveTargetsForSession("codex", "sess-z")
	if n < 1 {
		t.Fatalf("rebind conns = %d, want >= 1", n)
	}
	if !h.broadcaster.HasSessionSubscriber("codex", "sess-z") {
		t.Fatal("rebind must restore session subscriber")
	}
}

func TestBroadcasterTransferSubscriptionsMovesKeys(t *testing.T) {
	b := NewBroadcaster()
	old := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "d1"}}
	next := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "d1"}}
	key := SubscriptionKey{BackendID: "codex", SessionID: "s1"}
	b.Subscribe(old, key)
	b.TransferSubscriptions(old, next)

	targets := b.Targets("codex", "s1", "")
	if len(targets) != 1 || targets[0] != next {
		t.Fatalf("targets = %#v, want next only", targets)
	}
	// UnsubscribeAll on old must be a no-op for the transferred key.
	b.UnsubscribeAll(old)
	targets = b.Targets("codex", "s1", "")
	if len(targets) != 1 || targets[0] != next {
		t.Fatalf("after UnsubscribeAll(old) targets = %#v, want next", targets)
	}
}
