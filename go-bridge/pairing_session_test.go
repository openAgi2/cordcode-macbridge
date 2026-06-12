package gobridge

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 测试 NewPairingSession 生成的字段正确性
func TestNewPairingSession(t *testing.T) {
	s := NewPairingSession("bridge-1", "My Bridge", "ws://192.168.1.5:8777", "", 5*time.Minute)

	// ID 应有 pair_ 前缀
	if !strings.HasPrefix(s.ID, "pair_") {
		t.Errorf("ID 应有 pair_ 前缀，实际: %s", s.ID)
	}
	if len(s.ID) <= len("pair_") {
		t.Errorf("ID 在前缀之后应有随机部分")
	}

	// ManualCode 应为 6 位数字
	if len(s.ManualCode) != 6 {
		t.Errorf("ManualCode 长度应为 6，实际: %d", len(s.ManualCode))
	}
	for _, c := range s.ManualCode {
		if c < '0' || c > '9' {
			t.Errorf("ManualCode 应为纯数字，实际: %s", s.ManualCode)
			break
		}
	}

	// QRPayload 应包含 id 和 code
	if !strings.Contains(s.QRPayload, "id="+s.ID) {
		t.Errorf("QRPayload 应包含 id=%s", s.ID)
	}
	if !strings.Contains(s.QRPayload, "code="+s.ManualCode) {
		t.Errorf("QRPayload 应包含 code=%s", s.ManualCode)
	}

	// 基础字段
	if s.State != PairingCreated {
		t.Errorf("初始状态应为 created，实际: %s", s.State)
	}
	if s.BridgeID != "bridge-1" {
		t.Errorf("BridgeID 不匹配")
	}
	if s.DisplayName != "My Bridge" {
		t.Errorf("DisplayName 不匹配")
	}
	if s.LocalURL != "ws://192.168.1.5:8777" {
		t.Errorf("LocalURL 不匹配")
	}

	// 过期时间应在未来
	if !s.ExpiresAt.After(s.CreatedAt) {
		t.Errorf("ExpiresAt 应晚于 CreatedAt")
	}
	if !s.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt 应在未来")
	}
}

func TestNewPairingSessionWithRemoteURLs_EncodesAllRemoteCandidates(t *testing.T) {
	s := NewPairingSessionWithRemoteURLs(
		"bridge-1",
		"My Bridge",
		"ws://192.168.1.5:8777",
		[]string{
			"wss://100.79.255.127:8778/bridge",
			"wss://bridge.example.com/bridge",
		},
		5*time.Minute,
	)

	parsed, err := url.Parse(s.QRPayload)
	if err != nil {
		t.Fatalf("QRPayload 解析失败: %v", err)
	}

	remotes := parsed.Query()["remote"]
	if len(remotes) != 2 {
		t.Fatalf("remote 参数数量 = %d, want 2; payload=%s", len(remotes), s.QRPayload)
	}
	if remotes[0] != "wss://100.79.255.127:8778/bridge" {
		t.Errorf("remote[0] = %q", remotes[0])
	}
	if remotes[1] != "wss://bridge.example.com/bridge" {
		t.Errorf("remote[1] = %q", remotes[1])
	}
}

// Created → Claimed 正常转换
func TestClaimTransition(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)

	err := s.Claim("phone-001", "iPhone", "ios")
	if err != nil {
		t.Fatalf("Claim 不应报错: %v", err)
	}

	if s.State != PairingClaimed {
		t.Errorf("状态应为 claimed，实际: %s", s.State)
	}
	if s.ClaimingDeviceID != "phone-001" {
		t.Errorf("ClaimingDeviceID 不匹配")
	}
	if s.ClaimingDeviceName != "iPhone" {
		t.Errorf("ClaimingDeviceName 不匹配")
	}
	if s.ClaimingPlatform != "ios" {
		t.Errorf("ClaimingPlatform 不匹配")
	}
}

// 重复 Claim 返回 pairing.already_claimed
func TestClaimAlreadyClaimed(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	_ = s.Claim("d1", "Phone", "ios")

	err := s.Claim("d2", "Tablet", "android")
	if err == nil {
		t.Fatal("重复 Claim 应返回错误")
	}

	perr, ok := err.(PairingError)
	if !ok {
		t.Fatalf("错误类型应为 PairingError，实际: %T", err)
	}
	if perr.Code != "pairing.already_claimed" {
		t.Errorf("错误码应为 pairing.already_claimed，实际: %s", perr.Code)
	}
}

