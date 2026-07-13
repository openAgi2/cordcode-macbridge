package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestRollout(t *testing.T, codexHome, sessionID, content string) string {
	t.Helper()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "05", "20")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-05-20T12-00-00-"+sessionID+".jsonl")
	if err := os.WriteFile(rolloutPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return rolloutPath
}

func TestGetRichSessionHistory_TextOnly(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-text-only"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"world"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Role != "user" {
		t.Errorf("entries[0].Role = %q, want user", entries[0].Role)
	}
	if entries[0].Content != "hello" {
		t.Errorf("entries[0].Content = %q, want hello", entries[0].Content)
	}
	if entries[1].Role != "assistant" {
		t.Errorf("entries[1].Role = %q, want assistant", entries[1].Role)
	}
	if entries[1].Content != "world" {
		t.Errorf("entries[1].Content = %q, want world", entries[1].Content)
	}
	if len(entries[1].Parts) != 1 || entries[1].Parts[0]["type"] != "text" {
		t.Errorf("entries[1].Parts = %v, want one text part", entries[1].Parts)
	}
}

func TestGetRichSessionHistory_PatchApplyEndWithoutRecordedToolDoesNotPanic(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-orphan-patch-apply-end"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"update the file"}]}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-05-20T12:00:02Z","payload":{"type":"patch_apply_end","call_id":"call_missing","status":"completed","changes":{"updated":[{"path":"main.go","diff":"@@ -1 +1 @@"}]}}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"done"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[1].Content != "done" {
		t.Errorf("assistant content = %q, want done", entries[1].Content)
	}
}

func TestGetRichSessionHistory_UserInputImageFiles(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-user-image"
	imageURL := "data:image/png;base64,aGVsbG8="
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"这个呢"},{"type":"input_image","image_url":"` + imageURL + `","detail":"high"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Content != "这个呢" {
		t.Errorf("entry.Content = %q, want 这个呢", entry.Content)
	}
	if len(entry.Files) != 1 {
		t.Fatalf("len(entry.Files) = %d, want 1", len(entry.Files))
	}
	if entry.Files[0]["mime"] != "image/png" {
		t.Errorf("file mime = %v, want image/png", entry.Files[0]["mime"])
	}
	if entry.Files[0]["url"] != imageURL {
		t.Errorf("file url = %v, want original data URL", entry.Files[0]["url"])
	}
	if len(entry.Parts) != 2 {
		t.Fatalf("len(entry.Parts) = %d, want 2", len(entry.Parts))
	}
	if entry.Parts[0]["type"] != "text" || entry.Parts[1]["type"] != "file" {
		t.Errorf("entry.Parts types = %v, want text,file", entry.Parts)
	}
}

