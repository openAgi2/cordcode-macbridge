package gobridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type publisherCaptureConn struct {
	mu      sync.Mutex
	frames  []interface{}
	classes []relayOutboundClass
	notify  chan struct{}
	gate    <-chan struct{}
	closed  bool
	device  *TrustedDeviceRecord
}

func newPublisherCaptureConn(gate <-chan struct{}) *publisherCaptureConn {
	return &publisherCaptureConn{notify: make(chan struct{}, 4096), gate: gate}
}

func (c *publisherCaptureConn) SendJSON(frame interface{}) {
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	c.frames = append(c.frames, frame)
	c.mu.Unlock()
	c.notify <- struct{}{}
}
func (c *publisherCaptureConn) SendJSONClassified(frame any, class relayOutboundClass) {
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	c.frames = append(c.frames, frame)
	c.classes = append(c.classes, class)
	c.mu.Unlock()
	c.notify <- struct{}{}
}
func (c *publisherCaptureConn) SendResult(string, interface{}, *WireError) {}
func (c *publisherCaptureConn) AuthedDevice() *TrustedDeviceRecord         { return c.device }
func (c *publisherCaptureConn) RemoteAddr() string                         { return "capture" }
func (c *publisherCaptureConn) Close() error                               { c.mu.Lock(); c.closed = true; c.mu.Unlock(); return nil }

func (c *publisherCaptureConn) waitCount(t *testing.T, count int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		c.mu.Lock()
		got := len(c.frames)
		c.mu.Unlock()
		if got >= count {
			return
		}
		select {
		case <-c.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %d frames; got %d", count, got)
		}
	}
}

func (c *publisherCaptureConn) snapshot() []interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]interface{}(nil), c.frames...)
}

func (c *publisherCaptureConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func TestEventPublisherStampsIndependentPerSessionSequences(t *testing.T) {
	publisher := NewEventPublisher("epoch-session-chain")
	a1 := publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "a", Event: "turn_started"})
	b1 := publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "b", Event: "turn_started"})
	a2 := publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "a", Event: "turn_completed"})

	if a1.Seq != 1 || b1.Seq != 2 || a2.Seq != 3 {
		t.Fatalf("global seq mismatch: a1=%d b1=%d a2=%d", a1.Seq, b1.Seq, a2.Seq)
	}
	if a1.PerSessionSeq != 1 || b1.PerSessionSeq != 1 || a2.PerSessionSeq != 2 {
		t.Fatalf("per-session seq mismatch: a1=%d b1=%d a2=%d", a1.PerSessionSeq, b1.PerSessionSeq, a2.PerSessionSeq)
	}
}

func TestEventPublisherConcurrentPublishersShareOneOrderedIdentity(t *testing.T) {
	const epoch = "11111111-2222-4333-8444-555555555555"
	publisher := NewEventPublisher(epoch)
	first := newPublisherCaptureConn(nil)
	second := newPublisherCaptureConn(nil)
	const total = 200
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: fmt.Sprintf("s-%d", i%3), Event: "turn_started", Targets: []Connection{first, second}})
		}(i)
	}
	wg.Wait()
	first.waitCount(t, total)
	second.waitCount(t, total)

	for _, conn := range []*publisherCaptureConn{first, second} {
		for index, raw := range conn.snapshot() {
			msg, ok := raw.(EventMessage)
			if !ok {
				t.Fatalf("frame %d type = %T", index, raw)
			}
			wantSeq := index + 1
			if msg.Seq != wantSeq || msg.EventID != fmt.Sprintf("%s:%d", epoch, wantSeq) || msg.BridgeEpoch != epoch {
				t.Fatalf("frame %d identity = %+v", index, msg)
			}
		}
	}
}

func TestEventPublisherEnforcesObservationScopePerAuthenticatedDevice(t *testing.T) {
	observation := NewObservationManager()
	observation.SetScope("dev-full", ObservationScope{
		BackendID: "codex", SessionIDs: []string{"session-a"}, DeliveryMode: scopeFullStream,
	})
	observation.SetScope("dev-milestone", ObservationScope{
		BackendID: "codex", SessionIDs: []string{"session-a"}, DeliveryMode: scopeMilestonesOnly,
	})
	full := newPublisherCaptureConn(nil)
	full.device = &TrustedDeviceRecord{DeviceID: "dev-full"}
	milestone := newPublisherCaptureConn(nil)
	milestone.device = &TrustedDeviceRecord{DeviceID: "dev-milestone"}
	publisher := NewEventPublisher("epoch-observation")
	publisher.SetObservationManager(observation)

	publisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "session-a", Event: "text_delta", Targets: []Connection{full, milestone},
	})
	full.waitCount(t, 1)
	time.Sleep(50 * time.Millisecond)
	if got := len(milestone.snapshot()); got != 0 {
		t.Fatalf("milestone device received text delta: %d", got)
	}
	publisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "session-a", Event: "turn_completed", Targets: []Connection{full, milestone},
	})
	milestone.waitCount(t, 1)
}

