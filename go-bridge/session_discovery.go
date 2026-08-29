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
// 数据源对齐（§10）：Claude 取全局 catalog；Codex/Grok 取与 declared list_sessions
// 共用的 native visible membership；其它 backend 仍取 agent.ListSessions。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sessionDiscoveryInterval is the snapshot cadence. Package var so tests can shrink it.
var sessionDiscoveryInterval = 60 * time.Second

// A small native recency-head probe restores seconds-scale Codex detection without running the
// 500+ member full catalog continuously. A changed head is only a trigger: the normal full native
// fingerprint still owns fence/seen/publish. The 60-second full scan remains the safety net.
var codexDiscoveryHintInterval = 3 * time.Second

// Remote Control carries each thread/list page through the Desktop data plane.
// The official app-server emits thread lifecycle and turn-boundary notifications;
// those signals trigger the authoritative refresh directly. Keep the periodic
// 60-second safety scan below, but do not send a second Remote thread/list head
// request every few seconds while the stream is otherwise idle.
var codexRemoteDiscoveryHintInterval time.Duration

var (
	codexDiscoveryRetryBase = 15 * time.Second
	codexDiscoveryRetryMax  = 2 * time.Minute
)

const codexDiscoveryHeadLimit = 25

// Grok ACP session/list has no bounded head/page parameter, but the production catalog is small
// and the managed singleton answers a full native membership request in sub-millisecond time. Run
// that authoritative fingerprint on a backend-local cadence only while a client is connected; the
// 60-second scan remains the disconnected/error-recovery safety net.
var grokDiscoveryFastInterval = 5 * time.Second

// StartSessionDiscoveryWatcher launches the background new-session watcher. The
// first scan seeds the snapshot without broadcasting (no startup burst).
func (h *Handlers) StartSessionDiscoveryWatcher(ctx context.Context) {
	go h.runSessionDiscovery(ctx)
}

func (h *Handlers) runSessionDiscovery(ctx context.Context) {
	interval := sessionDiscoveryInterval
	codexHintInterval := codexDiscoveryHintInterval
	grokFastInterval := grokDiscoveryFastInterval
	slog.Info("go-bridge: session discovery watcher started",
		"interval", interval.String(),
		"codexRemoteHintInterval", codexRemoteDiscoveryHintInterval.String(),
		"codexDiscoveryRetryBase", codexDiscoveryRetryBase.String(),
		"codexDiscoveryRetryMax", codexDiscoveryRetryMax.String(),
		"backends", len(h.Agents()))
	var workers sync.WaitGroup
	for id, agent := range h.Agents() {
		backendHintInterval := codexHintInterval
		if id == "codex-remote" {
			backendHintInterval = codexRemoteDiscoveryHintInterval
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			h.runBackendSessionDiscovery(ctx, id, agent, interval, backendHintInterval, grokFastInterval)
		}()
	}
	<-ctx.Done()
	workers.Wait()
	slog.Info("go-bridge: session discovery watcher stopped (context done)")
}

// runBackendSessionDiscovery owns one backend's cadence and last-good fingerprint. Backends are
// deliberately isolated: a provider that blocks despite ctx cancellation must not starve native
// refresh for every other backend (owner A-O1 production failure, 2026-08-11).
func (h *Handlers) runBackendSessionDiscovery(ctx context.Context, id string, agent core.Agent, interval, codexHintInterval, grokFastInterval time.Duration) {
	// Keep panic recovery inside this tracked worker. Re-arming via an untracked goroutine would
	// let runSessionDiscovery report stopped while the replacement still accessed handler state.
	for ctx.Err() == nil {
		if !h.runBackendSessionDiscoveryLoop(ctx, id, agent, interval, codexHintInterval, grokFastInterval) {
			return
		}
	}
}

