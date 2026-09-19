package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const claudeContinuityWindowBytes int64 = 512 * 1024

// TranscriptContinuity is the source-proven identity that links the transcript
// Claude closes at compaction with the continuation transcript it creates.
// The same compact_boundary UUID is written at the end of the parent and the
// beginning of the child.
type TranscriptContinuity struct {
	Path        string
	SessionID   string
	CustomTitle string
	CreatedAt   time.Time
	BoundaryIDs []string
}

// claudeContinuityCacheEntry 是按 path 缓存的最近一次连续性检查结果。指纹
// （size+mtime）不匹配即失效重读。**必须按 path 单键、指纹只做校验值**：
// 活跃 session 频繁追加即频繁换指纹，按指纹做键会让缓存条目随时间无界增长。
//
// 效果边界（评审 B4，诚实表述）：本缓存消除的是**同一进程内未变化文件的
// 重复头尾扫描**（warm 收敛）。冷启动首轮、以及指纹变化后的首次访问仍要
// 真实读盘（cold miss，每指纹一次——并发 miss 由 flights 合并为一次，见
// claudeContinuityFlight）；进程冷启动后的第一次 catalog/rich-history
// 扫描仍会遍历项目目录全部 JSONL。缓存有严格容量上限 + FIFO 逐出，条目数
// 不随历史访问过的路径（删除/重命名/迁移后的残留）无界增长。
type claudeContinuityCacheEntry struct {
	size    int64
	modNano int64
	info    TranscriptContinuity
}

// claudeContinuityCacheDefaultCapacity：619 个现存 transcript 留 >6x 余量；
// 逐出只是退化为重读（自愈），代价可接受。
const claudeContinuityCacheDefaultCapacity = 4096

// claudeContinuityFlight 合并同一 (path, size, mtime) 指纹的并发 cold miss
// （评审 R2-B2）：第一个到达者成为 leader 真实读盘，并发等待者在 done 关闭
// 后复用同一结果，同一指纹恰好一次真实读。键含完整指纹而不是只含 path——
// 扫描期间文件被追加时，新指纹的调用者立即开自己的 flight，不被旧指纹
// 阻塞；也避免持全局 mutex 扫盘把不同 transcript 的 cold seed 串行化。
type claudeContinuityFlight struct {
	done chan struct{}
	info TranscriptContinuity
}

func claudeContinuityFlightKey(path string, size, modNano int64) string {
	return fmt.Sprintf("%s\x00%d\x00%d", path, size, modNano)
}

type claudeContinuityCacheStruct struct {
	mu       sync.Mutex
	entries  map[string]*claudeContinuityCacheEntry
	flights  map[string]*claudeContinuityFlight
	order    []string // FIFO 逐出序（插入顺序）
	capacity int
}

var claudeContinuityCache = &claudeContinuityCacheStruct{
	entries:  make(map[string]*claudeContinuityCacheEntry),
	flights:  make(map[string]*claudeContinuityFlight),
	capacity: claudeContinuityCacheDefaultCapacity,
}

// claudeContinuityFileReads 统计真实读盘次数（cold miss）；当前仅测试用它
// 区分 warm hit（计数不变）与 cold miss（计数递增）。
var claudeContinuityFileReads atomic.Int64

func resetClaudeContinuityCacheForTest() {
	claudeContinuityCache = &claudeContinuityCacheStruct{
		entries:  make(map[string]*claudeContinuityCacheEntry),
		flights:  make(map[string]*claudeContinuityFlight),
		capacity: claudeContinuityCacheDefaultCapacity,
	}
	claudeContinuityFileReads.Store(0)
}

func setClaudeContinuityCacheCapacityForTest(capacity int) {
	claudeContinuityCache.mu.Lock()
	defer claudeContinuityCache.mu.Unlock()
	claudeContinuityCache.capacity = capacity
}

func (c *claudeContinuityCacheStruct) put(path string, size, modNano int64, info TranscriptContinuity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[path]; !exists {
		c.order = append(c.order, path)
		for len(c.order) > c.capacity {
			delete(c.entries, c.order[0])
			c.order[0] = "" // 释放被逐出 path 的字符串引用
			c.order = c.order[1:]
		}
	}
	c.entries[path] = &claudeContinuityCacheEntry{size: size, modNano: modNano, info: info}
}

// claudeContinuityClone 解除返回值与缓存内部切片的 alias（评审 N1）：
// InspectTranscriptContinuity 是跨包 exported API，调用方原地修改不得污染
// 全局缓存。Boundary ID 数量很小，这点 copy 不会重新制造解析波。
func claudeContinuityClone(info TranscriptContinuity) TranscriptContinuity {
	clone := info
	clone.BoundaryIDs = append([]string(nil), info.BoundaryIDs...)
	return clone
}

