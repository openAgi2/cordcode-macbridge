package gobridge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTranscriptProbe swaps the package-level transcriptStateProbe for a counter
// and restores it at test cleanup. Returns the *counter so callers can assert.
func withTranscriptProbe(t *testing.T) *int {
	t.Helper()
	ticks := 0
	prev := transcriptStateProbe
	transcriptStateProbe = func() { ticks++ }
	t.Cleanup(func() { transcriptStateProbe = prev })
	return &ticks
}

// TestEnrichSessionStatesForList_NoTranscript_NoMutation is the core list-safe
// invariant proof: the batch enricher must (a) report correct runtime state with
// runningMap authoritative, (b) downgrade stale-running registry entries IN THE
// REPORT ONLY without mutating the registry, (c) preserve reasoningEffort
// injection, and (d) never touch transcript functions.
func TestEnrichSessionStatesForList_NoTranscript_NoMutation(t *testing.T) {
	ticks := withTranscriptProbe(t)

	handlers := newTestHandlers(t)
	agent := &fakeAgent{name: "claudecode", reasoningEffort: "ultra"}

	// Registry setup:
	//   staleA — registry running, runningMap will NOT confirm (stale-running row)
	//   liveB  — registry running AND runningMap confirms (genuine running)
	//   idleC  — absent from registry, not in runningMap (ordinary idle row)
	handlers.sessions.markRunning("staleA")
	handlers.sessions.markRunning("liveB")

	runningMap := map[string]bool{"liveB": true}
	in := []map[string]interface{}{
		{"id": "staleA"},
		{"id": "liveB"},
		{"id": "idleC"},
	}
	out := handlers.enrichSessionStatesForList(in, agent, runningMap)

	stateOf := func(m map[string]interface{}) string {
		v, _ := m["runtimeState"].(string)
		return v
	}
	if got := stateOf(out[0]); got != "idle" {
		t.Fatalf("staleA reported %q, want idle (runningMap authoritative; stale registry downgraded in report only)", got)
	}
	if got := stateOf(out[1]); got != "running" {
		t.Fatalf("liveB reported %q, want running", got)
	}
	if got := stateOf(out[2]); got != "idle" {
		t.Fatalf("idleC reported %q, want idle", got)
	}

	// Registry must be UNCHANGED — list path never calls markIdle.
	if ts, ok := handlers.sessions.get("staleA"); !ok || string(ts.state) != string(sessionStateRunning) {
		t.Fatalf("staleA registry state mutated by list path: state=%v ok=%v (must stay running; list is read-only)", ts.state, ok)
	}
	if ts, ok := handlers.sessions.get("liveB"); !ok || string(ts.state) != string(sessionStateRunning) {
		t.Fatalf("liveB registry state changed: state=%v ok=%v", ts.state, ok)
	}

	// reasoningEffort must still be injected for claude rows missing it.
	if got := out[0]["reasoningEffort"]; got != "ultra" {
		t.Fatalf("staleA reasoningEffort = %#v, want ultra (list-safe path must preserve injection)", got)
	}

	// HARD assertion: zero transcript opens from list enrichment.
	if *ticks != 0 {
		t.Fatalf("list enrichment opened transcript functions %d time(s); want 0 (no findClaudeSessionFile / detectClaudeTranscriptState per row)", *ticks)
	}
	// The batch enricher takes a precomputed runningMap; it must not call
	// GetRunningSessionIDs itself.
	if agent.runningCalls != 0 {
		t.Fatalf("batch enricher invoked GetRunningSessionIDs %d time(s); want 0 (runningMap is precomputed by getRunningMap)", agent.runningCalls)
	}
}

// TestGetRunningMap_NonListerReturnsNil proves the type-assertion seam: agents
// that do not implement core.RunningSessionLister get a nil runningMap, so
// applyListRuntimeState falls back to the registry (no GetRunningSessionIDs call).
func TestGetRunningMap_NonListerReturnsNil(t *testing.T) {
	handlers := newTestHandlers(t)
	non := &unsupportedMutationAgent{name: "codex"}
	if got := handlers.getRunningMap(context.Background(), non); got != nil {
		t.Fatalf("getRunningMap(non-lister) = %v, want nil", got)
	}
	// And nil agent.
	if got := handlers.getRunningMap(context.Background(), nil); got != nil {
		t.Fatalf("getRunningMap(nil agent) = %v, want nil", got)
	}
}

