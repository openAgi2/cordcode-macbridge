package gobridge

// P2 zombie-watchdog tests (docs/2026-10-13-codex-remote-absence-hardening-
// p2-plan.md §3 D3 / S-3): the judgment is a pure function with explicit
// boundaries; the loop emits the one-time armed feature line at startup,
// WARNs on threshold breaches (unthrottled), survives sample failures, and
// exits on ctx cancel.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestZombieWatchdogAssessBoundaries(t *testing.T) {
	cases := []struct {
		children, zombies, threshold int
		want                         bool
	}{
		{0, 0, 64, false},   // healthy quiet runtime
		{64, 0, 64, false},  // at threshold: not a breach
		{65, 0, 64, true},   // live-process leak beyond threshold
		{1, 0, 64, false},   // a single live child is a normal topology
		{0, 1, 64, true},    // one zombie is already a regression
		{100, 3, 64, true},  // both
		{0, 0, 0, false},    // zero threshold, zero counts
	}
	for _, tc := range cases {
		if got := zombieWatchdogAssess(tc.children, tc.zombies, tc.threshold); got != tc.want {
			t.Fatalf("assess(%d,%d,%d) = %v, want %v", tc.children, tc.zombies, tc.threshold, got, tc.want)
		}
	}
}

func TestSampleChildProcessesCountsChildrenAndZombies(t *testing.T) {
	// Deterministic construction: an alive child counts, an unreaped exited
	// child counts as a zombie, and reaping removes it. (Never assert the
	// test process's total zombie count — other tests in the binary may
	// leave unreaped children behind.)
	alive := exec.Command("sleep", "2")
	if err := alive.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = alive.Process.Kill(); _ = alive.Wait() }()
	dead := exec.Command("true") // exits immediately; deliberately no Wait
	if err := dead.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var beforeChildren, beforeZombies int
	for time.Now().Before(deadline) {
		var err error
		beforeChildren, beforeZombies, err = sampleChildProcesses(os.Getpid())
		if err != nil {
			t.Fatalf("sample: %v", err)
		}
		if beforeZombies >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if beforeChildren < 2 {
		t.Fatalf("expected ≥2 children (alive+dead), got %d", beforeChildren)
	}
	if beforeZombies < 1 {
		t.Fatal("unreaped child must count as a zombie")
	}
	_ = dead.Wait()
	afterChildren, afterZombies, err := sampleChildProcesses(os.Getpid())
	if err != nil {
		t.Fatalf("sample after reap: %v", err)
	}
	if afterZombies != beforeZombies-1 {
		t.Fatalf("reaping must remove exactly our zombie: before=%d after=%d", beforeZombies, afterZombies)
	}
	if afterChildren != beforeChildren-1 {
		t.Fatalf("reaping must remove exactly our child: before=%d after=%d", beforeChildren, afterChildren)
	}
}

// captureLogs swaps the default slog logger for one writing to buf and
// restores it on cleanup (tests in this package run sequentially).
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestRunZombieWatchdogArmedLineAndBreachWarn(t *testing.T) {
	prevPeriod := zombieWatchdogPeriod
	zombieWatchdogPeriod = 5 * time.Millisecond
	t.Cleanup(func() { zombieWatchdogPeriod = prevPeriod })

	buf := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	calls := 0
	sample := func(pid int) (int, int, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return 2, 1, nil // breach on the first sample: one zombie
		}
		return 0, 0, errors.New("boom") // sample failure on the second
	}
	go runZombieWatchdog(ctx, sample)

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		c := calls
		mu.Unlock()
		if c >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watchdog did not sample repeatedly, calls=%d", c)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	logs := buf.String()
	if !strings.Contains(logs, "go-bridge zombie watchdog armed") {
		t.Fatalf("armed feature line missing:\n%s", logs)
	}
	if !strings.Contains(logs, "childrenThreshold=64") || !strings.Contains(logs, "period=5ms") {
		t.Fatalf("armed line must carry pid/period/childrenThreshold:\n%s", logs)
	}
	if !strings.Contains(logs, `msg="go-bridge zombie watchdog"`) || !strings.Contains(logs, "zombies=1") {
		t.Fatalf("breach WARN missing:\n%s", logs)
	}
	if !strings.Contains(logs, "zombie watchdog sample failed") {
		t.Fatalf("sample failure must WARN and keep looping:\n%s", logs)
	}
	// Exactly one armed line per runtime lifetime.
	if strings.Count(logs, "armed") != 1 {
		t.Fatalf("armed line must be one-shot:\n%s", logs)
	}
}

func TestRunZombieWatchdogHealthySilent(t *testing.T) {
	prevPeriod := zombieWatchdogPeriod
	zombieWatchdogPeriod = 5 * time.Millisecond
	t.Cleanup(func() { zombieWatchdogPeriod = prevPeriod })

	buf := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runZombieWatchdog(ctx, func(int) (int, int, error) { return 3, 0, nil })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), `msg="go-bridge zombie watchdog"`) {
			t.Fatalf("healthy samples must not WARN:\n%s", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if !strings.Contains(buf.String(), "armed") {
		t.Fatalf("armed line missing on healthy run:\n%s", buf.String())
	}
}
