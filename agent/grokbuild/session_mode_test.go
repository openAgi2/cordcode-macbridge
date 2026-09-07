package grokbuild

// session_mode_test.go — typed 模式读侧定向测试（方案 2026-09-07 §5.1/§5.3，
// P7 阻断下的禁用+诊断交付）：P8 权威读映射、CanSet 恒 false + 稳定原因码、
// 缓存与 CMU dirty 重读、会话隔离失效、目录缺失→unknown。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeModeFixture(t *testing.T, home, cwd, sessionID, state string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", urlEncodePath(cwd), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"info":{"id":"` + sessionID + `","cwd":"` + cwd + `"},"updated_at":"2026-09-07T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if state != "" {
		if err := os.WriteFile(filepath.Join(dir, "plan_mode.json"), []byte(`{"state":"`+state+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModeStateFromPersistentOfficialRecoveryMapping(t *testing.T) {
	cases := []struct {
		state   string
		status  string
		mode    string // "" = Mode must be nil
	}{
		{"Active", "confirmed", "plan"},
		// 官方恢复语义（P8）：Pending/ExitPending/Inactive 恢复后都不是 plan。
		{"Pending", "confirmed", "default"},
		{"ExitPending", "confirmed", "default"},
		{"Inactive", "confirmed", "default"},
		// 未知枚举不强转 default；缺失/损坏同路（readPlanModeState 归一为 ""）。
		{"WeirdFutureState", "unknown", ""},
		{"", "unknown", ""},
	}
	for _, c := range cases {
		got := modeStateFromPersistent(c.state)
		if got.Status != c.status {
			t.Errorf("state %q: status = %q, want %q", c.state, got.Status, c.status)
		}
		if c.mode == "" && got.Mode != nil {
			t.Errorf("state %q: mode must be nil, got %q", c.state, *got.Mode)
		}
		if c.mode != "" && (got.Mode == nil || *got.Mode != c.mode) {
			t.Errorf("state %q: mode = %v, want %q", c.state, got.Mode, c.mode)
		}
		// P7 阻断：任何状态下写入面禁用 + 稳定原因码。
		if got.CanSet {
			t.Errorf("state %q: CanSet must be false (P7 blocked)", c.state)
		}
		if got.Reason != grokModeSwitchBlockedReason {
			t.Errorf("state %q: reason = %q", c.state, got.Reason)
		}
	}
}

func TestGetSessionModeReadsAuthoritativeFile(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeModeFixture(t, home, cwd, "sess-a", "Active")
	writeModeFixture(t, home, cwd, "sess-b", "Pending")
	// sess-c：无 plan_mode.json（新会话，set plan 前不存在）→ unknown。
	writeModeFixture(t, home, cwd, "sess-c", "")

	a := newModeTestAgent(t, home, cwd)
	ctx := context.Background()

	if got, _ := a.GetSessionMode(ctx, "sess-a"); got.Status != "confirmed" || got.Mode == nil || *got.Mode != "plan" {
		t.Fatalf("sess-a = %+v, want confirmed/plan", got)
	}
	if got, _ := a.GetSessionMode(ctx, "sess-b"); got.Status != "confirmed" || got.Mode == nil || *got.Mode != "default" {
		t.Fatalf("sess-b = %+v, want confirmed/default (official restore drops plan)", got)
	}
	if got, _ := a.GetSessionMode(ctx, "sess-c"); got.Status != "unknown" || got.Mode != nil {
		t.Fatalf("sess-c = %+v, want unknown with nil mode", got)
	}
	// 目录不存在的会话：unknown 值，不是错误。
	if got, err := a.GetSessionMode(ctx, "sess-void"); err != nil || got.Status != "unknown" {
		t.Fatalf("sess-void = (%+v, %v), want (unknown, nil)", got, err)
	}
}

func TestGetSessionModeCacheAndCMUDirtyReread(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	sid := "sess-d"
	writeModeFixture(t, home, cwd, sid, "Active")
	a := newModeTestAgent(t, home, cwd)
	ctx := context.Background()

	if got, _ := a.GetSessionMode(ctx, sid); got.Mode == nil || *got.Mode != "plan" {
		t.Fatalf("first read = %+v", got)
	}
	// 外部改写文件（模拟外部 actor set plan off）——缓存未失效：仍 plan。
	dir := filepath.Join(home, "sessions", urlEncodePath(cwd), sid)
	if err := os.WriteFile(filepath.Join(dir, "plan_mode.json"), []byte(`{"state":"Inactive"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.GetSessionMode(ctx, sid); got.Mode == nil || *got.Mode != "plan" {
		t.Fatalf("cached read should hold plan until dirty, got %+v", got)
	}
	// CMU 只置 dirty（§5.3），下一次读重读权威文件 → default（官方恢复语义）。
	a.modeSide.markDirty(sid)
	if got, _ := a.GetSessionMode(ctx, sid); got.Mode == nil || *got.Mode != "default" {
		t.Fatalf("post-CMU read must re-read file, got %+v", got)
	}
}

func TestModeSideInvalidateSemantics(t *testing.T) {
	m := newModeSideState()
	m.cached["s1"] = confirmedModeState("plan")
	m.cached["s2"] = confirmedModeState("plan")
	m.markDirty("s1")

	m.invalidateSession("s1")
	if _, ok := m.cached["s1"]; ok {
		t.Fatal("invalidateSession must drop s1")
	}
	if _, ok := m.cached["s2"]; !ok {
		t.Fatal("invalidateSession(s1) must not touch s2")
	}

	m.invalidateAll()
	if len(m.cached) != 0 || len(m.dirty) != 0 {
		t.Fatal("invalidateAll must clear everything")
	}
}

func newModeTestAgent(t *testing.T, home, cwd string) *Agent {
	t.Helper()
	a, err := New(map[string]any{"grok_home": home, "cli_path": "/usr/bin/true", "work_dir": cwd})
	if err != nil {
		t.Fatal(err)
	}
	ga, ok := a.(*Agent)
	if !ok {
		t.Fatalf("New returned %T", a)
	}
	return ga
}
