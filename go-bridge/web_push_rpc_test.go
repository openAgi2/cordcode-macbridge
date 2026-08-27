package gobridge

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── hello 协商（ApplyWebPushHelloProfile：direct 与 relay 共用同一 helper）─────────

func webPushHello(declares bool) *HelloMessage {
	hello := &HelloMessage{Type: "hello"}
	if declares {
		hello.Capabilities = []string{WebPushCapability}
	}
	return hello
}

func TestApplyWebPushHelloProfileHealthyEcho(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	ack := &HelloAckMessage{Ok: true, Capabilities: map[string]bool{}}
	ApplyWebPushHelloProfile(ack, webPushHello(true), store)
	if !ack.Capabilities[WebPushCapability] {
		t.Fatalf("capabilities[%s] = false, want true", WebPushCapability)
	}
	if ack.WebPush == nil {
		t.Fatal("webPush profile missing")
	}
	if ack.WebPush.VapidPublicKey != store.VapidPublicKey() {
		t.Fatal("profile public key differs from store")
	}
	if ack.WebPush.Status != "" {
		t.Fatalf("healthy profile should not carry status, got %q", ack.WebPush.Status)
	}
	if ack.WebPush.SchemaVersion != WebPushSchemaVersion {
		t.Fatalf("schemaVersion = %d", ack.WebPush.SchemaVersion)
	}
}

func TestApplyWebPushHelloProfileMisconfiguredNoKey(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if werr := os.WriteFile(filepath.Join(dir, webPushVapidFile), []byte("{broken"), 0o600); werr != nil {
		t.Fatalf("corrupt vapid: %v", werr)
	}
	broken, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if status, _ := broken.Status(); status != WebPushStoreMisconfigured {
		t.Fatalf("setup: status = %q", status)
	}

	ack := &HelloAckMessage{Ok: true, Capabilities: map[string]bool{}}
	ApplyWebPushHelloProfile(ack, webPushHello(true), broken)
	if ack.Capabilities[WebPushCapability] {
		t.Fatal("misconfigured store must not echo capability")
	}
	if ack.WebPush == nil {
		t.Fatal("misconfigured should still carry diagnostic profile")
	}
	if ack.WebPush.VapidPublicKey != "" {
		t.Fatal("misconfigured profile leaked a public key")
	}
	if ack.WebPush.Status != WebPushStatusMisconfigured {
		t.Fatalf("status = %q", ack.WebPush.Status)
	}
	if ack.WebPush.VapidPublicKey == store.VapidPublicKey() {
		t.Fatal("stale key reference")
	}
}

func TestApplyWebPushHelloProfileUndeclaredClientGetsNothing(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	ack := &HelloAckMessage{Ok: true, Capabilities: map[string]bool{}}
	ApplyWebPushHelloProfile(ack, webPushHello(false), store)
	if _, present := ack.Capabilities[WebPushCapability]; present {
		t.Fatal("undeclared client got capability echo")
	}
	if ack.WebPush != nil {
		t.Fatal("undeclared client got webPush profile")
	}
}

func TestApplyWebPushHelloProfileNilStore(t *testing.T) {
	ack := &HelloAckMessage{Ok: true, Capabilities: map[string]bool{}}
	ApplyWebPushHelloProfile(ack, webPushHello(true), nil)
	if _, present := ack.Capabilities[WebPushCapability]; present {
		t.Fatal("nil store must not echo capability")
	}
	if ack.WebPush != nil {
		t.Fatal("nil store must not emit profile")
	}
}

func TestHelloAckWebPushJSONShape(t *testing.T) {
	ack := &HelloAckMessage{Type: "hello_ack", Ok: true}
	raw, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "webPush") {
		t.Fatalf("nil profile must be omitted, got %s", raw)
	}
	ack.WebPush = &WebPushHelloProfile{SchemaVersion: 1, VapidPublicKey: "k"}
	raw, _ = json.Marshal(ack)
	var decoded map[string]interface{}
	if json.Unmarshal(raw, &decoded) != nil {
		t.Fatal("unmarshal")
	}
	profile, ok := decoded["webPush"].(map[string]interface{})
	if !ok {
		t.Fatalf("webPush shape wrong: %s", raw)
	}
	if profile["schemaVersion"] != float64(1) || profile["vapidPublicKey"] != "k" {
		t.Fatalf("webPush fields wrong: %s", raw)
	}
	if _, hasStatus := profile["status"]; hasStatus {
		t.Fatalf("healthy profile must omit status: %s", raw)
	}
}

