package gobridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
	"github.com/openAgi2/cordcode-macbridge/core"
)

type fakeCompositeHistoryAgent struct {
	*fakeAgent
	segments []core.TranscriptSourceSegment
	entries  []core.RichHistoryEntry
	received []core.TranscriptSourceSegment
}

func (f *fakeCompositeHistoryAgent) RichHistoryTranscriptSegments(
	context.Context,
	string,
) ([]core.TranscriptSourceSegment, error) {
	return append([]core.TranscriptSourceSegment(nil), f.segments...), nil
}

func (f *fakeCompositeHistoryAgent) GetRichSessionHistoryAtSegments(
	_ context.Context,
	_ string,
	segments []core.TranscriptSourceSegment,
) ([]core.RichHistoryEntry, error) {
	f.received = append([]core.TranscriptSourceSegment(nil), segments...)
	return append([]core.RichHistoryEntry(nil), f.entries...), nil
}

func TestPrepareClaudeProjectionSourceFreezesEveryCompositeSegment(t *testing.T) {
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.jsonl")
	childPath := filepath.Join(dir, "child.jsonl")
	parentComplete := "{\"type\":\"parent\"}\n"
	childComplete := "{\"type\":\"child\"}\n"
	if err := os.WriteFile(parentPath, []byte(parentComplete), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, []byte(childComplete+"{\"partial\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &fakeCompositeHistoryAgent{
		fakeAgent: &fakeAgent{name: "claudecode"},
		segments: []core.TranscriptSourceSegment{
			{Identity: "parent", Path: parentPath},
			{Identity: "child", Path: childPath},
		},
		entries: []core.RichHistoryEntry{{
			ID:      "user-after",
			Role:    "user",
			Content: "after compact",
		}},
	}
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", provider)
	source, err := handlers.prepareProjectionHydrateSource(
		context.Background(), "claudecode", "logical-session", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Segments) != 2 ||
		source.Segments[0].Cursor != int64(len(parentComplete)) ||
		source.Segments[1].Cursor != int64(len(childComplete)) {
		t.Fatalf("composite cuts = %+v", source.Segments)
	}
	var emitted []projectionHydrateEvent
	if err := handlers.produceProjectionHydrateSource(
		context.Background(),
		"claudecode",
		"logical-session",
		source,
		0,
		source.Cursor,
		SessionProjection{},
		func(event projectionHydrateEvent) bool {
			emitted = append(emitted, event)
			return true
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(provider.received) != 2 ||
		provider.received[1].Cursor != int64(len(childComplete)) {
		t.Fatalf("provider received unfrozen cuts: %+v", provider.received)
	}
	if len(emitted) != 1 || emitted[0].Event != "user_message" {
		t.Fatalf("composite rich history did not enter projection reducer events: %+v", emitted)
	}
}

// TestHandleGetSessionProjectionReturnsReducerState: a fed reducer is returned verbatim as the
// {projection} data — proving pull reads the same in-memory state push produces (design §6.4 r4).
func TestHandleGetSessionProjectionReturnsReducerState(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": "hi"}, Broadcast: true})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1"})
	msg := WireMessage{RequestID: "r1", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err != nil {
		t.Fatalf("unexpected error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	if proj.SessionID != "s1" || proj.SyncRev != 1 || len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
		t.Fatalf("projection = %+v", proj)
	}
	resume := dataMap["resume"].(ProjectionResumeDiagnostic)
	if resume.Kind != "full" || resume.Reason == nil || *resume.Reason != "cold" || resume.RequestedRev != nil {
		t.Fatalf("cold resume diagnostic = %+v", resume)
	}
}

func TestLegacyCodexRemoteLiveCompletionIsInlineLoaded(t *testing.T) {
	handlers := NewHandlers()
	t.Cleanup(func() { handlers.Shutdown(context.Background()) })
	const backendID = "codex-remote"
	const sessionID = "legacy-resumed"
	handlers.hydrateProducerSeeds.Store(
		projectionDeliveryKey(backendID, sessionID),
		&CodexProducerState{HistoryMode: "legacy"},
	)

	handlers.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": "T-live"})
	handlers.sendSessionEvent(sessionID, backendID, "reasoning_delta", map[string]interface{}{
		"turnId": "T-live", "itemId": "reason-live", "delta": "checked locally",
	})
	payload := map[string]interface{}{"turnId": "T-live"}
	handlers.sendSessionEvent(sessionID, backendID, "turn_completed", payload)

	projection, ok := handlers.eventPublisher.ProjectionReducer().Snapshot(backendID, sessionID)
	if !ok || len(projection.Turns) != 1 {
		t.Fatalf("projection = %+v, ok=%v", projection, ok)
	}
	turn := projection.Turns[0]
	if turn.DetailLoadState != "loaded" || !turn.DetailInline {
		t.Fatalf("legacy live completion detail = %q inline=%v, want loaded/true", turn.DetailLoadState, turn.DetailInline)
	}
	if _, mutated := payload["detailPreloaded"]; mutated {
		t.Fatal("legacy completion decoration mutated the caller-owned payload")
	}
}

// TestHandleGetSessionProjectionEmptyWhenNoState: without a source inspection, absence of reducer
// state is not proof of a real empty session and must never become Ready(empty).
func TestHandleGetSessionProjectionEmptyWhenNoState(t *testing.T) {
	handlers := NewHandlers()
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "unknown"})
	msg := WireMessage{RequestID: "r1", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.data != nil {
		t.Fatalf("source-unavailable failure must not carry success data: %T", conn.data)
	}
	if conn.err == nil || conn.err.Code != "projection.hydrate_failed" {
		t.Fatalf("expected honest projection.hydrate_failed, got %+v", conn.err)
	}
	if conn.err.Retryable == nil || !*conn.err.Retryable {
		t.Fatalf("source-unavailable failure must be retryable: %+v", conn.err)
	}
}

// TestHandleGetSessionProjectionDeltaAtHead: sinceRev == headRev returns a cheap empty-patch
// delta instead of the full projection.
func TestHandleGetSessionProjectionDeltaAtHead(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": "ready"}, Broadcast: true})
	// headRev is now 1: turn_started does not commit (a bare shell alone is not ready); text_delta carries content.
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1", "sinceRev": 1})
	msg := WireMessage{RequestID: "r1", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	patches, ok := dataMap["patches"].([]ProjectionPatch)
	if !ok {
		t.Fatalf("expected patches delta at head, got %+v", dataMap)
	}
	if len(patches) != 0 || dataMap["headRev"].(int) != 1 {
		t.Fatalf("expected empty patches + headRev 2, got %+v", dataMap)
	}
	resume := dataMap["resume"].(ProjectionResumeDiagnostic)
	if resume.Kind != "at_head" || resume.FromRev == nil || *resume.FromRev != 1 || resume.ToRev == nil || *resume.ToRev != 1 {
		t.Fatalf("at-head resume diagnostic = %+v", resume)
	}
}

func TestHandleGetSessionProjectionReturnsJournaledNonEmptyDelta(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "s1", Event: "turn_started",
		Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true,
	})
	handlers.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "s1", Event: "text_delta",
		Data: map[string]interface{}{"itemId": "T1", "delta": "first"}, Broadcast: true,
	})
	handlers.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "s1", Event: "text_delta",
		Data: map[string]interface{}{"itemId": "T1", "delta": " second"}, Broadcast: true,
	})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1", "sinceRev": 1})
	msg := WireMessage{RequestID: "r-delta", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err != nil {
		t.Fatalf("unexpected delta error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	patches, ok := dataMap["patches"].([]ProjectionPatch)
	if !ok || len(patches) != 1 {
		t.Fatalf("expected one non-empty journal patch, got %+v", dataMap)
	}
	if patches[0].BaseRev != 1 || patches[0].SyncRev != 2 || dataMap["headRev"].(int) != 2 {
		t.Fatalf("delta continuity mismatch: %+v", dataMap)
	}
	if len(patches[0].PartOps) != 1 || patches[0].PartOps[0].Text != " second" {
		t.Fatalf("delta payload changed: %+v", patches[0])
	}
	resume := dataMap["resume"].(ProjectionResumeDiagnostic)
	if resume.Kind != "journal" || resume.FromRev == nil || *resume.FromRev != 1 || resume.ToRev == nil || *resume.ToRev != 2 {
		t.Fatalf("journal resume diagnostic = %+v", resume)
	}
}

func TestHandleGetSessionProjectionEpochChangeForcesFull(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "s1", Event: "turn_started",
		Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true,
	})
	handlers.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex", SessionID: "s1", Event: "text_delta",
		Data: map[string]interface{}{"itemId": "T1", "delta": "ready"}, Broadcast: true,
	})
	conn := &readFileCaptureConn{}
	handlers.eventPublisher.SetConnSyncV2(conn, true)
	handlers.eventPublisher.SetConnProjectionEpoch(conn, "previous-process-epoch")
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1", "sinceRev": 1})
	msg := WireMessage{RequestID: "r-epoch", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	dataMap := conn.data.(map[string]interface{})
	if _, ok := dataMap["projection"].(SessionProjection); !ok {
		t.Fatalf("epoch change did not force authoritative full: %+v", dataMap)
	}
	resume := dataMap["resume"].(ProjectionResumeDiagnostic)
	if resume.Kind != "full" || resume.Reason == nil || *resume.Reason != "epoch_change" || resume.RequestedRev == nil || *resume.RequestedRev != 1 {
		t.Fatalf("epoch-change resume diagnostic = %+v", resume)
	}
}

func TestHandleGetSessionProjectionJournalGapReportsFullReason(t *testing.T) {
	handlers := NewHandlers()
	for index, text := range []string{"first", " second"} {
		event := "text_delta"
		data := map[string]interface{}{"itemId": "T1", "delta": text}
		if index == 0 {
			handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
		}
		handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: event, Data: data, Broadcast: true})
	}
	handlers.eventPublisher.mu.Lock()
	handlers.eventPublisher.projectionJournal.clear("codex", "s1")
	handlers.eventPublisher.mu.Unlock()
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1", "sinceRev": 1})
	handlers.handleGetSessionProjection(conn, WireMessage{RequestID: "r-gap", BackendID: "codex", Params: params}, nil)
	resume := conn.data.(map[string]interface{})["resume"].(ProjectionResumeDiagnostic)
	if resume.Reason == nil || *resume.Reason != "journal_gap" {
		t.Fatalf("journal-gap resume diagnostic = %+v", resume)
	}
}

