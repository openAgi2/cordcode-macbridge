package dshweb

// Structured readiness tests (2026-09-22 install-and-start plan §3/§5/§7):
// the full status-table rows — not_detected (binary first, port NOT
// consulted even under a non-dsh squatter), available (answering seat /
// grace), service_not_running (dark seat / dsh listener not ready / lsof
// unavailable), port_conflict (non-dsh squatter with the lsof command line
// as detail). All read-only: the starter must never be called.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readinessAgent builds an Agent around a resolver on the given seat with a
// fake starter and an explicit fake binary (WithManagedBinary pins
// effectiveBinary without touching the real PATH).
func readinessAgent(t *testing.T, seat string) (*Agent, *countingStarter) {
	t.Helper()
	starter := &countingStarter{}
	r := NewResolver(
		WithProbeURLs([]string{seat}),
		WithDataDir(t.TempDir()),
		withManagedStarter(starter),
		WithManagedBinary("/usr/local/bin/dsh-fake", nil),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}),
	)
	return &Agent{resolver: r}, starter
}

// withFakeLsof swaps the lsof discriminator for the test and restores it.
func withFakeLsof(t *testing.T, fn func(port int) (string, bool)) {
	t.Helper()
	orig := lsofSeatListener
	lsofSeatListener = fn
	t.Cleanup(func() { lsofSeatListener = orig })
}

// silentListener binds a plain TCP listener (something IS listening, but it
// does not answer host.describe) — the squatter stand-in.
func silentListener(t *testing.T, port int) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("bind squatter: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func TestReadinessNoBinaryIsNotDetectedEvenWithSquatter(t *testing.T) {
	// §3 row 1 + round-3 组合态：no binary wins over port occupancy — the
	// port is not consulted, install is the only forward action.
	seat := freeLoopbackSeat(t)
	starter := &countingStarter{}
	r := NewResolver(
		WithProbeURLs([]string{seat}),
		WithDataDir(t.TempDir()),
		withManagedStarter(starter),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}),
	)
	// No WithManagedBinary and a data dir without a record: effectiveBinary
	// falls to PATH/nvm. Pin HOME and PATH to dirs without any dsh so the
	// dev machine's real homebrew/nvm installs cannot leak in.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin")
	a := &Agent{resolver: r}

	// A non-dsh squatter holds the seat.
	_, port := seatHostPort(seat)
	silentListener(t, port)

	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessNotDetected {
		t.Fatalf("no binary must be not_detected even with a squatter, got %q (%q)", status, detail)
	}
	if starter.starts != 0 {
		t.Fatalf("readiness must never spawn (starts=%d)", starter.starts)
	}
}

func TestReadinessAnsweringSeatIsAvailable(t *testing.T) {
	seat := freeLoopbackSeat(t)
	_, port := seatHostPort(seat)
	srv := seatServer(t, port)
	defer srv.Close()

	a, starter := readinessAgent(t, seat)
	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessAvailable {
		t.Fatalf("answering seat must be available, got %q (%q)", status, detail)
	}
	if starter.starts != 0 {
		t.Fatalf("readiness must never spawn (starts=%d)", starter.starts)
	}
}

func TestReadinessDarkSeatIsServiceNotRunning(t *testing.T) {
	seat := freeLoopbackSeat(t) // nothing binds it
	a, starter := readinessAgent(t, seat)
	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("dark seat must be service_not_running, got %q (%q)", status, detail)
	}
	if !strings.Contains(detail, "not running") {
		t.Fatalf("detail must say not running: %q", detail)
	}
	if starter.starts != 0 {
		t.Fatalf("readiness must never spawn (starts=%d)", starter.starts)
	}
}

func TestReadinessPortConflictNonDshSquatter(t *testing.T) {
	seat := freeLoopbackSeat(t)
	_, port := seatHostPort(seat)
	silentListener(t, port) // listening, but host.describe fails
	withFakeLsof(t, func(int) (string, bool) {
		return "/usr/sbin/httpd -D FOREGROUND", true
	})

	a, starter := readinessAgent(t, seat)
	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessPortConflict {
		t.Fatalf("non-dsh squatter must be port_conflict, got %q (%q)", status, detail)
	}
	if detail != "/usr/sbin/httpd -D FOREGROUND" {
		t.Fatalf("port_conflict detail must be the lsof command line, got %q", detail)
	}
	if starter.starts != 0 {
		t.Fatalf("readiness must never spawn (starts=%d)", starter.starts)
	}
}

