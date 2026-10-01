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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// ErrSeatNotRunning is the typed cold-dark error: the seat is not answering
// and this process has never held it. Cold Resolve no longer spawns
// (2026-09-22 install-and-start plan §5) — the 启动 button (StartSeat) and
// the install back-half are the only spawn paths; handlers map this to
// backend_unavailable like the grace error, never not_configured.
type ErrSeatNotRunning struct {
	BaseURL string
}

func (e *ErrSeatNotRunning) Error() string {
	return fmt.Sprintf("dsh web instance not running on %s (start it from the CordCode Link workstation row, or run dsh web yourself)", e.BaseURL)
}

// ResolvedInstance is one live dsh web instance this backend talks to.
type ResolvedInstance struct {
	BaseURL string         // http://127.0.0.1:<port>
	Port    int            // listen port
	Source  InstanceSource // external | managed
	PID     int            // managed only; 0 for external
}

// probeInstance sends session/list at baseURL and reports whether a dsh web
// API answers (the typert gateway retired host.describe; the list is the
// cheapest authenticated liveness probe). Short timeout — this is a liveness
// probe, not a workload. auth may be nil (pre-auth dsh / tests): no cookie,
// no refresh.
func probeInstance(ctx context.Context, httpClient *http.Client, baseURL string, auth *seatAuth) error {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c := NewClient(baseURL, httpClient)
	c.SetAuth(auth)
	return c.Call(pctx, "session/list", listArgs(), nil)
}

// probeTimeout bounds one session/list probe.
const probeTimeout = 2 * time.Second

// managedBootTimeout bounds how long a freshly spawned `dsh web` may take to
// answer its first session/list probe (profile composition is pnpm/node
// work; be generous rather than flapping between spawn attempts).
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
	binPath   string // explicit pin (option / installer write-back); "" = discover
	extraArgs []string
	dshHome   string // optional DSH_HOME override (tests only; "" = user's ~/.dsh)
	logPath   string // optional stdout/stderr capture
	dataDir   string // install-record source for fresh discovery
	// onLaunchURL, when set, receives every captured child output line —
	// the resolver scans it for dsh's authenticated entry URL (2026-09-23
	// plan §4.3). Nil = inherit streams unchanged (tests).
	onLaunchURL func(string)

	cmd *exec.Cmd
	mu  sync.Mutex
}

// startArgs returns the managed server argv. Exposed for a build-assert test:
// loopback host, explicit port, profile web — and provably never
// --trusted-host (§4.4 red line).
func (s *execManagedStarter) startArgs(port int) []string {
	args := []string{"--profile", "web", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--no-open"}
	return append(args, s.extraArgs...)
}

// resolveBin returns the executable Start will spawn: the explicit pin
// first (option or the installer's write-back), then a fresh discovery so a
// mid-session terminal install is picked up without a runtime restart.
func (s *execManagedStarter) resolveBin() string {
	if s.binPath != "" {
		return s.binPath
	}
	return findDSHBinary(s.dataDir)
}

// setBinPath re-pins the executable (installer write-back, plan §5).
func (s *execManagedStarter) setBinPath(bin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.binPath = bin
}

func (s *execManagedStarter) Start(ctx context.Context, port int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		return 0, fmt.Errorf("dshweb: managed starter already has a process (pid %d)", s.cmd.Process.Pid)
	}
	bin := s.binPath
	if bin == "" {
		bin = findDSHBinary(s.dataDir)
	}
	if bin == "" {
		return 0, fmt.Errorf("dshweb: no dsh binary found (PATH, nvm, install record) — install dsh first")
	}
	cmd := exec.Command(bin, s.startArgs(port)...)
	// Own process group: dsh spawns node children; group kill reaps them all
	// (same posture as agent/dsh and grokbuild).
	prepareCmdForProcessGroup(cmd)
	// The dsh shebang is `#!/usr/bin/env node`; GUI PATH misses nvm installs,
	// so put the binary's own directory first (node lives next to dsh there).
	cmd.Env = prependBinDirToPath(os.Environ(), filepath.Dir(bin))
	if s.dshHome != "" {
		cmd.Env = append(cmd.Env, "DSH_HOME="+s.dshHome)
	}
	var outDest, errDest io.Writer = os.Stdout, os.Stderr
	if s.logPath != "" {
		if f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
			outDest, errDest = f, f
		}
	}
	if s.onLaunchURL != nil {
		// Pipe both streams through a line scanner (the entry URL has only
		// been verified on stdout, but scanning both is harmless) and tee
		// every line to the original destination — diagnostics visibility is
		// unchanged, only the launch URL is additionally captured.
		if pr, pw, err := os.Pipe(); err == nil {
			cmd.Stdout = pw
			go teeAndScan(pr, outDest, s.onLaunchURL)
		}
		if pr, pw, err := os.Pipe(); err == nil {
			cmd.Stderr = pw
			go teeAndScan(pr, errDest, s.onLaunchURL)
		}
	} else {
		cmd.Stdout = outDest
		cmd.Stderr = errDest
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("dshweb: spawn %s: %w", bin, err)
	}
	s.cmd = cmd
	return cmd.Process.Pid, nil
}

