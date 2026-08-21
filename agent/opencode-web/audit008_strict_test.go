package opencodeweb

import (
	"context"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// audit008_strict_test.go is the directive-009 Wave-0/2 destructive set:
// every case below must FAIL against the audit-008 lenient implementation
// (alias/default/silent-skip/any-2xx) and PASS only under strict fail-closed
// decoding (canonical §6.9/§6.10/§6.6).

// ── §6.9 Todo strictness ─────────────────────────────────────────────────────

func TestAudit008_TodoEndpointStrictDestructive(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantFail string
	}{
		{"text alias for content", `[{"text":"a","status":"pending","priority":"high"}]`, "missing required content"},
		{"missing status", `[{"content":"a","priority":"high"}]`, "missing required status"},
		{"missing priority", `[{"content":"a","status":"pending"}]`, "missing required priority"},
		{"empty content", `[{"content":"  ","status":"pending","priority":"high"}]`, "missing required content"},
		{"wrong-type status", `[{"content":"a","status":1,"priority":"high"}]`, "missing required status"},
		{"wrong-type row", `[{"content":42}]`, "missing required content"},
		{"non-object row", `["not-an-object"]`, "malformed"},
		{"envelope shape", `{"data":[]}`, "bare array"},
		{"null row", `[null]`, "malformed"},
		{"good row then bad row", `[{"content":"a","status":"pending","priority":"high"},{"content":""}]`, "missing required content"},
	}
	for _, tc := range cases {
		agent, _ := newDataAgent(t, map[string]string{
			"/session/ses_t/todo": tc.payload,
		}, "/tmp")
		_, err := agent.FetchTodos(context.Background(), "ses_t")
		if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
			t.Fatalf("%s must fail closed with %q, got %v", tc.name, tc.wantFail, err)
		}
	}
}

func TestAudit008_TodoEventStrictDestructiveAndNoPartialEmit(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	// Seed a known-good replacement, then attack with malformed ones.
	driveFrames(sub, sseFrame("todo.updated", map[string]any{
		"sessionID": "ses_t",
		"todos":     []any{map[string]any{"content": "good", "status": "pending", "priority": "high"}},
	}))
	if events := drain(sub); len(events) != 1 {
		t.Fatalf("seed must emit one plan event, got %+v", events)
	}

	attacks := []map[string]any{
		{"sessionID": "ses_t", "todos": []any{map[string]any{"text": "alias", "status": "pending", "priority": "high"}}},
		{"sessionID": "ses_t", "todos": []any{map[string]any{"content": "no-status"}}},
		{"sessionID": "ses_t", "todos": []any{map[string]any{"content": "ok", "status": "pending", "priority": "high"}, map[string]any{"content": ""}}},
		{"sessionID": "ses_t", "todos": []any{"not-an-object"}},
	}
	for i, attack := range attacks {
		driveFrames(sub, sseFrame("todo.updated", attack))
		if events := drain(sub); len(events) != 0 {
			t.Fatalf("attack %d: malformed replacement must emit NOTHING, got %+v", i, events)
		}
	}
	// Failure must not pollute the last-known-good snapshot.
	agent.todoMu.Lock()
	cached := append([]core.Todo(nil), agent.lastTodos["ses_t"]...)
	agent.todoMu.Unlock()
	if len(cached) != 1 || cached[0].Content != "good" {
		t.Fatalf("failed replacements must not touch lastTodos, got %+v", cached)
	}
}

// ── §6.6 provider/config strictness ─────────────────────────────────────────

func TestAudit008_ProviderCatalogStrictDestructive(t *testing.T) {
	base := func(models string) string {
		return `{"all":[{"id":"localmock","models":{` + models + `}}],"default":{"localmock":"echo"},"connected":["localmock"]}`
	}
	cases := []struct {
		name     string
		payload  string
		wantFail string
	}{
		{"connected model row without id (map-key fallback must die)", base(`"echo":{"name":"Echo"}`), "missing required id"},
		{"connected model row wrong id type", base(`"echo":{"id":42}`), "malformed"},
		{"connected provider row without id", `{"all":[{"models":{"x":{"id":"x"}}}],"default":{},"connected":["localmock"]}`, "missing required provider id"},
		{"envelope without all", `{"default":{},"connected":["localmock"]}`, "shape not recognized"},
		{"top-level array", `[]`, "shape not recognized"},
	}
	for _, tc := range cases {
		_, err := parseProviderCatalog([]byte(tc.payload))
		if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
			t.Fatalf("%s must fail closed with %q, got %v", tc.name, tc.wantFail, err)
		}
	}
	// Unconnected providers with imperfect rows are FILTERED (not guessed into
	// the catalog) — the physical row is never repaired.
	loose, err := parseProviderCatalog([]byte(`{"all":[{"id":"localmock","models":{"echo":{"id":"echo"}}},{"id":"unconnected","models":{"weird":{}}}],"default":{},"connected":["localmock"]}`))
	if err != nil {
		t.Fatalf("unconnected sloppy rows must be filtered, not fatal: %v", err)
	}
	if len(loose.Models) != 1 {
		t.Fatalf("catalog = %+v", loose.Models)
	}
}

