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

func TestShouldStartPassiveSubscription_CodexRequiresExplicitSharedURL(t *testing.T) {
	if shouldStartPassiveSubscription("codex", "app_server", "", "") {
		t.Fatal("codex implicit app_server should not start process-level passive subscription")
	}
	if !shouldStartPassiveSubscription("codex", "app_server", "ws://127.0.0.1:4141", "") {
		t.Fatal("codex explicit shared app_server URL should start passive subscription")
	}
	if shouldStartPassiveSubscription("codex", "exec", "ws://127.0.0.1:4141", "") {
		t.Fatal("codex exec mode should not start app-server passive subscription")
	}
	// OpenCode: 无 URL（endpoint 未配置）不得启动 SSE 订阅，避免无意义重连退避。
	if shouldStartPassiveSubscription("opencode", "", "", "") {
		t.Fatal("opencode without server URL should not start passive subscription")
	}
	if !shouldStartPassiveSubscription("opencode", "", "", "http://127.0.0.1:4096") {
		t.Fatal("opencode with a configured server URL should start passive subscription")
	}
}

func TestDisablesRelayIdleTimeoutIncludesOpenCode(t *testing.T) {
	if !disablesRelayIdleTimeout("opencode") {
		t.Fatal("opencode relay idle timeout should be disabled")
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