// 过期会话 Claim 返回 pairing.expired
func TestClaimExpired(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", -1*time.Second)

	err := s.Claim("d1", "Phone", "ios")
	if err == nil {
		t.Fatal("过期会话 Claim 应返回错误")
	}

	perr, ok := err.(PairingError)
	if !ok {
		t.Fatalf("错误类型应为 PairingError，实际: %T", err)
	}
	if perr.Code != "pairing.expired" {
		t.Errorf("错误码应为 pairing.expired，实际: %s", perr.Code)
	}
}

// Claimed → Approved 生成 token、hash、deviceID
func TestApproveTransition(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	_ = s.Claim("d1", "Phone", "ios")

	err := s.Approve()
	if err != nil {
		t.Fatalf("Approve 不应报错: %v", err)
	}

	if s.State != PairingApproved {
		t.Errorf("状态应为 approved，实际: %s", s.State)
	}
	if s.DeviceToken == "" {
		t.Error("Approve 后 DeviceToken 不应为空")
	}
	if !strings.HasPrefix(s.DeviceToken, "ccb1_") {
		t.Errorf("DeviceToken 应有 ccb1_ 前缀")
	}
	if s.DeviceTokenHash == "" {
		t.Error("DeviceTokenHash 不应为空")
	}
	if !strings.HasPrefix(s.DeviceTokenHash, "sha256:") {
		t.Errorf("DeviceTokenHash 应以 sha256: 开头")
	}
	if s.DeviceID != "d1" {
		t.Errorf("DeviceID 应保留客户端稳定 ID，实际: %s", s.DeviceID)
	}
}

// Approved → Complete 清空明文 token
func TestCompleteTransition(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	_ = s.Claim("d1", "Phone", "ios")
	_ = s.Approve()

	token := s.DeviceToken
	if token == "" {
		t.Fatal("Approve 后 token 不应为空（前提条件）")
	}

	err := s.Complete()
	if err != nil {
		t.Fatalf("Complete 不应报错: %v", err)
	}

	if s.State != PairingCompleted {
		t.Errorf("状态应为 completed，实际: %s", s.State)
	}
	if s.DeviceToken != "" {
		t.Error("Complete 后 DeviceToken 应被清空")
	}
	// hash 和 deviceID 应保留
	if s.DeviceTokenHash == "" {
		t.Error("DeviceTokenHash 应保留")
	}
	if s.DeviceID == "" {
		t.Error("DeviceID 应保留")
	}
}

// Claimed → Rejected
func TestRejectTransition(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	_ = s.Claim("d1", "Phone", "ios")

	err := s.Reject()
	if err != nil {
		t.Fatalf("Reject 不应报错: %v", err)
	}
	if s.State != PairingRejected {
		t.Errorf("状态应为 rejected，实际: %s", s.State)
	}
}

// 非 Claimed 状态 Reject 返回 pairing.invalid_state
func TestRejectInvalidState(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	// 状态为 Created，不是 Claimed

	err := s.Reject()
	if err == nil {
		t.Fatal("Created 状态 Reject 应返回错误")
	}
	perr, ok := err.(PairingError)
	if !ok {
		t.Fatalf("错误类型应为 PairingError")
	}
	if perr.Code != "pairing.invalid_state" {
		t.Errorf("错误码应为 pairing.invalid_state，实际: %s", perr.Code)
	}
}

// Approve 在非 Claimed 状态报错
func TestApproveInvalidState(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	// Created 状态

	err := s.Approve()
	if err == nil {
		t.Fatal("Created 状态 Approve 应返回错误")
	}
	perr, ok := err.(PairingError)
	if !ok {
		t.Fatalf("错误类型应为 PairingError")
	}
	if perr.Code != "pairing.invalid_state" {
		t.Errorf("错误码应为 pairing.invalid_state，实际: %s", perr.Code)
	}
}

