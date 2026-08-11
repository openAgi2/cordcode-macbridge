package gobridge

// claude_desktop_state.go bridges the honest compatibility gap between Claude
// Desktop's private session store and the JSONL-scan catalog.
//
// Claude Desktop 3P (and the older desktop app) keep archive/delete state in
// plain JSON files under
//
//	~/Library/Application Support/Claude-3p/claude-code-sessions/<account>/<org>/
//	~/Library/Application Support/Claude/claude-code-sessions/<account>/<org>/
//
// An archived session is a `local_*.json` file with `isArchived: true` whose
// `cliSessionId` matches the transcript under ~/.claude/projects. A deleted
// session is represented by a `deleted_<uuid>` tombstone; the transcript file
// itself is intentionally left in place by Desktop, so the JSONL scanner would
// otherwise keep showing it forever.
//
// This adapter reads only the app's own plain data files (not app.asar / the
// private Electron API layer) and is explicitly best-effort: if Desktop
// changes its storage layout, the catalog fails safe by listing the transcript
// as before rather than fabricating a result.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	claudeDesktopAppSupportDir3P = "Claude-3p"
	claudeDesktopAppSupportDir   = "Claude"
	claudeDesktopSessionsDir     = "claude-code-sessions"
	claudeDesktopLocalAgentDir   = "local-agent-mode-sessions"
	claudeDesktopLocalPrefix     = "local_"
	claudeDesktopDeletedPrefix   = "deleted_"
)

type claudeDesktopStoreFile struct {
	SessionID    string `json:"sessionId"`
	CLISessionID string `json:"cliSessionId"`
	IsArchived   bool   `json:"isArchived"`
}

// claudeDesktopSessionState is the filtered view of Desktop's session store
// used by the catalog. All keys are plain UUIDs; the `local_` prefix is
// stripped when reading the files.
type claudeDesktopSessionState struct {
	// archivedAtByCLI maps a transcript (CLI) session id to the mtime of the
	// Desktop local_*.json that marked it archived. Desktop does not expose an
	// archive timestamp in the JSON, so the file mtime is the closest honest
	// signal; clients only need non-nil.
	archivedAtByCLI map[string]time.Time
	// localIDToCLI maps a Desktop local session uuid to its CLI transcript id.
	localIDToCLI map[string]string
	// deletedByID contains every tombstoned uuid. Tombstones may carry either
	// the CLI transcript id or the Desktop local session id.
	deletedByID map[string]struct{}
	// modNanoByCLI is the max mtime (UnixNano) of Desktop files that carry the
	// CLI transcript id (local_*.json and deleted_* tombstones). It feeds the
	// catalog fingerprint so archive/unarchive/delete invalidate cached entries.
	modNanoByCLI map[string]int64
}

func newClaudeDesktopSessionState() claudeDesktopSessionState {
	return claudeDesktopSessionState{
		archivedAtByCLI: make(map[string]time.Time),
		localIDToCLI:    make(map[string]string),
		deletedByID:     make(map[string]struct{}),
		modNanoByCLI:    make(map[string]int64),
	}
}

func defaultClaudeAppSupportDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, "Library", "Application Support")
}

func loadClaudeDesktopSessionState(appSupportDir string) claudeDesktopSessionState {
	state := newClaudeDesktopSessionState()
	if strings.TrimSpace(appSupportDir) == "" {
		return state
	}
	for _, root := range []string{
		filepath.Join(appSupportDir, claudeDesktopAppSupportDir3P),
		filepath.Join(appSupportDir, claudeDesktopAppSupportDir),
	} {
		for _, sub := range []string{claudeDesktopSessionsDir, claudeDesktopLocalAgentDir} {
			scanClaudeDesktopStore(filepath.Join(root, sub), &state)
		}
	}
	return state
}

func scanClaudeDesktopStore(base string, state *claudeDesktopSessionState) {
	accounts, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !account.IsDir() {
			continue
		}
		accountPath := filepath.Join(base, account.Name())
		orgs, err := os.ReadDir(accountPath)
		if err != nil {
			continue
		}
		for _, org := range orgs {
			if !org.IsDir() {
				continue
			}
			orgPath := filepath.Join(accountPath, org.Name())
			entries, err := os.ReadDir(orgPath)
			if err != nil {
				continue
			}
			// First pass: local_*.json -> CLI id mapping, archive flags, mtimes.
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || !strings.HasPrefix(name, claudeDesktopLocalPrefix) || !strings.HasSuffix(name, ".json") {
					continue
				}
				localID := strings.TrimSuffix(strings.TrimPrefix(name, claudeDesktopLocalPrefix), ".json")
				path := filepath.Join(orgPath, name)
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				var storeFile claudeDesktopStoreFile
				if err := json.Unmarshal(data, &storeFile); err != nil {
					continue
				}
				info, infoErr := entry.Info()
				if infoErr != nil {
					continue
				}
				modNano := info.ModTime().UnixNano()
				cliID := strings.TrimSpace(storeFile.CLISessionID)
				if cliID == "" {
					continue
				}
				state.localIDToCLI[localID] = cliID
				if modNano > state.modNanoByCLI[cliID] {
					state.modNanoByCLI[cliID] = modNano
				}
				if storeFile.IsArchived {
					state.archivedAtByCLI[cliID] = info.ModTime().UTC()
				}
			}
			// Second pass: deleted_* tombstones. A tombstone may use either the
			// CLI id (direct match) or the Desktop local id (resolved through
			// localIDToCLI when the local JSON still exists).
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || !strings.HasPrefix(name, claudeDesktopDeletedPrefix) {
					continue
				}
				id := strings.TrimPrefix(name, claudeDesktopDeletedPrefix)
				state.deletedByID[id] = struct{}{}
				info, infoErr := entry.Info()
				if infoErr != nil {
					continue
				}
				modNano := info.ModTime().UnixNano()
				if modNano > state.modNanoByCLI[id] {
					state.modNanoByCLI[id] = modNano
				}
				if cliID, ok := state.localIDToCLI[id]; ok {
					state.deletedByID[cliID] = struct{}{}
					if modNano > state.modNanoByCLI[cliID] {
						state.modNanoByCLI[cliID] = modNano
					}
				}
			}
		}
	}
}

func (s *claudeDesktopSessionState) isDeleted(cliID string) bool {
	_, ok := s.deletedByID[cliID]
	return ok
}

func (s *claudeDesktopSessionState) archivedAt(cliID string) time.Time {
	return s.archivedAtByCLI[cliID]
}

func (s *claudeDesktopSessionState) modNano(cliID string) int64 {
	return s.modNanoByCLI[cliID]
}
