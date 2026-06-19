package gobridge

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cccode-macbridge/core"
)

// ── 测试用 fake agent ────────────────────────────────────────────────────────

type mgmtFakeAgent struct {
	name string
}

func (f *mgmtFakeAgent) Name() string { return f.name }
func (f *mgmtFakeAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return nil, nil
}
func (f *mgmtFakeAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (f *mgmtFakeAgent) Stop() error { return nil }

// ── 测试用配置构造 ──────────────────────────────────────────────────────────

const testMgmtToken = "test-mgmt-token-123"

func newTestMgmtServer(agents map[string]core.Agent) *ManagementServer {
	if agents == nil {
		agents = map[string]core.Agent{
			"claude": &mgmtFakeAgent{name: "claudecode"},
		}
	}
	cfg := ManagementConfig{
		Handlers:     NewHandlers(),
		Token:        testMgmtToken,
		DataDir:      nil,
		PairingStore: NewMemoryPairingStore(),
		DeviceStore:  NewMemoryDeviceStore(),
		BridgeID:     "brg_test123",
		DisplayName:  "Test Bridge",
		LocalURL:     "ws://127.0.0.1:8777",
		Agents:       agents,
	}
	return NewManagementServer(cfg)
}

func authRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+testMgmtToken)
	return req
}

func noAuthRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestMgmtAuth_ValidToken(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/status")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestMgmtAuth_InvalidToken(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/internal/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMgmtAuth_MissingAuth(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := noAuthRequest(http.MethodGet, "/internal/status")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMgmtStatus_Ready(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/status")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if body["status"] != "ready" {
		t.Errorf("status = %v, want ready", body["status"])
	}
	if body["bridgeId"] != "brg_test123" {
		t.Errorf("bridgeId = %v, want brg_test123", body["bridgeId"])
	}
	if body["displayName"] != "Test Bridge" {
		t.Errorf("displayName = %v, want Test Bridge", body["displayName"])
	}
	if body["version"] == nil {
		t.Error("version 不应为 nil")
	}
	if body["uptime"] == nil {
		t.Error("uptime 不应为 nil")
	}
}

func TestMgmtStatus_NoAgents(t *testing.T) {
	srv := newTestMgmtServer(map[string]core.Agent{})
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/status")
	srv.ServeHTTP(rec, req)

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ready_no_agents" {
		t.Errorf("status = %v, want ready_no_agents", body["status"])
	}
}

func TestMgmtAgents(t *testing.T) {
	agents := map[string]core.Agent{
		"claude":   &mgmtFakeAgent{name: "claudecode"},
		"opencode": &mgmtFakeAgent{name: "opencode"},
	}
	srv := newTestMgmtServer(agents)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/agents")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var descs []AgentProviderDescriptor
	if err := json.Unmarshal(rec.Body.Bytes(), &descs); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if len(descs) != 2 {
		t.Fatalf("agent 数量 = %d, want 2", len(descs))
	}
	// 按 ID 字典序
	if descs[0].ID != "claude" || descs[1].ID != "opencode" {
		t.Errorf("顺序错误: %s, %s", descs[0].ID, descs[1].ID)
	}
}

func TestMgmtAgentsGETUsesCachedDescriptorsUntilRefresh(t *testing.T) {
	agents := map[string]core.Agent{
		"custom_a": &mgmtFakeAgent{name: "Custom A"},
	}
	srv := newTestMgmtServer(agents)

	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/agents")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var cached []AgentProviderDescriptor
	if err := json.Unmarshal(rec.Body.Bytes(), &cached); err != nil {
		t.Fatalf("GET JSON 解析失败: %v", err)
	}
	if len(cached) != 1 || cached[0].ID != "custom_a" {
		t.Fatalf("GET descriptors = %#v, want cached custom_a only", cached)
	}

	agents["custom_b"] = &mgmtFakeAgent{name: "Custom B"}

	rec = httptest.NewRecorder()
	req = authRequest(http.MethodGet, "/internal/agents")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second GET status = %d, want 200", rec.Code)
	}
	cached = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &cached); err != nil {
		t.Fatalf("second GET JSON 解析失败: %v", err)
	}
	if len(cached) != 1 || cached[0].ID != "custom_a" {
		t.Fatalf("second GET descriptors = %#v, want cached custom_a only", cached)
	}

	rec = httptest.NewRecorder()
	req = authRequest(http.MethodPost, "/internal/agents/refresh")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = authRequest(http.MethodGet, "/internal/agents")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after refresh status = %d, want 200", rec.Code)
	}
	var refreshed []AgentProviderDescriptor
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshed); err != nil {
		t.Fatalf("GET after refresh JSON 解析失败: %v", err)
	}
	if len(refreshed) != 2 || refreshed[0].ID != "custom_a" || refreshed[1].ID != "custom_b" {
		t.Fatalf("GET after refresh descriptors = %#v, want custom_a/custom_b", refreshed)
	}
}

