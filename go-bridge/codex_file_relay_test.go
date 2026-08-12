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

// TestDetectCodexTranscriptRunningTurnWithContent locks the §3.3 rule #2 / D6 gate for the
// codex transcript-based cold-start live sampling: a bare task_started shell must NOT count as
// a live turn, while a non-terminal turn that has produced content must.
func TestDetectCodexTranscriptRunningTurnWithContent(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "live-content.jsonl")
	h := &Handlers{}

	write := func(t *testing.T, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
	}

	// Bare shell: only task_started, no content -> not a live turn (must stay hydrating).
	write(t, `{"timestamp":"2026-08-12T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-bare"}}`+"\n")
	if got := h.detectCodexTranscriptRunningTurnWithContent(path); got {
		t.Fatal("bare task_started shell must not be a live turn")
	}

	// Running turn with content (user + assistant response_items) -> live.
	write(t, strings.Join([]string{
		`{"timestamp":"2026-08-12T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-running"}}`,
		`{"timestamp":"2026-08-12T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","id":"msg-user","content":[{"type":"input_text","text":"mid-flight prompt"}]}}`,
		`{"timestamp":"2026-08-12T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"in progress"}]}}`,
	}, "\n")+"\n")
	if got := h.detectCodexTranscriptRunningTurnWithContent(path); !got {
		t.Fatal("non-terminal turn with content must be a live turn")
	}

	// Terminal closes the turn: completed tail must not be live.
	write(t, strings.Join([]string{
		`{"timestamp":"2026-08-12T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-done"}}`,
		`{"timestamp":"2026-08-12T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
		`{"timestamp":"2026-08-12T00:00:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-done"}}`,
	}, "\n")+"\n")
	if got := h.detectCodexTranscriptRunningTurnWithContent(path); got {
		t.Fatal("completed turn must not be a live turn")
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

// TestCodexRelayWatcherStartsRelayForSubscribedSession 验证 Layer 2 安全网：客户端订阅了
// codex session 但没有 relay 在跑（已退出/从未启动）时，ensureRelaysForSubscribedCodexSessions
// 会补启 relay，保证 live 通道不丢。
func TestCodexRelayWatcherStartsRelayForSubscribedSession(t *testing.T) {
	withFastCodexFileRelay(t)
	const sessionID = "watcher-restart"
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "rollout-"+sessionID+".jsonl")
	init := codexRolloutEvent("task_started") + "\n" + codexRolloutEvent("task_complete") + "\n"
	if err := os.WriteFile(path, []byte(init), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	handlers := newTestHandlers(t)
	agent := &fakeAgent{name: "codex", transcriptPath: path}
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)

	// 客户端订阅，但不启动 relay（模拟 relay 已退出/未启动）。
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "codex", SessionID: sessionID})
	if codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("relay should not be running before watcher")
	}

	// 安全网：为订阅中的 session 补启 relay。
	handlers.ensureRelaysForSubscribedCodexSessions()
	if !codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("watcher did not start relay for subscribed session")
	}

	// 补启的 relay 能捕获后续 turn（验证它真活着、能投递到订阅者）。
	_ = readEventNames(t, clientConn, 2) // idle startup: turn_completed + session_state_changed
	appendCodexRollout(t, agent.transcriptPath, codexRolloutEvent("task_started"))
	events := readEventNames(t, clientConn, 2)
	if events[0] != "turn_started" {
		t.Fatalf("events after late task_started = %v, want turn_started", events)
	}
}

// TestCodexFileRelayProcessDeathSynthesizesTurnAborted 验证 §3.3（codex 侧）：codex 进程死亡（无增长
// 超过 hardCap，既有「超限几乎可判定进程已死」语义）且 transcript 尾部为未终结 task_started 时，
// file relay 合成 turn_aborted（reason=process_death）收口投影，再广播 idle 并退出 —— 否则 crashed
// codex session 会在投影里留下永久 running turn（sourceIsLive=false 时提交门槛也不放行）。合成必须
// 在有订阅者时也发生（iOS push 模型下 iOS 打开 crashed session 不会自动解除订阅）。
func TestCodexFileRelayProcessDeathSynthesizesTurnAborted(t *testing.T) {
	const sessionID = "process-death-synth-abort"
	handlers, _, client, _ := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	// running 启动：turn_started + session_state_changed(running)。
	events := readEventNames(t, client, 2)
	if events[0] != "turn_started" || events[1] != "session_state_changed" {
		t.Fatalf("running startup events = %v, want [turn_started session_state_changed]", events)
	}

	// 无增长 → 软 TTL 复核仍 running → 续 watch（不误退）。
	time.Sleep(150 * time.Millisecond)
	if !codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("codex file relay exited during soft-TTL while task still running")
	}

	// 等 hardCap（fast fixture 1s）→ 进程死亡判定 → 合成 turn_aborted + idle，然后退出。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !codexFileRelayIsRunning(t, handlers, sessionID) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if codexFileRelayIsRunning(t, handlers, sessionID) {
		t.Fatal("codex file relay did not exit at hardCap for dead running process")
	}

	names, payloads := readEventNamesWithPayloads(t, client, 2)
	if names[0] != "turn_aborted" || names[1] != "session_state_changed" {
		t.Fatalf("events after process death = %v, want [turn_aborted session_state_changed]", names)
	}
	if payloads[0] == nil || payloads[0]["turnId"] != "turn-1" || payloads[0]["reason"] != "process_death" {
		t.Fatalf("turn_aborted data = %#v, want turnId=turn-1 reason=process_death", payloads[0])
	}

	// 合成的终态必须已进入投影 reducer（收口 in-flight turn）——这是非 live 冷开提交门槛
	// （所有 cold-armed turn 终结）在 crashed 场景唯一放行路径。
	snap, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("codex", sessionID)
	if !ok {
		t.Fatal("no projection snapshot after synthesized abort")
	}
	if len(snap.Turns) != 1 || snap.Turns[0].Status != "aborted" {
		t.Fatalf("projection after synthesized abort = %+v, want single aborted turn", snap.Turns)
	}
}

// TestCodexFileRelayProcessDeathIdleTailDoesNotSynthesizeAbort 验证 §3.3 对称防重：进程死亡时
// transcript 尾部已是终态（task_complete）→ 不合成第二个 turn_aborted（turn 已关闭，合成即重复终态）。
func TestCodexFileRelayProcessDeathIdleTailDoesNotSynthesizeAbort(t *testing.T) {
	const sessionID = "process-death-idle-tail"
	handlers, _, client, _ := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
		codexRolloutEvent("task_complete"),
	)
	_ = readEventNames(t, client, 2) // idle 启动：turn_completed + idle

	// 等 hardCap 后退出：idle 尾部只广播 idle，绝不合成 turn_aborted。
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
	// idle 尾部进程死亡：投影里的 turn 保持 completed，绝不被重复合成为 aborted。
	// （relay 退出时 registry 已是 idle，不会广播任何额外事件。）
	snap, ok := handlers.eventPublisher.ProjectionReducer().Snapshot("codex", sessionID)
	if !ok {
		t.Fatal("no projection snapshot after idle-tail process death")
	}
	if len(snap.Turns) != 1 || snap.Turns[0].Status != "completed" {
		t.Fatalf("projection after idle-tail process death = %+v, want single completed turn (no synthesized abort)", snap.Turns)
	}
}
