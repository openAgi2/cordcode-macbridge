package dsh

// DeepSeek Harness (DSH) driver for MacBridge.
//
// Speaks the DSH SDK JSON-RPC 2.0 stdio protocol (NOT ACP): 3 requests +
// 4 notifications. Full design: docs/2026-08-13-dsh-driver-design.md
// (v13, round12 APPROVE). Wire evidence: scripts/dsh-gate0/dumps/.

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

//go:embed cordis.yml
var embeddedCordisYML []byte

// dshProviderRoute is the LLM provider route mounted by the driver
// composition (driver-cordis.yml → @deepseek-ai/dsh-llm-deepseek). A custom
// endpoint is injected via DEEPSEEK_BASE_URL, not a different route name.
const dshProviderRoute = "deepseek-official"

const (
	defaultCLIBin  = "dsh-jsonrpc-agent"
	defaultModel   = "deepseek-chat"
	dshDataSubdir  = ".cccode-macbridge/dsh"
	cordisYMLName  = "cordis.yml"
	sessionsSubdir = "sessions"
)

var _ core.Agent = (*Agent)(nil)
var _ core.WireDescriptorProvider = (*Agent)(nil)
var _ core.WorkDirSwitcher = (*Agent)(nil)
var _ core.ModeSwitcher = (*Agent)(nil)
var _ core.ModelSwitcher = (*Agent)(nil)
var _ core.ProviderSwitcher = (*Agent)(nil)
var _ core.DiagnosticsProvider = (*Agent)(nil)

func init() {
	core.RegisterAgent("dsh", New)
}

// Agent implements core.Agent for the DeepSeek Harness runtime.
type Agent struct {
	workDir      string
	cliBin       string
	cliExtraArgs []string
	configPath   string
	model        string
	mode         string
	providers    []core.ProviderConfig
	activeIdx    int // -1 = no provider set

	// receiptWait bounds the session/prompt enqueue-receipt wait. Defaults to
	// promptReceiptWait; fault-injection tests shrink it.
	receiptWait time.Duration

	// runtimeSource labels how the runtime binary was found (diagnostics).
	runtimeSource string

	// Node-script launch mode (set when nodeBin != ""): the runtime runs as
	// `node [preArgs...] <scriptPath>` with cwd=spawnDir. Forms:
	//   route 2 (user-global dsh): vendored demo lib/bin.js, cwd = the
	//     driver's shadow-tree dir; bare plugins resolve beside cordis.yml
	//     (vendored SDK layer real + family symlinked to the user's tree).
	//   route 5 (dev checkout, opt-in): `--import tsx <bin.ts>`,
	//     cwd = checkout root (the gate0-verified launch shape).
	srcRoot       string // set only for source-checkout mode
	nodeBin       string
	scriptPath    string
	spawnDir      string
	scriptPreArgs []string
	configViaEnv  bool // route 2: pass the config via DSH_CORDIS_CONFIG
	globalDsh     *globalDshInstall

	mu sync.RWMutex
}

