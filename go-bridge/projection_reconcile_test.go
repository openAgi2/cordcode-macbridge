package gobridge

// projection_reconcile_test.go covers S-2 (disconnect-resilience plan §3.2/§5):
// the READY-safe reconcile transaction and its runner. A turn that terminated
// inside the disconnect window stays "running" in the projection (E-1: no
// controller-leg replay); the runner reads the authoritative summary and closes
// it against a baseline that IS the committed projection — older-window turns,
// producer state and the source cut all survive (F-R4-1), and the commit never
// runs the live-execution merge that would revert the closure (F-R5-1).

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// reconcileTestAgent fakes the codex-remote surfaces the runner touches:
// ColdHistoryReader (authoritative summary) + TurnReconciler (pending set and
// codec seam). gate/entered let window-interleave tests block the summary read
// exactly when the tx holds the session in Hydrating.
type reconcileTestAgent struct {
	*fakeAgent
	cold    *core.ColdHistoryResult
	coldErr error
	gate    chan struct{}
	entered atomic.Int64

	pending   map[string]struct{}
	active    map[string]string
	cleared   []string // ClearReconciledTurn calls (turn actually closed)
	pendClear []string // ClearPendingTurnReconcile calls (attempt completed)
}

func (a *reconcileTestAgent) ReadColdHistory(ctx context.Context, sessionID string) (*core.ColdHistoryResult, error) {
	a.entered.Add(1)
	if a.gate != nil {
		<-a.gate
	}
	return a.cold, a.coldErr
}

func (a *reconcileTestAgent) TurnReconcileSignals() <-chan struct{} {
	ch := make(chan struct{}, 1)
	return ch
}

func (a *reconcileTestAgent) PendingTurnReconciles() []string {
	out := make([]string, 0, len(a.pending))
	for id := range a.pending {
		out = append(out, id)
	}
	return out
}

func (a *reconcileTestAgent) ActiveTurnForReconcile(threadID string) string {
	return a.active[threadID]
}

func (a *reconcileTestAgent) ClearPendingTurnReconcile(threadID string) {
	delete(a.pending, threadID)
	a.pendClear = append(a.pendClear, threadID)
}

func (a *reconcileTestAgent) ClearReconciledTurn(threadID string) {
	delete(a.pending, threadID)
	delete(a.active, threadID)
	a.cleared = append(a.cleared, threadID)
}

func reconcileHarness(t *testing.T) (*Handlers, *reconcileTestAgent) {
	t.Helper()
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	h.SetDataDir(t.TempDir())
	agent := &reconcileTestAgent{
		fakeAgent: &fakeAgent{name: "codex-remote"},
		pending:   map[string]struct{}{},
		active:    map[string]string{},
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"codex-remote": agent}
	h.mu.Unlock()
	return h, agent
}

// reconcileReadyKernel builds the disconnect-window shape: a READY session
// whose committed projection already carries prepended older-window turns
// (O1/O2, banked by the older walk) plus an in-flight T3 that terminated
// inside the window.
func reconcileReadyKernel(t *testing.T, h *Handlers) {
	t.Helper()
	h.projectionKernel.MarkReady("codex-remote", "sess")
	h.projectionKernel.reducer.Restore("codex-remote", "sess", SessionProjection{
		SessionID:   "sess",
		SyncRev:     5,
		BridgeEpoch: h.eventPublisher.BridgeEpoch(),
		Execution:   ExecutionView{Phase: "running", ActiveTurnID: "T3"},
		Turns: []TurnProjection{
			{TurnID: "T1-old", Status: "completed"},
			{TurnID: "T2-old", Status: "completed"},
			{TurnID: "T3", Status: "running"},
		},
	})
}

func committedTurn(t *testing.T, h *Handlers, turnID string) TurnProjection {
	t.Helper()
	committed, ok := h.projectionKernel.CommittedSnapshot("codex-remote", "sess")
	if !ok {
		t.Fatal("committed snapshot missing after reconcile")
	}
	for _, turn := range committed.Turns {
		if turn.TurnID == turnID {
			return turn
		}
	}
	t.Fatalf("turn %s missing from committed projection: %+v", turnID, committed.Turns)
	return TurnProjection{}
}

