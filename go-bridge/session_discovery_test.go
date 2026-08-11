package gobridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func discoveryCodexAgent(t *testing.T, base *fakeAgent) *fakeCodexCatalogAgent {
	t.Helper()
	withCodexRootsDisabled(t)
	workspace := t.TempDir()
	return &fakeCodexCatalogAgent{fakeAgent: base, fetchFn: func(_ context.Context, _ string) ([]core.AgentSessionInfo, error) {
		if base.listHook != nil {
			base.listHook()
		}
		if base.sessionListErr != nil {
			return nil, base.sessionListErr
		}
		infos := append([]core.AgentSessionInfo(nil), base.sessionInfos...)
		for index := range infos {
			if infos[index].Directory == "" {
				infos[index].Directory = workspace
			}
		}
		return infos, nil
	}}
}

type discoveryHintState struct {
	mu           sync.Mutex
	expanded     bool
	headCalls    int
	headErrors   int
	fullCalls    int
	headSeeded   chan struct{}
	headSeedOnce sync.Once
	workspace    string
}

type discoveryHintCodexAgent struct {
	*fakeCodexCatalogAgent
	state *discoveryHintState
}

func (a *discoveryHintCodexAgent) FetchThreadListHead(context.Context, string, int) ([]core.AgentSessionInfo, error) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	a.state.headCalls++
	if a.state.headErrors > 0 {
		a.state.headErrors--
		return nil, errors.New("transient native head failure")
	}
	a.state.headSeedOnce.Do(func() { close(a.state.headSeeded) })
	infos := []core.AgentSessionInfo{{ID: "s1", Summary: "one", Directory: a.state.workspace}}
	if a.state.expanded {
		infos = append([]core.AgentSessionInfo{{ID: "s2", Summary: "two", Directory: a.state.workspace}}, infos...)
	}
	return infos, nil
}

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
	agent := discoveryCodexAgent(t, &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}})
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

func TestSessionDiscoveryBlockedBackendDoesNotStarveCodex(t *testing.T) {
	previousInterval := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	withCodexRootsDisabled(t)

	handlers := newTestHandlers(t)
	blockedStarted := make(chan struct{})
	releaseBlocked := make(chan struct{})
	var blockOnce sync.Once
	handlers.RegisterAgent("grokbuild", &fakeGrokCatalogAgent{
		fakeAgent: &fakeAgent{name: "grokbuild"},
		fetchFn: func(context.Context) ([]core.AgentSessionInfo, error) {
			blockOnce.Do(func() { close(blockedStarted) })
			<-releaseBlocked
			return nil, errors.New("released blocked provider")
		},
	})

	workspace := t.TempDir()
	var stateMu sync.Mutex
	expanded := false
	codexSeeded := make(chan struct{})
	var seedOnce sync.Once
	codex := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex"},
		fetchFn: func(context.Context, string) ([]core.AgentSessionInfo, error) {
			stateMu.Lock()
			defer stateMu.Unlock()
			seedOnce.Do(func() { close(codexSeeded) })
			infos := []core.AgentSessionInfo{{ID: "s1", Summary: "one", Directory: workspace}}
			if expanded {
				infos = append(infos, core.AgentSessionInfo{ID: "s2", Summary: "two", Directory: workspace})
			}
			return infos, nil
		},
	}
	handlers.RegisterAgent("codex", codex)

	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})
	ctx, cancel := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		handlers.runSessionDiscovery(ctx)
	}()
	t.Cleanup(func() {
		close(releaseBlocked)
		cancel()
		select {
		case <-watcherDone:
		case <-time.After(time.Second):
			t.Error("session discovery watcher did not stop")
		}
		sessionDiscoveryInterval = previousInterval
	})

	select {
	case <-blockedStarted:
	case <-time.After(time.Second):
		t.Fatal("blocking backend worker did not start")
	}
	select {
	case <-codexSeeded:
	case <-time.After(time.Second):
		t.Fatal("Codex seed was starved by blocking backend")
	}
	stateMu.Lock()
	expanded = true
	stateMu.Unlock()

	msg := readJSONMaps(t, clientConn, 1)[0]
	if msg["event"] != "sessions_changed" || msg["backendId"] != "codex" {
		t.Fatalf("Codex refresh event=%#v", msg)
	}
}

