package gobridge

// ── §7.1 PR 集成纯函数单测（C1）──────────────────────────────────────────────
//
// 覆盖 handleCreatePullRequest 依赖的四个纯/弱依赖函数：
//   - slugFromTitle：标题 → 分支 slug（含 [cordcode] 前缀剥离、截断、中文降级）
//   - branchSlugRe：分支名白名单 ^cordcode/[a-z0-9][a-z0-9-]{0,60}$（边界 61 合法 / 62 非法）
//   - detectGitHubRemote：git remote origin 是否 GitHub（临时 git repo 真跑）
//   - supportsPullRequests：非 GitHub / 无 remote → false（与 gh 是否安装无关）
//
// handler e2e（真 gh pr create + GitHub remote）需 owner Mac gh 认证环境，不在此测。

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSlugFromTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"simple", "Add login flow", "cordcode/add-login-flow"},
		{"mixed case lowercased", "Fix BUG #123!!!", "cordcode/fix-bug-123"},
		{"collapses separators", "  Hello   World  ", "cordcode/hello-world"},
		{"strips cordcode prefix", "[cordcode] Add login flow", "cordcode/add-login-flow"},
		{"strips prefix no separator", "[cordcode]add-login", "cordcode/add-login"},
		{"case-insensitive prefix", "[Cordcode] Do Thing", "cordcode/do-thing"},
		{"truncates over 60", strings.Repeat("ab", 40), "cordcode/" + strings.Repeat("ab", 30)},
		{"chinese only yields empty slug", "添加登录", "cordcode/"},
		{"empty yields bare prefix", "", "cordcode/"},
		{"leading dashes trimmed", "---hi---", "cordcode/hi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := slugFromTitle(c.title); got != c.want {
				t.Errorf("slugFromTitle(%q) = %q, want %q", c.title, got, c.want)
			}
		})
	}
}

func TestBranchSlugRe(t *testing.T) {
	// regex `^cordcode/[a-z0-9][a-z0-9-]{0,60}$`：前缀后 1 + (0..60) = 1..61 字符合法。
	valid := []string{
		"cordcode/a",
		"cordcode/abc-123",
		"cordcode/add-login-flow",
		"cordcode/" + strings.Repeat("a", 61), // regex 上界：1 + 60
	}
	for _, s := range valid {
		if !branchSlugRe.MatchString(s) {
			t.Errorf("expected valid: %q", s)
		}
	}
	invalid := []string{
		"cordcode/",                 // 前缀后为空
		"cordcode/-abc",             // 首字符为 -
		"cordcode/ABC",              // 大写
		"cordcode/abc/def",          // 额外斜杠
		"cordcode/" + strings.Repeat("a", 62), // 超过 regex 上界
		"feature/abc",               // 错误前缀
		"evil/../etc",               // 路径穿越
		"cordcode/$(whoami)",        // 注入：首字符 $ 非 [a-z0-9]
		"cordcode/a b",              // 空格
	}
	for _, s := range invalid {
		if branchSlugRe.MatchString(s) {
			t.Errorf("expected invalid: %q", s)
		}
	}
}

// gitIfAvailable 跳过没有 git 的环境（CI/dev 都有，保底）。
func gitIfAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

// initTempGitRepo 在临时目录 git init 并（当 remoteURL 非空）加 origin remote。
func initTempGitRepo(t *testing.T, remoteURL string) string {
	t.Helper()
	gitIfAvailable(t)
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %s (%v)", args, dir, out, err)
		}
	}
	run("init")
	run("config", "user.email", "t@t.test")
	run("config", "user.name", "test")
	if remoteURL != "" {
		run("remote", "add", "origin", remoteURL)
	}
	return dir
}

func TestDetectGitHubRemote(t *testing.T) {
	cases := []struct {
		name      string
		remoteURL string
		wantErr   bool
	}{
		{"github https", "https://github.com/owner/repo.git", false},
		{"github ssh", "git@github.com:owner/repo.git", false},
		{"gitlab", "https://gitlab.com/owner/repo.git", true},
		{"bitbucket", "git@bitbucket.org:owner/repo.git", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := initTempGitRepo(t, c.remoteURL)
			got, err := detectGitHubRemote(dir)
			if c.wantErr {
				if err == nil {
					t.Fatalf("detectGitHubRemote(%q) expected error, got url=%s", c.remoteURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("detectGitHubRemote(%q) unexpected error: %v", c.remoteURL, err)
			}
			if !strings.Contains(strings.ToLower(got), "github.com") {
				t.Errorf("detectGitHubRemote(%q) = %q, want contains github.com", c.remoteURL, got)
			}
		})
	}
}

func TestDetectGitHubRemote_NoRemote(t *testing.T) {
	dir := initTempGitRepo(t, "") // 无 remote
	if _, err := detectGitHubRemote(dir); err == nil {
		t.Fatal("detectGitHubRemote on repo without origin expected error, got nil")
	}
}

// supportsPullRequests 在「非 GitHub remote」/「无 remote」路径上必须 false——
// detectGitHubRemote 先返回 err，根本到不了 exec.LookPath("gh")。gh-present 路径
// 依赖本机 gh 认证状态，不在此测（环境敏感）。
func TestSupportsPullRequests_NonGitHub(t *testing.T) {
	dir := initTempGitRepo(t, "https://gitlab.com/owner/repo.git")
	if supportsPullRequests(dir) {
		t.Error("supportsPullRequests should be false for non-GitHub remote")
	}
}

func TestSupportsPullRequests_NoRemote(t *testing.T) {
	dir := initTempGitRepo(t, "")
	if supportsPullRequests(dir) {
		t.Error("supportsPullRequests should be false when no remote configured")
	}
}