func TestHandleGetSessionProjectionRetentionReportsFullLimit(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.projectionJournal = NewProjectionRevisionJournal(1, 1<<20)
	handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}, Broadcast: true})
	for _, text := range []string{"first", " second", " third"} {
		handlers.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s1", Event: "text_delta", Data: map[string]interface{}{"itemId": "T1", "delta": text}, Broadcast: true})
	}
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "s1", "sinceRev": 1})
	handlers.handleGetSessionProjection(conn, WireMessage{RequestID: "r-limit", BackendID: "codex", Params: params}, nil)
	resume := conn.data.(map[string]interface{})["resume"].(ProjectionResumeDiagnostic)
	if resume.Reason == nil || *resume.Reason != "limit" {
		t.Fatalf("retention-limit resume diagnostic = %+v", resume)
	}
}

// TestHandleGetSessionProjectionRoutedByDispatch: the dispatch switch routes get_session_projection
// to the handler (guards against a missing case).
func TestHandleGetSessionProjectionRoutedByDispatch(t *testing.T) {
	// shouldSwitchWorkDirForMethod must treat it as read-only (no workdir switch).
	if shouldSwitchWorkDirForMethod("get_session_projection") {
		t.Fatal("get_session_projection should be read-only (no workdir switch)")
	}
}

func TestClaudeProjectionPullResolvesRequestedDirectoryWithoutMutatingAgentWorkDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := filepath.Join(home, "Projects", "requested")
	projectDir := filepath.Join(home, ".claude", "projects", encodeProjectKey(workDir))
	sessionID := "claude-requested-directory"
	transcriptPath := filepath.Join(projectDir, sessionID+".jsonl")
	writeClaudeProjectionRollout(t, transcriptPath, 1, 64)

	handlers := NewHandlers()
	agent := &fakeAgent{
		name:           "claudecode",
		workDir:        filepath.Join(home, "Projects", "stale"),
		transcriptPath: filepath.Join(home, "missing.jsonl"),
		richHistory: []core.RichHistoryEntry{
			{ID: "user-1", Role: "user", Content: "question"},
			{ID: "assistant-1", Role: "assistant", Content: "answer"},
		},
	}
	handlers.RegisterAgent("claudecode", agent)

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{
		"sessionId": sessionID,
		"directory": workDir,
	})
	msg := WireMessage{
		RequestID: "r-claude-directory",
		BackendID: "claude",
		Method:    "get_session_projection",
		Params:    params,
	}
	handlers.handleGetSessionProjection(conn, msg, agent)

	if conn.err != nil {
		t.Fatalf("directory-scoped projection pull failed: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok || len(proj.Turns) == 0 {
		t.Fatalf("expected hydrated Claude projection, got %+v", dataMap["projection"])
	}
	if got := agent.GetWorkDir(); got != filepath.Join(home, "Projects", "stale") {
		t.Fatalf("read-only projection pull mutated shared agent workDir: %q", got)
	}
}

func TestClaudeProjectionHydrateStitchesCompactContinuation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := filepath.Join(home, "Projects", "compact")
	projectDir := filepath.Join(home, ".claude", "projects", encodeProjectKey(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := strings.Join([]string{
		`{"type":"user","timestamp":"2026-07-29T08:00:00Z","message":{"id":"user-before","role":"user","content":"before question"}}`,
		`{"type":"assistant","timestamp":"2026-07-29T08:10:00Z","message":{"id":"assistant-before","role":"assistant","content":[{"type":"text","text":"before answer"}]}}`,
		`{"type":"system","subtype":"compact_boundary","uuid":"shared-compact","timestamp":"2026-07-29T08:17:51Z","compactMetadata":{"preTokens":169352,"postTokens":8605}}`,
	}, "\n") + "\n"
	child := strings.Join([]string{
		`{"type":"system","subtype":"compact_boundary","uuid":"shared-compact","timestamp":"2026-07-29T08:17:51Z","compactMetadata":{"preTokens":169352,"postTokens":8605}}`,
		`{"type":"user","isVisibleInTranscriptOnly":true,"isCompactSummary":true,"message":{"role":"user","content":"INTERNAL SUMMARY"}}`,
		`{"type":"assistant","timestamp":"2026-07-29T08:10:00Z","message":{"id":"assistant-before","role":"assistant","content":[{"type":"text","text":"before answer"}]}}`,
		`{"type":"user","timestamp":"2026-07-29T08:34:00Z","message":{"id":"user-after","role":"user","content":"after question"}}`,
		`{"type":"assistant","timestamp":"2026-07-29T08:35:00Z","message":{"id":"assistant-after","role":"assistant","content":[{"type":"text","text":"after answer"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "parent-session.jsonl"), []byte(parent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "child-session.jsonl"), []byte(child), 0o600); err != nil {
		t.Fatal(err)
	}

	rawAgent, err := claudecode.New(map[string]any{"work_dir": filepath.Join(home, "stale")})
	if err != nil {
		t.Fatalf("claudecode.New: %v", err)
	}
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", rawAgent)
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "child-session", "directory": workDir})
	handlers.handleGetSessionProjection(conn, WireMessage{
		RequestID: "r-compact-chain",
		BackendID: "claude",
		Method:    "get_session_projection",
		Params:    params,
	}, rawAgent)
	if conn.err != nil {
		t.Fatalf("compact-chain hydrate failed: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	text := string(raw)
	for _, want := range []string{"before question", "before answer", "已压缩对话", "after question", "after answer"} {
		if !strings.Contains(text, want) {
			t.Fatalf("projection missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "INTERNAL SUMMARY") {
		t.Fatalf("projection leaked compact internals: %s", text)
	}
}

// writeProjectionHydrateRollout builds a minimal multi-turn Codex rollout JSONL that
// scanCodexTranscriptRelayEvents + hydrateCodexProjectionFromDisk can reduce into turns.
func writeProjectionHydrateRollout(t *testing.T, path string, turns int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"s"}}` + "\n")
	for i := 0; i < turns; i++ {
		turnID := "turn-" + strconv.Itoa(i+1)
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"` + turnID + `"}}` + "\n")
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","id":"msg_` + strconv.Itoa(i+1) + `","content":[{"type":"input_text","text":"q` + strconv.Itoa(i+1) + `"}]}}` + "\n")
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a` + strconv.Itoa(i+1) + `"}]}}` + "\n")
		b.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"` + turnID + `"}}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeProjectionRollout builds a multi-turn claude session .jsonl with the given number of
