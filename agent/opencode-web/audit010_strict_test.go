package opencodeweb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// audit010_strict_test.go owns the directive-010 strict destructive tail:
// exact HTTP 200 for mutations, archive timestamp confirmation, /agent and
// /provider explicit-presence decoding, and the exact-key Todo row — every
// failure must leave prior state, catalog signals, and the timeline
// unpolluted.

// ── /agent strict required fields ────────────────────────────────────────────

func TestAudit010_AgentRegistryExplicitFields(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantFail string
	}{
		{"missing description on non-hidden row", `[{"name":"build","mode":"primary","native":true}]`, "missing required description"},
		{"null description", `[{"name":"build","mode":"primary","native":true,"description":null}]`, "missing required description"},
		{"description wrong type", `[{"name":"build","mode":"primary","native":true,"description":7}]`, "description must be a string"},
		{"missing mode (primary default deleted)", `[{"name":"build","description":"d","native":true}]`, "missing required mode"},
		{"missing native", `[{"name":"build","description":"d","mode":"primary"}]`, "missing required native"},
		{"null native", `[{"name":"build","description":"d","mode":"primary","native":null}]`, "missing required native"},
		{"native wrong type", `[{"name":"build","description":"d","mode":"primary","native":"yes"}]`, "must be a boolean"},
		{"mode wrong type", `[{"name":"build","description":"d","mode":7,"native":true}]`, "must be a string"},
		{"empty mode", `[{"name":"build","description":"d","mode":"","native":true}]`, "must be non-empty"},
		{"second row missing fields fails whole list", `[{"name":"build","description":"d","mode":"primary","native":true},{"name":"plan"}]`, "row 1 missing required mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeAgentRegistry([]byte(tc.payload))
			if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
				t.Fatalf("must fail closed with %q, got %v", tc.wantFail, err)
			}
		})
	}
	// The full verified row passes.
	rows, err := decodeAgentRegistry([]byte(`[{"name":"build","description":"general coding","mode":"primary","native":true,"hidden":false}]`))
	if err != nil || len(rows) != 1 {
		t.Fatalf("verified row must pass, got %+v err=%v", rows, err)
	}
	// Same-version evidence: the hidden internal agents (compaction/summary/
	// title on the real 1.18.18 serve) omit description and must pass.
	hiddenRows, err := decodeAgentRegistry([]byte(`[{"name":"compaction","mode":"primary","native":true,"hidden":true}]`))
	if err != nil || len(hiddenRows) != 1 {
		t.Fatalf("hidden internal row without description is the evidenced shape, got %+v err=%v", hiddenRows, err)
	}
}

// ── /provider top-level explicit presence ───────────────────────────────────

func TestAudit010_ProviderTopLevelExplicit(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantFail string
	}{
		{"missing default", `{"all":[],"connected":[]}`, `"default" must be explicitly present`},
		{"null default", `{"all":[],"default":null,"connected":[]}`, `"default" must be explicitly present`},
		{"missing connected", `{"all":[],"default":{}}`, `"connected" must be explicitly present`},
		{"null connected", `{"all":[],"default":{},"connected":null}`, `"connected" must be explicitly present`},
		{"null all", `{"all":null,"default":{},"connected":[]}`, `"all" must be explicitly present`},
		{"connected wrong type", `{"all":[],"default":{},"connected":[7]}`, `"connected" must be a string array`},
		{"default wrong type", `{"all":[],"default":[],"connected":[]}`, `"default" must be the provider→model object`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseProviderCatalog([]byte(tc.payload))
			if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
				t.Fatalf("must fail closed with %q, got %v", tc.wantFail, err)
			}
		})
	}
	// Legal empties parse to an honest empty catalog.
	catalog, err := parseProviderCatalog([]byte(`{"all":[],"default":{},"connected":[]}`))
	if err != nil || len(catalog.Models) != 0 {
		t.Fatalf("legal empty envelope must parse to zero models, got %+v err=%v", catalog, err)
	}
}

// ── Todo exact key set ───────────────────────────────────────────────────────

