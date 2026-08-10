package gobridge

// handlers_codex_catalog.go 是 Codex list_sessions 的 catalog 主线路径（设计 §5.1 step 1-6 /
// §4.1.1 / §4.3，Phase 2 Stream A chunk 2A-2/3）。它与 OpenCode 的 handlers_opencode.go
// 1C 接线刻意同构：
//
//   - 复用 generic catalogWireSnapshotCache（与 OpenCode 共享：scope-keyed，codex/opencode
//     scope key 互斥，互不复用快照）；
//   - builder（buildCodexEnrichedSessions）跑 thread/list 富 wire 管线（FetchThreadList →
//     sessionsToWire → enrich/overlay → sortSessionsByUpdatedAt）；
//   - capability 门控（catalog_cursor_epoch_v2）：declared → v2 epoch-bearing cursor +
//     cursor_stale；undeclared → v1 paginateSessionList（既有 disk-scan 经 agent.ListSessions
//     的等价 wire 切片）byte-for-byte 不变。
//
// §10 发布顺序：capability 上线前（iOS Phase 6 才声明），MacBridge 不得对任何连接发射 v2 cursor。
// 当前 declared 恒为 false → codexHandleListSessions 走 v1 → 零行为变化。

import (
	"context"
	"fmt"
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

// agentWorkDir 返回 agent 的 workDir（经 core.WorkDirSwitcher 可选接口；codex.Agent 实现）。
// 无该接口或空 workDir 时返回 ""（builder 会回退到 codex.Agent 内部默认 workDir）。
func agentWorkDir(agent core.Agent) string {
	if ws, ok := agent.(core.WorkDirSwitcher); ok {
		return ws.GetWorkDir()
	}
	return ""
}

// codexThreadLister 是 go-bridge 与 *codex.Agent 之间的 catalog seam（结构化满足）。
// 定义为接口使 builder 可在注入 fake lister 时单测（不启动真实 codex app-server）。
// *codex.Agent.FetchThreadList 满足该接口。
type codexThreadLister interface {
	FetchThreadList(ctx context.Context, dir string) ([]core.AgentSessionInfo, error)
}

// codexCatalogWireCache 复用 generic catalogWireSnapshotCache（与 OpenCode 共享同一实例：
// scope-keyed，codexCatalogScopeKey 与 openCodeCatalogScopeKey 互斥，互不复用快照）。
func (h *Handlers) codexCatalogWireCache() *catalogWireSnapshotCache {
	return h.openCodeCatalogWireCache()
}

// codexCatalogScopeKey 派生 Codex scope 缓存键：(backendID, dir)。Codex thread/list 是 cwd
// 精确过滤（§3.1），无 rootsOnly 维度（OpenCode 有），故两维：backend + workspace dir。
func codexCatalogScopeKey(backendID, dir string) string {
	return backendID + "\x00" + dir
}

// buildCodexEnrichedSessions 执行 §5.1 step 2-3 的富 wire 管线：FetchThreadList（app-server
// thread/list，cwd=dir 精确过滤，recency_at desc）→ sessionsToWire → enrichSessionStatesForList
// → overlayPinnedState → sortSessionsByUpdatedAt。v1（paginateSessionList）与 v2
// （catalogWireSnapshotCache）两条路径共用此 builder（DRY）。
//
// 失败必须显式返回 error（§5.1 step 6：删除 catalog 失败时静默回退 JSONL 的路径）。
func (h *Handlers) buildCodexEnrichedSessions(backendID, dir string) ([]map[string]interface{}, error) {
	agent, ok := h.getAgent(backendID)
	if !ok {
		return nil, fmt.Errorf("codex agent not registered for backend %q", backendID)
	}
	lister, ok := agent.(codexThreadLister)
	if !ok {
		return nil, fmt.Errorf("codex agent %q does not support thread/list catalog", backendID)
	}
	sessions, err := lister.FetchThreadList(context.Background(), dir)
	if err != nil {
		return nil, err
	}
	mapped := sessionsToWire(sessions)
	mapped = h.enrichSessionStatesForList(mapped, agent, h.getRunningMap(context.Background(), agent))
	h.overlayPinnedState(mapped, "codex")
	sortSessionsByUpdatedAt(mapped)
	return mapped, nil
}

// codexHandleListSessions 是 Codex list_sessions 的 v2 catalog 主线路径（DECLARED：
// hello 声明 catalog_cursor_epoch_v2）。调用方（handleListSessions）仅在 capability 已声明时
// 路由到此；undeclared 连接走既有 generic disk-scan（agent.ListSessions）路径 byte-for-byte
// 不变（§10 发布顺序：capability 上线前 MacBridge 不得对任何连接发射 v2 cursor，且数据源
// 从 disk-scan 切到 thread/list 同样只对 declared 连接生效——iOS Phase 6 才声明 → 当前零行为变化）。
//
// cwd 取 agent 的 workDir（Codex thread/list 精确过滤到该 workspace，§3.1）。
func (h *Handlers) codexHandleListSessions(conn Connection, msg WireMessage, agent core.Agent) {
	limit := h.effectiveSessionListLimit(extractPositiveInt(msg, "limit"))
	cursor := extractStringParam(msg, "cursor")
	if limit > 1000 {
		limit = 1000
	}
	// cwd = agent workDir（Codex catalog 是 workspace-scoped；iOS 无需带 dir）。
	dir := agentWorkDir(agent)
	started := time.Now()

	// v2 快照路径（§4.1.1 / §5.1 step 2）。page-0：FetchOrReuse(builder) 取/建快照；
	// page-N：Peek（不重建）→ validateCursorV2 → 切片 / cursor_stale。
	scopeKey := codexCatalogScopeKey(msg.BackendID, dir)
	cache := h.codexCatalogWireCache()
	result, staleErr, err := cache.pageV2(scopeKey, cursor, limit, func() ([]map[string]interface{}, error) {
		return h.buildCodexEnrichedSessions(msg.BackendID, dir)
	})
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	if staleErr != nil {
		conn.SendResult(msg.RequestID, nil, staleErr) // cursor_stale（Retryable）
		return
	}
	if ws, ok := result["sessions"].([]map[string]interface{}); ok {
		slog.Info("codex list_sessions v2 (thread/list)",
			"directory", dir,
			"limit", limit,
			"cursor_present", cursor != "",
			"result_count", len(ws),
			"next_cursor_present", result["hasMore"] == true,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}
	conn.SendResult(msg.RequestID, result, nil)
}
