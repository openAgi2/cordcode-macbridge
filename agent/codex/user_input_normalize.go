package codex

// user_input_normalize.go 实现 Codex app-server 结构化用户输入的纯归一化与稳定 ID 派生。
// 依据：docs/2026-08-01-codex-claude-structured-user-input-design.md §6.1（ID 派生）与 §8.1（envelope
// 分类 + question 规范化）。全部为纯函数，不触碰 transport / session 状态，便于对 P0 冻结的 schema
// 形态做确定性单测。
//
// 关键不变量（设计已冻结，不得由开发 agent 重选）：
//   - 派生 ID 固定使用小写十六进制 SHA-256 前 32 字符（= 前 16 字节）。
//   - interactionId = "ui_" + sha256("codex\0" + requestIdType + "\0" + requestIdValue +
//     "\0" + threadId + "\0" + turnId + "\0" + itemId)[:32]；requestIdType ∈ {"string","int64"}。
//   - questionId = interactionId + "_q_" + zeroBasedIndex；optionId = questionId + "_o_" + zeroBasedIndex。
//   - options 缺失/null/[] → answerMode=text、allowsCustomAnswer=true；options 非空 → single、
//     allowsCustomAnswer 取 question-level isOther；每题恒 required=true。
//   - envelope 分类按 method/id/result/error 字段组合，不靠“有 id 即 response”。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// suiHexLen 是 interactionId 派生保留的十六进制字符数（= SHA-256 前 16 字节）。
const suiHexLen = 32

// suiInteractionPrefix 是所有结构化用户输入 interactionId 的统一前缀。
const suiInteractionPrefix = "ui_"

// rpcEnvelopeKind 分类 JSON-RPC envelope（设计 §8.1）。
type rpcEnvelopeKind int

const (
	envelopeServerRequest rpcEnvelopeKind = iota // method + id
	envelopeNotification                         // method, no id
	envelopeResponse                             // id + result/error, no method
	envelopeMalformed                            // 其他形态 → explicit protocol diagnostic
)

func (k rpcEnvelopeKind) String() string {
	switch k {
	case envelopeServerRequest:
		return "server_request"
	case envelopeNotification:
		return "notification"
	case envelopeResponse:
		return "response"
	default:
		return "malformed"
	}
}

// classifyRPCEnvelope 按 method/id/result/error 字段组合分类 envelope。
//   - method + id               → server request
//   - method, no id             → notification
//   - id + (result|error), 无 method → response（对 MacBridge client request 的回应）
//   - 其他形态                  → malformed（caller 必须发 explicit protocol diagnostic）
func classifyRPCEnvelope(hasMethod, hasID, hasResult, hasError bool) rpcEnvelopeKind {
	if hasMethod {
		if hasID {
			return envelopeServerRequest
		}
		return envelopeNotification
	}
	if hasID && (hasResult || hasError) {
		return envelopeResponse
	}
	return envelopeMalformed
}

// codexRequestIDType 把原始 JSON-RPC request id 归类为 interactionId 派生所需的稳定 type 标签与
// 规范字符串值。设计 §6.1：requestIdType 固定 "string"|"int64"，避免数字 1 与字符串 "1" 碰撞。
// raw 必须是 JSON 解码后的原生类型（string / float64）。
func codexRequestIDType(raw any) (typed, canonical string, ok bool) {
	switch v := raw.(type) {
	case string:
		return "string", v, true
	case float64:
		// JSON number 解码为 float64。int64 表达；用整数规范形式，避免 1.0 与 1 漂移。
		if v == float64(int64(v)) {
			return "int64", fmt.Sprintf("%d", int64(v)), true
		}
		return "int64", fmt.Sprintf("%g", v), true
	case int64:
		return "int64", fmt.Sprintf("%d", v), true
	case int:
		return "int64", fmt.Sprintf("%d", v), true
	default:
		return "", "", false
	}
}

