package dshweb

import (
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// mkHistoryEntry builds one apiHistoryEntry from a raw data payload.
func mkHistoryEntry(typ string, seq int64, data string) apiHistoryEntry {
	return apiHistoryEntry{
		Event: sessionEventWire{
			Type: typ,
			Seq:  seq,
			Time: 1789473149000,
			Data: json.RawMessage(data),
		},
	}
}

// askQuestionEvents builds a minimal journal slice: turn 5 with one
// ask_user_question tool call; withResult controls whether the call already
// carries its tool/result (answered) or not (pending).
func askQuestionEvents(withResult bool) []apiHistoryEntry {
	evs := []apiHistoryEntry{
		mkHistoryEntry("turn/start", 100, `{"turn": 5}`),
		mkHistoryEntry("assistant/message", 101, `{
			"turn": 5, "step": 2,
			"message": {"role": "assistant", "content": [
				{"type": "text", "text": "再次弹出选择框："},
				{"type": "tool-call", "id": "call_9ec02aa7", "name": "ask_user_question",
				 "arguments": "{\"questions\":[{\"header\":\"文件处理\",\"id\":\"file_handling_v4\",\"multi_select\":false,\"options\":[{\"description\":\"不动现有文件\",\"label\":\"另建新文件\"},{\"description\":\"丢弃现有 5 篇故事\",\"label\":\"覆盖原文件\"}],\"question\":\"漫威战斗故事.txt 已存在且含 5 篇故事，这次你想怎么处理？\"}]}"}
			]}
		}`),
	}
	if withResult {
		evs = append(evs,
			mkHistoryEntry("tool/result", 102, `{
				"turn": 5, "step": 2,
				"message": {"source": {"kind": "tool", "callId": "call_9ec02aa7"},
				 "content": [{"type": "tool-result", "toolCallId": "call_9ec02aa7",
				  "content": [{"type": "text", "text": "另建新文件"}]}]}
			}`),
		)
	}
	return append(evs, mkHistoryEventTurnEnd())
}

func mkHistoryEventTurnEnd() apiHistoryEntry {
	return mkHistoryEntry("turn/end", 200, `{"turn": 5}`)
}

func userInputPartsOf(entries []core.RichHistoryEntry) []map[string]any {
	var parts []map[string]any
	for _, entry := range entries {
		for _, p := range entry.Parts {
			if p["type"] == "user_input" {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

func toolPartsOf(entries []core.RichHistoryEntry) []map[string]any {
	var parts []map[string]any
	for _, entry := range entries {
		for _, p := range entry.Parts {
			if p["type"] == "tool" {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

// TestAskUserQuestionColdFoldPending：pending（journal 无 tool/result）的
// ask_user_question 冷拉折成 user_input part（status=pending、canRespond=true、
// 每题一 part、interactionId=dsh 题 id），**不出工具 step**——live codec 对
// tool-call 块本就不发工具事件（2026-09-15 owner 真机：重开后弹出卡被冷快照
// 收起的根因修复）。
func TestAskUserQuestionColdFoldPending(t *testing.T) {
	entries := mapHistoryEvents("session-4f09", askQuestionEvents(false))

	ui := userInputPartsOf(entries)
	if len(ui) != 1 {
		t.Fatalf("user_input parts = %d, want 1", len(ui))
	}
	p := ui[0]
	if p["interactionId"] != "file_handling_v4" {
		t.Fatalf("interactionId = %v, want dsh question id", p["interactionId"])
	}
	if p["status"] != "pending" || p["canRespond"] != true || p["canReject"] != true {
		t.Fatalf("pending part flags = %v", p)
	}
	questions, ok := p["questions"].([]map[string]any)
	if !ok || len(questions) != 1 {
		t.Fatalf("questions malformed: %+v", p["questions"])
	}
	q := questions[0]
	if q["id"] != "file_handling_v4" || q["header"] != "文件处理" ||
		q["prompt"] != "漫威战斗故事.txt 已存在且含 5 篇故事，这次你想怎么处理？" ||
		q["answerMode"] != "single" || q["allowsCustomAnswer"] != true || q["required"] != true {
		t.Fatalf("question = %+v", q)
	}
	opts, ok := q["options"].([]map[string]any)
	if !ok || len(opts) != 2 {
		t.Fatalf("options malformed: %+v", q["options"])
	}
	if opts[0]["id"] != "另建新文件" || opts[0]["label"] != "另建新文件" ||
		opts[0]["description"] != "不动现有文件" {
		t.Fatalf("options[0] = %+v（dsh 无 option id：label 即标识）", opts[0])
	}
	if tool := toolPartsOf(entries); len(tool) != 0 {
		t.Fatalf("pending ask_user_question 不得出工具 step（live 同形），got %d", len(tool))
	}
}

// TestAskUserQuestionColdFoldAnswered：journal 已带 tool/result（用户已作答，
// harness 把结果回灌模型）→ 工具 step 照旧（completed + result 输出，live 由
// tool/result 补全的同形）+ 终态 user_input part（answered、不可再答）。
func TestAskUserQuestionColdFoldAnswered(t *testing.T) {
	entries := mapHistoryEvents("session-4f09", askQuestionEvents(true))

	ui := userInputPartsOf(entries)
	if len(ui) != 1 {
		t.Fatalf("user_input parts = %d, want 1", len(ui))
	}
	if ui[0]["status"] != "answered" || ui[0]["canRespond"] != false {
		t.Fatalf("answered part = %+v", ui[0])
	}
	tool := toolPartsOf(entries)
	if len(tool) != 1 {
		t.Fatalf("answered ask_user_question 应保留工具 step，got %d", len(tool))
	}
	step, ok := tool[0]["step"].(map[string]any)
	if !ok || step["status"] != "completed" || step["toolName"] != "ask_user_question" {
		t.Fatalf("tool step = %+v", tool[0]["step"])
	}
}

// TestAskUserQuestionColdFoldMalformedFailsClosed：arguments 不可解析时落回
// 普通工具卡（不造半张问答卡）。
func TestAskUserQuestionColdFoldMalformedFailsClosed(t *testing.T) {
	evs := []apiHistoryEntry{
		mkHistoryEntry("turn/start", 100, `{"turn": 5}`),
		mkHistoryEntry("assistant/message", 101, `{
			"turn": 5, "step": 2,
			"message": {"role": "assistant", "content": [
				{"type": "tool-call", "id": "call_bad", "name": "ask_user_question", "arguments": "{not json"}
			]}
		}`),
		mkHistoryEventTurnEnd(),
	}
	entries := mapHistoryEvents("session-4f09", evs)

	if ui := userInputPartsOf(entries); len(ui) != 0 {
		t.Fatalf("malformed arguments 不得造 user_input part，got %d", len(ui))
	}
	if tool := toolPartsOf(entries); len(tool) != 1 {
		t.Fatalf("fail closed：应落回普通工具卡，got %d", len(tool))
	}
}
