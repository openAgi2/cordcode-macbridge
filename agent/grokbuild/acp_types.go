package grokbuild

// ACP (Agent Client Protocol) v1 wire types.
// Source: https://agentclientprotocol.com/protocol/v1/schema
//
// These types cover only the subset MacBridge's Grok driver needs:
// initialize, authenticate, session/new, session/load, session/prompt,
// session/cancel, session/update, session/request_permission.
//
// Convention: JSON keys are camelCase; discriminator string values are snake_case.
// The SessionUpdate union uses field "sessionUpdate" as discriminator.
// ContentBlock and ToolCallContent use field "type" as discriminator.
// RequestPermissionOutcome uses field "outcome" as discriminator.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// acpFlag decodes ACP capability flags that may be either a JSON boolean
// (Grok CLI: "loadSession": true) or an empty-object presence marker ({}).
type acpFlag struct {
	Enabled bool
}

func (f *acpFlag) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		f.Enabled = false
		return nil
	}
	switch string(data) {
	case "true":
		f.Enabled = true
		return nil
	case "false":
		f.Enabled = false
		return nil
	}
	// Empty object or any object → capability present/enabled.
	if data[0] == '{' {
		f.Enabled = true
		return nil
	}
	return fmt.Errorf("invalid acp capability flag %q", strings.TrimSpace(string(data)))
}

func (f acpFlag) MarshalJSON() ([]byte, error) {
	if f.Enabled {
		return []byte("true"), nil
	}
	return []byte("false"), nil
}

// --- JSON-RPC 2.0 envelope ---

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// --- initialize ---

type initializeParams struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities *clientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *clientInfo         `json:"clientInfo,omitempty"`
}

type clientCapabilities struct {
	Session *sessionClientCaps `json:"session,omitempty"`
}

