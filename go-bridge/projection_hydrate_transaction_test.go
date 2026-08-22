package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectionJSONLStartCutExcludesIncompleteRecord(t *testing.T) {
	path := t.TempDir() + "/growing.jsonl"
	complete := "{\"type\":\"session_meta\",\"payload\":{\"id\":\"s\"}}\n"
	partial := "{\"type\":\"event_msg\""
	if err := os.WriteFile(path, []byte(complete+partial), 0o600); err != nil {
		t.Fatal(err)
	}
	cut, err := projectionJSONLStartCut(path)
	if err != nil {
		t.Fatal(err)
	}
	if cut != int64(len(complete)) {
		t.Fatalf("start cut = %d, want last complete record boundary %d", cut, len(complete))
	}
}

func TestProjectionKernelRehydratesWhenCompositeSourceCutChanges(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	source := ProjectionSourceDescriptor{
		Identity: "logical-session",
		Cursor:   20,
		Segments: []ProjectionSourceSegment{
			{Identity: "parent", Path: "/tmp/parent.jsonl", Cursor: 10},
			{Identity: "child", Path: "/tmp/child.jsonl", Cursor: 10},
		},
	}
	admission, err := kernel.BeginHydrateTransaction(
		"claudecode", source.Identity, source, false, false, false,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("initial admission = %+v err=%v", admission, err)
	}
	if _, err := kernel.CommitHydrateTransaction("claudecode", source.Identity); err != nil {
		t.Fatal(err)
	}

	unchanged, err := kernel.BeginHydrateTransaction(
		"claudecode", source.Identity, source, false, false, false,
	)
	if err != nil || !unchanged.AlreadyReady {
		t.Fatalf("unchanged source must reuse Ready state: %+v err=%v", unchanged, err)
	}

	advanced := cloneProjectionSourceDescriptor(source)
	advanced.Segments[1].Cursor = 15
	advanced.Cursor = 25
	changed, err := kernel.BeginHydrateTransaction(
		"claudecode", source.Identity, advanced, false, false, false,
	)
	if err != nil || !changed.Leader || changed.AlreadyReady {
		t.Fatalf("advanced composite source must start private rebuild: %+v err=%v", changed, err)
	}
}

