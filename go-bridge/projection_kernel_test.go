package gobridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func writeProjectionSource(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testProjection(sessionID string, rev int) SessionProjection {
	return SessionProjection{
		SessionID:   sessionID,
		SyncRev:     rev,
		BridgeEpoch: "epoch-checkpoint",
		Execution:   ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{{
			TurnID: "turn-1",
			Status: "completed",
			Assistant: &MessageProjection{
				ID:   "turn-1",
				Role: "assistant",
				Parts: []ProjectionPart{{
					Type:       "tool",
					ItemID:     "call-1",
					ToolStatus: "completed",
					ToolInput: map[string]interface{}{
						"nested": []interface{}{map[string]interface{}{"value": "original"}},
					},
				}},
			},
		}},
	}
}

func saveTestCheckpoint(
	t *testing.T,
	store *ProjectionCheckpointStore,
	source ProjectionSourceDescriptor,
	projection SessionProjection,
) ProjectionCheckpoint {
	t.Helper()
	sourceCheckpoint, err := BuildProjectionSourceCheckpoint(source)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := NewReadyProjectionCheckpoint(
		"codex",
		projection.SessionID,
		sourceCheckpoint,
		projection,
		time.Unix(100, 0),
	)
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func TestProjectionCheckpointRestoreValidAndEmpty(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "rollout.jsonl")
	writeProjectionSource(t, sourcePath, "prefix\nappended\n")
	source := ProjectionSourceDescriptor{Identity: "rollout-1", Path: sourcePath, Cursor: int64(len("prefix\n"))}
	store := NewProjectionCheckpointStore(dir)

	want := testProjection("session-1", 7)
	saveTestCheckpoint(t, store, source, want)
	kernel := NewProjectionKernel(nil, store)
	restored, err := kernel.RestoreCheckpoint("codex", "session-1", source)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := kernel.Snapshot("codex", "session-1")
	if !ok || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(restored.Projection, want) {
		t.Fatalf("restored projection mismatch: ok=%v got=%+v", ok, got)
	}

	empty := SessionProjection{
		SessionID: "empty-session",
		SyncRev:   3,
		Execution: ExecutionView{Phase: "idle"},
		Turns:     []TurnProjection{},
	}
	saveTestCheckpoint(t, store, source, empty)
	emptyKernel := NewProjectionKernel(nil, store)
	if _, err := emptyKernel.RestoreCheckpoint("codex", "empty-session", source); err != nil {
		t.Fatal(err)
	}
	gotEmpty, ok := emptyKernel.Snapshot("codex", "empty-session")
	if !ok || gotEmpty.SessionID != "empty-session" || len(gotEmpty.Turns) != 0 {
		t.Fatalf("valid empty checkpoint must be ready: ok=%v projection=%+v", ok, gotEmpty)
	}
}

func TestProjectionCheckpointAdmissionAppendAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "rollout.jsonl")
	prefix := "record-one\n"
	writeProjectionSource(t, sourcePath, prefix)
	source := ProjectionSourceDescriptor{Identity: "rollout-1", Path: sourcePath, Cursor: int64(len(prefix))}
	store := NewProjectionCheckpointStore(dir)
	saveTestCheckpoint(t, store, source, testProjection("session-1", 4))

	writeProjectionSource(t, sourcePath, prefix+"record-two\n")
	got, err := store.LoadValidated("codex", "session-1", source)
	if err != nil {
		t.Fatalf("append must preserve consumed-prefix checkpoint: %v", err)
	}
	if got.Source.Cursor != int64(len(prefix)) {
		t.Fatalf("checkpoint cursor moved on admission: %d", got.Source.Cursor)
	}

	badIdentity := source
	badIdentity.Identity = "replacement-rollout"
	if _, err := store.LoadValidated("codex", "session-1", badIdentity); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("identity replacement error = %v", err)
	}

	writeProjectionSource(t, sourcePath, "short")
	if _, err := store.LoadValidated("codex", "session-1", source); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("truncate error = %v", err)
	}

	writeProjectionSource(t, sourcePath, "RECORD-ONE\nrecord-two\n")
	if _, err := store.LoadValidated("codex", "session-1", source); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("prefix mutation error = %v", err)
	}
}

