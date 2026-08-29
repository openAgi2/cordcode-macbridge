package gobridge

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestProjectionRPCTraceCorrelatesReceiveHydrateAndEnqueue(t *testing.T) {
	handlers := NewHandlers()
	handlers.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex",
		SessionID: "trace-session",
		Event:     "turn_started",
		Data:      map[string]interface{}{"turnId": "turn-1"},
		Broadcast: true,
	})
	handlers.eventPublisher.PublishLogical(LogicalEvent{
		BackendID: "codex",
		SessionID: "trace-session",
		Event:     "text_delta",
		Data:      map[string]interface{}{"itemId": "turn-1", "delta": "hello"},
		Broadcast: true,
	})

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)

	params, err := json.Marshal(map[string]interface{}{
		"sessionId": "trace-session",
		"sinceRev":  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := &readFileCaptureConn{}
	msg := WireMessage{
		RequestID: "req_trace_42",
		BackendID: "codex",
		Method:    "get_session_projection",
		Params:    params,
	}
	handlers.handleGetSessionProjection(conn, msg, nil)

	stages := map[string]map[string]interface{}{}
	var performance map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line: %v\n%s", err, line)
		}
		if record["msg"] != "go-bridge: projection_rpc" {
			if record["msg"] == "go-bridge: projection performance metrics" {
				performance = record
			}
			continue
		}
		stage, _ := record["stage"].(string)
		stages[stage] = record
	}
	if performance == nil {
		t.Fatalf("missing projection performance metrics in logs:\n%s", logs.String())
	}
	if performance["responseKind"] != "snapshot" ||
		performance["responseBytes"].(float64) <= 0 ||
		performance["projectionBytes"].(float64) <= 0 ||
		performance["turnCount"] != float64(1) ||
		performance["partCount"] != float64(1) {
		t.Fatalf("projection performance metrics incomplete: %+v", performance)
	}

	for _, stage := range []string{"mac_receive", "hydrate_ready", "response_enqueue"} {
		record, ok := stages[stage]
		if !ok {
			t.Fatalf("missing projection trace stage %q in logs:\n%s", stage, logs.String())
		}
		if record["requestId"] != "req_trace_42" ||
			record["backendID"] != "codex" ||
			record["sessionID"] != "trace-session" {
			t.Fatalf("stage %s lost correlation fields: %+v", stage, record)
		}
	}
	if got := stages["hydrate_ready"]["headRev"]; got != float64(1) {
		t.Fatalf("hydrate_ready headRev = %#v, want 1 (turn_started no longer commits)", got)
	}
	if got := stages["response_enqueue"]["outcome"]; got != "snapshot" {
		t.Fatalf("response_enqueue outcome = %#v, want snapshot", got)
	}
	for _, stage := range []string{"hydrate_ready", "response_enqueue"} {
		record := stages[stage]
		if record["bridgeEpoch"] == "" {
			t.Fatalf("%s omitted bridgeEpoch: %+v", stage, record)
		}
		if got := record["connectionGeneration"]; got != float64(1) {
			t.Fatalf("%s connectionGeneration = %#v, want 1", stage, got)
		}
		if got := record["cutRev"]; got != float64(1) {
			t.Fatalf("%s cutRev = %#v, want 2", stage, got)
		}
	}
}

func TestMeasureProjectionPayloadSeparatesLargeContentCategories(t *testing.T) {
	projection := SessionProjection{
		SessionID: "session",
		SyncRev:   7,
		Execution: ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{{
			TurnID: "turn",
			Status: "completed",
			Assistant: &MessageProjection{
				ID:   "assistant",
				Role: "assistant",
				Parts: []ProjectionPart{
					{Type: "text", Text: "hello"},
					{Type: "reasoning", Text: "thinking"},
					{Type: "tool", ToolResult: map[string]interface{}{"output": "large"}, FileChanges: []interface{}{map[string]interface{}{"path": "a.swift"}}},
				},
			},
		}},
	}

	got := measureProjectionPayload(projection)
	if got.ProjectionBytes <= 0 || got.ExecutionBytes <= 0 || got.TurnsBytes <= 0 {
		t.Fatalf("wire envelope sizes missing: %+v", got)
	}
	if got.TextBytes != len("hello") || got.ReasoningBytes != len("thinking") {
		t.Fatalf("text category sizes = %+v", got)
	}
	if got.ToolResultBytes <= 0 || got.FileChangesBytes <= 0 || got.TurnCount != 1 || got.PartCount != 3 {
		t.Fatalf("structured category sizes incomplete: %+v", got)
	}
}
