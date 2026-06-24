package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestRunDiagnostics_EmitsProgressAndAggregates(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"test-token","account_id":"acct-1"}}`), 0644)

	t.Setenv("CODEX_HOME", dir)

	agent := &Agent{
		workDir:   dir,
		backend:   "exec",
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
	if report.OverallStatus != cdxDiagStatusPassed {
		t.Fatalf("OverallStatus = %q, want passed", report.OverallStatus)
	}
	// exec mode: cli, auth, workdir = 3 checks
	if len(report.Results) != 3 {
		t.Fatalf("Results length = %d, want 3", len(report.Results))
	}
	// each check emits running + result = 6 progress
	if len(progress) != 6 {
		t.Fatalf("progress length = %d, want 6", len(progress))
	}
}

func TestRunDiagnostics_AppServerMode(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"test-token","account_id":"acct-1"}}`), 0644)

	t.Setenv("CODEX_HOME", dir)

	agent := &Agent{
		workDir:      dir,
		backend:      "app_server",
		appServerURL: "ws://127.0.0.1:1",
		activeIdx:    -1,
	}

	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics() error = %v", err)
	}
	// app_server mode: cli, auth, workdir, app_server = 4 checks
	if len(report.Results) != 4 {
		t.Fatalf("Results length = %d, want 4", len(report.Results))
	}

	// app_server check should fail (nothing listening)
	found := false
	for _, r := range report.Results {
		if r.ID == "app_server" && r.Status == cdxDiagStatusFailed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected app_server check to fail")
	}
}

func TestRunDiagnostics_MissingAuth(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	agent := &Agent{
		workDir:   dir,
		backend:   "exec",
		activeIdx: -1,
	}

	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics() error = %v", err)
	}

	found := false
	for _, r := range report.Results {
		if r.ID == "auth" && r.Status == cdxDiagStatusFailed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected auth check to fail")
	}
}
