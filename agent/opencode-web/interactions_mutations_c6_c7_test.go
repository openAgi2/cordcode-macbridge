package opencodeweb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// interactions_mutations_c6_c7_test.go owns the C6 (§6.7/§6.8/§6.9) and C7
// (§6.10) boundaries. Fixtures replay the archived A6/A7/A8/E6/E7 shapes.

// ── §6.7 permission ──────────────────────────────────────────────────────────

// TestPermissionReplyExactBodies pins the three official response bodies
// (A6): allow→{"response":"once"}, always→{"response":"always"},
// deny→{"response":"reject"} — posted to /session/{sid}/permissions/{id}.
func TestPermissionReplyExactBodies(t *testing.T) {
	agent, serve := newDataAgent(t, map[string]string{
		"/session/ses_p/permissions/perm_1": `{}`,
		"/session/ses_p/permissions/perm_2": `{}`,
		"/session/ses_p/permissions/perm_3": `{}`,
	}, "/tmp")
	cases := []struct {
		behavior string
		wantBody string
	}{
		{"allow", `{"response":"once"}`},
		{"always", `{"response":"always"}`},
		{"deny", `{"response":"reject"}`},
	}
	for i, tc := range cases {
		reqID := fmt.Sprintf("perm_%d", i+1)
		if err := agent.RespondSessionPermission(context.Background(), "ses_p", reqID, core.PermissionResult{Behavior: tc.behavior}); err != nil {
			t.Fatalf("%s: %v", tc.behavior, err)
		}
		posts := countRequests(serve, "POST", "/session/ses_p/permissions/")
		var body string
		for _, p := range posts {
			if strings.HasSuffix(p.Path, "/"+reqID) {
				body = p.Body
			}
		}
		if body != tc.wantBody {
			t.Fatalf("%s reply body = %s, want %s", tc.behavior, body, tc.wantBody)
		}
	}
}

// TestPermissionRawControlWritesZeroTimeline: answering a permission is a
// control-plane POST only — no SSE ingest, no timeline events.
func TestPermissionRawControlWritesZeroTimeline(t *testing.T) {
	agent, serve := newDataAgent(t, map[string]string{
		"/session/ses_p/permissions/perm_1": `{}`,
	}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	if err := agent.RespondSessionPermission(context.Background(), "ses_p", "perm_1", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	if events := drain(sub); len(events) != 0 {
		t.Fatalf("permission reply must not synthesize timeline events, got %+v", events)
	}
	for _, eventPath := range []string{"/global/event", "/api/event"} {
		if reqs := serve.requestsFor(eventPath); len(reqs) != 0 {
			t.Fatalf("permission reply must not open event streams, got %+v", reqs)
		}
	}
}

// ── §6.8 structured questions ────────────────────────────────────────────────

func questionAgent(t *testing.T, responses map[string]string) (*Agent, *recordingServe) {
	t.Helper()
	return newDataAgent(t, responses, "/tmp")
}

const a7AskedFrame = `{"payload":{"type":"question.asked","properties":{"id":"que_1","sessionID":"ses_q","questions":[{"question":"Which fixture color?","header":"Color","options":[{"label":"red","description":"Stop"},{"label":"green","description":"Go"}],"multiple":false}],"tool":{"messageID":"msg_1","callID":"call_1"}}}}`

// TestQuestionAskedMapsToCanonicalUserInput: the A7 asked frame translates
// once into EventUserInputRequested with deterministic option ids and the
// pending registry armed.
func TestQuestionAskedMapsToCanonicalUserInput(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
	sub := newDrivenSubscriber(t, agent)
	sub.handleRawEvent(a7AskedFrame)

	events := drain(sub)
	if len(events) != 1 || events[0].Type != core.EventUserInputRequested {
		t.Fatalf("asked must emit exactly one user_input_requested, got %+v", events)
	}
	ui := events[0].UserInput
	if ui == nil || ui.InteractionID != "que_1" || ui.Status != core.UserInputStatusPending || !ui.CanRespond || !ui.CanReject {
		t.Fatalf("interaction = %+v", ui)
	}
	if len(ui.Questions) != 1 {
		t.Fatalf("questions = %+v", ui.Questions)
	}
	q := ui.Questions[0]
	if q.ID != "que_1/q0" || q.Prompt != "Which fixture color?" || q.Header != "Color" || q.AnswerMode != core.UserInputAnswerModeSingle {
		t.Fatalf("question = %+v", q)
	}
	if len(q.Options) != 2 || q.Options[0].ID != "que_1/q0/o0" || q.Options[0].Label != "red" || q.Options[1].Label != "green" {
		t.Fatalf("options = %+v", q.Options)
	}
}

// TestQuestionReplySendsOfficialAnswersBody: resolving with the red option
// POSTs {"answers":[["red"]]} to /question/que_1/reply (A7 body verbatim).
func TestQuestionReplySendsOfficialAnswersBody(t *testing.T) {
	agent, serve := questionAgent(t, map[string]string{
		"/question":           `[{"id":"que_1","sessionID":"ses_q","questions":[{"question":"Which fixture color?","header":"Color","options":[{"label":"red","description":"Stop"},{"label":"green","description":"Go"}],"multiple":false}]}]`,
		"/question/que_1/reply": `{}`,
	})
	res, err := agent.ResolveUserInput(context.Background(), "que_1", "act_1", core.UserInputActionAnswer, []core.UserInputAnswer{{
		QuestionID: "que_1/q0",
		Values:     []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "que_1/q0/o0"}},
	}})
	if err != nil {
		t.Fatalf("ResolveUserInput: %v", err)
	}
	if res.Outcome != core.UserInputOutcomeAccepted || res.CurrentStatus != core.UserInputStatusAnswered {
		t.Fatalf("resolution = %+v", res)
	}
	posts := countRequests(serve, "POST", "/question/que_1/reply")
	if len(posts) != 1 || posts[0].Body != `{"answers":[["red"]]}` {
		t.Fatalf("reply body must be the official {answers:[[label]]}, got %+v", posts)
	}
}

