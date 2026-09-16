package gobridge

// claude_user_input_oracle.go 实现 Claude AskUserQuestion 可答性 oracle（设计 v6 §4.2）。
//
// oracle 只在进入主 Kernel 的调用边传真实实现：冷拉 RangeSeed、live source batch、
// hydrate 期 legacy row、pathless rich history。sidechain child reducer、push preview、
// trace、测试默认传 nil（fail closed → observe_only）。
//
// oracle 查询 tool-use 派生的 registry key（与 transcript mapper 同键），要求 entry
// pending + session alive + 证据门已翻转；任一不满足即 false。可答性只由可证明的活
// 控制通道决定，不从 transcript 来源或 registry miss 推断（owner 2026-08-31 裁决边界）。
//
// agent-owned assistant 行继续整行 cursor-only（handlers_relay.go 的
// stdout_owns_assistant_content 跳过），防止 file relay 与 stdout 双发 requested。

import "github.com/openAgi2/cordcode-macbridge/core"

// claudeAnswerabilityOracle reports whether a tool-use-derived interaction is currently
// answerable through a live bridge-held control channel. nil = fail closed.
type claudeAnswerabilityOracle func(interactionID string) bool

// claudeUserInputOracle resolves the real oracle for a (backendID, sessionID) pair from
// the live session registry. Returns nil when the session is absent or does not implement
// core.UserInputAnswerabilityOracle — callers must treat nil as observe-only.
func (h *Handlers) claudeUserInputOracle(backendID, sessionID string) claudeAnswerabilityOracle {
	if h == nil || sessionID == "" {
		return nil
	}
	if backendID != "claude" && backendID != "claudecode" {
		return nil
	}
	sess, ok := h.getSession(sessionID)
	if !ok {
		return nil
	}
	oracle, ok := sess.(core.UserInputAnswerabilityOracle)
	if !ok {
		return nil
	}
	return oracle.UserInputAnswerable
}

// applyClaudeUserInputOracle mutates the user_input parts of freshly built rich-history
// entries so the pathless rich-history call edge honors the same answerability oracle as
// the live requested path (design v6 §4.2). Entries are freshly built per GetRichSessionHistory
// call, so in-place mutation cannot leak across callers. A nil oracle leaves everything
// observe-only (fail closed).
func applyClaudeUserInputOracle(entries []core.RichHistoryEntry, oracle claudeAnswerabilityOracle) {
	if oracle == nil {
		return
	}
	for _, entry := range entries {
		for _, part := range entry.Parts {
			if part["type"] != "user_input" {
				continue
			}
			interactionID, _ := part["interactionId"].(string)
			if interactionID == "" {
				continue
			}
			status, _ := part["status"].(string)
			if status != "pending" {
				continue
			}
			if oracle(interactionID) {
				part["canRespond"] = true
				part["canReject"] = true
				delete(part, "diagnosticCode")
			}
		}
	}
}
