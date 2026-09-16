package gobridge

// claude_user_input_oracle_test.go 锁定 AskUserQuestion 可答性 oracle 的调用边语义
//（设计 v6 §4.2）：
//  1. oracle 命中（registry pending + session alive + 证据门）→ mapper 输出 canRespond=true；
//  2. oracle nil / miss / 证据门未翻转 → observe_only fail closed；
//  3. pathless rich history 边的 part 级 oracle 应用；
//  4. sidechain/trace/push-preview 调用边默认 nil（不进入 Kernel 的面不得推断可答）。

import (
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// askUserQuestionAssistantEntry 构造一个带 Ask tool_use 的 assistant transcript 行
//（JSON 反序列化，与 mapper 实际输入同形状）。
func askUserQuestionAssistantEntry(t *testing.T, toolUseID string) claudeTranscriptRelayEntry {
	t.Helper()
	raw := `{
		"type": "assistant",
		"uuid": "asst-1",
		"message": {
			"id": "msg-1",
			"role": "assistant",
			"content": [
				{
					"type": "tool_use",
					"name": "AskUserQuestion",
					"id": "` + toolUseID + `",
					"input": {"questions":[{"question":"Pick","header":"Choice","multiSelect":false,
						"options":[{"label":"a","description":"A"},{"label":"b","description":"B"}]}]}
				}
			]
		}
	}`
	var e claudeTranscriptRelayEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("unmarshal ask entry: %v", err)
	}
	return e
}

// findAskRequestedEvent 在 mapper 输出里找 user_input_requested 事件。
func findAskRequestedEvent(evs []projectionHydrateEvent) *projectionHydrateEvent {
	for i := range evs {
		if evs[i].Event == "user_input_requested" {
			return &evs[i]
		}
	}
	return nil
}

// TestClaudeMapperOracleHitFlipsCanRespond：oracle 命中时 mapper 的 user_input_requested
// 携带 canRespond/canReject=true 且无 observe_only 诊断码。
func TestClaudeMapperOracleHitFlipsCanRespond(t *testing.T) {
	toolUseID := "toolu_oracle-1"
	interactionID := claudecode.DeriveStructuredUserInputInteractionID(toolUseID)
	oracle := claudeAnswerabilityOracle(func(id string) bool { return id == interactionID })

	currentTurnID := "turn-1"
	evs := claudeEntryToProjectionEvents(askUserQuestionAssistantEntry(t, toolUseID), &currentTurnID, nil, oracle)
	ev := findAskRequestedEvent(evs)
	if ev == nil {
		t.Fatal("expected user_input_requested event")
	}
	if ev.Data["interactionId"] != interactionID {
		t.Fatalf("interactionId=%v want %q", ev.Data["interactionId"], interactionID)
	}
	if ev.Data["canRespond"] != true || ev.Data["canReject"] != true {
		t.Fatalf("oracle hit: canRespond/canReject 应为 true，实际 %v/%v", ev.Data["canRespond"], ev.Data["canReject"])
	}
	if _, has := ev.Data["diagnosticCode"]; has {
		t.Fatalf("oracle hit 不应携带诊断码，实际 %q", ev.Data["diagnosticCode"])
	}
}

// TestClaudeMapperNilOracleStaysObserveOnly：nil oracle（sidechain/trace/push-preview/测试）
// 与 oracle miss（registry 无该 interaction）都保持 observe_only。
func TestClaudeMapperNilOracleStaysObserveOnly(t *testing.T) {
	toolUseID := "toolu_oracle-2"

	currentTurnID := "turn-1"
	evs := claudeEntryToProjectionEvents(askUserQuestionAssistantEntry(t, toolUseID), &currentTurnID, nil, nil)
	ev := findAskRequestedEvent(evs)
	if ev == nil {
		t.Fatal("expected user_input_requested event")
	}
	if ev.Data["canRespond"] != false || ev.Data["canReject"] != false {
		t.Fatalf("nil oracle: canRespond/canReject 应为 false，实际 %v/%v", ev.Data["canRespond"], ev.Data["canReject"])
	}
	if ev.Data["diagnosticCode"] != "observe_only" {
		t.Fatalf("nil oracle 诊断码应为 observe_only，实际 %q", ev.Data["diagnosticCode"])
	}

	miss := claudeAnswerabilityOracle(func(string) bool { return false })
	evs = claudeEntryToProjectionEvents(askUserQuestionAssistantEntry(t, toolUseID), &currentTurnID, nil, miss)
	ev = findAskRequestedEvent(evs)
	if ev.Data["canRespond"] != false || ev.Data["diagnosticCode"] != "observe_only" {
		t.Fatalf("oracle miss 应保持 observe_only，实际 canRespond=%v diag=%q", ev.Data["canRespond"], ev.Data["diagnosticCode"])
	}
}

// TestClaudeRichHistoryOracleApplication：pathless rich history 边的 part 级应用——
// pending part 命中 oracle 翻 canRespond；非 pending part 不动；nil oracle 不动。
func TestClaudeRichHistoryOracleApplication(t *testing.T) {
	interactionID := "ui_rich_1"
	entries := []core.RichHistoryEntry{
		{
			ID: "e1", Role: "assistant",
			Parts: []map[string]any{
				{
					"type":           "user_input",
					"itemId":         "toolu_rich_1",
					"interactionId":  interactionID,
					"status":         "pending",
					"canRespond":     false,
					"canReject":      false,
					"diagnosticCode": "observe_only",
				},
				{
					"type":          "user_input",
					"itemId":        "toolu_rich_2",
					"interactionId": "ui_rich_2",
					"status":        "answered",
					"canRespond":   false,
				},
			},
		},
	}

	// nil oracle：全部保持 observe_only。
	applyClaudeUserInputOracle(entries, nil)
	if entries[0].Parts[0]["canRespond"] != false || entries[0].Parts[0]["diagnosticCode"] != "observe_only" {
		t.Fatal("nil oracle 不得修改 pending part")
	}

	// 命中 oracle：pending part 翻 true 清诊断码；answered part 不动。
	oracle := claudeAnswerabilityOracle(func(id string) bool { return id == interactionID })
	applyClaudeUserInputOracle(entries, oracle)
	if entries[0].Parts[0]["canRespond"] != true || entries[0].Parts[0]["canReject"] != true {
		t.Fatalf("oracle 命中应翻 canRespond/canReject，实际 %v/%v", entries[0].Parts[0]["canRespond"], entries[0].Parts[0]["canReject"])
	}
	if _, has := entries[0].Parts[0]["diagnosticCode"]; has {
		t.Fatalf("oracle 命中应清诊断码，实际 %q", entries[0].Parts[0]["diagnosticCode"])
	}
	if entries[0].Parts[1]["canRespond"] != false {
		t.Fatal("非 pending part 不得被 oracle 翻转")
	}
}

// TestClaudeUserInputOracleResolverFailClosed：resolver 对非 Claude backend、无 session、
// 不实现接口的 session 都返回 nil（fail closed）。
func TestClaudeUserInputOracleResolverFailClosed(t *testing.T) {
	h := newTestHandlers(t)
	if oracle := h.claudeUserInputOracle("codex", "s1"); oracle != nil {
		t.Fatal("非 Claude backend 应返回 nil oracle")
	}
	if oracle := h.claudeUserInputOracle("claude", ""); oracle != nil {
		t.Fatal("空 sessionID 应返回 nil oracle")
	}
	if oracle := h.claudeUserInputOracle("claude", "no-such-session"); oracle != nil {
		t.Fatal("无 session 应返回 nil oracle")
	}
}

