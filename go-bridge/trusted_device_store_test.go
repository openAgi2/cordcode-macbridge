package gobridge

import (
	"testing"
	"time"
)

func newTestStore() *MemoryDeviceStore {
	return NewMemoryDeviceStore()
}

func makeTestRecord(deviceID string) TrustedDeviceRecord {
	_, hash, _ := GenerateDeviceToken()
	return TrustedDeviceRecord{
		DeviceID:    deviceID,
		DisplayName: "Test " + deviceID,
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
}

func TestMemoryStore_AddAndLookupByID(t *testing.T) {
	store := newTestStore()
	rec := makeTestRecord("dev1")
	if err := store.AddDevice(rec); err != nil {
		t.Fatalf("AddDevice 失败: %v", err)
	}
	got, err := store.LookupByDeviceID("dev1")
	if err != nil {
		t.Fatalf("LookupByDeviceID 失败: %v", err)
	}
	if got == nil {
		t.Fatal("LookupByDeviceID 返回 nil")
	}
	if got.DeviceID != "dev1" {
		t.Errorf("DeviceID 不匹配: got %q, want %q", got.DeviceID, "dev1")
	}
	if got.TokenHash != rec.TokenHash {
		t.Errorf("TokenHash 不匹配: got %q, want %q", got.TokenHash, rec.TokenHash)
	}
}

func TestMemoryStore_AddDuplicate(t *testing.T) {
	store := newTestStore()
	rec := makeTestRecord("dev1")
	_ = store.AddDevice(rec)
	err := store.AddDevice(rec)
	if err == nil {
		t.Error("重复添加同 deviceID 应返回错误")
	}
}

func TestMemoryStore_ReplaceDeviceReplacesStableIDCredentials(t *testing.T) {
	store := newTestStore()
	old := makeTestRecord("dev1")
	_ = store.AddDevice(old)
	latest := makeTestRecord("dev1")

	replaced, err := store.ReplaceDevice(latest)
	if err != nil {
		t.Fatalf("ReplaceDevice 失败: %v", err)
	}
	if len(replaced) != 0 {
		t.Fatalf("稳定 deviceID 替换不应返回旧 ID: %v", replaced)
	}
	if got, _ := store.LookupByTokenHash(old.TokenHash); got != nil {
		t.Fatal("旧 token 应立即失效")
	}
	if got, _ := store.LookupByTokenHash(latest.TokenHash); got == nil {
		t.Fatal("新 token 应可用")
	}
}

func TestMemoryStore_ReplaceDeviceRemovesLegacyRandomID(t *testing.T) {
	store := newTestStore()
	old := makeTestRecord("legacy-random-id")
	old.DisplayName = "iPhone"
	_ = store.AddDevice(old)
	latest := makeTestRecord("dev_stable")
	latest.DisplayName = "iPhone"

	replaced, err := store.ReplaceDevice(latest)
	if err != nil {
		t.Fatalf("ReplaceDevice 失败: %v", err)
	}
	if len(replaced) != 1 || replaced[0] != old.DeviceID {
		t.Fatalf("replaced = %v, want [%s]", replaced, old.DeviceID)
	}
	devices, _ := store.ListDevices()
	if len(devices) != 1 || devices[0].DeviceID != latest.DeviceID {
		t.Fatalf("active devices = %#v", devices)
	}
}

func TestMemoryStore_LookupByID_NotFound(t *testing.T) {
	store := newTestStore()
	got, err := store.LookupByDeviceID("nonexistent")
	if err != nil {
		t.Fatalf("LookupByDeviceID 不应返回错误: %v", err)
	}
	if got != nil {
		t.Error("不存在的 deviceID 应返回 nil")
	}
}

func TestMemoryStore_LookupByTokenHash(t *testing.T) {
	store := newTestStore()
	rec := makeTestRecord("dev1")
	_ = store.AddDevice(rec)
	got, err := store.LookupByTokenHash(rec.TokenHash)
	if err != nil {
		t.Fatalf("LookupByTokenHash 失败: %v", err)
	}
	if got == nil {
		t.Fatal("LookupByTokenHash 返回 nil")
	}
	if got.DeviceID != "dev1" {
		t.Errorf("DeviceID 不匹配: got %q, want %q", got.DeviceID, "dev1")
	}
}

func TestMemoryStore_LookupByTokenHash_NotFound(t *testing.T) {
	store := newTestStore()
	got, err := store.LookupByTokenHash("sha256:nonexistent")
	if err != nil {
		t.Fatalf("LookupByTokenHash 不应返回错误: %v", err)
	}
	if got != nil {
		t.Error("不存在的 hash 应返回 nil")
	}
}

func TestMemoryStore_EnableRelayBindsIdentityOnce(t *testing.T) {
	store := newTestStore()
	_ = store.AddDevice(makeTestRecord("dev1"))

	if err := store.EnableRelay("dev1", "identity-a", 1); err != nil {
		t.Fatalf("EnableRelay 失败: %v", err)
	}
	got, _ := store.LookupByDeviceID("dev1")
	if !got.RelayEnabled || got.IdentityPublicKey != "identity-a" || got.RelayChannelGeneration != 1 {
		t.Fatalf("relay binding = %#v", got)
	}
	if err := store.EnableRelay("dev1", "identity-b", 1); err == nil {
		t.Fatal("已绑定设备不得替换 identity public key")
	}
}

func TestMemoryStore_RevokeDevice(t *testing.T) {
	store := newTestStore()
	rec := makeTestRecord("dev1")
	_ = store.AddDevice(rec)

	if err := store.RevokeDevice("dev1"); err != nil {
		t.Fatalf("RevokeDevice 失败: %v", err)
	}
	got, _ := store.LookupByDeviceID("dev1")
	if got == nil {
		t.Fatal("吊销后应仍能查到设备")
	}
	if got.RevokedAt == nil {
		t.Error("RevokedAt 应非 nil")
	}
	if got.RevokedAt.IsZero() {
		t.Error("RevokedAt 应有值")
	}
}

func TestMemoryStore_RevokeDevice_NotFound(t *testing.T) {
	store := newTestStore()
	err := store.RevokeDevice("nonexistent")
	if err == nil {
		t.Error("吊销不存在的设备应返回错误")
	}
}

func TestMemoryStore_ListDevices(t *testing.T) {
	store := newTestStore()
	_ = store.AddDevice(makeTestRecord("dev1"))
	_ = store.AddDevice(makeTestRecord("dev2"))

	list, err := store.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices 失败: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListDevices 应返回 2 条记录, got %d", len(list))
	}
}

