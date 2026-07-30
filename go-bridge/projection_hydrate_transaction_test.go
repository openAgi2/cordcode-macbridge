package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
		"claudecode", source.Identity, source, false, false,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("initial admission = %+v err=%v", admission, err)
	}
	if _, err := kernel.CommitHydrateTransaction("claudecode", source.Identity); err != nil {
		t.Fatal(err)
	}

	unchanged, err := kernel.BeginHydrateTransaction(
		"claudecode", source.Identity, source, false, false,
	)
	if err != nil || !unchanged.AlreadyReady {
		t.Fatalf("unchanged source must reuse Ready state: %+v err=%v", unchanged, err)
	}

	advanced := cloneProjectionSourceDescriptor(source)
	advanced.Segments[1].Cursor = 15
	advanced.Cursor = 25
	changed, err := kernel.BeginHydrateTransaction(
		"claudecode", source.Identity, advanced, false, false,
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
		"codex", "session-growing", source, false, false,
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
	if len(commit.Projection.Turns) != 2 || commit.Projection.SyncRev != 8 {
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

func TestClaudeProjectionHydrateRejectsUncorrelatedPendingLive(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	source := ProjectionSourceDescriptor{
		Identity: "claude-overlap", Path: "/private/source.jsonl", Cursor: 100,
	}
	if _, err := kernel.BeginHydrateTransaction(
		"claude", "claude-overlap", source, false, false,
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
	if _, err := kernel.CommitHydrateTransaction("claude", "claude-overlap"); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("commit error = %v, want explicit uncorrelated-overlap failure", err)
	}
	if projection, ok := kernel.Snapshot("claude", "claude-overlap"); ok {
		t.Fatalf("failed overlap commit exposed projection: %+v", projection)
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
		false,
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
	if commit.Projection.SyncRev != 2 {
		t.Fatalf("projection rev = %d, want two committed mutations", commit.Projection.SyncRev)
	}
}

func TestProjectionKernelBareShellWaitsForRealContent(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	if admission, err := kernel.BeginHydrateTransaction(
		"codex",
		"bare-wait",
		ProjectionSourceDescriptor{Identity: "bare-wait"},
		false,
		false,
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
		false, false,
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
		false, false,
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
		false, false,
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
		false, false,
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
		false, false,
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
		false, true,
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
