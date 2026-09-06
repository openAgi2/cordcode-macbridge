package gobridge

// handlers_session_goal.go — DSH 目标横条动作（官方 GoalBar 的四个动词）。
// core.SessionGoalController 的薄透传：{ok:true}（快照更新由 goal/change →
// session_goal 事件/patch 承载，无回显）；失败 message 携官方座位原文
// （例如 "cannot pause goal … from phase \"complete\"; expected active"）。
// 目标创建不在动词里——官方经 /goal 命令（execute_session_command）创建。

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleMutateSessionGoal(conn Connection, msg WireMessage, agent core.Agent) {
	controller, ok := agent.(core.SessionGoalController)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support session goal mutations"})
		return
	}
	var params MutateSessionGoalParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "sessionId required"})
		return
	}
	// Control-plane latency: fresh CAS ref fetch + one verb.
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	if err := controller.MutateSessionGoal(ctx, params.SessionID, params.Action, params.Objective); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "goal_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{"ok": true}, nil)
}
