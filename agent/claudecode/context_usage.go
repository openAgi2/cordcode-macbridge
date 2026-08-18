package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	claudeDefaultContextWindow = 200000
	claudeOneMillionWindow     = 1000000
	claudeUsageTailBytes       = 1 << 20
)

type claudeUsageLine struct {
	Type    string `json:"type"`
	Message *struct {
		ID    string          `json:"id"`
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	} `json:"message"`
}

func inferClaudeContextWindow(model string, maxContextTokens int) int {
	if maxContextTokens > 0 {
		return maxContextTokens
	}
	lower := strings.ToLower(model)
	if strings.Contains(lower, "1m") || strings.Contains(lower, "[1m]") {
		return claudeOneMillionWindow
	}
	return claudeDefaultContextWindow
}

func occupancyFromClaudeUsage(raw json.RawMessage, model string, maxContextTokens int) *core.ContextUsage {
	input, output, cacheRead, cacheCreation := parseTranscriptUsageTotals(raw)
	used := input + cacheRead + cacheCreation
	window := inferClaudeContextWindow(model, maxContextTokens)
	if used <= 0 || window <= 0 {
		return nil
	}
	return &core.ContextUsage{
		UsedTokens:        used,
		TotalTokens:       used,
		InputTokens:       input,
		CachedInputTokens: cacheRead,
		OutputTokens:      output,
		CacheReadTokens:   cacheRead,
		CacheWriteTokens:  cacheCreation,
		ContextWindow:     window,
	}
}

func occupancyFromClaudeUsageMap(usage map[string]any, model string, maxContextTokens int) *core.ContextUsage {
	if usage == nil {
		return nil
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return nil
	}
	return occupancyFromClaudeUsage(raw, model, maxContextTokens)
}

func loadClaudeContextUsage(path, model string, maxContextTokens int) *core.ContextUsage {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	if usage := readClaudeUsageTail(f, model, maxContextTokens); usage != nil {
		return usage
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	return scanClaudeUsage(f, model, maxContextTokens)
}

func readClaudeUsageTail(f *os.File, model string, maxContextTokens int) *core.ContextUsage {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil
	}
	start := info.Size() - claudeUsageTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	buf := make([]byte, info.Size()-start)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil
	}
	return parseClaudeUsageFromTail(buf[:n], model, maxContextTokens)
}

func parseClaudeUsageFromTail(data []byte, model string, maxContextTokens int) *core.ContextUsage {
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if usage := parseClaudeUsageLine(lines[i], model, maxContextTokens); usage != nil {
			return usage
		}
	}
	return nil
}

func scanClaudeUsage(r io.Reader, model string, maxContextTokens int) *core.ContextUsage {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var last *core.ContextUsage
	for scanner.Scan() {
		if usage := parseClaudeUsageLine(scanner.Bytes(), model, maxContextTokens); usage != nil {
			last = usage
		}
	}
	return last
}

func parseClaudeUsageLine(line []byte, model string, maxContextTokens int) *core.ContextUsage {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	var entry claudeUsageLine
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil
	}
	if entry.Type != "assistant" || entry.Message == nil || len(entry.Message.Usage) == 0 {
		return nil
	}
	if entry.Message.Model != "" {
		model = entry.Message.Model
	}
	return occupancyFromClaudeUsage(entry.Message.Usage, model, maxContextTokens)
}

func (a *Agent) GetSessionContextUsage(_ context.Context, sessionID string) (*core.ContextUsage, error) {
	if sessionID == "" {
		return nil, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	workDir := a.workDir
	model := a.model
	maxTokens := a.maxContextTokens
	a.mu.RUnlock()
	absWorkDir, _ := filepath.Abs(workDir)
	projectDir := resolveClaudeHistoryProjectDir(homeDir, absWorkDir, sessionID)
	if projectDir == "" {
		return nil, nil
	}
	return loadClaudeContextUsage(filepath.Join(projectDir, sessionID+".jsonl"), model, maxTokens), nil
}

func (cs *claudeSession) GetContextUsage() *core.ContextUsage {
	if cs == nil {
		return nil
	}
	cs.usageMu.Lock()
	defer cs.usageMu.Unlock()
	if cs.lastUsage == nil {
		return nil
	}
	copy := *cs.lastUsage
	return &copy
}

func (cs *claudeSession) rememberContextUsage(usage *core.ContextUsage) {
	if usage == nil {
		return
	}
	copy := *usage
	cs.usageMu.Lock()
	cs.lastUsage = &copy
	cs.usageMu.Unlock()
}

func (cs *claudeSession) emitContextUsage(raw map[string]any, model string) {
	if cs.historyDraining.Load() {
		return
	}
	usage := occupancyFromClaudeUsageMap(raw, model, cs.maxContextTokens)
	if usage == nil {
		return
	}
	cs.rememberContextUsage(usage)
	select {
	case cs.events <- cs.scopeEvent(core.Event{
		Type:         core.EventContextUsageUpdated,
		SessionID:    cs.CurrentSessionID(),
		ContextUsage: usage,
	}):
	case <-cs.ctx.Done():
	}
}

var _ core.ContextUsageReporter = (*claudeSession)(nil)