// teeAndScan drains one captured child stream: every line is teed to dest
// and offered to onLine (which filters noise itself).
func teeAndScan(r *os.File, dest io.Writer, onLine func(string)) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if dest != nil {
			_, _ = io.WriteString(dest, scanner.Text()+"\n")
		}
		onLine(scanner.Text())
	}
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
	probeURLs   []string // seat = probeURLs[0] (authoritative, design §9)
	binPath     string   // dsh executable for spawn ("" = LookPath)
	extraArgs   []string
	dshHome     string        // optional DSH_HOME override (sandbox experiments/tests)
	dataDir     string        // state persistence dir ("" = no persistence)
	gracePeriod time.Duration // zero = gracePeriodDefault

	httpClient   *http.Client
	managedStart managedStarter
	// auth is the seat's browser-session cookie manager (2026-09-23 plan);
	// nil until SetAuth wires it — pre-auth dsh and tests run without.
	auth *seatAuth

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
		r.managedStart = &execManagedStarter{
			binPath:     r.binPath,
			extraArgs:   r.extraArgs,
			dshHome:     r.dshHome,
			dataDir:     r.dataDir,
			onLaunchURL: r.notifyLaunchURL,
		}
	}
	if r.httpClient == nil {
		r.httpClient = &http.Client{}
	}
	if r.gracePeriod <= 0 {
		r.gracePeriod = gracePeriodDefault
	}
	slog.Info("dsh-web: held probe loss policy", "policy", "dial-refused-only",
		"requestFailuresTerminateTurn", false)
	return r
}

// seatURL returns the authoritative seat endpoint.
func (r *Resolver) seatURL() string {
	if len(r.probeURLs) > 0 && r.probeURLs[0] != "" {
		return r.probeURLs[0]
	}
	return fmt.Sprintf("http://127.0.0.1:%d", DefaultProbePort)
}

// SetAuth wires the seat's browser-session cookie manager (2026-09-23 plan
// §4.2): every probe and client created by this resolver attaches the cookie
// and refreshes on 401. Must be called before the first Resolve.
func (r *Resolver) SetAuth(a *seatAuth) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auth = a
}

// launchURLPattern matches dsh's startup line announcing the authenticated
// browser entry (verified on stdout of 0.1.7-alpha.1, 2026-09-23):
// "dsh web: http://127.0.0.1:3080/?token=<launch token>".
var launchURLPattern = regexp.MustCompile(`^dsh web: (https?://[^\s?]+)/\?token=([A-Za-z0-9_-]+)\s*$`)

// notifyLaunchURL receives captured entry URLs from the exec starter's stdout
// scan and forwards them to the auth manager — but only when the URL names
// this seat's authority (a foreign authority would mint a cookie for the
// wrong seat; the exchange validates the cookie name anyway, this avoids
// pointless exchanges).
func (r *Resolver) notifyLaunchURL(rawURL string) {
	m := launchURLPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return
	}
	u, err := url.Parse(m[1])
	if err != nil {
		return
	}
	if authorityOf(u.Host) != authorityOf(r.seatURL()) {
		return
	}
	r.mu.Lock()
	auth := r.auth
	r.mu.Unlock()
	auth.setLaunchURL(m[1] + "/?token=" + m[2])
	slog.Info("dsh-web: captured launch URL from spawned dsh (token exchange available)")
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

