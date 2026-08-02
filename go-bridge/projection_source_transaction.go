package gobridge

import (
	"errors"
	"fmt"
)

type ClaudeSourceBatchStatus string

const (
	ClaudeSourceBatchAcceptedProjection ClaudeSourceBatchStatus = "accepted_projection"
	ClaudeSourceBatchAcceptedSourceOnly ClaudeSourceBatchStatus = "accepted_source_only"
	ClaudeSourceBatchAlreadyApplied     ClaudeSourceBatchStatus = "already_applied"
	ClaudeSourceBatchRejected           ClaudeSourceBatchStatus = "rejected"
)

type ClaudeSourceRecordBatch struct {
	BackendID   string
	SessionID   string
	BridgeEpoch string
	Record      ClaudeSourceRecordTransition
	Events      []projectionHydrateEvent
}

type ClaudeSourceBatchResult struct {
	Status                     ClaudeSourceBatchStatus
	SourceStateRev             uint64
	PhysicalRowsAcknowledged   int
	LogicalRecordsChanged      int
	ProjectionSubeventsApplied int
	PublicSubeventsDelivered   int
	ProjectionTurnID           string
	ProjectionPartID           string
}

// claudeSourceBatchFaultHook is test-only fault injection. Returning an error before event index
// k proves the authoritative reducer/source tuple still exposes the old batch.
var claudeSourceBatchFaultHook func(eventIndex int) error

func (k *ProjectionKernel) InstallClaudeSourceState(
	backendID, sessionID string,
	state ClaudeSourceState,
) error {
	if k == nil || backendID == "" || sessionID == "" {
		return fmt.Errorf("%w: invalid Claude source-state install", ErrProjectionCheckpointInvalid)
	}
	if err := ValidateClaudeSourceState(state); err != nil {
		return err
	}
	cloned, err := cloneClaudeSourceState(state)
	if err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	session.claudeSourceState = &cloned
	return nil
}

