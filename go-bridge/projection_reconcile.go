package gobridge

import (
	"context"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// S-2 (disconnect-resilience plan §3.2): reconnect-time turn reconciliation.
// A turn that terminated inside the disconnect window stays "running" in the
// projection — the missed terminal event is never replayed (E-1: the official
// protocol has no controller-leg cursor). For each thread the live codec still
// tracks as in-flight, the runner reads the authoritative summary page and
// closes the turn through a READY-safe reconcile transaction whose baseline is
// the committed projection (older-window turns, producer state and goal/detail
// manifest all preserved — F-R4-1).

// reconcileReadBudget bounds one thread's authoritative summary read. The
// drain runs inside the 3s catalog loop goroutine, so the budget also bounds
// how long a wedged backend can stall the attach cadence.
const reconcileReadBudget = 30 * time.Second

// drainTurnReconciles consumes the agent's pending reconcile set after the
// catalog attach step (plan §3.2 wiring element 4). Failed threads stay
// pending and retry on the next 3s round.
func (h *Handlers) drainTurnReconciles(ctx context.Context, backendID string) {
	agent, ok := h.getAgent(backendID)
	if !ok {
		return
	}
	reconciler, ok := agent.(core.TurnReconciler)
	if !ok {
		return
	}
	// Drain the data-free wake signal; the pending set is the truth.
	select {
	case <-reconciler.TurnReconcileSignals():
	default:
	}
	for _, threadID := range reconciler.PendingTurnReconciles() {
		h.reconcileThreadTurns(ctx, backendID, threadID, agent, reconciler)
	}
}

// isTerminalUpstreamStatus reports whether an official Turn.status closes a
// turn (the cold path's §9.2 vocabulary; inProgress/unknown never close).
func isTerminalUpstreamStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "interrupted"
}

// upstreamTerminalToProjection maps an official terminal status to the
// projection's terminal vocabulary (failed → error, interrupted → aborted).
func upstreamTerminalToProjection(status string) string {
	switch status {
	case "completed":
		return "completed"
	case "failed":
		return "error"
	case "interrupted":
		return "aborted"
	default:
		return ""
	}
}

