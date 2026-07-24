package gobridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// GetSessionProjectionParams — get_session_projection request params. Additive; sits beside
// GetSessionMessagesParams. See docs/protocol/bridge-v1.md「Session Projection Stream」.
type GetSessionProjectionParams struct {
	SessionID  string `json:"sessionId"`
	Directory  string `json:"directory,omitempty"`
	SinceRev   int    `json:"sinceRev,omitempty"`
	LimitTurns int    `json:"limitTurns,omitempty"`
}

// handleGetSessionProjection returns the authoritative SessionProjection for a session, read
// from the ProjectionReducer's in-memory state — the SAME state push reads (design §6.4 rule 4:
// pull == push state). It is NOT a wrapper around get_session_messages: it returns projection
// turns, never raw history bodies for the client to merge.
//
// The reducer is fed by the active Codex file-relay. After Mac restart the in-memory projection
// is empty even when the rollout on disk is complete (file-relay starts at EOF). Cold pulls
// therefore one-shot hydrate from disk before answering (design §5.3 hydrate-on-cold-start).
func (h *Handlers) handleGetSessionProjection(conn Connection, msg WireMessage, agent core.Agent) {
	var params GetSessionProjectionParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	params.SessionID = h.resolveSessionIDForActiveSession(params.SessionID)
	slog.Info("go-bridge: get_session_projection", "backendID", msg.BackendID, "sessionID", params.SessionID, "sinceRev", params.SinceRev)

	// Subscribe so the conn receives subsequent projection_patch push frames (WP5 emission).
	h.subscribeConnToSession(conn, msg, params.SessionID)

	// Cold hydrate is best-effort and MUST NOT block the RPC (owner 2026-07-25:
	// awaiting a hung hydrate left iOS stuck before get_session_messages → all sessions blank).
	// Hydrate asynchronously; this response may still be empty head-0, and iOS always
	// loads history in parallel. A later pull/push will see the hydrated state.
	if msg.BackendID == "codex" {
		if head := h.eventPublisher.ProjectionHeadRev(msg.BackendID, params.SessionID); head == 0 {
			sid := params.SessionID
			backendID := msg.BackendID
			go func() {
				n := h.hydrateCodexProjectionFromDisk(backendID, sid)
				slog.Info("go-bridge: get_session_projection cold-hydrate async",
					"sessionID", sid, "events", n,
					"headRev", h.eventPublisher.ProjectionHeadRev(backendID, sid),
				)
			}()
		}
	}

	headRev := h.eventPublisher.ProjectionHeadRev(msg.BackendID, params.SessionID)

	// Cheap delta response when the client is already at head: empty patch set.
	if params.SinceRev != 0 && params.SinceRev == headRev {
		conn.SendResult(msg.RequestID, map[string]interface{}{
			"patches": []ProjectionPatch{},
			"headRev": headRev,
		}, nil)
		return
	}

	proj, ok := h.eventPublisher.ProjectionSnapshot(msg.BackendID, params.SessionID)
	if !ok {
		// No reducer state yet — return an empty projection at head 0 rather than fabricating
		// content. Push will bring the client up to date.
		proj = SessionProjection{
			SessionID: params.SessionID,
			SyncRev:   0,
			Execution: ExecutionView{Phase: "idle"},
			Turns:     []TurnProjection{},
		}
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"projection": proj}, nil)
}

// hydrateCodexProjectionFromDisk one-shot feeds the rollout JSONL into ProjectionReducer
// so get_session_projection can answer after Mac restart without waiting for a new turn.
// Events are published Offline (no live delivery requirement); Apply still runs.
func (h *Handlers) hydrateCodexProjectionFromDisk(backendID, sessionID string) int {
	if h == nil || h.eventPublisher == nil || sessionID == "" {
		return 0
	}
	agent, ok := h.getFirstAgentByName("codex")
	if !ok {
		return 0
	}
	locator, ok := agent.(core.TranscriptLocator)
	if !ok {
		return 0
	}
	sessPath, err := locator.TranscriptPath(context.Background(), sessionID)
	if err != nil || strings.TrimSpace(sessPath) == "" {
		return 0
	}
	events := scanCodexTranscriptRelayEvents(sessPath, 0)
	if len(events) == 0 {
		return 0
	}
	currentTurnID := ""
	toolNames := make(map[string]string)
	applied := 0
	for _, ev := range events {
		var eventName string
		var data map[string]interface{}
		switch ev.kind {
		case "task_started":
			currentTurnID = ev.turnID
			eventName = "turn_started"
			data = map[string]interface{}{"turnId": currentTurnID}
		case "task_complete":
			tid := ev.turnID
			if tid == "" {
				tid = currentTurnID
			}
			eventName = "turn_completed"
			data = map[string]interface{}{"turnId": tid, "done": true, "reason": "task_complete"}
			currentTurnID = ""
		case "text":
			eventName = "text_delta"
			data = map[string]interface{}{"itemId": currentTurnID, "delta": ev.text}
		case "reasoning":
			eventName = "reasoning_delta"
			data = map[string]interface{}{"itemId": currentTurnID, "delta": ev.text}
		case "user_message":
			eventName = "user_message"
			data = map[string]interface{}{"itemId": ev.itemId, "turnId": currentTurnID, "text": ev.text}
		case "tool_started":
			if ev.itemId != "" {
				toolNames[ev.itemId] = ev.toolName
			}
			eventName = "tool_started"
			data = map[string]interface{}{"toolName": ev.toolName, "toolInput": ev.toolInput}
			if ev.itemId != "" {
				data["itemId"] = ev.itemId
			}
		case "tool_finished":
			eventName = "tool_finished"
			data = map[string]interface{}{"toolResult": ev.toolResult, "toolStatus": "completed"}
			if name, ok := toolNames[ev.itemId]; ok && name != "" {
				data["toolName"] = name
			}
			if ev.itemId != "" {
				data["itemId"] = ev.itemId
			}
		default:
			continue
		}
		h.publishEvent(LogicalEvent{
			SessionID: sessionID,
			BackendID: backendID,
			Event:     eventName,
			Data:      data,
			Broadcast: false, // hydrate only — do not fan out mid-pull
			Offline:   true,
		})
		applied++
	}
	// Completed transcript should settle execution to idle.
	h.publishEvent(LogicalEvent{
		SessionID: sessionID,
		BackendID: backendID,
		Event:     "session_state_changed",
		Data:      map[string]interface{}{"state": "idle"},
		Broadcast: false,
		Offline:   true,
	})
	return applied
}

// ProjectionSnapshot returns a deep copy of the session projection (pull path accessor).
func (p *EventPublisher) ProjectionSnapshot(backendID, sessionID string) (SessionProjection, bool) {
	if p == nil || p.projection == nil {
		return SessionProjection{}, false
	}
	return p.projection.Snapshot(backendID, sessionID)
}

// ProjectionHeadRev returns the current head syncRev for the session.
func (p *EventPublisher) ProjectionHeadRev(backendID, sessionID string) int {
	if p == nil || p.projection == nil {
		return 0
	}
	return p.projection.LastAppliedRev(backendID, sessionID)
}
