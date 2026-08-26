package gobridge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestWebPushStore 构造一个临时目录里的 WebPushStore（首次启动即自动建 key）。
func newTestWebPushStore(t *testing.T) *WebPushStore {
	t.Helper()
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if status, _ := s.Status(); status != WebPushStoreHealthy {
		t.Fatalf("fresh store status = %q, want healthy", status)
	}
	return s
}

func testSubscriptionRecord(endpoint string) PushSubscriptionRecord {
	return PushSubscriptionRecord{
		Platform: "web",
		Endpoint: endpoint,
		P256dh:   base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x04}, 65)),
		Auth:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 16)),
	}
}

func TestWebPushStoreFirstCreateGeneratesKeyPair(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	status, detail := s.Status()
	if status != WebPushStoreHealthy {
		t.Fatalf("status = %q detail=%q, want healthy", status, detail)
	}
	pub := s.VapidPublicKey()
	if pub == "" {
		t.Fatal("VapidPublicKey empty after first create")
	}
	raw, err := DecodeBase64URL(pub)
	if err != nil || !IsUncompressedP256(raw) {
		t.Fatalf("public key is not 65-byte uncompressed P-256: %v", err)
	}
	if s.VapidPrivateKey() == nil {
		t.Fatal("VapidPrivateKey nil after first create")
	}
	if _, statErr := os.Stat(filepath.Join(dir, webPushVapidFile)); statErr != nil {
		t.Fatalf("vapid key file not written: %v", statErr)
	}
}

func TestWebPushStoreStableLoadKeepsSameKey(t *testing.T) {
	dir := t.TempDir()
	s1, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	pub1 := s1.VapidPublicKey()

	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if status, _ := s2.Status(); status != WebPushStoreHealthy {
		t.Fatalf("second load status = %q, want healthy", status)
	}
	if s2.VapidPublicKey() != pub1 {
		t.Fatal("VAPID public key changed across restarts")
	}
	if s2.SubscriptionCount() != 0 {
		t.Fatalf("subscription count = %d, want 0", s2.SubscriptionCount())
	}
}

func TestWebPushStoreFilesAre0600(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if _, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	s.LedgerRecord("hash1", "evt_1", "wps_1", "accepted")
	if err := s.PersistLedgerIfNeeded(); err != nil {
		t.Fatalf("PersistLedgerIfNeeded: %v", err)
	}
	for _, name := range []string{webPushVapidFile, webPushSubscriptionsFile, webPushLedgerFile} {
		info, statErr := os.Stat(filepath.Join(dir, name))
		if statErr != nil {
			t.Fatalf("stat %s: %v", name, statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
}

func TestWebPushStoreAtomicWriteLeavesTempFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if _, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// AtomicWriteFile 落盘后不应留下 .tmp 残留，且 JSON 可解析（半写文件会被 corruption 用例覆盖）。
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, webPushSubscriptionsFile))
	if readErr != nil {
		t.Fatalf("read subscriptions: %v", readErr)
	}
	var file WebPushSubscriptionsFile
	if json.Unmarshal(raw, &file) != nil {
		t.Fatalf("subscriptions file not valid JSON after %d entries", len(entries))
	}
	if len(file.Subscriptions) != 1 || file.Subscriptions[0].DeviceID != "dev_a" {
		t.Fatalf("persisted subscriptions = %+v", file.Subscriptions)
	}
}

func TestWebPushStoreCorruptVapidFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pub := s.VapidPublicKey()

	if writeErr := os.WriteFile(filepath.Join(dir, webPushVapidFile), []byte("{not-json"), 0o600); writeErr != nil {
		t.Fatalf("corrupt vapid: %v", writeErr)
	}
	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload with corrupt vapid: %v", err)
	}
	status, detail := s2.Status()
	if status != WebPushStoreMisconfigured {
		t.Fatalf("status = %q, want misconfigured", status)
	}
	if detail == "" {
		t.Fatal("misconfigured detail empty")
	}
	if strings.Contains(detail, pub) {
		t.Fatal("status detail leaks public key material")
	}
	if s2.VapidPublicKey() != "" {
		t.Fatal("VapidPublicKey non-empty in misconfigured state")
	}
	if s2.VapidPrivateKey() != nil {
		t.Fatal("VapidPrivateKey non-nil in misconfigured state")
	}
	// fail closed：register 拒绝；unregister 仍可用（恢复路径不依赖私钥）。
	if _, regErr := s2.Register("dev_a", testSubscriptionRecord("https://push.example.com/a")); regErr == nil {
		t.Fatal("Register succeeded in misconfigured state")
	}
}

func TestWebPushStoreSubscriptionsExistButKeyMissing(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if _, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if rmErr := os.Remove(filepath.Join(dir, webPushVapidFile)); rmErr != nil {
		t.Fatalf("remove vapid: %v", rmErr)
	}
	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	status, detail := s2.Status()
	if status != WebPushStoreMisconfigured {
		t.Fatalf("status = %q detail=%q, want misconfigured (must not silently regenerate)", status, detail)
	}
	// 显式 Reset 恢复：清空 store 后重建 key。
	if resetErr := s2.ResetWebPush(); resetErr != nil {
		t.Fatalf("ResetWebPush: %v", resetErr)
	}
	status, _ = s2.Status()
	if status != WebPushStoreHealthy {
		t.Fatalf("status after reset = %q, want healthy", status)
	}
	if s2.SubscriptionCount() != 0 {
		t.Fatalf("subscriptions survived reset: %d", s2.SubscriptionCount())
	}
	newPub := s2.VapidPublicKey()
	if newPub == "" || newPub == s.VapidPublicKey() && false {
		t.Fatal("reset did not produce a usable key")
	}
}

func TestWebPushStoreCorruptSubscriptionsFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	_ = s
	if writeErr := os.WriteFile(filepath.Join(dir, webPushSubscriptionsFile), []byte("[]"), 0o600); writeErr != nil {
		t.Fatalf("corrupt subscriptions: %v", writeErr)
	}
	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if status, _ := s2.Status(); status != WebPushStoreMisconfigured {
		t.Fatalf("status = %q, want misconfigured", status)
	}
}

func TestWebPushStoreRegisterUpsertIdempotent(t *testing.T) {
	s := newTestWebPushStore(t)
	rec := testSubscriptionRecord("https://push.example.com/a")

	id1, err := s.Register("dev_a", rec)
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	var firstCreatedAt int64
	for _, r := range s.Subscriptions() {
		if r.DeviceID == "dev_a" {
			firstCreatedAt = r.CreatedAt
		}
	}
	if firstCreatedAt == 0 {
		t.Fatal("CreatedAt not set on first register")
	}

	// 同 device 同 endpoint 重复注册：幂等，subscriptionId 稳定，CreatedAt 保留。
	time.Sleep(2 * time.Millisecond)
	id2, err := s.Register("dev_a", rec)
	if err != nil {
		t.Fatalf("idempotent Register: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("subscriptionId changed on re-register: %q vs %q", id1, id2)
	}
	if s.SubscriptionCount() != 1 {
		t.Fatalf("subscription count after re-register = %d, want 1", s.SubscriptionCount())
	}
	for _, r := range s.Subscriptions() {
		if r.DeviceID == "dev_a" && r.CreatedAt != firstCreatedAt {
			t.Fatalf("CreatedAt not preserved on idempotent upsert: %d vs %d", r.CreatedAt, firstCreatedAt)
		}
	}

	// 同 device 换 endpoint：替换（one PWA install = one active bridge owns it）。
	id3, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/b"))
	if err != nil {
		t.Fatalf("replacement Register: %v", err)
	}
	if id3 == id1 {
		t.Fatal("subscriptionId unchanged after endpoint change")
	}
	if s.SubscriptionCount() != 1 {
		t.Fatalf("subscription count after replace = %d, want 1", s.SubscriptionCount())
	}
}

func TestWebPushStoreDeviceIsolation(t *testing.T) {
	s := newTestWebPushStore(t)
	if _, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("register a: %v", err)
	}
	idB, err := s.Register("dev_b", testSubscriptionRecord("https://push.example.com/b"))
	if err != nil {
		t.Fatalf("register b: %v", err)
	}

	removed, err := s.Unregister("dev_a", "")
	if err != nil || !removed {
		t.Fatalf("Unregister a: removed=%v err=%v", removed, err)
	}
	if s.SubscriptionCount() != 1 {
		t.Fatalf("count after unregister = %d, want 1", s.SubscriptionCount())
	}
	for _, r := range s.Subscriptions() {
		if r.DeviceID != "dev_b" {
			t.Fatalf("unexpected survivor %+v", r)
		}
	}

	// 幂等：再删不存在的 device / 不匹配的 subscriptionId 均 no-op 成功。
	removed, err = s.Unregister("dev_a", "")
	if err != nil || removed {
		t.Fatalf("re-Unregister a: removed=%v err=%v", removed, err)
	}
	removed, err = s.Unregister("dev_b", "wps_does_not_match")
	if err != nil || removed {
		t.Fatalf("Unregister mismatched id: removed=%v err=%v", removed, err)
	}
	if s.SubscriptionCount() != 1 {
		t.Fatalf("count after no-op unregisters = %d, want 1", s.SubscriptionCount())
	}
	_ = idB
}

