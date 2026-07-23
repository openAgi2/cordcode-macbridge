package gobridge

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Observation Scope 测试 ─────────────────────────────────────────────

func TestObservationSetAndGet(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	om.SetScope("dev_1", ObservationScope{
		BackendID:    "codex",
		SessionIDs:   []string{"sess_1"},
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 45,
	})

	scope := om.GetScope("dev_1", "codex")
	if scope == nil {
		t.Fatal("scope should exist")
	}
	if scope.DeliveryMode != scopeFullStream {
		t.Errorf("mode = %q, want %q", scope.DeliveryMode, scopeFullStream)
	}
	if scope.LeaseSeconds != 45 {
		t.Errorf("lease = %d, want 45", scope.LeaseSeconds)
	}
}

func TestObservationIncludeRunningSignalsAllowsUnlistedMilestonesOnly(t *testing.T) {
	om := NewObservationManager()
	om.SetScope("dev_1", ObservationScope{
		BackendID: "codex", SessionIDs: []string{"foreground"}, DeliveryMode: scopeFullStream,
		IncludeRunningSignals: true,
	})
	if om.ShouldSendEvent("dev_1", "codex", "background", "text_delta") {
		t.Fatal("background text delta must not pass foreground scope")
	}
	if !om.ShouldSendEvent("dev_1", "codex", "background", "turn_completed") {
		t.Fatal("background durable milestone should pass when running signals are included")
	}
	if !om.ShouldSendEvent("dev_1", "codex", "", "sessions_changed") {
		t.Fatal("session discovery push should not be filtered by session IDs")
	}
}

