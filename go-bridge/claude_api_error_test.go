package gobridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func claudeAPIErrorFixture(t *testing.T, assistantJSON string) claudeTranscriptRelayEntry {
	t.Helper()
	user := `{"uuid":"user-1","type":"user","timestamp":"2026-10-16T06:00:00Z","message":{"role":"user","content":[{"type":"text","text":"review"}]}}`
	var userEntry claudeTranscriptRelayEntry
	if err := json.Unmarshal([]byte(user), &userEntry); err != nil {
		t.Fatal(err)
	}
	currentTurn := ""
	_ = claudeEntryToProjectionEvents(userEntry, &currentTurn, nil, nil)

	var entry claudeTranscriptRelayEntry
	if err := json.Unmarshal([]byte(assistantJSON), &entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestClaudeAPIErrorMapsToTurnError(t *testing.T) {
	entry := claudeAPIErrorFixture(t, `{
		"uuid":"assistant-1",
		"parentUuid":"user-1",
		"type":"assistant",
		"timestamp":"2026-10-16T06:00:01Z",
		"isApiErrorMessage":true,
		"apiErrorStatus":401,
		"error":"authentication_failed",
		"requestId":"req_401",
		"message":{
			"role":"assistant",
			"stop_reason":"stop_sequence",
			"content":[{"type":"text","text":"Failed to authenticate. API Error: 401 Invalid bearer token"}]
		}
	}`)
	currentTurn := "user-1"
	events := claudeEntryToProjectionEvents(entry, &currentTurn, nil, nil)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want exactly turn_error", events)
	}
	event := events[0]
	if event.Event != "turn_error" || !event.TurnDone {
		t.Fatalf("event = %+v, want terminal turn_error", event)
	}
	if event.Data["turnId"] != "user-1" ||
		event.Data["message"] != "Failed to authenticate. API Error: 401 Invalid bearer token" ||
		event.Data["reason"] != "claude_api_error" ||
		event.Data["apiErrorStatus"] != 401 ||
		event.Data["requestId"] != "req_401" {
		t.Fatalf("turn_error data = %#v", event.Data)
	}
	for _, forbidden := range []string{"text_delta", "turn_completed"} {
		if strings.Contains(strings.ToLower(strings.Join([]string{event.Event}, " ")), forbidden) {
			t.Fatalf("synthetic API error emitted forbidden event %q", event.Event)
		}
	}
}

func TestClaudeAPIErrorFallsBackToTopLevelError(t *testing.T) {
	entry := claudeAPIErrorFixture(t, `{
		"uuid":"assistant-2",
		"parentUuid":"user-1",
		"type":"assistant",
		"timestamp":"2026-10-16T06:00:02Z",
		"isApiErrorMessage":true,
		"apiErrorStatus":429,
		"error":"rate_limited",
		"message":{"role":"assistant","stop_reason":"stop_sequence","content":[]}
	}`)
	currentTurn := "user-1"
	events := claudeEntryToProjectionEvents(entry, &currentTurn, nil, nil)
	if len(events) != 1 || events[0].Event != "turn_error" {
		t.Fatalf("events = %#v, want one turn_error", events)
	}
	if events[0].Data["message"] != "rate_limited" || events[0].Data["apiErrorStatus"] != 429 {
		t.Fatalf("data = %#v", events[0].Data)
	}
}

func TestClaudeAPIErrorBeforeUserIsSelfKeyed(t *testing.T) {
	entry := claudeAPIErrorFixture(t, `{
		"uuid":"assistant-first",
		"type":"assistant",
		"timestamp":"2026-10-16T06:00:03Z",
		"isApiErrorMessage":true,
		"error":"startup_failed",
		"message":{"role":"assistant","stop_reason":"stop_sequence","content":[]}
	}`)
	currentTurn := ""
	events := claudeEntryToProjectionEvents(entry, &currentTurn, nil, nil)
	if len(events) != 1 || events[0].Event != "turn_error" || events[0].Data["turnId"] != "assistant-first" {
		t.Fatalf("events = %#v, want self-keyed turn_error", events)
	}
}

func TestClaudeLiveControlPlaneSettlesOnTurnError(t *testing.T) {
	h := NewHandlers()
	running := true
	batch := ClaudeSourceRecordBatch{Events: []projectionHydrateEvent{{
		Event: "turn_error",
		Data:  map[string]interface{}{"turnId": "user-1", "message": "401"},
	}}}
	h.deliverClaudeLiveControlPlane(batch, "api-error-live", "claude", &running, 42)
	if running {
		t.Fatal("turn_error left live control plane running")
	}
}

func TestClaudeLegacyRelayTurnErrorSettlesIdle(t *testing.T) {
	h := NewHandlers()
	entry := claudeAPIErrorFixture(t, `{
		"uuid":"assistant-live",
		"parentUuid":"user-1",
		"type":"assistant",
		"timestamp":"2026-10-16T06:00:04Z",
		"isApiErrorMessage":true,
		"apiErrorStatus":401,
		"error":"authentication_failed",
		"message":{"role":"assistant","stop_reason":"stop_sequence","content":[]}
	}`)
	currentTurn := "user-1"
	running := true
	held := []claudeHeldTerminalEvent{}
	h.deliverClaudeLegacyRow(entry, "api-error-legacy", "claude", &currentTurn, &running, 42, &held, nil)
	if running {
		t.Fatal("turn_error left legacy relay running")
	}
	if len(held) != 0 {
		t.Fatalf("turn_error was held as completion: %#v", held)
	}
	assertOutcome(t, h, "api-error-legacy", "failed")
}
