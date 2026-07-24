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

func TestUnregisterLastConnectionStillClearsObservation(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	device := &TrustedDeviceRecord{DeviceID: "dev-gone"}
	conn := &relayBroadcastCaptureConn{device: device}
	h.registerConnection(conn)
	h.observation.SetScope("dev-gone", ObservationScope{
		BackendID: "codex", DeliveryMode: scopeFullStream, LeaseSeconds: 45,
	})
	h.unregisterConnection(conn)
	if h.observation.GetScope("dev-gone", "codex") != nil {
		t.Fatal("true disconnect must still clear observation")
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
