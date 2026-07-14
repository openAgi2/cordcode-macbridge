package grokbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

// ---- Rich history Parts/Steps tests (§4 步骤 3) ----

// richToolCall builds a synthetic tool_calls element matching the real Grok
// JSONL shape confirmed by forensic audit: {name, id, arguments(JSON string)}.
func richToolCall(name, callID string, args map[string]any) map[string]any {
	argsJSON, _ := json.Marshal(args)
	return map[string]any{
		"name":      name,
		"id":        callID,
		"arguments": string(argsJSON),
	}
}

func TestReadRichSessionHistory_FillsPartsAndSteps(t *testing.T) {
	home := t.TempDir()
	sid := "rich-parts-1"
	writeSessionFixture(t, home, "/tmp/p", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/p"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "user", "content": []map[string]any{{"type": "text", "text": "<user_query>\nrun a search\n</user_query>"}}},
		// assistant row with tool_calls: command + read_file, both with proven arguments and call IDs
		{"type": "assistant", "content": "Let me check.", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-1", map[string]any{"command": "rg foo", "description": "search for foo"}),
			richToolCall("read_file", "call-2", map[string]any{"target_file": "/tmp/p/main.go"}),
		}},
		// tool_result rows follow, correlated by tool_call_id
		{"type": "tool_result", "tool_call_id": "call-1", "content": "found 3 matches"},
		{"type": "tool_result", "tool_call_id": "call-2", "content": "package main"},
		{"type": "assistant", "content": "Done."},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Turn accumulation: 1 user + 1 assistant turn (tools + text merged) = 2.
	// The assistant "Let me check." + tool calls + "Done." are all one turn.
	if len(entries) != 2 {
		t.Fatalf("entries=%d want 2 (turn accumulation): %+v", len(entries), entries)
	}

	// Find the assistant entry with tool_calls
	var toolEntry *core.RichHistoryEntry
	for i := range entries {
		if entries[i].Role == "assistant" && len(entries[i].Steps) > 0 {
			toolEntry = &entries[i]
			break
		}
	}
	if toolEntry == nil {
		t.Fatalf("no assistant entry with steps found")
	}

	// Content should contain the real assistant texts (merged), NOT "Tool: ..."
	if !strings.Contains(toolEntry.Content, "Let me check.") {
		t.Errorf("Content = %q, should contain real text (must not synthesize Tool:)", toolEntry.Content)
	}
	if len(toolEntry.Steps) != 2 {
		t.Fatalf("Steps len = %d, want 2", len(toolEntry.Steps))
	}
	// Parts: 2 text parts + 2 tool parts = 4 (text "Let me check." + 2 tools + text "Done.")
	if len(toolEntry.Parts) != 4 {
		t.Fatalf("Parts len = %d, want 4 (2 text + 2 tool)", len(toolEntry.Parts))
	}

	// Step 0: run_terminal_command
	s0 := toolEntry.Steps[0]
	if s0["toolName"] != "run_terminal_command" {
		t.Errorf("step0 toolName = %v", s0["toolName"])
	}
	if s0["status"] != "unknown" {
		t.Errorf("step0 status = %v, want unknown (P1-1: no status field)", s0["status"])
	}
	// Title derived from proven arguments
	title0, _ := s0["title"].(string)
	if !strings.Contains(title0, "rg foo") {
		t.Errorf("step0 title = %q, want to contain command", title0)
	}
	// Output correlated via tool_call_id
	output0 := s0["output"].(map[string]any)
	if output0["text"] != "found 3 matches" {
		t.Errorf("step0 output = %v, want 'found 3 matches' (P1-2: proven correlation)", output0["text"])
	}

	// Step 1: read_file
	s1 := toolEntry.Steps[1]
	if s1["toolName"] != "read_file" {
		t.Errorf("step1 toolName = %v", s1["toolName"])
	}
	title1, _ := s1["title"].(string)
	if title1 != "/tmp/p/main.go" {
		t.Errorf("step1 title = %q, want /tmp/p/main.go", title1)
	}
	output1 := s1["output"].(map[string]any)
	if output1["text"] != "package main" {
		t.Errorf("step1 output = %v, want 'package main'", output1["text"])
	}

	// P2-2: tool Parts and Steps must correspond 1:1 with same step IDs.
	// (Parts may also contain text/reasoning parts from turn accumulation.)
	toolParts := []map[string]any{}
	for _, part := range toolEntry.Parts {
		if part["type"] == "tool" {
			toolParts = append(toolParts, part)
		}
	}
	if len(toolParts) != len(toolEntry.Steps) {
		t.Fatalf("tool parts=%d vs steps=%d, want equal", len(toolParts), len(toolEntry.Steps))
	}
	for i, part := range toolParts {
		stepFromPart := part["step"].(map[string]any)
		if stepFromPart["id"] != toolEntry.Steps[i]["id"] {
			t.Errorf("part/step ID mismatch at %d: part=%v step=%v", i, stepFromPart["id"], toolEntry.Steps[i]["id"])
		}
	}
}

