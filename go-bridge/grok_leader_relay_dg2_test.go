package gobridge

// grok_leader_relay_dg2_test.go 验证 D-G2（设计 §3.5.2，owner 批准的受控 Go 改动 2/2）：
// subscriber-aware cancellation——iOS 持续无订阅者 ≥60s（名义区间 [60s,70s)）后
// grok leader relay 主动取消订阅并下线，不合成 turn_aborted(leader_disconnect)，
// registry 经 generation-token claim 释放（synthetic 删除 / 真实行转 unknown），
// catalog 快照 fence 穿透。
//
// G5 纯 helper 取消区间 + 短接线；G6 订阅抖动不取消不重建；
// G7 armed turn 主动取消负断言 + 真断开 F-7 对照；
// G8 claim 所有权 / unknown 不触发自动终态 / isKnownActive 逐点等价。

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── helpers ─────────────────────────────────────────────────────────────────

// syncLogBuffer collects slog output from concurrent goroutines.
type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// withFastGrokLeaderWatchdog 缩短 D-G2 守望 knobs（测试不动真实 60s）。
func withFastGrokLeaderWatchdog(t *testing.T, poll, cancelAfter time.Duration) {
	t.Helper()
	oldPoll := grokLeaderSubscriberPollInterval
	oldCancel := grokLeaderNoSubscriberCancel
	grokLeaderSubscriberPollInterval = poll
	grokLeaderNoSubscriberCancel = cancelAfter
	t.Cleanup(func() {
		grokLeaderSubscriberPollInterval = oldPoll
		grokLeaderNoSubscriberCancel = oldCancel
	})
}

// captureGrokDG2Slog 捕获默认 slog 输出（INFO 结构化字段断言用）。
func captureGrokDG2Slog(t *testing.T) *syncLogBuffer {
	t.Helper()
	buf := &syncLogBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// grokDG2Subscriber 是带调用计数的可控 SessionEventSubscriber（channel 由测试持有）。
type grokDG2Subscriber struct {
	calls atomic.Int32
	ch    chan core.Event
}

func (s *grokDG2Subscriber) SubscribeSessionEvents(ctx context.Context, sessionID, cwd string) (<-chan core.Event, error) {
	s.calls.Add(1)
	return s.ch, nil
}

func grokLeaderRelayIsRunning(t *testing.T, h *Handlers, sessionID string) bool {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.relayRunning[grokLeaderRelayKey(sessionID)]
}

// expectNoWebsocketEvent 断言窗口内没有任何 wire 帧到达（负断言专用）。
func expectNoWebsocketEvent(t *testing.T, conn *websocket.Conn, window time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(window)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]interface{}
	if err := conn.ReadJSON(&payload); err == nil {
		t.Fatalf("unexpected wire frame within %v: %v", window, payload)
	}
}

// stateChangeRecorder 记录 onStateChange 观察（断言 unknown/主动取消不触发 idle 链）。
type stateChangeRecorder struct {
	mu     sync.Mutex
	states []string
}

func (r *stateChangeRecorder) record(backendID, sessionID, newState string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, newState)
}

func (r *stateChangeRecorder) has(newState string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.states {
		if s == newState {
			return true
		}
	}
	return false
}

// runtimeStateBuilder 返回读取 registry 的 catalog builder：目标行 runtimeState 经
// applyListRuntimeState 从 registry 派生（生产 page-0 富化路径同一函数），filler 行
// 保证 hasMore/nextCursor 可用。builder 调用计数证明 fence 后确实重建。
func runtimeStateBuilder(h *Handlers, sid string, calls *int) func() ([]map[string]interface{}, error) {
	return func() ([]map[string]interface{}, error) {
		*calls++
		row := map[string]interface{}{"id": sid, "updatedAtMillis": int64(2_000_000)}
		h.applyListRuntimeState(row, nil)
		return []map[string]interface{}{
			row,
			{"id": "filler-1", "updatedAtMillis": int64(1_000_000)},
		}, nil
	}
}

func grokDG2RegistryState(t *testing.T, h *Handlers, sid string) (sessionState, core.AgentSession, bool) {
	t.Helper()
	ts, ok := h.sessions.get(sid)
	if !ok {
		return "", nil, false
	}
	return ts.state, ts.session, true
}

// ── G5：纯 helper 取消区间 + 短接线 ─────────────────────────────────────────

