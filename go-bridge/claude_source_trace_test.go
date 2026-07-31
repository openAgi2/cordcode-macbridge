package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaudeSourceCorrelationGenerationStableOnAppendChangesOnRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := newClaudeSourceCorrelationTracker()
	first, err := tracker.Observe("claude", "session", "segment", path, 2)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("b\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	appended, err := tracker.Observe("claude", "session", "segment", path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if appended != first {
		t.Fatalf("append changed generation: first=%+v appended=%+v", first, appended)
	}
	if err := os.WriteFile(path, []byte("x\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rewritten, err := tracker.Observe("claude", "session", "segment", path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.SegmentStableKey != first.SegmentStableKey ||
		rewritten.SegmentGeneration == first.SegmentGeneration {
		t.Fatalf("rewrite correlation = %+v, first=%+v", rewritten, first)
	}
}

func TestClaudeSourceTraceCorrelationEvidenceTable(t *testing.T) {
	t.Setenv(claudeSourceTraceEnv, "1")
	root := t.TempDir()
	path := filepath.Join(root, "source.jsonl")
	row := `{"type":"user","uuid":"trace-record","message":{"id":"trace-turn","role":"user","content":"prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handlers := newTestHandlers(t)
	turnID := ""
	if err := handlers.traceClaudeHydrateRange(
		context.Background(), "claude", "trace-session", "trace-segment",
		path, 0, int64(len(row)), &turnID,
	); err != nil {
		t.Fatal(err)
	}
	correlation, err := handlers.claudeSourceCorrelation.Observe(
		"claude", "trace-session", "trace-segment", path, int64(len(row)),
	)
	if err != nil {
		t.Fatal(err)
	}
	state := ClaudeSourceState{
		SchemaVersion: ClaudeSourceStateSchemaVersion, SourceGeneration: "source-generation",
		CursorVector: []ClaudeSourceCursor{{
			SegmentStableKey: correlation.SegmentStableKey, SegmentGeneration: correlation.SegmentGeneration,
			MembershipDigest: "membership",
		}},
		GraphNodes: map[string][]ClaudeGraphOccurrence{}, LogicalRecords: map[string]ClaudeLogicalRecord{},
	}
	if err := handlers.projectionKernel.InstallClaudeSourceState("claude", "trace-session", state); err != nil {
		t.Fatal(err)
	}
	result, err := handlers.projectionKernel.ApplyClaudeSourceRecordBatch(ClaudeSourceRecordBatch{
		BackendID: "claude", SessionID: "trace-session", BridgeEpoch: "epoch",
		Record: ClaudeSourceRecordTransition{
			LogicalRecordUUID: "trace-record", StructuralKind: "user",
			GraphResolvedTurn: "trace-turn", SegmentStableKey: correlation.SegmentStableKey,
			SegmentGeneration: correlation.SegmentGeneration, SourceGeneration: "source-generation",
			RawByteStart: 0, RawByteEnd: int64(len(row)),
			ContentBlocks:     []json.RawMessage{json.RawMessage(`{"type":"text","text":"prompt"}`)},
			SemanticLifecycle: json.RawMessage(`{"role":"user"}`),
			Contribution:      ClaudeProjectionContribution{TurnID: "trace-turn", PartID: "trace-record"},
		},
		Events: []projectionHydrateEvent{{
			Event: "user_message",
			Data:  map[string]interface{}{"turnId": "trace-turn", "itemId": "trace-record", "text": "prompt"},
		}},
	})
	if err != nil || result.Status != ClaudeSourceBatchAcceptedProjection {
		t.Fatalf("kernel transition = %+v err=%v", result, err)
	}
	tracePath := filepath.Join(root, "trace.jsonl")
	if err := os.WriteFile(tracePath, logs.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"python3",
		filepath.Join(claudeSourceShapesDir, "correlate_source_trace.py"),
		tracePath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("correlate trace: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "hydrate\tbaseline\t0\t") ||
		!strings.Contains(text, "\ttrace-record\taccepted_projection\t1\ttrace-turn\ttrace-record") ||
		!strings.Contains(text, "joined=1 source=1 kernel=1") {
		t.Fatalf("unexpected evidence table:\n%s", text)
	}
	t.Logf("correlated evidence:\n%s", text)
}

// lockedBuffer is a goroutine-safe bytes.Buffer so a test can read captured slog output while the
// relay goroutine writes via the slog handler (bytes.Buffer itself is not safe for concurrent use).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestClaudeSourceTraceLiveGrowthCarriesAbsoluteRangeAndCorrelation(t *testing.T) {
	t.Setenv(claudeSourceTraceEnv, "1")
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "live-source-trace"
	user := `{"type":"user","uuid":"trace-user","message":{"id":"trace-turn","role":"user","content":"prompt"}}`
	path := writeClaudeFileRelayTranscript(t, home, sessionID, user)

	var logs lockedBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)
	_ = client.readEvents(t, 1)
	assistant := `{"type":"assistant","uuid":"trace-assistant","parentUuid":"trace-user","message":{"id":"trace-response","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`
	appendClaudeFileRelayTranscript(t, path, assistant)
	_ = client.readEvents(t, 3)

	wantStart := int64(len(user) + 1)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) != nil ||
				entry["msg"] != "go-bridge: claude_source_trace" ||
				entry["phase"] != "live" {
				continue
			}
			if entry["backendID"] != "claude" ||
				entry["sessionPrefix"] != projectionSessionLogPrefix(sessionID) ||
				entry["uuid"] != "trace-assistant" ||
				entry["parentUuid"] != "trace-user" ||
				int64(entry["byteStart"].(float64)) != wantStart ||
				int64(entry["byteEnd"].(float64)) != info.Size() ||
				entry["segmentStableKey"] == "" ||
				entry["segmentGeneration"] == "" {
				t.Fatalf("live trace = %+v", entry)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missing live source trace:\n%s", logs.String())
}

func TestClaudeSourceTraceOffDoesNotOpenOrParseSource(t *testing.T) {
	t.Setenv(claudeSourceTraceEnv, "0")
	handlers := newTestHandlers(t)
	err := handlers.traceClaudeHydrateRange(
		context.Background(), "claude", "session", "segment",
		filepath.Join(t.TempDir(), "does-not-exist.jsonl"), 0, 10, nil,
	)
	if err != nil {
		t.Fatalf("trace-off must not touch source: %v", err)
	}
}

func TestClaudeSourceTraceHydrateMatchesManifestRangesAndCorrelation(t *testing.T) {
	t.Setenv(claudeSourceTraceEnv, "1")
	path := filepath.Join(claudeSourceShapesDir, "exact-text-replay.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handlers := newTestHandlers(t)
	turnID := ""
	if err := handlers.traceClaudeHydrateRange(
		context.Background(), "claude", "session-trace", "fixture-segment",
		path, 0, int64(len(data)), &turnID,
	); err != nil {
		t.Fatal(err)
	}
	wantRanges := claudeFixtureRecordRanges(data)
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != len(wantRanges) {
		t.Fatalf("trace lines = %d, want %d\n%s", len(lines), len(wantRanges), logs.String())
	}
	var generation string
	for index, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log %d: %v", index, err)
		}
		want := wantRanges[index]
		if entry["msg"] != "go-bridge: claude_source_trace" ||
			entry["phase"] != "hydrate" ||
			entry["ingestDomain"] != "baseline" ||
			int(entry["byteStart"].(float64)) != want.ByteStart ||
			int(entry["byteEnd"].(float64)) != want.ByteEnd {
			t.Fatalf("trace %d = %+v, want range %+v", index, entry, want)
		}
		currentGeneration, _ := entry["segmentGeneration"].(string)
		if currentGeneration == "" {
			t.Fatalf("trace %d missing generation: %+v", index, entry)
		}
		if generation == "" {
			generation = currentGeneration
		} else if currentGeneration != generation {
			t.Fatalf("trace generation changed within one source: %q != %q", currentGeneration, generation)
		}
	}
}
