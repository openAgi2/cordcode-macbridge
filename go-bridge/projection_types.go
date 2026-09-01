package gobridge

// Session Projection Stream (session_sync_v2) wire types.
//
// These MUST match docs/protocol/schema/bridge-v1.types.ts「Session Projection Stream」.
// Field names use lowerCamelCase JSON tags identical to the TypeScript shapes so a Mac-emitted
// patch/snapshot decodes directly into the iOS Swift mirror (Phase 2 WP7). Non-breaking,
// additive over bridge-v1; see bridge-v1_schema.go BridgeProtocolSchemaRevision.

// ProjectionPart is one part of a message projection. Type mirrors the Mapping Notes part
// vocabulary (text | reasoning | tool | file | subagent). Only the fields relevant to a type are set.
type ProjectionPart struct {
	Type string `json:"type"` // text | reasoning | tool | file | subagent

	// text / reasoning
	Text         string `json:"text,omitempty"`
	Presentation string `json:"presentation,omitempty"` // text: progress | final
	// presentationExplicit is reducer-local provenance. A live legacy delta has no
	// official phase even though the reducer may classify it for rendering later;
	// history text_delta frames carrying Codex MessagePhase set this bit so terminal
	// classification never promotes official commentary by array position.
	presentationExplicit bool

	// canonical official item id. Tool parts: rollout call_id (authoritative tool
	// identity). Text/reasoning/user parts: upstream item id, used by detail merge
	// dedup (T2.1; agent Summary mapper and reducer both stamp it).
	ItemID     string      `json:"itemId,omitempty"`
	ToolName   string      `json:"toolName,omitempty"`
	ToolInput  interface{} `json:"toolInput,omitempty"`
	ToolResult interface{} `json:"toolResult,omitempty"`
	ToolStatus string      `json:"toolStatus,omitempty"`
	Matches    interface{} `json:"matches,omitempty"`
	// Title is an optional path-bearing display title for tool steps (Claude file_path,
	// Codex patch target). Additive; see bridge-v1.types.ts BridgeProjectionPart tool.title.
	Title string `json:"title,omitempty"`
	// FileChanges is optional structured file mutations for this tool step (Codex Patch).
	// Wire shape: []{path, kind?, movePath?, diff?}. Additive; see bridge-v1.types.ts
	// BridgeProjectionPart tool.fileChanges.
	FileChanges interface{} `json:"fileChanges,omitempty"`
	// RequiresPermissionConfirmation marks a pending tool that must be approved
	// before the turn continues (dsh-web approval/requested → permission_request).
	// Additive; absent/false on older producers. SSV2 clients render the existing
	// permission card from this flag — the raw permission_request is not SoT.
	RequiresPermissionConfirmation bool `json:"requiresPermissionConfirmation,omitempty"`
	// Official permission payload (opencode-web v1.18 permission.asked, live-pinned):
	// category key + pattern rows mirror the wire permission_request fields; SSV2
	// clients render the official card (需要权限 + category line + patterns +
	// reject/always/once triple) from the projected part. Additive; absent on
	// other backends. See bridge-v1.md「Official permission payload」.
	PermissionKind     string   `json:"permissionKind,omitempty"`
	PermissionPatterns []string `json:"permissionPatterns,omitempty"`
	PermissionActions  []string `json:"permissionActions,omitempty"`

	// file
	Path     string `json:"path,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Diff     string `json:"diff,omitempty"`
	MovePath string `json:"movePath,omitempty"`

	// subagent (Type=="subagent") — Claude Agent/Task tool nested subagent group.
	// Populated by the MacBridge projection kernel as the single source of truth (B4 child-stream);
	// iOS/remote-web map this read-only into WebSubagentGroupBlock. Join keys mirror the real
	// sidechain `.meta.json` schema (sample-verified): depth-1 anchors to the mainstream turn via
	// SpawnToolUseID ↔ mainstream Agent tool_use id; depth≥2 nests via ParentAgentID ↔ parent agentId.
	AgentID            string           `json:"agentId,omitempty"`
	ParentAgentID      string           `json:"parentAgentId,omitempty"`
	SpawnToolUseID     string           `json:"spawnToolUseId,omitempty"`
	SpawnDepth         int              `json:"spawnDepth,omitempty"`
	SubagentType       string           `json:"subagentType,omitempty"`   // async | sync (from .meta.json agentType)
	SubagentStatus     string           `json:"subagentStatus,omitempty"` // running | completed | failed (sample only verified completed)
	SubagentBlocks     []ProjectionPart `json:"subagentBlocks,omitempty"` // recursive (text/reasoning/tool/nested subagent)
	SubagentError      string           `json:"subagentError,omitempty"`
	SubagentDiagnostic string           `json:"subagentDiagnostic,omitempty"` // orphan_parent | cycle | max_depth

	// user_input (Type=="user_input") — structured user input interaction (design §6/§10).
	// One part per interactionId, upserted in place (never a second "answered" card). The kernel
	// owns its status; reducer derives execution.phase=requires_action while any active-turn
	// user_input part is pending. Projection never stores answer content (esp. isSecret); the
	// resolved event only carries status/source/resolvedAt.
	//
	// UserInputQuestions is the canonical question array as wire data ([]any of maps with
	// id/header?/prompt/answerMode/options[{id,label,description?}]/allowsCustomAnswer/isSecret/
	// required); deep-cloned via cloneProjectionJSONValue like other interface{} part fields.
	UserInputInteractionID    string      `json:"interactionId,omitempty"`
	UserInputStatus           string      `json:"status,omitempty"` // pending|answered|rejected|auto_resolved|unavailable|failed
	UserInputQuestions        interface{} `json:"questions,omitempty"`
	UserInputCanRespond       bool        `json:"canRespond,omitempty"`
	UserInputCanReject        bool        `json:"canReject,omitempty"`
	UserInputExpiresAt        int64       `json:"expiresAt,omitempty"`
	UserInputResolvedAt       int64       `json:"resolvedAt,omitempty"`
	UserInputResolutionSource string      `json:"resolutionSource,omitempty"` // ios|mac|other_client|backend
	UserInputDiagnosticCode   string      `json:"diagnosticCode,omitempty"`
}

// MessageProjection is one user/assistant/system message within a turn.
type MessageProjection struct {
	ID       string           `json:"id"` // authoritative source id (see bridge-v1.md SPS)
	ClientID string           `json:"clientId,omitempty"`
	Role     string           `json:"role"` // user | assistant | system
	Parts    []ProjectionPart `json:"parts"`
}

// TurnProjection is one turn's projection. TurnID is the Codex rollout lifecycle turn_id.
type TurnProjection struct {
	TurnID      string `json:"turnId"`
	Status      string `json:"status"` // pending | running | completed | aborted | error
	StartedAt   int64  `json:"startedAt,omitempty"`
	CompletedAt int64  `json:"completedAt,omitempty"`
	// Official Turn.durationMs (app-server-protocol v2, "if known"). 0/absent = unknown;
	// clients fall back to completedAt−startedAt. Additive to bridge-v1 TurnProjection.
	DurationMs int64              `json:"durationMs,omitempty"`
	User       *MessageProjection `json:"user,omitempty"`
	Assistant  *MessageProjection `json:"assistant,omitempty"`
	System     *MessageProjection `json:"system,omitempty"`
	// turn_detail_lazy_v1 (bridge-v1.md, frozen 2026-08-30): turn-level lazy-detail
	// state. Absent decodes as DetailStateNotRequested (old snapshots stay valid).
	DetailLoadState  string `json:"detailLoadState,omitempty"`  // "" (notRequested) | loading | loaded | failed
	DetailReasonCode string `json:"detailReasonCode,omitempty"` // non-empty iff failed
	// True only for codex-remote legacy full-read turns. Their complete process
	// body is inline in Assistant.Parts, so expansion is local and never needs
	// the paginated session_turn_items transport.
	DetailInline   bool `json:"detailInline,omitempty"`
	TurnGeneration int  `json:"generation,omitempty"` // per-turn fence counter; bumps on post-completion content mutation
	// turn_detail_chunks_v1 (§11.8, owner final ruling 2026-08-30): manifest
	// SUMMARY only — detail content never enters the projection. Additive;
	// absent decodes as zero (v1 snapshots stay valid).
	DetailManifestRev int   `json:"detailManifestRev,omitempty"` // 0 = no manifest yet
	DetailItemCount   int   `json:"detailItemCount,omitempty"`
	DetailTotalBytes  int64 `json:"detailTotalBytes,omitempty"`
}

// ExecutionView is the session-level execution state. isExecuting = phase ∈ {running, requires_action}.
type ExecutionView struct {
	Phase        string `json:"phase"` // idle | running | requires_action
	ActiveTurnID string `json:"activeTurnId,omitempty"`
}

// SessionProjection is the authoritative per-(backendId,sessionId) projection. SyncRev belongs
// to the ProjectionReducer/Kernel commit chain; it is intentionally distinct from transport
// EventPublisher per-session sequence. Push and pull read the same committed Kernel head.
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
	Op        string           `json:"op"`              // append_text | set_thinking | upsert_tool | upsert_user_input | replace_parts
	Text      string           `json:"text,omitempty"`  // append_text / set_thinking
	Part      *ProjectionPart  `json:"part,omitempty"`  // upsert_tool / upsert_user_input
	Parts     []ProjectionPart `json:"parts,omitempty"` // replace_parts
}

// ProjectionPatch is the projection_patch push frame: a baseRev→syncRev delta.
type ProjectionPatch struct {
	BaseRev           int              `json:"baseRev"`
	SyncRev           int              `json:"syncRev"`
	Execution         *ExecutionView   `json:"execution,omitempty"`
	UpsertTurns       []TurnProjection `json:"upsertTurns,omitempty"`
	PartOps           []PartOp         `json:"partOps,omitempty"`
	TurnStateOps      []TurnStateOp    `json:"turnStateOps,omitempty"`
	ReplacesClientIDs []string         `json:"replacesClientIds,omitempty"`
}

// TurnStateOp is one turnStateOps entry of ProjectionPatch (turn_detail_lazy_v1,
// bridge-v1.md). Applied AFTER upsertTurns and BEFORE partOps; state ops never
// carry content and never bump TurnGeneration (the content mutation's
// replace_parts admission does, committed with T2.2).
type TurnStateOp struct {
	TurnID          string `json:"turnId"`
	DetailLoadState string `json:"detailLoadState"` // loading | loaded | failed (never "" or notRequested on the wire)
	ReasonCode      string `json:"reasonCode,omitempty"`
	TurnGeneration  int    `json:"generation"`
	// turn_detail_chunks_v1 manifest op (§11.8): additive summary fields. A
	// v2 op may set DetailLoadState=partial and carries the manifest summary
	// (validated by ValidateTurnStateOpsV2; v1 conns never receive v2 ops).
	ManifestRev int   `json:"manifestRev,omitempty"`
	ItemCount   int   `json:"itemCount,omitempty"`
	TotalBytes  int64 `json:"totalBytes,omitempty"`
}
