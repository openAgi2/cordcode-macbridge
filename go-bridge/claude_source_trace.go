package gobridge

// IR-7 read-only source-row correlation instrumentation.
//
// This file adds STRUCTURED DIAGNOSTIC LOGGING ONLY. It changes no timeline behavior,
// emits no projection events, and writes no projection/kernel state. Its sole purpose is
// to let an investigator reconstruct, from go-bridge.log, the physical byte range and
// identity of every Claude transcript record ingested by BOTH ingestion paths, so the
// hydrate/live overlap (H1) and replay/branch duplication (H3/H4) can be correlated
// against the projection without guessing.
//
// It is gated by GO_BRIDGE_CLAUDE_SOURCE_TRACE=1 (default off → zero log volume). Enable
// during incident reproduction, then read the two log lines this emits:
//
//   go-bridge: claude_source_trace  phase=hydrate|live  segment=<file>  byteStart byteEnd
//                                     uuid type role fileOrderTurn events
//   go-bridge: claude_source_hydrate_window  backendID sessionPrefix segment byteStart byteEnd
//
// Join claude_source_trace on (segment) with claude_source_hydrate_window to see which
// records fell inside the baseline byte window, and with projection_shadow hydrate_commit
// startCut (sessionPrefix) to see the admission cut. This closes the round-11 IR-7
// highest-value gap: "Claude relay physical [byteStart,byteEnd)" was previously unlogged.
//
// What this change does NOT deliver (kept honest, separately tracked): sourceStateRev
// (blocked on IR-2/IR-3 Go impl), per-record kernel ingest-domain (baseline/pending/
// catch-up/live) and transition-result logging inside the reducer, and the two-apply
// logical-UUID/turn/part identity correlation. See
// docs/2026-07-30-remote-web-…-investigation.md IR-7.

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// claudeSourceTraceEnabled gates the IR-7 source-row trace. It is a function variable
// (mirroring transcriptStateProbe) so tests can flip it without touching the process env.
var claudeSourceTraceEnabled = func() bool {
	return os.Getenv("GO_BRIDGE_CLAUDE_SOURCE_TRACE") == "1"
}

// claudeRelayScannedEntry pairs a parsed transcript entry with the absolute byte range it
// occupied in the source .jsonl ([byteStart,byteEnd) covers the record content; the LF
// terminator sits at byteEnd).
type claudeRelayScannedEntry struct {
	entry              claudeTranscriptRelayEntry
	byteStart, byteEnd int64
}

// scanClaudeRelayEntriesWithOffsets is the byte-range-aware core scanner. base is the
// absolute file offset at which the reader begins (0 for a full-file read, or the live
// seek offset for growth). It applies the IDENTICAL resume-meta / no-response /
// internal-compact / compaction-boundary / user+assistant filtering as
// scanClaudeRelayEntriesFromReader, so delegating that function here preserves behavior.
func scanClaudeRelayEntriesWithOffsets(r io.Reader, base int64) ([]claudeRelayScannedEntry, error) {
	skipNextResumeNoResponse := false
	var out []claudeRelayScannedEntry
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16)
	pos := base
	for scanner.Scan() {
		line := scanner.Bytes()
		lineStart := pos
		lineEnd := pos + int64(len(line)) // record content; LF terminator at lineEnd
		pos = lineEnd + 1                 // advance past LF for the next record
		if len(line) == 0 {
			continue
		}
		var entry claudeTranscriptRelayEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if isClaudeCompactionBoundaryRelayEntry(entry) {
			out = append(out, claudeRelayScannedEntry{entry: entry, byteStart: lineStart, byteEnd: lineEnd})
			continue
		}
		if isClaudeInternalCompactRelayEntry(entry) || entry.Message == nil {
			continue
		}
		if isClaudeResumeMetaRelayEntry(entry) {
			skipNextResumeNoResponse = true
			continue
		}
		if skipNextResumeNoResponse {
			if isClaudeResumeNoResponseRelayEntry(entry) {
				skipNextResumeNoResponse = false
				continue
			}
			skipNextResumeNoResponse = false
		}
		if entry.Type == "user" || entry.Type == "assistant" {
			out = append(out, claudeRelayScannedEntry{entry: entry, byteStart: lineStart, byteEnd: lineEnd})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// emitClaudeSourceTrace logs one structured source-row record. No-op unless the trace flag
// is set. phase is "hydrate" (cold baseline streamer) or "live" (file-relay growth loop).
func emitClaudeSourceTrace(segment, phase string, byteStart, byteEnd int64, e claudeTranscriptRelayEntry, fileOrderTurn string, eventCount int) {
	if !claudeSourceTraceEnabled() {
		return
	}
	role := ""
	if e.Message != nil {
		role = e.Message.Role
	}
	slog.Info("go-bridge: claude_source_trace",
		"phase", phase,
		"segment", segment,
		"byteStart", byteStart,
		"byteEnd", byteEnd,
		"uuid", e.UUID,
		"type", e.Type,
		"role", role,
		"fileOrderTurn", fileOrderTurn,
		"events", eventCount,
	)
}

// emitClaudeSourceHydrateWindow logs the byte window streamed as the cold-hydrate baseline,
// so per-record claude_source_trace lines can be partitioned into baseline vs post-cut on
// the same segment. No-op unless the trace flag is set.
func emitClaudeSourceHydrateWindow(backendID, sessionID, path string, startOffset, endOffset int64) {
	if !claudeSourceTraceEnabled() {
		return
	}
	slog.Info("go-bridge: claude_source_hydrate_window",
		"backendID", backendID,
		"sessionPrefix", projectionSessionLogPrefix(sessionID),
		"segment", filepath.Base(path),
		"byteStart", startOffset,
		"byteEnd", endOffset,
	)
}
