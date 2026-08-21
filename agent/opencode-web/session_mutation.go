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
// Live caveat (sandbox 1.18.18): the default GET /session enumeration does NOT
// exclude archived sessions — the rows keep flowing with time.archived set,
// so the list mapper populates AgentSessionInfo.ArchivedAt and clients hide
// them (wire archivedAtMillis semantics).

// FetchSessionInfo implements core.SessionInfoFetcher via GET /session/{id}
// (same Session.Info shape as the list rows; v2 {"data":…} envelope unwrapped
// by fetchSessionInfo). The single-object read stays valid for archived
// sessions that the default list may exclude, so the bridge's get_session
// does not race the list filter after an archive.
func (a *Agent) FetchSessionInfo(ctx context.Context, sessionID string) (*core.AgentSessionInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("opencode-web: fetch session info: empty session id")
	}
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	entry, err := a.fetchSessionInfo(ctx, c, sessionID)
	if err != nil {
		return nil, err
	}
	return ocwSessionEntryToInfo(entry), nil
}

func ocwSessionEntryToInfo(entry *ocwSessionEntry) *core.AgentSessionInfo {
	info := &core.AgentSessionInfo{
		ID:         entry.ID,
		Summary:    entry.Title,
		Directory:  entry.Directory,
		ModifiedAt: entry.Time.updatedAt(),
		ArchivedAt: entry.Time.archivedAt(),
	}
	if entry.Model != nil {
		info.ModelID = entry.Model.ID
		info.ProviderID = entry.Model.ProviderID
	}
	return info
}

// RenameSession implements core.SessionRenamer (E6 sample-verified): PATCH
// /session/{id} body {title} → 200 Session.Info with the new title; a missing
// id answers 404 NotFoundError. Only a non-empty title is sent; the returned
// metadata is the refresh truth (no timeline write, no list-only success).
func (a *Agent) RenameSession(ctx context.Context, sessionID, title string) (*core.AgentSessionInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	title = strings.TrimSpace(title)
	if sessionID == "" {
		return nil, fmt.Errorf("opencode-web: rename session: empty session id")
	}
	if title == "" {
		return nil, fmt.Errorf("opencode-web: rename session: empty title (official UpdatePayload accepts only a real title)")
	}
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	path := c.apiPath("/session/" + url.PathEscape(sessionID))
	body := map[string]any{"title": title}
	code, raw, err := c.doRequest(ctx, http.MethodPatch, c.endpoint(path), body, a.GetWorkDir(), true)
	if err != nil {
		return nil, fmt.Errorf("opencode-web: rename session %s: %w", sessionID, err)
	}
	// Directive-010: evidence-proven success is HTTP 200 EXACTLY (E6). Other
	// 2xx codes are unproven for this route and fail closed.
	if code != http.StatusOK {
		return nil, fmt.Errorf("opencode-web: rename session %s: evidence-proven success is HTTP 200 only, got %d: %s", sessionID, code, truncateForError(string(raw)))
	}
	var entry ocwSessionEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("opencode-web: rename session %s: response decode: %w", sessionID, err)
	}
	if entry.ID == "" || entry.ID != sessionID {
		return nil, fmt.Errorf("opencode-web: rename session %s: response missing or mismatched id %q", sessionID, entry.ID)
	}
	if entry.Title != title {
		return nil, fmt.Errorf("opencode-web: rename session %s: response title %q does not confirm the requested title %q", sessionID, entry.Title, title)
	}
	a.signalCatalogRefresh()
	return ocwSessionEntryToInfo(&entry), nil
}

var _ core.SessionRenamer = (*Agent)(nil)

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
	// Convergence needs the session's own directory for the scoped-list
	// absence check; fetch it BEFORE the delete (E7: list absence + by-ID 404).
	var directory string
	if info, err := a.fetchSessionInfo(ctx, c, sessionID); err == nil && info.Directory != "" {
		directory = info.Directory
	}
	path := c.apiPath("/session/" + url.PathEscape(sessionID))
	code, raw, err := c.doRequest(ctx, http.MethodDelete, c.endpoint(path), nil, a.GetWorkDir(), true)
	if err != nil {
		return fmt.Errorf("opencode-web: delete session %s: %w", sessionID, err)
	}
	// Directive-010: evidence-proven success is HTTP 200 EXACTLY (E7) — a
	// non-200 answer fails even when the body reads `true`, and no catalog
	// signal is sent on that path.
	if code != http.StatusOK {
		return fmt.Errorf("opencode-web: delete session %s: evidence-proven success is HTTP 200 only, got %d: %s", sessionID, code, truncateForError(string(raw)))
	}
	// E7: the ONLY evidence-proven success body is the bare boolean `true`.
	// false/null/object/empty/other 2xx bodies all fail (audit-008 W2.3).
	if strings.TrimSpace(string(raw)) != "true" {
		return fmt.Errorf("opencode-web: delete session %s: response is not the evidence-proven boolean true (got %q)", sessionID, truncateForError(string(raw)))
	}
	// Authoritative convergence: by-ID must 404 and the scoped list must no
	// longer carry the id. Until every check passes, no catalog signal and
	// no success claim (§6.10).
	if _, err := a.fetchSessionInfo(ctx, c, sessionID); err == nil || !strings.Contains(err.Error(), "404") {
		return fmt.Errorf("opencode-web: delete session %s: session still readable after delete (expected 404): %v", sessionID, err)
	}
	if directory != "" {
		rows, err := a.listSessionsWith(ctx, c, directory)
		if err != nil {
			return fmt.Errorf("opencode-web: delete session %s: convergence list check failed: %w", sessionID, err)
		}
		for _, row := range rows {
			if row.ID == sessionID {
				return fmt.Errorf("opencode-web: delete session %s: session still present in the scoped list", sessionID)
			}
		}
	}
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
	// Directive-010: evidence-proven success is HTTP 200 EXACTLY.
	if code != http.StatusOK {
		return nil, fmt.Errorf("opencode-web: archive session %s: evidence-proven success is HTTP 200 only, got %d: %s", sessionID, code, truncateForError(string(raw)))
	}
	var entry ocwSessionEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("opencode-web: archive session %s: response decode: %w", sessionID, err)
	}
	if entry.ID == "" || entry.ID != sessionID {
		return nil, fmt.Errorf("opencode-web: archive session %s: response missing or mismatched id %q", sessionID, entry.ID)
	}
	// Directive-010: the serve echoes the archived timestamp it persisted for
	// THIS request — missing, zero, or a different value than the requested
	// ms is a failed confirmation, not success. (Epoch-ms integers are exact
	// in float64; the direct comparison also rejects fractional echoes.)
	var archived float64
	if entry.Time != nil {
		archived = entry.Time.Archived
	}
	if archived != float64(ms) {
		return nil, fmt.Errorf("opencode-web: archive session %s: response time.archived %.0f does not confirm the requested %d", sessionID, archived, ms)
	}
	a.signalCatalogRefresh()
	return ocwSessionEntryToInfo(&entry), nil
}
