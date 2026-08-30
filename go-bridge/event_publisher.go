package gobridge

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

const (
	eventOutboundQueueCapacity        = 2048
	recoveryPendingMaxEvents          = 1000
	recoveryPendingMaxBytes           = 2 << 20
	recoveryPendingTimeout            = 30 * time.Second
	projectionFenceMaxPatches         = 128
	projectionFenceMaxBytes           = 2 << 20
	projectionNoObserverDropThreshold = 256 << 10
	// A failed rebind cannot become more useful on the next token. Keep a
	// short retry window so path-thrash recovery remains responsive without
	// turning a long stream into a registry scan/logging loop.
	rebindAttemptCooldown = time.Second
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
	// CatalogGeneration is observation-only correlation for sessions_changed. It is never
	// encoded into EventMessage and therefore cannot change the bridge protocol.
	CatalogGeneration uint64
	// PushIntent（web push §8.1）是不进 wire 的 producer 声明：该事件值得通知。
	// 只有位点清单允许的 live producer 可设置；默认 nil = 不发送。
	PushIntent *PushIntent
}

type eventOutboundFrame struct {
	value      interface{}
	requestID  string
	resultData interface{}
	resultErr  *WireError
	isResult   bool
	resultDone chan error
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
					if frame.resultDone != nil {
						frame.resultDone <- fmt.Errorf("connection closed before result delivery")
					}
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
				if frame.resultDone != nil {
					frame.resultDone <- fmt.Errorf("connection closed before result delivery")
				}
				if frame.delivered != nil {
					close(frame.delivered)
				}
				<-s.slots
				continue
			}
			// K4 probe: projection_patch write instrumentation. Detects where projection frames
			// vanish between sink dequeue and iOS receipt: marshal failure (json.Marshal the same
			// value SendJSON/WriteJSON will encode), stale-sink/wrong-conn (sink+remote identity),
			// or write timeout (elapsedMs near bridgeWriteTimeout). Scoped to projection_patch only.
			projProbe := false
			probeRev := 0
			probeMarshalErr := ""
			if projectionDiagnosticsEnabled() {
				if em, ok := frame.value.(EventMessage); ok && em.Event == "projection_patch" {
					projProbe = true
					if patch, perr := em.Data.(ProjectionPatch); perr {
						probeRev = patch.SyncRev
					}
					if _, merr := json.Marshal(frame.value); merr != nil {
						probeMarshalErr = merr.Error()
					}
				}
			}
			if projProbe {
				slog.Debug("go-bridge: [K4Patch] write_pre",
					"sink", fmt.Sprintf("%p", s),
					"remote", s.conn.RemoteAddr(),
					"syncRev", probeRev,
					"marshalErr", probeMarshalErr,
				)
			}
			writeStart := time.Now()
			var writeErr error
			if frame.isResult {
				s.conn.SendResult(frame.requestID, frame.resultData, frame.resultErr)
				if frame.resultDone != nil {
					frame.resultDone <- nil
				}
			} else if sender, ok := s.conn.(interface {
				SendJSONClassified(any, relayOutboundClass)
			}); ok && frame.classified {
				sender.SendJSONClassified(frame.value, frame.classHint)
			} else if reporter, ok := s.conn.(interface {
				SendJSONReport(any) error
			}); ok {
				// Prefer error-returning write so write_post can prove wire success
				// (plain SendJSON swallows closed-conn and WriteJSON errors).
				writeErr = reporter.SendJSONReport(frame.value)
			} else {
				s.conn.SendJSON(frame.value)
			}
			if projProbe {
				errStr := ""
				if writeErr != nil {
					errStr = writeErr.Error()
				}
				// Also record marshaled payload size so we can correlate with iOS WS-RAW.
				payloadSize := 0
				if b, merr := json.Marshal(frame.value); merr == nil {
					payloadSize = len(b)
				}
				slog.Debug("go-bridge: [K4Patch] write_post",
					"sink", fmt.Sprintf("%p", s),
					"remote", s.conn.RemoteAddr(),
					"syncRev", probeRev,
					"elapsedMs", time.Since(writeStart).Milliseconds(),
					"writeErr", errStr,
					"payloadBytes", payloadSize,
				)
			}
			if frame.delivered != nil {
				close(frame.delivered)
			}
			<-s.slots
		}
	}
}

// tryEnqueue 尝试非阻塞入队。失败时返回可判定的唯一原因与当时队列占用
// （queued = 已缓冲待写帧数），供「overflowed=1」三类原因（conn_closed /
// sink_stopped / queue_full）的定点取证。
func (s *eventOutboundSink) tryEnqueue(frame eventOutboundFrame) (ok bool, failReason string, queued int) {
	if closed, isClosable := s.conn.(interface{ isClosed() bool }); isClosable && closed.isClosed() {
		return false, "conn_closed", len(s.queue)
	}
	select {
	case <-s.stop:
		return false, "sink_stopped", len(s.queue)
	default:
	}
	select {
	case s.slots <- struct{}{}:
		select {
		case s.queue <- frame:
			return true, "", len(s.queue)
		case <-s.stop:
			<-s.slots
			return false, "sink_stopped", len(s.queue)
		}
	default:
		return false, "queue_full", len(s.queue)
	}
}

func (s *eventOutboundSink) close() {
	s.once.Do(func() { close(s.stop) })
}

// EventPublisher is the process-scoped owner of event identity and ordered
// publication. Stamping and destination enqueue happen under one lock; actual
// connection and offline I/O run outside that critical section.
type EventPublisher struct {
	mu                       sync.Mutex
	bridgeEpoch              string
	seq                      int
	perSessionSeq            map[string]int
	broadcaster              *Broadcaster
	sinks                    map[Connection]*eventOutboundSink
	offlineQueue             chan EventMessage
	offlineRoute             func(EventMessage)
	recoveries               map[Connection]*publisherRecovery
	buffer                   *EventBuffer
	liveBuffer               *LiveFrameBuffer
	now                      func() time.Time
	completed                map[Connection]string
	observation              *ObservationManager
	projection               *ProjectionReducer
	kernel                   *ProjectionKernel
	syncV2                   map[Connection]bool
	projectionEpochMismatch  map[Connection]bool
	readFileV2               map[Connection]bool
	catalogCursorEpochV2     map[Connection]bool
	projectionWindowV1       map[Connection]bool
	nextConnectionGeneration uint64
	connectionGenerations    map[Connection]uint64
	nextProjectionFenceID    uint64
	projectionFences         map[projectionFenceKey]*projectionSnapshotFence
	projectionSnapshotCuts   map[projectionFenceKey]int
	projectionDeliveryModes  map[Connection]map[string]ProjectionDeliveryMode
	// projectionHeldTurns tracks, per window-mode connection and session, the
	// turn ids its replica holds (from served window pages). Detail/state
	// commits route by it (turn_detail_lazy_v1 delivery rules 3/4): holders get
	// the content patch, non-holders the no-op revision patch.
	projectionHeldTurns   map[Connection]map[string]map[string]struct{}
	turnDetailV1          map[Connection]bool
	projectionInvalidated map[projectionFenceKey]bool
	projectionJournal     *ProjectionRevisionJournal
	// rebindLastAttempt throttles zero-target recovery. A stream can emit many
	// timeline events per second while a device is offline; rescanning the
	// device registry for every token only repeats the same failed lookup.
	rebindLastAttempt map[string]time.Time
	// rebindTargets is invoked (without p.mu) when a live event has zero online
	// targets. Handlers rebinds broadcaster subscriptions from device registry +
	// observation so mid-turn EMITs are not permanently dropped after path thrash.
	rebindTargets func(backendID, sessionID string) int
	// webPushSink（web push §8.1）接收 (EventMessage, PushIntent)。nil = 未接线，
	// 一切通知意图静默不产生 candidate（fail closed，不冒充发送）。
	webPushSink WebPushCandidateSink
}

