package gobridge

// §16 gate 7: reducer frozen samples through the REAL pipeline — fake DSH
// runtime process → agent/dsh codec → handleSendMessage relay → mapAgentEvent
// → EventPublisher → ProjectionReducer. Asserts user/assistant same turn,
// plugin user/message never entering the timeline, turn/step mismatch failing
// visibly (turn never settles completed), and per-spawn nonces keeping
// restart TurnIDs distinct.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/dsh"
)

// dshFakeRuntimeTemplate: mode is baked in at write time (the driver env
// allowlist forwards only runtime vars — exactly the production behavior).
//
//	ok       full legal turn incl. the user+plugin user/message double shape
//	mismatch turn-scoped chunk carrying a WRONG turn number mid-turn
const dshFakeRuntimeTemplate = `#!/usr/bin/env python3
import json, sys

mode = %q

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

def notify(method, params):
    send({"jsonrpc": "2.0", "method": method, "params": params})

def resp(rid, result):
    send({"jsonrpc": "2.0", "id": rid, "result": result})

sid = "fake-root"
seq = 0
def ev(typ, data):
    global seq
    notify("session.event", {"sessionId": sid, "event": {"type": typ, "seq": seq, "time": 0, "data": data}})
    seq += 1

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    m = msg.get("method")
    if m == "initialize":
        resp(msg["id"], {"serverInfo": {"name": "deepseek-harness-sdk-runtime", "version": "0.0.1"}})
    elif m == "session/prompt":
        p = msg["params"]
        sid = p["sessionId"]
        resp(msg["id"], {"messageId": "m-" + str(msg["id"])})
        ev("turn/start", {"turn": 1})
        ev("step/start", {"turn": 1, "step": 1})
        ev("user/message", {"content": [{"type": "text", "text": p["contentBlocks"][0]["text"]}], "source": {"kind": "user"}, "role": "user", "id": "u-real"})
        ev("user/message", {"content": [{"type": "text", "text": "permission runtime context, must never enter the timeline"}], "source": {"kind": "plugin"}, "role": "user", "id": "u-plugin"})
        ev("request/context", {"provider": "deepseek-official", "model": "deepseek-chat", "contextWindow": 1000000})
        ev("assistant/chunk", {"turn": 1, "step": 1, "chunk": {"type": "block-start", "index": 0, "blockType": "text"}})
        if mode == "mismatch":
            ev("assistant/chunk", {"turn": 7, "step": 1, "chunk": {"type": "text-delta", "index": 0, "text": "poison"}})
            continue
        ev("assistant/chunk", {"turn": 1, "step": 1, "chunk": {"type": "text-delta", "index": 0, "text": "hi"}})
        ev("assistant/chunk", {"turn": 1, "step": 1, "chunk": {"type": "block-end", "index": 0, "block": {"type": "text", "text": "hi"}}})
        ev("assistant/chunk", {"turn": 1, "step": 1, "chunk": {"type": "usage", "usage": {"inputTokens": 20, "outputTokens": 3, "cacheReadTokens": 100, "reasoningTokens": 0}}})
        ev("step/end", {"turn": 1, "step": 1})
        ev("turn/end", {"turn": 1, "reason": {"kind": "completed"}})
        notify("session.status", {"sessionId": sid, "status": "idle"})
    elif m == "shutdown":
        resp(msg["id"], {})
        sys.exit(0)
`

