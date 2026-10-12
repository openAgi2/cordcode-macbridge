package codexremote

// P2 readiness-state tests (docs/2026-10-13-codex-remote-absence-hardening-
// p2-plan.md §3 D1 / S-1): the five readiness states map from the absence
// class written at restoreOnce's single exit point — set on failure,
// cleared on success; unknown/cleared fail-closes to service_not_running
// (never not_configured).

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestClassifyRestoreError(t *testing.T) {
	cases := []struct {
		err  error
		want restoreErrClass
	}{
		{errors.New("请先安装并登录 ChatGPT Desktop"), errClassNotInstalled},
		{errors.New("ChatGPT 未登录"), errClassNotLoggedIn},
		{errors.New("no desktop environment"), errClassDesktopOffline},
		{errors.New("读取 ChatGPT 登录态超时"), errClassTransient},
		{errors.New("pairing state incomplete"), errClassTransient},
		{errPairingRevoked, errClassTransient},
	}
	for _, tc := range cases {
		if got := classifyRestoreError(tc.err); got != tc.want {
			t.Fatalf("classify(%v) = %s, want %s", tc.err, got, tc.want)
		}
	}
}

func setRestoreClass(t *testing.T, agent *Agent, class restoreErrClass) {
	t.Helper()
	agent.pairing.mu.Lock()
	agent.pairing.lastRestoreClass = class
	agent.pairing.mu.Unlock()
}

func TestReadinessMapsAbsenceClasses(t *testing.T) {
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		class restoreErrClass
		want  string
	}{
		{errClassNotInstalled, ReadinessNotDetected},
		{errClassNotLoggedIn, ReadinessNotLoggedIn},
		{errClassDesktopOffline, ReadinessServiceNotRunning},
		{errClassTransient, ReadinessServiceNotRunning},
		{"", ReadinessServiceNotRunning}, // cleared after success / pre-first-attempt: fail-closed
	}
	for _, tc := range cases {
		agent := newPersistedAgent(t, key)
		setRestoreClass(t, agent, tc.class)
		status, _ := agent.StructuredInstanceReadiness()
		if status != tc.want {
			t.Fatalf("class %q → status %s, want %s", tc.class, status, tc.want)
		}
	}
}

func TestRestoreFailureSetsClassAndReadinessReflects(t *testing.T) {
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := gateRestoreServer(t, key, false, 0)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent := newPersistedAgent(t, key)
	agent.pairing.http = srv.Client()
	agent.pairing.authTokenFn = func(context.Context) (string, string, error) {
		return "", "", errors.New("请先安装并登录 ChatGPT Desktop")
	}

	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err == nil {
		t.Fatal("not-installed restore must fail")
	}
	if got := agent.pairing.restoreAbsenceClass(); got != errClassNotInstalled {
		t.Fatalf("class after not-installed failure: %q", got)
	}
	status, _ := agent.StructuredInstanceReadiness()
	if status != ReadinessNotDetected {
		t.Fatalf("readiness after not-installed failure: %s, want not_detected", status)
	}
}

func TestRestoreSuccessClearsClass(t *testing.T) {
	// r1 F-4: a successful restore must clear the stale class — the first
	// window after a disconnect must not keep labeling the row with the
	// pre-success absence cause.
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := gateRestoreServer(t, key, true, 0)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent, _ := newGatedAgent(t, key, time.Now().Add(9*time.Minute).Format(time.RFC3339))
	agent.pairing.http = srv.Client()
	stubBind(t, agent)
	setRestoreClass(t, agent, errClassNotInstalled) // stale class from an earlier absence

	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	if got := agent.pairing.restoreAbsenceClass(); got != "" {
		t.Fatalf("class must clear on success, got %q", got)
	}
	status, _ := agent.StructuredInstanceReadiness()
	if status != ReadinessAvailable {
		t.Fatalf("readiness after restore: %s, want available", status)
	}
	// Post-success disconnect window (client gone, class still cleared):
	// fail-closed service_not_running, never the stale not_detected.
	agent.mu.Lock()
	agent.client = nil
	agent.mu.Unlock()
	status, _ = agent.StructuredInstanceReadiness()
	if status != ReadinessServiceNotRunning {
		t.Fatalf("post-success window readiness: %s, want service_not_running (fail-closed)", status)
	}
}

func TestProbeTimeoutClassifiesTransient(t *testing.T) {
	key, err := newDeviceKeyStore().Create()
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := gateRestoreServer(t, key, false, 0)
	t.Cleanup(srv.Close)
	prev := officialAPIBase
	officialAPIBase = srv.URL
	t.Cleanup(func() { officialAPIBase = prev })

	agent := newPersistedAgent(t, key)
	agent.pairing.http = srv.Client()
	agent.pairing.authTokenFn = func(context.Context) (string, string, error) {
		return "", "", errors.New("读取 ChatGPT 登录态超时")
	}
	if _, err := agent.StartRemoteControl(context.Background(), "", ""); err == nil {
		t.Fatal("timeout restore must fail")
	}
	if got := agent.pairing.restoreAbsenceClass(); got != errClassTransient {
		t.Fatalf("class after probe timeout: %q, want transient", got)
	}
	if status, _ := agent.StructuredInstanceReadiness(); status != ReadinessServiceNotRunning {
		t.Fatalf("timeout readiness: %s, want service_not_running", status)
	}
	_ = http.StatusOK
}
