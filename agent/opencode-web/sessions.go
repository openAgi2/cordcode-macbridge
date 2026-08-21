package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ocwModelRef is the {id, providerID} model reference shape carried by
// sessions and sends (design §3.6).
type ocwModelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
}

func (m *ocwModelRef) qualified() string {
	if m == nil || m.ID == "" {
		return ""
	}
	if m.ProviderID == "" {
		return m.ID
	}
	return m.ProviderID + "/" + m.ID
}

// ocwSessionEntry mirrors the GET /session element (design §3.6). The
// top-level tokens field is deliberately NOT typed here: usage reads the
// message-level info.tokens per the official web formula (§3.3) — the two
// shapes differ (S1: top level has no `total`) and must not be mixed.
type ocwSessionEntry struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Directory string       `json:"directory"`
	Time      *ocwTime     `json:"time"`
	Model     *ocwModelRef `json:"model"`
}

type ocwTime struct {
	Created  float64 `json:"created"`
	Updated  float64 `json:"updated"`
	Archived float64 `json:"archived"`
}

func (t *ocwTime) updatedAt() time.Time {
	if t == nil || t.Updated <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(t.Updated)).UTC()
}

// archivedAt maps the official time.archived timestamp (Schema.Finite epoch
// milliseconds). Live 1.18: the default list keeps returning archived rows —
// the bridge surfaces this as archivedAtMillis and clients hide them.
func (t *ocwTime) archivedAt() time.Time {
	if t == nil || t.Archived <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(t.Archived)).UTC()
}

// decodeListPayload accepts both list shapes: the 1.18 bare array and the v2
// {data:[…]} envelope (design §3.2). Any OTHER top-level shape is malformed
// and fails — it must never masquerade as an empty catalog (directive-003
// C2). Unknown extra fields are ignored everywhere downstream (§4.3.1:
// 未核实前不过滤).
func decodeListPayload(raw []byte) ([]json.RawMessage, error) {
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("opencode-web: unrecognized list payload shape: %w", err)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("opencode-web: unrecognized list payload shape (not a bare array, no data envelope): %s", truncateForError(string(raw)))
	}
	return envelope.Data, nil
}

