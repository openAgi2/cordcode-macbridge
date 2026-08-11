package gobridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestSessionDiscoveryBroadcastsOnNewSession: watcher detects a new session ID
// across snapshots and broadcasts "sessions_changed" to a subscribed client.
//
// Phase 7 §442：change detection 现由 catalog fingerprint 驱动（seen 为 backend→fingerprint）。
// 新增 session 改写 fingerprint → 触发 sessions_changed。
func TestSessionDiscoveryBroadcastsOnNewSession(t *testing.T) {
	prev := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	t.Cleanup(func() { sessionDiscoveryInterval = prev })

	handlers := newTestHandlers(t)
	agent := &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})

	seen := map[string]string{}
	// Seed (s1) — no broadcast.
	handlers.snapshotSessions(context.Background(), seen, true)
	// New session s2 appears → fingerprint changes.
	agent.sessionInfos = []core.AgentSessionInfo{{ID: "s1"}, {ID: "s2"}}
	handlers.snapshotSessions(context.Background(), seen, false)

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := clientConn.ReadJSON(&payload); err != nil {
		t.Fatalf("read sessions_changed: %v", err)
	}
	if got := payload["event"]; got != "sessions_changed" {
		t.Fatalf("event = %#v, want sessions_changed", got)
	}
	data, _ := payload["data"].(map[string]any)
	if data["backendId"] != "codex" {
		t.Fatalf("data = %#v, want backendId=codex", data)
	}
}

// TestSessionDiscoveryFiresOnUpdatedAtOnlyChange：Phase 7 §442 关键新能力——fingerprint 覆盖
// id|updatedAtMillis，故「既有 session 收到新 turn → updatedAt 变，但 ID 集合不变」也触发
// sessions_changed。旧 ID-set diff 会漏掉这种情况（列表 recency 不刷新）。本测试钉死：相同 ID
// 集合 {s1}，仅 s1 的 ModifiedAt 前进 → 必须 broadcast。
func TestSessionDiscoveryFiresOnUpdatedAtOnlyChange(t *testing.T) {
	prev := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	t.Cleanup(func() { sessionDiscoveryInterval = prev })

	handlers := newTestHandlers(t)
	t0 := time.Unix(1_700_000_000, 0).UTC()
	agent := &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1", ModifiedAt: t0}}}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})

	seen := map[string]string{}
	handlers.snapshotSessions(context.Background(), seen, true)
	// Same ID set {s1}, only ModifiedAt advances. ID-set diff would see no change;
	// fingerprint (id|updatedAtMillis) must change → sessions_changed.
	agent.sessionInfos = []core.AgentSessionInfo{{ID: "s1", ModifiedAt: t0.Add(1 * time.Hour)}}
	handlers.snapshotSessions(context.Background(), seen, false)

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := clientConn.ReadJSON(&payload); err != nil {
		t.Fatalf("expected sessions_changed on updatedAt-only change (§442 fingerprint): %v", err)
	}
	if got := payload["event"]; got != "sessions_changed" {
		t.Fatalf("event = %#v, want sessions_changed (updatedAt change must fire via fingerprint)", got)
	}
}

// TestSessionDiscoveryDoesNotBroadcastOnNoChange：fingerprint 未变 → 不广播（无新/删除/更新）。
func TestSessionDiscoveryDoesNotBroadcastOnNoChange(t *testing.T) {
	prev := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	t.Cleanup(func() { sessionDiscoveryInterval = prev })

	handlers := newTestHandlers(t)
	agent := &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})

	seen := map[string]string{}
	handlers.snapshotSessions(context.Background(), seen, true)
	// Same sessions, same ModifiedAt → identical fingerprint → no broadcast.
	handlers.snapshotSessions(context.Background(), seen, false)

	if err := clientConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := clientConn.ReadJSON(&payload); err == nil {
		t.Fatalf("unexpected sessions_changed on no-change snapshot (fingerprint stable): %#v", payload)
	}
}

