package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cccode-macbridge/core"
)

const codexSessionScannerMaxTokenSize = 64 * 1024 * 1024
const codexSessionListPrefixBytes int64 = 256 * 1024

// resolveCodexHomeDir returns the effective CODEX_HOME directory.
// Priority: explicit config value > CODEX_HOME env > ~/.codex
func resolveCodexHomeDir(explicit string) string {
	if h := strings.TrimSpace(explicit); h != "" {
		return h
	}
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".codex")
}

// ── Session list 缓存 ──────────────────────────────────────────────────────────

// fileEntry 缓存单个 JSONL 文件解析结果和 mtime
type fileEntry struct {
	mtime time.Time
	size  int64
	info  core.AgentSessionInfo
}

type sessionListCache struct {
	mu     sync.Mutex
	files  map[string]*fileEntry   // abs filepath → 解析结果 + mtime
	sorted []core.AgentSessionInfo // 排好序的快照，缓存命中时直接返回
}

// list 返回 session 列表。只增量重解析 mtime 变了的文件。
func (c *sessionListCache) list(ctx context.Context, codexHome string) ([]core.AgentSessionInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sessionsDir := filepath.Join(resolveCodexHomeDir(codexHome), "sessions")
	metrics := core.SessionLoadMetricsFromContext(ctx)

	// Phase 1: walk 目录收集所有 .jsonl path + mtime（只 stat，不读文件内容）
	type walkEntry struct {
		path  string
		mtime time.Time
		size  int64
	}
	var walked []walkEntry
	var totalBytes int64
	var maxFileBytes int64
	enumerateStarted := time.Now()
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		size := info.Size()
		walked = append(walked, walkEntry{path: path, mtime: info.ModTime(), size: size})
		totalBytes += size
		if size > maxFileBytes {
			maxFileBytes = size
		}
		return nil
	})
	metrics.RecordEnumeration(time.Since(enumerateStarted), len(walked), totalBytes, maxFileBytes)

	if len(walked) == 0 {
		c.files = nil
		c.sorted = nil
		return nil, nil
	}

	// Phase 2: 对比缓存，找出新增/变更/删除的文件
	compareStarted := time.Now()
	currentSet := make(map[string]walkEntry, len(walked))
	for _, w := range walked {
		currentSet[w.path] = w
	}

	var changed []walkEntry
	for _, w := range walked {
		if cached, ok := c.files[w.path]; !ok || !cached.mtime.Equal(w.mtime) || cached.size != w.size {
			changed = append(changed, w)
		}
	}

	deleted := 0
	for path := range c.files {
		if _, ok := currentSet[path]; !ok {
			deleted++
		}
	}

	// 完全命中：零开销直接返回
	if c.files != nil && len(changed) == 0 && deleted == 0 {
		metrics.RecordStatCompare(time.Since(compareStarted), 0, 0, true)
		return cloneSessionInfos(c.sorted), nil
	}
	metrics.RecordStatCompare(time.Since(compareStarted), len(changed), deleted, false)

	slog.Debug("codex: session cache incremental update", "total", len(walked), "changed", len(changed), "deleted", deleted)

	// Phase 3: 增量更新缓存
	if c.files == nil {
		c.files = make(map[string]*fileEntry, len(walked))
	}

	// 删除不再存在的文件
	for path := range c.files {
		if _, ok := currentSet[path]; !ok {
			delete(c.files, path)
		}
	}

	// 解析变更的文件
	parseStarted := time.Now()
	for _, w := range changed {
		info := parseCodexSessionFile(w.path)
		if info != nil {
			c.files[w.path] = &fileEntry{mtime: w.mtime, size: w.size, info: *info}
		} else {
			delete(c.files, w.path)
		}
	}
	metrics.AddMetadataParse(time.Since(parseStarted))

	// Phase 4: 重建排序列表
	sessions := make([]core.AgentSessionInfo, 0, len(c.files))
	for _, entry := range c.files {
		sessions = append(sessions, entry.info)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if !sessions[i].ModifiedAt.Equal(sessions[j].ModifiedAt) {
			return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
		}
		return sessions[i].ID < sessions[j].ID
	})
	c.sorted = sessions

	return cloneSessionInfos(c.sorted), nil
}

func cloneSessionInfos(sessions []core.AgentSessionInfo) []core.AgentSessionInfo {
	if len(sessions) == 0 {
		return nil
	}
	out := make([]core.AgentSessionInfo, len(sessions))
	copy(out, sessions)
	return out
}

