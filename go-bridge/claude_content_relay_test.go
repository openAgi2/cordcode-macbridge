package gobridge

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClaudeTaskNotificationSyntheticUserIsControlPlaneOnly(t *testing.T) {
	content := `{"type":"user","uuid":"u-root","message":{"role":"user","content":"watch CI"}}` + "\n" +
		`{"type":"assistant","uuid":"a-final","parentUuid":"u-root","message":{"id":"a-final","role":"assistant","content":[{"type":"text","text":"watching"}],"stop_reason":"end_turn"}}` + "\n" +
		`{"type":"queue-operation","operation":"enqueue","content":"<task-notification>queued</task-notification>"}` + "\n" +
		`{"type":"user","uuid":"u-task","parentUuid":"a-final","origin":{"kind":"task-notification"},"message":{"role":"user","content":"<task-notification>\n<task-id>task-1</task-id>\n<status>completed</status>\n</task-notification>"}}` + "\n" +
		`{"type":"assistant","uuid":"a-followup","parentUuid":"u-task","message":{"id":"a-followup","role":"assistant","content":[{"type":"text","text":"CI finished"}],"stop_reason":"end_turn"}}` + "\n"

	// Live scanner must consume the physical row so the source cursor stays contiguous, but
	// must not admit it into the projection transaction.
	scan, err := scanCompleteClaudeRelayEntriesFromReader(strings.NewReader(content), 0, &claudeRelayScanState{})
	if err != nil || scan.Poison != nil {
		t.Fatalf("scan: poison=%+v err=%v", scan.Poison, err)
	}
	if scan.ConsumedBytes != int64(len(content)) {
		t.Fatalf("consumed = %d, want %d", scan.ConsumedBytes, len(content))
	}
	if len(scan.Records) != 5 || scan.Records[2].Admitted || scan.Records[3].Admitted {
		t.Fatalf("control rows admitted: %+v", scan.Records)
	}
	if len(scan.Entries) != 3 {
		t.Fatalf("entries = %d, want 3 visible content rows", len(scan.Entries))
	}

	// Cold hydrate uses a separate range scanner; freeze the same structured-origin policy there.
	path := t.TempDir() + "/session.jsonl"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var events []projectionHydrateEvent
	if err := streamClaudeTranscriptProjectionEvents(context.Background(), path, func(event projectionHydrateEvent) bool {
		events = append(events, event)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	for _, event := range events {
		if event.Event == "user_message" {
			userMessages++
		}
		if text, _ := event.Data["text"].(string); strings.Contains(text, "task-notification") {
			t.Fatalf("task notification leaked into projection event: %+v", event)
		}
		if delta, _ := event.Data["delta"].(string); strings.Contains(delta, "task-notification") {
			t.Fatalf("task notification leaked into projection delta: %+v", event)
		}
	}
	if userMessages != 1 {
		t.Fatalf("user_message count = %d, want only real user prompt; events=%+v", userMessages, events)
	}
	if len(events) < 4 || events[len(events)-2].Event != "text_delta" || events[len(events)-2].Data["itemId"] != "u-root" {
		t.Fatalf("follow-up lost prior visible turn continuity: %+v", events)
	}
}

// TestScanClaudeRelayEntriesFromReaderExtractsBlocks：扫描返回所有 meaningful 记录，
// assistant 的 content block（thinking/text/tool_use）可由 claudeRelayContentBlocks 提取。
func TestScanClaudeRelayEntriesFromReaderExtractsBlocks(t *testing.T) {
	content := `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"t"},{"type":"text","text":"answer"},{"type":"tool_use","name":"Bash","input":{"command":"pwd"},"id":"toolu_x"}]}}` + "\n"
	entries, err := scanClaudeRelayEntriesFromReader(strings.NewReader(content))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2 (user + assistant)", len(entries))
	}
	if entries[0].Type != "user" || entries[1].Type != "assistant" {
		t.Fatalf("entry types = %v, want [user, assistant]", []string{entries[0].Type, entries[1].Type})
	}
	blocks := claudeRelayContentBlocks(entries[1].Message.Content)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3 (thinking/text/tool_use)", len(blocks))
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "t" {
		t.Fatalf("blocks[0] = %+v, want thinking 't'", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "answer" {
		t.Fatalf("blocks[1] = %+v, want text 'answer'", blocks[1])
	}
	if blocks[2].Type != "tool_use" || blocks[2].Name != "Bash" || blocks[2].ID != "toolu_x" {
		t.Fatalf("blocks[2] = %+v, want tool_use Bash toolu_x", blocks[2])
	}
}

func TestScanCompleteClaudeRelayEntriesRetainsPartialTail(t *testing.T) {
	state := claudeRelayScanState{}
	first := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"第一"}]}}` + "\r\n"
	partial := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second"}]}}`
	scan, err := scanCompleteClaudeRelayEntriesFromReader(
		strings.NewReader(first+partial),
		100,
		&state,
	)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scan.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 complete row", len(scan.Entries))
	}
	if len(scan.Records) != 1 ||
		scan.Records[0].ByteStart != 100 ||
		scan.Records[0].ByteEnd != 100+int64(len(first)) {
		t.Fatalf("record range = %+v, want delimiter-inclusive [100,%d)", scan.Records, 100+len(first))
	}
	if scan.ConsumedBytes != int64(len(first)) {
		t.Fatalf("consumed = %d, want %d", scan.ConsumedBytes, len(first))
	}
	if scan.Poison != nil {
		t.Fatalf("partial tail must not be poison: %+v", scan.Poison)
	}
}

