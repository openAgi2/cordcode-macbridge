package grokbuild

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestNew_DefaultConfig(t *testing.T) {
	// New should succeed if grok is in PATH (it is on this machine).
	a, err := New(map[string]any{
		"work_dir": "/tmp",
		"model":    "grok-4.5",
		"mode":     "plan",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	if agent.workDir != "/tmp" {
		t.Errorf("workDir = %q", agent.workDir)
	}
	if agent.model != "grok-4.5" {
		t.Errorf("model = %q", agent.model)
	}
	if agent.mode != "plan" {
		t.Errorf("mode = %q, want plan", agent.mode)
	}
	if agent.Name() != "grokbuild" {
		t.Errorf("Name = %q", agent.Name())
	}
}

func TestNew_CLINotFound(t *testing.T) {
	_, err := New(map[string]any{
		"cli_path": "grok-nonexistent-binary-xyz",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent CLI")
	}
}

func TestNew_WithCLIParse(t *testing.T) {
	a, err := New(map[string]any{
		"cli_path": "grok --debug",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	if agent.cliBin != "grok" {
		t.Errorf("cliBin = %q, want grok", agent.cliBin)
	}
	if len(agent.cliExtraArgs) != 1 || agent.cliExtraArgs[0] != "--debug" {
		t.Errorf("cliExtraArgs = %v, want [--debug]", agent.cliExtraArgs)
	}
}

func TestNormalizePermissionMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "default"},
		{"default", "default"},
		{"acceptEdits", "acceptEdits"},
		{"accept_edits", "acceptEdits"},
		{"auto", "auto"},
		{"dontAsk", "dontAsk"},
		{"dont_ask", "dontAsk"},
		{"bypassPermissions", "bypassPermissions"},
		{"bypass_permissions", "bypassPermissions"},
		{"plan", "plan"},
		{"PLAN", "plan"},
	}
	for _, tt := range tests {
		got := normalizePermissionMode(tt.input)
		if got != tt.want {
			t.Errorf("normalizePermissionMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeReasoningEffort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "medium"},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"HIGH", "high"},
	}
	for _, tt := range tests {
		got := normalizeReasoningEffort(tt.input)
		if got != tt.want {
			t.Errorf("normalizeReasoningEffort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAgent_WorkDirSwitcher(t *testing.T) {
	a, err := New(map[string]any{"work_dir": "/tmp"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	agent.SetWorkDir("/new/path")
	if agent.GetWorkDir() != "/new/path" {
		t.Errorf("GetWorkDir = %q", agent.GetWorkDir())
	}
}

func TestAgent_ModeSwitcher(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	modes := agent.PermissionModes()
	if len(modes) != 6 {
		t.Errorf("PermissionModes = %d modes, want 6", len(modes))
	}

	agent.SetMode("acceptEdits")
	if agent.GetMode() != "acceptEdits" {
		t.Errorf("GetMode = %q", agent.GetMode())
	}
}

func TestAgent_ModelSwitcher(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	agent.SetModel("grok-4")
	if agent.GetModel() != "grok-4" {
		t.Errorf("GetModel = %q", agent.GetModel())
	}

	models := agent.AvailableModels(nil)
	if len(models) == 0 {
		t.Error("AvailableModels should not be empty")
	}
}

func TestAgent_ReasoningEffortSwitcher(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	agent.SetReasoningEffort("high")
	if agent.GetReasoningEffort() != "high" {
		t.Errorf("GetReasoningEffort = %q", agent.GetReasoningEffort())
	}

	efforts := agent.AvailableReasoningEfforts()
	if len(efforts) != 3 {
		t.Errorf("AvailableReasoningEfforts = %v, want 3 items", efforts)
	}
}

func TestAgent_ToolAuthorizer(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	// Start with empty.
	if len(agent.GetAllowedTools()) != 0 {
		t.Error("expected empty allowed tools initially")
	}

	if err := agent.AddAllowedTools("Bash", "Read"); err != nil {
		t.Fatalf("AddAllowedTools: %v", err)
	}
	tools := agent.GetAllowedTools()
	if len(tools) != 2 {
		t.Errorf("GetAllowedTools = %v, want 2 items", tools)
	}
}

func TestAgent_ListSessions_LocalCatalog(t *testing.T) {
	a, err := New(map[string]any{"grok_home": t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	list, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("empty home should list 0 sessions, got %d", len(list))
	}
}

// --- Capability interface contract tests ---

func TestAgent_ImplementsDiagnosticsProvider(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.DiagnosticsProvider); !ok {
		t.Error("Agent does not implement core.DiagnosticsProvider")
	}
}

func TestAgent_ImplementsWorkDirSwitcher(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.WorkDirSwitcher); !ok {
		t.Error("Agent does not implement core.WorkDirSwitcher")
	}
}

func TestAgent_ImplementsModelSwitcher(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.ModelSwitcher); !ok {
		t.Error("Agent does not implement core.ModelSwitcher")
	}
}

func TestAgent_ImplementsModeSwitcher(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.ModeSwitcher); !ok {
		t.Error("Agent does not implement core.ModeSwitcher")
	}
}

func TestAgent_ImplementsToolAuthorizer(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.ToolAuthorizer); !ok {
		t.Error("Agent does not implement core.ToolAuthorizer — permission_resolve will not be advertised")
	}
}

func TestAgent_DoesNotImplementTodoProvider(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.TodoProvider); ok {
		t.Error("Agent should NOT implement core.TodoProvider — todos should not be advertised")
	}
}

func TestAgent_ImplementsHistoryProvider(t *testing.T) {
	a, err := New(map[string]any{"grok_home": t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.HistoryProvider); !ok {
		t.Error("Agent must implement core.HistoryProvider so session_history is advertised with local catalog")
	}
}

func TestGrokSession_ImplementsTurnCanceler(t *testing.T) {
	// Compile-time + type assertion on zero value pointer shape.
	var s *grokSession
	var _ core.TurnCanceler = s
}
