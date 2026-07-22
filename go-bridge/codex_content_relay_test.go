package gobridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCodexRolloutFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return path
}

// TestScanCodexTranscriptRelayEventsLegacy：纯 Legacy session——内容只在 event_msg。
func TestScanCodexTranscriptRelayEventsLegacy(t *testing.T) {
	path := writeCodexRolloutFile(t,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"hello world"}}`,
		`{"type":"event_msg","payload":{"type":"agent_reasoning","text":"planning the fix"}}`,
	)
	events := scanCodexTranscriptRelayEvents(path, 0)
	if len(events) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(events), events)
	}
	if events[0].kind != "text" || events[0].text != "hello world" {
		t.Fatalf("events[0] = %+v, want text 'hello world'", events[0])
	}
	if events[1].kind != "reasoning" || events[1].text != "planning the fix" {
		t.Fatalf("events[1] = %+v, want reasoning 'planning the fix'", events[1])
	}
}

// TestScanCodexTranscriptRelayEventsPaginated：纯 Paginated session——内容只在
// response_item；空 summary（仅 encrypted_content 无明文）必须被跳过。
func TestScanCodexTranscriptRelayEventsPaginated(t *testing.T) {
	path := writeCodexRolloutFile(t,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"paginated body"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"paginated thought"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[],"encrypted_content":"opaque"}}`,
	)
	events := scanCodexTranscriptRelayEvents(path, 0)
	// 期望 text + reasoning 各一条；空 summary 不产事件（shape-agnostic 跳过）。
	if len(events) != 2 {
		t.Fatalf("len = %d, want 2 (empty summary skipped): %+v", len(events), events)
	}
	if events[0].kind != "text" || events[0].text != "paginated body" {
		t.Fatalf("events[0] = %+v, want text 'paginated body'", events[0])
	}
	if events[1].kind != "reasoning" || events[1].text != "paginated thought" {
		t.Fatalf("events[1] = %+v, want reasoning 'paginated thought'", events[1])
	}
}

func TestScanCodexTranscriptRelayEventsUserMessage(t *testing.T) {
	path := writeCodexRolloutFile(t,
		`{"type":"response_item","payload":{"type":"message","id":"msg-user-1","role":"user","content":[{"type":"input_text","text":"测试第五轮\n"}]}}`,
	)
	events := scanCodexTranscriptRelayEvents(path, 0)
	if len(events) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(events), events)
	}
	if events[0].kind != "user_message" || events[0].itemId != "msg-user-1" || events[0].text != "测试第五轮\n" {
		t.Fatalf("event = %+v, want stable user_message", events[0])
	}
}

func TestCodexFileRelayEmitsUserMessageWithTurnIdentity(t *testing.T) {
	const sessionID = "user-message"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	_ = readEventNames(t, client, 2)

	appendCodexRollout(t, agent.transcriptPath,
		`{"type":"response_item","payload":{"type":"message","id":"msg-user-1","role":"user","content":[{"type":"input_text","text":"测试第五轮\n"}]}}`,
	)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := client.ReadJSON(&payload); err != nil {
		t.Fatal(err)
	}
	data, _ := payload["data"].(map[string]interface{})
	if payload["event"] != "user_message" || data["itemId"] != "msg-user-1" || data["turnId"] != "turn-1" || data["text"] != "测试第五轮\n" {
		t.Fatalf("payload = %#v, want user_message with stable message/turn ids", payload)
	}

	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_complete"))
	_ = readEventNames(t, client, 2)
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}

// TestScanCodexTranscriptRelayEventsDoubleWrite：双写过渡态 + 多元素 + 跨记录重复。
// 扫描器返回所有候选（不去重），验证元素口径提取 + 空 summary 跳过 + 多元素展开。
func TestScanCodexTranscriptRelayEventsDoubleWrite(t *testing.T) {
	path := writeCodexRolloutFile(t,
		// message 双写：event_msg + response_item 同文本
		`{"type":"event_msg","payload":{"type":"agent_message","message":"dup-msg"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"dup-msg"}]}}`,
		// reasoning 双写 + 空 summary + 多元素 + 跨记录重复
		`{"type":"event_msg","payload":{"type":"agent_reasoning","text":"r1"}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"r1"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"a"},{"type":"summary_text","text":"b"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"a"}]}}`,
	)
	events := scanCodexTranscriptRelayEvents(path, 0)
	var texts, reasonings []string
	for _, ev := range events {
		switch ev.kind {
		case "text":
			texts = append(texts, ev.text)
		case "reasoning":
			reasonings = append(reasonings, ev.text)
		}
	}
	// 扫描器不去重：2 text（双写同文本）+ 5 reasoning（r1,r1,a,b,a），空 summary 跳过。
	if len(texts) != 2 || texts[0] != "dup-msg" || texts[1] != "dup-msg" {
		t.Fatalf("texts = %v, want 2x 'dup-msg'", texts)
	}
	wantReasonings := []string{"r1", "r1", "a", "b", "a"}
	if len(reasonings) != len(wantReasonings) {
		t.Fatalf("reasonings = %v, want %v", reasonings, wantReasonings)
	}
	for i := range reasonings {
		if reasonings[i] != wantReasonings[i] {
			t.Fatalf("reasonings[%d] = %q, want %q (full: %v)", i, reasonings[i], wantReasonings[i], reasonings)
		}
	}
}

