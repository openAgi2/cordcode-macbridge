package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sseSubscriber 通过 HTTP SSE /global/event 端点被动订阅 OpenCode 服务端事件。
// 和 Codex passive subscriber 类似，不需要发送消息就能接收所有 session 的实时事件。
//
// 也被 opencodeServerSession 复用为 active-turn 的事件源：构造时通过
// setSessionFilter 锁定单个 sessionID，emit 只放行该 session 的事件，让 active
// session 复用全套解析 + dedup + turn 生命周期翻译逻辑，只收自己的事件。
type sseSubscriber struct {
	url        string
	authHeader string
	workDir    string

	events chan core.Event
	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	wg        sync.WaitGroup

	stateMu      sync.Mutex
	messageRoles map[string]string
	messageIDs   map[string]string
	partKinds    map[string]string
	partContent  map[string]string
	completed    map[string]bool

	// sessionFilter (active mode): when filterActive is true, emit drops any
	// event whose SessionID != sessionFilter. Lock-free via atomics so emit
	// stays cheap. Empty/resume case leaves filterActive false (observe all).
	filterActive atomic.Bool
	sessionFilter atomic.Value // string
}

func newSSESubscriber(ctx context.Context, a *Agent) *sseSubscriber {
	a.mu.RLock()
	url := a.httpBaseURL
	auth := a.httpAuthHeader
	dir := a.workDir
	a.mu.RUnlock()

	subCtx, cancel := context.WithCancel(ctx)
	return &sseSubscriber{
		url:          strings.TrimRight(url, "/"),
		authHeader:   auth,
		workDir:      dir,
		events:       make(chan core.Event, 128),
		ctx:          subCtx,
		cancel:       cancel,
		messageRoles: make(map[string]string),
		messageIDs:   make(map[string]string),
		partKinds:    make(map[string]string),
		partContent:  make(map[string]string),
		completed:    make(map[string]bool),
	}
}

func (s *sseSubscriber) connect() error {
	if s.url == "" {
		return fmt.Errorf("opencode: SSE subscription requires HTTP server URL")
	}

	sseURL := s.url + "/global/event"

	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return fmt.Errorf("opencode SSE subscriber: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if s.authHeader != "" {
		req.Header.Set("Authorization", s.authHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("opencode SSE subscriber connect %s: %w", sseURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("opencode SSE subscriber: HTTP %d", resp.StatusCode)
	}

	slog.Info("opencode SSE subscriber connected", "url", sseURL)

	s.wg.Add(1)
	go s.readLoop(resp.Body)

	return nil
}

// readLoop 读取 SSE 流，同时兼容 NDJSON 格式（某些版本可能不用 SSE 封装）。
func (s *sseSubscriber) readLoop(body io.ReadCloser) {
	defer s.wg.Done()
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	// SSE 事件缓冲
	var currentData strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// SSE 空行 = 事件分隔符
		if line == "" {
			data := currentData.String()
			currentData.Reset()
			if data == "" {
				continue
			}
			s.handleRawEvent(strings.TrimSpace(data))
			continue
		}

		// SSE data: 行
		if strings.HasPrefix(line, "data: ") {
			currentData.WriteString(strings.TrimPrefix(line, "data: "))
			currentData.WriteString("\n")
			continue
		}
		if strings.HasPrefix(line, "data:") {
			currentData.WriteString(strings.TrimPrefix(line, "data:"))
			currentData.WriteString("\n")
			continue
		}

		// 忽略 SSE event:/id:/retry: 行
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") || strings.HasPrefix(line, ":") {
			continue
		}

		// 非 SSE 前缀的 JSON 行：当作 NDJSON 处理
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{") {
			s.handleRawEvent(trimmed)
		}
	}

	if err := scanner.Err(); err != nil {
		if s.ctx.Err() == nil {
			slog.Debug("opencode SSE subscriber read error", "error", err)
		}
	}
}