// TestReconcileClosesDisconnectWindowTurn is the §5 main scenario: READY +
// prepended older pages + a turn completed inside the disconnect window →
// authoritative terminal closure, older turns preserved, Execution back to
// idle without the live-execution merge reverting it (F-R5-1 closure
// criterion), codec seam cleared, producer state file untouched.
func TestReconcileClosesDisconnectWindowTurn(t *testing.T) {
	h, agent := reconcileHarness(t)
	reconcileReadyKernel(t, h)
	agent.pending["sess"] = struct{}{}
	agent.active["sess"] = "T3"
	agent.cold = &core.ColdHistoryResult{HistoryMode: "paginated", Page: &core.UpstreamHistoryPage{
		Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T1-old", Status: "completed"},
			{TurnID: "T2-old", Status: "completed"},
			{TurnID: "T3", Status: "completed", DurationMs: 4200},
		},
	}}
	seed := CodexProducerState{
		HasOlderUpstream:   true,
		UpstreamNextCursor: "cursor-old",
		BoundaryTurnID:     "T1-old",
		HistoryMode:        "paginated",
		UpdatedAt:          time.Now().Add(-time.Minute),
	}
	if err := h.projectionKernel.SaveCodexProducerState("codex-remote", "sess", seed); err != nil {
		t.Fatalf("seed producer state: %v", err)
	}

	h.drainTurnReconciles(context.Background(), "codex-remote")

	// Terminal closure carried the official durationMs.
	closed := committedTurn(t, h, "T3")
	if closed.Status != "completed" {
		t.Fatalf("T3 status = %q, want completed", closed.Status)
	}
	if closed.DurationMs != 4200 {
		t.Fatalf("T3 durationMs = %d, want 4200", closed.DurationMs)
	}
	// F-R5-1: the commit published the tx baseline as-is — no live-execution
	// merge, so the closure survived and Execution settled to idle.
	committed, _ := h.projectionKernel.CommittedSnapshot("codex-remote", "sess")
	if committed.Execution.Phase != "idle" || committed.Execution.ActiveTurnID != "" {
		t.Fatalf("execution = %+v, want idle with no active turn", committed.Execution)
	}
	// Older-window turns survive intact, in order, at the front.
	if len(committed.Turns) != 3 ||
		committed.Turns[0].TurnID != "T1-old" || committed.Turns[0].Status != "completed" ||
		committed.Turns[1].TurnID != "T2-old" || committed.Turns[1].Status != "completed" {
		t.Fatalf("older-window turns lost/reordered: %+v", committed.Turns)
	}
	// Codec seam cleared only through the closed-turn path.
	if len(agent.cleared) != 1 || agent.cleared[0] != "sess" {
		t.Fatalf("ClearReconciledTurn calls = %v, want [sess]", agent.cleared)
	}
	if len(agent.pending) != 0 {
		t.Fatalf("pending set = %v, want empty", agent.pending)
	}
	// Producer state file unchanged: reconcile never stages or persists
	// a producer seed (F-R4-1 — the source cut and older-walk facts survive).
	// UpdatedAt compares via Equal: the JSON round-trip drops the monotonic
	// clock reading that plain struct equality would trip over.
	after, err := h.projectionKernel.LoadCodexProducerState("codex-remote", "sess")
	if err != nil || after == nil {
		t.Fatalf("producer state lost after reconcile: %v %v", after, err)
	}
	if after.HasOlderUpstream != seed.HasOlderUpstream ||
		after.UpstreamNextCursor != seed.UpstreamNextCursor ||
		after.BoundaryTurnID != seed.BoundaryTurnID ||
		after.HistoryMode != seed.HistoryMode ||
		!after.UpdatedAt.Equal(seed.UpdatedAt) {
		t.Fatalf("producer state mutated by reconcile: %+v, want %+v", *after, seed)
	}
}

// TestReconcileWindowLiveDeltaConverges proves the fence convergence: a live
// delta landing inside the reconcile window queues as pendingLive and drains
// in the same commit — the authoritative closure AND the live truth both land.
func TestReconcileWindowLiveDeltaConverges(t *testing.T) {
	h, agent := reconcileHarness(t)
	reconcileReadyKernel(t, h)
	agent.pending["sess"] = struct{}{}
	agent.active["sess"] = "T3"
	agent.cold = &core.ColdHistoryResult{HistoryMode: "paginated", Page: &core.UpstreamHistoryPage{
		Turns: []core.TurnScopedHistoryTurn{
			{TurnID: "T3", Status: "completed"},
		},
	}}
	agent.gate = make(chan struct{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.drainTurnReconciles(context.Background(), "codex-remote")
	}()
	deadline := time.Now().Add(5 * time.Second)
	for agent.entered.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("summary read never started")
		}
		time.Sleep(2 * time.Millisecond)
	}
	// The tx now holds the session in Hydrating: a live user_message for a new
	// turn fences into pendingLive instead of mutating the committed reducer.
	h.projectionKernel.IngestLive(EventMessage{
		BackendID: "codex-remote", SessionID: "sess", BridgeEpoch: h.eventPublisher.BridgeEpoch(),
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"itemId": "u4", "turnId": "T4", "text": "next question"},
	})
	close(agent.gate)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain never finished")
	}

	// Authoritative closure landed…
	if got := committedTurn(t, h, "T3").Status; got != "completed" {
		t.Fatalf("T3 status = %q, want completed", got)
	}
	// …and the window's live delta was drained in the same commit, not dropped.
	live := committedTurn(t, h, "T4")
	if live.Status != "running" {
		t.Fatalf("T4 status = %q, want running (live truth drained)", live.Status)
	}
	committed, _ := h.projectionKernel.CommittedSnapshot("codex-remote", "sess")
	if committed.Execution.Phase != "running" || committed.Execution.ActiveTurnID != "T4" {
		t.Fatalf("execution = %+v, want running/T4", committed.Execution)
	}
	if len(agent.cleared) != 1 || agent.cleared[0] != "sess" {
		t.Fatalf("ClearReconciledTurn calls = %v, want [sess]", agent.cleared)
	}
}

