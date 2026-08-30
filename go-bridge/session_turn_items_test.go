package gobridge

// session_turn_items_test.go covers the T2.2 kernel primitives (detail merge,
// state-only commits, orphan recovery) and the T2.3 handler against the frozen
// §11.7 contract: capability + per-session legacy gating, Kernel-adjudicated
// turn_not_found, loading→terminal state machine, atomic merge with Summary
// preservation on failure, resource-gate reasonCodes, idempotent repeats,
// singleflight, and orphan loading recovery.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// turnDetailAgent fakes the codex-remote surfaces the lazy-history paths touch:
// ColdHistoryReader (mode-aware cold open), UpstreamHistoryPager (older walk),
// TurnDetailReader (session_turn_items fetch).
type turnDetailAgent struct {
	*fakeAgent
	cold      *core.ColdHistoryResult
	detail    core.TurnScopedHistoryTurn
	detailErr error
	fetches   int64 // atomic fetch counter
	gate      chan struct{}
	coldGate  chan struct{} // blocks ReadColdHistory when set
	walkGate  chan struct{} // blocks cursor != "" upstream pages when set
	pages     map[string]*core.UpstreamHistoryPage
	walkFetch int64 // atomic cursor-page counter
}

func (a *turnDetailAgent) ReadColdHistory(ctx context.Context, sessionID string) (*core.ColdHistoryResult, error) {
	if a.coldGate != nil {
		<-a.coldGate
	}
	return a.cold, nil
}

func (a *turnDetailAgent) ReadUpstreamHistoryPage(ctx context.Context, sessionID, cursor string) (*core.UpstreamHistoryPage, error) {
	if cursor != "" {
		atomic.AddInt64(&a.walkFetch, 1)
		if a.walkGate != nil {
			<-a.walkGate
		}
	}
	if page, ok := a.pages[cursor]; ok {
		return page, nil
	}
	return &core.UpstreamHistoryPage{}, nil
}

func (a *turnDetailAgent) ReadTurnDetail(ctx context.Context, sessionID, turnID string) (core.TurnScopedHistoryTurn, error) {
	atomic.AddInt64(&a.fetches, 1)
	if a.gate != nil {
		<-a.gate
	}
	if a.detailErr != nil {
		return core.TurnScopedHistoryTurn{}, a.detailErr
	}
	turn := a.detail
	turn.TurnID = turnID
	return turn, nil
}

func turnDetailHarness(t *testing.T, detail core.TurnScopedHistoryTurn) (*Handlers, *olderWalkConn, string, *turnDetailAgent) {
	t.Helper()
	// TempDir registers its cleanup FIRST so it runs LAST (LIFO) — the
	// handlers Shutdown (registered after) must quiesce the hydrate runner's
	// post-commit checkpoint writes BEFORE the data dir disappears.
	dataDir := t.TempDir()
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	h.SetDataDir(dataDir)
	agent := &turnDetailAgent{
		fakeAgent: &fakeAgent{name: "codex-remote"},
		cold: &core.ColdHistoryResult{HistoryMode: "paginated", Page: &core.UpstreamHistoryPage{
			Turns: []core.TurnScopedHistoryTurn{
				{TurnID: "T1", Status: "completed", UserItemID: "u1", UserText: "q1",
					Parts: []map[string]any{{"type": "text", "content": "summary answer", "itemId": "asst1"}}},
			},
			NextCursor: "",
		}},
		detail: detail,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"codex-remote": agent}
	h.mu.Unlock()
	conn := &olderWalkConn{}
	h.eventPublisher.SetConnSyncV2(conn, true)
	h.eventPublisher.SetConnProjectionWindowV1(conn, true)
	h.eventPublisher.SetConnTurnDetailV1(conn, true)
	return h, conn, "sess", agent
}

func turnItemsDispatch(t *testing.T, h *Handlers, conn *olderWalkConn, sessionID, turnID string) (*WireError, map[string]any) {
	conn.mu.Lock()
	conn.err, conn.data = nil, nil
	conn.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"sessionId": sessionID, "turnId": turnID})
	msg := WireMessage{RequestID: "r-items", BackendID: "codex-remote", Method: "session_turn_items", Params: raw}
	h.handleSessionTurnItems(conn, msg, nil)
	quiesceProjectionWrites(t, h)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.err != nil {
		return conn.err, nil
	}
	encoded, _ := json.Marshal(conn.data)
	var decoded map[string]any
	_ = json.Unmarshal(encoded, &decoded)
	return nil, decoded
}

