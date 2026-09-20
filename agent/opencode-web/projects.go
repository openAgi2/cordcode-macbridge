package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// projects.go：目录发现走 serve 自己的工程注册表（GET /project）。
//
// 背景（2026-08-19 owner 真机报障 + 活体探针钉死）：1.18 的 GET /session 按
// x-opencode-directory 头按目录返回；不带头的响应是一份陈旧的百条切片
// （实测最新条目停在 7 月 6 日，当天新建的会话完全不在里面）。官方桌面端
// （@opencode-ai/desktop，Electron——本质是 web 程序）按「已打开目录」逐目录
// 拉列表；/project 就是 serve 侧的工程注册表（目录选择器同源）。Desktop 关掉
// 某个 tab 并不会从 GET /project 删除该行（WP：deleted-still-registered）；
// 「当前打开的 6 个」是 Desktop 本地窗口态，不是这份注册表。CordCode 列表
// 跟注册表，不跟 Desktop tab。
// 因此本 backend 的会话目录发现/分组以 /project 为准，不再依赖全局 /session。
//
// 评审 S2 活体：元素是 {id, worktree, vcs, time, sandboxes}——directory 建议
// 读 worktree 字段，never directory/path。v2 /api/location 只解析单个 location
// 不是工程列表；v2 代此面保持 not_supported，不伪造建议。

type ocwProjectEntry struct {
	ID       string   `json:"id"`
	Worktree string   `json:"worktree"`
	Time     *ocwTime `json:"time"`
}

// projectCacheTTL bounds how long the merged project-directory view is
// trusted. Invalidation also rides the SSE catalog signal (session.created/
// deleted → signalCatalogRefresh → invalidateProjectCache)，so a desktop-
// created session surfaces via discovery within signal latency, not just TTL.
const projectCacheTTL = 15 * time.Second

// Membership budgets (phase-1 plan §5). The resolver deadline bounds the
// whole home+window+proof resolution; per-tab proofs get their own shorter
// timeout so a wedged directory instance (Q-type) only starves its own tab.
// Measured baselines (2026-09-20, fixed 4096): by-ID 12–24 ms, scoped
// 15–79 ms, discovery 96–335 ms — the budgets are generous against reality
// while keeping the worst case far below the caller deadlines (recent/
// discovery 8 s, list_projects 5 s).
const (
	membershipResolveTimeout = 3 * time.Second
	tabProofTimeout          = 2 * time.Second
	tabProofConcurrency      = 4
)

func (a *Agent) fetchProjects(ctx context.Context, c *Client) ([]ocwProjectEntry, error) {
	if c.Generation() == generationV2 {
		return nil, core.ErrNotSupported
	}
	raw, err := c.fetchJSON(ctx, "/project", a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	return decodeProjectRegistry(raw)
}

// decodeProjectRegistry parses the verified 1.18.18 GET /project response:
// a BARE ARRAY of row objects (WP-FIX sample-verified at 4a215b0 — three real
// responses, rows {id, worktree, time, sandboxes, vcs?}). Any other top level
// (envelope, null, scalar, object) fails closed — /project never ships a v2
// {data:[…]} shape, so decodeListPayload's envelope tolerance does not apply
// here. Every row must be a JSON object whose required id and worktree are
// non-empty strings; wrong types, nulls, and omissions fail the whole
// registry instead of being trimmed (a silently shortened registry would
// shrink the OD-2 aggregate while looking healthy). Unknown extra fields
// (vcs, time, sandboxes…) are allowed and ignored. worktree "/" is a valid
// row — the serve's global pseudo-project — and is filtered only later by
// the CordCode visibility overlay, never by this decoder.
func decodeProjectRegistry(raw []byte) ([]ocwProjectEntry, error) {
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("opencode-web: project registry must be a bare array (generation-118 verified shape), got: %s", truncateForError(string(raw)))
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("opencode-web: project registry array malformed: %w", err)
	}
	out := make([]ocwProjectEntry, 0, len(rows))
	for i, row := range rows {
		rowBytes := trimSpaceBytes(row)
		if len(rowBytes) == 0 || rowBytes[0] != '{' {
			return nil, fmt.Errorf("opencode-web: project registry row %d must be an object, got: %s", i, truncateForError(string(row)))
		}
		var entry ocwProjectEntry
		if err := json.Unmarshal(row, &entry); err != nil {
			return nil, fmt.Errorf("opencode-web: project registry row %d malformed: %w", i, err)
		}
		if entry.ID == "" {
			return nil, fmt.Errorf("opencode-web: project registry row %d missing required id", i)
		}
		if entry.Worktree == "" {
			return nil, fmt.Errorf("opencode-web: project registry row %d missing required worktree", i)
		}
		out = append(out, entry)
	}
	return out, nil
}

