package opencodeweb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 2026-08-19 owner 真机报障的回归钉：桌面端（同 serve）在 cordcode-ios 目录新建
// 会话，iOS opencode-web 列表不可见、重启也不可见。活体根因：1.18 GET /session
// 按 x-opencode-directory 头按目录返回，不带头的全局响应是一份陈旧百条切片
// （当天新会话完全缺席）。列表必须走 /project 工程注册表逐目录限定拉取。

func mustDir(t *testing.T, base, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func sessionJSON(id, dir string, updated time.Time) string {
	b, _ := json.Marshal([]map[string]any{{
		"id":        id,
		"title":     id,
		"directory": dir,
		"time":      map[string]any{"created": updated.UnixMilli() - 1000, "updated": updated.UnixMilli()},
	}})
	return string(b)
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestListSessionsUnionOverProjectRegistryNotStaleGlobal(t *testing.T) {
	base := t.TempDir()
	dirA := mustDir(t, base, "projA")
	dirB := mustDir(t, base, "projB")
	ghost := filepath.Join(base, "closed-and-deleted")

	newSessionAt := time.Now()
	oldSessionAt := newSessionAt.Add(-48 * time.Hour)

	projects, _ := json.Marshal([]map[string]any{
		{"id": "p_global", "worktree": "/"},
		{"id": "p_ghost", "worktree": ghost},
		{"id": "p_dup", "worktree": dirA},
		{"id": "p_a", "worktree": dirA},
		{"id": "p_b", "worktree": dirB},
	})
	a, serve := newDataAgent(t, map[string]string{
		"/project": string(projects),
		// Stale headerless global: the newest entry is days old, and the
		// poison row proves the union never consumes this shape.
		"/session": sessionJSON("ses_stale_global", base, oldSessionAt),
	}, base)
	serve.dirResponses = map[string]string{
		"/session|" + dirA: sessionJSON("ses_old_a", dirA, oldSessionAt),
		"/session|" + dirB: sessionJSON("ses_new_desktop", dirB, newSessionAt),
	}

	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var ids []string
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	if len(ids) != 2 {
		t.Fatalf("union must list exactly the two registry directories, got %v", ids)
	}
	// Newest first: the same-day desktop session must be the FIRST row — the
	// stale global could never surface it (owner symptom).
	if ids[0] != "ses_new_desktop" || ids[1] != "ses_old_a" {
		t.Fatalf("union order must be newest-first, got %v", ids)
	}
	for _, s := range sessions {
		if s.Directory == ghost || s.Directory == "/" {
			t.Fatalf("ghost/global directories must not leak into the union: %+v", s)
		}
	}
}

func TestListSessionsInDirectoryScopesByHeaderAndFilters(t *testing.T) {
	base := t.TempDir()
	dirA := mustDir(t, base, "projA")
	dirB := mustDir(t, base, "projB")

	a, serve := newDataAgent(t, map[string]string{"/session": `[]`}, base)
	serve.dirResponses = map[string]string{
		"/session|" + dirA: `[{"id":"ses_in_a","directory":` + jsonStr(dirA) + `,"time":{"updated":1787116000000}},` +
			`{"id":"ses_leaked_b","directory":` + jsonStr(dirB) + `,"time":{"updated":1787115000000}}]`,
	}

	sessions, err := a.ListSessionsInDirectory(context.Background(), dirA)
	if err != nil {
		t.Fatalf("ListSessionsInDirectory: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "ses_in_a" {
		t.Fatalf("scoped list must keep only the requested directory's rows, got %+v", sessions)
	}
	reqs := serve.requestsFor("/session")
	if len(reqs) == 0 || reqs[len(reqs)-1].Directory != dirA {
		t.Fatalf("scoped fetch must carry the x-opencode-directory header, got %+v", reqs)
	}
}

func TestProjectSuggestionsDropsGlobalGhostAndDuplicates(t *testing.T) {
	base := t.TempDir()
	dirA := mustDir(t, base, "projA")
	ghost := filepath.Join(base, "deleted")

	projects, _ := json.Marshal([]map[string]any{
		{"id": "p_global", "worktree": "/"},
		{"id": "p_ghost", "worktree": ghost},
		{"id": "p_a", "worktree": dirA},
		{"id": "p_dup", "worktree": dirA},
	})
	a, _ := newDataAgent(t, map[string]string{"/project": string(projects)}, base)

	suggestions, err := a.ListProjectSuggestions(context.Background())
	if err != nil {
		t.Fatalf("ListProjectSuggestions: %v", err)
	}
	if len(suggestions) != 1 {
		t.Fatalf("registry must dedupe and drop global/ghost worktrees, got %+v", suggestions)
	}
	if suggestions[0].Directory != dirA || suggestions[0].Name != "projA" {
		t.Fatalf("suggestion shape mismatch: %+v", suggestions[0])
	}
}
