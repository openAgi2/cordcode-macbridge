package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

func init() {
	core.RegisterAgent("codex", New)
}

// Agent drives OpenAI Codex CLI using `codex exec --json`.
//
// Modes (maps to Codex permission presets):
//   - "default":     explicit default permissions
//   - "auto-review": Codex auto-review preset
//   - "full-access": full access, no sandbox
//   - "custom":      use config.toml without overriding approval/sandbox
type Agent struct {
	workDir         string
	model           string
	reasoningEffort string
	mode            string // "default" | "auto-review" | "full-access" | "custom"
	backend         string // "exec" | "app_server"
	appServerURL    string
	appServerURLSet bool
	codexHome       string
	cliBin          string   // CLI binary name, default "codex"
	cliExtraArgs    []string // extra args parsed from cli_path after the binary
	providers       []core.ProviderConfig
	activeIdx       int // -1 = no provider set
	sessionEnv      []string
	mu              sync.RWMutex

	// session list 缓存：walk 目录只做 stat，只重解析 mtime 变了的文件
	sessionCache sessionListCache

	// pinStore persists MacBridge-owned session pin (置顶) metadata for SessionPinner.
	// Injected via opts["pin_store"] from go-bridge main; nil in unit tests that do not
	// exercise pinning.
	pinStore *pinstore.Store
}

func New(opts map[string]any) (core.Agent, error) {
	workDir, _ := opts["work_dir"].(string)
	if workDir == "" {
		workDir = "."
	}
	model, _ := opts["model"].(string)
	reasoningEffort, _ := opts["reasoning_effort"].(string)
	mode, _ := opts["mode"].(string)
	backend, _ := opts["backend"].(string)
	appServerURL, _ := opts["app_server_url"].(string)
	appServerURL = strings.TrimSpace(appServerURL)
	_, appServerURLKeyPresent := opts["app_server_url"]
	appServerURLSet := appServerURLKeyPresent && appServerURL != ""
	codexHome, _ := opts["codex_home"].(string)
	mode = normalizeMode(mode)
	backend = normalizeBackend(backend)

	if appServerURL == "" {
		appServerURL = "ws://127.0.0.1:3845"
	}

	// cli_path allows overriding the binary, e.g. "omx" or "omx --flag val"
	cliBin := "codex"
	var cliExtraArgs []string
	if cliPath, _ := opts["cli_path"].(string); strings.TrimSpace(cliPath) != "" {
		parts := strings.Fields(cliPath)
		cliBin = parts[0]
		if len(parts) > 1 {
			cliExtraArgs = parts[1:]
		}
	}

	// Codex CLI is only spawned in exec mode; app-server mode connects via a
	// WebSocket URL (newAppServerSession) and never uses the CLI binary. Require
	// the CLI only when it will actually be invoked.
	if backend != "app_server" {
		if _, err := exec.LookPath(cliBin); err != nil {
			return nil, fmt.Errorf("codex: %q CLI not found in PATH, install with: npm install -g @openai/codex", cliBin)
		}
	}

	return &Agent{
		workDir:         workDir,
		model:           model,
		reasoningEffort: normalizeReasoningEffort(reasoningEffort),
		mode:            mode,
		backend:         backend,
		appServerURL:    appServerURL,
		appServerURLSet: appServerURLSet,
		codexHome:       strings.TrimSpace(codexHome),
		cliBin:          cliBin,
		cliExtraArgs:    cliExtraArgs,
		activeIdx:       -1,
		pinStore:        pinstore.FromOpts(opts),
	}, nil
}

func normalizeBackend(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "app-server", "app_server", "appserver", "ws":
		return "app_server"
	default:
		return "exec"
	}
}

func normalizeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "custom", "config", "config.toml", "toml":
		return "custom"
	case "auto-review", "autoreview", "auto_review", "auto-edit", "autoedit", "auto_edit", "edit":
		return "auto-review"
	case "full-access", "fullaccess", "full_access", "full-auto", "fullauto", "full_auto", "auto", "yolo", "bypass", "bypasspermissions", "bypass-permissions", "dangerously-bypass":
		return "full-access"
	default:
		return "default"
	}
}

func normalizeReasoningEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "low":
		return "low"
	case "medium", "med":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "x-high", "very-high":
		return "xhigh"
	default:
		return ""
	}
}