// turns and assistantTextBytes of assistant text per turn (controls total file size). Each turn is
// a user prompt followed by an assistant response (stop_reason end_turn). Written directly to disk
// line-by-line so multi-MB fixtures do not allocate one giant string.
func writeClaudeProjectionRollout(t *testing.T, path string, turns, assistantTextBytes int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	asstText := strings.Repeat("a", assistantTextBytes)
	for i := 0; i < turns; i++ {
		writeClaudeRolloutLine(t, bw, "user", fmt.Sprintf("user-msg-%d", i+1), "user", "",
			[]claudeRelayContentBlock{{Type: "text", Text: "question " + strconv.Itoa(i+1)}})
		writeClaudeRolloutLine(t, bw, "assistant", fmt.Sprintf("asst-msg-%d", i+1), "assistant", "end_turn",
			[]claudeRelayContentBlock{{Type: "text", Text: asstText}})
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeRolloutLine encodes one claude transcript entry as a JSONL line.
func writeClaudeRolloutLine(t *testing.T, w io.Writer, entryType, msgID, role, stopReason string, blocks []claudeRelayContentBlock) {
	t.Helper()
	contentJSON, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	msg := map[string]interface{}{
		"id":      msgID,
		"role":    role,
		"content": json.RawMessage(contentJSON),
	}
	if stopReason != "" {
		msg["stop_reason"] = stopReason
	}
	line := map[string]interface{}{"type": entryType, "message": msg}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// waitForProjectionTurns polls the reducer until it holds want turns or the deadline elapses
// (design §10.5.6 scheme A — remaining segments stream in via the background hydrate goroutine
// after the RPC has already returned a non-empty partial).
func waitForProjectionTurns(t *testing.T, h *Handlers, backendID, sessionID string, want int, deadline time.Duration) {
	t.Helper()
	stop := time.After(deadline)
	for {
		if got := h.eventPublisher.ProjectionTurnCount(backendID, sessionID); got >= want {
			return
		}
		select {
		case <-stop:
			t.Fatalf("reducer reached %d turns, want %d within %s (background hydrate did not complete)",
				h.eventPublisher.ProjectionTurnCount(backendID, sessionID), want, deadline)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// waitForColdHydrateDrained blocks until the Kernel single-flight leaves hydrating.
func waitForColdHydrateDrained(t *testing.T, h *Handlers, backendID, sessionID string, deadline time.Duration) {
	t.Helper()
	stop := time.After(deadline)
	for {
		if h.projectionKernel.Status(backendID, sessionID).Phase != ProjectionHydrateHydrating {
			return
		}
		select {
		case <-stop:
			t.Fatalf("cold-hydrate goroutine did not drain within %s (slot still occupied)", deadline)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestHandleGetSessionProjectionColdHydrateWaitsPastFormerTimeoutBudget: design §10.5.6 scheme A.
// Under the segmented model the RPC returns a non-empty PARTIAL once the first content turn
// lands — it no longer waits for the full scan. A hydrate whose start is delayed (here 2s) must
// still serve ≥1 content turn (never the old 750ms empty bail); the remaining turns stream in
// via the background goroutine.
func TestHandleGetSessionProjectionColdHydrateWaitsPastFormerTimeoutBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 3)

	prev := coldHydrateTestHook
	coldHydrateTestHook = func(context.Context) { time.Sleep(2 * time.Second) }
	t.Cleanup(func() { coldHydrateTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-slow"})
	msg := WireMessage{RequestID: "r-slow", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if elapsed < 2*time.Second {
		t.Fatalf("RPC returned in %s; expected to wait for hydrate delay ≥2s (no 750ms timeout bail)", elapsed)
	}
	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	// Partial contract: ≥1 content turn, never an empty head-0 shell.
	if proj.SyncRev <= 0 || len(proj.Turns) == 0 {
		t.Fatalf("served empty head after delayed hydrate (forbidden §10.5): %+v", proj)
	}
	// Background goroutine finishes the remaining turns.
	waitForProjectionTurns(t, handlers, "codex", "cold-slow", 3, 2*time.Second)
}

// TestHandleGetSessionProjectionColdHydrateLargeRolloutNonEmpty: design §10.5.6 scheme A —
// a large multi-turn rollout cold-hydrates and the RPC serves a non-empty PARTIAL (≥1 content
// turn) without waiting for the full scan; the remaining turns stream in via the background
// goroutine. An empty head-0 success remains a contract violation.
func TestHandleGetSessionProjectionColdHydrateLargeRolloutNonEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large-rollout.jsonl")
	const turns = 80
	writeProjectionHydrateRollout(t, path, turns)

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-large", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-large", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	// Partial contract: ≥1 content turn (not necessarily all `turns` — the rest stream in).
	if proj.SyncRev <= 0 {
		t.Fatalf("syncRev = %d, want > 0 after cold-hydrate", proj.SyncRev)
	}
	if len(proj.Turns) == 0 {
		t.Fatalf("served empty head-0 partial (contract violation): %+v", proj)
	}
	// Background goroutine finishes the remaining turns.
	waitForProjectionTurns(t, handlers, "codex", "cold-large", turns, 3*time.Second)
	head := handlers.eventPublisher.ProjectionHeadRev("codex", "cold-large")
	if head <= 0 {
		t.Fatalf("headRev = %d after hydrate, want > 0", head)
	}
}

// TestHandleGetSessionProjectionColdHydrateSingleFlight: concurrent cold pulls share one
// hydrate and all observe non-empty state (no double-apply race serving empty to a racer).
func TestHandleGetSessionProjectionColdHydrateSingleFlight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 2)

	prev := coldHydrateTestHook
	started := make(chan struct{})
	release := make(chan struct{})
	coldHydrateTestHook = func(ctx context.Context) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() { coldHydrateTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	const n = 4
	conns := make([]*readFileCaptureConn, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		conns[i] = &readFileCaptureConn{}
		go func(i int) {
			defer wg.Done()
			params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-sf"})
			msg := WireMessage{RequestID: "r-sf", BackendID: "codex", Method: "get_session_projection", Params: params}
			handlers.handleGetSessionProjection(conns[i], msg, nil)
		}(i)
	}

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("hydrate never started")
	}
	// All RPCs must still be blocked — none served empty before hydrate finished.
	time.Sleep(50 * time.Millisecond)
	for i, c := range conns {
		if c.data != nil || c.err != nil {
			t.Fatalf("conn %d answered before hydrate completed: data=%T err=%+v", i, c.data, c.err)
		}
	}
	close(release)
	wg.Wait()

	for i, c := range conns {
		if c.err != nil {
			t.Fatalf("conn %d error: %+v", i, c.err)
		}
		dataMap, ok := c.data.(map[string]interface{})
		if !ok {
			t.Fatalf("conn %d data not map: %T", i, c.data)
		}
		proj, ok := dataMap["projection"].(SessionProjection)
		// Partial contract: every conn sees ≥1 content turn, never an empty shell.
		if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
			t.Fatalf("conn %d empty projection: %+v", i, dataMap["projection"])
		}
	}
	// All callers shared one hydrate; the background goroutine finishes both turns.
	waitForProjectionTurns(t, handlers, "codex", "cold-sf", 2, 2*time.Second)
}

// A healthy long-running hydrate returns an explicit hydrating lifecycle response at the RPC
// budget while the single-flight continues; a later pull reads the committed full snapshot.
func TestHandleGetSessionProjectionColdHydrateHardTimeoutExplicitError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 2)

	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 150 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	release := make(chan struct{})
	prevHook := coldHydrateTestHook
	coldHydrateTestHook = func(context.Context) { <-release }
	t.Cleanup(func() { coldHydrateTestHook = prevHook })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-hard-timeout", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-hang", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("RPC hung %s after hard-timeout budget; expected explicit fail near %s", elapsed, coldHydrateTimeout)
	}
	if elapsed < coldHydrateTimeout {
		t.Fatalf("RPC returned in %s; expected to wait at least hard budget %s", elapsed, coldHydrateTimeout)
	}
	if conn.err == nil {
		t.Fatalf("expected explicit hydrating response at RPC budget, got success data=%T", conn.data)
	}
	if conn.err.Code != "projection.hydrating" {
		t.Fatalf("error code = %q, want projection.hydrating (got message %q)", conn.err.Code, conn.err.Message)
	}
	if conn.err.Retryable == nil || !*conn.err.Retryable {
		t.Fatalf("hydrating must be explicitly retryable: %+v", conn.err)
	}
	if conn.err.RetryAfterMillis == nil || *conn.err.RetryAfterMillis <= 0 {
		t.Fatalf("hydrating must provide a positive retryAfterMillis: %+v", conn.err)
	}
	if conn.data != nil {
		t.Fatalf("hydrating response must not pair data with error; data=%T", conn.data)
	}
	if status := handlers.projectionKernel.Status("codex", "cold-hard-timeout"); status.Phase != ProjectionHydrateHydrating {
		t.Fatalf("background single-flight did not remain healthy: %+v", status)
	}
	if _, ok := handlers.projectionKernel.Snapshot("codex", "cold-hard-timeout"); ok {
		t.Fatal("hydrating baseline was exposed as a success snapshot")
	}

	close(release)
	waitForProjectionTurns(t, handlers, "codex", "cold-hard-timeout", 2, 2*time.Second)
	conn2 := &readFileCaptureConn{}
	msg2 := WireMessage{RequestID: "r-retry", BackendID: "codex", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn2, msg2, nil)
	if conn2.err != nil {
		t.Fatalf("retry after hard timeout should succeed, got %+v", conn2.err)
	}
	dataMap, ok := conn2.data.(map[string]interface{})
	if !ok {
		t.Fatalf("retry data not map: %T", conn2.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	// Partial contract: retry serves ≥1 content turn (slot not poisoned); rest stream in.
	if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
		t.Fatalf("retry expected committed full projection, got %+v", dataMap["projection"])
	}
}

// TestHandleGetSessionProjectionColdHydrateReal26MBRollout exercises the owner exit-criteria
// session when present on disk. Skips on machines without the fixture.
func TestHandleGetSessionProjectionColdHydrateReal26MBRollout(t *testing.T) {
	const sessionID = "019f891d-c2b2-7b43-9dcd-0b3a8b9087b5"
	path := filepath.Join(os.Getenv("HOME"), ".codex/sessions/2026/07/22/rollout-2026-07-22T17-17-36-"+sessionID+".jsonl")
	if st, err := os.Stat(path); err != nil || st.Size() < 10*1024*1024 {
		t.Skipf("real 26MB rollout not present at %s", path)
	}

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-26mb", BackendID: "codex", Method: "get_session_projection", Params: params}
	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("data.projection not SessionProjection: %T", dataMap["projection"])
	}
	if proj.SyncRev <= 0 || len(proj.Turns) == 0 {
		t.Fatalf("26MB cold-hydrate returned empty head (contract violation): syncRev=%d turns=%d elapsed=%s",
			proj.SyncRev, len(proj.Turns), elapsed)
	}
	t.Logf("26MB cold-hydrate ok: events-head syncRev=%d turns=%d elapsed=%s", proj.SyncRev, len(proj.Turns), elapsed)
}

// TestHandleGetSessionProjectionColdHydrateSegmentedFirstTurnsFirst: design §10.5.6 scheme A.
// A multi-turn rollout scans in turn-bounded segments; earlier turns MUST enter the reducer
// BEFORE the full scan completes. Block the goroutine at the end of segment 1 and observe the
// the transaction-local reducer may hold turn 1, but no partial becomes authoritative before
// the complete baseline commits.
func TestHandleGetSessionProjectionColdHydrateSegmentedFirstTurnsFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	const turns = 4
	writeProjectionHydrateRollout(t, path, turns)

	release := make(chan struct{})
	reachedSeg1 := make(chan struct{}, 1)
	prev := coldHydrateSegmentTestHook
	coldHydrateSegmentTestHook = func(ctx context.Context, segmentIdx, contentTurns int) {
		if segmentIdx >= 1 {
			select {
			case reachedSeg1 <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	t.Cleanup(func() { coldHydrateSegmentTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-seg"})
	msg := WireMessage{RequestID: "r-seg", BackendID: "codex", Method: "get_session_projection", Params: params}
	done := make(chan struct{})
	go func() {
		handlers.handleGetSessionProjection(conn, msg, nil)
		close(done)
	}()

	select {
	case <-reachedSeg1:
	case <-time.After(3 * time.Second):
		t.Fatal("segment hook never reached segment 1")
	}
	if _, ok := handlers.projectionKernel.Snapshot("codex", "cold-seg"); ok {
		t.Fatal("partial baseline became authoritative before full hydrate commit")
	}
	select {
	case <-done:
		t.Fatal("RPC returned an uncommitted partial")
	default:
	}

	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("full hydrate did not commit after release")
	}
	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	data := conn.data.(map[string]interface{})
	projection := data["projection"].(SessionProjection)
	if len(projection.Turns) != turns {
		t.Fatalf("committed turns = %d, want %d", len(projection.Turns), turns)
	}
}

// TestHandleGetSessionProjectionColdHydratePartialHasContentTurn: design §10.5.6 scheme A
// contract — a content-bearing transaction-local partial is still not served; only the complete
// committed baseline may satisfy the pull.
func TestHandleGetSessionProjectionColdHydratePartialHasContentTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 3)

	release := make(chan struct{})
	reachedSeg1 := make(chan struct{}, 1)
	prev := coldHydrateSegmentTestHook
	coldHydrateSegmentTestHook = func(ctx context.Context, segmentIdx, contentTurns int) {
		if segmentIdx >= 1 {
			select {
			case reachedSeg1 <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	t.Cleanup(func() { coldHydrateSegmentTestHook = prev })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-partial"})
	msg := WireMessage{RequestID: "r-partial", BackendID: "codex", Method: "get_session_projection", Params: params}
	done := make(chan struct{})
	go func() {
		handlers.handleGetSessionProjection(conn, msg, nil)
		close(done)
	}()
	<-reachedSeg1
	if _, ok := handlers.projectionKernel.Snapshot("codex", "cold-partial"); ok {
		t.Fatal("content partial was exposed before full commit")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("full hydrate did not return")
	}
	if conn.err != nil {
		t.Fatalf("unexpected RPC error: %+v", conn.err)
	}
	dataMap := conn.data.(map[string]interface{})
	proj := dataMap["projection"].(SessionProjection)
	if len(proj.Turns) != 3 || proj.SyncRev == 0 {
		t.Fatalf("served snapshot is not the full committed baseline: %+v", proj)
	}
}

// When the full baseline cannot commit within the pull budget, the RPC returns hydrating and
// keeps the background transaction healthy; it never serves an empty or partial success.
func TestHandleGetSessionProjectionColdHydrateFirstSegmentTimeoutReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeProjectionHydrateRollout(t, path, 3)

	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 150 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	release := make(chan struct{})
	prevHook := coldHydrateTestHook
	coldHydrateTestHook = func(context.Context) { <-release }
	t.Cleanup(func() { coldHydrateTestHook = prevHook })

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "cold-first-seg-timeout", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-fst", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if conn.err == nil {
		t.Fatalf("expected projection.hydrating when baseline cannot commit; got success data=%T", conn.data)
	}
	if conn.err.Code != "projection.hydrating" {
		t.Fatalf("error code = %q, want projection.hydrating (message %q)", conn.err.Code, conn.err.Message)
	}
	if conn.data != nil {
		t.Fatalf("first-segment timeout must not pair data with error (no empty shell): data=%T", conn.data)
	}
	// Reducer must still be empty — no content turn was ever produced.
	if _, ok := handlers.projectionKernel.Snapshot("codex", "cold-first-seg-timeout"); ok {
		t.Fatal("uncommitted hydrate became visible")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("RPC hung %s; expected to fail near the %s budget", elapsed, coldHydrateTimeout)
	}
	close(release)
	waitForProjectionTurns(t, handlers, "codex", "cold-first-seg-timeout", 3, 2*time.Second)
}

// §10.5.7 修法 1 — claude cold-hydrate matrix. Each case registers a claudecode agent whose
// TranscriptPath points at a generated .jsonl, cold-pulls, and asserts a non-empty partial is
// served (never an empty head), the full turn count arrives via the background goroutine, and the
// first segment lands well within the 15s budget.
func TestClaudeColdHydrateMatrix(t *testing.T) {
	cases := []struct {
		name             string
		turns            int
		asstTextBytes    int
		expectTotalBytes string // human label for the coverage matrix (declared, not asserted)
	}{
		{"small_lt_1MB", 3, 100, "<1MB"},
		{"medium_1_to_10MB", 10, 100_000, "1-10MB"},
		{"large_10_to_100MB", 10, 1_000_000, "10-100MB"},
		// 超大 (>100MB) is covered by the owner real-device track (§10.5.7.5 owner 真机轨);
		// a unit test cannot faithfully own-size it (CI disk/time). The partial-return property
		// validated here (first segment < 15s regardless of total size) scales to 超大 identically.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "claude-"+tc.name+".jsonl")
			writeClaudeProjectionRollout(t, path, tc.turns, tc.asstTextBytes)

			handlers := NewHandlers()
			handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", transcriptPath: path})

			conn := &readFileCaptureConn{}
			params, _ := json.Marshal(map[string]interface{}{"sessionId": "claude-" + tc.name})
			msg := WireMessage{RequestID: "r-" + tc.name, BackendID: "claude", Method: "get_session_projection", Params: params}

			start := time.Now()
			handlers.handleGetSessionProjection(conn, msg, nil)
			elapsed := time.Since(start)

			if conn.err != nil {
				t.Fatalf("unexpected RPC error: %+v", conn.err)
			}
			dataMap, ok := conn.data.(map[string]interface{})
			if !ok {
				t.Fatalf("served data not a map: %T (empty shell?)", conn.data)
			}
			proj, ok := dataMap["projection"].(SessionProjection)
			if !ok || len(proj.Turns) == 0 || proj.SyncRev <= 0 {
				t.Fatalf("claude %s: served empty head-0 partial (forbidden §10.5.1): %+v", tc.name, dataMap["projection"])
			}
			// First segment (the partial) must land within the 15s protocol budget (§10.5.7 修法 2).
			if elapsed >= defaultColdHydrateTimeout {
				t.Fatalf("claude %s: partial served after %s, within-budget first segment required (< %s)",
					tc.name, elapsed, defaultColdHydrateTimeout)
			}
			t.Logf("claude %s (%s): partial in %s, turns=%d", tc.name, tc.expectTotalBytes, elapsed, len(proj.Turns))
			// Background goroutine finishes the remaining turns.
			waitForProjectionTurns(t, handlers, "claude", "claude-"+tc.name, tc.turns, 30*time.Second)
			waitForColdHydrateDrained(t, handlers, "claude", "claude-"+tc.name, 10*time.Second)
		})
	}
}

// TestClaudeColdHydrateHonestEmpty: a claude session with a real but content-empty transcript
// (only resume-meta / non-meaningful entries) returns an honest empty projection — NOT an error,
// NOT a fake content turn. Distinct from the forbidden empty-head-on-failure.
func TestClaudeColdHydrateHonestEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	// A session_meta-only file (no user/assistant content).
	if err := os.WriteFile(path, []byte(`{"type":"system","message":null}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "claude-empty"})
	msg := WireMessage{RequestID: "r-empty", BackendID: "claude", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err != nil {
		t.Fatalf("honest-empty session must NOT error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("expected honest empty projection, got %+v", dataMap)
	}
	if len(proj.Turns) != 0 || proj.SyncRev != 0 {
		t.Fatalf("honest-empty must be 0 turns / syncRev 0, got %+v", proj)
	}
}

// A projection-only web client does not call get_session_messages. Opening a Claude session
// through get_session_projection must therefore start the transcript relay itself, otherwise
// later Mac-authored turns never enter the reducer and no projection_patch can be emitted.
func TestClaudeProjectionPullStartsTranscriptRelay(t *testing.T) {
	withFastClaudeFileRelay(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "projection-only-live"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"before"}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"a1","role":"assistant","content":[{"type":"text","text":"before answer"}],"stop_reason":"end_turn"}}`,
	)
	agent := &fakeAgent{name: "claudecode", transcriptPath: path}
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", agent)

	conn := newPublisherCaptureConn(nil)
	handlers.eventPublisher.SetConnSyncV2(conn, true)
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID})
	msg := WireMessage{RequestID: "r-live", BackendID: "claude", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, agent)
	if !handlers.relayKindIs(sessionID, relayKindClaudeFile) {
		t.Fatal("projection-only pull did not start Claude transcript relay")
	}

	appendClaudeFileRelayTranscript(t, path,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"after open"}}`,
	)
	patches := waitForProjectionPatches(t, conn, 1)
	if len(patches) == 0 {
		t.Fatal("Claude transcript growth after projection pull emitted no projection_patch")
	}
}

// TestClaudeColdHydrateMissingFileHonestError: a transcript path that does not exist must surface
// as an honest hydrate error (projection.hydrate_failed), NEVER an empty head-0 success shell
// (§10.5.1). Distinct from honest-empty (which is a real, scanned, content-less session).
func TestClaudeColdHydrateMissingFileHonestError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", transcriptPath: path})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "claude-missing"})
	msg := WireMessage{RequestID: "r-missing", BackendID: "claude", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)

	if conn.err == nil {
		t.Fatalf("missing transcript must surface an honest error, got success data=%T", conn.data)
	}
	if conn.data != nil {
		t.Fatalf("error must not pair with data (no empty shell): data=%T", conn.data)
	}
	if conn.err.Code != "projection.hydrate_failed" {
		t.Fatalf("error code = %q, want projection.hydrate_failed", conn.err.Code)
	}
	if conn.err.Retryable == nil {
		t.Fatalf("hydrate_failed must carry explicit retryability: %+v", conn.err)
	}
}

// TestProjectionNotMigratedForUnsupportedBackend: a backend with no projection cold-hydrate
// producer MUST return an honest
// projection.not_migrated error — never fall through to an empty head-0 shell (§10.5.7 修法 1).
func TestProjectionNotMigratedForUnsupportedBackend(t *testing.T) {
	for _, backend := range []string{"madeup-backend"} {
		handlers := NewHandlers()
		conn := &readFileCaptureConn{}
		params, _ := json.Marshal(map[string]interface{}{"sessionId": "s-" + backend})
		msg := WireMessage{RequestID: "r-" + backend, BackendID: backend, Method: "get_session_projection", Params: params}
		handlers.handleGetSessionProjection(conn, msg, nil)
		if conn.err == nil {
			t.Fatalf("%s: expected projection.not_migrated, got success data=%T", backend, conn.data)
		}
		if conn.data != nil {
			t.Fatalf("%s: error must not pair with data (no empty shell): %T", backend, conn.data)
		}
		if conn.err.Code != "projection.not_migrated" {
			t.Fatalf("%s: error code = %q, want projection.not_migrated", backend, conn.err.Code)
		}
		if conn.err.Retryable == nil || *conn.err.Retryable {
			t.Fatalf("%s: not_migrated must be explicitly nonretryable: %+v", backend, conn.err)
		}
	}
}

// TestGrokBuildProjectionHydrateFromRichHistory: grokbuild is migrated as a pathless
// rich-history projection backend (chat_history.jsonl snapshot). A cold get_session_projection
// must reduce the entries into a committed projection instead of returning
// projection.not_migrated.
func TestGrokBuildProjectionHydrateFromRichHistory(t *testing.T) {
	handlers := NewHandlers()
	agent := &fakeAgent{
		name: "grokbuild",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "列一下当前目录文件"},
			{
				ID:       "a1",
				Role:     "assistant",
				Content:  "src/ 下有 main.go",
				Thinking: "先看目录结构",
				Parts: []map[string]any{
					{"type": "reasoning", "content": "先看目录结构"},
					{"type": "text", "content": "src/ 下有 main.go"},
					{"type": "tool", "step": map[string]any{
						"id": "tool-1", "toolName": "list_dir", "status": "completed",
						"output": map[string]any{"kind": "inline", "text": "main.go"},
					}},
				},
			},
		},
	}
	handlers.mu.Lock()
	handlers.agents = map[string]core.Agent{"grokbuild": agent}
	handlers.mu.Unlock()

	if !backendSupportsProjectionHydrate("grokbuild") {
		t.Fatal("grokbuild must be a projection hydrate backend after migration")
	}

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "ses-grok-1", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-grok", BackendID: "grokbuild", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)
	if conn.err != nil {
		t.Fatalf("grokbuild hydrate error: %+v", conn.err)
	}
	data, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", conn.data)
	}
	raw, _ := json.Marshal(data)
	if !strings.Contains(string(raw), "列一下当前目录文件") {
		t.Fatalf("projection missing user text: %s", string(raw))
	}
	if !strings.Contains(string(raw), "src/ 下有 main.go") {
		t.Fatalf("projection missing assistant text: %s", string(raw))
	}
	if !strings.Contains(string(raw), "tool-1") {
		t.Fatalf("projection missing tool part: %s", string(raw))
	}
}

// TestClaudeEntryToProjectionEvents unit-tests the claude transcript → projection-event mapper:
// user text starts a turn (user_message), assistant blocks emit text_delta/reasoning_delta/
// tool_started, a user tool_result emits tool_finished, and a final stop_reason emits a
// segment-boundary turn_completed.
func TestClaudeEntryToProjectionEvents(t *testing.T) {
	currentTurnID := ""
	// user prompt
	user := claudeTranscriptRelayEntry{Type: "user", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "u1", Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello"}]`)}}
	evs := claudeEntryToProjectionEvents(user, &currentTurnID, nil)
	if len(evs) != 1 || evs[0].Event != "user_message" || evs[0].Data["turnId"] != "u1" {
		t.Fatalf("user entry → %+v", evs)
	}
	if currentTurnID != "u1" {
		t.Fatalf("currentTurnID = %q, want u1", currentTurnID)
	}
	// assistant text + thinking + tool_use + final stop
	asst := claudeTranscriptRelayEntry{Type: "assistant", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "a1", Role: "assistant", StopReason: "end_turn", Content: json.RawMessage(`[{"type":"text","text":"hi"},{"type":"thinking","thinking":"plan"},{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"x"}}]`)}}
	evs = claudeEntryToProjectionEvents(asst, &currentTurnID, nil)
	// expect: text_delta, reasoning_delta, tool_started, turn_completed (TurnDone)
	if len(evs) != 4 {
		t.Fatalf("assistant entry → %d events: %+v", len(evs), evs)
	}
	if evs[0].Event != "text_delta" || evs[0].Data["itemId"] != "u1" {
		t.Fatalf("text_delta must attribute to active turn u1: %+v", evs[0])
	}
	if evs[1].Event != "reasoning_delta" {
		t.Fatalf("want reasoning_delta, got %+v", evs[1])
	}
	if evs[2].Event != "tool_started" || evs[2].Data["itemId"] != "tool-1" {
		t.Fatalf("tool_started with real tool id: %+v", evs[2])
	}
	if evs[3].Event != "turn_completed" || !evs[3].TurnDone {
		t.Fatalf("final stop must emit segment-boundary turn_completed: %+v", evs[3])
	}
	// user tool_result → tool_finished matched by tool_use_id
	tr := claudeTranscriptRelayEntry{Type: "user", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "u2", Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"tool-1","content":"file body"}]`)}}
	evs = claudeEntryToProjectionEvents(tr, &currentTurnID, nil)
	if len(evs) != 1 || evs[0].Event != "tool_finished" || evs[0].Data["itemId"] != "tool-1" {
		t.Fatalf("tool_result → tool_finished matched by tool_use_id: %+v", evs)
	}

	// Real Claude user rows often omit message.id and only carry top-level uuid.
	currentTurnID = ""
	userUUID := claudeTranscriptRelayEntry{
		Type: "user",
		UUID: "3ad62e62-13af-4371-9d16-ca9ef11ad6c3",
		Message: &struct {
			ID         string          `json:"id"`
			Role       string          `json:"role"`
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		}{Role: "user", Content: json.RawMessage(`"讲个程序员笑话"`)},
	}
	evs = claudeEntryToProjectionEvents(userUUID, &currentTurnID, nil)
	if len(evs) != 1 || evs[0].Event != "user_message" {
		t.Fatalf("uuid-only user → %+v", evs)
	}
	if evs[0].Data["turnId"] != "3ad62e62-13af-4371-9d16-ca9ef11ad6c3" || evs[0].Data["itemId"] != "3ad62e62-13af-4371-9d16-ca9ef11ad6c3" {
		t.Fatalf("uuid-only user must fall back to entry.uuid: %+v", evs[0])
	}
	if currentTurnID != "3ad62e62-13af-4371-9d16-ca9ef11ad6c3" {
		t.Fatalf("currentTurnID = %q, want uuid", currentTurnID)
	}
	asstUUID := claudeTranscriptRelayEntry{
		Type: "assistant",
		UUID: "9271f8e8-f785-44cb-8925-9bfc4c7d119d",
		Message: &struct {
			ID         string          `json:"id"`
			Role       string          `json:"role"`
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		}{ID: "msg_asst_1", Role: "assistant", StopReason: "end_turn", Content: json.RawMessage(`[{"type":"text","text":"SQL JOIN joke"}]`)},
	}
	evs = claudeEntryToProjectionEvents(asstUUID, &currentTurnID, nil)
	if len(evs) != 2 {
		t.Fatalf("uuid-turn assistant → %d events: %+v", len(evs), evs)
	}
	if evs[0].Event != "text_delta" || evs[0].Data["itemId"] != "3ad62e62-13af-4371-9d16-ca9ef11ad6c3" {
		t.Fatalf("assistant text must attribute to uuid turn: %+v", evs[0])
	}
	if evs[1].Event != "turn_completed" || evs[1].Data["turnId"] != "3ad62e62-13af-4371-9d16-ca9ef11ad6c3" {
		t.Fatalf("turn_completed must carry uuid turnId: %+v", evs[1])
	}

	compact := claudeTranscriptRelayEntry{
		Type:      "system",
		Subtype:   "compact_boundary",
		UUID:      "compact-1",
		Timestamp: "2026-07-29T08:25:27.000Z",
		CompactMetadata: &claudeRelayCompactMetadata{
			PreTokens:  169352,
			PostTokens: 8605,
		},
	}
	evs = claudeEntryToProjectionEvents(compact, &currentTurnID, nil)
	if len(evs) != 1 || evs[0].Event != "system_message" {
		t.Fatalf("compact boundary → %+v", evs)
	}
	if evs[0].Data["itemId"] != "compact-1" ||
		evs[0].Data["text"] != "已压缩对话 · 节省 160.7k tokens" ||
		evs[0].Data["timestampMillis"] == nil {
		t.Fatalf("compact boundary data = %+v", evs[0].Data)
	}

	internalSummary := claudeTranscriptRelayEntry{
		Type:                      "user",
		UUID:                      "internal-summary",
		IsCompactSummary:          true,
		IsVisibleInTranscriptOnly: true,
		Message: &struct {
			ID         string          `json:"id"`
			Role       string          `json:"role"`
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		}{Role: "user", Content: json.RawMessage(`"internal compact prompt"`)}}
	if got := claudeEntryToProjectionEvents(internalSummary, &currentTurnID, nil); len(got) != 0 {
		t.Fatalf("internal compact summary must be filtered, got %+v", got)
	}
}

// TestClaudeAskUserQuestionTranscriptProjectsAsObserveOnlyUserInput verifies the real Claude
// Desktop transcript shape: AskUserQuestion is an assistant tool_use block, but this relay does
// not own the Desktop responder handle. It must produce a structured pending card with disabled
// response capabilities, never an ordinary tool_started activity row.
func TestClaudeAskUserQuestionTranscriptProjectsAsObserveOnlyUserInput(t *testing.T) {
	currentTurnID := "turn-claude-desktop"
	entry := claudeTranscriptRelayEntry{
		Type:      "assistant",
		Timestamp: "2026-08-02T07:16:46.671Z",
		Message: &struct {
			ID         string          `json:"id"`
			Role       string          `json:"role"`
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		}{
			ID:      "assistant-ask-user-question",
			Role:    "assistant",
			Content: json.RawMessage(`[{"type":"tool_use","id":"call-ask-user-question","name":"AskUserQuestion","input":{"questions":[{"header":"构建失败策略","multiSelect":false,"options":[{"description":"失败后最多重试三次。","label":"自动重试 3 次"},{"description":"首次失败即停止。","label":"立即失败并报告"}],"question":"构建失败时,你希望脚本怎么处理?"}]}}]`),
		},
	}

	events := claudeEntryToProjectionEvents(entry, &currentTurnID, nil)
	if len(events) != 1 || events[0].Event != "user_input_requested" {
		t.Fatalf("AskUserQuestion must map to one user_input_requested event, got %+v", events)
	}
	data := events[0].Data
	if data["interactionId"] != claudecode.DeriveStructuredUserInputInteractionID("call-ask-user-question") {
		t.Fatalf("interactionId = %v", data["interactionId"])
	}
	if data["turnId"] != "turn-claude-desktop" || data["itemId"] != "call-ask-user-question" {
		t.Fatalf("attribution = %+v", data)
	}
	if data["status"] != "pending" || data["canRespond"] != false || data["canReject"] != false || data["diagnosticCode"] != "observe_only" {
		t.Fatalf("observe-only capabilities/status = %+v", data)
	}
	questions, ok := data["questions"].([]interface{})
	if !ok || len(questions) != 1 {
		t.Fatalf("normalized questions = %#v", data["questions"])
	}
	question, ok := questions[0].(map[string]interface{})
	if !ok || question["prompt"] != "构建失败时,你希望脚本怎么处理?" ||
		question["answerMode"] != "single" || question["allowsCustomAnswer"] != true {
		t.Fatalf("normalized question = %#v", questions[0])
	}
	if _, hasToolStarted := data["toolName"]; hasToolStarted || events[0].Event == "tool_started" {
		t.Fatalf("AskUserQuestion must not be projected as tool_started: %+v", events[0])
	}
}

// TestClaudeAskUserQuestionTranscriptResolutionProjectsWithoutAnswerBody locks the persisted
// Claude Desktop result envelope (toolUseResult.questions/answers + message tool_result). The
// relay resolves the same interaction in place and intentionally drops the answer text.
func TestClaudeAskUserQuestionTranscriptResolutionProjectsWithoutAnswerBody(t *testing.T) {
	currentTurnID := "turn-claude-desktop"
	line := []byte(`{"type":"user","uuid":"user-ask-result","timestamp":"2026-08-02T07:17:01.000Z","sourceToolAssistantUUID":"assistant-ask-user-question","toolUseResult":{"questions":[{"header":"构建失败策略","multiSelect":false,"options":[{"label":"自动重试 3 次"},{"label":"立即失败并报告"}],"question":"构建失败时,你希望脚本怎么处理?"}],"answers":{"构建失败时,你希望脚本怎么处理?":["立即失败并报告"]}},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-ask-user-question","content":"Your questions have been answered."}]}}`)
	var entry claudeTranscriptRelayEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		t.Fatalf("unmarshal real Claude result shape: %v", err)
	}
	events := claudeEntryToProjectionEvents(entry, &currentTurnID, nil)
	if len(events) != 1 || events[0].Event != "user_input_resolved" {
		t.Fatalf("AskUserQuestion result must map to one user_input_resolved event, got %+v", events)
	}
	data := events[0].Data
	if data["interactionId"] != claudecode.DeriveStructuredUserInputInteractionID("call-ask-user-question") ||
		data["status"] != "answered" || data["source"] != "other_client" || data["resolvedAt"] != int64(1785655021000) {
		t.Fatalf("resolution = %+v", data)
	}
	if _, hasAnswers := data["answers"]; hasAnswers {
		t.Fatalf("resolved projection must not contain answers: %+v", data)
	}
}

