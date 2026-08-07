package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// gitRunOption configures an extended runGitInDirectoryWith invocation (env override /
// context-bound execution). It keeps a single git-runner code path: checkpoint capture
// and any future caller pass options, while the legacy runGitInDirectory wrapper stays
// zero-opts so existing call sites are unchanged (no third bare exec.Command fork).
type gitRunOption func(*gitRunConfig)

type gitRunConfig struct {
	ctx context.Context
	env []string
}

// WithEnv appends extra environment variables to the inherited os.Environ() plus
// GIT_TERMINAL_PROMPT=0. Callers pass e.g. GIT_INDEX_FILE to stage a temp index.
func WithEnv(env []string) gitRunOption {
	return func(c *gitRunConfig) { c.env = append(c.env, env...) }
}

// WithContext bounds the git process; cancellation/timeout kills it (exec.CommandContext).
// Checkpoint git I/O MUST always pass a context with a timeout so a wedged git cannot hold
// the coalescer goroutine forever.
func WithContext(ctx context.Context) gitRunOption {
	return func(c *gitRunConfig) { c.ctx = ctx }
}

type gitWorktree struct {
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	IsCurrent bool   `json:"isCurrent"`
}

type gitContext struct {
	RepositoryRoot string        `json:"repositoryRoot"`
	CurrentBranch  string        `json:"currentBranch"`
	Worktrees      []gitWorktree `json:"worktrees"`
	Branches       []string      `json:"branches"`
}

func (h *Handlers) handleGetGitContext(conn Connection, msg WireMessage) {
	directory, wireErr := gitDirectoryParam(msg)
	if wireErr != nil {
		conn.SendResult(msg.RequestID, nil, wireErr)
		return
	}
	context, err := loadGitContext(directory)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_context_failed", err))
		return
	}
	conn.SendResult(msg.RequestID, context, nil)
}

func (h *Handlers) handleCheckoutGitBranch(conn Connection, msg WireMessage) {
	var params struct {
		Directory string `json:"directory"`
		Branch    string `json:"branch"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	if err := validateGitDirectory(params.Directory); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("invalid_directory", err))
		return
	}
	branch, err := validatedBranchName(params.Branch)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("invalid_branch", err))
		return
	}
	if _, err := runGitInDirectory(params.Directory, "switch", branch); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_checkout_failed", err))
		return
	}
	context, err := loadGitContext(params.Directory)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_context_failed", err))
		return
	}
	conn.SendResult(msg.RequestID, context, nil)
}

func (h *Handlers) handleCreateGitBranch(conn Connection, msg WireMessage) {
	var params struct {
		Directory string `json:"directory"`
		Branch    string `json:"branch"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	if err := validateGitDirectory(params.Directory); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("invalid_directory", err))
		return
	}
	branch, err := validatedBranchName(params.Branch)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("invalid_branch", err))
		return
	}
	if _, err := runGitInDirectory(params.Directory, "switch", "-c", branch); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_create_branch_failed", err))
		return
	}
	context, err := loadGitContext(params.Directory)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_context_failed", err))
		return
	}
	conn.SendResult(msg.RequestID, context, nil)
}

