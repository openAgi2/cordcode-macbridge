package codex

// catalog_thread_list.go 实现 catalog client 的 thread/list 调用 + 字段映射（设计
// §5.1 step 2-4，Phase 2 Stream A）。
//
// thread/list 请求参数冻结于 thread_list_contract_test.go::frozenThreadListRequestParams
// （cwd 精确过滤 / archived=false / source=interactive / sortKey=recency_at /
// sortDirection=desc）。limit + cursor 只用于 MacBridge 内部有界读取，不在冻结的核心
// scope 字段里（§4.1「统一分页边界」：上游 cursor 不越过 bridge 边界）。
//
// 字段映射以 thread_list_contract_test.go::frozenThreadFields 为准（实跑捕获原样），
// 不得凭记忆新增/重命名字段。标题优先 app-server `name`，name 为空时按 Codex 语义用
// `preview`（§5.1 step 4）；不再读 session_index.jsonl 覆盖标题。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// threadListParams 是 thread/list 的请求参数。Scope 字段（cwd/archived/source/sortKey/
// sortDirection）由 Phase 0 冻结；limit/cursor 是 MacBridge 内部有界读取控制，不进冻结集。
//
// Cwd 使用 omitempty：空串表示「全局 catalog」（与 Mac Codex UI 多项目列表一致），请求里
// 省略 cwd 字段。非空 cwd = 精确 workspace 过滤（§3.1）。
type threadListParams struct {
	Cwd           string `json:"cwd,omitempty"`
	Archived      bool   `json:"archived"`
	Source        string `json:"source"`
	SortKey       string `json:"sortKey"`
	SortDirection string `json:"sortDirection"`
	Limit         int    `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
}

// codexThreadListPageSize 是 MacBridge 内部 thread/list 单页大小。app-server 在省略 limit
// 时默认只回 ~25 条并给 nextCursor；不设 limit + 不跟 cursor 会把全局 catalog 截成一页
// （真机回归：iOS 只见 25 条，远少于 Mac Codex / 旧 disk-scan）。
const codexThreadListPageSize = 100

// codexThreadListHeadMax is the maximum lightweight recency head requested by catalog-change
// detection. It is a trigger only: the bridge still runs the bounded full native fetch before
// fencing or publishing, so this never becomes a second catalog truth.
const codexThreadListHeadMax = 25

// codexThreadListMaxItems 是一次有界全量读取的硬上限（§4.1.1：metadata-only 有数量上限）。
// 覆盖多项目全局 interactive catalog（本机实跑 ~500），同时防止异常膨胀。
const codexThreadListMaxItems = 1000

// frozenThreadListScopeParams 返回 Phase 0 冻结的 thread/list scope 参数。
// cwd 空 = 全局 catalog（省略 cwd 字段）；非空 = 精确 workspace 过滤。
// source="interactive" 覆盖 CLI/exec/non-interactive kind，与 Mac Codex UI 的可见集一致（§3.1）。
func frozenThreadListScopeParams(cwd string) threadListParams {
	return threadListParams{
		Cwd:           cwd,
		Archived:      false,
		Source:        "interactive",
		SortKey:       "recency_at",
		SortDirection: "desc",
	}
}

// codexThread 是 thread/list 单条 thread 的冻结字段（frozenThreadFields 原样）。catalog
// 只消费列表语义字段；turns 等 catalog 不需要的字段以 RawMessage 忽略，避免 schema 漂移
// 时反序列化失败。时间戳是 unix 秒（fixture 1784082325 = 2026-07，秒级）。
type codexThread struct {
	ID             string             `json:"id"`
	SessionID      string             `json:"sessionId"`
	Name           string             `json:"name"`
	Preview        string             `json:"preview"`
	Cwd            string             `json:"cwd"`
	Path           string             `json:"path"`
	Source         string             `json:"source"`
	ModelProvider  string             `json:"modelProvider"`
	ParentThreadID string             `json:"parentThreadId"`
	ForkedFromID   string             `json:"forkedFromId"`
	CreatedAt      int64              `json:"createdAt"`
	UpdatedAt      int64              `json:"updatedAt"`
	RecencyAt      int64              `json:"recencyAt"`
	Status         codexThreadStatus  `json:"status"`
	GitInfo        codexThreadGitInfo `json:"gitInfo"`
	Turns          json.RawMessage    `json:"turns"`
}

type codexThreadStatus struct {
	Type string `json:"type"`
}

type codexThreadGitInfo struct {
	Sha       string `json:"sha"`
	Branch    string `json:"branch"`
	OriginURL string `json:"originUrl"`
}

// threadListResult 是 thread/list 响应的 result 对象（request 方法把 rpc result 反序列化进它）。
type threadListResult struct {
	Data            []codexThread `json:"data"`
	NextCursor      string        `json:"nextCursor"`
	BackwardsCursor string        `json:"backwardsCursor"`
}

// listThreads 调用 thread/list 并返回有序 thread 集合 + 上游 cursor（仅 MacBridge 内部
// 有界读取用，不越过 bridge 边界）。ctx 控制本次请求寿命；底层连接由 catalogClient 持有。
func (c *catalogClient) listThreads(ctx context.Context, params threadListParams) (threadListResult, error) {
	if err := ctx.Err(); err != nil {
		return threadListResult{}, err
	}
	// 把 caller ctx 的超时/取消叠加到 request：request 用 c.ctx（连接寿命），这里用一个
	// 合成 deadline 兜底，避免单次 thread/list 挂满 catalogRequestTimeout。
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, catalogRequestTimeout)
		defer cancel()
	}
	// 连接已死时快速失败，避免 requestWithTimeout 干等超时。
	if !c.alive.Load() {
		return threadListResult{}, fmt.Errorf("codex catalog client not alive")
	}

	var result threadListResult
	if err := c.requestWithTimeoutCtx(ctx, "thread/list", params, &result); err != nil {
		return threadListResult{}, err
	}
	return result, nil
}

// requestWithTimeoutCtx 是 request 的 ctx-aware 变体：既 honor caller ctx（中途取消），
// 又保留 catalogRequestTimeout 上限。复用 appServerSession 的 pending 相关性骨架。
func (c *catalogClient) requestWithTimeoutCtx(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan rpcResponseEnvelope, 1)

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[int64]chan rpcResponseEnvelope)
	}
	c.pending[id] = ch
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeJSON(payload); err != nil {
		return err
	}

	timer := time.NewTimer(catalogRequestTimeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("%s", strings.TrimSpace(resp.Error.Message))
		}
		if out != nil {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("%s timed out", method)
	}
}

// codexThreadToAgentSessionInfo 把 thread/list 单条 thread 映射成 core.AgentSessionInfo
// （§5.1 step 3）。标题优先 name，name 空时用 preview（§5.1 step 4：按 Codex 响应语义用
// preview，不读 session_index.jsonl）。MessageCount 不在 catalog 字段内，置 0（不猜）。
//
// ModifiedAt 优先 recencyAt（thread/list 排序键、Mac Codex 侧栏同源），回退 updatedAt。
// 原因：state_5.sqlite 里大量 session 的 updated_at 被批量写脏成同一时间戳（owner 真机
// 「1天前」成片错误），而 recency_at 才是最近活动时间（2026-08-11）。
func codexThreadToAgentSessionInfo(th codexThread) core.AgentSessionInfo {
	title := th.Name
	if strings.TrimSpace(title) == "" {
		title = th.Preview
	}
	return core.AgentSessionInfo{
		ID:         th.ID,
		Summary:    title,
		Directory:  th.Cwd,
		ModifiedAt: codexThreadModifiedAt(th),
		ProviderID: th.ModelProvider,
		GitBranch:  th.GitInfo.Branch,
	}
}

// codexThreadModifiedAt 列表展示/排序用时间：recencyAt > updatedAt > zero。
func codexThreadModifiedAt(th codexThread) time.Time {
	if th.RecencyAt > 0 {
		return codexThreadUnixTime(th.RecencyAt)
	}
	return codexThreadUnixTime(th.UpdatedAt)
}

// codexThreadUnixTime 把 thread/list 的 unix 秒时间戳转 time.Time。0/负值返回零值。
func codexThreadUnixTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// ── Agent 单例 catalog client 管理 ───────────────────────────────────────────────

// catalogClientInstance 返回 Agent 持有的单例 catalog client，已死则重建。线程安全。
// registrar（bridge ProcessRegistry）由 go-bridge 经 SetCatalogSubprocessRegistrar 注入；
// nil 时 stdio 子进程不注册到 bridge registry（仅靠 client 自身 Close 的进程组 kill 回收，
// 单测路径）。ws 传输无子进程，registrar 不参与。
func (a *Agent) catalogClientInstance(ctx context.Context) (*catalogClient, error) {
	a.catalogClientMu.Lock()
	defer a.catalogClientMu.Unlock()

	if a.catalogClient != nil && a.catalogClient.Alive() {
		return a.catalogClient, nil
	}
	if a.catalogClient != nil {
		_ = a.catalogClient.Close()
		a.catalogClient = nil
	}

	a.mu.RLock()
	cfg := catalogClientConfig{
		appServerURL:    a.appServerURL,
		appServerURLSet: a.appServerURLSet,
		workDir:         a.workDir,
		codexHome:       a.codexHome,
		cliBin:          a.cliBin,
		extraEnv:        append([]string(nil), a.sessionEnv...),
		registrar:       a.catalogRegistrar,
	}
	a.mu.RUnlock()

	// provider env（auth.json 等）由 providerEnvLocked 提供；catalog stdio 子进程启动时
	// 需要它以访问 app-server（与 StartSession 同源）。
	cfg.extraEnv = append(cfg.extraEnv, a.providerEnvLocked()...)

	// The caller context bounds this construction and its initialize request, but must not own the
	// singleton process/WebSocket after FetchThreadList returns.
	cli, err := newCatalogClientWithRequestContext(context.WithoutCancel(ctx), ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.catalogClient = cli
	return cli, nil
}

// SetCatalogSubprocessRegistrar 由 go-bridge 在构造 Agent 后注入 bridge ProcessRegistry，
// 使 stdio catalog 子进程注册到 bridge shutdown 回收链（§4.3 / §11）。ws 传输无子进程，
// registrar 不参与。
func (a *Agent) SetCatalogSubprocessRegistrar(r CatalogSubprocessRegistrar) {
	a.mu.Lock()
	a.catalogRegistrar = r
	a.mu.Unlock()
}

// FetchThreadList 是 go-bridge catalog 适配器的入口：取单例 catalog client，用冻结
// scope 参数调 thread/list，并在 MacBridge 内部用上游 cursor 逐页取齐（§4.1.1 有界全量
// 读取），映射成 core.AgentSessionInfo。
//
// dir 语义：
//   - 空串 → 全局 catalog（省略 thread/list.cwd；与 Mac Codex 多项目侧栏同源）
//   - 非空 → cwd 精确过滤到该 workspace
//
// 故意不回退到 Agent.workDir：workDir 会随最近一次 start/resume session 漂移，若拿它当
// catalog scope，iOS 全局列表会被压成「当前工程第一页 ~25 条」，丢失其它项目。
//
// 失败必须显式返回 error（§5.1 step 6：删除 catalog 失败时静默回退 JSONL 的路径）。
// 导出以供 go-bridge 经 codexThreadLister 接口（结构化满足）调用。
func (a *Agent) FetchThreadList(ctx context.Context, dir string) ([]core.AgentSessionInfo, error) {
	cli, err := a.catalogClientInstance(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]core.AgentSessionInfo, 0, codexThreadListPageSize)
	seen := make(map[string]struct{}, codexThreadListPageSize)
	cursor := ""
	pages := 0
	for {
		if len(out) >= codexThreadListMaxItems {
			break
		}
		params := frozenThreadListScopeParams(dir)
		params.Limit = codexThreadListPageSize
		if cursor != "" {
			params.Cursor = cursor
		}
		result, listErr := cli.listThreads(ctx, params)
		if listErr != nil {
			return nil, listErr
		}
		pages++
		for _, th := range result.Data {
			id := strings.TrimSpace(th.ID)
			if id == "" {
				id = strings.TrimSpace(th.SessionID)
			}
			if id != "" {
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
			}
			out = append(out, codexThreadToAgentSessionInfo(th))
			if len(out) >= codexThreadListMaxItems {
				break
			}
		}
		if result.NextCursor == "" || len(result.Data) == 0 {
			break
		}
		// 防御：上游若回同一 cursor 会无限循环。
		if result.NextCursor == cursor {
			slog.Warn("codex catalog: thread/list repeated nextCursor; stopping page walk",
				"cwd", dir, "pages", pages, "count", len(out))
			break
		}
		cursor = result.NextCursor
		// 硬页数上限：maxItems/pageSize + 余量，防止异常 cursor 链。
		if pages >= (codexThreadListMaxItems/codexThreadListPageSize)+2 {
			slog.Warn("codex catalog: thread/list page walk hit page cap",
				"cwd", dir, "pages", pages, "count", len(out))
			break
		}
	}
	slog.Info("codex catalog: thread/list bounded fetch",
		"cwd", dir, "count", len(out), "pages", pages, "capped", len(out) >= codexThreadListMaxItems)
	return out, nil
}

// FetchThreadListHead returns one small native recency-ordered page without following the
// upstream cursor. MacBridge uses it only as a cheap change hint; membership, cache fencing and
// sessions_changed remain owned by FetchThreadList's full native snapshot.
func (a *Agent) FetchThreadListHead(ctx context.Context, dir string, limit int) ([]core.AgentSessionInfo, error) {
	cli, err := a.catalogClientInstance(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > codexThreadListHeadMax {
		limit = codexThreadListHeadMax
	}
	params := frozenThreadListScopeParams(dir)
	params.Limit = limit
	result, err := cli.listThreads(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]core.AgentSessionInfo, 0, len(result.Data))
	for _, thread := range result.Data {
		out = append(out, codexThreadToAgentSessionInfo(thread))
	}
	slog.Debug("codex catalog: thread/list head probe", "cwd", dir, "count", len(out), "limit", limit)
	return out, nil
}
