package dshweb

// Canonical-seat lifecycle tests (design
// docs/2026-08-19-dsh-web-canonical-3080-instance-design.md §3/§8.1/§8.4/§8.6/§8.7):
// seat loss → grace (no adopt, no spawn, typed error), grace rebind after a
// user restart, grace-expiry respawn ON the seat, ownership labeling (M5 —
// endpoint probe ≠ label, never a dead PID), cold-start single-flight, and
// the lost-callback edge.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// seatServer starts a describeHandler server bound to an explicit port (the
// seat) — the "user's instance" stand-in.
func seatServer(t *testing.T, port int) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("bind seat: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(describeHandler))
	srv.Listener = ln
	srv.Start()
	return srv
}

// holdSeat cold-starts the resolver so its starter owns the seat, returning
// the resolver and starter. Probes run with keep-alives disabled: closing a
// listener does not kill already-accepted sockets, so a pooled idle conn would
// survive the fake "process death" and answer the next probe — a real dsh
// restart tears its sockets down with the process.
func holdSeat(t *testing.T, grace time.Duration) (*Resolver, *countingStarter, string, string) {
	t.Helper()
	seat := freeLoopbackSeat(t)
	starter := &countingStarter{}
	r := NewResolver(
		WithProbeURLs([]string{seat}),
		WithDataDir(t.TempDir()),
		withManagedStarter(starter),
		withGracePeriod(grace),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}),
	)
	inst, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatalf("cold-start Resolve: %v", err)
	}
	if inst.Source != SourceManaged || starter.starts != 1 {
		t.Fatalf("expected one managed cold-start spawn: %+v starts=%d", inst, starter.starts)
	}
	return r, starter, seat, seatPort(seat)
}

func seatPort(seat string) string {
	_, port, _ := net.SplitHostPort(seat[len("http://"):])
	return port
}

func TestSeatLossGraceNoAdoptNoSpawnThenRebind(t *testing.T) {
	// §8.1 regression row (unit shape): a held instance dies; during grace the
	// resolver must NOT spawn and must NOT adopt anything off the seat — the
	// caller gets the typed error. When the user restarts their instance on
	// the seat, the next Resolve rebinds it as external.
	r, starter, seat, port := holdSeat(t, 400*time.Millisecond)

	// A legacy orphan on a stray port (the Aug-18 3096 shape) must be ignored
	// even while alive — nothing in the seat model may reach for it.
	orphan := seatServer(t, 0)
	defer orphan.Close()

	// Instance dies (user restart begins).
	if err := starter.Stop(); err != nil {
		t.Fatal(err)
	}

	_, err := r.Resolve(context.Background())
	var re *ErrInstanceReconnecting
	if !errors.As(err, &re) || re.Starting {
		t.Fatalf("expected grace typed error, got %v", err)
	}
	if re.BaseURL != seat {
		t.Fatalf("typed error must name the seat %s, got %s", seat, re.BaseURL)
	}
	if starter.starts != 1 {
		t.Fatalf("grace must not spawn (starts=%d)", starter.starts)
	}
	if inGrace, until := r.GraceState(); !inGrace || until.IsZero() {
		t.Fatalf("GraceState must report the window: %v %v", inGrace, until)
	}
	if cur := r.Current(); cur != nil {
		t.Fatalf("Current must be nil while dark (got %+v)", cur)
	}

	// Repeat calls inside grace keep the typed error (negative cache path).
	for i := 0; i < 3; i++ {
		_, err := r.Resolve(context.Background())
		if !errors.As(err, &re) {
			t.Fatalf("resolve #%d in grace: %v", i+1, err)
		}
		if starter.starts != 1 {
			t.Fatalf("grace resolve spawned (starts=%d)", starter.starts)
		}
	}

	// User's restarted instance comes back on the seat → rebind as external.
	// The ≤1s probe negative cache may hold the first attempt; retry like the
	// production stream loop (2s backoff) until the window clears.
	back := seatServer(t, mustPort(t, port))
	defer back.Close()
	var inst *ResolvedInstance
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		inst, err = r.Resolve(context.Background())
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("rebind Resolve: %v", err)
	}
	if inst.Source != SourceExternal || inst.BaseURL != seat || inst.PID != 0 {
		t.Fatalf("rebind must adopt the user's instance as external: %+v", inst)
	}
	if starter.starts != 1 {
		t.Fatalf("rebind must not spawn (starts=%d)", starter.starts)
	}
	if inGrace, _ := r.GraceState(); inGrace {
		t.Fatal("grace must clear after rebind")
	}
}

func TestGraceExpiryRespawnsOnSeat(t *testing.T) {
	r, starter, seat, _ := holdSeat(t, 150*time.Millisecond)
	_ = seat
	if err := starter.Stop(); err != nil {
		t.Fatal(err)
	}
	// Loss detection is lazy (probes discover it): trigger the transition
	// first, then burn through grace AND the 1s probe negative cache — the
	// respawn attempt only starts after both windows clear.
	if _, err := r.Resolve(context.Background()); err == nil {
		t.Fatal("expected loss error right after instance death")
	}
	time.Sleep(1200 * time.Millisecond)

	inst, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatalf("post-grace Resolve: %v", err)
	}
	if inst.Source != SourceManaged || inst.BaseURL != seat {
		t.Fatalf("grace expiry must respawn ON the seat: %+v", inst)
	}
	if starter.starts != 2 {
		t.Fatalf("expected exactly one respawn, starts=%d", starter.starts)
	}
}

