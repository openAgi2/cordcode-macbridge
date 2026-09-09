package grokbuild

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// readRichDisplayHistoryFromUpdates reduces Grok Build's durable ACP update
// stream into the user-visible transcript. This intentionally does not apply
// CompactionCheckpoint truncation: upstream applies that only while rebuilding
// chat_history.jsonl, its model-context cache, while session export retains the
// complete update stream as the source of truth.
func readRichDisplayHistoryFromUpdates(sessionDir, sessionID string) ([]core.RichHistoryEntry, bool, error) {
	f, err := os.Open(filepath.Join(sessionDir, "updates.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	var entries []core.RichHistoryEntry
	var user updateUserAccumulator
	var assistant updateAssistantAccumulator
	sawDisplayContent := false
	sawCompaction := false
	flushUser := func() {
		entry := user.build()
		user = updateUserAccumulator{}
		if entry == nil {
			return
		}
		if objective, ok := grokGoalObjectiveFromReminder(entry.Content); ok {
			entries = append(entries, core.RichHistoryEntry{ID: entry.ID, Role: "system", Parts: []map[string]any{{
				"type": "command", "commandId": entry.ID, "name": "goal", "args": objective,
				"kind": "success", "line": "/goal " + objective,
			}}})
			return
		}
		if looksLikeFrameworkBootstrap(entry.Content) {
			return
		}
		entries = append(entries, *entry)
	}
	flushAssistant := func() {
		if entry := assistant.build(); entry != nil {
			entries = append(entries, *entry)
		}
		assistant = updateAssistantAccumulator{}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	lineNum := 0
	for sc.Scan() {
		rawLine := bytes.TrimSpace(sc.Bytes())
		if len(rawLine) == 0 {
			continue
		}
		lineNum++
		var row displayUpdateRow
		if json.Unmarshal(rawLine, &row) != nil || !isSessionUpdateMethod(row.Method) {
			continue
		}
		if owner := strings.TrimSpace(row.Params.SessionID); owner != "" && owner != sessionID {
			continue
		}

		kind := strings.TrimSpace(row.Params.Update.SessionUpdate)
		hostTurn := row.Params.Update.Meta.HostTurn
		switch kind {
		case "compaction_checkpoint":
			sawCompaction = true

		case "user_message_chunk":
			if hostTurn {
				flushUser()
				flushAssistant()
				continue
			}
			text := contentTextFromRaw(row.Params.Update.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			sawDisplayContent = true
			flushAssistant()
			user.addText(
				displayUpdateID(sessionID, lineNum, rawLine, row.Params.Meta.EventID),
				text,
				displayUpdateTime(row),
			)

		case "agent_message_chunk":
			flushUser()
			if hostTurn {
				flushAssistant()
				continue
			}
			text := contentTextFromRaw(row.Params.Update.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			sawDisplayContent = true
			assistant.addText(
				displayUpdateID(sessionID, lineNum, rawLine, row.Params.Meta.EventID),
				text,
				displayUpdateTime(row),
			)

		case "agent_thought_chunk":
			flushUser()
			text := contentTextFromRaw(row.Params.Update.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			assistant.addReasoning(
				displayUpdateID(sessionID, lineNum, rawLine, row.Params.Meta.EventID),
				text,
				displayUpdateTime(row),
			)

		case "tool_call":
			flushUser()
			assistant.addTool(
				displayUpdateID(sessionID, lineNum, rawLine, row.Params.Meta.EventID),
				row.Params.Update.ToolCallID,
				row.Params.Update.Title,
				row.Params.Update.Status,
				row.Params.Update.RawInput,
				displayUpdateTime(row),
			)

		case "tool_call_update":
			assistant.finishTool(
				row.Params.Update.ToolCallID,
				row.Params.Update.Title,
				row.Params.Update.Status,
				row.Params.Update.Content,
				row.Params.Update.RawOutput,
			)

		case "turn_completed":
			flushUser()
			flushAssistant()
		}
	}
	flushUser()
	flushAssistant()
	if err := sc.Err(); err != nil {
		return entries, sawCompaction && sawDisplayContent, err
	}
	return entries, sawCompaction && sawDisplayContent, nil
}

type displayUpdateRow struct {
	Timestamp int64  `json:"timestamp"`
	Method    string `json:"method"`
	Params    struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Content       json.RawMessage `json:"content"`
			ToolCallID    string          `json:"toolCallId"`
			Title         string          `json:"title"`
			Status        string          `json:"status"`
			RawInput      json.RawMessage `json:"rawInput"`
			RawOutput     json.RawMessage `json:"rawOutput"`
			Meta          struct {
				HostTurn bool `json:"hostTurn"`
			} `json:"_meta"`
		} `json:"update"`
		Meta struct {
			EventID          string `json:"eventId"`
			AgentTimestampMs int64  `json:"agentTimestampMs"`
		} `json:"_meta"`
	} `json:"params"`
}

func displayUpdateID(sessionID string, lineNum int, rawLine []byte, eventID string) string {
	if id := strings.TrimSpace(eventID); id != "" {
		return id
	}
	return deriveStableMessageID(sessionID, lineNum, rawLine)
}

func displayUpdateTime(row displayUpdateRow) time.Time {
	if row.Params.Meta.AgentTimestampMs > 0 {
		return time.UnixMilli(row.Params.Meta.AgentTimestampMs)
	}
	if row.Timestamp > 0 {
		return time.Unix(row.Timestamp, 0)
	}
	return time.Time{}
}

func contentTextFromRaw(raw json.RawMessage) string {
	return (sessionUpdatePayload{Content: raw}).contentText()
}

type updateUserAccumulator struct {
	id        string
	text      string
	timestamp time.Time
}

func (u *updateUserAccumulator) addText(id, text string, timestamp time.Time) {
	if u.id == "" {
		u.id = id
		u.timestamp = timestamp
	}
	u.text += text
}

func (u *updateUserAccumulator) build() *core.RichHistoryEntry {
	text := strings.TrimSpace(u.text)
	if u.id == "" || text == "" {
		return nil
	}
	return &core.RichHistoryEntry{
		ID:        u.id,
		Role:      "user",
		Content:   text,
		Timestamp: u.timestamp,
	}
}

type updateAssistantAccumulator struct {
	id        string
	text      string
	thinking  string
	parts     []map[string]any
	steps     []map[string]any
	timestamp time.Time
}

func (a *updateAssistantAccumulator) start(id string, timestamp time.Time) {
	if a.id == "" {
		a.id = id
		a.timestamp = timestamp
	}
}

func (a *updateAssistantAccumulator) addText(id, text string, timestamp time.Time) {
	a.start(id, timestamp)
	a.text += text
	a.parts = append(a.parts, map[string]any{"type": "text", "content": text})
}

func (a *updateAssistantAccumulator) addReasoning(id, text string, timestamp time.Time) {
	a.start(id, timestamp)
	a.thinking += text
	a.parts = append(a.parts, map[string]any{"type": "reasoning", "content": text})
}

func (a *updateAssistantAccumulator) addTool(id, callID, title, status string, rawInput json.RawMessage, timestamp time.Time) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	a.start(id, timestamp)
	if a.findTool(callID) != nil {
		return
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "pending"
	}
	step := map[string]any{
		"id":                             callID,
		"toolName":                       strings.TrimSpace(title),
		"status":                         status,
		"output":                         map[string]any{"kind": "inline", "text": ""},
		"duration":                       nil,
		"requiresPermissionConfirmation": false,
		"availablePermissionOptions":     []any{},
	}
	if input := rawJSONText(rawInput); input != "" {
		step["input"] = input
	}
	a.steps = append(a.steps, step)
	a.parts = append(a.parts, map[string]any{"type": "tool", "step": step})
}

func (a *updateAssistantAccumulator) finishTool(callID, title, status string, content, rawOutput json.RawMessage) {
	step := a.findTool(strings.TrimSpace(callID))
	if step == nil {
		return
	}
	if name := strings.TrimSpace(title); name != "" && strings.TrimSpace(toString(step["toolName"])) == "" {
		step["toolName"] = name
	}
	if normalized := strings.ToLower(strings.TrimSpace(status)); normalized != "" {
		step["status"] = normalized
	}
	output := contentTextFromRaw(content)
	if strings.TrimSpace(output) == "" {
		output = rawJSONText(rawOutput)
	}
	if isGenericSuccessMessage(output) {
		output = ""
	}
	step["output"] = map[string]any{"kind": "inline", "text": output}
}

func (a *updateAssistantAccumulator) findTool(callID string) map[string]any {
	for _, step := range a.steps {
		if strings.TrimSpace(toString(step["id"])) == callID {
			return step
		}
	}
	return nil
}

func rawJSONText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (a *updateAssistantAccumulator) build() *core.RichHistoryEntry {
	if a.id == "" || (a.text == "" && a.thinking == "" && len(a.parts) == 0 && len(a.steps) == 0) {
		return nil
	}
	return &core.RichHistoryEntry{
		ID:        a.id,
		Role:      "assistant",
		Content:   a.text,
		Thinking:  a.thinking,
		Parts:     a.parts,
		Steps:     a.steps,
		Files:     []map[string]any{},
		Timestamp: a.timestamp,
	}
}
