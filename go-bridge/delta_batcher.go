package gobridge

import (
	"log/slog"
	"sync"
	"time"
)

// Fix 5 (plan §4 Fix 5): text_delta / reasoning_delta 时间窗攒批。
//
// 根因：agent/claudecode/session.go:emitTextDelta 每 token 立即写 channel → go-bridge
// 读一帧发一帧（handlers_relay.go relayEvents / main.go startPassiveSubscription →
// Broadcaster.Send → 每 conn SendJSON → relay_conn 每帧 HPKE）。1 token = 1 WS frame，
// 无任何服务于实时 text_delta 的攒批。
//
// 本批处理在 broadcaster.Send 之前按 (backendID, sessionID, directory) 维度累加缓冲
// text_delta/reasoning_delta，配 deltaBatchFlushInterval(33ms) ticker flush。窗口内多个
// delta 拼成一个 WS frame 的 `delta`（iOS 侧 text_delta 是 append 语义，拼接长 delta
// 字符串即可，无需改协议——见 plan §4 Fix 5「跨仓边界」）。
//
// 顺序与控制语义：
//   - 非 text_delta/reasoning_delta 事件（turn_started/turn_completed/error/todos_updated/
//     session_state_changed/tool_*/...）不进缓冲：触发该 key flush 后立即转发，保留
//     「text 在 control 之前」的严格顺序（iOS 状态机依赖 turn_completed 前已收到全部 text）。
//   - 同 key 内连续同类块在 flush 时合并（chunk.text 累加）。
//   - flush 用本批最后一个块的 seq（单调递增；中间 seq 被合并掉，iOS 不依赖连续 seq）。
//
// 背压：单个 key 累积超过 deltaBatchMaxPendingBytes 时立即 flush（避免 OOM）。
//
// 注：chunk 用 string（不可变）而非 strings.Builder——Builder 不能被复制，slice grow
// 会触发其 copyCheck panic。

const (
	deltaBatchFlushInterval   = 33 * time.Millisecond // < iOS 66ms 第一层，视觉无感
	deltaBatchMaxPendingBytes = 256 * 1024            // 单 key 缓冲上限，超限即 flush
)

// BroadcastSender 是 DeltaBatcher 的下游（*Broadcaster 满足；测试可注入 capture sender）。
type BroadcastSender interface {
	Send(BroadcastEvent)
}

type deltaChunk struct {
	eventType string // "text_delta" | "reasoning_delta"
	text      string
	seq       int
}

type deltaAccum struct {
	backendID  string
	sessionID  string
	directory  string
	chunks     []deltaChunk
	totalBytes int
}

// DeltaBatcher 把 text_delta/reasoning_delta 按 SubscriptionKey 维度攒批后转发给 sender。
type DeltaBatcher struct {
	sender BroadcastSender
	mu     sync.Mutex
	accums map[SubscriptionKey]*deltaAccum
	ticker *time.Ticker
	stop   chan struct{}
	done   chan struct{}
}

