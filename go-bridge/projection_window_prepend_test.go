package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ---------- reducer / kernel primitives ----------

func prependTestKernel(t *testing.T) *ProjectionKernel {
	t.Helper()
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	kernel.MarkReady("codex-remote", "sess")
	reducer := kernel.reducer
	reducer.Restore("codex-remote", "sess", SessionProjection{
		SessionID: "sess",
		SyncRev:   5,
		Execution: ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{
			{TurnID: "T1", Status: "completed"},
			{TurnID: "T2", Status: "completed"},
		},
	})
	return kernel
}

func TestPrependHistoricalTurnsOrderRevAndJournalGap(t *testing.T) {
	kernel := prependTestKernel(t)
	committed, err := kernel.PrependHistoricalTurns("codex-remote", "sess", []TurnProjection{
		{TurnID: "O1"}, // empty status heals to completed
		{TurnID: "O2", Status: "completed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Ascending page prepends at the front; order preserved.
	if got := committed.Turns; len(got) != 4 ||
		got[0].TurnID != "O1" || got[1].TurnID != "O2" || got[2].TurnID != "T1" || got[3].TurnID != "T2" {
		t.Fatalf("order = %+v", got)
	}
	if committed.SyncRev != 6 {
		t.Fatalf("syncRev = %d, want exactly one bump", committed.SyncRev)
	}
	if got := committed.Turns[0].Status; got != "completed" {
		t.Fatalf("healed status = %q", got)
	}
	// Journal gap by design: no patch flushes for this rev.
	if _, ok := kernel.reducer.FlushPatch("codex-remote", "sess"); ok {
		t.Fatal("prepend must not produce a content patch")
	}
}

func TestPrependHistoricalTurnsOverlapFailsAtomically(t *testing.T) {
	kernel := prependTestKernel(t)
	_, err := kernel.PrependHistoricalTurns("codex-remote", "sess", []TurnProjection{
		{TurnID: "O1", Status: "completed"},
		{TurnID: "T1", Status: "completed"}, // overlap with committed turn
	})
	if !errors.Is(err, ErrProjectionPrependInvalid) {
		t.Fatalf("err = %v, want ErrProjectionPrependInvalid", err)
	}
	proj, ok := kernel.Snapshot("codex-remote", "sess")
	if !ok || len(proj.Turns) != 2 || proj.SyncRev != 5 {
		t.Fatalf("failed prepend leaked: turns=%d rev=%d", len(proj.Turns), proj.SyncRev)
	}
}

func TestPrependKernelRequiresReadySession(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	if _, err := kernel.PrependHistoricalTurns("codex-remote", "sess",
		[]TurnProjection{{TurnID: "O1", Status: "completed"}}); err == nil {
		t.Fatal("prepend on non-ready session must fail")
	}
}

// ---------- slice honesty (R11d) ----------

func TestSliceHasOlderUpstreamHonesty(t *testing.T) {
	proj := SessionProjection{
		SessionID: "sess",
		SyncRev:   5,
		Execution: ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{
			{TurnID: "T1", Status: "completed", Assistant: &MessageProjection{ID: "T1", Role: "assistant", Parts: []ProjectionPart{{Type: "text", Text: "hi"}}}},
			{TurnID: "T2", Status: "completed", Assistant: &MessageProjection{ID: "T2", Role: "assistant", Parts: []ProjectionPart{{Type: "text", Text: "yo"}}}},
		},
	}
	// Upstream NOT exhausted: kernel front is not the session start.
	honest, err := sliceProjectionWindowWithUpstream("codex-remote", "sess", "bep", proj,
		GetSessionProjectionWindowParams{Direction: projectionWindowDirectionWindow0}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !honest.Window.HasOlder || honest.Window.Coverage != "window" || honest.Window.NextOlderCursor == "" {
		t.Fatalf("honest window = %+v", honest.Window)
	}
	// Upstream EOF: exactly today's semantics (full coverage).
	eof, err := sliceProjectionWindowWithUpstream("codex-remote", "sess", "bep", proj,
		GetSessionProjectionWindowParams{Direction: projectionWindowDirectionWindow0}, false)
	if err != nil {
		t.Fatal(err)
	}
	if eof.Window.HasOlder || eof.Window.Coverage != "full" || eof.Window.NextOlderCursor != "" {
		t.Fatalf("eof window = %+v", eof.Window)
	}
}

// ---------- producer state persistence (R11d side file) ----------

func TestProducerStateSideFileRoundTrip(t *testing.T) {
	store := NewProjectionCheckpointStore(t.TempDir())
	if state, err := store.LoadCodexProducerState("codex-remote", "sess"); err != nil || state != nil {
		t.Fatalf("missing file must be (nil,nil): state=%v err=%v", state, err)
	}
	saved := CodexProducerState{
		HasOlderUpstream:   true,
		UpstreamNextCursor: "cursor-1",
		BoundaryTurnID:     "T1",
		UpdatedAt:          time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := store.SaveCodexProducerState("codex-remote", "sess", saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadCodexProducerState("codex-remote", "sess")
	if err != nil || loaded == nil {
		t.Fatalf("load: %v %v", loaded, err)
	}
	if *loaded != saved {
		t.Fatalf("round-trip: %+v vs %+v", *loaded, saved)
	}
	if err := store.SaveCodexProducerState("codex-remote", "sess", CodexProducerState{HasOlderUpstream: true}); err == nil {
		t.Fatal("cursor-less claim must be rejected at save")
	}
}

// ---------- summary page mapping ----------

func TestUpstreamSummaryTurnsToProjection(t *testing.T) {
	turns := []core.TurnScopedHistoryTurn{
		{TurnID: "O1", Status: "completed", UserItemID: "u1", UserText: "question",
			Parts: []map[string]any{
				{"type": "text", "content": "answer summary"},
				{"type": "reasoning", "content": "thinking summary"},
			}},
		{TurnID: "LIVE", Status: "inProgress"}, // never on an older page; skipped honestly
	}
	mapped := upstreamSummaryTurnsToProjection(turns)
	if len(mapped) != 1 {
		t.Fatalf("mapped = %+v", mapped)
	}
	turn := mapped[0]
	if turn.TurnID != "O1" || turn.Status != "completed" || turn.DetailLoadState != DetailStateNotRequested {
		t.Fatalf("turn = %+v", turn)
	}
	if turn.User == nil || turn.User.ID != "u1" || len(turn.User.Parts) != 1 || turn.User.Parts[0].Text != "question" {
		t.Fatalf("user = %+v", turn.User)
	}
	if turn.Assistant == nil || len(turn.Assistant.Parts) == 0 || turn.Assistant.Parts[0].Text != "answer summary" {
		t.Fatalf("assistant = %+v", turn.Assistant)
	}
}

// ---------- per-connection routing (R11b/R11c) ----------

func TestPublishProjectionPrependRoutesByDeliveryMode(t *testing.T) {
	broadcaster := NewBroadcaster()
	p := NewEventPublisher("bep-t20", broadcaster)
	requester := newPublisherCaptureConn(nil)
	windowPeer := newPublisherCaptureConn(nil)
	fullPeer := newPublisherCaptureConn(nil)
	for _, conn := range []*publisherCaptureConn{requester, windowPeer, fullPeer} {
		broadcaster.RegisterConn(conn)
		broadcaster.Subscribe(conn, SubscriptionKey{BackendID: "codex-remote", SessionID: "sess"})
		p.SetConnSyncV2(conn, true)
	}
	p.SetConnProjectionDeliveryMode(windowPeer, "codex-remote", "sess", ProjectionDeliveryWindow)
	// requester + fullPeer stay full/default; requester is exempted by identity.

	// Bring every conn onto rev 1 through the normal broadcast path.
	p.PublishProjectionPatch("codex-remote", "sess", ProjectionPatch{BaseRev: 0, SyncRev: 1})
	requester.waitCount(t, 1)
	windowPeer.waitCount(t, 1)
	fullPeer.waitCount(t, 1)

	p.PublishProjectionPrepend("codex-remote", "sess", 2, requester)

	// Requester: NOTHING new (the page rides its window result; R3 ownership).
	if got := len(requester.snapshotFrames()); got != 1 {
		t.Fatalf("requester frames = %d, want 1 (no prepend frame)", got)
	}
	// Window peer: exactly one no-op revision patch 1→2 with no content ops.
	windowPeer.waitCount(t, 2)
	frames := windowPeer.snapshotFrames()
	noOp := frames[len(frames)-1]
	msg, ok := noOp.(EventMessage)
	if !ok || msg.Event != "projection_patch" {
		t.Fatalf("window peer frame = %+v", noOp)
	}
	patch, _ := msg.Data.(ProjectionPatch)
	if patch.BaseRev != 1 || patch.SyncRev != 2 {
		t.Fatalf("no-op patch revs = %d→%d", patch.BaseRev, patch.SyncRev)
	}
	if patch.UpsertTurns != nil || patch.PartOps != nil || patch.TurnStateOps != nil || patch.Execution != nil {
		t.Fatalf("no-op patch carries content: %+v", patch)
	}
	// Full peer: sync_invalidate → authoritative full re-pull (order-correct truth).
	fullPeer.waitCount(t, 2)
	fullFrames := fullPeer.snapshotFrames()
	invalidated := false
	for _, frame := range fullFrames {
		if m, ok := frame.(EventMessage); ok && m.Event == "sync_invalidate" {
			invalidated = true
		}
	}
	if !invalidated {
		t.Fatalf("full peer missing sync_invalidate: %+v", fullFrames)
	}
}

func (c *publisherCaptureConn) snapshotFrames() []interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]interface{}(nil), c.frames...)
}

// ---------- handler-level older-walk hydration ----------

type olderWalkPagerAgent struct {
	*fakeAgent
	pages     map[string]*core.UpstreamHistoryPage
	baseTurns []core.TurnScopedHistoryTurn
	fetches   atomic.Int64
	gate      chan struct{}
}

// GetTurnScopedRichHistory is codex-remote's cold-baseline surface: the hydrate
// path streams it through turnScopedHistoryTurnToProjectionEvents with OFFICIAL
// turn identity (T1/T2), the same discipline the prepended pages follow.
func (a *olderWalkPagerAgent) GetTurnScopedRichHistory(context.Context, string, int) ([]core.TurnScopedHistoryTurn, error) {
	return a.baseTurns, nil
}

func (a *olderWalkPagerAgent) ReadUpstreamHistoryPage(ctx context.Context, sessionID, cursor string) (*core.UpstreamHistoryPage, error) {
	a.fetches.Add(1)
	if a.gate != nil && cursor != "" {
		// Gate blocks walk pages only; the cold-open "" page must never stall.
		<-a.gate
	}
	page, ok := a.pages[cursor]
	if !ok {
		return nil, errors.New("unknown upstream cursor")
	}
	return page, nil
}

func olderWalkHarness(t *testing.T, pages map[string]*core.UpstreamHistoryPage) (*Handlers, *olderWalkConn, string, *olderWalkPagerAgent) {
	t.Helper()
	// TempDir registers its cleanup FIRST so it runs LAST (LIFO) — the
	// handlers Shutdown (registered after) must quiesce the hydrate runner's
	// post-commit checkpoint writes BEFORE the data dir disappears.
	dataDir := t.TempDir()
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	h.SetDataDir(dataDir)
	agent := &olderWalkPagerAgent{
		fakeAgent: &fakeAgent{name: "codex-remote"},
		baseTurns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1", Status: "completed", UserItemID: "u1", UserText: "q1",
				Parts: []map[string]any{{"type": "text", "content": "a1", "itemId": "asst1"}}},
			{TurnID: "T2", Status: "completed", UserItemID: "u2", UserText: "q2",
				Parts: []map[string]any{{"type": "text", "content": "a2", "itemId": "asst2"}}},
		},
		pages: pages,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"codex-remote": agent}
	h.mu.Unlock()
	conn := &olderWalkConn{}
	h.eventPublisher.SetConnSyncV2(conn, true)
	h.eventPublisher.SetConnProjectionWindowV1(conn, true)
	return h, conn, "sess", agent
}

// olderWalkConn captures both RPC results and pushed frames.
type olderWalkConn struct {
	mu     sync.Mutex
	frames []interface{}
	err    *WireError
	data   any
}

func (c *olderWalkConn) SendJSON(frame interface{}) {
	c.mu.Lock()
	c.frames = append(c.frames, frame)
	c.mu.Unlock()
}

func (c *olderWalkConn) SendJSONClassified(frame any, _ relayOutboundClass) { c.SendJSON(frame) }

func (c *olderWalkConn) SendResult(_ string, data interface{}, err *WireError) {
	c.mu.Lock()
	c.err, c.data = err, data
	c.mu.Unlock()
}

func (c *olderWalkConn) AuthedDevice() *TrustedDeviceRecord { return nil }
func (c *olderWalkConn) RemoteAddr() string                 { return "older-walk" }
func (c *olderWalkConn) Close() error                       { return nil }

func olderWalkDispatch(h *Handlers, conn *olderWalkConn, params map[string]any) (*WireError, map[string]any) {
	conn.mu.Lock()
	conn.err, conn.data = nil, nil
	conn.mu.Unlock()
	raw, _ := json.Marshal(params)
	msg := WireMessage{RequestID: "r-older", BackendID: "codex-remote", Method: "get_session_projection_window", Params: raw}
	h.handleGetSessionProjectionWindow(conn, msg, nil)
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

func TestOlderWalkHydratesOnePageAndPrepends(t *testing.T) {
	pages := map[string]*core.UpstreamHistoryPage{
		// T2.1: cold open consumes ONE Summary page via the pager ("" cursor,
		// already ascending) — includeTurns has left the production path.
		"": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1", Status: "completed", UserItemID: "u1", UserText: "q1",
				Parts: []map[string]any{{"type": "text", "content": "a1", "itemId": "asst1"}}},
			{TurnID: "T2", Status: "completed", UserItemID: "u2", UserText: "q2",
				Parts: []map[string]any{{"type": "text", "content": "a2", "itemId": "asst2"}}},
		}, NextCursor: "c1"},
		"c1": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "O1", Status: "completed", UserText: "oq1", Parts: []map[string]any{{"type": "text", "content": "oa1"}}},
			{TurnID: "O2", Status: "completed", UserText: "oq2", Parts: []map[string]any{{"type": "text", "content": "oa2"}}},
		}, NextCursor: "c2"},
	}
	h, conn, sessionID, agent := olderWalkHarness(t, pages)

	// window_0 → kernel ready (T1,T2). The Summary page's unexhausted cursor
	// auto-seeds the producer fact, and the front is honest about older upstream
	// (hasOlder=true even though the kernel itself holds the full known set).
	firstErr, first := olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	if firstErr != nil {
		t.Fatalf("window_0 failed: %+v", firstErr)
	}
	if turns := first["turns"]; turns == nil {
		t.Fatalf("window_0 result = %+v", first)
	}
	if window, _ := first["window"].(map[string]any); window == nil || window["hasOlder"] != true {
		t.Fatalf("window_0 must report hasOlder=true with an unexhausted upstream cursor: %+v", first["window"])
	}
	proj, ok := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if !ok || len(proj.Turns) != 2 {
		t.Fatalf("kernel after window_0: ok=%v turns=%+v", ok, proj.Turns)
	}
	front := proj.Turns[0].TurnID

	// T2.1 cold-open seeding: the producer fact lands with the page-1 cursor,
	// anchored at the committed kernel front — no manual seeding anywhere. The
	// seed hook runs right after Done release, so poll briefly for durability.
	seeded := waitForCodexProducerState(t, h, sessionID)
	if seeded == nil || !seeded.HasOlderUpstream ||
		seeded.UpstreamNextCursor != "c1" || seeded.BoundaryTurnID != front {
		t.Fatalf("cold-open producer seed = %+v", seeded)
	}

	cursor := encodeProjectionWindowCursor(projectionWindowCursor{
		V: 1, BridgeEpoch: h.eventPublisher.BridgeEpoch(), BackendID: "codex-remote",
		SessionID: sessionID, AnchorTurnID: front, Side: "o",
	})
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "older", "backendId": "codex-remote", "sessionId": sessionID,
		"cursor": cursor, "limit": 10,
	})

	if got := agent.fetches.Load(); got != 2 {
		t.Fatalf("upstream fetches = %d, want cold page + exactly one walk page", got)
	}
	proj, ok = h.projectionKernel.Snapshot("codex-remote", sessionID)
	if !ok {
		t.Fatal("kernel snapshot missing after hydration")
	}
	if got := projTurnIDs(proj); len(got) != 4 || got[0] != "O1" || got[1] != "O2" || got[2] != "T1" || got[3] != "T2" {
		t.Fatalf("kernel order after prepend = %v", got)
	}
	state, err := h.projectionKernel.LoadCodexProducerState("codex-remote", sessionID)
	if err != nil || state == nil || !state.HasOlderUpstream || state.UpstreamNextCursor != "c2" || state.BoundaryTurnID != "O1" {
		t.Fatalf("producer state after page = %+v err=%v", state, err)
	}

	// EOF page: honest hasOlder=false + coverage full.
	agent.pages["c2"] = &core.UpstreamHistoryPage{Turns: []core.TurnScopedHistoryTurn{
		{TurnID: "O0", Status: "completed", UserText: "oq0", Parts: []map[string]any{{"type": "text", "content": "oa0"}}},
	}}
	cursor2 := encodeProjectionWindowCursor(projectionWindowCursor{
		V: 1, BridgeEpoch: h.eventPublisher.BridgeEpoch(), BackendID: "codex-remote",
		SessionID: sessionID, AnchorTurnID: "O1", Side: "o",
	})
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "older", "backendId": "codex-remote", "sessionId": sessionID,
		"cursor": cursor2, "limit": 10,
	})
	state, err = h.projectionKernel.LoadCodexProducerState("codex-remote", sessionID)
	if err != nil || state == nil || state.HasOlderUpstream {
		t.Fatalf("producer state after EOF page = %+v err=%v", state, err)
	}
	proj, _ = h.projectionKernel.Snapshot("codex-remote", sessionID)
	if got := projTurnIDs(proj); got[0] != "O0" {
		t.Fatalf("EOF page prepended = %v", got)
	}
}

