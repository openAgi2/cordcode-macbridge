package gobridge

import (
	"sync/atomic"
	"time"
)

type OutboundBulkHandle struct {
	groupID   string
	cancelled atomic.Bool
}

func newOutboundBulkHandle(groupID string) *OutboundBulkHandle {
	return &OutboundBulkHandle{groupID: groupID}
}
func (h *OutboundBulkHandle) GroupID() string {
	if h == nil {
		return ""
	}
	return h.groupID
}
func (h *OutboundBulkHandle) Cancel() {
	if h != nil {
		h.cancelled.Store(true)
	}
}
func (h *OutboundBulkHandle) Cancelled() bool { return h != nil && h.cancelled.Load() }

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
