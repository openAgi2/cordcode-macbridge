package opencodeweb

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

// events.go is the常驻 SSE subscriber (design §4.3.3): /global/event on 1.18,
// /api/event on v2. It is copied from the legacy subscriber (design §7 兜底 —
// the turn lifecycle translation is battle-tested) and owned by this package,
// with the design-mandated deltas:
//
//   - session.updated recomputes occupancy via the official §3.3 message-level
//     formula (never top-level tokens);
//   - todo.updated is IGNORED in phase 1 (todos not advertised);
//   - session.created / session.deleted feed CatalogRefreshSignaler instead of
//     the chat stream;
//   - no CLI NDJSON event handlers — this package only talks to the server
//     face (the payload wrapper unwrap stays, top-level server types stay).
type sseSubscriber struct {
	agent  *Agent
	client *Client

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
	activeTurns  map[string]string // sessionID -> owning user/message turn id
	// userPrompts accumulates the live user prompt text for a message id so a
	// bare message.updated (role=user, no parts) plus later part deltas still
	// become one projection user_message. userTurnStarted de-dupes turn_started.
	userPrompts     map[string]string
	userTurnStarted map[string]bool
	// turnSawAssistantOutput tracks whether a turn produced any assistant
	// content. The serve closes a provider-resolution failure (e.g. stale
	// default model) as a silent empty loop exit — no error event, no
	// assistant message (2026-08-14: 81ms empty-turn lesson). At session idle
	// an armed turn with no output emits EventResult with Error (wire
	// turn_error) instead of a healthy empty turn_completed.
	turnSawAssistantOutput map[string]bool // turnID -> any assistant output seen
	// lastSessionError records the serve's TRANSIENT retry text (candidate
	// diagnosis only — a retried turn that later succeeds stays clean).
	lastSessionError map[string]string
	// lastTerminalError records the serve's EXPLICIT failure text
	// (session.error frame / assistant info.error). Such a turn settles as
	// turn_error with the serve's text AND the text is emitted as assistant
	// content — the projection reducer drops turn_error.message, so content
	// is the only carrier iOS renders (owner-verified 2026-08-19 twice: the
	// wire event carried the text, iOS showed nothing).
	lastTerminalError map[string]string

	// Active-mode filter (§8-4 session binding): filterActive drops events
	// whose SessionID != sessionFilter. Empty filter = pending (drops all).
	filterActive  atomic.Bool
	sessionFilter atomic.Value // string
}

func newSSESubscriber(ctx context.Context, a *Agent, c *Client) *sseSubscriber {
	subCtx, cancel := context.WithCancel(ctx)
	return &sseSubscriber{
		agent:                  a,
		client:                 c,
		events:                 make(chan core.Event, 128),
		ctx:                    subCtx,
		cancel:                 cancel,
		messageRoles:           make(map[string]string),
		messageIDs:             make(map[string]string),
		partKinds:              make(map[string]string),
		partContent:            make(map[string]string),
		completed:              make(map[string]bool),
		activeTurns:            make(map[string]string),
		userPrompts:            make(map[string]string),
		userTurnStarted:        make(map[string]bool),
		turnSawAssistantOutput: make(map[string]bool),
		lastSessionError:       make(map[string]string),
		lastTerminalError:      make(map[string]string),
	}
}

// sseReconnect backoff bounds.
const (
	sseReconnectMinBackoff = time.Second
	sseReconnectMaxBackoff = 15 * time.Second
)

// connect performs the FIRST dial synchronously (callers surface the error),
// then hands the body to run() which owns mid-life reconnects.
func (s *sseSubscriber) connect() error {
	resp, err := s.dial()
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go s.run(resp.Body)
	return nil
}

