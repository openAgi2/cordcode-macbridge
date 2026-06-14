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

// ToolAuthorizer is an optional interface for agents that support dynamic tool authorization.
type ToolAuthorizer interface {
	AddAllowedTools(tools ...string) error
	GetAllowedTools() []string
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
