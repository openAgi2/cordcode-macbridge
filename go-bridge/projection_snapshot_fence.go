package gobridge

import (
	"fmt"
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

type projectionFenceKey struct {
	conn                 Connection
	backendID, sessionID string
}

type projectionSnapshotFence struct {
	id           uint64
	generation   uint64
	cutRev       int
	expectedRev  int
	pending      []ProjectionPatch
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
	sink := p.sinkLocked(conn)
	required := 1 + len(fence.pending)
	if fence.invalidated {
		required = 2
	}
	if cap(sink.slots)-len(sink.slots) < required {
		delete(p.projectionFences, key)
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
		isResult:   true,
		resultDone: resultDone,
		delivered:  responseEnqueued,
	}
	if fence.invalidated {
		p.enqueueProjectionInvalidateIntoSinkLocked(sink, admission.BackendID, admission.SessionID)
		p.projectionInvalidated[key] = true
	} else {
		for _, patch := range fence.pending {
			sink.slots <- struct{}{}
			sink.queue <- eventOutboundFrame{
				value:      projectionPatchEvent(p.bridgeEpoch, admission.BackendID, admission.SessionID, patch),
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
			return err
		}
		return nil
	case <-time.After(bridgeWriteTimeout):
		_ = conn.Close()
		return fmt.Errorf("projection snapshot response delivery timeout")
	}
}

func (p *EventPublisher) enqueueProjectionInvalidateLocked(conn Connection, backendID, sessionID string) {
	sink := p.sinkLocked(conn)
	if sink == nil {
		return
	}
	if !sink.tryEnqueue(eventOutboundFrame{
		value:      projectionInvalidateEvent(p.bridgeEpoch, backendID, sessionID),
		classHint:  relayOutboundControl,
		classified: true,
	}) {
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
