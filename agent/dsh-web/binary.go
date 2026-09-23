package dshweb

// dsh / npm binary discovery (2026-09-22 install-and-start plan §4). The
// runtime PATH already carries the Mac app's merged CLI dirs
// (RuntimeManager.defaultCLISearchPath: bun, homebrew, /usr/local, pnpm,
// volta, ~/.npm-global) — GUI launches do NOT inherit the user's shell
// PATH, so an nvm-installed dsh is additionally discovered under the newest
// nvm node version. The install record's absolute path wins, so a
// CordCode-prefix install survives GUI PATH gaps across restarts.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// installRecordFile persists the 代装 install (0600, no credentials):
// bin path, npm scope, version, time. Written by the installer; read here
// as a cli_path source so the spawn and the not_detected discrimination
// agree on what "installed" means.
const installRecordFile = "dsh-web-install-record.json"

const installRecordVersion = 1

// Install scopes recorded in the install record.
const (
	InstallScopeUserGlobal     = "user-global"
	InstallScopeCordcodePrefix = "cordcode-prefix"
)

type installRecord struct {
	Version     int    `json:"version"`
	BinPath     string `json:"bin_path"`
	Scope       string `json:"scope"`       // user-global | cordcode-prefix
	DshVersion  string `json:"dsh_version"` // `dsh --version` output, diagnostics only
	InstalledAt string `json:"installed_at"`
}

// findDSHBinary locates the dsh executable: install record → PATH → newest
// nvm version. Empty = not_detected (the port is not consulted).
func findDSHBinary(dataDir string) string {
	if rec := readInstallRecord(dataDir); rec != nil && isExecutableFile(rec.BinPath) {
		return rec.BinPath
	}
	if found, err := exec.LookPath("dsh"); err == nil {
		return found
	}
	return latestNvmBinary("dsh")
}

// findNpmBinary locates npm for the installer: PATH → newest nvm version.
// Empty = the row shows 「需要 Node.js」 instead of 「安装」.
func findNpmBinary() string {
	if found, err := exec.LookPath("npm"); err == nil {
		return found
	}
	return latestNvmBinary("npm")
}

func readInstallRecord(dataDir string) *installRecord {
	if dataDir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dataDir, installRecordFile))
	if err != nil {
		return nil
	}
	var rec installRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil
	}
	return &rec
}

func isExecutableFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// latestNvmBinary finds <name> under the newest nvm node version that has
// it. Mirrors agent/dsh discovery route 4 deliberately: the two packages are
// required to stay import-disjoint, so the scan is duplicated rather than
// shared.
func latestNvmBinary(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	versionsDir := filepath.Join(home, ".nvm", "versions", "node")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return ""
	}
	type nodeVersion struct {
		major, minor, patch int
		dir                 string
	}
	var versions []nodeVersion
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		if major, minor, patch, ok := parseNodeVersion(e.Name()); ok {
			versions = append(versions, nodeVersion{major, minor, patch, e.Name()})
		}
	}
	for i := 0; i < len(versions); i++ {
		best := i
		for j := i + 1; j < len(versions); j++ {
			v, w := versions[j], versions[best]
			if v.major > w.major || (v.major == w.major && v.minor > w.minor) ||
				(v.major == w.major && v.minor == w.minor && v.patch > w.patch) {
				best = j
			}
		}
		versions[i], versions[best] = versions[best], versions[i]
		if candidate := filepath.Join(versionsDir, versions[i].dir, "bin", name); isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

// parseNodeVersion parses "v22.11.0" (leading v, exactly three integer parts).
func parseNodeVersion(s string) (major, minor, patch int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}
