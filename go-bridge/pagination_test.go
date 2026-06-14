package gobridge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cccode-macbridge/agent/codex"
	"github.com/openAgi2/cccode-macbridge/transcriptindex"
)

// writeCodexFixture writes an alternating user/assistant Codex transcript under a
// temp codex home so the agent's TranscriptPath resolves it. Returns (2*turns)
// logical messages.
func writeCodexFixture(t *testing.T, codexHome, sessionID string, turns int) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "01", "01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-01-01T00-00-00-"+sessionID+".jsonl")
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "{\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":\"/tmp\"}}\n", sessionID)
	for i := 0; i < turns; i++ {
		fmt.Fprintf(&buf, "{\"timestamp\":\"2026-01-01T00:0%d:00Z\",\"type\":\"response_item\",\"payload\":{\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"question %d\"}]}}\n", i%6, i)
		fmt.Fprintf(&buf, "{\"timestamp\":\"2026-01-01T00:0%d:01Z\",\"type\":\"response_item\",\"payload\":{\"role\":\"assistant\",\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer %d\"}]}}\n", i%6, i)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sendMessagesRPC(t *testing.T, h *Handlers, backendID, sessionID string, limit int, beforeCursor string, paginate bool) *sessionLoadCaptureConn {
	t.Helper()
	params, _ := json.Marshal(GetSessionMessagesParams{
		SessionID: sessionID, Limit: limit, BeforeCursor: beforeCursor, Paginate: paginate,
	})
	conn := &sessionLoadCaptureConn{}
	h.HandleRPC(conn, WireMessage{
		BackendID: backendID,
		Method:    "get_session_messages",
		RequestID: "test-" + sessionID,
		Params:    params,
	})
	return conn
}

func rpcData(t *testing.T, conn *sessionLoadCaptureConn) map[string]interface{} {
	t.Helper()
	resp, ok := conn.sent.(map[string]interface{})
	if !ok {
		t.Fatalf("response type %T", conn.sent)
	}
	if okb, _ := resp["ok"].(bool); !okb {
		t.Fatalf("rpc error: %+v", resp["error"])
	}
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("response missing data: %+v", resp)
	}
	return data
}

func rpcError(t *testing.T, conn *sessionLoadCaptureConn) *WireError {
	t.Helper()
	resp, ok := conn.sent.(map[string]interface{})
	if !ok {
		t.Fatalf("response type %T", conn.sent)
	}
	if err, ok := resp["error"].(*WireError); ok {
		return err
	}
	t.Fatalf("expected error response, got: %+v", resp)
	return nil
}

func messageTexts(msgs []map[string]interface{}) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprintf("%v", m["content"]))
	}
	return out
}

// First page returns the newest slice with cursors; a backward page using the
// returned oldestCursor returns the next-older slice with no overlap.
func TestPaginatedMessages_FirstAndBackwardPage(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "pagtestabc"
	writeCodexFixture(t, codexHome, sessionID, 8) // 16 logical messages

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatalf("codex.New: %v", err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	// First page: newest 5 of 16.
	page1 := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 5, "", true))
	msgs1 := page1["messages"].([]map[string]interface{})
	if len(msgs1) != 5 {
		t.Fatalf("page1 returned %d messages, want 5", len(msgs1))
	}
	oldest1, _ := page1["oldestCursor"].(string)
	newest1, _ := page1["newestCursor"].(string)
	if oldest1 == "" || newest1 == "" {
		t.Fatalf("page1 missing cursors: oldest=%q newest=%q", oldest1, newest1)
	}
	if hasMore, _ := page1["hasMore"].(bool); !hasMore {
		t.Errorf("page1 hasMore=false, want true (16 messages, page of 5)")
	}
	page1IDs := make(map[string]bool, len(msgs1))
	for _, message := range msgs1 {
		id, _ := message["id"].(string)
		if strings.TrimSpace(id) == "" {
			t.Fatal("page1 contains a message with an empty id")
		}
		if page1IDs[id] {
			t.Fatalf("page1 contains duplicate message id %q", id)
		}
		page1IDs[id] = true
	}
	texts1 := messageTexts(msgs1)

	// Backward page using oldest1 as beforeCursor.
	page2 := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 5, oldest1, true))
	msgs2 := page2["messages"].([]map[string]interface{})
	if len(msgs2) != 5 {
		t.Fatalf("page2 returned %d messages, want 5", len(msgs2))
	}
	texts2 := messageTexts(msgs2)
	// No overlap between pages.
	seen := map[string]bool{}
	for _, tx := range texts1 {
		seen[tx] = true
	}
	for _, tx := range texts2 {
		if seen[tx] {
			t.Errorf("message %q appears in both pages (overlap)", tx)
		}
	}
	for _, message := range msgs2 {
		id, _ := message["id"].(string)
		if strings.TrimSpace(id) == "" {
			t.Fatal("page2 contains a message with an empty id")
		}
		if page1IDs[id] {
			t.Fatalf("message id %q appears in both pages", id)
		}
	}
	// page2 oldestCursor must be older than page1's oldest.
	oldest2, _ := page2["oldestCursor"].(string)
	if oldest2 == "" {
		t.Fatalf("page2 missing oldestCursor")
	}
	if hasMore, _ := page2["hasMore"].(bool); !hasMore {
		t.Errorf("page2 hasMore=false, want true (16 messages, fetched 10)")
	}
}

