package grokbuild

// Local session catalog for Grok Build CLI.
//
// ACP session/list is not implemented by grok 0.2.93 (Method not found).
// Sessions are persisted under $GROK_HOME/sessions/ (default ~/.grok/sessions):
//   sessions/<url-encoded-cwd>/<sessionId>/summary.json
//   sessions/<url-encoded-cwd>/<sessionId>/chat_history.jsonl
//   sessions/session_search.sqlite  (session_id, cwd, updated_at, title, content)
//
// ListSessions + HistoryProvider read these on-disk artifacts so iOS can
// discover and resume Mac-side Grok sessions without the missing ACP list RPC.
//
// SQLite is queried via the optional `sqlite3` CLI (same approach as the
// OpenCode driver) to avoid adding a CGO/pure-Go sqlite dependency to the
// main MacBridge module. When sqlite3 is unavailable, summary.json walk is
// sufficient; titles may be filled from the first user line in chat_history.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// resolveGrokHome returns the Grok home directory.
// Priority: explicit override > GROK_HOME env > ~/.grok
func resolveGrokHome(explicit string) string {
	if h := strings.TrimSpace(explicit); h != "" {
		return h
	}
	if h := strings.TrimSpace(os.Getenv("GROK_HOME")); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".grok")
}

func (a *Agent) grokHomeLocked() string {
	return resolveGrokHome(a.grokHome)
}

// ListSessions returns sessions known from the local Grok session store.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	a.mu.RLock()
	home := a.grokHomeLocked()
	a.mu.RUnlock()
	if home == "" {
		return nil, nil
	}
	return listLocalSessions(ctx, home)
}

// GetSessionHistory implements core.HistoryProvider by reading chat_history.jsonl.
func (a *Agent) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	_ = ctx
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("grokbuild: empty session id")
	}
	a.mu.RLock()
	home := a.grokHomeLocked()
	a.mu.RUnlock()
	if home == "" {
		return nil, fmt.Errorf("grokbuild: cannot resolve GROK_HOME")
	}
	return readSessionHistory(home, sessionID, limit)
}

// GetRichSessionHistory implements core.RichHistoryProvider.
//
// Unlike the legacy HistoryProvider path (which returns core.HistoryEntry
// without an ID field), this returns RichHistoryEntry with a stable,
// deterministic ID derived from the sessionID + JSONL physical line number +
// hash of the raw JSONL line.  The same chat_history.jsonl read twice yields
// identical IDs, so iOS external-turn probe sees the same message set and does
// not falsely activate generation.
//
// The ID is derived from the physical line number in the JSONL file — NOT the
// index in the filtered output array.  This makes IDs immune to changes in
// filtering (system/synthetic/bootstrap lines), limit truncation, or future
// additions of new record types.
func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	_ = ctx
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("grokbuild: empty session id")
	}
	a.mu.RLock()
	home := a.grokHomeLocked()
	a.mu.RUnlock()
	if home == "" {
		return nil, fmt.Errorf("grokbuild: cannot resolve GROK_HOME")
	}
	return readRichSessionHistory(home, sessionID, limit)
}

func listLocalSessions(ctx context.Context, grokHome string) ([]core.AgentSessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionsDir := filepath.Join(grokHome, "sessions")
	if st, err := os.Stat(sessionsDir); err != nil || !st.IsDir() {
		return nil, nil
	}

	byID := map[string]core.AgentSessionInfo{}

	// Optional index from session_search.sqlite via sqlite3 CLI (id/cwd/updated_at/title).
	sqliteByID := querySessionsFromSQLite(filepath.Join(sessionsDir, "session_search.sqlite"))

	// Walk summary.json as the durable catalog source.
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() != "summary.json" {
			return nil
		}
		info, ok := parseSummaryFile(path)
		if !ok || info.ID == "" {
			return nil
		}
		if sq, found := sqliteByID[info.ID]; found {
			if info.Summary == "" && sq.Summary != "" {
				info.Summary = sq.Summary
			}
			if info.Directory == "" && sq.Directory != "" {
				info.Directory = sq.Directory
			}
			if sq.ModifiedAt.After(info.ModifiedAt) {
				info.ModifiedAt = sq.ModifiedAt
			}
		}
		if info.Summary == "" {
			if t := firstUserTitleFromHistory(filepath.Join(filepath.Dir(path), "chat_history.jsonl")); t != "" {
				info.Summary = t
			}
		}
		if info.Summary == "" {
			info.Summary = fallbackSessionTitle(info)
		}
		byID[info.ID] = info
		return nil
	})

	// Sessions present only in sqlite (no summary yet) still surface with full fields.
	for id, sq := range sqliteByID {
		if _, ok := byID[id]; ok {
			continue
		}
		if sq.Summary == "" {
			sq.Summary = fallbackSessionTitle(sq)
		}
		if sq.ModifiedAt.IsZero() {
			sq.ModifiedAt = time.Now().UTC()
		}
		byID[id] = sq
	}

	out := make([]core.AgentSessionInfo, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModifiedAt.After(out[j].ModifiedAt)
	})
	return out, nil
}

