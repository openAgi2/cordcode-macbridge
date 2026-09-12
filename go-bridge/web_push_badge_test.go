package gobridge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// web_push_badge_test.go — per-device badge state 定向测试（badge-and-collapse plan §5 B2/B3）。
//
// 诚实边界：这些用例证明 store 语义（binding 生命周期、revision 单调、saturated 边界、
// 持久化与重启、损坏隔离）与 dispatcher 的 badge 注入；不构成设备端 setAppBadge 展示
// 证据——那是 P4 SW/P5 UI 真机回归的职责。

// badgeTestBinding 生成固定格式的测试 bindingId（wpb_ + 22 base64url 字符）。
func badgeTestBinding(seed byte) string {
	body := make([]byte, 16)
	for i := range body {
		body[i] = seed
	}
	return "wpb_" + base64.RawURLEncoding.EncodeToString(body)
}

// badgeRegister 在 store 上为 device 注册带 binding 的 subscription（endpoint 可变，
// 用于构造相同/不同 subscription identity）。
func badgeRegister(t *testing.T, store *WebPushStore, device, binding, endpoint string) (string, error) {
	t.Helper()
	record := testSubscriptionRecord(endpoint)
	record.P256dh = base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	record.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	id, err := store.RegisterWithBinding(device, record, binding)
	if err != nil {
		return "", err
	}
	return id, nil
}

func badgeAdvance(store *WebPushStore, device, sessionKey string) (WebPushBadgeSnapshot, bool) {
	return store.AdvanceBadgeOnCompletion(device, sessionKey, time.Now().UTC().UnixMilli())
}

func badgeStateOf(t *testing.T, store *WebPushStore, device string) WebPushBadgeDeviceState {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	state, ok := store.badgeDevices[device]
	if !ok {
		t.Fatalf("no badge state for device %q", device)
	}
	return state
}

func TestBadgeBindingLifecycle(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_1"
	b1, b2 := badgeTestBinding(0x01), badgeTestBinding(0x02)
	endpoint := "https://web.push.apple.com/X1BA/dev_badge_1"

	// 首次 binding 建立状态。
	if _, err := badgeRegister(t, store, device, b1, endpoint); err != nil {
		t.Fatalf("register b1: %v", err)
	}
	if _, ok := badgeAdvance(store, device, "sess-a"); !ok {
		t.Fatal("first advance after binding must produce a snapshot")
	}
	// 同 binding 同 subscription：幂等重注册，不清空集合。
	if _, err := badgeRegister(t, store, device, b1, endpoint); err != nil {
		t.Fatalf("idempotent re-register b1: %v", err)
	}
	snap, ok := badgeAdvance(store, device, "sess-a")
	if !ok || snap.Count != 1 || snap.BindingID != b1 || snap.Revision != 2 {
		t.Fatalf("after idempotent re-register: snap = %+v ok=%v, want count=1 rev=2 binding=b1", snap, ok)
	}
	// 不同 binding：新状态并清空旧集合（revision 从新周期开始）。
	if _, err := badgeRegister(t, store, device, b2, endpoint); err != nil {
		t.Fatalf("register b2: %v", err)
	}
	snap, ok = badgeAdvance(store, device, "sess-a")
	if !ok || snap.Count != 1 || snap.BindingID != b2 || snap.Revision != 1 {
		t.Fatalf("after new binding: snap = %+v ok=%v, want fresh state count=1 rev=1 binding=b2", snap, ok)
	}
	// 同 binding 换 subscription identity：拒绝（客户端必须走完整 rebind + 新 bindingId）。
	_, err = badgeRegister(t, store, device, b2, endpoint+"/rebound")
	if err == nil {
		t.Fatal("same bindingId with changed subscription identity must be rejected")
	}
	wpErr, isWpErr := err.(*webPushValidationError)
	if !isWpErr || wpErr.code != WebPushErrBindingMismatch || wpErr.retryable {
		t.Fatalf("err = %+v, want non-retryable %s", err, WebPushErrBindingMismatch)
	}
	// 旧客户端（无 bindingId）：订阅能力不受影响，不建 badge 状态。
	record := testSubscriptionRecord(endpoint + "/legacy")
	record.P256dh = base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	record.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	if _, err := store.RegisterWithBinding("dev_badge_legacy", record, ""); err != nil {
		t.Fatalf("legacy register without binding: %v", err)
	}
	if _, ok := badgeAdvance(store, "dev_badge_legacy", "sess-a"); ok {
		t.Fatal("legacy client without bindingId must not get badge snapshots")
	}
}

