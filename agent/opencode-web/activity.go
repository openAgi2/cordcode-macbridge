package opencodeweb

import (
	"context"
)

// activity.go implements core.SessionActivityProbing (design §4.3.2): the
// cold-hydrate producer asks whether a session still has a turn in flight so
// a dead trailing user turn settles as turn_error while a live one stays
// open.
//
// 1.18 GET /session/status answers the server's own busy map — a MISSING key
// is a definitive idle verdict (verified live; the legacy package carries the
// same reading). v2 /api/session/active only reports THIS process's
// foreground drain — absence is NOT a global idle verdict (design §3.2), so
// on v2 a miss stays conservative-active.
//
// Any HTTP/parse failure reports active (the interface contract: unknown ⇒
// active — never falsely settle a live turn).

// IsSessionActive implements core.SessionActivityProbing.
func (a *Agent) IsSessionActive(ctx context.Context, sessionID string) bool {
	c, err := a.clientFor(ctx)
	if err != nil {
		return true
	}
	return a.isSessionActiveWith(ctx, c, sessionID)
}

func (a *Agent) isSessionActiveWith(ctx context.Context, c *Client, sessionID string) bool {
	statusPath := "/session/status"
	if c.Generation() == generationV2 {
		statusPath = "/api/session/active"
	}
	raw, err := c.fetchJSON(ctx, statusPath, a.GetWorkDir())
	if err != nil {
		return true
	}
	var busy map[string]struct{}
	if err := decodeJSONObject(raw, &busy); err != nil {
		return true
	}
	if _, isActive := busy[sessionID]; isActive {
		return true
	}
	// Present-key miss: 1.18 = definitive idle; v2 = foreground-drain scope
	// only, so absence stays conservative-active.
	return c.Generation() == generationV2
}
