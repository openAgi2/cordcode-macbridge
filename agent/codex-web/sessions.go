package codexweb

// sessions.go —— thread/list catalog（设计 §7 list_sessions/archive/rename/delete、§5.2 catalog）。
//
// 语义纪律：
//   - 官方排序、cursor、archive/source 语义原样保留；不做本地重排/本地过滤——
//     directory 过滤走官方 thread/list.cwd（ThreadListCwdFilter：exact-match 语义），
//     不在 CordCode 侧按路径前缀二次筛（dumps/catalog + stable ThreadListParams）；
//   - 请求字段以 Phase 0 样本为准：limit/cursor/archived 是已冻结组合；
//     searchTerm/sortKey/sortDirection/sourceKinds/modelProviders 仅在 schema 存在，
//     未采样的组合不主动使用（§11.2：不能由“可发送”推导“已验证”）；
//   - archive/unarchive/delete/name/set 响应为 {}；rename 不做本地乐观真相——
//     以 thread/read 重读确认（§7 rename 行）；
//   - -32600 "already has an active writer" 翻译为 OwnershipConflictError（§10.2），
//     携带 thread id/方法/transport 来源/官方原文/建议；绝不自动 kill 其他官方进程。
//
// 官方 wire 事实（0.149.0-alpha.4，dumps/catalog + stable bundle）：
//   - thread/list 响应 {data:[Thread], nextCursor, backwardsCursor}；cursor 为不透明
//     字符串（实测为 RFC3339 时间戳），nextCursor 空 = 没有更多页；
//   - 列表默认 desc（newest first，官方默认，不改写）；
//   - archived 列表条目 status 为 notLoaded、path 位于 archived_sessions/，
//     wire 无 archivedAt 时间戳——不由 CordCode 编造归档时间。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ThreadStatus 是官方 ThreadStatus oneOf（stable bundle）：notLoaded/idle/systemError/active。
// active 携带 activeFlags。Type 以官方字符串原样保留。
type ThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

const (
	ThreadStatusNotLoaded   = "notLoaded"
	ThreadStatusIdle        = "idle"
	ThreadStatusSystemError = "systemError"
	ThreadStatusActive      = "active"
)

// GitInfo 是官方 Thread.gitInfo（branch/originUrl/sha 均可空）。
type GitInfo struct {
	Branch    *string `json:"branch"`
	OriginURL *string `json:"originUrl"`
	SHA       *string `json:"sha"`
}

// ThreadInfo 是官方 Thread wire 形状（dumps/catalog 冻结；schema Thread 未列出而
// 实测存在的 extra/historyMode/canAcceptDirectInput 按样本保留，§3.0 样本优先）。
type ThreadInfo struct {
	ID               string          `json:"id"`
	Extra            json.RawMessage `json:"extra"`
	SessionID        string          `json:"sessionId"`
	ForkedFromID     *string         `json:"forkedFromId"`
	ParentThreadID   *string         `json:"parentThreadId"`
	Preview          string          `json:"preview"`
	Ephemeral        bool            `json:"ephemeral"`
	Section          *string         `json:"section"`
	SectionEnteredAt *int64          `json:"sectionEnteredAt"`
	ProjectID        *string         `json:"projectId"`
	HistoryMode      string          `json:"historyMode"`
	ModelProvider    string          `json:"modelProvider"`
	CreatedAt        int64           `json:"createdAt"`
	UpdatedAt        int64           `json:"updatedAt"`
	RecencyAt        int64           `json:"recencyAt"`
	Status           ThreadStatus    `json:"status"`
	Path             string          `json:"path"`
	Cwd              string          `json:"cwd"`
	CliVersion       string          `json:"cliVersion"`
	Source           string          `json:"source"`
	CanAcceptDirect  *bool           `json:"canAcceptDirectInput"`
	ThreadSource     *string         `json:"threadSource"`
	AgentNickname    *string         `json:"agentNickname"`
	AgentRole        *string         `json:"agentRole"`
	GitInfo          *GitInfo        `json:"gitInfo"`
	Name             *string         `json:"name"`
	Turns            []TurnInfo      `json:"turns"`
}

// Title 返回官方 thread 标题：显式 name 优先，否则 preview。两者都没有时为空——
// 不从 cwd/path 推测标题。
func (t *ThreadInfo) Title() string {
	if t.Name != nil && *t.Name != "" {
		return *t.Name
	}
	return t.Preview
}

