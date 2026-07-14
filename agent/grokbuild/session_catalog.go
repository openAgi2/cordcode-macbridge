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

// grokToolCall mirrors the tool_calls element in chat_history.jsonl.
// Forensic audit (§2) confirmed each element has:
//   - name      (string)  tool name
//   - id        (string)  stable call ID, used to correlate with tool_result.tool_call_id
//   - arguments (string)  JSON-encoded arguments (e.g. {"command":"rg ..."})
type grokToolCall struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Arguments string `json:"arguments"`
}

// grokToolResult mirrors the tool_result row in chat_history.jsonl.
// Forensic audit confirmed shape: {content (string), tool_call_id (string), type}.
// There is NO status field — success/failed cannot be proven, so steps use "unknown".
type grokToolResult struct {
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
}

type grokHistoryLine struct {
	Type            string          `json:"type"`
	Content         json.RawMessage `json:"content"`
	SyntheticReason string          `json:"synthetic_reason"`
	ToolCalls       []grokToolCall  `json:"tool_calls"`
	ToolResult      *grokToolResult `json:"tool_result"`
	ToolCallID      string          `json:"tool_call_id"`
	ModelID         string          `json:"model_id"`
	// Summary holds the plaintext reasoning summary for reasoning rows.
	// Format: [{"type":"summary_text","text":"..."}]. encrypted_content is
	// opaque ciphertext and cannot be decoded, so summary is the only usable
	// thinking source.
	Summary json.RawMessage `json:"summary"`
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
// Turn-boundary accumulation: consecutive reasoning/assistant/tool_result rows
// are accumulated into a single assistant entry (like Codex's richHistoryBuilder),
// so thinking + tool calls + text appear as one turn. A user row flushes the
// pending turn and starts a new boundary. This mirrors how Claude Code/Codex
// present a turn — reasoning and tools grouped together, not as independent
// messages.
//
// The entry ID is derived from the FIRST physical line of the accumulated turn,
// keeping IDs stable across reads and immune to limit truncation. A turn that
// has no usable content (all lines filtered) is dropped.
//
// Two-pass design: pass 1 collects tool_result content by tool_call_id; pass 2
// builds entries with correlated output. tool_result rows are consumed in pass
// 1 and contribute to their calling tool's step output in pass 2.
func readRichSessionHistory(grokHome, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	dir := findSessionDir(grokHome, sessionID)
	if dir == "" {
		return nil, fmt.Errorf("grokbuild: session not found: %s", sessionID)
	}
	path := filepath.Join(dir, "chat_history.jsonl")

	// Pass 1: collect tool_result content by tool_call_id.
	resultByCallID, err := collectToolResults(path)
	if err != nil {
		return nil, err
	}

	// Pass 2: build rich entries with turn-boundary accumulation.
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

	// Turn accumulator: accumulates parts/steps/thinking/content for consecutive
	// non-user rows. Flushed when a user row is encountered or at EOF.
	var turn turnAccumulator
	flushTurn := func() {
		if e := turn.build(sessionID); e != nil {
			entries = append(entries, *e)
		}
		turn = turnAccumulator{}
	}

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

		rowType := strings.ToLower(strings.TrimSpace(row.Type))

		// User rows are turn boundaries — flush pending turn, then emit user.
		if rowType == "user" {
			flushTurn()
			if row.SyntheticReason != "" {
				continue
			}
			text := strings.TrimSpace(unwrapUserQuery(extractTextContent(row.Content)))
			if text == "" || looksLikeFrameworkBootstrap(text) {
				continue
			}
			entries = append(entries, core.RichHistoryEntry{
				ID:      deriveStableMessageID(sessionID, lineNum, rawLine),
				Role:    "user",
				Content: text,
			})
			continue
		}

		// Accumulate reasoning / assistant / tool_result into the current turn.
		turn.add(row, sessionID, lineNum, rawLine, resultByCallID)
	}
	flushTurn()

	if err := sc.Err(); err != nil {
		return entries, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// turnAccumulator collects consecutive non-user rows into one assistant turn.
type turnAccumulator struct {
	started         bool
	firstID         string           // ID of the first line in this turn (stable entry ID)
	content         string           // assistant text (non-tool)
	thinking        string           // accumulated reasoning summaries (all merged)
	parts           []map[string]any // reasoning/tool/text parts, preserved in source order
	pendingThinking string
	steps           []map[string]any
	modelID         string
}

// add accumulates one row into the current turn. tool_result rows are skipped
// here (they were consumed in pass 1 via resultByCallID).
//
// Consecutive reasoning rows are coalesced, but are flushed before each visible
// tool or text part. This preserves the actual reasoning → tool → reasoning
// timeline instead of moving all thinking to the front of a completed turn.
func (t *turnAccumulator) add(row grokHistoryLine, sessionID string, lineNum int, rawLine []byte, resultByCallID map[string]string) {
	if !t.started {
		t.firstID = deriveStableMessageID(sessionID, lineNum, rawLine)
		t.started = true
	}

	rowType := strings.ToLower(strings.TrimSpace(row.Type))

	switch rowType {
	case "system":
		return
	case "tool_result":
		// Consumed in pass 1; nothing to accumulate.
		return
	case "reasoning":
		text := strings.TrimSpace(extractTextContent(row.Content))
		if text == "" {
			text = strings.TrimSpace(extractReasoningSummary(row.Summary))
		}
		if text == "" {
			return
		}
		t.thinking = appendHistoryText(t.thinking, text)
		t.pendingThinking = appendHistoryText(t.pendingThinking, text)
	case "assistant":
		t.flushReasoning()
		if row.ModelID != "" && t.modelID == "" {
			t.modelID = row.ModelID
		}
		// Build tool steps/parts first (the actual actions), then the text part (the visible utterance).
		// This ensures in Parts order: tools before final text, so iOS renders ProcessGroup (reasoning+tools)
		// before the (often verbose) narrative. Matches Codex presentation where process summaries come first.
		for i, tc := range row.ToolCalls {
			name := strings.TrimSpace(tc.Name)
			if name == "" {
				continue
			}
			stepID := deriveStableStepID(lineNum, i, tc)
			title := deriveToolTitle(name, tc.Arguments)
			output := ""
			if cid := strings.TrimSpace(tc.ID); cid != "" {
				if result, ok := resultByCallID[cid]; ok {
					if isGenericSuccessMessage(result) {
						output = ""
					} else {
						output = result
					}
				}
			}
			step := map[string]any{
				"id":                             stepID,
				"toolName":                       name,
				"status":                         "unknown",
				"output":                         map[string]any{"kind": "inline", "text": output},
				"duration":                       nil,
				"requiresPermissionConfirmation": false,
				"availablePermissionOptions":     []any{},
			}
			if title != "" {
				step["title"] = title
			}
			t.steps = append(t.steps, step)
			t.parts = append(t.parts, map[string]any{"type": "tool", "step": step})
		}

		// Preserve real assistant text in both Content and Parts. The text part is the authoritative
		// boundary between process phases: iOS renders reasoning/tools before it as one process
		// group, then the narrative, then any following process group. Dropping it collapses every
		// tool phase in a turn into one undifferentiated process block.
		text := strings.TrimSpace(extractTextContent(row.Content))
		if text != "" {
			if t.content != "" {
				t.content += "\n"
			}
			t.content += text
			t.parts = append(t.parts, map[string]any{"type": "text", "content": text})
		}
	}
}

func (t *turnAccumulator) flushReasoning() {
	if t.pendingThinking == "" {
		return
	}
	t.parts = append(t.parts, map[string]any{"type": "reasoning", "content": t.pendingThinking})
	t.pendingThinking = ""
}

func appendHistoryText(current, next string) string {
	if current == "" {
		return next
	}
	return current + "\n" + next
}

// build produces the final RichHistoryEntry from the accumulated turn.
// Returns nil if the turn has no usable content (all filtered).
func (t *turnAccumulator) build(_ string) *core.RichHistoryEntry {
	if !t.started {
		return nil
	}
	t.flushReasoning()
	// Entry admission: skip if everything is empty.
	if t.content == "" && t.thinking == "" && len(t.parts) == 0 && len(t.steps) == 0 {
		return nil
	}
	e := &core.RichHistoryEntry{
		ID:       t.firstID,
		Role:     "assistant",
		Content:  t.content,
		Thinking: t.thinking,
		Parts:    t.parts,
		Steps:    t.steps,
		Files:    []map[string]any{},
	}
	if t.modelID != "" {
		e.ModelID = t.modelID
	}
	return e
}

// collectToolResults scans chat_history.jsonl once and returns a map of
// tool_call_id → result content (≤500 runes). Longer results are dropped,
// matching legacy behavior (§4.2(e) option 1).
func collectToolResults(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		rawLine := bytes.TrimSpace(sc.Bytes())
		if len(rawLine) == 0 {
			continue
		}
		var row grokHistoryLine
		if err := json.Unmarshal(rawLine, &row); err != nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(row.Type)) != "tool_result" {
			continue
		}
		cid := strings.TrimSpace(row.ToolCallID)
		if cid == "" {
			continue
		}
		text := strings.TrimSpace(extractTextContent(row.Content))
		if text == "" || utf8.RuneCountInString(text) > 500 {
			continue
		}
		out[cid] = text
	}
	return out, sc.Err()
}