func TestGetRichSessionHistory_RepeatedUserPromptsKeepDistinctImages(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-repeated-image"
	firstImageURL := "data:image/jpeg;base64,Zmlyc3Q="
	secondImageURL := "data:image/jpeg;base64,c2Vjb25k"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"这个呢"},{"type":"input_image","image_url":"` + firstImageURL + `"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"user","content":[{"type":"input_text","text":"这个呢"},{"type":"input_image","image_url":"` + secondImageURL + `"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Content != "这个呢" || entries[1].Content != "这个呢" {
		t.Fatalf("entry contents = %q, %q; want repeated prompt text", entries[0].Content, entries[1].Content)
	}
	if len(entries[0].Files) != 1 || len(entries[1].Files) != 1 {
		t.Fatalf("file counts = %d, %d; want 1,1", len(entries[0].Files), len(entries[1].Files))
	}
	if entries[0].Files[0]["url"] != firstImageURL {
		t.Errorf("first file url = %v, want first image", entries[0].Files[0]["url"])
	}
	if entries[1].Files[0]["url"] != secondImageURL {
		t.Errorf("second file url = %v, want second image", entries[1].Files[0]["url"])
	}
	if entries[0].Files[0]["id"] == entries[1].Files[0]["id"] {
		t.Errorf("file ids should be distinct for distinct image URLs: %v", entries[0].Files[0]["id"])
	}
}

func TestGetRichSessionHistory_CommandExecution(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-cmd-exec"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"list files"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"Let me check."}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"type":"command_execution","command":"ls -la src/"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:04Z","payload":{"type":"function_call_output","call_id":"","output":"file1.go\nfile2.go\n","status":"completed"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:05Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"Here are the files."}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (user + assistant)", len(entries))
	}

	if entries[0].Role != "user" {
		t.Errorf("entries[0].Role = %q, want user", entries[0].Role)
	}

	asst := entries[1]
	if asst.Role != "assistant" {
		t.Errorf("entries[1].Role = %q, want assistant", asst.Role)
	}

	hasText := false
	hasTool := false
	for _, p := range asst.Parts {
		ptype, _ := p["type"].(string)
		if ptype == "text" {
			hasText = true
		}
		if ptype == "tool" {
			hasTool = true
			step, _ := p["step"].(map[string]any)
			if step == nil {
				t.Error("tool part missing step")
				continue
			}
			if step["toolName"] != "Bash" {
				t.Errorf("step.toolName = %v, want Bash", step["toolName"])
			}
			if step["title"] != "ls -la src/" {
				t.Errorf("step.title = %v, want 'ls -la src/'", step["title"])
			}
		}
	}
	if !hasText {
		t.Error("assistant entry missing text part")
	}
	if !hasTool {
		t.Error("assistant entry missing tool part")
	}

	if len(asst.Steps) != 1 {
		t.Fatalf("len(asst.Steps) = %d, want 1", len(asst.Steps))
	}
	if asst.Steps[0]["toolName"] != "Bash" {
		t.Errorf("Steps[0].toolName = %v, want Bash", asst.Steps[0]["toolName"])
	}
	if asst.Steps[0]["status"] != "completed" {
		t.Errorf("Steps[0].status = %v, want completed", asst.Steps[0]["status"])
	}
}

func TestGetRichSessionHistory_FunctionCallWithCallID(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-func-call"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"read file"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"type":"function_call","name":"Read","call_id":"call-abc-123","arguments":"{\"path\":\"src/main.go\"}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"type":"function_call_output","call_id":"call-abc-123","output":"package main\n","status":"completed"}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	asst := entries[1]
	if len(asst.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(asst.Steps))
	}
	if asst.Steps[0]["toolName"] != "Read" {
		t.Errorf("Steps[0].toolName = %v, want Read", asst.Steps[0]["toolName"])
	}
	if asst.Steps[0]["status"] != "completed" {
		t.Errorf("Steps[0].status = %v, want completed", asst.Steps[0]["status"])
	}
	if asst.Steps[0]["title"] != `{"path":"src/main.go"}` {
		t.Errorf("Steps[0].title = %v, want '{\"path\":\"src/main.go\"}'", asst.Steps[0]["title"])
	}
}

func TestGetRichSessionHistory_WithReasoning(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-reasoning"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"explain"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"type":"reasoning","summary":["thinking hard","about this"]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"done"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	asst := entries[1]
	if asst.Thinking != "thinking hard\nabout this" {
		t.Errorf("Thinking = %q, want 'thinking hard\\nabout this'", asst.Thinking)
	}

	hasReasoning := false
	for _, p := range asst.Parts {
		if p["type"] == "reasoning" {
			hasReasoning = true
		}
	}
	if !hasReasoning {
		t.Error("assistant entry missing reasoning part")
	}
}

func TestGetRichSessionHistory_Limit(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-limit"
	rollout := ""
	for i := 0; i < 10; i++ {
		rollout += `{"type":"response_item","timestamp":"2026-05-20T12:00:0` + string(rune('0'+i)) + `Z","payload":{"role":"user","content":[{"type":"input_text","text":"msg` + string(rune('0'+i)) + `"}]}}` + "\n"
	}
	writeTestRollout(t, codexHome, sessionID,
		`{"type":"session_meta","payload":{"id":"`+sessionID+`","cwd":"/tmp"}}`+"\n"+rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 3)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Content != "msg7" {
		t.Errorf("entries[0].Content = %q, want msg7", entries[0].Content)
	}
}

func TestGetRichSessionHistory_EmptySession(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-empty"
	rollout := `{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0", len(entries))
	}
}

func TestGetRichSessionHistory_MissingSession(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := getRichSessionHistory("missing", codexHome, 0)
	if err == nil {
		t.Fatal("getRichSessionHistory() error = nil, want missing session error")
	}
}

func TestGetRichSessionHistory_MultipleToolsInterspersed(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-multi-tool"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"fix it"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"I'll fix this."}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"type":"command_execution","command":"cat file.txt"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:04Z","payload":{"type":"function_call_output","call_id":"","output":"contents","status":"completed"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:05Z","payload":{"type":"function_call","name":"Edit","call_id":"call-edit-1","arguments":"{\"file\":\"file.txt\"}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:06Z","payload":{"type":"function_call_output","call_id":"call-edit-1","output":"ok","status":"completed"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:07Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"Done!"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	asst := entries[1]
	if len(asst.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(asst.Steps))
	}
	if asst.Steps[0]["toolName"] != "Bash" {
		t.Errorf("Steps[0].toolName = %v, want Bash", asst.Steps[0]["toolName"])
	}
	if asst.Steps[0]["status"] != "completed" {
		t.Errorf("Steps[0].status = %v, want completed", asst.Steps[0]["status"])
	}
	if asst.Steps[1]["toolName"] != "Edit" {
		t.Errorf("Steps[1].toolName = %v, want Edit", asst.Steps[1]["toolName"])
	}
	if asst.Steps[1]["status"] != "completed" {
		t.Errorf("Steps[1].status = %v, want completed", asst.Steps[1]["status"])
	}

	toolParts := 0
	for _, p := range asst.Parts {
		if p["type"] == "tool" {
			toolParts++
		}
	}
	if toolParts != 2 {
		t.Errorf("tool parts = %d, want 2", toolParts)
	}
}

func TestGetRichSessionHistory_Timestamps(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-ts"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01.123Z","payload":{"role":"user","content":[{"type":"input_text","text":"hi"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02.456Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"hello"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	expected := time.Date(2026, 5, 20, 12, 0, 1, 123000000, time.UTC)
	if !entries[0].Timestamp.Equal(expected) {
		t.Errorf("entries[0].Timestamp = %v, want %v", entries[0].Timestamp, expected)
	}
}

func TestGetRichSessionHistory_SkipsPlanUpdates(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-skip-plan"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"work"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"type":"function_call","name":"update_plan","call_id":"plan-1","arguments":"{\"plan\":[{\"step\":\"do it\",\"status\":\"in_progress\"}]}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"done"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (update_plan should be skipped)", len(entries))
	}
	if len(entries[1].Steps) != 0 {
		t.Errorf("len(Steps) = %d, want 0 (update_plan should not create steps)", len(entries[1].Steps))
	}
}

func TestGetRichSessionHistory_SkipsSystemPrompts(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-skip-sys"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"<system>ignore this</system>"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"role":"user","content":[{"type":"input_text","text":"real prompt"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:04Z","payload":{"role":"assistant","content":[{"type":"output_text","text":"response"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (system prompts should be skipped)", len(entries))
	}
	if entries[0].Content != "real prompt" {
		t.Errorf("entries[0].Content = %q, want 'real prompt'", entries[0].Content)
	}
}

func TestGetRichSessionHistory_FunctionCallOutOfOrder(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-out-of-order"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","content":[{"type":"input_text","text":"do stuff"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"type":"function_call","name":"Read","call_id":"call-read-1","arguments":"{\"path\":\"a.go\"}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"type":"function_call","name":"Read","call_id":"call-read-2","arguments":"{\"path\":\"b.go\"}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:04Z","payload":{"type":"function_call_output","call_id":"call-read-2","output":"b content","status":"completed"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:05Z","payload":{"type":"function_call_output","call_id":"call-read-1","output":"a content","status":"completed"}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	asst := entries[1]
	if len(asst.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(asst.Steps))
	}
	if asst.Steps[0]["toolName"] != "Read" {
		t.Errorf("Steps[0].toolName = %v, want Read", asst.Steps[0]["toolName"])
	}
	if asst.Steps[0]["status"] != "completed" {
		t.Errorf("Steps[0].status = %v, want completed", asst.Steps[0]["status"])
	}
	if asst.Steps[0]["output"].(map[string]any)["text"] != "a content" {
		t.Errorf("Steps[0].output.text = %v, want 'a content'", asst.Steps[0]["output"])
	}
	if asst.Steps[1]["toolName"] != "Read" {
		t.Errorf("Steps[1].toolName = %v, want Read", asst.Steps[1]["toolName"])
	}
	if asst.Steps[1]["status"] != "completed" {
		t.Errorf("Steps[1].status = %v, want completed", asst.Steps[1]["status"])
	}
	if asst.Steps[1]["output"].(map[string]any)["text"] != "b content" {
		t.Errorf("Steps[1].output.text = %v, want 'b content'", asst.Steps[1]["output"])
	}
}

func TestGetRichSessionHistory_AssistantTextWithMessageType(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-msg-type"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","type":"message","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"hi there"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"role":"user","type":"message","content":[{"type":"input_text","text":"do it"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:04Z","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"ok"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 4 {
		t.Fatalf("len(entries) = %d, want 4 (2 user + 2 assistant)", len(entries))
	}
	if entries[0].Role != "user" || entries[0].Content != "hello" {
		t.Errorf("entries[0] = %v, want user/hello", entries[0])
	}
	if entries[1].Role != "assistant" || entries[1].Content != "hi there" {
		t.Errorf("entries[1] = %v, want assistant/'hi there'", entries[1])
	}
	if entries[2].Role != "user" || entries[2].Content != "do it" {
		t.Errorf("entries[2] = %v, want user/'do it'", entries[2])
	}
	if entries[3].Role != "assistant" || entries[3].Content != "ok" {
		t.Errorf("entries[3] = %v, want assistant/'ok'", entries[3])
	}
}

func TestGetRichSessionHistory_ExecCommandExtractsCmd(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-exec-cmd"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","type":"message","content":[{"type":"input_text","text":"run it"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"Let me run that."}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"type":"function_call","name":"exec_command","call_id":"call-exec-1","arguments":"{\"cmd\":\"ls -la /tmp\"}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:04Z","payload":{"type":"function_call_output","call_id":"call-exec-1","output":"file1\nfile2\n","status":"completed"}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	asst := entries[1]
	if asst.Content != "Let me run that." {
		t.Errorf("Content = %q, want 'Let me run that.'", asst.Content)
	}
	if len(asst.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(asst.Steps))
	}
	if asst.Steps[0]["toolName"] != "exec_command" {
		t.Errorf("toolName = %v, want exec_command", asst.Steps[0]["toolName"])
	}
	if asst.Steps[0]["title"] != "ls -la /tmp" {
		t.Errorf("title = %v, want 'ls -la /tmp'", asst.Steps[0]["title"])
	}
	if asst.Steps[0]["status"] != "completed" {
		t.Errorf("status = %v, want completed", asst.Steps[0]["status"])
	}
}

func TestGetRichSessionHistory_CustomToolCallWithStructuredOutput(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-custom-tool-output"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","type":"message","content":[{"type":"input_text","text":"run it"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"type":"custom_tool_call","name":"exec","call_id":"call-exec","input":"const r = await tools.exec_command({\"cmd\":\"git status --short\"});"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"type":"custom_tool_call_output","call_id":"call-exec","output":[{"type":"input_text","text":"first line"},{"type":"input_text","text":"second line"}]}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	asst := entries[1]
	if len(asst.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(asst.Steps))
	}
	if asst.Steps[0]["toolName"] != "exec_command" {
		t.Errorf("toolName = %v, want exec_command", asst.Steps[0]["toolName"])
	}
	if asst.Steps[0]["title"] != "git status --short" {
		t.Errorf("title = %v, want command", asst.Steps[0]["title"])
	}
	if asst.Steps[0]["status"] != "completed" {
		t.Errorf("status = %v, want completed", asst.Steps[0]["status"])
	}
	output, _ := asst.Steps[0]["output"].(map[string]any)
	if output["text"] != "first line\nsecond line" {
		t.Errorf("output = %v, want joined structured text", output["text"])
	}
}

func TestGetRichSessionHistory_MultiTurnWithMessageAndTools(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "rich-multi-turn"
	rollout := "" +
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:01Z","payload":{"role":"user","type":"message","content":[{"type":"input_text","text":"turn 1"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:02Z","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"reply 1"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:03Z","payload":{"type":"function_call","name":"exec_command","call_id":"call-1","arguments":"{\"cmd\":\"echo hi\"}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:04Z","payload":{"type":"function_call_output","call_id":"call-1","output":"hi\n","status":"completed"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:05Z","payload":{"role":"user","type":"message","content":[{"type":"input_text","text":"turn 2"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:06Z","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"reply 2"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:07Z","payload":{"type":"function_call","name":"exec_command","call_id":"call-2","arguments":"{\"cmd\":\"echo bye\"}"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-05-20T12:00:08Z","payload":{"type":"function_call_output","call_id":"call-2","output":"bye\n","status":"completed"}}` + "\n"
	writeTestRollout(t, codexHome, sessionID, rollout)

	entries, err := getRichSessionHistory(sessionID, codexHome, 0)
	if err != nil {
		t.Fatalf("getRichSessionHistory() error = %v", err)
	}

	if len(entries) != 4 {
		t.Fatalf("len(entries) = %d, want 4", len(entries))
	}
	if entries[0].Role != "user" || entries[0].Content != "turn 1" {
		t.Errorf("entries[0] wrong: %+v", entries[0])
	}
	if entries[1].Role != "assistant" || entries[1].Content != "reply 1" {
		t.Errorf("entries[1] wrong: Content=%q", entries[1].Content)
	}
	if len(entries[1].Steps) != 1 {
		t.Errorf("entries[1].Steps = %d, want 1", len(entries[1].Steps))
	}
	if entries[2].Role != "user" || entries[2].Content != "turn 2" {
		t.Errorf("entries[2] wrong: %+v", entries[2])
	}
	if entries[3].Role != "assistant" || entries[3].Content != "reply 2" {
		t.Errorf("entries[3] wrong: Content=%q", entries[3].Content)
	}
	if len(entries[3].Steps) != 1 {
		t.Errorf("entries[3].Steps = %d, want 1", len(entries[3].Steps))
	}
}
