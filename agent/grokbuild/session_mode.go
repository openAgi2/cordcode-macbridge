package grokbuild

// session_mode.go — typed 模式状态读侧（Grok Build 面板方案 2026-09-07 §5.1/
// §5.3；P7 阻断下的「明确禁用与诊断」交付）。
//
// 权威读 = plan_mode.json + 官方恢复规则映射（phase0 P8 契约）：
//   Active → plan(confirmed)；Pending/ExitPending/Inactive → default(confirmed)
//   （官方 from_snapshot 恢复语义：这三态恢复后都不是 plan）；
//   未知 state / 文件缺失 / 损坏 → unknown（缺失→default 未被官方证明，
//   保守 unknown——1.0.13 新会话 set plan 前该文件不存在）。
//
// CanSet 恒 false：P7 决定性判定——1.0.13 官方恢复把 Pending→Inactive 且无
// CMU、无 plan 注入、文件不回写，短命 mode-only 切换路径不成立。模式切换在
// 官方生命周期方案落地前明确禁用；本接口只提供读侧诊断真值，绝不补偿写。
//
// CMU（current_mode_update）通知只置 dirty（§5.3：通知不携带"必须等到"的
// 目标值），下次 GetSessionMode 重读权威文件。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// grokModeSwitchBlockedReason 是 canSet=false 的稳定原因码（诊断面；证据
// scripts/grokbuild-phase0/EVIDENCE.md P7/漂移表）。
const grokModeSwitchBlockedReason = "grok_mode_switch_blocked_official_recovery"

type modeSideState struct {
	mu sync.Mutex
	// cached: sessionID → 最后一次权威读结果（含 unknown）。
	cached map[string]core.SessionModeEvent
	// dirty: sessionID 收到过 CMU（或会话重建），下次读取必须重读文件。
	dirty map[string]struct{}
}

func newModeSideState() *modeSideState {
	return &modeSideState{
		cached: make(map[string]core.SessionModeEvent),
		dirty:  make(map[string]struct{}),
	}
}

func (m *modeSideState) markDirty(sessionID string) {
	m.mu.Lock()
	m.dirty[sessionID] = struct{}{}
	m.mu.Unlock()
}

func (m *modeSideState) invalidateSession(sessionID string) {
	m.mu.Lock()
	delete(m.cached, sessionID)
	delete(m.dirty, sessionID)
	m.mu.Unlock()
}

func (m *modeSideState) invalidateAll() {
	m.mu.Lock()
	m.cached = make(map[string]core.SessionModeEvent)
	m.dirty = make(map[string]struct{})
	m.mu.Unlock()
}

// GetSessionMode reads the authoritative typed mode state for one session
// (core.SessionModeReader). "unknown" is a VALUE (missing/corrupt/unresolvable
// session — the honest answer), not an error.
func (a *Agent) GetSessionMode(ctx context.Context, sessionID string) (core.SessionModeEvent, error) {
	a.mu.RLock()
	home := a.grokHomeLocked()
	a.mu.RUnlock()
	dir := findSessionDir(home, sessionID)
	if dir == "" {
		return unknownModeState(), nil
	}

	a.modeSide.mu.Lock()
	cached, hasCached := a.modeSide.cached[sessionID]
	_, isDirty := a.modeSide.dirty[sessionID]
	a.modeSide.mu.Unlock()
	if hasCached && !isDirty {
		return cached, nil
	}

	result := modeStateFromPersistent(readPlanModeState(dir))
	a.modeSide.mu.Lock()
	a.modeSide.cached[sessionID] = result
	delete(a.modeSide.dirty, sessionID)
	a.modeSide.mu.Unlock()
	return result, nil
}

// planModeFile is the single authoritative persistent mode record (P8: the
// only file that carries official mode state).
type planModeFile struct {
	State string `json:"state"`
}

// readPlanModeState returns the raw persisted state string; "" = missing or
// corrupt (both map to unknown — P8 keeps them distinct from any confirmed
// value and never fabricates default).
func readPlanModeState(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "plan_mode.json"))
	if err != nil {
		return ""
	}
	var f planModeFile
	if json.Unmarshal(data, &f) != nil {
		return ""
	}
	return f.State
}

// modeStateFromPersistent applies the official recovery mapping (P8):
// Pending/ExitPending/Inactive → default（官方恢复丢弃 plan）；Active → plan；
// 其余（含缺失 ""）→ unknown。写入面恒禁用（P7），reason 带稳定原因码。
func modeStateFromPersistent(state string) core.SessionModeEvent {
	switch state {
	case "Active":
		return confirmedModeState("plan")
	case "Pending", "ExitPending", "Inactive":
		return confirmedModeState("default")
	default:
		return unknownModeState()
	}
}

func confirmedModeState(mode string) core.SessionModeEvent {
	return core.SessionModeEvent{
		Status: "confirmed",
		Mode:   &mode,
		// P7 阻断：官方恢复不支持跨 child 的短命 mode-only 切换——写入面
		// 明确禁用，本读侧只做诊断真值。
		CanSet: false,
		Reason: grokModeSwitchBlockedReason,
	}
}

func unknownModeState() core.SessionModeEvent {
	return core.SessionModeEvent{
		Status: "unknown",
		CanSet: false,
		Reason: grokModeSwitchBlockedReason,
	}
}
