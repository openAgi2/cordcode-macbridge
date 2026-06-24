package claudecode

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func collectEvents(cs *claudeSession, n int) []core.Event {
	var evts []core.Event
	for i := 0; i < n; i++ {
		select {
		case evt, ok := <-cs.events:
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

func newTestClaudeSession(t *testing.T) *claudeSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cs := &claudeSession{
		events: make(chan core.Event, 64),
		ctx:    ctx,
	}
	cs.alive.Store(true)
	return cs
}

// Test 1: 同一 message.id 文本增长 → 增量 diff
func TestHandleAssistant_SameMsgIDTextGrowing_EmitsDelta(t *testing.T) {
	cs := newTestClaudeSession(t)

	// 第一次：text="Hel"
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "Hel"}},
		},
	})
	// 第二次：text="Hello"
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "Hello"}},
		},
	})

	evts := collectEvents(cs, 2)
	if len(evts) != 2 {
		t.Fatalf("event count = %d, want 2", len(evts))
	}
	if evts[0].Content != "Hel" {
		t.Errorf("first event = %q, want %q", evts[0].Content, "Hel")
	}
	if evts[1].Content != "lo" {
		t.Errorf("second event (delta) = %q, want %q", evts[1].Content, "lo")
	}
}

// Test 2: 同一 message.id 文本未变 → 跳过
func TestHandleAssistant_SameMsgIDTextUnchanged_Skips(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "Hello"}},
		},
	})
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "Hello"}},
		},
	})

	evts := collectEvents(cs, 2)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1 (second skipped)", len(evts))
	}
	if evts[0].Content != "Hello" {
		t.Errorf("event = %q, want %q", evts[0].Content, "Hello")
	}
}

// Test 3: message.id 变化 → 全文发送
func TestHandleAssistant_MsgIDChange_EmitsFullText(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "abc"}},
		},
	})
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-2",
			"content": []any{map[string]any{"type": "text", "text": "abc done"}},
		},
	})

	evts := collectEvents(cs, 2)
	if len(evts) != 2 {
		t.Fatalf("event count = %d, want 2", len(evts))
	}
	if evts[0].Content != "abc" {
		t.Errorf("first = %q, want %q", evts[0].Content, "abc")
	}
	if evts[1].Content != "abc done" {
		t.Errorf("second (new id, full text) = %q, want %q", evts[1].Content, "abc done")
	}
}

// Test 4: 缺失 message.id → 全文发送，不更新 emittedText
func TestHandleAssistant_NoMsgID_EmitsFullText(t *testing.T) {
	cs := newTestClaudeSession(t)

	// 先设一个 emittedText
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "abc"}},
		},
	})
	// 无 id → 全文
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "abc done"}},
		},
	})

	evts := collectEvents(cs, 2)
	if len(evts) != 2 {
		t.Fatalf("event count = %d, want 2", len(evts))
	}
	if evts[1].Content != "abc done" {
		t.Errorf("no-id event = %q, want %q", evts[1].Content, "abc done")
	}
}

// Test 5: 混合 content 顺序 → thinking/text/tool_use 保持原始顺序
func TestHandleAssistant_MixedContentOrder_PreservesOrder(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id": "assistant-1",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "先想想"},
				map[string]any{"type": "text", "text": "结果如下"},
				map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "/tmp/a.go"}},
			},
		},
	})

	evts := collectEvents(cs, 3)
	if len(evts) != 3 {
		t.Fatalf("event count = %d, want 3", len(evts))
	}
	if evts[0].Type != core.EventThinking {
		t.Errorf("event[0].Type = %q, want thinking", evts[0].Type)
	}
	if evts[1].Type != core.EventText {
		t.Errorf("event[1].Type = %q, want text", evts[1].Type)
	}
	if evts[2].Type != core.EventToolUse {
		t.Errorf("event[2].Type = %q, want tool_use", evts[2].Type)
	}
}

// Test 6: 非前缀替换 → 全文发送
func TestHandleAssistant_NonPrefixReplace_EmitsFullText(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "abc"}},
		},
	})
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "xyz"}},
		},
	})

	evts := collectEvents(cs, 2)
	if len(evts) != 2 {
		t.Fatalf("event count = %d, want 2", len(evts))
	}
	if evts[0].Content != "abc" {
		t.Errorf("first = %q, want %q", evts[0].Content, "abc")
	}
	if evts[1].Content != "xyz" {
		t.Errorf("second (non-prefix replace) = %q, want %q", evts[1].Content, "xyz")
	}
}

// Test 7: --resume 历史重放 → 不 emit assistant events，第一个 result 后退出 drain
func TestHandleAssistant_HistoryDraining_SuppressesEmit(t *testing.T) {
	cs := newTestClaudeSession(t)
	cs.historyDraining.Store(true)

	// drain 期间的 assistant 事件只更新状态，不 emit
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "历史文本"}},
		},
	})

	evts := collectEvents(cs, 1)
	if len(evts) != 0 {
		t.Fatalf("drain period: event count = %d, want 0", len(evts))
	}

	// diff 状态应已更新
	emitted, _ := cs.emittedText.Load().(string)
	if emitted != "历史文本" {
		t.Errorf("emittedText = %q, want %q (state updated even during drain)", emitted, "历史文本")
	}

	// result 退出 drain
	cs.handleResult(map[string]any{
		"type":       "result",
		"result":     "done",
		"session_id": "test-session",
	})

	if cs.historyDraining.Load() {
		t.Error("historyDraining should be false after first result")
	}

	// drain 退出后正常 emit
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-2",
			"content": []any{map[string]any{"type": "text", "text": "live text"}},
		},
	})

	evts = collectEvents(cs, 2)
	// 只有 live text，没有 drain 期间的事件和 result（result 被 drain 吞掉）
	var textEvts []core.Event
	for _, e := range evts {
		if e.Type == core.EventText && e.Content != "" {
			textEvts = append(textEvts, e)
		}
	}
	if len(textEvts) != 1 || textEvts[0].Content != "live text" {
		t.Errorf("after drain: text events = %v, want one 'live text'", textEvts)
	}
}