// TestCodexFileRelayDoubleWriteDedupesContentDeltas：loop 级验证 per-turn seen-set 去重。
// 双写 + 多元素 + 跨记录重复的 turn，客户端应只收到 1 text_delta + 3 reasoning_delta。
func TestCodexFileRelayDoubleWriteDedupesContentDeltas(t *testing.T) {
	const sessionID = "double-write-dedup"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	_ = readEventNames(t, client, 2) // running startup: turn_started + session_state_changed

	appendCodexRollout(t, agent.transcriptPath,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"dup-msg"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"dup-msg"}]}}`,
		`{"type":"event_msg","payload":{"type":"agent_reasoning","text":"r1"}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"r1"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"a"},{"type":"summary_text","text":"b"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"a"}]}}`,
	)
	events := readEventNames(t, client, 4) // 期望去重后 1 text_delta + 3 reasoning_delta
	textCount, reasoningCount := 0, 0
	for _, e := range events {
		switch e {
		case "text_delta":
			textCount++
		case "reasoning_delta":
			reasoningCount++
		}
	}
	if textCount != 1 || reasoningCount != 3 {
		t.Fatalf("events = %v, want 1 text_delta + 3 reasoning_delta (got %d text, %d reasoning)", events, textCount, reasoningCount)
	}

	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_complete"))
	_ = readEventNames(t, client, 2)
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}

// TestScanCodexTranscriptRelayEventsToolsAndTokens：tool 生命周期 + token_count 解析。
// custom_tool_call→tool_started（exec-unified name + input JS 串 + call_id），
// custom_tool_call_output→tool_finished（output[] 拼接），token_count→context_usage。
func TestScanCodexTranscriptRelayEventsToolsAndTokens(t *testing.T) {
	path := writeCodexRolloutFile(t,
		`{"type":"response_item","payload":{"type":"custom_tool_call","call_id":"call_x","name":"exec","input":"tools.exec_command({cmd:'ls'})"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_x","output":[{"type":"input_text","text":"file1\n"},{"type":"input_text","text":"file2"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":10,"output_tokens":5,"reasoning_output_tokens":2,"total_tokens":105},"model_context_window":200000}}}`,
	)
	events := scanCodexTranscriptRelayEvents(path, 0)
	if len(events) != 3 {
		t.Fatalf("len = %d, want 3 (tool_started, tool_finished, context_usage): %+v", len(events), events)
	}
	if events[0].kind != "tool_started" || events[0].toolName != "exec" || events[0].itemId != "call_x" || events[0].toolInput == "" {
		t.Fatalf("events[0] = %+v, want tool_started exec call_x", events[0])
	}
	if events[1].kind != "tool_finished" || events[1].itemId != "call_x" || events[1].toolResult != "file1\nfile2" {
		t.Fatalf("events[1] = %+v, want tool_finished call_x with concatenated output", events[1])
	}
	if events[2].kind != "context_usage" {
		t.Fatalf("events[2] = %+v, want context_usage", events[2])
	}
	ctx := events[2].context
	if ctx["totalTokens"] != 105 || ctx["inputTokens"] != 100 || ctx["contextWindow"] != 200000 || ctx["cachedInputTokens"] != 10 {
		t.Fatalf("context = %+v, want totalTokens=105 inputTokens=100 contextWindow=200000 cached=10", ctx)
	}
}

// TestCodexFileRelayEmitsToolLifecycleAndContextUsage：loop 级验证外部 Codex turn
// 发出 tool_started/tool_finished/context_usage_updated（运行状态条 token + 工具显示）。
func TestCodexFileRelayEmitsToolLifecycleAndContextUsage(t *testing.T) {
	const sessionID = "tools-tokens"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	_ = readEventNames(t, client, 2) // running startup: turn_started + session_state_changed

	appendCodexRollout(t, agent.transcriptPath,
		`{"type":"response_item","payload":{"type":"custom_tool_call","call_id":"call_x","name":"exec","input":"ls"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_x","output":[{"type":"input_text","text":"out"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"total_tokens":105},"model_context_window":200000}}}`,
	)
	events := readEventNames(t, client, 3)
	if events[0] != "tool_started" || events[1] != "tool_finished" || events[2] != "context_usage_updated" {
		t.Fatalf("events = %v, want [tool_started, tool_finished, context_usage_updated]", events)
	}

	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_complete"))
	_ = readEventNames(t, client, 2)
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}
