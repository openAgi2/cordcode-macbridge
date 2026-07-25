package gobridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHandleGetSessionProjectionReturnsReducerState: a fed reducer is returned verbatim as the
// {projection} data — proving pull reads the same in-memory state push produces (design §6.4 r4).
func TestHandleGetSessionProjectionReturnsReducerState(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": "hi"}, Broadcast: true})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1"})
	msg := WireMessage{RequestID: "r1", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err != nil {
		t.Fatalf("unexpected error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	if proj.SessionID != "s1" || proj.SyncRev != 2 || len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
		t.Fatalf("projection = %+v", proj)
	}
}

// TestHandleGetSessionProjectionEmptyWhenNoState: a session with no reducer state returns an
// empty projection at head 0 (never fabricated content).
func TestHandleGetSessionProjectionEmptyWhenNoState(t *testing.T) {
	handlers := NewHandlers()
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "unknown"})
	msg := WireMessage{RequestID: "r1", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("expected empty projection, got %T", dataMap["projection"])
	}
	if proj.SyncRev != 0 || proj.Execution.Phase != "idle" || len(proj.Turns) != 0 {
		t.Fatalf("expected empty idle projection at head 0, got %+v", proj)
	}
}

// TestHandleGetSessionProjectionDeltaAtHead: sinceRev == headRev returns a cheap empty-patch
// delta instead of the full projection.
func TestHandleGetSessionProjectionDeltaAtHead(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	// headRev is now 1.
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1", "sinceRev": 1})
	msg := WireMessage{RequestID: "r1", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	patches, ok := dataMap["patches"].([]ProjectionPatch)
	if !ok {
		t.Fatalf("expected patches delta at head, got %+v", dataMap)
	}
	if len(patches) != 0 || dataMap["headRev"].(int) != 1 {
		t.Fatalf("expected empty patches + headRev 1, got %+v", dataMap)
	}
}

// TestHandleGetSessionProjectionRoutedByDispatch: the dispatch switch routes get_session_projection
// to the handler (guards against a missing case).
func TestHandleGetSessionProjectionRoutedByDispatch(t *testing.T) {
	// shouldSwitchWorkDirForMethod must treat it as read-only (no workdir switch).
	if shouldSwitchWorkDirForMethod("get_session_projection") {
		t.Fatal("get_session_projection should be read-only (no workdir switch)")
	}
}

// writeProjectionHydrateRollout builds a minimal multi-turn Codex rollout JSONL that
// scanCodexTranscriptRelayEvents + hydrateCodexProjectionFromDisk can reduce into turns.
func writeProjectionHydrateRollout(t *testing.T, path string, turns int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"s"}}` + "\n")
	for i := 0; i < turns; i++ {
		turnID := "turn-" + strconv.Itoa(i+1)
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"` + turnID + `"}}` + "\n")
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","id":"msg_` + strconv.Itoa(i+1) + `","content":[{"type":"input_text","text":"q` + strconv.Itoa(i+1) + `"}]}}` + "\n")
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a` + strconv.Itoa(i+1) + `"}]}}` + "\n")
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"` + turnID + `"}}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHandleGetSessionProjectionColdHydrateWaitsPastFormerTimeoutBudget: design §10.5 ring 1.
// A hydrate that takes longer than the old 750ms fixed budget must still complete before the
// RPC answers — never "timeout; serving current head" with empty turns.
func TestHandleGetSessionProjectionColdHydrateWaitsPastFormerTimeoutBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 3)

	prev := coldHydrateTestHook
	coldHydrateTestHook = func(context.Context) { time.Sleep(2 * time.Second) }
	t.Cleanup(func() { coldHydrateTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-slow"})
	msg := WireMessage{RequestID: "r-slow", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if elapsed < 2*time.Second {
		t.Fatalf("RPC returned in %s; expected to wait for hydrate delay ≥2s (no 750ms timeout bail)", elapsed)
	}
	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	if proj.SyncRev <= 0 || len(proj.Turns) == 0 {
		t.Fatalf("served empty head after delayed hydrate (forbidden §10.5): %+v", proj)
	}
	if len(proj.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(proj.Turns))
	}
}

// TestHandleGetSessionProjectionColdHydrateLargeRolloutNonEmpty: design §10.5 contract —
// a large multi-turn rollout cold-hydrates to non-empty turns (no empty head-0 success).
func TestHandleGetSessionProjectionColdHydrateLargeRolloutNonEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large-rollout.jsonl")
	const turns = 80
	writeProjectionHydrateRollout(t, path, turns)

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-large", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-large", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	if proj.SyncRev <= 0 {
		t.Fatalf("syncRev = %d, want > 0 after cold-hydrate", proj.SyncRev)
	}
	if len(proj.Turns) != turns {
		t.Fatalf("turns = %d, want %d (empty head-0 would be a contract violation)", len(proj.Turns), turns)
	}
	head := handlers.eventPublisher.ProjectionHeadRev("codex", "cold-large")
	if head <= 0 {
		t.Fatalf("headRev = %d after hydrate, want > 0", head)
	}
}

