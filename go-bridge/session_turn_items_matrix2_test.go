package gobridge

// session_turn_items_matrix2_test.go is the reopened-G2 supplement to the T2.5
// matrix (owner 2026-08-30 Phase-2 closure review): the concurrency and
// disconnect scenarios the first matrix missed —
//   - older-window hydration racing a gated detail load (prepend commit vs
//     per-turn generation fence, one contiguous wire chain for the window conn);
//   - leader/follower survival across requester disconnect (the fetch is
//     bridge-owned, not conn-bound);
//   - typed stale at merge time (turn re-activated mid-flight; concurrent
//     writer already merged — its truth wins);
//   - Summary cold hydrate racing a live turn completion (pendingLive replay
//     keeps the live turn exactly once);
//   - the loaded-repeat watermark's conservative fallback.
// The live-patch-vs-detail-commit delivery and window-fence races live in
// projection_detail_concurrency_test.go (P0-1/P0-2).

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (a *turnDetailAgent) walkStarted(t *testing.T) chan struct{} {
	t.Helper()
	started := make(chan struct{}, 1)
	go func() {
		for atomic.LoadInt64(&a.walkFetch) == 0 {
			time.Sleep(time.Millisecond)
		}
		started <- struct{}{}
	}()
	return started
}

// Older-window hydration (a prepend commit + a new held-turn registration)
// racing a gated detail load on the same session: both commits land, the
// per-turn fence keeps them independent, and the window connection's wire
// chain stays contiguous (loading → prepend no-op → detail content).
func TestTurnItemsOlderHydrateRacesDetailLoad(t *testing.T) {
	h, conn, sessionID, agent := twoTurnDetailHarness(t)
	agent.pages = map[string]*core.UpstreamHistoryPage{
		"c1": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "O1", Status: "completed", UserText: "oq1",
				Parts: []map[string]any{{"type": "text", "content": "oa1"}}},
		}, NextCursor: ""},
	}
	agent.cold.Page.NextCursor = "c1"
	detailGate := make(chan struct{})
	agent.gate = detailGate
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	waitForCodexProducerState(t, h, sessionID)
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	cutBefore := proj.SyncRev
	framesBefore := connFrameCount(conn)

	// Leader admitted (loading committed) then gated inside the fetch.
	detailDone := make(chan map[string]any, 1)
	go func() {
		_, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
		detailDone <- ack
	}()
	<-agent.fetchStarted(t)

	// Concurrent older-walk hydration prepends O1 while T1's fetch is gated.
	agent.walkGate = make(chan struct{})
	walkDone := make(chan struct{}, 1)
	go func() {
		proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
		cursor := encodeProjectionWindowCursor(projectionWindowCursor{
			V: 1, BridgeEpoch: h.eventPublisher.BridgeEpoch(), BackendID: "codex-remote",
			SessionID: sessionID, AnchorTurnID: proj.Turns[0].TurnID, Side: "o",
		})
		olderWalkDispatch(h, conn, map[string]any{
			"direction": "older", "backendId": "codex-remote", "sessionId": sessionID,
			"cursor": cursor, "limit": 10,
		})
		walkDone <- struct{}{}
	}()
	<-agent.walkStarted(t)
	close(agent.walkGate)
	<-walkDone
	quiesceProjectionWrites(t, h)

	// The prepend did NOT stale the gated detail fetch (per-turn fence).
	close(detailGate)
	ack := <-detailDone
	if ack == nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("detail ack after concurrent prepend = %+v", ack)
	}

	after, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if got := projTurnIDs(after); len(got) != 3 || got[0] != "O1" || got[1] != "T1" || got[2] != "T2" {
		t.Fatalf("kernel order after race = %v", got)
	}
	if after.Turns[1].DetailLoadState != DetailStateLoaded || len(after.Turns[1].Assistant.Parts) != 3 {
		t.Fatalf("T1 after race = %+v", after.Turns[1])
	}
	// Window conn wire invariant: every patch strictly advances the chain, the
	// first bases exactly at the conn's pre-race cut, and the final detail
	// commit lands at the acked rev. (A prepend no-op is intentionally absent:
	// R11b exempts THIS walk's requester — the O1 page rides its window result,
	// which advances the cut across the response boundary by design.)
	patches := connPatches(t, conn, framesBefore)
	if len(patches) < 2 {
		t.Fatalf("window conn received %d patches, want loading+detail: %+v", len(patches), patches)
	}
	for _, patch := range patches {
		if patch.SyncRev <= patch.BaseRev {
			t.Fatalf("zero-span patch: %d→%d", patch.BaseRev, patch.SyncRev)
		}
	}
	if patches[0].BaseRev != cutBefore {
		t.Fatalf("first patch bases at %d, want the pre-race cut %d", patches[0].BaseRev, cutBefore)
	}
	detailContent := false
	for _, patch := range patches {
		for _, op := range patch.PartOps {
			if op.Op == "replace_parts" && op.TurnID == "T1" {
				detailContent = true
			}
		}
	}
	if !detailContent {
		t.Fatalf("window conn never received the detail content after the race: %+v", patches)
	}
	if last := patches[len(patches)-1]; int(ack["syncRev"].(float64)) != last.SyncRev {
		t.Fatalf("final patch %d != ack syncRev %v", last.SyncRev, ack["syncRev"])
	}
}

