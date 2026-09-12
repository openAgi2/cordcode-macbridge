package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestMapAndReduceCodexGoalRecordStatusesAndClear(t *testing.T) {
	r := NewProjectionReducer()
	for i, status := range []string{"active", "paused", "blocked", "usageLimited", "budgetLimited", "complete"} {
		budget := int64(100 + i)
		name, raw, done := mapAgentEvent(core.Event{Type: core.EventSessionGoalRecord, GoalRecord: &core.SessionGoalSnapshot{Goal: &core.SessionGoalRecord{
			ThreadID: "thread", Objective: "objective", Status: status, TokenBudget: &budget,
			TokensUsed: int64(i), TimeUsedSeconds: int64(i + 1), CreatedAt: 2, UpdatedAt: int64(10 + i),
		}}})
		if done || name != "session_goal_record" {
			t.Fatalf("status %s map = %q done=%v", status, name, done)
		}
		r.Apply(ev(i+1, "codex-remote", "thread", name, raw.(map[string]interface{})))
		projection, _ := r.Snapshot("codex-remote", "thread")
		if projection.CodexGoal == nil || projection.CodexGoal.Goal == nil || projection.CodexGoal.Goal.Status != status || projection.CodexGoal.Goal.TokenBudget == nil || *projection.CodexGoal.Goal.TokenBudget != budget {
			t.Fatalf("status %s projection = %+v", status, projection.CodexGoal)
		}
		r.FlushPatch("codex-remote", "thread")
	}
	name, raw, _ := mapAgentEvent(core.Event{Type: core.EventSessionGoalRecord, GoalRecord: &core.SessionGoalSnapshot{}})
	r.Apply(ev(7, "codex-remote", "thread", name, raw.(map[string]interface{})))
	projection, _ := r.Snapshot("codex-remote", "thread")
	if projection.CodexGoal == nil || projection.CodexGoal.Goal != nil || projection.Goal != nil {
		t.Fatalf("Codex clear/dsh isolation = codex:%+v dsh:%+v", projection.CodexGoal, projection.Goal)
	}
	patch, ok := r.FlushPatch("codex-remote", "thread")
	if !ok || patch.CodexGoal == nil || patch.CodexGoal.Goal != nil {
		t.Fatalf("clear patch = %+v ok=%v", patch.CodexGoal, ok)
	}
}

func TestCodexGoalRecordLastWinsDedupAndIdentityGuard(t *testing.T) {
	r := NewProjectionReducer()
	data := map[string]interface{}{"goal": map[string]interface{}{
		"threadId": "thread", "objective": "one", "status": "active", "tokenBudget": nil,
		"tokensUsed": int64(0), "timeUsedSeconds": int64(0), "createdAt": int64(1), "updatedAt": int64(2),
	}}
	r.Apply(ev(1, "codex-remote", "thread", "session_goal_record", data))
	r.FlushPatch("codex-remote", "thread")
	projection, _ := r.Snapshot("codex-remote", "thread")
	before := projection.SyncRev
	r.Apply(ev(2, "codex-remote", "thread", "session_goal_record", data))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if projection.SyncRev != before {
		t.Fatalf("duplicate committed: %d -> %d", before, projection.SyncRev)
	}
	invalid := map[string]interface{}{"goal": map[string]interface{}{
		"threadId": "other", "objective": "bad", "status": "paused", "tokenBudget": nil,
		"tokensUsed": int64(0), "timeUsedSeconds": int64(0), "createdAt": int64(1), "updatedAt": int64(2),
	}}
	r.Apply(ev(3, "codex-remote", "thread", "session_goal_record", invalid))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if projection.CodexGoal.Goal.Objective != "one" || projection.SyncRev != before {
		t.Fatalf("identity mismatch mutated projection: %+v", projection.CodexGoal)
	}
	data["goal"].(map[string]interface{})["objective"] = "desktop newer"
	data["goal"].(map[string]interface{})["updatedAt"] = int64(3)
	r.Apply(ev(4, "codex-remote", "thread", "session_goal_record", data))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if projection.CodexGoal.Goal.Objective != "desktop newer" {
		t.Fatalf("last official value did not win: %+v", projection.CodexGoal)
	}
}