func TestMemoryStore_ListDevices_ExcludesRevoked(t *testing.T) {
	store := newTestStore()
	_ = store.AddDevice(makeTestRecord("dev1"))
	_ = store.AddDevice(makeTestRecord("dev2"))
	_ = store.RevokeDevice("dev1")

	list, _ := store.ListDevices()
	if len(list) != 1 {
		t.Errorf("ListDevices 应排除已吊销设备, got %d 条", len(list))
	}
	if list[0].DeviceID != "dev2" {
		t.Errorf("剩余设备应为 dev2, got %s", list[0].DeviceID)
	}
}

func TestValidateDeviceAuth_MissingToken(t *testing.T) {
	store := newTestStore()
	_, err := ValidateDeviceAuth(store, "", "dev1")
	authErr, ok := err.(AuthError)
	if !ok {
		t.Fatalf("应返回 AuthError, got %T: %v", err, err)
	}
	if authErr.Code != "auth.missing_token" {
		t.Errorf("错误码应为 auth.missing_token, got %q", authErr.Code)
	}
}

func TestValidateDeviceAuth_InvalidTokenFormat(t *testing.T) {
	store := newTestStore()
	_, err := ValidateDeviceAuth(store, "not_a_valid_token", "dev1")
	authErr, ok := err.(AuthError)
	if !ok {
		t.Fatalf("应返回 AuthError, got %T: %v", err, err)
	}
	if authErr.Code != "auth.invalid_token" {
		t.Errorf("错误码应为 auth.invalid_token, got %q", authErr.Code)
	}
}

func TestValidateDeviceAuth_UnknownToken(t *testing.T) {
	store := newTestStore()
	plain, _, _ := GenerateDeviceToken()
	_, err := ValidateDeviceAuth(store, plain, "")
	authErr, ok := err.(AuthError)
	if !ok {
		t.Fatalf("应返回 AuthError, got %T: %v", err, err)
	}
	if authErr.Code != "auth.invalid_token" {
		t.Errorf("错误码应为 auth.invalid_token, got %q", authErr.Code)
	}
}