// The detail fetch is bridge-owned: the requester connection vanishing
// mid-flight (disconnect, app kill) neither cancels the load nor loses the
// commit — the kernel reaches loaded and every other connection holding the
// turn receives the content patch.
func TestTurnItemsLeaderSurvivesRequesterDisconnect(t *testing.T) {
	h, connB, sessionID, agent := twoTurnDetailHarness(t)
	olderWalkDispatch(h, connB, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	framesBeforeB := connFrameCount(connB)

	gone := &olderWalkConn{}
	h.eventPublisher.broadcaster.RegisterConn(gone)
	h.eventPublisher.broadcaster.Subscribe(gone, SubscriptionKey{BackendID: "codex-remote", SessionID: sessionID})
	h.eventPublisher.SetConnSyncV2(gone, true)
	h.eventPublisher.SetConnTurnDetailV1(gone, true)

	detailGate := make(chan struct{})
	agent.gate = detailGate
	done := make(chan map[string]any, 1)
	go func() {
		_, ack := turnItemsDispatch(t, h, gone, sessionID, "T1")
		done <- ack
	}()
	<-agent.fetchStarted(t)
	// Requester disconnects while the leader's fetch is in flight.
	_ = gone.Close()

	close(detailGate)
	ack := <-done
	if ack == nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("leader must finish the load: %+v", ack)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if proj.Turns[0].DetailLoadState != DetailStateLoaded {
		t.Fatalf("kernel state = %+v", proj.Turns[0])
	}
	if got := connPatches(t, connB, framesBeforeB); len(got) == 0 {
		t.Fatal("window conn received nothing for the orphaned leader's commit")
	}
}

// Typed stale at merge time: the target turn re-activates (live lifecycle
// wins) while its detail fetch is gated — the merge refuses (new truth kept),
// the request ends failed/stale_turn, and the Summary parts are untouched.
func TestTurnItemsStaleWhenTurnReactivatedMidFlight(t *testing.T) {
	h, conn, sessionID, agent := twoTurnDetailHarness(t)
	detailGate := make(chan struct{})
	agent.gate = detailGate
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	done := make(chan map[string]any, 1)
	go func() {
		_, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
		done <- ack
	}()
	<-agent.fetchStarted(t)
	h.projectionKernel.reducer.Apply(projectionReducerEvent(
		"codex-remote", sessionID, "turn_started",
		map[string]interface{}{"turnId": "T1"}, 600, "epoch"))
	close(detailGate)

	ack := <-done
	if ack == nil || ack["detailLoadState"] != "failed" || ack["reasonCode"] != "stale_turn" {
		t.Fatalf("reactivation must end failed/stale_turn: %+v", ack)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	turn := proj.Turns[0]
	if turn.DetailLoadState != DetailStateFailed || turn.DetailReasonCode != "stale_turn" {
		t.Fatalf("terminal state = %s/%s", turn.DetailLoadState, turn.DetailReasonCode)
	}
	if turn.Status != "running" {
		t.Fatalf("live re-activation is the new truth: status = %s", turn.Status)
	}
	if len(turn.Assistant.Parts) != 1 || turn.Assistant.Parts[0].Text != "a1" {
		t.Fatalf("Summary parts must stay untouched: %+v", turn.Assistant.Parts)
	}
}

// Concurrent writer already merged the detail while this request's fetch was
// gated: the generation fence refuses the second merge and the loser ACKS THE
// WINNER (loaded at the winner's rev) — exactly one detail commit in the
// kernel, one fetch over the wire.
func TestTurnItemsConcurrentMergeWinsAndLoserAcksWinner(t *testing.T) {
	h, conn, sessionID, agent := twoTurnDetailHarness(t)
	detailGate := make(chan struct{})
	agent.gate = detailGate
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	done := make(chan map[string]any, 1)
	go func() {
		_, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
		done <- ack
	}()
	<-agent.fetchStarted(t)
	// The concurrent writer merges the same detail (bumps T1's generation).
	mapped := upstreamSummaryTurnsToProjection([]core.TurnScopedHistoryTurn{detailTurnFixture()})
	if _, _, err := h.projectionKernel.MergeHistoricalTurnDetail(
		"codex-remote", sessionID, "T1", 0, mapped[0].Assistant.Parts); err != nil {
		t.Fatal(err)
	}
	close(detailGate)

	ack := <-done
	if ack == nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("loser must ack the winner's loaded state: %+v", ack)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	turn := proj.Turns[0]
	if turn.TurnGeneration != 1 || turn.DetailLoadState != DetailStateLoaded {
		t.Fatalf("exactly one merge must survive: gen=%d state=%s", turn.TurnGeneration, turn.DetailLoadState)
	}
	if got := atomic.LoadInt64(&agent.fetches); got != 1 {
		t.Fatalf("fetches = %d", got)
	}
}

// Summary cold hydrate racing a live turn completion: the live turn rides the
// hydrate transaction's pendingLive replay and survives the commit exactly
// once, in order, next to the page turns — and the first window response
// already reflects both.
func TestColdHydrateRacesLiveCompletion(t *testing.T) {
	h, conn, sessionID, agent := turnDetailHarness(t, detailTurnFixture())
	coldGate := make(chan struct{})
	agent.coldGate = coldGate

	windowDone := make(chan struct{}, 1)
	go func() {
		olderWalkDispatch(h, conn, map[string]any{
			"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
		})
		windowDone <- struct{}{}
	}()
	// The cold read is gated: while the Summary page is in flight, a live turn
	// starts and completes through the normal publisher path.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if status := h.projectionKernel.Status("codex-remote", sessionID); status.Phase == ProjectionHydrateHydrating {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	for _, logical := range []LogicalEvent{
		{BackendID: "codex-remote", SessionID: sessionID, Event: "turn_started",
			Data: map[string]interface{}{"turnId": "TL"}},
		{BackendID: "codex-remote", SessionID: sessionID, Event: "user_message",
			Data: map[string]interface{}{"itemId": "tl-user", "turnId": "TL", "text": "live q"}},
		{BackendID: "codex-remote", SessionID: sessionID, Event: "text_delta",
			Data: map[string]interface{}{"itemId": "TL", "delta": "live a"}},
		{BackendID: "codex-remote", SessionID: sessionID, Event: "turn_completed",
			Data: map[string]interface{}{"turnId": "TL"}},
	} {
		h.eventPublisher.PublishLogical(logical)
	}
	close(coldGate)
	<-windowDone
	quiesceProjectionWrites(t, h)

	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if got := projTurnIDs(proj); len(got) != 2 || got[0] != "T1" || got[1] != "TL" {
		t.Fatalf("hydrate+live order = %v", got)
	}
	live := proj.Turns[1]
	if live.Status != "completed" {
		t.Fatalf("live turn status = %s", live.Status)
	}
	if live.Assistant == nil || len(live.Assistant.Parts) == 0 ||
		live.Assistant.Parts[len(live.Assistant.Parts)-1].Text != "live a" {
		t.Fatalf("live content lost or duplicated: %+v", live.Assistant)
	}
	if live.User == nil || live.User.Parts[0].Text != "live q" {
		t.Fatalf("live user message lost: %+v", live.User)
	}
}

// The loaded-repeat watermark falls back to the current syncRev only when the
// journal cannot recover the original commit rev — the fallback is
// conservative (appliedRev >= current implies >= original), never early.
func TestLoadedDetailWatermarkFallsBackToCurrentRev(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	if got := h.loadedDetailWatermark("codex-remote", sessionID, "never-loaded", 42); got != 42 {
		t.Fatalf("fallback watermark = %d", got)
	}
	_, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if ack["detailLoadState"] != "loaded" {
		t.Fatalf("ack = %+v", ack)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	// Journal still holds the commit: the repeat acks the ORIGINAL rev.
	_, repeat := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if int(repeat["syncRev"].(float64)) != int(ack["syncRev"].(float64)) {
		t.Fatalf("journal-backed repeat drifted: %v vs %v", repeat["syncRev"], ack["syncRev"])
	}
	if int(repeat["syncRev"].(float64)) > proj.SyncRev {
		t.Fatalf("repeat rev must not exceed the kernel head")
	}
}
