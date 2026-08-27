package gobridge

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// web_push_dispatcher.go — bounded delivery worker（web push 方案 §8.4）。
//
// 消费 WebPushCandidatePipeline 的有界队列：每 candidate 对每个 subscription 构造
// 固定文案 payload（§7.3），经 webpush-go（RFC 8291 加密 + RFC 8292 VAPID）直发浏览器
// PushSubscription endpoint。不创建无界 goroutine——固定 worker 数、队列即 pipeline 的
// 有界 channel。
//
// 状态机（§8.4）：
//   2xx → accepted（不声称设备已展示）；
//   404/410 → WP-RESP-2 样本证明前不写稳定产品语义：只记 expiry_unverified +
//     脱敏诊断，不删 subscription；样本后（webPushExpirySemanticsProven 置 true）
//     删除 subscription 并记 expired；
//   429 → 尊重有效 Retry-After，否则有界退避，总重试不超过 TTL；
//   5xx/网络错误 → TTL 内有界退避（temporary_failed）；
//   400/401/403 → permanent_failed，暴露 VAPID/payload 脱敏 diagnostic，
//     不删除 key、不伪造成功。
//
// 诚实边界：本文件的 httptest 单元测试证明状态机与头部契约，不构成 Apple push
// 服务互操作证据——那是 WP-RESP-1/2/3 样本门的职责（E2，owner-gated）。

const (
	// webPushDefaultSubscriber 是 VAPID JWT sub 的固定项目联系地址（可用
	// -web-push-subscriber 覆盖）。永不写入日志。
	webPushDefaultSubscriber = "mailto:noreply@byteseek.uk"

	webPushDispatcherWorkers = 2

	// TTL/Urgency（§8.4，写入测试）：completion/error 1h、permission/input 5min；
	// permission/input high、completion/error normal。
	webPushTTLCompletion = time.Hour
	webPushTTLAction     = 5 * time.Minute

	// 重试退避（测试可注入缩短）：429 无有效 Retry-After 与 5xx/网络错误共用。
	webPushRetryBaseDelay = 5 * time.Second
	webPushRetryMaxCount  = 3

	// 诊断 body 读取上限（脱敏用途）。
	webPushDiagnosticBodyLimit = 512
)

// webPushExpirySemanticsProven 在 WP-RESP-2（真实 404/410 样本）归档后由 owner
// 显式置 true。在此之前 404/410 不删 subscription、不写 expired 终态。
var webPushExpirySemanticsProven = false

// WebPushDispatcherConfig 汇总可注入项（测试用 httptest client + 短退避）。
type WebPushDispatcherConfig struct {
	Subscriber string
	HTTPClient webpush.HTTPClient
	Workers    int
	RetryDelay time.Duration
	RetryMax   int
	Now        func() time.Time
}

// WebPushDispatcher 是有界投递 worker 组。
type WebPushDispatcher struct {
	store    *WebPushStore
	pipeline *WebPushCandidatePipeline
	cfg      WebPushDispatcherConfig
	stop     chan struct{}
	wg       sync.WaitGroup
}

func NewWebPushDispatcher(store *WebPushStore, pipeline *WebPushCandidatePipeline, cfg WebPushDispatcherConfig) *WebPushDispatcher {
	if cfg.Subscriber == "" {
		cfg.Subscriber = webPushDefaultSubscriber
	}
	if cfg.Workers <= 0 {
		cfg.Workers = webPushDispatcherWorkers
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = webPushRetryBaseDelay
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = webPushRetryMaxCount
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &WebPushDispatcher{
		store:    store,
		pipeline: pipeline,
		cfg:      cfg,
		stop:     make(chan struct{}),
	}
}

// Start 启动固定数量 worker（幂等：重复调用无效）。
func (d *WebPushDispatcher) Start() {
	for i := 0; i < d.cfg.Workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}
}

// Stop 停止全部 worker 并等待退出。
func (d *WebPushDispatcher) Stop() {
	close(d.stop)
	d.wg.Wait()
}

func (d *WebPushDispatcher) worker() {
	defer d.wg.Done()
	for {
		select {
		case <-d.stop:
			return
		case candidate := <-d.pipeline.C():
			d.deliverCandidate(candidate)
		}
	}
}