func TestPaginatedMessages_CompactsDuplicateLargeMessageFields(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "large-duplicate"
	sessionDir := filepath.Join(codexHome, "sessions", "2026", "01", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "rollout-2026-01-01T00-00-00-"+sessionID+".jsonl")
	largeText := strings.Repeat("x", 900000)
	content := fmt.Sprintf(
		"{\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":\"/tmp\"}}\n"+
			"{\"timestamp\":\"2026-01-01T00:00:01Z\",\"type\":\"response_item\",\"payload\":{\"role\":\"assistant\",\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n",
		sessionID,
		largeText,
	)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	page := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 50, "", true))
	msgs := page["messages"].([]map[string]interface{})
	if len(msgs) != 1 {
		t.Fatalf("returned %d messages, want 1", len(msgs))
	}
	if _, duplicated := msgs[0]["content"]; duplicated {
		t.Fatal("top-level content was not compacted despite exact parts duplicate")
	}
	enc, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(enc) > maxPageResponseBytes {
		t.Fatalf("compacted page encoded %d bytes > budget %d", len(enc), maxPageResponseBytes)
	}
	parts := msgs[0]["parts"].([]interface{})
	part := parts[0].(map[string]any)
	if got, _ := part["content"].(string); got != largeText {
		t.Fatalf("parts content length = %d, want %d", len(got), len(largeText))
	}
}

func TestCompactDuplicateMessageFieldsPreservesNonEquivalentContent(t *testing.T) {
	message := map[string]interface{}{
		"content": "ab",
		"parts": []interface{}{
			map[string]any{"type": "text", "content": "a"},
			map[string]any{"type": "text", "content": "b"},
		},
	}

	compactDuplicateMessageFields(message)

	if got := message["content"]; got != "ab" {
		t.Fatalf("content = %#v, want exact original", got)
	}
}

func TestCompactDuplicateMessageFieldsRemovesExactStepCopy(t *testing.T) {
	step := map[string]any{"id": "tool-1", "toolName": "bash", "output": "done"}
	message := map[string]interface{}{
		"steps": []interface{}{step},
		"parts": []interface{}{
			map[string]any{"type": "tool", "step": cloneStringAnyMap(step)},
		},
	}

	compactDuplicateMessageFields(message)

	if _, duplicated := message["steps"]; duplicated {
		t.Fatal("top-level steps were not compacted despite exact parts duplicate")
	}
}

// A backward cursor pinned to a prefix that was rewritten must yield cursor_stale,
// not a silently-stitched page across lineages.
func TestPaginatedMessages_CursorStaleAfterRewrite(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "staletest"
	path := writeCodexFixture(t, codexHome, sessionID, 8)

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatalf("codex.New: %v", err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	page1 := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 5, "", true))
	oldest1, _ := page1["oldestCursor"].(string)
	if oldest1 == "" {
		t.Fatal("missing oldestCursor")
	}

	// Rewrite the transcript prefix in place (truncate + smaller content).
	if err := os.WriteFile(path, []byte("{\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"staletest\",\"cwd\":\"/tmp\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	errResp := rpcError(t, sendMessagesRPC(t, h, "codex", sessionID, 5, oldest1, true))
	if errResp.Code != "cursor_stale" {
		t.Fatalf("expected cursor_stale after rewrite, got code=%q msg=%q", errResp.Code, errResp.Message)
	}
}

// Clients that do not opt into pagination get the legacy full-parse path.
func TestPaginatedMessages_FallbackWhenNotOptedIn(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "fallbacktest"
	writeCodexFixture(t, codexHome, sessionID, 3) // 6 messages

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatalf("codex.New: %v", err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	// No paginate=true: legacy path, no cursor fields, all 6 messages returned.
	data := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 0, "", false))
	msgs := data["messages"].([]map[string]interface{})
	if len(msgs) != 6 {
		t.Fatalf("legacy path returned %d messages, want 6 (no pagination)", len(msgs))
	}
	if _, present := data["oldestCursor"]; present {
		t.Errorf("legacy path should not emit oldestCursor")
	}
}

