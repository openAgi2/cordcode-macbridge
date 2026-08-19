package dshweb

// §6/§8.8: the one-time legacy reap is PID-safe — every mismatch path
// (PID reuse, cmdline drift, port released) must remove the state and warn
// without killing; only full evidence match kills. Stop() must leave the
// spawned seat instance running (design §5 不杀+收养).

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeLegacyState(t *testing.T, dir string, port, pid int) {
	t.Helper()
	st := managedState{Version: 1, Source: "managed",
		URL: fmt.Sprintf("http://127.0.0.1:%d", port), Port: port, PID: pid,
		UpdatedAt: "2026-08-18T03:37:13Z"}
	b, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(dir, managedStateFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stateExists(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, managedStateFile))
	return err == nil
}

func TestCleanupLegacyNoStateFile(t *testing.T) {
	killed, note := CleanupLegacyManaged(t.TempDir(), nil)
	if killed || note != "no legacy state file" {
		t.Fatalf("empty dir must no-op: %v %q", killed, note)
	}
}

func TestCleanupLegacyKeepsCurrentEraSeatRecord(t *testing.T) {
	dir := t.TempDir()
	writeLegacyState(t, dir, 3080, os.Getpid()) // seat-era record, not legacy
	killed, _ := CleanupLegacyManaged(dir, nil)
	if killed {
		t.Fatal("seat-era record must never be killed")
	}
	if !stateExists(t, dir) {
		t.Fatal("seat-era record must be kept for diagnostics")
	}
}

func TestCleanupLegacyDeadPIDRemovesStateWithoutKill(t *testing.T) {
	dir := t.TempDir()
	writeLegacyState(t, dir, 3096, 1<<22) // legacy range, surely-dead pid
	killed, note := CleanupLegacyManaged(dir, nil)
	if killed {
		t.Fatal("dead pid must not report a kill")
	}
	if stateExists(t, dir) {
		t.Fatal("state must be removed")
	}
	if note == "" {
		t.Fatal("note must explain")
	}
}

func TestCleanupLegacyPIDReuseMismatchDoesNotKill(t *testing.T) {
	dir := t.TempDir()
	writeLegacyState(t, dir, 3096, os.Getpid()) // alive, but the cmdline is a test binary
	notDish := func(pid int) (string, bool) { return "/usr/bin/python3 unrelated", true }
	killed, _ := CleanupLegacyManaged(dir, notDish)
	if killed {
		t.Fatal("cmdline mismatch must never kill (PID reuse)")
	}
	if stateExists(t, dir) {
		t.Fatal("state must be removed on mismatch")
	}
	// Our own process obviously survived.
	if !processIsAlive(os.Getpid()) {
		t.Fatal("cleanup killed the wrong process!")
	}
}

func TestCleanupLegacyPortReleasedDoesNotKill(t *testing.T) {
	// A legacy-range port that nothing listens on (bind succeeds ⇒ released).
	port := 0
	for p := 3096; p <= 3196; p++ {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			_ = ln.Close()
			port = p
			break
		}
	}
	if port == 0 {
		t.Skip("entire legacy range occupied")
	}
	dir := t.TempDir()
	writeLegacyState(t, dir, port, os.Getpid())
	dish := func(pid int) (string, bool) { return "node /opt/homebrew/bin/dsh --profile web", true }
	killed, _ := CleanupLegacyManaged(dir, dish)
	if killed {
		t.Fatal("released port must not kill")
	}
	if stateExists(t, dir) {
		t.Fatal("state must be removed")
	}
}

func TestCleanupLegacyFullMatchKillsOrphan(t *testing.T) {
	// Real child process standing in for the orphan; a real listener holds a
	// legacy-range port; injected cmdline looks like dsh. Full evidence match
	// → TERM/KILL the child, remove the state.
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Skipf("cannot spawn helper: %v", err)
	}
	defer func() { _ = child.Process.Kill() }()

	// Find a free port in the legacy range and hold it.
	var ln net.Listener
	var port int
	for p := 3196; p >= 3096; p-- {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			ln, port = l, p
			break
		}
	}
	if ln == nil {
		t.Skip("no free port in legacy range")
	}
	defer ln.Close()

	dir := t.TempDir()
	writeLegacyState(t, dir, port, child.Process.Pid)
	dish := func(pid int) (string, bool) {
		return "node /opt/homebrew/bin/dsh --profile web --host 127.0.0.1 --port " + fmt.Sprint(port), true
	}
	killed, note := CleanupLegacyManaged(dir, dish)
	if !killed {
		t.Fatalf("full match must kill: %q", note)
	}
	if stateExists(t, dir) {
		t.Fatal("state must be removed after reap")
	}
	// The child is OUR test child, so it lingers as a zombie until Wait —
	// processIsAlive keeps succeeding on zombies (real orphans get reaped by
	// launchd). Wait and assert it died from a signal instead of polling.
	waitErr := child.Wait()
	if waitErr == nil {
		t.Fatal("expected the reaped child to exit with a signal error")
	}
}

func TestStopLeavesSpawnedSeatInstanceRunning(t *testing.T) {
	// Design §5: bridge shutdown must not tear down the seat — the browser
	// keeps its UI and the next bridge run adopts the same instance.
	r, starter, seat, _ := holdSeat(t, time.Second)
	_ = seat
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if starter.stops != 0 {
		t.Fatalf("Stop() must not terminate the spawned instance (stops=%d)", starter.stops)
	}
	// The seat still answers after Stop.
	if _, err := probeInstance(context.Background(), r.httpClient, r.seatURL()); err != nil {
		t.Fatalf("seat must keep serving after Stop: %v", err)
	}
	_ = starter.Stop() // test hygiene
}
