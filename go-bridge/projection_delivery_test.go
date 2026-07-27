package gobridge

import (
	"testing"
	"time"
)

// projectionPatchFrames extracts the projection_patch EventMessages a capture conn received.
func projectionPatchFrames(frames []interface{}) []EventMessage {
	var out []EventMessage
	for _, f := range frames {
		if em, ok := f.(EventMessage); ok && em.Event == "projection_patch" {
			out = append(out, em)
		}
	}
	return out
}

// waitForProjectionPatches polls the capture conn until it has at least min projection_patch
// frames (delivery is async via the sink goroutine), returning them.
func waitForProjectionPatches(t *testing.T, conn *publisherCaptureConn, min int) []EventMessage {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		got := projectionPatchFrames(conn.snapshot())
		if len(got) >= min {
			return got
		}
		select {
		case <-conn.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %d projection_patch frames; got %d", min, len(got))
		}
	}
}

// TestProjectionPatchDeliveredToV2ConnOnly: a subscribed syncV2 conn receives projection_patch
// frames; a subscribed legacy conn receives NONE (raw path unchanged) — the dual-publish model
// (design §9.3: server emits patch to v2; legacy keeps raw).
func TestProjectionPatchDeliveredToV2ConnOnly(t *testing.T) {
	broadcaster := NewBroadcaster()
	ep := NewEventPublisher("epoch-emit", broadcaster)
	key := SubscriptionKey{BackendID: "codex", SessionID: "s1"}

	v2 := newPublisherCaptureConn(nil)
	v2.device = &TrustedDeviceRecord{DeviceID: "dev-v2"}
	legacy := newPublisherCaptureConn(nil)
	legacy.device = &TrustedDeviceRecord{DeviceID: "dev-legacy"}
	broadcaster.Subscribe(v2, key)
	broadcaster.Subscribe(legacy, key)
	ep.SetConnSyncV2(v2, true) // legacy intentionally not marked

	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": "Hello"}, Broadcast: true})

	patches := waitForProjectionPatches(t, v2, 2)
	if len(patches) != 2 {
		t.Fatalf("v2 received %d projection_patch frames, want exactly 2 (one per event)", len(patches))
	}

	// syncRev is monotonic and == perSessionSeq (1 then 2).
	if patches[0].PerSessionSeq != 1 || patches[1].PerSessionSeq != 2 {
		t.Fatalf("syncRev sequence = %d,%d, want 1,2", patches[0].PerSessionSeq, patches[1].PerSessionSeq)
	}

	// Legacy conn receives zero projection_patch frames (it is never a syncV2 target, so
	// deliverProjectionPatchLocked skips it — only raw reaches it, via the unchanged path).
	time.Sleep(150 * time.Millisecond)
	if got := len(projectionPatchFrames(legacy.snapshot())); got != 0 {
		t.Fatalf("legacy conn received %d projection_patch frames (must be 0)", got)
	}
}

// TestProjectionPatchCarriesContent: the text_delta patch carries an append_text partOp with the
// delta text, and the turn_started patch carries the turn upsert — proving the reduce output is
// delivered intact over the funnel (Phase 1 "reduce correctness over the live funnel" proof).
func TestProjectionPatchCarriesContent(t *testing.T) {
	broadcaster := NewBroadcaster()
	ep := NewEventPublisher("epoch-emit", broadcaster)
	v2 := newPublisherCaptureConn(nil)
	v2.device = &TrustedDeviceRecord{DeviceID: "dev-v2"}
	broadcaster.Subscribe(v2, SubscriptionKey{BackendID: "codex", SessionID: "s1"})
	ep.SetConnSyncV2(v2, true)

	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": "Hello"}, Broadcast: true})
	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": " world"}, Broadcast: true})

	patches := waitForProjectionPatches(t, v2, 3)

	// First patch: turn lifecycle upsert.
	first := patches[0].Data.(ProjectionPatch)
	if len(first.UpsertTurns) != 1 || first.UpsertTurns[0].TurnID != "T1" || first.UpsertTurns[0].Status != "running" {
		t.Fatalf("first patch upsertTurns = %+v", first.UpsertTurns)
	}

	// Concatenate all append_text payloads across the content patches — must equal the deltas.
	var combined string
	for _, p := range patches[1:] {
		pp := p.Data.(ProjectionPatch)
		for _, op := range pp.PartOps {
			if op.Op == "append_text" {
				combined += op.Text
			}
		}
	}
	if combined != "Hello world" {
		t.Fatalf("delivered append_text = %q, want %q", combined, "Hello world")
	}
}