// TestReconcileGhostTurnNotMinted is the ghost negative: a summary turn absent
// from the baseline never mints a bare turn, and a still-in-flight verdict
// keeps the turn running, aborts the tx and retires the pending entry.
func TestReconcileGhostTurnNotMinted(t *testing.T) {
	h, agent := reconcileHarness(t)
	reconcileReadyKernel(t, h)
	agent.pending["sess"] = struct{}{}
	agent.active["sess"] = "T3"
	agent.cold = &core.ColdHistoryResult{HistoryMode: "paginated", Page: &core.UpstreamHistoryPage{
		Turns: []core.TurnScopedHistoryTurn{
			// Summary first page: the in-flight turn plus a turn the baseline
			// has never seen (e.g. another window's race) — log-only, no ghost.
			{TurnID: "T3", Status: "inProgress"},
			{TurnID: "G1", Status: "completed"},
		},
	}}

	h.drainTurnReconciles(context.Background(), "codex-remote")

	committed, ok := h.projectionKernel.CommittedSnapshot("codex-remote", "sess")
	if !ok {
		t.Fatal("committed snapshot missing")
	}
	if len(committed.Turns) != 3 {
		t.Fatalf("turns = %+v, want exactly the 3 baseline turns (no ghost)", committed.Turns)
	}
	for _, turn := range committed.Turns {
		if turn.TurnID == "G1" {
			t.Fatalf("ghost turn G1 minted: %+v", committed.Turns)
		}
	}
	if got := committedTurn(t, h, "T3").Status; got != "running" {
		t.Fatalf("T3 status = %q, want still running (authoritative inProgress)", got)
	}
	// Still-running verdict: pending entry retired, no closed-turn clear.
	if len(agent.pendClear) != 1 || agent.pendClear[0] != "sess" {
		t.Fatalf("ClearPendingTurnReconcile calls = %v, want [sess]", agent.pendClear)
	}
	if len(agent.cleared) != 0 {
		t.Fatalf("ClearReconciledTurn calls = %v, want none", agent.cleared)
	}
	// Session back to Ready: a fresh reconcile admission must succeed.
	if _, err := h.projectionKernel.BeginReconcileTransaction("codex-remote", "sess"); err != nil {
		t.Fatalf("session not Ready after still-running abort: %v", err)
	}
	abort, err := h.projectionKernel.AbortReconcileTransaction("codex-remote", "sess")
	if err != nil {
		t.Fatalf("abort leftover tx: %v", err)
	}
	if abort.PendingLive != 0 {
		t.Fatalf("abort replayed %d pendingLive rows, want 0", abort.PendingLive)
	}
}

