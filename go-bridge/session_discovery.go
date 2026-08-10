package gobridge

// Session-discovery push (optional, multi-client-streaming-sync §6).
//
// MacBridge periodically samples each backend's catalog fingerprint; when it changes
// (a new/removed/updated session — e.g. a turn opened in a native app while iOS sits
// on the session list), it broadcasts a "sessions_changed" event. Clients refresh
// list_sessions on receipt. This is a latency win only — clients also refresh on
// reconnect/foreground, and iOS posts sessionListNeedsRefresh on turn activity, so
// absence of this push is not a correctness gap. Routing relies on Broadcaster.Targets'
// all-backend / all-connections fallback (types.go Targets), which delivers a
// backend-scoped broadcast with empty SessionID to list-viewing clients.
//
// Phase 7 §442 / §335：sessions_changed 由 catalog fingerprint 驱动（不再是 ID-set diff）。
// fingerprint 覆盖有序成员的 id|updatedAtMillis，故新增/删除/更新（recency 变化）任一都改写
// fingerprint → 触发 sessions_changed。这比 ID-set diff 更准（ID-set 漏掉「既有 session 收到新
// turn → updatedAt 变化」的情况，列表 recency 不刷新）。
//
// 数据源对齐（§10）：fingerprint 取自每个 backend「list_sessions 当前实际服务的同一数据源」
// —— Claude 取 h.claudeSessions.list()（全局 catalog，已排除 archived），其余取 agent.ListSessions()
// （disk-scan）。codex/grok 的 native catalog（FetchThreadList/FetchSessionList）数据源切换随 iOS
// v2 迁移一并落地：poller 与 list_sessions 必须同源切换，否则 v2 未上线时 poller 看到的 native
// 变化与 iOS 看到的 disk-scan 不一致（§10 发布顺序）。当前 iOS 仍在 v1 → poller 同样走 disk-scan。

import (
	"context"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sessionDiscoveryInterval is the snapshot cadence. Package var so tests can shrink it.
var sessionDiscoveryInterval = 60 * time.Second

// StartSessionDiscoveryWatcher launches the background new-session watcher. The
// first scan seeds the snapshot without broadcasting (no startup burst).
func (h *Handlers) StartSessionDiscoveryWatcher(ctx context.Context) {
	go h.runSessionDiscovery(ctx)
}

func (h *Handlers) runSessionDiscovery(ctx context.Context) {
	// Critical: this goroutine must never silently die. snapshotSessions walks
	// Claude transcript files (209MB datasets, 16MB-per-line JSONL) and has, in
	// production, produced ZERO sessions_changed events across all logs since 7/5
	// — consistent with an unrecovered panic killing the watcher with no log line.
	// recover keeps the loop alive and emits a visible error so a future panic
	// can no longer hide. Control-plane only (guard #8); does not touch timeline.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("go-bridge: session discovery watcher recovered from panic — loop continuing",
				"error", r)
			// Re-arm by re-entering; ctx still governs exit.
			go h.runSessionDiscovery(ctx)
		}
	}()

	slog.Info("go-bridge: session discovery watcher started",
		"interval", sessionDiscoveryInterval.String())
	seen := map[string]string{}
	h.snapshotSessions(ctx, seen, true)
	ticker := time.NewTicker(sessionDiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("go-bridge: session discovery watcher stopped (context done)")
			return
		case <-ticker.C:
			h.snapshotSessions(ctx, seen, false)
		}
	}
}

