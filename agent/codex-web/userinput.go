package codexweb

// userinput.go —— item/tool/requestUserInput 的规范化与应答（§7.2）。
//
// 官方样本冻结（testdata/official-0.149.0-alpha.4/dumps/interaction，0.149.0-alpha.4）：
//
//	request params: {threadId, turnId, itemId,
//	  questions: [{id, header, question, isOther, isSecret, options: [{label, description}]}],
//	  isBlocking: false, autoResolutionMs: null}
//	success response: {"answers": {"<官方 question id>": {"answers": ["<option label 或自由文本>"]}}}
//	收口：serverRequest/resolved {threadId, requestId=envelope id}。
//
// 身份纪律：interactionID = threadId ":" itemId（interactions.go）；question 归一化 ID
// 直接采用官方原 id（样本保证每题有非空 id；空/重复 → invalid_backend_request failed，
// 不注册、不回写）。官方 options 无 id，归一化选项 ID 派生为 qid "_o_" 序号，应答时
// 经快照映射回官方 label。官方 0.149 request_user_input tool 强制每题 2–3 个
// options；自由文本通过 isOther=true 的 Other 路径表达。
// 跳过（Mac 面板「跳过」）：官方 wire 无独立 reject 词汇——ToolRequestUserInputResponse
// 只有 answers，跳过 = 空 answers 响应（官方 client-error 亦回落空 answers 继续 turn，
// bespoke_event_handling.rs on_request_user_input_response）；canReject=true 让 iOS
// 渲染「跳过本题」并把跳过按空 answers 应答（2026-08-25 真机对齐 Mac 面板）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// userInputRawQuestion 是 params.questions[] 的最小解析结构。
type userInputRawQuestion struct {
	ID       string `json:"id"`
	Header   string `json:"header"`
	Question string `json:"question"`
	IsOther  bool   `json:"isOther"`
	IsSecret bool   `json:"isSecret"`
	Options  []struct {
		Label       string  `json:"label"`
		Description *string `json:"description"`
	} `json:"options"`
}

// userInputSnapshot 是应答映射快照：归一化答案 → 官方 wire answers 所需信息。
type userInputSnapshot struct {
	OptionLabel map[string]string                   // 派生 optionID → 官方 label
	Mode        map[string]core.UserInputAnswerMode // qid → single/text
	Custom      map[string]bool                     // qid → allowsCustomAnswer（=isOther 或 text 题）
	Order       []string                            // 官方题序（required 全量校验）
}

// userInputEvents 把 requestUserInput server request 转成 EventUserInputRequested。
// questions 可规范化 → 注册 + pending；不可 → failed(invalid_backend_request)，不注册不回写。
func (a *Agent) userInputEvents(it *Interaction) []core.Event {
	var p struct {
		Questions        []userInputRawQuestion `json:"questions"`
		AutoResolutionMs *int64                 `json:"autoResolutionMs"`
	}
	if err := json.Unmarshal(it.Params, &p); err != nil {
		slog.Warn("codexweb interaction: requestUserInput params unparseable", "id", it.InteractionID, "error", err)
		return a.userInputFailedEvent(it, "invalid_backend_request")
	}
	snap, err := normalizeUserInputQuestions(p.Questions)
	if err != nil {
		slog.Warn("codexweb interaction: requestUserInput questions invalid", "id", it.InteractionID, "error", err)
		return a.userInputFailedEvent(it, "invalid_backend_request")
	}
	it.UI = snap

	var expiresAt int64
	if p.AutoResolutionMs != nil && *p.AutoResolutionMs > 0 {
		expiresAt = time.Now().UnixMilli() + *p.AutoResolutionMs
	}
	return []core.Event{{
		Type:      core.EventUserInputRequested,
		SessionID: it.ThreadID,
		TurnID:    it.TurnID,
		ItemID:    it.ItemID,
		ThreadID:  it.ThreadID,
		UserInput: &core.UserInputInteraction{
			InteractionID: it.InteractionID,
			Status:        core.UserInputStatusPending,
			Questions:     normalizedUserInputQuestions(it.InteractionID, p.Questions),
			CanRespond:    true,
			// 官方 wire 没有独立 reject 词汇：跳过 = 空 answers 响应
			// （ToolRequestUserInputResponse{answers:{}}；官方向错误回退亦为空
			// answers 继续 turn，Mac 面板「跳过」即此语义，bespoke_event_handling.rs
			// on_request_user_input_response）。
			CanReject:     true,
			ExpiresAt:     expiresAt,
		},
	}}
}

func (a *Agent) userInputFailedEvent(it *Interaction, code string) []core.Event {
	return []core.Event{{
		Type:      core.EventUserInputRequested,
		SessionID: it.ThreadID,
		TurnID:    it.TurnID,
		ItemID:    it.ItemID,
		ThreadID:  it.ThreadID,
		UserInput: &core.UserInputInteraction{
			InteractionID:  it.InteractionID,
			Status:         core.UserInputStatusFailed,
			DiagnosticCode: code,
		},
	}}
}

