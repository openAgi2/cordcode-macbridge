package gobridge

import (
	"fmt"
	"log/slog"
	"time"
)

// ProjectionSnapshotAdmission identifies one pull fence on one authenticated logical
// connection. connectionGeneration is process-local and never reused; bridgeEpoch changes only
// across a MacBridge process restart.
type ProjectionSnapshotAdmission struct {
	BridgeEpoch          string
	ConnectionGeneration uint64
	BackendID            string
	SessionID            string
	CutRev               int
	fenceID              uint64
}

type ProjectionResumeSelection struct {
	Patches        []ProjectionPatch
	Available      bool
	FallbackReason ProjectionResumeFallbackReason
	EpochChanged   bool
}

type projectionFenceKey struct {
	conn                 Connection
	backendID, sessionID string
}

// projectionFencePendingPatch is one patch queued behind a fence's response.
// decide marks a turn_detail rule-4 deferral: the connection was mid-window-
// pull when the detail commit landed, so content-vs-no-op routing is resolved
// at fence completion once the response's held turns are registered (audit
// P0-2 — deciding no-op up front would strand the detail behind the stale
// response permanently).
type projectionFencePendingPatch struct {
	patch  ProjectionPatch
	decide bool
}

type projectionSnapshotFence struct {
	id           uint64
	generation   uint64
	cutRev       int
	expectedRev  int
	pending      []projectionFencePendingPatch
	pendingBytes int
	invalidated  bool
}

