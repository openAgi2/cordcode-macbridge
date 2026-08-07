package gobridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// §4.1 / §4.1.1 Phase 1 get_git_context 扩展 status 字段测试。
//
// 覆盖（对齐 plan §4.1.1 wire fixture + §7.3 #15）：
//   - 1 tracked modified + 1 untracked → changedFileCount==2、additions/deletions 与
//     get_workspace_diff 同次一致、纯 numstat 文件数(1)不得作为期望值。
//   - clean 工作区 → isDirty==false、changedFileCount==0。
//   - detached HEAD → currentBranch==""（空串，非字面量）。
//   - 无 upstream → hasUpstream==false、aheadCount/behindCount==nil(omit)。
//   - 无 origin → defaultBranch==nil(omit，不猜 main)。
//   - openPullRequest：非 GitHub / 无 gh → nil(omit)，不伪造。
func TestLoadGitContextStatusFields_TrackedModifiedPlusUntrackedCountsTwo(t *testing.T) {
	repo := makeGitRepository(t)

	// 1 tracked modified：改 README.md
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 1 untracked（exclude-standard 内）
	if err := os.WriteFile(filepath.Join(repo, "newfile.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := loadGitContext(repo)
	if err != nil {
		t.Fatalf("loadGitContext: %v", err)
	}

	// §4.1.1 定稿：changedFileCount 含 untracked == 2（不是纯 numstat 的 1）
	if ctx.ChangedFileCount == nil || *ctx.ChangedFileCount != 2 {
		got := "<nil>"
		if ctx.ChangedFileCount != nil {
			got = strconv.Itoa(*ctx.ChangedFileCount)
		}
		t.Fatalf("changedFileCount = %s, want 2 (含 untracked)", got)
	}
	if ctx.IsDirty == nil || !*ctx.IsDirty {
		t.Fatalf("isDirty = %v, want true (有 untracked → dirty)", ctx.IsDirty)
	}
	if ctx.IsRepo == nil || !*ctx.IsRepo {
		t.Fatalf("isRepo = %v, want true", ctx.IsRepo)
	}

	// additions/deletions 与 loadWorkspaceDiff 同源。README.md: "test\n" → "changed\nkeep\n" = +2 -1；
	// newfile.txt (untracked): +2 -0 → 合计 +4 -1。
	if ctx.Additions == nil || *ctx.Additions != 4 {
		t.Fatalf("additions = %v, want 4", ctx.Additions)
	}
	if ctx.Deletions == nil || *ctx.Deletions != 1 {
		t.Fatalf("deletions = %v, want 1", ctx.Deletions)
	}
}

func TestLoadGitContextStatusFields_CleanWorkspaceNotDirty(t *testing.T) {
	repo := makeGitRepository(t)
	ctx, err := loadGitContext(repo)
	if err != nil {
		t.Fatalf("loadGitContext: %v", err)
	}
	if ctx.IsDirty == nil || *ctx.IsDirty {
		t.Fatalf("isDirty = %v, want false (clean)", ctx.IsDirty)
	}
	if ctx.ChangedFileCount == nil || *ctx.ChangedFileCount != 0 {
		t.Fatalf("changedFileCount = %v, want 0 (clean)", ctx.ChangedFileCount)
	}
}

func TestLoadGitContextStatusFields_DetachedHeadEmptyBranch(t *testing.T) {
	repo := makeGitRepository(t)
	// detached HEAD：checkout 到具体 commit
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	head := strings.TrimSpace(string(out))
	runGitTestCommand(t, repo, "checkout", "--detach", head)

	ctx, err := loadGitContext(repo)
	if err != nil {
		t.Fatalf("loadGitContext: %v", err)
	}
	// §3.2: detached HEAD → currentBranch=="" (空串，wire 不下发字面量 "Detached HEAD")
	if ctx.CurrentBranch != "" {
		t.Fatalf("currentBranch = %q, want \"\" (detached HEAD)", ctx.CurrentBranch)
	}
}

func TestLoadGitContextStatusFields_NoUpstreamOmitsAheadBehind(t *testing.T) {
	repo := makeGitRepository(t)
	ctx, err := loadGitContext(repo)
	if err != nil {
		t.Fatalf("loadGitContext: %v", err)
	}
	// 临时 repo 无 origin → 无 upstream → hasUpstream==false、ahead/behind==nil(omit)
	if ctx.HasUpstream == nil || *ctx.HasUpstream {
		t.Fatalf("hasUpstream = %v, want false (no upstream)", ctx.HasUpstream)
	}
	if ctx.AheadCount != nil {
		t.Fatalf("aheadCount = %v, want nil (omit, 无 upstream)", ctx.AheadCount)
	}
	if ctx.BehindCount != nil {
		t.Fatalf("behindCount = %v, want nil (omit, 无 upstream)", ctx.BehindCount)
	}
}

func TestLoadGitContextStatusFields_NoOriginOmitsDefaultBranch(t *testing.T) {
	repo := makeGitRepository(t)
	ctx, err := loadGitContext(repo)
	if err != nil {
		t.Fatalf("loadGitContext: %v", err)
	}
	// §4.1: 无 origin → defaultBranch==nil(omit)，客户端不猜 main
	if ctx.DefaultBranch != nil {
		t.Fatalf("defaultBranch = %v, want nil (omit, 无 origin)", ctx.DefaultBranch)
	}
}

func TestLoadGitContextStatusFields_OpenPROmittedForNonGitHubOrNoGh(t *testing.T) {
	repo := makeGitRepository(t)
	ctx, err := loadGitContext(repo)
	if err != nil {
		t.Fatalf("loadGitContext: %v", err)
	}
	// 非 GitHub remote（临时 repo 无 remote）→ openPullRequest==nil(omit)，不伪造
	if ctx.OpenPullRequest != nil {
		t.Fatalf("openPullRequest = %v, want nil (omit, 非 GitHub)", ctx.OpenPullRequest)
	}
}

