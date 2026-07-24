package gobridge

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	ClassHint   relayOutboundClass
}

type eventOutboundFrame struct {
	value      interface{}
	delivered  chan struct{}
	classHint  relayOutboundClass
	classified bool
}

type eventDeliveryWait struct {
	conn      Connection
	delivered <-chan struct{}
}

type eventOutboundSink struct {
	conn  Connection
	queue chan eventOutboundFrame
	slots chan struct{}
	stop  chan struct{}
	once  sync.Once
}

func newEventOutboundSink(conn Connection) *eventOutboundSink {
	s := &eventOutboundSink{
		conn:  conn,
		queue: make(chan eventOutboundFrame, eventOutboundQueueCapacity),
		slots: make(chan struct{}, eventOutboundQueueCapacity),
		stop:  make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *eventOutboundSink) run() {
	for {
		select {
		case <-s.stop:
			// Drain without I/O so queued frames never hit a replaced/closed conn.
			for {
				select {
				case frame := <-s.queue:
					if frame.delivered != nil {
						close(frame.delivered)
					}
					select {
					case <-s.slots:
					default:
					}
				default:
					return
				}
			}
		case frame := <-s.queue:
			if closed, ok := s.conn.(interface{ isClosed() bool }); ok && closed.isClosed() {
				if frame.delivered != nil {
					close(frame.delivered)
				}
				<-s.slots
				continue
			}
			if sender, ok := s.conn.(interface {
				SendJSONClassified(any, relayOutboundClass)
			}); ok && frame.classified {
				sender.SendJSONClassified(frame.value, frame.classHint)
			} else {
				s.conn.SendJSON(frame.value)
			}
			if frame.delivered != nil {
				close(frame.delivered)
			}
			<-s.slots
		}
	}
}

func (s *eventOutboundSink) tryEnqueue(frame eventOutboundFrame) bool {
	if closed, ok := s.conn.(interface{ isClosed() bool }); ok && closed.isClosed() {
		return false
	}
	select {
	case <-s.stop:
		return false
	default:
	}
	select {
	case s.slots <- struct{}{}:
		select {
		case s.queue <- frame:
			return true
		case <-s.stop:
			<-s.slots
			return false
		}
	default:
		return false
	}
}

func (s *eventOutboundSink) close() {
	s.once.Do(func() { close(s.stop) })
}

// EventPublisher is the process-scoped owner of event identity and ordered
// publication. Stamping and destination enqueue happen under one lock; actual
// connection and offline I/O run outside that critical section.
type EventPublisher struct {
	mu            sync.Mutex
	bridgeEpoch   string
	seq           int
	perSessionSeq map[string]int
	broadcaster   *Broadcaster
	sinks         map[Connection]*eventOutboundSink
	offlineQueue  chan EventMessage
	offlineRoute  func(EventMessage)
	recoveries    map[Connection]*publisherRecovery
	buffer        *EventBuffer
	liveBuffer    *LiveFrameBuffer
	now           func() time.Time
	completed     map[Connection]string
	observation   *ObservationManager
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
		bridgeEpoch:   bridgeEpoch,
		perSessionSeq: make(map[string]int),
		sinks:         make(map[Connection]*eventOutboundSink),
		offlineQueue:  make(chan EventMessage, eventOutboundQueueCapacity),
		recoveries:    make(map[Connection]*publisherRecovery),
		buffer:        NewEventBuffer(EventBufferConfig{}),
		liveBuffer:    NewLiveFrameBuffer(),
		now:           time.Now,
		completed:     make(map[Connection]string),
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

func (p *EventPublisher) SetObservationManager(observation *ObservationManager) {
	p.mu.Lock()
	p.observation = observation
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
	case sink.slots <- struct{}{}:
		sink.queue <- eventOutboundFrame{value: frame, delivered: delivered}
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
	if cap(sink.slots)-len(sink.slots) < required {
		delete(p.recoveries, conn)
		p.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("outbound queue cannot accept recovery batch")
	}
	sink.slots <- struct{}{}
	sink.queue <- eventOutboundFrame{value: map[string]interface{}{"type": "recovery_complete", "recoveryId": recoveryID}, classHint: relayOutboundControl, classified: true}
	for _, msg := range pending {
		sink.slots <- struct{}{}
		sink.queue <- eventOutboundFrame{value: msg, classHint: classifyRelayEvent(msg.Event), classified: true}
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
	logical.ClassHint = classifyRelayEvent(logical.Event)
	if logical.ClassHint == relayOutboundNormal {
		slog.Debug("relay outbound event uses normal default", "event", logical.Event)
	}
	p.seq++
	seq := p.seq
	perSessionSeq := 0
	if logical.BackendID != "" && logical.SessionID != "" {
		key := logical.BackendID + "\x00" + logical.SessionID
		p.perSessionSeq[key]++
		perSessionSeq = p.perSessionSeq[key]
	}
	msg := EventMessage{
		Type:          "event",
		EventID:       fmt.Sprintf("%s:%d", p.bridgeEpoch, seq),
		Seq:           seq,
		PerSessionSeq: perSessionSeq,
		BridgeEpoch:   p.bridgeEpoch,
		SessionID:     logical.SessionID,
		BackendID:     logical.BackendID,
		Event:         logical.Event,
		Data:          logical.Data,
		Message:       logical.Message,
		Replayable:    isReplayableEvent(logical.Event),
		Timestamp:     time.Now().UTC().UnixMilli(),
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
	enqueued := 0
	observationFiltered := 0
	for conn := range targets {
		if p.observation != nil {
			if device := conn.AuthedDevice(); device != nil &&
				!p.observation.ShouldSendEvent(device.DeviceID, logical.BackendID, logical.SessionID, logical.Event) {
				observationFiltered++
				continue
			}
		}
		if recovery := p.recoveries[conn]; recovery != nil {
			encoded, _ := json.Marshal(msg)
			if p.now().Sub(recovery.startedAt) >= recoveryPendingTimeout || len(recovery.pending)+1 > recoveryPendingMaxEvents || recovery.pendingBytes+len(encoded) > recoveryPendingMaxBytes {
				delete(p.recoveries, conn)
				overflowed = append(overflowed, conn)
				continue
			}
			recovery.pending = append(recovery.pending, msg)
			recovery.pendingBytes += len(encoded)
			enqueued++
			continue
		}
		sink := p.sinkLocked(conn)
		var delivered chan struct{}
		if _, shouldWait := waitTargets[conn]; shouldWait {
			delivered = make(chan struct{})
			waits = append(waits, eventDeliveryWait{conn: conn, delivered: delivered})
		}
		if !sink.tryEnqueue(eventOutboundFrame{value: msg, delivered: delivered, classHint: logical.ClassHint, classified: true}) {
			overflowed = append(overflowed, conn)
		} else {
			enqueued++
			if p.liveBuffer != nil {
				if d := conn.AuthedDevice(); d != nil && logical.BackendID != "" && logical.SessionID != "" {
					p.liveBuffer.NoteInterest(d.DeviceID, logical.BackendID, logical.SessionID)
				}
			}
		}
	}

	// Visibility for "Mac EMIT + iOS no EVT-RECV": zero live targets after
	// observation filter means the frame will not reach any online device.
	// Buffer live frames for interested degraded devices so reconnect can flush
	// instead of permanent jump via history bulk (live-frame-buffer design).
	if enqueued == 0 && len(overflowed) == 0 && !logical.Offline {
		switch logical.Event {
		case "text_delta", "reasoning_delta", "turn_started", "tool_started", "tool_finished":
			slog.Warn("event-publisher: live event has zero online targets",
				"event", logical.Event,
				"backendID", logical.BackendID,
				"sessionID", logical.SessionID,
				"candidateTargets", len(targets),
				"observationFiltered", observationFiltered,
			)
		}
		if p.liveBuffer != nil && isLiveBufferableEvent(logical.Event) {
			missed := p.degradedDeviceIDsLocked(logical, targets)
			if len(missed) > 0 {
				p.liveBuffer.Append(missed, msg)
				slog.Debug("live-frame-buffer append",
					"event", logical.Event,
					"sessionID", logical.SessionID,
					"devices", len(missed),
					"perSessionSeq", msg.PerSessionSeq,
				)
			}
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

// degradedDeviceIDsLocked returns device IDs that should receive this session's
// live events but are not among current online targets.
// Caller must hold p.mu.
func (p *EventPublisher) degradedDeviceIDsLocked(logical LogicalEvent, targets map[Connection]struct{}) []string {
	if logical.BackendID == "" || logical.SessionID == "" {
		return nil
	}
	online := make(map[string]struct{})
	for conn := range targets {
		if conn == nil {
			continue
		}
		if d := conn.AuthedDevice(); d != nil {
			online[d.DeviceID] = struct{}{}
		}
	}
	// Candidate set: observation devices ∪ live-buffer interest for this session.
	// Interest survives soft prune RemoveDevice so zero-target EMITs still buffer.
	cand := make(map[string]struct{})
	if p.observation != nil {
		for _, id := range p.observation.DeviceIDs() {
			cand[id] = struct{}{}
		}
	}
	if p.liveBuffer != nil {
		for _, id := range p.liveBuffer.InterestedDevices(logical.BackendID, logical.SessionID) {
			cand[id] = struct{}{}
		}
	}
	var missed []string
	for deviceID := range cand {
		if _, ok := online[deviceID]; ok {
			continue
		}
		// Prefer observation gate when present; otherwise trust prior interest.
		if p.observation != nil {
			if scope := p.observation.GetScope(deviceID, logical.BackendID); scope != nil {
				interested := len(scope.SessionIDs) == 0
				for _, sid := range scope.SessionIDs {
					if sid == logical.SessionID || sid == "*" {
						interested = true
						break
					}
				}
				if !interested {
					continue
				}
				if scope.DeliveryMode == scopeFullStream || isLiveControlPlaneEvent(logical.Event) || isLiveBufferableEvent(logical.Event) {
					// full_stream (or soft-expired) devices get all bufferable live frames
					if scope.DeliveryMode == scopeFullStream || isLiveControlPlaneEvent(logical.Event) {
						missed = append(missed, deviceID)
						continue
					}
					// milestones_only: only control-plane into buffer
					if isLiveControlPlaneEvent(logical.Event) {
						missed = append(missed, deviceID)
					}
					continue
				}
				continue
			}
		}
		// No scope row: buffer if interest map says they watched this session.
		missed = append(missed, deviceID)
	}
	return missed
}

func (p *EventPublisher) NoteLiveInterest(deviceID, backendID, sessionID string) {
	if p == nil || p.liveBuffer == nil {
		return
	}
	p.liveBuffer.NoteInterest(deviceID, backendID, sessionID)
}

func (p *EventPublisher) FlushLiveFrameBufferForDevice(conn Connection) {
	if p == nil || conn == nil || p.liveBuffer == nil {
		return
	}
	device := conn.AuthedDevice()
	if device == nil {
		return
	}
	frames := p.liveBuffer.Snapshot(device.DeviceID)
	if len(frames) == 0 {
		return
	}
	first, last := frames[0].Seq, frames[len(frames)-1].Seq
	LogLiveFrameBufferFlush(device.DeviceID, len(frames), first, last)
	for _, msg := range frames {
		// Re-check observation: if still milestones-only, skip pure deltas.
		if p.observation != nil &&
			!p.observation.ShouldSendEvent(device.DeviceID, msg.BackendID, msg.SessionID, msg.Event) &&
			!isLiveControlPlaneEvent(msg.Event) {
			// Soft: still try full_stream buffers after scope renew; if scope is
			// milestones, skip non-control live frames.
			if scope := p.observation.GetScope(device.DeviceID, msg.BackendID); scope == nil || scope.DeliveryMode != scopeFullStream {
				continue
			}
		}
		p.mu.Lock()
		sink := p.sinkLocked(conn)
		ok := sink.tryEnqueue(eventOutboundFrame{value: msg, classHint: classifyRelayEvent(msg.Event), classified: true})
		p.mu.Unlock()
		if !ok {
			slog.Warn("live-frame-buffer flush enqueue failed",
				"deviceID", safeID(device.DeviceID),
				"event", msg.Event,
				"seq", msg.Seq,
			)
			// Stop on backpressure; remaining frames stay until next flush/GC.
			break
		}
	}
}

