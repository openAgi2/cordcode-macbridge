package dsh

// DSH SDK JSON-RPC 2.0 wire types (newline-delimited stdio).
//
// Wire surface (design §3.0, pinned packages/sdk/protocol/src/types.ts:50-104):
// 3 client→server requests (initialize / session/prompt / shutdown) and 4
// server→client notifications (session.event / session.status /
// subagent.started / subagent.finished). Evidence: scripts/dsh-gate0/dumps/.

import "encoding/json"

// jsonrpcFrame is one newline-delimited JSON-RPC 2.0 frame. A frame with ID and
// no Method is a response; ID+Method is a request; Method without ID is a
// notification.
type jsonrpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// sessionEventParams is the session.event notification payload. sessionId
// covers every session in the runtime (not only the SDK root session), so the
// driver must route by scope before decoding into the root codec (§3.8).
type sessionEventParams struct {
	SessionID string   `json:"sessionId"`
	Event     dshEvent `json:"event"`
}

// sessionStatusParams is the session.status notification payload. Root-scope
// status is a driver-internal liveness signal only — it never becomes a
// core.Event and never substitutes turn/end as the turn completion truth (§3.4).
type sessionStatusParams struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"` // "idle" | "running"
}

// subagentStartedParams is the subagent.started notification payload. The
// child's events are explicitly filtered out of the root codec; the edge is
// recorded in the lineage tombstone and never removed on finished (§3.8).
type subagentStartedParams struct {
	ParentSessionID string `json:"parentSessionId"`
	ChildSessionID  string `json:"childSessionId"`
}

// subagentFinishedParams is the subagent.finished notification payload.
// lastAssistantMessage may be absent; the driver keeps the tombstone edge.
type subagentFinishedParams struct {
	Provider        string `json:"provider"`
	AgentID         string `json:"agentId"`
	ParentSessionID string `json:"parentSessionId"`
	ChildSessionID  string `json:"childSessionId"`
	Status          string `json:"status"`      // "ok" | "error"
	StopReason      string `json:"stopReason"`  // provider-reported
}

// dshEvent is the session-log event envelope. seq sits at the envelope top
// level (sibling of type/data), never inside data (987/987 dump evidence,
// §3.10.1). Ignorable is kept raw: only the exact literal `true` is the
// writer's safe-skip marker; `false`, other JSON types, and absence all mean
// REQUIRED (§3.10.2).
type dshEvent struct {
	Type      string          `json:"type"`
	Seq       int             `json:"seq"`
	Time      int64           `json:"time"`
	Ignorable json.RawMessage `json:"ignorable,omitempty"`
	Data      json.RawMessage `json:"data"`
}

// ignorableMarker reports whether the envelope carries the explicit
// `ignorable: true` marker — the only safe-skip channel (§3.10.2 class ④).
func (e *dshEvent) ignorableMarker() bool {
	if len(e.Ignorable) == 0 {
		return false
	}
	var b bool
	if err := json.Unmarshal(e.Ignorable, &b); err != nil {
		return false
	}
	return b
}

// dshUsage is the token usage snapshot carried by assistant/chunk(usage) and
// assistant/message. inputTokens does NOT include cache hits; the context
// pressure projection is inputTokens + cacheReadTokens (§3.7).
type dshUsage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	ReasoningTokens  int `json:"reasoningTokens"`
}

// wire request payloads

type initializeParams struct {
	CWD      string `json:"cwd"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type initializeResult struct {
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// dshServerInfoName is the wire-stable runtime identity returned by
// initialize (§3.1-3.2). A different name means we are not talking to the
// pinned protocol and must fail closed.
const dshServerInfoName = "deepseek-harness-sdk-runtime"

type sessionPromptParams struct {
	SessionID     string           `json:"sessionId"`
	ContentBlocks []promptContent  `json:"contentBlocks"`
}

type promptContent struct {
	Type string `json:"type"` // "text" only in phase 1 (§3.9 text-only)
	Text string `json:"text"`
}

type sessionPromptResult struct {
	MessageID string `json:"messageId"`
}
