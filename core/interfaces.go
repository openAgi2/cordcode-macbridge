package core

import (
	"context"
	"errors"
	"time"
)

// ErrNotSupported indicates an agent backend does not support a requested operation.
var ErrNotSupported = errors.New("operation not supported")

// SessionEnvInjector is an optional interface for agents that accept
// per-session environment variables (e.g. CC_PROJECT, CC_SESSION_KEY).
type SessionEnvInjector interface {
	SetSessionEnv(env []string)
}

// AgentSystemPrompt returns extra instructions appended to agent system prompts.
// MacBridge does not expose cccode-macbridge CLI helper commands, so this must stay
// empty unless a MacBridge-native capability is added.
func AgentSystemPrompt() string {
	return ""
}

// Agent abstracts an AI coding assistant (Claude Code, Cursor, Gemini CLI, etc.).
// All agents must support persistent bidirectional sessions via StartSession.
type Agent interface {
	Name() string
	// StartSession creates or resumes an interactive session with a persistent process.
	StartSession(ctx context.Context, sessionID string) (AgentSession, error)
	// ListSessions returns sessions known to the agent backend.
	ListSessions(ctx context.Context) ([]AgentSessionInfo, error)
	Stop() error
}

// AgentSession represents a running interactive agent session with a persistent process.
type AgentSession interface {
	// Send sends a user message (with optional images and files) to the running agent process.
	Send(prompt string, images []ImageAttachment, files []FileAttachment) error
	// RespondPermission sends a permission decision back to the agent process.
	RespondPermission(requestID string, result PermissionResult) error
	// Events returns the channel that emits agent events (kept open across turns).
	Events() <-chan Event
	// CurrentSessionID returns the current agent-side session ID.
	CurrentSessionID() string
	// Alive returns true if the underlying process is still running.
	Alive() bool
	// Close terminates the session and its underlying process.
	Close() error
	// RespondQuestion sends a reply to a question asked by the agent (Codex ask).
	RespondQuestion(questionID string, optionIDs []string) error
	// RejectQuestion rejects a question without answering (Codex ask).
	RejectQuestion(questionID string) error
}

// PermissionResult represents the user's decision on a permission request.
type PermissionResult struct {
	Behavior     string         `json:"behavior"`               // "allow" or "deny"
	UpdatedInput map[string]any `json:"updatedInput,omitempty"` // echoed back for allow
	Message      string         `json:"message,omitempty"`      // reason for deny
}

// AttachmentSupporter is an optional interface for agents that can declare
// which attachment kinds their CURRENT configuration semantically supports.
// This is the single positive truth source for the go-bridge attachment gate
// (design docs/2026-08-13-dsh-driver-design.md §3.9): absence of the
// interface — or absence of a kind — means that kind is NOT supported, and a
// structurally valid attachment of that kind is rejected with
// unsupported_attachment before StartSession.
//
// Declaring a kind is a semantic claim, not a signature claim: a driver may
// list "image" only when image bytes genuinely reach the backend request
// (mode-dependent drivers — e.g. opencode server mode silently dropping
// staged images — must NOT list it in that mode).
type AttachmentSupporter interface {
	// SupportedAttachmentKinds returns the positively supported subset of
	// {"image", "file"} for the agent's current mode. "text"-only drivers
	// should not implement this interface at all.
	SupportedAttachmentKinds() []string
}

// ToolAuthorizer is an optional interface for agents that support dynamic tool authorization.
type ToolAuthorizer interface {
	AddAllowedTools(tools ...string) error
	GetAllowedTools() []string
}

// ── 结构化用户输入 v2 回答能力（设计 §7/§10.1）─────────────────────────────────
// go-bridge resolve_user_input handler 只调用 UserInputResponder；旧
// RespondQuestion/RejectQuestion 仅留给明确 `.off` legacy client，不作为 v2 fallback。

// UserInputAction 是提交动作。
type UserInputAction string

const (
	UserInputActionAnswer UserInputAction = "answer"
	UserInputActionReject UserInputAction = "reject"
)

// UserInputValueKind 表达单个 value 是选项引用还是自定义文本。
type UserInputValueKind string

const (
	UserInputValueOption UserInputValueKind = "option"
	UserInputValueText   UserInputValueKind = "text"
)

// UserInputValue 是一题答案中的一个值。
type UserInputValue struct {
	Kind     UserInputValueKind `json:"kind"`
	OptionID string             `json:"optionId,omitempty"`
	Text     string             `json:"text,omitempty"`
}