// visibleProjectDir normalizes one worktree for listing purposes: "/" (the
// serve's global pseudo-project), duplicates and paths that no longer exist
// on disk (ghost directories the Mac side already closed/deleted) are not
// listable workspaces — the official desktop would not show them either.
func visibleProjectDir(dir string) (string, bool) {
	clean := filepath.Clean(dir)
	if clean == "" || clean == "/" || clean == "." {
		return "", false
	}
	if !filepath.IsAbs(clean) {
		return "", false
	}
	if info, err := os.Stat(clean); err != nil || !info.IsDir() {
		return "", false
	}
	return clean, true
}

// projectWorktreeDirs returns the deduped membership worktree list used by
// the OD-2 global aggregation (home ∪ proof-authorized tab directories,
// phase-1 plan §3), cached briefly on SUCCESS only (TTL + SSE catalog-signal
// invalidation). A persist/proof-resolution failure is returned as an error —
// a stale cached view must never impersonate this round's registry
// (directive-003). An empty membership set is an empty list, not a fallback
// target. The missing-worktree visibility overlay (visibleProjectDir) applies
// HERE only: / is the serve's global pseudo-project, non-absolute paths,
// duplicates, and worktrees that no longer exist on disk are not listable
// CordCode workspaces. This is a CordCode catalog visibility/safety overlay —
// the serve remains the registry fact owner and rows stay on the server;
// nothing is deleted or rewritten server-side.
func (a *Agent) projectWorktreeDirs(ctx context.Context, c *Client) ([]string, error) {
	a.projectsMu.Lock()
	if a.projectDirs != nil && time.Since(a.projectDirsAt) < projectCacheTTL {
		cached := append([]string(nil), a.projectDirs...)
		a.projectsMu.Unlock()
		return cached, nil
	}
	a.projectsMu.Unlock()

	dirs, source, err := a.loadMemberProjectDirs(ctx, c)
	if err != nil {
		return nil, err
	}
	slog.Info("opencode-web: membership directory list", "source", source, "count", len(dirs), "url", c.baseURL)

	a.projectsMu.Lock()
	a.projectDirs = dirs
	a.projectDirsAt = time.Now()
	a.projectsMu.Unlock()
	return append([]string(nil), dirs...), nil
}

// loadMemberProjectDirs resolves the membership set (phase-1 plan §3):
// Desktop home projects ∪ registered-window active SessionTab directories
// that pass the by-ID proof. Home follows the four-state table (§8); window
// parsing is independent of the home state. Proof failures are per-tab skips
// (logged, never amplified — a usable home-only catalog stays usable); only
// membership parse failures (corrupt persist, over-limit input) are errors.
func (a *Agent) loadMemberProjectDirs(ctx context.Context, c *Client) ([]string, string, error) {
	home, homeSource, err := a.loadHomeProjectDirs(ctx, c)
	if err != nil {
		return nil, "", err
	}
	tabs, err := readDesktopWindowTabs()
	if err != nil {
		return nil, "", err
	}
	var tabDirs []string
	if len(tabs) > 0 {
		tabDirs = a.authorizeTabDirectories(ctx, c, tabs)
	}
	merged := visibleDirsFromWorktrees(append(append([]string(nil), home...), tabDirs...))
	if len(merged) > maxDesktopDedupedDirs {
		return nil, "", fmt.Errorf("opencode-web: membership directory count %d exceeds limit %d", len(merged), maxDesktopDedupedDirs)
	}
	source := homeSource
	if len(tabDirs) > 0 {
		source += "+window-tabs"
	}
	return merged, source, nil
}

// authorizeTabDirectories runs the per-tab by-ID proof with bounded
// concurrency inside the resolver deadline. Tabs whose proof has not
// completed when the deadline hits are skipped (logged) — never an error
// (plan §4: proof failure must not turn a usable catalog global).
func (a *Agent) authorizeTabDirectories(ctx context.Context, c *Client, tabs []desktopSessionTab) []string {
	resolveCtx, cancel := context.WithTimeout(ctx, membershipResolveTimeout)
	defer cancel()

	sem := make(chan struct{}, tabProofConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	dirs := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		wg.Add(1)
		go func(t desktopSessionTab) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-resolveCtx.Done():
				slog.Warn("opencode-web: membership tab proof skipped (resolver deadline)", "sessionId", t.SessionID, "server", t.Server)
				return
			}
			dir, ok := a.proveTabDirectory(resolveCtx, c, t)
			if !ok {
				return
			}
			mu.Lock()
			dirs = append(dirs, dir)
			mu.Unlock()
		}(tab)
	}
	wg.Wait()
	return dirs
}