// Resolve returns the live instance on the seat. Decision matrix (§3.1 +
// 2026-09-22 plan §5):
//
//   - seat answers             → use it (label by ownership, never a dead PID)
//   - held probe request fails → preserve its error and the held identity;
//     only a refused dial establishes loss of the seat
//   - held instance died       → grace window: typed error, no adopt, no spawn
//   - grace elapsed (this process once held) → spawn ON the seat
//     (single-flight, outside mu) — the 2026-08-19 respawn contract
//   - cold start (never held)  → probe only, typed ErrSeatNotRunning, NO
//     spawn — the 启动 button / install back-half (StartSeat) are the only
//     explicit spawn paths left
//
// All probes and boot-waits run outside mu; concurrent callers never block on
// a spawn — they receive the typed starting/reconnecting error (§3.3).
func (r *Resolver) Resolve(ctx context.Context) (*ResolvedInstance, error) {
	seat := r.seatURL()

	r.mu.Lock()
	if r.resolved != nil {
		inst := r.resolved
		r.mu.Unlock()
		probeErr := probeInstance(ctx, r.httpClient, inst.BaseURL, r.auth)
		if probeErr == nil {
			return inst, nil
		}
		confirmedLoss := heldProbeConfirmsLoss(ctx, probeErr)
		logHeldProbeFailure(ctx, probeErr, confirmedLoss)
		if !confirmedLoss {
			return nil, probeErr
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		// Another probe may already have lost or rebound this instance while
		// we were outside mu. Never lose a newer identity or emit another edge.
		if r.resolved == inst {
			return nil, r.loseSeatLocked(inst)
		}
		if r.resolved == nil && !r.lostAt.IsZero() {
			return nil, &ErrInstanceReconnecting{BaseURL: seat, Until: r.lostAt.Add(r.gracePeriod)}
		}
		return nil, probeErr
	}

	if inGrace, until := r.graceStateLocked(); inGrace {
		if time.Now().Before(r.negUntil) {
			err := &ErrInstanceReconnecting{BaseURL: seat, Until: until}
			r.mu.Unlock()
			return nil, err
		}
		r.mu.Unlock()
		if err := probeInstance(ctx, r.httpClient, seat, r.auth); err == nil {
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

	// Dark seat, no grace: probe the seat first — a fresh process must adopt
	// an already-running instance (external) before ever spawning (§3.1
	// step 1; the 08-16 "external wins" invariant).
	if time.Now().Before(r.negUntil) {
		if !r.everResolved {
			// Cold negative cache: the seat is dark and we never held it.
			r.mu.Unlock()
			return nil, &ErrSeatNotRunning{BaseURL: seat}
		}
		err := &ErrInstanceReconnecting{BaseURL: seat, Starting: true}
		r.mu.Unlock()
		return nil, err
	}
	r.mu.Unlock()
	if err := probeInstance(ctx, r.httpClient, seat, r.auth); err == nil {
		inst := &ResolvedInstance{BaseURL: seat, Port: portOf(seat), Source: SourceExternal}
		r.mu.Lock()
		r.rebindLocked(inst, "seat-adopt")
		r.mu.Unlock()
		return inst, nil
	}
	r.mu.Lock()
	if !r.everResolved {
		// Cold start (this process never held the seat): probe only, NO
		// spawn (2026-09-22 plan §5) — the row shows 未启动 and the user
		// decides via 启动 / 安装. Opening Link, refreshing, 重新检查 and
		// diagnostics must never be a hidden spawn path.
		r.negUntil = time.Now().Add(seatProbeNegativeCache)
		r.mu.Unlock()
		return nil, &ErrSeatNotRunning{BaseURL: seat}
	}
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

// A failed session/list is not a turn terminal in dsh. An HTTP/RPC response,
// timeout, cancellation or broken request connection cannot establish that
// the listening instance disappeared. A refused dial to the held seat can.
func heldProbeConfirmsLoss(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var carrier *carrierError
	var transport *net.OpError
	return errors.As(err, &carrier) && carrier.Status == 0 &&
		errors.As(err, &transport) && transport.Op == "dial" &&
		errors.Is(err, syscall.ECONNREFUSED)
}

// Record shape, never response bodies or RPC messages/details: probes may
// carry private session data. Historical logs lacked this distinction.
func logHeldProbeFailure(ctx context.Context, err error, confirmedLoss bool) {
	category := "request_error"
	fields := []any{"operation", "session/list", "confirmedSeatLoss", confirmedLoss}
	var rpc *RPCError
	var carrier *carrierError
	var transport *net.OpError
	switch {
	case ctx.Err() != nil:
		category = "caller_context"
		fields = append(fields, "contextError", ctx.Err().Error())
	case errors.Is(err, context.DeadlineExceeded):
		category = "probe_timeout"
	case errors.As(err, &rpc):
		category = "rpc_error"
		fields = append(fields, "rpcCode", rpc.Code)
	case errors.As(err, &carrier):
		category = "transport_error"
		fields = append(fields, "httpStatus", carrier.Status)
		if carrier.Status != 0 {
			category = "http_response_error"
		}
	}
	if errors.As(err, &transport) {
		fields = append(fields, "transportOp", transport.Op)
		var errno syscall.Errno
		if errors.As(err, &errno) {
			fields = append(fields, "errno", int(errno))
		}
	}
	fields = append(fields, "category", category)
	slog.Warn("dsh-web: held seat probe failed", fields...)
}

// StartSeat is the explicit user-driven seat start (2026-09-22 plan §5: the
// 启动 button and the install back-half are the ONLY spawn paths left). It
// probes the seat first — an answering instance (whoever spawned it) is
// adopted, never a second process — then spawns ON the seat and waits for
// the first host.describe. Single-flight: a concurrent caller gets the typed
// starting error immediately. The explicit click bypasses the probe negative
// cache so every attempt re-checks the seat.
func (r *Resolver) StartSeat(ctx context.Context) (*ResolvedInstance, error) {
	seat := r.seatURL()
	r.mu.Lock()
	if r.resolved != nil {
		inst := r.resolved
		r.mu.Unlock()
		return inst, nil
	}
	if r.spawning {
		err := &ErrInstanceReconnecting{BaseURL: seat, Starting: true}
		r.mu.Unlock()
		return nil, err
	}
	r.spawning = true
	r.mu.Unlock()

	if err := probeInstance(ctx, r.httpClient, seat, r.auth); err == nil {
		inst := &ResolvedInstance{BaseURL: seat, Port: portOf(seat), Source: SourceExternal}
		r.mu.Lock()
		r.spawning = false
		r.rebindLocked(inst, "start-seat-adopt")
		r.mu.Unlock()
		return inst, nil
	}

	inst, err := r.spawnOnSeat(ctx, seat)
	r.mu.Lock()
	r.spawning = false
	if err != nil {
		r.spawnErr = err
		r.mu.Unlock()
		return nil, err
	}
	r.spawnErr = nil
	r.resolved = inst
	r.everResolved = true
	r.lostAt = time.Time{}
	r.mu.Unlock()
	slog.Info("dsh-web: instance resolved",
		"source", string(inst.Source), "baseURL", inst.BaseURL, "reason", "start-seat")
	return inst, nil
}

// SetManagedBinary pins the dsh executable for the managed spawn — the
// installer's write-back so a fresh install is used without a runtime
// restart (plan §5: 安装后的 bin 绝对路径写回 resolver 的 cli_path).
func (r *Resolver) SetManagedBinary(bin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.binPath = bin
	if st, ok := r.managedStart.(*execManagedStarter); ok {
		st.setBinPath(bin)
	}
}

// effectiveBinary returns the dsh executable the spawn path would use: the
// explicit pin (option or installer write-back) first, then a fresh
// discovery (install record → PATH → nvm). Empty = no binary anywhere —
// readiness reports not_detected without consulting the port.
func (r *Resolver) effectiveBinary() string {
	r.mu.Lock()
	explicit := r.binPath
	r.mu.Unlock()
	if explicit != "" {
		return explicit
	}
	return findDSHBinary(r.dataDir)
}

// prependBinDirToPath puts the binary's directory first in the child PATH so
// the dsh shebang (`#!/usr/bin/env node`) resolves node from the same
// install (nvm keeps node next to dsh; the GUI PATH misses them).
func prependBinDirToPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	rest := make([]string, 0, len(env))
	pathVal := ""
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			pathVal = strings.TrimPrefix(e, "PATH=")
			continue
		}
		rest = append(rest, e)
	}
	if pathVal == "" {
		pathVal = os.Getenv("PATH")
	}
	return append([]string{"PATH=" + dir + string(os.PathListSeparator) + pathVal}, rest...)
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
		if err := probeInstance(ctx, r.httpClient, seat, r.auth); err == nil {
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
			if err := probeInstance(ctx, r.httpClient, seat, r.auth); err == nil {
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
