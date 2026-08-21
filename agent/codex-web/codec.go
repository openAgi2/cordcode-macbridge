package codexweb

// codec.go —— 官方 Thread/Turn/Item live 通知 → core.Event（§5.2/§7.1 红线）。
//
// 红线落实：
//   - turn/started 是唯一 turn 开始真相；不因首个 delta 补造开始（delta 帧自带
//     官方 turnId/itemId——0.149 wire 事实，无需本地推导身份）；
//   - turn/completed.status/error 是唯一完成/失败真相；EOF/静默不猜完成；
//   - delta 与 completed snapshot 不双发：agentMessage/reasoning 的 item/completed
//     不再发正文事件（deltas 已流式）；命令/文件类 completed 发 result；
//   - 身份 = (threadId, turnId, itemId)（官方三元组），禁止正文相似度去重；
//   - 未识别 method 记录计数（UnknownMethods），不崩溃；不影响已广告 capability
//     之外的路径（§7.1 fail-closed 由 descriptor 层负责）。
//
// 输出回归对照（不复制实现）：agent/codex appserver_session.go / passive_subscriber.go
// 的事件词汇——Bash/MCP/WebSearch/Patch/原生 tool 名、EventResult 终态、
// tokenUsage 映射，保证 A/B 观察期两 backend 输出可比。

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// LiveCodec 把官方通知解码为 core.Event（有状态：per-thread 当前 turn、
// willRetry 连续计数）。
type LiveCodec struct {
	mu          sync.Mutex
	turnByThread map[string]string
	retryByThread map[string]int
	unknown     map[string]int
}

func NewLiveCodec() *LiveCodec {
	return &LiveCodec{
		turnByThread:  map[string]string{},
		retryByThread: map[string]int{},
		unknown:       map[string]int{},
	}
}

// UnknownMethods 返回未识别 method → 出现次数（诊断）。
func (c *LiveCodec) UnknownMethods() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.unknown))
	for k, v := range c.unknown {
		out[k] = v
	}
	return out
}

// ActiveTurn 返回 thread 当前 codec 观测到的 turn（供 steer/interrupt）。
func (c *LiveCodec) ActiveTurn(threadID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnByThread[threadID]
}

func (c *LiveCodec) setActiveTurn(threadID, turnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if turnID != "" {
		c.turnByThread[threadID] = turnID
	} else {
		delete(c.turnByThread, threadID)
	}
}

// Decode 解码一条官方通知。返回 0..n 个事件。
func (c *LiveCodec) Decode(n Notification) []core.Event {
	switch n.Method {
	case "turn/started":
		return c.decodeTurnStarted(n)
	case "turn/completed":
		return c.decodeTurnCompleted(n)
	case "item/agentMessage/delta":
		c.resetRetry(n)
		return decodeAgentMessageDelta(n)
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		c.resetRetry(n)
		return decodeReasoningDelta(n)
	case "item/started":
		return c.decodeItemStarted(n)
	case "item/completed":
		return decodeItemCompleted(n)
	case "thread/tokenUsage/updated":
		return decodeTokenUsage(n)
	case "turn/plan/updated":
		return decodePlanUpdated(n)
	case "error":
		return c.decodeErrorNotification(n)
	case "warning":
		return nil // 官方提示性警告（如 under-development features）；不映射事件
	case "thread/status/changed", "thread/started", "thread/name/updated",
		"thread/archived", "account/rateLimits/updated", "remoteControl/status/changed",
		"serverRequest/resolved", "thread/goal/cleared", "turn/diff/updated":
		// 已知但不映射 core 事件的官方面：catalog 刷新由 bridge discovery 轮询；
		// status 真相由 turn/completed 唯一承载；rateLimits 无 usage 字段（0.149 样本）。
		return nil
	default:
		c.mu.Lock()
		c.unknown[n.Method]++
		c.mu.Unlock()
		return nil
	}
}

// resetRetry 正文恢复流动后清零重试计数（provider 重连成功的官方证据）。
func (c *LiveCodec) resetRetry(n Notification) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(n.Params, &p) == nil && p.ThreadID != "" {
		c.mu.Lock()
		delete(c.retryByThread, p.ThreadID)
		c.mu.Unlock()
	}
}

