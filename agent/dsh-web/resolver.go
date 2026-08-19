package dshweb

// Instance lifecycle under the canonical-seat model (design
// docs/2026-08-19-dsh-web-canonical-3080-instance-design.md §3): the seat —
// probeURLs[0], default 127.0.0.1:3080 — is the ONLY place a dsh web instance
// may live. Resolution always targets the seat; if it answers, it is used no
// matter who spawned it (port = identity). If the seat goes dark after this
// process held an instance, a grace window (default 120s) holds: no adoption
// of stray ports, no respawn — callers get the typed ErrInstanceReconnecting
// so handlers can surface backend_unavailable (§3.2). Cold start (this
// process never held an instance) spawns directly ON the seat (§3.1). The
// 3096–3196 managed port range is retired.
//
// Lock discipline (§3.3): mu guards only the cached decision fields
// (resolved/lostAt/negUntil/spawning); probes and the spawn boot-wait run
// outside the lock. Concurrent Resolve callers during an in-flight spawn get
// an immediate typed error — never a 30s block. A ≤1s negative cache bounds
// probe frequency while the seat is dark.
//
// Managed spawn red lines (design §4.4) are unchanged: loopback host only,
// never --trusted-host, never 0.0.0.0.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// SourceExternal: an instance on the seat this backend did not spawn
	// (the user's own `dsh web`, or a previous bridge's leftover).
	SourceExternal InstanceSource = "external"
	// SourceManaged: an instance this backend spawned and whose child still
	// holds the seat (design §4 race 1: probe the endpoint, label by
	// ownership — a dead child never lends its PID).
	SourceManaged InstanceSource = "managed"
)

// DefaultProbePort is the dsh web default port (dsh web --help: default 3080).
const DefaultProbePort = 3080

// gracePeriodDefault bounds the reconnect grace window after a live seat goes
// dark (design §3.1: 90–120s covering the 60s watcher interval, a human
// restart, and the 30s spawn budget). Package-level var so tests can shrink
// it; per-resolver override via withGracePeriod.
var gracePeriodDefault = 120 * time.Second

// seatProbeNegativeCache bounds how often a dark seat is re-probed (§3.3:
// mux + host + RPC must not each pay the probe timeout on every call).
const seatProbeNegativeCache = 1 * time.Second

// spawnRetryBackoff spaces respawn attempts after a failed spawn (e.g. seat
// held by a non-dsh service) so a spawn storm cannot form.
const spawnRetryBackoff = 5 * time.Second

// managedStateFile persists the managed instance's identity for diagnostics
// and one-time legacy cleanup (§6). Resolution never reads it — the seat is
// the identity (no adoption-by-state-file under the canonical-seat model).
const managedStateFile = "dsh-web-managed-server.json"

// ErrInstanceReconnecting is the typed grace/boot error callers may match
// with errors.As (design §12.1-1). Handlers map it to the wire code
// backend_unavailable; it must NEVER surface as not_configured (§3.2).
type ErrInstanceReconnecting struct {
	BaseURL  string
	Until    time.Time // grace deadline; zero when Starting
	Starting bool      // true = spawn/boot in flight (not a lost instance)
}

func (e *ErrInstanceReconnecting) Error() string {
	if e.Starting {
		return fmt.Sprintf("dsh web instance starting on %s", e.BaseURL)
	}
	return fmt.Sprintf("dsh web instance reconnecting on %s (grace until %s)",
		e.BaseURL, e.Until.Format(time.RFC3339))
}

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

// managedStarter abstracts "get a dsh web server running on this port" so
// the resolver logic is unit-testable without a real dsh install.
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

