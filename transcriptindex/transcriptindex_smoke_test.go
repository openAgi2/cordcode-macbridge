package transcriptindex

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openAgi2/cccode-macbridge/core"
)

// linesBuilder accumulates JSONL lines and records each line's start offset by
// marker, so tests can assert exact byte-span boundaries.
type linesBuilder struct {
	buf     bytes.Buffer
	offsets map[string]int64
}

func newLinesBuilder() *linesBuilder {
	return &linesBuilder{offsets: make(map[string]int64)}
}

func (l *linesBuilder) add(marker, jsonLine string) *linesBuilder {
	l.offsets[marker] = int64(l.buf.Len())
	l.buf.WriteString(jsonLine)
	l.buf.WriteByte('\n')
	return l
}

func (l *linesBuilder) end() int64 { return int64(l.buf.Len()) }

func (l *linesBuilder) write(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, l.buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func tmpFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "transcript-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

// appendLine appends one JSONL line and returns its start offset.
func appendLine(path, jsonLine string) int64 {
	before := fileSize(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := f.WriteString(jsonLine + "\n"); err != nil {
		panic(err)
	}
	return before
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return st.Size()
}

func truncateTo(path string, n int64) {
	if err := os.Truncate(path, n); err != nil {
		panic(err)
	}
}

func mustBuild(t *testing.T, backend Backend, path string) *PageIndex {
	t.Helper()
	idx, err := Build(backend, path)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return idx
}

// ---- Codex span extraction + all three closure variants ----

func TestCodexBuildClosureVariants(t *testing.T) {
	lb := newLinesBuilder().
		add("user1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"hello"}]}}`).
		add("asst", `{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"hi"}]}}`).
		add("reasoning", `{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"reasoning","text":"thinking"}}`).
		add("call1", `{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{}"}}`).
		add("out1", `{"timestamp":"2026-01-01T00:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"done","status":"completed"}}`).
		add("patch", `{"timestamp":"2026-01-01T00:00:05Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c2","input":"@@@"}}`).
		add("patchout", `{"timestamp":"2026-01-01T00:00:06Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c2","output":"ok","status":"completed"}}`).
		add("evt", `{"timestamp":"2026-01-01T00:00:07Z","type":"event_msg","payload":{"type":"event","payload":{"type":"patch_apply_end","call_id":"c2","status":"completed"}}}`).
		add("user2", `{"timestamp":"2026-01-01T00:00:08Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"again"}]}}`)
	path := lb.write(t, t.TempDir(), "codex.jsonl")

	idx := mustBuild(t, BackendCodex, path)

	if len(idx.Spans) != 3 {
		t.Fatalf("expected 3 spans (user, assistant, user), got %d: %+v", len(idx.Spans), idx.Spans)
	}
	user1, asst, user2 := idx.Spans[0], idx.Spans[1], idx.Spans[2]
	if user1.ReplayStart != lb.offsets["user1"] || user1.EndOffset != lb.offsets["asst"] {
		t.Errorf("user1 span %d-%d, want %d-%d", user1.ReplayStart, user1.EndOffset, lb.offsets["user1"], lb.offsets["asst"])
	}
	// Assistant turn spans from its first record through the last result variant
	// (patch_apply_end event), and all tool uses are closed.
	if asst.ReplayStart != lb.offsets["asst"] {
		t.Errorf("assistant ReplayStart %d, want %d", asst.ReplayStart, lb.offsets["asst"])
	}
	if asst.EndOffset != lb.offsets["user2"] {
		t.Errorf("assistant EndOffset %d, want %d (covers patch_apply_end)", asst.EndOffset, lb.offsets["user2"])
	}
	if asst.OpenToolUses != 0 {
		t.Errorf("assistant OpenToolUses %d, want 0 (function_call, apply_patch both closed)", asst.OpenToolUses)
	}
	if user2.ReplayStart != lb.offsets["user2"] {
		t.Errorf("user2 ReplayStart %d, want %d", user2.ReplayStart, lb.offsets["user2"])
	}
	if idx.ParseOffset != lb.end() {
		t.Errorf("ParseOffset %d, want %d", idx.ParseOffset, lb.end())
	}
	if len(idx.GenerationLineage) != 1 || idx.GenerationLineage[0].ParentID != "" {
		t.Errorf("fresh build should have a single root generation, got %+v", idx.GenerationLineage)
	}
}

func TestCodexOpenToolUseAtEOF(t *testing.T) {
	lb := newLinesBuilder().
		add("user", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"go"}]}}`).
		add("asst", `{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"ok"}]}}`).
		add("call", `{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{}"}}`)
	path := lb.write(t, t.TempDir(), "codex-open.jsonl")

	idx := mustBuild(t, BackendCodex, path)
	if len(idx.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(idx.Spans))
	}
	if idx.Spans[1].OpenToolUses != 1 {
		t.Errorf("unclosed function_call should leave OpenToolUses=1, got %d", idx.Spans[1].OpenToolUses)
	}
}