func detailTurnFixture() core.TurnScopedHistoryTurn {
	return core.TurnScopedHistoryTurn{
		TurnID: "T1", Status: "completed",
		Parts: []map[string]any{
			{"type": "reasoning", "content": "thought summary", "itemId": "r1"},
			{"type": "tool", "step": map[string]any{"id": "c1", "toolName": "Bash", "status": "completed"}, "itemId": "c1"},
			{"type": "text", "content": "final answer", "itemId": "asst1"},
		},
	}
}

// ---------- T2.2 kernel primitive ----------

func TestMergeHistoricalTurnDetailAtomicCommit(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	if wireErr, _ := olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	}); wireErr != nil {
		t.Fatalf("window_0: %+v", wireErr)
	}
	quiesceProjectionWrites(t, h)
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	before := proj.SyncRev

	mapped := upstreamSummaryTurnsToProjection([]core.TurnScopedHistoryTurn{detailTurnFixture()})
	parts := mapped[0].Assistant.Parts
	committed, patches, err := h.projectionKernel.MergeHistoricalTurnDetail("codex-remote", sessionID, "T1", 0, parts)
	if err != nil {
		t.Fatal(err)
	}
	if committed.SyncRev != before+1 {
		t.Fatalf("rev %d want %d", committed.SyncRev, before+1)
	}
	turn := committed.Turns[0]
	if turn.DetailLoadState != DetailStateLoaded || turn.DetailReasonCode != "" {
		t.Fatalf("terminal state = %s/%s", turn.DetailLoadState, turn.DetailReasonCode)
	}
	if turn.TurnGeneration != 1 {
		t.Fatalf("generation must bump on the content mutation: %d", turn.TurnGeneration)
	}
	if len(turn.Assistant.Parts) != 3 || turn.Assistant.Parts[0].ItemID != "r1" {
		t.Fatalf("detail parts = %+v", turn.Assistant.Parts)
	}
	if turn.User == nil || turn.User.Parts[0].Text != "q1" {
		t.Fatalf("user slot must stay untouched: %+v", turn.User)
	}
	// Steady state (no staged live delta): ONE commit patch, both ops, frozen
	// shapes: replace_parts + loaded at one syncRev. (The staged-live prefix
	// variant is covered by TestTurnItemsDetailCommitFlushesPendingLiveText.)
	if len(patches) != 1 {
		t.Fatalf("patch chain = %+v, want exactly the commit patch", patches)
	}
	patch := patches[len(patches)-1]
	if patch.BaseRev != before || patch.SyncRev != before+1 {
		t.Fatalf("patch revs %d→%d", patch.BaseRev, patch.SyncRev)
	}
	if len(patch.PartOps) != 1 || patch.PartOps[0].Op != "replace_parts" || patch.PartOps[0].TurnID != "T1" {
		t.Fatalf("partOps = %+v", patch.PartOps)
	}
	if len(patch.TurnStateOps) != 1 || patch.TurnStateOps[0].DetailLoadState != DetailStateLoaded ||
		patch.TurnStateOps[0].TurnGeneration != 1 {
		t.Fatalf("turnStateOps = %+v", patch.TurnStateOps)
	}
}

func TestMergeHistoricalTurnDetailFencesAndEmptyDetail(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	// Unknown turn → typed missing, kernel untouched.
	if _, _, err := h.projectionKernel.MergeHistoricalTurnDetail("codex-remote", sessionID, "nope", 0, nil); !errors.Is(err, ErrDetailTargetMissing) {
		t.Fatalf("err = %v", err)
	}
	// Stale generation → typed stale, kernel untouched.
	if _, _, err := h.projectionKernel.MergeHistoricalTurnDetail("codex-remote", sessionID, "T1", 7, nil); !errors.Is(err, ErrTurnStateStale) {
		t.Fatalf("err = %v", err)
	}

	// 空明细也是 loaded：state-only commit, Summary parts preserved.
	h2, conn2, sessionID2, _ := turnDetailHarness(t, core.TurnScopedHistoryTurn{})
	olderWalkDispatch(h2, conn2, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID2, "limit": 10,
	})
	committed, patches, err := h2.projectionKernel.MergeHistoricalTurnDetail("codex-remote", sessionID2, "T1", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Turns[0].DetailLoadState != DetailStateLoaded {
		t.Fatalf("empty detail must still be loaded: %+v", committed.Turns[0])
	}
	if committed.Turns[0].Assistant == nil || committed.Turns[0].Assistant.Parts[0].Text != "summary answer" {
		t.Fatalf("empty detail must preserve Summary parts: %+v", committed.Turns[0].Assistant)
	}
	if commit := patches[len(patches)-1]; len(commit.PartOps) != 0 {
		t.Fatalf("empty detail patch must be state-only: %+v", commit.PartOps)
	}
}