func TestEventPublisherCarriesCanonicalTypedClassHint(t *testing.T) {
	publisher := NewEventPublisher("11111111-2222-4333-8444-555555555555")
	conn := newPublisherCaptureConn(nil)
	publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "session", Event: "session_updated", Targets: []Connection{conn}})
	conn.waitCount(t, 1)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.classes) != 1 || conn.classes[0] != relayOutboundMetadata {
		t.Fatalf("classes=%v want metadata", conn.classes)
	}
}

func TestEventPublisherSlowConnectionDoesNotBlockFastConnection(t *testing.T) {
	gate := make(chan struct{})
	slow := newPublisherCaptureConn(gate)
	fast := newPublisherCaptureConn(nil)
	publisher := NewEventPublisher("epoch-slow")
	for i := 0; i < 20; i++ {
		publisher.PublishLogical(LogicalEvent{BackendID: "claude", SessionID: "s", Event: "text_delta", Data: map[string]interface{}{"delta": "x"}, Targets: []Connection{slow, fast}})
	}
	fast.waitCount(t, 20)
	if got := len(slow.snapshot()); got != 0 {
		t.Fatalf("slow connection unexpectedly delivered %d frames before gate", got)
	}
	close(gate)
	slow.waitCount(t, 20)
}

func TestEventPublisherLiveOverflowClosesOnlySlowConnectionAndPreservesRecovery(t *testing.T) {
	gate := make(chan struct{})
	slow := newPublisherCaptureConn(gate)
	fast := newPublisherCaptureConn(nil)
	publisher := NewEventPublisher("epoch-live-overflow")

	for i := 0; i <= eventOutboundQueueCapacity; i++ {
		publisher.PublishLogical(LogicalEvent{
			BackendID: "codex", SessionID: "session", Event: "turn_started", Targets: []Connection{slow, fast},
		})
	}
	fast.waitCount(t, eventOutboundQueueCapacity+1)
	if !slow.isClosed() {
		t.Fatal("frame 2049 did not fail-close the slow connection")
	}
	if fast.isClosed() {
		t.Fatal("slow-peer overflow closed an unrelated fast connection")
	}
	if got := len(slow.snapshot()); got != 0 {
		t.Fatalf("slow connection delivered %d frames before its blocked writer was released", got)
	}

	replay := publisher.EventBuffer().Replay("codex", "session", BridgeSessionCut{})
	if replay.Disposition != ReplayAvailable || len(replay.Events) != eventOutboundQueueCapacity+1 {
		t.Fatalf("reconnect replay = disposition %q events %d", replay.Disposition, len(replay.Events))
	}
	close(gate)
}

func TestEventPublisherRelayVirtualOverflowClosesOnlyOldGenerationAndCanRecover(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	writer := newRelayOutboundWriter()
	defer writer.close()
	relay := NewRelayDeviceConn(
		"device", "bridge", "route", 1, nil, make([]byte, 32), nil,
		func(json.RawMessage) error {
			once.Do(func() { close(started) })
			<-gate
			return nil
		},
	)
	relay.setOutboundWriter(writer)
	fast := newPublisherCaptureConn(nil)
	publisher := NewEventPublisher("epoch-relay-live-overflow")

	publisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "session", Event: "turn_started", Targets: []Connection{relay, fast},
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("relay virtual connection did not enter its blocked writer")
	}
	for i := 1; i <= eventOutboundQueueCapacity; i++ {
		publisher.PublishLogical(LogicalEvent{
			BackendID: "codex", SessionID: "session", Event: "turn_started", Targets: []Connection{relay, fast},
		})
	}
	fast.waitCount(t, eventOutboundQueueCapacity+1)
	if !relay.isClosed() {
		t.Fatal("relay overflow did not close the old virtual connection generation")
	}
	if fast.isClosed() {
		t.Fatal("relay virtual-connection overflow closed an unrelated direct connection")
	}

	next := NewRelayDeviceConn("device", "bridge", "route", 2, nil, make([]byte, 32), nil, func(json.RawMessage) error { return nil })
	if next.isClosed() || next.channelGeneration() != 2 {
		t.Fatal("replacement relay virtual connection was not independently usable")
	}
	replay := publisher.EventBuffer().Replay("codex", "session", BridgeSessionCut{})
	if replay.Disposition != ReplayAvailable || len(replay.Events) != eventOutboundQueueCapacity+1 {
		t.Fatalf("relay reconnect replay = disposition %q events %d", replay.Disposition, len(replay.Events))
	}
	close(gate)
}

