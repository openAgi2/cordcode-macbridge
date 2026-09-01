package gobridge

// projection_window_t21_test.go is the T2.1 end-to-end matrix: the projection
// window rides the one-page Summary cold hydrate plus the producer-seeded older
// walk across a multi-page upstream (reachability, no gap/overlap, EOF),
// per-connection routing at the handler level (A/B/C), re-seeding of a stale
// producer fact, and canonical part item ids through the whole pipe.

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func t21Turn(id string) core.TurnScopedHistoryTurn {
	return core.TurnScopedHistoryTurn{
		TurnID:     id,
		Status:     "completed",
		UserItemID: "u_" + id,
		UserText:   "q_" + id,
		Parts:      []map[string]any{{"type": "text", "content": "a_" + id, "itemId": "i_" + id}},
	}
}

// t21AscendingPages builds an upstream of pageCount pages × perPage turns. The
// "" page is the newest; each older page precedes it in time. Page cursors chain
// c1..cN with the last page at EOF.
func t21AscendingPages(pageCount, perPage int) map[string]*core.UpstreamHistoryPage {
	total := pageCount * perPage
	pages := map[string]*core.UpstreamHistoryPage{}
	cursor := ""
	for page := 0; page < pageCount; page++ {
		start := total - (page+1)*perPage // newest page first
		turns := make([]core.TurnScopedHistoryTurn, 0, perPage)
		for i := 0; i < perPage; i++ {
			turns = append(turns, t21Turn(fmt.Sprintf("T%02d", start+i+1)))
		}
		key := cursor
		cursor = fmt.Sprintf("c%d", page+1)
		if page == pageCount-1 {
			cursor = ""
		}
		pages[key] = &core.UpstreamHistoryPage{Turns: turns, NextCursor: cursor}
	}
	return pages
}

func t21WindowMap(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	window, ok := result["window"].(map[string]any)
	if !ok {
		t.Fatalf("result window = %+v", result["window"])
	}
	return window
}

func t21TurnIDs(t *testing.T, result map[string]any) []string {
	t.Helper()
	raw, ok := result["turns"].([]any)
	if !ok {
		t.Fatalf("result turns = %+v", result["turns"])
	}
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		turn, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("turn entry = %+v", item)
		}
		id, _ := turn["turnId"].(string)
		ids = append(ids, id)
	}
	return ids
}

// TestWindowOlderWalkFullReachability walks the whole upstream through the
// window chain: window_0 (Summary page 1) then older pages until EOF. The
// kernel must hold every turn exactly once in ascending order, one upstream
// page per step, and the final page reports honest hasOlder=false.
func TestWindowOlderWalkFullReachability(t *testing.T) {
	pages := t21AscendingPages(4, 10) // T01..T40, "" page = T31..T40
	h, conn, sessionID, agent := olderWalkHarness(t, pages)

	_, first := olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	firstIDs := t21TurnIDs(t, first)
	if len(firstIDs) != 10 || firstIDs[0] != "T31" || firstIDs[9] != "T40" {
		t.Fatalf("window_0 ids = %v", firstIDs)
	}
	window := t21WindowMap(t, first)
	if window["hasOlder"] != true {
		t.Fatalf("window_0 hasOlder = %v (upstream unexhausted)", window["hasOlder"])
	}

	seen := map[string]bool{}
	for _, id := range firstIDs {
		seen[id] = true
	}
	cursor, _ := window["nextOlderCursor"].(string)
	steps := 0
	for cursor != "" && steps < 10 {
		steps++
		wireErr, result := olderWalkDispatch(h, conn, map[string]any{
			"direction": "older", "backendId": "codex-remote", "sessionId": sessionID,
			"cursor": cursor, "limit": 10,
		})
		if wireErr != nil {
			t.Fatalf("older step %d failed: %+v", steps, wireErr)
		}
		for _, id := range t21TurnIDs(t, result) {
			if seen[id] {
				t.Fatalf("older walk repeated turn %s", id)
			}
			seen[id] = true
		}
		window = t21WindowMap(t, result)
		if hasOlder, _ := window["hasOlder"].(bool); !hasOlder {
			cursor = ""
			break
		}
		cursor, _ = window["nextOlderCursor"].(string)
	}
	if len(seen) != 40 {
		t.Fatalf("walk reached %d/40 turns", len(seen))
	}
	if got := agent.fetches.Load(); got != 4 {
		t.Fatalf("upstream fetches = %d, want 1 cold page + 3 walk pages (4 total)", got)
	}

	proj, ok := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if !ok {
		t.Fatal("kernel snapshot missing")
	}
	ids := projTurnIDs(proj)
	if len(ids) != 40 || ids[0] != "T01" || ids[39] != "T40" {
		t.Fatalf("kernel order = %v…%v (%d turns)", ids[:2], ids[len(ids)-2:], len(ids))
	}
	for i, id := range ids {
		if want := fmt.Sprintf("T%02d", i+1); id != want {
			t.Fatalf("kernel gap/overlap at %d: %v", i, ids)
		}
	}
	// Canonical item ids ride the whole pipe: mapper → reducer → kernel.
	for _, turn := range proj.Turns {
		if turn.User == nil || len(turn.User.Parts) != 1 || turn.User.Parts[0].ItemID != "u_"+turn.TurnID {
			t.Fatalf("user part itemId lost on %s: %+v", turn.TurnID, turn.User)
		}
		if turn.Assistant == nil || len(turn.Assistant.Parts) != 1 || turn.Assistant.Parts[0].ItemID != "i_"+turn.TurnID {
			t.Fatalf("assistant part itemId lost on %s: %+v", turn.TurnID, turn.Assistant)
		}
	}
	state := waitForCodexProducerState(t, h, sessionID)
	if state == nil || state.HasOlderUpstream || state.UpstreamNextCursor != "" {
		t.Fatalf("producer state after EOF walk = %+v", state)
	}
}