// TestGrokDG2_G5_AnchorStateMachine 直接驱动 §3.5.2 冻结的纯 helper（无时钟无 IO）：
// 首个负样本设锚点；连续负样本不移动锚点；59.9s 不取消、≥60s 取消；正样本清零。
// 断言一律以订阅者实际消失时刻（首个负样本）起算。
func TestGrokDG2_G5_AnchorStateMachine(t *testing.T) {
	t0 := time.UnixMilli(1_000_000)

	// 零锚点 + 负样本 → 记 now，不取消。
	next, cancel := grokSubscriberAnchor(time.Time{}, t0, false)
	if !next.Equal(t0) || cancel {
		t.Fatalf("first negative: next=%v cancel=%v, want anchor=t0 no-cancel", next, cancel)
	}

	anchor := t0
	// 59.9s 不取消（含边界观察点）。
	next, cancel = grokSubscriberAnchor(anchor, t0.Add(59_900*time.Millisecond), false)
	if !next.Equal(anchor) || cancel {
		t.Fatalf("59.9s: next=%v cancel=%v, want anchor unchanged no-cancel", next, cancel)
	}
	// 连续第二/第三个负样本不移动锚点。
	next, cancel = grokSubscriberAnchor(anchor, t0.Add(10*time.Second), false)
	if !next.Equal(anchor) {
		t.Fatalf("2nd negative moved anchor: %v", next)
	}
	next, cancel = grokSubscriberAnchor(anchor, t0.Add(20*time.Second), false)
	if !next.Equal(anchor) {
		t.Fatalf("3rd negative moved anchor: %v", next)
	}
	// ≥60s → 取消（[60s,70s) 名义窗口的下界）。
	next, cancel = grokSubscriberAnchor(anchor, t0.Add(60*time.Second), false)
	if !next.Equal(anchor) || !cancel {
		t.Fatalf("60s: next=%v cancel=%v, want anchor unchanged cancel", next, cancel)
	}
	// 正样本 → 清零。
	next, cancel = grokSubscriberAnchor(anchor, t0.Add(70*time.Second), true)
	if !next.IsZero() || cancel {
		t.Fatalf("positive: next=%v cancel=%v, want zeroed anchor no-cancel", next, cancel)
	}
}

// TestGrokDG2_G5_ShortCircuit 短接线：无订阅者 → ticker→helper→cancel→清理全链。
// relay 退出、relayRunning 清理、日志可见取消原因（结构化字段）。
func TestGrokDG2_G5_ShortCircuit(t *testing.T) {
	withFastGrokLeaderWatchdog(t, 15*time.Millisecond, 60*time.Millisecond)
	logs := captureGrokDG2Slog(t)
	const sid = "dg2-g5-short"
	serverConn, _, cleanup := openTestConn(t)
	defer cleanup()
	_ = serverConn

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	// 不订阅任何 conn —— 每个采样都是负样本。

	sub := &grokDG2Subscriber{ch: make(chan core.Event, 1)}
	relayKey := grokLeaderRelayKey(sid)
	handlers.mu.Lock()
	handlers.relayRunning[relayKey] = true
	handlers.mu.Unlock()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub, relayKey, "/tmp")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not self-cancel without subscribers")
	}
	elapsed := time.Since(start)
	// 取消阈值 60ms + 采样粒度 15ms → 名义 [60ms,75ms)；放宽调度余量到 10 倍阈值。
	if elapsed > 600*time.Millisecond {
		t.Fatalf("self-cancel took %v, want bounded ≈ [60ms,75ms)", elapsed)
	}
	if grokLeaderRelayIsRunning(t, handlers, sid) {
		t.Fatal("relayRunning entry not cleaned after self-cancel")
	}
	got := logs.String()
	for _, want := range []string{
		"no subscribers, cancelling subscription",
		"reason=no_subscribers",
		"registryOutcome=noop", // 从未 claim（零事件）→ no-op 释放
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing log %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "leader disconnect with armed turn") {
		t.Fatalf("self-cancel must not run the F-7 disconnect path:\n%s", got)
	}
}

// ── G6：订阅抖动不取消不重建 ────────────────────────────────────────────────

// TestGrokDG2_G6_SubscriberFlapKeepsRelay：订阅在阈值内消失又重现（快速切页抖动）
// → 锚点清零、observer 不取消、relay 不重建（SubscribeSessionEvents 只调一次）。
func TestGrokDG2_G6_SubscriberFlapKeepsRelay(t *testing.T) {
	withFastGrokLeaderWatchdog(t, 15*time.Millisecond, 200*time.Millisecond)
	const sid = "dg2-g6-flap"
	serverConn, _, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})

	sub := &grokDG2Subscriber{ch: make(chan core.Event, 1)}
	relayKey := grokLeaderRelayKey(sid)
	handlers.mu.Lock()
	handlers.relayRunning[relayKey] = true
	handlers.mu.Unlock()
	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub, relayKey, "/tmp")
		close(done)
	}()

	// 消失 ~80ms（≥2 个负样本 tick，远低于 200ms 阈值）→ 重现。
	handlers.broadcaster.UnsubscribeAll(serverConn)
	time.Sleep(80 * time.Millisecond)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})

	// 若锚点未清零，原首个负样本起 ~200-215ms 就会取消；这里等到 300ms 仍必须在跑。
	time.Sleep(300 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("relay cancelled despite subscriber flap (anchor must clear on positive sample)")
	default:
	}
	if !grokLeaderRelayIsRunning(t, handlers, sid) {
		t.Fatal("relayRunning entry lost during flap")
	}

	// 真正持续无订阅者 → 取消（锚点从新首个负样本重新起算）。
	handlers.broadcaster.UnsubscribeAll(serverConn)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not cancel after sustained no-subscriber window")
	}
	if n := sub.calls.Load(); n != 1 {
		t.Fatalf("SubscribeSessionEvents calls = %d, want 1 (no relay rebuild during flap)", n)
	}
}

