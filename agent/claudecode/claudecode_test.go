package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestNew_ParsesRunAsUserAndRunAsEnv(t *testing.T) {
	opts := map[string]any{
		"work_dir":    "/tmp/claudecode-test",
		"run_as_user": "partseeker-coder",
		"run_as_env":  []any{"PGSSLROOTCERT", "PGSSLMODE"},
	}
	a, err := New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	ag, ok := a.(*Agent)
	if !ok {
		t.Fatalf("agent is not *Agent: %T", a)
	}
	if ag.spawnOpts.RunAsUser != "partseeker-coder" {
		t.Errorf("spawnOpts.RunAsUser = %q, want %q", ag.spawnOpts.RunAsUser, "partseeker-coder")
	}
	if got := ag.spawnOpts.EnvAllowlist; len(got) != 2 || got[0] != "PGSSLROOTCERT" || got[1] != "PGSSLMODE" {
		t.Errorf("spawnOpts.EnvAllowlist = %v, want [PGSSLROOTCERT PGSSLMODE]", got)
	}
}

func TestNew_RunAsUserSkipsClaudeLookPath(t *testing.T) {
	// With run_as_user set, the supervisor's PATH lookup for "claude" is
	// skipped because the target user's PATH is what matters. Verify that
	// New() doesn't fail even when claude isn't on this test process's PATH.
	opts := map[string]any{
		"work_dir":    "/tmp/claudecode-test",
		"run_as_user": "target-that-definitely-exists",
	}
	// Note: this test relies on New() NOT calling exec.LookPath("claude")
	// when run_as_user is set. If claude IS on PATH in the test env,
	// either branch of the code returns success and the test still passes.
	if _, err := New(opts); err != nil {
		// The only other reason New() could fail for these opts is the
		// LookPath check — fail loudly if that's what happened.
		t.Errorf("New with run_as_user returned error (LookPath not skipped?): %v", err)
	}
	_ = core.AgentSystemPrompt // keep the core import used
}

func TestParseUserQuestions_ValidInput(t *testing.T) {
	input := map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "Which database?",
				"header":      "Setup",
				"multiSelect": false,
				"options": []any{
					map[string]any{"label": "PostgreSQL", "description": "Production"},
					map[string]any{"label": "SQLite", "description": "Dev"},
				},
			},
		},
	}
	qs := parseUserQuestions(input)
	if len(qs) != 1 {
		t.Fatalf("expected 1 question, got %d", len(qs))
	}
	q := qs[0]
	if q.Question != "Which database?" {
		t.Errorf("question = %q", q.Question)
	}
	if q.Header != "Setup" {
		t.Errorf("header = %q", q.Header)
	}
	if q.MultiSelect {
		t.Error("expected multiSelect=false")
	}
	if len(q.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(q.Options))
	}
	if q.Options[0].Label != "PostgreSQL" {
		t.Errorf("option[0].label = %q", q.Options[0].Label)
	}
	if q.Options[1].Description != "Dev" {
		t.Errorf("option[1].description = %q", q.Options[1].Description)
	}
}

func TestParseUserQuestions_EmptyInput(t *testing.T) {
	qs := parseUserQuestions(map[string]any{})
	if len(qs) != 0 {
		t.Errorf("expected 0 questions, got %d", len(qs))
	}
}

func TestParseUserQuestions_NoQuestionText(t *testing.T) {
	input := map[string]any{
		"questions": []any{
			map[string]any{"header": "Setup"},
		},
	}
	qs := parseUserQuestions(input)
	if len(qs) != 0 {
		t.Errorf("expected 0 questions (no question text), got %d", len(qs))
	}
}

func TestParseUserQuestions_MultiSelect(t *testing.T) {
	input := map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "Select features",
				"multiSelect": true,
				"options": []any{
					map[string]any{"label": "Auth"},
					map[string]any{"label": "Logging"},
				},
			},
		},
	}
	qs := parseUserQuestions(input)
	if len(qs) != 1 {
		t.Fatalf("expected 1 question, got %d", len(qs))
	}
	if !qs[0].MultiSelect {
		t.Error("expected multiSelect=true")
	}
}

func TestNormalizePermissionMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// dontAsk aliases
		{"dontAsk", "dontAsk"},
		{"dontask", "dontAsk"},
		{"dont-ask", "dontAsk"},
		{"dont_ask", "dontAsk"},
		// auto
		{"auto", "auto"},
		// bypassPermissions aliases
		{"bypassPermissions", "bypassPermissions"},
		{"yolo", "bypassPermissions"},
		// acceptEdits aliases
		{"acceptEdits", "acceptEdits"},
		{"edit", "acceptEdits"},
		// plan
		{"plan", "plan"},
		// default fallback
		{"", "default"},
		{"unknown", "default"},
	}
	for _, tt := range tests {
		got := normalizePermissionMode(tt.input)
		if got != tt.want {
			t.Errorf("normalizePermissionMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestClaudeSessionSetLiveMode(t *testing.T) {
	cs := &claudeSession{}
	cs.setPermissionMode("default")
	if cs.autoApprove.Load() || cs.acceptEditsOnly.Load() || cs.dontAsk.Load() {
		t.Fatal("expected default mode flags to be off")
	}

	if !cs.SetLiveMode("acceptEdits") {
		t.Fatal("SetLiveMode(acceptEdits) = false, want true")
	}
	if !cs.acceptEditsOnly.Load() || cs.autoApprove.Load() || cs.dontAsk.Load() {
		t.Fatal("acceptEdits flags not set correctly")
	}

	if cs.SetLiveMode("auto") {
		t.Fatal("SetLiveMode(auto) = true, want false")
	}

	cs.SetLiveMode("dontAsk")
	if !cs.dontAsk.Load() || cs.autoApprove.Load() || cs.acceptEditsOnly.Load() {
		t.Fatal("dontAsk flags not set correctly")
	}

	cs.SetLiveMode("bypassPermissions")
	if !cs.autoApprove.Load() || cs.acceptEditsOnly.Load() || cs.dontAsk.Load() {
		t.Fatal("bypassPermissions alias flags not set correctly")
	}
}

func TestClaudeSessionSetLiveMode_AutoSessionRequiresRestart(t *testing.T) {
	cs := &claudeSession{}
	cs.setPermissionMode("auto")
	if cs.SetLiveMode("default") {
		t.Fatal("SetLiveMode(default) from auto session = true, want false")
	}
}

func TestAgent_PermissionModes(t *testing.T) {
	a := &Agent{}
	modes := a.PermissionModes()
	if len(modes) == 0 {
		t.Fatal("PermissionModes() returned no modes")
	}

	foundAuto := false
	foundBypass := false
	for _, mode := range modes {
		if mode.Key == "auto" {
			foundAuto = true
		}
		if mode.Key == "bypassPermissions" {
			foundBypass = true
		}
	}
	if !foundAuto {
		t.Fatal("PermissionModes() missing auto mode")
	}
	if !foundBypass {
		t.Fatal("PermissionModes() missing bypassPermissions mode")
	}
}

func TestIsClaudeEditTool(t *testing.T) {
	for _, tool := range []string{"Edit", "Write", "NotebookEdit", "MultiEdit"} {
		if !isClaudeEditTool(tool) {
			t.Fatalf("isClaudeEditTool(%q) = false, want true", tool)
		}
	}
	if isClaudeEditTool("Bash") {
		t.Fatal("isClaudeEditTool(Bash) = true, want false")
	}
}

func TestSummarizeInput_AskUserQuestion(t *testing.T) {
	input := map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which framework?",
				"options": []any{
					map[string]any{"label": "React"},
					map[string]any{"label": "Vue"},
				},
			},
		},
	}
	result := summarizeInput("AskUserQuestion", input)
	if result == "" {
		t.Error("expected non-empty summary for AskUserQuestion")
	}
}

func TestAgent_Name(t *testing.T) {
	a := &Agent{}
	if got := a.Name(); got != "claudecode" {
		t.Errorf("Name() = %q, want %q", got, "claudecode")
	}
}

func TestAgent_CLIBinaryName(t *testing.T) {
	a := &Agent{cliBin: "claude"}
	if got := a.CLIBinaryName(); got != "claude" {
		t.Errorf("CLIBinaryName() = %q, want %q", got, "claude")
	}

	a2 := &Agent{cliBin: "my-cli"}
	if got := a2.CLIBinaryName(); got != "my-cli" {
		t.Errorf("CLIBinaryName() = %q, want %q", got, "my-cli")
	}
}