// TestAudit010_TodoExtraAliasKeyFailsWholeList: a row carrying the required
// three fields PLUS an alias/unknown key (the {content,…,text} case) fails
// the whole replacement — endpoint and SSE paths both, and the prior
// snapshot stays untouched.
func TestAudit010_TodoExtraAliasKeyFailsWholeList(t *testing.T) {
	const withAlias = `[{"content":"c","status":"pending","priority":"high","text":"alias"}]`

	agent, _ := questionAgent(t, map[string]string{
		"/session/ses_t/todo": withAlias,
	})
	if _, err := agent.FetchTodos(context.Background(), "ses_t"); err == nil || !strings.Contains(err.Error(), "beyond the verified") {
		t.Fatalf("alias key next to required fields must fail, got %v", err)
	}
	agent.todoMu.Lock()
	_, hasSnapshot := agent.lastTodos["ses_t"]
	agent.todoMu.Unlock()
	if hasSnapshot {
		t.Fatal("failed endpoint decode must not pollute the last-known snapshot")
	}

	subAgent, _ := questionAgent(t, map[string]string{"/provider": `{}`})
	sub := newDrivenSubscriber(t, subAgent)
	sub.handleRawEvent(`{"payload":{"type":"todo.updated","properties":{"sessionID":"ses_t","todos":` + withAlias + `}}}`)
	for _, ev := range drain(sub) {
		if ev.Type == core.EventPlan {
			t.Fatalf("malformed replacement must not emit a plan event, got %+v", ev)
		}
	}
	subAgent.todoMu.Lock()
	_, hasSnapshot = subAgent.lastTodos["ses_t"]
	subAgent.todoMu.Unlock()
	if hasSnapshot {
		t.Fatal("failed SSE decode must not pollute the last-known snapshot")
	}
}

// ── Mutation exact HTTP 200 + archive timestamp ──────────────────────────────

// TestAudit010_MutationsRejectNon200: rename/archive/delete accept HTTP 200
// EXACTLY; 201/202/204 fail closed.
func TestAudit010_MutationsRejectNon200(t *testing.T) {
	for _, code := range []int{201, 202, 204} {
		t.Run(map[int]string{201: "201", 202: "202", 204: "204"}[code], func(t *testing.T) {
			agent, serve := questionAgent(t, map[string]string{
				"/session/ses_r": `{"id":"ses_r","title":"t","time":{"created":1,"updated":2,"archived":3}}`,
			})
			serve.statusBodies = map[string]recordingStatusBody{
				"PATCH /session/ses_r":  {code, `{"id":"ses_r","title":"t","time":{"archived":3}}`},
				"DELETE /session/ses_r": {code, `true`},
			}
			if _, err := agent.RenameSession(context.Background(), "ses_r", "t"); err == nil || !strings.Contains(err.Error(), "HTTP 200 only") {
				t.Fatalf("rename %d must fail closed, got %v", code, err)
			}
			if _, err := agent.ArchiveSession(context.Background(), "ses_r", mustTime()); err == nil || !strings.Contains(err.Error(), "HTTP 200 only") {
				t.Fatalf("archive %d must fail closed, got %v", code, err)
			}
			// delete: non-200 with a success-shaped body fails AND leaves the
			// catalog signal unfired.
			if err := agent.DeleteSession(context.Background(), "ses_r"); err == nil || !strings.Contains(err.Error(), "HTTP 200 only") {
				t.Fatalf("delete %d with body true must fail closed, got %v", code, err)
			}
			select {
			case <-agent.CatalogRefreshSignals():
				t.Fatal("non-200 delete must not signal catalog refresh")
			default:
			}
		})
	}
}

// TestAudit010_ArchiveTimestampConfirmation: the echoed time.archived must
// equal the requested epoch-ms — a different (or zero) echo fails.
func TestAudit010_ArchiveTimestampConfirmation(t *testing.T) {
	agent, _ := questionAgent(t, map[string]string{
		"/session/ses_r": `{"id":"ses_r","title":"x","time":{"created":1,"updated":2,"archived":111222333}}`,
	})
	at := time.UnixMilli(1700000000123).UTC()
	if _, err := agent.ArchiveSession(context.Background(), "ses_r", at); err == nil || !strings.Contains(err.Error(), "does not confirm the requested") {
		t.Fatalf("wrong archived timestamp must fail, got %v", err)
	}
	select {
	case <-agent.CatalogRefreshSignals():
		t.Fatal("unconfirmed archive must not signal catalog refresh")
	default:
	}

	// Matching echo succeeds (the serve persisted our ms verbatim).
	agent2, _ := questionAgent(t, map[string]string{
		"/session/ses_r": `{"id":"ses_r","title":"x","time":{"created":1,"updated":2,"archived":1700000000123}}`,
	})
	info, err := agent2.ArchiveSession(context.Background(), "ses_r", at)
	if err != nil {
		t.Fatalf("confirmed archive must succeed: %v", err)
	}
	if info.ArchivedAt.UnixMilli() != 1700000000123 {
		t.Fatalf("archivedAt = %d", info.ArchivedAt.UnixMilli())
	}
}
