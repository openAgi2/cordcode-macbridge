package dshweb

// GetSessionModelSelection (session.models → current selection): the layer-1
// session truth that go-bridge merges into get_session. Covers: real current
// → values + exact wire payload; empty current / RPC failure → ok=false
// (no fabricated fallback, per the no-invention rule).

import (
	"context"
	"encoding/json"
	"testing"
)

func TestGetSessionModelSelectionReportsCurrent(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/projections"] = fakeRPCResponse{value: map[string]any{
		"asOfSeq": 42,
		"values": map[string]any{
			"modelSelection": map[string]any{
				"lastUsed": map[string]any{"provider": "deepseek", "model": "deepseek-v4-flash", "reasoningEffort": "high"},
				"next":     nil,
			},
		},
	}}

	sel, ok := a.GetSessionModelSelection(context.Background(), "ses_abc")
	if !ok {
		t.Fatal("expected ok=true for a real current selection")
	}
	if sel.Provider != "deepseek" || sel.Model != "deepseek-v4-flash" || sel.ReasoningEffort != "high" {
		t.Fatalf("selection = %+v", sel)
	}

	calls := methodCalls(f, "session/projections")
	if len(calls) != 1 {
		t.Fatalf("session/projections calls = %d, want 1", len(calls))
	}
	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(unwrapArgs(t, calls[0]), &payload); err != nil || payload.SessionID != "ses_abc" {
		t.Fatalf("payload = %s (err %v)", calls[0], err)
	}
}

func TestGetSessionModelSelectionNoCurrentIsFalse(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/projections"] = fakeRPCResponse{value: map[string]any{
		"current":  map[string]any{},
		"routable": true,
		"groups":   []any{},
		"failures": []any{},
	}}

	if _, ok := a.GetSessionModelSelection(context.Background(), "ses_none"); ok {
		t.Fatal("expected ok=false when the session has no current selection")
	}
}

func TestGetSessionModelSelectionRPCErrorIsFalse(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/projections"] = fakeRPCResponse{
		err: &RPCError{Code: "internal", Message: "boom"},
	}

	if _, ok := a.GetSessionModelSelection(context.Background(), "ses_err"); ok {
		t.Fatal("expected ok=false on RPC failure")
	}
}
