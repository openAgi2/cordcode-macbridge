package codexremote

// session_pin.go maps core.SessionPinner onto the official Codex app-server
// thread-section surface (docs/2026-09-25-codex-remote-session-pinning-plan.md
// §3.1, v2). Pin truth is owned by the Desktop state DB: this file keeps no
// local pin copy and never touches the bridge pinstore.
//
// E-6 runtime gate (2026-09-25, Desktop 26.917.62051 / codex
// 0.155.0-alpha.16.3, one-shot controller probe): threadSection/list ok;
// thread/section/move relayed to app-server and validated (synthetic id →
// code -32600 thread-not-found); thread/list accepts sectionId +
// sortKey "section_position" + sortDirection "asc".

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// pinnedThreadSectionID mirrors the upstream built-in section constant
// (codex-rs/state/src/lib.rs PINNED_THREAD_SECTION_ID, checkout 4b1c0c30).
const pinnedThreadSectionID = "01984de2-8f74-7c91-a3b2-5c5e937cf318"

const pinnedListMaxItems = 100

// officialPinnedOrderKey encodes the official Pinned-section order (row 0
// first) as a stable descending timestamp for the wire's pinnedAt sort key
// (iOS sorts the pinned section by pinnedAt DESC). The official set carries
// order, not instants — the fixed base keeps re-fetches from reshuffling.
// Mirrors dsh-web officialPinOrderKey (sessions.go).
func officialPinnedOrderKey(index int) time.Time {
	return time.UnixMilli(pinnedOrderBaseMs - int64(index))
}

const pinnedOrderBaseMs = int64(1_000_000_000_000)

// SetSessionPinned implements core.SessionPinner. Pin/unpin is a
// thread/section/move into/out of the built-in Pinned section; the returned
// pin is envelope-only (PinnedAt echoes the caller's value for the receipt —
// the official store holds section order, not pin instants).
func (a *Agent) SetSessionPinned(ctx context.Context, sessionID, _ string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("codex-remote: pin session: empty session id")
	}
	var sectionID any
	if pinned {
		sectionID = pinnedThreadSectionID
	}
	if err := a.requestVoidThreadOperation(ctx, "thread/section/move", map[string]any{
		"threadId":  sessionID,
		"sectionId": sectionID,
	}); err != nil {
		return nil, err
	}
	a.signalCatalogRefresh()
	if !pinned {
		return nil, nil
	}
	return &core.SessionPin{
		BackendID: BackendID,
		SessionID: sessionID,
		PinnedAt:  pinnedAt.UTC(),
	}, nil
}

// ListPinnedSessions implements core.SessionPinner: the official Pinned
// section's threads in official order, via the sectionId-filtered thread/list.
// An empty section is an empty list, not an error. Upstream errors surface
// as-is (strict-fail; the bridge handler keeps iOS on its last cached set
// rather than fabricating a partial section).
func (a *Agent) ListPinnedSessions(ctx context.Context) ([]core.SessionPin, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	out := make([]core.SessionPin, 0, 16)
	cursor := ""
	for {
		params := map[string]any{
			"limit":         catalogListPageSize,
			"sectionId":     pinnedThreadSectionID,
			"sortKey":       "section_position",
			"sortDirection": "asc",
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, rpcErr, err := cl.RequestContext(ctx, "thread/list", params)
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return nil, rpcErr
		}
		var parsed struct {
			Data       []catalogThreadRow `json:"data"`
			NextCursor json.RawMessage    `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, err
		}
		for _, row := range parsed.Data {
			if row.ID == "" {
				continue
			}
			// Order key uses the GLOBAL row position (len(out)), not the
			// per-page index — page 2's first row must rank below page 1's
			// last row, not restart at the section head.
			out = append(out, core.SessionPin{
				BackendID: BackendID,
				SessionID: row.ID,
				PinnedAt:  officialPinnedOrderKey(len(out)),
			})
			if len(out) >= pinnedListMaxItems {
				return out, nil
			}
		}
		next := strings.Trim(strings.TrimSpace(string(parsed.NextCursor)), `"`)
		if next == "" || next == "null" || next == cursor {
			return out, nil
		}
		cursor = next
	}
}

var _ core.SessionPinner = (*Agent)(nil)
