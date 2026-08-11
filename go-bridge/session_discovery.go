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
// fingerprint 覆盖 native relative order 与每个成员的 id/updatedAt/title/normalized directory/
// projectId；新增、删除、recency、标题、可见目录或原生排序变化都会触发 sessions_changed。
//
// 数据源对齐（§10）：Claude 取全局 catalog；Codex/Grok 取与 declared/undeclared list_sessions
// 共用的 native visible membership；其它 backend 仍取 agent.ListSessions。

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
		started := time.Now()
		current, count, err := h.discoveryFingerprint(ctx, id, agent)
		duration := time.Since(started)
		if err != nil {
			// Previously this branch was silent. A recurring enumerate error leaves
			// seen[id] at the seed fingerprint forever → fingerprint never changes →
			// sessions_changed never fires, with no log to reveal why. Skip without
			// updating seen (preserve last good fingerprint so a later successful
			// poll can still detect change).
			slog.Warn("go-bridge: session discovery fingerprint error (no broadcast)",
				"phase", tag, "backend", id, "durationMs", duration.Milliseconds(), "error", err.Error())
			continue
		}
		prev, hadPrev := seen[id]
		if seed || !hadPrev {
			seen[id] = current
			slog.Info("go-bridge: session discovery snapshot seeded",
				"backend", id, "sessionCount", count, "durationMs", duration.Milliseconds())
			continue
		}
		// Phase 7 §442：fingerprint 变化即 catalog 变化（新增/删除/更新任一）→ 触发 sessions_changed。
		// 不再区分 new/removed：fingerprint 是确定摘要，任一成员或 updatedAt 变化都改写它，
		// 这覆盖了「session 被归档（从 visible 移除）」「新 session 出现」「既有 session 收到新 turn」
		// 三种情况，客户端刷新 list_sessions 即可拿到最新 catalog。
		if current != prev {
			// Fence every declared snapshot scope before exposing the new fingerprint or
			// publishing its notification. A client reacting immediately to the event can
			// therefore never reuse a pre-change global/directory snapshot.
			h.openCodeCatalogWireCache().FenceBackend(id)
			seen[id] = current
			slog.Info("go-bridge: sessions_changed (catalog fingerprint changed)",
				"backend", id, "sessionCount", count, "durationMs", duration.Milliseconds())
			if _, err := h.eventPublisher.PublishControlPlane(LogicalEvent{
				BackendID: id,
				Event:     "sessions_changed",
				Data:      map[string]interface{}{"backendId": id},
				Broadcast: true,
			}); err != nil {
				slog.Error("go-bridge: sessions_changed control-plane publish rejected",
					"backend", id, "error", err.Error())
			}
		} else {
			seen[id] = current
		}
	}
}

// discoveryFingerprint returns the catalog fingerprint for a backend, computed from
// the SAME data source list_sessions currently serves (Phase 7 §442). It derives
// Codex/Grok use listSemanticFingerprint over native relative order and the frozen
// membership tuple. Claude/other compatibility sources retain wireFingerprint.
//
// 数据源（对齐 list_sessions 当前服务的源）：
//   - Claude：h.claudeSessions.list()（全局 catalog，archived 排除——归档须改写 fingerprint）。
//   - Codex/Grok：ctx-aware native visible membership（不 enrich/pin、不走 disk scan）。
//   - 其余：agent.ListSessions()。
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
	listCtx, cancel := context.WithTimeout(ctx, catalogRequestTimeout)
	defer cancel()
	if agent.Name() == "codex" {
		wire, _, err := h.codexVisibleMembership(listCtx, id, "")
		if err != nil {
			return "", 0, err
		}
		return listSemanticFingerprint(wire), len(wire), nil
	}
	if agent.Name() == "grokbuild" {
		wire, _, err := h.grokVisibleMembership(listCtx, id)
		if err != nil {
			return "", 0, err
		}
		return listSemanticFingerprint(wire), len(wire), nil
	}
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
