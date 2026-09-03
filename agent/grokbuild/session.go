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
	// handshaking spans process start → post-handshake drain. session/load
	// makes Grok replay historical session/update notifications while no
	// consumer is attached yet; a replay larger than the 64-slot events
	// channel used to freeze readLoop inside emit() and starve
	// callRPC(session/load) of its response (2026-08-14 deadlock on a 3232
	// line replay). While handshaking, emit discards overflow instead of
	// blocking — same semantics as the post-handshake drain (replay is
	// historical state and is discarded anyway), counted for observability.
	handshaking            atomic.Bool
	droppedHandshakeEvents atomic.Int64

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when process exits

	// ACP request ID counter
	idCounter requestIDCounter

	// pending permission requests: requestID -> options (for allow/deny lookup)
	pendingPermsMu sync.Mutex
	// pendingUserEcho buffers the identityless user prompt echo (codec
	// user_message_chunk carries no promptId by upstream design — grok-build
	// meta.rs user_message_chunk_meta stamps only promptIndex). Guarded by
	// pendingPermsMu: written by readLoop (emitTurnScoped), cleared by Send.
	pendingUserEcho string
	pendingPerms    map[string][]permissionOption
	pendingPromptID int // session/prompt request ID for turn-end detection

	// pendingQuestions registers agent-initiated x.ai/ask_user_question
	// reverse-requests on OUR OWN driven turns (the driver is the sole ACP
	// client, so the question arrives as a direct REQUEST, not a leader
	// broadcast). Keyed by tool_call_id — same identity the leader rail uses
	// (questionIDFor). Guarded by pendingPermsMu; cleared by Send (stale
	// questions from a finished turn must not survive into the next one).
	pendingQuestions map[string]*pendingAskUserQuestion

	// pending response matching: maps request ID → channel for synchronous waits
	respMu       sync.Mutex
	respChannels map[int]chan *jsonrpcResponse

	// ACP capabilities learned from initialize
	supportsLoadSession bool
	supportsListSession bool

	// appliedModel/appliedEffort track what model selection the session is
	// actually running (seeded from session/new|load result `models` truth,
	// updated by applyModelSelection). Guarded by pendingPermsMu (Send-time
	// drift check runs on the turn path; readLoop does not touch these).
	appliedModel  string
	appliedEffort string
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
		agent:            agent,
		cmd:              cmd,
		stdin:            stdin,
		stdout:           stdout,
		stderr:           stderr,
		events:           make(chan core.Event, 64),
		ctx:              sessionCtx,
		cancel:           cancel,
		done:             make(chan struct{}),
		pendingPerms:     make(map[string][]permissionOption),
		pendingQuestions: make(map[string]*pendingAskUserQuestion),
		respChannels:     make(map[int]chan *jsonrpcResponse),
	}
	// Store requested ID early; loadSession keeps it, newSession replaces it.
	s.sessionID.Store(sessionID)
	s.alive.Store(true)
	s.handshaking.Store(true)

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
	// Overflow beyond the channel capacity was already discarded by emit()'s
	// handshaking mode; both are reported together below.
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
	// Reset unconditionally: any Done seen during handshake was replay, and a
	// discarded replay Done would otherwise leave terminalDone set and
	// suppress the real turn's terminal event.
	s.terminalDone.Store(false)
	if dropped := s.droppedHandshakeEvents.Swap(0); drained > 0 || dropped > 0 {
		slog.Info("grokbuild: drained stale handshake events",
			"drained", drained,
			"discarded_overflow", dropped)
	}
	// End handshaking mode last: real turn events only begin after
	// StartSession returns, so nothing live can be discarded by this window.
	s.handshaking.Store(false)

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

	// Adopt the official model catalog (grok 1.0.13 `_meta.modelState`) so
	// AvailableModels/EffortsForModel stop guessing.
	if s.agent != nil && initResp.Meta != nil {
		s.agent.adoptModelCatalog(initResp.Meta.ModelState)
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
	// Explicit model/effort selection rides session/new `_meta` (official
	// headless semantics; grok warns and falls back to its default for an
	// unknown model id rather than failing the session).
	params.Meta = s.agent.sessionNewMeta()
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
	s.recordAppliedModelState(resp.Models)
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
	loadResultRaw, err := s.callRPC(id, "session/load", sessionLoadParams{
		SessionID:  sessionID,
		CWD:        cwd,
		McpServers: []any{}, // required empty array (same as session/new)
	}, 15*time.Second)
	if err != nil {
		return err
	}
	var loadResp sessionNewResult
	if err := json.Unmarshal(loadResultRaw, &loadResp); err != nil {
		// Non-fatal: load succeeded; the per-session models view is optional.
		slog.Debug("grokbuild: session/load result models unparseable", "error", err.Error())
	}
	// Align process workDir with the loaded session workspace.
	if s.agent != nil {
		s.agent.SetWorkDir(cwd)
	}
	if s.cmd != nil {
		s.cmd.Dir = cwd
	}
	s.sessionID.Store(sessionID)
	s.recordAppliedModelState(loadResp.Models)
	// session/load accepts no model params (session_lifecycle.rs consumes
	// none) — apply an explicit selection via session/set_model after load,
	// exactly like official headless apply_headless_model_and_effort. Soft
	// failure: the session itself is healthy; the transcript reports the
	// actual model via SessionModelSelectionReader.
	if err := s.applyModelSelection(); err != nil {
		slog.Warn("grokbuild: post-load model selection not applied",
			"session_id_prefix", shortID(sessionID),
			"error_class", rpcErrorClass(err))
	}
	slog.Info("grokbuild: session loaded", "id_prefix", shortID(sessionID), "cwd_base", filepath.Base(cwd))
	return nil
}