// UserInputAnswer 是一题的规范化答案。
type UserInputAnswer struct {
	QuestionID string           `json:"questionId"`
	Values     []UserInputValue `json:"values"`
}

// UserInputResolutionOutcome 是 resolve RPC 的结果分类。
type UserInputResolutionOutcome string

const (
	UserInputOutcomeAccepted        UserInputResolutionOutcome = "accepted"
	UserInputOutcomeAlreadyResolved UserInputResolutionOutcome = "already_resolved"
	UserInputOutcomeInProgress      UserInputResolutionOutcome = "in_progress"
)

// UserInputResolution 是 ResolveUserInput 的返回。
type UserInputResolution struct {
	Outcome       UserInputResolutionOutcome
	CurrentStatus UserInputStatus
	HeadRev       int
}

// UserInputError 是结构化用户输入 resolve 的稳定错误，携带 §7 固定错误码。
// go-bridge resolve_user_input handler 用它映射 WireError；adapter 不得回显 secret/custom answer。
type UserInputError struct {
	Code    string
	Message string
}

func (e *UserInputError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// UserInputResponder 是 v2 结构化用户输入的唯一回答能力接口。
// 实现方负责原子 claim（first-writer-wins）、写真实 backend response、
// 成功后才提交 Kernel resolved event；clientActionID 提供幂等。
type UserInputResponder interface {
	ResolveUserInput(ctx context.Context, interactionID string, clientActionID string, action UserInputAction, answers []UserInputAnswer) (UserInputResolution, error)
}

// StructuredUserInputProvider marks an agent whose production adapter, real
// responder, and canonical interaction producer are enabled together. Backend
// capability advertisement must use this readiness instead of backend-name
// inference so a dormant session flag cannot diverge from the descriptor.
type StructuredUserInputProvider interface {
	StructuredUserInputReady() bool
}

// TurnCanceler is an optional interface for agent sessions that can cancel
// the currently running turn via an RPC call to the backend service.
type TurnCanceler interface {
	CancelTurn(ctx context.Context) error
}

// HistoryProvider is an optional interface for agents that can retrieve
// conversation history from their backend session files.
type HistoryProvider interface {
	GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]HistoryEntry, error)
}

// RichHistoryProvider is an optional interface for agents that can retrieve
// structured history with parts, steps, and thinking blocks without replacing
// the legacy HistoryProvider compatibility contract.
type RichHistoryProvider interface {
	GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]RichHistoryEntry, error)
}

// SessionActivityProbing is an optional interface for agents whose backend can
// report whether a session currently has a turn in flight. Cold-hydrate
// producers use it to decide whether a trailing unanswered user turn is a dead
// failure (safe to settle) or a live in-flight prompt (must stay open).
// Implementations must be conservative: unknown/error ⇒ active.
type SessionActivityProbing interface {
	IsSessionActive(ctx context.Context, sessionID string) bool
}

// TranscriptSourceSegment identifies one physical file in an ordered,
// source-proven logical transcript. Cursor is filled by the bridge at hydrate
// admission and is the exclusive complete-line byte cut for that file.
type TranscriptSourceSegment struct {
	Identity string
	Path     string
	Cursor   int64
}

// CompositeRichHistoryProvider is an optional contract for file-backed agents
// whose one logical session can span multiple physical transcripts. The bridge
// freezes every segment cut, then asks the provider to parse exactly that
// immutable source descriptor.
type CompositeRichHistoryProvider interface {
	RichHistoryTranscriptSegments(ctx context.Context, sessionID string) ([]TranscriptSourceSegment, error)
	GetRichSessionHistoryAtSegments(
		ctx context.Context,
		sessionID string,
		segments []TranscriptSourceSegment,
	) ([]RichHistoryEntry, error)
}

// TranscriptLocator is an optional interface for file-backed agents that can
// resolve the on-disk JSONL transcript path for a session. The bridge uses it to
// build a boundary-safe transcript page index (design §6.3) and to replay byte
// ranges for paginated get_session_messages. Agents that cannot expose a stable
// file path (e.g. proxied backends) should not implement it.
type TranscriptLocator interface {
	TranscriptPath(ctx context.Context, sessionID string) (string, error)
}

// TodoProvider is an optional interface for agents that can return backend
// todos for a session without relying on bridge-specific HTTP fallbacks.
type TodoProvider interface {
	FetchTodos(ctx context.Context, sessionID string) ([]Todo, error)
}

