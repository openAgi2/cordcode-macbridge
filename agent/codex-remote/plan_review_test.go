package codexremote

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func seedAwaitingPlan(agent *Agent, plan codexProposedPlan) {
	if agent.codec == nil {
		agent.codec = NewLiveCodec()
	}
	agent.codec.mu.Lock()
	agent.codec.awaitingPlanReview[plan.itemID] = plan
	agent.codec.mu.Unlock()
}

func awaitingPlanPresent(agent *Agent, itemID string) bool {
	agent.codec.mu.Lock()
	defer agent.codec.mu.Unlock()
	_, ok := agent.codec.awaitingPlanReview[itemID]
	return ok
}

type recordedRPC struct {
	mu     sync.Mutex
	calls  []recordedCall
	errors map[string]*RPCError
}

type recordedCall struct {
	Method string
	Params map[string]any
}

func (r *recordedRPC) handle(_ int64, method string, params json.RawMessage) (any, *RPCError) {
	parsed := map[string]any{}
	_ = json.Unmarshal(params, &parsed)
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{Method: method, Params: parsed})
	err := r.errors[method]
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if method == "turn/start" {
		return map[string]any{"turn": map[string]any{"id": "turn-impl", "status": "inProgress"}}, nil
	}
	return map[string]any{}, nil
}

func (r *recordedRPC) last(method string) (recordedCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.calls) - 1; i >= 0; i-- {
		if r.calls[i].Method == method {
			return r.calls[i], true
		}
	}
	return recordedCall{}, false
}

func newPlanReviewAgent(t *testing.T, rec *recordedRPC) *Agent {
	t.Helper()
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	t.Cleanup(func() { _ = stream.Close() })
	startEnvelopePeer(t, hostConn, rec.handle)
	cl := NewClient(stream, 1)
	t.Cleanup(func() { _ = cl.Close() })
	agent := New(nil)
	agent.BindClient(cl)
	agent.defaultModel = "gpt-5"
	return agent
}

func samplePlan() codexProposedPlan {
	return codexProposedPlan{
		threadID: "thread_probe",
		turnID:   "turn-1",
		itemID:   "turn-1-plan",
		text:     "# Final plan\n- first\n",
	}
}

