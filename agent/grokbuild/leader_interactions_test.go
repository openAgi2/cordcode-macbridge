package grokbuild

// Follower question interaction tests for the leader subscriber read path.
//
// All wire fixtures are VERBATIM from the installed grok 1.0.13 live capture
// (docs/2026-09-02-grokbuild-follower-interaction-research.md §3.1/§3.2/§3.4)
// — do not "clean up" the shapes; the exact field forms (numeric id,
// half-wrapped method, multiSelect:null) are the contract under test.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// writeACPRequestRaw frames a raw JSON-RPC request string (id + method +
// params — as captured on the wire) inside a leader "acp" envelope.
func writeACPRequestRaw(w interface{ Write([]byte) (int, error) }, rawJSON string) error {
	env, _ := json.Marshal(leaderServerMsg{Type: "acp", Payload: rawJSON})
	return writeTestFrame(w, env)
}

// leaderHandshake performs register → initialize → session/load and leaves the
// connection ready for injected frames.
func leaderHandshake(c net.Conn) error {
	reg, err := readClientMsg(c)
	if err != nil {
		return err
	}
	if reg.Type != "register" || reg.ClientType != leaderClientType {
		return fmt.Errorf("want register/%s, got %s/%s", leaderClientType, reg.Type, reg.ClientType)
	}
	rr, _ := json.Marshal(leaderServerMsg{Type: "registered", Ready: true})
	if err := writeTestFrame(c, rr); err != nil {
		return err
	}
	init, err := readClientMsg(c)
	if err != nil {
		return err
	}
	if err := writeACPResponse(c, acpPayloadID(init.Payload), map[string]any{"protocolVersion": "1"}); err != nil {
		return err
	}
	load, err := readClientMsg(c)
	if err != nil {
		return err
	}
	return writeACPResponse(c, acpPayloadID(load.Payload), map[string]any{})
}

// runLeaderSubscriber runs a full subscriber session against a mock leader
// whose script injects frames after the handshake, and returns (events,
// subscriber) for post-hoc assertions on both the event stream and the
// interaction registry.
func runLeaderSubscriber(t *testing.T, script func(c net.Conn) error) ([]core.Event, *LeaderSubscriber) {
	t.Helper()
	sock := filepath.Join("/tmp", fmt.Sprintf("cc-grok-leader-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var got []core.Event
	var mu sync.Mutex
	onEvent := func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		if err := leaderHandshake(c); err != nil {
			serverErr <- err
			return
		}
		serverErr <- script(c)
	}()

	sub := NewLeaderSubscriber(sock, "sess-1", "/tmp")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = sub.Run(ctx, onEvent)
	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return got, sub
}

// §3.1 fixture, verbatim: half-wrapped REQUEST with original numeric id=0.
const fixtureAskUserQuestionHalfWrapped = `{"jsonrpc":"2.0","id":0,"method":"_x.ai/ask_user_question","params":{"sessionId":"01a06290-1a64-70e1-a125-824782ed79ff","toolCallId":"call_410dc27a15f64707b7f36ca2","questions":[{"question":"你偏好哪种配色主题?","options":[{"label":"深色主题","description":"界面以深色背景为主,适合弱光环境"},{"label":"浅色主题","description":"界面以浅色背景为主,适合明亮环境"}],"multiSelect":null}],"mode":"default"}}`

// §3.4 fixture, verbatim: replay-on-attach of the still-pending id=2 request
// (same id, same payload form as the original broadcast).
const fixtureAskUserQuestionReplay = `{"jsonrpc":"2.0","id":2,"method":"_x.ai/ask_user_question","params":{"sessionId":"01a06290-1a64-70e1-a125-824782ed79ff","toolCallId":"call_d45f229bcd024f67b0ab9984","questions":[{"question":"你偏好哪种饮品?","options":[{"label":"咖啡","description":"含咖啡因的提神饮品"},{"label":"茶","description":"茶类饮品"}],"multiSelect":null}],"mode":"default"}}`

// TestLeaderSubscriberAskUserQuestionHalfWrapped: the §3.1 REQUEST form must
// register the interaction (keyed by tool_call_id, wire id preserved) and emit
// one EventQuestionAsked whose option ids are the grok labels.
func TestLeaderSubscriberAskUserQuestionHalfWrapped(t *testing.T) {
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionHalfWrapped); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		return nil
	})
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 question_asked: %+v", len(got), got)
	}
	ev := got[0]
	if ev.Type != core.EventQuestionAsked {
		t.Fatalf("type = %v, want question_asked", ev.Type)
	}
	if ev.QuestionID != "call_410dc27a15f64707b7f36ca2" {
		t.Fatalf("questionID = %q, want the tool_call_id (single-question uses the bare id)", ev.QuestionID)
	}
	if ev.QuestionText != "你偏好哪种配色主题?" {
		t.Fatalf("questionText = %q", ev.QuestionText)
	}
	if len(ev.QuestionOpts) != 2 {
		t.Fatalf("opts = %+v, want 2", ev.QuestionOpts)
	}
	for i, want := range []struct{ id, desc string }{
		{"深色主题", "界面以深色背景为主,适合弱光环境"},
		{"浅色主题", "界面以浅色背景为主,适合明亮环境"},
	} {
		if ev.QuestionOpts[i].ID != want.id || ev.QuestionOpts[i].Label != want.id || ev.QuestionOpts[i].Description != want.desc {
			t.Fatalf("opt[%d] = %+v, want id/label=%q desc=%q", i, ev.QuestionOpts[i], want.id, want.desc)
		}
	}
	if sub.interactions == nil || sub.interactions.len() != 1 {
		t.Fatalf("registry len = %v, want 1", sub.interactions)
	}
	entry, ok := sub.interactions.take("call_410dc27a15f64707b7f36ca2")
	if !ok {
		t.Fatal("interaction not registered by tool_call_id")
	}
	if entry.wireID != 0 {
		t.Fatalf("wireID = %d, want original numeric id 0", entry.wireID)
	}
	if entry.params.Mode != "default" || len(entry.params.Questions) != 1 {
		t.Fatalf("registered params = %+v", entry.params)
	}
}

