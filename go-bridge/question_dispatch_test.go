package gobridge

// question_dispatch_test.go covers the dual-rail question_reply /
// question_reject dispatch (grokbuild follower questions, plan Phase 3):
//
//   - active session owns its own-turn (driver rail) questions; success stops
//     there (agent-level responder untouched);
//   - a session error (the question lives on the leader rail of an external
//     turn) falls through to the agent-level core.SessionQuestionResponder;
//   - no session + responder → responder handles (external-turn reply);
//   - no session + no responder → session_not_found;
//   - reducer policy: question_asked with no active turn is dropped
//     fail-closed (no phantom turn, no orphan card) — replay/cold-pull
//     re-surfaces it once identity exists.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// questionMockSession wraps mockSession with overridable question methods.
type questionMockSession struct {
	mockSession
	respondErr error
	rejectErr  error
}

func (m *questionMockSession) RespondQuestion(string, []string) error { return m.respondErr }
func (m *questionMockSession) RejectQuestion(string) error            { return m.rejectErr }

// questionResponderAgent embeds fakeAgent and adds the agent-level responder.
type questionResponderAgent struct {
	fakeAgent
	mu          sync.Mutex
	replyCalls  []string
	rejectCalls []string
	replyFunc   func(sessionID, questionID string, optionIDs []string) error
	rejectFunc  func(sessionID, questionID string) error
}

func (q *questionResponderAgent) RespondSessionQuestion(_ context.Context, sessionID, questionID string, optionIDs []string) error {
	q.mu.Lock()
	q.replyCalls = append(q.replyCalls, sessionID+"/"+questionID)
	q.mu.Unlock()
	if q.replyFunc != nil {
		return q.replyFunc(sessionID, questionID, optionIDs)
	}
	return nil
}

func (q *questionResponderAgent) RejectSessionQuestion(_ context.Context, sessionID, questionID string) error {
	q.mu.Lock()
	q.rejectCalls = append(q.rejectCalls, sessionID+"/"+questionID)
	q.mu.Unlock()
	if q.rejectFunc != nil {
		return q.rejectFunc(sessionID, questionID)
	}
	return nil
}

func questionReplyMsg(t *testing.T, backendID, sessionID, questionID string, optionIDs []string) WireMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"sessionId": sessionID, "questionId": questionID, "optionIds": optionIDs,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return WireMessage{RequestID: "req-1", BackendID: backendID, Method: "question_reply", Params: b}
}

func questionRejectMsg(t *testing.T, backendID, sessionID, questionID string) WireMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"sessionId": sessionID, "questionId": questionID})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return WireMessage{RequestID: "req-1", BackendID: backendID, Method: "question_reject", Params: b}
}

func TestQuestionReplySessionSuccessSkipsResponder(t *testing.T) {
	h := newTestHandlers(t)
	h.putSessionWithMeta("ses_ext", "grokbuild", "", &questionMockSession{})
	agent := &questionResponderAgent{}
	h.RegisterAgent("grokbuild", agent)

	conn := &userInputCaptureConn{}
	h.handleQuestionReply(conn, questionReplyMsg(t, "grokbuild", "ses_ext", "call_leader1", []string{"深色主题"}))

	if conn.wireErr != nil {
		t.Fatalf("wireErr = %+v, want success", conn.wireErr)
	}
	agent.mu.Lock()
	calls := len(agent.replyCalls)
	agent.mu.Unlock()
	if calls != 0 {
		t.Fatalf("agent responder called %d times, want 0 (session owned the reply)", calls)
	}
}

func TestQuestionReplySessionErrorFallsThroughToResponder(t *testing.T) {
	h := newTestHandlers(t)
	// Session exists (own turn finished, still registered) but the pending
	// question belongs to an external TUI turn (leader rail): the driver-rail
	// lookup must error and the dispatch must fall through to the responder.
	h.putSessionWithMeta("ses_ext", "grokbuild", "", &questionMockSession{
		respondErr: errors.New("grokbuild: no pending question call_leader1"),
	})
	agent := &questionResponderAgent{}
	h.RegisterAgent("grokbuild", agent)

	conn := &userInputCaptureConn{}
	h.handleQuestionReply(conn, questionReplyMsg(t, "grokbuild", "ses_ext", "call_leader1", []string{"深色主题"}))

	if conn.wireErr != nil {
		t.Fatalf("wireErr = %+v, want fallthrough success", conn.wireErr)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.replyCalls) != 1 || agent.replyCalls[0] != "ses_ext/call_leader1" {
		t.Fatalf("replyCalls = %v, want [ses_ext/call_leader1]", agent.replyCalls)
	}
}

func TestQuestionReplyNoSessionResponderHandles(t *testing.T) {
	h := newTestHandlers(t)
	agent := &questionResponderAgent{}
	h.RegisterAgent("grokbuild", agent)

	conn := &userInputCaptureConn{}
	h.handleQuestionReply(conn, questionReplyMsg(t, "grokbuild", "ses_ext", "call_leader1", []string{"咖啡"}))

	if conn.wireErr != nil {
		t.Fatalf("wireErr = %+v, want responder success", conn.wireErr)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.replyCalls) != 1 {
		t.Fatalf("replyCalls = %v", agent.replyCalls)
	}
}

