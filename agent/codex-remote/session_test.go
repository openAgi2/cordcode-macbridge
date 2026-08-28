package codexremote

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestVerticalListResumeAndTextDelta(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "thread/list":
			return map[string]any{
				"data": []any{map[string]any{"id": "thread_probe", "name": "probe", "updatedAt": int64(1), "cwd": "/tmp"}},
			}, nil
		case "thread/resume":
			var p struct {
				ThreadID     string `json:"threadId"`
				ExcludeTurns bool   `json:"excludeTurns"`
			}
			_ = json.Unmarshal(params, &p)
			if p.ThreadID != "thread_probe" || !p.ExcludeTurns {
				return nil, &RPCError{Code: -32602, Message: "bad resume"}
			}
			return map[string]any{"thread": map[string]any{"id": "thread_probe", "turns": []any{}}}, nil
		case "turn/start":
			return map[string]any{"turn": map[string]any{"id": "turn_1", "status": "inProgress"}}, nil
		case "turn/interrupt":
			return map[string]any{}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(map[string]any{"work_dir": "/tmp"})
	agent.BindClient(cl)

	ok, detail := agent.InstanceStatus()
	if !ok {
		t.Fatalf("status %q", detail)
	}

	list, err := agent.ListSessions(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != "thread_probe" {
		t.Fatalf("list = %+v err=%v", list, err)
	}

	sess, err := agent.StartSession(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if err := sess.Send("hello", nil, nil); err != nil {
		t.Fatal(err)
	}

	inject := func(method string, params any) {
		seq := time.Now().UnixNano()
		s := uint64(seq)
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
		_ = hostConn.Write(Envelope{
			Type: typeServerMessage, ClientID: "client_probe", EnvID: "env_desktop",
			StreamID: "stream_primary", SeqID: &s, Message: body,
		})
	}
	inject("turn/started", map[string]any{"threadId": "thread_probe", "turn": map[string]any{"id": "turn_1"}})
	deadlineStart := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sess.Events():
			if ev.Type == core.EventTurnStarted {
				if err := sess.(*remoteSession).interrupt(); err != nil {
					t.Fatalf("interrupt: %v", err)
				}
				goto deltas
			}
		case <-deadlineStart:
			t.Fatal("no turn/started")
		}
	}
deltas:
	inject("item/agentMessage/delta", map[string]any{"threadId": "thread_probe", "turnId": "turn_1", "itemId": "i1", "delta": "Hi"})
	inject("turn/completed", map[string]any{"threadId": "thread_probe", "turn": map[string]any{"id": "turn_1", "status": "completed"}})

	got := map[core.EventType]int{core.EventTurnStarted: 1}
	deadline := time.After(2 * time.Second)
	for got[core.EventText] == 0 || got[core.EventResult] == 0 {
		select {
		case ev := <-sess.Events():
			got[ev.Type]++
			if ev.ThreadID != "thread_probe" {
				t.Fatalf("thread %q", ev.ThreadID)
			}
		case <-deadline:
			t.Fatalf("events = %+v", got)
		}
	}
}

func TestCodecUnknownMethodIsCounted(t *testing.T) {
	c := NewLiveCodec()
	if evs := c.Decode(Notification{Method: "thread/status/changed", Params: json.RawMessage(`{}`)}); len(evs) != 0 {
		t.Fatalf("status change must not become a turn event: %+v", evs)
	}
	if c.UnknownMethods()["thread/status/changed"] != 1 {
		t.Fatal("expected unknown counter")
	}
}
