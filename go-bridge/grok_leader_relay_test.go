package gobridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeSessionEventSubscriber mocks core.SessionEventSubscriber for grok leader relay tests.
// It returns a pre-seeded event channel, simulating leader-socket events.
type fakeSessionEventSubscriber struct {
	events chan core.Event
	err    error
}

func (f *fakeSessionEventSubscriber) SubscribeSessionEvents(ctx context.Context, sessionID, cwd string) (<-chan core.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.events, nil
}

// TestGrokLeaderRelay_SynthesizesTurnStartedOnFirstContent verifies that the grok
// leader relay loop synthesizes turn_started + session_state_changed(running) before
// the first content event, because upstream grok-build emits no turn-start signal
// (response_started = 0 in real data).
func TestGrokLeaderRelay_SynthesizesTurnStartedOnFirstContent(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_grok",
	})

	events := make(chan core.Event, 3)
	events <- core.Event{Type: core.EventText, Content: "hello"}
	events <- core.Event{Type: core.EventText, Content: " world"}
	close(events)
	sub := &fakeSessionEventSubscriber{events: events}

	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop("ses_grok", "grokbuild", sub, grokLeaderRelayKey("ses_grok"), "/tmp")
		close(done)
	}()

	// Expect: turn_started(synth) + session_state_changed:running(synth) + text_delta + text_delta
	// Leader channel closed without turn_completed → defer emits turn_aborted(leader_disconnect)
	// + session_state_changed:idle (F-7: 结果未知按「中断」收口, 不猜「完成」)
	names := readEventNames(t, clientConn, 6)
	want := []string{"turn_started", "session_state_changed", "text_delta", "text_delta", "turn_aborted", "session_state_changed"}
	if len(names) != len(want) {
		t.Fatalf("got %d events %v, want %d %v", len(names), names, len(want), want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Fatalf("event[%d] = %q, want %q (full: %v)", i, names[i], w, names)
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay loop did not exit after channel close")
	}
}

// TestGrokLeaderRelay_TurnStartedOnlyOncePerTurn verifies idempotency: multiple
// content events in the same turn produce only one turn_started synthesis.
func TestGrokLeaderRelay_TurnStartedOnlyOncePerTurn(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_grok2",
	})

	events := make(chan core.Event, 5)
	events <- core.Event{Type: core.EventText, Content: "a"}
	events <- core.Event{Type: core.EventThinking, Content: "thinking"}
	events <- core.Event{Type: core.EventToolUse, ToolName: "read"}
	events <- core.Event{Type: core.EventText, Content: "b"}
	events <- core.Event{Type: core.EventResult, Done: true, TurnID: "prompt-1"}
	close(events)
	sub := &fakeSessionEventSubscriber{events: events}

	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop("ses_grok2", "grokbuild", sub, grokLeaderRelayKey("ses_grok2"), "/tmp")
		close(done)
	}()

	// turn_started(1x synth) + running(1x synth) + text_delta + reasoning_delta + tool_started + text_delta + turn_completed
	// turn_completed was received normally → defer does NOT emit fallback idle (turnArmed=false)
	names := readEventNames(t, clientConn, 7)
	// Count turn_started occurrences
	startCount := 0
	for _, n := range names {
		if n == "turn_started" {
			startCount++
		}
	}
	if startCount != 1 {
		t.Fatalf("turn_started emitted %d times, want exactly 1 (events: %v)", startCount, names)
	}
	if names[0] != "turn_started" || names[1] != "session_state_changed" {
		t.Fatalf("first two events should be synth turn_started+running, got %v", names[:2])
	}
	// Last event should be turn_completed (from upstream), NOT session_state_changed:idle
	// (defer idle only fires if turn was armed but never completed)
	if names[len(names)-1] != "turn_completed" {
		t.Fatalf("last event = %q, want turn_completed (defer idle should NOT fire when turn completed normally)", names[len(names)-1])
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay loop did not exit after channel close")
	}
}

