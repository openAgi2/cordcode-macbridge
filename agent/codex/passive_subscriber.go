package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

type passiveSubscriber struct {
	url string

	events chan core.Event
	ctx    context.Context
	cancel context.CancelFunc

	conn   *websocket.Conn
	nextID atomic.Int64
	mu     sync.Mutex

	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponseEnvelope

	sawDelta sync.Map // key: threadID:itemID (string), value: bool

	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newPassiveSubscriber(ctx context.Context, a *Agent) *passiveSubscriber {
	a.mu.RLock()
	url := a.appServerURL
	a.mu.RUnlock()

	subCtx, cancel := context.WithCancel(ctx)
	return &passiveSubscriber{
		url:     strings.TrimSpace(url),
		events:  make(chan core.Event, 128),
		ctx:     subCtx,
		cancel:  cancel,
		pending: make(map[int64]chan rpcResponseEnvelope),
	}
}

func (s *passiveSubscriber) connect() error {
	if s.url == "" {
		return fmt.Errorf("codex: passive subscription requires app-server URL (no shared service to connect to)")
	}

	wsURL := s.url
	if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	} else if strings.HasPrefix(wsURL, "https://") {
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.DialContext(s.ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("codex passive subscriber dial %s: %w", wsURL, err)
	}
	s.conn = conn

	slog.Info("codex passive subscriber connected", "url", wsURL)

	s.wg.Add(1)
	go s.readLoop()

	if err := s.initialize(); err != nil {
		_ = s.Close()
		return fmt.Errorf("codex passive subscriber initialize: %w", err)
	}

	return nil
}

func (s *passiveSubscriber) initialize() error {
	var resp initResponse
	if err := s.request("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "cc-connect-passive",
			"title":   "CC Connect Passive Subscriber",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
			"optOutNotificationMethods": []string{
				"command/exec/outputDelta",
				"item/plan/delta",
				"item/fileChange/outputDelta",
				"item/reasoning/summaryTextDelta",
			},
		},
	}, &resp); err != nil {
		return err
	}

	return s.notify("initialized", nil)
}

func (s *passiveSubscriber) request(method string, params any, out any) error {
	id := s.nextID.Add(1)
	ch := make(chan rpcResponseEnvelope, 1)

	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := s.writeJSON(payload); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("codex passive subscriber %s: %s", method, strings.TrimSpace(resp.Error.Message))
		}
		if out != nil {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-time.After(15 * time.Second):
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return fmt.Errorf("codex passive subscriber %s timed out", method)
	}
}

func (s *passiveSubscriber) notify(method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	return s.writeJSON(payload)
}

func (s *passiveSubscriber) readLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if s.ctx.Err() == nil {
				// 连接断开，取消 context 触发 Subscribe 的清理 goroutine
				// 调用 Close() 关闭 events channel，让 bridge 重连循环继续
				s.cancel()
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					slog.Debug("codex passive subscriber: server closed connection")
				} else {
					slog.Debug("codex passive subscriber read error", "error", err)
				}
			}
			return
		}

		var probe map[string]json.RawMessage
		if err := json.Unmarshal(data, &probe); err != nil {
			continue
		}

		if _, ok := probe["id"]; ok {
			var resp rpcResponseEnvelope
			if err := json.Unmarshal(data, &resp); err != nil {
				continue
			}
			s.handleResponse(resp)
			continue
		}

		var notif rpcNotificationEnvelope
		if err := json.Unmarshal(data, &notif); err != nil {
			continue
		}
		s.handleNotification(notif.Method, notif.Params)
	}
}

