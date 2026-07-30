package gobridge

// Characterization tests for the Claude source-shape fixtures under
// go-bridge/testdata/claude-source-shapes/ (IR-6).
//
// WHAT THESE TESTS PROVE
//   1. Every fixture is valid Claude JSONL the mapper ingests without error.
//   2. manifest.json is in sync with the on-disk fixtures: each file's sha256, byte size,
//      record count and per-record [byteStart,byteEnd) range recomputed from raw bytes
//      match the manifest exactly. recompute_manifest.py is the offline validator; this
//      test is the in-CI validator.
//   3. The CURRENT (pre-D / pre-H4-fix) mapper output per fixture is locked down, so the
//      documented duplication / mis-attribution bugs are captured as regression baselines.
//
// WHAT THESE TESTS DO NOT PROVE (honesty boundary)
//   - They do NOT assert IR-1a (D) semantics, because D is not yet implemented in the
//     mapper. The "exact replay emits the assistant text twice" assertions below document
//     the CURRENT duplication. When D lands (first-occurrence-stable projection
//     contribution), flip each *_PreD expectation and the test enforces the fix.
//   - They do NOT assert H4 parent-chain attribution, because the H4 fix is not yet
//     implemented. The "a-2 attributed to turn B (file-order)" assertion documents the
//     CURRENT mis-attribution. When H4 lands (parent-chain nearest-user owner), flip it.
//   See docs/2026-07-30-remote-web-…-investigation.md IR-1a/1b/1c + IR-6.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const claudeSourceShapesDir = "testdata/claude-source-shapes"

type manifestRecord struct {
	ByteStart int `json:"byteStart"`
	ByteEnd   int `json:"byteEnd"`
}

type manifestFixture struct {
	File        string           `json:"file"`
	SHA256      string           `json:"sha256"`
	Bytes       int              `json:"bytes"`
	RecordCount int              `json:"recordCount"`
	Records     []manifestRecord `json:"records"`
	Provenance  string           `json:"provenance"`
}

type manifestDocument struct {
	ManifestVersion  int               `json:"manifestVersion"`
	FixtureCreatedAt string            `json:"fixtureCreatedAt"`
	Fixtures         []manifestFixture `json:"fixtures"`
}

// mapClaudeFixture runs the cold-hydrate mapper over one fixture file and returns every
// emitted projectionHydrateEvent in file order.
func mapClaudeFixture(t *testing.T, name string) []projectionHydrateEvent {
	t.Helper()
	path := filepath.Join(claudeSourceShapesDir, name)
	var out []projectionHydrateEvent
	if err := streamClaudeTranscriptProjectionEvents(context.Background(), path, func(ev projectionHydrateEvent) bool {
		out = append(out, ev)
		return true
	}); err != nil {
		t.Fatalf("stream %s: %v", name, err)
	}
	return out
}

// claudeFixtureRecordRanges recomputes per-record [start,end) byte ranges (each record
// spans its trailing LF), matching manifest.json / recompute_manifest.py exactly.
func claudeFixtureRecordRanges(data []byte) []manifestRecord {
	var out []manifestRecord
	pos := 0
	for pos < len(data) {
		nl := bytes.IndexByte(data[pos:], '\n')
		if nl < 0 {
			if pos < len(data) { // trailing line without LF (none of our fixtures, handled anyway)
				out = append(out, manifestRecord{pos, len(data)})
			}
			break
		}
		end := pos + nl + 1
		out = append(out, manifestRecord{pos, end})
		pos = end
	}
	return out
}

// TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest loads manifest.json, recomputes
// sha256 + per-record byte ranges from each on-disk fixture, asserts equality, and runs
// the mapper over every fixture to prove none crashes the ingest path.
func TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest(t *testing.T) {
	docBytes, err := os.ReadFile(filepath.Join(claudeSourceShapesDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var doc manifestDocument
	if err := json.Unmarshal(docBytes, &doc); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, doc.FixtureCreatedAt); err != nil {
		t.Fatalf("manifest fixtureCreatedAt must be a real RFC3339 timestamp: %q (%v)", doc.FixtureCreatedAt, err)
	}

	// every on-disk *.jsonl must be covered by the manifest
	actual := map[string]bool{}
	entries, err := os.ReadDir(claudeSourceShapesDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
			actual[e.Name()] = true
		}
	}

	seen := map[string]bool{}
	for _, fe := range doc.Fixtures {
		seen[fe.File] = true
		data, err := os.ReadFile(filepath.Join(claudeSourceShapesDir, fe.File))
		if err != nil {
			t.Fatalf("%s: %v", fe.File, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != fe.SHA256 {
			t.Errorf("%s sha256 drift: manifest=%s actual=%s", fe.File, fe.SHA256, got)
		}
		if len(data) != fe.Bytes {
			t.Errorf("%s byte-size drift: manifest=%d actual=%d", fe.File, fe.Bytes, len(data))
		}
		ranges := claudeFixtureRecordRanges(data)
		if len(ranges) != fe.RecordCount {
			t.Errorf("%s record-count drift: manifest=%d actual=%d", fe.File, fe.RecordCount, len(ranges))
		} else {
			for i, r := range ranges {
				if r != fe.Records[i] {
					t.Errorf("%s record[%d] drift: manifest=%+v actual=%+v", fe.File, i, fe.Records[i], r)
					break
				}
			}
		}
		scan, err := scanCompleteClaudeRelayEntriesFromReader(bytes.NewReader(data), 0, &claudeRelayScanState{})
		if err != nil {
			t.Fatalf("%s complete-record scan: %v", fe.File, err)
		}
		if scan.Poison != nil || scan.ConsumedBytes != int64(len(data)) || len(scan.Records) != len(fe.Records) {
			t.Errorf("%s scanner/manifest mismatch: consumed=%d bytes=%d records=%d manifest=%d poison=%+v",
				fe.File, scan.ConsumedBytes, len(data), len(scan.Records), len(fe.Records), scan.Poison)
		} else {
			for index, record := range scan.Records {
				want := fe.Records[index]
				if record.ByteStart != int64(want.ByteStart) || record.ByteEnd != int64(want.ByteEnd) {
					t.Errorf("%s scanner record[%d] range=[%d,%d), manifest=[%d,%d)",
						fe.File, index, record.ByteStart, record.ByteEnd, want.ByteStart, want.ByteEnd)
					break
				}
			}
		}
		admitted := make([]claudeTranscriptRelayEntry, 0, len(scan.Entries))
		for _, record := range scan.Records {
			if record.Admitted {
				admitted = append(admitted, record.Entry)
			}
		}
		if !reflect.DeepEqual(admitted, scan.Entries) {
			t.Errorf("%s record envelope changed admitted entry sequence", fe.File)
		}
		// no fixture may masquerade as containing real user content
		if fe.Provenance != "observed-derived-synthetic" {
			t.Errorf("%s provenance must be observed-derived-synthetic, got %q", fe.File, fe.Provenance)
		}
		// mapper must ingest every fixture without panicking
		_ = mapClaudeFixture(t, fe.File)
	}

	for name := range actual {
		if !seen[name] {
			t.Errorf("%s on disk but missing from manifest.json", name)
		}
	}
}

func TestClaudeSourceShapeCorpusOracleFixedFixtureCrossCheck(t *testing.T) {
	command := exec.Command(
		"python3",
		filepath.Join(claudeSourceShapesDir, "recompute_corpus_stats.py"),
		"--root", claudeSourceShapesDir,
		"--expect-h4", "10,1",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("corpus oracle: %v\n%s", err, output)
	}
	text := string(output)
	for _, required := range []string{
		"H3 logicalRecordReuseGroups: 3; physicalOccurrencesInGroups: 6",
		"H4 resolvableAssistantRows: 10; fileOrderOwnerMismatchRows: 1",
		"crossCheck: streaming==indexed",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("corpus oracle output missing %q:\n%s", required, text)
		}
	}
}

// TestClaudeSourceShapeFixture_ExactReplayCurrentlyDuplicates_PreD locks the CURRENT
// duplication: the same assistant UUID replayed under a different parent is emitted once
// per occurrence (here: text_delta "answer part 1" in BOTH turn u-root and turn
// u-followup). D (IR-1a, first-occurrence-stable) will collapse this to exactly one.
func TestClaudeSourceShapeFixture_ExactReplayCurrentlyDuplicates_PreD(t *testing.T) {
	events := mapClaudeFixture(t, "exact-text-replay.jsonl")
	const body = "answer part 1"
	turns := map[string]int{} // turnId -> count of deltas containing the body
	for _, ev := range events {
		if ev.Event != "text_delta" {
			continue
		}
		delta, _ := ev.Data["delta"].(string)
		if !strings.Contains(delta, body) {
			continue
		}
		id, _ := ev.Data["itemId"].(string)
		turns[id]++
	}
	if len(turns) != 2 {
		t.Fatalf("pre-D exact replay: expected the replayed assistant body to land in 2 turns (u-root, u-followup) — the duplication; got %d turns %v. If D is implemented, expect 1 turn.", len(turns), turns)
	}
	total := 0
	for _, n := range turns {
		total += n
	}
	if total != 2 {
		t.Fatalf("pre-D exact replay: expected body emitted 2x total, got %d", total)
	}
}

// TestClaudeSourceShapeFixture_ToolResultExtensionCurrentlyReEmitsFirstResult_PreD locks
// the CURRENT re-emit: occ2 of the same tool_result UUID re-emits tool_finished(call-1)
// even though call-1 was the identical prefix of occ1. D's "verified prefix extension →
// apply only new blocks" rule will make call-1 fire exactly once and call-2 once.
func TestClaudeSourceShapeFixture_ToolResultExtensionCurrentlyReEmitsFirstResult_PreD(t *testing.T) {
	events := mapClaudeFixture(t, "tool-result-extension.jsonl")
	call1, call2 := 0, 0
	for _, ev := range events {
		if ev.Event != "tool_finished" {
			continue
		}
		id, _ := ev.Data["itemId"].(string)
		switch id {
		case "call-1":
			call1++
		case "call-2":
			call2++
		}
	}
	if call1 != 2 {
		t.Fatalf("pre-D tool_result extension: expected tool_finished(call-1) emitted 2x (current re-emit of identical prefix); got %d. When D prefix-extension lands, expect call-1 exactly once.", call1)
	}
	if call2 != 1 {
		t.Fatalf("pre-D tool_result extension: expected tool_finished(call-2) emitted 1x (the genuinely new block); got %d", call2)
	}
}

// TestClaudeSourceShapeFixture_BranchFileOrderCurrentlyMisAttributes_PreH4 locks the
// CURRENT mis-attribution: a-2's parent chain resolves to turn A (a-2→a-1→u-A), but the
// file-order mapper attributes it to turn B (currentTurnID = u-B, the most recent user
// row in file order). H4 (parent-chain nearest-user owner) will attribute it to u-A.
func TestClaudeSourceShapeFixture_BranchFileOrderCurrentlyMisAttributes_PreH4(t *testing.T) {
	events := mapClaudeFixture(t, "branch-fileorder-interleave.jsonl")
	var a2turn string
	for _, ev := range events {
		if ev.Event != "text_delta" {
			continue
		}
		delta, _ := ev.Data["delta"].(string)
		if !strings.Contains(delta, "branched reply") {
			continue
		}
		a2turn, _ = ev.Data["itemId"].(string)
	}
	if a2turn != "u-B" {
		t.Fatalf("pre-H4 branch interleave: expected a-2 attributed to turn 'u-B' (file-order bug; parent-chain says 'u-A'); got %q. When H4 lands, expect 'u-A'.", a2turn)
	}
}

// TestClaudeSourceShapeFixture_ControlRowsCurrentlyInert asserts that top-level rows of
// type attachment / last-prompt / queue-operation contribute NO projection events and do
// not leak their identities into the timeline. They are control-plane transcript rows.
func TestClaudeSourceShapeFixture_ControlRowsCurrentlyInert(t *testing.T) {
	for _, name := range []string{"attachment-replay.jsonl", "last-prompt.jsonl", "queue-operation.jsonl"} {
		events := mapClaudeFixture(t, name)
		for _, ev := range events {
			// attachment/last-prompt/queue rows carry uuids att-1 / no uuid; none may surface
			if id, ok := ev.Data["itemId"].(string); ok && id == "att-1" {
				t.Errorf("%s: control row leaked into projection as itemId=att-1", name)
			}
			if txt, ok := ev.Data["text"].(string); ok && strings.Contains(txt, "queued while busy") {
				t.Errorf("%s: queue-operation content leaked into projection text", name)
			}
		}
	}
}

// TestClaudeSourceShapeFixture_ServerToolUseHasMatchedLifecycle freezes IR-5: server_tool_use
// starts the same tool lifecycle as tool_use and its matching user tool_result finishes it.
func TestClaudeSourceShapeFixture_ServerToolUseHasMatchedLifecycle(t *testing.T) {
	events := mapClaudeFixture(t, "server-tool-use.jsonl")
	started, finished := 0, 0
	for _, ev := range events {
		switch ev.Event {
		case "tool_started":
			started++
		case "tool_finished":
			finished++
		}
	}
	if started != 1 {
		t.Fatalf("server_tool_use: expected exactly 1 tool_started, got %d", started)
	}
	if finished != 1 {
		t.Fatalf("server_tool_use: expected exactly 1 matching tool_finished, got %d", finished)
	}
}

// TestClaudeSourceShapeFixture_ImageBlockTextOnly asserts the image block nested in a
// tool_result content array is dropped by claudeToolResultText (which extracts only sibling
// text), so the emitted tool_finished carries the caption text and NOT the base64 payload.
func TestClaudeSourceShapeFixture_ImageBlockTextOnly(t *testing.T) {
	events := mapClaudeFixture(t, "image-block.jsonl")
	var result string
	for _, ev := range events {
		if ev.Event == "tool_finished" {
			result, _ = ev.Data["toolResult"].(string)
		}
	}
	if !strings.Contains(result, "screenshot of the desktop") {
		t.Fatalf("image fixture: expected tool_finished to carry sibling caption text, got %q", result)
	}
	if strings.Contains(result, "iVBORw0KGgo") {
		t.Fatalf("image fixture: base64 payload leaked into toolResult text: %q", result)
	}
}

// TestClaudeSourceShapeFixture_SystemSubtypes asserts that of the four real system
// subtypes, only compact_boundary projects (as a single system_message); stop_hook_summary,
// api_error and informational are inert to the current timeline.
func TestClaudeSourceShapeFixture_SystemSubtypes(t *testing.T) {
	events := mapClaudeFixture(t, "system-subtypes.jsonl")
	systemMessages := 0
	for _, ev := range events {
		if ev.Event == "system_message" {
			systemMessages++
		}
	}
	if systemMessages != 1 {
		t.Fatalf("system subtypes: expected exactly 1 system_message (from compact_boundary only); got %d", systemMessages)
	}
}