// TestQuestionRejectUsesOwnEndpoint (A7): POST /question/{id}/reject body {}.
func TestQuestionRejectUsesOwnEndpoint(t *testing.T) {
	agent, serve := questionAgent(t, map[string]string{
		"/question":             `[{"id":"que_1","sessionID":"ses_q","questions":[{"question":"q","options":[{"label":"a"},{"label":"b"}]}]}]`,
		"/question/que_1/reject": `{}`,
	})
	res, err := agent.ResolveUserInput(context.Background(), "que_1", "act_1", core.UserInputActionReject, nil)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if res.Outcome != core.UserInputOutcomeAccepted || res.CurrentStatus != core.UserInputStatusRejected {
		t.Fatalf("resolution = %+v", res)
	}
	posts := countRequests(serve, "POST", "/question/que_1/reject")
	if len(posts) != 1 || posts[0].Body != `{}` {
		t.Fatalf("reject must POST {} to its own endpoint, got %+v", posts)
	}
}

// TestQuestionResolutionEventsFromServer: question.replied/rejected map to
// user_input_resolved with resolutionSource=other_client; no
// question_resolved event is invented (1.18.18 emits replied/rejected only).
func TestQuestionResolutionEventsFromServer(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
	sub := newDrivenSubscriber(t, agent)
	sub.handleRawEvent(a7AskedFrame)
	drain(sub)

	sub.handleRawEvent(`{"payload":{"type":"question.replied","properties":{"sessionID":"ses_q","requestID":"que_1","answers":[["red"]]}}}`)
	sub.handleRawEvent(`{"payload":{"type":"question.rejected","properties":{"sessionID":"ses_q2","requestID":"que_2"}}}`)

	events := drain(sub)
	if len(events) != 2 || events[0].Type != core.EventUserInputResolved || events[1].Type != core.EventUserInputResolved {
		t.Fatalf("replied+rejected must each map to user_input_resolved, got %+v", events)
	}
	if events[0].UserInput.Status != core.UserInputStatusAnswered || events[0].UserInput.ResolutionSource != "other_client" {
		t.Fatalf("replied = %+v", events[0].UserInput)
	}
	if events[1].UserInput.Status != core.UserInputStatusRejected || events[1].UserInput.InteractionID != "que_2" {
		t.Fatalf("rejected = %+v", events[1].UserInput)
	}
}

// TestQuestionInvalidAnswersFailClosed: unknown option, unknown question,
// and a missing answer each fail with the stable §7 codes and ZERO POSTs.
func TestQuestionInvalidAnswersFailClosed(t *testing.T) {
	agent, serve := questionAgent(t, map[string]string{
		"/question": `[{"id":"que_1","sessionID":"ses_q","questions":[{"question":"q","options":[{"label":"red"},{"label":"green"}]}]}]`,
	})
	cases := []struct {
		name    string
		answers []core.UserInputAnswer
		wantErr string
	}{
		{"unknown option", []core.UserInputAnswer{{QuestionID: "que_1/q0", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "que_1/q0/o9"}}}}, "unknown option"},
		{"unknown question", []core.UserInputAnswer{{QuestionID: "que_1/q7", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "que_1/q0/o0"}}}}, "unknown question"},
		{"no answers", nil, "no answer"},
	}
	for _, tc := range cases {
		_, err := agent.ResolveUserInput(context.Background(), "que_1", "act", core.UserInputActionAnswer, tc.answers)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s must fail closed, got %v", tc.name, err)
		}
	}
	if posts := countRequests(serve, "POST", "/question"); len(posts) != 0 {
		t.Fatalf("invalid answers must yield ZERO POSTs, got %+v", posts)
	}
	// Unknown interaction id: stable interaction_not_found, no POST.
	if _, err := agent.ResolveUserInput(context.Background(), "que_ghost", "act", core.UserInputActionAnswer, nil); err == nil || !strings.Contains(err.Error(), "no pending question") {
		t.Fatalf("unknown interaction must fail diagnosably, got %v", err)
	}
}