// New creates a DSH agent from the given options map.
//
//	opts["work_dir"]    workspace root (default ".")
//	opts["cli_path"]    dsh-jsonrpc-agent binary (default PATH lookup);
//	                    extra args after the first field are forwarded
//	opts["config_path"] explicit cordis.yml; when empty the driver
//	                    materializes its embedded composition under
//	                    <work_dir>/.cccode-macbridge/dsh/cordis.yml
//	opts["model"]       model name (default deepseek-chat)
//	opts["mode"]        DSH permission preset (default workspace-write)
func New(opts map[string]any) (core.Agent, error) {
	a := &Agent{
		workDir:   ".",
		cliBin:    defaultCLIBin,
		model:     defaultModel,
		mode:      "workspace-write",
		activeIdx: -1,
	}

	if v, ok := opts["work_dir"].(string); ok && v != "" {
		a.workDir = v
	}
	if v, ok := opts["cli_path"].(string); ok && v != "" {
		fields := strings.Fields(v)
		a.cliBin = fields[0]
		if len(fields) > 1 {
			a.cliExtraArgs = fields[1:]
		}
	} else if root, ok := opts["dsh_root"].(string); ok && strings.TrimSpace(root) != "" {
		// Explicit source-checkout root.
		if err := a.useSourceCheckout(strings.TrimSpace(root)); err != nil {
			return nil, err
		}
	} else {
		// Probe-only discovery (directive v2): explicit installs → user-global
		// npm dsh (the real-user form, served via the vendored SDK layer +
		// shadow tree) → pip wheel → nvm → dev checkout (opt-in env).
		if rt := discoverProbeOnly(); rt != nil {
			a.runtimeSource = rt.source
			a.nodeBin = rt.nodeBin
			switch {
			case rt.exe != "":
				a.cliBin = rt.exe
				slog.Info("dsh: runtime probed", "bin", filepath.Base(rt.exe), "source", rt.source)
			case rt.global != nil:
				// Route 2: materialize the shadow tree beside our cordis.yml.
				binJS, err := ensureShadowTree(a.shadowBaseDir(), rt.global)
				if err != nil {
					return nil, err
				}
				a.scriptPath = binJS
				a.spawnDir = a.shadowBaseDir()
				a.configViaEnv = true
				a.globalDsh = rt.global
				slog.Info("dsh: runtime via user-global npm dsh",
					"dsh", rt.global.dshVersion, "app-boot", rt.global.appBootVersion,
					"tree", rt.global.dshDir)
			case rt.srcRoot != "":
				a.srcRoot = rt.srcRoot
				a.scriptPath = rt.script
				a.spawnDir = rt.srcRoot
				a.scriptPreArgs = []string{"--import", "tsx"}
				slog.Info("dsh: runtime via dev-only source checkout", "root", rt.srcRoot)
			}
		} else {
			return nil, fmt.Errorf("dsh: DeepSeek Harness not found — install it with `npm i -g @deepseek-ai/dsh` (then `dsh web` once to save the API key), or pip install deepseek-harness-runtime-bin; nothing is installed on your behalf")
		}
	}

	// The runtime requires an explicit config argument (§1-2 强制 config).
	if v, ok := opts["config_path"].(string); ok && strings.TrimSpace(v) != "" {
		a.configPath = strings.TrimSpace(v)
	} else {
		path, err := a.materializeEmbeddedConfig()
		if err != nil {
			return nil, fmt.Errorf("dsh: materialize driver config: %w", err)
		}
		a.configPath = path
	}
	if _, err := os.Stat(a.configPath); err != nil {
		return nil, fmt.Errorf("dsh: config %q unavailable: %w", a.configPath, err)
	}

	// Exe-mode runtime validation (node-script modes validated their own
	// facts above): an explicit cli_path must actually resolve; discovery
	// already proved the rest.
	if a.nodeBin == "" {
		if _, err := exec.LookPath(a.cliBin); err != nil {
			if _, statErr := os.Stat(a.cliBin); statErr != nil {
				return nil, fmt.Errorf("dsh: runtime %q not found: %w", a.cliBin, err)
			}
		}
	}

	return a, nil
}

// shadowBaseDir is the driver's data dir: <workDir>/.cccode-macbridge/dsh —
// holds cordis.yml, the session root, and the route-2 shadow node_modules.
func (a *Agent) shadowBaseDir() string {
	return filepath.Join(a.workDir, dshDataSubdir)
}

