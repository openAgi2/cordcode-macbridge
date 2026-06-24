package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestFetchCodexTodosReturnsLatestPlanSnapshot(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "05", "05")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}

	sessionID := "session-plan"
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-05-05T12-00-00-"+sessionID+".jsonl")
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/project"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"第一步\",\"status\":\"in_progress\"},{\"step\":\"第二步\",\"status\":\"pending\"}]}"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"explanation\":\"latest\",\"plan\":[{\"step\":\"第一步\",\"status\":\"completed\"},{\"step\":\"第二步\",\"status\":\"in_progress\"},{\"step\":\"第三步\",\"status\":\"canceled\"}]}"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	todos, err := fetchCodexTodos(context.Background(), sessionID, codexHome)
	if err != nil {
		t.Fatalf("fetchCodexTodos() error = %v", err)
	}
	if len(todos) != 3 {
		t.Fatalf("len(todos) = %d, want 3", len(todos))
	}
	if got := todos[0].Status; got != "completed" {
		t.Fatalf("todos[0].Status = %q, want completed", got)
	}
	if got := todos[1].Status; got != "in_progress" {
		t.Fatalf("todos[1].Status = %q, want in_progress", got)
	}
	if got := todos[2].Status; got != "cancelled" {
		t.Fatalf("todos[2].Status = %q, want cancelled", got)
	}
	if got := todos[0].Priority; got != "normal" {
		t.Fatalf("todos[0].Priority = %q, want normal", got)
	}
}

func TestFetchCodexTodosReturnsEmptyForSessionWithoutPlan(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "05", "05")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}

	sessionID := "session-empty"
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-05-05T12-00-00-"+sessionID+".jsonl")
	rollout := `{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/project"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	todos, err := fetchCodexTodos(context.Background(), sessionID, codexHome)
	if err != nil {
		t.Fatalf("fetchCodexTodos() error = %v", err)
	}
	if len(todos) != 0 {
		t.Fatalf("len(todos) = %d, want 0", len(todos))
	}
}

func TestFetchCodexTodosReturnsMissingSessionError(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}

	_, err := fetchCodexTodos(context.Background(), "missing-session", codexHome)
	if err == nil {
		t.Fatal("fetchCodexTodos() error = nil, want missing session error")
	}
	if errors.Is(err, core.ErrNotSupported) {
		t.Fatalf("fetchCodexTodos() error = %v, want missing session error", err)
	}
}

func TestFetchCodexTodosReturnsNotSupportedWhenHomeUnavailable(t *testing.T) {
	oldResolver := codexTodoHomeResolver
	codexTodoHomeResolver = func(string) string { return "" }
	t.Cleanup(func() {
		codexTodoHomeResolver = oldResolver
	})

	_, err := fetchCodexTodos(context.Background(), "session-plan", "")
	if !errors.Is(err, core.ErrNotSupported) {
		t.Fatalf("fetchCodexTodos() error = %v, want ErrNotSupported", err)
	}
}

// TestFetchCodexTodosAbortResumeScenario 模拟 Mac 中途停止 turn 后的 plan 状态：
// 10 步任务，步骤 1-6 completed，步骤 7 in_progress（被中断），步骤 8-10 pending。
// 验证 FetchTodos 能返回完整的断点信息供 iOS 构造续接 prompt。
func TestFetchCodexTodosAbortResumeScenario(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "05", "22")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}

	sessionID := "session-abort-resume"
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-05-22T12-00-00-"+sessionID+".jsonl")

	// 模拟 LLM 在执行步骤 7 时被取消的最后一条 update_plan
	plan := "["
	for i := 1; i <= 10; i++ {
		status := "pending"
		if i <= 6 {
			status = "completed"
		} else if i == 7 {
			status = "in_progress"
		}
		if i > 1 {
			plan += ","
		}
		plan += fmt.Sprintf(`{\"step\":\"步骤%d\",\"status\":\"%s\"}`, i, status)
	}
	plan += "]"

	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/project"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"explanation\":\"executing\",\"plan\":` + plan + `}"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	todos, err := fetchCodexTodos(context.Background(), sessionID, codexHome)
	if err != nil {
		t.Fatalf("fetchCodexTodos() error = %v", err)
	}
	if len(todos) != 10 {
		t.Fatalf("len(todos) = %d, want 10", len(todos))
	}

	// 步骤 1-6: completed
	for i := 0; i < 6; i++ {
		if got := todos[i].Status; got != "completed" {
			t.Errorf("todos[%d].Status = %q, want completed (步骤 %d)", i, got, i+1)
		}
	}
	// 步骤 7: in_progress
	if got := todos[6].Status; got != "in_progress" {
		t.Errorf("todos[6].Status = %q, want in_progress (步骤 7)", got)
	}
	// 步骤 8-10: pending
	for i := 7; i < 10; i++ {
		if got := todos[i].Status; got != "pending" {
			t.Errorf("todos[%d].Status = %q, want pending (步骤 %d)", i, got, i+1)
		}
	}

	// 验证 content 包含步骤信息（供 iOS 构造续接 prompt）
	if got := todos[6].Content; got != "步骤7" {
		t.Errorf("todos[6].Content = %q, want 步骤7", got)
	}
}

