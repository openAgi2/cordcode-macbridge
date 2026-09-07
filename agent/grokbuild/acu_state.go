package grokbuild

// acu_state.go — ACU（available_commands_update）side-state 缓存与 D1 准入交集。
//
// 方案 docs/2026-09-07-grok-build-slash-command-panel-implementation.md §4.1 + Phase 0
// 实测（scripts/grokbuild-phase0/EVIDENCE.md P2/P3）：
//   - ACU 通知在握手 drain 之前就可能到达（per-turn actor handshake 期间 emit 丢弃
//     事件，但 side-state 必须先落），所以写入点在 readLoop/handleNotification，
//     不依赖 Events 消费方。
//   - stdout 与 leader/gateway 两条 rail 的 ACU 走同一个缓存入口，整表替换（空表
//     也是合法表）。
//   - ACU 无序号，无法与 List 成功值排序：不能覆盖较新的 List 成功值，只标记
//     pendingRefresh；也不得污染另一 session（按 (sessionID, cwd) 隔离）。
//   - 缓存只作 Execute 白名单与诊断；List 展示源永远是当次真实官方拉取
//     （session_commands.go 专用 child load）。失败的 List 将身份标记不可用，
//     不得随后拿旧缓存执行。
//   - cwd / binary·config / session 重建时失效；bridge 重启进程内缓存自然全清。

import (
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// acuEntry is the per-identity (sessionID+cwd) side-state. Two slots:
// notification-sourced (unordered, may be a mid-init partial wave) and
// list-sourced (an authoritative dedicated-child pull). The list slot wins for
// the Execute whitelist; a differing unordered ACU only marks pendingRefresh.
type acuEntry struct {
	notif          []core.SessionCommand
	list           []core.SessionCommand
	listAt         time.Time
	unavailable    bool // last real pull failed → Execute must refuse until a fresh pull succeeds
	pendingRefresh bool // unordered ACU differs from the current view; next List open re-pulls anyway
}

type acuSideState struct {
	mu      sync.Mutex
	entries map[string]*acuEntry
	gen     uint64 // agent identity generation; binary/config change bumps and clears all
}

func newACUSideState() *acuSideState {
	return &acuSideState{entries: make(map[string]*acuEntry)}
}

func acuKey(sessionID, cwd string) string {
	return strings.TrimSpace(sessionID) + "\x00" + strings.TrimSpace(cwd)
}

// storeNotification records an ACU table from either rail (stdio or leader).
// Full-table replace of the notification slot — an empty table replaces too.
// If a newer authoritative List value exists and differs, the entry is only
// marked pendingRefresh (§4.1: 无法排序的 ACU 不得覆盖较新 List 成功值).
func (s *acuSideState) storeNotification(sessionID, cwd string, cmds []core.SessionCommand) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entryLocked(sessionID, cwd)
	e.notif = cmds
	if e.listAt.IsZero() {
		return
	}
	if !acuSameSet(e.list, cmds) {
		e.pendingRefresh = true
	}
}

// storeListSuccess records the result of a successful authoritative pull
// (dedicated child load → session ACU). Clears failure/refresh marks.
func (s *acuSideState) storeListSuccess(sessionID, cwd string, cmds []core.SessionCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entryLocked(sessionID, cwd)
	e.list = cmds
	e.listAt = time.Now()
	e.unavailable = false
	e.pendingRefresh = false
}

// markListFailed marks the identity unavailable: the last real pull failed, so
// Execute must refuse rather than fall back to an older cached catalog (§4.1:
// 失败的 List 不能随后拿旧成功缓存执行).
func (s *acuSideState) markListFailed(sessionID, cwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entryLocked(sessionID, cwd)
	e.unavailable = true
	e.list = nil
	e.listAt = time.Time{}
}

