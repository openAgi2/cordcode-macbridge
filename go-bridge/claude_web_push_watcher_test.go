package gobridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeWebPushWatcherNotifiesForNeverOpenedSessionWithoutProjection(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-unopened")
	projectDir := filepath.Join(projectsDir, "-tmp-push-unopened")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "never-opened-claude"
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	baseline := `{"uuid":"u-old","type":"user","timestamp":"2026-09-12T00:00:00Z","cwd":"` + workspace + `","message":{"role":"user","content":[{"type":"text","text":"old"}]}}` + "\n" +
		`{"uuid":"a-old","parentUuid":"u-old","type":"assistant","timestamp":"2026-09-12T00:00:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t)
	if _, err := store.Register("dev_unopened", testSubscriptionRecord("https://push.example.com/unopened")); err != nil {
		t.Fatal(err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_unopened")
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState)}
	watcher.sweep() // baseline: historical completion must not notify
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("baseline produced %d historical candidates", len(got))
	}

	live := `{"uuid":"u-live","parentUuid":"a-old","type":"user","timestamp":"2026-09-12T00:01:00Z","cwd":"` + workspace + `","message":{"role":"user","content":[{"type":"text","text":"new"}]}}` + "\n" +
		`{"uuid":"a-live","parentUuid":"u-live","type":"assistant","timestamp":"2026-09-12T00:01:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"fresh answer"}],"stop_reason":"end_turn"}}` + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(live); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	watcher.sweep()
	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("live unopened-session candidates = %d, want 1", len(got))
	}
	if got[0].BackendID != "claude" || got[0].SessionID != sessionID || got[0].AnchorID != "u-live" {
		t.Fatalf("candidate identity = %+v", got[0])
	}
	if got[0].ContentPreview != "fresh answer" {
		t.Fatalf("preview = %q, want fresh answer", got[0].ContentPreview)
	}
	if h.projectionKernel.HasReducerState("claude", sessionID) {
		t.Fatal("notification-only watcher created hidden projection state")
	}
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("unchanged transcript produced %d duplicate candidates", len(got))
	}
}

func TestClaudeWebPushWatcherDoesNotBackfillWhenEnrollmentStarts(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-enroll")
	projectDir := filepath.Join(projectsDir, "-tmp-push-enroll")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(projectDir, "enroll-session.jsonl")
	row := `{"uuid":"u-before","type":"user","timestamp":"2026-09-12T00:00:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"before"}}` + "\n" +
		`{"uuid":"a-before","parentUuid":"u-before","type":"assistant","timestamp":"2026-09-12T00:00:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"before reply"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t)
	pipeline := NewWebPushCandidatePipeline(store)
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState)}
	watcher.sweep()
	if _, err := store.Register("dev_late", testSubscriptionRecord("https://push.example.com/late")); err != nil {
		t.Fatal(err)
	}
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("enrollment backfilled %d historical candidates", len(got))
	}
}

func TestClaudeWebPushWatcherDoesNotBackfillFirstVisibleHistoricalSession(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-first-visible")
	projectDir := filepath.Join(projectsDir, "-tmp-push-first-visible")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(projectDir, "first-visible-session.jsonl")
	rows := ""
	for i, reply := range []string{"old one", "old two", "old three"} {
		rows += `{"uuid":"u-first-` + string(rune('a'+i)) + `","type":"user","timestamp":"2026-07-21T11:4` + string(rune('7'+i)) + `:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"old prompt"}}` + "\n"
		rows += `{"uuid":"a-first-` + string(rune('a'+i)) + `","parentUuid":"u-first-` + string(rune('a'+i)) + `","type":"assistant","timestamp":"2026-07-21T11:4` + string(rune('7'+i)) + `:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"` + reply + `"}],"stop_reason":"end_turn"}}` + "\n"
	}
	if err := os.WriteFile(transcript, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t)
	if _, err := store.Register("dev_first_visible", testSubscriptionRecord("https://push.example.com/first-visible")); err != nil {
		t.Fatal(err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState)}
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("first-visible historical session backfilled %d candidates", len(got))
	}
}

