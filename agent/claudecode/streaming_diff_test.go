package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func collectEvents(cs *claudeSession, n int) []core.Event {
	var evts []core.Event
	for i := 0; i < n; i++ {
		select {
		case evt, ok := <-cs.events:
			if !ok {
				return evts
			}
			evts = append(evts, evt)
		default:
			return evts
		}
	}
	return evts
}

func collectAllEvents(cs *claudeSession) []core.Event {
	return collectEvents(cs, cap(cs.events))
}

func newTestClaudeSession(t *testing.T) *claudeSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cs := &claudeSession{
		events: make(chan core.Event, 64),
		ctx:    ctx,
	}
	cs.alive.Store(true)
	return cs
}

func feedFixture(t *testing.T, cs *claudeSession, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		var raw map[string]any
		if err := json.Unmarshal([]byte(text), &raw); err != nil {
			t.Fatalf("fixture %s line %d: %v", path, line, err)
		}
		cs.handleReadLoopLine(text)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture %s: %v", path, err)
	}
}

func eventContents(evts []core.Event, typ core.EventType) []string {
	var out []string
	for _, evt := range evts {
		if evt.Type == typ {
			out = append(out, evt.Content)
		}
	}
	return out
}

func TestBaseClaudeInnerArgs_IncludesPartialMessages(t *testing.T) {
	args := baseClaudeInnerArgs(false)
	for _, want := range []string{"--include-partial-messages", "--output-format", "stream-json", "--input-format", "--verbose"} {
		if !slices.Contains(args, want) {
			t.Fatalf("baseClaudeInnerArgs(false) missing %q in %v", want, args)
		}
	}

	args = baseClaudeInnerArgs(true)
	if !slices.Contains(args, "--include-partial-messages") {
		t.Fatalf("baseClaudeInnerArgs(true) missing partial flag: %v", args)
	}
	if slices.Contains(args, "--verbose") {
		t.Fatalf("baseClaudeInnerArgs(true) unexpectedly includes --verbose: %v", args)
	}
}

func TestStreamEventFixture_TextDeltasDoNotRepeatAtCheckpoint(t *testing.T) {
	cs := newTestClaudeSession(t)

	feedFixture(t, cs, "testdata/stream_partial_text.jsonl")

	evts := collectAllEvents(cs)
	texts := eventContents(evts, core.EventText)
	want := []string{"", "Hel", "lo"}
	if len(texts) != len(want) {
		t.Fatalf("text events = %#v, want %#v; all events=%#v", texts, want, evts)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("text[%d] = %q, want %q; all texts=%#v", i, texts[i], want[i], texts)
		}
	}
	for _, evt := range evts {
		if evt.Type == core.EventText && evt.Content == "Hello" {
			t.Fatalf("checkpoint emitted duplicate full text event: %#v", evts)
		}
	}
}

func TestStreamEventFixture_MultiBlockTextAndTool(t *testing.T) {
	cs := newTestClaudeSession(t)

	feedFixture(t, cs, "testdata/stream_partial_multiblock.jsonl")

	evts := collectAllEvents(cs)
	texts := eventContents(evts, core.EventText)
	wantTexts := []string{"Before tool.", "After tool."}
	if len(texts) != len(wantTexts) {
		t.Fatalf("text events = %#v, want %#v; all events=%#v", texts, wantTexts, evts)
	}
	for i := range wantTexts {
		if texts[i] != wantTexts[i] {
			t.Fatalf("text[%d] = %q, want %q", i, texts[i], wantTexts[i])
		}
	}
	var toolEvents []core.Event
	for _, evt := range evts {
		if evt.Type == core.EventToolUse {
			toolEvents = append(toolEvents, evt)
		}
		if evt.Type == core.EventText && evt.Content == "Before tool.\nAfter tool." {
			t.Fatalf("checkpoint unexpectedly emitted joined full text: %#v", evts)
		}
	}
	if len(toolEvents) != 1 || toolEvents[0].ToolName != "Read" {
		t.Fatalf("tool events = %#v, want one Read tool use; all events=%#v", toolEvents, evts)
	}
}

func TestHandleAssistant_StreamedPrefixEmitsOnlyTail(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "Hel"}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "m1", "content": []any{map[string]any{"type": "text", "text": "Hello"}}}})

	evts := collectAllEvents(cs)
	texts := eventContents(evts, core.EventText)
	want := []string{"Hel", "lo"}
	if len(texts) != len(want) {
		t.Fatalf("text events = %#v, want %#v; all events=%#v", texts, want, evts)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("text[%d] = %q, want %q", i, texts[i], want[i])
		}
	}
}

func TestHandleAssistant_CheckpointOnlyBlockStillEmits(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "A"}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "m1", "content": []any{
		map[string]any{"type": "text", "text": "A"},
		map[string]any{"type": "text", "text": "B"},
	}}})

	texts := eventContents(collectAllEvents(cs), core.EventText)
	want := []string{"A", "B"}
	if len(texts) != len(want) {
		t.Fatalf("text events = %#v, want %#v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("text[%d] = %q, want %q", i, texts[i], want[i])
		}
	}
}

