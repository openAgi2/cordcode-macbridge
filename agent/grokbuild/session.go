package grokbuild

// grokSession implements core.AgentSession for a single Grok Build CLI process
// running in ACP stdio mode (`grok agent stdio`).

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.AgentSession = (*grokSession)(nil)
var _ core.TurnCanceler = (*grokSession)(nil)

type grokSession struct {
	agent *Agent

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	stdinMu sync.Mutex // serializes writes to stdin
	events  chan core.Event

	sessionID atomic.Value // string
	alive     atomic.Bool
	// terminalDone is set when a Done event has been emitted so process exit
	// does not emit a second terminal event.
	terminalDone atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when process exits

	// ACP request ID counter
	idCounter requestIDCounter

	// pending permission requests: requestID -> options (for allow/deny lookup)
	pendingPermsMu  sync.Mutex
	pendingPerms    map[string][]permissionOption
	pendingPromptID int // session/prompt request ID for turn-end detection

	// pending response matching: maps request ID → channel for synchronous waits
	respMu       sync.Mutex
	respChannels map[int]chan *jsonrpcResponse

	// ACP capabilities learned from initialize
	supportsLoadSession bool
	supportsListSession bool
}

func newGrokSession(ctx context.Context, agent *Agent, sessionID string) (*grokSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	args := []string{"agent", "--no-leader", "stdio"}
	args = append(args, agent.cliExtraArgs...)

	cmd := exec.CommandContext(sessionCtx, agent.cliBin, args...)
	cmd.Dir = agent.workDir
	prepareCmdForProcessGroup(cmd)

	// Build a clean environment: no control-plane secrets.
	baseEnv := core.FilterEnvToAllowlist(
		filterOsEnviron(),
		core.DefaultEnvAllowlist,
	)
	cmd.Env = core.BuildAgentEnv(baseEnv, nil, nil)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild: start %s: %w", agent.cliBin, err)
	}

	s := &grokSession{
		agent:        agent,
		cmd:          cmd,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		events:       make(chan core.Event, 64),
		ctx:          sessionCtx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	// Store requested ID early; loadSession keeps it, newSession replaces it.
	s.sessionID.Store(sessionID)
	s.alive.Store(true)

	handshakeStart := time.Now()
	slog.Info("grokbuild: handshake step",
		"step", "process_started",
		"pid", cmd.Process.Pid,
		"session_id_prefix", shortID(sessionID),
		"elapsed_ms", time.Since(handshakeStart).Milliseconds())

	// Start stderr reader (logs only, no events).
	go s.readStderr()

	// Start stdout reader (ACP JSON-RPC messages → events).
	go s.readLoop()

	// Perform ACP initialization handshake.
	initStart := time.Now()
	if err := s.initialize(); err != nil {
		s.cleanup()
		slog.Warn("grokbuild: handshake failed at initialize",
			"elapsed_ms", time.Since(initStart).Milliseconds(),
			"error_class", rpcErrorClass(err))
		return nil, fmt.Errorf("grokbuild: initialize: %w", err)
	}
	slog.Info("grokbuild: handshake step",
		"step", "initialize_done",
		"elapsed_ms", time.Since(initStart).Milliseconds(),
		"supportsLoadSession", s.supportsLoadSession)

	// Create or load the session.
	// Resume path must not silently create a new session (audit P0-1).
	loadStart := time.Now()
	if sessionID != "" {
		if !s.supportsLoadSession {
			s.cleanup()
			return nil, fmt.Errorf("grokbuild: cannot resume session %s: agent did not advertise loadSession", sessionID)
		}
		if err := s.loadSession(sessionID); err != nil {
			s.cleanup()
			slog.Warn("grokbuild: handshake failed at session/load",
				"elapsed_ms", time.Since(loadStart).Milliseconds(),
				"error_class", rpcErrorClass(err))
			return nil, fmt.Errorf("grokbuild: session/load %s: %w", sessionID, err)
		}
		slog.Info("grokbuild: handshake step",
			"step", "session_loaded",
			"elapsed_ms", time.Since(loadStart).Milliseconds())
	} else {
		if err := s.newSession(); err != nil {
			s.cleanup()
			slog.Warn("grokbuild: handshake failed at session/new",
				"elapsed_ms", time.Since(loadStart).Milliseconds(),
				"error_class", rpcErrorClass(err))
			return nil, fmt.Errorf("grokbuild: session/new: %w", err)
		}
		slog.Info("grokbuild: handshake step",
			"step", "session_created",
			"elapsed_ms", time.Since(loadStart).Milliseconds())
	}

	slog.Info("grokbuild: handshake complete",
		"total_elapsed_ms", time.Since(handshakeStart).Milliseconds())

	// Drain stale events accumulated during handshake (session/load causes Grok
	// to replay state via session/update notifications). These are historical
	// state — not part of the user's current turn. If left in the channel,
	// relayEvents will forward them to iOS as if they were live turn events,
	// including any prior error that would abort the turn immediately.
	drained := 0
drainLoop:
	for {
		select {
		case <-s.events:
			drained++
		default:
			break drainLoop
		}
	}
	if drained > 0 {
		// Reset terminalDone so the real turn's terminal event is not suppressed.
		s.terminalDone.Store(false)
		slog.Info("grokbuild: drained stale handshake events", "count", drained)
	}

	// Wait for process exit in background; emit a terminal error if none yet.
	go func() {
		waitErr := cmd.Wait()
		close(s.done)
		s.alive.Store(false)
		if waitErr != nil && !s.terminalDone.Load() {
			s.emit(core.Event{
				Type:    core.EventError,
				Error:   waitErr,
				Content: fmt.Sprintf("grok process exited: %v", waitErr),
				Done:    true,
			})
		}
	}()

	return s, nil
}

