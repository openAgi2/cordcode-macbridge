package grokbuild

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
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
	if len(internal) != 1 || internal[0].WorkflowRun == nil || len(internal[0].WorkflowRun.Phases) != 1 ||
		*internal[0].WorkflowRun.Phases[0].Phase != "规划" || internal[0].WorkflowRun.Phases[0].Members[0].Label != "制定执行计划" {
		t.Fatalf("planner workflow projection = %+v", internal)
	}

	spawn := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "subagent_spawned", "subagent_id": "worker-1", "child_session_id": "worker-1", "description": "write chapter", "parent_prompt_id": "turn-1",
	}, 0), "parent", state)
	if len(spawn) != 1 || spawn[0].WorkflowRun == nil || spawn[0].TurnID != "turn-1" || spawn[0].WorkflowRun.Status != "running" ||
		len(spawn[0].WorkflowRun.Phases) != 2 || *spawn[0].WorkflowRun.Phases[1].Phase != "执行" || len(spawn[0].WorkflowRun.Phases[1].Members) != 1 {
		t.Fatalf("spawn projection = %+v", spawn)
	}
	finish := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "subagent_finished", "subagent_id": "worker-1", "child_session_id": "worker-1", "status": "completed",
	}, 0), "parent", state)
	if len(finish) != 1 || finish[0].WorkflowRun.Status != "running" || finish[0].WorkflowRun.Phases[1].Members[0].Status != "completed" {
		t.Fatalf("finish projection = %+v", finish)
	}
	terminal := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "goal_updated", "goal_id": "goal-1", "objective": "ship it", "status": "completed",
	}, 5678), "parent", state)
	if len(terminal) != 2 || terminal[1].WorkflowRun == nil || terminal[1].WorkflowRun.Status != "completed" {
		t.Fatalf("terminal goal/workflow projection = %+v", terminal)
	}
}

func TestGrokInfraPauseSurfacesRealFailure(t *testing.T) {
	const failure = "Turn failed: Unauthorized (401): no auth context"
	events := convertSessionUpdateWithState(goalUpdateParams(t, map[string]any{
		"sessionUpdate": "goal_updated", "goal_id": "goal-1", "objective": "ship it",
		"status": "infra_paused", "pause_message": failure,
	}, 1234), "parent", newGrokUpdateState())
	if len(events) != 1 || events[0].Goal == nil || events[0].Goal.Phase != "blocked" ||
		events[0].Goal.BlockedReason == nil || events[0].Goal.BlockedReason.Code != "infra_paused" ||
		events[0].Goal.BlockedReason.Message != failure {
		t.Fatalf("infra pause projection = %+v", events)
	}
}

func TestLoadGrokGoalSnapshotPreservesInfraPauseMessage(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "goal"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"goal_id": "goal-1", "objective": "ship it", "status": "infra_paused",
		"pause_message": "Turn failed: Unauthorized (401)",
		"history":       []map[string]any{{"timestamp": "2026-09-08T02:49:32Z", "event": "goal_paused", "detail": "infra"}},
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "goal", "state.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	goal := loadGrokGoalSnapshot(dir)
	if goal == nil || goal.Phase != "blocked" || goal.BlockedReason == nil ||
		goal.BlockedReason.Code != "infra_paused" || goal.BlockedReason.Message != "Turn failed: Unauthorized (401)" {
		t.Fatalf("goal snapshot = %+v", goal)
	}
}

func TestDecorateGrokGoalHistoryKeepsFailedWorkflowVisibleWithoutAssistantText(t *testing.T) {
	sessionDir := t.TempDir()
	updates := []map[string]any{
		{"sessionUpdate": "goal_updated", "goal_id": "goal-1", "objective": "ship it", "status": "active"},
		{"sessionUpdate": "subagent_spawned", "subagent_id": "planner", "child_session_id": "planner", "description": grokGoalPlanWriterDescription, "parent_prompt_id": "turn-1"},
		{"sessionUpdate": "subagent_finished", "subagent_id": "planner", "child_session_id": "planner", "status": "completed"},
		{"sessionUpdate": "goal_updated", "goal_id": "goal-1", "objective": "ship it", "status": "infra_paused", "pause_message": "401"},
	}
	var journal strings.Builder
	for _, update := range updates {
		params := goalUpdateParams(t, update, 1)
		line, err := json.Marshal(map[string]any{"method": "session/update", "params": json.RawMessage(params)})
		if err != nil {
			t.Fatal(err)
		}
		journal.Write(line)
		journal.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "updates.jsonl"), []byte(journal.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []core.RichHistoryEntry{{
		ID: "command", Role: "system", Parts: []map[string]any{{"type": "command", "name": "goal", "args": "ship it"}},
	}}
	got := decorateGrokGoalHistory(sessionDir, "parent", entries)
	if len(got) != 2 || got[1].Role != "assistant" || len(got[1].Parts) != 1 ||
		got[1].Parts[0]["type"] != "workflow" || got[1].Parts[0]["workflowStatus"] != string(core.WorkflowStatusFailed) {
		t.Fatalf("decorated entries = %+v", got)
	}
}

func TestSessionUpdateOwnerIDRejectsChildTranscript(t *testing.T) {
	params := json.RawMessage(`{"sessionId":"child-1","update":{"sessionUpdate":"agent_message_chunk"}}`)
	if got := sessionUpdateOwnerID(params); got != "child-1" {
		t.Fatalf("owner = %q, want child-1", got)
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