// AgentLister is an optional interface for agents that can enumerate available
// backend agent profiles without returning placeholder empty data.
type AgentLister interface {
	ListAgents(ctx context.Context) ([]AgentDescriptor, error)
}

// ProviderConfig holds API provider settings for an agent.
type ProviderConfig struct {
	Name     string
	APIKey   string
	BaseURL  string
	Model    string
	Models   []ModelOption     // pre-configured list of available models for this provider
	Thinking string            // override thinking type sent to this provider ("disabled", "enabled", or "" for no rewrite)
	Env      map[string]string // arbitrary extra env vars (e.g. CLAUDE_CODE_USE_BEDROCK=1)
	// Codex-specific provider config (maps to Codex model_providers.<name>)
	CodexWireAPI     string            // wire API format (e.g. "responses")
	CodexHTTPHeaders map[string]string // custom HTTP headers
}

// ProviderSwitcher is an optional interface for agents that support multiple API providers.
type ProviderSwitcher interface {
	SetProviders(providers []ProviderConfig)
	SetActiveProvider(name string) bool
	GetActiveProvider() *ProviderConfig
	ListProviders() []ProviderConfig
}

// MemoryFileProvider is an optional interface for agents that support
// persistent instruction files (CLAUDE.md, AGENTS.md, GEMINI.md, etc.).
// The engine uses these paths for the /memory command.
type MemoryFileProvider interface {
	ProjectMemoryFile() string // project-level instruction file (e.g., <work_dir>/CLAUDE.md)
	GlobalMemoryFile() string  // user-level instruction file (e.g., ~/.claude/CLAUDE.md)
}

// MemoryFile is a normalized read-only instruction file descriptor used by
// bridge-facing memory APIs.
type MemoryFile struct {
	ID           string
	Name         string
	Path         string
	Scope        string
	Description  string
	SizeBytes    int64
	UpdatedAt    time.Time
	LastModified time.Time
	ETag         string
	ContentType  string
	Encoding     string
	Content      string
}

// MemoryFileReader is an optional interface for agents that expose stable,
// read-only memory files (such as project/global CLAUDE.md) via opaque file IDs.
type MemoryFileReader interface {
	ListMemoryFiles(ctx context.Context) ([]MemoryFile, error)
	ReadMemoryFile(ctx context.Context, fileID string) (*MemoryFile, error)
}

// ModelSwitcher is an optional interface for agents that support runtime model switching.
// Model changes take effect on the next session (existing sessions keep their model).
type ModelSwitcher interface {
	SetModel(model string)
	GetModel() string
	// AvailableModels tries to fetch models from the provider API.
	// Falls back to a built-in list on failure.
	AvailableModels(ctx context.Context) []ModelOption
}

// ReasoningEffortSwitcher is an optional interface for agents that support
// runtime switching of reasoning effort.
type ReasoningEffortSwitcher interface {
	SetReasoningEffort(effort string)
	GetReasoningEffort() string
	AvailableReasoningEfforts() []string
}

// ModelOption describes a selectable model.
type ModelOption struct {
	Name  string // model identifier passed to CLI
	Desc  string // short description (display_name or empty)
	Alias string // optional short alias for the /model command (e.g. "codex" for "gpt-5.3-codex")
}

// UsageReporter is an optional interface for agents that can report account or
// model quota usage from their backing provider.
type UsageReporter interface {
	GetUsage(ctx context.Context) (*UsageReport, error)
}

// TokenUsageReporter is an optional interface for agents that can report
// transcript-derived token totals in the unified bridge shape.
type TokenUsageReporter interface {
	GetTokenUsage(ctx context.Context) (*TokenUsageReport, error)
}

// UsageReport is a provider-neutral quota snapshot returned by UsageReporter.
type UsageReport struct {
	Provider  string
	AccountID string
	UserID    string
	Email     string
	Plan      string
	Buckets   []UsageBucket
	Credits   *UsageCredits
}

// UsageBucket groups one logical quota, such as standard requests or code review.
type UsageBucket struct {
	Name         string
	Allowed      bool
	LimitReached bool
	Windows      []UsageWindow
}

// UsageWindow describes a single quota window.
type UsageWindow struct {
	Name              string
	UsedPercent       int
	WindowSeconds     int
	ResetAfterSeconds int
	ResetAtUnix       int64
}