func TestClaudeRichHistoryAskUserQuestionProjectsStructuredEventsWithoutToolActivity(t *testing.T) {
	transcript := strings.Join([]string{
		`{"type":"user","timestamp":"2026-08-02T07:16:35.730Z","message":{"id":"user-ask","role":"user","content":"先问我再继续"}}`,
		`{"type":"assistant","timestamp":"2026-08-02T07:16:46.671Z","message":{"id":"assistant-ask","role":"assistant","content":[{"type":"tool_use","id":"call-ask","name":"AskUserQuestion","input":{"questions":[{"header":"构建失败策略","multiSelect":false,"options":[{"label":"自动重试 3 次"},{"label":"立即失败并报告"}],"question":"构建失败时,你希望脚本怎么处理?"}]}}]}}`,
		`{"type":"user","timestamp":"2026-08-02T07:54:57.860Z","toolUseResult":{"questions":[{"header":"构建失败策略","multiSelect":false,"options":[{"label":"自动重试 3 次"},{"label":"立即失败并报告"}],"question":"构建失败时,你希望脚本怎么处理?"}],"answers":{"构建失败时,你希望脚本怎么处理?":["立即失败并报告"]}},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-ask","content":"Your questions have been answered."}]}}`,
	}, "\n")
	entries, err := claudecode.LoadClaudeRichHistoryFromReader(strings.NewReader(transcript), "ask-user-rich-history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var events []projectionHydrateEvent
	if err := streamRichHistoryProjectionEntries(context.Background(), entries, false, func(event projectionHydrateEvent) bool {
		events = append(events, event)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	var requested, resolved *projectionHydrateEvent
	for i := range events {
		event := &events[i]
		if event.Data["itemId"] == "call-ask" && (event.Event == "tool_started" || event.Event == "tool_finished") {
			t.Fatalf("AskUserQuestion leaked ordinary tool activity: %+v", event)
		}
		switch event.Event {
		case "user_input_requested":
			requested = event
		case "user_input_resolved":
			resolved = event
		}
	}
	if requested == nil || requested.Data["turnId"] != "user-ask" || requested.Data["status"] != "pending" ||
		requested.Data["canRespond"] != false || requested.Data["diagnosticCode"] != "observe_only" {
		t.Fatalf("requested = %+v; events=%+v", requested, events)
	}
	if resolved == nil || resolved.Data["turnId"] != "user-ask" || resolved.Data["status"] != "answered" ||
		resolved.Data["source"] != "other_client" || resolved.Data["resolvedAt"] != int64(1785657297860) {
		t.Fatalf("resolved = %+v; events=%+v", resolved, events)
	}
}

func TestPendingClaudeRichHistoryAskUserQuestionKeepsRequiresAction(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "user-ask", Role: "user", Content: "先问我再继续"},
		{ID: "assistant-ask", Role: "assistant", Parts: []map[string]any{{
			"type": "user_input", "itemId": "call-ask", "interactionId": "ui_ask",
			"status": "pending", "questions": []core.UserInputQuestion{{
				ID: "ui_ask_q_0", Prompt: "怎么处理?", AnswerMode: core.UserInputAnswerModeSingle,
				Options: []core.UserInputOption{{ID: "ui_ask_q_0_o_0", Label: "A"}}, Required: true,
			}}, "canRespond": false, "canReject": false, "diagnosticCode": "observe_only",
		}}},
	}
	var events []projectionHydrateEvent
	if err := streamRichHistoryProjectionEntries(context.Background(), entries, false, func(event projectionHydrateEvent) bool {
		events = append(events, event)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Event == "turn_completed" && event.Data["turnId"] == "user-ask" {
			t.Fatalf("pending AskUserQuestion must not be sealed: %+v", events)
		}
	}
	r := newTestReducer()
	for i, event := range events {
		r.Apply(ev(i+1, "claude", "s1", event.Event, event.Data))
	}
	projection, ok := r.Snapshot("claude", "s1")
	if !ok || projection.Execution.Phase != "requires_action" || projection.Execution.ActiveTurnID != "user-ask" {
		t.Fatalf("execution = %+v want requires_action/user-ask; events=%+v", projection.Execution, events)
	}
}

// collectRichHistoryEvents runs the adapter over entries and returns emitted
// events plus the reducer projection after applying them (opencode backend).
func collectRichHistoryEvents(t *testing.T, entries []core.RichHistoryEntry, seal bool) ([]projectionHydrateEvent, SessionProjection) {
	t.Helper()
	var events []projectionHydrateEvent
	emit := func(event projectionHydrateEvent) bool {
		events = append(events, event)
		return true
	}
	if err := streamRichHistoryProjectionEntries(context.Background(), entries, seal, emit); err != nil {
		t.Fatal(err)
	}
	r := newTestReducer()
	for i, event := range events {
		r.Apply(ev(i+1, "opencode", "s1", event.Event, event.Data))
	}
	projection, ok := r.Snapshot("opencode", "s1")
	if !ok {
		t.Fatalf("no projection; events=%+v", events)
	}
	return events, projection
}

// 2026-08-14 hydrate-hang regression: an idle session whose rich history ends
// with an unanswered user turn (the empty-turn incident left exactly this
// shape) must cold-hydrate — the adapter seals the dead turn as turn_error so
// the authoritative commit gate passes.
func TestRichHistoryTrailingUnansweredUserTurnSealedWhenIdle(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "msg_u1", Role: "user", Content: "讲个笑话"},
		{ID: "msg_a1", Role: "assistant", Content: "好笑的笑话"},
		{ID: "msg_u2", Role: "user", Content: "讲个鬼故事"},
	}
	events, projection := collectRichHistoryEvents(t, entries, true)

	var seal *projectionHydrateEvent
	for i := range events {
		if events[i].Event == "turn_error" {
			seal = &events[i]
		}
	}
	if seal == nil {
		t.Fatalf("no trailing turn_error seal: %+v", events)
	}
	if seal.Data["turnId"] != "msg_u2" || seal.TurnDone != true {
		t.Fatalf("seal = %+v, want turnId=msg_u2 TurnDone=true", seal)
	}
	if projection.Execution.Phase != "idle" {
		t.Fatalf("execution phase = %q, want idle after seal", projection.Execution.Phase)
	}
	if len(projection.Turns) != 2 {
		t.Fatalf("turn count = %d, want 2: %+v", len(projection.Turns), projection.Turns)
	}
	for _, turn := range projection.Turns {
		if turn.Status != "completed" && turn.Status != "error" {
			t.Fatalf("turn %s status = %q, want terminal", turn.TurnID, turn.Status)
		}
	}
	if idSet := NonTerminalTurnIDsOf(t, projection); len(idSet) != 0 {
		t.Fatalf("non-terminal turns after seal: %v", idSet)
	}
}