func TestSessionDiscoveryCodexHeadHintTriggersAuthoritativeRefresh(t *testing.T) {
	previousInterval := sessionDiscoveryInterval
	previousHintInterval := codexDiscoveryHintInterval
	sessionDiscoveryInterval = time.Hour
	codexDiscoveryHintInterval = 10 * time.Millisecond
	withCodexRootsDisabled(t)

	state := &discoveryHintState{
		headErrors: 2,
		headSeeded: make(chan struct{}),
		workspace:  t.TempDir(),
	}
	base := &fakeCodexCatalogAgent{fakeAgent: &fakeAgent{name: "codex"}}
	base.fetchFn = func(context.Context, string) ([]core.AgentSessionInfo, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.fullCalls++
		infos := []core.AgentSessionInfo{{ID: "s1", Summary: "one", Directory: state.workspace}}
		if state.expanded {
			infos = append([]core.AgentSessionInfo{{ID: "s2", Summary: "two", Directory: state.workspace}}, infos...)
		}
		return infos, nil
	}
	agent := &discoveryHintCodexAgent{fakeCodexCatalogAgent: base, state: state}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})

	ctx, cancel := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		handlers.runSessionDiscovery(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-watcherDone:
		case <-time.After(time.Second):
			t.Error("session discovery watcher did not stop")
		}
		sessionDiscoveryInterval = previousInterval
		codexDiscoveryHintInterval = previousHintInterval
	})

	select {
	case <-state.headSeeded:
	case <-time.After(time.Second):
		t.Fatal("Codex native head probe did not recover and seed")
	}
	// Head errors and a stable head must not start another full native walk.
	time.Sleep(30 * time.Millisecond)
	state.mu.Lock()
	fullBeforeChange := state.fullCalls
	state.expanded = true
	state.mu.Unlock()
	if fullBeforeChange != 1 {
		t.Fatalf("stable/error head probes caused %d full fetches, want seed only", fullBeforeChange)
	}

	msg := readJSONMaps(t, clientConn, 1)[0]
	if msg["event"] != "sessions_changed" || msg["backendId"] != "codex" {
		t.Fatalf("Codex hint-triggered refresh event=%#v", msg)
	}
	state.mu.Lock()
	fullAfterChange := state.fullCalls
	state.mu.Unlock()
	if fullAfterChange != 2 {
		t.Fatalf("head change full fetches=%d, want seed + one authoritative refresh", fullAfterChange)
	}
}

