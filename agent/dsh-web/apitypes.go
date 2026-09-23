package dshweb

// API payload structs pinned against the typert-gateway generation (upstream
// checkout 0d1f50007f; live probes of the local 0.1.7-alpha.1 instance
// 2026-09-23). Field names are verbatim wire names; comments cite the source.
// Every request below is the COMPLETE args object passed to Client.Call —
// see wire.go's payload-shape note for the per-method arg-name table.

import "encoding/json"

// ── session/list (api/session-controller/src/list.ts) ──────────────────────

// sessionListRequest rides the `_request` arg (the one method whose TS
// parameter is named with an underscore). cursor is a reserved seat; the
// bridge paginates on its side.
type sessionListRequest struct {
	Cursor string `json:"cursor,omitempty"`
}

// apiSessionSummary is one session/list row.
type apiSessionSummary struct {
	SessionID       string                      `json:"sessionId"`
	UpdatedAt       int64                       `json:"updatedAt"` // ms epoch
	Running         bool                        `json:"running"`
	Blank           bool                        `json:"blank"`
	AgentAvailable  bool                        `json:"agentAvailable"`
	ParentSessionID string                      `json:"parentSessionId,omitempty"`
	Origin          string                      `json:"origin,omitempty"` // "subagent"
	Cwd             string                      `json:"cwd,omitempty"`
	AgentPreset     string                      `json:"agentPreset,omitempty"`
	Projections     *apiSessionProjectionsBlock `json:"projections,omitempty"`
}

// apiSessionProjectionsBlock: {asOfSeq, values} — values stay raw; each value
// passed its own unit schema on the host.
type apiSessionProjectionsBlock struct {
	AsOfSeq int64                      `json:"asOfSeq"` // -1 = empty log
	Values  map[string]json.RawMessage `json:"values"`
}

type sessionListValue struct {
	Items []apiSessionSummary `json:"items"`
}

// listArgs is session/list's args object (the one method whose parameter is
// named `_request`).
func listArgs() map[string]any {
	return map[string]any{"_request": sessionListRequest{}}
}

// ── session/create / prompt / cancel / rename ───────────────────────────────