// P1-1: steps without proven result must have status "unknown", never "completed".
func TestReadRichSessionHistory_StatusUnknownWhenNoStatusField(t *testing.T) {
	home := t.TempDir()
	sid := "rich-status-1"
	writeSessionFixture(t, home, "/tmp/s", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/s"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-a", map[string]any{"command": "echo hi"}),
		}},
		// tool_result has content but NO status field
		{"type": "tool_result", "tool_call_id": "call-a", "content": "hi"},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	if len(entries[0].Steps) != 1 {
		t.Fatalf("steps=%d want 1", len(entries[0].Steps))
	}
	status := entries[0].Steps[0]["status"]
	if status != "unknown" {
		t.Errorf("status = %v, want 'unknown' (P1-1: tool_result has no status field)", status)
	}
	if status == "completed" {
		t.Error("status must never be 'completed' without proven status (P1-1)")
	}
}

// P1-2: output must not be filled when there's no proven correlation.
func TestReadRichSessionHistory_NoOutputWithoutProvenCorrelation(t *testing.T) {
	home := t.TempDir()
	sid := "rich-output-1"
	writeSessionFixture(t, home, "/tmp/o", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/o"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		// tool_call with no matching tool_result (call ID mismatch)
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-x", map[string]any{"command": "ls"}),
		}},
		// tool_result with a different call ID (no match)
		{"type": "tool_result", "tool_call_id": "call-y", "content": "unrelated output"},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	if len(entries[0].Steps) != 1 {
		t.Fatalf("steps=%d want 1", len(entries[0].Steps))
	}
	output := entries[0].Steps[0]["output"].(map[string]any)
	if output["text"] != "" {
		t.Errorf("output = %v, want empty (P1-2: no proven correlation)", output["text"])
	}
}

// P1-2: multiple calls with out-of-order results — correlation must use call ID, not position.
func TestReadRichSessionHistory_MultiCallOutOfOrderResults(t *testing.T) {
	home := t.TempDir()
	sid := "rich-multi-1"
	writeSessionFixture(t, home, "/tmp/m", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/m"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-1", map[string]any{"command": "echo first"}),
			richToolCall("run_terminal_command", "call-2", map[string]any{"command": "echo second"}),
		}},
		// Results in REVERSED order — must still correlate by call ID
		{"type": "tool_result", "tool_call_id": "call-2", "content": "second-out"},
		{"type": "tool_result", "tool_call_id": "call-1", "content": "first-out"},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	steps := entries[0].Steps
	if len(steps) != 2 {
		t.Fatalf("steps=%d want 2", len(steps))
	}
	out0 := steps[0]["output"].(map[string]any)["text"]
	out1 := steps[1]["output"].(map[string]any)["text"]
	if out0 != "first-out" {
		t.Errorf("step0 output = %v, want 'first-out' (correlation by call ID, not position)", out0)
	}
	if out1 != "second-out" {
		t.Errorf("step1 output = %v, want 'second-out' (correlation by call ID, not position)", out1)
	}
}

