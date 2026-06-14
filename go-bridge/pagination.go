package gobridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/openAgi2/cccode-macbridge/agent/claudecode"
	"github.com/openAgi2/cccode-macbridge/agent/codex"
	"github.com/openAgi2/cccode-macbridge/core"
	"github.com/openAgi2/cccode-macbridge/transcriptindex"
)

// Pagination defaults. The per-page byte budget leaves room below Relay's 1 MiB
// WebSocket limit for encrypted envelope metadata. Message-count paging alone
// cannot bound payload for sessions with a giant tool output, so the page is
// also trimmed by encoded wire bytes.
const (
	defaultPageSize      = 50
	maxPageSize          = 200
	maxPageResponseBytes = 960 << 10
)

// defaultTranscriptIndexDir returns the default persistence root for transcript
// page indexes when the Bridge data directory is not otherwise configured.
func defaultTranscriptIndexDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cccode", "transcript-index")
}

// transcriptBackend maps a Bridge backend id to a transcriptindex backend.
func transcriptBackend(backendID string) (transcriptindex.Backend, bool) {
	switch backendID {
	case "codex":
		return transcriptindex.BackendCodex, true
	case "claudecode", "claude":
		return transcriptindex.BackendClaude, true
	default:
		return "", false
	}
}

// replayByteRange opens path, seeks to rng.Start, and pipes the byte range to
// parse. Used by both backend replayers so the index replay reads only the
// page's byte union, not the whole transcript.
func replayByteRange(path string, rng transcriptindex.ByteRange, parse func(io.Reader) ([]core.RichHistoryEntry, error)) ([]core.RichHistoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if rng.Start > 0 {
		if _, err := f.Seek(rng.Start, io.SeekStart); err != nil {
			return nil, err
		}
	}
	limit := rng.End - rng.Start
	if limit <= 0 {
		return nil, nil
	}
	return parse(io.LimitReader(f, limit))
}

func replayCodexRange(path string, rng transcriptindex.ByteRange) ([]core.RichHistoryEntry, error) {
	return replayByteRange(path, rng, func(r io.Reader) ([]core.RichHistoryEntry, error) {
		return codex.ParseRichHistoryFromReader(r, 0)
	})
}

func replayClaudeRange(path string, rng transcriptindex.ByteRange) ([]core.RichHistoryEntry, error) {
	return replayByteRange(path, rng, func(r io.Reader) ([]core.RichHistoryEntry, error) {
		return claudecode.LoadClaudeRichHistoryFromReader(r, path)
	})
}

func transcriptReplayer(backend transcriptindex.Backend) transcriptindex.ContentReplayer {
	switch backend {
	case transcriptindex.BackendCodex:
		return replayCodexRange
	case transcriptindex.BackendClaude:
		return replayClaudeRange
	default:
		return nil
	}
}

// pageLimit clamps the requested page size to the allowed range.
func pageLimit(requested int) int {
	if requested <= 0 {
		return defaultPageSize
	}
	if requested > maxPageSize {
		return maxPageSize
	}
	return requested
}