func (h *Handlers) runBackendSessionDiscoveryLoop(ctx context.Context, id string, agent core.Agent, interval, codexHintInterval, grokFastInterval time.Duration) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			slog.Error("go-bridge: session discovery backend worker recovered from panic — worker continuing",
				"backend", id, "error", r)
		}
	}()
	seen := map[string]string{}
	_, isCodexCatalog := agent.(codexThreadHeadLister)
	retry := catalogDiscoveryRetry{base: codexDiscoveryRetryBase, max: codexDiscoveryRetryMax}
	probeRetry := catalogDiscoveryRetry{base: codexDiscoveryRetryBase, max: codexDiscoveryRetryMax}
	if !h.snapshotBackendSession(ctx, seen, true, id, agent) && isCodexCatalog {
		retry.fail(time.Now())
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var hintTicker *time.Ticker
	var hintC <-chan time.Time
	// 能力断言而非 id=="codex"：codex-web（与 codex 同属 thread/list 富 catalog
	// seam）同样获得 3s recency-head 探测——Mac 新建 session 时 hint 立即触发
	// authoritative 全量刷新，无需等 60s scan（P0-4）。
	if _, ok := agent.(codexThreadHeadLister); ok {
		if codexHintInterval > 0 {
			hintTicker = time.NewTicker(codexHintInterval)
			hintC = hintTicker.C
			defer hintTicker.Stop()
		}
	} else if id == "grokbuild" {
		if _, ok := agent.(grokSessionLister); ok && grokFastInterval > 0 {
			hintTicker = time.NewTicker(grokFastInterval)
			hintC = hintTicker.C
			defer hintTicker.Stop()
		}
	}
	// Event-capable catalogs signal an immediate authoritative rescan instead
	// of waiting for the safety-net cadence. The signal carries no catalog data;
	// fingerprint/list remain the sole truth.
	var refreshC <-chan struct{}
	if signaler, ok := agent.(core.CatalogRefreshSignaler); ok {
		refreshC = signaler.CatalogRefreshSignals()
	}
	var hintSeen string
	hintSeeded := false
	authoritativeRefresh := func(trigger string) bool {
		if isCodexCatalog && !retry.ready(time.Now()) {
			slog.Debug("go-bridge: Codex discovery authoritative refresh deferred",
				"backend", id, "trigger", trigger, "retryAt", retry.next)
			return false
		}
		if h.snapshotBackendSession(ctx, seen, false, id, agent) {
			if isCodexCatalog {
				retry.succeed()
			}
			return true
		}
		if isCodexCatalog {
			delay := retry.fail(time.Now())
			slog.Warn("go-bridge: Codex discovery authoritative refresh backed off",
				"backend", id, "trigger", trigger, "retryDelay", delay.String())
		}
		return false
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if authoritativeRefresh("safety-poll") && isCodexCatalog {
				hintSeeded = false
			}
		case <-refreshC:
			// Coalesced by the signaler's buffered channel; the fingerprint
			// diff itself decides whether sessions_changed fires.
			if authoritativeRefresh("catalog-signal") && isCodexCatalog {
				hintSeeded = false
			}
		case <-hintC:
			if !h.broadcaster.HasConnections() {
				continue
			}
			if id == "grokbuild" {
				// Unlike Codex, Grok has no cheap bounded head RPC. This call is already the
				// authoritative native fingerprint, so it directly owns fence/seen/publish and
				// must not be followed by a duplicate full fetch.
				authoritativeRefresh("grok-fast-poll")
				continue
			}
			if isCodexCatalog && !retry.ready(time.Now()) {
				continue
			}
			if isCodexCatalog && !probeRetry.ready(time.Now()) {
				continue
			}
			probeStarted := time.Now()
			current, err := h.codexDiscoveryHintFingerprint(ctx, agent)
			probeDuration := time.Since(probeStarted)
			if err != nil {
				slog.Warn("go-bridge: Codex discovery head probe error (no full refresh)",
					"backend", id, "durationMs", probeDuration.Milliseconds(), "error", err.Error())
				if isCodexCatalog {
					delay := probeRetry.fail(time.Now())
					slog.Warn("go-bridge: Codex discovery head probe backed off",
						"backend", id, "retryDelay", delay.String())
				}
				continue
			}
			if isCodexCatalog {
				probeRetry.succeed()
			}
			if !hintSeeded {
				hintSeen = current
				hintSeeded = true
				continue
			}
			if current == hintSeen {
				continue
			}
			if !retry.ready(time.Now()) {
				continue
			}
			slog.Info("go-bridge: Codex discovery head changed; running authoritative full refresh",
				"backend", id, "headProbeDurationMs", probeDuration.Milliseconds())
			if authoritativeRefresh("head-change") {
				hintSeen = current
			}
		}
	}
}

