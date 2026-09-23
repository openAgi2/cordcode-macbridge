package dshweb

// Structured instance readiness (2026-09-22 install-and-start plan §3/§5):
// the descriptor-facing, read-only seam that replaces the boolean
// InstanceStatus fold. One discrimination sequence, fail-closed, never
// collapsing any state into not_configured:
//
//  1. binary missing (search paths + install record) → not_detected — the
//     port is NOT consulted; a non-dsh squatter on 3080 does not change this
//     row (install is the only forward action).
//  2. read-only session/list on the seat → available.
//  3. describe failed → TCP dial: refused = nothing listening →
//     service_not_running.
//  4. dial connected → lsof: command line contains dsh → service_not_running
//     (process there, interface not ready); no dsh → port_conflict with the
//     squatter's command line as the detail; lsof failure guesses nothing —
//     stay service_not_running.
//
// The grace window keeps the row available (reconnecting detail); the
// 2026-08-19 grace contract is unchanged. 「安装中」「启动中」 are Mac-row
// local states — this seam keeps reporting the underlying status.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Readiness statuses (wire AgentStatus values; no new enums invented —
// not_detected / service_not_running / port_conflict already exist).
const (
	ReadinessAvailable         = "available"
	ReadinessNotDetected       = "not_detected"
	ReadinessServiceNotRunning = "service_not_running"
	ReadinessPortConflict      = "port_conflict"
)

// readinessProbeTimeout bounds the read-only session/list probe inside the
// readiness chain. Shorter than the resolver's 2s probeTimeout: the whole
// chain must stay well under the management API's 2s WriteTimeout (GET
// /internal/agents re-reads this seam on every poll).
const readinessProbeTimeout = 800 * time.Millisecond

// readinessDialTimeout bounds the TCP "is anything listening" check.
const readinessDialTimeout = 300 * time.Millisecond

// readinessCacheTTL memoizes the cold-discrimination verdict so descriptor
// polls (GET /internal/agents every few seconds, hello_ack rebuilds) do not
// re-pay the probe chain. Held-instance and grace answers are computed fresh
// (they are lock reads).
const readinessCacheTTL = 2 * time.Second

// lsofSeatListener is the port-squatter discriminator, fakeable in tests.
var lsofSeatListener = seatListenerCommandLine

// seatListenerCommandLine finds who listens on the port (lsof) and returns
// that process's full command line (ps). lsof's own COMMAND column is the
// truncated executable name ("node"), which cannot prove a dsh instance;
// the full argv can.
func seatListenerCommandLine(port int) (string, bool) {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fp").Output()
	if err != nil || len(out) == 0 {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "p") || len(line) < 2 {
			continue
		}
		pid, err := strconv.Atoi(line[1:])
		if err != nil {
			continue
		}
		if cmdline, ok := processCommandLine(pid); ok && strings.TrimSpace(cmdline) != "" {
			return cmdline, true
		}
	}
	return "", false
}

// StructuredInstanceReadiness implements the descriptor-facing read-only
// readiness seam. It never spawns and never adopts; the resolver keeps
// owning every mutation.
func (a *Agent) StructuredInstanceReadiness() (string, string) {
	// Held live instance: mirror the resolver's cached truth.
	if inst := a.resolver.Current(); inst != nil {
		switch inst.Source {
		case SourceExternal:
			return ReadinessAvailable, fmt.Sprintf("external dsh web instance at %s", inst.BaseURL)
		case SourceManaged:
			return ReadinessAvailable, fmt.Sprintf("managed dsh web instance at %s (pid %d)", inst.BaseURL, inst.PID)
		}
	}
	// Grace window: stay available with the reconnecting detail (2026-08-19
	// §3.2/§12.1-4 — a grace row must never fall through as unavailable).
	if inGrace, until := a.resolver.GraceState(); inGrace {
		return ReadinessAvailable, fmt.Sprintf("instance reconnecting (grace until %s)", until.Format(time.RFC3339))
	}

	a.readiness.mu.Lock()
	if time.Now().Before(a.readiness.expiresAt) {
		status, detail := a.readiness.status, a.readiness.detail
		a.readiness.mu.Unlock()
		return status, detail
	}
	a.readiness.mu.Unlock()

	status, detail := a.coldReadiness()

	a.readiness.mu.Lock()
	a.readiness.status, a.readiness.detail = status, detail
	a.readiness.expiresAt = time.Now().Add(readinessCacheTTL)
	a.readiness.mu.Unlock()
	return status, detail
}

// coldReadiness runs the §5 discrimination sequence (read-only).
func (a *Agent) coldReadiness() (string, string) {
	seat := a.resolver.seatURL()

	// 1. Binary first — none means not_detected regardless of the port.
	bin := a.resolver.effectiveBinary()
	if bin == "" {
		return ReadinessNotDetected, "dsh CLI not found (searched PATH, nvm, and the CordCode install record)"
	}

	// 2. Read-only session/list (with the browser-session cookie; a 401
	// here triggers the single-flight refresh — the 场景 A path where an
	// externally-held seat goes green on first open, 2026-09-23 plan §4.6).
	ctx, cancel := context.WithTimeout(context.Background(), readinessProbeTimeout)
	defer cancel()
	describeErr := probeInstance(ctx, a.resolver.httpClient, seat, a.auth)
	if describeErr == nil {
		return ReadinessAvailable, fmt.Sprintf("dsh web instance answering on %s", seat)
	}

	// 3. TCP: is anything listening at all?
	host, port := seatHostPort(seat)
	if port <= 0 {
		return ReadinessServiceNotRunning, fmt.Sprintf("dsh web seat %s is not answering", seat)
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), readinessDialTimeout)
	defer dialCancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		// Nothing listening → 未启动. A concrete spawn failure (from 启动 or
		// the install back-half) is the more specific detail — it may cover
		// the subtitle (§5).
		if sp := a.resolver.LastSpawnErr(); sp != nil {
			return ReadinessServiceNotRunning, sp.Error()
		}
		return ReadinessServiceNotRunning, fmt.Sprintf("dsh web not running on %s (binary at %s)", seat, bin)
	}
	_ = conn.Close()

	// 4. Something holds the port: lsof decides dsh vs squatter.
	cmdline, ok := lsofSeatListener(port)
	if !ok {
		return ReadinessServiceNotRunning, "端口开着但 session/list 失败，lsof 不可用"
	}
	if containsDishCommand(cmdline) {
		if isUnauthorized(describeErr) {
			// dsh ≥0.1.6-alpha's browser auth rejected us and the automatic
			// cookie acquisition failed — name the real cause (plan §4.6).
			return ReadinessServiceNotRunning, "dsh 进程已在监听，但认证失败（自动获取登录态未成功，详见日志）"
		}
		return ReadinessServiceNotRunning, fmt.Sprintf("dsh 进程已在 %s 监听，接口尚未就绪（%s）", seat, cmdline)
	}
	return ReadinessPortConflict, cmdline
}

// isUnauthorized reports whether err is the browser-auth 401 carrier error.
func isUnauthorized(err error) bool {
	var ce *carrierError
	return err != nil && errors.As(err, &ce) && ce.Status == http.StatusUnauthorized
}

// seatHostPort splits a seat base URL into host and numeric port.
func seatHostPort(seat string) (string, int) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(seat, "http://"), "https://")
	host, portStr, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "127.0.0.1", 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return host, 0
	}
	return host, port
}

// readinessCache memoizes one cold-discrimination verdict.
type readinessCache struct {
	mu        sync.Mutex
	expiresAt time.Time
	status    string
	detail    string
}
