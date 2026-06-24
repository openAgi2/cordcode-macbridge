package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

type rpcResponseEnvelope struct {
	ID     any             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcNotificationEnvelope struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type threadStartResponse struct {
	Cwd             string  `json:"cwd"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort"`
	Thread          struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type threadResumeResponse struct {
	Cwd             string  `json:"cwd"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort"`
	Thread          struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type turnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"turn"`
}

type itemNotification struct {
	ThreadID string         `json:"threadId"`
	TurnID   string         `json:"turnId"`
	Item     map[string]any `json:"item"`
}

type planNotification struct {
	ThreadID    string           `json:"threadId"`
	TurnID      string           `json:"turnId"`
	Explanation *string          `json:"explanation"`
	Plan        []codexPlanEntry `json:"plan"`
}

type errorNotification struct {
	Message string `json:"message"`
}

type questionNotification struct {
	ID       any              `json:"id"`
	Question any              `json:"question"`
	Options  []map[string]any `json:"options"`
	Required any              `json:"required"`
	ThreadID any              `json:"threadId"`
}

type appServerRateLimitsResponse struct {
	RateLimits          appServerRateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID map[string]appServerRateLimitSnapshot `json:"rateLimitsByLimitId"`
}

type appServerRateLimitSnapshot struct {
	LimitID   string                    `json:"limitId"`
	LimitName string                    `json:"limitName"`
	PlanType  string                    `json:"planType"`
	Primary   *appServerRateLimitWindow `json:"primary"`
	Secondary *appServerRateLimitWindow `json:"secondary"`
	Credits   *appServerCreditsSnapshot `json:"credits"`
}

type appServerRateLimitWindow struct {
	UsedPercent        int   `json:"usedPercent"`
	WindowDurationMins int   `json:"windowDurationMins"`
	ResetsAt           int64 `json:"resetsAt"`
}

type appServerCreditsSnapshot struct {
	Balance    *string `json:"balance"`
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
}

type appServerSession struct {
	url       string
	transport string
	workDir   string
	model     string
	effort    string
	mode      string
	extraEnv  []string
	codexHome string

	events chan core.Event

	ctx    context.Context
	cancel context.CancelFunc

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	conn    *websocket.Conn
	procMu  sync.Mutex
	writeMu sync.Mutex

	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponseEnvelope

	threadID atomic.Value
	alive    atomic.Bool

	closeOnce sync.Once
	wg        sync.WaitGroup

	stateMu     sync.Mutex
	pendingMsgs []string
	currentTurn string

	sawDelta sync.Map // key: itemId (string), value: bool — 标记已收到 delta 的 item

	runtimeMu sync.RWMutex
	usage     *core.UsageReport
	context   *core.ContextUsage
}

const (
	appServerRequestTimeout      = 120 * time.Second
	appServerUsageRefreshTimeout = 1500 * time.Millisecond

	appServerTransportStdio     = "stdio"
	appServerTransportWebSocket = "websocket"
)

func newAppServerSession(ctx context.Context, url, transport, workDir, model, effort, mode, resumeID string, extraEnv []string, codexHome string) (*appServerSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)
	if transport == "" {
		transport = appServerTransportStdio
	}
	s := &appServerSession{
		url:       url,
		transport: transport,
		workDir:   workDir,
		model:     model,
		effort:    effort,
		mode:      mode,
		extraEnv:  append([]string(nil), extraEnv...),
		codexHome: strings.TrimSpace(codexHome),
		events:    make(chan core.Event, 128),
		ctx:       sessionCtx,
		cancel:    cancel,
		pending:   make(map[int64]chan rpcResponseEnvelope),
	}
	s.alive.Store(true)

	slog.Info("codex app-server: creating new session",
		"url", url,
		"transport", transport,
		"workDir", workDir,
		"model", model,
		"resumeID", resumeID,
	)

	if err := s.connect(); err != nil {
		cancel()
		return nil, err
	}

	if err := s.initialize(); err != nil {
		_ = s.Close()
		return nil, err
	}

	if err := s.ensureThread(resumeID); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.refreshUsage(context.Background()); err != nil {
		slog.Debug("codex app-server: initial rate limit fetch failed", "error", err)
	}

	return s, nil
}

func (s *appServerSession) commandArgs() []string {
	return []string{"app-server"}
}

func (s *appServerSession) connect() error {
	if s.transport == appServerTransportWebSocket {
		return s.connectWebSocket()
	}
	return s.connectStdio()
}

func (s *appServerSession) connectStdio() error {
	args := s.commandArgs()
	cmd := exec.CommandContext(s.ctx, "codex", args...)
	cmd.Dir = s.workDir
	sessionEnv := append([]string(nil), s.extraEnv...)
	if s.codexHome != "" {
		sessionEnv = append(sessionEnv, "CODEX_HOME="+s.codexHome)
	}
	// Controlled agent env: always set (previously only set when env was
	// non-empty, which let a subprocess with no extra env inherit the full
	// supervisor env verbatim — the CCCODE_* control-plane leak).
	cmd.Env = core.BuildAgentEnv(
		core.FilterEnvToAllowlist(os.Environ(), core.AgentEnvRuntimeAllowlist()),
		nil,
		sessionEnv,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex app-server stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex app-server stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("codex app-server stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codex app-server start: %w", err)
	}

	s.procMu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.procMu.Unlock()

	slog.Info("codex app-server session started", "transport", "stdio", "pid", cmd.Process.Pid, "work_dir", s.workDir)

	s.wg.Add(3)
	go s.readLoop(stdout)
	go s.stderrLoop(stderr)
	go s.waitLoop()
	return nil
}

func (s *appServerSession) connectWebSocket() error {
	wsURL := strings.TrimSpace(s.url)
	if wsURL == "" {
		return fmt.Errorf("codex app-server websocket URL is empty")
	}
	if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	} else if strings.HasPrefix(wsURL, "https://") {
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(s.ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("codex app-server ws dial %s: %w", wsURL, err)
	}

	s.procMu.Lock()
	s.conn = conn
	s.procMu.Unlock()

	slog.Info("codex app-server session connected", "transport", "websocket", "url", wsURL, "work_dir", s.workDir)

	s.wg.Add(1)
	go s.wsReadLoop()
	return nil
}

func (s *appServerSession) initialize() error {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    "cc-connect-codex-agent",
			"title":   "CC Connect Codex Agent",
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
	}

	var resp initResponse
	if err := s.request("initialize", params, &resp); err != nil {
		return fmt.Errorf("codex app-server initialize: %w", err)
	}
	if err := s.notify("initialized", nil); err != nil {
		return fmt.Errorf("codex app-server initialized notify: %w", err)
	}
	return nil
}

func (s *appServerSession) ensureThread(resumeID string) error {
	if resumeID != "" && resumeID != core.ContinueSession {
		params := s.threadRequestParams()
		params["threadId"] = resumeID
		params["persistExtendedHistory"] = true

		slog.Info("codex app-server: calling thread/resume", "threadID", resumeID)

		var resp threadResumeResponse
		if err := s.request("thread/resume", params, &resp); err != nil {
			slog.Warn("codex app-server: thread/resume failed, falling back to thread/start", "threadID", resumeID, "error", err)
			return s.ensureThread("")
		}
		if resp.Thread.ID == "" {
			slog.Warn("codex app-server: thread/resume returned empty id, falling back to thread/start", "threadID", resumeID)
			return s.ensureThread("")
		}
		s.applyThreadRuntimeState(resp.Cwd, resp.Model, resp.ReasoningEffort)
		s.threadID.Store(resp.Thread.ID)
		slog.Info("codex app-server thread resumed", "thread_id", resp.Thread.ID)
		return nil
	}

	slog.Info("codex app-server: calling thread/start")
	var resp threadStartResponse
	if err := s.request("thread/start", s.threadRequestParams(), &resp); err != nil {
		slog.Error("codex app-server: thread/start failed", "error", err)
		return err
	}
	if resp.Thread.ID == "" {
		return fmt.Errorf("codex app-server start returned empty thread id")
	}
	s.applyThreadRuntimeState(resp.Cwd, resp.Model, resp.ReasoningEffort)
	s.threadID.Store(resp.Thread.ID)
	slog.Info("codex app-server thread started", "thread_id", resp.Thread.ID)
	return nil
}

func (s *appServerSession) threadRequestParams() map[string]any {
	params := map[string]any{
		"experimentalRawEvents":  false,
		"persistExtendedHistory": false,
		"config": map[string]any{
			"truncation_policy": map[string]any{
				"mode":  "tokens",
				"limit": 1000000,
			},
		},
	}
	if model := s.GetModel(); model != "" {
		params["model"] = model
	}
	if approval, sandbox := appServerModeSettings(s.mode); approval != "" {
		params["approvalPolicy"] = approval
		if sandbox != "" {
			params["sandbox"] = sandbox
		}
	}
	return params
}

func appServerModeSettings(mode string) (approval string, sandbox string) {
	switch normalizeMode(mode) {
	case "custom":
		return "", ""
	case "auto-review":
		return "on-failure", "workspace-write"
	case "full-access":
		return "never", "danger-full-access"
	default:
		return "on-request", "workspace-write"
	}
}

func (s *appServerSession) applyThreadRuntimeState(workDir, model string, effort *string) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if dir := strings.TrimSpace(workDir); dir != "" {
		s.workDir = dir
	}
	if m := strings.TrimSpace(model); m != "" {
		s.model = m
	}
	s.effort = normalizeRuntimeReasoningEffort(stringValue(effort))
}

func (s *appServerSession) refreshUsage(ctx context.Context) error {
	timeout := appServerUsageRefreshTimeout
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			if until := time.Until(deadline); until > 0 && until < timeout {
				timeout = until
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if timeout <= 0 {
		return context.DeadlineExceeded
	}

	var resp appServerRateLimitsResponse
	if err := s.requestWithTimeout("account/rateLimits/read", map[string]any{}, &resp, timeout); err != nil {
		return err
	}
	s.storeUsage(mapAppServerRateLimits(resp))
	return nil
}

func (s *appServerSession) cachedUsage() *core.UsageReport {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return cloneUsageReport(s.usage)
}

func (s *appServerSession) cachedContextUsage() *core.ContextUsage {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return cloneContextUsage(s.context)
}

func (s *appServerSession) storeUsage(report *core.UsageReport) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.usage = cloneUsageReport(report)
}

func (s *appServerSession) storeContextUsage(usage *core.ContextUsage) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.context = cloneContextUsage(usage)
}

func (s *appServerSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is closed")
	}

	if len(files) > 0 {
		filePaths := core.SaveFilesToDisk(s.workDir, files)
		prompt = core.AppendFileRefs(prompt, filePaths)
	}

	prompt, imagePaths, err := s.stageImages(prompt, images)
	if err != nil {
		return err
	}

	threadID := s.CurrentSessionID()
	if threadID == "" {
		return fmt.Errorf("codex app-server thread id is empty")
	}

	input := make([]map[string]any, 0, 1+len(imagePaths))
	input = append(input, map[string]any{
		"type":          "text",
		"text":          prompt,
		"text_elements": []any{},
	})
	for _, path := range imagePaths {
		input = append(input, map[string]any{
			"type": "localImage",
			"path": path,
		})
	}

	params := map[string]any{
		"threadId": threadID,
		"input":    input,
	}
	if model := s.GetModel(); model != "" {
		params["model"] = model
	}
	if effort := s.GetReasoningEffort(); effort != "" {
		params["effort"] = effort
	}
	if approval, _ := appServerModeSettings(s.mode); approval != "" {
		params["approvalPolicy"] = approval
	}

	var resp turnStartResponse
	if err := s.request("turn/start", params, &resp); err != nil {
		return fmt.Errorf("codex app-server turn/start: %w", err)
	}
	if resp.Turn.ID == "" {
		return fmt.Errorf("codex app-server turn/start returned empty turn id")
	}

	s.stateMu.Lock()
	s.currentTurn = resp.Turn.ID
	s.pendingMsgs = s.pendingMsgs[:0]
	s.stateMu.Unlock()

	return nil
}

func (s *appServerSession) stageImages(prompt string, images []core.ImageAttachment) (string, []string, error) {
	if len(images) == 0 {
		return prompt, nil, nil
	}

	imgDir := filepath.Join(s.workDir, ".cc-connect", "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("codex app-server: create image dir: %w", err)
	}

	imagePaths := make([]string, 0, len(images))
	for i, img := range images {
		ext := codexImageExt(img.MimeType)
		fname := fmt.Sprintf("img_%d_%d%s", time.Now().UnixMilli(), i, ext)
		fpath := filepath.Join(imgDir, fname)
		if err := os.WriteFile(fpath, img.Data, 0o644); err != nil {
			return "", nil, fmt.Errorf("codex app-server: save image: %w", err)
		}
		imagePaths = append(imagePaths, fpath)
	}

	if strings.TrimSpace(prompt) == "" {
		prompt = "Please analyze the attached image(s)."
	}

	return prompt, imagePaths, nil
}

func (s *appServerSession) RespondPermission(_ string, _ core.PermissionResult) error {
	return nil
}

func (s *appServerSession) RespondQuestion(questionID string, optionIDs []string) error {
	if questionID == "" {
		return fmt.Errorf("codex appserver: questionID is required")
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "question/reply",
		"params": map[string]any{
			"questionId": questionID,
			"optionIds":  optionIDs,
		},
	}
	if err := s.writeJSON(payload); err != nil {
		return fmt.Errorf("codex appserver: question reply failed: %w", err)
	}
	s.emit(core.Event{
		Type:       core.EventQuestionResolved,
		QuestionID: questionID,
		Content:    "replied",
	})
	return nil
}

func (s *appServerSession) RejectQuestion(questionID string) error {
	if questionID == "" {
		return fmt.Errorf("codex appserver: questionID is required")
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "question/reject",
		"params": map[string]any{
			"questionId": questionID,
		},
	}
	if err := s.writeJSON(payload); err != nil {
		return fmt.Errorf("codex appserver: question reject failed: %w", err)
	}
	s.emit(core.Event{
		Type:       core.EventQuestionResolved,
		QuestionID: questionID,
		Content:    "rejected",
	})
	return nil
}

// CancelTurn sends a turn/interrupt JSON-RPC request to the codex app-server.
// This is a best-effort cancel; errors are logged but not propagated
// because the caller will close the session regardless.
func (s *appServerSession) CancelTurn(ctx context.Context) error {
	s.stateMu.Lock()
	turnID := s.currentTurn
	s.stateMu.Unlock()

	if turnID == "" {
		slog.Debug("codex app-server: CancelTurn skipped, no active turn")
		return nil
	}

	threadID := s.CurrentSessionID()
	params := map[string]any{}
	if threadID != "" {
		params["threadId"] = threadID
	}
	if turnID != "" {
		params["turnId"] = turnID
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := s.requestWithTimeout("turn/interrupt", params, nil, 5*time.Second); err != nil {
		slog.Warn("codex app-server turn/interrupt failed", "turn_id", turnID, "error", err)
		return err
	}

	slog.Info("codex app-server turn interrupted", "turn_id", turnID)
	return nil
}

func (s *appServerSession) Events() <-chan core.Event {
	return s.events
}

func (s *appServerSession) CurrentSessionID() string {
	v, _ := s.threadID.Load().(string)
	return v
}

func (s *appServerSession) GetWorkDir() string {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.workDir
}

func (s *appServerSession) GetModel() string {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return strings.TrimSpace(s.model)
}

func (s *appServerSession) GetReasoningEffort() string {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return strings.TrimSpace(s.effort)
}

func (s *appServerSession) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	if err := s.refreshUsage(ctx); err != nil {
		if cached := s.cachedUsage(); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	if cached := s.cachedUsage(); cached != nil {
		return cached, nil
	}
	return nil, fmt.Errorf("codex app-server usage unavailable")
}

func (s *appServerSession) GetContextUsage() *core.ContextUsage {
	return s.cachedContextUsage()
}

func (s *appServerSession) Alive() bool {
	return s.alive.Load()
}

func (s *appServerSession) Close() error {
	s.alive.Store(false)
	s.cancel()

	s.procMu.Lock()
	if s.conn != nil {
		_ = s.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(2*time.Second),
		)
		_ = s.conn.Close()
		s.conn = nil
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	s.procMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// readLoop has exited; safe to close events exactly once.
		s.closeOnce.Do(func() {
			close(s.events)
		})
	case <-time.After(2 * time.Second):
		// Do NOT close(s.events) here: a producer goroutine may still be
		// running and a subsequent emit() would panic on a closed-channel send
		// (emit's default branch does NOT prevent that panic). We already
		// killed the process above; defer the events close until wg exits.
		slog.Debug("codex app-server: close timed out, deferring events close until readLoop exits")
		s.closeOnce.Do(func() {
			go func() {
				<-done
				close(s.events)
			}()
		})
	}
	return nil
}

func (s *appServerSession) readLoop(r io.Reader) {
	defer s.wg.Done()
	scanner := bufio.NewScanner(r)
	scanBuf := make([]byte, 0, 64*1024)
	const maxLineSize = 10 * 1024 * 1024 // 10MB
	scanner.Buffer(scanBuf, maxLineSize)

	for scanner.Scan() {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		data := scanner.Bytes()
		s.handleRPCMessage(data)
	}

	err := scanner.Err()
	if err != nil {
		if s.ctx.Err() == nil && !errors.Is(err, io.EOF) {
			slog.Warn("codex app-server read failed", "error", err)
			if errors.Is(err, bufio.ErrTooLong) {
				s.emitError(fmt.Errorf("codex app-server line exceeds max size (%d bytes): %w", maxLineSize, err))
			} else {
				s.emitError(fmt.Errorf("codex app-server connection closed: %w", err))
			}
		}
		s.alive.Store(false)
		s.rejectPending(err)
		return
	}

	s.alive.Store(false)
	s.rejectPending(io.EOF)
}

func (s *appServerSession) wsReadLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		s.procMu.Lock()
		conn := s.conn
		s.procMu.Unlock()
		if conn == nil {
			s.alive.Store(false)
			s.rejectPending(io.EOF)
			return
		}

		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if s.ctx.Err() == nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					slog.Debug("codex app-server websocket closed")
				} else {
					slog.Warn("codex app-server websocket read failed", "error", err)
					s.emitError(fmt.Errorf("codex app-server websocket closed: %w", err))
				}
			}
			s.alive.Store(false)
			s.rejectPending(err)
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		s.handleRPCMessage(data)
	}
}

func (s *appServerSession) handleRPCMessage(data []byte) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		slog.Debug("codex app-server: invalid JSON", "error", err)
		return
	}

	if _, ok := probe["id"]; ok {
		var resp rpcResponseEnvelope
		if err := json.Unmarshal(data, &resp); err != nil {
			slog.Debug("codex app-server: bad response envelope", "error", err)
			return
		}
		s.handleResponse(resp)
		return
	}

	var notif rpcNotificationEnvelope
	if err := json.Unmarshal(data, &notif); err != nil {
		slog.Debug("codex app-server: bad notification envelope", "error", err)
		return
	}
	s.handleNotification(notif.Method, notif.Params)
}

func (s *appServerSession) stderrLoop(r io.Reader) {
	defer s.wg.Done()
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Redact before logging: app-server stderr may echo env / tokens.
		slog.Debug("codex app-server stderr", "line", core.RedactStderr(line))
	}
	if err := scanner.Err(); err != nil && s.ctx.Err() == nil {
		slog.Debug("codex app-server stderr read failed", "error", err)
	}
}

func (s *appServerSession) waitLoop() {
	defer s.wg.Done()

	s.procMu.Lock()
	cmd := s.cmd
	s.procMu.Unlock()
	if cmd == nil {
		return
	}

	err := cmd.Wait()
	if s.ctx.Err() == nil && err != nil {
		slog.Warn("codex app-server exited unexpectedly", "error", err)
		s.emitError(fmt.Errorf("codex app-server exited: %w", err))
	}
	s.alive.Store(false)
	if err == nil {
		err = io.EOF
	}
	s.rejectPending(err)
}

func (s *appServerSession) handleResponse(resp rpcResponseEnvelope) {
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

func (s *appServerSession) handleNotification(method string, paramsRaw json.RawMessage) {
	switch method {
	case "turn/started":
		var notif turnNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.stateMu.Lock()
			s.currentTurn = notif.Turn.ID
			s.pendingMsgs = s.pendingMsgs[:0]
			s.stateMu.Unlock()
			s.storeContextUsage(nil)
			s.sawDelta.Clear()
			threadID := strings.TrimSpace(notif.ThreadID)
			if threadID != "" {
				s.emit(core.Event{Type: core.EventTurnStarted, SessionID: threadID})
			}
		} else {
			slog.Debug("codex app-server: turn/started parse failed", "error", err)
		}

	case "item/started":
		var notif itemNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.handleItemStarted(notif.Item)
		} else {
			slog.Debug("codex app-server: item/started parse failed", "error", err)
		}

	case "item/completed":
		var notif itemNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.handleItemCompleted(notif.Item)
		} else {
			slog.Debug("codex app-server: item/completed parse failed", "error", err)
		}

	case "event_msg":
		s.handleEventMessage(paramsRaw)

	case "patch_apply_end":
		s.handlePatchApplyEnd(paramsRaw)

	case "item/agentMessage/delta":
		s.handleAgentMessageDelta(paramsRaw)

	case "item/reasoning/textDelta":
		s.handleReasoningTextDelta(paramsRaw)

	case "turn/plan/updated":
		var notif planNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.emit(core.Event{
				Type:      core.EventPlan,
				SessionID: strings.TrimSpace(notif.ThreadID),
				Plan:      codexPlanEntriesToTodos(notif.Plan),
			})
		} else {
			slog.Debug("codex app-server: turn/plan/updated parse failed", "error", err)
		}

	case "turn/completed":
		slog.Info("codex app-server: received turn/completed", "threadID", s.CurrentSessionID())
		var notif turnNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.flushPendingAsText()
			s.sawDelta.Clear()
			s.emit(core.Event{
				Type:      core.EventResult,
				SessionID: s.CurrentSessionID(),
				Done:      true,
			})
			slog.Info("codex app-server: turn/completed → EventResult emitted", "threadID", notif.ThreadID, "turnStatus", notif.Turn.Status)
		} else {
			slog.Warn("codex app-server: turn/completed parse failed", "error", err)
		}

	case "account/rateLimits/updated":
		var notif appServerRateLimitsResponse
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			s.storeUsage(mapAppServerRateLimits(notif))
		} else {
			slog.Debug("codex app-server: account/rateLimits/updated parse failed", "error", err)
		}

	case "thread/tokenUsage/updated":
		var notif appServerThreadTokenUsageNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			usage := mapAppServerTokenUsage(notif)
			s.storeContextUsage(usage)
			if usage != nil {
				s.emit(core.Event{
					Type:         core.EventContextUsageUpdated,
					SessionID:    strings.TrimSpace(notif.ThreadID),
					ContextUsage: usage,
				})
			}
		} else {
			slog.Debug("codex app-server: thread/tokenUsage/updated parse failed", "error", err)
		}

	case "error":
		var notif errorNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil && strings.TrimSpace(notif.Message) != "" {
			s.emitError(fmt.Errorf("%s", notif.Message))
		}

	case "turn/question":
		var notif questionNotification
		if err := json.Unmarshal(paramsRaw, &notif); err == nil {
			threadID, _ := notif.ThreadID.(string)
			qID, _ := notif.ID.(string)
			qText, _ := notif.Question.(string)
			required, _ := notif.Required.(bool)
			var opts []core.QuestionOption
			for _, o := range notif.Options {
				optID, _ := o["id"].(string)
				optLabel, _ := o["label"].(string)
				optDesc, _ := o["description"].(string)
				opts = append(opts, core.QuestionOption{
					ID:          optID,
					Label:       optLabel,
					Description: optDesc,
				})
			}
			s.emit(core.Event{
				Type:         core.EventQuestionAsked,
				SessionID:    strings.TrimSpace(threadID),
				QuestionID:   qID,
				QuestionText: qText,
				QuestionOpts: opts,
				Required:     required,
				ThreadID:     strings.TrimSpace(threadID),
			})
		}
	}
}

func (s *appServerSession) handleItemStarted(item map[string]any) {
	itemType, _ := item["type"].(string)
	if itemType == "" {
		return
	}

	switch itemType {
	case "agentMessage", "reasoning", "userMessage", "plan", "hookPrompt":
		return
	case "contextCompaction":
		s.emit(core.Event{Type: core.EventContextCompressing, SessionID: s.CurrentSessionID()})
		return
	}

	itemID, _ := item["id"].(string)

	s.flushPendingAsThinking()

	switch itemType {
	case "commandExecution":
		command, _ := item["command"].(string)
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "Bash", ToolInput: command, RequestID: itemID})

	case "mcpToolCall":
		server, _ := item["server"].(string)
		tool, _ := item["tool"].(string)
		name := strings.Trim(strings.Join([]string{server, tool}, ":"), ":")
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "MCP", ToolInput: name + "\n" + appServerJSON(item["arguments"]), RequestID: itemID})

	case "webSearch":
		query, _ := item["query"].(string)
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "WebSearch", ToolInput: query, RequestID: itemID})

	case "dynamicToolCall":
		tool, _ := item["tool"].(string)
		s.emit(core.Event{Type: core.EventToolUse, ToolName: tool, ToolInput: appServerJSON(item["arguments"]), RequestID: itemID})

	case "fileChange":
		s.emit(core.Event{Type: core.EventToolUse, ToolName: "Patch", ToolInput: appServerJSON(item["changes"]), FileChanges: appServerFileChanges(item["changes"]), RequestID: itemID})

	case "customToolCall", "custom_tool_call":
		name := strings.TrimSpace(appServerStringValue(item["name"]))
		if name == "" {
			name = strings.TrimSpace(appServerStringValue(item["tool"]))
		}
		if name == "apply_patch" {
			s.emit(core.Event{Type: core.EventToolUse, ToolName: "Patch", ToolInput: appServerStringValue(item["input"]), RequestID: itemID})
		}
	}
}

func (s *appServerSession) handleItemCompleted(item map[string]any) {
	itemType, _ := item["type"].(string)
	if itemType == "" {
		return
	}

	itemID, _ := item["id"].(string)

	switch itemType {
	case "reasoning":
		if itemID != "" {
			if _, saw := s.sawDelta.Load(itemID); saw {
				return
			}
		}
		text := appServerReasoningText(item)
		if text != "" {
			s.emit(core.Event{Type: core.EventThinking, Content: text})
		}

	case "agentMessage":
		text, _ := item["text"].(string)
		if itemID != "" {
			if _, saw := s.sawDelta.Load(itemID); saw {
				return
			}
		}
		if strings.TrimSpace(text) != "" {
			s.stateMu.Lock()
			s.pendingMsgs = append(s.pendingMsgs, text)
			s.stateMu.Unlock()
		}

	case "commandExecution":
		command, _ := item["command"].(string)
		status, _ := item["status"].(string)
		output, _ := item["aggregatedOutput"].(string)
		exitCode, hasExitCode := toInt(item["exitCode"])
		var exitCodePtr *int
		if hasExitCode {
			exitCodePtr = &exitCode
		}
		success := appServerToolSuccess(status, exitCodePtr)
		s.emit(core.Event{
			Type:         core.EventToolResult,
			ToolName:     "Bash",
			ToolInput:    command,
			ToolResult:   truncate(strings.TrimSpace(output), 500),
			ToolStatus:   strings.TrimSpace(status),
			ToolExitCode: exitCodePtr,
			ToolSuccess:  &success,
			RequestID:    itemID,
		})

	case "mcpToolCall":
		tool, _ := item["tool"].(string)
		status, _ := item["status"].(string)
		result := appServerJSON(item["result"])
		if errText := appServerJSON(item["error"]); strings.TrimSpace(errText) != "" && result == "" {
			result = errText
		}
		success := appServerToolSuccess(status, nil)
		s.emit(core.Event{
			Type:        core.EventToolResult,
			ToolName:    tool,
			ToolResult:  truncate(strings.TrimSpace(result), 500),
			ToolStatus:  strings.TrimSpace(status),
			ToolSuccess: &success,
			RequestID:   itemID,
		})

	case "webSearch":
		query, _ := item["query"].(string)
		s.emit(core.Event{
			Type:       core.EventToolResult,
			ToolName:   "WebSearch",
			ToolResult: truncate(strings.TrimSpace(query), 500),
			RequestID:  itemID,
		})

	case "dynamicToolCall":
		tool, _ := item["tool"].(string)
		status, _ := item["status"].(string)
		result := appServerDynamicToolText(item["contentItems"])
		success := appServerToolSuccess(status, nil)
		s.emit(core.Event{
			Type:        core.EventToolResult,
			ToolName:    tool,
			ToolResult:  truncate(strings.TrimSpace(result), 500),
			ToolStatus:  strings.TrimSpace(status),
			ToolSuccess: &success,
			RequestID:   itemID,
		})

	case "fileChange":
		changes := appServerFileChanges(item["changes"])
		s.emit(core.Event{
			Type:        core.EventToolResult,
			ToolName:    "Patch",
			ToolResult:  appServerFileChangeResult(changes),
			ToolStatus:  "completed",
			ToolSuccess: ptrBool(true),
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
					FileChanges: changes,
					RequestID:   itemID,
				})
			}
		}

	case "contextCompaction":
		s.emit(core.Event{Type: core.EventContextCompressed, SessionID: s.CurrentSessionID()})
	}
}

func (s *appServerSession) handleEventMessage(paramsRaw json.RawMessage) {
	var payload map[string]any
	if err := json.Unmarshal(paramsRaw, &payload); err != nil {
		return
	}
	if nested, ok := payload["payload"].(map[string]any); ok {
		payload = nested
	}
	if strings.TrimSpace(appServerStringValue(payload["type"])) == "patch_apply_end" {
		raw, _ := json.Marshal(payload)
		s.handlePatchApplyEnd(raw)
	}
}

func (s *appServerSession) handlePatchApplyEnd(paramsRaw json.RawMessage) {
	var payload map[string]any
	if err := json.Unmarshal(paramsRaw, &payload); err != nil {
		return
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
		FileChanges: changes,
	})
}

func ptrBool(value bool) *bool {
	return &value
}

func appServerFileChanges(raw any) []core.FileChange {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	changes := make([]core.FileChange, 0, len(items))
	for _, item := range items {
		change, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path := strings.TrimSpace(appServerStringValue(change["path"]))
		if path == "" {
			continue
		}
		kind := strings.TrimSpace(appServerStringValue(change["kind"]))
		if kind == "" {
			kind = strings.TrimSpace(appServerStringValue(change["type"]))
		}
		if kind == "" {
			kind = strings.TrimSpace(appServerStringValue(change["operation"]))
		}
		if kind == "" {
			kind = "edit"
		}
		changes = append(changes, core.FileChange{
			Path:     path,
			Kind:     kind,
			Diff:     appServerStringValue(change["diff"]),
			MovePath: appServerStringValue(change["movePath"]),
		})
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

func appServerPatchChanges(raw any) []core.FileChange {
	items, ok := raw.(map[string]any)
	if !ok {
		return appServerFileChanges(raw)
	}
	changes := make([]core.FileChange, 0, len(items))
	for path, rawChange := range items {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		change, ok := rawChange.(map[string]any)
		if !ok {
			continue
		}
		kind := strings.TrimSpace(appServerStringValue(change["type"]))
		if kind == "" {
			kind = strings.TrimSpace(appServerStringValue(change["kind"]))
		}
		if kind == "update" {
			kind = "edit"
		}
		if kind == "" {
			kind = "edit"
		}
		changes = append(changes, core.FileChange{
			Path:     path,
			Kind:     kind,
			Diff:     appServerStringValue(change["unified_diff"]),
			MovePath: appServerStringValue(change["move_path"]),
		})
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

func appServerStringValue(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func appServerFileChangeResult(changes []core.FileChange) string {
	if len(changes) == 0 {
		return ""
	}
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, change.Path)
	}
	return strings.Join(lines, "\n")
}

func (s *appServerSession) handleAgentMessageDelta(paramsRaw json.RawMessage) {
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
		s.sawDelta.Store(notif.ItemID, true)
	}
	s.emit(core.Event{Type: core.EventText, Content: notif.Delta})
}

func (s *appServerSession) handleReasoningTextDelta(paramsRaw json.RawMessage) {
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
		s.sawDelta.Store(notif.ItemID, true)
	}
	s.emit(core.Event{Type: core.EventThinking, Content: notif.Delta})
}

func appServerReasoningText(item map[string]any) string {
	var parts []string
	if summary, ok := item["summary"].([]any); ok {
		for _, entry := range summary {
			if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		if content, ok := item["content"].([]any); ok {
			for _, entry := range content {
				if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func appServerDynamicToolText(raw any) string {
	items, ok := raw.([]any)
	if !ok {
		return appServerJSON(raw)
	}
	var parts []string
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text, _ := m["text"].(string); strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return appServerJSON(raw)
	}
	return strings.Join(parts, "\n")
}

func appServerToolSuccess(status string, exitCode *int) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	if exitCode != nil {
		return *exitCode == 0
	}
	return s == "completed" || s == "success" || s == "succeeded" || s == "ok"
}

func mapAppServerRateLimits(payload appServerRateLimitsResponse) *core.UsageReport {
	report := &core.UsageReport{Provider: "codex"}

	var snapshots []appServerRateLimitSnapshot
	if len(payload.RateLimitsByLimitID) > 0 {
		keys := make([]string, 0, len(payload.RateLimitsByLimitID))
		for key := range payload.RateLimitsByLimitID {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			snapshots = append(snapshots, payload.RateLimitsByLimitID[key])
		}
	} else if payload.RateLimits.LimitID != "" || payload.RateLimits.Primary != nil || payload.RateLimits.Secondary != nil || payload.RateLimits.Credits != nil {
		snapshots = append(snapshots, payload.RateLimits)
	}

	for _, snapshot := range snapshots {
		if report.Plan == "" && strings.TrimSpace(snapshot.PlanType) != "" {
			report.Plan = strings.TrimSpace(snapshot.PlanType)
		}
		if report.Credits == nil && snapshot.Credits != nil {
			report.Credits = &core.UsageCredits{
				HasCredits: snapshot.Credits.HasCredits,
				Unlimited:  snapshot.Credits.Unlimited,
			}
			if snapshot.Credits.Balance != nil {
				report.Credits.Balance = strings.TrimSpace(*snapshot.Credits.Balance)
			}
		}

		windows := appServerUsageWindows(snapshot)
		if len(windows) == 0 {
			continue
		}
		limitReached := false
		for _, window := range windows {
			if window.UsedPercent >= 100 {
				limitReached = true
				break
			}
		}

		report.Buckets = append(report.Buckets, core.UsageBucket{
			Name:         appServerBucketName(snapshot),
			Allowed:      !limitReached,
			LimitReached: limitReached,
			Windows:      windows,
		})
	}

	return report
}

func appServerBucketName(snapshot appServerRateLimitSnapshot) string {
	if name := strings.TrimSpace(snapshot.LimitName); name != "" {
		return name
	}
	if id := strings.TrimSpace(snapshot.LimitID); id != "" {
		return id
	}
	return "Rate limit"
}

func appServerUsageWindows(snapshot appServerRateLimitSnapshot) []core.UsageWindow {
	var windows []core.UsageWindow
	if snapshot.Primary != nil {
		windows = append(windows, appServerUsageWindow("Primary", snapshot.Primary))
	}
	if snapshot.Secondary != nil {
		windows = append(windows, appServerUsageWindow("Secondary", snapshot.Secondary))
	}
	return windows
}

func appServerUsageWindow(name string, window *appServerRateLimitWindow) core.UsageWindow {
	resetAfter := 0
	if window != nil && window.ResetsAt > 0 {
		resetAfter = int(time.Until(time.Unix(window.ResetsAt, 0)).Seconds())
		if resetAfter < 0 {
			resetAfter = 0
		}
	}
	return core.UsageWindow{
		Name:              name,
		UsedPercent:       window.UsedPercent,
		WindowSeconds:     window.WindowDurationMins * 60,
		ResetAfterSeconds: resetAfter,
		ResetAtUnix:       window.ResetsAt,
	}
}

func cloneUsageReport(report *core.UsageReport) *core.UsageReport {
	if report == nil {
		return nil
	}
	cloned := *report
	if len(report.Buckets) > 0 {
		cloned.Buckets = make([]core.UsageBucket, len(report.Buckets))
		for i, bucket := range report.Buckets {
			cloned.Buckets[i] = bucket
			if len(bucket.Windows) > 0 {
				cloned.Buckets[i].Windows = append([]core.UsageWindow(nil), bucket.Windows...)
			}
		}
	}
	if report.Credits != nil {
		credits := *report.Credits
		cloned.Credits = &credits
	}
	return &cloned
}

func normalizeRuntimeReasoningEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "med":
		return "medium"
	case "x-high", "very-high":
		return "xhigh"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func appServerJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "{}" || s == "[]" || s == `""` {
		return ""
	}
	return s
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i), true
		}
	}
	return 0, false
}

func rpcIDToInt64(v any) (int64, bool) {
	switch id := v.(type) {
	case float64:
		return int64(id), true
	case int64:
		return id, true
	case int:
		return int64(id), true
	case json.Number:
		i, err := id.Int64()
		return i, err == nil
	}
	return 0, false
}

func (s *appServerSession) flushPendingAsThinking() {
	s.stateMu.Lock()
	msgs := append([]string(nil), s.pendingMsgs...)
	s.pendingMsgs = s.pendingMsgs[:0]
	s.stateMu.Unlock()

	for _, text := range msgs {
		if strings.TrimSpace(text) != "" {
			s.emit(core.Event{Type: core.EventThinking, Content: text})
		}
	}
}

func (s *appServerSession) flushPendingAsText() {
	s.stateMu.Lock()
	msgs := append([]string(nil), s.pendingMsgs...)
	s.pendingMsgs = s.pendingMsgs[:0]
	s.stateMu.Unlock()

	for _, text := range msgs {
		if strings.TrimSpace(text) != "" {
			s.emit(core.Event{Type: core.EventText, Content: text})
		}
	}
}

func (s *appServerSession) emit(event core.Event) {
	select {
	case s.events <- event:
	default:
		slog.Warn("codex appserver: event channel full, dropping event", "type", event.Type)
	}
}

func (s *appServerSession) emitError(err error) {
	if err == nil {
		return
	}
	s.emit(core.Event{Type: core.EventError, Error: err})
}

func (s *appServerSession) rejectPending(err error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, ch := range s.pending {
		delete(s.pending, id)
		select {
		case ch <- rpcResponseEnvelope{ID: id, Error: &rpcError{Message: err.Error()}}:
		default:
		}
	}
}

// CompactContext sends thread/compact/start to the Codex app-server.
// Nil return means accepted; actual completion arrives via Events().
func (s *appServerSession) CompactContext(ctx context.Context) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is closed")
	}
	return s.request("thread/compact/start", map[string]any{
		"threadId": s.CurrentSessionID(),
	}, nil)
}

func (s *appServerSession) request(method string, params any, out any) error {
	return s.requestWithTimeout(method, params, out, appServerRequestTimeout)
}

func (s *appServerSession) requestWithTimeout(method string, params any, out any, timeout time.Duration) error {
	id := s.nextID.Add(1)
	ch := make(chan rpcResponseEnvelope, 1)

	s.pendingMu.Lock()
	if s.pending == nil {
		s.pending = make(map[int64]chan rpcResponseEnvelope)
	}
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
			return fmt.Errorf("%s", strings.TrimSpace(resp.Error.Message))
		}
		if out != nil {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-time.After(timeout):
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return fmt.Errorf("%s timed out", method)
	}
}

func (s *appServerSession) notify(method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	return s.writeJSON(payload)
}

func (s *appServerSession) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("codex app-server encode: %w", err)
	}

	return s.writeMessage(b)
}

func (s *appServerSession) writeMessage(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.procMu.Lock()
	conn := s.conn
	stdin := s.stdin
	transport := s.transport
	s.procMu.Unlock()

	if transport == appServerTransportWebSocket {
		if conn == nil {
			return fmt.Errorf("codex app-server websocket is closed")
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return fmt.Errorf("codex app-server websocket write: %w", err)
		}
		return nil
	}

	if stdin == nil {
		return fmt.Errorf("codex app-server connection is closed")
	}

	if _, err := stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("codex app-server write: %w", err)
	}
	return nil
}