func NewDeltaBatcher(sender BroadcastSender) *DeltaBatcher {
	d := &DeltaBatcher{
		sender: sender,
		accums: make(map[SubscriptionKey]*deltaAccum),
		ticker: time.NewTicker(deltaBatchFlushInterval),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go d.flushLoop()
	return d
}

// Stop 停止 ticker 并 flush 残留缓冲（shutdown 路径调用，保证流式结束时残留 delta 不丢）。
func (d *DeltaBatcher) Stop() {
	select {
	case <-d.stop:
		return // 已 stop
	default:
		close(d.stop)
	}
	d.ticker.Stop()
	d.FlushAll()
	<-d.done // 等 flushLoop 退出，避免 goroutine 泄漏。
}

// Send: text_delta/reasoning_delta 攒批；其它事件先 flush 该 key 再立即转发（保留顺序）。
// 与直接调 broadcaster.Send 同签名，调用方只需替换发送入口。
func (d *DeltaBatcher) Send(ev BroadcastEvent) {
	if d.tryAccumulate(ev) {
		return
	}
	// 控制/非 text 事件：先 flush 该 key 的残留 text，再转发本事件，保证顺序。
	d.flushKey(ev.BackendID, ev.SessionID, ev.Directory)
	d.sender.Send(ev)
}

// FlushAll flush 所有 key 的缓冲（ticker 定期调用；测试可直接调）。
func (d *DeltaBatcher) FlushAll() {
	d.mu.Lock()
	pending := make([]*deltaAccum, 0, len(d.accums))
	for k, a := range d.accums {
		pending = append(pending, a)
		delete(d.accums, k)
	}
	d.mu.Unlock()
	for _, a := range pending {
		d.emit(a)
	}
}

func (d *DeltaBatcher) tryAccumulate(ev BroadcastEvent) bool {
	msg, ok := ev.Message.(EventMessage)
	if !ok {
		return false
	}
	if msg.Event != "text_delta" && msg.Event != "reasoning_delta" {
		return false
	}
	deltaStr := extractDeltaText(msg.Data)
	if deltaStr == "" {
		// 无内容：直接转发原事件（保持 idle/heartbeat 类空 delta 的既有行为，不静默丢）。
		return false
	}
	key := SubscriptionKey{BackendID: ev.BackendID, SessionID: ev.SessionID, Directory: ev.Directory}
	overflow := false
	func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		a, exists := d.accums[key]
		if !exists {
			a = &deltaAccum{backendID: ev.BackendID, sessionID: ev.SessionID, directory: ev.Directory}
			d.accums[key] = a
		}
		if n := len(a.chunks); n > 0 && a.chunks[n-1].eventType == msg.Event {
			a.chunks[n-1].text += deltaStr
			a.chunks[n-1].seq = msg.Seq
		} else {
			a.chunks = append(a.chunks, deltaChunk{eventType: msg.Event, text: deltaStr, seq: msg.Seq})
		}
		a.totalBytes += len(deltaStr)
		overflow = a.totalBytes >= deltaBatchMaxPendingBytes
	}()

	if overflow {
		// 背压：单 key 缓冲过大，立即 flush（在锁外发，避免与 sender 死锁）。
		d.flushKey(ev.BackendID, ev.SessionID, ev.Directory)
	}
	return true
}

func (d *DeltaBatcher) flushKey(backendID, sessionID, directory string) {
	key := SubscriptionKey{BackendID: backendID, SessionID: sessionID, Directory: directory}
	d.mu.Lock()
	a, ok := d.accums[key]
	if ok {
		delete(d.accums, key)
	}
	d.mu.Unlock()
	if !ok {
		return
	}
	d.emit(a)
}

func (d *DeltaBatcher) flushLoop() {
	defer close(d.done)
	for {
		select {
		case <-d.stop:
			return
		case <-d.ticker.C:
			d.FlushAll()
		}
	}
}

// emit 把一个 key 的累积块发出去（连续同类已累积进同一 chunk.text，保留 text/reasoning 顺序）。
func (d *DeltaBatcher) emit(a *deltaAccum) {
	if a == nil || len(a.chunks) == 0 {
		return
	}
	for _, c := range a.chunks {
		if c.text == "" {
			continue
		}
		msg := EventMessage{
			Type:      "event",
			SessionID: a.sessionID,
			BackendID: a.backendID,
			Event:     c.eventType,
			Data:      map[string]interface{}{"delta": c.text},
			Seq:       c.seq,
		}
		d.sender.Send(BroadcastEvent{
			BackendID: a.backendID,
			SessionID: a.sessionID,
			Directory: a.directory,
			Message:   msg,
		})
	}
	slog.Debug("go-bridge: delta batch emitted",
		"backend", a.backendID, "session", a.sessionID, "dir", a.directory, "chunks", len(a.chunks))
}

// extractDeltaText 从 EventMessage.Data 取 "delta" 字符串（mapAgentEvent 的 text_delta/reasoning_delta 形状）。
func extractDeltaText(data interface{}) string {
	m, ok := data.(map[string]interface{})
	if !ok {
		return ""
	}
	if s, ok := m["delta"].(string); ok {
		return s
	}
	return ""
}
