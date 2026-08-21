package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// newAuditHarnessFull is newAuditHarness plus the live harness agent (the
// resolve_user_input handler takes the registered core.Agent).
func newAuditHarnessFull(t *testing.T) (*Handlers, *ssePushServe, core.Agent) {
	t.Helper()
	h, serve, agent := newAuditHarnessWithOptions(t, func(*ssePushServe) {})
	return h, serve, agent
}

// audit011_fullpath_test.go owns the directive-011 question terminal
// reconciliation proofs. Every test runs the REAL adapter + Handlers relay +
// deltaBatcher + EventPublisher + Kernel stack and asserts PROJECTION state.
// Interleavings are barrier-controlled through the serve's response gates —
// never "call A, then call B" sequential stand-ins.

// a7AnsweredHistoryForOCW1 is the A7 reload evidence shape after a reply:
// the question tool part carries state.status=completed plus
// state.metadata.answers, keyed by messageID + callID.
const a7AnsweredHistoryForOCW1 = `[
	{"info":{"id":"msg_u1","sessionID":"ses_ocw1","role":"user","time":{"created":1}},"parts":[{"type":"text","text":"ask"}]},
	{"info":{"id":"msg_a7_tool","sessionID":"ses_ocw1","role":"assistant","parentID":"msg_u1","time":{"created":2}},"parts":[
		{"id":"prt_q","sessionID":"ses_ocw1","messageID":"msg_a7_tool","type":"tool","callID":"call_a7","tool":"question",
		 "state":{"status":"completed","input":{"questions":[{"question":"Which fixture color?","header":"Color","options":[{"label":"red","description":"Stop"},{"label":"green","description":"Go"}],"multiple":false}]},"output":"User has answered your questions: \"Which fixture color?\"=\"red\".","metadata":{"answers":[["red"]],"truncated":false}}}
	]}
]`

// a7RejectedHistoryForOCW1 is the A7 reject evidence: state.status=error with
// the official RejectedError text.
const a7RejectedHistoryForOCW1 = `[
	{"info":{"id":"msg_u1","sessionID":"ses_ocw1","role":"user","time":{"created":1}},"parts":[{"type":"text","text":"ask"}]},
	{"info":{"id":"msg_a7_tool","sessionID":"ses_ocw1","role":"assistant","parentID":"msg_u1","time":{"created":2}},"parts":[
		{"id":"prt_q","sessionID":"ses_ocw1","messageID":"msg_a7_tool","type":"tool","callID":"call_a7","tool":"question",
		 "state":{"status":"error","input":{"questions":[{"question":"Which fixture color?","header":"Color","options":[{"label":"red","description":"Stop"}],"multiple":false}]},"error":"The user dismissed this question"}}
	]}
]`

// TestAudit011_GapLostTerminalReconcilesInPlace is THE audit-010 product gap:
// the pending dock is projected; the SSE stream drops; the server resolves the
// question during the gap (broadcast lost); the reconnect GET /question is
// empty and the A7 history carries the terminal tool. The SAME part must
// settle answered/rejected in place — no permanent pending dock.
func TestAudit011_GapLostTerminalReconcilesInPlace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		history string
		want    string
	}{
		{"answered", a7AnsweredHistoryForOCW1, "answered"},
		{"rejected", a7RejectedHistoryForOCW1, "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, serve := newAuditHarness(t)

			armA7QuestionFacts(serve)
			pushA7Asked(serve)
			waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool {
				return len(p) == 1 && p[0].Status == "pending"
			})

			// The resolution happens entirely inside the stream gap: no
			// replied/rejected frame is ever delivered live.
			serve.setPendingQuestions("[]")
			serve.setHistory("ses_ocw1", tc.history)
			serve.drop()

			parts := waitForUserInput(t, h, "in-place terminal after reconnect", func(p []userInputPartView) bool {
				return len(p) == 1 && p[0].Status == tc.want
			})
			if parts[0].InteractionID != "que_a7" || parts[0].TurnID != "msg_u1" {
				t.Fatalf("identity drifted during reconciliation: %+v", parts[0])
			}
		})
	}
}

