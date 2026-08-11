package gobridge

// catalog_workspace_filter.go：catalog 列表侧「workspace 是否仍可见」的共享规则。
//
// Mac Codex / Claude Desktop 不会把 cwd 已物理删除的 session 当成正常项目展示；
// 上游 thread/list 或磁盘 transcript 仍可能残留这些条目。bridge 在下发 list_sessions
// 前过滤，避免 iOS 侧栏出现 cccode-ios / cccode-macbridge 这类幽灵目录
// （owner 2026-08-11：目录已删、Mac Codex App 不显示，iOS 仍显示）。
//
// Codex 另有一层「项目白名单」：Mac ChatGPT-Codex 侧栏只展示
// ~/.codex/.codex-global-state.json 里 saved workspace roots / local-projects 登记的工程，
// 不会把 cwd=/、历史测试目录、从未加入项目列表的路径当成项目
// （owner 2026-08-11：/、bridge、opencode-cc-connect 等）。

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// filterSessionsMissingWorkspace 去掉 directory 为空、或绝对路径已不存在/非目录的 session。
// 相对路径（罕见）保留，避免误伤测试 fixture。
// Grok 等与 Codex 项目白名单无关的 backend 用此函数。
func filterSessionsMissingWorkspace(sessions []map[string]interface{}) []map[string]interface{} {
	return filterCatalogSessionsByVisibility(sessions, nil, false)
}

// filterCodexCatalogSessions 对齐 Mac Codex 侧栏可见集：
//  1. 路径存在性 / 非工程路径硬拒绝（/、home 单独作为「仅精确匹配」、temp…）；
//  2. 若能读到 Codex saved workspace roots：只保留 cwd 命中某 root 的 session，
//     并把 directory 规范成该 root（子路径归入父项目，避免 bridge 子目录单独成组）；
//  3. roots 不可用时退化为 (1)，不静默伪造空列表。
func filterCodexCatalogSessions(sessions []map[string]interface{}) []map[string]interface{} {
	roots := loadCodexWorkspaceRootsFn()
	return filterCatalogSessionsByVisibility(sessions, roots, true)
}

// filterCatalogSessionsByVisibility 共享过滤管线。
// codexRoots 非空且 requireCodexRoots 时启用项目白名单 + directory 归一。
func filterCatalogSessionsByVisibility(sessions []map[string]interface{}, codexRoots []string, requireCodexRoots bool) []map[string]interface{} {
	if len(sessions) == 0 {
		return sessions
	}
	out := make([]map[string]interface{}, 0, len(sessions))
	droppedByBase := map[string]int{}
	useRoots := requireCodexRoots && len(codexRoots) > 0
	for _, s := range sessions {
		dir := sessionDirectoryKey(s)
		clean, ok := normalizeCatalogDirectory(dir)
		if !ok || !sessionWorkspaceExistsForCatalog(clean) {
			recordDroppedBase(droppedByBase, dir)
			continue
		}
		// 非工程硬拒绝：/、过浅系统路径、temp（与是否 Codex roots 无关）。
		if isNonProjectCatalogPath(clean) {
			recordDroppedBase(droppedByBase, clean)
			continue
		}
		if useRoots {
			root, matched := matchCodexWorkspaceRoot(clean, codexRoots)
			if !matched {
				recordDroppedBase(droppedByBase, clean)
				continue
			}
			// 归一到项目 root，侧栏与 Mac 同组（…/opencodeIosNew/bridge 不会单独成「bridge」组）。
			if root != "" {
				s["directory"] = root
			}
		}
		out = append(out, s)
	}
	if len(droppedByBase) > 0 {
		names := make([]string, 0, len(droppedByBase))
		for name := range droppedByBase {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		total := 0
		for _, name := range names {
			n := droppedByBase[name]
			total += n
			parts = append(parts, name+"="+strconv.Itoa(n))
		}
		slog.Info("catalog workspace filter dropped sessions",
			"dropped_count", total,
			"kept_count", len(out),
			"raw_count", len(sessions),
			"codex_roots", len(codexRoots),
			"codex_roots_enforced", useRoots,
			"dropped_basenames", strings.Join(parts, ","),
		)
	}
	return out
}

func recordDroppedBase(dst map[string]int, dir string) {
	base := filepath.Base(strings.TrimSpace(dir))
	if base == "" || base == "." || dir == "/" {
		base = "(empty-or-root)"
	}
	dst[base]++
}

// sessionWorkspaceVisibleForCatalog 兼容旧测试名：存在性 + 非工程路径拒绝（不含 Codex roots）。
func sessionWorkspaceVisibleForCatalog(directory string) bool {
	clean, ok := normalizeCatalogDirectory(directory)
	if !ok {
		return false
	}
	if isNonProjectCatalogPath(clean) {
		return false
	}
	return sessionWorkspaceExistsForCatalog(clean)
}

// normalizeCatalogDirectory 展开 ~、Clean；空/. 返回 false。
// 相对路径返回 clean 且 ok=true（存在性检查跳过，由 exists 函数处理）。
func normalizeCatalogDirectory(directory string) (string, bool) {
	dir := strings.TrimSpace(directory)
	if dir == "" || dir == "." {
		return "", false
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	} else if dir == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	}
	return filepath.Clean(dir), true
}

