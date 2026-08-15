package grokbuild

// ACP codec: JSON-RPC encoding/decoding and SessionUpdate → core.Event conversion.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// nextRequestID generates sequential JSON-RPC request IDs starting from 1.
type requestIDCounter struct {
	n int
}

func (c *requestIDCounter) next() int {
	c.n++
	return c.n
}

func (c *requestIDCounter) encode() json.RawMessage {
	b, _ := json.Marshal(c.n)
	return b
}

// encodeRequest builds a JSON-RPC 2.0 request string (newline-delimited).
func encodeRequest(id int, method string, params any) ([]byte, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode params for %s: %w", method, err)
	}
	req := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsJSON,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request %s: %w", method, err)
	}
	return append(b, '\n'), nil
}

// encodeNotification builds a JSON-RPC 2.0 notification (no ID).
func encodeNotification(method string, params any) ([]byte, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode params for %s: %w", method, err)
	}
	req := struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode notification %s: %w", method, err)
	}
	return append(b, '\n'), nil
}

// encodeResponse builds a JSON-RPC 2.0 response to a request from the agent.
func encodeResponse(id json.RawMessage, result any) ([]byte, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode response result: %w", err)
	}
	resp := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resultJSON,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return append(b, '\n'), nil
}

// decodeMessage parses a single JSON-RPC line and routes it.
// Returns one of: *jsonrpcResponse (agent replied to our request),
// *agentRequest (agent sent us a request), *agentNotification (agent sent a notification).
type agentRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type agentNotification struct {
	Method string
	Params json.RawMessage
}

