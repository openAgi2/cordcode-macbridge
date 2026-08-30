package gobridge

// F2 Mac detail store tests against the FROZEN model (§11.8 detail store
// 事务模型): sha256 path safety, fixed-commit-order page accept, deterministic
// chunkSeq/splitter records, oversize blob extraction with actual offset
// tables, startup sweep of uncommitted state, whole-dir budget eviction.

import (
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

func TestDetailStorePathsAreHashed(t *testing.T) {
	store, root := newStore(t)
	// Even when IDs contain separators/NULs, the on-disk path must stay inside
	// root and use hex segments only (ids are hash inputs, never path segments).
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "../../etc/passwd\x00", TurnID: "x/y",
		Generation: 0, Page: 1, NextCursor: "c1",
		Items: []ProjectionPart{inlinePart("i1", "hello")},
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
		Items: []ProjectionPart{inlinePart("i1", "one"), inlinePart("i2", "two")},
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
		Generation: 2, Page: 2, NextCursor: "", EOF: true,
		Items: []ProjectionPart{inlinePart("i3", "three")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p2.ManifestRev != 2 || p2.ChunkSeqFirst != 2 || p2.ChunkSeqLast != 2 {
		t.Fatalf("page2 = rev %d seq %d-%d", p2.ManifestRev, p2.ChunkSeqFirst, p2.ChunkSeqLast)
	}
	m2, _ := store.LoadManifest("codex-remote", "s1", "t1")
	if m2.Resume.EOF != true || m2.Resume.Pages != 2 || m2.ItemCount != 3 || m2.ChunkSeqNext != 3 {
		t.Fatalf("final manifest = %+v", m2)
	}

	// Ordering + duplicate + generation fences.
	if _, err := store.AcceptPage(DetailPageAccept{BackendID: "codex-remote", SessionID: "s1", TurnID: "t1", Generation: 2, Page: 4, Items: []ProjectionPart{inlinePart("i9", "x")}}); !errors.Is(err, ErrDetailPageOrder) {
		t.Fatalf("page skip err = %v", ErrDetailPageOrder)
	}
	if _, err := store.AcceptPage(DetailPageAccept{BackendID: "codex-remote", SessionID: "s1", TurnID: "t1", Generation: 3, Page: 3, Items: []ProjectionPart{inlinePart("i9", "x")}}); !errors.Is(err, ErrDetailGeneration) {
		t.Fatalf("generation err = %v", err)
	}
	if _, err := store.AcceptPage(DetailPageAccept{BackendID: "codex-remote", SessionID: "s1", TurnID: "t1", Generation: 2, Page: 3, Items: []ProjectionPart{inlinePart("i1", "dup")}}); !errors.Is(err, ErrDetailDuplicateItem) {
		t.Fatalf("duplicate err = %v", err)
	}
}

func TestDetailStoreInlineThresholdEnforced(t *testing.T) {
	store, _ := newStore(t)
	big := strings.Repeat("x", int(core.TurnDetailBlobThresholdBytes)+1)
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 1,
		Items: []ProjectionPart{inlinePart("huge", big)},
	}); !errors.Is(err, ErrDetailInlineOversize) {
		t.Fatalf("oversize inline err = %v", err)
	}
}