// deliverCandidate 对全部 subscription 投递该 candidate 并按状态机记账。
func (d *WebPushDispatcher) deliverCandidate(candidate WebPushCandidate) {
	if d.store == nil {
		return
	}
	keyHash := WebPushNotificationKeyHash(candidate.NotificationKey)
	payload, ttl, urgency, err := d.buildPayload(candidate)
	if err != nil {
		// payload 构造失败是真实缺陷：记录脱敏诊断，不伪造成功。
		slog.Error("web-push: payload build failed",
			"backendID", candidate.BackendID,
			"sessionPrefix", projectionSessionLogPrefix(candidate.SessionID),
			"kind", string(candidate.Kind),
			"error", err.Error(),
		)
		return
	}
	for _, sub := range d.store.Subscriptions() {
		d.deliverToSubscription(candidate, keyHash, payload, ttl, urgency, sub)
	}
	_ = d.store.PersistLedgerIfNeeded()
}

func (d *WebPushDispatcher) buildPayload(candidate WebPushCandidate) ([]byte, int, webpush.Urgency, error) {
	title, body := buildWebPushNotificationText(candidate.Kind, candidate.SessionTitle)
	payload := WebPushPayloadV1{
		SchemaVersion: WebPushSchemaVersion,
		Notification: WebPushNotificationPayload{
			Title: title,
			Body:  body,
			Tag:   "cc_" + keyHashTag(candidate),
		},
		Target: WebPushTarget{
			BridgeID:  candidate.BridgeID,
			BackendID: candidate.BackendID,
			SessionID: candidate.SessionID,
			EventID:   candidate.EventID,
			Anchor:    buildAnchor(candidate),
		},
	}
	ttl := webPushTTLCompletion
	urgency := webpush.UrgencyNormal
	switch candidate.Kind {
	case WebPushKindPermission, WebPushKindInput:
		ttl = webPushTTLAction
		urgency = webpush.UrgencyHigh
	case WebPushKindCompletion, WebPushKindError:
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, "", err
	}
	return raw, int(ttl.Seconds()), urgency, nil
}

func keyHashTag(candidate WebPushCandidate) string {
	hash := WebPushNotificationKeyHash(candidate.NotificationKey)
	if len(hash) > 16 {
		return hash[:16]
	}
	return hash
}

// buildAnchor 只允许已验证 kind 的 anchor（§7.3）：producer 已保证 kind 匹配
// anchor 类型；未知形状回 nil（不伪造 anchor）。
func buildAnchor(candidate WebPushCandidate) *WebPushAnchorType {
	if candidate.AnchorKind == "" || candidate.AnchorID == "" {
		return nil
	}
	switch candidate.AnchorKind {
	case "turn", "interaction":
		return &WebPushAnchorType{Kind: candidate.AnchorKind, ID: candidate.AnchorID}
	default:
		return nil
	}
}

// webPushCaptureResponse 在每个投递分类分支记录脱敏响应样本（WP-RESP-1/2/3，
// 设计 delta §3；采集开关关闭时零开销）。
func webPushCaptureResponse(classification string, status int, retryAfterPresent bool, candidate WebPushCandidate, sub PushSubscriptionRecord) {
	captureWebPushSample("WP-RESP", map[string]interface{}{
		"classification": classification,
		"httpStatus":     status,
		"retryAfter":     retryAfterPresent,
		"kind":           string(candidate.Kind),
		"subscription":   webPushRedactID(sub.SubscriptionID),
	})
}