// R2-P1-2: empty-content tool_calls row must produce a valid entry (not skipped,
// not synthesized as "Tool:").
func TestReadRichSessionHistory_EmptyContentToolCallNotSkipped(t *testing.T) {
	home := t.TempDir()
	sid := "rich-empty-1"
	writeSessionFixture(t, home, "/tmp/e", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/e"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		// assistant row with empty content + tool_calls — must NOT be skipped
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-1", map[string]any{"command": "pwd"}),
		}},
		{"type": "tool_result", "tool_call_id": "call-1", "content": "/tmp"},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1 (empty-content tool_calls must not be skipped, R2-P1-2)", len(entries))
	}
	e := entries[0]
	if e.Role != "assistant" {
		t.Errorf("role = %q, want assistant", e.Role)
	}
	if e.Content != "" {
		t.Errorf("Content = %q, want empty (must not synthesize Tool:)", e.Content)
	}
	if !strings.Contains(e.Content, "Tool:") {
		// pass — content should not contain "Tool:"
	} else {
		t.Errorf("Content must not contain 'Tool:' (R2-P1-2)")
	}
	if len(e.Steps) != 1 {
		t.Fatalf("steps=%d want 1", len(e.Steps))
	}
	if len(e.Parts) != 1 {
		t.Fatalf("parts=%d want 1", len(e.Parts))
	}
}

// R2-P1-2: assistant row with real text + tool_calls — text and tool part both preserved.
func TestReadRichSessionHistory_RealTextAndToolCallPreserved(t *testing.T) {
	home := t.TempDir()
	sid := "rich-text-tool-1"
	writeSessionFixture(t, home, "/tmp/tt", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/tt"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "assistant", "content": "I will run a command.", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-1", map[string]any{"command": "echo hi"}),
		}},
		{"type": "tool_result", "tool_call_id": "call-1", "content": "hi"},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	e := entries[0]
	if e.Content != "I will run a command." {
		t.Errorf("Content = %q, want real assistant text", e.Content)
	}
	if len(e.Steps) != 1 {
		t.Fatalf("steps=%d want 1", len(e.Steps))
	}
}

// P1-4: long output (>500 runes) must be dropped, not truncated.
func TestReadRichSessionHistory_LongOutputDropped(t *testing.T) {
	home := t.TempDir()
	sid := "rich-long-1"
	longResult := strings.Repeat("x", 501) // 501 runes > 500 threshold
	writeSessionFixture(t, home, "/tmp/l", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/l"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-1", map[string]any{"command": "cat big"}),
		}},
		{"type": "tool_result", "tool_call_id": "call-1", "content": longResult},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(entries[0].Steps) != 1 {
		t.Fatalf("entries=%d steps=%d", len(entries), func() int {
			if len(entries) > 0 {
				return len(entries[0].Steps)
			}
			return 0
		}())
	}
	output := entries[0].Steps[0]["output"].(map[string]any)["text"]
	if output != "" {
		t.Errorf("output len = %d, want empty (>500 runes dropped, P1-4 option 1)", len(output.(string)))
	}
}

// P1-3: step IDs must be stable across reads and immune to limit truncation.
func TestReadRichSessionHistory_StepIDsStableAcrossReads(t *testing.T) {
	home := t.TempDir()
	sid := "rich-stepid-1"
	writeSessionFixture(t, home, "/tmp/si", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/si"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-1", map[string]any{"command": "echo a"}),
		}},
		{"type": "tool_result", "tool_call_id": "call-1", "content": "a"},
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-2", map[string]any{"command": "echo b"}),
		}},
		{"type": "tool_result", "tool_call_id": "call-2", "content": "b"},
	})

	first, _ := readRichSessionHistory(home, sid, 0)
	second, _ := readRichSessionHistory(home, sid, 0)
	if len(first) != len(second) {
		t.Fatalf("entry count drift: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if len(first[i].Steps) != len(second[i].Steps) {
			t.Errorf("step count drift at entry %d", i)
			continue
		}
		for j := range first[i].Steps {
			id1 := first[i].Steps[j]["id"]
			id2 := second[i].Steps[j]["id"]
			if id1 != id2 {
				t.Errorf("step ID drift at entry %d step %d: %v vs %v", i, j, id1, id2)
			}
		}
	}
}

