// Package opencodeweb is the CordCode backend for the official
// `opencode serve` HTTP/SSE API (design
// docs/2026-08-18-opencode-web-backend-design.md).
//
// It is a pure client: it never binds a port, never spawns the opencode
// binary, and never reads the serve storage directly. Lists, history, context
// usage, sends, models, activity, and permissions all come from the official
// HTTP surface. The legacy hybrid package stays untouched and un-imported.
package opencodeweb

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

// BackendID is the go-bridge driver id (drivers flag / hello_ack backends[].id).
const BackendID = "opencode-web"

// WireKind is the backend kind advertised on the wire. It intentionally equals
// the driver id here (unlike dsh-web's deepseek-web split): iOS maps
// "opencode-web" to BackendKind.openCodeWeb.
const WireKind = "opencode-web"

// NotConfiguredDetail is the honest not_configured reason for an empty URL —
// shared by InstanceStatus and the failing operations so the surfaces never
// disagree (design §4.1.2).
const NotConfiguredDetail = "OpenCode Web endpoint not configured"

func init() {
	core.RegisterAgent(BackendID, New)
}

// Agent implements core.Agent for the opencode-web backend.
type Agent struct {
	baseURL string
	user    string
	pass    string
	dataDir string

	workDir  string
	pinStore *pinstore.Store

	// pendingModel is the recorded "providerID/modelID" selection that rides
	// the next prompt (1.18 has no dedicated switch endpoint — design §4.3.5).
	pendingModel string

	// allowedTools records bridge-side tool authorization hints (returned
	// verbatim; the official API has no pre-authorization surface).
	allowedTools []string

	// probeMu guards the last API-generation probe outcome. The probe is a
	// read-only GET sequence; it never mutates server state.
	probeMu sync.Mutex
	probe   *probeResult

	// usageMu guards the last computed context usage per session and the
	// provider-catalog window map (design §3.3 — windows only ever come from
	// the runtime catalog, never a hand-written list).
	usageMu        sync.Mutex
	usageBySession map[string]*core.ContextUsage
	modelWindows   map[string]int
	modelWindowsAt time.Time

	// catalogMu guards the catalog refresh signal channel (SSE
	// session.created/deleted → sessions_changed; design §4.3.1).
	catalogMu      sync.Mutex
	catalogRefresh chan struct{}

	// projectsMu guards the merged serve-project directory view (projects.go:
	// /project registry, TTL-cached + SSE-catalog-signal invalidated).
	projectsMu    sync.Mutex
	projectDirs   []string
	projectDirsAt time.Time

	// catalogEntryMu guards the parsed provider catalog (~5MB raw JSON on
	// 1.18 — one fetch per TTL shared by list_models / send gate / windows).
	catalogEntryMu sync.Mutex
	catalogEntry   *catalogCacheEntry

	// onceMu guards the cross-subscriber once-claims. A live session is watched
	// by BOTH a dedicated subscriber (StartSession → relay channel) and the
	// global passive one — both decode the same SSE frames, so per-subscriber
	// dedupe lets terminal error text through once per subscriber (2026-08-19
	// live log: the 套餐 error text flushed 3× as text_delta). The claim lives
	// at Agent level and survives emitResultOnce's terminal-map consume.
	onceMu          sync.Mutex
	terminalTextSet map[string]bool // sessionID → terminal text already emitted this turn
	retryStatusSeen map[string]int  // sessionID → highest retry attempt already emitted

	// lastRetryMu guards the per-session retry snapshot for re-attach replay.
	// bridge-v1 session_retry_status is transient by design（不做离线持久化，
	// 官方 web 也只在实时流显示）——owner 2026-08-19：锁屏/后台窗口会错过
	// 重试行。重附（iOS 重开会话 / relay 重连 → StartSession）时若快照仍
	// 新鲜（2 分钟内）则重放一次；回合收口即清除，避免陈旧重试状态复活。
	lastRetryMu sync.Mutex
	lastRetry   map[string]retrySnapshot

	bgCtx    context.Context
	bgCancel context.CancelFunc

	mu sync.RWMutex // guards workDir
}

var _ core.Agent = (*Agent)(nil)