func (k *ProjectionKernel) ClaudeSourceStateSnapshot(
	backendID, sessionID string,
) (ClaudeSourceState, bool) {
	if k == nil {
		return ClaudeSourceState{}, false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.claudeSourceState == nil {
		return ClaudeSourceState{}, false
	}
	cloned, err := cloneClaudeSourceState(*session.claudeSourceState)
	return cloned, err == nil
}

// ApplyClaudeSourceRecordBatch is the private producer→Kernel ingest boundary. Decode/map stays
// pure before this call; under the Kernel lock it validates physical generation/range, proposes
// the logical ledger transition, reduces the whole row on a transaction-local reducer clone, and
// swaps source state + projection only after every subevent succeeds. Private source identity is
// never copied into EventMessage or a public delivery queue.
func (k *ProjectionKernel) ApplyClaudeSourceRecordBatch(
	batch ClaudeSourceRecordBatch,
) (ClaudeSourceBatchResult, error) {
	rejected := ClaudeSourceBatchResult{Status: ClaudeSourceBatchRejected}
	if k == nil || batch.BackendID == "" || batch.SessionID == "" {
		return rejected, fmt.Errorf("%w: invalid Claude source batch", ErrProjectionCheckpointInvalid)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(batch.BackendID, batch.SessionID)
	if session.claudeSourceState == nil {
		return rejected, fmt.Errorf("%w: Claude source state is not installed", ErrProjectionCheckpointInvalid)
	}
	current := *session.claudeSourceState
	if batch.Record.RawByteStart < 0 || batch.Record.RawByteEnd <= batch.Record.RawByteStart {
		return rejected, fmt.Errorf("%w: invalid Claude source batch range", ErrProjectionCheckpointInvalid)
	}
	cursor, found := claudeSourceCursorForSegment(current, batch.Record.SegmentStableKey)
	if !found || cursor.SegmentGeneration != batch.Record.SegmentGeneration {
		return rejected, fmt.Errorf("%w: Claude source batch generation mismatch", ErrProjectionCheckpointInvalid)
	}
	if batch.Record.RawByteEnd <= cursor.RawByteEnd {
		if batch.Record.RawByteEnd != cursor.RawByteEnd &&
			!claudeSourceOccurrenceExists(current, batch.Record) {
			return rejected, fmt.Errorf("%w: unproven historical Claude source batch", ErrProjectionCheckpointInvalid)
		}
		result := ClaudeSourceBatchResult{
			Status: ClaudeSourceBatchAlreadyApplied, SourceStateRev: current.SourceStateRev,
		}
		emitClaudeKernelSourceTransition(batch, result)
		return result, nil
	}
	if batch.Record.RawByteStart != cursor.RawByteEnd {
		return rejected, fmt.Errorf("%w: Claude source batch gap", ErrProjectionCheckpointInvalid)
	}
	proposal, err := ProposeClaudeSourceTransition(current, batch.Record)
	if err != nil {
		return rejected, err
	}
	result := ClaudeSourceBatchResult{
		SourceStateRev:           proposal.Next.SourceStateRev,
		PhysicalRowsAcknowledged: 1,
	}
	if proposal.Result == ClaudeTransitionAcceptedSourceOnly {
		result.Status = ClaudeSourceBatchAcceptedSourceOnly
		if err := CommitClaudeSourceTransition(session.claudeSourceState, proposal); err != nil {
			return rejected, err
		}
		emitClaudeKernelSourceTransition(batch, result)
		return result, nil
	}
	if len(batch.Events) == 0 {
		return rejected, errors.New("Claude projection transition has no projection subevents")
	}
	events := batch.Events
	if newCount := len(proposal.NewBlockOccurrenceIDs); newCount < len(batch.Record.ContentBlocks) {
		firstNewOrdinal := len(batch.Record.ContentBlocks) - newCount
		filtered := make([]projectionHydrateEvent, 0, len(events))
		for _, event := range events {
			if event.SourceBlockOrdinal == nil || *event.SourceBlockOrdinal >= firstNewOrdinal {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	}

	transactionReducer := k.reducer.cloneSessionReducer(batch.BackendID, batch.SessionID)
	nextSeq := transactionReducer.lastInputSequence(batch.BackendID, batch.SessionID)
	applied := 0
	for index, event := range events {
		if claudeSourceBatchFaultHook != nil {
			if err := claudeSourceBatchFaultHook(index); err != nil {
				return rejected, err
			}
		}
		nextSeq++
		before := transactionReducer.LastAppliedRev(batch.BackendID, batch.SessionID)
		transactionReducer.Apply(projectionReducerEvent(
			batch.BackendID,
			batch.SessionID,
			event.Event,
			cloneProjectionJSONValue(event.Data),
			nextSeq,
			batch.BridgeEpoch,
		))
		if transactionReducer.LastAppliedRev(batch.BackendID, batch.SessionID) != before {
			applied++
		}
	}
	if applied == 0 {
		if err := CommitClaudeSourceTransition(session.claudeSourceState, proposal); err != nil {
			return rejected, err
		}
		result.Status = ClaudeSourceBatchAcceptedSourceOnly
		result.LogicalRecordsChanged = 1
		emitClaudeKernelSourceTransition(batch, result)
		return result, nil
	}
	if claudeSourceBatchFaultHook != nil {
		if err := claudeSourceBatchFaultHook(len(events)); err != nil {
			return rejected, err
		}
	}
	nextState, err := cloneClaudeSourceState(proposal.Next)
	if err != nil {
		return rejected, err
	}
	k.reducer.swapSessionFrom(batch.BackendID, batch.SessionID, transactionReducer)
	session.claudeSourceState = &nextState
	result.Status = ClaudeSourceBatchAcceptedProjection
	result.LogicalRecordsChanged = 1
	result.ProjectionSubeventsApplied = applied
	result.ProjectionTurnID, result.ProjectionPartID = claudeProjectionTraceIdentity(events)
	emitClaudeKernelSourceTransition(batch, result)
	return result, nil
}

func claudeSourceOccurrenceExists(
	state ClaudeSourceState,
	record ClaudeSourceRecordTransition,
) bool {
	if record.LogicalRecordUUID == "" {
		return false
	}
	for _, occurrence := range state.GraphNodes[record.LogicalRecordUUID] {
		if occurrence.SegmentStableKey == record.SegmentStableKey &&
			occurrence.SourceGeneration == record.SourceGeneration &&
			occurrence.RawByteStart == record.RawByteStart &&
			occurrence.RawByteEnd == record.RawByteEnd {
			return true
		}
	}
	return false
}

func emitClaudeKernelSourceTransition(
	batch ClaudeSourceRecordBatch,
	result ClaudeSourceBatchResult,
) {
	emitClaudeSourceTrace(claudeSourceTraceRecord{
		Phase: "kernel", IngestDomain: "source_batch",
		BackendID: batch.BackendID, SessionID: batch.SessionID,
		Correlation: claudeSourceCorrelation{
			SegmentStableKey:  batch.Record.SegmentStableKey,
			SegmentGeneration: batch.Record.SegmentGeneration,
		},
		Record: claudeRelayScannedRecord{
			Entry: claudeTranscriptRelayEntry{
				Type:       batch.Record.StructuralKind,
				UUID:       batch.Record.LogicalRecordUUID,
				ParentUUID: batch.Record.ParentUUID,
			},
			ByteStart: batch.Record.RawByteStart,
			ByteEnd:   batch.Record.RawByteEnd,
			Admitted:  result.Status != ClaudeSourceBatchRejected,
		},
		FileOrderTurnID:   batch.Record.GraphResolvedTurn,
		ProjectionEvents:  result.ProjectionSubeventsApplied,
		Transition:        string(result.Status),
		SourceStateRev:    result.SourceStateRev,
		ProjectionTurnID:  result.ProjectionTurnID,
		ProjectionPartID:  result.ProjectionPartID,
		PhysicalRowsAcked: result.PhysicalRowsAcknowledged,
		LogicalChanged:    result.LogicalRecordsChanged,
		PublicDelivered:   result.PublicSubeventsDelivered,
	})
}

func claudeSourceCursorForSegment(
	state ClaudeSourceState,
	segmentStableKey string,
) (ClaudeSourceCursor, bool) {
	for _, cursor := range state.CursorVector {
		if cursor.SegmentStableKey == segmentStableKey {
			return cursor, true
		}
	}
	return ClaudeSourceCursor{}, false
}

// AcknowledgeClaudeSourceRange advances the segment cursor past a source-only (non-projected)
// physical row — non-admitted control rows (meta / resume markers / "No response requested") or
// admitted rows the mapper does not own (compaction boundaries). It preserves ledger cursor
// continuity so the next admitted content row's byte range does not gap the fence (guardrail #6),
// without projecting anything or recording a logical record (guardrail #1: source identity stays
// Mac-private). Idempotent: a byteEnd already covered is a no-op.
func (k *ProjectionKernel) AcknowledgeClaudeSourceRange(
	backendID, sessionID, segmentStableKey, segmentGeneration string,
	byteEnd int64,
) error {
	if k == nil || backendID == "" || sessionID == "" || segmentStableKey == "" || byteEnd < 0 {
		return fmt.Errorf("%w: invalid Claude source acknowledge input", ErrProjectionCheckpointInvalid)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.claudeSourceState == nil {
		return fmt.Errorf("%w: Claude source state is not installed", ErrProjectionCheckpointInvalid)
	}
	cursor, found := claudeSourceCursorForSegment(*session.claudeSourceState, segmentStableKey)
	if !found || cursor.SegmentGeneration != segmentGeneration {
		return fmt.Errorf("%w: Claude source acknowledge generation mismatch", ErrProjectionCheckpointInvalid)
	}
	if byteEnd <= cursor.RawByteEnd {
		return nil
	}
	next, err := cloneClaudeSourceState(*session.claudeSourceState)
	if err != nil {
		return err
	}
	for index := range next.CursorVector {
		if next.CursorVector[index].SegmentStableKey == segmentStableKey {
			next.CursorVector[index].RawByteEnd = byteEnd
			next.SourceStateRev++
			*session.claudeSourceState = next
			break
		}
	}
	return nil
}

// PrepareClaudeCheckpointCandidate snapshots projection and Claude source state while holding the
// Kernel commit lock. The returned immutable tuple may be persisted asynchronously; a crash may
// retain the previous tuple, but cannot persist a new projection with an old source ledger.
func (k *ProjectionKernel) PrepareClaudeCheckpointCandidate(
	checkpoint ProjectionCheckpoint,
) (ProjectionCheckpoint, error) {
	if k == nil || checkpoint.BackendID == "" || checkpoint.SessionID == "" {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: invalid Claude checkpoint candidate", ErrProjectionCheckpointInvalid)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(checkpoint.BackendID, checkpoint.SessionID)
	if session.claudeSourceState == nil {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: Claude source state missing", ErrProjectionCheckpointInvalid)
	}
	projection, ok := k.reducer.Snapshot(checkpoint.BackendID, checkpoint.SessionID)
	if !ok {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: Claude projection missing", ErrProjectionCheckpointInvalid)
	}
	state, err := cloneClaudeSourceState(*session.claudeSourceState)
	if err != nil {
		return ProjectionCheckpoint{}, err
	}
	checkpoint.Projection = projection
	checkpoint.ProjectionRev = projection.SyncRev
	checkpoint.ClaudeSourceState = &state
	checkpoint.HydrateState = ProjectionHydrateReady
	if checkpoint.SchemaVersion == 0 {
		checkpoint.SchemaVersion = projectionCheckpointSchemaVersion
	}
	if checkpoint.UpdatedAt.IsZero() {
		checkpoint.UpdatedAt = k.now()
	}
	return checkpoint, nil
}
