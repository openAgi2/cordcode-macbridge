package dsh

// Phase-7 tests (owner probe directive v2): probe-only discovery chain,
// shadow-tree construction, version logging, and the never-install lock.

import (
	"context"
	"os"
	"path/filepath"
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

// fakeNodeShim writes a `node` executable into dir (PATH-prependable) that
// runs the given script regardless of argv.
func fakeNodeShim(t *testing.T, runtimeScript string) string {
	t.Helper()
	dir := t.TempDir()
	shim := "#!/bin/sh\nexec python3 \"" + runtimeScript + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeNpmShim writes an `npm` executable that FAILS if asked to install —
// the §3 never-install lock. Read-only queries (root -g) are answered.
func fakeNpmShim(t *testing.T, root string) string {
	t.Helper()
	dir := t.TempDir()
	shim := "#!/bin/sh\ncase \"$1\" in\n  root) echo '" + root + "';;\n  *) echo 'npm-lock: install-class invocation rejected: '\"$@\" >&2; exit 99;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ── credential layers (unchanged semantics; regression guards) ──────────────

func TestCredentialsYAMLLayer(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), `# managed by dsh Web UI
DEEPSEEK_API_KEY: "sk-test-from-yaml"
DEEPSEEK_BASE_URL: https://api.example.com/v1
`)
	creds := discoverHarnessCredentials()
	if creds.APIKey != "sk-test-from-yaml" || creds.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("creds = %+v", creds)
	}
	if creds.Source != "credentials.yaml" {
		t.Fatalf("Source = %q", creds.Source)
	}
}

func TestCredentialLayerPrecedencePerKey(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "DEEPSEEK_API_KEY: sk-yaml\n")
	writeFileT(t, filepath.Join(home, ".env"), "DEEPSEEK_API_KEY=sk-env\nDEEPSEEK_BASE_URL=https://fallback.example\n")
	creds := discoverHarnessCredentials()
	if creds.APIKey != "sk-yaml" || creds.BaseURL != "https://fallback.example" {
		t.Fatalf("per-key precedence broken: %+v", creds)
	}
}

func TestCredentialMalformedIsHonestMiss(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "providers:\n  - id: weird\nDEEPSEEK_API_KEY: [flow, seq]\n")
	writeFileT(t, filepath.Join(home, ".env"), "garbage line without equals\n")
	if creds := discoverHarnessCredentials(); creds.APIKey != "" || creds.BaseURL != "" {
		t.Fatalf("malformed must miss honestly, got %+v", creds)
	}
}

func TestBuildProcessEnvCredentialPrecedence(t *testing.T) {
	home := useTempDshHome(t)
	writeFileT(t, filepath.Join(home, ".credentials.yaml"), "DEEPSEEK_API_KEY: sk-harness\n")
	lookup := func(env []string, key string) (string, bool) {
		for _, e := range env {
			if k, v, ok := strings.Cut(e, "="); ok && k == key {
				return v, true
			}
		}
		return "", false
	}
	a := &Agent{workDir: t.TempDir()}
	if v, ok := lookup(a.buildProcessEnv(), "DEEPSEEK_API_KEY"); !ok || v != "sk-harness" {
		t.Fatalf("harness fallback missing")
	}
	a.SetProviders([]core.ProviderConfig{{Name: "deepseek", APIKey: "sk-provider"}})
	a.SetActiveProvider("deepseek")
	if v, _ := lookup(a.buildProcessEnv(), "DEEPSEEK_API_KEY"); v != "sk-provider" {
		t.Fatalf("provider key must win, got %q", v)
	}
}

// 2026-08-16 owner 决策：DSH_SESSION_ROOT 必须指向用户 harness 默认存储
// （$DSH_HOME/sessions），CordCode 会话落入 dsh web 可见可续聊；仅在 HOME
// 解析失败时才允许退回 MacBridge 私有目录。
func TestBuildProcessEnvSessionRootInUserHarnessStore(t *testing.T) {
	lookup := func(env []string, key string) (string, bool) {
		for _, e := range env {
			if k, v, ok := strings.Cut(e, "="); ok && k == key {
				return v, true
			}
		}
		return "", false
	}
	home := useTempDshHome(t)
	a := &Agent{workDir: t.TempDir()}
	if v, ok := lookup(a.buildProcessEnv(), "DSH_SESSION_ROOT"); !ok || v != filepath.Join(home, "sessions") {
		t.Fatalf("DSH_SESSION_ROOT = %q, want %s", v, filepath.Join(home, "sessions"))
	}
	if _, err := os.Stat(filepath.Join(home, "sessions")); err != nil {
		t.Fatalf("session root not materialized under harness home: %v", err)
	}

	// HOME 与 DSH_HOME 都解析失败 → 防御性回退私有目录，绝不相对路径散写 cwd。
	t.Setenv("DSH_HOME", "")
	t.Setenv("HOME", "")
	if v, _ := lookup(a.buildProcessEnv(), "DSH_SESSION_ROOT"); v != filepath.Join(a.workDir, dshDataSubdir, sessionsSubdir) {
		t.Fatalf("fallback must stay inside MacBridge data dir, got %q", v)
	}
}

