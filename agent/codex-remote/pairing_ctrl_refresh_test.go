package codexremote

// pairing_ctrl_refresh_test.go covers S-4 (disconnect-resilience plan §3.4,
// OD-3): the proactive ctrl-token refresh scheduler — lead-window gating,
// bounded retry cadence, and the revoked branch.

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func newCtrlRefreshHarness(t *testing.T) (*PairingController, *int, *int) {
	t.Helper()
	p := newPairingController(&Agent{})
	calls, revoked := 0, 0
	p.ctrlRefreshExec = func(ctx context.Context) error {
		calls++
		if revoked > 0 {
			// When the revoked flag is armed the seam keeps failing revoked.
			return errPairingRevoked
		}
		return nil
	}
	t.Cleanup(func() { ctrlTokenRefreshMinRetry = 30 * time.Second })
	return p, &calls, &revoked
}

func setCtrlExp(p *PairingController, exp time.Time) {
	p.mu.Lock()
	p.state.ctrlExp = strconv.FormatInt(exp.Unix(), 10)
	p.mu.Unlock()
}

// OD-3 pin: the lead is 60s against the E-9-observed constant 600s lifetime.
func TestCtrlTokenRefreshLeadPinnedTo60s(t *testing.T) {
	if ctrlTokenRefreshLead != 60*time.Second {
		t.Fatalf("ctrlTokenRefreshLead = %v, want 60s (OD-3, E-9 attempt-011)", ctrlTokenRefreshLead)
	}
}

// Outside the lead window the scheduler is a no-op; inside it the refresh
// executes; the retry cadence bounds repeat attempts.
func TestCtrlTokenRefreshSchedulingWindowAndRetryGap(t *testing.T) {
	ctrlTokenRefreshMinRetry = 5 * time.Millisecond
	p, calls, _ := newCtrlRefreshHarness(t)

	// Not due: token still far from expiry.
	setCtrlExp(p, time.Now().Add(10*time.Minute))
	p.maybeRefreshCtrlToken()
	if *calls != 0 {
		t.Fatalf("refresh ran outside the lead window: %d", *calls)
	}

	// Due: inside the final 60s.
	setCtrlExp(p, time.Now().Add(30*time.Second))
	p.maybeRefreshCtrlToken()
	if *calls != 1 {
		t.Fatalf("refresh did not run inside the lead window: %d", *calls)
	}

	// Retry gap: an immediate second attempt is suppressed.
	p.maybeRefreshCtrlToken()
	if *calls != 1 {
		t.Fatalf("retry gap not enforced: %d", *calls)
	}

	time.Sleep(8 * time.Millisecond)
	p.maybeRefreshCtrlToken()
	if *calls != 2 {
		t.Fatalf("refresh after retry gap did not run: %d", *calls)
	}

	// Unparseable expiry is a silent no-op (never a death path).
	p.mu.Lock()
	p.state.ctrlExp = ""
	p.mu.Unlock()
	p.maybeRefreshCtrlToken()
	if *calls != 2 {
		t.Fatalf("unparseable expiry triggered refresh: %d", *calls)
	}
}

// Plan §3.4 revoked branch: errPairingRevoked → immediate invalidation, same
// destination as restoreOnce/watchBinding.
func TestCtrlTokenRefreshRevokedInvalidates(t *testing.T) {
	ctrlTokenRefreshMinRetry = time.Millisecond
	p, calls, revoked := newCtrlRefreshHarness(t)
	*revoked = 1
	setCtrlExp(p, time.Now().Add(30*time.Second))
	p.maybeRefreshCtrlToken()
	if *calls != 1 {
		t.Fatalf("revoked refresh did not execute: %d", *calls)
	}
	p.mu.Lock()
	phase := p.state.phase
	p.mu.Unlock()
	if phase != PairPhaseFailed {
		t.Fatalf("phase = %q, want failed (immediate invalidation)", phase)
	}
}

// A non-revoked failure keeps the connection on the existing path: no state
// transition, retry allowed after the gap.
func TestCtrlTokenRefreshTransientFailureKeepsGoing(t *testing.T) {
	ctrlTokenRefreshMinRetry = 5 * time.Millisecond
	p, calls, _ := newCtrlRefreshHarness(t)
	boom := errors.New("network reset")
	p.ctrlRefreshExec = func(ctx context.Context) error {
		*calls++
		return boom
	}
	setCtrlExp(p, time.Now().Add(30*time.Second))
	p.maybeRefreshCtrlToken()
	if *calls != 1 {
		t.Fatalf("refresh did not run: %d", *calls)
	}
	p.mu.Lock()
	phase := p.state.phase
	p.mu.Unlock()
	if phase == PairPhaseFailed {
		t.Fatal("transient failure must not invalidate the pairing")
	}
	time.Sleep(8 * time.Millisecond)
	p.maybeRefreshCtrlToken()
	if *calls != 2 {
		t.Fatalf("transient failure did not retry after the gap: %d", *calls)
	}
}
