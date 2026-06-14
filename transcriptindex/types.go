package transcriptindex

// IndexVersion is the persisted transcript index format version. Bumping it
// invalidates every older index file (Load discards version mismatches).
const IndexVersion = 1

// Backend identifies a transcript backend.
type Backend string

const (
	BackendCodex  Backend = "codex"
	BackendClaude Backend = "claude"
)

// extractorRevision identifies the boundary semantics used to build spans for
// each backend. Bump the relevant value whenever extractor decisions change,
// even when the persisted JSON shape remains compatible.
func extractorRevision(backend Backend) string {
	switch backend {
	case BackendCodex:
		return "codex-v2"
	case BackendClaude:
		return "claude-v1"
	default:
		return ""
	}
}

// Fingerprint captures the mtime+size of a transcript file at index time. It is
// the cheap "did the file change at all" signal; the strong continuity anchor in
// each generation is what proves a tail append.
type Fingerprint struct {
	SizeBytes       int64 `json:"sizeBytes"`
	ModTimeUnixNano int64 `json:"modTimeUnixNano"`
}

// Equal reports whether two fingerprints match.
func (f Fingerprint) Equal(o Fingerprint) bool {
	return f.SizeBytes == o.SizeBytes && f.ModTimeUnixNano == o.ModTimeUnixNano
}

// LogicalMessageSpan locates one logical message within a transcript without
// caching its content. Field semantics follow the design doc §6.3:
//
//   - ReplayStart is the earliest byte offset from which the message can be
//     fully rebuilt by the grouping builder.
//   - EndOffset is the position after the last JSONL record belonging to the
//     message. A tool_result that closes a tool in this message extends
//     EndOffset to the result's position even if the result is far downstream.
//   - OpenToolUses is the number of tool calls in the message not yet matched
//     by a backend result record. It drives the tail-replay start point.
type LogicalMessageSpan struct {
	Ordinal      int64  `json:"ordinal"`      // 0-based position in file order
	StableID     string `json:"stableId"`     // backend-natural id where available (Claude msg id)
	ReplayStart  int64  `json:"replayStart"`  // earliest byte offset to fully rebuild this message
	EndOffset    int64  `json:"endOffset"`    // position after the last record of this message
	OpenToolUses int    `json:"openToolUses"` // tool calls in this message not yet closed by a result
}

// ByteRange is a half-open [Start, End) byte interval in a transcript file.
type ByteRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Span returns the byte range covered by one span.
func (s LogicalMessageSpan) Span() ByteRange {
	return ByteRange{Start: s.ReplayStart, End: s.EndOffset}
}

// AnchorDesc records the continuity-anchor algorithm and window used to verify
// a tail append. It is persisted so an algorithm or window change safely
// invalidates older indexes (Load re-validates on use).
type AnchorDesc struct {
	Algo   string `json:"algo"`   // e.g. "sha256-128"
	Window int64  `json:"window"` // configured window size in bytes
}

// PrefixGenerationRecord is one verified append generation in the lineage. The
// lineage is a bounded chain rooted at the first full build; each verified
// append adds a child generation, and overflow folds old ancestors into a root
// checkpoint (see foldLineage).
type PrefixGenerationRecord struct {
	ID             string `json:"id"`             // opaque generation id
	ParentID       string `json:"parentId"`       // parent generation id; empty for a root
	IndexedThrough int64  `json:"indexedThrough"` // byte offset indexed through at this generation
	AnchorHex      string `json:"anchorHex"`      // continuity anchor over [IndexedThrough-window, IndexedThrough]
}

// PageIndex is the persisted, content-free locator index for one transcript.
type PageIndex struct {
	Version           int                      `json:"version"`
	Backend           Backend                  `json:"backend"`
	ExtractorRevision string                   `json:"extractorRevision"`
	FilePath          string                   `json:"filePath"`
	Fingerprint       Fingerprint              `json:"fingerprint"`
	Anchor            AnchorDesc               `json:"anchor"`
	ParseOffset       int64                    `json:"parseOffset"`       // bytes indexed through
	PrefixGeneration  string                   `json:"prefixGeneration"`  // current (newest) generation id
	GenerationLineage []PrefixGenerationRecord `json:"generationLineage"` // bounded, root-first
	Spans             []LogicalMessageSpan     `json:"spans"`
	FileDevice        uint64                   `json:"fileDevice,omitempty"` // best-effort identity check
	FileInode         uint64                   `json:"fileInode,omitempty"`
}

// currentGeneration returns the newest generation record, or nil if absent.
func (idx *PageIndex) currentGeneration() *PrefixGenerationRecord {
	if idx == nil || len(idx.GenerationLineage) == 0 {
		return nil
	}
	return &idx.GenerationLineage[len(idx.GenerationLineage)-1]
}

// spanAtOffset returns the span whose ReplayStart == off and its index.
func (idx *PageIndex) spanAtOffset(off int64) (LogicalMessageSpan, int, bool) {
	for i := range idx.Spans {
		if idx.Spans[i].ReplayStart == off {
			return idx.Spans[i], i, true
		}
	}
	return LogicalMessageSpan{}, -1, false
}
