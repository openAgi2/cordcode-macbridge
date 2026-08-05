package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