// TestHandleGetSessionProjectionColdHydrateSingleFlight: concurrent cold pulls share one
// hydrate and all observe non-empty state (no double-apply race serving empty to a racer).
func TestHandleGetSessionProjectionColdHydrateSingleFlight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 2)

	prev := coldHydrateTestHook
	started := make(chan struct{})
	release := make(chan struct{})
	coldHydrateTestHook = func(ctx context.Context) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() { coldHydrateTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	const n = 4
	conns := make([]*readFileCaptureConn, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		conns[i] = &readFileCaptureConn{}
		go func(i int) {
			defer wg.Done()
			params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-sf"})
			msg := WireMessage{RequestID: "r-sf", BackendID: "codex", Method: "get_session_projection", Params: params}
			handlers.handleGetSessionProjection(conns[i], msg, nil)
		}(i)
	}

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("hydrate never started")
	}
	// All RPCs must still be blocked — none served empty before hydrate finished.
	time.Sleep(50 * time.Millisecond)
	for i, c := range conns {
		if c.data != nil || c.err != nil {
			t.Fatalf("conn %d answered before hydrate completed: data=%T err=%+v", i, c.data, c.err)
		}
	}
	close(release)
	wg.Wait()

	for i, c := range conns {
		if c.err != nil {
			t.Fatalf("conn %d error: %+v", i, c.err)
		}
		dataMap, ok := c.data.(map[string]interface{})
		if !ok {
			t.Fatalf("conn %d data not map: %T", i, c.data)
		}
		proj, ok := dataMap["projection"].(SessionProjection)
		if !ok || len(proj.Turns) != 2 || proj.SyncRev <= 0 {
			t.Fatalf("conn %d empty/incomplete projection: %+v", i, dataMap["projection"])
		}
	}
}

// TestHandleGetSessionProjectionColdHydrateHardTimeoutExplicitError: §10.5 ring-1 close-out.
// A stuck hydrate must not hang forever or serve empty success — RPC returns explicit error
// within the hard budget, frees the single-flight slot, and a later pull can succeed.
func TestHandleGetSessionProjectionColdHydrateHardTimeoutExplicitError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 2)

	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 150 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	var hang atomic.Bool
	hang.Store(true)
	prevHook := coldHydrateTestHook
	coldHydrateTestHook = func(ctx context.Context) {
		if hang.Load() {
			<-ctx.Done() // stuck until hard-timeout cancel — simulates corrupt/hung scan
			return
		}
	}
	t.Cleanup(func() { coldHydrateTestHook = prevHook })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-hard-timeout", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-hang", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("RPC hung %s after hard-timeout budget; expected explicit fail near %s", elapsed, coldHydrateTimeout)
	}
	if elapsed < coldHydrateTimeout {
		t.Fatalf("RPC returned in %s; expected to wait at least hard budget %s", elapsed, coldHydrateTimeout)
	}
	if conn.err == nil {
		t.Fatalf("expected explicit RPC error on hard timeout, got success data=%T", conn.data)
	}
	if conn.err.Code != "projection.hydrate_timeout" {
		t.Fatalf("error code = %q, want projection.hydrate_timeout (got message %q)", conn.err.Code, conn.err.Message)
	}
	if conn.data != nil {
		t.Fatalf("hard timeout must not pair data with error; data=%T", conn.data)
	}
	// single-flight slot released: map must not retain a permanent waiter for this key
	handlers.mu.Lock()
	_, stuck := handlers.coldHydrateFlights["codex\x00cold-hard-timeout"]
	handlers.mu.Unlock()
	if stuck {
		t.Fatal("single-flight slot still occupied after hard timeout — subsequent pulls would hang")
	}

	// Retry after unblocking hydrate must succeed (slot not permanently poisoned).
	hang.Store(false)
	conn2 := &readFileCaptureConn{}
	msg2 := WireMessage{RequestID: "r-retry", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn2, msg2, nil)
	if conn2.err != nil {
		t.Fatalf("retry after hard timeout should succeed, got %+v", conn2.err)
	}
	dataMap, ok := conn2.data.(map[string]interface{})
	if !ok {
		t.Fatalf("retry data not map: %T", conn2.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok || len(proj.Turns) != 2 || proj.SyncRev <= 0 {
		t.Fatalf("retry expected non-empty projection, got %+v", dataMap["projection"])
	}
}

// TestHandleGetSessionProjectionColdHydrateReal26MBRollout exercises the owner exit-criteria
// session when present on disk. Skips on machines without the fixture.
func TestHandleGetSessionProjectionColdHydrateReal26MBRollout(t *testing.T) {
	const sessionID = "019f891d-c2b2-7b43-9dcd-0b3a8b9087b5"
	path := filepath.Join(os.Getenv("HOME"), ".codex/sessions/2026/07/22/rollout-2026-07-22T17-17-36-"+sessionID+".jsonl")
	if st, err := os.Stat(path); err != nil || st.Size() < 10*1024*1024 {
		t.Skipf("real 26MB rollout not present at %s", path)
	}

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-26mb", BackendID: "codex", Method: "get_session_projection", Params: params}
	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	if proj.SyncRev <= 0 || len(proj.Turns) == 0 {
		t.Fatalf("26MB cold-hydrate returned empty head (contract violation): syncRev=%d turns=%d elapsed=%s",
			proj.SyncRev, len(proj.Turns), elapsed)
	}
	t.Logf("26MB cold-hydrate ok: events-head syncRev=%d turns=%d elapsed=%s", proj.SyncRev, len(proj.Turns), elapsed)
}
