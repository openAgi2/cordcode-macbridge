package dshweb

// dsh-web Agent skeleton: registration, options, and the instance lifecycle.
// The RPC mapping (list/create/history/prompt/…) lands with §8-2; the WS
// mux/host pipelines with §8-3/§8-4. Until then StartSession/ListSessions
// fail loudly rather than pretending.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

// BackendID is the go-bridge driver id (drivers flag / hello_ack backends[]).
const BackendID = "dsh-web"

// WireKind is the backend kind advertised on the wire (iOS BackendKind
// .deepSeekWeb). Kept distinct from the driver id per design §4.1.
const WireKind = "deepseek-web"

func init() {
	core.RegisterAgent(BackendID, New)
}

// Agent implements core.Agent for the dsh-web backend.
type Agent struct {
	workDir  string
	resolver *Resolver

	// pinStore persists MacBridge-owned 置顶 metadata (bridge pin index —
	// design §4.3.1 ♻️; summary enrichment stays in go-bridge handlers).
	pinStore *pinstore.Store

	// running caches the last session.list running flags (§4.3.1 enrich;
	// §8-3's host/session-status frames keep it fresh).
	running runningCache
	// bindings tracks live session objects (§8-4 surface rule + §8-3 routing).
	bindings sessionBindings

	// Terminal-producer idempotence (design §12 item 3 / §12.1-3): keyed on
	// the resolver's loss sequence so one alive→dark edge closes each running
	// session exactly once, and a later edge re-arms.
	termMu       sync.Mutex
	lastTermLoss uint64

	// lastActiveSessionID is the most recent StartSession target — the
	// bridge-level switch_model surface (§4.3.5: no official global write;
	// session.selectModel is session-scoped).
	lastActiveSessionID string
	// mode is the last selected official permission preset
	// (read-only / workspace-write / danger-full-access).
	mode string
	// usageBySession caches official contextPressure/contextBreakdown
	// occupancy plus sessionStats/tokenUsage for GetContextUsage / live emit.
	usageMu        sync.Mutex
	usageBySession map[string]*core.ContextUsage
	// pendingSel is the recorded provider/model/effort selection.
	pendingSel selection
	// pendingPreset is the official agentPreset id for the next session.create.
	pendingPreset string
	// catalog is the last runtime-fetched provider/model catalog.
	catalog *modelCatalog
	// allowedTools records bridge-side tool authorization hints (returned
	// verbatim; dsh v1 has no pre-authorization surface).
	allowedTools []string

	// Dual-stream pump state (§8-3): passive channel, refresh signals,
	// per-session codecs.
	streamMu       sync.Mutex
	streamsStarted bool
	passive        chan core.Event
	refreshSignals chan struct{}
	codecs         map[string]*sessionCodec

	// approvals/question pending state (§8-4).
	approvalsMu sync.Mutex
	approvals   *approvalsState

	bgCtx    context.Context
	bgCancel context.CancelFunc

	// resolveErr holds the last background resolution failure detail for
	// InstanceStatus; a resolved instance reports its source instead.
	resolveErr atomic.Value // string

	mu sync.RWMutex
}

var _ core.Agent = (*Agent)(nil)

// New creates the dsh-web agent from options.
//
//	opts["work_dir"]      string — default working directory (session cwd default)
//	opts["dsh_web_url"]   string — explicit instance URL (user config; skips probing)
//	opts["cli_path"]      string — dsh executable for the managed spawn (default: PATH)
//	opts["dsh_home"]      string — DSH_HOME override (sandbox experiments only)
//	opts["data_dir"]      string — bridge data dir for dsh-web-managed-server.json
//	opts["pin_store"]     *pinstore.Store — bridge pin index
func New(opts map[string]any) (core.Agent, error) {
	a := &Agent{
		workDir: ".",
	}

	if v, ok := opts["work_dir"].(string); ok && v != "" {
		a.workDir = v
	}

	var resolverOpts []ResolverOption
	if v, ok := opts["dsh_web_url"].(string); ok && strings.TrimSpace(v) != "" {
		resolverOpts = append(resolverOpts, WithProbeURLs([]string{v}))
	}
	if v, ok := opts["cli_path"].(string); ok && strings.TrimSpace(v) != "" {
		fields := strings.Fields(v)
		resolverOpts = append(resolverOpts, WithManagedBinary(fields[0], fields[1:]))
	}
	if v, ok := opts["dsh_home"].(string); ok && strings.TrimSpace(v) != "" {
		resolverOpts = append(resolverOpts, WithDSHHome(strings.TrimSpace(v)))
	}
	if v, ok := opts["data_dir"].(string); ok && strings.TrimSpace(v) != "" {
		resolverOpts = append(resolverOpts, WithDataDir(strings.TrimSpace(v)))
	}
	if ps, ok := opts["pin_store"].(*pinstore.Store); ok {
		a.pinStore = ps
	}
	a.resolver = NewResolver(resolverOpts...)
	a.resolver.SetLostCallback(a.handleSeatLost)

	// Startup resolution (§4.2): probe the user's instance, else spawn the
	// managed one, in the background so agent construction (and the hello_ack
	// built moments later) never blocks on a 30s dsh boot. hello_ack shows
	// the honest current state via InstanceStatus; the first real operation
	// re-enters Resolve (cached fast path) once it succeeded.
	a.bgCtx, a.bgCancel = context.WithCancel(context.Background())
	go a.backgroundResolve()
	return a, nil
}

