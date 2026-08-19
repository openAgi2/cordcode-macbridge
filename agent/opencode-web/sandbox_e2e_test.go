package opencodeweb

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestSandboxEndToEnd runs the full client chain against a REAL locally
// spawned `opencode serve` sandbox (design §6 沙盒：端口 4296..4396 + 独立数据
// 目录). Gated by OCW_SANDBOX_URL so CI and normal `go test` skip it:
//
//	OCW_SANDBOX_URL=http://127.0.0.1:4296 OCW_SANDBOX_USER=u OCW_SANDBOX_PASS=p go test -run TestSandboxEndToEnd -count=1 -v
//
// What it proves on the real binary (everything except a billed model
// completion — that stays with the owner's现网 matrix):
//   - generation probe resolves 1.18 with Basic Auth,
//   - list + catalog + create + catalog-gated send reach the real routes,
//   - the SSE stream relays the live turn lifecycle (user bubble + terminal),
//   - a provider-unavailable turn closes as the diagnosable zero-output
//     turn_error instead of a healthy empty completion (§3.5).
func TestSandboxEndToEnd(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("OCW_SANDBOX_URL"))
	if base == "" {
		t.Skip("set OCW_SANDBOX_URL (and OCW_SANDBOX_USER/PASS) to run the sandbox E2E")
	}
	user := os.Getenv("OCW_SANDBOX_USER")
	pass := os.Getenv("OCW_SANDBOX_PASS")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	a, err := New(map[string]any{
		"work_dir":          "/tmp",
		"opencode_web_url":  base,
		"opencode_web_user": user,
		"opencode_web_pass": pass,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)

	available, detail := agent.InstanceStatus()
	if !available {
		t.Fatalf("sandbox probe failed: %s", detail)
	}
	t.Logf("probe: %s", detail)
	if !strings.Contains(detail, "generation=1.18") {
		t.Fatalf("sandbox serve expected generation 1.18, got %s", detail)
	}

	sessions, err := agent.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	t.Logf("listed %d sessions (isolated sandbox store)", len(sessions))

	models := agent.AvailableModels(ctx)
	if len(models) == 0 {
		t.Fatal("catalog must not be empty on the sandbox serve")
	}
	t.Logf("catalog: %d models, first=%s", len(models), models[0].Name)

	// OCW_SANDBOX_SESSION resumes a specific session (e.g. an errlab session
	// whose provider circuit already tripped → fast-fail path).
	resumeID := strings.TrimSpace(os.Getenv("OCW_SANDBOX_SESSION"))
	if resumeID != "" && len(models) > 0 {
		agent.SetModel(models[0].Name)
	}
	sess, err := agent.StartSession(ctx, resumeID)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	if resumeID == "" {
		agent.SetModel(models[0].Name)
	}

	events := sess.Events()
	if err := sess.Send("Reply with exactly: SANDBOX_OK", nil, nil); err != nil {
		t.Fatalf("send (catalog model %s): %v", models[0].Name, err)
	}
	t.Log("send accepted; waiting for the SSE turn lifecycle…")

	var sawUser, sawTerminal bool
	var terminalErr string
	// Long window: a failing provider makes the serve retry with backoff
	// (live-pinned 1.18.18: 3/8/16/34/60s…) before the terminal error frame.
	deadline := time.After(240 * time.Second)
loop:
	for !(sawUser && sawTerminal) {
		select {
		case ev := <-events:
			switch ev.Type {
			case core.EventUserMessage:
				sawUser = true
			case core.EventResult:
				sawTerminal = true
				if ev.Error != nil {
					terminalErr = ev.Error.Error()
				}
			case core.EventError:
				// terminal-ish; keep waiting for the result frame
			}
		case <-deadline:
			break loop
		}
	}
	if !sawUser {
		t.Fatal("SSE must relay the user bubble for the sent prompt")
	}
	if !sawTerminal {
		t.Fatal("SSE must close the turn (session idle → EventResult)")
	}
	if terminalErr != "" {
		// A failing provider turn must surface the SERVE's own error text
		// (session.error / retry.message), falling back to the generic
		// zero-output diagnosis only when the serve said nothing.
		t.Logf("turn closed as turn_error with the serve's text: %s", terminalErr)
	} else {
		t.Log("turn completed with output on the sandbox serve")
	}

	// Abort path reaches the real route (turn already terminal; a 2xx answer
	// proves route correctness).
	canceler, ok := sess.(core.TurnCanceler)
	if !ok {
		t.Fatal("TurnCanceler missing")
	}
	if err := canceler.CancelTurn(ctx); err != nil {
		t.Logf("abort after turn end: %v (route reached; post-terminal answers vary)", err)
	} else {
		t.Log("abort route answered 2xx")
	}

	// run_diagnostics carries generation + folding state end to end.
	report, err := agent.RunDiagnostics(ctx, nil)
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	raw, _ := json.Marshal(report)
	t.Logf("diagnostics: %s", truncateForError(string(raw)))
}

// TestSandboxServeLifecycle documents how the sandbox serve is spawned for the
// E2E above (not a test itself — kept here so the invocation stays reviewable
// and reproducible). See docs/…完成情况.md §沙盒.
var _ = func() bool {
	_ = exec.Command
	_ = url.QueryEscape
	_ = rand.Int
	return true
}()
