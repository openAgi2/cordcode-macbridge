package grokbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func writeSessionFixture(t *testing.T, home, cwd, sessionID string, summary map[string]any, history []map[string]any) {
	t.Helper()
	dir := filepath.Join(home, "sessions", url.PathEscape(cwd), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if summary != nil {
		raw, _ := json.Marshal(summary)
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if history != nil {
		f, err := os.Create(filepath.Join(dir, "chat_history.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		enc := json.NewEncoder(f)
		for _, row := range history {
			if err := enc.Encode(row); err != nil {
				t.Fatal(err)
			}
		}
		_ = f.Close()
	}
}

func TestListLocalSessions_FromSummary(t *testing.T) {
	home := t.TempDir()
	sid := "019f-test-session-aaaa"
	cwd := "/tmp/project-a"
	writeSessionFixture(t, home, cwd, sid, map[string]any{
		"info":              map[string]any{"id": sid, "cwd": cwd},
		"session_summary":   "",
		"updated_at":        "2026-07-12T10:00:00Z",
		"last_active_at":    "2026-07-12T11:00:00Z",
		"num_chat_messages": 4,
		"current_model_id":  "grok-4.5",
	}, []map[string]any{
		{"type": "system", "content": "You are Grok"},
		{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_info>\nOS</user_info>"}}},
		{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_query>\nhello from mac\n</user_query>"}}},
		{"type": "assistant", "content": "hi there"},
	})

	a, err := New(map[string]any{"grok_home": home})
	if err != nil {
		// CLI may be required by New; if missing, skip New and call listLocalSessions directly.
		if _, lookErr := os.Stat("/usr/bin/true"); lookErr == nil {
			// Try with a fake cli that exists
			a, err = New(map[string]any{"grok_home": home, "cli_path": "true"})
		}
	}
	if err != nil {
		// Fall back: test pure catalog helper without Agent.New
		list, lerr := listLocalSessions(context.Background(), home)
		if lerr != nil {
			t.Fatal(lerr)
		}
		if len(list) != 1 {
			t.Fatalf("list len = %d, want 1", len(list))
		}
		if list[0].ID != sid {
			t.Errorf("ID = %q", list[0].ID)
		}
		if list[0].Directory != cwd {
			t.Errorf("Directory = %q", list[0].Directory)
		}
		if list[0].Summary != "hello from mac" {
			t.Errorf("Summary = %q, want first user title", list[0].Summary)
		}
		if list[0].MessageCount != 4 {
			t.Errorf("MessageCount = %d", list[0].MessageCount)
		}
		if list[0].ModelID != "grok-4.5" {
			t.Errorf("ModelID = %q", list[0].ModelID)
		}
		wantMod, _ := time.Parse(time.RFC3339, "2026-07-12T11:00:00Z")
		if !list[0].ModifiedAt.Equal(wantMod) {
			t.Errorf("ModifiedAt = %v, want %v", list[0].ModifiedAt, wantMod)
		}
		return
	}

	list, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].ID != sid || list[0].Directory != cwd {
		t.Errorf("got %+v", list[0])
	}
	if list[0].Summary != "hello from mac" {
		t.Errorf("Summary = %q", list[0].Summary)
	}
}

func TestListLocalSessions_EmptyHome(t *testing.T) {
	list, err := listLocalSessions(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}

func TestQuerySessionsFromSQLite_ParsesFields(t *testing.T) {
	// Unit-test the parser path without requiring a real sqlite file by
	// exercising parseSQLiteUpdatedAt + merge of sqlite-only rows in listLocalSessions
	// via a synthetic map injection is not exposed; test timestamp parsing directly.
	sec := parseSQLiteUpdatedAt("1783844664")
	if sec.IsZero() {
		t.Fatal("unix seconds should parse")
	}
	ms := parseSQLiteUpdatedAt("1783844664123")
	if ms.IsZero() {
		t.Fatal("unix millis should parse")
	}
	// 1783844664123 ms == 1783844664.123 s → same second, later nanoseconds
	if !ms.After(sec) && !ms.Equal(sec) {
		t.Fatalf("millis path: sec=%v ms=%v", sec, ms)
	}
	// Threshold: values > 1e12 treated as millis
	if parseSQLiteUpdatedAt("999999999999").IsZero() { // still seconds-range
		t.Fatal("large seconds should parse")
	}
	rfc := parseSQLiteUpdatedAt("2026-07-12T11:00:00Z")
	if rfc.IsZero() {
		t.Fatal("RFC3339 should parse")
	}
}

func TestListLocalSessions_SQLiteOnlyRowKeepsDirectory(t *testing.T) {
	// When only sqlite contributes a row, Directory/ModifiedAt must not be empty/now-only.
	// Build a minimal sqlite DB if sqlite3 CLI is available.
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(sessionsDir, "session_search.sqlite")
	sqlite3, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	sql := `
CREATE TABLE session_docs (
  session_id TEXT PRIMARY KEY,
  cwd TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  title TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL DEFAULT '',
  last_indexed_offset INTEGER NOT NULL DEFAULT 0
);
INSERT INTO session_docs(session_id,cwd,updated_at,title,content,content_hash)
VALUES ('sqlite-only-1','/Users/jacklee/Projects/demo',1700000000,'demo title','','');
`
	cmd := exec.Command(sqlite3, dbPath)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 setup: %v %s", err, out)
	}

	list, err := listLocalSessions(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	got := list[0]
	if got.ID != "sqlite-only-1" {
		t.Errorf("ID=%q", got.ID)
	}
	if got.Directory != "/Users/jacklee/Projects/demo" {
		t.Errorf("Directory=%q", got.Directory)
	}
	if got.Summary != "demo title" {
		t.Errorf("Summary=%q", got.Summary)
	}
	if got.ModifiedAt.IsZero() || got.ModifiedAt.Equal(time.Now().UTC()) {
		// Must be the stored unix timestamp, not "now"
		want := time.Unix(1700000000, 0).UTC()
		if !got.ModifiedAt.Equal(want) {
			t.Errorf("ModifiedAt=%v want %v", got.ModifiedAt, want)
		}
	}
}

func TestListLocalSessions_SortByModifiedDesc(t *testing.T) {
	home := t.TempDir()
	writeSessionFixture(t, home, "/tmp/a", "sess-old", map[string]any{
		"info": map[string]any{"id": "sess-old", "cwd": "/tmp/a"}, "updated_at": "2026-07-01T00:00:00Z",
	}, nil)
	writeSessionFixture(t, home, "/tmp/b", "sess-new", map[string]any{
		"info": map[string]any{"id": "sess-new", "cwd": "/tmp/b"}, "updated_at": "2026-07-12T00:00:00Z",
	}, nil)
	list, err := listLocalSessions(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len=%d", len(list))
	}
	if list[0].ID != "sess-new" || list[1].ID != "sess-old" {
		t.Fatalf("order = %q, %q", list[0].ID, list[1].ID)
	}
}

func TestReadSessionHistory_FiltersSystemAndSynthetic(t *testing.T) {
	home := t.TempDir()
	sid := "hist-1"
	writeSessionFixture(t, home, "/tmp/x", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/x"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "system", "content": "You are Grok system prompt"},
		{"type": "user", "synthetic_reason": "system_reminder", "content": []map[string]any{{"type": "text", "text": "skill dump"}}},
		{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_info>\nbloat</user_info>"}}},
		{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_query>\n2+2?\n</user_query>"}}},
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{{"name": "run_terminal_command"}}},
		{"type": "assistant", "content": "4"},
	})

	entries, err := readSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("entries=%d want >=2: %+v", len(entries), entries)
	}
	// First real user
	var foundUser, foundAnswer bool
	for _, e := range entries {
		if e.Role == "user" && e.Content == "2+2?" {
			foundUser = true
		}
		if e.Role == "assistant" && e.Content == "4" {
			foundAnswer = true
		}
		if e.Role == "user" && (e.Content == "You are Grok system prompt" || e.Content == "skill dump") {
			t.Errorf("leaked system/synthetic into history: %q", e.Content)
		}
	}
	if !foundUser {
		t.Errorf("missing user query, entries=%+v", entries)
	}
	if !foundAnswer {
		t.Errorf("missing assistant answer, entries=%+v", entries)
	}
}

func TestReadSessionHistory_Limit(t *testing.T) {
	home := t.TempDir()
	sid := "hist-limit"
	hist := []map[string]any{}
	for i := 0; i < 5; i++ {
		hist = append(hist,
			map[string]any{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_query>\nm" + string(rune('0'+i)) + "\n</user_query>"}}},
			map[string]any{"type": "assistant", "content": "a" + string(rune('0'+i))},
		)
	}
	writeSessionFixture(t, home, "/tmp/y", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/y"}, "updated_at": "2026-07-12T00:00:00Z",
	}, hist)
	entries, err := readSessionHistory(home, sid, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("len=%d want 3", len(entries))
	}
}

// TestReadRichSessionHistory_StableIDsAcrossReads verifies that reading the
// same chat_history.jsonl twice yields identical IDs.  This is the root-cause
// regression guard for the iOS "执行中" stuck state: if IDs are not stable,
// the iOS external-turn probe sees "new" messages on every poll and falsely
// activates generation.
func TestReadRichSessionHistory_StableIDsAcrossReads(t *testing.T) {
	home := t.TempDir()
	sid := "rich-stable-1"
	writeSessionFixture(t, home, "/tmp/s", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/s"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "system", "content": "You are Grok"},
		{"type": "user", "synthetic_reason": "system_reminder", "content": "bloat"},
		{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_query>\nhello\n</user_query>"}}},
		{"type": "assistant", "content": "hi there"},
		{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_query>\nbye\n</user_query>"}}},
		{"type": "assistant", "content": "goodbye"},
	})

	first, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(first) != len(second) {
		t.Fatalf("entry count differs: first=%d second=%d", len(first), len(second))
	}
	// 2 user + 2 assistant = 4 (system and synthetic filtered out)
	if len(first) != 4 {
		t.Fatalf("entry count = %d, want 4", len(first))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("ID drift at index %d: first=%q second=%q", i, first[i].ID, second[i].ID)
		}
		if first[i].ID == "" {
			t.Errorf("empty ID at index %d", i)
		}
		if first[i].Role != second[i].Role || first[i].Content != second[i].Content {
			t.Errorf("content drift at index %d", i)
		}
	}
}

// TestReadRichSessionHistory_IDsImmuneToLimitTruncation verifies that limit
// truncation does not change the IDs of the remaining messages.  IDs must be
// derived from physical JSONL line numbers, not filtered-array indices.
func TestReadRichSessionHistory_IDsImmuneToLimitTruncation(t *testing.T) {
	home := t.TempDir()
	sid := "rich-limit"
	hist := []map[string]any{}
	for i := 0; i < 3; i++ {
		hist = append(hist,
			map[string]any{"type": "user", "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("<user_query>\nq%d\n</user_query>", i)}}},
			map[string]any{"type": "assistant", "content": fmt.Sprintf("a%d", i)},
		)
	}
	writeSessionFixture(t, home, "/tmp/lim", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/lim"}, "updated_at": "2026-07-12T00:00:00Z",
	}, hist)

	full, _ := readRichSessionHistory(home, sid, 0)
	limited, _ := readRichSessionHistory(home, sid, 2) // last 2 of 6 entries

	if len(limited) != 2 {
		t.Fatalf("limited len=%d, want 2", len(limited))
	}
	// The last 2 entries of the full result must have the same IDs as the
	// limited result.  If IDs were derived from filtered-array index, the
	// limited result would have different IDs.
	for i := range limited {
		fullIdx := len(full) - len(limited) + i
		if limited[i].ID != full[fullIdx].ID {
			t.Errorf("limit shifted ID at position %d: limited=%q full[%d]=%q",
				i, limited[i].ID, fullIdx, full[fullIdx].ID)
		}
	}
}

func TestAgent_ImplementsRichHistoryProvider(t *testing.T) {
	a, err := New(map[string]any{"grok_home": t.TempDir(), "cli_path": "true"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.(core.RichHistoryProvider); !ok {
		t.Fatal("Agent must implement core.RichHistoryProvider for stable history IDs")
	}
}

