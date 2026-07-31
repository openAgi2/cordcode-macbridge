package gobridge

import (
	"sync"
	"testing"
	"time"
)

// captureSender 记录所有 Send 调用，供 DeltaBatcher 测试断言帧数与顺序。
type captureSender struct {
	mu     sync.Mutex
	events []LogicalEvent
}

func (c *captureSender) PublishLogical(ev LogicalEvent) EventMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
	return EventMessage{Event: ev.Event, BackendID: ev.BackendID, SessionID: ev.SessionID, Data: ev.Data}
}

func (c *captureSender) snapshot() ([]LogicalEvent, []LogicalEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	evs := make([]LogicalEvent, len(c.events))
	copy(evs, c.events)
	return evs, evs
}

func textDeltaEvent(backend, session, delta string, _ int) LogicalEvent {
	return LogicalEvent{
		BackendID: backend,
		SessionID: session,
		Event:     "text_delta",
		Data:      map[string]interface{}{"delta": delta},
	}
}

func reasoningDeltaEvent(backend, session, delta string, _ int) LogicalEvent {
	return LogicalEvent{
		BackendID: backend,
		SessionID: session,
		Event:     "reasoning_delta",
		Data:      map[string]interface{}{"delta": delta},
	}
}

func nonTextEvent(backend, session, event string, _ int) LogicalEvent {
	return LogicalEvent{
		BackendID: backend,
		SessionID: session,
		Event:     event,
		Data:      map[string]interface{}{"foo": "bar"},
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
	// DeltaBatcher operates before stamping; EventPublisher assigns one new seq
	// to the merged logical event.
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
	time.Sleep(2*deltaBatchFlushInterval + 20*time.Millisecond)

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


// TestDeltaBatchPreservesItemID: projection SoT attributes text_delta by itemId.
// Batching must keep itemId on the merged frame; otherwise OpenCode/Claude live
// content is skipped by ProjectionReducer and only turn_completed advances headRev.
func TestDeltaBatchPreservesItemID(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	d.Send(LogicalEvent{
		BackendID: "opencode",
		SessionID: "ses_1",
		Event:     "text_delta",
		Data:      map[string]interface{}{"delta": "Hel", "itemId": "msg_user_1"},
		Broadcast: true,
	})
	d.Send(LogicalEvent{
		BackendID: "opencode",
		SessionID: "ses_1",
		Event:     "text_delta",
		Data:      map[string]interface{}{"delta": "lo", "itemId": "msg_user_1"},
		Broadcast: true,
	})
	d.FlushAll()

	evs, _ := capture.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected 1 merged frame, got %d", len(evs))
	}
	data, _ := evs[0].Data.(map[string]interface{})
	if data["delta"] != "Hello" {
		t.Fatalf("delta = %#v, want Hello", data["delta"])
	}
	if data["itemId"] != "msg_user_1" {
		t.Fatalf("itemId = %#v, want msg_user_1 (must survive batching)", data["itemId"])
	}
}

// TestDeltaBatchDoesNotMergeAcrossItemIDs: different owning turns must not be
// concatenated into one attribution-less or wrong-id frame.
func TestDeltaBatchDoesNotMergeAcrossItemIDs(t *testing.T) {
	capture := &captureSender{}
	d := NewDeltaBatcher(capture)
	defer d.Stop()

	d.Send(LogicalEvent{
		BackendID: "opencode",
		SessionID: "ses_1",
		Event:     "text_delta",
		Data:      map[string]interface{}{"delta": "A", "itemId": "t1"},
	})
	d.Send(LogicalEvent{
		BackendID: "opencode",
		SessionID: "ses_1",
		Event:     "text_delta",
		Data:      map[string]interface{}{"delta": "B", "itemId": "t2"},
	})
	d.FlushAll()

	evs, _ := capture.snapshot()
	if len(evs) != 2 {
		t.Fatalf("expected 2 frames for distinct itemIds, got %d", len(evs))
	}
	d0, _ := evs[0].Data.(map[string]interface{})
	d1, _ := evs[1].Data.(map[string]interface{})
	if d0["itemId"] != "t1" || d0["delta"] != "A" {
		t.Fatalf("first = %#v", d0)
	}
	if d1["itemId"] != "t2" || d1["delta"] != "B" {
		t.Fatalf("second = %#v", d1)
	}
}