// parseCodexSessionFile reads a Codex JSONL transcript.
func parseCodexSessionFile(path string) *core.AgentSessionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil
	}

	var sessionID string
	var sessionCwd string
	var summary string
	var msgCount int
	userMsgSeen := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), codexSessionScannerMaxTokenSize)

	if scanner.Scan() {
		parseCodexSessionMetadata(scanner.Bytes(), &sessionID, &sessionCwd)
	}

	if _, err := f.Seek(0, 0); err != nil {
		return nil
	}
	if stat.Size() > codexSessionListPrefixBytes {
		scanner = bufio.NewScanner(io.LimitReader(f, codexSessionListPrefixBytes))
	} else {
		scanner = bufio.NewScanner(f)
	}
	scanner.Buffer(make([]byte, 256*1024), codexSessionScannerMaxTokenSize)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "session_meta":
			parseCodexSessionMetadata(scanner.Bytes(), &sessionID, &sessionCwd)

		case "response_item":
			var item struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(entry.Payload, &item) == nil {
				if item.Role == "user" {
					userMsgSeen++
					msgCount++
					// The actual user prompt is the last user response_item
					// (earlier ones are system/AGENTS.md instructions).
					// Pick the last content block that looks like a real prompt.
					for _, c := range item.Content {
						if c.Type == "input_text" && c.Text != "" && isUserPrompt(c.Text) {
							summary = c.Text
						}
					}
				} else if item.Role == "assistant" {
					msgCount++
				}
			}
		}
	}

	if sessionID == "" {
		return nil
	}

	if len([]rune(summary)) > 60 {
		summary = string([]rune(summary)[:60]) + "..."
	}

	return &core.AgentSessionInfo{
		ID:           sessionID,
		Summary:      summary,
		MessageCount: msgCount,
		ModifiedAt:   stat.ModTime(),
		Directory:    sessionCwd,
	}
}

func parseCodexSessionMetadata(line []byte, sessionID, sessionCwd *string) {
	var entry struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "session_meta" {
		return
	}
	var meta struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(entry.Payload, &meta) != nil {
		return
	}
	*sessionID = meta.ID
	*sessionCwd = meta.Cwd
}

// findSessionFile locates the JSONL transcript for a given session ID.
func findSessionFile(sessionID, codexHome string) string {
	sessionsDir := filepath.Join(resolveCodexHomeDir(codexHome), "sessions")

	var found string
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return nil
		}
		if strings.Contains(filepath.Base(path), sessionID) {
			found = path
		}
		return nil
	})
	return found
}

// getSessionHistory reads the JSONL transcript and returns user/assistant messages.
func getSessionHistory(sessionID, codexHome string, limit int) ([]core.HistoryEntry, error) {
	path := findSessionFile(sessionID, codexHome)
	if path == "" {
		return nil, fmt.Errorf("session file not found for %s", sessionID)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []core.HistoryEntry

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), codexSessionScannerMaxTokenSize)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		if raw.Type != "response_item" {
			continue
		}

		var item struct {
			Role    string `json:"role"`
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Payload, &item) != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)

		switch {
		case item.Role == "user" && len(item.Content) > 0:
			for _, c := range item.Content {
				if c.Type == "input_text" && c.Text != "" && isUserPrompt(c.Text) {
					entries = append(entries, core.HistoryEntry{
						Role: "user", Content: c.Text, Timestamp: ts,
					})
				}
			}
		case item.Role == "assistant" && len(item.Content) > 0:
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					entries = append(entries, core.HistoryEntry{
						Role: "assistant", Content: c.Text, Timestamp: ts,
					})
				}
			}
		case item.Type == "reasoning" && item.Text != "":
			// skip reasoning items
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read codex session history: %w", err)
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// patchSessionSource rewrites the session_meta line in a Codex JSONL transcript
// so that source="cli" and originator="codex_cli_rs", making the session visible
// in the interactive `codex` terminal.
func patchSessionSource(sessionID, codexHome string) bool {
	path := findSessionFile(sessionID, codexHome)
	if path == "" {
		return false
	}
	return patchSessionSourceFile(path)
}

func patchSessionSourceFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	idx := bytes.IndexByte(data, '\n')
	if idx < 0 {
		return false
	}
	firstLine := data[:idx]

	// Only patch if it's actually an exec-sourced session
	if !bytes.Contains(firstLine, []byte(`"source":"exec"`)) {
		return false
	}

	patched := bytes.Replace(firstLine, []byte(`"source":"exec"`), []byte(`"source":"cli"`), 1)
	patched = bytes.Replace(patched, []byte(`"originator":"codex_exec"`), []byte(`"originator":"codex_cli_rs"`), 1)

	if bytes.Equal(patched, firstLine) {
		return false
	}

	out := make([]byte, 0, len(patched)+len(data)-idx)
	out = append(out, patched...)
	out = append(out, data[idx:]...)

	return os.WriteFile(path, out, 0o644) == nil
}

// isUserPrompt returns true if the text looks like an actual user prompt
// rather than system context (AGENTS.md, environment_context, permissions, etc.)
func isUserPrompt(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	// Skip XML-style system context
	if strings.HasPrefix(t, "<") {
		return false
	}
	// Skip AGENTS.md instructions injected by Codex
	if strings.HasPrefix(t, "# AGENTS.md") || strings.HasPrefix(t, "#AGENTS.md") {
		return false
	}
	return true
}
