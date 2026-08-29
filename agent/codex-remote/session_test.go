package codexremote

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type serverRequestBacklogTransport struct {
	startOnce sync.Once
	closeOnce sync.Once
	started   chan struct{}
	done      chan struct{}
	mu        sync.Mutex
	next      int
}

func newServerRequestBacklogTransport() *serverRequestBacklogTransport {
	return &serverRequestBacklogTransport{started: make(chan struct{}), done: make(chan struct{})}
}

func (t *serverRequestBacklogTransport) Send(payload []byte) error {
	var message struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(payload, &message)
	if message.Method == "thread/list" {
		t.startOnce.Do(func() { close(t.started) })
	}
	return nil
}

func (t *serverRequestBacklogTransport) Recv() ([]byte, error) {
	select {
	case <-t.started:
	case <-t.done:
		return nil, fmt.Errorf("closed")
	}
	t.mu.Lock()
	t.next++
	next := t.next
	t.mu.Unlock()
	if next <= 100 {
		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1000 + next,
			"method":  "item/commandExecution/requestApproval",
			"params":  map[string]any{"threadId": "thread_probe", "turnId": "turn_probe"},
		})
	}
	if next == 101 {
		return json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{"data": []any{}},
		})
	}
	<-t.done
	return nil, fmt.Errorf("closed")
}

func (t *serverRequestBacklogTransport) Close() error {
	t.closeOnce.Do(func() { close(t.done) })
	return nil
}

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

// Official codex app-server-client keeps its local event queue unbounded so
// unread events cannot hide a foreground response. Phase 1 rejects unsupported
// server requests, but must still drain them before the bounded channel fills.
func TestServerRequestBacklogDoesNotBlockRPCResponse(t *testing.T) {
	transport := newServerRequestBacklogTransport()
	cl := NewClient(transport, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/list", map[string]any{"limit": 1})
	if err != nil || rpcErr != nil {
		t.Fatalf("foreground response blocked behind server requests: err=%v rpcErr=%v", err, rpcErr)
	}
	if !json.Valid(raw) {
		t.Fatalf("result is not JSON: %s", raw)
	}
}

func TestBindClientStartsEventPumpForReplacementEpoch(t *testing.T) {
	client1, host1 := LoopbackPair()
	client2, host2 := LoopbackPair()
	cl1 := NewClient(NewStream(client1, "client_probe", "env_desktop", "stream_1"), 1)
	cl2 := NewClient(NewStream(client2, "client_probe", "env_desktop", "stream_2"), 2)
	defer cl2.Close()
	defer host1.Close()

	agent := New(nil)
	agent.BindClient(cl1)
	events := make(chan core.Event, 1)
	agent.addListener("thread_probe", events)
	agent.BindClient(cl2)

	seq := uint64(1)
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "turn/started",
		"params":  map[string]any{"threadId": "thread_probe", "turn": map[string]any{"id": "turn_2"}},
	})
	if err := host2.Write(Envelope{
		Type: typeServerMessage, ClientID: "client_probe", EnvID: "env_desktop",
		StreamID: "stream_2", SeqID: &seq, Message: payload,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != core.EventTurnStarted || event.TurnID != "turn_2" {
			t.Fatalf("replacement epoch event = %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement Client did not get its own event pump")
	}
}

func TestCancelTurnForThreadUsesActiveTurn(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	var interrupted map[string]any
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "turn/interrupt":
			_ = json.Unmarshal(params, &interrupted)
			return map[string]any{}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	_ = agent.codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread_probe","turn":{"id":"turn_live"}}`)})
	if err := agent.CancelTurnForThread(context.Background(), "thread_probe"); err != nil {
		t.Fatal(err)
	}
	if interrupted["threadId"] != "thread_probe" || interrupted["turnId"] != "turn_live" {
		t.Fatalf("interrupt=%v", interrupted)
	}
}

func TestCancelTurnForThreadFallsBackToInProgressHistory(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	var interrupted map[string]any
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "thread/read":
			return map[string]any{
				"thread": map[string]any{
					"id": "thread_probe",
					"turns": []any{
						map[string]any{"id": "turn_old", "status": "completed", "items": []any{}},
						map[string]any{"id": "turn_live", "status": "inProgress", "items": []any{}},
					},
				},
			}, nil
		case "turn/interrupt":
			_ = json.Unmarshal(params, &interrupted)
			return map[string]any{}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	if err := agent.CancelTurnForThread(context.Background(), "thread_probe"); err != nil {
		t.Fatal(err)
	}
	if interrupted["turnId"] != "turn_live" {
		t.Fatalf("interrupt=%v", interrupted)
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

// item/started 的 userMessage 是 live 流里用户消息的唯一来源：官方没有独立
// delta，item/started 已携带完整正文，必须编成 EventUserMessage 进投影，
// 否则 iOS 发出的提示词被吞、回复 append 到上一回合。
func TestCodecItemStartedUserMessage(t *testing.T) {
	c := NewLiveCodec()
	evs := c.Decode(Notification{Method: "item/started", Params: json.RawMessage(`{
		"threadId": "th_1", "turnId": "turn_1",
		"item": {"type": "userMessage", "id": "item_1", "text": "讲个鬼故事"}
	}`)})
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	ev := evs[0]
	if ev.Type != core.EventUserMessage || ev.ThreadID != "th_1" || ev.TurnID != "turn_1" ||
		ev.ItemID != "item_1" || ev.Content != "讲个鬼故事" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestCodecItemStartedUserMessageContentArray(t *testing.T) {
	c := NewLiveCodec()
	evs := c.Decode(Notification{Method: "item/started", Params: json.RawMessage(`{
		"threadId": "th_1", "turnId": "turn_1",
		"item": {"type": "userMessage", "id": "item_1",
			"content": [{"type": "text", "text": "第一段"}, {"type": "text", "text": "第二段"}]}
	}`)})
	if len(evs) != 1 || evs[0].Content != "第一段\n第二段" {
		t.Fatalf("events = %+v", evs)
	}
}

// assistant 正文只走 agentMessage/delta；非 userMessage 或缺 ID 的
// item/started 必须保持静默，避免投影重复。
func TestCodecItemStartedIgnoresNonUserMessage(t *testing.T) {
	c := NewLiveCodec()
	for _, params := range []string{
		`{"threadId": "th_1", "turnId": "turn_1", "item": {"type": "assistantMessage", "id": "item_2", "text": "hi"}}`,
		`{"threadId": "th_1", "item": {"type": "userMessage", "id": "item_1", "text": "缺 turnId"}}`,
		`{"threadId": "th_1", "turnId": "turn_1", "item": {"type": "userMessage", "text": "缺 itemId"}}`,
	} {
		if evs := c.Decode(Notification{Method: "item/started", Params: json.RawMessage(params)}); len(evs) != 0 {
			t.Fatalf("params %s: events = %+v", params, evs)
		}
	}
}