// --- ACP handshake ---

func (s *grokSession) initialize() error {
	id := s.idCounter.next()
	params := initializeParams{
		ProtocolVersion: 1,
		ClientCapabilities: &clientCapabilities{
			Session: &sessionClientCaps{
				ConfigOptions: &map[string]any{},
			},
		},
		ClientInfo: &clientInfo{
			Name:    "cordcode-macbridge",
			Title:   "CordCode MacBridge",
			Version: "1.0",
		},
	}
	result, err := s.callRPC(id, "initialize", params, 10*time.Second)
	if err != nil {
		return err
	}

	var initResp initializeResult
	if err := json.Unmarshal(result, &initResp); err != nil {
		return fmt.Errorf("decode initialize response: %w", err)
	}

	if initResp.AgentCapabilities != nil {
		// Grok returns loadSession as JSON bool true; other ACP agents may use {}.
		if initResp.AgentCapabilities.LoadSession.Enabled {
			s.supportsLoadSession = true
		}
		if initResp.AgentCapabilities.SessionCapabilities != nil {
			if initResp.AgentCapabilities.SessionCapabilities.List.Enabled {
				s.supportsListSession = true
			}
		}
		// Authenticate if methods are advertised.
		if len(initResp.AuthMethods) > 0 {
			if err := s.authenticate(initResp.AuthMethods[0].ID); err != nil {
				return fmt.Errorf("authenticate: %w", err)
			}
		}
	}

	return nil
}

func (s *grokSession) authenticate(method string) error {
	id := s.idCounter.next()
	_, err := s.callRPC(id, "authenticate", authenticateParams{MethodID: method}, 30*time.Second)
	return err
}

func (s *grokSession) newSession() error {
	id := s.idCounter.next()
	params := sessionNewParams{
		CWD:        s.agent.workDir,
		McpServers: []any{}, // empty array — no MCP servers
	}
	result, err := s.callRPC(id, "session/new", params, 15*time.Second)
	if err != nil {
		return err
	}
	var resp sessionNewResult
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("decode session/new response: %w", err)
	}
	sid := strings.TrimSpace(resp.SessionID)
	if sid == "" {
		return fmt.Errorf("grokbuild: session/new returned empty sessionId")
	}
	s.sessionID.Store(sid)
	// Log only a short prefix — never the full agent payload.
	slog.Info("grokbuild: session created", "id_prefix", shortID(sid))
	return nil
}

