package codexremote

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ErrNotConfigured is returned until Remote Control enrollment and a Desktop
// environment stream exist. Empty catalogs and fake sessions are forbidden.
var ErrNotConfigured = fmt.Errorf("请先在 Mac 的 CordCode Link 里配对 Codex Desktop")

// Agent is the fail-closed Phase 1 identity. Transport, RPC and live turns
// land in later Phase 1 units.
type Agent struct {
	mu          sync.Mutex
	attachMu    sync.Mutex
	catalogMu   sync.Mutex
	catalogWake chan struct{}
	// reconcileMu guards the S-2 turn-reconcile seam (core.TurnReconciler):
	// the wake channel mirrors catalogWake (one-slot, data-free) and the
	// pending set is the truth read by the bridge's 3s catalog loop.
	reconcileMu      sync.Mutex
	reconcileWake    chan struct{}
	pendingRec       map[string]struct{}
	workDir          string
	stopped          bool
	client           *Client
	codec            *LiveCodec
	listeners        map[string]map[chan core.Event]struct{}
	passiveObservers map[chan core.Event]struct{}
	attached         map[string]*Client
	// attachSkipped records threads whose thread/resume the app-server refused
	// with an RPC error (e.g. "no rollout found" = official ThreadNotFound) in
	// this client epoch, so the 3s catalog loop warns once instead of every
	// pass. BindClient clears it together with attached.
	attachSkipped      map[string]struct{}
	resumeInitialPages map[string]*resumeInitialPage
	resumePageBroken   bool
	// serverVersion is the codex app-server workspace version announced by
	// this client epoch's initialize response ("" until then); it gates the
	// thread/resume initialTurnsPage candidate on the probe-verified
	// allowlist. Client-epoch-scoped: BindClient clears it.
	serverVersion      string
	modelKnown         map[string]struct{}
	modelEfforts       map[string][]string
	modelDefaultEffort map[string]string
	defaultModel       string
	selectedModel      string
	sessionSelections  map[string]core.SessionModelSelection
	paired             bool
	pairing            *PairingController
	connEpoch          ConnectionEpoch
	// Runtime diagnostics are aggregate-only counters. They never carry prompt,
	// response, path, or stable session-identity data.
	backgroundScanActive             atomic.Int64
	backgroundScanTotal              atomic.Uint64
	backgroundScanSuccesses          atomic.Uint64
	backgroundScanFailures           atomic.Uint64
	backgroundScannedTurns           atomic.Uint64
	backgroundScanLastDurationMillis atomic.Int64
	turnItemRequests                 atomic.Uint64
	// collabFoldMu guards collabFolds, the session-keyed cross-turn workflow
	// fold contexts used by every cold history mapping surface (the live
	// codec keeps its own registry).
	collabFoldMu sync.Mutex
	collabFolds  map[string]*remoteCollabHistoryFolds
}

// New constructs an unenrolled agent.
func New(opts map[string]any) *Agent {
	workDir := ""
	dataDir := ""
	if opts != nil {
		if value, ok := opts["work_dir"].(string); ok {
			workDir = value
		}
		if value, ok := opts["data_dir"].(string); ok {
			dataDir = value
		}
	}
	skipRestore := false
	if opts != nil {
		if value, ok := opts["skip_restore"].(bool); ok {
			skipRestore = value
		}
	}
	a := &Agent{workDir: workDir}
	a.pairing = newPairingController(a)
	if dataDir != "" {
		a.pairing.storePath = pairingStorePath(dataDir)
		if !skipRestore {
			go a.restorePersistedPairing()
		}
	}
	return a
}

func (a *Agent) Name() string { return BackendID }

// WaitForRestore bounds the gap between process startup and persisted Remote
// Control stream binding. A missing client is ErrNotConfigured only when there is
// no persisted identity or pairing has terminally failed; offline/authorizing are
// transient retry states and return core.ErrRestoreInProgress after the bound.
func (a *Agent) WaitForRestore(ctx context.Context) error {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl != nil {
		return nil
	}
	if a.pairing == nil || !a.pairing.hasPersistedIdentity() {
		return ErrNotConfigured
	}
	deadline := time.After(remoteRestoreWaitTimeout)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot := a.pairing.Snapshot()
		if snapshot.Phase == PairPhaseFailed {
			return ErrNotConfigured
		}
		a.mu.Lock()
		cl = a.client
		a.mu.Unlock()
		if cl != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return core.ErrRestoreInProgress
		case <-ticker.C:
		}
	}
}

// BackgroundTaskScanCounters exposes aggregate scan/request counters for the
// management diagnostics endpoint. Values are advisory observations, not truth
// used to synthesize task lists.
// SuppressedErrorNotifications exposes the codec's aggregate duplicate-error
// suppression counter without carrying error text or session identity.
func (a *Agent) SuppressedErrorNotifications() uint64 {
	a.mu.Lock()
	codec := a.codec
	a.mu.Unlock()
	if codec == nil {
		return 0
	}
	return codec.SuppressedErrorNotifications()
}