// recordAppliedModelState seeds appliedModel/appliedEffort from the
// session/new|load result `models` truth (grok 1.0.13 returns the per-session
// model state on both; load restores the persisted model/effort).
func (s *grokSession) recordAppliedModelState(ms *sessionModelState) {
	if ms == nil {
		return
	}
	s.pendingPermsMu.Lock()
	if ms.CurrentModelID != "" {
		s.appliedModel = ms.CurrentModelID
	}
	if ms.CurrentModelID != "" || s.appliedModel != "" {
		// Per-model current effort lives in the entry's meta (reasoningEffort
		// mirrors the session-level choice after session/new _meta / set_model).
		for i := range ms.AvailableModels {
			if ms.AvailableModels[i].ModelID == ms.CurrentModelID && ms.AvailableModels[i].Meta != nil {
				s.appliedEffort = ms.AvailableModels[i].Meta.ReasoningEffort
				break
			}
		}
	}
	s.pendingPermsMu.Unlock()
}

// applyModelSelection pushes the agent's explicit model/effort selection onto
// this live session via session/set_model (snake-case method on grok 1.0.13;
// modelId is server-required — an effort-only switch resends the session's
// current model). No-op when nothing explicit is set or nothing drifted.
func (s *grokSession) applyModelSelection() error {
	if s.agent == nil {
		return nil
	}
	model, effort := s.agent.explicitModelSelection()
	s.pendingPermsMu.Lock()
	appliedModel, appliedEffort := s.appliedModel, s.appliedEffort
	s.pendingPermsMu.Unlock()

	targetModel := model
	if targetModel == "" {
		// Effort-only switch: modelId is required on the wire; resend the
		// session's current model. Without a known current model there is no
		// honest value to send — skip (nothing explicit about the model).
		if effort == "" || effort == appliedEffort {
			return nil
		}
		targetModel = appliedModel
		if targetModel == "" {
			return nil
		}
	}
	// Gate the effort against the official catalog before the wire: an
	// iOS-side leftover effort (e.g. high lingering after switching to a
	// model without effort support) must be dropped here, not sent as an
	// invalid set_model (grok 1.0.13 answers -32602 and kills the turn).
	effort = s.agent.effectiveEffortForModel(targetModel, effort)
	if targetModel == appliedModel && (effort == "" || effort == appliedEffort) {
		return nil
	}

	params := sessionSetModelParams{SessionID: s.CurrentSessionID(), ModelID: targetModel}
	if effort != "" {
		params.Meta = &setModelMeta{ReasoningEffort: effort}
	}
	slog.Info("grokbuild: applying model selection via set_model",
		"session_id_prefix", shortID(s.CurrentSessionID()),
		"model", targetModel, "effort", effort,
		"applied_model", appliedModel, "applied_effort", appliedEffort)
	id := s.idCounter.next()
	if _, err := s.callRPC(id, "session/set_model", params, 15*time.Second); err != nil {
		// The official catalog accepts entry ids (e.g. "grok-4.5") but
		// persists the UNDERLYING model id ("glm-5.3") into summary.json,
		// which iOS echoes back as the selected model. That id is not a
		// catalog entry, so set_model answers -32602 "unknown model id".
		// The session is already on the model the user picked in that case —
		// kill the turn would block messaging entirely, so keep the session's
		// current model and let the turn go out (transcript truth keeps the
		// visible model honest). Any other failure stays hard.
		if strings.Contains(err.Error(), "unknown model id") {
			slog.Warn("grokbuild: set_model rejected with unknown model id; keeping session's current model and continuing turn",
				"session_id_prefix", shortID(s.CurrentSessionID()),
				"requested_model", targetModel, "error", err.Error())
			return nil
		}
		return err
	}
	s.pendingPermsMu.Lock()
	s.appliedModel = targetModel
	if effort != "" {
		s.appliedEffort = effort
	}
	s.pendingPermsMu.Unlock()
	slog.Info("grokbuild: model selection applied",
		"session_id_prefix", shortID(s.CurrentSessionID()),
		"model", targetModel, "effort", effort)
	return nil
}

