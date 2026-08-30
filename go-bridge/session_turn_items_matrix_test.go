package gobridge

// session_turn_items_matrix_test.go is the T2.5 concurrency & ownership matrix
// (plan §3.2 T2.5) for the detail surface: cross-turn concurrency, live-append
// independence of the per-turn fence, retry-after-failure committing exactly
// once, and checkpoint-restore orphan recovery end to end.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func twoTurnDetailHarness(t *testing.T) (*Handlers, *olderWalkConn, string, *turnDetailAgent) {
	t.Helper()
	detail := detailTurnFixture()
	h, conn, sessionID, agent := turnDetailHarness(t, detail)
	// Two completed turns: T1 (older, detail target) + T2 (live-append victim).
	agent.cold = &core.ColdHistoryResult{HistoryMode: "paginated", Page: &core.UpstreamHistoryPage{
		Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1", Status: "completed", UserItemID: "u1", UserText: "q1",
				Parts: []map[string]any{{"type": "text", "content": "a1", "itemId": "asst1"}}},
			{TurnID: "T2", Status: "completed", UserItemID: "u2", UserText: "q2",
				Parts: []map[string]any{{"type": "text", "content": "a2", "itemId": "asst2"}}},
		},
		NextCursor: "",
	}}
	return h, conn, sessionID, agent
}

// Two turns expanded concurrently: separate singleflight keys, both terminal
// loaded commits, kernel holds both details.
func TestTurnItemsTwoTurnsConcurrentExpand(t *testing.T) {
	h, conn, sessionID, agent := twoTurnDetailHarness(t)
	gate := make(chan struct{})
	agent.gate = gate
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	var wg sync.WaitGroup
	acks := make([]map[string]any, 2)
	for i, turnID := range []string{"T1", "T2"} {
		wg.Add(1)
		go func(i int, turnID string) {
			defer wg.Done()
			_, acks[i] = turnItemsDispatch(t, h, conn, sessionID, turnID)
		}(i, turnID)
	}
	close(gate)
	wg.Wait()
	for i, ack := range acks {
		if ack == nil || ack["detailLoadState"] != "loaded" {
			t.Fatalf("turn %d ack = %+v", i, ack)
		}
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	for _, turn := range proj.Turns {
		if turn.DetailLoadState != DetailStateLoaded {
			t.Fatalf("turn %s state = %s", turn.TurnID, turn.DetailLoadState)
		}
	}
}

// Per-turn fence independence (audit-r4 P1-r4-2): a live append on ANOTHER
// turn while this turn's detail fetch is in flight must NOT stale the merge —
// a global baseRev fence would have killed it.
func TestTurnItemsLiveAppendOnOtherTurnDoesNotStaleDetail(t *testing.T) {
	h, conn, sessionID, agent := twoTurnDetailHarness(t)
	gate := make(chan struct{})
	agent.gate = gate
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
	// Live activity lands on T2 (a NEW turn lifecycle) while T1's fetch is
	// gated — the reducer mutates T2 only; T1's generation is untouched.
	h.projectionKernel.reducer.Apply(projectionReducerEvent(
		"codex-remote", sessionID, "turn_started",
		map[string]interface{}{"turnId": "T3"}, 500, "epoch"))
	h.projectionKernel.reducer.Apply(projectionReducerEvent(
		"codex-remote", sessionID, "text_delta",
		map[string]interface{}{"turnId": "T3", "itemId": "i3", "delta": "live text"}, 501, "epoch"))
	close(gate)

	ack := <-done
	if ack == nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("detail merge staled by an unrelated live turn: %+v", ack)
	}
}

func (a *turnDetailAgent) fetchStarted(t *testing.T) chan struct{} {
	t.Helper()
	started := make(chan struct{}, 1)
	go func() {
		for {
			if a.fetchesSnapshot() > 0 {
				started <- struct{}{}
				return
			}
		}
	}()
	return started
}

func (a *turnDetailAgent) fetchesSnapshot() int64 {
	return atomic.LoadInt64(&a.fetches)
}

// Retry after a terminal failure: failed -> loading -> loaded, exactly one
// detail merge commit, Summary replaced once.
func TestTurnItemsRetryAfterFailureCommitsOnce(t *testing.T) {
	h, conn, sessionID, agent := turnDetailHarness(t, detailTurnFixture())
	agent.detailErr = context.DeadlineExceeded
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	_, failAck := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if failAck["detailLoadState"] != "failed" {
		t.Fatalf("fail ack = %+v", failAck)
	}

	// Upstream heals; retry reaches loaded with ONE merge commit.
	agent.detailErr = nil
	_, okAck := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if okAck["detailLoadState"] != "loaded" {
		t.Fatalf("retry ack = %+v", okAck)
	}
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	turn := proj.Turns[0]
	if turn.DetailLoadState != DetailStateLoaded || turn.DetailReasonCode != "" {
		t.Fatalf("terminal = %s/%s", turn.DetailLoadState, turn.DetailReasonCode)
	}
	if len(turn.Assistant.Parts) != 3 {
		t.Fatalf("detail parts = %d (want exactly one merged set)", len(turn.Assistant.Parts))
	}
	// Generation bumped exactly once (failed consumed none).
	if turn.TurnGeneration != 1 {
		t.Fatalf("generation = %d", turn.TurnGeneration)
	}
}

// Codex-remote is a PATHLESS backend (no projection checkpoint on disk): a
// bridge restart re-hydrates from a fresh Summary page, so a loading turn can
// never survive the restart — orphan recovery's restart hook
// (RecoverOrphanDetailLoading at checkpoint restore) guards the future
// checkpoint-adoption path, and the in-place lazy recovery (no in-flight
// leader) is covered by TestSessionTurnItemsOrphanLoadingRecovery. Both
// primitives are unit-pinned in session_turn_items_test.go.
