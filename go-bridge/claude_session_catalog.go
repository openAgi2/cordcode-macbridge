// claude_session_catalog.go 是 Claude Code 的 session catalog 实现，属于「跨后端 Session Catalog
// 同源改造」（docs/2026-08-09-cross-backend-session-catalog-parity-implementation-plan.md §5.2）的
// **明确标注的 compatibility catalog**——不是 Claude Desktop / Claude Code 原生 catalog 同源。
//
// §5.2 诚实边界（honest boundary）：Claude Code CLI（claude --version 2.1.209）目前**没有**公开的
// session 列表 / catalog 子命令，也没有 `ccd_session_mgmt__list_sessions` 能力；该能力只存在于
// Claude Desktop 的私有 Electron 层（app.asar，§5.2 P0 #4 明确禁止复用）。因此 Claude 的 catalog
// 只能从 `~/.claude/projects/` 下的 JSONL transcript 文件派生（本文件做的事），与 codex thread/list、
// grok session/list、opencode /session 这些**后端原生 catalog API** 不同源。
//
// 这意味着（与已迁移 backend 的关键差异）：
//   - 本 catalog **不**走 catalog_cursor_epoch_v2 v2 主线（无原生 catalog → 无 pageV2 快照 → 无 v2
//     epoch cursor）。handlers.go 的 list_sessions dispatch 对 claudecode 没有 v2 分支（只有 codex /
//     grokbuild 在 ConnCatalogCursorEpochV2 时路由到各自 *HandleListSessions），即使连接声明了
//     catalog_cursor_epoch_v2，claudecode 也继续走 paginateSessionList 的 v1 盲切路径——这是「不
//     宣称 false parity」的结构保证（见 handlers_claude_catalog_guardrail_test.go）。
//   - 等 Claude Code 上游暴露稳定 catalog 接口后，再把 claude 迁移到原生同源主线（届时新增 v2
//     分支 + provider adapter + 定向测试），不要在本文件里伪造一致。
//
// upstream blocker 证据见 docs/2026-08-10-claude-catalog-supported-interface-investigation.md。
package gobridge

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type claudeSessionKey struct {
	ProjectKey string
	SessionID  string
}

type claudeSessionFingerprint struct {
	ModTimeUnixNano int64
	SizeBytes       int64
	// SidecarModTimeUnixNano tracks the .cc-connect-session-meta sidecar mtime.
	// Archive/unarchive only write the sidecar (the .jsonl transcript is
	// untouched), so without this the catalog's fingerprint match would reuse a
	// stale cached entry and ArchivedAt would never update.
	SidecarModTimeUnixNano int64
}

type claudeSessionIndexEntry struct {
	Key       claudeSessionKey
	FilePath  string
	Directory string
	Title     string
	// CustomTitle 来自 JSONL 的 type=custom-title 记录（assistant 文本回退的 Title 不算）。
	// 配合 FirstUserAt 用于检测 Claude Code fork 对：fork 时原会话开头被原样复制到新会话，
	// 因此 fork 对拥有相同的 CustomTitle + FirstUserAt。
	CustomTitle        string
	FirstUserAt        time.Time
	CompactBoundaryIDs []string
	ModelID            string
	ProviderID         string
	ReasoningEffort    string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	MessageCount       int
	ArchivedAt         time.Time
	Fingerprint        claudeSessionFingerprint
}

type claudeSessionSnapshot struct {
	ByKey  map[claudeSessionKey]claudeSessionIndexEntry
	Sorted []claudeSessionIndexEntry
}

type claudeSessionCatalog struct {
	projectsDir string

	mu       sync.Mutex
	snapshot *claudeSessionSnapshot
	inFlight chan struct{}

	parseSession func(string, time.Time) claudeSessionScanResult
}

func newClaudeSessionCatalog(projectsDir string) *claudeSessionCatalog {
	return &claudeSessionCatalog{
		projectsDir:  projectsDir,
		parseSession: scanClaudeSessionMetadata,
	}
}

func newDefaultClaudeSessionCatalog() *claudeSessionCatalog {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return newClaudeSessionCatalog("")
	}
	return newClaudeSessionCatalog(filepath.Join(homeDir, ".claude", "projects"))
}