// TestLeaderSubscriberAskUserQuestionFullyWrapped: upstream server.rs
// method_of/interaction_inner_params also admit a fully wrapped ext payload
// (params carries its own method+params). Both forms must normalize to the
// same behavior (research §2.1.5).
func TestLeaderSubscriberAskUserQuestionFullyWrapped(t *testing.T) {
	const fullyWrapped = `{"jsonrpc":"2.0","id":7,"method":"_x.ai/ask_user_question","params":{"method":"x.ai/ask_user_question","params":{"sessionId":"sess-1","toolCallId":"call_full_wrap","questions":[{"question":"哪种形态?","options":[{"label":"A","description":"全包装"},{"label":"B","description":"半包装"}],"multiSelect":null}],"mode":"default"}}}`
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fullyWrapped); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		return nil
	})
	if len(got) != 1 || got[0].Type != core.EventQuestionAsked {
		t.Fatalf("got %+v, want 1 question_asked", got)
	}
	if got[0].QuestionID != "call_full_wrap" || got[0].QuestionText != "哪种形态?" {
		t.Fatalf("ev = %+v", got[0])
	}
	entry, ok := sub.interactions.take("call_full_wrap")
	if !ok || entry.wireID != 7 {
		t.Fatalf("registry entry = %+v ok=%v, want wireID 7", entry, ok)
	}
}

// TestLeaderSubscriberInteractionLifecycle: §3.2 sequence — pending_interaction
// (with kind) is visibility only; interaction_resolved (tool_call_id only)
// evicts the registry and closes every surfaced question id; a second resolved
// for an already-evicted entry and a resolved for a never-registered
// (permission) tool call are silent no-ops.
func TestLeaderSubscriberInteractionLifecycle(t *testing.T) {
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionHalfWrapped); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		// pending_interaction with kind (§3.2 form 1) — no event, no eviction.
		if err := writeACPNotification(c, "_x.ai/session_notification", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "pending_interaction", "tool_call_id": "call_410dc27a15f64707b7f36ca2", "kind": "question"},
		}); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		// resolved (§3.2 form 2) — evict + question_resolved.
		if err := writeACPNotification(c, "_x.ai/session_notification", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "interaction_resolved", "tool_call_id": "call_410dc27a15f64707b7f36ca2"},
		}); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		// Duplicate resolved — silent.
		if err := writeACPNotification(c, "_x.ai/session_notification", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "interaction_resolved", "tool_call_id": "call_410dc27a15f64707b7f36ca2"},
		}); err != nil {
			return err
		}
		// Permission-family resolution we never registered — silent.
		return writeACPNotification(c, "_x.ai/session_notification", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "interaction_resolved", "tool_call_id": "call_permission_only"},
		})
	})
	if len(got) != 2 {
		t.Fatalf("got %d events, want exactly question_asked + question_resolved: %+v", len(got), got)
	}
	if got[0].Type != core.EventQuestionAsked || got[1].Type != core.EventQuestionResolved {
		t.Fatalf("event sequence = %v then %v, want question_asked → question_resolved", got[0].Type, got[1].Type)
	}
	if got[1].QuestionID != "call_410dc27a15f64707b7f36ca2" {
		t.Fatalf("resolved questionID = %q, want the tool_call_id", got[1].QuestionID)
	}
	if sub.interactions.len() != 0 {
		t.Fatalf("registry len = %d after resolution, want 0", sub.interactions.len())
	}
}

