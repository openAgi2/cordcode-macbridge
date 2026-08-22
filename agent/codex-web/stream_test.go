package codexweb

// stream_test.go —— p3-stream 测试：断线韧性（session 不死/后台重连/无合成事件）
// 与 §13.2 帧级指标。

import (
	"errors"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestStreamConnectionLossKeepsSessions §8.3：连接断开不关闭 session 监听者，
// 不合成任何事件；重连后泵重启、事件恢复。
func TestStreamConnectionLossKeepsSessions(t *testing.T) {
	ag := New(nil)
	// Unit test must not re-probe the user's real default ~/.codex daemon after
	// the injected connection closes. Keep reconnect deterministic and fail closed
	// until the test injects ep2 below.
	ag.lifecycleDeps = &LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		RunDaemonStart:     func(string, string) (string, error) { return "", errors.New("offline") },
		SocketExists:       func(string) bool { return false },
	}
	s := newScripted()
	cl := NewClient(s, 1)
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "t"}
	ep.client = cl
	ag.endpoint = ep
	ag.ensurePump()

	sess := &agentSession{agent: ag, threadID: "th-1"}
	sess.events = ag.addListener("th-1")

	// 连接断开（transport 关闭 → Notifications 关闭 → 泵退出）
	_ = s.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ag.mu.Lock()
		running := ag.pumpRunning
		ag.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ag.mu.Lock()
	running := ag.pumpRunning
	ag.mu.Unlock()
	if running {
		t.Fatal("泵应在连接断开后退出")
	}

	// §8.3-6：断线期间不合成成功/完成事件（通道保持打开且无事件）
	select {
	case ev, ok := <-sess.events:
		if ok {
			t.Fatalf("断线期间不得合成事件：%+v", ev)
		} else {
			t.Fatal("session 监听通道不得被断线关闭")
		}
	case <-time.After(200 * time.Millisecond):
		// 无事件 = 正确
	}

	// 重连：注入新连接并重启泵（reconnectLoop 的受控等价物）
	s2 := newScripted()
	cl2 := NewClient(s2, 2)
	ep2 := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "t"}
	ep2.client = cl2
	ag.mu.Lock()
	ag.endpoint = ep2
	ag.mu.Unlock()
	ag.ensurePump()
	s2.push(`{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"th-1","turn":{"id":"t-new"}}}`)
	select {
	case ev := <-sess.events:
		if ev.Type != core.EventTurnStarted || ev.TurnID != "t-new" {
			t.Fatalf("重连后事件应恢复：%+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("重连后事件未恢复")
	}
	_ = cl2.Close()
}

// TestStreamMetricsCollection §13.2 指标：send→started、首 delta、计数/字符/
// 间隔、完成延迟全部落账。
func TestStreamMetricsCollection(t *testing.T) {
	ag := New(nil)
	ag.noteSend("th-1", time.Now().Add(-2*time.Second))
	base := time.Now().Add(-1 * time.Second)

	ag.recordMetrics(core.Event{Type: core.EventTurnStarted, SessionID: "th-1", TurnID: "t1"})
	// 时间可控性：直接改内部时间戳模拟间隔
	ag.metricsMu.Lock()
	m := ag.turnMetrics["th-1/t1"]
	m.StartedAt = base
	m.FirstDeltaAt = base.Add(300 * time.Millisecond)
	m.lastDeltaAt = m.FirstDeltaAt
	ag.metricsMu.Unlock()

	ag.recordMetrics(core.Event{Type: core.EventText, SessionID: "th-1", TurnID: "t1", Content: "12345"})
	ag.recordMetrics(core.Event{Type: core.EventText, SessionID: "th-1", TurnID: "t1", Content: "1234567890"})
	ag.recordMetrics(core.Event{Type: core.EventResult, SessionID: "th-1", TurnID: "t1", Done: true})

	snap := ag.MetricsSnapshot()
	if len(snap) != 1 {
		t.Fatalf("应 1 个 turn 指标：%d", len(snap))
	}
	mt := snap[0]
	if mt.DeltaCount != 2 || mt.DeltaChars != 15 || mt.MaxDeltaChars != 10 {
		t.Fatalf("delta 计数/字符错误：%+v", mt)
	}
	if mt.SendToStarted() <= 0 || mt.SendToFirstDelta() < mt.SendToStarted() {
		t.Fatalf("延迟指标错误：started=%v firstDelta=%v", mt.SendToStarted(), mt.SendToFirstDelta())
	}
	if mt.TurnLatency() <= 0 {
		t.Fatal("完成延迟应大于 0")
	}
}