// proveTabDirectory fetches GET /session/<id> on the TARGET server with the
// tab's own info directory as the routing header (a wedged directory
// instance then only starves its own tab). The proof is type-blind by
// construction: child/archived/out-of-window sessions are all by-ID
// fetchable, so no per-type identity gate exists (plan §3.2). The response
// directory is the server truth; a mismatch with the Desktop-recorded info
// directory is logged and the response wins.
func (a *Agent) proveTabDirectory(ctx context.Context, c *Client, tab desktopSessionTab) (string, bool) {
	proofCtx, cancel := context.WithTimeout(ctx, tabProofTimeout)
	defer cancel()
	routeDir := tab.InfoDirectory
	if routeDir == "" {
		routeDir = a.GetWorkDir()
	}
	code, raw, err := c.doRequest(proofCtx, http.MethodGet, c.endpoint(c.apiPath("/session/")+url.PathEscape(tab.SessionID)), nil, routeDir, true)
	if err != nil {
		slog.Warn("opencode-web: membership tab proof failed (transport)", "sessionId", tab.SessionID, "error", err)
		return "", false
	}
	if code != http.StatusOK {
		slog.Warn("opencode-web: membership tab proof failed (http)", "sessionId", tab.SessionID, "code", code)
		return "", false
	}
	var entry ocwSessionEntry
	if err := json.Unmarshal(unwrapDataEnvelope(raw), &entry); err != nil || entry.ID == "" || entry.ID != tab.SessionID {
		slog.Warn("opencode-web: membership tab proof failed (shape)", "sessionId", tab.SessionID)
		return "", false
	}
	dir := strings.TrimSpace(entry.Directory)
	if dir == "" {
		slog.Warn("opencode-web: membership tab proof response missing directory", "sessionId", tab.SessionID)
		return "", false
	}
	if tab.InfoDirectory != "" && filepath.Clean(tab.InfoDirectory) != filepath.Clean(dir) {
		slog.Warn("opencode-web: membership tab info directory mismatch (server truth wins)",
			"sessionId", tab.SessionID, "infoDirectory", tab.InfoDirectory, "serverDirectory", dir)
	}
	return dir, true
}

// loadHomeProjectDirs resolves the Desktop home sidebar per the four-state
// table (phase-1 plan §8): file/row missing → fallback to the serve registry
// (GET /project); row authoritatively empty → empty home, NO fallback;
// corrupt persist is an error (handled by readDesktopOpenedWorktrees).
func (a *Agent) loadHomeProjectDirs(ctx context.Context, c *Client) ([]string, string, error) {
	state, opened, src, err := readDesktopOpenedWorktrees(c.baseURL)
	if err != nil {
		return nil, "", err
	}
	switch state {
	case desktopHomeRowPresent:
		dirs := visibleDirsFromWorktrees(opened)
		if len(dirs) > 0 {
			return dirs, "desktop-persist:" + src, nil
		}
		// All rows filtered by the visibility overlay (deleted dirs) — the
		// row is still authoritative; fall through to empty home, no fallback.
		return []string{}, "desktop-persist:" + src, nil
	case desktopHomeRowEmpty:
		// Authoritative empty home: an empty list, never a registry fallback.
		return []string{}, "desktop-persist:" + src, nil
	}
	entries, err := a.fetchProjects(ctx, c)
	if err != nil {
		return nil, "", err
	}
	worktrees := make([]string, 0, len(entries))
	for _, entry := range entries {
		worktrees = append(worktrees, entry.Worktree)
	}
	return visibleDirsFromWorktrees(worktrees), "get-project-registry", nil
}

func visibleDirsFromWorktrees(worktrees []string) []string {
	seen := make(map[string]bool, len(worktrees))
	dirs := make([]string, 0, len(worktrees))
	for _, wt := range worktrees {
		clean, ok := visibleProjectDir(wt)
		if !ok || seen[clean] {
			continue
		}
		seen[clean] = true
		dirs = append(dirs, clean)
	}
	sort.Strings(dirs)
	return dirs
}

// invalidateProjectCache is called from the SSE catalog signal path so a
// session.created/deleted on ANY client (the desktop included) refreshes the
// directory view and the discovery fingerprint immediately.
func (a *Agent) invalidateProjectCache() {
	a.projectsMu.Lock()
	a.projectDirs = nil
	a.projectsMu.Unlock()
}

// ListProjectSuggestions implements core.ProjectLister: the iOS directory
// chooser gets the same membership set the catalog aggregation uses (home ∪
// proof-authorized tab directories, phase-1 plan §3.4) — one resolver, one
// cache, so opening the chooser never re-runs a proof fan-out against a
// different view. The caller owns the deadline (go-bridge wraps this in an
// explicit 5 s budget).
func (a *Agent) ListProjectSuggestions(ctx context.Context) ([]core.ProjectSuggestion, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	dirs, err := a.projectWorktreeDirs(ctx, c)
	if err != nil {
		return nil, err
	}
	out := make([]core.ProjectSuggestion, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, core.ProjectSuggestion{
			ID:        dir,
			Directory: dir,
			Name:      filepath.Base(dir),
		})
	}
	return out, nil
}

var _ core.ProjectLister = (*Agent)(nil)
