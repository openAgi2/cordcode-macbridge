package gobridge

// S1（session-badges 上游对齐方案 §5/§8，r4/r5 APPROVED v6）：官方
// thread/status/changed → 控制面执行态投影的完整管道测试——codec 解码、
// mapAgentEvent 映射、registry 簿记、窄控制面发布到达广播入口、
// Kernel 零摄入零条目（F-6）、推送→终态序贯不解锁白名单、唯一发布（F-10）。

import (
	"encoding/json"
	"testing"
	"time"

	codexremote "github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	"github.com/openAgi2/cordcode-macbridge/core"
)

func statusChangedNotification(t *testing.T, params string) []core.Event {
	t.Helper()
	codec := codexremote.NewLiveCodec()
	return codec.Decode(codexremote.Notification{
		Method: "thread/status/changed",
		Params: json.RawMessage(params),
	})
}

// 映射词表：core.EventSessionState → 既有 wire 词 session_state_changed。
func TestMapAgentEventSessionState(t *testing.T) {
	cases := []struct{ state, want string }{
		{"running", "running"},
		{"requiresAction", "requiresAction"},
		{"idle", "idle"},
	}
	for _, tc := range cases {
		ev := statusChangedEvent(t, tc.state)
		name, data, done := mapAgentEvent(ev)
		if name != "session_state_changed" {
			t.Fatalf("state %q: event name = %q", tc.state, name)
		}
		m, ok := data.(map[string]interface{})
		if !ok || m["state"] != tc.want {
			t.Fatalf("state %q: data = %#v", tc.state, data)
		}
		if done {
			t.Fatalf("state %q: done must be false (non-terminal)", tc.state)
		}
	}

	// nil payload / 空 state 丢弃。
	if name, _, _ := mapAgentEvent(core.Event{Type: core.EventSessionState, SessionID: "th"}); name != "" {
		t.Fatalf("nil payload produced %q", name)
	}
	if name, _, _ := mapAgentEvent(core.Event{Type: core.EventSessionState, SessionID: "th", SessionState: &core.SessionStateEvent{}}); name != "" {
		t.Fatalf("empty state produced %q", name)
	}
}

func statusChangedEvent(t *testing.T, state string) core.Event {
	t.Helper()
	events := statusChangedNotification(t, `{"threadId":"th-status","status":{"type":"`+statusTypeFor(state)+`","activeFlags":`+flagsFor(state)+`}}`)
	if len(events) != 1 {
		t.Fatalf("codec produced %d events, want 1", len(events))
	}
	return events[0]
}

func statusTypeFor(state string) string {
	if state == "idle" {
		return "idle"
	}
	return "active"
}

func flagsFor(state string) string {
	if state == "requiresAction" {
		return `["waitingOnApproval"]`
	}
	return `[]`
}

// 完整管道（方案 §8 控制面发布定向测试）：无观察兴趣、无 kernel 态的 session 经
// codec 喂 Active/Idle → registry 翻转 ∧ 控制面发布到达广播入口 ∧ projection 零
// 变更零条目（Snapshot not-found ∧ HasReducerState==false）。
func TestSessionStatePushControlPlanePipeline(t *testing.T) {
	h := NewHandlers()
	conn := newPublisherCaptureConn(nil)
	// 广播投递经 Broadcaster.Targets 解析（无订阅者时全量 fallback）——注册进
	// broadcaster 才是真实到端面；RegisterConnection 只管 targeted/回放。
	h.broadcaster.RegisterConn(conn)

	// Active → running。
	events := statusChangedNotification(t, `{"threadId":"th-cp","status":{"type":"active"}}`)
	ev := events[0]
	eventName, data, _ := mapAgentEvent(ev)
	if eventName != "session_state_changed" {
		t.Fatalf("mapped name = %q", eventName)
	}
	// registry 簿记（被动泵/relay 环路同语义分支）。
	h.applyRelayEventRegistrySync(ev.SessionID, eventName, data)
	if st, ok := h.sessions.get("th-cp"); !ok || string(st.state) != string(sessionStateRunning) {
		t.Fatalf("registry not flipped to running: %+v", st)
	}
	// 控制面发布 → 广播入口到达（真实 capture connection）。
	if _, ok := h.projectionKernel.Snapshot("codex-remote", "th-cp"); ok {
		t.Fatalf("kernel entry must not exist before publish")
	}
	h.eventPublisher.PublishSessionStateControlPlane("codex-remote", "th-cp", "running")
	conn.waitCount(t, 1)
	frames := conn.snapshot()
	frame, ok := frames[0].(EventMessage)
	if !ok || frame.Event != "session_state_changed" {
		t.Fatalf("broadcast frame = %#v", frames[0])
	}
	if frame.SessionID != "th-cp" || frame.BackendID != "codex-remote" {
		t.Fatalf("frame identity = %s/%s", frame.BackendID, frame.SessionID)
	}
	if m, ok := frame.Data.(map[string]interface{}); !ok || m["state"] != "running" {
		t.Fatalf("frame data = %#v", frame.Data)
	}

	// Kernel 零条目、零摄入（F-6：控制面模式不为从未打开的 session 建 reducer 条目）。
	if _, ok := h.projectionKernel.Snapshot("codex-remote", "th-cp"); ok {
		t.Fatalf("control-plane publish must not create kernel entry")
	}
	if h.projectionKernel.HasReducerState("codex-remote", "th-cp") {
		t.Fatalf("HasReducerState must stay false (F-6 六环链第一环)")
	}

	// settle 一个 failed outcome，再喂 Idle → markIdle 但 outcome 保留（第一轮不变量）。
	h.settleTurnOutcomeFromEvent("th-cp", "turn_error", nil)
	h.applyRelayEventRegistrySync("th-cp", "session_state_changed", map[string]interface{}{"state": "idle"})
	if st, ok := h.sessions.get("th-cp"); !ok || string(st.state) != string(sessionStateIdle) {
		t.Fatalf("registry not flipped to idle: %+v", st)
	}
	if outcome, _ := h.sessions.lastOutcomeFor("th-cp"); outcome != "failed" {
		t.Fatalf("idle push must not clear outcome: %q", outcome)
	}
}