// waitForCodexProducerState polls the durable producer fact until the post-commit
// seed hook has flushed it (the hook runs just after CommitHydrateTransaction
// releases Done-waiters, so the immediate read may still be in flight).
func waitForCodexProducerState(t *testing.T, h *Handlers, sessionID string) *CodexProducerState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if state, err := h.projectionKernel.LoadCodexProducerState("codex-remote", sessionID); err == nil && state != nil {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, _ := h.projectionKernel.LoadCodexProducerState("codex-remote", sessionID)
	return state
}

func projTurnIDs(proj SessionProjection) []string {
	ids := make([]string, 0, len(proj.Turns))
	for _, turn := range proj.Turns {
		ids = append(ids, turn.TurnID)
	}
	return ids
}

// Stale upstream cursor (page overlaps a known turn): typed staleness, producer
// state falls back to a head re-walk, and no kernel mutation happens.
func TestOlderWalkUpstreamCursorStaleTypedRecovery(t *testing.T) {
	pages := map[string]*core.UpstreamHistoryPage{
		"": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1", Status: "completed", UserText: "q1", Parts: []map[string]any{{"type": "text", "content": "a1"}}},
			{TurnID: "T2", Status: "completed", UserText: "q2", Parts: []map[string]any{{"type": "text", "content": "a2"}}},
		}, NextCursor: "c1"},
		"c1": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1", Status: "completed", UserText: "dup", Parts: []map[string]any{{"type": "text", "content": "dup"}}},
		}, NextCursor: "c2"},
	}
	h, conn, sessionID, _ := olderWalkHarness(t, pages)
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	front := proj.Turns[0].TurnID
	cursor := encodeProjectionWindowCursor(projectionWindowCursor{
		V: 1, BridgeEpoch: h.eventPublisher.BridgeEpoch(), BackendID: "codex-remote",
		SessionID: sessionID, AnchorTurnID: front, Side: "o",
	})
	wireErr, _ := olderWalkDispatch(h, conn, map[string]any{
		"direction": "older", "backendId": "codex-remote", "sessionId": sessionID,
		"cursor": cursor, "limit": 10,
	})
	if wireErr == nil || wireErr.Code != "cursor_stale" {
		t.Fatalf("stale upstream cursor must surface cursor_stale, got %+v", wireErr)
	}
	after, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if len(after.Turns) != 2 || after.SyncRev != proj.SyncRev {
		t.Fatalf("stale page mutated kernel: %+v", after)
	}
	state, _ := h.projectionKernel.LoadCodexProducerState("codex-remote", sessionID)
	if state == nil || !state.HasOlderUpstream || state.UpstreamNextCursor != "" || state.BoundaryTurnID != front {
		t.Fatalf("stale cursor must reset to head re-walk anchored at the kernel front: %+v", state)
	}
}

