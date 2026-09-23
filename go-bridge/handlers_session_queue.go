package gobridge

// handlers_session_queue.go — S3 排队消息管理（OD-2b=B）：官方
// session/updateQueue 的桥接 RPC。itemId 是官方 UserMessage.id——与 S3
// 占位行的投影键同源（A3a id 连续性证据），iOS 直接用它渲染/操作同一行。
// 动作镜像官方 QueueAction union（edit 文本 / remove / steer）；错误 verbatim
// 透传（session/steer-unavailable、session/queue-item-not-found——A3b 活体
// 负例原文），不吞不改。

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleUpdateSessionQueue(conn Connection, msg WireMessage, agent core.Agent) {
	manager, ok := agent.(core.SessionQueueManager)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support session queue management"})
		return
	}
	var params UpdateSessionQueueParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "sessionId required"})
		return
	}
	if strings.TrimSpace(params.ItemID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "itemId required"})
		return
	}
	switch params.Action {
	case core.SessionQueueActionEdit, core.SessionQueueActionRemove, core.SessionQueueActionSteer:
	default:
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "action must be edit|remove|steer"})
		return
	}
	// Control-plane latency: one official verb (fresh CAS-free — updateQueue
	// targets the pending item id directly).
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	if err := manager.UpdateSessionQueue(ctx, params.SessionID, params.ItemID, params.Action, params.Content); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "queue_update_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{"ok": true}, nil)
}
