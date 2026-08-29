package codexremote

// codec.go decodes the ordinary app-server notifications that arrive inside a
// Remote Control server_message envelope. The protocol identity remains the
// official (threadId, turnId, itemId) tuple; Remote envelope ids are transport
// metadata and never become projection ids. Unknown methods are counted and
// dropped, while known notifications with no bridge event are consumed
// silently so an event backlog cannot grow.

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type LiveCodec struct {
	mu            sync.Mutex
	turnByThread  map[string]string
	retryByThread map[string]int
	unknown       map[string]int
}

func NewLiveCodec() *LiveCodec {
	return &LiveCodec{
		turnByThread:  map[string]string{},
		retryByThread: map[string]int{},
		unknown:       map[string]int{},
	}
}

func (c *LiveCodec) ActiveTurn(threadID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnByThread[threadID]
}

func (c *LiveCodec) setActiveTurn(threadID, turnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if turnID == "" {
		delete(c.turnByThread, threadID)
		return
	}
	c.turnByThread[threadID] = turnID
}

func (c *LiveCodec) UnknownMethods() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.unknown))
	for method, count := range c.unknown {
		out[method] = count
	}
	return out
}

func (c *LiveCodec) Decode(n Notification) []core.Event {
	switch n.Method {
	case "turn/started":
		return c.decodeTurnStarted(n)
	case "turn/completed":
		return c.decodeTurnCompleted(n)
	case "item/agentMessage/delta":
		c.resetRetry(n)
		return decodeRemoteAgentMessageDelta(n)
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		c.resetRetry(n)
		return decodeRemoteReasoningDelta(n)
	case "item/started":
		return c.decodeItemStarted(n)
	case "item/completed":
		return c.decodeItemCompleted(n)
	case "thread/tokenUsage/updated":
		return decodeRemoteTokenUsage(n)
	case "turn/plan/updated":
		return decodeRemotePlanUpdated(n)
	case "error":
		return c.decodeErrorNotification(n)
	case "warning", "thread/status/changed", "thread/started", "thread/name/updated",
		"thread/archived", "thread/deleted", "account/rateLimits/updated",
		"remoteControl/status/changed", "serverRequest/resolved", "thread/goal/cleared",
		"turn/diff/updated":
		// These are official notifications whose state is either fetched through
		// catalog/history or has no core.Event representation yet.
		return nil
	default:
		c.mu.Lock()
		c.unknown[n.Method]++
		c.mu.Unlock()
		return nil
	}
}

func (c *LiveCodec) resetRetry(n Notification) {
	var params struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(n.Params, &params) == nil && params.ThreadID != "" {
		c.mu.Lock()
		delete(c.retryByThread, params.ThreadID)
		c.mu.Unlock()
	}
}

func (c *LiveCodec) decodeTurnStarted(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" || params.Turn.ID == "" {
		return nil
	}
	c.mu.Lock()
	c.turnByThread[params.ThreadID] = params.Turn.ID
	delete(c.retryByThread, params.ThreadID)
	c.mu.Unlock()
	return []core.Event{{Type: core.EventTurnStarted, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.Turn.ID}}
}

