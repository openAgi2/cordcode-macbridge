package codexremote

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	nextSeq := startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "thread/list":
			return map[string]any{
				"data": []any{map[string]any{"id": "thread_probe", "name": "probe", "updatedAt": time.Now().Unix(), "cwd": "/tmp"}},
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
		s := nextSeq()
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

func TestSubscribeLiveReceivesUnattachedThreadEvent(t *testing.T) {
	agent := New(nil)
	client := &Client{}
	agent.mu.Lock()
	agent.client = client
	agent.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	events, err := agent.SubscribeLive(ctx)
	if err != nil {
		t.Fatalf("SubscribeLive: %v", err)
	}
	want := core.Event{Type: core.EventResult, ThreadID: "never-attached", SessionID: "never-attached", TurnID: "turn-live", Done: true}
	agent.dispatchForClient(client, want)
	select {
	case got := <-events:
		if got.ThreadID != want.ThreadID || got.TurnID != want.TurnID || !got.Done {
			t.Fatalf("live event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("unattached thread event did not reach live observer")
	}
	agent.mu.Lock()
	listeners := len(agent.listeners)
	agent.mu.Unlock()
	if listeners != 0 {
		t.Fatalf("SubscribeLive attached %d thread listeners", listeners)
	}
	cancel()
}

func TestAttachLiveCatalogResumesLoadedThreadWithoutHistory(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_live_catalog", "env_desktop", "stream_live_catalog")
	defer stream.Close()

	var mu sync.Mutex
	calls := map[string]int{}
	resumeParams := map[string]map[string]any{}
	nextSeq := startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		mu.Lock()
		defer mu.Unlock()
		calls[method]++
		switch method {
		case "thread/loaded/list":
			return map[string]any{"data": []any{"thread_loaded"}}, nil
		case "thread/resume":
			var request map[string]any
			_ = json.Unmarshal(params, &request)
			resumeParams["thread_loaded"] = request
			return map[string]any{"thread": map[string]any{"id": "thread_loaded"}}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})

	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events, err := agent.SubscribeLive(ctx)
	if err != nil {
		t.Fatalf("SubscribeLive: %v", err)
	}
	if err := agent.AttachLiveCatalog(context.Background()); err != nil {
		t.Fatalf("AttachLiveCatalog: %v", err)
	}
	if err := agent.AttachLiveCatalog(context.Background()); err != nil {
		t.Fatalf("repeat AttachLiveCatalog: %v", err)
	}

	mu.Lock()
	loadedCalls := calls["thread/loaded/list"]
	resumeCalls := calls["thread/resume"]
	var excludeTurns any
	if request := resumeParams["thread_loaded"]; request != nil {
		excludeTurns = request["excludeTurns"]
	}
	mu.Unlock()
	if loadedCalls != 2 || resumeCalls != 1 {
		t.Fatalf("loadedCalls=%d resumeCalls=%d, want 2/1 (idempotent resume)", loadedCalls, resumeCalls)
	}
	if excludeTurns != true {
		t.Fatalf("thread/resume excludeTurns=%v, want true (no history replay)", excludeTurns)
	}

	seq := nextSeq()
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "turn/completed",
		"params": map[string]any{
			"threadId": "thread_loaded",
			"turn":     map[string]any{"id": "turn_live", "status": "completed"},
		},
	})
	if err := hostConn.Write(Envelope{
		Type: typeServerMessage, ClientID: "client_live_catalog", EnvID: "env_desktop",
		StreamID: "stream_live_catalog", SeqID: &seq, Message: payload,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != core.EventResult || event.ThreadID != "thread_loaded" || event.TurnID != "turn_live" || !event.Done {
			t.Fatalf("attached live event = %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attached loaded thread completion did not reach live observer")
	}
}

func TestProjectionAttachReceivesDesktopTurnBeforeAnySend(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_projection", "env_desktop", "stream_projection")
	defer stream.Close()
	nextSeq := startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		if method == "thread/resume" {
			return map[string]any{"thread": map[string]any{"id": "thread_projection"}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: method}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	sess, err := agent.AttachProjectionLiveSession(context.Background(), "thread_projection")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	seq := nextSeq()
	payload := json.RawMessage(`{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"thread_projection","turn":{"id":"turn_external"}}}`)
	if err := hostConn.Write(Envelope{
		Type: typeServerMessage, ClientID: "client_projection", EnvID: "env_desktop",
		StreamID: "stream_projection", SeqID: &seq, Message: payload,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-sess.Events():
			if event.Type == core.EventSessionGoalRecord {
				continue
			}
			if event.Type != core.EventTurnStarted || event.ThreadID != "thread_projection" || event.TurnID != "turn_external" {
				t.Fatalf("external event = %+v", event)
			}
			return
		case <-deadline:
			t.Fatal("projection attachment did not deliver Desktop turn before an iOS send")
		}
	}
}

func TestAttachRejectsMismatchedResumeThreadIdentity(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_projection", "env_desktop", "stream_projection")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		if method == "thread/resume" {
			return map[string]any{"thread": map[string]any{"id": "another-thread"}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: method}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)

	if _, err := agent.AttachProjectionLiveSession(context.Background(), "wanted-thread"); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("error = %v, want identity mismatch", err)
	}
	agent.mu.Lock()
	_, attached := agent.attached["wanted-thread"]
	agent.mu.Unlock()
	if attached {
		t.Fatal("mismatched resume response must not mark the requested thread attached")
	}
}

func TestAttachResumeDoesNotOverrideThreadSettings(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_projection", "env_desktop", "stream_projection")
	defer stream.Close()
	var got map[string]any
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		if method != "thread/resume" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		if err := json.Unmarshal(params, &got); err != nil {
			t.Fatalf("resume params: %v", err)
		}
		return map[string]any{"thread": map[string]any{"id": "thread-settings"}}, nil
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(map[string]any{"work_dir": "/bridge-local-must-not-leak"})
	agent.BindClient(cl)
	sess, err := agent.AttachProjectionLiveSession(context.Background(), "thread-settings")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if len(got) != 2 || got["threadId"] != "thread-settings" || got["excludeTurns"] != true {
		t.Fatalf("resume params = %#v, want only threadId + excludeTurns", got)
	}
	for _, forbidden := range []string{"cwd", "model", "effort", "collaborationMode"} {
		if _, exists := got[forbidden]; exists {
			t.Fatalf("resume unexpectedly overrides %s: %#v", forbidden, got)
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

func TestUnsupportedRemoteServerRequestIsRejectedByOriginalID(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_request")
	defer stream.Close()
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)

	seq := uint64(1)
	request := json.RawMessage(`{"jsonrpc":"2.0","id":77,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread_probe","turnId":"turn_probe"}}`)
	if err := hostConn.Write(Envelope{Type: typeServerMessage, ClientID: "client_probe", EnvID: "env_desktop", StreamID: "stream_request", SeqID: &seq, Message: request}); err != nil {
		t.Fatal(err)
	}
	read := make(chan Envelope, 1)
	go func() {
		for {
			env, err := hostConn.Read()
			if err != nil {
				return
			}
			if env.Type == typeClientMessage && len(env.Message) > 0 {
				read <- env
				return
			}
		}
	}()
	select {
	case env := <-read:
		var response struct {
			ID    json.Number `json:"id"`
			Error *struct {
				Code int64 `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(env.Message, &response); err != nil {
			t.Fatal(err)
		}
		if response.ID.String() != "77" || response.Error == nil || response.Error.Code != -32601 {
			t.Fatalf("rejection=%s", env.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unsupported server request was not rejected")
	}
}

func TestOfficialThreadLifecycleNotificationsSignalCatalogRefresh(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_catalog", "env_desktop", "stream_catalog")
	defer stream.Close()
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	signals := agent.CatalogRefreshSignals()

	seq := uint64(0)
	for _, method := range []string{
		"thread/started", "thread/name/updated", "thread/archived", "thread/unarchived", "thread/deleted",
		"turn/started", "turn/completed",
	} {
		seq++
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "method": method, "params": map[string]any{"threadId": "thread_catalog"},
		})
		if err := hostConn.Write(Envelope{
			Type: typeServerMessage, ClientID: "client_catalog", EnvID: "env_desktop",
			StreamID: "stream_catalog", SeqID: &seq, Message: payload,
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-signals:
		case <-time.After(2 * time.Second):
			t.Fatalf("official %s notification did not wake catalog discovery", method)
		}
	}
	agent.signalCatalogRefresh()
	agent.signalCatalogRefresh()
	select {
	case <-signals:
	default:
		t.Fatal("missing coalesced catalog refresh signal")
	}
	select {
	case <-signals:
		t.Fatal("catalog refresh burst must coalesce")
	default:
	}
}

func TestBindClientStartsEventPumpForReplacementEpoch(t *testing.T) {
	client1, host1 := LoopbackPair()
	client2, host2 := LoopbackPair()
	cl1 := NewClient(NewStream(client1, "client_probe", "env_desktop", "stream_1"), 1)
	cl2 := NewClient(NewStream(client2, "client_probe", "env_desktop", "stream_2"), 2)
	defer cl2.Close()
	defer host1.Close()
	resumed := make(chan string, 1)
	nextSeq := startEnvelopePeer(t, host2, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		if method != "thread/resume" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		var request struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &request)
		resumed <- request.ThreadID
		return map[string]any{"thread": map[string]any{"id": request.ThreadID}}, nil
	})

	agent := New(nil)
	agent.BindClient(cl1)
	events := make(chan core.Event, 1)
	agent.addListener("thread_probe", events)
	agent.BindClient(cl2)
	select {
	case threadID := <-resumed:
		if threadID != "thread_probe" {
			t.Fatalf("replacement epoch resumed %q", threadID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement Client did not re-resume the observed thread")
	}

	seq := nextSeq()
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
		case "thread/turns/list":
			return map[string]any{
				"data": []any{
					map[string]any{"id": "turn_old", "status": "completed", "items": []any{}},
					map[string]any{"id": "turn_live", "status": "inProgress", "items": []any{}},
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

func TestRemoteSessionSteerUsesOfficialTurnIdentity(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_steer")
	defer stream.Close()
	var got map[string]any
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "turn/steer":
			_ = json.Unmarshal(params, &got)
			return map[string]any{"turnId": "turn_after_steer"}, nil
		case "thread/turns/list":
			return map[string]any{"data": []any{map[string]any{"id": "turn_before_steer", "status": "inProgress"}}}, nil
		case "thread/resume":
			return map[string]any{"thread": map[string]any{"id": "thread_probe"}}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	sess, err := agent.StartSession(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	steered, err := sess.(*remoteSession).Steer(context.Background(), "continue")
	if err != nil {
		t.Fatal(err)
	}
	if steered != "turn_after_steer" || got["threadId"] != "thread_probe" || got["expectedTurnId"] != "turn_before_steer" {
		t.Fatalf("steer response=%q params=%v", steered, got)
	}
	input, ok := got["input"].([]any)
	if !ok || len(input) != 1 || input[0].(map[string]any)["text"] != "continue" {
		t.Fatalf("steer input=%v", got["input"])
	}
}

func TestRemoteSessionRejectsUnsampledAttachments(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_attachment")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		if method == "thread/resume" {
			return map[string]any{"thread": map[string]any{"id": "thread_probe"}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: method}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	sess, err := agent.StartSession(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.Send("prompt", []core.ImageAttachment{{}}, nil); err == nil {
		t.Fatal("image input must fail closed until a real Remote sample is frozen")
	}
}

func TestRemoteSessionCompactUsesTypedMethodAndEmptyAck(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_compact")
	defer stream.Close()
	var methodSeen string
	var paramsSeen map[string]any
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		switch method {
		case "thread/resume":
			return map[string]any{"thread": map[string]any{"id": "thread_probe"}}, nil
		case "thread/compact/start":
			methodSeen = method
			_ = json.Unmarshal(params, &paramsSeen)
			return map[string]any{}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	sess, err := agent.StartSession(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.(core.ContextCompactingSession).CompactContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if methodSeen != "thread/compact/start" || len(paramsSeen) != 1 || paramsSeen["threadId"] != "thread_probe" {
		t.Fatalf("compact call = %q %#v", methodSeen, paramsSeen)
	}
}

func TestRemoteSessionCompactRejectsNonEmptyAck(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_compact_bad_ack")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		switch method {
		case "thread/resume":
			return map[string]any{"thread": map[string]any{"id": "thread_probe"}}, nil
		case "thread/compact/start":
			return map[string]any{"turnId": "fabricated"}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	sess, err := agent.StartSession(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.(core.ContextCompactingSession).CompactContext(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected fields") {
		t.Fatalf("error = %v, want strict empty ACK failure", err)
	}
}

func TestRemoteSessionCompactDisconnectAfterSendDoesNotReplay(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_compact_disconnect")
	defer stream.Close()
	var compactCalls int
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		switch method {
		case "thread/resume":
			return map[string]any{"thread": map[string]any{"id": "thread_probe"}}, nil
		case "thread/compact/start":
			compactCalls++
			_ = hostConn.Close()
			return nil, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	sess, err := agent.StartSession(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if err := sess.(core.ContextCompactingSession).CompactContext(context.Background()); err == nil {
		t.Fatal("disconnect after send must remain an unknown/error result")
	}
	if compactCalls != 1 {
		t.Fatalf("compact calls = %d, want exactly one with no automatic replay", compactCalls)
	}
}

func TestCodecUnknownMethodIsCounted(t *testing.T) {
	c := NewLiveCodec()
	if evs := c.Decode(Notification{Method: "future/notification", Params: json.RawMessage(`{}`)}); len(evs) != 0 {
		t.Fatalf("status change must not become a turn event: %+v", evs)
	}
	if c.UnknownMethods()["future/notification"] != 1 {
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
