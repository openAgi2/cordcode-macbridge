package codexweb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestGetSessionContextUsageReadsOfficialThreadPathTokenCount(t *testing.T) {
	// 契约 fixture（审计 §3.3-C1-1）：testdata 冻结的脱敏真实 token_count 记录。
	fixture, err := os.ReadFile(filepath.Join("testdata", "official-0.149.0-alpha.4", "dumps", "usage", "rollout-tail.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "thread-events")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)
	readCalls := captureParams(s, "thread/read", map[string]any{
		"thread": map[string]any{"id": "thread-existing", "path": path},
	})
	a := New(nil)
	a.endpoint = &ServiceEndpoint{Source: SourceExternalDaemonReused, client: cl, CLIVersion: "0.149.0-alpha.4"}

	usage, err := a.GetSessionContextUsage(context.Background(), "thread-existing")
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.UsedTokens != 125711 || usage.ContextWindow != 258400 {
		t.Fatalf("context occupancy = %+v", usage)
	}
	if usage.TotalTokens != 24569173 || usage.InputTokens != 124945 || usage.CachedInputTokens != 113408 {
		t.Fatalf("context accounting = %+v", usage)
	}
	expectParams(t, (*readCalls)[0], map[string]any{"threadId": "thread-existing"})

	// Every initial snapshot re-reads the official path so a Mac-side turn that
	// completed without an attached bridge listener cannot leave stale usage.
	if _, err := a.GetSessionContextUsage(context.Background(), "thread-existing"); err != nil {
		t.Fatal(err)
	}
	if len(*readCalls) != 2 {
		t.Fatalf("thread/read calls=%d, want 2", len(*readCalls))
	}
}

func TestAgentCachesOfficialContextUsageWithoutSessionListener(t *testing.T) {
	a := New(nil)
	a.dispatchEvent(core.Event{
		Type:      core.EventContextUsageUpdated,
		SessionID: "thread-passive",
		ContextUsage: &core.ContextUsage{
			UsedTokens: 73000, ContextWindow: 258400,
		},
	})
	usage := a.cachedContextUsage("thread-passive")
	if usage == nil || usage.UsedTokens != 73000 || usage.ContextWindow != 258400 {
		t.Fatalf("cached usage = %+v", usage)
	}
}

// TestPersistedUsageVersionGateSkipsFileForUnverifiedCLI（审计 §3.3-C1-2）：
// CLI 版本不在已验证版本族 → 不走文件路径（文件有效也不读），回 cache。
func TestPersistedUsageVersionGateSkipsFileForUnverifiedCLI(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "official-0.149.0-alpha.4", "dumps", "usage", "rollout-tail.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "thread-events")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)
	captureParams(s, "thread/read", map[string]any{
		"thread": map[string]any{"id": "thread-v", "path": path},
	})
	a := New(nil)
	a.endpoint = &ServiceEndpoint{Source: SourceExternalDaemonReused, client: cl, CLIVersion: "0.150.0"}

	usage, err := a.GetSessionContextUsage(context.Background(), "thread-v")
	if err != nil {
		t.Fatal(err)
	}
	if usage != nil {
		t.Fatalf("unverified CLI version must skip file path, got %+v", usage)
	}
}

// TestPersistedUsageShapeMismatchAbandonsFile（审计 §3.3-C1-1）：最新 token_count
// 记录 Info 缺失（官方 Option 语义 null）→ 弃用文件路径（nil, nil），不产出值。
func TestPersistedUsageShapeMismatchAbandonsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread-events")
	contents := "{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":null}}\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	usage, err := readPersistedContextUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if usage != nil {
		t.Fatalf("shape mismatch must abandon file path, got %+v", usage)
	}
}

// TestCLIVersionAllowsPersistedUsage：版本族门控单测（当前 0.149.x 族）。
func TestCLIVersionAllowsPersistedUsage(t *testing.T) {
	cases := map[string]bool{
		"0.149.0-alpha.4":  true,
		"0.149.1":          true,
		"0.150.0":          false,
		"0.148.0-alpha.21": false,
		"":                 false,
		"test":             false,
	}
	for version, want := range cases {
		if got := cliVersionAllowsPersistedUsage(version); got != want {
			t.Fatalf("cliVersionAllowsPersistedUsage(%q) = %v, want %v", version, got, want)
		}
	}
}