// ── G7：armed turn 主动取消负断言 ────────────────────────────────────────────

// TestGrokDG2_G7_SelfCancelSyntheticRowCASDeleted：armed turn（synthetic 行，
// session==nil）主动取消 → 不合成 turn_aborted / 不 markIdle / CAS 删除条目 /
// fence 穿透预热快照（page-0 重建输出 unknown、旧 cursor stale）/ 重开重建真值。
func TestGrokDG2_G7_SelfCancelSyntheticRowCASDeleted(t *testing.T) {
	withFastGrokLeaderWatchdog(t, 15*time.Millisecond, 60*time.Millisecond)
	logs := captureGrokDG2Slog(t)
	const sid = "dg2-g7-syn"
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	rec := &stateChangeRecorder{}
	old := handlers.sessions.onStateChange
	handlers.sessions.onStateChange = func(b, s, n string) {
		rec.record(b, s, n)
		if old != nil {
			old(b, s, n)
		}
	}
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})

	sub := &grokDG2Subscriber{ch: make(chan core.Event, 4)}
	relayKey := grokLeaderRelayKey(sid)
	handlers.mu.Lock()
	handlers.relayRunning[relayKey] = true
	handlers.mu.Unlock()
	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub, relayKey, "/tmp")
		close(done)
	}()

	// armed turn：首个内容事件 → turn_started + running + text_delta。
	sub.ch <- core.Event{Type: core.EventText, Content: "working", TurnID: "prompt-g7"}
	names := readEventNames(t, clientConn, 3)
	if names[0] != "turn_started" || names[1] != "session_state_changed" || names[2] != "text_delta" {
		t.Fatalf("arm events = %v, want [turn_started session_state_changed text_delta]", names)
	}
	state, sess, ok := grokDG2RegistryState(t, handlers, sid)
	if !ok || state != sessionStateRunning || sess != nil {
		t.Fatalf("after arm: state=%v session=%v ok=%v, want synthetic running row", state, sess, ok)
	}

	// 预热 running 快照（page-0 富化从 registry 读到 running）。
	scope := grokCatalogScopeKey("grokbuild")
	buildCalls := 0
	builder := runtimeStateBuilder(handlers, sid, &buildCalls)
	p0, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, "", 1, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("warm page-0: err=%v staleErr=%v", err, staleErr)
	}
	rows := p0["sessions"].([]map[string]interface{})
	if rows[0]["runtimeState"] != "running" {
		t.Fatalf("warm snapshot runtimeState = %v, want running", rows[0]["runtimeState"])
	}
	warmCursor, _ := p0["nextCursor"].(string)
	if warmCursor == "" {
		t.Fatal("warm page-0 must produce nextCursor for the stale assertion")
	}

	// 订阅者消失 → 主动取消。
	handlers.broadcaster.UnsubscribeAll(serverConn)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not self-cancel")
	}

	// 负断言：不合成 turn_aborted（wire 无帧）、不 markIdle（registry 无 idle 行）。
	expectNoWebsocketEvent(t, clientConn, 200*time.Millisecond)
	if _, _, ok := grokDG2RegistryState(t, handlers, sid); ok {
		t.Fatal("synthetic row must be CAS-deleted on self-cancel (markIdle would have left an idle row)")
	}
	if rec.has(string(sessionStateIdle)) {
		t.Fatalf("self-cancel must not fire idle state change (completeBridgeTurn trigger): %v", rec.states)
	}
	got := logs.String()
	for _, want := range []string{
		"no subscribers, cancelling subscription",
		"reason=no_subscribers",
		"registryOutcome=deleted",
		"claimReleased=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing log %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "leader disconnect with armed turn") {
		t.Fatalf("self-cancel must not synthesize turn_aborted(leader_disconnect):\n%s", got)
	}

	// fence 穿透：预热快照被清（Peek nil）；存量 cursor 先于任何重建请求 →
	// cursor_stale（nil 快照）；page-0 重建输出 unknown（synthetic 删除后
	// registry miss → F-8「不知道就不亮灯」）。
	if handlers.grokCatalogWireCache().Peek(scope) != nil {
		t.Fatal("fence did not clear the warm grokbuild snapshot")
	}
	if _, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, warmCursor, 1, builder); err != nil || staleErr == nil || staleErr.Code != "cursor_stale" {
		t.Fatalf("warm cursor after fence: err=%v staleErr=%v, want cursor_stale", err, staleErr)
	}
	before := buildCalls
	p0b, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, "", 1, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("rebuild page-0: err=%v staleErr=%v", err, staleErr)
	}
	if buildCalls == before {
		t.Fatal("page-0 after fence must rebuild, not reuse the warm snapshot")
	}
	rows = p0b["sessions"].([]map[string]interface{})
	if rows[0]["runtimeState"] != "unknown" {
		t.Fatalf("rebuilt runtimeState = %v, want unknown (no sticky running badge)", rows[0]["runtimeState"])
	}

	// 重开模拟（新连接 = iOS 重新打开 session）：冷拉 + 新 relay → turn 仍在跑则
	// claimRunning 重建徽标；终态后 idle。
	serverConn2, clientConn2, cleanup2 := openTestConn(t)
	defer cleanup2()
	handlers.broadcaster.Subscribe(serverConn2, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})
	sub2 := &grokDG2Subscriber{ch: make(chan core.Event, 4)}
	done2 := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub2, relayKey, "/tmp")
		close(done2)
	}()
	sub2.ch <- core.Event{Type: core.EventText, Content: "still running", TurnID: "prompt-g7"}
	_ = readEventNames(t, clientConn2, 3)
	if state, _, ok := grokDG2RegistryState(t, handlers, sid); !ok || state != sessionStateRunning {
		t.Fatalf("after reopen: state=%v ok=%v, want running (claim rebuilds badge)", state, ok)
	}
	sub2.ch <- core.Event{Type: core.EventResult, Done: true, TurnID: "prompt-g7"}
	_ = readEventNames(t, clientConn2, 1)
	if state, _, ok := grokDG2RegistryState(t, handlers, sid); !ok || state != sessionStateIdle {
		t.Fatalf("after terminal: state=%v ok=%v, want idle (history truth, no badge)", state, ok)
	}
}

