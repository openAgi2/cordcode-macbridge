package grokbuild

// Leader-socket read-only subscriber for external Grok turns.
//
// Attaches to a running grok leader (~/.grok/leader.sock) as a passive subscriber
// for ONE session: leader handshake (register → ACP initialize → ACP session/load),
// then live session/update notifications are fed through the existing
// convertSessionUpdate codec → core.Event. It does NOT spawn a leader, does NOT
// acquire the flock, and does NOT drive the session, so it coexists with the
// production leader, the active TUI pager, and MacBridge's own --no-leader stdio
// grok subprocess. Protocol verified against /Users/jacklee/Projects/grok-build
// (leader/protocol.rs framing; leader/client.rs connect; leader/server.rs routing).
//
// Wire framing: 4-byte big-endian length prefix + JSON. The leader envelope is a
// serde tagged enum {type: "register"|"acp"|"ping"|"disconnect" / "registered"|
// "acp"|"pong"|"leader_ready"|...}. ACP JSON-RPC messages ride inside the "acp"
// envelope variant as a JSON string payload.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	leaderMaxFrameBytes   = 64 * 1024 * 1024 // matches grok MAX_MESSAGE_SIZE
	leaderClientType      = "cordcode-macbridge-observer"
	leaderPingInterval    = 30 * time.Second
	leaderConnectTimeout  = 5 * time.Second
	leaderRegisterTimeout = 10 * time.Second
	leaderReadyTimeout    = 300 * time.Second
)

// leaderClientMsg is the leader-envelope frame sent by the subscriber. Only the
// variants a read-only subscriber uses are populated.
type leaderClientMsg struct {
	Type         string         `json:"type"`                   // register | acp | ping | disconnect
	Mode         string         `json:"mode,omitempty"`         // register: "stdio"
	ClientType   string         `json:"client_type,omitempty"`  // register
	Capabilities map[string]any `json:"capabilities,omitempty"` // register (empty = valid)
	Payload      string         `json:"payload,omitempty"`      // acp: JSON-RPC string
}

// leaderServerMsg is the leader-envelope frame received. Only fields the
// subscriber inspects are decoded; unknown variants/types are ignored.
type leaderServerMsg struct {
	Type    string `json:"type"`              // registered | acp | pong | leader_ready | error | ...
	Ready   bool   `json:"ready,omitempty"`   // registered
	Payload string `json:"payload,omitempty"` // acp: JSON-RPC string
}

// resolveLeaderSocket resolves the leader socket path with overrides:
// GROK_LEADER_SOCKET env → $GROK_HOME/leader.sock → grokHome/leader.sock → ~/.grok/leader.sock.
func resolveLeaderSocket(grokHome string) string {
	if v := strings.TrimSpace(os.Getenv("GROK_LEADER_SOCKET")); v != "" {
		return v
	}
	home := grokHome
	if home == "" {
		if v := strings.TrimSpace(os.Getenv("GROK_HOME")); v != "" {
			home = v
		} else if u, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(u, ".grok")
		} else {
			home = ".grok"
		}
	}
	return filepath.Join(home, "leader.sock")
}

// LeaderSubscriber attaches to a running grok leader as a read-only subscriber
// for one session and forwards live session/update notifications as core.Events.
// Shared interaction reverse-requests (ask_user_question) are registered and
// surfaced as core.EventQuestionAsked; per owner ruling B (research doc §6.1)
// request_permission / exit_plan_mode / mcp/elicit remain observe-only.
type LeaderSubscriber struct {
	socketPath string
	sessionID  string
	cwd        string
	// conn is the live leader connection (set by Run), shared by the ping
	// goroutine and the answer write path. All writes go through writeMu.
	conn    net.Conn
	writeMu sync.Mutex
	// interactions registers live ask_user_question reverse-requests awaiting
	// a follower answer (research §2/§3). Keyed by tool_call_id — the same
	// identity the official interaction_resolved broadcast carries — so
	// resolution clears it deterministically. Upstream first-answer-wins is
	// the only arbiter; CordCode never adjudicates locally.
	interactions *leaderInteractionRegistry
	// onRosterChanged fires when the leader broadcasts the machine-wide
	// x.ai/sessions/changed roster notification (grok-build roster.rs
	// RosterChanged; leader server.rs broadcasts it to EVERY connected client,
	// not just session subscribers). It is a catalog-invalidation signal only —
	// the upserted/removed deltas are NOT applied locally; the authoritative
	// fingerprint rescan owns fence/seen/publish. Set once by the constructor's
	// caller before Run; read-only afterwards.
	onRosterChanged func()
	// emitFn is the session event callback captured from Run. The answer write
	// path uses it to emit canonical+legacy resolved at flush time — that is
	// the authoritative close for the answering client, because the leader's
	// interaction_resolved broadcast already evicted the registry entry by
	// then (take() makes the broadcast side silent).
	emitFn func(core.Event)
	emitMu sync.Mutex
}

