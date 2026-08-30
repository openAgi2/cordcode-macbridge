package codexremote

import (
	"fmt"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ErrNotConfigured is returned until Remote Control enrollment and a Desktop
// environment stream exist. Empty catalogs and fake sessions are forbidden.
var ErrNotConfigured = fmt.Errorf("请先在 Mac 的 CordCode Link 里配对 Codex Desktop")

// Agent is the fail-closed Phase 1 identity. Transport, RPC and live turns
// land in later Phase 1 units.
type Agent struct {
	mu                 sync.Mutex
	attachMu           sync.Mutex
	catalogMu          sync.Mutex
	catalogWake        chan struct{}
	workDir            string
	stopped            bool
	client             *Client
	codec              *LiveCodec
	listeners          map[string]map[chan core.Event]struct{}
	attached           map[string]*Client
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

var _ core.CatalogRefreshSignaler = (*Agent)(nil)

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
