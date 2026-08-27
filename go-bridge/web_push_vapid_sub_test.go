package gobridge

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
	"strings"
	"testing"
)

// 2026-08-27 生产 403 回归（owner iPhone 真机）：Apple 推送端点对全部 dispatch 返回
// 403 permanent_4xx，同一时刻 Google/FCM 端点 201。根因是 VAPID JWT sub 被拼成
// "mailto:mailto:noreply@byteseek.uk"——webpush-go v1.3.0 在 vapid.go:78 自行加
// "mailto:" 前缀，而配置值又带了一次前缀。Apple 按 RFC 8292 严格校验 sub，
// Google 宽容。本回归解出真实 Authorization JWT 的 sub 声明做不变式断言。

func decodeVapidSubClaim(t *testing.T, authHeader string) string {
	t.Helper()
	// Authorization: vapid t=<base64url JWT>, k=<base64url pubkey>
	parts := strings.Fields(authHeader)
	if len(parts) < 2 || parts[0] != "vapid" {
		t.Fatalf("Authorization = %q, want 'vapid t=…'", authHeader)
	}
	var jwt string
	for _, kv := range strings.Split(strings.Join(parts[1:], ""), ",") {
		if strings.HasPrefix(kv, "t=") {
			jwt = strings.TrimPrefix(kv, "t=")
		}
	}
	if jwt == "" {
		t.Fatalf("Authorization = %q has no t=<jwt> token", authHeader)
	}
	segs := strings.Split(jwt, ".")
	if len(segs) != 3 {
		t.Fatalf("JWT segments = %d, want 3", len(segs))
	}
	payload, err := base64.RawURLEncoding.DecodeString(segs[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims struct {
		Sub string `json:"sub"`
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims.Sub
}

func TestDispatcherVAPIDSubSingleMailtoPrefix(t *testing.T) {
	h := newDispatcherHarness(t, 201)
	d := newTestDispatcher(h)
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, "codex|vapid-1|tv1|completed"))

	h.mu.Lock()
	if len(h.headers) == 0 {
		h.mu.Unlock()
		t.Fatal("no request captured")
	}
	auth := h.headers[0].Get("Authorization")
	h.mu.Unlock()

	sub := decodeVapidSubClaim(t, auth)
	if strings.Count(sub, "mailto:") != 1 || !strings.HasPrefix(sub, "mailto:") {
		t.Fatalf("VAPID sub = %q — must be exactly one mailto: prefix (Apple rejects mailto:mailto:… with 403)", sub)
	}
	if sub != "mailto:"+webPushDefaultSubscriber {
		t.Fatalf("VAPID sub = %q, want mailto:%s", sub, webPushDefaultSubscriber)
	}
}

// 外部传入带前缀的 Subscriber（-web-push-subscriber）也必须被归一化为裸地址。
func TestDispatcherSubscriberMailtoPrefixNormalized(t *testing.T) {
	h := newDispatcherHarness(t, 201)
	d := NewWebPushDispatcher(h.store, h.pipeline, WebPushDispatcherConfig{
		Subscriber: "mailto:" + webPushDefaultSubscriber,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RetryDelay: 5 * time.Millisecond,
		RetryMax:   2,
	})
	d.Start()
	t.Cleanup(d.Stop)
	h.deliverSync(t, d, dispatcherCandidate(WebPushKindCompletion, "codex|vapid-2|tv2|completed"))

	h.mu.Lock()
	if len(h.headers) == 0 {
		h.mu.Unlock()
		t.Fatal("no request captured")
	}
	auth := h.headers[0].Get("Authorization")
	h.mu.Unlock()

	sub := decodeVapidSubClaim(t, auth)
	if sub != "mailto:"+webPushDefaultSubscriber {
		t.Fatalf("VAPID sub = %q, want normalized mailto:%s", sub, webPushDefaultSubscriber)
	}
}
