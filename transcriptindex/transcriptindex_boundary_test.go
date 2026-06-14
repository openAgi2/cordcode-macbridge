package transcriptindex

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// rewriteByte overwrites a single byte in place (same size, new mtime) to
// simulate an in-place prefix rewrite that must fail the continuity anchor.
func rewriteByte(t *testing.T, path string, offset int64, b byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{b}); err != nil {
		t.Fatal(err)
	}
}

// §6.9: "Claude pending tool result 先于 owner 被解析" — a tool_result line that
// arrives before its owning tool_use must not crash or misattribute; the owner
// closes the tool when it appears. (This order does not occur in real Claude
// transcripts; the test pins the defensive path.)
func TestClaudePendingToolResultBeforeOwner(t *testing.T) {
	lb := newLinesBuilder().
		add("result", `{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"id":"u0","role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"early"}]}}`).
		add("asst", `{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"id":"m1","role":"assistant","model":"x","content":[{"type":"text","text":"go"},{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`).
		add("user2", `{"type":"user","timestamp":"2026-01-01T00:00:02Z","message":{"id":"u2","role":"user","content":[{"type":"text","text":"next"}]}}`)
	path := lb.write(t, t.TempDir(), "claude-pending.jsonl")

	idx := mustBuild(t, BackendClaude, path)
	// Two spans: assistant m1 (tool closed via pending resolution) and user u2.
	// The early result-only line is not its own message.
	if len(idx.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d: %+v", len(idx.Spans), idx.Spans)
	}
	asst := idx.Spans[0]
	if asst.StableID != "m1" {
		t.Errorf("first span StableID %q, want m1", asst.StableID)
	}
	if asst.OpenToolUses != 0 {
		t.Errorf("pending result should close the tool_use, OpenToolUses=%d", asst.OpenToolUses)
	}
}

// §6.9: "未闭合 tool_use 跨 append 后可从正确的 ReplayStart 重建" — a Codex turn
// truncated at an open function_call must, after appending the result, refresh
// from that turn's ReplayStart and close the tool.
func TestCodexUnclosedToolClosedOnAppend(t *testing.T) {
	lb := newLinesBuilder().
		add("u1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"go"}]}}`).
		add("a1", `{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"ok"}]}}`).
		add("call", `{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{}"}}`)
	path := lb.write(t, t.TempDir(), "codex-open-append.jsonl")

	idx := mustBuild(t, BackendCodex, path)
	if idx.Spans[1].OpenToolUses != 1 {
		t.Fatalf("baseline open tool: OpenToolUses=%d, want 1", idx.Spans[1].OpenToolUses)
	}

	appendLine(path, `{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"done","status":"completed"}}`)
	refreshed, err := Refresh(idx)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	last := refreshed.Spans[len(refreshed.Spans)-1]
	if last.OpenToolUses != 0 {
		t.Errorf("after append, last span OpenToolUses=%d, want 0 (result closed it)", last.OpenToolUses)
	}
	if last.EndOffset <= idx.Spans[1].EndOffset {
		t.Errorf("last span EndOffset did not extend past the appended result")
	}
}

// §6.9: "continuity anchor 验证失败时不得把变更误判为 append" + "已索引前缀改写后
// cursor stale" — an in-place rewrite of the indexed prefix (same size) must fail
// the anchor and trigger a fresh rebuild, not a tail append.
func TestContinuityAnchorRewriteRebuild(t *testing.T) {
	lb := newLinesBuilder().
		add("u1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"hello world"}]}}`).
		add("a1", `{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"reply"}]}}`)
	path := lb.write(t, t.TempDir(), "codex-rewrite.jsonl")

	idx := mustBuild(t, BackendCodex, path)
	if len(idx.GenerationLineage) != 1 {
		t.Fatalf("baseline lineage %d, want 1", len(idx.GenerationLineage))
	}

	// Rewrite a byte inside the indexed prefix without changing size.
	rewriteByte(t, path, 16, '9')
	refreshed, err := Refresh(idx)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(refreshed.GenerationLineage) != 1 {
		t.Errorf("in-place rewrite should trigger fresh rebuild (root lineage), got lineage %d", len(refreshed.GenerationLineage))
	}
}

// §6.9: "continuity anchor 算法或窗口版本变化时旧索引安全失效" — a version bump
// (the proxy for an algorithm/format change) must cause Load to discard the
// persisted index so the store rebuilds from the transcript.
func TestVersionMismatchDiscarded(t *testing.T) {
	lb := newLinesBuilder().
		add("u1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"hi"}]}}`)
	path := lb.write(t, t.TempDir(), "codex-version.jsonl")
	dir := t.TempDir()

	idx := mustBuild(t, BackendCodex, path)
	if err := Save(dir, idx); err != nil {
		t.Fatal(err)
	}
	// Simulate a future-incompatible index by bumping the persisted version.
	p := PersistPath(dir, BackendCodex, path)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	bumped := []byte(fmt.Sprintf(`{"version":%d`, IndexVersion+999))
	copy(data[:len(bumped)], bumped) // overwrite the leading version field
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(dir, BackendCodex, path); got != nil {
		t.Errorf("version-incompatible index should be discarded, got version %d", got.Version)
	}
}

func TestExtractorRevisionMismatchDiscardedAndRebuilt(t *testing.T) {
	lb := newLinesBuilder().
		add("u1", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"hi"}]}}`)
	path := lb.write(t, t.TempDir(), "codex-extractor-version.jsonl")
	dir := t.TempDir()

	idx := mustBuild(t, BackendCodex, path)
	if err := Save(dir, idx); err != nil {
		t.Fatal(err)
	}

	p := PersistPath(dir, BackendCodex, path)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var persisted PageIndex
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	persisted.ExtractorRevision = "codex-stale"
	data, err = json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(dir, BackendCodex, path); got != nil {
		t.Errorf("extractor-incompatible index should be discarded, got revision %q", got.ExtractorRevision)
	}

	rebuilt, err := Refresh(&persisted)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.ExtractorRevision != extractorRevision(BackendCodex) {
		t.Errorf("Refresh revision = %q, want %q", rebuilt.ExtractorRevision, extractorRevision(BackendCodex))
	}
}

// §6.9: "lineage 折叠后 root checkpoint" — many appends must keep the persisted
// lineage bounded at maxLineageDepth and well-formed.
func TestLineageFolding(t *testing.T) {
	lb := newLinesBuilder().
		add("u0", `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"seed"}]}}`)
	path := lb.write(t, t.TempDir(), "codex-fold.jsonl")

	idx := mustBuild(t, BackendCodex, path)
	// Far more appends than maxLineageDepth.
	for i := 0; i < maxLineageDepth+10; i++ {
		appendLine(path, fmt.Sprintf(`{"timestamp":"2026-01-01T00:00:%02dZ","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"q%d"}]}}`, i%60, i))
		next, err := Refresh(idx)
		if err != nil {
			t.Fatalf("Refresh %d: %v", i, err)
		}
		idx = next
	}
	if len(idx.GenerationLineage) != maxLineageDepth {
		t.Errorf("lineage length %d, want %d after folding", len(idx.GenerationLineage), maxLineageDepth)
	}
	if !validateLineage(idx.GenerationLineage) {
		t.Errorf("folded lineage is not well-formed: %+v", idx.GenerationLineage)
	}
}

