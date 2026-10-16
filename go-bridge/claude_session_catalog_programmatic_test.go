package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func writeClaudeEntrypointFixture(t *testing.T, path, directory, id, entrypoint, sessionKind, title string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{
		"type":       "user",
		"timestamp":  "2026-10-16T01:00:00Z",
		"cwd":        directory,
		"parentUuid": nil,
		"message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": "prompt"}},
		},
	}
	if entrypoint != "" {
		row["entrypoint"] = entrypoint
	}
	if sessionKind != "" {
		row["sessionKind"] = sessionKind
	}
	userJSON, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := json.Marshal(map[string]any{
		"type":      "assistant",
		"timestamp": "2026-10-16T01:01:00Z",
		"cwd":       directory,
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": title}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	content := string(userJSON) + "\n" + string(assistant) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSessionCatalogHidesProgrammaticButRetainsCacheIdentity(t *testing.T) {
	projectsDir := t.TempDir()
	ws := catalogFixtureWorkspace(t, projectsDir, "programmatic")
	projectDir := filepath.Join(projectsDir, "-tmp-programmatic")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		id          string
		entrypoint  string
		sessionKind string
	}{
		{id: "sdk-py-session", entrypoint: "sdk-py"},
		{id: "sdk-ts-session", entrypoint: "sdk-ts"},
		{id: "sdk-cli-session", entrypoint: "sdk-cli"},
		{id: "daemon-session", sessionKind: "daemon"},
		{id: "daemon-worker-session", sessionKind: "daemon-worker"},
		{id: "desktop-session", entrypoint: "claude-desktop"},
		{id: "vscode-session", entrypoint: "claude-vscode"},
		{id: "normal-cli-session"},
	}
	for _, fixture := range fixtures {
		writeClaudeEntrypointFixture(t, filepath.Join(projectDir, fixture.id+".jsonl"), ws, fixture.id, fixture.entrypoint, fixture.sessionKind, fixture.id)
	}

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("", &core.SessionLoadMetrics{})
	ids := make(map[string]bool, len(got))
	for _, session := range got {
		id, _ := session["id"].(string)
		ids[id] = true
	}
	for _, fixture := range fixtures {
		programmatic := fixture.entrypoint == "sdk-py" || fixture.entrypoint == "sdk-ts" || fixture.entrypoint == "sdk-cli" || fixture.sessionKind == "daemon" || fixture.sessionKind == "daemon-worker"
		if ids[fixture.id] == programmatic {
			t.Fatalf("visibility(%s) = %v, want %v; list=%v", fixture.id, ids[fixture.id], !programmatic, ids)
		}
		if catalog.snapshot.ByKey[claudeSessionKey{ProjectKey: "-tmp-programmatic", SessionID: fixture.id}].FilePath == "" {
			t.Fatalf("programmatic/session %s missing from ByKey cache identity", fixture.id)
		}
	}
}