// snapshotSessions samples every backend's catalog fingerprint once and broadcasts
// "sessions_changed" for any backend whose fingerprint changed since the last scan
// (Phase 7 §442：provider fingerprint 驱动 sessions_changed)。
func (h *Handlers) snapshotSessions(ctx context.Context, seen map[string]string, seed bool) {
	tag := "poll"
	if seed {
		tag = "seed"
	}
	for id, agent := range h.Agents() {
		current, count, err := h.discoveryFingerprint(ctx, id, agent)
		if err != nil {
			// Previously this branch was silent. A recurring enumerate error leaves
			// seen[id] at the seed fingerprint forever → fingerprint never changes →
			// sessions_changed never fires, with no log to reveal why. Skip without
			// updating seen (preserve last good fingerprint so a later successful
			// poll can still detect change).
			slog.Warn("go-bridge: session discovery fingerprint error (no broadcast)",
				"phase", tag, "backend", id, "error", err.Error())
			continue
		}
		prev, hadPrev := seen[id]
		seen[id] = current
		if seed || !hadPrev {
			slog.Info("go-bridge: session discovery snapshot seeded",
				"backend", id, "sessionCount", count)
			continue
		}
		// Phase 7 §442：fingerprint 变化即 catalog 变化（新增/删除/更新任一）→ 触发 sessions_changed。
		// 不再区分 new/removed：fingerprint 是确定摘要，任一成员或 updatedAt 变化都改写它，
		// 这覆盖了「session 被归档（从 visible 移除）」「新 session 出现」「既有 session 收到新 turn」
		// 三种情况，客户端刷新 list_sessions 即可拿到最新 catalog。
		if current != prev {
			slog.Info("go-bridge: sessions_changed (catalog fingerprint changed)",
				"backend", id, "sessionCount", count)
			h.deltaBatcher.Send(LogicalEvent{
				BackendID: id,
				Event:     "sessions_changed",
				Data:      map[string]interface{}{"backendId": id},
				Broadcast: true,
			})
		}
	}
}

// discoveryFingerprint returns the catalog fingerprint for a backend, computed from
// the SAME data source list_sessions currently serves (Phase 7 §442). It derives
// wireFingerprint (id|updatedAtMillis, sorted by id) over the visible session set;
// new/removed/updated sessions all change the fingerprint.
//
// 数据源（对齐 list_sessions 当前服务的源）：
//   - Claude：h.claudeSessions.list()（全局 catalog，archived 排除——归档须改写 fingerprint）。
//   - 其余：agent.ListSessions()（disk-scan）。codex/grok native catalog 切换随 v2 迁移（§10）。
//
// It must sample the SAME set a client sees on list_sessions, otherwise a change
// detected by the client is invisible to the poller and sessions_changed never fires.
// Claude is the special case documented in discoverySessionIDs' lineage: agent.ListSessions()
// resolves only the workDir project and returned 0 sessions in production, so Claude
// derives from the authoritative global catalog (h.claudeSessions) that list_sessions serves.
//
// 返回 (fingerprint, visibleCount, error)。error → 调用方 skip 本周期（不更新 seen、不广播）。
func (h *Handlers) discoveryFingerprint(ctx context.Context, id string, agent core.Agent) (string, int, error) {
	if id == "claude" && h.claudeSessions != nil {
		all := h.claudeSessions.list("", nil)
		visible := make([]map[string]interface{}, 0, len(all))
		for _, wire := range all {
			// Match what a client sees: archived sessions are hidden from the
			// active list (web session-grouping filters on archivedAtMillis), so
			// the poller must exclude them too — otherwise archiving a session
			// would not change the fingerprint and sessions_changed would never fire.
			if archivedMs, ok := wire["archivedAtMillis"]; ok {
				if ms, ok := archivedMs.(int64); ok && ms > 0 {
					continue
				}
				if f, ok := archivedMs.(float64); ok && f > 0 {
					continue
				}
			}
			visible = append(visible, wire)
		}
		return wireFingerprint(visible), len(visible), nil
	}
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	infos, err := agent.ListSessions(listCtx)
	if err != nil {
		return "", 0, err
	}
	// fingerprint over the disk-scan wire maps（与 list_sessions v1 同源）。sessionsToWire 把
	// AgentSessionInfo 映射成 wire map，wireFingerprint 按 id 排序取 id|updatedAtMillis 摘要。
	wire := sessionsToWire(infos)
	return wireFingerprint(wire), len(infos), nil
}

// lineage note：原 sessionIDSet / diffNewSessions / diffRemovedSessions 在 Phase 7 §442
// fingerprint 化后移除——fingerprint 字符串比较取代了 ID-set diff，且更准：覆盖 updatedAt 变化
// （既有 session 收到新 turn → recency 变 → fingerprint 变 → sessions_changed 触发，ID-set diff
// 会漏掉这种情况）。