// materializeEmbeddedConfig writes the driver's composition (design §10,
// identical to scripts/dsh-gate0/driver-cordis.yml) into the workspace-local
// dsh data dir. Rewriting each construction keeps the file in sync with the
// embedded source; the runtime reads it once per spawn.
func (a *Agent) materializeEmbeddedConfig() (string, error) {
	dir := filepath.Join(a.workDir, dshDataSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, cordisYMLName)
	if err := os.WriteFile(path, embeddedCordisYML, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// DiscoverRuntime exposes the probe-only runtime discovery so the bridge
// descriptor status reflects the same acquisition routes the driver spawns
// from (one truth source for hello_ack availability and StartSession). The
// returned path is the direct executable or the launch script; the
// user-global route reports the demo bin.js once the shadow tree exists.
func DiscoverRuntime() (string, string) {
	rt := discoverProbeOnly()
	if rt == nil {
		return "", ""
	}
	if rt.exe != "" {
		return rt.exe, rt.source
	}
	return rt.script, rt.source
}

// useSourceCheckout validates a source checkout and records its spawn facts.
func (a *Agent) useSourceCheckout(root string) error {
	script := filepath.Join(root, jsonrpcBinRel)
	if !fileExists(script) || !fileExists(filepath.Join(root, "node_modules", ".bin", "tsx")) {
		return fmt.Errorf("dsh: %q is not a usable deepseek-harness checkout (missing %s or installed node_modules)", root, jsonrpcBinRel)
	}
	node := resolveNodeBinary()
	if node == "" {
		return fmt.Errorf("dsh: source checkout %q found but no node binary", root)
	}
	a.srcRoot = root
	a.nodeBin, a.scriptPath, a.spawnDir = node, script, root
	a.scriptPreArgs = []string{"--import", "tsx"}
	a.runtimeSource = "source-checkout:explicit"
	return nil
}

func (a *Agent) Name() string { return "dsh" }

// ErrSessionNotResumable: the session id exists in the user's harness store
// but no live process holds it — the pinned SDK has no cross-process resume
// (session/prompt on a known id lazily CREATES; persistence then refuses to
// rematerialize an existing log, source-verified 2026-08-16 §2.1). Failing
// here is deterministic and honest; the go-bridge preflight maps it to the
// wire error session_resume_not_supported.
var ErrSessionNotResumable = errors.New("dsh: session exists on disk but the pinned SDK has no cross-process resume")

// StartSession spawns one runtime process per session (§1-2 process model,
// mirroring grokbuild's process-group handling). A requested id that already
// exists in the store is a dead session — resume is SDK-blocked, so it fails
// fast instead of erroring at the first session/prompt materialization.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	if strings.TrimSpace(sessionID) != "" && StoreHasSession(sessionID) {
		return nil, ErrSessionNotResumable
	}
	return newDshSession(ctx, a, sessionID)
}

// ListSessions scans the user's harness session store (2026-08-16 store
// bridge design §4.2). The store IS the user's own dsh storage: phone-created
// sessions land there (624c6a4 form) and dsh web sessions become visible on
// iOS with no bridge-private catalog. Delegated subagent tasks stay hidden.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	store := openDshSessionStore()
	sessions, err := store.scanSessions()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	infos := make([]core.AgentSessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		if sess.Subagent {
			continue
		}
		infos = append(infos, core.AgentSessionInfo{
			ID:         sess.ID,
			Summary:    sessionTitle(sess.Path, sess.Plain),
			Directory:  sess.Cwd,
			ModifiedAt: time.Unix(sess.ModTime, 0),
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ModifiedAt.After(infos[j].ModifiedAt) })
	return infos, nil
}

// GetRichSessionHistory implements core.RichHistoryProvider over the harness
// store log (design §4.3) — the file-backed counterpart the SSV2 pathless
// cold-hydrate and get_session_messages both consume.
func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sess, ok := openDshSessionStore().resolveSessionFile(sessionID)
	if !ok {
		return nil, fmt.Errorf("dsh: session %q not found in harness store", sessionID)
	}
	return readRichHistory(sess, limit)
}

func (a *Agent) Stop() error { return nil }

// --- WireDescriptor (§6.2 self-description / §8 capability) ---