// backgroundResolve runs the one startup resolution attempt. Failure is
// retained for InstanceStatus; real operations retry on demand.
func (a *Agent) backgroundResolve() {
	inst, err := a.resolver.Resolve(a.bgCtx)
	if err != nil {
		a.resolveErr.Store(err.Error())
		slog.Warn("dsh-web: startup instance resolution failed", "error", err)
		return
	}
	a.resolveErr.Store("")
	slog.Info("dsh-web: instance resolved", "source", string(inst.Source), "baseURL", inst.BaseURL)
}

// Name returns the registration name.
func (a *Agent) Name() string { return BackendID }

// Stop kills the managed instance this process spawned (if any). External
// instances are never touched.
func (a *Agent) Stop() error {
	if a.bgCancel != nil {
		a.bgCancel()
	}
	return a.resolver.Stop()
}

// handleSeatLost is the resolver's alive→dark edge hook (design §12 item 3):
// every live bound session whose official running flag was up receives ONE
// terminal error event — the instance died mid-turn and iOS must never hang
// on 「执行中」 (坑 8 red line). Idempotence is keyed on the resolver's loss
// sequence: grace entry, stream 1006, and any probe path all funnel through
// the single loseSeat transition, and this guard makes double-firing
// structurally impossible while re-arming on the next edge (§12.1-3).
func (a *Agent) handleSeatLost() {
	seq := a.resolver.LossSeq()
	a.termMu.Lock()
	if seq == a.lastTermLoss {
		a.termMu.Unlock()
		return
	}
	a.lastTermLoss = seq
	a.termMu.Unlock()

	err := fmt.Errorf("dsh web 实例失联（%s）：进行中的本轮对话随实例中断；实例恢复后请重发，或重新打开会话拉取历史", a.resolver.seatURL())
	for _, s := range a.bindings.snapshot() {
		id := s.CurrentSessionID()
		if id == "" {
			continue
		}
		running, known := a.running.get(id)
		if !known || !running {
			continue
		}
		select {
		case s.events <- core.Event{Type: core.EventError, SessionID: id, Error: err}:
			slog.Info("dsh-web: terminal error event emitted for running session",
				"sessionID", id, "lossSeq", seq)
		default:
			// Channel full — the bridge read loop for this session is gone;
			// the next use rebuilds. Fail loudly in logs, never silently.
			slog.Warn("dsh-web: terminal event dropped (session channel full)",
				"sessionID", id, "lossSeq", seq)
		}
	}
}

// InstanceStatus reports the resolved-instance state for hello_ack detection.
// It never resolves or spawns — only mirrors the background/startup result.
//
// Canonical-seat grace special case (design §3.2/§12.1-4): while the seat is
// in its grace window, Current()==nil — reporting available=false here would
// fall through detectInstanceStatusProber as not_configured, exactly the code
// the grace contract forbids. Stay visible with the reconnecting detail.
func (a *Agent) InstanceStatus() (available bool, detail string) {
	if inGrace, until := a.resolver.GraceState(); inGrace {
		return true, fmt.Sprintf("instance reconnecting (grace until %s)", until.Format(time.RFC3339))
	}
	if inst := a.resolver.Current(); inst != nil {
		switch inst.Source {
		case SourceExternal:
			return true, fmt.Sprintf("external dsh web instance at %s", inst.BaseURL)
		case SourceManaged:
			return true, fmt.Sprintf("managed dsh web instance at %s (pid %d)", inst.BaseURL, inst.PID)
		}
	}
	if errStr, _ := a.resolveErr.Load().(string); errStr != "" {
		return false, errStr
	}
	return false, "dsh web instance not resolved yet (probe/managed spawn in flight)"
}

// WorkDirSwitcher: create_session's directory parameter lands via switchDir
// before StartSession, so the agent-level work dir is the next create's cwd.

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

// GetRichSessionHistory implements core.RichHistoryProvider (§4.3.2:
// pathless cold-hydrate data source = session.history).
func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	return a.getRichHistory(ctx, client, sessionID, limit)
}

var _ core.RichHistoryProvider = (*Agent)(nil)
