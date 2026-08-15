package dsh

// Phase-6 tests: harness auto-discovery (owner feedback 2026-08-15) —
// runtime binary acquisition routes and ~/.dsh credential layering.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func useTempDshHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	return home
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsYAMLLayer(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), `# managed by dsh Web UI
DEEPSEEK_API_KEY: "sk-test-from-yaml"
DEEPSEEK_BASE_URL: https://api.example.com/v1
OTHER_TOOL_KEY: irrelevant
`)
	creds := discoverHarnessCredentials()
	if creds.APIKey != "sk-test-from-yaml" {
		t.Fatalf("APIKey = %q", creds.APIKey)
	}
	if creds.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("BaseURL = %q", creds.BaseURL)
	}
	if creds.Source != "credentials.yaml" {
		t.Fatalf("Source = %q", creds.Source)
	}
}

func TestCredentialsYAMLScalarShapes(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), `DEEPSEEK_API_KEY: sk-plain
`)
	if got := discoverHarnessCredentials().APIKey; got != "sk-plain" {
		t.Fatalf("plain scalar = %q", got)
	}

	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "DEEPSEEK_API_KEY: 'sk-single'\n")
	if got := discoverHarnessCredentials().APIKey; got != "sk-single" {
		t.Fatalf("single-quoted = %q", got)
	}

	// Trailing inline comment on an unquoted scalar.
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "DEEPSEEK_API_KEY: sk-cmt # saved 2026\n")
	if got := discoverHarnessCredentials().APIKey; got != "sk-cmt" {
		t.Fatalf("inline comment = %q", got)
	}
}

func TestEnvFileFallbackLayer(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".env"), `# user env layer
DEEPSEEK_API_KEY=sk-from-env
DEEPSEEK_BASE_URL="https://env.example.com"
OTHER=1
`)
	creds := discoverHarnessCredentials()
	if creds.APIKey != "sk-from-env" || creds.BaseURL != "https://env.example.com" {
		t.Fatalf("env layer: %+v", creds)
	}
	if creds.Source != "env-file" {
		t.Fatalf("Source = %q", creds.Source)
	}
}

func TestCredentialLayerPrecedencePerKey(t *testing.T) {
	home := useTempDshHome(t)
	// yaml has the key but no base URL; .env has both — key comes from the
	// higher layer, base URL falls through per-key.
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "DEEPSEEK_API_KEY: sk-yaml\n")
	writeFileT(t, filepath.Join(home, ".env"), "DEEPSEEK_API_KEY=sk-env\nDEEPSEEK_BASE_URL=https://fallback.example\n")
	creds := discoverHarnessCredentials()
	if creds.APIKey != "sk-yaml" {
		t.Fatalf("APIKey must prefer .credentials.yaml, got %q", creds.APIKey)
	}
	if creds.BaseURL != "https://fallback.example" {
		t.Fatalf("BaseURL must fall through to .env, got %q", creds.BaseURL)
	}
}

func TestCredentialMalformedIsHonestMiss(t *testing.T) {
	home := useTempDshHome(t)
	// Nested/flow YAML beyond the strict flat mapping: no key extracted —
	// an honest miss, never a partial guess.
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "providers:\n  - id: weird\nDEEPSEEK_API_KEY: [flow, seq]\n")
	writeFileT(t, filepath.Join(home, ".env"), "garbage line without equals\n")
	if creds := discoverHarnessCredentials(); creds.APIKey != "" || creds.BaseURL != "" {
		t.Fatalf("malformed layers must yield an honest miss, got %+v", creds)
	}
}

func TestDSHHomeEnvOverride(t *testing.T) {
	home := useTempDshHome(t)
	custom := t.TempDir()
	t.Setenv("DSH_HOME", custom)
	writeFileT(t, filepath.Join(custom, ".credentials.yaml"), "DEEPSEEK_API_KEY: sk-custom\n")
	if got := discoverHarnessCredentials().APIKey; got != "sk-custom" {
		t.Fatalf("DSH_HOME override ignored: %q", got)
	}
	// The first home still has nothing — proves the override redirected.
	if _, err := os.Stat(filepath.Join(home, ".credentials.yaml")); err == nil {
		t.Fatal("setup error: first home should be empty")
	}
}

