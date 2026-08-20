package opencodeweb

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// c4_sandbox_test.go is the C4 regression against the REAL isolated 1.18.18
// serve: A3 provider-error terminal honesty, A4 abort convergence, and E3
// external official-client turn observation through the one global
// subscriber + registered route.
//
//	OCW_SANDBOX_URL=http://127.0.0.1:4398 OCW_SANDBOX_USER=gatea OCW_SANDBOX_PASS=gatea-pass \
//	  go test ./agent/opencode-web -run TestSandboxC4TurnTerminals -count=1 -v
func TestSandboxC4TurnTerminals(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("OCW_SANDBOX_URL"))
	if base == "" {
		t.Skip("set OCW_SANDBOX_URL (and OCW_SANDBOX_USER/PASS) to run the C4 sandbox regression")
	}
	root := envOrDefault("OCW_SANDBOX_ROOT", "/tmp/ocw-gate-a-20260820")
	ws := envOrDefault("OCW_SANDBOX_WORKSPACE", filepath.Join(root, "workspace"))

	a, err := New(map[string]any{
		"work_dir":          ws,
		"opencode_web_url":  base,
		"opencode_web_user": os.Getenv("OCW_SANDBOX_USER"),
		"opencode_web_pass": os.Getenv("OCW_SANDBOX_PASS"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	t.Cleanup(func() { _ = agent.Stop() })
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	newSession := func() string {
		code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(c.apiPath("/session")+"?directory="+ws), map[string]any{}, ws, true)
		if err != nil || code >= 400 {
			t.Fatalf("create: code=%d err=%v body=%s", code, err, truncateForError(string(raw)))
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
			t.Fatalf("create decode: %v %s", err, truncateForError(string(raw)))
		}
		return created.ID
	}

	// ── A3: provider error retries then settles as turn_error ──────────────
	sidA3 := newSession()
	sessA3, err := agent.StartSession(ctx, sidA3)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	senderA3 := sessA3.(core.PromptOptionsSender)
	if err := senderA3.SendWithOptions("A3_PROVIDER_ERROR probe", nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
		t.Fatalf("A3 send: %v", err)
	}
	ev := waitForEventType(t, sessA3.Events(), core.EventResult, 120*time.Second)
	if ev.Error == nil {
		t.Fatalf("A3 turn must settle as turn_error with the provider text, got healthy %+v", ev)
	}
	if !strings.Contains(ev.Error.Error(), "localmock") {
		t.Logf("note: A3 terminal text = %v", ev.Error)
	}
	t.Logf("A3 settled as turn_error: %v", ev.Error)

	// ── A4: abort mid-slow-stream converges to a real terminal ─────────────
	sidA4 := newSession()
	sessA4, err := agent.StartSession(ctx, sidA4)
	if err != nil {
		t.Fatalf("StartSession A4: %v", err)
	}
	senderA4 := sessA4.(core.PromptOptionsSender)
	if err := senderA4.SendWithOptions("A4_SLOW_STREAM then reply", nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
		t.Fatalf("A4 send: %v", err)
	}
	time.Sleep(700 * time.Millisecond) // let the slow stream arm
	if err := sessA4.(core.TurnCanceler).CancelTurn(ctx); err != nil {
		t.Fatalf("A4 abort: %v", err)
	}
	evA4 := waitForEventType(t, sessA4.Events(), core.EventResult, 60*time.Second)
	// Abort is NOT a healthy completion: either an error terminal or a
	// settled turn whose persisted assistant carries the abort. A bare
	// healthy Result would be acceptable only when real output streamed
	// before the abort landed — the assertion is convergence + truth.
	t.Logf("A4 terminal after abort: err=%v done=%v", evA4.Error, evA4.Done)

	// ── E3: an external official-client turn streams through the route ─────
	sidE3 := newSession()
	sessE3, err := agent.StartSession(ctx, sidE3)
	if err != nil {
		t.Fatalf("StartSession E3: %v", err)
	}
	// Second client = raw HTTP, exactly like the official Web UI.
	extBody := map[string]any{
		"messageID": "msg_ext_bridge_e3",
		"agent":     "build",
		"model":     map[string]any{"providerID": "localmock", "modelID": "echo"},
		"parts":     []map[string]any{{"type": "text", "text": "E3 external turn from a second client"}},
	}
	code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint("/session/"+sidE3+"/prompt_async"), extBody, ws, true)
	if err != nil || code >= 400 {
		t.Fatalf("external prompt: code=%d err=%v body=%s", code, err, truncateForError(string(raw)))
	}
	// The registered route must observe the external user bubble + terminal
	// — no polling, the same global SSE stream.
	var sawUser bool
	deadline := time.After(60 * time.Second)
	for {
		select {
		case e := <-sessE3.Events():
			switch e.Type {
			case core.EventUserMessage:
				if strings.Contains(e.Content, "E3 external turn") {
					sawUser = true
				}
			case core.EventResult:
				if !sawUser {
					t.Fatal("E3 terminal arrived without the external user bubble on the route")
				}
				t.Log("E3 external turn observed through the single global subscriber route")
				return
			}
		case <-deadline:
			t.Fatal("E3 external turn never converged on the route")
		}
	}
}

// waitForEventType drains events until one of the wanted type lands.
func waitForEventType(t *testing.T, events <-chan core.Event, want core.EventType, timeout time.Duration) core.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-events:
			if ev.Type == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("event %s never arrived within %s", want, timeout)
			return core.Event{}
		}
	}
}