func (a *Agent) Name() string { return "codex" }

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workDir = dir
	slog.Info("codex: work_dir changed", "work_dir", dir)
}

func (a *Agent) GetWorkDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workDir
}

func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
	slog.Info("codex: model changed", "model", model)
}

func (a *Agent) GetModel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return core.GetProviderModel(a.providers, a.activeIdx, a.model)
}

func (a *Agent) SetReasoningEffort(effort string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reasoningEffort = normalizeReasoningEffort(effort)
	slog.Info("codex: reasoning effort changed", "reasoning_effort", a.reasoningEffort)
}

func (a *Agent) GetReasoningEffort() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reasoningEffort
}

func (a *Agent) AvailableReasoningEfforts() []string {
	return []string{"low", "medium", "high", "xhigh"}
}

func (a *Agent) configuredModels() []core.ModelOption {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return core.GetProviderModels(a.providers, a.activeIdx)
}

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	if models := a.configuredModels(); len(models) > 0 {
		return models
	}
	if models := readCodexCachedModels(); len(models) > 0 {
		return models
	}
	if models := a.fetchModelsFromAPI(ctx); len(models) > 0 {
		return models
	}
	return []core.ModelOption{
		{Name: "o4-mini", Desc: "O4 Mini (fast reasoning)"},
		{Name: "o3", Desc: "O3 (most capable reasoning)"},
		{Name: "gpt-4.1", Desc: "GPT-4.1 (balanced)"},
		{Name: "gpt-4.1-mini", Desc: "GPT-4.1 Mini (fast)"},
		{Name: "gpt-4.1-nano", Desc: "GPT-4.1 Nano (fastest)"},
		{Name: "codex-mini-latest", Desc: "Codex Mini (code-optimized)"},
	}
}

var openaiChatModels = map[string]bool{
	"o4-mini": true, "o3": true, "o3-mini": true, "o1": true, "o1-mini": true,
	"gpt-4.1": true, "gpt-4.1-mini": true, "gpt-4.1-nano": true,
	"gpt-4o": true, "gpt-4o-mini": true,
	"codex-mini-latest": true,
}

