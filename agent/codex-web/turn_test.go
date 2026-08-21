package codexweb

// turn_test.go —— p3-turn 测试：thread/turn 写入口请求形状冻结 + session 控制
// （active turn 跟踪/steer expectedTurnId/interrupt/unsubscribe/fail-closed）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestTurnOpsRequestShapesFrozen(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)

	thread := ThreadInfo{ID: "th-1"}
	capStart := captureParams(s, "thread/start", map[string]any{"thread": thread, "model": "m", "modelProvider": "mockpi"})
	capResume := captureParams(s, "thread/resume", map[string]any{"thread": thread})
	capTurn := captureParams(s, "turn/start", map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})
	capSteer := captureParams(s, "turn/steer", map[string]any{"turnId": "turn-1"})
	capInt := captureParams(s, "turn/interrupt", map[string]any{})
	capUnsub := captureParams(s, "thread/unsubscribe", map[string]any{"status": "unsubscribed"})

	ctx := context.Background()
	if _, _, err := StartThread(ctx, c, StartThreadOptions{Cwd: "/ws"}); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capStart)[0], map[string]any{"cwd": "/ws"})

	if _, _, err := StartThread(ctx, c, StartThreadOptions{Cwd: "/ws", Model: "m", ModelProvider: "mockpi"}); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capStart)[1], map[string]any{"cwd": "/ws", "model": "m", "modelProvider": "mockpi"})

	if _, _, _, err := ResumeThread(ctx, c, "th-1"); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capResume)[0], map[string]any{"threadId": "th-1"})

	if _, _, err := TurnStart(ctx, c, "th-1", []InputPart{TextPart("hi")}, ""); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capTurn)[0], map[string]any{"threadId": "th-1", "input": []any{map[string]any{"type": "text", "text": "hi"}}})

	if _, _, err := TurnStart(ctx, c, "th-1", []InputPart{TextPart("hi")}, "gpt-x"); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capTurn)[1], map[string]any{"threadId": "th-1", "input": []any{map[string]any{"type": "text", "text": "hi"}}, "model": "gpt-x"})

	if _, _, err := TurnSteer(ctx, c, "th-1", "turn-1", []InputPart{TextPart("more")}); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capSteer)[0], map[string]any{
		"threadId": "th-1", "expectedTurnId": "turn-1",
		"input": []any{map[string]any{"type": "text", "text": "more"}},
	})

	if rpcErr := TurnInterrupt(ctx, c, "th-1", "turn-1"); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	expectParams(t, (*capInt)[0], map[string]any{"threadId": "th-1", "turnId": "turn-1"})

	if _, _, err := ThreadUnsubscribe(ctx, c, "th-1"); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capUnsub)[0], map[string]any{"threadId": "th-1"})

	// fail-closed 边界
	if _, _, err := TurnSteer(ctx, c, "th-1", "", []InputPart{TextPart("x")}); err == nil || !strings.Contains(err.Error(), "expectedTurnId") {
		t.Fatalf("无 expectedTurnId 的 steer 必须显式拒绝：%v", err)
	}
	if _, _, err := TurnStart(ctx, c, "th-1", []InputPart{{Type: "image"}}, ""); err == nil || !strings.Contains(err.Error(), "fail closed") {
		t.Fatalf("未采样 input kind 必须显式拒绝：%v", err)
	}
	if _, _, err := TurnStart(ctx, c, "th-1", nil, ""); err == nil {
		t.Fatal("空输入必须拒绝")
	}
}

func TestResumeOwnershipConflictTranslated(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)
	s.mu.Lock()
	s.onSend = func(payload []byte) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(payload, &req)
		if req.Method != "thread/resume" {
			return
		}
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"error": map[string]any{"code": -32600, "message": "thread th-1 already has an active writer"}})
		s.push(string(frame))
	}
	s.mu.Unlock()

	_, oc, _, err := ResumeThread(context.Background(), c, "th-1")
	if err != nil {
		t.Fatal(err)
	}
	if oc == nil || !strings.Contains(oc.Error(), "另一个 Codex app-server") {
		t.Fatalf("resume 冲突必须翻译为 OwnershipConflictError：%v", oc)
	}
}

