package gobridge

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestRelayBridgeClientBasic 验证 relay bridge client 的基本生命周期。
func TestRelayBridgeClientBasic(t *testing.T) {
	hub := NewRelayHub()
	routeID, bridgeAuth, _ := hub.RegisterRoute()
	bridgeIdentityDir := t.TempDir()
	identity, _ := LoadOrCreateRelayCryptoIdentity(bridgeIdentityDir)
	handlers := NewHandlers()

	client := NewRelayBridgeClient(handlers, hub, identity, "brg-test", routeID, bridgeAuth)

	if client.Connected() {
		t.Error("should not be connected initially")
	}
	if client.ActiveDeviceCount() != 0 {
		t.Error("should start with 0 active devices")
	}

	client.Close()

	if client.Connected() {
		t.Error("should not be connected after close")
	}
}

func TestRelayBridgeClientConnectAuthenticatesBridgeSocket(t *testing.T) {
	authSeen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen <- r.Header.Get("Authorization")
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_, _, _ = ws.ReadMessage()
	}))
	defer server.Close()

	client := NewRelayBridgeClient(NewHandlers(), nil, nil, "brg-test", "route-test", "bridge-secret")
	if err := client.Connect("ws" + strings.TrimPrefix(server.URL, "http")); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case header := <-authSeen:
		if header != "Bearer bridge-secret" {
			t.Fatalf("Authorization = %q", header)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge socket connection not observed")
	}
}

func TestRelayBridgeClientProcessesClientHello(t *testing.T) {
	// 设置基础设施
	hub := NewRelayHub()
	routeID, bridgeAuth, _ := hub.RegisterRoute()
	deviceID := "dev-hello-proc"
	_, _ = hub.RegisterDevice(routeID, deviceID)

	bridgeIdentityDir := t.TempDir()
	identity, _ := LoadOrCreateRelayCryptoIdentity(bridgeIdentityDir)

	store := NewMemoryDeviceStore()
	iosPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	iosPubB64 := base64.StdEncoding.EncodeToString(iosPriv.PublicKey().Bytes())
	store.AddDevice(TrustedDeviceRecord{
		DeviceID:          deviceID,
		IdentityPublicKey: iosPubB64,
	})

	handlers := NewHandlers()
	handlers.SetBridgeID("brg-test")
	handlers.ConfigureRelayUpgrade(store, identity, nil)

	client := NewRelayBridgeClient(handlers, hub, identity, "brg-test", routeID, bridgeAuth)

	// 构造一个有效的 OnlineClientHello
	authKey, err := identity.DeriveIdentityAuthKey(iosPriv.PublicKey().Bytes(), "brg-test", deviceID)
	if err != nil {
		t.Fatalf("derive auth key: %v", err)
	}

	iosEphPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientRandom := make([]byte, 32)
	rand.Read(clientRandom)

	hello := OnlineClientHello{
		Type:                  "online_client_hello",
		BridgeID:              "brg-test",
		DeviceID:              deviceID,
		ChannelGeneration:     1,
		IOSEphemeralPublicKey: base64.StdEncoding.EncodeToString(iosEphPriv.PublicKey().Bytes()),
		ClientRandom:          base64.StdEncoding.EncodeToString(clientRandom),
	}

	// 计算 auth tag
	canonical, _ := canonicalOnlineClientHello(hello)
	hello.AuthTag = base64.StdEncoding.EncodeToString(hmacSHA256(authKey, canonical))

	// 设置 mock connection，让 handleClientHello 能发送响应
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		ws, _ := upgrader.Upgrade(w, r, nil)
		// 保持连接，让 bridge client 可以写入
		time.Sleep(2 * time.Second)
		ws.Close()
	}))
	defer mockServer.Close()

	mockWSURL := "ws" + strings.TrimPrefix(mockServer.URL, "http")
	mockConn, _, err := websocket.DefaultDialer.Dial(mockWSURL, nil)
	if err != nil {
		t.Fatalf("mock dial: %v", err)
	}
	defer mockConn.Close()

	client.mu.Lock()
	client.conn = mockConn
	client.mu.Unlock()

	client.handleClientHello(hello)

	// 验证设备已注册
	if client.ActiveDeviceCount() != 1 {
		t.Errorf("active devices = %d, want 1", client.ActiveDeviceCount())
	}

	client.mu.Lock()
	relayConn := client.devices[deviceID]
	client.mu.Unlock()
	if relayConn == nil {
		t.Fatal("relay device connection was not registered")
	}
	if bytes.Equal(relayConn.macToIosKey, make([]byte, len(relayConn.macToIosKey))) ||
		bytes.Equal(relayConn.iosToMacKey, make([]byte, len(relayConn.iosToMacKey))) {
		t.Fatal("relay device connection retained destroyed handshake key material")
	}

	client.Close()
}