func TestRespondSessionPermissionApproveStartsDefaultTurn(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	seedAwaitingPlan(agent, samplePlan())

	err := agent.RespondSessionPermission(context.Background(), "thread_probe", "turn-1-plan", core.PermissionResult{
		Behavior:   "allow",
		PlanAction: "approve",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	call, ok := rec.last("turn/start")
	if !ok {
		t.Fatalf("expected turn/start, calls=%+v", rec.calls)
	}
	if call.Params["threadId"] != "thread_probe" {
		t.Fatalf("threadId = %v", call.Params["threadId"])
	}
	input, _ := call.Params["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %+v", call.Params["input"])
	}
	item, _ := input[0].(map[string]any)
	if item["type"] != "text" || item["text"] != planImplementationCodingMessage {
		t.Fatalf("input item = %+v, want official %q", item, planImplementationCodingMessage)
	}
	mode, _ := call.Params["collaborationMode"].(map[string]any)
	if mode["mode"] != "default" {
		t.Fatalf("collaborationMode.mode = %v, want default", mode["mode"])
	}
	settings, _ := mode["settings"].(map[string]any)
	if settings["model"] != "gpt-5" {
		t.Fatalf("settings.model = %v", settings["model"])
	}
	if settings["developer_instructions"] != nil {
		t.Fatalf("developer_instructions = %v, want JSON null (built-in)", settings["developer_instructions"])
	}
	if awaitingPlanPresent(agent, "turn-1-plan") {
		t.Fatal("successful approve must consume the pending plan")
	}
}

func TestRespondSessionPermissionRequestChangesKeepsPlanMode(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	seedAwaitingPlan(agent, samplePlan())

	err := agent.RespondSessionPermission(context.Background(), "thread_probe", "turn-1-plan", core.PermissionResult{
		Behavior:   "deny",
		PlanAction: "requestChanges",
		Message:    "第二步改成并行",
	})
	if err != nil {
		t.Fatalf("requestChanges: %v", err)
	}
	call, ok := rec.last("turn/start")
	if !ok {
		t.Fatalf("expected turn/start, calls=%+v", rec.calls)
	}
	item, _ := call.Params["input"].([]any)[0].(map[string]any)
	if item["text"] != "第二步改成并行" {
		t.Fatalf("feedback = %+v", item)
	}
	mode, _ := call.Params["collaborationMode"].(map[string]any)
	if mode["mode"] != "plan" {
		t.Fatalf("mode = %v, want plan", mode["mode"])
	}
}

func TestRespondSessionPermissionRequestChangesEmptyUsesKeepPlanningCopy(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	seedAwaitingPlan(agent, samplePlan())

	err := agent.RespondSessionPermission(context.Background(), "thread_probe", "turn-1-plan", core.PermissionResult{
		PlanAction: "requestChanges",
	})
	if err != nil {
		t.Fatalf("empty requestChanges: %v", err)
	}
	call, _ := rec.last("turn/start")
	item, _ := call.Params["input"].([]any)[0].(map[string]any)
	if item["text"] != planKeepPlanningEmptyFeedback {
		t.Fatalf("empty feedback = %v", item["text"])
	}
}

func TestRespondSessionPermissionQuitUpdatesThreadToDefault(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	seedAwaitingPlan(agent, samplePlan())

	err := agent.RespondSessionPermission(context.Background(), "thread_probe", "turn-1-plan", core.PermissionResult{
		PlanAction: "quit",
	})
	if err != nil {
		t.Fatalf("quit: %v", err)
	}
	if _, ok := rec.last("turn/start"); ok {
		t.Fatal("quit must not start a turn (official clear-context-and-implement is unsupported; No is stay/leave without Implement the plan.)")
	}
	call, ok := rec.last("thread/settings/update")
	if !ok {
		t.Fatalf("expected thread/settings/update, calls=%+v", rec.calls)
	}
	mode, _ := call.Params["collaborationMode"].(map[string]any)
	if mode["mode"] != "default" {
		t.Fatalf("quit mode = %v, want default", mode["mode"])
	}
}

func TestRespondSessionPermissionUnknownActionRestoresPlan(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	seedAwaitingPlan(agent, samplePlan())

	err := agent.RespondSessionPermission(context.Background(), "thread_probe", "turn-1-plan", core.PermissionResult{
		PlanAction: "clearContext",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown planAction") {
		t.Fatalf("unknown action err = %v", err)
	}
	if !awaitingPlanPresent(agent, "turn-1-plan") {
		t.Fatal("failed action must restore the pending plan so the user can retry")
	}
}

func TestRespondSessionPermissionRPCFailureRestoresPlan(t *testing.T) {
	rec := &recordedRPC{errors: map[string]*RPCError{
		"turn/start": {Code: -32602, Message: "invalid collaborationMode"},
	}}
	agent := newPlanReviewAgent(t, rec)
	seedAwaitingPlan(agent, samplePlan())

	err := agent.RespondSessionPermission(context.Background(), "thread_probe", "turn-1-plan", core.PermissionResult{
		PlanAction: "approve",
	})
	if err == nil {
		t.Fatal("want RPC error")
	}
	if !awaitingPlanPresent(agent, "turn-1-plan") {
		t.Fatal("RPC failure must restore the pending plan")
	}
}

func TestRespondSessionPermissionMissingPlanIsNotSupported(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	err := agent.RespondSessionPermission(context.Background(), "thread_probe", "turn-1-plan", core.PermissionResult{
		PlanAction: "approve",
	})
	if err != core.ErrNotSupported {
		t.Fatalf("missing plan = %v, want ErrNotSupported", err)
	}
}

func TestRemoteSessionRespondPermissionDelegates(t *testing.T) {
	rec := &recordedRPC{}
	agent := newPlanReviewAgent(t, rec)
	seedAwaitingPlan(agent, samplePlan())
	sess := &remoteSession{agent: agent, threadID: "thread_probe"}
	if err := sess.RespondPermission("turn-1-plan", core.PermissionResult{PlanAction: "approve"}); err != nil {
		t.Fatalf("session approve: %v", err)
	}
	if _, ok := rec.last("turn/start"); !ok {
		t.Fatal("session RespondPermission must start the implement turn")
	}
}
