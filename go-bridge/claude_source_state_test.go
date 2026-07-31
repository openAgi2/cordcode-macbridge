package gobridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

func validClaudeSourceState() ClaudeSourceState {
	return ClaudeSourceState{
		SchemaVersion:    ClaudeSourceStateSchemaVersion,
		SourceGeneration: "generation-1",
		SourceStateRev:   2,
		CursorVector: []ClaudeSourceCursor{{
			SegmentStableKey:  "segment-0",
			SegmentGeneration: "segment-generation-1",
			RawByteEnd:        42,
			MembershipDigest:  "membership-digest",
		}},
		ParserFlags: ClaudeSourceParserFlags{SkipNextResumeNoResponse: true},
		GraphNodes: map[string][]ClaudeGraphOccurrence{
			"record-1": {{
				SourceStateRev:    1,
				ParentUUID:        "user-1",
				StructuralKind:    "assistant",
				GraphResolvedTurn: "user-1",
				SegmentStableKey:  "segment-0",
				SourceGeneration:  "generation-1",
				RawByteStart:      10,
				RawByteEnd:        42,
			}},
		},
		LogicalRecords: map[string]ClaudeLogicalRecord{
			"record-1": {
				ContentSequenceHash:   "content-hash",
				SemanticLifecycleHash: "lifecycle-hash",
				BlockOccurrenceIDs:    []string{"block-1"},
				Contribution:          ClaudeProjectionContribution{TurnID: "user-1", PartID: "part-1"},
			},
		},
	}
}

func TestClaudeSourceStateCodecRoundTrip(t *testing.T) {
	state := validClaudeSourceState()
	raw, err := EncodeClaudeSourceState(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClaudeSourceState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, state) {
		t.Fatalf("round trip mismatch:\nwant=%+v\ngot=%+v", state, decoded)
	}
}

func TestClaudeSourceStateOldSchemaRequiresInvalidRebuild(t *testing.T) {
	state := validClaudeSourceState()
	state.SchemaVersion = 0
	if _, err := EncodeClaudeSourceState(state); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("old schema error = %v, want ErrProjectionCheckpointInvalid", err)
	}
}

func TestProjectionCheckpointPersistsClaudeSourceState(t *testing.T) {
	root := t.TempDir()
	sourcePath := root + "/session.jsonl"
	data := []byte("{\"type\":\"user\"}\n")
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	source := ProjectionSourceDescriptor{Identity: "session-1", Path: sourcePath, Cursor: int64(len(data))}
	sourceCheckpoint, err := BuildProjectionSourceCheckpoint(source)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := NewReadyProjectionCheckpoint(
		"claude", "session-1", sourceCheckpoint,
		SessionProjection{SessionID: "session-1", SyncRev: 1, Execution: ExecutionView{Phase: "idle"}},
		time.Now(),
	)
	state := validClaudeSourceState()
	checkpoint.ClaudeSourceState = &state
	store := NewProjectionCheckpointStore(root)
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadValidated("claude", "session-1", source)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ClaudeSourceState == nil || !reflect.DeepEqual(*loaded.ClaudeSourceState, state) {
		t.Fatalf("checkpoint source state mismatch: %+v", loaded.ClaudeSourceState)
	}
}

func emptyTransitionState() ClaudeSourceState {
	return ClaudeSourceState{
		SchemaVersion:    ClaudeSourceStateSchemaVersion,
		SourceGeneration: "generation-1",
		CursorVector: []ClaudeSourceCursor{{
			SegmentStableKey:  "segment-0",
			SegmentGeneration: "segment-generation-1",
			MembershipDigest:  "membership",
		}},
		GraphNodes:     map[string][]ClaudeGraphOccurrence{},
		LogicalRecords: map[string]ClaudeLogicalRecord{},
	}
}

func sourceRecord(start, end int64, blocks ...string) ClaudeSourceRecordTransition {
	rawBlocks := make([]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		rawBlocks = append(rawBlocks, json.RawMessage(block))
	}
	return ClaudeSourceRecordTransition{
		LogicalRecordUUID: "assistant-1",
		ParentUUID:        "user-1",
		StructuralKind:    "assistant",
		GraphResolvedTurn: "user-1",
		SegmentStableKey:  "segment-0",
		SegmentGeneration: "segment-generation-1",
		SourceGeneration:  "generation-1",
		RawByteStart:      start,
		RawByteEnd:        end,
		ContentBlocks:     rawBlocks,
		SemanticLifecycle: json.RawMessage(`{"role":"assistant","stop_reason":null}`),
		Contribution:      ClaudeProjectionContribution{TurnID: "user-1", PartID: "assistant-1"},
	}
}

