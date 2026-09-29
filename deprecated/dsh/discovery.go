package dsh

// Harness probe-only discovery (owner product directive v2, 2026-08-15).
//
// The product form for EVERY backend is probe-reuse-not-started: MacBridge
// detects what the user already installed, reuses the credentials the tool
// itself configured, and reports honestly when nothing is there. MacBridge
// NEVER installs, compiles, or downloads anything for the user.
//
// DSH acquisition routes, first hit wins:
//
//  1. PATH `dsh-jsonrpc-agent` — the user explicitly installed the SDK demo
//     package or the pip wheel's exe onto PATH. Explicit installs win.
//  2. User-global `@deepseek-ai/dsh` (THE real-user form: `npm i -g
//     @deepseek-ai/dsh` + `dsh web`). The npm CLI tree carries the whole
//     runtime family (cordis / dsh-agent / dsh-llm / dsh-session / subagent /
//     sandbox stack / schemastery…) but NOT the SDK stdio layer — that layer
//     is vendored in agent/dsh/vendor and glued in via a shadow node_modules
//     (see shadow.go). Spawn: `node <shadow>/…/dsh-sdk-jsonrpc-demo/lib/bin.js`
//     with DSH_CORDIS_CONFIG pointing at the driver's cordis.yml.
//  3. pip wheel: PATH `dsh-jsonrpc-agent-pkg-<plat>` or the Python Resolution
//     API (`import deepseek_harness_runtime; bundled_runtime_path()`).
//  4. nvm: `~/.nvm/versions/node/<newest>/bin/dsh-jsonrpc-agent`.
//  5. Source checkout — dev-only, opt-in via DSH_DEV_SOURCE_ROOT. Never part
//     of the product chain; the source repo is reference material.
//
// Credential layering (unchanged, mirrors DSH's own trust order): MacBridge
// provider key (explicit) > $DSH_HOME/.credentials.yaml (written by `dsh`
// Web UI) > $DSH_HOME/.env. $DSH_HOME honors the env override, default ~/.dsh.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dshHome resolves the harness home: $DSH_HOME or ~/.dsh.
func dshHome() string {
	if v := strings.TrimSpace(os.Getenv("DSH_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dsh")
}

// pkgExeName maps GOOS/GOARCH onto the wheel's executable-name scheme
// (platforms.json: macos-arm64, linux-x64, linux-arm64). Empty when the
// combination has no shipped wheel.
func pkgExeName() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "dsh-jsonrpc-agent-pkg-macos-arm64"
	case "linux/amd64":
		return "dsh-jsonrpc-agent-pkg-linux-x64"
	case "linux/arm64":
		return "dsh-jsonrpc-agent-pkg-linux-arm64"
	default:
		return ""
	}
}

// discoveredRuntime captures WHICH harness installation was found and HOW to
// spawn it.
type discoveredRuntime struct {
	exe     string // direct executable (route 1/3/4)
	source  string // diagnostics label
	nodeBin string // node for script launches (routes 2/5)

	// Route 2 (user-global npm dsh): the probed install + resolved versions.
	global *globalDshInstall

	// Route 5 (dev source checkout): spawn via `node --import tsx <bin.ts>`
	// with cwd at the checkout root (the gate0-verified launch shape).
	srcRoot string
	script  string // bin.ts (source) or demo lib/bin.js (route 2)
}

// ── route 1: explicit PATH installs ─────────────────────────────────────────

func discoverInstalledExe() *discoveredRuntime {
	if path, err := exec.LookPath("dsh-jsonrpc-agent"); err == nil {
		return &discoveredRuntime{exe: path, source: "PATH:dsh-jsonrpc-agent"}
	}
	return nil
}

// ── route 2: user-global @deepseek-ai/dsh (the real-user form) ──────────────

// globalDshInstall is a probed user-global npm package of @deepseek-ai/dsh.
type globalDshInstall struct {
	dshDir         string // <npmRoot>/@deepseek-ai/dsh
	familyScopeDir string // <dshDir>/node_modules/@deepseek-ai — symlink source for the shadow tree
	appBootEntry   string // resolved dsh-app-boot entry (existence proof + version logging)
	dshVersion     string
	appBootVersion string
}