// TestApplyListRuntimeState_NilRunningMapFallsBackToRegistry covers the
// non-lister / lookup-error path: when runningMap is nil, runtime state comes
// from the registry (last-known), with idle as the default for unknown sessions.
func TestApplyListRuntimeState_NilRunningMapFallsBackToRegistry(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.sessions.markRunning("reg-running")

	if m := handlers.applyListRuntimeState(map[string]interface{}{"id": "reg-running"}, nil); m["runtimeState"] != "running" {
		t.Fatalf("nil runningMap fallback for reg-running = %#v, want running (registry)", m["runtimeState"])
	}
	if m := handlers.applyListRuntimeState(map[string]interface{}{"id": "unknown"}, nil); m["runtimeState"] != "idle" {
		t.Fatalf("unknown + nil runningMap = %#v, want idle (default)", m["runtimeState"])
	}
}

// TestListSessionsClaude_RunningMapComputedOncePerRequest is the Fix-2 hoisting
// proof plus the hard zero-transcript-open rule, driven through the real
// handleListSessions Claude branch with a stale-running registry row present.
func TestListSessionsClaude_RunningMapComputedOncePerRequest(t *testing.T) {
	ticks := withTranscriptProbe(t)

	agent := &fakeAgent{
		name:              "claudecode",
		reasoningEffort:   "high",
		runningSessionIDs: map[string]bool{"ses_running": true},
	}

	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-claude-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ses_running", "ses_idle", "ses_stale"} {
		if err := os.WriteFile(filepath.Join(projectDir, id+".jsonl"), []byte("{}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	catalog := newClaudeSessionCatalog(projectsDir)
	catalog.parseSession = func(string, time.Time) claudeSessionScanResult {
		return claudeSessionScanResult{
			Title:     "s",
			CreatedAt: time.Unix(1710000000, 0).UTC(),
			UpdatedAt: time.Unix(1710000500, 0).UTC(),
		}
	}

	handlers := newTestHandlers(t)
	handlers.claudeSessions = catalog
	handlers.sessions.markRunning("ses_stale") // stale-running registry row
	handlers.RegisterAgent("claudecode", agent)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_sessions",
		RequestID: "r1",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	data, _ := msgs[0]["data"].(map[string]any)
	sessions, _ := data["sessions"].([]any)
	if len(sessions) != 3 {
		t.Fatalf("sessions=%d, want 3", len(sessions))
	}

	// Fix 2: GetRunningSessionIDs invoked exactly once for the whole request,
	// regardless of how many sessions are listed.
	if agent.runningCalls != 1 {
		t.Fatalf("GetRunningSessionIDs called %d times for %d listed sessions; want 1 (hoisted running map)", agent.runningCalls, len(sessions))
	}
	// HARD: zero per-row transcript opens even with a stale-running registry row.
	if *ticks != 0 {
		t.Fatalf("list path opened transcript functions %d time(s); want 0 (incl. stale-running registry entries)", *ticks)
	}
	// Stale-running registry row must NOT be mutated by list (no markIdle).
	if ts, ok := handlers.sessions.get("ses_stale"); !ok || string(ts.state) != string(sessionStateRunning) {
		t.Fatalf("ses_stale registry mutated by list path: state=%v ok=%v (must stay running; list is read-only)", ts.state, ok)
	}

	// Second request: with the Fix-3 TTL cache the second call within the window
	// is a cache hit (runningCalls stays 1); without caching it is 2. Either way
	// it must NEVER be 2×session_count — that is the hoisting invariant.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_sessions",
		RequestID: "r2",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	_ = readJSONMaps(t, clientConn, 1)
	if agent.runningCalls < 1 || agent.runningCalls > 2 {
		t.Fatalf("GetRunningSessionIDs called %d times after 2 requests; want 1 (cached) or 2 (uncached), never 2×%d", agent.runningCalls, len(sessions))
	}
	if *ticks != 0 {
		t.Fatalf("list path opened transcript functions across requests: %d; want 0", *ticks)
	}
}

// TestListSessionsOpenCode_NoTranscript_NoMutation proves the third list call
// site (ocHandleListSessions) also uses list-safe batch enrichment.
func TestListSessionsOpenCode_NoTranscript_NoMutation(t *testing.T) {
	ticks := withTranscriptProbe(t)
	t.Setenv("HOME", t.TempDir())

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"oc_a","title":"A","time":{"created":1000,"updated":1000}}]`))
	}))
	defer proxyServer.Close()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "list_sessions",
		RequestID: "oc-listsafe-1",
		Params:    mustJSONRaw(t, map[string]any{"directory": "/tmp/project"}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if got := msgs[0]["ok"]; got != true {
		t.Fatalf("ok = %#v, want true", got)
	}
	data, _ := msgs[0]["data"].(map[string]any)
	sessions, _ := data["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("opencode sessions=%d, want 1", len(sessions))
	}
	// HARD: opencode list path also opens zero transcripts.
	if *ticks != 0 {
		t.Fatalf("opencode list path opened transcript functions %d time(s); want 0", *ticks)
	}
}

// TestListSessionsClaude_144SessionPerfFixture is the incident-scale regression
// fixture: 144 Claude sessions with large transcript files (>100MB equivalent,
// created sparse so setup is near-instant). The list path must NOT read any of
// them — proven by the transcript probe (zero opens) — and on the catalog-cache-hit
// path the whole request must stay under 200ms.
//
// The <200ms threshold applies ONLY to the cache-hit path, per the plan. The cold
// path (catalog build) intentionally does more work and has no threshold here.
// The "running-map cold cache may open ≤ K transcripts" bound is a property of
// GetRunningSessionIDs (live-PID-bounded) and is covered by phase3's large-K
// guardrail fixture with the isProcessRunning seam, not here.
func TestListSessionsClaude_144SessionPerfFixture(t *testing.T) {
	ticks := withTranscriptProbe(t)

	const (
		sessionCount = 144
		perFileSize  = 700 * 1024 // ~700KB × 144 ≈ 100MB reported total (sparse)
	)

	agent := &fakeAgent{name: "claudecode", reasoningEffort: "high", runningSessionIDs: map[string]bool{}}

	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-claude-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < sessionCount; i++ {
		path := filepath.Join(projectDir, fmt.Sprintf("ses_%03d.jsonl", i))
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		// Sparse truncate: stat reports perFileSize, but no bytes are written.
		if err := f.Truncate(perFileSize); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}

	catalog := newClaudeSessionCatalog(projectsDir)
	catalog.parseSession = func(string, time.Time) claudeSessionScanResult {
		return claudeSessionScanResult{
			Title:     "s",
			CreatedAt: time.Unix(1710000000, 0).UTC(),
			UpdatedAt: time.Unix(1710000500, 0).UTC(),
		}
	}

	handlers := newTestHandlers(t)
	handlers.claudeSessions = catalog
	handlers.sessions.markRunning("ses_000") // stale-running registry row
	handlers.RegisterAgent("claudecode", agent)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// Request 1: catalog cold build (stats + parseSession stub). Timed but no
	// threshold — cold catalog cost is not what list-safe enrichment changes.
	start := time.Now()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_sessions",
		RequestID: "perf-cold",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	coldMS := time.Since(start).Milliseconds()
	data, _ := msgs[0]["data"].(map[string]any)
	sessions, _ := data["sessions"].([]any)
	if len(sessions) != sessionCount {
		t.Fatalf("sessions=%d, want %d", len(sessions), sessionCount)
	}

	// Request 2: catalog cache HIT (fingerprints unchanged). Assert the
	// documented <200ms bound on this double-cache-hit path.
	start = time.Now()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_sessions",
		RequestID: "perf-warm",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	_ = readJSONMaps(t, clientConn, 1)
	warmMS := time.Since(start).Milliseconds()

	// HARD: zero per-row transcript opens across both requests, even with a
	// stale-running registry row and 144 listed sessions.
	if *ticks != 0 {
		t.Fatalf("list path opened transcript functions %d time(s) over 144-session fixture; want 0", *ticks)
	}
	if warmMS >= 200 {
		t.Fatalf("catalog-cache-hit list_sessions took %dms for %d sessions; want <200ms (cold=%dms)", warmMS, sessionCount, coldMS)
	}
	// Stale-running registry row must NOT be mutated by list.
	if ts, ok := handlers.sessions.get("ses_000"); !ok || string(ts.state) != string(sessionStateRunning) {
		t.Fatalf("ses_000 registry mutated by list: state=%v ok=%v (must stay running; list is read-only)", ts.state, ok)
	}
	t.Logf("perf: cold=%dms warm(cache-hit)=%dms sessions=%d transcript_opens=%d", coldMS, warmMS, sessionCount, *ticks)
}

// TestListSessionsClaude_StateChangeInvalidatesRunningMap proves that a MacBridge-
// tracked session-state transition (send_message / turn / abort / process exit —
// all funnel through markRunning/markIdle) drops the cached Claude running map so
// the next list_sessions recomputes immediately instead of serving stale state.
func TestListSessionsClaude_StateChangeInvalidatesRunningMap(t *testing.T) {
	agent := &fakeAgent{name: "claudecode", reasoningEffort: "high", runningSessionIDs: map[string]bool{}}

	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-claude-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "ses_x.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	catalog := newClaudeSessionCatalog(projectsDir)
	catalog.parseSession = func(string, time.Time) claudeSessionScanResult {
		return claudeSessionScanResult{
			Title:     "s",
			CreatedAt: time.Unix(1710000000, 0).UTC(),
			UpdatedAt: time.Unix(1710000500, 0).UTC(),
		}
	}

	handlers := newTestHandlers(t)
	handlers.claudeSessions = catalog
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	listOnce := func() {
		handlers.HandleRPC(serverConn, WireMessage{
			BackendID: "claudecode",
			Method:    "list_sessions",
			RequestID: "x",
			Params:    mustJSONRaw(t, map[string]any{}),
		})
		_ = readJSONMaps(t, clientConn, 1)
	}

	listOnce()
	if agent.runningCalls != 1 {
		t.Fatalf("cold list: runningCalls=%d, want 1", agent.runningCalls)
	}
	// Second list within the TTL window is a cache hit — no recompute.
	listOnce()
	if agent.runningCalls != 1 {
		t.Fatalf("cached list: runningCalls=%d, want 1 (TTL cache should collapse burst)", agent.runningCalls)
	}
	// A tracked state transition invalidates the cache. Use markIdle (turn_completed
	// equivalent) on a registered session to exercise the onStateChange callback.
	handlers.sessions.markIdle("ses_x")
	listOnce()
	if agent.runningCalls != 2 {
		t.Fatalf("post-invalidation list: runningCalls=%d, want 2 (state change must invalidate cache)", agent.runningCalls)
	}
}

// TestGetRunningMap_CacheSurfacesExternalSession proves the cache surfaces
// whatever GetRunningSessionIDs returns through the bridge, including a session
// with NO MacBridge-owned registry entry (an externally-launched Claude turn).
// Combined with agent/claudecode's external-turn fixture (live PID + active
// transcript via the procAlive seam), this covers the full external-turn path.
func TestGetRunningMap_CacheSurfacesExternalSession(t *testing.T) {
	agent := &fakeAgent{
		name:              "claudecode",
		runningSessionIDs: map[string]bool{"external-turn-ses": true}, // no registry entry
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)

	m := handlers.getRunningMap(context.Background(), agent)
	if !m["external-turn-ses"] {
		t.Fatalf("getRunningMap did not surface external session; got %v", m)
	}
	// Burst collapses to one GetRunningSessionIDs call across the TTL window.
	for i := 0; i < 5; i++ {
		_ = handlers.getRunningMap(context.Background(), agent)
	}
	if agent.runningCalls != 1 {
		t.Fatalf("runningCalls=%d after 6 gets; want 1 (cache collapse)", agent.runningCalls)
	}
}
