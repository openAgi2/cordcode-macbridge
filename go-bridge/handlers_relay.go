package gobridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
	"github.com/openAgi2/cordcode-macbridge/agent/codex"
	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	relayKindAgent      = "agent"
	relayKindClaudeFile = "claude_file"
)

var (
	claudeFileRelayPollInterval       = 3 * time.Second
	claudeFileRelayLiveIdleTTL        = 90 * time.Second
	claudeFileRelayProcessDeathMisses = 1
)

var (
	// codexFileRelayPollInterval 是 Codex transcript 文件 watch 的轮询间隔。
	codexFileRelayPollInterval = 1 * time.Second
	// codexFileRelayNoGrowthTTL 是 transcript 无增长时考虑退出的软 TTL。
	// Codex 没有 Claude 那样的 live PID 探测，只能用纯时间启发式：太短会在 agent
	// 长思考间隙（实测 turn 内 agent_reasoning 间隔可达 30–60s+）误退、漏后续事件；
	// 起步参照 claudeFileRelayLiveIdleTTL（90s），据真实思考间隙分布上调。软 TTL
	// 触发时会复核 detectCodexTranscriptTaskState，仍 running 则续 watch。
	codexFileRelayNoGrowthTTL = 90 * time.Second
	// codexFileRelayNoGrowthHardCap 是即使复核仍 running 也要退出的绝对上限，防止
	// 「进程已死但 task_started 未配 task_complete」导致 relay 永久滞留。真实 turn 写
	// 盘间隔远小于此值（§3.1 每 20–40s 一条），长静默工具执行偶发但极少超过此上限；
	// 超限几乎可判定进程已死。
	codexFileRelayNoGrowthHardCap = 15 * time.Minute
)

// startRelayIfNotRunning 为 session 启动事件转发（如果尚未运行）。
// 用于 iOS 仅调用 get_session_messages 而未调用 send_message 的场景。
//
// 关键：agent relay（relayEvents）的生命周期与全局 relayRunning/relayRunningKind 槽位解耦。
// 当 Claude file relay 已持有该槽位（kind=claude_file）时——例如 cold-open 已启动 file relay、
// 随后用户在本 session 发起本地 turn——我们**不**把 kind 翻成 agent，否则 claudeSessionFileRelayLoop
// 会在 superseded 检查（见本文件 "superseded by agent relay" 日志）处退出，丢失唯一的 UUID 内容来源；
// 而 agent relay 的事件缺少 itemId，会被 reducer 跳过（projection_reducer.go text_delta
// `if turnID == "" { return }`），导致本地发起的 turn 没有 content patch 实时投递（Issue 3）。
// 改为让 agent relay 作为 sidecar 并行运行（控制面事件 turn_started/completed + 任何带 itemId 的事件），
// file relay 继续作为 UUID-keyed 内容来源。reducer 跳过 agent relay 无 itemId 的正文，不会重复应用。
func (h *Handlers) startRelayIfNotRunning(sessionID string, sess core.AgentSession, conn Connection, backendID string) {
	h.mu.Lock()
	if h.agentRelayRunning[sessionID] && h.agentRelaySess[sessionID] == sess {
		h.mu.Unlock()
		return
	}
	stale := h.agentRelaySess[sessionID]
	h.agentRelayGen[sessionID]++
	gen := h.agentRelayGen[sessionID]
	h.agentRelayRunning[sessionID] = true
	h.agentRelaySess[sessionID] = sess
	// 仅当没有 relay 占用全局槽位时才认领并把 kind 标为 agent；若 file relay (claude_file) 已占用，
	// 保留其 kind 以免触发 claudeSessionFileRelayLoop 的 supersession 退出。
	if !h.relayRunning[sessionID] {
		h.relayRunning[sessionID] = true
		h.relayRunningKind[sessionID] = relayKindAgent
	}
	h.mu.Unlock()
	if stale != nil && stale != sess {
		slog.Warn("go-bridge: replacing stale agent relay after session respawn",
			"backendID", backendID, "sessionID", sessionID)
		_ = stale.Close()
	}
	go h.relayEvents(conn, sess, sessionID, backendID, gen)
}

// startClaudeSessionFileRelay 为没有 AgentSession 的 Claude Desktop session
// 启动基于 transcript 文件监视的事件转发。当 iOS 调用 resume_session 或
// get_session_messages 打开一个已在外部运行/已完成的 session 时，
// handleResumeSession 不创建 AgentSession（设计如此），导致 relayEvents 永远
// 不会启动。本函数通过轮询 .jsonl 文件变化来代替内存事件通道，向 iOS 广播
// turn_started / turn_completed / session_state_changed 事件。
func (h *Handlers) startClaudeSessionFileRelay(sessionID string, conn Connection, backendID string) {
	h.startClaudeSessionFileRelayAt(sessionID, conn, backendID, nil)
}

func (h *Handlers) startClaudeSessionFileRelayAt(
	sessionID string,
	conn Connection,
	backendID string,
	initialOffset *int64,
) {
	if backendID != "claude" && backendID != "claudecode" {
		return
	}
	h.mu.Lock()
	kind := h.relayRunningKind[sessionID]
	running := h.relayRunning[sessionID]
	switch {
	case running && kind == relayKindClaudeFile:
		// Already the UUID content source.
		h.mu.Unlock()
		return
	case running && kind == relayKindAgent:
		// Agent sidecar holds the global slot for control-plane. Promote kind to
		// claude_file so this loop is the content source without killing agent
		// (agent continues via agentRelayRunning). Without this, local send that
		// started agent first never attached file-relay → no mid-turn projection_patch.
		h.relayRunningKind[sessionID] = relayKindClaudeFile
		h.mu.Unlock()
		go h.claudeSessionFileRelayLoop(sessionID, conn, backendID, initialOffset)
		return
	case !running:
		h.relayRunning[sessionID] = true
		h.relayRunningKind[sessionID] = relayKindClaudeFile
		h.mu.Unlock()
		go h.claudeSessionFileRelayLoop(sessionID, conn, backendID, initialOffset)
		return
	default:
		h.mu.Unlock()
		return
	}
}

func (h *Handlers) startCodexSessionFileRelay(sessionID string, conn Connection, backendID string, agent core.Agent) {
	if backendID != "codex" || agent == nil || agent.Name() != "codex" {
		return
	}
	locator, ok := agent.(core.TranscriptLocator)
	if !ok {
		return
	}
	relayKey := codexSessionFileRelayKey(sessionID)
	h.mu.Lock()
	running := h.relayRunning[relayKey]
	if !running {
		h.relayRunning[relayKey] = true
	}
	h.mu.Unlock()
	if running {
		return
	}

	go h.codexSessionFileRelayLoop(sessionID, conn, backendID, relayKey, locator)
}

func grokLeaderRelayKey(sessionID string) string {
	return "grok-leader:" + sessionID
}

// startGrokLeaderSessionRelay attaches a read-only grok leader-socket subscriber
// for one session (external grok CLI turn). Mirrors startCodexSessionFileRelay but
// for the leader-socket path (Phase 1 Grok). Self-gates on backendID == "grokbuild".
func (h *Handlers) startGrokLeaderSessionRelay(sessionID, backendID string, agent core.Agent, directory string) {
	if backendID != "grokbuild" {
		return
	}
	sub, ok := agent.(core.SessionEventSubscriber)
	if !ok {
		return
	}
	cwd := directory
	if cwd == "" {
		if wd, ok := agent.(core.WorkDirSwitcher); ok {
			cwd = wd.GetWorkDir()
		}
	}
	relayKey := grokLeaderRelayKey(sessionID)
	h.mu.Lock()
	running := h.relayRunning[relayKey]
	if !running {
		h.relayRunning[relayKey] = true
	}
	h.mu.Unlock()
	if running {
		return
	}
	go h.grokLeaderSessionRelayLoop(sessionID, backendID, sub, relayKey, cwd)
}

// grokLeaderSessionRelayLoop forwards live leader-socket events (already converted
// to core.Event via convertSessionUpdate) to clients via the same wire path as
// local turns (mapAgentEvent + sendSessionEvent). Exits when the leader disconnects
// (channel close); the next session-open restarts it.
//
// Turn 生命周期合成 (Mac 发起的外部 turn 没有 iOS 发起路径里的 EventTurnStarted):
//   - turn_started: 上游 grok-build 不发任何 turn-start sessionUpdate (真实数据
//     response_started=0), 所以在首个内容事件前合成 turn_started + running, 激活
//     iOS isGenerating。turnId 用首个内容事件携带的 _meta.promptId (== 后续
//     turn_completed 的 prompt_id, convertSessionUpdate 已透传到 ev.TurnID)——SSV2
//     projection reducer 的 turn_started 分支要求 source-proven turnId 才会 arm
//     ActiveTurnID (projection_reducer.go:465), 留空会被 skip, 后续 tool_started 无
//     active turn 可挂。promptId 与 turn_completed 收口键一致, 不跳变。
//   - turn_completed: 由 convertSessionUpdate 的 case "turn_completed" 映射上游 durable
//     终态信号产生 (主收口), 这里只负责 markIdle + 重置 turnArmed。
//   - defer 中断: 仅当 leader 异常断开 (channel close) 且未收到 turn_completed 时
//     兜底, 合成 turn_aborted(leader_disconnect) + idle——turn 结果未知, 必须以
//     「中断」收口而非猜「完成」(F-7); 正常收口不经过这里。
func (h *Handlers) grokLeaderSessionRelayLoop(sessionID, backendID string, sub core.SessionEventSubscriber, relayKey, cwd string) {
	// 内容事件: 首个到达时触发 turn_started 合成。todos_updated (plan) 不算内容,
	// 因为它可能在 turn 真正开始前就到达, 误触发执行态。
	isContentEvent := func(eventName string) bool {
		switch eventName {
		case "text_delta", "reasoning_delta", "tool_started", "tool_finished":
			return true
		}
		return false
	}

	var turnArmed bool
	// armedTurnID 是首个内容事件透传的 promptId —— user_message_chunk 不带 promptId,
	// 需要用同 turn 的 promptId 补身份 (SSV2 reducer 对 identityless 的 user_message
	// 直接 skip, iOS 会只看到回复看不到 prompt)。
	var armedTurnID string
	// pendingUserText 缓冲外部 turn 的用户 prompt (attach 补扫或 live user_message_chunk),
	// 等 turn 身份确定后一次性以 user_message 送入投影。
	var pendingUserText string
	defer func() {
		// leader 异常断开且未收 turn_completed：turn 结果未知（可能仍在跑，也可能已死）。
		// F-7（2026-08-15 登记簿）：不再把「结果未知」静默猜成「已完成」——先合成
		// turn_aborted(leader_disconnect) 明确中断语义（对齐 codex 死进程先例 :423-432，
		// 协议 bridge-v1.md「turn_error/turn_aborted settle a turn as failed/aborted」），
		// 再补 idle 让客户端收口、防 isGenerating 残留（2026-08-04 修复保留）。
		if turnArmed {
			slog.Info("go-bridge: grokLeaderSessionRelay leader disconnect with armed turn, emitting turn_aborted(leader_disconnect) + idle", "sessionID", sessionID)
			h.sessions.markIdle(sessionID)
			h.sendSessionEvent(sessionID, backendID, "turn_aborted", map[string]interface{}{
				"turnId": armedTurnID, "reason": "leader_disconnect",
			})
			h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "idle"})
		}
		h.mu.Lock()
		delete(h.relayRunning, relayKey)
		h.mu.Unlock()
		slog.Info("go-bridge: grokLeaderSessionRelay exited", "sessionID", sessionID)
	}()
	events, err := sub.SubscribeSessionEvents(context.Background(), sessionID, cwd)
	if err != nil {
		slog.Debug("go-bridge: grok leader subscribe unavailable", "sessionID", sessionID, "error", err)
		return
	}
	slog.Info("go-bridge: grokLeaderSessionRelay started", "sessionID", sessionID)
	for ev := range events {
		eventName, data, _ := mapAgentEvent(ev)
		if eventName == "" {
			continue
		}
		if eventName == "user_message" && ev.TurnID == "" {
			// 身份延迟的 user prompt (codec 的 user_message_chunk): 挂起, 等首个
			// 内容事件用同 turn 的 promptId 补齐后再发, 不在此处猜身份。
			if text := strings.TrimSpace(ev.Content); text != "" {
				pendingUserText = text
			}
			continue
		}
		// 首个内容事件前合成 turn_started + running (Mac 外部 turn 无上游 start 信号)。
		// turnId 取自首个内容事件的 ev.TurnID (= convertSessionUpdate 透传的 _meta.promptId),
		// 让 reducer arm ActiveTurnID 后续 tool/text 才有 turn 可挂。
		if isContentEvent(eventName) && !turnArmed {
			turnArmed = true
			armedTurnID = ev.TurnID
			h.sessions.markRunning(sessionID)
			slog.Info("go-bridge: grokLeaderSessionRelay SYNTHESIZE turn_started+running", "sessionID", sessionID, "firstContent", eventName, "turnId", ev.TurnID)
			h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": ev.TurnID})
			if pendingUserText != "" {
				h.sendSessionEvent(sessionID, backendID, "user_message", map[string]interface{}{
					"itemId": ev.TurnID,
					"turnId": ev.TurnID,
					"text":   pendingUserText,
				})
				pendingUserText = ""
			}
			h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
		} else if eventName == "user_message" && turnArmed && armedTurnID != "" {
			// 内容事件先到、prompt 后到 (异常顺序): 用已 arm 的 turn 身份补发。
			h.sendSessionEvent(sessionID, backendID, "user_message", map[string]interface{}{
				"itemId": armedTurnID,
				"turnId": armedTurnID,
				"text":   ev.Content,
			})
			continue
		}
		if eventName == "turn_completed" || eventName == "error" {
			// 从未收到内容事件的 turn (空回复): 终态自带 promptId, 此时补发挂起的
			// prompt, 让 iOS 至少看到用户问题 + 完成收口, 而不是只看到空回复。
			if pendingUserText != "" && ev.TurnID != "" {
				h.sendSessionEvent(sessionID, backendID, "user_message", map[string]interface{}{
					"itemId": ev.TurnID,
					"turnId": ev.TurnID,
					"text":   pendingUserText,
				})
			}
			pendingUserText = ""
			armedTurnID = ""
			// 上游 durable 终态信号到达 (convertSessionUpdate 映射) → 主收口。
			h.sessions.markIdle(sessionID)
			turnArmed = false
			slog.Info("go-bridge: grokLeaderSessionRelay turn terminal", "sessionID", sessionID, "event", eventName)
		} else if eventName == "turn_started" {
			h.sessions.markRunning(sessionID)
		}
		h.sendSessionEvent(sessionID, backendID, eventName, data)
	}
}

