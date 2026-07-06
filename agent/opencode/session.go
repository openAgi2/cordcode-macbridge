package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// opencodeSession manages multi-turn conversations with the OpenCode CLI.
// Each Send() launches a new `opencode run --format json` process
// with --session for conversation continuity.
type opencodeSession struct {
	cmd      string
	workDir  string
	model    string
	mode     string
	extraEnv []string
	events   chan core.Event
	chatID   atomic.Value // stores string — OpenCode session ID
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	alive    atomic.Bool
	// closeOnce guarantees events is closed exactly once, and ONLY after the
	// producer (readLoop, tracked by wg) has exited. Closing from the timeout
	// branch while a producer may still send would panic (closed-channel send);
	// emit()'s default branch does NOT prevent that panic.
	closeOnce sync.Once
	// closeTimeout is how long Close() waits for the producer before deferring
	// the events close. Defaults to opencodeSessionCloseTimeout; overridable in
	// tests to exercise the timeout branch deterministically.
	closeTimeout time.Duration

	mu           sync.Mutex
	inputTokens  int // accumulated from step_finish events
	outputTokens int
}

func newOpencodeSession(ctx context.Context, cmd, workDir, model, mode, resumeID string, extraEnv []string) (*opencodeSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	s := &opencodeSession{
		cmd:      cmd,
		workDir:  workDir,
		model:    model,
		mode:     mode,
		extraEnv: extraEnv,
		events:   make(chan core.Event, 64),
		ctx:      sessionCtx,
		cancel:   cancel,
	}
	s.alive.Store(true)

	if resumeID != "" && resumeID != core.ContinueSession {
		s.chatID.Store(resumeID)
	}

	return s, nil
}

func (s *opencodeSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if len(files) > 0 {
		filePaths := core.SaveFilesToDisk(s.workDir, files)
		prompt = core.AppendFileRefs(prompt, filePaths)
	}
	prompt, imagePaths, err := stageOpencodeImages(s.workDir, prompt, images)
	if err != nil {
		return err
	}
	if !s.alive.Load() {
		return fmt.Errorf("session is closed")
	}

	chatID := s.CurrentSessionID()
	isResume := chatID != ""

	args := s.buildRunArgs(prompt, imagePaths, chatID)

	slog.Debug("opencodeSession: launching", "resume", isResume, "args", core.RedactArgs(args))

	cmd := exec.CommandContext(s.ctx, s.cmd, args...)
	cmd.Dir = s.workDir
	// Controlled agent env: minimal runtime allowlist + provider/session env,
	// CCCODE_* / OPENCODE_SERVER_* denied at every layer.
	cmd.Env = core.BuildAgentEnv(
		core.FilterEnvToAllowlist(os.Environ(), core.AgentEnvRuntimeAllowlist()),
		s.extraEnv,
		nil,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("opencodeSession: stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opencodeSession: start: %w", err)
	}

	s.wg.Add(1)
	go s.readLoop(cmd, stdout, &stderrBuf)

	return nil
}

func stageOpencodeImages(workDir, prompt string, images []core.ImageAttachment) (string, []string, error) {
	if len(images) == 0 {
		return prompt, nil, nil
	}

	imgDir := filepath.Join(workDir, ".cc-connect", "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("opencode: create image dir: %w", err)
	}

	imagePaths := make([]string, 0, len(images))
	for i, img := range images {
		ext := opencodeImageExt(img.MimeType)
		fname := fmt.Sprintf("img_%d_%d%s", time.Now().UnixMilli(), i, ext)
		fpath := filepath.Join(imgDir, fname)
		if err := os.WriteFile(fpath, img.Data, 0o644); err != nil {
			return "", nil, fmt.Errorf("opencode: save image: %w", err)
		}
		imagePaths = append(imagePaths, fpath)
	}

	if prompt == "" {
		prompt = "Please analyze the attached image(s)."
	}

	return prompt, imagePaths, nil
}

