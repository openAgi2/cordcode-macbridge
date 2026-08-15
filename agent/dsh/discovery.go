package dsh

// Harness auto-discovery (product hardening, owner feedback 2026-08-15):
// once the user has DeepSeek Harness on their Mac, CordCode Link must find
// the runtime and the credentials on its own — no extra provider-key setup in
// MacBridge for a harness that is already configured.
//
// Runtime discovery order (first hit wins):
//  1. explicit cli_path option (New's opt — installer/user override)
//  2. `dsh-jsonrpc-agent` on PATH (npm-global/checkout installs; the Mac app
//     merges node-ecosystem bin dirs into PATH for GUI launches)
//  3. `dsh-jsonrpc-agent-pkg-<platform>` on PATH — the single-file executable
//     name shipped by the official Python runtime wheel
//  4. nvm: ~/.nvm/versions/node/<max semver>/bin
//  5. the Python Resolution API (python3 -c 'import deepseek_harness_runtime;
//     print(bundled_runtime_path())') — the wheel's own locator, which also
//     validates the macOS -spawn-helper sibling
//
// Credential layering mirrors the harness's own trust order
// (credentials-local precedence: inherited env > ~/.dsh/.credentials.yaml >
// project .env > ~/.dsh/.env; the packaged runtime closure ships WITHOUT the
// credentials-local plugin, so the composition cannot mount it — the driver
// reads the files instead and injects via env, which ranks first in every
// harness layering anyway):
//
//	MacBridge provider key (explicit)   ← becomes inherited env, wins
//	> $DSH_HOME/.credentials.yaml       ← what the `dsh` Web UI Models page writes
//	> $DSH_HOME/.env                    ← dotenv fallback
//
// $DSH_HOME honors the env override, defaulting to ~/.dsh (resolveDshHome).

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// discoverRuntimeBinary searches every acquisition route in order. It returns
// the executable path plus a short human-readable source label for
// diagnostics. An empty result means the harness runtime is not installed.
func discoverRuntimeBinary() (string, string) {
	if path, err := exec.LookPath("dsh-jsonrpc-agent"); err == nil {
		return path, "PATH:dsh-jsonrpc-agent"
	}
	if name := pkgExeName(); name != "" {
		if path, err := exec.LookPath(name); err == nil {
			return path, "PATH:" + name
		}
	}
	if path := latestNvmRuntime(); path != "" {
		return path, "nvm"
	}
	if path := pythonWheelRuntime(); path != "" {
		return path, "python-wheel"
	}
	return "", ""
}

// latestNvmRuntime finds dsh-jsonrpc-agent under the newest nvm node version.
func latestNvmRuntime() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	versionsDir := filepath.Join(home, ".nvm", "versions", "node")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return ""
	}
	best, bestMajor, bestMinor, bestPatch := "", -1, -1, -1
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		major, minor, patch, ok := parseNodeVersion(e.Name())
		if !ok {
			continue
		}
		if major > bestMajor || (major == bestMajor && minor > bestMinor) ||
			(major == bestMajor && minor == bestMinor && patch > bestPatch) {
			candidate := filepath.Join(versionsDir, e.Name(), "bin", "dsh-jsonrpc-agent")
			if _, err := exec.LookPath(candidate); err == nil {
				best, bestMajor, bestMinor, bestPatch = candidate, major, minor, patch
			}
		}
	}
	return best
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

// ── harness credential layers ───────────────────────────────────────────────

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
			// Flow collections / block indicators are outside the strict
			// flat scalar mapping the credential store is documented to be —
			// honest miss for that key, never a partial guess.
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