// ListThreadsParams 是 thread/list 请求参数。零值字段按官方语义省略（omitempty），
// 保证冻结的请求形状 {limit} / {limit,cursor} / {limit,archived:true} 逐字段一致。
type ListThreadsParams struct {
	Limit          *uint32  `json:"limit,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
	Archived       *bool    `json:"archived,omitempty"`
	CWD            []string `json:"cwd,omitempty"`
	SearchTerm     string   `json:"searchTerm,omitempty"`
	ModelProviders []string `json:"modelProviders,omitempty"`
	SourceKinds    []string `json:"sourceKinds,omitempty"`
	SortKey        string   `json:"sortKey,omitempty"`
	SortDirection  string   `json:"sortDirection,omitempty"`
}

// SetCWDFilter 设置官方 ThreadListCwdFilter（string 或 [string]）。nil/空 = 不过滤；
// 语义是 exact match（官方描述），不是前缀匹配。
func (p *ListThreadsParams) SetCWDFilter(dirs []string) {
	p.CWD = dirs
}

// ThreadListPage 是 thread/list 响应。nextCursor/backwardsCursor 为 null 时解码为零值。
type ThreadListPage struct {
	Data            []ThreadInfo `json:"data"`
	NextCursor      string       `json:"nextCursor"`
	BackwardsCursor string       `json:"backwardsCursor"`
}

// maxListPages 是 ListAllThreads 的翻页上限（防 cursor 环/异常服务端）。
const maxListPages = 500

// ListThreads 发送一次 thread/list。
func ListThreads(ctx context.Context, cl *Client, params ListThreadsParams) (*ThreadListPage, *RPCError, error) {
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/list", params)
	if err != nil {
		return nil, nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr, nil
	}
	var page ThreadListPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, nil, fmt.Errorf("codexweb: thread/list response decode: %w", err)
	}
	if page.Data == nil {
		page.Data = []ThreadInfo{}
	}
	return &page, nil, nil
}

// ListAllThreads 沿官方 nextCursor 翻页聚合一侧列表（保持官方页序，不本地重排）。
// archived 语义由 params（含 Archived 指针）原样携带：true 只列已归档，false/nil 只列未归档。
//
// 官方分页边界（§22-6，rollout list.rs should_skip）：默认 created_at 秒粒度 cursor 会
// 跳过与 cursor 同秒创建的兄弟条目。聚合 catalog 应使用服务端默认页大小（单页覆盖），
// 不要以小 limit 深翻页追求完整性——那会触发官方同秒跳过。
func ListAllThreads(ctx context.Context, cl *Client, params ListThreadsParams) ([]ThreadInfo, *RPCError, error) {
	var all []ThreadInfo
	seenCursor := map[string]bool{}
	for i := 0; ; i++ {
		if i >= maxListPages {
			return nil, nil, fmt.Errorf("codexweb: thread/list exceeded %d pages (cursor not advancing)", maxListPages)
		}
		page, rpcErr, err := ListThreads(ctx, cl, params)
		if err != nil || rpcErr != nil {
			return nil, rpcErr, err
		}
		all = append(all, page.Data...)
		if page.NextCursor == "" {
			return all, nil, nil
		}
		if seenCursor[page.NextCursor] {
			return nil, nil, fmt.Errorf("codexweb: thread/list cursor repeated: %s", page.NextCursor)
		}
		seenCursor[page.NextCursor] = true
		params.Cursor = page.NextCursor
	}
}

// ArchiveThread 归档 thread（thread/archive，响应 {}）。
func ArchiveThread(ctx context.Context, cl *Client, threadID string) *RPCError {
	return voidThreadOp(ctx, cl, "thread/archive", threadID)
}

// UnarchiveThread 取消归档（thread/unarchive，响应 {}）。
func UnarchiveThread(ctx context.Context, cl *Client, threadID string) *RPCError {
	return voidThreadOp(ctx, cl, "thread/unarchive", threadID)
}

// DeleteThread 删除 thread（thread/delete，响应 {}）。破坏性动作由 iOS 侧既有
// 确认流程保护；本层不做二次确认。
func DeleteThread(ctx context.Context, cl *Client, threadID string) *RPCError {
	return voidThreadOp(ctx, cl, "thread/delete", threadID)
}

func voidThreadOp(ctx context.Context, cl *Client, method, threadID string) *RPCError {
	raw, rpcErr, err := cl.RequestContext(ctx, method, map[string]string{"threadId": threadID})
	switch {
	case err != nil:
		return &RPCError{Code: -1, Message: err.Error()}
	case rpcErr != nil:
		return rpcErr
	case raw == nil:
		return &RPCError{Code: -1, Message: "codexweb: empty response"}
	}
	// 官方响应为 {}；仅校验是 JSON 对象，不猜测字段。
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return &RPCError{Code: -1, Message: fmt.Sprintf("codexweb: %s response decode: %v", method, err)}
	}
	return nil
}

// SetThreadName 重命名并重读确认（不做本地乐观真相）。返回服务端观测到的 name；
// nil 表示重读时官方 name 为空（官方允许置空/重置）。
func SetThreadName(ctx context.Context, cl *Client, threadID, name string) (*string, *RPCError, error) {
	th, rpcErr, err := setThreadNameAndRead(ctx, cl, threadID, name)
	if err != nil || rpcErr != nil {
		return nil, rpcErr, err
	}
	return th.Name, nil, nil
}

// setThreadNameAndRead returns the complete authoritative thread metadata so
// the Agent capability can answer rename_session without reconstructing a row
// from the requested title. SetThreadName keeps the narrower fixture-facing
// API above for callers that only need the confirmed official name.
func setThreadNameAndRead(ctx context.Context, cl *Client, threadID, name string) (*ThreadInfo, *RPCError, error) {
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/name/set", map[string]string{
		"threadId": threadID,
		"name":     name,
	})
	switch {
	case err != nil:
		return nil, nil, err
	case rpcErr != nil:
		return nil, rpcErr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("codexweb: thread/name/set response decode: %w", err)
	}
	// 确认路径：thread/read 重读官方持久化结果（thread/name/updated 通知真实存在且
	// 已入 fixture，但通知通道由事件泵单读者消费；catalog 不抢读，见 rpc.go 单 reader 纪律）。
	th, rpcErr, err := ReadThread(ctx, cl, threadID, false)
	if err != nil || rpcErr != nil {
		return nil, rpcErr, err
	}
	return th, nil, nil
}

// ---- ownership 冲突翻译（§10.2） ----

const (
	ownershipErrorCode = -32600
	ownershipMarker    = "already has an active writer"
)

// OwnershipConflictError 表示目标 thread 正由另一个 Codex app-server writer 持有。
// 携带 §10.2 要求的全部事实；建议回到当前持有者完成/退出，而非 MacBridge 抢进程。
type OwnershipConflictError struct {
	ThreadID        string
	Method          string
	TransportSource ServiceSource
	OfficialCode    int64
	OfficialMessage string
}

func (e *OwnershipConflictError) Error() string {
	return fmt.Sprintf(
		"该会话正由另一个 Codex app-server 持有：thread %s（方法 %s，transport 来源 %s）。官方错误 %d: %s。请在当前持有该会话的 Codex 客户端完成或退出后重试；CordCode 不会终止其他官方进程。",
		e.ThreadID, e.Method, e.TransportSource, e.OfficialCode, e.OfficialMessage,
	)
}

// IsOwnershipConflict 判定官方错误是否为 writer 冲突（-32600 + 官方原文标记；
// dumps/ownership：resume/archive/delete 均为该形状）。
func IsOwnershipConflict(rpcErr *RPCError) bool {
	return rpcErr != nil && rpcErr.Code == ownershipErrorCode && strings.Contains(rpcErr.Message, ownershipMarker)
}

// TranslateOwnershipConflict 把官方 -32600 writer 冲突包装为 OwnershipConflictError；
// 非冲突错误返回 nil（调用方保留官方 *RPCError 原样展示）。
func TranslateOwnershipConflict(method string, source ServiceSource, threadID string, rpcErr *RPCError) *OwnershipConflictError {
	if !IsOwnershipConflict(rpcErr) {
		return nil
	}
	return &OwnershipConflictError{
		ThreadID:        threadID,
		Method:          method,
		TransportSource: source,
		OfficialCode:    rpcErr.Code,
		OfficialMessage: rpcErr.Message,
	}
}

// errOwnershipOrRPC 统一写操作的错误出口：冲突 → OwnershipConflictError；
// 其他官方错误 → 原样 *RPCError；传输错误 → error。
func errOwnershipOrRPC(method string, source ServiceSource, threadID string, rpcErr *RPCError, err error) error {
	if err != nil {
		return err
	}
	if oc := TranslateOwnershipConflict(method, source, threadID, rpcErr); oc != nil {
		return oc
	}
	if rpcErr != nil {
		return rpcErr
	}
	return nil
}

// asOwnership 供 errors.As 判定使用。
func asOwnership(err error) (*OwnershipConflictError, bool) {
	var oc *OwnershipConflictError
	if errors.As(err, &oc) {
		return oc, true
	}
	return nil, false
}