// servePaginatedMessages attempts the transcript-index pagination path for
// get_session_messages. handled reports whether this call produced an outcome
// the caller must act on:
//   - handled=true, result!=nil, err=nil: send result.
//   - handled=true, err!=nil: send a cursor_stale error (do not fall back).
//   - handled=false: pagination does not apply; caller falls back to full-parse.
func (h *Handlers) servePaginatedMessages(
	ctx context.Context,
	agent core.Agent,
	backendID string,
	params GetSessionMessagesParams,
) (result map[string]interface{}, err error, handled bool) {
	if !params.Paginate {
		return nil, nil, false
	}
	loc, ok := agent.(core.TranscriptLocator)
	if !ok {
		return nil, nil, false
	}
	h.mu.Lock()
	store := h.transcriptIndex
	h.mu.Unlock()
	if store == nil {
		return nil, nil, false
	}
	backend, ok := transcriptBackend(backendID)
	if !ok {
		return nil, nil, false
	}
	replayer := transcriptReplayer(backend)
	if replayer == nil {
		return nil, nil, false
	}

	path, err := loc.TranscriptPath(ctx, params.SessionID)
	if err != nil {
		// Cannot resolve transcript file; fall back to the agent's own history.
		return nil, nil, false
	}

	idx, err := store.Ensure(ctx, transcriptindex.IndexRequest{Backend: backend, FilePath: path})
	if err != nil {
		// Index build failed (e.g. budget exceeded). Fall back to full-parse
		// rather than failing the whole request.
		slog.Info("go-bridge: transcript index unavailable, falling back", "backendID", backendID, "sessionID", params.SessionID, "error", err)
		return nil, nil, false
	}

	limit := pageLimit(params.Limit)

	// Resolve the page spans. A backward cursor pins the exclusive upper bound;
	// no cursor means the newest page.
	var pageSpans []transcriptindex.LogicalMessageSpan
	if params.BeforeCursor == "" {
		pageSpans = transcriptindex.LastN(idx.Spans, limit)
	} else {
		cursor, cerr := transcriptindex.DecodeMessageCursor(params.BeforeCursor, params.SessionID)
		if cerr != nil {
			return nil, fmt.Errorf("cursor_stale: %v", cerr), true
		}
		if idx.IsCursorStale(cursor) {
			return nil, fmt.Errorf("cursor_stale: indexed prefix changed"), true
		}
		pageSpans = transcriptindex.PageBefore(idx.Spans, cursor.Ordinal, limit)
	}
	if len(pageSpans) == 0 {
		// No messages in range: return an empty page with current cursors.
		return paginatedEnvelope(nil, params.SessionID, idx, 0), nil, true
	}

	entries, err := idx.Replay(pageSpans, replayer)
	if err != nil {
		// Replay mismatch (span extractor vs. content builder disagreement) or
		// read failure: fall back to full-parse for correctness.
		slog.Info("go-bridge: transcript replay fell back", "backendID", backendID, "sessionID", params.SessionID, "error", err)
		return nil, nil, false
	}

	// Wire-encode, then enforce the per-page byte budget by dropping the oldest
	// messages (lowest ordinal) until under budget. The dropped messages are
	// fetched on the next backward page, so nothing is lost.
	wire := make([]map[string]interface{}, 0, len(entries))
	for i, e := range entries {
		message := h.richHistoryEntryToWire(e)
		if id, _ := message["id"].(string); strings.TrimSpace(id) == "" {
			message["id"] = fmt.Sprintf(
				"%s:%d:%s:%d",
				params.SessionID,
				pageSpans[i].Ordinal,
				e.Role,
				e.Timestamp.UnixMilli(),
			)
		}
		wire = append(wire, message)
	}
	if total, _ := json.Marshal(wire); len(total) > maxPageResponseBytes {
		for _, message := range wire {
			compactDuplicateMessageFields(message)
		}
	}
	for len(wire) > 1 {
		total, _ := json.Marshal(wire)
		if len(total) <= maxPageResponseBytes {
			break
		}
		wire = wire[1:]
	}

	kept := len(wire)
	dropped := len(entries) - kept
	oldestOrdinal := pageSpans[dropped].Ordinal // entries/pageSpans are ascending
	return paginatedEnvelope(wire, params.SessionID, idx, oldestOrdinal), nil, true
}

// compactDuplicateMessageFields removes top-level text copies only when the
// iOS mapper can reconstruct them exactly from parts. Rich history carries both
// representations for compatibility, but sending both can double a large
// assistant message and defeat the page byte budget.
func compactDuplicateMessageFields(message map[string]interface{}) {
	parts, ok := message["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		return
	}
	texts := make([]string, 0, len(parts))
	reasoning := make([]string, 0, len(parts))
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		content, _ := part["content"].(string)
		if content == "" {
			continue
		}
		switch part["type"] {
		case "text":
			texts = append(texts, content)
		case "reasoning":
			reasoning = append(reasoning, content)
		}
	}
	if content, _ := message["content"].(string); content != "" && content == strings.Join(texts, "\n") {
		delete(message, "content")
	}
	if thinking, _ := message["thinking"].(string); thinking != "" && thinking == strings.Join(reasoning, "\n") {
		delete(message, "thinking")
	}
	steps, ok := message["steps"].([]interface{})
	if !ok || len(steps) == 0 {
		return
	}
	partSteps := make([]interface{}, 0, len(steps))
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok || part["type"] != "tool" {
			continue
		}
		if step, ok := part["step"]; ok {
			partSteps = append(partSteps, step)
		}
	}
	if reflect.DeepEqual(steps, partSteps) {
		delete(message, "steps")
	}
}