// executeWhitelist returns the cached catalog serving as the Execute admission
// whitelist. ok=false when the identity is marked unavailable (a real pull must
// succeed first) or when no table has ever been stored.
func (s *acuSideState) executeWhitelist(sessionID, cwd string) ([]core.SessionCommand, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[acuKey(sessionID, cwd)]
	if !ok || e.unavailable {
		return nil, false
	}
	if e.list != nil {
		return e.list, true
	}
	if e.notif != nil {
		return e.notif, true
	}
	return nil, false
}

// invalidateSession drops the whole entry for one session (session rebuild /
// actor respawn → its notification slot belongs to the old actor).
func (s *acuSideState) invalidateSession(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.entries {
		if strings.HasPrefix(k, strings.TrimSpace(sessionID)+"\x00") {
			delete(s.entries, k)
		}
	}
}

// invalidateAll clears every entry (cwd/binary/config identity change).
func (s *acuSideState) invalidateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]*acuEntry)
	s.gen++
}

func (s *acuSideState) entryLocked(sessionID, cwd string) *acuEntry {
	k := acuKey(sessionID, cwd)
	e, ok := s.entries[k]
	if !ok {
		e = &acuEntry{}
		s.entries[k] = e
	}
	return e
}

func acuSameSet(a, b []core.SessionCommand) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, c := range a {
		set[c.Name] = true
	}
	for _, c := range b {
		if !set[c.Name] {
			return false
		}
	}
	return true
}

// --- D1 admission (§1 D1 / §4.1 / §7 正文组) ---
//
// Grok 面板目录 = 官方可执行目录 ∩ 准入集合。待样本项不展示；context 与未接入
// 的 pager-local surface 第一批排除；新命令不得只因出现在目录就自动获准。
//
// P6 证据准入的「正文组」判据（scripts/grokbuild-phase0/ADMISSION.md 终表）：
// host-turn 本地 built-in 命令 —— 零模型（totalTokens=0）、反馈正文经
// agent_message_chunk(_meta.hostTurn=true)（成功与失败文案同轨）、end_turn settle。
// 证据：samples/p6-turns.json A(hooks-list 成功)/B(hooks-add 非法路径失败) +
// [源码] slash_exec.rs builtin 表全部本地执行 ok_end_turn(0)。
//
// 2026-09-07 owner 裁决：面板收敛为 compact + goal（「hooks 那些命令我压根搞不懂，
// 也从来没用过」）。两条均在官方握手目录真实广播（samples/p2-handshake-
// notifications.jsonl 零模型样本，含 hint）。plan 不是 grok 斜杠命令（是会话
// 模式；模式切换已按 P7 取证裁决禁用，见 sessionMode 只读 chip）。compact/goal
// 的反馈形状未逐一取样，经通用 host-turn settle 路径呈现，owner 走查验收；
// hooks-* 家族移出面板与执行准入（不是 excluded 语义——无副作用问题，单纯
// owner 不用）。后续如需重新准入，回 ADMISSION.md 补证据。
var (
	// grokAdmittedCommands is the owner-ruled panel/execute admission set
	// (2026-09-07: compact + goal, replacing the P6 5×hooks-* wave).
	grokAdmittedCommands = map[string]struct{}{
		"compact": {},
		"goal":    {},
	}

	// grokExcludedCommands are ruled out of the first wave regardless of
	// evidence: context is pager-local (ShowContextInfo, no shell detail);
	// feedback/dream/flush/always-approve carry external-send / cross-session
	// memory / irreversible-permission side effects not yet isolated.
	grokExcludedCommands = map[string]struct{}{
		"context":        {},
		"feedback":       {},
		"dream":          {},
		"flush":          {},
		"always-approve": {},
	}
)

// applyGrokAdmission intersects the official catalog with the D1 admitted set.
// An empty admitted set yields an empty panel — the honest pre-P6 state.
func applyGrokAdmission(cmds []core.SessionCommand) []core.SessionCommand {
	out := make([]core.SessionCommand, 0, len(cmds))
	for _, c := range cmds {
		if _, excluded := grokExcludedCommands[c.Name]; excluded {
			continue
		}
		if _, admitted := grokAdmittedCommands[c.Name]; !admitted {
			continue
		}
		out = append(out, c)
	}
	return out
}
