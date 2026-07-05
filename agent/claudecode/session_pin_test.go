package claudecode

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

func TestClaudeSessionPinner_SetListCleanup(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{pinStore: pinstore.New(dir)}

	pt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	pin, err := a.SetSessionPinned(context.Background(), "sess-claude-1", "/work", true, pt)
	if err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}
	if pin == nil || pin.SessionID != "sess-claude-1" || pin.PinnedAt.IsZero() {
		t.Fatalf("unexpected pin: %+v", pin)
	}

	got, err := a.ListPinnedSessions(context.Background())
	if err != nil {
		t.Fatalf("ListPinnedSessions: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "sess-claude-1" {
		t.Fatalf("expected 1 pin, got %+v", got)
	}
	// Claude IDs are globally unique -> empty scope key.
	e, ok := a.pinStore.Get("claudecode", "", "sess-claude-1")
	if !ok {
		t.Fatal("not found under empty scope")
	}
	if e.Directory != "/work" {
		t.Fatalf("directory not stored: %q", e.Directory)
	}

	// Unpin.
	if _, err := a.SetSessionPinned(context.Background(), "sess-claude-1", "/work", false, time.Time{}); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	got, _ = a.ListPinnedSessions(context.Background())
	if len(got) != 0 {
		t.Fatalf("expected 0 after unpin, got %d", len(got))
	}

	// Re-pin then cleanupPin (DeleteSession path).
	if _, err := a.SetSessionPinned(context.Background(), "sess-claude-1", "/work", true, pt); err != nil {
		t.Fatalf("re-pin: %v", err)
	}
	a.cleanupPin("sess-claude-1")
	got, _ = a.ListPinnedSessions(context.Background())
	if len(got) != 0 {
		t.Fatalf("cleanupPin left %d entries", len(got))
	}
}

func TestClaudeSessionPinner_NilStore(t *testing.T) {
	a := &Agent{} // no pinStore
	if _, err := a.SetSessionPinned(context.Background(), "s", "/w", true, time.Now().UTC()); err == nil {
		t.Fatal("expected ErrStoreUnavailable with nil pinStore")
	}
	if _, err := a.ListPinnedSessions(context.Background()); err == nil {
		t.Fatal("expected ErrStoreUnavailable with nil pinStore")
	}
	// cleanupPin must be a safe no-op on nil store.
	a.cleanupPin("s")
}