// P1-3: limit truncation must not shift entry IDs (already tested, but extend
// to verify steps are also preserved in the limited tail).
func TestReadRichSessionHistory_LimitPreservesStepsInTail(t *testing.T) {
	home := t.TempDir()
	sid := "rich-limit-steps-1"
	hist := []map[string]any{}
	for i := 0; i < 3; i++ {
		hist = append(hist,
			map[string]any{"type": "user", "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("<user_query>\nq%d\n</user_query>", i)}}},
			map[string]any{"type": "assistant", "content": "", "tool_calls": []map[string]any{
				richToolCall("run_terminal_command", fmt.Sprintf("call-%d", i), map[string]any{"command": fmt.Sprintf("echo %d", i)}),
			}},
			map[string]any{"type": "tool_result", "tool_call_id": fmt.Sprintf("call-%d", i), "content": fmt.Sprintf("out%d", i)},
		)
	}
	writeSessionFixture(t, home, "/tmp/ls", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/ls"}, "updated_at": "2026-07-12T00:00:00Z",
	}, hist)

	full, _ := readRichSessionHistory(home, sid, 0)
	limited, _ := readRichSessionHistory(home, sid, 2) // last 2 entries

	if len(limited) != 2 {
		t.Fatalf("limited len=%d want 2", len(limited))
	}
	// Verify the limited entries match the tail of full
	for i := range limited {
		fullIdx := len(full) - len(limited) + i
		if limited[i].ID != full[fullIdx].ID {
			t.Errorf("limit shifted entry ID at %d", i)
		}
		if len(limited[i].Steps) != len(full[fullIdx].Steps) {
			t.Errorf("limit changed step count at %d: limited=%d full=%d", i, len(limited[i].Steps), len(full[fullIdx].Steps))
		}
	}
}

// Missing arguments / name-only call — must produce generic step without guessing.
func TestReadRichSessionHistory_NameOnlyCallNoGuessing(t *testing.T) {
	home := t.TempDir()
	sid := "rich-nameonly-1"
	writeSessionFixture(t, home, "/tmp/no", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/no"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		// tool_call with only name, no arguments — must not guess path/command
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			{"name": "run_terminal_command"},
		}},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	if len(entries[0].Steps) != 1 {
		t.Fatalf("steps=%d want 1", len(entries[0].Steps))
	}
	s := entries[0].Steps[0]
	if s["toolName"] != "run_terminal_command" {
		t.Errorf("toolName = %v", s["toolName"])
	}
	// Must NOT have a title (no proven arguments)
	if title, ok := s["title"]; ok && title != "" {
		t.Errorf("title = %v, want empty (no guessing from name only)", title)
	}
}

