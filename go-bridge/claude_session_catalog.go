package gobridge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cccode-macbridge/core"
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
	Key          claudeSessionKey
	FilePath     string
	Directory    string
	Title        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	Fingerprint  claudeSessionFingerprint
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

	parseSummary func(string, time.Time) (string, time.Time, time.Time)
}

func newClaudeSessionCatalog(projectsDir string) *claudeSessionCatalog {
	return &claudeSessionCatalog{
		projectsDir:  projectsDir,
		parseSummary: scanClaudeSessionSummary,
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
		title, createdAt, updatedAt := c.parseSummary(candidate.path, candidate.modTime)
		metrics.AddMetadataParse(time.Since(parseStarted))
		nextByKey[candidate.key] = claudeSessionIndexEntry{
			Key:         candidate.key,
			FilePath:    candidate.path,
			Directory:   candidate.directory,
			Title:       title,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			Fingerprint: candidate.fingerprint,
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
	return &claudeSessionSnapshot{ByKey: nextByKey, Sorted: sortedEntries}
}

func claudeSessionEntryToWire(entry claudeSessionIndexEntry) map[string]interface{} {
	return map[string]interface{}{
		"id":              entry.Key.SessionID,
		"title":           entry.Title,
		"messageCount":    entry.MessageCount,
		"directory":       entry.Directory,
		"modifiedAt":      entry.UpdatedAt.Format(time.RFC3339),
		"updatedAtMillis": entry.UpdatedAt.UnixMilli(),
		"createdAtMillis": entry.CreatedAt.UnixMilli(),
	}
}