func TestCodexGoalRecordReusesSessionCommandTimelineCard(t *testing.T) {
	r := NewProjectionReducer()
	goal := map[string]interface{}{
		"threadId": "thread", "objective": "写四个故事", "status": "active", "tokenBudget": nil,
		"tokensUsed": int64(0), "timeUsedSeconds": int64(0), "createdAt": int64(123), "updatedAt": int64(123),
	}
	r.Apply(ev(1, "codex-remote", "thread", "session_goal_record", map[string]interface{}{"goal": goal}))
	projection, _ := r.Snapshot("codex-remote", "thread")
	if len(projection.Turns) != 1 || projection.Turns[0].System == nil {
		t.Fatalf("goal command turn = %+v", projection.Turns)
	}
	part := projection.Turns[0].System.Parts[0]
	if projection.Turns[0].TurnID != "cmd:codex-goal:123" || part.Type != "command" ||
		part.CommandName != "goal" || part.CommandKind != "running" || part.CommandLine != "/goal 写四个故事" {
		t.Fatalf("goal command part = %+v turn=%+v", part, projection.Turns[0])
	}

	r.FlushPatch("codex-remote", "thread")
	goal["status"] = "complete"
	goal["tokensUsed"] = int64(99)
	goal["updatedAt"] = int64(130)
	r.Apply(ev(2, "codex-remote", "thread", "session_goal_record", map[string]interface{}{"goal": goal}))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if len(projection.Turns) != 1 || projection.Turns[0].System.Parts[0].CommandKind != "success" {
		t.Fatalf("goal settle must update in place: %+v", projection.Turns)
	}
	patch, ok := r.FlushPatch("codex-remote", "thread")
	if !ok || len(patch.UpsertTurns) != 1 || patch.UpsertTurns[0].TurnID != "cmd:codex-goal:123" {
		t.Fatalf("goal settle patch = %+v ok=%v", patch, ok)
	}
}

func TestCodexGoalRecordRejectsIncompleteOrFractionalOfficialShape(t *testing.T) {
	r := NewProjectionReducer()
	base := map[string]interface{}{
		"threadId": "thread", "objective": "real", "status": "active", "tokenBudget": nil,
		"tokensUsed": float64(1), "timeUsedSeconds": float64(2), "createdAt": float64(3), "updatedAt": float64(4),
	}
	missing := map[string]interface{}{}
	for key, value := range base {
		if key != "timeUsedSeconds" {
			missing[key] = value
		}
	}
	r.Apply(ev(1, "codex-remote", "thread", "session_goal_record", map[string]interface{}{"goal": missing}))
	projection, _ := r.Snapshot("codex-remote", "thread")
	if projection.SyncRev != 0 || projection.CodexGoal != nil {
		t.Fatalf("incomplete full record mutated projection: %+v", projection.CodexGoal)
	}
	fractional := map[string]interface{}{}
	for key, value := range base {
		fractional[key] = value
	}
	fractional["updatedAt"] = 4.5
	r.Apply(ev(2, "codex-remote", "thread", "session_goal_record", map[string]interface{}{"goal": fractional}))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if projection.SyncRev != 0 || projection.CodexGoal != nil {
		t.Fatalf("fractional integer field mutated projection: %+v", projection.CodexGoal)
	}
}

func TestGoalCommandTurnInsertsByTimeBeforeRunTurn(t *testing.T) {
	r := NewProjectionReducer()
	var clock int64 = 1_780_000_000_000
	r.now = func() int64 { return clock }

	// An older turn keeps its position ahead of the command card.
	r.Apply(ev(1, "codex-remote", "thread", "turn_started", map[string]interface{}{"turnId": "turn-old"}))
	// The live goal race: the run turn skeleton exists by the time the goal
	// record arrives, and the goal was created 0.4s BEFORE the turn started.
	clock = 1_780_000_001_000
	r.Apply(ev(2, "codex-remote", "thread", "turn_started", map[string]interface{}{"turnId": "turn-run"}))
	r.Apply(ev(3, "codex-remote", "thread", "session_goal_record", map[string]interface{}{"goal": map[string]interface{}{
		"threadId": "thread", "objective": "四故事", "status": "active",
		"tokensUsed": int64(0), "timeUsedSeconds": int64(0), "tokenBudget": nil,
		"createdAt": int64(1_780_000_000), "updatedAt": int64(1_780_000_000),
	}}))

	projection, _ := r.Snapshot("codex-remote", "thread")
	var ids []string
	for _, turn := range projection.Turns {
		ids = append(ids, turn.TurnID)
	}
	want := []string{"turn-old", "cmd:codex-goal:1780000000", "turn-run"}
	if len(ids) != len(want) {
		t.Fatalf("turns = %v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("turns = %v want %v (command card must sit before the run turn, not at arrival-order end)", ids, want)
		}
	}

	// A status refresh of the SAME goal updates the card in place, no reorder.
	clock = 1_780_000_002_000
	r.Apply(ev(4, "codex-remote", "thread", "session_goal_record", map[string]interface{}{"goal": map[string]interface{}{
		"threadId": "thread", "objective": "四故事", "status": "complete",
		"tokensUsed": int64(5), "timeUsedSeconds": int64(5), "tokenBudget": nil,
		"createdAt": int64(1_780_000_000), "updatedAt": int64(1_780_000_002_000),
	}}))
	projection, _ = r.Snapshot("codex-remote", "thread")
	if ids := projection.Turns[1].TurnID; ids != "cmd:codex-goal:1780000000" {
		t.Fatalf("update moved the card: %+v", projection.Turns)
	}
	if part := projection.Turns[1].System.Parts[0]; part.CommandKind != "success" {
		t.Fatalf("complete status kind = %q", part.CommandKind)
	}
}
