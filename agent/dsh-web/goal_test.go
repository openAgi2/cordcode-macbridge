package dshweb

// goal_test.go — 官方 goals/<verb> 透传的单测：wire 形状（gateway 单 args 规则
// + CAS ref）、edit 的 request.objective、无目标会话的失败、参数校验。
// 活体锚点：2026-09-05 座位探测（goals/pause args{agentId,ref} 命中官方
// transition 检查并被拒，证明 endpoint 与载荷）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func goalListFixture(sessionID string, revision int64) map[string]any {
	return map[string]any{
		"items": []map[string]any{{
			"sessionId": sessionID,
			"updatedAt": 1,
			"projections": map[string]any{
				"asOfSeq": 9,
				"values": map[string]any{
					"goal": map[string]any{
						"goal": map[string]any{
							"id": "goal-w-1", "revision": revision, "objective": "写个封神榜故事",
							"phase": "active", "maxGoalRounds": 256,
						},
						"roundsStarted": 1, "createdAt": 1, "updatedAt": 2,
					},
				},
			},
		}},
	}
}

func TestMutateSessionGoalWireAndRef(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/list"] = fakeRPCResponse{value: goalListFixture("sess-goal", 7)}
	f.handlers["goals/pause"] = fakeRPCResponse{value: map[string]any{}}

	if err := a.MutateSessionGoal(context.Background(), "sess-goal", "pause", ""); err != nil {
		t.Fatal(err)
	}

	calls := methodCalls(f, "goals/pause")
	if len(calls) != 1 {
		t.Fatalf("goals/pause calls = %d, want 1", len(calls))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 {
		t.Fatalf("payload must be exactly {args:…} (gateway single-args rule), keys = %v", payloadKeys(payload))
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(payload["args"], &args); err != nil {
		t.Fatal(err)
	}
	if string(args["agentId"]) != `"sess-goal"` {
		t.Fatalf("args.agentId = %s", args["agentId"])
	}
	var ref struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(args["ref"], &ref); err != nil {
		t.Fatal(err)
	}
	// CAS ref 必须来自 session.list 投影的最新快照（revision 7），不得用陈旧码器态。
	if ref.ID != "goal-w-1" || ref.Revision != 7 {
		t.Fatalf("args.ref = %+v, want {goal-w-1 7}", ref)
	}
	if _, has := args["request"]; has {
		t.Fatal("pause must not carry request.objective")
	}
}

func TestMutateSessionGoalEditCarriesObjective(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/list"] = fakeRPCResponse{value: goalListFixture("sess-goal", 2)}
	f.handlers["goals/edit"] = fakeRPCResponse{value: map[string]any{}}

	if err := a.MutateSessionGoal(context.Background(), "sess-goal", "edit", "  新目标  "); err != nil {
		t.Fatal(err)
	}
	calls := methodCalls(f, "goals/edit")
	if len(calls) != 1 {
		t.Fatalf("goals/edit calls = %d", len(calls))
	}
	var payload struct {
		Args struct {
			AgentID string `json:"agentId"`
			Ref     struct {
				ID       string `json:"id"`
				Revision int64  `json:"revision"`
			} `json:"ref"`
			Request *struct {
				Objective string `json:"objective"`
			} `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Args.Request == nil || payload.Args.Request.Objective != "新目标" {
		t.Fatalf("edit request = %+v, want trimmed objective", payload.Args.Request)
	}
	if payload.Args.Ref.Revision != 2 {
		t.Fatalf("edit ref revision = %d", payload.Args.Ref.Revision)
	}
}

func TestMutateSessionGoalNoCurrentGoal(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	// 投影无 goal 单元（官方 null / absent）。
	f.handlers["session/list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{{
			"sessionId": "sess-goal", "updatedAt": 1,
			"projections": map[string]any{"asOfSeq": 9, "values": map[string]any{}},
		}},
	}}
	err := a.MutateSessionGoal(context.Background(), "sess-goal", "pause", "")
	if err == nil || !strings.Contains(err.Error(), "no current goal") {
		t.Fatalf("absent goal must fail explicitly, got %v", err)
	}
	if calls := methodCalls(f, "goals/pause"); len(calls) != 0 {
		t.Fatalf("no verb call without a goal, got %d", len(calls))
	}
}

func TestMutateSessionGoalValidation(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/list"] = fakeRPCResponse{value: goalListFixture("sess-goal", 1)}

	if err := a.MutateSessionGoal(context.Background(), "", "pause", ""); err == nil {
		t.Fatal("empty session id must fail")
	}
	if err := a.MutateSessionGoal(context.Background(), "sess-goal", "teleport", ""); err == nil {
		t.Fatal("unknown action must fail")
	}
	if err := a.MutateSessionGoal(context.Background(), "sess-goal", "edit", "   "); err == nil {
		t.Fatal("edit without objective must fail")
	}
	// The resolver's liveness probe rides session/list; validation must fail
	// before any GOAL verb reaches the seat.
	for _, verb := range []string{"goals/pause", "goals/resume", "goals/clear", "goals/edit"} {
		if calls := methodCalls(f, verb); len(calls) != 0 {
			t.Fatalf("validation must fail before %s, got %d", verb, len(calls))
		}
	}
}
