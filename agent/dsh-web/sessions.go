package dshweb

// Session catalog mapping: session.list → core.AgentSessionInfo (design
// §4.3.1): field mapping, subagent/blank filtering, title via the
// session-title projection with the session.history tail-read fallback, and
// the running-flag cache that feeds list enrichment and SessionActivityProbing.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

// clientFor returns a Client bound to the resolved instance. The client is
// stateless beyond the base URL, so a fresh one per call is fine.
func (a *Agent) clientFor(ctx context.Context) (*Client, error) {
	inst, err := a.resolver.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	return NewClient(inst.BaseURL, nil), nil
}

// runningCache mirrors the last session.list running flags (per official
// rows). §8-3's host/session-status frames keep it fresh between lists;
// IsSessionActive reads it conservatively (unknown ⇒ active).
type runningCache struct {
	mu   sync.RWMutex
	set  map[string]bool
	next map[string]bool // staging during a list refresh
}

func (rc *runningCache) stage(items []apiSessionSummary) {
	rc.mu.Lock()
	rc.next = make(map[string]bool, len(items))
	for _, it := range items {
		rc.next[it.SessionID] = it.Running
	}
	rc.mu.Unlock()
}

func (rc *runningCache) commit() {
	rc.mu.Lock()
	rc.set = rc.next
	rc.next = nil
	rc.mu.Unlock()
}

// get returns (running, known). Unknown sessions are NOT invented.
func (rc *runningCache) get(sessionID string) (bool, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	running, ok := rc.set[sessionID]
	return running, ok
}

// setOne updates one session's flag (host/session-status frames).
func (rc *runningCache) setOne(sessionID string, running bool) {
	rc.mu.Lock()
	if rc.set != nil {
		rc.set[sessionID] = running
	}
	rc.mu.Unlock()
}

// ListSessions maps session.list onto AgentSessionInfo rows (design §4.3.1):
// sessionId→id, updatedAt(ms)→modifiedAt, cwd→directory, running→cache;
// subagent rows (origin=subagent / parentSessionId set) and blank sessions
// are filtered exactly like the web sidebar. The official cursor is an
// unimplemented reserved seat — one full page; the bridge paginates.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var val sessionListValue
	if err := client.Call(ctx, "session.list", sessionListRequest{}, &val); err != nil {
		return nil, err
	}

	a.running.stage(val.Items)
	out := make([]core.AgentSessionInfo, 0, len(val.Items))
	for _, item := range val.Items {
		if item.Origin == "subagent" || item.ParentSessionID != "" {
			continue
		}
		if item.Blank {
			continue
		}
		info := core.AgentSessionInfo{
			ID:         item.SessionID,
			ModifiedAt: time.UnixMilli(item.UpdatedAt),
			Directory:  item.Cwd,
		}
		info.Summary = titleFromProjections(item.Projections)
		if info.Summary == "" {
			// Fallback (§3.5): deployments without the session-title projection
			// unit (and cold sessions, whose list rows carry no projections —
			// live-verified 2026-08-16) get the history tail-read title.
			info.Summary = a.tailReadTitle(ctx, client, item.SessionID)
		}
		out = append(out, info)
	}
	a.running.commit()
	return out, nil
}

// titleFromProjections extracts projections.values.title (the session-title
// unit's product; a JSON null or absent unit yields "").
func titleFromProjections(block *apiSessionProjectionsBlock) string {
	if block == nil {
		return ""
	}
	raw, ok := block.Values["title"]
	if !ok || len(raw) == 0 {
		return ""
	}
	var title string
	if err := json.Unmarshal(raw, &title); err != nil || strings.TrimSpace(title) == "" {
		return ""
	}
	return title
}

