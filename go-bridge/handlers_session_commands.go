package gobridge

// handlers_session_commands.go — DSH「/」命令面板（docs/2026-09-04 §5.2）。
// 两个 RPC 都是 core.SessionCommandCatalog 的薄透传：list 回 bridge 自有
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