func TestScanCompleteClaudeRelayRecordRangesAreDelimiterInclusive(t *testing.T) {
	state := claudeRelayScanState{}
	lf := `{"type":"user","uuid":"lf","message":{"role":"user","content":"é"}}` + "\n"
	crlf := `{"type":"assistant","uuid":"crlf","message":{"role":"assistant","content":"ok"}}` + "\r\n"
	noTail := `{"type":"assistant","uuid":"partial","message":{"role":"assistant","content":"later"}}`
	const base = int64(37)
	scan, err := scanCompleteClaudeRelayEntriesFromReader(
		strings.NewReader(lf+crlf+noTail),
		base,
		&state,
	)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scan.Records) != 2 {
		t.Fatalf("records = %d, want LF and CRLF only", len(scan.Records))
	}
	want := [][2]int64{
		{base, base + int64(len(lf))},
		{base + int64(len(lf)), base + int64(len(lf)+len(crlf))},
	}
	for index, record := range scan.Records {
		if record.ByteStart != want[index][0] || record.ByteEnd != want[index][1] {
			t.Fatalf("record %d range = [%d,%d), want [%d,%d)",
				index, record.ByteStart, record.ByteEnd, want[index][0], want[index][1])
		}
	}
	if scan.ConsumedBytes != int64(len(lf)+len(crlf)) {
		t.Fatalf("consumed = %d, want %d", scan.ConsumedBytes, len(lf)+len(crlf))
	}
}