func listResult(t *testing.T, h *Handlers, backendID string, limit int, cursor string) map[string]interface{} {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"limit": limit, "cursor": cursor})
	conn := &sessionLoadCaptureConn{}
	h.HandleRPC(conn, WireMessage{BackendID: backendID, Method: "list_sessions", RequestID: "test-list", Params: params})
	return rpcData(t, conn)
}

func listIDs(data map[string]interface{}) []string {
	sessions := data["sessions"].([]map[string]interface{})
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		id, _ := s["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// list_sessions pages through sessions newest-first by composite cursor with no
// duplicates or gaps across pages.
func TestListSessionsPagination(t *testing.T) {
	codexHome := t.TempDir()
	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "01", "01")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ids := []string{"s1", "s2", "s3"}
	for i, id := range ids {
		path := filepath.Join(sessionsDir, "rollout-"+id+".jsonl")
		content := fmt.Sprintf("{\"timestamp\":\"2026-01-0%dT00:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":\"/tmp\"}}\n", i+1, id)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Date(2026, 1, 1, 0, 0, i+1, 0, time.UTC) // s1 oldest, s3 newest
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatalf("codex.New: %v", err)
	}
	h := NewHandlers()
	h.RegisterAgent("codex", agent)

	page1 := listResult(t, h, "codex", 1, "")
	if ids1 := listIDs(page1); len(ids1) != 1 || ids1[0] != "s3" {
		t.Fatalf("page1 ids %v, want [s3]", ids1)
	}
	if hasMore, _ := page1["hasMore"].(bool); !hasMore {
		t.Errorf("page1 hasMore=false, want true")
	}
	next1, _ := page1["nextCursor"].(string)
	if next1 == "" {
		t.Fatal("page1 missing nextCursor")
	}

	page2 := listResult(t, h, "codex", 1, next1)
	if ids2 := listIDs(page2); len(ids2) != 1 || ids2[0] != "s2" {
		t.Fatalf("page2 ids %v, want [s2]", ids2)
	}
	next2, _ := page2["nextCursor"].(string)

	page3 := listResult(t, h, "codex", 1, next2)
	if ids3 := listIDs(page3); len(ids3) != 1 || ids3[0] != "s1" {
		t.Fatalf("page3 ids %v, want [s1]", ids3)
	}
	if hasMore, _ := page3["hasMore"].(bool); hasMore {
		t.Errorf("page3 hasMore=true, want false (last page)")
	}
	if _, present := page3["nextCursor"]; present {
		t.Errorf("page3 should not emit nextCursor on last page")
	}
}

// A backward cursor stays valid after a continuous tail append (the generation
// lineage proves ancestry); the client pages older history without a reset.
func TestPaginatedMessages_CursorSurvivesAppend(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "appendtest"
	writeCodexFixture(t, codexHome, sessionID, 8) // 16 messages

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatalf("codex.New: %v", err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	page1 := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 5, "", true))
	oldest1, _ := page1["oldestCursor"].(string)
	if oldest1 == "" {
		t.Fatal("missing oldestCursor")
	}
	page1Texts := messageTexts(page1["messages"].([]map[string]interface{}))

	// Append a brand-new turn (continuous tail, not a rewrite).
	appendTurn := func(codexHome, sessionID string, i int) {
		dir := filepath.Join(codexHome, "sessions", "2026", "01", "01")
		path := filepath.Join(dir, "rollout-2026-01-01T00-00-00-"+sessionID+".jsonl")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintf(f, "{\"timestamp\":\"2026-01-01T00:0%d:00Z\",\"type\":\"response_item\",\"payload\":{\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"new question %d\"}]}}\n", i%6, i)
		fmt.Fprintf(f, "{\"timestamp\":\"2026-01-01T00:0%d:01Z\",\"type\":\"response_item\",\"payload\":{\"role\":\"assistant\",\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"new answer %d\"}]}}\n", i%6, i)
	}
	appendTurn(codexHome, sessionID, 9)

	// Backward page using oldest1: NOT stale; returns older history, no overlap.
	page2 := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 5, oldest1, true))
	msgs2 := page2["messages"].([]map[string]interface{})
	if len(msgs2) != 5 {
		t.Fatalf("backward page after append returned %d, want 5", len(msgs2))
	}
	for _, tx := range messageTexts(msgs2) {
		for _, p1 := range page1Texts {
			if tx == p1 {
				t.Errorf("backward page overlapped page1 at %q", tx)
			}
		}
	}
	if hasMore, _ := page2["hasMore"].(bool); !hasMore {
		t.Errorf("backward page hasMore=false, want true (18 messages total)")
	}
}

