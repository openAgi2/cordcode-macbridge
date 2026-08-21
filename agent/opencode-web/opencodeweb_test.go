package opencodeweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestNewEmptyURLIsNotConfigured(t *testing.T) {
	a, err := New(map[string]any{"work_dir": "/tmp/p"})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	available, detail := agent.InstanceStatus()
	if available {
		t.Fatal("empty URL must be unavailable")
	}
	if detail != NotConfiguredDetail {
		t.Fatalf("detail = %q, want %q", detail, NotConfiguredDetail)
	}
	if _, err := agent.ListSessions(context.Background()); err == nil || !strings.Contains(err.Error(), "not configured") && !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("ListSessions on empty URL should fail loudly, got %v", err)
	}
	if _, err := agent.StartSession(context.Background(), ""); err == nil {
		t.Fatal("StartSession on skeleton should fail loudly")
	}
	if err := agent.Stop(); err != nil {
		t.Fatalf("Stop err: %v", err)
	}
}

func TestInstanceStatusMirrorsProbeFailure(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close() // port now closed → probe fails

	a, err := New(map[string]any{"opencode_web_url": url})
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	agent := a.(*Agent)
	available, detail := agent.InstanceStatus()
	if available {
		t.Fatal("dead endpoint must be unavailable")
	}
	if !strings.Contains(detail, "probe failed:") {
		t.Fatalf("detail %q should carry probe failure detail", detail)
	}
	if _, err := agent.clientFor(context.Background()); err == nil {
		t.Fatal("clientFor must fail on unusable endpoint")
	}
}

func TestInstanceStatusSuccessCarriesGeneration(t *testing.T) {
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
	available, detail := agent.InstanceStatus()
	if !available {
		t.Fatalf("probe should succeed, detail=%q", detail)
	}
	if !strings.Contains(detail, "generation=1.18") || !strings.Contains(detail, "url=") {
		t.Fatalf("detail %q should carry generation+url", detail)
	}
	c, err := agent.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor err: %v", err)
	}
	if c.Generation() != generation118 {
		t.Fatalf("client generation = %q, want 1.18", c.Generation())
	}
}

func TestRegistrationByName(t *testing.T) {
	got, err := core.CreateAgent("opencode-web", map[string]any{})
	if err != nil {
		t.Fatalf("CreateAgent err: %v", err)
	}
	if got.Name() != "opencode-web" {
		t.Fatalf("Name = %q", got.Name())
	}
}

func TestWireDescriptorMatchesDesign(t *testing.T) {
	a, _ := New(map[string]any{})
	wd := a.(*Agent).WireDescriptor()
	if wd.Kind != "opencode-web" {
		t.Fatalf("Kind = %q", wd.Kind)
	}
	if wd.DisplayName != "OpenCode Web" {
		t.Fatalf("DisplayName = %q", wd.DisplayName)
	}
	if wd.LiveEventModel != core.LiveEventBroadcast {
		t.Fatalf("LiveEventModel = %q", wd.LiveEventModel)
	}
	if wd.RequiresExternalTurnPolling {
		t.Fatal("RequiresExternalTurnPolling must be false (SSE broadcast covers external turns)")
	}
	if len(wd.StaticCapabilities) != 1 || wd.StaticCapabilities[0] != "external_turn_streaming" {
		t.Fatalf("StaticCapabilities = %v (must be exactly external_turn_streaming; no todos/question_reply)", wd.StaticCapabilities)
	}
}

func TestWorkDirSwitcher(t *testing.T) {
	a, _ := New(map[string]any{"work_dir": "/tmp/first"})
	agent := a.(*Agent)
	if agent.GetWorkDir() != "/tmp/first" {
		t.Fatalf("GetWorkDir = %q", agent.GetWorkDir())
	}
	agent.SetWorkDir("/tmp/second")
	if agent.GetWorkDir() != "/tmp/second" {
		t.Fatalf("GetWorkDir after set = %q", agent.GetWorkDir())
	}
}
