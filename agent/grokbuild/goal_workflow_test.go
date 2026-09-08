package grokbuild

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func goalUpdateParams(t *testing.T, update map[string]any, ts int64) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"sessionId": "parent", "update": update, "_meta": map[string]any{"agentTimestampMs": ts}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGrokGoalAndSubagentsNormalizeToExistingProjections(t *testing.T) {
	state := newGrokUpdateState()
	goalEvents := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "goal_updated", "goal_id": "goal-1", "objective": "ship it", "status": "active", "last_event": "goal_created",
	}, 1234), "parent", state)
	if len(goalEvents) != 1 || goalEvents[0].Goal == nil || goalEvents[0].Goal.Phase != "active" || goalEvents[0].Goal.Revision != 1234 {
		t.Fatalf("goal events = %+v", goalEvents)
	}

	internal := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "subagent_spawned", "subagent_id": "planner", "description": grokGoalPlanWriterDescription, "parent_prompt_id": "turn-1",
	}, 0), "parent", state)
	if len(internal) != 0 {
		t.Fatalf("internal plan writer leaked: %+v", internal)
	}

	spawn := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "subagent_spawned", "subagent_id": "worker-1", "child_session_id": "worker-1", "description": "write chapter", "parent_prompt_id": "turn-1",
	}, 0), "parent", state)
	if len(spawn) != 1 || spawn[0].WorkflowRun == nil || spawn[0].TurnID != "turn-1" || spawn[0].WorkflowRun.Status != "running" || len(spawn[0].WorkflowRun.Phases[0].Members) != 1 {
		t.Fatalf("spawn projection = %+v", spawn)
	}
	finish := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "subagent_finished", "subagent_id": "worker-1", "child_session_id": "worker-1", "status": "completed",
	}, 0), "parent", state)
	if len(finish) != 1 || finish[0].WorkflowRun.Status != "completed" || finish[0].WorkflowRun.Phases[0].Members[0].Status != "completed" {
		t.Fatalf("finish projection = %+v", finish)
	}
}

func TestGrokBackgroundTasksUseRealMetaAndHidePlanWriter(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "cwd", "parent")
	root := filepath.Join(sessionDir, "subagents")
	for id, meta := range map[string]map[string]any{
		"planner": {"subagent_id": "planner", "parent_session_id": "parent", "child_session_id": "planner", "description": grokGoalPlanWriterDescription, "status": "completed"},
		"worker":  {"subagent_id": "worker", "parent_session_id": "parent", "child_session_id": "worker", "subagent_type": "general-purpose", "description": "write chapter", "prompt": "full instruction", "status": "completed", "started_at": "2026-09-07T16:00:00Z", "completed_at": "2026-09-07T16:00:02Z", "duration_ms": 2000, "tool_calls": 3},
	} {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(meta)
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	finished := `{"method":"_x.ai/session/update","params":{"sessionId":"parent","update":{"sessionUpdate":"subagent_finished","subagent_id":"worker","status":"completed","duration_ms":2000,"tool_calls":3,"tokens_used":42}}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "updates.jsonl"), []byte(finished), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{grokHome: home}
	tasks, err := a.ListBackgroundTasks(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	if tasks[0].TaskID != "worker" || tasks[0].Title != "write chapter" || tasks[0].TokenCount != 42 || tasks[0].ToolUseCount != 3 {
		t.Fatalf("task=%+v", tasks[0])
	}
	detail, err := a.GetBackgroundTaskDetail(context.Background(), "worker")
	if err != nil || detail.Instruction != "full instruction" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestGrokGoalReminderExtractsOnlyObjective(t *testing.T) {
	text := "<system-reminder>\nA goal has been set: first line\nsecond line\n\nYou are working directly on this goal across multiple turns.\nsecret instructions"
	objective, ok := grokGoalObjectiveFromReminder(text)
	if !ok || objective != "first line\nsecond line" {
		t.Fatalf("objective=%q ok=%v", objective, ok)
	}
}