func TestProjectionCheckpointAtomicWriteInterruptionKeepsPreviousCommit(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "rollout.jsonl")
	writeProjectionSource(t, sourcePath, "record\n")
	source := ProjectionSourceDescriptor{Identity: "rollout-1", Path: sourcePath, Cursor: int64(len("record\n"))}
	store := NewProjectionCheckpointStore(dir)
	saveTestCheckpoint(t, store, source, testProjection("session-1", 1))

	sourceCheckpoint, err := BuildProjectionSourceCheckpoint(source)
	if err != nil {
		t.Fatal(err)
	}
	store.beforeRename = func(_, _ string) error { return errors.New("injected rename interruption") }
	if err := store.Save(NewReadyProjectionCheckpoint(
		"codex", "session-1", sourceCheckpoint, testProjection("session-1", 2), time.Unix(200, 0),
	)); err == nil {
		t.Fatal("interrupted atomic write unexpectedly succeeded")
	}
	store.beforeRename = nil

	got, err := store.LoadValidated("codex", "session-1", source)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectionRev != 1 {
		t.Fatalf("interrupted write replaced committed checkpoint: rev=%d", got.ProjectionRev)
	}
	temps, err := filepath.Glob(filepath.Join(store.baseDir, ".projection-checkpoint-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary checkpoint files leaked: %v", temps)
	}
}

type recordingCheckpointStore struct {
	mu           sync.Mutex
	saves        []ProjectionCheckpoint
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func (s *recordingCheckpointStore) LoadValidated(
	_, _ string,
	_ ProjectionSourceDescriptor,
) (ProjectionCheckpoint, error) {
	return ProjectionCheckpoint{}, os.ErrNotExist
}

func (s *recordingCheckpointStore) Save(checkpoint ProjectionCheckpoint) error {
	s.mu.Lock()
	index := len(s.saves)
	s.saves = append(s.saves, checkpoint)
	s.mu.Unlock()
	if index == 0 && s.firstStarted != nil {
		close(s.firstStarted)
		<-s.releaseFirst
	}
	return nil
}

func (s *recordingCheckpointStore) revisions() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	revisions := make([]int, len(s.saves))
	for i := range s.saves {
		revisions[i] = s.saves[i].ProjectionRev
	}
	return revisions
}

func stagedCheckpoint(rev int) ProjectionCheckpoint {
	projection := testProjection("session-1", rev)
	return ProjectionCheckpoint{
		BackendID: "codex",
		SessionID: "session-1",
		Source: ProjectionSourceCheckpoint{
			Identity:     "rollout-1",
			Cursor:       1,
			PrefixSHA256: "digest",
		},
		Projection:    projection,
		ProjectionRev: rev,
	}
}

func TestProjectionCheckpointCoalescesAndSerializesWrites(t *testing.T) {
	store := &recordingCheckpointStore{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	kernel := NewProjectionKernel(nil, store)
	kernel.checkpointPolicy = ProjectionCheckpointPolicy{MaxInterval: time.Hour, MaxRevisions: 3}

	for rev := 1; rev <= 3; rev++ {
		if err := kernel.StageCheckpoint(stagedCheckpoint(rev), false); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-store.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("revision threshold did not start checkpoint write")
	}
	for rev := 4; rev <= 6; rev++ {
		if err := kernel.StageCheckpoint(stagedCheckpoint(rev), false); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.revisions(); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("concurrent checkpoint write escaped single-flight: %v", got)
	}
	close(store.releaseFirst)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := kernel.FlushCheckpoint(ctx, "codex", "session-1"); err != nil {
		t.Fatal(err)
	}
	if got := store.revisions(); !reflect.DeepEqual(got, []int{3, 6}) {
		t.Fatalf("coalesced checkpoint revisions = %v, want [3 6]", got)
	}
}

func TestProjectionCheckpointCrashRecoveryUsesLastPersistedCursor(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "rollout.jsonl")
	writeProjectionSource(t, sourcePath, "old\nnew\n")
	persistedSource := ProjectionSourceDescriptor{Identity: "rollout-1", Path: sourcePath, Cursor: int64(len("old\n"))}
	store := NewProjectionCheckpointStore(dir)
	saveTestCheckpoint(t, store, persistedSource, testProjection("session-1", 2))

	afterRestart := NewProjectionKernel(nil, store)
	restored, err := afterRestart.RestoreCheckpoint("codex", "session-1", persistedSource)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ProjectionRev != 2 || restored.Source.Cursor != int64(len("old\n")) {
		t.Fatalf("restart did not use last durable cut: %+v", restored)
	}
}

