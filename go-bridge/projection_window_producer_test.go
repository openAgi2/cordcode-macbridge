package gobridge

// PERF-S4B producer tests: window slicing (anchoring/cursors/bounds/locate), typed
// errors, handler capability gates, fence release on typed failures, disconnect cleanup,
// hello negotiation, and full-vs-window semantic parity on the canonical fixtures
// (codex-web official-0.149.0-alpha.4 + opencode-web official-1.18.18 derived).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func windowTestProjection(n int) SessionProjection {
	turns := make([]TurnProjection, 0, n)
	for index := 0; index < n; index++ {
		turnID := fmt.Sprintf("t%02d", index)
		turns = append(turns, TurnProjection{
			TurnID: turnID,
			Status: "completed",
			User: &MessageProjection{
				ID: "u" + turnID, Role: "user",
				Parts: []ProjectionPart{{Type: "text", Text: "user " + turnID}},
			},
			Assistant: &MessageProjection{
				ID: "a" + turnID, Role: "assistant",
				Parts: []ProjectionPart{{Type: "text", Text: "assistant " + turnID}},
			},
		})
	}
	return SessionProjection{SessionID: "ses-win", SyncRev: 42, Execution: ExecutionView{Phase: "idle"}, Turns: turns}
}

const windowTestEpoch = "bep-test-1"

// Anchoring paragraph: window_0/latest are tail-anchored; cursor-iff-remainder; resume
// iff hasNewer=false; coverage=full exactly when the whole projection is inside.
func TestProjectionWindowSliceWindow0TailAnchoring(t *testing.T) {
	proj := windowTestProjection(10)
	response, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{
		Direction: "window_0", Limit: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Turns) != 4 {
		t.Fatalf("turns = %d, want 4", len(response.Turns))
	}
	if got := response.Turns[0].TurnID; got != proj.Turns[6].TurnID {
		t.Fatalf("window head = %s, want %s (tail-anchored)", got, proj.Turns[6].TurnID)
	}
	if got := response.Turns[3].TurnID; got != proj.Turns[9].TurnID {
		t.Fatalf("window tail = %s, want %s", got, proj.Turns[9].TurnID)
	}
	w := response.Window
	if *w.HeadTurnID != proj.Turns[6].TurnID || *w.TailTurnID != proj.Turns[9].TurnID {
		t.Fatalf("descriptor boundary ids = %v/%v", *w.HeadTurnID, *w.TailTurnID)
	}
	if !w.HasOlder || w.HasNewer {
		t.Fatalf("hasOlder=%v hasNewer=%v, want true/false", w.HasOlder, w.HasNewer)
	}
	if w.NextOlderCursor == "" || w.NextNewerCursor != "" {
		t.Fatalf("cursor-iff-remainder violated: older=%q newer=%q", w.NextOlderCursor, w.NextNewerCursor)
	}
	if w.Coverage != "window" {
		t.Fatalf("coverage = %q, want window", w.Coverage)
	}
	if response.Resume == nil || response.Resume.Kind != "at_head" {
		t.Fatalf("resume = %+v, want at_head", response.Resume)
	}
	if response.SyncRev != proj.SyncRev {
		t.Fatalf("syncRev = %d, want %d (R4 admission cut)", response.SyncRev, proj.SyncRev)
	}
}

func TestProjectionWindowSliceWindow0FullCoverageWhenSmall(t *testing.T) {
	proj := windowTestProjection(3)
	response, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "window_0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Turns) != 3 || response.Window.Coverage != "full" || response.Window.HasOlder || response.Window.NextOlderCursor != "" {
		t.Fatalf("small projection must serve full coverage: %+v", response.Window)
	}
	if response.Turns[0].TurnID != proj.Turns[0].TurnID {
		t.Fatal("full coverage must start at projection head")
	}
}