func TestClaudeSourceTransitionExactReplayIsSourceOnly(t *testing.T) {
	state := emptyTransitionState()
	first, err := ProposeClaudeSourceTransition(state, sourceRecord(0, 10, `{"type":"text","text":"answer"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Result != ClaudeTransitionAcceptedProjection {
		t.Fatalf("first result = %q", first.Result)
	}
	if err := CommitClaudeSourceTransition(&state, first); err != nil {
		t.Fatal(err)
	}
	replay := sourceRecord(10, 20, `{"text":"answer","type":"text"}`)
	replay.ParentUUID = "other-parent"
	second, err := ProposeClaudeSourceTransition(state, replay)
	if err != nil {
		t.Fatal(err)
	}
	if second.Result != ClaudeTransitionAcceptedSourceOnly || len(second.NewBlockOccurrenceIDs) != 0 {
		t.Fatalf("replay proposal = %+v", second)
	}
	if err := CommitClaudeSourceTransition(&state, second); err != nil {
		t.Fatal(err)
	}
	if len(state.GraphNodes["assistant-1"]) != 2 || state.SourceStateRev != 2 {
		t.Fatalf("source-only replay did not acknowledge graph occurrence: %+v", state)
	}
	if got := state.LogicalRecords["assistant-1"].Contribution; got.TurnID != "user-1" {
		t.Fatalf("replay moved first contribution: %+v", got)
	}
}

func TestClaudeSourceTransitionPrefixAndRollback(t *testing.T) {
	state := emptyTransitionState()
	first, err := ProposeClaudeSourceTransition(state, sourceRecord(0, 10, `{"type":"tool_result","tool_use_id":"call-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitClaudeSourceTransition(&state, first); err != nil {
		t.Fatal(err)
	}
	extension, err := ProposeClaudeSourceTransition(state, sourceRecord(
		10, 20,
		`{"type":"tool_result","tool_use_id":"call-1"}`,
		`{"type":"tool_result","tool_use_id":"call-2"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if extension.Result != ClaudeTransitionAcceptedProjection || len(extension.NewBlockOccurrenceIDs) != 1 {
		t.Fatalf("extension = %+v", extension)
	}
	if err := CommitClaudeSourceTransition(&state, extension); err != nil {
		t.Fatal(err)
	}
	before, err := cloneClaudeSourceState(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProposeClaudeSourceTransition(state, sourceRecord(20, 30, `{"type":"text","text":"rewrite"}`)); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("non-monotonic error = %v", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("failed proposal mutated authoritative source state")
	}
}

func TestClaudeSourceTransitionCommitRejectsStaleCAS(t *testing.T) {
	state := emptyTransitionState()
	proposal, err := ProposeClaudeSourceTransition(state, sourceRecord(0, 10, `{"type":"text","text":"answer"}`))
	if err != nil {
		t.Fatal(err)
	}
	state.SourceStateRev++
	before, _ := cloneClaudeSourceState(state)
	if err := CommitClaudeSourceTransition(&state, proposal); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("stale CAS error = %v", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("stale commit mutated authoritative source state")
	}
}

func TestClaudeSourceTransitionControlAndAttachmentRowsAreSourceOnly(t *testing.T) {
	state := emptyTransitionState()
	queue := sourceRecord(0, 10)
	queue.LogicalRecordUUID = ""
	queue.StructuralKind = "queue-operation"
	queue.Contribution = ClaudeProjectionContribution{}
	queue.ControlOperation = "enqueue"
	proposal, err := ProposeClaudeSourceTransition(state, queue)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Result != ClaudeTransitionAcceptedSourceOnly {
		t.Fatalf("queue result = %q", proposal.Result)
	}
	if err := CommitClaudeSourceTransition(&state, proposal); err != nil {
		t.Fatal(err)
	}
	attachment := sourceRecord(10, 20)
	attachment.LogicalRecordUUID = "attachment-1"
	attachment.StructuralKind = "attachment"
	attachment.Contribution = ClaudeProjectionContribution{}
	proposal, err = ProposeClaudeSourceTransition(state, attachment)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Result != ClaudeTransitionAcceptedSourceOnly {
		t.Fatalf("attachment result = %q", proposal.Result)
	}
	if err := CommitClaudeSourceTransition(&state, proposal); err != nil {
		t.Fatal(err)
	}
	if len(state.GraphNodes["attachment-1"]) != 1 {
		t.Fatalf("attachment graph occurrence missing: %+v", state.GraphNodes)
	}
	if _, exists := state.LogicalRecords["attachment-1"]; exists {
		t.Fatal("attachment must not create a timeline logical record")
	}
}

func TestClaudeSourceStatePerformanceGate(t *testing.T) {
	if os.Getenv("CORDCODE_RUN_SOURCE_STATE_PERF") != "1" {
		t.Skip("set CORDCODE_RUN_SOURCE_STATE_PERF=1 for the hardware-bound release gate")
	}
	state := emptyTransitionState()
	const records = 2000
	for index := 0; index < records; index++ {
		uuid := fmt.Sprintf("record-%05d", index)
		rev := uint64(index + 1)
		state.GraphNodes[uuid] = []ClaudeGraphOccurrence{{
			SourceStateRev:    rev,
			StructuralKind:    "assistant",
			GraphResolvedTurn: "user-1",
			SegmentStableKey:  "segment-0",
			SourceGeneration:  state.SourceGeneration,
			RawByteStart:      int64(index * 100),
			RawByteEnd:        int64(index*100 + 100),
		}}
		state.LogicalRecords[uuid] = ClaudeLogicalRecord{
			ContentSequenceHash:   fmt.Sprintf("content-%064d", index),
			SemanticLifecycleHash: fmt.Sprintf("lifecycle-%064d", index),
			BlockOccurrenceIDs:    []string{fmt.Sprintf("block-%064d", index)},
			Contribution:          ClaudeProjectionContribution{TurnID: "user-1", PartID: uuid},
		}
	}
	state.SourceStateRev = records
	raw, err := EncodeClaudeSourceState(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 10*1024*1024 {
		t.Fatalf("state size = %d, limit = 10MiB", len(raw))
	}
	restoreSamples := make([]time.Duration, 25)
	for index := range restoreSamples {
		started := time.Now()
		if _, err := DecodeClaudeSourceState(raw); err != nil {
			t.Fatal(err)
		}
		restoreSamples[index] = time.Since(started)
	}

	root := t.TempDir()
	sourcePath := root + "/source.jsonl"
	sourceBytes := []byte("{}\n")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	source := ProjectionSourceDescriptor{Identity: "perf-session", Path: sourcePath, Cursor: int64(len(sourceBytes))}
	sourceCheckpoint, err := BuildProjectionSourceCheckpoint(source)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := NewReadyProjectionCheckpoint(
		"claude", "perf-session", sourceCheckpoint,
		SessionProjection{SessionID: "perf-session", SyncRev: 1, Execution: ExecutionView{Phase: "idle"}},
		time.Now(),
	)
	checkpoint.ClaudeSourceState = &state
	store := NewProjectionCheckpointStore(root)
	writeSamples := make([]time.Duration, 15)
	for index := range writeSamples {
		started := time.Now()
		if err := store.Save(checkpoint); err != nil {
			t.Fatal(err)
		}
		writeSamples[index] = time.Since(started)
	}
	restoreP95 := durationP95(restoreSamples)
	writeP95 := durationP95(writeSamples)
	t.Logf("records=%d stateBytes=%d restoreP95=%s checkpointWriteP95=%s", records, len(raw), restoreP95, writeP95)
	if restoreP95 > 100*time.Millisecond {
		t.Fatalf("restore p95 %s exceeds 100ms", restoreP95)
	}
	if writeP95 > 50*time.Millisecond {
		t.Fatalf("checkpoint write p95 %s exceeds 50ms", writeP95)
	}
}

func durationP95(samples []time.Duration) time.Duration {
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (len(ordered)*95 + 99) / 100
	if index == 0 {
		return 0
	}
	return ordered[index-1]
}
