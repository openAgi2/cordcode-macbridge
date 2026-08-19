package dshweb

// One-time legacy cleanup (design §6): the retired 3096–3196 managed port
// range could leave an orphan `dsh web` behind (the 2026-08-19 incident's
// pid-1406 shape, alive 33h+). On agent construction we reap it — but only
// when the evidence still matches the record: PID alive AND its command line
// still looks like dsh AND the recorded port is still listening. PID reuse
// makes any weaker check a wrong-process kill; on mismatch we delete the
// state file and warn instead.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The retired managed port range (opencode-managed precedent, 08-16 design
// §4.2). Kept ONLY here, for identifying legacy records — no new spawn may
// ever bind these.
const (
	legacyManagedPortMin = 3096
	legacyManagedPortMax = 3196
)

// killGrace bounds TERM→KILL escalation for the legacy reap.
const killGrace = 5 * time.Second

// CleanupLegacyManaged reaps a stray legacy-range managed instance recorded
// in dataDir's state file, then deletes the record. It returns what happened
// (for diagnostics). cmdline and port checks are injectable for tests; nil
// means the platform defaults.
func CleanupLegacyManaged(dataDir string, cmdlineFn func(pid int) (string, bool)) (killed bool, note string) {
	if dataDir == "" {
		return false, "no data dir"
	}
	path := dataDir + string(os.PathSeparator) + managedStateFile
	b, err := os.ReadFile(path)
	if err != nil {
		return false, "no legacy state file"
	}
	var st managedState
	if err := json.Unmarshal(b, &st); err != nil {
		_ = os.Remove(path)
		return false, "unreadable state file, removed"
	}
	if st.Source != string(SourceManaged) || st.Port <= 0 {
		_ = os.Remove(path)
		return false, "non-managed state record, removed"
	}
	if st.Port < legacyManagedPortMin || st.Port > legacyManagedPortMax {
		// Current-era record (seat-bound): keep it — it documents OUR live
		// instance for diagnostics.
		return false, fmt.Sprintf("current-era seat record (port %d) kept", st.Port)
	}

	// Legacy range: reap only on full evidence match.
	if st.PID <= 0 || !processIsAlive(st.PID) {
		_ = os.Remove(path)
		return false, fmt.Sprintf("legacy record port %d: pid %d gone, state removed", st.Port, st.PID)
	}
	if cmdlineFn == nil {
		cmdlineFn = processCommandLine
	}
	cmdline, ok := cmdlineFn(st.PID)
	if !ok || !containsDishCommand(cmdline) {
		_ = os.Remove(path)
		slog.Warn("dsh-web: legacy managed record did not match a dsh process (pid reuse?) — state removed, nothing killed",
			"port", st.Port, "pid", st.PID, "cmdline", cmdline)
		return false, fmt.Sprintf("pid %d reused (cmdline mismatch), state removed, not killed", st.PID)
	}
	if !portStillListening(st.Port) {
		_ = os.Remove(path)
		return false, fmt.Sprintf("legacy record port %d not listening anymore, state removed, not killed", st.Port)
	}

	// Evidence matches: TERM → KILL, then forget the record.
	if err := syscall.Kill(st.PID, syscall.SIGTERM); err != nil {
		_ = os.Remove(path)
		return false, fmt.Sprintf("TERM pid %d failed: %v, state removed", st.PID, err)
	}
	deadline := time.Now().Add(killGrace)
	for processIsAlive(st.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if processIsAlive(st.PID) {
		_ = syscall.Kill(st.PID, syscall.SIGKILL)
	}
	_ = os.Remove(path)
	return true, fmt.Sprintf("killed legacy managed instance pid %d on port %d, state removed", st.PID, st.Port)
}

// containsDishCommand reports whether a command line still looks like a dsh
// invocation ("dsh" as a token or inside a path component — node
// /opt/homebrew/bin/dsh web …).
func containsDishCommand(cmdline string) bool {
	for _, field := range strings.Fields(cmdline) {
		if base := filepath.Base(field); base == "dsh" || base == "dsh.exe" {
			return true
		}
	}
	return false
}

// portStillListening reports whether something still accepts on the port
// (bind failure = occupied).
func portStillListening(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}
