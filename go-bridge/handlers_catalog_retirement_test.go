package gobridge

import (
	"sync"
	"testing"
)

type catalogRetirementCaptureConn struct {
	*publisherCaptureConn
	resultMu sync.Mutex
	request  string
	data     interface{}
	wireErr  *WireError
}

func newCatalogRetirementCaptureConn() *catalogRetirementCaptureConn {
	return &catalogRetirementCaptureConn{publisherCaptureConn: newPublisherCaptureConn(nil)}
}

func (c *catalogRetirementCaptureConn) SendResult(requestID string, data interface{}, wireErr *WireError) {
	c.resultMu.Lock()
	defer c.resultMu.Unlock()
	c.request = requestID
	c.data = data
	c.wireErr = wireErr
}

// B-T1: retiring the undeclared list RPC must not gate the independent backend-scoped push.
// Each supported backend receives sessions_changed, then gets the same explicit non-retryable
// capability error with no success-shaped data when it asks for list_sessions.
func TestCatalogRetirement_UndeclaredPushThenListCapabilityError(t *testing.T) {
	for _, backendID := range []string{"codex", "grokbuild", "opencode", "claudecode"} {
		t.Run(backendID, func(t *testing.T) {
			handlers := newTestHandlers(t)
			handlers.RegisterAgent(backendID, &fakeAgent{name: backendID})
			conn := newCatalogRetirementCaptureConn()
			handlers.broadcaster.RegisterConn(conn)
			handlers.eventPublisher.RegisterConnection(conn)

			if _, err := handlers.eventPublisher.PublishControlPlane(LogicalEvent{
				BackendID: backendID,
				Event:     "sessions_changed",
				Broadcast: true,
			}); err != nil {
				t.Fatalf("PublishControlPlane: %v", err)
			}
			conn.waitCount(t, 1)
			frames := conn.snapshot()
			if len(frames) != 1 {
				t.Fatalf("push frames=%d, want exactly one", len(frames))
			}
			push, ok := frames[0].(EventMessage)
			if !ok || push.Event != "sessions_changed" || push.BackendID != backendID || push.SessionID != "" {
				t.Fatalf("push=%#v", frames[0])
			}

			handlers.HandleRPC(conn, WireMessage{
				BackendID: backendID,
				Method:    "list_sessions",
				RequestID: "retired-list",
			})
			conn.resultMu.Lock()
			defer conn.resultMu.Unlock()
			if conn.request != "retired-list" {
				t.Fatalf("requestID=%q", conn.request)
			}
			if conn.data != nil {
				t.Fatalf("data=%#v, want nil (no sessions success body)", conn.data)
			}
			if conn.wireErr == nil || conn.wireErr.Code != "protocol.capability_required" {
				t.Fatalf("error=%#v", conn.wireErr)
			}
			if conn.wireErr.Retryable == nil || *conn.wireErr.Retryable {
				t.Fatalf("retryable=%#v, want explicit false", conn.wireErr.Retryable)
			}
			if conn.wireErr.Message != "list_sessions requires catalog_cursor_epoch_v2" {
				t.Fatalf("message=%q", conn.wireErr.Message)
			}
		})
	}
}