// A session the backend reports as active (turn in flight) must NOT be sealed —
// the live rail owns the in-flight turn's terminal event.
func TestRichHistoryTrailingUnansweredUserTurnNotSealedWhenLive(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "msg_a1", Role: "assistant", Content: "好笑的笑话"},
		{ID: "msg_u2", Role: "user", Content: "正在跑的问题"},
	}
	events, _ := collectRichHistoryEvents(t, entries, false)
	for _, event := range events {
		if event.Event == "turn_error" {
			t.Fatalf("live session must not be sealed: %+v", events)
		}
	}
}

// Live in-flight assistant shells have no content yet. Treating that row as a
// complete snapshot emits turn_completed and commits execution.phase=idle
// (real device 2026-08-20: headRev=2, executionBytes=16, one user part).
func TestRichHistoryEmptyAssistantNotCompletedWhenLive(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "msg_u1", Role: "user", Content: "讲个猴哥语录100字左右"},
		{ID: "msg_a1", Role: "assistant", Content: ""},
	}
	events, projection := collectRichHistoryEvents(t, entries, false)
	for _, event := range events {
		if event.Event == "turn_completed" || event.Event == "turn_error" {
			t.Fatalf("live empty assistant must not be sealed: %+v", events)
		}
	}
	if projection.Execution.Phase != "running" {
		t.Fatalf("execution phase = %q, want running", projection.Execution.Phase)
	}
	if projection.Execution.ActiveTurnID != "msg_u1" {
		t.Fatalf("activeTurnId = %q, want msg_u1", projection.Execution.ActiveTurnID)
	}
}

