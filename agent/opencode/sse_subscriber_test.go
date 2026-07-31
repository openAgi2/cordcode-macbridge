package opencode

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newTestSSESubscriber() *sseSubscriber {
	ctx, cancel := context.WithCancel(context.Background())
	return &sseSubscriber{
		events:       make(chan core.Event, 16),
		ctx:          ctx,
		cancel:       cancel,
		messageRoles: make(map[string]string),
		messageIDs:   make(map[string]string),
		partKinds:    make(map[string]string),
		partContent:  make(map[string]string),
		completed:       make(map[string]bool),
		activeTurns:     make(map[string]string),
		userPrompts:     make(map[string]string),
		userTurnStarted: make(map[string]bool),
	}
}

func drainSSEEvents(sub *sseSubscriber) []core.Event {
	var events []core.Event
	for {
		select {
		case ev := <-sub.events:
			events = append(events, ev)
		default:
			return events
		}
	}
}

func TestSSESubscriber_ServerPayloadDelta(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_1","messageID":"msg_1","partID":"part_1","field":"text","delta":"Hello"}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != core.EventText || events[0].Content != "Hello" || events[0].SessionID != "ses_1" {
		t.Fatalf("event = %#v, want text delta for ses_1", events[0])
	}
}

func TestSSESubscriber_ServerPayloadDeltaUsesMessageSession(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"messageID":"msg_1","partID":"part_1","field":"text","delta":"Hello"}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != core.EventText || events[0].Content != "Hello" || events[0].SessionID != "ses_1" {
		t.Fatalf("event = %#v, want inherited session text delta", events[0])
	}
}

func TestSSESubscriber_DirectServerEventShape(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant"}}}`)
	sub.handleRawEvent(`{"type":"message.part.delta","properties":{"messageID":"msg_1","partID":"part_1","field":"text","delta":"Hi"}}`)

	events := drainSSEEvents(sub)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != core.EventText || events[0].Content != "Hi" || events[0].SessionID != "ses_1" {
		t.Fatalf("event = %#v, want direct-shape text delta", events[0])
	}
}

func TestSSESubscriber_MessageUpdatedSnapshotParts(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant","parts":[{"id":"part_1","type":"text","text":"Hello"},{"id":"part_2","type":"reasoning","text":"think"}]}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant","parts":[{"id":"part_1","type":"text","text":"Hello world"},{"id":"part_2","type":"reasoning","text":"think more"}]}}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4: %#v", len(events), events)
	}
	if events[0].Type != core.EventText || events[0].Content != "Hello" {
		t.Fatalf("first event = %#v", events[0])
	}
	if events[1].Type != core.EventThinking || events[1].Content != "think" {
		t.Fatalf("second event = %#v", events[1])
	}
	if events[2].Type != core.EventText || events[2].Content != " world" {
		t.Fatalf("third event = %#v", events[2])
	}
	if events[3].Type != core.EventThinking || events[3].Content != " more" {
		t.Fatalf("fourth event = %#v", events[3])
	}
}

func TestSSESubscriber_NonPrefixRewriteEmitsTextReplace(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant","parts":[{"id":"part_1","type":"text","text":"Hello world"}]}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant","parts":[{"id":"part_1","type":"text","text":"Rewritten content"}]}}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2: %#v", len(events), events)
	}
	if events[0].Type != core.EventText || events[0].Content != "Hello world" {
		t.Fatalf("first event = %#v", events[0])
	}
	if events[1].Type != core.EventTextReplace || events[1].Content != "Rewritten content" {
		t.Fatalf("second event = %#v, want EventTextReplace with full content", events[1])
	}
}

func TestSSESubscriber_ServerPayloadPartUpdatedEmitsSuffix(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.updated","properties":{"sessionID":"ses_1","messageID":"msg_1","part":{"id":"part_1","type":"reasoning","text":"think"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.updated","properties":{"sessionID":"ses_1","messageID":"msg_1","part":{"id":"part_1","type":"reasoning","text":"think more"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.updated","properties":{"sessionID":"ses_1","messageID":"msg_1","part":{"id":"part_1","type":"reasoning","text":"think more"}}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2: %#v", len(events), events)
	}
	if events[0].Type != core.EventThinking || events[0].Content != "think" {
		t.Fatalf("first event = %#v, want initial reasoning", events[0])
	}
	if events[1].Type != core.EventThinking || events[1].Content != " more" {
		t.Fatalf("second event = %#v, want suffix reasoning", events[1])
	}
}