// R3/R6 chain walk: window_0 → older* reassembles the exact turn chain with no duplicates
// and no gaps (unique page ownership), ending with hasOlder=false.
func TestProjectionWindowSliceOlderWalkReassemblesChain(t *testing.T) {
	proj := windowTestProjection(23)
	seen := map[string]bool{}
	collected := 0

	response, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "window_0", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range response.Turns {
		seen[turn.TurnID] = true
		collected++
	}
	oldestCollected := indexOfTurn(proj.Turns, response.Turns[0].TurnID)
	cursor := response.Window.NextOlderCursor
	for cursor != "" {
		page, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "older", Cursor: cursor, Limit: 5})
		if err != nil {
			t.Fatalf("older walk failed: %v", err)
		}
		if len(page.Turns) == 0 {
			t.Fatal("older page must not be empty while hasOlder was true")
		}
		// Pages arrive newest→oldest; each page's last turn must sit immediately before
		// the oldest turn collected so far (boundary adjacency, R3).
		pageBoundary := indexOfTurn(proj.Turns, page.Turns[len(page.Turns)-1].TurnID)
		if pageBoundary != oldestCollected-1 {
			t.Fatalf("page boundary index %d not adjacent to collected oldest index %d", pageBoundary, oldestCollected)
		}
		for _, turn := range page.Turns {
			if seen[turn.TurnID] {
				t.Fatalf("turn %s served twice (R3 unique page ownership)", turn.TurnID)
			}
			seen[turn.TurnID] = true
			collected++
		}
		oldestCollected = indexOfTurn(proj.Turns, page.Turns[0].TurnID)
		cursor = page.Window.NextOlderCursor
		if page.Window.HasOlder != (cursor != "") {
			t.Fatalf("hasOlder=%v but cursor=%q (cursor-iff-remainder)", page.Window.HasOlder, cursor)
		}
	}
	if collected != len(proj.Turns) || len(seen) != len(proj.Turns) {
		t.Fatalf("walk covered %d turns (unique %d), projection has %d", collected, len(seen), len(proj.Turns))
	}
	if !seen[proj.Turns[0].TurnID] || !seen[proj.Turns[len(proj.Turns)-1].TurnID] {
		t.Fatal("walk must reach both chain ends")
	}
}

// R7 strict turn-chain: newer never skips an unloaded turn and ends hasNewer=false at tail.
func TestProjectionWindowSliceNewerStrictChain(t *testing.T) {
	proj := windowTestProjection(10)
	anchorCursor := encodeProjectionWindowCursor(projectionWindowCursor{
		V: 1, BridgeEpoch: windowTestEpoch, BackendID: "codex-web", SessionID: proj.SessionID,
		AnchorTurnID: "t01", Side: "n",
	})
	response, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "newer", Cursor: anchorCursor, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{response.Turns[0].TurnID, response.Turns[1].TurnID, response.Turns[2].TurnID}; got[0] != "t02" || got[1] != "t03" || got[2] != "t04" {
		t.Fatalf("newer page = %v, want [t02 t03 t04] (strict order, no skips)", got)
	}
	if !response.Window.HasNewer || response.Window.NextNewerCursor == "" {
		t.Fatalf("hasNewer=%v cursor=%q, want remainder toward tail", response.Window.HasNewer, response.Window.NextNewerCursor)
	}
	final, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "newer", Cursor: response.Window.NextNewerCursor, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Turns) != 5 || final.Turns[0].TurnID != "t05" || final.Turns[4].TurnID != "t09" {
		t.Fatalf("tail page = %d turns starting %s", len(final.Turns), final.Turns[0].TurnID)
	}
	if final.Window.HasNewer || final.Resume == nil || final.Resume.Kind != "at_head" {
		t.Fatalf("tail page must end hasNewer=false with resume at_head: %+v", final.Window)
	}
}

func TestProjectionWindowSliceLocate(t *testing.T) {
	proj := windowTestProjection(30)
	response, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "locate", AnchorTurnID: "t15", Limit: 6})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, turn := range response.Turns {
		if turn.TurnID == "t15" {
			found = true
		}
	}
	if !found {
		t.Fatalf("locate window must contain the anchor: %+v", response.Window)
	}
	if len(response.Turns) > 6 {
		t.Fatalf("locate window = %d turns, want ≤ limit", len(response.Turns))
	}

	_, err = sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "locate", AnchorTurnID: "missing", Limit: 6})
	if err != errProjectionWindowLocateOut {
		t.Fatalf("unknown anchor error = %v, want locate_out_of_window", err)
	}
}

