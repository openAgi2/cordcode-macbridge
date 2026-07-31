package gobridge

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func projectionSourceBatch(record ClaudeSourceRecordTransition) ClaudeSourceRecordBatch {
	return ClaudeSourceRecordBatch{
		BackendID: "claude", SessionID: "session-1", BridgeEpoch: "epoch-1",
		Record: record,
		Events: []projectionHydrateEvent{
			{Event: "user_message", Data: map[string]interface{}{
				"turnId": "user-1", "itemId": "user-1", "text": "prompt",
			}},
			{Event: "text_delta", Data: map[string]interface{}{
				"turnId": "user-1", "itemId": "user-1", "delta": "answer",
			}},
		},
	}
}

func TestProjectionKernelClaudeCheckpointTupleRestoresProjectionAndCursorTogether(t *testing.T) {
	root := t.TempDir()
	sourcePath := root + "/source.jsonl"
	if err := os.WriteFile(sourcePath, []byte(strings.Repeat("x", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	source := ProjectionSourceDescriptor{Identity: "session-1", Path: sourcePath, Cursor: 20}
	sourceCheckpoint, err := BuildProjectionSourceCheckpoint(source)
	if err != nil {
		t.Fatal(err)
	}
	store := NewProjectionCheckpointStore(root)
	kernel := NewProjectionKernel(NewProjectionReducer(), store)
	if err := kernel.InstallClaudeSourceState("claude", "session-1", emptyTransitionState()); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.ApplyClaudeSourceRecordBatch(
		projectionSourceBatch(sourceRecord(0, 10, `{"type":"text","text":"answer"}`)),
	); err != nil {
		t.Fatal(err)
	}
	replay := projectionSourceBatch(sourceRecord(10, 20, `{"text":"answer","type":"text"}`))
	if _, err := kernel.ApplyClaudeSourceRecordBatch(replay); err != nil {
		t.Fatal(err)
	}
	projection, _ := kernel.reducer.Snapshot("claude", "session-1")
	base := NewReadyProjectionCheckpoint(
		"claude", "session-1", sourceCheckpoint, projection, time.Now(),
	)
	candidate, err := kernel.PrepareClaudeCheckpointCandidate(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(candidate); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(strings.Repeat("y", 10)); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	advancedSource := source
	advancedSource.Cursor = 30

	restoredReducer := NewProjectionReducer()
	restoredKernel := NewProjectionKernel(restoredReducer, store)
	loaded, err := restoredKernel.RestoreCheckpoint("claude", "session-1", advancedSource)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := restoredKernel.ClaudeSourceStateSnapshot("claude", "session-1")
	if !ok || state.SourceStateRev != 2 || state.CursorVector[0].RawByteEnd != 20 {
		t.Fatalf("restored source state = %+v ok=%v", state, ok)
	}
	restoredProjection, ok := restoredKernel.Snapshot("claude", "session-1")
	if !ok || !reflect.DeepEqual(restoredProjection, loaded.Projection) ||
		restoredProjection.SyncRev != projection.SyncRev {
		t.Fatalf("restored projection = %+v loaded=%+v", restoredProjection, loaded.Projection)
	}
	if replayed, err := restoredKernel.ApplyClaudeSourceRecordBatch(
		projectionSourceBatch(sourceRecord(0, 10, `{"type":"text","text":"answer"}`)),
	); err != nil || replayed.Status != ClaudeSourceBatchAlreadyApplied {
		t.Fatalf("post-restart historical replay = %+v err=%v", replayed, err)
	}
	extensionRecord := sourceRecord(
		20, 30,
		`{"type":"text","text":"answer"}`,
		`{"type":"text","text":" more"}`,
	)
	extension := ClaudeSourceRecordBatch{
		BackendID: "claude", SessionID: "session-1", BridgeEpoch: "epoch-1",
		Record: extensionRecord,
		Events: []projectionHydrateEvent{{
			Event: "text_delta",
			Data:  map[string]interface{}{"turnId": "user-1", "itemId": "user-1", "delta": " more"},
		}},
	}
	if result, err := restoredKernel.ApplyClaudeSourceRecordBatch(extension); err != nil ||
		result.Status != ClaudeSourceBatchAcceptedProjection {
		t.Fatalf("post-restart extension = %+v err=%v", result, err)
	}
	advancedProjection, _ := restoredReducer.Snapshot("claude", "session-1")
	if len(advancedProjection.Turns) != 1 ||
		advancedProjection.Turns[0].Assistant == nil ||
		len(advancedProjection.Turns[0].Assistant.Parts) != 1 ||
		advancedProjection.Turns[0].Assistant.Parts[0].Text != "answer more" {
		t.Fatalf("post-restart projection repeated or lost content: %+v", advancedProjection)
	}
	advancedState, _ := restoredKernel.ClaudeSourceStateSnapshot("claude", "session-1")
	if advancedState.SourceStateRev != 3 || advancedState.CursorVector[0].RawByteEnd != 30 {
		t.Fatalf("post-restart source vector = %+v", advancedState)
	}
}

func TestProjectionKernelClaudeSourceBatchRollbackAndSingleSwap(t *testing.T) {
	reducer := NewProjectionReducer()
	kernel := NewProjectionKernel(reducer, nil)
	initial := emptyTransitionState()
	if err := kernel.InstallClaudeSourceState("claude", "session-1", initial); err != nil {
		t.Fatal(err)
	}
	batch := projectionSourceBatch(sourceRecord(0, 10, `{"type":"text","text":"answer"}`))
	claudeSourceBatchFaultHook = func(index int) error {
		if index == 1 {
			return errors.New("injected reduce failure")
		}
		return nil
	}
	t.Cleanup(func() { claudeSourceBatchFaultHook = nil })
	if result, err := kernel.ApplyClaudeSourceRecordBatch(batch); err == nil ||
		result.Status != ClaudeSourceBatchRejected {
		t.Fatalf("fault result=%+v err=%v", result, err)
	}
	state, ok := kernel.ClaudeSourceStateSnapshot("claude", "session-1")
	if !ok || !reflect.DeepEqual(state, initial) {
		t.Fatalf("source state mutated after failed batch: %+v", state)
	}
	if projection, exists := reducer.Snapshot("claude", "session-1"); exists {
		t.Fatalf("projection became visible after failed batch: %+v", projection)
	}

	claudeSourceBatchFaultHook = nil
	result, err := kernel.ApplyClaudeSourceRecordBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ClaudeSourceBatchAcceptedProjection ||
		result.PhysicalRowsAcknowledged != 1 ||
		result.LogicalRecordsChanged != 1 ||
		result.ProjectionSubeventsApplied != 2 ||
		result.PublicSubeventsDelivered != 0 {
		t.Fatalf("accepted result = %+v", result)
	}
	projection, exists := reducer.Snapshot("claude", "session-1")
	if !exists || projection.SyncRev != 2 || len(projection.Turns) != 1 {
		t.Fatalf("projection after swap = %+v exists=%v", projection, exists)
	}
	state, _ = kernel.ClaudeSourceStateSnapshot("claude", "session-1")
	if state.SourceStateRev != 1 || state.CursorVector[0].RawByteEnd != 10 {
		t.Fatalf("source state after swap = %+v", state)
	}
}

func TestProjectionKernelClaudeSourceBatchGatesReplayAndCheckpointTuple(t *testing.T) {
	reducer := NewProjectionReducer()
	kernel := NewProjectionKernel(reducer, nil)
	if err := kernel.InstallClaudeSourceState("claude", "session-1", emptyTransitionState()); err != nil {
		t.Fatal(err)
	}
	firstBatch := projectionSourceBatch(sourceRecord(0, 10, `{"type":"text","text":"answer"}`))
	first, err := kernel.ApplyClaudeSourceRecordBatch(firstBatch)
	if err != nil {
		t.Fatal(err)
	}
	replayedRange, err := kernel.ApplyClaudeSourceRecordBatch(firstBatch)
	if err != nil || replayedRange.Status != ClaudeSourceBatchAlreadyApplied ||
		replayedRange.PhysicalRowsAcknowledged != 0 {
		t.Fatalf("same range replay result=%+v err=%v", replayedRange, err)
	}
	logicalReplay := projectionSourceBatch(sourceRecord(10, 20, `{"text":"answer","type":"text"}`))
	logicalReplay.Record.ParentUUID = "other-parent"
	sourceOnly, err := kernel.ApplyClaudeSourceRecordBatch(logicalReplay)
	if err != nil || sourceOnly.Status != ClaudeSourceBatchAcceptedSourceOnly ||
		sourceOnly.PhysicalRowsAcknowledged != 1 ||
		sourceOnly.ProjectionSubeventsApplied != 0 {
		t.Fatalf("logical replay result=%+v err=%v", sourceOnly, err)
	}
	projection, _ := reducer.Snapshot("claude", "session-1")
	if projection.SyncRev != first.ProjectionSubeventsApplied {
		t.Fatalf("source-only replay changed projection rev: %+v", projection)
	}
	checkpoint, err := kernel.PrepareClaudeCheckpointCandidate(ProjectionCheckpoint{
		BackendID: "claude", SessionID: "session-1", UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.ClaudeSourceState == nil ||
		checkpoint.ClaudeSourceState.SourceStateRev != sourceOnly.SourceStateRev ||
		checkpoint.ClaudeSourceState.CursorVector[0].RawByteEnd != 20 ||
		checkpoint.ProjectionRev != projection.SyncRev ||
		!reflect.DeepEqual(checkpoint.Projection, projection) {
		t.Fatalf("checkpoint tuple is inconsistent: %+v", checkpoint)
	}
}

func TestProjectionKernelClaudeSourceBatchRejectsGapAndGenerationWithoutMutation(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	initial := emptyTransitionState()
	if err := kernel.InstallClaudeSourceState("claude", "session-1", initial); err != nil {
		t.Fatal(err)
	}
	gap := projectionSourceBatch(sourceRecord(5, 10, `{"type":"text","text":"answer"}`))
	if result, err := kernel.ApplyClaudeSourceRecordBatch(gap); err == nil ||
		result.Status != ClaudeSourceBatchRejected {
		t.Fatalf("gap result=%+v err=%v", result, err)
	}
	stale := projectionSourceBatch(sourceRecord(0, 10, `{"type":"text","text":"answer"}`))
	stale.Record.SegmentGeneration = "old-generation"
	if result, err := kernel.ApplyClaudeSourceRecordBatch(stale); err == nil ||
		result.Status != ClaudeSourceBatchRejected {
		t.Fatalf("generation result=%+v err=%v", result, err)
	}
	state, _ := kernel.ClaudeSourceStateSnapshot("claude", "session-1")
	if !reflect.DeepEqual(state, initial) {
		t.Fatalf("rejected batches mutated source state: %+v", state)
	}
}