func TestBadgeAdvanceCountsDistinctSessions(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_2"
	if _, err := badgeRegister(t, store, device, badgeTestBinding(0x11), "https://web.push.apple.com/X1BA/dev_badge_2"); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 未绑定设备：不推进、不下发。
	if _, ok := badgeAdvance(store, "dev_badge_unknown", "sess-a"); ok {
		t.Fatal("unbound device must not produce a snapshot")
	}
	snap, ok := badgeAdvance(store, device, "sess-a")
	if !ok || snap.Count != 1 || snap.Revision != 1 {
		t.Fatalf("advance sess-a: snap = %+v ok=%v", snap, ok)
	}
	// 同一会话再次完成：只计 1，revision 递增。
	snap, ok = badgeAdvance(store, device, "sess-a")
	if !ok || snap.Count != 1 || snap.Revision != 2 {
		t.Fatalf("advance sess-a again: snap = %+v ok=%v, want count=1 rev=2", snap, ok)
	}
	// 不同会话：计数 +1。
	snap, ok = badgeAdvance(store, device, "sess-b")
	if !ok || snap.Count != 2 || snap.Revision != 3 {
		t.Fatalf("advance sess-b: snap = %+v ok=%v, want count=2 rev=3", snap, ok)
	}
}

func TestBadgeCountCappedAt999(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_cap"
	if _, err := badgeRegister(t, store, device, badgeTestBinding(0x21), "https://web.push.apple.com/X1BA/dev_badge_cap"); err != nil {
		t.Fatalf("register: %v", err)
	}
	var snap WebPushBadgeSnapshot
	var ok bool
	for i := 0; i < 1000; i++ {
		snap, ok = badgeAdvance(store, device, fmt.Sprintf("sess-%04d", i))
		if !ok {
			t.Fatalf("advance %d: unexpected omission below saturation", i)
		}
		if snap.Count > webPushBadgeMaxCount {
			t.Fatalf("advance %d: count = %d exceeds cap %d", i, snap.Count, webPushBadgeMaxCount)
		}
	}
	if snap.Count != webPushBadgeMaxCount {
		t.Fatalf("count = %d, want capped %d", snap.Count, webPushBadgeMaxCount)
	}
}

func TestBadgeSaturatedAt4097thDistinctSession(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_sat"
	if _, err := badgeRegister(t, store, device, badgeTestBinding(0x31), "https://web.push.apple.com/X1BA/dev_badge_sat"); err != nil {
		t.Fatalf("register: %v", err)
	}
	for i := 0; i < webPushBadgeMaxUnreadSessions; i++ {
		if _, ok := badgeAdvance(store, device, fmt.Sprintf("sess-%04d", i)); !ok {
			t.Fatalf("advance %d below cap must produce snapshot", i)
		}
	}
	// 第 4097 个不同 key：进入 saturated，三字段全部缺省，但 revision 继续推进。
	if snap, ok := badgeAdvance(store, device, "sess-overflow"); ok {
		t.Fatalf("saturated advance must omit badge tuple, got %+v", snap)
	}
	state := badgeStateOf(t, store, device)
	if !state.Saturated {
		t.Fatal("state must be saturated after 4097th distinct session")
	}
	if got := len(state.UnreadSessions); got != webPushBadgeMaxUnreadSessions {
		t.Fatalf("unread sessions = %d, want capped %d", got, webPushBadgeMaxUnreadSessions)
	}
	if state.Revision != uint64(webPushBadgeMaxUnreadSessions+1) {
		t.Fatalf("revision = %d, want %d (keeps advancing while saturated)", state.Revision, webPushBadgeMaxUnreadSessions+1)
	}
	// 已存在 key 在 saturated 下仍更新其 revision（ack 水位依赖）。
	badgeAdvance(store, device, "sess-0000")
	if state = badgeStateOf(t, store, device); state.UnreadSessions["sess-0000"] != state.Revision {
		t.Fatalf("existing key revision = %d, want current %d", state.UnreadSessions["sess-0000"], state.Revision)
	}
}

