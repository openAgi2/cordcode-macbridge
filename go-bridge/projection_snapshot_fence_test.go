package gobridge

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

type snapshotFenceCaptureConn struct {
	mu     sync.Mutex
	frames []string
	closed bool
}

func (c *snapshotFenceCaptureConn) SendJSON(value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if event, ok := value.(EventMessage); ok {
		c.frames = append(c.frames, fmt.Sprintf("%s:%d", event.Event, event.PerSessionSeq))
		return
	}
	c.frames = append(c.frames, "json")
}

func (c *snapshotFenceCaptureConn) SendJSONClassified(value any, _ relayOutboundClass) {
	c.SendJSON(value)
}

func (c *snapshotFenceCaptureConn) SendResult(requestID string, _ interface{}, _ *WireError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.frames = append(c.frames, "result:"+requestID)
	}
}

func (c *snapshotFenceCaptureConn) AuthedDevice() *TrustedDeviceRecord { return nil }
func (c *snapshotFenceCaptureConn) RemoteAddr() string                 { return "snapshot-fence-test" }
func (c *snapshotFenceCaptureConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}
func (c *snapshotFenceCaptureConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
func (c *snapshotFenceCaptureConn) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.frames...)
}

func readyProjectionPublisher(t *testing.T, epoch string) (*Handlers, *EventPublisher) {
	t.Helper()
	handlers := NewHandlers()
	publisher := NewEventPublisher(epoch, handlers.broadcaster)
	handlers.installEventPublisher(publisher)
	publisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "s1", Event: "turn_started",
		Data: map[string]interface{}{"turnId": "T1"},
	})
	publisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "s1", Event: "text_delta",
		Data: map[string]interface{}{"itemId": "T1", "delta": "base"},
	})
	if err := handlers.ensureProjectionHydrated("codex", "s1", "", false); err != nil {
		t.Fatalf("commit pathless live projection: %v", err)
	}
	if status := handlers.projectionKernel.Status("codex", "s1"); status.Phase != ProjectionHydrateReady {
		t.Fatalf("fixture projection phase = %s, want ready", status.Phase)
	}
	return handlers, publisher
}

func beginFence(t *testing.T, publisher *EventPublisher, conn Connection) ProjectionSnapshotAdmission {
	t.Helper()
	publisher.RegisterConnection(conn)
	publisher.SetConnSyncV2(conn, true)
	publisher.broadcaster.Subscribe(conn, SubscriptionKey{BackendID: "codex", SessionID: "s1"})
	_, admission, err := publisher.BeginProjectionSnapshot(conn, "codex", "s1")
	if err != nil {
		t.Fatal(err)
	}
	return admission
}

func sequentialPatch(rev int) ProjectionPatch {
	return ProjectionPatch{BaseRev: rev - 1, SyncRev: rev}
}

