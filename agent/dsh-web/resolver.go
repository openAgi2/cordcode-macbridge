package dshweb

// Instance lifecycle: probe the user's own dsh web instance first, then fall
// back to a managed loopback spawn (design §4.2). Both fail ⇒ the caller
// reports backend not_configured honestly — nothing is installed on the
// user's behalf (CordCode 初衷: 探测-复用-未启动).
//
// Managed spawn hard red lines (§4.4): loopback host only, NEVER
// --trusted-host, NEVER 0.0.0.0. The managed instance is an unauthenticated
// loopback service (dsh v1 has no auth layer — trust fence is not auth);
// loopback binding + Bridge-fronting is the entire defense, mirrored in
// diagnostics output.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// InstanceSource labels where the connected dsh web instance came from.
type InstanceSource string

const (
	// SourceExternal: a dsh web instance the user started themselves (probe
	// hit, e.g. their own `dsh web` on 3080).
	SourceExternal InstanceSource = "external"
	// SourceManaged: an instance this backend spawned and owns.
	SourceManaged InstanceSource = "managed"
)

// DefaultProbePort is the dsh web default port (dsh web --help: default 3080).
const DefaultProbePort = 3080

// managedPortRange mirrors the opencode managed-local precedent: 3096..3196.
const (
	managedPortMin = 3096
	managedPortMax = 3196
)

// managedStateFile persists the managed instance's identity so a bridge
// restart can re-adopt its own still-running instance instead of racing a
// second spawn (opencode-managed-server.json precedent; no credentials — dsh
// v1 has no auth surface to record, design S11).
const managedStateFile = "dsh-web-managed-server.json"

// ResolvedInstance is one live dsh web instance this backend talks to.
type ResolvedInstance struct {
	BaseURL string         // http://127.0.0.1:<port>
	Port    int            // listen port
	Source  InstanceSource // external | managed
	PID     int            // managed only; 0 for external
}

