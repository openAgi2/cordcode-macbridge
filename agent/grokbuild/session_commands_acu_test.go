package grokbuild

// session_commands_acu_test.go — 1a 目录方向定向测试（方案 §9 目录组可单测部分）：
// ACU 解析（真实样本形状）、side-state 语义（整表替换/失败禁用/迟到不覆盖/隔离）、
// D1 准入交集、readiness 广告门、catalog 单例 List 的 fake 进程 e2e（2026-09-07
// 通道重做：initialize/authenticate + `_x.ai/commands/list`；全表/空表/ext 错误
// 失败标记不可用）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// --- parseAvailableCommandsUpdate（真实样本形状，phase0 p2/p7-14） ---

func TestParseAvailableCommandsUpdate(t *testing.T) {
	full := `{"sessionId":"01a07b3a-0000","update":{"sessionUpdate":"available_commands_update","availableCommands":[` +
		`{"name":"compact","description":"Compress","input":{"hint":"optional context"}},` +
		`{"name":"context","description":"Show context","input":null},` +
		`{"name":"code-review:code-review","description":"namespaced","input":null}]}}`
	sid, cmds, ok := parseAvailableCommandsUpdate(json.RawMessage(full))
	if !ok || sid != "01a07b3a-0000" || len(cmds) != 3 {
		t.Fatalf("full wave: ok=%v sid=%q len=%d", ok, sid, len(cmds))
	}
	if cmds[0].Name != "compact" || cmds[0].Hint != "optional context" || cmds[0].Description != "Compress" {
		t.Errorf("hint mapping wrong: %+v", cmds[0])
	}
	if cmds[1].Hint != "" {
		t.Errorf("null input must map to empty hint, got %q", cmds[1].Hint)
	}
	if cmds[2].Name != "code-review:code-review" {
		t.Errorf("namespaced name must pass through verbatim, got %q", cmds[2].Name)
	}

	empty := `{"sessionId":"s2","update":{"sessionUpdate":"available_commands_update","availableCommands":[]}}`
	sid2, cmds2, ok2 := parseAvailableCommandsUpdate(json.RawMessage(empty))
	if !ok2 || sid2 != "s2" || len(cmds2) != 0 {
		t.Fatalf("empty table is a legal table: ok=%v len=%d", ok2, len(cmds2))
	}

	other := `{"sessionId":"s3","update":{"sessionUpdate":"current_mode_update","currentModeId":"plan"}}`
	if _, _, ok3 := parseAvailableCommandsUpdate(json.RawMessage(other)); ok3 {
		t.Error("current_mode_update must not parse as ACU")
	}
}

// --- ACU side-state 语义（§9 目录组：空表替换/失败不执行旧表/迟到 ACU 不覆盖新表/跨会话隔离） ---

func TestACUSideStateSemantics(t *testing.T) {
	s := newACUSideState()
	c := func(names ...string) []core.SessionCommand {
		out := make([]core.SessionCommand, 0, len(names))
		for _, n := range names {
			out = append(out, core.SessionCommand{Name: n})
		}
		return out
	}

	// 1. notification slot stores, empty table replaces non-empty.
	s.storeNotification("sA", "/w", c("compact", "hooks-list"))
	if got, ok := s.executeWhitelist("sA", "/w"); !ok || len(got) != 2 {
		t.Fatalf("whitelist after notif: ok=%v len=%d", ok, len(got))
	}
	s.storeNotification("sA", "/w", c())
	if got, ok := s.executeWhitelist("sA", "/w"); !ok || len(got) != 0 {
		t.Fatalf("empty table must replace: ok=%v len=%d", ok, len(got))
	}

	// 2. list success wins over notification; late differing ACU marks refresh
	// but must NOT clobber the list value (§4.1 无法排序的 ACU 不覆盖较新 List 值).
	s.storeListSuccess("sA", "/w", c("compact", "goal", "workflow"))
	s.storeNotification("sA", "/w", c("stale-wave"))
	got, ok := s.executeWhitelist("sA", "/w")
	if !ok || len(got) != 3 || got[0].Name != "compact" {
		t.Fatalf("late unordered ACU must not clobber list value: ok=%v len=%d", ok, len(got))
	}

	// 3. failed list → identity unavailable; no stale fallback.
	s.markListFailed("sA", "/w")
	if _, ok := s.executeWhitelist("sA", "/w"); ok {
		t.Fatal("after failed pull the identity must refuse the whitelist")
	}
	// recovery: a fresh successful pull restores.
	s.storeListSuccess("sA", "/w", c("compact"))
	if got, ok := s.executeWhitelist("sA", "/w"); !ok || len(got) != 1 {
		t.Fatalf("fresh pull must restore whitelist: ok=%v", ok)
	}

	// 4. cross-session / cross-cwd isolation (不污染另一 session).
	s.storeListSuccess("sB", "/w", c("goal"))
	s.storeNotification("sA", "/other", c("compact"))
	if got, _ := s.executeWhitelist("sB", "/w"); len(got) != 1 || got[0].Name != "goal" {
		t.Fatalf("sB polluted: %+v", got)
	}
	if _, ok := s.executeWhitelist("sB", "/other"); ok {
		t.Fatal("different cwd key must not resolve")
	}

	// 5. session rebuild invalidation drops only that session.
	s.invalidateSession("sA")
	if _, ok := s.executeWhitelist("sA", "/w"); ok {
		t.Fatal("invalidateSession must drop the entry")
	}
	if got, ok := s.executeWhitelist("sB", "/w"); !ok || len(got) != 1 {
		t.Fatal("invalidateSession(sA) must not touch sB")
	}
	// 6. invalidateAll clears everything (config generation).
	s.invalidateAll()
	if _, ok := s.executeWhitelist("sB", "/w"); ok {
		t.Fatal("invalidateAll must clear all entries")
	}
}

