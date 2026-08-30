package gobridge

// get_session_projection_window RPC handler (PERF-S4B).
//
// Sits beside handleGetSessionProjection and reads the SAME committed Kernel head through
// the same snapshot fence (R4): Begin→slice→Complete keeps the window response and the
// post-cut projection_patch stream ordered on one connection sink. Pull-only; windows
// never touch the mailbox or a second pipe (R6/R10). Undeclared connections get the frozen
// compatibility answer: protocol.capability_required with no window field.

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleGetSessionProjectionWindow(conn Connection, msg WireMessage, agent core.Agent) {
	var params GetSessionProjectionWindowParams
	if msg.Params != nil {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
			return
		}
	}
	if params.BackendID == "" {
		params.BackendID = msg.BackendID
	}
	params.SessionID = h.resolveSessionIDForActiveSession(params.SessionID)

	// Frozen gate: only connections that negotiated projection_window_v1 (hello echo +
	// SetConnProjectionWindowV1) may call this RPC. The message names the capability the
	// client must declare (R9: explicit typed failure, never a fabricated window).
	retryableFalse := false
	if !h.eventPublisher.ConnProjectionWindowV1(conn) {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:      "protocol.capability_required",
			Message:   "get_session_projection_window requires the projection_window_v1 capability",
			Retryable: &retryableFalse,
		})
		return
	}

	switch params.Direction {
	case projectionWindowDirectionWindow0, projectionWindowDirectionOlder, projectionWindowDirectionNewer,
		projectionWindowDirectionLatest, projectionWindowDirectionLocate:
	default:
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "invalid_params",
			Message: "direction must be one of window_0|older|newer|latest|locate",
		})
		return
	}

	// R5 before any kernel work: limit is an assertable bound, not advice.
	if params.Limit > maxWindowTurns {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:      "projection_window.limit_exceeded",
			Message:   "limit exceeds maxWindowTurns (256)",
			Retryable: &retryableFalse,
		})
		return
	}

	bridgeEpoch := h.eventPublisher.BridgeEpoch()

	// R1/R6 scope validation BEFORE any kernel work: cursor scope is request-side state
	// (backend/session/epoch), so a mismatched or epoch-stale cursor fails fast without
	// hydrating a foreign backend. Retention misses stay inside the slice (they need the
	// committed turn set).
	retryableTrue := true
	if params.Direction == projectionWindowDirectionOlder || params.Direction == projectionWindowDirectionNewer {
		cursor, err := decodeProjectionWindowCursor(params.Cursor)
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{
				Code:      "cursor_stale",
				Message:   err.Error(),
				Retryable: &retryableTrue,
			})
			return
		}
		if err := validateProjectionWindowCursorScope(cursor, msg.BackendID, params.SessionID, bridgeEpoch); err != nil {
			wireErr := &WireError{Code: "projection_window.cursor_scope_mismatch", Message: err.Error(), Retryable: &retryableFalse}
			if errors.Is(err, errProjectionWindowCursorStale) {
				wireErr = &WireError{Code: "cursor_stale", Message: err.Error(), Retryable: &retryableTrue}
			}
			conn.SendResult(msg.RequestID, nil, wireErr)
			return
		}
	}

	// Same session-open side effects as the full projection pull: subscribe the conn so
	// projection_patch frames flow, and attach the live producer relay that feeds the
	// reducer for projection-only clients (no get_session_messages call would otherwise
	// watch file/API growth). Claude starts its relay through the beforeHydrate hook.
	h.subscribeConnToSession(conn, msg, params.SessionID)
	if msg.BackendID != "claude" && msg.BackendID != "claudecode" {
		h.startProjectionLiveRelay(params.SessionID, conn, msg.BackendID, agent, params.Directory)
	}

	forceColdInspection := params.SessionID != "" &&
		(msg.BackendID == "opencode" || msg.BackendID == "opencode-web" ||
			msg.BackendID == "grokbuild" ||
			msg.BackendID == "claude" || msg.BackendID == "claudecode" ||
			msg.BackendID == "deepseek" || msg.BackendID == "dsh-web" ||
			msg.BackendID == "codex-web")
	var beforeHydrate []func(ProjectionSourceDescriptor)
	if msg.BackendID == "claude" || msg.BackendID == "claudecode" {
		beforeHydrate = append(beforeHydrate, func(source ProjectionSourceDescriptor) {
			h.startClaudeSessionFileRelayAt(params.SessionID, conn, msg.BackendID, &source.Cursor)
		})
	}
	if err := h.ensureProjectionHydrated(
		msg.BackendID,
		params.SessionID,
		params.Directory,
		forceColdInspection,
		beforeHydrate...,
	); err != nil {
		code := "projection.hydrate_failed"
		retryable := false
		switch {
		case errors.Is(err, errProjectionHydrating):
			code = "projection.hydrating"
			retryable = true
		case errors.Is(err, errProjectionBackendNotMigrated):
			code = "projection.not_migrated"
		case errors.Is(err, errProjectionSessionNotFound):
			code = "projection.not_found"
		}
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:      code,
			Message:   err.Error(),
			Retryable: &retryable,
		})
		return
	}
	if msg.BackendID == "claude" || msg.BackendID == "claudecode" {
		h.startProjectionLiveRelay(params.SessionID, conn, msg.BackendID, agent, params.Directory)
	}

	// R11b delivery-mode registry: a connection's FIRST window RPC registers it as
	// a window-mode consumer of this (backend, session); structural commits route
	// per-connection from here on. Full-projection connections stay on the default.
	h.eventPublisher.SetConnProjectionDeliveryMode(conn, msg.BackendID, params.SessionID, ProjectionDeliveryWindow)

	// R11a/R11d: bounded producer hydration + honest hasOlder fact.
	hasOlderUpstream := h.producerHasOlderUpstreamFact(msg.BackendID, params.SessionID)
	if params.Direction == projectionWindowDirectionOlder {
		hydrated, hydrateErr := h.hydrateOlderFromUpstream(conn, msg.BackendID, params.SessionID, params.Cursor, agent)
		if hydrateErr != nil {
			wireErr := &WireError{Code: "projection.hydrate_failed", Message: hydrateErr.Error()}
			if errors.Is(hydrateErr, ErrUpstreamCursorStale) {
				// R11d typed recovery: the client discards its cursor chain and
				// re-issues window_0 (shared cursor_stale contract).
				retryable := true
				wireErr = &WireError{Code: "cursor_stale", Message: hydrateErr.Error(), Retryable: &retryable}
			}
			conn.SendResult(msg.RequestID, nil, wireErr)
			return
		}
		hasOlderUpstream = hydrated
	}

	// R4: admit at one kernel cut through the shared snapshot fence — the sliced window
	// and any post-cut patches share the connection's ordered sink.
	proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(
		conn,
		msg.BackendID,
		params.SessionID,
	)
	if snapshotErr != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "projection.not_ready",
			Message: snapshotErr.Error(),
		})
		return
	}

	response, sliceErr := sliceProjectionWindowWithUpstream(msg.BackendID, params.SessionID, bridgeEpoch, proj, params, hasOlderUpstream)
	if sliceErr != nil {
		// Release the fence with a typed error result (R9: explicit typed failure, never a
		// fabricated or empty success window; the fence must not dangle for the next pull).
		wireErr := &WireError{Code: "rpc.error", Message: sliceErr.Error(), Retryable: &retryableFalse}
		switch {
		case errors.Is(sliceErr, errProjectionWindowScopeMismatch):
			wireErr = &WireError{Code: "projection_window.cursor_scope_mismatch", Message: sliceErr.Error(), Retryable: &retryableFalse}
		case errors.Is(sliceErr, errProjectionWindowCursorStale):
			wireErr = &WireError{Code: "cursor_stale", Message: sliceErr.Error(), Retryable: &retryableTrue}
		case errors.Is(sliceErr, errProjectionWindowLocateOut):
			wireErr = &WireError{Code: "projection_window.locate_out_of_window", Message: sliceErr.Error(), Retryable: &retryableFalse}
		}
		if err := h.eventPublisher.CompleteProjectionSnapshotError(conn, admission, msg.RequestID, wireErr); err != nil {
			slog.Warn("go-bridge: window slice fence release failed", "requestId", msg.RequestID, "error", err)
		}
		return
	}

	if err := h.eventPublisher.CompleteProjectionSnapshot(conn, admission, msg.RequestID, response); err != nil {
		slog.Warn("go-bridge: projection window response enqueue failed", "requestId", msg.RequestID, "error", err)
	}
	// turn_detail_lazy_v1 rule 3/4 bookkeeping: the replica now holds these
	// turns; detail/state commits route content vs no-op by this set.
	turnIDs := make([]string, 0, len(response.Turns))
	for _, turn := range response.Turns {
		turnIDs = append(turnIDs, turn.TurnID)
	}
	h.eventPublisher.RecordConnWindowTurns(conn, msg.BackendID, params.SessionID, turnIDs)
}