func TestDetailStoreOversizeBlobLifecycle(t *testing.T) {
	store, root := newStore(t)
	// 1.06MB evidence-shaped output with multibyte runes to exercise offsets.
	content := strings.Repeat("输出行 a moderately long line of text\n", 33*1024) // ~1.05MB
	accepted, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 1, Page: 1, NextCursor: "c", EOF: true,
		Items:    []ProjectionPart{inlinePart("i1", "small")},
		Oversize: []DetailOversizeStaged{{ItemID: "cmd-1", Type: "commandExecution", Preview: content[:42*100], Content: content}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := accepted.OversizeRefs[0]
	if ref.TotalBytes != int64(len(content)) {
		t.Fatalf("ref.TotalBytes = %d, want %d", ref.TotalBytes, len(content))
	}
	if ref.TotalChunks != DetailChunkCount(content) {
		t.Fatalf("ref.TotalChunks = %d, want actual offset table %d", ref.TotalChunks, DetailChunkCount(content))
	}
	if len(ref.Preview) > core.TurnDetailBlobPreviewBytes {
		t.Fatalf("preview must be truncated to %d bytes, got %d", core.TurnDetailBlobPreviewBytes, len(ref.Preview))
	}
	if !strings.HasPrefix(content, ref.Preview) {
		t.Fatal("preview must be a rune-aligned prefix of the content")
	}

	// Blob file exists under blobs/<handle>.bin inside the hashed dir.
	m, _ := store.LoadManifest("codex-remote", "s", "t")
	blobPath := filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t"), "blobs", ref.Handle+".bin")
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("blob file: %v", err)
	}
	if m.Items[1].BlobHandle != ref.Handle || m.Items[1].Bytes != int64(len(content)) {
		t.Fatalf("manifest oversize summary = %+v", m.Items[1])
	}

	// Chunk reads: deterministic, rune-safe, cover the full content.
	total := DetailChunkCount(content)
	var rebuilt strings.Builder
	for i := 0; i < total; i++ {
		data, chunks, totalBytes, err := store.ReadBlobChunk("codex-remote", "s", "t", ref.Handle, i)
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
	if _, _, _, err := store.ReadBlobChunk("codex-remote", "s", "t", ref.Handle, total); !errors.Is(err, ErrDetailChunkIndex) {
		t.Fatalf("out-of-range chunk err = %v", err)
	}
	if _, _, _, err := store.ReadBlobChunk("codex-remote", "s", "t", "not-a-handle", 0); !errors.Is(err, ErrDetailBlobUnref) {
		t.Fatalf("unreferenced handle err = %v", err)
	}
}

func TestDetailStoreReplayMatchesAccept(t *testing.T) {
	store, _ := newStore(t)
	// Page 1: two chunks' worth of items; page 2: one small item.
	var items []ProjectionPart
	for i := 0; i < 40; i++ {
		items = append(items, inlinePart(string(rune('a'+i%26))+string(rune('0'+i/26)), strings.Repeat("m", 20*1024)))
	}
	p1, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 1, NextCursor: "c1", Items: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 2, NextCursor: "", EOF: true,
		Items: []ProjectionPart{inlinePart("zz", "tail")},
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
		rebuilt, err := SplitDetailChunks(rec.Items, rec.Oversize)
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
	_, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 1, NextCursor: "c1",
		Items:    []ProjectionPart{inlinePart("i1", "committed")},
		Oversize: []DetailOversizeStaged{{ItemID: "b1", Type: "commandExecution", Preview: "p", Content: strings.Repeat("z", 300*1024)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "codex-remote", hashSeg("s"), hashSeg("t"))
	handle := hashSeg("codex-remote|s|t|b1")

	// Simulate a crash after steps 1+2 of page 2: blob renamed, log appended,
	// manifest still at TxApplied=1.
	crashBlob := filepath.Join(dir, "blobs", hashSeg("codex-remote|s|t|orphan")+".bin")
	if err := os.WriteFile(crashBlob, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	uncommitted := `{"tx":2,"page":2,"nextCursor":"c2","chunkSeqFirst":2,"chunkSeqLast":2,"items":[{"type":"text","text":"torn","itemId":"i2"}]}` + "\n"
	logF, err := os.OpenFile(filepath.Join(dir, "items.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logF.WriteString(uncommitted); err != nil {
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
	if _, err := os.Stat(filepath.Join(dir, "blobs", handle+".bin")); err != nil {
		t.Fatalf("committed blob must survive: %v", err)
	}
	// Duplicate accept of the "lost" page now succeeds (id space clean).
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: "s", TurnID: "t",
		Generation: 0, Page: 2, NextCursor: "c2", Items: []ProjectionPart{inlinePart("i2", "retried")},
	}); err != nil {
		t.Fatalf("re-accept after sweep: %v", err)
	}
}

// A dir with an items.log but no manifest is wholly uncommitted garbage.
func TestDetailStoreSweepRemovesManifestlessDir(t *testing.T) {
	store, root := newStore(t)
	if err := os.MkdirAll(filepath.Join(root, "codex-remote", hashSeg("s2"), hashSeg("t2")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "codex-remote", hashSeg("s2"), hashSeg("t2"), "items.log"), []byte("{\"tx\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SweepUncommitted(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "codex-remote", hashSeg("s2"), hashSeg("t2"))); !os.IsNotExist(err) {
		t.Fatal("manifest-less turn dir must be removed")
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
			Items: []ProjectionPart{inlinePart(itemID, strings.Repeat("d", 200*1024))},
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
		Items: []ProjectionPart{inlinePart("i1", "x")},
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
	chunks, err := SplitDetailChunks([]ProjectionPart{small, big, big2, small}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// small+big can't share (advisory) → [small],[big],[big2],[small] (the
	// trailing small cannot share with the first — big2 sits between them).
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(chunks))
	}
	if len(chunks[0].Items) != 1 || chunks[0].Items[0].ItemID != "a" ||
		len(chunks[1].Items) != 1 || chunks[1].Items[0].ItemID != "b" ||
		len(chunks[2].Items) != 1 || chunks[2].Items[0].ItemID != "c" ||
		len(chunks[3].Items) != 1 || chunks[3].Items[0].ItemID != "a" {
		t.Fatalf("packing order wrong: %+v", chunks)
	}
	// A single entry beyond the hard cap is an error, not a silent split.
	huge := inlinePart("h", strings.Repeat("H", int(core.TurnDetailPatchHardCapBytes)+1024))
	if _, err := SplitDetailChunks([]ProjectionPart{huge}, nil); !errors.Is(err, ErrDetailChunkTooLarge) {
		t.Fatalf("hard cap err = %v", err)
	}
	// Empty page packs into zero chunks.
	if got, _ := SplitDetailChunks(nil, nil); len(got) != 0 {
		t.Fatalf("empty page = %d chunks, want 0", len(got))
	}
}