// R1 scope + R6 staleness + retention.
func TestProjectionWindowCursorScopeAndStaleness(t *testing.T) {
	proj := windowTestProjection(10)
	cursor := encodeProjectionWindowCursor(projectionWindowCursor{
		V: 1, BridgeEpoch: windowTestEpoch, BackendID: "codex-web", SessionID: proj.SessionID,
		AnchorTurnID: "t05", Side: "o",
	})
	if _, err := sliceProjectionWindow("opencode-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "older", Cursor: cursor}); err != errProjectionWindowScopeMismatch {
		t.Fatalf("cross-backend cursor error = %v, want scope mismatch", err)
	}
	if _, err := sliceProjectionWindow("codex-web", "ses-other", windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "older", Cursor: cursor}); err != errProjectionWindowScopeMismatch {
		t.Fatalf("cross-session cursor error = %v, want scope mismatch", err)
	}
	if _, err := sliceProjectionWindow("codex-web", proj.SessionID, "bep-restarted", proj, GetSessionProjectionWindowParams{Direction: "older", Cursor: cursor}); err != errProjectionWindowCursorStale {
		t.Fatalf("cross-epoch cursor error = %v, want cursor stale", err)
	}
	shrunk := windowTestProjection(3)
	if _, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, shrunk, GetSessionProjectionWindowParams{Direction: "older", Cursor: cursor}); err != errProjectionWindowCursorStale {
		t.Fatalf("retention-miss cursor error = %v, want cursor stale", err)
	}
	for _, malformed := range []string{"", "not-base64!!!", "e30=", "bnVsbA=="} {
		if _, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "older", Cursor: malformed}); err != errProjectionWindowCursorStale {
			t.Fatalf("malformed cursor %q error = %v, want cursor stale (re-freeze mapping)", malformed, err)
		}
	}
}

// R5 byte bound: truncation at a turn boundary from the far side; response stays bounded;
// a single oversized turn is still served alone (never split).
func TestProjectionWindowSliceByteBound(t *testing.T) {
	bigText := strings.Repeat("x", 900<<10)
	proj := windowTestProjection(12)
	for index := range proj.Turns {
		proj.Turns[index].Assistant.Parts = []ProjectionPart{{Type: "text", Text: bigText}}
	}
	response, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "window_0", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Turns) >= 10 {
		t.Fatalf("byte bound must bind before the turn limit: %d turns", len(response.Turns))
	}
	if got := response.Turns[len(response.Turns)-1].TurnID; got != proj.Turns[11].TurnID {
		t.Fatalf("tail-anchored truncation keeps the tail, got last=%s", got)
	}
	if !response.Window.HasOlder || response.Window.NextOlderCursor == "" {
		t.Fatal("remainder must be expressed via hasOlder + nextOlderCursor")
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > maxWindowEncodedBytes {
		t.Fatalf("window payload = %d bytes, want ≤ %d", len(payload), maxWindowEncodedBytes)
	}

	// Single oversized turn (> 4MiB) is served alone, never split.
	huge := windowTestProjection(2)
	hugeText := strings.Repeat("y", (4<<20)+4096)
	huge.Turns[0].Assistant.Parts = []ProjectionPart{{Type: "text", Text: hugeText}}
	single, err := sliceProjectionWindow("codex-web", huge.SessionID, windowTestEpoch, huge, GetSessionProjectionWindowParams{Direction: "window_0", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Turns) != 1 || single.Turns[0].TurnID != huge.Turns[1].TurnID {
		t.Fatalf("oversized-turn window = %d turns (head %s), want the boundary turn alone", len(single.Turns), single.Turns[0].TurnID)
	}
}

func TestProjectionWindowSliceEmptyProjection(t *testing.T) {
	proj := SessionProjection{SessionID: "ses-empty", SyncRev: 7, Execution: ExecutionView{Phase: "idle"}}
	response, err := sliceProjectionWindow("codex-web", proj.SessionID, windowTestEpoch, proj, GetSessionProjectionWindowParams{Direction: "window_0"})
	if err != nil {
		t.Fatal(err)
	}
	w := response.Window
	if w.HeadTurnID != nil || w.TailTurnID != nil || w.HasOlder || w.HasNewer || w.Coverage != "full" || w.NextOlderCursor != "" || w.NextNewerCursor != "" {
		t.Fatalf("empty projection window = %+v", w)
	}
	if response.Resume != nil {
		t.Fatal("empty projection must not claim at_head resume")
	}
	if response.SyncRev != 7 {
		t.Fatalf("syncRev = %d, want 7", response.SyncRev)
	}
}

// Hello negotiation: prerequisite validation, echo gating on the rollout flag.
func TestNegotiateProjectionWindowV1(t *testing.T) {
	publisher := NewEventPublisher(windowTestEpoch)
	conn := &scopeTestConn{}

	buildAck := func() *HelloAckMessage {
		return &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	}
	server := &Server{eventPublisher: publisher}

	helloWindowOnly := &HelloMessage{Capabilities: []string{"projection_window_v1"}}
	ack := buildAck()
	if server.negotiateProjectionWindowV1(ack, helloWindowOnly, conn) {
		t.Fatal("projection_window_v1 without session_sync_v2 must fail hello")
	}
	if ack.Ok || ack.Error == nil || ack.Error.Code != "protocol.invalid_capabilities" {
		t.Fatalf("ack = %+v err=%+v", ack, ack.Error)
	}
	if publisher.ConnProjectionWindowV1(conn) {
		t.Fatal("failing hello must not mark the connection")
	}

	helloBoth := &HelloMessage{Capabilities: []string{"session_sync_v2", "projection_window_v1"}}
	ack = buildAck()
	if !server.negotiateProjectionWindowV1(ack, helloBoth, conn) {
		t.Fatal("valid declaration must not fail hello")
	}
	if ack.Capabilities["projection_window_v1"] || publisher.ConnProjectionWindowV1(conn) {
		t.Fatal("rollout flag off: no echo, no conn mark (frozen release ordering)")
	}

	server.SetProjectionWindowEnabled(true)
	ack = buildAck()
	if !server.negotiateProjectionWindowV1(ack, helloBoth, conn) {
		t.Fatal("valid declaration with flag on must not fail hello")
	}
	if !ack.Capabilities["projection_window_v1"] || !publisher.ConnProjectionWindowV1(conn) {
		t.Fatal("flag on + declared: echo and conn mark required")
	}

	ack = buildAck()
	if !server.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2"}}, conn) {
		t.Fatal("undeclared client must pass through untouched")
	}
	if _, present := ack.Capabilities["projection_window_v1"]; present {
		t.Fatal("undeclared client must not see the capability echo")
	}
}