func (h *Handlers) sessionLiveProcess(ctx context.Context, sessionID, backendID string) (core.LiveSessionProcess, core.LiveSessionLister, error) {
	// Codex has no per-session process-stub LiveSessionLister (unlike Claude's session-stubs).
	// The explicit lifecycle signal is the sessionRegistry state maintained by the passive
	// app-server subscriber (running/idle from turn_started / session_state_changed /
	// turn_completed) and by the codex file relay (task_started/task_complete/turn_aborted).
	// isIdle defaults to true for unknown sessions, so this is strictly "registered AND
	// running" — a session the bridge has never seen is not treated as live. Short-circuit
	// before the agent-lister loop so codex never falls through to the claudecode stub lookup.
	if backendID == "codex" {
		if !h.sessions.isIdle(sessionID) {
			return core.LiveSessionProcess{SessionID: sessionID, Live: true}, nil, nil
		}
		return core.LiveSessionProcess{SessionID: sessionID}, nil, nil
	}
	seen := make(map[string]bool)
	for _, id := range []string{backendID, "claude", "claudecode"} {
		if strings.TrimSpace(id) == "" || seen[id] {
			continue
		}
		seen[id] = true
		agent, ok := h.getAgent(id)
		if !ok {
			continue
		}
		lister, ok := agent.(core.LiveSessionLister)
		if !ok {
			continue
		}
		proc, err := lister.LiveSessionProcess(ctx, sessionID)
		return proc, lister, err
	}

	agent, ok := h.getFirstAgentByName("claudecode")
	if !ok {
		return core.LiveSessionProcess{SessionID: sessionID}, nil, nil
	}
	lister, ok := agent.(core.LiveSessionLister)
	if !ok {
		return core.LiveSessionProcess{SessionID: sessionID}, nil, nil
	}
	proc, err := lister.LiveSessionProcess(ctx, sessionID)
	return proc, lister, err
}

func codexSessionFileRelayKey(sessionID string) string {
	return "codex-file:" + sessionID
}

// codexSessionHasSubscriber reports whether a client (iOS) is currently connected
// and subscribed to this session — i.e. iOS still has it open. The file relay uses
// this to keep watching (push model) while iOS is interested, instead of exiting on
// idle TTL and missing the external turn.
func (h *Handlers) codexSessionHasSubscriber(sessionID, backendID string) bool {
	if h.broadcaster == nil {
		return false
	}
	return h.broadcaster.HasSessionSubscriber(backendID, sessionID)
}

