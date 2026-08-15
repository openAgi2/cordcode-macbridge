package gobridge

// DSH dead-session resume guard tests (2026-08-16 store bridge design §4.5):
// send_message on a deepseek id that exists in the user harness store but is
// not live in the registry fails fast with session_resume_not_supported —
// before any process spawn. New/pending/unknown ids pass through to
// StartSession untouched. The driver-level guard has its own test in
// agent/dsh/driver_guard_test.go.
import (
	"os"
	"path/filepath"
	"testing"
)

func writeDshStoreSessionMarker(t *testing.T, sessionID string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("DSH_HOME"), "sessions", "--demo--", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	header := `{"type":"session","version":0,"id":"` + sessionID + `","createdAt":1,"cwd":"/demo","delegationDepth":0}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDshDeadStoreSessionSendFailsFast(t *testing.T) {
	h := newDshProjectionHandlers(t)
	writeDshStoreSessionMarker(t, "session-dead-1")

	agent := &fakeAgent{name: "dsh"}
	h.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleSendMessage(serverConn, WireMessage{
		BackendID: "deepseek", Method: "send_message", RequestID: "r-guard",
		Params: []byte(`{"sessionId":"session-dead-1","content":"继续聊"}`),
	}, agent)

	frames := readJSONMaps(t, clientConn, 1)
	result := frames[0]
	if result["type"] != "result" {
		t.Fatalf("expected result frame, got %v", result)
	}
	errMap, _ := result["error"].(map[string]any)
	if errMap == nil || errMap["code"] != "session_resume_not_supported" {
		t.Fatalf("error = %#v, want session_resume_not_supported", errMap)
	}
	if retryable, _ := errMap["retryable"].(bool); retryable {
		t.Fatalf("session_resume_not_supported must be retryable=false: %#v", errMap)
	}
	if calls := len(agent.startCalls); calls != 0 {
		t.Fatalf("guard must fire BEFORE StartSession (no process spawn), got %d calls", calls)
	}
}

func TestDshSendNewPendingAndUnknownIDsPassGuard(t *testing.T) {
	h := newDshProjectionHandlers(t)
	// Only session-dead-2 is in the store; "fresh-unknown-id" is not.
	writeDshStoreSessionMarker(t, "session-dead-2")

	agent := &fakeAgent{name: "dsh"}
	h.RegisterAgent("deepseek", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	for _, id := range []string{"pending-brand-new", "fresh-unknown-id"} {
		framesRead := 0
		h.handleSendMessage(serverConn, WireMessage{
			BackendID: "deepseek", Method: "send_message", RequestID: "r-" + id,
			Params: []byte(`{"sessionId":"` + id + `","content":"hi"}`),
		}, agent)
		// Accepted sends broadcast running state before the result; drain
		// adaptively until the result frame lands.
		var result map[string]any
		for result == nil && framesRead < 3 {
			for _, frame := range readJSONMaps(t, clientConn, 1) {
				framesRead++
				if frame["type"] == "result" {
					result = frame
				}
			}
		}
		if result == nil {
			t.Fatalf("%s: no result frame", id)
		}
		if errMap, _ := result["error"].(map[string]any); errMap != nil {
			t.Fatalf("%s: unexpected error %#v", id, errMap)
		}
	}
	if got := agent.startCalls; len(got) != 2 {
		t.Fatalf("both ids must reach StartSession, calls=%v", got)
	}
	// pending- prefix is cleared before StartSession (fresh session form).
	if agent.startCalls[0] != "" {
		t.Fatalf("pending- id must reach StartSession as empty, got %q", agent.startCalls[0])
	}
	if agent.startCalls[1] != "fresh-unknown-id" {
		t.Fatalf("unknown id must pass through verbatim, got %q", agent.startCalls[1])
	}
}