func (s *passiveSubscriber) handleResponse(resp rpcResponseEnvelope) {
	id, ok := rpcIDToInt64(resp.ID)
	if !ok {
		return
	}

	s.pendingMu.Lock()
	ch := s.pending[id]
	delete(s.pending, id)
	s.pendingMu.Unlock()

	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func (s *passiveSubscriber) handleNotification(method string, paramsRaw json.RawMessage) {
	switch method {
	case "turn/started":
		var notif turnNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.clearSawDeltaByThread(notif.ThreadID)
			threadID := strings.TrimSpace(notif.ThreadID)
			if threadID != "" {
				s.emit(core.Event{Type: core.EventTurnStarted, SessionID: threadID})
			}
		}
	case "item/started":
		var notif itemNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.handleItemStarted(notif.Item, notif.ThreadID)
		}

	case "item/completed":
		var notif itemNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.handleItemCompleted(notif.Item, notif.ThreadID)
		}

	case "event_msg":
		s.handleEventMessage(paramsRaw)

	case "patch_apply_end":
		s.handlePatchApplyEnd(paramsRaw, "")

	case "item/agentMessage/delta":
		s.handlePassiveAgentMessageDelta(paramsRaw)

	case "item/reasoning/textDelta":
		s.handlePassiveReasoningTextDelta(paramsRaw)

	case "turn/plan/updated":
		var notif planNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.emit(core.Event{
				Type:      core.EventPlan,
				SessionID: strings.TrimSpace(notif.ThreadID),
				Plan:      codexPlanEntriesToTodos(notif.Plan),
			})
		}

	case "turn/completed":
		var notif turnNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.clearSawDeltaByThread(notif.ThreadID)
			s.emit(core.Event{
				Type:      core.EventResult,
				SessionID: strings.TrimSpace(notif.ThreadID),
				Done:      true,
			})
		}

	case "error":
		var notif errorNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil && strings.TrimSpace(notif.Message) != "" {
			s.emitError(fmt.Errorf("%s", notif.Message))
		}
	}
}

func (s *passiveSubscriber) handleItemStarted(item map[string]any, threadID string) {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "agentMessage", "reasoning", "userMessage", "plan", "hookPrompt":
		return
	case "contextCompaction":
		s.emit(core.Event{Type: core.EventContextCompressing, SessionID: threadID})
		return
	}

	itemID, _ := item["id"].(string)

	switch itemType {
	case "commandExecution":
		command, _ := item["command"].(string)
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "Bash", ToolInput: command, SessionID: threadID, RequestID: itemID})
	case "mcpToolCall":
		server, _ := item["server"].(string)
		tool, _ := item["tool"].(string)
		name := strings.Trim(strings.Join([]string{server, tool}, ":"), ":")
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "MCP", ToolInput: name, SessionID: threadID, RequestID: itemID})
	case "webSearch":
		query, _ := item["query"].(string)
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "WebSearch", ToolInput: query, SessionID: threadID, RequestID: itemID})
	case "dynamicToolCall":
		tool, _ := item["tool"].(string)
		s.emit(core.Event{Type: core.EventToolUse, ToolName: tool, ToolInput: appServerJSON(item["arguments"]), SessionID: threadID, RequestID: itemID})
	case "fileChange":
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "Patch", ToolInput: appServerJSON(item["changes"]), SessionID: threadID, RequestID: itemID})
	case "customToolCall", "custom_tool_call":
		name := strings.TrimSpace(appServerStringValue(item["name"]))
		if name == "" {
			name = strings.TrimSpace(appServerStringValue(item["tool"]))
		}
		if name == "apply_patch" {
			s.emit(core.Event{Type: core.EventToolUse, ToolName: "Patch", ToolInput: appServerStringValue(item["input"]), SessionID: threadID, RequestID: itemID})
		}
	}
}