// TestAudit011_RecoveryRequestedCannotOverwriteTerminal (barrier): recovery
// has fetched the pending row but parks before emitting (history gate); the
// live terminal broadcast lands first; releasing the recovery must NOT
// re-project the stale requested over the terminal.
func TestAudit011_RecoveryRequestedCannotOverwriteTerminal(t *testing.T) {
	h, serve, agent := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
		s.setPendingQuestions(a7PendingRowForOCW1)
		s.setHistory("ses_ocw1", a7HistoryForOCW1)
	})

	// The live asked projects the pending dock first.
	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "pending"
	})

	// A second route establishment runs a recovery that parks while its GET
	// /question is in flight (the pending row is not decoded yet, so any
	// emission is strictly after whatever lands during the park). An idle
	// frame cannot be used to steer this — it would exit the opencode-web
	// relay at the turn boundary and orphan later events.
	questionGate := make(chan struct{})
	serve.mu.Lock()
	serve.questionGate = questionGate
	serve.mu.Unlock()
	type startResult struct {
		err error
	}
	done := make(chan startResult, 1)
	go func() {
		sess, err := ocwebStartSession(t, agent, "ses_ocw1")
		if sess != nil {
			defer sess.Close()
		}
		done <- startResult{err: err}
	}()
	waitForQuestionPark(t, serve)

	// The terminal broadcast lands while the recovery is parked.
	serve.push(map[string]any{"type": "question.replied", "properties": map[string]any{
		"sessionID": "ses_ocw1", "requestID": "que_a7", "answers": [][]string{{"red"}}}})
	waitForUserInput(t, h, "terminal before release", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "answered"
	})

	// Release the parked recovery: the stale requested must be fenced out.
	close(questionGate)
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("second StartSession: %v", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery never finished after release")
	}

	// Settle window: a fenced-out recovery stays answered for the whole
	// window; a stale requested would flip the part within milliseconds.
	deadline := time.After(700 * time.Millisecond)
	for {
		parts := userInputPartViews(t, h)
		if len(parts) != 1 {
			t.Fatalf("part count drift: %+v", parts)
		}
		if parts[0].Status != "answered" {
			t.Fatalf("recovery re-projected a stale requested over the terminal: %+v", parts[0])
		}
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestAudit011_StaleEmptySnapshotKeepsNewerLiveAsk (barrier): the recovery
// snapshot is taken (empty); a NEW live asked lands while the recovery is
// parked; releasing the recovery must keep the new pending — an old empty
// snapshot must never clear a newer ask.
func TestAudit011_StaleEmptySnapshotKeepsNewerLiveAsk(t *testing.T) {
	h, serve, agent := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
		s.setHistory("ses_ocw1", a7HistoryForOCW1)
	})

	questionGate := make(chan struct{})
	serve.mu.Lock()
	serve.questionGate = questionGate
	serve.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		sess, err := ocwebStartSession(t, agent, "ses_ocw1")
		if sess != nil {
			defer sess.Close()
		}
		done <- err
	}()
	waitForQuestionPark(t, serve)

	// The live asked lands AFTER the snapshot was requested (empty answer).
	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "newer live ask projected", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "pending"
	})

	close(questionGate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("recovery: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery never finished after release")
	}

	deadline := time.After(700 * time.Millisecond)
	for {
		parts := userInputPartViews(t, h)
		if len(parts) != 1 || parts[0].Status != "pending" {
			t.Fatalf("stale empty snapshot must not clear the newer live ask, got %+v", parts)
		}
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestAudit011_LateAskedAfterResolvedDoesNotRearm: a duplicate/late asked
// frame for an already-terminal interaction must not re-arm pending.
func TestAudit011_LateAskedAfterResolvedDoesNotRearm(t *testing.T) {
	h, serve := newAuditHarness(t)

	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool { return len(p) == 1 })
	serve.push(map[string]any{"type": "question.replied", "properties": map[string]any{
		"sessionID": "ses_ocw1", "requestID": "que_a7", "answers": [][]string{{"red"}}}})
	waitForUserInput(t, h, "answered", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "answered"
	})

	// The same asked frame arrives again (server replay / duplicate).
	pushA7Asked(serve)

	deadline := time.After(700 * time.Millisecond)
	for {
		parts := userInputPartViews(t, h)
		if len(parts) != 1 || parts[0].Status != "answered" {
			t.Fatalf("late asked must not re-arm a terminal interaction, got %+v", parts)
		}
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// resolveResultCaptureConn captures SendResult for the resolve_user_input
// owning tests (publisherCaptureConn drops results).
type resolveResultCaptureConn struct {
	*publisherCaptureConn
	mu      sync.Mutex
	results []capturedResult
}

type capturedResult struct {
	RequestID string
	Result    interface{}
	Err       *WireError
}

func newResolveResultConn() *resolveResultCaptureConn {
	return &resolveResultCaptureConn{publisherCaptureConn: newPublisherCaptureConn(nil)}
}

func (c *resolveResultCaptureConn) SendResult(requestID string, result interface{}, err *WireError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, capturedResult{RequestID: requestID, Result: result, Err: err})
}