func TestMgmtShutdown(t *testing.T) {
	srv := newTestMgmtServer(nil)
	var shutdownCalled atomic.Bool
	srv.SetShutdownCallback(func() { shutdownCalled.Store(true) })

	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/shutdown")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["shutting_down"] != true {
		t.Error("shutting_down 应为 true")
	}

	// 等待异步回调
	time.Sleep(50 * time.Millisecond)
	if !shutdownCalled.Load() {
		t.Error("shutdown 回调未被调用")
	}
}

func TestMgmtDevices_Empty(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/devices")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var devices []TrustedDeviceRecord
	json.Unmarshal(rec.Body.Bytes(), &devices)
	if len(devices) != 0 {
		t.Errorf("设备数 = %d, want 0", len(devices))
	}
}

func TestMgmtDevices_WithRecords(t *testing.T) {
	store := NewMemoryDeviceStore()
	store.AddDevice(TrustedDeviceRecord{
		DeviceID:    "dev_abc",
		DisplayName: "iPhone",
		Platform:    "ios",
		TokenHash:   "sha256:abc",
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	})

	cfg := ManagementConfig{
		Handlers:     NewHandlers(),
		Token:        testMgmtToken,
		PairingStore: NewMemoryPairingStore(),
		DeviceStore:  store,
		BridgeID:     "brg_test",
		DisplayName:  "Test",
		LocalURL:     "ws://127.0.0.1:8777",
		Agents:       map[string]core.Agent{"claude": &mgmtFakeAgent{name: "claudecode"}},
	}
	srv := NewManagementServer(cfg)

	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/devices")
	srv.ServeHTTP(rec, req)

	var devices []TrustedDeviceRecord
	json.Unmarshal(rec.Body.Bytes(), &devices)
	if len(devices) != 1 || devices[0].DeviceID != "dev_abc" {
		t.Errorf("设备列表不符: %+v", devices)
	}
}

func TestMgmtRevokeDevice(t *testing.T) {
	store := NewMemoryDeviceStore()
	store.AddDevice(TrustedDeviceRecord{
		DeviceID:    "dev_revoke_me",
		DisplayName: "Test",
		Platform:    "ios",
		TokenHash:   "sha256:xyz",
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	})

	cfg := ManagementConfig{
		Handlers:     NewHandlers(),
		Token:        testMgmtToken,
		PairingStore: NewMemoryPairingStore(),
		DeviceStore:  store,
		BridgeID:     "brg_test",
		DisplayName:  "Test",
		LocalURL:     "ws://127.0.0.1:8777",
		Agents:       map[string]core.Agent{"claude": &mgmtFakeAgent{name: "claudecode"}},
	}
	srv := NewManagementServer(cfg)

	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/devices/dev_revoke_me/revoke")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	dev, err := store.LookupByDeviceID("dev_revoke_me")
	if err != nil {
		t.Fatalf("查询设备失败: %v", err)
	}
	if dev == nil {
		t.Fatal("设备不应为 nil")
	}
	if dev.RevokedAt == nil {
		t.Error("设备应已被吊销，RevokedAt 为 nil")
	}
}

func TestMgmtRevokeDevice_NotFound(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/devices/dev_nonexistent/revoke")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestMgmtPairingCreate(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/pairing/create")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var session map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &session)

	if session["id"] == nil || session["id"] == "" {
		t.Error("session id 不应为空")
	}
	if session["qrPayload"] == nil || session["qrPayload"] == "" {
		t.Error("qrPayload 不应为空")
	}
	if session["manualCode"] == nil || session["manualCode"] == "" {
		t.Error("manualCode 不应为空")
	}
	if session["state"] != "created" {
		t.Errorf("state = %v, want created", session["state"])
	}
	// QR payload 格式检查：应包含 host、port、name
	qrPayload, _ := session["qrPayload"].(string)
	if !strings.HasPrefix(qrPayload, "cccode://pair?") {
		t.Errorf("qrPayload 格式错误: %s", qrPayload)
	}
	if !strings.Contains(qrPayload, "host=") {
		t.Errorf("qrPayload 缺少 host 参数: %s", qrPayload)
	}
	if !strings.Contains(qrPayload, "port=") {
		t.Errorf("qrPayload 缺少 port 参数: %s", qrPayload)
	}
	if !strings.Contains(qrPayload, "name=") {
		t.Errorf("qrPayload 缺少 name 参数: %s", qrPayload)
	}
	// manual code 应为 6 位数字
	manualCode, _ := session["manualCode"].(string)
	if len(manualCode) != 6 {
		t.Errorf("manualCode 长度 = %d, want 6", len(manualCode))
	}
}

