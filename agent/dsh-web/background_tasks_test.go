package dshweb

// background_tasks（Phase 4）：官方 session.list 子任务行 → 只读任务摘要。
// 覆盖：子任务行映射（title/stats/tokens/两态状态）、root 行不进任务列表、
// detail 找得到/找不到。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
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
						"title":        "调查 WebSocket 重连问题",
						"sessionStats": map[string]any{"turns": 2, "steps": 31, "llmMs": 182817, "toolMs": 5017},
						"tokenUsage":   map[string]any{"uncachedInputTokens": 1000, "outputTokens": 500, "cacheReadTokens": 200, "cacheWriteTokens": 0},
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
	f.handlers["session/list"] = fakeRPCResponse{value: bgTaskFixtureSessionList()}

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
	// 官方耗时真值：sessionStats llmMs+toolMs（模型墙钟 + 工具墙钟）。
	if running.DurationMillis != 182817+5017 {
		t.Fatalf("durationMillis = %d, want %d (llmMs+toolMs)", running.DurationMillis, 182817+5017)
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
	if done.ToolUseCount != 0 || done.TokenCount != 0 || done.DurationMillis != 0 {
		t.Fatalf("unknown stats must stay zero, got tools=%d tokens=%d dur=%d",
			done.ToolUseCount, done.TokenCount, done.DurationMillis)
	}
}

// TestListBackgroundTasksNestedChainRootWalk：嵌套真值——subagent 的
// subagent：ParentTaskID 只在直接父也是 subagent 行时非空（depth-1 为 ""）；
// RootSessionID 沿 parentSessionId 链上溯到第一个非 subagent 祖先；环链
// 防御不死循环（取防御截断前的祖先）。
func TestListBackgroundTasksNestedChainRootWalk(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/list"] = fakeRPCResponse{value: map[string]any{
		"items": []any{
			map[string]any{
				"sessionId": "session-root-1", "updatedAt": 1786942288185, "running": false,
			},
			map[string]any{
				"sessionId": "sub-mid", "updatedAt": 1786942290000, "running": true,
				"parentSessionId": "session-root-1", "origin": "subagent",
				"projections": map[string]any{"asOfSeq": 1, "values": map[string]any{}},
			},
			map[string]any{
				"sessionId": "sub-deep", "updatedAt": 1786942295000, "running": true,
				"parentSessionId": "sub-mid", "origin": "subagent",
				"projections": map[string]any{"asOfSeq": 1, "values": map[string]any{}},
			},
			// 环链（官方不可能出现；防御验证不死循环）。
			map[string]any{
				"sessionId": "cyc-a", "updatedAt": 1786942296000, "running": false,
				"parentSessionId": "cyc-b", "origin": "subagent",
				"projections": map[string]any{"asOfSeq": 1, "values": map[string]any{}},
			},
			map[string]any{
				"sessionId": "cyc-b", "updatedAt": 1786942297000, "running": false,
				"parentSessionId": "cyc-a", "origin": "subagent",
				"projections": map[string]any{"asOfSeq": 1, "values": map[string]any{}},
			},
		},
	}}

	tasks, err := a.ListBackgroundTasks(context.Background())
	if err != nil {
		t.Fatalf("ListBackgroundTasks: %v", err)
	}
	byID := map[string]core.BackgroundTask{}
	for _, task := range tasks {
		byID[task.TaskID] = task
	}
	mid, ok := byID["sub-mid"]
	if !ok {
		t.Fatalf("sub-mid missing: %+v", tasks)
	}
	// depth-1：父是根会话 → 无 ParentTaskID，root=根会话。
	if mid.ParentTaskID != "" || mid.RootSessionID != "session-root-1" {
		t.Fatalf("depth-1 row: parent=%q root=%q", mid.ParentTaskID, mid.RootSessionID)
	}
	deep := byID["sub-deep"]
	// depth-2：父是 sub-mid（同为 subagent 行）→ ParentTaskID=sub-mid，
	// root 上溯两跳到 session-root-1。
	if deep.ParentTaskID != "sub-mid" || deep.RootSessionID != "session-root-1" {
		t.Fatalf("depth-2 row: parent=%q root=%q (want sub-mid / session-root-1)",
			deep.ParentTaskID, deep.RootSessionID)
	}
	// 环链：官方不可能出现（parentSessionId 无环）；防御目标只是不死循环
	// ——走到这里已证明终止，root 取任意截断祖先即可。
	cycA := byID["cyc-a"]
	if cycA.RootSessionID != "cyc-a" && cycA.RootSessionID != "cyc-b" {
		t.Fatalf("cycle row root = %q, want a truncated ancestor (cyc-a/cyc-b)", cycA.RootSessionID)
	}
}

func TestGetBackgroundTaskDetailFoundAndMissing(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/list"] = fakeRPCResponse{value: bgTaskFixtureSessionList()}

	detail, err := a.GetBackgroundTaskDetail(context.Background(), "sub-1111")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Task.TaskID != "sub-1111" || detail.Instruction != detail.Task.Title {
		t.Fatalf("detail = %+v", detail)
	}
	// Phase 5 语义（commit 361e5c2）：running 的官方子会话可经 session.cancel
	// 取消（CanCancel=true）；无 retry 面（CanRetry 恒 false，不提供假按钮）。
	if !detail.CanCancel {
		t.Fatal("Phase 5: running task must be cancellable (session.cancel surface)")
	}
	if detail.CanRetry {
		t.Fatal("no retry surface exists; CanRetry must stay false")
	}

	// 终态任务不可取消。
	doneDetail, err := a.GetBackgroundTaskDetail(context.Background(), "sub-2222")
	if err != nil {
		t.Fatalf("done detail: %v", err)
	}
	if doneDetail.CanCancel {
		t.Fatal("completed task must not be cancellable (no fake buttons)")
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
	f.handlers["session/cancel"] = fakeRPCResponse{value: map[string]any{}}

	if err := a.CancelBackgroundTask(context.Background(), "sub-999"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	calls := methodCalls(f, "session/cancel")
	if len(calls) != 1 {
		t.Fatalf("session.cancel calls = %d, want 1", len(calls))
	}
	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(unwrapArgs(t, calls[0]), &payload); err != nil || payload.SessionID != "sub-999" {
		t.Fatalf("payload = %s (err %v)", calls[0], err)
	}
	// 运行中任务的 detail 声明可取消；终态不声明。
	f.handlers["session/list"] = fakeRPCResponse{value: bgTaskFixtureSessionList()}
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