// 序贯回归（方案 §8：推送→终态帧）：状态推送不产生空条目 → passiveFeedAllowed 的
// terminal 白名单对后续 attach 形态终态帧恒不放行（无 kernel 态 ⇒ 门恒 false），
// Kernel 仍无该 session 条目。
func TestSessionStatePushThenTerminalStaysGated(t *testing.T) {
	h := NewHandlers()
	backendID, sid := "codex-remote", "th-seq"

	h.eventPublisher.PublishSessionStateControlPlane(backendID, sid, "running")
	if h.projectionKernel.HasReducerState(backendID, sid) {
		t.Fatalf("status push must not create kernel state")
	}
	for _, terminal := range []string{"turn_completed", "turn_error", "turn_aborted"} {
		if passiveFeedAllowed(false, false, h.projectionKernel.HasReducerState(backendID, sid), terminal) {
			t.Fatalf("terminal %q must stay gated without kernel state (F-6 unlock chain)", terminal)
		}
	}
	if _, ok := h.projectionKernel.Snapshot(backendID, sid); ok {
		t.Fatalf("kernel entry must not exist after push sequence")
	}
}

// 防御性单测（方案 §8）：即便未来出现 timeline 模式生产者，该帧 IngestLive 恒
// NoChange（reducer 零 case）——SyncRev 不前进、turns/blocks 恒空。
func TestSessionStateFrameIngestIsNoChange(t *testing.T) {
	h := NewHandlers()
	msg := EventMessage{
		Type:      "event",
		EventID:   "test:1",
		Seq:       1,
		BackendID: "codex-remote",
		SessionID: "th-def",
		Event:     "session_state_changed",
		Data:      map[string]interface{}{"state": "running"},
	}
	if got := h.projectionKernel.IngestLive(msg); got != ProjectionIngestNoChange {
		t.Fatalf("IngestLive = %v, want NoChange", got)
	}
	if h.projectionKernel.HasReducerState("codex-remote", "th-def") {
		t.Fatalf("zero-case frame must not flip HasReducerState")
	}
}

// 唯一发布（F-10）：S1 分派只走控制面入口，不经 deltaBatcher 双出——同一连接在
// 静置窗口内恰收一帧。
func TestSessionStateControlPlaneUniquePublish(t *testing.T) {
	h := NewHandlers()
	conn := newPublisherCaptureConn(nil)
	h.broadcaster.RegisterConn(conn)

	h.eventPublisher.PublishSessionStateControlPlane("codex-remote", "th-uni", "running")

	conn.waitCount(t, 1)
	time.Sleep(150 * time.Millisecond)
	if got := len(conn.snapshot()); got != 1 {
		t.Fatalf("frames = %d, want exactly 1 (no deltaBatcher double-fire)", got)
	}
}

// settle 面防御（方案 §8 回归行）：session_state_changed 对 settle helper 恒 no-op。
func TestSettleOutcomeIgnoresSessionStateChanged(t *testing.T) {
	h := NewHandlers()
	h.settleTurnOutcomeFromEvent("th-settle", "session_state_changed", map[string]interface{}{"state": "running"})
	if outcome, _ := h.sessions.lastOutcomeFor("th-settle"); outcome != "" {
		t.Fatalf("session_state_changed must not settle outcome: %q", outcome)
	}
}
