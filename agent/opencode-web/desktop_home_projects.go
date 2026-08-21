package opencodeweb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// desktop_home_projects.go reads the official OpenCode Desktop home project
// list. Official web (home-controller.ts / server.tsx createServerProjects)
// does NOT use GET /project as the sidebar: that registry is every worktree
// the serve has ever seen (31 rows live). The home sidebar is Persist.global
// "server".projects[serverURL] — locally opened tabs. Closing a tab writes
// recentlyClosed and removes the row here; it does not DELETE /project.
//
// There is no HTTP equivalent. CordCode therefore reads the same persist
// file Desktop writes (read-only) so iOS lists the same open worktrees.

// desktopPersistLookup is overridden in tests.
var desktopPersistLookup = defaultDesktopPersistPaths

func defaultDesktopPersistPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, "Library/Application Support/ai.opencode.desktop/opencode.global.dat"),
		filepath.Join(home, "Library/Application Support/ai.opencode.desktop.beta/opencode.global.dat"),
	}
}

type desktopServerPersist struct {
	Projects map[string][]desktopStoredProject `json:"projects"`
}

type desktopStoredProject struct {
	Worktree string `json:"worktree"`
}

func normalizeDesktopServerURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// parseDesktopOpenedWorktrees extracts opened worktrees for serverURL from a
// Desktop opencode.global.dat blob. The "server" value is itself a JSON string.
func parseDesktopOpenedWorktrees(global []byte, serverURL string) []string {
	want := normalizeDesktopServerURL(serverURL)
	if want == "" || len(global) == 0 {
		return nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(global, &root); err != nil {
		return nil
	}
	raw, ok := root["server"]
	if !ok || len(raw) == 0 {
		return nil
	}
	var inner []byte
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		inner = []byte(asString)
	} else {
		inner = raw
	}
	var persist desktopServerPersist
	if err := json.Unmarshal(inner, &persist); err != nil || persist.Projects == nil {
		return nil
	}
	if rows, ok := persist.Projects[want]; ok {
		return worktreesFromStored(rows)
	}
	return nil
}

func worktreesFromStored(rows []desktopStoredProject) []string {
	out := make([]string, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		wt := strings.TrimSpace(row.Worktree)
		if wt == "" {
			continue
		}
		if _, dup := seen[wt]; dup {
			continue
		}
		seen[wt] = struct{}{}
		out = append(out, wt)
	}
	return out
}

// readDesktopOpenedWorktrees returns Desktop's currently opened worktrees for
// this serve URL, plus the persist path used. Empty means "not found".
func readDesktopOpenedWorktrees(serverURL string) (dirs []string, source string) {
	want := normalizeDesktopServerURL(serverURL)
	for _, path := range desktopPersistLookup() {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		opened := parseDesktopOpenedWorktrees(raw, want)
		if len(opened) == 0 {
			continue
		}
		return opened, path
	}
	return nil, ""
}
