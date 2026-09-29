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

// rc.6 supported model set (real-device error 2026-08-16): the legacy defaults
// must normalize away — iOS may still hold cached pre-fix model ids.
func TestNormalizeModelNameMapsLegacyDefaults(t *testing.T) {
	cases := map[string]string{
		"deepseek-chat":     defaultModel,
		"deepseek-reasoner": "deepseek-v4-pro",
		"":                  defaultModel,
		"deepseek-v4-flash": "deepseek-v4-flash",
	}
	for in, want := range cases {
		if got := normalizeModelName(in); got != want {
			t.Errorf("normalizeModelName(%q) = %q, want %q", in, got, want)
		}
	}
	a := &Agent{}
	a.SetModel("deepseek-chat")
	if a.model != defaultModel {
		t.Fatalf("SetModel must normalize, got %q", a.model)
	}
}

func TestIsSessionActiveTracksLiveRoots(t *testing.T) {
	a := &Agent{}
	a.markLiveRoot("dsh-live-1")
	if !a.IsSessionActive(context.Background(), "dsh-live-1") {
		t.Fatal("live root must report active")
	}
	if a.IsSessionActive(context.Background(), "dsh-gone") {
		t.Fatal("unknown root must report idle (seal trailing turns)")
	}
	a.clearLiveRoot("dsh-live-1")
	if a.IsSessionActive(context.Background(), "dsh-live-1") {
		t.Fatal("cleared root must report idle")
	}
}
