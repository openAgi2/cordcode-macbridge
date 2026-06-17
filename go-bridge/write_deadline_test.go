package gobridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSendJSONWriteDeadlineFiresOnBlockedWrite 验证根因 A 修复：持锁写有写 deadline。
// 对端不读 → 大帧写填满 OS 发送缓冲后阻塞 → 必须在 deadline 内返回错误，
// 而不是挂死数十秒直到 TCP RTO。bridgeWriteTimeout 在测试内覆盖成短值（200ms）。
//
// 关联：docs/2026-06-17-bridge-hang-implementation-spec.md P0-A + 改进项 3（可注入短超时）。
func TestSendJSONWriteDeadlineFiresOnBlockedWrite(t *testing.T) {
	old := bridgeWriteTimeout
	bridgeWriteTimeout = 200 * time.Millisecond
	defer func() { bridgeWriteTimeout = old }()

	// 自建升级 handler，拿到服务端 *websocket.Conn 包成 *Conn 直接调 SendJSON。
	upgraded := make(chan *websocket.Conn, 1)
	block := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			close(block)
			return
		}
		upgraded <- ws
		<-block // 维持 handler goroutine，避免连接被提前关闭
	}))
	defer httpServer.Close()
	defer close(block)

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientWS.Close()
	// 故意不从 clientWS 读取 → 服务端写阻塞。

	serverWS := <-upgraded
	conn := newConn(serverWS)

	// 4MB 远大于 loopback 发送缓冲，确保写阻塞在内核层。
	big := strings.Repeat("x", 4*1024*1024)
	done := make(chan struct{})
	start := time.Now()
	go func() {
		conn.SendJSON(map[string]string{"type": "big", "data": big})
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(start)
		// 阻塞写应等到 ~deadline(200ms) 才返回：必须明显慢于无阻塞写(<10ms)，
		// 且远快于无 deadline 时的 TCP RTO（数十秒）。
		if elapsed < 100*time.Millisecond {
			t.Fatalf("write returned in %v — 未阻塞或 deadline 未生效（期望 ~%v）", elapsed, bridgeWriteTimeout)
		}
		if elapsed > 5*time.Second {
			t.Fatalf("write hung %v — deadline 未触发（期望 ~%v）", elapsed, bridgeWriteTimeout)
		}
		t.Logf("blocked write 在 %v 内返回（deadline=%v）", elapsed, bridgeWriteTimeout)
	case <-time.After(10 * time.Second):
		t.Fatal("SendJSON 挂死 >10s — 写 deadline 未生效")
	}
}

// TestSendJSONClosesConnAfterRepeatedWriteErrors 验证 P0-A 配套关闭（死锁陷阱修复）：
// 连续 5 次写错误后置 closed=true 并调底层 c.conn.Close()（gorilla 方法，不经 c.mu），
// 读循环随之退出。绝不能在此处用 CloseWithControl/c.Close()（会重入 c.mu 死锁）。
// 用 race detector 跑以确认无数据竞争/死锁。
func TestSendJSONClosesConnAfterRepeatedWriteErrors(t *testing.T) {
	upgraded := make(chan *websocket.Conn, 1)
	block := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			close(block)
			return
		}
		upgraded <- ws
		<-block
	}))
	defer httpServer.Close()
	defer close(block)

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientWS.Close()

	serverWS := <-upgraded
	conn := newConn(serverWS)

	// 关闭服务端底层 raw conn，模拟半开（对端消失）：后续 WriteJSON 立即失败，
	// 可快速累积 5 次错误走关闭路径（deadline 机制由 TestSendJSONWriteDeadlineFiresOnBlockedWrite 覆盖）。
	_ = serverWS.NetConn().Close()
	time.Sleep(50 * time.Millisecond) // 让错误状态稳定

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			conn.SendJSON(map[string]string{"type": "x"})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SendJSON 关闭路径挂死 >5s — 死锁（可能误用 CloseWithControl/c.Close() 重入 c.mu）")
	}

	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed {
		t.Fatal("连续 5 次写错误后 conn.closed 应为 true")
	}
}
