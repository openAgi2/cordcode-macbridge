//go:build realdata

// Package gobridge (realdata build tag): 监工指令 5 号取证 — against the owner's REAL
// on-disk transcripts (codex ~/.codex/sessions, claude ~/.claude/projects) through the REAL
// production hydrate pipeline (produceCodexHydrateEvents / produceClaudeHydrateEvents) and the
// REAL handleGetSessionProjection dispatch. Not run by default `go test` (needs the owner's
// machine data); invoke with `-tags realdata`.
//
// The agent layer is a TranscriptLocator stub (fakeAgent) pointed at the real transcript path —
// the hydrate code, the reducer, and the transcript DATA are all real production. This is not a
// mock: it verifies the exact cold-pull path iOS would exercise against real owner sessions
// without requiring an iOS UI tap (owner declined real-device UI regression per v3 Core Policy).
package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// largestJSONLUnder walks root and returns the path of the largest *.jsonl file (proxy for a
// real owner "大 session"). Returns "" if none.
func largestJSONLUnder(t *testing.T, root string) string {
	t.Helper()
	type entry struct {
		path string
		size int64
	}
	var files []entry
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(p, ".jsonl") {
			files = append(files, entry{p, info.Size()})
		}
		return nil
	})
	if len(files) == 0 {
		return ""
	}
	sort.Slice(files, func(i, j int) bool { return files[i].size > files[j].size })
	return files[0].path
}

// codexSessionIDFromRollout extracts the trailing uuid from a rollout filename
// (rollout-<timestamp>-<uuid>.jsonl). Falls back to the basename without extension.
func codexSessionIDFromRollout(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	// rollout-2026-07-22T17-17-36-<uuid>
	parts := strings.Split(base, "-")
	// uuid is 5 trailing groups joined by '-'
	if len(parts) >= 5 {
		uuid := strings.Join(parts[len(parts)-5:], "-")
		return uuid
	}
	return base
}

// TestRealColdPullCodexLargeSession: §10.5.7 修法 2 exit criterion — a real owner codex "大
// session" cold pull MUST return a non-empty partial within the 15s budget. Verifies the real
// produceCodexHydrateEvents pipeline against real owner data (the 1.85GB dataset).
func TestRealColdPullCodexLargeSession(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rollout := largestJSONLUnder(t, filepath.Join(home, ".codex", "sessions"))
	if rollout == "" {
		t.Skipf("no real codex rollout under %s/.codex/sessions", home)
	}
	sid := codexSessionIDFromRollout(rollout)
	stat, _ := os.Stat(rollout)
	t.Logf("real codex rollout: %s (sid=%s, %.1f MB)", rollout, sid, float64(stat.Size())/1e6)

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: rollout})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sid, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-real-codex", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if conn.err != nil {
		t.Fatalf("real codex cold pull FAILED (expected non-empty partial within 15s): code=%s msg=%s — %s",
			conn.err.Code, conn.err.Message, elapsed)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("real codex: served data not a map (empty shell?): %T — %s", conn.data, elapsed)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
		t.Fatalf("real codex: empty head-0 partial (forbidden §10.5.1): %+v — %s", dataMap["projection"], elapsed)
	}
	if elapsed >= defaultColdHydrateTimeout {
		t.Fatalf("real codex 大 session: partial served after %s, MUST be within %s (§10.5.7 修法 2)",
			elapsed, defaultColdHydrateTimeout)
	}
	t.Logf("REAL CODEX cold pull: partial in %s, turns=%d syncRev=%d (within 15s budget ✅)",
		elapsed, len(proj.Turns), proj.SyncRev)
	// Drain the background hydrate so the segment-hook global is not contaminated.
	waitForColdHydrateDrained(t, handlers, "codex", sid, 30*time.Second)
}

// TestRealColdPullClaudeNonEmpty: §10.5.7 修法 1 core verification — a real owner CLAUDE session
// cold pull MUST return a non-empty partial (previously .active showed全空白「还没有消息」). Verifies
// the real produceClaudeHydrateEvents parser + claudeEntryToProjectionEvents mapper against real
// claude .jsonl history.
func TestRealColdPullClaudeNonEmpty(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")
	rollout := largestJSONLUnder(t, projectsRoot)
	if rollout == "" {
		t.Skipf("no real claude transcript under %s/.claude/projects", home)
	}
	sid := strings.TrimSuffix(filepath.Base(rollout), ".jsonl")
	stat, _ := os.Stat(rollout)
	t.Logf("real claude transcript: %s (sid=%s, %.1f MB)", rollout, sid, float64(stat.Size())/1e6)

	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", transcriptPath: rollout})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sid, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-real-claude", BackendID: "claude", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if conn.err != nil {
		t.Fatalf("real claude cold pull FAILED (core §10.5.7 verification — previously全空白): code=%s msg=%s — %s",
			conn.err.Code, conn.err.Message, elapsed)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("real claude: served data not a map (empty shell?): %T — %s", conn.data, elapsed)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
		t.Fatalf("real claude: empty head-0 partial (§10.5.7.1 全空白 bug NOT fixed): %+v — %s", dataMap["projection"], elapsed)
	}
	// Verify real content (not a bare shell).
	hasContent := false
	for _, turn := range proj.Turns {
		if (turn.User != nil && len(turn.User.Parts) > 0) || (turn.Assistant != nil && len(turn.Assistant.Parts) > 0) {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatalf("real claude partial has no user/assistant content (bare shells): %+v", proj)
	}
	t.Logf("REAL CLAUDE cold pull: partial in %s, turns=%d syncRev=%d (非空 partial ✅ — 全空白 bug fixed)",
		elapsed, len(proj.Turns), proj.SyncRev)
	waitForColdHydrateDrained(t, handlers, "claude", sid, 30*time.Second)
}

// TestRealColdPullOpencodeNotMigrated: §10.5.7 修法 1 — opencode (HTTP/SQLite, not yet migrated
// to projection) MUST return projection.not_migrated, NEVER an empty head-0 shell. No agent
// registered (selectHydrateProducer returns nil for opencode).
func TestRealColdPullOpencodeNotMigrated(t *testing.T) {
	handlers := NewHandlers() // no opencode producer registered
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "any-opencode-sid", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-real-oc", BackendID: "opencode", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)
	if conn.err == nil {
		t.Fatalf("real opencode: expected projection.not_migrated, got success data=%T (empty shell?)", conn.data)
	}
	if conn.data != nil {
		t.Fatalf("real opencode: error must not pair with data (no empty shell): %T", conn.data)
	}
	if conn.err.Code != "projection.not_migrated" {
		t.Fatalf("real opencode: error code=%s msg=%s, want projection.not_migrated", conn.err.Code, conn.err.Message)
	}
	t.Logf("REAL OPENCODE cold pull: projection.not_migrated (honest, not empty head) ✅ — code=%s", conn.err.Code)
}