func TestSessionDiscoveryControlPlanePublisherCapabilityMatrix(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := discoveryCodexAgent(t, &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}})
	handlers.RegisterAgent("codex", agent)

	type capabilityCase struct {
		name          string
		sessionSyncV2 bool
		catalogV2     bool
		conn          *publisherCaptureConn
	}
	cases := []capabilityCase{
		{name: "legacy-undeclared", conn: newPublisherCaptureConn(nil)},
		{name: "legacy-catalog-v2", catalogV2: true, conn: newPublisherCaptureConn(nil)},
		{name: "sync-v2-undeclared", sessionSyncV2: true, conn: newPublisherCaptureConn(nil)},
		{name: "sync-v2-catalog-v2", sessionSyncV2: true, catalogV2: true, conn: newPublisherCaptureConn(nil)},
	}
	for _, item := range cases {
		handlers.broadcaster.RegisterConn(item.conn)
		handlers.eventPublisher.RegisterConnection(item.conn)
		handlers.eventPublisher.SetConnSyncV2(item.conn, item.sessionSyncV2)
		handlers.eventPublisher.SetConnCatalogCursorEpochV2(item.conn, item.catalogV2)
	}

	seen := map[string]string{}
	handlers.snapshotSessions(context.Background(), seen, true)
	agent.sessionInfos = []core.AgentSessionInfo{{ID: "s1"}, {ID: "s2"}}
	handlers.snapshotSessions(context.Background(), seen, false)

	for _, item := range cases {
		item.conn.waitCount(t, 1)
		frames := item.conn.snapshot()
		if len(frames) != 1 {
			t.Fatalf("%s received %d frames, want exactly one", item.name, len(frames))
		}
		msg, ok := frames[0].(EventMessage)
		if !ok || msg.Event != "sessions_changed" || msg.BackendID != "codex" || msg.SessionID != "" ||
			msg.Seq != 1 || msg.EventID != msg.BridgeEpoch+":1" || !msg.Replayable {
			t.Fatalf("%s frame=%#v", item.name, frames[0])
		}
		item.conn.mu.Lock()
		classes := append([]relayOutboundClass(nil), item.conn.classes...)
		item.conn.mu.Unlock()
		if len(classes) != 1 || classes[0] != classifyRelayEvent("sessions_changed") {
			t.Fatalf("%s classes=%v", item.name, classes)
		}
	}

	replay := handlers.eventPublisher.EventBuffer().Replay("codex", "", BridgeSessionCut{})
	if replay.Disposition != ReplayAvailable || len(replay.Events) != 1 || replay.Events[0].Event != "sessions_changed" {
		t.Fatalf("control-plane replay=%+v", replay)
	}
	if _, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("codex", ""); ok {
		t.Fatal("sessions_changed entered the legacy projection reducer")
	}
	if _, ok := handlers.projectionKernel.Snapshot("codex", ""); ok {
		t.Fatal("sessions_changed entered the Projection Kernel")
	}
}

func TestSessionDiscoveryFencesCatalogBeforeBroadcastAndForcesRebuild(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := discoveryCodexAgent(t, &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}})
	handlers.RegisterAgent("codex", agent)
	cache := handlers.codexCatalogWireCache()
	scope := codexCatalogScopeKey("codex", "")
	oldMaps := []map[string]interface{}{
		{"id": "old-a", "updatedAtMillis": int64(2)},
		{"id": "old-b", "updatedAtMillis": int64(1)},
	}
	calls := 0
	page0, stale, err := cache.pageV2(scope, "", 1, builderFromMaps(oldMaps, &calls))
	if err != nil || stale != nil {
		t.Fatalf("warm page0 err=%v stale=%v", err, stale)
	}
	oldCursor := page0["nextCursor"].(string)

	conn := newPublisherCaptureConn(nil)
	handlers.broadcaster.RegisterConn(conn)
	handlers.eventPublisher.RegisterConnection(conn)
	seen := map[string]string{}
	handlers.snapshotSessions(context.Background(), seen, true)
	agent.sessionInfos = []core.AgentSessionInfo{{ID: "s1"}, {ID: "s2"}}
	handlers.snapshotSessions(context.Background(), seen, false)
	conn.waitCount(t, 1)

	if cache.Peek(scope) != nil {
		t.Fatal("sessions_changed was observable before the old snapshot was fenced")
	}
	if _, stale, err := cache.pageV2(scope, oldCursor, 1, builderFromMaps(nil, &calls)); err != nil || stale == nil || stale.Code != "cursor_stale" {
		t.Fatalf("old cursor after notification stale=%+v err=%v", stale, err)
	}
	newMaps := []map[string]interface{}{{"id": "new-member", "updatedAtMillis": int64(3)}}
	rebuilt, stale, err := cache.pageV2(scope, "", 10, builderFromMaps(newMaps, &calls))
	if err != nil || stale != nil {
		t.Fatalf("rebuild err=%v stale=%v", err, stale)
	}
	got := rebuilt["sessions"].([]map[string]interface{})
	if len(got) != 1 || got[0]["id"] != "new-member" {
		t.Fatalf("post-notification page0 reused old snapshot: %#v", got)
	}
}