func (c *LiveCodec) decodeTurnStarted(n Notification) []core.Event {
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(n.Params, &p); err != nil || p.ThreadID == "" || p.Turn.ID == "" {
		return nil
	}
	c.setActiveTurn(p.ThreadID, p.Turn.ID)
	c.mu.Lock()
	delete(c.retryByThread, p.ThreadID)
	c.mu.Unlock()
	return []core.Event{{
		Type:      core.EventTurnStarted,
		SessionID: p.ThreadID,
		TurnID:    p.Turn.ID,
		ThreadID:  p.ThreadID,
	}}
}

func (c *LiveCodec) decodeTurnCompleted(n Notification) []core.Event {
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Error  *TurnErrorInfo  `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(n.Params, &p); err != nil || p.ThreadID == "" {
		return nil
	}
	turnID := p.Turn.ID
	if turnID == "" {
		turnID = c.ActiveTurn(p.ThreadID)
	}
	if turnID == "" {
		return nil // 无官方身份的完成帧不归属（§9.1 identity 红线）
	}
	c.setActiveTurn(p.ThreadID, "")
	c.mu.Lock()
	delete(c.retryByThread, p.ThreadID)
	c.mu.Unlock()

	base := core.Event{SessionID: p.ThreadID, TurnID: turnID, ThreadID: p.ThreadID, Done: true}
	switch p.Turn.Status {
	case TurnStatusFailed:
		msg := "turn failed"
		if p.Turn.Error != nil && p.Turn.Error.Message != "" {
			msg = p.Turn.Error.Message // 官方原文（§7.1 不丢原文）
		}
		ev := base
		ev.Type = core.EventError
		ev.Error = &officialError{msg: msg}
		return []core.Event{ev}
	default:
		// completed / interrupted：EventResult 唯一终态（interrupted 不伪装 error）
		ev := base
		ev.Type = core.EventResult
		return []core.Event{ev}
	}
}

type officialError struct{ msg string }

func (e *officialError) Error() string { return e.msg }

func decodeAgentMessageDelta(n Notification) []core.Event {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
	}
	if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
		return nil
	}
	return []core.Event{{
		Type:      core.EventText,
		SessionID: p.ThreadID,
		TurnID:    p.TurnID,
		ItemID:    p.ItemID,
		ThreadID:  p.ThreadID,
		Content:   p.Delta,
	}}
}

func decodeReasoningDelta(n Notification) []core.Event {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
	}
	if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
		return nil
	}
	return []core.Event{{
		Type:      core.EventThinking,
		SessionID: p.ThreadID,
		TurnID:    p.TurnID,
		ItemID:    p.ItemID,
		ThreadID:  p.ThreadID,
		Content:   p.Delta,
	}}
}

// decodeItemStarted 工具/系统类 item 开始（agentMessage/reasoning/userMessage 的
// started 不发正文——delta/completed 承载，避免双发）。
func (c *LiveCodec) decodeItemStarted(n Notification) []core.Event {
	var p itemNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	it := decodeThreadItem(p.Item)
	switch it.Type {
	case "contextCompaction":
		return []core.Event{{Type: core.EventContextCompressing, SessionID: p.ThreadID, ThreadID: p.ThreadID}}
	case "commandExecution":
		return []core.Event{toolUseEvent(p, it, "Bash", it.Command)}
	case "fileChange":
		ev := toolUseEvent(p, it, "Patch", jsonOrEmpty(it.Raw, "changes"))
		ev.FileChanges = fileChangesFromItem(it)
		return []core.Event{ev}
	case "mcpToolCall":
		title := strings.TrimSpace(it.Server + ":" + it.Tool)
		return []core.Event{toolUseEvent(p, it, "MCP", title+"\n"+string(orEmpty(it.Arguments)))}
	case "webSearch":
		return []core.Event{toolUseEvent(p, it, "WebSearch", it.Query)}
	case "dynamicToolCall":
		return []core.Event{toolUseEvent(p, it, it.Tool, string(orEmpty(it.Arguments)))}
	default:
		return nil
	}
}

// decodeItemCompleted 工具结果；agentMessage/reasoning completed 不再发正文
// （delta 已流式——§7.1 双发红线）。
func decodeItemCompleted(n Notification) []core.Event {
	var p itemNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	it := decodeThreadItem(p.Item)
	switch it.Type {
	case "commandExecution":
		ev := core.Event{
			Type:      core.EventToolResult,
			SessionID: p.ThreadID,
			TurnID:    p.TurnID,
			ItemID:    it.ID,
			ThreadID:  p.ThreadID,
			ToolName:  "Bash",
			RequestID: it.ID,
		}
		if it.AggregatedOut != nil {
			ev.ToolResult = *it.AggregatedOut
		}
		if it.ExitCode != nil {
			code := int(*it.ExitCode)
			ev.ToolExitCode = &code
			success := code == 0
			ev.ToolSuccess = &success
		}
		ev.ToolStatus = it.CommandStatus
		return []core.Event{ev}
	case "fileChange":
		ev := core.Event{
			Type:        core.EventToolResult,
			SessionID:   p.ThreadID,
			TurnID:      p.TurnID,
			ItemID:      it.ID,
			ThreadID:    p.ThreadID,
			ToolName:    "Patch",
			RequestID:   it.ID,
			ToolResult:  jsonOrEmpty(it.Raw, "changes"),
			ToolStatus:  it.PatchStatus,
			FileChanges: fileChangesFromItem(it),
		}
		return []core.Event{ev}
	case "mcpToolCall":
		ev := core.Event{
			Type:      core.EventToolResult,
			SessionID: p.ThreadID,
			TurnID:    p.TurnID,
			ItemID:    it.ID,
			ThreadID:  p.ThreadID,
			ToolName:  "MCP",
			RequestID: it.ID,
			ToolStatus: it.ToolStatus,
		}
		if len(it.Result) > 0 {
			ev.ToolResult = string(it.Result)
		} else if len(it.ToolError) > 0 {
			ev.ToolResult = string(it.ToolError)
		}
		return []core.Event{ev}
	case "contextCompaction":
		return []core.Event{{Type: core.EventContextCompressed, SessionID: p.ThreadID, TurnID: p.TurnID, ThreadID: p.ThreadID}}
	case "dynamicToolCall":
		return []core.Event{{
			Type:       core.EventToolResult,
			SessionID:  p.ThreadID,
			TurnID:     p.TurnID,
			ItemID:     it.ID,
			ThreadID:   p.ThreadID,
			ToolName:   it.Tool,
			RequestID:  it.ID,
			ToolStatus: it.ToolStatus,
			ToolResult: string(orEmpty(it.Arguments)),
		}}
	case "webSearch":
		return []core.Event{{
			Type:      core.EventToolResult,
			SessionID: p.ThreadID,
			TurnID:    p.TurnID,
			ItemID:    it.ID,
			ThreadID:  p.ThreadID,
			ToolName:  "WebSearch",
			RequestID: it.ID,
		}}
	default:
		return nil
	}
}

type itemNotification struct {
	Item     json.RawMessage `json:"item"`
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
}

func toolUseEvent(p itemNotification, it ThreadItem, name, input string) core.Event {
	return core.Event{
		Type:      core.EventToolUse,
		SessionID: p.ThreadID,
		TurnID:    p.TurnID,
		ItemID:    it.ID,
		ThreadID:  p.ThreadID,
		ToolName:  name,
		ToolInput: input,
		RequestID: it.ID,
	}
}

func fileChangesFromItem(it ThreadItem) []core.FileChange {
	out := make([]core.FileChange, 0, len(it.Changes))
	for _, ch := range it.Changes {
		fc := core.FileChange{Path: ch.Path, Kind: ch.ChangeKind(), Diff: ch.Diff}
		if ch.MovePath != nil {
			fc.MovePath = *ch.MovePath
		}
		out = append(out, fc)
	}
	return out
}

func decodeTokenUsage(n Notification) []core.Event {
	var p struct {
		ThreadID string `json:"threadId"`
		TokenUsage struct {
			Total struct {
				TotalTokens           int `json:"totalTokens"`
				InputTokens           int `json:"inputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				OutputTokens          int `json:"outputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
			} `json:"total"`
			ModelContextWindow int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(n.Params, &p); err != nil || p.ThreadID == "" {
		return nil
	}
	usage := &core.ContextUsage{
		UsedTokens:           p.TokenUsage.Total.TotalTokens,
		TotalTokens:          p.TokenUsage.Total.TotalTokens,
		InputTokens:          p.TokenUsage.Total.InputTokens,
		CachedInputTokens:    p.TokenUsage.Total.CachedInputTokens,
		OutputTokens:         p.TokenUsage.Total.OutputTokens,
		ReasoningOutputTokens: p.TokenUsage.Total.ReasoningOutputTokens,
		ContextWindow:        p.TokenUsage.ModelContextWindow,
	}
	return []core.Event{{
		Type:         core.EventContextUsageUpdated,
		SessionID:    p.ThreadID,
		ThreadID:     p.ThreadID,
		ContextUsage: usage,
	}}
}

