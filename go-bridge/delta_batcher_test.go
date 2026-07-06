package gobridge

import (
	"sync"
	"testing"
	"time"
)

// captureSender 记录所有 Send 调用，供 DeltaBatcher 测试断言帧数与顺序。
type captureSender struct {
	mu      sync.Mutex
	events  []EventMessage
	full    []BroadcastEvent // 完整 BroadcastEvent（含 backend/session/dir）
}

func (c *captureSender) Send(ev BroadcastEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg, ok := ev.Message.(EventMessage); ok {
		c.events = append(c.events, msg)
	}
	c.full = append(c.full, ev)
}

func (c *captureSender) snapshot() ([]EventMessage, []BroadcastEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	evs := make([]EventMessage, len(c.events))
	copy(evs, c.events)
	full := make([]BroadcastEvent, len(c.full))
	copy(full, c.full)
	return evs, full
}

func textDeltaEvent(backend, session, delta string, seq int) BroadcastEvent {
	return BroadcastEvent{
		BackendID: backend,
		SessionID: session,
		Message: EventMessage{
			Type:      "event",
			SessionID: session,
			BackendID: backend,
			Event:     "text_delta",
			Data:      map[string]interface{}{"delta": delta},
			Seq:       seq,
		},
	}
}

func reasoningDeltaEvent(backend, session, delta string, seq int) BroadcastEvent {
	return BroadcastEvent{
		BackendID: backend,
		SessionID: session,
		Message: EventMessage{
			Type:      "event",
			SessionID: session,
			BackendID: backend,
			Event:     "reasoning_delta",
			Data:      map[string]interface{}{"delta": delta},
			Seq:       seq,
		},
	}
}

func nonTextEvent(backend, session, event string, seq int) BroadcastEvent {
	return BroadcastEvent{
		BackendID: backend,
		SessionID: session,
		Message: EventMessage{
			Type:      "event",
			SessionID: session,
			BackendID: backend,
			Event:     event,
			Data:      map[string]interface{}{"foo": "bar"},
			Seq:       seq,
		},
	}
}

// TestDeltaBatchMergesConsecutiveTextDeltas: N 个 text_delta 在 <33ms 窗口内
// 被合并成 1 帧（M<N），且拼接顺序保留。
func TestDeltaBatchMergesConsecutiveTextDeltas(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	for i, chunk := range []string{"Hello", ", ", "world", "!"} {
		d.Send(textDeltaEvent("claude", "s1", chunk, i+1))
	}
	d.FlushAll()

	evs, _ := capture.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected 1 merged text_delta frame, got %d", len(evs))
	}
	if evs[0].Event != "text_delta" {
		t.Fatalf("merged event type = %q, want text_delta", evs[0].Event)
	}
	delta, _ := evs[0].Data.(map[string]interface{})["delta"].(string)
	if delta != "Hello, world!" {
		t.Fatalf("merged delta = %q, want %q", delta, "Hello, world!")
	}
	// seq 用最后一个（单调递增）。
	if evs[0].Seq != 4 {
		t.Fatalf("merged seq = %d, want 4 (last)", evs[0].Seq)
	}
}

// TestDeltaBatchFlushesOnControlEvent: text_delta 累积后，turn_completed 触发
// 该 key flush → text 在 turn_completed 之前到达（顺序严格保留）。
func TestDeltaBatchFlushesOnControlEvent(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	d.Send(textDeltaEvent("claude", "s1", "abc", 1))
	d.Send(textDeltaEvent("claude", "s1", "def", 2))
	d.Send(nonTextEvent("claude", "s1", "turn_completed", 3))

	evs, _ := capture.snapshot()
	if len(evs) != 2 {
		t.Fatalf("expected 2 frames (merged text + turn_completed), got %d", len(evs))
	}
	if evs[0].Event != "text_delta" {
		t.Fatalf("first frame = %q, want text_delta (must precede turn_completed)", evs[0].Event)
	}
	if evs[1].Event != "turn_completed" {
		t.Fatalf("second frame = %q, want turn_completed", evs[1].Event)
	}
	delta, _ := evs[0].Data.(map[string]interface{})["delta"].(string)
	if delta != "abcdef" {
		t.Fatalf("merged delta = %q, want abcdef", delta)
	}
}

