package grokbuild

// ACP codec: JSON-RPC encoding/decoding and SessionUpdate → core.Event conversion.

import (
	"encoding/json"
	"fmt"
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
		ID      *json.RawMessage `json:"id,omitempty"`
		Method  *string          `json:"method,omitempty"`
		Result  *json.RawMessage `json:"result,omitempty"`
		Error   *jsonrpcError    `json:"error,omitempty"`
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
// {"sessionId":"...", "update": {"sessionUpdate":"agent_message_chunk", "content":{...}}}
func convertSessionUpdate(params json.RawMessage, sessionID string) []core.Event {
	// First parse the outer wrapper to get the "update" field.
	var outer struct {
		Update sessionUpdatePayload `json:"update"`
	}
	if err := json.Unmarshal(params, &outer); err != nil {
		return []core.Event{{
			Type:    core.EventError,
			Content: fmt.Sprintf("failed to decode session/update: %v", err),
		}}
	}

	p := outer.Update

	switch p.SessionUpdate {
	case "agent_message_chunk":
		if p.Content != nil {
			return []core.Event{{
				Type:    core.EventText,
				Content: p.Content.Text,
			}}
		}
		return nil

	case "agent_thought_chunk":
		if p.Content != nil {
			return []core.Event{{
				Type:    core.EventThinking,
				Content: p.Content.Text,
			}}
		}
		return nil

	case "user_message_chunk":
		// User message echo — not forwarded to iOS.
		return nil

	case "tool_call":
		ev := core.Event{
			Type:     core.EventToolUse,
			ToolName: p.Title,
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

	default:
		// Unknown update type — expose as a diagnostic error, don't crash.
		return []core.Event{{
			Type:    core.EventError,
			Content: fmt.Sprintf("unknown sessionUpdate type: %q", p.SessionUpdate),
		}}
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
