package gobridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// S-3 v2 summary producer（冻结规范 docs/2026-10-19-s3-v2-summary-freeze-spec.md v3；
// canonical = docs/protocol/bridge-v1.md §Projection Window v2 Summary）。
// ③ 正文优先：普通 brief 直接显示；超长/工具过程折叠；always-visible 例外
// （running / permission 族 part / user_input pending part）原样内嵌 liveState。
// 不触碰 frozen v1 window 类型与字段集（additive 新 RPC）。

const (
	// summaryViewPayloadBudget = ≤1MiB 软预算（brief + 工具占位合计；liveState 例外与
	// scalarState 不计入；超限老 turns 降级为 budget 折叠，不截断已交付 brief）。
	summaryViewPayloadBudget = 1 << 20
	// brief 上限（chars）：user/agent brief 128；agentBriefHead 64。
	summaryBriefMaxChars   = 128
	summaryBriefHeadChars = 64
)

var errSummaryOversized = errors.New("summary liveState exception exceeds projectionMaxBytes")

// GetSessionProjectionWindowV2Params mirrors BridgeGetSessionProjectionWindowV2Params.
type GetSessionProjectionWindowV2Params struct {
	SessionID    string `json:"sessionId"`
	Directory    string `json:"directory,omitempty"`
	BackendID    string `json:"backendId"`
	Direction    string `json:"direction"`
	Cursor       string `json:"cursor,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	AnchorTurnID string `json:"anchorTurnId,omitempty"`
	ItemsView    string `json:"itemsView"` // "summary" | "full"（必填；echo 即承诺两值）
}

// TurnSummaryBrief mirrors BridgeTurnSummaryBrief。
type TurnSummaryBrief struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// TurnSummaryDescriptor mirrors BridgeTurnSummaryDescriptor（冻结定义 1+2）。
type TurnSummaryDescriptor struct {
	TurnID           string            `json:"turnId"`
	Status           string            `json:"status"`
	StartedAt        int64             `json:"startedAt,omitempty"`
	CompletedAt      int64             `json:"completedAt,omitempty"`
	DurationMs       int64             `json:"durationMs,omitempty"`
	UserBrief        *TurnSummaryBrief `json:"userBrief,omitempty"`
	AgentBrief       *TurnSummaryBrief `json:"agentBrief,omitempty"`
	AgentBriefHead   string            `json:"agentBriefHead,omitempty"`
	Folded           bool              `json:"folded"`
	FoldReason       string            `json:"foldReason,omitempty"` // long_body|tool_process|both|budget
	ToolProcessCount int               `json:"toolProcessCount,omitempty"`
	ToolProcessBytes int               `json:"toolProcessBytes,omitempty"`
	// always-visible 例外通道：完整 turn 原样内嵌（frozen TurnProjection，
	// never-split，不受预算约束，无新 wire 字段——识别按既有 part 类型）。
	LiveState     *TurnProjection `json:"liveState,omitempty"`
	TurnGeneration int            `json:"turnGeneration,omitempty"`
}

// ProjectionSummaryBudget mirrors BridgeProjectionSummaryBudget。
type ProjectionSummaryBudget struct {
	PayloadBytes  int      `json:"payloadBytes"`
	FoldedTurnIds []string `json:"foldedTurnIds"`
	Exceptions    []string `json:"exceptions"`
}

// ProjectionWindowV2SummaryResponse mirrors BridgeGetSessionProjectionWindowV2SummaryResult。
type ProjectionWindowV2SummaryResponse struct {
	Window        ProjectionWindowDescriptor `json:"window"`
	Turns         []TurnSummaryDescriptor    `json:"turns"`
	ScalarState   AnchorNewerScalarState     `json:"scalarState"`
	SyncRev       int                        `json:"syncRev"`
	Resume        *ProjectionWindowResume    `json:"resume,omitempty"`
	ItemsView     string                     `json:"itemsView"` // "summary"
	SummaryBudget ProjectionSummaryBudget    `json:"summaryBudget"`
}

// ProjectionWindowV2FullResponse mirrors BridgeGetSessionProjectionWindowV2FullResult。
type ProjectionWindowV2FullResponse struct {
	Window      ProjectionWindowDescriptor `json:"window"`
	Turns       []TurnProjection           `json:"turns"`
	ScalarState AnchorNewerScalarState     `json:"scalarState"`
	SyncRev     int                        `json:"syncRev"`
	Resume      *ProjectionWindowResume    `json:"resume,omitempty"`
	ItemsView   string                     `json:"itemsView"` // "full"
}

// ── part 分类（与冻结样本/③ 语义一致；不引入新 part 类型判定源）──

// summaryPartIsToolProcess：非 text 正文族 part = 工具过程占位（tool_use/tool_result/
// thinking/reasoning 等由既有 part type 字符串承载）。text 正文不计。
func summaryPartIsToolProcess(partType string) bool {
	switch partType {
	case "text", "permission_request", "permission_resolved", "user_input",
		"user_input_resolved", "system":
		return false
	default:
		return true
	}
}

// summaryTurnRequiresAlwaysVisible：running 轮次 / permission 族 part / user_input 族
// pending part（wire 字段名 status=="pending"）→ liveState 例外通道（冻结定义 2）。
func summaryTurnRequiresAlwaysVisible(turn TurnProjection) bool {
	if turn.Status != "completed" {
		return true
	}
	for _, message := range []*MessageProjection{turn.Assistant, turn.User} {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			// F-3（Codex 分析，2026-10-20）：正式权限由 RequiresPermissionConfirmation
			// 标记承载（projection_types.go:57——dsh-web approval → type=tool + 标记；
			// SSV2 客户端按该标记渲染权限卡，raw permission_request 不是 SoT）。
			// completed 轮次内的标记同样完整可见（状态条件只保护非 completed）。
			if part.RequiresPermissionConfirmation {
				return true
			}
			switch part.Type {
			case "permission_request":
				return true
			case "user_input":
				if part.UserInputStatus == "" || part.UserInputStatus == "pending" {
					return true
				}
			}
		}
	}
	return false
}

// briefFromText：≤max chars 完整（truncated=false）；超长截断（truncated=true）。
func briefFromText(text string, maxChars int) *TurnSummaryBrief {
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return &TurnSummaryBrief{Text: text, Truncated: false}
	}
	return &TurnSummaryBrief{Text: string(runes[:maxChars]), Truncated: true}
}

// summarizeTurn：单 turn → TurnSummaryDescriptor（不含预算降级；liveState 例外原样内嵌）。
func summarizeTurn(turn TurnProjection) TurnSummaryDescriptor {
	descriptor := TurnSummaryDescriptor{
		TurnID:    turn.TurnID,
		Status:    turn.Status,
		StartedAt: turn.StartedAt,
	}
	if turn.CompletedAt != 0 {
		descriptor.CompletedAt = turn.CompletedAt
		if turn.StartedAt != 0 {
			descriptor.DurationMs = turn.CompletedAt - turn.StartedAt
		} else if turn.DurationMs != 0 {
			descriptor.DurationMs = turn.DurationMs
		}
	}
	descriptor.TurnGeneration = turn.TurnGeneration

	if summaryTurnRequiresAlwaysVisible(turn) {
		// 例外通道：完整 turn 原样内嵌；briefs 照常计算（样本 turn-104 形态）。
		full := turn
		descriptor.LiveState = &full
	}

	// userBrief = user 首个 text 正文。
	if turn.User != nil {
		for _, part := range turn.User.Parts {
			if part.Type == "text" && part.Text != "" {
				descriptor.UserBrief = briefFromText(part.Text, summaryBriefMaxChars)
				break
			}
		}
	}
	// agentBrief（末段，结论优先）+ agentBriefHead（开头 64 chars）。
	if turn.Assistant != nil {
		var textParts []string
		for _, part := range turn.Assistant.Parts {
			if part.Type == "text" && part.Text != "" {
				textParts = append(textParts, part.Text)
			} else if summaryPartIsToolProcess(part.Type) {
				descriptor.ToolProcessCount++
				if encoded, err := json.Marshal(part); err == nil {
					descriptor.ToolProcessBytes += len(encoded)
				}
			}
		}
		if len(textParts) > 0 {
			tail := textParts[len(textParts)-1]
			descriptor.AgentBrief = briefFromText(tail, summaryBriefMaxChars)
			head := textParts[0]
			if len([]rune(head)) > summaryBriefHeadChars {
				descriptor.AgentBriefHead = string([]rune(head)[:summaryBriefHeadChars])
			} else {
				descriptor.AgentBriefHead = head
			}
		}
	}

	// 折叠判定（③）：工具过程存在 或 正文超 brief 上限（truncated）。
	hasTool := descriptor.ToolProcessCount > 0
	longBody := (descriptor.UserBrief != nil && descriptor.UserBrief.Truncated) ||
		(descriptor.AgentBrief != nil && descriptor.AgentBrief.Truncated)
	descriptor.Folded = hasTool || longBody
	switch {
	case hasTool && longBody:
		descriptor.FoldReason = "both"
	case hasTool:
		descriptor.FoldReason = "tool_process"
	case longBody:
		descriptor.FoldReason = "long_body"
	}
	if descriptor.LiveState != nil && descriptor.FoldReason == "" {
		// 例外行无折叠原因时给 tool_process/long_body 之外的诚实值：例外本身不折叠，
		// 但若底层有未交付内容 folded 语义由上面分支覆盖；此处保持 folded=false。
		descriptor.Folded = false
	}
	return descriptor
}

// descriptorPayloadBytes：摘要净载荷（brief + 工具占位；不含 liveState 例外——never-split）。
func descriptorPayloadBytes(d TurnSummaryDescriptor) int {
	n := 0
	if d.UserBrief != nil {
		n += utf8.RuneCountInString(d.UserBrief.Text) * 3 // UTF-8 上界近似
	}
	if d.AgentBrief != nil {
		n += utf8.RuneCountInString(d.AgentBrief.Text) * 3
	}
	n += utf8.RuneCountInString(d.AgentBriefHead) * 3
	return n
}

// summarizeWindowTurns：窗口 turns → summary 行 + 预算执行（老 turns budget 降级）。
// maxExceptionBytes > 0 时对 liveState 例外做 oversized 检查（summary.oversized fail-closed）。
func summarizeWindowTurns(turns []TurnProjection, maxExceptionBytes int) ([]TurnSummaryDescriptor, ProjectionSummaryBudget, error) {
	rows := make([]TurnSummaryDescriptor, 0, len(turns))
	budget := ProjectionSummaryBudget{FoldedTurnIds: []string{}, Exceptions: []string{}}
	for _, turn := range turns {
		row := summarizeTurn(turn)
		if row.LiveState != nil {
			encoded, err := json.Marshal(*row.LiveState)
			if err != nil {
				return nil, budget, err
			}
			if maxExceptionBytes > 0 && len(encoded) > maxExceptionBytes {
				return nil, budget, fmt.Errorf("%w: liveState turn %s = %d bytes > %d",
					errSummaryOversized, row.TurnID, len(encoded), maxExceptionBytes)
			}
			budget.Exceptions = append(budget.Exceptions, row.TurnID)
		}
		rows = append(rows, row)
	}
	// 预算（软）：超限从最老 turns 降级为 budget 形态（不截断已交付 brief）。
	total := 0
	for _, row := range rows {
		total += descriptorPayloadBytes(row)
	}
	for i := 0; i < len(rows) && total > summaryViewPayloadBudget; i++ {
		if rows[i].LiveState != nil || rows[i].FoldReason == "budget" {
			continue // 例外不降级；已降级不重复计
		}
		total -= descriptorPayloadBytes(rows[i])
		rows[i].UserBrief = nil
		rows[i].AgentBrief = nil
		rows[i].AgentBriefHead = ""
		rows[i].Folded = true
		rows[i].FoldReason = "budget"
	}
	for _, row := range rows {
		if row.Folded {
			budget.FoldedTurnIds = append(budget.FoldedTurnIds, row.TurnID)
		}
		if row.FoldReason != "budget" {
			budget.PayloadBytes += descriptorPayloadBytes(row)
		}
	}
	return rows, budget, nil
}

// scalarStateFromProjection：v2 原生携带（W1 语义镜像；execution 必选）。
func scalarStateFromProjection(proj SessionProjection) AnchorNewerScalarState {
	state := AnchorNewerScalarState{
		Execution: proj.Execution,
	}
	state.PlanMode = proj.PlanMode
	state.SessionMode = proj.SessionMode
	state.CollaborationMode = proj.CollaborationMode
	state.Goal = proj.Goal
	state.CodexGoal = proj.CodexGoal
	return state
}

// summarizeAnchorRows：anchor-newer itemsView="summary" 时 turns/anchorRefresh/
// changedTurns 行的 summary 形态（token 基准不变；例外通道照常）。
func summarizeAnchorRows(turns []TurnProjection, maxExceptionBytes int) ([]TurnSummaryDescriptor, error) {
	rows, _, err := summarizeWindowTurns(turns, maxExceptionBytes)
	return rows, err
}

