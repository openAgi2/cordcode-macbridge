package gobridge

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSessionIDSet(t *testing.T) {
	set := sessionIDSet([]core.AgentSessionInfo{
		{ID: "s1"}, {ID: " s2 "}, {ID: ""}, {ID: "s1"}, // dup + empty + trimmed
	})
	if len(set) != 2 || !set["s1"] || !set["s2"] {
		t.Fatalf("set = %v, want {s1, s2} (empty dropped, trimmed, deduped)", set)
	}
}

func TestDiffNewSessions(t *testing.T) {
	prev := map[string]bool{"a": true, "b": true}
	current := map[string]bool{"a": true, "b": true, "c": true, "d": true}
	got := diffNewSessions(prev, current)
	if len(got) != 2 {
		t.Fatalf("diff = %v, want 2 new (c,d)", got)
	}
	for _, id := range got {
		if id != "c" && id != "d" {
			t.Fatalf("unexpected new id %q", id)
		}
	}
	// No growth → no diff.
	if diff := diffNewSessions(current, current); len(diff) != 0 {
		t.Fatalf("diff of equal sets = %v, want empty", diff)
	}
	// Removed sessions are not "new".
	if diff := diffNewSessions(current, prev); len(diff) != 0 {
		t.Fatalf("diff on shrink = %v, want empty (removals are not new)", diff)
	}
}

// TestSessionDiscoveryBroadcastsOnNewSession: watcher detects a new session ID
// across snapshots and broadcasts "sessions_changed" to a subscribed client.
func TestSessionDiscoveryBroadcastsOnNewSession(t *testing.T) {
	prev := sessionDiscoveryInterval
	sessionDiscoveryInterval = 10 * time.Millisecond
	t.Cleanup(func() { sessionDiscoveryInterval = prev })

	handlers := newTestHandlers(t)
	agent := &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{{ID: "s1"}}}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: "list-view"})

	seen := map[string]map[string]bool{}
	// Seed (s1) — no broadcast.
	handlers.snapshotSessions(context.Background(), seen, true)
	// New session s2 appears.
	agent.sessionInfos = []core.AgentSessionInfo{{ID: "s1"}, {ID: "s2"}}
	handlers.snapshotSessions(context.Background(), seen, false)

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := clientConn.ReadJSON(&payload); err != nil {
		t.Fatalf("read sessions_changed: %v", err)
	}
	if got := payload["event"]; got != "sessions_changed" {
		t.Fatalf("event = %#v, want sessions_changed", got)
	}
	data, _ := payload["data"].(map[string]any)
	if data["backendId"] != "codex" {
		t.Fatalf("data = %#v, want backendId=codex", data)
	}
}