// Concurrent older walks on one session: exactly ONE upstream page per wave.
func TestOlderWalkSingleFlightConcurrent(t *testing.T) {
	pages := map[string]*core.UpstreamHistoryPage{
		"": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1", Status: "completed", UserText: "q1", Parts: []map[string]any{{"type": "text", "content": "a1"}}},
			{TurnID: "T2", Status: "completed", UserText: "q2", Parts: []map[string]any{{"type": "text", "content": "a2"}}},
		}, NextCursor: "c1"},
		"c1": {Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "O1", Status: "completed", UserText: "oq1", Parts: []map[string]any{{"type": "text", "content": "oa1"}}},
			{TurnID: "O2", Status: "completed", UserText: "oq2", Parts: []map[string]any{{"type": "text", "content": "oa2"}}},
		}, NextCursor: ""},
	}
	h, conn, sessionID, agent := olderWalkHarness(t, pages)
	agent.gate = make(chan struct{})
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	front := proj.Turns[0].TurnID
	cursor := encodeProjectionWindowCursor(projectionWindowCursor{
		V: 1, BridgeEpoch: h.eventPublisher.BridgeEpoch(), BackendID: "codex-remote",
		SessionID: sessionID, AnchorTurnID: front, Side: "o",
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			olderWalkDispatch(h, conn, map[string]any{
				"direction": "older", "backendId": "codex-remote", "sessionId": sessionID,
				"cursor": cursor, "limit": 10,
			})
		}()
	}
	// Let the leader fetch; followers must coalesce behind it.
	time.Sleep(150 * time.Millisecond)
	close(agent.gate)
	wg.Wait()

	if got := agent.fetches.Load(); got != 2 {
		t.Fatalf("upstream fetches = %d, want cold page + 1 walk page (singleflight)", got)
	}
	final, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if got := projTurnIDs(final); len(got) != 4 || got[0] != "O1" || got[1] != "O2" {
		t.Fatalf("kernel after concurrent walk = %v", got)
	}
}
