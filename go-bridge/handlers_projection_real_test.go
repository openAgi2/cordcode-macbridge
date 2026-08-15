//go:build realdata

// Package gobridge (realdata build tag): 监工指令 5 号取证 — against the owner's REAL
// on-disk transcripts (codex ~/.codex/sessions, claude ~/.claude/projects) through the REAL
// production Kernel hydrate pipeline and the
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
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex"
	"github.com/openAgi2/cordcode-macbridge/agent/opencode"
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

func largestJSONLsUnder(t *testing.T, root string, limit int) []string {
	t.Helper()
	type entry struct {
		path string
		size int64
	}
	var files []entry
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && info.Size() > 0 &&
			strings.HasSuffix(path, ".jsonl") {
			files = append(files, entry{path: path, size: info.Size()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].size > files[j].size })
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	result := make([]string, len(files))
	for index := range files {
		result[index] = files[index].path
	}
	return result
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

// TestRealColdPullCodexLargeSession verifies a real owner Codex large session reaches one full
// committed baseline within the 15s product anchor.
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
		t.Fatalf("real codex cold pull FAILED (expected full committed baseline within 15s): code=%s msg=%s — %s",
			conn.err.Code, conn.err.Message, elapsed)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("real codex: served data not a map (empty shell?): %T — %s", conn.data, elapsed)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
		t.Fatalf("real codex: empty head-0 snapshot (forbidden): %+v — %s", dataMap["projection"], elapsed)
	}
	if elapsed >= defaultColdHydrateTimeout {
		t.Fatalf("real codex large session: committed baseline served after %s, must be within %s",
			elapsed, defaultColdHydrateTimeout)
	}
	t.Logf("REAL CODEX cold pull: full baseline in %s, turns=%d syncRev=%d (within 15s budget)",
		elapsed, len(proj.Turns), proj.SyncRev)
	// Drain the background hydrate so the segment-hook global is not contaminated.
	waitForColdHydrateDrained(t, handlers, "codex", sid, 30*time.Second)
}

