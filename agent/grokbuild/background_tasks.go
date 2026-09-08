package grokbuild

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.BackgroundTaskProvider = (*Agent)(nil)
var _ core.BackgroundTaskDetailReader = (*Agent)(nil)

type grokSubagentMeta struct {
	SubagentID      string `json:"subagent_id"`
	ParentSessionID string `json:"parent_session_id"`
	ChildSessionID  string `json:"child_session_id"`
	SubagentType    string `json:"subagent_type"`
	Description     string `json:"description"`
	Prompt          string `json:"prompt"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at"`
	CompletedAt     string `json:"completed_at"`
	DurationMillis  int64  `json:"duration_ms"`
	ToolCalls       int64  `json:"tool_calls"`
	Error           string `json:"error"`
}

type grokFinishedStats struct {
	durationMillis int64
	toolCalls      int64
	tokensUsed     int64
}

// Grok persists descriptive/task timing metadata in subagents/<id>/meta.json,
// but the token total is owned by the matching subagent_finished update. Fold
// only those real terminal notifications; missing values remain unknown (0).
func loadGrokFinishedStats(updatesPath string) map[string]grokFinishedStats {
	stats := make(map[string]grokFinishedStats)
	f, err := os.Open(updatesPath)
	if err != nil {
		return stats
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		var head struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(line, &head) != nil || !isSessionUpdateMethod(head.Method) {
			continue
		}
		var outer struct {
			Update sessionUpdatePayload `json:"update"`
		}
		if json.Unmarshal(extractParams(line), &outer) != nil || outer.Update.SessionUpdate != "subagent_finished" {
			continue
		}
		id := strings.TrimSpace(outer.Update.SubagentID)
		if id == "" {
			id = strings.TrimSpace(outer.Update.ChildSessionID)
		}
		if id != "" {
			var tokensUsed int64
			if outer.Update.TokensUsed != nil {
				tokensUsed = int64(*outer.Update.TokensUsed)
			}
			stats[id] = grokFinishedStats{
				durationMillis: outer.Update.DurationMillis,
				toolCalls:      outer.Update.SubagentToolCalls,
				tokensUsed:     tokensUsed,
			}
		}
	}
	return stats
}

type grokTaskRecord struct {
	task   core.BackgroundTask
	prompt string
}

func parseGrokMetaTime(raw string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	return t
}

func normalizeGrokTaskStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "running", "completed", "failed", "cancelled":
		return strings.ToLower(strings.TrimSpace(status))
	case "active":
		return "running"
	default:
		return "failed"
	}
}

func loadGrokTaskRecords(ctx context.Context, grokHome string) ([]grokTaskRecord, error) {
	root := filepath.Join(resolveGrokHome(grokHome), "sessions")
	var records []grokTaskRecord
	finishedBySession := make(map[string]map[string]grokFinishedStats)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "meta.json" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "subagents" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var meta grokSubagentMeta
		if json.Unmarshal(raw, &meta) != nil || strings.TrimSpace(meta.SubagentID) == "" || isInternalGrokSubagent(meta.Description) {
			return nil
		}
		sessionDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
		statsByTask := finishedBySession[sessionDir]
		if statsByTask == nil {
			statsByTask = loadGrokFinishedStats(filepath.Join(sessionDir, "updates.jsonl"))
			finishedBySession[sessionDir] = statsByTask
		}
		terminal := statsByTask[meta.SubagentID]
		durationMillis := meta.DurationMillis
		if terminal.durationMillis > 0 {
			durationMillis = terminal.durationMillis
		}
		toolCalls := meta.ToolCalls
		if terminal.toolCalls > 0 {
			toolCalls = terminal.toolCalls
		}
		started, finished := parseGrokMetaTime(meta.StartedAt), parseGrokMetaTime(meta.CompletedAt)
		updated := finished
		if updated.IsZero() {
			updated = started
		}
		records = append(records, grokTaskRecord{task: core.BackgroundTask{
			TaskID: meta.SubagentID, BackendID: "grokbuild", RootSessionID: meta.ParentSessionID,
			AgentID: meta.ChildSessionID, Title: meta.Description, AgentName: meta.SubagentType,
			Status: normalizeGrokTaskStatus(meta.Status), StartedAt: started, FinishedAt: finished,
			DurationMillis: durationMillis, TokenCount: terminal.tokensUsed, ToolUseCount: toolCalls,
			Error: meta.Error, TranscriptAvailable: true, UpdatedAt: updated,
		}, prompt: meta.Prompt})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].task.UpdatedAt.Equal(records[j].task.UpdatedAt) {
			return records[i].task.TaskID < records[j].task.TaskID
		}
		return records[i].task.UpdatedAt.After(records[j].task.UpdatedAt)
	})
	return records, nil
}

func (a *Agent) ListBackgroundTasks(ctx context.Context) ([]core.BackgroundTask, error) {
	a.mu.RLock()
	home := a.grokHome
	a.mu.RUnlock()
	records, err := loadGrokTaskRecords(ctx, home)
	if err != nil {
		return nil, err
	}
	tasks := make([]core.BackgroundTask, 0, len(records))
	for _, record := range records {
		tasks = append(tasks, record.task)
	}
	return tasks, nil
}

func (a *Agent) GetBackgroundTaskDetail(ctx context.Context, taskID string) (*core.BackgroundTaskDetail, error) {
	a.mu.RLock()
	home := a.grokHome
	a.mu.RUnlock()
	records, err := loadGrokTaskRecords(ctx, home)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.task.TaskID == taskID {
			return &core.BackgroundTaskDetail{Task: record.task, Instruction: record.prompt, NestedTasks: []core.BackgroundTask{}, CanCancel: false, CanRetry: false}, nil
		}
	}
	return nil, fmt.Errorf("grokbuild: background task %q: %w", taskID, os.ErrNotExist)
}