func TestClaudeSessionCatalogProgrammaticFilterPreservesArchivedWireContract(t *testing.T) {
	projectsDir := t.TempDir()
	ws := catalogFixtureWorkspace(t, projectsDir, "archived")
	projectDir := filepath.Join(projectsDir, "-tmp-archived")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "archived-session.jsonl")
	writeClaudeEntrypointFixture(t, path, ws, "archived-session", "claude-desktop", "", "archived")
	metaDir := filepath.Join(projectDir, ".cc-connect-session-meta")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "archived-session.json"), []byte(`{"archivedAtMillis":1792100000000}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := newClaudeSessionCatalog(projectsDir).list("", &core.SessionLoadMetrics{})
	if len(got) != 1 {
		t.Fatalf("archived wire list = %#v, want one entry", got)
	}
	if _, ok := got[0]["archivedAtMillis"]; !ok {
		t.Fatalf("archived entry lacks archivedAtMillis: %#v", got[0])
	}
}

func TestClaudeSessionCatalogProgrammaticFilterRunsBeforeLineage(t *testing.T) {
	projectsDir := t.TempDir()
	ws := catalogFixtureWorkspace(t, projectsDir, "lineage")
	projectDir := filepath.Join(projectsDir, "-tmp-lineage")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeProgrammaticLineageFixture(t, filepath.Join(projectDir, "programmatic-newer.jsonl"), ws, "sdk-py", "2026-10-16T01:00:00Z", "2026-10-16T03:00:00Z")
	writeClaudeProgrammaticLineageFixture(t, filepath.Join(projectDir, "human-older.jsonl"), ws, "claude-desktop", "2026-10-16T01:00:00Z", "2026-10-16T02:00:00Z")

	catalog := newClaudeSessionCatalog(projectsDir)
	got := catalog.list("", &core.SessionLoadMetrics{})
	if len(got) != 1 || got[0]["id"] != "human-older" {
		t.Fatalf("visible lineage result = %#v, want human-older only", got)
	}
	key := claudeSessionKey{ProjectKey: "-tmp-lineage", SessionID: "programmatic-newer"}
	if catalog.snapshot.ByKey[key].FilePath == "" {
		t.Fatal("programmatic fork peer should remain in ByKey")
	}
	if _, ok := catalog.snapshot.ByKey[claudeSessionKey{ProjectKey: "-tmp-lineage", SessionID: "human-older"}]; !ok {
		t.Fatal("human fork primary missing from ByKey")
	}
}

func writeClaudeProgrammaticLineageFixture(t *testing.T, path, directory, entrypoint, firstUserAt, assistantAt string) {
	t.Helper()
	rows := []string{
		`{"type":"user","timestamp":"` + firstUserAt + `","cwd":"` + directory + `","entrypoint":"` + entrypoint + `","parentUuid":null,"message":{"role":"user","content":[{"type":"text","text":"same prompt"}]}}`,
		`{"type":"custom-title","customTitle":"same logical session","sessionId":"` + filepath.Base(path) + `"}`,
		`{"type":"assistant","timestamp":"` + assistantAt + `","cwd":"` + directory + `","entrypoint":"` + entrypoint + `","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSessionCatalogCachesLargeTailOnlyProgrammaticSession(t *testing.T) {
	projectsDir := t.TempDir()
	ws := catalogFixtureWorkspace(t, projectsDir, "large")
	projectDir := filepath.Join(projectsDir, "-tmp-large")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "tail-only-session.jsonl")
	var builder strings.Builder
	builder.WriteString(`{"type":"user","timestamp":"2026-10-16T01:00:00Z","cwd":"` + ws + `","parentUuid":null,"padding":"` + strings.Repeat("a", 384*1024) + `"}` + "\n")
	builder.WriteString(`{"type":"assistant","timestamp":"2026-10-16T01:01:00Z","cwd":"` + ws + `","parentUuid":null,"padding":"` + strings.Repeat("b", 256*1024) + `"}` + "\n")
	builder.WriteString(`{"type":"assistant","timestamp":"2026-10-16T01:02:00Z","cwd":"` + ws + `","entrypoint":"sdk-py","message":{"role":"assistant","content":[{"type":"text","text":"tail only"}]}}` + "\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := newClaudeSessionCatalog(projectsDir)
	var parseCalls atomic.Int32
	catalog.parseSession = func(path string, fallback time.Time) claudeSessionScanResult {
		parseCalls.Add(1)
		return scanClaudeSessionMetadata(path, fallback)
	}
	for i := 0; i < 2; i++ {
		if got := catalog.list("", &core.SessionLoadMetrics{}); len(got) != 0 {
			t.Fatalf("refresh %d returned hidden session: %#v", i+1, got)
		}
	}
	if parseCalls.Load() != 1 {
		t.Fatalf("parseCalls = %d, want one cached parse", parseCalls.Load())
	}
	key := claudeSessionKey{ProjectKey: "-tmp-large", SessionID: "tail-only-session"}
	if !catalog.snapshot.ByKey[key].Programmatic {
		t.Fatalf("tail-only session missing or not programmatic: %#v", catalog.snapshot.ByKey[key])
	}
}

func TestClaudeProgrammaticOracleLocalSnapshot(t *testing.T) {
	projectDir := os.Getenv("CORDCODE_PROGRAMMATIC_ORACLE_DIR")
	outputPath := os.Getenv("CORDCODE_PROGRAMMATIC_ORACLE_OUTPUT")
	if projectDir == "" || outputPath == "" {
		t.Skip("local official-SDK oracle path/output not requested")
	}

	visibleCatalog := newDefaultClaudeSessionCatalog()
	allCatalog := newDefaultClaudeSessionCatalog()
	allCatalog.includeProgrammatic = true
	collect := func(catalog *claudeSessionCatalog) []string {
		sessions := catalog.list("", &core.SessionLoadMetrics{})
		ids := make([]string, 0, len(sessions))
		for _, session := range sessions {
			if session["directory"] == projectDir {
				ids = append(ids, session["id"].(string))
			}
		}
		sort.Strings(ids)
		return ids
	}
	output := map[string]any{
		"all":      collect(allCatalog),
		"visible":  collect(visibleCatalog),
		"project":  projectDir,
		"filtered": false,
	}
	raw, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
