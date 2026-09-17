package gobridge

import (
	"encoding/json"
	"errors"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Session preview（session-list parity 方案 §6.4 / 计划 Phase 4 §6.3）。
//
// get_session_preview：按 Session ID 返回 Projection Kernel 中最后一个真实 turn
// 的只读预览，供侧栏长按预览卡展示真实内容。约束（§6.3，全部为协议冻结项）：
//   - 数据来源只能是已提交 Projection Kernel；冷态触发既有 single-flight hydrate
//     （ensureProjectionHydrated 的 Ready 快速路径 + pull 预算），绝不改读旧
//     history 作为 fallback。
//   - headRev == 生成 preview 时 Kernel 的 syncRev（与 SessionProjection.syncRev
//     同源同值），是响应后版本戳，不是第二套 revision。
//   - 不调用 subscribeConnToSession、不创建 live relay 订阅、不改变 timeline
//     writer 所有权：本 handler 是纯读路径。
//   - 响应序列化后最大 128 KiB；超预算时按 item（projection part）边界从较早
//     内容向后丢弃并置 truncated=true；不截断 JSON、不伪造 tool 结束态。
//   - projection.hydrating / not_found / not_migrated 原样返回（iOS 分别显示
//     加载重试 / 失败 / 不可用）。
//
// 不复用 turn_detail_lazy_v1 / get_session_projection 的检索结论与理由记录在
// bridge-v1.md「Session Preview」节与方案 §6.3。

// sessionPreviewMaxBytes 是 latestTurn 序列化后的预算上限（§6.3 冻结：128 KiB）。
const sessionPreviewMaxBytes = 128 * 1024

// handleGetSessionPreview serves `get_session_preview`.
func (h *Handlers) handleGetSessionPreview(conn Connection, msg WireMessage, agent core.Agent) {
	if !backendSupportsProjectionHydrate(msg.BackendID) {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "not_supported",
			Message: "session preview requires a projection-backed backend",
		})
		return
	}

	var params struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "missing_param",
			Message: "sessionId required",
		})
		return
	}

	// Kernel Ready 时这是 cheap hit；冷态加入既有 single-flight hydrate。preview
	// 永不 forceCold（只读当前权威状态，不重扫源）。
	if err := h.ensureProjectionHydrated(msg.BackendID, params.SessionID, params.Directory, false); err != nil {
		code := "projection.hydrate_failed"
		retryable := false
		var retryAfterMillis *int64
		switch {
		case errors.Is(err, errProjectionHydrating):
			code = "projection.hydrating"
			retryable = true
			value := projectionHydratingRetryAfter.Milliseconds()
			retryAfterMillis = &value
		case errors.Is(err, errProjectionBackendNotMigrated):
			code = "projection.not_migrated"
		case errors.Is(err, errProjectionSessionNotFound):
			code = "projection.not_found"
		}
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:             code,
			Message:          err.Error(),
			Retryable:        &retryable,
			RetryAfterMillis: retryAfterMillis,
		})
		return
	}

	proj, ok := h.projectionKernel.CommittedSnapshot(msg.BackendID, params.SessionID)
	if !ok {
		// hydrate 成功后 committed snapshot 不可得是防御性分支（理论不可达）。
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "projection.hydrate_failed",
			Message: "projection kernel has no committed snapshot",
		})
		return
	}

	result := map[string]interface{}{
		"sessionId": params.SessionID,
		"headRev":   proj.SyncRev,
		"truncated": false,
	}
	if len(proj.Turns) > 0 {
		trimmed, truncated := trimTurnToPreviewBudget(proj.Turns[len(proj.Turns)-1])
		result["latestTurn"] = trimmed
		result["truncated"] = truncated
	}
	// 无 turn：latestTurn 缺省（key 不出现），不伪造摘要。
	conn.SendResult(msg.RequestID, result, nil)
}

// trimTurnToPreviewBudget 把 turn 内容裁剪进 128 KiB 序列化预算。裁剪按 item
// （projection part）边界、从较早内容向后丢弃：User parts 头部 → System parts
// 头部 → Assistant parts 头部；一个 part 永远整只丢弃（不切断 JSON、不改编
// 单个 part 语义）。Kernel 快照的 slices 与内部状态共享底层数组，裁剪必须先
// deep copy。
func trimTurnToPreviewBudget(turn TurnProjection) (TurnProjection, bool) {
	clone := cloneTurnForPreview(turn)
	for {
		b, err := json.Marshal(clone)
		if err != nil {
			// 序列化失败不应发生；按未裁剪返回（调用方拿到的仍是完整副本）。
			return clone, false
		}
		if len(b) <= sessionPreviewMaxBytes {
			return clone, turnPartCount(turn) != turnPartCount(clone)
		}
		if !dropOldestPreviewPart(&clone) {
			// 所有 parts 已丢弃仍超预算（turn 元数据本身超限）：返回空内容骨架。
			return clone, true
		}
	}
}

// cloneTurnForPreview 深拷贝 turn 及其消息/parts 切片，避免裁剪写穿 Kernel
// 内部快照的共享底层数组。
func cloneTurnForPreview(turn TurnProjection) TurnProjection {
	clone := turn
	clone.User = cloneMessageForPreview(turn.User)
	clone.System = cloneMessageForPreview(turn.System)
	clone.Assistant = cloneMessageForPreview(turn.Assistant)
	return clone
}

func cloneMessageForPreview(m *MessageProjection) *MessageProjection {
	if m == nil {
		return nil
	}
	mc := *m
	if len(m.Parts) > 0 {
		mc.Parts = append([]ProjectionPart(nil), m.Parts...)
	}
	return &mc
}

// dropOldestPreviewPart 按消息时序（user → system → assistant）丢弃最早的一个
// part。返回 false 表示已无可丢弃内容。
func dropOldestPreviewPart(turn *TurnProjection) bool {
	for _, m := range []**MessageProjection{&turn.User, &turn.System, &turn.Assistant} {
		if m != nil && *m != nil && len((*m).Parts) > 0 {
			(*m).Parts = (*m).Parts[1:]
			return true
		}
	}
	return false
}

func turnPartCount(turn TurnProjection) int {
	n := 0
	for _, m := range []*MessageProjection{turn.User, turn.System, turn.Assistant} {
		if m != nil {
			n += len(m.Parts)
		}
	}
	return n
}
