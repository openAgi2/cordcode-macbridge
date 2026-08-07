package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSessionFilePrefersActiveSessionsOverArchived(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "019fd678-active-over-archived"

	activeDir := filepath.Join(codexHome, "sessions", "2026", "08", "06")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("mkdir active: %v", err)
	}
	activePath := filepath.Join(activeDir, "rollout-2026-08-06T17-47-04-"+sessionID+".jsonl")
	if err := os.WriteFile(activePath, []byte("{\"type\":\"session_meta\"}\n"), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}

	archivedDir := filepath.Join(codexHome, "archived_sessions")
	if err := os.MkdirAll(archivedDir, 0o755); err != nil {
		t.Fatalf("mkdir archived: %v", err)
	}
	archivedPath := filepath.Join(archivedDir, "rollout-2026-08-06T02-13-07-"+sessionID+".jsonl")
	if err := os.WriteFile(archivedPath, []byte("{\"type\":\"session_meta\"}\n"), 0o644); err != nil {
		t.Fatalf("write archived: %v", err)
	}

	got := findSessionFile(sessionID, codexHome)
	if got != activePath {
		t.Fatalf("findSessionFile = %q, want active %q", got, activePath)
	}
}

func TestFindSessionFileFallsBackToArchivedSessions(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "019fd321-archived-only"

	// Active sessions tree exists but empty for this id (mirrors Codex Desktop archive).
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions", "2026", "08", "06"), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	archivedDir := filepath.Join(codexHome, "archived_sessions")
	if err := os.MkdirAll(archivedDir, 0o755); err != nil {
		t.Fatalf("mkdir archived: %v", err)
	}
	archivedPath := filepath.Join(archivedDir, "rollout-2026-08-06T02-13-07-"+sessionID+".jsonl")
	if err := os.WriteFile(archivedPath, []byte("{\"type\":\"session_meta\"}\n"), 0o644); err != nil {
		t.Fatalf("write archived: %v", err)
	}

	got := findSessionFile(sessionID, codexHome)
	if got != archivedPath {
		t.Fatalf("findSessionFile = %q, want archived %q", got, archivedPath)
	}

	// Same helper used by todos / context usage / TranscriptPath.
	got2 := findSessionFileInCodexHome(codexHome, sessionID)
	if got2 != archivedPath {
		t.Fatalf("findSessionFileInCodexHome = %q, want archived %q", got2, archivedPath)
	}
}

func TestFindSessionFileMissingEverywhereReturnsEmpty(t *testing.T) {
	codexHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(codexHome, "archived_sessions"), 0o755); err != nil {
		t.Fatalf("mkdir archived: %v", err)
	}
	if got := findSessionFile("does-not-exist", codexHome); got != "" {
		t.Fatalf("findSessionFile = %q, want empty", got)
	}
}
