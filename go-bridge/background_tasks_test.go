package gobridge

// background_tasks handlers（Phase 4 只读）：claude sidechain registry（与 B4
// 同源派生，C1）、provider 路由、未声明 backend not_supported、wire OMIT 语义。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// writeSidechainFixture lays out a Claude projects tree with one root session and
// two sidechain subagents (one completed, one stoppedByUser → cancelled).
func writeBackgroundTaskSidechainFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	subDir := filepath.Join(root, "fixture-project", "ses_root_1", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, v any) {
		raw, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(subDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agent-agentA.meta.json", map[string]any{
		"agentType": "general-purpose", "description": "调查日志重连根因",
		"toolUseId": "call_a", "spawnDepth": 1,
	})
	write("agent-agentB.meta.json", map[string]any{
		"agentType": "code-reviewer", "description": "复审补丁",
		"toolUseId": "call_b", "spawnDepth": 2, "parentAgentId": "agentA", "stoppedByUser": true,
	})
	// sidechain JSONL：一条 assistant turn + 一个 tool_use（工具计数证据）。
	jsonl := `{"type":"assistant","message":{"content":[{"type":"text","text":"done"},{"type":"tool_use","name":"Bash"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(subDir, "agent-agentA.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-agentB.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestClaudeBackgroundTasksFromSidechainFixture(t *testing.T) {
	root := writeBackgroundTaskSidechainFixture(t)
	tasks, err := claudeBackgroundTasks(root)
	if err != nil {
		t.Fatalf("claudeBackgroundTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks len = %d, want 2", len(tasks))
	}
	byID := map[string]core.BackgroundTask{}
	for _, task := range tasks {
		byID[task.TaskID] = task
	}
	a := byID["agentA"]
	if a.Title != "调查日志重连根因" || a.AgentName != "general-purpose" {
		t.Fatalf("agentA = %+v", a)
	}
	if a.Status != "completed" {
		t.Fatalf("agentA status = %q, want completed (same derivation as B4)", a.Status)
	}
	if a.ToolUseCount != 1 {
		t.Fatalf("agentA toolUseCount = %d, want 1 (real tool_use rows)", a.ToolUseCount)
	}
	if a.RootSessionID != "ses_root_1" {
		t.Fatalf("rootSessionId = %q", a.RootSessionID)
	}
	if !a.TranscriptAvailable {
		t.Fatal("transcriptAvailable should be true (jsonl exists)")
	}

	b := byID["agentB"]
	if b.Status != "cancelled" {
		t.Fatalf("agentB status = %q, want cancelled (stoppedByUser summary signal)", b.Status)
	}
	if b.ParentTaskID != "agentA" {
		t.Fatalf("agentB parentTaskId = %q, want agentA (nested edge)", b.ParentTaskID)
	}
}

func TestClaudeBackgroundTaskDetailNested(t *testing.T) {
	root := writeBackgroundTaskSidechainFixture(t)
	detail, err := claudeBackgroundTaskDetail(root, "agentA")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Instruction != "调查日志重连根因" {
		t.Fatalf("instruction = %q", detail.Instruction)
	}
	if len(detail.NestedTasks) != 1 || detail.NestedTasks[0].TaskID != "agentB" {
		t.Fatalf("nested = %+v, want [agentB]", detail.NestedTasks)
	}
	if _, err := claudeBackgroundTaskDetail(root, "nope"); !os.IsNotExist(err) {
		t.Fatalf("missing task err = %v, want NotExist", err)
	}
}

// backgroundTaskProviderAgent 满足 core.BackgroundTaskProvider（dsh 路由）。
type backgroundTaskProviderAgent struct {
	*fakeAgent
	tasks []core.BackgroundTask
}

func (b *backgroundTaskProviderAgent) ListBackgroundTasks(context.Context) ([]core.BackgroundTask, error) {
	return b.tasks, nil
}

func TestBackgroundTasksListProviderRouting(t *testing.T) {
	agent := &backgroundTaskProviderAgent{
		fakeAgent: &fakeAgent{name: "dsh-web"},
		tasks: []core.BackgroundTask{{
			TaskID: "sub-1", BackendID: "dsh-web", RootSessionID: "root-1",
			Title: "官方子任务", Status: "running",
			UpdatedAt: time.UnixMilli(1786942290000), TokenCount: 1234,
		}},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web",
		Method:    "background_tasks.list",
		RequestID: "bt-1",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	tasks, _ := data["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(tasks))
	}
	task := tasks[0].(map[string]any)
	if task["taskId"] != "sub-1" || task["status"] != "running" {
		t.Fatalf("task = %#v", task)
	}
	if task["tokenCount"] != float64(1234) {
		t.Fatalf("tokenCount = %#v", task["tokenCount"])
	}
	// 未知统计 OMIT：没有 toolUseCount 键，客户端不得渲染 0。
	if _, present := task["toolUseCount"]; present {
		t.Fatal("unknown toolUseCount must be omitted, not 0")
	}
}

func TestBackgroundTasksListNotSupportedForPlainAgents(t *testing.T) {
	agent := &fakeAgent{name: "codex"} // 无 provider、非 claudecode
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "background_tasks.list",
		RequestID: "bt-2",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	if code, _ := messages[0]["error"].(map[string]any)["code"].(string); code != "not_supported" {
		t.Fatalf("error code = %#v, want not_supported (no capability → honest rejection)", messages[0]["error"])
	}
}

func TestBackgroundTasksCapabilityDerivation(t *testing.T) {
	provider := &backgroundTaskProviderAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}
	caps := deriveBackendCapabilities("dsh-web", provider, "")
	hasList, hasDetail := false, false
	for _, c := range caps {
		if c == "background_tasks" {
			hasList = true
		}
		if c == "background_task_details" {
			hasDetail = true
		}
	}
	if !hasList {
		t.Fatal("provider backend must advertise background_tasks")
	}
	if hasDetail {
		t.Fatal("list-only provider must NOT advertise background_task_details")
	}

	// claudecode 由 go-bridge sidechain registry 服务（id 分支，C1 注记）。
	claude := &fakeAgent{name: "claudecode"}
	caps = deriveBackendCapabilities("claudecode", claude, "")
	hasList = false
	for _, c := range caps {
		if c == "background_tasks" {
			hasList = true
		}
	}
	if !hasList {
		t.Fatal("claudecode must advertise background_tasks (sidechain registry)")
	}

	// codex 无任务面 → 不声明。
	codexCaps := deriveBackendCapabilities("codex", &fakeAgent{name: "codex"}, "")
	for _, c := range codexCaps {
		if c == "background_tasks" {
			t.Fatal("codex must not advertise background_tasks (no evidence of a task plane)")
		}
	}
}