// New creates the opencode-web agent from options. It never fails on a
// missing CLI binary — this backend does not use the CLI at all.
//
//	opts["work_dir"]          string — default working directory
//	opts["opencode_web_url"]  string — resolved serve URL; empty = not_configured
//	opts["opencode_web_user"] string — Basic Auth username
//	opts["opencode_web_pass"] string — Basic Auth password
//	opts["data_dir"]          string — bridge data dir (diagnostics only)
//	opts["pin_store"]         *pinstore.Store — bridge pin index
//
// The package deliberately does NOT read opencode_url: during the coexistence
// period both backends receive the same resolved URL through separate keys.
func New(opts map[string]any) (core.Agent, error) {
	a := &Agent{
		workDir: ".",
	}
	if v, ok := opts["work_dir"].(string); ok && v != "" {
		a.workDir = v
	}
	if v, ok := opts["opencode_web_url"].(string); ok {
		a.baseURL = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if v, ok := opts["opencode_web_user"].(string); ok {
		a.user = v
	}
	if v, ok := opts["opencode_web_pass"].(string); ok {
		a.pass = v
	}
	if v, ok := opts["data_dir"].(string); ok {
		a.dataDir = v
	}
	if ps, ok := opts["pin_store"].(*pinstore.Store); ok {
		a.pinStore = ps
	} else {
		a.pinStore = pinstore.FromOpts(opts)
	}
	a.bgCtx, a.bgCancel = context.WithCancel(context.Background())
	a.terminalTextSet = make(map[string]bool)
	a.retryStatusSeen = make(map[string]int)
	a.lastRetry = make(map[string]retrySnapshot)

	if a.baseURL != "" {
		// Bounded startup probe so the first hello_ack reports the real state
		// (loopback only; worst case adds the probe timeout to boot).
		a.refreshProbe(a.bgCtx)
	}
	return a, nil
}

// Name returns the registration name.
func (a *Agent) Name() string { return BackendID }

// Stop cancels background contexts. There is never a spawned process to kill.
func (a *Agent) Stop() error {
	if a.bgCancel != nil {
		a.bgCancel()
	}
	return nil
}

// claimTerminalText reports whether the caller won the single terminal-text
// emission slot for this session's turn (live 1.18 failure chain fires
// session.error once per SSE connection — with two subscribers that is two
// emissions, and the trailing assistant message.updated(info.error) adds a
// third after emitResultOnce consumed the per-subscriber terminal note).
func (a *Agent) claimTerminalText(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	a.onceMu.Lock()
	defer a.onceMu.Unlock()
	if a.terminalTextSet == nil {
		a.terminalTextSet = make(map[string]bool)
	}
	if a.terminalTextSet[sessionID] {
		return false
	}
	a.terminalTextSet[sessionID] = true
	return true
}

// clearTerminalOnceClaims re-arms the once-claims when a new turn arms
// (user prompt observed). Safe to call from both subscribers.
func (a *Agent) clearTerminalOnceClaims(sessionID string) {
	if sessionID == "" {
		return
	}
	a.onceMu.Lock()
	defer a.onceMu.Unlock()
	delete(a.terminalTextSet, sessionID)
	delete(a.retryStatusSeen, sessionID)
}

// claimRetryStatus dedupes the transient retry notice per attempt across the
// two subscribers (each fires the same session.status{retry} frame).
func (a *Agent) claimRetryStatus(sessionID string, attempt int) bool {
	if sessionID == "" {
		return false
	}
	a.onceMu.Lock()
	defer a.onceMu.Unlock()
	if a.retryStatusSeen == nil {
		a.retryStatusSeen = make(map[string]int)
	}
	if attempt > 0 && a.retryStatusSeen[sessionID] >= attempt {
		return false
	}
	if attempt > a.retryStatusSeen[sessionID] {
		a.retryStatusSeen[sessionID] = attempt
	}
	return true
}

// retrySnapshot is the replayable tail of the transient retry notices.
type retrySnapshot struct {
	Attempt int
	Message string
	Next    int64
	At      time.Time
}

// retrySnapshotFreshness bounds replay: serve retry backoff windows are
// seconds-scale; 2 minutes covers the observed worst case without resurrecting
// stale rows after a settled turn (settle clears the snapshot anyway).
const retrySnapshotFreshness = 2 * time.Minute

// noteRetrySnapshot records the latest retry notice for re-attach replay.
func (a *Agent) noteRetrySnapshot(sessionID string, attempt int, message string, next int64) {
	if sessionID == "" {
		return
	}
	a.lastRetryMu.Lock()
	defer a.lastRetryMu.Unlock()
	if a.lastRetry == nil {
		a.lastRetry = make(map[string]retrySnapshot)
	}
	a.lastRetry[sessionID] = retrySnapshot{Attempt: attempt, Message: message, Next: next, At: time.Now()}
}

// clearRetrySnapshot drops the replay tail when the turn settles (idle) or a
// new turn arms — settled turns must not replay a retry row on re-attach.
func (a *Agent) clearRetrySnapshot(sessionID string) {
	if sessionID == "" {
		return
	}
	a.lastRetryMu.Lock()
	defer a.lastRetryMu.Unlock()
	delete(a.lastRetry, sessionID)
}

// replayableRetrySnapshot returns the fresh snapshot for the session, if any.
func (a *Agent) replayableRetrySnapshot(sessionID string) (retrySnapshot, bool) {
	if sessionID == "" {
		return retrySnapshot{}, false
	}
	a.lastRetryMu.Lock()
	defer a.lastRetryMu.Unlock()
	snap, ok := a.lastRetry[sessionID]
	if !ok || time.Since(snap.At) > retrySnapshotFreshness {
		return retrySnapshot{}, false
	}
	return snap, true
}

// instanceStatusProbeTTL bounds how long a successful probe stays trusted
// before InstanceStatus re-probes (read-only GETs against a loopback server).
const instanceStatusProbeTTL = 15 * time.Second

// InstanceStatus mirrors the endpoint state for hello_ack detection. The probe
// is a read-only GET sequence; it never spawns, binds, or writes.
func (a *Agent) InstanceStatus() (available bool, detail string) {
	if a.baseURL == "" {
		return false, NotConfiguredDetail
	}
	a.probeMu.Lock()
	if a.probe == nil || a.probe.err != nil || time.Since(a.probe.at) > instanceStatusProbeTTL {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		a.runProbe(ctx)
		cancel()
	}
	res := a.probe
	a.probeMu.Unlock()
	if res == nil {
		return false, "probe failed: no result"
	}
	if res.err != nil {
		return false, "probe failed: " + res.err.Error()
	}
	return true, res.detail
}

// refreshProbe re-probes when the cached outcome is missing, failed, or stale.
// Used on the New() startup path.
func (a *Agent) refreshProbe(ctx context.Context) {
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	a.runProbe(ctx)
}

// runProbe performs the generation probe with the caller's context;
// probeMu must be held. Failures are logged (diagnostics read them via the
// cached result).
func (a *Agent) runProbe(ctx context.Context) {
	c := newClient(a.baseURL, a.user, a.pass)
	res := probeInstance(ctx, c)
	a.probe = &res
	if res.err != nil {
		slog.Warn("opencode-web: endpoint probe failed", "url", a.baseURL, "error", res.err)
	} else {
		c.setGeneration(res.gen)
		slog.Info("opencode-web: endpoint probe ok", "url", a.baseURL, "detail", res.detail)
	}
}

// clientFor returns an HTTP client pinned to the probed API generation,
// re-probing on demand when the cached outcome is missing, failed, or stale.
// This is the single entry every data-plane operation goes through.
func (a *Agent) clientFor(ctx context.Context) (*Client, error) {
	if a.baseURL == "" {
		return nil, fmt.Errorf("%s", NotConfiguredDetail)
	}
	a.probeMu.Lock()
	if a.probe == nil || a.probe.err != nil || time.Since(a.probe.at) > instanceStatusProbeTTL {
		a.runProbe(ctx)
	}
	res := a.probe
	a.probeMu.Unlock()
	if res == nil || res.err != nil {
		detail := "probe incomplete"
		if res != nil {
			detail = res.err.Error()
		}
		return nil, fmt.Errorf("opencode-web endpoint not usable: %s", detail)
	}
	c := newClient(a.baseURL, a.user, a.pass)
	c.setGeneration(res.gen)
	return c, nil
}

// StartSession lands with §8-4 — see session.go.

// ListSessions lands with §8-2 (GET /session) — see sessions.go.

// WorkDirSwitcher: create_session's directory parameter arrives via the
// go-bridge switchDir dispatch, so the agent-level work dir is the default
// x-opencode-directory header source until a session's own directory is known.

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

var _ core.WorkDirSwitcher = (*Agent)(nil)
