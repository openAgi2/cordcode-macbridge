package gobridge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// E1 — dispatcher 状态机定向测试（web push §8.4）。
//
// 诚实边界：这些用例用 httptest 服务证明头部契约与状态机转换（TTL/Urgency/Topic、
// accepted/temporary/permanent/expiry 路径、有界重试、非 healthy 关闭）。
// 它们不是 Apple push 服务互操作证据——那是 WP-RESP-1/2/3 样本门（E2）的职责。

type dispatcherHarness struct {
	store     *WebPushStore
	pipeline  *WebPushCandidatePipeline
	server    *httptest.Server
	mu        sync.Mutex
	requests  int32
	cursor    int32
	statusSeq []int
	headers   []http.Header
}

func newDispatcherHarness(t *testing.T, statusSeq ...int) *dispatcherHarness {
	t.Helper()
	h := &dispatcherHarness{statusSeq: statusSeq}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&h.requests, 1)
		h.mu.Lock()
		h.headers = append(h.headers, r.Header.Clone())
		idx := int(atomic.AddInt32(&h.cursor, 1)) - 1
		status := 200
		if idx < len(h.statusSeq) {
			status = h.statusSeq[idx]
		} else if len(h.statusSeq) > 0 {
			status = h.statusSeq[len(h.statusSeq)-1]
		}
		if status == 429 && idx == 0 {
			w.Header().Set("Retry-After", "0") // 无效值 → 走有界退避
		}
		h.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(h.server.Close)

	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	// 真实 P-256 客户端密钥：webpush-go 的 RFC 8291 ECDH 对伪造点会直接失败，
	// 走不到 HTTP——所以这里必须生成有效 keypair。
	clientKey, kerr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if kerr != nil {
		t.Fatalf("client key: %v", kerr)
	}
	record := testSubscriptionRecord(h.server.URL + "/push/dev_disp")
	record.P256dh = base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), clientKey.PublicKey.X, clientKey.PublicKey.Y))
	record.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	if _, err := store.Register("dev_disp", record); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h.store = store
	h.pipeline = NewWebPushCandidatePipeline(store)
	h.pipeline.SetBridgeID("brg_disp")
	return h
}

func newTestDispatcher(h *dispatcherHarness) *WebPushDispatcher {
	return NewWebPushDispatcher(h.store, h.pipeline, WebPushDispatcherConfig{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RetryDelay: 5 * time.Millisecond,
		RetryMax:   2,
	})
}

// deliverSync 直接调用 deliverCandidate 并等待队列清空（避免 worker 时序抖动）。
func (h *dispatcherHarness) deliverSync(t *testing.T, d *WebPushDispatcher, candidate WebPushCandidate) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.deliverCandidate(candidate)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deliverCandidate did not finish (retry loop unbounded?)")
	}
}

func dispatcherCandidate(kind WebPushNotificationKind, key string) WebPushCandidate {
	return WebPushCandidate{
		BridgeID:        "brg_disp",
		BackendID:       "codex",
		SessionID:       "disp-1",
		EventID:         "e1:1",
		Kind:            kind,
		NotificationKey: key,
		AnchorKind:      "turn",
		AnchorID:        "turn-1",
	}
}

func ledgerStatusOf(t *testing.T, store *WebPushStore, key string) (string, bool) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.ledger[WebPushNotificationKeyHash(key)]
	return entry.Status, ok
}

func TestDispatcherTwoxxAccepted(t *testing.T) {
	h := newDispatcherHarness(t, 200)
	d := newTestDispatcher(h)
	key := "codex|disp-1|t1|completed"
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, key))

	status, ok := ledgerStatusOf(t, h.store, key)
	if !ok || status != "accepted" {
		t.Fatalf("ledger = (%q,%v), want accepted", status, ok)
	}
	if got := atomic.LoadInt32(&h.requests); got != 1 {
		t.Fatalf("requests = %d, want 1 (2xx must not retry)", got)
	}
}

