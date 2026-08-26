package gobridge

import (
	"reflect"
	"testing"
)

// D1 — Kernel ingest tri-state (web push §8.1 / R2-O1 baseline).
//
// IngestLive must distinguish applied | deferred | no_change; only `applied` may drive
// the live-path patch flush in EventPublisher. CommitHydrateTransaction must report the
// pendingLive rows that actually advanced the reducer; MarkFailed must surface the
// uncommitted deferred eventIds so callers can discard push candidates without faking
// a send.

func tristateKernel() *ProjectionKernel {
	return NewProjectionKernel(NewProjectionReducer(), nil)
}

func TestIngestLiveTriStateAppliedVsNoChange(t *testing.T) {
	kernel := tristateKernel()
	msg := EventMessage{
		BackendID: "codex", SessionID: "tri-1", BridgeEpoch: "e1",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"turnId": "t1", "itemId": "t1", "text": "hello"},
	}
	if got := kernel.IngestLive(msg); got != ProjectionIngestApplied {
		t.Fatalf("first apply = %v, want ProjectionIngestApplied", got)
	}
	// Exact duplicate (same content, no new revision) → NoChange.
	if got := kernel.IngestLive(msg); got != ProjectionIngestNoChange {
		t.Fatalf("duplicate apply = %v, want ProjectionIngestNoChange", got)
	}
}

func TestIngestLiveTriStateDeferredDuringHydrate(t *testing.T) {
	kernel := tristateKernel()
	if _, err := kernel.BeginHydrateTransaction(
		"codex", "tri-2", ProjectionSourceDescriptor{Identity: "tri-2", Cursor: 1},
		false, false, false,
	); err != nil {
		t.Fatal(err)
	}
	msg := EventMessage{
		BackendID: "codex", SessionID: "tri-2", BridgeEpoch: "e1", EventID: "e1:42",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"turnId": "t1", "itemId": "t1", "text": "hello"},
	}
	if got := kernel.IngestLive(msg); got != ProjectionIngestDeferred {
		t.Fatalf("hydrate-window apply = %v, want ProjectionIngestDeferred", got)
	}
	// The committed reducer has NOT advanced — deferred must be observationally identical
	// to the old `false` for non-push callers (no half-applied state).
	if kernel.HasReducerState("codex", "tri-2") {
		if rev := kernel.reducer.LastAppliedRev("codex", "tri-2"); rev != 0 {
			t.Fatalf("deferred event leaked into committed reducer: rev = %d", rev)
		}
	}

	commit, err := kernel.CommitHydrateTransaction("codex", "tri-2")
	if err != nil {
		t.Fatal(err)
	}
	if commit.PendingLive != 1 {
		t.Fatalf("PendingLive = %d", commit.PendingLive)
	}
	if !reflect.DeepEqual(commit.AppliedPendingEventIDs, []string{"e1:42"}) {
		t.Fatalf("AppliedPendingEventIDs = %v, want [e1:42]", commit.AppliedPendingEventIDs)
	}
}

func TestCommitHydrateAppliedPendingIDsExcludesNoops(t *testing.T) {
	kernel := tristateKernel()
	if _, err := kernel.BeginHydrateTransaction(
		"codex", "tri-3", ProjectionSourceDescriptor{Identity: "tri-3", Cursor: 1},
		false, false, false,
	); err != nil {
		t.Fatal(err)
	}
	advancing := EventMessage{
		BackendID: "codex", SessionID: "tri-3", BridgeEpoch: "e1", EventID: "e1:7",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"turnId": "t1", "itemId": "t1", "text": "hello"},
	}
	duplicate := advancing // same shape → second apply is a no-op after restore

	if got := kernel.IngestLive(advancing); got != ProjectionIngestDeferred {
		t.Fatalf("first = %v", got)
	}
	if got := kernel.IngestLive(duplicate); got != ProjectionIngestDeferred {
		t.Fatalf("second = %v", got)
	}
	commit, err := kernel.CommitHydrateTransaction("codex", "tri-3")
	if err != nil {
		t.Fatal(err)
	}
	if commit.PendingLive != 2 {
		t.Fatalf("PendingLive = %d, want 2", commit.PendingLive)
	}
	// Only the row that actually advanced the committed reducer is reported: the deferred
	// candidate release must never fire for a no-op duplicate (Gate D: 同一 key 只入队一次).
	if !reflect.DeepEqual(commit.AppliedPendingEventIDs, []string{"e1:7"}) {
		t.Fatalf("AppliedPendingEventIDs = %v, want [e1:7]", commit.AppliedPendingEventIDs)
	}
}

func TestMarkFailedReturnsUncommittedDeferredEventIDs(t *testing.T) {
	kernel := tristateKernel()
	if _, err := kernel.BeginHydrateTransaction(
		"codex", "tri-4", ProjectionSourceDescriptor{Identity: "tri-4", Cursor: 1},
		false, false, false,
	); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1:10", "e1:11"} {
		kernel.IngestLive(EventMessage{
			BackendID: "codex", SessionID: "tri-4", BridgeEpoch: "e1", EventID: id,
			PerSessionSeq: 1, Event: "user_message",
			Data: map[string]interface{}{"turnId": "t1", "itemId": "t1", "text": "hello"},
		})
	}
	status, deferred := kernel.MarkFailed("codex", "tri-4", "projection.source_batch_build_failed", "boom", true)
	if status.Phase != ProjectionHydrateFailed {
		t.Fatalf("phase = %v", status.Phase)
	}
	if !reflect.DeepEqual(deferred, []string{"e1:10", "e1:11"}) {
		t.Fatalf("deferred = %v, want [e1:10 e1:11]", deferred)
	}
	// Transaction is gone: a second MarkFailed reports no stragglers (idempotent cleanup).
	_, deferredAgain := kernel.MarkFailed("codex", "tri-4", "projection.source_batch_build_failed", "boom", true)
	if len(deferredAgain) != 0 {
		t.Fatalf("deferred after cleanup = %v", deferredAgain)
	}
}

func TestMarkFailedOutsideHydrateHasNoDeferred(t *testing.T) {
	kernel := tristateKernel()
	_, deferred := kernel.MarkFailed("codex", "tri-5", "projection.commit_failed", "no tx", true)
	if len(deferred) != 0 {
		t.Fatalf("deferred = %v, want empty", deferred)
	}
}

// The R2-O1 regression baseline: the EventPublisher live path maps ONLY
// ProjectionIngestApplied to projectionApplied (patch flush). Deferred and NoChange must
// not flush at that site. This is asserted at the publisher integration level by the
// existing projection-patch suites; here we pin the mapping constant so the tri-state
// cannot silently regress to truthiness.
func TestProjectionIngestResultOrdering(t *testing.T) {
	// NoChange is the zero value: a missing/zero result can never be mistaken for Applied.
	var zero ProjectionIngestResult
	if zero != ProjectionIngestNoChange {
		t.Fatalf("zero value = %v, want NoChange", zero)
	}
	if ProjectionIngestApplied == ProjectionIngestNoChange || ProjectionIngestDeferred == ProjectionIngestNoChange {
		t.Fatal("tri-state values must be distinct")
	}
}
