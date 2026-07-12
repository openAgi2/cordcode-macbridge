package grokbuild

// Diagnostic tests for newGrokSession / callRPC timeout behavior.
// These tests verify that callRPC's select-timer and stdin.Write behave as
// expected under adversarial child processes. See:
// docs/2026-07-12-grok-send-message-rpc-timeout-analysis.md (iOS repo)

import (
	"bytes"
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// silentChildScript reads stdin (so writeRequest completes) but never writes
// stdout — simulates a CLI that started but hung before sending ACP responses.
const silentChildScript = "while(<STDIN>){} while(1){sleep 1}"

// Test A: no-response child — verifies callRPC's select timer fires.
// The child reads stdin so writeRequest completes; callRPC then enters its
// select and should hit the 10s initialize timeout. This does NOT cover the
// case where stdin.Write itself blocks (see Test B).
func TestCallRPC_ResponseTimeoutOnSilentChild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow timeout test in -short mode")
	}
	agent := &Agent{
		cliBin:  "perl",
		workDir: t.TempDir(),
		mode:    "default",
	}
	agent.cliExtraArgs = []string{"-e", silentChildScript}

	start := time.Now()
	sess, err := newGrokSession(context.Background(), agent, "test-session-id")
	elapsed := time.Since(start)

	// Must return an error and nil session. Checking only err==nil would
	// pass on the sess==nil && err==nil edge case.
	if sess != nil {
		sess.Close()
		t.Fatalf("expected nil session from silent child")
	}
	if err == nil {
		t.Fatalf("expected non-nil error from silent child, got nil")
	}
	// initialize callRPC timeout = 10s; allow margin for process spawn.
	if elapsed > 15*time.Second {
		t.Fatalf("newGrokSession took %v; expected <=15s (10s select timeout + spawn)", elapsed)
	}
	t.Logf("returned in %v, error_class=%s", elapsed, rpcErrorClass(err))
}

// Test B: full-pipe child — verifies stdin.Write blocks when the OS pipe
// buffer is full and the child never reads stdin. Also checks whether context
// cancel can unblock the write (via exec.CommandContext killing the child).
//
// The child does NOT read stdin and does NOT write stdout — it just sleeps.
// We bypass newGrokSession and test stdin.Write directly with looped writes
// to exceed the pipe buffer capacity.
func TestStdinWrite_BlocksWhenPipeBufferFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "perl", "-e", "sleep 9999")
	cmd.Dir = t.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if _, err := cmd.StdoutPipe(); err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer func() {
		cancel()   // exec.CommandContext ctx cancel → SIGKILL child → pipe close
		cmd.Wait() // reap
		_ = stdin.Close()
	}()

	chunk := make([]byte, 64*1024) // 64KB per write

	// totalWritten is written by the writer goroutine and read by the main
	// goroutine in the timeout branch. Use atomic to avoid a data race
	// (go test -race would flag a bare int).
	var totalWritten atomic.Int64

	type writeResult struct {
		total int64  // snapshot of cumulative bytes at completion
		n     int    // last write's return value
		err   error
	}
	writeDone := make(chan writeResult, 1)

	go func() {
		for {
			n, err := stdin.Write(chunk)
			total := totalWritten.Add(int64(n))
			if err != nil {
				writeDone <- writeResult{total, n, err}
				return
			}
			if n < len(chunk) {
				// partial write — pipe buffer full, record as backpressure signal
				writeDone <- writeResult{total, n, nil}
				return
			}
		}
	}()

	select {
	case res := <-writeDone:
		if res.err != nil {
			t.Logf("stdin.Write returned error after %dKB: %v", res.total/1024, res.err)
		} else {
			t.Logf("stdin.Write did NOT block; wrote %dKB (partial=%t, last_n=%d). "+
				"Pipe buffer may be very large or child read some data.",
				res.total/1024, res.n < len(chunk), res.n)
		}

	case <-time.After(3 * time.Second):
		// Write blocked 3s → pipe buffer full, no consumer, no timeout guard.
		snap := totalWritten.Load()
		t.Logf("stdin.Write BLOCKED after writing %dKB (pipe buffer full, no consumer)", snap/1024)

		// Verify: can context cancel interrupt the blocked Write?
		cancel()
		select {
		case res := <-writeDone:
			t.Logf("stdin.Write unblocked after context cancel (total %dKB, err=%v) — "+
				"ctx cancel frees blocked write via child kill", res.total/1024, res.err)
		case <-time.After(5 * time.Second):
			t.Errorf("stdin.Write still blocked 5s after context cancel — no cancel guard on stdin.Write")
		}
	}
}

// Ensure bytes import is used (for potential future chunk construction).
var _ = bytes.Repeat