// TestGrokDG2_G7_SelfCancelRealRowTransitionsUnknown：真实 session 行（session!=nil）
// 主动取消 → 转 unknown、句柄保留、条目未删除；typed outcome Unknown。
func TestGrokDG2_G7_SelfCancelRealRowTransitionsUnknown(t *testing.T) {
	withFastGrokLeaderWatchdog(t, 15*time.Millisecond, 60*time.Millisecond)
	logs := captureGrokDG2Slog(t)
	const sid = "dg2-g7-real"
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	realSess := &fakeAgentSession{id: sid, events: make(chan core.Event, 1)}
	handlers.putSessionWithMeta(sid, "grokbuild", "/tmp", realSess)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})

	sub := &grokDG2Subscriber{ch: make(chan core.Event, 4)}
	relayKey := grokLeaderRelayKey(sid)
	handlers.mu.Lock()
	handlers.relayRunning[relayKey] = true
	handlers.mu.Unlock()
	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub, relayKey, "/tmp")
		close(done)
	}()

	sub.ch <- core.Event{Type: core.EventText, Content: "real row", TurnID: "prompt-r"}
	_ = readEventNames(t, clientConn, 3)

	handlers.broadcaster.UnsubscribeAll(serverConn)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not self-cancel")
	}

	state, sess, ok := grokDG2RegistryState(t, handlers, sid)
	if !ok || state != sessionStateUnknown {
		t.Fatalf("real row after self-cancel: state=%v ok=%v, want unknown", state, ok)
	}
	if sess != core.AgentSession(realSess) {
		t.Fatal("unknown row must keep the session handle")
	}
	if !strings.Contains(logs.String(), "registryOutcome=unknown") {
		t.Fatalf("missing registryOutcome=unknown in:\n%s", logs.String())
	}

	// 重开后冷拉 + 新 relay（新连接）：terminal 到达 → 真值回 idle（不 sticky unknown）。
	serverConn2, clientConn2, cleanup2 := openTestConn(t)
	defer cleanup2()
	handlers.broadcaster.Subscribe(serverConn2, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})
	sub2 := &grokDG2Subscriber{ch: make(chan core.Event, 4)}
	done2 := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub2, relayKey, "/tmp")
		close(done2)
	}()
	sub2.ch <- core.Event{Type: core.EventResult, Done: true, TurnID: "prompt-r"}
	_ = readEventNames(t, clientConn2, 1)
	if state, _, ok := grokDG2RegistryState(t, handlers, sid); !ok || state != sessionStateIdle {
		t.Fatalf("after reopen terminal: state=%v ok=%v, want idle", state, ok)
	}
}

