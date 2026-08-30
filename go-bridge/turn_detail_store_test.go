package gobridge

// F2/F2.1 Mac detail store tests against the FROZEN model (§11.8 detail
// store 事务模型 + owner F2.1 review 2026-08-30): sha256 path safety,
// fixed-commit-order page accept on the ORDERED entry model (P0-1), slim
// oversize tool cards with metadata retention (P0-2), strict startup
// recovery — committed-range corruption quarantines, only a final torn tail
// rolls back (P0-3) — deterministic chunkSeq/splitter records, immutable
// blob handles (P1-5), whole-dir budget eviction.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newStore(t *testing.T) (*TurnDetailStore, string) {
	t.Helper()
	root := t.TempDir()
	return NewTurnDetailStore(filepath.Join(root, "detail")), filepath.Join(root, "detail")
}

func inlinePart(id, text string) ProjectionPart {
	return ProjectionPart{Type: "text", Text: text, ItemID: id}
}

// slimCmdCard is the F2.1 P0-2 shape: a tool card whose metadata (command,
// status, title) is intact and whose huge output is stripped (no ToolResult,
// no giant Text).
func slimCmdCard(id string) ProjectionPart {
	return ProjectionPart{
		Type: "tool", ItemID: id, ToolName: "shell",
		ToolInput:  map[string]any{"command": "cargo test --workspace"},
		ToolStatus: "completed", Title: "workspace test run",
	}
}

func inlineEntry(id, text string) DetailPageEntry {
	part := inlinePart(id, text)
	return DetailPageEntry{ItemID: id, Inline: &part}
}

func oversizeEntry(id, content string) DetailPageEntry {
	card := slimCmdCard(id)
	preview := content
	if len(preview) > 64 {
		preview = preview[:64]
	}
	return DetailPageEntry{ItemID: id, Oversize: &DetailOversizeStaged{Part: card, Preview: preview, Content: content}}
}

// flattenChunkItems returns every chunk's items concatenated in chunk order —
// the wire order a client would render.
func flattenChunkItems(chunks []DetailChunkContent) []string {
	var ids []string
	for _, c := range chunks {
		for _, item := range c.Items {
			ids = append(ids, item.ItemID)
		}
	}
	return ids
}

func TestDetailStorePathsAreHashed(t *testing.T) {
	store, root := newStore(t)
	// Even when IDs contain separators/NULs, the on-disk path must stay inside
	// root and use hex segments only (ids are hash inputs, never path segments).
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "../../etc/passwd\x00", TurnID: "x/y",
		Generation: 0, Page: 1, NextCursor: "c1",
		Entries: []DetailPageEntry{inlineEntry("i1", "hello")},
	}); err != nil {
		t.Fatalf("traversal-looking ids must be hashed, not rejected: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "codex-remote"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(entries[0].Name()) != 64 || !isHex(entries[0].Name()) {
		t.Fatalf("session segment must be 64-char sha256 hex, got %v", entries)
	}
	turnEntries, _ := os.ReadDir(filepath.Join(root, "codex-remote", entries[0].Name()))
	if len(turnEntries) != 1 || !isHex(turnEntries[0].Name()) {
		t.Fatalf("turn segment must be sha256 hex, got %v", turnEntries)
	}
	// Raw IDs live in the manifest for audit, never in the path.
	manifest, err := store.LoadManifest("codex-remote", "../../etc/passwd\x00", "x/y")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SessionID != "../../etc/passwd\x00" || manifest.TurnID != "x/y" {
		t.Fatalf("manifest keeps raw ids for audit: %+v", manifest)
	}
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return len(s) > 0
}

