package transcriptindex

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// fileFingerprint captures the cheap mtime+size signal at index time.
func fileFingerprint(stat os.FileInfo) Fingerprint {
	return Fingerprint{
		SizeBytes:       stat.Size(),
		ModTimeUnixNano: stat.ModTime().UnixNano(),
	}
}

// Build constructs a PageIndex by scanning the entire transcript once. The
// resulting index has a fresh root generation lineage.
func Build(backend Backend, path string) (*PageIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	ext := newExtractor(backend)
	if ext == nil {
		return nil, fmt.Errorf("transcriptindex: unknown backend %q", backend)
	}
	if err := scanTranscript(f, func(rec Record) error { ext.Process(rec); return nil }); err != nil {
		return nil, err
	}
	ext.Finish(stat.Size())

	idx := &PageIndex{
		Version:           IndexVersion,
		Backend:           backend,
		ExtractorRevision: extractorRevision(backend),
		FilePath:          path,
		Fingerprint:       fileFingerprint(stat),
		ParseOffset:       stat.Size(),
		Spans:             ext.Spans(),
		FileDevice:        statDevice(stat),
		FileInode:         statInode(stat),
	}
	idx.PrefixGeneration, idx.Anchor, idx.GenerationLineage, err = finalizeNewLineage(f, idx.ParseOffset)
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// Refresh updates idx to reflect an appended tail, reusing the persisted prefix
// when continuity is verified. It returns a new PageIndex; idx is not mutated.
//
// If the file was replaced, truncated, rewritten in place, or the continuity
// anchor does not verify, Refresh falls back to a fresh full Build with a new
// root lineage, so old backward cursors become safely stale.
func Refresh(idx *PageIndex) (*PageIndex, error) {
	if idx == nil {
		return nil, fmt.Errorf("transcriptindex: refresh on nil index")
	}
	if idx.ExtractorRevision != extractorRevision(idx.Backend) {
		return Build(idx.Backend, idx.FilePath)
	}
	f, err := os.Open(idx.FilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	// Replaced on disk (different dev/inode) -> fresh lineage.
	if !sameIdentity(idx, stat) {
		return Build(idx.Backend, idx.FilePath)
	}
	// Truncated -> fresh lineage.
	if stat.Size() < idx.ParseOffset {
		return Build(idx.Backend, idx.FilePath)
	}
	// No growth and fingerprint unchanged -> nothing to do.
	if stat.Size() == idx.ParseOffset && fileFingerprint(stat).Equal(idx.Fingerprint) {
		return idx, nil
	}
	// Grew: prove the old prefix is a continuous tail append.
	if ok, err := verifyAnchorAt(f, idx); err != nil {
		return nil, err
	} else if !ok {
		return Build(idx.Backend, idx.FilePath)
	}

	replayFrom := refreshReplayStart(idx.Spans)
	kept := keepSpansBefore(idx.Spans, replayFrom)

	ext := newExtractor(idx.Backend)
	if ext == nil {
		return nil, fmt.Errorf("transcriptindex: unknown backend %q", idx.Backend)
	}
	if _, err := f.Seek(replayFrom, io.SeekStart); err != nil {
		return nil, err
	}
	if err := scanTranscript(f, func(rec Record) error { ext.Process(rec); return nil }); err != nil {
		return nil, err
	}
	ext.Finish(stat.Size())
	rebuilt := ext.Spans()

	nextOrd := int64(0)
	if n := len(kept); n > 0 {
		nextOrd = kept[n-1].Ordinal + 1
	}
	for i := range rebuilt {
		rebuilt[i].Ordinal = nextOrd + int64(i)
	}

	newIdx := *idx // shallow copy; slices below are replaced
	newIdx.Fingerprint = fileFingerprint(stat)
	newIdx.ParseOffset = stat.Size()
	newIdx.FileDevice = statDevice(stat)
	newIdx.FileInode = statInode(stat)
	newIdx.Spans = concatSpans(kept, rebuilt)
	newIdx.PrefixGeneration, newIdx.Anchor, newIdx.GenerationLineage, err = appendChildGeneration(f, idx, stat.Size())
	if err != nil {
		return nil, err
	}
	return &newIdx, nil
}

// refreshReplayStart computes the earliest byte offset from which the tail must
// be re-parsed on append. It starts from the last span (for tail assistant
// merge) and any span with unclosed tool uses, then expands backward to fully
// contain any span whose [ReplayStart, EndOffset] overlaps the replay region.
func refreshReplayStart(spans []LogicalMessageSpan) int64 {
	if len(spans) == 0 {
		return 0
	}
	replayFrom := spans[len(spans)-1].ReplayStart
	for i := len(spans) - 1; i >= 0; i-- {
		if spans[i].OpenToolUses > 0 && spans[i].ReplayStart < replayFrom {
			replayFrom = spans[i].ReplayStart
		}
	}
	for moved := true; moved; {
		moved = false
		for _, s := range spans {
			if s.ReplayStart < replayFrom && s.EndOffset > replayFrom {
				replayFrom = s.ReplayStart
				moved = true
			}
		}
	}
	return replayFrom
}

// keepSpansBefore returns the clean prefix of spans whose ReplayStart precedes
// replayFrom. Because spans are in non-decreasing ReplayStart order and
// refreshReplayStart moved the boundary past any overlapping span, the result
// is exactly spans[0:k] with no overlap into the replayed region.
func keepSpansBefore(spans []LogicalMessageSpan, replayFrom int64) []LogicalMessageSpan {
	k := 0
	for k < len(spans) && spans[k].ReplayStart < replayFrom {
		k++
	}
	return append([]LogicalMessageSpan(nil), spans[:k]...)
}

func concatSpans(a, b []LogicalMessageSpan) []LogicalMessageSpan {
	out := make([]LogicalMessageSpan, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// LastN returns up to n newest spans in ascending ordinal order.
func LastN(spans []LogicalMessageSpan, n int) []LogicalMessageSpan {
	if n <= 0 || len(spans) == 0 {
		return nil
	}
	if n > len(spans) {
		n = len(spans)
	}
	return append([]LogicalMessageSpan(nil), spans[len(spans)-n:]...)
}

// PageBefore returns up to count spans with ordinal strictly less than
// beforeOrdinal, in ascending order. Used for backward paging.
func PageBefore(spans []LogicalMessageSpan, beforeOrdinal int64, count int) []LogicalMessageSpan {
	if count <= 0 || len(spans) == 0 {
		return nil
	}
	end := sort.Search(len(spans), func(i int) bool { return spans[i].Ordinal >= beforeOrdinal })
	start := end - count
	if start < 0 {
		start = 0
	}
	if start >= end {
		return nil
	}
	return append([]LogicalMessageSpan(nil), spans[start:end]...)
}

// ByteUnionFor returns the smallest byte range that fully contains every span
// in page and every span overlapping it, expanded to a fixpoint so a grouping
// builder sees only whole messages. Use this to record page_replay_bytes.
func (idx *PageIndex) ByteUnionFor(page []LogicalMessageSpan) ByteRange {
	return spanByteUnion(idx.Spans, page)
}

// spanByteUnion is the fixpoint expansion over allSpans for the page subset.
func spanByteUnion(allSpans, page []LogicalMessageSpan) ByteRange {
	if len(page) == 0 {
		return ByteRange{}
	}
	lo, hi := page[0].ReplayStart, page[0].EndOffset
	for _, s := range page[1:] {
		if s.ReplayStart < lo {
			lo = s.ReplayStart
		}
		if s.EndOffset > hi {
			hi = s.EndOffset
		}
	}
	for moved := true; moved; {
		moved = false
		for _, s := range allSpans {
			if s.EndOffset <= lo || s.ReplayStart >= hi {
				continue // disjoint
			}
			if s.ReplayStart < lo {
				lo = s.ReplayStart
				moved = true
			}
			if s.EndOffset > hi {
				hi = s.EndOffset
				moved = true
			}
		}
	}
	return ByteRange{Start: lo, End: hi}
}

// ContentReplayer rebuilds logical-message content from a byte range of the
// transcript. It must run the backend's grouping builder over the range and
// return ALL logical messages it produces, in ascending file order, beginning
// with the message whose ReplayStart == r.Start. The number returned must equal
// the count of whole messages fully contained in r; PageIndex.Replay verifies
// this and fails safe on mismatch.
type ContentReplayer func(path string, r ByteRange) ([]core.RichHistoryEntry, error)

// Replay resolves the byte union for page, runs replayer, and returns exactly
// the page's messages in ascending ordinal order. Callers wanting newest-first
// should reverse the result. A mismatch between produced and expected messages
// is reported as an error rather than silently misaligning pages.
func (idx *PageIndex) Replay(page []LogicalMessageSpan, replayer ContentReplayer) ([]core.RichHistoryEntry, error) {
	if replayer == nil {
		return nil, fmt.Errorf("transcriptindex: nil replayer")
	}
	if len(page) == 0 {
		return nil, nil
	}
	union := idx.ByteUnionFor(page)
	entries, err := replayer(idx.FilePath, union)
	if err != nil {
		return nil, err
	}
	first, _, ok := idx.spanAtOffset(union.Start)
	if !ok {
		return nil, fmt.Errorf("transcriptindex: union start %d is not a message boundary", union.Start)
	}
	firstOrd := first.Ordinal
	want := make(map[int64]bool, len(page))
	for _, s := range page {
		want[s.Ordinal] = true
	}
	out := make([]core.RichHistoryEntry, 0, len(page))
	for i, e := range entries {
		if want[firstOrd+int64(i)] {
			out = append(out, e)
		}
	}
	if len(out) != len(page) {
		return out, fmt.Errorf("transcriptindex: replay matched %d of %d page messages (union %d-%d, produced %d)",
			len(out), len(page), union.Start, union.End, len(entries))
	}
	return out, nil
}