// TestWindowOlderWalkRoutesPerConnection is the handler-level A/B/C matrix
// (R11b): the older-walk requester's page rides its RPC result, window-mode
// peers get a no-op revision patch on their own baseRev chain, and full
// projection connections get sync_invalidate.
func TestWindowOlderWalkRoutesPerConnection(t *testing.T) {
	pages := map[string]*core.UpstreamHistoryPage{
		"": {Turns: []core.TurnScopedHistoryTurn{t21Turn("T1"), t21Turn("T2")}, NextCursor: "c1"},
		"c1": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "O1", Status: "completed", UserText: "oq1", Parts: []map[string]any{{"type": "text", "content": "oa1"}}},
		}, NextCursor: ""},
	}
	h, connA, sessionID, _ := olderWalkHarness(t, pages)

	// B: a second window-mode consumer (its own window_0 registers delivery mode
	// + subscription + snapshot fence cut).
	connB := &olderWalkConn{}
	h.eventPublisher.SetConnSyncV2(connB, true)
	h.eventPublisher.SetConnProjectionWindowV1(connB, true)
	if _, bFirst := olderWalkDispatch(h, connB, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	}); bFirst["turns"] == nil {
		t.Fatalf("B window_0 = %+v", bFirst)
	}

	// C: full-projection syncV2 consumer (never calls the window RPC).
	connC := &olderWalkConn{}
	h.eventPublisher.broadcaster.RegisterConn(connC)
	h.eventPublisher.broadcaster.Subscribe(connC, SubscriptionKey{BackendID: "codex-remote", SessionID: sessionID})
	h.eventPublisher.SetConnSyncV2(connC, true)

	_, first := olderWalkDispatch(h, connA, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	window := t21WindowMap(t, first)
	cursor, _ := window["nextOlderCursor"].(string)
	wireErr, result := olderWalkDispatch(h, connA, map[string]any{
		"direction": "older", "backendId": "codex-remote", "sessionId": sessionID,
		"cursor": cursor, "limit": 10,
	})
	if wireErr != nil {
		t.Fatalf("A older walk failed: %+v", wireErr)
	}
	if ids := t21TurnIDs(t, result); len(ids) != 1 || ids[0] != "O1" {
		t.Fatalf("A page = %v", ids)
	}

	// B: exactly one projection_patch — a no-op revision patch, no content ops.
	bPatch := t21WaitForPatch(t, connB)
	if bPatch.UpsertTurns != nil || bPatch.PartOps != nil || bPatch.TurnStateOps != nil || bPatch.Execution != nil {
		t.Fatalf("B no-op patch carries content: %+v", bPatch)
	}
	if bPatch.SyncRev <= bPatch.BaseRev {
		t.Fatalf("B patch must advance the rev: %d→%d", bPatch.BaseRev, bPatch.SyncRev)
	}

	// C: sync_invalidate for the order-correct full re-pull.
	if !t21WaitForEvent(t, connC, "sync_invalidate", 2*time.Second) {
		t.Fatalf("C never received sync_invalidate: %+v", connC.frames)
	}

	// A: no prepend frame — the page rode the window result.
	for _, frame := range connA.frames {
		if msg, ok := frame.(EventMessage); ok && msg.Event == "projection_patch" {
			t.Fatalf("requester A received a prepend patch it must not: %+v", msg)
		}
	}
}