func (c *LiveCodec) decodeTurnCompleted(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string           `json:"id"`
			Status string           `json:"status"`
			Error  *remoteTurnError `json:"error"`
		} `json:"turn"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" {
		return nil
	}
	if params.Turn.ID == "" {
		// Turn.id is required by the official TurnCompletedNotification. Never
		// infer it from a local active-turn map after a malformed wire frame.
		slog.Warn("codex-remote codec: turn/completed missing turn.id, dropping", "thread", params.ThreadID)
		return nil
	}
	c.mu.Lock()
	delete(c.turnByThread, params.ThreadID)
	delete(c.retryByThread, params.ThreadID)
	c.mu.Unlock()
	event := core.Event{SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.Turn.ID, Done: true}
	if params.Turn.Status == remoteTurnStatusFailed {
		message := "turn failed"
		if params.Turn.Error != nil && params.Turn.Error.Message != "" {
			message = params.Turn.Error.Message
		}
		event.Type = core.EventError
		event.Error = &remoteOfficialError{message: message}
		return []core.Event{event}
	}
	event.Type = core.EventResult
	return []core.Event{event}
}

type remoteOfficialError struct{ message string }

func (e *remoteOfficialError) Error() string { return e.message }

func decodeRemoteAgentMessageDelta(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.Delta == "" {
		return nil
	}
	return []core.Event{{Type: core.EventText, Content: params.Delta, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID}}
}

func decodeRemoteReasoningDelta(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.Delta == "" {
		return nil
	}
	return []core.Event{{Type: core.EventThinking, Content: params.Delta, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID}}
}

type remoteItemNotification struct {
	Item     json.RawMessage `json:"item"`
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
}

func (c *LiveCodec) decodeItemStarted(n Notification) []core.Event {
	var params remoteItemNotification
	if json.Unmarshal(n.Params, &params) != nil {
		return nil
	}
	item := decodeRemoteThreadItem(params.Item)
	if params.ThreadID == "" || params.TurnID == "" || item.ID == "" {
		return nil
	}
	switch item.Type {
	case "userMessage":
		return []core.Event{{Type: core.EventUserMessage, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, Content: item.userText()}}
	case "contextCompaction":
		return []core.Event{{Type: core.EventContextCompressing, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID}}
	case "commandExecution":
		return []core.Event{remoteToolUseEvent(params, item, "Bash", item.Command)}
	case "fileChange":
		event := remoteToolUseEvent(params, item, "Patch", remoteJSONField(item.Raw, "changes"))
		event.FileChanges = remoteFileChanges(item)
		return []core.Event{event}
	case "mcpToolCall":
		title := strings.TrimSpace(item.Server + ":" + item.Tool)
		return []core.Event{remoteToolUseEvent(params, item, "MCP", title+"\n"+string(remoteOrEmpty(item.Arguments)))}
	case "webSearch":
		return []core.Event{remoteToolUseEvent(params, item, "WebSearch", item.Query)}
	case "dynamicToolCall":
		return []core.Event{remoteToolUseEvent(params, item, item.Tool, string(remoteOrEmpty(item.Arguments)))}
	default:
		return nil
	}
}

func (c *LiveCodec) decodeItemCompleted(n Notification) []core.Event {
	var params remoteItemNotification
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" || params.TurnID == "" {
		return nil
	}
	item := decodeRemoteThreadItem(params.Item)
	if item.ID == "" {
		return nil
	}
	switch item.Type {
	case "commandExecution":
		event := core.Event{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "Bash", RequestID: item.ID, ToolStatus: item.CommandStatus}
		if item.AggregatedOutput != nil {
			event.ToolResult = *item.AggregatedOutput
		}
		if item.ExitCode != nil {
			code := int(*item.ExitCode)
			event.ToolExitCode = &code
			success := code == 0
			event.ToolSuccess = &success
		}
		return []core.Event{event}
	case "fileChange":
		return []core.Event{{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "Patch", RequestID: item.ID, ToolStatus: item.PatchStatus, ToolResult: remoteJSONField(item.Raw, "changes"), FileChanges: remoteFileChanges(item)}}
	case "mcpToolCall":
		event := core.Event{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "MCP", RequestID: item.ID, ToolStatus: item.ToolStatus}
		if len(item.Result) > 0 {
			event.ToolResult = string(item.Result)
		} else if len(item.ToolError) > 0 {
			event.ToolResult = string(item.ToolError)
		}
		return []core.Event{event}
	case "contextCompaction":
		return []core.Event{{Type: core.EventContextCompressed, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID}}
	case "dynamicToolCall":
		return []core.Event{{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: item.Tool, RequestID: item.ID, ToolStatus: item.ToolStatus, ToolResult: string(remoteOrEmpty(item.Arguments))}}
	case "webSearch":
		return []core.Event{{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "WebSearch", RequestID: item.ID}}
	default:
		// userMessage, agentMessage and reasoning are represented by item/started
		// or their delta notifications; completed snapshots must not duplicate.
		return nil
	}
}

func remoteToolUseEvent(params remoteItemNotification, item remoteThreadItem, name, input string) core.Event {
	return core.Event{Type: core.EventToolUse, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: name, ToolInput: input, RequestID: item.ID}
}

func remoteFileChanges(item remoteThreadItem) []core.FileChange {
	changes := make([]core.FileChange, 0, len(item.Changes))
	for _, change := range item.Changes {
		mapped := core.FileChange{Path: change.Path, Kind: change.changeKind(), Diff: change.Diff}
		if movePath := change.movePath(); movePath != nil {
			mapped.MovePath = *movePath
		}
		changes = append(changes, mapped)
	}
	return changes
}

func decodeRemoteTokenUsage(n Notification) []core.Event {
	var params struct {
		ThreadID   string `json:"threadId"`
		TokenUsage struct {
			Total struct {
				TotalTokens           int `json:"totalTokens"`
				InputTokens           int `json:"inputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				OutputTokens          int `json:"outputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
			} `json:"total"`
			Last struct {
				TotalTokens           int `json:"totalTokens"`
				InputTokens           int `json:"inputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				OutputTokens          int `json:"outputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
			} `json:"last"`
			ModelContextWindow int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" {
		return nil
	}
	usage := &core.ContextUsage{
		UsedTokens: params.TokenUsage.Last.TotalTokens, TotalTokens: params.TokenUsage.Total.TotalTokens,
		InputTokens: params.TokenUsage.Last.InputTokens, CachedInputTokens: params.TokenUsage.Last.CachedInputTokens,
		OutputTokens: params.TokenUsage.Last.OutputTokens, ReasoningOutputTokens: params.TokenUsage.Last.ReasoningOutputTokens,
		ContextWindow: params.TokenUsage.ModelContextWindow,
	}
	return []core.Event{{Type: core.EventContextUsageUpdated, SessionID: params.ThreadID, ThreadID: params.ThreadID, ContextUsage: usage}}
}

