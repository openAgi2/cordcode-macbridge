package codex

import (
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newTestAppServerSession(t *testing.T) *appServerSession {
	t.Helper()
	return &appServerSession{
		events: make(chan core.Event, 64),
	}
}

func collectAppServerEvents(s *appServerSession, n int) []core.Event {
	var evts []core.Event
	for i := 0; i < n; i++ {
		select {
		case evt, ok := <-s.events:
			if !ok {
				return evts
			}
			evts = append(evts, evt)
		default:
			return evts
		}
	}
	return evts
}

// 真实 payload 结构（来自 Codex SDK v2_all.py）：
// AgentMessageDeltaNotification: delta, itemId, threadId, turnId
// ReasoningTextDeltaNotification: delta, itemId, threadId, turnId, contentIndex

// Test 1: agentMessage delta → EventText
func TestHandleAgentMessageDelta_EmitsEventText(t *testing.T) {
	s := newTestAppServerSession(t)
	params, _ := json.Marshal(map[string]any{
		"delta":    "Hello",
		"itemId":   "item-1",
		"threadId": "thread-1",
		"turnId":   "turn-1",
	})

	s.handleAgentMessageDelta(params)

	evts := collectAppServerEvents(s, 1)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1", len(evts))
	}
	if evts[0].Type != core.EventText {
		t.Errorf("type = %q, want text", evts[0].Type)
	}
	if evts[0].Content != "Hello" {
		t.Errorf("content = %q, want Hello", evts[0].Content)
	}
}

// Test 2: reasoning delta → EventThinking
func TestHandleReasoningTextDelta_EmitsEventThinking(t *testing.T) {
	s := newTestAppServerSession(t)
	params, _ := json.Marshal(map[string]any{
		"delta":        "Let me think",
		"itemId":       "item-r1",
		"threadId":     "thread-1",
		"turnId":       "turn-1",
		"contentIndex": 0,
	})

	s.handleReasoningTextDelta(params)

	evts := collectAppServerEvents(s, 1)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1", len(evts))
	}
	if evts[0].Type != core.EventThinking {
		t.Errorf("type = %q, want thinking", evts[0].Type)
	}
	if evts[0].Content != "Let me think" {
		t.Errorf("content = %q, want 'Let me think'", evts[0].Content)
	}
}

// Test 3: delta 后 agentMessage completed 不进入 pendingMsgs
func TestHandleItemCompleted_AgentMessage_AfterDelta_SkipsPendingMsgs(t *testing.T) {
	s := newTestAppServerSession(t)

	params, _ := json.Marshal(map[string]any{
		"delta":    "Hello",
		"itemId":   "item-1",
		"threadId": "thread-1",
		"turnId":   "turn-1",
	})
	s.handleAgentMessageDelta(params)

	s.handleItemCompleted(map[string]any{
		"type": "agentMessage",
		"id":   "item-1",
		"text": "Hello world",
	})

	evts := collectAppServerEvents(s, 2)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1 (delta only, completed skipped)", len(evts))
	}
	if evts[0].Content != "Hello" {
		t.Errorf("content = %q, want Hello", evts[0].Content)
	}
}

// Test 4: 未收到 delta 的 agentMessage completed → fallback 发文本
func TestHandleItemCompleted_AgentMessage_NoDelta_Fallback(t *testing.T) {
	s := newTestAppServerSession(t)

	s.handleItemCompleted(map[string]any{
		"type": "agentMessage",
		"id":   "item-2",
		"text": "Full text here",
	})
	s.flushPendingAsText()

	evts := collectAppServerEvents(s, 1)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1", len(evts))
	}
	if evts[0].Content != "Full text here" {
		t.Errorf("content = %q, want 'Full text here'", evts[0].Content)
	}
}

// Test 5: reasoning delta 后 reasoning completed 跳过全文
func TestHandleItemCompleted_Reasoning_AfterDelta_SkipsFullText(t *testing.T) {
	s := newTestAppServerSession(t)

	params, _ := json.Marshal(map[string]any{
		"delta":    "thinking delta",
		"itemId":   "item-r1",
		"threadId": "thread-1",
		"turnId":   "turn-1",
	})
	s.handleReasoningTextDelta(params)

	s.handleItemCompleted(map[string]any{
		"type":    "reasoning",
		"id":      "item-r1",
		"summary": []any{"full thinking text"},
	})

	evts := collectAppServerEvents(s, 2)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1 (delta only, completed skipped)", len(evts))
	}
	if evts[0].Content != "thinking delta" {
		t.Errorf("content = %q, want 'thinking delta'", evts[0].Content)
	}
}

// Test 6: 空 itemId → delta 正常发但不写 sawDelta，completed fallback 保留
func TestHandleDelta_EmptyItemId_DoesNotMarkSawDelta(t *testing.T) {
	s := newTestAppServerSession(t)

	params, _ := json.Marshal(map[string]any{
		"delta":    "delta text",
		"itemId":   "",
		"threadId": "thread-1",
		"turnId":   "turn-1",
	})
	s.handleAgentMessageDelta(params)

	evts := collectAppServerEvents(s, 1)
	if len(evts) != 1 || evts[0].Content != "delta text" {
		t.Fatalf("delta event = %v, want one 'delta text'", evts)
	}

	s.handleItemCompleted(map[string]any{
		"type": "agentMessage",
		"id":   "",
		"text": "full text",
	})
	s.flushPendingAsText()

	evts2 := collectAppServerEvents(s, 1)
	if len(evts2) != 1 {
		t.Fatalf("completed fallback: event count = %d, want 1", len(evts2))
	}
	if evts2[0].Content != "full text" {
		t.Errorf("fallback content = %q, want 'full text'", evts2[0].Content)
	}
}