func appendTranscriptFixture(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeWebPushWatcherDedupsTextlessThenTextTerminal(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-terminal-dedup")
	projectDir := filepath.Join(projectsDir, "-tmp-push-terminal-dedup")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "terminal-dedup-session"
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	baseline := `{"uuid":"u-old","type":"user","timestamp":"2026-09-12T00:00:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"old"}}` + "\n" +
		`{"uuid":"a-old","parentUuid":"u-old","type":"assistant","timestamp":"2026-09-12T00:00:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t)
	if _, err := store.Register("dev_dedup", testSubscriptionRecord("https://push.example.com/dedup")); err != nil {
		t.Fatal(err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_terminal_dedup")
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState)}
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("baseline produced %d candidates", len(got))
	}

	firstTerminal := `{"uuid":"u-live","parentUuid":"a-old","type":"user","timestamp":"2026-09-12T00:01:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"new"}}` + "\n" +
		`{"uuid":"a-live-empty","parentUuid":"u-live","type":"assistant","timestamp":"2026-09-12T00:01:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[],"stop_reason":"end_turn"}}` + "\n"
	appendTranscriptFixture(t, transcript, firstTerminal)
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("textless terminal flushed early: %+v", got)
	}

	secondTerminal := `{"uuid":"a-live-text","parentUuid":"a-live-empty","type":"assistant","timestamp":"2026-09-12T00:01:04Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"fresh answer"}],"stop_reason":"end_turn"}}` + "\n"
	appendTranscriptFixture(t, transcript, secondTerminal)
	watcher.sweep()
	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want one deduplicated candidate", len(got))
	}
	if got[0].SessionID != sessionID || got[0].AnchorID != "u-live" || got[0].ContentPreview != "fresh answer" {
		t.Fatalf("candidate = %+v", got[0])
	}
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("unchanged transcript produced %d duplicate candidates", len(got))
	}
}

func TestClaudeWebPushWatcherFirstVisibleRetainsLiveCompletion(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-first-live")
	projectDir := filepath.Join(projectsDir, "-tmp-push-first-live")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "first-live-session"
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	oldRows := `{"uuid":"u-old","type":"user","timestamp":"2026-07-21T11:47:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"old"}}` + "\n" +
		`{"uuid":"a-old","parentUuid":"u-old","type":"assistant","timestamp":"2026-07-21T11:47:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(oldRows), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t)
	if _, err := store.Register("dev_first_live", testSubscriptionRecord("https://push.example.com/first-live")); err != nil {
		t.Fatal(err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState)}
	watcher.startedAt = time.Now().UTC().Add(-time.Second)
	liveAt := time.Now().UTC().Format(time.RFC3339Nano)
	liveRows := `{"uuid":"u-live","parentUuid":"a-old","type":"user","timestamp":"` + liveAt + `","cwd":"` + workspace + `","message":{"role":"user","content":"new"}}` + "\n" +
		`{"uuid":"a-live","parentUuid":"u-live","type":"assistant","timestamp":"` + liveAt + `","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"live reply"}],"stop_reason":"end_turn"}}` + "\n"
	appendTranscriptFixture(t, transcript, liveRows)
	watcher.sweep()
	watcher.sweep()
	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want only the post-start live turn", len(got))
	}
	if got[0].SessionID != sessionID || got[0].AnchorID != "u-live" || got[0].ContentPreview != "live reply" {
		t.Fatalf("candidate = %+v", got[0])
	}
}

func TestClaudeWebPushWatcherTextlessTerminalUsesFallbackAfterHold(t *testing.T) {
	prevHold := claudeWebPushTerminalHold
	claudeWebPushTerminalHold = time.Millisecond
	t.Cleanup(func() { claudeWebPushTerminalHold = prevHold })
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-terminal-fallback")
	projectDir := filepath.Join(projectsDir, "-tmp-push-terminal-fallback")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(projectDir, "fallback-session.jsonl")
	baseline := `{"uuid":"u-old","type":"user","timestamp":"2026-09-12T00:00:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"old"}}` + "\n" +
		`{"uuid":"a-old","parentUuid":"u-old","type":"assistant","timestamp":"2026-09-12T00:00:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t)
	if _, err := store.Register("dev_fallback", testSubscriptionRecord("https://push.example.com/fallback")); err != nil {
		t.Fatal(err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState)}
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("baseline produced %d candidates", len(got))
	}
	live := `{"uuid":"u-live","parentUuid":"a-old","type":"user","timestamp":"2026-09-12T00:01:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"new"}}` + "\n" +
		`{"uuid":"a-live","parentUuid":"u-live","type":"assistant","timestamp":"2026-09-12T00:01:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[],"stop_reason":"end_turn"}}` + "\n"
	appendTranscriptFixture(t, transcript, live)
	watcher.sweep()
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("textless terminal flushed before hold: %+v", got)
	}
	time.Sleep(2 * time.Millisecond)
	watcher.sweep()
	got := pipeline.Drain()
	if len(got) != 1 || got[0].ContentPreview != "" {
		t.Fatalf("fallback candidate = %+v, want one with empty real preview", got)
	}
}

