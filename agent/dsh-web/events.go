package dshweb

// SessionEvent data payload types — COPIED from agent/dsh (design §4.1/M3:
// "复用 codec = 复制映射表进新包，不 import"; the mux session/event frame's
// event field is the same strict-envelope + wide-data shape as the disk log,
// so the data payload types are identical). Source: agent/dsh/{events.go,
// store.go} at dsh/driver round12.

import "encoding/json"

// dshSource is the shared source discriminant (user/plugin/model/tool kinds).
type dshSource struct {
	Kind   string `json:"kind"`
	Plugin string `json:"plugin,omitempty"`
	CallID string `json:"callId,omitempty"`
}

// dshModelSource extends the source discriminant with the model attribution
// assistant messages carry.
type dshModelSource struct {
	dshSource
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// dshContentBlock is one message content block; text/reasoning/tool-call/
// tool-result shapes share this envelope (nested content belongs to
// tool-result blocks).
type dshContentBlock struct {
	Type       string            `json:"type"`
	Text       string            `json:"text,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Arguments  json.RawMessage   `json:"arguments,omitempty"`
	ToolCallID string            `json:"toolCallId,omitempty"`
	Content    []dshContentBlock `json:"content,omitempty"`
}

// dshUserMessageData is user/message's data payload.
type dshUserMessageData struct {
	Content []dshContentBlock `json:"content"`
	Source  *dshSource        `json:"source,omitempty"`
	Role    string            `json:"role,omitempty"`
	ID      string            `json:"id,omitempty"`
}

// dshUsage is the token usage snapshot carried by assistant/chunk(usage) and
// assistant/message. inputTokens does NOT include cache hits.
type dshUsage struct {
	InputTokens     int `json:"inputTokens"`
	OutputTokens    int `json:"outputTokens"`
	CacheReadTokens int `json:"cacheReadTokens"`
	ReasoningTokens int `json:"reasoningTokens"`
}

// dshTitleData is session/title's data payload.
type dshTitleData struct {
	Title  string     `json:"title"`
	Source *dshSource `json:"source,omitempty"`
}
