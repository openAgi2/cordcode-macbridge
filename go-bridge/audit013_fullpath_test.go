package gobridge

import (
	"testing"
)

// audit013_fullpath_test.go owns the directive-013 lookup terminal gate: the
// interaction is terminal and the in-memory mapping is empty, but the CURRENT
// GET /question still returns the same stale row (server lag). A resolve
// attempt through the REAL resolve_user_input handler + concrete opencode-web
// responder must be authoritatively refused with ZERO official POSTs. The
// endpoint is NEVER emptied during the test and the POST fixture answers a
// plain 200 true (no 404/409 masking of the adapter bypass).

// TestAudit013_StaleCurrentGetRowSecondResolveZeroPOST: answered+answer and
// rejected+reject both covered.
func TestAudit013_StaleCurrentGetRowSecondResolveZeroPOST(t *testing.T) {
	for _, tc := range []struct {
		name       string
		terminal   string
		resolveAct string
		withAns    bool
	}{
		{"answered terminal, answer refused while GET still lists the row", "question.replied", "answer", true},
		{"rejected terminal, reject refused while GET still lists the row", "question.rejected", "reject", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The serve's pending list keeps returning the stale row for the
			// whole test — the exact boundary audit-012 convicted.
			h, serve, agent := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
				s.setPendingQuestions(a7PendingRowForOCW1)
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

			results := resolveViaHandlerA12(t, h, agent, tc.resolveAct, tc.withAns)
			if len(results) != 1 || results[0].Err == nil {
				t.Fatalf("resolve must be authoritatively refused even while GET still lists the stale row, got %+v", results)
			}
			if results[0].Err.Code != "interaction_not_found" && results[0].Err.Code != "already_resolved" {
				t.Fatalf("refusal code = %+v, want interaction_not_found/already_resolved", results[0].Err)
			}
			if posts := serve.recordedQuestionPOSTs(); len(posts) != 0 {
				t.Fatalf("stale current GET row must yield ZERO official POSTs, got %+v", posts)
			}
			parts := userInputPartViews(t, h)
			if len(parts) != 1 || parts[0].Status == "pending" || parts[0].InteractionID != "que_a7" || parts[0].TurnID != "msg_u1" {
				t.Fatalf("projection must keep the same single terminal part, got %+v", parts)
			}
		})
	}
}
