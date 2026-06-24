package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

func (a *Agent) fetchJSON(ctx context.Context, path string) (json.RawMessage, error) {
	a.mu.RLock()
	baseURL := a.httpBaseURL
	authHeader := a.httpAuthHeader
	workDir := a.workDir
	a.mu.RUnlock()

	if baseURL == "" {
		return nil, core.ErrNotSupported
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if workDir != "" {
		req.Header.Set("x-opencode-directory", workDir)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("opencode HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	raw, err := a.fetchJSON(ctx, "/session/"+sessionID+"/message")
	if err != nil {
		return nil, err
	}

	var messages []map[string]any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	if limit > 0 && len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}

	result := make([]core.RichHistoryEntry, 0, len(messages))
	for _, message := range messages {
		result = append(result, mapRichHistoryEntry(message))
	}
	return result, nil
}

func (a *Agent) FetchTodos(ctx context.Context, sessionID string) ([]core.Todo, error) {
	raw, err := a.fetchJSON(ctx, "/session/"+sessionID+"/todo")
	if err != nil {
		return nil, err
	}

	var todos []map[string]any
	if err := json.Unmarshal(raw, &todos); err != nil {
		return nil, err
	}

	result := make([]core.Todo, 0, len(todos))
	for _, todo := range todos {
		content, _ := todo["content"].(string)
		if content == "" {
			content, _ = todo["text"].(string)
		}
		status, _ := todo["status"].(string)
		if status == "" {
			status = "pending"
		}
		priority, _ := todo["priority"].(string)
		if priority == "" {
			priority = "normal"
		}
		result = append(result, core.Todo{Content: content, Status: status, Priority: priority})
	}
	return result, nil
}

func (a *Agent) ListAgents(ctx context.Context) ([]core.AgentDescriptor, error) {
	raw, err := a.fetchJSON(ctx, "/agent")
	if err != nil {
		return nil, err
	}

	var agents []map[string]any
	if err := json.Unmarshal(raw, &agents); err != nil {
		return nil, err
	}

	result := make([]core.AgentDescriptor, 0, len(agents))
	for _, agent := range agents {
		name, _ := agent["name"].(string)
		if name == "" {
			name, _ = agent["id"].(string)
		}
		mode, _ := agent["mode"].(string)
		if mode == "" {
			mode = "primary"
		}
		description, _ := agent["description"].(string)
		hidden, _ := agent["hidden"].(bool)
		native, _ := agent["native"].(bool)
		result = append(result, core.AgentDescriptor{
			Name:        name,
			Mode:        mode,
			Hidden:      hidden,
			Native:      native,
			Description: description,
		})
	}
	return result, nil
}

func mapRichHistoryEntry(message map[string]any) core.RichHistoryEntry {
	info := message
	if sub, ok := message["info"].(map[string]any); ok {
		info = sub
	}

	timeMap, _ := info["time"].(map[string]any)
	parts, _ := message["parts"].([]any)
	if parts == nil {
		parts, _ = info["parts"].([]any)
	}

	var content, thinking string
	steps := make([]map[string]any, 0)
	mappedParts := make([]map[string]any, 0, len(parts))

	for _, partValue := range parts {
		part, _ := partValue.(map[string]any)
		if part == nil {
			continue
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "text":
			text, _ := part["text"].(string)
			if text == "" {
				text, _ = part["initial"].(string)
			}
			if content != "" && text != "" {
				content += "\n"
			}
			content += text
			mappedParts = append(mappedParts, map[string]any{"type": "text", "content": text})
		case "reasoning":
			text, _ := part["text"].(string)
			if text == "" {
				text, _ = part["initial"].(string)
			}
			if thinking != "" && text != "" {
				thinking += "\n"
			}
			thinking += text
			mappedParts = append(mappedParts, map[string]any{"type": "reasoning", "content": text})
		case "tool":
			tool, _ := part["tool"].(map[string]any)
			if tool == nil {
				continue
			}
			toolID, _ := tool["id"].(string)
			if toolID == "" {
				toolID, _ = tool["toolName"].(string)
			}
			toolName, _ := tool["toolName"].(string)
			if toolName == "" {
				toolName, _ = tool["name"].(string)
			}
			state, _ := tool["state"].(map[string]any)
			status := "completed"
			if state != nil {
				if value, ok := state["status"].(string); ok && value != "" {
					status = value
				}
			}
			var output any
			var duration any
			if state != nil {
				output = state["output"]
				duration = state["durationMs"]
			}
			step := map[string]any{
				"id":                             toolID,
				"toolName":                       toolName,
				"status":                         status,
				"output":                         makeToolOutput(output),
				"duration":                       duration,
				"requiresPermissionConfirmation": false,
				"availablePermissionOptions":     []any{},
			}
			steps = append(steps, step)
			mappedParts = append(mappedParts, map[string]any{"type": "tool", "step": step})
		case "file":
			file := map[string]any{
				"id":       part["id"],
				"mime":     part["mime"],
				"url":      part["url"],
				"filename": part["filename"],
			}
			if file["mime"] == nil {
				file["mime"] = "application/octet-stream"
			}
			mappedParts = append(mappedParts, map[string]any{"type": "file", "file": file})
		}
	}

	timestamp := time.Now().UTC()
	if timeMap != nil {
		if created, ok := timeMap["created"].(float64); ok && created > 0 {
			timestamp = time.UnixMilli(int64(created)).UTC()
		}
	}

	role, _ := info["role"].(string)
	if role == "" {
		role, _ = message["role"].(string)
	}
	id, _ := info["id"].(string)
	if id == "" {
		id, _ = message["id"].(string)
	}

	entry := core.RichHistoryEntry{
		ID:         id,
		Role:       role,
		Content:    content,
		Thinking:   thinking,
		Parts:      mappedParts,
		Steps:      steps,
		Files:      []map[string]any{},
		Timestamp:  timestamp,
		AgentName:  strValue(info, "agent"),
		ModelID:    strValue(info, "modelID"),
		ProviderID: strValue(info, "providerID"),
		ModelName:  strValue(info, "modelName"),
	}
	return entry
}

func makeToolOutput(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		if m["kind"] == "inline" || m["kind"] == "content_ref" {
			return m
		}
	}
	if s, ok := value.(string); ok {
		return map[string]any{"kind": "inline", "text": s}
	}
	b, _ := json.Marshal(value)
	return map[string]any{"kind": "inline", "text": string(b)}
}

func strValue(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}
