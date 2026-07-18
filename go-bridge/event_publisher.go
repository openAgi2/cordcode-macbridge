package gobridge

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	eventOutboundQueueCapacity = 2048
	recoveryPendingMaxEvents   = 1000
	recoveryPendingMaxBytes    = 2 << 20
	recoveryPendingTimeout     = 30 * time.Second
)

type BridgeSessionCutMap map[string]map[string]BridgeSessionCut

// LogicalEvent is an unstamped business event. DeltaBatcher merges these before
// EventPublisher assigns identity, so discarded token chunks never create gaps.
type LogicalEvent struct {
	BackendID   string
	SessionID   string
	Directory   string
	Event       string
	Data        interface{}
	Message     string
	Broadcast   bool
	Targets     []Connection
	WaitTargets []Connection
	Offline     bool
}

type eventOutboundFrame struct {
	value     interface{}
	delivered chan struct{}
}

type eventDeliveryWait struct {
	conn      Connection
	delivered <-chan struct{}
}

type eventOutboundSink struct {
	conn  Connection
	queue chan eventOutboundFrame
	stop  chan struct{}
	once  sync.Once
}

func newEventOutboundSink(conn Connection) *eventOutboundSink {
	s := &eventOutboundSink{
		conn:  conn,
		queue: make(chan eventOutboundFrame, eventOutboundQueueCapacity),
		stop:  make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *eventOutboundSink) run() {
	for {
		select {
		case <-s.stop:
			return
		case frame := <-s.queue:
			s.conn.SendJSON(frame.value)
			if frame.delivered != nil {
				close(frame.delivered)
			}
		}
	}
}

func (s *eventOutboundSink) close() {
	s.once.Do(func() { close(s.stop) })
}

// EventPublisher is the process-scoped owner of event identity and ordered
// publication. Stamping and destination enqueue happen under one lock; actual
// connection and offline I/O run outside that critical section.
type EventPublisher struct {
	mu           sync.Mutex
	bridgeEpoch  string
	seq          int
	broadcaster  *Broadcaster
	sinks        map[Connection]*eventOutboundSink
	offlineQueue chan EventMessage
	offlineRoute func(EventMessage)
	recoveries   map[Connection]*publisherRecovery
	buffer       *EventBuffer
	now          func() time.Time
	completed    map[Connection]string
}

type RecoveryAdmission struct {
	BridgeEpoch string
	Seq         int
}

type publisherRecovery struct {
	recoveryID   string
	pending      []EventMessage
	pendingBytes int
	startedAt    time.Time
	cutBySession BridgeSessionCutMap
}

func NewEventPublisher(bridgeEpoch string, broadcaster ...*Broadcaster) *EventPublisher {
	if bridgeEpoch == "" {
		panic("event publisher bridge epoch must not be empty")
	}
	p := &EventPublisher{
		bridgeEpoch:  bridgeEpoch,
		sinks:        make(map[Connection]*eventOutboundSink),
		offlineQueue: make(chan EventMessage, eventOutboundQueueCapacity),
		recoveries:   make(map[Connection]*publisherRecovery),
		buffer:       NewEventBuffer(EventBufferConfig{}),
		now:          time.Now,
		completed:    make(map[Connection]string),
	}
	if len(broadcaster) > 0 {
		p.broadcaster = broadcaster[0]
	}
	go p.runOffline()
	return p
}

func (p *EventPublisher) BridgeEpoch() string { return p.bridgeEpoch }

func (p *EventPublisher) EventBuffer() *EventBuffer { return p.buffer }

func (p *EventPublisher) SetOfflineRoute(route func(EventMessage)) {
	p.mu.Lock()
	p.offlineRoute = route
	p.mu.Unlock()
}

func (p *EventPublisher) RegisterConnection(conn Connection) {
	if conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sinkLocked(conn)
}

func (p *EventPublisher) EnqueueControl(conn Connection, frame interface{}, wait bool) error {
	if conn == nil {
		return fmt.Errorf("connection is required")
	}
	p.mu.Lock()
	sink := p.sinkLocked(conn)
	var delivered chan struct{}
	if wait {
		delivered = make(chan struct{})
	}
	select {
	case sink.queue <- eventOutboundFrame{value: frame, delivered: delivered}:
		p.mu.Unlock()
	case <-time.After(bridgeWriteTimeout):
		p.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("outbound control queue timeout")
	}
	if delivered != nil {
		select {
		case <-delivered:
		case <-time.After(bridgeWriteTimeout):
			_ = conn.Close()
			return fmt.Errorf("outbound control delivery timeout")
		}
	}
	return nil
}

func (p *EventPublisher) UnregisterConnection(conn Connection) {
	p.mu.Lock()
	sink := p.sinks[conn]
	delete(p.sinks, conn)
	delete(p.recoveries, conn)
	delete(p.completed, conn)
	p.mu.Unlock()
	if sink != nil {
		sink.close()
	}
}

// BeginRecovery and Publish share p.mu, making the returned fence the exact
// boundary between live delivery and per-connection pending delivery.
func (p *EventPublisher) BeginRecovery(conn Connection, recoveryID string, cuts ...BridgeSessionCutMap) (RecoveryAdmission, error) {
	if conn == nil || recoveryID == "" {
		return RecoveryAdmission{}, fmt.Errorf("connection and recoveryId are required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.recoveries[conn]; exists {
		return RecoveryAdmission{}, fmt.Errorf("connection already recovering")
	}
	p.sinkLocked(conn)
	var cutBySession BridgeSessionCutMap
	if len(cuts) > 0 {
		cutBySession = cloneCutMap(cuts[0])
	}
	p.recoveries[conn] = &publisherRecovery{recoveryID: recoveryID, startedAt: p.now(), cutBySession: cutBySession}
	return RecoveryAdmission{BridgeEpoch: p.bridgeEpoch, Seq: p.seq}, nil
}

// CompleteRecovery atomically enqueues completion followed by every pending
// envelope, then restores live pass-through. The transfer is all-or-fail.
func (p *EventPublisher) CompleteRecovery(conn Connection, recoveryID string, applied ...BridgeSessionCutMap) error {
	p.mu.Lock()
	recovery := p.recoveries[conn]
	if recovery == nil && p.completed[conn] == recoveryID {
		p.mu.Unlock()
		return nil
	}
	if recovery == nil || recovery.recoveryID != recoveryID {
		p.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("recovery transaction mismatch")
	}
	if p.now().Sub(recovery.startedAt) >= recoveryPendingTimeout {
		delete(p.recoveries, conn)
		p.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("recovery transaction timed out")
	}
	if recovery.cutBySession != nil {
		if len(applied) != 1 || !cutMapsEqual(recovery.cutBySession, applied[0]) {
			delete(p.recoveries, conn)
			p.mu.Unlock()
			_ = conn.Close()
			return fmt.Errorf("recovery cut acknowledgement mismatch")
		}
	}
	sink := p.sinkLocked(conn)
	pending := make([]EventMessage, 0, len(recovery.pending))
	for _, msg := range recovery.pending {
		cut, known := lookupCut(recovery.cutBySession, msg.BackendID, msg.SessionID)
		if !known || msg.Seq > cut.Seq {
			pending = append(pending, msg)
		}
	}
	required := 1 + len(pending)
	if cap(sink.queue)-len(sink.queue) < required {
		delete(p.recoveries, conn)
		p.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("outbound queue cannot accept recovery batch")
	}
	sink.queue <- eventOutboundFrame{value: map[string]interface{}{"type": "recovery_complete", "recoveryId": recoveryID}}
	for _, msg := range pending {
		sink.queue <- eventOutboundFrame{value: msg}
	}
	delete(p.recoveries, conn)
	p.completed[conn] = recoveryID
	p.mu.Unlock()
	return nil
}

func (p *EventPublisher) FailRecovery(conn Connection, recoveryID string) {
	p.mu.Lock()
	if recovery := p.recoveries[conn]; recovery != nil && recovery.recoveryID == recoveryID {
		delete(p.recoveries, conn)
	}
	p.mu.Unlock()
}

// FreezeRecoverySnapshot implements RFC scheme C. It advances the session cut
// to the latest stamped event, then keeps publication frozen until release so
// history bytes and the returned HWM cannot straddle another Publish call.
func (p *EventPublisher) FreezeRecoverySnapshot(conn Connection, recoveryID, backendID, sessionID string) (BridgeSessionCut, func(), error) {
	p.mu.Lock()
	recovery := p.recoveries[conn]
	if recovery == nil || recovery.recoveryID != recoveryID {
		p.mu.Unlock()
		return BridgeSessionCut{}, nil, fmt.Errorf("recovery snapshot transaction mismatch")
	}
	cut, ok := p.buffer.LatestCut(backendID, sessionID)
	if !ok {
		cut, ok = lookupCut(recovery.cutBySession, backendID, sessionID)
	}
	if !ok {
		p.mu.Unlock()
		return BridgeSessionCut{}, nil, fmt.Errorf("recovery snapshot session is not affected")
	}
	if recovery.cutBySession[backendID] == nil {
		recovery.cutBySession[backendID] = make(map[string]BridgeSessionCut)
	}
	recovery.cutBySession[backendID][sessionID] = cut
	var once sync.Once
	release := func() { once.Do(func() { p.mu.Unlock() }) }
	return cut, release, nil
}

func (p *EventPublisher) sinkLocked(conn Connection) *eventOutboundSink {
	if sink := p.sinks[conn]; sink != nil {
		return sink
	}
	sink := newEventOutboundSink(conn)
	p.sinks[conn] = sink
	return sink
}

func (p *EventPublisher) PublishLogical(logical LogicalEvent) EventMessage {
	p.mu.Lock()
	p.seq++
	seq := p.seq
	msg := EventMessage{
		Type:        "event",
		EventID:     fmt.Sprintf("%s:%d", p.bridgeEpoch, seq),
		Seq:         seq,
		BridgeEpoch: p.bridgeEpoch,
		SessionID:   logical.SessionID,
		BackendID:   logical.BackendID,
		Event:       logical.Event,
		Data:        logical.Data,
		Message:     logical.Message,
		Replayable:  isReplayableEvent(logical.Event),
		Timestamp:   time.Now().UTC().UnixMilli(),
	}
	p.buffer.Append(msg)

	targets := make(map[Connection]struct{}, len(logical.Targets))
	waitTargets := make(map[Connection]struct{}, len(logical.WaitTargets))
	for _, conn := range logical.Targets {
		if conn != nil {
			targets[conn] = struct{}{}
		}
	}
	for _, conn := range logical.WaitTargets {
		if conn != nil {
			targets[conn] = struct{}{}
			waitTargets[conn] = struct{}{}
		}
	}
	if logical.Broadcast && p.broadcaster != nil {
		for _, conn := range p.broadcaster.Targets(logical.BackendID, logical.SessionID, logical.Directory) {
			targets[conn] = struct{}{}
		}
	}

	overflowed := make([]Connection, 0)
	waits := make([]eventDeliveryWait, 0, len(waitTargets))
	for conn := range targets {
		if recovery := p.recoveries[conn]; recovery != nil {
			encoded, _ := json.Marshal(msg)
			if p.now().Sub(recovery.startedAt) >= recoveryPendingTimeout || len(recovery.pending)+1 > recoveryPendingMaxEvents || recovery.pendingBytes+len(encoded) > recoveryPendingMaxBytes {
				delete(p.recoveries, conn)
				overflowed = append(overflowed, conn)
				continue
			}
			recovery.pending = append(recovery.pending, msg)
			recovery.pendingBytes += len(encoded)
			continue
		}
		sink := p.sinkLocked(conn)
		var delivered chan struct{}
		if _, shouldWait := waitTargets[conn]; shouldWait {
			delivered = make(chan struct{})
			waits = append(waits, eventDeliveryWait{conn: conn, delivered: delivered})
		}
		select {
		case sink.queue <- eventOutboundFrame{value: msg, delivered: delivered}:
		default:
			overflowed = append(overflowed, conn)
		}
	}
	if logical.Offline {
		select {
		case p.offlineQueue <- msg:
		default:
			// Offline delivery cannot silently skip an event. Marking the route
			// unavailable is handled by the router when the connection retries.
			overflowed = append(overflowed, nil)
		}
	}
	p.mu.Unlock()

	for _, conn := range overflowed {
		if conn != nil {
			_ = conn.Close()
		}
	}
	for _, wait := range waits {
		select {
		case <-wait.delivered:
		case <-time.After(bridgeWriteTimeout):
			_ = wait.conn.Close()
		}
	}
	return msg
}

func cloneCutMap(input BridgeSessionCutMap) BridgeSessionCutMap {
	if input == nil {
		return nil
	}
	result := make(BridgeSessionCutMap, len(input))
	for backendID, sessions := range input {
		copySessions := make(map[string]BridgeSessionCut, len(sessions))
		for sessionID, cut := range sessions {
			copySessions[sessionID] = cut
		}
		result[backendID] = copySessions
	}
	return result
}

func lookupCut(cuts BridgeSessionCutMap, backendID, sessionID string) (BridgeSessionCut, bool) {
	sessions, ok := cuts[backendID]
	if !ok {
		return BridgeSessionCut{}, false
	}
	cut, ok := sessions[sessionID]
	return cut, ok
}

func cutMapsEqual(left, right BridgeSessionCutMap) bool {
	if len(left) != len(right) {
		return false
	}
	for backendID, sessions := range left {
		other, ok := right[backendID]
		if !ok || len(sessions) != len(other) {
			return false
		}
		for sessionID, cut := range sessions {
			if other[sessionID] != cut {
				return false
			}
		}
	}
	return true
}

func (p *EventPublisher) runOffline() {
	for msg := range p.offlineQueue {
		p.mu.Lock()
		route := p.offlineRoute
		p.mu.Unlock()
		if route != nil {
			route(msg)
		}
	}
}

func isReplayableEvent(eventName string) bool {
	switch eventName {
	case "text_delta", "reasoning_delta", "tool_output_delta":
		return false
	default:
		return true
	}
}