// Test 7: turn 结束清理 sawDelta
func TestSawDelta_ClearedOnTurnBoundary(t *testing.T) {
	s := newTestAppServerSession(t)

	params, _ := json.Marshal(map[string]any{
		"delta":    "hello",
		"itemId":   "item-1",
		"threadId": "thread-1",
		"turnId":   "turn-1",
	})
	s.handleAgentMessageDelta(params)

	if _, ok := s.sawDelta.Load("item-1"); !ok {
		t.Fatal("sawDelta[item-1] should be set")
	}

	turnStartedParams, _ := json.Marshal(turnNotification{})
	s.handleNotification("turn/started", turnStartedParams)

	if _, ok := s.sawDelta.Load("item-1"); ok {
		t.Error("sawDelta should be cleared after turn/started")
	}

	s.handleAgentMessageDelta(params)

	turnCompletedParams, _ := json.Marshal(turnNotification{})
	s.handleNotification("turn/completed", turnCompletedParams)

	if _, ok := s.sawDelta.Load("item-1"); ok {
		t.Error("sawDelta should be cleared after turn/completed")
	}
}

// Test 8: 空 delta 文本 → 不 emit，不标记 sawDelta
func TestHandleDelta_EmptyDelta_NoEmitNoMark(t *testing.T) {
	s := newTestAppServerSession(t)

	params, _ := json.Marshal(map[string]any{
		"delta":    "",
		"itemId":   "item-1",
		"threadId": "thread-1",
		"turnId":   "turn-1",
	})
	s.handleAgentMessageDelta(params)

	evts := collectAppServerEvents(s, 1)
	if len(evts) != 0 {
		t.Fatalf("event count = %d, want 0 (empty delta)", len(evts))
	}
	if _, ok := s.sawDelta.Load("item-1"); ok {
		t.Error("sawDelta[item-1] should not be set for empty delta")
	}
}

// Test 9: 同一 itemId 多次 delta → 每个 emit，completed 跳过一次全文
func TestHandleDelta_MultipleDeltasSameItem_AllEmitted(t *testing.T) {
	s := newTestAppServerSession(t)

	for _, delta := range []string{"Hel", "lo", " world"} {
		params, _ := json.Marshal(map[string]any{
			"delta":    delta,
			"itemId":   "item-1",
			"threadId": "thread-1",
			"turnId":   "turn-1",
		})
		s.handleAgentMessageDelta(params)
	}

	evts := collectAppServerEvents(s, 3)
	if len(evts) != 3 {
		t.Fatalf("event count = %d, want 3", len(evts))
	}
	want := []string{"Hel", "lo", " world"}
	for i, w := range want {
		if evts[i].Content != w {
			t.Errorf("event[%d] = %q, want %q", i, evts[i].Content, w)
		}
	}

	s.handleItemCompleted(map[string]any{
		"type": "agentMessage",
		"id":   "item-1",
		"text": "Hello world",
	})

	evts2 := collectAppServerEvents(s, 1)
	if len(evts2) != 0 {
		t.Fatalf("after completed: event count = %d, want 0 (skipped)", len(evts2))
	}
}

// Test 10: whitespace-only delta → emit，标记 sawDelta
func TestHandleDelta_WhitespaceDelta_EmitsAndMarksSawDelta(t *testing.T) {
	s := newTestAppServerSession(t)

	for _, delta := range []string{"Hello", " ", "world", "\n"} {
		params, _ := json.Marshal(map[string]any{
			"delta":    delta,
			"itemId":   "item-1",
			"threadId": "thread-1",
			"turnId":   "turn-1",
		})
		s.handleAgentMessageDelta(params)
	}

	evts := collectAppServerEvents(s, 4)
	if len(evts) != 4 {
		t.Fatalf("event count = %d, want 4", len(evts))
	}
	want := []string{"Hello", " ", "world", "\n"}
	for i, w := range want {
		if evts[i].Content != w {
			t.Errorf("event[%d] = %q, want %q", i, evts[i].Content, w)
		}
	}
	if _, ok := s.sawDelta.Load("item-1"); !ok {
		t.Error("sawDelta[item-1] should be set after whitespace delta")
	}

	// completed 全文被跳过
	s.handleItemCompleted(map[string]any{
		"type": "agentMessage",
		"id":   "item-1",
		"text": "Hello world\n",
	})
	evts2 := collectAppServerEvents(s, 1)
	if len(evts2) != 0 {
		t.Fatalf("after completed: event count = %d, want 0 (skipped)", len(evts2))
	}
}

// Test 11: reasoning whitespace delta → emit
func TestHandleReasoningWhitespaceDelta_Emits(t *testing.T) {
	s := newTestAppServerSession(t)

	params, _ := json.Marshal(map[string]any{
		"delta":        " ",
		"itemId":       "item-r1",
		"threadId":     "thread-1",
		"turnId":       "turn-1",
		"contentIndex": 0,
	})
	s.handleReasoningTextDelta(params)

	evts := collectAppServerEvents(s, 1)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1", len(evts))
	}
	if evts[0].Type != core.EventThinking {
		t.Errorf("type = %q, want thinking", evts[0].Type)
	}
	if evts[0].Content != " " {
		t.Errorf("content = %q, want ' '", evts[0].Content)
	}
}
