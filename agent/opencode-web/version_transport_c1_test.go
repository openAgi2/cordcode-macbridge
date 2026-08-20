package opencodeweb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// version_transport_c1_test.go pins the C1 version/transport boundary
// (plan §6 C1 + S3 impact record C1):
//
//   - OpenCode 1.18.18 (generation118) is the ONLY verified product adapter;
//   - v2 and unknown generations fail closed / quarantined at clientFor:
//     no normal selection, no prompt, no SSE ingest, no Kernel feed, no
//     capability;
//   - the legacy unknown-shape recursive catalog walk is gone (fail closed);
//   - Basic Auth / directory scoping / HTTP timeout / SSE no-lifetime-timeout /
//     bounded reconnect stay transport-only control plane: they never write
//     timeline content or manufacture a turn.
//
// Every test has an explicit timeout and tears down its server/contexts; none
// leaves an unterminated background task behind.

// newC1Serve boots a recordingServe with the given canned responses.
func newC1Serve(t *testing.T, responses map[string]string) (*Agent, *recordingServe) {
	t.Helper()
	s := &recordingServe{responses: responses}
	base := s.start(t)
	a, err := New(map[string]any{
		"work_dir":          "/tmp/proj",
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return a.(*Agent), s
}

func TestVerified118Only(t *testing.T) {
	// The verified 1.18.18 shape (health 200 authed + bare-array /session) is
	// the ONLY generation clientFor hands out; an unknown shape fails with the
	// distinct probe error, not the quarantine error.
	agent, _ := newC1Serve(t, map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `[]`,
	})
	c, err := agent.clientFor(context.Background())
	if err != nil {
		t.Fatalf("1.18 endpoint must be usable: %v", err)
	}
	if c.Generation() != generation118 {
		t.Fatalf("generation = %q, want 1.18", c.Generation())
	}
	ok, detail := agent.InstanceStatus()
	if !ok || !strings.Contains(detail, "generation=1.18") {
		t.Fatalf("InstanceStatus must report supported 1.18.18, got ok=%v detail=%q", ok, detail)
	}

	// Unknown shape: health answers but neither session shape matches — the
	// probe error must stay distinct from the v2 quarantine verdict.
	unknown, _ := newC1Serve(t, map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `{"weird":true}`,
		"/api/session":   `{"also-weird":true}`,
	})
	if c, err := unknown.clientFor(context.Background()); err == nil || !strings.Contains(err.Error(), "not usable") {
		t.Fatalf("unknown shape must fail closed as probe-unusable, got client=%v err=%v", c, err)
	}
	if _, err := unknown.clientFor(context.Background()); err != nil && strings.Contains(err.Error(), "unsupported-generation (quarantined)") {
		t.Fatalf("unknown shape is NOT the v2 quarantine verdict, got %v", err)
	}
	okU, detailU := unknown.InstanceStatus()
	if okU || !strings.Contains(detailU, "probe failed") {
		t.Fatalf("unknown shape must surface probe failure, got ok=%v detail=%q", okU, detailU)
	}
}

