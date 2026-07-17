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
}

type claudeSessionIndexEntry struct {
	Key             claudeSessionKey
	FilePath        string
	Directory       string
	Title           string
	// CustomTitle 来自 JSONL 的 type=custom-title 记录（assistant 文本回退的 Title 不算）。
	// 配合 FirstUserAt 用于检测 Claude Code fork 对：fork 时原会话开头被原样复制到新会话，
	// 因此 fork 对拥有相同的 CustomTitle + FirstUserAt。
	CustomTitle     string
	FirstUserAt     time.Time
	ModelID         string
	ProviderID      string
	ReasoningEffort string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	MessageCount    int
	Fingerprint     claudeSessionFingerprint
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
			candidates = append(candidates, fileCandidate{
				key: claudeSessionKey{
					ProjectKey: projectKey,
					SessionID:  strings.TrimSuffix(name, ".jsonl"),
				},
				path:      filepath.Join(projectPath, name),
				directory: realDirectory,
				fingerprint: claudeSessionFingerprint{
					ModTimeUnixNano: info.ModTime().UnixNano(),
					SizeBytes:       size,
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
			Key:             candidate.key,
			FilePath:        candidate.path,
			Directory:       candidate.directory,
			Title:           scan.Title,
			CustomTitle:     scan.CustomTitle,
			FirstUserAt:     scan.FirstUserAt,
			ModelID:         scan.ModelID,
			ProviderID:      scan.ProviderID,
			ReasoningEffort: scan.ReasoningEffort,
			CreatedAt:       scan.CreatedAt,
			UpdatedAt:       scan.UpdatedAt,
			Fingerprint:     candidate.fingerprint,
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
	return &claudeSessionSnapshot{ByKey: nextByKey, Sorted: sortedEntries}
}

// hideClaudeForkChildren 在已排序的会话列表里检测 Claude Code fork 对并隐藏较旧的分支。
// fork 配对条件（全部满足才配对，避免误伤）：
//  1. 同一 ProjectKey；
//  2. 双方都有非空 CustomTitle 且相等；
//  3. 双方 FirstUserAt 非零且相等。
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
	return wire
}
