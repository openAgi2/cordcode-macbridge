package codexremote

// catalog.go — bounded Desktop thread/list for this kind only.
//
// Proven on this controller stream: thread/list returns
// {data, nextCursor, backwardsCursor}. This adapter paginates nextCursor
// up to a hard cap and maps id/name/updatedAt/cwd. It does not inherit
// Codex Web daemon catalog cache, workspace-root filters, or JSONL fallback.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	// The official app-server clamps thread/list at 100. Using that maximum
	// halves Remote envelope round trips for the authoritative 400+ row catalog.
	catalogListPageSize = 100
	catalogListMaxItems = 500
	catalogListHeadMax  = 25
	// Owner product decision (2026-09-26): the session list only shows
	// sessions whose last update falls inside this window — the full catalog
	// (all providers, all directories) had grown to 400+ rows and reads as
	// clutter. Older sessions stay resumable by ID and remain searchable after
	// opening, they just leave the list. Pinned rows are exempt: pinning is
	// the explicit "keep this visible" gesture (the official Pinned section
	// behaves the same way).
	catalogActivityWindow = 10 * 24 * time.Hour
)

type catalogThreadRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updatedAt"`
	Cwd       string `json:"cwd"`
	// Thread-section decoration (upstream v2 thread_data.rs: section +
	// sectionEnteredAt, serde defaults). Used to carry pin state on list rows
	// (session pinning plan §3.1); unsectioned threads decode zero values.
	Section *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"section"`
	SectionEnteredAt int64 `json:"sectionEnteredAt"`
}

func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	return a.FetchThreadList(ctx, "")
}

func (a *Agent) FetchThreadList(ctx context.Context, dir string) ([]core.AgentSessionInfo, error) {
	return a.listThreads(ctx, dir, 0, true)
}

func (a *Agent) FetchThreadListHead(ctx context.Context, dir string, limit int) ([]core.AgentSessionInfo, error) {
	if limit <= 0 || limit > catalogListHeadMax {
		limit = catalogListHeadMax
	}
	return a.listThreads(ctx, dir, limit, false)
}

func (a *Agent) listThreads(ctx context.Context, dir string, limit int, followCursor bool) ([]core.AgentSessionInfo, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	pageLimit := catalogListPageSize
	if limit > 0 && limit < pageLimit {
		pageLimit = limit
	}
	out := make([]core.AgentSessionInfo, 0, pageLimit)
	seen := map[string]struct{}{}
	cutoff := time.Now().Add(-catalogActivityWindow).Unix()
	cursor := ""
	for {
		params := map[string]any{
			"limit":         pageLimit,
			"sortKey":       "recency_at",
			"sortDirection": "desc",
			// codex 0.158+ thread/list defaults an omitted provider filter to
			// the Desktop's currently active model provider, so a provider
			// switch (e.g. a local routing gateway) would hide every session
			// recorded under earlier providers. Present-but-empty is the
			// documented "all providers" shape (ThreadListParams docs).
			"modelProviders": []string{},
		}
		if dir != "" {
			params["cwd"] = []string{dir}
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
			if _, dup := seen[row.ID]; dup {
				continue
			}
			seen[row.ID] = struct{}{}
			if row.UpdatedAt < cutoff && !rowPinned(row) {
				continue
			}
			out = append(out, mapCatalogThread(row))
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
			if len(out) >= catalogListMaxItems {
				return out, nil
			}
		}
		if !followCursor {
			return out, nil
		}
		next := strings.Trim(strings.TrimSpace(string(parsed.NextCursor)), `"`)
		if next == "" || next == "null" || next == cursor {
			return out, nil
		}
		cursor = next
	}
}

// rowPinned reports whether a catalog row belongs to the official Pinned
// section (the section decoration plus a positive entered-at timestamp).
func rowPinned(row catalogThreadRow) bool {
	return row.Section != nil && row.Section.ID == pinnedThreadSectionID && row.SectionEnteredAt > 0
}

func mapCatalogThread(row catalogThreadRow) core.AgentSessionInfo {
	info := core.AgentSessionInfo{ID: row.ID, Summary: row.Name, Directory: row.Cwd}
	if row.UpdatedAt > 0 {
		info.ModifiedAt = time.Unix(row.UpdatedAt, 0)
	}
	// Pinned-section membership rides the row itself (official decoration);
	// the authoritative pinned SET still comes from ListPinnedSessions.
	if rowPinned(row) {
		info.PinnedAt = time.Unix(row.SectionEnteredAt, 0)
	}
	return info
}

// SupportsRecentCatalog opts codex-remote into the `catalogView:"recent"` session-list
// view (core.RecentCatalogProvider). thread/list is a global recency-ordered root
// catalog; the official 500-row read ceiling is surfaced as hasMore=false by the
// bridge (plan §6.1) rather than hidden here.
func (a *Agent) SupportsRecentCatalog() bool { return true }