func TestV2FailClosedQuarantine(t *testing.T) {
	agent, s := newC1Serve(t, map[string]string{
		"/api/health":                  `{"healthy":true}`,
		"/api/session":                 `{"data":[]}`,
		"/provider":                    testProviderCatalog,
		"/session/ses_x":               `{"id":"ses_x","model":{"id":"glm-4.7","providerID":"zhipuai-coding-plan"}}`,
		"/api/session/ses_x/prompt":    `{}`,
		"/api/session/ses_x/model":     `{}`,
		"/api/session/ses_x/interrupt": `{}`,
	})

	// Honest, distinct status: detected but quarantined.
	ok, detail := agent.InstanceStatus()
	if ok || !strings.Contains(detail, "unsupported-generation (quarantined)") || !strings.Contains(detail, "v2") {
		t.Fatalf("InstanceStatus must name the quarantined v2 generation, got ok=%v detail=%q", ok, detail)
	}

	// Fail closed at the single gate and at every surface.
	if c, err := agent.clientFor(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported-generation (quarantined)") {
		t.Fatalf("clientFor must quarantine v2, got client=%v err=%v", c, err)
	}
	if _, err := agent.ListSessions(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported-generation (quarantined)") {
		t.Fatalf("ListSessions must fail closed, got %v", err)
	}
	if _, err := agent.StartSession(context.Background(), "ses_x"); err == nil || !strings.Contains(err.Error(), "unsupported-generation (quarantined)") {
		t.Fatalf("StartSession must fail closed, got %v", err)
	}
	if models := agent.AvailableModels(context.Background()); len(models) != 0 {
		t.Fatalf("quarantined endpoint must expose zero models, got %v", models)
	}
	// Zero writes of any kind escaped to the v2 fixture.
	if posts := countRequests(s, "POST", ""); len(posts) != 0 {
		t.Fatalf("v2 quarantine must issue ZERO POSTs, got %+v", posts)
	}
}

func TestGenerationV2QuarantineZeroPromptAndZeroKernelIngest(t *testing.T) {
	// The Kernel feed for this backend is the SSE subscriber's core.Event
	// stream — quarantine must mean zero prompt POSTs AND zero event
	// subscriptions, so no canonical event can ever reach the publisher.
	agent, s := newC1Serve(t, map[string]string{
		"/api/health":                  `{"healthy":true}`,
		"/api/session":                 `{"data":[]}`,
		"/provider":                    testProviderCatalog,
		"/api/session/ses_x/prompt":    `{}`,
		"/api/session/ses_x/model":     `{}`,
		"/api/session/ses_x/interrupt": `{}`,
	})

	sess, err := agent.StartSession(context.Background(), "ses_x")
	if err == nil || !strings.Contains(err.Error(), "unsupported-generation (quarantined)") {
		t.Fatalf("StartSession must fail closed on v2, got sess=%v err=%v", sess, err)
	}
	if sess != nil {
		t.Fatal("no session object may exist for a quarantined generation")
	}

	// No prompt / model-switch / interrupt wire writes…
	for _, prefix := range []string{"/api/session/ses_x/prompt", "/api/session/ses_x/model", "/api/session/ses_x/interrupt", "/session/"} {
		if reqs := countRequests(s, "POST", prefix); len(reqs) != 0 {
			t.Fatalf("quarantine must issue ZERO writes to %s, got %+v", prefix, reqs)
		}
	}
	// …and no SSE subscription was ever opened (either generation's route).
	for _, eventPath := range []string{"/global/event", "/api/event"} {
		if reqs := s.requestsFor(eventPath); len(reqs) != 0 {
			t.Fatalf("quarantine must open ZERO event streams at %s, got %+v", eventPath, reqs)
		}
	}
}