// --- D1 准入交集 ---

func TestApplyGrokAdmission(t *testing.T) {
	origAdmitted, origExcluded := grokAdmittedCommands, grokExcludedCommands
	defer func() { grokAdmittedCommands, grokExcludedCommands = origAdmitted, origExcluded }()

	grokAdmittedCommands = map[string]struct{}{"hooks-list": {}, "context": {}, "novel": {}}
	grokExcludedCommands = map[string]struct{}{"context": {}}

	in := []core.SessionCommand{
		{Name: "hooks-list"}, {Name: "context"}, {Name: "compact"}, {Name: "novel"},
	}
	got := applyGrokAdmission(in)
	if len(got) != 2 || got[0].Name != "hooks-list" || got[1].Name != "novel" {
		t.Fatalf("D1 intersection wrong: %+v", got)
	}

	// Empty-admission mechanism guard: the intersection must collapse to an
	// honest empty panel (the pre-P6 shipped state; P6 has since filled the
	// default table — see TestGrokAdmittedCommandsTerminalTable).
	grokAdmittedCommands = map[string]struct{}{}
	if out := applyGrokAdmission(in); len(out) != 0 {
		t.Fatalf("empty admission must yield empty panel, got %+v", out)
	}
}

// TestGrokAdmittedCommandsTerminalTable pins the shipped D1 admission set to
// the 2026-09-07 owner ruling (compact + goal；hooks-* 5 条移出——owner 不用)。
// Anything else appearing here is an unreviewed admission and must go through
// ADMISSION.md + evidence (or an explicit owner ruling recorded in acu_state.go)
// first.
func TestGrokAdmittedCommandsTerminalTable(t *testing.T) {
	origAdmitted, origExcluded := grokAdmittedCommands, grokExcludedCommands
	defer func() { grokAdmittedCommands, grokExcludedCommands = origAdmitted, origExcluded }()

	want := []string{"compact", "goal"}
	if len(grokAdmittedCommands) != len(want) {
		t.Fatalf("admitted set = %v, want exactly %v", grokAdmittedCommands, want)
	}
	for _, n := range want {
		if _, ok := grokAdmittedCommands[n]; !ok {
			t.Fatalf("admitted set missing %q: %v", n, grokAdmittedCommands)
		}
	}
	for _, n := range []string{"hooks-add", "hooks-list", "hooks-remove", "hooks-trust", "hooks-untrust", "always-approve", "context", "feedback", "dream", "flush", "session-info", "plugins"} {
		if _, ok := grokAdmittedCommands[n]; ok {
			t.Fatalf("%q admitted without owner ruling / P6 evidence — update ADMISSION.md and this guard together", n)
		}
	}
	// Terminal table must remain a subset of the official 1.0.13 session ACU
	// (27 names, samples/p3-acu-session-new.json): anything not in the official
	// catalog is dead weight even if admitted.
	official := []string{"always-approve", "audit-plan", "code-review", "compact", "context", "deep-research",
		"exec-plan", "feature-dev", "feedback", "frontend-design", "goal", "handoff-doc", "hooks-add",
		"hooks-list", "hooks-remove", "hooks-trust", "hooks-untrust", "ios-real-device-doc", "loop",
		"plugins", "reload-plugins", "session-info", "skill-creator", "source-command-handoff-doc",
		"supervise", "takeover", "workflow"}
	officialSet := make(map[string]struct{}, len(official))
	for _, n := range official {
		officialSet[n] = struct{}{}
	}
	for n := range grokAdmittedCommands {
		if _, ok := officialSet[n]; !ok {
			t.Fatalf("admitted %q is not in the official 1.0.13 session ACU", n)
		}
	}
}

