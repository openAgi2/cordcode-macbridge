package dshweb

// background_tasks（Phase 4）：官方 session.list 子任务行 → 只读任务摘要。
// 覆盖：子任务行映射（title/stats/tokens/两态状态）、root 行不进任务列表、
// detail 找得到/找不到。

import (
	"context"
	"encoding/json"
	"testing"
)

func bgTaskFixtureSessionList() map[string]any {
	return map[string]any{
		"items": []any{
			map[string]any{
				"sessionId": "session-root-1", "updatedAt": 1786942288185, "running": false,
				"cwd": "/tmp/fixture", "agentPreset": "standard",
				"projections": map[string]any{
					"asOfSeq": 10,
					"values": map[string]any{
						"title": "root 会话",
					},
				},
			},
			map[string]any{
				"sessionId": "sub-1111", "updatedAt": 1786942290000, "running": true,
				"parentSessionId": "session-root-1", "origin": "subagent",
				"cwd": "/tmp/fixture", "agentPreset": "standard",
				"projections": map[string]any{
					"asOfSeq": 20,
					"values": map[string]any{
						"title": "调查 WebSocket 重连问题",
						"sessionStats": map[string]any{"turns": 2, "steps": 31, "llmMs": 182817, "toolMs": 5017},
						"tokenUsage": map[string]any{"uncachedInputTokens": 1000, "outputTokens": 500, "cacheReadTokens": 200, "cacheWriteTokens": 0},
					},
				},
			},
			map[string]any{
				"sessionId": "sub-2222", "updatedAt": 1786941200000, "running": false,
				"parentSessionId": "session-root-1", "origin": "subagent",
				"projections": map[string]any{"asOfSeq": 5, "values": map[string]any{}},
			},
		},
	}
}

func TestListBackgroundTasksMapsSubagentRows(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.list"] = fakeRPCResponse{value: bgTaskFixtureSessionList()}

	tasks, err := a.ListBackgroundTasks(context.Background())
	if err != nil {
		t.Fatalf("ListBackgroundTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks len = %d, want 2 (root rows never enter the task list)", len(tasks))
	}
	// updatedAt 降序：sub-1111 在前。
	if tasks[0].TaskID != "sub-1111" {
		t.Fatalf("first task = %s, want sub-1111 (updatedAt desc)", tasks[0].TaskID)
	}
	running := tasks[0]
	if running.Status != "running" {
		t.Fatalf("running row status = %q, want running", running.Status)
	}
	if running.Title != "调查 WebSocket 重连问题" {
		t.Fatalf("title = %q", running.Title)
	}
	if running.RootSessionID != "session-root-1" {
		t.Fatalf("rootSessionId = %q", running.RootSessionID)
	}
	if running.ToolUseCount != 31 {
		t.Fatalf("toolUseCount = %d, want 31 (sessionStats.steps)", running.ToolUseCount)
	}
	if running.TokenCount != 1700 {
		t.Fatalf("tokenCount = %d, want 1700 (uncached+output+cacheRead+cacheWrite)", running.TokenCount)
	}
	if !running.TranscriptAvailable {
		t.Fatal("transcriptAvailable should be true (official history readable)")
	}

	done := tasks[1]
	if done.Status != "completed" {
		t.Fatalf("finished row status = %q, want completed", done.Status)
	}
	if done.Title == "" || done.Title == "子任务 " {
		t.Fatalf("no-title row should fall back to a placeholder id fragment, got %q", done.Title)
	}
	// 无 stats 投影 → 统计保持未知（0，wire OMIT），不编造。
	if done.ToolUseCount != 0 || done.TokenCount != 0 {
		t.Fatalf("unknown stats must stay zero, got tools=%d tokens=%d", done.ToolUseCount, done.TokenCount)
	}
}

func TestGetBackgroundTaskDetailFoundAndMissing(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.list"] = fakeRPCResponse{value: bgTaskFixtureSessionList()}

	detail, err := a.GetBackgroundTaskDetail(context.Background(), "sub-1111")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Task.TaskID != "sub-1111" || detail.Instruction != detail.Task.Title {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.CanCancel || detail.CanRetry {
		t.Fatal("Phase 4 read-only: capabilities must be false")
	}

	if _, err := a.GetBackgroundTaskDetail(context.Background(), "no-such"); err == nil {
		t.Fatal("missing task must error (task_not_found path)")
	}
}

// Phase 5：官方 session.cancel 真实取消面。
func TestCancelBackgroundTaskUsesOfficialSessionCancel(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["background"] = fakeRPCResponse{}
	f.handlers["session.cancel"] = fakeRPCResponse{value: map[string]any{}}

	if err := a.CancelBackgroundTask(context.Background(), "sub-999"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	calls := methodCalls(f, "session.cancel")
	if len(calls) != 1 {
		t.Fatalf("session.cancel calls = %d, want 1", len(calls))
	}
	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(calls[0], &payload); err != nil || payload.SessionID != "sub-999" {
		t.Fatalf("payload = %s (err %v)", calls[0], err)
	}
	// 运行中任务的 detail 声明可取消；终态不声明。
	f.handlers["session.list"] = fakeRPCResponse{value: bgTaskFixtureSessionList()}
	detail, err := a.GetBackgroundTaskDetail(context.Background(), "sub-1111")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.CanCancel {
		t.Fatal("running task detail must declare CanCancel (official session.cancel surface)")
	}
	done, _ := a.GetBackgroundTaskDetail(context.Background(), "sub-2222")
	if done.CanCancel {
		t.Fatal("terminal task must NOT declare CanCancel")
	}
}
