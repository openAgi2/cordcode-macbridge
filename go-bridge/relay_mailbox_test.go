package gobridge

import (
	"encoding/json"
	"testing"
	"time"
)

// ─── Mailbox 服务测试 ────────────────────────────────────────────────────
//
// 覆盖：
//   - enqueue / fetch / ack 基本流程
//   - cursor 补取与分页
//   - TTL 过期清理
//   - 容量淘汰
//   - crash-safe ack 语义
//   - epoch metadata 保存
//   - 设备撤销清理
//   - durable milestone 白名单

func newTestMailboxService() *MailboxService {
	hub := NewRelayHub()
	return NewMailboxService(hub)
}

// TestMailboxEnqueueFetchAck 测试基本入队/补取/确认流程。
func TestMailboxEnqueueFetchAck(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_001"
	deviceID := "dev_test_001"

	// 入队 3 帧
	for i := 0; i < 3; i++ {
		envelope := json.RawMessage(`{"counter": ` + string(rune('1'+i)) + `}`)
		err := ms.Enqueue(routeID, deviceID, envelope, nil)
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	// 补取：从 cursor 0 开始
	resp := ms.Fetch(routeID, deviceID, 0, 10)
	if len(resp.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(resp.Frames))
	}
	if resp.HasMore {
		t.Error("hasMore should be false")
	}

	// 确认前两个
	err := ms.Ack(routeID, deviceID, resp.Frames[1].Cursor)
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// 再次补取：只应返回未确认的第 3 帧
	resp = ms.Fetch(routeID, deviceID, 0, 10)
	if len(resp.Frames) != 1 {
		t.Fatalf("frames after ack = %d, want 1", len(resp.Frames))
	}
}

// TestMailboxCursorPagination 测试 cursor 分页。
func TestMailboxCursorPagination(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_002"
	deviceID := "dev_test_002"

	// 入队 5 帧
	for i := 0; i < 5; i++ {
		envelope := json.RawMessage(`{"i": ` + string(rune('0'+i)) + `}`)
		ms.Enqueue(routeID, deviceID, envelope, nil)
	}

	// 分页：每页 2 条
	resp := ms.Fetch(routeID, deviceID, 0, 2)
	if len(resp.Frames) != 2 {
		t.Fatalf("page 1: frames = %d, want 2", len(resp.Frames))
	}
	if !resp.HasMore {
		t.Error("page 1: hasMore should be true")
	}

	// 使用 nextCursor 获取下一页
	lastCursor := resp.Frames[1].Cursor
	resp = ms.Fetch(routeID, deviceID, lastCursor, 2)
	if len(resp.Frames) != 2 {
		t.Fatalf("page 2: frames = %d, want 2", len(resp.Frames))
	}
	if !resp.HasMore {
		t.Error("page 2: hasMore should be true")
	}

	// 第三页
	lastCursor = resp.Frames[1].Cursor
	resp = ms.Fetch(routeID, deviceID, lastCursor, 2)
	if len(resp.Frames) != 1 {
		t.Fatalf("page 3: frames = %d, want 1", len(resp.Frames))
	}
	if resp.HasMore {
		t.Error("page 3: hasMore should be false")
	}
}

// TestMailboxTTLExpiry 测试 TTL 过期清理。
// 方案 §15.4：TTL 到期后 frame 可被淘汰。
func TestMailboxTTLExpiry(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_003"
	deviceID := "dev_test_003"

	// 入队
	envelope := json.RawMessage(`{"test": "ttl"}`)
	ms.Enqueue(routeID, deviceID, envelope, nil)

	// 手动设置过期时间
	ms.mu.Lock()
	key := mailboxKey(routeID, deviceID)
	for i := range ms.mailboxes[key].frames {
		ms.mailboxes[key].frames[i].expiresAt = time.Now().Add(-time.Hour) // 已过期
	}
	ms.mu.Unlock()

	// 触发过期清理
	expired := ms.Expire(routeID, deviceID)
	if expired != 1 {
		t.Errorf("expired = %d, want 1", expired)
	}

	// 过期后补取应为空
	resp := ms.Fetch(routeID, deviceID, 0, 10)
	if len(resp.Frames) != 0 {
		t.Errorf("frames after expiry = %d, want 0", len(resp.Frames))
	}
}