func (h *Handlers) handleCreateGitWorktree(conn Connection, msg WireMessage) {
	var params struct {
		Directory string `json:"directory"`
		Path      string `json:"path"`
		Branch    string `json:"branch"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	if err := validateGitDirectory(params.Directory); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("invalid_directory", err))
		return
	}
	branch, err := validatedBranchName(params.Branch)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("invalid_branch", err))
		return
	}
	targetPath, err := expandPath(strings.TrimSpace(params.Path))
	if err != nil || !filepath.IsAbs(targetPath) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_worktree_path", Message: "worktree path must be absolute"})
		return
	}
	if _, err := os.Stat(targetPath); err == nil || !os.IsNotExist(err) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "worktree_path_exists", Message: "worktree path already exists"})
		return
	}
	if _, err := runGitInDirectory(params.Directory, "worktree", "add", "-b", branch, targetPath); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_create_worktree_failed", err))
		return
	}
	context, err := loadGitContext(targetPath)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_context_failed", err))
		return
	}
	conn.SendResult(msg.RequestID, context, nil)
}

func gitDirectoryParam(msg WireMessage) (string, *WireError) {
	var params struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return "", &WireError{Code: "invalid_params", Message: err.Error()}
	}
	if err := validateGitDirectory(params.Directory); err != nil {
		return "", gitWireError("invalid_directory", err)
	}
	return params.Directory, nil
}

func validateGitDirectory(directory string) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("directory is required")
	}
	resolved, err := expandPath(directory)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("directory is not a folder")
	}
	return nil
}

func validatedBranchName(raw string) (string, error) {
	branch := strings.TrimSpace(raw)
	if branch == "" {
		return "", fmt.Errorf("branch is required")
	}
	cmd := exec.Command("git", "check-ref-format", "--branch", branch)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	return branch, nil
}

func loadGitContext(directory string) (gitContext, error) {
	root, err := runGitInDirectory(directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitContext{}, err
	}
	root = strings.TrimSpace(root)
	currentBranch, err := runGitInDirectory(directory, "branch", "--show-current")
	if err != nil {
		return gitContext{}, err
	}
	branchesOutput, err := runGitInDirectory(directory, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return gitContext{}, err
	}
	worktreeOutput, err := runGitInDirectory(root, "worktree", "list", "--porcelain")
	if err != nil {
		return gitContext{}, err
	}

	currentPath, _ := filepath.Abs(directory)
	worktrees := parseGitWorktrees(worktreeOutput, currentPath)
	branches := nonEmptyLines(branchesOutput)
	return gitContext{
		RepositoryRoot: root,
		CurrentBranch:  strings.TrimSpace(currentBranch),
		Worktrees:      worktrees,
		Branches:       branches,
	}, nil
}

func parseGitWorktrees(output, currentPath string) []gitWorktree {
	blocks := strings.Split(strings.TrimSpace(output), "\n\n")
	worktrees := make([]gitWorktree, 0, len(blocks))
	for _, block := range blocks {
		var item gitWorktree
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				item.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch refs/heads/"):
				item.Branch = strings.TrimPrefix(line, "branch refs/heads/")
			}
		}
		if item.Path == "" {
			continue
		}
		item.IsCurrent = sameResolvedPath(item.Path, currentPath)
		worktrees = append(worktrees, item)
	}
	return worktrees
}

func sameResolvedPath(lhs, rhs string) bool {
	left, leftErr := filepath.EvalSymlinks(lhs)
	right, rightErr := filepath.EvalSymlinks(rhs)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return filepath.Clean(lhs) == filepath.Clean(rhs)
}

func nonEmptyLines(output string) []string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func runGitInDirectory(directory string, args ...string) (string, error) {
	return runGitInDirectoryWith(directory, nil, args...)
}

// runGitInDirectoryWith is the single extended git runner. It preserves the legacy contract
// (git -C <resolved>, GIT_TERMINAL_PROMPT=0, combined stderr→error) and adds optional env
// override + context. Checkpoint capture uses this with a context timeout and a temp
// GIT_INDEX_FILE; legacy callers go through runGitInDirectory (zero opts).
func runGitInDirectoryWith(directory string, opts []gitRunOption, args ...string) (string, error) {
	resolved, err := expandPath(directory)
	if err != nil {
		return "", err
	}
	cfg := &gitRunConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	allArgs := append([]string{"-C", resolved}, args...)
	var cmd *exec.Cmd
	if cfg.ctx != nil {
		cmd = exec.CommandContext(cfg.ctx, "git", allArgs...)
	} else {
		cmd = exec.Command("git", allArgs...)
	}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if len(cfg.env) > 0 {
		env = append(env, cfg.env...)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("%s", message)
	}
	return stdout.String(), nil
}

// isGitWorkspace reports whether dir is inside a git working tree and returns the
// resolved repository root (rev-parse --show-toplevel). Any git error ⇒ unsupported,
// ok=false. Shared by checkpoint capture (checkpoint.go); workspace_diff.go and
// loadGitContext keep their existing toplevel calls (no forced migration — plan §6.1
// "don't force"). validateGitDirectory only checks dir exists, NOT git-ness; do not
// conflate.
func isGitWorkspace(dir string) (root string, ok bool) {
	root, err := runGitInDirectory(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(root), true
}

func gitWireError(code string, err error) *WireError {
	return &WireError{Code: code, Message: err.Error()}
}

// ── §7.1 PR 集成（GitHub-only） ────────────────────────────────────────────────────────

// snakeCaseRe matches one or more non-alnum/underscore chars for slug generation.
var snakeCaseRe = regexp.MustCompile(`[^a-z0-9]+`)
var branchSlugRe = regexp.MustCompile(`^cordcode/[a-z0-9][a-z0-9-]{0,60}$`)

func (h *Handlers) handleCreatePullRequest(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		Directory string `json:"directory"`
		Base      string `json:"base"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	if err := validateGitDirectory(params.Directory); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("invalid_directory", err))
		return
	}

	if _, err := detectGitHubRemote(params.Directory); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pr_not_supported", Message: err.Error()})
		return
	}
	if _, err := exec.LookPath("gh"); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pr_not_supported", Message: "gh CLI not found; install and authenticate gh first"})
		return
	}

	generator, ok := agent.(core.PrContentGenerator)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pr_content_generation_unsupported", Message: "current backend does not support PR content generation"})
		return
	}
	root, ok := isGitWorkspace(params.Directory)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "workspace_not_git", Message: "workspace is not a git repository"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	base := params.Base
	if base == "" {
		base = defaultPRBaseBranch(ctx, root)
	}
	head := strings.TrimSpace(runGitContext(ctx, root, "rev-parse", "--abbrev-ref", "HEAD"))
	commitSummary, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)}, "log", "--oneline", "-n", "50", base+"..HEAD")
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_diff_failed", err))
		return
	}
	diffSummary, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)}, "diff", "--stat", base+"...HEAD")
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_diff_failed", err))
		return
	}
	diffPatch, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)}, "diff", base+"...HEAD")
	if err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_diff_failed", err))
		return
	}
	template, _ := readPRTemplate(root)

	generated, err := generator.GeneratePrContent(ctx, core.PrContentInput{
		Cwd:           root,
		BaseBranch:    base,
		HeadBranch:    head,
		CommitSummary: limitPRString(commitSummary, 12_000),
		DiffSummary:   limitPRString(diffSummary, 12_000),
		DiffPatch:     limitPRString(diffPatch, 40_000),
		Template:      template,
	})
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pr_content_generation_failed", Message: err.Error()})
		return
	}

	title := strings.TrimSpace(generated.Title)
	if title == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "generated PR title is empty"})
		return
	}
	branchSlug := slugFromTitle(title)
	if !branchSlugRe.MatchString(branchSlug) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_branch_name", Message: "generated branch name " + branchSlug + " does not match whitelist"})
		return
	}

	currentBranch := head
	if currentBranch != branchSlug {
		if _, err := runGitInDirectory(params.Directory, "checkout", "-b", branchSlug); err != nil {
			if _, err2 := runGitInDirectory(params.Directory, "checkout", branchSlug); err2 != nil {
				conn.SendResult(msg.RequestID, nil, &WireError{Code: "git_checkout_failed", Message: "cannot switch to " + branchSlug + ": " + err.Error() + " / " + err2.Error()})
				return
			}
		}
	}

	if _, err := runGitInDirectory(params.Directory, "push", "-u", "origin", branchSlug); err != nil {
		conn.SendResult(msg.RequestID, nil, gitWireError("git_push_failed", err))
		return
	}

	ghArgs := []string{"pr", "create",
		"--title", title,
		"--body", generated.Body,
		"--head", branchSlug,
	}
	if base != "" {
		ghArgs = append(ghArgs, "--base", base)
	}
	cmd := exec.Command("gh", ghArgs...)
	cmd.Dir = params.Directory
	out, err := cmd.CombinedOutput()
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "gh_pr_create_failed", Message: string(out) + "; " + err.Error()})
		return
	}
	prURL := strings.TrimSpace(string(out))
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"pr_url": prURL,
		"branch": branchSlug,
		"base":   base,
	}, nil)
}