// handleRawEvent 将 SSE data 或 NDJSON 行解析为 core.Event。
// OpenCode server 的 /global/event 使用 payload 包装：
//
//	{"payload":{"type":"message.part.delta","properties":{"sessionID":"...","delta":"..."}}}
//
// CLI `opencode run --format json` 的 NDJSON 仍是顶层 type/part 结构。这里同时
// 保留兼容，避免被动订阅路径影响主动 CLI session 路径的格式假设。
func (s *sseSubscriber) handleRawEvent(data string) {
	data = strings.TrimSpace(data)
	if data == "" {
		return
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		slog.Debug("opencode SSE subscriber: non-JSON data", "data", truncate(data, 200))
		return
	}

	if payload, _ := raw["payload"].(map[string]any); payload != nil {
		s.handleServerEvent(payload)
		return
	}

	eventType, _ := raw["type"].(string)
	if isServerEventType(eventType) {
		s.handleServerEvent(raw)
		return
	}
	sessionID, _ := raw["sessionID"].(string)

	switch eventType {
	case "text":
		s.handleText(raw, sessionID)
	case "reasoning":
		s.handleReasoning(raw, sessionID)
	case "tool_use":
		s.handleToolUse(raw, sessionID)
	case "step_start":
		s.handleStepStart(raw, sessionID)
	case "step_finish":
		s.handleStepFinish(raw, sessionID)
	case "error":
		s.handleError(raw, sessionID)
	default:
		slog.Debug("opencode SSE subscriber: unhandled event", "type", eventType)
	}
}

func (s *sseSubscriber) handleServerEvent(payload map[string]any) {
	eventType, _ := payload["type"].(string)
	properties, _ := payload["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
	}
	sessionID := extractSSESessionID(properties)

	switch eventType {
	case "message.updated":
		s.handleSSEMessageUpdated(properties, sessionID)
	case "message.part.delta":
		s.handleSSEPartDelta(properties, sessionID)
	case "message.part.updated":
		s.handleSSEPartUpdated(properties, sessionID)
	case "session.status":
		s.handleSSESessionStatus(properties, sessionID)
	case "session.updated":
		s.handleSSESessionUpdated(properties, sessionID)
	case "todo.updated":
		s.handleSSETodoUpdated(properties, sessionID)
	case "permission.asked":
		s.handleSSEPermissionAsked(properties, sessionID)
	case "server.connected", "session.created", "session.deleted", "message.removed", "message.part.removed", "session.diff":
		// 非 chat 内容事件，无需转发给统一流。
	default:
		slog.Debug("opencode SSE subscriber: unhandled server event", "type", eventType)
	}
}

func (s *sseSubscriber) handleText(raw map[string]any, sessionID string) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}
	text, _ := part["text"].(string)
	if text != "" {
		s.emit(core.Event{Type: core.EventText, Content: text, SessionID: sessionID})
	}
}

func (s *sseSubscriber) handleReasoning(raw map[string]any, sessionID string) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}
	text, _ := part["text"].(string)
	if text != "" {
		s.emit(core.Event{Type: core.EventThinking, Content: text, SessionID: sessionID})
	}
}

func (s *sseSubscriber) handleToolUse(raw map[string]any, sessionID string) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}

	toolName, _ := part["tool"].(string)
	state, _ := part["state"].(map[string]any)
	status := ""
	if state != nil {
		status, _ = state["status"].(string)
	}
	input := extractToolInput(state)
	partID := firstString(part, "id", "partID", "partId")

	if status == "completed" {
		s.emit(core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: input, SessionID: sessionID, RequestID: partID})
		output, _ := state["output"].(string)
		s.emit(core.Event{Type: core.EventToolResult, ToolName: toolName, Content: truncate(output, 500), SessionID: sessionID, RequestID: partID})
	} else {
		s.emit(core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: input, SessionID: sessionID, RequestID: partID})
	}
}

func (s *sseSubscriber) handleStepStart(raw map[string]any, sessionID string) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}
	// step_start 可能携带 OpenCode 内部 sessionID
	if sid, ok := part["sessionID"].(string); ok && sid != "" && sessionID == "" {
		sessionID = sid
	}
	slog.Debug("opencode SSE subscriber: step started", "session", sessionID)
}

