package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ─── Observation Scope 管理 ─────────────────────────────────────────────
//
// 方案 §8.3：
//   Mac 端决定向哪些设备发送哪些事件。
//   iOS 前台打开 session 时发送 full_stream，租约到期自动降为 milestones_only。
//   iOS 即将进入后台时发送 milestones_only。
//   Relay 本身不知道 scope 内容。

const (
	scopeFullStream     = "full_stream"
	scopeMilestonesOnly = "milestones_only"
	defaultLeaseSeconds = 90 // iOS renews ~20–30s; 45s too tight under reconnect

	// 有界 outbox 参数（§8.5）
	outboxMaxFrames = 1000
	outboxMaxBytes  = 16 * 1024 * 1024 // 16 MB
)

// ObservationScope 单个 backend 的观察范围。
type ObservationScope struct {
	BackendID             string   `json:"backendId"`
	SessionIDs            []string `json:"sessionIds"`
	DeliveryMode          string   `json:"deliveryMode"` // "full_stream" | "milestones_only"
	IncludeRunningSignals bool     `json:"includeRunningSessionSignals"`
	LeaseSeconds          int      `json:"leaseSeconds"`
	leasedAt              time.Time
}

// DeviceObservation 管理 per-device 的 observation scope。
type DeviceObservation struct {
	mu     sync.Mutex
	scopes map[string]*ObservationScope // backendID -> scope
}

// ObservationManager 管理所有设备的 observation scope。
type ObservationManager struct {
	mu         sync.RWMutex
	devices    map[string]*DeviceObservation // deviceID -> observation
	leaseTimer *time.Ticker
	stopCh     chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
}

// NewObservationManager 创建 observation manager。T09: 构造函数不再启动 lease loop——
// 调用方（Handlers）必须显式 Start(ctx) 启动，Shutdown 时 Stop()。构造函数内无 go ... 语句，
// 满足 plan §3.9「observation 由显式 Start 启动」。历史调用方（部分测试）可仍用 NewObservationManager，
// 但需在需要租约过期时调用 Start。
func NewObservationManager() *ObservationManager {
	return &ObservationManager{
		devices: make(map[string]*DeviceObservation),
		stopCh:  make(chan struct{}),
	}
}

// Start 启动租约检查 goroutine（幂等，仅一次）。Handlers 在构造后调用一次，
// Shutdown 时调 Stop。取消 ctx 也会让 leaseCheckLoop 退出（通过 stopCh 兜底）。
func (om *ObservationManager) Start(ctx context.Context) {
	om.startOnce.Do(func() {
		om.leaseTimer = time.NewTicker(5 * time.Second)
		go om.leaseCheckLoop(ctx)
	})
}

// SetScope 设置设备的 observation scope。
func (om *ObservationManager) SetScope(deviceID string, scope ObservationScope) {
	om.mu.Lock()
	defer om.mu.Unlock()

	dev, ok := om.devices[deviceID]
	if !ok {
		dev = &DeviceObservation{
			scopes: make(map[string]*ObservationScope),
		}
		om.devices[deviceID] = dev
	}

	dev.mu.Lock()
	defer dev.mu.Unlock()

	// 设置租约时间
	if scope.LeaseSeconds <= 0 {
		scope.LeaseSeconds = defaultLeaseSeconds
	}
	scope.leasedAt = time.Now()

	dev.scopes[scope.BackendID] = &scope

	slog.Debug("observation: scope set",
		"deviceID", safeID(deviceID),
		"backendID", scope.BackendID,
		"mode", scope.DeliveryMode,
		"lease", scope.LeaseSeconds,
	)
}

// isLiveControlPlaneEvent is true for events that must reach a watching client
// even under milestones_only. Without turn_started / session_state_changed,
// iOS never arms isGenerating and the input bar stays idle for the whole turn
// (owner symptom: bulk text only after Mac finishes).
func isLiveControlPlaneEvent(eventType string) bool {
	switch eventType {
	case "turn_started", "session_state_changed", "session_running_signal", "user_message":
		return true
	default:
		return false
	}
}

