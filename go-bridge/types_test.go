package gobridge

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type testRegistrySession struct {
	core.AgentSession
	id string
}

func (f *testRegistrySession) CurrentSessionID() string {
	return f.id
}

func (f *testRegistrySession) Close() error {
	return nil
}

func TestSessionRegistry_DeleteClearsBothIDs(t *testing.T) {
	registry := newSessionRegistry()
	fakeSess := &testRegistrySession{id: "thread-real-123"}

	// 1. Put raw session under pending ID
	registry.putRaw("pending-123", fakeSess)

	// Verify it exists under pending-123
	if _, ok := registry.get("pending-123"); !ok {
		t.Fatal("expected pending-123 to exist in registry")
	}

	// 2. Rebind pending ID to real ID
	registry.rebind("pending-123", "thread-real-123")

	// Verify both exist
	if _, ok := registry.get("pending-123"); !ok {
		t.Fatal("expected pending-123 to still exist in registry after rebind")
	}
	if _, ok := registry.get("thread-real-123"); !ok {
		t.Fatal("expected thread-real-123 to exist in registry after rebind")
	}

	// 3. Delete using pending ID
	sess, ok := registry.delete("pending-123")
	if !ok || sess == nil {
		t.Fatal("expected delete to return the session")
	}

	// Verify both keys are completely removed
	if _, ok := registry.get("pending-123"); ok {
		t.Error("expected pending-123 to be deleted")
	}
	if _, ok := registry.get("thread-real-123"); ok {
		t.Error("expected thread-real-123 to be deleted")
	}
}

func TestSessionRegistry_DeleteClearsBothIDsUsingRealID(t *testing.T) {
	registry := newSessionRegistry()
	fakeSess := &testRegistrySession{id: "thread-real-456"}

	// 1. Put raw session under pending ID
	registry.putRaw("pending-456", fakeSess)

	// 2. Rebind pending ID to real ID
	registry.rebind("pending-456", "thread-real-456")

	// 3. Delete using real ID
	sess, ok := registry.delete("thread-real-456")
	if !ok || sess == nil {
		t.Fatal("expected delete to return the session")
	}

	// Verify both keys are completely removed
	if _, ok := registry.get("pending-456"); ok {
		t.Error("expected pending-456 to be deleted")
	}
	if _, ok := registry.get("thread-real-456"); ok {
		t.Error("expected thread-real-456 to be deleted")
	}
}