func TestSpawnLabelOwnershipNeverDeadPid(t *testing.T) {
	// M5: boot-wait probes the ENDPOINT; the source label follows ownership.
	// Direct spawnOnSeat calls isolate the labeling decision.
	t.Run("alive child on seat → managed", func(t *testing.T) {
		seat := freeLoopbackSeat(t)
		srv := seatServer(t, mustPort(t, seatPort(seat)))
		defer srv.Close()
		st := &noBindStarter{pid: os.Getpid()}
		r := NewResolver(WithProbeURLs([]string{seat}), withManagedStarter(st))
		inst, err := r.spawnOnSeat(context.Background(), seat)
		if err != nil {
			t.Fatal(err)
		}
		if inst.Source != SourceManaged || inst.PID != os.Getpid() {
			t.Fatalf("alive child must label managed with pid: %+v", inst)
		}
	})
	t.Run("dead child → external, no pid, no state", func(t *testing.T) {
		seat := freeLoopbackSeat(t)
		srv := seatServer(t, mustPort(t, seatPort(seat)))
		defer srv.Close()
		dataDir := t.TempDir()
		st := &noBindStarter{pid: 1 << 22} // almost surely unallocated → dead
		r := NewResolver(WithProbeURLs([]string{seat}), WithDataDir(dataDir), withManagedStarter(st))
		inst, err := r.spawnOnSeat(context.Background(), seat)
		if err != nil {
			t.Fatal(err)
		}
		if inst.Source != SourceExternal {
			t.Fatalf("dead child must not claim ownership: %+v", inst)
		}
		if inst.PID != 0 {
			t.Fatalf("never record a dead pid: %+v", inst)
		}
		if _, err := os.Stat(filepath.Join(dataDir, managedStateFile)); !os.IsNotExist(err) {
			t.Fatalf("external instances must not write managed state (err=%v)", err)
		}
	})
	t.Run("dead child on dark seat fails fast", func(t *testing.T) {
		seat := freeLoopbackSeat(t) // nothing binds it
		st := &noBindStarter{pid: 1 << 22}
		r := NewResolver(WithProbeURLs([]string{seat}), withManagedStarter(st))
		start := time.Now()
		_, err := r.spawnOnSeat(context.Background(), seat)
		if err == nil {
			t.Fatal("expected honest failure on dark seat with dead child")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("dead child must fail fast, took %s: %v", elapsed, err)
		}
	})
}

func TestColdStartSingleFlight(t *testing.T) {
	// §3.3/§8.6: one spawn in flight; concurrent resolvers get the typed
	// starting error immediately instead of blocking or double-spawning.
	seat := freeLoopbackSeat(t)
	st := &blockingStarter{release: make(chan struct{})}
	r := NewResolver(WithProbeURLs([]string{seat}), withManagedStarter(st), withGracePeriod(time.Second))

	const n = 8
	var wg sync.WaitGroup
	startingErrs := atomic.Int32{}
	oks := atomic.Int32{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst, err := r.Resolve(context.Background())
			switch {
			case err == nil:
				oks.Add(1)
			default:
				var re *ErrInstanceReconnecting
				if errors.As(err, &re) && re.Starting {
					startingErrs.Add(1)
				}
			}
			_ = inst
		}()
	}
	// Give the winner a moment to enter Start, then let it finish.
	time.Sleep(50 * time.Millisecond)
	close(st.release)
	wg.Wait()

	if st.starts != 1 {
		t.Fatalf("exactly one spawn expected, got %d", st.starts)
	}
	if oks.Add(0) != 1 {
		t.Fatalf("exactly one resolver may see the instance, got %d", oks.Load())
	}
	if got := startingErrs.Load(); got != n-1 {
		t.Fatalf("expected %d immediate typed errors, got %d", n-1, got)
	}
}

func TestLostCallbackFiresOncePerEdge(t *testing.T) {
	r, starter, _, _ := holdSeat(t, time.Second)

	fired := make(chan struct{}, 4)
	r.SetLostCallback(func() { fired <- struct{}{} })

	if err := starter.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background()); err == nil {
		t.Fatal("expected loss error")
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("lost callback must fire on the alive→dark edge")
	}
	// Further grace resolves must not re-fire the edge callback.
	for i := 0; i < 3; i++ {
		_, _ = r.Resolve(context.Background())
	}
	select {
	case <-fired:
		t.Fatal("callback fired more than once for one loss")
	case <-time.After(150 * time.Millisecond):
	}
}

// noBindStarter simulates a spawned child without binding anything — used to
// exercise the ownership-labeling branches of spawnOnSeat directly.
type noBindStarter struct{ pid int }

func (s *noBindStarter) Start(ctx context.Context, port int) (int, error) { return s.pid, nil }
func (s *noBindStarter) Stop() error                                     { return nil }

// blockingStarter parks inside Start until released, modelling a slow dsh
// boot for the single-flight test.
type blockingStarter struct {
	release chan struct{}
	starts  int32
}

func (s *blockingStarter) Start(ctx context.Context, port int) (int, error) {
	atomic.AddInt32(&s.starts, 1)
	<-s.release
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return 0, err
	}
	go func() { _ = http.Serve(ln, http.HandlerFunc(describeHandler)) }()
	return os.Getpid(), nil
}

func (s *blockingStarter) Stop() error { return nil }

func mustPort(t *testing.T, portStr string) int {
	t.Helper()
	var p int
	if _, err := fmt.Sscanf(portStr, "%d", &p); err != nil || p <= 0 {
		t.Fatalf("bad port %q", portStr)
	}
	return p
}
