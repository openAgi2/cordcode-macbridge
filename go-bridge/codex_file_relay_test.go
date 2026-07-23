package gobridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDetectCodexTranscriptTaskState(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "rollout-session.jsonl")
	h := &Handlers{}

	writeTranscript := func(t *testing.T, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
	}

	writeTranscript(t, `{"timestamp":"2026-07-01T07:37:47.626Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`+"\n")
	if got := h.detectCodexTranscriptTaskState(path); got != "running" {
		t.Fatalf("state after task_started = %q, want running", got)
	}
	if state, turnID := h.detectCodexTranscriptTask(path); state != "running" || turnID != "turn-1" {
		t.Fatalf("task after task_started = (%q, %q), want (running, turn-1)", state, turnID)
	}

	writeTranscript(t,
		`{"timestamp":"2026-07-01T07:37:47.626Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`+"\n"+
			`{"timestamp":"2026-07-01T07:39:17.071Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`+"\n",
	)
	if got := h.detectCodexTranscriptTaskState(path); got != "idle" {
		t.Fatalf("state after task_complete = %q, want idle", got)
	}
	if state, turnID := h.detectCodexTranscriptTask(path); state != "idle" || turnID != "turn-1" {
		t.Fatalf("task after task_complete = (%q, %q), want (idle, turn-1)", state, turnID)
	}
}

// codexRolloutEvent 构造一条 Codex rollout event_msg 行（用于 file-relay 测试）。
func codexRolloutEvent(eventType string) string {
	return fmt.Sprintf(`{"timestamp":"2026-07-01T07:37:47.626Z","type":"event_msg","payload":{"type":%q,"turn_id":"turn-1"}}`, eventType)
}

// withFastCodexFileRelay 把 Codex file-relay 的轮询/退避时间压缩，让 loop 级测试快速且确定。
func withFastCodexFileRelay(t *testing.T) {
	t.Helper()
	prevPoll := codexFileRelayPollInterval
	prevTTL := codexFileRelayNoGrowthTTL
	prevHardCap := codexFileRelayNoGrowthHardCap
	codexFileRelayPollInterval = 10 * time.Millisecond
	codexFileRelayNoGrowthTTL = 80 * time.Millisecond
	codexFileRelayNoGrowthHardCap = time.Second
	t.Cleanup(func() {
		codexFileRelayPollInterval = prevPoll
		codexFileRelayNoGrowthTTL = prevTTL
		codexFileRelayNoGrowthHardCap = prevHardCap
	})
}

func appendCodexRollout(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open rollout for append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatalf("append rollout: %v", err)
	}
}

// startCodexFileRelayFixture 写入初始 rollout、注册一个实现 TranscriptLocator 的
// codex fakeAgent，订阅 test conn 并启动 file relay。返回 agent（其 transcriptPath 即文件路径）
// 与 serverConn（供测试在结束时 UnsubscribeAll 以触发 relay 退出）。
func startCodexFileRelayFixture(t *testing.T, sessionID string, initialLines ...string) (*Handlers, *fakeAgent, *websocket.Conn, *Conn) {
	t.Helper()
	withFastCodexFileRelay(t)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "rollout-"+sessionID+".jsonl")
	content := strings.Join(initialLines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	handlers := newTestHandlers(t)
	agent := &fakeAgent{name: "codex", transcriptPath: path}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: sessionID})
	handlers.startCodexSessionFileRelay(sessionID, serverConn, "codex", agent)
	return handlers, agent, clientConn, serverConn
}

func codexFileRelayIsRunning(t *testing.T, h *Handlers, sessionID string) bool {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.relayRunning[codexSessionFileRelayKey(sessionID)]
}

// waitCodexFileRelayStopped 模拟 iOS 离开（UnsubscribeAll 该 serverConn），再等 relay 在无订阅者时
// 于 hardCap 后退出（Layer 1 后不再在软 TTL 退出）。hardCap 回收是防 goroutine 泄漏的兜底。
func waitCodexFileRelayStopped(t *testing.T, h *Handlers, sessionID string, serverConn *Conn) {
	t.Helper()
	if serverConn != nil {
		h.broadcaster.UnsubscribeAll(serverConn)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !codexFileRelayIsRunning(t, h, sessionID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("codex file relay still running for %s", sessionID)
}

// TestCodexFileRelayIdleStartupKeepsWatchingNextTurn 验证 Phase 0 修复 #1：
// idle 启动不再 return，进入 watch loop，下一轮 task_started 仍被广播。
func TestCodexFileRelayIdleStartupKeepsWatchingNextTurn(t *testing.T) {
	const sessionID = "idle-start-watch"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
		codexRolloutEvent("task_complete"),
	)
	// idle 启动：广播 turn_completed + idle，但不退出。
	events := readEventNames(t, client, 2)
	if events[0] != "turn_completed" || events[1] != "session_state_changed" {
		t.Fatalf("idle startup events = %v, want [turn_completed, session_state_changed]", events)
	}
	// 下一轮 task_started 必须被广播（证明 idle 启动没有 return）。
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_started"))
	events = readEventNames(t, client, 2)
	if events[0] != "turn_started" {
		t.Fatalf("after append events = %v, want turn_started first", events)
	}
	// 让 relay 干净退出，避免 goroutine 泄漏到后续测试。
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_complete"))
	_ = readEventNames(t, client, 2)
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}

// TestCodexFileRelayTaskCompleteContinuesWatchingNextTurn 验证 Phase 0 修复 #2：
// task_complete 后不再 return（继续 watch），同一 session 下一轮 task_started 仍被广播。
func TestCodexFileRelayTaskCompleteContinuesWatchingNextTurn(t *testing.T) {
	const sessionID = "task-complete-continue"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	// running 启动：先广播带稳定 identity 的 turn_started，再广播 running。
	events := readEventNames(t, client, 2)
	if events[0] != "turn_started" || events[1] != "session_state_changed" {
		t.Fatalf("running startup events = %v, want [turn_started session_state_changed]", events)
	}
	// task_complete → turn_completed + idle，但不 return。
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_complete"))
	events = readEventNames(t, client, 2)
	if events[0] != "turn_completed" {
		t.Fatalf("after task_complete events = %v, want turn_completed first", events)
	}
	// 下一轮 task_started 必须被广播（证明 task_complete 没有 return）。
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_started"))
	events = readEventNames(t, client, 2)
	if events[0] != "turn_started" {
		t.Fatalf("next turn events = %v, want turn_started first", events)
	}
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_complete"))
	_ = readEventNames(t, client, 2)
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}

// TestCodexFileRelayNoGrowthTTLExitsWhenIdle 验证无增长回收：idle 后无增长，软 TTL 复核为 idle 不再
// 退出（见 TestCodexFileRelayKeepsWatchingPastTTLWithoutSubscriber），守到 hardCap 才回收 goroutine。
func TestCodexFileRelayNoGrowthTTLExitsWhenIdle(t *testing.T) {
	const sessionID = "ttl-exit-idle"
	handlers, _, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
		codexRolloutEvent("task_complete"),
	)
	_ = readEventNames(t, client, 2) // idle startup 广播
	// 无增长：软 TTL 复核为 idle → 不退出，守到 hardCap 回收。
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}

