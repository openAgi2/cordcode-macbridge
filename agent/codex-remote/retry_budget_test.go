package codexremote

// P2 retry-budget tests (docs/2026-10-13-codex-remote-absence-hardening-p2-
// plan.md §3 D2 / S-2): notInstalled waits are fixed 120s WITHOUT advancing
// the backoff sequence; transient classes keep the official-mirrored
// backoff; a class flip resumes the sequence where it was. Both restore
// loops (restorePersistedPairing and watchBinding) route through the shared
// restoreRetryWait helper.

import (
	"testing"
	"time"
)

func TestRestoreRetryWaitNotInstalledFixedNoBackoffAdvance(t *testing.T) {
	a := &Agent{}
	b := newReconnectBackoff()
	for i := 0; i < 4; i++ {
		if got := a.restoreRetryWait(errClassNotInstalled, b); got != restorePermanentRetryInterval {
			t.Fatalf("notInstalled wait #%d = %v, want %v", i, got, restorePermanentRetryInterval)
		}
	}
	if b.attempt != 0 {
		t.Fatalf("notInstalled rounds must not advance backoff, attempt=%d", b.attempt)
	}
}

func TestRestoreRetryWaitTransientAdvancesBackoff(t *testing.T) {
	a := &Agent{}
	b := newReconnectBackoff()
	_ = a.restoreRetryWait(errClassTransient, b)
	_ = a.restoreRetryWait(errClassDesktopOffline, b)
	_ = a.restoreRetryWait(errClassNotLoggedIn, b)
	if b.attempt != 3 {
		t.Fatalf("transient rounds must advance backoff, attempt=%d", b.attempt)
	}
}

func TestRestoreRetryWaitClassFlipResumesSequence(t *testing.T) {
	// Install-happens-mid-storm shape: permanent rounds leave the sequence
	// untouched, so the first transient round after the flip takes the
	// FIRST backoff step, not a skipped-ahead one.
	a := &Agent{}
	b := newReconnectBackoff()
	_ = a.restoreRetryWait(errClassTransient, b) // attempt 0 → 1
	_ = a.restoreRetryWait(errClassNotInstalled, b)
	_ = a.restoreRetryWait(errClassNotInstalled, b)
	if b.attempt != 1 {
		t.Fatalf("permanent rounds must not advance, attempt=%d", b.attempt)
	}
	_ = a.restoreRetryWait(errClassTransient, b) // attempt 1 → 2
	if b.attempt != 2 {
		t.Fatalf("post-flip transient must resume sequence, attempt=%d", b.attempt)
	}
}

func TestRestorePermanentRetryIntervalShrinkable(t *testing.T) {
	// The interval is a var so tests can shrink the 120s window — assert it
	// is the single source of the fixed wait.
	prev := restorePermanentRetryInterval
	restorePermanentRetryInterval = 50 * time.Millisecond
	t.Cleanup(func() { restorePermanentRetryInterval = prev })
	a := &Agent{}
	if got := a.restoreRetryWait(errClassNotInstalled, newReconnectBackoff()); got != 50*time.Millisecond {
		t.Fatalf("shrunken interval not honored: %v", got)
	}
}
