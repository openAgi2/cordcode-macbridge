package grokbuild

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 2026-09-02 owner 验收 row 6：iPhone 自己发起 grok turn，流式正常但发送的
// prompt 不显示（输出直接接在上个回复后面）。根因：user_message_chunk 按上游
// 设计不带 promptId，echo 在 relayEvents 路径无身份 → SSV2 reducer 跳过 →
// 乐观占位释放后用户消息消失。emitTurnScoped 在 session 层缓冲 echo、用同 turn
// 首个带 promptId 的事件补身份后再发。本文件钉住该语义。

func newEchoTestSession() *grokSession {
	s := &grokSession{
		agent:  &Agent{},
		events: make(chan core.Event, 64),
		stdin:  nopWriteCloser{},
		ctx:    context.Background(),
	}
	s.sessionID.Store("sess-echo")
	s.alive.Store(true)
	return s
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

func feedUpdate(t *testing.T, s *grokSession, params string) {
	t.Helper()
	s.handleNotification(&agentNotification{
		Method: "session/update",
		Params: json.RawMessage(params),
	})
}

func drainEchoEvents(t *testing.T, s *grokSession) []core.Event {
	t.Helper()
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

func TestEmitTurnScoped_StampsEchoWithFirstContentTurnID(t *testing.T) {
	s := newEchoTestSession()
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"蝙蝠侠和猫女的故事200字"}}}`)
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"思考"}},"_meta":{"promptId":"turn-abc"}}`)

	events := drainEchoEvents(t, s)
	if len(events) != 2 {
		t.Fatalf("expected [user_message, thought], got %d events", len(events))
	}
	if events[0].Type != core.EventUserMessage {
		t.Fatalf("first event must be the stamped user_message, got %v", events[0].Type)
	}
	if events[0].TurnID != "turn-abc" || events[0].ItemID != "turn-abc" {
		t.Fatalf("user_message must carry the turn identity, got turnId=%q itemId=%q", events[0].TurnID, events[0].ItemID)
	}
	if events[0].Content != "蝙蝠侠和猫女的故事200字" {
		t.Fatalf("user_message text mismatch: %q", events[0].Content)
	}
	if events[1].Type != core.EventThinking {
		t.Fatalf("second event must be the thought chunk, got %v", events[1].Type)
	}
}

func TestEmitTurnScoped_LateEchoStampedBeforeNextChunk(t *testing.T) {
	s := newEchoTestSession()
	// 异常顺序：内容事件先到（turn 已可归属），echo 后到，再一个内容事件。
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"第一段"}},"_meta":{"promptId":"turn-1"}}`)
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"稍后到达的 prompt"}}}`)
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"第二段"}},"_meta":{"promptId":"turn-1"}}`)

	events := drainEchoEvents(t, s)
	if len(events) != 3 {
		t.Fatalf("expected [text, user_message, text], got %d events", len(events))
	}
	if events[1].Type != core.EventUserMessage || events[1].TurnID != "turn-1" {
		t.Fatalf("late echo must be stamped with the armed turn identity, got %+v", events[1])
	}
	if events[2].Type != core.EventText {
		t.Fatalf("last event must be the follow-up chunk, got %v", events[2].Type)
	}
}

func TestEmitTurnScoped_EmptyReplyTurnStampsBeforeTerminal(t *testing.T) {
	s := newEchoTestSession()
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"空回复的问题"}}}`)
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"turn_completed","stopReason":"end_turn","prompt_id":"turn-zz"}}`)

	events := drainEchoEvents(t, s)
	if len(events) != 2 {
		t.Fatalf("expected [user_message, result], got %d events", len(events))
	}
	if events[0].Type != core.EventUserMessage || events[0].TurnID != "turn-zz" {
		t.Fatalf("echo must be stamped from the terminal prompt_id, got %+v", events[0])
	}
	if events[1].Type != core.EventResult || !events[1].Done {
		t.Fatalf("terminal must still flow through, got %+v", events[1])
	}
}

func TestEmitTurnScoped_SendClearsStaleEcho(t *testing.T) {
	s := newEchoTestSession()
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"上个 turn 的残留"}}}`)
	drainEchoEvents(t, s)

	if err := s.Send("新 turn 的问题", nil, nil); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"回复"}},"_meta":{"promptId":"turn-2"}}`)

	events := drainEchoEvents(t, s)
	for _, ev := range events {
		if ev.Type == core.EventUserMessage && ev.Content == "上个 turn 的残留" {
			t.Fatal("stale echo from previous turn must not leak into the new turn")
		}
	}
}

func TestEmitTurnScoped_IdentitylessEchoWithoutFollowUpStaysBuffered(t *testing.T) {
	s := newEchoTestSession()
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"悬置的 echo"}}}`)
	// 不带 promptId 的事件不消费缓冲。
	feedUpdate(t, s, `{"sessionId":"sess-echo","update":{"sessionUpdate":"available_commands_update"}}`)

	events := drainEchoEvents(t, s)
	if len(events) != 0 {
		t.Fatalf("echo must stay buffered until identity arrives, got %d events", len(events))
	}
	s.pendingPermsMu.Lock()
	buffered := s.pendingUserEcho
	s.pendingPermsMu.Unlock()
	if buffered != "悬置的 echo" {
		t.Fatalf("buffer content mismatch: %q", buffered)
	}
}