func TestAgent_CLIDisplayName(t *testing.T) {
	a := &Agent{}
	if got := a.CLIDisplayName(); got != "Claude" {
		t.Errorf("CLIDisplayName() = %q, want %q", got, "Claude")
	}
}

func TestAgent_SetWorkDir(t *testing.T) {
	a := &Agent{}
	a.SetWorkDir("/tmp/test")
	if got := a.GetWorkDir(); got != "/tmp/test" {
		t.Errorf("GetWorkDir() = %q, want %q", got, "/tmp/test")
	}
}

func TestAgent_SetModel(t *testing.T) {
	a := &Agent{}
	a.SetModel("claude-sonnet-4-20250514")
	if got := a.GetModel(); got != "claude-sonnet-4-20250514" {
		t.Errorf("GetModel() = %q, want %q", got, "claude-sonnet-4-20250514")
	}
}

func TestAgent_SetSessionEnv(t *testing.T) {
	a := &Agent{}
	a.SetSessionEnv([]string{"KEY=value"})
	if len(a.sessionEnv) != 1 || a.sessionEnv[0] != "KEY=value" {
		t.Errorf("sessionEnv = %v, want [KEY=value]", a.sessionEnv)
	}
}

func TestAgent_SetPlatformPrompt(t *testing.T) {
	a := &Agent{}
	a.SetPlatformPrompt("You are a helpful assistant on a custom platform.")
	if a.platformPrompt != "You are a helpful assistant on a custom platform." {
		t.Errorf("platformPrompt = %q, want %q", a.platformPrompt, "You are a helpful assistant on a custom platform.")
	}
}

func TestAgent_SetMode(t *testing.T) {
	a := &Agent{}

	a.SetMode("auto")
	if got := a.GetMode(); got != "auto" {
		t.Fatalf("GetMode() after SetMode(auto) = %q, want auto", got)
	}

	a.SetMode("yolo")
	if got := a.GetMode(); got != "bypassPermissions" {
		t.Fatalf("GetMode() after SetMode(yolo) = %q, want bypassPermissions", got)
	}
}

func TestStripXMLTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<tag>content</tag>", "content"},
		{"no tags", "no tags"},
		{"<a>hello</a><b>world</b>", "helloworld"},
		{"<nested><inner>text</inner></nested>", "text"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripXMLTags(tt.input)
			if got != tt.expected {
				t.Errorf("stripXMLTags(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// verify Agent implements core.Agent
var _ core.Agent = (*Agent)(nil)

func TestEncodeClaudeProjectKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple ASCII path",
			input:    "/Users/username/Documents/project",
			expected: "-Users-username-Documents-project",
		},
		{
			name:     "path with Chinese characters",
			input:    "/Users/username/Documents/项目文件夹",
			expected: "-Users-username-Documents------", // 6 hyphens: 1 for "/" + 5 for Chinese chars
		},
		{
			name:     "path with Japanese characters",
			input:    "/Users/username/Documents/プロジェクト",
			expected: "-Users-username-Documents-------", // 6 hyphens: 1 for "/" + 5 for Japanese chars
		},
		{
			name:     "path with emoji",
			input:    "/Users/username/Documents/🎉project",
			expected: "-Users-username-Documents--project", // 2 hyphens: 1 for "/" + 1 for emoji
		},
		{
			name:     "Windows path with colon",
			input:    "C:\\Users\\username\\Documents",
			expected: "C--Users-username-Documents",
		},
		{
			name:     "path with underscore",
			input:    "/Users/username/my_project",
			expected: "-Users-username-my-project",
		},
		{
			name:     "path with spaces",
			input:    "/Users/username/Mobile Documents/my project",
			expected: "-Users-username-Mobile-Documents-my-project",
		},
		{
			name:     "path with tildes",
			input:    "/Users/username/com~apple~CloudDocs/project",
			expected: "-Users-username-com-apple-CloudDocs-project",
		},
		{
			name:     "iCloud path with spaces and tildes",
			input:    "/Users/username/Library/Mobile Documents/com~apple~CloudDocs/my project",
			expected: "-Users-username-Library-Mobile-Documents-com-apple-CloudDocs-my-project",
		},
		{
			name:     "mixed ASCII and non-ASCII",
			input:    "/Users/username/中文folder/english文件夹",
			expected: "-Users-username---folder-english---", // "/中文" = 3 hyphens, "/文件夹" = 4 hyphens
		},
		{
			name:     "empty path",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeClaudeProjectKey(tt.input)
			if got != tt.expected {
				t.Errorf("encodeClaudeProjectKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFindProjectDir_NonASCIIPath(t *testing.T) {
	// This test verifies that findProjectDir can handle non-ASCII paths
	// by creating a mock projects directory structure
	homeDir := t.TempDir()
	projectsBase := filepath.Join(homeDir, ".claude", "projects")

	// Test case: Chinese characters in path
	chineseWorkDir := "/Users/test/Documents/项目文件夹"
	expectedKey := encodeClaudeProjectKey(chineseWorkDir)

	// Create the mock project directory
	mockProjectDir := filepath.Join(projectsBase, expectedKey)
	if err := os.MkdirAll(mockProjectDir, 0755); err != nil {
		t.Fatalf("failed to create mock project dir: %v", err)
	}

	// Verify findProjectDir finds the directory
	found := findProjectDir(homeDir, chineseWorkDir)
	if found != mockProjectDir {
		t.Errorf("findProjectDir(%q, %q) = %q, want %q", homeDir, chineseWorkDir, found, mockProjectDir)
	}
}

func TestFindProjectDir_ASCIIPath(t *testing.T) {
	// Verify ASCII paths still work correctly
	homeDir := t.TempDir()
	projectsBase := filepath.Join(homeDir, ".claude", "projects")

	asciiWorkDir := "/Users/test/Documents/project"
	expectedKey := encodeClaudeProjectKey(asciiWorkDir)

	mockProjectDir := filepath.Join(projectsBase, expectedKey)
	if err := os.MkdirAll(mockProjectDir, 0755); err != nil {
		t.Fatalf("failed to create mock project dir: %v", err)
	}

	found := findProjectDir(homeDir, asciiWorkDir)
	if found != mockProjectDir {
		t.Errorf("findProjectDir(%q, %q) = %q, want %q", homeDir, asciiWorkDir, found, mockProjectDir)
	}
}

func TestFindProjectDir_NotFound(t *testing.T) {
	homeDir := t.TempDir()
	// Don't create any project directories

	workDir := "/Users/test/Documents/nonexistent"
	found := findProjectDir(homeDir, workDir)
	if found != "" {
		t.Errorf("findProjectDir for nonexistent project = %q, want empty string", found)
	}
}

func TestFindProjectDir_ICloudPath(t *testing.T) {
	// Regression for issue #500: paths containing spaces and "~" (common in macOS
	// iCloud Drive paths like "/Users/x/Library/Mobile Documents/com~apple~CloudDocs/...")
	// must match the on-disk project key that Claude Code CLI generates, which
	// collapses both spaces and "~" to "-".
	homeDir := t.TempDir()
	projectsBase := filepath.Join(homeDir, ".claude", "projects")

	iCloudWorkDir := "/Users/test/Library/Mobile Documents/com~apple~CloudDocs/my project"
	// The on-disk key Claude Code CLI actually writes (spaces and "~" → "-").
	expectedKey := "-Users-test-Library-Mobile-Documents-com-apple-CloudDocs-my-project"

	mockProjectDir := filepath.Join(projectsBase, expectedKey)
	if err := os.MkdirAll(mockProjectDir, 0755); err != nil {
		t.Fatalf("failed to create mock project dir: %v", err)
	}

	found := findProjectDir(homeDir, iCloudWorkDir)
	if found != mockProjectDir {
		t.Errorf("findProjectDir(%q, %q) = %q, want %q", homeDir, iCloudWorkDir, found, mockProjectDir)
	}
}

func TestGetRunningSessionIDs(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionsDir := filepath.Join(tempHome, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}

	// 1. Set up a mock project dir and transcript file for the active session.
	// The session .json must have a cwd so GetRunningSessionIDs can locate
	// the .jsonl transcript and inspect the last message.
	workDir := t.TempDir()
	projectKey := encodeClaudeProjectKey(workDir)
	projectsDir := filepath.Join(tempHome, ".claude", "projects", projectKey)
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatalf("failed to create projects dir: %v", err)
	}
	// Write a transcript file whose last message is a user prompt → executing.
	transcriptPath := filepath.Join(projectsDir, "ses-active-123.jsonl")
	activeTranscript := `{"type":"user","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(activeTranscript), 0644); err != nil {
		t.Fatalf("failed to write active transcript: %v", err)
	}

	myPid := os.Getpid()
	stateData := []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"ses-active-123","cwd":%q}`, myPid, workDir))
	if err := os.WriteFile(filepath.Join(sessionsDir, fmt.Sprintf("%d.json", myPid)), stateData, 0644); err != nil {
		t.Fatalf("failed to write active session: %v", err)
	}

	// 2. Write an inactive session file with a PID that is not running (e.g. 999999 or similar very large PID)
	stateDataInactive := []byte(`{"pid":999999,"sessionId":"ses-inactive-456"}`)
	if err := os.WriteFile(filepath.Join(sessionsDir, "999999.json"), stateDataInactive, 0644); err != nil {
		t.Fatalf("failed to write inactive session: %v", err)
	}

	a, err := New(map[string]any{"work_dir": "/tmp"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	ag := a.(*Agent)

	// Bypass real process identity introspection (the test process's cwd is not
	// workDir, and its comm is the go-test binary). Identity is exercised in the
	// dedicated PID-reuse regression test below.
	prevIdent := procIdentityAlive
	procIdentityAlive = func(pid int, _ string) bool { return pid == myPid }
	t.Cleanup(func() { procIdentityAlive = prevIdent })

	running, err := ag.GetRunningSessionIDs(context.Background())
	if err != nil {
		t.Fatalf("GetRunningSessionIDs returned error: %v", err)
	}

	if !running["ses-active-123"] {
		t.Error("expected ses-active-123 to be running")
	}
	if running["ses-inactive-456"] {
		t.Error("expected ses-inactive-456 to NOT be running")
	}
}

// TestGetRunningSessionIDs_ExternalTurnViaInjectableSeam proves a Claude turn
// launched outside MacBridge (live PID + active transcript, but no MacBridge-owned
// registry entry) is detected as running through the bounded GetRunningSessionIDs
// path. It uses the injectable procAlive seam instead of os.Getpid()/real
// process timing, so the test is deterministic in CI.
//
// Acceptance: external turns are eventually detected through a bounded TTL/cache
// path without requiring a MacBridge-owned turn registry entry. This test covers
// the GetRunningSessionIDs half of that path; the bridge TTL-cache half is
// covered by go-bridge's running_map_cache + list_enrich tests.
func TestGetRunningSessionIDs_ExternalTurnViaInjectableSeam(t *testing.T) {
	prev := procIdentityAlive
	procIdentityAlive = func(pid int, _ string) bool { return pid == 4242 } // deterministic fake identity
	t.Cleanup(func() { procIdentityAlive = prev })

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	sessionsDir := filepath.Join(tempHome, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	workDir := t.TempDir()
	projectKey := encodeClaudeProjectKey(workDir)
	projectsDir := filepath.Join(tempHome, ".claude", "projects", projectKey)
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatalf("projects dir: %v", err)
	}
	// Transcript whose last message is an unanswered user prompt → executing.
	transcript := `{"type":"user","message":{"role":"user","content":"external turn launched from another terminal"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectsDir, "ext-ses.jsonl"), []byte(transcript), 0644); err != nil {
		t.Fatalf("transcript: %v", err)
	}
	// Session stub pointing the fake-alive PID at the external session + cwd.
	stub := []byte(fmt.Sprintf(`{"pid":4242,"sessionId":"ext-ses","cwd":%q}`, workDir))
	if err := os.WriteFile(filepath.Join(sessionsDir, "4242.json"), stub, 0644); err != nil {
		t.Fatalf("stub: %v", err)
	}

	a, err := New(map[string]any{"work_dir": "/tmp"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ag := a.(*Agent)

	running, err := ag.GetRunningSessionIDs(context.Background())
	if err != nil {
		t.Fatalf("GetRunningSessionIDs: %v", err)
	}
	if !running["ext-ses"] {
		t.Fatalf("external turn (live PID + active transcript, no MacBridge-owned entry) must be detected running; got %v", running)
	}

	// Precise semantics preserved: flip the PID dead → the external session must
	// NOT be reported running (a live-but-idle/dead process is not "executing").
	procIdentityAlive = func(pid int, _ string) bool { return false }
	running2, _ := ag.GetRunningSessionIDs(context.Background())
	if running2["ext-ses"] {
		t.Fatalf("external session with DEAD pid reported running; got %v (precise GetRunningSessionIDs semantics broken)", running2)
	}
}

func TestLiveSessionProcess_LiveButIdleIsNotRunning(t *testing.T) {
	prev := procIdentityAlive
	procIdentityAlive = func(pid int, _ string) bool { return pid == 4242 }
	t.Cleanup(func() { procIdentityAlive = prev })

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	sessionsDir := filepath.Join(tempHome, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	workDir := t.TempDir()
	projectKey := encodeClaudeProjectKey(workDir)
	projectsDir := filepath.Join(tempHome, ".claude", "projects", projectKey)
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatalf("projects dir: %v", err)
	}
	finalTranscript := `{"type":"assistant","message":{"role":"assistant","content":"done","stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectsDir, "idle-live-ses.jsonl"), []byte(finalTranscript), 0644); err != nil {
		t.Fatalf("transcript: %v", err)
	}
	stub := []byte(fmt.Sprintf(`{"pid":4242,"sessionId":"idle-live-ses","cwd":%q}`, workDir))
	if err := os.WriteFile(filepath.Join(sessionsDir, "4242.json"), stub, 0644); err != nil {
		t.Fatalf("stub: %v", err)
	}

	a, err := New(map[string]any{"work_dir": "/tmp"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ag := a.(*Agent)

	proc, err := ag.LiveSessionProcess(context.Background(), "idle-live-ses")
	if err != nil {
		t.Fatalf("LiveSessionProcess: %v", err)
	}
	if proc.SessionID != "idle-live-ses" || proc.PID != 4242 || !proc.Live {
		t.Fatalf("LiveSessionProcess = %#v, want live pid 4242", proc)
	}
	running, err := ag.GetRunningSessionIDs(context.Background())
	if err != nil {
		t.Fatalf("GetRunningSessionIDs: %v", err)
	}
	if running["idle-live-ses"] {
		t.Fatalf("live-but-idle session reported executing; running=%v", running)
	}
}

func TestLiveSessionProcess_UsesProcAliveAndDoesNotNeedTranscript(t *testing.T) {
	prev := procAlive
	procAlive = func(pid int) bool { return pid == 7777 }
	t.Cleanup(func() { procAlive = prev })
	prevIdent := procIdentityAlive
	procIdentityAlive = func(pid int, _ string) bool { return pid == 7777 }
	t.Cleanup(func() { procIdentityAlive = prevIdent })

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	sessionsDir := filepath.Join(tempHome, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "7777.json"), []byte(`{"pid":7777,"sessionId":"stub-only-ses","cwd":"/no/transcript/here"}`), 0644); err != nil {
		t.Fatalf("stub: %v", err)
	}

	a, err := New(map[string]any{"work_dir": "/tmp"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ag := a.(*Agent)

	proc, err := ag.LiveSessionProcess(context.Background(), "stub-only-ses")
	if err != nil {
		t.Fatalf("LiveSessionProcess: %v", err)
	}
	if proc.PID != 7777 || !proc.Live {
		t.Fatalf("LiveSessionProcess = %#v, want live stub-only pid", proc)
	}
	if !ag.IsProcessAlive(context.Background(), 7777) {
		t.Fatal("IsProcessAlive(7777) = false, want true")
	}
	if ag.IsProcessAlive(context.Background(), 1) {
		t.Fatal("IsProcessAlive(1) = true, want false")
	}
}

// TestGetRunningSessionIDs_PIDReuseNotRunning is the PID-reuse regression: a
// session stub points at a PID whose original claude exited and whose PID was
// reused by an unrelated process. Even when the transcript still looks active
// (an unanswered user prompt), the session must NOT be reported running,
// because the live PID no longer belongs to a Claude process for this session.
//
// procIdentityAlive is the injectable seam that represents "ps/lsof showed the
// PID is now a different process"; here we override it to return false to
// simulate that mismatch deterministically.
func TestGetRunningSessionIDs_PIDReuseNotRunning(t *testing.T) {
	prev := procIdentityAlive
	procIdentityAlive = func(pid int, _ string) bool { return false } // PID reused by non-claude
	t.Cleanup(func() { procIdentityAlive = prev })

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	sessionsDir := filepath.Join(tempHome, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	workDir := t.TempDir()
	projectKey := encodeClaudeProjectKey(workDir)
	projectsDir := filepath.Join(tempHome, ".claude", "projects", projectKey)
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatalf("projects dir: %v", err)
	}
	// Active transcript: an unanswered user prompt — without the identity check
	// this would classify as executing.
	transcript := `{"type":"user","message":{"role":"user","content":"turn in progress before the original claude exited"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectsDir, "reuse-ses.jsonl"), []byte(transcript), 0644); err != nil {
		t.Fatalf("transcript: %v", err)
	}
	stub := []byte(fmt.Sprintf(`{"pid":4242,"sessionId":"reuse-ses","cwd":%q}`, workDir))
	if err := os.WriteFile(filepath.Join(sessionsDir, "4242.json"), stub, 0644); err != nil {
		t.Fatalf("stub: %v", err)
	}

	a, err := New(map[string]any{"work_dir": "/tmp"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ag := a.(*Agent)

	running, err := ag.GetRunningSessionIDs(context.Background())
	if err != nil {
		t.Fatalf("GetRunningSessionIDs: %v", err)
	}
	if running["reuse-ses"] {
		t.Fatalf("session with a PID reused by a non-claude process must NOT be running; got %v (PID-reuse defence broken)", running)
	}
	if proc, _ := ag.LiveSessionProcess(context.Background(), "reuse-ses"); proc.Live {
		t.Fatalf("LiveSessionProcess.Live = true for reused PID; want false (identity mismatch)")
	}

	// Sanity: with identity restored (the PID IS our claude again), the active
	// transcript makes the session running — proves the fixture is genuinely active.
	procIdentityAlive = func(pid int, _ string) bool { return pid == 4242 }
	running2, _ := ag.GetRunningSessionIDs(context.Background())
	if !running2["reuse-ses"] {
		t.Fatalf("sanity: same active transcript + matching identity should be running; got %v", running2)
	}
}

func TestIsSessionExecuting(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.jsonl")

	// Case 1: Missing file -> false
	if isSessionExecuting(sessionPath) {
		t.Error("expected false for missing file")
	}

	// Case 2: Empty file -> false
	if err := os.WriteFile(sessionPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}
	if isSessionExecuting(sessionPath) {
		t.Error("expected false for empty file")
	}

	// Helper to write lines
	writeLines := func(lines []string) {
		data := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(sessionPath, []byte(data), 0644); err != nil {
			t.Fatalf("failed to write lines: %v", err)
		}
	}

	// Case 3: Regular user prompt -> executing (true)
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
	})
	if !isSessionExecuting(sessionPath) {
		t.Error("expected true for user prompt")
	}

	// Case 4: Assistant calling tool (stop_reason: tool_use) -> executing (true)
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[]}}`,
	})
	if !isSessionExecuting(sessionPath) {
		t.Error("expected true for tool use stop reason")
	}

	// Case 5: Assistant completed turn (stop_reason: end_turn) -> idle (false)
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
	})
	if isSessionExecuting(sessionPath) {
		t.Error("expected false for end_turn stop reason")
	}

	// Case 6: User interrupted -> idle (false)
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
	})
	if isSessionExecuting(sessionPath) {
		t.Error("expected false for interrupted user message")
	}

	// Case 7: Trailing attachment / non-message line -> should still resolve the last message
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"attachment","uuid":"abc"}`,
	})
	if isSessionExecuting(sessionPath) {
		t.Error("expected false when trailing line is non-message but last message is end_turn")
	}

	// Case 8: Claude CLI resume meta user should not make an idle session look running
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}`,
	})
	if isSessionExecuting(sessionPath) {
		t.Error("expected false when trailing line is Claude resume meta user")
	}

	// Case 9: The paired no-response assistant is also hidden from execution state
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"No response requested."}]}}`,
	})
	if isSessionExecuting(sessionPath) {
		t.Error("expected false after Claude resume no-response assistant")
	}

	// Case 10: A real user after the hidden resume pair still means running
	writeLines([]string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"No response requested."}]}}`,
		`{"type":"user","message":{"role":"user","content":"second real prompt"}}`,
	})
	if !isSessionExecuting(sessionPath) {
		t.Error("expected true when real user follows hidden Claude resume pair")
	}
}
