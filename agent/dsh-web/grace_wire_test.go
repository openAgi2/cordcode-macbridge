package dshweb

// §8.5 wire-contract rows (canonical-3080 design §3.2/§12.1): an already-bound
// session's Send during grace returns the typed error (handlers map it to
// backend_unavailable, never send_failed), and InstanceStatus stays available
// with the reconnecting detail so the hello detector cannot fold it into
// not_configured.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// graceFixture holds a seat then kills it, leaving the resolver inside its
// grace window.
func graceFixture(t *testing.T) *Resolver {
	t.Helper()
	r, starter, _, _ := holdSeat(t, time.Second)
	if err := starter.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background()); err == nil {
		t.Fatal("expected loss error to enter grace")
	}
	if inGrace, _ := r.GraceState(); !inGrace {
		t.Fatal("fixture not in grace")
	}
	return r
}

func TestSendDuringGraceReturnsTypedError(t *testing.T) {
	// §8.5: already-open session send → typed error → handler maps to
	// backend_unavailable. The bound client bypasses Resolve, so Send itself
	// must surface the grace window (§12.1-2).
	r := graceFixture(t)
	a := &Agent{resolver: r}
	s := &dshSession{agent: a}

	err := s.Send("hello", nil, nil)
	var re *ErrInstanceReconnecting
	if !errors.As(err, &re) {
		t.Fatalf("Send during grace must return the typed error, got %v", err)
	}
	if re.Starting {
		t.Fatalf("loss grace must not read as a cold boot: %+v", re)
	}
	if !strings.Contains(err.Error(), "reconnecting") {
		t.Fatalf("error text must say reconnecting: %v", err)
	}
}

func TestInstanceStatusDuringGraceStaysAvailable(t *testing.T) {
	// §12.1-4: Current() is nil while dark; InstanceStatus must still report
	// available=true + reconnecting detail, or detectInstanceStatusProber
	// would emit not_configured — the code the grace contract forbids.
	r := graceFixture(t)
	a := &Agent{resolver: r}

	if cur := r.Current(); cur != nil {
		t.Fatalf("Current must be nil during grace, got %+v", cur)
	}
	available, detail := a.InstanceStatus()
	if !available {
		t.Fatalf("InstanceStatus must stay available during grace: %q", detail)
	}
	if !strings.Contains(detail, "reconnecting") {
		t.Fatalf("detail must mention reconnecting: %q", detail)
	}

	// Outside grace (healthy seat) the detail reverts to the instance line.
	r2, _, _, _ := holdSeat(t, time.Second)
	a2 := &Agent{resolver: r2}
	available2, detail2 := a2.InstanceStatus()
	if !available2 || !strings.Contains(detail2, "instance at") {
		t.Fatalf("healthy status mismatch: %v %q", available2, detail2)
	}
}