// TestDeltaBatchPreservesTextReasoningOrder: text 与 reasoning 混合时，按到达顺序
// 各自成帧（不串台为单一类型），保留相对顺序。
func TestDeltaBatchPreservesTextReasoningOrder(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	d.Send(reasoningDeltaEvent("claude", "s1", "think ", 1))
	d.Send(reasoningDeltaEvent("claude", "s1", "more ", 2))
	d.Send(textDeltaEvent("claude", "s1", "answer", 3))
	d.FlushAll()

	evs, _ := capture.snapshot()
	if len(evs) != 2 {
		t.Fatalf("expected 2 frames (reasoning run + text), got %d", len(evs))
	}
	if evs[0].Event != "reasoning_delta" {
		t.Fatalf("first = %q, want reasoning_delta", evs[0].Event)
	}
	if evs[1].Event != "text_delta" {
		t.Fatalf("second = %q, want text_delta", evs[1].Event)
	}
	rd, _ := evs[0].Data.(map[string]interface{})["delta"].(string)
	if rd != "think more " {
		t.Fatalf("reasoning merged = %q", rd)
	}
	td, _ := evs[1].Data.(map[string]interface{})["delta"].(string)
	if td != "answer" {
		t.Fatalf("text = %q", td)
	}
}

// TestDeltaBatchDoesNotBatchNonTextEvents: turn_started/tool_started 等立即透传，
// 不进缓冲。
func TestDeltaBatchDoesNotBatchNonTextEvents(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	d.Send(nonTextEvent("claude", "s1", "turn_started", 1))
	d.Send(nonTextEvent("claude", "s1", "tool_started", 2))
	d.Send(nonTextEvent("claude", "s1", "todos_updated", 3))

	evs, _ := capture.snapshot()
	// 控制事件不进缓冲，立即转发（无 FlushAll 也已全到）。
	if len(evs) != 3 {
		t.Fatalf("expected 3 non-text events passed through immediately, got %d", len(evs))
	}
}

// TestDeltaBatchIsolatesPerSession: 不同 session 的 delta 各自独立累积，
// 不跨 session 合并。
func TestDeltaBatchIsolatesPerSession(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	d.Send(textDeltaEvent("claude", "s1", "A", 1))
	d.Send(textDeltaEvent("claude", "s2", "B", 1))
	d.Send(textDeltaEvent("claude", "s1", "C", 2))
	d.Send(textDeltaEvent("claude", "s2", "D", 2))
	d.FlushAll()

	evs, _ := capture.snapshot()
	if len(evs) != 2 {
		t.Fatalf("expected 2 merged frames (one per session), got %d", len(evs))
	}
	// 按 session 收集 delta
	bySession := map[string]string{}
	for _, ev := range evs {
		delta, _ := ev.Data.(map[string]interface{})["delta"].(string)
		bySession[ev.SessionID] += delta
	}
	if bySession["s1"] != "AC" || bySession["s2"] != "BD" {
		t.Fatalf("per-session isolation failed: %+v", bySession)
	}
}

// TestDeltaBatchTickerFlushesWithinWindow: 不手动 FlushAll 时，ticker 在
// ~33ms 内自动 flush。
func TestDeltaBatchTickerFlushesWithinWindow(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	d.Send(textDeltaEvent("claude", "s1", "tick", 1))

	// 等待 2 个 ticker 周期（~66ms）确保 flush 触发。
	time.Sleep(2 * deltaBatchFlushInterval + 20*time.Millisecond)

	evs, _ := capture.snapshot()
	if len(evs) != 1 {
		t.Fatalf("ticker should have flushed 1 frame within window, got %d", len(evs))
	}
}

// TestDeltaBatchStopFlushesResidual: Stop() 必须把残留缓冲 flush 出去，
// 不丢流式末尾的 token。
func TestDeltaBatchStopFlushesResidual(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)

	d.Send(textDeltaEvent("claude", "s1", "tail", 1))
	d.Stop() // 必须先 flush 再退

	evs, _ := capture.snapshot()
	if len(evs) != 1 {
		t.Fatalf("Stop should have flushed 1 residual frame, got %d", len(evs))
	}
	delta, _ := evs[0].Data.(map[string]interface{})["delta"].(string)
	if delta != "tail" {
		t.Fatalf("residual delta = %q, want tail", delta)
	}
}

// TestDeltaBatchOverflowFlushesImmediately: 单 key 缓冲超上限时立即 flush（背压，防 OOM）。
func TestDeltaBatchOverflowFlushesImmediately(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	big := make([]byte, deltaBatchMaxPendingBytes/2+1)
	for i := range big {
		big[i] = 'x'
	}
	// 两个半-上限块 → 第二个触发 overflow flush。
	d.Send(textDeltaEvent("claude", "s1", string(big), 1))
	d.Send(textDeltaEvent("claude", "s1", string(big), 2))

	evs, _ := capture.snapshot()
	if len(evs) < 1 {
		t.Fatalf("overflow should have flushed at least 1 frame immediately, got %d", len(evs))
	}
}
