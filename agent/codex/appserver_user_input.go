package codex

// appserver_user_input.go 把 P1 foundation（user_input_normalize.go / user_input_registry.go）
// 接到 Codex app-server session 的 live JSON-RPC 通道。实现 §8（Codex app-server 实现设计）：
//
//   - handleRequestUserInput：收到 item/tool/requestUserInput server request → 派生稳定
//     interactionId、规范化 questions、注册 pending、发 EventUserInputRequested(pending)；
//     questions 无法规范化时发 status=failed/invalid_backend_request，不注册 responder、不回写。
//   - serverRequest/resolved notification（在 appserver_session.go handleNotification）：反查
//     registry → MarkExternallyResolved → 发 EventUserInputResolved(answered, source=backend)。
//   - ResolveUserInput（core.UserInputResponder）：claim → 校验 answer shape → 写 §8.2 wire
//     response（每题 answers 恒 string[]，single/text 恰一元素）→ 写成功 ConfirmResolved +
//     发 answered(ios)；写失败 ReleaseClaim 回 pending + backend_response_failed。Codex
//     canReject=false → reject 返回 response_not_supported，不写 backend。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// codexRequestUserInputParams 是 item/tool/requestUserInput 的 params 最小解析结构。
// RequestID 留空——它属于 envelope 顶层 id，由 handleServerRequest 单独传入。
type codexRequestUserInputParams struct {
	ThreadID         string             `json:"threadId"`
	TurnID           string             `json:"turnId"`
	ItemID           string             `json:"itemId"`
	Questions        []codexRawQuestion `json:"questions"`
	AutoResolutionMs *uint64            `json:"autoResolutionMs"`
}

// validateCodexRawQuestions 校验 questions 可规范化（每题有 id 与 question 文本）。
// 失败时 caller 走 §8.1 step6 的 failed 路径。
func validateCodexRawQuestions(raw []codexRawQuestion) error {
	for i, q := range raw {
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("question[%d] missing id", i)
		}
		if strings.TrimSpace(q.Question) == "" {
			return fmt.Errorf("question[%d] missing question text", i)
		}
	}
	return nil
}

// buildCodexPendingEntry 把规范化后的 questions 组装成 registry pending entry。
// rawQuestionID（派生 qid → backend 原 qid）用于写 wire answers map 的 key。
func buildCodexPendingEntry(interactionID, canonical string, rawID json.RawMessage, raw []codexRawQuestion, normalized []core.UserInputQuestion) pendingEntry {
	rawQ := make(map[string]string, len(normalized))
	optLabel := make(map[string]string)
	mode := make(map[string]core.UserInputAnswerMode, len(normalized))
	custom := make(map[string]bool, len(normalized))
	order := make([]string, 0, len(normalized))
	for i, q := range normalized {
		rawQ[q.ID] = raw[i].ID
		mode[q.ID] = q.AnswerMode
		custom[q.ID] = q.AllowsCustomAnswer
		order = append(order, q.ID)
		for _, o := range q.Options {
			optLabel[o.ID] = o.Label
		}
	}
	return pendingEntry{
		interactionID:      interactionID,
		requestIDCanonical: canonical,
		rawRequestID:       rawID,
		rawQuestionID:      rawQ,
		optionLabel:        optLabel,
		questionMode:       mode,
		questionCustom:     custom,
		questionOrder:      order,
	}
}

