package opencodeweb

// desktop_window_tabs.go reads the official OpenCode Desktop window registry
// (phase-1 plan §3.1/§7, docs/2026-09-20-opencode-web-off-home-membership-
// phase1-plan.md). Only opencode.settings.windowIds construct registered
// window paths — never a glob. Each opencode.window.<id>.dat carries
// double-JSON-encoded values (the outer JSON's values are JSON strings),
// exactly like the verified 1.18.31 fixtures in testdata/desktop-persist-1.18.31.
//
// Failure domains (plan §4): a corrupt registered window file or an
// over-limit input is a membership parse failure → catalog error (fail
// closed, no partial publish). A registered id whose file is absent is an
// empty window, not an error — mirroring upstream restoreMainWindows.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Input hard limits (plan §7 — real distribution measured 2026-09-20:
// 1 window, 3 tabs/window, 8 deduped dirs, 105,660B largest persist file,
// ≈107KB total; the 8 MiB total aligns with the upstream persist cache cap
// CACHE_MAX_BYTES in packages/app/src/utils/persist.ts). Exceeding any limit
// is a membership parse failure with an explicit resource error.
const (
	maxDesktopWindowIDs     = 8
	maxDesktopTabsPerWindow = 32
	maxDesktopPersistFile   = 2 << 20 // 2 MiB per persist file
	// maxDesktopPersistTotal is the aggregate budget across settings+global+
	// all window files (plan §7, aligned with the upstream persist cache cap).
	// The window reader enforces settings+windows against total-minus-one-file
	// so the global.dat read (≤ maxDesktopPersistFile) fits inside the budget.
	maxDesktopPersistTotal  = 8 << 20
	maxDesktopDedupedDirs    = 64
	desktopSettingsFilename  = "opencode.settings"
	desktopWindowFilePrefix  = "opencode.window."
	desktopWindowFileSuffix  = ".dat"
)

// desktopSettingsLookup is overridden in tests (same injection pattern as
// desktopPersistLookup). The window files live next to the settings file.
var desktopSettingsLookup = defaultDesktopSettingsPaths

func defaultDesktopSettingsPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, "Library/Application Support/ai.opencode.desktop", desktopSettingsFilename),
		filepath.Join(home, "Library/Application Support/ai.opencode.desktop.beta", desktopSettingsFilename),
	}
}

// desktopSessionTab is one active SessionTab from a registered window, plus
// the directory the Desktop UI recorded for it (tabs.info; may be empty when
// the info join misses — the proof response is the server truth then).
type desktopSessionTab struct {
	Server        string
	SessionID     string
	InfoDirectory string
}

// desktopWindowTabFile mirrors the double-encoded window persist shape:
//
//	{"tabs": "<json array>", "tabs.info": "<json object>", …}
type desktopWindowTabFile struct {
	Tabs     json.RawMessage `json:"tabs"`
	TabsInfo json.RawMessage `json:"tabs.info"`
}

type desktopTabInfo struct {
	Title     string `json:"title"`
	Directory string `json:"directory"`
}

// desktopTab is the decoded tabs[] row. DraftTab rows carry no session and
// never grant membership (plan §6); unknown types are skipped.
type desktopTab struct {
	Type      string `json:"type"`
	Server    string `json:"server"`
	SessionID string `json:"sessionId"`
}

// tabKey reproduces the official key algorithm exactly (upstream
// packages/app/src/context/tabs.tsx:47 + utils/session-route.ts:5-7):
//
//	server + "\n" + "/server/" + base64URL_nopad(server) + "/session/" + id
//
// The base64 variant is URL-safe WITHOUT padding (upstream encode.ts
// base64Encode: '+'→'-', '/'→'_', '=' stripped) — "sidecar" encodes to
// c2lkZWNhcg, NOT c2lkZWNhcg==. Standard base64 breaks the exact join.
func tabKey(server, sessionID string) string {
	return server + "\n" + "/server/" + base64.RawURLEncoding.EncodeToString([]byte(server)) + "/session/" + sessionID
}

// windowDataFile mirrors upstream windowDataFile (windows.ts): sanitize the
// window id by replacing anything outside [a-zA-Z0-9._-] with '-'.
func windowDataFile(windowID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, windowID)
	return desktopWindowFilePrefix + safe + desktopWindowFileSuffix
}

