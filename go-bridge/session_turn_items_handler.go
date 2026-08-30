package gobridge

// session_turn_items (turn_detail_lazy_v1, unified-bridge-protocol.md §11.7 —
// frozen 2026-08-30 BEFORE this handler). Mac pulls the target turn's items to
// EOF and merges them ATOMICALLY into the projection Kernel; canonical items
// never enter the RPC result — the projection snapshot/patch pipes are the only
// content writers. The ack is success-shaped for both terminal states; only
// request-level errors are WireErrors.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	"github.com/openAgi2/cordcode-macbridge/core"
)

type turnDetailFlight struct {
	done chan struct{}
	ack  map[string]interface{}
}

// sessionTurnItemsAck is the success-shaped ack (§11.7): loaded and failed
// alike carry the commit's syncRev; failed adds reasonCode.
func sessionTurnItemsAck(state string, syncRev int, reasonCode string) map[string]interface{} {
	ack := map[string]interface{}{
		"detailLoadState": state,
		"syncRev":         syncRev,
	}
	if reasonCode != "" {
		ack["reasonCode"] = reasonCode
	}
	return ack
}

func (h *Handlers) handleSessionTurnItems(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		SessionID string `json:"sessionId"`
		TurnID    string `json:"turnId"`
	}
	// G3 #10 诊断（owner 2026-08-30）：request-level 错误与终态 ack 此前均无结果日志，
	// 真机「点击无反应 / 加载失败」无法与 iOS 端 memo/catch 集合对账。每个出口都留痕。
	sendErr := func(code, message string, retryable bool) {
		slog.Info("go-bridge: session_turn_items error",
			"requestId", msg.RequestID, "backendId", msg.BackendID,
			"sessionId", params.SessionID, "turnId", params.TurnID,
			"code", code, "message", message)
		r := retryable
		conn.SendResult(msg.RequestID, nil, &WireError{Code: code, Message: message, Retryable: &r})
	}
	sendAck := func(ack map[string]interface{}) {
		slog.Info("go-bridge: session_turn_items ack",
			"requestId", msg.RequestID, "backendId", msg.BackendID,
			"sessionId", params.SessionID, "turnId", params.TurnID,
			"detailLoadState", ack["detailLoadState"],
			"syncRev", ack["syncRev"], "reasonCode", ack["reasonCode"])
		conn.SendResult(msg.RequestID, ack, nil)
	}

	// Capability gate: hello-negotiated turn_detail_lazy_v1 (same discipline as
	// projection_window_v1). The per-session legacy gate follows below.
	if !h.eventPublisher.ConnTurnDetailV1(conn) {
		sendErr("protocol.capability_required",
			"session_turn_items requires the turn_detail_lazy_v1 capability", false)
		return
	}
	if msg.BackendID != "codex-remote" {
		// §11.7 v1: only codex-remote declares turn_detail_lazy_v1.
		sendErr("unsupported_capability",
			fmt.Sprintf("session_turn_items v1 is codex-remote only, not %s", msg.BackendID), false)
		return
	}

	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			sendErr("invalid_params", "session_turn_items params decode: "+err.Error(), false)
			return
		}
	}
	if params.SessionID == "" || params.TurnID == "" {
		sendErr("invalid_params", "session_turn_items requires sessionId and turnId", false)
		return
	}

	// Hydrate gate: turn_not_found is Kernel-adjudicated (never asks upstream).
	if err := h.ensureProjectionHydrated(msg.BackendID, params.SessionID, "", false); err != nil {
		code := "projection.hydrate_failed"
		retryable := false
		switch {
		case errors.Is(err, errProjectionHydrating):
			code, retryable = "projection.hydrating", true
		case errors.Is(err, errProjectionSessionNotFound):
			code = "session_not_found"
		}
		sendErr(code, err.Error(), retryable)
		return
	}

	proj, ok := h.projectionKernel.Snapshot(msg.BackendID, params.SessionID)
	if !ok {
		sendErr("session_not_found", "projection not available for "+params.SessionID, false)
		return
	}
	var target *TurnProjection
	for i := range proj.Turns {
		if proj.Turns[i].TurnID == params.TurnID {
			target = &proj.Turns[i]
			break
		}
	}
	if target == nil {
		sendErr("turn_not_found",
			fmt.Sprintf("turn %s is not in the committed projection of %s", params.TurnID, params.SessionID), false)
		return
	}
	if target.Status != "completed" {
		// §11.7 invalid_params: target not a completed turn — no fetch at all.
		sendErr("invalid_params",
			fmt.Sprintf("turn %s is %q, not completed", params.TurnID, target.Status), false)
		return
	}

	// Per-session legacy gate: the producer fact records the thread's declared
	// historyMode at cold open. Legacy (or unknown-mode) sessions never expose
	// session_turn_items (§11.7 method gating).
	if state := h.loadProducerState(msg.BackendID, params.SessionID); state == nil || state.HistoryMode != "paginated" {
		sendErr("unsupported_capability",
			"session_turn_items requires a paginated-history session", false)
		return
	}

	// Idempotent repeat: already loaded → same terminal ack without refetch
	// (§11.7). The journal recovers the original commit rev when still
	// retained; otherwise the current syncRev is a conservative watermark.
	if target.DetailLoadState == DetailStateLoaded {
		sendAck(sessionTurnItemsAck(DetailStateLoaded, h.loadedDetailWatermark(msg.BackendID, params.SessionID, params.TurnID, proj.SyncRev), ""))
		return
	}

	// Lazy orphan recovery: a loading turn with no in-flight leader is a
	// crashed leader's leftover — recover it to failed(interrupted) FIRST so
	// the retry starts from an honest failed state (restarts recover at
	// checkpoint restore; this covers in-place leader loss).
	flightKey := projectionDeliveryKey(msg.BackendID, params.SessionID) + "|" + params.TurnID
	if target.DetailLoadState == DetailStateLoading {
		if _, inFlight := h.turnDetailFlights.Load(flightKey); !inFlight {
			h.recoverOrphanLoadingTurn(msg.BackendID, params.SessionID, params.TurnID, target.TurnGeneration)
		}
	}

	// Singleflight: the leader owns the fetch; followers wait for the SAME
	// terminal commit and mirror the leader's ack verbatim (same terminal
	// syncRev — never a mid-flight loading ack). The flight pointer is read
	// after done closes, so the ack read is race-free.
	candidate := &turnDetailFlight{done: make(chan struct{})}
	for {
		actual, loaded := h.turnDetailFlights.LoadOrStore(flightKey, candidate)
		flight := actual.(*turnDetailFlight)
		if !loaded {
			ack := h.runTurnDetailFetch(conn, msg.BackendID, params.SessionID, params.TurnID)
			flight.ack = ack
			close(flight.done)
			h.turnDetailFlights.Delete(flightKey)
			sendAck(ack)
			return
		}
		<-flight.done
		sendAck(flight.ack)
		return
	}
}

