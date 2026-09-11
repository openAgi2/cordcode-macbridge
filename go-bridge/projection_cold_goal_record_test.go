package gobridge

import (
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestCodexRemoteColdHistoryKeepsGoalCommandBeforeContinuationTurn(t *testing.T) {
	goal := core.SessionGoalRecord{
		ThreadID: "thread-goal", Objective: "finish the rollout", Status: "complete",
		TokensUsed: 100, TimeUsedSeconds: 8, CreatedAt: 200, UpdatedAt: 208,
	}
	result := &core.ColdHistoryResult{
		HistoryMode: "paginated",
		GoalRecord:  &core.SessionGoalSnapshot{Goal: &goal},
		Page: &core.UpstreamHistoryPage{Turns: []core.TurnScopedHistoryTurn{
			{
				TurnID: "turn-before", Status: "completed", HasTime: true,
				StartedAt: time.Unix(100, 0), UserItemID: "user-before", UserText: "start",
			},
			{
				TurnID: "turn-continuation", Status: "completed", HasTime: true,
				StartedAt: time.Unix(200, 0), UserItemID: "user-continuation", UserText: "continue",
			},
		}},
	}
	h := NewHandlers()
	var events []projectionHydrateEvent
	if err := h.streamCodexRemoteColdHistoryFromResult(result, "codex-remote", "thread-goal", func(ev projectionHydrateEvent) bool {
		events = append(events, ev)
		return true
	}); err != nil {
		t.Fatal(err)
	}

	goalIndex, continuationIndex := -1, -1
	for i, event := range events {
		if event.Event == "session_goal_record" {
			goalIndex = i
		}
		if event.Event == "user_message" && event.Data["turnId"] == "turn-continuation" {
			continuationIndex = i
		}
	}
	if goalIndex < 0 || continuationIndex < 0 || goalIndex >= continuationIndex {
		t.Fatalf("goal/continuation event order = goal:%d continuation:%d events:%+v", goalIndex, continuationIndex, events)
	}

	reducer := NewProjectionReducer()
	for i, event := range events {
		reducer.Apply(projectionReducerEvent("codex-remote", "thread-goal", event.Event, event.Data, i+1, ""))
	}
	projection, ok := reducer.Snapshot("codex-remote", "thread-goal")
	if !ok {
		t.Fatal("projection missing")
	}
	if len(projection.Turns) != 3 || projection.Turns[0].TurnID != "turn-before" ||
		projection.Turns[1].TurnID != "cmd:codex-goal:200" || projection.Turns[2].TurnID != "turn-continuation" {
		t.Fatalf("cold timeline = %+v", projection.Turns)
	}
	if projection.CodexGoal == nil || projection.CodexGoal.Goal == nil || projection.CodexGoal.Goal.Objective != "finish the rollout" {
		t.Fatalf("cold goal = %+v", projection.CodexGoal)
	}
}
