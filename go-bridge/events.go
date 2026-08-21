package gobridge

import (
	"log/slog"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// mapAgentEvent converts a cc-connect Agent Event to a WS event name + data payload.
func mapAgentEvent(ev core.Event) (eventName string, data interface{}, done bool) {
	switch ev.Type {
	case core.EventText:
		payload := map[string]interface{}{
			"delta": ev.Content,
		}
		if ev.ItemID != "" {
			payload["itemId"] = ev.ItemID
		} else if ev.TurnID != "" {
			// OpenCode live: assistant text is attributed to the owning turn identity.
			payload["itemId"] = ev.TurnID
		}
		return "text_delta", eventData(ev, payload), false

	case core.EventTextReplace:
		return "message_updated", eventData(ev, map[string]interface{}{
			"content": ev.Content,
		}), false

	case core.EventThinking:
		payload := map[string]interface{}{
			"delta": ev.Content,
		}
		if ev.ItemID != "" {
			payload["itemId"] = ev.ItemID
		} else if ev.TurnID != "" {
			payload["itemId"] = ev.TurnID
		}
		return "reasoning_delta", eventData(ev, payload), false

	case core.EventToolUse:
		payload := map[string]interface{}{
			"toolName":     ev.ToolName,
			"toolInput":    ev.ToolInput,
			"toolInputRaw": ev.ToolInputRaw,
		}
		if ev.RequestID != "" {
			payload["itemId"] = ev.RequestID
		}
		if ev.ToolMatches != nil {
			payload["matches"] = ev.ToolMatches
		}
		return "tool_started", eventData(ev, payload), false

	case core.EventToolResult:
		status := ev.ToolStatus
		if status == "" {
			if ev.ToolSuccess != nil && *ev.ToolSuccess {
				status = "completed"
			} else if ev.ToolExitCode != nil && *ev.ToolExitCode != 0 {
				status = "failed"
			} else {
				status = "completed"
			}
		}
		toolResult := ev.ToolResult
		if toolResult == "" {
			toolResult = ev.Content
		}
		payload := map[string]interface{}{
			"toolName":     ev.ToolName,
			"toolResult":   toolResult,
			"toolStatus":   status,
			"toolExitCode": ev.ToolExitCode,
			"toolInput":    ev.ToolInput,
		}
		if ev.RequestID != "" {
			payload["itemId"] = ev.RequestID
		}
		if fileChanges := fileChangesToWire(ev.FileChanges); len(fileChanges) > 0 {
			payload["fileChanges"] = fileChanges
		}
		if ev.ToolMatches != nil {
			payload["matches"] = ev.ToolMatches
		}
		return "tool_finished", eventData(ev, payload), false

	case core.EventPlan:
		return "todos_updated", map[string]interface{}{
			"todos": todosToWire(ev.Plan),
		}, false

	case core.EventUserMessage:
		// OpenCode/Claude-style user attribution for projection SoT. turnId/itemId are
		// source-proven; empty identity is skipped by the reducer.
		payload := map[string]interface{}{
			"text": ev.Content,
		}
		if ev.TurnID != "" {
			payload["turnId"] = ev.TurnID
		}
		if ev.ItemID != "" {
			payload["itemId"] = ev.ItemID
		} else if ev.TurnID != "" {
			payload["itemId"] = ev.TurnID
		}
		return "user_message", eventData(ev, payload), false

	case core.EventTurnStarted:
		// Phase-3 turnId plumbing: carry source-proven turn identity so ProjectionReducer
		// can mark execution.running (design §6.4 / §7.4). Empty turnId remains skipped.
		return "turn_started", eventData(ev, map[string]interface{}{
			"turnId": ev.TurnID,
		}), false

	case core.EventResult:
		if ev.Done {
			if ev.Error != nil {
				// Failed turn (e.g. opencode provider resolution produced zero
				// output, 2026-08-14): settle as turn_error so the kernel marks
				// the turn error instead of a healthy empty turn_completed.
				payload := map[string]interface{}{
					"done":    true,
					"message": ev.Error.Error(),
				}
				if ev.TurnID != "" {
					payload["turnId"] = ev.TurnID
				}
				return "turn_error", eventData(ev, payload), true
			}
			payload := map[string]interface{}{
				"done":         true,
				"text":         ev.Content,
				"inputTokens":  ev.InputTokens,
				"outputTokens": ev.OutputTokens,
			}
			if ev.TurnID != "" {
				payload["turnId"] = ev.TurnID
			}
			return "turn_completed", eventData(ev, payload), true
		}
		return "text_delta", eventData(ev, map[string]interface{}{
			"delta": ev.Content,
		}), false

	case core.EventError:
		msg := "unknown error"
		if ev.Error != nil {
			msg = ev.Error.Error()
		}
		return "error", eventData(ev, map[string]interface{}{
			"message": msg,
		}), true

	case core.EventRetryStatus:
		// Transient provider-retry notice (opencode-web 1.18 session.status
		// {type:"retry"}): the turn stays alive, so this must NOT settle any
		// turn state — clients render it as a transient row (official web
		// parity). Not in IsDurableMilestone (no mailbox persistence) and not
		// in the syncV2 raw deny-list (control-plane raw delivery is the only
		// carrier; the projection kernel ignores it).
		payload := map[string]interface{}{
			"message": ev.Content,
		}
		if ev.RetryAttempt > 0 {
			payload["attempt"] = ev.RetryAttempt
		}
		if ev.RetryNext > 0 {
			payload["next"] = ev.RetryNext
		}
		return "session_retry_status", eventData(ev, payload), false

	case core.EventPermissionRequest:
		payload := map[string]interface{}{
			"requestId":    ev.RequestID,
			"toolName":     ev.ToolName,
			"toolInput":    ev.ToolInput,
			"toolInputRaw": ev.ToolInputRaw,
		}
		if ev.Content != "" {
			payload["reason"] = ev.Content
		}
		// Official permission.asked payload (opencode-web v1.18, live-pinned):
		// clients render the category line + pattern rows from these and offer
		// the official reject/always/once triple; absent on other backends.
		if ev.PermissionKind != "" {
			payload["permissionKind"] = ev.PermissionKind
		}
		if len(ev.PermissionPatterns) > 0 {
			payload["patterns"] = ev.PermissionPatterns
		}
		return "permission_request", payload, false

	case core.EventPermissionResolved:
		// Projection SoT close for a pending permission card. iOS resolve_permission
		// and host approval/resolved both land here so SSV2 remaps without a leftover
		// requiresPermissionConfirmation tool.
		return "permission_resolved", map[string]interface{}{
			"requestId": ev.RequestID,
			"behavior":  ev.Content,
		}, false

	case core.EventContextCompressing:
		return "context_compressing", map[string]interface{}{
			"sessionId": ev.SessionID,
		}, false

	case core.EventContextCompressed:
		return "context_compressed", map[string]interface{}{
			"sessionId": ev.SessionID,
		}, false

	case core.EventContextUsageUpdated:
		if ev.ContextUsage == nil {
			return "", nil, false
		}
		return "context_usage_updated", map[string]interface{}{
			"context": contextUsageToWire(ev.ContextUsage),
		}, false

	case core.EventQuestionAsked:
		opts := make([]map[string]interface{}, 0, len(ev.QuestionOpts))
		for _, o := range ev.QuestionOpts {
			opts = append(opts, map[string]interface{}{
				"id":          o.ID,
				"label":       o.Label,
				"description": o.Description,
			})
		}
		return "question_asked", map[string]interface{}{
			"questionId":   ev.QuestionID,
			"questionText": ev.QuestionText,
			"options":      opts,
			"required":     ev.Required,
			"threadId":     ev.ThreadID,
			"sessionId":    ev.SessionID,
		}, false

	case core.EventQuestionResolved:
		return "question_resolved", map[string]interface{}{
			"questionId": ev.QuestionID,
			"result":     ev.Content,
		}, false

	case core.EventUserInputRequested:
		// Structured user input v2 (design §10.1). Projects once through the Kernel as a
		// user_input part; status=pending (normal) or failed (malformed questions, still
		// projected once with canRespond=false). turnId/itemId carry source-proven attribution
		// the reducer requires to attach the part (identityless frames are dropped upstream).
		if ev.UserInput == nil {
			return "", nil, false
		}
		ui := ev.UserInput
		return "user_input_requested", eventData(ev, map[string]interface{}{
			"turnId":         ev.TurnID,
			"itemId":         ev.ItemID,
			"interactionId":  ui.InteractionID,
			"status":         string(ui.Status),
			"questions":      userInputQuestionsToWire(ui.Questions),
			"canRespond":     ui.CanRespond,
			"canReject":      ui.CanReject,
			"expiresAt":      ui.ExpiresAt,
			"diagnosticCode": ui.DiagnosticCode,
		}), false

	case core.EventUserInputResolved:
		// Resolved in place; projection carries no answer text. source ∈ ios|mac|other_client|backend.
		if ev.UserInput == nil {
			return "", nil, false
		}
		ui := ev.UserInput
		return "user_input_resolved", eventData(ev, map[string]interface{}{
			"turnId":        ev.TurnID,
			"itemId":        ev.ItemID,
			"interactionId": ui.InteractionID,
			"status":        string(ui.Status),
			"source":        ui.ResolutionSource,
			"resolvedAt":    ui.ResolvedAt,
		}), false

	default:
		slog.Debug("go-bridge: unhandled event type", "type", ev.Type)
		return "", nil, false
	}
}

func eventData(ev core.Event, payload map[string]interface{}) map[string]interface{} {
	if ev.StreamID != "" {
		payload["streamId"] = ev.StreamID
	}
	if ev.ParentStreamID != "" {
		payload["parentStreamId"] = ev.ParentStreamID
	}
	return payload
}

func fileChangesToWire(changes []core.FileChange) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(changes))
	for _, change := range changes {
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		kind := strings.TrimSpace(change.Kind)
		if kind == "" {
			kind = "edit"
		}
		item := map[string]interface{}{
			"path": path,
			"kind": kind,
		}
		if change.Diff != "" {
			item["diff"] = change.Diff
		}
		if change.MovePath != "" {
			item["movePath"] = change.MovePath
		}
		result = append(result, item)
	}
	return result
}

