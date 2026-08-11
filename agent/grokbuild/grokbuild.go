package grokbuild

// Grok Build CLI driver for MacBridge.
// Uses `grok agent stdio` (ACP v1 JSON-RPC over stdin/stdout) to communicate
// with the Grok Build CLI. See docs/2026-07-12-grok-driver-design.md for the
// full design and docs/2026-07-12-grok-cli-compatibility-evidence.md for Gate 0
// evidence.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.Agent = (*Agent)(nil)
var _ core.DiagnosticsProvider = (*Agent)(nil)
var _ core.WorkDirSwitcher = (*Agent)(nil)
var _ core.ModeSwitcher = (*Agent)(nil)
var _ core.ModelSwitcher = (*Agent)(nil)
var _ core.ProviderSwitcher = (*Agent)(nil)
var _ core.ReasoningEffortSwitcher = (*Agent)(nil)
var _ core.ToolAuthorizer = (*Agent)(nil)
var _ core.HistoryProvider = (*Agent)(nil)
var _ core.RichHistoryProvider = (*Agent)(nil)
var _ core.SessionEventSubscriber = (*Agent)(nil)

// Agent implements core.Agent for the Grok Build CLI.
type Agent struct {
	workDir         string
	cliBin          string
	cliExtraArgs    []string
	model           string
	reasoningEffort string
	mode            string
	allowedTools    []string
	// providers / activeIdx 背载 iOS 下发的第三方 Grok provider 配置（GLM/DeepSeek 等
	// 经 grok 网关）。AvailableModels 优先返 active provider 的 Models，使 custom 模型可见；
	// modelProviderForAgent 据此把无前缀模型标到 active provider 名下而非 "default"。
	providers []core.ProviderConfig
	activeIdx int // -1 = no provider set
	// grokHome overrides ~/.grok / GROK_HOME for session catalog (tests).
	grokHome string
	mu       sync.RWMutex

	// --- catalog subprocess singleton（§5.4 Phase 3）---
	// catalogClient 是进程级单例 ACP catalog 子进程（grok agent --no-leader stdio），
	// 与 per-turn grokSession 子进程分开管理、分开回收。catalogClientMu 串行化
	// create/replace；catalogRegistrar 是 bridge ProcessRegistry 注入句柄。
	// §10：capability 未声明前 go-bridge 不路由到 FetchSessionList → 当前不可达 = 零行为变化。
	catalogClient   *grokCatalogClient
	catalogClientMu sync.Mutex
	catalogRegistrar CatalogSubprocessRegistrar
}

func init() {
	core.RegisterAgent("grokbuild", New)
}

// New creates a Grok Build agent from the given options map.
func New(opts map[string]any) (core.Agent, error) {
	a := &Agent{
		workDir:  ".",
		mode:     "default",
		activeIdx: -1,
	}

	if v, ok := opts["work_dir"].(string); ok && v != "" {
		a.workDir = v
	}

	cliPath := "grok"
	if v, ok := opts["cli_path"].(string); ok && v != "" {
		fields := strings.Fields(v)
		cliPath = fields[0]
		if len(fields) > 1 {
			a.cliExtraArgs = fields[1:]
		}
	}
	a.cliBin = cliPath

	if v, ok := opts["model"].(string); ok {
		a.model = v
	}
	if v, ok := opts["reasoning_effort"].(string); ok {
		a.reasoningEffort = normalizeReasoningEffort(v)
	}
	if v, ok := opts["mode"].(string); ok {
		a.mode = normalizePermissionMode(v)
	}
	if raw, ok := opts["allowed_tools"].([]any); ok {
		for _, t := range raw {
			if s, ok := t.(string); ok {
				a.allowedTools = append(a.allowedTools, s)
			}
		}
	}
	if v, ok := opts["grok_home"].(string); ok && strings.TrimSpace(v) != "" {
		a.grokHome = strings.TrimSpace(v)
	}

	// Verify the CLI exists (unless in isolation mode — skip like claudecode does).
	if _, err := exec.LookPath(a.cliBin); err != nil {
		return nil, fmt.Errorf("grokbuild: CLI %q not found in PATH: %w", a.cliBin, err)
	}

	return a, nil
}