func runGitContext(ctx context.Context, dir string, args ...string) string {
	out, _ := runGitInDirectoryWith(dir, []gitRunOption{WithContext(ctx)}, args...)
	return out
}

func defaultPRBaseBranch(ctx context.Context, root string) string {
	out, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)}, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		branch := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "origin/"))
		if branch != "" {
			return branch
		}
	}
	return "main"
}

func limitPRString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[truncated]"
}

// handleCheckPullRequestSupport returns whether the given directory currently
// supports PR creation. It is the live, per-directory check iOS calls when
// opening the diff sheet, instead of relying on a cached hello_ack capability.
func (h *Handlers) handleCheckPullRequestSupport(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	if _, ok := agent.(core.PrContentGenerator); !ok {
		conn.SendResult(msg.RequestID, map[string]interface{}{"supported": false}, nil)
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"supported": supportsPullRequests(params.Directory),
	}, nil)
}

// slugFromTitle 把 PR 标题转成分支 slug：小写→去 PR 标题约定前缀 `[cordcode]`→
// 非 alnum 替换为 -→去首尾 -→截断 60 字符→去尾 -→加 cordcode/ 前缀。
// 去前缀让 plan §7.1 标题模板 `[cordcode] <summary>` 命中时，分支名不出现
// `cordcode/cordcode-...` 双前缀（PR 标题本身仍保留 [cordcode] 前缀，由调用方原样传 gh）。
func slugFromTitle(title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = strings.TrimPrefix(slug, "[cordcode]")
	slug = strings.TrimLeft(slug, " -")
	slug = snakeCaseRe.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = slug[:60]
	}
	slug = strings.TrimRight(slug, "-")
	return "cordcode/" + slug
}