func opencodeImageExt(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func (s *opencodeSession) buildRunArgs(prompt string, imagePaths []string, chatID string) []string {
	args := []string{"run", "--format", "json"}

	if chatID != "" {
		args = append(args, "--session", chatID)
	}
	if s.model != "" {
		args = append(args, "--model", s.model)
	}
	if s.workDir != "" {
		args = append(args, "--dir", s.workDir)
	}

	// Enable thinking blocks.
	args = append(args, "--thinking")

	for _, imagePath := range imagePaths {
		if imagePath == "" {
			continue
		}
		args = append(args, "--file", imagePath)
	}

	// Use "--" to separate flags from the positional prompt so that
	// --file (yargs [array]) does not greedily consume the prompt text.
	args = append(args, "--", prompt)
	return args
}

func (s *opencodeSession) readLoop(cmd *exec.Cmd, stdout io.ReadCloser, stderrBuf *bytes.Buffer) {
	defer s.wg.Done()
	defer func() { _ = cmd.Wait() }()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			slog.Debug("opencodeSession: non-JSON line", "line", line)
			continue
		}

		s.handleEvent(raw)
	}

	if err := scanner.Err(); err != nil {
		slog.Error("opencodeSession: scanner error", "error", err)
		evt := core.Event{Type: core.EventError, Error: fmt.Errorf("read stdout: %w", err)}
		select {
		case s.events <- evt:
		case <-s.ctx.Done():
			return
		}
		return
	}

	stderrMsg := stderrBuf.String()
	if stderrMsg != "" {
		redacted := core.RedactStderr(stderrMsg)
		slog.Error("opencodeSession: process error", "stderr", truncate(redacted, 500))
		// "Session not found" detection runs against the original (pre-redact)
		// text because redaction preserves stable ASCII substrings, but check
		// the redacted copy too for belt-and-braces.
		if strings.Contains(stderrMsg, "Session not found") || strings.Contains(redacted, "Session not found") {
			s.chatID.Store("")
			slog.Warn("opencodeSession: cleared stale session ID")
		}
		evt := core.Event{Type: core.EventError, Error: fmt.Errorf("%s", redacted)}
		select {
		case s.events <- evt:
		case <-s.ctx.Done():
		}
		return
	}

	sid := s.CurrentSessionID()
	s.mu.Lock()
	inTok, outTok := s.inputTokens, s.outputTokens
	s.inputTokens = 0
	s.outputTokens = 0
	s.mu.Unlock()
	slog.Debug("opencodeSession: readLoop complete, sending fallback EventResult", "session_id", sid, "inputTokens", inTok, "outputTokens", outTok)
	evt := core.Event{Type: core.EventResult, SessionID: sid, Done: true, InputTokens: inTok, OutputTokens: outTok}
	select {
	case s.events <- evt:
	case <-s.ctx.Done():
	}
}

// OpenCode NDJSON event structure:
//
//	{ "type": "text|tool_use|reasoning|step_start|step_finish",
//	  "part": { "type": "text|tool|reasoning|step-start|step-finish", ... } }
func (s *opencodeSession) handleEvent(raw map[string]any) {
	eventType, _ := raw["type"].(string)

	switch eventType {
	case "text":
		s.handleText(raw)
	case "tool_use":
		s.handleToolUse(raw)
	case "reasoning":
		s.handleReasoning(raw)
	case "step_start":
		s.handleStepStart(raw)
	case "step_finish":
		s.handleStepFinish(raw)
	case "error":
		s.handleError(raw)
	default:
		b, _ := json.Marshal(raw)
		slog.Debug("opencodeSession: unhandled event", "type", eventType, "raw", string(b))
	}
}

func (s *opencodeSession) handleText(raw map[string]any) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}
	text, _ := part["text"].(string)
	if text != "" {
		evt := core.Event{Type: core.EventText, Content: text}
		select {
		case s.events <- evt:
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *opencodeSession) handleToolUse(raw map[string]any) {
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

	// Extract tool input summary for display
	input := extractToolInput(state)

	if status == "completed" {
		// OpenCode bundles call + result in one event; emit both for UI.
		useEvt := core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: input}
		select {
		case s.events <- useEvt:
		case <-s.ctx.Done():
			return
		}

		output, _ := state["output"].(string)
		resultEvt := core.Event{Type: core.EventToolResult, ToolName: toolName, Content: truncate(output, 500)}
		select {
		case s.events <- resultEvt:
		case <-s.ctx.Done():
			return
		}
	} else {
		evt := core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: input}
		select {
		case s.events <- evt:
		case <-s.ctx.Done():
			return
		}
	}
}

func extractToolInput(state map[string]any) string {
	if state == nil {
		return ""
	}
	// Prefer title as a concise description (e.g. "List files in current directory")
	if title, ok := state["title"].(string); ok && title != "" {
		return title
	}
	switch input := state["input"].(type) {
	case string:
		return input
	case map[string]any:
		// Use "description" or "command" fields if available
		if desc, ok := input["description"].(string); ok && desc != "" {
			return desc
		}
		if cmd, ok := input["command"].(string); ok && cmd != "" {
			return cmd
		}
		b, _ := json.Marshal(input)
		return truncate(string(b), 200)
	}
	return ""
}