func (s *sseSubscriber) handleStepFinish(raw map[string]any, sessionID string) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}
	reason, _ := part["reason"].(string)
	slog.Debug("opencode SSE subscriber: step finished", "reason", reason, "session", sessionID)

	// step_finish 表示一次 turn 完成
	s.emit(core.Event{Type: core.EventResult, SessionID: sessionID, Done: true})
}

func (s *sseSubscriber) handleError(raw map[string]any, sessionID string) {
	errMsg := extractErrorMessage(raw)
	if errMsg != "" {
		s.emit(core.Event{Type: core.EventError, SessionID: sessionID, Error: fmt.Errorf("%s", errMsg)})
	}
}

func (s *sseSubscriber) handleSSEMessageUpdated(properties map[string]any, sessionID string) {
	info := firstMap(properties, "info", "message")
	if info == nil {
		return
	}
	messageID := firstString(info, "id", "messageID", "messageId")
	role := firstString(info, "role")
	if sessionID == "" {
		sessionID = extractSSESessionID(info)
	}
	if messageID != "" && role != "" {
		s.stateMu.Lock()
		s.messageRoles[messageID] = role
		if sessionID != "" {
			s.messageIDs[messageID] = sessionID
		}
		s.stateMu.Unlock()
	}
	if sessionID != "" && role == "user" {
		s.resetCompletion(sessionID)
	}
	if sessionID == "" || role != "assistant" {
		return
	}
	for _, part := range extractMessageParts(info) {
		partID := firstString(part, "id", "partID", "partId")
		kind := firstString(part, "type")
		if kind == "" {
			continue
		}
		s.rememberPartKind(sessionID, messageID, partID, kind)
		switch kind {
		case "text":
			text := firstString(part, "text", "content")
			if d := s.deltaForPartSnapshot(sessionID, messageID, partID, kind, text); d.content != "" {
				eventType := core.EventText
				if d.isComplete {
					eventType = core.EventTextReplace
				}
				s.emit(core.Event{Type: eventType, Content: d.content, SessionID: sessionID})
			}
		case "reasoning":
			text := firstString(part, "text", "content")
			if d := s.deltaForPartSnapshot(sessionID, messageID, partID, kind, text); d.content != "" {
				eventType := core.EventThinking
				if d.isComplete {
					eventType = core.EventTextReplace
				}
				s.emit(core.Event{Type: eventType, Content: d.content, SessionID: sessionID})
			}
		case "tool":
			s.handleSSEToolPart(part, sessionID)
		}
	}
	if isCompletedMessage(info) {
		s.emitResultOnce(sessionID)
	}
}

func (s *sseSubscriber) handleSSEPartDelta(properties map[string]any, sessionID string) {
	messageID := firstString(properties, "messageID", "messageId")
	if s.isUserMessage(messageID) {
		return
	}
	if sessionID == "" {
		sessionID = s.sessionIDForMessage(messageID)
	}
	field := firstString(properties, "field")
	delta := firstString(properties, "delta")
	if delta == "" {
		return
	}
	partID := firstString(properties, "partID", "partId")
	kind := s.kindForPart(sessionID, messageID, partID, field)
	switch kind {
	case "reasoning":
		s.appendPartContent(sessionID, messageID, partID, kind, delta)
		s.emit(core.Event{Type: core.EventThinking, Content: delta, SessionID: sessionID})
	case "text", "":
		s.appendPartContent(sessionID, messageID, partID, "text", delta)
		s.emit(core.Event{Type: core.EventText, Content: delta, SessionID: sessionID})
	default:
		slog.Debug("opencode SSE subscriber: ignored part delta", "kind", kind, "field", field)
	}
}

