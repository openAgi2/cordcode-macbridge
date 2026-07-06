package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestOpencodeServerSessionLiveStreaming drives a real turn through the managed
// opencode server via the NEW opencodeServerSession code path, asserting that
// message.part.delta arrives as multiple EventText frames (real streaming, not
// the batch CLI's single frame) and the turn completes with EventResult.
//
// Gated by OPENCODE_LIVE=1 + the presence of the CordCode managed-server state
// file. Does NOT run in CI. Validates the Phase 2 fix end-to-end against the
// live server before handing to owner for the iOS real-device test.
func TestOpencodeServerSessionLiveStreaming(t *testing.T) {
	if os.Getenv("OPENCODE_LIVE") != "1" {
		t.Skip("set OPENCODE_LIVE=1 with a running CordCode managed opencode server")
	}
	home, _ := os.UserHomeDir()
	statePath := filepath.Join(home, "Library/Application Support/CordCode Link/opencode-managed-server.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Skipf("managed server state not found: %v", err)
	}
	var state struct {
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("state parse: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	a := &Agent{
		httpBaseURL:    state.URL,
		httpAuthHeader: "Basic " + basicAuth(state.Username, state.Password),
	}

	sess, err := newOpencodeServerSession(ctx, a, "", "opencode", "mimo-v2.5-free")
	if err != nil {
		t.Fatalf("newOpencodeServerSession: %v", err)
	}
	defer sess.Close()
	t.Logf("server session id: %s", sess.CurrentSessionID())

	if err := sess.Send("用中文写一首30字的关于秋天的诗", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	textFrames := 0
	var lastText string
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatalf("events channel closed before EventResult (textFrames=%d)", textFrames)
			}
			switch ev.Type {
			case core.EventText:
				textFrames++
				lastText += ev.Content
			case core.EventError:
				t.Fatalf("EventError: %v", ev.Error)
			case core.EventResult:
				t.Logf("DIAG: textFrames=%d (streaming >= several; batch CLI = 1); assistant text sample=%q",
					textFrames, truncate(lastText, 60))
				if textFrames < 3 {
					t.Errorf("expected real streaming (>=3 text frames over generation), got %d", textFrames)
				}
				return
			}
		case <-time.After(70 * time.Second):
			t.Fatalf("timeout waiting for EventResult (textFrames=%d)", textFrames)
		}
	}
}