func (s *opencodeSession) handleReasoning(raw map[string]any) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}
	text, _ := part["text"].(string)
	if text != "" {
		evt := core.Event{Type: core.EventThinking, Content: text}
		select {
		case s.events <- evt:
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *opencodeSession) handleError(raw map[string]any) {
	errMsg := extractErrorMessage(raw)
	slog.Error("opencodeSession: agent error", "error", errMsg)
	evt := core.Event{Type: core.EventError, Error: fmt.Errorf("%s", errMsg)}
	select {
	case s.events <- evt:
	case <-s.ctx.Done():
		return
	}
}

// extractErrorMessage tries to pull a human-readable message from various
// OpenCode error JSON shapes.
func extractErrorMessage(raw map[string]any) string {
	// Shape: {"error": {"data": {"message": "..."}, "name": "..."}}
	if errObj, ok := raw["error"].(map[string]any); ok {
		if data, ok := errObj["data"].(map[string]any); ok {
			if msg, ok := data["message"].(string); ok && msg != "" {
				name, _ := errObj["name"].(string)
				if name != "" {
					return name + ": " + msg
				}
				return msg
			}
		}
		if msg, ok := errObj["message"].(string); ok && msg != "" {
			return msg
		}
		if name, ok := errObj["name"].(string); ok && name != "" {
			return name
		}
	}
	// Shape: {"error": "string message"}
	if errStr, ok := raw["error"].(string); ok && errStr != "" {
		return errStr
	}
	// Shape: {"part": {"error": "...", "message": "..."}}
	if part, ok := raw["part"].(map[string]any); ok {
		if msg, ok := part["error"].(string); ok && msg != "" {
			return msg
		}
		if msg, ok := part["message"].(string); ok && msg != "" {
			return msg
		}
	}
	if msg, ok := raw["message"].(string); ok && msg != "" {
		return msg
	}
	b, _ := json.Marshal(raw)
	return string(b)
}

func (s *opencodeSession) handleStepStart(raw map[string]any) {
	part, _ := raw["part"].(map[string]any)
	if part == nil {
		return
	}
	sessionID, _ := part["sessionID"].(string)
	if sessionID != "" {
		s.chatID.Store(sessionID)
		slog.Debug("opencodeSession: session started", "session_id", sessionID)
	}
}

func (s *opencodeSession) handleStepFinish(raw map[string]any) {
	part, _ := raw["part"].(map[string]any)
	reason, _ := part["reason"].(string)
	slog.Debug("opencodeSession: step finished", "reason", reason, "session_id", s.CurrentSessionID())

	if tokens, ok := part["tokens"].(map[string]any); ok {
		in, out := 0, 0
		if v, ok := tokens["input"].(float64); ok {
			in = int(v)
		}
		if v, ok := tokens["output"].(float64); ok {
			out = int(v)
		}
		s.mu.Lock()
		s.inputTokens += in
		s.outputTokens += out
		s.mu.Unlock()
	}
}

// RespondPermission is a no-op — OpenCode handles permissions internally.
func (s *opencodeSession) RespondPermission(_ string, _ core.PermissionResult) error {
	return nil
}

func (s *opencodeSession) RespondQuestion(_ string, _ []string) error {
	return fmt.Errorf("%s does not support question reply", "opencodeSession")
}

func (s *opencodeSession) RejectQuestion(_ string) error {
	return fmt.Errorf("%s does not support question reject", "opencodeSession")
}

func (s *opencodeSession) Events() <-chan core.Event {
	return s.events
}

func (s *opencodeSession) CurrentSessionID() string {
	v, _ := s.chatID.Load().(string)
	return v
}

func (s *opencodeSession) Alive() bool {
	return s.alive.Load()
}

// opencodeSessionCloseTimeout is how long Close() waits for readLoop to exit
// after cancelling the context before force-abandoning the wg.Wait. The events
// channel is NEVER closed from this timeout branch — it is closed only after
// the producer (readLoop, tracked by wg) has confirmed exit, to avoid a
// closed-channel send panic. If the timeout fires, a deferred goroutine keeps
// waiting for done and then closes events.
const opencodeSessionCloseTimeout = 8 * time.Second

// opencodeSessionForceKillWait is the grace period after the close timeout
// during which we still wait (in a background goroutine) for the producer to
// exit before closing events.
const opencodeSessionForceKillWait = 2 * time.Second

func (s *opencodeSession) Close() error {
	s.alive.Store(false)
	s.cancel()
	timeout := s.closeTimeout
	if timeout <= 0 {
		timeout = opencodeSessionCloseTimeout
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// readLoop has exited; safe to close the events channel exactly once.
		s.closeOnce.Do(func() { close(s.events) })
		return nil
	case <-time.After(timeout):
		// Do NOT close(s.events) here: readLoop may still be running and a
		// subsequent send would panic. Defer the close to a goroutine that
		// waits for the producer to actually exit.
		slog.Warn("opencodeSession: close timed out, deferring events channel close until readLoop exits",
			"wait", opencodeSessionCloseTimeout)
		go func() {
			select {
			case <-done:
			case <-time.After(opencodeSessionForceKillWait):
				// Even after force-kill wait, still defer to done — we never
				// close events speculatively. readLoop will exit when its
				// pipe/ctx is torn down; this goroutine ensures the close
				// happens exactly once when it does.
				<-done
			}
			s.closeOnce.Do(func() { close(s.events) })
		}()
		return nil
	}
}

func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}
