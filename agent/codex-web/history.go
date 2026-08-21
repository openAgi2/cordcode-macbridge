package codexweb

// history.go —— thread/read(includeTurns) → rich history → pathless hydrate（§7/§9）。
//
// 一期稳定基线 = thread/read{includeTurns:true}（只读，不 resume）；thread/turns/list
// 仅 experimental 门控后用于分页优化（§11.2）。Phase 0 样本：dumps/catalog。
//
// wire 事实（stable bundle Turn + dumps/catalog）：
//   - 响应 {thread:{..., turns:[Turn]}}；Turn{id, items, itemsView, status, error,
//     startedAt, completedAt, durationMs}；status ∈ completed/interrupted/failed/inProgress；
//     itemsView ∈ full/summary/notLoaded（notLoaded 时 items 为空，不代表 turn 无 item）；
//   - error 仅在 status=failed 时填充（TurnError.message 必有）；
//   - item 正文映射（userMessage/agentMessage/reasoning/commandExecution/...）在
//     本文件 ReadThread 之上由 p2-history 落地；此处只保留官方原始形状。

import (
	"context"
	"encoding/json"
	"fmt"
)

// TurnItemsView 官方枚举。
const (
	TurnItemsViewFull     = "full"
	TurnItemsViewSummary  = "summary"
	TurnItemsViewNotLoaded = "notLoaded"
)

// TurnStatus 官方枚举。
const (
	TurnStatusCompleted  = "completed"
	TurnStatusInterrupted = "interrupted"
	TurnStatusFailed     = "failed"
	TurnStatusInProgress = "inProgress"
)

// TurnErrorInfo 是官方 TurnError（message 必有；codexErrorInfo/additionalDetails 可选）。
type TurnErrorInfo struct {
	Message          string          `json:"message"`
	AdditionalDetails *string        `json:"additionalDetails"`
	CodexErrorInfo   json.RawMessage `json:"codexErrorInfo"`
}

// TurnInfo 是官方 Turn wire 形状。Items 保留原始 JSON（官方 variant 众多，
// 映射按 variant 白名单进行，见 item codec）。
type TurnInfo struct {
	ID         string          `json:"id"`
	Items      []json.RawMessage `json:"items"`
	ItemsView  string          `json:"itemsView"`
	Status     string          `json:"status"`
	Error      *TurnErrorInfo  `json:"error"`
	StartedAt  *int64          `json:"startedAt"`
	CompletedAt *int64         `json:"completedAt"`
	DurationMs *int64          `json:"durationMs"`
}

// ReadThread 发送 thread/read。includeTurns=true 时返回官方持久化 turns
// （rollout history 的官方只读视图）；false 只读 thread 元数据（rename 确认等）。
func ReadThread(ctx context.Context, cl *Client, threadID string, includeTurns bool) (*ThreadInfo, *RPCError, error) {
	params := map[string]any{"threadId": threadID}
	if includeTurns {
		params["includeTurns"] = true
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/read", params)
	if err != nil {
		return nil, nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr, nil
	}
	var resp struct {
		Thread *ThreadInfo `json:"thread"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("codexweb: thread/read response decode: %w", err)
	}
	if resp.Thread == nil || resp.Thread.ID == "" {
		return nil, nil, fmt.Errorf("codexweb: thread/read response missing thread identity")
	}
	return resp.Thread, nil, nil
}
