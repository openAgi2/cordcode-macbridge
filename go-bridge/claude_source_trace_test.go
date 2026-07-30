package gobridge

// Tests for the IR-7 read-only source-row trace (claude_source_trace.go).
//
// These prove three things required by the round-11 IR-7 close bar:
//  1. Off by default → zero log volume, no behavior change in normal operation.
//  2. When enabled, emits one claude_source_trace line per ingested user/assistant record
//     carrying the physical byte range, top-level UUID and file-order turn.
//  3. Enabling the trace does NOT change the projection events the mapper emits — it is a
//     pure side-effect (read-only instrumentation, no timeline behavior change).
//  4. scanClaudeRelayEntriesWithOffsets returns absolute byte ranges that match the on-disk
//     record layout and shift correctly with the base (live seek) offset.

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// captureSlog swaps the default slog handler for an in-memory text handler (Info level)
// for the duration of fn, restoring the previous default on cleanup.
func captureSlog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

// withTrace toggles the trace flag for the duration of a callback, restoring it on return.
func withTrace(t *testing.T, on bool, fn func()) {
	t.Helper()
	prev := claudeSourceTraceEnabled
	claudeSourceTraceEnabled = func() bool { return on }
	defer func() { claudeSourceTraceEnabled = prev }()
	fn()
}

func TestClaudeSourceTrace_OffByDefault(t *testing.T) {
	// The package default already returns false, but set it explicitly to be unambiguous.
	logged := captureSlog(t, func() {
		withTrace(t, false, func() {
			_ = mapClaudeFixture(t, "exact-text-replay.jsonl")
		})
	})
	if strings.Contains(logged, "claude_source_trace") || strings.Contains(logged, "claude_source_hydrate_window") {
		t.Fatalf("trace leaked into logs with flag off (must be zero volume):\n%s", logged)
	}
}

func TestClaudeSourceTrace_OnEmitsPerRecordByteRanges(t *testing.T) {
	logged := captureSlog(t, func() {
		withTrace(t, true, func() {
			_ = mapClaudeFixture(t, "exact-text-replay.jsonl")
		})
	})
	// The replayed assistant UUID a-text-1 occurs twice (both physical occurrences); each
	// must be traced so an investigator can see the duplicate at distinct byte ranges.
	if got := strings.Count(logged, "uuid=a-text-1"); got != 2 {
		t.Fatalf("expected 2 claude_source_trace lines for uuid=a-text-1 (both replay occurrences), got %d\n%s", got, logged)
	}
	// phase must identify the cold-hydrate streamer
	if !strings.Contains(logged, "phase=hydrate") {
		t.Fatalf("trace missing phase=hydrate:\n%s", logged)
	}
	// byte range fields present
	if !strings.Contains(logged, "byteStart=") || !strings.Contains(logged, "byteEnd=") {
		t.Fatalf("trace missing byteStart/byteEnd:\n%s", logged)
	}
}

func TestClaudeSourceTrace_DoesNotChangeProjectionEvents(t *testing.T) {
	// Enabling the trace must not alter the mapper's emitted events (pure side-effect).
	var off, on []projectionHydrateEvent
	captureSlog(t, func() {
		withTrace(t, false, func() { off = mapClaudeFixture(t, "exact-text-replay.jsonl") })
	})
	captureSlog(t, func() {
		withTrace(t, true, func() { on = mapClaudeFixture(t, "exact-text-replay.jsonl") })
	})
	if len(off) != len(on) {
		t.Fatalf("trace changed event count: off=%d on=%d", len(off), len(on))
	}
	for i := range off {
		if !reflect.DeepEqual(off[i], on[i]) {
			t.Fatalf("trace changed event[%d]:\n off=%+v\n on =%+v", i, off[i], on[i])
		}
	}
}

func TestScanClaudeRelayEntriesWithOffsets_ByteRanges(t *testing.T) {
	path := filepath.Join(claudeSourceShapesDir, "exact-text-replay.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanned, err := scanClaudeRelayEntriesWithOffsets(f, 0)
	if err != nil {
		t.Fatal(err)
	}
	// u-root, a-text-1, u-followup, a-text-1 — four meaningful user/assistant records.
	if len(scanned) != 4 {
		t.Fatalf("expected 4 scanned entries, got %d", len(scanned))
	}
	if scanned[0].byteStart != 0 {
		t.Fatalf("first byteStart = %d, want 0 (base=0)", scanned[0].byteStart)
	}
	// strictly increasing, non-overlapping record ranges
	for i := 1; i < len(scanned); i++ {
		if scanned[i].byteStart < scanned[i-1].byteEnd {
			t.Fatalf("entry %d overlaps previous: start=%d < prevEnd=%d", i, scanned[i].byteStart, scanned[i-1].byteEnd)
		}
	}
	// the two a-text-1 occurrences must carry DISTINCT byte ranges (the replay evidence)
	if scanned[1].entry.UUID != "a-text-1" || scanned[3].entry.UUID != "a-text-1" {
		t.Fatalf("expected a-text-1 at positions 1 and 3, got %q / %q", scanned[1].entry.UUID, scanned[3].entry.UUID)
	}
	if scanned[1].byteStart == scanned[3].byteStart {
		t.Fatalf("replay occurrences share byteStart=%d (must be distinct physical ranges)", scanned[1].byteStart)
	}
}

func TestScanClaudeRelayEntriesWithOffsets_BaseShiftsAbsoluteRange(t *testing.T) {
	// The live relay loop seeks to `offset` before reading; base must shift every range.
	path := filepath.Join(claudeSourceShapesDir, "exact-text-replay.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const base = int64(4096)
	scanned, err := scanClaudeRelayEntriesWithOffsets(f, base)
	if err != nil {
		t.Fatal(err)
	}
	if scanned[0].byteStart != base {
		t.Fatalf("with base=%d, first byteStart = %d, want %d", base, scanned[0].byteStart, base)
	}
}