// TestGrokDG2_G7_RealDisconnectStillRunsF7：对照用例——同 armed 状态下真 source
// 断开（channel close）仍走 F-7：turn_aborted(leader_disconnect) + markIdle + claim
// 清零；fence 后 page-0 重建输出 idle、旧 cursor stale。
func TestGrokDG2_G7_RealDisconnectStillRunsF7(t *testing.T) {
	const sid = "dg2-g7-f7"
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})

	sub := &grokDG2Subscriber{ch: make(chan core.Event, 4)}
	relayKey := grokLeaderRelayKey(sid)
	handlers.mu.Lock()
	handlers.relayRunning[relayKey] = true
	handlers.mu.Unlock()
	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub, relayKey, "/tmp")
		close(done)
	}()

	sub.ch <- core.Event{Type: core.EventText, Content: "partial", TurnID: "prompt-f7"}
	_ = readEventNames(t, clientConn, 3)

	scope := grokCatalogScopeKey("grokbuild")
	buildCalls := 0
	builder := runtimeStateBuilder(handlers, sid, &buildCalls)
	p0, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, "", 1, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("warm page-0: err=%v staleErr=%v", err, staleErr)
	}
	warmCursor, _ := p0["nextCursor"].(string)

	// 真 source 断开（订阅者仍在）。
	close(sub.ch)

	names, payloads := readEventNamesWithPayloads(t, clientConn, 2)
	if names[0] != "turn_aborted" || names[1] != "session_state_changed" {
		t.Fatalf("F-7 events = %v, want [turn_aborted session_state_changed]", names)
	}
	if payloads[0]["reason"] != "leader_disconnect" || payloads[0]["turnId"] != "prompt-f7" {
		t.Fatalf("turn_aborted payload = %v, want leader_disconnect/prompt-f7", payloads[0])
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not exit after real disconnect")
	}
	if state, _, ok := grokDG2RegistryState(t, handlers, sid); !ok || state != sessionStateIdle {
		t.Fatalf("registry after F-7: state=%v ok=%v, want idle (not unknown)", state, ok)
	}
	if handlers.grokCatalogWireCache().Peek(scope) != nil {
		t.Fatal("F-7 fence did not clear the warm snapshot")
	}
	if _, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, warmCursor, 1, builder); err != nil || staleErr == nil || staleErr.Code != "cursor_stale" {
		t.Fatalf("warm cursor after F-7 fence: err=%v staleErr=%v, want cursor_stale", err, staleErr)
	}
	p0b, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, "", 1, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("rebuild page-0: err=%v staleErr=%v", err, staleErr)
	}
	rows := p0b["sessions"].([]map[string]interface{})
	if rows[0]["runtimeState"] != "idle" {
		t.Fatalf("rebuilt runtimeState = %v, want idle after F-7 markIdle", rows[0]["runtimeState"])
	}
}

// ── G8：claim 所有权与消费者谓词 ────────────────────────────────────────────