// Disconnect cleanup: the per-conn window mark dies with the connection; other clients keep theirs.
func TestProjectionWindowConnMarkCleanupOnUnregister(t *testing.T) {
	publisher := NewEventPublisher(windowTestEpoch)
	connA := &scopeTestConn{}
	connB := &scopeTestConn{}
	publisher.SetConnProjectionWindowV1(connA, true)
	publisher.SetConnProjectionWindowV1(connB, true)
	publisher.UnregisterConnection(connA)
	if publisher.ConnProjectionWindowV1(connA) {
		t.Fatal("unregistered connection must lose the projection_window_v1 mark")
	}
	if !publisher.ConnProjectionWindowV1(connB) {
		t.Fatal("unrelated connection must keep its mark")
	}
}

// windowTestHarness: kernel committed through the REAL hydrate path (opencode-web
// pathless rich history), then window RPC against a marked capture conn.
func windowTestHarness(t *testing.T, entries []core.RichHistoryEntry) (*Handlers, *readFileCaptureConn) {
	t.Helper()
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	agent := &fakeAgent{name: "opencode-web", richHistory: entries}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()
	return h, &readFileCaptureConn{}
}

func windowDispatch(h *Handlers, conn *readFileCaptureConn, sessionID string, params map[string]any) (*WireError, map[string]any) {
	raw, _ := json.Marshal(params)
	msg := WireMessage{RequestID: "r-win", BackendID: "opencode-web", Method: "get_session_projection_window", Params: raw}
	h.handleGetSessionProjectionWindow(conn, msg, nil)
	if conn.err != nil {
		return conn.err, nil
	}
	encoded, _ := json.Marshal(conn.data)
	var decoded map[string]any
	_ = json.Unmarshal(encoded, &decoded)
	return nil, decoded
}

