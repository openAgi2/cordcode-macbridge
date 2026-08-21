package gobridge

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestOpenCodeWebTodosUpdatedReachesSyncV2ObservedClient pins the Mac-side
// half of the todo-deck contract (owner 真机 2026-08-21): a syncV2 client with
// set_observation_scope(full_stream) + session subscription MUST receive the
// raw todos_updated frame with the full replacement list when opencode-web
// relayEvents forwards core.EventPlan. The iOS-side gap-gate drop of this very
// frame was fixed in the adjacent cordcode-ios repo; if this test ever fails,
// the wire delivery itself regressed (deny-list / observation / broadcaster).
func TestOpenCodeWebTodosUpdatedReachesSyncV2ObservedClient(t *testing.T) {
	agent := &fakeAgent{name: "opencode-web"}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode-web", agent)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	serverConn.authedDevice = &TrustedDeviceRecord{DeviceID: "dev-ios"}
	handlers.eventPublisher.SetConnSyncV2(serverConn, true)

	params := json.RawMessage(`{"backendId":"opencode-web","sessionIds":["ses_1"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	handlers.handleSetObservationScope(serverConn, WireMessage{RequestID: "req1", BackendID: "opencode-web", Params: params})

	session := &fakeAgentSession{
		id:     "ses_1",
		events: make(chan core.Event, 2),
	}
	session.events <- core.Event{
		Type: core.EventPlan,
		Plan: []core.Todo{{Content: "任务一", Status: "in_progress", Priority: "medium"}},
	}
	session.events <- core.Event{Type: core.EventResult, Done: true, Content: "done"}
	close(session.events)

	done := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_1", "opencode-web")
		close(done)
	}()

	todoFrame := waitForEventFrame(t, clientConn, "todos_updated")
	eventData, _ := todoFrame["data"].(map[string]any)
	liveTodos, _ := eventData["todos"].([]any)
	if len(liveTodos) != 1 {
		t.Fatalf("live todo count = %d, want 1, data=%#v", len(liveTodos), eventData)
	}
	todo, _ := liveTodos[0].(map[string]any)
	if got := todo["content"]; got != "任务一" {
		t.Fatalf("content = %#v, want 任务一", got)
	}
	if got := todo["status"]; got != "in_progress" {
		t.Fatalf("status = %#v, want in_progress", got)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not finish")
	}
}

// waitForEventFrame reads client frames until an `event` frame with the given
// name arrives (skipping RPC result frames such as the set_observation_scope
// ack). Fails after a bounded deadline.
func waitForEventFrame(t *testing.T, conn interface{ ReadJSON(interface{}) error }, event string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var raw map[string]any
		if err := conn.ReadJSON(&raw); err != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if raw["type"] != "event" {
			continue
		}
		if raw["event"] == event {
			return raw
		}
	}
	t.Fatalf("timed out waiting for %q event frame", event)
	return nil
}
