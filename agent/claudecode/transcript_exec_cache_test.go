package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTranscript writes content to path and returns it.
func writeTranscript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestTranscriptExecCache_UnchangedFileNotReparsed proves an unchanged transcript
// (same sessionID+path+size+mtime) is a cache hit and skips the JSONL reparse on
// repeated list refreshes. misses counts reparses; a hit must not increment it.
func TestTranscriptExecCache_UnchangedFileNotReparsed(t *testing.T) {
	transcriptExec.reset()
	path := filepath.Join(t.TempDir(), "ses.jsonl")
	writeTranscript(t, path, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n")

	v1 := isSessionExecutingCached("ses", path)
	if got := transcriptExec.misses.Load(); got != 1 {
		t.Fatalf("first eval misses=%d, want 1", got)
	}
	if got := transcriptExec.hits.Load(); got != 0 {
		t.Fatalf("first eval hits=%d, want 0", got)
	}

	v2 := isSessionExecutingCached("ses", path)
	if v1 != v2 {
		t.Fatalf("cached value changed: %v -> %v for unchanged file", v1, v2)
	}
	if got := transcriptExec.hits.Load(); got != 1 {
		t.Fatalf("second eval hits=%d, want 1 (unchanged file must hit cache)", got)
	}
	if got := transcriptExec.misses.Load(); got != 1 {
		t.Fatalf("second eval misses=%d, want 1 (unchanged file must NOT reparse)", got)
	}
}

// TestTranscriptExecCache_SizeChangeInvalidates proves a size change forces
// re-evaluation.
func TestTranscriptExecCache_SizeChangeInvalidates(t *testing.T) {
	transcriptExec.reset()
	path := filepath.Join(t.TempDir(), "ses.jsonl")
	writeTranscript(t, path, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n")
	isSessionExecutingCached("ses", path) // miss

	// Append an assistant turn → size changes.
	writeTranscript(t, path, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n"+
		`{"type":"assistant","message":{"role":"assistant","content":"ok","stop_reason":"end_turn"}}`+"\n")
	isSessionExecutingCached("ses", path)
	if got := transcriptExec.misses.Load(); got != 2 {
		t.Fatalf("after size change misses=%d, want 2 (size change must invalidate)", got)
	}
}

// TestTranscriptExecCache_MtimeAloneForbidden proves the fingerprint does not
// rely on mtime alone: SAME size + DIFFERENT mtime must still invalidate, and
// DIFFERENT size + SAME mtime must also invalidate. Comparing mtime alone is
// forbidden because its resolution can be too coarse under rapid writes.
func TestTranscriptExecCache_MtimeAloneForbidden(t *testing.T) {
	t.Run("same size different mtime", func(t *testing.T) {
		transcriptExec.reset()
		path := filepath.Join(t.TempDir(), "ses.jsonl")
		content := `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n"
		writeTranscript(t, path, content)
		isSessionExecutingCached("ses", path) // miss

		// Same content (same size), explicitly different mtime.
		mtime := time.Now().Add(-30 * time.Second)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		isSessionExecutingCached("ses", path)
		if got := transcriptExec.misses.Load(); got != 2 {
			t.Fatalf("same-size/different-mtime misses=%d, want 2 (mtime change must invalidate even when size is identical)", got)
		}
	})

	t.Run("different size same mtime", func(t *testing.T) {
		transcriptExec.reset()
		path := filepath.Join(t.TempDir(), "ses.jsonl")
		writeTranscript(t, path, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n")
		mtime := time.Now().Add(-60 * time.Second)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		isSessionExecutingCached("ses", path) // miss

		// Change content (different size) then restore the SAME mtime.
		writeTranscript(t, path, `{"type":"user","message":{"role":"user","content":"hello there"}}`+"\n")
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		isSessionExecutingCached("ses", path)
		if got := transcriptExec.misses.Load(); got != 2 {
			t.Fatalf("different-size/same-mtime misses=%d, want 2 (size change must invalidate even when mtime is identical)", got)
		}
	})
}

// TestTranscriptExecCache_PathAndSessionKey proves different paths and different
// sessionIDs produce distinct cache entries.
func TestTranscriptExecCache_PathAndSessionKey(t *testing.T) {
	transcriptExec.reset()
	dir := t.TempDir()
	p1 := filepath.Join(dir, "ses_a.jsonl")
	p2 := filepath.Join(dir, "ses_b.jsonl")
	writeTranscript(t, p1, `{"type":"user","message":{"role":"user","content":"x"}}`+"\n")
	writeTranscript(t, p2, `{"type":"user","message":{"role":"user","content":"x"}}`+"\n")

	isSessionExecutingCached("ses_a", p1) // miss
	isSessionExecutingCached("ses_b", p2) // miss (different path+sessionID)
	if got := transcriptExec.misses.Load(); got != 2 {
		t.Fatalf("distinct keys misses=%d, want 2", got)
	}
	// Same path but different sessionID also misses (sessionID is part of the key).
	isSessionExecutingCached("ses_other", p1)
	if got := transcriptExec.misses.Load(); got != 3 {
		t.Fatalf("different sessionID same path misses=%d, want 3", got)
	}
}

// TestTranscriptExecCache_LargeKGuardrail is the cold-cache guardrail: with K
// live-PID transcripts, an unchanged fingerprint is a hit. Cold cost is bounded
// by the number of CHANGED transcripts, not by K. Changing one transcript
// re-evaluates only that one.
func TestTranscriptExecCache_LargeKGuardrail(t *testing.T) {
	transcriptExec.reset()
	dir := t.TempDir()
	const K = 5
	paths := make([]string, K)
	for i := 0; i < K; i++ {
		p := filepath.Join(dir, fmt.Sprintf("ses_%d.jsonl", i))
		writeTranscript(t, p, `{"type":"user","message":{"role":"user","content":"x"}}`+"\n")
		paths[i] = p
	}
	sid := func(i int) string { return fmt.Sprintf("s%d", i) }

	// Round 1: K cold evaluations → K misses, 0 hits.
	for i, p := range paths {
		isSessionExecutingCached(sid(i), p)
	}
	if got := transcriptExec.misses.Load(); got != int64(K) {
		t.Fatalf("round1 misses=%d, want %d", got, K)
	}

	// Round 2: all unchanged → K hits, no new misses.
	for i, p := range paths {
		isSessionExecutingCached(sid(i), p)
	}
	if got := transcriptExec.misses.Load(); got != int64(K) {
		t.Fatalf("round2 misses=%d, want %d (unchanged transcripts must hit, not reparse)", got, K)
	}
	if got := transcriptExec.hits.Load(); got != int64(K) {
		t.Fatalf("round2 hits=%d, want %d", got, K)
	}

	// Round 3: change ONE transcript → exactly 1 new miss (K-1 hits).
	writeTranscript(t, paths[2], `{"type":"user","message":{"role":"user","content":"x"}}`+"\n"+
		`{"type":"assistant","message":{"role":"assistant","content":"ok","stop_reason":"end_turn"}}`+"\n")
	for i, p := range paths {
		isSessionExecutingCached(sid(i), p)
	}
	if got := transcriptExec.misses.Load(); got != int64(K+1) {
		t.Fatalf("round3 misses=%d, want %d (only the 1 changed transcript re-evaluates, not all %d)", got, K+1, K)
	}
}
