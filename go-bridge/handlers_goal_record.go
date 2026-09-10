package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleGetSessionGoal(conn Connection, msg WireMessage, agent core.Agent) {
	controller, ok := goalRecordController(conn, msg, agent)
	if !ok {
		return
	}
	sessionID, err := decodeGoalSessionID(msg.Params)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, nativeSessionActionTimeout)
	defer cancel()
	if _, err := h.prepareProjectionLiveSession(ctx, sessionID, conn, msg.BackendID, agent, extractDir(msg)); err != nil {
		sendCollaborationAttachError(conn, msg.RequestID, err)
		return
	}
	snapshot, err := controller.GetSessionGoal(ctx, sessionID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "goal_get_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, snapshot, nil)
}

func (h *Handlers) handleSetSessionGoal(conn Connection, msg WireMessage, agent core.Agent) {
	controller, ok := goalRecordController(conn, msg, agent)
	if !ok {
		return
	}
	sessionID, update, err := decodeGoalSetParams(msg.Params)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	release, admitted := h.tryBeginNativeSessionWrite(msg.BackendID, sessionID)
	if !admitted {
		sendNativeSessionWriteBusy(conn, msg.RequestID)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(h.ctx, nativeSessionActionTimeout)
	defer cancel()
	if _, err := h.prepareProjectionLiveSession(ctx, sessionID, conn, msg.BackendID, agent, extractDir(msg)); err != nil {
		sendCollaborationAttachError(conn, msg.RequestID, err)
		return
	}
	goal, err := controller.SetSessionGoal(ctx, sessionID, update)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "goal_set_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{"goal": goal}, nil)
}

func (h *Handlers) handleClearSessionGoal(conn Connection, msg WireMessage, agent core.Agent) {
	controller, ok := goalRecordController(conn, msg, agent)
	if !ok {
		return
	}
	sessionID, err := decodeGoalSessionID(msg.Params)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	release, admitted := h.tryBeginNativeSessionWrite(msg.BackendID, sessionID)
	if !admitted {
		sendNativeSessionWriteBusy(conn, msg.RequestID)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(h.ctx, nativeSessionActionTimeout)
	defer cancel()
	if _, err := h.prepareProjectionLiveSession(ctx, sessionID, conn, msg.BackendID, agent, extractDir(msg)); err != nil {
		sendCollaborationAttachError(conn, msg.RequestID, err)
		return
	}
	cleared, err := controller.ClearSessionGoal(ctx, sessionID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "goal_clear_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{"cleared": cleared}, nil)
}

func goalRecordController(conn Connection, msg WireMessage, agent core.Agent) (core.SessionGoalRecordController, bool) {
	controller, ok := agent.(core.SessionGoalRecordController)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support typed session goals"})
		return nil, false
	}
	if readiness, gated := agent.(core.SessionGoalReadinessProvider); gated && !readiness.SessionGoalReady() {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "unsupported_capability", Message: "session_goal is not ready for this backend"})
		return nil, false
	}
	return controller, true
}

func decodeGoalSessionID(raw json.RawMessage) (string, error) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return "", fmt.Errorf("invalid goal params: %w", err)
		}
	}
	params.SessionID = strings.TrimSpace(params.SessionID)
	if params.SessionID == "" {
		return "", fmt.Errorf("sessionId required")
	}
	return params.SessionID, nil
}

func decodeGoalSetParams(raw json.RawMessage) (string, core.SessionGoalUpdate, error) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return "", core.SessionGoalUpdate{}, fmt.Errorf("valid goal params required")
	}
	sessionID, err := decodeGoalSessionID(raw)
	if err != nil {
		return "", core.SessionGoalUpdate{}, err
	}
	var update core.SessionGoalUpdate
	if value, exists := fields["objective"]; exists {
		if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			var objective string
			if json.Unmarshal(value, &objective) != nil || strings.TrimSpace(objective) == "" {
				return "", core.SessionGoalUpdate{}, fmt.Errorf("objective must be a non-empty string or null")
			}
			objective = strings.TrimSpace(objective)
			update.Objective = &objective
		}
	}
	if value, exists := fields["status"]; exists {
		if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			var status string
			if json.Unmarshal(value, &status) != nil || !validCodexGoalStatus(status) {
				return "", core.SessionGoalUpdate{}, fmt.Errorf("unsupported goal status")
			}
			update.Status = &status
		}
	}
	if value, exists := fields["tokenBudget"]; exists {
		update.TokenBudgetSet = true
		if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			var budget int64
			if json.Unmarshal(value, &budget) != nil {
				return "", core.SessionGoalUpdate{}, fmt.Errorf("tokenBudget must be an integer or null")
			}
			update.TokenBudget = &budget
		}
	}
	if update.Objective == nil && update.Status == nil && !update.TokenBudgetSet {
		return "", core.SessionGoalUpdate{}, fmt.Errorf("at least one goal field is required")
	}
	return sessionID, update, nil
}