func TestDispatcherHeadersPerKind(t *testing.T) {
	cases := []struct {
		kind    WebPushNotificationKind
		ttl     string
		urgency string
	}{
		{WebPushKindCompletion, "3600", "normal"},
		{WebPushKindPermission, "300", "high"},
	}
	for i, tc := range cases {
		h := newDispatcherHarness(t, 200)
		d := newTestDispatcher(h)
		h.deliverSync(t, d, dispatcherCandidate(tc.kind, "codex|disp-1|k"+string(rune('a'+i))+"|completed"))

		h.mu.Lock()
		if len(h.headers) == 0 {
			h.mu.Unlock()
			t.Fatalf("case %s: no request captured", tc.kind)
		}
		header := h.headers[0]
		h.mu.Unlock()
		if got := header.Get("TTL"); got != tc.ttl {
			t.Fatalf("%s TTL = %q, want %q", tc.kind, got, tc.ttl)
		}
		if got := header.Get("Urgency"); got != tc.urgency {
			t.Fatalf("%s Urgency = %q, want %q", tc.kind, got, tc.urgency)
		}
		if topic := header.Get("Topic"); topic == "" || len(topic) > 32 {
			t.Fatalf("%s Topic = %q (must be non-empty, ≤32 chars)", tc.kind, topic)
		}
		// RFC 8291/8292 痕迹：加密头与 VAPID 授权头必须存在。
		if header.Get("Content-Encoding") == "" {
			t.Fatalf("%s missing Content-Encoding", tc.kind)
		}
		auth := header.Get("Authorization")
		if len(auth) < 20 || auth[:4] != "vapid "[:4] && len(auth) < 20 {
			t.Fatalf("%s missing VAPID Authorization header: %q", tc.kind, auth)
		}
	}
}

func TestSessionAggregationKeyStableAndBackendScoped(t *testing.T) {
	first := WebPushCandidate{BackendID: "codex", SessionID: "session-1", NotificationKey: "codex|session-1|turn-1|completed"}
	secondTurn := first
	secondTurn.NotificationKey = "codex|session-1|turn-2|completed"
	otherBackend := first
	otherBackend.BackendID = "dsh-web"
	otherSession := first
	otherSession.SessionID = "session-2"

	got := sessionAggregationKey(first)
	if got == "" || !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(got) {
		t.Fatalf("sessionAggregationKey = %q, want 22 base64url characters", got)
	}
	if next := sessionAggregationKey(secondTurn); next != got {
		t.Fatalf("same backend/session key changed across turns: %q != %q", next, got)
	}
	if next := sessionAggregationKey(otherBackend); next == got {
		t.Fatalf("different backends collided: %q", got)
	}
	if next := sessionAggregationKey(otherSession); next == got {
		t.Fatalf("different sessions collided: %q", got)
	}
	if WebPushNotificationKeyHash(first.NotificationKey) == WebPushNotificationKeyHash(secondTurn.NotificationKey) {
		t.Fatal("per-turn notification ledger identity must remain distinct")
	}
}

func TestDispatcherTopicStablePerSessionAcrossTurns(t *testing.T) {
	h := newDispatcherHarness(t, 200, 200)
	d := newTestDispatcher(h)
	first := dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|turn-1|completed")
	second := dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|turn-2|completed")
	second.EventID = "e1:2"
	second.AnchorID = "turn-2"

	h.deliverSync(t, d, first)
	h.deliverSync(t, d, second)

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.headers) != 2 {
		t.Fatalf("requests = %d, want 2 distinct per-turn deliveries", len(h.headers))
	}
	want := "ccs_" + sessionAggregationKey(first)
	for i, header := range h.headers {
		if got := header.Get("Topic"); got != want {
			t.Fatalf("request %d Topic = %q, want %q", i, got, want)
		}
		if len(header.Get("Topic")) > 32 {
			t.Fatalf("request %d Topic exceeds RFC 8030 limit: %q", i, header.Get("Topic"))
		}
	}
}

