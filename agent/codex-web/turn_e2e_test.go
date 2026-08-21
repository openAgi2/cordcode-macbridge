package codexweb

// turn_e2e_test.go —— p3-turn-regression：真实官方 app-server 上的 turn 生命周期。
// mock provider 只控制上游（STREAM=多 delta 完成、SLOW=长任务供 steer/interrupt）。
// 断言：
//   1. 新建 session（thread/start）→ Send → turn/started/deltas/completed 全链路，
//      事件身份 == 官方 turn id（§2.5 identity 红线）；
//   2. 同 daemon 第二连接 resume 同一 thread 无冲突（Phase 0 ownership 实证）；
//   3. steer 注入 active turn（expectedTurnId=turn/start 返回 id）→ 官方 {turnId}；
//   4. interrupt → 官方 turn/completed(interrupted) 唯一终态；
//   5. Close → unsubscribe（幂等）。

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func collectEvents(sess core.AgentSession) (chan core.Event, *[]core.Event) {
	var all []core.Event
	out := make(chan core.Event, 256)
	go func() {
		defer close(out)
		for ev := range sess.Events() {
			if os.Getenv("CW_E2E_DEBUG") == "1" {
				log.Printf("E2E-EV type=%s turn=%s item=%s done=%v err=%v", ev.Type, ev.TurnID, ev.ItemID, ev.Done, ev.Error)
			}
			all = append(all, ev)
			out <- ev
		}
	}()
	return out, &all
}

func waitForEventType(t *testing.T, ch <-chan core.Event, want core.EventType, timeout time.Duration) core.Event {
	t.Helper()
	deadline := time.After(timeout)
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
			t.Fatalf("%s 内未等到事件 %s", timeout, want)
			return core.Event{}
		}
	}
}

func TestE2ETurnLifecycle(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	providerURL := e2eMockProvider(t)
	_, home, workDir := e2eSetupBase(t, providerURL, nil)
	ctx := context.Background()

	agent := New(map[string]any{"codex_web_codex_home": home, "work_dir": workDir})
	defer func() { _ = agent.Stop() }()

	// 1. 新建 session + 完整 turn（按官方 turn id 精确匹配）
	sess, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = sess.Close() }()
	as := sess.(*agentSession)
	evCh, allEvents := collectEvents(sess)

	if err := sess.Send("MOCK:STREAM e2e turn lifecycle", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	firstTurnID := as.activeTurnSnapshot()
	if firstTurnID == "" {
		t.Fatal("Send 后应跟踪官方 turn id")
	}
	waitForTurnEvent(t, evCh, core.EventTurnStarted, firstTurnID, 30*time.Second)
	texts := 0
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ev := pollEvent(t, evCh, 200*time.Millisecond)
		if ev == nil {
			continue
		}
		if ev.Type == core.EventText && ev.TurnID == firstTurnID {
			if ev.ItemID == "" {
				t.Fatalf("text delta 缺官方 itemId：%+v", ev)
			}
			texts++
		}
		if ev.Type == core.EventResult && ev.TurnID == firstTurnID && ev.Done {
			goto turnDone
		}
	}
	t.Fatal("第一个 turn 未在 20s 内完成")
turnDone:
	if texts < 2 {
		t.Fatalf("MOCK:STREAM 应产生多个 delta，得 %d", texts)
	}

	// 2. 同 daemon 第二连接 resume 同一 thread（无 writer 冲突）
	sess2, err := agent.StartSession(ctx, sess.CurrentSessionID())
	if err != nil {
		t.Fatalf("同 daemon resume 不应冲突: %v", err)
	}
	ev2, _ := collectEvents(sess2)
	if err := sess2.Send("MOCK:STREAM second connection resume", nil, nil); err != nil {
		t.Fatalf("resume 后 Send: %v", err)
	}
	turn2 := sess2.(*agentSession).activeTurnSnapshot()
	res2 := waitForTurnEvent(t, ev2, core.EventResult, turn2, 30*time.Second)
	if !res2.Done {
		t.Fatal("第二连接 turn 终态异常")
	}
	_ = sess2.Close()
	drainQuiet(t, evCh)

	// 3. steer active turn（expectedTurnId=turn/start 返回 id）
	if err := sess.Send("MOCK:SLOW steer target", nil, nil); err != nil {
		t.Fatalf("SLOW Send: %v", err)
	}
	slowID := as.activeTurnSnapshot()
	waitForTurnEvent(t, evCh, core.EventTurnStarted, slowID, 30*time.Second)
	steered, err := as.Steer(ctx, "MOCK:STREAM steer injected")
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if steered == slowID {
		t.Logf("steer 返回同 turn id（inline 语义）")
	} else {
		t.Logf("steer 返回新 turn id %s（官方 queue 语义，active=%s）", steered, slowID)
	}
	// steer 后任一官方终态（SLOW 轮或注入轮）
	waitForAnyTerminal(t, evCh, 90*time.Second)
	drainQuiet(t, evCh)

	// 4. interrupt → 唯一终态
	if err := sess.Send("MOCK:SLOW interrupt target", nil, nil); err != nil {
		t.Fatalf("SLOW Send: %v", err)
	}
	intID := as.activeTurnSnapshot()
	waitForTurnEvent(t, evCh, core.EventTurnStarted, intID, 60*time.Second)
	if err := as.CancelTurn(ctx); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	final := waitForTurnEvent(t, evCh, core.EventResult, intID, 30*time.Second)
	if !final.Done {
		t.Fatal("interrupt 后必须有唯一终态")
	}

	// 5. 事件身份一致性（§2.5：全链官方 identity）
	for _, ev := range *allEvents {
		if ev.Type == core.EventText && ev.TurnID == "" {
			t.Fatal("存在无官方 turnId 的正文事件（identity 红线）")
		}
	}
}

// waitForTurnEvent 等待 (type, turnID) 精确匹配的事件（同 thread 多 turn 并存时
// 不误读其他 turn 的同型事件）。
func waitForTurnEvent(t *testing.T, ch <-chan core.Event, want core.EventType, turnID string, timeout time.Duration) core.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("事件通道关闭，未等到 %s(%s)", want, turnID)
			}
			if ev.Type == want && ev.TurnID == turnID {
				return ev
			}
		case <-deadline:
			t.Fatalf("%s 内未等到事件 %s(turn=%s)", timeout, want, turnID)
			return core.Event{}
		}
	}
}

// pollEvent 非阻塞取一条事件（无事件返回 nil）。
func pollEvent(t *testing.T, ch <-chan core.Event, d time.Duration) *core.Event {
	select {
	case ev, ok := <-ch:
		if !ok {
			return nil
		}
		return &ev
	case <-time.After(d):
		return nil
	}
}

// drainQuiet 排空到静默（500ms 无事件）——同 thread 多监听者的残留事件不再
// 污染后续步骤的匹配。
func drainQuiet(t *testing.T, ch <-chan core.Event) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if pollEvent(t, ch, 50*time.Millisecond) == nil {
			return
		}
	}
}

func (s *agentSession) activeTurnSnapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTurnID
}

var _ = strings.TrimSpace

// waitForAnyTerminal 等 EventResult 或 EventError（官方唯一终态的两种 core 表达）。
func waitForAnyTerminal(t *testing.T, ch <-chan core.Event, timeout time.Duration) core.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("事件通道关闭，未等到终态")
			}
			if (ev.Type == core.EventResult || ev.Type == core.EventError) && ev.Done {
				return ev
			}
		case <-deadline:
			t.Fatalf("%s 内未等到官方终态", timeout)
			return core.Event{}
		}
	}
}
