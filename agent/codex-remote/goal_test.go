package codexremote

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func goalResult(threadID, objective, status string, budget any, updated int64) map[string]any {
	return map[string]any{"goal": map[string]any{
		"threadId": threadID, "objective": objective, "status": status, "tokenBudget": budget,
		"tokensUsed": int64(11), "timeUsedSeconds": int64(7), "createdAt": int64(100), "updatedAt": updated,
	}}
}

func TestGoalGetFullAndNullHydratesAuthoritativeCache(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/goal/get": goalResult("thread_probe", "ship it", "active", int64(900), 101)}}
	agent := newPlanReviewAgent(t, rec)
	snapshot, err := agent.GetSessionGoal(context.Background(), "thread_probe")
	if err != nil || snapshot.Goal == nil || snapshot.Goal.TokenBudget == nil || *snapshot.Goal.TokenBudget != 900 {
		t.Fatalf("full get = %+v err=%v", snapshot, err)
	}
	if current, ok := agent.codec.CurrentGoal("thread_probe"); !ok || current.Goal == nil || current.Goal.Objective != "ship it" {
		t.Fatalf("cached get = %+v ok=%v", current, ok)
	}

	rec.results["thread/goal/get"] = map[string]any{"goal": nil}
	snapshot, err = agent.GetSessionGoal(context.Background(), "thread_probe")
	if err != nil || snapshot.Goal != nil {
		t.Fatalf("null get = %+v err=%v", snapshot, err)
	}
	if current, ok := agent.codec.CurrentGoal("thread_probe"); !ok || current.Goal != nil {
		t.Fatalf("cached clear = %+v ok=%v", current, ok)
	}
}

func TestGoalSetTokenBudgetOmitNullValueAndFullStatuses(t *testing.T) {
	for _, status := range []string{"active", "paused", "blocked", "usageLimited", "budgetLimited", "complete"} {
		t.Run(status, func(t *testing.T) {
			rec := &recordedRPC{results: map[string]any{"thread/goal/set": goalResult("thread_probe", "goal", status, nil, 102)}}
			agent := newPlanReviewAgent(t, rec)
			objective := " goal "
			if _, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{Objective: &objective, Status: &status}); err != nil {
				t.Fatal(err)
			}
			call, _ := rec.last("thread/goal/set")
			if _, exists := call.Params["tokenBudget"]; exists || call.Params["objective"] != "goal" || call.Params["status"] != status {
				t.Fatalf("omitted payload = %#v", call.Params)
			}
		})
	}

	rec := &recordedRPC{results: map[string]any{"thread/goal/set": goalResult("thread_probe", "goal", "active", nil, 103)}}
	agent := newPlanReviewAgent(t, rec)
	if _, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{TokenBudgetSet: true}); err != nil {
		t.Fatal(err)
	}
	call, _ := rec.last("thread/goal/set")
	if value, exists := call.Params["tokenBudget"]; !exists || value != nil {
		t.Fatalf("explicit null payload = %#v", call.Params)
	}
	budget := int64(1200)
	rec.results["thread/goal/set"] = goalResult("thread_probe", "goal", "active", budget, 104)
	if _, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{TokenBudgetSet: true, TokenBudget: &budget}); err != nil {
		t.Fatal(err)
	}
	call, _ = rec.last("thread/goal/set")
	if call.Params["tokenBudget"] != float64(1200) {
		t.Fatalf("value payload = %#v", call.Params)
	}
}

func TestGoalSetRejectsUnknownStatusAndEmptyPatch(t *testing.T) {
	agent := newPlanReviewAgent(t, &recordedRPC{})
	unknown := "dreaming"
	if _, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{Status: &unknown}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown status error = %v", err)
	}
	if _, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{}); err == nil || !strings.Contains(err.Error(), "no fields") {
		t.Fatalf("empty patch error = %v", err)
	}
}

