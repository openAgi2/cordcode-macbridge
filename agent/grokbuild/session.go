package grokbuild

// grokSession implements core.AgentSession for a single Grok Build CLI process
// running in ACP stdio mode (`grok agent stdio`).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.AgentSession = (*grokSession)(nil)

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

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when process exits

	// ACP request ID counter
	idCounter requestIDCounter

	// pending permission requests: requestID -> options (for allow/deny lookup)
	pendingPermsMu sync.Mutex
	pendingPerms   map[string][]permissionOption
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
	s.sessionID.Store(sessionID)
	s.alive.Store(true)

	// Start stderr reader (logs only, no events).
	go s.readStderr()

	// Start stdout reader (ACP JSON-RPC messages → events).
	go s.readLoop()

	// Perform ACP initialization handshake.
	if err := s.initialize(); err != nil {
		s.cleanup()
		return nil, fmt.Errorf("grokbuild: initialize: %w", err)
	}

	// Create or load the session.
	if sessionID != "" && s.supportsLoadSession {
		if err := s.loadSession(sessionID); err != nil {
			slog.Warn("grokbuild: session/load failed, creating new", "id", sessionID, "error", err)
			if err := s.newSession(); err != nil {
				s.cleanup()
				return nil, fmt.Errorf("grokbuild: session/new after load failure: %w", err)
			}
		}
	} else {
		if err := s.newSession(); err != nil {
			s.cleanup()
			return nil, fmt.Errorf("grokbuild: session/new: %w", err)
		}
	}

	// Wait for process exit in background.
	go func() {
		_ = cmd.Wait()
		close(s.done)
		s.alive.Store(false)
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
	if err := s.sendRequest(id, "initialize", params); err != nil {
		return err
	}

	// Wait for the response.
	result, err := s.waitForResponse(id, 10*1e9) // 10s timeout
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
	if err := s.sendRequest(id, "authenticate", authenticateParams{MethodID: method}); err != nil {
		return err
	}
	_, err := s.waitForResponse(id, 30*1e9) // 30s for interactive auth
	return err
}

func (s *grokSession) newSession() error {
	id := s.idCounter.next()
	params := sessionNewParams{
		CWD:        s.agent.workDir,
		McpServers: []any{}, // empty array — no MCP servers
	}
	if err := s.sendRequest(id, "session/new", params); err != nil {
		return err
	}
	result, err := s.waitForResponse(id, 15*1e9)
	if err != nil {
		return err
	}
	var resp sessionNewResult
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("decode session/new response: %w", err)
	}
	if resp.SessionID != "" {
		s.sessionID.Store(resp.SessionID)
		slog.Info("grokbuild: session created", "id", resp.SessionID)
	}
	return nil
}

func (s *grokSession) loadSession(sessionID string) error {
	id := s.idCounter.next()
	if err := s.sendRequest(id, "session/load", sessionLoadParams{SessionID: sessionID}); err != nil {
		return err
	}
	_, err := s.waitForResponse(id, 15*1e9)
	if err == nil {
		s.sessionID.Store(sessionID)
		slog.Info("grokbuild: session loaded", "id", sessionID)
	}
	return err
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
	// Emit turn_started before sending.
	s.emit(core.Event{Type: core.EventTurnStarted})

	if err := s.sendRequest(id, "session/prompt", sessionPromptParams{
		SessionID: s.CurrentSessionID(),
		Prompt:    content,
	}); err != nil {
		return err
	}

	// The response will arrive asynchronously and be handled in readLoop.
	// We store the pending prompt ID so readLoop can emit EventResult when
	// the matching response arrives.
	s.pendingPermsMu.Lock()
	s.pendingPromptID = id
	s.pendingPermsMu.Unlock()

	return nil
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
	s.signalProcessGroup(sigTERM)

	select {
	case <-s.done:
		return nil
	case <-time.After(5 * time.Second):
	}

	// Phase 3: SIGKILL.
	s.cancel()
	s.signalProcessGroup(sigKILL)
	<-s.done
	return nil
}

// --- read loop ---

func (s *grokSession) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		s.handleMessage(line)
	}
	if err := scanner.Err(); err != nil && s.alive.Load() {
		s.emit(core.Event{
			Type:    core.EventError,
			Content: fmt.Sprintf("stdout read error: %v", err),
			Done:    true,
		})
	}
	// Process exited or stdout EOF.
	s.alive.Store(false)
}

func (s *grokSession) handleMessage(line []byte) {
	resp, req, notif, err := decodeMessage(line)
	if err != nil {
		slog.Warn("grokbuild: decode failed", "error", err, "line", string(line))
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
	for scanner.Scan() {
		slog.Debug("grokbuild: stderr", "line", scanner.Text())
	}
}

// --- helpers ---

// waitForResponse blocks until the response for the given request ID arrives.
// timeoutNs is in nanoseconds; 0 means no timeout (not recommended).
func (s *grokSession) waitForResponse(id int, timeoutNs int64) (json.RawMessage, error) {
	ch := make(chan *jsonrpcResponse, 1)
	s.respMu.Lock()
	s.respChannels[id] = ch
	s.respMu.Unlock()

	var timer *time.Timer
	var timeoutCh <-chan time.Time
	if timeoutNs > 0 {
		timer = time.NewTimer(time.Duration(timeoutNs))
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-timeoutCh:
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to request %d", id)
	case <-s.ctx.Done():
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		return nil, s.ctx.Err()
	}
}

// signalProcessGroup sends a signal to the entire process group.
func (s *grokSession) signalProcessGroup(sig syscall.Signal) {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	// Signal the process group (negative PID).
	_ = syscall.Kill(-s.cmd.Process.Pid, sig)
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
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *grokSession) sendRequest(id int, method string, params any) error {
	data, err := encodeRequest(id, method, params)
	if err != nil {
		return err
	}
	s.stdinMu.Lock()
	_, err = s.stdin.Write(data)
	s.stdinMu.Unlock()
	return err
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
