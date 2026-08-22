package gobridge

import (
	"os"
	"strings"
	"testing"
)

func TestBuildAgentOptions_DefaultCodexUsesSuggestMode(t *testing.T) {
	opts := buildAgentOptions("codex", agentOptionsConfig{
		workDir:      "/tmp/project",
		openCodeURL:  "http://localhost:64667",
		openCodeUser: "user",
		openCodePass: "pass",
	})

	if got := opts["mode"]; got != "custom" {
		t.Fatalf("mode = %#v, want custom", got)
	}
	if _, ok := opts["backend"]; ok {
		t.Fatalf("backend unexpectedly set for default codex opts: %#v", opts["backend"])
	}
	if _, ok := opts["app_server_url"]; ok {
		t.Fatalf("app_server_url unexpectedly set for default codex opts: %#v", opts["app_server_url"])
	}
}

func TestBuildAgentOptions_OpenCodeWebUsesSeparateKeys(t *testing.T) {
	opts := buildAgentOptions("opencode-web", agentOptionsConfig{
		workDir:         "/tmp/project",
		openCodeURL:     "http://127.0.0.1:4096",
		openCodeUser:    "legacy-user",
		openCodePass:    "legacy-pass",
		openCodeWebURL:  "http://127.0.0.1:4096",
		openCodeWebUser: "web-user",
		openCodeWebPass: "web-pass",
	})
	if got := opts["opencode_web_url"]; got != "http://127.0.0.1:4096" {
		t.Fatalf("opencode_web_url = %#v", got)
	}
	if got := opts["opencode_web_user"]; got != "web-user" {
		t.Fatalf("opencode_web_user = %#v, want web-user (not the legacy keys)", got)
	}
	if got := opts["opencode_web_pass"]; got != "web-pass" {
		t.Fatalf("opencode_web_pass = %#v, want web-pass (not the legacy keys)", got)
	}
}

func TestBuildAgentOptions_CodexAppServerUsesFullAuto(t *testing.T) {
	opts := buildAgentOptions("codex", agentOptionsConfig{
		workDir:           "/tmp/project",
		codexBackend:      "app-server",
		codexAppServerURL: "ws://127.0.0.1:9999",
	})

	if got := opts["mode"]; got != "custom" {
		t.Fatalf("mode = %#v, want custom", got)
	}
	if got := opts["backend"]; got != "app_server" {
		t.Fatalf("backend = %#v, want app_server", got)
	}
	if got := opts["app_server_url"]; got != "ws://127.0.0.1:9999" {
		t.Fatalf("app_server_url = %#v, want ws://127.0.0.1:9999", got)
	}
}

func TestDefaultDriversKeepCodexAndCodexWebIndependent(t *testing.T) {
	drivers := strings.Split(defaultDrivers, ",")
	want := map[string]bool{"codex": false, "codex-web": false}
	for _, driver := range drivers {
		if _, ok := want[driver]; ok {
			want[driver] = true
		}
	}
	for driver, present := range want {
		if !present {
			t.Fatalf("default drivers %q missing %q", defaultDrivers, driver)
		}
	}
}

func TestBuildAgentOptions_CodexWebUsesIndependentURLKey(t *testing.T) {
	opts := buildAgentOptions("codex-web", agentOptionsConfig{
		codexAppServerURL: "ws://127.0.0.1:4141",
		codexWebAppSrvURL: "ws://127.0.0.1:5151",
	})
	if got := opts["codex_web_app_server_url"]; got != "ws://127.0.0.1:5151" {
		t.Fatalf("codex_web_app_server_url = %#v, want independent codex-web URL", got)
	}
	if _, ok := opts["app_server_url"]; ok {
		t.Fatalf("codex-web options leaked legacy codex app_server_url: %#v", opts)
	}
}