// SetWebPushCandidateSink 注入 candidate sink（main.go 启动时；publisher 锁外调用）。
func (p *EventPublisher) SetWebPushCandidateSink(sink WebPushCandidateSink) {
	p.mu.Lock()
	p.webPushSink = sink
	p.mu.Unlock()
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
		bridgeEpoch:             bridgeEpoch,
		perSessionSeq:           make(map[string]int),
		sinks:                   make(map[Connection]*eventOutboundSink),
		offlineQueue:            make(chan EventMessage, eventOutboundQueueCapacity),
		recoveries:              make(map[Connection]*publisherRecovery),
		buffer:                  NewEventBuffer(EventBufferConfig{}),
		liveBuffer:              NewLiveFrameBuffer(),
		now:                     time.Now,
		completed:               make(map[Connection]string),
		projection:              NewProjectionReducer(),
		syncV2:                  make(map[Connection]bool),
		projectionEpochMismatch: make(map[Connection]bool),
		readFileV2:              make(map[Connection]bool),
		catalogCursorEpochV2:    make(map[Connection]bool),
		projectionWindowV1:      make(map[Connection]bool),
		connectionGenerations:   make(map[Connection]uint64),
		projectionFences:        make(map[projectionFenceKey]*projectionSnapshotFence),
		projectionSnapshotCuts:  make(map[projectionFenceKey]int),
		projectionDeliveryModes: make(map[Connection]map[string]ProjectionDeliveryMode),
		projectionHeldTurns:     make(map[Connection]map[string]map[string]struct{}),
		turnDetailV1:            make(map[Connection]bool),
		projectionInvalidated:   make(map[projectionFenceKey]bool),
		projectionJournal:       NewProjectionRevisionJournal(0, 0),
		rebindLastAttempt:       make(map[string]time.Time),
	}
	if len(broadcaster) > 0 {
		p.broadcaster = broadcaster[0]
	}
	go p.runOffline()
	return p
}

func (p *EventPublisher) BridgeEpoch() string { return p.bridgeEpoch }

func (p *EventPublisher) EventBuffer() *EventBuffer { return p.buffer }

// ProjectionReducer exposes the reducer instance owned by this publisher so the Projection
// Kernel can preserve the existing push/pull single-head invariant while it takes over
// lifecycle and checkpoint ownership.
func (p *EventPublisher) ProjectionReducer() *ProjectionReducer {
	if p == nil {
		return nil
	}
	return p.projection
}

func (p *EventPublisher) SetProjectionKernel(kernel *ProjectionKernel) {
	p.mu.Lock()
	p.kernel = kernel
	p.mu.Unlock()
}

// PublishProjectionPatch is the explicit Kernel-to-EventPublisher outlet. Hydrate itself never
// calls this; only post-cut live events committed after the baseline can produce a patch.
func (p *EventPublisher) PublishProjectionPatch(backendID, sessionID string, patch ProjectionPatch) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.recordProjectionPatchLocked(backendID, sessionID, patch)
	p.deliverProjectionPatchLocked(backendID, sessionID, patch)
	p.mu.Unlock()
}