func TestBuildProcessEnvCredentialPrecedence(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "DEEPSEEK_API_KEY: sk-harness\n")

	// No provider configured → harness credential injected.
	a := &Agent{workDir: t.TempDir()}
	env := a.buildProcessEnv()
	lookup := func(key string) (string, bool) {
		for _, e := range env {
			if k, v, ok := strings.Cut(e, "="); ok && k == key {
				return v, true
			}
		}
		return "", false
	}
	if v, ok := lookup("DEEPSEEK_API_KEY"); !ok || v != "sk-harness" {
		t.Fatalf("harness fallback missing: %q ok=%v", v, ok)
	}
	if v, _ := lookup("DSH_HOME"); v != home {
		t.Fatalf("DSH_HOME must not be overridden by the driver: %q", v)
	}

	// Explicit MacBridge provider key wins over the harness store.
	a.SetProviders([]core.ProviderConfig{{Name: "deepseek", APIKey: "sk-provider"}})
	a.SetActiveProvider("deepseek")
	env = a.buildProcessEnv()
	if v, _ := lookup("DEEPSEEK_API_KEY"); v != "sk-provider" {
		t.Fatalf("provider key must win, got %q", v)
	}
}

func TestPkgExeNamePlatformMapping(t *testing.T) {
	// Pure mapping check: the wheel's executable-name scheme.
	valid := map[string]bool{"darwin/arm64": true, "linux/amd64": true, "linux/arm64": true}
	name := pkgExeName()
	if !valid[platformKey()] && name != "" {
		t.Fatalf("unexpected mapping for %s: %q", platformKey(), name)
	}
	switch platformKey() {
	case "darwin/arm64":
		if name != "dsh-jsonrpc-agent-pkg-macos-arm64" {
			t.Fatalf("macos name = %q", name)
		}
	}
}

func TestLatestNvmRuntime(t *testing.T) {
	// Fake nvm layout: two versions, newest carries the runtime.
	binDir := t.TempDir()
	v20 := filepath.Join(binDir, ".nvm", "versions", "node", "v20.1.0", "bin")
	v22 := filepath.Join(binDir, ".nvm", "versions", "node", "v22.9.0", "bin")
	writeFileT(t, filepath.Join(v22, "dsh-jsonrpc-agent"), "#!/bin/sh\n")
	os.Chmod(filepath.Join(v22, "dsh-jsonrpc-agent"), 0o755)
	os.MkdirAll(v20, 0o755)

	t.Setenv("HOME", binDir)
	got := latestNvmRuntime()
	if !strings.HasSuffix(got, filepath.Join("v22.9.0", "bin", "dsh-jsonrpc-agent")) {
		t.Fatalf("nvm discovery = %q, want the newest version", got)
	}
}

func TestPythonWheelRuntimeProbe(t *testing.T) {
	// Seam injection: the Resolution API path is honored verbatim.
	restored := pythonRuntimeProbe
	t.Cleanup(func() { pythonRuntimeProbe = restored })
	exe := filepath.Join(t.TempDir(), "dsh-jsonrpc-agent-pkg-macos-arm64")
	writeFileT(t, exe, "stub")
	pythonRuntimeProbe = func(ctx context.Context) (string, error) { return exe, nil }
	if got := pythonWheelRuntime(); got != exe {
		t.Fatalf("probe result = %q", got)
	}

	// Probe failure (missing package) is an honest miss, not an error path.
	pythonRuntimeProbe = func(ctx context.Context) (string, error) {
		return "", context.DeadlineExceeded
	}
	if got := pythonWheelRuntime(); got != "" {
		t.Fatalf("failed probe must miss, got %q", got)
	}
}

func TestDiscoveryDoesNotHangOnPython(t *testing.T) {
	// A wedged python3 must not stall startup: the probe is bounded.
	restored := pythonRuntimeProbe
	t.Cleanup(func() { pythonRuntimeProbe = restored })
	start := time.Now()
	pythonRuntimeProbe = func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	pythonWheelRuntime()
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("probe exceeded its bound: %v", elapsed)
	}
}

// platformKey mirrors runtime.GOOS/GOARCH for the mapping test.
func platformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// ── source-checkout discovery (owner: harness as source at ~/Projects) ─────