// loadedDetailWatermark prefers the original loaded-commit rev (journal) and
// falls back to the current syncRev — appliedRev >= current implies
// appliedRev >= original, so the fallback is conservative, never early.
func (h *Handlers) loadedDetailWatermark(backendID, sessionID, turnID string, currentRev int) int {
	if original, found := h.eventPublisher.LoadedDetailRev(backendID, sessionID, turnID); found && original <= currentRev {
		return original
	}
	return currentRev
}

// recoverOrphanLoadingTurn commits failed(interrupted) for a loading turn with
// no active leader (§11.7 orphan recovery; the in-place complement of the
// checkpoint-restore scan). A stale fence means another writer moved the turn —
// its truth stays.
func (h *Handlers) recoverOrphanLoadingTurn(backendID, sessionID, turnID string, generation int) {
	_, patches, err := h.projectionKernel.CommitTurnStateOps(backendID, sessionID, []TurnStateOp{{
		TurnID:          turnID,
		DetailLoadState: DetailStateFailed,
		ReasonCode:      "interrupted",
		TurnGeneration:  generation,
	}})
	if err != nil {
		return
	}
	h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, nil)
}

// runTurnDetailFetch is the singleflight leader: loading admission → bounded
// fetch → terminal commit (merge or failed state). Returns the success-shaped
// ack; every failure path is a terminal failed commit + failed ack.
func (h *Handlers) runTurnDetailFetch(requester Connection, backendID, sessionID, turnID string) map[string]interface{} {
	findTurn := func(proj SessionProjection) *TurnProjection {
		for i := range proj.Turns {
			if proj.Turns[i].TurnID == turnID {
				return &proj.Turns[i]
			}
		}
		return nil
	}
	failTerminal := func(reasonCode string) map[string]interface{} {
		// Terminal failed commit at the turn's CURRENT generation (a bump since
		// the loading admission means a concurrent mutation won — stale_turn).
		proj, ok := h.projectionKernel.Snapshot(backendID, sessionID)
		if !ok {
			return sessionTurnItemsAck(DetailStateFailed, 0, "upstream_error")
		}
		turn := findTurn(proj)
		if turn == nil {
			return sessionTurnItemsAck(DetailStateFailed, proj.SyncRev, "stale_turn")
		}
		if turn.DetailLoadState == DetailStateLoaded {
			// Another request already merged the detail — its truth wins.
			return sessionTurnItemsAck(DetailStateLoaded, h.loadedDetailWatermark(backendID, sessionID, turnID, proj.SyncRev), "")
		}
		_, patches, err := h.projectionKernel.CommitTurnStateOps(backendID, sessionID, []TurnStateOp{{
			TurnID:          turnID,
			DetailLoadState: DetailStateFailed,
			ReasonCode:      reasonCode,
			TurnGeneration:  turn.TurnGeneration,
		}})
		if err != nil {
			// A stale fence here means the turn moved between the snapshot and
			// the commit: the new truth stays; this request is typed stale.
			return sessionTurnItemsAck(DetailStateFailed, proj.SyncRev, "stale_turn")
		}
		h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, requester)
		return sessionTurnItemsAck(DetailStateFailed, patches[len(patches)-1].SyncRev, reasonCode)
	}

	proj, ok := h.projectionKernel.Snapshot(backendID, sessionID)
	if !ok {
		return sessionTurnItemsAck(DetailStateFailed, 0, "upstream_error")
	}
	turn := findTurn(proj)
	if turn == nil || turn.Status != "completed" {
		return failTerminal("stale_turn")
	}
	generation := turn.TurnGeneration

	// 1. Loading admission into the projection SoT (§11.7 state machine).
	_, patches, err := h.projectionKernel.CommitTurnStateOps(backendID, sessionID, []TurnStateOp{{
		TurnID:          turnID,
		DetailLoadState: DetailStateLoading,
		TurnGeneration:  generation,
	}})
	if err == nil {
		h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, requester)
	} else if errors.Is(err, ErrTurnStateStale) {
		// The turn moved under the admission (generation bump / re-activation).
		return failTerminal("stale_turn")
	} else {
		return sessionTurnItemsAck(DetailStateFailed, proj.SyncRev, "upstream_error")
	}

	// 2. Bounded fetch through the agent's detail surface.
	agent, haveAgent := h.getFirstAgentByName(backendID)
	if !haveAgent {
		return failTerminal("upstream_error")
	}
	reader, canRead := agent.(core.TurnDetailReader)
	if !canRead {
		return failTerminal("unsupported_item_type")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	detailTurn, err := reader.ReadTurnDetail(ctx, sessionID, turnID)
	if err != nil {
		return failTerminal(turnDetailReasonCode(err))
	}
	if len(detailTurn.SkippedTypes) > 0 {
		// SkippedTypes is diagnostic, NOT a discard-and-succeed switch: an
		// unmappable decoded item fails the whole turn atomically.
		return failTerminal("unsupported_item_type")
	}

	// 3. Atomic merge: replace_parts + loaded in ONE Kernel transaction.
	mapped := upstreamSummaryTurnsToProjection([]core.TurnScopedHistoryTurn{detailTurn})
	var detailParts []ProjectionPart
	if len(mapped) == 1 && mapped[0].Assistant != nil {
		detailParts = mapped[0].Assistant.Parts
	}
	_, mergePatches, err := h.projectionKernel.MergeHistoricalTurnDetail(
		backendID, sessionID, turnID, generation, detailParts,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrTurnStateStale),
			errors.Is(err, ErrDetailTargetRunning),
			errors.Is(err, ErrDetailTargetMissing):
			return failTerminal("stale_turn")
		default:
			return failTerminal("upstream_error")
		}
	}
	h.eventPublisher.PublishProjectionDetail(backendID, sessionID, mergePatches, requester)
	return sessionTurnItemsAck(DetailStateLoaded, mergePatches[len(mergePatches)-1].SyncRev, "")
}

// turnDetailReasonCode maps the agent's typed detail-fetch errors onto the
// frozen §11.7 closed set.
func turnDetailReasonCode(err error) string {
	switch {
	case errors.Is(err, codexremote.ErrTurnItemsMaxPages):
		return "max_pages"
	case errors.Is(err, codexremote.ErrTurnItemsMaxBytes):
		return "max_bytes"
	case errors.Is(err, codexremote.ErrTurnItemsTimeout):
		return "timeout"
	case errors.Is(err, codexremote.ErrUnknownThreadItem),
		errors.Is(err, codexremote.ErrForeignTurnItem):
		return "unsupported_item_type"
	default:
		return "upstream_error"
	}
}
