package gobridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestApproveThenAuthConnect 验证完整配对→审批→认证→WebSocket 连接流程。
// 模拟 iOS 外网配对场景：pairing 通过 /pairing → approve 后 iOS 用 token 连接 /。
func TestApproveThenAuthConnect(t *testing.T) {
	// 1. 设置全局状态（保存/恢复避免影响其他测试）
	prevPairingStore := globalPairingStore
	prevDeviceStore := globalDeviceStore
	prevRegistry := globalPairingRegistry

	pairingStore := NewMemoryPairingStore()
	deviceStore := NewMemoryDeviceStore()
	globalPairingStore = pairingStore
	globalDeviceStore = deviceStore
	globalPairingRegistry = &PairingPendingRegistry{conns: make(map[string]*PairingPendingConn)}
	t.Cleanup(func() {
		globalPairingStore = prevPairingStore
		globalDeviceStore = prevDeviceStore
		globalPairingRegistry = prevRegistry
	})

	handlers := NewHandlers()
	handlers.RegisterAgent("claude", &mgmtFakeAgent{name: "claudecode"})

	server := NewServer(handlers)
	server.SetBridgeIdentity("brg-test", "Test Bridge", "0.1.0", "ws://192.168.1.100:8777", "wss://example.com:9090")
	server.SetAuthMiddleware(NewAuthMiddleware(deviceStore))

	// 同时挂载 /pairing 和 /
	mux := http.NewServeMux()
	mux.Handle("/pairing", http.HandlerFunc(handlePairingWebSocket))
	mux.Handle("/", server)

	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// 2. 创建配对会话（模拟 Mac App 调用管理 API）
	session := NewPairingSession("brg-test", "Test Bridge", "ws://192.168.1.100:8777", "wss://example.com:9090", 5*time.Minute)
	if err := pairingStore.Create(session); err != nil {
		t.Fatalf("创建配对会话失败: %v", err)
	}

	// 3. iOS 连接到 /pairing 并发送 pairing_claim
	wsPairURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/pairing"
	iOSConn, _, err := websocket.DefaultDialer.Dial(wsPairURL, nil)
	if err != nil {
		t.Fatalf("iOS 连接 /pairing 失败: %v", err)
	}
	defer iOSConn.Close()

	claim := map[string]any{
		"type":       "pairing_claim",
		"pairingId":  session.ID,
		"manualCode": session.ManualCode,
		"device": map[string]string{
			"deviceId":    "ios-claim-device",
			"displayName": "Jack iPhone",
			"platform":    "ios",
		},
	}
	if err := iOSConn.WriteJSON(claim); err != nil {
		t.Fatalf("发送 pairing_claim 失败: %v", err)
	}

	// 读取 pairing_result
	iOSConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var result map[string]any
	if err := iOSConn.ReadJSON(&result); err != nil {
		t.Fatalf("读取 pairing_result 失败: %v", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("pairing_result.ok = false: %v", result)
	}
	t.Logf("pairing_result: ok=true, session state=%s", session.State)

	// 4. Mac App 审批配对（模拟管理 API approve 调用）
	if err := session.Approve(); err != nil {
		t.Fatalf("Approve 失败: %v", err)
	}
	pairingStore.Update(session)

	// 添加设备到 store（和 handlePairingApprove 相同的逻辑）
	deviceRecord := TrustedDeviceRecord{
		DeviceID:    session.DeviceID,
		DisplayName: session.ClaimingDeviceName,
		Platform:    session.ClaimingPlatform,
		TokenHash:   session.DeviceTokenHash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	if err := deviceStore.AddDevice(deviceRecord); err != nil {
		t.Fatalf("AddDevice 失败: %v", err)
	}
	t.Logf("设备已注册: id=%s tokenHash=%s", session.DeviceID, session.DeviceTokenHash)

	// 推送 pairing_complete 给 iOS
	plainToken := session.DeviceToken
	push := PairingCompletePush{
		Type: "pairing_complete",
		Device: PairingCompleteDevice{
			DeviceID: session.DeviceID,
			Token:    plainToken,
		},
		Bridge: PairingCompleteBridge{
			BridgeID:    "brg-test",
			DisplayName: "Test Bridge",
			LocalURL:    "ws://192.168.1.100:8777/bridge",
			RemoteURL:   strPtr("wss://example.com:9090"),
		},
	}
	globalPairingRegistry.NotifyComplete(session.ID, push)
	session.Complete()
	pairingStore.Update(session)

	// 读取 pairing_complete
	iOSConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var complete map[string]any
	if err := iOSConn.ReadJSON(&complete); err != nil {
		t.Fatalf("读取 pairing_complete 失败: %v", err)
	}
	if complete["type"] != "pairing_complete" {
		t.Fatalf("期望 pairing_complete, got type=%v", complete["type"])
	}
	deviceInfo, _ := complete["device"].(map[string]any)
	receivedToken, _ := deviceInfo["token"].(string)
	receivedDeviceID, _ := deviceInfo["deviceId"].(string)
	t.Logf("pairing_complete: deviceId=%s token=%s...", receivedDeviceID, receivedToken[:12])

	// 5. 无 auth 连接 bridge → 应被拒绝
	wsBridgeURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/"
	_, respNoAuth, errNoAuth := websocket.DefaultDialer.Dial(wsBridgeURL, nil)
	if errNoAuth == nil {
		t.Fatal("无 auth 应该被拒绝")
	}
	if respNoAuth != nil && respNoAuth.StatusCode != http.StatusUnauthorized {
		t.Errorf("无 auth 期望 401, got %d", respNoAuth.StatusCode)
	}
	t.Log("无 auth 连接被正确拒绝 (401) ✓")

	// 6. 通过 query 参数传递 token 连接（模拟 iOS URLSessionWebSocketTask 的实际路径）
	wsAuthURL := wsBridgeURL + "?token=" + receivedToken + "&deviceId=" + receivedDeviceID
	bridgeConn, resp2, err := websocket.DefaultDialer.Dial(wsAuthURL, nil)
	if resp2 != nil && resp2.StatusCode != http.StatusSwitchingProtocols {
		var errBody map[string]any
		json.NewDecoder(resp2.Body).Decode(&errBody)
		t.Fatalf("bridge query-param auth 连接失败: status=%d body=%v token=%s... deviceId=%s",
			resp2.StatusCode, errBody, receivedToken[:12], receivedDeviceID)
	}
	if err != nil {
		t.Fatalf("bridge query-param auth 连接失败: %v", err)
	}
	defer bridgeConn.Close()
	t.Log("bridge WebSocket 连接成功（query-param auth 通过）")

	// 7. 发送 hello 并等待 hello_ack
	helloMsg := map[string]any{
		"type": "hello",
		"client": map[string]string{
			"app":      "CordCode iOS",
			"version":  "1.0.0",
			"deviceId": receivedDeviceID,
		},
		"protocol": map[string]any{
			"name":                     "cccode-bridge",
			"version":                  1,
			"supportedSchemaRevisions": []string{"v1"},
		},
	}
	if err := bridgeConn.WriteJSON(helloMsg); err != nil {
		t.Fatalf("发送 hello 失败: %v", err)
	}

	bridgeConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var helloAck map[string]any
	if err := bridgeConn.ReadJSON(&helloAck); err != nil {
		t.Fatalf("读取 hello_ack 失败: %v", err)
	}
	if helloAck["type"] != "hello_ack" {
		t.Fatalf("期望 hello_ack, got type=%v", helloAck["type"])
	}
	if ok, _ := helloAck["ok"].(bool); !ok {
		t.Fatalf("hello_ack.ok = false: %v", helloAck)
	}

	t.Logf("hello_ack: ok=true, backends=%v", helloAck["backends"])
	t.Log("完整配对→审批→认证→连接→握手流程验证通过 ✓")
}

// TestApproveThenAuthWithWrongToken 验证错误 token 被正确拒绝。
func TestApproveThenAuthWithWrongToken(t *testing.T) {
	prevDeviceStore2 := globalDeviceStore
	prevPairingStore2 := globalPairingStore
	prevRegistry2 := globalPairingRegistry

	deviceStore := NewMemoryDeviceStore()
	globalDeviceStore = deviceStore
	globalPairingStore = NewMemoryPairingStore()
	globalPairingRegistry = &PairingPendingRegistry{conns: make(map[string]*PairingPendingConn)}
	t.Cleanup(func() {
		globalDeviceStore = prevDeviceStore2
		globalPairingStore = prevPairingStore2
		globalPairingRegistry = prevRegistry2
	})

	handlers := NewHandlers()
	handlers.RegisterAgent("claude", &mgmtFakeAgent{name: "claudecode"})
	server := NewServer(handlers)
	server.SetAuthMiddleware(NewAuthMiddleware(deviceStore))

	mux := http.NewServeMux()
	mux.Handle("/", server)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// 注册一台设备
	plain, hash, _ := GenerateDeviceToken()
	deviceStore.AddDevice(TrustedDeviceRecord{
		DeviceID:    "dev_test",
		DisplayName: "Test",
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	})

	// 用错误 token 连接
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/"
	authHeader := http.Header{}
	authHeader.Set("Authorization", "Bearer ccb1_wrongwrongwrongwrongwrongwrongwrongwrong")
	authHeader.Set("X-CCCode-Device-ID", "dev_test")

	resp, err := http.DefaultClient.Do(&http.Request{
		Method: "GET",
		URL:    mustParseURL(wsURL),
		Header: authHeader,
	})
	_ = resp
	_ = err

	// gorilla/websocket Dial 会在非 101 时返回错误
	_, resp2, err2 := websocket.DefaultDialer.Dial(wsURL, authHeader)
	if err2 == nil {
		t.Fatal("错误 token 应该被拒绝，但连接成功了")
	}
	if resp2 != nil && resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("期望 401, got %d", resp2.StatusCode)
	}
	t.Logf("错误 token 被正确拒绝: %v (expect 401)", err2)

	// 用正确 token 连接应该成功
	authHeader2 := http.Header{}
	authHeader2.Set("Authorization", "Bearer "+plain)
	authHeader2.Set("X-CCCode-Device-ID", "dev_test")

	conn, resp3, err3 := websocket.DefaultDialer.Dial(wsURL, authHeader2)
	if err3 != nil {
		if resp3 != nil {
			t.Fatalf("正确 token 连接失败: status=%d err=%v", resp3.StatusCode, err3)
		}
		t.Fatalf("正确 token 连接失败: %v", err3)
	}
	conn.Close()
	t.Log("正确 token 连接成功 ✓")
}

// TestTokenHashConsistency 验证 token hash 在生成和验证之间的一致性。
func TestTokenHashConsistency(t *testing.T) {
	plain, hash, err := GenerateDeviceToken()
	if err != nil {
		t.Fatalf("GenerateDeviceToken 失败: %v", err)
	}

	// 手动计算 hash 验证
	computed := HashToken(plain)
	if computed != hash {
		t.Fatalf("hash 不一致: GenerateDeviceToken 返回 %s, HashToken 返回 %s", hash, computed)
	}

	// 验证 ValidateDeviceAuth 能找到设备
	store := NewMemoryDeviceStore()
	store.AddDevice(TrustedDeviceRecord{
		DeviceID:    "dev_hash_test",
		DisplayName: "Test",
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	})

	rec, err := ValidateDeviceAuth(store, plain, "dev_hash_test")
	if err != nil {
		t.Fatalf("ValidateDeviceAuth 失败: %v", err)
	}
	if rec.DeviceID != "dev_hash_test" {
		t.Errorf("rec.DeviceID = %s, want dev_hash_test", rec.DeviceID)
	}
	t.Logf("token hash 一致性验证通过: token=%s... hash=%s", plain[:12], hash[:20])
}

func strPtr(s string) *string { return &s }

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