func TestScanCompleteClaudeRelayEntriesStopsBeforePoison(t *testing.T) {
	state := claudeRelayScanState{}
	valid := `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n"
	poison := `{"type":"assistant",broken}` + "\n"
	after := `{"type":"assistant","message":{"role":"assistant","content":"must not leapfrog"}}` + "\n"
	scan, err := scanCompleteClaudeRelayEntriesFromReader(
		strings.NewReader(valid+poison+after),
		50,
		&state,
	)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scan.Entries) != 1 || scan.Entries[0].Type != "user" {
		t.Fatalf("entries = %+v, want only valid row before poison", scan.Entries)
	}
	if scan.ConsumedBytes != int64(len(valid)) {
		t.Fatalf("consumed = %d, want %d", scan.ConsumedBytes, len(valid))
	}
	if scan.Poison == nil ||
		scan.Poison.ByteStart != 50+int64(len(valid)) ||
		scan.Poison.ByteEnd != 50+int64(len(valid)+len(poison)) {
		t.Fatalf("poison range = %+v", scan.Poison)
	}
}

func TestScanCompleteClaudeRelayEntriesCarriesResumeStateAcrossTicks(t *testing.T) {
	state := claudeRelayScanState{}
	meta := `{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}` + "\n"
	first, err := scanCompleteClaudeRelayEntriesFromReader(strings.NewReader(meta), 0, &state)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 0 || !state.skipNextResumeNoResponse {
		t.Fatalf("first scan = %+v state=%+v", first, state)
	}
	noResponse := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"No response requested."}],"stop_reason":"end_turn"}}` + "\n"
	second, err := scanCompleteClaudeRelayEntriesFromReader(
		strings.NewReader(noResponse),
		first.ConsumedBytes,
		&state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 0 || state.skipNextResumeNoResponse {
		t.Fatalf("second scan = %+v state=%+v", second, state)
	}
}

func TestClaudeFileRelayCompletesStartupPartialRowWithoutLoss(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "startup-partial-row"
	user := `{"type":"user","uuid":"partial-user","message":{"role":"user","content":"prompt"}}`
	path := writeClaudeFileRelayTranscript(t, home, sessionID, user)
	partial := `{"type":"assistant","uuid":"partial-assistant","message":{"id":"partial-msg","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":`
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(partial); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)
	_ = client.readEvents(t, 1) // startup turn_started from the last complete user row
	time.Sleep(30 * time.Millisecond)
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`"end_turn"}}` + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	messages := client.readEvents(t, 3)
	if got := eventNames(messages); got[0] != "text_delta" || got[1] != "turn_completed" || got[2] != "session_state_changed" {
		t.Fatalf("events = %v, want completed partial row exactly once", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	if data["delta"] != "done" {
		t.Fatalf("text delta = %#v, want done", data["delta"])
	}
}

func TestClaudeFileRelayQuarantinesPoisonWithoutLeapingToLaterRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "poison-row"
	user := `{"type":"user","uuid":"poison-user","message":{"role":"user","content":"prompt"}}`
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		user,
	)
	handlers, _, client := startClaudeFileRelayFixture(t, sessionID, true)
	_ = client.readEvents(t, 1)

	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant",broken}`,
		`{"type":"assistant","uuid":"must-not-pass","message":{"id":"blocked","role":"assistant","content":[{"type":"text","text":"must not pass"}],"stop_reason":"end_turn"}}`,
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if handlers.projectionKernel.Status("claude", sessionID).Phase == ProjectionHydrateFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := handlers.projectionKernel.Status("claude", sessionID)
	if status.Phase != ProjectionHydrateFailed || status.Failure == nil ||
		status.Failure.Code != "projection.source_poison_record" || !status.Failure.Retryable {
		t.Fatalf("projection status = %+v, want retryable poison failure", status)
	}

	// A later append changes the source and permits a bounded rescan, but the unchanged poison row
	// is found again and the valid row after it remains unreachable.
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"still-blocked","message":{"id":"blocked-2","role":"assistant","content":[{"type":"text","text":"still blocked"}],"stop_reason":"end_turn"}}`,
	)
	time.Sleep(250 * time.Millisecond)

	repaired := `{"type":"assistant","uuid":"repaired","message":{"id":"repaired","role":"assistant","content":[{"type":"text","text":"repaired"}],"stop_reason":"end_turn"}}`
	if err := os.WriteFile(path, []byte(user+"\n"+repaired+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	messages := client.readEvents(t, 3)
	data, _ := messages[0]["data"].(map[string]any)
	if messages[0]["event"] != "text_delta" || data["delta"] != "repaired" {
		t.Fatalf("poison row was leapfrogged or repair not retried: events=%v firstData=%+v", eventNames(messages), data)
	}
}

// TestClaudeFileRelayEmitsAssistantContentBlocks：Phase 1 loop 级验证——外部 Claude turn
// 进行中按 content block 发 reasoning_delta/thinking、text_delta、tool_started，end_turn→turn_completed。
func TestClaudeFileRelayEmitsAssistantContentBlocks(t *testing.T) {
	home, err := os.MkdirTemp("", "claude-content")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	const sessionID = "claude-content-blocks"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","message":{"role":"user","content":"external prompt"}}`,
	)
	_, _, client := startClaudeFileRelayFixture(t, sessionID, true)
	_ = client.readEvents(t, 1) // startup: turn_started

	// 非 final assistant：thinking + text + tool_use → reasoning_delta, text_delta, tool_started
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"planning"},{"type":"text","text":"hello"},{"type":"tool_use","name":"Bash","input":{"command":"ls"},"id":"toolu_1"}]}}`,
	)
	msgs := client.readEvents(t, 3)
	if msgs[0]["event"] != "reasoning_delta" || msgs[1]["event"] != "text_delta" || msgs[2]["event"] != "tool_started" {
		t.Fatalf("non-final assistant events = %v, want [reasoning_delta, text_delta, tool_started]", msgs)
	}
	toolPayload, ok := msgs[2]["data"].(map[string]any)
	if !ok || toolPayload["toolName"] != "Bash" || toolPayload["itemId"] != "toolu_1" {
		t.Fatalf("tool_started payload = %+v, want toolName=Bash itemId=toolu_1", toolPayload)
	}

	// final assistant end_turn：text_delta("done") + turn_completed + idle
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	)
	msgs = client.readEvents(t, 3)
	if msgs[0]["event"] != "text_delta" || msgs[1]["event"] != "turn_completed" || msgs[2]["event"] != "session_state_changed" {
		t.Fatalf("final assistant events = %v, want [text_delta, turn_completed, session_state_changed]", msgs)
	}
}
