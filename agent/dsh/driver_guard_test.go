package dsh

// Driver-level dead-session guard (design §4.5): StartSession on an id that
// exists in the user harness store fails deterministically with
// ErrSessionNotResumable — the pinned SDK has no cross-process resume, and the
// harness would otherwise refuse at the first session/prompt materialization.
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDriverStartSessionDeadStoreIDFailsDeterministically(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	dir := filepath.Join(home, "sessions", "--demo--", "session-drv-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	header := `{"type":"session","version":0,"id":"session-drv-1","createdAt":1,"cwd":"/demo","delegationDepth":0}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{workDir: t.TempDir()}
	if _, err := a.StartSession(context.Background(), "session-drv-1"); !errors.Is(err, ErrSessionNotResumable) {
		t.Fatalf("dead store id must fail with ErrSessionNotResumable, got %v", err)
	}
	// An id absent from the store passes the guard (it may lazily create a
	// fresh harness session under that id); whatever happens next must be a
	// different error — usually the spawn failure in this bare test setup.
	_, err := a.StartSession(context.Background(), "not-in-store")
	if errors.Is(err, ErrSessionNotResumable) {
		t.Fatalf("unknown id must not trip the resume guard: %v", err)
	}
}
