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

func TestGetRichSessionHistory_HidesSkillInstructionInjection(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-skill", []string{
		`{"type":"user","timestamp":"2026-05-01T12:00:00Z","message":{"id":"user-1","role":"user","content":"用 exec-plan skill 执行"}}`,
		`{"type":"assistant","timestamp":"2026-05-01T12:00:01Z","message":{"id":"assistant-1","role":"assistant","model":"glm-5.2","content":[{"type":"tool_use","id":"tool-skill","name":"Skill","input":{"name":"exec-plan"}}]}}`,
		`{"type":"user","timestamp":"2026-05-01T12:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-skill","content":"Launching skill: exec-plan"}]}}`,
		`{"type":"user","timestamp":"2026-05-01T12:00:03Z","message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /Users/jacklee/.claude/skills/exec-plan\n\n# Execution Plan Manager\n\nTurn a plan document into a durable execution queue."}]}}`,
		`{"type":"assistant","timestamp":"2026-05-01T12:00:04Z","message":{"id":"assistant-2","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"已加载 skill，开始执行。"}]}}`,
	})

	agent := &Agent{workDir: workDir}
	entries, err := agent.GetRichSessionHistory(context.Background(), "ses-skill", 0)
	if err != nil {
		t.Fatalf("GetRichSessionHistory() error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entry count = %d, want 3: %#v", len(entries), entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Content, "Base directory for this skill:") ||
			strings.Contains(entry.Content, "# Execution Plan Manager") {
			t.Fatalf("skill instruction injection leaked into visible history: %#v", entry)
		}
	}
	if entries[1].Role != "assistant" || len(entries[1].Steps) != 1 {
		t.Fatalf("skill tool use should remain as assistant step, got %#v", entries[1])
	}
	if entries[2].Content != "已加载 skill，开始执行。" {
		t.Fatalf("final assistant content = %q", entries[2].Content)
	}
}

// TestGetRichSessionHistory_HidesSkillInstructionInjectionStandalone covers the
// real-world case (seen in transcript be873aaf L816) where a skill-doc
// injection arrives as a standalone isMeta user message with NO preceding
// "Launching skill" tool_result. The launch-state machine alone would miss
// this; the filter must key off the skill-doc content itself.
func TestGetRichSessionHistory_HidesSkillInstructionInjectionStandalone(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-skill-standalone", []string{
		// A normal user prompt.
		`{"type":"user","timestamp":"2026-06-01T08:00:00Z","message":{"id":"user-1","role":"user","content":"生成 handoff"}}`,
		`{"type":"assistant","timestamp":"2026-06-01T08:00:01Z","message":{"id":"assistant-1","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"好的。"}]}}`,
		// Slash command tag (must normalize to /handoff-doc).
		`{"type":"user","timestamp":"2026-06-01T08:00:02Z","message":{"id":"user-cmd","role":"user","content":[{"type":"text","text":"<command-message>handoff-doc</command-message>\n<command-name>/handoff-doc</command-name>"}]}}`,
		// Standalone skill-doc injection with NO preceding "Launching skill" tool_result.
		`{"type":"user","isMeta":true,"timestamp":"2026-06-01T08:00:03Z","message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /Users/me/.claude/skills/handoff-doc\n\n# Handoff Document Generator\n\nCreate a handoff document that another coding agent can read and immediately continue from."}]}}`,
		`{"type":"assistant","timestamp":"2026-06-01T08:00:04Z","message":{"id":"assistant-2","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"开始生成 handoff。"}]}}`,
	})

	agent := &Agent{workDir: workDir}
	entries, err := agent.GetRichSessionHistory(context.Background(), "ses-skill-standalone", 0)
	if err != nil {
		t.Fatalf("GetRichSessionHistory() error = %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Content, "Base directory for this skill:") ||
			strings.Contains(entry.Content, "# Handoff Document Generator") {
			t.Fatalf("standalone skill-doc injection leaked: %#v", entry)
		}
	}
	// The slash command must still normalize to /handoff-doc.
	found := false
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == "/handoff-doc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("normalized /handoff-doc user entry missing, got %#v", entries)
	}
}