// emitSessionEvent delivers an event through the Run callback if still live.
func (s *LeaderSubscriber) emitSessionEvent(ev core.Event) {
	s.emitMu.Lock()
	fn := s.emitFn
	s.emitMu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

// NewLeaderSubscriber builds a subscriber for the given session. socketPath is
// normally resolveLeaderSocket(grokHome).
func NewLeaderSubscriber(socketPath, sessionID, cwd string) *LeaderSubscriber {
	return &LeaderSubscriber{socketPath: socketPath, sessionID: sessionID, cwd: cwd,
		interactions: newLeaderInteractionRegistry()}
}

// leaderInteraction is one registered ask_user_question reverse-request.
type leaderInteraction struct {
	wireID     int                   // original numeric JSON-RPC id to answer with
	toolCallID string                // identity shared with interaction_resolved
	params     askUserQuestionParams // parsed inner params (questions, mode)
	// answers accumulates iOS replies per question index; the wire response
	// carries ALL questions at once, so it flushes only when complete
	// (single-question interactions — the only observed live shape — flush
	// immediately).
	answers map[int][]string
	// notes carries the freeform text per question index (typed iOS answers);
	// it flushes alongside answers as annotations[q].notes.
	notes map[int]string
}

type leaderInteractionRegistry struct {
	mu     sync.Mutex
	byTool map[string]leaderInteraction
	// tombstones records recently-consumed tool_call_ids (resolved broadcast
	// or our own flush). A late answer for a tombstoned id returns success
	// without writing — upstream silently drops it (research §3.5, red line 3:
	// no local error for consumed ids). A replayed REQUEST for the same
	// tool_call_id revives it (removes the tombstone).
	tombstones map[string]bool
}

func newLeaderInteractionRegistry() *leaderInteractionRegistry {
	return &leaderInteractionRegistry{
		byTool:     map[string]leaderInteraction{},
		tombstones: map[string]bool{},
	}
}

func (r *leaderInteractionRegistry) put(i leaderInteraction) {
	i.answers = map[int][]string{}
	r.mu.Lock()
	r.byTool[i.toolCallID] = i
	delete(r.tombstones, i.toolCallID)
	r.mu.Unlock()
}

// take removes and returns the entry for toolCallID (resolved eviction) and
// tombstones the id so late answers read as silently-consumed.
func (r *leaderInteractionRegistry) take(toolCallID string) (leaderInteraction, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.byTool[toolCallID]
	if ok {
		delete(r.byTool, toolCallID)
		r.markTombstoneLocked(toolCallID)
	}
	return i, ok
}

// get returns a read-only copy without evicting.
func (r *leaderInteractionRegistry) get(toolCallID string) (leaderInteraction, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.byTool[toolCallID]
	return i, ok
}

// consumed reports whether toolCallID was registered before and has since
// been consumed (resolved / answered / cleared).
func (r *leaderInteractionRegistry) consumed(toolCallID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tombstones[toolCallID]
}

func (r *leaderInteractionRegistry) markTombstoneLocked(toolCallID string) {
	if len(r.tombstones) >= 256 {
		r.tombstones = map[string]bool{}
	}
	r.tombstones[toolCallID] = true
}

// setAnswer records the selected labels (and optional freeform notes) for one
// question index and reports whether every question of the interaction is now
// answered.
func (r *leaderInteractionRegistry) setAnswer(toolCallID string, index int, labels []string, notes string) (complete bool, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, exists := r.byTool[toolCallID]
	if !exists || index < 0 || index >= len(i.params.Questions) {
		return false, false
	}
	if i.answers == nil {
		i.answers = map[int][]string{}
	}
	if i.notes == nil {
		i.notes = map[int]string{}
	}
	i.answers[index] = labels
	if notes != "" {
		i.notes[index] = notes
	}
	r.byTool[toolCallID] = i
	return len(i.answers) >= len(i.params.Questions), true
}

func (r *leaderInteractionRegistry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byTool)
}