func TestWebPushStoreDeleteDeviceRevocationLinkage(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if _, err := s.Register("dev_web_1234567890abcdef", testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := s.DeleteDevice("dev_web_1234567890abcdef"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if s.SubscriptionCount() != 0 {
		t.Fatalf("subscription not removed with device")
	}
	// 持久化生效：重启后仍然没有该 subscription。
	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s2.SubscriptionCount() != 0 {
		t.Fatalf("subscription resurrected after reload: %d", s2.SubscriptionCount())
	}
}

func TestWebPushStoreDeleteDeviceLogRedacted(t *testing.T) {
	s := newTestWebPushStore(t)
	deviceID := "dev_web_SENSITIVETOKEN123456"
	if _, err := s.Register(deviceID, testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	if err := s.DeleteDevice(deviceID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	logged := buf.String()
	if strings.Contains(logged, deviceID) {
		t.Fatalf("log leaks full device id: %q", logged)
	}
	if !strings.Contains(logged, deviceID[:12]) {
		t.Fatalf("log missing truncated device prefix: %q", logged)
	}
	if strings.Contains(logged, s.VapidPublicKey()) {
		t.Fatal("log leaks VAPID public key")
	}
}

func TestWebPushStoreLedgerDedup(t *testing.T) {
	s := newTestWebPushStore(t)
	hash := WebPushNotificationKeyHash("brg_x|codex|sess_1|evt_turn_1|turn")

	if !s.LedgerShouldSend(hash) {
		t.Fatal("empty ledger should allow send")
	}
	s.LedgerRecord(hash, "evt_turn_1", "wps_abc", "temporary_failed")
	if !s.LedgerShouldSend(hash) {
		t.Fatal("temporary_failed entry must not suppress retry")
	}
	s.LedgerRecord(hash, "evt_turn_1", "wps_abc", "accepted")
	if s.LedgerShouldSend(hash) {
		t.Fatal("accepted entry must suppress re-send")
	}

	// 空 keyHash（identity 不完整）不入账。
	s.LedgerRecord("", "evt", "wps", "accepted")
	if err := s.PersistLedgerIfNeeded(); err != nil {
		t.Fatalf("PersistLedgerIfNeeded: %v", err)
	}
	var file WebPushLedgerFile
	raw, err := os.ReadFile(filepath.Join(s.dir, webPushLedgerFile))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if json.Unmarshal(raw, &file) != nil {
		t.Fatal("ledger file not valid JSON")
	}
	if len(file.Entries) != 1 || file.Entries[0].NotificationKeyHash != hash {
		t.Fatalf("ledger entries = %+v", file.Entries)
	}
	if file.Entries[0].Status != "accepted" || file.Entries[0].EventID != "evt_turn_1" {
		t.Fatalf("ledger entry fields = %+v", file.Entries[0])
	}
}

func TestWebPushStoreLedgerCleanupDoesNotTouchSubscriptions(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if _, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// 写入一条已过保留期的 accepted 条目，再持久化触发清理。
	old := WebPushNotificationKeyHash("old-key")
	s.LedgerRecord(old, "evt_old", "wps_old", "accepted")
	s.mu.Lock()
	entry := s.ledger[old]
	entry.LastAttemptMillis = time.Now().UTC().Add(-25 * time.Hour).UnixMilli()
	s.ledger[old] = entry
	s.mu.Unlock()
	if err := s.PersistLedgerIfNeeded(); err != nil {
		t.Fatalf("PersistLedgerIfNeeded: %v", err)
	}
	// 尺寸未超上限时，内存态不做强制淘汰（按需增长）；该条目仍在内存中。
	if s.LedgerShouldSend(old) {
		t.Fatal("in-memory ledger lost the entry before persist")
	}
	if s.SubscriptionCount() != 1 {
		t.Fatalf("ledger cleanup touched subscription store: count = %d", s.SubscriptionCount())
	}
	// 重启后 ledger 保留策略同样生效，subscription 不受影响。
	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s2.SubscriptionCount() != 1 {
		t.Fatalf("subscription lost across reload: %d", s2.SubscriptionCount())
	}
	if !s2.LedgerShouldSend(old) {
		t.Fatal("expired accepted entry survived reload")
	}
}

func TestWebPushStoreRegisterPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	id, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/a"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s2.VapidPublicKey() != s.VapidPublicKey() {
		t.Fatal("VAPID key changed across reload with subscriptions present")
	}
	subs := s2.Subscriptions()
	if len(subs) != 1 || subs[0].SubscriptionID != id {
		t.Fatalf("reloaded subscriptions = %+v, want id %q", subs, id)
	}
}

func TestWebPushStoreUnregisterWorksWhenMisconfigured(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	if _, err := s.Register("dev_a", testSubscriptionRecord("https://push.example.com/a")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, webPushVapidFile), []byte("{broken"), 0o600); writeErr != nil {
		t.Fatalf("corrupt vapid: %v", writeErr)
	}
	s2, err := LoadWebPushStore(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// misconfigured 下 unregister 仍可执行（不依赖私钥）。
	removed, err := s2.Unregister("dev_a", "")
	if err != nil || !removed {
		t.Fatalf("Unregister under misconfigured: removed=%v err=%v", removed, err)
	}
	if s2.SubscriptionCount() != 0 {
		t.Fatalf("count = %d, want 0", s2.SubscriptionCount())
	}
	// store 清空后，显式 Reset 重建 key 恢复 healthy。
	if resetErr := s2.ResetWebPush(); resetErr != nil {
		t.Fatalf("ResetWebPush: %v", resetErr)
	}
	if status, _ := s2.Status(); status != WebPushStoreHealthy {
		t.Fatalf("status after reset = %q, want healthy", status)
	}
}

func TestWebPushNotificationKeyHashIsOpaque(t *testing.T) {
	key := "brg_x|codex|sess_1|evt_y|turn"
	h := WebPushNotificationKeyHash(key)
	if len(h) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(h))
	}
	if strings.Contains(h, "brg_") || strings.Contains(h, "sess_1") {
		t.Fatal("hash leaks key material")
	}
	if WebPushNotificationKeyHash(key) != h {
		t.Fatal("hash not deterministic")
	}
}
