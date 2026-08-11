package gobridge

import (
	"sync/atomic"
	"time"
)

// OutboundBulkHandle 的 cancel/commit 用单一 atomic state 线性化（plan §3.6.4「cancel唯一原子状态机」
// 的 committedToWriter = too_late 边界）。两个独立 atomic（cancelled + committed）会有 cancel 与
// index0-commit 之间的竞态窗口；CAS 状态机保证 cancel 与 commit 互斥裁决。
type OutboundBulkHandle struct {
	groupID string
	state   atomic.Int32 // 0=active, 1=cancelled, 2=index0Committed
}

const (
	bulkActive    int32 = 0 // 未 cancel 也未 commit index0
	bulkCancelled int32 = 1 // cancel 赢得 CAS（writer 将跳过 index0）
	bulkCommitted int32 = 2 // writer 赢得 CAS（index0 已 commit；后续 cancel = too_late）
)

func newOutboundBulkHandle(groupID string) *OutboundBulkHandle {
	return &OutboundBulkHandle{groupID: groupID}
}
func (h *OutboundBulkHandle) GroupID() string {
	if h == nil {
		return ""
	}
	return h.groupID
}

// Cancel 尝试把状态从 active→cancelled（CAS）。返回是否 cancel 生效：
//   - active → cancelled：true（writer 将在 index0 前跳过该 group）
//   - 已 cancelled：true（幂等）
//   - 已 committed（index0）：false（too_late）
func (h *OutboundBulkHandle) Cancel() bool {
	if h == nil {
		return false
	}
	for {
		s := h.state.Load()
		if s == bulkCancelled {
			return true
		}
		if s == bulkCommitted {
			return false
		}
		if h.state.CompareAndSwap(bulkActive, bulkCancelled) {
			return true
		}
	}
}

// Cancelled 返回是否处于 cancelled 终态（supersede / cleanup 观察用）。
func (h *OutboundBulkHandle) Cancelled() bool { return h != nil && h.state.Load() == bulkCancelled }

// MarkIndex0Committed 由 writer 在写 index0 / Direct 首帧前 CAS active→committed。
// 返回 true = 本 worker 赢得 commit（写 index0）；false = 已被 cancel（跳过 group）。
func (h *OutboundBulkHandle) MarkIndex0Committed() bool {
	if h == nil {
		return true
	}
	return h.state.CompareAndSwap(bulkActive, bulkCommitted)
}

// Index0Committed 返回是否已过 too_late 边界（cancel handler 报 too_late 用）。
func (h *OutboundBulkHandle) Index0Committed() bool { return h != nil && h.state.Load() == bulkCommitted }

type relayBulkRequestContext struct {
	sessionID  string
	generation uint64
	startedAt  time.Time
}

func (rc *RelayDeviceConn) advanceSessionBulkGeneration(sessionID, requestID string) uint64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	next := rc.sessionBulkGenerations[sessionID] + 1
	rc.sessionBulkGenerations[sessionID] = next
	if handle := rc.activeBulkHandles[sessionID]; handle != nil {
		handle.Cancel()
		delete(rc.activeBulkHandles, sessionID)
	}
	if requestID != "" {
		rc.bulkRequestContexts[requestID] = relayBulkRequestContext{sessionID: sessionID, generation: next, startedAt: time.Now()}
	}
	return next
}

func (rc *RelayDeviceConn) bulkRequestContext(requestID string) (relayBulkRequestContext, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	ctx, ok := rc.bulkRequestContexts[requestID]
	return ctx, ok
}

func (rc *RelayDeviceConn) installHandleIfSessionBulkGenerationCurrent(sessionID string, generation uint64, handle *OutboundBulkHandle) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if handle == nil || rc.closed || rc.sessionBulkGenerations[sessionID] != generation {
		return false
	}
	if old := rc.activeBulkHandles[sessionID]; old != nil {
		old.Cancel()
	}
	rc.activeBulkHandles[sessionID] = handle
	return true
}

func (rc *RelayDeviceConn) completeBulkHandle(sessionID string, handle *OutboundBulkHandle) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.activeBulkHandles[sessionID] == handle {
		delete(rc.activeBulkHandles, sessionID)
	}
}

// installRequestBulkHandle 把 read_file_v2 chunked result 的 handle 按 requestId 登记到
// requestBulkHandles（R1.5），供 cancel_request_v1 查找并 Cancel()。已存在则替换并 Cancel 旧的。
func (rc *RelayDeviceConn) installRequestBulkHandle(requestID string, handle *OutboundBulkHandle) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if old := rc.requestBulkHandles[requestID]; old != nil {
		old.Cancel()
	}
	rc.requestBulkHandles[requestID] = handle
}

// lookupRequestBulkHandle 返回 requestId 对应的 in-flight handle（R1.5 cancel 用）。不存在返回 nil。
func (rc *RelayDeviceConn) lookupRequestBulkHandle(requestID string) *OutboundBulkHandle {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.requestBulkHandles[requestID]
}

// completeRequestBulkHandle 在 chunk group 完成 / 单帧 result / error 时清理 requestId→handle（R1.5）。
func (rc *RelayDeviceConn) completeRequestBulkHandle(requestID string, handle *OutboundBulkHandle) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.requestBulkHandles[requestID] == handle {
		delete(rc.requestBulkHandles, requestID)
	}
}