func (a *Agent) Name() string { return "grokbuild" }

// StartSession creates a new Grok ACP session or loads an existing one.
//
// This method must NOT hold a.mu (not even RLock): newGrokSession → loadSession
// calls s.agent.SetWorkDir which acquires a.mu.Lock(). If StartSession held
// RLock, that would be a read→write upgrade deadlock — the Lock() blocks
// forever waiting for the RLock to release, but RLock won't release until
// newGrokSession returns, which won't happen because it's blocked on
// SetWorkDir. The agent pointer is stable (registered once at init), so no
// lock is needed here; individual field access inside newGrokSession uses the
// getter/setter methods which take their own locks.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	return newGrokSession(ctx, a, sessionID)
}

// ListSessions returns sessions from the local Grok session store
// ($GROK_HOME/sessions). This is the v1 on-disk discovery path (undeclared
// connections, §10). The catalog main line is FetchSessionList (managed ACP
// session/list subprocess, catalog_session_list.go) — routed only for connections
// that declare catalog_cursor_epoch_v2.
// Implementation: session_catalog.go.

func (a *Agent) Stop() error { return nil }

// SubscribeSessionEvents streams a session's live session/update notifications as
// core.Events (via convertSessionUpdate). It prefers the leader-socket subscriber
// (push, low-latency) when ~/.grok/leader.sock exists; otherwise it falls back to
// the updates.jsonl file tailer (poll). grok's leader socket only exists under
// use_leader=true (default inline embedded agent mode never creates it), so the
// file fallback is the path most users actually hit — and it works without any
// requirement on how grok was launched, since grok writes updates.jsonl in all
// modes. Both sources feed the same codec (convertSessionUpdate), so the downstream
// relay loop's turn-start synthesis / defer-idle logic is shared unchanged.
//
// Neither path spawns a leader, acquires the flock, or drives the session. The
// channel closes when the source disconnects / tails out or ctx is cancelled.
//
// grokHome is write-once (set in New), so reading it here without a.mu is safe.
func (a *Agent) SubscribeSessionEvents(ctx context.Context, sessionID, cwd string) (<-chan core.Event, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("grokbuild: SubscribeSessionEvents requires a sessionId")
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = a.GetWorkDir()
	}
	ch := make(chan core.Event, 32)
	forward := func(ev core.Event) {
		if ev.SessionID == "" {
			ev.SessionID = sessionID
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
	}

	socketPath := resolveLeaderSocket(a.grokHome)
	if _, err := os.Stat(socketPath); err != nil {
		// Leader socket absent (默认 inline 模式) —— fallback 到 updates.jsonl file tailer。
		slog.Info("grokbuild: leader socket absent, falling back to updates.jsonl file tailer",
			"session", sessionID, "socket", socketPath)
		tail := newUpdatesFileTailSubscriber(a.grokHome, sessionID)
		go func() {
			defer close(ch)
			if err := tail.Run(ctx, forward); err != nil {
				slog.Debug("grokbuild: updates file tailer ended", "session", sessionID, "error", err)
			}
		}()
		return ch, nil
	}

	sub := NewLeaderSubscriber(socketPath, sessionID, cwd)
	go func() {
		defer close(ch)
		slog.Info("grokbuild: leader subscriber starting", "session", sessionID, "socket", socketPath)
		if err := sub.Run(ctx, forward); err != nil {
			slog.Debug("grokbuild: leader subscriber ended", "session", sessionID, "error", err)
		}
	}()
	return ch, nil
}

// --- WorkDirSwitcher ---

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	a.workDir = dir
	a.mu.Unlock()
}

func (a *Agent) GetWorkDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workDir
}

// --- ModeSwitcher ---

func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	a.mode = normalizePermissionMode(mode)
	a.mu.Unlock()
}