// --- core.AgentSession ---

func (s *grokSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("grokbuild: session not alive")
	}

	// Mid-session model/effort switch (iOS changed the selection after this
	// session was spawned): push it via session/set_model before the turn.
	// Unlike the post-load path this is a hard failure — the user just chose
	// this model for the turn they are sending, so a silent fallback would
	// send the turn under a different model than requested.
	if err := s.applyModelSelection(); err != nil {
		return fmt.Errorf("grokbuild: apply model selection: %w", err)
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
	// A stale un-stamped echo from a previous turn must not leak into this one.
	s.pendingPermsMu.Lock()
	s.pendingPromptID = id
	s.pendingUserEcho = ""
	// Stale questions from a finished turn must not leak into the new one
	// (their wire ids are already dead — upstream drops late responses).
	staleQuestions := make([]string, 0, len(s.pendingQuestions))
	for toolCallID := range s.pendingQuestions {
		staleQuestions = append(staleQuestions, toolCallID)
	}
	s.pendingQuestions = make(map[string]*pendingAskUserQuestion)
	s.pendingPermsMu.Unlock()
	for _, toolCallID := range staleQuestions {
		markQuestionConsumed(toolCallID)
	}
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
	// "always" (opencode-web official reply) has no grok option; degrade to allow.
	if result.Behavior == "allow" || result.Behavior == "always" {
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

// pendingAskUserQuestion is one agent-initiated question interaction on the
// driver rail. Question identity mirrors the leader rail (questionIDFor) so
// iOS sees the same id shape regardless of which rail surfaced the question.
type pendingAskUserQuestion struct {
	rawID   json.RawMessage       // original request id to respond with
	params  askUserQuestionParams // parsed request (questions, mode)
	answers map[int][]string      // accumulated per-question selections
	notes   map[int]string        // per-question freeform text (annotations notes)
}

// consumedQuestions tombstones driver-rail interactions that were answered,
// cancelled, or cleared by a new turn, so a late iOS reply returns nil
// silently instead of erroring (red line 3 / research §3.5).
var (
	consumedQuestionsMu sync.Mutex
	consumedQuestions   = map[string]bool{}
)

func markQuestionConsumed(toolCallID string) {
	consumedQuestionsMu.Lock()
	defer consumedQuestionsMu.Unlock()
	if len(consumedQuestions) >= 256 {
		consumedQuestions = map[string]bool{}
	}
	consumedQuestions[toolCallID] = true
}

func questionWasConsumed(toolCallID string) bool {
	consumedQuestionsMu.Lock()
	defer consumedQuestionsMu.Unlock()
	return consumedQuestions[toolCallID]
}

func (s *grokSession) handleAskUserQuestionRequest(req *agentRequest) {
	var p askUserQuestionParams
	if err := json.Unmarshal(interactionInnerParams(req.Params), &p); err != nil || p.ToolCallID == "" || len(p.Questions) == 0 {
		slog.Warn("grokbuild: ask_user_question request unparseable", "toolCallId", p.ToolCallID)
		return
	}
	pending := &pendingAskUserQuestion{
		rawID:   append(json.RawMessage(nil), req.ID...),
		params:  p,
		answers: map[int][]string{},
	}
	s.pendingPermsMu.Lock()
	s.pendingQuestions[p.ToolCallID] = pending
	s.pendingPermsMu.Unlock()
	consumedQuestionsMu.Lock()
	delete(consumedQuestions, p.ToolCallID)
	consumedQuestionsMu.Unlock()

	for i, q := range p.Questions {
		opts := make([]core.QuestionOption, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, core.QuestionOption{ID: o.Label, Label: o.Label, Description: o.Description})
		}
		multi := q.MultiSelect != nil && *q.MultiSelect
		emitQuestionAsked(s.emit, s.CurrentSessionID(), questionIDFor(p.ToolCallID, i), q.Question, opts, multi)
	}
	slog.Info("grokbuild: ask_user_question pending", "toolCallId", p.ToolCallID, "questions", len(p.Questions))
}

// flushQuestionResponse removes the pending entry (if still present) and
// writes the JSON-RPC response to the agent's stdin.
func (s *grokSession) flushQuestionResponse(questionID string, result askUserQuestionExtResponse) error {
	toolCallID, _, err := parseQuestionID(questionID)
	if err != nil {
		return err
	}
	s.pendingPermsMu.Lock()
	pending, ok := s.pendingQuestions[toolCallID]
	delete(s.pendingQuestions, toolCallID)
	s.pendingPermsMu.Unlock()
	if !ok {
		if questionWasConsumed(toolCallID) {
			return nil // late reply to an already-consumed interaction — silent
		}
		return fmt.Errorf("grokbuild: no pending question %s", questionID)
	}
	markQuestionConsumed(toolCallID)
	if !s.alive.Load() {
		return fmt.Errorf("grokbuild: session not alive")
	}
	resp, err := encodeResponse(pending.rawID, result)
	if err != nil {
		return err
	}
	s.stdinMu.Lock()
	_, err = s.stdin.Write(resp)
	s.stdinMu.Unlock()
	if err != nil {
		return err
	}
	// Driver rail has no leader broadcast to close the card — the flush itself
	// is the authoritative outcome. accepted → answered, cancelled → rejected,
	// on both the canonical (v2 projection) and legacy (v1) faces.
	for i := range pending.params.Questions {
		emitQuestionResolved(s.emit, s.CurrentSessionID(), questionIDFor(toolCallID, i), result.Outcome, "ios")
	}
	return nil
}

// RespondQuestion answers an agent question with the selected option ids
// (grok labels). Multi-question interactions accumulate replies and flush the
// single wire response once every question is answered (upstream expects all
// answers in one response object). The legacy v1 rail carries no freeform
// text; typed answers ride respondQuestionWithNotes.
func (s *grokSession) RespondQuestion(questionID string, optionIDs []string) error {
	return s.respondQuestionWithNotes(questionID, optionIDs, "")
}

// respondQuestionWithNotes is RespondQuestion plus the freeform answer path:
// non-empty notes make the wire label "Other" a valid selection and flush as
// annotations[q].notes (the TUI "type your answer here" shape).
func (s *grokSession) respondQuestionWithNotes(questionID string, optionIDs []string, notes string) error {
	toolCallID, index, err := parseQuestionID(questionID)
	if err != nil {
		return err
	}
	s.pendingPermsMu.Lock()
	pending, ok := s.pendingQuestions[toolCallID]
	s.pendingPermsMu.Unlock()
	if !ok {
		if questionWasConsumed(toolCallID) {
			return nil // late reply to an already-answered interaction — silent
		}
		return fmt.Errorf("grokbuild: no pending question %s", questionID)
	}
	if index >= len(pending.params.Questions) {
		return fmt.Errorf("grokbuild: question index %d out of range for %s", index, questionID)
	}
	if len(optionIDs) == 0 {
		return fmt.Errorf("grokbuild: question reply carries no option")
	}
	valid := map[string]bool{}
	for _, o := range pending.params.Questions[index].Options {
		valid[o.Label] = true
	}
	for _, id := range optionIDs {
		if valid[id] {
			continue
		}
		if id == freeformOtherLabel && strings.TrimSpace(notes) != "" {
			continue
		}
		return fmt.Errorf("grokbuild: option %q is not one of the offered choices", id)
	}
	s.pendingPermsMu.Lock()
	pending.answers[index] = optionIDs
	if notes != "" {
		if pending.notes == nil {
			pending.notes = map[int]string{}
		}
		pending.notes[index] = notes
	}
	complete := len(pending.answers) >= len(pending.params.Questions)
	s.pendingPermsMu.Unlock()
	if !complete {
		return nil
	}
	answers := make(map[string][]string, len(pending.params.Questions))
	for i, q := range pending.params.Questions {
		if sel, ok := pending.answers[i]; ok && len(sel) > 0 {
			answers[q.Question] = sel
		}
	}
	var annotations map[string]askAnnotation
	for i, q := range pending.params.Questions {
		if n, ok := pending.notes[i]; ok && n != "" {
			if annotations == nil {
				annotations = make(map[string]askAnnotation, len(pending.params.Questions))
			}
			annotations[q.Question] = askAnnotation{Notes: n}
		}
	}
	return s.flushQuestionResponse(questionID, askUserQuestionExtResponse{Outcome: "accepted", Answers: answers, Annotations: annotations})
}

// RejectQuestion dismisses a pending question (upstream Path D: Cancelled is
// a user action, not an error — the turn completes normally).
func (s *grokSession) RejectQuestion(questionID string) error {
	return s.flushQuestionResponse(questionID, askUserQuestionExtResponse{Outcome: "cancelled"})
}

func (s *grokSession) hasPendingQuestion(toolCallID string) bool {
	s.pendingPermsMu.Lock()
	defer s.pendingPermsMu.Unlock()
	_, ok := s.pendingQuestions[toolCallID]
	return ok
}

var _ core.UserInputResponder = (*grokSession)(nil)

// ResolveUserInput is the session-level v2 responder for driver-rail
// questions (asked by this session's own stdio agent turn). A miss means the
// interaction belongs to the leader rail of an external turn — fall through to
// the agent-level responder, which routes via liveSubs. Typed text answers map
// to grok's freeform wire shape: label "Other" + annotations notes.
func (s *grokSession) ResolveUserInput(ctx context.Context, interactionID, _ string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	toolCallID, _, err := parseQuestionID(interactionID)
	if err != nil {
		return core.UserInputResolution{}, err
	}
	if !s.hasPendingQuestion(toolCallID) {
		if questionWasConsumed(toolCallID) {
			return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
		}
		if s.agent != nil {
			return s.agent.ResolveUserInput(ctx, interactionID, "", action, answers)
		}
		return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "question is not pending"}
	}
	if action == core.UserInputActionReject {
		if err := s.RejectQuestion(interactionID); err != nil {
			return core.UserInputResolution{}, err
		}
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusRejected}, nil
	}
	for _, ans := range answers {
		qid := ans.QuestionID
		if qid == "" {
			qid = interactionID
		}
		var selected []string
		notes := ""
		for _, v := range ans.Values {
			if v.Kind == core.UserInputValueText && strings.TrimSpace(v.Text) != "" {
				notes = strings.TrimSpace(v.Text)
				continue
			}
			if v.Kind == core.UserInputValueOption && v.OptionID != "" {
				selected = append(selected, v.OptionID)
			}
		}
		if notes != "" {
			// Freeform answer: append the wire "Other" label alongside any
			// picked options; the text rides annotations notes (TUI shape).
			selected = append(selected, freeformOtherLabel)
		}
		if err := s.respondQuestionWithNotes(qid, selected, notes); err != nil {
			return core.UserInputResolution{}, err
		}
	}
	if s.hasPendingQuestion(toolCallID) {
		// Multi-question interaction partially answered; the wire response
		// flushes (and resolved events emit) on the last answer.
		return core.UserInputResolution{Outcome: core.UserInputOutcomeInProgress, CurrentStatus: core.UserInputStatusPending}, nil
	}
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
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
	// Direct stdio rail: the agent addresses ext interaction methods to its
	// sole client (us). The leader rail's half-wrapped "_x." prefix may also
	// appear here; normalizeLeaderMethod tolerates both forms.
	switch normalizeLeaderMethod(req.Method, req.Params) {
	case "session/request_permission":
		s.handlePermissionRequest(req)
	case "x.ai/ask_user_question":
		s.handleAskUserQuestionRequest(req)
	default:
		slog.Debug("grokbuild: unhandled agent request", "method", req.Method)
	}
}

