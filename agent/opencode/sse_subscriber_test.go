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
		completed:    make(map[string]bool),
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

func TestSSESubscriber_ServerPayloadIgnoresUserDelta(t *testing.T) {
	sub := newTestSSESubscriber()
	defer sub.cancel()

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_user","sessionID":"ses_1","role":"user"}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_1","messageID":"msg_user","partID":"part_1","field":"text","delta":"prompt"}}}`)

	if events := drainSSEEvents(sub); len(events) != 0 {
		t.Fatalf("event count = %d, want 0: %#v", len(events), events)
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

	sub.handleRawEvent(`{"payload":{"type":"message.updated","properties":{"info":{"id":"msg_1","sessionID":"ses_1","role":"assistant","time":{"completed":1710000000}}}}}`)
	sub.handleRawEvent(`{"payload":{"type":"session.status","properties":{"sessionID":"ses_1","type":"idle"}}}`)

	events := drainSSEEvents(sub)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != core.EventResult || !events[0].Done || events[0].SessionID != "ses_1" {
		t.Fatalf("result event = %#v", events[0])
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