// dial opens one SSE response. The stream rides streamClient (NO timeout —
// http.Client.Timeout covers reading the response body, so any finite value
// kills the stream mid-turn; owner-verified 2026-08-19: turn 2 died at the
// 30s mark mid-stream).
func (s *sseSubscriber) dial() (*http.Response, error) {
	eventPath := "/global/event"
	if s.client.Generation() == generationV2 {
		eventPath = "/api/event"
	}
	sseURL := s.client.endpoint(eventPath)

	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("opencode-web SSE subscriber: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if s.client.authHeader != "" {
		req.Header.Set("Authorization", s.client.authHeader)
	}

	resp, err := s.client.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode-web SSE subscriber connect %s: %w", sseURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("opencode-web SSE subscriber: HTTP %d", resp.StatusCode)
	}
	slog.Info("opencode-web SSE subscriber connected", "url", sseURL, "generation", string(s.client.Generation()))
	return resp, nil
}

// run owns the stream lifecycle: read → on drop (body ends while ctx is
// alive) heal armed turns, backoff, redial. A dropped stream otherwise
// leaves iOS stuck in 执行中 forever — the terminal session-idle event dies
// with the connection.
func (s *sseSubscriber) run(body io.ReadCloser) {
	defer s.wg.Done()
	backoff := sseReconnectMinBackoff
	for {
		s.readStream(body)
		if s.ctx.Err() != nil {
			return
		}
		slog.Warn("opencode-web SSE: stream dropped mid-flight, healing + reconnecting")
		s.healArmedTurnsAfterDrop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < sseReconnectMaxBackoff {
				backoff *= 2
			}
			resp, err := s.dial()
			if err == nil {
				body = resp.Body
				break
			}
			if s.ctx.Err() != nil {
				return
			}
			slog.Debug("opencode-web SSE: reconnect attempt failed", "error", err)
		}
		backoff = sseReconnectMinBackoff
	}
}

// healArmedTurnsAfterDrop settles turns that armed before a stream drop and
// went idle during the gap (their terminal event was lost): 1.18
// GET /session/status is the definitive busy map, so any armed session no
// longer busy gets its one-shot result now. v2's /api/session/active has
// foreground-drain-only semantics — absence is not an idle verdict, so v2
// stays conservative (no heal).
func (s *sseSubscriber) healArmedTurnsAfterDrop() {
	if s.client.Generation() == generationV2 {
		return
	}
	s.stateMu.Lock()
	armed := make([]string, 0, len(s.activeTurns))
	for sessionID := range s.activeTurns {
		armed = append(armed, sessionID)
	}
	s.stateMu.Unlock()
	if len(armed) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := s.client.fetchJSON(ctx, "/session/status", s.agent.GetWorkDir())
	if err != nil {
		slog.Debug("opencode-web SSE: post-drop status heal failed", "error", err)
		return
	}
	var busy map[string]struct{}
	if err := decodeJSONObject(raw, &busy); err != nil {
		return
	}
	for _, sessionID := range armed {
		if _, isActive := busy[sessionID]; !isActive {
			slog.Info("opencode-web SSE: settling turn that went idle during stream gap", "session", sessionID)
			s.emitResultOnce(sessionID)
		}
	}
}

// readStream reads one SSE connection to its end (data: lines, blank-line
// separators); a bare JSON line is tolerated as NDJSON compat.
func (s *sseSubscriber) readStream(body io.ReadCloser) {
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var currentData strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			data := currentData.String()
			currentData.Reset()
			if data == "" {
				continue
			}
			s.handleRawEvent(strings.TrimSpace(data))
			continue
		}
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
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") || strings.HasPrefix(line, ":") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{") {
			s.handleRawEvent(trimmed)
		}
	}
	if err := scanner.Err(); err != nil && s.ctx.Err() == nil {
		slog.Debug("opencode-web SSE subscriber read error", "error", err)
	}
}