func TestDispatcherPayloadTagStablePerSession(t *testing.T) {
	h := newDispatcherHarness(t, 200)
	d := newTestDispatcher(h)
	first := dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|turn-1|completed")
	secondTurn := first
	secondTurn.NotificationKey = "codex|disp-1|turn-2|completed"
	otherSession := first
	otherSession.SessionID = "disp-2"
	otherSession.NotificationKey = "codex|disp-2|turn-1|completed"
	otherBackend := first
	otherBackend.BackendID = "claude-code"
	otherBackend.NotificationKey = "claude-code|disp-1|turn-1|completed"

	tagOf := func(candidate WebPushCandidate) string {
		t.Helper()
		payload, _, _, err := d.buildPayloadTemplate(candidate)
		if err != nil {
			t.Fatalf("buildPayloadTemplate: %v", err)
		}
		return payload.Notification.Tag
	}

	firstTag := tagOf(first)
	// tag 必须与 Topic 共用同一 (backendId, sessionId) 聚合身份：同会话跨 turn 稳定，
	// 跨会话/跨 backend 不碰撞，且满足与 Topic 相同的长度与字母表约束。
	if firstTag != "ccs_"+sessionAggregationKey(first) {
		t.Fatalf("tag = %q, want ccs_+sessionAggregationKey (must share the Topic aggregation identity)", firstTag)
	}
	if !regexp.MustCompile(`^ccs_[A-Za-z0-9_-]{22}$`).MatchString(firstTag) {
		t.Fatalf("tag = %q, want ccs_ prefix + 22 base64url characters", firstTag)
	}
	if len(firstTag) > 32 {
		t.Fatalf("tag = %q exceeds 32 characters", firstTag)
	}
	if next := tagOf(secondTurn); next != firstTag {
		t.Fatalf("same backend/session tag changed across turns: %q != %q", next, firstTag)
	}
	if next := tagOf(otherSession); next == firstTag {
		t.Fatalf("different sessions collided on tag: %q", firstTag)
	}
	if next := tagOf(otherBackend); next == firstTag {
		t.Fatalf("different backends collided on tag: %q", firstTag)
	}
}

func TestDispatcher404PreSampleDoesNotDelete(t *testing.T) {
	// 默认已翻转（WP-RESP-2 已归档，2026-09-19）；此处显式钉住旧门行为，
	// 保证 proven=false 分支不因默认值变化而失去覆盖。
	prev := webPushExpirySemanticsProven
	webPushExpirySemanticsProven = false
	t.Cleanup(func() { webPushExpirySemanticsProven = prev })

	h := newDispatcherHarness(t, 404)
	d := newTestDispatcher(h)
	key := "codex|disp-1|t404|completed"
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, key))

	status, ok := ledgerStatusOf(t, h.store, key)
	if !ok || status != "expiry_unverified" {
		t.Fatalf("ledger = (%q,%v), want expiry_unverified (proven=false 门控行为)", status, ok)
	}
	if h.store.SubscriptionCount() != 1 {
		t.Fatalf("subscription deleted before expiry semantics sample-proven: count = %d", h.store.SubscriptionCount())
	}
}

func TestDispatcher404PostSampleDeletes(t *testing.T) {
	prev := webPushExpirySemanticsProven
	webPushExpirySemanticsProven = true
	t.Cleanup(func() { webPushExpirySemanticsProven = prev })

	h := newDispatcherHarness(t, 404)
	d := newTestDispatcher(h)
	key := "codex|disp-1|t410|completed"
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, key))

	status, ok := ledgerStatusOf(t, h.store, key)
	if !ok || status != "expired" {
		t.Fatalf("ledger = (%q,%v), want expired", status, ok)
	}
	if h.store.SubscriptionCount() != 0 {
		t.Fatalf("expired subscription not cleaned: count = %d", h.store.SubscriptionCount())
	}
}

// 404 清理失败路径（评审 r2§4）：删除持久化失败时 subscription 必须保留
// （store 回滚，内存与磁盘一致），账本记 expiry_cleanup_failed 而不是
// expired——不得把未落盘的删除写成已清理。
func TestDispatcher404CleanupFailureKeepsSubscriptionAndHonestLedger(t *testing.T) {
	prev := webPushExpirySemanticsProven
	webPushExpirySemanticsProven = true
	t.Cleanup(func() { webPushExpirySemanticsProven = prev })

	h := newDispatcherHarness(t, 404)
	// 注册已落盘后破坏持久化：store 目录去写权限 → atomic write 建临时文件失败。
	if err := os.Chmod(h.store.dir, 0o500); err != nil {
		t.Fatalf("chmod store dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.store.dir, 0o700) })

	d := newTestDispatcher(h)
	key := "codex|disp-1|t404f|completed"
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, key))

	status, ok := ledgerStatusOf(t, h.store, key)
	if !ok || status != "expiry_cleanup_failed" {
		t.Fatalf("ledger = (%q,%v), want expiry_cleanup_failed (deletion not persisted must not record expired)", status, ok)
	}
	if h.store.SubscriptionCount() != 1 {
		t.Fatalf("subscription lost despite persist failure: count = %d, want 1 (memory rollback)", h.store.SubscriptionCount())
	}
}

