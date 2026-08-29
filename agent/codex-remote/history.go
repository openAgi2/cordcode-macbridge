package codexremote

// history.go is the Remote app-server cold baseline. Remote Control carries
// ordinary app-server JSON-RPC after the envelope is removed, so these wire
// structs follow the official v2 Thread/Turn/Item schema rather than a
// rollout file, daemon cache, or codex-web state. Unknown item variants stay
// visible in SkippedTypes; this adapter never invents an id or terminal state.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	remoteTurnItemsViewFull       = "full"
	remoteTurnItemsViewSummary    = "summary"
	remoteTurnItemsViewNotLoaded  = "notLoaded"
	remoteTurnStatusCompleted     = "completed"
	remoteTurnStatusInterrupted   = "interrupted"
	remoteTurnStatusFailed        = "failed"
	remoteTurnStatusInProgress    = "inProgress"
	remoteThreadStatusActive      = "active"
	remoteThreadStatusIdle        = "idle"
	remoteThreadStatusNotLoaded   = "notLoaded"
	remoteThreadStatusSystemError = "systemError"
)

// remoteThreadStatus is the official ThreadStatus one-of. The current v2
// schema serializes it as {type,activeFlags}; accepting a string also keeps
// the reader compatible with older private app-server builds. Unknown values
// remain conservative in IsSessionActive.
type remoteThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

func (s *remoteThreadStatus) UnmarshalJSON(raw []byte) error {
	var object struct {
		Type        string   `json:"type"`
		ActiveFlags []string `json:"activeFlags"`
	}
	if err := json.Unmarshal(raw, &object); err == nil && object.Type != "" {
		s.Type, s.ActiveFlags = object.Type, object.ActiveFlags
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		s.Type = value
		s.ActiveFlags = nil
		return nil
	}
	return fmt.Errorf("codex-remote: invalid thread status")
}

type remoteThread struct {
	ID        string             `json:"id"`
	Name      *string            `json:"name"`
	Preview   string             `json:"preview"`
	CreatedAt int64              `json:"createdAt"`
	UpdatedAt int64              `json:"updatedAt"`
	RecencyAt *int64             `json:"recencyAt"`
	Cwd       string             `json:"cwd"`
	Status    remoteThreadStatus `json:"status"`
	Turns     []remoteTurn       `json:"turns"`
}

type remoteTurnError struct {
	Message           string          `json:"message"`
	AdditionalDetails *string         `json:"additionalDetails"`
	CodexErrorInfo    json.RawMessage `json:"codexErrorInfo"`
}

type remoteTurn struct {
	ID          string            `json:"id"`
	Items       []json.RawMessage `json:"items"`
	ItemsView   string            `json:"itemsView"`
	Status      string            `json:"status"`
	Error       *remoteTurnError  `json:"error"`
	StartedAt   *int64            `json:"startedAt"`
	CompletedAt *int64            `json:"completedAt"`
	DurationMs  *int64            `json:"durationMs"`
}

// remoteUserContentPart mirrors the official UserInput tagged union only for
// fields that can contribute visible text. Image/audio/mention variants stay
// opaque and are deliberately not converted into text.
type remoteUserContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URL  string `json:"url"`
}

type remoteFileUpdateChange struct {
	Path           string          `json:"path"`
	Kind           json.RawMessage `json:"kind"`
	Diff           string          `json:"diff"`
	MovePath       *string         `json:"movePath"`
	LegacyMovePath *string         `json:"move_path"`
}

func (c remoteFileUpdateChange) changeKind() string {
	var kind struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(c.Kind, &kind) == nil && kind.Type != "" {
		return kind.Type
	}
	var value string
	if json.Unmarshal(c.Kind, &value) == nil && value != "" {
		return value
	}
	return string(c.Kind)
}

func (c remoteFileUpdateChange) movePath() *string {
	if c.MovePath != nil {
		return c.MovePath
	}
	if c.LegacyMovePath != nil {
		return c.LegacyMovePath
	}
	var kind struct {
		MovePath       *string `json:"movePath"`
		LegacyMovePath *string `json:"move_path"`
	}
	if json.Unmarshal(c.Kind, &kind) == nil {
		if kind.MovePath != nil {
			return kind.MovePath
		}
		return kind.LegacyMovePath
	}
	return nil
}

