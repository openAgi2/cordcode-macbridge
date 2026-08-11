package gobridge

// codex_workspace_roots.go：从 Mac ChatGPT-Codex 本机状态读取「侧栏项目根」白名单。
//
// 权威字段（~/.codex/.codex-global-state.json）：
//   - electron-saved-workspace-roots: []string
//   - local-projects.*.rootPaths: []string
//
// iOS/bridge 的 Codex list_sessions 只展示落在这些 root 上的 session，并与 Mac 侧栏对齐
// （owner 2026-08-11：thread/list 全量 cwd 聚类会放出 /、bridge、已从 Mac 移除的工程）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const codexGlobalStateFileName = ".codex-global-state.json"

// codexRootsCacheTTL 避免每个 list_sessions 都读大 JSON；项目增删后最多延迟该时长生效。
const codexRootsCacheTTL = 5 * time.Second

type codexRootsCache struct {
	mu        sync.Mutex
	roots     []string
	loadedAt  time.Time
	filePath  string
	fileMTime time.Time
	fileSize  int64
}

var globalCodexRootsCache codexRootsCache

// loadCodexWorkspaceRootsFn 可在单测中替换（返回 nil = 不启用 roots 白名单，只做存在性/非工程过滤）。
var loadCodexWorkspaceRootsFn = loadCodexWorkspaceRoots

// loadCodexWorkspaceRoots 返回去重后的绝对路径列表（Clean）。失败返回 nil（调用方退化过滤）。
func loadCodexWorkspaceRoots() []string {
	return globalCodexRootsCache.get(codexGlobalStatePath())
}

// codexGlobalStatePath 默认 ~/.codex/.codex-global-state.json；CODex_HOME 可覆盖（测试）。
func codexGlobalStatePath() string {
	if override := strings.TrimSpace(os.Getenv("CORDCODE_CODEX_GLOBAL_STATE")); override != "" {
		return override
	}
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(home, ".codex")
	}
	return filepath.Join(home, codexGlobalStateFileName)
}

func (c *codexRootsCache) get(path string) []string {
	if path == "" {
		return nil
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	info, err := os.Stat(path)
	if err != nil {
		c.roots = nil
		c.loadedAt = now
		c.filePath = path
		c.fileMTime = time.Time{}
		c.fileSize = 0
		return nil
	}
	mtime := info.ModTime()
	size := info.Size()
	if path == c.filePath &&
		!c.loadedAt.IsZero() &&
		now.Sub(c.loadedAt) < codexRootsCacheTTL &&
		mtime.Equal(c.fileMTime) &&
		size == c.fileSize {
		return append([]string(nil), c.roots...)
	}

	roots := readCodexWorkspaceRootsFile(path)
	c.roots = roots
	c.loadedAt = now
	c.filePath = path
	c.fileMTime = mtime
	c.fileSize = size
	return append([]string(nil), roots...)
}

// readCodexWorkspaceRootsFile 解析 global-state；字段缺失时返回空切片（非 nil 若文件可读但无 roots）。
func readCodexWorkspaceRootsFile(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		SavedRoots    []string `json:"electron-saved-workspace-roots"`
		LocalProjects map[string]struct {
			RootPaths []string `json:"rootPaths"`
		} `json:"local-projects"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." {
			return
		}
		if !filepath.IsAbs(p) {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, r := range doc.SavedRoots {
		add(r)
	}
	for _, proj := range doc.LocalProjects {
		for _, r := range proj.RootPaths {
			add(r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		// 长路径优先（匹配时更具体）；稳定次要键。
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// resetCodexWorkspaceRootsCacheForTest 单测用。
func resetCodexWorkspaceRootsCacheForTest() {
	globalCodexRootsCache.mu.Lock()
	defer globalCodexRootsCache.mu.Unlock()
	globalCodexRootsCache.roots = nil
	globalCodexRootsCache.loadedAt = time.Time{}
	globalCodexRootsCache.filePath = ""
	globalCodexRootsCache.fileMTime = time.Time{}
	globalCodexRootsCache.fileSize = 0
}
