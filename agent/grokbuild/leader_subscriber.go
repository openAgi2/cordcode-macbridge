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
type LeaderSubscriber struct {
	socketPath string
	sessionID  string
	cwd        string
	// onRosterChanged fires when the leader broadcasts the machine-wide
	// x.ai/sessions/changed roster notification (grok-build roster.rs
	// RosterChanged; leader server.rs broadcasts it to EVERY connected client,
	// not just session subscribers). It is a catalog-invalidation signal only —
	// the upserted/removed deltas are NOT applied locally; the authoritative
	// fingerprint rescan owns fence/seen/publish. Set once by the constructor's
	// caller before Run; read-only afterwards.
	onRosterChanged func()
}

// NewLeaderSubscriber builds a subscriber for the given session. socketPath is
// normally resolveLeaderSocket(grokHome).
func NewLeaderSubscriber(socketPath, sessionID, cwd string) *LeaderSubscriber {
	return &LeaderSubscriber{socketPath: socketPath, sessionID: sessionID, cwd: cwd}
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
	slog.Info("grokbuild: leader subscriber connected", "session", s.sessionID, "socket", s.socketPath)

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
	// Notification (method, no id). Read-only subscriber never answers requests.
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
	for _, ev := range convertSessionUpdate(params, sessionID) {
		if onEvent != nil {
			onEvent(ev)
		}
	}
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