// ── RPC dispatch（handleWebPushRPC）───────────────────────────────────────────

// webPushCaptureConn 捕获 SendResult 的最后一条 data/error。
type webPushCaptureConn struct {
	relayBroadcastCaptureConn
	result   interface{}
	wireErr  *WireError
	deviceID string
}

func (c *webPushCaptureConn) AuthedDevice() *TrustedDeviceRecord {
	if c.deviceID == "" {
		return nil
	}
	return &TrustedDeviceRecord{DeviceID: c.deviceID}
}

func (c *webPushCaptureConn) SendResult(_ string, data interface{}, err *WireError) {
	c.result = data
	c.wireErr = err
}

func newWebPushRPCHarness(t *testing.T) (*Handlers, *WebPushStore, *webPushCaptureConn) {
	t.Helper()
	h := NewHandlers()
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	h.SetWebPushStore(store)
	conn := &webPushCaptureConn{deviceID: "dev_push_1"}
	return h, store, conn
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func validRegisterParams(t *testing.T, store *WebPushStore) json.RawMessage {
	t.Helper()
	params := map[string]interface{}{
		"schemaVersion":        1,
		"platform":             "ios-pwa",
		"applicationServerKey": store.VapidPublicKey(),
		"subscription": map[string]interface{}{
			"endpoint":       "https://web.push.apple.com/X1BA/dev_push_1",
			"expirationTime": nil,
			"keys": map[string]string{
				"p256dh": base64.RawURLEncoding.EncodeToString(repeatByte(0x04, 65)),
				"auth":   base64.RawURLEncoding.EncodeToString(repeatByte(0x07, 16)),
			},
		},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func decodeResult(t *testing.T, conn *webPushCaptureConn, out interface{}) {
	t.Helper()
	if conn.wireErr != nil {
		t.Fatalf("unexpected wire error: %+v", conn.wireErr)
	}
	if conn.result == nil {
		t.Fatal("no result sent")
	}
	raw, _ := json.Marshal(conn.result)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("unmarshal result %s: %v", raw, err)
	}
}

func TestWebPushRegisterRPCStoresAuthenticatedDevice(t *testing.T) {
	h, store, conn := newWebPushRPCHarness(t)
	msg := WireMessage{
		Type:      "request",
		RequestID: "req_wp_1",
		BackendID: "codex",
		Method:    WebPushMethodRegister,
		Params:    validRegisterParams(t, store),
	}
	if !h.handleWebPushRPC(conn, msg) {
		t.Fatal("handleWebPushRPC did not claim the method")
	}

	var data RegisterPushSubscriptionResult
	decodeResult(t, conn, &data)
	if !strings.HasPrefix(data.SubscriptionID, "wps_") || len(data.SubscriptionID) != 4+webPushSubscriptionIDLen {
		t.Fatalf("subscriptionId = %q", data.SubscriptionID)
	}
	if data.RegisteredAtMillis <= 0 {
		t.Fatalf("registeredAtMillis = %d", data.RegisteredAtMillis)
	}
	if store.SubscriptionCount() != 1 {
		t.Fatalf("store count = %d", store.SubscriptionCount())
	}
	for _, r := range store.Subscriptions() {
		if r.DeviceID != "dev_push_1" {
			t.Fatalf("record deviceID = %q, want authenticated device", r.DeviceID)
		}
	}
}

func TestWebPushRegisterRPCRequiresAuth(t *testing.T) {
	h, _, conn := newWebPushRPCHarness(t)
	conn.deviceID = ""
	msg := WireMessage{Type: "request", RequestID: "req_wp_auth", BackendID: "codex", Method: WebPushMethodRegister, Params: json.RawMessage(`{}`)}
	h.handleWebPushRPC(conn, msg)
	if conn.wireErr == nil || conn.wireErr.Code != "auth.required" {
		t.Fatalf("err = %+v, want auth.required", conn.wireErr)
	}
}

func TestWebPushRegisterRPCNilStoreUnsupported(t *testing.T) {
	h := NewHandlers()
	conn := &webPushCaptureConn{deviceID: "dev_push_1"}
	msg := WireMessage{Type: "request", RequestID: "req_wp_nil", BackendID: "codex", Method: WebPushMethodRegister, Params: json.RawMessage(`{}`)}
	h.handleWebPushRPC(conn, msg)
	if conn.wireErr == nil || conn.wireErr.Code != WebPushErrUnsupported {
		t.Fatalf("err = %+v, want %s", conn.wireErr, WebPushErrUnsupported)
	}
}

func TestWebPushRegisterRPCMisconfiguredUnsupported(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if werr := os.WriteFile(filepath.Join(dir, webPushVapidFile), []byte("{broken"), 0o600); werr != nil {
		t.Fatalf("corrupt: %v", werr)
	}
	broken, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if status, _ := broken.Status(); status != WebPushStoreMisconfigured {
		t.Fatalf("setup status = %q", status)
	}
	h := NewHandlers()
	h.SetWebPushStore(broken)
	conn := &webPushCaptureConn{deviceID: "dev_push_1"}

	msg := WireMessage{Type: "request", RequestID: "req_wp_mis", BackendID: "codex", Method: WebPushMethodRegister, Params: validRegisterParams(t, store)}
	h.handleWebPushRPC(conn, msg)
	if conn.wireErr == nil || conn.wireErr.Code != WebPushErrUnsupported {
		t.Fatalf("err = %+v, want %s", conn.wireErr, WebPushErrUnsupported)
	}
	if conn.wireErr != nil && conn.wireErr.Retryable != nil && *conn.wireErr.Retryable {
		t.Fatal("unsupported must not be retryable")
	}
}

func TestWebPushRegisterRPCVapidMismatch(t *testing.T) {
	h, store, conn := newWebPushRPCHarness(t)
	// 用另一个 bridge 的 key 注册 → vapid_key_mismatch。
	other, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("load other: %v", err)
	}
	msg := WireMessage{Type: "request", RequestID: "req_wp_mis_key", BackendID: "codex", Method: WebPushMethodRegister, Params: validRegisterParams(t, other)}
	h.handleWebPushRPC(conn, msg)
	if conn.wireErr == nil || conn.wireErr.Code != WebPushErrVapidKeyMismatch {
		t.Fatalf("err = %+v, want %s", conn.wireErr, WebPushErrVapidKeyMismatch)
	}
	if store.SubscriptionCount() != 0 {
		t.Fatal("mismatched register must not write store")
	}
}

func TestWebPushUnregisterRPCIdempotent(t *testing.T) {
	h, store, conn := newWebPushRPCHarness(t)
	h.handleWebPushRPC(conn, WireMessage{Type: "request", RequestID: "req_wp_r1", BackendID: "codex", Method: WebPushMethodRegister, Params: validRegisterParams(t, store)})
	var data RegisterPushSubscriptionResult
	decodeResult(t, conn, &data)

	unreg := func(id string) *UnregisterPushSubscriptionResult {
		params, _ := json.Marshal(map[string]interface{}{"schemaVersion": 1, "subscriptionId": id})
		msg := WireMessage{Type: "request", RequestID: "req_wp_u1", BackendID: "codex", Method: WebPushMethodUnregister, Params: params}
		h.handleWebPushRPC(conn, msg)
		var out UnregisterPushSubscriptionResult
		decodeResult(t, conn, &out)
		return &out
	}
	if got := unreg(data.SubscriptionID); !got.Removed {
		t.Fatalf("first unregister removed = false, want true")
	}
	if got := unreg(data.SubscriptionID); got.Removed {
		t.Fatalf("second unregister removed = true, want false (idempotent)")
	}
	if store.SubscriptionCount() != 0 {
		t.Fatalf("count = %d", store.SubscriptionCount())
	}
}

func TestWebPushUnregisterWorksWhenMisconfigured(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	h := NewHandlers()
	h.SetWebPushStore(store)
	conn := &webPushCaptureConn{deviceID: "dev_push_1"}

	h.handleWebPushRPC(conn, WireMessage{Type: "request", RequestID: "r1", BackendID: "codex", Method: WebPushMethodRegister, Params: validRegisterParams(t, store)})
	if conn.wireErr != nil {
		t.Fatalf("register: %+v", conn.wireErr)
	}

	if werr := os.WriteFile(filepath.Join(dir, webPushVapidFile), []byte("{broken"), 0o600); werr != nil {
		t.Fatalf("corrupt: %v", werr)
	}
	broken, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	h.SetWebPushStore(broken)

	// misconfigured 下 unregister 必须仍可达（恢复路径不依赖私钥）。
	params, _ := json.Marshal(map[string]interface{}{"schemaVersion": 1, "subscriptionId": "wps_0123456789abcdef"})
	h.handleWebPushRPC(conn, WireMessage{Type: "request", RequestID: "r2", BackendID: "codex", Method: WebPushMethodUnregister, Params: params})
	if conn.wireErr != nil {
		t.Fatalf("unregister under misconfigured: %+v", conn.wireErr)
	}
	var out UnregisterPushSubscriptionResult
	decodeResult(t, conn, &out)
}

func TestWebPushRegisterRPCDeviceIsolation(t *testing.T) {
	h, store, conn := newWebPushRPCHarness(t)
	h.handleWebPushRPC(conn, WireMessage{Type: "request", RequestID: "r1", BackendID: "codex", Method: WebPushMethodRegister, Params: validRegisterParams(t, store)})
	if conn.wireErr != nil {
		t.Fatalf("register: %+v", conn.wireErr)
	}

	// 另一 device 用同一个 endpoint 注册：各有各的记录（upsert key = deviceId）。
	other := &webPushCaptureConn{deviceID: "dev_push_2"}
	h.handleWebPushRPC(other, WireMessage{Type: "request", RequestID: "r2", BackendID: "codex", Method: WebPushMethodRegister, Params: validRegisterParams(t, store)})
	if other.wireErr != nil {
		t.Fatalf("register 2: %+v", other.wireErr)
	}
	if store.SubscriptionCount() != 2 {
		t.Fatalf("count = %d, want 2", store.SubscriptionCount())
	}
	ids := map[string]bool{}
	for _, r := range store.Subscriptions() {
		ids[r.DeviceID] = true
	}
	if !ids["dev_push_1"] || !ids["dev_push_2"] {
		t.Fatalf("device records = %v", ids)
	}

	// 撤销联动（management 路径调用同一 store API）。
	if err := store.DeleteDevice("dev_push_1"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if store.SubscriptionCount() != 1 {
		t.Fatalf("count after revoke = %d, want 1", store.SubscriptionCount())
	}
}

// ── scope 表守卫──────────────────────────────────────────────────────────────

func TestWebPushScopeTableEntries(t *testing.T) {
	if got := rpcScopeTable[WebPushMethodRegister]; got != ScopeWebPushManage {
		t.Fatalf("register scope = %q", got)
	}
	if got := rpcScopeTable[WebPushMethodUnregister]; got != ScopeWebPushManage {
		t.Fatalf("unregister scope = %q", got)
	}
	if got := scopeForMethod(WebPushMethodRegister); got != WebPushScope {
		t.Fatalf("scopeForMethod = %q, want %s", got, WebPushScope)
	}
	found := false
	for _, s := range DefaultGrantedScopes() {
		if s == ScopeWebPushManage {
			found = true
		}
	}
	if !found {
		t.Fatal("web_push.manage missing from DefaultGrantedScopes")
	}
}

func TestWebPushScopeDeniedForRestrictedDevice(t *testing.T) {
	device := &TrustedDeviceRecord{DeviceID: "dev_limited", GrantedScopes: []string{ScopeSessionRead}}
	if deviceHasScope(device, ScopeWebPushManage) {
		t.Fatal("restricted device must lack web_push.manage")
	}
	full := &TrustedDeviceRecord{DeviceID: "dev_full"}
	if !deviceHasScope(full, ScopeWebPushManage) {
		t.Fatal("nil GrantedScopes (legacy record) must default to all scopes")
	}
}
