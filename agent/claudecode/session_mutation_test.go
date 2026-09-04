package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenameSession_AppendsCustomTitleAndUpdatesListSessions(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	path := writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-rename", []string{
		`{"type":"user","timestamp":"2026-05-03T10:00:00Z","message":{"id":"user-1","role":"user","content":"原始标题来源"}}`,
		`{"type":"assistant","timestamp":"2026-05-03T10:00:01Z","message":{"id":"assistant-1","role":"assistant","content":[{"type":"text","text":"收到"}]}}`,
	})

	agent := &Agent{workDir: workDir}
	updated, err := agent.RenameSession(context.Background(), "ses-rename", "新的会话标题")
	if err != nil {
		t.Fatalf("RenameSession() error = %v", err)
	}
	if updated == nil {
		t.Fatal("RenameSession() = nil, want session info")
	}
	if updated.Summary != "新的会话标题" {
		t.Fatalf("updated.Summary = %q, want 新的会话标题", updated.Summary)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if got := string(content); !containsAll(got, `"type":"cordcode:custom-title"`, `"customTitle":"新的会话标题"`) {
		t.Fatalf("session file missing custom-title record: %s", got)
	}

	sessions, err := agent.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}
	if sessions[0].Summary != "新的会话标题" {
		t.Fatalf("ListSessions summary = %q, want 新的会话标题", sessions[0].Summary)
	}
}

func TestListSessionsUsesFirstUserPromptAsDefaultTitle(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-title", []string{
		`{"type":"user","timestamp":"2026-05-03T10:00:00Z","message":{"id":"user-1","role":"user","content":"第一个问题"}}`,
		`{"type":"assistant","timestamp":"2026-05-03T10:00:01Z","message":{"id":"assistant-1","role":"assistant","content":[{"type":"text","text":"收到"}]}}`,
		`{"type":"user","timestamp":"2026-05-03T10:00:02Z","message":{"id":"user-2","role":"user","content":"第二个问题"}}`,
	})

	agent := &Agent{workDir: workDir}
	sessions, err := agent.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}
	if sessions[0].Summary != "第一个问题" {
		t.Fatalf("ListSessions summary = %q, want 第一个问题", sessions[0].Summary)
	}
}

func TestListSessionsIgnoresResumeMetaContinuation(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-resume-meta-list", []string{
		`{"type":"user","timestamp":"2026-07-01T08:00:00Z","message":{"id":"user-1","role":"user","content":"first real prompt"}}`,
		`{"type":"assistant","timestamp":"2026-07-01T08:00:01Z","message":{"id":"assistant-1","role":"assistant","content":[{"type":"text","text":"first real answer"}],"stop_reason":"end_turn"}}`,
		`{"type":"user","isMeta":true,"timestamp":"2026-07-01T08:01:00Z","message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-01T08:01:00Z","message":{"id":"assistant-meta","role":"assistant","content":[{"type":"text","text":"No response requested."}],"stop_reason":"end_turn"}}`,
		`{"type":"user","timestamp":"2026-07-01T08:01:01Z","message":{"id":"user-2","role":"user","content":"second real prompt"}}`,
		`{"type":"assistant","timestamp":"2026-07-01T08:01:02Z","message":{"id":"assistant-2","role":"assistant","content":[{"type":"text","text":"second real answer"}],"stop_reason":"end_turn"}}`,
	})

	agent := &Agent{workDir: workDir}
	sessions, err := agent.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}
	if sessions[0].Summary != "first real prompt" {
		t.Fatalf("ListSessions summary = %q, want first real prompt", sessions[0].Summary)
	}
	if sessions[0].MessageCount != 4 {
		t.Fatalf("ListSessions message count = %d, want 4", sessions[0].MessageCount)
	}
}

func TestArchiveSession_WritesSidecarAndMarksListSessions(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-archive", []string{
		`{"type":"user","timestamp":"2026-05-03T11:00:00Z","message":{"id":"user-1","role":"user","content":"待归档"}}`,
	})

	archivedAt := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	agent := &Agent{workDir: workDir}
	updated, err := agent.ArchiveSession(context.Background(), "ses-archive", archivedAt)
	if err != nil {
		t.Fatalf("ArchiveSession() error = %v", err)
	}
	if updated == nil {
		t.Fatal("ArchiveSession() = nil, want session info")
	}
	if !updated.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("updated.ArchivedAt = %v, want %v", updated.ArchivedAt, archivedAt)
	}

	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		t.Fatalf("Abs(workDir): %v", err)
	}
	projectDir := filepath.Join(homeDir, ".claude", "projects", encodeClaudeProjectKey(absWorkDir))
	sidecarPath := filepath.Join(projectDir, claudeSessionMetaDirName, "ses-archive.json")
	if _, err := os.Stat(sidecarPath); err != nil {
		t.Fatalf("expected archive sidecar at %q: %v", sidecarPath, err)
	}

	sessions, err := agent.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}
	if !sessions[0].ArchivedAt.Equal(archivedAt) {
		t.Fatalf("ListSessions archivedAt = %v, want %v", sessions[0].ArchivedAt, archivedAt)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}
