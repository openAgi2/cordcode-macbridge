package dsh

// dshSession implements core.AgentSession for one DeepSeek Harness runtime
// process (`dsh-jsonrpc-agent <cordis.yml>`, design §1-2). One process serves
// one root session; the session id sent to session/prompt is generated and
// held by the driver (the SDK does not allocate it — §3.8 root session
// identity).

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.AgentSession = (*dshSession)(nil)

// errAttachmentsNotSupported is the driver-level defense-in-depth refusal for
// non-empty image/file slices (§3.9 path 4): the go-bridge pre-check already
// rejects attachments before StartSession; this keeps the driver itself from
// ever fabricating attachment delivery.
var errAttachmentsNotSupported = fmt.Errorf("dsh: attachments are not supported (text-only)")

const (
	eventsBufferSize  = 256
	stdoutLineLimit   = 8 << 20 // match gate0: envelopes with big request/header payloads
	promptReceiptWait = 60 * time.Second
	initializeWait    = 90 * time.Second
	gracefulStopWait  = 8 * time.Second
	killStopWait      = 5 * time.Second
)

type dshSession struct {
	agent *Agent

	// rootSessionID is the exact id the driver sends as
	// session/prompt.params.sessionId. Generated here; the SDK only echoes it.
	rootSessionID string

	// nonce is the per-spawn 16-byte CSPRNG hex; TurnIDs embed it so
	// projections from different process generations never collide (§3.6.3①).
	nonce string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	stdinMu sync.Mutex // serializes writes to stdin
	events  chan core.Event

	// eventsMu + eventsClosed serialize emit against the final close(s.events)
	// so a read-loop send can never race the exit watcher's close (which
	// would panic with a send-on-closed-channel).
	eventsMu     sync.Mutex
	eventsClosed bool

	alive        atomic.Bool
	terminalDone atomic.Bool // one Done terminal per turn segment; reset on Send

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the process exits

	idCounter atomic.Int64

	respMu       sync.Mutex
	respChannels map[int64]chan *jsonrpcFrame

	// codec is owned exclusively by the read loop goroutine.
	codec *dshCodec
	// scope routes notifications against the root session tree (§3.8); same
	// read-loop ownership as the codec.
	scope *scopeRouter
}

