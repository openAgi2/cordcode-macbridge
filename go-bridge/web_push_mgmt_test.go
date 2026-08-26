package gobridge

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setGlobalWebPushStoreForTest 设置/恢复进程级 store 引用。
func setGlobalWebPushStoreForTest(t *testing.T, store *WebPushStore) {
	t.Helper()
	prev := globalWebPushStore
	globalWebPushStore = store
	t.Cleanup(func() { globalWebPushStore = prev })
}

func TestWebPushMgmtStatusHealthy(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := store.Register("dev_a", testSubscriptionRecord("https://web.push.apple.com/a")); err != nil {
		t.Fatalf("register: %v", err)
	}
	setGlobalWebPushStoreForTest(t, store)

	s := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, authRequest("GET", "/internal/webpush/status"))
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp WebPushStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "healthy" {
		t.Fatalf("status = %q", resp.Status)
	}
	if resp.SubscriptionCount != 1 {
		t.Fatalf("count = %d", resp.SubscriptionCount)
	}
	if len(resp.VapidKeyFingerprint) != 16 {
		t.Fatalf("fingerprint = %q", resp.VapidKeyFingerprint)
	}
	// 指纹必须是公钥 hash 的前缀（可核对、不含私钥）。
	pub := store.VapidPublicKey()
	if !strings.HasPrefix(WebPushNotificationKeyHash(pub), resp.VapidKeyFingerprint) {
		t.Fatal("fingerprint does not match public key hash prefix")
	}
}

func TestWebPushMgmtStatusMisconfiguredNoKeyLeak(t *testing.T) {
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
	setGlobalWebPushStoreForTest(t, broken)
	_ = store

	s := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, authRequest("GET", "/internal/webpush/status"))
	var resp WebPushStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "misconfigured" {
		t.Fatalf("status = %q", resp.Status)
	}
	if resp.VapidKeyFingerprint != "" {
		t.Fatalf("misconfigured must not expose fingerprint, got %q", resp.VapidKeyFingerprint)
	}
}

func TestWebPushMgmtStatusUnconfiguredWithoutStore(t *testing.T) {
	setGlobalWebPushStoreForTest(t, nil)
	s := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, authRequest("GET", "/internal/webpush/status"))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var resp WebPushStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "unconfigured" || resp.SubscriptionCount != 0 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestWebPushMgmtResetFlow(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := store.Register("dev_a", testSubscriptionRecord("https://web.push.apple.com/a")); err != nil {
		t.Fatalf("register: %v", err)
	}
	oldFingerprint := WebPushNotificationKeyHash(store.VapidPublicKey())[:16]
	setGlobalWebPushStoreForTest(t, store)

	s := newTestMgmtServer(nil)

	// 重置成功：报告删除数量、新 key 指纹、healthy 状态。
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, authRequest("POST", "/internal/webpush/reset"))
	if rec.Code != 200 {
		t.Fatalf("reset code = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["reset"] != true {
		t.Fatalf("resp = %v", resp)
	}
	if resp["removedSubscriptions"] != float64(1) {
		t.Fatalf("removed = %v", resp["removedSubscriptions"])
	}
	if resp["status"] != "healthy" {
		t.Fatalf("status = %v", resp["status"])
	}
	newFingerprint, _ := resp["vapidKeyFingerprint"].(string)
	if newFingerprint == "" || newFingerprint == oldFingerprint {
		t.Fatalf("fingerprint after reset = %q (old %q)", newFingerprint, oldFingerprint)
	}
	if store.SubscriptionCount() != 0 {
		t.Fatalf("count after reset = %d", store.SubscriptionCount())
	}
	if at, lastErr := store.LastResetInfo(); at == 0 || lastErr != "" {
		t.Fatalf("lastReset = (%d, %q)", at, lastErr)
	}

	// 重置后 status 端点反映新状态。
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, authRequest("GET", "/internal/webpush/status"))
	var status WebPushStatusResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Status != "healthy" || status.SubscriptionCount != 0 || status.LastResetAtMillis == 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestWebPushMgmtResetWithoutStoreFails(t *testing.T) {
	setGlobalWebPushStoreForTest(t, nil)
	s := newTestMgmtServer(nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, authRequest("POST", "/internal/webpush/reset"))
	if rec.Code != 500 {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "web_push_not_configured") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestWebPushMgmtRequiresAuth(t *testing.T) {
	setGlobalWebPushStoreForTest(t, nil)
	s := newTestMgmtServer(nil)
	for _, path := range []string{"/internal/webpush/status", "/internal/webpush/reset"} {
		rec := httptest.NewRecorder()
		method := "GET"
		if strings.HasSuffix(path, "/reset") {
			method = "POST"
		}
		s.ServeHTTP(rec, noAuthRequest(method, path))
		if rec.Code == 200 {
			t.Fatalf("%s accessible without auth", path)
		}
	}
}
