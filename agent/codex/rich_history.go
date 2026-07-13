package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// GetRichSessionHistory returns structured history with parts, steps, and
// thinking blocks parsed from the Codex session JSONL transcript.
func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	a.mu.RLock()
	codexHome := a.codexHome
	a.mu.RUnlock()
	return getRichSessionHistoryWithContext(ctx, sessionID, codexHome, limit)
}

// TranscriptPath resolves the on-disk JSONL transcript path for a Codex session,
// implementing core.TranscriptLocator so the bridge can index and page it.
func (a *Agent) TranscriptPath(_ context.Context, sessionID string) (string, error) {
	a.mu.RLock()
	codexHome := a.codexHome
	a.mu.RUnlock()
	path := findSessionFile(sessionID, codexHome)
	if path == "" {
		return "", fmt.Errorf("codex: session file not found for %s", sessionID)
	}
	return path, nil
}

func getRichSessionHistory(sessionID, codexHome string, limit int) ([]core.RichHistoryEntry, error) {
	return getRichSessionHistoryWithContext(context.Background(), sessionID, codexHome, limit)
}

func getRichSessionHistoryWithContext(ctx context.Context, sessionID, codexHome string, limit int) ([]core.RichHistoryEntry, error) {
	path := findSessionFile(sessionID, codexHome)
	if path == "" {
		return nil, fmt.Errorf("session file not found for %s", sessionID)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	started := time.Now()
	entries, err := parseRichHistoryFromJSONL(f, limit)
	var fileBytes int64
	if stat, statErr := f.Stat(); statErr == nil {
		fileBytes = stat.Size()
	}
	core.SessionLoadMetricsFromContext(ctx).AddHistoryParse(time.Since(started), fileBytes)
	return entries, err
}

func parseRichHistoryFromJSONL(f *os.File, limit int) ([]core.RichHistoryEntry, error) {
	return ParseRichHistoryFromReader(f, limit)
}

// ParseRichHistoryFromReader runs the Codex logical-message grouping builder
// over r (one JSONL transcript or a byte-range section of one) and returns the
// logical messages in file order. It is exported so the bridge can replay a
// transcript page index byte range (design §6.3); the boundaries match the
// codex span extractor in package transcriptindex.
func ParseRichHistoryFromReader(r io.Reader, limit int) ([]core.RichHistoryEntry, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 256*1024), codexSessionScannerMaxTokenSize)

	builder := &richHistoryBuilder{maxEntries: limit}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)

		if raw.Type == "event_msg" {
			var envelope map[string]any
			if json.Unmarshal(raw.Payload, &envelope) != nil {
				continue
			}
			payload := envelope
			if nested, ok := envelope["payload"].(map[string]any); ok {
				payload = nested
			}
			if strings.TrimSpace(appServerStringValue(payload["type"])) == "patch_apply_end" {
				builder.addPatchResultByCallID(
					strings.TrimSpace(appServerStringValue(payload["call_id"])),
					appServerPatchChanges(payload["changes"]),
					appServerStringValue(payload["status"]),
				)
			}
			continue
		}

		if raw.Type != "response_item" {
			continue
		}

		var item struct {
			Role      string          `json:"role"`
			Type      string          `json:"type"`
			Name      string          `json:"name"`
			Input     string          `json:"input"`
			Text      string          `json:"text"`
			Output    json.RawMessage `json:"output"`
			Status    string          `json:"status"`
			Command   string          `json:"command"`
			CallID    string          `json:"call_id"`
			Arguments json.RawMessage `json:"arguments"`
			Summary   []string        `json:"summary"`
			Content   []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Payload, &item) != nil {
			continue
		}

		switch {
		case item.Role == "user" && len(item.Content) > 0:
			builder.flush()
			var textParts []string
			parts := make([]map[string]any, 0, len(item.Content))
			files := make([]map[string]any, 0)
			for _, c := range item.Content {
				if c.Type == "input_text" && c.Text != "" && isUserPrompt(c.Text) {
					textParts = append(textParts, c.Text)
					parts = append(parts, map[string]any{"type": "text", "content": c.Text})
					continue
				}
				if c.Type == "input_image" {
					if file, ok := codexInputImageFile(c.ImageURL, len(files)+1); ok {
						files = append(files, file)
						parts = append(parts, map[string]any{"type": "file", "file": file})
					}
				}
			}
			if len(textParts) > 0 || len(files) > 0 {
				builder.addEntry(core.RichHistoryEntry{
					Role:      "user",
					Content:   strings.Join(textParts, "\n"),
					Parts:     parts,
					Files:     files,
					Timestamp: ts,
				})
			}

		case item.Role == "assistant" && len(item.Content) > 0 && (item.Type == "" || item.Type == "message"):
			var textParts []string
			var parts []map[string]any
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					textParts = append(textParts, c.Text)
					parts = append(parts, map[string]any{"type": "text", "content": c.Text})
				}
			}
			if len(textParts) > 0 {
				builder.addText(ts, textParts, parts)
			}

		case item.Type == "reasoning":
			text := item.Text
			if text == "" && len(item.Summary) > 0 {
				for _, s := range item.Summary {
					if text != "" {
						text += "\n"
					}
					text += s
				}
			}
			if text != "" {
				builder.addReasoning(ts, text)
			}

		case item.Type == "function_call" && item.Name == "update_plan":

		case item.Type == "function_call":
			toolName := item.Name
			if mapped, ok := codexToolNames[toolName]; ok {
				toolName = mapped
			}
			if toolName == "" {
				toolName = "Unknown"
			}
			toolInput := extractToolInput(item.Name, item.Arguments)
			builder.addToolUse(ts, toolName, toolInput, item.CallID)

		case item.Type == "command_execution":
			builder.addToolUse(ts, "Bash", item.Command, "")

		case item.Type == "function_call_output":
			builder.addToolResultByCallID(item.CallID, codexTranscriptOutput(item.Output), item.Status)

		case item.Type == "custom_tool_call":
			toolName, toolInput := codexCustomToolUse(item.Name, item.Input)
			if toolName != "" {
				builder.addToolUse(ts, toolName, toolInput, item.CallID)
			}

		case item.Type == "custom_tool_call_output":
			builder.addToolResultByCallID(item.CallID, codexTranscriptOutput(item.Output), item.Status)

		default:
			slog.Debug("codex rich history: unhandled response_item",
				"role", item.Role, "type", item.Type, "name", item.Name)
		}
	}
	builder.flush()
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read codex rich history: %w", err)
	}

	entries := builder.entries
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

