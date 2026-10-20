package gobridge

// get_session_projection_anchor_newer RPC handler (S-7, 方案 v30 §3.8).
//
// 与 handleGetSessionProjectionWindow 同族：读同一 committed Kernel head、走同一
// snapshot fence（Begin→slice→Complete 保持对账响应与 post-cut projection_patch 在
// 同一连接 sink 上有序）；held-turn 注册在同一完成事务内（含 anchorRefresh 与
// changedTurns——漏注册会永久 strand 并发 detail commit，P0-2）。Pull-only。
// 未协商连接得到 frozen 兼容答案：protocol.capability_required。

import (
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleGetSessionProjectionAnchorNewer(conn Connection, msg WireMessage, agent core.Agent) {
	// F-25：与 full 同构的 projection_rpc trace（requestId/backend/sessionID/outcome），
	// §7.3 重试指标在 window 生效后可按本 trace 计数。
	requestStartedAt := time.Now()
	var params AnchorNewerParams
	if msg.Params != nil {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
			return
		}
	}
	isContinuation := params.CorrectionCursor != ""
	outcomeKind := "anchor_first_page"
	if isContinuation {
		outcomeKind = "anchor_continuation"
	}
	logProjectionRPCTrace("mac_receive", msg, params.SessionID, 0, -1, outcomeKind, nil)
	if params.BackendID == "" {
		params.BackendID = msg.BackendID
	}
	params.SessionID = h.resolveSessionIDForActiveSession(params.SessionID)

	retryableFalse := false
	retryableTrue := true

	// Frozen gate：仅已协商 projection_window_anchor_newer_v1 的连接可调用。
	if !h.eventPublisher.ConnAnchorNewerV1(conn) {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:      "protocol.capability_required",
			Message:   "get_session_projection_anchor_newer requires the projection_window_anchor_newer_v1 capability",
			Retryable: &retryableFalse,
		})
		return
	}
	if params.AnchorTurnID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "invalid_params",
			Message: "anchorTurnId is required (bare turn-id watermark)",
		})
		return
	}
	if len(params.BaseDigests) > anchorNewerMaxDigestEntries {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:      "invalid_params",
			Message:   "baseDigests exceeds the 256-entry review-set cap",
			Retryable: &retryableFalse,
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

	// 与 window/full 拉取相同的 session-open 副作用：订阅连接、挂 live producer relay。
	h.subscribeConnToSession(conn, msg, params.SessionID, "")
	if msg.BackendID != "claude" && msg.BackendID != "claudecode" {
		h.startProjectionLiveRelay(params.SessionID, conn, msg.BackendID, agent, "")
	}

	// S-2/F-14：kernel Ready 且 committed 源未推进（stat-only）则跳过冷检——
	// 对齐 full 路径 sinceRev=0 门语义，window_0 不再每请求强制冷源检查。
	coldGate := true
	if h.projectionKernel.Status(msg.BackendID, params.SessionID).Phase == ProjectionHydrateReady &&
		projectionSourceUnchangedSinceCommit(h.projectionKernel, msg.BackendID, params.SessionID) {
		coldGate = false
	}
	forceColdInspection := coldGate && params.SessionID != "" &&
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
		"",
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
		h.startProjectionLiveRelay(params.SessionID, conn, msg.BackendID, agent, "")
	}

	h.eventPublisher.SetConnProjectionDeliveryMode(conn, msg.BackendID, params.SessionID, ProjectionDeliveryWindow)

	// 同一 snapshot fence 准入（对账页与 post-cut patch 有序）。
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

	// S-3 v2：itemsView 只接受 summary（additive 单值）；非法值 = invalid_params。
	// F-01 修复（2026-10-20 Codex 评审 §5.2 末）：summary 对账入口同样受 producer
	// 范围门（claude-only）——普通 full 形态 anchor 不受限（评审明确不得一并禁掉）。
	if params.ItemsView == "summary" && msg.BackendID != "claude" && msg.BackendID != "claudecode" {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code: "invalid_params",
			Message: "anchor-newer summary form (itemsView=summary) is claude-only in this delivery; backend " +
				msg.BackendID + " is not in the delivered producer set",
			Retryable: &retryableFalse,
		})
		return
	}
	if params.ItemsView != "" && params.ItemsView != "summary" {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "invalid_params",
			Message: "itemsView supports \"summary\" only",
		})
		return
	}
	result, sliceErr := sliceAnchorNewerReconciliation(proj, params)
	if sliceErr != nil {
		// 2026-10-19 验收发现（观测增强）：cursor_stale 曾以一次 55ms 窗口的 fence/快照
		// 不一致出现（快照 turns 落后于 cutRev——anchor 是刚并入 head 的新 turn 时被判
		// stale；同 ms 的全量请求已能看到新 turns）。stale 路径此前服务端零日志，只能靠
		// 客户端推断；登记 follow-up：BeginProjectionSnapshot 与 cutRev 的一致性。
		if errors.Is(sliceErr, errAnchorNewerStale) || errors.Is(sliceErr, errAnchorNewerCursor) {
			sidPrefix := params.SessionID
			if len(sidPrefix) > 8 {
				sidPrefix = sidPrefix[:8]
			}
			slog.Warn("go-bridge: anchor-newer stale slice",
				"requestId", msg.RequestID,
				"sessionPrefix", sidPrefix,
				"anchorTurnId", params.AnchorTurnID,
				"kernelTurns", len(proj.Turns),
				"kernelHeadRev", proj.SyncRev,
				"baseDigests", len(params.BaseDigests),
				"sliceErr", sliceErr.Error(),
			)
		}
		// fence 以 typed error 释放（R9 同族：显式 typed 失败，不留悬挂 fence）。
		wireErr := &WireError{Code: "rpc.error", Message: sliceErr.Error(), Retryable: &retryableFalse}
		switch {
		case errors.Is(sliceErr, errAnchorNewerStale):
			// anchor 离开保留区：水位线失效，客户端 fail-closed FallbackFull。
			wireErr = &WireError{Code: "cursor_stale", Message: sliceErr.Error(), Retryable: &retryableTrue}
		case errors.Is(sliceErr, errAnchorNewerOversized):
			// never-split 独占额度超限：两级 typed 链第一级。
			wireErr = &WireError{Code: "reconciliation.oversized", Message: sliceErr.Error(), Retryable: &retryableFalse}
		case errors.Is(sliceErr, errAnchorNewerCursor):
			wireErr = &WireError{Code: "cursor_stale", Message: sliceErr.Error(), Retryable: &retryableTrue}
		}
		if err := h.eventPublisher.CompleteProjectionSnapshotError(conn, admission, msg.RequestID, wireErr); err != nil {
			slog.Warn("go-bridge: anchor-newer fence release failed", "requestId", msg.RequestID, "error", err)
		}
		return
	}

	// held-turn 注册在同一完成事务内（F-34/v23 扩展）：本页全部交付 turns——turns、
	// anchorRefresh、changedTurns——注册进该连接的 held 集合，使后续针对这些 turn 的
	// decide=true detail commit 不被 fence 清空。完成失败随完成事务回滚。
	// S-3 v2：请求 itemsView=summary → 行转 summary 形态（token/复查判定已按 full
	// 真值完成；例外通道照常完整内嵌——token 基准不变）。摘要行放 turns/anchorRefresh/
	// changedTurns 标准 wire 字段（frozen 样本形态），发送 anchorNewerSummaryWire。
	var summaryWire *anchorNewerSummaryWire
	if params.ItemsView == "summary" {
		maxExceptionBytes := int(h.eventPublisher.ProjectionMaxBytes(conn))
		summaryTurns, sumErr := summarizeAnchorRows(result.Turns, maxExceptionBytes)
		if sumErr == nil {
			var summaryChanged []TurnSummaryDescriptor
			summaryChanged, sumErr = summarizeAnchorRows(result.ChangedTurns, maxExceptionBytes)
			if sumErr == nil {
				wire := &anchorNewerSummaryWire{
					Turns:              summaryTurns,
					ChangedTurns:       summaryChanged,
					RemovedTurnIDs:     result.RemovedTurnIDs,
					ScalarState:        result.ScalarState,
					CorrectionsPending: result.CorrectionsPending,
					TurnTokens:         result.TurnTokens,
					SyncRev:            result.SyncRev,
					ItemsView:          "summary",
				}
				if result.AnchorRefresh != nil {
					refreshRows, refreshErr := summarizeAnchorRows([]TurnProjection{*result.AnchorRefresh}, maxExceptionBytes)
					if refreshErr == nil && len(refreshRows) == 1 {
						anchorCopy := refreshRows[0]
						wire.AnchorRefresh = &anchorCopy
					}
					sumErr = refreshErr
				}
				if sumErr == nil {
					summaryWire = wire
				}
			}
		}
		if sumErr != nil {
			wireErr := &WireError{Code: "rpc.error", Message: sumErr.Error(), Retryable: &retryableFalse}
			if errors.Is(sumErr, errSummaryOversized) {
				wireErr = &WireError{Code: "summary.oversized", Message: sumErr.Error(), Retryable: &retryableFalse}
			}
			if err := h.eventPublisher.CompleteProjectionSnapshotError(conn, admission, msg.RequestID, wireErr); err != nil {
				slog.Warn("go-bridge: anchor summary fence release failed", "requestId", msg.RequestID, "error", err)
			}
			return
		}
	}
	held := make([]string, 0, len(result.Turns)+len(result.ChangedTurns)+1)
	for _, turn := range result.Turns {
		held = append(held, turn.TurnID)
	}
	for _, turn := range result.ChangedTurns {
		held = append(held, turn.TurnID)
	}
	if result.AnchorRefresh != nil {
		held = append(held, result.AnchorRefresh.TurnID)
	}
	sendPayload := interface{}(result)
	if summaryWire != nil {
		sendPayload = summaryWire
	}
	if err := h.eventPublisher.CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, sendPayload, held); err != nil {
		slog.Warn("go-bridge: anchor-newer response enqueue failed", "requestId", msg.RequestID, "error", err)
	}
	logProjectionRPCTrace("response_enqueue", msg, params.SessionID, 0, result.SyncRev, outcomeKind, &admission)
	logProjectionResponseMetrics(msg, params.SessionID, 0, result.SyncRev, outcomeKind, result, h.eventPublisher.ActiveRecoveryID(conn), requestStartedAt, &proj)
	_ = bridgeEpoch // epoch 判定在客户端侧（F-55）：续页不带 epoch、Mac 不比对。
}