// TestGrokDG2_G8_TerminalThenGraceKeepsIdle：armed turn 正常 turn_completed 收口
// （claim 清零）后 grace 到期 self-cancel → 不把 idle 覆盖成 unknown；预热 running
// 快照 → terminal + fence → page-0 重建输出 idle、旧 cursor stale。
func TestGrokDG2_G8_TerminalThenGraceKeepsIdle(t *testing.T) {
	withFastGrokLeaderWatchdog(t, 15*time.Millisecond, 60*time.Millisecond)
	logs := captureGrokDG2Slog(t)
	const sid = "dg2-g8-terminal"
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})

	sub := &grokDG2Subscriber{ch: make(chan core.Event, 4)}
	relayKey := grokLeaderRelayKey(sid)
	handlers.mu.Lock()
	handlers.relayRunning[relayKey] = true
	handlers.mu.Unlock()
	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub, relayKey, "/tmp")
		close(done)
	}()

	sub.ch <- core.Event{Type: core.EventText, Content: "turn body", TurnID: "prompt-t"}
	_ = readEventNames(t, clientConn, 3)

	scope := grokCatalogScopeKey("grokbuild")
	buildCalls := 0
	builder := runtimeStateBuilder(handlers, sid, &buildCalls)
	p0, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, "", 1, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("warm page-0: err=%v staleErr=%v", err, staleErr)
	}
	warmCursor, _ := p0["nextCursor"].(string)

	// 正常终态：turn_completed → markIdle + claim 清零 + fence。
	sub.ch <- core.Event{Type: core.EventResult, Done: true, TurnID: "prompt-t"}
	_ = readEventNames(t, clientConn, 1)
	if state, _, ok := grokDG2RegistryState(t, handlers, sid); !ok || state != sessionStateIdle {
		t.Fatalf("after terminal: state=%v ok=%v, want idle", state, ok)
	}
	if _, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, warmCursor, 1, builder); err != nil || staleErr == nil || staleErr.Code != "cursor_stale" {
		t.Fatalf("warm cursor after terminal fence: err=%v staleErr=%v, want cursor_stale", err, staleErr)
	}
	p0b, staleErr, err := handlers.grokCatalogWireCache().pageV2(scope, "", 1, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("rebuild page-0: err=%v staleErr=%v", err, staleErr)
	}
	rows := p0b["sessions"].([]map[string]interface{})
	if rows[0]["runtimeState"] != "idle" {
		t.Fatalf("rebuilt runtimeState = %v, want idle", rows[0]["runtimeState"])
	}

	// grace 到期 self-cancel：claim 已清零 → defer 释放 no-op，idle 不被覆盖成 unknown。
	handlers.broadcaster.UnsubscribeAll(serverConn)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not self-cancel after terminal + grace")
	}
	if state, _, ok := grokDG2RegistryState(t, handlers, sid); !ok || state != sessionStateIdle {
		t.Fatalf("registry after post-terminal self-cancel: state=%v ok=%v, want idle (no unknown overwrite)", state, ok)
	}
	got := logs.String()
	if !strings.Contains(got, "registryOutcome=noop") || !strings.Contains(got, "claimReleased=false") {
		t.Fatalf("post-terminal self-cancel log must show noop release / claimReleased=false:\n%s", got)
	}
}

// TestGrokDG2_G8_StaleClaimDoesNotOverwriteNewerState：claim 后其他路径对同一
// session 盖新 gen（markIdle）再 self-cancel → 过期 cancel no-op，不覆盖较新状态。
func TestGrokDG2_G8_StaleClaimDoesNotOverwriteNewerState(t *testing.T) {
	withFastGrokLeaderWatchdog(t, 15*time.Millisecond, 60*time.Millisecond)
	logs := captureGrokDG2Slog(t)
	const sid = "dg2-g8-stale"
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "grokbuild", SessionID: sid})

	sub := &grokDG2Subscriber{ch: make(chan core.Event, 4)}
	relayKey := grokLeaderRelayKey(sid)
	handlers.mu.Lock()
	handlers.relayRunning[relayKey] = true
	handlers.mu.Unlock()
	done := make(chan struct{})
	go func() {
		handlers.grokLeaderSessionRelayLoop(sid, "grokbuild", sub, relayKey, "/tmp")
		close(done)
	}()

	// 本 relay claim（gen G1）。
	sub.ch <- core.Event{Type: core.EventText, Content: "claimed", TurnID: "prompt-s"}
	_ = readEventNames(t, clientConn, 3)

	// 其他路径对同一 session 较新的终态（gen G2 取代）。
	handlers.sessions.markIdle(sid)

	handlers.broadcaster.UnsubscribeAll(serverConn)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not self-cancel")
	}
	if state, _, ok := grokDG2RegistryState(t, handlers, sid); !ok || state != sessionStateIdle {
		t.Fatalf("registry after stale self-cancel: state=%v ok=%v, want idle preserved (no unknown overwrite)", state, ok)
	}
	if !strings.Contains(logs.String(), "registryOutcome=noop") {
		t.Fatalf("stale release must log registryOutcome=noop:\n%s", logs.String())
	}
}