// UsageCredits contains optional credit/balance metadata.
type UsageCredits struct {
	HasCredits bool
	Unlimited  bool
	Balance    string
}

// TokenUsageReport is a lightweight aggregate token report suitable for the
// unified bridge get_usage RPC.
type TokenUsageReport struct {
	TotalTokensUsed     int
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	PerSessionBreakdown []SessionTokenUsage
}

// SessionTokenUsage contains one session's aggregated transcript token totals.
type SessionTokenUsage struct {
	SessionID           string
	TokensUsed          int
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
}

// DiagnosticProgress is one incremental diagnostics update.
type DiagnosticProgress struct {
	CheckID string
	Status  string
	Message string
}

// DiagnosticResult is one completed diagnostics check result.
type DiagnosticResult struct {
	ID            string
	Name          string
	Status        string
	Message       string
	Severity      string
	FixSuggestion string
}

// DiagnosticReport is the final result of one diagnostics run.
type DiagnosticReport struct {
	Results       []DiagnosticResult
	OverallStatus string
}

// DiagnosticsProvider is an optional interface for agents that can run
// backend diagnostics and stream incremental progress.
type DiagnosticsProvider interface {
	RunDiagnostics(ctx context.Context, progress func(DiagnosticProgress)) (*DiagnosticReport, error)
}

// ContextUsageReporter is an optional interface for running agent sessions that
// can report real runtime context usage for the active conversation.
type ContextUsageReporter interface {
	GetContextUsage() *ContextUsage
}

// ContextUsage describes runtime context consumption for the active session.
type ContextUsage struct {
	// UsedTokens is the current token load to compare against ContextWindow when
	// computing remaining context capacity for the next turn.
	UsedTokens int
	// BaselineTokens is the portion of the context window always occupied by
	// fixed runtime/system instructions and therefore excluded from user-visible
	// "left" calculations when the agent provides it.
	BaselineTokens        int
	TotalTokens           int
	InputTokens           int
	CachedInputTokens     int
	OutputTokens          int
	ReasoningOutputTokens int
	ContextWindow         int
}

// ContextCompressor is an optional interface for agents that support
// compressing/compacting the conversation context within a running session.
// CompressCommand returns the native slash command (e.g. "/compact", "/compress")
// that will be forwarded to the agent process. Return "" if not supported.
type ContextCompressor interface {
	CompressCommand() string
}

// ContextCompactingSession is an optional AgentSession capability for
// context compression via a dedicated RPC method (e.g. Codex thread/compact/start).
// AgentSession implementations that support compression must also implement this interface.
// Callers discover it by type-asserting an AgentSession instance:
//
//	if cc, ok := session.(ContextCompactingSession); ok {
//	    err := cc.CompactContext(ctx)
//	}
//
// CompactContext returns nil when the compress request has been accepted by the backend.
// Actual completion is signaled by subsequent events (EventContextCompressing / EventContextCompressed)
// on the session's Events() channel; callers must not treat a nil return as "done".
type ContextCompactingSession interface {
	CompactContext(ctx context.Context) error
}

// CommandProvider is an optional interface for agents that expose custom slash
// commands via local files (e.g. .claude/commands/*.md). The engine scans the
// returned directories for *.md files and registers them as slash commands.
type CommandProvider interface {
	CommandDirs() []string
}

// SkillProvider is an optional interface for agents that expose skills via
// local directories (e.g. .claude/skills/<name>/SKILL.md). Each subdirectory
// containing a SKILL.md is treated as a skill. Skills are project-level and
// agent-specific — they are NOT shared across different agent types.
type SkillProvider interface {
	SkillDirs() []string
}

// SessionDeleter is an optional interface for agents that support deleting sessions.
type SessionDeleter interface {
	DeleteSession(ctx context.Context, sessionID string) error
}

// SessionRenamer is an optional interface for agents that support renaming sessions.
type SessionRenamer interface {
	RenameSession(ctx context.Context, sessionID, title string) (*AgentSessionInfo, error)
}

// SessionArchiver is an optional interface for agents that support archiving sessions.
type SessionArchiver interface {
	ArchiveSession(ctx context.Context, sessionID string, archivedAt time.Time) (*AgentSessionInfo, error)
}

