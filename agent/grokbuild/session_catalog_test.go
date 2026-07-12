package grokbuild

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
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