// describeValue is the host.describe business value (host.schema.ts).
type describeValue struct {
	Version          string `json:"version"`
	Cwd              string `json:"cwd"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	AttachedSessions int    `json:"attachedSessions"`
	CanOpenPath      bool   `json:"canOpenPath"`
}

// probeInstance sends host.describe at baseURL and reports whether a dsh web
// API answers. Short timeout — this is a liveness probe, not a workload.
func probeInstance(ctx context.Context, httpClient *http.Client, baseURL string) (*describeValue, error) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c := NewClient(baseURL, httpClient)
	var out describeValue
	if err := c.Call(pctx, "host.describe", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// probeTimeout bounds one host.describe probe.
const probeTimeout = 2 * time.Second

// managedBootTimeout bounds how long a freshly spawned `dsh web` may take to
// answer its first host.describe (profile composition is pnpm/node work; be
// generous rather than flapping between spawn attempts).
const managedBootTimeout = 30 * time.Second

// managedStarter abstracts "get a dsh web server running on this port" so the
// resolver logic is unit-testable without a real dsh install.
type managedStarter interface {
	// Start brings a server up on 127.0.0.1:port. It returns the server PID.
	Start(ctx context.Context, port int) (int, error)
	// Stop terminates everything Start launched (process group).
	Stop() error
}

// execManagedStarter spawns the real `dsh web` CLI as a child process.
type execManagedStarter struct {
	binPath   string
	extraArgs []string
	dshHome   string // optional DSH_HOME override (tests only; "" = user's ~/.dsh)
	logPath   string // optional stdout/stderr capture

	cmd *exec.Cmd
	mu  sync.Mutex
}

// startArgs returns the managed server argv. Exposed for a build-assert test:
// loopback host, explicit port, profile web — and provably never
// --trusted-host (§4.4 red line).
func (s *execManagedStarter) startArgs(port int) []string {
	args := []string{"--profile", "web", "--host", "127.0.0.1", "--port", strconv.Itoa(port)}
	return append(args, s.extraArgs...)
}

func (s *execManagedStarter) Start(ctx context.Context, port int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		return 0, fmt.Errorf("dshweb: managed starter already has a process (pid %d)", s.cmd.Process.Pid)
	}
	cmd := exec.Command(s.binPath, s.startArgs(port)...)
	// Own process group: dsh spawns node children; group kill reaps them all
	// (same posture as agent/dsh and grokbuild).
	prepareCmdForProcessGroup(cmd)
	cmd.Env = os.Environ()
	if s.dshHome != "" {
		cmd.Env = append(cmd.Env, "DSH_HOME="+s.dshHome)
	}
	if s.logPath != "" {
		if f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
			cmd.Stdout = f
			cmd.Stderr = f
		}
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("dshweb: spawn %s: %w", s.binPath, err)
	}
	s.cmd = cmd
	return cmd.Process.Pid, nil
}

func (s *execManagedStarter) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	err := terminateProcessGroup(s.cmd)
	s.cmd = nil
	return err
}

// Resolver owns the probe→managed lifecycle for one dshweb Agent.
type Resolver struct {
	probeURLs []string // external candidates, probe order
	binPath   string   // dsh executable for managed spawn ("" = LookPath)
	extraArgs []string
	dshHome   string // optional DSH_HOME override (sandbox experiments/tests)
	dataDir   string // managed-state persistence dir ("" = no persistence)

	httpClient   *http.Client
	managedStart managedStarter

	mu       sync.Mutex
	resolved *ResolvedInstance
}

// ResolverOption configures a Resolver.
type ResolverOption func(*Resolver)

// WithProbeURLs overrides the external probe list (default: 127.0.0.1:3080).
func WithProbeURLs(urls []string) ResolverOption {
	return func(r *Resolver) {
		r.probeURLs = normalizeBaseURLs(urls)
	}
}

// WithManagedBinary pins the dsh executable path (tests / explicit config).
func WithManagedBinary(bin string, extraArgs []string) ResolverOption {
	return func(r *Resolver) {
		r.binPath = bin
		r.extraArgs = extraArgs
	}
}

// WithDSHHome overrides DSH_HOME for the managed spawn (sandbox experiments).
// Production leaves it empty: the managed instance must share the user's real
// ~/.dsh store (design §5).
func WithDSHHome(home string) ResolverOption {
	return func(r *Resolver) { r.dshHome = home }
}

// WithDataDir sets where dsh-web-managed-server.json is persisted.
func WithDataDir(dir string) ResolverOption {
	return func(r *Resolver) { r.dataDir = dir }
}

// WithHTTPClient overrides the probe/call HTTP client (tests).
func WithHTTPClient(hc *http.Client) ResolverOption {
	return func(r *Resolver) { r.httpClient = hc }
}

// withManagedStarter swaps the spawn implementation (tests only).
func withManagedStarter(starter managedStarter) ResolverOption {
	return func(r *Resolver) { r.managedStart = starter }
}

func normalizeBaseURLs(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimSpace(strings.TrimRight(u, "/"))
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

// NewResolver builds the lifecycle manager. Default probe list is the single
// dsh web default port on loopback.
func NewResolver(opts ...ResolverOption) *Resolver {
	r := &Resolver{
		probeURLs: []string{fmt.Sprintf("http://127.0.0.1:%d", DefaultProbePort)},
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.managedStart == nil {
		bin := r.binPath
		if bin == "" {
			if found, err := exec.LookPath("dsh"); err == nil {
				bin = found
			}
		}
		r.managedStart = &execManagedStarter{
			binPath:   bin,
			extraArgs: r.extraArgs,
			dshHome:   r.dshHome,
		}
	}
	if r.httpClient == nil {
		r.httpClient = &http.Client{}
	}
	return r
}

// managedState is the persisted managed-instance record (0600).
type managedState struct {
	Version   int    `json:"version"`
	Source    string `json:"source"` // "managed"
	URL       string `json:"url"`
	Port      int    `json:"port"`
	PID       int    `json:"pid,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

const managedStateVersion = 1

func (r *Resolver) statePath() string {
	if r.dataDir == "" {
		return ""
	}
	return r.dataDir + string(os.PathSeparator) + managedStateFile
}

func (r *Resolver) loadState() *managedState {
	path := r.statePath()
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var st managedState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil
	}
	if st.Source != string(SourceManaged) || st.Port <= 0 || st.URL == "" {
		return nil
	}
	return &st
}

