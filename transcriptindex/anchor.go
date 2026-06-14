package transcriptindex

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// Anchor algorithm and window defaults. The design (§6.5.1) allows any strong
// 128-bit checksum versioned in the index; sha256-128 (first 16 bytes of
// SHA-256) is stdlib-only and strong. The actual algorithm and window are
// persisted per index so a future Phase 0 micro-benchmark can swap them without
// trusting older anchors.
const (
	defaultAnchorAlgo   = "sha256-128"
	defaultAnchorWindow = 4096
)

// anchorFor computes a continuity anchor over the fixed window ending at
// indexedThrough. If the indexed prefix is smaller than the window, the whole
// prefix is hashed. Returns the hex digest and the actual bytes hashed.
func anchorFor(r io.ReaderAt, indexedThrough, window int64) (digest string, hashed int64, err error) {
	if window <= 0 {
		window = defaultAnchorWindow
	}
	start := indexedThrough - window
	if start < 0 {
		start = 0
	}
	hashed = indexedThrough - start
	if hashed <= 0 {
		return "", 0, nil
	}
	section := io.NewSectionReader(r, start, hashed)
	h := sha256.New()
	if _, err := io.Copy(h, section); err != nil {
		return "", 0, err
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16]), hashed, nil
}
