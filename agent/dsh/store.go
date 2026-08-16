// store.go reads the user's DSH harness session store (§2026-08-16 design):
// sessions live under $DSH_HOME/sessions/<projectKey(cwd)>/<sessionId>/session.jsonl[.zstd].
// Reading is a reuse of the user's own storage (零迁移、不自建真相源) — the
// driver never writes here outside the harness process itself (624c6a4 form).
//
// Format facts (source-verified against dsh pin 47f9438):
//   - projectKey is LOSSY (separator runs collapse to '-'), so the true cwd
//     comes from each log's header line, never from the directory name.
//   - The harness web profile and the driver composition both write zstd
//     artifacts (the driver pinned none until 2026-08-16, when the shared
//     user store made the encoding check reject the mix) — both suffixes
//     stay readable for any plaintext legacy logs.
package dsh

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// storeTitleScanBudget bounds the decompressed-prefix bytes examined when
	// deriving a list title; session/title events and the first human prompt
	// both appear near the head of the log.
	storeTitleScanBudget = 512 << 10

	// Fallback-title limits (driver-chosen; only used for logs without
	// session/title events — the harness web writes those with its own
	// provider config).
	storeTitleFallbackMaxWords = 12
	storeTitleFallbackMaxBytes = 64
)

// dshSessionStore is a read view over the user harness session store.
type dshSessionStore struct {
	root string
}

// openDshSessionStore resolves the store root: $DSH_HOME/sessions (HOME
// resolution failure ⇒ no store — an honest empty list, never an error).
func openDshSessionStore() *dshSessionStore {
	home := dshHome()
	if home == "" {
		return &dshSessionStore{root: ""}
	}
	return &dshSessionStore{root: filepath.Join(home, sessionsSubdir)}
}

// storeSession is one discovered session log.
type storeSession struct {
	ID        string
	Cwd       string // header cwd — the true project directory
	CreatedAt int64  // header createdAt (ms)
	Path      string // session.jsonl or session.jsonl.zstd
	Plain     bool   // true ⇒ .jsonl, false ⇒ .jsonl.zstd
	ModTime   int64  // file mtime (unix seconds)
	Subagent  bool   // delegated child task, hidden from lists
}

// dshSessionHeader is the first JSONL record of a session log
// (session-persistence-jsonl format.ts HeaderLine).
type dshSessionHeader struct {
	Type            string `json:"type"`
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CreatedAt       int64  `json:"createdAt"`
	Cwd             string `json:"cwd,omitempty"`
	ParentSession   string `json:"parentSession,omitempty"`
	SeedLength      *int   `json:"seedLength,omitempty"`
	Origin          string `json:"origin,omitempty"`
	DelegationDepth int    `json:"delegationDepth"`
	AgentPreset     string `json:"agentPreset,omitempty"`
}

// isSubagent reports whether a session is a delegated child task, not a
// user conversation (origin:"subagent" or delegationDepth>0). The list hides
// these, matching the harness web session list.
func (h *dshSessionHeader) isSubagent() bool {
	return h.Origin == "subagent" || h.DelegationDepth > 0
}

// projectKey ports dsh-session-persistence-jsonl format.ts projectKey():
// '/'/'\'/':' runs collapse to one '-', [A-Za-z0-9._-] stays literal (except
// '~'), every other UTF-16 code unit becomes ~XXXX (uppercase hex), leading
// '-' is stripped, empty collapses to "root", and the slug is wrapped as
// --<slug>-- capped at 251 code units. TS operates on UTF-16 code units, so
// the Go port must too (astral chars are surrogate pairs there).
func projectKey(cwd string) string {
	if cwd == "" {
		return ""
	}
	units := utf16.Encode([]rune(cwd))
	var out []uint16
	separatorRun := false
	for _, u := range units {
		ch := rune(u)
		switch {
		case ch == '/' || ch == '\\' || ch == ':':
			if !separatorRun {
				out = append(out, '-')
			}
			separatorRun = true
			continue
		case ch != '~' && isProjectKeySafe(ch):
			out = append(out, u)
		default:
			out = append(out, escapeUnit(u)...)
		}
		separatorRun = false
	}
	slug := string(utf16.Decode(out))
	slug = strings.TrimLeft(slug, "-")
	if slug == "" {
		slug = "root"
	}
	runes := []rune(slug)
	if len(runes) > 251 {
		// Match TS slice(0,251) on code units; a cut surrogate pair would be
		// invalid UTF-8 material, so drop a dangling trailing half.
		units := utf16.Encode(runes[:251])
		if n := len(units); n > 0 && utf16.IsSurrogate(rune(units[n-1])) {
			units = units[:n-1]
		}
		slug = string(utf16.Decode(units))
	}
	return "--" + slug + "--"
}

