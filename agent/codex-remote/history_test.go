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
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		if method != "thread/read" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		var p struct {
			ThreadID     string `json:"threadId"`
			IncludeTurns bool   `json:"includeTurns"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ThreadID != "thread_probe" || !p.IncludeTurns {
			return nil, &RPCError{Code: -32602, Message: "bad read"}
		}
		return map[string]any{
			"thread": map[string]any{
				"id": "thread_probe",
				"turns": []any{
					map[string]any{
						"id":        "turn_1",
						"status":    "completed",
						"startedAt": int64(100),
						"items": []any{
							map[string]any{
								"type": "userMessage", "id": "user_1",
								"content": []any{map[string]any{"type": "text", "text": "hello desktop"}},
							},
							map[string]any{"type": "agentMessage", "id": "asst_1", "text": "hi from desktop"},
							map[string]any{"type": "commandExecution", "id": "cmd_1", "command": "ls"},
						},
					},
				},
			},
		}, nil
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
	if rich[1].Role != "assistant" || rich[1].ID != "turn_1" || len(rich[1].Parts) != 1 {
		t.Fatalf("assistant = %+v", rich[1])
	}
	if rich[1].Parts[0]["content"] != "hi from desktop" {
		t.Fatalf("assistant text = %+v", rich[1].Parts[0])
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