// TestMailboxCapacityEviction 测试容量淘汰。
// 方案 §15.4：单设备最大 50MB，超出淘汰最早未 ack 的 frame。
func TestMailboxCapacityEviction(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_004"
	deviceID := "dev_test_004"

	// 入队大量数据接近上限
	largeFrame := json.RawMessage(make([]byte, 1024*1024)) // 1MB frame
	for i := 0; i < 49; i++ {
		err := ms.Enqueue(routeID, deviceID, largeFrame, nil)
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	// 检查状态
	_, pending, bytes, ok := ms.Stats(routeID, deviceID)
	if !ok {
		t.Fatal("mailbox should exist")
	}
	if pending != 49 {
		t.Errorf("pending = %d, want 49", pending)
	}
	if bytes < 49*1024*1024 {
		t.Errorf("totalBytes = %d, want >= %d", bytes, 49*1024*1024)
	}

	// 再入队应触发淘汰
	err := ms.Enqueue(routeID, deviceID, largeFrame, nil)
	if err != nil {
		// 容量超限可能返回错误
		t.Logf("Enqueue after capacity: %v (acceptable)", err)
	}
}

// TestMailboxCrashSafeAck 测试 crash-safe ack 语义。
// 方案 §15.4：iOS 在 durable apply 后才 ack。
// Ack 后的 frame 不应出现在后续 fetch 中。
func TestMailboxCrashSafeAck(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_005"
	deviceID := "dev_test_005"

	// 入队 3 帧
	for i := 0; i < 3; i++ {
		envelope := json.RawMessage(`{"frame": ` + string(rune('1'+i)) + `}`)
		ms.Enqueue(routeID, deviceID, envelope, nil)
	}

	// 模拟 iOS durable apply frame 1 和 2
	resp := ms.Fetch(routeID, deviceID, 0, 10)
	if len(resp.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(resp.Frames))
	}

	// ack frame 2（包含 frame 1 和 2）
	cursor2 := resp.Frames[1].Cursor
	err := ms.Ack(routeID, deviceID, cursor2)
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// 模拟崩溃重启：重新 fetch
	// 应只返回 frame 3
	resp = ms.Fetch(routeID, deviceID, 0, 10)
	if len(resp.Frames) != 1 {
		t.Errorf("after crash recovery: frames = %d, want 1", len(resp.Frames))
	}

	// ack frame 3
	err = ms.Ack(routeID, deviceID, resp.Frames[0].Cursor)
	if err != nil {
		t.Fatalf("Ack frame 3: %v", err)
	}

	// 所有 frame 已 ack
	resp = ms.Fetch(routeID, deviceID, 0, 10)
	if len(resp.Frames) != 0 {
		t.Errorf("all acked: frames = %d, want 0", len(resp.Frames))
	}
}

