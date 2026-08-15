package dsh

// Phase-3 fault-injection tests (design §3.6.3, §16 gates 5+6 driver side):
// delivery fault matrix and process lifecycle against a scripted fake
// runtime. No real key, no real DeepSeek endpoint.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeRuntimeScript is a python3 stand-in for dsh-jsonrpc-agent. The mode
// line is baked in at write time (the driver's env allowlist intentionally
// forwards only runtime vars, so a DSH_FAKE_MODE env would never reach the
// child — which is exactly the production behavior under test):
//
//	ok            initialize → prompt receipt → full legal turn → idle
//	die-on-prompt initialize, then exit BEFORE responding to session/prompt
//	              (request written, receipt lost)
//	turn-error    legal turn/end with reason "error" (application error)
//	garbage-line  emit a non-JSON line mid-turn (framing violation)
const fakeRuntimeScriptTemplate = `#!/usr/bin/env python3
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
        if mode == "die-on-prompt":
            sys.exit(3)
        resp(msg["id"], {"messageId": "m-" + str(msg["id"])})
        ev("turn/start", {"turn": 1})
        ev("step/start", {"turn": 1, "step": 1})
        ev("user/message", {"content": [{"type": "text", "text": p["contentBlocks"][0]["text"]}], "source": {"kind": "user"}, "role": "user", "id": "u1"})
        if mode == "turn-error":
            ev("step/end", {"turn": 1, "step": 1})
            ev("turn/end", {"turn": 1, "reason": {"kind": "error"}})
        elif mode == "garbage-line":
            sys.stdout.write("this is not json\n")
            sys.stdout.flush()
        else:
            ev("assistant/chunk", {"turn": 1, "step": 1, "chunk": {"type": "text-delta", "index": 0, "text": "hi"}})
            ev("step/end", {"turn": 1, "step": 1})
            ev("turn/end", {"turn": 1, "reason": {"kind": "completed"}})
        notify("session.status", {"sessionId": sid, "status": "idle"})
    elif m == "shutdown":
        resp(msg["id"], {})
        sys.exit(0)
`

// newFakeRuntime writes the scripted runtime (mode baked in) and returns its
// absolute path.
func newFakeRuntime(t *testing.T, mode string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "dsh-jsonrpc-agent")
	if err := os.WriteFile(script, []byte(fmt.Sprintf(fakeRuntimeScriptTemplate, mode)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cordis.yml"), []byte("- id: fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return script, filepath.Join(dir, "cordis.yml")
}

func newFaultAgent(t *testing.T, mode string) *Agent {
	t.Helper()
	cli, cfg := newFakeRuntime(t, mode)
	return &Agent{
		workDir:     t.TempDir(),
		cliBin:      cli,
		configPath:  cfg,
		model:       defaultModel,
		mode:        "workspace-write",
		activeIdx:   -1,
		receiptWait: 5 * time.Second,
	}
}

// waitForEvent polls the session's event stream until pred matches or the
// deadline expires.
func waitForEvent(t *testing.T, s *dshSession, timeout time.Duration, pred func(core.Event) bool) core.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				t.Fatal("events channel closed before matching event")
			}
			if pred(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for matching event")
			return core.Event{}
		}
	}
}

func isTerminal(ev core.Event) bool {
	return ev.Done
}

// Scenario A: happy path — full legal turn mapped end to end.
func TestLifecycleHappyPathTurn(t *testing.T) {
	agent := newFaultAgent(t, "ok")
	s, err := newDshSession(context.Background(), agent, "ses-happy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Send("hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	term := waitForEvent(t, s, 5*time.Second, func(ev core.Event) bool {
		return ev.Type == core.EventResult && ev.Done
	})
	if term.Error != nil {
		t.Fatalf("completed turn must not carry an error: %v", term.Error)
	}
	if term.TurnID != "p"+s.nonce+"-t1" {
		t.Fatalf("terminal TurnID = %q", term.TurnID)
	}
}

// Scenario B / 五场景4: application error (legal turn/end reason=error) closes
// the TURN with a visible failure but KEEPS the process — the next send works.
func TestLifecycleApplicationErrorKeepsSession(t *testing.T) {
	agent := newFaultAgent(t, "turn-error")
	s, err := newDshSession(context.Background(), agent, "ses-apperr")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Send("one", nil, nil); err != nil {
		t.Fatal(err)
	}
	term := waitForEvent(t, s, 5*time.Second, isTerminal)
	if term.Type != core.EventResult || term.Error == nil {
		t.Fatalf("application error must settle as turn_error, got %+v", term)
	}

	// The process survived: the next turn runs on the SAME session.
	deadline := time.Now().Add(2 * time.Second)
	for !s.Alive() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.Alive() {
		t.Fatal("application error must not kill the process")
	}
	if err := s.Send("two", nil, nil); err != nil {
		t.Fatalf("second turn after application error failed: %v", err)
	}
	term2 := waitForEvent(t, s, 5*time.Second, isTerminal)
	if term2.Error == nil {
		t.Fatalf("second application-error turn expected, got %+v", term2)
	}
}

// Scenario C: framing violation (non-JSON line) — visible terminal + process
// death (fatal class; the decoder must not serve another turn).
func TestLifecycleFramingViolationKillsProcess(t *testing.T) {
	agent := newFaultAgent(t, "garbage-line")
	s, err := newDshSession(context.Background(), agent, "ses-garbage")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Send("x", nil, nil); err != nil {
		t.Fatal(err)
	}
	term := waitForEvent(t, s, 5*time.Second, isTerminal)
	if term.Type != core.EventError {
		t.Fatalf("framing violation must surface as terminal error, got %+v", term)
	}

	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("process must be dead after framing violation")
	}
	if s.Alive() {
		t.Fatal("alive must be false after fatal violation")
	}
	// One terminal only — waitForEvent already consumed it; any additional
	// Done event in the stream would be a duplicated terminal.
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				goto done
			}
			if ev.Done {
				t.Fatalf("duplicated terminal event: %+v", ev)
			}
		default:
			goto done
		}
	}
