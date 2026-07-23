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
	defaultLeaseSeconds = 45 // 方案 §8.3 observation 合约

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

// ShouldSendEvent 判断是否应向设备发送指定事件。
// 方案 §8.3：milestones_only 只投递白名单内的 durable milestone。
func (om *ObservationManager) ShouldSendEvent(deviceID, backendID, sessionID, eventType string) bool {
	om.mu.RLock()
	defer om.mu.RUnlock()

	dev, ok := om.devices[deviceID]
	if !ok {
		// 无 scope 时默认只发送 durable milestones
		return IsDurableMilestone(eventType)
	}

	dev.mu.Lock()
	defer dev.mu.Unlock()

	scope, ok := dev.scopes[backendID]
	if !ok {
		return IsDurableMilestone(eventType)
	}
	if eventType == "sessions_changed" {
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

	// 检查租约是否过期
	if scope.DeliveryMode == scopeFullStream {
		elapsed := time.Since(scope.leasedAt).Seconds()
		if elapsed > float64(scope.LeaseSeconds) {
			// 租约过期，自动降级为 milestones_only
			scope.DeliveryMode = scopeMilestonesOnly
			slog.Debug("observation: lease expired, downgraded to milestones",
				"deviceID", safeID(deviceID),
				"backendID", backendID,
			)
		}
	}

	switch scope.DeliveryMode {
	case scopeFullStream:
		return true // 全部事件
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
					scope.DeliveryMode = scopeMilestonesOnly
					slog.Debug("observation: lease expired during check",
						"backendID", scope.BackendID,
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