// FlushPatchAndRecord fixes the publication lock order at EventPublisher.mu → ProjectionReducer.mu
// and records the exact committed patch before any target/filter decision. A patch therefore
// remains pull-resumable even when no v2 client was online for its push delivery.
func (p *EventPublisher) FlushPatchAndRecord(backendID, sessionID string) (ProjectionPatch, bool) {
	if p == nil || p.projection == nil {
		return ProjectionPatch{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	patch, ok := p.projection.FlushPatch(backendID, sessionID)
	if !ok {
		return ProjectionPatch{}, false
	}
	p.recordProjectionPatchLocked(backendID, sessionID, patch)
	p.deliverProjectionPatchLocked(backendID, sessionID, patch)
	return patch, true
}

func (p *EventPublisher) recordProjectionPatchLocked(backendID, sessionID string, patch ProjectionPatch) {
	if p.projectionJournal != nil {
		p.projectionJournal.Record(backendID, sessionID, patch, p.now())
	}
}

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

// SetRebindTargets installs a callback used when live delivery has zero targets.
// The callback must not call back into PublishLogical under the same stack with
// p.mu held — PublishLogical releases p.mu before invoking it on retry.
func (p *EventPublisher) SetRebindTargets(fn func(backendID, sessionID string) int) {
	p.mu.Lock()
	p.rebindTargets = fn
	p.mu.Unlock()
}

func (p *EventPublisher) RegisterConnection(conn Connection) {
	if conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.registerConnectionLocked(conn)
	p.sinkLocked(conn)
}

func (p *EventPublisher) ActiveRecoveryID(conn Connection) string {
	if p == nil || conn == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if recovery := p.recoveries[conn]; recovery != nil {
		return recovery.recoveryID
	}
	return ""
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
		gen := p.connectionGenerations[conn]
		p.mu.Unlock()
		slog.Info("event-publisher: control queue timeout closing conn",
			"remote", conn.RemoteAddr(),
			"generation", gen,
		)
		_ = conn.Close()
		return fmt.Errorf("outbound control queue timeout")
	}
	if delivered != nil {
		select {
		case <-delivered:
		case <-time.After(bridgeWriteTimeout):
			slog.Info("event-publisher: control delivery timeout closing conn",
				"remote", conn.RemoteAddr(),
				"generation", p.connectionGenerations[conn],
			)
			_ = conn.Close()
			return fmt.Errorf("outbound control delivery timeout")
		}
	}
	return nil
}

func (p *EventPublisher) UnregisterConnection(conn Connection) {
	p.mu.Lock()
	unregisterGeneration := p.connectionGenerations[conn]
	sink := p.sinks[conn]
	delete(p.sinks, conn)
	delete(p.recoveries, conn)
	delete(p.completed, conn)
	delete(p.syncV2, conn)
	delete(p.projectionEpochMismatch, conn)
	delete(p.readFileV2, conn)
	delete(p.catalogCursorEpochV2, conn)
	delete(p.projectionWindowV1, conn)
	delete(p.connectionGenerations, conn)
	delete(p.projectionDeliveryModes, conn)
	delete(p.projectionHeldTurns, conn)
	delete(p.turnDetailV1, conn)
	for key := range p.projectionFences {
		if key.conn == conn {
			delete(p.projectionFences, key)
		}
	}
	for key := range p.projectionSnapshotCuts {
		if key.conn == conn {
			delete(p.projectionSnapshotCuts, key)
		}
	}
	for key := range p.projectionInvalidated {
		if key.conn == conn {
			delete(p.projectionInvalidated, key)
		}
	}
	p.mu.Unlock()
	if sink != nil {
		sink.close()
	}
	slog.Info("event-publisher: connection unregistered",
		"remote", conn.RemoteAddr(),
		"generation", unregisterGeneration,
	)
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
		// Unknown or already-abandoned transaction: a late ack is a no-op.
		// Closing here turned a slow client ack into a reconnect storm
		// (2026-08-24: every hello re-entered full_resync and degraded in a
		// 38-connection loop), so the connection must survive.
		p.mu.Unlock()
		return nil
	}
	if recovery.cutBySession != nil {
		if len(applied) != 1 || !cutMapsEqual(recovery.cutBySession, applied[0]) {
			// Cut mismatch is a real protocol error, but the penalty must not
			// be a failed transport. Abandon the transaction and let the
			// client converge via live events + projection pull.
			delete(p.recoveries, conn)
			p.completed[conn] = recoveryID
			p.mu.Unlock()
			_ = p.EnqueueControl(conn, map[string]interface{}{
				"type":       "recovery_aborted",
				"recoveryId": recoveryID,
			}, false)
			return nil
		}
	}
	// A late acknowledgement is accepted: the transaction only orders relay
	// frames, so completing late converges to live delivery just as on-time
	// completion does. The old timeout-Close has been removed.
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

func (p *EventPublisher) registerConnectionLocked(conn Connection) uint64 {
	if generation := p.connectionGenerations[conn]; generation != 0 {
		return generation
	}
	p.nextConnectionGeneration++
	p.connectionGenerations[conn] = p.nextConnectionGeneration
	return p.nextConnectionGeneration
}

func projectionPatchEvent(bridgeEpoch, backendID, sessionID string, patch ProjectionPatch) EventMessage {
	return EventMessage{
		Type:          "event",
		Event:         "projection_patch",
		BackendID:     backendID,
		SessionID:     sessionID,
		PerSessionSeq: patch.SyncRev,
		BridgeEpoch:   bridgeEpoch,
		Data:          patch,
	}
}

func projectionInvalidateEvent(bridgeEpoch, backendID, sessionID string) EventMessage {
	return EventMessage{
		Type:        "event",
		Event:       "sync_invalidate",
		BackendID:   backendID,
		SessionID:   sessionID,
		BridgeEpoch: bridgeEpoch,
		Data: map[string]interface{}{
			"reason":      "gap",
			"bridgeEpoch": bridgeEpoch,
		},
	}
}

// ProjectionDeliveryMode is the R11b per-(conn, backend, session) delivery mode,
// registered at the connection's FIRST window-RPC hit (observed behavior, not a
// declared capability).
type ProjectionDeliveryMode string

const (
	ProjectionDeliveryWindow ProjectionDeliveryMode = "window"
	ProjectionDeliveryFull   ProjectionDeliveryMode = "full"
)

// SetConnProjectionDeliveryMode registers the R11b delivery mode for
// (conn, backend, session). Window RPCs register window mode; connections that
// only ever pull full projections stay on the full default.
func (p *EventPublisher) SetConnProjectionDeliveryMode(conn Connection, backendID, sessionID string, mode ProjectionDeliveryMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := projectionDeliveryKey(backendID, sessionID)
	if p.projectionDeliveryModes[conn] == nil {
		p.projectionDeliveryModes[conn] = make(map[string]ProjectionDeliveryMode)
	}
	p.projectionDeliveryModes[conn][key] = mode
}

// ConnProjectionDeliveryMode reports the registered mode; absent = full
// (today's behavior for every non-window connection).
func (p *EventPublisher) ConnProjectionDeliveryMode(conn Connection, backendID, sessionID string) ProjectionDeliveryMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connDeliveryModeLocked(conn, backendID, sessionID)
}

func (p *EventPublisher) connDeliveryModeLocked(conn Connection, backendID, sessionID string) ProjectionDeliveryMode {
	if modes := p.projectionDeliveryModes[conn]; modes != nil {
		if mode, ok := modes[projectionDeliveryKey(backendID, sessionID)]; ok {
			return mode
		}
	}
	return ProjectionDeliveryFull
}

func projectionDeliveryKey(backendID, sessionID string) string {
	return backendID + "|" + sessionID
}

// PublishProjectionPrepend routes a structural historical prepend commit (R11a)
// per delivery mode (R11b):
//   - the REQUESTING connection receives nothing — its page rides the window
//     result admitted at the new cut (R3 unique page ownership);
//   - other WINDOW connections receive a connection-specific no-op revision
//     patch (R11c) advancing their appliedRev along the single chain without
//     content; the prepended turns reach them through their own window pulls;
//   - FULL-projection connections receive sync_invalidate so their next
//     get_session_projection re-syncs order-correct complete truth (a replica
//     cannot express a front insert via upsertTurns).
//
// No content patch is broadcast or journaled for this revision (journal gap by
// design — see ProjectionReducer.PrependHistoricalTurns).
func (p *EventPublisher) PublishProjectionPrepend(backendID, sessionID string, syncRev int, requester Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.broadcaster == nil || len(p.syncV2) == 0 {
		return
	}
	targets := p.broadcaster.Targets(backendID, sessionID, "")
	for _, conn := range targets {
		if !p.syncV2[conn] || conn == requester {
			continue
		}
		key := projectionFenceKey{conn: conn, backendID: backendID, sessionID: sessionID}
		if p.connDeliveryModeLocked(conn, backendID, sessionID) == ProjectionDeliveryWindow {
			base := p.projectionSnapshotCuts[key]
			if fence := p.projectionFences[key]; fence != nil && !fence.invalidated {
				base = fence.expectedRev
			}
			if base == 0 || base >= syncRev {
				// Not yet synced onto this chain (or already past it): its own
				// snapshot/window pull will land at the new head — no frame needed.
				continue
			}
			p.deliverSingleConnPatchLocked(conn, backendID, sessionID, ProjectionPatch{BaseRev: base, SyncRev: syncRev})
			continue
		}
		p.enqueueProjectionInvalidateLocked(conn, backendID, sessionID)
	}
}

