package gobridge

// handlers_session_goal_test.go — DSH 目标横条动作 RPC（mutate_session_goal）：
// 无接口 not_supported、sessionId 空 invalid_params、成功 {ok:true}、失败
// goal_failed 透传座位原文；capability 仅 SessionGoalController 实现者广告。

import (
	"context"
	"errors"
	"testing"
)

type sessionGoalAgent struct {
	*fakeAgent
	mutateCalls []string
	mutateErr   error
}

func (a *sessionGoalAgent) MutateSessionGoal(ctx context.Context, sessionID, action, objective string) error {
	a.mutateCalls = append(a.mutateCalls, sessionID+"|"+action+"|"+objective)
	return a.mutateErr
}

func TestSessionGoalCapabilityOnlyForControllerAgents(t *testing.T) {
	controller := &sessionGoalAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}
	found := false
	for _, c := range deriveBackendCapabilities("dsh-web", controller, "") {
		if c == "session_goal" {
			found = true
		}
	}
	if !found {
		t.Fatal("dsh-web (SessionGoalController) must advertise session_goal")
	}
	for _, id := range []string{"grok", "claude", "codex-remote", "opencode-web"} {
		for _, c := range deriveBackendCapabilities(id, &fakeAgent{name: id}, "") {
			if c == "session_goal" {
				t.Fatalf("%s must NOT advertise session_goal (no SessionGoalController)", id)
			}
		}
	}
}

func TestMutateSessionGoalHandler(t *testing.T) {
	agent := &sessionGoalAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web",
		Method:    "mutate_session_goal",
		RequestID: "sg-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "action": "pause"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("result = %#v, want {ok:true}", messages[0])
	}
	if len(agent.mutateCalls) != 1 || agent.mutateCalls[0] != "sess-1|pause|" {
		t.Fatalf("mutateCalls = %v", agent.mutateCalls)
	}

	// edit 携带 objective。
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web",
		Method:    "mutate_session_goal",
		RequestID: "sg-2",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "action": "edit", "objective": "新目标"}),
	})
	messages = readJSONMaps(t, clientConn, 1)
	data, _ = messages[0]["data"].(map[string]any)
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("edit result = %#v", messages[0])
	}
	if agent.mutateCalls[len(agent.mutateCalls)-1] != "sess-1|edit|新目标" {
		t.Fatalf("edit mutateCall = %v", agent.mutateCalls)
	}
}

func TestMutateSessionGoalHandlerRejections(t *testing.T) {
	// 无接口 → not_supported。
	plain := &fakeAgent{name: "codex"}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", plain)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "mutate_session_goal",
		RequestID: "sg-3",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "s", "action": "pause"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	if code, _ := messages[0]["error"].(map[string]any)["code"].(string); code != "not_supported" {
		t.Fatalf("code = %v, want not_supported", messages[0]["error"])
	}

	// sessionId 空 → invalid_params。
	agent := &sessionGoalAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}
	handlers.RegisterAgent("dsh-web", agent)
	serverConn2, clientConn2, cleanup2 := openTestConn(t)
	defer cleanup2()
	handlers.HandleRPC(serverConn2, WireMessage{
		BackendID: "dsh-web",
		Method:    "mutate_session_goal",
		RequestID: "sg-4",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "  ", "action": "pause"}),
	})
	messages = readJSONMaps(t, clientConn2, 1)
	if code, _ := messages[0]["error"].(map[string]any)["code"].(string); code != "invalid_params" {
		t.Fatalf("code = %v, want invalid_params", messages[0]["error"])
	}

	// 官方拒绝 → goal_failed + 座位原文（GoalBar inline `${message} (${code})` 的文本源）。
	agent.mutateErr = errors.New(`cannot pause goal "goal-1" from phase "complete"; expected active`)
	serverConn3, clientConn3, cleanup3 := openTestConn(t)
	defer cleanup3()
	handlers.HandleRPC(serverConn3, WireMessage{
		BackendID: "dsh-web",
		Method:    "mutate_session_goal",
		RequestID: "sg-5",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "action": "pause"}),
	})
	messages = readJSONMaps(t, clientConn3, 1)
	errObj, _ := messages[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "goal_failed" {
		t.Fatalf("code = %v, want goal_failed", errObj)
	}
	if msg, _ := errObj["message"].(string); msg == "" || agent.mutateErr.Error() == "" || msg != agent.mutateErr.Error() {
		t.Fatalf("message must carry the seat error verbatim: %q", msg)
	}
}
