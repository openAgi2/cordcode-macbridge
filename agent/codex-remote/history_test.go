package codexremote

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestHistoryReadMapsUserAndAssistantText(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	var compatFullReads int
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "thread/read":
			var p map[string]any
			_ = json.Unmarshal(params, &p)
			if p["threadId"] != "thread_probe" {
				return nil, &RPCError{Code: -32602, Message: "bad read"}
			}
			if _, ok := p["includeTurns"]; ok {
				compatFullReads++
				return nil, &RPCError{Code: -32602, Message: "includeTurns must not be requested for paginated dispatch"}
			}
			return map[string]any{"thread": map[string]any{"id": "thread_probe", "historyMode": "paginated", "status": "idle"}}, nil
		case "thread/turns/list":
			return map[string]any{
				"data": []any{map[string]any{
					"id": "turn_1", "status": "completed", "startedAt": int64(100), "itemsView": "summary",
					"items": []any{
						map[string]any{"type": "userMessage", "id": "user_1", "content": []any{map[string]any{"type": "text", "text": "hello desktop"}}},
						map[string]any{"type": "agentMessage", "id": "asst_1", "text": "hi from desktop"},
					},
				}},
			}, nil
		case "thread/items/list":
			return map[string]any{
				"data": []any{
					map[string]any{"turnId": "turn_1", "item": map[string]any{"type": "userMessage", "id": "user_1", "content": []any{map[string]any{"type": "text", "text": "hello desktop"}}}},
					map[string]any{"turnId": "turn_1", "item": map[string]any{"type": "agentMessage", "id": "asst_1", "text": "hi from desktop"}},
					map[string]any{"turnId": "turn_1", "item": map[string]any{"type": "commandExecution", "id": "cmd_1", "command": "ls"}},
				},
			}, nil
		}
		return nil, &RPCError{Code: -32601, Message: method}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)

	rich, err := agent.GetRichSessionHistory(context.Background(), "thread_probe", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rich) != 2 {
		t.Fatalf("entries=%d", len(rich))
	}
	if rich[0].Role != "user" || rich[0].Content != "hello desktop" || rich[0].ID != "user_1" {
		t.Fatalf("user = %+v", rich[0])
	}
	if rich[1].Role != "assistant" || rich[1].ID != "turn_1" || len(rich[1].Parts) != 2 {
		t.Fatalf("assistant = %+v", rich[1])
	}
	if rich[1].Parts[0]["content"] != "hi from desktop" {
		t.Fatalf("assistant text = %+v", rich[1].Parts[0])
	}
	if step, ok := rich[1].Parts[1]["step"].(map[string]any); !ok || step["toolName"] != "Bash" {
		t.Fatalf("command item = %+v", rich[1].Parts[1])
	}
	if compatFullReads != 0 {
		t.Fatalf("paginated history must never request includeTurns, got %d", compatFullReads)
	}

	legacy, err := agent.GetSessionHistory(context.Background(), "thread_probe", 0)
	if err != nil || len(legacy) != 2 || legacy[1].Content != "hi from desktop" {
		t.Fatalf("legacy = %+v err=%v", legacy, err)
	}
	if _, ok := interface{}(agent).(core.RichHistoryProvider); !ok {
		t.Fatal("must advertise RichHistoryProvider")
	}
}

func TestHistoryReadFailClosedWithoutClient(t *testing.T) {
	if _, err := New(nil).GetRichSessionHistory(context.Background(), "thread_probe", 0); err == nil {
		t.Fatal("expected not configured")
	}
}

