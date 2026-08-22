package codexweb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func readyPeer() *fakePeer {
	peer := newFakePeer()
	peer.install(happyHandlers())
	return peer
}

func TestServiceEndpointOpenClientDialsSameResolvedService(t *testing.T) {
	var dials atomic.Int32
	deps := LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		SocketExists:       func(string) bool { return true },
		DialUDS: func(context.Context, string) (Transport, error) {
			dials.Add(1)
			return readyPeer(), nil
		},
	}
	ep, err := ProbeWith(deps, ProbeOptions{CodexHome: "/tmp/cw-home"})
	if err != nil {
		t.Fatalf("ProbeWith: %v", err)
	}
	t.Cleanup(func() { _ = ep.Close() })
	observer, err := ep.OpenClient(context.Background(), ProbeOptions{CodexHome: "/tmp/cw-home"})
	if err != nil {
		t.Fatalf("OpenClient: %v", err)
	}
	_ = observer.Close()
	if got := dials.Load(); got != 2 {
		t.Fatalf("same service should have two connections and one resolution, dials=%d", got)
	}
}

func TestAgentEndpointResolutionSingleFlightManagedFallback(t *testing.T) {
	var starts atomic.Int32
	var dials atomic.Int32
	deps := LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		RunDaemonStart:     func(string, string) (string, error) { return "", errors.New("no daemon") },
		SocketExists:       func(string) bool { return false },
		StartManagedWS: func(string, string, string) (string, *managedProcess, error) {
			starts.Add(1)
			return "ws://127.0.0.1:45678", &managedProcess{port: 45678}, nil
		},
		HTTPHealth: func(string) error { return nil },
		DialTCP: func(context.Context, string) (Transport, error) {
			dials.Add(1)
			return readyPeer(), nil
		},
	}
	a := New(map[string]any{"work_dir": "/tmp", "codex_web_codex_home": "/tmp/cw-home"})
	a.lifecycleDeps = &deps
	const callers = 16
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := a.endpointFor(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("endpointFor: %v", err)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("managed service spawned %d times, want exactly 1", got)
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("readiness connection dialed %d times, want exactly 1", got)
	}
	_ = a.Stop()
}

func TestSubscribeUsesObserverConnectionOnSameManagedService(t *testing.T) {
	var starts atomic.Int32
	var dials atomic.Int32
	var peersMu sync.Mutex
	var peers []*fakePeer
	deps := LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		RunDaemonStart:     func(string, string) (string, error) { return "", errors.New("no daemon") },
		SocketExists:       func(string) bool { return false },
		StartManagedWS: func(string, string, string) (string, *managedProcess, error) {
			starts.Add(1)
			return "ws://127.0.0.1:45680", &managedProcess{port: 45680}, nil
		},
		HTTPHealth: func(string) error { return nil },
		DialTCP: func(context.Context, string) (Transport, error) {
			dials.Add(1)
			peer := readyPeer()
			peersMu.Lock()
			peers = append(peers, peer)
			peersMu.Unlock()
			return peer, nil
		},
	}
	a := New(map[string]any{"work_dir": "/tmp", "codex_web_codex_home": "/tmp/cw-home"})
	a.lifecycleDeps = &deps
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.Subscribe(ctx); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("Subscribe spawned %d managed services, want exactly 1", got)
	}
	if got := dials.Load(); got != 2 {
		t.Fatalf("main plus observer should dial same service twice, got %d", got)
	}
	peersMu.Lock()
	for _, peer := range peers {
		_ = peer.Close()
	}
	peersMu.Unlock()
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestManagedRecordModeAndStrictMismatchDoesNotKill(t *testing.T) {
	dir := t.TempDir()
	opts := ProbeOptions{DataDir: dir, CodexHome: "/tmp/cw-home"}
	ep := &ServiceEndpoint{managed: &managedProcess{
		pid: 42, port: 45678, startTime: "Sat Aug 22 14:00:00 2026",
		binary: "/fake/codex", url: "ws://127.0.0.1:45678",
	}}
	if err := persistManagedRecord(opts, ep); err != nil {
		t.Fatalf("persistManagedRecord: %v", err)
	}
	path := filepath.Join(dir, managedStateFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("record mode=%o, want 600", got)
	}
	var killed atomic.Int32
	deps := LifecycleDeps{
		InspectProcess: func(int) (string, string, bool) {
			return "/fake/codex app-server --listen ws://127.0.0.1:45678", "DIFFERENT START", true
		},
		ProcessOwnsPort: func(int, int) bool { return true },
		TerminateProcess: func(int) error {
			killed.Add(1)
			return nil
		},
	}
	cleanupRecordedManaged(opts, "/fake/codex", deps)
	if killed.Load() != 0 {
		t.Fatal("start-time mismatch must never terminate a possibly reused PID")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale record should be removed, stat err=%v", err)
	}
}

func TestRecoverRecordedManagedAdoptsVerifiedService(t *testing.T) {
	dir := t.TempDir()
	opts := ProbeOptions{DataDir: dir, CodexHome: "/tmp/cw-home"}
	seed := &ServiceEndpoint{managed: &managedProcess{
		pid: 77, port: 45679, startTime: "Sat Aug 22 14:01:00 2026",
		binary: "/fake/codex", url: "ws://127.0.0.1:45679",
	}}
	if err := persistManagedRecord(opts, seed); err != nil {
		t.Fatal(err)
	}
	var terminated atomic.Int32
	deps := LifecycleDeps{
		DialTCP:    func(context.Context, string) (Transport, error) { return readyPeer(), nil },
		HTTPHealth: func(string) error { return nil },
		InspectProcess: func(int) (string, string, bool) {
			return "/fake/codex app-server --listen ws://127.0.0.1:45679", "Sat Aug 22 14:01:00 2026", true
		},
		ProcessOwnsPort: func(pid, port int) bool { return pid == 77 && port == 45679 },
		TerminateProcess: func(int) error {
			terminated.Add(1)
			return nil
		},
	}
	fillDeps(&deps)
	ep, ok, err := recoverRecordedManaged(deps, opts, "/fake/codex")
	if err != nil || !ok {
		t.Fatalf("recover=%v ok=%v", err, ok)
	}
	if ep.Source != SourceManagedLoopbackWS || ep.managed == nil || ep.managed.cmd != nil {
		t.Fatalf("adopted endpoint=%+v managed=%+v", ep, ep.managed)
	}
	if err := ep.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if terminated.Load() != 1 {
		t.Fatalf("adopted managed service termination count=%d, want 1", terminated.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, managedStateFile)); !os.IsNotExist(err) {
		t.Fatalf("owned record remains after Close: %v", err)
	}
}