// querySessionsFromSQLite uses the system sqlite3 CLI when present.
// Returns id → AgentSessionInfo with Directory/ModifiedAt/Summary filled.
// Failures yield an empty map (non-fatal).
func querySessionsFromSQLite(dbPath string) map[string]core.AgentSessionInfo {
	out := map[string]core.AgentSessionInfo{}
	if _, err := os.Stat(dbPath); err != nil {
		return out
	}
	sqlite3, err := exec.LookPath("sqlite3")
	if err != nil {
		return out
	}
	// Columns: session_id, cwd, updated_at, title
	cmd := exec.Command(sqlite3, "-separator", "\t", dbPath,
		`SELECT session_id, cwd, updated_at, replace(replace(ifnull(title,''), char(9), ' '), char(10), ' ') FROM session_docs;`)
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 1 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		if id == "" {
			continue
		}
		cwd := ""
		if len(parts) >= 2 {
			cwd = strings.TrimSpace(parts[1])
		}
		var mod time.Time
		if len(parts) >= 3 {
			mod = parseSQLiteUpdatedAt(strings.TrimSpace(parts[2]))
		}
		title := ""
		if len(parts) >= 4 {
			title = strings.TrimSpace(parts[3])
		}
		out[id] = core.AgentSessionInfo{
			ID:         id,
			Summary:    title,
			Directory:  cwd,
			ModifiedAt: mod,
		}
	}
	return out
}