func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:        "deepseek", // iOS fromWireKind case "deepseek" → BackendKind.deepSeek
		DisplayName: "DeepSeek",
		// One private process per session: there is no external writer that
		// could drive a turn outside the driver, so no external-turn polling.
		LiveEventModel:              core.LiveEventSessionProcess,
		RequiresExternalTurnPolling: false,
		// §8/§3.9: DSH declares text only — NOT image, NOT file. The
		// go-bridge attachment gate reads this positive declaration.
		StaticCapabilities: []string{"text"},
	}
}

// --- env ---

// buildProcessEnv assembles the subprocess environment (§1-2): the runtime
// allowlist base plus DEEPSEEK_API_KEY / DEEPSEEK_BASE_URL (data-plane
// provider credentials from the active ProviderSwitcher config) and the
// driver-injected DSH_CWD / DSH_SESSION_ROOT / DSH_PERMISSION_MODE. Built via
// core.BuildAgentEnv so control-plane secrets can never ride along.
func (a *Agent) buildProcessEnv() []string {
	a.mu.RLock()
	workDir := a.workDir
	mode := a.mode
	providerKey, providerBaseURL := "", ""
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) {
		providerKey = a.providers[a.activeIdx].APIKey
		providerBaseURL = a.providers[a.activeIdx].BaseURL
	}
	a.mu.RUnlock()

	// Credential layering (product hardening): an explicit MacBridge provider
	// key wins; otherwise fall back to the harness's own user-level store
	// (~/.dsh/.credentials.yaml written by the dsh Web UI, then ~/.dsh/.env).
	// Injected env ranks first in every DSH trust layering, so this is
	// semantically the harness resolving its own credentials.
	var providerEnv []string
	if providerKey != "" {
		providerEnv = append(providerEnv, "DEEPSEEK_API_KEY="+providerKey)
	} else if h := discoverHarnessCredentials(); h.APIKey != "" {
		providerEnv = append(providerEnv, "DEEPSEEK_API_KEY="+h.APIKey)
		slog.Debug("dsh: using harness-stored DeepSeek credential", "source", h.Source)
	}
	if providerBaseURL != "" {
		providerEnv = append(providerEnv, "DEEPSEEK_BASE_URL="+providerBaseURL)
	} else if h := discoverHarnessCredentials(); h.BaseURL != "" {
		providerEnv = append(providerEnv, "DEEPSEEK_BASE_URL="+h.BaseURL)
	}

	// 2026-08-16 owner 决策：DSH 会话写入用户 harness 默认存储（$DSH_HOME/sessions，
	// 默认 ~/.dsh/sessions）——CordCode 起的会话直接出现在 dsh web 的会话列表并可在
	// Mac 端续聊（初衷「双向接力」前半）。MacBridge 私有 session 目录废止；仅当 HOME
	// 解析彻底失败时防御性回退私有目录，绝不以相对路径散写 cwd。
	home := dshHome()
	sessionRoot := filepath.Join(home, sessionsSubdir)
	if home == "" {
		sessionRoot = filepath.Join(workDir, dshDataSubdir, sessionsSubdir)
	}
	_ = os.MkdirAll(sessionRoot, 0o755)

	driverEnv := []string{
		"DSH_CWD=" + workDir,
		"DSH_SESSION_ROOT=" + sessionRoot,
		"DSH_PERMISSION_MODE=" + mode,
	}
	// Forward a custom harness home so the runtime's own $DSH_HOME/.env
	// fallback layer resolves the same directory the driver read credentials
	// from. (Default ~/.dsh needs no forwarding — HOME is allowlisted.)
	if custom := strings.TrimSpace(os.Getenv("DSH_HOME")); custom != "" {
		driverEnv = append(driverEnv, "DSH_HOME="+custom)
	}

	base := core.FilterEnvToAllowlist(os.Environ(), core.AgentEnvRuntimeAllowlist())
	return core.BuildAgentEnv(base, providerEnv, driverEnv)
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

