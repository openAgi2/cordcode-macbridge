package gobridge

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProjectionRevisionJournalReturnsExactContiguousRangeAndClones(t *testing.T) {
	journal := NewProjectionRevisionJournal(8, 1<<20)
	patch1 := ProjectionPatch{
		BaseRev:     1,
		SyncRev:     2,
		Execution:   &ExecutionView{Phase: "running", ActiveTurnID: "turn-1"},
		UpsertTurns: []TurnProjection{{TurnID: "turn-1", Status: "running"}},
		PartOps: []PartOp{{
			TurnID: "turn-1", MessageID: "turn-1", Op: "append_text", Text: "real",
		}},
		ReplacesClientIDs: []string{"client-1"},
	}
	patch2 := ProjectionPatch{BaseRev: 2, SyncRev: 3, Execution: &ExecutionView{Phase: "idle"}}
	if !journal.Record("codex", "s1", patch1) || !journal.Record("codex", "s1", patch2) {
		t.Fatal("expected both committed patches to be recorded")
	}
	patch1.PartOps[0].Text = "mutated-source"
	patch1.ReplacesClientIDs[0] = "mutated-source"

	got, ok := journal.ContiguousRange("codex", "s1", 1, 3)
	if !ok || len(got) != 2 {
		t.Fatalf("range = %+v, ok=%v", got, ok)
	}
	if got[0].PartOps[0].Text != "real" || got[0].ReplacesClientIDs[0] != "client-1" {
		t.Fatalf("journal retained mutable caller storage: %+v", got[0])
	}
	got[0].PartOps[0].Text = "mutated-result"
	again, ok := journal.ContiguousRange("codex", "s1", 1, 3)
	if !ok || again[0].PartOps[0].Text != "real" {
		t.Fatalf("range exposed mutable journal storage: %+v", again)
	}

	wantJSON, _ := json.Marshal(ProjectionPatch{
		BaseRev:     1,
		SyncRev:     2,
		Execution:   &ExecutionView{Phase: "running", ActiveTurnID: "turn-1"},
		UpsertTurns: []TurnProjection{{TurnID: "turn-1", Status: "running"}},
		PartOps: []PartOp{{
			TurnID: "turn-1", MessageID: "turn-1", Op: "append_text", Text: "real",
		}},
		ReplacesClientIDs: []string{"client-1"},
	})
	gotJSON, _ := json.Marshal(again[0])
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("canonical patch changed\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestProjectionRevisionJournalRetentionGapAndOversizeFailFull(t *testing.T) {
	journal := NewProjectionRevisionJournal(2, 256)
	journal.Record("codex", "s1", ProjectionPatch{BaseRev: 1, SyncRev: 2})
	journal.Record("codex", "s1", ProjectionPatch{BaseRev: 2, SyncRev: 3})
	journal.Record("codex", "s1", ProjectionPatch{BaseRev: 3, SyncRev: 4})
	if _, ok := journal.ContiguousRange("codex", "s1", 1, 4); ok {
		t.Fatal("evicted base revision must require authoritative full")
	}
	if got, ok := journal.ContiguousRange("codex", "s1", 2, 4); !ok || len(got) != 2 {
		t.Fatalf("retained suffix = %+v, ok=%v", got, ok)
	}

	journal.Record("codex", "s1", ProjectionPatch{BaseRev: 9, SyncRev: 10})
	if _, ok := journal.ContiguousRange("codex", "s1", 3, 10); ok {
		t.Fatal("revision gap must require authoritative full")
	}
	oversized := ProjectionPatch{
		BaseRev: 10, SyncRev: 11,
		PartOps: []PartOp{{TurnID: "t", MessageID: "t", Op: "append_text", Text: string(make([]byte, 512))}},
	}
	if journal.Record("codex", "s1", oversized) {
		t.Fatal("oversized patch must not enter bounded journal")
	}
	if _, ok := journal.ContiguousRange("codex", "s1", 9, 10); ok {
		t.Fatal("oversize admission must clear the previous suffix to avoid stale resume")
	}
}

func TestEventPublisherRecordsPatchWithoutOnlineTargets(t *testing.T) {
	publisher := NewEventPublisher("epoch-journal")
	patch := ProjectionPatch{BaseRev: 4, SyncRev: 5, ReplacesClientIDs: []string{"client-real"}}
	publisher.PublishProjectionPatch("codex", "s1", patch)
	publisher.mu.Lock()
	got, ok := publisher.projectionJournal.ContiguousRange("codex", "s1", 4, 5)
	publisher.mu.Unlock()
	if !ok || len(got) != 1 || got[0].ReplacesClientIDs[0] != "client-real" {
		t.Fatalf("offline journal range = %+v, ok=%v", got, ok)
	}
}

func TestEventPublisherFlushPatchAndRecordUsesAuthoritativeReducerPayload(t *testing.T) {
	publisher := NewEventPublisher("epoch-flush-record")
	publisher.projection.Apply(EventMessage{
		BackendID: "codex", SessionID: "s1", Event: "turn_started", PerSessionSeq: 1,
		Data: map[string]interface{}{"turnId": "turn-1"},
	})
	publisher.projection.Apply(EventMessage{
		BackendID: "codex", SessionID: "s1", Event: "text_delta", PerSessionSeq: 2,
		Data: map[string]interface{}{"itemId": "turn-1", "delta": "baseline"},
	})
	if _, ok := publisher.projection.FlushPatch("codex", "s1"); !ok {
		t.Fatal("expected baseline flush")
	}
	publisher.projection.Apply(EventMessage{
		BackendID: "codex", SessionID: "s1", Event: "text_delta", PerSessionSeq: 3,
		Data: map[string]interface{}{"itemId": "turn-1", "delta": " authoritative"},
	})
	patch, ok := publisher.FlushPatchAndRecord("codex", "s1")
	if !ok {
		t.Fatal("expected authoritative reducer patch")
	}
	publisher.mu.Lock()
	got, rangeOK := publisher.projectionJournal.ContiguousRange("codex", "s1", patch.BaseRev, patch.SyncRev)
	publisher.mu.Unlock()
	if !rangeOK || len(got) != 1 || !reflect.DeepEqual(got[0], patch) {
		t.Fatalf("flush/record diverged: patch=%+v range=%+v ok=%v", patch, got, rangeOK)
	}
}