// TestLeaderSubscriberAskUserQuestionReplay: §3.4/§3.6 — replay-on-attach
// re-delivers the SAME request frame (same id, same payload). The read path
// must register and re-surface it (this is the reconnect recovery mechanism),
// NOT drop it as stale.
func TestLeaderSubscriberAskUserQuestionReplay(t *testing.T) {
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionReplay); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		// Attach-time replay: same id, same payload verbatim.
		return writeACPRequestRaw(c, fixtureAskUserQuestionReplay)
	})
	if len(got) != 2 {
		t.Fatalf("got %d events, want question_asked re-emitted on replay: %+v", len(got), got)
	}
	for i := range got {
		if got[i].Type != core.EventQuestionAsked || got[i].QuestionID != "call_d45f229bcd024f67b0ab9984" || got[i].QuestionText != "你偏好哪种饮品?" {
			t.Fatalf("event[%d] = %+v, want replayed ask for 饮品", i, got[i])
		}
	}
	if sub.interactions.len() != 1 {
		t.Fatalf("registry len = %d, want 1 (replay overwrites the same tool_call_id key)", sub.interactions.len())
	}
	if entry, _ := sub.interactions.take("call_d45f229bcd024f67b0ab9984"); entry.wireID != 2 {
		t.Fatalf("wireID = %d, want replayed id 2", entry.wireID)
	}
}

// TestLeaderSubscriberIgnoresOtherRequestForms: reverse-requests outside the
// question scope stay observe-only (ruling B), and non-numeric ids are
// dropped — neither may register or emit.
func TestLeaderSubscriberIgnoresOtherRequestForms(t *testing.T) {
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		// session/request_permission REQUEST (numeric id) — observe-only.
		if err := writeACPRequestRaw(c, `{"jsonrpc":"2.0","id":5,"method":"session/request_permission","params":{"sessionId":"sess-1","toolCall":{"toolCallId":"call_perm","title":"rm","kind":"execute","status":"pending"},"options":[{"optionId":"allow_once","name":"Allow","kind":"allow_once"}]}}`); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		// exit_plan_mode ext request — observe-only.
		if err := writeACPRequestRaw(c, `{"jsonrpc":"2.0","id":6,"method":"_x.ai/exit_plan_mode","params":{"sessionId":"sess-1"}}`); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		// Non-numeric id — dropped without panic.
		return writeACPRequestRaw(c, `{"jsonrpc":"2.0","id":"string-id","method":"_x.ai/ask_user_question","params":{"sessionId":"sess-1","toolCallId":"call_str","questions":[{"question":"q","options":[{"label":"x","description":""}],"multiSelect":null}],"mode":"default"}}`)
	})
	if len(got) != 0 {
		t.Fatalf("got %+v, want no events for observe-only requests", got)
	}
	if sub.interactions.len() != 0 {
		t.Fatalf("registry len = %d, want 0", sub.interactions.len())
	}
}

// --- unit tests for the normalization/derivation helpers ---

func TestNormalizeLeaderMethod(t *testing.T) {
	for _, tc := range []struct {
		top, params, want string
	}{
		{"session/update", `{}`, "session/update"},                                       // unprefixed passes through
		{"_x.ai/ask_user_question", `{"sessionId":"s"}`, "x.ai/ask_user_question"},       // half-wrapped: strip only
		{"_x.ai/ask_user_question", `{"method":"x.ai/other","params":{}}`, "x.ai/other"}, // fully wrapped: params.method wins
		{"_x.ai/foo", `{"method":"","params":{}}`, "x.ai/foo"},                           // empty params.method ignored
		{"_x.ai/foo", ``, "x.ai/foo"},                                                    // no params at all
	} {
		if got := normalizeLeaderMethod(tc.top, json.RawMessage(tc.params)); got != tc.want {
			t.Errorf("normalizeLeaderMethod(%q, %s) = %q, want %q", tc.top, tc.params, got, tc.want)
		}
	}
}

func TestInteractionInnerParams(t *testing.T) {
	// Half-wrapped: params IS the real payload.
	real := json.RawMessage(`{"sessionId":"s","toolCallId":"t"}`)
	if got := interactionInnerParams(real); string(got) != string(real) {
		t.Errorf("half-wrapped params mutated: %s", got)
	}
	// Fully wrapped: real params live at params.params.
	wrapped := json.RawMessage(`{"method":"x.ai/ask_user_question","params":{"toolCallId":"t"}}`)
	got := interactionInnerParams(wrapped)
	if string(got) != `{"toolCallId":"t"}` {
		t.Errorf("fully wrapped inner = %s", got)
	}
}

func TestQuestionIDFor(t *testing.T) {
	if q := questionIDFor("call_x", 0); q != "call_x" {
		t.Errorf("index 0 = %q, want bare id", q)
	}
	if q := questionIDFor("call_x", 2); q != "call_x#2" {
		t.Errorf("index 2 = %q, want call_x#2", q)
	}
}

func TestPeekInteractionLifecycle(t *testing.T) {
	if u, tc := peekInteractionLifecycle(json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"pending_interaction","tool_call_id":"c1","kind":"question"}}`)); u != "pending_interaction" || tc != "c1" {
		t.Errorf("pending peek = %q/%q", u, tc)
	}
	if u, tc := peekInteractionLifecycle(json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"interaction_resolved","tool_call_id":"c2"}}`)); u != "interaction_resolved" || tc != "c2" {
		t.Errorf("resolved peek = %q/%q", u, tc)
	}
	if u, tc := peekInteractionLifecycle(json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}}`)); u != "" || tc != "" {
		t.Errorf("non-lifecycle peek = %q/%q, want empty", u, tc)
	}
}