func (s *grokSession) loadSession(sessionID string) error {
	cwd := ""
	var grokHome string
	if s.agent != nil {
		cwd = strings.TrimSpace(s.agent.GetWorkDir())
		grokHome = s.agent.grokHome
	}
	if cwd == "" || cwd == "." {
		// Fall back to on-disk catalog when switchDir was not applied.
		if home := resolveGrokHome(grokHome); home != "" {
			if dir := findSessionDir(home, sessionID); dir != "" {
				if info, ok := parseSummaryFile(filepath.Join(dir, "summary.json")); ok && info.Directory != "" {
					cwd = info.Directory
				}
			}
		}
	}
	if cwd == "" || cwd == "." {
		return fmt.Errorf("grokbuild: session/load requires cwd (work_dir empty and catalog miss)")
	}

	id := s.idCounter.next()
	slog.Info("grokbuild: loadSession calling callRPC",
		"session_id_prefix", shortID(sessionID),
		"cwd_base", filepath.Base(cwd))
	_, err := s.callRPC(id, "session/load", sessionLoadParams{
		SessionID:  sessionID,
		CWD:        cwd,
		McpServers: []any{}, // required empty array (same as session/new)
	}, 15*time.Second)
	if err != nil {
		return err
	}
	// Align process workDir with the loaded session workspace.
	if s.agent != nil {
		s.agent.SetWorkDir(cwd)
	}
	if s.cmd != nil {
		s.cmd.Dir = cwd
	}
	s.sessionID.Store(sessionID)
	slog.Info("grokbuild: session loaded", "id_prefix", shortID(sessionID), "cwd_base", filepath.Base(cwd))
	return nil
}

// --- core.AgentSession ---

func (s *grokSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("grokbuild: session not alive")
	}

	// Save file attachments to disk and reference paths in the prompt.
	filePaths := core.SaveFilesToDisk(s.agent.workDir, files)
	fullPrompt := prompt
	if len(filePaths) > 0 {
		fullPrompt = prompt + "\n\nAttached files:\n" + strings.Join(filePaths, "\n")
	}

	// Build ACP prompt as a content block array.
	content := []contentBlock{
		{Type: "text", Text: fullPrompt},
	}

	// Add images as image content blocks.
	for _, img := range images {
		content = append(content, contentBlock{
			Type: "image",
			Name: img.FileName,
		})
	}

	id := s.idCounter.next()
	// Register turn-end ID before write so a fast response cannot be lost (P0-2).
	s.pendingPermsMu.Lock()
	s.pendingPromptID = id
	s.pendingPermsMu.Unlock()
	// Reset terminal flag for the new turn.
	s.terminalDone.Store(false)

	// Emit turn_started before sending.
	s.emit(core.Event{Type: core.EventTurnStarted})

	if err := s.writeRequest(id, "session/prompt", sessionPromptParams{
		SessionID: s.CurrentSessionID(),
		Prompt:    content,
	}); err != nil {
		s.pendingPermsMu.Lock()
		if s.pendingPromptID == id {
			s.pendingPromptID = 0
		}
		s.pendingPermsMu.Unlock()
		return err
	}

	return nil
}

// CancelTurn implements core.TurnCanceler by sending ACP session/cancel.
func (s *grokSession) CancelTurn(ctx context.Context) error {
	_ = ctx
	if !s.alive.Load() {
		return fmt.Errorf("grokbuild: session not alive")
	}
	sid := s.CurrentSessionID()
	if sid == "" {
		return fmt.Errorf("grokbuild: no session id for cancel")
	}
	data, err := encodeNotification("session/cancel", sessionCancelParams{SessionID: sid})
	if err != nil {
		return err
	}
	s.stdinMu.Lock()
	_, err = s.stdin.Write(data)
	s.stdinMu.Unlock()
	return err
}