// deferProjectionPatchForFenceLocked queues a rule-4-routed detail commit
// behind an in-flight window fence as UNDECIDED (audit P0-2): whether the
// connection holds the patch's turns only becomes known when the fence
// completes and the response's turns are registered — the completion
// transaction then resolves content vs no-op. Deciding no-op here (as the
// pre-fix code did) would strand the detail behind the stale response.
// Caller MUST hold p.mu.
func (p *EventPublisher) deferProjectionPatchForFenceLocked(
	backendID, sessionID string,
	fence *projectionSnapshotFence, patch ProjectionPatch,
) {
	p.appendFencePatchLocked(backendID, sessionID, fence, patch, true)
}

// appendFencePatchLocked admits one patch into an active snapshot fence: chain
// and byte guards invalidate the fence on mismatch/overflow (the client then
// re-syncs from the authoritative snapshot), otherwise the patch queues behind
// the response in arrival (= revision chain) order. decide marks a rule-4
// deferral whose content-vs-no-op routing resolves at fence completion.
// Caller MUST hold p.mu.
func (p *EventPublisher) appendFencePatchLocked(
	backendID, sessionID string,
	fence *projectionSnapshotFence, patch ProjectionPatch, decide bool,
) {
	if patch.SyncRev <= fence.expectedRev || fence.invalidated {
		return
	}
	if patch.BaseRev != fence.expectedRev {
		slog.Info("go-bridge: [K4Patch] drop",
			"sessionPrefix", projectionSessionLogPrefix(sessionID),
			"reason", "fence_base_mismatch",
			"baseRev", patch.BaseRev, "expectedRev", fence.expectedRev,
		)
		fence.pending = nil
		fence.pendingBytes = 0
		fence.invalidated = true
		return
	}
	encoded, _ := json.Marshal(patch)
	if len(fence.pending)+1 > projectionFenceMaxPatches ||
		fence.pendingBytes+len(encoded) > projectionFenceMaxBytes {
		slog.Info("go-bridge: [K4Patch] drop",
			"sessionPrefix", projectionSessionLogPrefix(sessionID),
			"reason", "fence_overflow", "pending", len(fence.pending),
		)
		fence.pending = nil
		fence.pendingBytes = 0
		fence.invalidated = true
		return
	}
	fence.pending = append(fence.pending, projectionFencePendingPatch{patch: patch, decide: decide})
	fence.pendingBytes += len(encoded)
	fence.expectedRev = patch.SyncRev
}

// deliverSingleConnPatchLocked sends ONE patch to ONE connection through the same
// fence/cut/sink machinery as the broadcast path. Caller MUST hold p.mu.
func (p *EventPublisher) deliverSingleConnPatchLocked(conn Connection, backendID, sessionID string, patch ProjectionPatch) {
	msg := projectionPatchEvent(p.bridgeEpoch, backendID, sessionID, patch)
	classHint := classifyRelayEvent("projection_patch")
	key := projectionFenceKey{conn: conn, backendID: backendID, sessionID: sessionID}
	if fence := p.projectionFences[key]; fence != nil {
		p.appendFencePatchLocked(backendID, sessionID, fence, patch, false)
		return
	}
	if p.projectionInvalidated[key] {
		return
	}
	if patch.SyncRev <= p.projectionSnapshotCuts[key] {
		return
	}
	if cut := p.projectionSnapshotCuts[key]; cut != 0 && patch.BaseRev != cut {
		p.enqueueProjectionInvalidateLocked(conn, backendID, sessionID)
		p.projectionInvalidated[key] = true
		return
	}
	if ok, _, _ := p.sinkLocked(conn).tryEnqueue(eventOutboundFrame{value: msg, classHint: classHint, classified: true}); ok {
		p.projectionSnapshotCuts[key] = patch.SyncRev
	}
}

// deliverProjectionPatchLocked delivers a projection_patch frame to the v2 (session_sync_v2)
// observers of a session. Caller MUST hold p.mu. Targets come from the broadcaster — the same
// target table raw dispatch uses (design §6.5 — no separate delivery network); only conns marked
// syncV2 receive the patch, so legacy conns are untouched. Best-effort: a dropped patch (sink
// overflow / no online target) is recoverable via get_session_projection pull (design §8.4 option
// A — projection frames are NOT durable/live-buffered). This is the single outbound projection
// path; it reuses sinks + broadcaster, adding no new transport (design §6.2, N8).
func (p *EventPublisher) deliverProjectionPatchLocked(backendID, sessionID string, patch ProjectionPatch) {
	if p.broadcaster == nil || len(p.syncV2) == 0 {
		slog.Debug("go-bridge: [K4Patch] drop",
			"sessionPrefix", projectionSessionLogPrefix(sessionID),
			"reason", "no_v2_conns", "syncRev", patch.SyncRev,
		)
		return
	}
	targets := p.broadcaster.Targets(backendID, sessionID, "")
	if len(targets) == 0 {
		slog.Debug("go-bridge: [K4Patch] drop",
			"sessionPrefix", projectionSessionLogPrefix(sessionID),
			"reason", "no_targets", "syncRev", patch.SyncRev,
		)
		return
	}
	msg := projectionPatchEvent(p.bridgeEpoch, backendID, sessionID, patch)
	classHint := classifyRelayEvent("projection_patch")
	for _, conn := range targets {
		if !p.syncV2[conn] {
			continue
		}
		recoveryID := ""
		if recovery := p.recoveries[conn]; recovery != nil {
			recoveryID = recovery.recoveryID
		}
		logProjectionPatchMetrics(backendID, sessionID, recoveryID, patch)
		// Design §6.5: projection push reuses observation filter (same as raw path).
		if p.observation != nil {
			if device := conn.AuthedDevice(); device != nil &&
				!p.observation.ShouldSendEvent(device.DeviceID, backendID, sessionID, "projection_patch") {
				continue
			}
		}
		sink := p.sinkLocked(conn)
		if sink == nil {
			continue
		}
		key := projectionFenceKey{conn: conn, backendID: backendID, sessionID: sessionID}
		if fence := p.projectionFences[key]; fence != nil {
			if patch.SyncRev <= fence.expectedRev || fence.invalidated {
				slog.Info("go-bridge: [K4Patch] drop",
					"sessionPrefix", projectionSessionLogPrefix(sessionID),
					"reason", "fence_stale_or_invalidated",
					"syncRev", patch.SyncRev, "expectedRev", fence.expectedRev,
					"invalidated", fence.invalidated,
				)
				continue
			}
			p.appendFencePatchLocked(backendID, sessionID, fence, patch, false)
			if !fence.invalidated {
				slog.Info("go-bridge: [K4Patch] held_in_fence",
					"sessionPrefix", projectionSessionLogPrefix(sessionID),
					"syncRev", patch.SyncRev, "pending", len(fence.pending),
				)
			}
			continue
		}
		if p.projectionInvalidated[key] {
			continue
		}
		if patch.SyncRev <= p.projectionSnapshotCuts[key] {
			continue
		}
		if cut := p.projectionSnapshotCuts[key]; cut != 0 && patch.BaseRev != cut {
			p.enqueueProjectionInvalidateLocked(conn, backendID, sessionID)
			p.projectionInvalidated[key] = true
			continue
		}
		// Best-effort enqueue; overflow is recoverable via pull. No live-buffer interest note
		// (projection frames are reconstructable, not live-bufferable).
		if ok, reason, queued := sink.tryEnqueue(eventOutboundFrame{value: msg, classHint: classHint, classified: true}); ok {
			p.projectionSnapshotCuts[key] = patch.SyncRev
			slog.Debug("go-bridge: [K4Patch] delivered",
				"sessionPrefix", projectionSessionLogPrefix(sessionID),
				"syncRev", patch.SyncRev,
				"remote", conn.RemoteAddr(),
				"device", func() string {
					if d := conn.AuthedDevice(); d != nil {
						return d.DeviceID
					}
					return ""
				}(),
			)
		} else {
			p.projectionInvalidated[key] = true
			slog.Info("go-bridge: [K4Patch] drop",
				"sessionPrefix", projectionSessionLogPrefix(sessionID),
				"reason", "sink_overflow", "syncRev", patch.SyncRev,
				"sink", reason, "queued", queued,
			)
		}
	}
}

