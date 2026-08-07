package gobridge

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
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

// Pending→real rebind must rewrite observation + broadcaster so live targets
// exist under the real id (Codex lazy create first-turn blank body).
func TestRebindSessionIDIfResolvedRewritesObservationAndLiveTargets(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	device := &TrustedDeviceRecord{DeviceID: "dev-pending-rebind"}
	conn := &relayBroadcastCaptureConn{device: device}
	globalDeviceConnRegistry.Register(device.DeviceID, conn)
	defer globalDeviceConnRegistry.Unregister(device.DeviceID, conn)

	const pendingID = "pending-first-turn"
	const realID = "019fdc9f-real-thread"

	// Client still observes the pending id (pre-lease-renew).
	h.observation.SetScope(device.DeviceID, ObservationScope{
		BackendID: "codex", SessionIDs: []string{pendingID}, DeliveryMode: scopeFullStream, LeaseSeconds: 90,
	})
	h.broadcaster.Subscribe(conn, SubscriptionKey{BackendID: "codex", SessionID: pendingID})

	// Registry entry under pending (as StartSession/put does).
	// Non-nil events so Handlers.Shutdown closeWithTimeout does not panic.
	sess := &fakeAgentSession{id: realID, events: make(chan core.Event)}
	h.sessions.put(pendingID, "codex", "/tmp/ws", sess)

	got := h.rebindSessionIDIfResolved(pendingID, sess, realID, "codex", "/tmp/ws")
	if got != realID {
		t.Fatalf("rebind returned %q, want %q", got, realID)
	}

	scope := h.observation.GetScope(device.DeviceID, "codex")
	if scope == nil || len(scope.SessionIDs) != 1 || scope.SessionIDs[0] != realID {
		t.Fatalf("observation scope after rebind = %#v, want [%s]", scope, realID)
	}
	if h.broadcaster.HasSessionSubscriber("codex", pendingID) {
		t.Fatal("broadcaster still has pending subscriber")
	}
	if !h.broadcaster.HasSessionSubscriber("codex", realID) {
		t.Fatal("broadcaster missing real-id subscriber")
	}
	if !h.observation.ShouldSendEvent(device.DeviceID, "codex", realID, "projection_patch") {
		t.Fatal("projection_patch for real id must pass observation after rebind")
	}
	targets := h.broadcaster.Targets("codex", realID, "")
	if len(targets) == 0 {
		t.Fatal("live targets for real id must be non-empty after rebind")
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

func TestRebindLiveTargetsForSessionDoesNotSubscribeNonObserver(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	device := &TrustedDeviceRecord{DeviceID: "dev-rebind-non-observer"}
	conn := &relayBroadcastCaptureConn{device: device}
	globalDeviceConnRegistry.Register(device.DeviceID, conn)
	defer globalDeviceConnRegistry.Unregister(device.DeviceID, conn)
	h.observation.SetScope(device.DeviceID, ObservationScope{
		BackendID: "codex", SessionIDs: []string{"sess-other"}, DeliveryMode: scopeFullStream, LeaseSeconds: 90,
	})

	if n := h.rebindLiveTargetsForSession("codex", "sess-target"); n != 0 {
		t.Fatalf("rebound non-observer conns = %d, want 0", n)
	}
	if h.broadcaster.HasSessionSubscriber("codex", "sess-target") {
		t.Fatal("non-observing device must not acquire a session subscription")
	}
}

func TestRebindLiveTargetsForSessionPreservesNegotiatedV2Ownership(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	legacyDevice := &TrustedDeviceRecord{DeviceID: "dev-rebind-legacy"}
	legacy := &relayBroadcastCaptureConn{device: legacyDevice}
	v2Device := &TrustedDeviceRecord{DeviceID: "dev-rebind-v2"}
	v2 := &relayBroadcastCaptureConn{device: v2Device}
	for _, item := range []struct {
		device *TrustedDeviceRecord
		conn   *relayBroadcastCaptureConn
	}{
		{legacyDevice, legacy},
		{v2Device, v2},
	} {
		globalDeviceConnRegistry.Register(item.device.DeviceID, item.conn)
		defer globalDeviceConnRegistry.Unregister(item.device.DeviceID, item.conn)
		h.observation.SetScope(item.device.DeviceID, ObservationScope{
			BackendID: "codex", SessionIDs: []string{"sess-z"}, DeliveryMode: scopeFullStream, LeaseSeconds: 90,
		})
	}
	h.eventPublisher.SetConnSyncV2(v2, true)

	if n := h.rebindLiveTargetsForSession("codex", "sess-z"); n != 2 {
		t.Fatalf("rebound conns = %d, want 2", n)
	}
	if h.eventPublisher.ConnSyncV2(legacy) {
		t.Fatal("rebind must not upgrade a legacy connection to session_sync_v2")
	}
	if !h.eventPublisher.ConnSyncV2(v2) {
		t.Fatal("rebind must preserve the v2 mark negotiated by hello")
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