// normalizeUserInputQuestions 校验并构建应答映射快照。每题必须有非空官方 id、题干
// 与 2–3 个 options；id 批内不得重复。官方自由文本由 single + isOther 表达。
func normalizeUserInputQuestions(raw []userInputRawQuestion) (*userInputSnapshot, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("questions must not be empty")
	}
	snap := &userInputSnapshot{
		OptionLabel: map[string]string{},
		Mode:        map[string]core.UserInputAnswerMode{},
		Custom:      map[string]bool{},
	}
	for i, q := range raw {
		if strings.TrimSpace(q.ID) == "" {
			return nil, fmt.Errorf("question[%d] missing id", i)
		}
		qid := q.ID // wire answer key must preserve the official id byte-for-byte
		if strings.TrimSpace(q.Question) == "" {
			return nil, fmt.Errorf("question[%d] missing question text", i)
		}
		if _, dup := snap.Mode[qid]; dup {
			return nil, fmt.Errorf("question[%d] duplicate id %q", i, qid)
		}
		snap.Order = append(snap.Order, qid)
		// 官方约束仅"每题 options 不得为空"（request_user_input_spec.rs
		// "requires non-empty options"）；Phase 0 样本碰巧 2–3 个被误当成硬限，
		// 2026-08-25 iOS 发起真机模型生成 4 选项直接 invalid_backend_request。
		// 对齐官方：非空即可，数量不设上限。
		if len(q.Options) < 1 {
			return nil, fmt.Errorf("question[%d] options must not be empty", i)
		}
		snap.Mode[qid] = core.UserInputAnswerModeSingle
		snap.Custom[qid] = q.IsOther
		for j, o := range q.Options {
			if strings.TrimSpace(o.Label) == "" {
				return nil, fmt.Errorf("question[%d] option[%d] missing label", i, j)
			}
			optID := fmt.Sprintf("%s_o_%d", qid, j)
			snap.OptionLabel[optID] = o.Label
		}
	}
	return snap, nil
}

// normalizedUserInputQuestions 构建面向 iOS 的规范化 questions（与快照同序同规则）。
func normalizedUserInputQuestions(_ string, raw []userInputRawQuestion) []core.UserInputQuestion {
	out := make([]core.UserInputQuestion, 0, len(raw))
	for _, q := range raw {
		qid := q.ID
		cq := core.UserInputQuestion{
			ID:       qid,
			Header:   q.Header,
			Prompt:   q.Question,
			IsSecret: q.IsSecret,
			Required: true,
		}
		cq.AnswerMode = core.UserInputAnswerModeSingle
		cq.AllowsCustomAnswer = q.IsOther
		for j, o := range q.Options {
			opt := core.UserInputOption{Label: o.Label, ID: fmt.Sprintf("%s_o_%d", qid, j)}
			if o.Description != nil {
				opt.Description = *o.Description
			}
			cq.Options = append(cq.Options, opt)
		}
		out = append(out, cq)
	}
	return out
}

// ResolveUserInput 实现 core.UserInputResponder（v2 唯一回答入口，§10.1）。
// 写官方 response 成功后才收口并发 answered(ios)；写失败保持 pending 供重试。
func (a *Agent) ResolveUserInput(ctx context.Context, interactionID, _ string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	resolution, err := a.resolveUserInput(ctx, interactionID, action, answers)
	if err != nil {
		return core.UserInputResolution{}, err
	}
	return resolution, nil
}

func (a *Agent) resolveUserInput(ctx context.Context, interactionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	if action != core.UserInputActionAnswer && action != core.UserInputActionReject {
		return core.UserInputResolution{}, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown action"}
	}
	it := a.registry.Lookup(interactionID)
	switch {
	case it != nil:
	case a.registry.ResolvedKnown(interactionID):
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	default:
		return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "interaction not found"}
	}
	if it.Kind != InteractionUserInput || it.UI == nil {
		return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "interaction is not a pending structured user input"}
	}
	var wireAnswers map[string]any
	if action == core.UserInputActionAnswer {
		var err error
		wireAnswers, err = buildUserInputWireAnswers(it.UI, answers)
		if err != nil {
			return core.UserInputResolution{}, err
		}
	} else {
		// 跳过：官方 ToolRequestUserInputResponse 只有 answers 字段，Mac 面板「跳过」
		// 即空 answers（官方对 client error 亦回落空 answers 继续 turn）；不答复会让
		// 官方永远挂起。
		wireAnswers = map[string]any{}
	}
	it, claimed := a.registry.Claim(interactionID)
	if it == nil {
		if a.registry.ResolvedKnown(interactionID) {
			return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
		}
		return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "interaction not found"}
	}
	if !claimed {
		return core.UserInputResolution{Outcome: core.UserInputOutcomeInProgress, CurrentStatus: core.UserInputStatusPending}, nil
	}
	sent := false
	defer func() {
		if !sent {
			a.registry.ReleaseClaim(interactionID, it)
		}
	}()
	cl, err := a.clientForEpoch(ctx, it.Epoch)
	if err != nil {
		return core.UserInputResolution{}, err
	}
	if err := cl.RespondServerRequest(it.RequestID, map[string]any{"answers": wireAnswers}); err != nil {
		slog.Warn("codexweb interaction: requestUserInput response failed", "id", interactionID, "error", err)
		return core.UserInputResolution{}, &core.UserInputError{Code: "backend_response_failed", Message: "failed to write official response"}
	}
	sent = true
	outcome := "answered"
	if action == core.UserInputActionReject {
		outcome = "skipped"
	}
	a.registry.NoteOutcome(interactionID, outcome)
	// 收口不在这里本地合成：统一交给官方 serverRequest/resolved（resolvedEvents 按
	// kind 产出 permission_resolved / user_input_resolved，主泵与观察泵都在处理）。
	// 提前 MarkResolved 会清掉 pending，官方 resolved 到达时无法归属，且观察路径
	// 收不到任何事件——iOS 的 user_input 面板因此永不消失（2026-08-25 真机：
	// Mac 面板已消失并继续执行，iOS 面板残留）。与 respondPermission 同一语义。
	status := core.UserInputStatusAnswered
	if action == core.UserInputActionReject {
		status = core.UserInputStatusRejected
	}
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: status}, nil
}