func (s *sseSubscriber) handleSSEPartUpdated(properties map[string]any, sessionID string) {
	part := firstMap(properties, "part")
	if part == nil {
		return
	}
	messageID := firstString(properties, "messageID", "messageId")
	if messageID == "" {
		messageID = firstString(part, "messageID", "messageId")
	}
	if s.isUserMessage(messageID) {
		return
	}
	if sessionID == "" {
		sessionID = firstString(part, "sessionID", "sessionId")
	}
	if sessionID == "" {
		sessionID = s.sessionIDForMessage(messageID)
	}
	partID := firstString(properties, "partID", "partId")
	if partID == "" {
		partID = firstString(part, "id", "partID", "partId")
	}
	kind := firstString(part, "type")
	if kind == "" {
		kind = s.kindForPart(sessionID, messageID, partID, "")
	}
	s.rememberPartKind(sessionID, messageID, partID, kind)

	switch kind {
	case "text":
		text := firstString(part, "text", "content")
		if d := s.deltaForPartSnapshot(sessionID, messageID, partID, kind, text); d.content != "" {
			eventType := core.EventText
			if d.isComplete {
				eventType = core.EventTextReplace
			}
			s.emit(core.Event{Type: eventType, Content: d.content, SessionID: sessionID})
		}
	case "reasoning":
		text := firstString(part, "text", "content")
		if d := s.deltaForPartSnapshot(sessionID, messageID, partID, kind, text); d.content != "" {
			eventType := core.EventThinking
			if d.isComplete {
				eventType = core.EventTextReplace
			}
			s.emit(core.Event{Type: eventType, Content: d.content, SessionID: sessionID})
		}
	case "tool":
		s.handleSSEToolPart(part, sessionID)
	default:
		slog.Debug("opencode SSE subscriber: ignored part update", "kind", kind)
	}
}

func (s *sseSubscriber) handleSSEToolPart(part map[string]any, sessionID string) {
	toolName := firstString(part, "tool", "name")
	if toolName == "" {
		toolName = firstString(firstMap(part, "tool"), "name", "id")
	}
	state := firstMap(part, "state")
	status := firstString(state, "status")
	input := extractToolInput(state)
	if input == "" {
		input = firstString(part, "title")
	}
	partID := firstString(part, "id", "partID", "partId")

	s.emit(core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: input, SessionID: sessionID, RequestID: partID})
	if status == "completed" || status == "error" || status == "failed" {
		output := firstString(state, "output", "result")
		toolStatus := status
		if toolStatus == "error" {
			toolStatus = "failed"
		}
		s.emit(core.Event{
			Type:       core.EventToolResult,
			ToolName:   toolName,
			ToolResult: truncate(output, 500),
			ToolStatus: toolStatus,
			SessionID:  sessionID,
			RequestID:  partID,
		})
	}
}

func (s *sseSubscriber) handleSSESessionStatus(properties map[string]any, sessionID string) {
	status := firstString(properties, "type")
	if status == "" {
		status = firstString(firstMap(properties, "status"), "type", "status")
	}
	if sessionID == "" {
		sessionID = extractSSESessionID(firstMap(properties, "status"))
	}
	if status == "running" && sessionID != "" {
		s.resetCompletion(sessionID)
	}
	if status == "idle" && sessionID != "" {
		s.emitResultOnce(sessionID)
	}
}

func (s *sseSubscriber) handleSSESessionUpdated(properties map[string]any, sessionID string) {
	info := firstMap(properties, "info", "session")
	if info == nil {
		return
	}
	if sessionID == "" {
		sessionID = extractSSESessionID(info)
	}
	status := firstString(info, "status")
	if status == "running" && sessionID != "" {
		s.resetCompletion(sessionID)
	}
	if status == "idle" && sessionID != "" {
		s.emitResultOnce(sessionID)
	}
}

func (s *sseSubscriber) handleSSETodoUpdated(properties map[string]any, sessionID string) {
	rawTodos, ok := properties["todos"].([]any)
	if !ok {
		return
	}
	todos := make([]core.Todo, 0, len(rawTodos))
	for _, rawTodo := range rawTodos {
		todo, _ := rawTodo.(map[string]any)
		if todo == nil {
			continue
		}
		content := firstString(todo, "content", "text", "title")
		if content == "" {
			continue
		}
		status := firstString(todo, "status")
		if status == "" {
			status = "pending"
		}
		priority := firstString(todo, "priority")
		if priority == "" {
			priority = "normal"
		}
		todos = append(todos, core.Todo{Content: content, Status: status, Priority: priority})
	}
	s.emit(core.Event{Type: core.EventPlan, Plan: todos, SessionID: sessionID})
}

