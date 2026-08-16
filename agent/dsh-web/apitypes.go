package dshweb

// API payload structs pinned against the dsh source schemas at 47f9438
// (packages/host/apiproxy/src/api/{sessions,host,workspace,llm}.schema.ts).
// Field names are verbatim wire names; comments cite the schema source.

import "encoding/json"

// ── session.list (sessions.schema.ts) ───────────────────────────────────────

// sessionListRequest: cursor is a reserved seat, unimplemented in v1 (one
// full page; the bridge paginates on its side).
type sessionListRequest struct {
	Cursor string `json:"cursor,omitempty"`
}

// apiSessionSummary is one session.list row.
type apiSessionSummary struct {
	SessionID       string                      `json:"sessionId"`
	UpdatedAt       int64                       `json:"updatedAt"` // ms epoch
	Running         bool                        `json:"running"`
	Blank           bool                        `json:"blank"`
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

// ── session.create / prompt / cancel / rename ───────────────────────────────

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

// promptContentPart is the wire content union; phase 1 is text-only (image
// parts exist on the official wire but this backend declares text-only via
// StaticCapabilities until attachments land in phase 2).
type promptContentPart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type sessionPromptRequest struct {
	SessionID      string              `json:"sessionId"`
	Mode           string              `json:"mode"` // "queue" | "steer"
	Content        []promptContentPart `json:"content"`
	ClientTimeZone string              `json:"clientTimeZone,omitempty"`
}

type sessionPromptCommand struct {
	Kind string  `json:"kind"` // "success"
	Text *string `json:"text,omitempty"`
}

type sessionPromptValue struct {
	Accepted bool                  `json:"accepted"`
	Command  *sessionPromptCommand `json:"command,omitempty"`
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

// ── session.history (cold read; beforeSeq/maxMessages page backwards) ──────

type sessionHistoryRequest struct {
	SessionID   string `json:"sessionId"`
	BeforeSeq   *int64 `json:"beforeSeq,omitempty"`
	MaxMessages *int   `json:"maxMessages,omitempty"`
}

// apiHistoryEntry is one history row: the session event plus its optional
// host-computed tool view (view interiors stay raw — host product).
type apiHistoryEntry struct {
	Event sessionEventWire `json:"event"`
	View  json.RawMessage  `json:"view,omitempty"`
}

// sessionEventWire is the strict-envelope + wide-data SessionEvent shape,
// shared by history rows and mux session/event frames (与磁盘日志同构).
type sessionEventWire struct {
	Type      string          `json:"type"`
	Seq       int64           `json:"seq"`
	Time      int64           `json:"time"`
	Data      json.RawMessage `json:"data"`
	Ignorable json.RawMessage `json:"ignorable,omitempty"`
}

type sessionHistoryValue struct {
	Events      []apiHistoryEntry           `json:"events"`
	HasMore     bool                        `json:"hasMore"`
	Projections *apiSessionProjectionsBlock `json:"projections,omitempty"`
}

// ── models: llm.providers / llm.models / session.models / selectModel ──────

type configurableProviderView struct {
	Provider     string   `json:"provider"`
	DisplayName  string   `json:"displayName"`
	SettingsNs   string   `json:"settingsNs"`
	SettingsPath []string `json:"settingsPath"`
	Active       bool     `json:"active"`
	Declared     *bool    `json:"declared,omitempty"`
}

type llmProvidersValue struct {
	Providers []configurableProviderView `json:"providers"`
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

type modelCatalogFailure struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type llmModelsValue struct {
	Groups   []modelProviderGroup  `json:"groups"`
	Failures []modelCatalogFailure `json:"failures"`
}

type modelSelection struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

type sessionModelsRequest struct {
	SessionID string `json:"sessionId"`
}

type sessionModelsValue struct {
	Current  modelSelection        `json:"current"`
	Routable bool                  `json:"routable"`
	Groups   []modelProviderGroup  `json:"groups"`
	Failures []modelCatalogFailure `json:"failures"`
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

// ── host / workspace (consumed by §8-6) ────────────────────────────────────

type hostListDirectoryRequest struct {
	Path string `json:"path,omitempty"`
}

type apiDirectoryEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden"`
}

type hostListDirectoryValue struct {
	Path      string              `json:"path"`
	Home      string              `json:"home"`
	Crumbs    []apiDirectoryEntry `json:"crumbs"`
	Entries   []apiDirectoryEntry `json:"entries"`
	Truncated bool                `json:"truncated"`
}

type apiWorkspaceView struct {
	WorkspaceID string   `json:"workspaceId"`
	Path        string   `json:"path"`
	Title       string   `json:"title"`
	SessionIDs  []string `json:"sessionIds"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

type workspaceListValue struct {
	Items              []apiWorkspaceView `json:"items"`
	ArchivedSessionIds []string           `json:"archivedSessionIds"`
}
