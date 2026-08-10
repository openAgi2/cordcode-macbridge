package gobridge

// handlers_catalog_echo_test.go 锁定 Phase 7 §446 的可观测性前置不变量：客户端在 hello
// 声明 catalog_cursor_epoch_v2 时，服务端（direct LAN 路径）必须把该 capability 原样 echo 回
// hello_ack（ack.Capabilities["catalog_cursor_epoch_v2"] == true）。
//
// 这一层是 server.go handleHello 的 wiring 测试（client declares → server echoes），介于：
//   - catalog_wire_snapshot_test.go：EventPublisher SetConn/Conn 往返（unit）+ helloSupports… 检测；
//   - handlers_{codex,grok,opencode}_catalog_test.go：DECLARED 连接的下游 v2 行为。
// 之前 server.go:598-601 的「声明 → echo」wiring 没有直接测试覆盖；Phase 7 的声明率观测依赖它，
// 故补齐。relay 路径（main.go）结构与 direct 对称，按 session_sync_v2_test.go 的覆盖惯例只测 direct。
//
// 同时验证「未声明不 echo」（legacy client 零行为变化），与 TestSessionSyncV2CapabilityOmittedWithoutOptIn
// 同形。

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestCatalogCursorEpochV2CapabilityEchoedWhenDeclared：client 在 hello 声明
// catalog_cursor_epoch_v2 → hello_ack 必须 echo 该 capability。catalog 是纯 client-capability
// 门控（无 server-side toggle，见 projection_delivery.go SetConnCatalogCursorEpochV2 注释），
// 故无需 SetSessionSyncV2Enabled 之类的服务端开关。
func TestCatalogCursorEpochV2CapabilityEchoedWhenDeclared(t *testing.T) {
	const epoch = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := HelloMessage{
		Type: "hello", Client: HelloClient{DeviceID: "device"},
		Protocol:     HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
		Capabilities: []string{"catalog_cursor_epoch_v2"},
	}
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack HelloAckMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if !ack.Ok {
		t.Fatalf("hello_ack not ok: %+v", ack)
	}
	if !ack.Capabilities["catalog_cursor_epoch_v2"] {
		t.Fatalf("hello_ack did not echo catalog_cursor_epoch_v2; capabilities=%+v", ack.Capabilities)
	}
}

// TestCatalogCursorEpochV2CapabilityOmittedWhenNotDeclared：legacy client（未声明）的 hello_ack
// 不得包含 catalog_cursor_epoch_v2，行为零变化（§10 发布顺序：未声明连接走 v1 主线）。
func TestCatalogCursorEpochV2CapabilityOmittedWhenNotDeclared(t *testing.T) {
	const epoch = "aaaaaaaa-bbbb-4ccc-8ddd-ffffffffffff"
	handlers := NewHandlers()
	server := NewServerWithEpoch(handlers, epoch)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := HelloMessage{
		Type: "hello", Client: HelloClient{DeviceID: "device"},
		Protocol:     HelloProtocol{Name: BridgeProtocolName, Version: BridgeProtocolVersion},
		Capabilities: []string{"recovery_v1", "read_file_v2"},
	}
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack HelloAckMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if _, ok := ack.Capabilities["catalog_cursor_epoch_v2"]; ok {
		t.Fatalf("legacy hello_ack unexpectedly echoed catalog_cursor_epoch_v2: %+v", ack.Capabilities)
	}
}
