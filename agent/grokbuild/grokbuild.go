package grokbuild

// Grok Build CLI driver for MacBridge.
// Uses `grok agent stdio` (ACP v1 JSON-RPC over stdin/stdout) to communicate
// with the Grok Build CLI. See docs/2026-07-12-grok-driver-design.md for the
// full design and docs/2026-07-12-grok-cli-compatibility-evidence.md for Gate 0
// evidence.

import (
	"context"
	"fmt"
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
var _ core.ReasoningEffortSwitcher = (*Agent)(nil)
var _ core.ToolAuthorizer = (*Agent)(nil)
var _ core.HistoryProvider = (*Agent)(nil)

// Agent implements core.Agent for the Grok Build CLI.
type Agent struct {
	workDir         string
	cliBin          string
	cliExtraArgs    []string
	model           string
	reasoningEffort string
	mode            string
	allowedTools    []string
	// grokHome overrides ~/.grok / GROK_HOME for session catalog (tests).
	grokHome string
	mu       sync.RWMutex
}

func init() {
	core.RegisterAgent("grokbuild", New)
}

// New creates a Grok Build agent from the given options map.
func New(opts map[string]any) (core.Agent, error) {
	a := &Agent{
		workDir: ".",
		mode:    "default",
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
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return newGrokSession(ctx, a, sessionID)
}

// ListSessions returns sessions from the local Grok session store
// ($GROK_HOME/sessions). ACP session/list is not available on current CLI
// versions (Method not found); the on-disk catalog is the v1 discovery path.
// Implementation: session_catalog.go.

func (a *Agent) Stop() error { return nil }

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
	return a.model
}

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
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
