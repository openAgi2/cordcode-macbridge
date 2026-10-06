package opencodeweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// readiness_test.go — structured readiness seam (2026-10-06 plan §3/§8):
// every wire state stays distinct. The regression guard throughout: a probe
// failure must surface as service_not_running, never collapse back into
// not_configured (the boolean-fold behavior the seam replaces).

func TestReadinessEmptyURLNotConfigured(t *testing.T) {
	a, err := New(map[string]any{"work_dir": "/tmp/p"})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	status, detail := agent.StructuredInstanceReadiness()
	if status != ReadinessNotConfigured {
		t.Fatalf("status = %q, want %q", status, ReadinessNotConfigured)
	}
	if detail != NotConfiguredDetail {
		t.Fatalf("detail = %q, want %q", detail, NotConfiguredDetail)
	}
}

func TestReadinessUnreachableServiceNotRunning(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close() // port now closed → probe fails

	a, err := New(map[string]any{"opencode_web_url": url})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	status, detail := agent.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("status = %q, want %q (probe failure must not fold into not_configured)", status, ReadinessServiceNotRunning)
	}
	if !strings.Contains(detail, "probe failed:") {
		t.Fatalf("detail %q should carry the raw probe error", detail)
	}
}

func TestReadinessAuthRejectedServiceNotRunning(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
		},
	})

	// Server requires Basic Auth; the agent carries a mismatched password.
	a, err := New(map[string]any{
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "wrong",
	})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	status, detail := agent.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("status = %q, want %q", status, ReadinessServiceNotRunning)
	}
	if !strings.Contains(detail, "认证失败") {
		t.Fatalf("detail %q should name the auth failure", detail)
	}
	if !strings.Contains(detail, "401") {
		t.Fatalf("detail %q should carry the 401 evidence", detail)
	}
}

func TestReadinessNoAuth200ServiceNotRunning(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: false,
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
		},
	})

	a, err := New(map[string]any{
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "p",
	})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	status, detail := agent.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("status = %q, want %q", status, ReadinessServiceNotRunning)
	}
	if !strings.Contains(detail, "未启用认证") {
		t.Fatalf("detail %q should name the missing-auth rejection", detail)
	}
}

func TestReadinessV2QuarantinedServiceNotRunning(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/api/health":  `{"healthy":true}`,
			"/api/session": `{"data":[]}`,
		},
	})

	a, err := New(map[string]any{
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "p",
	})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	status, detail := agent.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("status = %q, want %q (quarantine is a probe verdict, not not_configured)", status, ReadinessServiceNotRunning)
	}
	if !strings.Contains(detail, "unsupported-generation (quarantined)") || !strings.Contains(detail, "v2") {
		t.Fatalf("detail %q should carry the shared quarantine wording", detail)
	}
}

func TestReadinessGeneration118Available(t *testing.T) {
	base := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
			"/session":       legacySessions,
		},
	})

	a, err := New(map[string]any{
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "p",
	})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	status, detail := agent.StructuredInstanceReadiness()
	if status != ReadinessAvailable {
		t.Fatalf("status = %q, want %q (detail=%q)", status, ReadinessAvailable, detail)
	}
	if !strings.Contains(detail, "generation=1.18") {
		t.Fatalf("detail %q should carry the detected generation", detail)
	}
}

// TestReadinessRecoversAfterServerStarts — the seam re-probes on every read
// when the cached outcome is a failure, so a server started after the last
// failed probe flips the row without a bridge restart (plan §2.2: 行刷新即绿).
func TestReadinessRecoversAfterServerStarts(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()

	a, err := New(map[string]any{
		"opencode_web_url":  url,
		"opencode_web_user": "u",
		"opencode_web_pass": "p",
	})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	status, _ := agent.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("pre-start status = %q, want %q", status, ReadinessServiceNotRunning)
	}

	// The endpoint recovers (simulated by swapping in a healthy 1.18 server;
	// the mechanism under test is that a failed probe is never served from
	// cache — every read re-probes).
	live := startFake(t, &fakeServe{
		healthAuth: true,
		username:   "u",
		password:   "p",
		responses: map[string]string{
			"/global/health": `{"healthy":true}`,
			"/session":       legacySessions,
		},
	})
	agent.baseURL = live
	status, detail := agent.StructuredInstanceReadiness()
	if status != ReadinessAvailable {
		t.Fatalf("post-start status = %q, want %q (failed probe must be retried, detail=%q)", status, ReadinessAvailable, detail)
	}
}