// sessionCreateRequest: at most one of workspaceId / cwd (schema refine).
type sessionCreateRequest struct {
	WorkspaceID string `json:"workspaceId,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
	AgentPreset string `json:"agentPreset,omitempty"`
}

type sessionCreateValue struct {
	SessionID   string `json:"sessionId"`
	AgentPreset string `json:"agentPreset,omitempty"`
}

// promptContentPart is the official PromptContentPart wire union
// (session-controller/src/types.ts:89-96): text | image | file. Image parts
// carry canonical base64 bytes + one of the four official media types; file
// parts carry the opaque receiptId minted by a preceding uploadFileBinary
// call on the same session (S4, A4a/A4b live evidence).
type promptContentPart struct {
	Type string `json:"type"` // "text" | "image" | "file"
	Text string `json:"text,omitempty"`
	// image part fields (types.ts:90-95; admission enforces canonical base64
	// and the ImageMediaType set seat-side — errors surface verbatim).
	MediaType string `json:"mediaType,omitempty"`
	Data      string `json:"data,omitempty"`
	Name      string `json:"name,omitempty"`
	// file part field: opaque upload receipt (commands.ts:582-601
	// resolvePromptFileReceipts; unknown receipt → session/attachment-invalid
	// FILE_NOT_STAGED verbatim).
	ReceiptID string `json:"receiptId,omitempty"`
}

// sessionPromptRequest: requestId is REQUIRED (a fresh caller-side id per
// prompt; the host echoes it for queue dedup), mode is queue|steer.
type sessionPromptRequest struct {
	RequestID      string              `json:"requestId"`
	SessionID      string              `json:"sessionId"`
	Mode           string              `json:"mode"` // "queue" | "steer"
	Content        []promptContentPart `json:"content"`
	ClientTimeZone string              `json:"clientTimeZone,omitempty"`
}

type sessionPromptValue struct {
	Accepted bool `json:"accepted"`
}

type sessionCancelRequest struct {
	SessionID string `json:"sessionId"`
}

type sessionRenameRequest struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

// sessionRenameValue: the host-normalized accepted title and its event seq.
type sessionRenameValue struct {
	Title string `json:"title"`
	Seq   int64  `json:"seq"`
}

// ── session/page (cold read; beforeSeq/maxMessages page backwards) ──────────

// sessionAddress is the durable page/follow address union; the bridge only
// addresses root sessions.
type sessionAddress struct {
	Kind      string `json:"kind"` // "session"
	SessionID string `json:"sessionId"`
}

// sessionPageRequest: throughSeq -1 = latest cursor (history.ts page():
// `request.throughSeq === -1 ? -1 : …`); beforeSeq pages backwards
// (exclusive); maxMessages bounds one page.
type sessionPageRequest struct {
	Address     sessionAddress `json:"address"`
	ThroughSeq  int64          `json:"throughSeq"`
	BeforeSeq   *int64         `json:"beforeSeq,omitempty"`
	MaxMessages *int           `json:"maxMessages,omitempty"`
}

// pageRecord is one page/snapshot row: the session event. The record wrapper
// carries type:"event"; other record kinds (assistant-stream frames) exist on
// the follow wire but are opt-in and never requested here.
type pageRecord struct {
	Type  string           `json:"type"` // "event"
	Event sessionEventWire `json:"event"`
}

// sessionEventWire is the strict-envelope + wide-data SessionEvent shape,
// shared by page/snapshot records and follow event frames (与磁盘日志同构).
type sessionEventWire struct {
	Type      string          `json:"type"`
	Seq       int64           `json:"seq"`
	Time      int64           `json:"time"`
	Data      json.RawMessage `json:"data"`
	Ignorable json.RawMessage `json:"ignorable,omitempty"`
}

type sessionPageValue struct {
	Records []pageRecord `json:"records"`
	HasMore bool         `json:"hasMore"`
}

// ── session/follow (live stream; opening snapshot + gap-free events) ────────

// sessionFollowRequest opens one session's follow stream. assistantStream
// mirrors the official web client (client/transport.ts:181 hardcodes true):
// the server only subscribes the process-local agent/assistant-stream
// publication when the request opts in (history.ts:165-176) — without it the
// follow stream carries journal events only, and typert-generation journals
// commit assistant text once per completed message (runtime-types.ts:355:
// "Chunk frames are transient; the loop appends one final v2 assistant/message
// … before a committed end frame"), so the bridge would never see per-chunk
// live text. maxMessages bounds the opening snapshot (gap-seed depth; cold
// pulls stay authoritative for full history). OD-4=A: the window mirrors the
// official web client's HISTORY_PAGE_OPTIONS (client/sessions/session.ts:54,
// :631 — maxMessages 500 + turnWindow {minMessages: 50, minTurns: 2}), so a
// reconnect gap-seed covers the same recent span the Mac web reconciles.
type sessionFollowRequest struct {
	Address         sessionAddress `json:"address"`
	MaxMessages     *int           `json:"maxMessages,omitempty"`
	TurnWindow      *turnWindow    `json:"turnWindow,omitempty"`
	AssistantStream bool           `json:"assistantStream,omitempty"`
}

// turnWindow mirrors the official SessionPageRequest.turnWindow
// (session-controller/src/types.ts:476-481): stop at a Turn start after both
// minima, unless maxMessages or history exhaustion wins.
type turnWindow struct {
	MinMessages int `json:"minMessages"`
	MinTurns     int `json:"minTurns"`
}

// followSnapshot is the first item of every follow stream: the recent page
// (records ascending, ending at cursor), the cursor live events continue
// from, and the session's projections at that revision. With
// assistantStream opted in the official server also carries the live
// attempt reconnect baseline (client transport.ts:186-192 treats its
// absence as a protocol error).
type followSnapshot struct {
	Type            string                       `json:"type"` // "snapshot"
	Cursor          int64                        `json:"cursor"`
	Records         []pageRecord                 `json:"records"`
	HasMore         bool                         `json:"hasMore"`
	Projections     *apiSessionProjectionsBlock  `json:"projections,omitempty"`
	AssistantStream *assistantStreamBaseline     `json:"assistantStream,omitempty"`
}

// assistantStreamBaseline is the snapshot's opted-in reconnect baseline
// (types.ts:493-503): the session accumulator's revision plus the
// still-live attempt's compact chunk prefix.
type assistantStreamBaseline struct {
	Revision      int64                       `json:"revision"`
	ActiveAttempt *assistantStreamAttemptWire `json:"activeAttempt,omitempty"`
}

// assistantStreamAttemptWire is one active attempt in the opening snapshot.
type assistantStreamAttemptWire struct {
	AttemptID       string            `json:"attemptId"`
	StartedAfterSeq int64             `json:"startedAfterSeq"`
	Turn            int               `json:"turn"`
	Step            int               `json:"step"`
	NextIndex       int               `json:"nextIndex"`
	Stream          []json.RawMessage `json:"stream"` // compact AssistantStreamRecord runs
}

// assistantStreamFrame is the follow stream's transient frame union
// (types.ts:511-541): start registers one attempt, chunk carries one dense
// StreamChunk, end terminates with the durable settlement pointer. Frames
// are process-local and never enter the journal.
type assistantStreamFrame struct {
	Type           string                     `json:"type"` // "start" | "chunk" | "end"
	AttemptID      string                     `json:"attemptId"`
	Revision       int64                      `json:"revision"`
	Index          int                        `json:"index"` // chunk: dense position; end: represented chunk count
	Time           int64                      `json:"time"`  // chunk only
	Chunk          *dshChunk                  `json:"chunk"` // chunk only
	StartedAfterSeq int64                     `json:"startedAfterSeq"` // start only
	Turn           int                        `json:"turn"`             // start only
	Step           int                        `json:"step"`             // start only
	Outcome        *assistantStreamOutcome   `json:"outcome"`          // end only
}

// assistantStreamOutcome is the end frame's settlement pointer.
type assistantStreamOutcome struct {
	Kind      string `json:"kind"`      // "committed" | "abandoned"
	EventType string `json:"eventType"` // committed: "assistant/message" | "assistant/attempt"
	Seq       int64  `json:"seq"`
}

// ── session/projections (per-session projection read) ──────────────────────

type sessionProjectionsRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionProjectionsValue is the same block the list rows carry inline.
type sessionProjectionsValue struct {
	AsOfSeq int64                      `json:"asOfSeq"`
	Values  map[string]json.RawMessage `json:"values"`
}

// modelSelectionProjection is projections.values.modelSelection: the
// session's last-used and queued-next selections (null before first use).
type modelSelectionProjection struct {
	LastUsed *modelSelection `json:"lastUsed"`
	Next     *modelSelection `json:"next"`
}

// ── models: session/modelCatalog + llm/listProviders + selectModel ─────────

// configurableProviderRow is one llm/listConfigurableProviders row. The
// pre-gateway `active` bit is gone; routability is the session/modelCatalog
// routableProviders set.
type configurableProviderRow struct {
	Provider     string   `json:"provider"`
	DisplayName  string   `json:"displayName"`
	SettingsNs   string   `json:"settingsNs"`
	SettingsPath []string `json:"settingsPath"`
	Declared     bool     `json:"declared"`
}

type configurableProvidersValue struct {
	Providers []configurableProviderRow `json:"providers"`
}

// modelCatalogValue is session/modelCatalog (no args): the routable provider
// id set plus the full per-provider model groups.
type modelCatalogValue struct {
	Default           modelSelection       `json:"default"`
	RoutableProviders []string             `json:"routableProviders"`
	Groups            []modelProviderGroup `json:"groups"`
}

type modelReasoningEffort struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type modelReasoning struct {
	Efforts       []modelReasoningEffort `json:"efforts"`
	DefaultEffort string                 `json:"defaultEffort,omitempty"`
}

type modelCatalogModel struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Reasoning   *modelReasoning `json:"reasoning,omitempty"`
}

type modelProviderGroup struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Models []modelCatalogModel `json:"models"`
}

type modelSelection struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

type sessionSelectModelRequest struct {
	SessionID       string `json:"sessionId"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

type sessionSelectModelValue struct {
	Selected modelSelection `json:"selected"`
}

// ── workspace/follow (grouping baseline stream) ─────────────────────────────

// apiWorkspaceView: one durable Workspace projected for browser consumers
// (workspace-controller/src/types.ts WorkspaceView — same shape the retired
// workspace.list RPC returned).
type apiWorkspaceView struct {
	WorkspaceID string   `json:"workspaceId"`
	Path        string   `json:"path"`
	Title       string   `json:"title"`
	SessionIDs  []string `json:"sessionIds"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

// workspaceFollowFrame is the workspace/follow stream item: exactly one
// baseline per generation, then ordered increments.
type workspaceFollowFrame struct {
	Type               string             `json:"type"`                         // baseline|upsert|remove|order|archived
	Value              *workspaceBaseline `json:"value,omitempty"`              // baseline
	Workspace          *apiWorkspaceView  `json:"workspace,omitempty"`          // upsert
	WorkspaceID        string             `json:"workspaceId,omitempty"`         // remove
	WorkspaceIDs       []string           `json:"workspaceIds,omitempty"`        // order
	ArchivedSessionIDs []string           `json:"archivedSessionIds,omitempty"` // archived
	// PinnedSessionIDs carries the {type:'pinned'} increment (official
	// WorkspaceFollowIncrement, workspace-controller/src/types.ts:174).
	PinnedSessionIDs []string `json:"pinnedSessionIds,omitempty"`
}

type workspaceBaseline struct {
	Items              []apiWorkspaceView `json:"items"`
	ArchivedSessionIds []string           `json:"archivedSessionIds"`
	// PinnedSessionIds is the registry-global pin set, most recently pinned
	// first (WorkspaceBaseline, workspace-controller/src/types.ts:160-166).
	PinnedSessionIds []string `json:"pinnedSessionIds"`
}
