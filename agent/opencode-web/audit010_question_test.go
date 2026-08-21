package opencodeweb

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// audit010_question_test.go owns the directive-010 question source-correlation
// and cold-recovery proofs: every asked frame (live or recovered from
// GET /question) must map tool.messageID/callID to a subscriber-observed or
// history-proven assistant message of the SAME session, whose parentID owning
// turn matches the armed turn. Anything else fails closed with zero projection.

func askedFrame(id, sessionID, messageID, callID string) string {
	return `{"payload":{"type":"question.asked","properties":{"id":"` + id + `","sessionID":"` + sessionID +
		`","questions":[{"question":"q","options":[{"label":"a"},{"label":"b"}]}],` +
		`"tool":{"messageID":"` + messageID + `","callID":"` + callID + `"}}}}`
}

// countUserInputRequested counts EventUserInputRequested emissions.
func countUserInputRequested(events []core.Event) int {
	n := 0
	for _, ev := range events {
		if ev.Type == core.EventUserInputRequested {
			n++
		}
	}
	return n
}

// pendingRegistrySize snapshots the live pending-question registry.
func pendingRegistrySize(a *Agent) int {
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	return len(a.pendingQuestions)
}

// TestAudit010_QuestionAskedSourceCorrelationDestructive is the audit-009
// conviction matrix: missing callID, unknown messageID, other-session
// messageID, stale previous-turn messageID all fail closed (zero emissions,
// zero registry entries); the correct same-turn messageID is the ONLY
// projecting case, attributed to the parentID turn.
func TestAudit010_QuestionAskedSourceCorrelationDestructive(t *testing.T) {
	t.Run("missing callID", func(t *testing.T) {
		agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
		sub := newDrivenSubscriber(t, agent)
		armTurn(sub, "ses_q", "msg_u0")
		armAssistant(sub, "ses_q", "msg_1", "msg_u0")
		sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", ""))
		if events := drain(sub); countUserInputRequested(events) != 0 {
			t.Fatalf("missing callID must fail closed, got %+v", events)
		}
		if pendingRegistrySize(agent) != 0 {
			t.Fatal("missing callID must not enter the pending registry")
		}
	})

	t.Run("unknown messageID", func(t *testing.T) {
		agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
		sub := newDrivenSubscriber(t, agent)
		armTurn(sub, "ses_q", "msg_u0")
		sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_NEVER_OBSERVED", "call_1"))
		if events := drain(sub); countUserInputRequested(events) != 0 {
			t.Fatalf("unknown assistant message must fail closed, got %+v", events)
		}
		if pendingRegistrySize(agent) != 0 {
			t.Fatal("unknown messageID must not enter the pending registry")
		}
	})

	t.Run("other-session messageID", func(t *testing.T) {
		agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
		sub := newDrivenSubscriber(t, agent)
		armTurn(sub, "ses_q", "msg_u0")
		// The assistant message exists — but belongs to ANOTHER session.
		armTurn(sub, "ses_OTHER", "msg_uX")
		armAssistant(sub, "ses_OTHER", "msg_1", "msg_uX")
		sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", "call_1"))
		if events := drain(sub); countUserInputRequested(events) != 0 {
			t.Fatalf("other-session messageID must fail closed, got %+v", events)
		}
		if pendingRegistrySize(agent) != 0 {
			t.Fatal("other-session messageID must not enter the pending registry")
		}
	})

	t.Run("stale previous-turn messageID", func(t *testing.T) {
		agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
		sub := newDrivenSubscriber(t, agent)
		// Previous turn's assistant message (parentID = previous user msg).
		armTurn(sub, "ses_q", "msg_uOLD")
		armAssistant(sub, "ses_q", "msg_1", "msg_uOLD")
		// The session moved on to a new armed turn before the asked frame.
		armTurn(sub, "ses_q", "msg_uNEW")
		sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", "call_1"))
		if events := drain(sub); countUserInputRequested(events) != 0 {
			t.Fatalf("stale previous-turn messageID must fail closed, got %+v", events)
		}
		if pendingRegistrySize(agent) != 0 {
			t.Fatal("stale messageID must not enter the pending registry")
		}
	})

	t.Run("assistant fact without parentID", func(t *testing.T) {
		agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
		sub := newDrivenSubscriber(t, agent)
		armTurn(sub, "ses_q", "msg_u0")
		sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","role":"assistant"},"sessionID":"ses_q"}}}`)
		sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", "call_1"))
		if events := drain(sub); countUserInputRequested(events) != 0 {
			t.Fatalf("parentless assistant fact must fail closed, got %+v", events)
		}
	})

	t.Run("correct same-turn messageID projects to the parentID turn", func(t *testing.T) {
		agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
		sub := newDrivenSubscriber(t, agent)
		armTurn(sub, "ses_q", "msg_u0")
		armAssistant(sub, "ses_q", "msg_1", "msg_u0")
		sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", "call_1"))
		events := drain(sub)
		if countUserInputRequested(events) != 1 {
			t.Fatalf("the proven asked frame must project exactly once, got %+v", events)
		}
		ev := events[0]
		if ev.TurnID != "msg_u0" || ev.ItemID != "call_1" {
			t.Fatalf("attribution must be parentID turn + tool.callID, got turn=%q item=%q", ev.TurnID, ev.ItemID)
		}
		if pendingRegistrySize(agent) != 1 {
			t.Fatal("the proven ask must arm the pending registry")
		}
	})
}

