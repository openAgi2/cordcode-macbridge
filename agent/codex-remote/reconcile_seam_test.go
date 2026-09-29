package codexremote

// reconcile_seam_test.go covers the S-2 TurnReconciler seam (disconnect-
// resilience plan §3.2 wiring element 3): the pending set is the truth, the
// wake channel is a data-free one-slot coalescer, and ClearReconciledTurn is
// the only path that clears the codec's in-flight turn entry (so a closed
// turn never re-enters the pending set on a later rebind).

import (
	"sort"
	"testing"
)

func TestTurnReconcilerSeamLifecycle(t *testing.T) {
	a := &Agent{}
	a.codec = NewLiveCodec()
	a.codec.setActiveTurn("th1", "turn-1")

	// The codec's in-flight turn is the reconcile admission truth.
	if got := a.ActiveTurnForReconcile("th1"); got != "turn-1" {
		t.Fatalf("ActiveTurnForReconcile = %q, want turn-1", got)
	}

	// addPendingTurnReconcile: pending set + one-slot wake signal.
	a.addPendingTurnReconcile("th1")
	a.addPendingTurnReconcile("th2")
	pending := a.PendingTurnReconciles()
	sort.Strings(pending)
	if len(pending) != 2 || pending[0] != "th1" || pending[1] != "th2" {
		t.Fatalf("pending = %v, want [th1 th2]", pending)
	}
	signals := a.TurnReconcileSignals()
	select {
	case <-signals:
	default:
		t.Fatal("wake signal not fired by addPendingTurnReconcile")
	}
	// One-slot coalescing: a burst of adds leaves exactly one token.
	a.addPendingTurnReconcile("th3")
	select {
	case <-signals:
	default:
	}
	select {
	case <-signals:
		t.Fatal("wake channel must coalesce bursts into one token")
	default:
	}

	// ClearPendingTurnReconcile retires the attempt without touching the codec.
	a.ClearPendingTurnReconcile("th1")
	if pending := a.PendingTurnReconciles(); len(pending) != 2 {
		t.Fatalf("pending after ClearPendingTurnReconcile = %v, want th2+th3", pending)
	}
	if got := a.ActiveTurnForReconcile("th1"); got != "turn-1" {
		t.Fatalf("codec entry cleared by ClearPendingTurnReconcile: %q", got)
	}

	// ClearReconciledTurn drops the pending entry AND the codec entry.
	a.ClearReconciledTurn("th1")
	if pending := a.PendingTurnReconciles(); len(pending) != 2 {
		t.Fatalf("pending after ClearReconciledTurn = %v, want th2+th3", pending)
	}
	if got := a.ActiveTurnForReconcile("th1"); got != "" {
		t.Fatalf("codec entry survived ClearReconciledTurn: %q", got)
	}
}

// TestTurnReconcilerSeamNilCodecSafe: the zero-value Agent (no transport, no
// codec yet) must answer the seam without panicking — the bridge's catalog
// loop type-asserts TurnReconciler before the first successful attach.
func TestTurnReconcilerSeamNilCodecSafe(t *testing.T) {
	a := &Agent{}
	if got := a.ActiveTurnForReconcile("th1"); got != "" {
		t.Fatalf("ActiveTurnForReconcile on nil codec = %q, want empty", got)
	}
	a.ClearReconciledTurn("th1")
	a.ClearPendingTurnReconcile("th1")
	if pending := a.PendingTurnReconciles(); len(pending) != 0 {
		t.Fatalf("pending = %v, want empty", pending)
	}
}
