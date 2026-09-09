package grokbuild

// session_mode.go — Grok Build typed 模式状态。
//
// 权威读 = plan_mode.json + 官方恢复规则映射（phase0 P8 契约）：
//   Active → plan(confirmed)；Pending/ExitPending/Inactive → default(confirmed)
//   （官方 from_snapshot 恢复语义：这三态恢复后都不是 plan）；
//   未知 state / 文件缺失 / 损坏 → unknown（缺失→default 未被官方证明，
//   保守 unknown——1.0.13 新会话 set plan 前该文件不存在）。
//
// 冷读仍遵守 P7：短命 mode-only actor 的 Pending 恢复成 Inactive，因此无
// resident actor 时 CanSet=false。2026-09-09 接通官方 pager 路径后，CordCode
// 持有的 resident actor 通过 session/set_mode 切换；它的 CMU 是该 actor 的
// live 真值，CanSet=true，直到 actor 注销后再回到冷恢复语义。

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
	// live is the effective mode acknowledged by a resident CordCode-owned
	// actor. Pending on disk means "plan on the next prompt" only for that same
	// actor; it must not be interpreted with cold-recovery rules until the actor
	// goes away.
	live map[string]string
}

func newModeSideState() *modeSideState {
	return &modeSideState{
		cached: make(map[string]core.SessionModeEvent),
		dirty:  make(map[string]struct{}),
		live:   make(map[string]string),
	}
}

func (m *modeSideState) observeLive(sessionID, mode string) {
	if sessionID == "" || (mode != "plan" && mode != "default") {
		return
	}
	m.mu.Lock()
	m.live[sessionID] = mode
	delete(m.dirty, sessionID)
	m.mu.Unlock()
}

func (m *modeSideState) clearLive(sessionID string) {
	m.mu.Lock()
	delete(m.live, sessionID)
	delete(m.cached, sessionID)
	m.dirty[sessionID] = struct{}{}
	m.mu.Unlock()
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
	delete(m.live, sessionID)
	m.mu.Unlock()
}

func (m *modeSideState) invalidateAll() {
	m.mu.Lock()
	m.cached = make(map[string]core.SessionModeEvent)
	m.dirty = make(map[string]struct{})
	m.live = make(map[string]string)
	m.mu.Unlock()
}

// GetSessionMode reads the authoritative typed mode state for one session
// (core.SessionModeReader). "unknown" is a VALUE (missing/corrupt/unresolvable
// session — the honest answer), not an error.
func (a *Agent) GetSessionMode(ctx context.Context, sessionID string) (core.SessionModeEvent, error) {
	if a.modeSide != nil {
		a.modeSide.mu.Lock()
		liveMode, hasLiveMode := a.modeSide.live[sessionID]
		a.modeSide.mu.Unlock()
		if hasLiveMode {
			return liveConfirmedModeState(liveMode), nil
		}
	}
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
	if _, live := a.liveSessionForCommand(sessionID); live && result.Status == "confirmed" {
		result.CanSet = true
		result.Reason = ""
	}
	a.modeSide.mu.Lock()
	a.modeSide.cached[sessionID] = result
	delete(a.modeSide.dirty, sessionID)
	a.modeSide.mu.Unlock()
	return result, nil
}

func liveConfirmedModeState(mode string) core.SessionModeEvent {
	return core.SessionModeEvent{Status: "confirmed", Mode: &mode, CanSet: true}
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
// 其余（含缺失 ""）→ unknown。此函数只描述冷恢复，所以 CanSet=false；
// resident actor 的 GetSessionMode 会把确认态提升为可切换。
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
		// 冷恢复不拥有 resident actor，不能承诺 set_mode 后的生命周期。
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
