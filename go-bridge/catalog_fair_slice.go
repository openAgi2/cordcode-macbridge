package gobridge

// catalog_fair_slice.go 实现 root-only 全局 list_sessions 的公平切片：
//
//   按 recency 顺序，每个 directory 最多取 perDirLimit（K，默认 20）条；
//   总和再受 globalLimit（N，session-list-limit）硬顶。
//
// 刻意 **不做** 「总和 < N 时用全局 recency 回填」：回填会让热门目录再次吃光配额
// （owner 真机：Claude cordcode-ios 又回到 查看更多(78)）。深度历史走 directory-scoped
// 分页 + 侧栏「加载更多」（对齐 OpenCode 交互）。
//
// 仅用于 directory 为空的全局首页；directory-scoped 请求走普通分页。

import "strings"

// defaultSessionListPerDirectoryLimit 是全局首页每个 directory 的硬顶 K。
// 对齐 OpenCode 首页每项目 5 条 + 侧栏「加载更多」；深度靠 directory-scoped 分页。
const defaultSessionListPerDirectoryLimit = 5

// fairSliceSessionList 对已按 recency 排序（updatedAtMillis DESC）的 wire sessions 做
// 每目录 K 硬顶 + 全局 N 硬顶。perDirLimit<=0 → 默认 K；globalLimit<=0 → 不限制总数。
func fairSliceSessionList(sessions []map[string]interface{}, perDirLimit, globalLimit int) []map[string]interface{} {
	if len(sessions) == 0 {
		return sessions
	}
	if perDirLimit <= 0 {
		perDirLimit = defaultSessionListPerDirectoryLimit
	}
	if globalLimit <= 0 {
		globalLimit = len(sessions)
	}

	out := make([]map[string]interface{}, 0, fairMin(len(sessions), globalLimit))
	selected := make(map[string]struct{}, fairMin(len(sessions), globalLimit))
	perDir := make(map[string]int)

	for _, s := range sessions {
		if len(out) >= globalLimit {
			break
		}
		id, _ := s["id"].(string)
		if id == "" {
			continue
		}
		if _, ok := selected[id]; ok {
			continue
		}
		dir := sessionDirectoryKey(s)
		if perDir[dir] >= perDirLimit {
			continue
		}
		out = append(out, s)
		selected[id] = struct{}{}
		perDir[dir]++
	}
	return out
}

// fairHomeHasMore 报告公平首页之后是否仍有未返回的 session（任意目录被截断或总数更大）。
// 用于 hasMore：提示客户端可对具体 directory 发 scoped list 深挖，而不是盲翻全局 cursor。
func fairHomeHasMore(full, fair []map[string]interface{}, perDirLimit int) bool {
	if len(full) > len(fair) {
		return true
	}
	if perDirLimit <= 0 {
		perDirLimit = defaultSessionListPerDirectoryLimit
	}
	// 即使 len 相同（不应发生），也检查是否有目录在 full 中超过 K 而 fair 只取了 K。
	fullPerDir := make(map[string]int)
	fairPerDir := make(map[string]int)
	for _, s := range full {
		fullPerDir[sessionDirectoryKey(s)]++
	}
	for _, s := range fair {
		fairPerDir[sessionDirectoryKey(s)]++
	}
	for dir, n := range fullPerDir {
		if n > fairPerDir[dir] {
			return true
		}
		_ = perDirLimit // 文档锚点：K 是 fair 阶段顶；回填后 fairPerDir 可能 >K
	}
	return false
}

// sessionDirectoryKey 从 wire session 取 directory 分组键；空 directory 归为 "" 桶。
func sessionDirectoryKey(s map[string]interface{}) string {
	if s == nil {
		return ""
	}
	dir, _ := s["directory"].(string)
	return strings.TrimSpace(dir)
}

// packageFairHomePage 把公平切片打成与 paginateSessionList 同形的 wire result。
// 全局首页不发 nextCursor（深度分页走 directory 参数，避免 fair 与全局 cursor 语义冲突）。
//
// directoryTotals：full 快照里每个 directory 的真实总数（非 fair 切片数）。
// iOS 侧栏「查看更多(N)」用 total - 可见条数 显示剩余（owner 2026-08-11：恢复带数字样式）。
func packageFairHomePage(full []map[string]interface{}, perDirLimit, globalLimit int) map[string]interface{} {
	fair := fairSliceSessionList(full, perDirLimit, globalLimit)
	hasMore := fairHomeHasMore(full, fair, perDirLimit)
	return map[string]interface{}{
		"sessions":         fair,
		"hasMore":          hasMore,
		"directoryTotals":  directorySessionTotals(full),
	}
}

// directorySessionTotals 统计 full 列表中每个 directory 的 session 数。
func directorySessionTotals(sessions []map[string]interface{}) map[string]int {
	if len(sessions) == 0 {
		return map[string]int{}
	}
	totals := make(map[string]int, 16)
	for _, s := range sessions {
		totals[sessionDirectoryKey(s)]++
	}
	return totals
}

func fairMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