// deriveStableStepID produces a deterministic step ID from the physical line
// number + tool call index + raw-line hash. This is more stable than a simple
// in-entry index "tool-N" because it survives filtering changes (R1-P1-3).
func deriveStableStepID(lineNum, callIndex int, tc grokToolCall) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", lineNum, callIndex, tc.ID, tc.Name)))
	return fmt.Sprintf("tool-%d-%x", lineNum, h[:8])
}

// deriveToolTitle extracts a human-readable title from proven tool arguments.
// Only fields confirmed by the §2 forensic audit are used; no guessing from
// tool name or model text. Returns "" if no usable argument is found.
func deriveToolTitle(toolName, argumentsJSON string) string {
	argumentsJSON = strings.TrimSpace(argumentsJSON)
	if argumentsJSON == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch name {
	case "run_terminal_command", "bash", "execute":
		// command + description
		var parts []string
		if desc, ok := args["description"].(string); ok && strings.TrimSpace(desc) != "" {
			parts = append(parts, strings.TrimSpace(desc))
		}
		if cmd, ok := args["command"].(string); ok && strings.TrimSpace(cmd) != "" {
			parts = append(parts, strings.TrimSpace(cmd))
		}
		if len(parts) > 0 {
			return strings.Join(parts, " — ")
		}
	case "read_file", "read":
		if path, ok := args["target_file"].(string); ok && strings.TrimSpace(path) != "" {
			return strings.TrimSpace(path)
		}
		if path, ok := args["path"].(string); ok && strings.TrimSpace(path) != "" {
			return strings.TrimSpace(path)
		}
	case "grep", "search":
		if pattern, ok := args["pattern"].(string); ok && strings.TrimSpace(pattern) != "" {
			return strings.TrimSpace(pattern)
		}
	case "list_dir", "glob", "ls":
		if dir, ok := args["target_directory"].(string); ok && strings.TrimSpace(dir) != "" {
			return strings.TrimSpace(dir)
		}
		if dir, ok := args["path"].(string); ok && strings.TrimSpace(dir) != "" {
			return strings.TrimSpace(dir)
		}
	case "search_replace", "edit", "write", "filechange", "file_change", "patch":
		for _, key := range []string{"file_path", "path", "target_file", "filename", "file", "target_path"} {
			if path, ok := args[key].(string); ok && strings.TrimSpace(path) != "" {
				return strings.TrimSpace(path)
			}
		}
	}
	// Fallback: no proven title — return empty (do not guess from tool name).
	return ""
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

// extractReasoningSummary extracts plaintext thinking from a Grok reasoning
// row's "summary" field. The field is a JSON array of summary segments,
// typically [{"type":"summary_text","text":"..."}]. We concatenate all
// summary_text segments. Returns "" if the field is absent or malformed.
func extractReasoningSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var segments []map[string]any
	if err := json.Unmarshal(raw, &segments); err != nil {
		return ""
	}
	var b strings.Builder
	for _, seg := range segments {
		if segType, _ := seg["type"].(string); segType == "summary_text" {
			if text, _ := seg["text"].(string); strings.TrimSpace(text) != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

// isGenericSuccessMessage detects common low-value success strings from tool results
// (e.g. "The file X has been updated successfully.") so we can avoid polluting
// step output. Structured title + status is sufficient for the process UI.
func isGenericSuccessMessage(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return false
	}
	return strings.Contains(t, "has been updated successfully") ||
		strings.Contains(t, "updated successfully") ||
		(t == "success" || strings.HasSuffix(t, "successfully"))
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
