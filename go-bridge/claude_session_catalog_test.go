package gobridge

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestClaudeSessionCatalogIncrementalRefreshAndDeletion(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "session-1.jsonl")
	writeClaudeCatalogFixture(t, sessionPath, "/tmp/project", "first", "2026-06-01T10:00:00Z")

	catalog := newClaudeSessionCatalog(projectsDir)
	var parseCalls atomic.Int32
	catalog.parseSession = func(path string, fallback time.Time) claudeSessionScanResult {
		parseCalls.Add(1)
		return scanClaudeSessionMetadata(path, fallback)
	}

	firstMetrics := &core.SessionLoadMetrics{}
	first := catalog.list("", firstMetrics)
	if len(first) != 1 || parseCalls.Load() != 1 {
		t.Fatalf("first refresh sessions=%d parseCalls=%d", len(first), parseCalls.Load())
	}
	if firstMetrics.Snapshot().CacheChanged != 1 {
		t.Fatalf("first refresh metrics = %+v", firstMetrics.Snapshot())
	}

	hitMetrics := &core.SessionLoadMetrics{}
	second := catalog.list("", hitMetrics)
	if len(second) != 1 || parseCalls.Load() != 1 {
		t.Fatalf("cache hit sessions=%d parseCalls=%d", len(second), parseCalls.Load())
	}
	if !hitMetrics.Snapshot().CacheHit {
		t.Fatalf("second refresh should be a cache hit: %+v", hitMetrics.Snapshot())
	}

	info, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeCatalogFixture(t, sessionPath, "/tmp/project", "changed with same mtime", "2026-06-01T11:00:00Z")
	if err := os.Chtimes(sessionPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changedMetrics := &core.SessionLoadMetrics{}
	changed := catalog.list("", changedMetrics)
	if len(changed) != 1 || parseCalls.Load() != 2 {
		t.Fatalf("size change sessions=%d parseCalls=%d", len(changed), parseCalls.Load())
	}
	if changedMetrics.Snapshot().CacheChanged != 1 {
		t.Fatalf("same-mtime size change not detected: %+v", changedMetrics.Snapshot())
	}

	if err := os.Remove(sessionPath); err != nil {
		t.Fatal(err)
	}
	deletedMetrics := &core.SessionLoadMetrics{}
	if got := catalog.list("", deletedMetrics); len(got) != 0 {
		t.Fatalf("deleted session remained in catalog: %#v", got)
	}
	if deletedMetrics.Snapshot().CacheDeleted != 1 {
		t.Fatalf("deletion metrics = %+v", deletedMetrics.Snapshot())
	}
}