// a7HistoryFixture is the A7 reload `messages` shape: user msg_u0 → assistant
// msg_1 (parentID msg_u0). The history transaction is the authoritative
// cold-recovery truth.
const a7HistoryFixture = `[
	{"info":{"id":"msg_u0","sessionID":"ses_q","role":"user","time":{"created":1}},"parts":[{"type":"text","text":"ask"}]},
	{"info":{"id":"msg_1","sessionID":"ses_q","role":"assistant","parentID":"msg_u0","time":{"created":2}},"parts":[]}
]`

// TestAudit010_ColdRecoveryFromGETQuestion proves the directive-010 reload
// path: a FRESH process (no live facts) StartSession-resumes a session whose
// question was asked while it was away; GET /question + the authoritative
// history transaction re-present the pending interaction through the session
// route with the source-proven turn.
func TestAudit010_ColdRecoveryFromGETQuestion(t *testing.T) {
	agent, serve := questionAgent(t, map[string]string{
		"/question":               `[{"id":"que_1","sessionID":"ses_q","questions":[{"question":"q","options":[{"label":"a"}]}],"tool":{"messageID":"msg_1","callID":"call_1"}}]`,
		"/session/ses_q/message":  a7HistoryFixture,
		"/session/ses_q":          `{"id":"ses_q","directory":"/tmp","model":{"id":"m","providerID":"p"}}`,
	})
	sess, err := agent.StartSession(context.Background(), "ses_q")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()

	asked := waitForUserInputRequested(t, sess)
	if asked.UserInput == nil || asked.UserInput.InteractionID != "que_1" {
		t.Fatalf("recovered interaction = %+v", asked.UserInput)
	}
	if asked.TurnID != "msg_u0" || asked.ItemID != "call_1" {
		t.Fatalf("recovery must carry the history-proven turn + callID, got turn=%q item=%q", asked.TurnID, asked.ItemID)
	}
	if gets := serve.requestsFor("/question"); len(gets) != 1 {
		t.Fatalf("recovery must GET /question exactly once per route establishment, got %d", len(gets))
	}
	if pendingRegistrySize(agent) != 1 {
		t.Fatal("recovery must arm the pending registry")
	}
}

// waitForUserInputRequested drains the session route until one requested
// event lands (recovery is synchronous, but the route is buffered).
func waitForUserInputRequested(t *testing.T, sess core.AgentSession) core.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-sess.Events():
			if ev.Type == core.EventUserInputRequested {
				return ev
			}
		case <-deadline:
			t.Fatal("no user_input_requested reached the session route")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestAudit010_ColdRecoveryDestructive: rows that cannot be source-proven