func TestSetObservationScopeRPCRequiresAuthenticatedDeviceAndStoresScope(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())
	conn := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev-rpc"}}
	params := json.RawMessage(`{"backendId":"codex","sessionIds":["s1"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":45}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req", BackendID: "codex", Params: params})

	scope := h.observation.GetScope("dev-rpc", "codex")
	if scope == nil || len(scope.SessionIDs) != 1 || scope.SessionIDs[0] != "s1" {
		t.Fatalf("scope not stored: %#v", scope)
	}
}

func TestObservationCleanupWaitsForLastDeviceConnection(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())
	first := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev-multi"}}
	second := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev-multi"}}
	h.registerConnection(first)
	h.registerConnection(second)
	h.observation.SetScope("dev-multi", ObservationScope{BackendID: "codex", DeliveryMode: scopeFullStream})

	h.unregisterConnection(first)
	if h.observation.GetScope("dev-multi", "codex") == nil {
		t.Fatal("scope removed while another connection remained")
	}
	h.unregisterConnection(second)
	if h.observation.GetScope("dev-multi", "codex") != nil {
		t.Fatal("scope retained after last connection closed")
	}
}

func TestObservationFullStreamSendsAll(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	om.SetScope("dev_1", ObservationScope{
		BackendID:    "codex",
		SessionIDs:   []string{"sess_1"},
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 60,
	})

	events := []string{"text_delta", "thinking_delta", "turn_completed", "todos_updated", "tool_content"}
	for _, e := range events {
		if !om.ShouldSendEvent("dev_1", "codex", "sess_1", e) {
			t.Errorf("full_stream should send %q", e)
		}
	}
}

func TestObservationMilestonesOnlySendsDurable(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	om.SetScope("dev_1", ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeMilestonesOnly,
		LeaseSeconds: 60,
	})

	// Durable milestones should be sent
	if !om.ShouldSendEvent("dev_1", "codex", "sess_1", "turn_completed") {
		t.Error("milestones_only should send turn_completed")
	}
	if !om.ShouldSendEvent("dev_1", "codex", "sess_1", "todos_updated") {
		t.Error("milestones_only should send todos_updated")
	}

	// Non-durable should be filtered
	if om.ShouldSendEvent("dev_1", "codex", "sess_1", "text_delta") {
		t.Error("milestones_only should NOT send text_delta")
	}
	if om.ShouldSendEvent("dev_1", "codex", "sess_1", "thinking_delta") {
		t.Error("milestones_only should NOT send thinking_delta")
	}
}

func TestObservationLeaseExpiry(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	// 设置极短租约
	om.SetScope("dev_1", ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 1,
	})

	// 立即应该可以发送
	if !om.ShouldSendEvent("dev_1", "codex", "sess_1", "text_delta") {
		t.Error("should send before lease expiry")
	}

	// 等待租约过期
	time.Sleep(1500 * time.Millisecond)

	// 过期后应降级为 milestones_only
	if om.ShouldSendEvent("dev_1", "codex", "sess_1", "text_delta") {
		t.Error("should NOT send text_delta after lease expiry")
	}
	if !om.ShouldSendEvent("dev_1", "codex", "sess_1", "turn_completed") {
		t.Error("should still send durable milestone after lease expiry")
	}
}

func TestObservationSessionFilter(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	om.SetScope("dev_1", ObservationScope{
		BackendID:    "codex",
		SessionIDs:   []string{"sess_1"},
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 60,
	})

	// 匹配的 session 应发送
	if !om.ShouldSendEvent("dev_1", "codex", "sess_1", "text_delta") {
		t.Error("should send for matching session")
	}
	// 不匹配的 session 不应发送
	if om.ShouldSendEvent("dev_1", "codex", "sess_2", "text_delta") {
		t.Error("should NOT send for non-matching session")
	}
	// wildcard 应匹配所有
	om.SetScope("dev_1", ObservationScope{
		BackendID:    "codex",
		SessionIDs:   []string{"*"},
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 60,
	})
	if !om.ShouldSendEvent("dev_1", "codex", "any_session", "text_delta") {
		t.Error("wildcard should match any session")
	}
}

func TestObservationNoScopeDefaultsToMilestones(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	// 无 scope 时默认只发送 durable milestones
	if om.ShouldSendEvent("dev_unknown", "codex", "sess_1", "text_delta") {
		t.Error("no scope should NOT send text_delta")
	}
	if !om.ShouldSendEvent("dev_unknown", "codex", "sess_1", "turn_completed") {
		t.Error("no scope should send turn_completed")
	}
}

// ─── Outbox 测试 ────────────────────────────────────────────────────────

func TestOutboxEnqueueAndDrain(t *testing.T) {
	om := NewOutboxManager(nil)

	for i := 0; i < 5; i++ {
		envelope := json.RawMessage(`{"counter": ` + string(rune('1'+i)) + `}`)
		err := om.Enqueue("dev_1", uint64(i+1), envelope)
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	frames, bytes, overflowed := om.Stats("dev_1")
	if frames != 5 {
		t.Errorf("frames = %d, want 5", frames)
	}
	if bytes == 0 {
		t.Error("bytes should be > 0")
	}
	if overflowed {
		t.Error("should not be overflowed")
	}

	entries := om.Drain("dev_1")
	if len(entries) != 5 {
		t.Fatalf("drain entries = %d, want 5", len(entries))
	}
	for i, e := range entries {
		if e.Counter != uint64(i+1) {
			t.Errorf("entry %d counter = %d, want %d", i, e.Counter, i+1)
		}
	}

	// Drain 后应为空
	frames, _, _ = om.Stats("dev_1")
	if frames != 0 {
		t.Errorf("after drain: frames = %d, want 0", frames)
	}
}

func TestOutboxOverflowTriggersReconcile(t *testing.T) {
	ps := NewPrekeyStore("brg_fixture")
	om := NewOutboxManager(ps)

	overflowTriggered := atomic.Bool{}
	om.SetOverflowCallback(func(deviceID string, reason string) {
		overflowTriggered.Store(true)
		if reason != "outbox_overflow" {
			t.Errorf("reason = %q, want %q", reason, "outbox_overflow")
		}
	})

	// 入队超出上限
	for i := 0; i < outboxMaxFrames+1; i++ {
		envelope := json.RawMessage(`{}`)
		om.Enqueue("dev_1", uint64(i+1), envelope)
	}

	if !om.IsOverflowed("dev_1") {
		t.Error("should be overflowed")
	}

	// 等回调触发
	time.Sleep(100 * time.Millisecond)
	if !overflowTriggered.Load() {
		t.Error("overflow callback should have been triggered")
	}

	// 溢出后继续入队应失败
	err := om.Enqueue("dev_1", 9999, json.RawMessage(`{}`))
	if err == nil {
		t.Error("enqueue after overflow should fail")
	}
}

func TestOutboxResetAfterNewEpoch(t *testing.T) {
	om := NewOutboxManager(nil)

	// 入队到溢出
	for i := 0; i < outboxMaxFrames+1; i++ {
		om.Enqueue("dev_1", uint64(i+1), json.RawMessage(`{}`))
	}

	if !om.IsOverflowed("dev_1") {
		t.Fatal("should be overflowed")
	}

	// 重置
	om.ResetOverflow("dev_1")

	if om.IsOverflowed("dev_1") {
		t.Error("should not be overflowed after reset")
	}

	// 应该可以重新入队
	err := om.Enqueue("dev_1", 1, json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("enqueue after reset: %v", err)
	}
}

func TestOutboxDeviceIsolation(t *testing.T) {
	om := NewOutboxManager(nil)

	om.Enqueue("dev_a", 1, json.RawMessage(`{"a": true}`))
	om.Enqueue("dev_b", 1, json.RawMessage(`{"b": true}`))

	entriesA := om.Drain("dev_a")
	if len(entriesA) != 1 {
		t.Fatalf("dev_a entries = %d, want 1", len(entriesA))
	}
	entriesB := om.Drain("dev_b")
	if len(entriesB) != 1 {
		t.Fatalf("dev_b entries = %d, want 1", len(entriesB))
	}
}