// handleRequestUserInput 处理 item/tool/requestUserInput server request（§8.1）。
func (s *appServerSession) handleRequestUserInput(rawID json.RawMessage, params json.RawMessage) {
	var p codexRequestUserInputParams
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Warn("codex app-server: requestUserInput params parse failed", "error", err)
		// params 全坏：无 thread/turn/item 可派生 interactionId；回 JSON-RPC error 不让 server 挂起。
		s.respondServerError(rawID, -32602, "invalid requestUserInput params")
		return
	}

	var rawIDAny any
	if err := json.Unmarshal(rawID, &rawIDAny); err != nil {
		slog.Warn("codex app-server: requestUserInput id unparseable", "error", err)
		s.respondServerError(rawID, -32602, "invalid request id")
		return
	}
	typ, canonical, ok := codexRequestIDType(rawIDAny)
	if !ok {
		slog.Warn("codex app-server: requestUserInput id not string|int64", "id", rawIDAny)
		s.respondServerError(rawID, -32602, "invalid request id type")
		return
	}

	threadID := strings.TrimSpace(p.ThreadID)
	turnID := strings.TrimSpace(p.TurnID)
	itemID := strings.TrimSpace(p.ItemID)
	interactionID := deriveCodexInteractionID(typ, canonical, threadID, turnID, itemID)

	// §8.1 step6：envelope attributable 但 questions 无法规范化 → failed，不注册、不回写。
	if err := validateCodexRawQuestions(p.Questions); err != nil {
		slog.Warn("codex app-server: requestUserInput questions invalid",
			"interactionId", interactionID, "error", err)
		s.emit(core.Event{
			Type:      core.EventUserInputRequested,
			SessionID: threadID,
			TurnID:    turnID,
			ItemID:    itemID,
			UserInput: &core.UserInputInteraction{
				InteractionID:  interactionID,
				Status:         core.UserInputStatusFailed,
				Questions:      normalizeCodexQuestions(interactionID, p.Questions),
				CanRespond:     false,
				CanReject:      false,
				DiagnosticCode: "invalid_backend_request",
			},
		})
		return
	}

	questions := normalizeCodexQuestions(interactionID, p.Questions)
	entry := buildCodexPendingEntry(interactionID, canonical, rawID, p.Questions, questions)

	// expiresAt 仅作显示（§8.3：不授权本地 timer 改 status）。
	var expiresAt int64
	if p.AutoResolutionMs != nil {
		expiresAt = time.Now().UnixMilli() + int64(*p.AutoResolutionMs)
	}

	registered := s.userInputReg.Register(entry)
	if !registered {
		// 已存在（重放）：只在仍 pending 时重发 pending（幂等 upsert）；已 resolved 不降级。
		if s.userInputReg.Status(interactionID) != registryPending {
			return
		}
	}

	s.emit(core.Event{
		Type:      core.EventUserInputRequested,
		SessionID: threadID,
		TurnID:    turnID,
		ItemID:    itemID,
		UserInput: &core.UserInputInteraction{
			InteractionID: interactionID,
			Status:        core.UserInputStatusPending,
			Questions:     questions,
			CanRespond:    true,
			CanReject:     false, // Codex 无已验证 reject 语义（§8.2）
			ExpiresAt:     expiresAt,
		},
	})
}

// ResolveUserInput 实现 core.UserInputResponder（§7/§8.2）。go-bridge resolve_user_input 唯一入口。
func (s *appServerSession) ResolveUserInput(ctx context.Context, interactionID, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	if err := ctx.Err(); err != nil {
		return core.UserInputResolution{}, err
	}
	if !s.alive.Load() {
		return core.UserInputResolution{}, &core.UserInputError{Code: "session_not_active", Message: "codex session not active"}
	}
	if action == core.UserInputActionReject {
		// Codex canReject=false：fail-closed，不写 backend（§8.2）。
		return core.UserInputResolution{}, &core.UserInputError{Code: "response_not_supported", Message: "codex structured user input does not support reject"}
	}
	if action != core.UserInputActionAnswer {
		return core.UserInputResolution{}, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown action"}
	}

	dec := s.userInputReg.Claim(interactionID, clientActionID)
	// 幂等：同 clientActionID 已成功处理过。
	if dec.outcome == core.UserInputOutcomeAccepted {
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
	}
	if dec.outcome == core.UserInputOutcomeAlreadyResolved {
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	}
	if !dec.claimed {
		if dec.status == registryAbsent {
			return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "interaction not found"}
		}
		if dec.status == registryClaimed {
			return core.UserInputResolution{Outcome: core.UserInputOutcomeInProgress, CurrentStatus: core.UserInputStatusPending}, nil
		}
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	}

	snap := dec.snapshot
	wireAnswers, err := buildCodexWireAnswers(snap, answers)
	if err != nil {
		s.userInputReg.ReleaseClaim(interactionID)
		return core.UserInputResolution{}, err
	}

	result := map[string]any{"answers": wireAnswers}
	if err := s.respondServerRequestContext(ctx, snap.RawRequestID, result); err != nil {
		slog.Warn("codex app-server: write requestUserInput response failed", "interactionId", interactionID, "error", err)
		s.userInputReg.ReleaseClaim(interactionID)
		return core.UserInputResolution{}, &core.UserInputError{Code: "backend_response_failed", Message: "failed to write codex response"}
	}

	// ConfirmResolved 仅在仍 claimed 时转移。false = 外部 serverRequest/resolved 刚好先到并已
	// 发 answered(backend)（§8.3 幂等），此时不再发第二次。
	resolver := "ios"
	if s.userInputReg.ConfirmResolved(interactionID, clientActionID, resolver) {
		s.emit(core.Event{
			Type:      core.EventUserInputResolved,
			SessionID: s.CurrentSessionID(),
			UserInput: &core.UserInputInteraction{
				InteractionID:    interactionID,
				Status:           core.UserInputStatusAnswered,
				ResolutionSource: resolver,
			},
		})
	}
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
}

