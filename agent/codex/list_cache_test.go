package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionListCacheDoesNotRewriteRollout(t *testing.T) {
	codexHome, sessionPath := writeCodexSessionFixture(t)
	before, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session before list: %v", err)
	}

	var cache sessionListCache
	sessions, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(sessions))
	}

	stat, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	entry := cache.files[sessionPath]
	if entry == nil {
		t.Fatalf("cache entry missing for %s", sessionPath)
	}
	if !entry.mtime.Equal(stat.ModTime()) {
		t.Fatalf("cached mtime = %v, want file mtime %v", entry.mtime, stat.ModTime())
	}

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	firstLine := strings.SplitN(string(data), "\n", 2)[0]
	if !strings.Contains(firstLine, `"source":"exec"`) {
		t.Fatalf("session source changed while listing: %s", firstLine)
	}
	if string(data) != string(before) {
		t.Fatal("session rollout was rewritten while listing")
	}
}

func TestSessionListCacheDetectsSizeChangeWithSameMTime(t *testing.T) {
	codexHome, sessionPath := writeCodexSessionFixture(t)
	var cache sessionListCache
	if _, err := cache.list(context.Background(), codexHome); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"event_msg","payload":{"type":"noise"}}` + "\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sessionPath, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}

	if _, err := cache.list(context.Background(), codexHome); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if cache.files[sessionPath].size != after.Size() {
		t.Fatalf("cached size = %d, want %d", cache.files[sessionPath].size, after.Size())
	}
}

func TestSessionListCacheUsesSessionIDAsTimestampTieBreaker(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modifiedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"session-b", "session-a"} {
		path := filepath.Join(sessionsDir, "rollout-"+id+".jsonl")
		content := `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/tmp/project"}}` + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modifiedAt, modifiedAt); err != nil {
			t.Fatal(err)
		}
	}

	var cache sessionListCache
	sessions, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != "session-a" || sessions[1].ID != "session-b" {
		t.Fatalf("tie-break ordering = %#v", sessions)
	}
}

