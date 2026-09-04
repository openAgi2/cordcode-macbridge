package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// InspectTranscriptContinuity reads bounded head/tail windows. Compact
// continuation evidence is necessarily at the parent's tail and child's head,
// so listing sessions does not need to rescan arbitrarily large transcripts.
func InspectTranscriptContinuity(path string) TranscriptContinuity {
	info := TranscriptContinuity{
		Path:      path,
		SessionID: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
	}
	file, err := os.Open(path)
	if err != nil {
		return info
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return info
	}

	boundaries := make(map[string]struct{})
	scanClaudeContinuityWindow(file, 0, minInt64(stat.Size(), claudeContinuityWindowBytes), false, &info, boundaries)
	if stat.Size() > claudeContinuityWindowBytes {
		start := stat.Size() - claudeContinuityWindowBytes
		scanClaudeContinuityWindow(file, start, stat.Size()-start, true, &info, boundaries)
	}
	info.BoundaryIDs = make([]string, 0, len(boundaries))
	for boundaryID := range boundaries {
		info.BoundaryIDs = append(info.BoundaryIDs, boundaryID)
	}
	sort.Strings(info.BoundaryIDs)
	return info
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