func TestEventPublisherRecoveryAdmissionAndAtomicCompletionOrder(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	publisher := NewEventPublisher("epoch-recovery")
	publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "a", Event: "turn_started", Targets: []Connection{conn}})
	conn.waitCount(t, 1)
	fence, err := publisher.BeginRecovery(conn, "recovery-1")
	if err != nil || fence.Seq != 1 {
		t.Fatalf("BeginRecovery = %+v, %v", fence, err)
	}
	publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "a", Event: "text_delta", Targets: []Connection{conn}})
	publisher.PublishLogical(LogicalEvent{BackendID: "claude", SessionID: "b", Event: "turn_completed", Targets: []Connection{conn}})
	time.Sleep(20 * time.Millisecond)
	if got := len(conn.snapshot()); got != 1 {
		t.Fatalf("recovering connection received pending frames: %d", got)
	}
	if err := publisher.CompleteRecovery(conn, "recovery-1"); err != nil {
		t.Fatal(err)
	}
	conn.waitCount(t, 4)
	frames := conn.snapshot()
	complete, ok := frames[1].(map[string]interface{})
	if !ok || complete["type"] != "recovery_complete" || complete["recoveryId"] != "recovery-1" {
		t.Fatalf("completion frame = %#v", frames[1])
	}
	if frames[2].(EventMessage).Seq != 2 || frames[3].(EventMessage).Seq != 3 {
		t.Fatalf("pending flush order = %#v", frames[2:])
	}
}

func TestEventPublisherAdmissionRaceHasNoUnownedSequence(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		conn := newPublisherCaptureConn(nil)
		publisher := NewEventPublisher(fmt.Sprintf("epoch-%d", iteration))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			publisher.PublishLogical(LogicalEvent{Event: "turn_started", Targets: []Connection{conn}})
		}()
		var fence RecoveryAdmission
		go func() {
			defer wg.Done()
			<-start
			fence, _ = publisher.BeginRecovery(conn, "race")
		}()
		close(start)
		wg.Wait()
		if err := publisher.CompleteRecovery(conn, "race"); err != nil {
			t.Fatal(err)
		}
		conn.waitCount(t, 2)
		frames := conn.snapshot()
		seqs := make([]int, 0, 1)
		for _, frame := range frames {
			if msg, ok := frame.(EventMessage); ok {
				seqs = append(seqs, msg.Seq)
			}
		}
		sort.Ints(seqs)
		if len(seqs) != 1 || seqs[0] != 1 || (fence.Seq != 0 && fence.Seq != 1) {
			t.Fatalf("iteration %d fence=%+v seqs=%v frames=%#v", iteration, fence, seqs, frames)
		}
	}
}

func TestEventPublisherExactMultiSessionCutAndFilteredFlush(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-cuts")
	cuts := BridgeSessionCutMap{
		"codex":  {"a": {EventID: "epoch-cuts:4", Seq: 4}},
		"claude": {"b": {EventID: "epoch-cuts:7", Seq: 7}},
	}
	if _, err := p.BeginRecovery(conn, "cuts", cuts); err != nil {
		t.Fatal(err)
	}
	// Global seq begins at 1, so both are at/below their session cuts and must
	// not be re-applied after acknowledgement.
	p.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "a", Event: "turn_started", Targets: []Connection{conn}})
	p.PublishLogical(LogicalEvent{BackendID: "claude", SessionID: "b", Event: "turn_started", Targets: []Connection{conn}})
	// A session first seen after admission has no cut and flushes in full.
	p.PublishLogical(LogicalEvent{BackendID: "opencode", SessionID: "new", Event: "turn_started", Targets: []Connection{conn}})
	if err := p.CompleteRecovery(conn, "cuts", cloneCutMap(cuts)); err != nil {
		t.Fatal(err)
	}
	conn.waitCount(t, 2)
	frames := conn.snapshot()
	if frames[0].(map[string]interface{})["type"] != "recovery_complete" || frames[1].(EventMessage).SessionID != "new" {
		t.Fatalf("unexpected flush: %#v", frames)
	}
	if err := p.CompleteRecovery(conn, "cuts", cloneCutMap(cuts)); err != nil {
		t.Fatalf("duplicate completed ack must be idempotent: %v", err)
	}
}