func (s *sseSubscriber) handleSSEPermissionAsked(properties map[string]any, sessionID string) {
	id := firstString(properties, "id", "permissionID", "permissionId")
	toolName := firstString(properties, "tool", "toolName")
	input := firstString(properties, "title", "description")
	s.emit(core.Event{
		Type:      core.EventPermissionRequest,
		RequestID: id,
		ToolName:  toolName,
		ToolInput: input,
		SessionID: sessionID,
	})
}

func (s *sseSubscriber) isUserMessage(messageID string) bool {
	if messageID == "" {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.messageRoles[messageID] == "user"
}

func (s *sseSubscriber) sessionIDForMessage(messageID string) string {
	if messageID == "" {
		return ""
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.messageIDs[messageID]
}

func (s *sseSubscriber) emitResultOnce(sessionID string) {
	s.stateMu.Lock()
	if s.completed[sessionID] {
		s.stateMu.Unlock()
		return
	}
	s.completed[sessionID] = true
	s.stateMu.Unlock()
	s.emit(core.Event{Type: core.EventResult, SessionID: sessionID, Done: true})
}

func (s *sseSubscriber) resetCompletion(sessionID string) {
	s.stateMu.Lock()
	delete(s.completed, sessionID)
	s.stateMu.Unlock()
}

func (s *sseSubscriber) kindForPart(sessionID, messageID, partID, field string) string {
	if field == "reasoning" {
		return "reasoning"
	}
	if field == "text" {
		return "text"
	}
	key := partCacheKey(sessionID, messageID, partID, "")
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.partKinds[key]
}

func (s *sseSubscriber) rememberPartKind(sessionID, messageID, partID, kind string) {
	if kind == "" {
		return
	}
	key := partCacheKey(sessionID, messageID, partID, "")
	s.stateMu.Lock()
	s.partKinds[key] = kind
	s.stateMu.Unlock()
}

func (s *sseSubscriber) appendPartContent(sessionID, messageID, partID, kind, delta string) {
	key := partCacheKey(sessionID, messageID, partID, kind)
	s.stateMu.Lock()
	s.partContent[key] += delta
	if kind != "" {
		s.partKinds[partCacheKey(sessionID, messageID, partID, "")] = kind
	}
	s.stateMu.Unlock()
}

type partDelta struct {
	content    string
	isComplete bool
}

func (s *sseSubscriber) deltaForPartSnapshot(sessionID, messageID, partID, kind, text string) partDelta {
	if text == "" {
		return partDelta{}
	}
	key := partCacheKey(sessionID, messageID, partID, kind)
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	previous := s.partContent[key]
	s.partContent[key] = text
	if kind != "" {
		s.partKinds[partCacheKey(sessionID, messageID, partID, "")] = kind
	}
	if previous == "" {
		return partDelta{content: text}
	}
	if strings.HasPrefix(text, previous) {
		return partDelta{content: strings.TrimPrefix(text, previous)}
	}
	if text == previous {
		return partDelta{}
	}
	return partDelta{content: text, isComplete: true}
}

func partCacheKey(sessionID, messageID, partID, kind string) string {
	key := sessionID + "\x00" + messageID + "\x00" + partID
	if kind != "" {
		key += "\x00" + kind
	}
	return key
}

func isServerEventType(eventType string) bool {
	switch eventType {
	case "message.updated", "message.part.delta", "message.part.updated", "session.status", "session.updated", "todo.updated", "permission.asked",
		"server.connected", "session.created", "session.deleted", "message.removed", "message.part.removed", "session.diff":
		return true
	default:
		return false
	}
}

func extractSSESessionID(properties map[string]any) string {
	if properties == nil {
		return ""
	}
	if sid := firstString(properties, "sessionID", "sessionId"); sid != "" {
		return sid
	}
	for _, key := range []string{"info", "session", "message", "status"} {
		if sid := extractSSESessionID(firstMap(properties, key)); sid != "" {
			return sid
		}
	}
	return ""
}

func extractMessageParts(info map[string]any) []map[string]any {
	if info == nil {
		return nil
	}
	var result []map[string]any
	for _, key := range []string{"parts", "content"} {
		items, _ := info[key].([]any)
		for _, item := range items {
			if part, _ := item.(map[string]any); part != nil {
				result = append(result, part)
			}
		}
	}
	if part := firstMap(info, "part"); part != nil {
		result = append(result, part)
	}
	return result
}

func isCompletedMessage(info map[string]any) bool {
	if info == nil {
		return false
	}
	timeInfo := firstMap(info, "time")
	if timeInfo == nil {
		return false
	}
	switch v := timeInfo["completed"].(type) {
	case float64:
		return v > 0
	case string:
		return v != ""
	default:
		return v != nil
	}
}

func firstMap(raw map[string]any, keys ...string) map[string]any {
	if raw == nil {
		return nil
	}
	for _, key := range keys {
		if value, _ := raw[key].(map[string]any); value != nil {
			return value
		}
	}
	return nil
}

func firstString(raw map[string]any, keys ...string) string {
	if raw == nil {
		return ""
	}
	for _, key := range keys {
		if value, _ := raw[key].(string); value != "" {
			return value
		}
	}
	return ""
}

func (s *sseSubscriber) emit(ev core.Event) {
	// Active-mode filter: when filterActive, only forward events whose
	// SessionID exactly matches sessionFilter. sessionFilter == "" means
	// "pending" (chatID not yet known) — drop everything so no other session's
	// events leak to the active consumer before the filter is set.
	if s.filterActive.Load() {
		f, _ := s.sessionFilter.Load().(string)
		if f == "" || ev.SessionID != f {
			return
		}
	}
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	default:
		slog.Debug("opencode SSE subscriber: event dropped", "type", ev.Type)
	}
}

// setSessionFilter locks the subscriber to a single sessionID (active mode).
// Subsequent emit calls drop events whose SessionID != id. Used by
// opencodeServerSession once the server-side session id is known. No-op if id
// is empty (leaves the pending "" filter in place).
func (s *sseSubscriber) setSessionFilter(id string) {
	if id == "" {
		return
	}
	s.sessionFilter.Store(id)
}

// newFilteredSSESubscriber creates a dedicated subscriber for one active
// session. filterActive is true from construction so nothing leaks; if
// sessionID is empty the filter is "pending" (drops all) until the caller
// resolves the id and calls setSessionFilter.
func newFilteredSSESubscriber(ctx context.Context, a *Agent, sessionID string) *sseSubscriber {
	sub := newSSESubscriber(ctx, a)
	sub.sessionFilter.Store(sessionID)
	sub.filterActive.Store(true)
	return sub
}

// Close 关闭 SSE 连接和事件 channel。
//
// INVARIANT: events is closed only after the producer (readLoop, tracked by
// wg) has exited. The timeout branch never closes events directly — a producer
// still running would panic on send (emit()'s default branch does NOT prevent a
// closed-channel send panic). On timeout we defer the close to a goroutine that
// waits for done.
func (s *sseSubscriber) Close() error {
	s.cancel()
	s.closeOnce.Do(func() {
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			close(s.events)
		case <-time.After(3 * time.Second):
			slog.Debug("opencode SSE subscriber: close timeout, deferring events close until readLoop exits")
			go func() {
				<-done
				close(s.events)
			}()
		}
	})
	return nil
}

// Subscribe 实现 core.EventSubscriber，让 go-bridge 的 startPassiveSubscription 可以使用。
// Agent 必须配置了 HTTP server URL（opencode serve 或 opencode web 启动的 HTTP API）。
func (a *Agent) Subscribe(ctx context.Context) (<-chan core.Event, error) {
	a.mu.RLock()
	baseURL := a.httpBaseURL
	a.mu.RUnlock()

	if baseURL == "" {
		return nil, fmt.Errorf("opencode: SSE subscription requires HTTP server URL (opencode serve)")
	}

	sub := newSSESubscriber(ctx, a)
	if err := sub.connect(); err != nil {
		return nil, err
	}

	go func() {
		<-sub.ctx.Done()
		_ = sub.Close()
	}()

	return sub.events, nil
}