// decodeErrorNotification 官方 error 通知 {error:{message,...}, willRetry, threadId}。
// willRetry=true 是瞬时 provider 重试（serve 保持 turn 存活）——映射 EventRetryStatus
// （连续次数由 codec 计数，任何 delta/completed 重置）；willRetry=false 映射 EventError
// 保留官方原文。turn 终态仍只认 turn/completed。
func (c *LiveCodec) decodeErrorNotification(n Notification) []core.Event {
	var p struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool   `json:"willRetry"`
		ThreadID  string `json:"threadId"`
	}
	if err := json.Unmarshal(n.Params, &p); err != nil || strings.TrimSpace(p.Error.Message) == "" {
		return nil
	}
	if p.WillRetry {
		c.mu.Lock()
		c.retryByThread[p.ThreadID]++
		attempt := c.retryByThread[p.ThreadID]
		c.mu.Unlock()
		return []core.Event{{
			Type:        core.EventRetryStatus,
			SessionID:  p.ThreadID,
			ThreadID:    p.ThreadID,
			RetryAttempt: attempt,
			Content:     p.Error.Message,
		}}
	}
	return []core.Event{{
		Type:      core.EventError,
		SessionID: p.ThreadID,
		ThreadID:  p.ThreadID,
		Error:     &officialError{msg: p.Error.Message},
	}}
}