func TestMgmtPairingCreateIncludesRelayFirstQRWhenConfigured(t *testing.T) {
	identity, err := LoadOrCreateRelayCryptoIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestMgmtServer(nil)
	srv.cfg.RelayConfigured = true
	srv.cfg.RelayEnabled = true
	srv.cfg.RelayEndpoint = "wss://relay.example.com:8443"
	srv.cfg.RelayRouteID = "route_123"
	srv.cfg.RelayIdentity = identity

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authRequest(http.MethodPost, "/internal/pairing/create"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var session PairingSession
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(session.QRPayload)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("relay") != "wss://relay.example.com:8443" || query.Get("relayRoute") != "route_123" {
		t.Fatalf("relay QR fields missing: %s", session.QRPayload)
	}
	if query.Get("relayBridgeKey") == "" || query.Get("relayCapability") == "" {
		t.Fatalf("relay security QR fields missing: %s", session.QRPayload)
	}
}

func TestMgmtRelayFirstClaimApprovalProducesSealedResult(t *testing.T) {
	identity, err := LoadOrCreateRelayCryptoIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	devicePrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pairID string
	var completion RelayFirstResult
	var registeredDeviceID string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pairing-claims"):
			claim, claimErr := CreatePairingClaim("candidate_phone", "iPhone", devicePrivate.PublicKey().Bytes(), identity.PublicKeyBytes())
			if claimErr != nil {
				t.Fatal(claimErr)
			}
			sealed, _ := json.Marshal(claim)
			json.NewEncoder(w).Encode(map[string]any{"claims": []map[string]any{{
				"claimId": pairID, "state": "pending", "sealedClaim": sealed,
			}}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/devices/register"):
			var registration struct {
				DeviceID string `json:"deviceId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
				t.Fatal(err)
			}
			registeredDeviceID = registration.DeviceID
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"deviceAuth": "relay_device_auth"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/complete"):
			var payload struct {
				State        string `json:"state"`
				SealedResult []byte `json:"sealedResult"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			var ciphertext HPKECiphertext
			if err := json.Unmarshal(payload.SealedResult, &ciphertext); err != nil {
				t.Fatal(err)
			}
			plaintext, _, err := HPKEOpen(devicePrivate.Bytes(), []byte(pairingContextLabel), []byte("pairing-result:"+registeredDeviceID), &ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(plaintext, &completion); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected relay request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer relay.Close()

	store := NewMemoryPairingStore()
	srv := NewManagementServer(ManagementConfig{
		Token:           testMgmtToken,
		PairingStore:    store,
		DeviceStore:     NewMemoryDeviceStore(),
		BridgeID:        "brg_relay",
		DisplayName:     "Relay Mac",
		LocalURL:        "ws://127.0.0.1:8777",
		RelayEndpoint:   relay.URL,
		RelayRouteID:    "route_123",
		RelayCredential: "bridge_auth",
		RelayConfigured: true,
		RelayEnabled:    true,
		RelayIdentity:   identity,
		Agents:          map[string]core.Agent{"claude": &mgmtFakeAgent{name: "claudecode"}},
	})
	session := NewPairingSession("brg_relay", "Relay Mac", "ws://127.0.0.1:8777", "", 5*time.Minute)
	pairID = session.ID
	if err := store.Create(session); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authRequest(http.MethodGet, "/internal/pairing/"+pairID))
	if rec.Code != http.StatusOK || session.State != PairingClaimed || session.RelayClaim == nil {
		t.Fatalf("relay claim not synchronized: status=%d state=%s body=%s", rec.Code, session.State, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, authRequest(http.MethodPost, "/internal/pairing/"+pairID+"/approve"))
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", rec.Code, rec.Body.String())
	}
	var approval struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	if approval.DeviceID == "" || completion.DeviceID != approval.DeviceID || completion.DeviceAuth != "relay_device_auth" {
		t.Fatal("relay approval did not create device")
	}
}

func TestMgmtPairingGet(t *testing.T) {
	pairingStore := NewMemoryPairingStore()
	cfg := ManagementConfig{
		Handlers:     NewHandlers(),
		Token:        testMgmtToken,
		PairingStore: pairingStore,
		DeviceStore:  NewMemoryDeviceStore(),
		BridgeID:     "brg_test",
		DisplayName:  "Test",
		LocalURL:     "ws://127.0.0.1:8777",
	}
	srv := NewManagementServer(cfg)

	session := NewPairingSession("brg_test", "Test", "ws://127.0.0.1:8777", "", 5*time.Minute)
	pairingStore.Create(session)

	// GET existing session
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/pairing/"+session.ID)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["id"] != session.ID {
		t.Errorf("id = %v, want %s", result["id"], session.ID)
	}
	if result["state"] != "created" {
		t.Errorf("state = %v, want created", result["state"])
	}

	// GET nonexistent session
	rec2 := httptest.NewRecorder()
	req2 := authRequest(http.MethodGet, "/internal/pairing/pair_nonexistent")
	srv.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec2.Code)
	}
}

func TestMgmtPairingApprove(t *testing.T) {
	pairingStore := NewMemoryPairingStore()
	cfg := ManagementConfig{
		Handlers:     NewHandlers(),
		Token:        testMgmtToken,
		PairingStore: pairingStore,
		DeviceStore:  NewMemoryDeviceStore(),
		BridgeID:     "brg_test",
		DisplayName:  "Test",
		LocalURL:     "ws://127.0.0.1:8777",
		Agents:       map[string]core.Agent{"claude": &mgmtFakeAgent{name: "claudecode"}},
	}
	srv := NewManagementServer(cfg)

	// 创建并认领会话
	session := NewPairingSession("brg_test", "Test", "ws://127.0.0.1:8777", "", 5*time.Minute)
	session.Claim("dev_claimant", "iPhone", "ios")
	pairingStore.Create(session)

	// approve
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/pairing/"+session.ID+"/approve")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)

	if body["deviceToken"] == nil || body["deviceToken"] == "" {
		t.Error("deviceToken 不应为空")
	}
	if body["deviceId"] == nil || body["deviceId"] == "" {
		t.Error("deviceId 不应为空")
	}
	if body["state"] != "approved" {
		t.Errorf("state = %v, want approved", body["state"])
	}

	// 验证 device token 格式
	token, _ := body["deviceToken"].(string)
	if !strings.HasPrefix(token, "ccb1_") {
		t.Errorf("deviceToken 格式错误: %s", token)
	}
}

func TestMgmtPairingApprove_NotFound(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/pairing/pair_nonexistent/approve")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestMgmtPairingReject(t *testing.T) {
	pairingStore := NewMemoryPairingStore()
	cfg := ManagementConfig{
		Handlers:     NewHandlers(),
		Token:        testMgmtToken,
		PairingStore: pairingStore,
		DeviceStore:  NewMemoryDeviceStore(),
		BridgeID:     "brg_test",
		DisplayName:  "Test",
		LocalURL:     "ws://127.0.0.1:8777",
		Agents:       map[string]core.Agent{"claude": &mgmtFakeAgent{name: "claudecode"}},
	}
	srv := NewManagementServer(cfg)

	session := NewPairingSession("brg_test", "Test", "ws://127.0.0.1:8777", "", 5*time.Minute)
	session.Claim("dev_rejected", "iPhone", "ios")
	pairingStore.Create(session)

	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/pairing/"+session.ID+"/reject")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["state"] != "rejected" {
		t.Errorf("state = %v, want rejected", body["state"])
	}
}