func TestShouldStartPassiveSubscription_CodexRequiresExplicitSharedURL(t *testing.T) {
	if shouldStartPassiveSubscription("codex", "app_server", "", "", "") {
		t.Fatal("codex implicit app_server should not start process-level passive subscription")
	}
	if !shouldStartPassiveSubscription("codex", "app_server", "ws://127.0.0.1:4141", "", "") {
		t.Fatal("codex explicit shared app_server URL should start passive subscription")
	}
	if shouldStartPassiveSubscription("codex", "exec", "ws://127.0.0.1:4141", "", "") {
		t.Fatal("codex exec mode should not start app-server passive subscription")
	}
	// OpenCode: 无 URL（endpoint 未配置）不得启动 SSE 订阅，避免无意义重连退避。
	if shouldStartPassiveSubscription("opencode", "", "", "", "") {
		t.Fatal("opencode without server URL should not start passive subscription")
	}
	if !shouldStartPassiveSubscription("opencode", "", "", "http://127.0.0.1:4096", "") {
		t.Fatal("opencode with a configured server URL should start passive subscription")
	}
	// opencode-web: 同规则（设计 §2.1 坑 13）——空 URL = not_configured，不启动 SSE。
	if shouldStartPassiveSubscription("opencode-web", "", "", "", "") {
		t.Fatal("opencode-web without server URL should not start passive subscription")
	}
	if !shouldStartPassiveSubscription("opencode-web", "", "", "", "http://127.0.0.1:4096") {
		t.Fatal("opencode-web with a configured server URL should start passive subscription")
	}
}

func TestDisablesRelayIdleTimeoutIncludesOpenCode(t *testing.T) {
	if !disablesRelayIdleTimeout("opencode") {
		t.Fatal("opencode relay idle timeout should be disabled")
	}
	if !disablesRelayIdleTimeout("dsh-web") {
		t.Fatal("dsh-web relay idle timeout should be disabled (approval wait is silent)")
	}
	if !disablesRelayIdleTimeout("codex-web") {
		t.Fatal("codex-web relay idle timeout should be disabled so Mac turns stay live after an iOS turn")
	}
}

func TestRelaySurvivesTurnBoundaryForDSHWeb(t *testing.T) {
	if !relaySurvivesTurnBoundary("dsh-web") {
		t.Fatal("dsh-web relay must stay up after turn_completed so the next approval is forwarded")
	}
	if !relaySurvivesTurnBoundary("codex-web") {
		t.Fatal("codex-web relay must stay up after turn_completed so Mac Desktop turns keep streaming")
	}
	if relaySurvivesTurnBoundary("codex") {
		t.Fatal("codex relay still exits on EventResult (historical contract)")
	}
}

func TestClearOpenCodeServerAuthEnv(t *testing.T) {
	t.Setenv("OPENCODE_SERVER_USERNAME", "user")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "pass")

	clearOpenCodeServerAuthEnv()

	if got := os.Getenv("OPENCODE_SERVER_USERNAME"); got != "" {
		t.Fatalf("expected OPENCODE_SERVER_USERNAME to be cleared, got %q", got)
	}
	if got := os.Getenv("OPENCODE_SERVER_PASSWORD"); got != "" {
		t.Fatalf("expected OPENCODE_SERVER_PASSWORD to be cleared, got %q", got)
	}
}

func TestRuntimeVersionStringUsesProductBinaryName(t *testing.T) {
	got := runtimeVersionString()
	if !strings.HasPrefix(got, runtimeBinaryName+" ") {
		t.Fatalf("runtimeVersionString() = %q, want prefix %q", got, runtimeBinaryName+" ")
	}
	if !strings.Contains(got, runtimeVersion) {
		t.Fatalf("runtimeVersionString() = %q, want version %q", got, runtimeVersion)
	}
}

func TestLoadStableBridgeIDUsesPersistedIdentity(t *testing.T) {
	dataDir := NewDataDir(t.TempDir())
	if err := dataDir.Initialize(); err != nil {
		t.Fatal(err)
	}
	identity, err := dataDir.ReadIdentity()
	if err != nil {
		t.Fatal(err)
	}

	got, err := loadStableBridgeID(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != identity.BridgeID {
		t.Fatalf("loadStableBridgeID() = %q, want persisted %q", got, identity.BridgeID)
	}
}

func TestLocalRelayServiceListenAddressOnlyAllowsLoopback(t *testing.T) {
	for input, expected := range map[string]string{
		":8788":          "127.0.0.1:8788",
		"127.0.0.1:8788": "127.0.0.1:8788",
		"localhost:8788": "localhost:8788",
		"[::1]:8788":     "[::1]:8788",
	} {
		got, err := localRelayServiceListenAddress(input)
		if err != nil || got != expected {
			t.Fatalf("localRelayServiceListenAddress(%q) = %q, %v, want %q", input, got, err, expected)
		}
	}
	for _, input := range []string{"0.0.0.0:8788", ":bad", "203.0.113.10:8788"} {
		if got, err := localRelayServiceListenAddress(input); err == nil {
			t.Fatalf("localRelayServiceListenAddress(%q) = %q, want rejection", input, got)
		}
	}
}