type catalogDiscoveryRetry struct {
	base    time.Duration
	max     time.Duration
	attempt uint
	next    time.Time
}

func (r *catalogDiscoveryRetry) ready(now time.Time) bool {
	return r.next.IsZero() || !now.Before(r.next)
}

func (r *catalogDiscoveryRetry) fail(now time.Time) time.Duration {
	delay := r.base
	if delay <= 0 {
		delay = time.Second
	}
	for i := uint(0); i < r.attempt && delay < r.max; i++ {
		delay *= 2
		if r.max > 0 && delay > r.max {
			delay = r.max
		}
	}
	r.attempt++
	r.next = now.Add(delay)
	return delay
}

func (r *catalogDiscoveryRetry) succeed() {
	r.attempt = 0
	r.next = time.Time{}
}

func (h *Handlers) codexDiscoveryHintFingerprint(ctx context.Context, agent core.Agent) (string, error) {
	lister, ok := agent.(codexThreadHeadLister)
	if !ok {
		return "", fmt.Errorf("Codex agent does not support native head probe")
	}
	probeCtx, cancel := context.WithTimeout(ctx, catalogRequestTimeout)
	defer cancel()
	infos, err := lister.FetchThreadListHead(probeCtx, "", codexDiscoveryHeadLimit)
	if err != nil {
		return "", err
	}
	wire := sessionsToWire(infos)
	if usesCodexWorkspaceCatalog(agent) {
		wire = filterCodexCatalogSessions(wire)
	}
	// 只用顺序+id：语义指纹（含 updatedAt）会在流式 turn 中随每个 delta 变化，
	// 让长任务执行期间每 3s 误触发一次全量刷新（2026-08-23 真机风暴）。
	return listOrderFingerprint(wire), nil
}

// snapshotSessions samples every backend's catalog fingerprint once and broadcasts
// "sessions_changed" for any backend whose fingerprint changed since the last scan
// (Phase 7 §442：provider fingerprint 驱动 sessions_changed)。
func (h *Handlers) snapshotSessions(ctx context.Context, seen map[string]string, seed bool) {
	for id, agent := range h.Agents() {
		h.snapshotBackendSession(ctx, seen, seed, id, agent)
	}
}