// TestRealProjectionShadowParityCodexLargeSession compares the production projection hydrate
// with the existing canonical Codex rich-history parser without printing transcript content,
// paths, or session identifiers. K3 shadow admission requires semantic parity before active
// timeline ownership can be considered.
func TestRealProjectionShadowParityCodexLargeSession(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rollout := largestJSONLUnder(t, filepath.Join(home, ".codex", "sessions"))
	if rollout == "" {
		t.Skip("no real Codex rollout available")
	}
	source, err := os.Open(rollout)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := codex.ParseRichHistoryFromReader(source, 0)
	_ = source.Close()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := codexSessionIDFromRollout(rollout)
	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: rollout})
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": 0})
	handlers.handleGetSessionProjection(conn, WireMessage{
		RequestID: "real-shadow-parity",
		BackendID: "codex",
		Method:    "get_session_projection",
		Params:    params,
	}, nil)
	if conn.err != nil {
		t.Fatalf("projection hydrate failed: code=%s", conn.err.Code)
	}
	waitForColdHydrateDrained(t, handlers, "codex", sessionID, 30*time.Second)
	projection, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("codex", sessionID)
	if !ok {
		t.Fatal("projection hydrate committed no snapshot")
	}

	type comparableMessage struct {
		role       string
		content    string
		thinking   string
		tools      []string
		isComplete bool
	}
	projected := make([]comparableMessage, 0, len(projection.Turns)*2)
	for _, turn := range projection.Turns {
		appendMessage := func(message *MessageProjection, complete bool) {
			if message == nil {
				return
			}
			if message.Role == "assistant" && len(message.Parts) == 0 {
				return
			}
			item := comparableMessage{role: message.Role, isComplete: complete}
			var content, finalContent, thinking []string
			for _, part := range message.Parts {
				switch part.Type {
				case "text":
					content = append(content, part.Text)
					if part.Presentation == "final" {
						finalContent = append(finalContent, part.Text)
					}
				case "reasoning":
					thinking = append(thinking, part.Text)
				case "tool":
					item.tools = append(item.tools, part.ToolName+"\x00"+part.ToolStatus)
				}
			}
			if len(finalContent) > 0 {
				item.content = strings.Join(finalContent, "\n")
			} else {
				item.content = strings.Join(content, "\n")
			}
			item.thinking = strings.Join(thinking, "\n")
			projected = append(projected, item)
		}
		appendMessage(turn.User, true)
		appendMessage(turn.Assistant, turn.Status == "completed" || turn.Status == "aborted" || turn.Status == "error")
	}
	canonical := make([]comparableMessage, 0, len(legacy))
	for _, entry := range legacy {
		item := comparableMessage{
			role:       entry.Role,
			content:    entry.Content,
			thinking:   entry.Thinking,
			isComplete: entry.Role != "assistant" || entry.TurnCompletedAt != nil,
		}
		for _, step := range entry.Steps {
			name, _ := step["toolName"].(string)
			status, _ := step["status"].(string)
			item.tools = append(item.tools, name+"\x00"+status)
		}
		canonical = append(canonical, item)
	}

	roleMismatch, contentMismatch, thinkingMismatch := 0, 0, 0
	toolMismatch, completionMismatch := 0, 0
	projectedUsers, canonicalUsers := 0, 0
	for _, message := range projected {
		if message.role == "user" {
			projectedUsers++
		}
	}
	for _, message := range canonical {
		if message.role == "user" {
			canonicalUsers++
		}
	}
	loggedMismatch := 0
	for index := 0; index < len(projected) && index < len(canonical); index++ {
		if projected[index].role != canonical[index].role {
			roleMismatch++
		}
		if projected[index].content != canonical[index].content {
			contentMismatch++
		}
		if projected[index].thinking != canonical[index].thinking {
			thinkingMismatch++
		}
		if !reflect.DeepEqual(projected[index].tools, canonical[index].tools) {
			toolMismatch++
		}
		if projected[index].isComplete != canonical[index].isComplete {
			completionMismatch++
		}
		if loggedMismatch < 12 && !reflect.DeepEqual(projected[index], canonical[index]) {
			t.Logf(
				"shadow parity shape[%d]: projected(role=%s content=%d thinking=%d tools=%d complete=%t) canonical(role=%s content=%d thinking=%d tools=%d complete=%t)",
				index,
				projected[index].role, len(projected[index].content), len(projected[index].thinking),
				len(projected[index].tools), projected[index].isComplete,
				canonical[index].role, len(canonical[index].content), len(canonical[index].thinking),
				len(canonical[index].tools), canonical[index].isComplete,
			)
			loggedMismatch++
		}
		if !reflect.DeepEqual(projected[index].tools, canonical[index].tools) {
			firstToolMismatch := 0
			for firstToolMismatch < len(projected[index].tools) &&
				firstToolMismatch < len(canonical[index].tools) &&
				projected[index].tools[firstToolMismatch] == canonical[index].tools[firstToolMismatch] {
				firstToolMismatch++
			}
			projectedTool := "<end>"
			canonicalTool := "<end>"
			if firstToolMismatch < len(projected[index].tools) {
				projectedTool = projected[index].tools[firstToolMismatch]
			}
			if firstToolMismatch < len(canonical[index].tools) {
				canonicalTool = canonical[index].tools[firstToolMismatch]
			}
			t.Logf(
				"shadow parity tool-shape[%d]: first=%d projected=%q canonical=%q",
				index, firstToolMismatch, projectedTool, canonicalTool,
			)
		}
	}
	t.Logf(
		"shadow parity aggregate: projected=%d(users=%d) canonical=%d(users=%d) role=%d content=%d thinking=%d tools=%d completion=%d",
		len(projected), projectedUsers, len(canonical), canonicalUsers, roleMismatch, contentMismatch, thinkingMismatch,
		toolMismatch, completionMismatch,
	)
	if len(projected) != len(canonical) || roleMismatch+contentMismatch+thinkingMismatch+toolMismatch+completionMismatch != 0 {
		t.Fatalf("projection differs from canonical transcript semantics")
	}
}

