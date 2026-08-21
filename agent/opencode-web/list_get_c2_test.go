package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// list_get_c2_test.go owns the directive-003 (C2 official list/get) boundary
// tests. Fixtures use the recordingServe so every assertion can be tied to the
// exact wire request (query/header/body) the adapter issued.

// newC2Agent boots an agent over a registry-backed fixture. Every worktree dir
// is created on disk (the missing-worktree overlay only applies to the GLOBAL
// aggregate; the serve registry itself is scripted via /project).
func newC2Agent(t *testing.T, projectWorktrees ...string) (*Agent, *recordingServe) {
	t.Helper()
	base := t.TempDir()
	entries := make([]map[string]any, 0, len(projectWorktrees)+1)
	entries = append(entries, map[string]any{"id": "p_global", "worktree": "/"})
	for i, wt := range projectWorktrees {
		entries = append(entries, map[string]any{
			"id":       fmt.Sprintf("p_%d", i),
			"worktree": wt,
			"vcs":      "git",
			"time":     map[string]any{"created": 1, "updated": 2},
		})
	}
	projects, _ := json.Marshal(entries)
	s := &recordingServe{responses: map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `[]`, // probe shape arbiter (bare array → 1.18)
		"/project":       string(projects),
	}}
	serverURL := s.start(t)
	a, err := New(map[string]any{
		"work_dir":          base,
		"opencode_web_url":  serverURL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return a.(*Agent), s
}

func c2Rows(t *testing.T, dir string, ids ...string) string {
	t.Helper()
	rows := make([]map[string]any, 0, len(ids))
	for i, id := range ids {
		rows = append(rows, map[string]any{
			"id":        id,
			"title":     id,
			"directory": dir,
			"time": map[string]any{
				"created": 1000 + i,
				"updated": 2000 + i,
			},
		})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

// listQuery returns the parsed query of the /session request scoped to dir.
func listQuery(t *testing.T, s *recordingServe, dir string) url.Values {
	t.Helper()
	for _, r := range s.requestsFor("/session") {
		if r.Directory == dir && strings.Contains(r.Query, "roots=") {
			q, err := url.ParseQuery(r.Query)
			if err != nil {
				t.Fatalf("bad query %q: %v", r.Query, err)
			}
			return q
		}
	}
	t.Fatalf("no scoped /session request for %q in %+v", dir, s.requestsFor("/session"))
	return nil
}

func TestOfficialRootsLimit(t *testing.T) {
	base := t.TempDir()
	a, s := newC2Agent(t, base)
	s.dirResponses = map[string]string{"/session|" + base: c2Rows(t, base, "ses_a", "ses_b")}

	rows, err := a.ListSessionsInDirectory(context.Background(), base)
	if err != nil {
		t.Fatalf("scoped listing: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	q := listQuery(t, s, base)
	if q.Get("roots") != "true" {
		t.Fatalf("request must carry roots=true, got %q", q)
	}
	if q.Get("limit") != "100" || core.OpenCodeSessionFetchLimit != 100 {
		t.Fatalf("request must carry the frozen limit=100, got %q", q.Get("limit"))
	}
	if q.Get("directory") == "" {
		t.Fatalf("request must carry the directory scope, got %q", q)
	}
	// Exactly one upstream LIST request (the probe's bare shape GET carries no
	// roots param) — the official UI's omit-limit retry fallback is
	// deliberately NOT copied; a failure is a diagnosable error.
	listCalls := 0
	for _, r := range s.requestsFor("/session") {
		if strings.Contains(r.Query, "roots=") {
			listCalls++
		}
	}
	if listCalls != 1 {
		t.Fatalf("exactly one upstream list request expected, got %d", listCalls)
	}

	// Exactly-at-limit and over-limit: the frozen budget rides every request;
	// row counts pass through honestly (no fabricated truncation).
	big := make([]string, 101)
	for i := range big {
		big[i] = fmt.Sprintf("ses_%03d", i)
	}
	s2dir := t.TempDir()
	a2, s2 := newC2Agent(t, s2dir)
	s2.dirResponses = map[string]string{"/session|" + s2dir: c2Rows(t, s2dir, big[:100]...)}
	if rows, err := a2.ListSessionsInDirectory(context.Background(), s2dir); err != nil || len(rows) != 100 {
		t.Fatalf("exactly-at-limit: rows=%d err=%v", len(rows), err)
	}
	q2 := listQuery(t, s2, s2dir)
	if q2.Get("limit") != "100" || q2.Get("roots") != "true" {
		t.Fatalf("frozen budget must ride every request, got %q", q2)
	}

	// Failure surfaces; no second, limit-less attempt. Baseline = the
	// startup probe's bare shape GET; after the failed scoped call the only
	// addition must be that ONE list request (roots=) — a retry without the
	// limit would show up as a post-baseline request lacking roots=.
	a3dir := t.TempDir()
	a3, s3 := newC2Agent(t, a3dir)
	delete(s3.responses, "/session") // no catch-all → scoped request 404s
	baseline := len(s3.requestsFor("/session"))
	if _, err := a3.ListSessionsInDirectory(context.Background(), a3dir); err == nil {
		t.Fatal("upstream failure must surface as a catalog error")
	}
	after := s3.requestsFor("/session")
	if len(after) != baseline+1 {
		t.Fatalf("exactly one list attempt expected after baseline %d, got %d", baseline, len(after))
	}
	if !strings.Contains(after[len(after)-1].Query, "roots=") {
		t.Fatalf("the single list attempt must carry roots=, got %+v", after[len(after)-1])
	}

	// Malformed top-level shape fails; malformed/required-missing rows fail.
	a4dir := t.TempDir()
	a4, s4 := newC2Agent(t, a4dir)
	s4.dirResponses = map[string]string{"/session|" + a4dir: `{"weird":true}`}
	if _, err := a4.ListSessionsInDirectory(context.Background(), a4dir); err == nil || !strings.Contains(err.Error(), "unrecognized list payload shape") {
		t.Fatalf("malformed top-level must fail, got %v", err)
	}
	s4.dirResponses = map[string]string{"/session|" + a4dir: `[{"title":"no-id"}]`}
	if _, err := a4.ListSessionsInDirectory(context.Background(), a4dir); err == nil || !strings.Contains(err.Error(), "missing required id") {
		t.Fatalf("row missing required id must fail, got %v", err)
	}
}

func TestArchivedHiddenInDefaultListKeptById(t *testing.T) {
	base := t.TempDir()
	a, s := newC2Agent(t, base)
	archived := float64(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli())
	rows, _ := json.Marshal([]map[string]any{
		{"id": "ses_live", "title": "live", "directory": base, "time": map[string]any{"created": archived - 3000, "updated": archived - 2000}},
		{"id": "ses_arch", "title": "arch", "directory": base, "time": map[string]any{"created": archived - 3000, "updated": archived - 1000, "archived": archived}},
	})
	s.dirResponses = map[string]string{"/session|" + base: string(rows)}
	s.responses["/session/ses_arch"] = fmt.Sprintf(`{"id":"ses_arch","title":"arch","directory":%q,"time":{"created":%v,"updated":%v,"archived":%v}}`, base, archived-3000, archived-1000, archived)

	// Global default enumeration hides the archived row.
	global, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("global: %v", err)
	}
	if len(global) != 1 || global[0].ID != "ses_live" {
		t.Fatalf("global default enumeration must hide archived rows, got %+v", global)
	}
	// Scoped default enumeration hides it too.
	scoped, err := a.ListSessionsInDirectory(context.Background(), base)
	if err != nil {
		t.Fatalf("scoped: %v", err)
	}
	if len(scoped) != 1 || scoped[0].ID != "ses_live" {
		t.Fatalf("scoped default enumeration must hide archived rows, got %+v", scoped)
	}
	// By-ID keeps the archived row with its timestamp (hide != delete).
	info, err := a.FetchSessionInfo(context.Background(), "ses_arch")
	if err != nil {
		t.Fatalf("by-ID must keep the archived row: %v", err)
	}
	if !info.ArchivedAt.Equal(time.UnixMilli(int64(archived)).UTC()) {
		t.Fatalf("ArchivedAt = %v", info.ArchivedAt)
	}
}

func TestGlobalAggregatePerWorktree(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "projA")
	dirB := filepath.Join(base, "projB")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	a, s := newC2Agent(t, dirA, dirB)
	// The same stable ID in two buckets is serve-side duplication: exactly one
	// deterministic row may reach the client.
	s.dirResponses = map[string]string{
		"/session|" + dirA: c2Rows(t, dirA, "ses_shared", "ses_old"),
		"/session|" + dirB: c2Rows(t, dirB, "ses_shared", "ses_new"),
	}

	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("global aggregation: %v", err)
	}
	var ids []string
	for _, x := range sessions {
		ids = append(ids, x.ID)
	}
	if len(ids) != 3 {
		t.Fatalf("duplicate stable ID must collapse to one row, got %v", ids)
	}
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	if seen["ses_shared"] != 1 {
		t.Fatalf("ses_shared must appear exactly once, got %v", ids)
	}
	// Stable order: updated DESC (ses_new and ses_old share the newest
	// updated stamp → ID ASC between them), ses_shared oldest last.
	if ids[0] != "ses_new" || ids[1] != "ses_old" || ids[2] != "ses_shared" {
		t.Fatalf("stable ModifiedAt DESC / ID ASC order expected, got %v", ids)
	}
	// Exactly one scoped fetch per registry worktree.
	for _, d := range []string{dirA, dirB} {
		n := 0
		for _, r := range s.requestsFor("/session") {
			if r.Directory == d {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("worktree %s must be fetched exactly once, got %d", d, n)
		}
	}
	// No headerless global LIST fetch: every list request (roots= present)
	// carries directory+roots+limit. The probe's bare shape GET is not a list
	// request and is exempt.
	for _, r := range s.requestsFor("/session") {
		if !strings.Contains(r.Query, "roots=") {
			continue
		}
		q, _ := url.ParseQuery(r.Query)
		if q.Get("directory") == "" || q.Get("roots") != "true" || q.Get("limit") != "100" {
			t.Fatalf("every aggregation request must carry directory+roots+limit, got %+v", r)
		}
	}
}

func TestProjectRegistryAndBucketFailuresSurface(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "projA")
	dirB := filepath.Join(base, "projB")
	for _, d := range []string{dirA, dirB} {
		_ = os.MkdirAll(d, 0o755)
	}

	// Registry failure (404) → catalog failure, never a stale/partial view.
	a1, s1 := newC2Agent(t)
	delete(s1.responses, "/project")
	if _, err := a1.ListSessions(context.Background()); err == nil || !strings.Contains(err.Error(), "project registry") {
		t.Fatalf("registry failure must surface, got %v", err)
	}

	// One bucket failing (missing dirResponse → 404) fails the WHOLE global
	// list — partial success is forbidden.
	a2, s2 := newC2Agent(t, dirA, dirB)
	delete(s2.responses, "/session") // drop the probe catch-all so dirB 404s
	s2.dirResponses = map[string]string{
		"/session|" + dirA: c2Rows(t, dirA, "ses_a"),
		// dirB: no response → 404 bucket failure
	}
	if sessions, err := a2.ListSessions(context.Background()); err == nil {
		t.Fatalf("bucket failure must fail the whole global list, got %+v", sessions)
	}
}

func TestEmptyProjectRegistryIsEmpty(t *testing.T) {
	a, s := newC2Agent(t)
	s.responses["/project"] = `[]`
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("empty registry is the empty catalog, not an error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("empty registry must yield zero rows, got %+v", sessions)
	}
	// Zero /session LIST fallback: no work-dir scoped fetch, no headerless
	// global (the probe's bare shape GET carries no roots param).
	for _, r := range s.requestsFor("/session") {
		if strings.Contains(r.Query, "roots=") {
			t.Fatalf("empty registry must issue ZERO list requests, got %+v", r)
		}
	}
}

func TestMissingWorktreeRule(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "projA")
	ghost := filepath.Join(base, "closed-and-deleted") // registry row, dir gone
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Registry with a live worktree AND a ghost: the GLOBAL aggregate applies
	// the CordCode visibility overlay (ghost excluded), but the serve remains
	// the registry fact owner.
	entries, _ := json.Marshal([]map[string]any{
		{"id": "p_global", "worktree": "/"},
		{"id": "p_ghost", "worktree": ghost},
		{"id": "p_a", "worktree": dirA},
	})
	s := &recordingServe{responses: map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `[]`,
		"/project":       string(entries),
	}}
	serverURL := s.start(t)
	aa, _ := New(map[string]any{
		"work_dir":          base,
		"opencode_web_url":  serverURL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	a := aa.(*Agent)
	t.Cleanup(func() { _ = a.Stop() })
	s.dirResponses = map[string]string{
		"/session|" + dirA:  c2Rows(t, dirA, "ses_a"),
		"/session|" + ghost: c2Rows(t, ghost, "ses_ghost"),
	}

	global, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("global: %v", err)
	}
	if len(global) != 1 || global[0].ID != "ses_a" {
		t.Fatalf("visibility overlay must exclude the ghost worktree from the aggregate, got %+v", global)
	}
	for _, r := range s.requestsFor("/session") {
		if r.Directory == ghost {
			t.Fatalf("ghost worktree must not be fetched in the aggregate, got %+v", r)
		}
	}

	// An EXPLICIT scoped request keeps its scope: the ghost dir is still
	// queried on the serve — never silently redirected or emptied locally.
	scoped, err := a.ListSessionsInDirectory(context.Background(), ghost)
	if err != nil {
		t.Fatalf("scoped ghost request must hit the serve faithfully: %v", err)
	}
	if len(scoped) != 1 || scoped[0].ID != "ses_ghost" {
		t.Fatalf("scoped ghost result must pass through serve truth, got %+v", scoped)
	}

	// By-ID is unaffected by the existence overlay.
	s.responses["/session/ses_ghost"] = fmt.Sprintf(`{"id":"ses_ghost","directory":%q,"time":{"created":1,"updated":2}}`, ghost)
	info, err := a.FetchSessionInfo(context.Background(), "ses_ghost")
	if err != nil || info.ID != "ses_ghost" {
		t.Fatalf("by-ID must ignore the missing-worktree overlay, got %+v err=%v", info, err)
	}
}

func TestListGetDoesNotWriteMessages(t *testing.T) {
	base := t.TempDir()
	a, s := newC2Agent(t, base)
	s.dirResponses = map[string]string{"/session|" + base: c2Rows(t, base, "ses_a")}
	s.responses["/session/ses_a"] = fmt.Sprintf(`{"id":"ses_a","directory":%q,"time":{"created":1,"updated":2}}`, base)

	if _, err := a.ListSessions(context.Background()); err != nil {
		t.Fatalf("global: %v", err)
	}
	if _, err := a.ListSessionsInDirectory(context.Background(), base); err != nil {
		t.Fatalf("scoped: %v", err)
	}
	if _, err := a.FetchSessionInfo(context.Background(), "ses_a"); err != nil {
		t.Fatalf("by-ID: %v", err)
	}
	if _, err := a.ListProjectSuggestions(context.Background()); err != nil {
		t.Fatalf("projects: %v", err)
	}

	// The only timeline feed for this backend is the SSE subscriber's
	// core.Event stream. list/get/project paths must open ZERO event streams
	// and issue ZERO writes — no POST means no turn, no Kernel/EventPublisher
	// input, no messages[] effect is even constructible.
	if posts := countRequests(s, "POST", ""); len(posts) != 0 {
		t.Fatalf("list/get must issue ZERO POSTs, got %+v", posts)
	}
	for _, eventPath := range []string{"/global/event", "/api/event"} {
		if reqs := s.requestsFor(eventPath); len(reqs) != 0 {
			t.Fatalf("list/get must open ZERO event streams at %s, got %+v", eventPath, reqs)
		}
	}
}

func TestCreateBodyEmptyMatchesV1(t *testing.T) {
	// Pin the verified 1.18.18 create contract: POST /session?directory=<dir>
	// with body {} (SessionCreateData: parentID?/title? optional; model is NOT
	// part of create). C3's messageID/agent/model/parts work is NOT in scope.
	agent, serve := newSendAgent(t, map[string]string{
		"/session/ses_new/prompt_async": `{}`,
	})
	withCreateRoute(serve, `{"id":"ses_new"}`)
	agent.SetModel("zhipuai-coding-plan/glm-4.7")
	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.Send("hello", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var create recordedRequest
	for _, r := range countRequests(serve, "POST", "/session") {
		if strings.HasSuffix(r.Path, "/session") {
			create = r
		}
	}
	if create.Body != "{}" {
		t.Fatalf("create body must stay exactly {} (v1 contract), got %q", create.Body)
	}
	if !strings.Contains(create.Query, "directory=") {
		t.Fatalf("create must carry the directory query, got %q", create.Query)
	}
}
