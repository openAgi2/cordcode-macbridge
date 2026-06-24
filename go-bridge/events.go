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
		return "text_delta", map[string]interface{}{
			"delta": ev.Content,
		}, false

	case core.EventTextReplace:
		return "message_updated", map[string]interface{}{
			"content": ev.Content,
		}, false

	case core.EventThinking:
		return "reasoning_delta", map[string]interface{}{
			"delta": ev.Content,
		}, false

	case core.EventToolUse:
		payload := map[string]interface{}{
			"toolName":     ev.ToolName,
			"toolInput":    ev.ToolInput,
			"toolInputRaw": ev.ToolInputRaw,
		}
		if ev.RequestID != "" {
			payload["itemId"] = ev.RequestID
		}
		return "tool_started", payload, false

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
		return "tool_finished", payload, false

	case core.EventPlan:
		return "todos_updated", map[string]interface{}{
			"todos": todosToWire(ev.Plan),
		}, false

	case core.EventTurnStarted:
		return "turn_started", map[string]interface{}{
			"turnId": "",
		}, false

	case core.EventResult:
		if ev.Done {
			return "turn_completed", map[string]interface{}{
				"done":         true,
				"text":         ev.Content,
				"inputTokens":  ev.InputTokens,
				"outputTokens": ev.OutputTokens,
			}, true
		}
		return "text_delta", map[string]interface{}{
			"delta": ev.Content,
		}, false

	case core.EventError:
		msg := "unknown error"
		if ev.Error != nil {
			msg = ev.Error.Error()
		}
		return "error", map[string]interface{}{
			"message": msg,
		}, true

	case core.EventPermissionRequest:
		return "permission_request", map[string]interface{}{
			"requestId":    ev.RequestID,
			"toolName":     ev.ToolName,
			"toolInput":    ev.ToolInput,
			"toolInputRaw": ev.ToolInputRaw,
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
			"context": map[string]interface{}{
				"usedTokens":            ev.ContextUsage.UsedTokens,
				"baselineTokens":        ev.ContextUsage.BaselineTokens,
				"totalTokens":           ev.ContextUsage.TotalTokens,
				"inputTokens":           ev.ContextUsage.InputTokens,
				"cachedInputTokens":     ev.ContextUsage.CachedInputTokens,
				"outputTokens":          ev.ContextUsage.OutputTokens,
				"reasoningOutputTokens": ev.ContextUsage.ReasoningOutputTokens,
				"contextWindow":         ev.ContextUsage.ContextWindow,
			},
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

	default:
		slog.Debug("go-bridge: unhandled event type", "type", ev.Type)
		return "", nil, false
	}
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
