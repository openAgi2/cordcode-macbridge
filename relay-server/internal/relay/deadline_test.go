package relay

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// halfOpenWSPair 建一对 loopback websocket 连接：返回 (clientWS, serverWS)。
// serverWS 交给测试构造 socketPeer；clientWS 由测试控制（不读/发 ping 等）。
func halfOpenWSPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgraded := make(chan *websocket.Conn, 1)
	block := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			close(block)
			return
		}
		upgraded <- ws
		<-block
	}))
	t.Cleanup(httpServer.Close)
	t.Cleanup(func() { close(block) })

	clientWS, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { clientWS.Close() })
	serverWS := <-upgraded
	return clientWS, serverWS
}

// TestSocketPeerWriteDeadlineFiresOnBlockedWrite 验证 PR-2 写 deadline：
// socketPeer.write 给半开 peer（对端不读）写大帧时，必须在 deadline 内返回错误，
// 而不是挂死数十秒卡死 writeMu（对称于 go-bridge 根因 A）。relayWriteDeadline 测试内覆盖成短值。
//
// 关联：docs/2026-06-17-bridge-hang-implementation-spec.md relay-server 落点（socketPeer.write）。
func TestSocketPeerWriteDeadlineFiresOnBlockedWrite(t *testing.T) {
	old := relayWriteDeadline
	relayWriteDeadline = 200 * time.Millisecond
	defer func() { relayWriteDeadline = old }()

	clientWS, serverWS := halfOpenWSPair(t)
	_ = clientWS // 故意不读 → socketPeer.write 阻塞
	peer := &socketPeer{conn: serverWS}

	// 4MB 远大于 loopback 发送缓冲，确保写阻塞在内核层。
	big := bytes.Repeat([]byte("x"), 4*1024*1024)
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- peer.write(big) }()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if elapsed < 100*time.Millisecond {
			t.Fatalf("write returned in %v — 未阻塞或 deadline 未生效（期望 ~%v）", elapsed, relayWriteDeadline)
		}
		if elapsed > 5*time.Second {
			t.Fatalf("write hung %v — deadline 未触发（期望 ~%v）", elapsed, relayWriteDeadline)
		}
		if err == nil {
			t.Fatalf("blocked write 返回 nil error — 期望 deadline 超时错误")
		}
		t.Logf("blocked write 在 %v 内返回错误（deadline=%v, err=%v）", elapsed, relayWriteDeadline, err)
	case <-time.After(10 * time.Second):
		t.Fatal("peer.write 挂死 >10s — 写 deadline 未生效")
	}
}

// TestSocketPeerReadDeadlineExitsOnNoData 验证 PR-2 读 deadline：
// 半开 peer（不发任何数据）时，读必须在 relayReadDeadline 内返回错误，
// 让 relay 主动判死半开连接（对称于 go-bridge 直连路径）。relayReadDeadline 测试内覆盖成短值。
func TestSocketPeerReadDeadlineExitsOnNoData(t *testing.T) {
	old := relayReadDeadline
	relayReadDeadline = 200 * time.Millisecond
	defer func() { relayReadDeadline = old }()

	clientWS, serverWS := halfOpenWSPair(t)
	_ = clientWS // 不发任何数据（半开）
	peer := &socketPeer{conn: serverWS}
	peer.setReadKeepalive()

	// 在测试 goroutine 捕获 deadline（避免子 goroutine 读包级 var 与 var 恢复 race）。
	deadline := relayReadDeadline
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_ = peer.conn.SetReadDeadline(time.Now().Add(deadline))
		_, _, err := peer.conn.ReadMessage()
		done <- err
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("期望读超时错误，得 nil")
		}
		if elapsed > 5*time.Second {
			t.Fatalf("read hung %v — deadline 未触发", elapsed)
		}
		t.Logf("半开读在 %v 内返回错误（deadline=%v, err=%v）", elapsed, deadline, err)
	case <-time.After(10 * time.Second):
		t.Fatal("ReadMessage 挂死 >10s — 读 deadline 未生效")
	}
}

// TestSocketPeerReadKeepaliveExtendsDeadlineOnPing 验证读 deadline 的保活正确性：
// 对端持续发 ping 时，ping handler 重置读 deadline，read 不应在 deadline 内超时返回
// （否则健康但数据空闲的连接会被误判死，造成重连抖动）。relayReadDeadline 覆盖成短值。
func TestSocketPeerReadKeepaliveExtendsDeadlineOnPing(t *testing.T) {
	old := relayReadDeadline
	relayReadDeadline = 150 * time.Millisecond
	defer func() { relayReadDeadline = old }()

	clientWS, serverWS := halfOpenWSPair(t)
	peer := &socketPeer{conn: serverWS}
	peer.setReadKeepalive()

	// client 每 40ms 发一次 ping（远小于 150ms deadline）→ ping handler 应持续重置读 deadline。
	stopPing := make(chan struct{})
	t.Cleanup(func() { close(stopPing) })
	go func() {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ticker.C:
				_ = clientWS.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
			}
		}
	}()

	deadline := relayReadDeadline
	readDone := make(chan error, 1)
	go func() {
		_ = peer.conn.SetReadDeadline(time.Now().Add(deadline))
		_, _, err := peer.conn.ReadMessage()
		readDone <- err
	}()

	// 若 ping 未重置 deadline，read 会在 ~150ms 超时返回。
	// 断言：在 5× deadline（750ms）内 read 不返回 → ping 持续重置 deadline 生效。
	select {
	case err := <-readDone:
		t.Fatalf("read 在保活期返回（ping 未重置 deadline）err=%v", err)
	case <-time.After(750 * time.Millisecond):
		t.Logf("read 在 5× deadline 后仍存活——ping handler 重置读 deadline 生效（健康连接不被误判死）")
	}
	// read goroutine 在 clientWS.Close()（t.Cleanup）后收到错误退出，readDone 有缓冲不泄漏。
}

