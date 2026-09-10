package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type collaborationModeParams struct {
	SessionID string `json:"sessionId"`
	Preset    string `json:"preset,omitempty"`
}

func (h *Handlers) handleListCollaborationModes(conn Connection, msg WireMessage, agent core.Agent) {
	controller, ok := agent.(core.SessionCollaborationModeController)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support collaboration modes"})
		return
	}
	if readiness, gated := agent.(core.CollaborationModeReadinessProvider); gated && !readiness.CollaborationModeReady() {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "unsupported_capability", Message: "session_collaboration_mode is not ready for this backend"})
		return
	}
	var params collaborationModeParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	params.SessionID = strings.TrimSpace(params.SessionID)
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "sessionId required"})
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, nativeSessionActionTimeout)
	defer cancel()
	if _, err := h.prepareProjectionLiveSession(ctx, params.SessionID, conn, msg.BackendID, agent, extractDir(msg)); err != nil {
		sendCollaborationAttachError(conn, msg.RequestID, err)
		return
	}
	catalog, err := controller.ListSessionCollaborationModes(ctx, params.SessionID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "collaboration_list_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, catalog, nil)
}

func (h *Handlers) handleUpdateCollaborationMode(conn Connection, msg WireMessage, agent core.Agent) {
	controller, ok := agent.(core.SessionCollaborationModeController)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support collaboration modes"})
		return
	}
	if readiness, gated := agent.(core.CollaborationModeReadinessProvider); gated && !readiness.CollaborationModeReady() {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "unsupported_capability", Message: "session_collaboration_mode is not ready for this backend"})
		return
	}
	var params collaborationModeParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.Preset = strings.TrimSpace(params.Preset)
	if params.SessionID == "" || params.Preset == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "sessionId and preset required"})
		return
	}
	release, admitted := h.tryBeginNativeSessionWrite(msg.BackendID, params.SessionID)
	if !admitted {
		sendNativeSessionWriteBusy(conn, msg.RequestID)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(h.ctx, nativeSessionActionTimeout)
	defer cancel()
	if _, err := h.prepareProjectionLiveSession(ctx, params.SessionID, conn, msg.BackendID, agent, extractDir(msg)); err != nil {
		sendCollaborationAttachError(conn, msg.RequestID, err)
		return
	}
	if err := controller.UpdateSessionCollaborationMode(ctx, params.SessionID, params.Preset); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "collaboration_update_failed", Message: err.Error()})
		return
	}
	// The upstream {} response acknowledges submission only. Projection state
	// changes exclusively through thread/settings/updated.
	conn.SendResult(msg.RequestID, map[string]any{"accepted": true}, nil)
}

func sendCollaborationAttachError(conn Connection, requestID string, err error) {
	code := "session_attach_failed"
	if errors.Is(err, errProjectionLiveSessionMissing) {
		code = "session_not_found"
	} else if errors.Is(err, errProjectionLiveSessionBackendMismatch) || errors.Is(err, errProjectionLiveSessionIdentityMismatch) {
		code = "session_identity_mismatch"
	}
	conn.SendResult(requestID, nil, &WireError{Code: code, Message: err.Error()})
}
