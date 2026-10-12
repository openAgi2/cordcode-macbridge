package dshweb

// execManagedStarter zombie-reaping regression (incidents/2026-10-12-bridge-
// zombie-process-exhaustion.md §5.1-3, r1 F-2 corrected failure mode): a
// managed seat dying spontaneously used to leave one zombie holding s.cmd,
// so the Start guard kept rejecting the grace-expiry respawn (auto-recovery
// stuck). Start now runs a single reaper goroutine owning the child's Wait;
// Stop is signal-only and relies on that reaper. Assertions use the Wait
// contract, not "respawn N times piles N zombies" (contradicts the guard,
// see §3.5) and not ps system-state polling (r1 F-4): the slot clearing
// happens after cmd.Wait() returns, and ESRCH on the pid proves a full reap
// (a zombie still answers signal 0).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func writeSeatStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dsh-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// waitSeatPidGone polls until the pid is fully gone from the process table
// (exit + reap). Zombies keep answering signal 0, so this fails on the
// pre-fix behavior.
func waitSeatPidGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("seat pid %d still in the process table — zombie left behind", pid)
}

func TestExecManagedStarterReapsSpontaneousDeathAndReAdmits(t *testing.T) {
	// The seat dies on its own (crash / exit), nobody calls Stop.
	stub := writeSeatStub(t, "sleep 0.2\n")
	s := &execManagedStarter{binPath: stub}
	pid, err := s.Start(context.Background(), 3998)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The reaper goroutine must reap the dead child and free the slot so the
	// Start guard re-admits (grace-expiry respawn unblocked). Slot clearing
	// happens only after cmd.Wait() returned → also proves the Wait contract.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		freed := s.cmd == nil
		s.mu.Unlock()
		if freed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	freed := s.cmd == nil
	s.mu.Unlock()
	if !freed {
		t.Fatal("s.cmd never freed after spontaneous seat death — reaper did not run (respawn stays blocked)")
	}
	waitSeatPidGone(t, pid, 5*time.Second)

	// Guard re-admission: a second Start must succeed on the same starter.
	pid2, err := s.Start(context.Background(), 3999)
	if err != nil {
		t.Fatalf("Start after spontaneous death must re-admit: %v", err)
	}
	if pid2 == pid {
		t.Fatalf("second Start must be a new child, got reused pid %d", pid2)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("cleanup Stop: %v", err)
	}
	waitSeatPidGone(t, pid2, 5*time.Second)
}

func TestExecManagedStarterStopIsSignalOnlyAndReaperReaps(t *testing.T) {
	// Stop is signal-only now; the single Wait belongs to the reaper. The
	// killed child must still be reaped (no zombie) even though Stop returned
	// without waiting.
	stub := writeSeatStub(t, "sleep 30\n")
	s := &execManagedStarter{binPath: stub}
	pid, err := s.Start(context.Background(), 3998)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitSeatPidGone(t, pid, 5*time.Second)

	// And the slot is free again for the next spawn.
	if _, err := s.Start(context.Background(), 3999); err != nil {
		t.Fatalf("Start after Stop must re-admit: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("cleanup Stop: %v", err)
	}
}

// TestExecManagedStarterStartGuardStillRejectsDoubleSpawn pins the guard
// semantics the reaper must preserve: while a child is alive, a second Start
// on the same starter fails instead of double-spawning.
func TestExecManagedStarterStartGuardStillRejectsDoubleSpawn(t *testing.T) {
	stub := writeSeatStub(t, "sleep 30\n")
	s := &execManagedStarter{binPath: stub}
	if _, err := s.Start(context.Background(), 3998); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := s.Start(context.Background(), 3999); err == nil {
		t.Fatal("second Start on a live child must be rejected by the guard")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("cleanup Stop: %v", err)
	}
}