// handleRawEvent unwraps the server payload envelope (design §3.6):
//
//	{"payload":{"type":"message.part.delta","properties":{…}}}
//
// and also accepts a top-level server type (CLI NDJSON shape).
func (s *sseSubscriber) handleRawEvent(data string) {
	data = strings.TrimSpace(data)
	if data == "" {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		slog.Debug("opencode-web SSE: non-JSON data", "data", truncateForError(data))
		return
	}
	if payload, _ := raw["payload"].(map[string]any); payload != nil {
		s.handleServerEvent(payload)
		return
	}
	if eventType, _ := raw["type"].(string); isServerEventType(eventType) {
		s.handleServerEvent(raw)
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
		s.handleMessageUpdated(properties, sessionID)
	case "message.part.delta":
		s.handlePartDelta(properties, sessionID)
	case "message.part.updated":
		s.handlePartUpdated(properties, sessionID)
	case "session.status":
		s.handleSessionStatus(properties, sessionID)
	case "session.error":
		// Terminal failure frame (live-pinned 1.18.18: provider APIError with
		// the real message). Recorded as terminal AND emitted as assistant
		// content — the projection reducer drops turn_error.message, so the
		// text part is what iOS actually renders (same as the web timeline).
		// The emission gate is agent-level: both subscribers decode this frame
		// and per-subscriber state would emit the text once per connection.
		if msg := extractSessionErrorMessage(properties); msg != "" {
			s.noteSessionError(sessionID, msg)
			s.noteTerminalSessionError(sessionID, msg)
			if s.agent.claimTerminalText(sessionID) {
				turnID := s.owningTurnID(sessionID, "")
				s.emit(core.Event{
					Type:      core.EventText,
					Content:   msg,
					SessionID: sessionID,
					TurnID:    turnID,
					ItemID:    turnID,
				})
			}
		}
	case "session.idle":
		// 1.18.18 emits both session.status idle AND session.idle; treat the
		// latter as the same one-shot terminal (emitResultOnce de-dupes).
		if sessionID == "" {
			sessionID = firstString(properties, "sessionID", "sessionId")
		}
		if sessionID != "" {
			s.agent.clearRetrySnapshot(sessionID)
			s.emitResultOnce(sessionID)
		}
	case "session.updated":
		s.handleSessionUpdated(properties, sessionID)
	case "permission.asked":
		s.handlePermissionAsked(properties, sessionID)
	case "todo.updated":
		// Phase 1: todos are not advertised (design §4.3.3) — deliberately
		// ignored, no EventPlan.
	case "server.connected", "message.removed", "message.part.removed", "session.diff":
		// Not chat content; not catalog-affecting either.
	case "session.created", "session.deleted":
		// Catalog-affecting: ask the bridge for an immediate fingerprint
		// rescan → sessions_changed. Never enters the chat stream.
		s.agent.signalCatalogRefresh()
	default:
		slog.Debug("opencode-web SSE: unhandled server event", "type", eventType)
	}
}

func (s *sseSubscriber) handleMessageUpdated(properties map[string]any, sessionID string) {
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
		if messageID != "" {
			s.setActiveTurn(sessionID, messageID)
			userText := ""
			for _, part := range extractMessageParts(info) {
				if firstString(part, "type") == "text" {
					if chunk := firstString(part, "text", "content", "initial"); chunk != "" {
						if userText != "" {
							userText += "\n"
						}
						userText += chunk
					}
				}
			}
			if strings.TrimSpace(userText) != "" {
				s.noteUserPrompt(sessionID, messageID, userText, false)
			}
		}
	}
	if sessionID == "" || role != "assistant" {
		return
	}
	// A failed assistant message carries info.error (live-pinned 1.18.18) —
	// terminal: record and surface the text as content. The emission gate is
	// the agent-level claim: this frame trails session.error by ~100ms and
	// per-subscriber state was already consumed by emitResultOnce, which used
	// to re-arm the text as "first" and emit a duplicate (2026-08-19 live log:
	// 套餐 error rendered 3× on iOS).
	if errMsg := extractSessionErrorMessage(info); errMsg != "" {
		s.noteSessionError(sessionID, errMsg)
		s.noteTerminalSessionError(sessionID, errMsg)
		if s.agent.claimTerminalText(sessionID) {
			turnID := s.owningTurnID(sessionID, messageID)
			s.emit(core.Event{
				Type:      core.EventText,
				Content:   errMsg,
				SessionID: sessionID,
				TurnID:    turnID,
				ItemID:    turnID,
			})
		}
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
				turnID := s.owningTurnID(sessionID, messageID)
				s.emit(core.Event{Type: eventType, Content: d.content, SessionID: sessionID, TurnID: turnID, ItemID: turnID})
			}
		case "reasoning":
			text := firstString(part, "text", "content")
			if d := s.deltaForPartSnapshot(sessionID, messageID, partID, kind, text); d.content != "" {
				turnID := s.owningTurnID(sessionID, messageID)
				s.emit(core.Event{Type: core.EventThinking, Content: d.content, SessionID: sessionID, TurnID: turnID, ItemID: turnID})
			}
		case "tool":
			s.handleToolPart(part, sessionID, messageID)
		}
	}
	// Intentionally no result on assistant time.completed: multi-step turns
	// mark each tool-bearing assistant message completed before the next step
	// runs. Turn completion is owned solely by session.status/session.updated
	// idle (design §4.3.3 红线).
}