func (c *claudeSessionCatalog) list(projectKey string, metrics *core.SessionLoadMetrics) []map[string]interface{} {
	snapshot := c.refresh(metrics)
	if snapshot == nil {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(snapshot.Sorted))
	for _, entry := range snapshot.Sorted {
		if projectKey != "" && entry.Key.ProjectKey != projectKey {
			continue
		}
		result = append(result, claudeSessionEntryToWire(entry))
	}
	return result
}

func (c *claudeSessionCatalog) refresh(metrics *core.SessionLoadMetrics) *claudeSessionSnapshot {
	c.mu.Lock()
	if c.inFlight != nil {
		if c.snapshot != nil {
			snapshot := c.snapshot
			c.mu.Unlock()
			return snapshot
		}
		inFlight := c.inFlight
		c.mu.Unlock()
		<-inFlight
		c.mu.Lock()
		snapshot := c.snapshot
		c.mu.Unlock()
		return snapshot
	}
	inFlight := make(chan struct{})
	c.inFlight = inFlight
	previous := c.snapshot
	c.mu.Unlock()

	next := c.buildSnapshot(previous, metrics)

	c.mu.Lock()
	if next != nil {
		c.snapshot = next
	}
	close(inFlight)
	c.inFlight = nil
	snapshot := c.snapshot
	c.mu.Unlock()
	return snapshot
}

