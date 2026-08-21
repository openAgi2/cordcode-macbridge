package gobridge

import (
	"testing"
	"time"
)

// audit010_fullpath_test.go owns the directive-010 question full-path proofs:
// live asked → pending part; reply/reject → in-place resolution; unproven
// identity → zero projection; cold reload and stream-gap reconnect recover
// the pending interaction through the ONE Kernel route; GET+live converge to
// a single part. All assertions are at PROJECTION level, through the real
// adapter + Handlers relay + deltaBatcher + EventPublisher + Kernel stack.

// userInputPartView is the projection-level view of one user_input part.
type userInputPartView struct {
	TurnID        string
	InteractionID string
	Status        string
	Source        string
}

func userInputPartViews(t *testing.T, h *Handlers) []userInputPartView {
	t.Helper()
	proj, ok := h.projectionKernel.reducer.Snapshot("opencode-web", "ses_ocw1")
	if !ok {
		return nil
	}
	var out []userInputPartView
	for _, turn := range proj.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type == "user_input" {
				out = append(out, userInputPartView{
					TurnID:        turn.TurnID,
					InteractionID: part.UserInputInteractionID,
					Status:        part.UserInputStatus,
					Source:        part.UserInputResolutionSource,
				})
			}
		}
	}
	return out
}

// waitForUserInput polls the projection until the predicate holds.
func waitForUserInput(t *testing.T, h *Handlers, what string, pred func([]userInputPartView) bool) []userInputPartView {
	t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		parts := userInputPartViews(t, h)
		if pred(parts) {
			return parts
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s; parts = %+v", what, parts)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// a7HistoryForOCW1 is the A7-shaped authoritative history: user msg_u1 →
// assistant msg_a7_tool (parentID msg_u1) — the tool message of the pending
// question below.
const a7HistoryForOCW1 = `[
	{"info":{"id":"msg_u1","sessionID":"ses_ocw1","role":"user","time":{"created":1}},"parts":[{"type":"text","text":"ask"}]},
	{"info":{"id":"msg_a7_tool","sessionID":"ses_ocw1","role":"assistant","parentID":"msg_u1","time":{"created":2}},"parts":[]}
]`

const a7PendingRowForOCW1 = `[{"id":"que_a7","sessionID":"ses_ocw1","questions":[{"question":"Which fixture color?","header":"Color","options":[{"label":"red","description":"Stop"},{"label":"green","description":"Go"}],"multiple":false}],"tool":{"messageID":"msg_a7_tool","callID":"call_a7"}}]`

// armA7QuestionFacts pushes the live frames that make the asked frame
// source-proven: the user echo (arming turn msg_u1) and the assistant
// message.updated carrying parentID (A7 frames 7→14→77 ordering).
func armA7QuestionFacts(serve *ssePushServe) {
	serve.push(map[string]any{"type": "message.updated", "properties": map[string]any{
		"info": map[string]any{"id": "msg_u1", "role": "user"}, "sessionID": "ses_ocw1"}})
	serve.push(map[string]any{"type": "message.updated", "properties": map[string]any{
		"info": map[string]any{"id": "msg_a7_tool", "role": "assistant", "parentID": "msg_u1"},
		"sessionID": "ses_ocw1"}})
}

func pushA7Asked(serve *ssePushServe) {
	serve.push(map[string]any{"type": "question.asked", "properties": map[string]any{
		"id": "que_a7", "sessionID": "ses_ocw1",
		"questions": []any{map[string]any{
			"question": "Which fixture color?", "header": "Color",
			"options": []any{
				map[string]any{"label": "red", "description": "Stop"},
				map[string]any{"label": "green", "description": "Go"},
			},
			"multiple": false,
		}},
		"tool": map[string]any{"messageID": "msg_a7_tool", "callID": "call_a7"}}})
}

// TestAudit010_QuestionUnprovenIdentityZeroProjection: the asked frame whose
// tool.messageID was never observed as an assistant message of this session
// projects NOTHING — the audit-009 lenient attribution (activeTurn + non-empty
// messageID) is gone.
func TestAudit010_QuestionUnprovenIdentityZeroProjection(t *testing.T) {
	h, serve := newAuditHarness(t)

	armA7QuestionFacts(serve)
	// Same frame, but the tool message was never observed as an assistant
	// message of this session (identity swap) — correlation must fail closed.
	serve.push(map[string]any{"type": "question.asked", "properties": map[string]any{
		"id": "que_ghost", "sessionID": "ses_ocw1",
		"questions": []any{map[string]any{
			"question": "q", "options": []any{map[string]any{"label": "a"}},
		}},
		"tool": map[string]any{"messageID": "msg_NEVER_OBSERVED", "callID": "call_x"}}})

	deadline := time.After(700 * time.Millisecond)
	for {
		select {
		case <-deadline:
			goto settled
		case <-time.After(50 * time.Millisecond):
		}
	}
settled:
	if parts := userInputPartViews(t, h); len(parts) != 0 {
		t.Fatalf("unproven identity must project ZERO parts, got %+v", parts)
	}
}

// TestAudit010_QuestionReplyResolvesInPlace: the server's question.replied
// (external client answered) settles the ONE pending part in place — count
// stays 1, status answered, source other_client.
func TestAudit010_QuestionReplyResolvesInPlace(t *testing.T) {
	h, serve := newAuditHarness(t)

	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	parts := waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool { return len(p) == 1 })
	if parts[0].Status != "pending" || parts[0].TurnID != "msg_u1" || parts[0].InteractionID != "que_a7" {
		t.Fatalf("pending part = %+v", parts[0])
	}

	serve.push(map[string]any{"type": "question.replied", "properties": map[string]any{
		"sessionID": "ses_ocw1", "requestID": "que_a7", "answers": [][]string{{"red"}}}})
	parts = waitForUserInput(t, h, "in-place resolution", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "answered"
	})
	if parts[0].Source != "other_client" {
		t.Fatalf("server-broadcast reply resolves as other_client, got %+v", parts[0])
	}
	if parts[0].TurnID != "msg_u1" {
		t.Fatalf("resolution must stay on the proven turn, got %+v", parts[0])
	}
}