// Resolver owns the seat lifecycle for one dshweb Agent.
type Resolver struct {
	probeURLs   []string      // seat = probeURLs[0] (authoritative, design §9)
	binPath     string        // dsh executable for spawn ("" = LookPath)
	extraArgs   []string
	dshHome     string        // optional DSH_HOME override (sandbox experiments/tests)
	dataDir     string        // state persistence dir ("" = no persistence)
	gracePeriod time.Duration // zero = gracePeriodDefault

	httpClient   *http.Client
	managedStart managedStarter

	// mu guards exactly these fields (§3.3); all network I/O and spawn
	// waits happen outside the lock.
	mu           sync.Mutex
	resolved     *ResolvedInstance // nil while dark
	everResolved bool              // this process once held a live seat
	lostAt       time.Time         // seat went dark at; zero while healthy
	lossSeq      uint64            // alive→dark edges seen (terminal-producer idempotence key)
	negUntil     time.Time         // dark-seat probe cache / spawn backoff
	spawning     bool              // a spawn/boot-wait is in flight
	spawnErr     error             // last spawn failure (diagnostics)
	onLost       func()            // fired once per alive→dark transition
}

// ResolverOption configures a Resolver.
type ResolverOption func(*Resolver)

// WithProbeURLs overrides the probe list. The FIRST entry is the seat: it is
// probed, and it is the only port a spawn may bind (design §9 — a configured
// URL makes that port the identity, replacing the 3080 default).
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
// Production leaves it empty: the spawned instance must share the user's real
// ~/.dsh store.
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

// withGracePeriod overrides the grace window (tests only).
func withGracePeriod(d time.Duration) ResolverOption {
	return func(r *Resolver) { r.gracePeriod = d }
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

// NewResolver builds the seat lifecycle manager. Default seat is the dsh web
// default port on loopback.
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
	if r.gracePeriod <= 0 {
		r.gracePeriod = gracePeriodDefault
	}
	return r
}

// seatURL returns the authoritative seat endpoint.
func (r *Resolver) seatURL() string {
	if len(r.probeURLs) > 0 && r.probeURLs[0] != "" {
		return r.probeURLs[0]
	}
	return fmt.Sprintf("http://127.0.0.1:%d", DefaultProbePort)
}

// SetLostCallback registers a callback fired (outside the resolver lock) once
// per alive→dark transition of a held instance. The turn-terminal producer
// (design §12 item 3) hangs off this.
func (r *Resolver) SetLostCallback(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onLost = fn
}

// LossSeq returns how many alive→dark edges this resolver has seen. The
// terminal producer keys its idempotence on this sequence: however many
// probe/stream paths notice one death, each edge fires at most once per
// session, and a later edge re-arms (design §12.1-3).
func (r *Resolver) LossSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lossSeq
}

// GraceState reports whether the seat is inside a reconnect grace window and
// the window's deadline. InstanceStatus consults this to keep the backend
// visible during grace (§3.2 / §12.1-4: never let Current()==nil fall through
// the detector as not_configured while a rebind is still expected).
func (r *Resolver) GraceState() (bool, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.graceStateLocked()
}

func (r *Resolver) graceStateLocked() (bool, time.Time) {
	if r.lostAt.IsZero() {
		return false, time.Time{}
	}
	until := r.lostAt.Add(r.gracePeriod)
	if !time.Now().Before(until) {
		return false, time.Time{}
	}
	return true, until
}

// managedState is the persisted managed-instance record (0600). Write-only
// under the seat model: diagnostics and one-time legacy cleanup read it;
// resolution never does.
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