func isProjectKeySafe(ch rune) bool {
	if ch >= utf8.RuneSelf {
		return false
	}
	c := byte(ch)
	return c == '.' || c == '_' || c == '-' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func escapeUnit(u uint16) []uint16 {
	const hex = "0123456789ABCDEF"
	out := make([]uint16, 0, 5)
	out = append(out, '~',
		uint16(hex[(u>>12)&0xF]), uint16(hex[(u>>8)&0xF]),
		uint16(hex[(u>>4)&0xF]), uint16(hex[u&0xF]))
	return out
}

// scanSessions walks the whole store (every project directory) and returns
// every parseable session log. Directory names are not trusted for identity:
// the header id/cwd win. Unreadable or malformed entries are skipped — a
// partial store still lists its healthy sessions.
func (s *dshSessionStore) scanSessions() ([]storeSession, error) {
	if s.root == "" {
		return nil, nil
	}
	projectDirs, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("dsh store: read %s: %w", s.root, err)
	}
	var sessions []storeSession
	for _, project := range projectDirs {
		if !project.IsDir() {
			continue
		}
		sessionDirs, err := os.ReadDir(filepath.Join(s.root, project.Name()))
		if err != nil {
			continue
		}
		for _, dir := range sessionDirs {
			if !dir.IsDir() {
				continue
			}
			sess, ok := s.loadSessionDir(filepath.Join(s.root, project.Name(), dir.Name()))
			if ok {
				sessions = append(sessions, sess)
			}
		}
	}
	return sessions, nil
}

// loadSessionDir resolves one <store>/<projectKey>/<sessionId>/ directory to
// a storeSession via its log header.
func (s *dshSessionStore) loadSessionDir(dir string) (storeSession, bool) {
	path, plain, err := sessionLogPath(dir)
	if err != nil {
		return storeSession{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return storeSession{}, false
	}
	header, err := readSessionHeader(path, plain)
	if err != nil || header.Type != "session" || header.ID == "" {
		return storeSession{}, false
	}
	return storeSession{
		ID:        header.ID,
		Cwd:       header.Cwd,
		CreatedAt: header.CreatedAt,
		Path:      path,
		Plain:     plain,
		ModTime:   info.ModTime().Unix(),
		Subagent:  header.isSubagent(),
	}, true
}

// sessionLogPath picks the session artifact inside one session directory,
// preferring the plaintext log (driver composition) and accepting the zstd
// artifact (harness web default). Both may exist across format changes; the
// newer mtime wins.
func sessionLogPath(dir string) (path string, plain bool, err error) {
	plainPath := filepath.Join(dir, "session.jsonl")
	zstdPath := filepath.Join(dir, "session.jsonl.zstd")
	plainInfo, plainErr := os.Stat(plainPath)
	zstdInfo, zstdErr := os.Stat(zstdPath)
	havePlain := plainErr == nil && !plainInfo.IsDir()
	haveZstd := zstdErr == nil && !zstdInfo.IsDir()
	switch {
	case havePlain && haveZstd:
		if zstdInfo.ModTime().After(plainInfo.ModTime()) {
			return zstdPath, false, nil
		}
		return plainPath, true, nil
	case havePlain:
		return plainPath, true, nil
	case haveZstd:
		return zstdPath, false, nil
	default:
		return "", false, os.ErrNotExist
	}
}

// resolveSessionFile finds the log for a session id across all project
// directories (the harness's findClaudeSessionFile counterpart). Ids are
// path-unsafe by contract (branded strings) — separators never reach disk.
func (s *dshSessionStore) resolveSessionFile(sessionID string) (storeSession, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return storeSession{}, false
	}
	if s.root == "" {
		return storeSession{}, false
	}
	projectDirs, err := os.ReadDir(s.root)
	if err != nil {
		return storeSession{}, false
	}
	for _, project := range projectDirs {
		if !project.IsDir() {
			continue
		}
		if sess, ok := s.loadSessionDir(filepath.Join(s.root, project.Name(), sessionID)); ok {
			return sess, true
		}
	}
	return storeSession{}, false
}