func (h *Handlers) codexSessionFileRelayLoop(sessionID string, conn Connection, backendID string, relayKey string, locator core.TranscriptLocator) {
	defer func() {
		h.mu.Lock()
		delete(h.relayRunning, relayKey)
		h.mu.Unlock()
		slog.Info("go-bridge: codexSessionFileRelay exited", "sessionID", sessionID)
	}()

	sessPath, err := locator.TranscriptPath(context.Background(), sessionID)
	if err != nil || strings.TrimSpace(sessPath) == "" {
		slog.Debug("go-bridge: codexSessionFileRelay no transcript file found", "sessionID", sessionID, "error", err)
		return
	}
	slog.Info("go-bridge: codexSessionFileRelay started", "sessionID", sessionID, "path", sessPath)

	offset := func() int64 {
		info, err := os.Stat(sessPath)
		if err != nil {
			return 0
		}
		return info.Size()
	}()

	state, currentTurnID := h.detectCodexTranscriptTask(sessPath)
	switch state {
	case "idle":
		h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"turnId": currentTurnID, "done": true, "reason": "task_complete"})
		h.broadcastIdleState(sessionID, backendID)
		// Phase 0 修复：idle 不再 return。此前 return 导致 relay 连 watch loop 都不进，
		// 下一轮 task_started 永远到不了客户端（只能等下次 get_session_messages 顺带看到）。
		// 与 Claude relay 对齐：广播当前态后继续 watch，等下一个 turn。
		slog.Info("go-bridge: codexSessionFileRelay initial idle; watching for next turn", "sessionID", sessionID)
	case "running":
		h.sessions.markRunning(sessionID)
		h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": currentTurnID})
		h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
	}

	ticker := time.NewTicker(codexFileRelayPollInterval)
	defer ticker.Stop()
	// lastGrowth 在文件增长时刷新；无增长超过软 TTL 时复核 task state（仍 running 续 watch），
	// 超过硬上限直接退出（死进程兜底）。recheckedAfterTTL 确保软 TTL 每个静默窗口只复核
	// 一次，避免每个 tick 都全量重扫大 rollout 文件。
	lastGrowth := time.Now()
	recheckedAfterTTL := false
	// seen 是 per-turn 内容去重集合（r2/r3/r5/r6：message 与 reasoning 双源、元素口径、
	// 按 turn 重置）。同一 turn 内相同文本（双写过渡态 / 跨记录重复）只发一次。
	seen := make(map[string]bool)
	// toolNames 记录 call_id → toolName，让 custom_tool_call_output（无 name）的
	// tool_finished 能带上对应 custom_tool_call 的 name。按 turn 重置。
	toolNames := make(map[string]string)

	for range ticker.C {
		info, err := os.Stat(sessPath)
		if err != nil {
			continue
		}
		newSize := info.Size()
		if newSize <= offset {
			if newSize < offset {
				// 文件被截断重写（truncate），重置 offset 从头扫。
				offset = 0
				lastGrowth = time.Now()
				recheckedAfterTTL = false
				continue
			}
			since := time.Since(lastGrowth)
			// hardCap 判定进程死亡（既有语义：「超限几乎可判定进程已死」），并回收 goroutine。
			// §3.3：若此时仍 armed 一个未终结 turn（transcript 尾部 task_started、registry 仍
			// running），合成 turn_aborted 收口投影 —— 否则 crashed codex session 会在投影里留下
			// 永久 running turn。该判定不因有订阅者而跳过：死进程不会再写文件，下一轮真实 turn
			// 由 codex relay watcher / 下次打开补启 relay。
			if codexFileRelayNoGrowthHardCap > 0 && since >= codexFileRelayNoGrowthHardCap {
				if currentTurnID != "" && !h.sessions.isIdle(sessionID) &&
					h.detectCodexTranscriptTaskState(sessPath) == "running" {
					h.sendSessionEvent(sessionID, backendID, "turn_aborted", map[string]interface{}{
						"turnId": currentTurnID, "reason": "process_death",
					})
					slog.Info("go-bridge: codexSessionFileRelay synthesized turn_aborted for dead process",
						"sessionID", sessionID, "backendID", backendID, "turnId", currentTurnID)
				}
				if !h.sessions.isIdle(sessionID) {
					h.broadcastIdleState(sessionID, backendID)
				}
				slog.Info("go-bridge: codexSessionFileRelay no-growth hardCap elapsed, exiting", "sessionID", sessionID, "idleFor", since.String(), "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
				return
			}
			// iOS 仍订阅该 session 时持续 watch（push 模型）：打开 idle 外部 session 后 iOS 常停
			// 轮询，若 relay 在 Mac 端稍后发 turn 前退出会错过整轮（owner 复现：打开 idle session →
			// relay 90s 退出 → 后来发任务 → 无 live 同步）。软 TTL 不再作为退出门槛；只有 hardCap
			// 回收 goroutine。running 复核保留（仍 running → 续 watch）。
			if h.codexSessionHasSubscriber(sessionID, backendID) {
				continue
			}
			if codexFileRelayNoGrowthTTL > 0 && since >= codexFileRelayNoGrowthTTL {
				if !recheckedAfterTTL {
					recheckedAfterTTL = true
					if h.detectCodexTranscriptTaskState(sessPath) == "running" {
						slog.Info("go-bridge: codexSessionFileRelay no-growth TTL elapsed but task still running; keep watching", "sessionID", sessionID, "idleFor", since.String())
					} else {
						slog.Info("go-bridge: codexSessionFileRelay no-growth TTL elapsed, idle — keep watching until hardCap (no-subscriber no longer exits)", "sessionID", sessionID, "idleFor", since.String(), "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
					}
				}
			}
			continue
		}

		events := scanCodexTranscriptRelayEvents(sessPath, offset)
		if len(events) > 0 {
			slog.Info("go-bridge: codexSessionFileRelay growth", "sessionID", sessionID, "bytes", newSize-offset, "events", len(events), "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
		}
		offset = newSize
		lastGrowth = time.Now()
		recheckedAfterTTL = false
		for _, ev := range events {
			switch ev.kind {
			case "task_started":
				currentTurnID = ev.turnID
				slog.Info("go-bridge: codexSessionFileRelay EMIT turn_started", "sessionID", sessionID, "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
				// per-turn 内容去重 seen-set：turn_started 清空（r5/r6 元素口径去重）。
				seen = make(map[string]bool)
				toolNames = make(map[string]string)
				h.sessions.markRunning(sessionID)
				h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": currentTurnID})
				h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
			case "task_complete":
				completedTurnID := ev.turnID
				if completedTurnID == "" {
					completedTurnID = currentTurnID
				}
				slog.Info("go-bridge: codexSessionFileRelay EMIT turn_completed", "sessionID", sessionID)
				h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"turnId": completedTurnID, "done": true, "reason": "task_complete"})
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "completed", "task_complete")
				currentTurnID = ""
				// task_complete 后继续 watch 下一轮（Phase 0）；relay 靠无增长 TTL 退出。
			case "turn_aborted":
				// §5.1 #7 producer layer 3（live file-relay）：rollout 增长出 turn_aborted
				// （真实形态 019f5453）→ 收口 active turn（reducer turn_aborted 终态）+ idle。
				// 不发 completed 通知（用户中断，非完成）。清空 currentTurnID 让 watch-loop 的
				// detectCodexTranscriptTask 复核判 idle，及时停止 watch 该 stale 文件。
				abortedTurnID := ev.turnID
				if abortedTurnID == "" {
					abortedTurnID = currentTurnID
				}
				slog.Info("go-bridge: codexSessionFileRelay EMIT turn_aborted", "sessionID", sessionID)
				h.sendSessionEvent(sessionID, backendID, "turn_aborted", map[string]interface{}{"turnId": abortedTurnID, "reason": "turn_aborted"})
				h.broadcastIdleState(sessionID, backendID)
				currentTurnID = ""
			case "text":
				if seen[ev.text] {
					continue
				}
				seen[ev.text] = true
				slog.Info("go-bridge: codexSessionFileRelay EMIT text_delta", "sessionID", sessionID, "len", len(ev.text), "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
				h.sendSessionEvent(sessionID, backendID, "text_delta", map[string]interface{}{"itemId": currentTurnID, "delta": ev.text})
			case "reasoning":
				if seen[ev.text] {
					continue
				}
				seen[ev.text] = true
				slog.Info("go-bridge: codexSessionFileRelay EMIT reasoning_delta", "sessionID", sessionID, "len", len(ev.text), "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
				h.sendSessionEvent(sessionID, backendID, "reasoning_delta", map[string]interface{}{"itemId": currentTurnID, "delta": ev.text})
			case "user_message":
				key := "user:" + ev.itemId + ":" + ev.text
				if seen[key] {
					continue
				}
				seen[key] = true
				h.sendSessionEvent(sessionID, backendID, "user_message", map[string]interface{}{
					"itemId": ev.itemId,
					"turnId": currentTurnID,
					"text":   ev.text,
				})
			case "tool_started":
				if ev.itemId != "" {
					toolNames[ev.itemId] = ev.toolName
				}
				payload := map[string]interface{}{"toolName": ev.toolName, "toolInput": ev.toolInput}
				if ev.itemId != "" {
					payload["itemId"] = ev.itemId
				}
				h.sendSessionEvent(sessionID, backendID, "tool_started", payload)
			case "tool_finished":
				payload := map[string]interface{}{
					"toolResult": ev.toolResult,
					"toolStatus": "completed",
				}
				if name, ok := toolNames[ev.itemId]; ok && name != "" {
					payload["toolName"] = name
				}
				if ev.itemId != "" {
					payload["itemId"] = ev.itemId
				}
				h.sendSessionEvent(sessionID, backendID, "tool_finished", payload)
			case "context_usage":
				h.sendSessionEvent(sessionID, backendID, "context_usage_updated", map[string]interface{}{"context": ev.context})
			}
		}
	}
}

// codexRelayWatcherInterval is the safety-net cadence: ensures every codex session a
// client currently has open (subscribed) has a running file relay. Catches cases where a
// relay exited (e.g. hardCap after a transient no-subscriber window) or never started
// despite renewed interest. Layer 2 on top of the relay's own keep-watching logic.
var codexRelayWatcherInterval = 10 * time.Second

// StartCodexRelayWatcher launches the safety-net that keeps a file relay running for
// every codex session a client is subscribed to. Mirror of StartSessionDiscoveryWatcher.
func (h *Handlers) StartCodexRelayWatcher(ctx context.Context) {
	go h.runCodexRelayWatcher(ctx)
}

func (h *Handlers) runCodexRelayWatcher(ctx context.Context) {
	ticker := time.NewTicker(codexRelayWatcherInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.ensureRelaysForSubscribedCodexSessions()
		}
	}
}

// ensureRelaysForSubscribedCodexSessions starts a file relay for every codex session a
// client is subscribed to but that has no running relay. startCodexSessionFileRelay is
// idempotent (relayRunning guard); conn is nil because codexSessionFileRelayLoop does not
// use it (events are broadcast to subscribers via the broadcaster).
func (h *Handlers) ensureRelaysForSubscribedCodexSessions() {
	agent, ok := h.Agents()["codex"]
	if !ok || agent == nil || agent.Name() != "codex" {
		return
	}
	for _, sessionID := range h.broadcaster.SubscribedSessionIDs("codex") {
		// Lazy-create placeholders never have a rollout JSONL; restarting a file
		// relay for pending-* only burns CPU and log noise. Prefer the resolved
		// real id (registry alias) when available.
		if strings.HasPrefix(sessionID, "pending-") {
			if t, ok := h.sessions.get(sessionID); ok && t != nil && t.sessionID != "" && t.sessionID != sessionID {
				sessionID = t.sessionID
			} else {
				continue
			}
		}
		h.mu.Lock()
		running := h.relayRunning[codexSessionFileRelayKey(sessionID)]
		h.mu.Unlock()
		if running {
			continue
		}
		slog.Info("go-bridge: codex relay watcher restarting relay for subscribed session", "sessionID", sessionID)
		h.startCodexSessionFileRelay(sessionID, nil, "codex", agent)
	}
}

func (h *Handlers) claudeSessionFileRelayLoop(
	sessionID string,
	conn Connection,
	backendID string,
	initialOffset *int64,
) {
	defer func() {
		h.clearRelayKindIf(sessionID, relayKindClaudeFile)
		slog.Info("go-bridge: claudeSessionFileRelay exited", "sessionID", sessionID)
	}()

	_, sessPath := h.findClaudeSessionFile(sessionID, "")
	if sessPath == "" {
		slog.Debug("go-bridge: claudeSessionFileRelay no transcript file found", "sessionID", sessionID)
		return
	}
	slog.Info("go-bridge: claudeSessionFileRelay started", "sessionID", sessionID, "path", sessPath)
	if !h.relayKindIs(sessionID, relayKindClaudeFile) {
		slog.Info("go-bridge: claudeSessionFileRelay superseded before initial scan", "sessionID", sessionID)
		return
	}

	// Start at the last complete JSONL record, not raw file size. If Claude currently owns an
	// unterminated tail, the relay must retain that row and read it from its beginning after the
	// terminating delimiter arrives.
	offset, err := projectionJSONLStartCut(sessPath)
	if err != nil {
		slog.Error("go-bridge: claudeSessionFileRelay initial complete-record cut failed",
			"sessionID", sessionID, "backendID", backendID, "error", err)
		return
	}
	if initialOffset != nil {
		if *initialOffset < 0 || *initialOffset > offset {
			slog.Error("go-bridge: claudeSessionFileRelay rejected invalid inherited cursor",
				"sessionID", sessionID, "backendID", backendID,
				"inheritedCursor", *initialOffset, "completeCut", offset)
			return
		}
		offset = *initialOffset
	}
	initialInfo, err := os.Stat(sessPath)
	if err != nil {
		slog.Error("go-bridge: claudeSessionFileRelay initial stat failed",
			"sessionID", sessionID, "backendID", backendID, "error", err)
		return
	}
	observedSize := initialInfo.Size()
	observedModTime := initialInfo.ModTime()
	if initialOffset != nil && offset < initialInfo.Size() {
		// Force the first poll to consume bytes appended after hydrate admission/commit. The
		// continuous reader inherits the committed cursor; it never resamples raw file size.
		observedSize = offset
		observedModTime = time.Time{}
	}
	// Install the Mac-private source ledger at relay startup using the inherited complete-record cut
	// as the admission cut, so the relay is self-sufficient even when launched without hydrate
	// (hydrate commit also installs, idempotently). Must precede the Observe so the first batch's
	// segment generation matches the installed cursor (Session Sync v2 guardrails #1/#5/#6).
	h.ensureClaudeSourceStateInstalled(backendID, sessionID, sessPath, offset)
	// Compute the segment correlation once at relay startup. Needed for every live source batch
	// (the Kernel cursor/gap/generation fence keys on SegmentStableKey/SegmentGeneration) and reused
	// for the trace label. The startup cut equals the hydrate committed cut, so this matches the
	// ledger cursor installed above. No per-poll cost: the generation is stable per file incarnation.
	traceCorrelation, err := h.claudeSourceCorrelation.Observe(
		backendID, sessionID, sessionID, sessPath, offset,
	)
	if err != nil {
		slog.Warn("go-bridge: Claude source correlation unavailable",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
	}
	var scanState claudeRelayScanState
	var quarantined *claudeRelayPoisonRecord
	var quarantineSize int64
	var quarantineModTime time.Time
	var quarantineRetryAt time.Time
	var quarantineBackoff time.Duration
	var quarantineSourceChanged bool

	initialEntry := h.classifyClaudeTranscriptFile(sessPath)
	proc, liveLister, err := h.sessionLiveProcess(context.Background(), sessionID, backendID)
	if err != nil {
		slog.Warn("go-bridge: claudeSessionFileRelay live process lookup failed", "sessionID", sessionID, "backendID", backendID, "error", err)
	}
	live := err == nil && proc.Live
	cachedPID := 0
	if live {
		cachedPID = proc.PID
	} else {
		// Process not live yet (or not discoverable): still watch the transcript.
		// Do not cache a non-live PID — IsProcessAlive would immediately treat it as
		// "process death" and exit. Late-bind when a real live PID appears.
		h.broadcastIdleState(sessionID, backendID)
		slog.Info("go-bridge: claudeSessionFileRelay initial process not live; watching transcript for growth", "sessionID", sessionID, "backendID", backendID, "pid", proc.PID)
	}

	// 开始轮询监视新内容（process live 与否都继续；live 只影响 death/TTL 路径）。
	ticker := time.NewTicker(claudeFileRelayPollInterval)
	defer ticker.Stop()
	lastMeaningfulGrowth := time.Now()
	runningObserved := false
	processDeathMisses := 0

	// Seed turn identity from last user on disk so warm-start / mid-turn open can
	// attribute subsequent assistant growth without empty turnId frames.
	currentTurnID := lastClaudeUserIdentityFromPath(sessPath, offset)
	// Dedup guard for §3.3 process-death turn_aborted synthesis: at most one synthesized abort
	// per turn identity, so a replacement process dying on a LATER turn still synthesizes while
	// the same turn is never double-aborted.
	lastSynthesizedAbortTurnID := ""

	if !live {
		// No discoverable process: stay idle and watch for future transcript growth.
		// Do not arm running from historical tail (process may have died mid-turn).
		h.broadcastIdleState(sessionID, backendID)
	} else {
		switch {
		case !initialEntry.hasMeaningfulEntry || initialEntry.finalAssistant:
			h.broadcastIdleState(sessionID, backendID)
			slog.Info("go-bridge: claudeSessionFileRelay initial idle but process live; watching", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
		case initialEntry.entryType == "user" && initialEntry.interrupt:
			h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"turnId": currentTurnID, "done": true, "reason": "user_interrupt"})
			h.broadcastIdleState(sessionID, backendID)
			slog.Info("go-bridge: claudeSessionFileRelay initial interrupt marker with live process; watching", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
		case initialEntry.entryType == "user":
			h.sessions.markRunning(sessionID)
			if currentTurnID != "" {
				h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": currentTurnID})
			} else {
				// No stable identity yet — still arm running so legacy consumers see activity,
				// but do not emit empty-turnId turn_started (reducer would skip it).
				h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
			}
			runningObserved = true
		case initialEntry.entryType == "assistant":
			h.sessions.markRunning(sessionID)
			h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
			runningObserved = true
		}
	}

	for range ticker.C {
		if !h.relayKindIs(sessionID, relayKindClaudeFile) {
			slog.Info("go-bridge: claudeSessionFileRelay superseded by agent relay", "sessionID", sessionID)
			return
		}
		if cachedPID == 0 {
			// Late bind: process may appear after open (owner opens idle B, then Mac starts turn).
			if proc2, lister2, err2 := h.sessionLiveProcess(context.Background(), sessionID, backendID); err2 == nil && proc2.Live && proc2.PID > 0 {
				// A process catalog can briefly retain a just-dead worker. Verify the PID before
				// binding so a subscribed watcher does not churn dead→bind on every poll.
				if lister2 == nil || lister2.IsProcessAlive(context.Background(), proc2.PID) {
					cachedPID = proc2.PID
					if lister2 != nil {
						liveLister = lister2
					}
					slog.Info("go-bridge: claudeSessionFileRelay bound live process", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
				}
			}
		}
		if liveLister != nil && cachedPID > 0 {
			if !liveLister.IsProcessAlive(context.Background(), cachedPID) {
				processDeathMisses++
				if processDeathMisses >= claudeFileRelayProcessDeathMisses {
					// §3.3 (required, not optional): a dead process with a non-terminal transcript
					// tail must close the in-flight turn with turn_aborted, mirroring the codex
					// producer. Without it a crashed Claude session stays a non-live cold-armed
					// turn with no terminal event, and the hydrate commit gate (which only
					// releases non-live armed turns once every armed turn is terminal) would stay
					// hydrating forever. Synthesis feeds pendingLive during hydrate and the
					// committed reducer afterwards.
					h.synthesizeClaudeTurnAbortedIfNeeded(sessPath, sessionID, backendID, currentTurnID, &lastSynthesizedAbortTurnID)
					runningObserved = false
					h.broadcastIdleState(sessionID, backendID)
					if !h.broadcaster.HasSessionSubscriber(backendID, sessionID) {
						slog.Info("go-bridge: claudeSessionFileRelay process dead with no subscriber, exiting", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
						return
					}
					// Claude Desktop may end one worker and later append another turn to the
					// same transcript from a replacement process. The open client subscription,
					// not the lifetime of one PID, owns this watcher. Forget the stale PID and
					// return to late-binding mode while continuing to observe file growth.
					slog.Info("go-bridge: claudeSessionFileRelay process dead; keeping subscribed transcript watch", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
					cachedPID = 0
					liveLister = nil
					processDeathMisses = 0
				}
			} else {
				processDeathMisses = 0
			}
		}
		info, err := os.Stat(sessPath)
		if err != nil {
			continue
		}
		newSize := info.Size()
		sourceChanged := newSize != observedSize || !info.ModTime().Equal(observedModTime)
		observedSize = newSize
		observedModTime = info.ModTime()
		if quarantined != nil {
			if sourceChanged {
				quarantineSourceChanged = true
			}
			if !quarantineSourceChanged || time.Now().Before(quarantineRetryAt) {
				continue
			}
			// A byte-level source change is the only automatic retry trigger. Keep the cursor before
			// the poison row; appends after an unrepaired poison row therefore cannot leapfrog it.
			quarantined = nil
			quarantineSourceChanged = false
			// The triggering change may have happened before the backoff elapsed; retain that
			// pending rescan even when the current stat tick itself is unchanged.
			sourceChanged = true
			slog.Info("go-bridge: claudeSessionFileRelay retrying quarantined source after change",
				"sessionID", sessionID, "backendID", backendID,
				"previousSize", quarantineSize, "currentSize", newSize,
				"previousModTime", quarantineModTime, "currentModTime", info.ModTime())
		}
		if !sourceChanged {
			processStillLive := cachedPID > 0 && liveLister != nil && liveLister.IsProcessAlive(context.Background(), cachedPID)
			if !runningObserved && !processStillLive &&
				!h.broadcaster.HasSessionSubscriber(backendID, sessionID) &&
				claudeFileRelayLiveIdleTTL > 0 && time.Since(lastMeaningfulGrowth) >= claudeFileRelayLiveIdleTTL {
				if !h.sessions.isIdle(sessionID) {
					h.broadcastIdleState(sessionID, backendID)
				}
				slog.Info("go-bridge: claudeSessionFileRelay live-idle TTL elapsed, exiting", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
				return
			}
			continue
		}
		if newSize <= offset {
			// 文件没有增长，可能被截断重写（truncate）。
			if newSize < offset {
				offset = 0
				scanState = claudeRelayScanState{}
				lastMeaningfulGrowth = time.Now()
				continue
			}
			continue
		}

		// 读取新增内容。
		f, err := os.Open(sessPath)
		if err != nil {
			continue
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			continue
		}

		scan, err := scanCompleteClaudeRelayEntriesFromReader(f, offset, &scanState)
		f.Close()
		if err != nil {
			continue
		}
		offset += scan.ConsumedBytes
		if len(scan.Entries) > 0 {
			lastMeaningfulGrowth = time.Now()
		}

		// Live growth reuses the same hydrate mapper so projection reducer receives
		// identity-bearing user_message / text_delta / turn_completed frames (not bare
		// turn_started turnId:"" or itemId-less deltas that reducer skips).
		for _, scanned := range scan.Records {
			e := scanned.Entry
			if !scanned.Admitted {
				h.acknowledgeClaudeSourceRow(backendID, sessionID, traceCorrelation, scanned.ByteEnd)
				emitClaudeSourceTrace(claudeSourceTraceRecord{
					Phase: "live", IngestDomain: "source_only", BackendID: backendID,
					SessionID: sessionID, Correlation: traceCorrelation, Record: scanned,
					FileOrderTurnID: currentTurnID, Transition: "ignored_source_only",
				})
				continue
			}
			// Interrupt markers usually emit no projection events from the mapper; keep
			// legacy interrupt → completed/idle behaviour for live consumers.
			if e.Type == "user" && isClaudeUserInterruptRelayEntry(e) {
				h.acknowledgeClaudeSourceRow(backendID, sessionID, traceCorrelation, scanned.ByteEnd)
				emitClaudeSourceTrace(claudeSourceTraceRecord{
					Phase: "live", IngestDomain: "live", BackendID: backendID,
					SessionID: sessionID, Correlation: traceCorrelation, Record: scanned,
					FileOrderTurnID: currentTurnID, Transition: "accepted_lifecycle",
					ProjectionEvents: 1, ProjectionTurnID: currentTurnID,
				})
				h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"turnId": currentTurnID, "done": true, "reason": "user_interrupt"})
				h.broadcastIdleState(sessionID, backendID)
				runningObserved = false
				continue
			}
			// Admitted content rows (user/assistant with UUID+message) route through the Kernel-
			// private source-batch transaction — the sole projection writer for Claude content
			// (guardrail #3). H3 exact-replay / H4 graph dedup applies; exact replays mutate nothing
			// and deliver nothing. currentTurnID is threaded as the file-order fallback turn for rows
			// whose parent chain has no admitted owner (mapper degrades to file-order attribution,
			// not content refereeing, guardrail #4).
			if e.UUID != "" && e.Message != nil && (e.Type == "user" || e.Type == "assistant") {
				h.applyClaudeLiveSourceRecord(scanned, backendID, sessionID, traceCorrelation, &currentTurnID, &runningObserved, cachedPID)
				continue
			}
			// Admitted non-content rows (compaction-boundary system_message, etc.) keep legacy
			// per-event delivery via the projection writer; historical milestones, not replay
			// content, so not a dedup concern (guardrail #3). Acknowledge the byte range so the
			// ledger cursor stays contiguous with the next admitted content row (guardrail #6).
			h.acknowledgeClaudeSourceRow(backendID, sessionID, traceCorrelation, scanned.ByteEnd)
			h.deliverClaudeLegacyRow(e, sessionID, backendID, &currentTurnID, &runningObserved, cachedPID)
		}
		if scan.Poison != nil {
			quarantined = scan.Poison
			quarantineSize = newSize
			quarantineModTime = info.ModTime()
			if quarantineBackoff == 0 {
				quarantineBackoff = claudeFileRelayPollInterval
				if quarantineBackoff < 100*time.Millisecond {
					quarantineBackoff = 100 * time.Millisecond
				}
			} else {
				quarantineBackoff *= 2
				if quarantineBackoff > 30*time.Second {
					quarantineBackoff = 30 * time.Second
				}
			}
			quarantineRetryAt = time.Now().Add(quarantineBackoff)
			slog.Error("go-bridge: claudeSessionFileRelay quarantined invalid complete record",
				"sessionID", sessionID, "backendID", backendID,
				"byteStart", scan.Poison.ByteStart, "byteEnd", scan.Poison.ByteEnd,
				"retryable", true, "retryAfter", quarantineBackoff)
			h.projectionKernel.MarkFailed(
				backendID, sessionID, "projection.source_poison_record",
				"Claude transcript contains an invalid complete JSONL record", true,
			)
		} else {
			quarantineBackoff = 0
			quarantineRetryAt = time.Time{}
		}
	}
}

// acknowledgeClaudeSourceRow advances the Mac-private source ledger cursor past a source-only
// (non-projected) physical row (non-admitted control rows, interrupts, compaction boundaries) so
// the cursor stays contiguous with the next admitted content row. Without it the next content row
// would gap the fence and the transaction would reject (guardrail #6). Never projects or records a
// logical record (guardrail #1). Failure is logged, not fatal: the transaction is the authority.
func (h *Handlers) acknowledgeClaudeSourceRow(backendID, sessionID string, correlation claudeSourceCorrelation, byteEnd int64) {
	if correlation.SegmentStableKey == "" {
		return
	}
	if err := h.projectionKernel.AcknowledgeClaudeSourceRange(
		backendID, sessionID, correlation.SegmentStableKey, correlation.SegmentGeneration, byteEnd,
	); err != nil {
		slog.Warn("go-bridge: Claude source acknowledge failed (cursor may gap)",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
	}
}

// synthesizeClaudeTurnAbortedIfNeeded closes an in-flight Claude turn with turn_aborted when
// the live process died without a final stop_reason in the transcript (crashed scenario; §3.3
// rule #2 / D6 producer-layer semantics, mirroring the codex producer). The hydrate commit gate
// (WaitHydrateCommitReady) only releases non-live cold-armed turns once every armed turn is
// terminal, so a crashed session would otherwise stay hydrating forever. Emits at most once per
// turn: lastSynthesized records the turnId already closed, so a replacement process dying on a
// later turn still synthesizes while the same turn is never double-aborted. No-op when the
// transcript tail is already terminal or no turn identity is known.
func (h *Handlers) synthesizeClaudeTurnAbortedIfNeeded(
	sessPath, sessionID, backendID, currentTurnID string,
	lastSynthesized *string,
) {
	if currentTurnID == "" || lastSynthesized == nil || *lastSynthesized == currentTurnID {
		return
	}
	last := h.classifyClaudeTranscriptFile(sessPath)
	if !last.hasMeaningfulEntry {
		return
	}
	inFlight := last.entryType == "user" && !last.interrupt
	if last.entryType == "assistant" && !last.finalAssistant {
		inFlight = true
	}
	if !inFlight {
		return
	}
	h.sendSessionEvent(sessionID, backendID, "turn_aborted", map[string]interface{}{
		"turnId": currentTurnID, "reason": "process_death",
	})
	*lastSynthesized = currentTurnID
	slog.Info("go-bridge: claudeSessionFileRelay synthesized turn_aborted for dead process",
		"sessionID", sessionID, "backendID", backendID, "turnId", currentTurnID)
}

// applyClaudeLiveSourceRecord routes one live Claude content record through the Kernel-private
// source-batch transaction — the sole projection writer for Claude content (guardrail #3). H3
// exact-replay (graph-only, projection-stable) and H4 parent-chain ownership resolve under the
// Kernel lock; an exact replay yields no projection mutation and no delivery (dedup). Accepted
// projection transitions deliver one authoritative projection_patch (flushed after the swap, never
// re-reduced) plus dual-send raw frames for legacy consumers (guardrail #3). On an un-projectable
// row the mapper degrades to file-order turn (no content refereeing, #4); only a genuine ledger
// inconsistency (gap/CAS) or missing ledger surfaces as MarkFailed (guardrail #10 — expose, do not
// silently legacy-fallback the main path).
func (h *Handlers) applyClaudeLiveSourceRecord(
	scanned claudeRelayScannedRecord,
	backendID, sessionID string,
	correlation claudeSourceCorrelation,
	currentTurnID *string,
	runningObserved *bool,
	cachedPID int,
) {
	ingestDomain := "live"
	hydrating := h.projectionKernel.Status(backendID, sessionID).Phase == ProjectionHydrateHydrating
	if hydrating {
		ingestDomain = "pending_live"
		// During hydrate the committed reducer is still the OLD baseline; the hydrate
		// transaction owns the authoritative reducer for [checkpoint, cut). Route this live
		// content row through the EventPublisher legacy path so IngestLive queues the
		// projection events into the transaction's pendingLive (applied atomically after the
		// baseline commits, §3.2 of the SSV2 running-session cold-open fix), and advance the
		// Mac-private source ledger past the row so the cursor stays contiguous for the next
		// live batch. The source-batch transaction cannot run here: it swaps the committed
		// reducer, which CommitHydrateTransaction would overwrite with the baseline Restore.
		// The mapper (claudeEntryToProjectionEvents) is identical to the batch path, so the
		// queued events are the same authoritative projection events the live path would emit.
		h.acknowledgeClaudeSourceRow(backendID, sessionID, correlation, scanned.ByteEnd)
		h.deliverClaudeLegacyRow(scanned.Entry, sessionID, backendID, currentTurnID, runningObserved, cachedPID)
		emitClaudeSourceTrace(claudeSourceTraceRecord{
			Phase: "live", IngestDomain: ingestDomain, BackendID: backendID, SessionID: sessionID,
			Correlation: correlation, Record: scanned, FileOrderTurnID: *currentTurnID,
			Transition: "queued_pending_live",
		})
		return
	}
	state, ok := h.projectionKernel.ClaudeSourceStateSnapshot(backendID, sessionID)
	if !ok {
		slog.Warn("go-bridge: Claude source ledger unavailable for live ingest",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID))
		emitClaudeSourceTrace(claudeSourceTraceRecord{
			Phase: "live", IngestDomain: ingestDomain, BackendID: backendID, SessionID: sessionID,
			Correlation: correlation, Record: scanned, FileOrderTurnID: *currentTurnID,
			Transition: "source_state_missing",
		})
		h.projectionKernel.MarkFailed(backendID, sessionID, "projection.source_state_missing",
			"Claude source ledger not installed for live ingest", true)
		return
	}
	batch, err := buildClaudeSourceRecordBatch(state, scanned, backendID, sessionID, h.eventPublisher.BridgeEpoch(), correlation, *currentTurnID)
	if err != nil {
		// Mapper cannot attribute this content row (no graph-resolved owner AND no file-order
		// fallback turn — an orphan row with no prior user). Expose honestly rather than auto-
		// fallback to legacy (guardrail #10: no auto-legacy, no silent degrade); the owning layer
		// re-syncs via projection pull. The common case (attributable rows, including the file-order
		// fallback) never reaches here, so this never masks the main path.
		slog.Warn("go-bridge: Claude source batch build failed; mark failed (orphan/unattributable row)",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
		emitClaudeSourceTrace(claudeSourceTraceRecord{
			Phase: "live", IngestDomain: ingestDomain, BackendID: backendID, SessionID: sessionID,
			Correlation: correlation, Record: scanned, FileOrderTurnID: *currentTurnID,
			Transition: "batch_build_failed_marked_failed",
		})
		h.projectionKernel.MarkFailed(backendID, sessionID, "projection.source_batch_build_failed", err.Error(), true)
		return
	}
	// Keep the loop's file-order turn tracker in sync with the transaction's resolved turn, so
	// later rows' file-order fallback (and any legacy degrade) attribute to the correct user owner
	// — matching the prior claudeEntryToProjectionEvents(&currentTurnID) side effect.
	if resolved := batch.Record.GraphResolvedTurn; resolved != "" {
		*currentTurnID = resolved
	}
	result, err := h.projectionKernel.ApplyClaudeSourceRecordBatch(batch)
	if err != nil {
		// Ledger inconsistency (gap/generation/CAS): expose honestly rather than silently legacy
		// (guardrail #10). Marks the session failed so the owning layer re-syncs via projection pull.
		slog.Warn("go-bridge: Claude source batch rejected",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
		emitClaudeSourceTrace(claudeSourceTraceRecord{
			Phase: "live", IngestDomain: ingestDomain, BackendID: backendID, SessionID: sessionID,
			Correlation: correlation, Record: scanned, FileOrderTurnID: *currentTurnID,
			ProjectionEvents: len(batch.Events), Transition: "rejected",
			ProjectionTurnID: batch.Record.GraphResolvedTurn,
		})
		h.projectionKernel.MarkFailed(backendID, sessionID, "projection.source_batch_rejected", err.Error(), true)
		return
	}
	delivered := 0
	if result.Status == ClaudeSourceBatchAcceptedProjection {
		// Sole projection delivery: flush the authoritative patch the transaction just advanced and
		// deliver it on the single projection stream. Never sendSessionEvent the content events —
		// that would reduce them a second time and double-write the timeline (guardrail #3).
		if _, flushOk := h.eventPublisher.FlushPatchAndRecord(backendID, sessionID); flushOk {
			delivered = 1
		}
		// Dual-send (design §9.3): legacy non-syncV2 consumers still get the raw content frames
		// (deliver-only, never re-reduced). Control-plane running/idle follows.
		h.deliverClaudeLiveRawFrames(batch, sessionID, backendID)
		h.deliverClaudeLiveControlPlane(batch, sessionID, backendID, runningObserved, cachedPID)
	}
	emitClaudeSourceTrace(claudeSourceTraceRecord{
		Phase: "live", IngestDomain: ingestDomain, BackendID: backendID, SessionID: sessionID,
		Correlation: correlation, Record: scanned, FileOrderTurnID: *currentTurnID,
		ProjectionEvents: len(batch.Events), Transition: string(result.Status),
		ProjectionTurnID: result.ProjectionTurnID, ProjectionPartID: result.ProjectionPartID,
		SourceStateRev: result.SourceStateRev, PhysicalRowsAcked: result.PhysicalRowsAcknowledged,
		LogicalChanged: result.LogicalRecordsChanged, PublicDelivered: delivered,
	})
}

// deliverClaudeLegacyRow is the pre-transaction per-event delivery (claudeEntryToProjectionEvents →
// sendSessionEvent), used for admitted non-content rows (compaction boundary) and for content rows
// that fall back when the source batch cannot be built. It reduces through the projection writer
// (PublishLogical), so it is never a second writer (guardrail #3).
func (h *Handlers) deliverClaudeLegacyRow(
	e claudeTranscriptRelayEntry,
	sessionID, backendID string,
	currentTurnID *string,
	runningObserved *bool,
	cachedPID int,
) {
	evs := claudeEntryToProjectionEvents(e, currentTurnID, nil)
	for _, ev := range evs {
		switch ev.Event {
		case "user_message":
			h.sessions.markRunning(sessionID)
			if turnID, _ := ev.Data["turnId"].(string); turnID != "" {
				h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": turnID})
			}
			h.sendSessionEvent(sessionID, backendID, "user_message", ev.Data)
			h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
			*runningObserved = true
		case "turn_completed":
			h.sendSessionEvent(sessionID, backendID, "turn_completed", ev.Data)
			h.broadcastIdleState(sessionID, backendID)
			*runningObserved = false
			slog.Info("go-bridge: claudeSessionFileRelay turn completed, keeping watch while process live", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID, "turnId", *currentTurnID)
		case "system_message":
			h.sendSessionEvent(sessionID, backendID, "system_message", ev.Data)
		default:
			h.sendSessionEvent(sessionID, backendID, ev.Event, ev.Data)
			*runningObserved = true
		}
	}
}

// deliverClaudeLiveRawFrames dual-sends the raw content frames of an accepted live source batch to
// legacy (non-syncV2) consumers via the package-private pre-reduced outlet — never re-reduced (the transaction
// already reduced them; guardrail #3). Mirrors the prior legacy raw sequence (turn_started arms
// before user_message). v2 consumers get the projection_patch instead (design §6.5/§9.3).
func (h *Handlers) deliverClaudeLiveRawFrames(batch ClaudeSourceRecordBatch, sessionID, backendID string) {
	h.mu.Lock()
	dir := h.sessions.directoryForSession(sessionID)
	h.mu.Unlock()
	publish := func(event string, data map[string]interface{}) {
		h.eventPublisher.publishPreReducedTimeline(LogicalEvent{
			SessionID: sessionID, BackendID: backendID, Event: event, Data: data,
			Directory: dir, Broadcast: true, Offline: IsDurableMilestone(event),
		})
	}
	for _, ev := range batch.Events {
		switch ev.Event {
		case "user_message":
			if turnID, _ := ev.Data["turnId"].(string); turnID != "" {
				publish("turn_started", map[string]interface{}{"turnId": turnID})
			}
			publish("user_message", ev.Data)
		default:
			publish(ev.Event, ev.Data)
		}
	}
}

// deliverClaudeLiveControlPlane fires the non-reduced control-plane side-effects of an accepted
// live source batch: session_state_changed=running when a turn started, idle when completed. The
// content itself was already delivered as the authoritative projection_patch; these raw control
// events are absent from the reducer switch, so they cannot double-write the timeline (guardrail #3).
func (h *Handlers) deliverClaudeLiveControlPlane(
	batch ClaudeSourceRecordBatch,
	sessionID, backendID string,
	runningObserved *bool,
	cachedPID int,
) {
	hasUserMessage, hasTurnCompleted := false, false
	for _, ev := range batch.Events {
		switch ev.Event {
		case "user_message":
			hasUserMessage = true
		case "turn_completed":
			hasTurnCompleted = true
		}
	}
	switch {
	case hasTurnCompleted:
		h.broadcastIdleState(sessionID, backendID)
		*runningObserved = false
		slog.Info("go-bridge: claudeSessionFileRelay turn completed, keeping watch while process live", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID, "turnId", batch.Record.GraphResolvedTurn)
	case hasUserMessage:
		h.sessions.markRunning(sessionID)
		h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
		*runningObserved = true
	default:
		*runningObserved = true
	}
}

type claudeTranscriptRelayEntry struct {
	Type                      string                      `json:"type"`
	Subtype                   string                      `json:"subtype"`
	UUID                      string                      `json:"uuid"`
	ParentUUID                string                      `json:"parentUuid"`
	Timestamp                 string                      `json:"timestamp"`
	IsMeta                    bool                        `json:"isMeta"`
	IsCompactSummary          bool                        `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool                        `json:"isVisibleInTranscriptOnly"`
	CompactMetadata           *claudeRelayCompactMetadata `json:"compactMetadata"`
	// Claude Desktop persists the complete AskUserQuestion resolution on the user row in
	// toolUseResult, alongside message.content.tool_result. The relay needs presence and
	// shape only; answers are never copied into the projection event.
	ToolUseResult           json.RawMessage `json:"toolUseResult"`
	SourceToolAssistantUUID string          `json:"sourceToolAssistantUUID"`
	Message                 *struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeRelayCompactMetadata struct {
	PreTokens  int64 `json:"preTokens"`
	PostTokens int64 `json:"postTokens"`
}

func isClaudeCompactionBoundaryRelayEntry(entry claudeTranscriptRelayEntry) bool {
	return entry.Type == "system" && entry.Subtype == "compact_boundary"
}

func isClaudeInternalCompactRelayEntry(entry claudeTranscriptRelayEntry) bool {
	return entry.IsCompactSummary || entry.IsVisibleInTranscriptOnly
}

func claudeRelayCompactionSummary(metadata *claudeRelayCompactMetadata) string {
	if metadata == nil || metadata.PreTokens <= metadata.PostTokens {
		return "已压缩对话"
	}
	saved := metadata.PreTokens - metadata.PostTokens
	if saved >= 1000 {
		return fmt.Sprintf("已压缩对话 · 节省 %.1fk tokens", float64(saved)/1000)
	}
	return fmt.Sprintf("已压缩对话 · 节省 %d tokens", saved)
}

func claudeRelayTimestampMillis(raw string) int64 {
	timestamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return timestamp.UnixMilli()
}

// claudeEntryTurnIdentity returns the stable identity for a Claude transcript row.
// User rows often omit message.id and only carry top-level uuid; assistant rows
// usually have message.id. Prefer message.id, fall back to entry.uuid.
func claudeEntryTurnIdentity(e claudeTranscriptRelayEntry) string {
	if e.Message != nil {
		if id := strings.TrimSpace(e.Message.ID); id != "" {
			return id
		}
	}
	return strings.TrimSpace(e.UUID)
}

// lastClaudeUserIdentityFromPath seeds live relay turnId from the last user
// prompt already on disk (warm-start / mid-turn open).
func lastClaudeUserIdentityFromPath(sessPath string, completeCut int64) string {
	f, err := os.Open(sessPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	entries, err := scanClaudeRelayEntriesFromReader(io.LimitReader(f, completeCut))
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type != "user" || isClaudeUserInterruptRelayEntry(e) {
			continue
		}
		if id := claudeEntryTurnIdentity(e); id != "" {
			return id
		}
	}
	return ""
}

type claudeTranscriptRelayTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeTranscriptRelayMeaningfulEntry struct {
	hasMeaningfulEntry bool
	entryType          string
	interrupt          bool
	finalAssistant     bool
}

func claudeTranscriptRelayTextBlocks(raw json.RawMessage) []claudeTranscriptRelayTextBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []claudeTranscriptRelayTextBlock{{Type: "text", Text: text}}
	}
	var blocks []claudeTranscriptRelayTextBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

func isClaudeUserInterruptRelayEntry(entry claudeTranscriptRelayEntry) bool {
	if entry.Type != "user" || entry.Message == nil {
		return false
	}
	for _, block := range claudeTranscriptRelayTextBlocks(entry.Message.Content) {
		if block.Type == "text" && strings.HasPrefix(strings.TrimSpace(block.Text), "[Request interrupted by user") {
			return true
		}
	}
	return false
}

func isFinalClaudeStopReason(reason string) bool {
	switch reason {
	case "end_turn", "stop_limit", "stop_sequence", "max_tokens":
		return true
	default:
		return false
	}
}

// scanClaudeRelayEntriesFromReader 返回新增字节内所有 meaningful user/assistant/system 记录
// （按文件顺序），应用 resume meta / no-response 跳过逻辑。
func scanClaudeRelayEntriesFromReader(r io.Reader) ([]claudeTranscriptRelayEntry, error) {
	skipNextResumeNoResponse := false
	var entries []claudeTranscriptRelayEntry
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry claudeTranscriptRelayEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if isClaudeCompactionBoundaryRelayEntry(entry) {
			entries = append(entries, entry)
			continue
		}
		if isClaudeInternalCompactRelayEntry(entry) || entry.Message == nil {
			continue
		}
		if isClaudeResumeMetaRelayEntry(entry) {
			skipNextResumeNoResponse = true
			continue
		}
		if skipNextResumeNoResponse {
			if isClaudeResumeNoResponseRelayEntry(entry) {
				skipNextResumeNoResponse = false
				continue
			}
			skipNextResumeNoResponse = false
		}
		if entry.Type == "user" || entry.Type == "assistant" {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

type claudeRelayScanState struct {
	skipNextResumeNoResponse bool
}

type claudeRelayPoisonRecord struct {
	ByteStart int64
	ByteEnd   int64
}

type claudeRelayCompleteScan struct {
	Entries       []claudeTranscriptRelayEntry
	Records       []claudeRelayScannedRecord
	ConsumedBytes int64
	Poison        *claudeRelayPoisonRecord
}

// claudeRelayScannedRecord keeps the private, delimiter-inclusive physical identity beside the
// admitted source row. It is never embedded in EventMessage or sent over Bridge/Relay.
type claudeRelayScannedRecord struct {
	Entry     claudeTranscriptRelayEntry
	ByteStart int64
	ByteEnd   int64
	Admitted  bool
}

// scanCompleteClaudeRelayEntriesFromReader scans newline-terminated physical records only.
// ConsumedBytes advances through validated or intentionally ignored complete rows, but stops before
// an invalid complete JSON row and never includes an unterminated tail.
func scanCompleteClaudeRelayEntriesFromReader(
	r io.Reader,
	baseOffset int64,
	state *claudeRelayScanState,
) (claudeRelayCompleteScan, error) {
	if state == nil {
		state = &claudeRelayScanState{}
	}
	var result claudeRelayCompleteScan
	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		raw, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			// ReadBytes returns the final unterminated bytes with io.EOF. They remain unconsumed.
			break
		}
		if readErr != nil {
			return claudeRelayCompleteScan{}, readErr
		}
		recordStart := baseOffset + result.ConsumedBytes
		recordEnd := recordStart + int64(len(raw))
		if len(raw) > 1024*1024*16 {
			result.Poison = &claudeRelayPoisonRecord{ByteStart: recordStart, ByteEnd: recordEnd}
			return result, nil
		}
		line := raw[:len(raw)-1] // ReadBytes returned a delimiter-terminated record.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			result.ConsumedBytes += int64(len(raw))
			continue
		}
		var entry claudeTranscriptRelayEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			result.Poison = &claudeRelayPoisonRecord{ByteStart: recordStart, ByteEnd: recordEnd}
			return result, nil
		}
		result.ConsumedBytes += int64(len(raw))
		recordIndex := len(result.Records)
		result.Records = append(result.Records, claudeRelayScannedRecord{
			Entry: entry, ByteStart: recordStart, ByteEnd: recordEnd,
		})
		if isClaudeCompactionBoundaryRelayEntry(entry) {
			result.Entries = append(result.Entries, entry)
			result.Records[recordIndex].Admitted = true
			continue
		}
		if isClaudeInternalCompactRelayEntry(entry) || entry.Message == nil {
			continue
		}
		if isClaudeResumeMetaRelayEntry(entry) {
			state.skipNextResumeNoResponse = true
			continue
		}
		if state.skipNextResumeNoResponse {
			if isClaudeResumeNoResponseRelayEntry(entry) {
				state.skipNextResumeNoResponse = false
				continue
			}
			state.skipNextResumeNoResponse = false
		}
		if entry.Type == "user" || entry.Type == "assistant" {
			result.Entries = append(result.Entries, entry)
			result.Records[recordIndex].Admitted = true
		}
	}
	return result, nil
}

func classifyLastMeaningfulClaudeRelayEntryFromReader(r io.Reader) (claudeTranscriptRelayMeaningfulEntry, error) {
	entries, err := scanClaudeRelayEntriesFromReader(r)
	if err != nil {
		return claudeTranscriptRelayMeaningfulEntry{}, err
	}
	var last claudeTranscriptRelayMeaningfulEntry
	for _, e := range entries {
		switch e.Type {
		case "user":
			last = claudeTranscriptRelayMeaningfulEntry{hasMeaningfulEntry: true, entryType: "user", interrupt: isClaudeUserInterruptRelayEntry(e)}
		case "assistant":
			last = claudeTranscriptRelayMeaningfulEntry{hasMeaningfulEntry: true, entryType: "assistant", finalAssistant: isFinalClaudeStopReason(e.Message.StopReason)}
		case "system":
			if isClaudeCompactionBoundaryRelayEntry(e) {
				last = claudeTranscriptRelayMeaningfulEntry{hasMeaningfulEntry: true, entryType: "system"}
			}
		}
	}
	return last, nil
}

// claudeRelayContentBlock 是 assistant/user message.content 数组里的一个 block
// （text/thinking/tool_use/tool_result）。
type claudeRelayContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`        // text
	Thinking  string          `json:"thinking"`    // thinking
	Name      string          `json:"name"`        // tool_use
	Input     json.RawMessage `json:"input"`       // tool_use
	ID        string          `json:"id"`          // tool_use
	ToolUseID string          `json:"tool_use_id"` // tool_result
	Content   json.RawMessage `json:"content"`     // tool_result (string or block array)
	IsError   bool            `json:"is_error"`    // tool_result
}

// claudeToolUseMeta records a tool_use block's display metadata (toolName + path-bearing title
// + raw toolInput) so the later tool_result record (matched by tool_use_id) can carry the same
// fields onto tool_finished. This is the Phase 1C L-α correlation for the relay-transcript
// cold-start path.
type claudeToolUseMeta struct {
	ToolName  string
	Title     string
	ToolInput string
}

// claudeSummarizeToolInput mirrors the claudecode package's summarizeInput: derives a
// human-readable, often path-bearing summary from a tool input map (file_path for
// Edit/Write/Read/MultiEdit, command for Bash, pattern for Grep/Glob). Used so cold-start
// tool events carry a title that iOS extractPrimaryPath branch 2 can resolve to a file path.
// Returns "" when no known field is present (caller falls back to toolName).
func claudeSummarizeToolInput(tool string, input map[string]any) string {
	if input == nil {
		return ""
	}
	switch tool {
	case "Read", "Edit", "Write", "MultiEdit":
		if fp, ok := input["file_path"].(string); ok && fp != "" {
			return fp
		}
	case "Bash":
		if cmd, ok := input["command"].(string); ok && cmd != "" {
			return cmd
		}
	case "Grep":
		if p, ok := input["pattern"].(string); ok && p != "" {
			return p
		}
	case "Glob":
		if p, ok := input["pattern"].(string); ok && p != "" {
			return p
		}
		if p, ok := input["glob_pattern"].(string); ok && p != "" {
			return p
		}
	}
	return ""
}

// claudeRelayContentBlocks 解析 assistant message.content（可能是字符串或 block 数组）。
func claudeRelayContentBlocks(raw json.RawMessage) []claudeRelayContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []claudeRelayContentBlock{{Type: "text", Text: s}}
	}
	var blocks []claudeRelayContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

// streamClaudeTranscriptProjectionEvents streams a claude session .jsonl and invokes emit for each
// derived projectionHydrateEvent in file order (design §10.5.7 修法 1 — claude cold-hydrate). A
// claude "turn" is a user prompt followed by its assistant response; the user message id owns the
// turn (used as both turnId and the assistant text itemId so user+assistant land in ONE reducer
// turn). tool_use → tool_started (real tool id); tool_result → tool_finished (matched by
// tool_use_id). ctx cancel is honored between lines; a single unparseable line is skipped (parity
// with the live file-relay scanner).
func streamClaudeTranscriptProjectionEvents(ctx context.Context, sessPath string, emit func(projectionHydrateEvent) bool) error {
	return streamClaudeTranscriptProjectionEventsRange(ctx, sessPath, 0, -1, emit)
}

func streamClaudeTranscriptProjectionEventsRange(
	ctx context.Context,
	sessPath string,
	startOffset, endOffset int64,
	emit func(projectionHydrateEvent) bool,
) error {
	return streamClaudeTranscriptProjectionEventsRangeSeed(ctx, sessPath, startOffset, endOffset, "", emit)
}

func streamClaudeTranscriptProjectionEventsRangeSeed(
	ctx context.Context,
	sessPath string,
	startOffset, endOffset int64,
	initialTurnID string,
	emit func(projectionHydrateEvent) bool,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := os.Open(sessPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if startOffset > 0 {
		if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
			return err
		}
	}
	var reader io.Reader = f
	if endOffset >= 0 {
		if endOffset < startOffset {
			return fmt.Errorf("invalid transcript range [%d,%d)", startOffset, endOffset)
		}
		reader = io.LimitReader(f, endOffset-startOffset)
	}
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16)
	skipNextResumeNoResponse := false
	currentTurnID := initialTurnID
	// L-α: per-scan tool_use metadata correlation (tool_use_id → toolName/title/toolInput),
	// so tool_finished (from the later user tool_result record) carries a path-bearing title.
	toolUseMeta := make(map[string]claudeToolUseMeta)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e claudeTranscriptRelayEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if isClaudeInternalCompactRelayEntry(e) {
			continue
		}
		if isClaudeCompactionBoundaryRelayEntry(e) {
			for _, ev := range claudeEntryToProjectionEvents(e, &currentTurnID, nil) {
				if !emit(ev) {
					return ctx.Err()
				}
			}
			continue
		}
		if e.Message == nil {
			continue
		}
		if isClaudeResumeMetaRelayEntry(e) {
			skipNextResumeNoResponse = true
			continue
		}
		if skipNextResumeNoResponse {
			if isClaudeResumeNoResponseRelayEntry(e) {
				skipNextResumeNoResponse = false
				continue
			}
			skipNextResumeNoResponse = false
		}
		if e.Type != "user" && e.Type != "assistant" {
			continue
		}
		for _, ev := range claudeEntryToProjectionEvents(e, &currentTurnID, toolUseMeta) {
			if !emit(ev) {
				return ctx.Err()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

// claudeEntryToProjectionEvents maps one claude transcript entry to its projection events,
// threading the active turn id (the owning user message id). User text starts a new turn and
// emits user_message; user tool_result blocks emit tool_finished; assistant blocks emit
// text_delta / reasoning_delta / tool_started; a final stop_reason emits turn_completed (the
// segment boundary). Returns nil for entries that carry no projection-meaningful content.
//
// toolUseMeta threads tool_use block metadata (toolName + path-bearing title + toolInput) from
// the assistant tool_use record to the later user tool_result record (matched by tool_use_id),
// so tool_finished carries the same path-bearing title/toolName iOS needs for cold-start
// activity rows (Phase 1C L-α on the relay-transcript path). May be nil when the caller does
// not need cross-entry correlation.
func claudeEntryToProjectionEvents(e claudeTranscriptRelayEntry, currentTurnID *string, toolUseMeta map[string]claudeToolUseMeta) []projectionHydrateEvent {
	if isClaudeInternalCompactRelayEntry(e) {
		return nil
	}
	if isClaudeCompactionBoundaryRelayEntry(e) {
		identity := strings.TrimSpace(e.UUID)
		if identity == "" {
			return nil
		}
		data := map[string]interface{}{
			"itemId": identity,
			"turnId": identity,
			"text":   claudeRelayCompactionSummary(e.CompactMetadata),
		}
		if timestampMillis := claudeRelayTimestampMillis(e.Timestamp); timestampMillis > 0 {
			data["timestampMillis"] = timestampMillis
		}
		return []projectionHydrateEvent{{Event: "system_message", Data: data}}
	}
	if e.Message == nil {
		return nil
	}
	identity := claudeEntryTurnIdentity(e)
	role := e.Message.Role
	if role == "" {
		role = e.Type
	}
	blocks := claudeRelayContentBlocks(e.Message.Content)
	var out []projectionHydrateEvent
	if role == "user" {
		for _, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			if b.ToolUseID != "" && claudecode.HasStructuredUserInputResultEnvelope(e.ToolUseResult) {
				data := map[string]interface{}{
					"turnId":        *currentTurnID,
					"itemId":        b.ToolUseID,
					"interactionId": claudecode.DeriveStructuredUserInputInteractionID(b.ToolUseID),
					"status":        "answered",
					"source":        "other_client",
				}
				if timestampMillis := claudeRelayTimestampMillis(e.Timestamp); timestampMillis > 0 {
					data["resolvedAt"] = timestampMillis
				}
				// Do not emit tool_finished for AskUserQuestion. Its resolution is a structured
				// user_input update; the answer body remains outside projection by contract.
				out = append(out, projectionHydrateEvent{Event: "user_input_resolved", Data: data})
				continue
			}
			data := map[string]interface{}{"toolResult": claudeToolResultText(b), "toolStatus": "completed"}
			if b.ToolUseID != "" {
				data["itemId"] = b.ToolUseID
				// L-α (relay-transcript path): carry the tool_use metadata (toolName + title +
				// toolInput) onto tool_finished so cold-start hydration forwards a path-bearing
				// title to iOS. Previously tool_finished only had itemId/toolResult/toolStatus,
				// so Claude cold-start showed no file path (R5).
				if toolUseMeta != nil {
					if meta, ok := toolUseMeta[b.ToolUseID]; ok {
						if meta.ToolName != "" {
							data["toolName"] = meta.ToolName
						}
						if meta.Title != "" {
							data["title"] = meta.Title
						}
						if meta.ToolInput != "" {
							data["toolInput"] = meta.ToolInput
						}
					}
				}
			}
			out = append(out, projectionHydrateEvent{Event: "tool_finished", Data: data})
		}
		if isClaudeUserInterruptRelayEntry(e) {
			return out // interrupt marker — no user_message, no new turn
		}
		text := claudeConcatTextBlocks(blocks)
		if strings.TrimSpace(text) == "" {
			return out
		}
		// Real Claude user rows often lack message.id; fall back to top-level uuid so
		// hydrate + live growth both produce reducer-acceptable turn/item ids.
		if identity == "" {
			return out
		}
		*currentTurnID = identity
		out = append(out, projectionHydrateEvent{Event: "user_message", Data: map[string]interface{}{
			"itemId": identity, "turnId": identity, "text": text,
		}})
		return out
	}
	// assistant
	turnID := *currentTurnID
	if turnID == "" {
		turnID = identity // assistant before any user prompt (rare) — self-keyed
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, projectionHydrateEvent{Event: "text_delta", Data: map[string]interface{}{"itemId": turnID, "delta": b.Text}})
			}
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				out = append(out, projectionHydrateEvent{Event: "reasoning_delta", Data: map[string]interface{}{"itemId": turnID, "delta": b.Thinking}})
			}
		case "tool_use", "server_tool_use":
			if b.Name == "AskUserQuestion" && b.ID != "" {
				interactionID := claudecode.DeriveStructuredUserInputInteractionID(b.ID)
				var input map[string]any
				var normalized []core.UserInputQuestion
				var normalizeErr error
				if err := json.Unmarshal(b.Input, &input); err != nil {
					normalizeErr = err
				} else {
					normalized, normalizeErr = claudecode.NormalizeStructuredUserInputQuestions(interactionID, input)
				}
				status := "pending"
				diagnosticCode := "observe_only"
				canRespond := false
				canReject := false
				if normalizeErr != nil || len(normalized) == 0 {
					status = "failed"
					diagnosticCode = "invalid_backend_request"
				}
				out = append(out, projectionHydrateEvent{
					Event: "user_input_requested",
					Data: map[string]interface{}{
						"turnId":         turnID,
						"itemId":         b.ID,
						"interactionId":  interactionID,
						"status":         status,
						"questions":      userInputQuestionsToWire(normalized),
						"canRespond":     canRespond,
						"canReject":      canReject,
						"diagnosticCode": diagnosticCode,
					},
				})
				continue
			}
			data := map[string]interface{}{"toolName": b.Name}
			// L-α (relay-transcript path): derive a path-bearing title from the tool input
			// (file_path for Edit/Write/Read, command for Bash) so cold-start activity rows
			// show a file path. Matches the live session.go summarizeInput path.
			toolInputStr := ""
			title := b.Name
			if len(b.Input) > 0 && string(b.Input) != "null" {
				data["toolInput"] = json.RawMessage(b.Input)
				toolInputStr = string(b.Input)
				var decoded map[string]any
				if err := json.Unmarshal(b.Input, &decoded); err == nil {
					if summarized := claudeSummarizeToolInput(b.Name, decoded); strings.TrimSpace(summarized) != "" {
						title = summarized
					}
				}
			}
			data["title"] = title
			if b.ID != "" {
				data["itemId"] = b.ID
				// Record metadata so the matching tool_result (user record) tool_finished
				// can carry the same title/toolName/toolInput.
				if toolUseMeta != nil {
					toolUseMeta[b.ID] = claudeToolUseMeta{
						ToolName:  b.Name,
						Title:     title,
						ToolInput: toolInputStr,
					}
				}
			}
			out = append(out, projectionHydrateEvent{Event: "tool_started", Data: data})
		}
	}
	if isFinalClaudeStopReason(e.Message.StopReason) {
		out = append(out, projectionHydrateEvent{
			Event:    "turn_completed",
			Data:     map[string]interface{}{"turnId": turnID, "done": true, "reason": e.Message.StopReason},
			TurnDone: true,
		})
	}
	return out
}

// claudeConcatTextBlocks joins an entry's text blocks into one string.
func claudeConcatTextBlocks(blocks []claudeRelayContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// claudeToolResultText extracts a tool_result block's output as text (string or block array).
func claudeToolResultText(b claudeRelayContentBlock) string {
	if len(b.Content) == 0 || string(b.Content) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var textBlocks []claudeRelayContentBlock
	if json.Unmarshal(b.Content, &textBlocks) == nil {
		return claudeConcatTextBlocks(textBlocks)
	}
	return strings.TrimSpace(string(b.Content))
}

func (h *Handlers) classifyClaudeTranscriptFile(sessPath string) claudeTranscriptRelayMeaningfulEntry {
	h.noteTranscriptStateProbe()
	completeCut, err := projectionJSONLStartCut(sessPath)
	if err != nil {
		return claudeTranscriptRelayMeaningfulEntry{}
	}
	f, err := os.Open(sessPath)
	if err != nil {
		return claudeTranscriptRelayMeaningfulEntry{}
	}
	defer f.Close()
	entry, err := classifyLastMeaningfulClaudeRelayEntryFromReader(io.LimitReader(f, completeCut))
	if err != nil {
		return claudeTranscriptRelayMeaningfulEntry{}
	}
	return entry
}

func isClaudeResumeMetaRelayEntry(entry claudeTranscriptRelayEntry) bool {
	if !entry.IsMeta || entry.Type != "user" || entry.Message == nil {
		return false
	}
	for _, block := range claudeTranscriptRelayTextBlocks(entry.Message.Content) {
		if block.Type == "text" && strings.TrimSpace(block.Text) == "Continue from where you left off." {
			return true
		}
	}
	return false
}

func isClaudeResumeNoResponseRelayEntry(entry claudeTranscriptRelayEntry) bool {
	if entry.Type != "assistant" || entry.Message == nil {
		return false
	}
	for _, block := range claudeTranscriptRelayTextBlocks(entry.Message.Content) {
		if block.Type == "text" && strings.TrimSpace(block.Text) == "No response requested." {
			return true
		}
	}
	return false
}

// detectClaudeTranscriptState 扫描 transcript 文件的最后几条消息，
// 判定 session 当前是否处于执行中。用于文件 relay 的初始状态检测。
func (h *Handlers) detectClaudeTranscriptState(sessPath string) string {
	last := h.classifyClaudeTranscriptFile(sessPath)
	if !last.hasMeaningfulEntry {
		return "unknown"
	}
	if last.interrupt {
		return "idle"
	}
	if last.entryType == "assistant" {
		if last.finalAssistant {
			return "idle"
		}
		return "running"
	}
	if last.entryType == "user" {
		return "running"
	}
	if last.entryType == "system" {
		return "idle"
	}
	return "unknown"
}

func (h *Handlers) detectCodexTranscriptTaskState(sessPath string) string {
	state, _ := h.detectCodexTranscriptTask(sessPath)
	return state
}

// detectCodexTranscriptRunningTurnWithContent 判定 transcript 尾部是否存在一个「未终结且已产生
// 可投影内容」的 turn（§3.3 rule #2 / D6 的 codex 侧实现）。codex 的冷启 live 采样走 transcript
// 判定时，不能只凭尾部 task_started 未闭合就放行 —— bare turn shell（仅 task_started、没有任何
// 内容事件）必须保持 hydrating，直到出现真实内容或终态，否则会把空壳当 ready 暴露给投影。
// 任一非 lifecycle 事件（text/reasoning/user_message/tool_*/context_usage）落在未闭合的
// task_started 之后即视为「有内容」。
func (h *Handlers) detectCodexTranscriptRunningTurnWithContent(sessPath string) bool {
	open := false
	hasContent := false
	for _, ev := range scanCodexTranscriptRelayEvents(sessPath, 0) {
		switch ev.kind {
		case "task_started":
			open = true
			hasContent = false
		case "task_complete", "turn_aborted":
			open = false
			hasContent = false
		default:
			if open {
				hasContent = true
			}
		}
	}
	return open && hasContent
}

func (h *Handlers) detectCodexTranscriptTask(sessPath string) (string, string) {
	events := h.scanCodexTranscriptTaskEvents(sessPath, 0)
	state := "unknown"
	turnID := ""
	for _, event := range events {
		switch event.kind {
		case "task_started":
			state = "running"
			turnID = event.turnID
		case "task_complete":
			state = "idle"
			if event.turnID != "" {
				turnID = event.turnID
			}
		case "turn_aborted":
			// §5.1 #7 producer layer 3：rollout 以 turn_aborted 收口（无 task_complete，
			// 真实形态 019f5453）→ 视同 idle 终态，避免 detectCodexTranscriptTaskState 永久
			// 判 running 致 file-relay 滞留 watch 一个已死的 turn 文件。
			state = "idle"
			if event.turnID != "" {
				turnID = event.turnID
			}
		}
	}
	return state, turnID
}

// scanCodexTranscriptTaskEvents 提取 lifecycle 事件（task_started/task_complete/turn_aborted），
// 供 detectCodexTranscriptTaskState 判定当前态。委托给统一扫描器后过滤。turn_aborted 是
// §5.1 #7 新增的终态（中断收口），与 task_complete 同等进入 state 判定。
func (h *Handlers) scanCodexTranscriptTaskEvents(sessPath string, offset int64) []codexRelayEvent {
	var events []codexRelayEvent
	for _, ev := range scanCodexTranscriptRelayEvents(sessPath, offset) {
		if ev.kind == "task_started" || ev.kind == "task_complete" || ev.kind == "turn_aborted" {
			events = append(events, ev)
		}
	}
	return events
}

// codexRelayEvent 是 rollout 扫描产出的有序事件。kind 为 lifecycle
// (task_started/task_complete) 或内容候选 (text/reasoning)，text 字段为明文。
type codexRelayEvent struct {
	kind       string
	turnID     string                 // source-proven rollout task_started/task_complete turn_id
	text       string                 // text/reasoning 明文
	canonical  bool                   // canonical rich-history content source (response_item)
	newPart    bool                   // persisted response block starts a canonical text part
	toolName   string                 // tool_started/tool_finished
	toolInput  string                 // tool_started（custom_tool_call.input JS 串）
	toolResult string                 // tool_finished（custom_tool_call_output.output 拼接）
	toolStatus string                 // persisted tool completion status
	itemId     string                 // call_id
	context    map[string]interface{} // context_usage_updated 的 context 对象
}

type codexRolloutEntry struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// codexEventPayload 对应 event_msg payload（agent_message 的 message 明文 /
// agent_reasoning 的 text 明文）。某些 event_msg 把真正 payload 嵌套在 payload.payload
// 下，codexNormalizeEventPayload 两种形态都吃。
type codexEventPayload struct {
	Type    string `json:"type"`
	TurnID  string `json:"turn_id"`
	Message string `json:"message"` // agent_message 明文
	Text    string `json:"text"`    // agent_reasoning 明文
}

func codexNormalizeEventPayload(raw json.RawMessage) (codexEventPayload, bool) {
	var p codexEventPayload
	if json.Unmarshal(raw, &p) == nil && p.Type != "" {
		return p, true
	}
	var nested struct {
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &nested) == nil && len(nested.Payload) > 0 {
		if json.Unmarshal(nested.Payload, &p) == nil && p.Type != "" {
			return p, true
		}
	}
	return codexEventPayload{}, false
}

type codexResponseItemPayload struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Role    string                 `json:"role"`
	Content []codexResponseContent `json:"content"` // message
	Summary []codexResponseSummary `json:"summary"` // reasoning
	// Tool lifecycle (custom_tool_call / function_call / local_shell_call / mcp_call / …).
	// custom_tool_call: name often "exec", real op buried in input JS string.
	// function_call: name e.g. "exec_command", args in arguments JSON string.
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Input     string          `json:"input"`
	Arguments json.RawMessage `json:"arguments"`
	Status    string          `json:"status"`
	Command   string          `json:"command"`
	// output is a string for function_call_output / local_shell_call_output, or a
	// content[] array for custom_tool_call_output — parse via extractCodexToolOutput.
	Output json.RawMessage `json:"output"`
}

// extractCodexToolOutput accepts either a JSON string or a content[] array.
func extractCodexToolOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asBlocks []codexResponseContent
	if err := json.Unmarshal(raw, &asBlocks); err == nil {
		var sb strings.Builder
		for _, b := range asBlocks {
			sb.WriteString(b.Text)
		}
		return sb.String()
	}
	return strings.TrimSpace(string(raw))
}

type codexResponseContent struct {
	Type     string `json:"type"` // output_text / input_text / input_image
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
}

type codexResponseSummary struct {
	Type string `json:"type"` // summary_text
	Text string `json:"text"`
}

// codexTokenCountPayload 对应 event_msg/token_count（运行状态条 token 显示的数据源）。
type codexTokenCountPayload struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage    codexTokenUsage `json:"total_token_usage"`
		ModelContextWindow int             `json:"model_context_window"`
	} `json:"info"`
}

type codexTokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

// scanCodexTranscriptRelayEvents 扫描 rollout 从 offset 起的新增字节，按文件顺序产出
// lifecycle + 内容候选事件（§5 wire 映射的数据源）。内容提取口径为「元素」——每个
// output_text / summary_text block 各一个单元（一条 reasoning 可含多个 summary_text，
// r5）。空摘要 shape-agnostic 跳过（提取不到非空 summary_text 文本即不发，r3/r4）。message
// 与 reasoning 两源都解析（event_msg + response_item），由调用方用 per-turn 内容去重合并，
// 覆盖 Legacy / Paginated / 双写三种 session 模式（policy.rs:108-119，r1/r2/r3）。token 级
// delta 经 policy.rs:172 不落盘，故天花板为事件/条目级（§2/§3.1 源码实证）。
func scanCodexTranscriptRelayEvents(sessPath string, offset int64) []codexRelayEvent {
	f, err := os.Open(sessPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil
		}
	}

	var out []codexRelayEvent
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16)
	for scanner.Scan() {
		var entry codexRolloutEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		out = append(out, codexRolloutEntryEvents(entry)...)
	}
	return out
}

// codexRolloutEntryEvents extracts the ordered codexRelayEvent(s) a single rollout JSONL line
// produces (lifecycle + content candidates; see scanCodexTranscriptRelayEvents doc for the
// extraction口径). Extracted so the batch scanner and the streaming scanner share one parsing
// path — no behavior fork between full-scan and segmented-scan (design §10.5.6 scheme A).
func codexRolloutEntryEvents(entry codexRolloutEntry) []codexRelayEvent {
	var out []codexRelayEvent
	switch entry.Type {
	case "event_msg":
		p, ok := codexNormalizeEventPayload(entry.Payload)
		if !ok {
			return out
		}
		switch p.Type {
		case "task_started":
			out = append(out, codexRelayEvent{kind: "task_started", turnID: p.TurnID})
		case "task_complete":
			out = append(out, codexRelayEvent{kind: "task_complete", turnID: p.TurnID})
		case "turn_aborted":
			// §5.1 #7 producer layer 3（cold rollout）：真实 rollout 形态见 session
			// 019f5453（event_msg.payload.type="turn_aborted"，携带 turn_id / reason /
			// completed_at / duration_ms）。这是 turn 的终态标记——只读 turn_id（reason
			// 等字段 catalog/projection 不消费），映射到 reducer 的 turn_aborted 终态 case，
			// 使 content-less aborted turn 不再永久 hydrating（guardrail #6）。
			out = append(out, codexRelayEvent{kind: "turn_aborted", turnID: p.TurnID})
		case "agent_message":
			if strings.TrimSpace(p.Message) != "" {
				out = append(out, codexRelayEvent{kind: "text", text: p.Message})
			}
		case "agent_reasoning":
			if strings.TrimSpace(p.Text) != "" {
				out = append(out, codexRelayEvent{kind: "reasoning", text: p.Text})
			}
		case "token_count":
			// token 级 delta 不落盘（policy.rs:172），但 token_count 是事件级用量记录，
			// 映射到 context_usage_updated（运行状态条 token 显示）。
			var tc codexTokenCountPayload
			if json.Unmarshal(entry.Payload, &tc) == nil {
				tu := tc.Info.TotalTokenUsage
				if tu.TotalTokens > 0 || tc.Info.ModelContextWindow > 0 {
					out = append(out, codexRelayEvent{kind: "context_usage", context: map[string]interface{}{
						"usedTokens":            tu.TotalTokens,
						"totalTokens":           tu.TotalTokens,
						"inputTokens":           tu.InputTokens,
						"cachedInputTokens":     tu.CachedInputTokens,
						"outputTokens":          tu.OutputTokens,
						"reasoningOutputTokens": tu.ReasoningOutputTokens,
						"contextWindow":         tc.Info.ModelContextWindow,
					}})
				}
			}
		}
	case "response_item":
		var p codexResponseItemPayload
		if json.Unmarshal(entry.Payload, &p) != nil {
			return out
		}
		switch p.Type {
		case "message":
			switch p.Role {
			case "assistant":
				for _, b := range p.Content {
					if b.Type == "output_text" && strings.TrimSpace(b.Text) != "" {
						out = append(out, codexRelayEvent{kind: "text", text: b.Text, canonical: true, newPart: true})
					}
				}
			case "user":
				var texts []string
				hasImage := false
				for _, b := range p.Content {
					if b.Type == "input_text" && strings.TrimSpace(b.Text) != "" &&
						codex.IsTranscriptUserPrompt(b.Text) {
						texts = append(texts, b.Text)
					}
					if b.Type == "input_image" && strings.TrimSpace(b.ImageURL) != "" {
						hasImage = true
					}
				}
				if len(texts) > 0 || hasImage {
					out = append(out, codexRelayEvent{
						kind:   "user_message",
						itemId: strings.TrimSpace(p.ID),
						text:   strings.Join(texts, "\n"),
					})
				}
			}
		case "reasoning":
			for _, s := range p.Summary {
				if s.Type == "summary_text" && strings.TrimSpace(s.Text) != "" {
					out = append(out, codexRelayEvent{kind: "reasoning", text: s.Text, canonical: true})
				}
			}
		case "custom_tool_call":
			if name, input, ok := codex.NormalizeTranscriptCustomToolCall(p.Name, p.Input); ok {
				out = append(out, codexRelayEvent{kind: "tool_started", toolName: name, toolInput: input, itemId: p.CallID})
			}
		case "custom_tool_call_output":
			out = append(out, codexRelayEvent{
				kind:       "tool_finished",
				itemId:     p.CallID,
				toolResult: codex.TranscriptToolOutput(p.Output),
				toolStatus: p.Status,
			})
		// Native Codex tool shapes (session 019f8dd1 / 2026-07: function_call dominates).
		// Previously only custom_tool_* was mapped → live tool_* EMIT=0 for those turns.
		case "function_call":
			if name, input, ok := codex.NormalizeTranscriptFunctionCall(p.Name, p.Arguments); ok {
				out = append(out, codexRelayEvent{
					kind:      "tool_started",
					toolName:  name,
					toolInput: input,
					itemId:    p.CallID,
				})
			}
		case "function_call_output":
			out = append(out, codexRelayEvent{
				kind:       "tool_finished",
				itemId:     p.CallID,
				toolResult: codex.TranscriptToolOutput(p.Output),
				toolStatus: p.Status,
			})
		case "command_execution":
			out = append(out, codexRelayEvent{
				kind:      "tool_started",
				toolName:  "Bash",
				toolInput: p.Command,
				itemId:    p.CallID,
			})
		}
	}
	return out
}