func (s *sseSubscriber) handlePartDelta(properties map[string]any, sessionID string) {
	messageID := firstString(properties, "messageID", "messageId")
	if sessionID == "" {
		sessionID = s.sessionIDForMessage(messageID)
	}
	field := firstString(properties, "field")
	delta := firstString(properties, "delta")
	if delta == "" {
		return
	}
	// User prompt text often arrives ONLY as part.delta after a bare
	// message.updated; dropping it leaves turns with no user bubble.
	if s.isUserMessage(messageID) {
		if field == "" || field == "text" {
			s.noteUserPrompt(sessionID, messageID, delta, true)
		}
		return
	}
	partID := firstString(properties, "partID", "partId")
	kind := s.kindForPart(sessionID, messageID, partID, field)
	switch kind {
	case "reasoning":
		s.appendPartContent(sessionID, messageID, partID, kind, delta)
		turnID := s.owningTurnID(sessionID, messageID)
		s.emit(core.Event{Type: core.EventThinking, Content: delta, SessionID: sessionID, TurnID: turnID, ItemID: turnID})
	case "text", "":
		s.appendPartContent(sessionID, messageID, partID, "text", delta)
		turnID := s.owningTurnID(sessionID, messageID)
		s.emit(core.Event{Type: core.EventText, Content: delta, SessionID: sessionID, TurnID: turnID, ItemID: turnID})
	default:
		slog.Debug("opencode-web SSE: ignored part delta", "kind", kind, "field", field)
	}
}

func (s *sseSubscriber) handlePartUpdated(properties map[string]any, sessionID string) {
	part := firstMap(properties, "part")
	if part == nil {
		return
	}
	messageID := firstString(properties, "messageID", "messageId")
	if messageID == "" {
		messageID = firstString(part, "messageID", "messageId")
	}
	if sessionID == "" {
		sessionID = firstString(part, "sessionID", "sessionId")
	}
	if sessionID == "" {
		sessionID = s.sessionIDForMessage(messageID)
	}
	if s.isUserMessage(messageID) {
		kind := firstString(part, "type")
		if kind == "" || kind == "text" {
			text := firstString(part, "text", "content", "initial")
			if strings.TrimSpace(text) != "" {
				s.noteUserPrompt(sessionID, messageID, text, false)
			}
		}
		return
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
			turnID := s.owningTurnID(sessionID, messageID)
			s.emit(core.Event{Type: eventType, Content: d.content, SessionID: sessionID, TurnID: turnID, ItemID: turnID})
		}
	case "reasoning":
		text := firstString(part, "text", "content")
		if d := s.deltaForPartSnapshot(sessionID, messageID, partID, kind, text); d.content != "" {
			turnID := s.owningTurnID(sessionID, messageID)
			s.emit(core.Event{Type: core.EventThinking, Content: d.content, SessionID: sessionID, TurnID: turnID, ItemID: turnID})
		}
	case "tool":
		s.handleToolPart(part, sessionID, messageID)
	default:
		slog.Debug("opencode-web SSE: ignored part update", "kind", kind)
	}
}

