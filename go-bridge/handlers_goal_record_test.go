package gobridge

import (
	"context"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type goalRecordFakeAgent struct {
	*fakeAgent
	mu       sync.Mutex
	ready    bool
	snapshot core.SessionGoalSnapshot
	setGoal  core.SessionGoalRecord
	updates  []core.SessionGoalUpdate
	clear    bool
}

func (a *goalRecordFakeAgent) SessionGoalReady() bool { return a.ready }
func (a *goalRecordFakeAgent) GetSessionGoal(context.Context, string) (core.SessionGoalSnapshot, error) {
	return a.snapshot, nil
}
func (a *goalRecordFakeAgent) SetSessionGoal(_ context.Context, _ string, update core.SessionGoalUpdate) (core.SessionGoalRecord, error) {
	a.mu.Lock()
	a.updates = append(a.updates, update)
	a.mu.Unlock()
	return a.setGoal, nil
}
func (a *goalRecordFakeAgent) ClearSessionGoal(context.Context, string) (bool, error) {
	return a.clear, nil
}

func readyGoalRecordFake() *goalRecordFakeAgent {
	budget := int64(1000)
	goal := core.SessionGoalRecord{ThreadID: "thread", Objective: "ship", Status: "active", TokenBudget: &budget, TokensUsed: 10, TimeUsedSeconds: 5, CreatedAt: 1, UpdatedAt: 2}
	return &goalRecordFakeAgent{fakeAgent: &fakeAgent{name: "codex-remote"}, ready: true, snapshot: core.SessionGoalSnapshot{Goal: &goal}, setGoal: goal, clear: true}
}

func registerGoalRecordSession(h *Handlers, agent *goalRecordFakeAgent) {
	h.RegisterAgent("codex-remote", agent)
	h.mu.Lock()
	h.putSessionWithMeta("thread", "codex-remote", "", &fakeAgentSession{id: "thread", events: make(chan core.Event, 1)})
	h.mu.Unlock()
}

func TestGoalRecordHandlersGetSetClearAndDoubleOptionalBudget(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h := newTestHandlers(t)
	agent := readyGoalRecordFake()
	registerGoalRecordSession(h, agent)
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "get_session_goal", RequestID: "get", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread"})})
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "set_session_goal", RequestID: "set", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread", "objective": "ship v2", "tokenBudget": nil})})
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "clear_session_goal", RequestID: "clear", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread"})})
	messages := readJSONMaps(t, clientConn, 3)
	byID := map[string]map[string]any{}
	for _, message := range messages {
		byID[message["requestId"].(string)] = message
	}
	goal := byID["get"]["data"].(map[string]any)["goal"].(map[string]any)
	if goal["status"] != "active" || goal["tokenBudget"] != float64(1000) || goal["tokensUsed"] != float64(10) {
		t.Fatalf("get goal = %#v", goal)
	}
	if byID["clear"]["data"].(map[string]any)["cleared"] != true {
		t.Fatalf("clear = %#v", byID["clear"])
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.updates) != 1 || agent.updates[0].Objective == nil || *agent.updates[0].Objective != "ship v2" || !agent.updates[0].TokenBudgetSet || agent.updates[0].TokenBudget != nil {
		t.Fatalf("set update = %+v", agent.updates)
	}
}

func TestGoalRecordSetNullableOrdinaryFieldsPreserve(t *testing.T) {
	_, update, err := decodeGoalSetParams(mustJSONRaw(t, map[string]any{
		"sessionId": "thread", "objective": nil, "status": nil, "tokenBudget": int64(500),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if update.Objective != nil || update.Status != nil || !update.TokenBudgetSet || update.TokenBudget == nil || *update.TokenBudget != 500 {
		t.Fatalf("nullable preserve update = %+v", update)
	}
}

func TestGoalRecordHandlerValidationReadinessAndWriteGuard(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h := newTestHandlers(t)
	agent := readyGoalRecordFake()
	agent.ready = false
	registerGoalRecordSession(h, agent)
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "get_session_goal", RequestID: "closed", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread"})})
	agent.ready = true
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "set_session_goal", RequestID: "invalid", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread", "status": "future"})})
	release, ok := h.tryBeginNativeSessionWrite("codex-remote", "thread")
	if !ok {
		t.Fatal("initial write guard failed")
	}
	h.HandleRPC(serverConn, WireMessage{BackendID: "codex-remote", Method: "clear_session_goal", RequestID: "busy", Params: mustJSONRaw(t, map[string]any{"sessionId": "thread"})})
	release()
	messages := readJSONMaps(t, clientConn, 3)
	codes := map[string]string{}
	for _, message := range messages {
		codes[message["requestId"].(string)] = message["error"].(map[string]any)["code"].(string)
	}
	if codes["closed"] != "unsupported_capability" || codes["invalid"] != "invalid_params" || codes["busy"] != "session_action_in_progress" {
		t.Fatalf("codes = %v", codes)
	}
	if len(agent.updates) != 0 {
		t.Fatalf("invalid/busy write escaped: %+v", agent.updates)
	}
}

func TestGoalRecordCapabilityRequiresControllerAndReadiness(t *testing.T) {
	agent := readyGoalRecordFake()
	if caps := deriveBackendCapabilities("codex-remote", agent, ""); !containsString(caps, "session_goal") {
		t.Fatalf("ready record controller did not advertise: %v", caps)
	}
	agent.ready = false
	if caps := deriveBackendCapabilities("codex-remote", agent, ""); containsString(caps, "session_goal") {
		t.Fatalf("closed record controller advertised: %v", caps)
	}
}