// The running fence, reducer-level: the reducer never un-completes a settled
// turn, so the fenced state is only reachable through the live lifecycle —
// build it with events (snapshot clones don't mutate the Kernel and Restore
// heals zombie running turns back).
func TestMergeHistoricalTurnDetailRunningFence(t *testing.T) {
	r := NewProjectionReducer()
	r.Apply(projectionReducerEvent("codex-remote", "sess", "turn_started",
		map[string]interface{}{"turnId": "T1"}, 1, "epoch"))
	if _, _, err := r.MergeHistoricalTurnDetail("codex-remote", "sess", "T1", 0, nil); !errors.Is(err, ErrDetailTargetRunning) {
		t.Fatalf("err = %v", err)
	}
}

func TestCommitTurnStateOpsAtomicAndGated(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)

	// Validation failure consumes no rev and mutates nothing.
	bad := []TurnStateOp{{TurnID: "T1", DetailLoadState: DetailStateFailed, ReasonCode: "not-in-set", TurnGeneration: 0}}
	if _, _, err := h.projectionKernel.CommitTurnStateOps("codex-remote", sessionID, bad); !errors.Is(err, ErrTurnStateInvalid) {
		t.Fatalf("err = %v", err)
	}
	after, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if after.SyncRev != proj.SyncRev {
		t.Fatalf("failed commit consumed a rev: %d", after.SyncRev)
	}

	// Loading commit: state-only patch shape.
	_, patches, err := h.projectionKernel.CommitTurnStateOps("codex-remote", sessionID, []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoading, TurnGeneration: 0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 1 {
		t.Fatalf("steady-state chain = %+v, want exactly the commit patch", patches)
	}
	patch := patches[len(patches)-1]
	if len(patch.TurnStateOps) != 1 || patch.TurnStateOps[0].DetailLoadState != DetailStateLoading {
		t.Fatalf("patch = %+v", patch.TurnStateOps)
	}
	if len(patch.PartOps) != 0 || len(patch.UpsertTurns) != 0 {
		t.Fatalf("state commit must be state-only: %+v", patch)
	}

	// Stale fence: generation moved by the detail merge.
	if _, _, err := h.projectionKernel.MergeHistoricalTurnDetail("codex-remote", sessionID, "T1", 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.projectionKernel.CommitTurnStateOps("codex-remote", sessionID, []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoading, TurnGeneration: 0,
	}}); !errors.Is(err, ErrTurnStateStale) {
		t.Fatalf("err = %v", err)
	}
}

func TestRecoverOrphanDetailLoading(t *testing.T) {
	proj := SessionProjection{SessionID: "s", SyncRev: 5, Turns: []TurnProjection{
		{TurnID: "T1", Status: "completed", DetailLoadState: DetailStateLoading, TurnGeneration: 2},
		{TurnID: "T2", Status: "completed", DetailLoadState: DetailStateLoaded, TurnGeneration: 1},
	}}
	if !RecoverOrphanDetailLoading(&proj) {
		t.Fatal("orphan loading must be recovered")
	}
	if proj.Turns[0].DetailLoadState != DetailStateFailed || proj.Turns[0].DetailReasonCode != "interrupted" {
		t.Fatalf("recovered turn = %+v", proj.Turns[0])
	}
	if proj.Turns[1].DetailLoadState != DetailStateLoaded {
		t.Fatalf("loaded turn disturbed: %+v", proj.Turns[1])
	}
	if proj.SyncRev != 6 {
		t.Fatalf("recovery must bump the rev (journal gap): %d", proj.SyncRev)
	}
	again := cloneSessionProjection(proj)
	if RecoverOrphanDetailLoading(&again) {
		t.Fatal("no loading turns → no recovery")
	}
}