func (c *resolveResultCaptureConn) captured() []capturedResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedResult, len(c.results))
	copy(out, c.results)
	return out
}

// TestAudit011_ResolveUserInputFullChainAnswer drives the REAL production
// chain: resolve_user_input handler → concrete opencode-web responder →
// official POST /question/{id}/reply (fixture answers true and broadcasts) →
// SSE → adapter → EventPublisher/Kernel → handler returns authoritative
// headRev/currentStatus.
func TestAudit011_ResolveUserInputFullChainAnswer(t *testing.T) {
	h, serve, agent := newAuditHarnessFull(t)
	serve.mu.Lock()
	serve.replyBroadcasts = true
	serve.mu.Unlock()

	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool { return len(p) == 1 })

	conn := newResolveResultConn()
	params, _ := json.Marshal(map[string]any{
		"sessionId":      "ses_ocw1",
		"interactionId":  "que_a7",
		"clientActionId": "11111111-2222-4333-8444-555555555555",
		"action":         "answer",
		"answers": []map[string]any{{
			"questionId": "que_a7/q0",
			"values":     []map[string]any{{"kind": "option", "optionId": "que_a7/q0/o0"}},
		}},
	})
	msg := WireMessage{Type: "request", RequestID: "req_resolve_1", BackendID: "opencode-web", Method: "resolve_user_input", Params: params}
	h.handleResolveUserInput(conn, msg, agent)

	results := conn.captured()
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("resolve result = %+v", results)
	}
	res, _ := results[0].Result.(map[string]any)
	if fmt.Sprint(res["outcome"]) != "accepted" || fmt.Sprint(res["currentStatus"]) != "answered" {
		t.Fatalf("authoritative resolution = %+v", res)
	}
	if headRevValue(t, res) <= 0 {
		t.Fatalf("headRev must be authoritative (>0), got %+v", res)
	}
	parts := waitForUserInput(t, h, "part answered", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "answered"
	})
	if parts[0].TurnID != "msg_u1" || parts[0].InteractionID != "que_a7" {
		t.Fatalf("identity drift: %+v", parts[0])
	}
	posts := serve.recordedQuestionPOSTs()
	if len(posts) != 1 || posts[0].Path != "/question/que_a7/reply" {
		t.Fatalf("official POST = %+v", posts)
	}
	if posts[0].Body != `{"answers":[["red"]]}` {
		t.Fatalf("official reply body = %s", posts[0].Body)
	}
}