func TestValidateDeviceAuth_RevokedDevice(t *testing.T) {
	store := newTestStore()
	plain, hash, _ := GenerateDeviceToken()
	rec := TrustedDeviceRecord{
		DeviceID:    "dev1",
		DisplayName: "Revoked Device",
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	_ = store.AddDevice(rec)
	_ = store.RevokeDevice("dev1")

	_, err := ValidateDeviceAuth(store, plain, "dev1")
	authErr, ok := err.(AuthError)
	if !ok {
		t.Fatalf("应返回 AuthError, got %T: %v", err, err)
	}
	if authErr.Code != "auth.revoked" {
		t.Errorf("错误码应为 auth.revoked, got %q", authErr.Code)
	}
}

func TestValidateDeviceAuth_Success(t *testing.T) {
	store := newTestStore()
	plain, hash, _ := GenerateDeviceToken()
	rec := TrustedDeviceRecord{
		DeviceID:    "dev1",
		DisplayName: "Good Device",
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	_ = store.AddDevice(rec)

	got, err := ValidateDeviceAuth(store, plain, "dev1")
	if err != nil {
		t.Fatalf("合法 token 认证失败: %v", err)
	}
	if got.DeviceID != "dev1" {
		t.Errorf("DeviceID 不匹配: got %q, want %q", got.DeviceID, "dev1")
	}
}

func TestValidateDeviceAuth_SuccessWithoutDeviceID(t *testing.T) {
	store := newTestStore()
	plain, hash, _ := GenerateDeviceToken()
	rec := TrustedDeviceRecord{
		DeviceID:    "dev1",
		DisplayName: "Good Device",
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	_ = store.AddDevice(rec)

	// deviceID 为空时跳过一致性校验
	got, err := ValidateDeviceAuth(store, plain, "")
	if err != nil {
		t.Fatalf("合法 token（无 deviceID）认证失败: %v", err)
	}
	if got.DeviceID != "dev1" {
		t.Errorf("DeviceID 不匹配: got %q, want %q", got.DeviceID, "dev1")
	}
}

func TestFileDeviceStorePersistsDevicesAcrossRestart(t *testing.T) {
	path := t.TempDir() + "/devices.json"
	plain, hash, _ := GenerateDeviceToken()

	store, err := NewFileDeviceStore(path)
	if err != nil {
		t.Fatalf("NewFileDeviceStore 失败: %v", err)
	}
	if err := store.AddDevice(TrustedDeviceRecord{
		DeviceID:    "dev1",
		DisplayName: "Good Device",
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}); err != nil {
		t.Fatalf("AddDevice 失败: %v", err)
	}
	if err := store.EnableRelay("dev1", "identity-persisted", 1); err != nil {
		t.Fatalf("EnableRelay 失败: %v", err)
	}

	restarted, err := NewFileDeviceStore(path)
	if err != nil {
		t.Fatalf("NewFileDeviceStore restart 失败: %v", err)
	}
	got, err := ValidateDeviceAuth(restarted, plain, "dev1")
	if err != nil {
		t.Fatalf("重启后合法 token 认证失败: %v", err)
	}
	if got.DeviceID != "dev1" {
		t.Errorf("DeviceID 不匹配: got %q, want %q", got.DeviceID, "dev1")
	}
	if !got.RelayEnabled || got.IdentityPublicKey != "identity-persisted" || got.RelayChannelGeneration != 1 {
		t.Fatalf("重启后 relay binding 丢失: %#v", got)
	}
}

func TestValidateDeviceAuth_DeviceIDMismatch(t *testing.T) {
	store := newTestStore()
	plain, hash, _ := GenerateDeviceToken()
	rec := TrustedDeviceRecord{
		DeviceID:    "dev1",
		DisplayName: "Device One",
		Platform:    "ios",
		TokenHash:   hash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	_ = store.AddDevice(rec)

	_, err := ValidateDeviceAuth(store, plain, "wrong_device")
	authErr, ok := err.(AuthError)
	if !ok {
		t.Fatalf("应返回 AuthError, got %T: %v", err, err)
	}
	if authErr.Code != "auth.invalid_token" {
		t.Errorf("错误码应为 auth.invalid_token (deviceID 不匹配), got %q", authErr.Code)
	}
}
