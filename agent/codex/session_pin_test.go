package codex

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

func TestCodexSessionPinner_SetListCleanup(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{pinStore: pinstore.New(dir), codexHome: "/home/.codex"}

	pt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	pin, err := a.SetSessionPinned(context.Background(), "sess-codex-1", "/work", true, pt)
	if err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}
	if pin == nil || pin.SessionID != "sess-codex-1" {
		t.Fatalf("unexpected pin: %+v", pin)
	}

	// Codex scope = CODEX_HOME.
	if _, ok := a.pinStore.Get("codex", "/home/.codex", "sess-codex-1"); !ok {
		t.Fatal("not found under codexHome scope")
	}

	got, err := a.ListPinnedSessions(context.Background())
	if err != nil {
		t.Fatalf("ListPinnedSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}

	// A second agent with a DIFFERENT codexHome isolates pins (no cross-home collision).
	a2 := &Agent{pinStore: a.pinStore, codexHome: "/other/.codex"}
	if _, err := a2.SetSessionPinned(context.Background(), "sess-codex-1", "/work", true, pt); err != nil {
		t.Fatalf("SetSessionPinned a2: %v", err)
	}
	// Both scopes coexist for the same session ID.
	all := a.pinStore.List("codex")
	if len(all) != 2 {
		t.Fatalf("expected 2 pins across 2 codex homes, got %d", len(all))
	}

	// cleanupPin on a removes only a's scoped pin (RemoveAll sweeps by sessionID across
	// scopes — acceptable for delete; the other-scope pin would also be swept since delete
	// implies the session is gone globally).
	a.cleanupPin("sess-codex-1")
	if rem := a.pinStore.RemoveAll("codex", "sess-codex-1"); rem != 0 {
		t.Fatalf("expected 0 remaining after cleanup, RemoveAll removed %d", rem)
	}
}

func TestCodexSessionPinner_NilStore(t *testing.T) {
	a := &Agent{}
	if _, err := a.SetSessionPinned(context.Background(), "s", "/w", true, time.Now().UTC()); err == nil {
		t.Fatal("expected ErrStoreUnavailable")
	}
	if _, err := a.ListPinnedSessions(context.Background()); err == nil {
		t.Fatal("expected ErrStoreUnavailable")
	}
	a.cleanupPin("s")
}