// buildCodexWireAnswers 把规范化 answers 转成 §8.2 wire result.answers map。
// key = backend 原 question id；value.answers 恒 string[]（single/text 恰一元素）。
func buildCodexWireAnswers(snap *registrySnapshot, answers []core.UserInputAnswer) (map[string]codexAnswerEnvelopeQuestion, error) {
	expected := make(map[string]bool, len(snap.QuestionOrder))
	for _, qid := range snap.QuestionOrder {
		expected[qid] = true
	}
	seen := make(map[string]bool, len(answers))
	out := make(map[string]codexAnswerEnvelopeQuestion, len(answers))
	for _, a := range answers {
		qid := a.QuestionID
		if !expected[qid] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown question id"}
		}
		if seen[qid] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "duplicate question id"}
		}
		seen[qid] = true
		labels, err := codexNormalizeAnswerValues(snap, qid, snap.QuestionMode[qid], snap.QuestionCustom[qid], a.Values)
		if err != nil {
			return nil, err
		}
		out[snap.RawQuestionID[qid]] = codexAnswerEnvelopeQuestion{Answers: labels}
	}
	for qid := range expected {
		if !seen[qid] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "missing required question"}
		}
	}
	return out, nil
}

// codexNormalizeAnswerValues 按 mode 校验单题 values 并产出 wire string[]。
// 非空校验用 TrimSpace，但写入的是用户原文（§7）。
func codexNormalizeAnswerValues(snap *registrySnapshot, qid string, mode core.UserInputAnswerMode, allowsCustom bool, values []core.UserInputValue) ([]string, error) {
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
			if !allowsCustom {
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
		if len(values) != 1 {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "text requires exactly one value"}
		}
		v := values[0]
		if v.Kind != core.UserInputValueText {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "text mode requires text value"}
		}
		if strings.TrimSpace(v.Text) == "" {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "empty text"}
		}
		return []string{v.Text}, nil
	default:
		// Codex 不产生 multiple（§0/§9.2）。
		return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unsupported answer mode for codex"}
	}
}

// serverResponseFrame 是写回 app-server 的 JSON-RPC response（id 原样回传，避免 int64 精度丢失）。
type serverResponseFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
}

type serverErrorFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *rpcError       `json:"error"`
}

// respondServerRequest 写回 item/tool/requestUserInput 的成功 response（§8.2 envelope）。
func (s *appServerSession) respondServerRequest(rawID json.RawMessage, result any) error {
	return s.respondServerRequestContext(context.Background(), rawID, result)
}

func (s *appServerSession) respondServerRequestContext(ctx context.Context, rawID json.RawMessage, result any) error {
	frame := serverResponseFrame{JSONRPC: "2.0", ID: rawID, Result: result}
	b, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode server response: %w", err)
	}
	return s.writeMessageContext(ctx, b)
}

// respondServerError 写回 JSON-RPC error response（用于未知 method / 坏 params，避免 server 挂起）。
func (s *appServerSession) respondServerError(rawID json.RawMessage, code int, message string) {
	frame := serverErrorFrame{JSONRPC: "2.0", ID: rawID, Error: &rpcError{Code: code, Message: message}}
	b, err := json.Marshal(frame)
	if err != nil {
		slog.Debug("codex app-server: encode server error response failed", "error", err)
		return
	}
	if err := s.writeMessage(b); err != nil {
		slog.Debug("codex app-server: write server error response failed", "error", err)
	}
}
