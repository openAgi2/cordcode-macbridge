package codexweb

// history.go —— thread/read(includeTurns) → rich history → pathless hydrate（§7/§9）。
//
// 一期稳定基线 = thread/read{includeTurns:true}（只读，不 resume）；thread/turns/list
// 仅 experimental 门控后用于分页优化（§11.2）。Phase 0 样本：dumps/catalog（历史视图）、
// dumps/interaction（command/file 变体的 item 帧，与历史视图同一 ThreadItem 形状）。
//
// 身份纪律（§9.1 live/cold 同一官方 identity）：
//   - core.TurnScopedHistoryTurn.TurnID = 官方 turn.id（不是 user item id——那是 opencode 家族约定，
//     Codex live 事件带官方 turn id，冷基线必须同 id 才能 merge）；
//   - item 身份 = 官方 item.id（userMessage/agentMessage/commandExecution/...）；
//   - 禁止编造合成 id；无 agentMessage 的 turn 用 turn 自身作为 assistant 边界载体，
//     但 Parts 为空时消费端不得伪造内容。
//
// 工具命名与旧 backend live 词汇对齐（输出回归对照 agent/codex appserver_session.go）：
// commandExecution→Bash、fileChange→Patch、mcpToolCall→MCP、webSearch→WebSearch、
// dynamicToolCall→官方 tool 名。这是冷/热一致性的要求，不是复用旧实现。
//
// variant 证据分级（§11.2）：
//   - 真实样本映射：userMessage/agentMessage/reasoning（dumps/catalog 历史视图）、
//     commandExecution/fileChange（dumps/interaction item 帧）；
//   - 仅 schema 映射（目标二进制生成 bundle）：mcpToolCall/dynamicToolCall/plan/
//     contextCompaction/webSearch/imageView/hookPrompt/subAgentActivity 等——结构化
//     映射按 schema 冻结字段，capability 广告仍保持关闭直到取得真实样本；
//   - 未知 type：跳过并保留官方 type 于诊断（不猜测字段，不崩溃）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TurnItemsView 官方枚举。
const (
	TurnItemsViewFull      = "full"
	TurnItemsViewSummary   = "summary"
	TurnItemsViewNotLoaded = "notLoaded"
)

// TurnStatus 官方枚举。
const (
	TurnStatusCompleted   = "completed"
	TurnStatusInterrupted = "interrupted"
	TurnStatusFailed      = "failed"
	TurnStatusInProgress  = "inProgress"
)

// TurnErrorInfo 是官方 TurnError（message 必有；codexErrorInfo/additionalDetails 可选）。
type TurnErrorInfo struct {
	Message           string          `json:"message"`
	AdditionalDetails *string         `json:"additionalDetails"`
	CodexErrorInfo    json.RawMessage `json:"codexErrorInfo"`
}

// TurnInfo 是官方 Turn wire 形状。Items 保留原始 JSON（官方 variant 众多，
// 映射按 variant 白名单进行，见 item codec）。
type TurnInfo struct {
	ID          string            `json:"id"`
	Items       []json.RawMessage `json:"items"`
	ItemsView   string            `json:"itemsView"`
	Status      string            `json:"status"`
	Error       *TurnErrorInfo    `json:"error"`
	StartedAt   *int64            `json:"startedAt"`
	CompletedAt *int64            `json:"completedAt"`
	DurationMs  *int64            `json:"durationMs"`
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

// ---- typed ThreadItem（stable bundle ThreadItem oneOf；字段以 schema+样本冻结） ----

// UserContentPart 是 userMessage.content 的元素：text（样本冻结）；
// image/localImage 等 schema variant 仅保留 type/url，不映射正文。
type UserContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
}

// FileUpdateChange 是官方 fileChange.changes[]（path/kind/diff 必有；
// kind 为 {type:"add"|"delete"|"update", move_path?}）。
type FileUpdateChange struct {
	Path     string          `json:"path"`
	Kind     json.RawMessage `json:"kind"`
	Diff     string          `json:"diff"`
	MovePath *string         `json:"move_path"`
}

