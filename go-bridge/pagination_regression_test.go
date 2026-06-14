package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cccode-macbridge/agent/codex"
)

// Full backward traversal of a session via cursors must cover every logical
// message exactly once (no duplicates, no gaps) and every page response must
// stay under the wire-byte budget — the guarantee that defeats close-1009.
func TestPaginatedMessages_FullBackwardTraversalNoDupesOrGaps(t *testing.T) {
	codexHome := t.TempDir()
	sessionID := "traversal"
	const turns = 12 // 24 logical messages
	writeCodexFixture(t, codexHome, sessionID, turns)

	agent, err := codex.New(map[string]any{"work_dir": ".", "codex_home": codexHome})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	seen := make(map[string]bool)
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("paged more than 100 times; cursor loop suspected")
		}
		page := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 5, cursor, true))
		msgs := page["messages"].([]map[string]interface{})

		// Byte-budget: the marshaled page messages must never exceed the budget.
		if enc, err := json.Marshal(msgs); err == nil && len(enc) > maxPageResponseBytes {
			t.Errorf("page %d encoded %d bytes > budget %d", pages, len(enc), maxPageResponseBytes)
		}
		for _, m := range msgs {
			id, _ := m["id"].(string)
			if id == "" {
				t.Fatal("paginated message has an empty id")
			}
			if seen[id] {
				t.Errorf("duplicate message across pages: %s", id)
			}
			seen[id] = true
		}
		hasMore, _ := page["hasMore"].(bool)
		if !hasMore {
			break
		}
		cursor, _ = page["oldestCursor"].(string)
		if cursor == "" {
			t.Fatal("hasMore=true but no oldestCursor to continue")
		}
	}

	if len(seen) != 2*turns {
		t.Errorf("traversed %d unique messages, want %d (gap detected)", len(seen), 2*turns)
	}
}

// Against the real close-1009 Codex session (env-gated), the first page response
// must be bounded under the byte budget — far below the ~12.45MB frame — proving
// the byte budget fixes close-1009 even for a session whose total history is
// ~47.8MB across only 48 logical messages.
func TestPaginatedMessages_RealSessionFirstPageBounded(t *testing.T) {
	if os.Getenv("CCCODE_SESSION_LOADING_BASELINE") != "1" {
		t.Skip("set CCCODE_SESSION_LOADING_BASELINE=1 to run against real local transcripts")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir := filepath.Join(home, ".codex", "sessions")
	var path string
	var size int64
	requestedSessionID := os.Getenv("CCCODE_SESSION_LOADING_SESSION_ID")
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if requestedSessionID != "" {
			if strings.Contains(filepath.Base(p), requestedSessionID) {
				path = p
				size = info.Size()
			}
			return nil
		}
		if info.Size() > size {
			size = info.Size()
			path = p
		}
		return nil
	})
	if path == "" || size < 1<<20 {
		t.Skip("no codex session >= 1MB found")
	}
	sessionID := requestedSessionID
	if sessionID == "" {
		sessionID = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".jsonl"), "")
		if i := strings.LastIndex(sessionID, "-"); i >= 0 {
			sessionID = sessionID[i+1:]
		}
	}

	agent, err := codex.New(map[string]any{"work_dir": "."}) // default ~/.codex
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlers()
	h.SetTranscriptIndexBaseDir(t.TempDir())
	h.RegisterAgent("codex", agent)

	page := rpcData(t, sendMessagesRPC(t, h, "codex", sessionID, 50, "", true))
	msgs := page["messages"].([]map[string]interface{})
	ids := make(map[string]bool, len(msgs))
	roles := make(map[string]bool, len(msgs))
	for _, message := range msgs {
		id, _ := message["id"].(string)
		if strings.TrimSpace(id) == "" {
			t.Fatal("real paginated message has an empty id")
		}
		if ids[id] {
			t.Fatalf("real paginated page has duplicate message id %q", id)
		}
		ids[id] = true
		role, _ := message["role"].(string)
		roles[role] = true
	}
	if !roles["user"] || !roles["assistant"] {
		t.Fatalf("real first page roles=%v, want user and assistant", roles)
	}
	enc, _ := json.Marshal(msgs)
	t.Logf("real session first page: %d messages, %d bytes (budget=%d, file=%d)",
		len(msgs), len(enc), maxPageResponseBytes, size)
	if len(enc) > maxPageResponseBytes {
		if len(msgs) == 1 {
			for key, value := range msgs[0] {
				field, _ := json.Marshal(value)
				t.Logf("oversized message field %s: %d bytes", key, len(field))
			}
		}
		t.Errorf("real first page %d bytes exceeds budget %d", len(enc), maxPageResponseBytes)
	}
	// The close-1009 frame was ~12.45MB; the first page must be far under it.
	const close1009 = 12 * (1 << 20)
	if len(enc) >= close1009 {
		t.Errorf("real first page %d bytes >= close-1009 threshold %d", len(enc), close1009)
	}
}
