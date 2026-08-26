package gobridge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
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

func TestDispatcher404PreSampleDoesNotDelete(t *testing.T) {
	h := newDispatcherHarness(t, 404)
	d := newTestDispatcher(h)
	key := "codex|disp-1|t404|completed"
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, key))

	status, ok := ledgerStatusOf(t, h.store, key)
	if !ok || status != "expiry_unverified" {
		t.Fatalf("ledger = (%q,%v), want expiry_unverified (WP-RESP-2 未归档)", status, ok)
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