// remoteThreadItem is a typed view over the official ThreadItem tagged union.
// Raw is retained so bridge projections can preserve structured result data.
type remoteThreadItem struct {
	Type string
	ID   string
	Raw  json.RawMessage

	Content []remoteUserContentPart
	Text    string
	Summary []string
	Tail    []string

	Command          string
	CommandCwd       string
	CommandStatus    string
	AggregatedOutput *string
	ExitCode         *int32
	ProcessID        *string
	CommandSource    string
	CommandDuration  *int64

	Changes     []remoteFileUpdateChange
	PatchStatus string

	Server     string
	Tool       string
	Arguments  json.RawMessage
	ToolStatus string
	Result     json.RawMessage
	ToolError  json.RawMessage

	Query string
}

func decodeRemoteThreadItem(raw json.RawMessage) remoteThreadItem {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	it := remoteThreadItem{Type: probe.Type, ID: probe.ID, Raw: raw}
	switch probe.Type {
	case "userMessage":
		var value struct {
			Text    string                  `json:"text"`
			Content []remoteUserContentPart `json:"content"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Text, it.Content = value.Text, value.Content
	case "agentMessage", "plan":
		var value struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Text = value.Text
	case "reasoning":
		var value struct {
			Summary []string `json:"summary"`
			Content []string `json:"content"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Summary, it.Tail = value.Summary, value.Content
	case "commandExecution":
		var value struct {
			Command          string  `json:"command"`
			Cwd              string  `json:"cwd"`
			Status           string  `json:"status"`
			AggregatedOutput *string `json:"aggregatedOutput"`
			ExitCode         *int32  `json:"exitCode"`
			ProcessID        *string `json:"processId"`
			Source           string  `json:"source"`
			DurationMs       *int64  `json:"durationMs"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Command, it.CommandCwd, it.CommandStatus = value.Command, value.Cwd, value.Status
		it.AggregatedOutput, it.ExitCode, it.ProcessID = value.AggregatedOutput, value.ExitCode, value.ProcessID
		it.CommandSource, it.CommandDuration = value.Source, value.DurationMs
	case "fileChange":
		var value struct {
			Changes []remoteFileUpdateChange `json:"changes"`
			Status  string                   `json:"status"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Changes, it.PatchStatus = value.Changes, value.Status
	case "mcpToolCall":
		var value struct {
			Server    string          `json:"server"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
			Status    string          `json:"status"`
			Result    json.RawMessage `json:"result"`
			Error     json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Server, it.Tool, it.Arguments = value.Server, value.Tool, value.Arguments
		it.ToolStatus, it.Result, it.ToolError = value.Status, value.Result, value.Error
	case "dynamicToolCall":
		var value struct {
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
			Status    string          `json:"status"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Tool, it.Arguments, it.ToolStatus = value.Tool, value.Arguments, value.Status
	case "webSearch":
		var value struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(raw, &value)
		it.Query = value.Query
	}
	return it
}

func (it remoteThreadItem) userText() string {
	if strings.TrimSpace(it.Text) != "" {
		return it.Text
	}
	var parts []string
	for _, part := range it.Content {
		if part.Type == "text" && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (a *Agent) readThread(ctx context.Context, threadID string) (*remoteThread, error) {
	return a.readThreadWithTurns(ctx, threadID, true)
}

func (a *Agent) readThreadWithTurns(ctx context.Context, threadID string, includeTurns bool) (*remoteThread, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	params := map[string]any{"threadId": threadID}
	if includeTurns {
		params["includeTurns"] = true
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/read", params)
	if err != nil {
		return nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr
	}
	var response struct {
		Thread *remoteThread `json:"thread"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("codex-remote: thread/read decode: %w", err)
	}
	if response.Thread == nil || response.Thread.ID == "" {
		return nil, fmt.Errorf("codex-remote: thread/read missing thread identity")
	}
	return response.Thread, nil
}

func mapRemoteHistoryTurns(thread *remoteThread, limit int) []core.TurnScopedHistoryTurn {
	if thread == nil {
		return nil
	}
	out := make([]core.TurnScopedHistoryTurn, 0, len(thread.Turns))
	for _, turn := range thread.Turns {
		historyTurn := core.TurnScopedHistoryTurn{TurnID: turn.ID, Status: turn.Status}
		if turn.Error != nil {
			historyTurn.ErrorMessage = turn.Error.Message
		}
		if turn.StartedAt != nil {
			historyTurn.StartedAt = time.Unix(*turn.StartedAt, 0).UTC()
			historyTurn.HasTime = true
		}
		if turn.CompletedAt != nil {
			historyTurn.CompletedAt = time.Unix(*turn.CompletedAt, 0).UTC()
		}
		if turn.ItemsView == remoteTurnItemsViewNotLoaded {
			historyTurn.SkippedTypes = append(historyTurn.SkippedTypes, "itemsView:"+remoteTurnItemsViewNotLoaded)
		}
		for _, rawItem := range turn.Items {
			mapRemoteHistoryItem(&historyTurn, decodeRemoteThreadItem(rawItem))
		}
		out = append(out, historyTurn)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func mapRemoteHistoryItem(turn *core.TurnScopedHistoryTurn, item remoteThreadItem) {
	switch item.Type {
	case "userMessage":
		text := item.userText()
		if turn.UserItemID == "" {
			turn.UserItemID, turn.UserText = item.ID, text
		} else if text != "" {
			turn.Parts = append(turn.Parts, map[string]any{"type": "text", "content": text, "itemId": item.ID})
		}
	case "agentMessage":
		if item.Text != "" {
			turn.Parts = append(turn.Parts, map[string]any{"type": "text", "content": item.Text, "itemId": item.ID})
		}
	case "reasoning":
		text := strings.Join(item.Summary, "\n")
		if strings.TrimSpace(text) == "" {
			text = strings.Join(item.Tail, "\n")
		}
		if strings.TrimSpace(text) != "" {
			turn.Parts = append(turn.Parts, map[string]any{"type": "reasoning", "content": text, "itemId": item.ID})
		}
	case "commandExecution":
		step := map[string]any{
			"id": item.ID, "toolName": "Bash", "status": remoteCommandStepStatus(item.CommandStatus),
			"toolInput": map[string]any{"command": item.Command, "cwd": item.CommandCwd},
			"title":     item.Command,
		}
		if item.AggregatedOutput != nil {
			step["output"] = *item.AggregatedOutput
		}
		if item.ExitCode != nil {
			step["exitCode"] = *item.ExitCode
		}
		if item.CommandDuration != nil {
			step["duration"] = *item.CommandDuration
		}
		turn.Parts = append(turn.Parts, map[string]any{"type": "tool", "step": step, "itemId": item.ID})
	case "fileChange":
		step := map[string]any{"id": item.ID, "toolName": "Patch", "status": remoteCommandStepStatus(item.PatchStatus)}
		changes := make([]map[string]any, 0, len(item.Changes))
		for _, change := range item.Changes {
			mapped := map[string]any{"path": change.Path, "kind": change.changeKind(), "diff": change.Diff}
			if movePath := change.movePath(); movePath != nil {
				mapped["movePath"] = *movePath
			}
			changes = append(changes, mapped)
		}
		step["fileChanges"] = changes
		turn.Parts = append(turn.Parts, map[string]any{"type": "tool", "step": step, "itemId": item.ID})
	case "mcpToolCall":
		step := map[string]any{"id": item.ID, "toolName": "MCP", "status": remoteCommandStepStatus(item.ToolStatus)}
		if item.Server != "" || item.Tool != "" {
			step["title"] = strings.TrimSpace(item.Server + " " + item.Tool)
		}
		if len(item.Arguments) > 0 {
			step["toolInput"] = json.RawMessage(item.Arguments)
		}
		if len(item.Result) > 0 {
			step["output"] = json.RawMessage(item.Result)
		} else if len(item.ToolError) > 0 {
			step["output"] = json.RawMessage(item.ToolError)
		}
		turn.Parts = append(turn.Parts, map[string]any{"type": "tool", "step": step, "itemId": item.ID})
	case "dynamicToolCall":
		step := map[string]any{"id": item.ID, "toolName": item.Tool, "status": remoteCommandStepStatus(item.ToolStatus)}
		if len(item.Arguments) > 0 {
			step["toolInput"] = json.RawMessage(item.Arguments)
		}
		turn.Parts = append(turn.Parts, map[string]any{"type": "tool", "step": step, "itemId": item.ID})
	case "plan":
		if strings.TrimSpace(item.Text) == "" {
			return
		}
		status := "unknown"
		if turn.Status == remoteTurnStatusCompleted {
			status = remoteTurnStatusCompleted
		}
		step := map[string]any{"id": item.ID, "toolName": "Plan", "status": status, "output": item.Text}
		turn.Parts = append(turn.Parts, map[string]any{"type": "tool", "step": step, "itemId": item.ID})
	case "webSearch":
		step := map[string]any{"id": item.ID, "toolName": "WebSearch", "status": remoteTurnStatusCompleted, "title": item.Query}
		turn.Parts = append(turn.Parts, map[string]any{"type": "tool", "step": step, "itemId": item.ID})
	case "contextCompaction":
		turn.SystemNotes = append(turn.SystemNotes, "contextCompaction")
	default:
		if item.Type != "" {
			turn.SkippedTypes = append(turn.SkippedTypes, item.Type)
		}
	}
}

func remoteCommandStepStatus(status string) string {
	if status == "inProgress" {
		return "running"
	}
	return status
}

func (a *Agent) inProgressTurn(ctx context.Context, threadID string) string {
	thread, err := a.readThread(ctx, threadID)
	if err != nil || thread == nil {
		return ""
	}
	for i := len(thread.Turns) - 1; i >= 0; i-- {
		if thread.Turns[i].Status == remoteTurnStatusInProgress && thread.Turns[i].ID != "" {
			return thread.Turns[i].ID
		}
	}
	return ""
}

func (a *Agent) GetTurnScopedRichHistory(ctx context.Context, sessionID string, limit int) ([]core.TurnScopedHistoryTurn, error) {
	thread, err := a.readThread(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return mapRemoteHistoryTurns(thread, limit), nil
}

func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	turns, err := a.GetTurnScopedRichHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.RichHistoryEntry, 0, len(turns)*2)
	for _, turn := range turns {
		started := turn.StartedAt
		if !turn.HasTime {
			started = turn.CompletedAt
		}
		if turn.UserItemID != "" && turn.UserText != "" {
			out = append(out, core.RichHistoryEntry{ID: turn.UserItemID, Role: "user", Content: turn.UserText, Timestamp: started})
		}
		if len(turn.Parts) == 0 && turn.ErrorMessage == "" && len(turn.SystemNotes) == 0 {
			continue
		}
		entry := core.RichHistoryEntry{
			ID: turn.TurnID, Role: "assistant", Parts: turn.Parts, Timestamp: started,
			TurnStartedAt:   remoteTimeOrNil(turn.HasTime, turn.StartedAt),
			TurnCompletedAt: remoteTimeOrNil(turn.HasTime, turn.CompletedAt),
		}
		if turn.ErrorMessage != "" {
			entry.Content = "官方 turn 失败：" + turn.ErrorMessage
		}
		out = append(out, entry)
		for _, note := range turn.SystemNotes {
			out = append(out, core.RichHistoryEntry{ID: turn.TurnID + ":" + note, Role: "system", Content: note, Timestamp: started})
		}
	}
	return out, nil
}

func remoteTimeOrNil(hasTime bool, value time.Time) *time.Time {
	if !hasTime || value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func (a *Agent) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	rich, err := a.GetRichSessionHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.HistoryEntry, 0, len(rich))
	for _, entry := range rich {
		content := entry.Content
		if content == "" {
			for _, part := range entry.Parts {
				if part["type"] != "text" {
					continue
				}
				if text, ok := part["content"].(string); ok {
					content += text
				}
			}
		}
		out = append(out, core.HistoryEntry{Role: entry.Role, Content: content, Timestamp: entry.Timestamp})
	}
	return out, nil
}

// IsSessionActive uses the authoritative thread status. Unknown or failed
// reads are treated as active so hydrate never seals a live user turn.
func (a *Agent) IsSessionActive(ctx context.Context, sessionID string) bool {
	thread, err := a.readThreadWithTurns(ctx, sessionID, false)
	if err != nil || thread == nil {
		return true
	}
	switch thread.Status.Type {
	case remoteThreadStatusActive:
		return true
	case remoteThreadStatusIdle, remoteThreadStatusNotLoaded, remoteThreadStatusSystemError:
		return false
	default:
		return true
	}
}

var (
	_ core.TurnScopedRichHistoryProvider = (*Agent)(nil)
	_ core.RichHistoryProvider           = (*Agent)(nil)
	_ core.HistoryProvider               = (*Agent)(nil)
	_ core.SessionActivityProbing        = (*Agent)(nil)
)