func (c *claudeSessionCatalog) buildSnapshot(
	previous *claudeSessionSnapshot,
	metrics *core.SessionLoadMetrics,
) *claudeSessionSnapshot {
	if strings.TrimSpace(c.projectsDir) == "" {
		return nil
	}

	type fileCandidate struct {
		key         claudeSessionKey
		path        string
		directory   string
		fingerprint claudeSessionFingerprint
		modTime     time.Time
	}

	enumerateStarted := time.Now()
	projectDirs, err := os.ReadDir(c.projectsDir)
	if err != nil {
		metrics.RecordEnumeration(time.Since(enumerateStarted), 0, 0, 0)
		return nil
	}
	var candidates []fileCandidate
	var totalBytes int64
	var maxFileBytes int64
	for _, projectDir := range projectDirs {
		projectKey := projectDir.Name()
		if !projectDir.IsDir() || isHiddenProjectDir(projectKey) {
			continue
		}
		projectPath := filepath.Join(c.projectsDir, projectKey)
		realDirectory := resolveProjectRealDirectory(projectPath)
		if realDirectory == "" {
			realDirectory = projectKey
		}
		// Compatibility catalog ≈ Desktop visibility: hide ephemeral scratch / deleted
		// worktrees that Claude Desktop does not surface as top-level projects
		// (owner 2026-08-10: claude_aq_capture under /private/tmp, quirky worktree).
		if !claudeWorkspaceVisibleForCatalog(realDirectory) {
			continue
		}
		files, readErr := os.ReadDir(projectPath)
		if readErr != nil {
			continue
		}
		for _, file := range files {
			name := file.Name()
			if file.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			info, infoErr := file.Info()
			if infoErr != nil {
				continue
			}
			size := info.Size()
			totalBytes += size
			if size > maxFileBytes {
				maxFileBytes = size
			}
			// Include the sidecar mtime in the fingerprint so an archive/unarchive
			// (which only writes the sidecar) invalidates the cached entry and the
			// re-parse picks up the new ArchivedAt.
			var sidecarModNano int64
			if si, serr := os.Stat(claudeBridgeSessionSidecarPath(projectPath, strings.TrimSuffix(name, ".jsonl"))); serr == nil {
				sidecarModNano = si.ModTime().UnixNano()
			}
			candidates = append(candidates, fileCandidate{
				key: claudeSessionKey{
					ProjectKey: projectKey,
					SessionID:  strings.TrimSuffix(name, ".jsonl"),
				},
				path:      filepath.Join(projectPath, name),
				directory: realDirectory,
				fingerprint: claudeSessionFingerprint{
					ModTimeUnixNano:       info.ModTime().UnixNano(),
					SizeBytes:             size,
					SidecarModTimeUnixNano: sidecarModNano,
				},
				modTime: info.ModTime(),
			})
		}
	}
	metrics.RecordEnumeration(time.Since(enumerateStarted), len(candidates), totalBytes, maxFileBytes)

	compareStarted := time.Now()
	nextByKey := make(map[claudeSessionKey]claudeSessionIndexEntry, len(candidates))
	changed := 0
	for _, candidate := range candidates {
		if previous != nil {
			if cached, ok := previous.ByKey[candidate.key]; ok && cached.Fingerprint == candidate.fingerprint {
				nextByKey[candidate.key] = cached
				continue
			}
		}
		changed++
		parseStarted := time.Now()
		scan := c.parseSession(candidate.path, candidate.modTime)
		metrics.AddMetadataParse(time.Since(parseStarted))
			nextByKey[candidate.key] = claudeSessionIndexEntry{
				Key:                candidate.key,
				FilePath:           candidate.path,
				Directory:          candidate.directory,
				Title:              scan.Title,
				CustomTitle:        scan.CustomTitle,
				FirstUserAt:        scan.FirstUserAt,
				CompactBoundaryIDs: append([]string(nil), scan.CompactBoundaryIDs...),
				ModelID:            scan.ModelID,
				ProviderID:         scan.ProviderID,
				ReasoningEffort:    scan.ReasoningEffort,
				CreatedAt:          scan.CreatedAt,
				UpdatedAt:          scan.UpdatedAt,
				ArchivedAt:         scan.ArchivedAt,
				Fingerprint:        candidate.fingerprint,
			}
	}
	deleted := 0
	if previous != nil {
		for key := range previous.ByKey {
			if _, ok := nextByKey[key]; !ok {
				deleted++
			}
		}
	}
	metrics.RecordStatCompare(time.Since(compareStarted), changed, deleted, previous != nil && changed == 0 && deleted == 0)

	sortedEntries := make([]claudeSessionIndexEntry, 0, len(nextByKey))
	for _, entry := range nextByKey {
		sortedEntries = append(sortedEntries, entry)
	}
	sort.Slice(sortedEntries, func(i, j int) bool {
		if !sortedEntries[i].UpdatedAt.Equal(sortedEntries[j].UpdatedAt) {
			return sortedEntries[i].UpdatedAt.After(sortedEntries[j].UpdatedAt)
		}
		if sortedEntries[i].Key.ProjectKey != sortedEntries[j].Key.ProjectKey {
			return sortedEntries[i].Key.ProjectKey < sortedEntries[j].Key.ProjectKey
		}
		return sortedEntries[i].Key.SessionID < sortedEntries[j].Key.SessionID
	})

	// Claude Code fork 检测：同一 projectKey 下，custom-title 和首条用户消息 timestamp
	// 都相同的会话被视为 fork 对（/resume 或中断后续接会产生）。只保留最新的一条，
	// 较旧的从 ByKey 和 Sorted 同时移除，使 list_sessions / pin 解析等所有调用方一致。
	sortedEntries = hideClaudeForkChildren(nextByKey, sortedEntries)
	sortedEntries = hideClaudeCompactContinuationParents(nextByKey, sortedEntries)
	return &claudeSessionSnapshot{ByKey: nextByKey, Sorted: sortedEntries}
}

