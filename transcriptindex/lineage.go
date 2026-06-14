package transcriptindex

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"time"
)

// maxLineageDepth bounds the persisted generation chain. On overflow the oldest
// contiguous ancestors fold into a root checkpoint (design §6.5.2); cursors
// referencing folded generations become stale, which is the safe choice.
const maxLineageDepth = 64

// newGenerationID returns an opaque generation identifier.
func newGenerationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "g" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// finalizeNewLineage builds the root lineage for a freshly indexed transcript.
func finalizeNewLineage(f io.ReaderAt, parseOffset int64) (genID string, anchor AnchorDesc, lineage []PrefixGenerationRecord, err error) {
	digest, _, aerr := anchorFor(f, parseOffset, defaultAnchorWindow)
	if aerr != nil {
		return "", AnchorDesc{}, nil, aerr
	}
	genID = newGenerationID()
	anchor = AnchorDesc{Algo: defaultAnchorAlgo, Window: defaultAnchorWindow}
	lineage = []PrefixGenerationRecord{{
		ID:             genID,
		IndexedThrough: parseOffset,
		AnchorHex:      digest,
	}}
	return genID, anchor, lineage, nil
}

// appendChildGeneration extends a lineage with one verified append generation.
func appendChildGeneration(f io.ReaderAt, parent *PageIndex, indexedThrough int64) (genID string, anchor AnchorDesc, lineage []PrefixGenerationRecord, err error) {
	window := parent.Anchor.Window
	if window <= 0 {
		window = defaultAnchorWindow
	}
	digest, _, aerr := anchorFor(f, indexedThrough, window)
	if aerr != nil {
		return "", AnchorDesc{}, nil, aerr
	}
	algo := parent.Anchor.Algo
	if algo == "" {
		algo = defaultAnchorAlgo
	}
	genID = newGenerationID()
	anchor = AnchorDesc{Algo: algo, Window: window}
	lineage = append([]PrefixGenerationRecord(nil), parent.GenerationLineage...)
	lineage = append(lineage, PrefixGenerationRecord{
		ID:             genID,
		ParentID:       parent.PrefixGeneration,
		IndexedThrough: indexedThrough,
		AnchorHex:      digest,
	})
	lineage = foldLineage(lineage)
	return genID, anchor, lineage, nil
}

// foldLineage collapses an over-long chain by replacing the oldest prefix with
// a single root checkpoint. Generations older than the kept window are no longer
// individually provable, so cursors referencing them are stale (safe).
func foldLineage(lineage []PrefixGenerationRecord) []PrefixGenerationRecord {
	if len(lineage) <= maxLineageDepth {
		return lineage
	}
	keep := maxLineageDepth - 1 // newest kept generations
	newest := append([]PrefixGenerationRecord(nil), lineage[len(lineage)-keep:]...)
	// The fold boundary becomes the new root; its child (oldest kept) already
	// had it as parent, so the rewiring below preserves the true relationship.
	foldBoundary := lineage[len(lineage)-keep-1]
	root := PrefixGenerationRecord{
		ID:             foldBoundary.ID,
		ParentID:       "",
		IndexedThrough: foldBoundary.IndexedThrough,
		AnchorHex:      foldBoundary.AnchorHex,
	}
	newest[0] = PrefixGenerationRecord{
		ID:             newest[0].ID,
		ParentID:       root.ID,
		IndexedThrough: newest[0].IndexedThrough,
		AnchorHex:      newest[0].AnchorHex,
	}
	return append([]PrefixGenerationRecord{root}, newest...)
}

// validateLineage checks that a lineage is well-formed: non-empty, unique ids,
// rooted (first record has no parent), and every non-root parent exists.
func validateLineage(lineage []PrefixGenerationRecord) bool {
	if len(lineage) == 0 {
		return false
	}
	seen := make(map[string]bool, len(lineage))
	for _, g := range lineage {
		if g.ID == "" || seen[g.ID] {
			return false
		}
		seen[g.ID] = true
	}
	if lineage[0].ParentID != "" {
		return false
	}
	for _, g := range lineage[1:] {
		if g.ParentID == "" || !seen[g.ParentID] {
			return false
		}
	}
	return true
}

// sameIdentity reports whether the file at stat is the same on-disk file the
// index was built against. When identity was not tracked (zeros) it returns
// true so the size/anchor checks make the decision.
func sameIdentity(idx *PageIndex, stat os.FileInfo) bool {
	if idx.FileDevice == 0 && idx.FileInode == 0 {
		return true
	}
	return statDevice(stat) == idx.FileDevice && statInode(stat) == idx.FileInode
}

// verifyAnchorAt re-computes the continuity anchor at the index's parse offset
// and compares it to the current generation's stored anchor.
func verifyAnchorAt(f io.ReaderAt, idx *PageIndex) (bool, error) {
	cur := idx.currentGeneration()
	if cur == nil {
		return false, nil
	}
	window := idx.Anchor.Window
	if window <= 0 {
		window = defaultAnchorWindow
	}
	digest, _, err := anchorFor(f, idx.ParseOffset, window)
	if err != nil {
		return false, err
	}
	return digest == cur.AnchorHex, nil
}