func TestProjectionHydrateGrowingSourceKeepsBaselineAndPendingDisjoint(t *testing.T) {
	path := t.TempDir() + "/rollout.jsonl"
	writeProjectionHydrateRollout(t, path, 1)
	cut, err := projectionJSONLStartCut(path)
	if err != nil {
		t.Fatal(err)
	}

	handlers := NewHandlers()
	var offlineRouted atomic.Int32
	handlers.eventPublisher.SetOfflineRoute(func(EventMessage) {
		offlineRouted.Add(1)
	})
	recovering := newPublisherCaptureConn(nil)
	if _, err := handlers.eventPublisher.BeginRecovery(recovering, "recovery-growing"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handlers.eventPublisher.UnregisterConnection(recovering) })
	source := ProjectionSourceDescriptor{
		Identity: "session-growing",
		Path:     path,
		Cursor:   cut,
	}
	admission, err := handlers.projectionKernel.BeginHydrateTransaction(
		"codex", "session-growing", source, false, false, false,
	)
	if err != nil || !admission.Leader || admission.StartCut != cut {
		t.Fatalf("hydrate admission = %+v err=%v", admission, err)
	}

	// The source grows after the strict start cut. These same logical facts arrive live and
	// must enter pending exactly once; the baseline scanner is forbidden to cross cut.
	writeProjectionHydrateRollout(t, path, 2)
	for _, logical := range []LogicalEvent{
		{
			BackendID: "codex", SessionID: "session-growing", Event: "turn_started",
			Data: map[string]interface{}{"turnId": "turn-2"}, Targets: []Connection{recovering},
		},
		{
			BackendID: "codex", SessionID: "session-growing", Event: "user_message",
			Data: map[string]interface{}{"itemId": "msg_2", "turnId": "turn-2", "text": "q2"}, Targets: []Connection{recovering},
		},
		{
			BackendID: "codex", SessionID: "session-growing", Event: "text_delta",
			Data: map[string]interface{}{"itemId": "turn-2", "delta": "a2"}, Targets: []Connection{recovering},
		},
		{
			BackendID: "codex", SessionID: "session-growing", Event: "turn_completed",
			Data: map[string]interface{}{"turnId": "turn-2"}, Targets: []Connection{recovering},
		},
	} {
		handlers.eventPublisher.PublishLogical(logical)
	}
	if _, ok := handlers.projectionKernel.Snapshot("codex", "session-growing"); ok {
		t.Fatal("uncommitted baseline became visible during hydrate")
	}

	base := SessionProjection{}
	err = handlers.produceProjectionHydrateRange(
		context.Background(),
		"codex",
		"session-growing",
		path,
		admission.StartCursor,
		admission.StartCut,
		base,
		func(event projectionHydrateEvent) bool {
			handlers.projectionKernel.ApplyHydrateEvent(
				"codex",
				"session-growing",
				handlers.eventPublisher.BridgeEpoch(),
				event.Event,
				event.Data,
			)
			return true
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := handlers.projectionKernel.CommitHydrateTransaction("codex", "session-growing")
	if err != nil {
		t.Fatal(err)
	}
	if commit.PendingLive != 4 {
		t.Fatalf("pending live count = %d, want 4", commit.PendingLive)
	}
	if len(commit.Projection.Turns) != 2 || commit.Projection.SyncRev != 6 {
		t.Fatalf("committed projection = %+v", commit.Projection)
	}
	if commit.Projection.Turns[0].TurnID != "turn-1" ||
		commit.Projection.Turns[1].TurnID != "turn-2" {
		t.Fatalf("baseline/pending order = %+v", commit.Projection.Turns)
	}
	if got := projectionTurnText(commit.Projection.Turns[1]); got != "q2a2" {
		t.Fatalf("post-cut turn content duplicated or lost: %q", got)
	}

	records := handlers.eventPublisher.EventBuffer().RecordsForTesting()
	if len(records) != 4 {
		t.Fatalf("hydrate polluted EventBuffer: records=%d, want live-only 4", len(records))
	}
	for _, record := range records {
		if record.message.EventID == "" || record.message.Seq == 0 {
			t.Fatalf("live record was not normally stamped: %+v", record.message)
		}
	}
	if handlers.eventPublisher.seq != 4 {
		t.Fatalf("hydrate allocated global EventPublisher seq: got %d, want live-only 4", handlers.eventPublisher.seq)
	}
	if offlineRouted.Load() != 0 {
		t.Fatalf("hydrate/live transaction wrote offline route %d times", offlineRouted.Load())
	}
	handlers.eventPublisher.mu.Lock()
	recoveryPending := len(handlers.eventPublisher.recoveries[recovering].pending)
	handlers.eventPublisher.mu.Unlock()
	if recoveryPending != 4 {
		t.Fatalf("hydrate polluted recovery pending queue: got %d, want live-only 4", recoveryPending)
	}
}

func TestClaudeProjectionHydrateAppliesPendingLiveOnCommit(t *testing.T) {
	// §3.1/§3.2 of the SSV2 running-session cold-open fix: the Claude live file relay starts
	// BEFORE the hydrate wait and routes in-flight content rows into the transaction's
	// pendingLive (same authoritative mapper as the source-batch path). CommitHydrateTransaction
	// therefore no longer rejects pendingLive for Claude; it applies the queued events after the
	// cold baseline in their stamped order, so the committed projection is the honest running
	// partial plus any post-cut content that already arrived.
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	source := ProjectionSourceDescriptor{
		Identity: "claude-overlap", Path: "/private/source.jsonl", Cursor: 100,
	}
	if _, err := kernel.BeginHydrateTransaction(
		"claude", "claude-overlap", source, false, false, false,
	); err != nil {
		t.Fatal(err)
	}
	if !kernel.ApplyHydrateEvent(
		"claude", "claude-overlap", "epoch", "user_message",
		map[string]interface{}{"turnId": "turn-1", "itemId": "turn-1", "text": "baseline"},
	) {
		t.Fatal("baseline event was not applied")
	}
	kernel.IngestLive(EventMessage{
		BackendID: "claude", SessionID: "claude-overlap", BridgeEpoch: "epoch",
		PerSessionSeq: 1, Event: "text_delta",
		Data: map[string]interface{}{"itemId": "turn-1", "delta": "unproven overlap"},
	})
	commit, err := kernel.CommitHydrateTransaction("claude", "claude-overlap")
	if err != nil {
		t.Fatalf("commit error = %v, want pendingLive applied after baseline", err)
	}
	if commit.PendingLive != 1 {
		t.Fatalf("commit.PendingLive = %d, want 1", commit.PendingLive)
	}
	if len(commit.Projection.Turns) != 1 {
		t.Fatalf("committed turns = %d, want 1", len(commit.Projection.Turns))
	}
	if got := projectionTurnText(commit.Projection.Turns[0]); got != "baselineunproven overlap" {
		t.Fatalf("pending live delta lost or misordered after commit: %q", got)
	}
}

func projectionTurnText(turn TurnProjection) string {
	text := ""
	if turn.User != nil {
		for _, part := range turn.User.Parts {
			text += part.Text
		}
	}
	if turn.Assistant != nil {
		for _, part := range turn.Assistant.Parts {
			text += part.Text
		}
	}
	return text
}

func TestProjectionKernelPendingLiveRejectsDuplicateAndOutOfOrderInput(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	admission, err := kernel.BeginHydrateTransaction(
		"codex",
		"session-order",
		ProjectionSourceDescriptor{Identity: "session-order", Cursor: 0},
		false,
		false, false,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission = %+v err=%v", admission, err)
	}
	kernel.ApplyHydrateEvent(
		"codex", "session-order", "epoch", "turn_started",
		map[string]interface{}{"turnId": "turn-1"},
	)
	newer := EventMessage{
		EventID:       "epoch:2",
		Seq:           2,
		PerSessionSeq: 2,
		BridgeEpoch:   "epoch",
		BackendID:     "codex",
		SessionID:     "session-order",
		Event:         "text_delta",
		Data:          map[string]interface{}{"itemId": "turn-1", "delta": "once"},
	}
	kernel.IngestLive(newer)
	kernel.IngestLive(newer)
	older := newer
	older.EventID = "epoch:1"
	older.Seq = 1
	older.PerSessionSeq = 1
	older.Data = map[string]interface{}{"itemId": "turn-1", "delta": "stale"}
	kernel.IngestLive(older)

	commit, err := kernel.CommitHydrateTransaction("codex", "session-order")
	if err != nil {
		t.Fatal(err)
	}
	if got := projectionTurnText(commit.Projection.Turns[0]); got != "once" {
		t.Fatalf("duplicate/out-of-order pending input changed projection: %q", got)
	}
	if commit.Projection.SyncRev != 1 {
		t.Fatalf("projection rev = %d, want one committed mutation (turn_started no longer commits)", commit.Projection.SyncRev)
	}
}

func TestProjectionKernelBareShellWaitsForRealContent(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	if admission, err := kernel.BeginHydrateTransaction(
		"codex",
		"bare-wait",
		ProjectionSourceDescriptor{Identity: "bare-wait"},
		false,
		false, false,
	); err != nil || !admission.Leader {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	kernel.ApplyHydrateEvent(
		"codex", "bare-wait", "epoch", "turn_started",
		map[string]interface{}{"turnId": "turn-bare"},
	)
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(short, "codex", "bare-wait"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bare shell commit readiness error=%v, want deadline", err)
	}
	if _, ok := kernel.Snapshot("codex", "bare-wait"); ok {
		t.Fatal("bare shell became ready")
	}

	// §5.1 #7: content alone no longer satisfies the commit gate. The bare shell must reach a
	// terminal turn state AND the cold source must be marked fully ingested — mirroring the
	// handler path (runProjectionHydrateTransaction marks source-complete after ingest, then
	// waits). Here real content lands, then the turn is sealed, then source-complete arms the
	// gate; only then is the transaction committable.
	kernel.IngestLive(EventMessage{
		EventID:       "epoch:1",
		Seq:           1,
		PerSessionSeq: 1,
		BridgeEpoch:   "epoch",
		BackendID:     "codex",
		SessionID:     "bare-wait",
		Event:         "text_delta",
		Data:          map[string]interface{}{"itemId": "turn-bare", "delta": "content"},
	})
	kernel.IngestLive(EventMessage{
		EventID:       "epoch:2",
		Seq:           2,
		PerSessionSeq: 2,
		BridgeEpoch:   "epoch",
		BackendID:     "codex",
		SessionID:     "bare-wait",
		Event:         "turn_completed",
		Data:          map[string]interface{}{"turnId": "turn-bare"},
	})
	kernel.MarkHydrateSourceIngestComplete("codex", "bare-wait")
	if err := kernel.WaitHydrateCommitReady(context.Background(), "codex", "bare-wait"); err != nil {
		t.Fatal(err)
	}
	commit, err := kernel.CommitHydrateTransaction("codex", "bare-wait")
	if err != nil {
		t.Fatal(err)
	}
	if got := projectionTurnText(commit.Projection.Turns[0]); got != "content" {
		t.Fatalf("content did not unblock bare shell transaction: %q", got)
	}
}

func TestProjectionHydrateBareSourceReturnsHydratingNotReady(t *testing.T) {
	path := t.TempDir() + "/bare.jsonl"
	line := `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-bare"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	previousPullBudget := coldHydrateTimeout
	previousBackgroundBudget := coldHydrateBackgroundBudget
	coldHydrateTimeout = 30 * time.Millisecond
	coldHydrateBackgroundBudget = 100 * time.Millisecond
	t.Cleanup(func() {
		coldHydrateTimeout = previousPullBudget
		coldHydrateBackgroundBudget = previousBackgroundBudget
	})

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "bare-source"})
	conn := &readFileCaptureConn{}
	handlers.handleGetSessionProjection(conn, WireMessage{
		RequestID: "bare-source",
		BackendID: "codex",
		Method:    "get_session_projection",
		Params:    params,
	}, nil)
	if conn.err == nil || conn.err.Code != "projection.hydrating" {
		t.Fatalf("bare source response=%+v data=%T, want hydrating", conn.err, conn.data)
	}
	if _, ok := handlers.projectionKernel.Snapshot("codex", "bare-source"); ok {
		t.Fatal("bare source was exposed as ready")
	}
	waitForColdHydrateDrained(t, handlers, "codex", "bare-source", time.Second)
}

func TestProjectionHydrateMatchesCanonicalRecordedResult(t *testing.T) {
	path := t.TempDir() + "/recorded.jsonl"
	writeProjectionHydrateRollout(t, path, 120)
	cut, err := projectionJSONLStartCut(path)
	if err != nil {
		t.Fatal(err)
	}

	canonicalReducer := NewProjectionReducer()
	inputSeq := 0
	handlers := NewHandlers()
	err = handlers.produceProjectionHydrateRange(
		context.Background(),
		"codex",
		"recorded-canonical",
		path,
		0,
		cut,
		SessionProjection{},
		func(event projectionHydrateEvent) bool {
			inputSeq++
			canonicalReducer.Apply(EventMessage{
				BackendID:     "codex",
				SessionID:     "recorded-canonical",
				Event:         event.Event,
				Data:          event.Data,
				PerSessionSeq: inputSeq,
				BridgeEpoch:   "canonical",
			})
			return true
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	canonical, ok := canonicalReducer.Snapshot("codex", "recorded-canonical")
	if !ok {
		t.Fatal("canonical reducer produced no projection")
	}

	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "recorded-canonical"})
	conn := &readFileCaptureConn{}
	handlers.handleGetSessionProjection(conn, WireMessage{
		RequestID: "recorded-canonical",
		BackendID: "codex",
		Method:    "get_session_projection",
		Params:    params,
	}, nil)
	if conn.err != nil {
		t.Fatal(conn.err)
	}
	committed := conn.data.(map[string]interface{})["projection"].(SessionProjection)
	normalizeProjectionRuntimeFields(&canonical)
	normalizeProjectionRuntimeFields(&committed)
	if !reflect.DeepEqual(committed, canonical) {
		t.Fatalf("transaction result diverged from canonical full reduce: committed rev/turns=%d/%d canonical=%d/%d",
			committed.SyncRev, len(committed.Turns), canonical.SyncRev, len(canonical.Turns))
	}
}

func normalizeProjectionRuntimeFields(projection *SessionProjection) {
	projection.UpdatedAt = 0
	projection.BridgeEpoch = ""
	for i := range projection.Turns {
		projection.Turns[i].StartedAt = 0
		projection.Turns[i].CompletedAt = 0
	}
}

// Ready projection must catch up when the transcript source cut advances past
// the committed cursor (live relay gap / process-not-live miss).
func TestProjectionCatchUpWhenSourceAdvancesPastReady(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	const backendID = "claude"
	const sessionID = "catch-up-session"

	// First hydrate: empty source cut at 0 commits ready empty baseline.
	admission, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/unused", Cursor: 10},
		false, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Leader {
		t.Fatal("want leader for first hydrate")
	}
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message", map[string]interface{}{
		"itemId": "u1", "turnId": "u1", "text": "old",
	})
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta", map[string]interface{}{
		"itemId": "u1", "delta": "old reply",
	})
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "turn_completed", map[string]interface{}{
		"turnId": "u1", "done": true, "reason": "end_turn",
	})
	if _, err := kernel.CommitHydrateTransaction(backendID, sessionID); err != nil {
		t.Fatal(err)
	}
	if st := kernel.Status(backendID, sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("phase = %s, want ready", st.Phase)
	}

	// Same cut → AlreadyReady.
	again, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/unused", Cursor: 10},
		false, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyReady {
		t.Fatalf("same cut must be AlreadyReady, got %+v", again)
	}

	// Source advanced → catch-up leader, not AlreadyReady.
	catchUp, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/unused", Cursor: 50},
		false, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if catchUp.AlreadyReady || !catchUp.Leader {
		t.Fatalf("advanced source must force catch-up leader, got %+v", catchUp)
	}
	if catchUp.StartCursor != 10 || catchUp.StartCut != 50 {
		t.Fatalf("catch-up range = [%d,%d), want [10,50)", catchUp.StartCursor, catchUp.StartCut)
	}
	// Base must still contain the old turn.
	base, ok := kernel.HydrateSnapshot(backendID, sessionID)
	if !ok || len(base.Turns) != 1 || base.Turns[0].TurnID != "u1" {
		t.Fatalf("catch-up base turns = %+v", base.Turns)
	}
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message", map[string]interface{}{
		"itemId": "u2", "turnId": "u2", "text": "message 3",
	})
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta", map[string]interface{}{
		"itemId": "u2", "delta": "reply 3",
	})
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "turn_completed", map[string]interface{}{
		"turnId": "u2", "done": true, "reason": "end_turn",
	})
	commit, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 2 {
		t.Fatalf("after catch-up turns = %d, want 2", len(commit.Projection.Turns))
	}
	if commit.Projection.Turns[1].TurnID != "u2" {
		t.Fatalf("second turn = %+v", commit.Projection.Turns[1])
	}
}

// TestOpenCodePathlessForceRebuildOnSourceChanged: pathless Ready + sourceChanged must
// not return AlreadyReady; rebuild starts from an empty reducer so rich history is sole baseline.
func TestOpenCodePathlessForceRebuildOnSourceChanged(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	const backendID = "opencode"
	const sessionID = "oc-pathless"

	admission, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "", Cursor: 0},
		false, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Leader {
		t.Fatal("want leader")
	}
	// Live-polluted baseline: assistant only, no user (the bug shape).
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta", map[string]interface{}{
		"itemId": "u1", "delta": "reply only",
	})
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "turn_completed", map[string]interface{}{
		"turnId": "u1", "done": true,
	})
	if _, err := kernel.CommitHydrateTransaction(backendID, sessionID); err != nil {
		t.Fatal(err)
	}

	// Without sourceChanged → AlreadyReady.
	again, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "", Cursor: 0},
		false, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyReady {
		t.Fatalf("pathless same state must AlreadyReady, got %+v", again)
	}

	// With sourceChanged → force rebuild leader, empty base.
	force, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "", Cursor: 0},
		false, true, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if force.AlreadyReady || !force.Leader {
		t.Fatalf("pathless sourceChanged must force rebuild leader, got %+v", force)
	}
	// Empty tx reducer has no session row yet; first Apply creates the sole baseline.
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message", map[string]interface{}{
		"itemId": "u1", "turnId": "u1", "text": "讲个月球笑话",
	})
	kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta", map[string]interface{}{
		"itemId": "u1", "delta": "嫦娥…",
	})
	commit, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 1 || commit.Projection.Turns[0].User == nil {
		t.Fatalf("rebuilt projection missing user: %+v", commit.Projection.Turns)
	}
}

// TestPathlessRebuildDoesNotCarryCheckpointBaseline proves a pathless cold hydrate
// rebuilds the projection SOLELY from the rich-history builder and does NOT carry a
// prior committed projection as baseline. This is the b6b5a45f regression: the live
// relay commits a user turn keyed by a row-UUID id (claudeEntryTurnIdentity, used
// when message.id is empty); a later pathless reopen runs the rich-history builder,
// which re-emits the SAME content under a builder-style id (user-line-N). If the
// checkpoint baseline were carried, the reducer (upsert by turnId) could not
// reconcile the two ids → two turns with duplicated content, persisted in the
// checkpoint and served stale on every subsequent AlreadyReady reopen.
//
// Site B (checkpoint-load Restore) is the dominant real-world vector: a fresh kernel
// has no in-memory state, so the pathless reopen starts a transaction and loads the
// checkpoint. The fix makes pathless skip the baseline Restore (rich history is the
// sole baseline); the rebuild therefore yields exactly one turn.
func TestPathlessRebuildDoesNotCarryCheckpointBaseline(t *testing.T) {
	dir := t.TempDir()
	store := NewProjectionCheckpointStore(dir)
	const backendID = "claude"
	const sessionID = "b6b5a45f-pathless-dup"

	const userText = "做一个需要委派的多步任务…"
	const assistantText = "我先派出两个子 agent"

	// Phase 1 — kernel A commits a "live-style" projection: one user turn keyed by a
	// row-UUID turnId (what the live relay / claudeEntryTurnIdentity produces when the
	// transcript row's message.id is empty).
	kernelA := NewProjectionKernel(NewProjectionReducer(), store)
	if admission, err := kernelA.BeginHydrateTransaction(backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "", Cursor: 0},
		false, false, false); err != nil {
		t.Fatal(err)
	} else if !admission.Leader {
		t.Fatal("phase1: want leader")
	}
	const liveTurnID = "a09974d6-9af6-4a32-b70b-7b191889ab30" // row-UUID style
	kernelA.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message", map[string]interface{}{
		"itemId": liveTurnID, "turnId": liveTurnID, "text": userText,
	})
	kernelA.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta", map[string]interface{}{
		"itemId": liveTurnID, "delta": assistantText,
	})
	kernelA.ApplyHydrateEvent(backendID, sessionID, "epoch", "turn_completed", map[string]interface{}{
		"turnId": liveTurnID, "done": true,
	})
	commitA, err := kernelA.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(commitA.Projection.Turns) != 1 {
		t.Fatalf("phase1: want 1 turn, got %d", len(commitA.Projection.Turns))
	}

	// Phase 2 — fresh kernel B (same disk store) reopens the session. No in-memory
	// state → a pathless hydrate transaction starts and the v5 checkpoint loads.
	// The builder replay re-emits the SAME content under a builder-style id.
	kernelB := NewProjectionKernel(NewProjectionReducer(), store)
	admission2, err := kernelB.BeginHydrateTransaction(backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "", Cursor: 0},
		false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !admission2.Leader {
		t.Fatal("pathless reopen on fresh kernel must start a rebuild leader")
	}
	const builderTurnID = "user-line-16" // builder fallback id for empty message.id
	kernelB.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message", map[string]interface{}{
		"itemId": builderTurnID, "turnId": builderTurnID, "text": userText,
	})
	kernelB.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta", map[string]interface{}{
		"itemId": builderTurnID, "delta": assistantText,
	})
	kernelB.ApplyHydrateEvent(backendID, sessionID, "epoch", "turn_completed", map[string]interface{}{
		"turnId": builderTurnID, "done": true,
	})
	commitB, err := kernelB.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	// After fix: pathless rebuild started empty; the builder emitted exactly one turn
	// and the carried live row-UUID turn is gone. Before fix: baseline Restore brought
	// the liveTurnID turn forward AND the builder added builderTurnID → 2 turns.
	if got := len(commitB.Projection.Turns); got != 1 {
		t.Fatalf("pathless rebuild must yield exactly 1 turn (builder sole baseline), got %d: %+v",
			got, commitB.Projection.Turns)
	}
	if commitB.Projection.Turns[0].TurnID != builderTurnID {
		t.Fatalf("want sole builder turn %q, got %q (live baseline was carried)",
			builderTurnID, commitB.Projection.Turns[0].TurnID)
	}
}

// TestClaudeCompositeCheckpointHitRestoresBaseline_NotEmptyHead0 reproduces the
// 2026-08-01 owner regression: every Claude session opened empty ("还没有消息").
//
// Production Claude hydrate always uses CompositeRichHistoryProvider segments
// (prepareProjectionHydrateSource Path=="", Segments=[{Cursor:EOF}]). A validated
// checkpoint hit sets startCursor=source.Cursor (EOF). If Site B skips Restore
// because Path=="" is treated as pathless full rebuild, produce returns early on
// startOffset==endOffset and the committed projection is empty (headRev=0).
//
// Fix: pathlessFullRebuildSource requires Path=="" AND no Segments — composite
// Claude must Restore the checkpoint baseline when the gap is empty.
func TestClaudeCompositeCheckpointHitRestoresBaseline_NotEmptyHead0(t *testing.T) {
	dir := t.TempDir()
	store := NewProjectionCheckpointStore(dir)
	const backendID = "claude"
	const sessionID = "5054485f-composite-empty-reg"
	const cut int64 = 5641696 // matches real transcript EOF in go-bridge logs

	// Phase 1 — commit a non-empty projection + composite checkpoint at EOF.
	kernelA := NewProjectionKernel(NewProjectionReducer(), store)
	sourceAtEOF := ProjectionSourceDescriptor{
		Identity: sessionID,
		Cursor:   cut,
		Segments: []ProjectionSourceSegment{{
			Identity: sessionID,
			Path:     filepath.Join(dir, "transcript.jsonl"),
			Cursor:   cut,
		}},
	}
	// Write a real file of size `cut` so BuildProjectionSourceCheckpoints / Save work.
	// Cursor validation only needs size >= cursor; content is irrelevant for this unit.
	if err := os.WriteFile(sourceAtEOF.Segments[0].Path, make([]byte, cut), 0o600); err != nil {
		t.Fatal(err)
	}
	// For checkpoint save we need a simpler path: stage via Commit + StageCheckpoint
	// through the same helpers production uses. Here we seed via non-composite first,
	// then overwrite with composite checkpoint matching production schema.
	admission1, err := kernelA.BeginHydrateTransaction(backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: sourceAtEOF.Segments[0].Path, Cursor: cut},
		false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !admission1.Leader {
		t.Fatal("phase1 want leader")
	}
	const turnID = "turn-seed-1"
	kernelA.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message", map[string]interface{}{
		"itemId": turnID, "turnId": turnID, "text": "hello from checkpoint",
	})
	kernelA.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta", map[string]interface{}{
		"itemId": turnID, "delta": "assistant reply",
	})
	kernelA.ApplyHydrateEvent(backendID, sessionID, "epoch", "turn_completed", map[string]interface{}{
		"turnId": turnID, "done": true,
	})
	commit1, err := kernelA.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if commit1.Projection.SyncRev == 0 || len(commit1.Projection.Turns) != 1 {
		t.Fatalf("seed projection empty: rev=%d turns=%d", commit1.Projection.SyncRev, len(commit1.Projection.Turns))
	}
	// Persist composite-shaped checkpoint (sources[] at EOF) like production Claude.
	srcCkpts, err := BuildProjectionSourceCheckpoints(sourceAtEOF)
	if err != nil {
		t.Fatal(err)
	}
	ckpt := NewReadyCompositeProjectionCheckpoint(
		backendID, sessionID, srcCkpts, commit1.Projection, time.Now(),
	)
	if err := store.Save(ckpt); err != nil {
		t.Fatal(err)
	}

	// Phase 2 — fresh kernel, composite source at same EOF cut (checkpoint validates).
	kernelB := NewProjectionKernel(NewProjectionReducer(), store)
	admission2, err := kernelB.BeginHydrateTransaction(backendID, sessionID, sourceAtEOF, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !admission2.Leader {
		t.Fatal("want leader on composite reopen")
	}
	if !admission2.CheckpointHit {
		t.Fatal("want checkpoint hit at unchanged EOF cut")
	}
	if admission2.StartCursor != cut || admission2.StartCut != cut {
		t.Fatalf("want startCursor=startCut=%d (empty gap), got [%d,%d)",
			cut, admission2.StartCursor, admission2.StartCut)
	}
	// Production produce returns nil immediately when startOffset==endOffset — no events.
	// Commit must still expose the restored baseline (non-zero rev, non-empty turns).
	commit2, err := kernelB.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if commit2.Projection.SyncRev == 0 {
		t.Fatal("REGRESSION: composite checkpoint hit produced headRev=0 empty projection " +
			"(Claude sessions show 还没有消息 on iOS)")
	}
	if len(commit2.Projection.Turns) != 1 {
		t.Fatalf("want 1 restored turn, got %d", len(commit2.Projection.Turns))
	}
	if commit2.Projection.Turns[0].TurnID != turnID {
		t.Fatalf("want restored turn %q, got %q", turnID, commit2.Projection.Turns[0].TurnID)
	}
}

// TestProjectionHydrateAbortedTurnWithoutSourceCompleteNotReady is the §5.1 #7 RED baseline.
// A content-less aborted turn whose cold source is NOT yet marked fully ingested must NOT be
// committable. This encodes the boundary as a permanent contract: source-EOF is the
// authoritative readiness signal, not turn shape. Before the §5.1 #7 fix this stayed not-ready
// for the wrong reason (HasContentTurn=false on a content-less turn); after the fix it stays
// not-ready for the right reason (source not yet complete). Either way: no commit without the
// source-complete signal.
func TestProjectionHydrateAbortedTurnWithoutSourceCompleteNotReady(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	admission, err := kernel.BeginHydrateTransaction(
		"codex", "aborted-no-src",
		ProjectionSourceDescriptor{Identity: "aborted-no-src"},
		false, false, false,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	kernel.ApplyHydrateEvent("codex", "aborted-no-src", "epoch", "turn_started",
		map[string]interface{}{"turnId": "turn-aborted"})
	kernel.ApplyHydrateEvent("codex", "aborted-no-src", "epoch", "turn_aborted",
		map[string]interface{}{"turnId": "turn-aborted"})

	short, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(short, "codex", "aborted-no-src"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("aborted turn without source-complete must stay not-ready, got err=%v", err)
	}
	if _, ok := kernel.Snapshot("codex", "aborted-no-src"); ok {
		t.Fatal("aborted turn without source-complete became ready")
	}
}

// TestProjectionHydrateAbortedTurnWithSourceCompleteReady is the §5.1 #7 GREEN target. A
// content-less aborted turn that has reached a terminal state, once the cold source is fully
// ingested, IS committable. This is exactly the case the old gate (HasContentTurn) could never
// satisfy — an aborted/empty session with no assistant content would hydrate forever — and the
// reason §5.1 #7 replaces content-shape guessing with authoritative source-EOF + terminal state.
func TestProjectionHydrateAbortedTurnWithSourceCompleteReady(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	admission, err := kernel.BeginHydrateTransaction(
		"codex", "aborted-src",
		ProjectionSourceDescriptor{Identity: "aborted-src"},
		false, false, false,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	kernel.ApplyHydrateEvent("codex", "aborted-src", "epoch", "turn_started",
		map[string]interface{}{"turnId": "turn-aborted"})
	kernel.ApplyHydrateEvent("codex", "aborted-src", "epoch", "turn_aborted",
		map[string]interface{}{"turnId": "turn-aborted"})
	kernel.MarkHydrateSourceIngestComplete("codex", "aborted-src")

	if err := kernel.WaitHydrateCommitReady(context.Background(), "codex", "aborted-src"); err != nil {
		t.Fatalf("aborted terminal turn + source-complete must be ready, got err=%v", err)
	}
	commit, err := kernel.CommitHydrateTransaction("codex", "aborted-src")
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 1 {
		t.Fatalf("want 1 aborted turn committed, got %d turns", len(commit.Projection.Turns))
	}
	if got := commit.Projection.Turns[0].Status; got != "aborted" {
		t.Fatalf("aborted turn status=%q, want \"aborted\"", got)
	}
}

// TestProjectionHydrateErrorTurnWithSourceCompleteReady covers the crash sibling of the aborted
// case (§5.1 #7). A turn_error event must settle the turn to status=error and satisfy the
// commit gate once the source is fully ingested. Crash and abort are distinct terminal states;
// covering both prevents a "only aborted works" regression (the historical failure mode where
// fixing one terminal kind left the crash sibling stuck hydrating).
func TestProjectionHydrateErrorTurnWithSourceCompleteReady(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	admission, err := kernel.BeginHydrateTransaction(
		"codex", "error-src",
		ProjectionSourceDescriptor{Identity: "error-src"},
		false, false, false,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	kernel.ApplyHydrateEvent("codex", "error-src", "epoch", "turn_started",
		map[string]interface{}{"turnId": "turn-err"})
	kernel.ApplyHydrateEvent("codex", "error-src", "epoch", "turn_error",
		map[string]interface{}{"turnId": "turn-err", "message": "official compact failure"})
	kernel.MarkHydrateSourceIngestComplete("codex", "error-src")

	if err := kernel.WaitHydrateCommitReady(context.Background(), "codex", "error-src"); err != nil {
		t.Fatalf("error terminal turn + source-complete must be ready, got err=%v", err)
	}
	commit, err := kernel.CommitHydrateTransaction("codex", "error-src")
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 1 {
		t.Fatalf("want 1 error turn committed, got %d turns", len(commit.Projection.Turns))
	}
	if got := commit.Projection.Turns[0].Status; got != "error" {
		t.Fatalf("error turn status=%q, want \"error\"", got)
	}
	system := commit.Projection.Turns[0].System
	if system == nil || system.Role != "system" || len(system.Parts) != 1 || system.Parts[0].Text != "official compact failure" {
		t.Fatalf("official turn error must remain visible as system content, got %+v", system)
	}
}

// M1 (§5.1): a running Claude turn cold-opened with a LIVE source commits as an honest running
// partial once the cold source is fully ingested — no terminal event required. This is the
// §3.1 core of the SSV2 running-session cold-open fix: the commit gate releases
// (sourceIngestComplete && (all armed turns terminal || sourceIsLive)).
func TestColdHydrateRunningClaudeCommitsPartialWithRunningTurn(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	const (
		backendID = "claude"
		sessionID = "cold-live-running"
		turnID    = "turn-live"
	)
	admission, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/live.jsonl", Cursor: 10},
		false, false, true, // sourceIsLive: live process sampled at admission
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	// In-flight content: user_message + non-terminal assistant text delta (no stop_reason).
	if !kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message",
		map[string]interface{}{"turnId": turnID, "itemId": turnID, "text": "q"}) {
		t.Fatal("user_message not applied")
	}
	if !kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "text_delta",
		map[string]interface{}{"itemId": turnID, "delta": "partial answer"}) {
		t.Fatal("text_delta not applied")
	}
	kernel.MarkHydrateSourceIngestComplete(backendID, sessionID)

	short, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(short, backendID, sessionID); err != nil {
		t.Fatalf("live running turn must not gate on its terminal event, got err=%v", err)
	}
	commit, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 1 {
		t.Fatalf("committed turns = %d, want 1", len(commit.Projection.Turns))
	}
	if got := commit.Projection.Turns[0].Status; got != "running" {
		t.Fatalf("in-flight turn status=%q, want \"running\" (honest partial, not fake completion)", got)
	}
	if got := commit.Projection.Execution.Phase; got != "running" {
		t.Fatalf("execution phase=%q, want \"running\"", got)
	}
	if got := projectionTurnText(commit.Projection.Turns[0]); got != "qpartial answer" {
		t.Fatalf("partial content lost: %q", got)
	}
}

// M2 (§5.1): the running partial committed by M1 is later settled by the LIVE domain via a
// turn_completed patch — monotonic forward, no duplicate turn, status flips to completed.
func TestColdHydrateRunningTurnCompletesViaLivePatch(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	const (
		backendID = "claude"
		sessionID = "cold-live-completes"
		turnID    = "turn-live"
	)
	if _, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/live.jsonl", Cursor: 10},
		false, false, true,
	); err != nil {
		t.Fatal(err)
	}
	if !kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message",
		map[string]interface{}{"turnId": turnID, "itemId": turnID, "text": "q"}) {
		t.Fatal("user_message not applied")
	}
	kernel.MarkHydrateSourceIngestComplete(backendID, sessionID)
	if err := kernel.WaitHydrateCommitReady(context.Background(), backendID, sessionID); err != nil {
		t.Fatal(err)
	}
	commit, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	revBefore := commit.Projection.SyncRev
	if len(commit.Projection.Turns) != 1 || commit.Projection.Turns[0].Status != "running" {
		t.Fatalf("baseline commit not a running partial: %+v", commit.Projection.Turns)
	}

	// Live turn_completed settles the running partial on the committed reducer.
	kernel.IngestLive(EventMessage{
		BackendID: backendID, SessionID: sessionID, BridgeEpoch: "epoch",
		PerSessionSeq: 1, Event: "turn_completed",
		Data: map[string]interface{}{"turnId": turnID},
	})
	snap, ok := kernel.Snapshot(backendID, sessionID)
	if !ok {
		t.Fatal("no snapshot after live completion")
	}
	if len(snap.Turns) != 1 {
		t.Fatalf("live completion duplicated turns: %d, want 1", len(snap.Turns))
	}
	if got := snap.Turns[0].Status; got != "completed" {
		t.Fatalf("turn status after live patch=%q, want \"completed\"", got)
	}
	if snap.SyncRev <= revBefore {
		t.Fatalf("sync rev not monotonic: before=%d after=%d", revBefore, snap.SyncRev)
	}
}

// M3 (§5.1): a NON-live source with a non-terminal cold-armed turn must still gate — the
// §3.3 rule #2 / D6 "bare turn shell must not be treated as ready" defense is preserved.
// Only the explicit live signal (or a terminal event) releases the commit gate.
func TestColdHydrateNonLiveNonTerminalStillGates(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	const (
		backendID = "claude"
		sessionID = "cold-nonlive-running"
		turnID    = "turn-stuck"
	)
	if _, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/dead.jsonl", Cursor: 10},
		false, false, false, // sourceIsLive: false — no live process
	); err != nil {
		t.Fatal(err)
	}
	if !kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message",
		map[string]interface{}{"turnId": turnID, "itemId": turnID, "text": "q"}) {
		t.Fatal("user_message not applied")
	}
	kernel.MarkHydrateSourceIngestComplete(backendID, sessionID)

	short, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(short, backendID, sessionID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("non-live non-terminal armed turn must stay not-ready, got err=%v", err)
	}
	if _, ok := kernel.Snapshot(backendID, sessionID); ok {
		t.Fatal("non-live non-terminal turn became ready")
	}
}

// Pathless opencode-web rebuild starts from an empty tx reducer. Live
// user_message that landed before BeginHydrate is not in pendingLive, so a
// cold idle baseline would Restore over running (real device 2026-08-20
// pendingLive=0). Commit must keep the live in-flight execution.
func TestCommitHydrateDoesNotRegressLiveRunningToIdle(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	const (
		backendID = "opencode-web"
		sessionID = "ses-live-run"
		turnID    = "u1"
	)
	source := ProjectionSourceDescriptor{Identity: sessionID, Path: "", Cursor: 0}
	first, err := kernel.BeginHydrateTransaction(backendID, sessionID, source, false, false, true)
	if err != nil || !first.Leader {
		t.Fatalf("first admission=%+v err=%v", first, err)
	}
	if !kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message",
		map[string]interface{}{"turnId": turnID, "itemId": turnID, "text": "讲个猴哥语录100字左右"}) {
		t.Fatal("seed user_message not applied")
	}
	kernel.MarkHydrateSourceIngestComplete(backendID, sessionID)
	if err := kernel.WaitHydrateCommitReady(context.Background(), backendID, sessionID); err != nil {
		t.Fatalf("seed commit not ready: %v", err)
	}
	seed, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if seed.Projection.Execution.Phase != "running" {
		t.Fatalf("seed phase=%q, want running", seed.Projection.Execution.Phase)
	}

	rebuild, err := kernel.BeginHydrateTransaction(backendID, sessionID, source, false, true, true)
	if err != nil || !rebuild.Leader || rebuild.AlreadyReady {
		t.Fatalf("pathless rebuild admission=%+v err=%v", rebuild, err)
	}
	// Cold source idle: user + turn_completed (empty assistant shell) and
	// pendingLive=0 — the captured first-turn snapshot shape.
	if !kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "user_message",
		map[string]interface{}{"turnId": turnID, "itemId": turnID, "text": "讲个猴哥语录100字左右"}) {
		t.Fatal("cold user_message not applied")
	}
	if !kernel.ApplyHydrateEvent(backendID, sessionID, "epoch", "turn_completed",
		map[string]interface{}{"turnId": turnID, "done": true, "reason": "rich_history"}) {
		t.Fatal("cold turn_completed not applied")
	}
	kernel.MarkHydrateSourceIngestComplete(backendID, sessionID)
	if err := kernel.WaitHydrateCommitReady(context.Background(), backendID, sessionID); err != nil {
		t.Fatalf("rebuild not ready: %v", err)
	}
	commit, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if commit.PendingLive != 0 {
		t.Fatalf("pendingLive=%d, want 0", commit.PendingLive)
	}
	if commit.Projection.Execution.Phase != "running" {
		t.Fatalf("hydrate must not Restore idle over live running, phase=%q", commit.Projection.Execution.Phase)
	}
	if commit.Projection.Execution.ActiveTurnID != turnID {
		t.Fatalf("activeTurnId=%q, want %s", commit.Projection.Execution.ActiveTurnID, turnID)
	}
}
