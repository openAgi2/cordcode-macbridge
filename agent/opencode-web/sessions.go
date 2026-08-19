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
	Created float64 `json:"created"`
	Updated float64 `json:"updated"`
}

func (t *ocwTime) updatedAt() time.Time {
	if t == nil || t.Updated <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(t.Updated)).UTC()
}

// decodeListPayload accepts both list shapes: the 1.18 bare array and the v2
// {data:[…]} envelope (design §3.2). Unknown extra fields are ignored
// everywhere downstream (§4.3.1: 未核实前不过滤).
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
		return nil, err
	}
	return envelope.Data, nil
}

// ListSessions implements core.Agent as the MERGED view over the serve's
// project registry (projects.go): one scoped GET /session per worktree,
// newest-first (2026-08-19 live-pinned: the headerless global /session is a
// stale bounded slice — newest entry weeks old, same-day sessions missing —
// so it must never back a catalog). This is what the discovery fingerprint
// and iOS global requests consume. On the v2 generation (no /project) it
// degrades to the current-directory fetch.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	dirs := a.projectDirectories(ctx)
	if len(dirs) == 0 {
		// v2 (no /project) or a cold registry error: one scoped fetch with
		// the current work dir rather than the stale headerless global.
		return a.listSessionsWith(ctx, c, a.GetWorkDir())
	}
	type bucket struct {
		idx   int
		items []core.AgentSessionInfo
	}
	buckets := make([]bucket, len(dirs))
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Add(1)
		go func(idx int, dir string) {
			defer wg.Done()
			items, err := a.listSessionsWith(ctx, c, dir)
			if err == nil {
				buckets[idx] = bucket{idx: idx, items: items}
			}
		}(i, dir)
	}
	wg.Wait()
	total := 0
	for _, b := range buckets {
		total += len(b.items)
	}
	out := make([]core.AgentSessionInfo, 0, total)
	for _, b := range buckets {
		out = append(out, b.items...)
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
	// Official v1 SDK shape: GET /session?directory=<path> (SessionListData.
	// query.directory). doRequest appends the query param (plus the redundant
	// x-opencode-directory header) whenever a directory is in scope.
	raw, err := c.fetchJSON(ctx, c.apiPath("/session"), dir)
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	out := make([]core.AgentSessionInfo, 0, len(items))
	for _, item := range items {
		var entry ocwSessionEntry
		if err := json.Unmarshal(item, &entry); err != nil {
			// Unknown/malformed fields must not drop the whole row.
			continue
		}
		if entry.ID == "" {
			continue
		}
		info := core.AgentSessionInfo{
			ID:         entry.ID,
			Summary:    entry.Title,
			Directory:  entry.Directory,
			ModifiedAt: entry.Time.updatedAt(),
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
		// if the serve ignored the header (post-conditions over trust).
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
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	if entry.ID == "" {
		return nil, fmt.Errorf("opencode-web: session payload missing id")
	}
	return &entry, nil
}
