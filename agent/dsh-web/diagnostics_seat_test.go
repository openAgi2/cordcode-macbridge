package dshweb

// s5: diagnostics speak the canonical-seat model — the grace window is
// reported as a window (not a bare failure), and healthy lines name the seat
// semantics (external = adopted via port identity; managed = ours on the
// seat, survives Link restarts).

import (
	"context"
	"net/http"
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

	// Instance rotates (user restarts on the seat). Diagnostics are read-only
	// (2026-09-22 plan §5): the answering-but-unheld seat reports the 在听
	// line without adopting or spawning; the next real RPC (Resolve) adopts.
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
		if strings.Contains(res2.Message, "在听") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(res2.Message, "在听") {
		t.Fatalf("read-only answering-seat line never appeared: %s", res2.Message)
	}
	if starter.starts != 1 {
		t.Fatalf("read-only diagnostics must not spawn (starts=%d)", starter.starts)
	}

	// A real RPC adopts the rotated instance; the diag line then names the
	// external adoption.
	if _, err := r.Resolve(context.Background()); err != nil {
		t.Fatalf("adoption Resolve: %v", err)
	}
	res3 := a.diagInstance(context.Background())
	if !strings.Contains(res3.Message, "复用权威端口") {
		t.Fatalf("external adoption line mismatch: %s", res3.Message)
	}
}

// TestDiagnosticsReadOnlyNeverSpawns (2026-09-22 plan §7): RunDiagnostics on
// a dark seat with a never-held resolver must not call the starter —
// diagnostics is not a third spawn path.
func TestDiagnosticsReadOnlyNeverSpawns(t *testing.T) {
	seat := freeLoopbackSeat(t) // nothing binds it
	starter := &countingStarter{}
	r := NewResolver(
		WithProbeURLs([]string{seat}),
		withManagedStarter(starter),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}),
	)
	a := &Agent{resolver: r}

	report, err := a.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if starter.starts != 0 {
		t.Fatalf("RunDiagnostics must not spawn on a dark cold seat (starts=%d)", starter.starts)
	}
	if report.OverallStatus != "unhealthy" {
		t.Fatalf("dark cold seat must report unhealthy, got %q", report.OverallStatus)
	}

	// The descriptor seam is read-only too.
	if _, err := a.resolver.Resolve(context.Background()); err == nil {
		t.Fatal("expected not-running error")
	}
	if starter.starts != 0 {
		t.Fatalf("Resolve must not spawn on a cold dark seat (starts=%d)", starter.starts)
	}
}