// List cursor tie-breaks by sessionID ASC when two sessions share updatedAtMillis.
func TestListSessionsPagination_TieBreakByID(t *testing.T) {
	codexHome := t.TempDir()
	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "01", "01")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two sessions share the newest mtime; ids "bbb" and "aaa" -> "aaa" sorts first.
	mkSession := func(id string, mt time.Time) {
		path := filepath.Join(sessionsDir, "rollout-"+id+".jsonl")
		content := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":\"/tmp\"}}\n", id)
		os.WriteFile(path, []byte(content), 0o644)
		os.Chtimes(path, mt, mt)
	}
	newest := time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC)
	mkSession("bbb", newest)
	mkSession("aaa", newest)
	mkSession("older", time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC))

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.RegisterAgent("codex", agent)

	page1 := listResult(t, h, "codex", 1, "")
	if ids := listIDs(page1); len(ids) != 1 || ids[0] != "aaa" {
		t.Fatalf("page1 ids %v, want [aaa] (ASC tie-break among newest)", ids)
	}
	next1, _ := page1["nextCursor"].(string)

	page2 := listResult(t, h, "codex", 1, next1)
	if ids := listIDs(page2); len(ids) != 1 || ids[0] != "bbb" {
		t.Fatalf("page2 ids %v, want [bbb] (next in tie group)", ids)
	}
	next2, _ := page2["nextCursor"].(string)

	page3 := listResult(t, h, "codex", 1, next2)
	if ids := listIDs(page3); len(ids) != 1 || ids[0] != "older" {
		t.Fatalf("page3 ids %v, want [older]", ids)
	}
}

// A cursor carrying an unsupported version must yield cursor_stale, not a
// silently-stitched page.
func TestPaginatedMessages_CursorVersionMismatch(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "vertest"
	writeCodexFixture(t, codexHome, sessionID, 4)

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	// Hand-craft a version-999 cursor.
	bad := base64.RawURLEncoding.EncodeToString([]byte(`{"v":999,"sid":"vertest","ord":3,"gen":"x"}`))
	errResp := rpcError(t, sendMessagesRPC(t, h, "codex", sessionID, 5, bad, true))
	if errResp.Code != "cursor_stale" {
		t.Fatalf("expected cursor_stale for version mismatch, got code=%q", errResp.Code)
	}
}

// Protocol samples parse and match the Go wire shapes.
func TestPaginationSamplesMatchWireShape(t *testing.T) {
	for _, raw := range []string{
		`{"sessionId":"s","directory":"/p","limit":50,"paginate":true,"beforeCursor":"abc"}`,
	} {
		var p GetSessionMessagesParams
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if !p.Paginate || p.BeforeCursor != "abc" || p.Limit != 50 {
			t.Errorf("params parsed wrong: %+v", p)
		}
	}
	// Result envelope shape used by the paginated path.
	result := paginatedEnvelope(
		[]map[string]interface{}{{"id": "m1", "role": "user", "content": "hi"}},
		"vertest", &transcriptindex.PageIndex{PrefixGeneration: "g1"}, 2,
	)
	for _, key := range []string{"messages", "oldestCursor", "newestCursor", "hasMore"} {
		if _, ok := result[key]; !ok {
			t.Errorf("paginated result missing %q", key)
		}
	}
	if hasMore, _ := result["hasMore"].(bool); !hasMore {
		t.Errorf("oldestOrdinal=2 should mean hasMore=true")
	}
}
