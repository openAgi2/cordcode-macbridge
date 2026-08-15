package dsh

// Phase-2 tests: notification session scope & lineage (design §3.8, §16 gate
// 4) and sequence integrity (§3.10.1, §16 gate 3). All wire fixtures — no
// real key, no process.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── §3.10.1 seq integrity (§16 gate 3) ─────────────────────────────────────

func TestSeqFirstFrameMustBeZero(t *testing.T) {
	c := newCodec(testNonce)
	// First frame seq 0: accepted.
	evs, err := c.apply(envelope(0, "turn/start", `{"turn":1}`))
	if err != nil || len(evs) != 1 {
		t.Fatalf("seq 0 first frame must be accepted: evs=%v err=%v", evs, err)
	}
	// Fresh codec, first frame seq 3: missing prefix → fail visibly.
	c2 := newCodec(testNonce)
	err = mustViolation(t, c2, envelope(3, "turn/start", `{"turn":1}`))
	if !contains(err.Error(), "seq 0") {
		t.Fatalf("violation should mention the seq-0 rule: %v", err)
	}
}

func TestSeqExactReplayIdempotentSkip(t *testing.T) {
	c := newCodec(testNonce)
	frame := envelope(0, "turn/start", `{"turn":1}`)
	mustEvents(t, c, frame)

	// Exact replay of the previous envelope (same seq, same canonical
	// content): idempotent skip — no event, no re-advance.
	evs := mustEvents(t, c, frame)
	if len(evs) != 0 {
		t.Fatalf("exact replay must emit nothing, got %+v", evs)
	}

	// The next in-order frame still lands on expectedSeq=1.
	mustEvents(t, c, envelope(1, "step/start", `{"turn":1,"step":1}`))

	// Replay with different wire key ORDER but identical canonical content
	// is still an exact replay (canonical comparison is key-order-blind).
	c2 := newCodec(testNonce)
	mustEvents(t, c2, envelope(0, "turn/start", `{"turn":1,"meta":"a"}`))
	evs = mustEvents(t, c2, &dshEvent{
		Type: "turn/start",
		Seq:  0,
		Time: 1786634455860,
		Data: json.RawMessage(`{"meta":"a","turn":1}`),
	})
	if len(evs) != 0 {
		t.Fatalf("reordered exact replay must emit nothing, got %+v", evs)
	}
}

func TestSeqConflictingDuplicateFails(t *testing.T) {
	c := newCodec(testNonce)
	mustEvents(t, c, envelope(0, "turn/start", `{"turn":1}`))
	// Same seq as the last accepted frame, different payload → conflicting
	// duplicate, protocol corruption.
	err := mustViolation(t, c, envelope(0, "turn/start", `{"turn":2}`))
	if !contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("violation should name conflicting duplicate: %v", err)
	}
}

func TestSeqGapFails(t *testing.T) {
	c := newCodec(testNonce)
	mustEvents(t, c, envelope(0, "turn/start", `{"turn":1}`))
	err := mustViolation(t, c, envelope(3, "step/start", `{"turn":1,"step":1}`))
	if !contains(err.Error(), "gap") {
		t.Fatalf("violation should name gap: %v", err)
	}
}

func TestSeqRegressionFails(t *testing.T) {
	c := newCodec(testNonce)
	mustEvents(t, c,
		envelope(0, "turn/start", `{"turn":1}`),
		envelope(1, "step/start", `{"turn":1,"step":1}`),
		envelope(2, "step/end", `{"turn":1,"step":1}`),
	)
	err := mustViolation(t, c, envelope(0, "turn/end", `{"turn":1,"reason":{"kind":"completed"}}`))
	if !contains(err.Error(), "regression") {
		t.Fatalf("violation should name regression: %v", err)
	}
}

func TestSeqNegativeFails(t *testing.T) {
	c := newCodec(testNonce)
	mustViolation(t, c, envelope(-1, "turn/start", `{"turn":1}`))
}

// ── §3.8 scope routing & lineage (§16 gate 4) ──────────────────────────────

// bareSession builds a dshSession with just the read-loop-owned state
// (codec + scope + events channel) so handleNotification can be driven
// directly from fixtures — no process, no spawn.
func bareSession(t *testing.T, rootID string) *dshSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &dshSession{
		rootSessionID: rootID,
		events:        make(chan core.Event, eventsBufferSize),
		ctx:           ctx,
		cancel:        cancel,
		codec:         newCodec(testNonce),
		scope:         newScopeRouter(rootID),
	}
}