func (c *LiveCodec) decodeErrorNotification(n Notification) []core.Event {
	var params struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool   `json:"willRetry"`
		ThreadID  string `json:"threadId"`
		TurnID    string `json:"turnId"`
	}
	if json.Unmarshal(n.Params, &params) != nil || strings.TrimSpace(params.Error.Message) == "" {
		return nil
	}
	if params.WillRetry {
		c.mu.Lock()
		c.retryByThread[params.ThreadID]++
		attempt := c.retryByThread[params.ThreadID]
		c.mu.Unlock()
		return []core.Event{{Type: core.EventRetryStatus, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, RetryAttempt: attempt, Content: params.Error.Message}}
	}
	return []core.Event{{Type: core.EventError, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, Error: &remoteOfficialError{message: params.Error.Message}}}
}

func decodeRemotePlanUpdated(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Plan     []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" {
		return nil
	}
	todos := make([]core.Todo, 0, len(params.Plan))
	for _, entry := range params.Plan {
		if strings.TrimSpace(entry.Step) == "" {
			continue
		}
		status := entry.Status
		if status == "inProgress" {
			status = "in_progress"
		}
		todos = append(todos, core.Todo{Content: entry.Step, Status: status, Priority: "normal"})
	}
	if len(todos) == 0 {
		return nil
	}
	return []core.Event{{Type: core.EventPlan, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, Plan: todos}}
}

func remoteJSONField(raw json.RawMessage, key string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	return string(remoteOrEmpty(object[key]))
}

func remoteOrEmpty(raw []byte) []byte {
	if raw == nil {
		return []byte{}
	}
	return raw
}