func TestSessionListCacheUsesTranscriptTimestampWhenMTimesMatch(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	migrationTime := time.Date(2026, 7, 10, 12, 53, 0, 0, time.UTC)
	fixtures := []struct {
		id        string
		updatedAt time.Time
	}{
		{"older-session", time.Date(2026, 7, 6, 12, 45, 0, 0, time.UTC)},
		{"newer-session", time.Date(2026, 7, 9, 9, 30, 0, 0, time.UTC)},
	}
	for _, fixture := range fixtures {
		path := filepath.Join(sessionsDir, "rollout-"+fixture.id+".jsonl")
		content := strings.Join([]string{
			`{"timestamp":"2026-07-01T00:00:00Z","type":"session_meta","payload":{"id":"` + fixture.id + `","cwd":"/tmp/project"}}`,
			`{"timestamp":"` + fixture.updatedAt.Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"task_complete"}}`,
		}, "\n")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, migrationTime, migrationTime); err != nil {
			t.Fatal(err)
		}
	}

	var cache sessionListCache
	sessions, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions length = %d, want 2", len(sessions))
	}
	if sessions[0].ID != "newer-session" || !sessions[0].ModifiedAt.Equal(fixtures[1].updatedAt) {
		t.Fatalf("first session = %#v, want newer transcript timestamp", sessions[0])
	}
	if sessions[1].ID != "older-session" || !sessions[1].ModifiedAt.Equal(fixtures[0].updatedAt) {
		t.Fatalf("second session = %#v, want older transcript timestamp", sessions[1])
	}
}

func TestSessionListCacheUsesFirstUserPromptAsSummary(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	sessionID := "multi-turn-session"
	sessionPath := filepath.Join(sessionsDir, "rollout-"+sessionID+".jsonl")
	content := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/project"}}`,
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"first real prompt"}]}}`,
		`{"type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`,
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"second real prompt"}]}}`,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	var cache sessionListCache
	sessions, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(sessions))
	}
	if sessions[0].Summary != "first real prompt" {
		t.Fatalf("summary = %q, want first real prompt", sessions[0].Summary)
	}
}

func TestSessionListCacheRestoresModelAndReasoningEffort(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	sessionID := "runtime-metadata-session"
	sessionPath := filepath.Join(sessionsDir, "rollout-"+sessionID+".jsonl")
	content := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/project","model_provider":"openai"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5.5","collaboration_mode":{"settings":{"reasoning_effort":"medium"}}}}`,
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"prompt"}]}}`,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	var cache sessionListCache
	sessions, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(sessions))
	}
	if sessions[0].ModelID != "gpt-5.5" {
		t.Fatalf("ModelID = %q, want gpt-5.5", sessions[0].ModelID)
	}
	if sessions[0].ProviderID != "openai" {
		t.Fatalf("ProviderID = %q, want openai", sessions[0].ProviderID)
	}
	if sessions[0].ReasoningEffort != "medium" {
		t.Fatalf("ReasoningEffort = %q, want medium", sessions[0].ReasoningEffort)
	}
}

func TestSessionListCacheUsesCodexThreadNameAsSummary(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	sessionID := "indexed-session"
	sessionPath := filepath.Join(sessionsDir, "rollout-"+sessionID+".jsonl")
	content := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/project"}}`,
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"raw first prompt"}]}}`,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(codexHome, "session_index.jsonl"),
		[]byte(`{"id":"`+sessionID+`","thread_name":"Codex Desktop title","updated_at":"2026-06-28T08:42:45Z"}`+"\n"),
		0o644,
	); err != nil {
		t.Fatalf("write session index: %v", err)
	}

	var cache sessionListCache
	sessions, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(sessions))
	}
	if sessions[0].Summary != "Codex Desktop title" {
		t.Fatalf("summary = %q, want Codex Desktop title", sessions[0].Summary)
	}

	if err := os.WriteFile(
		filepath.Join(codexHome, "session_index.jsonl"),
		[]byte(`{"id":"`+sessionID+`","thread_name":"Updated Desktop title","updated_at":"2026-06-28T08:45:00Z"}`+"\n"),
		0o644,
	); err != nil {
		t.Fatalf("update session index: %v", err)
	}
	sessions, err = cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("list after index update failed: %v", err)
	}
	if sessions[0].Summary != "Updated Desktop title" {
		t.Fatalf("summary after index update = %q, want Updated Desktop title", sessions[0].Summary)
	}
}

func TestSessionListCacheReadsLargeRolloutFromBoundedPrefix(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	sessionID := "large-session-cache"
	sessionPath := filepath.Join(sessionsDir, "rollout-"+sessionID+".jsonl")
	var content strings.Builder
	content.WriteString(`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/large"}}` + "\n")
	content.WriteString(`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"first prompt"}]}}` + "\n")
	for content.Len() <= int(codexSessionListPrefixBytes)+1024 {
		content.WriteString(`{"type":"event_msg","payload":{"type":"noise","message":"`)
		content.WriteString(strings.Repeat("x", 4096))
		content.WriteString(`"}}` + "\n")
	}
	content.WriteString(`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"latest prompt"}]}}` + "\n")
	if err := os.WriteFile(sessionPath, []byte(content.String()), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	var cache sessionListCache
	sessions, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(sessions))
	}
	if sessions[0].ID != sessionID {
		t.Fatalf("session id = %q, want %q", sessions[0].ID, sessionID)
	}
	if sessions[0].Directory != "/tmp/large" {
		t.Fatalf("directory = %q, want /tmp/large", sessions[0].Directory)
	}
	if sessions[0].Summary != "first prompt" {
		t.Fatalf("summary = %q, want first prompt", sessions[0].Summary)
	}
}

func TestSessionListCacheReturnsCopy(t *testing.T) {
	codexHome, _ := writeCodexSessionFixture(t)

	var cache sessionListCache
	first, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("first list failed: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first length = %d, want 1", len(first))
	}

	first[0].Summary = "polluted"

	second, err := cache.list(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("second list failed: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("second length = %d, want 1", len(second))
	}
	if second[0].Summary == "polluted" {
		t.Fatalf("cached session was mutated through returned slice")
	}
}

func writeCodexSessionFixture(t *testing.T) (string, string) {
	t.Helper()

	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	sessionID := "test-session-cache"
	sessionPath := filepath.Join(sessionsDir, "rollout-"+sessionID+".jsonl")
	content := strings.Join([]string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","source":"exec","originator":"codex_exec","cwd":"/tmp/project"}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"real user prompt"}]}}`,
		``,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	return codexHome, sessionPath
}