func userInputOptionsToWire(options []core.UserInputOption) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(options))
	for _, o := range options {
		opt := map[string]interface{}{
			"id":    o.ID,
			"label": o.Label,
		}
		if o.Description != "" {
			opt["description"] = o.Description
		}
		out = append(out, opt)
	}
	return out
}

// userInputQuestionsToWire serializes the canonical core.UserInputQuestion slice to the wire shape
// consumed by go-bridge events.go (user_input_requested) and the projection reducer. Mirrors the
// ProjectionPart.UserInputQuestions vocabulary (design §6.1/§10.1) and the iOS Swift / message-web
// mirror types: each question carries id / header? / prompt / answerMode / options[{id,label,
// description?}] / allowsCustomAnswer / isSecret / required.
func userInputQuestionsToWire(questions []core.UserInputQuestion) []interface{} {
	out := make([]interface{}, 0, len(questions))
	for _, q := range questions {
		entry := map[string]interface{}{
			"id":                 q.ID,
			"prompt":             q.Prompt,
			"answerMode":         string(q.AnswerMode),
			"options":            userInputOptionsToWire(q.Options),
			"allowsCustomAnswer": q.AllowsCustomAnswer,
			"isSecret":           q.IsSecret,
			"required":           q.Required,
		}
		if q.Header != "" {
			entry["header"] = q.Header
		}
		out = append(out, entry)
	}
	return out
}

func todosToWire(todos []core.Todo) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(todos))
	for _, todo := range todos {
		content := strings.TrimSpace(todo.Content)
		if content == "" {
			continue
		}
		status := strings.TrimSpace(todo.Status)
		if status == "" {
			status = "pending"
		}
		priority := strings.TrimSpace(todo.Priority)
		if priority == "" {
			priority = "normal"
		}
		result = append(result, map[string]interface{}{
			"content":  content,
			"status":   status,
			"priority": priority,
		})
	}
	return result
}