// TestSessionDiscoverySurvivesPanicAndStillBroadcasts: if ListSessions (or the
// snapshot walk) panics, the watcher goroutine must recover and keep emitting
// sessions_changed on later polls. This pins the production root cause: the
// watcher had no recover(), so a single panic in the 209MB Claude transcript
// walk would silently kill the goroutine — yielding ZERO sessions_changed across
// all logs forever, with no error line to reveal it.
func TestSessionDiscoverySurvivesPanicAndStillBroadcasts(t *testing.T) {
	prev := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	t.Cleanup(func() { sessionDiscoveryInterval = prev })

	handlers := newTestHandlers(t)
	// listHook: seed poll completes normally (records seen=s1); the FIRST regular
	// poll panics, then all subsequent polls succeed. The watcher must recover and
	// still broadcast sessions_changed once s2 appears — proving it did not die.
	callCount := 0
	var mu sync.Mutex
	agent := &fakeAgent{
		name:         "codex",
		sessionInfos: []core.AgentSessionInfo{{ID: "s1"}},
		listHook: func() {
			mu.Lock()
			callCount++
			n := callCount
			mu.Unlock()
			if n == 2 { // first non-seed poll panics once
				panic("simulated transcript parse failure")
			}
		},
	}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	handlers.StartSessionDiscoveryWatcher(ctx)

	// After the seed (s1) and the panicking poll, add s2. The recovered watcher
	// must still detect the growth on a later poll and broadcast sessions_changed.
	time.Sleep(60 * time.Millisecond) // let seed + panic poll happen
	agent.sessionInfos = []core.AgentSessionInfo{{ID: "s1"}, {ID: "s2"}}

	if err := clientConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := clientConn.ReadJSON(&payload); err != nil {
		t.Fatalf("expected sessions_changed after watcher recovered from panic: %v", err)
	}
	if got := payload["event"]; got != "sessions_changed" {
		t.Fatalf("event = %#v, want sessions_changed (watcher must survive panic)", got)
	}
	data, _ := payload["data"].(map[string]any)
	if data["backendId"] != "codex" {
		t.Fatalf("data = %#v, want backendId=codex", data)
	}
}

// TestSessionDiscoveryClaudeUsesGlobalCatalog: the production root cause. Claude's
// agent.ListSessions() resolves only the agent workDir's project and returned 0
// sessions in production (the encoded workDir key has no ~/.claude/projects dir),
// so new Claude sessions — even under other project dirs — were never detected and
// sessions_changed never fired. The watcher must enumerate Claude via the SAME
// authoritative global catalog the list_sessions RPC serves (h.claudeSessions),
// not the single-project agent.ListSessions(). This test pins that: a Claude agent
// whose ListSessions() returns nothing still yields the catalog's session IDs.
func TestSessionDiscoveryClaudeUsesGlobalCatalog(t *testing.T) {
	prev := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	t.Cleanup(func() { sessionDiscoveryInterval = prev })

	handlers := newTestHandlers(t)

	// A global catalog with one real Claude session under a project dir that is
	// NOT the agent workDir (mirrors production: sessions live under their own
	// encoded project key, not under the workDir key).
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-Users-someuser-elsewhere")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeCatalogFixture(t, filepath.Join(projectDir, "claude-abc.jsonl"),
		"/Users/someuser/elsewhere", "elsewhere session", "2026-07-30T10:00:00Z")
	catalog := newClaudeSessionCatalog(projectsDir)
	handlers.claudeSessions = catalog

	// Claude agent whose ListSessions returns nothing — exactly the production
	// failure (workDir key has no project dir → 0 sessions).
	claudeAgent := &fakeAgent{name: "claudecode", sessionInfos: nil}
	handlers.RegisterAgent("claude", claudeAgent)

	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: "list-view"})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	handlers.StartSessionDiscoveryWatcher(ctx)

	// Let the watcher seed on the single catalog session (claude-abc). The seed
	// must come from the catalog, NOT agent.ListSessions (which is nil here).
	time.Sleep(80 * time.Millisecond)

	// A second session appears under a different project dir → must trigger
	// sessions_changed even though agent.ListSessions still returns nil.
	otherProject := filepath.Join(projectsDir, "-Users-someuser-app2")
	if err := os.Mkdir(otherProject, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeCatalogFixture(t, filepath.Join(otherProject, "claude-def.jsonl"),
		"/Users/someuser/app2", "app2 session", "2026-07-30T10:05:00Z")

	if err := clientConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := clientConn.ReadJSON(&payload); err != nil {
		t.Fatalf("expected sessions_changed for new Claude session under a non-workDir project: %v", err)
	}
	if got := payload["event"]; got != "sessions_changed" {
		t.Fatalf("event = %#v, want sessions_changed", got)
	}
	data, _ := payload["data"].(map[string]any)
	if data["backendId"] != "claude" {
		t.Fatalf("data = %#v, want backendId=claude", data)
	}
}