// --- readiness 广告门（§5.1 不能仅靠类型断言） ---

type fakeCatalogAgent struct{ core.Agent; ready bool }

func (f *fakeCatalogAgent) ListSessionCommands(ctx context.Context, id string) ([]core.SessionCommand, error) {
	return nil, nil
}
func (f *fakeCatalogAgent) ExecuteSessionCommand(ctx context.Context, id, line string) (core.SessionCommandResult, error) {
	return core.SessionCommandResult{}, nil
}
func (f *fakeCatalogAgent) SessionCommandsReady() bool { return f.ready }

func TestSessionCommandsAdvertiseGate(t *testing.T) {
	// pointer receivers: pass &fakeCatalogAgent, the value type asserts false
	// against SessionCommandCatalog and would pass the first check for the
	// wrong reason.
	if core.SessionCommandsAdvertise(&fakeCatalogAgent{ready: false}) {
		t.Fatal("gated agent not ready must not advertise")
	}
	if !core.SessionCommandsAdvertise(&fakeCatalogAgent{ready: true}) {
		t.Fatal("gated agent ready must advertise")
	}
	// grokbuild itself: catalog implemented; readiness flipped true with p1b
	// (real dispatcher + P6 admission). The gate must still close honestly if
	// a regression flips the atomic back.
	a := &Agent{acu: newACUSideState()}
	orig := grokCommandsReady.Load()
	defer grokCommandsReady.Store(orig)
	if !core.SessionCommandsAdvertise(a) {
		t.Fatal("grokbuild must advertise session_commands with the p1b gate open")
	}
	grokCommandsReady.Store(false)
	if core.SessionCommandsAdvertise(a) {
		t.Fatal("gate closed must not advertise")
	}
}

// --- catalog 单例 List fake e2e（内部故障注入，与产品路径隔离；官方形状由 phase0 真样本守护） ---

// writeFakeGrokCatalogList writes a minimal ACP stdio peer script for the
// catalog singleton (initialize → authenticate → `_x.ai/commands/list`).
// Modes: "table" serves a 3-command table; "empty" serves an empty table;
// "exterr" answers the ext method with a JSON-RPC error.
func writeFakeGrokCatalogList(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-grok")
	table := `[{"name":"compact","description":"d","input":{"hint":"h"}},{"name":"hooks-list","description":"d","input":null},{"name":"context","description":"d","input":null}]`
	if mode == "empty" {
		table = `[]`
	}
	// mode is baked in as a literal: the catalog launcher execs the CLI with
	// argv ("agent", "--no-leader", "stdio"), so an @ARGV-based mode would
	// silently read "agent" and match no branch.
	prog := `#!/usr/bin/perl
use strict; use warnings;
$| = 1; # autoflush — block-buffered stdout would starve the JSON-RPC peer
my $mode = '` + mode + `';
my $table = q(` + table + `);
while (my $line = <STDIN>) {
  last unless defined $line;
  my ($id) = $line =~ /"id":(\d+)/;
  my ($method) = $line =~ /"method":"([^"]+)"/;
  next unless defined $id && defined $method;
  if ($method eq "initialize") {
    print '{"jsonrpc":"2.0","id":' . $id . ',"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"list":{"enabled":true}}},"authMethods":[{"id":"fake"}]}}' . "\n";
  } elsif ($method eq "authenticate") {
    print "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"_meta\":{\"stub\":1}}}\n";
  } elsif ($method eq "_x.ai/commands/list") {
    if ($mode eq "exterr") {
      print "{\"jsonrpc\":\"2.0\",\"id\":$id,\"error\":{\"code\":-32601,\"message\":\"fake ext failure\"}}\n";
    } else {
      print "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"commands\":$table}}\n";
    }
  }
}
exit 0;
`
	if err := os.WriteFile(script, []byte(prog), 0o755); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FAKE_GROK_DUMP") != "" {
		_ = os.WriteFile(os.Getenv("FAKE_GROK_DUMP"), []byte(prog), 0o755)
	}
	return script
}