// ---- Codex tail-append refresh vs truncation rebuild ----

func TestCodexRefreshAppendAndTruncate(t *testing.T) {
	lb := newLinesBuilder().
		add("u1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"one"}]}}`).
		add("a1", `{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"1"}]}}`)
	path := lb.write(t, t.TempDir(), "codex-grow.jsonl")

	idx := mustBuild(t, BackendCodex, path)
	rootID := idx.PrefixGeneration
	if len(idx.Spans) != 2 {
		t.Fatalf("baseline spans %d, want 2", len(idx.Spans))
	}

	// Append a new user+assistant turn (continuous tail).
	appendLine(path, `{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"two"}]}}`)
	appendLine(path, `{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"2"}]}}`)

	refreshed, err := Refresh(idx)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(refreshed.Spans) != 4 {
		t.Fatalf("after append, spans %d, want 4: %+v", len(refreshed.Spans), refreshed.Spans)
	}
	if refreshed.ParseOffset != fileSize(path) {
		t.Errorf("ParseOffset %d, want %d", refreshed.ParseOffset, fileSize(path))
	}
	if len(refreshed.GenerationLineage) != 2 {
		t.Fatalf("append should add a child generation, got lineage %d", len(refreshed.GenerationLineage))
	}
	if refreshed.GenerationLineage[1].ParentID != rootID {
		t.Errorf("child parent %q, want root %q", refreshed.GenerationLineage[1].ParentID, rootID)
	}
	// Existing prefix ordinals are preserved.
	for i, s := range refreshed.Spans[:2] {
		if s.Ordinal != int64(i) {
			t.Errorf("prefix span %d ordinal changed to %d", i, s.Ordinal)
		}
	}

	// Truncation must trigger a fresh rebuild (new single-root lineage).
	truncateTo(path, lb.offsets["a1"])
	rebuilt, err := Refresh(refreshed)
	if err != nil {
		t.Fatalf("Refresh after truncate: %v", err)
	}
	if len(rebuilt.GenerationLineage) != 1 {
		t.Errorf("truncation should rebuild with fresh root lineage, got %d", len(rebuilt.GenerationLineage))
	}
}

// ---- Claude: tool_result extends the owning assistant span across lines ----

func TestClaudeToolResultExtendsOwner(t *testing.T) {
	lb := newLinesBuilder().
		add("asst", `{"type":"assistant","timestamp":"2026-01-01T00:00:00Z","message":{"id":"m1","role":"assistant","model":"x","content":[{"type":"text","text":"run"},{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`).
		add("result", `{"type":"user","timestamp":"2026-01-01T00:00:01Z","message":{"id":"u1","role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"out"}]}}`).
		add("user2", `{"type":"user","timestamp":"2026-01-01T00:00:02Z","message":{"id":"u2","role":"user","content":[{"type":"text","text":"next"}]}}`)
	path := lb.write(t, t.TempDir(), "claude.jsonl")

	idx := mustBuild(t, BackendClaude, path)

	// Two logical messages: assistant m1 (result attributed to it) and user u2.
	// The tool_result-only user line must NOT become its own message.
	if len(idx.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d: %+v", len(idx.Spans), idx.Spans)
	}
	asst, user2 := idx.Spans[0], idx.Spans[1]
	if asst.StableID != "m1" {
		t.Errorf("assistant StableID %q, want m1", asst.StableID)
	}
	if asst.ReplayStart != lb.offsets["asst"] {
		t.Errorf("assistant ReplayStart %d, want %d", asst.ReplayStart, lb.offsets["asst"])
	}
	// Assistant EndOffset must extend across the tool_result line.
	if asst.EndOffset != lb.offsets["user2"] {
		t.Errorf("assistant EndOffset %d, want %d (extends past tool_result)", asst.EndOffset, lb.offsets["user2"])
	}
	if asst.OpenToolUses != 0 {
		t.Errorf("assistant OpenToolUses %d, want 0", asst.OpenToolUses)
	}
	if user2.ReplayStart != lb.offsets["user2"] {
		t.Errorf("user2 ReplayStart %d, want %d", user2.ReplayStart, lb.offsets["user2"])
	}
}

// ---- Interval-union replay matches page messages positionally ----

// testReplayer re-runs the extractor over a byte range and emits one entry per
// span, tagged by local index, exercising the same boundary logic as Build so
// positional alignment holds.
func testReplayer(backend Backend) ContentReplayer {
	return func(path string, r ByteRange) ([]core.RichHistoryEntry, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err := f.Seek(r.Start, io.SeekStart); err != nil {
			return nil, err
		}
		ext := newExtractor(backend)
		if err := scanTranscript(io.LimitReader(f, r.End-r.Start), func(rec Record) error {
			ext.Process(rec)
			return nil
		}); err != nil {
			return nil, err
		}
		ext.Finish(r.End)
		spans := ext.Spans()
		out := make([]core.RichHistoryEntry, len(spans))
		for i := range spans {
			out[i] = core.RichHistoryEntry{Content: indexTag(i)}
		}
		return out, nil
	}
}

func indexTag(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	if i < len(letters) {
		return string(letters[i])
	}
	return "z"
}

func TestReplayPageUnionAndMatch(t *testing.T) {
	// Three assistant turns, each with a tool_use+result so each assistant span
	// extends across its own result line (non-overlapping, sequential).
	lb := newLinesBuilder()
	marks := []string{"u1", "a1", "r1", "u2", "a2", "r2", "u3", "a3", "r3"}
	bodies := []string{
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"id":"u1","role":"user","content":[{"type":"text","text":"q1"}]}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"id":"a1","role":"assistant","model":"x","content":[{"type":"text","text":"r1"},{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-01-01T00:00:02Z","message":{"id":"ur1","role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"o1"}]}}`,
		`{"type":"user","timestamp":"2026-01-01T00:00:03Z","message":{"id":"u2","role":"user","content":[{"type":"text","text":"q2"}]}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:04Z","message":{"id":"a2","role":"assistant","model":"x","content":[{"type":"text","text":"r2"},{"type":"tool_use","id":"t2","name":"Bash","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-01-01T00:00:05Z","message":{"id":"ur2","role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"o2"}]}}`,
		`{"type":"user","timestamp":"2026-01-01T00:00:06Z","message":{"id":"u3","role":"user","content":[{"type":"text","text":"q3"}]}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:07Z","message":{"id":"a3","role":"assistant","model":"x","content":[{"type":"text","text":"r3"},{"type":"tool_use","id":"t3","name":"Bash","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-01-01T00:00:08Z","message":{"id":"ur3","role":"user","content":[{"type":"tool_result","tool_use_id":"t3","content":"o3"}]}}`,
	}
	for i := range marks {
		lb.add(marks[i], bodies[i])
	}
	path := lb.write(t, t.TempDir(), "claude-multi.jsonl")
	idx := mustBuild(t, BackendClaude, path)

	// 6 logical messages: u1, a1, u2, a2, u3, a3.
	if len(idx.Spans) != 6 {
		t.Fatalf("expected 6 spans, got %d: %+v", len(idx.Spans), idx.Spans)
	}

	// Last page = newest message (a3, ordinal 5).
	last := LastN(idx.Spans, 1)
	union := idx.ByteUnionFor(last)
	// Union must exactly cover a3's span (no overlap with neighbors here).
	if union.Start != idx.Spans[5].ReplayStart || union.End != idx.Spans[5].EndOffset {
		t.Errorf("last-page union %d-%d, want %d-%d", union.Start, union.End, idx.Spans[5].ReplayStart, idx.Spans[5].EndOffset)
	}
	out, err := idx.Replay(last, testReplayer(BackendClaude))
	if err != nil {
		t.Fatalf("Replay last page: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Replay last page returned %d, want 1", len(out))
	}

	// Middle page = u2,a2 (ordinals 2,3). Their spans are contiguous and
	// non-overlapping with neighbors, so union == their combined range.
	mid := append([]LogicalMessageSpan(nil), idx.Spans[2], idx.Spans[3])
	midUnion := idx.ByteUnionFor(mid)
	wantStart := idx.Spans[2].ReplayStart
	wantEnd := idx.Spans[3].EndOffset
	if midUnion.Start != wantStart || midUnion.End != wantEnd {
		t.Errorf("mid-page union %d-%d, want %d-%d", midUnion.Start, midUnion.End, wantStart, wantEnd)
	}
	midOut, err := idx.Replay(mid, testReplayer(BackendClaude))
	if err != nil {
		t.Fatalf("Replay mid page: %v", err)
	}
	if len(midOut) != 2 {
		t.Fatalf("Replay mid page returned %d, want 2", len(midOut))
	}
}

