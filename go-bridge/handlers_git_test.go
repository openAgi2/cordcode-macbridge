package gobridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLoadGitContextListsWorktreesAndRecentBranches(t *testing.T) {
	repository := makeGitRepository(t)
	runGitTestCommand(t, repository, "branch", "feature/test")
	worktreePath := filepath.Join(t.TempDir(), "feature-worktree")
	runGitTestCommand(t, repository, "worktree", "add", worktreePath, "feature/test")

	context, err := loadGitContext(repository)
	if err != nil {
		t.Fatalf("loadGitContext: %v", err)
	}
	if !sameResolvedPath(context.RepositoryRoot, repository) {
		t.Fatalf("repositoryRoot = %q, want %q", context.RepositoryRoot, repository)
	}
	if context.CurrentBranch != "main" {
		t.Fatalf("currentBranch = %q, want main", context.CurrentBranch)
	}
	if len(context.Worktrees) != 2 {
		t.Fatalf("worktrees = %#v, want 2 entries", context.Worktrees)
	}
	if len(context.Branches) != 2 {
		t.Fatalf("branches = %#v, want main and feature/test", context.Branches)
	}
}

func TestValidatedBranchNameRejectsInvalidRef(t *testing.T) {
	if _, err := validatedBranchName("bad branch"); err == nil {
		t.Fatal("validatedBranchName accepted a branch containing spaces")
	}
}

func makeGitRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, repository, "init", "-b", "main")
	runGitTestCommand(t, repository, "config", "user.email", "test@example.invalid")
	runGitTestCommand(t, repository, "config", "user.name", "CordCode Test")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, repository, "add", "README.md")
	runGitTestCommand(t, repository, "commit", "-m", "initial")
	return repository
}

func runGitTestCommand(t *testing.T, directory string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
