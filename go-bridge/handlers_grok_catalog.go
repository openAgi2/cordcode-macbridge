package gobridge

// handlers_grok_catalog.go 是 Grok list_sessions 的 catalog 主线路径（设计 §5.4 Phase 3 /
// §4.1.1 / §4.3，与 handlers_codex_catalog.go 刻意同构）。
//
//   - 复用 generic catalogWireSnapshotCache（与 Codex/OpenCode 共享：scope-keyed，
//     grokbuild scope key 与其它 backend 互斥，互不复用快照）；
//   - builder（buildGrokEnrichedSessions）跑 session/list 富 wire 管线（FetchSessionList →
//     sessionsToWire → enrich/overlay → sortSessionsByUpdatedAt）；
//   - capability 门控（catalog_cursor_epoch_v2）：declared → v2 epoch-bearing cursor +
//     cursor_stale；undeclared → v1 paginateSessionList（既有 disk-scan 经 agent.ListSessions
//     的等价 wire 切片）byte-for-byte 不变。
//
// Grok 与 Codex 的差异：Grok 的 ACP session/list **不按 cwd 过滤**（Grok native catalog 跨
// 所有 cwd 返回），故 FetchSessionList 不取 dir、scope key 无 dir 维度。映射遵循 frozen
// fixture（testdata/session_list_sanitized.json）。
//
// §10 发布顺序：capability 上线前（iOS Phase 6 才声明），MacBridge 不得对任何连接发射 v2
// cursor，且数据源 disk-scan→session/list 同样只对 declared 连接生效。当前 declared 恒为
// false → grokHandleListSessions 不可达 → 零行为变化。

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/grokbuild"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// injectGrokCatalogRegistrar 把 bridge 的 catalog ProcessRegistry 注入 *grokbuild.Agent，
// 使其 stdio catalog 子进程注册到 bridge shutdown 回收链（§4.3 / §11）。由 RegisterAgent 在
// 注册 agent 后调用。非 grokbuild agent 无操作。
//
// 注册不依赖 h.mu（catalogProcessRegistry 走 sync.Once，SetCatalogSubprocessRegistrar 走
// grokbuild.Agent 自身 catalogClientMu），故可在持有 h.mu 时安全调用。
func (h *Handlers) injectGrokCatalogRegistrar(agent core.Agent) {
	ga, ok := agent.(*grokbuild.Agent)
	if !ok {
		return
	}
	ga.SetCatalogSubprocessRegistrar(h.catalogProcessRegistry())
}

// grokSessionLister 是 go-bridge 与 *grokbuild.Agent 之间的 catalog seam（结构化满足）。
// 定义为接口使 builder 可在注入 fake lister 时单测（不启动真实 grok 子进程）。
// *grokbuild.Agent.FetchSessionList 满足该接口。注意：与 codexThreadLister 不同，**不取
// dir 参数**（Grok session/list 非 cwd-scoped，§5.4）。
type grokSessionLister interface {
	FetchSessionList(ctx context.Context) ([]core.AgentSessionInfo, error)
}

// grokCatalogWireCache 复用 generic catalogWireSnapshotCache（与 Codex/OpenCode 共享同一实例：
// scope-keyed，grokCatalogScopeKey 与其它 backend scope key 互斥，互不复用快照）。
func (h *Handlers) grokCatalogWireCache() *catalogWireSnapshotCache {
	return h.openCodeCatalogWireCache()
}

// grokCatalogScopeKey 派生 Grok scope 缓存键：仅 backendID。Grok ACP session/list 跨 cwd
// 返回所有 session（§5.4 / frozen fixture），无 workspace dir 维度，故一维 key。空 dir 段
// 仅用于与 codex/opencode（两段 key）格式对齐，实际唯一性靠 backendID="grokbuild"。
func grokCatalogScopeKey(backendID string) string {
	return backendID + "\x00"
}

