package gobridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func applyClaudeSourceFixture(
	t *testing.T,
	name string,
) (*ProjectionKernel, []ClaudeSourceBatchResult) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeSourceShapesDir, name))
	if err != nil {
		t.Fatal(err)
	}
	correlation := claudeSourceCorrelation{
		SegmentStableKey: "fixture-segment", SegmentGeneration: "fixture-generation",
	}
	state := ClaudeSourceState{
		SchemaVersion: ClaudeSourceStateSchemaVersion, SourceGeneration: "fixture-source-generation",
		CursorVector: []ClaudeSourceCursor{{
			SegmentStableKey:  correlation.SegmentStableKey,
			SegmentGeneration: correlation.SegmentGeneration,
			MembershipDigest:  "fixture-membership",
		}},
		GraphNodes: map[string][]ClaudeGraphOccurrence{}, LogicalRecords: map[string]ClaudeLogicalRecord{},
	}
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	if err := kernel.InstallClaudeSourceState("claude", name, state); err != nil {
		t.Fatal(err)
	}
	scan, err := scanCompleteClaudeRelayEntriesFromReader(
		strings.NewReader(string(data)), 0, &claudeRelayScanState{},
	)
	if err != nil || scan.Poison != nil {
		t.Fatalf("scan %s: poison=%+v err=%v", name, scan.Poison, err)
	}
	results := make([]ClaudeSourceBatchResult, 0, len(scan.Records))
	for _, record := range scan.Records {
		if !record.Admitted {
			continue
		}
		current, ok := kernel.ClaudeSourceStateSnapshot("claude", name)
		if !ok {
			t.Fatal("source state disappeared")
		}
		batch, err := buildClaudeSourceRecordBatch(
			current, record, "claude", name, "epoch", correlation, "",
		)
		if err != nil {
			t.Fatalf("map %s [%d,%d): %v", name, record.ByteStart, record.ByteEnd, err)
		}
		result, err := kernel.ApplyClaudeSourceRecordBatch(batch)
		if err != nil {
			t.Fatalf("apply %s [%d,%d): %v", name, record.ByteStart, record.ByteEnd, err)
		}
		results = append(results, result)
	}
	return kernel, results
}

func TestClaudeSourceMapperExactReplayIsGraphOnlyProjectionStable(t *testing.T) {
	kernel, results := applyClaudeSourceFixture(t, "exact-text-replay.jsonl")
	if len(results) != 4 ||
		results[0].Status != ClaudeSourceBatchAcceptedProjection ||
		results[1].Status != ClaudeSourceBatchAcceptedProjection ||
		results[2].Status != ClaudeSourceBatchAcceptedProjection ||
		results[3].Status != ClaudeSourceBatchAcceptedSourceOnly {
		t.Fatalf("transition results = %+v", results)
	}
	projection, ok := kernel.reducer.Snapshot("claude", "exact-text-replay.jsonl")
	if !ok || len(projection.Turns) != 2 {
		t.Fatalf("projection = %+v ok=%v", projection, ok)
	}
	answerCount := 0
	for _, turn := range projection.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			answerCount += strings.Count(part.Text, "answer part 1")
		}
	}
	if answerCount != 1 {
		t.Fatalf("exact replay projected %d copies: %+v", answerCount, projection)
	}
	state, _ := kernel.ClaudeSourceStateSnapshot("claude", "exact-text-replay.jsonl")
	if len(state.GraphNodes["a-text-1"]) != 2 ||
		state.LogicalRecords["a-text-1"].Contribution.TurnID != "u-root" {
		t.Fatalf("graph-only replay moved/lost contribution: %+v", state)
	}
}

func TestClaudeSourceMapperDistinctUUIDUsesParentChainOwner(t *testing.T) {
	kernel, results := applyClaudeSourceFixture(t, "branch-fileorder-interleave.jsonl")
	if len(results) != 4 {
		t.Fatalf("transition results = %+v", results)
	}
	projection, ok := kernel.reducer.Snapshot("claude", "branch-fileorder-interleave.jsonl")
	if !ok || len(projection.Turns) != 2 {
		t.Fatalf("projection = %+v ok=%v", projection, ok)
	}
	var turnA, turnB *TurnProjection
	for index := range projection.Turns {
		switch projection.Turns[index].TurnID {
		case "u-A":
			turnA = &projection.Turns[index]
		case "u-B":
			turnB = &projection.Turns[index]
		}
	}
	if turnA == nil || turnA.Assistant == nil || turnB == nil {
		t.Fatalf("missing preserved admitted turns: %+v", projection)
	}
	turnAText := ""
	for _, part := range turnA.Assistant.Parts {
		turnAText += part.Text
	}
	if !strings.Contains(turnAText, "turn A answer") ||
		!strings.Contains(turnAText, "branched reply under turn A ancestry") {
		t.Fatalf("turn A parent-chain content = %q", turnAText)
	}
	if turnB.Assistant != nil {
		for _, part := range turnB.Assistant.Parts {
			if strings.Contains(part.Text, "branched reply") {
				t.Fatalf("branch reply remained on file-order turn B: %+v", projection)
			}
		}
	}
	state, _ := kernel.ClaudeSourceStateSnapshot("claude", "branch-fileorder-interleave.jsonl")
	if state.LogicalRecords["a-2"].Contribution.TurnID != "u-A" {
		t.Fatalf("a-2 graph owner = %+v", state.LogicalRecords["a-2"])
	}
}

func TestClaudeSourceMapperPrefixExtensionDoesNotReapplyOldBlock(t *testing.T) {
	kernel, results := applyClaudeSourceFixture(t, "tool-result-extension.jsonl")
	if len(results) != 4 {
		t.Fatalf("transition results = %+v", results)
	}
	if results[3].PhysicalRowsAcknowledged != 1 ||
		results[3].LogicalRecordsChanged != 1 ||
		results[3].ProjectionSubeventsApplied != 0 ||
		results[3].Status != ClaudeSourceBatchAcceptedSourceOnly {
		t.Fatalf("prefix extension result = %+v", results[3])
	}
	state, _ := kernel.ClaudeSourceStateSnapshot("claude", "tool-result-extension.jsonl")
	logical := state.LogicalRecords["tr-1"]
	if len(logical.BlockOccurrenceIDs) != 2 || len(state.GraphNodes["tr-1"]) != 2 {
		t.Fatalf("prefix extension ledger = %+v graph=%+v", logical, state.GraphNodes["tr-1"])
	}
	projection, _ := kernel.reducer.Snapshot("claude", "tool-result-extension.jsonl")
	toolResultCount := 0
	for _, turn := range projection.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.ToolResult != nil {
				toolResultCount++
			}
		}
	}
	if toolResultCount != 1 {
		t.Fatalf("old tool result was reapplied or lost: count=%d projection=%+v", toolResultCount, projection)
	}
}