// ListSessions implements core.Agent as the OD-2 aggregate: one scoped
// GET /session?roots=true&limit=100 per /project registry worktree, merged
// newest-first. The headerless global /session is a stale bounded slice
// (2026-08-19 live-pinned) and must never back a catalog — it is not issued
// at all. Registry truth is serve-owned: a /project failure is a catalog
// failure (no stale cache), an empty registry is an empty list (no
// work-dir/global fallback), and any per-worktree bucket failure fails the
// whole aggregate (no partial success). A CordCode visibility overlay
// (missing-worktree rule, projects.go) excludes non-listable worktrees from
// the AGGREGATE only — it never rewrites scoped requests or by-ID reads.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	dirs, err := a.projectWorktreeDirs(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("opencode-web: session catalog unavailable (project registry): %w", err)
	}
	if len(dirs) == 0 {
		// Empty registry IS the empty catalog — zero /session requests.
		return []core.AgentSessionInfo{}, nil
	}
	type bucket struct {
		idx   int
		items []core.AgentSessionInfo
	}
	buckets := make([]bucket, len(dirs))
	errs := make([]error, len(dirs))
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Add(1)
		go func(idx int, dir string) {
			defer wg.Done()
			items, err := a.listSessionsWith(ctx, c, dir)
			if err != nil {
				errs[idx] = fmt.Errorf("directory %s: %w", dir, err)
				return
			}
			buckets[idx] = bucket{idx: idx, items: items}
		}(i, dir)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			// Any bucket failure fails the whole global list — never a
			// partial catalog (directive-003 OD-2).
			return nil, fmt.Errorf("opencode-web: global session aggregation failed: %w", err)
		}
	}
	seen := make(map[string]bool)
	out := make([]core.AgentSessionInfo, 0)
	for _, b := range buckets {
		for _, info := range b.items {
			if seen[info.ID] {
				// Same stable ID in two buckets is serve-side duplication;
				// keep exactly one deterministic row (bucket order is the
				// sorted registry order) — never emit a duplicate session.
				continue
			}
			seen[info.ID] = true
			out = append(out, info)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].ModifiedAt, out[j].ModifiedAt
		if !mi.Equal(mj) {
			return mi.After(mj)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ListSessionsInDirectory implements core.DirectorySessionLister: a scoped
// fetch for one directory (the serve returns that directory's sessions,
// newest first). iOS per-directory list requests MUST go through this — the
// global merged view plus a post-filter would both over-fetch and inherit
// registry staleness for brand-new sessions.
func (a *Agent) ListSessionsInDirectory(ctx context.Context, directory string) ([]core.AgentSessionInfo, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return a.ListSessions(ctx)
	}
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	return a.listSessionsWith(ctx, c, directory)
}

var _ core.DirectorySessionLister = (*Agent)(nil)

func (a *Agent) listSessionsWith(ctx context.Context, c *Client, dir string) ([]core.AgentSessionInfo, error) {
	// Official v1 SDK shape (session-load.ts:19-26):
	// GET /session?directory=<path>&roots=true&limit=<budget> — one bounded
	// root fetch per directory. The limit reuses the frozen
	// core.OpenCodeSessionFetchLimit budget (C2: no second fetch number). A
	// request failure is a diagnosable catalog error — the official UI's
	// retry-without-limit compat fallback is deliberately NOT copied.
	path := fmt.Sprintf("%s?roots=true&limit=%d", c.apiPath("/session"), core.OpenCodeSessionFetchLimit)
	raw, err := c.fetchJSON(ctx, path, dir)
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	out := make([]core.AgentSessionInfo, 0, len(items))
	for i, item := range items {
		var entry ocwSessionEntry
		if err := json.Unmarshal(item, &entry); err != nil {
			// Required row shape malformed → fail. Unknown extra FIELDS are
			// still ignored (Unmarshal skips them); silent row trimming would
			// fake a successful catalog (directive-003).
			return nil, fmt.Errorf("opencode-web: session catalog row %d malformed: %w", i, err)
		}
		if entry.ID == "" {
			return nil, fmt.Errorf("opencode-web: session catalog row %d missing required id", i)
		}
		if entry.Time != nil && entry.Time.Archived > 0 {
			// OD-1: archived rows are hidden from DEFAULT enumeration (global
			// aggregate and scoped default list alike). This is CordCode
			// catalog visibility, not a server delete — by-ID reads keep the
			// row (fetchSessionInfo applies no filter).
			continue
		}
		info := core.AgentSessionInfo{
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
		out = append(out, info)
	}
	// Stable order for cursor pagination: newest first, ID as tiebreak.
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].ModifiedAt, out[j].ModifiedAt
		if !mi.Equal(mj) {
			return mi.After(mj)
		}
		return out[i].ID < out[j].ID
	})
	if dir != "" {
		// Scoped fetch safety: keep only the requested directory's rows even
		// if the serve ignored the header (post-conditions over trust). The
		// requested scope is never rewritten — a local os.Stat miss does not
		// redirect the query (missing-worktree rule applies to the GLOBAL
		// aggregate only).
		scoped := make([]core.AgentSessionInfo, 0, len(out))
		for _, info := range out {
			if strings.TrimSpace(info.Directory) == "" || filepath.Clean(info.Directory) == filepath.Clean(dir) {
				scoped = append(scoped, info)
			}
		}
		return scoped, nil
	}
	return out, nil
}

// unwrapDataEnvelope descends a v2 {"data": …} envelope when present,
// returning the inner payload (v1 flat payloads pass through unchanged).
func unwrapDataEnvelope(raw []byte) []byte {
	var probe struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe.Data) == 0 {
		return raw
	}
	return probe.Data
}

// fetchSessionInfo fetches one session via GET /session/:id. The directory
// header uses the current work dir (the serve does not hard-scope single
// session GETs by it); the response carries the session's own directory for
// subsequent reads.
func (a *Agent) fetchSessionInfo(ctx context.Context, c *Client, sessionID string) (*ocwSessionEntry, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/session/")+sessionID, a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	var entry ocwSessionEntry
	if err := json.Unmarshal(unwrapDataEnvelope(raw), &entry); err != nil {
		return nil, err
	}
	if entry.ID == "" {
		return nil, fmt.Errorf("opencode-web: session payload missing id")
	}
	return &entry, nil
}