func t21WaitForPatch(t *testing.T, conn *olderWalkConn) ProjectionPatch {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn.mu.Lock()
		frames := append([]interface{}(nil), conn.frames...)
		conn.mu.Unlock()
		for i := len(frames) - 1; i >= 0; i-- {
			msg, ok := frames[i].(EventMessage)
			if !ok || msg.Event != "projection_patch" {
				continue
			}
			encoded, _ := json.Marshal(msg.Data)
			var patch ProjectionPatch
			if err := json.Unmarshal(encoded, &patch); err != nil {
				t.Fatalf("B patch decode: %v", err)
			}
			return patch
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("B never received the no-op revision patch")
	return ProjectionPatch{}
}

func t21WaitForEvent(t *testing.T, conn *olderWalkConn, event string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn.mu.Lock()
		frames := append([]interface{}(nil), conn.frames...)
		conn.mu.Unlock()
		for _, frame := range frames {
			if msg, ok := frame.(EventMessage); ok && msg.Event == event {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestColdHydrateReseedsStaleProducerFact: a producer fact left behind by an
// earlier epoch (or written before a failed hydrate) is overwritten by the
// fresh page-1 seed at the next successful cold hydrate — the guard compares
// UpdatedAt, so older claims never survive a newer Summary page.
func TestColdHydrateReseedsStaleProducerFact(t *testing.T) {
	pages := map[string]*core.UpstreamHistoryPage{
		"": {Turns: []core.TurnScopedHistoryTurn{t21Turn("T1"), t21Turn("T2")}, NextCursor: "c1"},
	}
	h, conn, sessionID, _ := olderWalkHarness(t, pages)
	if err := h.projectionKernel.SaveCodexProducerState("codex-remote", sessionID, CodexProducerState{
		HasOlderUpstream:   true,
		UpstreamNextCursor: "c-stale",
		BoundaryTurnID:     "T0",
		UpdatedAt:          time.Now().Add(-time.Hour).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if wireErr, _ := olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	}); wireErr != nil {
		t.Fatalf("window_0 failed: %+v", wireErr)
	}
	// Poll for the RESEEDED fact (the stale file is already non-nil, so the
	// generic non-nil wait helper cannot be used here).
	var state *CodexProducerState
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ = h.projectionKernel.LoadCodexProducerState("codex-remote", sessionID)
		if state != nil && state.UpstreamNextCursor == "c1" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if state == nil || state.UpstreamNextCursor != "c1" || state.BoundaryTurnID != "T1" {
		t.Fatalf("stale producer fact survived the cold re-seed: %+v", state)
	}
}

// TestReducerPartsCarryCanonicalItemIds pins the T2.1 reducer wiring: user and
// reasoning parts stamp the upstream item id, same-item text deltas keep
// appending, and a different-item delta splits into a new part carried by a
// whole-turn upsert (append_text PartOps cannot express the split).
func TestReducerPartsCarryCanonicalItemIds(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "codex-remote", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex-remote", "s1", "user_message", map[string]interface{}{
		"turnId": "T1", "itemId": "u1", "text": "question",
	}))
	p1, ok := r.FlushPatch("codex-remote", "s1")
	if !ok || len(p1.UpsertTurns) != 1 {
		t.Fatalf("user_message patch = %+v", p1)
	}
	if user := p1.UpsertTurns[0].User; user == nil || len(user.Parts) != 1 || user.Parts[0].ItemID != "u1" {
		t.Fatalf("user part itemId = %+v", p1.UpsertTurns[0].User)
	}

	r.Apply(ev(3, "codex-remote", "s1", "text_delta", map[string]interface{}{
		"turnId": "T1", "itemId": "i1", "delta": "hel",
	}))
	r.Apply(ev(4, "codex-remote", "s1", "text_delta", map[string]interface{}{
		"turnId": "T1", "itemId": "i1", "delta": "lo",
	}))
	r.Apply(ev(5, "codex-remote", "s1", "reasoning_delta", map[string]interface{}{
		"turnId": "T1", "itemId": "r1", "delta": "thinking",
	}))
	r.Apply(ev(6, "codex-remote", "s1", "text_delta", map[string]interface{}{
		"turnId": "T1", "itemId": "i2", "delta": "world",
	}))

	proj, ok := r.Snapshot("codex-remote", "s1")
	if !ok {
		t.Fatal("snapshot missing")
	}
	turn := proj.Turns[0]
	if turn.Assistant == nil {
		t.Fatal("assistant missing")
	}
	var texts, itemIDs []string
	for _, part := range turn.Assistant.Parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
			itemIDs = append(itemIDs, part.ItemID)
		}
	}
	if len(texts) != 2 || texts[0] != "hello" || texts[1] != "world" {
		t.Fatalf("text parts = %v", texts)
	}
	if len(itemIDs) != 2 || itemIDs[0] != "i1" || itemIDs[1] != "i2" {
		t.Fatalf("text part itemIds = %v", itemIDs)
	}
	reasoningStamped := false
	for _, part := range turn.Assistant.Parts {
		if part.Type == "reasoning" {
			reasoningStamped = part.ItemID == "r1"
		}
	}
	if !reasoningStamped {
		t.Fatalf("reasoning part itemId lost: %+v", turn.Assistant.Parts)
	}

	// The item-boundary split must ride a whole-turn upsert, never append_text.
	p2, ok := r.FlushPatch("codex-remote", "s1")
	if !ok {
		t.Fatal("flush after split missing")
	}
	splitUpserted := false
	for _, upsert := range p2.UpsertTurns {
		if upsert.TurnID != "T1" {
			continue
		}
		for _, part := range upsert.Assistant.Parts {
			if part.Type == "text" && part.ItemID == "i2" {
				splitUpserted = true
			}
		}
	}
	if !splitUpserted {
		t.Fatalf("split part not upserted: %+v", p2.UpsertTurns)
	}
}