// handleToolPart emits the tool lifecycle. messageID feeds turn attribution:
// tool events carry the owning turn id so a tool-bearing step counts as
// assistant output — a healthy multi-step tool turn must NOT close as
// zero-output at idle (design §4.3.3; delta vs the legacy copy, which left
// tool events unattributed).
func (s *sseSubscriber) handleToolPart(part map[string]any, sessionID, messageID string) {
	toolName := firstString(part, "tool", "name")
	if toolName == "" {
		// Verified live shape (history mapping parity): the tool object carries
		// toolName; "name" and "id" stay as fallbacks.
		toolName = firstString(firstMap(part, "tool"), "toolName", "name", "id")
	}
	state := firstMap(part, "state")
	if state == nil {
		// Verified live shape (history mapping parity): state can nest inside
		// the tool object instead of sitting on the part.
		state = firstMap(firstMap(part, "tool"), "state")
	}
	status := firstString(state, "status")
	input := extractToolInput(state)
	if input == "" {
		input = firstString(part, "title")
	}
	partID := firstString(part, "id", "partID", "partId")
	turnID := s.owningTurnID(sessionID, messageID)

	s.emit(core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: input, SessionID: sessionID, RequestID: partID, TurnID: turnID, ItemID: turnID})
	if status == "completed" || status == "error" || status == "failed" {
		output := firstString(state, "output", "result")
		toolStatus := status
		if toolStatus == "error" {
			toolStatus = "failed"
		}
		s.emit(core.Event{
			Type:       core.EventToolResult,
			ToolName:   toolName,
			ToolResult: truncateForError(output),
			ToolStatus: toolStatus,
			SessionID:  sessionID,
			RequestID:  partID,
			TurnID:     turnID,
			ItemID:     turnID,
		})
	}
}

func (s *sseSubscriber) handleSessionStatus(properties map[string]any, sessionID string) {
	statusMap := firstMap(properties, "status")
	status := firstString(properties, "type")
	if status == "" {
		status = firstString(statusMap, "type", "status")
	}
	if sessionID == "" {
		sessionID = extractSSESessionID(statusMap)
	}
	if status == "retry" {
		// Transient: the serve retries with backoff and the turn continues
		// (official web renders a "Retrying automatically..." row with the
		// provider message). Record the message — it is the only early carrier
		// of the provider text — and forward the notice on the wire as
		// session_retry_status so clients can render the same transient row.
		// Live-pinned frame: properties.status = {type:"retry", attempt:N,
		// message, next:<epoch-ms>}.
		msg := firstString(statusMap, "message")
		if msg != "" {
			s.noteSessionError(sessionID, msg)
		}
		attempt := int(firstNumeric(statusMap, "attempt"))
		next := int64(firstNumeric(statusMap, "next"))
		// Re-attach replay tail: iOS 锁屏/后台会错过瞬态重试行（owner
		// 2026-08-19）——记录最新快照，StartSession 重附时新鲜则重放一次。
		s.agent.noteRetrySnapshot(sessionID, attempt, msg, next)
		if s.agent.claimRetryStatus(sessionID, attempt) {
			s.emit(core.Event{
				Type:         core.EventRetryStatus,
				Content:      msg,
				SessionID:    sessionID,
				RetryAttempt: attempt,
				RetryNext:    next,
			})
		}
	}
	if status == "running" && sessionID != "" {
		s.resetCompletion(sessionID)
	}
	if status == "idle" && sessionID != "" {
		s.agent.clearRetrySnapshot(sessionID)
		s.emitResultOnce(sessionID)
	}
}

// handleSessionUpdated: the idle/running transition carries the same
// turn-lifecycle semantics as session.status, plus the §3.3 occupancy
// recompute — always message-level, never top-level tokens.
func (s *sseSubscriber) handleSessionUpdated(properties map[string]any, sessionID string) {
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
	if sessionID != "" {
		// Async: the recompute fetches messages (and on a cold cache the ~5MB
		// provider JSON) — running it inline would stall the SSE read loop
		// mid-turn.
		go s.recomputeUsage(sessionID)
	}
}

func (s *sseSubscriber) recomputeUsage(sessionID string) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	messages, err := s.agent.fetchMessageMapsWithClient(ctx, s.client, sessionID)
	if err != nil {
		slog.Debug("opencode-web: usage recompute fetch failed", "session", sessionID, "error", err)
		return
	}
	usage := usageFromMessages(ctx, s.agent, s.client, messages)
	if usage == nil {
		return
	}
	s.agent.rememberContextUsage(sessionID, usage)
	s.emit(core.Event{
		Type:         core.EventContextUsageUpdated,
		SessionID:    sessionID,
		ContextUsage: usage,
	})
}