// TestSessionDiscoveryFiresOnArchive: archiving a session (which only writes
// the sidecar and hides the session from the client's active list) must trigger
// sessions_changed so the web list refreshes and drops it. This is the Issue 4
// regression: the old diff only detected additions, so removals/archives never
// signaled the client to refresh. Also pins that the catalog surfaces
// archivedAtMillis (so the web archived filter can act on it).
func TestSessionDiscoveryFiresOnArchive(t *testing.T) {
	prev := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	t.Cleanup(func() { sessionDiscoveryInterval = prev })

	handlers := newTestHandlers(t)
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-Users-someuser-work")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeCatalogFixture(t, filepath.Join(projectDir, "claude-xyz.jsonl"),
		"/Users/someuser/work", "to be archived", "2026-07-30T11:00:00Z")
	handlers.claudeSessions = newClaudeSessionCatalog(projectsDir)
	handlers.RegisterAgent("claude", &fakeAgent{name: "claudecode", sessionInfos: nil})

	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claude", SessionID: "list-view"})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	handlers.StartSessionDiscoveryWatcher(ctx)

	// First, prove the catalog surfaces archivedAtMillis once archived. Directly
	// write the sidecar the way ArchiveSession does (only the sidecar changes;
	// the .jsonl is untouched). The fingerprint must include sidecar mtime so the
	// cached entry is invalidated.
	time.Sleep(80 * time.Millisecond) // let the watcher seed on the live session
	sidecarDir := filepath.Join(projectDir, ".cc-connect-session-meta")
	if err := os.MkdirAll(sidecarDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archivedMs := time.Now().UnixMilli()
	sidecarJSON := fmt.Sprintf(`{"archivedAtMillis":%d}`, archivedMs)
	if err := os.WriteFile(filepath.Join(sidecarDir, "claude-xyz.json"), []byte(sidecarJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// The archive must remove the session from the poller's visible set (archived
	// excluded) → fingerprint changes → sessions_changed reaches the client.
	// (Phase 7 §442: fingerprint-over-visible; archive shrinks visible → fingerprint 变。)
	if err := clientConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := clientConn.ReadJSON(&payload); err != nil {
		t.Fatalf("expected sessions_changed after archiving a Claude session: %v", err)
	}
	if got := payload["event"]; got != "sessions_changed" {
		t.Fatalf("event = %#v, want sessions_changed (archive must signal)", got)
	}
}

// TestClaudeCatalogSurfacesArchivedAtMillis pins that the global catalog outputs
// archivedAtMillis (read from the sidecar) so the web session-grouping filter can
// hide archived sessions. Without it, archived Claude sessions never disappear
// from the web list even after sessions_changed forces a refresh.
func TestClaudeCatalogSurfacesArchivedAtMillis(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-Users-someuser-work")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := catalogFixtureWorkspace(t, projectsDir, "someuser-work")
	writeClaudeCatalogFixture(t, filepath.Join(projectDir, "claude-xyz.jsonl"),
		ws, "work session", "2026-07-30T11:00:00Z")

	catalog := newClaudeSessionCatalog(projectsDir)

	// Before archiving: no archivedAtMillis.
	before := catalog.list("", nil)
	if len(before) != 1 || before[0]["archivedAtMillis"] != nil {
		t.Fatalf("before archive: expected 1 session without archivedAtMillis, got %#v", before)
	}

	// Archive via sidecar (mirrors ArchiveSession which only writes the sidecar).
	sidecarDir := filepath.Join(projectDir, ".cc-connect-session-meta")
	if err := os.MkdirAll(sidecarDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archivedMs := time.Now().UnixMilli()
	if err := os.WriteFile(filepath.Join(sidecarDir, "claude-xyz.json"),
		[]byte(fmt.Sprintf(`{"archivedAtMillis":%d}`, archivedMs)), 0o600); err != nil {
		t.Fatal(err)
	}

	after := catalog.list("", nil)
	if len(after) != 1 {
		t.Fatalf("after archive: expected 1 session, got %d", len(after))
	}
	got, ok := after[0]["archivedAtMillis"].(int64)
	if !ok || got != archivedMs {
		t.Fatalf("after archive: archivedAtMillis = %#v, want %d (catalog must surface sidecar archived time)", after[0]["archivedAtMillis"], archivedMs)
	}
}
