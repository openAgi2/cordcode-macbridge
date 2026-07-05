package pinstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return New(dir), dir
}

func TestSetPinnedThenGet(t *testing.T) {
	s, _ := newTestStore(t)
	pt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	if err := s.SetPinned("codex", "/home/.codex", "sess-A", "/work", true, pt); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	got, ok := s.Get("codex", "/home/.codex", "sess-A")
	if !ok {
		t.Fatal("Get: not found after SetPinned")
	}
	if got.PinnedAtMillis != pt.UnixMilli() {
		t.Fatalf("PinnedAtMillis=%d want=%d", got.PinnedAtMillis, pt.UnixMilli())
	}
	if got.Directory != "/work" || got.BackendID != "codex" || got.SessionID != "sess-A" {
		t.Fatalf("entry fields wrong: %+v", got)
	}
	if !got.PinnedAt().Equal(pt) {
		t.Fatalf("PinnedAt round-trip mismatch: %v vs %v", got.PinnedAt(), pt)
	}
}

func TestUnpinRemovesEntry(t *testing.T) {
	s, _ := newTestStore(t)
	pt := time.Now().UTC()
	_ = s.SetPinned("codex", "h", "s1", "/w", true, pt)
	if _, ok := s.Get("codex", "h", "s1"); !ok {
		t.Fatal("expected pinned before unpin")
	}
	if err := s.SetPinned("codex", "h", "s1", "/w", false, time.Time{}); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if _, ok := s.Get("codex", "h", "s1"); ok {
		t.Fatal("still pinned after unpin")
	}
	// Unpinning a never-pinned session is a no-op, not an error.
	if err := s.SetPinned("codex", "h", "never", "/w", false, time.Time{}); err != nil {
		t.Fatalf("unpin never-pinned: %v", err)
	}
}

func TestKeyStabilityAcrossInstances(t *testing.T) {
	// Key uniqueness across backend instances / directories / codex homes: same sessionId
	// under different scopes must NOT collide.
	s, dir := newTestStore(t)
	pt := time.Now().UTC()
	for _, scope := range []string{"/homeA/.codex", "/homeB/.codex", ""} {
		if err := s.SetPinned("codex", scope, "same-sess", "/w", true, pt); err != nil {
			t.Fatalf("SetPinned scope=%q: %v", scope, err)
		}
	}
	got := s.List("codex")
	if len(got) != 3 {
		t.Fatalf("expected 3 distinct pins for 3 scopes, got %d", len(got))
	}

	// Key shape is stable across restart (re-create store on same dir) — reload preserves them.
	s2 := New(dir)
	got2 := s2.List("codex")
	if len(got2) != 3 {
		t.Fatalf("after reload: expected 3 pins, got %d", len(got2))
	}
}

func TestBackendIDIsolation(t *testing.T) {
	s, _ := newTestStore(t)
	pt := time.Now().UTC()
	_ = s.SetPinned("codex", "h", "s1", "/w", true, pt)
	_ = s.SetPinned("opencode", "/proj", "s1", "/w", true, pt)
	if len(s.List("codex")) != 1 {
		t.Fatal("codex list leak")
	}
	if len(s.List("opencode")) != 1 {
		t.Fatal("opencode list leak")
	}
	if len(s.List("claudecode")) != 0 {
		t.Fatal("claudecode phantom pins")
	}
}

func TestListSortedPinnedAtDesc(t *testing.T) {
	s, _ := newTestStore(t)
	base := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	_ = s.SetPinned("codex", "h", "old", "/w", true, base)
	_ = s.SetPinned("codex", "h", "newest", "/w", true, base.Add(2*time.Hour))
	_ = s.SetPinned("codex", "h", "mid", "/w", true, base.Add(1*time.Hour))
	got := s.List("codex")
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].SessionID != "newest" || got[1].SessionID != "mid" || got[2].SessionID != "old" {
		t.Fatalf("not sorted desc by pinnedAt: %+v", got)
	}
}

func TestRemoveByEntry(t *testing.T) {
	s, _ := newTestStore(t)
	pt := time.Now().UTC()
	_ = s.SetPinned("opencode", "/projA", "s1", "/projA", true, pt)
	_ = s.SetPinned("opencode", "/projB", "s2", "/projB", true, pt)
	if !s.RemoveEntry("opencode", "s1", "/projA") {
		t.Fatal("RemoveEntry s1 said not-found")
	}
	if s.RemoveEntry("opencode", "s1", "/projA") {
		t.Fatal("RemoveEntry second call should be no-op/false")
	}
	got := s.List("opencode")
	if len(got) != 1 || got[0].SessionID != "s2" {
		t.Fatalf("expected only s2 remaining, got %+v", got)
	}
}

func TestAtomicReplaceNoTornFile(t *testing.T) {
	// After a write, the file must be valid JSON (atomic replace never leaves a partial file
	// visible to readers). Read the raw file and json-check it.
	s, dir := newTestStore(t)
	pt := time.Now().UTC()
	for i := 0; i < 20; i++ {
		if err := s.SetPinned("codex", "h", "s", "/w", true, pt.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, Filename))
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if !isJSON(raw) {
			t.Fatalf("write %d produced non-JSON file (torn write)", i)
		}
	}
}

func TestConcurrentWritersDoNotLoseUpdates(t *testing.T) {
	// N goroutines each pin a distinct session against one shared store; after join, all N
	// must be present. A missing mutex or non-atomic replace would drop entries or corrupt
	// the file.
	s, _ := newTestStore(t)
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			if err := s.SetPinned("codex", "h", sessName(i), "/w", true, time.Now().UTC()); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent SetPinned: %v", err)
	}
	got := s.List("codex")
	if len(got) != n {
		t.Fatalf("lost updates: expected %d pins, got %d", n, len(got))
	}
}

func TestMissingDirectoryCreatesParent(t *testing.T) {
	// New(dir) where dir does not yet exist should still write (save() MkdirAlls parent).
	dir := filepath.Join(t.TempDir(), "nested", "missing")
	s := New(dir)
	if err := s.SetPinned("codex", "h", "s1", "/w", true, time.Now().UTC()); err != nil {
		t.Fatalf("SetPinned into missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, Filename)); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestCorruptFileIsErrorNotSilentReset(t *testing.T) {
	// A corrupt pin file must surface an error, never silently reset to empty (that would
	// lose real pin data).
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if err := s.SetPinned("codex", "h", "s1", "/w", true, time.Now().UTC()); err == nil {
		t.Fatal("expected error on corrupt file, got nil")
	}
}

func TestSetPinnedRejectsEmptyID(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SetPinned("", "h", "s1", "/w", true, time.Now().UTC()); err == nil {
		t.Fatal("expected error for empty backendID")
	}
	if err := s.SetPinned("codex", "h", "", "/w", true, time.Now().UTC()); err == nil {
		t.Fatal("expected error for empty sessionID")
	}
}

func sessName(i int) string {
	return fmt.Sprintf("sess-%03d", i)
}

func isJSON(b []byte) bool {
	var v interface{}
	return json.Unmarshal(b, &v) == nil
}