// ShouldSendEvent 判断是否应向设备发送指定事件。
// 方案 §8.3：milestones_only 只投递白名单内的 durable milestone。
//
// Extension: when the device has IncludeRunningSignals (or any scope for the
// backend), control-plane events (turn_started / session_state_changed /
// user_message) always pass so external turns can arm generation even if the
// client briefly holds milestones_only.
func (om *ObservationManager) ShouldSendEvent(deviceID, backendID, sessionID, eventType string) bool {
	om.mu.RLock()
	defer om.mu.RUnlock()

	// sessions_changed is a backend-scoped catalog control-plane event (guard #8).
	// It must reach any connected client regardless of observation scope —
	// the web client on session-list view has no per-backend scope yet.
	if eventType == "sessions_changed" {
		return true
	}

	dev, ok := om.devices[deviceID]
	if !ok {
		// 无 scope：control-plane + durable only (cannot assume full_stream)
		return IsDurableMilestone(eventType) || isLiveControlPlaneEvent(eventType)
	}

	dev.mu.Lock()
	defer dev.mu.Unlock()

	scope, ok := dev.scopes[backendID]
	if !ok {
		return IsDurableMilestone(eventType) || isLiveControlPlaneEvent(eventType)
	}
	if isLiveControlPlaneEvent(eventType) {
		// Session filter still applies for control-plane when SessionIDs is non-empty.
		if len(scope.SessionIDs) > 0 {
			found := false
			for _, sid := range scope.SessionIDs {
				if sid == sessionID || sid == "*" {
					found = true
					break
				}
			}
			if !found {
				// Unlisted session: only if running-signals allow durable/control for background awareness
				return scope.IncludeRunningSignals
			}
		}
		return true
	}

	// 检查 session 过滤
	if len(scope.SessionIDs) > 0 {
		found := false
		for _, sid := range scope.SessionIDs {
			if sid == sessionID || sid == "*" {
				found = true
				break
			}
		}
		if !found {
			return scope.IncludeRunningSignals && IsDurableMilestone(eventType)
		}
	}

	// Soft lease: full_stream only while leasedAt is still fresh.
	// Never permanently rewrite DeliveryMode — a missed renew must not pin the
	// device on milestones_only until the next SetScope (bulk/completed-bar symptom).
	effectiveMode := scope.DeliveryMode
	if scope.DeliveryMode == scopeFullStream {
		elapsed := time.Since(scope.leasedAt).Seconds()
		// Soft window = 2× lease so a reconnect that lands just after the
		// nominal renew cadence does not filter text/reasoning (owner flash).
		// Tests use 1s leases; production uses 90s → 180s soft window.
		grace := float64(scope.LeaseSeconds) * 2
		if elapsed > grace {
			effectiveMode = scopeMilestonesOnly
		}
	}

	switch effectiveMode {
	case scopeFullStream:
		return true
	case scopeMilestonesOnly:
		return IsDurableMilestone(eventType)
	default:
		return IsDurableMilestone(eventType)
	}
}

// RemoveDevice 移除设备的 observation（设备断连/撤销时调用）。
func (om *ObservationManager) RemoveDevice(deviceID string) {
	om.mu.Lock()
	defer om.mu.Unlock()
	delete(om.devices, deviceID)
}

// DeviceIDs returns a snapshot of device IDs with any observation state.
// Used by LiveFrameBuffer to find interested devices when targets are empty.
func (om *ObservationManager) DeviceIDs() []string {
	om.mu.RLock()
	defer om.mu.RUnlock()
	out := make([]string, 0, len(om.devices))
	for id := range om.devices {
		out = append(out, id)
	}
	return out
}

// GetScope 返回设备的当前 scope 快照。
func (om *ObservationManager) GetScope(deviceID, backendID string) *ObservationScope {
	om.mu.RLock()
	defer om.mu.RUnlock()

	dev, ok := om.devices[deviceID]
	if !ok {
		return nil
	}

	dev.mu.Lock()
	defer dev.mu.Unlock()

	scope, ok := dev.scopes[backendID]
	if !ok {
		return nil
	}
	// 返回副本
	copy := *scope
	return &copy
}

// Stop 停止租约检查循环（幂等）。Handlers.Shutdown 调用一次。
func (om *ObservationManager) Stop() {
	om.stopOnce.Do(func() {
		if om.leaseTimer != nil {
			om.leaseTimer.Stop()
		}
		close(om.stopCh)
	})
}