func TestMgmtPairingReject_NotClaimed(t *testing.T) {
	pairingStore := NewMemoryPairingStore()
	cfg := ManagementConfig{
		Handlers:     NewHandlers(),
		Token:        testMgmtToken,
		PairingStore: pairingStore,
		DeviceStore:  NewMemoryDeviceStore(),
		BridgeID:     "brg_test",
		DisplayName:  "Test",
		LocalURL:     "ws://127.0.0.1:8777",
		Agents:       map[string]core.Agent{"claude": &mgmtFakeAgent{name: "claudecode"}},
	}
	srv := NewManagementServer(cfg)

	// 创建但不 claim
	session := NewPairingSession("brg_test", "Test", "ws://127.0.0.1:8777", "", 5*time.Minute)
	pairingStore.Create(session)

	rec := httptest.NewRecorder()
	req := authRequest(http.MethodPost, "/internal/pairing/"+session.ID+"/reject")
	srv.ServeHTTP(rec, req)

	// 未 claim 就 reject 应返回 400
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMgmtLogsRecent_NoDataDir(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/logs/recent")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var lines []string
	json.Unmarshal(rec.Body.Bytes(), &lines)
	if len(lines) != 0 {
		t.Errorf("无 DataDir 时应返回空数组, got %d lines", len(lines))
	}
}

func TestMgmtNotFound(t *testing.T) {
	srv := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/nonexistent")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestMgmtPortZero(t *testing.T) {
	srv := newTestMgmtServer(nil)
	actualPort, err := srv.Start("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if actualPort == 0 {
		t.Fatal("actualPort 不应为 0，OS 应分配了端口")
	}
	t.Logf("OS 分配端口: %d", actualPort)
}

// ── GET /internal/remote/status ──────────────────────────────────────────────

func newTestMgmtServerWithRemote(remoteURL string) *ManagementServer {
	cfg := ManagementConfig{
		Handlers:      NewHandlers(),
		Token:         testMgmtToken,
		DataDir:       nil,
		PairingStore:  NewMemoryPairingStore(),
		DeviceStore:   NewMemoryDeviceStore(),
		BridgeID:      "brg_test123",
		DisplayName:   "Test Bridge",
		LocalURL:      "ws://192.168.1.100:8777",
		RemoteURL:     remoteURL,
		IncludeRemote: true,
		Agents:        map[string]core.Agent{"claude": &mgmtFakeAgent{name: "claudecode"}},
	}
	return NewManagementServer(cfg)
}

func TestMgmtRemoteStatus_LocalOnly(t *testing.T) {
	srv := newTestMgmtServerWithRemote("")
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/remote/status")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if body["connectionMode"] != "local_only" {
		t.Errorf("connectionMode = %v, want local_only", body["connectionMode"])
	}
	if body["remoteConfigured"] != false {
		t.Errorf("remoteConfigured = %v, want false", body["remoteConfigured"])
	}
	if body["remoteAnalysis"] != nil {
		t.Errorf("remoteAnalysis 应为 nil, got %v", body["remoteAnalysis"])
	}
}

