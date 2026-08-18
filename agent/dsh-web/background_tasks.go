package dshweb

// background_tasks.go — Phase 4 read-only task plane (roadmap §3.3 DSH): the
// official session.list subagent rows (origin=subagent + parentSessionId — the
// SAME rows ListSessions filters OUT of the root list) become background-task
// summaries. No invented fields: list rows carry title/running/stats via the
// projections block; anything absent stays zero/empty.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var errTaskNotFound = errors.New("dsh-web: background task not found")

// dshSessionStats mirrors the official sessionStats projection (live-captured
// 2026-08-18, testdata/session_list_sanitized.json).
type dshSessionStats struct {
	Turns  int64 `json:"turns"`
	Steps  int64 `json:"steps"`
	LLMMs  int64 `json:"llmMs"`
	ToolMs int64 `json:"toolMs"`
}

// dshTokenUsage mirrors the official tokenUsage projection.
type dshTokenUsage struct {
	UncachedInput int64 `json:"uncachedInputTokens"`
	Output        int64 `json:"outputTokens"`
	CacheRead     int64 `json:"cacheReadTokens"`
	CacheWrite    int64 `json:"cacheWriteTokens"`
}

func decodeProjection[T any](block *apiSessionProjectionsBlock, key string) (T, bool) {
	var zero T
	if block == nil {
		return zero, false
	}
	raw, ok := block.Values[key]
	if !ok || len(raw) == 0 {
		return zero, false
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, false
	}
	return v, true
}

// ListBackgroundTasks implements core.BackgroundTaskProvider: official subagent
// sub-session rows as read-only task summaries. Status is honest two-state —
// running (official flag) or completed; the list surface carries no failure
// signal, so failed/cancelled are never invented here.
func (a *Agent) ListBackgroundTasks(ctx context.Context) ([]core.BackgroundTask, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var val sessionListValue
	if err := client.Call(ctx, "session.list", sessionListRequest{}, &val); err != nil {
		return nil, err
	}
	out := make([]core.BackgroundTask, 0, 8)
	for _, item := range val.Items {
		if item.Origin != "subagent" || item.ParentSessionID == "" {
			continue
		}
		status := "completed"
		if item.Running {
			status = "running"
		}
		task := core.BackgroundTask{
			TaskID:              item.SessionID,
			BackendID:           BackendID,
			RootSessionID:       item.ParentSessionID,
			AgentID:             item.SessionID,
			Title:               titleFromProjections(item.Projections),
			Status:              status,
			UpdatedAt:           time.UnixMilli(item.UpdatedAt),
			TranscriptAvailable: true, // 官方 session.history 可读（只读详情路径）
		}
		if stats, ok := decodeProjection[dshSessionStats](item.Projections, "sessionStats"); ok {
			task.ToolUseCount = stats.Steps
		}
		if usage, ok := decodeProjection[dshTokenUsage](item.Projections, "tokenUsage"); ok {
			task.TokenCount = usage.UncachedInput + usage.Output + usage.CacheRead + usage.CacheWrite
		}
		if task.Title == "" {
			// 无 title 投影的行：显示占位 id 片段，不编造标题。
			task.Title = "子任务 " + shortTaskID(item.SessionID)
		}
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// GetBackgroundTaskDetail implements core.BackgroundTaskDetailReader with the
// truth the list surface actually carries: the row itself. Instruction equals
// the title projection (the official list surface has no separate instruction
// field — reading one would mean parsing history transcripts iOS-side, which
// guardrail C3 forbids and MacBridge does not fabricate).
func (a *Agent) GetBackgroundTaskDetail(ctx context.Context, taskID string) (*core.BackgroundTaskDetail, error) {
	tasks, err := a.ListBackgroundTasks(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.TaskID == taskID {
			return &core.BackgroundTaskDetail{
				Task:        t,
				Instruction: t.Title,
				// 只读列表里仍 running 的官方子会话可经 session.cancel 取消；
				// 终态任务不可取消（不提供假按钮）。
				CanCancel: t.Status == "running",
			}, nil
		}
	}
	return nil, errTaskNotFound
}

func shortTaskID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// CancelBackgroundTask implements core.BackgroundTaskCanceller via the official
// session.cancel RPC on the sub-session — a real cancellation surface, no local
// simulation.
func (a *Agent) CancelBackgroundTask(ctx context.Context, taskID string) error {
	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	return client.Call(ctx, "session.cancel", sessionCancelRequest{SessionID: taskID}, nil)
}

var _ core.BackgroundTaskProvider = (*Agent)(nil)
var _ core.BackgroundTaskDetailReader = (*Agent)(nil)
var _ core.BackgroundTaskCanceller = (*Agent)(nil)