// paginatedEnvelope builds the wire response with page cursors. oldestOrdinal is
// the lowest ordinal actually present in the returned page (after byte-budget
// trimming); it becomes the client's next beforeCursor. When messages is empty,
// oldestOrdinal is ignored and no oldestCursor is emitted.
func paginatedEnvelope(messages []map[string]interface{}, sessionID string, idx *transcriptindex.PageIndex, oldestOrdinal int64) map[string]interface{} {
	out := map[string]interface{}{"messages": messages}
	if len(messages) == 0 {
		out["hasMore"] = false
		return out
	}
	oldestCursor, _ := transcriptindex.EncodeMessageCursor(transcriptindex.MessageCursor{
		SessionID: sessionID, Ordinal: oldestOrdinal, Generation: idx.PrefixGeneration,
	})
	// newestCursor is the highest ordinal present; informational for client merge.
	newestOrdinal := oldestOrdinal + int64(len(messages)) - 1
	newestCursor, _ := transcriptindex.EncodeMessageCursor(transcriptindex.MessageCursor{
		SessionID: sessionID, Ordinal: newestOrdinal, Generation: idx.PrefixGeneration,
	})
	out["oldestCursor"] = oldestCursor
	out["newestCursor"] = newestCursor
	out["hasMore"] = oldestOrdinal > 0
	return out
}

// listCursor is an opaque, versioned cursor into a backend's sorted session
// list. Both file-backed backends expose updatedAtMillis (DESC) and a unique
// session id (ASC tie-breaker), so one cursor shape serves both (design §6.6).
// The cursor is never just a timestamp.
type listCursor struct {
	Version         int    `json:"v"`
	UpdatedAtMillis int64  `json:"ts"`
	SessionID       string `json:"sid"`
}

const listCursorVersion = 1

func encodeListCursor(c listCursor) (string, error) {
	c.Version = listCursorVersion
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeListCursor(s string) (listCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return listCursor{}, fmt.Errorf("malformed list cursor")
	}
	var c listCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return listCursor{}, fmt.Errorf("malformed list cursor")
	}
	if c.Version != listCursorVersion {
		return listCursor{}, fmt.Errorf("unsupported list cursor version %d", c.Version)
	}
	return c, nil
}

// paginateSessionList slices an already-sorted (updatedAtMillis DESC, id ASC)
// session list by an optional cursor and limit. It always emits nextCursor and
// hasMore so new clients can page while old clients ignore the extra fields
// (design §6.6, §6.7). A malformed cursor degrades to the first page rather than
// failing the whole list, because the list is cheap to re-fetch.
func paginateSessionList(sessions []map[string]interface{}, cursorStr string, limit int) map[string]interface{} {
	if cursorStr != "" {
		if cursor, err := decodeListCursor(cursorStr); err == nil {
			filtered := make([]map[string]interface{}, 0, len(sessions))
			for _, s := range sessions {
				ts, _ := s["updatedAtMillis"].(int64)
				id, _ := s["id"].(string)
				if ts < cursor.UpdatedAtMillis || (ts == cursor.UpdatedAtMillis && id > cursor.SessionID) {
					filtered = append(filtered, s)
				}
			}
			sessions = filtered
		}
		// malformed cursor: fall through to first page
	}

	hasMore := false
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
		hasMore = true
	}

	result := map[string]interface{}{"sessions": sessions, "hasMore": hasMore}
	if hasMore && len(sessions) > 0 {
		last := sessions[len(sessions)-1]
		ts, _ := last["updatedAtMillis"].(int64)
		id, _ := last["id"].(string)
		if next, err := encodeListCursor(listCursor{UpdatedAtMillis: ts, SessionID: id}); err == nil {
			result["nextCursor"] = next
		}
	}
	return result
}

// extractStringParam reads an optional string field from msg.Params.
func extractStringParam(msg WireMessage, key string) string {
	if len(msg.Params) == 0 {
		return ""
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return ""
	}
	var value string
	if err := json.Unmarshal(params[key], &value); err != nil {
		return ""
	}
	return value
}