// hasProjectionPatchTargetLocked reports whether a live projection patch has
// at least one eligible v2 observer. Caller must hold p.mu. It intentionally
// uses the same broadcaster/observation gates as delivery, so a session with no
// eligible observer can discard its pending patch and retain only authoritative
// reducer state until the next snapshot pull.
func (p *EventPublisher) hasProjectionPatchTargetLocked(backendID, sessionID string) bool {
	if p == nil || p.broadcaster == nil || len(p.syncV2) == 0 {
		return false
	}
	for _, conn := range p.broadcaster.Targets(backendID, sessionID, "") {
		if !p.syncV2[conn] {
			continue
		}
		if p.observation != nil {
			if device := conn.AuthedDevice(); device != nil &&
				!p.observation.ShouldSendEvent(device.DeviceID, backendID, sessionID, "projection_patch") {
				continue
			}
		}
		return true
	}
	return false
}

type eventPublishMode uint8

const (
	publishTimelineEvent eventPublishMode = iota
	publishControlPlaneEvent
	publishPreReducedTimelineEvent
)

// PublishLogical publishes a timeline event through the process-scoped ordering,
// recovery, observation and delivery pipeline. Timeline events are reduced into
// the Projection Kernel before their raw frame is delivered.
func (p *EventPublisher) PublishLogical(logical LogicalEvent) EventMessage {
	msg, err := p.publish(logical, publishTimelineEvent)
	if err != nil {
		panic(err)
	}
	return msg
}

// PublishControlPlane publishes the only catalog control-plane push currently
// allowed by the protocol. Validation happens before publish acquires the ordering
// lock, stamps Seq/EventID, appends EventBuffer state, resolves targets, or touches
// any delivery side effect. The shared publish primitive deliberately skips only
// Projection Kernel/reducer ingestion and projection patch flushing.
func (p *EventPublisher) PublishControlPlane(logical LogicalEvent) (EventMessage, error) {
	if err := validateControlPlaneEvent(logical); err != nil {
		return EventMessage{}, err
	}
	return p.publish(logical, publishControlPlaneEvent)
}

// publishPreReducedTimeline is package-private because only a projection transaction that has
// already committed the same logical source record may use it. It preserves the legacy raw frame
// while routing stamping, buffering, recovery, observation and delivery through publish.
func (p *EventPublisher) publishPreReducedTimeline(logical LogicalEvent) EventMessage {
	msg, err := p.publish(logical, publishPreReducedTimelineEvent)
	if err != nil {
		panic(err)
	}
	return msg
}

func validateControlPlaneEvent(logical LogicalEvent) error {
	// Phase 5：background_tasks_changed 是 sessions_changed 同形的 backend 级
	// invalidate 通知（不携带任务数据，客户端重新 list 拿真值）。
	if logical.Event != "sessions_changed" && logical.Event != "background_tasks_changed" {
		return fmt.Errorf("control-plane event %q is not allowed", logical.Event)
	}
	if logical.BackendID == "" {
		return fmt.Errorf("control-plane %s requires backend ID", logical.Event)
	}
	if logical.SessionID != "" {
		return fmt.Errorf("control-plane %s must not be session-scoped", logical.Event)
	}
	return nil
}

