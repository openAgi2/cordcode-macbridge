package transcriptindex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// cursorVersion is the wire version of the opaque message cursor. Bumping it
// makes older client cursors un-decodable so they are treated as stale and the
// client reloads the first page (design §6.5).
const cursorVersion = 1

// MessageCursor is an opaque, versioned backward cursor into one session's
// logical-message index. It pins a logical-message ordinal within a specific
// prefix generation, so backward paging keeps working across tail appends and
// Bridge restarts as long as that generation remains in the provable lineage.
type MessageCursor struct {
	Version    int    `json:"v"`
	SessionID  string `json:"sid"`
	Ordinal    int64  `json:"ord"`
	Generation string `json:"gen"`
}

// EncodeMessageCursor returns an opaque (base64url) encoding of c suitable for
// sending to clients. The encoding is versioned; clients must treat it as opaque.
func EncodeMessageCursor(c MessageCursor) (string, error) {
	if c.SessionID == "" {
		return "", fmt.Errorf("transcriptindex: cursor requires session id")
	}
	c.Version = cursorVersion
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeMessageCursor parses an opaque cursor. When sessionID is non-empty it
// must match the cursor's session id, preventing a cursor from one session being
// replayed against another. A malformed or wrong-version cursor returns an error
// that the handler maps to cursor_stale.
func DecodeMessageCursor(s, sessionID string) (MessageCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return MessageCursor{}, fmt.Errorf("transcriptindex: malformed cursor")
	}
	var c MessageCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return MessageCursor{}, fmt.Errorf("transcriptindex: malformed cursor")
	}
	if c.Version != cursorVersion {
		return MessageCursor{}, fmt.Errorf("transcriptindex: unsupported cursor version %d", c.Version)
	}
	if sessionID != "" && c.SessionID != sessionID {
		return MessageCursor{}, fmt.Errorf("transcriptindex: cursor session mismatch")
	}
	return c, nil
}

// IsCursorStale reports whether c references a prefix generation that is no
// longer provable from the current lineage (design §6.5.2). A generation is
// provable while it remains in the bounded lineage chain (or survives as the
// folded root checkpoint). After folding, generations older than the retained
// window are stale, so the client must reload the first page rather than silently
// stitching across lineages.
func (idx *PageIndex) IsCursorStale(c MessageCursor) bool {
	if idx == nil {
		return true
	}
	for _, g := range idx.GenerationLineage {
		if g.ID == c.Generation {
			return false
		}
	}
	return true
}