func (s *grokSession) RespondPermission(requestID string, result core.PermissionResult) error {
	if !s.alive.Load() {
		return fmt.Errorf("grokbuild: session not alive")
	}

	s.pendingPermsMu.Lock()
	options, ok := s.pendingPerms[requestID]
	if ok {
		delete(s.pendingPerms, requestID)
	}
	s.pendingPermsMu.Unlock()

	if !ok {
		return fmt.Errorf("grokbuild: no pending permission for request %s", requestID)
	}

	var outcome outcomePayload
	if result.Behavior == "allow" {
		optionID, found := selectPermissionOption(options, "allow")
		if !found {
			return fmt.Errorf("grokbuild: no allow option in permission request %s", requestID)
		}
		outcome = outcomePayload{Outcome: "selected", OptionID: optionID}
	} else {
		optionID, found := selectPermissionOption(options, "deny")
		if !found {
			outcome = outcomePayload{Outcome: "cancelled"}
		} else {
			outcome = outcomePayload{Outcome: "selected", OptionID: optionID}
		}
	}

	// Parse the request ID as a JSON-RPC id (it was the numeric id from the agent's request).
	rawID := json.RawMessage(requestID)
	resp, err := encodeResponse(rawID, requestPermissionResult{Outcome: outcome})
	if err != nil {
		return err
	}

	s.stdinMu.Lock()
	_, err = s.stdin.Write(resp)
	s.stdinMu.Unlock()
	return err
}

func (s *grokSession) Events() <-chan core.Event { return s.events }

func (s *grokSession) CurrentSessionID() string {
	v := s.sessionID.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

func (s *grokSession) Alive() bool { return s.alive.Load() }

// RespondQuestion — ACP has no question protocol. Always returns ErrNotSupported.
func (s *grokSession) RespondQuestion(questionID string, optionIDs []string) error {
	return core.ErrNotSupported
}

// RejectQuestion — ACP has no question protocol. Always returns ErrNotSupported.
func (s *grokSession) RejectQuestion(questionID string) error {
	return core.ErrNotSupported
}

func (s *grokSession) Close() error {
	// Phase 1: close stdin, wait for graceful exit.
	s.stdinMu.Lock()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	s.stdinMu.Unlock()

	select {
	case <-s.done:
		return nil
	case <-time.After(gracefulStopTimeout):
	}

	// Phase 2: SIGTERM the process group.
	_ = signalProcessGroup(s.cmd, sigTERM)

	select {
	case <-s.done:
		return nil
	case <-time.After(5 * time.Second):
	}

	// Phase 3: SIGKILL.
	s.cancel()
	_ = signalProcessGroup(s.cmd, sigKILL)
	<-s.done
	return nil
}

// --- read loop ---

func (s *grokSession) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	msgCount := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		msgCount++
		s.handleMessage(line)
	}
	// scan_err_class: fixed safe category, never the raw scanner error (may
	// contain agent stdout content).
	scanErrClass := "none"
	if err := scanner.Err(); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			scanErrClass = "context_cancelled"
		default:
			scanErrClass = "scanner_error"
		}
		if s.alive.Load() {
			s.emit(core.Event{
				Type:    core.EventError,
				Content: fmt.Sprintf("stdout read error: %v", err),
				Done:    true,
			})
		}
	}
	slog.Info("grokbuild: readLoop exited",
		"messages_processed", msgCount,
		"scan_err_class", scanErrClass,
		"alive", s.alive.Load())
	// Process exited or stdout EOF.
	s.alive.Store(false)
}

func (s *grokSession) handleMessage(line []byte) {
	resp, req, notif, err := decodeMessage(line)
	if err != nil {
		// Never log agent-controlled payload (may contain prompt / tool args / paths).
		slog.Warn("grokbuild: decode failed",
			"error_class", logErrorClass(err),
			"bytes", len(line),
		)
		return
	}

	switch {
	case resp != nil:
		s.handleResponse(resp)
	case req != nil:
		s.handleRequest(req)
	case notif != nil:
		s.handleNotification(notif)
	}
}