// publish is the single stamping and delivery primitive for timeline and
// control-plane logical events. mode is private so callers cannot opt arbitrary
// events out of the Projection Kernel through a public reduce flag.
func (p *EventPublisher) publish(logical LogicalEvent, mode eventPublishMode) (EventMessage, error) {
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
	// ProjectionReducer mount (design §6.2): reduce the stamped event into the authoritative
	// SessionProjection under the publisher ordering lock. Projection revision advances only
	// when the reducer commits a mutation; it is distinct from transport perSessionSeq.
	projectionApplied := false
	kernelIngest := ProjectionIngestNoChange
	kernelIngested := false
	if mode == publishControlPlaneEvent || mode == publishPreReducedTimelineEvent {
		// Catalog control-plane events intentionally do not enter the timeline.
		// Pre-reduced timeline events have already committed through the Kernel transaction.
	} else if isDerivedLegacyQuestionEvent(logical.Event) {
		// Strictly one-way: canonical user_input -> legacy question presentation. The derived raw
		// frame is never allowed to become a second projection writer.
	} else if p.kernel != nil {
		// R2-O1 baseline: only ProjectionIngestApplied may trigger the live-path patch
		// flush below. Deferred rows flush via the hydrate commit's own path; NoChange
		// (duplicates/no-ops) never flushes (web push §8.1 tri-state).
		kernelIngest = p.kernel.IngestLive(msg)
		kernelIngested = true
		projectionApplied = kernelIngest == ProjectionIngestApplied
	} else if p.projection != nil {
		before := p.projection.LastAppliedRev(logical.BackendID, logical.SessionID)
		p.projection.Apply(msg)
		projectionApplied = p.projection.LastAppliedRev(logical.BackendID, logical.SessionID) != before
	}
	// Web Push candidate（§8.1）：authoritative ingest 之后、transport 分支之前。锁内
	// 只做小对象复制 + 非阻塞有界入队；零在线 target 不影响 candidate（后台通知正是
	// 为不在线的 PWA 存在）。非 kernel 路径与 control-plane/pre-reduced/derived 路径
	// 不允许携带 intent——出现即 producer 违约，fail closed 丢弃并计数。
	if logical.PushIntent != nil && p.webPushSink != nil {
		if kernelIngested {
			p.webPushSink.Ingest(WebPushCandidate{
				BackendID:       msg.BackendID,
				SessionID:       msg.SessionID,
				EventID:         msg.EventID,
				Kind:            logical.PushIntent.Kind,
				NotificationKey: logical.PushIntent.NotificationKey,
				AnchorKind:      logical.PushIntent.AnchorKind,
				AnchorID:        logical.PushIntent.AnchorID,
				SessionTitle:    logical.PushIntent.SessionTitle,
				ContentPreview:  logical.PushIntent.ContentPreview,
				ReceivedAt:      msg.Timestamp,
			}, kernelIngest)
		} else {
			slog.Warn("web-push: PushIntent present on non-kernel publish path (dropped)",
				"backendID", msg.BackendID,
				"sessionPrefix", projectionSessionLogPrefix(msg.SessionID),
				"event", msg.Event,
			)
		}
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
	overflowDetail := make(map[Connection]string, 0)
	recoveryDegraded := make(map[Connection]string, 0)
	waits := make([]eventDeliveryWait, 0, len(waitTargets))
	rawEligible := make(map[Connection]struct{}, len(targets))
	enqueued := 0
	observationFiltered := 0
	for conn := range targets {
		if !p.shouldDeliverRawEventLocked(conn, logical.BackendID, logical.SessionID, logical.Event) {
			continue
		}
		rawEligible[conn] = struct{}{}
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
				// Degrade instead of fail-closed: drop the transaction, replay
				// its pending buffer in order, deliver this event live, and
				// notify the client (recovery_aborted) so it stops waiting for
				// an acknowledgement no one will send. Closing the conn here fed
				// a reconnect-storm loop in which every hello re-entered
				// full_resync (2026-08-24 evidence: 38 conns in ~60s).
				delete(p.recoveries, conn)
				recoveryDegraded[conn] = recovery.recoveryID
				sink := p.sinkLocked(conn)
				replayFailed := false
				for _, pend := range recovery.pending {
					if ok, reason, queued := sink.tryEnqueue(eventOutboundFrame{
						value:      pend,
						classHint:  classifyRelayEvent(pend.Event),
						classified: true,
					}); !ok {
						overflowed = append(overflowed, conn)
						overflowDetail[conn] = fmt.Sprintf("recovery_replay tryEnqueue=%s queued=%d", reason, queued)
						replayFailed = true
						break
					} else {
						enqueued++
					}
				}
				if replayFailed {
					continue
				}
				// Fall through: current event is delivered live below.
			} else {
				recovery.pending = append(recovery.pending, msg)
				recovery.pendingBytes += len(encoded)
				enqueued++
				continue
			}
		}
		sink := p.sinkLocked(conn)
		var delivered chan struct{}
		if _, shouldWait := waitTargets[conn]; shouldWait {
			delivered = make(chan struct{})
			waits = append(waits, eventDeliveryWait{conn: conn, delivered: delivered})
		}
		if ok, reason, queued := sink.tryEnqueue(eventOutboundFrame{value: msg, delivered: delivered, classHint: logical.ClassHint, classified: true}); !ok {
			overflowed = append(overflowed, conn)
			overflowDetail[conn] = fmt.Sprintf("tryEnqueue=%s queued=%d", reason, queued)
		} else {
			enqueued++
			if p.liveBuffer != nil {
				if d := conn.AuthedDevice(); d != nil && logical.BackendID != "" && logical.SessionID != "" {
					p.liveBuffer.NoteInterest(d.DeviceID, logical.BackendID, logical.SessionID)
				}
			}
		}
	}

	// Zero-target recovery: rebind session subscriptions from device registry +
	// observation, then retry delivery once for this stamped event (projection
	// already applied; do not re-Apply). Fixes mid-turn candidateTargets=0 after
	// path thrash while the device still has an open transport.
	canRebind := p.rebindTargets != nil && logical.BackendID != "" && logical.SessionID != ""
	if canRebind && p.broadcaster != nil && !p.broadcaster.HasConnections() {
		// There is no live connection to recover. Skipping the callback avoids
		// scanning the device registry once per token while an external turn is
		// still producing events with every client offline.
		canRebind = false
	}
	if enqueued == 0 && len(overflowed) == 0 && (len(targets) == 0 || len(rawEligible) > 0) && !logical.Offline && canRebind {
		// A missing target is stable across adjacent stream events. Reserve one
		// retry slot per session for the cooldown window; this preserves quick
		// recovery after a path switch while avoiding a registry scan on every
		// token when the device is actually gone.
		rebindKey := logical.BackendID + "\x00" + logical.SessionID
		now := p.now()
		if last, ok := p.rebindLastAttempt[rebindKey]; ok && now.Before(last.Add(rebindAttemptCooldown)) {
			canRebind = false
		} else {
			p.rebindLastAttempt[rebindKey] = now
		}
	}
	if enqueued == 0 && len(overflowed) == 0 && (len(targets) == 0 || len(rawEligible) > 0) && !logical.Offline && canRebind {
		rebind := p.rebindTargets
		p.mu.Unlock()
		n := rebind(logical.BackendID, logical.SessionID)
		p.mu.Lock()
		if n > 0 {
			delete(p.rebindLastAttempt, logical.BackendID+"\x00"+logical.SessionID)
			if logical.Broadcast && p.broadcaster != nil {
				for _, conn := range p.broadcaster.Targets(logical.BackendID, logical.SessionID, logical.Directory) {
					targets[conn] = struct{}{}
				}
			}
			for conn := range targets {
				if !p.shouldDeliverRawEventLocked(conn, logical.BackendID, logical.SessionID, logical.Event) {
					continue
				}
				rawEligible[conn] = struct{}{}
				if p.observation != nil {
					if device := conn.AuthedDevice(); device != nil &&
						!p.observation.ShouldSendEvent(device.DeviceID, logical.BackendID, logical.SessionID, logical.Event) {
						observationFiltered++
						continue
					}
				}
				if recovery := p.recoveries[conn]; recovery != nil {
					continue
				}
				sink := p.sinkLocked(conn)
				if ok, reason, queued := sink.tryEnqueue(eventOutboundFrame{value: msg, classHint: logical.ClassHint, classified: true}); ok {
					enqueued++
					if p.liveBuffer != nil {
						if d := conn.AuthedDevice(); d != nil {
							p.liveBuffer.NoteInterest(d.DeviceID, logical.BackendID, logical.SessionID)
						}
					}
				} else {
					slog.Warn("event-publisher: rebind retry enqueue failed",
						"backendID", logical.BackendID,
						"sessionID", logical.SessionID,
						"remote", conn.RemoteAddr(),
						"tryEnqueue", reason, "queued", queued,
					)
				}
			}
		}
	}

	// Session Projection Stream (session_sync_v2): flush + deliver a projection_patch to the
	// session's v2 observers. The reducer state was already advanced by Apply above; this is the
	// single outbound projection path, reusing broadcaster targets + sinks (design §6.2 — no
	// parallel pipe).
	//
	// Offline flag semantics:
	// - LogicalEvent.Offline=true means "also route this raw event to durable offline/mailbox".
	//   Live durable milestones (turn_completed etc.) intentionally set Offline=true via
	//   IsDurableMilestone so offline devices can catch up later.
	// - That flag must NOT suppress live projection_patch fanout for online v2 observers.
	//   Otherwise end-of-turn execution.phase=idle never reaches iOS (K4 G4 evidence:
	//   turn_completed flush offline=true with no [K4Patch] delivered).
	// - True cold-hydrate mid-scan still must not fan out: hydrate goes through
	//   ProjectionKernel.ApplyHydrateEvent / CommitHydrateTransaction and never calls
	//   PublishLogical, so this live path is not that gate (design §5.3 / D2).
	// Phase 1 flushes per-event for live correctness; timed coalesce remains an optional
	// bandwidth optimization (design §2.3 / §10 item 3).
	if projectionApplied && p.projection != nil && logical.BackendID != "" && logical.SessionID != "" {
		noObserver := !p.hasProjectionPatchTargetLocked(logical.BackendID, logical.SessionID)
		if noObserver && p.projection.PendingPatchExceeds(logical.BackendID, logical.SessionID, projectionNoObserverDropThreshold) {
			// No v2 observer can receive a live patch. Keep the reducer's
			// authoritative state, but discard only the unsent delta accumulator;
			// a later get_session_projection supplies the complete current state.
			if p.projection.DropPendingPatch(logical.BackendID, logical.SessionID) {
				slog.Debug("go-bridge: [K4Patch] discard_without_observer",
					"sessionPrefix", projectionSessionLogPrefix(logical.SessionID),
					"event", logical.Event,
					"syncRev", p.projection.LastAppliedRev(logical.BackendID, logical.SessionID),
				)
			}
		} else if patch, flushOk := p.projection.FlushPatch(logical.BackendID, logical.SessionID); flushOk {
			p.recordProjectionPatchLocked(logical.BackendID, logical.SessionID, patch)
			slog.Debug("go-bridge: [K4Patch] flush",
				"sessionPrefix", projectionSessionLogPrefix(logical.SessionID),
				"event", logical.Event,
				"syncRev", patch.SyncRev,
				"baseRev", patch.BaseRev,
				"offline", logical.Offline,
				"kernelMode", p.kernel != nil,
			)
			// Always deliver live projection patches from PublishLogical. Offline only
			// controls raw durable mailbox routing below, not the projection SoT stream.
			p.deliverProjectionPatchLocked(logical.BackendID, logical.SessionID, patch)
		}
	}

	// Visibility for "Mac EMIT + iOS no EVT-RECV": zero live targets after
	// observation filter means the frame will not reach any online device.
	// Buffer live frames for interested degraded devices so reconnect can flush
	// instead of permanent jump via history bulk (live-frame-buffer design).
	if enqueued == 0 && len(overflowed) == 0 {
		switch logical.Event {
		case "text_delta", "reasoning_delta", "turn_started", "turn_completed", "tool_started", "tool_finished":
			slog.Debug("event-publisher: live event has zero online targets",
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
	if mode == publishControlPlaneEvent {
		slog.Info("event-publisher: control-plane delivery outcome",
			"event", logical.Event,
			"backendID", logical.BackendID,
			"catalogGeneration", logical.CatalogGeneration,
			"candidateTargets", len(targets),
			"enqueued", enqueued,
			"observationFiltered", observationFiltered,
			"overflowed", len(overflowed),
			"overflowDetail", overflowDetailText(overflowDetail, p))
	}
	for conn, recoveryID := range recoveryDegraded {
		slog.Info("event-publisher: recovery transaction degraded; notifying client",
			"remote", conn.RemoteAddr(),
			"generation", p.connectionGenerations[conn],
			"recoveryID", recoveryID,
		)
		_ = p.EnqueueControl(conn, map[string]interface{}{
			"type":       "recovery_aborted",
			"recoveryId": recoveryID,
		}, false)
	}

	for _, conn := range overflowed {
		if conn != nil {
			slog.Info("event-publisher: fail-closed closing conn",
				"remote", conn.RemoteAddr(),
				"generation", p.connectionGenerations[conn],
				"detail", overflowDetail[conn],
			)
			_ = conn.Close()
		}
	}
	for _, wait := range waits {
		select {
		case <-wait.delivered:
		case <-time.After(bridgeWriteTimeout):
			slog.Info("event-publisher: delivery-wait timeout closing conn",
				"remote", wait.conn.RemoteAddr(),
				"generation", p.connectionGenerations[wait.conn],
			)
			_ = wait.conn.Close()
		}
	}
	return msg, nil
}

// overflowDetailText 把带 Connection 键的失败明细压成一行日志文本（按 remote 归类）。
func overflowDetailText(detail map[Connection]string, p *EventPublisher) []string {
	if len(detail) == 0 {
		return nil
	}
	out := make([]string, 0, len(detail))
	for conn, d := range detail {
		out = append(out, fmt.Sprintf("%s(gen=%d) %s", conn.RemoteAddr(), p.connectionGenerations[conn], d))
	}
	sort.Strings(out)
	return out
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
		ok, enqueueReason, queued := sink.tryEnqueue(eventOutboundFrame{value: msg, classHint: classifyRelayEvent(msg.Event), classified: true})
		p.mu.Unlock()
		if !ok {
			slog.Warn("live-frame-buffer flush enqueue failed",
				"deviceID", safeID(device.DeviceID),
				"event", msg.Event,
				"seq", msg.Seq,
				"tryEnqueue", enqueueReason, "queued", queued,
			)
			// Stop on backpressure; remaining frames stay until next flush/GC.
			break
		}
	}
}

// RecordConnWindowTurns records the turn ids a window response delivered to a
// connection (turn_detail_lazy_v1 delivery rule 3/4 bookkeeping). Idempotent;
// bounded by the session's turn count per connection.
func (p *EventPublisher) RecordConnWindowTurns(conn Connection, backendID, sessionID string, turnIDs []string) {
	if p == nil || conn == nil || len(turnIDs) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordConnWindowTurnsLocked(conn, backendID, sessionID, turnIDs)
}

// recordConnWindowTurnsLocked is the p.mu-held core of RecordConnWindowTurns,
// shared with the snapshot-fence completion transaction (P0-2).
func (p *EventPublisher) recordConnWindowTurnsLocked(conn Connection, backendID, sessionID string, turnIDs []string) {
	key := projectionDeliveryKey(backendID, sessionID)
	if p.projectionHeldTurns[conn] == nil {
		p.projectionHeldTurns[conn] = make(map[string]map[string]struct{})
	}
	if p.projectionHeldTurns[conn][key] == nil {
		p.projectionHeldTurns[conn][key] = make(map[string]struct{}, len(turnIDs))
	}
	for _, id := range turnIDs {
		if id != "" {
			p.projectionHeldTurns[conn][key][id] = struct{}{}
		}
	}
}

// ClearConnWindowTurns forgets a connection's held-turn set for one session —
// used when the connection is invalidated (sync_invalidate) so it re-records
// from its own re-pull.
func (p *EventPublisher) ClearConnWindowTurns(conn Connection, backendID, sessionID string) {
	if p == nil || conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearConnWindowTurnsLocked(conn, backendID, sessionID)
}

func (p *EventPublisher) clearConnWindowTurnsLocked(conn Connection, backendID, sessionID string) {
	if per := p.projectionHeldTurns[conn]; per != nil {
		delete(per, projectionDeliveryKey(backendID, sessionID))
	}
}

func (p *EventPublisher) connHoldsAnyTurnLocked(conn Connection, backendID, sessionID string, turnIDs []string) bool {
	per := p.projectionHeldTurns[conn]
	if per == nil {
		return false
	}
	held := per[projectionDeliveryKey(backendID, sessionID)]
	if held == nil {
		return false
	}
	for _, id := range turnIDs {
		if _, ok := held[id]; ok {
			return true
		}
	}
	return false
}

// patchTurnIDs unions the turn ids a patch mutates (turnStateOps + partOps +
// upsertTurns) — the routing predicate for detail/state commits.
func patchTurnIDs(patch ProjectionPatch) []string {
	seen := make(map[string]struct{}, len(patch.TurnStateOps)+len(patch.PartOps)+len(patch.UpsertTurns))
	ids := make([]string, 0, len(seen))
	add := func(id string) {
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, op := range patch.TurnStateOps {
		add(op.TurnID)
	}
	for _, op := range patch.PartOps {
		add(op.TurnID)
	}
	for _, turn := range patch.UpsertTurns {
		add(turn.TurnID)
	}
	return ids
}

// PublishProjectionDetail routes a detail/state commit (turn_detail_lazy_v1
// delivery rules, bridge-v1.md) through ONE serial publisher transaction:
//   1. the REQUESTING connection always receives the commit patches — its
//      completion condition is appliedRev >= ack.syncRev (patch-before-ack and
//      ack-before-patch are both valid);
//   2. full-projection connections always receive them (full-truth obligation);
//   3. other WINDOW connections that hold one of the patch's turns receive the
//      full commit patch;
//   4. window connections that do NOT hold any of them receive the
//      connection-specific no-op revision patch that keeps the single revision
//      chain intact (they obtain the turn, with its current detailLoadState,
//      through their own window pull).
//
// patches is the P0-1 atomic chain from the reducer: [staged-live patch?] +
// [commit patch]. The staged-live prefixes publish FIRST through the normal
// live broadcast path (content to every v2 connection — same semantics as any
// live delta), then the final commit patch routes by rules 1-4. Journal order
// matches publication order.
func (p *EventPublisher) PublishProjectionDetail(backendID, sessionID string, patches []ProjectionPatch, requester Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.broadcaster == nil || len(p.syncV2) == 0 || len(patches) == 0 {
		return
	}
	commit := patches[len(patches)-1]
	if commit.SyncRev <= commit.BaseRev {
		return
	}
	for _, patch := range patches {
		if patch.SyncRev <= patch.BaseRev {
			continue
		}
		p.projectionJournal.Record(backendID, sessionID, patch, p.now())
	}
	for _, live := range patches[:len(patches)-1] {
		p.deliverProjectionPatchLocked(backendID, sessionID, live)
	}
	targets := p.broadcaster.Targets(backendID, sessionID, "")
	turnIDs := patchTurnIDs(commit)
	for _, conn := range targets {
		if !p.syncV2[conn] {
			continue
		}
		if conn == requester ||
			p.connDeliveryModeLocked(conn, backendID, sessionID) != ProjectionDeliveryWindow ||
			p.connHoldsAnyTurnLocked(conn, backendID, sessionID, turnIDs) {
			p.deliverSingleConnPatchLocked(conn, backendID, sessionID, commit)
			continue
		}
		key := projectionFenceKey{conn: conn, backendID: backendID, sessionID: sessionID}
		base := p.projectionSnapshotCuts[key]
		if fence := p.projectionFences[key]; fence != nil && !fence.invalidated {
			// The connection's window response is still being built — whether it
			// "holds" the patch's turns is decided when that fence completes
			// (CompleteProjectionSnapshotWithHeldTurns registers the response's
			// turns and resolves the deferral). Queue the FULL patch as
			// undecided so the completion transaction can pick content vs no-op
			// (audit P0-2: a no-op decided here would permanently strand the
			// detail behind the not-yet-sent stale response).
			p.deferProjectionPatchForFenceLocked(backendID, sessionID, fence, commit)
			continue
		}
		if base == 0 || base >= commit.SyncRev {
			continue
		}
		p.deliverSingleConnPatchLocked(conn, backendID, sessionID, ProjectionPatch{BaseRev: base, SyncRev: commit.SyncRev})
	}
}

// LoadedDetailRev recovers the syncRev of the commit that set a turn's terminal
// loaded state (for §11.7 idempotent repeats: "携带原 commit 的 syncRev"). The
// journal is process-scoped and bounded — when the entry has aged out the
// caller falls back to the current syncRev (a conservative watermark: appliedRev
// >= current implies appliedRev >= original).
func (p *EventPublisher) LoadedDetailRev(backendID, sessionID, turnID string) (int, bool) {
	if p == nil {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := projectionJournalKey{backendID: backendID, sessionID: sessionID}
	entries := p.projectionJournal.entries[key]
	for i := len(entries) - 1; i >= 0; i-- {
		for _, op := range entries[i].patch.TurnStateOps {
			if op.TurnID == turnID && op.DetailLoadState == DetailStateLoaded {
				return entries[i].patch.SyncRev, true
			}
		}
	}
	return 0, false
}