func parseSQLiteUpdatedAt(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	// Integer unix seconds or milliseconds.
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 1_000_000_000_000 {
			return time.UnixMilli(n).UTC()
		}
		if n > 0 {
			return time.Unix(n, 0).UTC()
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

type grokSummaryFile struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	SessionSummary  string `json:"session_summary"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	LastActiveAt    string `json:"last_active_at"`
	NumMessages     int    `json:"num_messages"`
	NumChatMessages int    `json:"num_chat_messages"`
	CurrentModelID  string `json:"current_model_id"`
	AgentName       string `json:"agent_name"`
}

func parseSummaryFile(path string) (core.AgentSessionInfo, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return core.AgentSessionInfo{}, false
	}
	var s grokSummaryFile
	if err := json.Unmarshal(raw, &s); err != nil {
		return core.AgentSessionInfo{}, false
	}
	id := strings.TrimSpace(s.Info.ID)
	if id == "" {
		id = filepath.Base(filepath.Dir(path))
	}
	if id == "" || id == "." || id == "sessions" {
		return core.AgentSessionInfo{}, false
	}
	cwd := strings.TrimSpace(s.Info.CWD)
	if cwd == "" {
		encoded := filepath.Base(filepath.Dir(filepath.Dir(path)))
		if decoded, err := url.PathUnescape(encoded); err == nil && decoded != "" && decoded != "sessions" {
			cwd = decoded
		}
	}
	mod := parseGrokTime(s.LastActiveAt)
	if mod.IsZero() {
		mod = parseGrokTime(s.UpdatedAt)
	}
	if mod.IsZero() {
		mod = parseGrokTime(s.CreatedAt)
	}
	if mod.IsZero() {
		if st, err := os.Stat(path); err == nil {
			mod = st.ModTime().UTC()
		}
	}
	msgCount := s.NumChatMessages
	if msgCount == 0 {
		msgCount = s.NumMessages
	}
	return core.AgentSessionInfo{
		ID:           id,
		Summary:      strings.TrimSpace(s.SessionSummary),
		Directory:    cwd,
		MessageCount: msgCount,
		ModifiedAt:   mod,
		ModelID:      strings.TrimSpace(s.CurrentModelID),
	}, true
}

func parseGrokTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func fallbackSessionTitle(s core.AgentSessionInfo) string {
	if s.Directory != "" {
		base := filepath.Base(s.Directory)
		if base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	if len(s.ID) > 8 {
		return s.ID[:8]
	}
	return s.ID
}

func firstUserTitleFromHistory(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var row grokHistoryLine
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			continue
		}
		if strings.ToLower(row.Type) != "user" || row.SyntheticReason != "" {
			continue
		}
		text := strings.TrimSpace(unwrapUserQuery(extractTextContent(row.Content)))
		if text == "" || looksLikeFrameworkBootstrap(text) {
			continue
		}
		// One-line title, truncated.
		text = strings.ReplaceAll(text, "\n", " ")
		if utf8.RuneCountInString(text) > 80 {
			r := []rune(text)
			text = string(r[:80]) + "…"
		}
		return text
	}
	return ""
}

// findSessionDir locates the on-disk directory for a session id.
func findSessionDir(grokHome, sessionID string) string {
	sessionsDir := filepath.Join(grokHome, "sessions")
	var found string
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() != sessionID {
			return nil
		}
		if fileExists(filepath.Join(path, "chat_history.jsonl")) || fileExists(filepath.Join(path, "summary.json")) {
			found = path
			return filepath.SkipAll
		}
		if found == "" {
			found = path
		}
		return nil
	})
	return found
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

type grokHistoryLine struct {
	Type            string          `json:"type"`
	Content         json.RawMessage `json:"content"`
	SyntheticReason string          `json:"synthetic_reason"`
	ToolCalls       []struct {
		Name string `json:"name"`
	} `json:"tool_calls"`
}

func readSessionHistory(grokHome, sessionID string, limit int) ([]core.HistoryEntry, error) {
	dir := findSessionDir(grokHome, sessionID)
	if dir == "" {
		return nil, fmt.Errorf("grokbuild: session not found: %s", sessionID)
	}
	path := filepath.Join(dir, "chat_history.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []core.HistoryEntry{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []core.HistoryEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var row grokHistoryLine
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		role, content := mapHistoryLine(row)
		if role == "" || content == "" {
			continue
		}
		entries = append(entries, core.HistoryEntry{
			Role:    role,
			Content: content,
		})
	}
	if err := sc.Err(); err != nil {
		return entries, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// readRichSessionHistory reads chat_history.jsonl and returns RichHistoryEntry
// with stable IDs derived from the physical JSONL line number.
//
// The physical line counter (lineNum) increments for every non-blank line in
// the file — including lines that are filtered out (system, synthetic,
// bootstrap).  This ensures that adding or removing a filtered line does NOT
// shift the IDs of subsequent messages, keeping IDs stable across reads even
// when the file grows or filtering logic changes.
func readRichSessionHistory(grokHome, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	dir := findSessionDir(grokHome, sessionID)
	if dir == "" {
		return nil, fmt.Errorf("grokbuild: session not found: %s", sessionID)
	}
	path := filepath.Join(dir, "chat_history.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []core.RichHistoryEntry{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []core.RichHistoryEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNum := 0
	for sc.Scan() {
		rawLine := bytes.TrimSpace(sc.Bytes())
		if len(rawLine) == 0 {
			continue
		}
		lineNum++
		var row grokHistoryLine
		if err := json.Unmarshal(rawLine, &row); err != nil {
			continue
		}
		role, content := mapHistoryLine(row)
		if role == "" || content == "" {
			continue
		}
		entries = append(entries, core.RichHistoryEntry{
			ID:      deriveStableMessageID(sessionID, lineNum, rawLine),
			Role:    role,
			Content: content,
		})
	}
	if err := sc.Err(); err != nil {
		return entries, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// deriveStableMessageID produces a deterministic ID for a Grok history message.
//
// The ID is derived from sessionID + physical line number + SHA-256 hash of
// the raw JSONL line content.  The same file read twice always yields the same
// IDs.  The full sessionID is hashed (never truncated) to avoid index-out-of-
// range panics on short session IDs.
func deriveStableMessageID(sessionID string, lineNum int, rawLine []byte) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", sessionID, lineNum, rawLine)))
	return fmt.Sprintf("grok-%x", h[:16])
}

func mapHistoryLine(row grokHistoryLine) (role, content string) {
	switch strings.ToLower(strings.TrimSpace(row.Type)) {
	case "system":
		return "", ""
	case "user":
		if row.SyntheticReason != "" {
			return "", ""
		}
		text := strings.TrimSpace(unwrapUserQuery(extractTextContent(row.Content)))
		if text == "" || looksLikeFrameworkBootstrap(text) {
			return "", ""
		}
		return "user", text
	case "assistant":
		text := strings.TrimSpace(extractTextContent(row.Content))
		if text == "" && len(row.ToolCalls) > 0 {
			names := make([]string, 0, len(row.ToolCalls))
			for _, tc := range row.ToolCalls {
				if n := strings.TrimSpace(tc.Name); n != "" {
					names = append(names, n)
				}
			}
			if len(names) > 0 {
				text = "Tool: " + strings.Join(names, ", ")
			}
		}
		if text == "" {
			return "", ""
		}
		return "assistant", text
	case "reasoning":
		text := strings.TrimSpace(extractTextContent(row.Content))
		if text == "" {
			return "", ""
		}
		return "assistant", text
	case "tool_result":
		text := strings.TrimSpace(extractTextContent(row.Content))
		if text == "" || utf8.RuneCountInString(text) > 500 {
			return "", ""
		}
		return "assistant", text
	default:
		return "", ""
	}
}

func extractTextContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if t, ok := p["text"].(string); ok && t != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(t)
			}
		}
		return b.String()
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if t, ok := obj["text"].(string); ok {
			return t
		}
	}
	return ""
}

func unwrapUserQuery(text string) string {
	const open = "<user_query>"
	const close = "</user_query>"
	start := strings.Index(text, open)
	if start < 0 {
		return text
	}
	start += len(open)
	end := strings.Index(text[start:], close)
	if end < 0 {
		return strings.TrimSpace(text[start:])
	}
	return strings.TrimSpace(text[start : start+end])
}

func looksLikeFrameworkBootstrap(text string) bool {
	trim := strings.TrimSpace(text)
	if strings.HasPrefix(trim, "<user_info>") {
		return true
	}
	if strings.HasPrefix(trim, "<system-reminder>") {
		return true
	}
	if strings.Contains(text, "Available tools:") && strings.Contains(text, "function calls") && len(text) > 2000 {
		return true
	}
	return false
}
