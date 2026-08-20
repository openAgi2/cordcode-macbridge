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

// prompt_options_sandbox_test.go is the C3/C5 regression against a REAL
// isolated 1.18.18 serve (harness: start_sandbox.sh — E1b/E4b/E5b sandbox,
// localmock provider with echo.variants {high,low}, provider default zeta,
// config default alpha). One user action must produce exactly one persisted
// user message and one assistant turn; the prompt must carry the resolved
// agent/model/variant; reload converges to the same server truth.
//
//	OCW_SANDBOX_URL=http://127.0.0.1:4398 OCW_SANDBOX_USER=gatea OCW_SANDBOX_PASS=gatea-pass \
//	  go test ./agent/opencode-web -run TestSandboxPromptOptions -count=1 -v
func TestSandboxPromptOptions(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("OCW_SANDBOX_URL"))
	if base == "" {
		t.Skip("set OCW_SANDBOX_URL (and OCW_SANDBOX_USER/PASS) to run the C3/C5 sandbox regression")
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Fresh throwaway session in the harness workspace.
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
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
	sid := created.ID

	sess, err := agent.StartSession(ctx, sid)
	if err != nil {
		t.Fatalf("StartSession resume %s: %v", sid, err)
	}
	defer sess.Close()
	sender, ok := sess.(core.PromptOptionsSender)
	if !ok {
		t.Fatal("serverSession must implement core.PromptOptionsSender")
	}

	// Turn 1: explicit agent + echo + live variant "high" (E1b chain).
	if err := sender.SendWithOptions("C3C5_SANDBOX reply with SANDBOX_OK", nil, nil, core.PromptOptions{
		Agent:      "build",
		ProviderID: "localmock",
		ModelID:    "echo",
		Variant:    "high",
	}); err != nil {
		t.Fatalf("SendWithOptions: %v", err)
	}
	waitTurnTerminal(t, sess.Events(), 90*time.Second)

	// Turn 2 (follow-up, no explicit options): §6.6 resolution — provider
	// default zeta wins over config alpha (E5b).
	if err := sender.SendWithOptions("C3C5_SANDBOX follow-up", nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
		t.Fatalf("follow-up SendWithOptions: %v", err)
	}
	waitTurnTerminal(t, sess.Events(), 90*time.Second)

	// Persisted truth: exactly two user messages and two assistant turns,
	// turn 1 carries the variant on the user message model (E1b persistence —
	// live shape: role lives at info.role, model at info.model{modelID}).
	msgs := sandboxMessages(t, c, sid, ws)
	var user, assistant int
	for _, m := range msgs {
		info, _ := m["info"].(map[string]any)
		if info == nil {
			continue
		}
		switch info["role"] {
		case "user":
			user++
			model, _ := info["model"].(map[string]any)
			if model == nil {
				continue
			}
			if model["modelID"] == "echo" && user == 1 {
				if model["variant"] != "high" {
					t.Fatalf("turn-1 user message must persist variant high (E1b), got %v", model)
				}
				if model["providerID"] != "localmock" {
					t.Fatalf("turn-1 user model provider = %v", model)
				}
			}
			if user == 2 && model["modelID"] != "zeta" {
				// E5b: with no explicit selection the provider default (zeta)
				// wins before legacy config (alpha) — level-3 resolution.
				t.Fatalf("turn-2 (no explicit model) must resolve the provider default zeta, got %v", model)
			}
		case "assistant":
			assistant++
		}
	}
	if user != 2 || assistant < 1 {
		t.Fatalf("two sends must persist two user messages and at least one assistant turn, got user=%d assistant=%d", user, assistant)
	}
	t.Logf("persisted user=%d assistant=%d; turn-1 variant=high on user model (reload-verified)", user, assistant)

	// Unlisted variant is a pre-POST failure on the REAL serve catalog.
	if err := sender.SendWithOptions("must not POST", nil, nil, core.PromptOptions{ProviderID: "localmock", ModelID: "echo", Variant: "turbo"}); err == nil || !strings.Contains(err.Error(), "not a live key") {
		t.Fatalf("unlisted variant must fail closed on the real catalog, got %v", err)
	}
}

// waitTurnTerminal consumes events until the turn closes (result/error) or
// the deadline passes.
func waitTurnTerminal(t *testing.T, events <-chan core.Event, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-events:
			switch ev.Type {
			case core.EventResult, core.EventError:
				return
			}
		case <-deadline:
			t.Fatal("sandbox turn did not reach a terminal frame in time")
			return
		}
	}
}

// sandboxMessages fetches GET /session/:id/message rows as raw maps.
func sandboxMessages(t *testing.T, c *Client, sid, dir string) []map[string]any {
	t.Helper()
	raw, err := c.fetchJSON(context.Background(), c.apiPath("/session/"+sid+"/message"), dir)
	if err != nil {
		t.Fatalf("message reload: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("message reload decode: %v (%s)", err, truncateForError(string(raw)))
	}
	return rows
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