// 撤销设备 fail closed（评审 R4-B1/R5-B1）三阶段验收：清理落盘失败后
// ①同进程下一条 candidate 不向 revoked device 发请求；②重启形状（磁盘重载
// store + 新 dispatcher，存储仍坏）仍不发；③存储恢复后下一次 fan-out 的清理
// 重试完成物理删除，磁盘归零。使用生产授权判定（webPushDeviceAuthorization）。
func TestDispatcherRevokedDeviceFailsClosedAndRetriesCleanup(t *testing.T) {
	h := newDispatcherHarness(t, 200) // 若误投递会 2xx 并被 requests 计数捕获
	devices := NewMemoryDeviceStore()
	devices.AddDevice(TrustedDeviceRecord{
		DeviceID:    "dev_disp",
		DisplayName: "Test",
		Platform:    "ios",
		TokenHash:   "sha256:rvk",
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	})
	if err := devices.RevokeDevice("dev_disp"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	newDispatcher := func(store *WebPushStore) *WebPushDispatcher {
		return NewWebPushDispatcher(store, h.pipeline, WebPushDispatcherConfig{
			HTTPClient:      &http.Client{Timeout: 5 * time.Second},
			RetryDelay:      5 * time.Millisecond,
			RetryMax:        2,
			DeviceAuthorized: webPushDeviceAuthorization(devices, false),
		})
	}

	// 前提形状（R4-B1）：撤销后清理落盘失败 → subscription 回滚进可见集合。
	if err := os.Chmod(h.store.dir, 0o500); err != nil {
		t.Fatalf("chmod store dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.store.dir, 0o700) })
	if err := h.store.DeleteDevice("dev_disp"); err == nil {
		t.Fatal("DeleteDevice succeeded despite unwritable store dir, want persist error")
	}
	if h.store.SubscriptionCount() != 1 {
		t.Fatalf("rollback premise broken: count = %d, want 1", h.store.SubscriptionCount())
	}

	// 阶段 1（同进程，存储仍坏）：不得向 revoked device 发任何请求。
	h.deliverSync(t, newDispatcher(h.store), dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|rv1|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 0 {
		t.Fatalf("revoked device received %d HTTP requests, want 0 (fail closed)", got)
	}

	// 阶段 2（重启形状：磁盘重载 store + 新 dispatcher，存储仍坏）：仍不投递。
	reloaded, err := LoadWebPushStore(h.store.dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	h.deliverSync(t, newDispatcher(reloaded), dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|rv2|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 0 {
		t.Fatalf("revoked device received request after reload: %d, want 0", got)
	}
	if reloaded.SubscriptionCount() != 1 {
		t.Fatalf("subscription must remain while storage is broken: count = %d", reloaded.SubscriptionCount())
	}

	// 阶段 3（存储恢复）：下一次 fan-out 的清理重试完成物理删除。
	if err := os.Chmod(h.store.dir, 0o700); err != nil {
		t.Fatalf("chmod restore: %v", err)
	}
	h.deliverSync(t, newDispatcher(reloaded), dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|rv3|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 0 {
		t.Fatalf("revoked device received request after storage recovery: %d, want 0", got)
	}
	if reloaded.SubscriptionCount() != 0 {
		t.Fatalf("cleanup retry after storage recovery failed: count = %d, want 0", reloaded.SubscriptionCount())
	}
	// 磁盘真相：再次重载为 0（物理删除已落盘）。
	after, err := LoadWebPushStore(h.store.dir)
	if err != nil {
		t.Fatalf("reload after cleanup: %v", err)
	}
	if after.SubscriptionCount() != 0 {
		t.Fatalf("disk still holds revoked subscription after recovery: count = %d", after.SubscriptionCount())
	}
}

// 授权判定的 dispatcher 行为（评审 R5-B1）：健康 store 下的 missing（orphan，
// 含 ReplaceDevice 替换残留）→ 0 请求且订阅被自愈清理；Unknown（查询失败等
// 暂时状态）→ 0 请求但订阅保留（不能因暂时错误删订阅）。
func TestDispatcherAuthorizationMissingOrphanAndUnknown(t *testing.T) {
	// missing / orphan：生产授权判定 over 空（健康）store。
	h := newDispatcherHarness(t, 200)
	d := NewWebPushDispatcher(h.store, h.pipeline, WebPushDispatcherConfig{
		HTTPClient:      &http.Client{Timeout: 5 * time.Second},
		RetryDelay:      5 * time.Millisecond,
		RetryMax:        2,
		DeviceAuthorized: webPushDeviceAuthorization(NewMemoryDeviceStore(), false),
	})
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|orph|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 0 {
		t.Fatalf("orphan subscription received %d HTTP requests, want 0", got)
	}
	if h.store.SubscriptionCount() != 0 {
		t.Fatalf("orphan subscription must be self-healed (deleted): count = %d", h.store.SubscriptionCount())
	}

	// Unknown：查询失败的 store——拒绝投递但不清理。
	h2 := newDispatcherHarness(t, 200)
	d2 := NewWebPushDispatcher(h2.store, h2.pipeline, WebPushDispatcherConfig{
		HTTPClient:      &http.Client{Timeout: 5 * time.Second},
		RetryDelay:      5 * time.Millisecond,
		RetryMax:        2,
		DeviceAuthorized: webPushDeviceAuthorization(&failingLookupDeviceStore{}, false),
	})
	h2.deliverSync(t, d2, dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|unk|completed"))
	if got := atomic.LoadInt32(&h2.requests); got != 0 {
		t.Fatalf("unknown-authorization subscription received %d HTTP requests, want 0", got)
	}
	if h2.store.SubscriptionCount() != 1 {
		t.Fatalf("subscription must be retained on Unknown (transient lookup failure): count = %d", h2.store.SubscriptionCount())
	}
}

// 真实 FileDeviceStore + WebPushStore 双重重载的生产形状测试（评审 R5-B1）：
// 持久 revoke 后同时从 devices.json / web-push-subscriptions.json 重载，revoked
// 订阅仍 0 请求（跨重启 fail closed），active 设备正常投递；存储恢复后完成
// 物理清理。
func TestDispatcherDeviceAuthorizationFileStoreReload(t *testing.T) {
	h := newDispatcherHarness(t, 200) // dev_disp 的订阅（endpoint /push/dev_disp）

	// 第二个 active 设备的订阅（endpoint /push/dev_active2）。
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	recActive := testSubscriptionRecord(h.server.URL + "/push/dev_active2")
	recActive.P256dh = base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), clientKey.PublicKey.X, clientKey.PublicKey.Y))
	recActive.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	if _, err := h.store.Register("dev_active2", recActive); err != nil {
		t.Fatalf("Register dev_active2: %v", err)
	}

	// 真实 FileDeviceStore：dev_disp 撤销（持久化到 devices.json），dev_active2 active。
	devicesPath := filepath.Join(t.TempDir(), "devices.json")
	fileDevices, err := NewFileDeviceStore(devicesPath)
	if err != nil {
		t.Fatalf("NewFileDeviceStore: %v", err)
	}
	addDevice := func(deviceID string) {
		t.Helper()
		if err := fileDevices.AddDevice(TrustedDeviceRecord{
			DeviceID:    deviceID,
			DisplayName: "Test",
			Platform:    "ios",
			TokenHash:   "sha256:" + deviceID,
			CreatedAt:   time.Now(),
			LastSeenAt:  time.Now(),
		}); err != nil {
			t.Fatalf("AddDevice %s: %v", deviceID, err)
		}
	}
	addDevice("dev_disp")
	addDevice("dev_active2")
	if err := fileDevices.RevokeDevice("dev_disp"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	newDispatcher := func(store *WebPushStore, devices TrustedDeviceStore) *WebPushDispatcher {
		return NewWebPushDispatcher(store, h.pipeline, WebPushDispatcherConfig{
			HTTPClient:      &http.Client{Timeout: 5 * time.Second},
			RetryDelay:      5 * time.Millisecond,
			RetryMax:        2,
			DeviceAuthorized: webPushDeviceAuthorization(devices, false),
		})
	}

	// 阶段 1（同进程，存储破坏）：revoked 0 请求、active 投递 1 次；revoked
	// 订阅因清理重试失败而保留（供阶段 2 重载后继续验证拒绝）。
	if err := os.Chmod(h.store.dir, 0o500); err != nil {
		t.Fatalf("chmod store dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.store.dir, 0o700) })
	h.deliverSync(t, newDispatcher(h.store, fileDevices), dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|fr1|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 1 {
		t.Fatalf("requests = %d, want exactly 1 (active only; revoked must be denied)", got)
	}
	if h.store.SubscriptionCount() != 2 {
		t.Fatalf("subscriptions = %d, want 2 (revoked retained while storage broken)", h.store.SubscriptionCount())
	}

	// 阶段 2（重启形状：devices.json + web-push-subscriptions.json 双重重载，
	// 存储仍坏）：revoked 订阅从磁盘回来仍被拒，active 继续投递。
	reloadedDevices, err := NewFileDeviceStore(devicesPath)
	if err != nil {
		t.Fatalf("reload devices: %v", err)
	}
	reloadedPush, err := LoadWebPushStore(h.store.dir)
	if err != nil {
		t.Fatalf("reload push store: %v", err)
	}
	h.deliverSync(t, newDispatcher(reloadedPush, reloadedDevices), dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|fr2|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 2 {
		t.Fatalf("requests after reload = %d, want 2 (active delivers again; revoked stays denied across real reload)", got)
	}
	if reloadedPush.SubscriptionCount() != 2 {
		t.Fatalf("subscriptions after reload = %d, want 2 (cleanup retry still failing)", reloadedPush.SubscriptionCount())
	}

	// 阶段 3（存储恢复）：下一次 fan-out 完成 revoked 订阅的物理清理。
	if err := os.Chmod(h.store.dir, 0o700); err != nil {
		t.Fatalf("chmod restore: %v", err)
	}
	h.deliverSync(t, newDispatcher(reloadedPush, reloadedDevices), dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|fr3|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 3 {
		t.Fatalf("requests after recovery = %d, want 3 (active only)", got)
	}
	if reloadedPush.SubscriptionCount() != 1 {
		t.Fatalf("subscriptions after recovery = %d, want 1 (revoked physically deleted; active retained)", reloadedPush.SubscriptionCount())
	}
	// 磁盘真相：再次重载只剩 active。
	after, err := LoadWebPushStore(h.store.dir)
	if err != nil {
		t.Fatalf("reload after cleanup: %v", err)
	}
	if after.SubscriptionCount() != 1 {
		t.Fatalf("disk after recovery = %d subscriptions, want 1 (active only)", after.SubscriptionCount())
	}
}

// webPushDeviceAuthorization 判定单元行为（评审 R5-B1）：只有显式 active 才
// 允许投递——lookup 错误 → Unknown（拒绝但不清理）；健康 store 下 missing
//（orphan）与 revoked → Denied（拒绝并自愈清理）；degraded store（devices.json
// 加载失败退回空 store）下 missing → Unknown（拒绝但不删订阅）。
type failingLookupDeviceStore struct {
	MemoryDeviceStore
}

func (f *failingLookupDeviceStore) LookupByDeviceID(string) (*TrustedDeviceRecord, error) {
	return nil, fmt.Errorf("lookup failed")
}

func TestWebPushDeviceAuthorizationDecisions(t *testing.T) {
	auth := webPushDeviceAuthorization(&failingLookupDeviceStore{}, false)
	if got := auth("dev_x"); got != WebPushDeviceUnknown {
		t.Fatalf("lookup error decision = %v, want Unknown", got)
	}

	devices := NewMemoryDeviceStore()
	devices.AddDevice(TrustedDeviceRecord{
		DeviceID:    "dev_a",
		DisplayName: "Test",
		Platform:    "ios",
		TokenHash:   "sha256:a",
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	})
	auth = webPushDeviceAuthorization(devices, false)
	if got := auth("dev_a"); got != WebPushDeviceActive {
		t.Fatalf("active device decision = %v, want Active", got)
	}
	if err := devices.RevokeDevice("dev_a"); err != nil {
		t.Fatal(err)
	}
	if got := auth("dev_a"); got != WebPushDeviceDenied {
		t.Fatalf("revoked device decision = %v, want Denied", got)
	}
	if got := auth("dev_missing"); got != WebPushDeviceDenied {
		t.Fatalf("missing record on healthy store decision = %v, want Denied (orphan)", got)
	}

	// degraded store：记录缺失是暂时状态（devices.json 加载失败），不删订阅。
	auth = webPushDeviceAuthorization(NewMemoryDeviceStore(), true)
	if got := auth("dev_any"); got != WebPushDeviceUnknown {
		t.Fatalf("missing record on degraded store decision = %v, want Unknown", got)
	}
}

func TestDispatcher5xxBoundedRetryThenTemporary(t *testing.T) {
	h := newDispatcherHarness(t, 503, 503, 503, 503)
	d := newTestDispatcher(h)
	key := "codex|disp-1|t5xx|completed"
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, key))

	status, ok := ledgerStatusOf(t, h.store, key)
	if !ok || status != "temporary_failed" {
		t.Fatalf("ledger = (%q,%v), want temporary_failed", status, ok)
	}
	// RetryMax=2 → 初次 + 2 次重试 = 3 次，不得无界重试。
	if got := atomic.LoadInt32(&h.requests); got != 3 {
		t.Fatalf("requests = %d, want 3 (bounded by RetryMax)", got)
	}
}

func TestDispatcher400PermanentNoKeyDeletion(t *testing.T) {
	h := newDispatcherHarness(t, 401)
	d := newTestDispatcher(h)
	key := "codex|disp-1|t400|completed"
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, key))

	status, ok := ledgerStatusOf(t, h.store, key)
	if !ok || status != "permanent_failed" {
		t.Fatalf("ledger = (%q,%v), want permanent_failed", status, ok)
	}
	if h.store.SubscriptionCount() != 1 {
		t.Fatalf("permanent failure must not delete subscription/key: count = %d", h.store.SubscriptionCount())
	}
	if h.store.VapidPrivateKey() == nil {
		t.Fatal("VAPID key must survive a 4xx")
	}
	if got := atomic.LoadInt32(&h.requests); got != 1 {
		t.Fatalf("requests = %d, want 1 (4xx no retry)", got)
	}
}

func TestDispatcherNoSubscriptionsNoRequests(t *testing.T) {
	h := newDispatcherHarness(t, 200)
	// 删掉唯一 subscription：candidate 不得触发任何 HTTP。
	if _, err := h.store.Unregister("dev_disp", ""); err != nil {
		t.Fatal(err)
	}
	d := newTestDispatcher(h)
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|none|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 0 {
		t.Fatalf("requests = %d, want 0", got)
	}
}

func TestDispatcherMisconfiguredStoreFailsClosed(t *testing.T) {
	h := newDispatcherHarness(t, 200)
	// 把 store 打成 misconfigured（vapid 损坏后重载），dispatcher 仍指向旧 store 对象——
	// 这里验证 VapidPrivateKey() == nil 时（Reset 后 status 非 healthy）不发请求。
	h.store.status = WebPushStoreMisconfigured
	d := newTestDispatcher(h)
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|mis|completed"))
	if got := atomic.LoadInt32(&h.requests); got != 0 {
		t.Fatalf("requests = %d, want 0 (misconfigured store must fail closed)", got)
	}
}

func TestDispatcherWorkerConsumesPipelineQueue(t *testing.T) {
	h := newDispatcherHarness(t, 200)
	d := newTestDispatcher(h)
	d.Start()
	defer d.Stop()
	h.pipeline.Ingest(dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|w|completed"), ProjectionIngestApplied)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&h.requests) == 1 {
			status, ok := ledgerStatusOf(t, h.store, "codex|disp-1|w|completed")
			if ok && status == "accepted" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not consume the queued candidate in time")
}

func TestRetryAfterParsing(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	if _, ok := retryAfter(resp); ok {
		t.Fatal("missing header must be invalid")
	}
	resp.Header.Set("Retry-After", "abc")
	if _, ok := retryAfter(resp); ok {
		t.Fatal("non-numeric must be invalid")
	}
	resp.Header.Set("Retry-After", "0")
	if _, ok := retryAfter(resp); ok {
		t.Fatal("zero must be invalid")
	}
	resp.Header.Set("Retry-After", "12")
	if delay, ok := retryAfter(resp); !ok || delay != 12*time.Second {
		t.Fatalf("delay = %v", delay)
	}
}

// webpush.Options 满足我们注入 HTTPClient 的需求（编译期确认接口形状）。
var _ webpush.HTTPClient = (*http.Client)(nil)