// deliverToSubscription 按 §8.4 状态机投递单个 subscription（含 TTL 内有界重试）。
func (d *WebPushDispatcher) deliverToSubscription(
	candidate WebPushCandidate,
	keyHash string,
	payload []byte,
	ttlSeconds int,
	urgency webpush.Urgency,
	sub PushSubscriptionRecord,
) {
	privateKey := d.store.VapidPrivateKey()
	if privateKey == nil {
		// store 非 healthy：send 关闭（fail closed），不伪造投递。
		slog.Warn("web-push: store not healthy, send disabled", "kind", string(candidate.Kind))
		return
	}
	options := &webpush.Options{
		HTTPClient:      d.cfg.HTTPClient,
		Subscriber:      d.cfg.Subscriber,
		TTL:             ttlSeconds,
		Urgency:         urgency,
		Topic:           "cc_" + keyHashTag(candidate),
		VAPIDPublicKey:  d.store.VapidPublicKey(),
		VAPIDPrivateKey: vapidPrivateScalarBase64(privateKey),
		RecordSize:      webpush.MaxRecordSize,
	}
	target := webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			Auth:   sub.Auth,
			P256dh: sub.P256dh,
		},
	}
	attempt := 0
	for {
		resp, err := webpush.SendNotificationWithContext(d.ctx(), payload, &target, options)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, webPushDiagnosticBodyLimit)) //nolint:errcheck — 诊断读（有界）
			resp.Body.Close()
			status := resp.StatusCode
			switch {
			case status >= 200 && status < 300:
				webPushCaptureResponse("accepted_2xx", status, false, candidate, sub)
				d.store.LedgerRecord(keyHash, candidate.EventID, sub.SubscriptionID, "accepted")
				return
			case status == http.StatusNotFound || status == http.StatusGone:
				if webPushExpirySemanticsProven {
					if delErr := d.store.MarkSubscriptionExpired(sub.SubscriptionID); delErr != nil {
						slog.Warn("web-push: expired subscription cleanup failed", "error", delErr.Error())
					}
					d.store.LedgerRecord(keyHash, candidate.EventID, sub.SubscriptionID, "expired")
				} else {
					// WP-RESP-2 样本未归档：只记录观察，不写稳定产品语义。
					d.store.LedgerRecord(keyHash, candidate.EventID, sub.SubscriptionID, "expiry_unverified")
					slog.Warn("web-push: 404/410 observed but expiry semantics not sample-proven (no deletion)",
						"status", status,
						"subscriptionPrefix", safeID(sub.SubscriptionID),
					)
				}
				return
			case status == http.StatusTooManyRequests:
				if delay, ok := retryAfter(resp); ok && delay > 0 {
					if !d.sleepInterruptible(delay) {
						return
					}
					attempt++
					if attempt > d.cfg.RetryMax {
						d.store.LedgerRecord(keyHash, candidate.EventID, sub.SubscriptionID, "temporary_failed")
						return
					}
					continue
				}
				d.recordTemporary(keyHash, candidate, sub, &attempt)
				if attempt > d.cfg.RetryMax {
					return
				}
				if !d.sleepInterruptible(d.backoff(attempt)) {
					return
				}
				continue
			case status >= 500:
				d.recordTemporary(keyHash, candidate, sub, &attempt)
				if attempt > d.cfg.RetryMax {
					return
				}
				if !d.sleepInterruptible(d.backoff(attempt)) {
					return
				}
				continue
			default:
				// 400/401/403 及其他 4xx：permanent failure。暴露脱敏 diagnostic，
				// 不删 key、不伪造成功。
				webPushCaptureResponse("permanent_4xx", status, false, candidate, sub)
				d.store.LedgerRecord(keyHash, candidate.EventID, sub.SubscriptionID, "permanent_failed")
				slog.Warn("web-push: permanent delivery failure",
					"status", status,
					"subscriptionPrefix", safeID(sub.SubscriptionID),
					"kind", string(candidate.Kind),
					"diagnostic", fmt.Sprintf("vapid=%dchars payload=%dbytes ttl=%d urgency=%s",
						len(options.VAPIDPublicKey), len(payload), ttlSeconds, urgency),
				)
				return
			}
		}
		// 网络错误/超时：TTL 内有界退避。
		d.recordTemporary(keyHash, candidate, sub, &attempt)
		if attempt > d.cfg.RetryMax {
			return
		}
		if !d.sleepInterruptible(d.backoff(attempt)) {
			return
		}
	}
}

func (d *WebPushDispatcher) recordTemporary(keyHash string, candidate WebPushCandidate, sub PushSubscriptionRecord, attempt *int) {
	d.store.LedgerRecord(keyHash, candidate.EventID, sub.SubscriptionID, "temporary_failed")
	*attempt++
}

func (d *WebPushDispatcher) backoff(attempt int) time.Duration {
	delay := d.cfg.RetryDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > time.Minute {
			return time.Minute
		}
	}
	return delay
}

func (d *WebPushDispatcher) sleepInterruptible(delay time.Duration) bool {
	select {
	case <-d.stop:
		return false
	case <-time.After(delay):
		return true
	}
}

func (d *WebPushDispatcher) ctx() context.Context {
	return context.Background()
}

// vapidPrivateScalarBase64 把 ecdsa 私钥序列化为 webpush-go 期望的 base64url scalar。
func vapidPrivateScalarBase64(private *ecdsa.PrivateKey) string {
	return base64.RawURLEncoding.EncodeToString(private.D.FillBytes(make([]byte, 32)))
}

// retryAfter 解析有效 Retry-After 秒数（0 或负值视为无效）。
func retryAfter(resp *http.Response) (time.Duration, bool) {
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	var seconds int
	if _, err := fmt.Sscanf(value, "%d", &seconds); err != nil || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