// Complete 在非 Approved 状态报错
func TestCompleteInvalidState(t *testing.T) {
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	_ = s.Claim("d1", "Phone", "ios")
	// Claimed，不是 Approved

	err := s.Complete()
	if err == nil {
		t.Fatal("Claimed 状态 Complete 应返回错误")
	}
	perr, ok := err.(PairingError)
	if !ok {
		t.Fatalf("错误类型应为 PairingError")
	}
	if perr.Code != "pairing.invalid_state" {
		t.Errorf("错误码应为 pairing.invalid_state，实际: %s", perr.Code)
	}
}

// IsExpired 正确判断过期
func TestIsExpired(t *testing.T) {
	future := NewPairingSession("b1", "B", "ws://x:8777", "", 10*time.Minute)
	if future.IsExpired() {
		t.Error("未过期会话不应报告已过期")
	}

	past := NewPairingSession("b1", "B", "ws://x:8777", "", -1*time.Second)
	if !past.IsExpired() {
		t.Error("已过期会话应报告已过期")
	}
}

// MemoryPairingStore: Create + Get 正常工作
func TestStoreCreateAndGet(t *testing.T) {
	store := NewMemoryPairingStore()
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 5*time.Minute)

	if err := store.Create(s); err != nil {
		t.Fatalf("Create 报错: %v", err)
	}

	got, err := store.Get(s.ID)
	if err != nil {
		t.Fatalf("Get 报错: %v", err)
	}
	if got == nil {
		t.Fatal("Get 返回 nil")
	}
	if got.ID != s.ID {
		t.Errorf("Get 返回的 ID 不匹配")
	}

	// 不存在的 ID
	missing, _ := store.Get("pair_nonexistent")
	if missing != nil {
		t.Error("不存在的 ID 应返回 nil")
	}
}

// GetByManualCode 按手动码查找
func TestStoreGetByManualCode(t *testing.T) {
	store := NewMemoryPairingStore()
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 5*time.Minute)
	_ = store.Create(s)

	got, _ := store.GetByManualCode(s.ManualCode)
	if got == nil {
		t.Fatal("GetByManualCode 应找到会话")
	}
	if got.ID != s.ID {
		t.Errorf("找到的会话 ID 不匹配")
	}

	// 不存在的码
	missing, _ := store.GetByManualCode("000000")
	if missing != nil {
		t.Error("不存在的码应返回 nil")
	}
}

// 重复 Create 同一 ID 报错
func TestStoreCreateDuplicate(t *testing.T) {
	store := NewMemoryPairingStore()
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 5*time.Minute)
	_ = store.Create(s)

	err := store.Create(s)
	if err == nil {
		t.Fatal("重复 Create 应返回错误")
	}
}

// Update 更新会话状态
func TestStoreUpdate(t *testing.T) {
	store := NewMemoryPairingStore()
	s := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 5*time.Minute)
	_ = store.Create(s)

	_ = s.Claim("d1", "Phone", "ios")
	if err := store.Update(s); err != nil {
		t.Fatalf("Update 报错: %v", err)
	}

	got, _ := store.Get(s.ID)
	if got.State != PairingClaimed {
		t.Errorf("更新后状态应为 claimed，实际: %s", got.State)
	}

	// 更新不存在的会话
	fake := &PairingSession{ID: "pair_fake", ManualCode: "123456"}
	err := store.Update(fake)
	if err == nil {
		t.Fatal("更新不存在的会话应报错")
	}
}

// DeleteExpired 只删除过期会话
func TestStoreDeleteExpired(t *testing.T) {
	store := NewMemoryPairingStore()

	fresh := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 10*time.Minute)
	expired := NewPairingSession("b1", "Bridge", "ws://x:8777", "", -1*time.Second)

	_ = store.Create(fresh)
	_ = store.Create(expired)

	_ = store.DeleteExpired()

	got, _ := store.Get(fresh.ID)
	if got == nil {
		t.Error("未过期会话不应被删除")
	}

	got, _ = store.Get(expired.ID)
	if got != nil {
		t.Error("已过期会话应被删除")
	}
}

// CleanupAll 清空所有会话
func TestStoreCleanupAll(t *testing.T) {
	store := NewMemoryPairingStore()
	s1 := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 5*time.Minute)
	s2 := NewPairingSession("b1", "Bridge", "ws://x:8777", "", 5*time.Minute)
	_ = store.Create(s1)
	_ = store.Create(s2)

	store.CleanupAll()

	got, _ := store.Get(s1.ID)
	if got != nil {
		t.Error("CleanupAll 后不应找到会话")
	}
	got, _ = store.Get(s2.ID)
	if got != nil {
		t.Error("CleanupAll 后不应找到会话")
	}
}