func TestReadinessDshListenerNotReadyIsServiceNotRunning(t *testing.T) {
	// §5: TCP connects, lsof command line contains dsh → the process is
	// there but the interface is not ready — service_not_running, not
	// port_conflict.
	seat := freeLoopbackSeat(t)
	_, port := seatHostPort(seat)
	silentListener(t, port)
	withFakeLsof(t, func(int) (string, bool) {
		return "/opt/homebrew/bin/dsh --profile web --host 127.0.0.1 --port 3080 --no-open", true
	})

	a, _ := readinessAgent(t, seat)
	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("dsh listener not ready must be service_not_running, got %q (%q)", status, detail)
	}
	if !strings.Contains(detail, "dsh") {
		t.Fatalf("detail must carry the dsh command line: %q", detail)
	}
}

func TestReadinessLsofUnavailableGuessesNothing(t *testing.T) {
	// §5: lsof fails → do not guess the occupant; stay service_not_running.
	seat := freeLoopbackSeat(t)
	_, port := seatHostPort(seat)
	silentListener(t, port)
	withFakeLsof(t, func(int) (string, bool) { return "", false })

	a, _ := readinessAgent(t, seat)
	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("lsof failure must stay service_not_running, got %q (%q)", status, detail)
	}
	if !strings.Contains(detail, "lsof 不可用") {
		t.Fatalf("detail must disclose lsof unavailability: %q", detail)
	}
}

func TestReadinessSpawnErrorCoversServiceNotRunningDetail(t *testing.T) {
	// §3 last row: after a failed 启动 (or install back-half), the spawn
	// error text is kept as the service_not_running detail.
	seat := freeLoopbackSeat(t)
	a, _ := readinessAgent(t, seat)
	a.resolver.mu.Lock()
	a.resolver.spawnErr = fmt.Errorf("dshweb: managed dsh web child (pid 4242) exited; port is not answering")
	a.resolver.mu.Unlock()

	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("spawn failure keeps service_not_running, got %q (%q)", status, detail)
	}
	if !strings.Contains(detail, "pid 4242") {
		t.Fatalf("detail must retain the spawn error text: %q", detail)
	}
}

func TestReadinessGraceStaysAvailable(t *testing.T) {
	r := graceFixture(t)
	a := &Agent{resolver: r}
	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessAvailable || !strings.Contains(detail, "reconnecting") {
		t.Fatalf("grace must stay available with reconnecting detail: %q (%q)", status, detail)
	}
}

func TestReadinessHeldInstanceIsAvailable(t *testing.T) {
	r, _, _, _ := holdSeat(t, time.Second)
	a := &Agent{resolver: r}
	status, detail := a.StructuredInstanceReadiness()
	if status != ReadinessAvailable || !strings.Contains(detail, "instance at") {
		t.Fatalf("held instance must be available: %q (%q)", status, detail)
	}
}

func TestReadinessCacheAvoidsReprobeStorm(t *testing.T) {
	// The cold verdict is memoized for readinessCacheTTL: a second call
	// within the window must not re-probe the seat (count describe hits).
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		describeHandler(w, r)
	}))
	defer srv.Close()

	a, _ := readinessAgent(t, srv.URL)
	for i := 0; i < 5; i++ {
		if status, _ := a.StructuredInstanceReadiness(); status != ReadinessAvailable {
			t.Fatalf("call %d: expected available", i+1)
		}
	}
	if hits != 1 {
		t.Fatalf("cached readiness re-probed: %d describe hits, want 1", hits)
	}
}

func TestReadinessInstallRecordBinSurvivesPathGap(t *testing.T) {
	// §4/§5: the install record's absolute path is a cli_path source — a
	// CordCode-prefix install must be found even when PATH has no dsh.
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()
	bin := filepath.Join(dataDir, fallbackPrefixDir, "node_modules", ".bin", "dsh")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeInstallRecord(dataDir, bin, InstallScopeCordcodePrefix, "0.1.5-fake"); err != nil {
		t.Fatal(err)
	}

	r := NewResolver(WithDataDir(dataDir))
	if got := r.effectiveBinary(); got != bin {
		t.Fatalf("install record bin must win, got %q", got)
	}

	// And the readiness flips from not_detected to service_not_running on a
	// dark seat (binary exists now).
	seat := freeLoopbackSeat(t)
	r2 := NewResolver(
		WithProbeURLs([]string{seat}),
		WithDataDir(dataDir),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}),
	)
	a := &Agent{resolver: r2}
	if status, _ := a.StructuredInstanceReadiness(); status != ReadinessServiceNotRunning {
		t.Fatalf("record-backed binary on dark seat must be service_not_running, got %q", status)
	}
}

func TestColdResolveErrorMapsBackendUnavailableShape(t *testing.T) {
	// The typed cold error exists for errors.As matching (go-bridge maps it
	// to backend_unavailable alongside ErrInstanceReconnecting).
	seat := freeLoopbackSeat(t)
	r := NewResolver(WithProbeURLs([]string{seat}))
	_, err := r.Resolve(context.Background())
	var nr *ErrSeatNotRunning
	if !errors.As(err, &nr) {
		t.Fatalf("cold resolve error must be ErrSeatNotRunning, got %T", err)
	}
}