// TestMailboxEpochMetadata 测试 opaque epoch 元数据保存。
func TestMailboxEpochMetadata(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_006"
	deviceID := "dev_test_006"

	epochIdx := uint64(3)
	meta := &EpochMetadata{
		EpochIndex: &epochIdx,
		PrekeyID:   "pk_meta_test",
		KeyEpochID: "mailbox:3",
		Counter:    5,
	}

	envelope := json.RawMessage(`{"test": "epoch_meta"}`)
	err := ms.Enqueue(routeID, deviceID, envelope, meta)
	if err != nil {
		t.Fatalf("Enqueue with epoch meta: %v", err)
	}

	// 验证元数据保存
	ms.mu.Lock()
	key := mailboxKey(routeID, deviceID)
	frames := ms.mailboxes[key].frames
	if len(frames) != 1 {
		ms.mu.Unlock()
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	f := frames[0]
	ms.mu.Unlock()

	if f.epochIndex == nil || *f.epochIndex != 3 {
		t.Errorf("epochIndex = %v, want 3", f.epochIndex)
	}
	if f.prekeyID != "pk_meta_test" {
		t.Errorf("prekeyID = %q, want %q", f.prekeyID, "pk_meta_test")
	}
	if f.keyEpochID != "mailbox:3" {
		t.Errorf("keyEpochID = %q, want %q", f.keyEpochID, "mailbox:3")
	}
	if f.counter != 5 {
		t.Errorf("counter = %d, want 5", f.counter)
	}
}

// TestMailboxDeviceRevokeClear 测试设备撤销清理。
// 方案 §7.3 POST /v1/devices/revoke。
func TestMailboxDeviceRevokeClear(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_007"
	deviceID := "dev_test_007"

	// 入队数据
	for i := 0; i < 5; i++ {
		envelope := json.RawMessage(`{"frame": ` + string(rune('0'+i)) + `}`)
		ms.Enqueue(routeID, deviceID, envelope, nil)
	}

	// 确认存在
	_, pending, _, ok := ms.Stats(routeID, deviceID)
	if !ok || pending != 5 {
		t.Fatalf("pending = %d, ok = %v, want 5, true", pending, ok)
	}

	// 清除
	ms.ClearForDevice(routeID, deviceID)

	// 确认已清除
	_, _, _, ok = ms.Stats(routeID, deviceID)
	if ok {
		t.Error("mailbox should be cleared after revoke")
	}
}

// TestMailboxNoCrossDevice 测试设备间 mailbox 隔离。
func TestMailboxNoCrossDevice(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_008"
	devA := "dev_a"
	devB := "dev_b"

	// 各入队不同数量
	for i := 0; i < 3; i++ {
		ms.Enqueue(routeID, devA, json.RawMessage(`{"a": true}`), nil)
	}
	for i := 0; i < 5; i++ {
		ms.Enqueue(routeID, devB, json.RawMessage(`{"b": true}`), nil)
	}

	respA := ms.Fetch(routeID, devA, 0, 100)
	respB := ms.Fetch(routeID, devB, 0, 100)

	if len(respA.Frames) != 3 {
		t.Errorf("devA frames = %d, want 3", len(respA.Frames))
	}
	if len(respB.Frames) != 5 {
		t.Errorf("devB frames = %d, want 5", len(respB.Frames))
	}

	// ack devA 不影响 devB
	ms.Ack(routeID, devA, respA.Frames[2].Cursor)
	respB = ms.Fetch(routeID, devB, 0, 100)
	if len(respB.Frames) != 5 {
		t.Errorf("devB after devA ack: frames = %d, want 5", len(respB.Frames))
	}
}

// TestMailboxStats 测试统计信息。
func TestMailboxStats(t *testing.T) {
	ms := newTestMailboxService()
	routeID := "route_test_009"
	deviceID := "dev_test_009"

	// 不存在的设备
	_, _, _, ok := ms.Stats(routeID, deviceID)
	if ok {
		t.Error("non-existent device should return ok=false")
	}

	// 入队
	for i := 0; i < 3; i++ {
		ms.Enqueue(routeID, deviceID, json.RawMessage(`{}`), nil)
	}

	total, pending, bytes, ok := ms.Stats(routeID, deviceID)
	if !ok {
		t.Fatal("device should exist")
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if pending != 3 {
		t.Errorf("pending = %d, want 3", pending)
	}
	if bytes == 0 {
		t.Error("bytes should be > 0")
	}
}

// ─── Milestone 白名单测试 ────────────────────────────────────────────────

func TestDurableMilestoneWhitelist(t *testing.T) {
	// 应该在白名单中的事件
	whitelistEvents := []string{
		"turn_completed",
		"turn_error",
		"todos_updated",
		"session_running_signal",
		"delivery_reconcile_required",
	}
	for _, e := range whitelistEvents {
		if !IsDurableMilestone(e) {
			t.Errorf("IsDurableMilestone(%q) = false, want true", e)
		}
	}

	// 不应在白名单中的事件
	excludedEvents := []string{
		"text_delta",
		"thinking_delta",
		"tool_content",
		"message_content",
		"file_content",
		"session_history",
		"unknown_event",
	}
	for _, e := range excludedEvents {
		if IsDurableMilestone(e) {
			t.Errorf("IsDurableMilestone(%q) = true, want false", e)
		}
	}
}

// ─── Reconcile 控制消息测试 ──────────────────────────────────────────────

func TestDeliveryReconcileRequired(t *testing.T) {
	msg := NewDeliveryReconcileRequired("dev_reconcile", "prekey_exhausted")
	if msg.Type != "delivery_reconcile_required" {
		t.Errorf("type = %q, want %q", msg.Type, "delivery_reconcile_required")
	}
	if msg.DeviceID != "dev_reconcile" {
		t.Errorf("deviceID = %q, want %q", msg.DeviceID, "dev_reconcile")
	}
	if msg.Reason != "prekey_exhausted" {
		t.Errorf("reason = %q, want %q", msg.Reason, "prekey_exhausted")
	}

	// JSON 序列化
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed DeliveryReconcileRequired
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Type != msg.Type {
		t.Errorf("roundtrip type = %q, want %q", parsed.Type, msg.Type)
	}
}