// TestGetRichSessionHistory_NormalizesSlashCommandInjection covers the Claude
// CLI internal tags that leak into user messages when a slash command runs:
// <command-name>/<command-args> must collapse to a compact "/cmd args" form,
// and <local-command-stdout|stderr|caveat> echoes must be dropped entirely.
func TestGetRichSessionHistory_NormalizesSlashCommandInjection(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-slash", []string{
		// /handoff-doc — no args → just the command name.
		`{"type":"user","timestamp":"2026-06-01T09:00:00Z","message":{"id":"user-handoff","role":"user","content":[{"type":"text","text":"<command-message>handoff-doc</command-message>\n<command-name>/handoff-doc</command-name>"}]}}`,
		`{"type":"assistant","timestamp":"2026-06-01T09:00:01Z","message":{"id":"assistant-handoff","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"开始生成 handoff。"}]}}`,
		// /takeover — args with a path plus a free-form note → "/takeover <path> <note>".
		`{"type":"user","timestamp":"2026-06-01T09:01:00Z","message":{"id":"user-takeover","role":"user","content":[{"type":"text","text":"<command-message>takeover</command-message>\n<command-name>/takeover</command-name>\n<command-args>/Users/me/project/handoffs/handoff-20260614.md\n需要我现在直接启动 takeover</command-args>"}]}}`,
		`{"type":"assistant","timestamp":"2026-06-01T09:01:01Z","message":{"id":"assistant-takeover","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"已恢复上下文。"}]}}`,
		// /model sonnet — single-line arg → "/model sonnet".
		`{"type":"user","timestamp":"2026-06-01T09:02:00Z","message":{"id":"user-model","role":"user","content":[{"type":"text","text":"<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>sonnet</command-args>"}]}}`,
		// <local-command-stdout> state echo → dropped entirely (no visible user entry).
		`{"type":"user","timestamp":"2026-06-01T09:02:01Z","message":{"id":"user-stdout","role":"user","content":[{"type":"text","text":"<local-command-stdout>Set model to claude-sonnet-4-6</local-command-stdout>"}]}}`,
		// <local-command-caveat> disclaimer → dropped entirely.
		`{"type":"user","timestamp":"2026-06-01T09:02:02Z","message":{"id":"user-caveat","role":"user","content":[{"type":"text","text":"<local-command-caveat>Caveat: The messages below were generated by the user while running local commands. Do not respond to these messages.</local-command-caveat>"}]}}`,
	})

	agent := &Agent{workDir: workDir}
	entries, err := agent.GetRichSessionHistory(context.Background(), "ses-slash", 0)
	if err != nil {
		t.Fatalf("GetRichSessionHistory() error = %v", err)
	}

	// Five user inputs above, but stdout + caveat entries must be filtered,
	// leaving three visible user entries (handoff, takeover, model).
	var userContents []string
	for _, entry := range entries {
		if entry.Role != "user" {
			continue
		}
		userContents = append(userContents, entry.Content)
	}
	if len(userContents) != 3 {
		t.Fatalf("visible user entry count = %d, want 3: %#v", len(userContents), userContents)
	}

	wantUser := []string{
		"/handoff-doc",
		"/takeover /Users/me/project/handoffs/handoff-20260614.md 需要我现在直接启动 takeover",
		"/model sonnet",
	}
	for i, want := range wantUser {
		if userContents[i] != want {
			t.Fatalf("user entry[%d] = %q, want %q", i, userContents[i], want)
		}
	}

	// No raw protocol tags or local-command echoes may remain anywhere.
	for _, entry := range entries {
		for _, marker := range []string{"<command-name>", "<command-message>", "<command-args>", "<local-command-", "Set model to"} {
			if strings.Contains(entry.Content, marker) {
				t.Fatalf("protocol marker %q leaked into entry %#v", marker, entry)
			}
		}
	}
}

