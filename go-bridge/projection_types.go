package gobridge

// Session Projection Stream (session_sync_v2) wire types.
//
// These MUST match docs/protocol/schema/bridge-v1.types.ts「Session Projection Stream」.
// Field names use lowerCamelCase JSON tags identical to the TypeScript shapes so a Mac-emitted
// patch/snapshot decodes directly into the iOS Swift mirror (Phase 2 WP7). Non-breaking,
// additive over bridge-v1; see bridge-v1_schema.go BridgeProtocolSchemaRevision.

// ProjectionPart is one part of a message projection. Type mirrors the Mapping Notes part
// vocabulary (text | reasoning | tool | file). Only the fields relevant to a type are set.
type ProjectionPart struct {
	Type string `json:"type"` // text | reasoning | tool | file

	// text / reasoning
	Text string `json:"text,omitempty"`

	// tool
	ItemID     string      `json:"itemId,omitempty"` // rollout call_id (authoritative tool identity)
	ToolName   string      `json:"toolName,omitempty"`
	ToolInput  interface{} `json:"toolInput,omitempty"`
	ToolResult interface{} `json:"toolResult,omitempty"`
	ToolStatus string      `json:"toolStatus,omitempty"`
	Matches    interface{} `json:"matches,omitempty"`

	// file
	Path     string `json:"path,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Diff     string `json:"diff,omitempty"`
	MovePath string `json:"movePath,omitempty"`
}

// MessageProjection is one user/assistant message within a turn.
type MessageProjection struct {
	ID       string           `json:"id"` // authoritative source id (see bridge-v1.md SPS)
	ClientID string           `json:"clientId,omitempty"`
	Role     string           `json:"role"` // user | assistant
	Parts    []ProjectionPart `json:"parts"`
}

// TurnProjection is one turn's projection. TurnID is the Codex rollout lifecycle turn_id.
type TurnProjection struct {
	TurnID      string             `json:"turnId"`
	Status      string             `json:"status"` // pending | running | completed | aborted | error
	StartedAt   int64              `json:"startedAt,omitempty"`
	CompletedAt int64              `json:"completedAt,omitempty"`
	User        *MessageProjection `json:"user,omitempty"`
	Assistant   *MessageProjection `json:"assistant,omitempty"`
}

// ExecutionView is the session-level execution state. isExecuting = phase ∈ {running, requires_action}.
type ExecutionView struct {
	Phase        string `json:"phase"` // idle | running | requires_action
	ActiveTurnID string `json:"activeTurnId,omitempty"`
}

// SessionProjection is the authoritative per-(backendId,sessionId) projection. syncRev ≡
// EventPublisher.perSessionSeq. Push and pull read the same in-memory instance (design §6.4).
type SessionProjection struct {
	SessionID   string           `json:"sessionId"`
	SyncRev     int              `json:"syncRev"`
	BridgeEpoch string           `json:"bridgeEpoch,omitempty"`
	UpdatedAt   int64            `json:"updatedAt,omitempty"`
	Execution   ExecutionView    `json:"execution"`
	Turns       []TurnProjection `json:"turns"`
}

// PartOp is one incremental part operation targeting a specific (turnId, messageId).
type PartOp struct {
	TurnID    string           `json:"turnId"`
	MessageID string           `json:"messageId"`
	Op        string           `json:"op"` // append_text | set_thinking | upsert_tool | replace_parts
	Text      string           `json:"text,omitempty"`  // append_text / set_thinking
	Part      *ProjectionPart  `json:"part,omitempty"`  // upsert_tool
	Parts     []ProjectionPart `json:"parts,omitempty"` // replace_parts
}

// ProjectionPatch is the projection_patch push frame: a baseRev→syncRev delta.
type ProjectionPatch struct {
	BaseRev           int              `json:"baseRev"`
	SyncRev           int              `json:"syncRev"`
	Execution         *ExecutionView   `json:"execution,omitempty"`
	UpsertTurns       []TurnProjection `json:"upsertTurns,omitempty"`
	PartOps           []PartOp         `json:"partOps,omitempty"`
	ReplacesClientIDs []string         `json:"replacesClientIds,omitempty"`
}
