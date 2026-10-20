package gobridge

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// S-3 v2 handler：get_session_projection_window_v2（冻结规范 v3；
// bridge-v1.md §Projection Window v2 Summary）。复用 v1 window handler 的全部
// 门控/游标/hydrate/fence 语义，窗口选择经 sliceProjectionWindowWithUpstream，
// 行形态按 itemsView 组装（summary = TurnSummaryDescriptor；full = v1 行 + scalarState）。

func (h *Handlers) handleGetSessionProjectionWindowV2(conn Connection, msg WireMessage, agent core.Agent) {
	var params GetSessionProjectionWindowV2Params
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{
				Code:    "invalid_params",
				Message: "get_session_projection_window_v2 params decode: " + err.Error(),
			})
			return
		}
	}
	if params.BackendID == "" {
		params.BackendID = msg.BackendID
	}
	params.SessionID = h.resolveSessionIDForActiveSession(params.SessionID)

	retryableFalse := false
	retryableTrue := true
	if !h.eventPublisher.ConnProjectionWindowV2(conn) {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:      "protocol.capability_required",
			Message:   "get_session_projection_window_v2 requires the projection_window_v2 capability",
			Retryable: &retryableFalse,
		})
		return
	}
	// F-01 修复（2026-10-20 Codex 评审 §5.1）：producer 范围门（冻结 §1：producer
	// 落地 = Claude；§8 推广走独立冻结）。echo 是连接级布尔，不表达 per-backend
	// 生产范围——服务端拒绝在订阅/hydrate/fence 等副作用之前（评审 §8.3）。
	// 错误码 = 冻结错误集既有 invalid_params（明确 backend message；不把「能力
	// 已 echo 但该 backend 未交付」误报为 capability_required，评审 §5.1）。
	if msg.BackendID != "claude" && msg.BackendID != "claudecode" {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code: "invalid_params",
			Message: "projection_window_v2 summary producer is claude-only in this delivery; backend " +
				msg.BackendID + " is not in the delivered producer set",
			Retryable: &retryableFalse,
		})
		return
	}
	switch params.ItemsView {
	case "summary", "full":
	default:
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "invalid_params",
			Message: "itemsView must be summary|full",
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
	if params.Limit > maxWindowTurns {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:      "projection_window.limit_exceeded",
			Message:   "limit exceeds maxWindowTurns (256)",
			Retryable: &retryableFalse,
		})
		return
	}

	bridgeEpoch := h.eventPublisher.BridgeEpoch()

	// 游标 scope 校验先于任何 kernel 工作（R1/R6，同 v1）。
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

	h.subscribeConnToSession(conn, msg, params.SessionID, "")
	if msg.BackendID != "claude" && msg.BackendID != "claudecode" {
		h.startProjectionLiveRelay(params.SessionID, conn, msg.BackendID, agent, params.Directory)
	}

	coldGate := true
	if h.projectionKernel.Status(msg.BackendID, params.SessionID).Phase == ProjectionHydrateReady &&
		projectionSourceUnchangedSinceCommit(h.projectionKernel, msg.BackendID, params.SessionID) {
		coldGate = false
	}
	forceColdInspection := coldGate && params.SessionID != "" &&
		(msg.BackendID == "claude" || msg.BackendID == "claudecode" ||
			msg.BackendID == "opencode" || msg.BackendID == "opencode-web" ||
			msg.BackendID == "grokbuild" || msg.BackendID == "deepseek" ||
			msg.BackendID == "dsh-web" || msg.BackendID == "codex-web")
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

	h.eventPublisher.SetConnProjectionDeliveryMode(conn, msg.BackendID, params.SessionID, ProjectionDeliveryWindow)

	hasOlderUpstream := h.producerHasOlderUpstreamFact(msg.BackendID, params.SessionID)
	if params.Direction == projectionWindowDirectionOlder {
		hydrated, hydrateErr := h.hydrateOlderFromUpstream(conn, msg.BackendID, params.SessionID, params.Cursor, agent)
		if hydrateErr != nil {
			wireErr := &WireError{Code: "projection.hydrate_failed", Message: hydrateErr.Error()}
			if errors.Is(hydrateErr, ErrUpstreamCursorStale) {
				wireErr = &WireError{Code: "cursor_stale", Message: hydrateErr.Error(), Retryable: &retryableTrue}
			}
			conn.SendResult(msg.RequestID, nil, wireErr)
			return
		}
		hasOlderUpstream = hydrated
	}

	proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(conn, msg.BackendID, params.SessionID)
	if snapshotErr != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "projection.not_ready",
			Message: snapshotErr.Error(),
		})
		return
	}

	// 窗口选择复用 v1 切片（全部 R 规则/游标/locate 语义不变）。
	v1Params := GetSessionProjectionWindowParams{
		SessionID:    params.SessionID,
		Directory:    params.Directory,
		BackendID:    params.BackendID,
		Direction:    params.Direction,
		Cursor:       params.Cursor,
		Limit:        params.Limit,
		AnchorTurnID: params.AnchorTurnID,
	}
	v1Response, sliceErr := sliceProjectionWindowWithUpstream(msg.BackendID, params.SessionID, bridgeEpoch, proj, v1Params, hasOlderUpstream)
	if sliceErr != nil {
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
			slog.Warn("go-bridge: window v2 slice fence release failed", "requestId", msg.RequestID, "error", err)
		}
		return
	}

	heldTurnIDs := make([]string, 0, len(v1Response.Turns))
	for _, turn := range v1Response.Turns {
		heldTurnIDs = append(heldTurnIDs, turn.TurnID)
	}
	maxExceptionBytes := int(h.eventPublisher.ProjectionMaxBytes(conn))

	if params.ItemsView == "full" {
		response := ProjectionWindowV2FullResponse{
			Window:      v1Response.Window,
			Turns:       v1Response.Turns,
			ScalarState: scalarStateFromProjection(proj),
			SyncRev:     v1Response.SyncRev,
			ItemsView:   "full",
		}
		if err := h.eventPublisher.CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, response, heldTurnIDs); err != nil {
			slog.Warn("go-bridge: projection window v2 response enqueue failed", "requestId", msg.RequestID, "error", err)
		}
		return
	}

	rows, budget, summarizeErr := summarizeWindowTurns(v1Response.Turns, maxExceptionBytes)
	if summarizeErr != nil {
		wireErr := &WireError{Code: "rpc.error", Message: summarizeErr.Error(), Retryable: &retryableFalse}
		if errors.Is(summarizeErr, errSummaryOversized) {
			// fail-closed：例外通道自身超 projectionMaxBytes——不静默截断权限卡/待答问题。
			wireErr = &WireError{Code: "summary.oversized", Message: summarizeErr.Error(), Retryable: &retryableFalse}
		}
		if err := h.eventPublisher.CompleteProjectionSnapshotError(conn, admission, msg.RequestID, wireErr); err != nil {
			slog.Warn("go-bridge: window v2 summarize fence release failed", "requestId", msg.RequestID, "error", err)
		}
		return
	}
	response := ProjectionWindowV2SummaryResponse{
		Window:        v1Response.Window,
		Turns:         rows,
		ScalarState:   scalarStateFromProjection(proj),
		SyncRev:       v1Response.SyncRev,
		ItemsView:     "summary",
		SummaryBudget: budget,
	}
	if err := h.eventPublisher.CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, response, heldTurnIDs); err != nil {
		slog.Warn("go-bridge: projection window v2 summary response enqueue failed", "requestId", msg.RequestID, "error", err)
	}
	slog.Debug("go-bridge: window v2 summary served",
		"requestId", msg.RequestID,
		"sessionPrefix", sessionPrefix8(params.SessionID),
		"rows", len(rows),
		"payloadBytes", budget.PayloadBytes,
		"folded", len(budget.FoldedTurnIds),
		"exceptions", len(budget.Exceptions),
	)
}

func sessionPrefix8(sessionID string) string {
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}