// fakeNodeShim writes a `node` executable into dir that just runs the python
// fake runtime regardless of argv — letting src-mode spawn run end to end
// hermetically. Returns dir for PATH prepending.
func fakeNodeShim(t *testing.T, runtimeScript string) string {
	t.Helper()
	dir := t.TempDir()
	shim := "#!/bin/sh\nexec python3 \"" + runtimeScript + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeCheckout lays out a minimal deepseek-harness source checkout under home.
func fakeCheckout(t *testing.T, home, rel string) string {
	t.Helper()
	root := filepath.Join(home, rel)
	writeFileT(t, filepath.Join(root, jsonrpcBinRel), "// placeholder bin.ts\n")
	writeFileT(t, filepath.Join(root, "node_modules", ".bin", "tsx"), "#!/bin/sh\n")
	return root
}

func TestDiscoverSourceCheckoutConventionalRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	nodeDir := fakeNodeShim(t, "/nonexistent-runtime.py")
	t.Setenv("PATH", nodeDir+":"+os.Getenv("PATH"))

	fakeCheckout(t, home, "Projects/deepseek-harness")
	rt := discoverSourceCheckout()
	if rt == nil {
		t.Fatal("conventional Projects/ checkout not discovered")
	}
	if !strings.HasSuffix(rt.srcRoot, "Projects/deepseek-harness") || rt.script == "" || rt.nodeBin == "" {
		t.Fatalf("discovered = %+v", rt)
	}
	if rt.source != "source-checkout:Projects/deepseek-harness" {
		t.Fatalf("source label = %q", rt.source)
	}

	// A checkout without installed node_modules is NOT usable.
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	root2 := filepath.Join(home2, "code", "deepseek-harness")
	writeFileT(t, filepath.Join(root2, jsonrpcBinRel), "// x\n")
	if rt := discoverSourceCheckout(); rt != nil {
		t.Fatalf("checkout without node_modules must not be discovered: %+v", rt)
	}
}

func TestNewWithSourceRootOpt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	root := fakeCheckout(t, home, "harness")
	nodeDir := fakeNodeShim(t, "/nonexistent-runtime.py")
	t.Setenv("PATH", nodeDir+":"+os.Getenv("PATH"))

	agentIface, err := New(map[string]any{"dsh_root": root, "work_dir": home})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := agentIface.(*Agent)
	if !ok {
		t.Fatalf("New returned %T", agentIface)
	}
	if a.srcRoot != root || a.scriptPath == "" || a.nodeBin == "" {
		t.Fatalf("src mode not armed: src=%q script=%q node=%q", a.srcRoot, a.scriptPath, a.nodeBin)
	}

	// Bad root → honest constructor error.
	if _, err := New(map[string]any{"dsh_root": filepath.Join(home, "missing"), "work_dir": home}); err == nil {
		t.Fatal("invalid dsh_root must fail closed")
	}
}

// End-to-end src-mode spawn: fake node shim + fake runtime through a full
// StartSession/Send/turn cycle, proving argv shape (config reaches the
// runtime as the trailing positional) and cwd independence.
func TestLifecycleSourceCheckoutSpawn(t *testing.T) {
	agent := newFaultAgent(t, "ok") // writes runtime script + config, mode "ok"
	// Rebuild the agent as src mode around the same fake runtime script.
	home := t.TempDir()
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	nodeDir := fakeNodeShim(t, agent.cliBin) // node shim execs the fake runtime
	t.Setenv("PATH", nodeDir+":"+os.Getenv("PATH"))

	root := fakeCheckout(t, home, "Projects/deepseek-harness")
	agentIface, err := New(map[string]any{"dsh_root": root, "work_dir": home, "config_path": agent.configPath})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := agentIface.(*Agent)
	if !ok {
		t.Fatalf("New returned %T", agentIface)
	}

	s, err := newDshSession(context.Background(), a, "ses-srcmode")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Send("hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	term := waitForEvent(t, s, 5*time.Second, func(ev core.Event) bool {
		return ev.Type == core.EventResult && ev.Done
	})
	if term.Error != nil {
		t.Fatalf("src-mode turn failed: %v", term.Error)
	}
}