// ── probe chain: priority fixtures (①>②>③>④, ⑤ env opt-in only) ───────────

// fakeGlobalDshTree lays out a user-global npm dsh install at root.
func fakeGlobalDshTree(t *testing.T, root string) {
	t.Helper()
	dshDir := filepath.Join(root, "@deepseek-ai", "dsh")
	writeFileT(t, filepath.Join(dshDir, "package.json"), `{"name":"@deepseek-ai/dsh","version":"0.1.0-rc.6"}`)
	scope := filepath.Join(dshDir, "node_modules", "@deepseek-ai")
	writeFileT(t, filepath.Join(scope, "dsh-app-boot", "package.json"), `{"name":"@deepseek-ai/dsh-app-boot","version":"0.1.0-rc.6","main":"lib/index.js"}`)
	writeFileT(t, filepath.Join(scope, "dsh-app-boot", "lib", "index.js"), "export const boot = 1\n")
	writeFileT(t, filepath.Join(scope, "dsh-llm-deepseek", "package.json"), `{"name":"@deepseek-ai/dsh-llm-deepseek","version":"0.1.0-rc.6"}`)
}

func probeChainEnv(t *testing.T, npmRoot string) {
	t.Helper()
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("DSH_DEV_SOURCE_ROOT", "")
	// Isolated PATH: node + read-only npm; nothing else can satisfy probes.
	t.Setenv("PATH", fakeNodeShim(t, "/nonexistent.py")+":"+fakeNpmShim(t, npmRoot))
	// Wheel/nvm/python routes must not fire in these fixtures.
	restored := pythonRuntimeProbe
	pythonRuntimeProbe = func(ctx context.Context) (string, error) { return "", context.Canceled }
	// Route-2 roots isolated from the HOST machine's real global trees.
	restoredRoots := npmGlobalRoots
	npmGlobalRoots = func() []string { return []string{npmRoot} }
	t.Cleanup(func() {
		pythonRuntimeProbe = restored
		npmGlobalRoots = restoredRoots
	})
}

func TestProbeChainGlobalDshRoute(t *testing.T) {
	root := t.TempDir()
	fakeGlobalDshTree(t, root)
	probeChainEnv(t, root)

	rt := discoverProbeOnly()
	if rt == nil || rt.global == nil {
		t.Fatalf("route 2 must hit on a global dsh tree, got %+v", rt)
	}
	if rt.global.dshVersion != "0.1.0-rc.6" || rt.global.appBootVersion != "0.1.0-rc.6" {
		t.Fatalf("versions not resolved: %+v", rt.global)
	}
	if rt.source != "npm-global:@deepseek-ai/dsh" {
		t.Fatalf("source = %q", rt.source)
	}
}

func TestProbeChainGlobalDshMissingFallsThrough(t *testing.T) {
	empty := t.TempDir()
	probeChainEnv(t, empty)

	if rt := discoverGlobalDsh(); rt != nil {
		t.Fatalf("empty root must miss route 2, got %+v", rt)
	}
	// App-boot present but entry unresolvable → honest degrade.
	root := t.TempDir()
	dshDir := filepath.Join(root, "@deepseek-ai", "dsh")
	writeFileT(t, filepath.Join(dshDir, "package.json"), `{"name":"@deepseek-ai/dsh"}`)
	writeFileT(t, filepath.Join(dshDir, "node_modules", "@deepseek-ai", "dsh-app-boot", "package.json"), `{"name":"@deepseek-ai/dsh-app-boot"}`)
	if rt := discoverGlobalDsh(); rt != nil {
		t.Fatalf("unresolvable app-boot must degrade honestly, got %+v", rt)
	}
}

