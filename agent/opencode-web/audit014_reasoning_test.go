package opencodeweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// audit014_reasoning_test.go owns the directive-014 owner-acceptance repair:
// populated reasoning parts in the authoritative HTTP history must hydrate as
// first-class reasoning content (canonical §6.3 as corrected — E2b evidence),
// not fail the whole page.

// e2bHistoryFixture reproduces the E2b owner-history structure exactly (4
// rows; assistant part order step-start, reasoning, text, step-finish ×2).
// Texts are sanitized placeholders — only shape/order/non-empty facts are
// evidence.
const e2bHistoryFixture = `[
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

// e2bHistoryWithoutReasoning is the correspondence control: identical rows
// with the reasoning parts removed — the old implementation must succeed on
// it, proving the hydrate failure maps 1:1 to reasoning presence.
const e2bHistoryWithoutReasoning = `[
	{"info":{"id":"msg_u1","sessionID":"ses_ocw1","role":"user","time":{"created":1}},"parts":[
		{"id":"prt_u1","sessionID":"ses_ocw1","messageID":"msg_u1","type":"text","text":"E2B_USER_1"}]},
	{"info":{"id":"msg_a1","sessionID":"ses_ocw1","role":"assistant","parentID":"msg_u1","time":{"created":2,"completed":3}},"parts":[
		{"id":"prt_s1","sessionID":"ses_ocw1","messageID":"msg_a1","type":"step-start","time":{"start":1,"end":1}},
		{"id":"prt_t1","sessionID":"ses_ocw1","messageID":"msg_a1","type":"text","text":"E2B_ANSWER_1","time":{"start":2,"end":3}},
		{"id":"prt_f1","sessionID":"ses_ocw1","messageID":"msg_a1","type":"step-finish","time":{"start":1,"end":3}}]},
	{"info":{"id":"msg_u2","sessionID":"ses_ocw1","role":"user","time":{"created":4}},"parts":[
		{"id":"prt_u2","sessionID":"ses_ocw1","messageID":"msg_u2","type":"text","text":"E2B_USER_2"}]},
	{"info":{"id":"msg_a2","sessionID":"ses_ocw1","role":"assistant","parentID":"msg_u2","time":{"created":5,"completed":6}},"parts":[
		{"id":"prt_s2","sessionID":"ses_ocw1","messageID":"msg_a2","type":"step-start","time":{"start":4,"end":4}},
		{"id":"prt_t2","sessionID":"ses_ocw1","messageID":"msg_a2","type":"text","text":"E2B_ANSWER_2","time":{"start":5,"end":6}},
		{"id":"prt_f2","sessionID":"ses_ocw1","messageID":"msg_a2","type":"step-finish","time":{"start":4,"end":6}}]}
]`

// historyServeURL boots a minimal authed serve answering the authoritative
// history route with the given body.
func historyServeURL(t *testing.T, body string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case "/session":
			_, _ = w.Write([]byte(`[]`))
		case "/session/ses_ocw1/message":
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func historyAgentFor(t *testing.T, body string) *Agent {
	t.Helper()
	agentAny, err := New(map[string]any{
		"work_dir":          "/tmp/audit014",
		"opencode_web_url":  historyServeURL(t, body),
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return agentAny.(*Agent)
}

// TestAudit014_E2bHistoryReasoningMapping is the directive-014 red→green:
// the E2b-structured history with populated reasoning must hydrate with the
// reasoning kept as first-class parts in server order. On the old
// implementation this fails with errUnsupportedReasoning (the exact branch
// behind the owner's projection.hydrate_failed).
func TestAudit014_E2bHistoryReasoningMapping(t *testing.T) {
	t.Run("populated reasoning hydrates as first-class parts", func(t *testing.T) {
		agent := historyAgentFor(t, e2bHistoryFixture)
		entries, err := agent.GetRichSessionHistory(context.Background(), "ses_ocw1", 0)
		if err != nil {
			t.Fatalf("E2b history must hydrate (canonical §6.3 corrected), got %v", err)
		}
		if len(entries) != 4 {
			t.Fatalf("entries = %d, want 4", len(entries))
		}
		// Assistant rows carry reasoning then text, in server order, exact text.
		for i, want := range []struct{ entry, reasoning, answer string }{
			{"msg_a1", "E2B_REASONING_1", "E2B_ANSWER_1"},
			{"msg_a2", "E2B_REASONING_2", "E2B_ANSWER_2"},
		} {
			var entry *struct {
				id  string
				idx int
			}
			for j, e := range entries {
				if e.ID == want.entry {
					entry = &struct {
						id  string
						idx int
					}{e.ID, j}
					break
				}
			}
			if entry == nil {
				t.Fatalf("assistant row %s missing: %+v", want.entry, entries)
			}
			parts := entries[entry.idx].Parts
			var reasoning, text []map[string]any
			for _, p := range parts {
				switch p["type"] {
				case "reasoning":
					reasoning = append(reasoning, p)
				case "text":
					text = append(text, p)
				}
			}
			if len(reasoning) != 1 || reasoning[0]["content"] != want.reasoning {
				t.Fatalf("row %s reasoning parts = %+v, want exactly one %q", want.entry, reasoning, want.reasoning)
			}
			if len(text) != 1 || text[0]["content"] != want.answer {
				t.Fatalf("row %s text parts = %+v, want exactly one %q", want.entry, text, want.answer)
			}
			// Server order preserved: reasoning precedes the answer text.
			rIdx, tIdx := -1, -1
			for j, p := range parts {
				if p["type"] == "reasoning" {
					rIdx = j
				}
				if p["type"] == "text" {
					tIdx = j
				}
			}
			if rIdx > tIdx {
				t.Fatalf("row %s part order violated: reasoning at %d after text at %d", want.entry, rIdx, tIdx)
			}
			_ = i
		}
		// Reasoning never pollutes the folded Content of any row.
		for _, e := range entries {
			if strings.Contains(e.Content, "E2B_REASONING") {
				t.Fatalf("reasoning must not fold into Content, got %q", e.Content)
			}
		}
	})

	t.Run("correspondence: same fixture without reasoning succeeds", func(t *testing.T) {
		agent := historyAgentFor(t, e2bHistoryWithoutReasoning)
		entries, err := agent.GetRichSessionHistory(context.Background(), "ses_ocw1", 0)
		if err != nil {
			t.Fatalf("reasoning-free history must hydrate: %v", err)
		}
		if len(entries) != 4 {
			t.Fatalf("entries = %d, want 4", len(entries))
		}
	})
}