func decodeMessage(line []byte) (*jsonrpcResponse, *agentRequest, *agentNotification, error) {
	// Try response first (has "result" or "error" and "id" but no "method").
	var probe struct {
		ID     *json.RawMessage `json:"id,omitempty"`
		Method *string          `json:"method,omitempty"`
		Result *json.RawMessage `json:"result,omitempty"`
		Error  *jsonrpcError    `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, nil, nil, fmt.Errorf("decode json-rpc line: %w", err)
	}

	// Response: has id, no method, has result or error.
	if probe.ID != nil && probe.Method == nil {
		resp := &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      *probe.ID,
			Result:  derefRaw(probe.Result),
			Error:   probe.Error,
		}
		return resp, nil, nil, nil
	}

	// Request or notification: has method.
	if probe.Method != nil {
		method := *probe.Method
		if probe.ID != nil {
			return nil, &agentRequest{
				ID:     *probe.ID,
				Method: method,
				Params: extractParams(line),
			}, nil, nil
		}
		return nil, nil, &agentNotification{
			Method: method,
			Params: extractParams(line),
		}, nil
	}

	return nil, nil, nil, fmt.Errorf("unrecognized json-rpc message: %s", string(line))
}

func derefRaw(r *json.RawMessage) json.RawMessage {
	if r == nil {
		return nil
	}
	return *r
}

func extractParams(line []byte) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil
	}
	if p, ok := m["params"]; ok {
		return p
	}
	return nil
}

// --- SessionUpdate → core.Event conversion ---

// convertSessionUpdate converts an ACP session/update notification params to one or more core.Events.
// params is the raw "params" field of the session/update notification:
// {"sessionId":"...", "update": {"sessionUpdate":"agent_message_chunk", "content":{...}}, "_meta":{...}}
func convertSessionUpdate(params json.RawMessage, sessionID string) []core.Event {
	// First parse the outer wrapper to get the "update" field and the top-level _meta.
	var outer struct {
		Update sessionUpdatePayload `json:"update"`
		Meta   struct {
			PromptID    string `json:"promptId,omitempty"`
			PromptIDRaw string `json:"prompt_id,omitempty"`
		} `json:"_meta,omitempty"`
	}
	if err := json.Unmarshal(params, &outer); err != nil {
		return []core.Event{{
			Type:    core.EventError,
			Content: fmt.Sprintf("failed to decode session/update: %v", err),
		}}
	}

	p := outer.Update

	// grok 流式 chunk 的稳定 turn/item 关联键 = params._meta.promptId, 与 turn_completed 的
	// prompt_id 实测同值 (例如 "73494e8f-...")。SSV2 projection reducer 要求 text_delta /
	// reasoning_delta / tool 携带 source-proven itemId/turnId, 否则 identityless 事件被直接 skip
	// (projection_reducer.go:465/537/568/598); 同时 iOS 的 syncV2 连接 raw timeline 被 seal
	// (projection_delivery.go), 只有 projection patch 能到 iOS。因此必须把 promptId 透传成
	// ItemID/TurnID, reducer 才会接受并 flush patch。tool_call 的 toolCallId 是 per-tool 稳定 id,
	// 独立作 RequestID (tool_started/tool_finished 的 itemId)。
	promptID := outer.Meta.PromptID
	if promptID == "" {
		promptID = outer.Meta.PromptIDRaw
	}

	switch p.SessionUpdate {
	case "agent_message_chunk":
		if !p.hasContent() {
			return nil
		}
		return []core.Event{{
			Type:    core.EventText,
			Content: p.contentText(),
			ItemID:  promptID,
			TurnID:  promptID,
		}}

	case "agent_thought_chunk":
		if !p.hasContent() {
			return nil
		}
		return []core.Event{{
			Type:    core.EventThinking,
			Content: p.contentText(),
			ItemID:  promptID,
			TurnID:  promptID,
		}}

	case "user_message_chunk":
		// 外部 turn 的用户 prompt 回显 (真实 updates.jsonl: 只带 promptIndex, 不带
		// promptId)。必须转成 EventUserMessage 交给 relay loop 缓冲, 否则 iOS 只能
		// 看到回复看不到 prompt。turn 身份 (promptId) 由同 turn 首个内容事件到达时
		// 补齐 (见 grokLeaderSessionRelayLoop), 这里不合成任何身份。
		if !p.hasContent() {
			return nil
		}
		text := strings.TrimSpace(p.contentText())
		if text == "" {
			return nil
		}
		return []core.Event{{
			Type:    core.EventUserMessage,
			Content: text,
		}}

	case "tool_call":
		ev := core.Event{
			Type:      core.EventToolUse,
			ToolName:  p.Title,
			TurnID:    promptID,
			RequestID: p.ToolCallID,
		}
		if p.Status == "completed" {
			success := true
			ev.Type = core.EventToolResult
			ev.ToolSuccess = &success
			ev.ToolStatus = "completed"
		} else if p.Status == "failed" {
			success := false
			ev.Type = core.EventToolResult
			ev.ToolSuccess = &success
			ev.ToolStatus = "failed"
		} else {
			ev.ToolStatus = p.Status
		}
		return []core.Event{ev}

	case "tool_call_update":
		// Status update for an existing tool call.
		if p.Status == "completed" || p.Status == "failed" {
			success := p.Status == "completed"
			return []core.Event{{
				Type:        core.EventToolResult,
				ToolName:    p.Title,
				ToolStatus:  p.Status,
				ToolSuccess: &success,
				RequestID:   p.ToolCallID,
				TurnID:      promptID,
			}}
		}
		return nil

	case "plan":
		todos := make([]core.Todo, 0, len(p.Entries))
		for _, e := range p.Entries {
			todos = append(todos, core.Todo{
				Content:  e.Content,
				Status:   e.Status,
				Priority: e.Priority,
			})
		}
		return []core.Event{{
			Type: core.EventPlan,
			Plan: todos,
		}}

	case "usage_update":
		if p.Used != nil && p.Size != nil {
			return []core.Event{{
				Type: core.EventContextUsageUpdated,
				ContextUsage: &core.ContextUsage{
					UsedTokens:    *p.Used,
					ContextWindow: *p.Size,
					TotalTokens:   *p.Used,
				},
			}}
		}
		return nil

	case "session_info_update", "current_mode_update",
		"available_commands_update", "config_option_update":
		// Internal state updates — not forwarded as events.
		return nil

	case "turn_completed":
		// 上游 durable 终态信号 (x.ai/session_notification, 无 isReplay → 进 leader live rail)。
		// prompt_id 是跨重连的 turn 关联键 (resolvedPromptID 兼容 promptId/prompt_id 两种 key);
		// stop_reason 区分正常结束与异常。映射成 EventResult{Done:true}, mapAgentEvent 会把它转成
		// wire turn_completed, grokLeaderSessionRelayLoop 的 markIdle 分支据此收口。
		promptID := p.resolvedPromptID()
		switch p.StopReason {
		case "error":
			// 上游报错 → 转 wire error (终态), iOS 显示真实失败而非假完成。
			return []core.Event{{
				Type:    core.EventError,
				Content: "grok turn error",
				Done:    true,
				TurnID:  promptID,
			}}
		case "cancelled":
			return []core.Event{{
				Type:    core.EventResult,
				Done:    true,
				TurnID:  promptID,
				Content: "cancelled",
			}}
		default:
			// end_turn / rate_limit / 空 → 正常完成。
			return []core.Event{{
				Type:   core.EventResult,
				Done:   true,
				TurnID: promptID,
			}}
		}

	default:
		// Unknown update type — log for diagnostics but do NOT emit an error
		// event. Treating unknown notifications as terminal errors would abort
		// turns whenever Grok emits an extension type we haven't mapped yet.
		slog.Debug("grokbuild: unmapped sessionUpdate type",
			"sessionUpdate", p.SessionUpdate)
		return nil
	}
}

// selectPermissionOption picks the optionId that matches the user's allow/deny decision.
func selectPermissionOption(options []permissionOption, behavior string) (string, bool) {
	var wantPrefix string
	if behavior == "allow" {
		wantPrefix = "allow"
	} else {
		wantPrefix = "reject"
	}
	for _, opt := range options {
		if strings.HasPrefix(opt.Kind, wantPrefix) {
			return opt.OptionID, true
		}
	}
	return "", false
}