// InspectTranscriptContinuity reads bounded head/tail windows, cached per
// (size, mtime) fingerprint. Compact continuation evidence is necessarily at
// the parent's tail and child's head, so listing sessions does not need to
// rescan arbitrarily large transcripts. Concurrent misses of the same
// fingerprint merge into a single real read (claudeContinuityFlight).
func InspectTranscriptContinuity(path string) TranscriptContinuity {
	info := TranscriptContinuity{
		Path:      path,
		SessionID: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
	}
	stat, err := os.Stat(path)
	if err != nil {
		return info
	}
	size, modNano := stat.Size(), stat.ModTime().UnixNano()

	// 缓存命中与 flight 查找必须在同一临界区内判定：leader 的 put 与
	// flight 删除之间不存在「两者都 miss」的窗口，等待者不会重复扫盘。
	claudeContinuityCache.mu.Lock()
	if entry, ok := claudeContinuityCache.entries[path]; ok && entry.size == size && entry.modNano == modNano {
		cached := entry.info
		claudeContinuityCache.mu.Unlock()
		return claudeContinuityClone(cached)
	}
	key := claudeContinuityFlightKey(path, size, modNano)
	if flight, ok := claudeContinuityCache.flights[key]; ok {
		claudeContinuityCache.mu.Unlock()
		<-flight.done
		return claudeContinuityClone(flight.info)
	}
	flight := &claudeContinuityFlight{done: make(chan struct{})}
	claudeContinuityCache.flights[key] = flight
	claudeContinuityCache.mu.Unlock()

	claudeContinuityFileReads.Add(1)
	if file, err := os.Open(path); err == nil {
		boundaries := make(map[string]struct{})
		scanClaudeContinuityWindow(file, 0, minInt64(size, claudeContinuityWindowBytes), false, &info, boundaries)
		if size > claudeContinuityWindowBytes {
			start := size - claudeContinuityWindowBytes
			scanClaudeContinuityWindow(file, start, size-start, true, &info, boundaries)
		}
		info.BoundaryIDs = make([]string, 0, len(boundaries))
		for boundaryID := range boundaries {
			info.BoundaryIDs = append(info.BoundaryIDs, boundaryID)
		}
		sort.Strings(info.BoundaryIDs)
		claudeContinuityCache.put(path, size, modNano, info)
		file.Close()
	}

	// 读盘失败（open 出错）不写缓存：flight 照常完成，等待者拿到与自行
	// 失败一致的空结果；下一个调用者重新真实读盘（自愈），不缓存假阴性。
	claudeContinuityCache.mu.Lock()
	delete(claudeContinuityCache.flights, key)
	flight.info = info
	claudeContinuityCache.mu.Unlock()
	close(flight.done)
	return claudeContinuityClone(info)
}