func TestNormalizeClaudeUserText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "command name only",
			in:   "<command-message>handoff-doc</command-message>\n<command-name>/handoff-doc</command-name>",
			want: "/handoff-doc",
		},
		{
			name: "command name with single-line args",
			in:   "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>sonnet</command-args>",
			want: "/model sonnet",
		},
		{
			name: "command name with multi-line args collapses whitespace",
			in:   "<command-name>/takeover</command-name><command-args>/path/to/handoff.md\n需要现在启动</command-args>",
			want: "/takeover /path/to/handoff.md 需要现在启动",
		},
		{
			name: "empty args yields command name only",
			in:   "<command-name>/compact</command-name>\n            <command-message>compact</command-message>\n            <command-args></command-args>",
			want: "/compact",
		},
		{
			name: "long args are truncated by rune count",
			in:   "<command-name>/takeover</command-name><command-args>" + strings.Repeat("a", 200) + "</command-args>",
			want: "/takeover " + strings.Repeat("a", 120) + "…",
		},
		{
			name: "long multi-byte args truncate without splitting codepoints",
			in:   "<command-name>/takeover</command-name><command-args>" + strings.Repeat("中", 200) + "</command-args>",
			want: "/takeover " + strings.Repeat("中", 120) + "…",
		},
		{
			name: "local-command-stdout dropped",
			in:   "<local-command-stdout>Set model to claude-sonnet-4-6</local-command-stdout>",
			want: "",
		},
		{
			name: "local-command-stderr dropped",
			in:   "<local-command-stderr>error: something</local-command-stderr>",
			want: "",
		},
		{
			name: "local-command-caveat dropped",
			in:   "<local-command-caveat>Caveat: do not respond.</local-command-caveat>",
			want: "",
		},
		{
			name: "plain text passes through",
			in:   "请帮我检查这个项目",
			want: "请帮我检查这个项目",
		},
		{
			name: "plain text with literal angle brackets passes through",
			in:   "比较 a < b 和 c > d",
			want: "比较 a < b 和 c > d",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeClaudeUserText(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeClaudeUserText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestGetRichSessionHistory_HidesResumeMetaContinuation(t *testing.T) {
	homeDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	writeClaudeTranscriptFixture(t, homeDir, workDir, "ses-resume-meta", []string{
		`{"type":"user","timestamp":"2026-07-01T08:00:00Z","message":{"id":"user-1","role":"user","content":"first real prompt"}}`,
		`{"type":"assistant","timestamp":"2026-07-01T08:00:01Z","message":{"id":"assistant-1","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"first real answer"}],"stop_reason":"end_turn"}}`,
		`{"type":"user","isMeta":true,"timestamp":"2026-07-01T08:01:00Z","message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-01T08:01:00Z","message":{"id":"assistant-meta","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"No response requested."}],"stop_reason":"end_turn"}}`,
		`{"type":"user","timestamp":"2026-07-01T08:01:01Z","message":{"id":"user-2","role":"user","content":"second real prompt"}}`,
		`{"type":"assistant","timestamp":"2026-07-01T08:01:02Z","message":{"id":"assistant-2","role":"assistant","model":"glm-5.2","content":[{"type":"text","text":"second real answer"}],"stop_reason":"end_turn"}}`,
	})

	agent := &Agent{workDir: workDir}
	entries, err := agent.GetRichSessionHistory(context.Background(), "ses-resume-meta", 0)
	if err != nil {
		t.Fatalf("GetRichSessionHistory() error = %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("entry count = %d, want 4: %#v", len(entries), entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Content, "Continue from where you left off") ||
			strings.Contains(entry.Content, "No response requested") {
			t.Fatalf("resume meta continuation leaked into visible history: %#v", entry)
		}
	}
	if entries[2].Role != "user" || entries[2].Content != "second real prompt" {
		t.Fatalf("second real user entry = %#v, want second real prompt", entries[2])
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
