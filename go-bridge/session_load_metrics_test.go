package gobridge

import (
	"encoding/json"
	"testing"
)

type sessionLoadCaptureConn struct {
	sent any
}

func (c *sessionLoadCaptureConn) SendJSON(value any) {
	c.sent = value
}

func (c *sessionLoadCaptureConn) SendResult(string, interface{}, *WireError)    {}
func (c *sessionLoadCaptureConn) SendEvent(string, string, string, interface{}) {}
func (c *sessionLoadCaptureConn) AuthedDevice() *TrustedDeviceRecord            { return nil }
func (c *sessionLoadCaptureConn) RemoteAddr() string                            { return "test" }
func (c *sessionLoadCaptureConn) Close() error                                  { return nil }

func TestSessionLoadTransportRoute(t *testing.T) {
	if got := sessionLoadTransportRoute(&sessionLoadCaptureConn{}); got != "direct" {
		t.Fatalf("capture route = %q, want direct", got)
	}
	if got := sessionLoadTransportRoute(&RelayDeviceConn{}); got != "relay" {
		t.Fatalf("relay route = %q, want relay", got)
	}
}

func TestSessionLoadMetricsCountsEncodedResponseBytes(t *testing.T) {
	conn := &sessionLoadCaptureConn{}
	metrics := newSessionLoadRequestMetrics(conn, WireMessage{
		RequestID: "req-1",
		Method:    "list_sessions",
		BackendID: "codex",
	})
	data := map[string]any{
		"sessions": []any{
			map[string]any{"id": "one", "title": "First"},
		},
	}

	metrics.sendResult(conn, "req-1", data, nil)

	encoded, err := json.Marshal(conn.sent)
	if err != nil {
		t.Fatalf("marshal captured response: %v", err)
	}
	if metrics.responseLen != len(encoded) {
		t.Fatalf("response bytes = %d, want %d", metrics.responseLen, len(encoded))
	}
	if metrics.route != "direct" || metrics.method != "list_sessions" || metrics.backendID != "codex" {
		t.Fatalf("unexpected request metadata: %+v", metrics)
	}
	if metrics.requestID != "req-1" {
		t.Fatalf("request id = %q, want req-1", metrics.requestID)
	}
}