func TestClaudeSessionCatalogKeepsProjectIdentityAndFiltersSnapshot(t *testing.T) {
	projectsDir := t.TempDir()
	for _, fixture := range []struct {
		projectKey string
		directory  string
		title      string
	}{
		{"-tmp-one", "/tmp/one", "one"},
		{"-tmp-two", "/tmp/two", "two"},
	} {
		projectDir := filepath.Join(projectsDir, fixture.projectKey)
		if err := os.Mkdir(projectDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeClaudeCatalogFixture(
			t,
			filepath.Join(projectDir, "same-session.jsonl"),
			fixture.directory,
			fixture.title,
			"2026-06-01T10:00:00Z",
		)
	}

	catalog := newClaudeSessionCatalog(projectsDir)
	all := catalog.list("", &core.SessionLoadMetrics{})
	if len(all) != 2 {
		t.Fatalf("same session id across projects collapsed: %#v", all)
	}
	filtered := catalog.list("-tmp-one", &core.SessionLoadMetrics{})
	if len(filtered) != 1 || filtered[0]["directory"] != "/tmp/one" {
		t.Fatalf("project filter result = %#v", filtered)
	}
}

func TestClaudeSessionCatalogUsesCustomTitle(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "session-1.jsonl")
	content := `{"type":"user","timestamp":"2026-06-01T10:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-06-01T10:01:00Z","cwd":"/tmp/project","message":{"role":"assistant","content":[{"type":"text","text":"assistant fallback"}]}}` + "\n" +
		`{"type":"custom-title","customTitle":"Claude Desktop title","sessionId":"session-1"}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("", &core.SessionLoadMetrics{})
	if len(got) != 1 {
		t.Fatalf("catalog sessions = %#v", got)
	}
	if got[0]["title"] != "Claude Desktop title" {
		t.Fatalf("title = %#v", got[0]["title"])
	}
}

func TestClaudeSessionCatalogUsesAssistantModel(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "session-1.jsonl")
	content := `{"type":"user","timestamp":"2026-06-01T10:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-06-01T10:01:00Z","cwd":"/tmp/project","message":{"role":"assistant","model":"anthropic/claude-sonnet-4","content":[{"type":"text","text":"assistant fallback"}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("", &core.SessionLoadMetrics{})
	if len(got) != 1 {
		t.Fatalf("catalog sessions = %#v", got)
	}
	if got[0]["modelId"] != "anthropic/claude-sonnet-4" {
		t.Fatalf("modelId = %#v", got[0]["modelId"])
	}
	if got[0]["effectiveProviderId"] != "anthropic" {
		t.Fatalf("effectiveProviderId = %#v", got[0]["effectiveProviderId"])
	}
}

func TestClaudeSessionCatalogUsesSidecarModelAndEffort(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "session-1.jsonl")
	content := `{"type":"user","timestamp":"2026-06-01T10:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-06-01T10:01:00Z","cwd":"/tmp/project","message":{"role":"assistant","model":"old-model","content":[{"type":"thinking","thinking":"thinking first"}]}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-06-01T10:02:00Z","cwd":"/tmp/project","message":{"role":"assistant","model":"old-model","content":[{"type":"text","text":"assistant fallback"}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	metaDir := filepath.Join(projectDir, ".cc-connect-session-meta")
	if err := os.Mkdir(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sidecar := `{"modelId":"glm-5.2","providerId":"default","reasoningEffort":"ultra"}` + "\n"
	if err := os.WriteFile(filepath.Join(metaDir, "session-1.json"), []byte(sidecar), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("", &core.SessionLoadMetrics{})
	if len(got) != 1 {
		t.Fatalf("catalog sessions = %#v", got)
	}
	if got[0]["modelId"] != "glm-5.2" {
		t.Fatalf("modelId = %#v", got[0]["modelId"])
	}
	if got[0]["effectiveProviderId"] != "default" {
		t.Fatalf("effectiveProviderId = %#v", got[0]["effectiveProviderId"])
	}
	if got[0]["reasoningEffort"] != "ultra" {
		t.Fatalf("reasoningEffort = %#v", got[0]["reasoningEffort"])
	}
	if got[0]["title"] != "assistant fallback" {
		t.Fatalf("title = %#v", got[0]["title"])
	}
}

func TestClaudeSessionCatalogReadersReuseSnapshotDuringRefresh(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "session-1.jsonl")
	writeClaudeCatalogFixture(t, sessionPath, "/tmp/project", "first", "2026-06-01T10:00:00Z")

	catalog := newClaudeSessionCatalog(projectsDir)
	if got := catalog.list("", &core.SessionLoadMetrics{}); len(got) != 1 {
		t.Fatalf("initial catalog = %#v", got)
	}
	writeClaudeCatalogFixture(t, sessionPath, "/tmp/project", "changed", "2026-06-01T11:00:00Z")

	parseStarted := make(chan struct{})
	releaseParse := make(chan struct{})
	catalog.parseSession = func(path string, fallback time.Time) claudeSessionScanResult {
		close(parseStarted)
		<-releaseParse
		return scanClaudeSessionMetadata(path, fallback)
	}
	refreshDone := make(chan struct{})
	go func() {
		catalog.list("", &core.SessionLoadMetrics{})
		close(refreshDone)
	}()
	<-parseStarted

	readDone := make(chan []map[string]interface{}, 1)
	go func() {
		readDone <- catalog.list("", &core.SessionLoadMetrics{})
	}()
	select {
	case result := <-readDone:
		if len(result) != 1 {
			t.Fatalf("snapshot read = %#v", result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("reader blocked on refresh file parsing")
	}
	close(releaseParse)
	<-refreshDone
}

func writeClaudeCatalogFixture(t *testing.T, path, directory, title, timestamp string) {
	t.Helper()
	content := `{"type":"user","timestamp":"` + timestamp + `","cwd":"` + directory + `","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}` + "\n" +
		`{"type":"assistant","timestamp":"` + timestamp + `","cwd":"` + directory + `","message":{"role":"assistant","content":[{"type":"text","text":"` + title + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeForkFixture 写一个带 custom-title 和首条 user 消息的 JSONL，用于 fork 检测测试。
// firstUserTs 是首条 user 消息的 timestamp（fork 对要求完全相同）；
// assistantTs 用于区分两个文件的 UpdatedAt（fork 检测保留最新者）。
func writeClaudeForkFixture(t *testing.T, path, directory, customTitle, firstUserTs, assistantTs string) {
	t.Helper()
	content := `{"type":"user","timestamp":"` + firstUserTs + `","cwd":"` + directory + `","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}` + "\n" +
		`{"type":"custom-title","customTitle":"` + customTitle + `","sessionId":"` + filepath.Base(path) + `"}` + "\n" +
		`{"type":"assistant","timestamp":"` + assistantTs + `","cwd":"` + directory + `","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeSessionCatalogHidesForkChildren 验证：同目录下 custom-title + 首条用户 timestamp
// 都相同的两个会话被视为 Claude Code fork 对，较旧的被隐藏，只保留最新的。
func TestClaudeSessionCatalogHidesForkChildren(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-fork-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	directory := "/tmp/fork-project"
	// fork 对：相同 custom-title + 相同首条 user timestamp，不同的 assistant timestamp（区分新旧）
	writeClaudeForkFixture(t,
		filepath.Join(projectDir, "older-session.jsonl"),
		directory, "Header regression bug",
		"2026-07-07T13:22:32.902Z", // 相同首条 user ts
		"2026-07-07T17:18:58.000Z") // 旧的 assistant ts
	writeClaudeForkFixture(t,
		filepath.Join(projectDir, "newer-session.jsonl"),
		directory, "Header regression bug",
		"2026-07-07T13:22:32.902Z", // 相同首条 user ts
		"2026-07-08T02:10:05.000Z") // 新的 assistant ts

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("-tmp-fork-project", &core.SessionLoadMetrics{})
	if len(got) != 1 {
		t.Fatalf("fork pair should collapse to 1 session, got %d: %+v", len(got), got)
	}
	if got[0]["id"] != "newer-session" {
		t.Fatalf("should keep newer session, got id=%v", got[0]["id"])
	}
	if got[0]["title"] != "Header regression bug" {
		t.Fatalf("unexpected title: %v", got[0]["title"])
	}
}

// TestClaudeSessionCatalogKeepsDifferentFirstUserTs 验证：custom-title 相同但首条用户
// timestamp 不同的两个会话不是 fork，两条都保留（防误伤）。
func TestClaudeSessionCatalogKeepsDifferentFirstUserTs(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-nofork-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	directory := "/tmp/nofork-project"
	writeClaudeForkFixture(t,
		filepath.Join(projectDir, "session-a.jsonl"),
		directory, "Same title",
		"2026-07-01T10:00:00.000Z", // 不同首条 user ts
		"2026-07-01T11:00:00.000Z")
	writeClaudeForkFixture(t,
		filepath.Join(projectDir, "session-b.jsonl"),
		directory, "Same title",
		"2026-07-02T10:00:00.000Z", // 不同首条 user ts
		"2026-07-02T11:00:00.000Z")

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("-tmp-nofork-project", &core.SessionLoadMetrics{})
	if len(got) != 2 {
		t.Fatalf("non-fork pair (different first-user-ts) should keep both, got %d: %+v", len(got), got)
	}
}

// TestClaudeSessionCatalogKeepsSessionsWithoutCustomTitle 验证：没有 custom-title
// （只有 assistant 文本回退 title）的会话不参与 fork 检测，即使首条 user ts 相同也保留。
func TestClaudeSessionCatalogKeepsSessionsWithoutCustomTitle(t *testing.T) {
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-notitle-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 两个会话首条 user ts 相同，但都没有 custom-title（只有 assistant 回退 title）。
	// 不应被当成 fork。
	writeClaudeCatalogFixture(t,
		filepath.Join(projectDir, "a-no-title.jsonl"),
		"/tmp/notitle", "Same assistant fallback",
		"2026-07-01T10:00:00Z")
	writeClaudeCatalogFixture(t,
		filepath.Join(projectDir, "b-no-title.jsonl"),
		"/tmp/notitle", "Same assistant fallback",
		"2026-07-01T10:00:00Z")

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("-tmp-notitle-project", &core.SessionLoadMetrics{})
	if len(got) != 2 {
		t.Fatalf("sessions without custom-title should not be fork-deduped, got %d: %+v", len(got), got)
	}
}
