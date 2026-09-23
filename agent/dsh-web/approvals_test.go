package dshweb

// §8-4 unit tests: approval chain (waterfall→permission event→$events/result
// correlation→resolved close), external-session routing, first-writer-wins,
// and the full batch-question semantics (per-question ids, one respond when
// complete, settle expansion, reject asymmetry, overwrite idempotency,
// replay).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// boundTestSession starts a session bound to the fake and returns it.
func boundTestSession(t *testing.T, f *fakeDSHServer, a *Agent, sessionID string) *dshSession {
	t.Helper()
	f.handlers["session/create"] = fakeRPCResponse{value: map[string]any{"sessionId": sessionID}}
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

func drainOf(t *testing.T, ch <-chan core.Event, typ core.EventType, what string) core.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("%s never arrived (want %s)", what, typ)
			return core.Event{}
		}
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

// setMuxClientID binds the $events generation id the way the ready frame
// does in production (unit tests surface waterfalls without the pump).
func setMuxClientID(a *Agent, id string) {
	a.streamMu.Lock()
	a.muxClientID = id
	a.streamMu.Unlock()
}

// approvalWaterfall builds one approval/request waterfall frame.
func approvalWaterfall(eventID, sessionID string, request map[string]any) remoteEventFrame {
	return remoteEventFrame{
		Type:    "waterfall",
		Event:   "approval/request",
		EventID: eventID,
		AgentID: sessionID,
		Request: mustJSON(request),
	}
}

// questionWaterfall builds one user-questions/request waterfall frame.
func questionWaterfall(eventID, sessionID string, questions []map[string]any) remoteEventFrame {
	return remoteEventFrame{
		Type:    "waterfall",
		Event:   "user-questions/request",
		EventID: eventID,
		AgentID: sessionID,
		Request: mustJSON(map[string]any{"questions": questions}),
	}
}

func TestSessionCloseUnblocksEventsChannel(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-close")
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := <-sess.Events(); ok {
		t.Fatal("Events() must close so relayEvents can exit after idle eviction")
	}
}

