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
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
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
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line: %v\n%s", err, line)
		}
		if record["msg"] != "go-bridge: projection_rpc" {
			continue
		}
		stage, _ := record["stage"].(string)
		stages[stage] = record
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
	if got := stages["hydrate_ready"]["headRev"]; got != float64(2) {
		t.Fatalf("hydrate_ready headRev = %#v, want 2", got)
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
		if got := record["cutRev"]; got != float64(2) {
			t.Fatalf("%s cutRev = %#v, want 2", stage, got)
		}
	}
}
