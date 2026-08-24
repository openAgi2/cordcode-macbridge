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

func TestPublishControlPlaneRejectsInvalidEventsWithoutSideEffects(t *testing.T) {
	publisher := NewEventPublisher("epoch-control-invalid")
	conn := newPublisherCaptureConn(nil)
	publisher.RegisterConnection(conn)

	cases := []LogicalEvent{
		{BackendID: "codex", Event: "text_delta", Targets: []Connection{conn}},
		{BackendID: "codex", Event: "turn_completed", Targets: []Connection{conn}},
		{Event: "sessions_changed", Targets: []Connection{conn}},
		{BackendID: "codex", SessionID: "session-a", Event: "sessions_changed", Targets: []Connection{conn}},
	}
	for index, logical := range cases {
		if msg, err := publisher.PublishControlPlane(logical); err == nil || msg != (EventMessage{}) {
			t.Fatalf("case %d accepted invalid control-plane event: msg=%+v err=%v", index, msg, err)
		}
	}

	publisher.mu.Lock()
	seq := publisher.seq
	publisher.mu.Unlock()
	events, bytes := publisher.EventBuffer().Stats()
	if seq != 0 || events != 0 || bytes != 0 || len(conn.snapshot()) != 0 {
		t.Fatalf("invalid publishes had side effects: seq=%d events=%d bytes=%d frames=%d", seq, events, bytes, len(conn.snapshot()))
	}
	if _, ok := publisher.ProjectionReducer().Snapshot("codex", ""); ok {
		t.Fatal("invalid control-plane event created backend-scoped projection state")
	}
}