func (r *Resolver) saveState(inst *ResolvedInstance) {
	path := r.statePath()
	if path == "" || inst.Source != SourceManaged {
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
	// precedent; no credentials exist to protect — but keep the mode).
	_ = core.AtomicWriteFile(path, b, 0o600)
}

// Resolve returns the live instance on the seat. Decision matrix (§3.1):
//
//   - seat answers             → use it (label by ownership, never a dead PID)
//   - held instance died       → grace window: typed error, no adopt, no spawn
//   - grace elapsed, or cold
//     start (never held)       → spawn ON the seat (single-flight, outside mu)
//
// All probes and boot-waits run outside mu; concurrent callers never block on
// a spawn — they receive the typed starting/reconnecting error (§3.3).
func (r *Resolver) Resolve(ctx context.Context) (*ResolvedInstance, error) {
	seat := r.seatURL()

	r.mu.Lock()
	if r.resolved != nil {
		inst := r.resolved
		if time.Now().Before(r.negUntil) {
			// A probe failed <1s ago and the loss transition already ran;
			// defensively re-run it for the resolved!=nil case.
			err := r.loseSeatLocked(inst)
			r.mu.Unlock()
			return nil, err
		}
		r.mu.Unlock()
		if _, err := probeInstance(ctx, r.httpClient, inst.BaseURL); err == nil {
			return inst, nil
		}
		r.mu.Lock()
		err := r.loseSeatLocked(inst)
		r.mu.Unlock()
		return nil, err
	}

	if inGrace, until := r.graceStateLocked(); inGrace {
		if time.Now().Before(r.negUntil) {
			err := &ErrInstanceReconnecting{BaseURL: seat, Until: until}
			r.mu.Unlock()
			return nil, err
		}
		r.mu.Unlock()
		if _, err := probeInstance(ctx, r.httpClient, seat); err == nil {
			inst := &ResolvedInstance{BaseURL: seat, Port: portOf(seat), Source: SourceExternal}
			r.mu.Lock()
			r.rebindLocked(inst, "grace-rebind")
			r.mu.Unlock()
			return inst, nil
		}
		r.mu.Lock()
		r.negUntil = time.Now().Add(seatProbeNegativeCache)
		err := &ErrInstanceReconnecting{BaseURL: seat, Until: r.lostAt.Add(r.gracePeriod)}
		r.mu.Unlock()
		return nil, err
	}

	// Dark seat, no grace: cold start or grace expiry. Probe the seat first —
	// a fresh process must adopt an already-running instance (external) before
	// ever spawning (§3.1 step 1; the 08-16 "external wins" invariant).
	if time.Now().Before(r.negUntil) {
		err := &ErrInstanceReconnecting{BaseURL: seat, Starting: true}
		r.mu.Unlock()
		return nil, err
	}
	r.mu.Unlock()
	if _, err := probeInstance(ctx, r.httpClient, seat); err == nil {
		inst := &ResolvedInstance{BaseURL: seat, Port: portOf(seat), Source: SourceExternal}
		r.mu.Lock()
		r.rebindLocked(inst, "seat-adopt")
		r.mu.Unlock()
		return inst, nil
	}
	r.mu.Lock()
	if r.spawning {
		err := &ErrInstanceReconnecting{BaseURL: seat, Starting: true}
		r.mu.Unlock()
		return nil, err
	}
	r.spawning = true
	everResolved := r.everResolved
	r.mu.Unlock()

	inst, err := r.spawnOnSeat(ctx, seat)

	r.mu.Lock()
	r.spawning = false
	if err != nil {
		r.spawnErr = err
		r.negUntil = time.Now().Add(spawnRetryBackoff)
		r.mu.Unlock()
		return nil, err
	}
	r.spawnErr = nil
	r.resolved = inst
	r.everResolved = true
	r.lostAt = time.Time{}
	r.mu.Unlock()
	slog.Info("dsh-web: instance resolved",
		"source", string(inst.Source), "baseURL", inst.BaseURL,
		"reason", spawnReason(everResolved))
	return inst, nil
}

// loseSeatLocked transitions a held instance into the grace window and
// returns the typed error for the current caller.
func (r *Resolver) loseSeatLocked(prev *ResolvedInstance) error {
	r.resolved = nil
	r.lostAt = time.Now()
	r.lossSeq++
	r.negUntil = r.lostAt.Add(seatProbeNegativeCache)
	until := r.lostAt.Add(r.gracePeriod)
	slog.Info("dsh-web: seat lost — grace window, no adopt/no spawn",
		"baseURL", prev.BaseURL, "source", string(prev.Source),
		"graceUntil", until.Format(time.RFC3339))
	if cb := r.onLost; cb != nil {
		go cb()
	}
	return &ErrInstanceReconnecting{BaseURL: prev.BaseURL, Until: until}
}

// rebindLocked restores a live instance (recovered after grace).
func (r *Resolver) rebindLocked(inst *ResolvedInstance, reason string) {
	r.resolved = inst
	r.everResolved = true
	r.lostAt = time.Time{}
	r.negUntil = time.Time{}
	slog.Info("dsh-web: instance resolved", "source", string(inst.Source),
		"baseURL", inst.BaseURL, "reason", reason)
}

func spawnReason(everResolved bool) string {
	if everResolved {
		return "grace-expiry-respawn"
	}
	return "cold-start"
}

// spawnOnSeat spawns a managed instance bound to the seat and waits for its
// first host.describe — probing the ENDPOINT (not the child), so a user
// instance winning the bind race is adopted as external with no dead PID
// (design §4 race 1 / M5).
func (r *Resolver) spawnOnSeat(ctx context.Context, seat string) (*ResolvedInstance, error) {
	port := portOf(seat)
	if port <= 0 {
		return nil, fmt.Errorf("dshweb: seat URL %q has no port to bind", seat)
	}
	pid, err := r.managedStart.Start(ctx, port)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(managedBootTimeout)
	for {
		if _, err := probeInstance(ctx, r.httpClient, seat); err == nil {
			inst := &ResolvedInstance{BaseURL: seat, Port: port, Source: SourceExternal}
			if processIsAlive(pid) {
				// Our child still holds the port → we own it.
				inst.Source = SourceManaged
				inst.PID = pid
			}
			r.saveState(inst)
			if inst.Source == SourceManaged {
				slog.Info("dsh-web: spawned managed instance on seat",
					"baseURL", seat, "pid", pid)
			} else {
				slog.Info("dsh-web: seat won by external instance during spawn",
					"baseURL", seat, "deadChildPid", pid)
			}
			return inst, nil
		}
		if time.Now().After(deadline) {
			_ = r.managedStart.Stop()
			return nil, fmt.Errorf("dshweb: managed dsh web on %s did not answer host.describe within %s", seat, managedBootTimeout)
		}
		if !processIsAlive(pid) {
			// Child died (likely EADDRINUSE against a squatter). Give the
			// seat one more beat for a real instance, then fail honestly.
			time.Sleep(300 * time.Millisecond)
			if _, err := probeInstance(ctx, r.httpClient, seat); err == nil {
				inst := &ResolvedInstance{BaseURL: seat, Port: port, Source: SourceExternal}
				r.saveState(inst)
				slog.Info("dsh-web: seat won by external instance; spawn child exited",
					"baseURL", seat, "deadChildPid", pid)
				return inst, nil
			}
			_ = r.managedStart.Stop()
			return nil, fmt.Errorf("dshweb: managed dsh web child (pid %d) exited; port %d is not answering (occupied by a non-dsh service or spawn failed)", pid, port)
		}
		select {
		case <-ctx.Done():
			_ = r.managedStart.Stop()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Current returns the cached instance without probing (nil while dark/in
// grace — InstanceStatus consults GraceState first, §12.1-4).
func (r *Resolver) Current() *ResolvedInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolved
}

// LastSpawnErr exposes the most recent spawn failure for diagnostics.
func (r *Resolver) LastSpawnErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.spawnErr
}

// dataDirOf exposes the persistence dir for the one-time legacy cleanup.
func (r *Resolver) dataDirOf() string { return r.dataDir }

// Stop disconnects the resolver WITHOUT killing the instance this process
// spawned (design §5 "不杀 + 下次收养"): the seat keeps serving the user's
// browser across bridge restarts, and the next run adopts it via the seat.
// Failed-spawn children are reaped inside spawnOnSeat itself; this path never
// owns a live child's death anymore. Tests clean up via their own starters.
func (r *Resolver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolved = nil
	return nil
}

func portOf(baseURL string) int {
	if _, portStr, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(baseURL, "http://"), "https://")); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			return p
		}
	}
	return 0
}

// processIsAlive reports whether pid exists (signal 0; EPERM still means the
// process exists). The spawn path uses it to label ownership and never record
// a dead PID (design M5).
func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