// TestRealColdPullCodexSLODistribution measures no-checkpoint cold hydrate across the owner's
// largest persisted Codex sessions. It reports aggregate size/turn/timing only: no path, content,
// or session identity is emitted.
func TestRealColdPullCodexSLODistribution(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rollouts := largestJSONLsUnder(t, filepath.Join(home, ".codex", "sessions"), 20)
	if len(rollouts) < 5 {
		t.Skipf("need at least five persisted Codex sessions, found %d", len(rollouts))
	}

	var durations []time.Duration
	var minBytes, maxBytes int64
	minTurns, maxTurns := int(^uint(0)>>1), 0
	for _, rollout := range rollouts {
		info, statErr := os.Stat(rollout)
		if statErr != nil {
			t.Fatal(statErr)
		}
		sessionID := codexSessionIDFromRollout(rollout)
		handlers := NewHandlers()
		handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: rollout})
		conn := &readFileCaptureConn{}
		params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": 0})
		started := time.Now()
		handlers.handleGetSessionProjection(conn, WireMessage{
			RequestID: "real-slo-sample",
			BackendID: "codex",
			Method:    "get_session_projection",
			Params:    params,
		}, nil)
		elapsed := time.Since(started)
		if conn.err != nil {
			t.Fatalf("no-checkpoint sample failed: code=%s retryable=%v", conn.err.Code, conn.err.Retryable)
		}
		waitForColdHydrateDrained(t, handlers, "codex", sessionID, 30*time.Second)
		projection, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("codex", sessionID)
		if !ok {
			t.Fatal("no-checkpoint sample committed no projection")
		}
		durations = append(durations, elapsed)
		if minBytes == 0 || info.Size() < minBytes {
			minBytes = info.Size()
		}
		if info.Size() > maxBytes {
			maxBytes = info.Size()
		}
		if len(projection.Turns) < minTurns {
			minTurns = len(projection.Turns)
		}
		if len(projection.Turns) > maxTurns {
			maxTurns = len(projection.Turns)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	nearestRank := func(percentile float64) time.Duration {
		index := int(float64(len(durations))*percentile+0.999999) - 1
		if index < 0 {
			index = 0
		}
		if index >= len(durations) {
			index = len(durations) - 1
		}
		return durations[index]
	}
	p50, p95, maximum := nearestRank(0.50), nearestRank(0.95), durations[len(durations)-1]
	t.Logf(
		"real no-checkpoint SLO: samples=%d sourceBytes=[%d,%d] turns=[%d,%d] p50=%s p95=%s max=%s",
		len(durations), minBytes, maxBytes, minTurns, maxTurns, p50, p95, maximum,
	)
	if p50 > projectionColdOpenP50SLO || p95 > projectionColdOpenP95SLO ||
		maximum > projectionColdOpenMaximumSLO {
		t.Fatalf(
			"no-checkpoint cold-open exceeds F2: p50=%s/%s p95=%s/%s max=%s/%s",
			p50, projectionColdOpenP50SLO, p95, projectionColdOpenP95SLO,
			maximum, projectionColdOpenMaximumSLO,
		)
	}
}

