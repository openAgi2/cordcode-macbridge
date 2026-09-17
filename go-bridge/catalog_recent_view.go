package gobridge

// catalog_recent_view.go implements the `catalogView:"recent"` session-list view
// (docs/2026-09-17-session-list-chatgpt-parity-implementation-plan.md §3.1/§6.1).
//
// The recent view is a GLOBAL root-session catalog ordered by authoritative
// recency (updatedAtMillis DESC, id ASC tie-break), paged over the same
// cursor-v2 frozen-snapshot machinery as the declared catalogs. It is NOT the
// fair-home page (per-directory K slice) re-sorted: fair-home deliberately
// truncates per directory, so it can never back a global recency feed.
//
// Fail-closed parameter matrix (§6.1):
//
//	catalogView omitted / "standard" → existing semantics, untouched.
//	catalogView "recent" + directory non-empty → invalid_params.
//	catalogView "recent" + rootsOnly != true    → invalid_params (client must
//	declare root-only explicitly; the view is root-only by definition).
//
// Scope identity: catalogView participates in catalogWireScope so a recent
// snapshot is never reused for (or cursor-chained into) a standard view and
// vice versa (§3.1「catalogView 必须进入 scope identity」).

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"

	"log/slog"
)

// catalogViewStandard / catalogViewRecent are the only legal `catalogView` wire
// values. Anything else is invalid_params (fail closed, no silent standard
// downgrade).
const (
	catalogViewStandard = "standard"
	catalogViewRecent   = "recent"
)

// parseCatalogView validates the `catalogView` request parameter together with
// the other list_sessions scope parameters. Returns ("standard", nil) when the
// parameter is absent. The returned view feeds both the handler branch and the
// snapshot scope identity.
func parseCatalogView(view, directory string, rootsOnly bool) (string, *WireError) {
	switch strings.TrimSpace(view) {
	case "":
		return catalogViewStandard, nil
	case catalogViewStandard:
		return catalogViewStandard, nil
	case catalogViewRecent:
		if strings.TrimSpace(directory) != "" {
			return "", &WireError{
				Code:    "invalid_params",
				Message: "catalogView \"recent\" does not accept a directory parameter",
			}
		}
		if !rootsOnly {
			return "", &WireError{
				Code:    "invalid_params",
				Message: "catalogView \"recent\" requires rootsOnly:true",
			}
		}
		return catalogViewRecent, nil
	default:
		return "", &WireError{
			Code:    "invalid_params",
			Message: fmt.Sprintf("unknown catalogView %q (expected \"standard\" or \"recent\")", view),
		}
	}
}

// recentCatalogScope builds the wire-snapshot scope for the recent view of one
// backend. The view rides the scope identity, so recent and standard snapshots
// and their cursor chains are fully isolated (§6.1「snapshot scope identity
// 增加 catalog view」).
func recentCatalogScope(backendID string) catalogWireScope {
	return catalogWireScope{BackendID: backendID, Global: true, RootsOnly: true, View: catalogViewRecent}
}

// prepareRecentSnapshot normalizes a global enriched wire list into the recent
// view: drop non-root rows (non-empty parentID), then sort by authoritative
// recency (updatedAtMillis DESC, id ASC tie-break — the same stable order the
// cursor anchor relies on). The input is the backend's own enriched global
// snapshot; the output is the frozen recent catalog a pageV2 call slices.
//
// parentID is the only child marker on the wire (opencode/dsh-web carry it;
// claude fork children are already hidden upstream of this point). Rows without
// the field are root by definition.
func prepareRecentSnapshot(maps []map[string]interface{}) []map[string]interface{} {
	roots := make([]map[string]interface{}, 0, len(maps))
	for _, m := range maps {
		if parent, _ := m["parentID"].(string); strings.TrimSpace(parent) != "" {
			continue
		}
		if parent, _ := m["parentId"].(string); strings.TrimSpace(parent) != "" {
			continue
		}
		roots = append(roots, m)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		ti, _ := roots[i]["updatedAtMillis"].(int64)
		tj, _ := roots[j]["updatedAtMillis"].(int64)
		if ti != tj {
			return ti > tj
		}
		idi, _ := roots[i]["id"].(string)
		idj, _ := roots[j]["id"].(string)
		return idi < idj
	})
	return roots
}

// rejectCrossViewCursor guards the v1-compat standard views (generic
// paginateSessionList / claude catalog) against a cursor minted by the recent
// view. A v2 cursor replayed into a standard view must fail closed with
// cursor_stale — the v1 path would otherwise silently degrade to page 0
// (plan §3.1「禁止把 standard cursor 用到 recent 视图」cuts both ways).
func rejectCrossViewCursor(cursor string) *WireError {
	if cursor == "" {
		return nil
	}
	if _, _, err := decodeListCursorV2(cursor); err == nil {
		return retryableSessionError("cursor_stale", "recent-view cursor cannot be replayed against the standard catalog view")
	}
	return nil
}