func (h *Handlers) snapshotBackendSession(ctx context.Context, seen map[string]string, seed bool, id string, agent core.Agent) bool {
	tag := "poll"
	if seed {
		tag = "seed"
	}
	started := time.Now()
	current, count, rawCount, err := h.discoveryFingerprint(ctx, id, agent)
	duration := time.Since(started)
	if err != nil {
		// Live-only backends (dsh) have no list by design — a quiet skip, not
		// a recurring warning (there is no fingerprint to preserve).
		if errors.Is(err, core.ErrNotSupported) {
			slog.Debug("go-bridge: session discovery skipped (backend has no session list)",
				"phase", tag, "backend", id)
			return false
		}
		// Previously this branch was silent. A recurring enumerate error leaves
		// seen[id] at the seed fingerprint forever → fingerprint never changes →
		// sessions_changed never fires, with no log to reveal why. Skip without
		// updating seen (preserve last good fingerprint so a later successful
		// poll can still detect change).
		slog.Warn("go-bridge: session discovery fingerprint error (no broadcast)",
			"phase", tag, "backend", id, "durationMs", duration.Milliseconds(), "error", err.Error())
		return false
	}
	prev, hadPrev := seen[id]
	if seed || !hadPrev {
		seen[id] = current
		slog.Info("go-bridge: session discovery snapshot seeded",
			"backend", id, "sessionCount", count, "rawCount", rawCount, "durationMs", duration.Milliseconds())
		return true
	}
	// Phase 7 §442：fingerprint 变化即 catalog 变化（新增/删除/更新任一）→ 触发 sessions_changed。
	// 不再区分 new/removed：fingerprint 是确定摘要，任一成员或 updatedAt 变化都改写它，
	// 这覆盖了「session 被归档（从 visible 移除）」「新 session 出现」「既有 session 收到新 turn」
	// 三种情况，客户端刷新 list_sessions 即可拿到最新 catalog。
	if current != prev {
		catalogGeneration := h.catalogGeneration.Add(1)
		// Fence every declared snapshot scope before exposing the new fingerprint or
		// publishing its notification. A client reacting immediately to the event can
		// therefore never reuse a pre-change global/directory snapshot.
		h.openCodeCatalogWireCache().FenceBackend(id)
		seen[id] = current
		slog.Info("go-bridge: sessions_changed (catalog fingerprint changed)",
			"backend", id, "catalogGeneration", catalogGeneration,
			"sessionCount", count, "rawCount", rawCount, "durationMs", duration.Milliseconds())
		if _, err := h.eventPublisher.PublishControlPlane(LogicalEvent{
			BackendID:         id,
			Event:             "sessions_changed",
			Data:              map[string]interface{}{"backendId": id},
			Broadcast:         true,
			CatalogGeneration: catalogGeneration,
		}); err != nil {
			slog.Error("go-bridge: sessions_changed control-plane publish rejected",
				"backend", id, "catalogGeneration", catalogGeneration, "error", err.Error())
		}
		// Phase 5：catalog 指纹变化同样意味着后台任务面可能变化（DSH 子任务
		// 是 session 行、Claude sidechain 挂在 session 目录下）。对有任务面的
		// backend 追加一条 background_tasks.changed invalidate 通知——客户端
		// 重新 background_tasks.list 拿真值，事件本身不携带任务数据（不做
		// 双真值）。
		if id == "claudecode" {
			h.publishBackgroundTasksChanged(id, catalogGeneration)
		} else if _, ok := agent.(core.BackgroundTaskProvider); ok {
			h.publishBackgroundTasksChanged(id, catalogGeneration)
		}
	} else {
		seen[id] = current
	}
	return true
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
// 返回 (fingerprint, filteredCount, rawCount, error)。raw/filter 同出一次 fetch
// （Codex 的 thread/list 全量只拉一次，过滤前后计数用于日志审计 438/429 差异）。
// error → 调用方 skip 本周期（不更新 seen、不广播）。
func (h *Handlers) discoveryFingerprint(ctx context.Context, id string, agent core.Agent) (string, int, int, error) {
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
		return wireFingerprint(visible), len(visible), len(all), nil
	}
	listCtx, cancel := context.WithTimeout(ctx, catalogRequestTimeout)
	defer cancel()
	// 能力断言而非 Name()=="codex"：codex-web 与 codex 共用同一 thread/list 富
	// catalog seam（P0-4），discovery fingerprint 与 list_sessions 天然同源。
	if usesCodexWorkspaceCatalog(agent) {
		wire, rawCount, err := h.codexVisibleMembershipCounts(listCtx, id, "")
		if err != nil {
			return "", 0, 0, err
		}
		return listSemanticFingerprint(wire), len(wire), rawCount, nil
	}
	if agent.Name() == "codex-remote" {
		infos, err := agent.ListSessions(listCtx)
		if err != nil {
			return "", 0, 0, err
		}
		return remoteCatalogFingerprint(sessionsToWire(infos)), len(infos), len(infos), nil
	}
	if agent.Name() == "grokbuild" {
		wire, _, err := h.grokVisibleMembership(listCtx, id)
		if err != nil {
			return "", 0, 0, err
		}
		return listSemanticFingerprint(wire), len(wire), len(wire), nil
	}
	infos, err := agent.ListSessions(listCtx)
	if err != nil {
		return "", 0, 0, err
	}
	// fingerprint over the disk-scan wire maps（与 list_sessions v1 同源）。sessionsToWire 把
	// AgentSessionInfo 映射成 wire map，wireFingerprint 按 id 排序取 id|updatedAtMillis 摘要。
	wire := sessionsToWire(infos)
	return wireFingerprint(wire), len(infos), len(infos), nil
}

// lineage note：原 sessionIDSet / diffNewSessions / diffRemovedSessions 在 Phase 7 §442
// fingerprint 化后移除——fingerprint 字符串比较取代了 ID-set diff，且更准：覆盖 updatedAt 变化
// （既有 session 收到新 turn → recency 变 → fingerprint 变 → sessions_changed 触发，ID-set diff
// 会漏掉这种情况）。