// buildGrokEnrichedSessions 执行 §5.1 step 2-3 的富 wire 管线：FetchSessionList（managed ACP
// subprocess session/list，跨 cwd，recency 排序）→ sessionsToWire → enrichSessionStatesForList
// → overlayPinnedState。Phase 7 §445 收敛（与 OpenCode Phase 4 / codex 同形）：**不再
// sortSessionsByUpdatedAt 覆盖**——session/list 的 recency 是权威上游序，本地 updatedAt 重排会改写它。
// v1（undeclared → generic disk-scan agent.ListSessions）不经此 builder，故移除 sort 只影响 v2 主线。
// builder 仅由 grokHandleListSessions（v2 DECLARED）调用。
//
// 失败必须显式返回 error（§5.1 step 6 / §5.4 #5：删除 catalog 失败时静默回退 JSONL 的路径；
// 握手缺 session/list 能力时 FetchSessionList 已 fail-closed，此处不再二次 fallback）。
func (h *Handlers) buildGrokEnrichedSessions(backendID string) ([]map[string]interface{}, error) {
	agent, ok := h.getAgent(backendID)
	if !ok {
		return nil, fmt.Errorf("grokbuild agent not registered for backend %q", backendID)
	}
	lister, ok := agent.(grokSessionLister)
	if !ok {
		return nil, fmt.Errorf("grokbuild agent %q does not support session/list catalog", backendID)
	}
	sessions, err := lister.FetchSessionList(context.Background())
	if err != nil {
		return nil, err
	}
	mapped := sessionsToWire(sessions)
	mapped = h.enrichSessionStatesForList(mapped, agent, h.getRunningMap(context.Background(), agent))
	h.overlayPinnedState(mapped, "grokbuild")
	return mapped, nil
}

// grokHandleListSessions 是 Grok list_sessions 的 v2 catalog 主线路径（DECLARED：
// hello 声明 catalog_cursor_epoch_v2）。调用方（handleListSessions）仅在 capability 已声明时
// 路由到此；undeclared 连接走既有 generic disk-scan（agent.ListSessions）路径 byte-for-byte
// 不变（§10 发布顺序：capability 上线前 MacBridge 不得对任何连接发射 v2 cursor，且数据源
// 从 disk-scan 切到 session/list 同样只对 declared 连接生效——iOS Phase 6 才声明 → 当前零
// 行为变化）。
//
// Grok session/list 非 cwd-scoped → builder 不取 dir（与 codexHandleListSessions 的 cwd=workDir
// 不同）。
func (h *Handlers) grokHandleListSessions(conn Connection, msg WireMessage, agent core.Agent) {
	limit := h.effectiveSessionListLimit(extractPositiveInt(msg, "limit"))
	cursor := extractStringParam(msg, "cursor")
	if limit > 1000 {
		limit = 1000
	}
	started := time.Now()

	// v2 快照路径（§4.1.1 / §5.1 step 2）。page-0：FetchOrReuse(builder) 取/建快照；
	// page-N：Peek（不重建）→ validateCursorV2 → 切片 / cursor_stale。
	scopeKey := grokCatalogScopeKey(msg.BackendID)
	cache := h.grokCatalogWireCache()
	result, staleErr, err := cache.pageV2(scopeKey, cursor, limit, func() ([]map[string]interface{}, error) {
		return h.buildGrokEnrichedSessions(msg.BackendID)
	})
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	if staleErr != nil {
		// Phase 7 §443 可观测性：cursor_stale 是 live/stale 协商结果（非错误），记录脱敏指标便于
		// 统计 stale 触发率。Grok session/list 非 cwd-scoped，无 dir 维度。
		slog.Info("grokbuild list_sessions cursor_stale",
			"cursor_present", cursor != "",
			"duration_ms", time.Since(started).Milliseconds(),
		)
		conn.SendResult(msg.RequestID, nil, staleErr) // cursor_stale（Retryable）
		return
	}
	if ws, ok := result["sessions"].([]map[string]interface{}); ok {
		slog.Info("grokbuild list_sessions v2 (session/list)",
			"limit", limit,
			"cursor_present", cursor != "",
			"result_count", len(ws),
			"next_cursor_present", result["hasMore"] == true,
			"catalog_alive_procs", len(h.catalogProcessRegistry().AlivePIDs()), // Phase 7 §443 活跃 catalog 子进程数
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}
	conn.SendResult(msg.RequestID, result, nil)
}