func TestPublishControlPlanePreservesRecoveryObservationAndClassifiedDelivery(t *testing.T) {
	observation := NewObservationManager()
	conn := newPublisherCaptureConn(nil)
	conn.device = &TrustedDeviceRecord{DeviceID: "control-plane-device"}
	publisher := NewEventPublisher("epoch-control-recovery")
	publisher.SetObservationManager(observation)
	publisher.RegisterConnection(conn)
	if _, err := publisher.BeginRecovery(conn, "control-plane-recovery"); err != nil {
		t.Fatal(err)
	}

	msg, err := publisher.PublishControlPlane(LogicalEvent{
		BackendID: "codex", Event: "sessions_changed", Targets: []Connection{conn},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := len(conn.snapshot()); got != 0 {
		t.Fatalf("recovering connection received control-plane event early: %d", got)
	}
	if err := publisher.CompleteRecovery(conn, "control-plane-recovery"); err != nil {
		t.Fatal(err)
	}
	conn.waitCount(t, 2)
	frames := conn.snapshot()
	if frames[0].(map[string]interface{})["type"] != "recovery_complete" || frames[1] != msg {
		t.Fatalf("recovery frames=%#v want completion then sessions_changed", frames)
	}
	conn.mu.Lock()
	classes := append([]relayOutboundClass(nil), conn.classes...)
	conn.mu.Unlock()
	if len(classes) != 2 || classes[0] != relayOutboundControl || classes[1] != classifyRelayEvent("sessions_changed") {
		t.Fatalf("classified recovery delivery=%v", classes)
	}
}

func TestControlPlanePublisherHasNoPublicBypassOrEventNameBranchInPublishLogical(t *testing.T) {
	data, err := os.ReadFile("event_publisher.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if strings.Contains(source, "PublishLogicalDeliverOnly") {
		t.Fatal("deprecated public deliver-only publisher still exists")
	}
	start := strings.Index(source, "func (p *EventPublisher) PublishLogical(")
	if start < 0 {
		t.Fatal("could not locate PublishLogical wrapper")
	}
	end := strings.Index(source[start:], "\n}")
	if end < 0 {
		t.Fatal("could not locate end of PublishLogical wrapper")
	}
	if body := source[start : start+end]; strings.Contains(body, "sessions_changed") {
		t.Fatal("PublishLogical contains an event-name special case")
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
		p.RegisterConnection(conn)
		if _, err := p.BeginRecovery(conn, "recovery", want); err != nil {
			t.Fatal(err)
		}
		// Cut mismatch is now degraded (abort + notify), not fail-closed: the
		// connection survives the bad ack.
		if err := p.CompleteRecovery(conn, "recovery", applied); err != nil {
			t.Fatalf("case %d: cut mismatch must be a degraded no-op, got %v", index, err)
		}
		conn.mu.Lock()
		closed := conn.closed
		conn.mu.Unlock()
		if closed {
			t.Fatalf("case %d closed connection on cut mismatch", index)
		}
		conn.waitCount(t, 1)
		aborted, ok := conn.snapshot()[0].(map[string]interface{})
		if !ok || aborted["type"] != "recovery_aborted" || aborted["recoveryId"] != "recovery" {
			t.Fatalf("case %d frame = %#v", index, conn.snapshot()[0])
		}
	}
}

func TestEventPublisherPendingEventLimitFailsWithoutCompletion(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-overflow")
	p.RegisterConnection(conn)
	if _, err := p.BeginRecovery(conn, "overflow"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= recoveryPendingMaxEvents; i++ {
		p.PublishLogical(LogicalEvent{Event: "turn_started", Targets: []Connection{conn}})
	}
	// Cap exceeded: degrade instead of fail-closed. Pending buffer is replayed
	// in order, the current event goes live, then recovery_aborted is sent.
	conn.waitCount(t, recoveryPendingMaxEvents+2)
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if closed {
		t.Fatal("overflow must not close connection")
	}
	frames := conn.snapshot()
	last, ok := frames[len(frames)-1].(map[string]interface{})
	if !ok || last["type"] != "recovery_aborted" || last["recoveryId"] != "overflow" {
		t.Fatalf("last frame = %#v", frames[len(frames)-1])
	}
	eventCount := 0
	for _, frame := range frames {
		if _, ok := frame.(EventMessage); ok {
			eventCount++
		}
	}
	if want := recoveryPendingMaxEvents + 1; eventCount != want {
		t.Fatalf("replayed+live events=%d want %d", eventCount, want)
	}
	if err := p.CompleteRecovery(conn, "overflow"); err != nil {
		t.Fatalf("overflowed transaction already abandoned, completion must be no-op, got %v", err)
	}
}

func TestEventPublisherPendingByteLimitFailsWithoutDroppingOldest(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-byte-overflow")
	p.RegisterConnection(conn)
	if _, err := p.BeginRecovery(conn, "overflow"); err != nil {
		t.Fatal(err)
	}
	p.PublishLogical(LogicalEvent{Event: "turn_started", Data: strings.Repeat("x", recoveryPendingMaxBytes), Targets: []Connection{conn}})
	// Byte cap exceeded on the first pending event: degrade, deliver the event
	// live, notify aborted, keep the connection.
	conn.waitCount(t, 2)
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if closed {
		t.Fatal("byte overflow must not close connection")
	}
	frames := conn.snapshot()
	if _, ok := frames[0].(EventMessage); !ok {
		t.Fatalf("first frame = %#v", frames[0])
	}
	last, ok := frames[len(frames)-1].(map[string]interface{})
	if !ok || last["type"] != "recovery_aborted" {
		t.Fatalf("last frame = %#v", frames[len(frames)-1])
	}
}

func TestEventPublisherRecoveryTimeoutUsesInjectedClock(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	p := NewEventPublisher("epoch-timeout")
	p.RegisterConnection(conn)
	now := time.Unix(100, 0)
	p.now = func() time.Time { return now }
	if _, err := p.BeginRecovery(conn, "timeout"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(recoveryPendingTimeout)
	// A late acknowledgement is now accepted: the transaction converges to live
	// delivery instead of tearing down the transport (reconnect-storm fix).
	if err := p.CompleteRecovery(conn, "timeout"); err != nil {
		t.Fatalf("late completion must be accepted, got %v", err)
	}
	conn.waitCount(t, 1)
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if closed || len(conn.snapshot()) != 1 {
		t.Fatalf("timeout closed=%v frames=%#v", closed, conn.snapshot())
	}
	complete, ok := conn.snapshot()[0].(map[string]interface{})
	if !ok || complete["type"] != "recovery_complete" {
		t.Fatalf("completion frame = %#v", conn.snapshot()[0])
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
		// event_buffer.go may build a metadata-only tombstone from an already stamped event.
		// projection_kernel.go builds reducer-only hydrate input that never reaches transport.
		// Neither is an egress constructor; only EventPublisher may create a business envelope.
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			name == "event_publisher.go" || name == "event_buffer.go" || name == "projection_kernel.go" {
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

func TestPublishDegradesTimedOutRecoveryInsteadOfClosing(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	publisher := NewEventPublisher("epoch-degrade")
	publisher.RegisterConnection(conn)
	if _, err := publisher.BeginRecovery(conn, "degrade-1"); err != nil {
		t.Fatal(err)
	}
	// Freeze the clock so the transaction is already past recoveryPendingTimeout.
	base := time.Now()
	publisher.now = func() time.Time { return base.Add(recoveryPendingTimeout + time.Second) }
	publisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "a", Event: "turn_started", Targets: []Connection{conn}})
	conn.waitCount(t, 2)
	frames := conn.snapshot()
	if conn.isClosed() {
		t.Fatal("timed-out recovery must not fail-closed the connection")
	}
	aborted, ok := frames[1].(map[string]interface{})
	if !ok || aborted["type"] != "recovery_aborted" || aborted["recoveryId"] != "degrade-1" {
		t.Fatalf("recovery_aborted frame = %#v", frames[1])
	}
	if msg, ok := frames[0].(EventMessage); !ok || msg.Event != "turn_started" {
		t.Fatalf("live event frame = %#v", frames[0])
	}
}

func TestCompleteRecoveryLateAcknowledgementDoesNotClose(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	publisher := NewEventPublisher("epoch-late-ack")
	publisher.RegisterConnection(conn)
	if _, err := publisher.BeginRecovery(conn, "late-1"); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	publisher.now = func() time.Time { return base.Add(recoveryPendingTimeout + time.Second) }
	if err := publisher.CompleteRecovery(conn, "late-1"); err != nil {
		t.Fatalf("late completion must be accepted, got %v", err)
	}
	conn.waitCount(t, 1)
	if conn.isClosed() {
		t.Fatal("late acknowledgement must not close the connection")
	}
	complete, ok := conn.snapshot()[0].(map[string]interface{})
	if !ok || complete["type"] != "recovery_complete" || complete["recoveryId"] != "late-1" {
		t.Fatalf("completion frame = %#v", conn.snapshot()[0])
	}
}

func TestCompleteRecoveryUnknownOrMismatchedDoesNotClose(t *testing.T) {
	conn := newPublisherCaptureConn(nil)
	publisher := NewEventPublisher("epoch-mismatch")
	publisher.RegisterConnection(conn)
	// Unknown transaction id: late/no-op ack must keep the connection alive.
	if err := publisher.CompleteRecovery(conn, "ghost"); err != nil {
		t.Fatalf("unknown recovery ack must be a no-op, got %v", err)
	}
	if conn.isClosed() {
		t.Fatal("unknown recovery ack must not close the connection")
	}
	// Begin with a cut map, complete with a non-matching cut: degrade + notify,
	// connection survives.
	cuts := BridgeSessionCutMap{"codex": {"a": {EventID: "epoch:1", Seq: 1}}}
	if _, err := publisher.BeginRecovery(conn, "mismatch-1", cuts); err != nil {
		t.Fatal(err)
	}
	wrong := BridgeSessionCutMap{"codex": {"a": {EventID: "epoch:2", Seq: 2}}}
	if err := publisher.CompleteRecovery(conn, "mismatch-1", wrong); err != nil {
		t.Fatalf("cut mismatch must be a degraded no-op, got %v", err)
	}
	conn.waitCount(t, 1)
	if conn.isClosed() {
		t.Fatal("cut mismatch must not close the connection")
	}
	aborted, ok := conn.snapshot()[0].(map[string]interface{})
	if !ok || aborted["type"] != "recovery_aborted" || aborted["recoveryId"] != "mismatch-1" {
		t.Fatalf("recovery_aborted frame = %#v", conn.snapshot()[0])
	}
}
