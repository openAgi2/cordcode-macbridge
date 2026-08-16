package dshweb

// §8-4 unit tests: approval chain (frame→permission event→respond rpcId
// echo→resolved close), external-session routing, first-writer-wins, and the
// full batch-question semantics (per-question ids, one respond when complete,
// batch-resolved expansion, reject asymmetry, overwrite idempotency, replay).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// boundTestSession starts a session bound to the fake and returns it.
func boundTestSession(t *testing.T, f *fakeDSHServer, a *Agent, sessionID string) *dshSession {
	t.Helper()
	f.handlers["session.create"] = fakeRPCResponse{value: map[string]any{"sessionId": sessionID}}
	f.hooks["session.history"] = func(_ []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"events": []any{}, "hasMore": false}}
	}
	sessAny, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() { _ = sessAny.Close() })
	return sessAny.(*dshSession)
}

func drainOne(t *testing.T, ch <-chan core.Event, what string) core.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never arrived", what)
		return core.Event{}
	}
}

func assertNoEvent(t *testing.T, ch <-chan core.Event, what string) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("%s unexpectedly arrived: %+v", what, ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestApprovalChainSurfacesAndResponds(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-appr")

	// approval/requested for a BOUND session surfaces a permission request.
	a.handleApprovalFrame(context.Background(), "frame-rpc-a1", "approval/requested", mustJSON(map[string]any{
		"sessionId": "sess-appr", "approvalId": "appr-1", "toolName": "bash", "callId": "c9",
	}))
	ev := drainOne(t, sess.Events(), "permission_request")
	if ev.Type != core.EventPermissionRequest || ev.RequestID != "appr-1" || ev.ToolName != "bash" {
		t.Fatalf("permission event: %+v", ev)
	}

	// iOS allow → /api/respond echoes the frame rpcId, outcome allowed-once.
	f.handlers["/api/respond"] = fakeRPCResponse{}
	if err := sess.RespondPermission("appr-1", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	f.lastRespond.mu.Lock()
	body := f.lastRespond.body
	f.lastRespond.mu.Unlock()
	var sent struct {
		Type   string        `json:"type"`
		RPCID  string        `json:"rpcId"`
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Type != "client-response" || sent.RPCID != "frame-rpc-a1" {
		t.Fatalf("respond envelope/rpcId echo: %+v", sent)
	}
	if !sent.Result.OK {
		t.Fatalf("approval answer must ride the value branch: %+v", sent.Result)
	}
	var val map[string]any
	_ = json.Unmarshal(sent.Result.Value, &val)
	if val["approvalId"] != "appr-1" || val["outcome"] != "allowed-once" {
		t.Fatalf("approval value: %s", sent.Result.Value)
	}

	// Late/unknown approval id: first-writer-wins semantics — the respond
	// rides the official surface, the not-pending receipt settles it as an
	// honest no-op (never an error for the iOS submit).
	if err := sess.RespondPermission("appr-late", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("late respond must not error: %v", err)
	}
	f.lastRespond.mu.Lock()
	body2 := f.lastRespond.body
	f.lastRespond.mu.Unlock()
	var sent2 struct {
		RPCID  string        `json:"rpcId"`
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body2, &sent2); err != nil {
		t.Fatal(err)
	}
	if !sent2.Result.OK || sent2.RPCID != "appr-late" {
		t.Fatalf("late deny maps to the rejected outcome on the value branch: %+v", sent2)
	}
	var val2 map[string]any
	_ = json.Unmarshal(sent2.Result.Value, &val2)
	if val2["outcome"] != "rejected" {
		t.Fatalf("deny must map to rejected: %s", sent2.Result.Value)
	}
}

func TestApprovalExternalSessionNotSurfaced(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	bound := boundTestSession(t, f, a, "sess-bound") // bind SOME session

	// approval for an UNBOUND session: no surface, no pending registry entry.
	a.handleApprovalFrame(context.Background(), "rpc-x", "approval/requested", mustJSON(map[string]any{
		"sessionId": "sess-external", "approvalId": "appr-ext", "toolName": "write",
	}))
	assertNoEvent(t, bound.Events(), "external approval")

	// question for an UNBOUND session: same rule.
	a.handleQuestionFrame(context.Background(), "rpc-q", "question/requested", mustJSON(map[string]any{
		"sessionId": "sess-external",
		"questions": []map[string]any{{"id": "q-ext", "question": "外部？"}},
	}))
	assertNoEvent(t, bound.Events(), "external question")

	a.approvalsMu.Lock()
	n := len(a.approvals.batches)
	a.approvalsMu.Unlock()
	if n != 0 {
		t.Fatalf("external frames must not create pending state, got %d batches", n)
	}
}

func TestApprovalResolvedClosesPendingFirstWriterWins(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-fw")

	a.handleApprovalFrame(context.Background(), "rpc-fw", "approval/requested", mustJSON(map[string]any{
		"sessionId": "sess-fw", "approvalId": "appr-fw", "toolName": "edit",
	}))
	drainOne(t, sess.Events(), "permission_request")

	// The WEB answers first: the resolved frame arrives before our respond.
	a.handleApprovalFrame(context.Background(), "rpc-fw2", "approval/resolved", mustJSON(map[string]any{
		"sessionId": "sess-fw", "approvalId": "appr-fw", "outcome": "allowed-once",
	}))
	// The mux stream replays still-pending frames on reconnect — but this one
	// is settled, so a replayed approval/requested is a NEW pending entry only
	// if re-pushed after settle; our late iOS respond now hits not-pending,
	// which must surface as success (the outcome already stands).

	// Late respond returns nil (first-writer-wins, honest no-op).
	if err := sess.RespondPermission("appr-fw", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("late respond must not error: %v", err)
	}
}

func TestQuestionBatchFullSemantics(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-q")

	// Batch of 2 questions — BOTH must be visible with their own ids (R3-1).
	a.handleQuestionFrame(context.Background(), "batch-rpc-1", "question/requested", mustJSON(map[string]any{
		"sessionId": "sess-q",
		"questions": []map[string]any{
			{"id": "q1", "question": "选方案", "options": []map[string]any{{"label": "A 方案"}, {"label": "B 方案"}}},
			{"id": "q2", "question": "要不要跑测试", "header": "验证", "multiSelect": false},
		},
	}))
	q1 := drainOne(t, sess.Events(), "question q1")
	q2 := drainOne(t, sess.Events(), "question q2")
	if q1.Type != core.EventQuestionAsked || q1.QuestionID != "q1" || q1.QuestionText != "选方案" {
		t.Fatalf("q1: %+v", q1)
	}
	if q1.QuestionOpts[0].ID != "A 方案" || q1.QuestionOpts[0].Label != "A 方案" {
		t.Fatalf("options use labels as ids (dsh has no option ids): %+v", q1.QuestionOpts)
	}
	if q2.QuestionID != "q2" || q2.QuestionText != "验证：要不要跑测试" {
		t.Fatalf("q2: %+v", q2)
	}

	// Partial answer: accumulated, NO respond yet.
	if err := sess.RespondQuestion("q1", []string{"A 方案"}); err != nil {
		t.Fatalf("partial answer: %v", err)
	}
	f.requests.mu.Lock()
	respondCalls := 0
	for _, r := range f.requests.list {
		if r.method == "/api/respond" {
			respondCalls++
		}
	}
	f.requests.mu.Unlock()
	if respondCalls != 0 {
		t.Fatalf("batch must respond ONCE complete, got %d responds after partial", respondCalls)
	}

	// Duplicate submit overwrites (S-3): q1 re-answered, still no respond.
	if err := sess.RespondQuestion("q1", []string{"B 方案"}); err != nil {
		t.Fatalf("overwrite answer: %v", err)
	}

	// Completing q2 fires ONE respond keyed by per-question ids.
	if err := sess.RespondQuestion("q2", []string{"是"}); err != nil {
		t.Fatalf("complete answer: %v", err)
	}
	f.lastRespond.mu.Lock()
	body := f.lastRespond.body
	f.requests.mu.Lock()
	respondCalls = 0
	for _, r := range f.requests.list {
		if r.method == "/api/respond" {
			respondCalls++
		}
	}
	f.requests.mu.Unlock()
	f.lastRespond.mu.Unlock()
	if respondCalls != 1 {
		t.Fatalf("exactly one respond expected, got %d", respondCalls)
	}
	var sent struct {
		RPCID  string        `json:"rpcId"`
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.RPCID != "batch-rpc-1" {
		t.Fatalf("respond must echo the FRAME rpcId (batch key): %q", sent.RPCID)
	}
	if !sent.Result.OK {
		t.Fatalf("question answer rides the value branch: %+v", sent.Result)
	}
	var val struct {
		Answer struct {
			Answers []struct {
				ID       string   `json:"id"`
				Selected []string `json:"selected"`
				Custom   string   `json:"custom"`
			} `json:"answers"`
		} `json:"answer"`
	}
	_ = json.Unmarshal(sent.Result.Value, &val)
	if len(val.Answer.Answers) != 2 {
		t.Fatalf("both answers keyed by question id: %+v", val.Answer)
	}
	for _, ans := range val.Answer.Answers {
		switch ans.ID {
		case "q1":
			if len(ans.Selected) != 1 || ans.Selected[0] != "B 方案" {
				t.Fatalf("q1 answer must be the OVERWRITTEN one: %+v", ans)
			}
		case "q2":
			if len(ans.Selected) != 1 || ans.Selected[0] != "是" {
				t.Fatalf("q2 answer: %+v", ans)
			}
		default:
			t.Fatalf("unexpected answer id %q", ans.ID)
		}
	}

	// 中间态如实: submit succeeded but NO resolved event until the host frame.
	assertNoEvent(t, sess.Events(), "synthetic question_resolved")

	// Batch resolved frame (no per-question content) expands N resolved (S-1).
	a.handleQuestionFrame(context.Background(), "batch-rpc-1", "question/resolved", mustJSON(map[string]any{
		"sessionId": "sess-q", "questionRpcId": "batch-rpc-1", "outcome": "answered",
	}))
	r1 := drainOne(t, sess.Events(), "resolved q1")
	r2 := drainOne(t, sess.Events(), "resolved q2")
	if r1.Type != core.EventQuestionResolved || r1.QuestionID != "q1" || r1.Content != "answered" {
		t.Fatalf("r1: %+v", r1)
	}
	if r2.QuestionID != "q2" {
		t.Fatalf("r2: %+v", r2)
	}

	// Post-terminal answer attempts error honestly (state was cleared by the
	// resolved frame, so the question is no longer owned by any batch).
	if err := sess.RespondQuestion("q1", []string{"A 方案"}); err == nil {
		t.Fatal("post-terminal answer must error")
	}
}

func TestQuestionRejectCancelsWholeBatchViaErrorBranch(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-rj")

	a.handleQuestionFrame(context.Background(), "batch-rpc-rj", "question/requested", mustJSON(map[string]any{
		"sessionId": "sess-rj",
		"questions": []map[string]any{
			{"id": "r1", "question": "一"},
			{"id": "r2", "question": "二"},
		},
	}))
	drainOne(t, sess.Events(), "question r1")
	drainOne(t, sess.Events(), "question r2")

	// Answering one question first must NOT send anything; rejecting the
	// OTHER cancels the WHOLE batch through the error branch (asymmetry).
	if err := sess.RespondQuestion("r1", []string{"ok"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.RejectQuestion("r2"); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}
	f.lastRespond.mu.Lock()
	body := f.lastRespond.body
	f.lastRespond.mu.Unlock()
	var sent struct {
		RPCID  string        `json:"rpcId"`
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.RPCID != "batch-rpc-rj" || sent.Result.OK || sent.Result.Error == nil || sent.Result.Error.Code != "cancelled" {
		t.Fatalf("reject must cancel the whole batch via ok:false cancelled: rpcId=%q result=%+v", sent.RPCID, sent.Result)
	}
	// Both questions are terminal now.
	if err := sess.RespondQuestion("r1", []string{"late"}); err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("cancelled batch must reject further answers: %v", err)
	}
}

func TestQuestionReconnectReplayIsIdempotent(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-rc")

	frame := mustJSON(map[string]any{
		"sessionId": "sess-rc",
		"questions": []map[string]any{{"id": "rc1", "question": "重连"}},
	})
	// First delivery.
	a.handleQuestionFrame(context.Background(), "batch-rc", "question/requested", frame)
	drainOne(t, sess.Events(), "question rc1 (first)")
	// Partial answer, then the mux reconnect replays the same frame (S-2).
	if err := sess.RespondQuestion("rc1", []string{"已答"}); err != nil {
		t.Fatal(err)
	}
	a.handleQuestionFrame(context.Background(), "batch-rc", "question/requested", frame)
	ev := drainOne(t, sess.Events(), "question rc1 (replay)")
	if ev.QuestionID != "rc1" {
		t.Fatalf("replay event: %+v", ev)
	}
	// The batch was answered before the replay — the replayed batch state
	// carries responded=true, so the COMPLETE condition does not re-send.
	// (Single-question batch: the first respond already fired.)
	f.requests.mu.Lock()
	respondCalls := 0
	for _, r := range f.requests.list {
		if r.method == "/api/respond" {
			respondCalls++
		}
	}
	f.requests.mu.Unlock()
	if respondCalls != 1 {
		t.Fatalf("replay must not re-respond (responds=%d)", respondCalls)
	}

	// Host resolved for the replayed batch expands and clears state.
	a.handleQuestionFrame(context.Background(), "batch-rc", "question/resolved", mustJSON(map[string]any{
		"sessionId": "sess-rc", "questionRpcId": "batch-rc", "outcome": "answered",
	}))
	if ev := drainOne(t, sess.Events(), "resolved rc1"); ev.QuestionID != "rc1" {
		t.Fatalf("resolved: %+v", ev)
	}
}
