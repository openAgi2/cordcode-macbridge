package gobridge

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// audit012_fullpath_test.go owns the directive-012 terminal reply-mapping
// seal: after an interaction is terminal, a late question.asked or a stale
// recovery row must NOT re-open the reply mapping — a second resolve attempt
// through the REAL resolve_user_input handler must return an authoritative
// non-submittable result with ZERO new official POSTs, and the projection
// keeps the same single terminal part.

// resolveViaHandlerA12 drives the real resolve_user_input handler through the
// concrete opencode-web responder.
func resolveViaHandlerA12(t *testing.T, h *Handlers, agent core.Agent, action string, withAnswers bool) []capturedResult {
	t.Helper()
	conn := newResolveResultConn()
	params := map[string]any{
		"sessionId":      "ses_ocw1",
		"interactionId":  "que_a7",
		"clientActionId": "11111111-2222-4333-8444-555555555555",
		"action":         action,
	}
	if withAnswers {
		params["answers"] = []map[string]any{{
			"questionId": "que_a7/q0",
			"values":     []map[string]any{{"kind": "option", "optionId": "que_a7/q0/o0"}},
		}}
	}
	raw, _ := json.Marshal(params)
	msg := WireMessage{Type: "request", RequestID: "req_a12", BackendID: "opencode-web", Method: "resolve_user_input", Params: raw}
	h.handleResolveUserInput(conn, msg, agent)
	return conn.captured()
}

// TestAudit012_TerminalThenLateAskedSecondResolveZeroPOST: the terminal
// broadcast lands, a duplicate asked frame arrives, and the follow-up resolve
// must be authoritatively refused with ZERO new official POSTs (answer and
// reject terminals both covered).
func TestAudit012_TerminalThenLateAskedSecondResolveZeroPOST(t *testing.T) {
	for _, tc := range []struct {
		name       string
		terminal   string
		resolveAct string
		withAns    bool
	}{
		{"answered terminal, second answer refused", "question.replied", "answer", true},
		{"rejected terminal, second reject refused", "question.rejected", "reject", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, serve, agent := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
				s.setHistory("ses_ocw1", a7HistoryForOCW1)
			})

			armA7QuestionFacts(serve)
			pushA7Asked(serve)
			waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool {
				return len(p) == 1 && p[0].Status == "pending"
			})
			terminalProps := map[string]any{"sessionID": "ses_ocw1", "requestID": "que_a7"}
			if tc.terminal == "question.replied" {
				terminalProps["answers"] = [][]string{{"red"}}
			}
			serve.push(map[string]any{"type": tc.terminal, "properties": terminalProps})
			waitForUserInput(t, h, "terminal part", func(p []userInputPartView) bool {
				return len(p) == 1 && p[0].Status != "pending"
			})

			// The late duplicate asked frame re-enters the pipeline; the
			// server truth (GET /question) is empty — no legitimate submit
			// path may exist anymore.
			pushA7Asked(serve)
			serve.setPendingQuestions("[]")

			// The late frame is processed asynchronously, so the refusal must
			// hold across a bounded window of repeated attempts — every one
			// refused, ZERO official POSTs, projection untouched.
			deadline := time.After(2 * time.Second)
			for {
				results := resolveViaHandlerA12(t, h, agent, tc.resolveAct, tc.withAns)
				if len(results) != 1 || results[0].Err == nil {
					t.Fatalf("second resolve must be authoritatively refused, got %+v", results)
				}
				if results[0].Err.Code != "interaction_not_found" {
					t.Fatalf("refusal code = %+v, want interaction_not_found", results[0].Err)
				}
				if posts := serve.recordedQuestionPOSTs(); len(posts) != 0 {
					t.Fatalf("terminal + late asked must yield ZERO new official POSTs, got %+v", posts)
				}
				select {
				case <-deadline:
					parts := userInputPartViews(t, h)
					if len(parts) != 1 || parts[0].Status == "pending" || parts[0].InteractionID != "que_a7" || parts[0].TurnID != "msg_u1" {
						t.Fatalf("projection must keep the same single terminal part, got %+v", parts)
					}
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		})
	}
}

// TestAudit012_BarrierStaleRecoverySecondResolveZeroPOST (barrier): the
// recovery's GET carries a STALE pending row; the live terminal lands while
// the response is parked; after release the stale row must not re-open the
// reply mapping — the follow-up resolve posts NOTHING.
func TestAudit012_BarrierStaleRecoverySecondResolveZeroPOST(t *testing.T) {
	h, serve, agent := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
		s.setPendingQuestions(a7PendingRowForOCW1)
		s.setHistory("ses_ocw1", a7HistoryForOCW1)
	})

	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "pending"
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

	// The live terminal lands while the stale snapshot is parked.
	serve.push(map[string]any{"type": "question.replied", "properties": map[string]any{
		"sessionID": "ses_ocw1", "requestID": "que_a7", "answers": [][]string{{"red"}}}})
	waitForUserInput(t, h, "terminal before release", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "answered"
	})

	// Server truth flips empty; release the stale row into the recovery.
	serve.setPendingQuestions("[]")
	close(questionGate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("recovery: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery never finished after release")
	}

	results := resolveViaHandlerA12(t, h, agent, "answer", true)
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("post-stale-recovery resolve must be authoritatively refused, got %+v", results)
	}
	if results[0].Err.Code != "interaction_not_found" {
		t.Fatalf("refusal code = %+v, want interaction_not_found", results[0].Err)
	}
	if posts := serve.recordedQuestionPOSTs(); len(posts) != 0 {
		t.Fatalf("stale recovery row must yield ZERO official POSTs, got %+v", posts)
	}
	deadline := time.After(700 * time.Millisecond)
	for {
		parts := userInputPartViews(t, h)
		if len(parts) != 1 || parts[0].Status != "answered" {
			t.Fatalf("projection must keep the terminal part, got %+v", parts)
		}
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}