// ── §6.9 todos ───────────────────────────────────────────────────────────────

// TestTodoEndpointReplayPreservesOrderAndFields: the A8 endpoint shape (items
// exactly {content,status,priority}) maps verbatim — order preserved, no ids
// invented (core.Todo has none), malformed rows fail.
func TestTodoEndpointReplayPreservesOrderAndFields(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{
		"/session/ses_t/todo": `[{"content":"capture A8","status":"pending","priority":"high"},{"content":"complete A8","status":"in_progress","priority":"medium"}]`,
	})
	todos, err := agent.FetchTodos(context.Background(), "ses_t")
	if err != nil {
		t.Fatalf("FetchTodos: %v", err)
	}
	if len(todos) != 2 || todos[0].Content != "capture A8" || todos[0].Status != "pending" || todos[0].Priority != "high" {
		t.Fatalf("todos = %+v", todos)
	}
	if todos[1].Status != "in_progress" {
		t.Fatalf("second row status = %+v", todos[1])
	}

	bad, _ := questionAgent(t, map[string]string{
		"/session/ses_t/todo": `[{"status":"pending"}]`,
	})
	if _, err := bad.FetchTodos(context.Background(), "ses_t"); err == nil || !strings.Contains(err.Error(), "missing required content") {
		t.Fatalf("row without content must fail, got %v", err)
	}
	env, _ := questionAgent(t, map[string]string{
		"/session/ses_t/todo": `{"data":[]}`,
	})
	if _, err := env.FetchTodos(context.Background(), "ses_t"); err == nil || !strings.Contains(err.Error(), "bare array") {
		t.Fatalf("envelope shape must fail, got %v", err)
	}
}

// ── §6.10 rename / delete / archive ──────────────────────────────────────────

// TestRenameSessionContract (E6): PATCH /session/:id body {title} → 200
// Session.Info with the new title; 404 surfaces NotFoundError verbatim.
func TestRenameSessionContract(t *testing.T) {
	agent, serve := questionAgent(t, map[string]string{
		"/session/ses_r": `{"id":"ses_r","title":"renamed","directory":"/tmp","time":{"created":1,"updated":2}}`,
	})
	info, err := agent.RenameSession(context.Background(), "ses_r", "renamed")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if info.ID != "ses_r" || info.Summary != "renamed" {
		t.Fatalf("returned metadata = %+v", info)
	}
	patches := countRequests(serve, "PATCH", "/session/ses_r")
	if len(patches) != 1 || patches[0].Body != `{"title":"renamed"}` {
		t.Fatalf("rename must PATCH the official {title} body, got %+v", patches)
	}

	// Empty title is refused before any request (official UpdatePayload).
	if _, err := agent.RenameSession(context.Background(), "ses_r", "  "); err == nil || !strings.Contains(err.Error(), "empty title") {
		t.Fatalf("empty title must fail pre-request, got %v", err)
	}
	// 404 NotFoundError preserved.
	agent2, _ := questionAgent(t, map[string]string{})
	if _, err := agent2.RenameSession(context.Background(), "ses_missing", "x"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("missing session rename must surface 404, got %v", err)
	}
}

// TestDeleteSessionConvergenceWithoutInventedEvents (E7): success is the
// boolean response + catalog signal; completion never depends on
// session.deleted, and a second delete surfaces the serve's 404.
func TestDeleteSessionConvergenceWithoutInventedEvents(t *testing.T) {
	agent, serve := questionAgent(t, map[string]string{
		"/session/ses_d": `true`,
	})
	if err := agent.DeleteSession(context.Background(), "ses_d"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	dels := countRequests(serve, "DELETE", "/session/ses_d")
	if len(dels) != 1 {
		t.Fatalf("exactly one DELETE expected, got %+v", dels)
	}
	// The catalog refresh signal (not a session.deleted event) is the
	// invalidation surface.
	select {
	case <-agent.CatalogRefreshSignals():
	default:
		t.Fatal("delete success must signal catalog refresh")
	}
	// Zero timeline synthesis: no SSE stream was opened for the mutation.
	for _, eventPath := range []string{"/global/event", "/api/event"} {
		if reqs := serve.requestsFor(eventPath); len(reqs) != 0 {
			t.Fatalf("mutation must not open event streams, got %+v", reqs)
		}
	}

	// Second delete on a 404-answering serve surfaces the error — never a
	// fabricated success.
	agent2, _ := questionAgent(t, map[string]string{})
	if err := agent2.DeleteSession(context.Background(), "ses_gone"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("second delete must surface 404, got %v", err)
	}
}

// TestMutationFailuresPreserveMetadata: failed mutations surface the error
// and return no fabricated metadata.
func TestMutationFailuresPreserveMetadata(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{})
	if _, err := agent.ArchiveSession(context.Background(), "ses_x", mustTime()); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("archive on missing session must fail visibly, got %v", err)
	}
}

func mustTime() time.Time { return time.Now().UTC() }