// streamCodexTranscriptRelayEvents scans the rollout line-by-line and invokes emit for each
// parsed event in file order WITHOUT buffering the whole file into memory (design §10.5.6
// scheme A — segmented cold-hydrate). emit returns false to stop early (e.g. after the first
// turn-bounded segment, or on ctx cancel). Parsing parity with scanCodexTranscriptRelayEvents
// is guaranteed by sharing codexRolloutEntryEvents. A read/scan error is returned; a single
// unparseable line is skipped. ctx cancel is honored between lines.
func streamCodexTranscriptRelayEvents(ctx context.Context, sessPath string, offset int64, emit func(codexRelayEvent) bool) error {
	return streamCodexTranscriptRelayEventsRange(ctx, sessPath, offset, -1, emit)
}

func streamCodexTranscriptRelayEventsRange(
	ctx context.Context,
	sessPath string,
	startOffset, endOffset int64,
	emit func(codexRelayEvent) bool,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := os.Open(sessPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if startOffset > 0 {
		if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
			return err
		}
	}
	var reader io.Reader = f
	if endOffset >= 0 {
		if endOffset < startOffset {
			return fmt.Errorf("invalid transcript range [%d,%d)", startOffset, endOffset)
		}
		reader = io.LimitReader(f, endOffset-startOffset)
	}
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var entry codexRolloutEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		for _, ev := range codexRolloutEntryEvents(entry) {
			if !emit(ev) {
				return ctx.Err()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

var (
	// relayInitialTimeout 是 passive join 后首次等待事件的超时。
	// 如果 session 的 turn 已经结束，不会收到 turn/completed，
	// 需要快速超时让 iOS 退出执行态。
	relayInitialTimeout = 10 * time.Second
	// relayActiveTimeout 是收到首个事件后的空闲超时。只适用于不能查询
	// 权威 runtime state 的后端；Codex/Claude 长工具执行期间可能长期不吐事件。
	relayActiveTimeout = 60 * time.Second
)

func disablesRelayIdleTimeout(backendID string) bool {
	switch backendID {
	case "claude", "claudecode", "codex", "opencode", "dsh-web":
		// dsh-web mux 在审批等待期间不再吐 text_delta。60s 空闲超时会
		// auto-complete 并退出 relayEvents（真机 18:10:41，审批已 surface
		// 仍被收口），iOS 权限卡来不及停留。
		return true
	default:
		return false
	}
}

// relaySurvivesTurnBoundary reports backends whose AgentSession outlives a
// single turn. Exiting relayEvents on EventResult/EventError leaves a zombie
// if Close does not close Events() — the next send starts a new session
// object while startRelayIfNotRunning no-ops, and iOS misses later approvals.
func relaySurvivesTurnBoundary(backendID string) bool {
	switch backendID {
	case "claude", "claudecode", "dsh-web":
		return true
	default:
		return false
	}
}

func (h *Handlers) relayKindIs(sessionID, kind string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.relayRunning[sessionID] && h.relayRunningKind[sessionID] == kind
}

func (h *Handlers) clearRelayKindIf(sessionID, kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.relayRunningKind[sessionID] != kind {
		return
	}
	delete(h.relayRunning, sessionID)
	delete(h.relayRunningKind, sessionID)
}

func (h *Handlers) rebindRelayKind(fromID, toID, kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.relayRunningKind[fromID] != kind {
		return
	}
	delete(h.relayRunning, fromID)
	delete(h.relayRunningKind, fromID)
	if !h.relayRunning[toID] {
		h.relayRunning[toID] = true
		h.relayRunningKind[toID] = kind
	}
}

// 且事件通道没有跨进程共享事件总线，它的 relayEvents goroutine 在完成一轮（EventResult）或空闲时
// 绝不能退出（通过 continue 忽略）。这也意味着该 goroutine 和底层 session 会常驻在内存中，
// 其最终生命周期的释放依赖于 session 显式关闭/删除导致 events channel 关闭。这需要注意潜在的泄漏风险。
func (h *Handlers) relayEvents(conn Connection, sess core.AgentSession, sessionID, backendID string, gen ...uint64) {
	var relayGen uint64
	if len(gen) > 0 {
		relayGen = gen[0]
	}
	origSessionID := sessionID
	defer func() {
		h.mu.Lock()
		current := h.agentRelayGen[origSessionID]
		if g := h.agentRelayGen[sessionID]; g > current {
			current = g
		}
		owns := relayGen == 0 || current == relayGen
		if owns {
			delete(h.agentRelayRunning, origSessionID)
			delete(h.agentRelaySess, origSessionID)
			delete(h.agentRelayRunning, sessionID)
			delete(h.agentRelaySess, sessionID)
		}
		h.mu.Unlock()
		if owns {
			h.clearRelayKindIf(origSessionID, relayKindAgent)
			h.clearRelayKindIf(sessionID, relayKindAgent)
		}
		slog.Info("go-bridge: relayEvents exited", "backendID", backendID, "sessionID", sessionID)
	}()
	slog.Info("go-bridge: relayEvents started", "backendID", backendID, "sessionID", sessionID)
	events := sess.Events()
	eventCount := 0

	idleTimer := time.NewTimer(relayInitialTimeout)
	defer idleTimer.Stop()
	if disablesRelayIdleTimeout(backendID) {
		idleTimer.Stop()
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if !h.sessions.isIdle(sessionID) {
					h.mu.Lock()
					dir := h.sessions.directoryForSession(sessionID)
					h.mu.Unlock()

					h.deltaBatcher.Send(LogicalEvent{
						SessionID: sessionID,
						BackendID: backendID,
						Event:     "turn_completed",
						Data:      map[string]interface{}{"done": true, "reason": "events_channel_closed"},
						Directory: dir,
						Broadcast: true,
						Offline:   true,
					})

					h.broadcastIdleState(sessionID, backendID)
					h.recordPendingNotification(sessionID, backendID, "completed", "events_channel_closed")
				}
				return
			}
			if !disablesRelayIdleTimeout(backendID) {
				idleTimer.Reset(relayActiveTimeout)
			}
			eventCount++
			h.mu.Lock()
			dir := h.sessions.directoryForSession(sessionID)
			h.mu.Unlock()
			sessionID = h.rebindSessionIDIfResolved(sessionID, sess, ev.SessionID, backendID, dir)
			eventName, data, _ := mapAgentEvent(ev)
			if eventName == "" {
				slog.Debug("go-bridge: relayEvents unmapped event", "backendID", backendID, "sessionID", sessionID, "eventType", ev.Type)
				continue
			}

			// Sync session runtimeState from relayed events to memory sessionRegistry
			if eventName == "turn_started" {
				h.sessions.markRunning(sessionID)
			} else if eventName == "turn_completed" || eventName == "error" {
				h.sessions.markIdle(sessionID)
			} else if eventName == "session_state_changed" {
				if dataMap, ok := data.(map[string]interface{}); ok {
					if state, ok := dataMap["state"].(string); ok {
						if state == "running" || state == "requiresAction" {
							h.sessions.markRunning(sessionID)
						} else if state == "idle" {
							h.sessions.markIdle(sessionID)
						}
					}
				}
			} else if eventName == "session_status_changed" {
				if dataMap, ok := data.(map[string]interface{}); ok {
					if isIdle, ok := dataMap["isIdle"].(bool); ok && isIdle {
						h.sessions.markIdle(sessionID)
					}
				}
			}

			if eventCount <= 3 || eventName == "todos_updated" || eventName == "turn_completed" || eventName == "error" {
				slog.Info("go-bridge: relayEvents forwarding", "backendID", backendID, "sessionID", sessionID, "event", eventName, "seq", eventCount)
			}

			h.mu.Lock()
			directory := h.sessions.directoryForSession(sessionID)
			h.mu.Unlock()

			h.deltaBatcher.Send(LogicalEvent{
				SessionID: sessionID,
				BackendID: backendID,
				Event:     eventName,
				Data:      data,
				Directory: directory,
				Broadcast: true,
				Offline:   IsDurableMilestone(eventName),
			})

			// 持续刷新 lastEventAt，防止 idle cleanup 在长 turn 期间误杀 session。
			h.sessions.touch(sessionID)

			if ev.Type == core.EventResult && ev.Done {
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "completed", "")
				if relaySurvivesTurnBoundary(backendID) {
					continue
				}
				return
			}
			if ev.Type == core.EventError {
				errMsg := ""
				if ev.Error != nil {
					errMsg = ev.Error.Error()
				}
				// §5.1 #7 producer layer 3（live）：进程崩溃 / turn.failed / app-server error 都
				// 走 core.EventError。若不收口，projection 的 active turn 永久 running（计划
				// §5.1 #7 line 211）。reducer 的 turn_error case（5df5a28）settle active turn；
				// turnId 省略时 reducer 回退到 ActiveTurnID。仅对 EventError 即终态的非 claude
				// backend 合成（claude 的 EventError 可恢复，loop continue 不收口）。
				if backendID != "claude" && backendID != "claudecode" {
					h.deltaBatcher.Send(LogicalEvent{
						SessionID: sessionID,
						BackendID: backendID,
						Event:     "turn_error",
						Data:      map[string]interface{}{"turnId": ev.TurnID, "message": errMsg},
						Directory: directory,
						Broadcast: true,
						Offline:   true,
					})
				}
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "error", errMsg)
				if relaySurvivesTurnBoundary(backendID) {
					continue
				}
				return
			}

		case <-idleTimer.C:
			if disablesRelayIdleTimeout(backendID) {
				continue
			}
			slog.Warn("go-bridge: relayEvents idle timeout, auto-completing", "backendID", backendID, "sessionID", sessionID, "eventsSeen", eventCount)
			if !h.sessions.isIdle(sessionID) {
				h.mu.Lock()
				dir := h.sessions.directoryForSession(sessionID)
				h.mu.Unlock()
				h.deltaBatcher.Send(LogicalEvent{
					SessionID: sessionID,
					BackendID: backendID,
					Event:     "turn_completed",
					Data:      map[string]interface{}{"done": true, "text": ""},
					Directory: dir,
					Broadcast: true,
					Offline:   true,
				})
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "completed", "relay_idle_timeout")
			}
			return
		}
	}
}