func notifParams(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rootEvent(t *testing.T, rootID string, seq int, typ, data string) (string, json.RawMessage) {
	return "session.event", notifParams(t, sessionEventParams{
		SessionID: rootID,
		Event:     *envelope(seq, typ, data),
	})
}

func childEvent(t *testing.T, childID string, seq int, typ, data string) (string, json.RawMessage) {
	return "session.event", notifParams(t, sessionEventParams{
		SessionID: childID,
		Event:     *envelope(seq, typ, data),
	})
}

func drainEvents(s *dshSession) []core.Event {
	var out []core.Event
	for {
		select {
		case ev := <-s.events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// Scenario 1: parent turn with a complete child turn interleaved — the root
// timeline contains zero child content and the parent active state survives.
func TestScopeChildTurnDoesNotPolluteRoot(t *testing.T) {
	const root, child = "root-1", "child-1"
	s := bareSession(t, root)

	s.handleNotification("subagent.started", notifParams(t, subagentStartedParams{
		ParentSessionID: root, ChildSessionID: child}))

	// Parent turn opens.
	s.handleNotification(rootEvent(t, root, 0, "turn/start", `{"turn":1}`))
	s.handleNotification(rootEvent(t, root, 1, "step/start", `{"turn":1,"step":1}`))
	s.handleNotification(rootEvent(t, root, 2, "user/message",
		`{"content":[{"type":"text","text":"parent prompt"}],"source":{"kind":"user"},"role":"user","id":"u1"}`))

	// Child runs a FULL turn — every frame filtered, its seq irrelevant.
	s.handleNotification(childEvent(t, child, 0, "turn/start", `{"turn":1}`))
	s.handleNotification(childEvent(t, child, 1, "step/start", `{"turn":1,"step":1}`))
	s.handleNotification(childEvent(t, child, 2, "user/message",
		`{"content":[{"type":"text","text":"child prompt"}],"source":{"kind":"user"},"role":"user","id":"cu1"}`))
	s.handleNotification(childEvent(t, child, 3, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"child text"}}`))
	s.handleNotification(childEvent(t, child, 4, "turn/end", `{"turn":1,"reason":{"kind":"completed"}}`))
	s.handleNotification("subagent.finished", notifParams(t, subagentFinishedParams{
		Provider: "spawn", AgentID: child, ParentSessionID: root, ChildSessionID: child,
		Status: "ok", StopReason: "completed"}))

	// Parent turn continues and closes normally.
	s.handleNotification(rootEvent(t, root, 3, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"parent text"}}`))
	s.handleNotification(rootEvent(t, root, 4, "step/end", `{"turn":1,"step":1}`))
	s.handleNotification(rootEvent(t, root, 5, "turn/end", `{"turn":1,"reason":{"kind":"completed"}}`))

	evs := drainEvents(s)
	var texts, users []core.Event
	term := 0
	for _, e := range evs {
		switch e.Type {
		case core.EventText:
			texts = append(texts, e)
		case core.EventUserMessage:
			users = append(users, e)
		case core.EventResult:
			term++
		}
	}
	if len(users) != 1 || users[0].Content != "parent prompt" {
		t.Fatalf("root must see only the parent user message, got %+v", users)
	}
	if len(texts) != 1 || texts[0].Content != "parent text" {
		t.Fatalf("root must see only parent text, got %+v", texts)
	}
	if term != 1 {
		t.Fatalf("exactly one parent terminal expected, got %d", term)
	}
	if evs[len(evs)-1].Type != core.EventResult || evs[len(evs)-1].TurnID != "p"+testNonce+"-t1" {
		t.Fatalf("last event must be the parent terminal, got %+v", evs[len(evs)-1])
	}
}

// Scenario 2: child idle arriving before parent turn/end must not close the
// parent turn (§3.8 frozen test 2).
func TestScopeChildIdleDoesNotCloseParentTurn(t *testing.T) {
	const root, child = "root-1", "child-1"
	s := bareSession(t, root)
	s.handleNotification("subagent.started", notifParams(t, subagentStartedParams{
		ParentSessionID: root, ChildSessionID: child}))

	s.handleNotification(rootEvent(t, root, 0, "turn/start", `{"turn":1}`))
	drainEvents(s)
	// Child goes idle mid-parent-turn.
	s.handleNotification("session.status", notifParams(t, sessionStatusParams{
		SessionID: child, Status: "idle"}))
	// Parent turn is still open: another root frame maps normally.
	evs := mustApplyRoot(t, s, 1, "step/start", `{"turn":1,"step":1}`)
	if len(evs) != 0 {
		t.Fatalf("step/start is internal, got %+v", evs)
	}
	if s.codec.activeTurn != 1 {
		t.Fatalf("parent turn must still be active after child idle, got %d", s.codec.activeTurn)
	}
}

// mustApplyRoot drives one root-scoped session.event through
// handleNotification and asserts no protocol violation occurred.
func mustApplyRoot(t *testing.T, s *dshSession, seq int, typ, data string) []core.Event {
	t.Helper()
	method, params := rootEvent(t, s.rootSessionID, seq, typ, data)
	s.handleNotification(method, params)
	evs := drainEvents(s)
	for _, e := range evs {
		if e.Type == core.EventError && e.Done {
			t.Fatalf("unexpected protocol violation terminal: %+v", e)
		}
	}
	return evs
}

// mustApplyChild drives one child-scoped session.event (must be filtered).
func mustApplyChild(t *testing.T, s *dshSession, childID string, seq int, typ, data string) {
	t.Helper()
	method, params := childEvent(t, childID, seq, typ, data)
	s.handleNotification(method, params)
	for _, e := range drainEvents(s) {
		if e.Type == core.EventError && e.Done {
			t.Fatalf("child frame must be filtered, got violation: %+v", e)
		}
	}
}

// Scenario 3: two-level descendants (grandchild) are all filtered.
func TestScopeGrandchildFiltered(t *testing.T) {
	const root, child, grand = "root-1", "child-1", "grand-1"
	s := bareSession(t, root)
	s.handleNotification("subagent.started", notifParams(t, subagentStartedParams{
		ParentSessionID: root, ChildSessionID: child}))
	s.handleNotification("subagent.started", notifParams(t, subagentStartedParams{
		ParentSessionID: child, ChildSessionID: grand}))
	s.handleNotification("subagent.finished", notifParams(t, subagentFinishedParams{
		Provider: "spawn", AgentID: child, ParentSessionID: root, ChildSessionID: child,
		Status: "ok", StopReason: "completed"}))

	mustApplyChild(t, s, grand, 0, "turn/start", `{"turn":1}`)
	mustApplyChild(t, s, child, 0, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"x"}}`)
	if got := s.scope.classify(grand); got != scopeDescendant {
		t.Fatalf("grandchild must classify as descendant, got %v", got)
	}
}

// Scenario 4: foreign session.event / session.status → fail visibly (§3.8
// 唯一策略 — foreign = 协议污染，必须停).
func TestScopeForeignFailsVisibly(t *testing.T) {
	const root = "root-1"
	s := bareSession(t, root)

	s.handleNotification(childEvent(t, "stranger-1", 0, "turn/start", `{"turn":1}`))
	evs := drainEvents(s)
	if len(evs) != 1 || evs[0].Type != core.EventError || !evs[0].Done {
		t.Fatalf("foreign session.event must produce exactly one terminal error, got %+v", evs)
	}

	// Foreign status terminates too (round9 P1-2).
	s2 := bareSession(t, root)
	s2.handleNotification("session.status", notifParams(t, sessionStatusParams{
		SessionID: "stranger-2", Status: "running"}))
	evs = drainEvents(s2)
	if len(evs) != 1 || evs[0].Type != core.EventError || !evs[0].Done {
		t.Fatalf("foreign session.status must produce exactly one terminal error, got %+v", evs)
	}
}

// Scenario 5: self-loop / empty-id started edges are rejected without
// injecting the child into the root tree.
func TestScopeStartedEdgeValidation(t *testing.T) {
	const root = "root-1"
	r := newScopeRouter(root)

	if r.recordStarted("", "c1") {
		t.Fatal("empty parent must be rejected")
	}
	if r.recordStarted(root, "") {
		t.Fatal("empty child must be rejected")
	}
	if r.recordStarted("c1", "c1") {
		t.Fatal("self-loop must be rejected")
	}
	if r.classify("c1") != scopeForeign {
		t.Fatal("rejected child must stay foreign")
	}

	// Foreign parent cannot inject an arbitrary child.
	if r.recordStarted("stranger", "c2") {
		t.Fatal("foreign parent must be rejected")
	}
	if r.classify("c2") != scopeForeign {
		t.Fatal("c2 must stay foreign after rejected injection")
	}

	// Cycle: root → a → b, then started(parent=b, child=a) must be rejected.
	if !r.recordStarted(root, "a") || !r.recordStarted("a", "b") {
		t.Fatal("valid edges must be accepted")
	}
	if r.recordStarted("b", "a") {
		t.Fatal("cyclic edge must be rejected")
	}
}

// Scenario 6: finished without lastAssistantMessage keeps lineage normally.
// Scenario 7: late child frames AFTER finished still filter as descendant —
// the tombstone must not be deleted (round8 P0-1).
func TestScopeFinishedRetainsTombstone(t *testing.T) {
	const root, child = "root-1", "child-1"
	s := bareSession(t, root)
	s.handleNotification("subagent.started", notifParams(t, subagentStartedParams{
		ParentSessionID: root, ChildSessionID: child}))
	// lastAssistantMessage absent — schema-optional, no failure.
	s.handleNotification("subagent.finished", notifParams(t, subagentFinishedParams{
		Provider: "spawn", AgentID: child, ParentSessionID: root, ChildSessionID: child,
		Status: "ok", StopReason: "completed"}))

	if got := s.scope.classify(child); got != scopeDescendant {
		t.Fatalf("finished child must stay descendant (tombstone), got %v", got)
	}
	// Late child event + status after finished: filtered, NOT foreign-kill.
	mustApplyChild(t, s, child, 9, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"late"}}`)
	s.handleNotification("session.status", notifParams(t, sessionStatusParams{
		SessionID: child, Status: "idle"}))
	if evs := drainEvents(s); len(evs) != 0 {
		t.Fatalf("late child frames must be silently filtered, got %+v", evs)
	}
}

