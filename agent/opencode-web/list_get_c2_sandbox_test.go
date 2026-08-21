package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSandboxC2ListGetCycle is the c2-list-get review-fix regression: the
// strict decoder, OD-2 aggregation, OD-1 archive rule, and by-ID reads are
// proven against a REAL isolated 1.18.18 serve — create/list/archive/get
// must match official server state end to end (canonical §6.2).
//
// Registry semantics pinned by WP: the serve's own cwd (non-worktree) folds
// into the global pseudo-project worktree "/", and only a git worktree with
// ≥1 commit created as a sibling becomes its own registry row (realpath).
// The cycle therefore targets a harness-owned git worktree.
//
//	OCW_SANDBOX_URL=http://127.0.0.1:4398 OCW_SANDBOX_USER=gatea OCW_SANDBOX_PASS=gatea-pass \
//	  go test ./agent/opencode-web -run TestSandboxC2ListGetCycle -count=1 -v
func TestSandboxC2ListGetCycle(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("OCW_SANDBOX_URL"))
	if base == "" {
		t.Skip("set OCW_SANDBOX_URL (and OCW_SANDBOX_USER/PASS) to run the sandbox C2 regression")
	}
	root := strings.TrimSpace(os.Getenv("OCW_SANDBOX_ROOT"))
	if root == "" {
		root = "/tmp/ocw-gate-a-20260820"
	}
	// Harness-owned git worktree (unique per run) — the registry only gives a
	// non-cwd directory its own row when it is a git worktree with a commit.
	wt := filepath.Join(root, fmt.Sprintf("c2reg-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if out, err := exec.Command("git", "init", "-q", wt).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("c2 regression\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	for _, args := range [][]string{
		{"git", "-C", wt, "-c", "user.name=gatea", "-c", "user.email=gatea@invalid", "add", "."},
		{"git", "-C", wt, "-c", "user.name=gatea", "-c", "user.email=gatea@invalid", "commit", "-qm", "c2 regression init"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	realWt, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatalf("realpath: %v", err)
	}

	a, err := New(map[string]any{
		"work_dir":          realWt,
		"opencode_web_url":  base,
		"opencode_web_user": os.Getenv("OCW_SANDBOX_USER"),
		"opencode_web_pass": os.Getenv("OCW_SANDBOX_PASS"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	t.Cleanup(func() { _ = agent.Stop() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Real /project must decode through the strict bare-array decoder.
	suggestions, err := agent.ListProjectSuggestions(ctx)
	if err != nil {
		t.Fatalf("strict /project decode on real serve: %v", err)
	}
	found := false
	for _, s := range suggestions {
		if s.Directory == realWt {
			found = true
		}
	}
	if !found {
		t.Fatalf("git worktree %s must be a project suggestion row, got %+v", realWt, suggestions)
	}

	// Create through the official route, scoped to the worktree.
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	code, raw, err := c.doRequest(ctx, "POST", c.endpoint(c.apiPath("/session")+"?directory="+realWt), map[string]any{}, realWt, true)
	if err != nil || code >= 400 {
		t.Fatalf("create: code=%d err=%v body=%s", code, err, truncateForError(string(raw)))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("create decode: %v %s", err, truncateForError(string(raw)))
	}
	sid := created.ID
	t.Logf("created %s in %s", sid, realWt)

	// Global aggregate sees the fresh session (registry → bucket → merge).
	seenGlobal := false
	sessions, err := agent.ListSessions(ctx)
	if err != nil {
		t.Fatalf("global list: %v", err)
	}
	for _, s := range sessions {
		if s.ID == sid {
			seenGlobal = true
		}
	}
	if !seenGlobal {
		t.Fatalf("global aggregate must surface the fresh session %s (got %d rows)", sid, len(sessions))
	}
	// Scoped list stays scoped and also carries it.
	seenScoped := false
	scoped, err := agent.ListSessionsInDirectory(ctx, realWt)
	if err != nil {
		t.Fatalf("scoped list: %v", err)
	}
	for _, s := range scoped {
		if s.ID == sid {
			seenScoped = true
			if filepath.Clean(s.Directory) != filepath.Clean(realWt) {
				t.Fatalf("scoped row directory = %q, want %q", s.Directory, realWt)
			}
		}
	}
	if !seenScoped {
		t.Fatalf("scoped list must surface %s", sid)
	}

	// Archive: default enumeration hides (OD-1), by-ID keeps the row.
	archivedAt := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	info, err := agent.ArchiveSession(ctx, sid, archivedAt)
	if err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if info.ArchivedAt.IsZero() {
		t.Fatalf("archive echo must carry time.archived: %+v", info)
	}
	sessions, err = agent.ListSessions(ctx)
	if err != nil {
		t.Fatalf("global list after archive: %v", err)
	}
	for _, s := range sessions {
		if s.ID == sid {
			t.Fatalf("archived %s must be hidden from the default global list (OD-1)", sid)
		}
	}
	scoped, err = agent.ListSessionsInDirectory(ctx, realWt)
	if err != nil {
		t.Fatalf("scoped list after archive: %v", err)
	}
	for _, s := range scoped {
		if s.ID == sid {
			t.Fatalf("archived %s must be hidden from the default scoped list (OD-1)", sid)
		}
	}
	byID, err := agent.FetchSessionInfo(ctx, sid)
	if err != nil {
		t.Fatalf("by-ID must keep the archived row (hide != delete): %v", err)
	}
	if byID.ArchivedAt.IsZero() {
		t.Fatalf("by-ID archived row must carry ArchivedAt: %+v", byID)
	}

	// Delete: response + list absence + by-ID 404 convergence (§6.10).
	if err := agent.DeleteSession(ctx, sid); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	sessions, err = agent.ListSessions(ctx)
	if err != nil {
		t.Fatalf("global list after delete: %v", err)
	}
	for _, s := range sessions {
		if s.ID == sid {
			t.Fatalf("deleted %s must be absent from the global list", sid)
		}
	}
	if _, err := agent.FetchSessionInfo(ctx, sid); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("by-ID after delete must 404, got %v", err)
	}
	t.Logf("C2 sandbox cycle converged: create → list(global+scoped) → archive(hidden/by-ID kept) → delete(gone/404) on %s", base)
}