func (s *grokSession) handleResponse(resp *jsonrpcResponse) {
	// Parse the numeric request ID.
	var idNum int
	if err := json.Unmarshal(resp.ID, &idNum); err != nil {
		// Could be a string ID — try that.
		var idStr string
		if err2 := json.Unmarshal(resp.ID, &idStr); err2 != nil {
			return
		}
		slog.Debug("grokbuild: response with string ID, ignoring", "id", idStr)
		return
	}

	// Route to synchronous waiters (initialize, session/new, etc.).
	s.respMu.Lock()
	ch, ok := s.respChannels[idNum]
	if ok {
		delete(s.respChannels, idNum)
	}
	s.respMu.Unlock()
	if ok {
		ch <- resp
		return
	}

	// Check if this is a session/prompt response (turn end).
	s.pendingPermsMu.Lock()
	promptID := s.pendingPromptID
	s.pendingPermsMu.Unlock()

	if idNum == promptID && promptID != 0 {
		// This is the session/prompt response — turn is done.
		if resp.Error != nil {
			// Log only the numeric code; message may contain agent payload.
			slog.Warn("grokbuild: session/prompt returned error",
				"error_code", resp.Error.Code)
			s.emit(core.Event{
				Type:  core.EventError,
				Error: fmt.Errorf("session/prompt error %d: %s", resp.Error.Code, resp.Error.Message),
				Done:  true,
			})
		} else {
			s.emit(core.Event{
				Type: core.EventResult,
				Done: true,
			})
		}
	}
}

func (s *grokSession) handleRequest(req *agentRequest) {
	switch req.Method {
	case "session/request_permission":
		s.handlePermissionRequest(req)
	default:
		slog.Debug("grokbuild: unhandled agent request", "method", req.Method)
	}
}

func (s *grokSession) handleNotification(notif *agentNotification) {
	switch notif.Method {
	case "session/update":
		events := convertSessionUpdate(notif.Params, s.CurrentSessionID())
		for _, ev := range events {
			s.emit(ev)
		}
	case "session/cancel":
		// Agent cancelled its own turn — emit a result.
		s.emit(core.Event{
			Type: core.EventResult,
			Done: true,
		})
	default:
		slog.Debug("grokbuild: unhandled notification", "method", notif.Method)
	}
}

func (s *grokSession) handlePermissionRequest(req *agentRequest) {
	var params requestPermissionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		slog.Warn("grokbuild: decode permission request", "error", err)
		return
	}

	// Store the options for later allow/deny lookup.
	reqIDStr := string(req.ID)
	s.pendingPermsMu.Lock()
	s.pendingPerms[reqIDStr] = params.Options
	s.pendingPermsMu.Unlock()

	s.emit(core.Event{
		Type:     core.EventPermissionRequest,
		RequestID: reqIDStr,
		ToolName: params.ToolCall.Title,
	})
}

func (s *grokSession) readStderr() {
	scanner := bufio.NewScanner(s.stderr)
	scanner.Buffer(make([]byte, 0, 4*1024), 256*1024)
	var lines, bytes int
	for scanner.Scan() {
		// Do not log stderr text: agent may echo prompts, tool args, or paths.
		lines++
		bytes += len(scanner.Bytes())
	}
	if lines > 0 {
		slog.Debug("grokbuild: stderr closed", "lines", lines, "bytes", bytes)
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("grokbuild: stderr read error", "error_class", logErrorClass(err))
	}
}

// --- helpers ---

// callRPC registers a response waiter *before* writing the request so a fast
// local stdio agent cannot deliver a response that is dropped (audit P0-2).
func (s *grokSession) callRPC(id int, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	ch := make(chan *jsonrpcResponse, 1)
	s.respMu.Lock()
	s.respChannels[id] = ch
	s.respMu.Unlock()

	writeStart := time.Now()
	if err := s.writeRequest(id, method, params); err != nil {
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		slog.Warn("grokbuild: callRPC writeRequest failed",
			"method", method,
			"write_elapsed_ms", time.Since(writeStart).Milliseconds(),
			"error_class", rpcErrorClass(err))
		return nil, err
	}
	writeElapsed := time.Since(writeStart)

	var timer *time.Timer
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case resp := <-ch:
		slog.Info("grokbuild: callRPC response received",
			"method", method,
			"write_elapsed_ms", writeElapsed.Milliseconds(),
			"wait_elapsed_ms", time.Since(writeStart).Milliseconds())
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-timeoutCh:
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		slog.Warn("grokbuild: callRPC select timeout fired",
			"method", method,
			"write_elapsed_ms", writeElapsed.Milliseconds(),
			"total_elapsed_ms", time.Since(writeStart).Milliseconds())
		return nil, fmt.Errorf("timeout waiting for response to request %d", id)
	case <-s.ctx.Done():
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		slog.Warn("grokbuild: callRPC ctx done",
			"method", method,
			"write_elapsed_ms", writeElapsed.Milliseconds(),
			"total_elapsed_ms", time.Since(writeStart).Milliseconds())
		return nil, s.ctx.Err()
	}
}