// TestGrokLeaderRelay_ErrorStopReasonEmitsWireError verifies that a turn_completed
// with stop_reason=error (mapped to EventError by codec) produces a wire "error"
// event and resets turnArmed so defer idle does NOT also fire.
func TestGrokLeaderRelay_ErrorStopReasonEmitsWireError(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_grok3",
	})

	events := make(chan core.Event, 2)
	events <- core.Event{Type: core.EventText, Content: "partial"}
	// Simulate codec mapping of turn_completed stop_reason=error
	events <- core.Event{Type: core.EventError, Content: "grok turn error", Done: true, TurnID: "p-err"}
	close(events)
	sub := &fakeSessionEventSubscriber{events: events}

	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop("ses_grok3", "grokbuild", sub, grokLeaderRelayKey("ses_grok3"), "/tmp")
		close(done)
	}()

	// turn_started(synth) + running(synth) + text_delta + error
	// error resets turnArmed=false → defer does NOT emit fallback idle
	names := readEventNames(t, clientConn, 4)
	if names[3] != "error" {
		t.Fatalf("last event = %q, want error", names[3])
	}
	for _, n := range names {
		if n == "session_state_changed" && names[3] != "error" {
			// session_state_changed at index 1 (running) is expected; an idle at the end is NOT
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay loop did not exit after channel close")
	}
}

// TestGrokLeaderRelay_PlanDoesNotTriggerTurnStarted verifies that todos_updated
// (EventPlan) alone does NOT synthesize turn_started — plan can arrive before
// the turn truly starts, and must not falsely activate execution state.
func TestGrokLeaderRelay_PlanDoesNotTriggerTurnStarted(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_grok4",
	})

	events := make(chan core.Event, 3)
	events <- core.Event{Type: core.EventPlan, Plan: []core.Todo{{Content: "step1"}}}
	events <- core.Event{Type: core.EventText, Content: "actual content"}
	events <- core.Event{Type: core.EventResult, Done: true}
	close(events)
	sub := &fakeSessionEventSubscriber{events: events}

	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop("ses_grok4", "grokbuild", sub, grokLeaderRelayKey("ses_grok4"), "/tmp")
		close(done)
	}()

	// todos_updated does NOT trigger synth → turn_started should come AFTER it, before text_delta
	// Expected order: todos_updated, turn_started(synth), running(synth), text_delta, turn_completed
	names := readEventNames(t, clientConn, 5)
	if names[0] != "todos_updated" {
		t.Fatalf("event[0] = %q, want todos_updated", names[0])
	}
	if names[1] != "turn_started" {
		t.Fatalf("event[1] = %q, want turn_started (should be triggered by text_delta, not plan)", names[1])
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay loop did not exit after channel close")
	}
}

// TestGrokLeaderRelay_DefersAbortedPlusIdleOnDisconnectWithoutCompletion verifies the
// fallback (F-7): if the leader channel closes while a turn is armed (no turn_completed
// received), defer emits turn_aborted(leader_disconnect) + session_state_changed:idle —
// the turn outcome is unknown and must settle as interrupted, NOT silently guessed
// "completed" — while the trailing idle still prevents isGenerating from being stuck
// (2026-08-04 regression guard).
func TestGrokLeaderRelay_DefersAbortedPlusIdleOnDisconnectWithoutCompletion(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_grok5",
	})

	// Content event arms the turn, then channel closes without turn_completed
	events := make(chan core.Event, 1)
	events <- core.Event{Type: core.EventText, Content: "partial response, leader dies", TurnID: "prompt-dc"}
	close(events)
	sub := &fakeSessionEventSubscriber{events: events}

	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop("ses_grok5", "grokbuild", sub, grokLeaderRelayKey("ses_grok5"), "/tmp")
		close(done)
	}()

	// turn_started(synth) + running(synth) + text_delta + turn_aborted(defer) + session_state_changed:idle(defer)
	names, payloads := readEventNamesWithPayloads(t, clientConn, 5)
	want := []string{"turn_started", "session_state_changed", "text_delta", "turn_aborted", "session_state_changed"}
	for i, w := range want {
		if names[i] != w {
			t.Fatalf("event[%d] = %q, want %q (full: %v)", i, names[i], w, names)
		}
	}
	aborted := payloads[3]
	if aborted["turnId"] != "prompt-dc" || aborted["reason"] != "leader_disconnect" {
		t.Fatalf("turn_aborted payload = %v, want turnId=prompt-dc reason=leader_disconnect", aborted)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay loop did not exit after channel close")
	}
}

