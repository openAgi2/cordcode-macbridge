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
	catalog.parseSummary = func(path string, fallback time.Time) (string, time.Time, time.Time) {
		parseCalls.Add(1)
		return scanClaudeSessionSummary(path, fallback)
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
	catalog.parseSummary = func(path string, fallback time.Time) (string, time.Time, time.Time) {
		close(parseStarted)
		<-releaseParse
		return scanClaudeSessionSummary(path, fallback)
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