// newProcessNonce generates the 16-byte (128-bit) CSPRNG nonce for one spawn.
// rand.Read failure fails the spawn closed — no timestamp/zero fallback (§3.6.3①).
func newProcessNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("dsh: process nonce generation failed: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func newDshSession(ctx context.Context, agent *Agent, sessionID string) (*dshSession, error) {
	nonce, err := newProcessNonce()
	if err != nil {
		return nil, err
	}

	rootID := strings.TrimSpace(sessionID)
	if rootID == "" || strings.HasPrefix(rootID, "pending-") {
		// DSH has no resume (§4 live-only): a fresh root id is generated and
		// held by the driver for this process lifetime.
		rootID = fmt.Sprintf("dsh-%s", nonce[:16])
	}

	sessionCtx, cancel := context.WithCancel(ctx)

	var cmd *exec.Cmd
	if agent.nodeBin != "" {
		// Node-script mode (user-global dsh shadow tree or dev checkout):
		// `node [preArgs...] <script>` with the mode's spawn dir as cwd —
		// plugin resolution walks that tree's node_modules. Route 2 passes
		// the config via DSH_CORDIS_CONFIG (the upstream runner's preferred
		// channel); route 5 keeps the argv positional.
		args := append(append([]string{}, agent.scriptPreArgs...), agent.scriptPath)
		if !agent.configViaEnv {
			args = append(args, agent.cliExtraArgs...)
			args = append(args, agent.configPath)
		}
		cmd = exec.CommandContext(sessionCtx, agent.nodeBin, args...)
		cmd.Dir = agent.spawnDir
	} else {
		args := append([]string{agent.configPath}, agent.cliExtraArgs...)
		cmd = exec.CommandContext(sessionCtx, agent.cliBin, args...)
		cmd.Dir = agent.workDir
	}
	prepareCmdForProcessGroup(cmd)
	cmdEnv := agent.buildProcessEnv()
	if agent.configViaEnv {
		// Upstream runner contract: DSH_CORDIS_CONFIG wins over argv.
		cmdEnv = append(cmdEnv, "DSH_CORDIS_CONFIG="+agent.configPath)
	}
	cmd.Env = cmdEnv

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("dsh: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("dsh: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("dsh: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("dsh: start %s: %w", agent.cliBin, err)
	}

	s := &dshSession{
		agent:         agent,
		rootSessionID: rootID,
		nonce:         nonce,
		cmd:           cmd,
		stdin:         stdin,
		stdout:        stdout,
		stderr:        stderr,
		events:        make(chan core.Event, eventsBufferSize),
		ctx:           sessionCtx,
		cancel:        cancel,
		done:          make(chan struct{}),
		respChannels:  make(map[int64]chan *jsonrpcFrame),
		codec:         newCodec(nonce),
		scope:         newScopeRouter(rootID),
	}
	s.alive.Store(true)

	go s.readStderr()
	go s.readLoop()

	if err := s.initialize(); err != nil {
		s.teardownProcess()
		return nil, fmt.Errorf("dsh: initialize: %w", err)
	}
	slog.Info("dsh: session started",
		"pid", cmd.Process.Pid,
		"session_id_prefix", shortID(rootID),
		"nonce_prefix", nonce[:8])

	// Process exit watcher: unexpected death with an in-flight turn must
	// surface a visible terminal (§3.6.3② events-channel-close row).
	go func() {
		waitErr := cmd.Wait()
		close(s.done)
		s.alive.Store(false)
		if waitErr != nil && !s.terminalDone.Load() {
			s.emit(core.Event{
				Type:  core.EventError,
				Error: fmt.Errorf("dsh process exited: %v", waitErr),
				Done:  true,
			})
		}
		s.closeEvents()
	}()

	return s, nil
}

// --- handshake ---

func (s *dshSession) initialize() error {
	id := s.nextRequestID()
	result, err := s.callRPC(id, "initialize", initializeParams{
		CWD:      s.agent.workDir,
		Provider: dshProviderRoute,
		Model:    s.agent.GetModel(),
	}, initializeWait)
	if err != nil {
		return err
	}
	var initResp initializeResult
	if err := json.Unmarshal(result, &initResp); err != nil {
		return fmt.Errorf("decode initialize response: %w", err)
	}
	if initResp.ServerInfo.Name != dshServerInfoName {
		return fmt.Errorf("unexpected serverInfo.name %q (want %q)", initResp.ServerInfo.Name, dshServerInfoName)
	}
	return nil
}

// --- core.AgentSession ---

// Send queues one user turn via session/prompt and waits for the enqueue
// receipt ({messageId}). The receipt is NOT turn completion (§3.4); the turn
// terminates only on turn/end.
func (s *dshSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	// §3.9 defense-in-depth: DSH is text-only; the go-bridge pre-check rejects
	// attachments before StartSession, and the driver still refuses non-empty
	// slices rather than fabricating delivery.
	if len(images) > 0 || len(files) > 0 {
		return errAttachmentsNotSupported
	}
	if prompt == "" {
		return fmt.Errorf("dsh: empty prompt")
	}
	// Pre-send health check (§3.6.3② !Alive() row): provably zero bytes
	// written — the caller may CAS-evict, respawn, and send ONCE.
	if !s.alive.Load() {
		return &core.DeliveryError{
			Stage: core.StagePreWrite,
			Cause: fmt.Errorf("dsh: session not alive (pre-send)"),
		}
	}

	id := s.nextRequestID()
	s.terminalDone.Store(false)

	params := sessionPromptParams{
		SessionID:     s.rootSessionID,
		ContentBlocks: []promptContent{{Type: "text", Text: prompt}},
	}
	receiptWait := s.agent.receiptWait
	if receiptWait <= 0 {
		receiptWait = promptReceiptWait
	}
	if _, err := s.callRPC(id, "session/prompt", params, receiptWait); err != nil {
		var de *core.DeliveryError
		if errors.As(err, &de) {
			// Write-phase classification stands (zero-byte vs partial).
			return err
		}
		var re *rpcError
		if errors.As(err, &re) {
			// Definite server-side rejection: the prompt was NOT enqueued —
			// a plain error, no delivery uncertainty.
			return fmt.Errorf("dsh: session/prompt: %w", err)
		}
		// Wait-phase failure: the request was fully written but the receipt
		// never arrived. DSH enqueues the prompt BEFORE responding, so the
		// turn may already be running — replay is forbidden (§3.6.3③).
		return &core.DeliveryError{
			Stage: core.StageAwaitingResponse,
			Cause: fmt.Errorf("dsh: session/prompt: %w", err),
		}
	}
	return nil
}

func (s *dshSession) RespondPermission(requestID string, result core.PermissionResult) error {
	// DSH approvals do not traverse the driver protocol in phase 1 (§3.5 /
	// design risk 4: approval 不经协议).
	return core.ErrNotSupported
}

func (s *dshSession) Events() <-chan core.Event { return s.events }

func (s *dshSession) CurrentSessionID() string { return s.rootSessionID }

func (s *dshSession) Alive() bool { return s.alive.Load() }

func (s *dshSession) RespondQuestion(questionID string, optionIDs []string) error {
	return core.ErrNotSupported
}

func (s *dshSession) RejectQuestion(questionID string) error {
	return core.ErrNotSupported
}

// Close terminates the session in three phases (§5-7): graceful shutdown RPC
// → close stdin and wait → SIGTERM the process group → SIGKILL.
func (s *dshSession) Close() error {
	// Phase 1: graceful shutdown request (bounded — a wedged runtime must not
	// hang Close). A transport error here is not fatal: phases 2-3 still reap.
	done := make(chan struct{})
	go func() {
		id := s.nextRequestID()
		_, _ = s.callRPC(id, "shutdown", nil, gracefulStopWait)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(gracefulStopWait):
	}

	// Phase 2: close stdin, wait for exit.
	s.stdinMu.Lock()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	s.stdinMu.Unlock()
	select {
	case <-s.done:
		return nil
	case <-time.After(gracefulStopWait):
	}

	// Phase 3: SIGTERM then SIGKILL the process group.
	_ = signalProcessGroup(s.cmd, sigTERM)
	select {
	case <-s.done:
		return nil
	case <-time.After(killStopWait):
	}
	s.cancel()
	_ = signalProcessGroup(s.cmd, sigKILL)
	<-s.done
	return nil
}

// --- read loop ---

func (s *dshSession) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), stdoutLineLimit)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		s.handleLine(line)
	}
	if err := scanner.Err(); err != nil && s.alive.Load() {
		s.handleProtocolViolation(protocolViolationf("stdout read error: %v", err))
	}
	slog.Info("dsh: readLoop exited", "alive", s.alive.Load())
}

