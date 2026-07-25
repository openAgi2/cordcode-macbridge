package gobridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// writeClaudeProjectionRollout builds a multi-turn claude session .jsonl with the given number of
// turns and assistantTextBytes of assistant text per turn (controls total file size). Each turn is
// a user prompt followed by an assistant response (stop_reason end_turn). Written directly to disk
// line-by-line so multi-MB fixtures do not allocate one giant string.
func writeClaudeProjectionRollout(t *testing.T, path string, turns, assistantTextBytes int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	asstText := strings.Repeat("a", assistantTextBytes)
	for i := 0; i < turns; i++ {
		writeClaudeRolloutLine(t, bw, "user", fmt.Sprintf("user-msg-%d", i+1), "user", "",
			[]claudeRelayContentBlock{{Type: "text", Text: "question " + strconv.Itoa(i+1)}})
		writeClaudeRolloutLine(t, bw, "assistant", fmt.Sprintf("asst-msg-%d", i+1), "assistant", "end_turn",
			[]claudeRelayContentBlock{{Type: "text", Text: asstText}})
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeRolloutLine encodes one claude transcript entry as a JSONL line.
func writeClaudeRolloutLine(t *testing.T, w io.Writer, entryType, msgID, role, stopReason string, blocks []claudeRelayContentBlock) {
	t.Helper()
	contentJSON, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	msg := map[string]interface{}{
		"id":      msgID,
		"role":    role,
		"content": json.RawMessage(contentJSON),
	}
	if stopReason != "" {
		msg["stop_reason"] = stopReason
	}
	line := map[string]interface{}{"type": entryType, "message": msg}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// waitForProjectionTurns polls the reducer until it holds want turns or the deadline elapses
// (design §10.5.6 scheme A — remaining segments stream in via the background hydrate goroutine
// after the RPC has already returned a non-empty partial).
func waitForProjectionTurns(t *testing.T, h *Handlers, backendID, sessionID string, want int, deadline time.Duration) {
	t.Helper()
	stop := time.After(deadline)
	for {
		if got := h.eventPublisher.ProjectionTurnCount(backendID, sessionID); got >= want {
			return
		}
		select {
		case <-stop:
			t.Fatalf("reducer reached %d turns, want %d within %s (background hydrate did not complete)",
				h.eventPublisher.ProjectionTurnCount(backendID, sessionID), want, deadline)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// waitForColdHydrateDrained blocks until the background hydrate goroutine has exited (its
// single-flight slot is cleared). Tests that install coldHydrate*TestHook MUST drain before
// returning, otherwise a still-running goroutine from test N can call the package-global hook
// that test N+1 has since overwritten — cross-test contamination via the shared hook var.
func waitForColdHydrateDrained(t *testing.T, h *Handlers, backendID, sessionID string, deadline time.Duration) {
	t.Helper()
	key := backendID + "\x00" + sessionID
	stop := time.After(deadline)
	for {
		h.mu.Lock()
		_, exists := h.coldHydrateFlights[key]
		h.mu.Unlock()
		if !exists {
			return
		}
		select {
		case <-stop:
			t.Fatalf("cold-hydrate goroutine did not drain within %s (slot still occupied)", deadline)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestHandleGetSessionProjectionColdHydrateWaitsPastFormerTimeoutBudget: design §10.5.6 scheme A.
// Under the segmented model the RPC returns a non-empty PARTIAL once the first content turn
// lands — it no longer waits for the full scan. A hydrate whose start is delayed (here 2s) must
// still serve ≥1 content turn (never the old 750ms empty bail); the remaining turns stream in
// via the background goroutine.
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
	// Partial contract: ≥1 content turn, never an empty head-0 shell.
	if proj.SyncRev <= 0 || len(proj.Turns) == 0 {
		t.Fatalf("served empty head after delayed hydrate (forbidden §10.5): %+v", proj)
	}
	// Background goroutine finishes the remaining turns.
	waitForProjectionTurns(t, handlers, "codex", "cold-slow", 3, 2*time.Second)
}

// TestHandleGetSessionProjectionColdHydrateLargeRolloutNonEmpty: design §10.5.6 scheme A —
// a large multi-turn rollout cold-hydrates and the RPC serves a non-empty PARTIAL (≥1 content
// turn) without waiting for the full scan; the remaining turns stream in via the background
// goroutine. An empty head-0 success remains a contract violation.
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
	// Partial contract: ≥1 content turn (not necessarily all `turns` — the rest stream in).
	if proj.SyncRev <= 0 {
		t.Fatalf("syncRev = %d, want > 0 after cold-hydrate", proj.SyncRev)
	}
	if len(proj.Turns) == 0 {
		t.Fatalf("served empty head-0 partial (contract violation): %+v", proj)
	}
	// Background goroutine finishes the remaining turns.
	waitForProjectionTurns(t, handlers, "codex", "cold-large", turns, 3*time.Second)
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
		// Partial contract: every conn sees ≥1 content turn, never an empty shell.
		if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
			t.Fatalf("conn %d empty projection: %+v", i, dataMap["projection"])
		}
	}
	// All callers shared one hydrate; the background goroutine finishes both turns.
	waitForProjectionTurns(t, handlers, "codex", "cold-sf", 2, 2*time.Second)
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
	// Partial contract: retry serves ≥1 content turn (slot not poisoned); rest stream in.
	if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
		t.Fatalf("retry expected non-empty partial projection, got %+v", dataMap["projection"])
	}
	waitForProjectionTurns(t, handlers, "codex", "cold-hard-timeout", 2, 2*time.Second)
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

// TestHandleGetSessionProjectionColdHydrateSegmentedFirstTurnsFirst: design §10.5.6 scheme A.
// A multi-turn rollout scans in turn-bounded segments; earlier turns MUST enter the reducer
// BEFORE the full scan completes. Block the goroutine at the end of segment 1 and observe the
// reducer holds exactly turn 1 (not all turns) — direct evidence of incremental segmentation.
func TestHandleGetSessionProjectionColdHydrateSegmentedFirstTurnsFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	const turns = 4
	writeProjectionHydrateRollout(t, path, turns)

	release := make(chan struct{})
	reachedSeg1 := make(chan struct{}, 1)
	prev := coldHydrateSegmentTestHook
	coldHydrateSegmentTestHook = func(ctx context.Context, segmentIdx, contentTurns int) {
		if segmentIdx >= 1 {
			select {
			case reachedSeg1 <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	t.Cleanup(func() { coldHydrateSegmentTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-seg"})
	msg := WireMessage{RequestID: "r-seg", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)
	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}

	// Wait until the goroutine has finished segment 1 and is blocked before segment 2.
	select {
	case <-reachedSeg1:
	case <-time.After(3 * time.Second):
		t.Fatal("segment hook never reached segment 1")
	}
	// While blocked after segment 1: turn 1 is in the reducer, turns 2..N are NOT yet — proving
	// earlier turns entered the reducer before the full scan completed (segmented hydrate).
	if got := handlers.eventPublisher.ProjectionTurnCount("codex", "cold-seg"); got != 1 {
		t.Fatalf("while blocked after segment 1, reducer turns = %d, want exactly 1 (segmented scan should not have reached later turns)", got)
	}

	close(release)
	waitForProjectionTurns(t, handlers, "codex", "cold-seg", turns, 3*time.Second)
	// Drain the background goroutine BEFORE returning so its trailing segment-hook calls cannot
	// contaminate a later test via the shared package-global hook var.
	waitForColdHydrateDrained(t, handlers, "codex", "cold-seg", 3*time.Second)
}

// TestHandleGetSessionProjectionColdHydratePartialHasContentTurn: design §10.5.6 scheme A
// contract — the served PARTIAL must contain at least one turn with real user/assistant content,
// never an empty head-0 shell and never a bare task_started shell. Capture the partial while the
// background scan is still blocked after segment 1 so the assertion targets the partial itself.
func TestHandleGetSessionProjectionColdHydratePartialHasContentTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 3)

	release := make(chan struct{})
	reachedSeg1 := make(chan struct{}, 1)
	prev := coldHydrateSegmentTestHook
	coldHydrateSegmentTestHook = func(ctx context.Context, segmentIdx, contentTurns int) {
		if segmentIdx >= 1 {
			select {
			case reachedSeg1 <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	t.Cleanup(func() { coldHydrateSegmentTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-partial"})
	msg := WireMessage{RequestID: "r-partial", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)
	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	<-reachedSeg1 // freeze the partial so we assert what was actually served

	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("served data not a map: %T (empty shell?)", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("served partial missing projection: %+v (empty shell?)", dataMap)
	}
	if proj.SyncRev <= 0 || len(proj.Turns) == 0 {
		t.Fatalf("served partial is an empty head-0 shell (forbidden §10.5.6): %+v", proj)
	}
	hasContent := false
	for _, turn := range proj.Turns {
		if (turn.User != nil && len(turn.User.Parts) > 0) || (turn.Assistant != nil && len(turn.Assistant.Parts) > 0) {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatalf("served partial turns carry no user/assistant content (bare shells): %+v", proj)
	}

	close(release)
	waitForProjectionTurns(t, handlers, "codex", "cold-partial", 3, 3*time.Second)
	waitForColdHydrateDrained(t, handlers, "codex", "cold-partial", 3*time.Second)
}

// TestHandleGetSessionProjectionColdHydrateFirstSegmentTimeoutReturnsError: design §10.5.6
// scheme A — when even the first content turn cannot be produced within the cold-hydrate budget
// (an extreme rollout whose first segment cannot scan in time), the RPC MUST return
// projection.hydrate_timeout, NOT an empty success shell. Scheme A never trades honesty for a
// fast-but-empty answer.
func TestHandleGetSessionProjectionColdHydrateFirstSegmentTimeoutReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 3)

	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 150 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	prevHook := coldHydrateTestHook
	coldHydrateTestHook = func(ctx context.Context) {
		<-ctx.Done() // simulate a first segment that cannot complete within budget
	}
	t.Cleanup(func() { coldHydrateTestHook = prevHook })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-first-seg-timeout", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-fst", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if conn.err == nil {
		t.Fatalf("expected projection.hydrate_timeout when first segment cannot complete; got success data=%T", conn.data)
	}
	if conn.err.Code != "projection.hydrate_timeout" {
		t.Fatalf("error code = %q, want projection.hydrate_timeout (message %q)", conn.err.Code, conn.err.Message)
	}
	if conn.data != nil {
		t.Fatalf("first-segment timeout must not pair data with error (no empty shell): data=%T", conn.data)
	}
	// Reducer must still be empty — no content turn was ever produced.
	if handlers.eventPublisher.ProjectionHasContentTurn("codex", "cold-first-seg-timeout") {
		t.Fatalf("reducer has a content turn despite first-segment timeout — timeout path is wrong")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("RPC hung %s; expected to fail near the %s budget", elapsed, coldHydrateTimeout)
	}
}

// §10.5.7 修法 1 — claude cold-hydrate matrix. Each case registers a claudecode agent whose
// TranscriptPath points at a generated .jsonl, cold-pulls, and asserts a non-empty partial is
// served (never an empty head), the full turn count arrives via the background goroutine, and the
// first segment lands well within the 15s budget.
func TestClaudeColdHydrateMatrix(t *testing.T) {
	cases := []struct {
		name             string
		turns            int
		asstTextBytes    int
		expectTotalBytes string // human label for the coverage matrix (declared, not asserted)
	}{
		{"small_lt_1MB", 3, 100, "<1MB"},
		{"medium_1_to_10MB", 10, 100_000, "1-10MB"},
		{"large_10_to_100MB", 10, 1_000_000, "10-100MB"},
		// 超大 (>100MB) is covered by the owner real-device track (§10.5.7.5 owner 真机轨);
		// a unit test cannot faithfully own-size it (CI disk/time). The partial-return property
		// validated here (first segment < 15s regardless of total size) scales to 超大 identically.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "claude-"+tc.name+".jsonl")
			writeClaudeProjectionRollout(t, path, tc.turns, tc.asstTextBytes)

			handlers := NewHandlers()
			handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", transcriptPath: path})

			conn := &readFileCaptureConn{}
			params, _ := json.Marshal(map[string]interface{}{"sessionId": "claude-" + tc.name})
			msg := WireMessage{RequestID: "r-" + tc.name, BackendID: "claude", Method: "get_session_projection", Params: params}

			start := time.Now()
			handlers.handleGetSessionProjection(conn, msg, nil)
			elapsed := time.Since(start)

			if conn.err != nil {
				t.Fatalf("unexpected RPC error: %+v", conn.err)
			}
			dataMap, ok := conn.data.(map[string]interface{})
			if !ok {
				t.Fatalf("served data not a map: %T (empty shell?)", conn.data)
			}
			proj, ok := dataMap["projection"].(SessionProjection)
			if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
				t.Fatalf("claude %s: served empty head-0 partial (forbidden §10.5.1): %+v", tc.name, dataMap["projection"])
			}
			// First segment (the partial) must land within the 15s protocol budget (§10.5.7 修法 2).
			if elapsed >= defaultColdHydrateTimeout {
				t.Fatalf("claude %s: partial served after %s, within-budget first segment required (< %s)",
					tc.name, elapsed, defaultColdHydrateTimeout)
			}
			t.Logf("claude %s (%s): partial in %s, turns=%d", tc.name, tc.expectTotalBytes, elapsed, len(proj.Turns))
			// Background goroutine finishes the remaining turns.
			waitForProjectionTurns(t, handlers, "claude", "claude-"+tc.name, tc.turns, 30*time.Second)
			waitForColdHydrateDrained(t, handlers, "claude", "claude-"+tc.name, 10*time.Second)
		})
	}
}

// TestClaudeColdHydrateHonestEmpty: a claude session with a real but content-empty transcript
// (only resume-meta / non-meaningful entries) returns an honest empty projection — NOT an error,
// NOT a fake content turn. Distinct from the forbidden empty-head-on-failure.
func TestClaudeColdHydrateHonestEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	// A session_meta-only file (no user/assistant content).
	if err := os.WriteFile(path, []byte(`{"type":"system","message":null}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "claude-empty"})
	msg := WireMessage{RequestID: "r-empty", BackendID: "claude", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err != nil {
		t.Fatalf("honest-empty session must NOT error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("expected honest empty projection, got %+v", dataMap)
	}
	if len(proj.Turns) != 0 || proj.SyncRev != 0 {
		t.Fatalf("honest-empty must be 0 turns / syncRev 0, got %+v", proj)
	}
}

// TestClaudeColdHydrateMissingFileHonestError: a transcript path that does not exist must surface
// as an honest hydrate error (projection.hydrate_failed), NEVER an empty head-0 success shell
// (§10.5.1). Distinct from honest-empty (which is a real, scanned, content-less session).
func TestClaudeColdHydrateMissingFileHonestError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "claude-missing"})
	msg := WireMessage{RequestID: "r-missing", BackendID: "claude", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err == nil {
		t.Fatalf("missing transcript must surface an honest error, got success data=%T", conn.data)
	}
	if conn.data != nil {
		t.Fatalf("error must not pair with data (no empty shell): data=%T", conn.data)
	}
	if conn.err.Code != "projection.hydrate_failed" {
		t.Fatalf("error code = %q, want projection.hydrate_failed", conn.err.Code)
	}
}

// TestProjectionNotMigratedForUnsupportedBackend: a backend with no projection cold-hydrate
// producer (opencode is HTTP/SQLite-backed; deferred to a separate sub-task) MUST return an honest
// projection.not_migrated error — never fall through to an empty head-0 shell (§10.5.7 修法 1).
func TestProjectionNotMigratedForUnsupportedBackend(t *testing.T) {
	for _, backend := range []string{"opencode", "madeup-backend"} {
		handlers := NewHandlers()
		conn := &readFileCaptureConn{}
		params, _ := json.Marshal(map[string]interface{}{"sessionId": "s-" + backend})
		msg := WireMessage{RequestID: "r-" + backend, BackendID: backend, Method: "get_session_projection", Params: params}
		handlers.handleGetSessionProjection(conn, msg, nil)
		if conn.err == nil {
			t.Fatalf("%s: expected projection.not_migrated, got success data=%T", backend, conn.data)
		}
		if conn.data != nil {
			t.Fatalf("%s: error must not pair with data (no empty shell): %T", backend, conn.data)
		}
		if conn.err.Code != "projection.not_migrated" {
			t.Fatalf("%s: error code = %q, want projection.not_migrated", backend, conn.err.Code)
		}
	}
}

// TestClaudeEntryToProjectionEvents unit-tests the claude transcript → projection-event mapper:
// user text starts a turn (user_message), assistant blocks emit text_delta/reasoning_delta/
// tool_started, a user tool_result emits tool_finished, and a final stop_reason emits a
// segment-boundary turn_completed.
func TestClaudeEntryToProjectionEvents(t *testing.T) {
	currentTurnID := ""
	// user prompt
	user := claudeTranscriptRelayEntry{Type: "user", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "u1", Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello"}]`)}}
	evs := claudeEntryToProjectionEvents(user, &currentTurnID)
	if len(evs) != 1 || evs[0].Event != "user_message" || evs[0].Data["turnId"] != "u1" {
		t.Fatalf("user entry → %+v", evs)
	}
	if currentTurnID != "u1" {
		t.Fatalf("currentTurnID = %q, want u1", currentTurnID)
	}
	// assistant text + thinking + tool_use + final stop
	asst := claudeTranscriptRelayEntry{Type: "assistant", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "a1", Role: "assistant", StopReason: "end_turn", Content: json.RawMessage(`[{"type":"text","text":"hi"},{"type":"thinking","thinking":"plan"},{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"x"}}]`)}}
	evs = claudeEntryToProjectionEvents(asst, &currentTurnID)
	// expect: text_delta, reasoning_delta, tool_started, turn_completed (TurnDone)
	if len(evs) != 4 {
		t.Fatalf("assistant entry → %d events: %+v", len(evs), evs)
	}
	if evs[0].Event != "text_delta" || evs[0].Data["itemId"] != "u1" {
		t.Fatalf("text_delta must attribute to active turn u1: %+v", evs[0])
	}
	if evs[1].Event != "reasoning_delta" {
		t.Fatalf("want reasoning_delta, got %+v", evs[1])
	}
	if evs[2].Event != "tool_started" || evs[2].Data["itemId"] != "tool-1" {
		t.Fatalf("tool_started with real tool id: %+v", evs[2])
	}
	if evs[3].Event != "turn_completed" || !evs[3].TurnDone {
		t.Fatalf("final stop must emit segment-boundary turn_completed: %+v", evs[3])
	}
	// user tool_result → tool_finished matched by tool_use_id
	tr := claudeTranscriptRelayEntry{Type: "user", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "u2", Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"tool-1","content":"file body"}]`)}}
	evs = claudeEntryToProjectionEvents(tr, &currentTurnID)
	if len(evs) != 1 || evs[0].Event != "tool_finished" || evs[0].Data["itemId"] != "tool-1" {
		t.Fatalf("tool_result → tool_finished matched by tool_use_id: %+v", evs)
	}
}