func TestGoalOfficialMutationErrorPassesThroughWithoutState(t *testing.T) {
	rec := &recordedRPC{errors: map[string]*RPCError{"thread/goal/set": {Code: -32602, Message: "direct input is not allowed for this subagent"}}}
	agent := newPlanReviewAgent(t, rec)
	objective := "blocked"
	_, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{Objective: &objective})
	if err == nil || !strings.Contains(err.Error(), "direct input is not allowed") {
		t.Fatalf("official error = %v", err)
	}
	if _, ok := agent.codec.CurrentGoal("thread_probe"); ok {
		t.Fatal("failed mutation must not synthesize goal state")
	}
}

func TestGoalClearFalseAndTrue(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/goal/clear": map[string]any{"cleared": false}}}
	agent := newPlanReviewAgent(t, rec)
	agent.codec.applyGoalSnapshot("thread_probe", core.SessionGoalSnapshot{Goal: goalFromResult(t, goalResult("thread_probe", "keep", "paused", nil, 105))}, nil)
	cleared, err := agent.ClearSessionGoal(context.Background(), "thread_probe")
	if err != nil || cleared {
		t.Fatalf("clear false = %v err=%v", cleared, err)
	}
	if current, ok := agent.codec.CurrentGoal("thread_probe"); !ok || current.Goal == nil || current.Goal.Objective != "keep" {
		t.Fatalf("cleared:false must preserve the known record: %+v ok=%v", current, ok)
	}
	agent.codec.applyGoalSnapshot("thread_probe", core.SessionGoalSnapshot{Goal: goalFromResult(t, goalResult("thread_probe", "clear me", "paused", nil, 106))}, nil)
	rec.results["thread/goal/clear"] = map[string]any{"cleared": true}
	cleared, err = agent.ClearSessionGoal(context.Background(), "thread_probe")
	if err != nil || !cleared {
		t.Fatalf("clear true = %v err=%v", cleared, err)
	}
	if current, ok := agent.codec.CurrentGoal("thread_probe"); !ok || current.Goal != nil {
		t.Fatalf("clear true cache = %+v ok=%v", current, ok)
	}
}

func TestGoalAttachRefreshRehydratesAfterEpochReset(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/goal/get": goalResult("thread_probe", "fresh epoch", "blocked", nil, 107)}}
	agent := newPlanReviewAgent(t, rec)
	agent.codec.applyGoalSnapshot("thread_probe", core.SessionGoalSnapshot{Goal: goalFromResult(t, goalResult("thread_probe", "stale epoch", "active", nil, 106))}, nil)
	agent.codec.ResetNativeSessionState()

	agent.mu.Lock()
	cl, codec := agent.client, agent.codec
	agent.mu.Unlock()
	if _, err := agent.getSessionGoalOn(context.Background(), cl, codec, "thread_probe"); err != nil {
		t.Fatal(err)
	}
	current, ok := agent.codec.CurrentGoal("thread_probe")
	if !ok || current.Goal == nil || current.Goal.Objective != "fresh epoch" || current.Goal.Status != "blocked" {
		t.Fatalf("fresh baseline = %+v ok=%v", current, ok)
	}
}

func TestGoalNewerNotificationWinsOverOlderResponse(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/goal/set": goalResult("thread_probe", "older response", "active", nil, 110)}}
	agent := newPlanReviewAgent(t, rec)
	rec.hooks = map[string]func(){"thread/goal/set": func() {
		agent.codec.Decode(Notification{Method: "thread/goal/updated", Params: json.RawMessage(`{"threadId":"thread_probe","goal":{"threadId":"thread_probe","objective":"newer desktop","status":"paused","tokenBudget":null,"tokensUsed":12,"timeUsedSeconds":8,"createdAt":100,"updatedAt":111}}`)})
	}}
	objective := "older response"
	goal, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{Objective: &objective})
	if err != nil || goal.Objective != "newer desktop" || goal.Status != "paused" {
		t.Fatalf("returned converged goal = %+v err=%v", goal, err)
	}
	current, _ := agent.codec.CurrentGoal("thread_probe")
	if current.Goal == nil || current.Goal.Objective != "newer desktop" || current.Goal.UpdatedAt != 111 {
		t.Fatalf("stale response overwrote notification: %+v", current)
	}
}