// tailReadTitle implements the fallback title source: read the history tail
// and prefer the newest session/title event, else the newest human message's
// first text block (truncated). Failures degrade to "" — a missing title must
// never fail the list.
func (a *Agent) tailReadTitle(ctx context.Context, client *Client, sessionID string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	max := 12
	var val sessionHistoryValue
	req := sessionHistoryRequest{SessionID: sessionID, MaxMessages: &max}
	if err := client.Call(ctx, "session.history", req, &val); err != nil {
		return ""
	}
	// Rows are newest-first (beforeSeq pages backwards from the tail).
	lastUser := ""
	for _, row := range val.Events {
		switch row.Event.Type {
		case "session/title":
			var d dshTitleData
			if json.Unmarshal(row.Event.Data, &d) == nil && strings.TrimSpace(d.Title) != "" {
				return truncateTitle(d.Title)
			}
		case "user/message":
			var d dshUserMessageData
			if json.Unmarshal(row.Event.Data, &d) == nil && (d.Source == nil || d.Source.Kind == "user") {
				if text := strings.TrimSpace(joinTextBlocks(d.Content)); text != "" && lastUser == "" {
					lastUser = text
				}
			}
		}
	}
	if lastUser != "" {
		return truncateTitle(lastUser)
	}
	return ""
}

func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		// rune-safe truncation
		r := []rune(s)
		if len(r) > 80 {
			r = r[:80]
		}
		s = string(r)
	}
	return s
}

// GetRunningSessionIDs implements core.RunningSessionLister — the running
// cache refreshed by the last session.list (list enrichment calls this right
// after ListSessions, so the data is fresh).
func (a *Agent) GetRunningSessionIDs(ctx context.Context) (map[string]bool, error) {
	a.running.mu.RLock()
	defer a.running.mu.RUnlock()
	if a.running.set == nil {
		return nil, nil
	}
	out := make(map[string]bool, len(a.running.set))
	for id, running := range a.running.set {
		out[id] = running
	}
	return out, nil
}

// ── SessionRenamer (§4.3.6) ─────────────────────────────────────────────────

// RenameSession maps onto session.rename; the host normalizes and returns the
// accepted title, which is what the refreshed row reports.
func (a *Agent) RenameSession(ctx context.Context, sessionID, title string) (*core.AgentSessionInfo, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var val sessionRenameValue
	if err := client.Call(ctx, "session.rename", sessionRenameRequest{SessionID: sessionID, Title: title}, &val); err != nil {
		return nil, err // *RPCError carries the official title-invalid text verbatim
	}
	info := core.AgentSessionInfo{
		ID:         sessionID,
		Summary:    val.Title,
		ModifiedAt: time.Now(),
	}
	return &info, nil
}

// ── SessionPinner (§4.3.1 ♻️ bridge pin index) ─────────────────────────────

const dshWebPinBackendID = BackendID

// SetSessionPinned implements core.SessionPinner (bridge-owned pin index,
// opencode pattern; summary enrichment stays in go-bridge handlers).
func (a *Agent) SetSessionPinned(_ context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	if pinned && pinnedAt.IsZero() {
		pinnedAt = time.Now().UTC()
	}
	if err := a.pinStore.SetPinned(dshWebPinBackendID, pinScope(directory), sessionID, directory, pinned, pinnedAt); err != nil {
		return nil, err
	}
	if !pinned {
		return nil, nil
	}
	return &core.SessionPin{
		BackendID: dshWebPinBackendID,
		SessionID: sessionID,
		Directory: directory,
		PinnedAt:  pinnedAt.UTC(),
	}, nil
}

// ListPinnedSessions returns identity-only pins across all directories.
func (a *Agent) ListPinnedSessions(_ context.Context) ([]core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	entries := a.pinStore.List(dshWebPinBackendID)
	out := make([]core.SessionPin, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ToPin())
	}
	return out, nil
}

func pinScope(directory string) string {
	d := strings.TrimSpace(directory)
	return d // dsh sessions are not directory-scoped on the wire; keep the hint raw
}

var _ core.SessionPinner = (*Agent)(nil)
var _ core.SessionRenamer = (*Agent)(nil)
var _ core.RunningSessionLister = (*Agent)(nil)