func TestApprovalChainSurfacesAndResponds(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-appr")

	// approval/request for a BOUND session surfaces a permission request.
	a.handleApprovalWaterfall(approvalWaterfall("ev-appr-1", "sess-appr", map[string]any{
		"toolName": "bash", "callId": "c9",
		"reason": "escalate sandbox to danger-full-access: 超出工作区",
	}))
	ev := drainOne(t, sess.Events(), "permission_request")
	if ev.Type != core.EventPermissionRequest || ev.RequestID != "ev-appr-1" || ev.ToolName != "bash" {
		t.Fatalf("permission event: %+v", ev)
	}
	if ev.Content != "escalate sandbox to danger-full-access: 超出工作区" {
		t.Fatalf("reason not plumbed: %+v", ev)
	}

	// iOS allow → $events/result carries the bare outcome string.
	if err := sess.RespondPermission("ev-appr-1", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	f.lastEventResult.mu.Lock()
	args := f.lastEventResult.args
	f.lastEventResult.mu.Unlock()
	if args.EventID != "ev-appr-1" || args.ClientID != "fake-client-1" {
		t.Fatalf("$events/result correlation: %+v", args)
	}
	if args.Outcome.Kind != "result" || string(args.Outcome.Value) != `"allowed-once"` {
		t.Fatalf("approval answer must be the bare outcome string: %+v", args.Outcome)
	}

	// The accepted respond settles the card locally (permission_resolved).
	resolved := drainOne(t, sess.Events(), "permission_resolved")
	if resolved.Type != core.EventPermissionResolved || resolved.RequestID != "ev-appr-1" || resolved.Content != "allow" {
		t.Fatalf("permission_resolved: %+v", resolved)
	}

	// Late/unknown approval id: first-writer-wins semantics — the pending
	// entry is gone, so the respond is an honest no-op (never an error for
	// the iOS submit).
	if err := sess.RespondPermission("ev-appr-late", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("late respond must not error: %v", err)
	}
	f.eventResults.mu.Lock()
	n := len(f.eventResults.list)
	f.eventResults.mu.Unlock()
	if n != 1 {
		t.Fatalf("late respond must not reach the wire (results=%d)", n)
	}
}

func TestApprovalExternalSessionSurfacesOnPassive(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	bound := boundTestSession(t, f, a, "sess-bound") // bind SOME session

	// Mac-initiated turn: no StartSession binding. Still surface on the
	// agent passive channel so an observing iPhone can approve.
	a.handleApprovalWaterfall(approvalWaterfall("ev-ext", "sess-external", map[string]any{
		"toolName": "write",
		"reason":   "escalate sandbox to danger-full-access: 超出工作区",
	}))
	assertNoEvent(t, bound.Events(), "must not double-deliver to a different binding")
	ev := drainOne(t, a.passiveEvents(), "external approval via passive")
	if ev.Type != core.EventPermissionRequest || ev.RequestID != "ev-ext" || ev.SessionID != "sess-external" {
		t.Fatalf("passive permission: %+v", ev)
	}
	if ev.Content == "" {
		t.Fatal("reason must ride the passive permission event")
	}
	if err := a.RespondSessionPermission(context.Background(), "sess-external", "ev-ext", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("observe-only respond: %v", err)
	}

	// question for an UNBOUND session also goes to passive (same product rule).
	a.handleQuestionWaterfall(questionWaterfall("ev-q-ext", "sess-external", []map[string]any{
		{"id": "q-ext", "question": "外部？"},
	}))
	assertNoEvent(t, bound.Events(), "must not double-deliver question to a different binding")
	uiex := drainOf(t, a.passiveEvents(), core.EventUserInputRequested, "external user_input via passive")
	if uiex.UserInput == nil || uiex.UserInput.InteractionID != "q-ext" {
		t.Fatalf("passive user_input: %+v", uiex)
	}
	qev := drainOf(t, a.passiveEvents(), core.EventQuestionAsked, "external question via passive")
	if qev.Type != core.EventQuestionAsked || qev.QuestionID != "q-ext" {
		t.Fatalf("passive question: %+v", qev)
	}

	a.approvalsMu.Lock()
	n := len(a.approvals.batches)
	a.approvalsMu.Unlock()
	if n == 0 {
		t.Fatal("external question must stay pending so iOS can answer")
	}
}

func TestApprovalCancelFrameClosesPendingFirstWriterWins(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-fw")

	a.handleApprovalWaterfall(approvalWaterfall("ev-fw", "sess-fw", map[string]any{"toolName": "edit"}))
	drainOne(t, sess.Events(), "permission_request")

	// The WEB answers first: the Host cancels our waterfall (cancel frame).
	a.closePendingInteraction("ev-fw", "cancelled")
	resolved := drainOne(t, sess.Events(), "permission_resolved")
	if resolved.Type != core.EventPermissionResolved || resolved.RequestID != "ev-fw" {
		t.Fatalf("permission_resolved: %+v", resolved)
	}
	if resolved.Content == "allow" {
		t.Fatal("withdrawn approval must never claim allow")
	}
	// Late respond returns nil (first-writer-wins, honest no-op).
	if err := sess.RespondPermission("ev-fw", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("late respond must not error: %v", err)
	}
}

func TestApprovalAnsweredElsewhereSettlesAsSuccess(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-else")

	a.handleApprovalWaterfall(approvalWaterfall("ev-else", "sess-else", map[string]any{"toolName": "bash"}))
	drainOne(t, sess.Events(), "permission_request")

	// The gateway rejects our respond: the waterfall was claimed elsewhere.
	f.SetEventResultErr(&RPCError{
		Code: "gateway/internal", Message: "typert gateway: Remote event result identifies no active event stream",
		Details: mustJSON(map[string]any{}),
	})
	if err := sess.RespondPermission("ev-else", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("answered-elsewhere must not error for iOS: %v", err)
	}
	resolved := drainOne(t, sess.Events(), "permission_resolved")
	if resolved.Type != core.EventPermissionResolved || resolved.RequestID != "ev-else" {
		t.Fatalf("permission_resolved: %+v", resolved)
	}
}

func TestQuestionBatchFullSemantics(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-q")

	// Batch of 2 questions — BOTH must be visible with their own ids (R3-1).
	a.handleQuestionWaterfall(questionWaterfall("ev-batch-1", "sess-q", []map[string]any{
		{"id": "q1", "question": "选方案", "options": []map[string]any{{"label": "A 方案"}, {"label": "B 方案"}}},
		{"id": "q2", "question": "要不要跑测试", "header": "验证", "multiSelect": false},
	}))
	ui1 := drainOf(t, sess.Events(), core.EventUserInputRequested, "user_input q1")
	q1 := drainOf(t, sess.Events(), core.EventQuestionAsked, "question q1")
	q2 := drainOf(t, sess.Events(), core.EventQuestionAsked, "question q2")
	if ui1.UserInput == nil || ui1.UserInput.InteractionID != "q1" || ui1.UserInput.Questions[0].AnswerMode != core.UserInputAnswerModeSingle {
		t.Fatalf("canonical q1: %+v", ui1.UserInput)
	}
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
	f.eventResults.mu.Lock()
	results := len(f.eventResults.list)
	f.eventResults.mu.Unlock()
	if results != 0 {
		t.Fatalf("batch must respond ONCE complete, got %d results after partial", results)
	}

	// Duplicate submit overwrites (S-3): q1 re-answered, still no respond.
	if err := sess.RespondQuestion("q1", []string{"B 方案"}); err != nil {
		t.Fatalf("overwrite answer: %v", err)
	}

	// Completing q2 fires ONE $events/result keyed by per-question ids.
	if err := sess.RespondQuestion("q2", []string{"是"}); err != nil {
		t.Fatalf("complete answer: %v", err)
	}
	f.lastEventResult.mu.Lock()
	args := f.lastEventResult.args
	f.eventResults.mu.Lock()
	results = len(f.eventResults.list)
	f.eventResults.mu.Unlock()
	f.lastEventResult.mu.Unlock()
	if results != 1 {
		t.Fatalf("exactly one $events/result expected, got %d", results)
	}
	if args.EventID != "ev-batch-1" || args.ClientID != "fake-client-1" {
		t.Fatalf("result correlation: %+v", args)
	}
	if args.Outcome.Kind != "result" {
		t.Fatalf("question answer rides the result branch: %+v", args.Outcome)
	}
	var val struct {
		Answers []struct {
			ID       string   `json:"id"`
			Selected []string `json:"selected"`
			Custom   string   `json:"custom"`
		} `json:"answers"`
	}
	_ = jsonUnmarshal(args.Outcome.Value, &val)
	if len(val.Answers) != 2 {
		t.Fatalf("both answers keyed by question id: %+v", val.Answers)
	}
	for _, ans := range val.Answers {
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

	// The accepted respond settles the batch locally (S-1 expansion — one
	// resolved event per question id so each iOS pending card closes).
	r1 := drainOf(t, sess.Events(), core.EventQuestionResolved, "resolved q1")
	r2 := drainOf(t, sess.Events(), core.EventQuestionResolved, "resolved q2")
	if r1.Type != core.EventQuestionResolved || r1.QuestionID != "q1" || r1.Content != "answered" {
		t.Fatalf("r1: %+v", r1)
	}
	if r2.QuestionID != "q2" {
		t.Fatalf("r2: %+v", r2)
	}

	// Post-terminal answer attempts error honestly (state was cleared by the
	// settle, so the question is no longer owned by any batch).
	if err := sess.RespondQuestion("q1", []string{"A 方案"}); err == nil {
		t.Fatal("post-terminal answer must error")
	}
}

func TestQuestionRejectCancelsWholeBatchViaRejectedOutcome(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-rj")

	a.handleQuestionWaterfall(questionWaterfall("ev-rj", "sess-rj", []map[string]any{
		{"id": "r1", "question": "一"},
		{"id": "r2", "question": "二"},
	}))
	drainOf(t, sess.Events(), core.EventQuestionAsked, "question r1")
	drainOf(t, sess.Events(), core.EventQuestionAsked, "question r2")

	// Answering one question first must NOT send anything; rejecting the
	// OTHER cancels the WHOLE batch through the rejected outcome (asymmetry).
	if err := sess.RespondQuestion("r1", []string{"ok"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.RejectQuestion("r2"); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}
	f.lastEventResult.mu.Lock()
	args := f.lastEventResult.args
	f.lastEventResult.mu.Unlock()
	if args.EventID != "ev-rj" || args.Outcome.Kind != "rejected" || args.Outcome.Error == nil {
		t.Fatalf("reject must cancel the whole batch via the rejected outcome: %+v", args)
	}
	// The cancelled batch settles both questions.
	r1 := drainOf(t, sess.Events(), core.EventQuestionResolved, "resolved r1")
	if r1.QuestionID != "r1" {
		t.Fatalf("r1 settle: %+v", r1)
	}
	drainOf(t, sess.Events(), core.EventQuestionResolved, "resolved r2")
	// Both questions are terminal now.
	if err := sess.RespondQuestion("r1", []string{"late"}); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("cancelled batch must reject further answers: %v", err)
	}
}

func TestQuestionReconnectReplayIsIdempotent(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-rc")

	// First delivery.
	a.handleQuestionWaterfall(questionWaterfall("ev-rc-1", "sess-rc", []map[string]any{
		{"id": "rc1", "question": "重连"},
	}))
	drainOf(t, sess.Events(), core.EventQuestionAsked, "question rc1 (first)")
	// Partial answer, then the mux reconnect replays the ask under a NEW
	// waterfall id (S-2): the question ids are what iOS dedups on.
	if err := sess.RespondQuestion("rc1", []string{"已答"}); err != nil {
		t.Fatal(err)
	}
	a.handleQuestionWaterfall(questionWaterfall("ev-rc-2", "sess-rc", []map[string]any{
		{"id": "rc1", "question": "重连"},
	}))
	ev := drainOf(t, sess.Events(), core.EventQuestionAsked, "question rc1 (replay)")
	if ev.QuestionID != "rc1" {
		t.Fatalf("replay event: %+v", ev)
	}
	// The first batch was answered before the replay — its state carries
	// responded=true, so the replayed batch does not re-send. (Single-question
	// batch: the first respond already fired.)
	f.eventResults.mu.Lock()
	respondCalls := len(f.eventResults.list)
	f.eventResults.mu.Unlock()
	if respondCalls != 1 {
		t.Fatalf("replay must not re-respond (results=%d)", respondCalls)
	}

	// A generation loss settles the replayed batch and clears state.
	a.dropAllPendingInteractions()
	if ev := drainOf(t, sess.Events(), core.EventQuestionResolved, "resolved rc1"); ev.QuestionID != "rc1" {
		t.Fatalf("resolved: %+v", ev)
	}
}

func TestQuestionMultiSelectProjectsCanonicalMultiple(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	a.handleQuestionWaterfall(questionWaterfall("ev-ms", "sess-ms", []map[string]any{{
		"id":          "q-ms",
		"header":      "测试多选",
		"question":    "西游记小故事.txt 已存在。请选择您想执行哪些操作：",
		"multiSelect": true,
		"options": []map[string]any{
			{"label": "保留现状", "description": "不做任何改动"},
			{"label": "覆盖西游记小故事.txt", "description": "用新版本替换其内容"},
		},
	}}))
	ev := drainOf(t, a.passiveEvents(), core.EventUserInputRequested, "multi-select user_input")
	if ev.UserInput == nil || len(ev.UserInput.Questions) != 1 {
		t.Fatalf("user_input: %+v", ev.UserInput)
	}
	q := ev.UserInput.Questions[0]
	if q.AnswerMode != core.UserInputAnswerModeMultiple || q.Header != "测试多选" || len(q.Options) != 2 {
		t.Fatalf("multi-select question = %+v", q)
	}
	if q.Options[0].ID != "保留现状" || !q.AllowsCustomAnswer {
		t.Fatalf("options/custom = %+v", q)
	}
}