// leaderPending maps in-flight ACP request id → response channel.
type leaderPending struct {
	mu    sync.Mutex
	chans map[int]chan jsonrpcResponse
}

func newLeaderPending() *leaderPending {
	return &leaderPending{chans: map[int]chan jsonrpcResponse{}}
}

func (p *leaderPending) register(id int) chan jsonrpcResponse {
	ch := make(chan jsonrpcResponse, 1)
	p.mu.Lock()
	p.chans[id] = ch
	p.mu.Unlock()
	return ch
}

func (p *leaderPending) deliver(id int, resp jsonrpcResponse) bool {
	p.mu.Lock()
	ch, ok := p.chans[id]
	if ok {
		delete(p.chans, id)
	}
	p.mu.Unlock()
	if !ok {
		return false
	}
	ch <- resp
	return true
}

func (p *leaderPending) dropAll() {
	p.mu.Lock()
	p.chans = map[int]chan jsonrpcResponse{}
	p.mu.Unlock()
}

// Run connects, handshakes, subscribes, and forwards live events to onEvent
// until ctx is cancelled or the connection fails. Replay notifications
// (_meta.isReplay == true) are dropped — iOS already loaded authoritative history.
func (s *LeaderSubscriber) Run(ctx context.Context, onEvent func(core.Event)) error {
	if strings.TrimSpace(s.sessionID) == "" {
		return fmt.Errorf("grokbuild: leader subscriber requires a sessionId")
	}
	dialer := net.Dialer{Timeout: leaderConnectTimeout}
	conn, err := dialer.DialContext(ctx, "unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("grokbuild: dial leader socket %s: %w", s.socketPath, err)
	}
	defer conn.Close()
	s.writeMu.Lock()
	s.conn = conn
	s.writeMu.Unlock()
	defer func() {
		s.writeMu.Lock()
		s.conn = nil
		s.writeMu.Unlock()
	}()
	slog.Info("grokbuild: leader subscriber connected", "session", s.sessionID, "socket", s.socketPath)
	s.emitMu.Lock()
	s.emitFn = onEvent
	s.emitMu.Unlock()

	registerCh := make(chan leaderServerMsg, 4)
	pending := newLeaderPending()
	readerDone := make(chan error, 1)
	go func() { readerDone <- s.readLoop(conn, registerCh, pending, s.sessionID, onEvent) }()

	// Ping keepalive (leader expects Ping every 30s; replies Pong).
	pingStop := make(chan struct{})
	go func() {
		t := time.NewTicker(leaderPingInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := writeLeaderMsg(conn, leaderClientMsg{Type: "ping"}); err != nil {
					return
				}
			case <-pingStop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	defer close(pingStop)

	// 1. register → registered (+ leader_ready if ready==false).
	if err := writeLeaderMsg(conn, leaderClientMsg{Type: "register", Mode: "stdio", ClientType: leaderClientType, Capabilities: map[string]any{}}); err != nil {
		return err
	}
	if err := s.awaitReady(ctx, registerCh); err != nil {
		return fmt.Errorf("grokbuild: leader register: %w", err)
	}

	// 2. ACP initialize (required before session/load).
	if _, err := s.acpCall(ctx, conn, pending, 1, "initialize", map[string]any{
		"protocolVersion":    "1",
		"clientCapabilities": map[string]any{},
	}); err != nil {
		return fmt.Errorf("grokbuild: leader initialize: %w", err)
	}

	// 3. ACP session/load — this is what subscribes us to the session.
	if _, err := s.acpCall(ctx, conn, pending, 2, "session/load", map[string]any{
		"sessionId":  s.sessionID,
		"cwd":        s.cwd,
		"mcpServers": []any{},
		"_meta":      map[string]any{},
	}); err != nil {
		return fmt.Errorf("grokbuild: leader session/load: %w", err)
	}
	slog.Info("grokbuild: leader subscriber live", "session", s.sessionID)

	// 4. Stay attached until ctx cancel or reader exit.
	select {
	case <-ctx.Done():
		pending.dropAll()
		return ctx.Err()
	case err := <-readerDone:
		pending.dropAll()
		if err != nil {
			return fmt.Errorf("grokbuild: leader read loop: %w", err)
		}
		return nil
	}
}

// awaitReady waits for "registered" and, if ready==false, a subsequent "leader_ready".
func (s *LeaderSubscriber) awaitReady(ctx context.Context, registerCh <-chan leaderServerMsg) error {
	for {
		select {
		case m := <-registerCh:
			switch m.Type {
			case "leader_ready":
				return nil
			case "registered":
				if m.Ready {
					return nil
				}
				// ready==false: keep waiting for leader_ready.
			default:
				// unexpected; ignore and keep waiting
			}
		case <-time.After(leaderReadyTimeout):
			return fmt.Errorf("leader_ready timeout")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// acpCall sends an ACP JSON-RPC request (wrapped in the leader "acp" envelope)
// and waits for its response.
func (s *LeaderSubscriber) acpCall(ctx context.Context, conn io.Writer, pending *leaderPending, id int, method string, params any) (json.RawMessage, error) {
	ch := pending.register(id)
	if err := writeACPRequest(conn, id, method, params); err != nil {
		pending.deliver(id, jsonrpcResponse{}) // free the slot
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(leaderRPCTimeout()):
		return nil, fmt.Errorf("%s timeout", method)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func leaderRPCTimeout() time.Duration { return 30 * time.Second }

// readLoop reads leader frames and dispatches them until the connection closes.
// Handshake control messages (registered/leader_ready) → registerCh; ACP responses
// → pending; ACP session/update notifications → convertSessionUpdate → onEvent
// (replay dropped). Other frames are ignored.
func (s *LeaderSubscriber) readLoop(conn io.Reader, registerCh chan<- leaderServerMsg, pending *leaderPending, sessionID string, onEvent func(core.Event)) error {
	for {
		payload, err := readLeaderFrame(conn)
		if err != nil {
			if err == io.EOF || strings.Contains(err.Error(), "closed") {
				return nil
			}
			return err
		}
		var msg leaderServerMsg
		if err := json.Unmarshal(payload, &msg); err != nil {
			slog.Debug("grokbuild: leader frame not envelope json", "error", err)
			continue
		}
		switch msg.Type {
		case "registered", "leader_ready":
			select {
			case registerCh <- msg:
			default:
			}
		case "pong":
			// keepalive ack
		case "acp":
			s.handleACP(msg.Payload, pending, sessionID, onEvent)
		default:
			// error / shutting_down / shutdown / control_result — ignore (reconnect on next Run)
		}
	}
}

// handleACP parses an ACP JSON-RPC payload and routes responses to pending,
// session/update notifications through convertSessionUpdate → onEvent.
func (s *LeaderSubscriber) handleACP(payload string, pending *leaderPending, sessionID string, onEvent func(core.Event)) {
	if payload == "" {
		return
	}
	var probe struct {
		ID     *json.RawMessage `json:"id,omitempty"`
		Method *string          `json:"method,omitempty"`
		Result json.RawMessage  `json:"result,omitempty"`
		Error  *jsonrpcError    `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		return
	}
	// Response to one of our requests.
	if probe.ID != nil && probe.Method == nil {
		var id int
		if json.Unmarshal(*probe.ID, &id) == nil {
			pending.deliver(id, jsonrpcResponse{Result: probe.Result, Error: probe.Error})
		}
		return
	}
	// Reverse-request (id + method): the leader broadcasts shared interaction
	// requests to every subscriber so any client can answer (first-answer-wins
	// upstream). Only ask_user_question is consumed (ruling B); others stay
	// observe-only.
	if probe.ID != nil {
		s.handleInteractionRequest(*probe.ID, *probe.Method, extractParams([]byte(payload)), sessionID, onEvent)
		return
	}
	// Notification (method, no id).
	if probe.Method == nil {
		return
	}
	method := *probe.Method
	if isRosterChangedMethod(method) {
		s.handleRosterChanged(extractParams([]byte(payload)))
		return
	}
	if !isSessionUpdateMethod(method) {
		return
	}
	params := extractParams([]byte(payload))
	if len(params) == 0 {
		return
	}
	if isReplayUpdate(params) {
		return // iOS already loaded authoritative history; don't re-emit the transcript.
	}
	// Official interaction lifecycle broadcasts ride the durable
	// session_notification rail; resolved evicts the registry and closes the
	// question on iOS (pending_interaction is a visibility signal only — the
	// REQUEST frame already carried the full question).
	if update, toolCallID := peekInteractionLifecycle(params); update != "" {
		s.handleInteractionLifecycle(update, toolCallID, sessionID, onEvent)
		return
	}
	for _, ev := range convertSessionUpdate(params, sessionID) {
		if onEvent != nil {
			onEvent(ev)
		}
	}
}

// handleInteractionRequest registers an ask_user_question broadcast and emits
// core.EventQuestionAsked per question. Wire id stays the ORIGINAL numeric id
// (research §3.1) — the answer path echoes it verbatim (§3.3).
func (s *LeaderSubscriber) handleInteractionRequest(rawID json.RawMessage, topMethod string, params json.RawMessage, sessionID string, onEvent func(core.Event)) {
	var wireID int
	if err := json.Unmarshal(rawID, &wireID); err != nil {
		slog.Debug("grokbuild: leader interaction request with non-numeric id dropped", "method", topMethod)
		return
	}
	method := normalizeLeaderMethod(topMethod, params)
	if method != "x.ai/ask_user_question" {
		return // request_permission / exit_plan_mode / mcp/elicit: observe-only (ruling B)
	}
	var p askUserQuestionParams
	if err := json.Unmarshal(interactionInnerParams(params), &p); err != nil || p.ToolCallID == "" || len(p.Questions) == 0 {
		slog.Warn("grokbuild: leader ask_user_question unparseable", "session", sessionID, "toolCallId", p.ToolCallID, "error", err)
		return
	}
	if s.interactions != nil {
		s.interactions.put(leaderInteraction{wireID: wireID, toolCallID: p.ToolCallID, params: p})
	}
	for i, q := range p.Questions {
		qid := questionIDFor(p.ToolCallID, i)
		opts := make([]core.QuestionOption, 0, len(q.Options))
		for _, o := range q.Options {
			// grok answers key by option LABEL (research §2.4.3), so the wire
			// option id IS the label.
			opts = append(opts, core.QuestionOption{ID: o.Label, Label: o.Label, Description: o.Description})
		}
		multi := q.MultiSelect != nil && *q.MultiSelect
		if multi {
			slog.Info("grokbuild: leader multiSelect question: canonical user_input face honors multiple; legacy v1 face stays single-select (wire has no multiSelect field)", "toolCallId", p.ToolCallID, "index", i)
		}
		emitQuestionAsked(onEvent, sessionID, qid, q.Question, opts, multi)
	}
	slog.Info("grokbuild: leader ask_user_question registered",
		"session", sessionID, "toolCallId", p.ToolCallID, "questions", len(p.Questions), "wireId", wireID, "mode", p.Mode)
}

// handleInteractionLifecycle maps the official pending_interaction /
// interaction_resolved broadcasts. Resolved is the authoritative close signal:
// it evicts the registry entry and emits question_resolved for every surfaced
// question id. Entries we never registered (permission requests, resolutions
// that raced ahead of our attach) produce no event — iOS only ever saw
// questions we surfaced.
func (s *LeaderSubscriber) handleInteractionLifecycle(update, toolCallID, sessionID string, onEvent func(core.Event)) {
	switch update {
	case "interaction_resolved":
		if s.interactions == nil {
			return
		}
		entry, ok := s.interactions.take(toolCallID)
		if !ok {
			slog.Debug("grokbuild: leader interaction_resolved for unregistered tool call", "toolCallId", toolCallID)
			return
		}
		for i := range entry.params.Questions {
			// Leader-side resolution (TUI answered first, or the answer raced
			// our attach): close on both faces. The broadcast carries no
			// outcome detail, only that the interaction completed — answered is
			// the projection close that matters for card state.
			emitQuestionResolved(onEvent, sessionID, questionIDFor(entry.toolCallID, i), "resolved", "mac")
		}
		slog.Info("grokbuild: leader interaction_resolved closed question", "toolCallId", toolCallID)
	case "pending_interaction":
		// REQUEST frame already surfaced questions; permission pendings are
		// observe-only visibility (ruling B) — log, no wire event.
		slog.Debug("grokbuild: leader pending_interaction", "toolCallId", toolCallID)
	}
}

// questionIDFor derives the bridge question identity from the interaction's
// tool_call_id. Single-question requests (the only observed live shape,
// research §3.1) use the bare id; later indices append #i to stay unique.
func questionIDFor(toolCallID string, index int) string {
	if index == 0 {
		return toolCallID
	}
	return fmt.Sprintf("%s#%d", toolCallID, index)
}

// parseQuestionID is the inverse of questionIDFor.
func parseQuestionID(questionID string) (toolCallID string, index int, err error) {
	if i := strings.LastIndex(questionID, "#"); i > 0 {
		n, perr := strconv.Atoi(questionID[i+1:])
		if perr != nil || n < 0 {
			return "", 0, fmt.Errorf("grokbuild: malformed question id %q", questionID)
		}
		return questionID[:i], n, nil
	}
	return questionID, 0, nil
}

// askUserQuestionExtResponse mirrors upstream AskUserQuestionExtResponse
// (ask_user_question/types.rs): internally tagged on "outcome"; Accepted
// carries answers keyed by QUESTION TEXT with one label per selected option,
// plus optional per-question annotations where a typed freeform answer rides
// as notes (freeform-only selects are the single label "Other"); Cancelled is
// a bare tag (not an error upstream).
type askUserQuestionExtResponse struct {
	Outcome     string                     `json:"outcome"` // "accepted" | "cancelled"
	Answers     map[string][]string        `json:"answers,omitempty"`
	Annotations map[string]askAnnotation   `json:"annotations,omitempty"`
}

// askAnnotation mirrors upstream QuestionAnnotation{preview, notes} — only
// notes is produced (CordCode never synthesizes a preview).
type askAnnotation struct {
	Notes string `json:"notes,omitempty"`
}

// freeformOtherLabel is the wire label grok's TUI submits for a freeform-only
// answer ("type your answer here"); the typed text rides annotations notes
// (types.rs AskUserQuestionExtResponse::Accepted doc).
const freeformOtherLabel = "Other"

// AnswerQuestion records an iOS reply for one question of a pending
// interaction and, once every question is answered, sends the wire response
// with the ORIGINAL numeric id (research §3.3). notes is the typed freeform
// answer: non-empty notes make the freeform label "Other" a valid selection
// and flush as annotations[q].notes (mirroring the TUI's "type your answer
// here" path). The response is a plain fire-and-forget frame: upstream
// first-answer-wins consumes it silently, and a late/duplicate answer is
// dropped by the leader without any error frame (§3.5) — so success here
// means "submitted", not "adjudicated". An answer for an already-consumed
// interaction (TUI grabbed it, resolved broadcast arrived first) returns
// resolved=true silently per red line 3.
//
// resolved reports whether the interaction is closed after this call: true on
// flush and on already-consumed silence, false while a multi-question
// interaction is still accumulating replies. On flush, canonical+legacy
// resolved events emit through the Run callback — the answering client's
// authoritative close (the later interaction_resolved broadcast finds the
// registry empty and stays silent).
func (s *LeaderSubscriber) AnswerQuestion(questionID string, optionIDs []string, notes string) (bool, error) {
	toolCallID, index, err := parseQuestionID(questionID)
	if err != nil {
		return false, err
	}
	if s.interactions == nil {
		return false, fmt.Errorf("grokbuild: no pending question %s", questionID)
	}
	if _, ok := s.interactions.get(toolCallID); !ok {
		if s.interactions.consumed(toolCallID) {
			return true, nil // late answer to an adjudicated interaction — silent (§3.5)
		}
		return false, fmt.Errorf("grokbuild: no pending question %s", questionID)
	}
	labels, err := s.validateQuestionSelection(toolCallID, index, optionIDs, notes)
	if err != nil {
		return false, err
	}
	complete, ok := s.interactions.setAnswer(toolCallID, index, labels, notes)
	if !ok {
		return false, fmt.Errorf("grokbuild: no pending question %s", questionID)
	}
	if !complete {
		// Multi-question interaction still missing replies; flush happens on
		// the last one.
		return false, nil
	}
	entry, _ := s.interactions.take(toolCallID)
	if err := s.sendInteractionResponse(entry, askUserQuestionExtResponse{
		Outcome:     "accepted",
		Answers:     answersByQuestionText(entry, entry.answers),
		Annotations: annotationsByQuestionText(entry, entry.notes),
	}); err != nil {
		return false, err
	}
	for i := range entry.params.Questions {
		emitQuestionResolved(s.emitSessionEvent, s.sessionID, questionIDFor(entry.toolCallID, i), "accepted", "ios")
	}
	return true, nil
}

// CancelQuestion dismisses a pending interaction (upstream Path D: not an
// error; the tool completes the turn as Cancelled). A cancel for an
// already-consumed interaction is silent, mirroring AnswerQuestion. resolved
// semantics match AnswerQuestion; a live cancel always flushes, so it returns
// true unless the write itself fails.
func (s *LeaderSubscriber) CancelQuestion(questionID string) (bool, error) {
	toolCallID, _, err := parseQuestionID(questionID)
	if err != nil {
		return false, err
	}
	if s.interactions == nil {
		return false, fmt.Errorf("grokbuild: no pending question %s", questionID)
	}
	entry, ok := s.interactions.take(toolCallID)
	if !ok {
		if s.interactions.consumed(toolCallID) {
			return true, nil
		}
		return false, fmt.Errorf("grokbuild: no pending question %s", questionID)
	}
	if err := s.sendInteractionResponse(entry, askUserQuestionExtResponse{Outcome: "cancelled"}); err != nil {
		return false, err
	}
	for i := range entry.params.Questions {
		emitQuestionResolved(s.emitSessionEvent, s.sessionID, questionIDFor(entry.toolCallID, i), "cancelled", "ios")
	}
	return true, nil
}

// validateQuestionSelection checks the reply against the registered question:
// the option ids on the bridge wire are grok option LABELS, so every id must
// be one of the question's labels — except the freeform label "Other", which
// is only valid when accompanied by typed notes (the TUI's freeform path;
// "Other" without notes is never submitted upstream). Read-only (peek) —
// accumulated answers for sibling questions must survive.
func (s *LeaderSubscriber) validateQuestionSelection(toolCallID string, index int, optionIDs []string, notes string) ([]string, error) {
	entry, ok := s.interactions.get(toolCallID)
	if !ok {
		return nil, fmt.Errorf("grokbuild: no pending question for tool call %s", toolCallID)
	}
	if index >= len(entry.params.Questions) {
		return nil, fmt.Errorf("grokbuild: question index %d out of range for tool call %s", index, toolCallID)
	}
	valid := map[string]bool{}
	for _, o := range entry.params.Questions[index].Options {
		valid[o.Label] = true
	}
	if len(optionIDs) == 0 {
		return nil, fmt.Errorf("grokbuild: question reply carries no option")
	}
	for _, id := range optionIDs {
		if valid[id] {
			continue
		}
		if id == freeformOtherLabel && strings.TrimSpace(notes) != "" {
			continue
		}
		return nil, fmt.Errorf("grokbuild: option %q is not one of the offered choices", id)
	}
	return optionIDs, nil
}

// sendInteractionResponse encodes the JSON-RPC response with the ORIGINAL
// numeric wire id and writes it through the live leader connection.
func (s *LeaderSubscriber) sendInteractionResponse(entry leaderInteraction, result askUserQuestionExtResponse) error {
	rawID, err := json.Marshal(entry.wireID)
	if err != nil {
		return err
	}
	resp, err := encodeResponse(rawID, result)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.conn == nil {
		return fmt.Errorf("grokbuild: leader connection closed")
	}
	return writeLeaderMsg(s.conn, leaderClientMsg{Type: "acp", Payload: string(resp)})
}

// answersByQuestionText maps per-index selections onto the wire answers map,
// which upstream keys by the question TEXT (research §2.4.3 / §3.3).
func answersByQuestionText(entry leaderInteraction, answers map[int][]string) map[string][]string {
	out := make(map[string][]string, len(entry.params.Questions))
	for i, q := range entry.params.Questions {
		if sel, ok := answers[i]; ok && len(sel) > 0 {
			out[q.Question] = sel
		}
	}
	return out
}

// annotationsByQuestionText maps per-index freeform notes onto the wire
// annotations map (keyed by question TEXT like answers). Returns nil when no
// question carries notes — the field is omitempty upstream.
func annotationsByQuestionText(entry leaderInteraction, notes map[int]string) map[string]askAnnotation {
	var out map[string]askAnnotation
	for i, q := range entry.params.Questions {
		if n, ok := notes[i]; ok && n != "" {
			if out == nil {
				out = make(map[string]askAnnotation, len(entry.params.Questions))
			}
			out[q.Question] = askAnnotation{Notes: n}
		}
	}
	return out
}

// peekInteractionLifecycle reports the interaction lifecycle sessionUpdate
// (pending_interaction / interaction_resolved) and its tool_call_id carried
// inside a session_notification frame (research §3.2: pending also carries
// kind, resolved carries only tool_call_id).
func peekInteractionLifecycle(params json.RawMessage) (update, toolCallID string) {
	var p struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			ToolCallID    string `json:"tool_call_id"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", ""
	}
	switch p.Update.SessionUpdate {
	case "pending_interaction", "interaction_resolved":
		return p.Update.SessionUpdate, p.Update.ToolCallID
	}
	return "", ""
}

// normalizeLeaderMethod mirrors grok-build server.rs method_of: gateway ext
// methods may arrive "_"-prefixed, and a fully wrapped payload repeats the
// real method under params.method (which then wins over the stripped prefix).
// Direct: {"method":"x.ai/foo"} → x.ai/foo; wrapped/half-wrapped top-level
// "_x.ai/foo" → x.ai/foo (params.method override when present).
func normalizeLeaderMethod(topMethod string, params json.RawMessage) string {
	if !strings.HasPrefix(topMethod, "_") {
		return topMethod
	}
	stripped := strings.TrimPrefix(topMethod, "_")
	if len(params) > 0 {
		var probe struct {
			Method *string `json:"method"`
		}
		if json.Unmarshal(params, &probe) == nil && probe.Method != nil && *probe.Method != "" {
			return *probe.Method
		}
	}
	return stripped
}

// interactionInnerParams mirrors server.rs interaction_inner_params: for a
// fully wrapped ext payload (params carries its own method+params) the real
// params live at params.params; otherwise params is already the real payload
// (the half-wrapped form observed on the 1.0.13 wire, research §3.1).
func interactionInnerParams(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return params
	}
	var probe struct {
		Method *json.RawMessage `json:"method"`
		Params *json.RawMessage `json:"params"`
	}
	if json.Unmarshal(params, &probe) == nil && probe.Method != nil && probe.Params != nil {
		return *probe.Params
	}
	return params
}

// isSessionUpdateMethod reports whether a notification method carries a session/update.
// The gateway ext rail wraps methods with an "_" prefix on the wire: the durable turn
// terminal arrives as _x.ai/session_notification (params.update carries the
// sessionUpdate payload directly), never as the unwrapped x.ai/session_notification.
func isSessionUpdateMethod(method string) bool {
	switch method {
	case "session/update", "_x.ai/session/update", "_x.ai/session_notification":
		return true
	}
	return false
}

// isRosterChangedMethod reports whether a notification method is the machine-wide
// roster broadcast x.ai/sessions/changed (grok-build roster.rs
// SESSIONS_CHANGED_METHOD; leader server.rs routes it to every connected client).
// Both the gateway-ext "_"-prefixed form and the bare form are accepted — the
// official leader tests exercise both wire shapes (server_tests.rs).
func isRosterChangedMethod(method string) bool {
	switch method {
	case "x.ai/sessions/changed", "_x.ai/sessions/changed":
		return true
	}
	return false
}

// rosterChangedPayload mirrors the upstream RosterChanged wire shape
// (roster.rs, camelCase). Only the counts are inspected — for logging — because
// consumption is invalidation-only; the authoritative catalog rescan owns truth.
type rosterChangedPayload struct {
	Upserted []json.RawMessage `json:"upserted"`
	Removed  []string          `json:"removed"`
}

// handleRosterChanged fires the roster callback. The frame itself is the signal:
// even an unparseable or empty payload still means "the roster may have changed",
// and the downstream fingerprint diff is idempotent, so the callback always fires.
func (s *LeaderSubscriber) handleRosterChanged(params json.RawMessage) {
	if s.onRosterChanged == nil {
		return
	}
	var p rosterChangedPayload
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Debug("grokbuild: leader roster changed (unparseable payload)", "bytes", len(params))
	} else {
		slog.Debug("grokbuild: leader roster changed", "upserted", len(p.Upserted), "removed", len(p.Removed))
	}
	s.onRosterChanged()
}

// isReplayUpdate reports whether the session/update params carry _meta.isReplay==true.
func isReplayUpdate(params json.RawMessage) bool {
	var meta struct {
		Meta struct {
			IsReplay bool `json:"isReplay"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(params, &meta); err != nil {
		return false
	}
	return meta.Meta.IsReplay
}

// --- frame + envelope codecs ---

func writeLeaderMsg(w io.Writer, msg leaderClientMsg) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return writeLeaderFrame(w, b)
}

func writeACPRequest(w io.Writer, id int, method string, params any) error {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode params for %s: %w", method, err)
	}
	req := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{
		JSONRPC: "2.0", ID: id, Method: method, Params: paramsJSON,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return writeLeaderMsg(w, leaderClientMsg{Type: "acp", Payload: string(b)})
}

func writeLeaderFrame(w io.Writer, payload []byte) error {
	if len(payload) > leaderMaxFrameBytes {
		return fmt.Errorf("frame too large: %d", len(payload))
	}
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}

func readLeaderFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return []byte{}, nil
	}
	if n > leaderMaxFrameBytes {
		return nil, fmt.Errorf("leader frame too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