// OpenCode often emits bare message.updated (role=user, no parts) then the real
// prompt as message.part.delta. Projection SoT needs that text as user_message;
// otherwise iOS shows assistant replies with no user bubble.
func TestSSESubscriber_BareUserUpdatedThenPartDeltaEmitsUserPrompt(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_user","sessionID":"ses_1","role":"user"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_1","messageID":"msg_user","partID":"part_1","field":"text","delta":"讲个月球笑话"}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_1","messageID":"msg_asst","partID":"part_a","field":"text","delta":"reply"}}}`)

	events := drainSSEEvents(sub)
	if len(events) < 3 {
		t.Fatalf("event count = %d, want >=3: %#v", len(events), events)
	}
	if events[0].Type != core.EventUserMessage || events[0].Content != "讲个月球笑话" || events[0].TurnID != "msg_user" {
		t.Fatalf("user_message = %#v", events[0])
	}
	if events[1].Type != core.EventTurnStarted || events[1].TurnID != "msg_user" {
		t.Fatalf("turn_started = %#v", events[1])
	}
	// Assistant text must attribute to the user turn id (activeTurn armed by bare updated).
	foundAsst := false
	for _, ev := range events[2:] {
		if ev.Type == core.EventText && ev.Content == "reply" && ev.TurnID == "msg_user" && ev.ItemID == "msg_user" {
			foundAsst = true
			break
		}
	}
	if !foundAsst {
		t.Fatalf("assistant text missing or unattributed: %#v", events)
	}
}

func TestSSESubscriber_UserPartUpdatedEmitsUserPrompt(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_user","sessionID":"ses_1","role":"user"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.updated","properties":{"sessionID":"ses_1","messageID":"msg_user","part":{"id":"part_1","type":"text","text":"讲个太阳笑话"}}}}`)

	events := drainSSEEvents(sub)
	if len(events) < 2 {
		t.Fatalf("event count = %d, want >=2: %#v", len(events), events)
	}
	if events[0].Type != core.EventUserMessage || events[0].Content != "讲个太阳笑话" {
		t.Fatalf("user_message = %#v", events[0])
	}
	if events[1].Type != core.EventTurnStarted || events[1].TurnID != "msg_user" {
		t.Fatalf("turn_started = %#v", events[1])
	}
}

func TestSSESubscriber_ServerPayloadToolTodoAndIdle(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.part.updated","properties":{"sessionID":"ses_1","messageID":"msg_1","part":{"id":"tool_1","type":"tool","tool":"bash","state":{"status":"completed","title":"List files","output":"ok"}}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"todo.updated","properties":{"sessionID":"ses_1","todos":[{"content":"Ship fix","status":"in_progress","priority":"high"}]}}}`)
	sub.handleRawEvent(`{"payload":{"type":"session.status","properties":{"sessionID":"ses_1","type":"idle"}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4: %#v", len(events), events)
	}
	if events[0].Type != core.EventToolUse || events[0].ToolName != "bash" || events[0].ToolInput != "List files" {
		t.Fatalf("tool use event = %#v", events[0])
	}
	if events[1].Type != core.EventToolResult || events[1].ToolResult != "ok" || events[1].ToolStatus != "completed" {
		t.Fatalf("tool result event = %#v", events[1])
	}
	if events[2].Type != core.EventPlan || len(events[2].Plan) != 1 || events[2].Plan[0].Content != "Ship fix" {
		t.Fatalf("todo event = %#v", events[2])
	}
	if events[3].Type != core.EventResult || !events[3].Done || events[3].SessionID != "ses_1" {
		t.Fatalf("result event = %#v", events[3])
	}
}

func TestSSESubscriber_ServerPayloadTodoEmptyClearsPlan(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"todo.updated","properties":{"sessionID":"ses_1","todos":[]}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != core.EventPlan || len(events[0].Plan) != 0 || events[0].SessionID != "ses_1" {
		t.Fatalf("todo clear event = %#v", events[0])
	}
}

func TestSSESubscriber_ServerPayloadCompletionIsIdempotent(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	// Intermediate assistant message completion must NOT complete the turn by itself.
	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant","time":{"completed":1710000000}}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"session.status","properties":{"sessionID":"ses_1","type":"idle"}}}`)
	sub.handleRawEvent(`{"payload":{"type":"session.status","properties":{"sessionID":"ses_1","type":"idle"}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != core.EventResult || !events[0].Done || events[0].SessionID != "ses_1" {
		t.Fatalf("result event = %#v", events[0])
	}
}

// Multi-step tool turns: intermediate assistant completion + step_finish must not emit
// EventResult; only session.status idle closes the turn (composer stays 执行中 across tools).
func TestSSESubscriber_MultiStepToolsDoNotCompleteUntilSessionIdle(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_user","sessionID":"ses_1","role":"user","parts":[{"type":"text","text":"do tasks"}]}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_1","messageID":"msg_a1","partID":"t1","field":"text","delta":"Working"}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.updated","properties":{"sessionID":"ses_1","messageID":"msg_a1","part":{"id":"tool_1","type":"tool","tool":"read","state":{"status":"completed","title":"Read f","output":"ok"}}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_a1","sessionID":"ses_1","role":"assistant","time":{"completed":1710000001},"parts":[{"id":"tool_1","type":"tool","tool":"read"}]}}}}`)
	sub.handleRawEvent(`{"type":"step_finish","part":{"type":"step-finish","reason":"tool-calls"},"sessionID":"ses_1"}`)
	sub.handleRawEvent(`{"payload":{"type":"todo.updated","properties":{"sessionID":"ses_1","todos":[{"content":"A","status":"in_progress","priority":"high"}]}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_1","messageID":"msg_a2","partID":"t2","field":"text","delta":"Done"}}}`)
	sub.handleRawEvent(`{"payload":{"type":"session.status","properties":{"sessionID":"ses_1","type":"idle"}}}`)

	events := drainSSEEvents(sub)
	var results []core.Event
	var plans []core.Event
	for _, ev := range events {
		switch ev.Type {
		case core.EventResult:
			results = append(results, ev)
		case core.EventPlan:
			plans = append(plans, ev)
		}
	}
	if len(results) != 1 {
		t.Fatalf("EventResult count = %d, want 1 (only session idle): %#v", len(results), events)
	}
	if !results[0].Done || results[0].SessionID != "ses_1" {
		t.Fatalf("result = %#v", results[0])
	}
	if len(plans) != 1 || plans[0].Plan[0].Content != "A" {
		t.Fatalf("plan events = %#v", plans)
	}
}

