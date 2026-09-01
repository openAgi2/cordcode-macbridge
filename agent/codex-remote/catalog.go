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
)

type catalogThreadRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updatedAt"`
	Cwd       string `json:"cwd"`
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
	cursor := ""
	for {
		params := map[string]any{
			"limit":         pageLimit,
			"sortKey":       "recency_at",
			"sortDirection": "desc",
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

func mapCatalogThread(row catalogThreadRow) core.AgentSessionInfo {
	info := core.AgentSessionInfo{ID: row.ID, Summary: row.Name, Directory: row.Cwd}
	if row.UpdatedAt > 0 {
		info.ModifiedAt = time.Unix(row.UpdatedAt, 0)
	}
	return info
}