func TestClaudeWebPushWatcherSkipsCatalogRefreshWithoutSubscriptions(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-nosub")
	projectDir := filepath.Join(projectsDir, "-tmp-push-nosub")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "nosub-claude"
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	baseline := `{"uuid":"u-old","type":"user","timestamp":"2026-09-12T00:00:00Z","cwd":"` + workspace + `","message":{"role":"user","content":[{"type":"text","text":"old"}]}}` + "\n" +
		`{"uuid":"a-old","parentUuid":"u-old","type":"assistant","timestamp":"2026-09-12T00:00:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t) // 不 Register：0 订阅
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_nosub")
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	// 生产构造形状：StartClaudeWebPushWatcher 在构造时设置 startedAt（评审 B3：
	// 零值构造会绕开"进程启动时无订阅、稍后首次订阅"的真实路径）。
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState), startedAt: time.Now().UTC().Add(-time.Hour)}

	watcher.sweep()
	if h.claudeSessions.snapshot != nil {
		t.Fatal("sweep without subscriptions refreshed the catalog (must skip entirely)")
	}
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("no-subscription sweep produced %d candidates", len(got))
	}

	// 订阅出现后：首轮 sweep 恢复 refresh，且不回放订阅前的历史完成。
	if _, err := store.Register("dev_nosub", testSubscriptionRecord("https://push.example.com/nosub")); err != nil {
		t.Fatal(err)
	}
	watcher.sweep()
	if h.claudeSessions.snapshot == nil {
		t.Fatal("sweep with a subscription did not refresh the catalog")
	}
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("enrollment sweep replayed %d historical candidates", len(got))
	}

	// 订阅后的新完成仍要通知（门控不得吞正常路径）。
	live := `{"uuid":"u-live2","parentUuid":"a-old","type":"user","timestamp":"2026-09-12T00:02:00Z","cwd":"` + workspace + `","message":{"role":"user","content":[{"type":"text","text":"new"}]}}` + "\n" +
		`{"uuid":"a-live2","parentUuid":"u-live2","type":"assistant","timestamp":"2026-09-12T00:02:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"fresh answer"}],"stop_reason":"end_turn"}}` + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(live); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	watcher.sweep()
	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("post-enrollment live candidates = %d, want 1", len(got))
	}
	if got[0].SessionID != sessionID || got[0].ContentPreview != "fresh answer" {
		t.Fatalf("candidate = %+v, want session %s with fresh answer", got[0], sessionID)
	}
}

// 评审 B3 回归：生产构造形状（startedAt 在构造时设置）+ 进程启动时无订阅 +
// 稍后首次订阅。enrollment 前已完成的历史不得回放为通知；enrollment 时进行中、
// 之后完成的 turn 会通知（由 claudeFirstVisibleLiveCut 的 enrollment cut 规则固定）。
func TestClaudeWebPushWatcherEnrollmentAfterDisabledPeriodDoesNotReplayHistory(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	projectsDir := t.TempDir()
	workspace := catalogFixtureWorkspace(t, projectsDir, "push-enroll-late")
	projectDir := filepath.Join(projectsDir, "-tmp-push-enroll-late")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "enroll-late-session"
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	// 历史完成：发生在"进程启动"之后、"enrollment"之前——正是 B3 会回放的区间。
	history := `{"uuid":"u-hist","type":"user","timestamp":"2026-09-12T00:00:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"hist"}}` + "\n" +
		`{"uuid":"a-hist","parentUuid":"u-hist","type":"assistant","timestamp":"2026-09-12T00:00:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"hist reply"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers()
	h.claudeSessions = newClaudeSessionCatalog(projectsDir)
	store := newTestWebPushStore(t)
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_enroll_late")
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)
	// 生产形状：startedAt = 进程启动时刻（1 小时前），先经历无订阅期。
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState), startedAt: time.Now().UTC().Add(-time.Hour)}

	watcher.sweep() // disabled：不 refresh，且必须清空 startedAt（B3 修复点）
	if h.claudeSessions.snapshot != nil {
		t.Fatal("disabled sweep refreshed the catalog")
	}

	// 数小时后首次注册订阅 → enrollment。
	if _, err := store.Register("dev_enroll_late", testSubscriptionRecord("https://push.example.com/enroll-late")); err != nil {
		t.Fatal(err)
	}
	watcher.sweep() // 首个 enabled sweep：以 enrollment 时刻重基线
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("enrollment replayed %d historical candidates (B3: startedAt must re-baseline at enrollment)", len(got))
	}

	// enrollment 时进行中、之后完成的 turn：通知（规则固定）。
	live := `{"uuid":"u-live3","parentUuid":"a-hist","type":"user","timestamp":"2026-09-12T00:01:00Z","cwd":"` + workspace + `","message":{"role":"user","content":"new"}}` + "\n" +
		`{"uuid":"a-live3","parentUuid":"u-live3","type":"assistant","timestamp":"2026-09-12T00:01:01Z","cwd":"` + workspace + `","message":{"role":"assistant","content":[{"type":"text","text":"fresh answer"}],"stop_reason":"end_turn"}}` + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(live); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	watcher.sweep()
	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("post-enrollment live candidates = %d, want 1", len(got))
	}
	if got[0].SessionID != sessionID || got[0].ContentPreview != "fresh answer" {
		t.Fatalf("candidate = %+v, want session %s with fresh answer", got[0], sessionID)
	}
}
