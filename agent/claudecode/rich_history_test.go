package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestGetRichSessionHistory_RebuildsStructuredMessagesAndAppliesLimitAfterMerge(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-rich", []string{
		`{"type":"user","timestamp":"2026-05-01T10:00:00Z","message":{"id":"user-1","role":"user","content":"请检查这个项目"}}`,
		`{"type":"assistant","timestamp":"2026-05-01T10:00:01Z","message":{"id":"assistant-1","role":"assistant","model":"claude-sonnet-4","content":[{"type":"thinking","thinking":"先读文件"}]}}`,
		`{"type":"assistant","timestamp":"2026-05-01T10:00:02Z","message":{"id":"assistant-1","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"让我先看看。"}]}}`,
		`{"type":"assistant","timestamp":"2026-05-01T10:00:03Z","message":{"id":"assistant-1","role":"assistant","model":"claude-sonnet-4","content":[{"type":"tool_use","id":"tool-read","name":"Read","input":{"file_path":"/tmp/demo.txt"}}]}}`,
		`{"type":"assistant","timestamp":"2026-05-01T10:00:04Z","message":{"id":"assistant-1","role":"assistant","model":"claude-sonnet-4","content":[{"type":"tool_use","id":"tool-bash","name":"Bash","input":{"command":"pwd"}}]}}`,
		`{"type":"user","timestamp":"2026-05-01T10:00:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-bash","content":[{"type":"text","text":"bash ok"}]}]}}`,
		`{"type":"user","timestamp":"2026-05-01T10:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-read","content":{"kind":"summary","lines":2}}]}}`,
		`{"type":"assistant","timestamp":"2026-05-01T10:00:07Z","message":{"id":"assistant-2","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"都完成了。"}]}}`,
	})

	agent := &Agent{workDir: workDir}
	entries, err := agent.GetRichSessionHistory(context.Background(), "ses-rich", 2)
	if err != nil {
		t.Fatalf("GetRichSessionHistory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}

	first := entries[0]
	if first.ID != "assistant-1" {
		t.Fatalf("first.ID = %q, want assistant-1", first.ID)
	}
	if first.Role != "assistant" {
		t.Fatalf("first.Role = %q, want assistant", first.Role)
	}
	if first.Content != "让我先看看。" {
		t.Fatalf("first.Content = %q, want 让我先看看。", first.Content)
	}
	if first.Thinking != "先读文件" {
		t.Fatalf("first.Thinking = %q, want 先读文件", first.Thinking)
	}
	if first.ModelID != "claude-sonnet-4" {
		t.Fatalf("first.ModelID = %q, want claude-sonnet-4", first.ModelID)
	}
	if len(first.Parts) != 4 {
		t.Fatalf("first.Parts length = %d, want 4", len(first.Parts))
	}
	if got := first.Parts[0]["type"]; got != "reasoning" {
		t.Fatalf("first part type = %#v, want reasoning", got)
	}
	if got := first.Parts[1]["type"]; got != "text" {
		t.Fatalf("second part type = %#v, want text", got)
	}
	if got := first.Parts[2]["type"]; got != "tool" {
		t.Fatalf("third part type = %#v, want tool", got)
	}
	if got := first.Parts[3]["type"]; got != "tool" {
		t.Fatalf("fourth part type = %#v, want tool", got)
	}
	if len(first.Steps) != 2 {
		t.Fatalf("first.Steps length = %d, want 2", len(first.Steps))
	}

	readStep := first.Steps[0]
	if got := readStep["id"]; got != "tool-read" {
		t.Fatalf("read step id = %#v, want tool-read", got)
	}
	if got := readStep["status"]; got != "completed" {
		t.Fatalf("read step status = %#v, want completed", got)
	}
	readOutput, _ := readStep["output"].(map[string]any)
	if got := readOutput["kind"]; got != "summary" {
		t.Fatalf("read step output.kind = %#v, want summary", got)
	}
	if got := readOutput["lines"]; got != float64(2) {
		t.Fatalf("read step output.lines = %#v, want 2", got)
	}

	bashStep := first.Steps[1]
	if got := bashStep["id"]; got != "tool-bash" {
		t.Fatalf("bash step id = %#v, want tool-bash", got)
	}
	if got := bashStep["status"]; got != "completed" {
		t.Fatalf("bash step status = %#v, want completed", got)
	}
	bashOutput, _ := bashStep["output"].(map[string]any)
	if got := bashOutput["kind"]; got != "inline" {
		t.Fatalf("bash step output.kind = %#v, want inline", got)
	}
	if got := bashOutput["text"]; got != "bash ok" {
		t.Fatalf("bash step output.text = %#v, want bash ok", got)
	}

	second := entries[1]
	if second.ID != "assistant-2" {
		t.Fatalf("second.ID = %q, want assistant-2", second.ID)
	}
	if second.Content != "都完成了。" {
		t.Fatalf("second.Content = %q, want 都完成了。", second.Content)
	}
}

func TestGetRichSessionHistory_GracefullyHandlesUnknownBlocksMissingResultsAndBrokenJSON(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-unknown", []string{
		`{"type":"user","timestamp":"2026-05-01T11:00:00Z","message":{"id":"user-1","role":"user","content":"继续"}}`,
		`{"type":"assistant","timestamp":"2026-05-01T11:00:01Z","message":{"id":"assistant-1","role":"assistant","model":"claude-opus-4","content":[{"type":"thinking","thinking":"先看看情况"},{"type":"mystery","value":"ignored"},{"type":"text","text":"我先读取一下。"},{"type":"tool_use","id":"tool-missing","name":"Read","input":{"file_path":"/tmp/missing.txt"}}]}}`,
		`{"type":"user","timestamp":"2026-05-01T11:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"orphan-tool","content":"orphan"}]}}`,
		`{"type":"assistant",`,
	})

	agent := &Agent{workDir: workDir}
	entries, err := agent.GetRichSessionHistory(context.Background(), "ses-unknown", 0)
	if err != nil {
		t.Fatalf("GetRichSessionHistory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}

	assistant := entries[1]
	if assistant.ID != "assistant-1" {
		t.Fatalf("assistant.ID = %q, want assistant-1", assistant.ID)
	}
	if assistant.Thinking != "先看看情况" {
		t.Fatalf("assistant.Thinking = %q, want 先看看情况", assistant.Thinking)
	}
	if assistant.Content != "我先读取一下。" {
		t.Fatalf("assistant.Content = %q, want 我先读取一下。", assistant.Content)
	}
	if len(assistant.Parts) != 3 {
		t.Fatalf("assistant.Parts length = %d, want 3", len(assistant.Parts))
	}
	if len(assistant.Steps) != 1 {
		t.Fatalf("assistant.Steps length = %d, want 1", len(assistant.Steps))
	}
	step := assistant.Steps[0]
	if got := step["status"]; got != "unknown" {
		t.Fatalf("step status = %#v, want unknown", got)
	}
	if _, ok := step["output"]; ok {
		t.Fatalf("step output should be absent for missing tool_result, got %#v", step["output"])
	}
}

func writeClaudeTranscriptFixture(t *testing.T, homeDir, workDir, sessionID string, lines []string) string {
	t.Helper()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		t.Fatalf("Abs(workDir): %v", err)
	}
	projectDir := filepath.Join(homeDir, ".claude", "projects", encodeClaudeProjectKey(absWorkDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectDir): %v", err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

var _ core.RichHistoryProvider = (*Agent)(nil)