// ---------- T2.3 handler (§11.7) ----------

func TestSessionTurnItemsCapabilityAndAdjudicationGates(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	// Capability not negotiated on this conn.
	h.eventPublisher.SetConnTurnDetailV1(conn, false)
	if wireErr, _ := turnItemsDispatch(t, h, conn, sessionID, "T1"); wireErr == nil || wireErr.Code != "protocol.capability_required" {
		t.Fatalf("err = %+v", wireErr)
	}
	h.eventPublisher.SetConnTurnDetailV1(conn, true)

	// turn_not_found is Kernel-adjudicated.
	if wireErr, _ := turnItemsDispatch(t, h, conn, sessionID, "nope"); wireErr == nil || wireErr.Code != "turn_not_found" {
		t.Fatalf("err = %+v", wireErr)
	}
}

func TestSessionTurnItemsLegacyModeGate(t *testing.T) {
	h, conn, sessionID, agent := turnDetailHarness(t, detailTurnFixture())
	agent.cold = &core.ColdHistoryResult{HistoryMode: "legacy", Page: &core.UpstreamHistoryPage{
		Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1", Status: "completed", UserText: "q1", Parts: []map[string]any{{"type": "text", "content": "a1"}}},
		},
	}}
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	if wireErr, _ := turnItemsDispatch(t, h, conn, sessionID, "T1"); wireErr == nil || wireErr.Code != "unsupported_capability" {
		t.Fatalf("legacy session must not expose session_turn_items: %+v", wireErr)
	}
}

func TestSessionTurnItemsHappyPathAndIdempotentRepeat(t *testing.T) {
	h, conn, sessionID, agent := turnDetailHarness(t, detailTurnFixture())
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	wireErr, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if ack["detailLoadState"] != "loaded" {
		t.Fatalf("ack = %+v", ack)
	}
	loadedRev := int(ack["syncRev"].(float64))
	if loadedRev <= 0 {
		t.Fatalf("ack syncRev = %v", ack["syncRev"])
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	turn := proj.Turns[0]
	if turn.DetailLoadState != DetailStateLoaded || len(turn.Assistant.Parts) != 3 {
		t.Fatalf("kernel turn = %+v", turn)
	}
	// The requester received the commit patches (rule 1: appliedRev >= ack.syncRev).
	patchSeen := false
	conn.mu.Lock()
	frames := append([]interface{}(nil), conn.frames...)
	conn.mu.Unlock()
	for _, frame := range frames {
		if msg, ok := frame.(EventMessage); ok && msg.Event == "projection_patch" {
			patchSeen = true
		}
	}
	if !patchSeen {
		t.Fatal("requester never received the detail commit patches")
	}

	// Idempotent repeat: loaded ack, ORIGINAL commit rev, no refetch.
	if got := atomic.LoadInt64(&agent.fetches); got != 1 {
		t.Fatalf("fetches = %d", got)
	}
	_, repeat := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if repeat["detailLoadState"] != "loaded" || int(repeat["syncRev"].(float64)) != loadedRev {
		t.Fatalf("repeat ack = %+v (want loaded @%d)", repeat, loadedRev)
	}
	if got := atomic.LoadInt64(&agent.fetches); got != 1 {
		t.Fatalf("repeat refetched: %d", got)
	}
}

func TestSessionTurnItemsFailureKeepsSummaryAndReasonCodes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		reasonCode string
	}{
		{"unknown item", codexremote.ErrUnknownThreadItem, "unsupported_item_type"},
		{"foreign item", codexremote.ErrForeignTurnItem, "unsupported_item_type"},
		{"max bytes", codexremote.ErrTurnItemsMaxBytes, "max_bytes"},
		{"max pages", codexremote.ErrTurnItemsMaxPages, "max_pages"},
		{"timeout", codexremote.ErrTurnItemsTimeout, "timeout"},
		{"generic", errors.New("wss broke"), "upstream_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, conn, sessionID, agent := turnDetailHarness(t, detailTurnFixture())
			agent.detailErr = tc.err
			olderWalkDispatch(h, conn, map[string]any{
				"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
			})
			wireErr, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
			if wireErr != nil {
				t.Fatalf("process failures are success-shaped: %+v", wireErr)
			}
			if ack["detailLoadState"] != "failed" || ack["reasonCode"] != tc.reasonCode {
				t.Fatalf("ack = %+v", ack)
			}
			proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
			turn := proj.Turns[0]
			if turn.DetailLoadState != DetailStateFailed || turn.DetailReasonCode != tc.reasonCode {
				t.Fatalf("terminal state = %+v", turn)
			}
			// Summary parts survive atomically — no partial replace.
			if len(turn.Assistant.Parts) != 1 || turn.Assistant.Parts[0].Text != "summary answer" {
				t.Fatalf("summary parts disturbed: %+v", turn.Assistant.Parts)
			}
		})
	}
}

