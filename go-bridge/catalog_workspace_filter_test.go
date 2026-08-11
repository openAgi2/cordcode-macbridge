package gobridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterSessionsMissingWorkspace_DropsDeletedDirs(t *testing.T) {
	alive := t.TempDir()
	missing := filepath.Join(t.TempDir(), "deleted-project")
	// missing 故意不创建

	// /private/tmp 若存在则可能通过存在性检查；本测试只断言 deleted + 空 directory 被丢。
	in := []map[string]interface{}{
		{"id": "a", "directory": alive, "title": "live"},
		{"id": "b", "directory": missing, "title": "ghost"},
		{"id": "c", "directory": "", "title": "no-dir"},
		{"id": "d", "directory": "/", "title": "root"},
	}
	out := filterSessionsMissingWorkspace(in)
	if len(out) != 1 {
		t.Fatalf("len=%d want 1, out=%#v", len(out), out)
	}
	if out[0]["id"] != "a" {
		t.Fatalf("kept id=%v want a", out[0]["id"])
	}
}

func TestSessionWorkspaceVisibleForCatalog_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	if !sessionWorkspaceVisibleForCatalog(dir) {
		t.Fatalf("existing dir should be visible: %s", dir)
	}
	if sessionWorkspaceVisibleForCatalog(filepath.Join(dir, "nope")) {
		t.Fatal("missing dir must be hidden")
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sessionWorkspaceVisibleForCatalog(filepath.Join(dir, "file")) {
		t.Fatal("file path must be hidden (need directory)")
	}
	if sessionWorkspaceVisibleForCatalog("/") {
		t.Fatal("filesystem root must not be a project")
	}
}

func TestFilterCodexCatalogSessions_UsesWorkspaceRoots(t *testing.T) {
	resetCodexWorkspaceRootsCacheForTest()
	t.Cleanup(resetCodexWorkspaceRootsCacheForTest)
	prev := loadCodexWorkspaceRootsFn
	loadCodexWorkspaceRootsFn = loadCodexWorkspaceRoots
	t.Cleanup(func() { loadCodexWorkspaceRootsFn = prev })

	rootA := t.TempDir()
	rootB := t.TempDir()
	orphan := t.TempDir() // 磁盘在，但不在 Mac saved roots
	nestedBridge := filepath.Join(rootA, "bridge")
	if err := os.MkdirAll(nestedBridge, 0o755); err != nil {
		t.Fatal(err)
	}

	// 注入假 global-state：只登记 rootA（不含 orphan、不含 /）。
	statePath := filepath.Join(t.TempDir(), ".codex-global-state.json")
	body := `{"electron-saved-workspace-roots":["` + rootA + `"],"local-projects":{}}`
	if err := os.WriteFile(statePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORDCODE_CODEX_GLOBAL_STATE", statePath)

	in := []map[string]interface{}{
		{"id": "1", "directory": rootA},
		{"id": "2", "directory": nestedBridge}, // 应归一到 rootA
		{"id": "3", "directory": orphan},       // 未登记 → 丢
		{"id": "4", "directory": "/"},          // 非工程 → 丢
		{"id": "5", "directory": rootB},        // 未登记 → 丢
	}
	out := filterCodexCatalogSessions(in)
	if len(out) != 2 {
		t.Fatalf("len=%d want 2, out=%#v", len(out), out)
	}
	for _, s := range out {
		if s["directory"] != rootA {
			t.Fatalf("directory = %v, want normalized rootA %s", s["directory"], rootA)
		}
	}
}

func TestMatchCodexWorkspaceRoot_HomeOnlyExact(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Clean(home)
	proj := filepath.Join(home, "Projects", "demo-app")
	roots := []string{home, filepath.Join(home, "Projects", "other")}

	if r, ok := matchCodexWorkspaceRoot(home, roots); !ok || r != home {
		t.Fatalf("exact home: root=%q ok=%v", r, ok)
	}
	// 未登记子路径不得被 home 吞掉
	if _, ok := matchCodexWorkspaceRoot(proj, roots); ok {
		t.Fatal("nested under home-only must not match via home prefix")
	}
	// 登记的具体 root 可前缀匹配
	other := filepath.Join(home, "Projects", "other")
	nested := filepath.Join(other, "pkg")
	if r, ok := matchCodexWorkspaceRoot(nested, []string{home, other}); !ok || r != other {
		t.Fatalf("nested under registered root: root=%q ok=%v want %s", r, ok, other)
	}
}
