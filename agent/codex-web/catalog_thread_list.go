package codexweb

// catalog_thread_list.go —— P0-4：codex-web 满足 go-bridge 的 codexThreadLister /
// codexThreadHeadLister 富 catalog seam（FetchThreadList / FetchThreadListHead），
// 使 codex-web 的 list_sessions、discovery fingerprint 与 3s hint 共用同一个
// official thread/list 数据源。此前 go-bridge 按 agent.Name()=="codex" 分派，
// codex-web 只能走 agent.ListSessions（ListAllThreads 默认参数），且 hint 只对
// "codex" 生效——Mac 新建 session 时 codex-web 目录的 sessions_changed 触发面与
// list_sessions 数据源分离，iOS 列表无法自愈（问题 1 的 Mac 侧根因）。
//
// 语义纪律（与 sessions.go 一致）：页序=官方序（recency desc 官方默认，不本地
// 重排）；dir 空=全局 catalog（省略 cwd），非空=官方 cwd exact-match 过滤；聚合
// 用服务端默认页大小，不以小 limit 深翻页（官方秒级 cursor 跳过兄弟条目）。

import (
	"context"
	"log/slog"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	// catalogThreadListMaxItems 是有界全量读取的硬上限（防异常膨胀）。
	catalogThreadListMaxItems = 1000
	// catalogThreadListHeadMax 是轻量 recency head 探测的最大行数（仅触发信号，
	// 不构成第二个 catalog 真相——完整 fingerprint 仍由 FetchThreadList 拥有）。
	catalogThreadListHeadMax = 25
)

// catalogThreadListParams 组装 thread/list 请求：dir 非空才带官方 cwd 过滤。
// SortKey/archived 等其余参数保持官方默认（不主动使用未采样组合，§11.2）。
func catalogThreadListParams(dir string) ListThreadsParams {
	params := ListThreadsParams{}
	if dir != "" {
		params.SetCWDFilter([]string{dir})
	}
	return params
}

// FetchThreadList 实现 codexThreadLister：与 codex Agent 同形的 thread/list 富
// catalog（go-bridge list_sessions / discovery 共用）。dir 空=全局；非空=cwd
// 精确过滤。返回按 official 页序的可见成员。
func (a *Agent) FetchThreadList(ctx context.Context, dir string) ([]core.AgentSessionInfo, error) {
	var out []core.AgentSessionInfo
	err := a.withClient(ctx, func(cl *Client) error {
		threads, rpcErr, err := ListAllThreads(ctx, cl, catalogThreadListParams(dir))
		if err != nil {
			return err
		}
		if rpcErr != nil {
			return rpcErr
		}
		out = make([]core.AgentSessionInfo, 0, len(threads))
		seen := make(map[string]struct{}, len(threads))
		for i := range threads {
			th := &threads[i]
			id := th.ID
			if id != "" {
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
			}
			out = append(out, threadToAgentSessionInfo(th))
			if len(out) >= catalogThreadListMaxItems {
				slog.Warn("codexweb: thread/list bounded fetch hit item cap",
					"dirFiltered", dir != "", "count", len(out))
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slog.Info("codexweb: thread/list bounded fetch", "dirFiltered", dir != "", "count", len(out))
	return out, nil
}

// FetchThreadListHead 实现 codexThreadHeadLister：单页 recency head 探测（不跟
// cursor），仅供 MacBridge 3s hint 作廉价变化信号。
func (a *Agent) FetchThreadListHead(ctx context.Context, dir string, limit int) ([]core.AgentSessionInfo, error) {
	if limit <= 0 || limit > catalogThreadListHeadMax {
		limit = catalogThreadListHeadMax
	}
	params := catalogThreadListParams(dir)
	l := uint32(limit)
	params.Limit = &l

	var out []core.AgentSessionInfo
	err := a.withClient(ctx, func(cl *Client) error {
		page, rpcErr, err := ListThreads(ctx, cl, params)
		if err != nil {
			return err
		}
		if rpcErr != nil {
			return rpcErr
		}
		out = make([]core.AgentSessionInfo, 0, len(page.Data))
		for i := range page.Data {
			out = append(out, threadToAgentSessionInfo(&page.Data[i]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slog.Debug("codexweb: thread/list head probe", "dirFiltered", dir != "", "count", len(out), "limit", limit)
	return out, nil
}