func TestBasicAuthDirectoryTimeoutAreControlNotTimeline(t *testing.T) {
	agent, s := newC1Serve(t, map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `[]`,
		"/provider":      testProviderCatalog,
	})

	// Every transport call carries the configured Basic Auth. The single
	// sanctioned exception is the probe's FIRST health call, which is
	// deliberately unauthenticated — its answer is the server_unauthenticated
	// detection signal (a 200 there gets the endpoint rejected).
	if _, err := agent.clientFor(context.Background()); err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if _, err := agent.ListSessionsInDirectory(context.Background(), "/tmp/proj"); err != nil {
		t.Fatalf("ListSessionsInDirectory: %v", err)
	}
	reqs := s.requestsFor("/session")
	if len(reqs) == 0 {
		t.Fatal("expected scoped list requests")
	}
	unauthedProbes := 0
	for _, r := range s.requests {
		if r.Authed {
			continue
		}
		if r.Method == http.MethodGet && r.Path == "/global/health" {
			unauthedProbes++
			continue
		}
		t.Fatalf("only the probe's first health call may be unauthenticated, got %s %s", r.Method, r.Path)
	}
	if unauthedProbes > 1 {
		t.Fatalf("at most one unauthed health probe expected, got %d", unauthedProbes)
	}
	// Directory scoping rides the official query (+ 1.18 header redundancy).
	scoped := false
	for _, r := range reqs {
		if r.Directory == "/tmp/proj" || strings.Contains(r.Query, "directory=") {
			scoped = true
		}
	}
	if !scoped {
		t.Fatalf("scoped list must carry the directory (query/header), got %+v", reqs)
	}

	// Timeouts: regular HTTP is bounded; the SSE client has NO lifetime
	// timeout (a finite one kills streams mid-turn — owner-verified).
	c := newClient("http://127.0.0.1:1", "u", "pw")
	if c.httpClient == nil || c.httpClient.Timeout != 30*time.Second {
		t.Fatalf("regular HTTP client must keep the 30s request timeout, got %+v", c.httpClient)
	}
	if c.streamClient == nil || c.streamClient.Timeout != 0 {
		t.Fatalf("SSE stream client must have NO lifetime timeout, got %+v", c.streamClient)
	}

	// Control plane only: probe + list issued GETs exclusively and emitted no
	// core.Event (no session/subscriber exists, so nothing can reach the
	// timeline — transport never manufactures a turn).
	for _, r := range s.requests {
		if r.Method != http.MethodGet {
			t.Fatalf("transport/control calls must be GET-only here, got %+v", r)
		}
	}
}