func TestProjectionSnapshotFenceResponsePrecedesHigherRevisionPatches(t *testing.T) {
	_, publisher := readyProjectionPublisher(t, "epoch-fence-order")
	conn := &snapshotFenceCaptureConn{}
	admission := beginFence(t, publisher, conn)
	publisher.PublishProjectionPatch("codex", "s1", sequentialPatch(admission.CutRev+1))
	if got := conn.snapshot(); len(got) != 0 {
		t.Fatalf("patch escaped before response enqueue: %v", got)
	}
	if err := publisher.CompleteProjectionSnapshot(conn, admission, "r1", map[string]any{"projection": "snapshot"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshotFenceFrames(t, conn, 2)
	got := conn.snapshot()
	if got[0] != "result:r1" || got[1] != fmt.Sprintf("projection_patch:%d", admission.CutRev+1) {
		t.Fatalf("wire order = %v", got)
	}
}

func TestProjectionSnapshotFenceRetainsOneHundredSequentialPatches(t *testing.T) {
	_, publisher := readyProjectionPublisher(t, "epoch-fence-100")
	conn := &snapshotFenceCaptureConn{}
	admission := beginFence(t, publisher, conn)
	for rev := admission.CutRev + 1; rev <= admission.CutRev+100; rev++ {
		publisher.PublishProjectionPatch("codex", "s1", sequentialPatch(rev))
	}
	if err := publisher.CompleteProjectionSnapshot(conn, admission, "r100", nil); err != nil {
		t.Fatal(err)
	}
	waitSnapshotFenceFrames(t, conn, 101)
	got := conn.snapshot()
	if got[0] != "result:r100" || got[len(got)-1] != fmt.Sprintf("projection_patch:%d", admission.CutRev+100) {
		t.Fatalf("100-patch fence boundaries = first %q last %q count %d", got[0], got[len(got)-1], len(got))
	}
}

func TestProjectionSnapshotFenceOverflowInvalidatesAfterResponse(t *testing.T) {
	_, publisher := readyProjectionPublisher(t, "epoch-fence-overflow")
	conn := &snapshotFenceCaptureConn{}
	admission := beginFence(t, publisher, conn)
	for rev := admission.CutRev + 1; rev <= admission.CutRev+projectionFenceMaxPatches+1; rev++ {
		publisher.PublishProjectionPatch("codex", "s1", sequentialPatch(rev))
	}
	if err := publisher.CompleteProjectionSnapshot(conn, admission, "overflow", nil); err != nil {
		t.Fatal(err)
	}
	waitSnapshotFenceFrames(t, conn, 2)
	got := conn.snapshot()
	if len(got) != 2 || got[0] != "result:overflow" || got[1] != "sync_invalidate:0" {
		t.Fatalf("overflow wire result = %v", got)
	}
}

func TestProjectionSnapshotFenceBaseMismatchInvalidatesOnce(t *testing.T) {
	_, publisher := readyProjectionPublisher(t, "epoch-fence-gap")
	conn := &snapshotFenceCaptureConn{}
	admission := beginFence(t, publisher, conn)
	publisher.PublishProjectionPatch("codex", "s1", ProjectionPatch{
		BaseRev: admission.CutRev + 1,
		SyncRev: admission.CutRev + 2,
	})
	if err := publisher.CompleteProjectionSnapshot(conn, admission, "gap", nil); err != nil {
		t.Fatal(err)
	}
	waitSnapshotFenceFrames(t, conn, 2)
	publisher.PublishProjectionPatch("codex", "s1", ProjectionPatch{
		BaseRev: admission.CutRev + 2,
		SyncRev: admission.CutRev + 3,
	})
	time.Sleep(20 * time.Millisecond)
	got := conn.snapshot()
	if len(got) != 2 || got[0] != "result:gap" || got[1] != "sync_invalidate:0" {
		t.Fatalf("base mismatch must emit exactly one invalidate until repull: %v", got)
	}
}

func TestProjectionSnapshotFencesAreIndependentPerConnection(t *testing.T) {
	_, publisher := readyProjectionPublisher(t, "epoch-fence-independent")
	first := &snapshotFenceCaptureConn{}
	second := &snapshotFenceCaptureConn{}
	a := beginFence(t, publisher, first)
	b := beginFence(t, publisher, second)
	publisher.PublishProjectionPatch("codex", "s1", sequentialPatch(a.CutRev+1))
	if err := publisher.CompleteProjectionSnapshot(first, a, "a", nil); err != nil {
		t.Fatal(err)
	}
	waitSnapshotFenceFrames(t, first, 2)
	if got := second.snapshot(); len(got) != 0 {
		t.Fatalf("second connection released with first: %v", got)
	}
	if err := publisher.CompleteProjectionSnapshot(second, b, "b", nil); err != nil {
		t.Fatal(err)
	}
	waitSnapshotFenceFrames(t, second, 2)
}

func TestProjectionSnapshotFenceReconnectGetsNewGenerationAndDropsOldState(t *testing.T) {
	_, publisher := readyProjectionPublisher(t, "epoch-fence-reconnect")
	old := &snapshotFenceCaptureConn{}
	oldAdmission := beginFence(t, publisher, old)
	publisher.PublishProjectionPatch("codex", "s1", sequentialPatch(oldAdmission.CutRev+1))
	publisher.UnregisterConnection(old)
	if err := publisher.CompleteProjectionSnapshot(old, oldAdmission, "old", nil); err == nil {
		t.Fatal("old generation completion unexpectedly succeeded")
	}
	if !old.isClosed() {
		t.Fatal("old generation must reach explicit disconnect terminal state")
	}

	next := &snapshotFenceCaptureConn{}
	nextAdmission := beginFence(t, publisher, next)
	if nextAdmission.ConnectionGeneration <= oldAdmission.ConnectionGeneration {
		t.Fatalf("generation did not advance: old=%d new=%d", oldAdmission.ConnectionGeneration, nextAdmission.ConnectionGeneration)
	}
	if err := publisher.CompleteProjectionSnapshot(next, nextAdmission, "new", nil); err != nil {
		t.Fatal(err)
	}
	waitSnapshotFenceFrames(t, next, 1)
	if got := next.snapshot(); len(got) != 1 || got[0] != "result:new" {
		t.Fatalf("new generation did not converge from full snapshot: %v", got)
	}
}

func TestProjectionSnapshotFenceBridgeRestartChangesEpoch(t *testing.T) {
	_, before := readyProjectionPublisher(t, "epoch-before-restart")
	beforeAdmission := beginFence(t, before, &snapshotFenceCaptureConn{})
	_, after := readyProjectionPublisher(t, "epoch-after-restart")
	afterAdmission := beginFence(t, after, &snapshotFenceCaptureConn{})
	if beforeAdmission.BridgeEpoch == afterAdmission.BridgeEpoch {
		t.Fatalf("bridge restart reused epoch %q", beforeAdmission.BridgeEpoch)
	}
	if afterAdmission.CutRev != beforeAdmission.CutRev {
		t.Fatalf("restored full snapshot head changed: before=%d after=%d", beforeAdmission.CutRev, afterAdmission.CutRev)
	}
}

func TestProjectionSnapshotFenceRelayUsesSameResultBeforePatchOrder(t *testing.T) {
	_, publisher := readyProjectionPublisher(t, "epoch-fence-relay")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var plaintextFrames [][]byte
	delivered := make(chan struct{}, 4)
	conn := NewRelayDeviceConn(
		"device", "bridge", "route", 9, nil, append([]byte(nil), key...), nil,
		func(raw json.RawMessage) error {
			var envelope RelayEnvelope
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return err
			}
			aad, err := envelope.EncodeAAD()
			if err != nil {
				return err
			}
			plaintext, err := OpenEnvelope(key, envelope.Counter, aad, envelope.Ciphertext)
			if err != nil {
				return err
			}
			mu.Lock()
			plaintextFrames = append(plaintextFrames, plaintext)
			mu.Unlock()
			delivered <- struct{}{}
			return nil
		},
	)
	admission := beginFence(t, publisher, conn)
	publisher.PublishProjectionPatch("codex", "s1", sequentialPatch(admission.CutRev+1))
	if err := publisher.CompleteProjectionSnapshot(conn, admission, "relay-rpc", nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-delivered:
		case <-time.After(3 * time.Second):
			t.Fatal("relay ordered sink did not deliver result and patch")
		}
	}
	mu.Lock()
	frames := append([][]byte(nil), plaintextFrames...)
	mu.Unlock()
	var first, second map[string]interface{}
	if err := json.Unmarshal(frames[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(frames[1], &second); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "result" || first["requestId"] != "relay-rpc" ||
		second["type"] != "event" || second["event"] != "projection_patch" {
		t.Fatalf("relay wire order = %s then %s", frames[0], frames[1])
	}
}

func waitSnapshotFenceFrames(t *testing.T, conn *snapshotFenceCaptureConn, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(conn.snapshot()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d frames; got %v", count, conn.snapshot())
}