// handlePermissionAsked forwards the official permission.asked payload.
//
// Live-pinned 1.18.18 frame (permlab /global/event, 2026-08-19):
//
//	{id, sessionID, permission, patterns[], metadata{filepath,parentDir},
//	 always[], tool{messageID,callID}}
//
// The official desktop renders category text from `permission` (i18n key
// settings.permissions.tool.{permission}.description) plus one row per
// pattern, with reject/always/once buttons. The earlier fields this read
// (tool as a string, title/description) do not exist in the real frame.
func (s *sseSubscriber) handlePermissionAsked(properties map[string]any, sessionID string) {
	id := firstString(properties, "id", "permissionID", "permissionId")
	if sid := firstString(properties, "sessionID"); sid != "" {
		sessionID = sid
	}
	kind := firstString(properties, "permission")
	patterns := stringSlice(properties, "patterns")
	filePath := ""
	if meta := firstMap(properties, "metadata"); meta != nil {
		filePath = firstString(meta, "filepath")
	}
	s.emit(core.Event{
		Type:               core.EventPermissionRequest,
		RequestID:          id,
		ToolName:           kind,
		ToolInput:          filePath,
		SessionID:          sessionID,
		PermissionKind:     kind,
		PermissionPatterns: patterns,
	})
}

// noteUserPrompt records user prompt text into the live stream. isDelta=true
// appends; false replaces/grows from a snapshot. Emits EventUserMessage with
// the accumulated text and EventTurnStarted once per message id.
func (s *sseSubscriber) noteUserPrompt(sessionID, messageID, text string, isDelta bool) {
	text = strings.TrimRight(text, "\r")
	if sessionID == "" || messageID == "" || strings.TrimSpace(text) == "" {
		return
	}
	s.setActiveTurn(sessionID, messageID)

	s.stateMu.Lock()
	prev := s.userPrompts[messageID]
	full := text
	if isDelta {
		full = prev + text
	} else if prev != "" {
		if text == prev {
			s.stateMu.Unlock()
			return
		}
		if strings.HasPrefix(text, prev) {
			full = text
		} else if strings.HasPrefix(prev, text) {
			s.stateMu.Unlock()
			return
		}
	}
	s.userPrompts[messageID] = full
	startTurn := !s.userTurnStarted[messageID]
	if startTurn {
		s.userTurnStarted[messageID] = true
	}
	s.stateMu.Unlock()

	if startTurn {
		// New turn: re-arm the agent-level once-claims so a fresh failure in
		// this turn can surface its own terminal text / retry statuses.
		s.agent.clearTerminalOnceClaims(sessionID)
	}

	s.emit(core.Event{
		Type:      core.EventUserMessage,
		Content:   full,
		SessionID: sessionID,
		TurnID:    messageID,
		ItemID:    messageID,
	})
	if startTurn {
		s.emit(core.Event{
			Type:      core.EventTurnStarted,
			SessionID: sessionID,
			TurnID:    messageID,
		})
	}
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

// emitResultOnce is the ONLY turn terminal (design §4.3.3): session idle.
// A turn that armed but produced zero assistant output surfaces as
// EventResult with the diagnosable error text (wire turn_error) — never a
// healthy empty completion.
func (s *sseSubscriber) emitResultOnce(sessionID string) {
	s.stateMu.Lock()
	if s.completed[sessionID] {
		s.stateMu.Unlock()
		return
	}
	turnID := s.activeTurns[sessionID]
	terminal := s.lastTerminalError[sessionID]
	if turnID == "" && terminal == "" {
		// Fresh-session idle: POST /session makes the serve broadcast the new
		// session's initial session.updated/session.status idle, and that
		// creation broadcast races the first prompt_async through the same SSE
		// filter — it can arrive before the user echo arms a turn. A bare idle
		// with no armed turn and no terminal error completes nothing: emitting
		// EventResult here fakes a healthy turn terminal, which exits the
		// bridge relay (opencode-web does not survive turn boundaries) and
		// kills the live feed for the whole first turn (real device
		// 2026-08-20: input flips completed instantly, the reply lands as one
		// bulk patch seconds later). Leave `completed` unset so the real
		// turn-end idle still emits exactly once.
		s.stateMu.Unlock()
		return
	}
	s.completed[sessionID] = true
	hadOutput := turnID != "" && s.turnSawAssistantOutput[turnID]
	delete(s.turnSawAssistantOutput, turnID)
	delete(s.lastTerminalError, sessionID)
	s.stateMu.Unlock()
	if terminal != "" {
		// The serve explicitly failed the turn (session.error / info.error):
		// settle as turn_error even when partial output landed, carrying the
		// serve's text.
		s.emit(core.Event{
			Type:      core.EventResult,
			Error:     fmt.Errorf("%s", terminal),
			SessionID: sessionID,
			Done:      true,
			TurnID:    turnID,
		})
		s.clearActiveTurn(sessionID)
		return
	}
	if turnID != "" && !hadOutput {
		// Prefer the serve's own error text (session.error / retry.message /
		// assistant info.error) over the generic guess — the provider's
		// diagnosis must reach iOS verbatim.
		detail := s.takeSessionError(sessionID)
		if detail == "" {
			detail = "model produced no output (model or provider may be unavailable on the server)"
		}
		slog.Warn("opencode-web: turn closed with zero assistant output",
			"session_id", sessionID,
			"turn_id", turnID,
			"detail", detail)
		s.emit(core.Event{
			Type:      core.EventResult,
			Error:     fmt.Errorf("%s", detail),
			SessionID: sessionID,
			Done:      true,
			TurnID:    turnID,
		})
		s.clearActiveTurn(sessionID)
		return
	}
	s.emit(core.Event{Type: core.EventResult, SessionID: sessionID, Done: true, TurnID: turnID})
	s.clearActiveTurn(sessionID)
}

// noteSessionError records the serve's transient retry text (candidate).
func (s *sseSubscriber) noteSessionError(sessionID, message string) {
	if sessionID == "" || strings.TrimSpace(message) == "" {
		return
	}
	s.stateMu.Lock()
	if s.lastSessionError == nil {
		s.lastSessionError = make(map[string]string)
	}
	s.lastSessionError[sessionID] = message
	s.stateMu.Unlock()
}

// noteTerminalSessionError records the serve's explicit failure text
// (session.error / assistant info.error). Returns true when this is the
// FIRST terminal error for the turn (caller emits the text content exactly
// once).
func (s *sseSubscriber) noteTerminalSessionError(sessionID, message string) bool {
	if sessionID == "" || strings.TrimSpace(message) == "" {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.lastTerminalError == nil {
		s.lastTerminalError = make(map[string]string)
	}
	_, existed := s.lastTerminalError[sessionID]
	s.lastTerminalError[sessionID] = message
	return !existed
}

func (s *sseSubscriber) takeSessionError(sessionID string) string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	msg := s.lastSessionError[sessionID]
	delete(s.lastSessionError, sessionID)
	return msg
}

// extractSessionErrorMessage pulls properties.error.data.message (1.18.18
// session.error frame) with name/message fallbacks.
func extractSessionErrorMessage(properties map[string]any) string {
	err := firstMap(properties, "error")
	if err == nil {
		return ""
	}
	if data := firstMap(err, "data"); data != nil {
		if msg := firstString(data, "message"); msg != "" {
			return msg
		}
	}
	return firstString(err, "message")
}

func (s *sseSubscriber) resetCompletion(sessionID string) {
	s.stateMu.Lock()
	delete(s.completed, sessionID)
	s.stateMu.Unlock()
}

func (s *sseSubscriber) setActiveTurn(sessionID, turnID string) {
	if sessionID == "" || turnID == "" {
		return
	}
	s.stateMu.Lock()
	s.activeTurns[sessionID] = turnID
	delete(s.lastSessionError, sessionID)
	delete(s.lastTerminalError, sessionID)
	s.stateMu.Unlock()
}

func (s *sseSubscriber) activeTurn(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.activeTurns[sessionID]
}

func (s *sseSubscriber) clearActiveTurn(sessionID string) {
	if sessionID == "" {
		return
	}
	s.stateMu.Lock()
	delete(s.activeTurns, sessionID)
	s.stateMu.Unlock()
}

func (s *sseSubscriber) owningTurnID(sessionID, messageID string) string {
	if turnID := s.activeTurn(sessionID); turnID != "" {
		return turnID
	}
	return messageID
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
	case "message.updated", "message.part.delta", "message.part.updated", "session.status", "session.updated", "session.error", "session.idle", "todo.updated", "permission.asked",
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

// stringSlice reads a JSON string-array field; nil/absent yields nil.
func stringSlice(raw map[string]any, key string) []string {
	if raw == nil {
		return nil
	}
	values, _ := raw[key].([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, _ := value.(string); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// firstNumeric reads a JSON number field (float64 after decode) tolerantly.
func firstNumeric(raw map[string]any, key string) float64 {
	if raw == nil {
		return 0
	}
	switch typed := raw[key].(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return 0
}

func extractToolInput(state map[string]any) string {
	if state == nil {
		return ""
	}
	raw, _ := state["input"]
	switch typed := raw.(type) {
	case string:
		return truncateForError(typed)
	case map[string]any, []any:
		b, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return truncateForError(string(b))
	default:
		return ""
	}
}

func (s *sseSubscriber) emit(ev core.Event) {
	if s.filterActive.Load() {
		f, _ := s.sessionFilter.Load().(string)
		if f == "" || ev.SessionID != f {
			return
		}
	}
	switch ev.Type {
	case core.EventText, core.EventTextReplace, core.EventThinking, core.EventToolUse, core.EventToolResult:
		if ev.TurnID != "" {
			s.stateMu.Lock()
			s.turnSawAssistantOutput[ev.TurnID] = true
			s.stateMu.Unlock()
		}
	}
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	default:
		slog.Debug("opencode-web SSE: event dropped", "type", ev.Type)
	}
}

func (s *sseSubscriber) setSessionFilter(id string) {
	if id == "" {
		return
	}
	s.sessionFilter.Store(id)
}

// Close tears the stream down. INVARIANT (copied from the legacy subscriber):
// events is closed only after the producer (readLoop, tracked by wg) exits —
// the timeout branch defers the close instead of racing it.
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
			slog.Debug("opencode-web SSE: close timeout, deferring events close until readLoop exits")
			go func() {
				<-done
				close(s.events)
			}()
		}
	})
	return nil
}

// Subscribe implements core.EventSubscriber for the passive (旁观) stream —
// every session on the serve, including external web turns.
func (a *Agent) Subscribe(ctx context.Context) (<-chan core.Event, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, fmt.Errorf("opencode-web SSE subscription unavailable: %w", err)
	}
	sub := newSSESubscriber(ctx, a, c)
	if err := sub.connect(); err != nil {
		return nil, err
	}
	go func() {
		<-sub.ctx.Done()
		_ = sub.Close()
	}()
	return sub.events, nil
}

var _ core.EventSubscriber = (*Agent)(nil)

// CatalogRefreshSignals implements core.CatalogRefreshSignaler: each SSE
// session.created/deleted asks the bridge for an immediate fingerprint
// rescan (sessions_changed); the discovery watcher stays the safety net.
func (a *Agent) CatalogRefreshSignals() <-chan struct{} {
	a.catalogMu.Lock()
	defer a.catalogMu.Unlock()
	if a.catalogRefresh == nil {
		a.catalogRefresh = make(chan struct{}, 16)
	}
	return a.catalogRefresh
}

func (a *Agent) signalCatalogRefresh() {
	a.catalogMu.Lock()
	if a.catalogRefresh == nil {
		a.catalogRefresh = make(chan struct{}, 16)
	}
	select {
	case a.catalogRefresh <- struct{}{}:
	default:
	}
	a.catalogMu.Unlock()
	// A catalog change also invalidates the merged project-directory view:
	// the next discovery poller / union listing must see the fresh registry.
	a.invalidateProjectCache()
}

var _ core.CatalogRefreshSignaler = (*Agent)(nil)
