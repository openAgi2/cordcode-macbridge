package opencodeweb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// permissionFake is a generation-aware fake for the §3.4 folding tests: it
// records every permission-reply body and accepts only the configured
// literals (others 400) — the 1.18 probe-first scenario.
type permissionFake struct {
	mu     sync.Mutex
	bodies []string
	accept map[string]bool
	gen    generation
}

func (f *permissionFake) start(t *testing.T) string {
	t.Helper()
	health := func(w http.ResponseWriter, ok bool) {
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"healthy":true}`))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		if f.gen == generationV2 {
			// checkout v2 has no /global routes at all.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, pass, ok := r.BasicAuth()
		health(w, ok && pass == "pw")
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		health(w, ok && pass == "pw")
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	mux.HandleFunc("/global/event", func(w http.ResponseWriter, r *http.Request) {
		if f.gen == generationV2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Everything else under /session… or /api/session… is treated as a
		// permission reply route for recording purposes.
		body, _ := io.ReadAll(r.Body)
		literal := ""
		if f.gen == generationV2 {
			var parsed struct {
				Reply string `json:"reply"`
			}
			_ = json.Unmarshal(body, &parsed)
			literal = parsed.Reply
		} else {
			var parsed struct {
				Response string `json:"response"`
			}
			_ = json.Unmarshal(body, &parsed)
			literal = parsed.Response
		}
		f.mu.Lock()
		f.bodies = append(f.bodies, r.URL.Path+" "+string(body))
		accepted := f.accept[literal]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if accepted {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid response value"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *permissionFake) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.bodies))
	copy(out, f.bodies)
	return out
}

func newPermissionAgent(t *testing.T, f *permissionFake) *Agent {
	t.Helper()
	base := f.start(t)
	a, err := New(map[string]any{
		"work_dir":          "/tmp/p",
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a.(*Agent)
}

func TestPermissionFoldAllowPrefersOnceThenFallsBack(t *testing.T) {
	// Scenario A: 1.18 accepts `once` → single POST {"response":"once"}.
	f := &permissionFake{accept: map[string]bool{"once": true}}
	agent := newPermissionAgent(t, f)
	if err := agent.RespondSessionPermission(context.Background(), "ses_1", "pr_1", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("allow with once accepted: %v", err)
	}
	rec := f.recorded()
	if len(rec) != 1 || !strings.Contains(rec[0], `"response":"once"`) {
		t.Fatalf("must send once first and stop, got %v", rec)
	}

	// Scenario B: `once` rejected (4xx) → fallback literal `allow`.
	f2 := &permissionFake{accept: map[string]bool{"allow": true}}
	agent2 := newPermissionAgent(t, f2)
	if err := agent2.RespondSessionPermission(context.Background(), "ses_1", "pr_1", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("allow with once rejected must fall back: %v", err)
	}
	rec2 := f2.recorded()
	if len(rec2) != 2 || !strings.Contains(rec2[0], `"response":"once"`) || !strings.Contains(rec2[1], `"response":"allow"`) {
		t.Fatalf("must probe once then fall back to allow, got %v", rec2)
	}
}

func TestPermissionFoldDenyPrefersRejectThenFallsBack(t *testing.T) {
	f := &permissionFake{accept: map[string]bool{"reject": true}}
	agent := newPermissionAgent(t, f)
	if err := agent.RespondSessionPermission(context.Background(), "ses_1", "pr_1", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("deny with reject accepted: %v", err)
	}
	rec := f.recorded()
	if len(rec) != 1 || !strings.Contains(rec[0], `"response":"reject"`) {
		t.Fatalf("must send reject first, got %v", rec)
	}

	f2 := &permissionFake{accept: map[string]bool{"deny": true}}
	agent2 := newPermissionAgent(t, f2)
	if err := agent2.RespondSessionPermission(context.Background(), "ses_1", "pr_1", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("deny fallback: %v", err)
	}
	rec2 := f2.recorded()
	if len(rec2) != 2 || !strings.Contains(rec2[1], `"response":"deny"`) {
		t.Fatalf("must fall back to deny, got %v", rec2)
	}
}

func TestPermissionFoldV2SendsReplyLiteral(t *testing.T) {
	f := &permissionFake{gen: generationV2, accept: map[string]bool{"once": true, "reject": true}}
	agent := newPermissionAgent(t, f)
	if c, err := agent.clientFor(context.Background()); err != nil || c.Generation() != generationV2 {
		t.Fatalf("generation must resolve v2, got %v err=%v", c.Generation(), err)
	}
	if err := agent.RespondSessionPermission(context.Background(), "ses_1", "pr_1", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("v2 allow: %v", err)
	}
	if err := agent.RespondSessionPermission(context.Background(), "ses_1", "pr_2", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("v2 deny: %v", err)
	}
	rec := f.recorded()
	if len(rec) != 2 {
		t.Fatalf("v2 replies = %v", rec)
	}
	if !strings.Contains(rec[0], "/api/session/ses_1/permission/pr_1/reply") || !strings.Contains(rec[0], `"reply":"once"`) {
		t.Fatalf("v2 allow must POST {reply:once} to the v2 route, got %v", rec)
	}
	if !strings.Contains(rec[1], `"reply":"reject"`) {
		t.Fatalf("v2 deny must POST {reply:reject}, got %v", rec)
	}
}

func TestRespondPermissionNeedsIDs(t *testing.T) {
	agent, _ := newSendAgent(t, map[string]string{})
	if err := agent.RespondSessionPermission(context.Background(), "", "pr_1", core.PermissionResult{Behavior: "allow"}); err == nil {
		t.Fatal("empty session id must fail")
	}
	if err := agent.RespondSessionPermission(context.Background(), "ses_1", "", core.PermissionResult{Behavior: "allow"}); err == nil {
		t.Fatal("empty request id must fail")
	}
}

func TestToolAuthorizerRecordsVerbatim(t *testing.T) {
	agent, _ := newSendAgent(t, map[string]string{})
	if err := agent.AddAllowedTools("read", "bash"); err != nil {
		t.Fatalf("AddAllowedTools: %v", err)
	}
	got := agent.GetAllowedTools()
	if len(got) != 2 || got[0] != "read" || got[1] != "bash" {
		t.Fatalf("allowed tools must round-trip verbatim, got %v", got)
	}
	var authorizer core.ToolAuthorizer = agent
	_ = authorizer
}

func TestDiagnosticsCarriesGenerationAndFold(t *testing.T) {
	agent, _ := newSendAgent(t, map[string]string{})
	report, err := agent.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics: %v", err)
	}
	if report.OverallStatus != "passed" {
		t.Fatalf("overall = %q, results %+v", report.OverallStatus, report.Results)
	}
	var genLine, foldLine string
	for _, r := range report.Results {
		if r.ID == "ocw_probe" {
			genLine = r.Message
		}
		if r.ID == "ocw_permission_fold" {
			foldLine = r.Message
		}
	}
	if !strings.Contains(genLine, "generation=1.18") {
		t.Fatalf("diagnostics must carry the generation, got %q", genLine)
	}
	if !strings.Contains(foldLine, "permission folding") {
		t.Fatalf("diagnostics must carry the folding state, got %q", foldLine)
	}

	// Empty URL: honest failure with the shared not-configured text.
	empty, _ := New(map[string]any{})
	report2, err := empty.(*Agent).RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunDiagnostics(empty): %v", err)
	}
	if report2.OverallStatus != "failed" || !strings.Contains(report2.Results[0].Message, NotConfiguredDetail) {
		t.Fatalf("empty endpoint diagnostics = %+v", report2.Results)
	}
}