func newDSHFakeRuntime(t *testing.T, mode string) map[string]any {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "dsh-jsonrpc-agent")
	if err := os.WriteFile(script, []byte(fmt.Sprintf(dshFakeRuntimeTemplate, mode)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "cordis.yml")
	if err := os.WriteFile(cfg, []byte("- id: fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"cli_path":    script,
		"config_path": cfg,
		"work_dir":    t.TempDir(),
	}
}

// readUntilEvent reads frames from the test client until one carries the
// wanted event name (or the deadline expires).
func readUntilEvent(t *testing.T, read func(int) []map[string]any, want string, cap int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	total := 0
	for time.Now().Before(deadline) {
		frames := read(1)
		for _, f := range frames {
			total++
			if f["event"] == want {
				return f
			}
			if total > cap {
				t.Fatalf("gave up after %d frames without %q", total, want)
			}
		}
	}
	t.Fatalf("timed out waiting for %q", want)
	return nil
}

// Frozen sample 1+2: user and assistant land in the SAME turn; the plugin
// user/message never reaches the timeline.
func TestDSHReducerFrozenSamples_UserAssistantSameTurn_PluginExcluded(t *testing.T) {
	agent, err := dsh.New(newDSHFakeRuntime(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r1",
		Params: []byte(`{"sessionId":"ses-dsh-frozen","content":"real user prompt"}`),
	}, agent)

	readUntilEvent(t, func(n int) []map[string]any { return readJSONMaps(t, clientConn, n) }, "turn_completed", 32)

	snap, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("deepseek", "ses-dsh-frozen")
	if !ok {
		t.Fatal("no projection snapshot after completed turn")
	}
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snap.Turns))
	}
	turn := snap.Turns[0]
	if turn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", turn.Status)
	}
	if turn.User == nil {
		t.Fatalf("frozen sample: turn must carry the user message part")
	}
	var userText string
	for _, p := range turn.User.Parts {
		if p.Type == "text" {
			userText += p.Text
		}
	}
	if !strings.Contains(userText, "real user prompt") {
		t.Fatalf("user part text = %q", userText)
	}
	if strings.Contains(userText, "permission runtime context") {
		t.Fatalf("plugin user/message leaked into the timeline: %q", userText)
	}
	if turn.Assistant == nil {
		t.Fatalf("frozen sample: turn must carry the assistant message")
	}
	var assistantText string
	for _, p := range turn.Assistant.Parts {
		if p.Type == "text" {
			assistantText += p.Text
		}
	}
	if assistantText != "hi" {
		t.Fatalf("assistant text = %q, want %q", assistantText, "hi")
	}
}

// Frozen sample 3: a turn-scoped frame with a mismatched source turn fails
// visibly — the poisoned turn NEVER settles completed.
func TestDSHReducerFrozenSamples_TurnMismatchFailsVisibly(t *testing.T) {
	agent, err := dsh.New(newDSHFakeRuntime(t, "mismatch"))
	if err != nil {
		t.Fatal(err)
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r1",
		Params: []byte(`{"sessionId":"ses-dsh-mismatch","content":"x"}`),
	}, agent)

	readUntilEvent(t, func(n int) []map[string]any { return readJSONMaps(t, clientConn, n) }, "error", 32)

	snap, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("deepseek", "ses-dsh-mismatch")
	if !ok {
		t.Fatal("no projection snapshot")
	}
	for _, turn := range snap.Turns {
		if turn.Status == "completed" {
			t.Fatalf("mismatch-poisoned turn settled completed: %+v", turn)
		}
		if turn.Assistant == nil {
			continue
		}
		for _, p := range turn.Assistant.Parts {
			if p.Type == "text" && strings.Contains(p.Text, "poison") {
				t.Fatalf("mismatched-frame content entered the timeline: %+v", turn.Assistant.Parts)
			}
		}
	}
}

// Frozen sample 4: every spawn draws a fresh nonce — restarts never collide in
// the projection's turn-key space.
func TestDSHReducerFrozenSamples_NonceNoCollisionAcrossSpawns(t *testing.T) {
	agent, err := dsh.New(newDSHFakeRuntime(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	turnIDFor := func(sessionID string) string {
		handlers.handleSendMessage(serverConn, WireMessage{
			BackendID: "deepseek", Method: "send_message", RequestID: "r-" + sessionID,
			Params: []byte(`{"sessionId":"` + sessionID + `","content":"hi"}`),
		}, agent)
		frame := readUntilEvent(t, func(n int) []map[string]any { return readJSONMaps(t, clientConn, n) }, "turn_completed", 32)
		data, _ := frame["data"].(map[string]any)
		turnID, _ := data["turnId"].(string)
		if turnID == "" {
			t.Fatalf("turn_completed without turnId: %#v", frame)
		}
		return turnID
	}

	first := turnIDFor("ses-dsh-nonce-a")
	second := turnIDFor("ses-dsh-nonce-b")

	// Same turn number (t1), different process nonce → different TurnID.
	if first == second {
		t.Fatalf("TurnIDs from different spawns collided: %q", first)
	}
	for _, id := range []string{first, second} {
		if !strings.HasPrefix(id, "p") || !strings.Contains(id, "-t1") {
			t.Fatalf("TurnID not in p{nonce}-t{turn} form: %q", id)
		}
	}

	// Both sessions' projections carry their own single completed turn.
	for _, sessionID := range []string{"ses-dsh-nonce-a", "ses-dsh-nonce-b"} {
		snap, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("deepseek", sessionID)
		if !ok || len(snap.Turns) != 1 || snap.Turns[0].Status != "completed" {
			t.Fatalf("session %s projection: ok=%v turns=%+v", sessionID, ok, snap.Turns)
		}
	}
}
