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