// from the history transaction project NOTHING (no phantom turn, no registry).
func TestAudit010_ColdRecoveryDestructive(t *testing.T) {
	pendingRow := func(messageID string) string {
		return `[{"id":"que_1","sessionID":"ses_q","questions":[{"question":"q","options":[{"label":"a"}]}],"tool":{"messageID":"` + messageID + `","callID":"call_1"}}]`
	}
	cases := []struct {
		name    string
		pending string
		history string
	}{
		{"assistant message absent from history", pendingRow("msg_MISSING"), a7HistoryFixture},
		{"assistant without parentID", pendingRow("msg_1"), `[
			{"info":{"id":"msg_u0","sessionID":"ses_q","role":"user"},"parts":[]},
			{"info":{"id":"msg_1","sessionID":"ses_q","role":"assistant"},"parts":[]}]`},
		{"messageID belongs to the user message", pendingRow("msg_u0"), a7HistoryFixture},
		{"parent is not a user message", pendingRow("msg_1"), `[
			{"info":{"id":"msg_u0","sessionID":"ses_q","role":"system"},"parts":[]},
			{"info":{"id":"msg_1","sessionID":"ses_q","role":"assistant","parentID":"msg_u0"},"parts":[]}]`},
		{"row session mismatch inside history", pendingRow("msg_1"), `[
			{"info":{"id":"msg_u0","sessionID":"ses_q","role":"user"},"parts":[]},
			{"info":{"id":"msg_1","sessionID":"ses_OTHER","role":"assistant","parentID":"msg_u0"},"parts":[]}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent, _ := questionAgent(t, map[string]string{
				"/question":              tc.pending,
				"/session/ses_q/message": tc.history,
			})
			sess, err := agent.StartSession(context.Background(), "ses_q")
			if err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			defer sess.Close()
			// Recovery is synchronous inside StartSession: by return time the
			// route carries everything it ever will.
			select {
			case ev := <-sess.Events():
				t.Fatalf("unproven recovery must project nothing, got %+v", ev)
			default:
			}
			if pendingRegistrySize(agent) != 0 {
				t.Fatal("unproven recovery must not arm the pending registry")
			}
		})
	}
}

// TestAudit010_RecoveryFiltersOtherSessions: GET /question is serve-wide;
// only the target session's rows are processed.
func TestAudit010_RecoveryFiltersOtherSessions(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{
		"/question": `[{"id":"que_other","sessionID":"ses_OTHER","questions":[{"question":"q","options":[{"label":"a"}]}],"tool":{"messageID":"msg_1","callID":"call_1"}}]`,
	})
	sess, err := agent.StartSession(context.Background(), "ses_q")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	select {
	case ev := <-sess.Events():
		t.Fatalf("another session's pending row must not enter this route, got %+v", ev)
	default:
	}
	if pendingRegistrySize(agent) != 0 {
		t.Fatal("other-session rows must not arm the registry")
	}
}

// TestAudit010_GetLiveRaceConvergesToOneProjection: the recovered projection
// (GET /question during StartSession) and a later live asked frame for the
// SAME interaction converge to ONE emission through the route — the claim
// primitive prevents the double ingest.
func TestAudit010_GetLiveRaceConvergesToOneProjection(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{
		"/question":              `[{"id":"que_R","sessionID":"ses_q","questions":[{"question":"q","options":[{"label":"a"}]}],"tool":{"messageID":"msg_1","callID":"call_1"}}]`,
		"/session/ses_q/message": a7HistoryFixture,
	})
	sess, err := agent.StartSession(context.Background(), "ses_q")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	first := waitForUserInputRequested(t, sess)

	// The live frame for the SAME interaction arrives after recovery — armed
	// with facts so the live correlation itself would also pass.
	sub := agent.globalSub
	armTurn(sub, "ses_q", "msg_u0")
	armAssistant(sub, "ses_q", "msg_1", "msg_u0")
	sub.handleRawEvent(askedFrame("que_R", "ses_q", "msg_1", "call_1"))

	total := 1
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case ev := <-sess.Events():
			if ev.Type == core.EventUserInputRequested {
				total++
			}
		case <-deadline:
			if total != 1 {
				t.Fatalf("GET+live must converge to ONE projection, got %d", total)
			}
			if first.TurnID != "msg_u0" {
				t.Fatalf("converged projection must keep the proven turn, got %q", first.TurnID)
			}
			return
		}
	}
}

// TestAudit010_QuestionResolvedCarriesFactTurn: the resolved terminal rides
// the same assistant parentID fact — no activeTurn fallback.
func TestAudit010_QuestionResolvedCarriesFactTurn(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
	sub := newDrivenSubscriber(t, agent)
	armTurn(sub, "ses_q", "msg_u0")
	armAssistant(sub, "ses_q", "msg_1", "msg_u0")
	sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", "call_1"))
	drain(sub)

	sub.handleRawEvent(`{"payload":{"type":"question.replied","properties":{"sessionID":"ses_q","requestID":"que_1","answers":[["a"]]}}}`)
	events := drain(sub)
	found := false
	for _, ev := range events {
		if ev.Type != core.EventUserInputResolved {
			continue
		}
		found = true
		if ev.TurnID != "msg_u0" {
			t.Fatalf("resolved must carry the fact turn msg_u0, got %q", ev.TurnID)
		}
		if ev.ItemID != "call_1" {
			t.Fatalf("resolved must carry the tool callID, got %q", ev.ItemID)
		}
		if ev.UserInput.ResolutionSource != "other_client" {
			t.Fatalf("server-broadcast resolution is other_client, got %+v", ev.UserInput)
		}
	}
	if !found {
		t.Fatalf("replied must emit user_input_resolved, got %+v", events)
	}
}

// TestAudit010_RecoveryFailsClosedOnServeErrors: an unreachable /question or
// a malformed list is an honest no-recovery — never a fabricated projection.
func TestAudit010_RecoveryFailsClosedOnServeErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"question route missing (404)", ""},
		{"malformed question list", `{"data":[{}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := map[string]string{}
			if tc.body != "" {
				responses["/question"] = tc.body
			}
			agent, _ := questionAgent(t, responses)
			sess, err := agent.StartSession(context.Background(), "ses_q")
			if err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			defer sess.Close()
			select {
			case ev := <-sess.Events():
				t.Fatalf("failed recovery must project nothing, got %+v", ev)
			default:
			}
		})
	}
}

// ── directive-011 terminal reconciliation (agent level) ─────────────────────

// runRecovery drives one recovery cycle against the given serve responses
// with a live-fact-free subscriber (process-restart semantics).
func runRecovery(t *testing.T, responses map[string]string, sub *sseSubscriber, agent *Agent) {
	t.Helper()
	c, err := agent.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	agent.recoverPendingQuestions(context.Background(), c, sub, "ses_q", "")
}

// TestAudit011_ReconcileRejectEvidence: a locally-pending interaction absent
// from GET /question settles rejected when the history carries the official
// dismissed-error shape.
func TestAudit011_ReconcileRejectEvidence(t *testing.T) {
	const rejectedHistory = `[{"info":{"id":"msg_u0","sessionID":"ses_q","role":"user"},"parts":[]},{"info":{"id":"msg_1","sessionID":"ses_q","role":"assistant","parentID":"msg_u0"},"parts":[{"type":"tool","callID":"call_1","messageID":"msg_1","tool":"question","state":{"status":"error","input":{"questions":[]},"error":"The user dismissed this question"}}]}]`
	agent, _ := questionAgent(t, map[string]string{
		"/question":              `[]`,
		"/session/ses_q/message": rejectedHistory,
	})
	sub := newDrivenSubscriber(t, agent)
	armTurn(sub, "ses_q", "msg_u0")
	armAssistant(sub, "ses_q", "msg_1", "msg_u0")
	sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", "call_1"))
	drain(sub)
	runRecovery(t, nil, sub, agent)

	events := drain(sub)
	if len(events) != 1 || events[0].Type != core.EventUserInputResolved {
		t.Fatalf("reconciliation must emit exactly one resolved, got %+v", events)
	}
	if events[0].UserInput.Status != core.UserInputStatusRejected || events[0].TurnID != "msg_u0" || events[0].ItemID != "call_1" {
		t.Fatalf("reconciled terminal = %+v", events[0])
	}
}

// TestAudit011_ReconcileFailClosedOnUnknownTerminal: absence from GET plus a
// history tool that is NOT an evidence-proven terminal (completed without
// captured answers) decides nothing — no emission, pending survives.
func TestAudit011_ReconcileFailClosedOnUnknownTerminal(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{
		"/question": `[]`,
		"/session/ses_q/message": `[{"info":{"id":"msg_u0","sessionID":"ses_q","role":"user"},"parts":[]},{"info":{"id":"msg_1","sessionID":"ses_q","role":"assistant","parentID":"msg_u0"},"parts":[{"type":"tool","callID":"call_1","messageID":"msg_1","tool":"question","state":{"status":"completed","input":{"questions":[]}}}]}`,
	})
	sub := newDrivenSubscriber(t, agent)
	armTurn(sub, "ses_q", "msg_u0")
	armAssistant(sub, "ses_q", "msg_1", "msg_u0")
	sub.handleRawEvent(askedFrame("que_1", "ses_q", "msg_1", "call_1"))
	drain(sub)

	runRecovery(t, map[string]string{}, sub, agent)
	if events := drain(sub); len(events) != 0 {
		t.Fatalf("unknown terminal shape must fail closed (no emission), got %+v", events)
	}
	agent.questionMu.Lock()
	lc := agent.questions[questionLifecycleKey("ses_q", "que_1")]
	agent.questionMu.Unlock()
	if lc == nil || lc.status != core.UserInputStatusPending {
		t.Fatalf("lifecycle must stay pending after a fail-closed reconciliation, got %+v", lc)
	}
}
