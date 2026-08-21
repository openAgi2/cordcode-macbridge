package opencodeweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeServe is a configurable stand-in for the official `opencode serve`,
// scoped to the routes the generation probe touches. healthAuth toggles
// Basic Auth enforcement; the session routes answer shapes recorded per path.
type fakeServe struct {
	healthAuth bool // require Basic Auth on health routes (managed-local style)

	username string
	password string

	// responses keyed by path, e.g. "/global/health", "/api/health",
	// "/session", "/api/session". Missing path = 404.
	responses map[string]string
}

func (f *fakeServe) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if f.healthAuth && (r.URL.Path == "/global/health" || r.URL.Path == "/api/health") {
			if _, pass, ok := r.BasicAuth(); !ok || pass != f.password {
				w.Header().Set("WWW-Authenticate", `Basic realm="opencode"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		body, ok := f.responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// startFake spins the fake serve up and returns its base URL.
func startFake(t *testing.T, f *fakeServe) string {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

const legacySessions = `[{"id":"ses_a","title":"A","directory":"/tmp/p"}]`
const v2Sessions = `{"data":[{"id":"ses_a"}],"cursor":null}`

func TestProbeSelectsLegacyGenerationWithAuth(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
			// Dual-presence (design §3.2 S3): 1.18.18 also answers /api/* —
			// the /global/health route is the mutual-exclusion signal.
			"/api/health":  `{"healthy":true}`,
			"/api/session": v2Sessions,
			"/session":     legacySessions,
		},
	})

	c := newClient(base, "u", "p")
	res := probeInstance(context.Background(), c)
	if res.err != nil {
		t.Fatalf("probe err: %v", res.err)
	}
	if res.gen != generation118 {
		t.Fatalf("gen = %q, want 1.18 (dual-presence must not flip to v2)", res.gen)
	}
	if !strings.Contains(res.detail, "generation=1.18") {
		t.Fatalf("detail %q missing generation marker", res.detail)
	}
}

func TestProbeSelectsV2GenerationWhenLegacyRouteMissing(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/api/health":  `{"healthy":true}`,
			"/api/session": v2Sessions,
		},
	})

	c := newClient(base, "u", "p")
	res := probeInstance(context.Background(), c)
	if res.err != nil {
		t.Fatalf("probe err: %v", res.err)
	}
	if res.gen != generationV2 {
		t.Fatalf("gen = %q, want v2", res.gen)
	}
}

func TestProbeFailsWithoutCredentialsWhenAuthRequired(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
			"/session":       legacySessions,
		},
	})

	c := newClient(base, "", "")
	res := probeInstance(context.Background(), c)
	if res.err == nil {
		t.Fatal("probe should fail without credentials")
	}
	if !strings.Contains(res.err.Error(), "401") {
		t.Fatalf("err %q should mention 401", res.err)
	}
}

func TestProbeRejectsUnauthenticatedServer(t *testing.T) {
	base := startFake(t, &fakeServe{
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
			"/session":       legacySessions,
		},
	})

	c := newClient(base, "", "")
	res := probeInstance(context.Background(), c)
	if res.err == nil {
		t.Fatal("no-auth 200 health must be rejected (server_unauthenticated)")
	}
	if !strings.Contains(res.err.Error(), "server_unauthenticated") {
		t.Fatalf("err %q should carry server_unauthenticated", res.err)
	}
}

func TestProbeShapeArbiterFlipsGeneration(t *testing.T) {
	// Health suggests v2 (no /global/health route) but /api/session serves the
	// bare array while /session also serves the bare array: the shape arbiter
	// trusts the path that matches its own generation's shape → 1.18.
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/api/health":  `{"healthy":true}`,
			"/api/session": legacySessions,
			"/session":     legacySessions,
		},
	})

	c := newClient(base, "u", "p")
	res := probeInstance(context.Background(), c)
	if res.err != nil {
		t.Fatalf("probe err: %v", res.err)
	}
	if res.gen != generation118 {
		t.Fatalf("gen = %q, want 1.18 (bare array wins)", res.gen)
	}
}

func TestProbeFailsWhenNoRouteAnswers(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses:  map[string]string{},
	})

	c := newClient(base, "u", "p")
	res := probeInstance(context.Background(), c)
	if res.err == nil {
		t.Fatal("probe should fail with no routes")
	}
	for _, frag := range []string{"/global/health", "/api/health"} {
		if !strings.Contains(res.err.Error(), frag) {
			t.Fatalf("err %q should record tried path %s", res.err, frag)
		}
	}
}