// readSessionHeader parses only the first record of a session log.
func readSessionHeader(path string, plain bool) (*dshSessionHeader, error) {
	closer, err := openSessionLog(path, plain)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	dec := json.NewDecoder(bufio.NewReaderSize(closer, 64<<10))
	var header dshSessionHeader
	if err := dec.Decode(&header); err != nil {
		return nil, err
	}
	return &header, nil
}

// openSessionLog opens a session log as a stream of JSON bytes. zstd frames
// are decompressed with the pure-Go klauspost decoder; concurrent decode is
// disabled — these reads are latency-insensitive listing/history paths.
func openSessionLog(path string, plain bool) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if plain {
		return f, nil
	}
	zr, err := newZstdReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &zstdLogReader{file: f, zr: zr}, nil
}

// sessionTitle derives a list title: the latest session/title event folded
// from the log head, else the first human user/message prompt trimmed to the
// fallback limits. Both sources appear near the head, so the scan stops at
// storeTitleScanBudget decompressed bytes.
func sessionTitle(path string, plain bool) string {
	closer, err := openSessionLog(path, plain)
	if err != nil {
		return ""
	}
	defer closer.Close()
	limited := io.LimitReader(closer, storeTitleScanBudget)
	dec := json.NewDecoder(bufio.NewReaderSize(limited, 64<<10))
	fallback := ""
	for {
		var rec dshLogRecord
		if err := dec.Decode(&rec); err != nil {
			break // budget exhausted or torn tail: keep what we have
		}
		switch rec.Type {
		case "session/title":
			var d dshTitleData
			if json.Unmarshal(rec.Data, &d) == nil && strings.TrimSpace(d.Title) != "" {
				return d.Title
			}
		case "user/message":
			if fallback == "" {
				var d dshUserMessageData
				if json.Unmarshal(rec.Data, &d) == nil && d.Source != nil && d.Source.Kind == "user" {
					fallback = fallbackTitleFromBlocks(d.Content)
				}
			}
		}
	}
	return fallback
}

// fallbackTitleFromBlocks joins an eligible prompt's text blocks and trims to
// the fallback limits (whitespace-normalized, never splitting a code point).
func fallbackTitleFromBlocks(blocks []dshContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(b.Text)
		}
	}
	normalized := strings.Join(strings.Fields(sb.String()), " ")
	words := strings.Split(normalized, " ")
	if len(words) > storeTitleFallbackMaxWords {
		words = words[:storeTitleFallbackMaxWords]
	}
	out := strings.Join(words, " ")
	for len(out) > storeTitleFallbackMaxBytes {
		last := len(out) - 1
		for !utf8.RuneStart(out[last]) {
			last--
		}
		if last == 0 {
			return ""
		}
		out = strings.TrimRight(out[:last], " ")
	}
	return out
}

// dshLogRecord is the generic envelope {type,seq,time,data}; data is decoded
// per-type only by the record consumers that need it.
type dshLogRecord struct {
	Type string          `json:"type"`
	Seq  int             `json:"seq"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

// dshSource is the shared source discriminant (user/plugin/model/tool kinds).
type dshSource struct {
	Kind   string `json:"kind"`
	Plugin string `json:"plugin,omitempty"`
	CallID string `json:"callId,omitempty"`
}

// dshContentBlock is one message content block; the text/reasoning/tool-call/
// tool-result shapes share this envelope (nested content belongs to
// tool-result blocks).
type dshContentBlock struct {
	Type       string            `json:"type"`
	Text       string            `json:"text,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Arguments  json.RawMessage   `json:"arguments,omitempty"`
	ToolCallID string            `json:"toolCallId,omitempty"`
	Content    []dshContentBlock `json:"content,omitempty"`
}

// dshTitleData is session/title's data payload.
type dshTitleData struct {
	Title  string     `json:"title"`
	Source *dshSource `json:"source,omitempty"`
}

// dshUserMessageData is user/message's data payload.
type dshUserMessageData struct {
	Content []dshContentBlock `json:"content"`
	Source  *dshSource        `json:"source,omitempty"`
	Role    string            `json:"role,omitempty"`
	ID      string            `json:"id,omitempty"`
}

// StoreHasSession reports whether the user harness store holds a log for the
// session id. go-bridge uses it to pick the DSH projection baseline source:
// a store log cold-hydrates as the file-backed baseline, while its absence
// falls back to the live-only admission window (design §4.4).
func StoreHasSession(sessionID string) bool {
	_, ok := openDshSessionStore().resolveSessionFile(sessionID)
	return ok
}