func TestHandleAssistant_DivergentCheckpointReplacesFullText(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "Hel"}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "m1", "content": []any{map[string]any{"type": "text", "text": "Hi"}}}})

	evts := collectAllEvents(cs)
	replacements := eventContents(evts, core.EventTextReplace)
	if len(replacements) != 1 || replacements[0] != "Hi" {
		t.Fatalf("replacements = %#v, want [Hi]; all events=%#v", replacements, evts)
	}
	texts := eventContents(evts, core.EventText)
	if len(texts) != 1 || texts[0] != "Hel" {
		t.Fatalf("text events after divergent replace = %#v, want only streamed prefix", texts)
	}
}

func TestHandleAssistant_DivergentMultiBlockReplacesJoinedFullText(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "Before."}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(2), "delta": map[string]any{"type": "text_delta", "text": "Oops"}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "m1", "content": []any{
		map[string]any{"type": "text", "text": "Before."},
		map[string]any{"type": "tool_use", "id": "tool-1", "name": "Read", "input": map[string]any{"file_path": "a"}},
		map[string]any{"type": "text", "text": "After."},
	}}})

	evts := collectAllEvents(cs)
	replacements := eventContents(evts, core.EventTextReplace)
	if len(replacements) != 1 || replacements[0] != "Before.\nAfter." {
		t.Fatalf("replacements = %#v, want joined full text; all events=%#v", replacements, evts)
	}
	if texts := eventContents(evts, core.EventText); len(texts) != 2 {
		t.Fatalf("text events = %#v, want only the two streamed deltas before replace", texts)
	}
}

func TestHandleAssistant_LegacyAssistantDiffStillWorksWithoutStreamEvents(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "assistant-1", "content": []any{map[string]any{"type": "text", "text": "Hel"}}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "assistant-1", "content": []any{map[string]any{"type": "text", "text": "Hello"}}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "assistant-2", "content": []any{map[string]any{"type": "text", "text": "Done."}}}})

	texts := eventContents(collectAllEvents(cs), core.EventText)
	want := []string{"Hel", "lo", "Done."}
	if len(texts) != len(want) {
		t.Fatalf("text events = %#v, want %#v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("text[%d] = %q, want %q", i, texts[i], want[i])
		}
	}
}

func TestHandleAssistant_MixedContentOrder_PreservesCheckpointOrder(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "assistant-1", "content": []any{
		map[string]any{"type": "thinking", "thinking": "先想想"},
		map[string]any{"type": "text", "text": "结果如下"},
		map[string]any{"type": "tool_use", "name": "Read", "id": "tool-1", "input": map[string]any{"file_path": "/tmp/a.go"}},
	}}})

	evts := collectEvents(cs, 3)
	if len(evts) != 3 {
		t.Fatalf("event count = %d, want 3", len(evts))
	}
	want := []core.EventType{core.EventThinking, core.EventText, core.EventToolUse}
	for i := range want {
		if evts[i].Type != want[i] {
			t.Fatalf("event[%d].Type = %q, want %q; events=%#v", i, evts[i].Type, want[i], evts)
		}
	}
}

func TestStreamEvent_MessageIDSwitchResetsBlockState(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "Old"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m2"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "New"}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "m2", "content": []any{map[string]any{"type": "text", "text": "New"}}}})

	texts := eventContents(collectAllEvents(cs), core.EventText)
	want := []string{"Old", "New"}
	if len(texts) != len(want) {
		t.Fatalf("text events = %#v, want %#v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("text[%d] = %q, want %q", i, texts[i], want[i])
		}
	}
}

func TestHistoryDrainingSuppressesStreamAndAssistant(t *testing.T) {
	cs := newTestClaudeSession(t)
	cs.historyDraining.Store(true)

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "history"}}})
	cs.handleAssistant(map[string]any{"message": map[string]any{"id": "m1", "content": []any{map[string]any{"type": "text", "text": "history"}}}})

	if evts := collectAllEvents(cs); len(evts) != 0 {
		t.Fatalf("history drain emitted events: %#v", evts)
	}

	cs.handleResult(map[string]any{"type": "result", "result": "done", "session_id": "test-session"})
	if cs.historyDraining.Load() {
		t.Fatal("historyDraining should be false after first result")
	}

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m2"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "live"}}})
	texts := eventContents(collectAllEvents(cs), core.EventText)
	if len(texts) != 1 || texts[0] != "live" {
		t.Fatalf("post-drain text events = %#v, want [live]", texts)
	}
}

func TestHandleResultAndSystemResetStreamState(t *testing.T) {
	cs := newTestClaudeSession(t)

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "some text"}}})
	cs.handleResult(map[string]any{"type": "result", "result": "done", "session_id": "test-session"})

	if cs.streamState.currentMsgID != "" || len(cs.streamState.streamedTextByIdx) != 0 {
		t.Fatalf("stream state after result = %#v, want reset", cs.streamState)
	}

	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "m2"}}})
	cs.handleStreamEvent(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "other text"}}})
	cs.handleSystem(map[string]any{"type": "system", "session_id": "new-session"})

	if cs.streamState.currentMsgID != "" || len(cs.streamState.streamedTextByIdx) != 0 {
		t.Fatalf("stream state after system = %#v, want reset", cs.streamState)
	}
}