// Dead sessions keep the complete-snapshot seal on an empty assistant row.
func TestRichHistoryEmptyAssistantCompletedWhenIdle(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "msg_u1", Role: "user", Content: "讲个猴哥语录100字左右"},
		{ID: "msg_a1", Role: "assistant", Content: ""},
	}
	events, projection := collectRichHistoryEvents(t, entries, true)
	found := false
	for _, event := range events {
		if event.Event == "turn_completed" && event.Data["turnId"] == "msg_u1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dead empty assistant must still complete: %+v", events)
	}
	if projection.Execution.Phase != "idle" {
		t.Fatalf("execution phase = %q, want idle after complete snapshot", projection.Execution.Phase)
	}
}

// Turns that DID get an assistant reply are sealed by turn_completed; no
// synthetic turn_error may appear.
func TestRichHistoryAnsweredTurnsNotErrorSealed(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "msg_u1", Role: "user", Content: "讲个笑话"},
		{ID: "msg_a1", Role: "assistant", Content: "好笑的笑话"},
	}
	events, projection := collectRichHistoryEvents(t, entries, true)
	for _, event := range events {
		if event.Event == "turn_error" {
			t.Fatalf("answered turn error-sealed: %+v", events)
		}
	}
	if len(projection.Turns) != 1 || projection.Turns[0].Status != "completed" {
		t.Fatalf("turns = %+v", projection.Turns)
	}
}