func (s *grokSession) handleNotification(notif *agentNotification) {
	switch notif.Method {
	case "session/update":
		events := convertSessionUpdate(notif.Params, s.CurrentSessionID())
		alreadyUsage := false
		refreshSignals := false
		for _, ev := range events {
			if ev.Type == core.EventContextUsageUpdated {
				alreadyUsage = true
			}
			if ev.Done {
				refreshSignals = true
			}
			s.emitTurnScoped(ev)
		}
		if !alreadyUsage && refreshSignals {
			if usage := loadGrokSignalsUsage(s.agent.grokHome, s.CurrentSessionID()); usage != nil {
				s.emit(core.Event{Type: core.EventContextUsageUpdated, ContextUsage: usage})
			}
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

// emitTurnScoped forwards session/update events, stamping turn identity onto the
// identityless user prompt echo. The upstream user_message_chunk carries no
// promptId (meta.rs user_message_chunk_meta: promptIndex/hideFromScrollback
// only), so the echo cannot be attributed when it arrives; the SSV2 reducer
// skips identityless user_message (projection_reducer.go "user_message" empty
// turnId return) and the sender's own prompt vanishes from the projection once
// iOS releases its optimistic local-send paint (owner 2026-09-02: iPhone 发送
// 的消息不显示，流式输出直接接在上个回复后面). The promptId lands with the
// first promptId-carrying event of the same turn (text/thought/tool/result),
// so the echo is buffered until then — the same reconstruction the leader
// observation loop (grokLeaderSessionRelayLoop pendingUserText) applies to
// external turns, here at the session layer for own turns. readLoop is the
// single writer of this path; Send clears the buffer for the next turn.
func (s *grokSession) emitTurnScoped(ev core.Event) {
	if ev.Type == core.EventUserMessage && ev.TurnID == "" {
		if text := strings.TrimSpace(ev.Content); text != "" {
			s.pendingPermsMu.Lock()
			s.pendingUserEcho = text
			s.pendingPermsMu.Unlock()
		}
		return
	}
	s.pendingPermsMu.Lock()
	echo := s.pendingUserEcho
	s.pendingPermsMu.Unlock()
	if echo != "" && ev.TurnID != "" {
		s.pendingPermsMu.Lock()
		s.pendingUserEcho = ""
		s.pendingPermsMu.Unlock()
		s.emit(core.Event{
			Type:    core.EventUserMessage,
			Content: echo,
			TurnID:  ev.TurnID,
			ItemID:  ev.TurnID,
		})
	}
	s.emit(ev)
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
		Type:      core.EventPermissionRequest,
		RequestID: reqIDStr,
		ToolName:  params.ToolCall.Title,
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
			if len(resp.Error.Data) > 0 {
				return nil, fmt.Errorf("rpc error %d: %s (%s)", resp.Error.Code, resp.Error.Message, string(resp.Error.Data))
			}
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

	// Handshake replay: non-blocking discard. Blocking here (pre-2026-08-14)
	// froze readLoop once the 64-slot channel filled and starved the pending
	// callRPC(session/load) of its response.
	//
	// Scope note (recorded 2026-08-15): EVERYTHING arriving during the handshake
	// window is treated as replayed historical state and may be discarded — by
	// this path and, before the fix, by the post-handshake drain. That includes
	// a `session/request_permission` notification replayed during session/load:
	// it is dropped, not surfaced. This is the window's intended semantics (the
	// drain comment explicitly discards replayed state "including any prior
	// error"); live permission requests belong to a started turn and arrive
	// after StartSession returns, outside this window.
	if s.handshaking.Load() {
		select {
		case s.events <- ev:
		default:
			s.droppedHandshakeEvents.Add(1)
		}
		return
	}

	// Diagnostic probe: if the events channel has no consumer (e.g. relayEvents
	// between turns), s.events <- ev blocks here. Without a
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
