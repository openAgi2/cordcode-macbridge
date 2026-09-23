package dshweb

// Session catalog mapping: session.list → core.AgentSessionInfo (design
// §4.3.1): field mapping, subagent/blank filtering, title via the
// session-title projection with the session.history tail-read fallback, and
// the running-flag cache that feeds list enrichment and SessionActivityProbing.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// clientFor returns a Client bound to the resolved instance. The client is
// stateless beyond the base URL, so a fresh one per call is fine; it carries
// the seat's browser-session auth (2026-09-23 plan §4.2).
func (a *Agent) clientFor(ctx context.Context) (*Client, error) {
	inst, err := a.resolver.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	c := NewClient(inst.BaseURL, nil)
	c.SetAuth(a.auth)
	return c, nil
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

// setOne updates one session's flag (host/session-status frames). Creates
// the map on demand so flips before the first list still land.
func (rc *runningCache) setOne(sessionID string, running bool) {
	rc.mu.Lock()
	if rc.set == nil {
		rc.set = map[string]bool{}
	}
	rc.set[sessionID] = running
	rc.mu.Unlock()
}

// ungroupedDirectory is the official sidebar bucket label (zh-CN
// `group.ungrouped` = "未分组"). Official grouping is workspace.sessionIds
// membership, not cwd; iOS sidebars group by session.directory, so stray
// rows must not keep a workspace path or they collapse into that folder.
const ungroupedDirectory = "未分组"

// ── workspace/follow grouping cache ────────────────────────────────────────

// workspaceState caches the workspace/follow stream's grouping truth (the
// retired workspace.list RPC's successor): one baseline per generation, then
// ordered increments. ready=false until the first baseline — ListSessions
// then degrades to cwd rows exactly like the old failed-call path.
type workspaceState struct {
	mu       sync.RWMutex
	ready    bool
	items    []apiWorkspaceView
	archived map[string]struct{}
	// pinned is the registry-global official pin set in official order
	// (most recently pinned first — WorkspaceBaseline.pinnedSessionIds /
	// the {type:'pinned'} increment; S5, OD-1=A).
	pinned []string
}

func (w *workspaceState) applyBaseline(b *workspaceBaseline) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.items = append([]apiWorkspaceView(nil), b.Items...)
	w.archived = map[string]struct{}{}
	for _, id := range b.ArchivedSessionIds {
		if id != "" {
			w.archived[id] = struct{}{}
		}
	}
	w.pinned = nonEmptyIDs(b.PinnedSessionIds)
	w.ready = true
}

// applyPinned replaces the cached official pin set (the {type:'pinned'}
// increment carries the complete set, official order).
func (w *workspaceState) applyPinned(ids []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pinned = nonEmptyIDs(ids)
}

func nonEmptyIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func (w *workspaceState) applyUpsert(view apiWorkspaceView) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.items {
		if w.items[i].WorkspaceID == view.WorkspaceID {
			w.items[i] = view
			return
		}
	}
	w.items = append(w.items, view)
}

func (w *workspaceState) applyRemove(workspaceID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.items {
		if w.items[i].WorkspaceID == workspaceID {
			w.items = append(w.items[:i], w.items[i+1:]...)
			return
		}
	}
}

func (w *workspaceState) applyOrder(ids []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rank := make(map[string]int, len(ids))
	for i, id := range ids {
		rank[id] = i
	}
	sort.SliceStable(w.items, func(i, j int) bool {
		ri, okI := rank[w.items[i].WorkspaceID]
		rj, okJ := rank[w.items[j].WorkspaceID]
		if okI && okJ {
			return ri < rj
		}
		return okI && !okJ
	})
}

func (w *workspaceState) applyArchived(ids []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.archived = map[string]struct{}{}
	for _, id := range ids {
		if id != "" {
			w.archived[id] = struct{}{}
		}
	}
}

// snapshot returns (items, archived, pinned, ready).
func (w *workspaceState) snapshot() ([]apiWorkspaceView, map[string]struct{}, []string, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.ready {
		return nil, nil, nil, false
	}
	return w.items, w.archived, w.pinned, true
}

