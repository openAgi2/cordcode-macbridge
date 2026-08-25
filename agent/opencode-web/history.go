package opencodeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func trimSpaceBytes(raw []byte) []byte {
	return bytes.TrimSpace(raw)
}

// GetRichSessionHistory implements core.RichHistoryProvider: GET
// /session/:id/message (v2: /api prefix). Parts follow official 1.18.18
// MessageV2: info 下沉, parts[].type text/reasoning/tool/file. Tool parts
// are schema ToolPart (`tool` is the name string, `state` is a sibling) —
// the same shape official Desktop loads via session.messages and renders
// through session-ui renderable()/groupParts. Nested `tool: {toolName,state}`
// is only kept so the copied legacy fixture still maps.
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
	return MapMessageListToRichEntries(raw, limit)
}

// MapMessageListToRichEntries 把 GET /session/:id/message 的响应体（列表）映射为
// rich history entries。导出供 PERF-S0B fixture 生成测试复用同一真实映射（不复制逻辑）。
func MapMessageListToRichEntries(body []byte, limit int) ([]core.RichHistoryEntry, error) {
	items, err := decodeListPayload(body)
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

// errUnsupportedReasoning was the pre-2026-08-21 live verdict for populated
// reasoning. It is retired: emitting it as core.EventError settled every
// reasoning-model turn as turn_error and tore the live relay down mid-stream
// (owner 真机 2026-08-21). Live carriers now SKIP populated reasoning
// untranslated (skipLiveReasoning); E2b-verified HTTP history maps it as
// first-class content via mapRichHistoryEntry.

// mapRichHistoryEntry maps one official message element to
// core.RichHistoryEntry. Unknown part types are ignored; a POPULATED
// reasoning part maps as first-class content in server order (E2b,
// directive-014).
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
			// Canonical §6.3 (corrected, directive-014): populated reasoning
			// in the authoritative HTTP history is E2b-evidenced and maps as a
			// first-class part with the exact server text, order preserved —
			// never folded into Content, dropped, or truncated. Missing or
			// non-string text is a shape violation (fail closed); a
			// whitespace-only text is not populated and is skipped, matching
			// the live-carrier population check.
			raw, exists := part["text"]
			if !exists {
				return core.RichHistoryEntry{}, fmt.Errorf("opencode-web: reasoning part missing required text")
			}
			text, ok := raw.(string)
			if !ok {
				return core.RichHistoryEntry{}, fmt.Errorf("opencode-web: reasoning part text must be a string")
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			if thinking != "" {
				thinking += "\n"
			}
			thinking += text
			mappedParts = append(mappedParts, map[string]any{"type": "reasoning", "content": text})
		case "step-start", "step-finish", "patch":
			// Official Web skip list (E2b officialWeb.skipParts) — lifecycle
			// markers, never projection content. Only these evidenced types
			// are skipped; this is not a general unknown-part amnesty.
		case "tool":
			step := mapToolStepFromPart(part)
			if step == nil {
				continue
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
		Thinking:   thinking,
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

// mapToolStepFromPart translates one official (or legacy-nested) tool part
// into the projection step map hydrateToolEventsFromStep consumes.
//
// Official 1.18.18 ToolPart (packages/schema/src/v1/session.ts; live GET
// /session/:id/message on 4096; sample a6/a8):
//
//	{type:"tool", id, callID, tool:"read"|"edit"|..., state:{status,input,output,title,metadata,time}}
//
// `tool` is a STRING name. The previous mapper type-asserted it to
// map[string]any and skipped every official part — cold hydrate therefore
// had reasoning/text but zero tool cards (owner 2026-08-21, 红楼梦 session).
func mapToolStepFromPart(part map[string]any) map[string]any {
	if part == nil {
		return nil
	}
	toolName := ""
	toolID := firstString(part, "id", "callID")
	state := firstMap(part, "state")
	switch toolVal := part["tool"].(type) {
	case string:
		toolName = strings.TrimSpace(toolVal)
	case map[string]any:
		// Copied-legacy fixture shape (tool nested object). Not the 1.18.18
		// HTTP part; kept so existing tests and the live SSE nested fallback
		// stay consistent.
		if toolName == "" {
			toolName = firstString(toolVal, "toolName", "name")
		}
		if toolID == "" {
			toolID = firstString(toolVal, "id", "toolName")
		}
		if state == nil {
			state = firstMap(toolVal, "state")
		}
	}
	if toolName == "" {
		toolName = firstString(part, "name")
	}
	if toolName == "" && toolID == "" {
		return nil
	}
	// Official session-ui HIDDEN_TOOLS: todowrite lives in the todo dock, not
	// the timeline (packages/session-ui/src/components/message-part.tsx).
	if toolName == "todowrite" {
		return nil
	}
	if toolID == "" {
		toolID = toolName
	}
	status := "completed"
	if s := firstString(state, "status"); s != "" {
		status = s
	}
	var output any
	var duration any
	if state != nil {
		output = state["output"]
		if output == nil {
			output = state["error"]
		}
		duration = state["durationMs"]
		if duration == nil {
			if timeMap := firstMap(state, "time"); timeMap != nil {
				start := firstNumeric(timeMap, "start")
				end := firstNumeric(timeMap, "end")
				if end > start && start > 0 {
					duration = end - start
				}
			}
		}
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
	if title := firstString(state, "title"); title != "" {
		step["title"] = title
	}
	if state != nil {
		if input := state["input"]; input != nil {
			step["toolInput"] = input
		}
	}
	if changes := fileChangesFromToolState(state); len(changes) > 0 {
		step["fileChanges"] = changes
	}
	return step
}

// fileChangesFromToolState reads official edit metadata.filediff
// ({file, patch, additions, deletions}) into the projection fileChanges
// vocabulary iOS already renders for Claude/Codex.
func fileChangesFromToolState(state map[string]any) []map[string]any {
	meta := firstMap(state, "metadata")
	if meta == nil {
		return nil
	}
	filediff := firstMap(meta, "filediff")
	if filediff == nil {
		return nil
	}
	path := firstString(filediff, "file", "path")
	if path == "" {
		return nil
	}
	diff := firstString(filediff, "patch")
	if diff == "" {
		diff = firstString(meta, "diff")
	}
	change := map[string]any{
		"path": path,
		"kind": "edit",
	}
	if diff != "" {
		change["diff"] = diff
	}
	return []map[string]any{change}
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
