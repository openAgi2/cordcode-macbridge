package opencodeweb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseDesktopHomeStateMatchesServerURL(t *testing.T) {
	blob := []byte(`{
		"server": "{\"projects\":{\"http://127.0.0.1:4096\":[{\"worktree\":\"/Users/samples/cordcode-macbridge\",\"expanded\":true},{\"worktree\":\"/Users/samples/Chat\",\"expanded\":true}],\"local\":[{\"worktree\":\"/Users/samples/Chat\"}]}}"
	}`)
	state, got := parseDesktopHomeState(blob, "http://127.0.0.1:4096/")
	if state != desktopHomeRowPresent || len(got) != 2 || got[0] != "/Users/samples/cordcode-macbridge" || got[1] != "/Users/samples/Chat" {
		t.Fatalf("4096 home state=%d open-set = %v", state, got)
	}
	state, local := parseDesktopHomeState(blob, "local")
	if state != desktopHomeRowPresent || len(local) != 1 || local[0] != "/Users/samples/Chat" {
		t.Fatalf("local home state=%d open-set = %v", state, local)
	}
	if state, miss := parseDesktopHomeState(blob, "http://127.0.0.1:9999"); state != desktopHomeRowMissing || miss != nil {
		t.Fatalf("unknown URL must be row-missing without inheriting another scope, state=%d dirs=%v", state, miss)
	}
}

func TestReadDesktopOpenedWorktreesUsesInjectedPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.global.dat")
	body := []byte(`{"server":"{\"projects\":{\"http://127.0.0.1:4096\":[{\"worktree\":\"/tmp/opened-a\"},{\"worktree\":\"/tmp/opened-b\"}]}}"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	prev := desktopPersistLookup
	desktopPersistLookup = func() []string { return []string{path} }
	t.Cleanup(func() { desktopPersistLookup = prev })

	state, got, src, err := readDesktopOpenedWorktrees("http://127.0.0.1:4096")
	if err != nil {
		t.Fatal(err)
	}
	if state != desktopHomeRowPresent || src != path {
		t.Fatalf("state=%d source = %q", state, src)
	}
	if len(got) != 2 {
		t.Fatalf("opened = %v", got)
	}
}

func TestProjectWorktreeDirsPrefersDesktopOpenSetOverRegistry(t *testing.T) {
	openedA := t.TempDir()
	openedB := t.TempDir()
	extra := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.global.dat")
	// persist URL filled after serve starts
	prev := desktopPersistLookup
	t.Cleanup(func() { desktopPersistLookup = prev })

	agent, _ := newC2Agent(t, openedA, openedB, extra)
	c, err := agent.clientFor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(map[string]any{
		"server": mustJSONString(t, map[string]any{
			"projects": map[string]any{
				c.baseURL: []map[string]any{
					{"worktree": openedA, "expanded": true},
					{"worktree": openedB, "expanded": true},
				},
			},
		}),
	})
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	desktopPersistLookup = func() []string { return []string{path} }

	agent.invalidateProjectCache()
	dirs, err := agent.projectWorktreeDirs(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("home list must be Desktop open-set (2), not GET /project (3+global), got %v", dirs)
	}
	got := map[string]bool{dirs[0]: true, dirs[1]: true}
	if !got[openedA] || !got[openedB] {
		t.Fatalf("dirs = %v", dirs)
	}
	if got[extra] {
		t.Fatalf("registry-only extra worktree must not appear: %v", dirs)
	}
}

func mustJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