func decodeCodexFunctionCallArgumentsRaw(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	return string(raw), nil
}

// codexTranscriptOutput accepts both legacy string output and the current
// structured content-item array used by Codex Desktop custom tools. Keeping
// the text fragments lets history replay restore real tool completion events.
func codexTranscriptOutput(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.TrimSpace(part.Text) != "" {
				texts = append(texts, part.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return string(raw)
}

func extractToolInput(toolName string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if toolName == "exec_command" {
			var obj struct {
				Cmd string `json:"cmd"`
			}
			if json.Unmarshal([]byte(s), &obj) == nil && obj.Cmd != "" {
				return obj.Cmd
			}
		}
		return s
	}
	return string(raw)
}

var codexExecCommandInputPattern = regexp.MustCompile(`tools\.exec_command\(\s*\{[^{}]*"cmd"\s*:\s*"((?:\\.|[^"\\])*)"`)
var codexPatchTargetPattern = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: ([^\r\n]+)`)

// codexCustomToolUse unwraps the Codex Desktop `exec` wrapper when its source
// contains exactly one real operation. This preserves the actual command or
// patch target in history rather than presenting the JavaScript wrapper as a
// generic tool. Mixed/multi-operation wrappers deliberately remain generic:
// guessing one operation would make the timeline less truthful.
func codexCustomToolUse(name, input string) (string, string) {
	name = strings.TrimSpace(name)
	if name == "apply_patch" {
		return "Patch", codexPatchTarget(input)
	}
	if name != "exec" {
		return name, input
	}

	hasCommand := strings.Count(input, "tools.exec_command")
	hasPatch := strings.Count(input, "tools.apply_patch")
	if hasCommand == 1 && hasPatch == 0 {
		if command := codexWrappedExecCommand(input); command != "" {
			return "exec_command", command
		}
	}
	if hasPatch == 1 && hasCommand == 0 {
		return "Patch", codexPatchTarget(input)
	}
	return name, input
}

func codexWrappedExecCommand(input string) string {
	match := codexExecCommandInputPattern.FindStringSubmatch(input)
	if len(match) != 2 {
		return ""
	}
	command, err := strconv.Unquote(`"` + match[1] + `"`)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(command)
}

func codexPatchTarget(input string) string {
	match := codexPatchTargetPattern.FindStringSubmatch(input)
	if len(match) == 2 && strings.TrimSpace(match[1]) != "" {
		return strings.TrimSpace(match[1])
	}
	return "编辑文件"
}

func codexInputImageFile(imageURL string, index int) (map[string]any, bool) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return nil, false
	}
	mime := codexInputImageMime(imageURL)
	hash := sha256.Sum256([]byte(imageURL))
	id := fmt.Sprintf("codex-image-%x", hash[:8])
	return map[string]any{
		"id":       id,
		"mime":     mime,
		"url":      imageURL,
		"filename": fmt.Sprintf("image-%d%s", index, codexImageExtension(mime)),
	}, true
}

func codexInputImageMime(imageURL string) string {
	const defaultMime = "image/jpeg"
	if !strings.HasPrefix(imageURL, "data:") {
		return defaultMime
	}
	metadata, _, ok := strings.Cut(strings.TrimPrefix(imageURL, "data:"), ",")
	if !ok {
		return defaultMime
	}
	mediaType, _, _ := strings.Cut(metadata, ";")
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if strings.HasPrefix(mediaType, "image/") {
		return mediaType
	}
	return defaultMime
}

func codexImageExtension(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	default:
		return ".jpg"
	}
}

type richHistoryBuilder struct {
	entries    []core.RichHistoryEntry
	current    *core.RichHistoryEntry
	callIDMap  map[string]int
	maxEntries int
}

func (b *richHistoryBuilder) addEntry(entry core.RichHistoryEntry) {
	b.flush()
	b.entries = append(b.entries, entry)
}

func (b *richHistoryBuilder) addText(ts time.Time, texts []string, parts []map[string]any) {
	if b.current != nil && b.current.Role == "assistant" {
		fullContent := ""
		for _, t := range texts {
			if fullContent != "" {
				fullContent += "\n"
			}
			fullContent += t
		}
		b.current.Content += fullContent
		b.current.Parts = append(b.current.Parts, parts...)
		return
	}
	b.flush()
	content := ""
	for _, t := range texts {
		if content != "" {
			content += "\n"
		}
		content += t
	}
	b.current = &core.RichHistoryEntry{
		Role:      "assistant",
		Content:   content,
		Parts:     parts,
		Steps:     []map[string]any{},
		Files:     []map[string]any{},
		Timestamp: ts,
	}
}

func (b *richHistoryBuilder) addReasoning(ts time.Time, text string) {
	if b.current != nil && b.current.Role == "assistant" {
		if b.current.Thinking != "" {
			b.current.Thinking += "\n"
		}
		b.current.Thinking += text
		b.current.Parts = append(b.current.Parts, map[string]any{"type": "reasoning", "content": text})
		return
	}
	b.flush()
	b.current = &core.RichHistoryEntry{
		Role:      "assistant",
		Thinking:  text,
		Parts:     []map[string]any{{"type": "reasoning", "content": text}},
		Steps:     []map[string]any{},
		Files:     []map[string]any{},
		Timestamp: ts,
	}
}

func (b *richHistoryBuilder) addToolUse(ts time.Time, toolName, toolInput, callID string) {
	if b.current != nil && b.current.Role == "assistant" {
		stepID := fmt.Sprintf("tool-%d", len(b.current.Steps)+1)
		step := map[string]any{
			"id":                             stepID,
			"toolName":                       toolName,
			"status":                         "running",
			"output":                         map[string]any{"kind": "inline", "text": ""},
			"duration":                       nil,
			"requiresPermissionConfirmation": false,
			"availablePermissionOptions":     []any{},
		}
		if toolInput != "" {
			step["title"] = toolInput
		}
		b.current.Parts = append(b.current.Parts, map[string]any{"type": "tool", "step": step})
		b.current.Steps = append(b.current.Steps, step)
		if callID != "" {
			if b.callIDMap == nil {
				b.callIDMap = make(map[string]int)
			}
			b.callIDMap[callID] = len(b.current.Steps) - 1
		}
		return
	}
	b.flush()
	stepID := "tool-1"
	step := map[string]any{
		"id":                             stepID,
		"toolName":                       toolName,
		"status":                         "running",
		"output":                         map[string]any{"kind": "inline", "text": ""},
		"duration":                       nil,
		"requiresPermissionConfirmation": false,
		"availablePermissionOptions":     []any{},
	}
	if toolInput != "" {
		step["title"] = toolInput
	}
	b.current = &core.RichHistoryEntry{
		Role:      "assistant",
		Parts:     []map[string]any{{"type": "tool", "step": step}},
		Steps:     []map[string]any{step},
		Files:     []map[string]any{},
		Timestamp: ts,
	}
	if callID != "" {
		if b.callIDMap == nil {
			b.callIDMap = make(map[string]int)
		}
		b.callIDMap[callID] = 0
	}
}

func (b *richHistoryBuilder) addToolResult(ts time.Time, toolName, output, status string) {
	if b.current == nil {
		return
	}
	if len(b.current.Steps) == 0 {
		return
	}
	stepIdx := len(b.current.Steps) - 1
	step := b.current.Steps[stepIdx]
	if status == "" {
		status = "completed"
	}
	step["status"] = status
	if output != "" {
		step["output"] = map[string]any{"kind": "inline", "text": output}
	}
	if toolName != "" {
		step["toolName"] = toolName
	}
	_ = ts
}

func (b *richHistoryBuilder) addToolResultByCallID(callID, output, status string) {
	if b.current == nil {
		return
	}
	if callID != "" {
		idx, ok := b.callIDMap[callID]
		if !ok || idx >= len(b.current.Steps) {
			return
		}
		step := b.current.Steps[idx]
		if status == "" {
			status = "completed"
		}
		step["status"] = status
		if output != "" {
			step["output"] = map[string]any{"kind": "inline", "text": output}
		}
		return
	}
	b.addToolResult(time.Time{}, "", output, status)
}

func (b *richHistoryBuilder) addPatchResultByCallID(callID string, changes []core.FileChange, status string) {
	if b.current == nil || len(changes) == 0 {
		return
	}
	// Newer Codex transcripts can emit patch_apply_end before the matching
	// custom_tool_call has been recorded in the response-item stream. There is
	// no step to attach it to in that ordering, so leave it unrendered rather
	// than indexing Steps[-1] and dropping the whole RPC connection.
	if len(b.current.Steps) == 0 {
		return
	}
	stepIdx := len(b.current.Steps) - 1
	if callID != "" {
		idx, ok := b.callIDMap[callID]
		if ok && idx < len(b.current.Steps) {
			stepIdx = idx
		}
	}
	step := b.current.Steps[stepIdx]
	if status == "" {
		status = "completed"
	}
	step["toolName"] = "Patch"
	step["status"] = status
	step["output"] = map[string]any{"kind": "inline", "text": appServerFileChangeResult(changes)}
	step["fileChanges"] = richHistoryFileChanges(changes)
}

func richHistoryFileChanges(changes []core.FileChange) []map[string]any {
	if len(changes) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(changes))
	for _, change := range changes {
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		item := map[string]any{
			"path": path,
			"kind": change.Kind,
		}
		if change.Diff != "" {
			item["diff"] = change.Diff
		}
		if change.MovePath != "" {
			item["movePath"] = change.MovePath
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func (b *richHistoryBuilder) flush() {
	if b.current != nil {
		if len(b.current.Parts) == 0 {
			b.current.Parts = []map[string]any{{"type": "text", "content": b.current.Content}}
		}
		b.entries = append(b.entries, *b.current)
		if b.maxEntries > 0 && len(b.entries) > b.maxEntries {
			b.entries = b.entries[len(b.entries)-b.maxEntries:]
		}
		b.current = nil
		b.callIDMap = nil
	}
}
