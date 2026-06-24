package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestRunCLIDiagnostic_AbsolutePathStates(t *testing.T) {
	t.Run("missing absolute path", func(t *testing.T) {
		agent := &Agent{cliBin: filepath.Join(t.TempDir(), "missing-claude")}
		result := agent.runCLIDiagnostic(context.Background())
		if result.Status != diagnosticStatusFailed {
			t.Fatalf("Status = %q, want %q", result.Status, diagnosticStatusFailed)
		}
		if result.FixSuggestion == "" {
			t.Fatal("FixSuggestion = empty, want actionable message")
		}
	})

	t.Run("existing absolute path", func(t *testing.T) {
		cliPath := filepath.Join(t.TempDir(), "claude")
		if err := os.WriteFile(cliPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(cliPath): %v", err)
		}
		agent := &Agent{cliBin: cliPath}
		result := agent.runCLIDiagnostic(context.Background())
		if result.Status != diagnosticStatusPassed {
			t.Fatalf("Status = %q, want %q", result.Status, diagnosticStatusPassed)
		}
	})
}

func TestRunModelQueryDiagnostic_WarnsWithoutProbeConfig(t *testing.T) {
	agent := &Agent{}
	result := agent.runModelQueryDiagnostic(context.Background())
	if result.Status != diagnosticStatusWarning {
		t.Fatalf("Status = %q, want %q", result.Status, diagnosticStatusWarning)
	}
	if result.FixSuggestion == "" {
		t.Fatal("FixSuggestion = empty, want guidance")
	}
}

func TestRunCredentialDiagnostic_ReflectsHints(t *testing.T) {
	t.Run("warning without hints", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		t.Setenv("USERPROFILE", homeDir)
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

		agent := &Agent{}
		result := agent.runCredentialDiagnostic(context.Background())
		if result.Status != diagnosticStatusWarning {
			t.Fatalf("Status = %q, want %q", result.Status, diagnosticStatusWarning)
		}
	})

	t.Run("pass with router hint", func(t *testing.T) {
		agent := &Agent{routerURL: "http://127.0.0.1:3456", routerAPIKey: "token"}
		result := agent.runCredentialDiagnostic(context.Background())
		if result.Status != diagnosticStatusPassed {
			t.Fatalf("Status = %q, want %q", result.Status, diagnosticStatusPassed)
		}
	})
}

func TestSummarizeDiagnosticOverallStatus(t *testing.T) {
	if got := summarizeDiagnosticOverallStatus([]core.DiagnosticResult{{Status: diagnosticStatusPassed, Severity: "required"}}); got != "healthy" {
		t.Fatalf("healthy result = %q, want healthy", got)
	}
	if got := summarizeDiagnosticOverallStatus([]core.DiagnosticResult{{Status: diagnosticStatusWarning, Severity: "optional"}}); got != "degraded" {
		t.Fatalf("warning result = %q, want degraded", got)
	}
	if got := summarizeDiagnosticOverallStatus([]core.DiagnosticResult{{Status: diagnosticStatusFailed, Severity: "required"}}); got != "unhealthy" {
		t.Fatalf("required failure result = %q, want unhealthy", got)
	}
}

func TestRunDiagnostics_EmitsProgressAndAggregatesResults(t *testing.T) {
	workDir := t.TempDir()
	agent := &Agent{
		workDir:   workDir,
		cliBin:    filepath.Join(workDir, "missing-claude"),
		activeIdx: -1,
	}

	var progress []core.DiagnosticProgress
	report, err := agent.RunDiagnostics(context.Background(), func(update core.DiagnosticProgress) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf("RunDiagnostics() error = %v", err)
	}
	if report == nil {
		t.Fatal("RunDiagnostics() = nil, want report")
	}
	if report.OverallStatus != "unhealthy" {
		t.Fatalf("OverallStatus = %q, want unhealthy", report.OverallStatus)
	}
	if len(report.Results) != 4 {
		t.Fatalf("Results length = %d, want 4", len(report.Results))
	}
	if len(progress) != 8 {
		t.Fatalf("progress length = %d, want 8", len(progress))
	}
	if progress[0].CheckID != "cli" || progress[0].Status != diagnosticStatusRunning {
		t.Fatalf("first progress = %+v, want cli running", progress[0])
	}
	if progress[len(progress)-1].CheckID != "credentials" {
		t.Fatalf("last progress = %+v, want credentials result", progress[len(progress)-1])
	}
}