func scanClaudeContinuityWindow(
	file *os.File,
	start, length int64,
	skipPartialFirstLine bool,
	info *TranscriptContinuity,
	boundaries map[string]struct{},
) {
	if length <= 0 {
		return
	}
	reader := bufio.NewReader(io.NewSectionReader(file, start, length))
	if skipPartialFirstLine {
		_, _ = reader.ReadString('\n')
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), int(claudeContinuityWindowBytes))
	for scanner.Scan() {
		var envelope struct {
			Type        string `json:"type"`
			Subtype     string `json:"subtype"`
			UUID        string `json:"uuid"`
			Timestamp   string `json:"timestamp"`
			CustomTitle string `json:"customTitle"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil {
			continue
		}
		if info.CustomTitle == "" && isClaudeCustomTitleRecord(envelope.Type) {
			info.CustomTitle = strings.TrimSpace(envelope.CustomTitle)
		}
		if info.CreatedAt.IsZero() && envelope.Timestamp != "" {
			if timestamp, err := time.Parse(time.RFC3339Nano, envelope.Timestamp); err == nil {
				info.CreatedAt = timestamp
			}
		}
		if envelope.Type == "system" && envelope.Subtype == "compact_boundary" {
			if boundaryID := strings.TrimSpace(envelope.UUID); boundaryID != "" {
				boundaries[boundaryID] = struct{}{}
			}
		}
	}
}

func minInt64(lhs, rhs int64) int64 {
	if lhs < rhs {
		return lhs
	}
	return rhs
}

// resolveClaudeContinuationPaths returns the connected component containing
// sessionID, ordered parent-first. Membership requires a shared source UUID;
// matching titles alone never stitches unrelated conversations.
func resolveClaudeContinuationPaths(projectDir, sessionID string) []string {
	currentPath := filepath.Join(projectDir, sessionID+".jsonl")
	current := InspectTranscriptContinuity(currentPath)
	if len(current.BoundaryIDs) == 0 {
		return []string{currentPath}
	}

	dirEntries, err := os.ReadDir(projectDir)
	if err != nil {
		return []string{currentPath}
	}
	infos := make([]TranscriptContinuity, 0)
	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() || !strings.HasSuffix(dirEntry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, dirEntry.Name())
		continuity := InspectTranscriptContinuity(path)
		if len(continuity.BoundaryIDs) > 0 {
			infos = append(infos, continuity)
		}
	}

	component := map[string]bool{currentPath: true}
	knownBoundaries := make(map[string]bool)
	for _, boundaryID := range current.BoundaryIDs {
		knownBoundaries[boundaryID] = true
	}
	for changed := true; changed; {
		changed = false
		for _, continuity := range infos {
			if component[continuity.Path] || !sharesClaudeBoundary(continuity.BoundaryIDs, knownBoundaries) {
				continue
			}
			component[continuity.Path] = true
			for _, boundaryID := range continuity.BoundaryIDs {
				knownBoundaries[boundaryID] = true
			}
			changed = true
		}
	}

	ordered := make([]TranscriptContinuity, 0, len(component))
	for _, continuity := range infos {
		if component[continuity.Path] {
			ordered = append(ordered, continuity)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			if ordered[i].CreatedAt.IsZero() {
				return false
			}
			if ordered[j].CreatedAt.IsZero() {
				return true
			}
			return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
		}
		return ordered[i].Path < ordered[j].Path
	})
	paths := make([]string, 0, len(ordered))
	for _, continuity := range ordered {
		paths = append(paths, continuity.Path)
	}
	if len(paths) == 0 {
		return []string{currentPath}
	}
	return paths
}

func sharesClaudeBoundary(boundaryIDs []string, known map[string]bool) bool {
	for _, boundaryID := range boundaryIDs {
		if known[boundaryID] {
			return true
		}
	}
	return false
}

func loadClaudeContinuationHistory(projectDir, sessionID string) ([]core.RichHistoryEntry, int64, error) {
	paths := resolveClaudeContinuationPaths(projectDir, sessionID)
	segments := make([]core.TranscriptSourceSegment, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, 0, err
		}
		segments = append(segments, core.TranscriptSourceSegment{
			Identity: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
			Path:     path,
			Cursor:   info.Size(),
		})
	}
	return loadClaudeContinuationHistoryAtSegments(context.Background(), segments)
}

func loadClaudeContinuationHistoryAtSegments(
	ctx context.Context,
	segments []core.TranscriptSourceSegment,
) ([]core.RichHistoryEntry, int64, error) {
	merged := make([]core.RichHistoryEntry, 0)
	positions := make(map[string]int)
	var totalBytes int64
	for _, segment := range segments {
		if err := ctx.Err(); err != nil {
			return nil, totalBytes, err
		}
		if segment.Cursor < 0 {
			return nil, totalBytes, io.ErrUnexpectedEOF
		}
		file, err := os.Open(segment.Path)
		if err != nil {
			return nil, totalBytes, err
		}
		entries, parseErr := LoadClaudeRichHistoryFromReader(
			io.LimitReader(file, segment.Cursor),
			segment.Path,
		)
		closeErr := file.Close()
		if parseErr != nil {
			return nil, totalBytes, parseErr
		}
		if closeErr != nil {
			return nil, totalBytes, closeErr
		}
		totalBytes += segment.Cursor
		for _, entry := range entries {
			key := claudeHistoryDedupKey(entry)
			if index, exists := positions[key]; exists {
				// The continuation copy may contain a more complete tool result.
				// Replace in place so chronology remains anchored to the parent.
				merged[index] = entry
				continue
			}
			positions[key] = len(merged)
			merged = append(merged, entry)
		}
	}
	return merged, totalBytes, nil
}

func richHistoryTranscriptSegments(projectDir, sessionID string) ([]core.TranscriptSourceSegment, error) {
	paths := resolveClaudeContinuationPaths(projectDir, sessionID)
	segments := make([]core.TranscriptSourceSegment, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		segments = append(segments, core.TranscriptSourceSegment{
			Identity: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
			Path:     path,
		})
	}
	return segments, nil
}

func resolveClaudeHistoryProjectDir(homeDir, preferredWorkDir, sessionID string) string {
	preferred := findProjectDir(homeDir, preferredWorkDir)
	if preferred != "" {
		if _, err := os.Stat(filepath.Join(preferred, sessionID+".jsonl")); err == nil {
			return preferred
		}
	}
	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	dirEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}
	for _, dirEntry := range dirEntries {
		if !dirEntry.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsDir, dirEntry.Name())
		if _, err := os.Stat(filepath.Join(candidate, sessionID+".jsonl")); err == nil {
			return candidate
		}
	}
	return ""
}

func claudeHistoryDedupKey(entry core.RichHistoryEntry) string {
	id := strings.TrimSpace(entry.ID)
	if id != "" &&
		!strings.HasPrefix(id, "assistant-line-") &&
		!strings.HasPrefix(id, "user-line-") &&
		!strings.HasPrefix(id, "compact-boundary-line-") {
		return entry.Role + "\x00id\x00" + id
	}
	return entry.Role + "\x00content\x00" + entry.Timestamp.UTC().Format(time.RFC3339Nano) + "\x00" + entry.Content
}
