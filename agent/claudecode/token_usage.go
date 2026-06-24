package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type transcriptUsageEnvelope struct {
	Type    string `json:"type"`
	Message *struct {
		ID    string          `json:"id"`
		Usage json.RawMessage `json:"usage"`
	} `json:"message"`
}

func (a *Agent) GetTokenUsage(ctx context.Context) (*core.TokenUsageReport, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("claudecode: cannot determine home dir: %w", err)
	}

	absWorkDir, err := filepath.Abs(a.workDir)
	if err != nil {
		return nil, fmt.Errorf("claudecode: resolve work_dir: %w", err)
	}

	projectDir := findProjectDir(homeDir, absWorkDir)
	if projectDir == "" {
		return &core.TokenUsageReport{}, nil
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &core.TokenUsageReport{}, nil
		}
		return nil, fmt.Errorf("claudecode: read project dir: %w", err)
	}

	type sessionFile struct {
		sessionID string
		path      string
		modified  int64
	}
	files := make([]sessionFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, sessionFile{
			sessionID: strings.TrimSuffix(entry.Name(), ".jsonl"),
			path:      filepath.Join(projectDir, entry.Name()),
			modified:  info.ModTime().UnixNano(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modified > files[j].modified
	})

	report := &core.TokenUsageReport{}
	for _, file := range files {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		usage, err := aggregateTokenUsageFromTranscript(file.path, file.sessionID)
		if err != nil {
			return nil, err
		}
		if usage.TokensUsed == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheCreationTokens == 0 {
			continue
		}

		report.TotalTokensUsed += usage.TokensUsed
		report.InputTokens += usage.InputTokens
		report.OutputTokens += usage.OutputTokens
		report.CacheReadTokens += usage.CacheReadTokens
		report.CacheCreationTokens += usage.CacheCreationTokens
		report.PerSessionBreakdown = append(report.PerSessionBreakdown, usage)
	}

	return report, nil
}

func aggregateTokenUsageFromTranscript(path, sessionID string) (core.SessionTokenUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return core.SessionTokenUsage{}, fmt.Errorf("claudecode: open session file: %w", err)
	}
	defer f.Close()

	scanner := newTranscriptScanner(f)
	seenMessageIDs := make(map[string]struct{})
	usage := core.SessionTokenUsage{SessionID: sessionID}
	lineNo := 0

	for scanner.Scan() {
		lineNo++

		var entry transcriptUsageEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			slog.Debug("claudecode: skip invalid usage transcript line", "path", path, "line", lineNo, "error", err)
			continue
		}
		if entry.Type != "assistant" || entry.Message == nil || len(entry.Message.Usage) == 0 || string(entry.Message.Usage) == "null" {
			continue
		}

		messageID := strings.TrimSpace(entry.Message.ID)
		if messageID == "" {
			messageID = fmt.Sprintf("assistant-line-%d", lineNo)
		}
		if _, seen := seenMessageIDs[messageID]; seen {
			continue
		}
		seenMessageIDs[messageID] = struct{}{}

		input, output, cacheRead, cacheCreation := parseTranscriptUsageTotals(entry.Message.Usage)
		usage.InputTokens += input
		usage.OutputTokens += output
		usage.CacheReadTokens += cacheRead
		usage.CacheCreationTokens += cacheCreation
		usage.TokensUsed += input + output + cacheRead + cacheCreation
	}

	if err := scanner.Err(); err != nil {
		return core.SessionTokenUsage{}, fmt.Errorf("claudecode: scan session file: %w", err)
	}

	return usage, nil
}

func parseTranscriptUsageTotals(raw json.RawMessage) (input int, output int, cacheRead int, cacheCreation int) {
	var usage map[string]any
	if err := json.Unmarshal(raw, &usage); err != nil {
		return 0, 0, 0, 0
	}

	input = usageInt(usage, "input_tokens", "inputTokens")
	output = usageInt(usage, "output_tokens", "outputTokens")
	cacheRead = usageInt(usage, "cache_read_input_tokens", "cache_read_tokens", "cacheReadInputTokens", "cacheReadTokens")
	cacheCreation = usageInt(usage, "cache_creation_input_tokens", "cache_creation_tokens", "cacheCreationInputTokens", "cacheCreationTokens")
	return input, output, cacheRead, cacheCreation
}

func usageInt(usage map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := usage[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed)
		case float32:
			return int(typed)
		case int:
			return typed
		case int64:
			return int(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return int(parsed)
			}
		}
	}
	return 0
}

var _ core.TokenUsageReporter = (*Agent)(nil)