type sessionClientCaps struct {
	// ConfigOptions with an empty object means "supported".
	ConfigOptions *map[string]any `json:"configOptions,omitempty"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type initializeResult struct {
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities *agentCapabilities `json:"agentCapabilities,omitempty"`
	AgentInfo         *clientInfo        `json:"agentInfo,omitempty"`
	AuthMethods       []authMethod       `json:"authMethods,omitempty"`
}

type agentCapabilities struct {
	LoadSession         acpFlag              `json:"loadSession,omitempty"`
	SessionCapabilities *sessionCapabilities `json:"sessionCapabilities,omitempty"`
}

type sessionCapabilities struct {
	List   acpFlag `json:"list,omitempty"`
	Resume acpFlag `json:"resume,omitempty"`
	Close  acpFlag `json:"close,omitempty"`
	Delete acpFlag `json:"delete,omitempty"`
}

type authMethod struct {
	Type        string `json:"type,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// --- authenticate ---

type authenticateParams struct {
	MethodID string `json:"methodId"`
}

// authenticateResult is empty apart from optional _meta.

// --- session/new ---

type sessionNewParams struct {
	CWD                   string   `json:"cwd"`
	McpServers            []any    `json:"mcpServers"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

type sessionNewResult struct {
	SessionID string `json:"sessionId"`
}

// --- session/load ---

// Grok CLI 0.2.93 requires sessionId + cwd + mcpServers (same shape as session/new
// plus the id). Omitting either cwd or mcpServers returns -32602 Invalid params.
type sessionLoadParams struct {
	SessionID  string `json:"sessionId"`
	CWD        string `json:"cwd"`
	McpServers []any  `json:"mcpServers"`
}

// sessionLoadResult mirrors sessionNewResult.

// --- session/prompt ---

type sessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

// --- session/cancel (notification) ---

type sessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

// --- session/update (notification) ---

type sessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

// sessionUpdate is a tagged union on field "sessionUpdate".
// We decode the discriminator first, then re-parse the relevant fields.
type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Raw           json.RawMessage `json:"-"` // full JSON for re-parsing
}

// sessionUpdatePayload holds all possible fields across variants.
// Only fields relevant to the active variant are populated.
type sessionUpdatePayload struct {
	SessionUpdate string `json:"sessionUpdate"`
	MessageID     string `json:"messageId,omitempty"`
	// Content 保持 raw: grok 的 agent_*_chunk 用单个 text object, 但 tool_call_update
	// 记录的 content 是数组 (实测 929 条 tool_call_update 里约一半是数组形状,
	// 见 contentText())。用 *contentBlock 会让整个 outer unmarshal 失败 → EventError →
	// relay loop 误判终态 → idle/running 振荡。raw + contentText() 同时兼容两种形状。
	Content    json.RawMessage    `json:"content,omitempty"`
	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      string             `json:"title,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Status     string             `json:"status,omitempty"`
	Locations  []toolCallLocation `json:"locations,omitempty"`
	Entries    []planEntry        `json:"entries,omitempty"`
	Used       *int               `json:"used,omitempty"`
	Size       *int               `json:"size,omitempty"`
	// turn_completed 终态字段。上游在不同版本里对 prompt_id 的 JSON key 不一致
	// (真实 updates.jsonl: "prompt_id" 440 次, "promptId" 289 次), 两个字段都接收,
	// 取非空者作 durable turn 关联键。stop_reason 区分正常结束 / 取消 / 限流 / 错误。
	PromptID    string `json:"promptId,omitempty"`
	PromptIDRaw string `json:"prompt_id,omitempty"` // snake_case 兜底 (旧上游版本)
	StopReason  string `json:"stop_reason,omitempty"`
}

// resolvedPromptID returns the durable turn correlation key from a turn_completed
// payload, tolerating upstream's inconsistent key casing ("promptId" vs "prompt_id").
func (p sessionUpdatePayload) resolvedPromptID() string {
	if p.PromptID != "" {
		return p.PromptID
	}
	return p.PromptIDRaw
}

// hasContent reports whether the raw content field is present and non-null.
// agent_message_chunk / agent_thought_chunk use it to decide whether to emit a chunk.
func (p sessionUpdatePayload) hasContent() bool {
	s := strings.TrimSpace(string(p.Content))
	return s != "" && s != "null"
}

// contentText extracts concatenated text from the raw content field, tolerating both
// single-object and array shapes.
//
//	grok agent_*_chunk:  content: {"type":"text","text":"..."}
//	grok tool_call_update: content: [{"type":"content","content":{"type":"text","text":"..."}}]
//
// Returns "" when no text is found. Never errors — a malformed block is skipped so the
// surrounding chunk still decodes (avoids the prior whole-message EventError failure).
func (p sessionUpdatePayload) contentText() string {
	s := strings.TrimSpace(string(p.Content))
	if s == "" || s == "null" {
		return ""
	}
	if s[0] == '[' {
		var blocks []json.RawMessage
		if err := json.Unmarshal(p.Content, &blocks); err != nil {
			return ""
		}
		var b strings.Builder
		for _, raw := range blocks {
			b.WriteString(extractContentText(raw))
		}
		return b.String()
	}
	return extractContentText(p.Content)
}

// extractContentText parses one content block, tolerating both the direct text shape
// {type:"text", text:"..."} and the nested {type:"content", content:{type:"text",...}}
// wrapper that grok uses inside tool_call_update content arrays.
func extractContentText(raw json.RawMessage) string {
	var probe struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	if probe.Text != "" {
		return probe.Text
	}
	if len(probe.Content) > 0 {
		var inner struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(probe.Content, &inner); err == nil {
			return inner.Text
		}
	}
	return ""
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`
}

type toolCallLocation struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

type planEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

// --- session/request_permission (agent → client request) ---

type requestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  permissionToolCall `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type permissionToolCall struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once, reject_always
}

// requestPermissionResult is the response we send back.
type requestPermissionResult struct {
	Outcome outcomePayload `json:"outcome"`
}

// outcomePayload is a tagged union on field "outcome".
type outcomePayload struct {
	Outcome  string `json:"outcome"` // "selected" or "cancelled"
	OptionID string `json:"optionId,omitempty"`
}

// --- session/list ---

type sessionListResult struct {
	Sessions []acpSessionInfo `json:"sessions"`
}

type acpSessionInfo struct {
	SessionID    string `json:"sessionId"`
	CWD          string `json:"cwd,omitempty"`
	Title        string `json:"title,omitempty"`
	LastActivity string `json:"lastActivity,omitempty"`
}