func TestEventPublisherRejectsMissingExtraAndWrongCutEntries(t *testing.T) {
	want := BridgeSessionCutMap{"codex": {"a": {EventID: "e:1", Seq: 1}}}
	cases := []BridgeSessionCutMap{
		{},
		{"codex": {"a": {EventID: "e:1", Seq: 1}, "extra": {EventID: "e:2", Seq: 2}}},
		{"codex": {"a": {EventID: "e:2", Seq: 2}}},
	}
	for index, applied := range cases {
		conn := newPublisherCaptureConn(nil)
		p := NewEventPublisher(fmt.Sprintf("epoch-cut-%d", index))
		if _, err := p.BeginRecovery(conn, "recovery", want); err != nil {
			t.Fatal(err)
		}
		if err := p.CompleteRecovery(conn, "recovery", applied); err == nil {
			t.Fatalf("case %d accepted mismatched cut", index)
		}
		conn.mu.Lock()
		closed := conn.closed
		conn.mu.Unlock()
		if !closed {
			t.Fatalf("case %d did not close connection", index)
		}
		if len(conn.snapshot()) != 0 {
			t.Fatalf("case %d exposed completion", index)
		}
	}
}

func TestEventPublisherPendingEventLimitFailsWithoutCompletion(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-overflow")
	if _, err := p.BeginRecovery(conn, "overflow"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= recoveryPendingMaxEvents; i++ {
		p.PublishLogical(LogicalEvent{Event: "turn_started", Targets: []Connection{conn}})
	}
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed {
		t.Fatal("overflow did not close connection")
	}
	if len(conn.snapshot()) != 0 {
		t.Fatalf("overflow exposed frames: %#v", conn.snapshot())
	}
	if err := p.CompleteRecovery(conn, "overflow"); err == nil {
		t.Fatal("overflowed transaction still completed")
	}
}

func TestEventPublisherPendingByteLimitFailsWithoutDroppingOldest(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-byte-overflow")
	if _, err := p.BeginRecovery(conn, "overflow"); err != nil {
		t.Fatal(err)
	}
	p.PublishLogical(LogicalEvent{Event: "turn_started", Data: strings.Repeat("x", recoveryPendingMaxBytes), Targets: []Connection{conn}})
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed {
		t.Fatal("byte overflow did not close connection")
	}
	if len(conn.snapshot()) != 0 {
		t.Fatal("byte overflow partially flushed")
	}
}

func TestEventPublisherRecoveryTimeoutUsesInjectedClock(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-timeout")
	now := time.Unix(100, 0)
	p.now = func() time.Time { return now }
	if _, err := p.BeginRecovery(conn, "timeout"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(recoveryPendingTimeout)
	if err := p.CompleteRecovery(conn, "timeout"); err == nil {
		t.Fatal("timed out recovery completed")
	}
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed || len(conn.snapshot()) != 0 {
		t.Fatalf("timeout closed=%v frames=%#v", closed, conn.snapshot())
	}
}

func TestEventPublisherSnapshotFreezeCoversAdmissionToHWMWithoutGap(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-snapshot")
	p.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "turn_started"})
	initial := BridgeSessionCutMap{"codex": {"s": {EventID: "epoch-snapshot:1", Seq: 1}}}
	if _, err := p.BeginRecovery(conn, "snapshot", initial); err != nil {
		t.Fatal(err)
	}
	cut, release, err := p.FreezeRecoverySnapshot(conn, "snapshot", "codex", "s")
	if err != nil || cut.Seq != 1 {
		t.Fatalf("freeze cut=%+v err=%v", cut, err)
	}
	started := make(chan struct{})
	published := make(chan struct{})
	go func() {
		close(started)
		p.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "turn_completed", Targets: []Connection{conn}})
		close(published)
	}()
	<-started
	select {
	case <-published:
		t.Fatal("publication crossed the frozen snapshot cut")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("publication did not resume after snapshot release")
	}
	if err := p.CompleteRecovery(conn, "snapshot", initial); err != nil {
		t.Fatal(err)
	}
	conn.waitCount(t, 2)
	frames := conn.snapshot()
	if frames[0].(map[string]interface{})["type"] != "recovery_complete" || frames[1].(EventMessage).Seq != 2 {
		t.Fatalf("frames=%#v", frames)
	}
}

func TestBusinessEventConstructionHasNoProductionBypass(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"EventMessage{", `Type:      "event"`, `Type: "event"`, `"type":    "event"`, ".SendEvent(", "broadcaster.Send("}
	for _, entry := range entries {
		name := entry.Name()
		// event_buffer.go may build a metadata-only tombstone from an already stamped event; it is
		// storage, not an egress constructor. Only EventPublisher may create a new business envelope.
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "event_publisher.go" || name == "event_buffer.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range banned {
			if strings.Contains(string(data), token) {
				t.Errorf("production event egress bypass in %s: %q", name, token)
			}
		}
	}
}