// TestGrokDG2_RegistryClaimReleaseOutcomes：typed outcome 三态 + gen 单调 + N 轮有界
// （synthetic 行释放即删，registry map 不随轮数增长）。
func TestGrokDG2_RegistryClaimReleaseOutcomes(t *testing.T) {
	r := newSessionRegistry()

	// synthetic 行：claim → Deleted（条目 CAS 删除）。
	sid := "co"
	g1 := r.claimRunning(sid)
	if g1 == 0 {
		t.Fatal("claim gen must be non-zero")
	}
	if out := r.releasePassiveClaim(sid, g1); out != passiveClaimDeleted {
		t.Fatalf("synthetic release = %v, want deleted", out)
	}
	if _, ok := r.get(sid); ok {
		t.Fatal("synthetic row must be CAS-deleted")
	}

	// 真实行：put → claim → Unknown（句柄保留、条目未删除）。
	r.put(sid, "grokbuild", "/tmp", &fakeAgentSession{id: sid})
	g2 := r.claimRunning(sid)
	if out := r.releasePassiveClaim(sid, g2); out != passiveClaimUnknown {
		t.Fatalf("real-row release = %v, want unknown", out)
	}
	ts, ok := r.get(sid)
	if !ok || ts.state != sessionStateUnknown || ts.session == nil {
		t.Fatalf("real row after release: ok=%v state=%v session=%v, want unknown with handle", ok, ts.state, ts.session)
	}

	// gen 失配：过期 claim → Noop（不覆盖较新 running）。
	g3 := r.claimRunning(sid)
	if out := r.releasePassiveClaim(sid, g2); out != passiveClaimNoop {
		t.Fatalf("stale release = %v, want noop", out)
	}
	if ts, _ := r.get(sid); ts.state != sessionStateRunning {
		t.Fatalf("stale release overwrote newer running: %v", ts.state)
	}

	// terminal 收口（state 离开 running）→ 再释放 → Noop。
	r.markIdle(sid)
	if out := r.releasePassiveClaim(sid, g3); out != passiveClaimNoop {
		t.Fatalf("post-terminal release = %v, want noop", out)
	}

	// putRaw 盖新 gen → 旧 claim Noop。
	g4 := r.claimRunning(sid)
	r.putRaw(sid, &fakeAgentSession{id: sid})
	if out := r.releasePassiveClaim(sid, g4); out != passiveClaimNoop {
		t.Fatalf("post-putRaw release = %v, want noop", out)
	}

	// 不存在的条目 → Noop；gen==0 → Noop。
	if out := r.releasePassiveClaim("missing", g1); out != passiveClaimNoop {
		t.Fatalf("missing-entry release = %v, want noop", out)
	}
	if out := r.releasePassiveClaim(sid, 0); out != passiveClaimNoop {
		t.Fatalf("zero-gen release = %v, want noop", out)
	}

	// N 轮（synthetic id）：gen 单调递增，释放即删，map 不增长。
	sid2 := "co2"
	prev := uint64(0)
	for i := 0; i < 5; i++ {
		g := r.claimRunning(sid2)
		if g <= prev {
			t.Fatalf("gen must be monotonically increasing: %d after %d", g, prev)
		}
		prev = g
		if out := r.releasePassiveClaim(sid2, g); out != passiveClaimDeleted {
			t.Fatalf("round %d release = %v, want deleted", i, out)
		}
		if _, ok := r.get(sid2); ok {
			t.Fatalf("round %d: synthetic row must not accumulate", i)
		}
	}
	r.mu.Lock()
	size := len(r.sessions)
	r.mu.Unlock()
	if size != 1 {
		t.Fatalf("registry size = %d, want 1 (only the real row; no per-round growth)", size)
	}
}

// TestGrokDG2_G8_IsKnownActiveTruthTable：isKnownActive 在旧三态域上与 `!isIdle`
// 逐点等价（absent/idle=false，running/closing=true），仅 unknown 改变（false）。
func TestGrokDG2_G8_IsKnownActiveTruthTable(t *testing.T) {
	r := newSessionRegistry()
	sid := "tt"
	if r.isKnownActive(sid) {
		t.Fatal("absent: want not known-active")
	}
	r.put(sid, "grokbuild", "/tmp", &fakeAgentSession{id: sid})
	if r.isKnownActive(sid) {
		t.Fatal("idle: want not known-active")
	}
	r.markRunning(sid)
	if !r.isKnownActive(sid) {
		t.Fatal("running: want known-active")
	}
	// closing 无生产 setter（防御态），同包直置。
	r.mu.Lock()
	r.sessions[sid].state = sessionStateClosing
	r.mu.Unlock()
	if !r.isKnownActive(sid) {
		t.Fatal("closing: want known-active (point-wise equivalence with old !isIdle)")
	}
	r.markIdle(sid)
	if r.isKnownActive(sid) {
		t.Fatal("idle again: want not known-active")
	}
	r.markRunning(sid)
	r.markUnknown(sid)
	if r.isKnownActive(sid) {
		t.Fatal("unknown: must NOT be known-active (D-G2: no auto-terminal on unknown)")
	}
}

// TestGrokDG2_G8_UnknownNoAutoTerminalOnChannelClose：registry 置 unknown 后
// relayEvents channel-close 路径不合成 turn_completed。
func TestGrokDG2_G8_UnknownNoAutoTerminalOnChannelClose(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "claudecode",
		SessionID: "ses_dg2_unk_cc",
	})
	session := &fakeAgentSession{id: "ses_dg2_unk_cc", events: make(chan core.Event, 1)}
	handlers.putSessionWithMeta("ses_dg2_unk_cc", "claudecode", "", session)
	handlers.sessions.markUnknown("ses_dg2_unk_cc")

	session.events <- core.Event{Type: core.EventText, Content: "output"}
	done := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_dg2_unk_cc", "claudecode")
		close(done)
	}()

	events := readEventNames(t, clientConn, 1)
	if events[0] != "text_delta" {
		t.Fatalf("events = %v, want text_delta only", events)
	}
	// unknown：channel close 不得合成 turn_completed（对照 markRunning 时的既有行为）。
	close(session.events)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not exit after channel close")
	}
	expectNoWebsocketEvent(t, clientConn, 200*time.Millisecond)
}