// filterOsEnviron returns os.Environ() — separated for testability.
func filterOsEnviron() []string {
	return osEnviron()
}

// osEnviron is a seam for tests.
var osEnviron = func() []string {
	return syscall.Environ()
}

const (
	sigTERM = syscall.SIGTERM
	sigKILL = syscall.SIGKILL
)

func (s *grokSession) emit(ev core.Event) {
	if ev.SessionID == "" {
		ev.SessionID = s.CurrentSessionID()
	}
	// Only one Done terminal event per turn / process lifetime segment.
	if ev.Done {
		if !s.terminalDone.CompareAndSwap(false, true) {
			return
		}
	}

	// Diagnostic probe: if the events channel has no consumer (e.g. relayEvents
	// not yet started during handshake), s.events <- ev blocks here. Without a
	// timeout this would freeze readLoop and deadlock callRPC waiters.
	// We start a 100ms side-channel timer; if it fires before send completes,
	// we log a warning — then continue the original blocking send unchanged.
	chLen := len(s.events)
	chCap := cap(s.events)
	warnTimer := time.NewTimer(100 * time.Millisecond)
	defer warnTimer.Stop()
	warned := false
	delivered := false

	select {
	case s.events <- ev:
		delivered = true
	case <-warnTimer.C:
		warned = true
		slog.Warn("grokbuild: emit blocked >100ms (no consumer)",
			"event_type", ev.Type,
			"channel_len", chLen,
			"channel_cap", chCap)
		// Continue the original blocking wait — behavior unchanged.
		select {
		case s.events <- ev:
			delivered = true
		case <-s.ctx.Done():
			delivered = false
		}
	case <-s.ctx.Done():
		delivered = false
	}

	if warned {
		outcome := "cancelled"
		if delivered {
			outcome = "delivered"
		}
		slog.Info("grokbuild: emit resolved after delay",
			"event_type", ev.Type,
			"outcome", outcome)
	}
}

func (s *grokSession) writeRequest(id int, method string, params any) error {
	data, err := encodeRequest(id, method, params)
	if err != nil {
		return err
	}
	s.stdinMu.Lock()
	_, err = s.stdin.Write(data)
	s.stdinMu.Unlock()
	return err
}

// sendRequest is retained for tests that only need a fire-and-forget write.
func (s *grokSession) sendRequest(id int, method string, params any) error {
	return s.writeRequest(id, method, params)
}

func (s *grokSession) cleanup() {
	s.alive.Store(false)
	s.cancel()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// pendingPromptID tracks the session/prompt request ID so handleResponse
// can detect the turn-end response. Stored under pendingPermsMu.
// Declared as a field on grokSession — initialized in Send.
// (Using the mutex for simplicity since permissions and prompt tracking
// share the same critical section.)

// logErrorClass returns a short, non-sensitive label for logging.
func logErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	// Prefer type name; fall back to a truncated error string without payloads.
	msg := err.Error()
	if i := strings.Index(msg, ":"); i > 0 && i < 48 {
		return msg[:i]
	}
	if len(msg) > 64 {
		return msg[:64]
	}
	return msg
}

// rpcErrorClass returns a fixed safe category for RPC errors.
// The classification is based on our own wrapper text (e.g. "timeout waiting
// for response"), not on agent payload; only the fixed category constant is
// written to logs. Long-term this should use sentinel errors + errors.Is.
func rpcErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.Canceled) {
		return "context_cancelled"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout waiting for response"):
		return "rpc_timeout"
	case strings.HasPrefix(msg, "rpc error"):
		return "rpc_error"
	case strings.Contains(msg, "stdin") || strings.Contains(msg, "write"):
		return "write_failed"
	default:
		return "unknown"
	}
}

// shortID returns a log-safe prefix of a session id (or "empty").
func shortID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "empty"
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