// TestProjectionPatchNoSubscriberNoCrash: emitting with no v2 subscriber is a safe no-op.
func TestProjectionPatchNoSubscriberNoCrash(t *testing.T) {
	broadcaster := NewBroadcaster()
	ep := NewEventPublisher("epoch-emit", broadcaster)
	// No subscribers, no v2 conns.
	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	// Reducer still advanced (pull works even with no live subscriber).
	if rev := ep.ProjectionHeadRev("codex", "s1"); rev != 1 {
		t.Fatalf("headRev = %d, want 1", rev)
	}
}

// TestSetConnSyncV2TogglesMarking: SetConnSyncV2(false) removes the conn from the v2 set so it
// stops receiving projection_patch (e.g. on capability downgrade).
func TestSetConnSyncV2TogglesMarking(t *testing.T) {
	broadcaster := NewBroadcaster()
	ep := NewEventPublisher("epoch-emit", broadcaster)
	v2 := newPublisherCaptureConn(nil)
	v2.device = &TrustedDeviceRecord{DeviceID: "dev-v2"}
	broadcaster.Subscribe(v2, SubscriptionKey{BackendID: "codex", SessionID: "s1"})
	ep.SetConnSyncV2(v2, true)
	ep.SetConnSyncV2(v2, false) // downgrade

	ep.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	// Give async delivery a moment; expect zero projection_patch frames after downgrade.
	time.Sleep(100 * time.Millisecond)
	if got := len(projectionPatchFrames(v2.snapshot())); got != 0 {
		t.Fatalf("downgraded conn received %d projection_patch frames (must be 0)", got)
	}
}

// TestDurableOfflineTurnCompletedDeliversIdleProjectionPatch proves the K4 G4 contract:
// live turn_completed is stamped Offline=true (IsDurableMilestone / mailbox path), but the
// projection SoT stream must still fan out execution.phase=idle to online v2 observers.
// Offline only means "also durable-route raw event", not "skip live projection_patch".
func TestDurableOfflineTurnCompletedDeliversIdleProjectionPatch(t *testing.T) {
	broadcaster := NewBroadcaster()
	ep := NewEventPublisher("epoch-idle-contract", broadcaster)
	v2 := newPublisherCaptureConn(nil)
	v2.device = &TrustedDeviceRecord{DeviceID: "dev-v2-idle"}
	broadcaster.Subscribe(v2, SubscriptionKey{BackendID: "codex", SessionID: "s-idle"})
	ep.SetConnSyncV2(v2, true)

	ep.PublishLogical(LogicalEvent{
		BackendID: "codex",
		SessionID: "s-idle",
		Event:     "turn_started",
		Data:      map[string]interface{}{"turnId": "T-idle"},
		Broadcast: true,
	})
	ep.PublishLogical(LogicalEvent{
		BackendID: "codex",
		SessionID: "s-idle",
		Event:     "text_delta",
		Data:      map[string]interface{}{"itemId": "T-idle", "delta": "ok"},
		Broadcast: true,
	})
	// Mirror production sendSessionEvent: durable milestone Offline=true.
	ep.PublishLogical(LogicalEvent{
		BackendID: "codex",
		SessionID: "s-idle",
		Event:     "turn_completed",
		Data:      map[string]interface{}{"turnId": "T-idle", "done": true},
		Broadcast: true,
		Offline:   true,
	})

	patches := waitForProjectionPatches(t, v2, 3)
	last := patches[len(patches)-1].Data.(ProjectionPatch)
	if last.Execution == nil || last.Execution.Phase != "idle" {
		t.Fatalf("final patch execution = %+v, want phase idle under Offline durable stamp", last.Execution)
	}
	if last.SyncRev < 3 {
		t.Fatalf("final syncRev = %d, want >= 3", last.SyncRev)
	}
}