// TestAudit011_ResolveUserInputFullChainReject is the reject twin.
func TestAudit011_ResolveUserInputFullChainReject(t *testing.T) {
	h, serve, agent := newAuditHarnessFull(t)
	serve.mu.Lock()
	serve.rejectBroadcasts = true
	serve.mu.Unlock()

	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool { return len(p) == 1 })

	conn := newResolveResultConn()
	params, _ := json.Marshal(map[string]any{
		"sessionId":      "ses_ocw1",
		"interactionId":  "que_a7",
		"clientActionId": "11111111-2222-4333-8444-555555555555",
		"action":         "reject",
	})
	msg := WireMessage{Type: "request", RequestID: "req_resolve_2", BackendID: "opencode-web", Method: "resolve_user_input", Params: params}
	h.handleResolveUserInput(conn, msg, agent)

	results := conn.captured()
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("resolve result = %+v", results)
	}
	res, _ := results[0].Result.(map[string]any)
	if fmt.Sprint(res["outcome"]) != "accepted" || fmt.Sprint(res["currentStatus"]) != "rejected" {
		t.Fatalf("authoritative rejection = %+v", res)
	}
	if headRevValue(t, res) <= 0 {
		t.Fatalf("headRev must be authoritative (>0), got %+v", res)
	}
	waitForUserInput(t, h, "part rejected", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "rejected"
	})
	posts := serve.recordedQuestionPOSTs()
	if len(posts) != 1 || posts[0].Path != "/question/que_a7/reject" || posts[0].Body != `{}` {
		t.Fatalf("official reject POST = %+v", posts)
	}
}

// TestAudit011_ReopenAfterResolveKeepsInPlace: with the terminal already
// projected, a route re-establishment whose GET /question is empty and whose
// history carries the terminal tool must NOT re-project anything — the
// resolved part stays exactly where it is.
func TestAudit011_ReopenAfterResolveKeepsInPlace(t *testing.T) {
	h, serve, agent := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
		s.setHistory("ses_ocw1", a7AnsweredHistoryForOCW1)
	})

	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool { return len(p) == 1 })
	serve.push(map[string]any{"type": "question.rejected", "properties": map[string]any{
		"sessionID": "ses_ocw1", "requestID": "que_a7"}})
	waitForUserInput(t, h, "rejected", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "rejected"
	})

	// Route re-establishment with empty pending set + terminal history.
	sess2, err := ocwebStartSession(t, agent, "ses_ocw1")
	if err != nil {
		t.Fatalf("second StartSession: %v", err)
	}
	defer sess2.Close()

	deadline := time.After(700 * time.Millisecond)
	for {
		parts := userInputPartViews(t, h)
		if len(parts) != 1 || parts[0].Status != "rejected" || parts[0].InteractionID != "que_a7" || parts[0].TurnID != "msg_u1" {
			t.Fatalf("reopen must keep the resolved part in place, got %+v", parts)
		}
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// headRevValue extracts the revision the handler returned (int or float64
// depending on the transport shaping).
func headRevValue(t *testing.T, res map[string]any) int64 {
	t.Helper()
	switch v := res["headRev"].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

// waitForQuestionPark blocks until the gated GET /question actually reached
// the serve (barrier armed). The history park variant additionally waits for
// the pending fetch to have completed, since the history request follows it.
func waitForHistoryPark(t *testing.T, serve *ssePushServe) {
	t.Helper()
	waitForQuestionPark(t, serve)
	time.Sleep(100 * time.Millisecond)
}

func waitForQuestionPark(t *testing.T, serve *ssePushServe) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		serve.mu.Lock()
		fetches := serve.questionFetches
		serve.mu.Unlock()
		if fetches > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("recovery never issued its GET /question (barrier not armed)")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ocwebStartSession opens a real second session against the harness serve —
// the route re-establishment path whose recovery runs synchronously inside.
func ocwebStartSession(t *testing.T, agent core.Agent, sessionID string) (interface {
	Close() error
}, error) {
	t.Helper()
	sess, err := agent.StartSession(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	return sess, nil
}