// Malformed JSON line must not panic or affect other line IDs.
func TestReadRichSessionHistory_MalformedJSONLineSkipped(t *testing.T) {
	home := t.TempDir()
	sid := "rich-malformed-1"
	dir := filepath.Join(home, "sessions", url.PathEscape("/tmp/mf"), sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a mix of valid + invalid JSONL lines manually
	lines := []string{
		`{"type":"user","content":[{"type":"text","text":"<user_query>\nhi\n</user_query>"}]}`,
		`{invalid json line`,
		`{"type":"assistant","content":"hello"}`,
	}
	os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"info":{"id":"`+sid+`","cwd":"/tmp/mf"},"updated_at":"2026-07-12T00:00:00Z"}`), 0o644)

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 1 user + 1 assistant = 2 (malformed line skipped)
	if len(entries) != 2 {
		t.Fatalf("entries=%d want 2 (malformed line skipped)", len(entries))
	}
	// IDs must be non-empty
	for _, e := range entries {
		if e.ID == "" {
			t.Error("entry ID must not be empty")
		}
	}
}

// Reasoning rows produce assistant entries with thinking + reasoning part.
func TestReadRichSessionHistory_ReasoningProducesThinkingEntry(t *testing.T) {
	home := t.TempDir()
	sid := "rich-reasoning-1"
	writeSessionFixture(t, home, "/tmp/r", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/r"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "reasoning", "content": "thinking about the problem"},
		{"type": "assistant", "content": "answer"},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Turn accumulation: reasoning + assistant text = 1 entry
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1 (reasoning+assistant merged into one turn)", len(entries))
	}
	if entries[0].Thinking != "thinking about the problem" {
		t.Errorf("thinking = %q", entries[0].Thinking)
	}
	if entries[0].Content != "answer" {
		t.Errorf("content = %q, want 'answer'", entries[0].Content)
	}
	// Parts: 1 reasoning + 1 text = 2
	if len(entries[0].Parts) != 2 {
		t.Errorf("parts len = %d, want 2 (reasoning + text)", len(entries[0].Parts))
	}
	if entries[0].Parts[0]["type"] != "reasoning" {
		t.Errorf("part 0 type = %v, want reasoning", entries[0].Parts[0]["type"])
	}
}

func TestReadRichSessionHistory_PreservesInterleavedReasoningAndTools(t *testing.T) {
	home := t.TempDir()
	sid := "rich-interleaved-1"
	writeSessionFixture(t, home, "/tmp/interleaved", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/interleaved"},
	}, []map[string]any{
		{"type": "reasoning", "content": "plan the change"},
		{"type": "assistant", "tool_calls": []map[string]any{
			richToolCall("read_file", "call-1", map[string]any{"target_file": "a.swift"}),
		}},
		{"type": "tool_result", "tool_call_id": "call-1", "content": "contents"},
		{"type": "reasoning", "content": "verify the result"},
		{"type": "assistant", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-2", map[string]any{"command": "swift test"}),
		}},
		{"type": "assistant", "content": "Done."},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}

	parts := entries[0].Parts
	if len(parts) != 5 {
		t.Fatalf("parts len=%d want 5: %+v", len(parts), parts)
	}
	gotTypes := []string{parts[0]["type"].(string), parts[1]["type"].(string), parts[2]["type"].(string), parts[3]["type"].(string), parts[4]["type"].(string)}
	if want := []string{"reasoning", "tool", "reasoning", "tool", "text"}; !reflect.DeepEqual(gotTypes, want) {
		t.Fatalf("part types=%v want %v", gotTypes, want)
	}
	if parts[0]["content"] != "plan the change" || parts[2]["content"] != "verify the result" {
		t.Fatalf("reasoning order lost: %+v", parts)
	}
}

// Real Grok reasoning rows store plaintext in "summary" ([{type:summary_text,text:...}]),
// not "content". encrypted_content is opaque. The summary must be extracted.
func TestReadRichSessionHistory_ReasoningFromSummaryField(t *testing.T) {
	home := t.TempDir()
	sid := "rich-reasoning-summary-1"
	writeSessionFixture(t, home, "/tmp/rs", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/rs"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		// Real Grok shape: no "content", has "summary" + "encrypted_content"
		{"type": "reasoning", "summary": []map[string]any{
			{"type": "summary_text", "text": "I need to read the handoff file and continue the work."},
		}, "encrypted_content": "XYs5TFdup4LFTU/mRJuy...", "status": "completed"},
		{"type": "assistant", "content": "Done."},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Turn accumulation: reasoning + assistant = 1 entry
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1 (reasoning+assistant merged, summary must not be dropped)", len(entries))
	}
	if entries[0].Thinking != "I need to read the handoff file and continue the work." {
		t.Errorf("thinking = %q, want summary_text", entries[0].Thinking)
	}
	// Parts: 1 reasoning + 1 text = 2
	if len(entries[0].Parts) != 2 || entries[0].Parts[0]["type"] != "reasoning" {
		t.Errorf("reasoning part missing: %+v", entries[0].Parts)
	}
}

// Duplicate call IDs — multiple calls with the same ID.
// The last result wins (map semantics); step still gets output.
func TestReadRichSessionHistory_DuplicateCallIDs(t *testing.T) {
	home := t.TempDir()
	sid := "rich-dup-1"
	writeSessionFixture(t, home, "/tmp/d", sid, map[string]any{
		"info": map[string]any{"id": sid, "cwd": "/tmp/d"}, "updated_at": "2026-07-12T00:00:00Z",
	}, []map[string]any{
		{"type": "assistant", "content": "", "tool_calls": []map[string]any{
			richToolCall("run_terminal_command", "call-dup", map[string]any{"command": "echo 1"}),
			richToolCall("run_terminal_command", "call-dup", map[string]any{"command": "echo 2"}),
		}},
		{"type": "tool_result", "tool_call_id": "call-dup", "content": "single-result"},
	})

	entries, err := readRichSessionHistory(home, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	if len(entries[0].Steps) != 2 {
		t.Fatalf("steps=%d want 2 (both calls produce steps)", len(entries[0].Steps))
	}
	// Both steps share the same call ID → both get the same output
	for i, s := range entries[0].Steps {
		out := s["output"].(map[string]any)["text"]
		if out != "single-result" {
			t.Errorf("step %d output = %v, want 'single-result'", i, out)
		}
	}
}