func TestProjectionWindowHandlerCapabilityGateAndServe(t *testing.T) {
	h, conn := windowTestHarness(t, []core.RichHistoryEntry{
		{ID: "u1", Role: "user", Content: "hello"},
		{ID: "a1", Role: "assistant", Content: "world", Parts: []map[string]any{{"type": "text", "content": "world"}}},
		{ID: "u2", Role: "user", Content: "second"},
		{ID: "a2", Role: "assistant", Content: "reply-two", Parts: []map[string]any{{"type": "text", "content": "reply-two"}}},
		{ID: "u3", Role: "user", Content: "third"},
		{ID: "a3", Role: "assistant", Content: "reply-three", Parts: []map[string]any{{"type": "text", "content": "reply-three"}}},
	})

	// Undeclared connection: frozen compatibility answer — typed failure, no window field.
	wireErr, _ := windowDispatch(h, conn, "ses-gated", map[string]any{"sessionId": "ses-gated", "direction": "window_0"})
	if wireErr == nil || wireErr.Code != "protocol.capability_required" {
		t.Fatalf("undeclared conn error = %+v, want protocol.capability_required", wireErr)
	}
	if !strings.Contains(wireErr.Message, "projection_window_v1") {
		t.Fatalf("capability_required message must name the capability: %q", wireErr.Message)
	}

	// Declared connection: hydrate → fence → slice → success shape. Three user
	// messages → three kernel turns; limit=1 serves only the tail turn.
	h.eventPublisher.SetConnProjectionWindowV1(conn, true)
	wireErr, response := windowDispatch(h, conn, "ses-gated", map[string]any{"sessionId": "ses-gated", "direction": "window_0", "limit": 1})
	if wireErr != nil {
		t.Fatalf("declared conn window serve failed: %+v", wireErr)
	}
	window, _ := response["window"].(map[string]any)
	if window == nil {
		t.Fatalf("response missing window: %+v", response)
	}
	turns, _ := response["turns"].([]any)
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	if response["syncRev"] == nil {
		t.Fatalf("syncRev missing: %+v", response)
	}
	resume, _ := response["resume"].(map[string]any)
	if resume == nil || resume["kind"] != "at_head" {
		t.Fatalf("tail window resume = %+v, want at_head", response["resume"])
	}
	if window["hasNewer"] != false || window["hasOlder"] != true {
		t.Fatalf("limit=1 tail window flags: %+v", window)
	}

	// R4: syncRev equals the kernel head at admission.
	proj, ok := h.projectionKernel.Snapshot("opencode-web", "ses-gated")
	if !ok {
		t.Fatal("kernel snapshot missing after window serve")
	}
	if int(response["syncRev"].(float64)) != proj.SyncRev {
		t.Fatalf("window syncRev = %v, kernel head = %d", response["syncRev"], proj.SyncRev)
	}

	// Older walk with the served cursor; then cross-backend reuse → scope mismatch.
	olderCursor, _ := window["nextOlderCursor"].(string)
	if olderCursor == "" {
		t.Fatal("hasOlder window must carry nextOlderCursor")
	}
	wireErr, older := windowDispatch(h, conn, "ses-gated", map[string]any{"sessionId": "ses-gated", "direction": "older", "cursor": olderCursor, "limit": 5})
	if wireErr != nil {
		t.Fatalf("older walk failed: %+v", wireErr)
	}
	olderWindow, _ := older["window"].(map[string]any)
	olderTurns, _ := older["turns"].([]any)
	if len(olderTurns) != 2 {
		t.Fatalf("older page turns = %d, want the remaining two turns", len(olderTurns))
	}
	if olderWindow["hasOlder"] != false || olderWindow["coverage"] != "full" {
		t.Fatalf("older terminal page = %+v", olderWindow)
	}

	raw, _ := json.Marshal(map[string]any{"sessionId": "ses-gated", "backendId": "opencode", "direction": "older", "cursor": olderCursor})
	msg := WireMessage{RequestID: "r-scope", BackendID: "opencode", Method: "get_session_projection_window", Params: raw}
	h.handleGetSessionProjectionWindow(conn, msg, nil)
	if conn.err == nil || conn.err.Code != "projection_window.cursor_scope_mismatch" {
		t.Fatalf("cross-backend cursor error = %+v", conn.err)
	}
}

func TestProjectionWindowHandlerLimitAndLocateTypedFailures(t *testing.T) {
	h, conn := windowTestHarness(t, []core.RichHistoryEntry{
		{ID: "u1", Role: "user", Content: "hello"},
		{ID: "a1", Role: "assistant", Content: "world", Parts: []map[string]any{{"type": "text", "content": "world"}}},
	})
	h.eventPublisher.SetConnProjectionWindowV1(conn, true)

	wireErr, _ := windowDispatch(h, conn, "ses-limits", map[string]any{"sessionId": "ses-limits", "direction": "window_0", "limit": maxWindowTurns + 1})
	if wireErr == nil || wireErr.Code != "projection_window.limit_exceeded" {
		t.Fatalf("limit error = %+v", wireErr)
	}

	wireErr, _ = windowDispatch(h, conn, "ses-limits", map[string]any{"sessionId": "ses-limits", "direction": "locate", "anchorTurnId": "nope"})
	if wireErr == nil || wireErr.Code != "projection_window.locate_out_of_window" {
		t.Fatalf("locate error = %+v", wireErr)
	}

	// The typed-failure paths must have released the snapshot fence: a follow-up
	// window_0 on the same conn+session succeeds.
	wireErr, response := windowDispatch(h, conn, "ses-limits", map[string]any{"sessionId": "ses-limits", "direction": "window_0"})
	if wireErr != nil || response["window"] == nil {
		t.Fatalf("follow-up window after typed failure failed: err=%+v response=%+v", wireErr, response)
	}
}