func TestListSessionCommandsCatalogTable(t *testing.T) {
	cli := writeFakeGrokCatalogList(t, "table")
	a, proj := newListTestAgent(t, cli)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmds, err := a.ListSessionCommands(ctx, "sess-fake")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	// D1 display = official table ∩ admission (2026-09-07 owner 裁决表
	// {compact, goal}): of [compact, hooks-list, context] only compact
	// is admitted (hooks-* 移出、context 排除).
	if len(cmds) != 1 || cmds[0].Name != "compact" || cmds[0].Hint != "h" {
		t.Fatalf("display must be exactly [compact] with hint, got %+v", cmds)
	}
	// Whitelist cache holds the FULL official table (3 cmds).
	got, ok := a.acu.executeWhitelist("sess-fake", proj)
	if !ok || len(got) != 3 || got[0].Name != "compact" {
		t.Fatalf("whitelist must hold full catalog table: ok=%v len=%d first=%+v", ok, len(got), got[0])
	}
}

func TestListSessionCommandsCatalogEmptyTable(t *testing.T) {
	cli := writeFakeGrokCatalogList(t, "empty")
	a, proj := newListTestAgent(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmds, err := a.ListSessionCommands(ctx, "sess-fake")
	if err != nil {
		t.Fatalf("empty table is success, not error: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("empty table must map to empty list, got %+v", cmds)
	}
	if got, ok := a.acu.executeWhitelist("sess-fake", proj); !ok || len(got) != 0 {
		t.Fatalf("empty whitelist must be stored as a legal table: ok=%v len=%d", ok, len(got))
	}
}

func TestListSessionCommandsCatalogExtErrorFails(t *testing.T) {
	cli := writeFakeGrokCatalogList(t, "exterr")
	a, proj := newListTestAgent(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := a.ListSessionCommands(ctx, "sess-fake")
	if err == nil {
		t.Fatal("ext-method error must be a real error")
	}
	// Failure marks the identity unavailable — no stale execution.
	if _, ok := a.acu.executeWhitelist("sess-fake", proj); ok {
		t.Fatal("failed pull must mark identity unavailable")
	}
}

func newListTestAgent(t *testing.T, cli string) (*Agent, string) {
	t.Helper()
	home := t.TempDir()
	proj := t.TempDir()
	sid := "sess-fake"
	sdir := filepath.Join(home, "sessions", urlEncodePath(proj), sid)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"` + sid + `","cwd":"` + proj + `"},"updated_at":"2026-09-07T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(sdir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := New(map[string]any{"grok_home": home, "cli_path": cli, "work_dir": proj})
	if err != nil {
		t.Fatal(err)
	}
	ga, ok := a.(*Agent)
	if !ok {
		t.Fatalf("New returned %T, want *Agent", a)
	}
	return ga, proj
}

func urlEncodePath(p string) string {
	// grok stores sessions under the url-encoded cwd (path-escaped slashes).
	return strings.ReplaceAll(p, "/", "%2F")
}

// Execute fail-closed until the shared turn dispatcher lands (1b).
func TestExecuteSessionCommandFailsClosed(t *testing.T) {
	// p1b: Execute is real — the fail-closed layers are now admission and
	// liveness gates. Deep e2e lives in session_commands_execute_test.go;
	// this keeps the pre-dispatch rejections pinned.
	a := &Agent{acu: newACUSideState()}
	cases := []struct {
		line, wantErr string
	}{
		{"/feedback something", "excluded"},
		{"/hooks-list", "not admitted"},
		{"/goal", "no official catalog"},
		{"plain message", "slash line"},
	}
	for _, tc := range cases {
		_, err := a.ExecuteSessionCommand(context.Background(), "s", tc.line)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("Execute(%q) error = %v, want containing %q", tc.line, err, tc.wantErr)
		}
	}
}