func TestSessionDiscoveryErrorSkipsFenceButSuccessEmptyFences(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := discoveryCodexAgent(t, &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}})
	handlers.RegisterAgent("codex", agent)
	cache := handlers.codexCatalogWireCache()
	scope := codexCatalogScopeKey("codex", "")
	calls := 0
	if _, err := cache.FetchOrReuse(scope, builderFromMaps(synthWireMaps(2), &calls)); err != nil {
		t.Fatal(err)
	}
	conn := newPublisherCaptureConn(nil)
	handlers.broadcaster.RegisterConn(conn)
	handlers.eventPublisher.RegisterConnection(conn)
	seen := map[string]string{}
	handlers.snapshotSessions(context.Background(), seen, true)

	agent.sessionListErr = errors.New("native unavailable")
	handlers.snapshotSessions(context.Background(), seen, false)
	if cache.Peek(scope) == nil || len(conn.snapshot()) != 0 {
		t.Fatal("error poll fenced cache, updated seen, or broadcast")
	}

	agent.sessionListErr = nil
	agent.sessionInfos = nil
	handlers.snapshotSessions(context.Background(), seen, false)
	conn.waitCount(t, 1)
	if cache.Peek(scope) != nil {
		t.Fatal("successful empty catalog did not fence prior snapshot")
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
	agent := discoveryCodexAgent(t, &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1", ModifiedAt: t0}}})
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
	agent := discoveryCodexAgent(t, &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}})
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
	withCodexRootsDisabled(t)

	handlers := newTestHandlers(t)
	// The seed poll completes normally (records seen=s1); the FIRST regular poll
	// panics, then all subsequent polls succeed. The watcher must recover and
	// still broadcast sessions_changed once s2 appears — proving it did not die.
	workspace := t.TempDir()
	callCount := 0
	expanded := false
	var mu sync.Mutex
	agent := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex"},
		fetchFn: func(context.Context, string) ([]core.AgentSessionInfo, error) {
			mu.Lock()
			defer mu.Unlock()
			callCount++
			if callCount == 2 { // first non-seed poll panics once
				panic("simulated transcript parse failure")
			}
			infos := []core.AgentSessionInfo{{ID: "s1", Directory: workspace}}
			if expanded {
				infos = append(infos, core.AgentSessionInfo{ID: "s2", Directory: workspace})
			}
			return infos, nil
		},
	}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})

	ctx, cancel := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		handlers.runSessionDiscovery(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-watcherDone:
		case <-time.After(time.Second):
			t.Error("session discovery watcher did not stop")
		}
		sessionDiscoveryInterval = prev
	})

	// After the seed (s1) and the panicking poll, add s2. The recovered watcher
	// must still detect the growth on a later poll and broadcast sessions_changed.
	time.Sleep(60 * time.Millisecond) // let seed + panic poll happen
	mu.Lock()
	expanded = true
	mu.Unlock()

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
	elsewhereWorkspace := catalogFixtureWorkspace(t, projectsDir, "someuser-elsewhere")
	writeClaudeCatalogFixture(t, filepath.Join(projectDir, "claude-abc.jsonl"),
		elsewhereWorkspace, "elsewhere session", "2026-07-30T10:00:00Z")
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
	app2Workspace := catalogFixtureWorkspace(t, projectsDir, "someuser-app2")
	writeClaudeCatalogFixture(t, filepath.Join(otherProject, "claude-def.jsonl"),
		app2Workspace, "app2 session", "2026-07-30T10:05:00Z")

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
	workspace := catalogFixtureWorkspace(t, projectsDir, "someuser-work")
	writeClaudeCatalogFixture(t, filepath.Join(projectDir, "claude-xyz.jsonl"),
		workspace, "to be archived", "2026-07-30T11:00:00Z")
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
