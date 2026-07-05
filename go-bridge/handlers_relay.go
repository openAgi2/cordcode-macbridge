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

	state := h.detectCodexTranscriptTaskState(sessPath)
	switch state {
	case "idle":
		h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"done": true, "reason": "task_complete"})
		h.broadcastIdleState(sessionID, backendID)
		return
	case "running":
		h.sessions.markRunning(sessionID)
		h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
	}

	const pollInterval = time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		info, err := os.Stat(sessPath)
		if err != nil {
			continue
		}
		newSize := info.Size()
		if newSize <= offset {
			if newSize < offset {
				offset = 0
			}
			continue
		}

		events := h.scanCodexTranscriptTaskEvents(sessPath, offset)
		offset = newSize
		for _, eventName := range events {
			switch eventName {
			case "task_started":
				h.sessions.markRunning(sessionID)
				h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": ""})
				h.sendSessionEvent(sessionID, backendID, "session_state_changed", map[string]interface{}{"state": "running"})
			case "task_complete":
				h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"done": true, "reason": "task_complete"})
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "completed", "task_complete")
				return
			}
		}
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
			if !runningObserved && claudeFileRelayLiveIdleTTL > 0 && time.Since(lastMeaningfulGrowth) >= claudeFileRelayLiveIdleTTL {
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

		entry, err := classifyLastMeaningfulClaudeRelayEntryFromReader(f)
		f.Close()
		if err != nil {
			continue
		}
		if !entry.hasMeaningfulEntry {
			offset = newSize
			continue
		}

		offset = newSize
		lastMeaningfulGrowth = time.Now()

		// 广播事件。
		if entry.entryType == "user" && !entry.interrupt {
			// 用户发送新消息 → turn_started
			h.sessions.markRunning(sessionID)
			h.sendSessionEvent(sessionID, backendID, "turn_started", map[string]interface{}{"turnId": ""})
			runningObserved = true
		} else if entry.interrupt {
			// 用户中断 → turn_completed(idle)
			h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"done": true, "reason": "user_interrupt"})
			h.broadcastIdleState(sessionID, backendID)
			runningObserved = false
			// 中断后 session 可能还会被继续，继续监视。
		} else if entry.entryType == "assistant" {
			if entry.finalAssistant {
				// 任务完成 → turn_completed(idle)
				h.sendSessionEvent(sessionID, backendID, "turn_completed", map[string]interface{}{"done": true, "reason": "end_turn"})
				h.broadcastIdleState(sessionID, backendID)
				slog.Info("go-bridge: claudeSessionFileRelay turn completed, exiting", "sessionID", sessionID)
				return // 任务完成，退出文件监视。
			}
			runningObserved = true
			// assistant 消息但不是最终（如 tool_use），继续监视。
		}
	}
}

type claudeTranscriptRelayEntry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	Message *struct {
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

func classifyLastMeaningfulClaudeRelayEntryFromReader(r io.Reader) (claudeTranscriptRelayMeaningfulEntry, error) {
	var last claudeTranscriptRelayMeaningfulEntry
	skipNextResumeNoResponse := false

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
		switch entry.Type {
		case "user":
			last = claudeTranscriptRelayMeaningfulEntry{
				hasMeaningfulEntry: true,
				entryType:          "user",
				interrupt:          isClaudeUserInterruptRelayEntry(entry),
			}
		case "assistant":
			last = claudeTranscriptRelayMeaningfulEntry{
				hasMeaningfulEntry: true,
				entryType:          "assistant",
				finalAssistant:     isFinalClaudeStopReason(entry.Message.StopReason),
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return claudeTranscriptRelayMeaningfulEntry{}, err
	}
	return last, nil
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
	events := h.scanCodexTranscriptTaskEvents(sessPath, 0)
	state := "unknown"
	for _, eventName := range events {
		switch eventName {
		case "task_started":
			state = "running"
		case "task_complete":
			state = "idle"
		}
	}
	return state
}

func (h *Handlers) scanCodexTranscriptTaskEvents(sessPath string, offset int64) []string {
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

	var events []string
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16)
	for scanner.Scan() {
		var entry struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || entry.Type != "event_msg" {
			continue
		}
		eventType := codexEventPayloadType(entry.Payload)
		if eventType == "task_started" || eventType == "task_complete" {
			events = append(events, eventType)
		}
	}
	return events
}

func codexEventPayloadType(raw json.RawMessage) string {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if nested, ok := payload["payload"].(map[string]any); ok {
		payload = nested
	}
	return strings.TrimSpace(fmt.Sprint(payload["type"]))
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
					h.seq++
					seq := h.seq
					dir := h.sessions.directoryForSession(sessionID)
					h.mu.Unlock()

					compMsg := EventMessage{
						Type:      "event",
						SessionID: sessionID,
						BackendID: backendID,
						Event:     "turn_completed",
						Data:      map[string]interface{}{"done": true, "reason": "events_channel_closed"},
						Seq:       seq,
					}
					h.broadcaster.Send(BroadcastEvent{
						BackendID: backendID,
						SessionID: sessionID,
						Directory: dir,
						Message:   compMsg,
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
			h.seq++
			seq := h.seq
			directory := h.sessions.directoryForSession(sessionID)
			h.mu.Unlock()

			msg := EventMessage{
				Type:      "event",
				SessionID: sessionID,
				BackendID: backendID,
				Event:     eventName,
				Data:      data,
				Seq:       seq,
			}
			h.broadcaster.Send(BroadcastEvent{
				BackendID: backendID,
				SessionID: sessionID,
				Directory: directory,
				Message:   msg,
			})
			h.routeRelayOfflineEvent(sessionID, backendID, eventName, data)

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
				h.seq++
				seq := h.seq
				dir := h.sessions.directoryForSession(sessionID)
				h.mu.Unlock()
				completeMsg := EventMessage{
					Type:      "event",
					SessionID: sessionID,
					BackendID: backendID,
					Event:     "turn_completed",
					Data:      map[string]interface{}{"done": true, "text": ""},
					Seq:       seq,
				}
				h.broadcaster.Send(BroadcastEvent{
					BackendID: backendID,
					SessionID: sessionID,
					Directory: dir,
					Message:   completeMsg,
				})
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "completed", "relay_idle_timeout")
			}
			return
		}
	}
}

func (h *Handlers) routeRelayOfflineEvent(sessionID, backendID, eventName string, data interface{}) {
	if !IsDurableMilestone(eventName) {
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
	h.relayEventRouter.RouteEvent(sessionID, backendID, eventName, data, onlineDevices, mailboxDevices)
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