// ListSessions maps session/list onto AgentSessionInfo rows (design §4.3.1):
// sessionId→id, updatedAt(ms)→modifiedAt, running→cache;
// subagent rows (origin=subagent / parentSessionId set) and blank sessions
// are filtered exactly like the web sidebar. Directory is official workspace
// membership (workspace/follow baseline sessionIds), not cwd — same rule as
// the Mac web sidebar (think.md 2026-08-16). Archived ids get ArchivedAt so
// iOS hides them. The official cursor is an unimplemented reserved seat — one
// full page; the bridge paginates.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var val sessionListValue
	if err := client.Call(ctx, "session/list", map[string]any{"_request": sessionListRequest{}}, &val); err != nil {
		return nil, err
	}

	grouping, archived, haveGrouping := a.groupingFromWorkspace()
	pinnedOrder := map[string]int{}
	if pinned, havePins := a.pinnedFromWorkspace(); havePins {
		for i, id := range pinned {
			pinnedOrder[id] = i
		}
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
			ID:          item.SessionID,
			ModifiedAt:  time.UnixMilli(item.UpdatedAt),
			Directory:   item.Cwd,
			AgentPreset: item.AgentPreset,
		}
		if haveGrouping {
			if path, ok := grouping[item.SessionID]; ok {
				info.Directory = path
			} else {
				info.Directory = ungroupedDirectory
			}
		}
		if _, ok := archived[item.SessionID]; ok {
			info.ArchivedAt = info.ModifiedAt
			if info.ArchivedAt.IsZero() {
				info.ArchivedAt = time.Unix(1, 0).UTC()
			}
		}
		// S5 (OD-1=A): the official pin set marks pinned rows. pinnedAtMillis
		// is a stable order encoding of the official set (most recently pinned
		// first), not a real instant — the official set carries order only.
		if idx, ok := pinnedOrder[item.SessionID]; ok {
			info.PinnedAt = officialPinOrderKey(idx)
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

// groupingFromWorkspace reads the workspace/follow cache. On ready, grouping
// maps a session id to the first workspace path that lists it, and archived
// is the registry-global archive set. Not ready (no baseline yet) returns
// haveGrouping=false so ListSessions keeps cwd (list still works; grouping
// degrades) — the same posture as the retired workspace.list failure path.
func (a *Agent) groupingFromWorkspace() (map[string]string, map[string]struct{}, bool) {
	items, archived, _, ready := a.ws.snapshot()
	if !ready {
		return nil, nil, false
	}
	grouping := make(map[string]string, 16)
	for _, w := range items {
		if w.Path == "" {
			continue
		}
		for _, id := range w.SessionIDs {
			if id == "" {
				continue
			}
			if _, exists := grouping[id]; exists {
				continue
			}
			grouping[id] = w.Path
		}
	}
	return grouping, archived, true
}

// pinnedFromWorkspace returns the cached official pin set in official order
// (most recently pinned first) and whether the baseline has arrived.
func (a *Agent) pinnedFromWorkspace() ([]string, bool) {
	_, _, pinned, ready := a.ws.snapshot()
	return pinned, ready
}

// officialPinOrderKey encodes the official pin-set order (most recently
// pinned first) as a STABLE descending timestamp for the wire's
// pinnedAtMillis sort key (iOS sorts the pinned section by pinnedAt DESC).
// The official set carries order, not instants — the fixed base keeps the
// encoding call-stable so re-fetches never reshuffle the section.
func officialPinOrderKey(index int) time.Time {
	return time.UnixMilli(dshPinnedOrderBaseMs - int64(index))
}

const dshPinnedOrderBaseMs = int64(1_000_000_000_000) // fixed 2001-09-09T01:46:40Z base

// isUngroupedDirectory reports the iOS sidebar bucket for sessions that are
// not on any workspace.sessionIds list. Create must not send it as cwd.
func isUngroupedDirectory(dir string) bool {
	return strings.TrimSpace(dir) == ungroupedDirectory
}

// workspaceIDForDirectory returns the official workspace id whose path matches
// dir, from the workspace/follow cache. Empty when no baseline arrived yet or
// no path matches — caller then sends cwd.
func (a *Agent) workspaceIDForDirectory(dir string) string {
	want := normalizeWorkspacePath(dir)
	if want == "" {
		return ""
	}
	items, _, _, ready := a.ws.snapshot()
	if !ready {
		return ""
	}
	for _, w := range items {
		if normalizeWorkspacePath(w.Path) == want && w.WorkspaceID != "" {
			return w.WorkspaceID
		}
	}
	return ""
}

func normalizeWorkspacePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
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
	head, err := headSeqOf(ctx, client, sessionID)
	if err != nil || head < 0 {
		return ""
	}
	max := 12
	var val sessionPageValue
	req := sessionPageRequest{
		Address:     sessionAddress{Kind: "session", SessionID: sessionID},
		ThroughSeq:  head,
		MaxMessages: &max,
	}
	if err := client.Call(ctx, "session/page", map[string]any{"request": req}, &val); err != nil {
		return ""
	}
	// Records are journal-ordered (oldest→newest, ending at the page cut);
	// scan newest-first to prefer the most recent title/message.
	lastUser := ""
	events := pageEvents(val.Records)
	for i := len(events) - 1; i >= 0; i-- {
		row := events[i]
		switch row.Type {
		case "session/title":
			var d dshTitleData
			if json.Unmarshal(row.Data, &d) == nil && strings.TrimSpace(d.Title) != "" {
				return truncateTitle(d.Title)
			}
		case "user/message":
			var d dshUserMessageData
			if json.Unmarshal(row.Data, &d) == nil && (d.Source == nil || d.Source.Kind == "user") {
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
	if err := client.Call(ctx, "session/rename", map[string]any{"request": sessionRenameRequest{SessionID: sessionID, Title: title}}, &val); err != nil {
		return nil, err // *RPCError carries the official title-invalid text verbatim
	}
	info := core.AgentSessionInfo{
		ID:         sessionID,
		Summary:    val.Title,
		ModifiedAt: time.Now(),
	}
	return &info, nil
}

// ── SessionPinner (S5, OD-1=A: official workspace pin set) ────────────────

const dshWebPinBackendID = BackendID

// SetSessionPinned implements core.SessionPinner against the OFFICIAL
// registry-global pin set (workspace/pinSession / unpinSession — A5 live
// evidence: most-recently-pinned-first order, idempotent unpin,
// session/not-found for unknown ids verbatim). The retired bridge-local
// pinStore path kept iOS and the Mac web sidebar in disagreement; the
// official set is the single truth both surfaces mutate. pinnedAt is
// accepted for the returned pin envelope only — the official set stores
// order, not instants (ListSessions encodes order as pinnedAtMillis).
func (a *Agent) SetSessionPinned(ctx context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("dsh-web: pin session: empty session id")
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	method := "workspace/unpinSession"
	if pinned {
		method = "workspace/pinSession"
	}
	if err := client.Call(ctx, method, map[string]any{
		"request": map[string]any{"sessionId": sessionID}}, nil); err != nil {
		return nil, err
	}
	if !pinned {
		return nil, nil
	}
	if pinnedAt.IsZero() {
		pinnedAt = time.Now().UTC()
	}
	return &core.SessionPin{
		BackendID: dshWebPinBackendID,
		SessionID: sessionID,
		Directory: directory,
		PinnedAt:  pinnedAt.UTC(),
	}, nil
}

// ListPinnedSessions returns the official pin set in official order (most
// recently pinned first). PinnedAt is the stable order encoding — the
// official set carries no instants. Not ready (no baseline yet) returns an
// empty list rather than an error: the pin set is grouping state, and the
// next baseline refresh repopulates it.
func (a *Agent) ListPinnedSessions(_ context.Context) ([]core.SessionPin, error) {
	pinned, ready := a.pinnedFromWorkspace()
	if !ready || len(pinned) == 0 {
		return []core.SessionPin{}, nil
	}
	out := make([]core.SessionPin, 0, len(pinned))
	for i, id := range pinned {
		out = append(out, core.SessionPin{
			BackendID: dshWebPinBackendID,
			SessionID: id,
			PinnedAt:  officialPinOrderKey(i),
		})
	}
	return out, nil
}

// ── SessionArchiver / SessionUnarchiver (S5, OD-1=A: official archive set) ─

// ArchiveSession implements core.SessionArchiver against the official
// registry-global archive set (workspace/archiveSession). stopActivity:true
// mirrors the only official archive form that succeeds for active sessions
// (A5 live evidence: plain archive of an active session fails with
// workspace/session-active verbatim); the bridge's archive verb is the
// user's intent to remove the row, so the turn stops. Errors pass through
// verbatim (session/not-found).
func (a *Agent) ArchiveSession(ctx context.Context, sessionID string, archivedAt time.Time) (*core.AgentSessionInfo, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("dsh-web: archive session: empty session id")
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.Call(ctx, "workspace/archiveSession", map[string]any{
		"request": map[string]any{"sessionId": sessionID, "stopActivity": true}}, nil); err != nil {
		return nil, err
	}
	if archivedAt.IsZero() {
		archivedAt = time.Now().UTC()
	}
	return &core.AgentSessionInfo{
		ID:         sessionID,
		ArchivedAt: archivedAt.UTC(),
		Summary:    a.tailReadTitle(ctx, client, sessionID),
	}, nil
}

// UnarchiveSession implements core.SessionUnarchiver against the official
// archive set (workspace/unarchiveSession — A5 live evidence: idempotent,
// unknown/not-archived ids succeed as no-ops, response is the complete
// resulting set). The row reappears via the ws archived increment → catalog
// refresh on the next list.
func (a *Agent) UnarchiveSession(ctx context.Context, sessionID string) (*core.AgentSessionInfo, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("dsh-web: unarchive session: empty session id")
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.Call(ctx, "workspace/unarchiveSession", map[string]any{
		"request": map[string]any{"sessionId": sessionID}}, nil); err != nil {
		return nil, err
	}
	return &core.AgentSessionInfo{
		ID:      sessionID,
		Summary: a.tailReadTitle(ctx, client, sessionID),
	}, nil
}

var _ core.SessionPinner = (*Agent)(nil)
var _ core.SessionRenamer = (*Agent)(nil)
var _ core.SessionArchiver = (*Agent)(nil)
var _ core.SessionUnarchiver = (*Agent)(nil)
var _ core.RunningSessionLister = (*Agent)(nil)

// SupportsRecentCatalog opts dsh-web into the `catalogView:"recent"` session-list
// view (core.RecentCatalogProvider). Its official session.list is a global
// recency-ordered catalog; the bridge filters to root sessions (parentID empty).
func (a *Agent) SupportsRecentCatalog() bool { return true }