// newTestSession 构造中央泵架构的测试 session：Agent 持 scripted 连接，
// ensurePump 后按 threadID 注册监听。
func newTestSession(t *testing.T) (*agentSession, *scriptedTransport) {
	t.Helper()
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "t"}
	ep.client = cl
	ag := New(nil)
	ag.endpoint = ep
	ag.ensurePump()
	sess := &agentSession{agent: ag, threadID: "th-1"}
	sess.events = ag.addListener("th-1")
	return sess, s
}

func TestSessionSendTracksActiveTurn(t *testing.T) {
	sess, s := newTestSession(t)
	captureParams(s, "turn/start", map[string]any{"turn": map[string]any{"id": "official-turn-9", "status": "inProgress"}})

	if err := sess.Send("hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := sess.currentTurnForControl(); got != "official-turn-9" {
		t.Fatalf("active turn 应为 turn/start 返回 id：%q", got)
	}

	// turn/completed 后 active 清空（唯一终态真相；turn/completed{id} 缺失时回退 codec 观测）
	s.push(`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"th-1","turn":{"id":"official-turn-9","status":"completed"}}}`)
	ev := waitForEvent(t, sess.events, core.EventResult)
	sess.observeEvent(ev)
	if got := sess.currentTurnForControl(); got != "" {
		t.Fatalf("completed 后不应再有 active turn：%q", got)
	}
}

func TestSessionInterruptAndSteer(t *testing.T) {
	sess, s := newTestSession(t)
	captureParams(s, "turn/start", map[string]any{"turn": map[string]any{"id": "t-active", "status": "inProgress"}})
	capInt := captureParams(s, "turn/interrupt", map[string]any{})
	capSteer := captureParams(s, "turn/steer", map[string]any{"turnId": "t-active"})

	if err := sess.Send("long task", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := sess.CancelTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capInt)[0], map[string]any{"threadId": "th-1", "turnId": "t-active"})

	if _, err := sess.Steer(context.Background(), "extra"); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capSteer)[0], map[string]any{
		"threadId": "th-1", "expectedTurnId": "t-active",
		"input": []any{map[string]any{"type": "text", "text": "extra"}},
	})

	// 无 active turn：interrupt/steer 显式拒绝（不伪造身份）
	sess.mu.Lock()
	sess.activeTurnID = ""
	sess.mu.Unlock()
	sess.agent.mu.Lock()
	sess.agent.liveCodec.setActiveTurn("th-1", "")
	sess.agent.mu.Unlock()
	if err := sess.CancelTurn(context.Background()); err == nil || !strings.Contains(err.Error(), "no active turn") {
		t.Fatalf("无 active turn 的 interrupt 必须拒绝：%v", err)
	}
	if _, err := sess.Steer(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "expectedTurnId") {
		t.Fatalf("无 active turn 的 steer 必须拒绝：%v", err)
	}
}

func TestSessionCloseSharedConnectionNoUnsubscribe(t *testing.T) {
	sess, s := newTestSession(t)
	capUnsub := captureParams(s, "thread/unsubscribe", map[string]any{"status": "unsubscribed"})

	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if len(*capUnsub) != 0 {
		t.Fatalf("共享连接上 per-session Close 不得 unsubscribe（会断流同 thread 其他 session），得 %d 次", len(*capUnsub))
	}
	// 监听者已注销：后续事件不再投递到该 session
	sess.agent.mu.Lock()
	_, has := sess.agent.listeners["th-1"]
	sess.agent.mu.Unlock()
	if has {
		t.Fatal("Close 后监听者应注销")
	}
}

func waitForEvent(t *testing.T, ch <-chan core.Event, want core.EventType) core.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("事件通道关闭，未等到 %s", want)
			}
			if ev.Type == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("3s 内未等到事件 %s", want)
			return core.Event{}
		}
	}
}
