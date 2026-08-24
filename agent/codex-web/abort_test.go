package codexweb

// abort_test.go —— CancelTurnForThread：观察/被动 thread 的 turn/interrupt 直达
// （P0-3）。turnID 来自中央泵 liveCodec 对 turn/started 的观测（含观察连接共享
// 解码）；无活动 turn 时惰性回退官方 thread/read 冷基线（订阅前已运行的 turn
// 官方不重放 turn/started）；两者皆无时 fail closed（不发明 turn id）。

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// TestAgentCancelTurnForThreadObservedTurn 外部 turn 观测 → 官方 turn/interrupt
// 直达（threadId+观测 turnId 原样）。
func TestAgentCancelTurnForThreadObservedTurn(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)

	interruptCalls := captureParams(s, "turn/interrupt", map[string]any{})

	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a := New(nil)
	a.endpoint = ep

	// 模拟泵观测到外部 turn/started（Mac 在共享 daemon 上发起的 turn）。
	evs := a.liveCodec.Decode(Notification{
		Method: "turn/started",
		Params: mustJSON(t, map[string]any{
			"threadId": "th-observe",
			"turn":     map[string]any{"id": "turn-mac-1", "status": "inProgress"},
		}),
	})
	if len(evs) != 1 || evs[0].TurnID != "turn-mac-1" {
		t.Fatalf("观测 turn/started 解码失败：%+v", evs)
	}

	if err := a.CancelTurnForThread(context.Background(), "th-observe"); err != nil {
		t.Fatal(err)
	}
	if len(*interruptCalls) != 1 {
		t.Fatalf("turn/interrupt calls=%d, want 1", len(*interruptCalls))
	}
	expectParams(t, (*interruptCalls)[0], map[string]any{"threadId": "th-observe", "turnId": "turn-mac-1"})
}

// TestAgentCancelTurnForThreadNoActiveTurn 无活动 turn：返回错误且不发
// turn/interrupt（fail closed，不猜 turn id）。
func TestAgentCancelTurnForThreadNoActiveTurn(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)

	interruptCalls := captureParams(s, "turn/interrupt", map[string]any{})

	// thread/read 冷基线兜底：无 inProgress turn → 仍应 fail closed。
	captureParams(s, "thread/read", map[string]any{
		"thread": map[string]any{
			"id": "th-idle",
			"turns": []any{
				map[string]any{"id": "turn-done", "status": "completed"},
			},
		},
	})

	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a := New(nil)
	a.endpoint = ep

	if err := a.CancelTurnForThread(context.Background(), "th-idle"); err == nil {
		t.Fatal("无活动 turn 必须返回错误")
	}
	if len(*interruptCalls) != 0 {
		t.Fatalf("无活动 turn 不应发 turn/interrupt：%d", len(*interruptCalls))
	}
}

// TestAgentCancelTurnForThreadColdBaselineFallback 订阅前已运行的 turn：
// liveCodec 无观测（官方对观察连接不重放 turn/started），thread/read 冷基线的
// inProgress turn 兜底 → turn/interrupt 用官方 turn id 直达。
func TestAgentCancelTurnForThreadColdBaselineFallback(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)

	interruptCalls := captureParams(s, "turn/interrupt", map[string]any{})
	captureParams(s, "thread/read", map[string]any{
		"thread": map[string]any{
			"id": "th-observe",
			"turns": []any{
				map[string]any{"id": "turn-done", "status": "completed"},
				map[string]any{"id": "turn-mac-live", "status": "inProgress"},
			},
		},
	})

	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a := New(nil)
	a.endpoint = ep

	if err := a.CancelTurnForThread(context.Background(), "th-observe"); err != nil {
		t.Fatal(err)
	}
	if len(*interruptCalls) != 1 {
		t.Fatalf("turn/interrupt calls=%d, want 1", len(*interruptCalls))
	}
	expectParams(t, (*interruptCalls)[0], map[string]any{"threadId": "th-observe", "turnId": "turn-mac-live"})
}

// TestPassiveSubscribeSharesLiveCodec 观察连接（Subscribe）解码的 turn/started
// 必须进入 a.liveCodec：被动与中央泵共享同一 codec，否则 iOS 停止观察 turn 时
// CancelTurnForThread 读不到宏端发起的 turn（2026-08-23 真机 "no active turn
// to interrupt" 的 A2 根因）。
func TestPassiveSubscribeSharesLiveCodec(t *testing.T) {
	var mu sync.Mutex
	var peers []*fakePeer
	deps := LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		SocketExists:       func(string) bool { return true },
		DialUDS: func(context.Context, string) (Transport, error) {
			peer := newFakePeer()
			peer.install(happyHandlers())
			peer.on("thread/loaded/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
				return map[string]any{"data": []any{"th-observe"}}, nil
			})
			peer.on("thread/resume", func(_ int64, params json.RawMessage) (any, *fakeRPCError) {
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
			mu.Lock()
			peers = append(peers, peer)
			mu.Unlock()
			return peer, nil
		},
	}
	agent := &Agent{lifecycleDeps: &deps, workDir: "/tmp"}
	// 用独立 codec 断言总线：Subscribe 解码后 a.liveCodec 必须持有该 turn。
	agent.liveCodec = NewLiveCodec()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		mu.Lock()
		snapshot := append([]*fakePeer(nil), peers...)
		mu.Unlock()
		for _, peer := range snapshot {
			_ = peer.Close()
		}
	})
	events, err := agent.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = <-events })

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(peers)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("observer connection not established")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	observer := peers[len(peers)-1]
	mu.Unlock()

	notify, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "turn/started",
		"params":  map[string]any{"threadId": "th-observe", "turn": map[string]any{"id": "turn-mac-2", "status": "inProgress"}},
	})
	observer.out <- notify

	deadline = time.Now().Add(2 * time.Second)
	for {
		if got := agent.liveCodec.ActiveTurn("th-observe"); got == "turn-mac-2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("观察连接解码的 turn/started 未进入共享 liveCodec")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

