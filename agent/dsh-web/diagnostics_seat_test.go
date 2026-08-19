package dshweb

// s5: diagnostics speak the canonical-seat model — the grace window is
// reported as a window (not a bare failure), and healthy lines name the seat
// semantics (external = adopted via port identity; managed = ours on the
// seat, survives Link restarts).

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDiagInstanceReportsGraceWindow(t *testing.T) {
	r := graceFixture(t)
	a := &Agent{resolver: r}
	res := a.diagInstance(context.Background())
	if !strings.Contains(res.Message, "宽限") {
		t.Fatalf("grace must be reported as a window, got: %s", res.Message)
	}
	if !strings.Contains(res.Message, r.seatURL()) {
		t.Fatalf("message must name the seat, got: %s", res.Message)
	}
	if !strings.Contains(res.Message, "backend_unavailable") {
		t.Fatalf("message must state the wire behavior, got: %s", res.Message)
	}
}

func TestDiagInstanceHealthyLines(t *testing.T) {
	r, starter, seat, port := holdSeat(t, time.Second)
	a := &Agent{resolver: r}

	res := a.diagInstance(context.Background())
	if !strings.Contains(res.Message, "托管实例") || !strings.Contains(res.Message, seat) {
		t.Fatalf("managed line mismatch: %s", res.Message)
	}

	// Instance rotates (user restarts on the seat): external adoption line.
	if err := starter.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background()); err == nil {
		t.Fatal("expected loss")
	}
	back := seatServer(t, mustPort(t, port))
	defer back.Close()
	deadline := time.Now().Add(3 * time.Second)
	var res2 = res
	for time.Now().Before(deadline) {
		res2 = a.diagInstance(context.Background())
		if strings.Contains(res2.Message, "复用权威端口") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("external adoption line never appeared: %s", res2.Message)
}