func TestSSESubscriber_CompletionResetsForNextTurn(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"session.status","properties":{"sessionID":"ses_1","type":"idle"}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_user_2","sessionID":"ses_1","role":"user"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"session.status","properties":{"sessionID":"ses_1","type":"idle"}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2: %#v", len(events), events)
	}
	for i, event := range events {
		if event.Type != core.EventResult || !event.Done || event.SessionID != "ses_1" {
			t.Fatalf("event[%d] = %#v, want completion for ses_1", i, event)
		}
	}
}

func TestSSESubscriber_UserMessageWithTextEmitsProjectionIdentity(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_user","sessionID":"ses_1","role":"user","parts":[{"type":"text","text":"hello"}]}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_1","messageID":"msg_asst","partID":"part_1","field":"text","delta":"world"}}}`)

	events := drainSSEEvents(sub)
	if len(events) < 3 {
		t.Fatalf("event count = %d, want >=3: %#v", len(events), events)
	}
	if events[0].Type != core.EventUserMessage || events[0].TurnID != "msg_user" || events[0].Content != "hello" {
		t.Fatalf("user event = %#v", events[0])
	}
	if events[1].Type != core.EventTurnStarted || events[1].TurnID != "msg_user" {
		t.Fatalf("turn_started = %#v", events[1])
	}
	if events[2].Type != core.EventText || events[2].Content != "world" || events[2].TurnID != "msg_user" || events[2].ItemID != "msg_user" {
		t.Fatalf("assistant text must attribute to user turn: %#v", events[2])
	}
}

func TestSSESubscriber_StillHandlesCLINDJSONShape(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"type":"text","sessionID":"ses_1","part":{"type":"text","text":"Hello"}}`)

	events := drainSSEEvents(sub)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != core.EventText || events[0].Content != "Hello" || events[0].SessionID != "ses_1" {
		t.Fatalf("event = %#v, want CLI text event", events[0])
	}
}
