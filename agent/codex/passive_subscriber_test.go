package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeAppServer 模拟 Codex app-server 的 JSON-RPC over WebSocket 协议。
type fakeAppServer struct {
	server *httptest.Server
	wsURL  string

	// onConnect 在 WebSocket upgrade 后调用。返回时关闭连接。
	// 默认行为：响应 initialize，然后等待客户端断开。
	onConnect func(conn *websocket.Conn)
}

func newFakeAppServer() *fakeAppServer {
	f := &fakeAppServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if f.onConnect != nil {
			f.onConnect(conn)
			return
		}

		// 默认：响应 initialize 成功，然后静默等待
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			method, _ := msg["method"].(string)
			switch method {
			case "initialize":
				conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result":  map[string]any{"protocolVersion": "0.1"},
				})
			case "initialized":
				// notification，不需要响应
			}
		}
	}))

	f.wsURL = "ws://" + f.server.Listener.Addr().String()
	return f
}

func (f *fakeAppServer) close() { f.server.Close() }

// ── helpers ──────────────────────────────────────────────────────────────────

func testAgent(url string) *Agent {
	return &Agent{
		backend:      "app_server",
		appServerURL: url,
	}
}

// waitForEvents 从 channel 收集事件直到超时或 channel 关闭。
func waitForEvents(ch <-chan core.Event, timeout time.Duration) []core.Event {
	var events []core.Event
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timer.C:
			return events
		}
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestPassiveSubscribe_DialFailure(t *testing.T) {
	agent := testAgent("ws://127.0.0.1:1") // nobody listening
	_, err := agent.Subscribe(context.Background())
	if err == nil {
		t.Fatal("expected dial error, got nil")
	}
}

func TestPassiveSubscribe_InitializeRejected(t *testing.T) {
	fake := newFakeAppServer()
	defer fake.close()

	fake.onConnect = func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg["method"] == "initialize" {
				conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"error":   map[string]any{"code": -32000, "message": "server busy"},
				})
				return
			}
		}
	}

	agent := testAgent(fake.wsURL)
	_, err := agent.Subscribe(context.Background())
	if err == nil {
		t.Fatal("expected initialize rejection error, got nil")
	}
}

func TestPassiveSubscribe_ServerClose_ClosesEventsChannel(t *testing.T) {
	fake := newFakeAppServer()
	defer fake.close()

	serverDone := make(chan struct{})
	fake.onConnect = func(conn *websocket.Conn) {
		defer close(serverDone)
		defer conn.Close()

		// 完成 initialize handshake
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			method, _ := msg["method"].(string)
			switch method {
			case "initialize":
				conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result":  map[string]any{"protocolVersion": "0.1"},
				})
			case "initialized":
				// 握手完成，立即关闭连接
				return
			}
		}
	}

	agent := testAgent(fake.wsURL)
	events, err := agent.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// 等待服务端关闭，events channel 应该随后关闭
	<-serverDone

	got := waitForEvents(events, 3*time.Second)
	// events channel 应该已经关闭
	if len(got) > 0 {
		t.Logf("received %d events before close (ok)", len(got))
	}

	// 再次读取确认 channel 已关闭
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("events channel still open after server closed connection")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for events channel to close")
	}
}

func TestPassiveSubscribe_NotificationsMappedToEvents(t *testing.T) {
	fake := newFakeAppServer()
	defer fake.close()

	fake.onConnect = func(conn *websocket.Conn) {
		defer conn.Close()

		// 完成 handshake
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			method, _ := msg["method"].(string)
			switch method {
			case "initialize":
				conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result":  map[string]any{"protocolVersion": "0.1"},
				})
			case "initialized":
				goto SEND_EVENTS
			}
		}

	SEND_EVENTS:
		// 发送 agentMessage 文本
		conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/completed",
			"params": map[string]any{
				"threadId": "thread-1",
				"item": map[string]any{
					"type": "agentMessage",
					"text": "hello world",
				},
			},
		})

		// 发送 turn/completed
		conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/completed",
			"params": map[string]any{
				"threadId": "thread-1",
				"turn":     map[string]any{"id": "turn-1", "status": "completed"},
			},
		})
	}

	agent := testAgent(fake.wsURL)
	events, err := agent.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	got := waitForEvents(events, 3*time.Second)

	// 至少应该收到 text event 和 result event
	var hasText, hasResult bool
	for _, ev := range got {
		if ev.Type == core.EventText && ev.Content == "hello world" && ev.SessionID == "thread-1" {
			hasText = true
		}
		if ev.Type == core.EventResult && ev.Done && ev.SessionID == "thread-1" {
			hasResult = true
		}
	}
	if !hasText {
		t.Error("missing EventText from agentMessage item/completed")
	}
	if !hasResult {
		t.Error("missing EventResult(Done:true) from turn/completed")
	}
}