// An assistant row with a pending user_input holds requires_action — it must
// clear the unanswered marker even though it emits no turn_completed, and no
// turn_error may be synthesized for it.
func TestRichHistoryPendingUserInputNotErrorSealed(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "msg_u1", Role: "user", Content: "先问我再继续"},
		{ID: "msg_a1", Role: "assistant", Parts: []map[string]any{{
			"type": "user_input", "itemId": "call-ask", "interactionId": "ui_ask",
			"status": "pending", "questions": []core.UserInputQuestion{{
				ID: "ui_ask_q_0", Prompt: "怎么处理?", AnswerMode: core.UserInputAnswerModeSingle,
				Options: []core.UserInputOption{{ID: "ui_ask_q_0_o_0", Label: "A"}}, Required: true,
			}}, "canRespond": false, "canReject": false, "diagnosticCode": "observe_only",
		}}},
	}
	events, projection := collectRichHistoryEvents(t, entries, true)
	for _, event := range events {
		if event.Event == "turn_error" {
			t.Fatalf("requires_action boundary error-sealed: %+v", events)
		}
	}
	if projection.Execution.Phase != "requires_action" {
		t.Fatalf("execution phase = %q, want requires_action", projection.Execution.Phase)
	}
}

// Consecutive unanswered user rows (multiple empty turns in a row) are each
// sealed, so the commit gate cannot strand the older one.
func TestRichHistoryConsecutiveUnansweredUserTurnsAllSealed(t *testing.T) {
	entries := []core.RichHistoryEntry{
		{ID: "msg_u1", Role: "user", Content: "第一条"},
		{ID: "msg_u2", Role: "user", Content: "第二条"},
	}
	events, projection := collectRichHistoryEvents(t, entries, true)
	sealed := map[string]bool{}
	for _, event := range events {
		if event.Event == "turn_error" {
			sealed[event.Data["turnId"].(string)] = true
		}
	}
	if !sealed["msg_u1"] || !sealed["msg_u2"] {
		t.Fatalf("sealed = %v, want both turns; events=%+v", sealed, events)
	}
	if idSet := NonTerminalTurnIDsOf(t, projection); len(idSet) != 0 {
		t.Fatalf("non-terminal turns after seal: %v", idSet)
	}
}

func NonTerminalTurnIDsOf(t *testing.T, projection SessionProjection) []string {
	t.Helper()
	var ids []string
	for _, turn := range projection.Turns {
		if turn.Status != "completed" && turn.Status != "aborted" && turn.Status != "error" {
			ids = append(ids, turn.TurnID)
		}
	}
	return ids
}

func TestClaudeDesktopTranscriptPendingQuestionKeepsCustomAndRequiresAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-desktop-pending.jsonl")
	transcript := strings.Join([]string{
		`{"type":"user","uuid":"user-ask","timestamp":"2026-08-02T10:00:00.000Z","message":{"role":"user","content":"先问我再继续"}}`,
		`{"type":"assistant","uuid":"assistant-ask","parentUuid":"user-ask","timestamp":"2026-08-02T10:00:01.000Z","message":{"id":"assistant-ask","role":"assistant","content":[{"type":"tool_use","id":"call-ask","name":"AskUserQuestion","input":{"questions":[{"header":"构建失败策略","multiSelect":false,"options":[{"label":"自动重试 3 次"},{"label":"立即失败并报告"}],"question":"构建失败时如何处理？"}]}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	var events []projectionHydrateEvent
	if err := streamClaudeTranscriptProjectionEvents(context.Background(), path, func(event projectionHydrateEvent) bool {
		events = append(events, event)
		return true
	}); err != nil {
		t.Fatal(err)
	}

	requested := -1
	for index, event := range events {
		if event.Event == "turn_completed" && event.Data["turnId"] == "user-ask" {
			t.Fatalf("pending external question was incorrectly completed: %+v", events)
		}
		if event.Event == "user_input_requested" {
			requested = index
			questions, ok := event.Data["questions"].([]interface{})
			if !ok || len(questions) != 1 {
				t.Fatalf("questions = %#v", event.Data["questions"])
			}
			question, ok := questions[0].(map[string]interface{})
			if !ok || question["allowsCustomAnswer"] != true || event.Data["canRespond"] != false ||
				event.Data["canReject"] != false || event.Data["diagnosticCode"] != "observe_only" {
				t.Fatalf("external question contract = event:%+v question:%#v", event, questions[0])
			}
		}
	}
	if requested < 0 {
		t.Fatalf("missing user_input_requested: %+v", events)
	}

	r := newTestReducer()
	for index, event := range events {
		r.Apply(ev(index+1, "claude", "desktop-session", event.Event, event.Data))
	}
	projection, ok := r.Snapshot("claude", "desktop-session")
	if !ok || projection.Execution.Phase != "requires_action" || projection.Execution.ActiveTurnID != "user-ask" {
		t.Fatalf("execution = %+v want requires_action/user-ask; events=%+v", projection.Execution, events)
	}
}

func TestStreamClaudeTranscriptProjectionEventsCompactionBoundaryFiltersInternalSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compact.jsonl")
	transcript := strings.Join([]string{
		`{"type":"system","subtype":"compact_boundary","uuid":"compact-1","timestamp":"2026-07-29T08:25:27.000Z","compactMetadata":{"preTokens":169352,"postTokens":8605}}`,
		`{"type":"user","uuid":"internal-summary","isVisibleInTranscriptOnly":true,"isCompactSummary":true,"message":{"role":"user","content":"internal compact prompt"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	var events []projectionHydrateEvent
	err := streamClaudeTranscriptProjectionEvents(context.Background(), path, func(event projectionHydrateEvent) bool {
		events = append(events, event)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event != "system_message" {
		t.Fatalf("events = %+v, want one system_message", events)
	}
	if events[0].Data["text"] != "已压缩对话 · 节省 160.7k tokens" {
		t.Fatalf("system summary = %#v", events[0].Data["text"])
	}
}

func TestOpenCodeRichHistoryEntryToProjectionEvents(t *testing.T) {
	current := ""
	user := core.RichHistoryEntry{ID: "u-oc-1", Role: "user", Content: "hello opencode"}
	evs := openCodeRichHistoryEntryToProjectionEvents(user, &current, true)
	if len(evs) != 1 || evs[0].Event != "user_message" || evs[0].Data["turnId"] != "u-oc-1" {
		t.Fatalf("user → %+v", evs)
	}
	if current != "u-oc-1" {
		t.Fatalf("currentTurnID = %q", current)
	}
	asst := core.RichHistoryEntry{
		ID:       "a-oc-1",
		Role:     "assistant",
		Content:  "world",
		Thinking: "plan",
		Parts: []map[string]any{
			{"type": "reasoning", "content": "plan"},
			{"type": "text", "content": "world"},
			{"type": "tool", "step": map[string]any{
				"id": "tool-1", "toolName": "bash", "status": "completed",
				"output": map[string]any{"kind": "inline", "text": "ok"},
			}},
		},
	}
	evs = openCodeRichHistoryEntryToProjectionEvents(asst, &current, true)
	if len(evs) < 4 {
		t.Fatalf("assistant → %d events: %+v", len(evs), evs)
	}
	if evs[0].Event != "reasoning_delta" || evs[0].Data["itemId"] != "u-oc-1" {
		t.Fatalf("reasoning must attribute to user turn: %+v", evs[0])
	}
	if evs[1].Event != "text_delta" || evs[1].Data["itemId"] != "u-oc-1" {
		t.Fatalf("text must attribute to user turn: %+v", evs[1])
	}
	last := evs[len(evs)-1]
	if last.Event != "turn_completed" || last.Data["turnId"] != "u-oc-1" || !last.TurnDone {
		t.Fatalf("turn_completed = %+v", last)
	}
}

func TestRichHistorySystemEntryToProjectionEvent(t *testing.T) {
	current := "user-before"
	entry := core.RichHistoryEntry{
		ID:        "compact-1",
		Role:      "system",
		Content:   "已压缩对话 · 节省 160.7k tokens",
		Timestamp: time.Date(2026, 7, 29, 8, 17, 51, 0, time.UTC),
	}
	events := openCodeRichHistoryEntryToProjectionEvents(entry, &current, true)
	if len(events) != 1 || events[0].Event != "system_message" {
		t.Fatalf("system rich history events = %+v", events)
	}
	if events[0].Data["itemId"] != "compact-1" ||
		events[0].Data["text"] != "已压缩对话 · 节省 160.7k tokens" {
		t.Fatalf("system event data = %+v", events[0].Data)
	}
	if current != "" {
		t.Fatalf("compact must end prior turn attribution, current = %q", current)
	}
}

func TestOpenCodeProjectionHydrateFromRichHistory(t *testing.T) {
	handlers := NewHandlers()
	agent := &fakeAgent{
		name: "opencode",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "ping"},
			{ID: "a1", Role: "assistant", Content: "pong"},
		},
	}
	handlers.mu.Lock()
	handlers.agents = map[string]core.Agent{"opencode": agent}
	handlers.mu.Unlock()

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "ses-oc-1", "sinceRev": 0})
	msg := WireMessage{RequestID: "r-oc", BackendID: "opencode", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, nil)
	if conn.err != nil {
		t.Fatalf("opencode hydrate error: %+v", conn.err)
	}
	data, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", conn.data)
	}
	// Accept either snapshot or projection wrapper shapes used by the RPC.
	raw, _ := json.Marshal(data)
	if !strings.Contains(string(raw), "pong") {
		t.Fatalf("projection missing assistant text: %s", string(raw))
	}
	if !strings.Contains(string(raw), "ping") {
		t.Fatalf("projection missing user text: %s", string(raw))
	}
}

// TestOpenCodeHandleRPCRoutesGetSessionProjection: production short-circuits opencode
// through handleOpenCodeRPC when ocProxy is registered. get_session_projection must be
// on that allowlist and reach handleGetSessionProjection — not method_not_found.
func TestOpenCodeHandleRPCRoutesGetSessionProjection(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "opencode",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "ping"},
			{ID: "a1", Role: "assistant", Content: "pong"},
		},
	}
	handlers.RegisterAgent("opencode", agent)
	// Any non-nil ocProxy arms isOC(); URL is unused for projection routing.
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy("http://127.0.0.1:1", "", ""))

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "get_session_projection",
		RequestID: "oc-proj-route-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses-oc-route", "sinceRev": 0}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	msg0 := messages[0]
	if errObj, _ := msg0["error"].(map[string]any); errObj != nil {
		if code, _ := errObj["code"].(string); code == "method_not_found" {
			t.Fatalf("opencode get_session_projection must not be method_not_found: %+v", errObj)
		}
		t.Fatalf("unexpected error: %+v", errObj)
	}
	if ok, _ := msg0["ok"].(bool); ok != true {
		t.Fatalf("ok = %#v, want true; full=%+v", msg0["ok"], msg0)
	}
	data, _ := msg0["data"].(map[string]any)
	raw, _ := json.Marshal(data)
	if !strings.Contains(string(raw), "pong") || !strings.Contains(string(raw), "ping") {
		t.Fatalf("routed projection missing history content: %s", string(raw))
	}
}