// detectGitHubRemote 检查目录的 git remote origin 是否为 GitHub。
func detectGitHubRemote(directory string) (string, error) {
	out, err := runGitInDirectory(directory, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("no git remote origin found: %w", err)
	}
	url := strings.TrimSpace(out)
	lower := strings.ToLower(url)
	if !strings.Contains(lower, "github.com") {
		return "", fmt.Errorf("remote %s is not a GitHub repository; only GitHub PRs are supported", url)
	}
	return url, nil
}

// supportsPullRequests 检测当前环境是否支持 PR 创建（§7.1 capability 派生源）。
func supportsPullRequests(directory string) bool {
	if _, err := detectGitHubRemote(directory); err != nil {
		return false
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	return true
}

// ── §7.1 PULL_REQUEST_TEMPLATE.md 模板探测（GitHub-only PR 集成） ───────────────────────

// prTemplateMaxBytes 限制单个模板文件大小，避免超大文件撑大 PR body（plan §2.4 规则 1）。
const prTemplateMaxBytes = 64 * 1024

// prTemplateCandidates 返回固定、有序的模板发现相对路径（相对 repo root）。
// 顺序即优先级：先根目录，再 .github/。这里只返回**固定常量名**——不接受 wire payload
// 控制的路径段，因此 `..` 逃逸在结构上不可能（root 本身由 isGitWorkspace 用
// `git rev-parse --show-toplevel` 解析为受信任的绝对路径）。plan §2.3 v1 范围：只支持这两个
// 单文件位置；不支持 `.github/PULL_REQUEST_TEMPLATE/` 多模板目录、`docs/` 或 `?template=`。
func prTemplateCandidates() []string {
	return []string{
		"PULL_REQUEST_TEMPLATE.md",
		filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"),
	}
}

// readPRTemplate 发现并读取 root 下第一个存在的 PULL_REQUEST_TEMPLATE.md。
// 返回 (content, true) 当且仅当命中一个非空且 ≤ prTemplateMaxBytes 的模板。
// 缺失 / 空文件 / 读取错误 → 尝试下一个候选；超大（> prTemplateMaxBytes）→ 视为无模板
// 直接返回 false（plan §2.4 规则 1「超过上限视为无模板」+ §2.5-4「超大模板回退」）。
// 只读：绝不写工作区、不把模板写回 git。
func readPRTemplate(root string) (string, bool) {
	for _, rel := range prTemplateCandidates() {
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > prTemplateMaxBytes {
			return "", false
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if len(data) > prTemplateMaxBytes {
			return "", false
		}
		if strings.TrimSpace(string(data)) == "" {
			continue
		}
		return string(data), true
	}
	return "", false
}
