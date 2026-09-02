package grokbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Roster broadcast consumption (x.ai/sessions/changed). The leader broadcasts
// it machine-wide to every connected client (grok-build leader/server.rs
// is_machine_wide_broadcast_notification); MacBridge turns it into a
// core.CatalogRefreshSignal — an immediate authoritative fingerprint rescan —
// instead of applying the roster delta locally. Payload sample below is the
// official fixture from grok-build server_tests.rs (camelCase RosterChanged).

func TestIsRosterChangedMethodWireForms(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   bool
	}{
		{"x.ai/sessions/changed", true},
		{"_x.ai/sessions/changed", true},
		{"_x.ai/sessions/list", false},
		{"_x.ai/models/update", false},
		{"session/update", false},
		{"", false},
	} {
		if got := isRosterChangedMethod(tc.method); got != tc.want {
			t.Errorf("isRosterChangedMethod(%q) = %v, want %v", tc.method, got, tc.want)
		}
	}
}

// TestLeaderSubscriberRosterFiresSignalAndStaysOutOfEventStream: a live
// subscription receives the machine-wide roster broadcast — the roster callback
// fires, the frame does NOT enter the core.Event stream, and session/update
// forwarding is unaffected.
func TestLeaderSubscriberRosterFiresSignalAndStaysOutOfEventStream(t *testing.T) {
	sock := filepath.Join("/tmp", fmt.Sprintf("cc-grok-leader-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var got []core.Event
	var mu sync.Mutex
	onEvent := func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	rosterFired := make(chan int, 4)
	onRoster := func() { rosterFired <- 1 }

	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()

		reg, err := readClientMsg(c)
		if err != nil {
			serverErr <- err
			return
		}
		if reg.Type != "register" || reg.ClientType != leaderClientType {
			serverErr <- fmt.Errorf("want register/%s, got %s/%s", leaderClientType, reg.Type, reg.ClientType)
			return
		}
		rr, _ := json.Marshal(leaderServerMsg{Type: "registered", Ready: true})
		if err := writeTestFrame(c, rr); err != nil {
			serverErr <- err
			return
		}

		init, err := readClientMsg(c)
		if err != nil {
			serverErr <- err
			return
		}
		if err := writeACPResponse(c, acpPayloadID(init.Payload), map[string]any{"protocolVersion": "1"}); err != nil {
			serverErr <- err
			return
		}

		load, err := readClientMsg(c)
		if err != nil {
			serverErr <- err
			return
		}
		if err := writeACPResponse(c, acpPayloadID(load.Payload), map[string]any{}); err != nil {
			serverErr <- err
			return
		}

		// Official fixture (grok-build server_tests.rs:4419) — ext-wrapped form.
		if err := writeACPNotification(c, "_x.ai/sessions/changed", map[string]any{
			"upserted": []map[string]any{{
				"sessionId":        "sess-roster",
				"cwd":              "/repo",
				"isWorktree":       false,
				"yolo":             false,
				"activity":         "working",
				"resident":         true,
				"lastChangeUnixMs": 1,
				"origin":           map[string]any{"kind": "local"},
			}},
			"removed": []string{},
		}); err != nil {
			serverErr <- err
			return
		}
		// Bare (unwrapped) form also occurs on the wire — official tests
		// exercise both shapes; both must fire the signal.
		if err := writeACPNotification(c, "x.ai/sessions/changed", map[string]any{
			"upserted": []any{},
			"removed":  []string{"sess-gone"},
		}); err != nil {
			serverErr <- err
			return
		}
		// A live session/update must still flow through the codec untouched.
		if err := writeACPNotification(c, "session/update", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "after roster"}},
		}); err != nil {
			serverErr <- err
			return
		}
		time.Sleep(150 * time.Millisecond)
		serverErr <- nil
	}()

	sub := NewLeaderSubscriber(sock, "sess-1", "/tmp")
	sub.onRosterChanged = onRoster
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = sub.Run(ctx, onEvent)

	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}

	// Both roster frames (ext-wrapped + bare) fired the signal.
	if n := len(rosterFired); n != 2 {
		t.Fatalf("roster signal fired %d times, want 2 (both wire forms)", n)
	}

	// The roster frames must NOT leak into the event stream — only the live
	// session/update text survives.
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 (roster frames stay out of the stream): %+v", len(got), got)
	}
	if got[0].Type != core.EventText || got[0].Content != "after roster" {
		t.Fatalf("got[0] = %+v, want EventText 'after roster'", got[0])
	}
}

// TestAgentCatalogRefreshSignalCoalesces: N live subscriber connections each
// deliver the machine-wide broadcast; the buffered-1 channel keeps at most one
// pending rescan.
func TestAgentCatalogRefreshSignalCoalesces(t *testing.T) {
	a := &Agent{catalogRefresh: make(chan struct{}, 1)}
	for i := 0; i < 5; i++ {
		a.signalCatalogRefresh()
	}
	select {
	case <-a.CatalogRefreshSignals():
	default:
		t.Fatal("expected one pending catalog refresh signal")
	}
	select {
	case <-a.CatalogRefreshSignals():
		t.Fatal("signal must coalesce to a single pending rescan")
	default:
	}
}
