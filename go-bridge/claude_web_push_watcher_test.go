package gobridge

import (
	"os"
	"path/filepath"
	"testing"
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