// npmGlobalRoots lists candidate npm global roots: `npm root -g` following the
// resolved npm (a READ-ONLY query — never an install), plus well-known roots.
// A package var seam so tests can isolate the chain from the host machine.
var npmGlobalRoots = func() []string {
	var roots []string
	if npm := resolveNpmQueryBinary(); npm != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, npm, "root", "-g")
		if out, err := cmd.Output(); err == nil {
			if root := strings.TrimSpace(string(out)); root != "" && !strings.Contains(root, "\n") {
				roots = append(roots, root)
			}
		}
	}
	home, _ := os.UserHomeDir()
	roots = append(roots,
		"/opt/homebrew/lib/node_modules",
		"/usr/local/lib/node_modules",
		filepath.Join(home, ".npm-global", "lib", "node_modules"),
	)
	// nvm: every installed node version's global root.
	if entries, err := os.ReadDir(filepath.Join(home, ".nvm", "versions", "node")); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
				roots = append(roots, filepath.Join(home, ".nvm", "versions", "node", e.Name(), "lib", "node_modules"))
			}
		}
	}
	// pnpm global (read-only query when pnpm exists).
	if pnpm, err := exec.LookPath("pnpm"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, pnpm, "root", "-g")
		if out, err := cmd.Output(); err == nil {
			if root := strings.TrimSpace(string(out)); root != "" && !strings.Contains(root, "\n") {
				roots = append(roots, root)
			}
		}
	}
	sort.Strings(roots)
	return roots
}