func TestSSEReconnectIsTransportOnly(t *testing.T) {
	// A stream that dies with no armed turns must reconnect (bounded backoff)
	// WITHOUT manufacturing any core.Event: the drop itself is transport, not
	// timeline evidence. No terminal is inferred from the reconnect.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var dials atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.URL.Path == "/session":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/provider":
			_, _ = w.Write([]byte(testProviderCatalog))
		case r.URL.Path == "/global/event":
			n := dials.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			// One keepalive comment, then close the response — a mid-life
			// stream drop the subscriber must heal by reconnecting.
			_, _ = fmt.Fprintf(w, ": keepalive %d\n\n", n)
			w.(http.Flusher).Flush()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	a, err := New(map[string]any{
		"work_dir":          "/tmp/proj",
		"opencode_web_url":  srv.URL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	t.Cleanup(func() { _ = agent.Stop() })

	sess, err := agent.StartSession(ctx, "ses_x")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	// Wait for at least one reconnect (bounded backoff ≥1s) with an explicit
	// deadline; then verify zero timeline events crossed the Events channel.
	deadline := time.Now().Add(6 * time.Second)
	for {
		if dials.Load() >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SSE subscriber did not reconnect (dials=%d)", dials.Load())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context expired before reconnect (dials=%d)", dials.Load())
		case ev, open := <-sess.Events():
			if open {
				t.Fatalf("reconnect is transport-only: unexpected core.Event %+v", ev)
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	// Grace period: any manufactured event would surface here.
	grace := time.NewTimer(300 * time.Millisecond)
	defer grace.Stop()
	select {
	case ev, open := <-sess.Events():
		if open {
			t.Fatalf("transport reconnect must not emit timeline events, got %+v", ev)
		}
	case <-grace.C:
	}
}

// result returns the named diagnostic row (directive-002 hole-fill tests).
func result(t *testing.T, report *core.DiagnosticReport, id string) core.DiagnosticResult {
	t.Helper()
	for _, r := range report.Results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("diagnostic row %q missing from %+v", id, report.Results)
	return core.DiagnosticResult{}
}

func TestV2DiagnosticsQuarantinedStopsAtProbe(t *testing.T) {
	// Directive-002: a detected-but-quarantined v2 endpoint must fail
	// diagnostics at the probe row with the shared quarantine wording and
	// STOP — zero /provider catalog reads, zero writes, zero event streams.
	agent, s := newC1Serve(t, map[string]string{
		"/api/health":  `{"healthy":true}`,
		"/api/session": `{"data":[]}`,
		"/provider":    testProviderCatalog,
	})
	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if report.OverallStatus != "failed" {
		t.Fatalf("overall = %q, results %+v", report.OverallStatus, report.Results)
	}
	probe := result(t, report, "ocw_probe")
	if probe.Status != "failed" {
		t.Fatalf("ocw_probe.status = %q, want failed", probe.Status)
	}
	if !strings.Contains(probe.Message, "unsupported-generation") || !strings.Contains(probe.Message, "quarantined") {
		t.Fatalf("ocw_probe message must name the quarantine, got %q", probe.Message)
	}
	for _, r := range report.Results {
		if r.ID == "ocw_catalog" {
			t.Fatalf("quarantine must stop before catalog checks, got %+v", r)
		}
	}
	if reqs := s.requestsFor("/provider"); len(reqs) != 0 {
		t.Fatalf("quarantine must issue ZERO /provider requests, got %+v", reqs)
	}
	if posts := countRequests(s, "POST", ""); len(posts) != 0 {
		t.Fatalf("quarantine must issue ZERO POSTs, got %+v", posts)
	}
	for _, eventPath := range []string{"/global/event", "/api/event"} {
		if reqs := s.requestsFor(eventPath); len(reqs) != 0 {
			t.Fatalf("quarantine must open ZERO event streams at %s, got %+v", eventPath, reqs)
		}
	}
}

func TestDiagnostics118StillPassed(t *testing.T) {
	agent, _ := newC1Serve(t, map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `[]`,
		"/provider":      testProviderCatalog,
	})
	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if report.OverallStatus != "passed" {
		t.Fatalf("1.18.18 diagnostics must stay passed, got %q (%+v)", report.OverallStatus, report.Results)
	}
	probe := result(t, report, "ocw_probe")
	if probe.Status != "passed" || !strings.Contains(probe.Message, "generation=1.18") {
		t.Fatalf("ocw_probe must pass with generation=1.18, got %+v", probe)
	}
}

func TestDiagnosticsUnknownAndUnauthenticatedStayProbeFailures(t *testing.T) {
	// Unknown shape: probe failure, NOT the v2 quarantine verdict.
	unknown, _ := newC1Serve(t, map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `{"weird":true}`,
		"/api/session":   `{"also-weird":true}`,
	})
	report, err := unknown.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if report.OverallStatus != "failed" {
		t.Fatalf("unknown shape must fail, got %q", report.OverallStatus)
	}
	probe := result(t, report, "ocw_probe")
	if probe.Status != "failed" || !strings.Contains(probe.Message, "probe failed") {
		t.Fatalf("unknown shape must be a probe failure, got %+v", probe)
	}
	if strings.Contains(probe.Message, "unsupported-generation") {
		t.Fatalf("unknown shape is NOT the quarantine verdict, got %q", probe.Message)
	}

	// server_unauthenticated: health answers 200 WITHOUT auth — its own probe
	// failure class, distinct from quarantine.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`)) // no auth required — the failure signal
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a, _ := New(map[string]any{
		"work_dir":          "/tmp/proj",
		"opencode_web_url":  srv.URL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	report2, err := a.(*Agent).RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if report2.OverallStatus != "failed" {
		t.Fatalf("unauthenticated server must fail, got %q", report2.OverallStatus)
	}
	probe2 := result(t, report2, "ocw_probe")
	if probe2.Status != "failed" || !strings.Contains(probe2.Message, "server_unauthenticated") {
		t.Fatalf("no-auth 200 must surface server_unauthenticated, got %+v", probe2)
	}
	if strings.Contains(probe2.Message, "unsupported-generation") {
		t.Fatalf("server_unauthenticated is NOT the quarantine verdict, got %q", probe2.Message)
	}
}