func TestAudit008_ConfigModelNullVsAbsent(t *testing.T) {
	agent, serve := newDataAgent(t, map[string]string{
		"/provider": `{"all":[{"id":"localmock","models":{"echo":{"id":"echo"},"zeta":{"id":"zeta"}}}],"default":{},"connected":["localmock"]}`,
	}, "/tmp")

	// Evidence-proven ABSENT key = no configured model (legal).
	serve.responses["/config"] = `{"small_model":"localmock/echo"}`
	if got, err := agent.fetchConfiguredModel(context.Background(), mustClient(t, agent)); err != nil || got != "" {
		t.Fatalf("absent model key must read as no configured model, got %q err=%v", got, err)
	}

	// null / wrong type / empty string = unproven shape → fail closed.
	for _, payload := range []string{
		`{"model":null}`,
		`{"model":42}`,
		`{"model":""}`,
		`{"model":["localmock/echo"]}`,
	} {
		serve.responses["/config"] = payload
		if _, err := agent.fetchConfiguredModel(context.Background(), mustClient(t, agent)); err == nil {
			t.Fatalf("config %s must fail closed", payload)
		}
	}
}

func TestAudit008_AgentRegistryStrictDestructive(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantFail string
	}{
		{"row missing name", `[{"mode":"primary"}]`, "missing required name"},
		{"row wrong name type", `[{"name":42}]`, "must be a string"},
		{"non-object row", `[7]`, "must be an object"},
		{"envelope", `{"data":[]}`, "bare array"},
		{"scalar", `42`, "bare array"},
	}
	for _, tc := range cases {
		_, err := decodeAgentRegistry([]byte(tc.payload))
		if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
			t.Fatalf("%s must fail closed with %q, got %v", tc.name, tc.wantFail, err)
		}
	}
}

// ── §6.10 mutation strictness ───────────────────────────────────────────────

func TestAudit008_RenameArchiveStrictResponse(t *testing.T) {
	// `{}` echo with no id — the old code backfilled the requested id.
	agent, _ := newDataAgent(t, map[string]string{
		"/session/ses_r": `{}`,
	}, "/tmp")
	if _, err := agent.RenameSession(context.Background(), "ses_r", "t"); err == nil || !strings.Contains(err.Error(), "missing or mismatched id") {
		t.Fatalf("identity-less rename echo must fail, got %v", err)
	}
	if _, err := agent.ArchiveSession(context.Background(), "ses_r", mustTime()); err == nil || !strings.Contains(err.Error(), "missing or mismatched id") {
		t.Fatalf("identity-less archive echo must fail, got %v", err)
	}

	// Wrong-id echo (server answered a different session) must fail.
	agent2, _ := newDataAgent(t, map[string]string{
		"/session/ses_r": `{"id":"ses_OTHER","title":"t","time":{"created":1,"updated":2,"archived":3}}`,
	}, "/tmp")
	if _, err := agent2.RenameSession(context.Background(), "ses_r", "t"); err == nil || !strings.Contains(err.Error(), "missing or mismatched id") {
		t.Fatalf("mismatched rename echo must fail, got %v", err)
	}

	// Rename echo whose title does not match the requested title must fail.
	agent3, _ := newDataAgent(t, map[string]string{
		"/session/ses_r": `{"id":"ses_r","title":"NOT-what-we-asked","time":{"created":1,"updated":2}}`,
	}, "/tmp")
	if _, err := agent3.RenameSession(context.Background(), "ses_r", "t"); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("rename echo must carry the server-confirmed title, got %v", err)
	}

	// Archive echo without time.archived must fail.
	agent4, _ := newDataAgent(t, map[string]string{
		"/session/ses_r": `{"id":"ses_r","title":"x","time":{"created":1,"updated":2}}`,
	}, "/tmp")
	if _, err := agent4.ArchiveSession(context.Background(), "ses_r", mustTime()); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("archive echo must carry time.archived, got %v", err)
	}
}

func TestAudit008_DeleteStrictBooleanAndConvergence(t *testing.T) {
	// Non-`true` bodies on 2xx must all fail.
	for _, body := range []string{`false`, `null`, `{}`, ``, `"true"`, `1`} {
		agent, _ := newDataAgent(t, map[string]string{
			"/session/ses_d": body,
		}, "/tmp")
		if err := agent.DeleteSession(context.Background(), "ses_d"); err == nil || !strings.Contains(err.Error(), "boolean true") {
			t.Fatalf("delete body %q must fail closed, got %v", body, err)
		}
	}

	// Full convergence: pre-fetch 200 (directory) → DELETE true → by-ID 404
	// → scoped list absence.
	agent, serve := newDataAgent(t, map[string]string{
		"/session/ses_d": `{"id":"ses_d","directory":"/tmp/proj","time":{"created":1,"updated":2}}`,
	}, "/tmp")
	serve.methodResponses = map[string]string{"DELETE /session/ses_d": `true`}
	serve.statusAfter = map[string]recordingStatusAfter{
		"GET /session/ses_d": {code: 404, after: 1}, // pre-fetch 200, then 404
	}
	serve.dirResponses = map[string]string{"/session|/tmp/proj": `[]`}
	if err := agent.DeleteSession(context.Background(), "ses_d"); err != nil {
		t.Fatalf("convergent delete must succeed: %v", err)
	}

	// Convergence failure surfaces: by-ID still answers → error, and the
	// catalog signal must NOT have fired.
	agent2, serve2 := newDataAgent(t, map[string]string{
		"/session/ses_e": `{"id":"ses_e"}`, // by-ID stays alive AFTER the delete
	}, "/tmp")
	serve2.methodResponses = map[string]string{"DELETE /session/ses_e": `true`}
	if err := agent2.DeleteSession(context.Background(), "ses_e"); err == nil || !strings.Contains(err.Error(), "still readable") {
		t.Fatalf("non-convergent delete must fail visibly, got %v", err)
	}
	select {
	case <-agent2.CatalogRefreshSignals():
		t.Fatal("non-convergent delete must not signal catalog refresh")
	default:
	}
}