// resolveNpmQueryBinary finds npm for READ-ONLY queries only (root -g).
// Installing is prohibited and test-locked; this binary never receives an
// install-class argument from this package.
func resolveNpmQueryBinary() string {
	if path, err := exec.LookPath("npm"); err == nil {
		return path
	}
	if node := resolveNodeBinary(); node != "" {
		candidate := filepath.Join(filepath.Dir(node), "npm")
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// readPkgVersion reads package.json "version" (empty on any failure).
func readPkgVersion(pkgDir string) string {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

// resolvePackageEntry resolves a package's "." entry file via exports/main
// (existence proof for dsh-app-boot). Empty when unresolvable.
func resolvePackageEntry(pkgDir string) string {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Main    string          `json:"main"`
		Module  string          `json:"module"`
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	pick := func(rel string) string {
		if rel == "" {
			return ""
		}
		candidate := filepath.Join(pkgDir, rel)
		if fileExists(candidate) {
			return candidate
		}
		return ""
	}
	// exports["."] may be a string or a conditions object.
	if len(pkg.Exports) > 0 {
		var root any
		if json.Unmarshal(pkg.Exports, &root) == nil {
			switch v := root.(type) {
			case string:
				if p := pick(v); p != "" {
					return p
				}
			case map[string]any:
				dot, ok := v["."]
				if !ok {
					return ""
				}
				switch entry := dot.(type) {
				case string:
					if p := pick(entry); p != "" {
						return p
					}
				case map[string]any:
					for _, cond := range []string{"default", "import", "node", "require"} {
						if s, ok := entry[cond].(string); ok {
							if p := pick(s); p != "" {
								return p
							}
						}
					}
				}
			}
		}
	}
	for _, rel := range []string{pkg.Module, pkg.Main, "lib/index.js", "index.js"} {
		if p := pick(rel); p != "" {
			return p
		}
	}
	return ""
}

// discoverGlobalDsh probes the user-global npm dsh install. Valid when the
// dsh package AND its nested dsh-app-boot exist and the app-boot entry
// resolves; anything else is an honest miss that falls through to the next
// route.
func discoverGlobalDsh() *discoveredRuntime {
	node := resolveNodeBinary()
	if node == "" {
		return nil
	}
	for _, root := range npmGlobalRoots() {
		dshDir := filepath.Join(root, "@deepseek-ai", "dsh")
		if !fileExists(filepath.Join(dshDir, "package.json")) {
			continue
		}
		scopeDir := filepath.Join(dshDir, "node_modules", "@deepseek-ai")
		appBootDir := filepath.Join(scopeDir, "dsh-app-boot")
		entry := resolvePackageEntry(appBootDir)
		if entry == "" {
			continue // app-boot present but unresolvable → honest degrade
		}
		return &discoveredRuntime{
			source:  "npm-global:@deepseek-ai/dsh",
			nodeBin: node,
			global: &globalDshInstall{
				dshDir:         dshDir,
				familyScopeDir: scopeDir,
				appBootEntry:   entry,
				dshVersion:     readPkgVersion(dshDir),
				appBootVersion: readPkgVersion(appBootDir),
			},
		}
	}
	return nil
}

// ── route 3: pip wheel ──────────────────────────────────────────────────────

// pythonRuntimeProbe is the Resolution-API seam (injectable in tests).
var pythonRuntimeProbe = func(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "python3", "-c",
		"import deepseek_harness_runtime as r; print(r.bundled_runtime_path())")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" || strings.Contains(path, "\n") {
		return "", fmt.Errorf("unexpected Resolution API output")
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

// pythonWheelRuntime locates the runtime through the official wheel API with
// a bounded timeout. A missing python3 or missing package is an honest miss.
func pythonWheelRuntime() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path, err := pythonRuntimeProbe(ctx)
	if err != nil {
		return ""
	}
	return path
}

func discoverWheel() *discoveredRuntime {
	if name := pkgExeName(); name != "" {
		if path, err := exec.LookPath(name); err == nil {
			return &discoveredRuntime{exe: path, source: "PATH:" + name}
		}
	}
	if path := pythonWheelRuntime(); path != "" {
		return &discoveredRuntime{exe: path, source: "python-wheel"}
	}
	return nil
}

// ── route 4: nvm ────────────────────────────────────────────────────────────

// latestNvmRuntime finds dsh-jsonrpc-agent under the newest nvm node version.
func latestNvmRuntime() string { return latestNvmBinary("dsh-jsonrpc-agent") }

// latestNvmBinary finds <name> under the newest nvm node version that has it.
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
	type version struct {
		major, minor, patch int
		dir                 string
	}
	var versions []version
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		if major, minor, patch, ok := parseNodeVersion(e.Name()); ok {
			versions = append(versions, version{major, minor, patch, e.Name()})
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
		candidate := filepath.Join(versionsDir, versions[i].dir, "bin", name)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func parseNodeVersion(name string) (major, minor, patch int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(name, "v"), ".")
	if len(parts) < 2 {
		return 0, 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := 0, error(nil)
	if len(parts) > 2 {
		patch, err3 = strconv.Atoi(parts[2])
	}
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

// ── route 5: dev source checkout (opt-in only) ──────────────────────────────

const jsonrpcBinRel = "packages/examples/jsonrpc-demo/src/bin.ts"

// devSourceRoot returns the explicit opt-in checkout root (DSH_DEV_SOURCE_ROOT).
func devSourceRoot() string {
	return strings.TrimSpace(os.Getenv("DSH_DEV_SOURCE_ROOT"))
}

// discoverDevSourceCheckout probes ONLY the DSH_DEV_SOURCE_ROOT opt-in path —
// source checkouts are reference material, never a product runtime form.
func discoverDevSourceCheckout() *discoveredRuntime {
	root := devSourceRoot()
	if root == "" {
		return nil
	}
	script := filepath.Join(root, jsonrpcBinRel)
	if !fileExists(script) || !fileExists(filepath.Join(root, "node_modules", ".bin", "tsx")) {
		slog.Warn("dsh: DSH_DEV_SOURCE_ROOT is not a usable checkout", "root", root)
		return nil
	}
	node := resolveNodeBinary()
	if node == "" {
		return nil
	}
	return &discoveredRuntime{
		source:  "dev-only source checkout",
		nodeBin: node,
		srcRoot: root,
		script:  script,
	}
}

// ── node resolution (shared by routes 2/5) ─────────────────────────────────

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// resolveNodeBinary finds node: PATH first, then nvm's newest version, then
// common absolute install locations.
func resolveNodeBinary() string {
	if path, err := exec.LookPath("node"); err == nil {
		return path
	}
	if node := latestNvmBinary("node"); node != "" {
		return node
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, p := range []string{
		filepath.Join(home, ".volta", "bin", "node"),
		"/opt/homebrew/bin/node",
		"/usr/local/bin/node",
	} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// ── the chain ────────────────────────────────────────────────────────────────

// discoverProbeOnly runs the full probe chain (1→4, plus opt-in 5) without
// side effects. Used by the bridge descriptor status; agent construction
// applies the same order and then materializes the shadow tree for route 2.
func discoverProbeOnly() *discoveredRuntime {
	for _, probe := range []func() *discoveredRuntime{
		discoverInstalledExe,
		discoverGlobalDsh,
		discoverWheel,
		func() *discoveredRuntime {
			if path := latestNvmRuntime(); path != "" {
				return &discoveredRuntime{exe: path, source: "nvm"}
			}
			return nil
		},
		discoverDevSourceCheckout,
	} {
		if rt := probe(); rt != nil {
			return rt
		}
	}
	return nil
}

// ── harness credential layers (unchanged) ───────────────────────────────────

// harnessCredentials is what the ~/.dsh layers supplied.
type harnessCredentials struct {
	APIKey  string
	BaseURL string
	Source  string // "credentials.yaml" | "env-file" | ""
}

// discoverHarnessCredentials reads $DSH_HOME/.credentials.yaml then
// $DSH_HOME/.env for DEEPSEEK_API_KEY / DEEPSEEK_BASE_URL. Each key resolves
// independently from the highest layer that supplies it. Anything malformed
// is an honest miss for that key — never a guess.
func discoverHarnessCredentials() harnessCredentials {
	home := dshHome()
	if home == "" {
		return harnessCredentials{}
	}
	var out harnessCredentials
	if creds := readCredentialsYAML(filepath.Join(home, ".credentials.yaml")); creds.APIKey != "" || creds.BaseURL != "" {
		if creds.APIKey != "" {
			out.APIKey = creds.APIKey
		}
		if creds.BaseURL != "" {
			out.BaseURL = creds.BaseURL
		}
		out.Source = "credentials.yaml"
	}
	if out.APIKey == "" || out.BaseURL == "" {
		envs := readEnvFile(filepath.Join(home, ".env"))
		if out.APIKey == "" && envs["DEEPSEEK_API_KEY"] != "" {
			out.APIKey = envs["DEEPSEEK_API_KEY"]
			out.Source = "env-file"
		}
		if out.BaseURL == "" && envs["DEEPSEEK_BASE_URL"] != "" {
			out.BaseURL = envs["DEEPSEEK_BASE_URL"]
			if out.Source == "" {
				out.Source = "env-file"
			}
		}
	}
	return out
}

// readCredentialsYAML parses the strict top-level `CredentialRef: value`
// mapping (the Web UI Models page writes exactly this shape; dsh-credentials
// documents it as flat by design). Nested/flow YAML beyond a top-level scalar
// mapping is ignored — an honest miss rather than a partial guess.
func readCredentialsYAML(path string) harnessCredentials {
	var out harnessCredentials
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") ||
			value == "|" || value == ">" {
			continue
		}
		value = unquoteYAMLScalar(value)
		switch key {
		case "DEEPSEEK_API_KEY":
			out.APIKey = value
		case "DEEPSEEK_BASE_URL":
			out.BaseURL = value
		}
	}
	return out
}

// unquoteYAMLScalar strips one pair of surrounding quotes and a trailing
// inline comment on unquoted values.
func unquoteYAMLScalar(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	if i := strings.Index(v, " #"); i > 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// readEnvFile parses a dotenv subset: KEY=VALUE lines, comments with #,
// optional surrounding quotes on the value.
func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
		if key != "" {
			out[key] = value
		}
	}
	return out
}