func TestRemoteHistoryMapsOfficialItemVariants(t *testing.T) {
	thread := &remoteThread{ID: "thread_variants", Turns: []remoteTurn{{
		ID: "turn_variants", Status: remoteTurnStatusCompleted,
		Items: []json.RawMessage{
			json.RawMessage(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"hello"},{"type":"image","url":"opaque"}]}`),
			json.RawMessage(`{"type":"reasoning","id":"r1","summary":["summary one","summary two"],"content":["tail that must not duplicate"]}`),
			json.RawMessage(`{"type":"commandExecution","id":"cmd1","command":"printf ok","cwd":"/tmp","status":"completed","aggregatedOutput":"ok","exitCode":0,"durationMs":4}`),
			json.RawMessage(`{"type":"fileChange","id":"patch1","status":"completed","changes":[{"path":"a.txt","kind":{"type":"update","movePath":"b.txt"},"diff":"@@"}]}`),
			json.RawMessage(`{"type":"mcpToolCall","id":"m1","server":"srv","tool":"lookup","status":"completed","arguments":{"q":"x"},"result":{"content":[{"type":"text","text":"hit"}]}}`),
			json.RawMessage(`{"type":"dynamicToolCall","id":"d1","tool":"custom","status":"failed","arguments":{"a":1}}`),
			json.RawMessage(`{"type":"plan","id":"p1","text":"do it"}`),
			json.RawMessage(`{"type":"webSearch","id":"w1","query":"search it"}`),
			json.RawMessage(`{"type":"contextCompaction","id":"c1"}`),
			json.RawMessage(`{"type":"futureItem","id":"future1","payload":{"x":1}}`),
		},
	}}}
	turn := mapRemoteHistoryTurns(thread, 0)[0]
	if turn.UserItemID != "u1" || turn.UserText != "hello" {
		t.Fatalf("user message = %+v", turn)
	}
	if len(turn.Parts) != 7 {
		t.Fatalf("parts=%d, want 7: %+v", len(turn.Parts), turn.Parts)
	}
	if got := turn.Parts[0]["type"]; got != "reasoning" {
		t.Fatalf("reasoning ordering = %v", got)
	}
	if step := turn.Parts[1]["step"].(map[string]any); step["status"] != "completed" || step["output"] != "ok" {
		t.Fatalf("command step = %+v", step)
	}
	patchStep := turn.Parts[2]["step"].(map[string]any)
	changes := patchStep["fileChanges"].([]map[string]any)
	if changes[0]["kind"] != "update" || changes[0]["movePath"] != "b.txt" {
		t.Fatalf("patch changes = %+v", changes)
	}
	if step := turn.Parts[3]["step"].(map[string]any); step["toolName"] != "MCP" || step["title"] != "srv lookup" {
		t.Fatalf("mcp step = %+v", step)
	}
	if step := turn.Parts[4]["step"].(map[string]any); step["toolName"] != "custom" || step["status"] != "failed" {
		t.Fatalf("dynamic step = %+v", step)
	}
	if step := turn.Parts[5]["step"].(map[string]any); step["status"] != "completed" {
		t.Fatalf("plan status = %+v", step)
	}
	if step := turn.Parts[6]["step"].(map[string]any); step["toolName"] != "WebSearch" {
		t.Fatalf("search step = %+v", step)
	}
	if len(turn.SystemNotes) != 1 || turn.SystemNotes[0] != "contextCompaction" {
		t.Fatalf("system notes = %+v", turn.SystemNotes)
	}
	if len(turn.SkippedTypes) != 1 || turn.SkippedTypes[0] != "futureItem" {
		t.Fatalf("unknown types = %+v", turn.SkippedTypes)
	}
}

func TestRemoteHistoryDoesNotGuessTerminalState(t *testing.T) {
	thread := &remoteThread{ID: "thread_status", Turns: []remoteTurn{
		{ID: "failed", Status: remoteTurnStatusFailed, Error: &remoteTurnError{Message: "upstream failed"}},
		{ID: "interrupted", Status: remoteTurnStatusInterrupted},
		{ID: "running", Status: remoteTurnStatusInProgress},
		{ID: "not_loaded", Status: remoteTurnStatusCompleted, ItemsView: remoteTurnItemsViewNotLoaded},
	}}
	turns := mapRemoteHistoryTurns(thread, 0)
	if turns[0].Status != remoteTurnStatusFailed || turns[0].ErrorMessage != "upstream failed" {
		t.Fatalf("failed turn = %+v", turns[0])
	}
	if turns[1].Status != remoteTurnStatusInterrupted || turns[2].Status != remoteTurnStatusInProgress {
		t.Fatalf("terminal status rewrite = %+v", turns)
	}
	if len(turns[3].SkippedTypes) != 1 || turns[3].SkippedTypes[0] != "itemsView:notLoaded" {
		t.Fatalf("notLoaded = %+v", turns[3])
	}
}

func TestRemoteIsSessionActiveUsesOfficialThreadStatus(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_active")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		if method != "thread/read" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		var p map[string]any
		if err := json.Unmarshal(params, &p); err != nil || p["threadId"] != "thread_probe" {
			return nil, &RPCError{Code: -32602, Message: "bad params"}
		}
		if _, ok := p["includeTurns"]; ok {
			return nil, &RPCError{Code: -32602, Message: "activity probe must omit includeTurns"}
		}
		return map[string]any{"thread": map[string]any{"id": "thread_probe", "status": map[string]any{"type": "active"}}}, nil
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	if !agent.IsSessionActive(context.Background(), "thread_probe") {
		t.Fatal("active official status must remain active")
	}
}