// Scenario 10: duplicate started (same edge) is idempotent.
// Scenario 11: child-id reuse after finished re-parents via the new edge.
func TestScopeDuplicateStartedAndIdReuse(t *testing.T) {
	const root, child = "root-1", "child-1"
	r := newScopeRouter(root)
	if !r.recordStarted(root, child) {
		t.Fatal("first started must be accepted")
	}
	if !r.recordStarted(root, child) {
		t.Fatal("duplicate started must be idempotent, not a failure")
	}
	if r.parents[child] != root {
		t.Fatalf("edge intact after duplicate: %q", r.parents[child])
	}

	// finished, then the id is reused under a DIFFERENT (valid) parent.
	r.recordFinished(root, child)
	other := "child-2"
	if !r.recordStarted(root, other) {
		t.Fatal("second child edge must be accepted")
	}
	if !r.recordStarted(other, child) {
		t.Fatal("id reuse under a valid parent must re-parent")
	}
	if r.parents[child] != other {
		t.Fatalf("re-parented edge expected, got %q", r.parents[child])
	}
	if r.classify(child) != scopeDescendant {
		t.Fatal("reused id must still be a descendant")
	}
}

// Root seq must not advance on descendant/foreign frames (§3.8:
// session-scope routing precedes seq validation).
func TestScopeRoutingPrecedesSeq(t *testing.T) {
	const root, child = "root-1", "child-1"
	s := bareSession(t, root)
	s.handleNotification("subagent.started", notifParams(t, subagentStartedParams{
		ParentSessionID: root, ChildSessionID: child}))

	// Child seq numbers are independent and arbitrary.
	mustApplyChild(t, s, child, 77, "turn/end", `{"turn":9,"reason":{"kind":"completed"}}`)
	mustApplyChild(t, s, child, 3, "assistant/chunk", `{}`)

	// Root seq still starts at 0.
	mustApplyRoot(t, s, 0, "turn/start", `{"turn":1}`)
	if s.codec.expectedSeq != 1 {
		t.Fatalf("root expectedSeq = %d, want 1", s.codec.expectedSeq)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
