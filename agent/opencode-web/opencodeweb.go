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

// StartSession lands with §8-4 (create/resume + Send). Until then it fails
// loudly rather than pretending a session exists.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	return nil, fmt.Errorf("opencode-web: StartSession not implemented yet (skeleton; lands with the send phase)")
}

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
