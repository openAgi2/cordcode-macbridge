package gobridge

import (
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// audit014_fullpath_test.go owns the directive-014 full-path proofs: the E2b
// owner-history shape through the REAL get_session_projection hydrate. Red on
// the old implementation (projection.hydrate_failed); green after the adapter
// maps populated reasoning as first-class parts.

// e2bHistoryForOCW1 mirrors the agent-package fixture (sanitized placeholders,
// E2b structure: user/assistant ×2, assistant part order step-start,
// reasoning, text, step-finish).
const e2bHistoryForOCW1 = `[
	{"info":{"id":"msg_u1","sessionID":"ses_ocw1","role":"user","time":{"created":1}},"parts":[
		{"id":"prt_u1","sessionID":"ses_ocw1","messageID":"msg_u1","type":"text","text":"E2B_USER_1"}]},
	{"info":{"id":"msg_a1","sessionID":"ses_ocw1","role":"assistant","parentID":"msg_u1","time":{"created":2,"completed":3}},"parts":[
		{"id":"prt_s1","sessionID":"ses_ocw1","messageID":"msg_a1","type":"step-start","time":{"start":1,"end":1}},
		{"id":"prt_r1","sessionID":"ses_ocw1","messageID":"msg_a1","type":"reasoning","text":"E2B_REASONING_1","time":{"start":1,"end":2}},
		{"id":"prt_t1","sessionID":"ses_ocw1","messageID":"msg_a1","type":"text","text":"E2B_ANSWER_1","time":{"start":2,"end":3}},
		{"id":"prt_f1","sessionID":"ses_ocw1","messageID":"msg_a1","type":"step-finish","time":{"start":1,"end":3}}]},
	{"info":{"id":"msg_u2","sessionID":"ses_ocw1","role":"user","time":{"created":4}},"parts":[
		{"id":"prt_u2","sessionID":"ses_ocw1","messageID":"msg_u2","type":"text","text":"E2B_USER_2"}]},
	{"info":{"id":"msg_a2","sessionID":"ses_ocw1","role":"assistant","parentID":"msg_u2","time":{"created":5,"completed":6}},"parts":[
		{"id":"prt_s2","sessionID":"ses_ocw1","messageID":"msg_a2","type":"step-start","time":{"start":4,"end":4}},
		{"id":"prt_r2","sessionID":"ses_ocw1","messageID":"msg_a2","type":"reasoning","text":"E2B_REASONING_2","time":{"start":4,"end":5}},
		{"id":"prt_t2","sessionID":"ses_ocw1","messageID":"msg_a2","type":"text","text":"E2B_ANSWER_2","time":{"start":5,"end":6}},
		{"id":"prt_f2","sessionID":"ses_ocw1","messageID":"msg_a2","type":"step-finish","time":{"start":4,"end":6}}]}
]`

// projectionViaHandler drives the real get_session_projection RPC.
func projectionViaHandler(t *testing.T, h *Handlers, agent core.Agent) []capturedResult {
	t.Helper()
	conn := newResolveResultConn()
	raw, _ := json.Marshal(map[string]any{"sessionId": "ses_ocw1"})
	msg := WireMessage{Type: "request", RequestID: "req_proj_a14", BackendID: "opencode-web", Method: "get_session_projection", Params: raw}
	h.handleGetSessionProjection(conn, msg, agent)
	return conn.captured()
}

// reasoningSnapshotParts returns the reasoning parts per turn from the Kernel
// snapshot (turn id -> reasoning texts).
func reasoningSnapshotParts(t *testing.T, h *Handlers) map[string][]string {
	t.Helper()
	proj, ok := h.projectionKernel.reducer.Snapshot("opencode-web", "ses_ocw1")
	if !ok {
		return nil
	}
	out := map[string][]string{}
	for _, turn := range proj.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type == "reasoning" {
				out[turn.TurnID] = append(out[turn.TurnID], part.Text)
			}
		}
	}
	return out
}

// TestAudit014_ProjectionHydrateWithReasoning: the E2b shape must hydrate —
// RED on the old implementation with projection.hydrate_failed; GREEN with
// each assistant turn carrying its reasoning exactly once, step-start/-finish
// producing zero parts, and the answer text unpolluted.
func TestAudit014_ProjectionHydrateWithReasoning(t *testing.T) {
	h, serve, agent := newAuditHarnessWithOptions(t, func(s *ssePushServe) {
		s.setHistory("ses_ocw1", e2bHistoryForOCW1)
	})
	_ = serve

	results := projectionViaHandler(t, h, agent)
	if len(results) != 1 {
		t.Fatalf("projection result = %+v", results)
	}
	if results[0].Err != nil {
		t.Fatalf("E2b history must hydrate through get_session_projection, got %+v (old implementation reproduces projection.hydrate_failed here)", results[0].Err)
	}

	reasoning := reasoningSnapshotParts(t, h)
	if len(reasoning) != 2 {
		t.Fatalf("both assistant turns must carry reasoning, got %+v", reasoning)
	}
	if got := reasoning["msg_u1"]; len(got) != 1 || got[0] != "E2B_REASONING_1" {
		t.Fatalf("turn msg_u1 reasoning = %+v, want exactly [E2B_REASONING_1]", got)
	}
	if got := reasoning["msg_u2"]; len(got) != 1 || got[0] != "E2B_REASONING_2" {
		t.Fatalf("turn msg_u2 reasoning = %+v, want exactly [E2B_REASONING_2]", got)
	}

	// Answer text stays unpolluted; step-start/step-finish contribute nothing.
	proj, ok := h.projectionKernel.reducer.Snapshot("opencode-web", "ses_ocw1")
	if !ok {
		t.Fatal("snapshot missing after hydrate")
	}
	for _, turn := range proj.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			switch part.Type {
			case "text":
				if part.Text != "E2B_ANSWER_1" && part.Text != "E2B_ANSWER_2" {
					t.Fatalf("answer text polluted: %+v", part)
				}
			case "step-start", "step-finish":
				t.Fatalf("step markers must not become projection parts: %+v", part)
			}
		}
	}
}