// deriveCodexInteractionID 按 §6.1 派生 Codex interactionId。
// 各字段以 NUL 分隔，避免相邻字段拼接产生跨字段碰撞。
func deriveCodexInteractionID(requestIDType, requestIDValue, threadID, turnID, itemID string) string {
	h := sha256.New()
	h.Write([]byte("codex\x00"))
	h.Write([]byte(requestIDType))
	h.Write([]byte("\x00"))
	h.Write([]byte(requestIDValue))
	h.Write([]byte("\x00"))
	h.Write([]byte(threadID))
	h.Write([]byte("\x00"))
	h.Write([]byte(turnID))
	h.Write([]byte("\x00"))
	h.Write([]byte(itemID))
	sum := h.Sum(nil)
	return suiInteractionPrefix + hex.EncodeToString(sum[:suiHexLen/2])
}

// deriveClaudeInteractionID 按 §6.1 派生 Claude interactionId（claudecode\0 + requestId）。
// 放在本文件便于跨 adapter 共享 SHA 截断语义；Claude adapter 在 P2 使用。
func deriveClaudeInteractionID(requestID string) string {
	h := sha256.New()
	h.Write([]byte("claudecode\x00"))
	h.Write([]byte(requestID))
	sum := h.Sum(nil)
	return suiInteractionPrefix + hex.EncodeToString(sum[:suiHexLen/2])
}

// deriveQuestionID = interactionId + "_q_" + zeroBasedIndex。
func deriveQuestionID(interactionID string, questionIndex int) string {
	return fmt.Sprintf("%s_q_%d", interactionID, questionIndex)
}

// deriveOptionID = questionId + "_o_" + zeroBasedIndex。
func deriveOptionID(questionID string, optionIndex int) string {
	return fmt.Sprintf("%s_o_%d", questionID, optionIndex)
}

// codexRawOption 是 item/tool/requestUserInput params.questions[].options[] 的最小解析结构。
// option 无 id（schema 已证），按 index 派生。
type codexRawOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// codexRawQuestion 是 params.questions[] 的最小解析结构。
// IsOther/IsSecret 用指针区分“缺失”与“显式 false”。Options 为 nil/空均等价于 text 题。
type codexRawQuestion struct {
	ID       string           `json:"id"`
	Header   string           `json:"header"`
	Question string           `json:"question"`
	IsOther  *bool            `json:"isOther"`
	IsSecret *bool            `json:"isSecret"`
	Options  []codexRawOption `json:"options"`
}

// normalizeCodexQuestions 把 raw params.questions 规范化为 bridge domain questions（设计 §8.1 step 3）。
// interactionID 必须已按 §6.1 派生；questionId/optionId 由本函数按 index 继续派生。
func normalizeCodexQuestions(interactionID string, raw []codexRawQuestion) []core.UserInputQuestion {
	out := make([]core.UserInputQuestion, 0, len(raw))
	for i, q := range raw {
		qid := deriveQuestionID(interactionID, i)
		isSecret := false
		if q.IsSecret != nil {
			isSecret = *q.IsSecret
		}
		if len(q.Options) == 0 {
			// options 缺失/null/[] → text mode、allowsCustomAnswer=true、不渲染 option row。
			out = append(out, core.UserInputQuestion{
				ID:                 qid,
				Header:             q.Header,
				Prompt:             q.Question,
				AnswerMode:         core.UserInputAnswerModeText,
				Options:            nil,
				AllowsCustomAnswer: true,
				IsSecret:           isSecret,
				Required:           true,
			})
			continue
		}
		isOther := false
		if q.IsOther != nil {
			isOther = *q.IsOther
		}
		opts := make([]core.UserInputOption, 0, len(q.Options))
		for j, o := range q.Options {
			opts = append(opts, core.UserInputOption{
				ID:          deriveOptionID(qid, j),
				Label:       o.Label,
				Description: o.Description,
			})
		}
		out = append(out, core.UserInputQuestion{
			ID:                 qid,
			Header:             q.Header,
			Prompt:             q.Question,
			AnswerMode:         core.UserInputAnswerModeSingle,
			Options:            opts,
			AllowsCustomAnswer: isOther,
			IsSecret:           isSecret,
			Required:           true,
		})
	}
	return out
}

// codexAnswerEnvelopeQuestion 是写回 app-server 的 result.answers map value。
// 设计 §8.2：每题 answers 恒为 string[]；single/text 都恰好写一个元素。
type codexAnswerEnvelopeQuestion struct {
	Answers []string `json:"answers"`
}