func TestBadgePersistFailureOmitsTupleButConverges(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_persist"
	if _, err := badgeRegister(t, store, device, badgeTestBinding(0x41), "https://web.push.apple.com/X1BA/dev_badge_persist"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if snap, ok := badgeAdvance(store, device, "sess-a"); !ok || snap.Count != 1 {
		t.Fatalf("advance sess-a: snap = %+v ok=%v", snap, ok)
	}
	// 让 badge 文件路径不可写（目录占位）：持久化失败 → 本次缺省三元组，通知路径不受影响。
	badgePath := filepath.Join(dir, webPushBadgeStateFile)
	if err := os.Remove(badgePath); err != nil {
		t.Fatalf("remove badge file: %v", err)
	}
	if err := os.Mkdir(badgePath, 0o700); err != nil {
		t.Fatalf("block badge path: %v", err)
	}
	if snap, ok := badgeAdvance(store, device, "sess-b"); ok {
		t.Fatalf("persist failure must omit badge tuple, got %+v", snap)
	}
	// 恢复可写后：内存态收敛（含失败期间观察到的 sess-b）。
	if err := os.Remove(badgePath); err != nil {
		t.Fatalf("unblock badge path: %v", err)
	}
	snap, ok := badgeAdvance(store, device, "sess-c")
	if !ok || snap.Count != 3 || snap.Revision != 3 {
		t.Fatalf("converged advance: snap = %+v ok=%v, want count=3 rev=3 (sess-b retained)", snap, ok)
	}
}

func TestBadgeStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_restart"
	if _, err := badgeRegister(t, store, device, badgeTestBinding(0x51), "https://web.push.apple.com/X1BA/dev_badge_restart"); err != nil {
		t.Fatalf("register: %v", err)
	}
	badgeAdvance(store, device, "sess-a")
	badgeAdvance(store, device, "sess-b")

	reloaded, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	snap, ok := badgeAdvance(reloaded, device, "sess-a")
	if !ok || snap.Count != 2 || snap.Revision != 3 || snap.BindingID != badgeTestBinding(0x51) {
		t.Fatalf("post-restart advance: snap = %+v ok=%v, want count=2 rev=3 same binding", snap, ok)
	}
}

func TestBadgeFileCorruptDisablesBadgeOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if status, _ := store.Status(); status != WebPushStoreHealthy {
		t.Fatalf("pre-corrupt status = %s", status)
	}
	// 写入损坏 badge 文件后重载：订阅健康度不受影响（不进 misconfigured），badge 关闭。
	if err := os.WriteFile(filepath.Join(dir, webPushBadgeStateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt badge file: %v", err)
	}
	reloaded, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload with corrupt badge file: %v", err)
	}
	if status, _ := reloaded.Status(); status != WebPushStoreHealthy {
		t.Fatalf("corrupt badge file must not degrade subscription health, got %s", status)
	}
	if _, ok := badgeAdvance(reloaded, "dev_badge_x", "sess-a"); ok {
		t.Fatal("corrupt badge state must disable badge snapshots")
	}
	// 带 binding 的 register 仍成功（订阅能力不受影响），但不建 badge 状态。
	if _, err := badgeRegister(t, reloaded, "dev_badge_x", badgeTestBinding(0x61), "https://web.push.apple.com/X1BA/dev_badge_x"); err != nil {
		t.Fatalf("register with binding under corrupt badge file: %v", err)
	}
	if _, ok := badgeAdvance(reloaded, "dev_badge_x", "sess-a"); ok {
		t.Fatal("no badge state may be created while badge store is corrupt")
	}
}

