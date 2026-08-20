package opencodeweb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// projects.go：目录发现走 serve 自己的工程注册表（GET /project）。
//
// 背景（2026-08-19 owner 真机报障 + 活体探针钉死）：1.18 的 GET /session 按
// x-opencode-directory 头按目录返回；不带头的响应是一份陈旧的百条切片
// （实测最新条目停在 7 月 6 日，当天新建的会话完全不在里面）。官方桌面端
// （@opencode-ai/desktop，Electron——本质是 web 程序）按「已打开目录」逐目录
// 拉列表；/project 就是 serve 侧的工程注册表（桌面端的目录选择器同源）。
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

func (a *Agent) fetchProjects(ctx context.Context, c *Client) ([]ocwProjectEntry, error) {
	if c.Generation() == generationV2 {
		return nil, core.ErrNotSupported
	}
	raw, err := c.fetchJSON(ctx, "/project", a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	out := make([]ocwProjectEntry, 0, len(items))
	for _, item := range items {
		var entry ocwProjectEntry
		if err := json.Unmarshal(item, &entry); err != nil {
			continue
		}
		if entry.ID == "" || entry.Worktree == "" {
			continue
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

// projectWorktreeDirs returns the deduped serve-truth worktree list used by
// the OD-2 global aggregation, cached briefly on SUCCESS only (TTL + SSE
// catalog-signal invalidation). A /project fetch/decode failure is returned
// as an error — a stale cached view must never impersonate this round's
// registry (directive-003). An empty registry is an empty list, not a
// fallback target. The missing-worktree visibility overlay (visibleProjectDir)
// applies HERE only: / is the serve's global pseudo-project, non-absolute
// paths, duplicates, and worktrees that no longer exist on disk are not
// listable CordCode workspaces. This is a CordCode catalog visibility/safety
// overlay — the serve remains the registry fact owner and rows stay on the
// server; nothing is deleted or rewritten server-side.
func (a *Agent) projectWorktreeDirs(ctx context.Context, c *Client) ([]string, error) {
	a.projectsMu.Lock()
	if a.projectDirs != nil && time.Since(a.projectDirsAt) < projectCacheTTL {
		cached := append([]string(nil), a.projectDirs...)
		a.projectsMu.Unlock()
		return cached, nil
	}
	a.projectsMu.Unlock()

	entries, err := a.fetchProjects(ctx, c)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(entries))
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		clean, ok := visibleProjectDir(entry.Worktree)
		if !ok || seen[clean] {
			continue
		}
		seen[clean] = true
		dirs = append(dirs, clean)
	}
	sort.Strings(dirs)

	a.projectsMu.Lock()
	a.projectDirs = dirs
	a.projectDirsAt = time.Now()
	a.projectsMu.Unlock()
	return append([]string(nil), dirs...), nil
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
// chooser gets the serve's own project registry (official parity — the same
// entries the desktop's directory switcher shows).
func (a *Agent) ListProjectSuggestions(ctx context.Context) ([]core.ProjectSuggestion, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := a.fetchProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entries))
	out := make([]core.ProjectSuggestion, 0, len(entries))
	for _, entry := range entries {
		clean, ok := visibleProjectDir(entry.Worktree)
		if !ok || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, core.ProjectSuggestion{
			ID:        entry.ID,
			Directory: clean,
			Name:      filepath.Base(clean),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Directory < out[j].Directory })
	return out, nil
}

var _ core.ProjectLister = (*Agent)(nil)
