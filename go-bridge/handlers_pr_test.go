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
	"os"
	"os/exec"
	"path/filepath"
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

// ── §7.1 PULL_REQUEST_TEMPLATE.md 模板探测单测 ──────────────────────────────────────
//
// 覆盖 mergePullRequestBody / readPRTemplate / prBodySections 三个纯函数。全部基于
// t.TempDir()，不触网、不依赖 gh 认证。handler e2e（真 gh pr create + GitHub PR 上
// 是否含模板内容）需 owner Mac gh 认证环境，由 owner 真机回归（plan §2.6）。

// samplePRBody 复刻 iOS buildPullRequestBody (ChatUIKitContainerView.swift) 的三段输出，
// 供占位符替换测试取「对应段」。
func samplePRBody() string {
	return strings.Join([]string{
		"## Summary",
		"",
		"Add login flow",
		"",
		"## Changes",
		"",
		"2 个文件变更（+10 −3）",
		"",
		"- `src/a.swift` (+7 −1)",
		"- `src/b.swift` (+3 −2)",
		"",
		"## Cordcode",
		"",
		"由 CordCode iOS 创建。",
	}, "\n")
}

func writeTemplate(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if dir := filepath.Dir(path); dir != root {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMergePRBody_NoTemplate(t *testing.T) {
	root := t.TempDir()
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != samplePRBody() {
		t.Errorf("no template: expected original body unchanged, got diff")
	}
}

func TestMergePRBody_RootTemplate_Append(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", "## 背景\n这是一个改动。")
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "## 背景\n这是一个改动。\n\n---\n\n" + samplePRBody()
	if got != want {
		t.Errorf("root template append mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestMergePRBody_GitHubDirTemplate_Append(t *testing.T) {
	root := t.TempDir()
	// 根目录无模板，仅 .github/ 有 → 命中 .github 候选。
	writeTemplate(t, root, filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"), "GH TEMPLATE")
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "GH TEMPLATE\n\n---\n\n" + samplePRBody()
	if got != want {
		t.Errorf(".github template append mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestMergePRBody_RootPreferredOverGitHubDir(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", "ROOT")
	writeTemplate(t, root, filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"), "GITHUB")
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "ROOT\n\n---") || strings.Contains(got, "GITHUB") {
		t.Errorf("root template must win over .github/: got %q", got)
	}
}

func TestMergePRBody_PlaceholderReplace(t *testing.T) {
	root := t.TempDir()
	tpl := strings.Join([]string{
		"## 背景",
		"{{ summary }}",
		"",
		"## 改动",
		"{{ changes }}",
		"",
		"## 备注",
		"（手填）",
	}, "\n")
	writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", tpl)
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "{{ summary }}") || strings.Contains(got, "{{ changes }}") {
		t.Errorf("placeholders not replaced: %q", got)
	}
	if !strings.Contains(got, "## 背景\nAdd login flow") {
		t.Errorf("summary not spliced into {{ summary }}: %q", got)
	}
	if !strings.Contains(got, "## 改动\n2 个文件变更（+10 −3）") {
		t.Errorf("changes not spliced into {{ changes }}: %q", got)
	}
	// 占位符模式：模板完全控制布局，Cordcode 署名段不自动追加（v1 由模板作者决定）。
	if strings.Contains(got, "由 CordCode iOS 创建。") {
		t.Errorf("placeholder mode must not auto-append Cordcode section: %q", got)
	}
}

func TestMergePRBody_OnlySummarySlot(t *testing.T) {
	root := t.TempDir()
	// 只有 {{ summary }} 没有 {{ changes }}：仍判为占位符模式，未出现的占位符不替换。
	writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", "T: {{ summary }}")
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "T: Add login flow") {
		t.Errorf("summary slot not filled: %q", got)
	}
	if strings.Contains(got, "---") {
		t.Errorf("single-placeholder must stay in placeholder mode (no append): %q", got)
	}
}

func TestMergePRBody_OversizedFallsBack(t *testing.T) {
	root := t.TempDir()
	// > 64 KiB → 视为无模板，原样回落。
	huge := strings.Repeat("a", prTemplateMaxBytes+1)
	writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", huge)
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != samplePRBody() {
		t.Errorf("oversized template must fall back to original body, got len=%d", len(got))
	}
}

func TestMergePRBody_EmptyTemplateFallsBack(t *testing.T) {
	root := t.TempDir()
	// 纯空白模板 → 视为该候选未配置；无其他候选 → 原样回落。
	writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", "   \n\n  \n")
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != samplePRBody() {
		t.Errorf("empty template must fall back to original body, got %q", got)
	}
}

func TestMergePRBody_PathEscapeRejected(t *testing.T) {
	// root 受信任（git-resolved）。函数只拼固定相对名，不会读取 root 之外的文件。
	// 把「陷阱模板」放进 root 的父目录，确认不被读取。
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	writeTemplate(t, parent, "PULL_REQUEST_TEMPLATE.md", "PARENT-TRAP {{ summary }}")
	// root 内放一个同名陷阱（也会被合法读到），以及一个绝不应被读取的 decoy。
	writeTemplate(t, root, "README.md", "DECOY-BODY")
	got, err := mergePullRequestBody(root, samplePRBody())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "PARENT-TRAP") {
		t.Errorf("read outside root (parent dir): %q", got)
	}
	if strings.Contains(got, "DECOY-BODY") {
		t.Errorf("read non-template file under root: %q", got)
	}
	// root 内无任何合法模板候选 → 原样回落。
	if got != samplePRBody() {
		t.Errorf("expected original body when no candidate under root, got %q", got)
	}
}

func TestReadPRTemplate(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		if _, ok := readPRTemplate(t.TempDir()); ok {
			t.Error("expected ok=false for missing template")
		}
	})
	t.Run("root hit", func(t *testing.T) {
		root := t.TempDir()
		writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", "ROOT")
		got, ok := readPRTemplate(root)
		if !ok || got != "ROOT" {
			t.Errorf("root hit: got %q ok=%v", got, ok)
		}
	})
	t.Run("github dir hit", func(t *testing.T) {
		root := t.TempDir()
		writeTemplate(t, root, filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"), "GH")
		got, ok := readPRTemplate(root)
		if !ok || got != "GH" {
			t.Errorf(".github hit: got %q ok=%v", got, ok)
		}
	})
	t.Run("oversized bails", func(t *testing.T) {
		root := t.TempDir()
		writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", strings.Repeat("x", prTemplateMaxBytes+1))
		if _, ok := readPRTemplate(root); ok {
			t.Error("oversized template must not be returned")
		}
	})
	t.Run("empty skipped", func(t *testing.T) {
		root := t.TempDir()
		writeTemplate(t, root, "PULL_REQUEST_TEMPLATE.md", "  \n")
		writeTemplate(t, root, filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"), "GH")
		// 根模板空白 → 跳过 → 命中 .github。
		got, ok := readPRTemplate(root)
		if !ok || got != "GH" {
			t.Errorf("empty root should skip to .github: got %q ok=%v", got, ok)
		}
	})
}

func TestPRBodySections(t *testing.T) {
	sections := prBodySections(samplePRBody())
	if sections["Summary"] != "Add login flow" {
		t.Errorf("Summary section = %q, want %q", sections["Summary"], "Add login flow")
	}
	wantChanges := "2 个文件变更（+10 −3）\n\n- `src/a.swift` (+7 −1)\n- `src/b.swift` (+3 −2)"
	if sections["Changes"] != wantChanges {
		t.Errorf("Changes section = %q, want %q", sections["Changes"], wantChanges)
	}
	if sections["Cordcode"] != "由 CordCode iOS 创建。" {
		t.Errorf("Cordcode section = %q", sections["Cordcode"])
	}
}