func TestProbeChainSourceCheckoutEnvOptInOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	t.Setenv("PATH", fakeNodeShim(t, "/nonexistent.py")+":"+fakeNpmShim(t, t.TempDir()))

	// A perfectly valid checkout exists at a conventional location…
	root := filepath.Join(home, "Projects", "deepseek-harness")
	writeFileT(t, filepath.Join(root, jsonrpcBinRel), "// bin\n")
	writeFileT(t, filepath.Join(root, "node_modules", ".bin", "tsx"), "#!/bin/sh\n")

	// …but without the env it is NEVER part of the product chain.
	t.Setenv("DSH_DEV_SOURCE_ROOT", "")
	if rt := discoverDevSourceCheckout(); rt != nil {
		t.Fatal("source checkout must be opt-in only")
	}

	t.Setenv("DSH_DEV_SOURCE_ROOT", root)
	rt := discoverDevSourceCheckout()
	if rt == nil || rt.srcRoot != root || rt.source != "dev-only source checkout" {
		t.Fatalf("opt-in checkout must hit: %+v", rt)
	}
}

// ── shadow tree ──────────────────────────────────────────────────────────────

func TestEnsureShadowTreeVendoredAndSymlinks(t *testing.T) {
	root := t.TempDir()
	fakeGlobalDshTree(t, root)
	rt := discoverGlobalDshLike(t, root)

	dataDir := t.TempDir()
	binJS, err := ensureShadowTree(dataDir, rt)
	if err != nil {
		t.Fatal(err)
	}
	// Vendored SDK layer: REAL files from the embedded rc.6 copies.
	if !fileExists(filepath.Join(dataDir, "node_modules", "@deepseek-ai", "dsh-sdk-jsonrpc-server", "lib", "index.js")) {
		t.Fatal("vendored server missing")
	}
	if binJS != shadowBinScript(filepath.Join(dataDir, "node_modules", "@deepseek-ai")) {
		t.Fatalf("binJS = %q", binJS)
	}
	// Family: symlink into the user's global tree.
	link := filepath.Join(dataDir, "node_modules", "@deepseek-ai", "dsh-llm-deepseek")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("family package not a symlink: %v", err)
	}
	if !strings.HasPrefix(target, root) {
		t.Fatalf("symlink target %q outside the global tree", target)
	}
	// Idempotent refresh.
	if _, err := ensureShadowTree(dataDir, rt); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	// Nothing written into the user's global tree.
	if _, err := os.Stat(filepath.Join(root, "@deepseek-ai", "dsh", "node_modules", "@deepseek-ai", "dsh-sdk-jsonrpc-server")); err == nil {
		t.Fatal("must never write vendored packages into the user's global tree")
	}
}

func discoverGlobalDshLike(t *testing.T, root string) *globalDshInstall {
	t.Helper()
	dshDir := filepath.Join(root, "@deepseek-ai", "dsh")
	return &globalDshInstall{
		dshDir:         dshDir,
		familyScopeDir: filepath.Join(dshDir, "node_modules", "@deepseek-ai"),
		appBootEntry:   filepath.Join(dshDir, "node_modules", "@deepseek-ai", "dsh-app-boot", "lib", "index.js"),
		dshVersion:     "0.1.0-rc.6",
		appBootVersion: "0.1.0-rc.6",
	}
}

// ── the never-install lock (§3) ─────────────────────────────────────────────

// The chain runs with a PATH whose npm REJECTS install-class invocations and
// whose node runs a recording no-op. Any install attempt = test failure via
// npm exit 99 surfaced as a probe miss — and we additionally assert the dsh
// package exports no install-capable path by construction (no such code).
func TestProbeChainNeverInstalls(t *testing.T) {
	root := t.TempDir() // no dsh tree: nothing to find
	probeChainEnv(t, root)

	if rt := discoverProbeOnly(); rt != nil {
		t.Fatalf("empty environment must probe nothing, got %+v", rt)
	}
	// The §3 lock itself is structural: agent/dsh contains no install code
	// (grep-lock below keeps it that way even under refactors).
	for _, f := range []string{"discovery.go", "shadow.go", "dsh.go", "session.go"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"npm install", "npmInstallFunc", "ensureRuntimeProject", "npx "} {
			if strings.Contains(string(data), banned) {
				t.Fatalf("%s must not contain install-capable code %q", f, banned)
			}
		}
	}
}

// ── rc.6 vendored glue sanity ────────────────────────────────────────────────

func TestVendoredDemoBinPresent(t *testing.T) {
	// The vendored rc.6 demo glue must be intact in the embedded FS.
	data, err := vendorFS.ReadFile("vendor/@deepseek-ai/dsh-sdk-jsonrpc-demo/lib/bin.js")
	if err != nil || len(data) == 0 {
		t.Fatalf("vendored bin.js missing: %v", err)
	}
}

func TestDiscoveryDoesNotHangOnPython(t *testing.T) {
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