// hideClaudeCompactContinuationParents collapses the physical JSONL files
// Claude creates around compaction into one logical session. Parent and child
// carry the same compact_boundary UUID; this is stronger evidence than title
// or timestamp similarity and supports transitive multi-compaction chains.
func hideClaudeCompactContinuationParents(
	byKey map[claudeSessionKey]claudeSessionIndexEntry,
	sorted []claudeSessionIndexEntry,
) []claudeSessionIndexEntry {
	parent := make([]int, len(sorted))
	for index := range parent {
		parent[index] = index
	}
	var find func(int) int
	find = func(index int) int {
		if parent[index] != index {
			parent[index] = find(parent[index])
		}
		return parent[index]
	}
	union := func(lhs, rhs int) {
		leftRoot, rightRoot := find(lhs), find(rhs)
		if leftRoot != rightRoot {
			parent[rightRoot] = leftRoot
		}
	}

	type boundaryKey struct {
		ProjectKey string
		BoundaryID string
	}
	firstByBoundary := make(map[boundaryKey]int)
	for index, entry := range sorted {
		for _, boundaryID := range entry.CompactBoundaryIDs {
			boundaryID = strings.TrimSpace(boundaryID)
			if boundaryID == "" {
				continue
			}
			key := boundaryKey{ProjectKey: entry.Key.ProjectKey, BoundaryID: boundaryID}
			if first, exists := firstByBoundary[key]; exists {
				union(first, index)
			} else {
				firstByBoundary[key] = index
			}
		}
	}

	primaryByRoot := make(map[int]int)
	for index := range sorted {
		root := find(index)
		primary, exists := primaryByRoot[root]
		if !exists || sorted[index].UpdatedAt.After(sorted[primary].UpdatedAt) ||
			(sorted[index].UpdatedAt.Equal(sorted[primary].UpdatedAt) &&
				sorted[index].Key.SessionID > sorted[primary].Key.SessionID) {
			primaryByRoot[root] = index
		}
	}
	hide := make(map[int]bool)
	for index := range sorted {
		root := find(index)
		if primaryByRoot[root] == index {
			continue
		}
		// A singleton has itself as primary and never reaches this branch.
		hide[index] = true
		delete(byKey, sorted[index].Key)
	}
	if len(hide) == 0 {
		return sorted
	}
	result := make([]claudeSessionIndexEntry, 0, len(sorted)-len(hide))
	for index, entry := range sorted {
		if !hide[index] {
			result = append(result, entry)
		}
	}
	slog.Info("claude compact continuation detected: hiding physical parent transcripts",
		"hidden", len(hide))
	return result
}

// hideClaudeForkChildren 在已排序的会话列表里检测 Claude Code fork 对并隐藏较旧的分支。
// fork 配对条件（全部满足才配对，避免误伤）：
//  1. 同一 ProjectKey；
//  2. 双方都有非空 CustomTitle 且相等；
//  3. 双方 FirstUserAt 非零且相等。
//
// 同组多于一个时保留 UpdatedAt 最新的（primary），其余从 byKey 和 sorted 里删除。
// 同组 UpdatedAt 相同时按 SessionID 排序保留字典序最大者，保证确定性。
func hideClaudeForkChildren(byKey map[claudeSessionKey]claudeSessionIndexEntry, sorted []claudeSessionIndexEntry) []claudeSessionIndexEntry {
	type forkGroupKey struct {
		ProjectKey  string
		CustomTitle string
		FirstUserAt time.Time
	}
	groups := make(map[forkGroupKey][]int) // key -> indices into sorted
	for i, e := range sorted {
		ct := strings.TrimSpace(e.CustomTitle)
		if ct == "" || e.FirstUserAt.IsZero() {
			continue
		}
		k := forkGroupKey{ProjectKey: e.Key.ProjectKey, CustomTitle: ct, FirstUserAt: e.FirstUserAt}
		groups[k] = append(groups[k], i)
	}
	if len(groups) == 0 {
		return sorted
	}
	// 收集要隐藏的 index。sorted 已按 UpdatedAt DESC 排序，同组第一个就是 primary。
	hideIdx := make(map[int]struct{})
	hiddenTitles := make(map[string]int) // title -> 隐藏数量，仅用于日志
	for _, idxs := range groups {
		if len(idxs) < 2 {
			continue
		}
		// sorted 已按 UpdatedAt DESC（再按 ProjectKey、SessionID）排序。
		// primary = idxs[0]（最新）。但需处理 UpdatedAt 完全相等的情况：此时 sorted 的
		// 次序是 SessionID ASC，保留字典序最大者更稳定（即 idxs 的最后一个）。
		primary := idxs[0]
		if len(idxs) > 1 && sorted[idxs[0]].UpdatedAt.Equal(sorted[idxs[1]].UpdatedAt) {
			// UpdatedAt 相等：找 SessionID 最大的作为 primary
			primary = idxs[0]
			for _, idx := range idxs[1:] {
				if sorted[idx].Key.SessionID > sorted[primary].Key.SessionID {
					primary = idx
				}
			}
		}
		for _, idx := range idxs {
			if idx == primary {
				continue
			}
			hideIdx[idx] = struct{}{}
			hiddenTitles[sorted[idx].Title]++
			delete(byKey, sorted[idx].Key)
		}
	}
	if len(hideIdx) == 0 {
		return sorted
	}
	result := make([]claudeSessionIndexEntry, 0, len(sorted)-len(hideIdx))
	for i, e := range sorted {
		if _, hide := hideIdx[i]; hide {
			continue
		}
		result = append(result, e)
	}
	for title, n := range hiddenTitles {
		slog.Info("claude session fork detected: hiding older fork children",
			"title", title, "hidden", n)
	}
	return result
}

