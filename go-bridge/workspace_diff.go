package gobridge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type workspaceDiffFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Diff      string `json:"diff,omitempty"`
}

type workspaceDiffResult struct {
	Files     []workspaceDiffFile `json:"files"`
	Additions int                 `json:"additions"`
	Deletions int                 `json:"deletions"`
}

func (h *Handlers) handleGetWorkspaceDiff(conn Connection, msg WireMessage, agent core.Agent) {
	workDir := extractDir(msg)
	if workDir == "" {
		if wd, ok := agent.(core.WorkDirSwitcher); ok {
			workDir = wd.GetWorkDir()
		}
	}
	if workDir == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "workspace_missing", Message: "workspace directory is required"})
		return
	}

	result, err := loadWorkspaceDiff(context.Background(), workDir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "workspace_diff_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, result, nil)
}

func loadWorkspaceDiff(ctx context.Context, workDir string) (*workspaceDiffResult, error) {
	rootBytes, err := runGit(ctx, workDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("resolve git workspace: %w", err)
	}
	root := strings.TrimSpace(string(rootBytes))

	numstat, err := runGit(ctx, root, "diff", "--no-ext-diff", "--no-renames", "--numstat", "HEAD", "--")
	if err != nil {
		return nil, fmt.Errorf("read tracked diff: %w", err)
	}

	files := make(map[string]workspaceDiffFile)
	for _, line := range strings.Split(strings.TrimSuffix(string(numstat), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		additions, _ := strconv.Atoi(parts[0])
		deletions, _ := strconv.Atoi(parts[1])
		path := parts[2]
		diff, diffErr := runGit(ctx, root, "diff", "--no-ext-diff", "--no-renames", "--unified=3", "HEAD", "--", path)
		if diffErr != nil {
			return nil, fmt.Errorf("read diff for %s: %w", path, diffErr)
		}
		files[path] = workspaceDiffFile{
			Path:      path,
			Additions: additions,
			Deletions: deletions,
			Diff:      string(diff),
		}
	}

	untracked, err := runGit(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("read untracked files: %w", err)
	}
	for _, rawPath := range bytes.Split(bytes.TrimSuffix(untracked, []byte{0}), []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		path := string(rawPath)
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			return nil, fmt.Errorf("read untracked file %s: %w", path, readErr)
		}
		additions := countContentLines(content)
		diff := makeUntrackedDiff(path, content)
		files[path] = workspaceDiffFile{Path: path, Additions: additions, Diff: diff}
	}

	result := &workspaceDiffResult{Files: make([]workspaceDiffFile, 0, len(files))}
	for _, file := range files {
		result.Files = append(result.Files, file)
		result.Additions += file.Additions
		result.Deletions += file.Deletions
	}
	sortWorkspaceDiffFiles(result.Files)
	return result, nil
}

func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func countContentLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	count := bytes.Count(content, []byte{'\n'})
	if content[len(content)-1] != '\n' {
		count++
	}
	return count
}

func makeUntrackedDiff(path string, content []byte) string {
	var builder strings.Builder
	builder.WriteString("--- /dev/null\n+++ b/")
	builder.WriteString(path)
	builder.WriteString("\n@@ -0,0 +1,")
	builder.WriteString(strconv.Itoa(countContentLines(content)))
	builder.WriteString(" @@\n")
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		builder.WriteByte('+')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func sortWorkspaceDiffFiles(files []workspaceDiffFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}