func TestPassiveSubscribe_ReconnectAfterServerClose(t *testing.T) {
	// 验证：第一次 Subscribe 成功 → 服务端关闭 → events channel 关闭 → 再次 Subscribe 成功
	fake := newFakeAppServer()
	defer fake.close()

	// T11: closeCount 改 atomic.Int32，连接内 n := closeCount.Add(1) 取本次连接序号，
	// 后续只比较局部 n（不再跨 goroutine 读写裸 int）。WebSocket onConnect 由多个连接
	// goroutine 并发执行，裸 int 的 Write(:290)/Read(:311) 是实跑复现的 DATA RACE。
	var closeCount atomic.Int32
	fake.onConnect = func(conn *websocket.Conn) {
		defer conn.Close()
		n := closeCount.Add(1)

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			method, _ := msg["method"].(string)
			switch method {
			case "initialize":
				conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result":  map[string]any{"protocolVersion": "0.1"},
				})
			case "initialized":
				// 第一次连接：握手后立即关闭
				if n == 1 {
					return
				}
				// 第二次连接：保持打开，发一个事件
				conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "item/completed",
					"params": map[string]any{
						"threadId": "thread-reconnect",
						"item": map[string]any{
							"type": "agentMessage",
							"text": "reconnected",
						},
					},
				})
			}
		}
	}

	agent := testAgent(fake.wsURL)

	// 第一次订阅
	events1, err := agent.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("first Subscribe failed: %v", err)
	}

	// 等待 events1 关闭
	select {
	case _, ok := <-events1:
		if ok {
			t.Fatal("events1 should be closed after server disconnect")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for events1 to close")
	}

	// 第二次订阅（模拟 reconnection loop）
	events2, err := agent.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("second Subscribe failed: %v", err)
	}

	got := waitForEvents(events2, 3*time.Second)
	var hasReconnectEvent bool
	for _, ev := range got {
		if ev.Type == core.EventText && ev.Content == "reconnected" && ev.SessionID == "thread-reconnect" {
			hasReconnectEvent = true
		}
	}
	if !hasReconnectEvent {
		t.Error("missing EventText from reconnected session")
	}
}

func TestPassiveSubscribe_ContextCancel(t *testing.T) {
	fake := newFakeAppServer()
	defer fake.close()

	// 服务端会一直保持连接
	agent := testAgent(fake.wsURL)
	ctx, cancel := context.WithCancel(context.Background())

	events, err := agent.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// 取消 context
	cancel()

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("events should be closed after context cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for events to close after context cancel")
	}
}

// Thread A delta → Thread B turn/started → Thread A item/completed
// 不应发 fallback 全文（sawDelta 按 threadID:itemID 隔离）
func TestPassiveSubscribe_InterleavedTurns_SawDeltaPerThread(t *testing.T) {
	fake := newFakeAppServer()
	defer fake.close()

	fake.onConnect = func(conn *websocket.Conn) {
		defer conn.Close()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			method, _ := msg["method"].(string)
			switch method {
			case "initialize":
				conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result":  map[string]any{"protocolVersion": "0.1"},
				})
			case "initialized":
				goto SEND_EVENTS
			}
		}

	SEND_EVENTS:
		// Thread A: delta
		conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/agentMessage/delta",
			"params": map[string]any{
				"delta":    "Hi",
				"itemId":   "item-a",
				"threadId": "thread-a",
				"turnId":   "turn-a",
			},
		})

		// Thread B: turn/started（不应清掉 thread A 的 sawDelta）
		conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/started",
			"params": map[string]any{
				"threadId": "thread-b",
				"turn":     map[string]any{"id": "turn-b"},
			},
		})

		// Thread A: item/completed（应被 sawDelta 跳过，不发全文）
		conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/completed",
			"params": map[string]any{
				"threadId": "thread-a",
				"item": map[string]any{
					"type": "agentMessage",
					"id":   "item-a",
					"text": "Hi from thread A full text",
				},
			},
		})

		// Thread B: turn/completed
		conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/completed",
			"params": map[string]any{
				"threadId": "thread-b",
				"turn":     map[string]any{"id": "turn-b", "status": "completed"},
			},
		})

		// Thread A: turn/completed
		conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/completed",
			"params": map[string]any{
				"threadId": "thread-a",
				"turn":     map[string]any{"id": "turn-a", "status": "completed"},
			},
		})
	}

	agent := testAgent(fake.wsURL)
	events, err := agent.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	got := waitForEvents(events, 3*time.Second)

	// 不应出现 thread A 的 fallback 全文 "Hi from thread A full text"
	for _, ev := range got {
		if ev.Content == "Hi from thread A full text" {
			t.Error("thread A item/completed should be skipped (sawDelta), but full text was emitted")
		}
	}

	// 应出现 thread A 的 delta "Hi"
	var hasDeltaA, hasResultA, hasResultB bool
	for _, ev := range got {
		if ev.Type == core.EventText && ev.Content == "Hi" && ev.SessionID == "thread-a" {
			hasDeltaA = true
		}
		if ev.Type == core.EventResult && ev.Done && ev.SessionID == "thread-a" {
			hasResultA = true
		}
		if ev.Type == core.EventResult && ev.Done && ev.SessionID == "thread-b" {
			hasResultB = true
		}
	}
	if !hasDeltaA {
		t.Error("missing EventText delta 'Hi' from thread A")
	}
	if !hasResultA {
		t.Error("missing EventResult(Done:true) from thread A turn/completed")
	}
	if !hasResultB {
		t.Error("missing EventResult(Done:true) from thread B turn/completed")
	}
}
