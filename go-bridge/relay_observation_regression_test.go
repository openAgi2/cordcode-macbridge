package gobridge

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Phase 2 Observation/Outbox Regression Gate ──────────────────────────
//
// 方案 §11 Phase 2 退出标准：
//   - scope lease 自动降级
//   - 有界 outbox 溢出触发新 epoch reconcile
//   - counter 空洞后的帧不被应用
//
// 验证项：
//   R1: full-stream 租约过期后自动降级为 milestones_only
//   R2: 后台 scope 下只发送 durable milestones
//   R3: Outbox 溢出后不继续使用旧 epoch 的 counter
//   R4: Outbox 溢出后必须重建 epoch 才能继续发送
//   R5: 设备间 scope 和 outbox 完全隔离

// TestRegressionR1_LeaseAutoDowngrade 验证租约超过 soft window 后有效降级。
// DeliveryMode 保留 full_stream，让下一次 SetScope 续租能立即恢复；
// ShouldSendEvent 的 effective mode 才是真实投递语义。
func TestRegressionR1_LeaseAutoDowngrade(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	om.SetScope("dev_r1", ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 1,
	})

	// 租约内发送全部事件
	if !om.ShouldSendEvent("dev_r1", "codex", "s1", "text_delta") {
		t.Error("should send before expiry")
	}

	// Soft window is 2x the nominal lease, matching reconnect jitter handling.
	time.Sleep(2500 * time.Millisecond)

	// 过期后降级
	if om.ShouldSendEvent("dev_r1", "codex", "s1", "text_delta") {
		t.Error("should NOT send non-durable after lease expiry")
	}
	if !om.ShouldSendEvent("dev_r1", "codex", "s1", "turn_completed") {
		t.Error("should still send durable milestone")
	}

	// Stored intent remains full_stream; effective delivery above is downgraded.
	scope := om.GetScope("dev_r1", "codex")
	if scope.DeliveryMode != scopeFullStream {
		t.Errorf("mode = %q, want retained %q intent", scope.DeliveryMode, scopeFullStream)
	}
}

// TestRegressionR2_BackgroundScopeOnlyDurable 验证后台 scope 只投递 durable milestones。
func TestRegressionR2_BackgroundScopeOnlyDurable(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	om.SetScope("dev_r2", ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeMilestonesOnly,
		LeaseSeconds: 60,
	})

	// 非 durable 不投递
	nonDurable := []string{"text_delta", "thinking_delta", "tool_content", "message_content", "file_content", "session_history"}
	for _, e := range nonDurable {
		if om.ShouldSendEvent("dev_r2", "codex", "s1", e) {
			t.Errorf("milestones_only should NOT send %q", e)
		}
	}

	// Durable 投递
	durable := []string{"turn_completed", "turn_error", "todos_updated", "session_running_signal", "delivery_reconcile_required"}
	for _, e := range durable {
		if !om.ShouldSendEvent("dev_r2", "codex", "s1", e) {
			t.Errorf("milestones_only should send %q", e)
		}
	}
}

// TestRegressionR3_OutboxOverflowAbandonsEpoch 验证溢出后放弃旧 epoch。
// 方案 §8.5：溢出后不继续使用产生空洞的 delivery epoch。
func TestRegressionR3_OutboxOverflowAbandonsEpoch(t *testing.T) {
	om := NewOutboxManager(nil)

	// 入队到溢出
	for i := 0; i < outboxMaxFrames+1; i++ {
		envelope := json.RawMessage(`{}`)
		om.Enqueue("dev_r3", uint64(i+1), envelope)
	}

	if !om.IsOverflowed("dev_r3") {
		t.Fatal("should be overflowed")
	}

	// 溢出后入队应失败（不使用旧 epoch）
	err := om.Enqueue("dev_r3", outboxMaxFrames+2, json.RawMessage(`{}`))
	if err == nil {
		t.Error("should reject enqueue after overflow (old epoch abandoned)")
	}
}

// TestRegressionR4_OutboxResetRequiresNewEpoch 验证溢出后必须重建 epoch。
func TestRegressionR4_OutboxResetRequiresNewEpoch(t *testing.T) {
	om := NewOutboxManager(nil)

	// 溢出
	for i := 0; i < outboxMaxFrames+1; i++ {
		om.Enqueue("dev_r4", uint64(i+1), json.RawMessage(`{}`))
	}

	if !om.IsOverflowed("dev_r4") {
		t.Fatal("should be overflowed")
	}

	// 重置（模拟新 epoch）
	om.ResetOverflow("dev_r4")

	if om.IsOverflowed("dev_r4") {
		t.Error("should not be overflowed after reset")
	}

	// 可以重新入队（新 epoch counter 从 1 开始）
	err := om.Enqueue("dev_r4", 1, json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("enqueue after reset: %v", err)
	}
}

// TestRegressionR5_ObservationOutboxDeviceIsolation 验证设备间完全隔离。
func TestRegressionR5_ObservationOutboxDeviceIsolation(t *testing.T) {
	om := NewObservationManager()
	om.Start(context.Background()) // T09: 显式启动 lease loop
	defer om.Stop()

	// dev_a: full_stream
	om.SetScope("dev_a", ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeFullStream,
		LeaseSeconds: 60,
	})

	// dev_b: milestones_only
	om.SetScope("dev_b", ObservationScope{
		BackendID:    "codex",
		DeliveryMode: scopeMilestonesOnly,
		LeaseSeconds: 60,
	})

	// dev_a 收到全部事件
	if !om.ShouldSendEvent("dev_a", "codex", "s1", "text_delta") {
		t.Error("dev_a should receive text_delta")
	}
	// dev_b 只收到 milestones
	if om.ShouldSendEvent("dev_b", "codex", "s1", "text_delta") {
		t.Error("dev_b should NOT receive text_delta")
	}
	if !om.ShouldSendEvent("dev_b", "codex", "s1", "turn_completed") {
		t.Error("dev_b should receive turn_completed")
	}

	// Outbox 隔离
	obm := NewOutboxManager(nil)
	obm.Enqueue("dev_a", 1, json.RawMessage(`{}`))
	obm.Enqueue("dev_a", 2, json.RawMessage(`{}`))

	entries := obm.Drain("dev_b")
	if len(entries) != 0 {
		t.Errorf("dev_b should have 0 outbox entries, got %d", len(entries))
	}

	entries = obm.Drain("dev_a")
	if len(entries) != 2 {
		t.Errorf("dev_a should have 2 outbox entries, got %d", len(entries))
	}
}

// TestRegressionR6_OutboxOverflowCallbackReconcileSignal 验证溢出回调触发 reconcile 信号。
func TestRegressionR6_OutboxOverflowCallbackReconcileSignal(t *testing.T) {
	om := NewOutboxManager(nil)

	reconcileSignalSent := atomic.Bool{}
	om.SetOverflowCallback(func(deviceID string, reason string) {
		reconcileSignalSent.Store(true)
		if reason != "outbox_overflow" {
			t.Errorf("reason = %q, want outbox_overflow", reason)
		}
	})

	for i := 0; i < outboxMaxFrames+1; i++ {
		om.Enqueue("dev_r6", uint64(i+1), json.RawMessage(`{}`))
	}

	time.Sleep(100 * time.Millisecond)
	if !reconcileSignalSent.Load() {
		t.Error("overflow should trigger reconcile signal callback")
	}
}