// TestRealProjectionCheckpointRestart reduces an owner Codex rollout through the production
// parser, persists the committed projection, and proves a fresh Kernel restores the identical
// head. A copied source is then appended after the durable cursor to prove append-only growth
// preserves admission without moving the persisted cut.
func TestRealProjectionCheckpointRestart(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rollout := largestJSONLUnder(t, filepath.Join(home, ".codex", "sessions"))
	if rollout == "" {
		t.Skip("no real Codex rollout available")
	}
	sessionID := codexSessionIDFromRollout(rollout)
	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: rollout})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{
		RequestID: "real-checkpoint-restart",
		BackendID: "codex",
		Method:    "get_session_projection",
		Params:    params,
	}
	hydrateStarted := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	if conn.err != nil {
		t.Fatalf("real hydrate failed: code=%s message=%s", conn.err.Code, conn.err.Message)
	}
	waitForColdHydrateDrained(t, handlers, "codex", sessionID, 30*time.Second)
	hydrateElapsed := time.Since(hydrateStarted)
	projection, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("codex", sessionID)
	if !ok || projection.SyncRev == 0 {
		t.Fatalf("real hydrate produced no committed projection: ok=%v rev=%d", ok, projection.SyncRev)
	}
	info, err := os.Stat(rollout)
	if err != nil {
		t.Fatal(err)
	}
	source := ProjectionSourceDescriptor{
		Identity: sessionID,
		Path:     rollout,
		Cursor:   info.Size(),
	}
	sourceCheckpoint, err := BuildProjectionSourceCheckpoint(source)
	if err != nil {
		t.Fatal(err)
	}
	store := NewProjectionCheckpointStore(t.TempDir())
	checkpoint := NewReadyProjectionCheckpoint(
		"codex", sessionID, sourceCheckpoint, projection, time.Now(),
	)
	saveStarted := time.Now()
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	saveElapsed := time.Since(saveStarted)

	restarted := NewProjectionKernel(nil, store)
	restoreStarted := time.Now()
	restored, err := restarted.RestoreCheckpoint("codex", sessionID, source)
	if err != nil {
		t.Fatal(err)
	}
	restoreElapsed := time.Since(restoreStarted)
	if !reflect.DeepEqual(restored.Projection, projection) {
		t.Fatal("restart projection differs from committed canonical projection")
	}

	appendedPath := filepath.Join(t.TempDir(), "appended-rollout.jsonl")
	src, err := os.Open(rollout)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.OpenFile(appendedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = src.Close()
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = src.Close()
		_ = dst.Close()
		t.Fatal(err)
	}
	_ = src.Close()
	if _, err := dst.WriteString("\n"); err != nil {
		_ = dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	appendedSource := source
	appendedSource.Path = appendedPath
	afterAppend := NewProjectionKernel(nil, store)
	appendedRestore, err := afterAppend.RestoreCheckpoint("codex", sessionID, appendedSource)
	if err != nil {
		t.Fatalf("append-only source invalidated durable prefix: %v", err)
	}
	if appendedRestore.Source.Cursor != source.Cursor ||
		!reflect.DeepEqual(appendedRestore.Projection, projection) {
		t.Fatal("append admission changed the durable cursor or projection")
	}

	checkpointPath, err := store.checkpointPath("codex", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	checkpointInfo, err := os.Stat(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"real checkpoint: sourceBytes=%d turns=%d rev=%d checkpointBytes=%d hydrate=%s save=%s restore=%s recoveryWindowBytes=%d",
		info.Size(),
		len(projection.Turns),
		projection.SyncRev,
		checkpointInfo.Size(),
		hydrateElapsed,
		saveElapsed,
		restoreElapsed,
		int64(1),
	)
}

// TestRealColdPullClaudeNonEmpty verifies a real owner Claude session commits a non-empty full
// baseline through the range-bounded Kernel parser.
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

// TestRealColdPullOpencodeWithoutAgentIsSourceUnavailable: OpenCode is migrated, but with no
// registered agent/history provider the pull must fail honestly (source unavailable / not empty shell).
func TestRealColdPullOpencodeWithoutAgentIsSourceUnavailable(t *testing.T) {
	handlers := NewHandlers() // no opencode agent registered
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "any-opencode-sid", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-real-oc", BackendID: "opencode", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)
	if conn.err == nil {
		t.Fatalf("real opencode: expected source failure, got success data=%T", conn.data)
	}
	if conn.data != nil {
		t.Fatalf("real opencode: error must not pair with data: %T", conn.data)
	}
	if conn.err.Code == "projection.not_migrated" {
		t.Fatalf("opencode must be migrated; got not_migrated")
	}
	t.Logf("REAL OPENCODE cold pull without agent: honest failure ✅ — code=%s msg=%s", conn.err.Code, conn.err.Message)
}

// TestRealColdPullOpencodeTrailingUnansweredTurnCommits drives the REAL
// production cold-pull path against the REAL managed opencode server for a
// session whose rich history ends with an unanswered user turn (the 2026-08-14
// empty-turn incident left exactly this shape; pre-fix the hydrate commit gate
// blocked forever → iOS endless "loading"). Post-fix the adapter seals the dead
// turn as turn_error once the server confirms the session is idle, and the
// cold pull must commit a full baseline within the normal budget.
//
// Env-gated (owner machine data; nothing baked into the repo):
//
//	CC_REAL_OPENCODE_SESSION — target ses_… id
//	CC_REAL_OPENCODE_URL / _USER / _PASS — managed server endpoint + basic auth
func TestRealColdPullOpencodeTrailingUnansweredTurnCommits(t *testing.T) {
	sessionID := strings.TrimSpace(os.Getenv("CC_REAL_OPENCODE_SESSION"))
	baseURL := strings.TrimSpace(os.Getenv("CC_REAL_OPENCODE_URL"))
	if sessionID == "" || baseURL == "" {
		t.Skip("CC_REAL_OPENCODE_SESSION / CC_REAL_OPENCODE_URL not set")
	}
	agent, err := opencode.New(map[string]any{
		"cmd":           "opencode",
		"work_dir":      ".",
		"opencode_url":  baseURL,
		"opencode_user": os.Getenv("CC_REAL_OPENCODE_USER"),
		"opencode_pass": os.Getenv("CC_REAL_OPENCODE_PASS"),
	})
	if err != nil {
		t.Skipf("opencode agent unavailable on this machine: %v", err)
	}

	handlers := NewHandlers()
	handlers.RegisterAgent("opencode", agent)

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-real-oc-hang", BackendID: "opencode", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if conn.err != nil {
		t.Fatalf("REAL OPENCODE trailing-unanswered cold pull FAILED (pre-fix symptom: endless hydrating): code=%s msg=%s — %s",
			conn.err.Code, conn.err.Message, elapsed)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("real opencode: served data not a map: %T — %s", conn.data, elapsed)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
		t.Fatalf("real opencode: empty snapshot (forbidden): %+v — %s", dataMap["projection"], elapsed)
	}
	if elapsed >= defaultColdHydrateTimeout {
		t.Fatalf("real opencode: committed baseline after %s, must be within %s (pre-fix: never)",
			elapsed, defaultColdHydrateTimeout)
	}
	// The trailing unanswered turn must now carry a terminal status.
	last := proj.Turns[len(proj.Turns)-1]
	if last.Status != "completed" && last.Status != "aborted" && last.Status != "error" {
		t.Fatalf("trailing turn still non-terminal (%q) — gate would block again", last.Status)
	}
	t.Logf("REAL OPENCODE trailing-unanswered cold pull: full baseline in %s, turns=%d syncRev=%d, trailing status=%s",
		elapsed, len(proj.Turns), proj.SyncRev, last.Status)
	waitForColdHydrateDrained(t, handlers, "opencode", sessionID, 30*time.Second)
}