// TestAudit010_QuestionRejectResolvesInPlace: question.rejected settles the
// same single part as rejected.
func TestAudit010_QuestionRejectResolvesInPlace(t *testing.T) {
	h, serve := newAuditHarness(t)

	armA7QuestionFacts(serve)
	pushA7Asked(serve)
	waitForUserInput(t, h, "pending part", func(p []userInputPartView) bool { return len(p) == 1 })

	serve.push(map[string]any{"type": "question.rejected", "properties": map[string]any{
		"sessionID": "ses_ocw1", "requestID": "que_a7"}})
	parts := waitForUserInput(t, h, "rejected in place", func(p []userInputPartView) bool {
		return len(p) == 1 && p[0].Status == "rejected"
	})
	if parts[0].Source != "other_client" {
		t.Fatalf("server-broadcast reject resolves as other_client, got %+v", parts[0])
	}
}

// TestAudit010_QuestionColdReloadRecoversPending: a FRESH adapter + bridge
// (process restarted) opens the session while a question is still pending on
// the serve: StartSession's GET /question + authoritative history transaction
// re-present it through the ONE Kernel route as exactly one pending part on
// the history-proven turn. No live asked frame exists.
func TestAudit010_QuestionColdReloadRecoversPending(t *testing.T) {
	h, _ := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
		s.setPendingQuestions(a7PendingRowForOCW1)
		s.setHistory("ses_ocw1", a7HistoryForOCW1)
	})

	parts := waitForUserInput(t, h, "cold-recovered pending part", func(p []userInputPartView) bool {
		return len(p) == 1
	})
	if parts[0].Status != "pending" || parts[0].InteractionID != "que_a7" {
		t.Fatalf("recovered part = %+v", parts[0])
	}
	if parts[0].TurnID != "msg_u1" {
		t.Fatalf("recovery must attribute the history-proven turn msg_u1, got %+v", parts[0])
	}
}

// TestAudit010_QuestionReconnectRecoversPending: the asked frame is lost
// inside a mid-flight stream gap; after the redial the pending-question
// reconciliation re-derives it (live facts absent → history transaction) and
// projects exactly one pending part.
func TestAudit010_QuestionReconnectRecoversPending(t *testing.T) {
	h, serve := newAuditHarness(t)

	// The question lands on the serve while our stream is about to break —
	// no asked frame is ever delivered live.
	serve.setPendingQuestions(a7PendingRowForOCW1)
	serve.setHistory("ses_ocw1", a7HistoryForOCW1)
	serve.drop()

	parts := waitForUserInput(t, h, "post-redial recovered part", func(p []userInputPartView) bool {
		return len(p) == 1
	})
	if parts[0].Status != "pending" || parts[0].TurnID != "msg_u1" {
		t.Fatalf("gap recovery must project one pending part on the proven turn, got %+v", parts[0])
	}
}

// TestAudit010_QuestionGetLiveRaceSinglePart: the GET /question recovery and
// a live asked frame for the SAME interaction both race the route; the
// projection must end with exactly ONE pending part (claim convergence + the
// reducer's interactionID upsert), never two.
func TestAudit010_QuestionGetLiveRaceSinglePart(t *testing.T) {
	h, serve := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
		s.setPendingQuestions(a7PendingRowForOCW1)
		s.setHistory("ses_ocw1", a7HistoryForOCW1)
	})

	// Recovery ran inside StartSession; now the live asked frame for the same
	// interaction arrives with full facts — it must NOT create a second part.
	armA7QuestionFacts(serve)
	pushA7Asked(serve)

	parts := waitForUserInput(t, h, "single converged part", func(p []userInputPartView) bool {
		return len(p) >= 1
	})
	if len(parts) != 1 {
		t.Fatalf("GET+live must converge to ONE part, got %+v", parts)
	}
	if parts[0].InteractionID != "que_a7" || parts[0].TurnID != "msg_u1" {
		t.Fatalf("converged part = %+v", parts[0])
	}
}