// TestReconcileReadFailureAbortsAndKeepsPending: a failed summary read aborts
// the tx, replays the window's pendingLive into the committed reducer (R-4:
// live truth is never dropped), returns the session to Ready and keeps the
// thread pending for the next 3s round.
func TestReconcileReadFailureAbortsAndKeepsPending(t *testing.T) {
	h, agent := reconcileHarness(t)
	reconcileReadyKernel(t, h)
	agent.pending["sess"] = struct{}{}
	agent.active["sess"] = "T3"
	agent.coldErr = errors.New("transport reset")
	agent.gate = make(chan struct{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.drainTurnReconciles(context.Background(), "codex-remote")
	}()
	deadline := time.Now().Add(5 * time.Second)
	for agent.entered.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("summary read never started")
		}
		time.Sleep(2 * time.Millisecond)
	}
	// A live text_delta on the in-flight turn fences into pendingLive while the
	// tx holds the session in Hydrating. (Not a user_message: markRunning's
	// settleOtherOpenTurns supersession would settle T3 regardless, muddying
	// what the abort path itself did.)
	h.projectionKernel.IngestLive(EventMessage{
		BackendID: "codex-remote", SessionID: "sess", BridgeEpoch: h.eventPublisher.BridgeEpoch(),
		PerSessionSeq: 1, Event: "text_delta",
		Data: map[string]interface{}{"itemId": "a3", "turnId": "T3", "delta": "window delta"},
	})
	close(agent.gate)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain never finished")
	}

	// The closure never landed — T3 stays running…
	t3 := committedTurn(t, h, "T3")
	if t3.Status != "running" {
		t.Fatalf("T3 status = %q, want running (failed reconcile must not close)", t3.Status)
	}
	// …but the window's pendingLive was replayed into the committed reducer.
	if t3.Assistant == nil || len(t3.Assistant.Parts) == 0 ||
		t3.Assistant.Parts[len(t3.Assistant.Parts)-1].Text != "window delta" {
		t.Fatalf("T3 assistant = %+v, want the replayed window delta", t3.Assistant)
	}
	// Thread stays pending for the next round; nothing was cleared.
	if len(agent.pending) != 1 {
		t.Fatalf("pending set = %v, want [sess] retained for retry", agent.pending)
	}
	if len(agent.pendClear) != 0 || len(agent.cleared) != 0 {
		t.Fatalf("clears on failed reconcile: pendClear=%v cleared=%v, want none", agent.pendClear, agent.cleared)
	}
	// Session back to Ready.
	if _, err := h.projectionKernel.BeginReconcileTransaction("codex-remote", "sess"); err != nil {
		t.Fatalf("session not Ready after abort: %v", err)
	}
	if _, err := h.projectionKernel.AbortReconcileTransaction("codex-remote", "sess"); err != nil {
		t.Fatalf("abort leftover tx: %v", err)
	}
}

// TestReconcileLiveClosedTurnSkipsPending: a turn that already closed on the
// live path (codec entry gone) is skipped and its pending entry retired — no
// summary read, no transaction.
func TestReconcileLiveClosedTurnSkipsPending(t *testing.T) {
	h, agent := reconcileHarness(t)
	reconcileReadyKernel(t, h)
	agent.pending["sess"] = struct{}{}
	// No active codec turn: the live path already delivered the terminal event.
	agent.active["sess"] = ""

	h.drainTurnReconciles(context.Background(), "codex-remote")

	if agent.entered.Load() != 0 {
		t.Fatalf("summary read ran %d times, want 0 (live-closed turns skip the read)", agent.entered.Load())
	}
	if len(agent.pending) != 0 {
		t.Fatalf("pending set = %v, want retired", agent.pending)
	}
	if len(agent.cleared) != 0 {
		t.Fatalf("ClearReconciledTurn calls = %v, want none (skip path uses ClearPending)", agent.cleared)
	}
}

// TestBeginReconcileTransactionGuards covers the kernel admission rules: only
// a Ready session with committed state can open a reconcile tx, and Abort
// rejects anything that is not an active reconcile transaction.
func TestBeginReconcileTransactionGuards(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	// Fresh session: not Ready.
	if _, err := kernel.BeginReconcileTransaction("codex-remote", "never-ready"); err == nil {
		t.Fatal("begin on non-Ready session must fail")
	}
	// Ready but no committed state.
	kernel.MarkReady("codex-remote", "empty")
	if _, err := kernel.BeginReconcileTransaction("codex-remote", "empty"); err == nil {
		t.Fatal("begin without committed state must fail")
	}
	// Abort without an active reconcile tx.
	if _, err := kernel.AbortReconcileTransaction("codex-remote", "empty"); err == nil {
		t.Fatal("abort without active reconcile tx must fail")
	}
	// A normal cold hydrate tx is not a reconcile tx: Abort must reject it.
	kernel.MarkReady("codex-remote", "hydrating")
	kernel.reducer.Restore("codex-remote", "hydrating", SessionProjection{
		SessionID: "hydrating", SyncRev: 1, Execution: ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{{TurnID: "T1", Status: "completed"}},
	})
	if _, err := kernel.BeginHydrateTransaction(
		"codex-remote", "hydrating",
		ProjectionSourceDescriptor{Identity: "hydrating", Path: "/tmp/x.jsonl", Cursor: 10},
		false, false, true,
	); err != nil {
		t.Fatalf("cold hydrate begin: %v", err)
	}
	if _, err := kernel.AbortReconcileTransaction("codex-remote", "hydrating"); err == nil {
		t.Fatal("abort must reject a non-reconcile hydrate tx")
	}
}
