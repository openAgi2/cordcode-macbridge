package grokbuild

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRichHistoryUsesDurableUpdatesAcrossCompaction(t *testing.T) {
	home := t.TempDir()
	sessionID := "compacted-display-history"
	dir := filepath.Join(home, "sessions", url.PathEscape("/tmp/project"), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// This is the upstream post-/compact shape: chat_history is a model-context
	// cache containing only surviving context, while updates keeps the original
	// display transcript.
	if err := os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte(
		`{"type":"user","content":[{"type":"text","text":"surviving model context only"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	goal := "write four real stories"
	rows := []map[string]any{
		displayJournalRow("session/update", sessionID, "u-1", map[string]any{
			"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "old prompt"},
		}),
		displayJournalRow("session/update", sessionID, "a-1", map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "first real reply"},
		}),
		displayJournalRow("session/update", sessionID, "a-2", map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "final real reply"},
		}),
		displayJournalRow("_x.ai/session/update", sessionID, "done-1", map[string]any{
			"sessionUpdate": "turn_completed", "prompt_id": "turn-1", "stop_reason": "end_turn",
		}),
		displayJournalRow("_x.ai/session/update", sessionID, "goal-1", map[string]any{
			"sessionUpdate": "goal_updated", "goal_id": "run-1", "objective": goal, "status": "completed",
		}),
		displayJournalRow("_x.ai/session/update", sessionID, "worker-1", map[string]any{
			"sessionUpdate": "subagent_spawned", "subagent_id": "worker-1", "child_session_id": "child-1",
			"description": "write the first story", "parent_prompt_id": "turn-2",
		}),
		displayJournalRow("session/update", sessionID, "u-2", map[string]any{
			"sessionUpdate": "user_message_chunk",
			"content":       map[string]any{"type": "text", "text": "<system-reminder>\nA goal has been set: " + goal + "\n\nYou are working directly on this goal across multiple turns."},
		}),
		displayJournalRow("session/update", sessionID, "a-3", map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "the delivered story content"},
		}),
		displayJournalRow("_x.ai/session/update", sessionID, "checkpoint", map[string]any{
			"sessionUpdate": "compaction_checkpoint", "checkpoint_id": "cp-1",
		}),
		displayJournalRow("session/update", sessionID, "host-u", map[string]any{
			"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "/compact"},
			"_meta": map[string]any{"hostTurn": true},
		}),
		displayJournalRow("session/update", sessionID, "host-a", map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Compacted."},
			"_meta": map[string]any{"hostTurn": true},
		}),
	}
	var journal strings.Builder
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		journal.Write(line)
		journal.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(journal.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readRichSessionHistory(home, sessionID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("entries=%d, want user/reply/goal/reply: %+v", len(got), got)
	}
	if got[0].ID != "u-1" || got[0].Role != "user" || got[0].Content != "old prompt" {
		t.Fatalf("original user turn not restored: %+v", got[0])
	}
	if got[1].ID != "a-1" || got[1].Role != "assistant" ||
		got[1].Content != "first real replyfinal real reply" || len(got[1].Parts) != 2 {
		t.Fatalf("original assistant reply not restored: %+v", got[1])
	}
	if got[2].Role != "system" || got[2].Parts[0]["name"] != "goal" || got[2].Parts[0]["args"] != goal {
		t.Fatalf("goal command not decoded from official reminder: %+v", got[2])
	}
	if got[3].Content != "the delivered story content" || got[3].Parts[0]["type"] != "workflow" {
		t.Fatalf("goal reply/workflow not restored together: %+v", got[3])
	}
	for _, entry := range got {
		if strings.Contains(entry.Content, "model context") || strings.Contains(entry.Content, "/compact") || strings.Contains(entry.Content, "Compacted") {
			t.Fatalf("cache/host-turn content leaked into display history: %+v", entry)
		}
	}
}

func TestDisplayHistoryMergesContiguousUserChunksWithStableSourceID(t *testing.T) {
	dir := t.TempDir()
	sessionID := "chunked-user"
	rows := []map[string]any{
		displayJournalRow("_x.ai/session/update", sessionID, "checkpoint", map[string]any{
			"sessionUpdate": "compaction_checkpoint", "checkpoint_id": "cp-1",
		}),
		displayJournalRow("session/update", sessionID, "event-first", map[string]any{
			"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "hello "},
		}),
		displayJournalRow("session/update", sessionID, "event-second", map[string]any{
			"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "world"},
		}),
		displayJournalRow("session/update", sessionID, "event-thought", map[string]any{
			"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "check the file"},
		}),
		displayJournalRow("session/update", sessionID, "event-tool", map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "read_file",
			"rawInput": map[string]any{"path": "story.txt"},
		}),
		displayJournalRow("session/update", sessionID, "event-tool-done", map[string]any{
			"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
			"content": []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": "story body"}}},
		}),
		displayJournalRow("session/update", sessionID, "event-agent", map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "reply"},
		}),
	}
	var journal strings.Builder
	for _, row := range rows {
		line, _ := json.Marshal(row)
		journal.Write(line)
		journal.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(journal.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got, authoritative, err := readRichDisplayHistoryFromUpdates(dir, sessionID)
	if err != nil || !authoritative {
		t.Fatalf("authoritative=%v err=%v", authoritative, err)
	}
	if len(got) != 2 || got[0].ID != "event-first" || got[0].Content != "hello world" ||
		got[1].ID != "event-thought" || got[1].Content != "reply" || got[1].Thinking != "check the file" {
		t.Fatalf("chunk reduction=%+v", got)
	}
	if len(got[1].Parts) != 3 || got[1].Parts[0]["type"] != "reasoning" || got[1].Parts[1]["type"] != "tool" || got[1].Parts[2]["type"] != "text" {
		t.Fatalf("assistant part order=%+v", got[1].Parts)
	}
	step := got[1].Parts[1]["step"].(map[string]any)
	if step["id"] != "call-1" || step["status"] != "completed" || step["output"].(map[string]any)["text"] != "story body" {
		t.Fatalf("tool lifecycle=%+v", step)
	}
}

func TestRichHistoryKeepsChatCachePathBeforeAnyCompaction(t *testing.T) {
	home := t.TempDir()
	sessionID := "ordinary-history"
	dir := filepath.Join(home, "sessions", url.PathEscape("/tmp/project"), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := strings.Join([]string{
		`{"type":"user","content":[{"type":"text","text":"cache prompt"}]}`,
		`{"type":"reasoning","content":"cache reasoning"}`,
		`{"type":"assistant","content":"cache reply"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		displayJournalRow("session/update", sessionID, "update-user", map[string]any{
			"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "update prompt"},
		}),
		displayJournalRow("session/update", sessionID, "update-agent", map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "update reply"},
		}),
	}
	var journal strings.Builder
	for _, row := range rows {
		line, _ := json.Marshal(row)
		journal.Write(line)
		journal.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(journal.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readRichSessionHistory(home, sessionID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "cache prompt" || got[1].Content != "cache reply" || got[1].Thinking != "cache reasoning" {
		t.Fatalf("non-compacted cache path regressed: %+v", got)
	}
}

func displayJournalRow(method, sessionID, eventID string, update map[string]any) map[string]any {
	return map[string]any{
		"timestamp": int64(1_788_920_582),
		"method":    method,
		"params": map[string]any{
			"sessionId": sessionID,
			"update":    update,
			"_meta":     map[string]any{"eventId": eventID, "agentTimestampMs": int64(1_788_920_582_000)},
		},
	}
}