// readDesktopWindowTabs returns the active SessionTabs of every REGISTERED
// window (opencode.settings.windowIds, no glob). Missing settings file →
// no windows (nil, not an error). A registered window whose file is absent
// is an empty window. Corrupt persist or over-limit input is an error.
func readDesktopWindowTabs() ([]desktopSessionTab, error) {
	var settingsPath string
	for _, path := range desktopSettingsLookup() {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			settingsPath = path
			break
		}
	}
	if settingsPath == "" {
		return nil, nil
	}
	windowIDs, err := readDesktopWindowIDs(settingsPath)
	if err != nil || len(windowIDs) == 0 {
		return nil, err
	}

	root := filepath.Dir(settingsPath)
	// The settings file itself counts against the aggregate budget (plan §7:
	// settings+global+all windows ≤ maxDesktopPersistTotal); the per-window
	// check below reserves one file's worth so global.dat (≤
	// maxDesktopPersistFile) fits inside the budget.
	total := int64(0)
	if info, err := os.Stat(settingsPath); err == nil {
		total += info.Size()
	}
	tabs := make([]desktopSessionTab, 0, len(windowIDs))
	for _, id := range windowIDs {
		path := filepath.Join(root, windowDataFile(id))
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue // registered id with no file = empty window (upstream restore semantics)
		}
		if err != nil {
			return nil, fmt.Errorf("opencode-web: desktop window persist %s: %w", windowDataFile(id), err)
		}
		if len(raw) > maxDesktopPersistFile {
			return nil, fmt.Errorf("opencode-web: desktop window persist %s exceeds %d bytes (limit)", windowDataFile(id), maxDesktopPersistFile)
		}
		total += int64(len(raw))
		if total > maxDesktopPersistTotal-maxDesktopPersistFile {
			return nil, fmt.Errorf("opencode-web: desktop persist total exceeds %d bytes (limit)", maxDesktopPersistTotal)
		}
		windowTabs, err := parseDesktopWindowTabs(raw)
		if err != nil {
			return nil, fmt.Errorf("opencode-web: desktop window persist %s: %w", windowDataFile(id), err)
		}
		tabs = append(tabs, windowTabs...)
	}
	return tabs, nil
}

// readDesktopWindowIDs parses opencode.settings (plain JSON, not
// double-encoded) and validates windowIds: non-empty strings only, hard
// limit maxDesktopWindowIDs.
func readDesktopWindowIDs(settingsPath string) ([]string, error) {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("opencode-web: desktop settings: %w", err)
	}
	if len(raw) > maxDesktopPersistFile {
		return nil, fmt.Errorf("opencode-web: desktop settings exceeds %d bytes (limit)", maxDesktopPersistFile)
	}
	var settings struct {
		WindowIDs []string `json:"windowIds"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("opencode-web: desktop settings malformed: %w", err)
	}
	out := make([]string, 0, len(settings.WindowIDs))
	for _, id := range settings.WindowIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		out = append(out, id)
	}
	if len(out) > maxDesktopWindowIDs {
		return nil, fmt.Errorf("opencode-web: desktop windowIds %d exceeds limit %d", len(out), maxDesktopWindowIDs)
	}
	return out, nil
}

// parseDesktopWindowTabs decodes one window file: the tabs / tabs.info
// values are themselves JSON strings (double encoding). tabs.info keys are
// official tabKeys; the join is exact. A missing info entry leaves
// InfoDirectory empty (the proof response directory is the fallback truth).
func parseDesktopWindowTabs(raw []byte) ([]desktopSessionTab, error) {
	var file desktopWindowTabFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("malformed window persist: %w", err)
	}
	var tabRows []desktopTab
	if len(file.Tabs) > 0 {
		inner, err := decodeDoubleEncoded(file.Tabs)
		if err != nil {
			return nil, fmt.Errorf("tabs: %w", err)
		}
		if err := json.Unmarshal(inner, &tabRows); err != nil {
			return nil, fmt.Errorf("tabs malformed: %w", err)
		}
	}
	if len(tabRows) > maxDesktopTabsPerWindow {
		return nil, fmt.Errorf("window has %d tabs (limit %d)", len(tabRows), maxDesktopTabsPerWindow)
	}
	var info map[string]desktopTabInfo
	if len(file.TabsInfo) > 0 {
		inner, err := decodeDoubleEncoded(file.TabsInfo)
		if err != nil {
			return nil, fmt.Errorf("tabs.info: %w", err)
		}
		if err := json.Unmarshal(inner, &info); err != nil {
			return nil, fmt.Errorf("tabs.info malformed: %w", err)
		}
	}
	out := make([]desktopSessionTab, 0, len(tabRows))
	for _, row := range tabRows {
		if row.Type != "session" {
			continue // DraftTab / unknown types never grant membership
		}
		if row.Server == "" || row.SessionID == "" {
			return nil, fmt.Errorf("session tab missing server or sessionId")
		}
		tab := desktopSessionTab{Server: row.Server, SessionID: row.SessionID}
		if entry, ok := info[tabKey(row.Server, row.SessionID)]; ok {
			tab.InfoDirectory = entry.Directory
		}
		out = append(out, tab)
	}
	return out, nil
}

// decodeDoubleEncoded unwraps one persist value: the outer JSON stores it as
// a JSON string whose content is itself JSON (verified fixture shape). A
// raw object/array is tolerated (defensive; the official writer always
// stringifies).
func decodeDoubleEncoded(raw json.RawMessage) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return []byte(asString), nil
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return raw, nil
	}
	return nil, fmt.Errorf("value is neither JSON string nor object")
}
