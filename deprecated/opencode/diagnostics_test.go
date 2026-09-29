package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestRunDiagnostics_EmitsProgressAndAggregates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("[]"))
		case "/global/config":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"providers":{"openai":{"models":{"gpt-4":{"name":"GPT-4"}}}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	agent := &Agent{
		workDir:     t.TempDir(),
		httpBaseURL: srv.URL,
		activeIdx:   -1,
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
	if report.OverallStatus != ocDiagStatusPassed {
		t.Fatalf("OverallStatus = %q, want passed", report.OverallStatus)
	}
	if len(report.Results) != 4 {
		t.Fatalf("Results length = %d, want 4", len(report.Results))
	}
	if len(progress) != 8 {
		t.Fatalf("progress length = %d, want 8", len(progress))
	}
}

func TestRunDiagnostics_ServerUnreachable(t *testing.T) {
	agent := &Agent{
		workDir:     t.TempDir(),
		httpBaseURL: "http://127.0.0.1:1",
		activeIdx:   -1,
	}

	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics() error = %v", err)
	}
	if report.OverallStatus != ocDiagStatusFailed {
		t.Fatalf("OverallStatus = %q, want failed", report.OverallStatus)
	}

	found := false
	for _, r := range report.Results {
		if r.ID == "server" && r.Status == ocDiagStatusFailed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected server check to fail")
	}
}

func TestRunDiagnostics_ServerAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	agent := &Agent{
		workDir:        t.TempDir(),
		httpBaseURL:    srv.URL,
		httpAuthHeader: "Basic dXNlcjpwYXNz",
		activeIdx:      -1,
	}

	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics() error = %v", err)
	}

	found := false
	for _, r := range report.Results {
		if r.ID == "server" && r.Status == ocDiagStatusFailed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected server check to fail with auth error")
	}
}

func TestRunDiagnostics_WorkDirMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	agent := &Agent{
		workDir:     "/nonexistent/path/that/does/not/exist",
		httpBaseURL: srv.URL,
		activeIdx:   -1,
	}

	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics() error = %v", err)
	}

	found := false
	for _, r := range report.Results {
		if r.ID == "workdir" && r.Status == ocDiagStatusFailed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected workdir check to fail")
	}
}
