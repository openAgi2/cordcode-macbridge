package gobridge

// handlers_codex_catalog.go 是 Codex list_sessions 的 catalog 主线路径（设计 §5.1 step 1-6 /
// §4.1.1 / §4.3，Phase 2 Stream A chunk 2A-2/3）。它与 OpenCode 的 handlers_opencode.go
// 1C 接线刻意同构：
//
//   - 复用 generic catalogWireSnapshotCache（与 OpenCode 共享：scope-keyed，codex/opencode
//     scope key 互斥，互不复用快照）；
//   - builder（buildCodexEnrichedSessions）跑 thread/list 富 wire 管线（FetchThreadList →
//     sessionsToWire → enrich/overlay）；
//   - declared catalog_cursor_epoch_v2 → v2 epoch-bearing cursor + cursor_stale；
//   - undeclared clients are rejected by the shared dispatch gate before this handler.

import (
	"context"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// injectCodexCatalogRegistrar 把 bridge 的 catalog ProcessRegistry 注入 *codex.Agent，使其 stdio
// catalog 子进程注册到 bridge shutdown 回收链（§4.3 进程管理红线 / §11）。由 RegisterAgent 在
// 注册 agent 后调用。非 codex agent 无操作。
//
// 注册不依赖 h.mu（catalogProcessRegistry 走 sync.Once，SetCatalogSubprocessRegistrar 走 codex.Agent
// 自身 mu），故可在持有 h.mu 时安全调用。ws 传输的 catalog client 无子进程，registrar 不参与。
func (h *Handlers) injectCodexCatalogRegistrar(agent core.Agent) {
	ca, ok := agent.(*codex.Agent)
	if !ok {
		return
	}
	ca.SetCatalogSubprocessRegistrar(h.catalogProcessRegistry())
}

// codexThreadLister 是 go-bridge 与 *codex.Agent 之间的 catalog seam（结构化满足）。
// 定义为接口使 builder 可在注入 fake lister 时单测（不启动真实 codex app-server）。
// *codex.Agent.FetchThreadList 满足该接口。
type codexThreadLister interface {
	FetchThreadList(ctx context.Context, dir string) ([]core.AgentSessionInfo, error)
}

type codexThreadHeadLister interface {
	FetchThreadListHead(ctx context.Context, dir string, limit int) ([]core.AgentSessionInfo, error)
}

// codexCatalogWireCache 复用 generic catalogWireSnapshotCache（与 OpenCode 共享同一实例：
// scope-keyed，codexCatalogScopeKey 与 openCodeCatalogScopeKey 互斥，互不复用快照）。
func (h *Handlers) codexCatalogWireCache() *catalogWireSnapshotCache {
	return h.openCodeCatalogWireCache()
}

// codexCatalogScopeKey 派生 Codex scope 缓存键：(backendID, dir)。dir 空 = 全局 catalog；
// 非空 = workspace 精确过滤。无 rootsOnly 维度（OpenCode 有），故两维：backend + dir。
func codexCatalogScopeKey(backendID, dir string) catalogWireScope {
	return newCatalogWireScope(backendID, dir, false)
}

// buildCodexEnrichedSessions 执行 §5.1 step 2-3 的富 wire 管线：FetchThreadList（app-server
// thread/list；dir 空=全局 / 非空=cwd 精确过滤；内部已用上游 cursor 有界取齐；recency_at
// desc）→ sessionsToWire → enrichSessionStatesForList → overlayPinnedState。
// Phase 7 §445 收敛（与 OpenCode Phase 4 / handlers_opencode.go 同形）：
// **不再 sortSessionsByUpdatedAt 覆盖**——thread/list 的 recency_at desc 是权威上游序，本地
// updatedAt 重排会改写它（"Mac native 显示什么，iOS 就显示什么"）。builder 仅由 declared
// codexHandleListSessions 调用；undeclared clients do not have a list data path.
//
// 失败必须显式返回 error（§5.1 step 6：删除 catalog 失败时静默回退 JSONL 的路径）。
func (h *Handlers) buildCodexEnrichedSessions(ctx context.Context, backendID, dir string) ([]map[string]interface{}, error) {
	mapped, agent, err := h.codexVisibleMembership(ctx, backendID, dir)
	if err != nil {
		return nil, err
	}
	mapped = h.enrichSessionStatesForList(mapped, agent, h.getRunningMap(ctx, agent))
	h.overlayPinnedState(mapped, agentBackendID(agent))
	return mapped, nil
}

// codexHandleListSessions 是 Codex list_sessions 的 declared v2 catalog 主线路径。调用方仅在
// hello 已协商 catalog_cursor_epoch_v2 时路由到此；undeclared 连接在 dispatch gate 返回
// protocol.capability_required。
//
// Scope：
//   - iOS Codex 是 root-only 全局 catalog（不带 directory）→ dir="" → 与 Mac Codex 多项目
//     侧栏同源（thread/list 省略 cwd，内部 cursor 逐页取齐）。
//   - 若 wire 带 directory，则 cwd 精确过滤到该 workspace（§3.1）。
//   - 绝不使用 agent.workDir：workDir 会随 start/resume 漂移，会把全局列表压成单工程。
func (h *Handlers) codexHandleListSessions(conn Connection, msg WireMessage, agent core.Agent) {
	ctx, cancel := context.WithTimeout(h.ctx, catalogRequestTimeout)
	defer cancel()
	limit := h.effectiveSessionListLimit(extractPositiveInt(msg, "limit"))
	cursor := extractStringParam(msg, "cursor")
	if limit > 1000 {
		limit = 1000
	}
	// 全局默认；可选 directory 做 workspace 精确过滤。不读 agentWorkDir。
	dir := extractDir(msg)
	started := time.Now()

	// v2 快照路径（§4.1.1 / §5.1 step 2）。page-0：FetchOrReuse(builder) 取/建快照；
	// page-N：Peek（不重建）→ validateCursorV2 → 切片 / cursor_stale。
	scopeKey := codexCatalogScopeKey(msg.BackendID, dir)
	cache := h.codexCatalogWireCache()

	// 全局首页（dir="" + cursor=""）：先取完整快照，再 B2 公平切片（K=20 + N），
	// 避免 thread/list 全局 recency 被单项目吃光。directory-scoped 仍走 pageV2 深挖。
	//
	// workspace 存在性在 fair-home 出站时再滤一次：thread/list 快照可复用（贵），
	// 但磁盘删除必须立刻反映，不能被 10m wire TTL 冻住幽灵目录。
	if dir == "" && cursor == "" {
		snap, err := cache.FetchOrReuseContext(ctx, scopeKey, func() ([]map[string]interface{}, error) {
			return h.buildCodexEnrichedSessions(ctx, msg.BackendID, dir)
		})
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
			return
		}
		// builder 已滤过；出站再滤一次，覆盖「缓存期间目录被删 / roots 变更」的窗口。
		full := filterCodexCatalogSessions(snap.maps)
		result := packageFairHomePage(full, defaultSessionListPerDirectoryLimit, limit)
		if ws, ok := result["sessions"].([]map[string]interface{}); ok {
			slog.Info("codex list_sessions v2 (thread/list fair-home)",
				"directory", redactDirForLog(dir),
				"scope", "global-fair",
				"limit", limit,
				"per_dir_limit", defaultSessionListPerDirectoryLimit,
				"result_count", len(ws),
				"full_count", len(full),
				"next_cursor_present", result["hasMore"] == true,
				"catalog_alive_procs", len(h.catalogProcessRegistry().AlivePIDs()),
				"duration_ms", time.Since(started).Milliseconds(),
			)
		}
		conn.SendResult(msg.RequestID, result, nil)
		return
	}

	result, staleErr, err := cache.pageV2Context(ctx, scopeKey, cursor, limit, func() ([]map[string]interface{}, error) {
		return h.buildCodexEnrichedSessions(ctx, msg.BackendID, dir)
	})
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	if staleErr != nil {
		// Phase 7 §443 可观测性：cursor_stale 是 live/stale 协商结果（非错误），记录脱敏指标便于
		// 统计 stale 触发率。dir 脱敏（§444）。
		slog.Info("codex list_sessions cursor_stale",
			"directory", redactDirForLog(dir),
			"cursor_present", cursor != "",
			"duration_ms", time.Since(started).Milliseconds(),
		)
		conn.SendResult(msg.RequestID, nil, staleErr) // cursor_stale（Retryable）
		return
	}
	if ws, ok := result["sessions"].([]map[string]interface{}); ok {
		slog.Info("codex list_sessions v2 (thread/list)",
			"directory", redactDirForLog(dir),
			"scope", codexCatalogScopeLabel(dir),
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

// codexCatalogScopeLabel 把 dir 打成日志用 scope 标签：空 → "global"，非空 → basename。
func codexCatalogScopeLabel(dir string) string {
	if dir == "" {
		return "global"
	}
	return redactDirForLog(dir)
}