func TestQuestionReplyBothRailsFailReturnsError(t *testing.T) {
	h := newTestHandlers(t)
	h.putSessionWithMeta("ses_ext", "grokbuild", "", &questionMockSession{
		respondErr: errors.New("grokbuild: no pending question call_x"),
	})
	agent := &questionResponderAgent{replyFunc: func(_, _ string, _ []string) error {
		return errors.New("grokbuild: no live subscriber for session")
	}}
	h.RegisterAgent("grokbuild", agent)

	conn := &userInputCaptureConn{}
	h.handleQuestionReply(conn, questionReplyMsg(t, "grokbuild", "ses_ext", "call_x", []string{"A"}))

	if conn.wireErr == nil || conn.wireErr.Code != "question_reply_failed" {
		t.Fatalf("wireErr = %+v, want question_reply_failed", conn.wireErr)
	}
}

func TestQuestionReplyNoSessionNoResponder(t *testing.T) {
	h := newTestHandlers(t)
	h.RegisterAgent("grokbuild", &fakeAgent{name: "grokbuild"})

	conn := &userInputCaptureConn{}
	h.handleQuestionReply(conn, questionReplyMsg(t, "grokbuild", "ses_none", "call_x", []string{"A"}))

	if conn.wireErr == nil || conn.wireErr.Code != "session_not_found" {
		t.Fatalf("wireErr = %+v, want session_not_found", conn.wireErr)
	}
}

func TestQuestionRejectFallsThroughToResponder(t *testing.T) {
	h := newTestHandlers(t)
	h.putSessionWithMeta("ses_ext", "grokbuild", "", &questionMockSession{
		rejectErr: errors.New("grokbuild: no pending question call_leader1"),
	})
	agent := &questionResponderAgent{}
	h.RegisterAgent("grokbuild", agent)

	conn := &userInputCaptureConn{}
	h.handleQuestionReject(conn, questionRejectMsg(t, "grokbuild", "ses_ext", "call_leader1"))

	if conn.wireErr != nil {
		t.Fatalf("wireErr = %+v, want fallthrough success", conn.wireErr)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.rejectCalls) != 1 || agent.rejectCalls[0] != "ses_ext/call_leader1" {
		t.Fatalf("rejectCalls = %v, want [ses_ext/call_leader1]", agent.rejectCalls)
	}
}

// Reducer policy lock: a question that arrives with no active turn anywhere in
// the projection is dropped fail-closed — no phantom turn is created. (Live
// grok turns always arm before the question — the ask is a tool call mid-turn —
// so this window is a cold-attach race; replay-on-attach / cold-pull
// re-surfaces the question once the turn exists.)
func TestReducerQuestionAskedWithoutTurnDroppedFailClosed(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "question_asked", map[string]interface{}{
		"questionId":   "call_410dc27a15f64707b7f36ca2",
		"questionText": "你偏好哪种配色主题?",
		"options": []interface{}{
			map[string]interface{}{"id": "深色主题", "label": "深色主题"},
		},
	}))
	if _, ok := r.FlushPatch("grokbuild", "s1"); ok {
		t.Fatal("identityless question must not produce a patch")
	}
	proj, ok := r.Snapshot("grokbuild", "s1")
	if !ok {
		t.Fatal("no projection snapshot")
	}
	if len(proj.Turns) != 0 {
		t.Fatalf("turns = %d, want 0 (no phantom turn)", len(proj.Turns))
	}
}

// The grokbuild leader-rail shape: question lands inside the armed synthetic
// turn (turn_started from the relay loop's first content event), replay of the
// same question id upserts the same card, resolved closes it as answered.
func TestReducerGrokQuestionLifecycleInsideSyntheticTurn(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "turn_started", map[string]interface{}{"turnId": "prompt-42"}))
	r.Apply(ev(2, "grokbuild", "s1", "tool_started", map[string]interface{}{"itemId": "call_410dc27a15f64707b7f36ca2", "toolName": "ask_user_question"}))
	ask := func(seq int) {
		r.Apply(ev(seq, "grokbuild", "s1", "question_asked", map[string]interface{}{
			"questionId":   "call_410dc27a15f64707b7f36ca2",
			"questionText": "你偏好哪种配色主题?",
			"options": []interface{}{
				map[string]interface{}{"id": "深色主题", "label": "深色主题", "description": "界面以深色背景为主,适合弱光环境"},
				map[string]interface{}{"id": "浅色主题", "label": "浅色主题", "description": "界面以浅色背景为主,适合明亮环境"},
			},
		}))
	}
	ask(3)
	// Replay-on-attach re-delivers the same id: same turn, same card (upsert, no duplicate).
	ask(4)
	r.Apply(ev(5, "grokbuild", "s1", "question_resolved", map[string]interface{}{
		"questionId": "call_410dc27a15f64707b7f36ca2",
		"result":     "resolved",
	}))

	proj, ok := r.Snapshot("grokbuild", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	if proj.Execution.ActiveTurnID != "prompt-42" {
		t.Fatalf("activeTurn = %q, want prompt-42", proj.Execution.ActiveTurnID)
	}
	cards := 0
	for _, turn := range proj.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, p := range turn.Assistant.Parts {
			if p.Type == "user_input" && p.UserInputInteractionID == "call_410dc27a15f64707b7f36ca2" {
				cards++
				if p.UserInputStatus != "answered" {
					t.Fatalf("status = %q, want answered", p.UserInputStatus)
				}
			}
		}
	}
	if cards != 1 {
		t.Fatalf("question cards = %d, want exactly 1 (replay upserts)", cards)
	}
}