done:
}

// §16 gate 5 (pre-write): dead session at the pre-send check → typed
// StagePreWrite, replay allowed.
func TestDeliveryPreWriteAfterProcessDeath(t *testing.T) {
	agent := newFaultAgent(t, "ok")
	s, err := newDshSession(context.Background(), agent, "ses-dead")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	<-s.done

	err = s.Send("again", nil, nil)
	var de *core.DeliveryError
	if !errors.As(err, &de) {
		t.Fatalf("want *core.DeliveryError, got %T: %v", err, err)
	}
	if de.Stage != core.StagePreWrite {
		t.Fatalf("stage = %s, want pre_write", de.Stage)
	}
	if !de.ReplayAllowed() {
		t.Fatal("pre_write must allow one repair send")
	}
}

// §16 gate 5 (response lost): request fully written, process dies before the
// receipt — StageAwaitingResponse, replay forbidden.
func TestDeliveryResponseLostAwaitingResponse(t *testing.T) {
	agent := newFaultAgent(t, "die-on-prompt")
	s, err := newDshSession(context.Background(), agent, "ses-lost")
	if err != nil {
		t.Fatal(err)
	}

	err = s.Send("maybe-enqueued", nil, nil)
	var de *core.DeliveryError
	if !errors.As(err, &de) {
		t.Fatalf("want *core.DeliveryError, got %T: %v", err, err)
	}
	if de.Stage != core.StageAwaitingResponse {
		t.Fatalf("stage = %s, want awaiting_response", de.Stage)
	}
	if de.ReplayAllowed() {
		t.Fatal("awaiting_response must forbid replay (prompt may be enqueued)")
	}
}

// Write-phase classification unit (zero-byte vs partial, §3.6.3③).
func TestDeliveryWriteStageClassification(t *testing.T) {
	// partial: some bytes moved before failure
	s := &dshSession{stdin: stubWriter{n: 5, err: errors.New("EPIPE")}}
	err := s.writeRequest(1, "session/prompt", map[string]any{"x": 1})
	var de *core.DeliveryError
	if !errors.As(err, &de) || de.Stage != core.StagePartialWrite {
		t.Fatalf("partial write: %+v", err)
	}
	if de.ReplayAllowed() {
		t.Fatal("partial write must forbid replay")
	}
	// zero-byte: provably undelivered
	s2 := &dshSession{stdin: stubWriter{n: 0, err: errors.New("EPIPE")}}
	err = s2.writeRequest(1, "session/prompt", map[string]any{"x": 1})
	if !errors.As(err, &de) || de.Stage != core.StagePreWrite {
		t.Fatalf("zero-byte write: %+v", err)
	}
}

type stubWriter struct {
	n   int
	err error
}

func (w stubWriter) Write(b []byte) (int, error) { return w.n, w.err }
func (w stubWriter) Close() error                { return nil }

// 五场景 respawn evidence: every spawn draws a fresh 128-bit nonce, so
// TurnIDs across process generations never collide.
func TestProcessNonceFreshPerSpawn(t *testing.T) {
	agent := newFaultAgent(t, "ok")
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		s, err := newDshSession(context.Background(), agent, "ses-nonce")
		if err != nil {
			t.Fatal(err)
		}
		if seen[s.nonce] {
			_ = s.Close()
			t.Fatalf("nonce reused across spawns: %s", s.nonce)
		}
		seen[s.nonce] = true
		if len(s.nonce) != 32 {
			_ = s.Close()
			t.Fatalf("nonce must be 16 bytes hex (32 chars), got %q", s.nonce)
		}
		_ = s.Close()
	}
}

// Close must be idempotent across the CAS-winner/loser and abort paths —
// one process, one reap.
func TestCloseIdempotent(t *testing.T) {
	agent := newFaultAgent(t, "ok")
	s, err := newDshSession(context.Background(), agent, "ses-close")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Second Close (CAS loser / abort race) must not panic or hang.
	done := make(chan struct{})
	go func() {
		_ = s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("second Close hung")
	}
}