// CleanupExpiredSessions 辅助函数正常工作
func TestCleanupExpiredSessions(t *testing.T) {
	store := NewMemoryPairingStore()
	expired := NewPairingSession("b1", "B", "ws://x:8777", "", -1*time.Second)
	_ = store.Create(expired)

	CleanupExpiredSessions(store)

	got, _ := store.Get(expired.ID)
	if got != nil {
		t.Error("过期会话应被清理")
	}
}

// JSON 序列化验证：DeviceToken 不出现在 JSON 中
func TestPairingSessionJSON(t *testing.T) {
	s := NewPairingSession("bridge-1", "My Bridge", "ws://192.168.1.5:8777", "", 5*time.Minute)
	_ = s.Claim("phone-1", "iPhone 16", "ios")
	_ = s.Approve()

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("JSON marshal 报错: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("JSON unmarshal 报错: %v", err)
	}

	// ID 和手动码应在 JSON 中
	if m["id"] != s.ID {
		t.Errorf("JSON id 字段不匹配")
	}
	if m["manualCode"] != s.ManualCode {
		t.Errorf("JSON manualCode 字段不匹配")
	}
	if m["state"] != "approved" {
		t.Errorf("JSON state 应为 approved，实际: %v", m["state"])
	}
	if m["bridgeId"] != "bridge-1" {
		t.Errorf("JSON bridgeId 不匹配")
	}
	if m["displayName"] != "My Bridge" {
		t.Errorf("JSON displayName 不匹配")
	}
	if m["localUrl"] != "ws://192.168.1.5:8777" {
		t.Errorf("JSON localUrl 不匹配")
	}
	if m["deviceId"] == nil || m["deviceId"].(string) == "" {
		t.Error("JSON deviceId 应存在")
	}
	if m["deviceTokenHash"] == nil || m["deviceTokenHash"].(string) == "" {
		t.Error("JSON deviceTokenHash 应存在")
	}

	// DeviceToken 明文不应出现在 JSON 中
	if _, ok := m["deviceToken"]; ok {
		t.Error("DeviceToken 明文不应出现在 JSON 输出中")
	}

	// camelCase 检查：确认没有 snake_case 键
	jsonStr := string(data)
	if strings.Contains(jsonStr, "claiming_device") {
		t.Error("JSON 中不应出现 snake_case 字段名")
	}
	if strings.Contains(jsonStr, "device_id") {
		t.Error("JSON 中不应出现 snake_case 字段名")
	}
}

func TestPairingSessionQRPayload_ContainsHostPortName(t *testing.T) {
	session := NewPairingSession("brg_test", "My Mac", "ws://192.168.1.50:8777", "", 5*time.Minute)

	qr := session.QRPayload
	if !strings.HasPrefix(qr, "cccode://pair?") {
		t.Fatalf("qrPayload 格式错误: %s", qr)
	}

	parsed, err := url.Parse(qr)
	if err != nil {
		t.Fatalf("qrPayload 不是合法 URL: %v", err)
	}

	q := parsed.Query()
	if got := q.Get("id"); got == "" {
		t.Error("qrPayload 缺少 id")
	}
	if got := q.Get("code"); got == "" {
		t.Error("qrPayload 缺少 code")
	}
	if got := q.Get("host"); got != "192.168.1.50" {
		t.Errorf("host = %q, want 192.168.1.50", got)
	}
	if got := q.Get("port"); got != "8777" {
		t.Errorf("port = %q, want 8777", got)
	}
	if got := q.Get("name"); got != "My Mac" {
		t.Errorf("name = %q, want My Mac", got)
	}
}

func TestPairingSessionQRPayload_EmptyLocalURL(t *testing.T) {
	session := NewPairingSession("brg_test", "Test", "", "", 5*time.Minute)
	if !strings.HasPrefix(session.QRPayload, "cccode://pair?id=") {
		t.Fatalf("qrPayload 格式错误: %s", session.QRPayload)
	}
	if strings.Contains(session.QRPayload, "host=") {
		t.Error("空 localURL 时 qrPayload 不应包含 host")
	}
}