func TestProjectionHydrateRetryPolicy(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	now := time.Unix(1000, 0)
	kernel.now = func() time.Time { return now }
	kernel.retryPolicy = ProjectionRetryPolicy{Initial: time.Second, Maximum: 4 * time.Second}

	if !kernel.BeginHydrate("codex", "session-1", false, false) {
		t.Fatal("absent session did not begin hydrate")
	}
	failed := kernel.MarkFailed("codex", "session-1", "io", "temporary", true)
	if failed.Failure == nil || failed.Failure.Attempts != 1 || !failed.Failure.RetryAt.Equal(now.Add(time.Second)) {
		t.Fatalf("unexpected retryable failure: %+v", failed)
	}
	if kernel.BeginHydrate("codex", "session-1", false, false) {
		t.Fatal("retryable failure tight-looped before retryAt")
	}
	if !kernel.BeginHydrate("codex", "session-1", true, false) {
		t.Fatal("explicit retry did not bypass retryAt")
	}
	kernel.MarkFailed("codex", "session-1", "io", "temporary", true)
	now = now.Add(2 * time.Second)
	if !kernel.BeginHydrate("codex", "session-1", false, false) {
		t.Fatal("retryable failure did not recover at retryAt")
	}
	kernel.MarkFailed("codex", "session-1", "format", "permanent", false)
	if kernel.BeginHydrate("codex", "session-1", true, false) {
		t.Fatal("explicit retry bypassed nonretryable source failure")
	}
	if !kernel.BeginHydrate("codex", "session-1", false, true) {
		t.Fatal("source change did not unblock nonretryable failure")
	}
}

func TestProjectionKernelReadyEmptyAndBareShellReadiness(t *testing.T) {
	reducer := newTestReducer()
	kernel := NewProjectionKernel(reducer, nil)
	reducer.Apply(ev(1, "codex", "bare", "turn_started", map[string]interface{}{"turnId": "turn-shell"}))
	if _, ok := kernel.Snapshot("codex", "bare"); ok {
		t.Fatal("bare reducer shell inferred ready without lifecycle commit")
	}

	kernel.MarkReady("codex", "empty")
	empty, ok := kernel.Snapshot("codex", "empty")
	if !ok || empty.SessionID != "empty" || len(empty.Turns) != 0 || empty.Execution.Phase != "idle" {
		t.Fatalf("committed empty session is not a ready empty projection: ok=%v projection=%+v", ok, empty)
	}
}

func TestProjectionSnapshotDeeplyImmutable(t *testing.T) {
	reducer := NewProjectionReducer()
	reducer.Restore("codex", "session-1", testProjection("session-1", 5))
	first, ok := reducer.Snapshot("codex", "session-1")
	if !ok {
		t.Fatal("missing restored projection")
	}
	nested := first.Turns[0].Assistant.Parts[0].ToolInput.(map[string]interface{})
	nested["nested"].([]interface{})[0].(map[string]interface{})["value"] = "mutated"
	second, _ := reducer.Snapshot("codex", "session-1")
	got := second.Turns[0].Assistant.Parts[0].ToolInput.(map[string]interface{})["nested"].([]interface{})[0].(map[string]interface{})["value"]
	if got != "original" {
		t.Fatalf("snapshot shared nested tool JSON: %v", got)
	}
}

func TestProjectionRevisionAdvancesOnlyOnProjectionCommit(t *testing.T) {
	reducer := newTestReducer()
	reducer.Apply(ev(1, "codex", "session-1", "turn_started", map[string]interface{}{"turnId": ""}))
	reducer.Apply(ev(2, "codex", "session-1", "text_delta", map[string]interface{}{"itemId": "turn-1", "delta": "a"}))
	first, ok := reducer.Snapshot("codex", "session-1")
	if !ok || first.SyncRev != 1 {
		t.Fatalf("first committed projection rev = %d ok=%v, want 1", first.SyncRev, ok)
	}
	reducer.Apply(ev(3, "codex", "session-1", "unrelated_event", map[string]interface{}{"value": "ignored"}))
	reducer.Apply(ev(4, "codex", "session-1", "text_delta", map[string]interface{}{"itemId": "turn-1", "delta": "b"}))
	second, _ := reducer.Snapshot("codex", "session-1")
	if second.SyncRev != 2 {
		t.Fatalf("projection rev after two commits = %d, want 2", second.SyncRev)
	}
}
