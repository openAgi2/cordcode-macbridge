package grokbuild

// Regression test for the 2026-08-14 handshake replay deadlock (production
// session 019ff1b3…: 3232 updates.jsonl replay lines):
//
// session/load makes the Grok CLI replay historical session/update
// notifications BEFORE answering the load response. Pre-fix, emit() performed
// a blocking send into the 64-slot events channel with no consumer attached
// during handshake, so once the replay exceeded 64 events readLoop froze in
// emit(), the session/load response (already sitting in the OS pipe) was
// never read, and callRPC timed out at 15s — every send on a large session
// failed with "timeout waiting for response to request 3".
//
// The fake agent below answers initialize, then floods 300
// agent_thought_chunk notifications before the session/load response. The
// flood fits inside the OS pipe buffer (~40KB < 64KB), so the child can
// always finish writing — the only way the handshake can fail is our own
// readLoop blocking on emit.
//
// Evidence & analysis: docs/2026-08-14-opencode-empty-turn-and-grokbuild-session-load-timeout-analysis.md
// (iOS repo, §3).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// replayFloodAgent: line-oriented ACP JSON-RPC over stdio. Extra argv
// (newGrokSession always appends "agent --no-leader stdio") is ignored.
//   - initialize → loadSession=true (no authMethods, so no authenticate call)
//   - session/load → 300 session/update notifications, THEN the response
//   - anything else → empty result
const replayFloodAgent = `#!/usr/bin/perl
use strict; use warnings; use JSON::PP;
$| = 1;
while (my $line = <STDIN>) {
	my $req = eval { decode_json($line) } or next;
	my ($id, $method) = ($req->{id}, $req->{method});
	if ($method eq 'initialize') {
		print encode_json({jsonrpc => '2.0', id => $id, result => {
			protocolVersion => 1,
			agentCapabilities => {loadSession => JSON::PP::true()},
		}}), "\n";
	} elsif ($method eq 'session/load') {
		for my $i (1 .. 300) {
			print encode_json({jsonrpc => '2.0', method => 'session/update', params => {
				update => {sessionUpdate => 'agent_thought_chunk',
				           content => {type => 'text', text => 'replay'}},
			}}), "\n";
		}
		print encode_json({jsonrpc => '2.0', id => $id, result => {}}), "\n";
	} else {
		print encode_json({jsonrpc => '2.0', id => $id, result => {}}), "\n";
	}
}
`

func TestSessionLoadReplayFloodDoesNotDeadlockHandshake(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow handshake test in -short mode")
	}
	cliPath := filepath.Join(t.TempDir(), "fake-grok-agent")
	if err := os.WriteFile(cliPath, []byte(replayFloodAgent), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	agent := &Agent{
		cliBin:  cliPath,
		workDir: t.TempDir(),
		mode:    "default",
	}

	start := time.Now()
	sess, err := newGrokSession(context.Background(), agent, "test-replay-session")
	elapsed := time.Since(start)

	if err != nil {
		if sess != nil {
			sess.Close()
		}
		t.Fatalf("handshake deadlocked on replay flood: err=%v (elapsed=%v, "+
			"expected success — emit must not block readLoop during handshake)", err, elapsed)
	}
	// Pre-fix failure mode: 15s session/load timeout. Leave margin for spawn.
	if elapsed > 10*time.Second {
		sess.Close()
		t.Fatalf("handshake took %v; expected well under the 15s session/load timeout", elapsed)
	}
	t.Logf("handshake succeeded in %v despite 300-event replay flood", elapsed)

	// The replay is historical state: post-handshake the channel must be empty
	// (drained + overflow discarded) and the terminal-event latch reset so the
	// real turn's Done event is not suppressed.
	select {
	case ev := <-sess.events:
		sess.Close()
		t.Fatalf("stale handshake replay leaked to consumers: %+v", ev)
	default:
	}
	if sess.terminalDone.Load() {
		sess.Close()
		t.Fatalf("terminalDone still set after handshake drain; real turn terminal event would be suppressed")
	}
	if !sess.alive.Load() {
		sess.Close()
		t.Fatalf("session not alive after successful handshake")
	}
	sess.Close()
}
