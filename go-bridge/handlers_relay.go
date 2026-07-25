package gobridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

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
func (h *Handlers) startRelayIfNotRunning(sessionID string, sess core.AgentSession, conn Connection, backendID string) {
	h.mu.Lock()
	running := h.relayRunning[sessionID] && h.relayRunningKind[sessionID] == relayKindAgent
	if !running {
		h.relayRunning[sessionID] = true
		h.relayRunningKind[sessionID] = relayKindAgent
	}
	h.mu.Unlock()
	if !running {
		go h.relayEvents(conn, sess, sessionID, backendID)
	}
}

// startClaudeSessionFileRelay 为没有 AgentSession 的 Claude Desktop session
// 启动基于 transcript 文件监视的事件转发。当 iOS 调用 resume_session 或
// get_session_messages 打开一个已在外部运行/已完成的 session 时，
// handleResumeSession 不创建 AgentSession（设计如此），导致 relayEvents 永远
// 不会启动。本函数通过轮询 .jsonl 文件变化来代替内存事件通道，向 iOS 广播
// turn_started / turn_completed / session_state_changed 事件。
func (h *Handlers) startClaudeSessionFileRelay(sessionID string, conn Connection, backendID string) {
	if backendID != "claude" && backendID != "claudecode" {
		return
	}
	h.mu.Lock()
	running := h.relayRunning[sessionID]
	if !running {
		h.relayRunning[sessionID] = true
		h.relayRunningKind[sessionID] = relayKindClaudeFile
	}
	h.mu.Unlock()
	if running {
		return // 已有标准 relay 或文件 relay 在运行
	}

	go h.claudeSessionFileRelayLoop(sessionID, conn, backendID)
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
func (h *Handlers) grokLeaderSessionRelayLoop(sessionID, backendID string, sub core.SessionEventSubscriber, relayKey, cwd string) {
	defer func() {
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
		if eventName == "turn_started" {
			h.sessions.markRunning(sessionID)
		} else if eventName == "turn_completed" || eventName == "error" {
			h.sessions.markIdle(sessionID)
		}
		h.sendSessionEvent(sessionID, backendID, eventName, data)
	}
}

func (h *Handlers) sessionLiveProcess(ctx context.Context, sessionID, backendID string) (core.LiveSessionProcess, core.LiveSessionLister, error) {
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
			// iOS 仍订阅该 session 时持续 watch（push 模型）。无订阅者时也不再因软 TTL 退出：iOS 打开
			// idle 外部 session 后常停轮询，若 relay 在 Mac 端稍后发 turn 前退出会错过整轮（owner
			// 复现：打开 idle session → relay 90s 退出 → 后来发任务 → 无 live 同步）。subscriber 现仅
			// 作日志，不再当退出门槛；只用 hardCap 回收 goroutine，running 复核保留。
			if h.codexSessionHasSubscriber(sessionID, backendID) {
				continue
			}
			since := time.Since(lastGrowth)
			shouldExit := false
			switch {
			case codexFileRelayNoGrowthHardCap > 0 && since >= codexFileRelayNoGrowthHardCap:
				shouldExit = true
			case codexFileRelayNoGrowthTTL > 0 && since >= codexFileRelayNoGrowthTTL:
				if !recheckedAfterTTL {
					recheckedAfterTTL = true
					if h.detectCodexTranscriptTaskState(sessPath) == "running" {
						slog.Info("go-bridge: codexSessionFileRelay no-growth TTL elapsed but task still running; keep watching", "sessionID", sessionID, "idleFor", since.String())
					} else {
						slog.Info("go-bridge: codexSessionFileRelay no-growth TTL elapsed, idle — keep watching until hardCap (no-subscriber no longer exits)", "sessionID", sessionID, "idleFor", since.String(), "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
					}
				}
			}
			if shouldExit {
				if !h.sessions.isIdle(sessionID) {
					h.broadcastIdleState(sessionID, backendID)
				}
				slog.Info("go-bridge: codexSessionFileRelay no-growth hardCap elapsed, exiting", "sessionID", sessionID, "idleFor", since.String(), "subscribed", h.codexSessionHasSubscriber(sessionID, backendID))
				return
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

func (h *Handlers) claudeSessionFileRelayLoop(sessionID string, conn Connection, backendID string) {
	defer func() {
		h.clearRelayKindIf(sessionID, relayKindClaudeFile)
		slog.Info("go-bridge: claudeSessionFileRelay exited", "sessionID", sessionID)
	}()

	_, sessPath := findClaudeSessionFile(sessionID, "")
	if sessPath == "" {
		slog.Debug("go-bridge: claudeSessionFileRelay no transcript file found", "sessionID", sessionID)
		return
	}
	slog.Info("go-bridge: claudeSessionFileRelay started", "sessionID", sessionID, "path", sessPath)
	if !h.relayKindIs(sessionID, relayKindClaudeFile) {
		slog.Info("go-bridge: claudeSessionFileRelay superseded before initial scan", "sessionID", sessionID)
		return
	}

	// 读取当前文件大小作为初始偏移，只检测新增内容。
	offset := func() int64 {
		info, err := os.Stat(sessPath)
		if err != nil {
			return 0
		}
		return info.Size()
	}()

	initialEntry := h.classifyClaudeTranscriptFile(sessPath)
	proc, liveLister, err := h.sessionLiveProcess(context.Background(), sessionID, backendID)
	if err != nil {
		slog.Warn("go-bridge: claudeSessionFileRelay live process lookup failed", "sessionID", sessionID, "backendID", backendID, "error", err)
	}
	live := err == nil && proc.Live
	cachedPID := proc.PID
	if !live {
		h.broadcastIdleState(sessionID, backendID)
		slog.Info("go-bridge: claudeSessionFileRelay initial process not live, broadcasting idle and exiting", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
		return
	}

	// Session 仍在运行中，开始轮询监视新内容。
	ticker := time.NewTicker(claudeFileRelayPollInterval)
	defer ticker.Stop()
	lastMeaningfulGrowth := time.Now()
	runningObserved := false
	processDeathMisses := 0

	switch {
	case !initialEntry.hasMeaningfulEntry || initialEntry.finalAssistant:
		h.broadcastIdleState(sessionID, backendID)
		slog.Info("go-bridge: claudeSessionFileRelay initial idle but process live; watching", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
	case initialEntry.entryType == "user" && initialEntry.interrupt:
		h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"done": true, "reason": "user_interrupt"})
		h.broadcastIdleState(sessionID, backendID)
		slog.Info("go-bridge: claudeSessionFileRelay initial interrupt marker with live process; watching", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
	case initialEntry.entryType == "user":
		h.sessions.markRunning(sessionID)
		h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": ""})
		runningObserved = true
	case initialEntry.entryType == "assistant":
		h.sessions.markRunning(sessionID)
		h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
		runningObserved = true
	}

	for range ticker.C {
		if !h.relayKindIs(sessionID, relayKindClaudeFile) {
			slog.Info("go-bridge: claudeSessionFileRelay superseded by agent relay", "sessionID", sessionID)
			return
		}
		if liveLister != nil && cachedPID > 0 {
			if !liveLister.IsProcessAlive(context.Background(), cachedPID) {
				processDeathMisses++
				if processDeathMisses >= claudeFileRelayProcessDeathMisses {
					h.broadcastIdleState(sessionID, backendID)
					slog.Info("go-bridge: claudeSessionFileRelay process dead, exiting", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
					return
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
		if newSize <= offset {
			// 文件没有增长，可能被截断重写（truncate）。
			if newSize < offset {
				offset = 0
				lastMeaningfulGrowth = time.Now()
				continue
			}
			// Only exit on live-idle TTL when the Claude process is no longer alive.
			// A live process with a quiet transcript is normal during long thinking; exiting
			// here drops turn_started/turn_completed for the next Mac message until the next
			// get_session_messages restarts the relay (Web/iOS then miss the external turn).
			processStillLive := cachedPID > 0 && liveLister != nil && liveLister.IsProcessAlive(context.Background(), cachedPID)
			if !runningObserved && !processStillLive && claudeFileRelayLiveIdleTTL > 0 && time.Since(lastMeaningfulGrowth) >= claudeFileRelayLiveIdleTTL {
				if !h.sessions.isIdle(sessionID) {
					h.broadcastIdleState(sessionID, backendID)
				}
				slog.Info("go-bridge: claudeSessionFileRelay live-idle TTL elapsed, exiting", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
				return
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

		entries, err := scanClaudeRelayEntriesFromReader(f)
		f.Close()
		if err != nil {
			continue
		}
		if len(entries) == 0 {
			offset = newSize
			continue
		}

		offset = newSize
		lastMeaningfulGrowth = time.Now()

		// Phase 1：遍历新增区间内所有 meaningful 记录（不再只取最后一条）。
		// user→turn_started（interrupt→turn_completed）；assistant 按 content block 发
		// text_delta(text)/reasoning_delta(thinking)/tool_started(tool_use)，最终 end_turn→turn_completed。
		for _, e := range entries {
			switch e.Type {
			case "user":
				if isClaudeUserInterruptRelayEntry(e) {
					h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"done": true, "reason": "user_interrupt"})
					h.broadcastIdleState(sessionID, backendID)
					runningObserved = false
				} else {
					h.sessions.markRunning(sessionID)
					h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": ""})
					runningObserved = true
				}
			case "assistant":
				h.emitClaudeAssistantContent(sessionID, backendID, e)
				if isFinalClaudeStopReason(e.Message.StopReason) {
					// 任务完成 → turn_completed(idle)。进程仍 live 时继续监视（同 PID 多轮外部 turn）。
					h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"done": true, "reason": "end_turn"})
					h.broadcastIdleState(sessionID, backendID)
					runningObserved = false
					slog.Info("go-bridge: claudeSessionFileRelay turn completed, keeping watch while process live", "sessionID", sessionID, "backendID", backendID, "pid", cachedPID)
				} else {
					runningObserved = true
				}
			}
		}
	}
}

type claudeTranscriptRelayEntry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	Message *struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
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

// scanClaudeRelayEntriesFromReader 返回新增字节内所有 meaningful user/assistant 记录
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
		if entry.Message == nil {
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
		}
	}
	return last, nil
}

// claudeRelayContentBlock 是 assistant/user message.content 数组里的一个 block
//（text/thinking/tool_use/tool_result）。
type claudeRelayContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`      // text
	Thinking  string          `json:"thinking"`  // thinking
	Name      string          `json:"name"`      // tool_use
	Input     json.RawMessage `json:"input"`     // tool_use
	ID        string          `json:"id"`        // tool_use
	ToolUseID string          `json:"tool_use_id"` // tool_result
	Content   json.RawMessage `json:"content"`   // tool_result (string or block array)
	IsError   bool            `json:"is_error"`  // tool_result
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
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := os.Open(sessPath)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16)
	skipNextResumeNoResponse := false
	currentTurnID := ""
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
		for _, ev := range claudeEntryToProjectionEvents(e, &currentTurnID) {
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
func claudeEntryToProjectionEvents(e claudeTranscriptRelayEntry, currentTurnID *string) []projectionHydrateEvent {
	if e.Message == nil {
		return nil
	}
	msgID := e.Message.ID
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
			data := map[string]interface{}{"toolResult": claudeToolResultText(b), "toolStatus": "completed"}
			if b.ToolUseID != "" {
				data["itemId"] = b.ToolUseID
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
		*currentTurnID = msgID
		out = append(out, projectionHydrateEvent{Event: "user_message", Data: map[string]interface{}{
			"itemId": msgID, "turnId": msgID, "text": text,
		}})
		return out
	}
	// assistant
	turnID := *currentTurnID
	if turnID == "" {
		turnID = msgID // assistant before any user prompt (rare) — self-keyed
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
		case "tool_use":
			data := map[string]interface{}{"toolName": b.Name}
			if len(b.Input) > 0 && string(b.Input) != "null" {
				data["toolInput"] = json.RawMessage(b.Input)
			}
			if b.ID != "" {
				data["itemId"] = b.ID
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

// emitClaudeAssistantContent 按 content block 发 text_delta(text) / reasoning_delta(thinking)
// / tool_started(tool_use)。Phase 1：外部 Claude turn 进行中实时推送 reasoning + 文本 + 工具，
// 而非只在 end_turn 落盘后等重连。payload 形状对齐 mapAgentEvent 的 EventToolUse。
func (h *Handlers) emitClaudeAssistantContent(sessionID, backendID string, e claudeTranscriptRelayEntry) {
	if e.Message == nil {
		return
	}
	for _, b := range claudeRelayContentBlocks(e.Message.Content) {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				h.sendSessionEvent(sessionID, backendID, "text_delta", map[string]interface{}{"delta": b.Text})
			}
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				h.sendSessionEvent(sessionID, backendID, "reasoning_delta", map[string]interface{}{"delta": b.Thinking})
			}
		case "tool_use":
			payload := map[string]interface{}{"toolName": b.Name}
			if len(b.Input) > 0 && string(b.Input) != "null" {
				payload["toolInputRaw"] = string(b.Input)
			}
			if b.ID != "" {
				payload["itemId"] = b.ID
			}
			h.sendSessionEvent(sessionID, backendID, "tool_started", payload)
		}
	}
}

func (h *Handlers) classifyClaudeTranscriptFile(sessPath string) claudeTranscriptRelayMeaningfulEntry {
	transcriptStateProbe()
	f, err := os.Open(sessPath)
	if err != nil {
		return claudeTranscriptRelayMeaningfulEntry{}
	}
	defer f.Close()
	entry, err := classifyLastMeaningfulClaudeRelayEntryFromReader(f)
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
	return "unknown"
}

func (h *Handlers) detectCodexTranscriptTaskState(sessPath string) string {
	state, _ := h.detectCodexTranscriptTask(sessPath)
	return state
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
		}
	}
	return state, turnID
}

// scanCodexTranscriptTaskEvents 提取 lifecycle 事件（task_started/task_complete），
// 供 detectCodexTranscriptTaskState 判定当前态。委托给统一扫描器后过滤。
func (h *Handlers) scanCodexTranscriptTaskEvents(sessPath string, offset int64) []codexRelayEvent {
	var events []codexRelayEvent
	for _, ev := range scanCodexTranscriptRelayEvents(sessPath, offset) {
		if ev.kind == "task_started" || ev.kind == "task_complete" {
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
	toolName   string                 // tool_started/tool_finished
	toolInput  string                 // tool_started（custom_tool_call.input JS 串）
	toolResult string                 // tool_finished（custom_tool_call_output.output 拼接）
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
	Arguments string          `json:"arguments"`
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
	Type string `json:"type"` // output_text / input_text
	Text string `json:"text"`
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
						out = append(out, codexRelayEvent{kind: "text", text: b.Text})
					}
				}
			case "user":
				var texts []string
				for _, b := range p.Content {
					if b.Type == "input_text" && strings.TrimSpace(b.Text) != "" {
						texts = append(texts, b.Text)
					}
				}
				if len(texts) > 0 {
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
					out = append(out, codexRelayEvent{kind: "reasoning", text: s.Text})
				}
			}
		case "custom_tool_call":
			// exec-unified：name 恒 "exec"，真实操作埋在 input JS 串里（非结构化字段）。
			out = append(out, codexRelayEvent{kind: "tool_started", toolName: p.Name, toolInput: p.Input, itemId: p.CallID})
		case "custom_tool_call_output":
			out = append(out, codexRelayEvent{
				kind:       "tool_finished",
				itemId:     p.CallID,
				toolResult: extractCodexToolOutput(p.Output),
			})
		// Native Codex tool shapes (session 019f8dd1 / 2026-07: function_call dominates).
		// Previously only custom_tool_* was mapped → live tool_* EMIT=0 for those turns.
		case "function_call":
			input := p.Arguments
			if input == "" {
				input = p.Input
			}
			out = append(out, codexRelayEvent{
				kind:     "tool_started",
				toolName: p.Name,
				toolInput: input,
				itemId:   p.CallID,
			})
		case "function_call_output":
			out = append(out, codexRelayEvent{
				kind:       "tool_finished",
				itemId:     p.CallID,
				toolResult: extractCodexToolOutput(p.Output),
			})
		case "local_shell_call", "mcp_call", "web_search_call":
			input := p.Arguments
			if input == "" {
				input = p.Input
			}
			name := p.Name
			if name == "" {
				name = p.Type
			}
			out = append(out, codexRelayEvent{
				kind:      "tool_started",
				toolName:  name,
				toolInput: input,
				itemId:    p.CallID,
			})
		case "local_shell_call_output", "mcp_call_output", "web_search_call_output":
			out = append(out, codexRelayEvent{
				kind:       "tool_finished",
				itemId:     p.CallID,
				toolResult: extractCodexToolOutput(p.Output),
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
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := os.Open(sessPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	scanner := bufio.NewScanner(f)
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
	case "claude", "claudecode", "codex", "opencode":
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
func (h *Handlers) relayEvents(conn Connection, sess core.AgentSession, sessionID, backendID string) {
	origSessionID := sessionID
	defer func() {
		h.clearRelayKindIf(origSessionID, relayKindAgent)
		h.clearRelayKindIf(sessionID, relayKindAgent)
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
				if backendID == "claude" || backendID == "claudecode" {
					continue
				}
				return
			}
			if ev.Type == core.EventError {
				errMsg := ""
				if ev.Error != nil {
					errMsg = ev.Error.Error()
				}
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "error", errMsg)
				if backendID == "claude" || backendID == "claudecode" {
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