func (s *dshSession) handleLine(line []byte) {
	var frame jsonrpcFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		// Non-JSON line: the stream is polluted — fatal (§3.6.3②).
		s.handleProtocolViolation(protocolViolationf("non-JSON stdout line (%d bytes)", len(line)))
		return
	}

	switch {
	case frame.Method == "" && len(frame.ID) > 0:
		s.handleResponse(&frame)
	case frame.Method != "" && len(frame.ID) == 0:
		s.handleNotification(frame.Method, frame.Params)
	case frame.Method != "" && len(frame.ID) > 0:
		// Server→client request: the SDK runtime protocol (3 requests, all
		// client→server) defines none. Fail visibly instead of guessing.
		s.handleProtocolViolation(protocolViolationf("unexpected server request %q", frame.Method))
	default:
		s.handleProtocolViolation(protocolViolationf("malformed JSON-RPC frame (no id/method)"))
	}
}

func (s *dshSession) handleResponse(frame *jsonrpcFrame) {
	var idNum int64
	if err := json.Unmarshal(frame.ID, &idNum); err != nil {
		slog.Debug("dsh: response with non-numeric ID, ignoring")
		return
	}
	s.respMu.Lock()
	ch, ok := s.respChannels[idNum]
	if ok {
		delete(s.respChannels, idNum)
	}
	s.respMu.Unlock()
	if ok {
		ch <- frame
	}
}