// --- ModeSwitcher (DSH permission presets, §3.5) ---

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
		{Key: "read-only", Name: "Read Only", NameZh: "只读", Desc: "Sandbox restricts operations to reads", DescZh: "沙箱限制为只读操作"},
		{Key: "workspace-write", Name: "Workspace Write", NameZh: "工作区可写", Desc: "Writes allowed under the session workspace; approvals ask", DescZh: "允许写入会话工作区；需审批时询问"},
		{Key: "danger-full-access", Name: "Full Access", NameZh: "完全访问", Desc: "No sandbox restrictions; approvals never asked", DescZh: "无沙箱限制；不询问审批"},
	}
}

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "workspace-write", "workspacewrite", "workspace_write":
		return "workspace-write"
	case "read-only", "readonly", "read_only":
		return "read-only"
	case "danger-full-access", "dangerfullaccess", "danger_full_access", "full-access":
		return "danger-full-access"
	default:
		return "workspace-write"
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

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	if models := core.GetProviderModels(a.providers, a.activeIdx); len(models) > 0 {
		a.mu.RLock()
		defer a.mu.RUnlock()
		return models
	}
	return []core.ModelOption{
		{Name: "deepseek-chat", Desc: "DeepSeek Chat"},
		{Name: "deepseek-reasoner", Desc: "DeepSeek Reasoner"},
	}
}

// --- ProviderSwitcher (data-plane credentials for the runtime) ---

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
		return true
	}
	for i, p := range a.providers {
		if p.Name == name {
			a.activeIdx = i
			slog.Info("dsh: provider switched", "provider", name)
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
	out := make([]core.ProviderConfig, len(a.providers))
	copy(out, a.providers)
	return out
}

// --- DiagnosticsProvider ---

func (a *Agent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	report := &core.DiagnosticReport{Results: []core.DiagnosticResult{}, OverallStatus: "ok"}
	notify := func(r core.DiagnosticResult) {
		report.Results = append(report.Results, r)
		progress(core.DiagnosticProgress{CheckID: r.ID, Status: r.Status, Message: r.Name})
	}

	a.mu.RLock()
	cliBin, configPath := a.cliBin, a.configPath
	apiKeySet := false
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) && a.providers[a.activeIdx].APIKey != "" {
		apiKeySet = true
	}
	a.mu.RUnlock()

	if _, err := exec.LookPath(cliBin); err != nil {
		notify(core.DiagnosticResult{
			ID: "dsh-runtime", Name: "dsh-jsonrpc-agent runtime", Status: "failed",
			Message:       fmt.Sprintf("runtime %q not found in PATH", cliBin),
			Severity:      "error",
			FixSuggestion: "Install the DeepSeek Harness runtime (dsh-jsonrpc-agent) and ensure it is on PATH",
		})
		report.OverallStatus = "failed"
	} else {
		notify(core.DiagnosticResult{ID: "dsh-runtime", Name: "dsh-jsonrpc-agent runtime", Status: "passed", Message: "runtime found in PATH"})
	}

	if _, err := os.Stat(configPath); err != nil {
		notify(core.DiagnosticResult{
			ID: "dsh-config", Name: "Driver composition (cordis.yml)", Status: "failed",
			Message:  fmt.Sprintf("config unavailable: %v", err),
			Severity: "error",
		})
		report.OverallStatus = "failed"
	} else {
		notify(core.DiagnosticResult{ID: "dsh-config", Name: "Driver composition (cordis.yml)", Status: "passed", Message: configPath})
	}

	if !apiKeySet {
		notify(core.DiagnosticResult{
			ID: "dsh-api-key", Name: "DeepSeek API key", Status: "warning",
			Message:       "no active provider carries a DeepSeek API key; turns will fail authentication",
			Severity:      "warning",
			FixSuggestion: "Configure the DeepSeek provider API key in backend provider settings",
		})
		if report.OverallStatus == "ok" {
			report.OverallStatus = "warning"
		}
	} else {
		notify(core.DiagnosticResult{ID: "dsh-api-key", Name: "DeepSeek API key", Status: "passed", Message: "API key configured"})
	}

	return report, nil
}