// ChangeKind 返回官方 kind.type（add/delete/update）；无法解析时返回原串。
func (c FileUpdateChange) ChangeKind() string {
	var k struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(c.Kind, &k); err != nil || k.Type == "" {
		return string(c.Kind)
	}
	return k.Type
}

// ThreadItem 是历史视图 item 的 typed 载荷：只填该 type 官方定义的字段，
// Raw 保留原始 JSON 供诊断。未识别 type 只置 Type+Raw。
type ThreadItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Raw  json.RawMessage

	// userMessage
	Content []UserContentPart

	// agentMessage / plan
	Text string

	// reasoning（summary 与 content 不重复：优先 summary）
	Summary       []string
	ReasoningTail []string

	// commandExecution
	Command         string
	CommandCwd      string
	CommandStatus   string
	AggregatedOut   *string
	ExitCode        *int32
	ProcessID       *string
	CommandSource   string
	CommandDuration *int64

	// fileChange
	Changes     []FileUpdateChange
	PatchStatus string

	// mcpToolCall / dynamicToolCall
	Server     string
	Tool       string
	Arguments  json.RawMessage
	ToolStatus string
	Result     json.RawMessage
	ToolError  json.RawMessage

	// webSearch
	Query string
}

// decodeThreadItem 解析一个 item；未识别 type 不报错（返回 Type+Raw），由调用方决定
// 跳过或诊断——§7.1 未识别通知/item 不能导致崩溃，也不能猜字段。
func decodeThreadItem(raw json.RawMessage) ThreadItem {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	it := ThreadItem{Type: probe.Type, ID: probe.ID, Raw: raw}
	switch probe.Type {
	case "userMessage":
		var v struct {
			Content []UserContentPart `json:"content"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Content = v.Content
	case "agentMessage", "plan":
		var v struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Text = v.Text
	case "reasoning":
		var v struct {
			Summary []string `json:"summary"`
			Content []string `json:"content"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Summary, it.ReasoningTail = v.Summary, v.Content
	case "commandExecution":
		var v struct {
			Command          string  `json:"command"`
			Cwd              string  `json:"cwd"`
			Status           string  `json:"status"`
			AggregatedOutput *string `json:"aggregatedOutput"`
			ExitCode         *int32  `json:"exitCode"`
			ProcessID        *string `json:"processId"`
			Source           string  `json:"source"`
			DurationMs       *int64  `json:"durationMs"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Command, it.CommandCwd, it.CommandStatus = v.Command, v.Cwd, v.Status
		it.AggregatedOut, it.ExitCode, it.ProcessID = v.AggregatedOutput, v.ExitCode, v.ProcessID
		it.CommandSource, it.CommandDuration = v.Source, v.DurationMs
	case "fileChange":
		var v struct {
			Changes []FileUpdateChange `json:"changes"`
			Status  string             `json:"status"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Changes, it.PatchStatus = v.Changes, v.Status
	case "mcpToolCall":
		var v struct {
			Server    string          `json:"server"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
			Status    string          `json:"status"`
			Result    json.RawMessage `json:"result"`
			Error     json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Server, it.Tool, it.Arguments = v.Server, v.Tool, v.Arguments
		it.ToolStatus, it.Result, it.ToolError = v.Status, v.Result, v.Error
	case "dynamicToolCall":
		var v struct {
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
			Status    string          `json:"status"`
			Success   *bool           `json:"success"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Tool, it.Arguments, it.ToolStatus = v.Tool, v.Arguments, v.Status
	case "webSearch":
		var v struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(raw, &v)
		it.Query = v.Query
	}
	return it
}

// ---- turn-scoped 冷基线（官方 identity；类型为 core.TurnScopedHistoryTurn） ----

// ReadThreadRich 读取 includeTurns 历史并映射为官方 identity 的 turn 冷基线。
// limit>0 时只保留最新 limit 个 turn（官方升序，取尾部）；thread/read 本身无稳定
// 分页（excludeTurns+cursor 属 experimental 面），有界加载在客户端裁剪。
func ReadThreadRich(ctx context.Context, cl *Client, threadID string, limit int) ([]core.TurnScopedHistoryTurn, *RPCError, error) {
	th, rpcErr, err := ReadThread(ctx, cl, threadID, true)
	if err != nil || rpcErr != nil {
		return nil, rpcErr, err
	}
	turns := make([]core.TurnScopedHistoryTurn, 0, len(th.Turns))
	for _, t := range th.Turns {
		ht := core.TurnScopedHistoryTurn{TurnID: t.ID, Status: t.Status}
		if t.Error != nil {
			ht.ErrorMessage = t.Error.Message
		}
		if t.StartedAt != nil {
			ht.StartedAt = time.Unix(*t.StartedAt, 0).UTC()
			ht.HasTime = true
		}
		if t.CompletedAt != nil {
			ht.CompletedAt = time.Unix(*t.CompletedAt, 0).UTC()
		}
		if t.ItemsView == TurnItemsViewNotLoaded {
			// 官方未加载 item：不伪造内容；记录边界事实（§9.2 不猜完成）。
			ht.SkippedTypes = append(ht.SkippedTypes, "itemsView:"+TurnItemsViewNotLoaded)
		}
		for _, rawItem := range t.Items {
			it := decodeThreadItem(rawItem)
			mapHistoryItem(&ht, it)
		}
		turns = append(turns, ht)
	}
	if limit > 0 && len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	return turns, nil, nil
}

// mapHistoryItem 把一个官方 item 映射进 core.TurnScopedHistoryTurn。身份/命名纪律见文件头。
func mapHistoryItem(ht *core.TurnScopedHistoryTurn, it ThreadItem) {
	switch it.Type {
	case "userMessage":
		if ht.UserItemID != "" {
			// 官方 turn 的首个 userMessage 是 turn 输入；后续（如 steer 注入）按
			// 服务端顺序并入正文，不丢不重。
			if it.UserText() != "" {
				ht.Parts = append(ht.Parts, map[string]any{"type": "text", "content": it.UserText(), "itemId": it.ID})
			}
			return
		}
		ht.UserItemID = it.ID
		ht.UserText = it.UserText()
	case "agentMessage":
		if it.Text == "" {
			return
		}
		ht.Parts = append(ht.Parts, map[string]any{"type": "text", "content": it.Text, "itemId": it.ID})
	case "reasoning":
		// summary 与 raw content 不重复（§7 reasoning 行）：优先官方 summary。
		text := strings.Join(it.Summary, "\n")
		if strings.TrimSpace(text) == "" {
			text = strings.Join(it.ReasoningTail, "\n")
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		ht.Parts = append(ht.Parts, map[string]any{"type": "reasoning", "content": text, "itemId": it.ID})
	case "commandExecution":
		step := map[string]any{
			"id":       it.ID,
			"toolName": "Bash",
			"status":   commandStepStatus(it.CommandStatus),
		}
		step["toolInput"] = map[string]any{"command": it.Command, "cwd": it.CommandCwd}
		step["title"] = it.Command
		if it.AggregatedOut != nil {
			step["output"] = *it.AggregatedOut
		}
		if it.ExitCode != nil {
			step["exitCode"] = *it.ExitCode
		}
		if it.CommandDuration != nil {
			step["duration"] = *it.CommandDuration
		}
		ht.Parts = append(ht.Parts, map[string]any{"type": "tool", "step": step, "itemId": it.ID})
	case "fileChange":
		step := map[string]any{
			"id":       it.ID,
			"toolName": "Patch",
			"status":   commandStepStatus(it.PatchStatus),
		}
		changes := make([]map[string]any, 0, len(it.Changes))
		for _, ch := range it.Changes {
			c := map[string]any{"path": ch.Path, "kind": ch.ChangeKind(), "diff": ch.Diff}
			if ch.MovePath != nil {
				c["movePath"] = *ch.MovePath
			}
			changes = append(changes, c)
		}
		step["fileChanges"] = changes
		ht.Parts = append(ht.Parts, map[string]any{"type": "tool", "step": step, "itemId": it.ID})
	case "mcpToolCall":
		step := map[string]any{
			"id":       it.ID,
			"toolName": "MCP",
			"status":   commandStepStatus(it.ToolStatus),
		}
		if it.Server != "" || it.Tool != "" {
			step["title"] = strings.TrimSpace(it.Server + " " + it.Tool)
		}
		if len(it.Arguments) > 0 {
			step["toolInput"] = json.RawMessage(it.Arguments)
		}
		if len(it.Result) > 0 {
			step["output"] = json.RawMessage(it.Result)
		} else if len(it.ToolError) > 0 {
			step["output"] = json.RawMessage(it.ToolError)
		}
		ht.Parts = append(ht.Parts, map[string]any{"type": "tool", "step": step, "itemId": it.ID})
	case "dynamicToolCall":
		step := map[string]any{
			"id":       it.ID,
			"toolName": it.Tool,
			"status":   commandStepStatus(it.ToolStatus),
		}
		if len(it.Arguments) > 0 {
			step["toolInput"] = json.RawMessage(it.Arguments)
		}
		ht.Parts = append(ht.Parts, map[string]any{"type": "tool", "step": step, "itemId": it.ID})
	case "plan":
		// plan item 的官方消费路径是 todo dock；冷基线以工具卡承载官方 text，
		// 不解析为本地 todo（live plan→todos 映射在 Phase 3 codec 统一）。
		if strings.TrimSpace(it.Text) == "" {
			return
		}
		// 审计 §3.3-C2：官方 plan item 无 status 字段（仅 text），turn 级
		// turn.status 是唯一终态来源——completed → completed，否则 unknown，
		// 不无条件合成 "completed"（§9.2 不本地猜完成）。
		status := "unknown"
		if ht.Status == "completed" {
			status = "completed"
		}
		step := map[string]any{"id": it.ID, "toolName": "Plan", "status": status, "output": it.Text}
		ht.Parts = append(ht.Parts, map[string]any{"type": "tool", "step": step, "itemId": it.ID})
	case "webSearch":
		step := map[string]any{
			"id":       it.ID,
			"toolName": "WebSearch",
			"status":   "completed",
			"title":    it.Query,
		}
		ht.Parts = append(ht.Parts, map[string]any{"type": "tool", "step": step, "itemId": it.ID})
	case "contextCompaction":
		ht.SystemNotes = append(ht.SystemNotes, "contextCompaction")
	default:
		// 未识别/未取样 variant（hookPrompt/imageView/sleep/subAgentActivity/...）：
		// 记录官方 type，跳过不猜（§7.1）。
		if it.Type != "" {
			ht.SkippedTypes = append(ht.SkippedTypes, it.Type)
		}
	}
}

// UserText 拼接 userMessage 的 text parts（image 等 variant 不映射正文）。
func (it ThreadItem) UserText() string {
	var b strings.Builder
	for _, p := range it.Content {
		if p.Type != "text" || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

// commandStepStatus 把官方执行状态映射为 projection step status：
// inProgress→running、completed→completed、failed→failed、declined→declined（原样保留
// 官方枚举词，declined 是审批拒绝的官方终态）。
func commandStepStatus(official string) string {
	switch official {
	case "inProgress":
		return "running"
	case "", "completed", "failed", "declined":
		return official
	default:
		return official
	}
}

// ---- core provider 面（SSV2 pathless hydrate 消费） ----

// GetTurnScopedRichHistory 实现 core.TurnScopedRichHistoryProvider：官方 turn
// identity 的冷基线（§9.1 live/cold 同一官方 identity 合并）。
func (a *Agent) GetTurnScopedRichHistory(ctx context.Context, sessionID string, limit int) ([]core.TurnScopedHistoryTurn, error) {
	var out []core.TurnScopedHistoryTurn
	err := a.withClient(ctx, func(cl *Client) error {
		turns, rpcErr, err := ReadThreadRich(ctx, cl, sessionID, limit)
		if err != nil {
			return err
		}
		if rpcErr != nil {
			return rpcErr
		}
		out = turns
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetRichSessionHistory 实现 core.RichHistoryProvider（扁平兼容面）：turn-scoped
// 冷基线折叠为 user/assistant 行。assistant 行 ID 用官方 agentMessage 之外的
// turn 边界（Parts 为空的 turn 折叠时无内容行——不编造）。
func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	turns, err := a.GetTurnScopedRichHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.RichHistoryEntry, 0, len(turns)*2)
	for _, t := range turns {
		started := t.StartedAt
		if !t.HasTime {
			started = t.CompletedAt
		}
		if t.UserItemID != "" && t.UserText != "" {
			out = append(out, core.RichHistoryEntry{
				ID:        t.UserItemID,
				Role:      "user",
				Content:   t.UserText,
				Timestamp: started,
			})
		}
		if len(t.Parts) == 0 && t.ErrorMessage == "" && len(t.SystemNotes) == 0 {
			continue
		}
		entry := core.RichHistoryEntry{
			// assistant 行身份：官方 turn id（本 backend 的扁平兼容面不进入
			// SSV2 冷基线 dispatch——dispatch 走 turn-scoped provider）。
			ID:              t.TurnID,
			Role:            "assistant",
			Parts:           t.Parts,
			Timestamp:       started,
			TurnStartedAt:   timeOrZero(t.HasTime, t.StartedAt),
			TurnCompletedAt: timeOrZero(t.HasTime, t.CompletedAt),
		}
		if t.ErrorMessage != "" {
			entry.Content = "官方 turn 失败：" + t.ErrorMessage
		}
		out = append(out, entry)
		for _, note := range t.SystemNotes {
			// 合成 system 行 ID（审计 §3.3-C2：官方无 system note 行——bridge 兼容
			// 面发明）：turnID+":"+note。碰撞不可能——官方 turn id 全局唯一 × note
			// 枚举（当前仅 contextCompaction）在单 turn 内唯一。
			out = append(out, core.RichHistoryEntry{
				ID: t.TurnID + ":" + note, Role: "system", Content: note, Timestamp: started,
			})
		}
	}
	return out, nil
}

func timeOrZero(has bool, t time.Time) *time.Time {
	if !has || t.IsZero() {
		return nil
	}
	v := t
	return &v
}

// GetSessionHistory 实现 core.HistoryProvider（legacy 兼容面）。
func (a *Agent) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	rich, err := a.GetRichSessionHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.HistoryEntry, 0, len(rich))
	for _, e := range rich {
		out = append(out, core.HistoryEntry{Role: e.Role, Content: e.Content, Timestamp: e.Timestamp})
	}
	return out, nil
}

// IsSessionActive 实现 core.SessionActivityProbing：官方 thread.status 是 activity
// 真相（active=有 turn 在跑；§9.2 不另造本地 detector）。读失败保守按 active 处理。
func (a *Agent) IsSessionActive(ctx context.Context, sessionID string) bool {
	active := true
	_ = a.withClient(ctx, func(cl *Client) error {
		th, rpcErr, err := ReadThread(ctx, cl, sessionID, false)
		if err != nil {
			return nil // 保守：错误按 active
		}
		if rpcErr != nil {
			return nil
		}
		active = th.Status.Type == ThreadStatusActive
		return nil
	})
	return active
}

var (
	_ core.TurnScopedRichHistoryProvider = (*Agent)(nil)
	_ core.RichHistoryProvider           = (*Agent)(nil)
	_ core.HistoryProvider               = (*Agent)(nil)
	_ core.SessionActivityProbing        = (*Agent)(nil)
)