// handleNotification routes the four server→client notifications through the
// §3.8 matrix. Session-scope routing runs BEFORE any root decode state is
// touched: descendant frames are filtered with diagnostics, foreign frames
// are protocol pollution and terminate the process (the driver keeps one root
// tree per process — a foreign id means the single-root invariant broke).
func (s *dshSession) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "session.event":
		var p sessionEventParams
		if err := json.Unmarshal(params, &p); err != nil {
			s.handleProtocolViolation(protocolViolationf("session.event params schema violation: %v", err))
			return
		}
		switch s.scope.classify(p.SessionID) {
		case scopeRoot:
			events, err := s.codec.apply(&p.Event)
			if err != nil {
				s.handleProtocolViolation(err)
				return
			}
			for _, ev := range events {
				s.emit(ev)
			}
		case scopeDescendant:
			// Child session content never enters the root codec: its seq
			// does not advance root expectedSeq and its turn frames never
			// touch the root active-turn state machine.
			slog.Debug("dsh: filtered descendant session.event",
				"scope_session", shortID(p.SessionID), "type", p.Event.Type)
		default:
			s.handleProtocolViolation(protocolViolationf(
				"foreign session.event from %q (type %s) — outside the root tree", p.SessionID, p.Event.Type))
		}

	case "session.status":
		// Driver-internal liveness only (§3.4): never a core.Event, never a
		// turn terminal, never projected to iOS. Cross-checked against
		// turn/end for diagnostics.
		var p sessionStatusParams
		if err := json.Unmarshal(params, &p); err != nil {
			s.handleProtocolViolation(protocolViolationf("session.status params schema violation: %v", err))
			return
		}
		switch s.scope.classify(p.SessionID) {
		case scopeRoot:
			slog.Debug("dsh: root session.status (driver-internal liveness)", "status", p.Status)
		case scopeDescendant:
			// Child idle must never close the parent turn.
			slog.Debug("dsh: filtered descendant session.status",
				"scope_session", shortID(p.SessionID), "status", p.Status)
		default:
			s.handleProtocolViolation(protocolViolationf(
				"foreign session.status from %q (status %s) — outside the root tree", p.SessionID, p.Status))
		}

	case "subagent.started":
		var p subagentStartedParams
		if err := json.Unmarshal(params, &p); err != nil {
			s.handleProtocolViolation(protocolViolationf("subagent.started params schema violation: %v", err))
			return
		}
		s.scope.recordStarted(p.ParentSessionID, p.ChildSessionID)

	case "subagent.finished":
		var p subagentFinishedParams
		if err := json.Unmarshal(params, &p); err != nil {
			s.handleProtocolViolation(protocolViolationf("subagent.finished params schema violation: %v", err))
			return
		}
		s.scope.recordFinished(p.ParentSessionID, p.ChildSessionID)

	default:
		// Unknown notification method: not part of the pinned wire surface
		// (§3.0). Treat as protocol drift and fail visibly.
		s.handleProtocolViolation(protocolViolationf("unknown notification method %q", method))
	}
}

