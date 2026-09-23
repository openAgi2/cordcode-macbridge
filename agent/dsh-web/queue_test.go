package dshweb

// queue_test.go — S3 排队消息管理（OD-2b=B）：session/updateQueue 的 wire
// 形状（A3b 活体证据钉死：{request:{sessionId,itemId,action:{kind,content?}}}
// → {accepted:true}）+ 官方错误 verbatim 透传（steer-unavailable /
// queue-item-not-found）+ 入参校验。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestUpdateSessionQueueWireShape(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/updateQueue"] = fakeRPCResponse{value: map[string]any{"accepted": true}}

	if err := a.UpdateSessionQueue(context.Background(), "sess-q", "item-1", core.SessionQueueActionEdit, "edited text"); err != nil {
		t.Fatal(err)
	}
	calls := methodCalls(f, "session/updateQueue")
	if len(calls) != 1 {
		t.Fatalf("session/updateQueue calls = %d, want 1", len(calls))
	}
	var payload struct {
		Args struct {
			Request struct {
				SessionID string `json:"sessionId"`
				ItemID    string `json:"itemId"`
				Action    struct {
					Kind    string              `json:"kind"`
					Content []map[string]string `json:"content"`
				} `json:"action"`
			} `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	r := payload.Args.Request
	if r.SessionID != "sess-q" || r.ItemID != "item-1" || r.Action.Kind != "edit" {
		t.Fatalf("args = %+v, want {sessionId:sess-q, itemId:item-1, action.kind:edit}", r)
	}
	if len(r.Action.Content) != 1 || r.Action.Content[0]["type"] != "text" || r.Action.Content[0]["text"] != "edited text" {
		t.Fatalf("edit content = %+v, want one text block", r.Action.Content)
	}

	// remove/steer 不带 content（官方 QueueAction union：仅 edit 有 content）。
	if err := a.UpdateSessionQueue(context.Background(), "sess-q", "item-1", core.SessionQueueActionRemove, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateSessionQueue(context.Background(), "sess-q", "item-1", core.SessionQueueActionSteer, ""); err != nil {
		t.Fatal(err)
	}
	for _, call := range methodCalls(f, "session/updateQueue")[1:] {
		if strings.Contains(string(call), `"content"`) {
			t.Fatalf("remove/steer must not carry content, got %s", call)
		}
	}
}

// 官方错误 verbatim 透传（坑 7）：A3b 活体负例原文。
func TestUpdateSessionQueueErrorPassthrough(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/updateQueue"] = fakeRPCResponse{err: &RPCError{
		Code: "session/steer-unavailable", Message: "current turn no longer accepts steering",
	}}
	err := a.UpdateSessionQueue(context.Background(), "s", "item-1", core.SessionQueueActionSteer, "")
	if err == nil || !strings.Contains(err.Error(), "current turn no longer accepts steering") {
		t.Fatalf("official error text must pass through verbatim, got %v", err)
	}
}

func TestUpdateSessionQueueValidation(t *testing.T) {
	a := &Agent{}
	if err := a.UpdateSessionQueue(context.Background(), "", "i", core.SessionQueueActionRemove, ""); err == nil {
		t.Fatal("empty session id must be rejected")
	}
	if err := a.UpdateSessionQueue(context.Background(), "s", "", core.SessionQueueActionRemove, ""); err == nil {
		t.Fatal("empty item id must be rejected")
	}
	if err := a.UpdateSessionQueue(context.Background(), "s", "i", "bogus", ""); err == nil {
		t.Fatal("unknown action must be rejected")
	}
	if err := a.UpdateSessionQueue(context.Background(), "s", "i", core.SessionQueueActionEdit, "  "); err == nil {
		t.Fatal("whitespace-only edit content must be rejected (official gateway/bad-request mirror)")
	}
}