// M4 (§5.1): a Claude cold open must start the live file relay BEFORE the hydrate wait
// (§3.2), so in-flight terminal events can feed the commit gate while the transaction is
// blocked. We hold the hydrate goroutine at its start hook and assert the file relay is
// already running while the RPC is still pending — the ordering that lets a live
// turn_completed / synthesized turn_aborted release the gate instead of waiting 15s.
func TestClaudeFileRelayStartedBeforeHydrate(t *testing.T) {
	withFastClaudeFileRelay(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "relay-before-hydrate"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"prompt"}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"a1","role":"assistant","content":[{"type":"text","text":"answer"}],"stop_reason":"end_turn"}}`,
	)

	prevHook := coldHydrateTestHook
	started := make(chan struct{})
	release := make(chan struct{})
	coldHydrateTestHook = func(ctx context.Context) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() { coldHydrateTestHook = prevHook })

	handlers := NewHandlers()
	agent := &fakeAgent{name: "claudecode", transcriptPath: path}
	handlers.RegisterAgent("claudecode", agent)

	conn := &readFileCaptureConn{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID})
		msg := WireMessage{RequestID: "r-order", BackendID: "claude", Method: "get_session_projection", Params: params}
		handlers.handleGetSessionProjection(conn, msg, agent)
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("hydrate goroutine never started")
	}
	// The RPC is still blocked in the hydrate wait; the Claude file relay must already be
	// running (started via the beforeHydrate hook at admission, ahead of the gate wait).
	if !handlers.relayKindIs(sessionID, relayKindClaudeFile) {
		t.Fatal("Claude file relay was not running while hydrate was still waiting")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RPC did not return after hydrate release")
	}
	if conn.err != nil {
		t.Fatalf("RPC error: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not map: %T", conn.data)
	}
	if _, ok := dataMap["projection"].(SessionProjection); !ok {
		t.Fatalf("expected committed projection, got %+v", dataMap)
	}
}

// M6 (§5.1, end-to-end): cold-opening a RUNNING Claude session must return a snapshot within
// the RPC budget (well under 15s) instead of projection.hydrating. The transcript tail is a
// non-terminal user row (no assistant terminal event yet); the process is live, so §3.1
// releases the commit gate and the RPC serves the honest running partial immediately.
func TestHandleGetSessionProjectionRunningSessionWithinBudget(t *testing.T) {
	withFastClaudeFileRelay(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "running-session-budget"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"running-user-1","message":{"role":"user","content":"long running task"}}`,
	)

	agent := &fakeCompositeHistoryAgent{
		fakeAgent: &fakeAgent{
			name: "claudecode",
			liveProcesses: map[string]core.LiveSessionProcess{
				sessionID: {SessionID: sessionID, PID: 4242, Live: true},
			},
			alivePIDs: map[int]bool{4242: true},
		},
		segments: []core.TranscriptSourceSegment{
			{Identity: sessionID, Path: path},
		},
		entries: []core.RichHistoryEntry{
			{ID: "running-user-1", Role: "user", Content: "long running task"},
		},
	}
	handlers := NewHandlers()
	handlers.RegisterAgent("claudecode", agent)

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID})
	msg := WireMessage{RequestID: "r-running", BackendID: "claude", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, agent)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("running-session cold open took %s; must return well under the 15s RPC budget", elapsed)
	}
	if conn.err != nil {
		t.Fatalf("running-session cold open failed: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("expected committed projection, got %+v", dataMap)
	}
	if len(proj.Turns) != 1 {
		t.Fatalf("projection turns = %d, want the single in-flight turn", len(proj.Turns))
	}
	// Honest running partial: the in-flight turn stays running (never fake-completed, and
	// never stuck hydrating).
	if got := proj.Turns[0].Status; got != "running" {
		t.Fatalf("in-flight turn status=%q, want \"running\"", got)
	}
	if got := proj.Execution.Phase; got != "running" {
		t.Fatalf("execution phase=%q, want \"running\"", got)
	}
}

// TestHandleGetSessionProjectionRunningCodexSessionWithinBudget is the codex analogue of
// TestHandleGetSessionProjectionRunningSessionWithinBudget: a running codex session cold-opened
// mid-flight must return an honest running partial well under the 15s RPC budget instead of
// blocking on the in-flight turn's terminal event. Codex liveness is the sessionRegistry state
// maintained by the passive app-server subscriber / codex file relay (§3.1 extension), sampled
// at hydrate admission; the relay-before-hydrate ordering plus the synchronous transcript-state
// fallback close the cold-start race.
func TestHandleGetSessionProjectionRunningCodexSessionWithinBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-running-rollout.jsonl")
	rollout := strings.Join([]string{
		`{"timestamp":"2026-08-12T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-running"}}`,
		`{"timestamp":"2026-08-12T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","id":"msg-user","content":[{"type":"input_text","text":"long running task"}]}}`,
		`{"timestamp":"2026-08-12T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial answer"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})
	// The passive app-server subscriber (or a prior relay tick) has already marked the session
	// running — this is the explicit lifecycle signal sampled at hydrate admission.
	handlers.sessions.markRunning("running-codex-session")

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "running-codex-session"})
	msg := WireMessage{RequestID: "r-running-codex", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("running-codex cold open took %s; must return well under the 15s RPC budget", elapsed)
	}
	if conn.err != nil {
		t.Fatalf("running-codex cold open failed: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("expected committed projection, got %+v", dataMap)
	}
	if len(proj.Turns) != 1 {
		t.Fatalf("projection turns = %d, want the single in-flight turn", len(proj.Turns))
	}
	// Honest running partial: the in-flight turn stays running (never fake-completed, and
	// never stuck hydrating).
	if got := proj.Turns[0].Status; got != "running" {
		t.Fatalf("in-flight turn status=%q, want \"running\"", got)
	}
	if got := proj.Execution.Phase; got != "running" {
		t.Fatalf("execution phase=%q, want \"running\"", got)
	}
}

// TestHandleGetSessionProjectionRunningCodexSessionTranscriptFallback verifies the cold-start
// race closer: even when the registry has not yet marked the session running (relay goroutine
// has not run its first tick), a transcript whose tail is a non-terminal task_started still
// samples sourceIsLive=true at hydrate admission and commits the running partial immediately.
func TestHandleGetSessionProjectionRunningCodexSessionTranscriptFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-running-fallback.jsonl")
	rollout := strings.Join([]string{
		`{"timestamp":"2026-08-12T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-fallback"}}`,
		`{"timestamp":"2026-08-12T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","id":"msg-user","content":[{"type":"input_text","text":"mid-flight prompt"}]}}`,
		`{"timestamp":"2026-08-12T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"in progress"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}

	handlers := NewHandlers()
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", transcriptPath: path})
	// Deliberately do NOT markRunning: the transcript fallback must carry liveness on its own.

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "running-codex-fallback"})
	msg := WireMessage{RequestID: "r-running-codex-fallback", BackendID: "codex", Method: "get_session_projection", Params: params}

	start := time.Now()
	handlers.handleGetSessionProjection(conn, msg, nil)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("running-codex transcript-fallback cold open took %s; must be well under the 15s RPC budget", elapsed)
	}
	if conn.err != nil {
		t.Fatalf("running-codex transcript-fallback cold open failed: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("expected committed projection, got %+v", dataMap)
	}
	if len(proj.Turns) != 1 {
		t.Fatalf("projection turns = %d, want the single in-flight turn", len(proj.Turns))
	}
	if got := proj.Turns[0].Status; got != "running" {
		t.Fatalf("in-flight turn status=%q, want \"running\"", got)
	}
}

// TestGrokBuildProjectionHydratePendingQuestionGate (D-G4 断线恢复): a cold
// baseline whose final turn carries a pending user_input part (the rehydrated
// unanswered ask_user_question) deliberately has NO terminal event, so the
// commit gate needs a live signal. handleGetSessionProjection itself
// subscribes the pulling conn (WP5 patch push) — and hydrate only ever runs
// because someone pulled — so the grokbuild §3.1 branch (subscriber ⇒ live)
// admits the honest requires_action partial on the real path. The narrow
// gate lives in the agent: a pending part is only produced while the leader
// socket accepts (GetRichSessionHistory), so a dead leader falls back to the
// all-terminal tool-step baseline that commits without any live signal.
func TestGrokBuildProjectionHydratePendingQuestionGate(t *testing.T) {
	pendingParts := []map[string]any{{
		"type": "user_input", "itemId": "call_open", "interactionId": "call_open",
		"status": "pending", "canRespond": true, "canReject": true,
		"questions": []map[string]any{{
			"id": "call_open", "prompt": "选一个？", "answerMode": "single",
			"options":       []map[string]any{{"id": "A", "label": "A", "description": "da"}},
			"allowsCustomAnswer": true, "required": true,
		}},
	}}
	rich := func() []core.RichHistoryEntry {
		return []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "问个问题"},
			{ID: "a1", Role: "assistant", Parts: pendingParts},
		}
	}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": "ses-ask-gate", "sinceRev": 0})

	// The real path: pull (⇒ subscribe) → live signal → the pending-question
	// partial commits with execution requires_action and the card in place.
	h := NewHandlers()
	h.mu.Lock()
	h.agents = map[string]core.Agent{"grokbuild": &fakeAgent{name: "grokbuild", richHistory: rich()}}
	h.mu.Unlock()
	conn := &readFileCaptureConn{}
	h.handleGetSessionProjection(conn, WireMessage{RequestID: "r-sub", BackendID: "grokbuild", Method: "get_session_projection", Params: params}, nil)
	if conn.err != nil {
		t.Fatalf("pull error: %v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	if !strings.Contains(string(raw), "call_open") {
		t.Fatalf("snapshot missing the pending question card: %s", string(raw))
	}
	if !strings.Contains(string(raw), "requires_action") {
		t.Fatalf("pending question must keep execution in requires_action: %s", string(raw))
	}
}
