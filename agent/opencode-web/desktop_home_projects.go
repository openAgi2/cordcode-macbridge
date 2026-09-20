package opencodeweb

import (
	"encoding/json"
	"fmt"
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

// desktopHomeState is the four-state home parse (phase-1 plan §8). The states
// must NOT fold: an authoritatively empty row is an empty home (no registry
// fallback), and JSON damage is a membership parse failure (catalog error).
type desktopHomeState int

const (
	// desktopHomeFileMissing: no persist file at all → home falls back to the
	// serve registry (GET /project). Window parsing stays independent.
	desktopHomeFileMissing desktopHomeState = iota
	// desktopHomeRowMissing: file exists but has no row for this server URL →
	// fallback to GET /project (no Desktop install / different machine).
	desktopHomeRowMissing
	// desktopHomeRowEmpty: row exists and is authoritatively empty → home is
	// empty, NO fallback.
	desktopHomeRowEmpty
	// desktopHomeRowPresent: row exists with entries.
	desktopHomeRowPresent
	// desktopHomeCorrupt: file or row JSON damaged → catalog error, no partial.
	desktopHomeCorrupt
)

// parseDesktopHomeState extracts the home state for serverURL from one
// opencode.global.dat blob. The "server" value is itself a JSON string.
func parseDesktopHomeState(global []byte, serverURL string) (desktopHomeState, []string) {
	want := normalizeDesktopServerURL(serverURL)
	if want == "" || len(global) == 0 {
		return desktopHomeFileMissing, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(global, &root); err != nil {
		return desktopHomeCorrupt, nil
	}
	raw, ok := root["server"]
	if !ok || len(raw) == 0 {
		return desktopHomeRowMissing, nil
	}
	var inner []byte
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		inner = []byte(asString)
	} else {
		inner = raw
	}
	var persist desktopServerPersist
	if err := json.Unmarshal(inner, &persist); err != nil {
		return desktopHomeCorrupt, nil
	}
	if persist.Projects == nil {
		return desktopHomeRowMissing, nil
	}
	rows, ok := persist.Projects[want]
	if !ok {
		return desktopHomeRowMissing, nil
	}
	dirs := worktreesFromStored(rows)
	if len(dirs) == 0 {
		return desktopHomeRowEmpty, nil
	}
	return desktopHomeRowPresent, dirs
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

// readDesktopOpenedWorktrees returns Desktop's home state for this serve
// URL: the parsed state, the home worktrees (row states only), the persist
// path used, and an error only for corrupt/over-limit persist (membership
// parse failure → catalog error upstream). File/row-missing states carry no
// error — the caller decides the GET /project fallback.
func readDesktopOpenedWorktrees(serverURL string) (desktopHomeState, []string, string, error) {
	want := normalizeDesktopServerURL(serverURL)
	for _, path := range desktopPersistLookup() {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return desktopHomeFileMissing, nil, "", err
		}
		if len(raw) > maxDesktopPersistFile {
			return desktopHomeCorrupt, nil, "", fmt.Errorf("opencode-web: desktop home persist %s exceeds %d bytes (limit)", filepath.Base(path), maxDesktopPersistFile)
		}
		state, dirs := parseDesktopHomeState(raw, want)
		switch state {
		case desktopHomeCorrupt:
			return desktopHomeCorrupt, nil, "", fmt.Errorf("opencode-web: desktop home persist %s malformed", filepath.Base(path))
		case desktopHomeRowPresent, desktopHomeRowEmpty:
			return state, dirs, path, nil
		}
	}
	return desktopHomeFileMissing, nil, "", nil
}
