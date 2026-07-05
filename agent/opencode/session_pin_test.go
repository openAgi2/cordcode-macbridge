package opencode

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

func TestOpenCodeSessionPinner_SetListCleanup(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{pinStore: pinstore.New(dir)}

	pt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	// OpenCode pins are scoped by directory (essential for getSession(id, dir) enrichment).
	if _, err := a.SetSessionPinned(context.Background(), "sess-oc-1", "/projA", true, pt); err != nil {
		t.Fatalf("SetSessionPinned A: %v", err)
	}
	if _, err := a.SetSessionPinned(context.Background(), "sess-oc-2", "/projB", true, pt); err != nil {
		t.Fatalf("SetSessionPinned B: %v", err)
	}

	got, err := a.ListPinnedSessions(context.Background())
	if err != nil {
		t.Fatalf("ListPinnedSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 pins across 2 dirs, got %d", len(got))
	}

	// Directory is captured in each entry (handler uses it for getSession).
	byDir := map[string]string{}
	for _, p := range got {
		byDir[p.Directory] = p.SessionID
	}
	if byDir["/projA"] != "sess-oc-1" || byDir["/projB"] != "sess-oc-2" {
		t.Fatalf("directory mapping wrong: %+v", byDir)
	}

	// Unpin one.
	if _, err := a.SetSessionPinned(context.Background(), "sess-oc-1", "/projA", false, time.Time{}); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	got, _ = a.ListPinnedSessions(context.Background())
	if len(got) != 1 || got[0].SessionID != "sess-oc-2" {
		t.Fatalf("expected only sess-oc-2 after unpin, got %+v", got)
	}

	// cleanupPin sweeps by sessionID (DeleteSession doesn't know the directory).
	a.cleanupPin("sess-oc-2")
	got, _ = a.ListPinnedSessions(context.Background())
	if len(got) != 0 {
		t.Fatalf("cleanupPin left %d", len(got))
	}
}

func TestOpenCodeSessionPinner_NilStore(t *testing.T) {
	a := &Agent{}
	if _, err := a.SetSessionPinned(context.Background(), "s", "/w", true, time.Now().UTC()); err == nil {
		t.Fatal("expected ErrStoreUnavailable")
	}
	if _, err := a.ListPinnedSessions(context.Background()); err == nil {
		t.Fatal("expected ErrStoreUnavailable")
	}
	a.cleanupPin("s")
}