// §6.9: "page span 重叠时按区间并集 replay, tool result 不丢失" — when a Claude
// tool_result lands far downstream, its owner's span overlaps later messages;
// the byte union must expand to whole-message boundaries and replay must still
// pick exactly the requested page.
func TestReplayOverlapExpands(t *testing.T) {
	lb := newLinesBuilder().
		add("m1", `{"type":"assistant","timestamp":"2026-01-01T00:00:00Z","message":{"id":"m1","role":"assistant","model":"x","content":[{"type":"text","text":"run"},{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`).
		add("u2", `{"type":"user","timestamp":"2026-01-01T00:00:01Z","message":{"id":"u2","role":"user","content":[{"type":"text","text":"q2"}]}}`).
		add("a3", `{"type":"assistant","timestamp":"2026-01-01T00:00:02Z","message":{"id":"a3","role":"assistant","model":"x","content":[{"type":"text","text":"a3"}]}}`).
		add("r1", `{"type":"user","timestamp":"2026-01-01T00:00:03Z","message":{"id":"ur1","role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"late-result"}]}}`)
	path := lb.write(t, t.TempDir(), "claude-overlap.jsonl")
	idx := mustBuild(t, BackendClaude, path)

	// m1's tool_result is at r1, so m1's span [m1.start, r1.end] contains u2 and a3.
	m1, u2, a3 := idx.Spans[0], idx.Spans[1], idx.Spans[2]
	if m1.EndOffset < a3.EndOffset {
		t.Errorf("m1 EndOffset %d should extend past a3 EndOffset %d", m1.EndOffset, a3.EndOffset)
	}

	// Page = {u2, a3}: their naive union is inside m1's span, so the fixpoint
	// expansion must pull the union back to m1.start (whole-message boundary).
	page := []LogicalMessageSpan{u2, a3}
	union := idx.ByteUnionFor(page)
	if union.Start != m1.ReplayStart {
		t.Errorf("overlap union start %d, want m1 ReplayStart %d", union.Start, m1.ReplayStart)
	}
	out, err := idx.Replay(page, testReplayer(BackendClaude))
	if err != nil {
		t.Fatalf("Replay overlap page: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("overlap page replay returned %d, want 2", len(out))
	}
}

// §6.9: "500+ 逻辑消息首次只解码一页正文" + "response bytes 和峰值内存受限" —
// for a large transcript, a single page's byte union must be a small fraction of
// the whole file, so only that range is decoded.
func TestLargeSessionOnePageBoundedBytes(t *testing.T) {
	const turns = 300 // 600 logical messages (user + assistant each turn)
	lb := newLinesBuilder()
	for i := 0; i < turns; i++ {
		lb.add(fmt.Sprintf("u%d", i), fmt.Sprintf(`{"timestamp":"2026-01-01T00:%02d:%02dZ","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"question number %d"}]}}`, (i/60)%60, i%60, i))
		lb.add(fmt.Sprintf("a%d", i), fmt.Sprintf(`{"timestamp":"2026-01-01T00:%02d:%02dZ","type":"response_item","payload":{"role":"assistant","type":"message","content":[{"type":"output_text","text":"answer number %d"}]}}`, (i/60)%60+1, i%60, i))
	}
	path := lb.write(t, t.TempDir(), "codex-large.jsonl")
	idx := mustBuild(t, BackendCodex, path)

	if len(idx.Spans) != 2*turns {
		t.Fatalf("expected %d spans, got %d", 2*turns, len(idx.Spans))
	}

	page := LastN(idx.Spans, 50)
	union := idx.ByteUnionFor(page)
	full := lb.end()
	if union.End-union.Start >= full {
		t.Errorf("page union %d bytes should be far smaller than full file %d", union.End-union.Start, full)
	}
	if union.End-union.Start > full/4 {
		t.Errorf("page union %d is more than a quarter of the file %d; expected a small page", union.End-union.Start, full)
	}
	out, err := idx.Replay(page, testReplayer(BackendCodex))
	if err != nil {
		t.Fatalf("Replay large page: %v", err)
	}
	if len(out) != 50 {
		t.Errorf("large page replay returned %d, want 50", len(out))
	}
}