func TestSessionTurnItemsSkippedTypesFailsAtomically(t *testing.T) {
	detail := detailTurnFixture()
	detail.SkippedTypes = []string{"futureItem"}
	h, conn, sessionID, _ := turnDetailHarness(t, detail)
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	_, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if ack["detailLoadState"] != "failed" || ack["reasonCode"] != "unsupported_item_type" {
		t.Fatalf("ack = %+v", ack)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if len(proj.Turns[0].Assistant.Parts) != 1 || proj.Turns[0].Assistant.Parts[0].Text != "summary answer" {
		t.Fatalf("summary parts disturbed: %+v", proj.Turns[0].Assistant.Parts)
	}
}

func TestSessionTurnItemsEmptyDetailIsLoaded(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, core.TurnScopedHistoryTurn{Status: "completed"})
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	wireErr, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if wireErr != nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("ack = %+v err=%+v", ack, wireErr)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	turn := proj.Turns[0]
	if turn.DetailLoadState != DetailStateLoaded {
		t.Fatalf("turn = %+v", turn)
	}
	if turn.Assistant == nil || turn.Assistant.Parts[0].Text != "summary answer" {
		t.Fatalf("empty detail must preserve Summary parts: %+v", turn.Assistant)
	}
}

func TestSessionTurnItemsSingleFlight(t *testing.T) {
	h, conn, sessionID, agent := turnDetailHarness(t, detailTurnFixture())
	gate := make(chan struct{})
	agent.gate = gate // block inside ReadTurnDetail until released
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	const followers = 4
	results := make([]map[string]any, followers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < followers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
			results[i] = ack
		}(i)
	}
	close(start)
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	for i, ack := range results {
		if ack == nil || ack["detailLoadState"] != "loaded" {
			t.Fatalf("follower %d ack = %+v", i, ack)
		}
	}
}

func TestSessionTurnItemsOrphanLoadingRecovery(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	// Simulate a crashed leader: loading committed, no in-flight flight.
	if _, _, err := h.projectionKernel.CommitTurnStateOps("codex-remote", sessionID, []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoading, TurnGeneration: 0,
	}}); err != nil {
		t.Fatal(err)
	}
	wireErr, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if wireErr != nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("ack = %+v err=%+v (orphan must recover then succeed)", ack, wireErr)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if proj.Turns[0].DetailLoadState != DetailStateLoaded {
		t.Fatalf("final state = %+v", proj.Turns[0])
	}
}

// quiesceProjectionWrites waits until the hydrate runner's post-Done writes
// (producer seed, checkpoint save) settle, so a subtest's TempDir cleanup never
// races them (Shutdown does not join the runner goroutine).
func quiesceProjectionWrites(t *testing.T, h *Handlers) {
	t.Helper()
	h.mu.Lock()
	dataDir := h.dataDir
	h.mu.Unlock()
	dir := filepath.Join(dataDir, "session-projection", "checkpoints")
	snapshot := func() string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "err"
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			info, infoErr := e.Info()
			if infoErr == nil {
				names = append(names, fmt.Sprintf("%s:%d", e.Name(), info.ModTime().UnixNano()))
			}
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}
	deadline := time.Now().Add(2 * time.Second)
	prev := snapshot()
	for time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
		cur := snapshot()
		if cur == prev && cur != "" && !strings.Contains(cur, ".tmp") {
			return
		}
		prev = cur
	}
}
