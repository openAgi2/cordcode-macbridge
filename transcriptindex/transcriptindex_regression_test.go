package transcriptindex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// largestCodexSession walks ~/.codex/sessions and returns the biggest .jsonl
// transcript (the kind of session that triggered close 1009). Skips when none is
// large enough to be interesting.
func largestCodexSession(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir := filepath.Join(home, ".codex", "sessions")
	var best string
	var bestSize int64
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info.Size() > bestSize {
			bestSize = info.Size()
			best = path
		}
		return nil
	})
	if best == "" || bestSize < 1<<20 {
		t.Skip("no codex session >= 1MB found under ~/.codex/sessions")
	}
	return best
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTranscriptIndexLargeSessionRegression is the Phase 2 transcript-index
// regression (design 6.3/6.4/9.2). Env-gated like the Phase 0 baseline so it
// only runs against real local transcripts. It records page_replay_bytes and
// worst-page rebuild time for the largest Codex session — the numbers that
// decide page sizing for the close-1009 fix and whether finer-grained
// checkpoints are needed.
func TestTranscriptIndexLargeSessionRegression(t *testing.T) {
	if os.Getenv("CCCODE_SESSION_LOADING_BASELINE") != "1" {
		t.Skip("set CCCODE_SESSION_LOADING_BASELINE=1 to run against real local transcripts")
	}
	path := largestCodexSession(t)
	rep := testReplayer(BackendCodex)

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	buildStart := time.Now()
	idx, err := Build(BackendCodex, path)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	buildTime := time.Since(buildStart)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	fullSize := fileSize(path)
	spanCount := len(idx.Spans)

	// Measure worst-case page_replay_bytes across sliding windows for several
	// page sizes. This is the raw transcript byte range a page must re-parse.
	type pageSizeResult struct {
		Size             int
		WorstReplayBytes int64
		WorstRebuildMs   int64
		FirstReplayBytes int64
	}
	pageSizes := []int{10, 20, 50}
	results := make([]pageSizeResult, 0, len(pageSizes))
	for _, ps := range pageSizes {
		if ps > spanCount {
			ps = spanCount
		}
		if ps <= 0 {
			continue
		}
		worst := idx.ByteUnionFor(idx.Spans[0:ps])
		for start := 0; start+ps <= spanCount; start++ {
			u := idx.ByteUnionFor(idx.Spans[start : start+ps])
			if u.End-u.Start > worst.End-worst.Start {
				worst = u
			}
		}
		rebuildStart := time.Now()
		out, rerr := rep(path, worst)
		if rerr != nil {
			t.Fatalf("worst-page rebuild (size %d): %v", ps, rerr)
		}
		worstRebuild := time.Since(rebuildStart)
		first := idx.ByteUnionFor(LastN(idx.Spans, ps))
		results = append(results, pageSizeResult{
			Size: ps, WorstReplayBytes: worst.End - worst.Start,
			WorstRebuildMs: worstRebuild.Milliseconds(), FirstReplayBytes: first.End - first.Start,
		})
		if len(out) != ps {
			t.Errorf("page size %d: worst-page rebuild returned %d entries, want %d", ps, len(out), ps)
		}
	}

	// Append/refresh timing on a copy (never mutate the real session file).
	copyPath := filepath.Join(t.TempDir(), "copy.jsonl")
	copyFile(t, path, copyPath)
	copyIdx, err := Build(BackendCodex, copyPath)
	if err != nil {
		t.Fatalf("Build copy: %v", err)
	}
	appendLine(copyPath, `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"regression-append"}]}}`)
	refreshStart := time.Now()
	refreshed, err := Refresh(copyIdx)
	refreshTime := time.Since(refreshStart)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Sanity: a page's byte union must never exceed the full file, and the
	// smallest page size must bound the worst page below the full file when the
	// session has more messages than the page.
	if len(results) > 0 {
		worst := results[0].WorstReplayBytes
		if worst > fullSize {
			t.Errorf("worst page_replay_bytes %d > full file %d", worst, fullSize)
		}
		if spanCount > results[0].Size && worst >= fullSize {
			t.Errorf("page size %d did not bound worst page below the full file (spans=%d)", results[0].Size, spanCount)
		}
	}
	if len(refreshed.Spans) != spanCount+1 {
		t.Errorf("after append, spans %d, want %d", len(refreshed.Spans), spanCount+1)
	}

	rec := map[string]any{
		"record_type":             "transcript_index_regression",
		"backend":                 "codex",
		"file":                    path,
		"file_bytes":              fullSize,
		"span_count":              spanCount,
		"index_build_ms":          buildTime.Milliseconds(),
		"index_build_alloc_bytes": int64(after.TotalAlloc - before.TotalAlloc),
		"refresh_ms":              refreshTime.Milliseconds(),
		"page_results":            results,
	}
	payload, _ := json.Marshal(rec)
	t.Logf("transcript-index regression: %s", string(payload))

	if out := os.Getenv("CCCODE_TRANSCRIPT_INDEX_REGRESSION_OUTPUT"); out != "" {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(f, string(payload))
		f.Close()
	}
}