func (a *Agent) BackgroundTaskScanCounters() (active, scans, successes, failures, turnItemRequests, scannedTurns uint64, lastDurationMillis int64) {
	return uint64(a.backgroundScanActive.Load()), a.backgroundScanTotal.Load(),
		a.backgroundScanSuccesses.Load(), a.backgroundScanFailures.Load(),
		a.turnItemRequests.Load(), a.backgroundScannedTurns.Load(),
		a.backgroundScanLastDurationMillis.Load()
}

var _ core.CatalogRefreshSignaler = (*Agent)(nil)
var _ core.RestoreWaiter = (*Agent)(nil)
var _ core.LiveEventSubscriber = (*Agent)(nil)
var _ core.LiveEventCatalogAttacher = (*Agent)(nil)

// CatalogRefreshSignals exposes official catalog-affecting notifications to
// the Bridge discovery worker. The signal is deliberately data-free:
// thread/list remains the sole catalog truth and the one-slot channel coalesces
// bursts.
func (a *Agent) CatalogRefreshSignals() <-chan struct{} {
	a.catalogMu.Lock()
	defer a.catalogMu.Unlock()
	if a.catalogWake == nil {
		a.catalogWake = make(chan struct{}, 1)
	}
	return a.catalogWake
}

func (a *Agent) signalCatalogRefresh() {
	a.catalogMu.Lock()
	if a.catalogWake == nil {
		a.catalogWake = make(chan struct{}, 1)
	}
	select {
	case a.catalogWake <- struct{}{}:
	default:
	}
	a.catalogMu.Unlock()
}

// TurnReconcileSignals implements core.TurnReconciler (disconnect-resilience
// plan S-2 wiring element 3): same data-free one-slot mechanism as
// CatalogRefreshSignals — the pending set is the truth, the channel only
// wakes the bridge's 3s catalog loop early.
func (a *Agent) TurnReconcileSignals() <-chan struct{} {
	a.reconcileMu.Lock()
	defer a.reconcileMu.Unlock()
	if a.reconcileWake == nil {
		a.reconcileWake = make(chan struct{}, 1)
	}
	return a.reconcileWake
}

// addPendingTurnReconcile records a thread whose live codec still tracks an
// in-flight turn at reconnect time and wakes the reconcile consumer. The
// missed terminal event inside the disconnect window is never replayed (E-1),
// so only an authoritative summary read can close the turn.
func (a *Agent) addPendingTurnReconcile(threadID string) {
	a.reconcileMu.Lock()
	if a.pendingRec == nil {
		a.pendingRec = map[string]struct{}{}
	}
	a.pendingRec[threadID] = struct{}{}
	wake := a.reconcileWake
	if wake == nil {
		wake = make(chan struct{}, 1)
		a.reconcileWake = wake
	}
	a.reconcileMu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

// PendingTurnReconciles implements core.TurnReconciler: snapshot without
// clearing — failed threads stay pending for the next 3s round.
func (a *Agent) PendingTurnReconciles() []string {
	a.reconcileMu.Lock()
	defer a.reconcileMu.Unlock()
	out := make([]string, 0, len(a.pendingRec))
	for threadID := range a.pendingRec {
		out = append(out, threadID)
	}
	return out
}

// ActiveTurnForReconcile implements core.TurnReconciler: the codec's
// in-flight turn for the thread ("" when none) — a turn that closed on the
// live path needs no reconciliation.
func (a *Agent) ActiveTurnForReconcile(threadID string) string {
	a.mu.Lock()
	codec := a.codec
	a.mu.Unlock()
	if codec == nil {
		return ""
	}
	return codec.ActiveTurn(threadID)
}

// ClearPendingTurnReconcile implements core.TurnReconciler: drop the pending
// entry after a completed reconcile attempt; the codec entry is untouched.
func (a *Agent) ClearPendingTurnReconcile(threadID string) {
	a.reconcileMu.Lock()
	delete(a.pendingRec, threadID)
	a.reconcileMu.Unlock()
}

// ClearReconciledTurn implements core.TurnReconciler: drop the pending entry
// AND the codec's in-flight turn entry — only for turns the reconciliation
// actually closed, so the closed turn never re-enters the pending set on a
// later rebind.
func (a *Agent) ClearReconciledTurn(threadID string) {
	a.reconcileMu.Lock()
	delete(a.pendingRec, threadID)
	a.reconcileMu.Unlock()
	a.mu.Lock()
	codec := a.codec
	a.mu.Unlock()
	if codec != nil {
		codec.setActiveTurn(threadID, "")
	}
}

// SetWorkDir implements core.WorkDirSwitcher. The bridge calls it before
// create_session/send_message; StartSession snapshots the value into the
// official thread/start cwd parameter.
func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	a.workDir = dir
	a.mu.Unlock()
}

func (a *Agent) GetWorkDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workDir
}

// StartSession is in session.go; ListSessions/FetchThreadList are in catalog.go.
// Unbound agents still return ErrNotConfigured.

func (a *Agent) Stop() error {
	a.mu.Lock()
	a.stopped = true
	cl := a.client
	a.client = nil
	a.mu.Unlock()
	if cl != nil {
		return cl.Close()
	}
	return nil
}