func TestBadgeUnregisterAndDeviceDeleteClearState(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_del"
	endpoint := "https://web.push.apple.com/X1BA/dev_badge_del"
	subID, err := badgeRegister(t, store, device, badgeTestBinding(0x71), endpoint)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	badgeAdvance(store, device, "sess-a")

	// Unregister 删除 badge 状态；同 endpoint 重新启用必须用新 binding（从空集合开始）。
	if _, err := store.Unregister(device, subID); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, ok := badgeAdvance(store, device, "sess-a"); ok {
		t.Fatal("badge state must be deleted by unregister")
	}
	if _, err := badgeRegister(t, store, device, badgeTestBinding(0x72), endpoint); err != nil {
		t.Fatalf("re-register new binding: %v", err)
	}
	if snap, ok := badgeAdvance(store, device, "sess-a"); !ok || snap.Count != 1 || snap.Revision != 1 {
		t.Fatalf("re-enabled binding must start empty: snap = %+v ok=%v", snap, ok)
	}

	// DeleteDevice（trusted device revoke）同样删除状态。
	if err := store.DeleteDevice(device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if _, ok := badgeAdvance(store, device, "sess-a"); ok {
		t.Fatal("badge state must be deleted by device revoke")
	}
}

func TestBadgeConcurrentAdvanceIsSerializedAndMonotonic(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_race"
	if _, err := badgeRegister(t, store, device, badgeTestBinding(0x81), "https://web.push.apple.com/X1BA/dev_badge_race"); err != nil {
		t.Fatalf("register: %v", err)
	}
	const workers, perWorker = 2, 50
	seen := make(chan uint64, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				snap, ok := badgeAdvance(store, device, fmt.Sprintf("sess-w%d-%03d", w, i))
				if !ok {
					t.Errorf("worker %d advance %d: unexpected omission", w, i)
					return
				}
				seen <- snap.Revision
			}
		}(w)
	}
	wg.Wait()
	close(seen)
	// 两个 worker 并发：revision 必须两两不同且单调分配（无重复、无回退）。
	revs := make(map[uint64]bool)
	for rev := range seen {
		if revs[rev] {
			t.Fatalf("revision %d handed out twice", rev)
		}
		revs[rev] = true
	}
	if len(revs) != workers*perWorker {
		t.Fatalf("distinct revisions = %d, want %d", len(revs), workers*perWorker)
	}
	state := badgeStateOf(t, store, device)
	if state.Revision != uint64(workers*perWorker) {
		t.Fatalf("final revision = %d, want %d", state.Revision, workers*perWorker)
	}
}

func TestIsValidWebPushBindingID(t *testing.T) {
	valid := []string{
		badgeTestBinding(0x00),
		"wpb_" + strings.Repeat("A", 22),
		"wpb_0123456789-_abcDEFghij",
	}
	invalid := []string{
		"",
		"wpb_",
		"wpb_short",
		"wpb_" + strings.Repeat("A", 23),
		"wpb_" + strings.Repeat("A", 21),
		"cc_" + strings.Repeat("A", 22),
		"wpb_" + strings.Repeat("A", 21) + "+",
		"wpb_" + strings.Repeat("A", 21) + "/",
		" wpb_" + strings.Repeat("A", 22),
	}
	for _, id := range valid {
		if !IsValidWebPushBindingID(id) {
			t.Errorf("IsValidWebPushBindingID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if IsValidWebPushBindingID(id) {
			t.Errorf("IsValidWebPushBindingID(%q) = true, want false", id)
		}
	}
}

// ── dispatcher badge 注入（§7 dispatcher 单测）────────────────────────────────

func TestDispatcherBadgeTuplePerDevice(t *testing.T) {
	h := newDispatcherHarness(t, 200, 200)
	d := newTestDispatcher(h)
	// harness 默认订阅（dev_disp）无 binding：同 endpoint + 同真实密钥材料幂等
	// upsert 补 binding（identity 不变，不触发 mismatch；P256dh 必须是真实 P-256
	// 点，否则 webpush-go ECDH 直接失败走不到 HTTP）。
	var defaultRecord PushSubscriptionRecord
	for _, sub := range h.store.Subscriptions() {
		if sub.DeviceID == "dev_disp" {
			defaultRecord = sub
		}
	}
	if defaultRecord.Endpoint == "" {
		t.Fatal("harness default subscription missing")
	}
	if _, err := h.store.RegisterWithBinding("dev_disp", defaultRecord, badgeTestBinding(0x91)); err != nil {
		t.Fatalf("register dev_disp with binding: %v", err)
	}
	// 第二台设备：不同 endpoint + 独立生成的真实 P-256 客户端密钥 + 自己的 binding。
	clientKey, kerr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if kerr != nil {
		t.Fatalf("client key: %v", kerr)
	}
	second := testSubscriptionRecord(h.server.URL + "/push/dev_disp2")
	second.P256dh = base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), clientKey.PublicKey.X, clientKey.PublicKey.Y))
	second.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	if _, err := h.store.RegisterWithBinding("dev_disp2", second, badgeTestBinding(0x92)); err != nil {
		t.Fatalf("register dev_disp2 with binding: %v", err)
	}

	first := dispatcherCandidate(WebPushKindCompletion, "codex|disp-1|turn-b1|completed")
	h.deliverSync(t, d, first)

	// 两设备各自推进：revision=1、count=1（同会话只计 1），互不干扰。
	for _, device := range []string{"dev_disp", "dev_disp2"} {
		binding, revision, count, saturated, ok := h.store.BadgeStateForDevice(device)
		if !ok || saturated {
			t.Fatalf("%s: state ok=%v saturated=%v", device, ok, saturated)
		}
		if revision != 1 || count != 1 {
			t.Fatalf("%s: rev=%d count=%d, want 1/1", device, revision, count)
		}
		if binding == "" {
			t.Fatalf("%s: empty binding", device)
		}
	}

	// 同会话第二回合：count 仍 1，revision=2；投递仍成功（2 订阅 × 2 回合 = 4 请求）。
	secondTurn := first
	secondTurn.NotificationKey = "codex|disp-1|turn-b2|completed"
	h.deliverSync(t, d, secondTurn)
	if _, revision, count, _, ok := h.store.BadgeStateForDevice("dev_disp"); !ok || revision != 2 || count != 1 {
		t.Fatalf("dev_disp after 2nd turn: rev=%d count=%d ok=%v, want 2/1", revision, count, ok)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if int(h.requests) != 4 {
		t.Fatalf("requests = %d, want 4 (2 devices × 2 turns)", h.requests)
	}
}