// sessionWorkspaceExistsForCatalog：绝对路径必须仍是目录；相对路径放行（测试 fixture）。
func sessionWorkspaceExistsForCatalog(clean string) bool {
	if clean == "" {
		return false
	}
	if !filepath.IsAbs(clean) {
		return true
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}

// isNonProjectCatalogPath 拒绝 Mac Codex 不会当「项目」展示的路径形状。
// 注意：用户 home 目录本身可以是 local-projects 里的一个 root（name=jacklee），
// 不在这里一刀切；由 matchCodexWorkspaceRoot 对 home 只允许精确匹配。
//
// 刻意 **不** 整段封禁 /var/folders：macOS 上 t.TempDir 与部分合法路径也在其下；
// 只拦明确的系统根与 /tmp 系（历史 phase* 测试 cwd 落在 /private/tmp/...）。
func isNonProjectCatalogPath(clean string) bool {
	if clean == "" || clean == "." {
		return true
	}
	// 文件系统根与常见挂载根：绝不当项目。
	switch clean {
	case "/", "/Users", "/home", "/private", "/var", "/tmp", "/private/tmp":
		return true
	}
	// 明确的系统临时目录前缀（不含通用 /var/folders，避免误伤）。
	if clean == "/tmp" || strings.HasPrefix(clean, "/tmp/") ||
		clean == "/private/tmp" || strings.HasPrefix(clean, "/private/tmp/") {
		return true
	}
	return false
}

// matchCodexWorkspaceRoot 找最长 saved root 覆盖 cwd。
// - 精确匹配任意 root（含 home）→ 命中；
// - 前缀匹配（cwd 在 root 下）仅当 root 不是用户 home：避免 /Users/jacklee 吞掉所有子工程，
//   把 opencode-cc-connect 错误归到「jacklee」组（Mac 侧栏也不会把未登记工程挂出来）。
func matchCodexWorkspaceRoot(cwd string, roots []string) (root string, ok bool) {
	if cwd == "" || len(roots) == 0 {
		return "", false
	}
	home, _ := os.UserHomeDir()
	home = filepath.Clean(home)

	// 最长匹配优先。
	best := ""
	for _, r := range roots {
		r = filepath.Clean(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if cwd == r {
			if len(r) >= len(best) {
				best = r
			}
			continue
		}
		// home 只允许精确匹配。
		if home != "" && r == home {
			continue
		}
		prefix := r + string(os.PathSeparator)
		if strings.HasPrefix(cwd, prefix) && len(r) >= len(best) {
			best = r
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}
