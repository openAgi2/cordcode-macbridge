package gobridge

// handlers_session_unarchive.go — S5 归档恢复（OD-1=A）：官方
// workspace/unarchiveSession 的桥接 RPC（dsh-web）。幂等语义与官方一致
// （未归档/未知 id = 成功 no-op，A5 活体证据）；错误 verbatim 透传（坑 7）。
// 行的重新可见由 ws archived 增量 → catalog 刷新承载（下一次 list）。

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleUnarchiveSession(conn Connection, msg WireMessage, agent core.Agent) {
	unarchiver, ok := agent.(core.SessionUnarchiver)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "session unarchive not supported for this backend"})
		return
	}
	var params struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory,omitempty"`
	}
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}
	session, err := unarchiver.UnarchiveSession(context.Background(), params.SessionID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "unarchive_failed", Message: err.Error()})
		return
	}
	if session == nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "unarchive_failed", Message: "backend returned no session"})
		return
	}
	// 归档集变化必须立刻作废 wire cache 快照（与 handleArchiveSession 同理：
	// 不 fence 的话恢复行会在 TTL 窗口内仍以归档态从 recent feed 回流）。
	h.openCodeCatalogWireCache().FenceBackend(agentBackendID(agent))
	conn.SendResult(msg.RequestID, map[string]interface{}{"session": sessionsToWire([]core.AgentSessionInfo{*session})[0]}, nil)
}
