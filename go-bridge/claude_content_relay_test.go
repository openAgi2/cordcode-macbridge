package gobridge

import (
	"os"
	"strings"
	"testing"
)

// TestScanClaudeRelayEntriesFromReaderExtractsBlocks：扫描返回所有 meaningful 记录，
// assistant 的 content block（thinking/text/tool_use）可由 claudeRelayContentBlocks 提取。
func TestScanClaudeRelayEntriesFromReaderExtractsBlocks(t *testing.T) {
	content := `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"t"},{"type":"text","text":"answer"},{"type":"tool_use","name":"Bash","input":{"command":"pwd"},"id":"toolu_x"}]}}` + "\n"
	entries, err := scanClaudeRelayEntriesFromReader(strings.NewReader(content))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2 (user + assistant)", len(entries))
	}
	if entries[0].Type != "user" || entries[1].Type != "assistant" {
		t.Fatalf("entry types = %v, want [user, assistant]", []string{entries[0].Type, entries[1].Type})
	}
	blocks := claudeRelayContentBlocks(entries[1].Message.Content)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3 (thinking/text/tool_use)", len(blocks))
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "t" {
		t.Fatalf("blocks[0] = %+v, want thinking 't'", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "answer" {
		t.Fatalf("blocks[1] = %+v, want text 'answer'", blocks[1])
	}
	if blocks[2].Type != "tool_use" || blocks[2].Name != "Bash" || blocks[2].ID != "toolu_x" {
		t.Fatalf("blocks[2] = %+v, want tool_use Bash toolu_x", blocks[2])
	}
}

// TestClaudeFileRelayEmitsAssistantContentBlocks：Phase 1 loop 级验证——外部 Claude turn
// 进行中按 content block 发 reasoning_delta/thinking、text_delta、tool_started，end_turn→turn_completed。
func TestClaudeFileRelayEmitsAssistantContentBlocks(t *testing.T) {
	home, err := os.MkdirTemp("", "claude-content")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	const sessionID = "claude-content-blocks"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","message":{"role":"user","content":"external prompt"}}`,
	)
	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)
	_ = client.readEvents(t, 1) // startup: turn_started

	// 非 final assistant：thinking + text + tool_use → reasoning_delta, text_delta, tool_started
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"planning"},{"type":"text","text":"hello"},{"type":"tool_use","name":"Bash","input":{"command":"ls"},"id":"toolu_1"}]}}`,
	)
	msgs := client.readEvents(t, 3)
	if msgs[0]["event"] != "reasoning_delta" || msgs[1]["event"] != "text_delta" || msgs[2]["event"] != "tool_started" {
		t.Fatalf("non-final assistant events = %v, want [reasoning_delta, text_delta, tool_started]", msgs)
	}
	toolPayload, ok := msgs[2]["data"].(map[string]any)
	if !ok || toolPayload["toolName"] != "Bash" || toolPayload["itemId"] != "toolu_1" {
		t.Fatalf("tool_started payload = %+v, want toolName=Bash itemId=toolu_1", toolPayload)
	}

	// final assistant end_turn：text_delta("done") + turn_completed + idle
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	)
	msgs = client.readEvents(t, 3)
	if msgs[0]["event"] != "text_delta" || msgs[1]["event"] != "turn_completed" || msgs[2]["event"] != "session_state_changed" {
		t.Fatalf("final assistant events = %v, want [text_delta, turn_completed, session_state_changed]", msgs)
	}
}