func (r *Resolver) saveState(inst *ResolvedInstance) {
	path := r.statePath()
	if path == "" {
		return
	}
	st := managedState{
		Version:   managedStateVersion,
		Source:    string(SourceManaged),
		URL:       inst.BaseURL,
		Port:      inst.Port,
		PID:       inst.PID,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(r.dataDir, 0o700)
	// 0600: port + pid of a loopback service (opencode-managed-server.json
	// precedent; no credentials exist to protect — S11 — but keep the mode).
	_ = core.AtomicWriteFile(path, b, 0o600)
}

// Resolve returns the live instance, probing external first, then re-adopting
// or spawning managed. The decision is cached: while the cached instance
// answers, it is returned as-is (§4.2 S3 — probing happens once per instance
// lifetime, not per call; a user starting 3080 later coexists with managed).
func (r *Resolver) Resolve(ctx context.Context) (*ResolvedInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Cached instance still answering?
	if r.resolved != nil {
		if _, err := probeInstance(ctx, r.httpClient, r.resolved.BaseURL); err == nil {
			return r.resolved, nil
		}
		// Cached instance died: fall through and re-resolve. A managed
		// instance that died is NOT silently respawned here unless the whole
		// managed path below succeeds again (which it will attempt).
		r.resolved = nil
	}

	// 2. External probe (user's own instance wins).
	for _, base := range r.probeURLs {
		if _, err := probeInstance(ctx, r.httpClient, base); err == nil {
			r.resolved = &ResolvedInstance{
				BaseURL: base,
				Port:    portOf(base),
				Source:  SourceExternal,
			}
			return r.resolved, nil
		}
	}

	// 3. Managed: re-adopt a previously spawned instance first (bridge restart
	// without the child dying), then spawn fresh.
	if st := r.loadState(); st != nil {
		if _, err := probeInstance(ctx, r.httpClient, st.URL); err == nil {
			r.resolved = &ResolvedInstance{
				BaseURL: st.URL,
				Port:    st.Port,
				Source:  SourceManaged,
				PID:     st.PID,
			}
			// Adopted but no longer owned by this process's starter; Stop()
			// therefore does not kill it. That is correct: an adopted instance
			// outlived one bridge restart already and the next spawn will
			// simply target a different port if this one stays alive.
			return r.resolved, nil
		}
	}

	inst, err := r.spawnManaged(ctx)
	if err != nil {
		return nil, err
	}
	r.resolved = inst
	r.saveState(inst)
	return inst, nil
}

// spawnManaged picks a free port in the managed range, starts the server, and
// waits for its first successful host.describe.
func (r *Resolver) spawnManaged(ctx context.Context) (*ResolvedInstance, error) {
	port, err := pickFreePort(managedPortMin, managedPortMax, r.preferredPort())
	if err != nil {
		return nil, fmt.Errorf("dshweb: managed port range %d..%d unavailable: %w", managedPortMin, managedPortMax, err)
	}
	pid, err := r.managedStart.Start(ctx, port)
	if err != nil {
		return nil, err
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	deadline := time.Now().Add(managedBootTimeout)
	for {
		if _, err := probeInstance(ctx, r.httpClient, baseURL); err == nil {
			return &ResolvedInstance{
				BaseURL: baseURL,
				Port:    port,
				Source:  SourceManaged,
				PID:     pid,
			}, nil
		}
		if time.Now().After(deadline) {
			_ = r.managedStart.Stop()
			return nil, fmt.Errorf("dshweb: managed dsh web on 127.0.0.1:%d did not answer host.describe within %s", port, managedBootTimeout)
		}
		select {
		case <-ctx.Done():
			_ = r.managedStart.Stop()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// preferredPort reuses the persisted managed port when it is still free,
// keeping the instance address stable across restarts.
func (r *Resolver) preferredPort() int {
	if st := r.loadState(); st != nil {
		return st.Port
	}
	return 0
}

// Current returns the cached instance without probing (nil if unresolved).
func (r *Resolver) Current() *ResolvedInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolved
}

// Stop kills the managed instance this process spawned (bridge shutdown).
// External and adopted instances are left running — they are not ours.
func (r *Resolver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stopErr error
	if r.managedStart != nil {
		stopErr = r.managedStart.Stop()
	}
	r.resolved = nil
	return stopErr
}

func portOf(baseURL string) int {
	if _, portStr, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(baseURL, "http://"), "https://")); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			return p
		}
	}
	return 0
}

// pickFreePort scans the inclusive range for a port that can be bound on
// loopback right now (preferred tried first when in range and free).
func pickFreePort(min, max, preferred int) (int, error) {
	try := func(port int) (int, bool) {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return 0, false
		}
		_ = ln.Close()
		return port, true
	}
	if preferred >= min && preferred <= max {
		if p, ok := try(preferred); ok {
			return p, nil
		}
	}
	for p := min; p <= max; p++ {
		if port, ok := try(p); ok {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port")
}

// processIsAlive reports whether pid exists (signal 0). Unexported helper for
// adoption checks; errors conservative (no pid ⇒ not alive).
func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