func TestMgmtRemoteStatus_RelayConfigDoesNotExposeCredential(t *testing.T) {
	srv := newTestMgmtServerWithRemote("")
	srv.cfg.RelayEndpoint = "wss://relay.example.com"
	srv.cfg.RelayRouteID = "route_123"
	srv.cfg.RelayConfigured = true
	srv.cfg.RelayEnabled = true
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/remote/status")
	srv.ServeHTTP(rec, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	relay, ok := body["relay"].(map[string]interface{})
	if !ok || relay["configured"] != true || relay["routeId"] != "route_123" {
		t.Fatalf("relay 状态错误: %#v", body["relay"])
	}
	if _, exists := relay["credential"]; exists {
		t.Fatal("relay 状态不得暴露 credential")
	}
}

func TestMgmtRemoteStatus_TailscaleWS(t *testing.T) {
	srv := newTestMgmtServerWithRemote("ws://100.100.50.20:8777")
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/remote/status")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["connectionMode"] != "remote_configured" {
		t.Errorf("connectionMode = %v, want remote_configured", body["connectionMode"])
	}

	analysis, ok := body["remoteAnalysis"].(map[string]interface{})
	if !ok {
		t.Fatal("remoteAnalysis 应为 object")
	}
	if analysis["isTailscaleCGNAT"] != true {
		t.Errorf("isTailscaleCGNAT = %v, want true", analysis["isTailscaleCGNAT"])
	}
	if analysis["securityLevel"] != "tailscale_tunnel" {
		t.Errorf("securityLevel = %v, want tailscale_tunnel", analysis["securityLevel"])
	}
	if analysis["hostCategory"] != "tailscale" {
		t.Errorf("hostCategory = %v, want tailscale", analysis["hostCategory"])
	}
}

