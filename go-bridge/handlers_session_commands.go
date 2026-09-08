package gobridge

// handlers_session_commands.go — session-domain「/」命令面板。
// 两个 RPC 都以 core.SessionCommandCatalog 为官方命令边界：list 回 bridge 自有
// {commands:[…]} 包装（官方裸数组由 dsh-web 解码映射）；execute 回
// {ok:true, commandId?, resultKind?, resultText?}（官方 settle 透传——成功
// 反馈的可见面，2026-09-05 owner 报障「点了没反应」后补），失败 message 携
// 官方 result.text 原文。命令执行是 host 动作，绝不经
// send_message/session.prompt（那会把 /plan 当聊天发给模型）。

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleListSessionCommands(conn Connection, msg WireMessage, agent core.Agent) {
	catalog, ok := agent.(core.SessionCommandCatalog)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support session commands"})
		return
	}
	var params ListSessionCommandsParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "sessionId required"})
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	commands, err := catalog.ListSessionCommands(ctx, params.SessionID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	wireCommands := make([]map[string]any, 0, len(commands))
	for _, cmd := range commands {
		wireCommands = append(wireCommands, map[string]any{
			"name":        cmd.Name,
			"description": cmd.Description,
			"hint":        cmd.Hint,
		})
	}
	conn.SendResult(msg.RequestID, map[string]any{"commands": wireCommands}, nil)
}

func (h *Handlers) handleExecuteSessionCommand(conn Connection, msg WireMessage, agent core.Agent) {
	catalog, ok := agent.(core.SessionCommandCatalog)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support session commands"})
		return
	}
	var params ExecuteSessionCommandParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "sessionId required"})
		return
	}
	if strings.TrimSpace(params.Line) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "line required"})
		return
	}
	// /compact waits on an LLM summary host-side; bound generously, not at
	// control-plane latency. 300s covers large-session compaction through the
	// gateway（官方 execute 无人为上限，signal 随派发请求存活；2026-09-06
	// rework ⑨：90s 从未生效，被 wire 层 30s 硬顶掐断）。iOS 对
	// execute_session_command 挂同预算（CCCodeBridgeTransport 超时表）。
	ctx, cancel := context.WithTimeout(h.ctx, 300*time.Second)
	defer cancel()
	// Grok 的 execute 必须驱动该会话自己的 ACP actor。用户可以在刚打开
	// 历史会话、尚未发过普通消息时直接点 /compact；此时 bridge registry
	// 没有 actor。在命令派发前按 send_message 的同一恢复语义挂载它，并先
	// 订阅/启动 relay，确保 hostTurn 正文与 terminal 进入原会话投影。DSH
	// 等 backend 保持原来的 catalog 薄透传，不额外 StartSession。
	if agent.Name() == "grokbuild" {
		h.mu.Lock()
		sess, live := h.getSession(params.SessionID)
		h.mu.Unlock()
		if !live || sess == nil {
			started, startErr := agent.StartSession(h.ctx, params.SessionID)
			if startErr != nil {
				conn.SendResult(msg.RequestID, nil, &WireError{Code: "execute_failed", Message: startErr.Error()})
				return
			}
			h.mu.Lock()
			existing, existingOK := h.getSession(params.SessionID)
			if existingOK && existing != nil {
				h.mu.Unlock()
				_ = started.Close()
				sess = existing
			} else {
				h.putSessionWithMeta(params.SessionID, msg.BackendID, params.Directory, started)
				h.mu.Unlock()
				sess = started
			}
		}
		h.broadcaster.Subscribe(conn, SubscriptionKey{
			BackendID: msg.BackendID,
			SessionID: params.SessionID,
			Directory: params.Directory,
		})
		h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
	}

	result, err := catalog.ExecuteSessionCommand(ctx, params.SessionID, params.Line)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "execute_failed", Message: err.Error()})
		return
	}
	// Official settle passthrough (additive optional fields): kind is
	// "success" here by construction, text is the command's own feedback
	// ("Plan mode on. …"). Empty fields are omitted, not null.
	payload := map[string]any{"ok": true}
	if result.CommandID != "" {
		payload["commandId"] = result.CommandID
	}
	if result.ResultKind != "" {
		payload["resultKind"] = result.ResultKind
	}
	if result.ResultText != "" {
		payload["resultText"] = result.ResultText
	}
	conn.SendResult(msg.RequestID, payload, nil)
}