func (om *ObservationManager) leaseCheckLoop(ctx context.Context) {
	for {
		select {
		case <-om.leaseTimer.C:
			om.checkLeases()
		case <-om.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (om *ObservationManager) checkLeases() {
	om.mu.RLock()
	defer om.mu.RUnlock()

	now := time.Now()
	for _, dev := range om.devices {
		dev.mu.Lock()
		for _, scope := range dev.scopes {
			if scope.DeliveryMode == scopeFullStream {
				elapsed := now.Sub(scope.leasedAt).Seconds()
				if elapsed > float64(scope.LeaseSeconds) {
					// Soft expiry only (ShouldSendEvent uses leasedAt). Keep
					// DeliveryMode so a subsequent SetScope renew stays full_stream.
					slog.Debug("observation: lease soft-expired (mode kept)",
						"backendID", scope.BackendID,
						"elapsedSeconds", int(elapsed),
					)
				}
			}
		}
		dev.mu.Unlock()
	}
}

// ─── 有界 Outbox ─────────────────────────────────────────────────────────
//
// 方案 §8.5：Mac→Relay 断链时 per-device 有界内存缓冲。
// 溢出后废弃当前 delivery epoch，重建后发 delivery_reconcile_required。

// OutboxEntry 是 outbox 中的一个加密信封条目。
type OutboxEntry struct {
	Counter   uint64
	Envelope  json.RawMessage
	Size      int
	CreatedAt time.Time
}

// DeviceOutbox 是 per-device 的有界 outbox。
type DeviceOutbox struct {
	mu         sync.Mutex
	deviceID   string
	entries    []OutboxEntry
	totalBytes int64
	epochIndex uint64
	overflowed bool
}

// OutboxManager 管理所有设备的 outbox。
type OutboxManager struct {
	mu         sync.RWMutex
	outboxes   map[string]*DeviceOutbox // deviceID -> outbox
	prekeys    *PrekeyStore
	onOverflow func(deviceID string, reason string)
}

// NewOutboxManager 创建 outbox manager。
func NewOutboxManager(prekeys *PrekeyStore) *OutboxManager {
	return &OutboxManager{
		outboxes: make(map[string]*DeviceOutbox),
		prekeys:  prekeys,
	}
}

// SetOverflowCallback 设置溢出回调。
func (om *OutboxManager) SetOverflowCallback(fn func(deviceID string, reason string)) {
	om.mu.Lock()
	defer om.mu.Unlock()
	om.onOverflow = fn
}

// Enqueue 将加密信封加入设备 outbox。
// 方案 §8.5：达到上限后标记 overflow，触发 reconcile。
func (om *OutboxManager) Enqueue(deviceID string, counter uint64, envelope json.RawMessage) error {
	om.mu.Lock()
	ob, ok := om.outboxes[deviceID]
	if !ok {
		ob = &DeviceOutbox{
			deviceID: deviceID,
		}
		om.outboxes[deviceID] = ob
	}
	om.mu.Unlock()

	ob.mu.Lock()
	defer ob.mu.Unlock()

	// 如果已溢出，不再入队
	if ob.overflowed {
		return fmt.Errorf("outbox overflow for device %s", safeID(deviceID))
	}

	size := len(envelope)

	// 检查上限
	if len(ob.entries) >= outboxMaxFrames || ob.totalBytes+int64(size) > outboxMaxBytes {
		ob.overflowed = true
		slog.Warn("outbox: overflow",
			"deviceID", safeID(deviceID),
			"frames", len(ob.entries),
			"bytes", ob.totalBytes,
		)
		// 触发 reconcile
		om.mu.Lock()
		callback := om.onOverflow
		om.mu.Unlock()
		if callback != nil {
			go callback(deviceID, "outbox_overflow")
		}
		return fmt.Errorf("outbox overflow for device %s", safeID(deviceID))
	}

	ob.entries = append(ob.entries, OutboxEntry{
		Counter:   counter,
		Envelope:  envelope,
		Size:      size,
		CreatedAt: time.Now(),
	})
	ob.totalBytes += int64(size)

	return nil
}

// Drain 取出并清空 outbox（Relay 恢复后调用）。
// 返回所有缓存的信封，按 counter 排序。
func (om *OutboxManager) Drain(deviceID string) []OutboxEntry {
	om.mu.RLock()
	ob, ok := om.outboxes[deviceID]
	om.mu.RUnlock()

	if !ok {
		return nil
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	entries := ob.entries
	ob.entries = nil
	ob.totalBytes = 0
	ob.overflowed = false

	return entries
}

// Flush 按顺序发送 outbox 中的 frame，并且只移除已成功发送的条目。
func (om *OutboxManager) Flush(deviceID string, send func(json.RawMessage) error) error {
	om.mu.RLock()
	ob, ok := om.outboxes[deviceID]
	om.mu.RUnlock()
	if !ok {
		return nil
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	sent := 0
	for _, entry := range ob.entries {
		if err := send(entry.Envelope); err != nil {
			if sent > 0 {
				ob.entries = append([]OutboxEntry(nil), ob.entries[sent:]...)
				ob.totalBytes = 0
				for _, remaining := range ob.entries {
					ob.totalBytes += int64(remaining.Size)
				}
			}
			return err
		}
		sent++
	}
	ob.entries = nil
	ob.totalBytes = 0
	ob.overflowed = false
	return nil
}

// IsOverflowed 检查设备 outbox 是否已溢出。
func (om *OutboxManager) IsOverflowed(deviceID string) bool {
	om.mu.RLock()
	ob, ok := om.outboxes[deviceID]
	om.mu.RUnlock()

	if !ok {
		return false
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()
	return ob.overflowed
}

// ResetOverflow 重置溢出状态（新 epoch 建立后调用）。
func (om *OutboxManager) ResetOverflow(deviceID string) {
	om.mu.RLock()
	ob, ok := om.outboxes[deviceID]
	om.mu.RUnlock()

	if !ok {
		return
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()
	ob.overflowed = false
	ob.entries = nil
	ob.totalBytes = 0
	slog.Info("outbox: reset after new epoch", "deviceID", safeID(deviceID))
}

// Stats 返回 outbox 统计信息。
func (om *OutboxManager) Stats(deviceID string) (frames int, bytes int64, overflowed bool) {
	om.mu.RLock()
	ob, ok := om.outboxes[deviceID]
	om.mu.RUnlock()

	if !ok {
		return 0, 0, false
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()
	return len(ob.entries), ob.totalBytes, ob.overflowed
}