func (s *passiveSubscriber) handleItemCompleted(item map[string]any, threadID string) {
	itemType, _ := item["type"].(string)
	itemID, _ := item["id"].(string)
	switch itemType {
	case "reasoning":
		if itemID != "" {
			if _, saw := s.sawDelta.Load(threadID + ":" + itemID); saw {
				return
			}
		}
		text := appServerReasoningText(item)
		if text != "" {
			s.emit(core.Event{Type: core.EventThinking, Content: text, SessionID: threadID})
		}
	case "agentMessage":
		if itemID != "" {
			if _, saw := s.sawDelta.Load(threadID + ":" + itemID); saw {
				return
			}
		}
		text, _ := item["text"].(string)
		if text != "" {
			s.emit(core.Event{Type: core.EventText, Content: text, SessionID: threadID})
		}
	case "fileChange":
		changes := appServerFileChanges(item["changes"])
		s.emit(core.Event{
			Type:        core.EventToolResult,
			ToolName:    "Patch",
			ToolResult:  appServerFileChangeResult(changes),
			ToolStatus:  strings.TrimSpace(appServerStringValue(item["status"])),
			ToolSuccess: ptrBool(true),
			SessionID:   threadID,
			FileChanges: changes,
			RequestID:   itemID,
		})
	case "customToolCall", "custom_tool_call":
		name := strings.TrimSpace(appServerStringValue(item["name"]))
		if name == "" {
			name = strings.TrimSpace(appServerStringValue(item["tool"]))
		}
		if name == "apply_patch" {
			changes := appServerPatchChanges(item["changes"])
			if len(changes) > 0 {
				s.emit(core.Event{
					Type:        core.EventToolResult,
					ToolName:    "Patch",
					ToolResult:  appServerFileChangeResult(changes),
					ToolStatus:  strings.TrimSpace(appServerStringValue(item["status"])),
					ToolSuccess: ptrBool(true),
					SessionID:   threadID,
					FileChanges: changes,
					RequestID:   itemID,
				})
			}
		}
	case "commandExecution", "mcpToolCall", "webSearch", "dynamicToolCall":
		output, _ := item["output"].(string)
		toolName := itemType
		if t, ok := item["tool"].(string); ok && t != "" {
			toolName = t
		}
		exitCode := 0
		if code, ok := item["exitCode"].(float64); ok && code != 0 {
			exitCode = int(code)
		}
		s.emit(core.Event{Type: core.EventToolResult, ToolName: toolName, ToolResult: output, SessionID: threadID, ToolExitCode: &exitCode, RequestID: itemID})
	}
}

func (s *passiveSubscriber) handleEventMessage(paramsRaw json.RawMessage) {
	var envelope map[string]any
	if err := json.Unmarshal(paramsRaw, &envelope); err != nil {
		return
	}
	threadID := strings.TrimSpace(appServerStringValue(envelope["threadId"]))
	if threadID == "" {
		threadID = strings.TrimSpace(appServerStringValue(envelope["thread_id"]))
	}
	payload := envelope
	if nested, ok := envelope["payload"].(map[string]any); ok {
		payload = nested
	}
	if strings.TrimSpace(appServerStringValue(payload["type"])) == "patch_apply_end" {
		if threadID == "" {
			threadID = strings.TrimSpace(appServerStringValue(payload["threadId"]))
		}
		s.handlePatchApplyEndMap(payload, threadID)
	}
}

func (s *passiveSubscriber) handlePatchApplyEnd(paramsRaw json.RawMessage, threadID string) {
	var payload map[string]any
	if err := json.Unmarshal(paramsRaw, &payload); err != nil {
		return
	}
	s.handlePatchApplyEndMap(payload, threadID)
}

func (s *passiveSubscriber) handlePatchApplyEndMap(payload map[string]any, threadID string) {
	if threadID == "" {
		threadID = strings.TrimSpace(appServerStringValue(payload["threadId"]))
	}
	if threadID == "" {
		threadID = strings.TrimSpace(appServerStringValue(payload["thread_id"]))
	}
	changes := appServerPatchChanges(payload["changes"])
	if len(changes) == 0 {
		return
	}
	status := strings.TrimSpace(appServerStringValue(payload["status"]))
	if status == "" {
		status = "completed"
	}
	success := status == "completed" || payload["success"] == true
	s.emit(core.Event{
		Type:        core.EventToolResult,
		ToolName:    "Patch",
		ToolResult:  appServerFileChangeResult(changes),
		ToolStatus:  status,
		ToolSuccess: &success,
		SessionID:   threadID,
		FileChanges: changes,
	})
}