// reconcileThreadTurns closes one thread's disconnect-window turns from the
// authoritative summary. Producer filter rules (plan §3.2, ghost-turn
// protection): only baseline turns that the summary shows terminal get a
// terminal event (mirroring the cold path §9.2 closure discipline); summary-only
// turns, first-page fall-offs and terminal mismatches are log-only — no ghost
// turns, no silent rewrites of settled history.
func (h *Handlers) reconcileThreadTurns(
	ctx context.Context,
	backendID, threadID string,
	agent core.Agent,
	reconciler core.TurnReconciler,
) {
	// The turn already closed on the live path (codec entry gone): nothing to
	// reconcile, just retire the pending entry.
	if reconciler.ActiveTurnForReconcile(threadID) == "" {
		reconciler.ClearPendingTurnReconcile(threadID)
		return
	}
	reader, ok := agent.(core.ColdHistoryReader)
	if !ok {
		slog.Warn("go-bridge: turn reconcile agent lacks cold history; keeping pending",
			"backendID", backendID, "thread", threadID)
		return
	}
	if _, err := h.projectionKernel.BeginReconcileTransaction(backendID, threadID); err != nil {
		// Not Ready (e.g. a concurrent cold hydrate owns the session) or no
		// committed state: retry on the next 3s round.
		slog.Info("go-bridge: turn reconcile begin deferred",
			"backendID", backendID, "thread", threadID, "error", err)
		return
	}
	// The tx baseline is the committed snapshot restored at Begin; read it
	// from the transaction so a live event landing between Ready and Begin
	// cannot produce a stale baseline.
	baseline, ok := h.projectionKernel.HydrateSnapshot(backendID, threadID)
	if !ok {
		h.abortTurnReconcile(backendID, threadID, "tx baseline missing")
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, reconcileReadBudget)
	result, err := reader.ReadColdHistory(readCtx, threadID)
	cancel()
	if err != nil {
		h.abortTurnReconcile(backendID, threadID, "summary read failed: "+err.Error())
		return
	}
	summary := map[string]core.TurnScopedHistoryTurn{}
	if result != nil && result.Page != nil {
		for _, t := range result.Page.Turns {
			if t.TurnID != "" {
				summary[t.TurnID] = t
			}
		}
	}
	type turnClosure struct {
		event string
		data  map[string]interface{}
	}
	var closures []turnClosure
	baselineTurns := make(map[string]struct{}, len(baseline.Turns))
	for i := range baseline.Turns {
		turn := baseline.Turns[i]
		if turn.TurnID == "" {
			continue
		}
		baselineTurns[turn.TurnID] = struct{}{}
		upstream, inSummary := summary[turn.TurnID]
		switch {
		case turn.Status == "completed" || turn.Status == "aborted" || turn.Status == "error":
			// Rule 4: a terminal mismatch between baseline and summary is a
			// log-only warning — never silently rewrite settled history.
			if inSummary && isTerminalUpstreamStatus(upstream.Status) &&
				upstreamTerminalToProjection(upstream.Status) != turn.Status {
				slog.Warn("go-bridge: turn reconcile terminal mismatch; keeping baseline",
					"backendID", backendID, "thread", threadID, "turn", turn.TurnID,
					"baselineStatus", turn.Status, "summaryStatus", upstream.Status)
			}
		case !inSummary:
			// Rule 3: a non-terminal turn that fell off the summary first page
			// stays running — the window delivered a full page of newer turns.
			slog.Info("go-bridge: turn reconcile turn fell off summary first page; keeping running",
				"backendID", backendID, "thread", threadID, "turn", turn.TurnID)
		case upstream.Status == "completed":
			data := map[string]interface{}{"turnId": turn.TurnID, "done": true, "reason": "official_turn_status"}
			if upstream.DurationMs > 0 {
				data["durationMs"] = upstream.DurationMs
			}
			closures = append(closures, turnClosure{event: "turn_completed", data: data})
		case upstream.Status == "failed":
			closures = append(closures, turnClosure{event: "turn_error", data: map[string]interface{}{
				"turnId": turn.TurnID, "error": upstream.ErrorMessage, "reason": "official_turn_status"}})
		case upstream.Status == "interrupted":
			closures = append(closures, turnClosure{event: "turn_aborted", data: map[string]interface{}{
				"turnId": turn.TurnID, "reason": "official_turn_status"}})
		default:
			// Summary still shows the turn in flight: keep running (SSV2
			// rule 7 — never guess completion).
		}
	}
	// Rule 2: summary turns absent from the baseline are log-only — a terminal
	// event for an unknown turn would mint a bare ghost turn.
	for turnID, t := range summary {
		if _, ok := baselineTurns[turnID]; !ok {
			slog.Info("go-bridge: turn reconcile summary turn absent from baseline; logging only",
				"backendID", backendID, "thread", threadID, "turn", turnID, "status", t.Status)
		}
	}
	if len(closures) == 0 {
		// Authoritative verdict: still running (or nothing closable). Discard
		// the transaction — Abort replays the window's pendingLive and returns
		// the session to Ready without a pointless baseline re-publish.
		h.abortTurnReconcile(backendID, threadID, "no terminal closures")
		reconciler.ClearPendingTurnReconcile(threadID)
		return
	}
	epoch := h.eventPublisher.BridgeEpoch()
	for _, c := range closures {
		h.projectionKernel.ApplyHydrateEvent(backendID, threadID, epoch, c.event, c.data)
	}
	commit, err := h.projectionKernel.CommitHydrateTransaction(backendID, threadID)
	if err != nil {
		h.abortTurnReconcile(backendID, threadID, "commit failed: "+err.Error())
		return
	}
	// Reuse the hydrate commit post-processing (release deferred candidates +
	// publish patch) but NOT persistCodexProducerSeed or checkpoint staging:
	// the reconcile never moves the source cut or the producer state.
	h.releaseDeferredPushCandidates(commit.AppliedPendingEventIDs)
	if commit.PendingPatch != nil {
		h.eventPublisher.PublishProjectionPatch(backendID, threadID, *commit.PendingPatch)
	}
	reconciler.ClearReconciledTurn(threadID)
	slog.Info("go-bridge: turn reconcile committed",
		"backendID", backendID, "thread", threadID,
		"closedTurns", len(closures), "pendingLive", commit.PendingLive,
		"headRev", commit.Projection.SyncRev)
}

// abortTurnReconcile discards the active reconcile transaction and reuses the
// hydrate post-processing on its result (release deferred candidates + publish
// the pendingLive-replay patch). The thread stays in the pending set for the
// next 3s round — the committed projection was never mutated.
func (h *Handlers) abortTurnReconcile(backendID, threadID, reason string) {
	abort, err := h.projectionKernel.AbortReconcileTransaction(backendID, threadID)
	if err != nil {
		slog.Warn("go-bridge: turn reconcile abort failed",
			"backendID", backendID, "thread", threadID, "error", err)
		return
	}
	h.releaseDeferredPushCandidates(abort.AppliedPendingEventIDs)
	if abort.PendingPatch != nil {
		h.eventPublisher.PublishProjectionPatch(backendID, threadID, *abort.PendingPatch)
	}
	slog.Warn("go-bridge: turn reconcile aborted; retrying next round",
		"backendID", backendID, "thread", threadID, "reason", reason,
		"pendingLiveReplayed", abort.PendingLive)
}
