package codexremote

import (
	"context"
	"errors"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestIdentityIsIndependent(t *testing.T) {
	if BackendID != "codex-remote" || WireKind != "codex-remote" {
		t.Fatalf("identity = %s/%s", BackendID, WireKind)
	}
	if DisplayName != "Codex Desktop" {
		t.Fatalf("display name = %q", DisplayName)
	}
	if BackendID == "codex" || BackendID == "codex-web" {
		t.Fatal("codex-remote must not reuse legacy identities")
	}
}

func TestFactoryRegistersFailClosedAgent(t *testing.T) {
	agent, err := NewAgentFactory(map[string]any{"work_dir": "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name() != BackendID {
		t.Fatalf("Name = %q", agent.Name())
	}
	if _, err := agent.ListSessions(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ListSessions error = %v", err)
	}
	if _, err := agent.StartSession(context.Background(), "thread_probe"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("StartSession error = %v", err)
	}
	if err := agent.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestWireDescriptorDoesNotAdvertiseUnprovenCapabilities(t *testing.T) {
	agent := New(nil)
	desc := agent.WireDescriptor()
	if desc.Kind != WireKind || desc.DisplayName != DisplayName {
		t.Fatalf("descriptor identity = %+v", desc)
	}
	if desc.LiveEventModel != core.LiveEventBroadcast {
		t.Fatalf("live model = %q", desc.LiveEventModel)
	}
	if desc.RequiresExternalTurnPolling {
		t.Fatal("controller stream must not require external-turn polling once wired")
	}
	// Phase 3 flip (2026-08-30): turn_detail_lazy_v1 graduated from unproven to the
	// single proven static capability (§11.7; iOS client shipped + G2 closed before the
	// server advertised it). Exact-singleton assertion lives in wire_descriptor_test.go.
	if len(desc.StaticCapabilities) != 1 || desc.StaticCapabilities[0] != "turn_detail_lazy_v1" {
		t.Fatalf("StaticCapabilities = %v, want exactly [turn_detail_lazy_v1]", desc.StaticCapabilities)
	}
}

func TestInstanceStatusNotConfigured(t *testing.T) {
	ok, detail := New(nil).InstanceStatus()
	if ok {
		t.Fatal("unenrolled agent must not report available")
	}
	if detail == "" {
		t.Fatal("need a user-facing reason")
	}
}

func TestCoreRegistryHasCodexRemote(t *testing.T) {
	for _, name := range core.ListRegisteredAgents() {
		if name == BackendID {
			return
		}
	}
	t.Fatal("init must register BackendID on the global agent registry")
}