// decodePlanUpdated 官方 turn/plan/updated {threadId, turnId, plan:[{step,status}],
// explanation}（stable schema TurnPlanStep：pending/inProgress/completed）→ EventPlan。
// item/plan/delta 流式属 experimental（§7 🧪），未取样不消费。
func decodePlanUpdated(n Notification) []core.Event {
	var p struct {
		ThreadID     string `json:"threadId"`
		TurnID       string `json:"turnId"`
		Explanation  *string `json:"explanation"`
		Plan         []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(n.Params, &p); err != nil || p.ThreadID == "" {
		return nil
	}
	todos := make([]core.Todo, 0, len(p.Plan))
	for _, entry := range p.Plan {
		step := strings.TrimSpace(entry.Step)
		if step == "" {
			continue
		}
		todos = append(todos, core.Todo{
			Content:  step,
			Status:   normalizePlanStepStatus(entry.Status),
			Priority: "normal",
		})
	}
	if len(todos) == 0 {
		return nil
	}
	return []core.Event{{
		Type:      core.EventPlan,
		SessionID: p.ThreadID,
		TurnID:    p.TurnID,
		ThreadID:  p.ThreadID,
		Plan:      todos,
	}}
}

func normalizePlanStepStatus(official string) string {
	switch official {
	case "inProgress":
		return "in_progress"
	case "pending", "completed":
		return official
	default:
		return official // 官方枚举外原样保留（不猜）
	}
}

func jsonOrEmpty(raw json.RawMessage, key string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return string(orEmpty(obj[key]))
}

func orEmpty(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}