// TestFetchCodexTodosReturnsPlanAfterMultipleTurns 验证跨 turn 的 plan 数据：
// turn 1 的 update_plan 显示步骤 1-3 completed，turn 2（续接 turn）的 update_plan
// 显示步骤 1-3 completed + 步骤 4-6 completed。验证总是返回最新的 plan。
func TestFetchCodexTodosReturnsPlanAfterMultipleTurns(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "05", "22")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}

	sessionID := "session-multi-turn"
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-05-22T14-00-00-"+sessionID+".jsonl")

	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp/project"}}` + "\n" +
		// turn 1 的 plan：步骤 1-3 completed，步骤 4 in_progress
		`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"步骤1\",\"status\":\"completed\"},{\"step\":\"步骤2\",\"status\":\"completed\"},{\"step\":\"步骤3\",\"status\":\"completed\"},{\"step\":\"步骤4\",\"status\":\"in_progress\"}]}"}}` + "\n" +
		// turn 2（续接）的 plan：步骤 1-6 completed
		`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"步骤1\",\"status\":\"completed\"},{\"step\":\"步骤2\",\"status\":\"completed\"},{\"step\":\"步骤3\",\"status\":\"completed\"},{\"step\":\"步骤4\",\"status\":\"completed\"},{\"step\":\"步骤5\",\"status\":\"completed\"},{\"step\":\"步骤6\",\"status\":\"completed\"}]}"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	todos, err := fetchCodexTodos(context.Background(), sessionID, codexHome)
	if err != nil {
		t.Fatalf("fetchCodexTodos() error = %v", err)
	}
	if len(todos) != 6 {
		t.Fatalf("len(todos) = %d, want 6", len(todos))
	}
	for i, todo := range todos {
		if todo.Status != "completed" {
			t.Errorf("todos[%d].Status = %q, want completed", i, todo.Status)
		}
	}
}

func TestFetchCodexTodosClearsStalePlanAfterTaskComplete(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}

	sessionID := "session-completed-stale-plan"
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-06-13T12-00-00-"+sessionID+".jsonl")
	rollout := "" +
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"完成步骤\",\"status\":\"completed\"},{\"step\":\"遗留进行中\",\"status\":\"in_progress\"},{\"step\":\"遗留待处理\",\"status\":\"pending\"}]}"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	todos, err := fetchCodexTodos(context.Background(), sessionID, codexHome)
	if err != nil {
		t.Fatalf("fetchCodexTodos() error = %v", err)
	}
	if len(todos) != 0 {
		t.Fatalf("len(todos) = %d, want 0 after task_complete", len(todos))
	}
}

func TestFetchCodexTodosDoesNotReusePreviousTaskPlan(t *testing.T) {
	data := []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n" +
			`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"旧任务\",\"status\":\"in_progress\"}]}"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}` + "\n",
	)

	todos, found, err := parseTodoSnapshotFromRolloutBytes(data)
	if err != nil {
		t.Fatalf("parseTodoSnapshotFromRolloutBytes() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want task boundary to be authoritative")
	}
	if len(todos) != 0 {
		t.Fatalf("len(todos) = %d, want 0 for new task without plan", len(todos))
	}
}

var _ core.TodoProvider = (*Agent)(nil)