func TestRelayBridgeClientRejectsUnknownDevice(t *testing.T) {
	hub := NewRelayHub()
	routeID, bridgeAuth, _ := hub.RegisterRoute()
	bridgeIdentityDir := t.TempDir()
	identity, _ := LoadOrCreateRelayCryptoIdentity(bridgeIdentityDir)

	store := NewMemoryDeviceStore()
	handlers := NewHandlers()
	handlers.SetBridgeID("brg-test")
	handlers.ConfigureRelayUpgrade(store, identity, nil)

	client := NewRelayBridgeClient(handlers, hub, identity, "brg-test", routeID, bridgeAuth)

	hello := OnlineClientHello{
		Type:              "online_client_hello",
		BridgeID:          "brg-test",
		DeviceID:          "dev-nonexistent",
		ChannelGeneration: 1,
	}

	client.handleClientHello(hello)

	if client.ActiveDeviceCount() != 0 {
		t.Error("unknown device should not be registered")
	}
	client.Close()
}

// TestRelayBridgeClientPingAndReadDeadlineDetectsHalfOpen 验证 P0-B 保活：
// (1) client 主动发 ping（pingLoop 生效）；(2) 对端不回 pong 时，读 deadline 到期 →
// readLoop 退出并 close(c.done)——这正是 Run() 重连循环 select 的触发信号
// （relay_bridge_client.go Run 的 case <-done），故证明"卡连接中"能在 ~读 deadline 内自愈，
// 而非原症状的假活数分钟。
//
// 用单次 Connect（不跑 Run 重连循环）测试：pingLoop/readLoop 只构造一次，读 relayPingPeriod/
// relayReadTimeout 也只在构造期发生一次，与测试末尾的 var 恢复 defer 在时间上错开，确定性无 data race。
// 保活参数覆盖为短值（spec 改进项3：可注入短超时）。
//
// 关联：docs/2026-06-17-bridge-hang-implementation-spec.md P0-B + 验证方法 §2。
func TestRelayBridgeClientPingAndReadDeadlineDetectsHalfOpen(t *testing.T) {
	// 覆盖保活参数为短值：ping 50ms / 读 deadline 200ms。
	oldP, oldR := relayPingPeriod, relayReadTimeout
	relayPingPeriod = 50 * time.Millisecond
	relayReadTimeout = 200 * time.Millisecond
	defer func() { relayPingPeriod, relayReadTimeout = oldP, oldR }()

	pingSeen := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		// 自定义 PingHandler：记录 client 发来的 ping，但不回 pong（模拟半开/对端不响应）。
		ws.SetPingHandler(func(appData string) error {
			select {
			case pingSeen <- struct{}{}:
			default:
			}
			return nil
		})
		// 读循环驱动 PingHandler（gorilla 在 ReadMessage 内部处理 ping 帧并回调 handler）。
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	client := NewRelayBridgeClient(NewHandlers(), nil, nil, "brg-test", "route-test", "bridge-secret")
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if err := client.Connect(wsURL); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// 1) client 主动发 ping（pingLoop 生效，WriteControl 不套 writeMu）。
	select {
	case <-pingSeen:
		t.Logf("收到 client ping（pingLoop 生效）")
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 client ping — pingLoop 未生效")
	}

	// 2) 对端不回 pong → 读 deadline 到期 → readLoop 退出 → close(c.done)。
	// c.done 关闭即 Run() 重连循环的触发信号（证明半开能在 deadline 内自愈）。
	client.mu.Lock()
	done := client.done
	client.mu.Unlock()
	if done == nil {
		t.Fatal("c.done 为 nil")
	}
	select {
	case <-done:
		t.Logf("读 deadline 到期，readLoop 退出并关闭 c.done（驱动 Run 重连）")
	case <-time.After(2 * time.Second):
		t.Fatal("c.done 未在读 deadline 内关闭 — 读 deadline/ping 保活未生效（原症状：假活数分钟）")
	}

	// 清理：Close 关闭 c.done 让残留 pingLoop 退出。等一个 ping 周期确保 goroutine 收尾，
	// 再让 var 恢复 defer 执行（本测试 pingLoop 构造期早已完成，此处为防御性等待）。
	client.Close()
	time.Sleep(relayPingPeriod + 50*time.Millisecond)
}