func TestGoalResponseThenMatchingNotificationDedupes(t *testing.T) {
	rec := &recordedRPC{results: map[string]any{"thread/goal/set": goalResult("thread_probe", "same", "active", nil, 112)}}
	agent := newPlanReviewAgent(t, rec)
	objective := "same"
	if _, err := agent.SetSessionGoal(context.Background(), "thread_probe", core.SessionGoalUpdate{Objective: &objective}); err != nil {
		t.Fatal(err)
	}
	version := agent.codec.GoalVersion("thread_probe")
	events := agent.codec.Decode(Notification{Method: "thread/goal/updated", Params: json.RawMessage(`{"threadId":"thread_probe","goal":{"threadId":"thread_probe","objective":"same","status":"active","tokenBudget":null,"tokensUsed":11,"timeUsedSeconds":7,"createdAt":100,"updatedAt":112}}`)})
	if len(events) != 0 || agent.codec.GoalVersion("thread_probe") != version {
		t.Fatalf("matching notification events=%+v version=%d->%d", events, version, agent.codec.GoalVersion("thread_probe"))
	}
}

func TestGoalCodecNotificationsFullClearAndUnknown(t *testing.T) {
	codec := NewLiveCodec()
	updated := codec.Decode(Notification{Method: "thread/goal/updated", Params: json.RawMessage(`{"threadId":"thread_probe","turnId":null,"goal":{"threadId":"thread_probe","objective":"goal","status":"usageLimited","tokenBudget":null,"tokensUsed":1,"timeUsedSeconds":2,"createdAt":3,"updatedAt":4}}`)})
	if len(updated) != 1 || updated[0].GoalRecord == nil || updated[0].GoalRecord.Goal == nil || updated[0].GoalRecord.Goal.Status != "usageLimited" {
		t.Fatalf("updated = %+v", updated)
	}
	codec.ResetNativeSessionState()
	if _, ok := codec.CurrentGoal("thread_probe"); ok {
		t.Fatal("reconnect reset must discard prior-epoch goal cache")
	}
	updated = codec.Decode(Notification{Method: "thread/goal/updated", Params: json.RawMessage(`{"threadId":"thread_probe","turnId":null,"goal":{"threadId":"thread_probe","objective":"goal","status":"usageLimited","tokenBudget":null,"tokensUsed":1,"timeUsedSeconds":2,"createdAt":3,"updatedAt":4}}`)})
	cleared := codec.Decode(Notification{Method: "thread/goal/cleared", Params: json.RawMessage(`{"threadId":"thread_probe"}`)})
	if len(cleared) != 1 || cleared[0].GoalRecord == nil || cleared[0].GoalRecord.Goal != nil {
		t.Fatalf("cleared = %+v", cleared)
	}
	unknown := codec.Decode(Notification{Method: "thread/goal/updated", Params: json.RawMessage(`{"threadId":"thread_probe","goal":{"threadId":"thread_probe","objective":"x","status":"future","tokenBudget":null,"tokensUsed":0,"timeUsedSeconds":0,"createdAt":0,"updatedAt":0}}`)})
	if len(unknown) != 0 {
		t.Fatalf("unknown = %+v", unknown)
	}
}

func TestGoalDecodeRejectsUnknownStatusAndNullRequiredCounters(t *testing.T) {
	raw, _ := json.Marshal(goalResult("thread_probe", "goal", "future", nil, 4))
	if _, err := decodeGoalResponse(raw, "thread_probe"); err == nil {
		t.Fatal("unknown response status must fail closed")
	}
	for _, field := range []string{"tokensUsed", "timeUsedSeconds", "createdAt", "updatedAt"} {
		result := goalResult("thread_probe", "goal", "active", nil, 4)
		result["goal"].(map[string]any)[field] = nil
		raw, _ = json.Marshal(result)
		if _, err := decodeGoalResponse(raw, "thread_probe"); err == nil {
			t.Fatalf("null %s must fail closed", field)
		}
	}
}

func goalFromResult(t *testing.T, result map[string]any) *core.SessionGoalRecord {
	t.Helper()
	raw, _ := json.Marshal(result)
	snapshot, err := decodeGoalResponse(raw, "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Goal
}