// handleProtocolViolation is the fatal path (§3.6.3②): emit a visible
// terminal if a turn may be in flight, then stop the process. The decoder is
// polluted; it must not serve another turn.
func (s *dshSession) handleProtocolViolation(err error) {
	slog.Error("dsh: protocol violation", "error", err.Error())
	if !s.terminalDone.Load() {
		s.emit(core.Event{
			Type:  core.EventError,
			Error: err,
			Done:  true,
		})
	}
	s.teardownProcess()
}

// teardownProcess kills the runtime process without waiting on Close().
func (s *dshSession) teardownProcess() {
	s.cancel()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = signalProcessGroup(s.cmd, sigKILL)
	}
}

func (s *dshSession) readStderr() {
	scanner := bufio.NewScanner(s.stderr)
	scanner.Buffer(make([]byte, 0, 4*1024), 256*1024)
	var lines, bytes int
	for scanner.Scan() {
		// Never log stderr text: the runtime may echo prompts, tool args, or paths.
		lines++
		bytes += len(scanner.Bytes())
	}
	if lines > 0 {
		slog.Debug("dsh: stderr closed", "lines", lines, "bytes", bytes)
	}
}

// --- JSON-RPC plumbing ---

// rpcError is a definite JSON-RPC error RESPONSE from the server: the request
// reached the runtime and was rejected there. Unlike a *core.DeliveryError it
// carries no delivery uncertainty — the prompt was not enqueued — so the
// caller fails visibly without transport-classification semantics.
type rpcError struct {
	Code    int
	Message string
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

func (s *dshSession) nextRequestID() int64 { return s.idCounter.Add(1) }

// callRPC registers the response waiter BEFORE writing, so a fast stdio peer
// cannot race the registration (gate0 round-3 lesson).
func (s *dshSession) callRPC(id int64, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	ch := make(chan *jsonrpcFrame, 1)
	s.respMu.Lock()
	s.respChannels[id] = ch
	s.respMu.Unlock()

	if err := s.writeRequest(id, method, params); err != nil {
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case frame := <-ch:
		if frame.Error != nil {
			return nil, &rpcError{Code: frame.Error.Code, Message: frame.Error.Message}
		}
		return frame.Result, nil
	case <-timer.C:
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to %s (id %d)", method, id)
	case <-s.ctx.Done():
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		return nil, s.ctx.Err()
	case <-s.done:
		s.respMu.Lock()
		delete(s.respChannels, id)
		s.respMu.Unlock()
		return nil, fmt.Errorf("dsh process exited while waiting for %s (id %d)", method, id)
	}
}

// failPendingWaiters is folded into callRPC's <-s.done select arm: waiters are
// released by the exit watcher closing s.done, keeping a single error wording.

func (s *dshSession) writeRequest(id int64, method string, params any) error {
	obj := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		obj["params"] = params
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	n, err := s.stdin.Write(append(data, '\n'))
	if err != nil || n != len(data)+1 {
		// Delivery classification (§3.6.3③): zero bytes moved = provably
		// undelivered (PreWrite, repairable); any byte written = the request
		// may have been delivered (PartialWrite, no replay).
		stage := core.StagePartialWrite
		if n == 0 {
			stage = core.StagePreWrite
		}
		return &core.DeliveryError{
			Stage: stage,
			Cause: fmt.Errorf("%s write: %d of %d bytes: %v", method, n, len(data)+1, err),
		}
	}
	return nil
}

func (s *dshSession) emit(ev core.Event) {
	if ev.SessionID == "" {
		ev.SessionID = s.rootSessionID
	}
	// One Done terminal per turn segment: turn_error must not be duplicated
	// by a later process-exit terminal (and vice versa).
	if ev.Done {
		if !s.terminalDone.CompareAndSwap(false, true) {
			return
		}
	}
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if s.eventsClosed {
		return
	}
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

// closeEvents seals the events channel exactly once, after every emitter has
// quiesced (mutual exclusion with emit).
func (s *dshSession) closeEvents() {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if !s.eventsClosed {
		s.eventsClosed = true
		close(s.events)
	}
}

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