// Test 8: 空 session resume 超时兜底退出 drain
func TestHandleAssistant_HistoryDrainingTimeout(t *testing.T) {
	cs := newTestClaudeSession(t)
	cs.historyDraining.Store(true)

	// 模拟超时：手动关闭
	cs.historyDraining.Store(false)

	// 关闭后应正常 emit
	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "after timeout"}},
		},
	})

	evts := collectEvents(cs, 1)
	if len(evts) != 1 {
		t.Fatalf("event count = %d, want 1", len(evts))
	}
	if evts[0].Content != "after timeout" {
		t.Errorf("event = %q, want %q", evts[0].Content, "after timeout")
	}
}

// Test 9: 空 text 或缺失 text 字段 → 不发空 EventText
func TestHandleAssistant_EmptyText_NoEmit(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id": "assistant-1",
			"content": []any{
				map[string]any{"type": "text", "text": ""},
				map[string]any{"type": "text"},
			},
		},
	})

	evts := collectEvents(cs, 1)
	if len(evts) != 0 {
		t.Fatalf("event count = %d, want 0 (empty/missing text)", len(evts))
	}

	// emittedText 不应为空
	emitted, _ := cs.emittedText.Load().(string)
	if emitted != "" {
		t.Errorf("emittedText = %q, want empty (no valid text)", emitted)
	}
}

// Test 10: 串行调用 diff 状态无需锁（验证串行正确性）
func TestHandleAssistant_SerialCalls_CorrectDiffState(t *testing.T) {
	cs := newTestClaudeSession(t)

	// 模拟完整流式序列
	events := []map[string]any{
		{"message": map[string]any{"id": "a1", "content": []any{map[string]any{"type": "text", "text": "H"}}}},
		{"message": map[string]any{"id": "a1", "content": []any{map[string]any{"type": "text", "text": "He"}}}},
		{"message": map[string]any{"id": "a1", "content": []any{map[string]any{"type": "text", "text": "Hello"}}}},
		{"message": map[string]any{"id": "a1", "content": []any{map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "/tmp/x"}}}}},
		{"message": map[string]any{"id": "a2", "content": []any{map[string]any{"type": "text", "text": "Done."}}}},
	}

	for _, raw := range events {
		cs.handleAssistant(raw)
	}

	evts := collectEvents(cs, 4)
	if len(evts) != 4 {
		t.Fatalf("event count = %d, want 4", len(evts))
	}

	want := []struct {
		typ  core.EventType
		text string
	}{
		{core.EventText, "H"},
		{core.EventText, "e"},
		{core.EventText, "llo"},
		{core.EventToolUse, ""},
	}
	for i, w := range want {
		if evts[i].Type != w.typ {
			t.Errorf("event[%d].Type = %q, want %q", i, evts[i].Type, w.typ)
		}
		if w.text != "" && evts[i].Content != w.text {
			t.Errorf("event[%d].Content = %q, want %q", i, evts[i].Content, w.text)
		}
	}

	// a2 的 "Done." 是新 id → 全文
	if evts[3].Type == core.EventText && evts[3].Content == "Done." {
		// This would be evts[3] if tool_use was first
	}
	// Actually: H, e, llo, tool_use → 4 events
	// "Done." is the 5th
	evts2 := collectEvents(cs, 1)
	if len(evts2) != 1 {
		t.Fatalf("a2 event count = %d, want 1", len(evts2))
	}
	if evts2[0].Content != "Done." {
		t.Errorf("a2 = %q, want %q", evts2[0].Content, "Done.")
	}
}

// Test: handleResult 在非 drain 模式下重置 diff 状态
func TestHandleResult_ResetsDiffState(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{
		"message": map[string]any{
			"id":      "assistant-1",
			"content": []any{map[string]any{"type": "text", "text": "some text"}},
		},
	})

	// 确认状态已更新
	activeID, _ := cs.activeMsgID.Load().(string)
	if activeID != "assistant-1" {
		t.Errorf("activeMsgID = %q, want assistant-1", activeID)
	}

	cs.handleResult(map[string]any{
		"type":       "result",
		"result":     "done",
		"session_id": "test-session",
	})

	activeID, _ = cs.activeMsgID.Load().(string)
	emitted, _ := cs.emittedText.Load().(string)
	if activeID != "" {
		t.Errorf("activeMsgID after result = %q, want empty", activeID)
	}
	if emitted != "" {
		t.Errorf("emittedText after result = %q, want empty", emitted)
	}
}

// Test: handleSystem 重置 diff 状态
func TestHandleSystem_ResetsDiffState(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.activeMsgID.Store("old-id")
	cs.emittedText.Store("old text")

	cs.handleSystem(map[string]any{
		"type":       "system",
		"session_id": "new-session",
	})

	activeID, _ := cs.activeMsgID.Load().(string)
	emitted, _ := cs.emittedText.Load().(string)
	if activeID != "" {
		t.Errorf("activeMsgID after system = %q, want empty", activeID)
	}
	if emitted != "" {
		t.Errorf("emittedText after system = %q, want empty", emitted)
	}
}
