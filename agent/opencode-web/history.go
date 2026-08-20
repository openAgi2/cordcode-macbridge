package opencodeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func trimSpaceBytes(raw []byte) []byte {
	return bytes.TrimSpace(raw)
}

// GetRichSessionHistory implements core.RichHistoryProvider: GET
// /session/:id/message (v2: /api prefix). The mapping copies the legacy
// semantics (info 下沉、parts[].type text/reasoning/tool/file、tool
// state.status/output) — copied per design §7 兜底, owned by this package.
func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	return a.getRichHistory(ctx, c, sessionID, limit)
}

func (a *Agent) getRichHistory(ctx context.Context, c *Client, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/session/")+sessionID+"/message", a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}

	result := make([]core.RichHistoryEntry, 0, len(items))
	for i, item := range items {
		var message map[string]any
		if err := json.Unmarshal(item, &message); err != nil {
			return nil, fmt.Errorf("opencode-web: history row %d malformed: %w", i, err)
		}
		entry, err := mapRichHistoryEntry(message)
		if err != nil {
			return nil, fmt.Errorf("opencode-web: history row %d: %w", i, err)
		}
		result = append(result, entry)
	}
	return result, nil
}

// GetSessionHistory implements the legacy HistoryProvider surface by folding
// the rich entries to plain turns.
func (a *Agent) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	rich, err := a.GetRichSessionHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.HistoryEntry, 0, len(rich))
	for _, e := range rich {
		out = append(out, core.HistoryEntry{
			Role:      e.Role,
			Content:   e.Content,
			Timestamp: e.Timestamp,
		})
	}
	return out, nil
}

var _ core.RichHistoryProvider = (*Agent)(nil)
var _ core.HistoryProvider = (*Agent)(nil)

// errUnsupportedReasoning is the canonical §6.3/§6.5 verdict for populated
// reasoning parts: E2 proved no verified populated reasoning shape on
// 1.18.18, so reasoning is explicitly UNSUPPORTED — the hydrate FAILS with
// this diagnosable error; the part is never mapped, dropped, or folded into
// answer text.
var errUnsupportedReasoning = errors.New("unsupported content.reasoning for verified 1.18.18 shape")

// mapRichHistoryEntry maps one official message element to
// core.RichHistoryEntry. Unknown part types are ignored; a POPULATED
// reasoning part fails the mapping (errUnsupportedReasoning).
func mapRichHistoryEntry(message map[string]any) (core.RichHistoryEntry, error) {
	info := message
	if sub, ok := message["info"].(map[string]any); ok {
		info = sub
	}

	timeMap, _ := info["time"].(map[string]any)
	parts, _ := message["parts"].([]any)
	if parts == nil {
		parts, _ = info["parts"].([]any)
	}

	var content string
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
			if strings.TrimSpace(text) != "" {
				return core.RichHistoryEntry{}, errUnsupportedReasoning
			}
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

	// A provider-failed assistant message has no text parts but carries
	// info.error — surface the serve's text so cold-opens match the web.
	if strings.TrimSpace(content) == "" {
		if errMsg := errorMessageFromInfo(info); errMsg != "" {
			content = errMsg
		}
	}

	return core.RichHistoryEntry{
		ID:         id,
		Role:       role,
		Content:    content,
		Parts:      mappedParts,
		Steps:      steps,
		Files:      []map[string]any{},
		Timestamp:  timestamp,
		AgentName:  strValue(info, "agent"),
		ModelID:    strValue(info, "modelID"),
		ProviderID: strValue(info, "providerID"),
		ModelName:  strValue(info, "modelName"),
	}, nil
}

// errorMessageFromInfo reads info.error.data.message (1.18.18 shape) with
// name/message fallbacks.
func errorMessageFromInfo(info map[string]any) string {
	if info == nil {
		return ""
	}
	err := firstMap(info, "error")
	if err == nil {
		return ""
	}
	if data := firstMap(err, "data"); data != nil {
		if msg := firstString(data, "message"); msg != "" {
			return msg
		}
	}
	return firstString(err, "message")
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

// fetchMessageMaps is the raw message accessor shared by the usage formula —
// the formula must read the same elements the history mapping reads.
func (a *Agent) fetchMessageMaps(ctx context.Context, c *Client, sessionID string) ([]map[string]any, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/session/")+sessionID+"/message", a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		var message map[string]any
		if err := json.Unmarshal(item, &message); err != nil {
			continue
		}
		out = append(out, message)
	}
	return out, nil
}

// fetchMessageMapsWithClient lets the SSE recompute path reuse an
// already-generation-pinned client instead of re-probing.
func (a *Agent) fetchMessageMapsWithClient(ctx context.Context, c *Client, sessionID string) ([]map[string]any, error) {
	return a.fetchMessageMaps(ctx, c, sessionID)
}

// lastAssistantWithTokens scans backward for the last assistant message whose
// token total is positive (official web formula step 1, design §3.3). info is
// the下沉 info map; ok=false when no such message exists.
func lastAssistantWithTokens(messages []map[string]any) (info map[string]any, ok bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		info := message
		if sub, exists := message["info"].(map[string]any); exists {
			info = sub
		}
		role, _ := info["role"].(string)
		if role != "assistant" {
			continue
		}
		if usageTotalFor(info) > 0 {
			return info, true
		}
	}
	return nil, false
}

// usageTotalFor computes total = input+output+reasoning+cache.read+cache.write
// from a message-level info map (formula step 2). Falls back to the message
// level `total` field (present only at message level — S1) when the parts sum
// to zero.
func usageTotalFor(info map[string]any) int {
	tokens, _ := info["tokens"].(map[string]any)
	if tokens == nil {
		return 0
	}
	cacheRead, cacheWrite := 0, 0
	if cache, ok := tokens["cache"].(map[string]any); ok {
		cacheRead = anyInt(cache["read"])
		cacheWrite = anyInt(cache["write"])
	}
	// Official tokenTotal is STRICTLY the five-part sum
	// (session-context-metrics.ts) — no `total`-field fallback.
	return anyInt(tokens["input"]) + anyInt(tokens["output"]) + anyInt(tokens["reasoning"]) + cacheRead + cacheWrite
}

func anyInt(v any) int {
	switch typed := v.(type) {
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}
