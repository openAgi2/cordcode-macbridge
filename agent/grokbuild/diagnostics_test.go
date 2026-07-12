package grokbuild

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestDiagnostics_CLIAvailable(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if report == nil {
		t.Fatal("nil report")
	}
	if len(report.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(report.Results))
	}

	// CLI check should pass (grok is installed).
	cliResult := report.Results[0]
	if cliResult.ID != "cli" {
		t.Errorf("first result ID = %q, want cli", cliResult.ID)
	}
	if cliResult.Status != diagStatusPassed {
		t.Errorf("cli status = %q, want passed", cliResult.Status)
	}

	// Version check should pass (grok --version works).
	versionResult := report.Results[1]
	if versionResult.ID != "version" {
		t.Errorf("second result ID = %q, want version", versionResult.ID)
	}
	if versionResult.Status != diagStatusPassed {
		t.Errorf("version status = %q, want passed", versionResult.Status)
	}

	if report.OverallStatus != "healthy" {
		t.Errorf("OverallStatus = %q, want healthy", report.OverallStatus)
	}
}

func TestDiagnostics_ProgressCallbacks(t *testing.T) {
	a, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	var progressCount int
	report, err := agent.RunDiagnostics(context.Background(), func(p core.DiagnosticProgress) {
		progressCount++
		if p.CheckID == "" {
			t.Error("progress CheckID is empty")
		}
	})
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if report == nil {
		t.Fatal("nil report")
	}
	// 2 checks × 2 progress events (running + final) = 4.
	if progressCount != 4 {
		t.Errorf("progress count = %d, want 4", progressCount)
	}
}

func TestDiagnostics_CLINotFound(t *testing.T) {
	_, err := New(map[string]any{
		"cli_path": "grok-nonexistent-xyz-test",
	})
	// New() fails on LookPath for nonexistent CLI.
	if err == nil {
		t.Fatal("expected New to fail for nonexistent CLI")
	}
}

func TestSummarizeDiagStatus_Healthy(t *testing.T) {
	results := []core.DiagnosticResult{
		{Status: diagStatusPassed, Severity: "required"},
		{Status: diagStatusPassed, Severity: "recommended"},
	}
	if got := summarizeDiagStatus(results); got != "healthy" {
		t.Errorf("got %q, want healthy", got)
	}
}

func TestSummarizeDiagStatus_Unhealthy(t *testing.T) {
	results := []core.DiagnosticResult{
		{Status: diagStatusPassed, Severity: "required"},
		{Status: diagStatusFailed, Severity: "required"},
	}
	if got := summarizeDiagStatus(results); got != "unhealthy" {
		t.Errorf("got %q, want unhealthy", got)
	}
}

func TestSummarizeDiagStatus_Degraded(t *testing.T) {
	results := []core.DiagnosticResult{
		{Status: diagStatusPassed, Severity: "required"},
		{Status: diagStatusWarning, Severity: "recommended"},
	}
	if got := summarizeDiagStatus(results); got != "degraded" {
		t.Errorf("got %q, want degraded", got)
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"0", 0},
		{"42", 42},
		{"93", 93},
		{"abc", 0},
		{"12abc", 12},
	}
	for _, tt := range tests {
		got := parseInt(tt.input)
		if got != tt.want {
			t.Errorf("parseInt(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