// SessionPinner is an optional interface for agents that support pinning (置顶) sessions.
// Pin state is MacBridge-owned session metadata (NOT agent-local storage): each driver
// persists it in its prescribed store (Claude → .cc-connect-session-meta sidecar;
// Codex/OpenCode → bridge-owned pin index). deriveBackendCapabilities advertises the
// backend-neutral "session_pin" capability when a driver implements this interface,
// INDEPENDENT of SessionRenamer/SessionArchiver, so Codex/OpenCode (which lack
// rename/archive) still advertise pinning.
//
// The interface is identity-only on purpose: it does NOT resolve session summaries.
// Summary fields (title/messageCount/modifiedAt) are resolved by the go-bridge handler
// from the real backend source (Claude catalog / Codex sessionListCache / OpenCode proxy)
// when building AgentSessionInfo for the wire. This keeps the catalog/proxy/cache — all
// of which live in go-bridge — out of the agent packages, and keeps the pin store limited
// to identity + pinnedAt.
//
// SetSessionPinned is idempotent: pinned=true with pinnedAt records/overwrites the pin
// (storing directory as the scope hint so later enrichment can resolve the summary, e.g.
// OpenCodeProxy.getSession(id, dir)); pinned=false clears it. directory is the RPC request's
// scope hint (the workDir/project dir); the driver still computes its own internal scope
// (Claude sessionID-only, Codex CODEX_HOME, OpenCode normalized directory) for the store key.
// It returns the resulting pin entry.
//
// ListPinnedSessions returns ALL pinned entries for this backend (NOT directory-scoped).
// The handler enriches each entry and applies the prune-vs-fail rule: a definitively-missing
// session (e.g. OpenCode upstream 404) is pruned and omitted; a transient upstream failure
// fails the RPC rather than yielding a fabricated/partial summary. See
// docs/protocol/bridge-v1.md「Session Pinning」.
type SessionPinner interface {
	SetSessionPinned(ctx context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*SessionPin, error)
	ListPinnedSessions(ctx context.Context) ([]SessionPin, error)
}

// CheckpointProvider is an opt-in interface for agents whose sessions MacBridge may
// snapshot into hidden git refs after each completed turn (§6.1 read-only checkpoint
// diff). The snapshot is a workspace FILE snapshot only — it is NOT a session truth
// source; session truth always stays in the official CLI (plan §3 防呆, SSV2 guardrail 1).
//
// Per-session workspace resolution lives in go-bridge (sessionRegistry.directoryForSession,
// populated when create_session/send_message carry a directory); the driver does not need
// to track (sessionID → cwd) itself. The capability therefore gates the feature on
// "this backend opts in", while capture still no-ops honestly when the resolved workspace
// is not a git repo (workspace_not_git) — no mock/placeholder snapshot is ever written.
//
// deriveBackendCapabilities advertises "supports_checkpoint" when a driver implements
// this interface; iOS gates the diff UI on that capability string.
type CheckpointProvider interface {
	// SupportsCheckpoint is the stable opt-in signal. Returning true means MacBridge
	// may capture read-only git-ref workspace checkpoints for this backend's sessions.
	SupportsCheckpoint() bool
}

// ConversationRollbackProvider is a forward-compatibility opt-in interface for agents
// that can roll a conversation back to a prior turn. No driver implements it today;
// the capability "supports_conversation_rollback" stays absent until one does, which
// keeps the (currently hidden) revert entry gated off (§6.1 "revert 未实现").
// The signature is intentionally minimal so a future driver can fill it in without
// reshaping the interface.
type ConversationRollbackProvider interface {
	// RollbackConversationToTurn rolls the conversation state of sessionID back to the
	// given 1-based turn number (as reported by ProjectionReducer.TurnCount). It must
	// return a non-nil error until a real driver implements it.
	RollbackConversationToTurn(ctx context.Context, sessionID string, turnNumber int) error
}

// LiveEventModel enumerates how a backend surfaces external-turn progress to MacBridge
// (and thus to clients). §6.2 driver self-description carries this as a static value;
// the codex app_server runtime override (session_process → broadcast when a shared
// app-server URL is configured) still applies in go-bridge on top of the static base.
type LiveEventModel string

const (
	// LiveEventSessionProcess: the agent runs as a stdin/stdout pipe (or per-session
	// process) whose external turns MacBridge can only observe by tailing its output
	// files. claudecode / codex / grokbuild are process-model by default.
	LiveEventSessionProcess LiveEventModel = "session_process"
	// LiveEventBroadcast: the agent exposes a service-level event stream that fans out
	// external-turn events to multiple observers. opencode is broadcast-native; codex
	// upgrades to broadcast only when a shared app-server URL is configured.
	LiveEventBroadcast LiveEventModel = "broadcast"
)

