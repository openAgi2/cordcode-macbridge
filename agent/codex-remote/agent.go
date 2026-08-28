package codexremote

import (
	"fmt"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ErrNotConfigured is returned until Remote Control enrollment and a Desktop
// environment stream exist. Empty catalogs and fake sessions are forbidden.
var ErrNotConfigured = fmt.Errorf("codex-remote: not_configured: remote control enrollment required")

// Agent is the fail-closed Phase 1 identity. Transport, RPC and live turns
// land in later Phase 1 units.
type Agent struct {
	mu          sync.Mutex
	workDir     string
	stopped     bool
	client      *Client
	codec       *LiveCodec
	listeners   map[string]map[chan core.Event]struct{}
	pumpRunning bool
}

// New constructs an unenrolled agent.
func New(opts map[string]any) *Agent {
	workDir := ""
	if opts != nil {
		if value, ok := opts["work_dir"].(string); ok {
			workDir = value
		}
	}
	return &Agent{workDir: workDir}
}

func (a *Agent) Name() string { return BackendID }

// StartSession and ListSessions are implemented in session.go. Unbound agents
// still return ErrNotConfigured.

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