// TestGrokLeaderRelay_SubscribeErrorExitsCleanly verifies that if the leader subscriber
// fails to connect, the loop exits without emitting anything (no spurious events).
func TestGrokLeaderRelay_SubscribeErrorExitsCleanly(t *testing.T) {
	serverConn, _, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_grok6",
	})

	sub := &fakeSessionEventSubscriber{err: errors.New("leader socket unavailable")}

	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop("ses_grok6", "grokbuild", sub, grokLeaderRelayKey("ses_grok6"), "/tmp")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay loop should exit immediately on subscribe error")
	}
}

// TestGrokLeaderRelay_FlushesPendingUserMessageWithTurnIdentity: identityless
// user_message (codec 的 user_message_chunk) 必须挂起, 并在首个内容事件携带同 turn
// 的 promptId 时以 user_message 送入投影 —— 否则 reducer skip identityless 事件,
// iOS 只看到回复看不到 prompt。
func TestGrokLeaderRelay_FlushesPendingUserMessageWithTurnIdentity(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_grok_um",
	})

	events := make(chan core.Event, 4)
	events <- core.Event{Type: core.EventUserMessage, Content: "讲个法国笑话"} // identityless
	events <- core.Event{Type: core.EventText, Content: "bonjour", ItemID: "prompt-um", TurnID: "prompt-um"}
	events <- core.Event{Type: core.EventText, Content: "!", ItemID: "prompt-um", TurnID: "prompt-um"}
	events <- core.Event{Type: core.EventResult, Done: true, TurnID: "prompt-um"}
	close(events)
	sub := &fakeSessionEventSubscriber{events: events}

	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop("ses_grok_um", "grokbuild", sub, grokLeaderRelayKey("ses_grok_um"), "/tmp")
		close(done)
	}()

	names, payloads := readEventNamesWithPayloads(t, clientConn, 6)
	want := []string{"turn_started", "user_message", "session_state_changed", "text_delta", "text_delta", "turn_completed"}
	if len(names) != len(want) {
		t.Fatalf("got %d events %v, want %d %v", len(names), names, len(want), want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Fatalf("event[%d] = %q, want %q (full: %v)", i, names[i], w, names)
		}
	}
	um := payloads[1]
	if um["turnId"] != "prompt-um" || um["itemId"] != "prompt-um" || um["text"] != "讲个法国笑话" {
		t.Fatalf("user_message payload = %v, want turnId/itemId=prompt-um text=讲个法国笑话", um)
	}
	if names[0] != "turn_started" {
		t.Fatalf("user_message must be emitted AFTER turn_started, got %v", names[:2])
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay loop did not exit after channel close")
	}
}

// readEventNamesWithPayloads reads count WS frames and returns event names plus
// their payload maps (payload-only assertions for synthesized user_message).
func readEventNamesWithPayloads(t *testing.T, clientConn *websocket.Conn, count int) ([]string, []map[string]interface{}) {
	t.Helper()
	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline failed: %v", err)
	}
	var names []string
	var payloads []map[string]interface{}
	for range count {
		var payload map[string]interface{}
		if err := clientConn.ReadJSON(&payload); err != nil {
			t.Fatalf("read json failed: %v", err)
		}
		eventName, _ := payload["event"].(string)
		names = append(names, eventName)
		if data, ok := payload["data"].(map[string]interface{}); ok {
			payloads = append(payloads, data)
		} else {
			payloads = append(payloads, nil)
		}
	}
	return names, payloads
}