// TestCodexFileRelayNoGrowthTTLKeepsWatchingWhenRunning 验证无增长 TTL 的 running 复核：
// task_started 后长时间无增长，软 TTL 触发复核仍 running → 续 watch（不误退、不漏后续事件）。
func TestCodexFileRelayNoGrowthTTLKeepsWatchingWhenRunning(t *testing.T) {
	const sessionID = "ttl-running-keep"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	_ = readEventNames(t, client, 2) // running startup: turn_started + session_state_changed
	// 等待超过软 TTL：复核仍 running → 续 watch（不退出）。
	time.Sleep(150 * time.Millisecond)
	if !codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("codex file relay exited during soft-TTL while task still running")
	}
	// 续 watch 期间 append task_complete 必须被收到（证明 relay 仍活着、未误退）。
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_complete"))
	events := readEventNames(t, client, 2)
	if events[0] != "turn_completed" {
		t.Fatalf("events after TTL-survival append = %v, want turn_completed", events)
	}
	// 之后无增长 → idle 复核 → 退出。
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}

// TestCodexFileRelayKeepsWatchingWhileSubscribed 验证 push 模型修复：iOS 仍订阅该 session 时，
// relay 即使远超 idle TTL 也持续 watch（等 Mac 端外部 turn），不退出——否则会在外部 turn
// 到来前 TTL 退出而错过整轮（owner 复现）。只有 iOS 取消订阅（断开/切走）后才在 TTL 内退出。
func TestCodexFileRelayKeepsWatchingWhileSubscribed(t *testing.T) {
	const sessionID = "subscribed-keep-watch"
	handlers, _, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
		codexRolloutEvent("task_complete"),
	)
	_ = readEventNames(t, client, 2) // idle startup: turn_completed + idle

	// 远超 idle TTL（80ms）：iOS 仍订阅 → relay 必须仍在 watch。
	time.Sleep(200 * time.Millisecond)
	if !codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("codex file relay exited while session still subscribed (should keep watching for external turns)")
	}

	// iOS 离开（UnsubscribeAll）→ relay 在 hardCap 内退出（不再在软 TTL 退出）。
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}

// TestCodexFileRelayKeepsWatchingPastTTLWithoutSubscriber 验证 Layer 1 修复：iOS 打开一个
// idle 外部 session 后常停止轮询（无订阅者），relay 不再因「无订阅者 + 软 TTL」退出，持续守到
// hardCap，从而能在 Mac 端稍后发 turn 时捕获并投递（owner 复现：打开 idle session → relay 90s
// 退出 → 后来发任务 → 无 live 同步）。
func TestCodexFileRelayKeepsWatchingPastTTLWithoutSubscriber(t *testing.T) {
	const sessionID = "no-sub-keep-watch"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
		codexRolloutEvent("task_complete"),
	)
	_ = readEventNames(t, client, 2) // idle startup: turn_completed + idle

	// iOS 停止轮询（无订阅者）。
	handlers.broadcaster.UnsubscribeAll(serverConn)

	// 远超软 TTL（80ms）但远未到 hardCap（1s）：relay 必须仍在 watch（不再因无订阅者 TTL 退出）。
	time.Sleep(300 * time.Millisecond)
	if !codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("codex file relay exited at soft-TTL without subscriber (should keep watching until hardCap for later external turns)")
	}

	// Mac 端稍后发 turn + iOS 重新关注（重新订阅）→ relay 必须捕获并投递 turn_started。
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: sessionID})
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_started"))
	events := readEventNames(t, client, 2) // turn_started + session_state_changed(running)
	if events[0] != "turn_started" {
		t.Fatalf("events after late task_started = %v, want turn_started (relay must catch a later external turn)", events)
	}
}