// respondUserInput 是 legacy question_reply/reject 的别名路径（仅 `.off` legacy client，
// 不作 v2 fallback，core/interfaces.go）。values 为派生 option id 或自由文本，映射到
// 首题作答（官方样本为单题批）。
func (a *Agent) respondUserInput(ctx context.Context, interactionID string, values []string, reject bool) error {
	if reject {
		return &core.UserInputError{Code: "response_not_supported", Message: "codex-web structured user input does not support reject"}
	}
	it := a.registry.Lookup(interactionID)
	if it == nil || it.Kind != InteractionUserInput || it.UI == nil || len(it.UI.Order) == 0 {
		return &core.UserInputError{Code: "interaction_not_found", Message: "interaction not found"}
	}
	qid := it.UI.Order[0]
	vals := make([]core.UserInputValue, 0, len(values))
	for _, v := range values {
		if _, isOpt := it.UI.OptionLabel[v]; isOpt {
			vals = append(vals, core.UserInputValue{Kind: core.UserInputValueOption, OptionID: v})
		} else {
			vals = append(vals, core.UserInputValue{Kind: core.UserInputValueText, Text: v})
		}
	}
	_, err := a.resolveUserInput(ctx, interactionID, core.UserInputActionAnswer, []core.UserInputAnswer{{QuestionID: qid, Values: vals}})
	return err
}

// buildUserInputWireAnswers 校验规范化答案并产出官方 wire answers map：
// key = 官方 question id；value.answers 恒 string[]（single/text 恰一元素）。
// 全部 required：缺题即 invalid_answer_shape。非空校验用 TrimSpace，写入用户原文。
func buildUserInputWireAnswers(snap *userInputSnapshot, answers []core.UserInputAnswer) (map[string]any, error) {
	expected := map[string]bool{}
	for _, qid := range snap.Order {
		expected[qid] = true
	}
	seen := map[string]bool{}
	out := map[string]any{}
	for _, ans := range answers {
		qid := ans.QuestionID
		if !expected[qid] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown question id"}
		}
		if seen[qid] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "duplicate question id"}
		}
		seen[qid] = true
		labels, err := userInputAnswerLabels(snap, qid, ans.Values)
		if err != nil {
			return nil, err
		}
		out[qid] = map[string]any{"answers": labels}
	}
	for _, qid := range snap.Order {
		if !seen[qid] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "missing required question"}
		}
	}
	return out, nil
}

func userInputAnswerLabels(snap *userInputSnapshot, qid string, values []core.UserInputValue) ([]string, error) {
	mode := snap.Mode[qid]
	switch mode {
	case core.UserInputAnswerModeSingle:
		if len(values) != 1 {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "single requires exactly one value"}
		}
		v := values[0]
		switch v.Kind {
		case core.UserInputValueOption:
			label, ok := snap.OptionLabel[v.OptionID]
			if !ok {
				return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown option"}
			}
			return []string{label}, nil
		case core.UserInputValueText:
			if !snap.Custom[qid] {
				return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "custom text not allowed for this question"}
			}
			if strings.TrimSpace(v.Text) == "" {
				return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "empty text"}
			}
			return []string{v.Text}, nil
		default:
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown value kind"}
		}
	case core.UserInputAnswerModeText:
		if len(values) != 1 || values[0].Kind != core.UserInputValueText {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "text mode requires exactly one text value"}
		}
		if strings.TrimSpace(values[0].Text) == "" {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "empty text"}
		}
		return []string{values[0].Text}, nil
	default:
		// 官方样本只产生 single/text（multiple 无样本，fail closed）。
		return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unsupported answer mode"}
	}
}

var _ core.UserInputResponder = (*Agent)(nil)
