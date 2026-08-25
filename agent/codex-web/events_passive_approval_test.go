package codexweb

// events_passive_approval_test.go —— 观察连接 server request 不忽略（§8.2 产品面）：
// iOS 打开 session 时官方 thread/resume 重放线程 pending 批准请求（thread_lifecycle.rs
// replay_requests_to_connection_for_thread），观察泵必须登记 + 发 permission_request 事件，
// 应答回同一连接（clientForEpoch 按 epoch 路由），serverRequest/resolved 收口。

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// recordPeer 是 fakePeer 的变体：记录从客户端发出的全部帧（供断言应答回原 id）。
type recordPeer struct {
	*fakePeer
	mu  sync.Mutex
	got [][]byte
}

func newRecordPeer() *recordPeer {
	return &recordPeer{fakePeer: newFakePeer()}
}

func (p *recordPeer) Send(payload []byte) error {
	p.mu.Lock()
	p.got = append(p.got, append([]byte(nil), payload...))
	p.mu.Unlock()
	return p.fakePeer.Send(payload)
}

func (p *recordPeer) frames() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.got))
	copy(out, p.got)
	return out
}

// passiveApprovalHarness 组装主连接 + 观察连接（两条独立 transport）并把观察
// 连接交回测试（订阅已建立，startPassiveSubscription 所需的 events 通道有效）。
type passiveApprovalHarness struct {
	agent    *Agent
	observer *recordPeer
	events   <-chan core.Event
	cancel   context.CancelFunc
}

func newPassiveApprovalHarness(t *testing.T) *passiveApprovalHarness {
	t.Helper()
	var (
		mu   sync.Mutex
		main = newFakePeer()
	)
	main.install(happyHandlers())
	main.on("thread/loaded/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
		return map[string]any{"data": []any{}}, nil
	})
	observer := newRecordPeer()
	observer.install(happyHandlers())
	observer.on("thread/loaded/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
		return map[string]any{"data": []any{}}, nil
	})
	observer.on("thread/resume", func(_ int64, params json.RawMessage) (any, *fakeRPCError) {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{
			"thread":        map[string]any{"id": p.ThreadID},
			"model":         "m",
			"modelProvider": "mockpi",
		}, nil
	})
	deps := LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		SocketExists:       func(string) bool { return true },
		DialUDS: func(context.Context, string) (Transport, error) {
			mu.Lock()
			defer mu.Unlock()
			if main != nil {
				peer := main
				main = nil
				return peer, nil
			}
			return observer, nil
		},
	}
	agent := New(nil)
	agent.lifecycleDeps = &deps
	agent.workDir = "/tmp"
	ctx, cancel := context.WithCancel(context.Background())
	events, err := agent.Subscribe(ctx)
	if err != nil {
		cancel()
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = observer.Close()
	})
	return &passiveApprovalHarness{agent: agent, observer: observer, events: events, cancel: cancel}
}

func (h *passiveApprovalHarness) injectServerRequest(t *testing.T, id int, method, params string) {
	t.Helper()
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method,
		"params": json.RawMessage(params),
	})
	if err != nil {
		t.Fatalf("marshal server request: %v", err)
	}
	h.observer.out <- frame
}

func (h *passiveApprovalHarness) injectNotification(t *testing.T, method, params string) {
	t.Helper()
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": method,
		"params": json.RawMessage(params),
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	h.observer.out <- frame
}

func (h *passiveApprovalHarness) waitEvent(t *testing.T, timeout time.Duration, want core.EventType) core.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-h.events:
			if !ok {
				t.Fatalf("passive events channel closed while waiting for %s", want)
			}
			if ev.Type == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for passive event %s", want)
		}
	}
}

func TestPassiveObserverSurfacesReplayedApproval(t *testing.T) {
	h := newPassiveApprovalHarness(t)

	h.injectServerRequest(t, 42, "item/commandExecution/requestApproval",
		`{"threadId":"th-obs","turnId":"turn-1","itemId":"call-1","startedAtMs":1,"command":"echo hi","cwd":"/tmp","reason":"run command"}`)

	ev := h.waitEvent(t, 2*time.Second, core.EventPermissionRequest)
	if ev.SessionID != "th-obs" || ev.RequestID != "th-obs:call-1" {
		t.Fatalf("permission event = %+v, want session th-obs / request th-obs:call-1", ev)
	}
	if ev.ToolName != "Bash" {
		t.Fatalf("tool name = %q, want Bash", ev.ToolName)
	}
	// 登记在观察连接 epoch（客户端 epoch 由 transport 绑定，非 0）。
	it := h.agent.registry.Lookup("th-obs:call-1")
	if it == nil {
		t.Fatal("interaction not registered via observer pump")
	}
	if it.Epoch == 0 {
		t.Fatal("interaction epoch not populated")
	}
}

func TestPassiveObserverApprovalRespondsOnObserverConnection(t *testing.T) {
	h := newPassiveApprovalHarness(t)

	h.injectServerRequest(t, 9, "item/commandExecution/requestApproval",
		`{"threadId":"th-obs","turnId":"turn-1","itemId":"call-9","startedAtMs":1,"command":"cat file","cwd":"/tmp","reason":"read"}`)
	h.waitEvent(t, 2*time.Second, core.EventPermissionRequest)

	if err := h.agent.RespondSessionPermission(context.Background(), "th-obs", "th-obs:call-9", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("RespondSessionPermission: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		found := false
		for _, raw := range h.observer.frames() {
			var frame struct {
				ID     *int64          `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(raw, &frame) != nil || frame.ID == nil {
				continue
			}
			if *frame.ID != 9 {
				continue
			}
			var result struct {
				Decision string `json:"decision"`
			}
			if json.Unmarshal(frame.Result, &result) == nil && result.Decision == "accept" {
				found = true
				break
			}
		}
		if found {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("observer connection missing approval response for id=9; got %s", h.observer.frames())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPassiveObserverApprovalResolvedClosesPending(t *testing.T) {
	h := newPassiveApprovalHarness(t)

	h.injectServerRequest(t, 7, "item/fileChange/requestApproval",
		`{"threadId":"th-obs","turnId":"turn-1","itemId":"call-7","startedAtMs":1,"reason":"write outside workspace"}`)
	h.waitEvent(t, 2*time.Second, core.EventPermissionRequest)
	if h.agent.registry.Lookup("th-obs:call-7") == nil {
		t.Fatal("file approval not registered")
	}

	h.injectNotification(t, "serverRequest/resolved", `{"threadId":"th-obs","requestId":7}`)
	resolved := h.waitEvent(t, 2*time.Second, core.EventPermissionResolved)
	if resolved.RequestID != "th-obs:call-7" {
		t.Fatalf("resolved request id = %q, want th-obs:call-7", resolved.RequestID)
	}
	if it := h.agent.registry.Lookup("th-obs:call-7"); it != nil {
		t.Fatalf("interaction still pending after resolved: %+v", it)
	}
	if !h.agent.registry.ResolvedKnown("th-obs:call-7") {
		t.Fatal("resolved interaction not recorded in history")
	}
}
