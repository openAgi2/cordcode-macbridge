package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestListMemoryFiles_OnlyReturnsExistingStableAgentFiles(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll(globalCodexDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte("# project\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(project AGENTS.md): %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".codex", "AGENTS.md"), []byte("# global\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(global AGENTS.md): %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "IGNORED.md"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf("WriteFile(IGNORED.md): %v", err)
	}

	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	agent := &Agent{workDir: workDir}
	files, err := agent.ListMemoryFiles(context.Background())
	if err != nil {
		t.Fatalf("ListMemoryFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("file count = %d, want 2", len(files))
	}
	if files[0].ID != projectAgentsMemoryFileID {
		t.Fatalf("files[0].ID = %q, want %q", files[0].ID, projectAgentsMemoryFileID)
	}
	if files[0].Name != "AGENTS.md" {
		t.Fatalf("files[0].Name = %q, want AGENTS.md", files[0].Name)
	}
	if files[0].Scope != "project" {
		t.Fatalf("files[0].Scope = %q, want project", files[0].Scope)
	}
	if files[1].ID != globalAgentsMemoryFileID {
		t.Fatalf("files[1].ID = %q, want %q", files[1].ID, globalAgentsMemoryFileID)
	}
	if files[1].Scope != "global" {
		t.Fatalf("files[1].Scope = %q, want global", files[1].Scope)
	}
	if files[0].ETag == "" || files[1].ETag == "" {
		t.Fatal("expected non-empty etag for listed memory files")
	}
}

func TestReadMemoryFile_UsesStableIDsAndReturnsContent(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll(globalCodexDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	projectContent := "# Project Instructions\nAlways test changes.\n"
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte(projectContent), 0o644); err != nil {
		t.Fatalf("WriteFile(project AGENTS.md): %v", err)
	}

	agent := &Agent{workDir: workDir}
	file, err := agent.ReadMemoryFile(context.Background(), projectAgentsMemoryFileID)
	if err != nil {
		t.Fatalf("ReadMemoryFile() error = %v", err)
	}
	if file == nil {
		t.Fatal("ReadMemoryFile() = nil, want file")
	}
	if file.ID != projectAgentsMemoryFileID {
		t.Fatalf("file.ID = %q, want %q", file.ID, projectAgentsMemoryFileID)
	}
	if file.Name != "AGENTS.md" {
		t.Fatalf("file.Name = %q, want AGENTS.md", file.Name)
	}
	if file.Content != projectContent {
		t.Fatalf("file.Content = %q, want %q", file.Content, projectContent)
	}
	if file.ContentType != "text/markdown" {
		t.Fatalf("file.ContentType = %q, want text/markdown", file.ContentType)
	}
	if file.Scope != "project" {
		t.Fatalf("file.Scope = %q, want project", file.Scope)
	}
}

func TestAgentImplementsMemoryFileReader(t *testing.T) {
	var _ core.MemoryFileReader = (*Agent)(nil)
}