func TestMarshalBadgePayloadTupleAllOrNothing(t *testing.T) {
	template := WebPushPayloadV1{
		SchemaVersion: WebPushSchemaVersion,
		Notification:  WebPushNotificationPayload{Title: "Codex 任务已完成", Body: "预览", Tag: "ccs_x"},
		Target:        WebPushTarget{BridgeID: "brg", BackendID: "codex", SessionID: "s", EventID: "e:1"},
	}
	badge := 3
	withTuple, err := marshalBadgePayload(template, WebPushBadgeSnapshot{BindingID: badgeTestBinding(0x93), Revision: 7, Count: 3}, true)
	if err != nil {
		t.Fatalf("marshal with tuple: %v", err)
	}
	var decoded WebPushPayloadV1
	if err := json.Unmarshal(withTuple, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Notification.Badge == nil || *decoded.Notification.Badge != badge {
		t.Fatalf("badge = %+v, want %d", decoded.Notification.Badge, badge)
	}
	if decoded.Notification.BadgeBindingID != badgeTestBinding(0x93) || decoded.Notification.BadgeRevision != "7" {
		t.Fatalf("tuple = (%q,%q), want binding=%q rev=\"7\"", decoded.Notification.BadgeBindingID, decoded.Notification.BadgeRevision, badgeTestBinding(0x93))
	}

	withoutTuple, err := marshalBadgePayload(template, WebPushBadgeSnapshot{}, false)
	if err != nil {
		t.Fatalf("marshal without tuple: %v", err)
	}
	raw := string(withoutTuple)
	for _, field := range []string{`"badge"`, `"badgeBindingId"`, `"badgeRevision"`} {
		if strings.Contains(raw, field) {
			t.Fatalf("payload without snapshot must omit %s: %s", field, raw)
		}
	}
}

func TestBadgeAcknowledgeSemantics(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_ack"
	binding := badgeTestBinding(0xA1)
	if _, err := badgeRegister(t, store, device, binding, "https://web.push.apple.com/X1BA/dev_badge_ack"); err != nil {
		t.Fatalf("register: %v", err)
	}
	badgeAdvance(store, device, "sess-a") // rev 1
	badgeAdvance(store, device, "sess-b") // rev 2
	badgeAdvance(store, device, "sess-c") // rev 3

	// binding mismatch：稳定错误，不盲重试。
	if _, ackErr := store.AcknowledgeBadge(device, badgeTestBinding(0xA2), 3, time.Now().UTC().UnixMilli()); ackErr == nil || ackErr.code != WebPushErrBindingMismatch {
		t.Fatalf("ack with wrong binding: err = %+v, want %s", ackErr, WebPushErrBindingMismatch)
	}

	// available：只删除 ≤ throughRevision 的条目（sess-a@1、sess-b@2），sess-c@3 保留。
	view, ackErr := store.AcknowledgeBadge(device, binding, 2, time.Now().UTC().UnixMilli())
	if ackErr != nil {
		t.Fatalf("ack through 2: %v", ackErr)
	}
	if view.Count != 1 || view.Revision != 3 || view.Saturated {
		t.Fatalf("after ack through 2: view = %+v, want count=1 rev=3", view)
	}
	// 重复 ack（同水位）：幂等，不删除 sess-c。
	view, ackErr = store.AcknowledgeBadge(device, binding, 2, time.Now().UTC().UnixMilli())
	if ackErr != nil || view.Count != 1 {
		t.Fatalf("idempotent re-ack: view = %+v err = %+v", view, ackErr)
	}
	// ack 到当前 revision：集合清空，count=0。
	view, ackErr = store.AcknowledgeBadge(device, binding, 3, time.Now().UTC().UnixMilli())
	if ackErr != nil || view.Count != 0 {
		t.Fatalf("ack through head: view = %+v err = %+v", view, ackErr)
	}
}

func TestBadgeAcknowledgeSaturatedOnlyClearsWhenStable(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_acksat"
	binding := badgeTestBinding(0xB1)
	if _, err := badgeRegister(t, store, device, binding, "https://web.push.apple.com/X1BA/dev_badge_acksat"); err != nil {
		t.Fatalf("register: %v", err)
	}
	for i := 0; i < webPushBadgeMaxUnreadSessions; i++ {
		badgeAdvance(store, device, fmt.Sprintf("sess-%04d", i))
	}
	// 第 4097 个 key → saturated，revision = 4097。
	badgeAdvance(store, device, "sess-overflow")

	// 水位仍在前进（ack 落后于当前 revision）：保持 saturated，不清空。
	view, ackErr := store.AcknowledgeBadge(device, binding, 4096, time.Now().UTC().UnixMilli())
	if ackErr != nil {
		t.Fatalf("ack behind head while saturated: %v", ackErr)
	}
	if !view.Saturated || view.Count != webPushBadgeMaxUnreadSessions {
		t.Fatalf("ack behind head: view = %+v, want saturated with %d keys", view, webPushBadgeMaxUnreadSessions)
	}
	// 追上当前 revision：整体清空并退出 saturated。
	view, ackErr = store.AcknowledgeBadge(device, binding, 4097, time.Now().UTC().UnixMilli())
	if ackErr != nil {
		t.Fatalf("ack at head while saturated: %v", ackErr)
	}
	if view.Saturated || view.Count != 0 {
		t.Fatalf("ack at head: view = %+v, want cleared and not saturated", view)
	}
	// 清空后新 completion：从空集合重新计数。
	if snap, ok := badgeAdvance(store, device, "sess-new"); !ok || snap.Count != 1 {
		t.Fatalf("post-clear advance: snap = %+v ok = %v", snap, ok)
	}
}

func TestBadgeStateView(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	const device = "dev_badge_view"
	binding := badgeTestBinding(0xC1)
	if _, err := badgeRegister(t, store, device, binding, "https://web.push.apple.com/X1BA/dev_badge_view"); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 无状态设备 / binding 不符 → binding_mismatch（客户端需先 reconcile）。
	if _, viewErr := store.BadgeStateView("dev_badge_none", binding); viewErr == nil || viewErr.code != WebPushErrBindingMismatch {
		t.Fatalf("no-state view: err = %+v, want %s", viewErr, WebPushErrBindingMismatch)
	}
	if _, viewErr := store.BadgeStateView(device, badgeTestBinding(0xC2)); viewErr == nil || viewErr.code != WebPushErrBindingMismatch {
		t.Fatalf("wrong-binding view: err = %+v, want %s", viewErr, WebPushErrBindingMismatch)
	}
	badgeAdvance(store, device, "sess-a")
	view, viewErr := store.BadgeStateView(device, binding)
	if viewErr != nil {
		t.Fatalf("view: %v", viewErr)
	}
	if view.BindingID != binding || view.Revision != 1 || view.Count != 1 || view.Saturated {
		t.Fatalf("view = %+v, want binding/rev=1/count=1/available", view)
	}
}
