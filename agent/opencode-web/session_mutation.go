package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// session_mutation.go implements core.SessionDeleter + core.SessionArchiver
// via the official serve HTTP routes (design §4.3.6 会话增删改 — the two
// entries deferred at design time are now live-pinned on the real 1.18.18
// binary, sandbox 2026-08-20):
//
//	delete  DELETE  /session/{id}              → 200 `true` (SessionDeleteResponses.200 = boolean); 404 after
//	archive PATCH   /session/{id} {"time":{"archived": <epoch ms>}}
//	                                         → 200 Session.Info with time.archived echoed
//
// v2 carries the same routes under the /api namespace with an explicitly
// documented time.archived body field (v2 SDK SessionUpdateData) — apiPath()
// maps the prefix per pinned generation. The archive timestamp is the official
// Session.ArchivedTimestamp shape (Schema.Finite epoch milliseconds; the
// official Session.setArchived / touch callers use Date.now()).
//
// Live caveat (sandbox 1.18.18): the default GET /session list does NOT
// exclude archived sessions — the rows keep flowing with time.archived set,
// so the list mapper populates AgentSessionInfo.ArchivedAt and clients hide
// them (wire archivedAtMillis semantics).

// DeleteSession implements core.SessionDeleter.
func (a *Agent) DeleteSession(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("opencode-web: delete session: empty session id")
	}
	c, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	path := c.apiPath("/session/" + url.PathEscape(sessionID))
	code, raw, err := c.doRequest(ctx, http.MethodDelete, c.endpoint(path), nil, a.GetWorkDir(), true)
	if err != nil {
		return fmt.Errorf("opencode-web: delete session %s: %w", sessionID, err)
	}
	if code >= 400 {
		return fmt.Errorf("opencode-web: delete session %s: HTTP %d: %s", sessionID, code, truncateForError(string(raw)))
	}
	// 1.18 answers the bare boolean `true`; any 2xx is the official success.
	a.signalCatalogRefresh()
	return nil
}

// ArchiveSession implements core.SessionArchiver.
func (a *Agent) ArchiveSession(ctx context.Context, sessionID string, archivedAt time.Time) (*core.AgentSessionInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("opencode-web: archive session: empty session id")
	}
	ms := archivedAt.UnixMilli()
	if ms <= 0 {
		ms = time.Now().UnixMilli()
	}
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	path := c.apiPath("/session/" + url.PathEscape(sessionID))
	body := map[string]any{"time": map[string]any{"archived": ms}}
	code, raw, err := c.doRequest(ctx, http.MethodPatch, c.endpoint(path), body, a.GetWorkDir(), true)
	if err != nil {
		return nil, fmt.Errorf("opencode-web: archive session %s: %w", sessionID, err)
	}
	if code >= 400 {
		return nil, fmt.Errorf("opencode-web: archive session %s: HTTP %d: %s", sessionID, code, truncateForError(string(raw)))
	}
	var entry ocwSessionEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("opencode-web: archive session %s: response decode: %w", sessionID, err)
	}
	info := core.AgentSessionInfo{
		ID:        entry.ID,
		Summary:   entry.Title,
		Directory: entry.Directory,
		ModifiedAt: entry.Time.updatedAt(),
		ArchivedAt: entry.Time.archivedAt(),
	}
	if info.ID == "" {
		info.ID = sessionID
	}
	if entry.Model != nil {
		info.ModelID = entry.Model.ID
		info.ProviderID = entry.Model.ProviderID
	}
	a.signalCatalogRefresh()
	return &info, nil
}