func (s *passiveSubscriber) handlePassiveAgentMessageDelta(paramsRaw json.RawMessage) {
	var notif struct {
		Delta    string `json:"delta"`
		ItemID   string `json:"itemId"`
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if err := json.Unmarshal(paramsRaw, &notif); err != nil {
		return
	}
	if notif.Delta == "" {
		return
	}
	if notif.ItemID != "" {
		s.sawDelta.Store(notif.ThreadID+":"+notif.ItemID, true)
	}
	s.emit(core.Event{Type: core.EventText, Content: notif.Delta, SessionID: strings.TrimSpace(notif.ThreadID)})
}

func (s *passiveSubscriber) handlePassiveReasoningTextDelta(paramsRaw json.RawMessage) {
	var notif struct {
		Delta        string `json:"delta"`
		ItemID       string `json:"itemId"`
		ThreadID     string `json:"threadId"`
		TurnID       string `json:"turnId"`
		ContentIndex int    `json:"contentIndex"`
	}
	if err := json.Unmarshal(paramsRaw, &notif); err != nil {
		return
	}
	if notif.Delta == "" {
		return
	}
	if notif.ItemID != "" {
		s.sawDelta.Store(notif.ThreadID+":"+notif.ItemID, true)
	}
	s.emit(core.Event{Type: core.EventThinking, Content: notif.Delta, SessionID: strings.TrimSpace(notif.ThreadID)})
}

func (s *passiveSubscriber) emit(ev core.Event) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	default:
		slog.Debug("codex passive subscriber: event dropped", "type", ev.Type)
	}
}

func (s *passiveSubscriber) clearSawDeltaByThread(threadID string) {
	prefix := threadID + ":"
	s.sawDelta.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			s.sawDelta.Delete(k)
		}
		return true
	})
}

func (s *passiveSubscriber) emitError(err error) {
	s.emit(core.Event{Type: core.EventError, Error: err})
}

func (s *passiveSubscriber) writeJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return fmt.Errorf("connection closed")
	}
	return s.conn.WriteJSON(v)
}

func (s *passiveSubscriber) Close() error {
	s.cancel()
	// Tear down the connection once (inside closeOnce). The events channel is
	// closed ONLY after the producer (wg) exits — never from the timeout
	// branch — so a still-running producer can't panic on a closed-channel send.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.closeOnce.Do(func() {
			s.closeConnAndEvents()
		})
	case <-time.After(3 * time.Second):
		slog.Debug("codex passive subscriber: close timeout, deferring events close until readLoop exits")
		s.closeOnce.Do(func() {
			// Close the conn now to unblock the producer, but defer events
			// close until wg actually exits.
			s.closeConnOnly()
			go func() {
				<-done
				close(s.events)
			}()
		})
	}
	return nil
}

// closeConnOnly sends a close frame and tears down the underlying conn without
// touching the events channel.
func (s *passiveSubscriber) closeConnOnly() {
	if s.conn != nil {
		_ = s.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(2*time.Second),
		)
		_ = s.conn.Close()
	}
}

// closeConnAndEvents closes the conn then the events channel (producer already
// exited, so closing events is safe).
func (s *passiveSubscriber) closeConnAndEvents() {
	s.closeConnOnly()
	close(s.events)
}

// Subscribe implements core.EventSubscriber for the Codex agent.
// It connects to the shared Codex app-server as a WebSocket client,
// not by launching a competing app-server process.
func (a *Agent) Subscribe(ctx context.Context) (<-chan core.Event, error) {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()

	if backend != "app_server" {
		return nil, fmt.Errorf("codex: passive subscription only supported in app_server mode")
	}

	sub := newPassiveSubscriber(ctx, a)
	if err := sub.connect(); err != nil {
		return nil, err
	}

	go func() {
		<-sub.ctx.Done()
		_ = sub.Close()
	}()

	return sub.events, nil
}