// WireDescriptor is a driver's self-described static wire attributes (§6.2 provider
// 零跨层抽象). Each driver returns its own Kind / DisplayName / LiveEventModel /
// RequiresExternalTurnPolling / StaticCapabilities instead of go-bridge branching on
// backend id. Only STATIC attributes belong here — anything that depends on runtime
// state (codex app_server mode, adapter readiness, detection probes) stays in wire.
//
// StaticCapabilities is the home for A-class positive capabilities that were
// previously id-keyed in backend_capabilities.go (content_chunking, claude
// question_reply, external_turn_streaming, opencode todos 兜底). Mode-conditional
// capabilities (codex app_server compression/question_reply/structured_user_input_v1)
// and interface-gated capabilities (TodoProvider/ToolAuthorizer/...) are NOT here.
type WireDescriptor struct {
	Kind                        string
	DisplayName                 string
	LiveEventModel              LiveEventModel
	RequiresExternalTurnPolling bool
	StaticCapabilities          []string
}

// WireDescriptorProvider is implemented by every driver that self-describes its static
// wire attributes. go-bridge prefers this self-description and falls back to the
// pre-§6.2 id-keyed switches only for drivers that have not yet migrated. A nil
// descriptor is treated as "not provided" so a driver can opt out per-build.
type WireDescriptorProvider interface {
	WireDescriptor() *WireDescriptor
}

// WorkDirSwitcher is an optional interface for agents that support runtime
// work directory switching. The change takes effect on the next session start;
// the current running session is terminated automatically by the engine.
type WorkDirSwitcher interface {
	SetWorkDir(dir string)
	GetWorkDir() string
}

// ModeSwitcher is an optional interface for agents that support runtime permission mode switching.
type ModeSwitcher interface {
	SetMode(mode string)
	GetMode() string
	PermissionModes() []PermissionModeInfo
}

// WorkspaceAgentOptionSnapshotter is an optional interface for agents that can
// export reusable constructor options needed to recreate an equivalent agent in
// a different workspace. Snapshot values should omit work_dir; the caller is
// responsible for setting the target workspace explicitly. Provider wiring and
// run_as propagation may still be handled separately by the engine.
type WorkspaceAgentOptionSnapshotter interface {
	WorkspaceAgentOptions() map[string]any
}

// LiveModeSwitcher is an optional interface for running agent sessions that can
// apply a mode change immediately without restarting the process.
type LiveModeSwitcher interface {
	SetLiveMode(mode string) bool
}

// PermissionModeInfo describes a permission mode for display.
type PermissionModeInfo struct {
	Key    string
	Name   string
	NameZh string
	Desc   string
	DescZh string
}

// EventSubscriber is an optional interface for agents that can passively subscribe
// to backend broadcast events without sending messages (e.g. Codex app-server).
type EventSubscriber interface {
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// SessionEventSubscriber is the per-session counterpart: agents that expose ONE
// session's live events via a subscribe channel (e.g. Grok leader-socket: a
// read-only subscriber attaches per sessionID). Used by MacBridge's per-session
// relay for external turns (multi-client-streaming-sync refactor Phase 1 Grok).
type SessionEventSubscriber interface {
	SubscribeSessionEvents(ctx context.Context, sessionID, cwd string) (<-chan Event, error)
}

// RunningSessionLister is an optional interface for agents that can detect running sessions from the OS or filesystem.
type RunningSessionLister interface {
	GetRunningSessionIDs(ctx context.Context) (map[string]bool, error)
}

// LiveSessionProcess describes the backing process for one backend session.
// Live is identity-verified: true only when PID is alive AND the live process
// still matches the recorded session (executable is Claude, cwd matches), so a
// reused PID cannot resurrect a stale session as live.
type LiveSessionProcess struct {
	SessionID string
	PID       int
	Live      bool
}

// LiveSessionLister is the live-only counterpart to RunningSessionLister.
// LiveSessionProcess reports PID liveness AND process identity (executable +
// cwd), without transcript inspection and without executing-state
// classification. IsProcessAlive reports PID liveness only and is meant for
// cheap per-tick rechecks of a PID already identity-verified at relay start.
type LiveSessionLister interface {
	LiveSessionProcess(ctx context.Context, sessionID string) (LiveSessionProcess, error)
	IsProcessAlive(ctx context.Context, pid int) bool
}