// BeginProjectionSnapshot takes the immutable Kernel head and installs the subscriber fence under
// the same EventPublisher lock used by live ingest and patch delivery. A commit can therefore be
// entirely before the cut or entirely after it, never in an unowned interval.
func (p *EventPublisher) BeginProjectionSnapshot(
	conn Connection,
	backendID, sessionID string,
) (SessionProjection, ProjectionSnapshotAdmission, error) {
	if p == nil || conn == nil || backendID == "" || sessionID == "" {
		return SessionProjection{}, ProjectionSnapshotAdmission{}, fmt.Errorf("projection snapshot identity is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.beginProjectionSnapshotLocked(conn, backendID, sessionID)
}

// BeginProjectionSnapshotWithResume selects a journal range and installs the snapshot fence under
// one EventPublisher lock. The returned range is therefore bounded by the same immutable cutRev as
// the full projection snapshot; post-cut live patches remain queued behind the result.
func (p *EventPublisher) BeginProjectionSnapshotWithResume(
	conn Connection,
	backendID, sessionID string,
	sinceRev int,
) (SessionProjection, ProjectionSnapshotAdmission, ProjectionResumeSelection, error) {
	if p == nil || conn == nil || backendID == "" || sessionID == "" {
		return SessionProjection{}, ProjectionSnapshotAdmission{}, ProjectionResumeSelection{}, fmt.Errorf("projection snapshot identity is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	projection, admission, err := p.beginProjectionSnapshotLocked(conn, backendID, sessionID)
	if err != nil {
		return SessionProjection{}, ProjectionSnapshotAdmission{}, ProjectionResumeSelection{}, err
	}
	selection := ProjectionResumeSelection{EpochChanged: p.projectionEpochMismatch[conn]}
	if selection.EpochChanged {
		return projection, admission, selection, nil
	}
	patches, ok, reason := p.projectionJournal.ContiguousRangeAt(
		backendID, sessionID, sinceRev, admission.CutRev, p.now(),
	)
	selection.Patches = patches
	selection.Available = ok
	selection.FallbackReason = reason
	return projection, admission, selection, nil
}

// beginProjectionSnapshotLocked requires EventPublisher.mu. Callers use it to keep journal range
// selection and fence admission atomic without introducing a second journal lock.
func (p *EventPublisher) beginProjectionSnapshotLocked(
	conn Connection,
	backendID, sessionID string,
) (SessionProjection, ProjectionSnapshotAdmission, error) {
	if p.kernel == nil {
		return SessionProjection{}, ProjectionSnapshotAdmission{}, fmt.Errorf("projection kernel is not configured")
	}
	key := projectionFenceKey{conn: conn, backendID: backendID, sessionID: sessionID}
	if _, exists := p.projectionFences[key]; exists {
		return SessionProjection{}, ProjectionSnapshotAdmission{}, fmt.Errorf("projection snapshot already pending")
	}
	projection, ok := p.kernel.Snapshot(backendID, sessionID)
	if !ok {
		return SessionProjection{}, ProjectionSnapshotAdmission{}, fmt.Errorf("projection did not reach a committed ready state")
	}
	generation := p.registerConnectionLocked(conn)
	p.sinkLocked(conn)
	p.nextProjectionFenceID++
	fenceID := p.nextProjectionFenceID
	p.projectionFences[key] = &projectionSnapshotFence{
		id:          fenceID,
		generation:  generation,
		cutRev:      projection.SyncRev,
		expectedRev: projection.SyncRev,
	}
	delete(p.projectionInvalidated, key)
	slog.Info("go-bridge: [K4Patch] fence_begin",
		"sessionPrefix", projectionSessionLogPrefix(sessionID),
		"cutRev", projection.SyncRev, "generation", generation,
	)
	return projection, ProjectionSnapshotAdmission{
		BridgeEpoch:          p.bridgeEpoch,
		ConnectionGeneration: generation,
		BackendID:            backendID,
		SessionID:            sessionID,
		CutRev:               projection.SyncRev,
		fenceID:              fenceID,
	}, nil
}

// CompleteProjectionSnapshot enqueues the RPC result first and then releases every post-cut patch
// into that connection's same ordered sink. Queue overflow is represented by one sync_invalidate
// after the result. If the connection was replaced or cannot accept the atomic batch, it is closed
// so the client request reaches an explicit disconnect terminal state.
func (p *EventPublisher) CompleteProjectionSnapshot(
	conn Connection,
	admission ProjectionSnapshotAdmission,
	requestID string,
	data interface{},
) error {
	return p.completeProjectionSnapshot(conn, admission, requestID, data, nil, nil)
}

// CompleteProjectionSnapshotWithHeldTurns completes a projection-window fence
// AND registers the response's turn ids as the connection's held-turn set in
// the SAME transaction (audit P0-2): a concurrent detail commit on another
// connection that lands between the response and a post-hoc registration would
// route this connection a no-op revision patch — stale response + no-op =
// permanently missed detail. Registering before the fence releases means every
// later routing decision (and every rule-4 deferral resolved below) sees the
// turns this response actually delivered. Any failure after registration rolls
// the set back.
func (p *EventPublisher) CompleteProjectionSnapshotWithHeldTurns(
	conn Connection,
	admission ProjectionSnapshotAdmission,
	requestID string,
	data interface{},
	heldTurnIDs []string,
) error {
	return p.completeProjectionSnapshot(conn, admission, requestID, data, heldTurnIDs, nil)
}

// CompleteProjectionSnapshotError releases a fence whose pull failed after admission (e.g. a
// projection-window typed error) with a WireError result instead of a success payload, so a
// failed pull never leaves the session's snapshot fence dangling for the next request.
func (p *EventPublisher) CompleteProjectionSnapshotError(
	conn Connection,
	admission ProjectionSnapshotAdmission,
	requestID string,
	resultErr *WireError,
) error {
	return p.completeProjectionSnapshot(conn, admission, requestID, nil, nil, resultErr)
}

func (p *EventPublisher) completeProjectionSnapshot(
	conn Connection,
	admission ProjectionSnapshotAdmission,
	requestID string,
	data interface{},
	heldTurnIDs []string,
	resultErr *WireError,
) error {
	if p == nil || conn == nil || requestID == "" {
		return fmt.Errorf("projection snapshot completion identity is required")
	}
	p.mu.Lock()
	key := projectionFenceKey{
		conn:      conn,
		backendID: admission.BackendID,
		sessionID: admission.SessionID,
	}
	fence := p.projectionFences[key]
	generation := p.connectionGenerations[conn]
	if fence == nil || fence.id != admission.fenceID || fence.generation != admission.ConnectionGeneration ||
		generation != admission.ConnectionGeneration || admission.BridgeEpoch != p.bridgeEpoch {
		p.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("projection snapshot connection generation changed")
	}
	// P0-2 held-turn registration INSIDE the completion transaction, before the
	// fence releases: from here on, every detail/state routing decision sees the
	// turns this response delivers. It also feeds the rule-4 deferral decisions
	// just below. Failed completions roll the registration back.
	if len(heldTurnIDs) > 0 && !fence.invalidated {
		p.recordConnWindowTurnsLocked(conn, admission.BackendID, admission.SessionID, heldTurnIDs)
	}
	if fence.invalidated {
		// Deferred decisions and the registration are moot: the client re-syncs
		// from the authoritative snapshot, which already carries the detail
		// truth, and its next window pull re-registers fresh held turns.
		fence.pending = nil
	}
	for i, frame := range fence.pending {
		if !frame.decide {
			continue
		}
		if p.connHoldsAnyTurnLocked(conn, admission.BackendID, admission.SessionID, patchTurnIDs(frame.patch)) {
			continue // held: the queued full patch is correct as-is
		}
		resolved := frame.patch
		resolved.UpsertTurns = nil
		resolved.PartOps = nil
		resolved.TurnStateOps = nil
		resolved.Execution = nil
		fence.pending[i].patch = resolved
	}
	sink := p.sinkLocked(conn)
	required := 1 + len(fence.pending)
	slog.Info("go-bridge: [K4Patch] fence_complete",
		"sessionPrefix", projectionSessionLogPrefix(admission.SessionID),
		"pending", len(fence.pending), "invalidated", fence.invalidated,
		"expectedRev", fence.expectedRev,
	)
	if fence.invalidated {
		required = 2
	}
	if cap(sink.slots)-len(sink.slots) < required {
		delete(p.projectionFences, key)
		p.clearConnWindowTurnsLocked(conn, admission.BackendID, admission.SessionID)
		p.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("outbound queue cannot accept projection snapshot batch")
	}
	responseEnqueued := make(chan struct{})
	resultDone := make(chan error, 1)
	sink.slots <- struct{}{}
	sink.queue <- eventOutboundFrame{
		requestID:  requestID,
		resultData: data,
		resultErr:  resultErr,
		isResult:   true,
		resultDone: resultDone,
		delivered:  responseEnqueued,
	}
	if fence.invalidated {
		p.enqueueProjectionInvalidateIntoSinkLocked(sink, admission.BackendID, admission.SessionID)
		p.projectionInvalidated[key] = true
	} else {
		for _, frame := range fence.pending {
			sink.slots <- struct{}{}
			sink.queue <- eventOutboundFrame{
				value:      projectionPatchEvent(p.bridgeEpoch, admission.BackendID, admission.SessionID, frame.patch),
				classHint:  classifyRelayEvent("projection_patch"),
				classified: true,
			}
		}
		p.projectionSnapshotCuts[key] = fence.expectedRev
		delete(p.projectionInvalidated, key)
	}
	delete(p.projectionFences, key)
	p.mu.Unlock()
	select {
	case <-responseEnqueued:
		if err := <-resultDone; err != nil {
			p.clearConnWindowTurnsLocked(conn, admission.BackendID, admission.SessionID)
			return err
		}
		return nil
	case <-time.After(bridgeWriteTimeout):
		slog.Info("event-publisher: projection snapshot delivery timeout closing conn",
			"remote", conn.RemoteAddr(),
			"generation", p.connectionGenerations[conn],
		)
		p.clearConnWindowTurnsLocked(conn, admission.BackendID, admission.SessionID)
		_ = conn.Close()
		return fmt.Errorf("projection snapshot response delivery timeout")
	}
}

func (p *EventPublisher) enqueueProjectionInvalidateLocked(conn Connection, backendID, sessionID string) {
	// turn_detail_lazy_v1 rule 4 bookkeeping: an invalidated connection
	// re-pulls from scratch, so its held-turn set is stale — forget it now;
	// it re-records from its own window/snapshot pulls.
	if per := p.projectionHeldTurns[conn]; per != nil {
		delete(per, projectionDeliveryKey(backendID, sessionID))
	}
	sink := p.sinkLocked(conn)
	if sink == nil {
		return
	}
	if ok, reason, queued := sink.tryEnqueue(eventOutboundFrame{
		value:      projectionInvalidateEvent(p.bridgeEpoch, backendID, sessionID),
		classHint:  relayOutboundControl,
		classified: true,
	}); !ok {
		slog.Warn("event-publisher: projection invalidate enqueue failed",
			"backendID", backendID,
			"sessionID", sessionID,
			"remote", conn.RemoteAddr(),
			"tryEnqueue", reason, "queued", queued,
		)
		return
	}
}

func (p *EventPublisher) enqueueProjectionInvalidateIntoSinkLocked(
	sink *eventOutboundSink,
	backendID, sessionID string,
) {
	sink.slots <- struct{}{}
	sink.queue <- eventOutboundFrame{
		value:      projectionInvalidateEvent(p.bridgeEpoch, backendID, sessionID),
		classHint:  relayOutboundControl,
		classified: true,
	}
}

// ConnectionGeneration is intentionally test/diagnostic-only; it is not a projection revision
// and does not reuse Relay mailbox or secure-channel generation values.
func (p *EventPublisher) ConnectionGeneration(conn Connection) (uint64, bool) {
	if p == nil || conn == nil {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	generation, ok := p.connectionGenerations[conn]
	return generation, ok
}