// TestGrokDG2_G8_UnknownNoAutoTerminalOnIdleTimer：registry 置 unknown 后
// relayEvents idleTimer 到期不合成 turn_completed；后续正常终态仍可收口。
func TestGrokDG2_G8_UnknownNoAutoTerminalOnIdleTimer(t *testing.T) {
	oldInitialTimeout := relayInitialTimeout
	oldActiveTimeout := relayActiveTimeout
	relayInitialTimeout = 200 * time.Millisecond
	relayActiveTimeout = 25 * time.Millisecond
	defer func() {
		relayInitialTimeout = oldInitialTimeout
		relayActiveTimeout = oldActiveTimeout
	}()

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := NewHandlers()
	defer handlers.observation.Stop()
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "grokbuild",
		SessionID: "ses_dg2_unk_it",
	})
	session := &fakeAgentSession{id: "ses_dg2_unk_it", events: make(chan core.Event, 2)}
	handlers.putSessionWithMeta("ses_dg2_unk_it", "grokbuild", "", session)
	handlers.sessions.markUnknown("ses_dg2_unk_it")

	session.events <- core.Event{Type: core.EventText, Content: "working"}
	done := make(chan struct{})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_dg2_unk_it", "grokbuild")
		close(done)
	}()

	events := readEventNames(t, clientConn, 1)
	if events[0] != "text_delta" {
		t.Fatalf("events = %v, want text_delta only", events)
	}

	// 远超 idleTimer（25ms）：unknown → 不合成 turn_completed、不广播 idle
	// （对照 running 时的既有自动收口行为）。
	time.Sleep(100 * time.Millisecond)
	expectNoWebsocketEvent(t, clientConn, 150*time.Millisecond)

	// idleTimer 到期后 relay 照常退出（D-G2 只改谓词不改生命周期；
	// 真实终态经重开冷拉/新 relay 收口，由 G7 synthetic 重开用例覆盖）。
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents should exit after idle timer fires")
	}
}

// TestGrokDG2_G8_UnknownNoAbortOnCodexHardCap：registry 置 unknown 后 codex
// file relay hardCap（进程死亡判定）不合成 turn_aborted、不广播 idle。
func TestGrokDG2_G8_UnknownNoAbortOnCodexHardCap(t *testing.T) {
	const sessionID = "dg2-unknown-hardcap"
	handlers, _, client, _ := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	events := readEventNames(t, client, 2) // running 启动：turn_started + running
	if events[0] != "turn_started" || events[1] != "session_state_changed" {
		t.Fatalf("running startup events = %v, want [turn_started session_state_changed]", events)
	}

	// D-G2 主动下线后的真实行形态：unknown。
	handlers.sessions.markUnknown(sessionID)

	// 等 hardCap（fast fixture 1s）退出。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !codexFileRelayIsRunning(t, handlers, sessionID) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("codex file relay did not exit at hardCap")
	}
	// unknown：不得合成 turn_aborted（对照 running 时的 TestCodexFileRelayProcessDeathSynthesizesTurnAborted）。
	expectNoWebsocketEvent(t, client, 200*time.Millisecond)
}

// TestGrokDG2_G8_UnknownNoIdleBroadcastOnClaudeTTL：registry 置 unknown 后
// claude file relay live-idle TTL 退出路径不 broadcastIdleState。
func TestGrokDG2_G8_UnknownNoIdleBroadcastOnClaudeTTL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "dg2-unknown-claude-ttl"
	writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"dg2-u1","message":{"role":"user","content":"external prompt"}}`,
	)
	handlers, agent, client := startClaudeFileRelayFixture(t, sessionID, true)

	messages := client.readEvents(t, 1)
	if got := messages[0]["event"]; got != "turn_started" {
		t.Fatalf("event = %#v, want turn_started", got)
	}

	// D-G2 主动下线后的真实行形态：unknown。
	handlers.sessions.markUnknown(sessionID)

	unsubscribeAllClaudeFileRelayClients(handlers)
	agent.processMu.Lock()
	agent.alivePIDs[4242] = false
	agent.processMu.Unlock()
	waitClaudeFileRelayStopped(t, handlers, sessionID)

	// unknown：TTL 退出不得广播 idle（对照 running 时的 MidTurnBroadcastsIdle 先例）。
	if err := client.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := client.conn.ReadJSON(&payload); err == nil {
		t.Fatalf("unexpected idle broadcast for unknown session: %v", payload)
	}
}