func TestDetailStoreAcceptPageCommitsInOrder(t *testing.T) {
	store, _ := newStore(t)
	p1, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s1", TurnID: "t1",
		Generation: 2, Page: 1, NextCursor: "cur-1",
		Entries: []DetailPageEntry{inlineEntry("i1", "one"), inlineEntry("i2", "two")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p1.ManifestRev != 1 || p1.ChunkSeqFirst != 1 || p1.ChunkSeqLast != 1 {
		t.Fatalf("page1 = rev %d seq %d-%d, want 1 / 1-1", p1.ManifestRev, p1.ChunkSeqFirst, p1.ChunkSeqLast)
	}
	m := p1.Manifest
	if m.Resume.Pages != 1 || m.Resume.AcceptedCount != 2 || m.Resume.NextCursor != "cur-1" || m.Resume.LastAcceptedItemID != "i2" {
		t.Fatalf("resume after p1 = %+v", m.Resume)
	}
	if m.ItemCount != 2 || m.TotalBytes != partSize(inlinePart("i1", "one"))+partSize(inlinePart("i2", "two")) {
		t.Fatalf("summary after p1 = %d/%d", m.ItemCount, m.TotalBytes)
	}

	p2, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s1", TurnID: "t1",
		Generation: 2, Page: 2, NextCursor: "cur-2",
		Entries: []DetailPageEntry{inlineEntry("i3", "three")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p2.ManifestRev != 2 || p2.ChunkSeqFirst != 2 || p2.ChunkSeqLast != 2 {
		t.Fatalf("page2 = rev %d seq %d-%d", p2.ManifestRev, p2.ChunkSeqFirst, p2.ChunkSeqLast)
	}

	// P0-1 boundary: a page whose LAST entry is oversize must still stamp that
	// entry's id as the accepted boundary (the old two-array model dropped it).
	p3, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s1", TurnID: "t1",
		Generation: 2, Page: 3, NextCursor: "", EOF: true,
		Entries: []DetailPageEntry{
			inlineEntry("i4", "four"),
			oversizeEntry("cmd-9", strings.Repeat("z", 300*1024)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p3.Manifest.Resume.LastAcceptedItemID != "cmd-9" {
		t.Fatalf("oversize-last page boundary = %q, want cmd-9", p3.Manifest.Resume.LastAcceptedItemID)
	}
	m3, _ := store.LoadManifest("codex-remote", "s1", "t1")
	if m3.Resume.EOF != true || m3.Resume.Pages != 3 || m3.ItemCount != 5 || m3.ChunkSeqNext != p3.ChunkSeqLast+1 {
		t.Fatalf("final manifest = %+v", m3)
	}
	for i, want := range []string{"i1", "i2", "i3", "i4", "cmd-9"} {
		if m3.Items[i].ItemID != want {
			t.Fatalf("manifest items out of order: [%d] = %q, want %q", i, m3.Items[i].ItemID, want)
		}
	}
	if m3.Items[4].BlobHandle == "" || m3.Items[4].Type != "tool" {
		t.Fatalf("oversize summary = %+v", m3.Items[4])
	}

	// Fences: page skip, generation mismatch, duplicate item (cross-page and
	// within-page).
	if _, err := store.AcceptPage(DetailPageAccept{BackendID: "codex-remote", SessionID: "s1", TurnID: "t1", Generation: 2, Page: 6, Entries: []DetailPageEntry{inlineEntry("i9", "x")}}); !errors.Is(err, ErrDetailPageOrder) {
		t.Fatalf("page skip err = %v", err)
	}
	if _, err := store.AcceptPage(DetailPageAccept{BackendID: "codex-remote", SessionID: "s1", TurnID: "t1", Generation: 3, Page: 4, Entries: []DetailPageEntry{inlineEntry("i9", "x")}}); !errors.Is(err, ErrDetailGeneration) {
		t.Fatalf("generation err = %v", err)
	}
	if _, err := store.AcceptPage(DetailPageAccept{BackendID: "codex-remote", SessionID: "s1", TurnID: "t1", Generation: 2, Page: 4, Entries: []DetailPageEntry{inlineEntry("i1", "dup")}}); !errors.Is(err, ErrDetailDuplicateItem) {
		t.Fatalf("duplicate err = %v", err)
	}
	if _, err := store.AcceptPage(DetailPageAccept{BackendID: "codex-remote", SessionID: "s1", TurnID: "t1", Generation: 2, Page: 4,
		Entries: []DetailPageEntry{inlineEntry("i5", "a"), inlineEntry("i5", "b")}}); !errors.Is(err, ErrDetailDuplicateItem) {
		t.Fatalf("within-page duplicate err = %v", err)
	}
}

func TestDetailStoreEntryValidation(t *testing.T) {
	store, _ := newStore(t)
	base := DetailPageAccept{BackendID: "codex-remote", SessionID: "s", TurnID: "t", Generation: 0, Page: 1}

	// Exactly one of Inline/Oversize must be set.
	both := base
	part := inlinePart("i1", "x")
	both.Entries = []DetailPageEntry{{ItemID: "i1", Inline: &part, Oversize: &DetailOversizeStaged{Part: part, Content: "c"}}}
	if _, err := store.AcceptPage(both); !errors.Is(err, ErrDetailBadID) {
		t.Fatalf("both-set err = %v", err)
	}
	neither := base
	neither.Entries = []DetailPageEntry{{ItemID: "i1"}}
	if _, err := store.AcceptPage(neither); !errors.Is(err, ErrDetailBadID) {
		t.Fatalf("neither-set err = %v", err)
	}

	// Inline threshold: a plain item beyond the blob threshold must be staged
	// as oversize by the batch engine, not silently stored inline.
	over := base
	over.Entries = []DetailPageEntry{inlineEntry("huge", strings.Repeat("x", int(core.TurnDetailBlobThresholdBytes)+1))}
	if _, err := store.AcceptPage(over); !errors.Is(err, ErrDetailInlineOversize) {
		t.Fatalf("oversize inline err = %v", err)
	}

	// P0-2: the oversize entry's card must stay SLIM — a card still carrying
	// the output exceeds the threshold and is rejected.
	fatCard := base
	fat := slimCmdCard("cmd-1")
	fat.ToolResult = strings.Repeat("R", int(core.TurnDetailBlobThresholdBytes)+1)
	fatCard.Entries = []DetailPageEntry{{ItemID: "cmd-1", Oversize: &DetailOversizeStaged{Part: fat, Content: "c"}}}
	if _, err := store.AcceptPage(fatCard); !errors.Is(err, ErrDetailInlineOversize) {
		t.Fatalf("fat card err = %v", err)
	}

	// Card id must match the entry id; blob content must be valid UTF-8.
	mismatch := base
	card := slimCmdCard("other")
	mismatch.Entries = []DetailPageEntry{{ItemID: "cmd-1", Oversize: &DetailOversizeStaged{Part: card, Content: "c"}}}
	if _, err := store.AcceptPage(mismatch); !errors.Is(err, ErrDetailBadID) {
		t.Fatalf("card id mismatch err = %v", err)
	}
	badUTF8 := base
	badUTF8.Entries = []DetailPageEntry{{ItemID: "cmd-1", Oversize: &DetailOversizeStaged{Part: slimCmdCard("cmd-1"), Content: "a\xffb"}}}
	if _, err := store.AcceptPage(badUTF8); !errors.Is(err, ErrDetailBadID) {
		t.Fatalf("invalid utf-8 err = %v", err)
	}
}

// P0-1: an interleaved official page (reasoning → huge command → fileChange)
// must keep its exact order in the manifest, the transaction record, and the
// packed chunk contents — never "inline items first, oversize after".
func TestDetailStorePreservesOfficialPageOrder(t *testing.T) {
	store, _ := newStore(t)
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 1, NextCursor: "c1", EOF: true,
		Entries: []DetailPageEntry{
			{ItemID: "r1", Inline: &ProjectionPart{Type: "reasoning", ItemID: "r1", Text: strings.Repeat("思", 7*1024)}},
			oversizeEntry("cmd-1", strings.Repeat("输出\n", 60*1024)),
			{ItemID: "f1", Inline: &ProjectionPart{Type: "file", ItemID: "f1", Text: "changed files"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	m, _ := store.LoadManifest("codex-remote", "s", "t")
	for i, want := range []struct{ id, typ string }{{"r1", "reasoning"}, {"cmd-1", "tool"}, {"f1", "file"}} {
		if m.Items[i].ItemID != want.id || m.Items[i].Type != want.typ {
			t.Fatalf("manifest order[%d] = %+v, want %s/%s", i, m.Items[i], want.id, want.typ)
		}
	}

	records, err := store.ReadRecords("codex-remote", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || len(records[0].Entries) != 3 {
		t.Fatalf("record entries = %+v", records)
	}
	for i, want := range []string{"r1", "cmd-1", "f1"} {
		if records[0].Entries[i].ItemID != want {
			t.Fatalf("record order[%d] = %q, want %q", i, records[0].Entries[i].ItemID, want)
		}
	}

	entries := make([]DetailChunkEntry, 0, 3)
	for _, e := range records[0].Entries {
		entries = append(entries, DetailChunkEntry{Part: e.Part, Ref: e.Ref})
	}
	chunks, err := SplitDetailChunks(entries)
	if err != nil {
		t.Fatal(err)
	}
	got := flattenChunkItems(chunks)
	if len(got) != 3 || got[0] != "r1" || got[1] != "cmd-1" || got[2] != "f1" {
		t.Fatalf("chunk item order = %v, want [r1 cmd-1 f1]", got)
	}
	// P0-2/P0-1: the oversize ref rides the SAME chunk as its slim card.
	for _, c := range chunks {
		cardIn := false
		for _, item := range c.Items {
			if item.ItemID == "cmd-1" {
				cardIn = true
			}
		}
		refIn := false
		for _, ref := range c.Oversize {
			if ref.ItemID == "cmd-1" {
				refIn = true
			}
		}
		if cardIn != refIn {
			t.Fatalf("slim card and its ref must share a chunk: %+v", c)
		}
	}
}

// P0-2: the committed slim card keeps command metadata (ToolName/ToolInput/
// ToolStatus/Title) with the output stripped; the ref describes the blob
// accurately. P1-5: handles are immutable — generation and content hash are
// part of the identity.
func TestDetailStoreOversizeCardMetadataAndHandles(t *testing.T) {
	store, root := newStore(t)
	content := strings.Repeat("输出行 a moderately long line of text\n", 33*1024) // ~1.05MB
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 1, Page: 1, NextCursor: "", EOF: true,
		Entries: []DetailPageEntry{oversizeEntry("cmd-1", content)},
	}); err != nil {
		t.Fatal(err)
	}

	records, _ := store.ReadRecords("codex-remote", "s", "t")
	card := records[0].Entries[0].Part
	if card.ToolName != "shell" || card.ToolStatus != "completed" || card.Title != "workspace test run" {
		t.Fatalf("slim card lost tool metadata: %+v", card)
	}
	if card.ToolInput == nil {
		t.Fatal("slim card must keep ToolInput (command)")
	}
	if card.ToolResult != nil {
		t.Fatal("slim card must strip ToolResult into the blob")
	}
	ref := records[0].Entries[0].Ref
	if ref == nil {
		t.Fatal("oversize entry must carry a ref")
	}
	if ref.TotalBytes != int64(len(content)) || ref.TotalChunks != DetailChunkCount(content) {
		t.Fatalf("ref = %d bytes / %d chunks, want %d / %d", ref.TotalBytes, ref.TotalChunks, len(content), DetailChunkCount(content))
	}
	if len(ref.Preview) > core.TurnDetailBlobPreviewBytes || !strings.HasPrefix(content, ref.Preview) {
		t.Fatalf("preview must be a rune-aligned truncated prefix: %d bytes, prefix=%v", len(ref.Preview), strings.HasPrefix(content, ref.Preview))
	}

	// P1-5: handle identity dims — generation and content hash included, so
	// blobs are immutable and stale handles can never overwrite newer truth.
	if blobHandle("b", "s", "t", 1, "i", "x") == blobHandle("b", "s", "t", 2, "i", "x") {
		t.Fatal("handle must bind generation")
	}
	if blobHandle("b", "s", "t", 1, "i", "x") == blobHandle("b", "s", "t", 1, "i", "y") {
		t.Fatal("handle must bind content hash")
	}
	// Same itemId + same content in TWO different turns: distinct handles,
	// both blobs on disk, each reads back its own content.
	for _, turn := range []string{"t1", "t2"} {
		if _, err := store.AcceptPage(DetailPageAccept{
			BackendID: "codex-remote", SessionID: "s", TurnID: turn,
			Generation: 0, Page: 1, NextCursor: "", EOF: true,
			Entries: []DetailPageEntry{oversizeEntry("cmd-same", "shared payload\n")},
		}); err != nil {
			t.Fatal(err)
		}
	}
	m1, _ := store.LoadManifest("codex-remote", "s", "t1")
	m2, _ := store.LoadManifest("codex-remote", "s", "t2")
	h1, h2 := m1.Items[0].BlobHandle, m2.Items[0].BlobHandle
	if h1 == h2 {
		t.Fatal("same item id in different turns must not share a blob handle")
	}
	for _, h := range []string{h1, h2} {
		if _, err := os.Stat(filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t1"), "blobs", h+".bin")); err == nil && h == h2 {
			t.Fatal("t2 blob must not live in t1's dir")
		}
	}
	d1, _, _, err := store.ReadBlobChunk("codex-remote", "s", "t1", h1, 0)
	if err != nil || d1 != "shared payload\n" {
		t.Fatalf("t1 chunk = %q, %v", d1, err)
	}
	d2, _, _, err := store.ReadBlobChunk("codex-remote", "s", "t2", h2, 0)
	if err != nil || d2 != "shared payload\n" {
		t.Fatalf("t2 chunk = %q, %v", d2, err)
	}
}

func TestDetailStoreOversizeBlobLifecycle(t *testing.T) {
	store, root := newStore(t)
	content := strings.Repeat("输出行 a moderately long line of text\n", 33*1024) // ~1.05MB
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 1, Page: 1, NextCursor: "c", EOF: true,
		Entries: []DetailPageEntry{
			inlineEntry("i1", "small"),
			oversizeEntry("cmd-1", content),
		},
	}); err != nil {
		t.Fatal(err)
	}
	m, _ := store.LoadManifest("codex-remote", "s", "t")
	handle := m.Items[1].BlobHandle

	// Blob file exists under blobs/<handle>.bin inside the hashed dir.
	blobPath := filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t"), "blobs", handle+".bin")
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("blob file: %v", err)
	}
	if m.Items[1].Bytes != int64(len(content)) {
		t.Fatalf("manifest oversize summary = %+v", m.Items[1])
	}
	// The offset table is PERSISTED at accept time (P1-3): chunk reads reuse
	// it instead of rescanning content.
	want := DetailChunkOffsets(content)
	if len(m.Items[1].BlobOffsets) != len(want) {
		t.Fatalf("persisted offsets = %v, want the accept-time table (%d entries)", m.Items[1].BlobOffsets, len(want))
	}
	for i := range want {
		if m.Items[1].BlobOffsets[i] != want[i] {
			t.Fatalf("persisted offsets differ at %d: %v", i, m.Items[1].BlobOffsets)
		}
	}

	// Chunk reads: deterministic, rune-safe, cover the full content.
	total := DetailChunkCount(content)
	var rebuilt strings.Builder
	for i := 0; i < total; i++ {
		data, chunks, totalBytes, err := store.ReadBlobChunk("codex-remote", "s", "t", handle, i)
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if chunks != total || totalBytes != int64(len(content)) {
			t.Fatalf("chunk %d meta = %d/%d", i, chunks, totalBytes)
		}
		rebuilt.WriteString(data)
	}
	if rebuilt.String() != content {
		t.Fatal("chunk union must reconstruct blob content")
	}
	if _, _, _, err := store.ReadBlobChunk("codex-remote", "s", "t", handle, total); !errors.Is(err, ErrDetailChunkIndex) {
		t.Fatalf("out-of-range chunk err = %v", err)
	}
	if _, _, _, err := store.ReadBlobChunk("codex-remote", "s", "t", "not-a-handle", 0); !errors.Is(err, ErrDetailBlobUnref) {
		t.Fatalf("unreferenced handle err = %v", err)
	}
}

func TestDetailStoreReplayMatchesAccept(t *testing.T) {
	store, _ := newStore(t)
	// Page 1: two chunks' worth of items; page 2: one small item.
	var entries []DetailPageEntry
	for i := 0; i < 40; i++ {
		entries = append(entries, inlineEntry(string(rune('a'+i%26))+string(rune('0'+i/26)), strings.Repeat("m", 20*1024)))
	}
	p1, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 1, NextCursor: "c1", Entries: entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 2, NextCursor: "", EOF: true,
		Entries: []DetailPageEntry{inlineEntry("zz", "tail")},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, err := store.ReadRecords("codex-remote", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	// Re-split each record: the deterministic splitter must reproduce the
	// recorded chunkSeq span exactly (fast-path replay guarantee).
	for _, rec := range records {
		pack := make([]DetailChunkEntry, 0, len(rec.Entries))
		for _, e := range rec.Entries {
			pack = append(pack, DetailChunkEntry{Part: e.Part, Ref: e.Ref})
		}
		rebuilt, err := SplitDetailChunks(pack)
		if err != nil {
			t.Fatal(err)
		}
		span := len(rebuilt)
		if span == 0 {
			if rec.ChunkSeqFirst != 0 || rec.ChunkSeqLast != 0 {
				t.Fatalf("empty page must record 0-0, got %d-%d", rec.ChunkSeqFirst, rec.ChunkSeqLast)
			}
			continue
		}
		if rec.ChunkSeqLast-rec.ChunkSeqFirst+1 != span {
			t.Fatalf("record tx%d seq %d-%d but re-split gives %d chunks", rec.Tx, rec.ChunkSeqFirst, rec.ChunkSeqLast, span)
		}
	}
	if records[0].ChunkSeqFirst != p1.ChunkSeqFirst || records[1].ChunkSeqLast != p2.ChunkSeqLast {
		t.Fatal("replay spans must equal accept-time assignment")
	}
}

// Crash between steps: blob + items record on disk, manifest never advanced.
// SweepUncommitted must roll the log back and delete the orphan blob.
func TestDetailStoreSweepRollsBackUncommitted(t *testing.T) {
	store, root := newStore(t)
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 1, NextCursor: "c1",
		Entries: []DetailPageEntry{
			inlineEntry("i1", "committed"),
			oversizeEntry("b1", strings.Repeat("z", 300*1024)),
		},
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t"))
	m, _ := store.LoadManifest("codex-remote", "s", "t")
	committedHandle := m.Items[1].BlobHandle

	// Simulate a crash after steps 1+2 of page 2: blob renamed, log appended,
	// manifest still at TxApplied=1.
	orphanHandle := blobHandle("codex-remote", "s", "t", 0, "orphan", "orphan")
	crashBlob := filepath.Join(dir, "blobs", orphanHandle+".bin")
	if err := os.WriteFile(crashBlob, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	tornRecord := detailTxRecord{Tx: 2, Page: 2, NextCursor: "c2", ChunkSeqFirst: 2, ChunkSeqLast: 2,
		Entries: []detailStoredEntry{{ItemID: "i2", Part: inlinePart("i2", "torn")}}}
	line, err := json.Marshal(tornRecord)
	if err != nil {
		t.Fatal(err)
	}
	logF, err := os.OpenFile(filepath.Join(dir, "items.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logF.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	logF.Close()

	if err := store.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	records, err := store.ReadRecords("codex-remote", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Tx != 1 {
		t.Fatalf("sweep must keep only committed tx1, got %+v", records)
	}
	if _, err := os.Stat(crashBlob); !os.IsNotExist(err) {
		t.Fatal("orphan blob must be deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", committedHandle+".bin")); err != nil {
		t.Fatalf("committed blob must survive: %v", err)
	}
	// Duplicate accept of the "lost" page now succeeds (id space clean).
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 2, NextCursor: "c2",
		Entries: []DetailPageEntry{inlineEntry("i2", "retried")},
	}); err != nil {
		t.Fatalf("re-accept after sweep: %v", err)
	}

	// A torn FINAL append (crash mid-write of tx3) is legal and rolls back.
	logF2, err := os.OpenFile(filepath.Join(dir, "items.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logF2.WriteString(`{"tx":3,"pag`); err != nil {
		t.Fatal(err)
	}
	logF2.Close()
	if err := store.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if records, err := store.ReadRecords("codex-remote", "s", "t"); err != nil || len(records) != 2 {
		t.Fatalf("torn tail must roll back to tx2: %d records, err %v", len(records), err)
	}
}

// P0-3 position rule: a torn line is legal ONLY as the final line. Damage
// followed by more content — or two bad lines — quarantines the whole dir.
func TestDetailStoreSweepTornTailOnlyAsFinalLine(t *testing.T) {
	committed := func(t *testing.T) (*TurnDetailStore, string, string) {
		store, root := newStore(t)
		if _, err := store.AcceptPage(DetailPageAccept{
			BackendID: "codex-remote", SessionID: "s", TurnID: "t",
			Generation: 0, Page: 1, NextCursor: "", EOF: true,
			Entries: []DetailPageEntry{inlineEntry("i1", "x")},
		}); err != nil {
			t.Fatal(err)
		}
		return store, root, filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t"))
	}

	appendLine := func(t *testing.T, dir, text string) {
		t.Helper()
		logF, err := os.OpenFile(filepath.Join(dir, "items.log"), os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := logF.WriteString(text); err != nil {
			t.Fatal(err)
		}
		logF.Close()
	}

	// Legal: single torn tail at the very end.
	store, _, dir := committed(t)
	appendLine(t, dir, `{"tx":2,"pa`)
	if err := store.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadManifest("codex-remote", "s", "t"); err != nil {
		t.Fatalf("final torn tail must roll back, not quarantine: %v", err)
	}

	// Illegal: torn line FOLLOWED by a parseable line (a torn append cannot
	// produce this — it is log-structure corruption).
	store2, _, dir2 := committed(t)
	appendLine(t, dir2, `{"tx":2,"pa`+"\n"+`{"tx":3,"page":3,"entries":[]}`+"\n")
	if err := store2.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if _, err := store2.LoadManifest("codex-remote", "s", "t"); !errors.Is(err, ErrDetailStoreNotFound) {
		t.Fatalf("mid-log torn line must quarantine the dir, got err = %v", err)
	}

	// Illegal: two torn lines.
	store3, _, dir3 := committed(t)
	appendLine(t, dir3, `{"tx":2,"p`+"\n"+`{"tx":3,"p`+"\n")
	if err := store3.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if _, err := store3.LoadManifest("codex-remote", "s", "t"); !errors.Is(err, ErrDetailStoreNotFound) {
		t.Fatalf("two torn lines must quarantine the dir, got err = %v", err)
	}
}

// P0-3: ANY defect in the committed range 1..TxApplied quarantines the whole
// dir — the turn re-hydrates from official pagination, never "repaired".
func TestDetailStoreSweepQuarantinesCommittedCorruption(t *testing.T) {
	committed2 := func(t *testing.T) (*TurnDetailStore, string, string) {
		store, root := newStore(t)
		for page, id := range []string{"i1", "i2"} {
			if _, err := store.AcceptPage(DetailPageAccept{
				BackendID: "codex-remote", SessionID: "s", TurnID: "t",
				Generation: 0, Page: page + 1, NextCursor: "", EOF: page == 1,
				Entries: []DetailPageEntry{inlineEntry(id, "x")},
			}); err != nil {
				t.Fatal(err)
			}
		}
		return store, root, filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t"))
	}

	// Missing committed record (line 1 dropped): 1 record vs TxApplied=2.
	store, _, dir := committed2(t)
	raw, err := os.ReadFile(filepath.Join(dir, "items.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(strings.TrimRight(string(raw), "\n"), "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("expected 2 committed lines, got %q", raw)
	}
	if err := os.WriteFile(filepath.Join(dir, "items.log"), []byte(lines[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadManifest("codex-remote", "s", "t"); !errors.Is(err, ErrDetailStoreNotFound) {
		t.Fatalf("missing committed record must quarantine, got err = %v", err)
	}

	// Parseable but WRONG committed record (page field tampered): the strict
	// validators catch it at sweep time too.
	store2, _, dir2 := committed2(t)
	raw2, err := os.ReadFile(filepath.Join(dir2, "items.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines2 := strings.SplitN(strings.TrimRight(string(raw2), "\n"), "\n", 2)
	var rec detailTxRecord
	if err := json.Unmarshal([]byte(lines2[0]), &rec); err != nil {
		t.Fatal(err)
	}
	rec.Page = 7
	bad, _ := json.Marshal(rec)
	rebuilt := append(bad, '\n')
	rebuilt = append(rebuilt, []byte(lines2[1])...)
	rebuilt = append(rebuilt, '\n')
	if err := os.WriteFile(filepath.Join(dir2, "items.log"), rebuilt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store2.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if _, err := store2.LoadManifest("codex-remote", "s", "t"); !errors.Is(err, ErrDetailStoreNotFound) {
		t.Fatalf("tampered committed record must quarantine, got err = %v", err)
	}
}

// P1-4: runtime replay is fail-closed — a parseable-but-wrong committed
// record, or any unparseable line, is an error, not silently skipped data.
func TestDetailStoreReadRecordsFailClosed(t *testing.T) {
	store, root := newStore(t)
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 1, NextCursor: "", EOF: true,
		Entries: []DetailPageEntry{inlineEntry("i1", "x")},
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t"))

	// Tamper the committed record's chunkSeq span (stays parseable).
	raw, err := os.ReadFile(filepath.Join(dir, "items.log"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"chunkSeqLast":1`, `"chunkSeqLast":9`, 1)
	if tampered == string(raw) {
		t.Fatalf("test tamper target not found in %q", raw)
	}
	if err := os.WriteFile(filepath.Join(dir, "items.log"), []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadRecords("codex-remote", "s", "t"); !errors.Is(err, ErrDetailStoreCorrupt) {
		t.Fatalf("tampered span must fail closed, err = %v", err)
	}

	// Restore, then append an unparseable line: runtime replay refuses
	// (post-sweep the log must be clean).
	if err := os.WriteFile(filepath.Join(dir, "items.log"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	logF, err := os.OpenFile(filepath.Join(dir, "items.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logF.WriteString("garbage\n"); err != nil {
		t.Fatal(err)
	}
	logF.Close()
	if _, err := store.ReadRecords("codex-remote", "s", "t"); !errors.Is(err, ErrDetailStoreCorrupt) {
		t.Fatalf("unparseable line must fail closed, err = %v", err)
	}
}

// A dir without a manifest is uncommitted garbage — removed whole, whether it
// holds an items.log, only blobs, or both (P0-3: the old sweep only handled
// the items.log case, leaving blob-only crash leftovers forever).
func TestDetailStoreSweepRemovesManifestlessDir(t *testing.T) {
	store, root := newStore(t)
	withLog := filepath.Join(root, "codex-remote", hashSeg("s2"), hashSeg("t2"))
	if err := os.MkdirAll(withLog, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(withLog, "items.log"), []byte("{\"tx\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blobsOnly := filepath.Join(root, "codex-remote", hashSeg("s3"), hashSeg("t3"), "blobs")
	if err := os.MkdirAll(blobsOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobsOnly, "deadbeef.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(withLog); !os.IsNotExist(err) {
		t.Fatal("manifest-less turn dir (items.log) must be removed")
	}
	if _, err := os.Stat(filepath.Dir(blobsOnly)); !os.IsNotExist(err) {
		t.Fatal("manifest-less turn dir (blobs only) must be removed")
	}
}

func TestDetailStoreBudgetEvictsWholeTurnDirs(t *testing.T) {
	store, _ := newStore(t)
	store.SetBudget(500 * 1024)

	accept := func(session, turn, itemID string) {
		t.Helper()
		if _, err := store.AcceptPage(DetailPageAccept{
			BackendID: "codex-remote", SessionID: session, TurnID: turn,
			Generation: 0, Page: 1, NextCursor: "", EOF: true,
			Entries: []DetailPageEntry{inlineEntry(itemID, strings.Repeat("d", 200*1024))},
		}); err != nil {
			t.Fatal(err)
		}
	}
	accept("s", "old", "a") // ~200KB
	accept("s", "mid", "b") // ~200KB
	accept("s", "new", "c") // ~200KB → total ~600KB > 500KB budget

	if _, err := store.LoadManifest("codex-remote", "s", "old"); !errors.Is(err, ErrDetailStoreNotFound) {
		t.Fatalf("oldest turn must be evicted, err = %v", err)
	}
	if _, err := store.LoadManifest("codex-remote", "s", "mid"); err != nil {
		t.Fatalf("mid must survive: %v", err)
	}
	if _, err := store.LoadManifest("codex-remote", "s", "new"); err != nil {
		t.Fatalf("newest must survive: %v", err)
	}
	if usage := store.StoreUsage(); usage > 500*1024+64*1024 {
		t.Fatalf("usage after eviction = %d, want ≤ ~budget (+manifest slack)", usage)
	}
}

func TestDetailStoreUpdateState(t *testing.T) {
	store, _ := newStore(t)
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 5, Page: 1, NextCursor: "", EOF: true,
		Entries: []DetailPageEntry{inlineEntry("i1", "x")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateState("codex-remote", "s", "t", 5, DetailStateLoaded, ""); err != nil {
		t.Fatal(err)
	}
	m, _ := store.LoadManifest("codex-remote", "s", "t")
	if m.State != DetailStateLoaded || m.ManifestRev != 1 {
		t.Fatalf("state mirror = %q rev %d, want loaded rev 1 (no progress bump)", m.State, m.ManifestRev)
	}
	if err := store.UpdateState("codex-remote", "s", "t", 6, DetailStateLoaded, ""); !errors.Is(err, ErrDetailGeneration) {
		t.Fatalf("generation mismatch err = %v", err)
	}
}

func TestSplitDetailChunksPacking(t *testing.T) {
	small := inlinePart("a", strings.Repeat("s", 10*1024))
	big := inlinePart("b", strings.Repeat("B", 250*1024)) // > advisory alone (serialized), ≤ hard
	big2 := inlinePart("c", strings.Repeat("C", 250*1024))
	chunks, err := SplitDetailChunks([]DetailChunkEntry{{Part: small}, {Part: big}, {Part: big2}, {Part: small}})
	if err != nil {
		t.Fatal(err)
	}
	// small+big can't share (advisory) → [small],[big],[big2],[small] (the
	// trailing small cannot share with the first — big2 sits between them).
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(chunks))
	}
	if got := flattenChunkItems(chunks); len(got) != 4 || got[0] != "a" || got[1] != "b" || got[2] != "c" || got[3] != "a" {
		t.Fatalf("packing order wrong: %v", got)
	}
	// A single entry beyond the hard cap is an error, not a silent split.
	huge := inlinePart("h", strings.Repeat("H", int(core.TurnDetailPatchHardCapBytes)+1024))
	if _, err := SplitDetailChunks([]DetailChunkEntry{{Part: huge}}); !errors.Is(err, ErrDetailChunkTooLarge) {
		t.Fatalf("hard cap err = %v", err)
	}
	// Empty page packs into zero chunks.
	if got, _ := SplitDetailChunks(nil); len(got) != 0 {
		t.Fatalf("empty page = %d chunks, want 0", len(got))
	}
	// An oversize entry's ref rides the SAME chunk as its slim card (P0-2).
	card := slimCmdCard("cmd-1")
	ref := TurnDetailOversizeRef{ItemID: "cmd-1", Handle: "h0", Type: "tool", TotalChunks: 1}
	over, err := SplitDetailChunks([]DetailChunkEntry{{Part: card, Ref: &ref}})
	if err != nil {
		t.Fatal(err)
	}
	if len(over) != 1 || len(over[0].Items) != 1 || over[0].Items[0].ItemID != "cmd-1" ||
		len(over[0].Oversize) != 1 || over[0].Oversize[0].Handle != "h0" {
		t.Fatalf("card and ref must share the chunk: %+v", over)
	}
}
