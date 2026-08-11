package gobridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWorkspaceDiffReflectsCurrentGitState(t *testing.T) {
	repo := t.TempDir()
	runTestGit(t, repo, "init", "-q")
	runTestGit(t, repo, "config", "user.email", "test@example.com")
	runTestGit(t, repo, "config", "user.name", "Test")

	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("old\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "add", "tracked.txt")
	runTestGit(t, repo, "commit", "-qm", "initial")

	if err := os.WriteFile(tracked, []byte("new\nkeep\nextra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := loadWorkspaceDiff(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("files = %d, want 2: %#v", len(result.Files), result.Files)
	}
	if result.Additions != 4 || result.Deletions != 1 {
		t.Fatalf("summary = +%d -%d, want +4 -1", result.Additions, result.Deletions)
	}
	if result.Files[0].Path != "new.txt" || result.Files[1].Path != "tracked.txt" {
		t.Fatalf("paths = %#v", result.Files)
	}

	runTestGit(t, repo, "add", ".")
	runTestGit(t, repo, "commit", "-qm", "clean")
	clean, err := loadWorkspaceDiff(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(clean.Files) != 0 || clean.Additions != 0 || clean.Deletions != 0 {
		t.Fatalf("clean result = %#v", clean)
	}
}

func TestLoadWorkspaceDiffKeepsNonASCIIPathRaw(t *testing.T) {
	repo := t.TempDir()
	runTestGit(t, repo, "init", "-q")
	runTestGit(t, repo, "config", "user.email", "test@example.com")
	runTestGit(t, repo, "config", "user.name", "Test")
	runTestGit(t, repo, "config", "core.quotePath", "true")

	path := filepath.Join(repo, "童话故事.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "add", ".")
	runTestGit(t, repo, "commit", "-qm", "initial")
	if err := os.WriteFile(path, []byte("old\nnew line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := loadWorkspaceDiff(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("files = %d, want 1: %#v", len(result.Files), result.Files)
	}
	if result.Files[0].Path != "童话故事.txt" {
		t.Fatalf("path = %q, want raw UTF-8 童话故事.txt", result.Files[0].Path)
	}
	if !strings.Contains(result.Files[0].Diff, "+new line") {
		t.Fatalf("diff missing patch content: %q", result.Files[0].Diff)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