// recentHandleListSessions serves `catalogView:"recent"` for every backend that
// opts in via core.RecentCatalogProvider (plan §6.1). One shared wire path:
//
//	provider gate → backend-specific global enriched builder →
//	prepareRecentSnapshot (root filter + recency order) → cursor-v2 frozen
//	snapshot paging (pageV2Context on a recent-view scope).
//
// The backend branch only picks the global-list builder — the recent semantics
// (ordering, cursor, EOF) are bridge-owned and identical across backends, so
// production logic stays capability/interface-driven, not backend-ID-driven.
func (h *Handlers) recentHandleListSessions(conn Connection, msg WireMessage, agent core.Agent) {
	provider, ok := agent.(core.RecentCatalogProvider)
	if !ok || !provider.SupportsRecentCatalog() {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "not_supported",
			Message: "backend does not support the recent session catalog view",
		})
		return
	}

	ctx, cancel := context.WithTimeout(h.ctx, catalogRequestTimeout)
	defer cancel()
	limit := h.effectiveSessionListLimit(extractPositiveInt(msg, "limit"))
	if limit > 1000 {
		limit = 1000
	}
	cursor := extractStringParam(msg, "cursor")
	metrics := newSessionLoadRequestMetrics(conn, msg)
	mctx := core.WithSessionLoadMetrics(ctx, metrics.context())
	backendID := agentBackendID(agent)
	started := time.Now()

	// Backend-specific global enriched builder. Each returns the same shape its
	// standard view uses (enrich + pin overlay already applied), so the recent
	// view never invents a second enrichment pipeline.
	buildGlobal := func() ([]map[string]interface{}, error) {
		switch {
		case usesCodexWorkspaceCatalog(agent):
			// thread/list 富 catalog（codex / codex-web / codex-remote 共用 seam）。
			// dir="" 取全局；500 行官方上限在 driver 内编码并如实反映为 hasMore=false。
			return h.buildCodexEnrichedSessions(mctx, msg.BackendID, "")
		case agent.Name() == "grokbuild":
			return h.buildGrokEnrichedSessions(mctx, msg.BackendID)
		case agent.Name() == "claudecode":
			// claude catalog：全局快照（fork children 已隐藏），enrich + pin overlay。
			all := h.claudeSessions.list("", metrics.context())
			all = h.enrichSessionStatesForList(all, agent, h.getRunningMap(mctx, agent))
			h.overlayPinnedState(all, "claudecode")
			return all, nil
		default:
			// generic / dsh-web / opencode-web：全局 ListSessions → wire → enrich → pin。
			sessions, err := agent.ListSessions(mctx)
			if err != nil {
				return nil, err
			}
			wire := sessionsToWire(sessions)
			if _, scoped := agent.(core.DirectorySessionLister); scoped {
				wire = filterSessionsMissingWorkspace(wire)
			}
			wire = h.enrichSessionStatesForList(wire, agent, h.getRunningMap(mctx, agent))
			h.overlayPinnedState(wire, backendID)
			return wire, nil
		}
	}

	cache := h.openCodeCatalogWireCache()
	scope := recentCatalogScope(backendID)
	result, staleErr, err := cache.pageV2Context(ctx, scope, cursor, limit, func() ([]map[string]interface{}, error) {
		global, err := buildGlobal()
		if err != nil {
			return nil, err
		}
		return prepareRecentSnapshot(global), nil
	})
	if err != nil {
		metrics.sendResult(conn, msg.RequestID, nil, listWireError(err))
		return
	}
	if staleErr != nil {
		slog.Info("list_sessions recent cursor_stale",
			"backend", backendID,
			"cursor_present", cursor != "",
			"duration_ms", time.Since(started).Milliseconds(),
		)
		metrics.sendResult(conn, msg.RequestID, nil, staleErr)
		return
	}
	if ws, ok := result["sessions"].([]map[string]interface{}); ok {
		slog.Info("list_sessions recent",
			"backend", backendID,
			"limit", limit,
			"cursor_present", cursor != "",
			"result_count", len(ws),
			"next_cursor_present", result["hasMore"] == true,
			"duration_ms", time.Since(started).Milliseconds(),
		)
		metrics.resultCount = len(ws)
	}
	// recent 视图无 directoryTotals 语义（§6.1：仅 standard fair-home 发送）。
	metrics.sendResult(conn, msg.RequestID, result, nil)
}