func TestMgmtRemoteStatus_PublicWS(t *testing.T) {
	srv := newTestMgmtServerWithRemote("ws://1.2.3.4:8777")
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/remote/status")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)

	analysis := body["remoteAnalysis"].(map[string]interface{})
	if analysis["isPublicWS"] != true {
		t.Errorf("isPublicWS = %v, want true", analysis["isPublicWS"])
	}
	if analysis["securityLevel"] != "insecure" {
		t.Errorf("securityLevel = %v, want insecure", analysis["securityLevel"])
	}
	if analysis["hostCategory"] != "public" {
		t.Errorf("hostCategory = %v, want public", analysis["hostCategory"])
	}
}

func TestMgmtRemoteStatus_PublicWSS(t *testing.T) {
	srv := newTestMgmtServerWithRemote("wss://my-tunnel.example.com:8777")
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/remote/status")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)

	analysis := body["remoteAnalysis"].(map[string]interface{})
	if analysis["securityLevel"] != "encrypted" {
		t.Errorf("securityLevel = %v, want encrypted", analysis["securityLevel"])
	}
	if analysis["scheme"] != "wss" {
		t.Errorf("scheme = %v, want wss", analysis["scheme"])
	}
}

func TestMgmtRemoteStatus_LANWS(t *testing.T) {
	srv := newTestMgmtServerWithRemote("ws://192.168.1.50:8777")
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/remote/status")
	srv.ServeHTTP(rec, req)

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)

	analysis := body["remoteAnalysis"].(map[string]interface{})
	if analysis["securityLevel"] != "lan" {
		t.Errorf("securityLevel = %v, want lan", analysis["securityLevel"])
	}
	if analysis["isPublicWS"] != false {
		t.Errorf("isPublicWS = %v, want false", analysis["isPublicWS"])
	}
}

// ── classifyRemoteURL 单元测试 ──────────────────────────────────────────────

func TestClassifyRemoteURL_Tailscale(t *testing.T) {
	a := classifyRemoteURL("ws://100.64.0.1:8777")
	if !a.IsTailscaleCGNAT {
		t.Error("100.64.0.1 应被识别为 Tailscale CGNAT")
	}
	if a.SecurityLevel != "tailscale_tunnel" {
		t.Errorf("securityLevel = %s, want tailscale_tunnel", a.SecurityLevel)
	}
}

func TestClassifyRemoteURL_TailscaleUpperBound(t *testing.T) {
	a := classifyRemoteURL("ws://100.127.255.255:8777")
	if !a.IsTailscaleCGNAT {
		t.Error("100.127.255.255 应被识别为 Tailscale CGNAT")
	}
}

func TestClassifyRemoteURL_NotTailscale(t *testing.T) {
	a := classifyRemoteURL("ws://100.63.255.255:8777")
	if a.IsTailscaleCGNAT {
		t.Error("100.63.255.255 不应被识别为 Tailscale CGNAT")
	}
}

func TestClassifyRemoteURL_InvalidURL(t *testing.T) {
	a := classifyRemoteURL("://bad")
	if a.HostCategory != "invalid" {
		t.Errorf("hostCategory = %s, want invalid", a.HostCategory)
	}
}

func TestIsTailscaleCGNAT(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.0", true},
		{"100.64.0.1", true},
		{"100.100.50.20", true},
		{"100.127.255.255", true},
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"1.2.3.4", false},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("无法解析 IP: %s", tt.ip)
		}
		got := isTailscaleCGNAT(ip)
		if got != tt.want {
			t.Errorf("isTailscaleCGNAT(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestMgmtRemoteStatus_RelayDisabled(t *testing.T) {
	srv := newTestMgmtServerWithRemote("")
	srv.cfg.RelayEndpoint = "wss://relay.example.com"
	srv.cfg.RelayRouteID = "route_123"
	srv.cfg.RelayConfigured = true
	srv.cfg.RelayEnabled = false // Disabled
	
	rec := httptest.NewRecorder()
	req := authRequest(http.MethodGet, "/internal/remote/status")
	srv.ServeHTTP(rec, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	relay, ok := body["relay"].(map[string]interface{})
	if !ok {
		t.Fatalf("relay 字段不是 map: %v", body["relay"])
	}
	if relay["enabled"] != false {
		t.Errorf("enabled = %v, want false", relay["enabled"])
	}
	
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, authRequest(http.MethodPost, "/internal/pairing/create"))
	var pBody map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &pBody); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	qrPayload, _ := pBody["qrPayload"].(string)
	if strings.Contains(qrPayload, "relay") {
		t.Errorf("qrPayload = %s, should not contain relay info when disabled", qrPayload)
	}
}