func (h *Handlers) routeRelayOfflineStampedEvent(eventMsg EventMessage) {
	if !IsDurableMilestone(eventMsg.Event) {
		return
	}
	h.mu.Lock()
	store := h.trustedDevices
	sender := h.relayEnvelopeSender
	h.mu.Unlock()
	if store == nil || sender == nil || h.relayEventRouter == nil {
		return
	}
	devices, err := store.ListDevices()
	if err != nil {
		slog.Warn("go-bridge: list relay devices for offline delivery failed", "error", err)
		return
	}
	onlineDevices := h.broadcaster.ActiveDeviceIDs()
	mailboxDevices := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.RevokedAt != nil || !device.RelayEnabled || device.IdentityPublicKey == "" {
			continue
		}
		mailboxDevices = append(mailboxDevices, device.DeviceID)
	}
	if len(mailboxDevices) == 0 {
		return
	}
	h.relayEventRouter.RouteStampedEvent(eventMsg, onlineDevices, mailboxDevices)
	for _, deviceID := range mailboxDevices {
		if err := h.relayOutbox.Flush(deviceID, sender); err != nil {
			slog.Warn("go-bridge: relay offline delivery flush failed", "deviceID", safeID(deviceID), "error", err)
		}
	}
}

func (h *Handlers) FlushRelayOutboxes() {
	h.mu.Lock()
	store := h.trustedDevices
	sender := h.relayEnvelopeSender
	h.mu.Unlock()
	if store == nil || sender == nil || h.relayOutbox == nil {
		return
	}
	devices, err := store.ListDevices()
	if err != nil {
		slog.Warn("go-bridge: list relay devices for outbox flush failed", "error", err)
		return
	}
	for _, device := range devices {
		if device.RevokedAt != nil || !device.RelayEnabled || device.IdentityPublicKey == "" {
			continue
		}
		if err := h.relayOutbox.Flush(device.DeviceID, sender); err != nil {
			slog.Warn("go-bridge: relay outbox flush failed", "deviceID", safeID(device.DeviceID), "error", err)
		}
	}
}