func (a *Agent) GetMode() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mode
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Default", NameZh: "默认", Desc: "Ask for permission on each tool", DescZh: "每次工具调用都询问"},
		{Key: "acceptEdits", Name: "Accept Edits", NameZh: "接受编辑", Desc: "Auto-accept file edits", DescZh: "自动接受文件编辑"},
		{Key: "auto", Name: "Auto", NameZh: "自动", Desc: "Auto-approve most operations", DescZh: "自动批准大多数操作"},
		{Key: "dontAsk", Name: "Don't Ask", NameZh: "不询问", Desc: "Don't ask for any permission", DescZh: "不询问任何权限"},
		{Key: "bypassPermissions", Name: "Bypass Permissions", NameZh: "绕过权限", Desc: "Bypass all permission checks", DescZh: "绕过所有权限检查"},
		{Key: "plan", Name: "Plan", NameZh: "计划", Desc: "Plan mode — no execution", DescZh: "计划模式——不执行"},
	}
}

// --- ModelSwitcher ---

func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	a.model = model
	a.mu.Unlock()
}

func (a *Agent) GetModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return core.GetProviderModel(a.providers, a.activeIdx, a.model)
}

// configuredModels returns the model list pre-configured on the active provider
// (iOS-injected third-party Grok providers). Empty when no provider is set.
func (a *Agent) configuredModels() []core.ModelOption {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return core.GetProviderModels(a.providers, a.activeIdx)
}

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	if models := a.configuredModels(); len(models) > 0 {
		return models
	}
	// Grok CLI 走 ACP agent stdio（grok agent stdio），无 `grok models` 子命令（ACP v1 无标准
	// listModels），故不照搬 opencode 的 exec models 探测；custom provider 模型经
	// configuredModels 可见，无 provider 时回落默认 Grok 模型。详见 t3code-adoption-plan §5.1。
	return []core.ModelOption{
		{Name: "grok-4.5", Desc: "Grok 4.5"},
		{Name: "grok-4", Desc: "Grok 4"},
	}
}

// --- ReasoningEffortSwitcher ---

func (a *Agent) SetReasoningEffort(effort string) {
	a.mu.Lock()
	a.reasoningEffort = normalizeReasoningEffort(effort)
	a.mu.Unlock()
}

func (a *Agent) GetReasoningEffort() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reasoningEffort
}

func (a *Agent) AvailableReasoningEfforts() []string {
	return []string{"low", "medium", "high"}
}

// --- ProviderSwitcher ---

func (a *Agent) SetProviders(providers []core.ProviderConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providers = providers
}

func (a *Agent) SetActiveProvider(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if name == "" {
		a.activeIdx = -1
		slog.Info("grokbuild: provider cleared")
		return true
	}
	for i, p := range a.providers {
		if p.Name == name {
			a.activeIdx = i
			slog.Info("grokbuild: provider switched", "provider", name)
			return true
		}
	}
	return false
}

func (a *Agent) GetActiveProvider() *core.ProviderConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
		return nil
	}
	p := a.providers[a.activeIdx]
	return &p
}

func (a *Agent) ListProviders() []core.ProviderConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]core.ProviderConfig, len(a.providers))
	copy(result, a.providers)
	return result
}

// --- ToolAuthorizer ---

func (a *Agent) AddAllowedTools(tools ...string) error {
	a.mu.Lock()
	a.allowedTools = append(a.allowedTools, tools...)
	a.mu.Unlock()
	return nil
}

func (a *Agent) GetAllowedTools() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.allowedTools
}

// --- normalizers ---

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(mode) {
	case "", "default":
		return "default"
	case "acceptedits", "accept_edits":
		return "acceptEdits"
	case "auto":
		return "auto"
	case "dontask", "dont_ask":
		return "dontAsk"
	case "bypasspermissions", "bypass_permissions":
		return "bypassPermissions"
	case "plan":
		return "plan"
	default:
		return mode
	}
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(effort) {
	case "", "medium":
		return "medium"
	case "low":
		return "low"
	case "high":
		return "high"
	default:
		return strings.ToLower(effort)
	}
}

// gracefulStopTimeout is the time to wait for the process to exit after closing stdin.
const gracefulStopTimeout = 8 * time.Second