func (a *Agent) fetchModelsFromAPI(ctx context.Context) []core.ModelOption {
	a.mu.Lock()
	apiKey := ""
	baseURL := ""
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) {
		apiKey = a.providers[a.activeIdx].APIKey
		baseURL = a.providers[a.activeIdx].BaseURL
	}
	a.mu.Unlock()

	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/v1/models", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("codex: failed to fetch models", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}

	var models []core.ModelOption
	for _, m := range result.Data {
		if openaiChatModels[m.ID] {
			models = append(models, core.ModelOption{Name: m.ID})
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models
}

func readCodexCachedModels() []core.ModelOption {
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		codexHome = filepath.Join(home, ".codex")
	}
	path := filepath.Join(codexHome, "models_cache.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var payload struct {
		Models []struct {
			Slug           string `json:"slug"`
			DisplayName    string `json:"display_name"`
			Description    string `json:"description"`
			Visibility     string `json:"visibility"`
			SupportedInAPI bool   `json:"supported_in_api"`
		} `json:"models"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil
	}

	var models []core.ModelOption
	seen := make(map[string]struct{}, len(payload.Models))
	for _, m := range payload.Models {
		name := strings.TrimSpace(m.Slug)
		if name == "" {
			name = strings.TrimSpace(m.DisplayName)
		}
		if name == "" {
			continue
		}
		if m.Visibility != "" && m.Visibility != "list" {
			continue
		}
		if !m.SupportedInAPI {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		displayName := strings.TrimSpace(m.DisplayName)
		if displayName == "" {
			displayName = name
		}
		models = append(models, core.ModelOption{
			Name: name,
			Desc: displayName,
		})
	}
	return models
}

func (a *Agent) SetSessionEnv(env []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionEnv = env
}

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.Lock()
	mode := a.mode
	model := a.model
	reasoningEffort := a.reasoningEffort
	backend := a.backend
	appServerURL := a.appServerURL
	appServerURLSet := a.appServerURLSet
	codexHome := a.codexHome
	cliBin := a.cliBin
	cliExtraArgs := a.cliExtraArgs
	workDir := a.workDir
	extraEnv := a.providerEnvLocked()
	extraEnv = append(extraEnv, a.sessionEnv...)
	var baseURL string
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) {
		if m := a.providers[a.activeIdx].Model; m != "" {
			model = m
		}
		baseURL = a.providers[a.activeIdx].BaseURL
	}
	provName, provAPIKey, provWireAPI, provHeaders := a.activeProviderCodexConfig()
	a.mu.Unlock()

	if provName != "" {
		if err := ensureCodexProviderConfig(codexHome, provName, baseURL, provWireAPI, provHeaders); err != nil {
			slog.Warn("codex: failed to write provider config", "provider", provName, "error", err)
		}
		if err := ensureCodexAuth(codexHome, provAPIKey); err != nil {
			slog.Warn("codex: failed to write auth.json", "provider", provName, "error", err)
		}
	}

	if backend == "app_server" {
		transportMode := appServerSessionTransport(appServerURLSet)
		return newAppServerSession(ctx, appServerURL, transportMode, workDir, model, reasoningEffort, mode, sessionID, extraEnv, codexHome)
	}
	if codexHome != "" {
		extraEnv = append(extraEnv, "CODEX_HOME="+codexHome)
	}

	return newCodexSession(ctx, cliBin, cliExtraArgs, workDir, model, reasoningEffort, mode, sessionID, baseURL, extraEnv, provName)
}

func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	a.mu.RLock()
	codexHome := a.codexHome
	a.mu.RUnlock()
	return a.sessionCache.list(ctx, codexHome)
}

func (a *Agent) GetSessionHistory(_ context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	a.mu.RLock()
	codexHome := a.codexHome
	a.mu.RUnlock()
	return getSessionHistory(sessionID, codexHome, limit)
}

func (a *Agent) GetSessionContextUsage(_ context.Context, sessionID string) (*core.ContextUsage, error) {
	a.mu.RLock()
	codexHome := a.codexHome
	extraEnv := a.providerEnvLocked()
	extraEnv = append(extraEnv, a.sessionEnv...)
	a.mu.RUnlock()

	if codexHome != "" {
		extraEnv = append(extraEnv, "CODEX_HOME="+codexHome)
	}
	usage, _, err := loadContextUsageFromRollout(extraEnv, sessionID, "")
	if err != nil {
		return nil, err
	}
	return usage, nil
}

func (a *Agent) DeleteSession(_ context.Context, sessionID string) error {
	a.mu.RLock()
	codexHome := a.codexHome
	a.mu.RUnlock()
	path := findSessionFile(sessionID, codexHome)
	if path == "" {
		return fmt.Errorf("session file not found: %s", sessionID)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	// Drop bridge-owned pin metadata (置顶). Best-effort: a leftover entry would be pruned
	// on the next list_pinned_sessions anyway.
	a.cleanupPin(sessionID)
	return nil
}

func appServerSessionTransport(appServerURLSet bool) string {
	if appServerURLSet {
		return appServerTransportWebSocket
	}
	return appServerTransportStdio
}

// Stop is a no-op for now. Per-session Close() already reaps each codex
// process group on shutdown (Handlers.Shutdown → AgentSession.Close).
// TODO: process-group stop at the Agent level.
func (a *Agent) Stop() error { return nil }

// SetMode changes the approval mode for future sessions.
func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = normalizeMode(mode)
	slog.Info("codex: approval mode changed", "mode", a.mode)
}

func (a *Agent) GetMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

func (a *Agent) WorkspaceAgentOptions() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()

	opts := map[string]any{
		"mode":    a.mode,
		"backend": a.backend,
	}
	if a.model != "" {
		opts["model"] = a.model
	}
	if a.reasoningEffort != "" {
		opts["reasoning_effort"] = a.reasoningEffort
	}
	if a.appServerURLSet && a.appServerURL != "" {
		opts["app_server_url"] = a.appServerURL
	}
	if a.codexHome != "" {
		opts["codex_home"] = a.codexHome
	}
	return opts
}

// ── SkillProvider implementation ──────────────────────────────

func (a *Agent) SkillDirs() []string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	return codexSkillDirs(absDir, a.codexHome)
}

// ── ContextCompressor implementation ──────────────────────────

// CompressCommand returns "" because Codex native slash commands (/compact, /clear)
// are not reliably executed in exec/resume mode — they may be treated as plain text.
// See: https://github.com/openAgi2/cordcode-macbridge/issues/378
func (a *Agent) CompressCommand() string { return "" }

func codexSkillDirs(workDir, explicitCodexHome string) []string {
	homeDir, _ := os.UserHomeDir()
	codexHome := strings.TrimSpace(explicitCodexHome)
	if codexHome == "" {
		codexHome = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if codexHome == "" && homeDir != "" {
		codexHome = filepath.Join(homeDir, ".codex")
	}

	projectDirs := walkUpCodexProjectSkillDirs(workDir, homeDir)
	userDirs := make([]string, 0, 2)
	if codexHome != "" {
		userDirs = append(userDirs, filepath.Join(codexHome, "skills"))
	}
	if homeDir != "" {
		userDirs = append(userDirs, filepath.Join(homeDir, ".agents", "skills"))
	}
	return uniqueCodexSkillDirs(append(projectDirs, userDirs...))
}

func walkUpCodexProjectSkillDirs(workDir, homeDir string) []string {
	current := filepath.Clean(workDir)
	homeDir = filepath.Clean(homeDir)
	stopAt := findCodexProjectRoot(current)

	var dirs []string
	for {
		if homeDir != "" && sameCodexPath(current, homeDir) {
			break
		}
		dirs = append(dirs,
			filepath.Join(current, ".agents", "skills"),
			filepath.Join(current, ".codex", "skills"),
		)
		if stopAt != "" && sameCodexPath(current, stopAt) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return uniqueCodexSkillDirs(dirs)
}

func findCodexProjectRoot(start string) string {
	current := filepath.Clean(start)
	for {
		for _, marker := range []string{".git", ".jj"} {
			if _, err := os.Stat(filepath.Join(current, marker)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func sameCodexPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func uniqueCodexSkillDirs(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

// ── MemoryFileProvider implementation ─────────────────────────

func (a *Agent) ProjectMemoryFile() string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	return filepath.Join(absDir, "AGENTS.md")
}

func (a *Agent) GlobalMemoryFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(homeDir, ".codex")
	}
	return filepath.Join(codexHome, "AGENTS.md")
}

// ── ProviderSwitcher implementation ──────────────────────────

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
		slog.Info("codex: provider cleared")
		return true
	}
	for i, p := range a.providers {
		if p.Name == name {
			a.activeIdx = i
			slog.Info("codex: provider switched", "provider", name)
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

func (a *Agent) providerEnvLocked() []string {
	if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
		return nil
	}
	p := a.providers[a.activeIdx]
	var env []string
	if p.APIKey != "" {
		env = append(env, "OPENAI_API_KEY="+p.APIKey)
	}
	if p.BaseURL != "" {
		env = append(env, "OPENAI_BASE_URL="+p.BaseURL)
	}
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// activeProviderCodexConfig returns Codex-specific config for the active provider.
// Returns non-empty name when the provider has codex config (wire_api, headers)
// OR when it has a BaseURL (third-party provider needing auth.json).
func (a *Agent) activeProviderCodexConfig() (name string, apiKey string, wireAPI string, headers map[string]string) {
	if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
		return
	}
	p := a.providers[a.activeIdx]
	hasCodexConfig := p.CodexWireAPI != "" || len(p.CodexHTTPHeaders) > 0
	isThirdParty := p.BaseURL != "" && p.APIKey != ""
	if !hasCodexConfig && !isThirdParty {
		return
	}
	return p.Name, p.APIKey, p.CodexWireAPI, p.CodexHTTPHeaders
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Default permissions", NameZh: "默认权限", Desc: "Run commands in a sandbox", DescZh: "在沙盒中运行命令"},
		{Key: "auto-review", Name: "Auto review", NameZh: "自动审查", Desc: "Automatically review permission requests", DescZh: "自动审查授权请求"},
		{Key: "full-access", Name: "Full access", NameZh: "完全访问权限", Desc: "Full computer access; higher risk", DescZh: "完全访问计算机（风险较高）"},
		{Key: "custom", Name: "Custom (config.toml)", NameZh: "自定义 (config.toml)", Desc: "Use permissions defined in config.toml", DescZh: "Codex 使用 config.toml 中定义的权限"},
	}
}
