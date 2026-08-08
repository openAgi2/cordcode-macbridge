package gobridge

import (
	"sync"

	"github.com/openAgi2/cordcode-macbridge/go-bridge/relaystate"
)

// BulkCorrelationRegistry 是 plan §3.6.4 双端 correlation registry 的并发安全运行时实现
// （server 端，挂在每个 RelayDeviceConn 上，天然按 (authenticatedDevice, generation) 作用域）。
//
// 复用 relaystate.CorrelatedRegistry proof 的语义（active/retired + put-if-absent + caps），
// 在其上加 mu 串行化——proof 模型本身非线程安全。key 是 16-byte bulk correlation（opaque，
// 非 secret；认证来自 AEAD/AAD + generation，不宣称 constant-time 比较）。
//
// 生命周期：active（request 已 admit，response/chunk 尚未完成）→ retired（response commit /
// chunked complete / cancel / error / single-frame）。retired 只能 close generation，不静默 LRU。
// duplicate（active）与 reuse（retired 窗口内）都拒绝，调用方 close transport generation。
type BulkCorrelationRegistry struct {
	mu  sync.Mutex
	reg *relaystate.CorrelatedRegistry
}

// NewBulkCorrelationRegistry 构造带 caps 的 registry。maxActive/maxRetired 由 A0 冻结。
func NewBulkCorrelationRegistry(maxActive, maxRetired int) *BulkCorrelationRegistry {
	return &BulkCorrelationRegistry{reg: relaystate.NewCorrelatedRegistry(maxActive, maxRetired)}
}

// PutIfAbsent 原子登记 correlation key。
// 返回 (admitted, reason)：admitted=true 表示首次登记成功；
// reason ∈ {admitted, already_active（duplicate）, reuse（retired 窗口内复用）, busy（active 满）}。
// duplicate/reuse 调用方必须 close transport generation（plan §3.6.4 strict failure）。
func (r *BulkCorrelationRegistry) PutIfAbsent(key string) (bool, string) {
	if r == nil {
		return true, "admitted" // registry disabled（未注入）→ 不阻断
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reg.PutIfAbsent(key)
}

// Retire 把 active key 移到 retired。返回 false 表示 key 不在 active（从未登记或已 retire）；
// retired 集合超 cap 时也返回 false，调用方 close generation。
func (r *BulkCorrelationRegistry) Retire(key string) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reg.Retire(key)
}

// ActiveCount / RetiredCount 供测试与可观测性观察。
func (r *BulkCorrelationRegistry) ActiveCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reg.ActiveCount()
}
func (r *BulkCorrelationRegistry) RetiredCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reg.RetiredCount()
}