// ---- Persistence round-trip + corrupt-discard ----

func TestPersistenceRoundTripAndCorrupt(t *testing.T) {
	lb := newLinesBuilder().
		add("u1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"hi"}]}}`).
		add("a1", `{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"yo"}]}}`)
	path := lb.write(t, t.TempDir(), "codex-persist.jsonl")
	dir := t.TempDir()

	idx := mustBuild(t, BackendCodex, path)
	if err := Save(dir, idx); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(dir, BackendCodex, path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil after Save")
	}
	if len(loaded.Spans) != len(idx.Spans) || loaded.ParseOffset != idx.ParseOffset {
		t.Errorf("round-trip mismatch: spans %d/%d parseOffset %d/%d", len(loaded.Spans), len(idx.Spans), loaded.ParseOffset, idx.ParseOffset)
	}
	if loaded.PrefixGeneration != idx.PrefixGeneration {
		t.Errorf("generation changed across round-trip: %q vs %q", loaded.PrefixGeneration, idx.PrefixGeneration)
	}

	// Corrupt the index file -> Load must discard (return nil) so the store rebuilds.
	p := PersistPath(dir, BackendCodex, path)
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(dir, BackendCodex, path); got != nil {
		t.Errorf("corrupt index should be discarded, got %+v", got)
	}
}

// ---- Store: per-session singleflight + budget enforcement ----

func TestStoreSingleflightAndBudget(t *testing.T) {
	lb := newLinesBuilder().
		add("u1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"hi"}]}}`)
	path := lb.write(t, t.TempDir(), "codex-store.jsonl")
	dir := t.TempDir()

	store := NewStore(dir, WithMaxConcurrentBuilds(1), WithMaxInFlightBytes(1<<20))

	var wg sync.WaitGroup
	var builds int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx, err := store.Ensure(context.Background(), IndexRequest{Backend: BackendCodex, FilePath: path})
			if err != nil {
				t.Errorf("Ensure: %v", err)
				return
			}
			if idx == nil || len(idx.Spans) != 1 {
				t.Errorf("Ensure returned idx=%+v", idx)
			}
			atomic.AddInt32(&builds, 1)
		}()
	}
	wg.Wait()

	// Concurrent Ensure for the same session must coalesce; the persisted index
	// should exist and load correctly afterward.
	loaded, _ := Load(dir, BackendCodex, path)
	if loaded == nil || len(loaded.Spans) != 1 {
		t.Errorf("post-Ensure Load got %+v", loaded)
	}
}

func TestStoreByteBudgetRejectsOversize(t *testing.T) {
	path := tmpFile(t, strings.Repeat("a", 1<<20)+`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"x"}]}}`+"\n")
	store := NewStore(t.TempDir(), WithMaxInFlightBytes(1024)) // tiny budget
	_, err := store.Ensure(context.Background(), IndexRequest{Backend: BackendCodex, FilePath: path})
	if err == nil {
		t.Fatal("expected byte-budget rejection for oversize file, got nil")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("expected budget error, got %v", err)
	}
}