func claudeSessionEntryToWire(entry claudeSessionIndexEntry) map[string]interface{} {
	wire := map[string]interface{}{
		"id":              entry.Key.SessionID,
		"title":           entry.Title,
		"messageCount":    entry.MessageCount,
		"directory":       entry.Directory,
		"modifiedAt":      entry.UpdatedAt.Format(time.RFC3339),
		"updatedAtMillis": entry.UpdatedAt.UnixMilli(),
		"createdAtMillis": entry.CreatedAt.UnixMilli(),
	}
	if entry.ModelID != "" {
		wire["modelId"] = entry.ModelID
		wire["effectiveModelId"] = entry.ModelID
	}
	if entry.ProviderID != "" {
		wire["providerId"] = entry.ProviderID
		wire["effectiveProviderId"] = entry.ProviderID
	}
	if entry.ReasoningEffort != "" {
		wire["reasoningEffort"] = entry.ReasoningEffort
	}
	// Surface archivedAtMillis so clients can hide archived sessions (web
	// session-grouping filters on archivedAtMillis). Matches the agent-layer
	// wire shape in handlers.go sessionsToWire.
	if !entry.ArchivedAt.IsZero() {
		wire["archivedAtMillis"] = entry.ArchivedAt.UnixMilli()
	}
	return wire
}

// claudeWorkspaceVisibleForCatalog approximates Claude Desktop's public session list
// for this compatibility catalog (JSONL scan — not Desktop-native). Desktop does not
// surface ephemeral scratch workspaces as top-level projects; hide them so iOS does
// not show ghost groups like claude_aq_capture (/private/tmp) or deleted worktrees
// (…/.claude/worktrees/<name>).
//
// Rules (path-shape only — no Desktop private DB):
//  1. empty / encoded-key-only fallbacks → hide
//  2. Claude Code git worktree paths (…/.claude/worktrees/…) → hide
//  3. system temp roots (/tmp, /private/tmp, /var/tmp, /var/folders) → hide
//  4. absolute path that no longer exists as a directory → hide (deleted worktree/project)
func claudeWorkspaceVisibleForCatalog(directory string) bool {
	dir := strings.TrimSpace(directory)
	if dir == "" || dir == "." {
		return false
	}
	clean := filepath.Clean(dir)
	// Encoded project keys look like "-Users-jacklee-Projects-foo" (no real path).
	// Those only appear when resolveProjectRealDirectory failed; hide them.
	if !filepath.IsAbs(clean) {
		return false
	}
	if isClaudeWorktreePath(clean) {
		return false
	}
	if isSystemTempWorkspace(clean) {
		return false
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}

// isClaudeWorktreePath reports Claude Code's per-session git worktrees under
// <repo>/.claude/worktrees/<name>. Desktop does not list these as independent projects.
func isClaudeWorktreePath(dir string) bool {
	const marker = string(filepath.Separator) + ".claude" + string(filepath.Separator) + "worktrees" + string(filepath.Separator)
	if strings.Contains(dir, marker) {
		return true
	}
	suffix := string(filepath.Separator) + ".claude" + string(filepath.Separator) + "worktrees"
	return strings.HasSuffix(dir, suffix)
}

// isSystemTempWorkspace reports OS scratch roots. Capture fixtures and one-off probes
// often land under /private/tmp; Desktop does not list them alongside real projects.
//
// Note: /var/folders is intentionally NOT included — macOS unit tests and some app
// sandboxes live there; filtering it would hide legitimate fixtures and break CI.
func isSystemTempWorkspace(dir string) bool {
	prefixes := []string{
		"/private/tmp",
		"/var/tmp",
		"/tmp",
	}
	for _, p := range prefixes {
		if dir == p || strings.HasPrefix(dir, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
