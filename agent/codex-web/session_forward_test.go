package codexweb

// session_forward_test.go —— agentSession 事件转发（startEventForward）的
// 回归护栏：activeTurnID 随官方 turn/started 与 turn/completed 事件流更新，
// Close 关闭对外 events 通道（go-bridge relayEvents 据此退出）。
// 2026-08-24 真机：activeTurnID 停留在本端 TurnStart 返回值，外部 turn（共享
// daemon 上其他客户端发起）开始后停止请求报 -32600；Close 后 relay 事件通道
// 永不关闭，agent relay 成为僵尸挡住被动泵，官方 turn/completed 无人摄入。

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newForwardAgentSession(t *testing.T, threadID string) (*agentSession, chan core.Event) {
	t.Helper()
	a := New(nil)
	a.liveCodec = NewLiveCodec()
	raw := make(chan core.Event, 16)
	s := &agentSession{
		agent:     a,
		threadID:  threadID,
		rawEvents: raw,
		quit:      make(chan struct{}),
		events:    make(chan core.Event, 16),
	}
	s.startEventForward()
	t.Cleanup(func() {
		select {
		case <-s.quit:
		default:
			_ = s.Close()
		}
	})
	return s, raw
}

// attachSessionForward 给手工构造的 agentSession 接上完整的监听→转发链（与
// StartSession 一致）。旧测试直接 `sess.events = ag.addListener(th)` 把监听者
// 通道直通为对外通道，绕过了 active turn 维护与 Close 关闭 events 的语义。
func attachSessionForward(s *agentSession) {
	s.rawEvents = s.agent.addListener(s.threadID)
	s.quit = make(chan struct{})
	s.events = make(chan core.Event, 256)
	s.startEventForward()
}

// TestEventForwardMaintainsActiveTurn 事件流驱动 activeTurnID：本端发送后停止
// 指向最新 turn/started（外部 turn 覆盖），turn/completed 后清空。
func TestEventForwardMaintainsActiveTurn(t *testing.T) {
	s, raw := newForwardAgentSession(t, "th-fwd")
	s.mu.Lock()
	s.activeTurnID = "local-turn" // 模拟本端 TurnStart 返回的 id
	s.mu.Unlock()

	raw <- core.Event{Type: core.EventTurnStarted, SessionID: "th-fwd", TurnID: "external-turn", ThreadID: "th-fwd"}
	ev := <-s.events
	if ev.TurnID != "external-turn" {
		t.Fatalf("forwarded event turnID = %q, want external-turn", ev.TurnID)
	}
	if got := s.currentTurnForControl(); got != "external-turn" {
		t.Fatalf("currentTurnForControl = %q, want external-turn（外部 turn 必须覆盖过期 local）", got)
	}

	raw <- core.Event{Type: core.EventResult, SessionID: "th-fwd", TurnID: "external-turn", ThreadID: "th-fwd", Done: true}
	<-s.events
	if got := s.currentTurnForControl(); got != "" {
		t.Fatalf("currentTurnForControl after completion = %q, want empty", got)
	}
}

// TestEventForwardObservedTurnWins 中央泵观测优先：liveCodec 有观测时不用本端
// 过期值（外部 turn 到达中央泵早于本 session 事件转发的场景）。
func TestEventForwardObservedTurnWins(t *testing.T) {
	s, _ := newForwardAgentSession(t, "th-obs-win")
	s.mu.Lock()
	s.activeTurnID = "stale-local"
	s.mu.Unlock()

	notifs := s.agent.liveCodec.Decode(Notification{
		Method: "turn/started",
		Params: json.RawMessage(`{"threadId":"th-obs-win","turn":{"id":"live-observed"}}`),
	})
	if len(notifs) != 1 {
		t.Fatalf("Decode turn/started = %d events, want 1", len(notifs))
	}
	if got := s.currentTurnForControl(); got != "live-observed" {
		t.Fatalf("currentTurnForControl = %q, want live-observed（观测必须优先于过期 local）", got)
	}
	select {
	case ev := <-s.events:
		t.Fatalf("liveCodec.Decode 结果不经 raw，不应进入对外通道: %+v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestSessionCloseClosesEvents Close 关闭对外事件通道：relayEvents 的
// !ok 分支据此退出 agent relay，释放 agentRelayRunning。
func TestSessionCloseClosesEvents(t *testing.T) {
	s, _ := newForwardAgentSession(t, "th-close")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		select {
		case ev, ok := <-s.events:
			if ok {
				t.Fatalf("Close 后不应再收到事件: %+v", ev)
			}
			goto closed
		case <-time.After(time.Millisecond * 10):
			if time.Now().After(deadline) {
				t.Fatal("Close 后 events 通道未在期限内关闭")
			}
		}
	}
closed:
	if !s.closed {
		t.Fatal("session 状态应标记 closed")
	}
}

// TestSessionCloseIdempotent 重复 Close 不 panic、不 double-close。
func TestSessionCloseIdempotent(t *testing.T) {
	s, _ := newForwardAgentSession(t, "th-close-2")
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
