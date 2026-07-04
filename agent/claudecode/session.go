package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// claudeSession manages a long-running Claude Code process using
// --input-format stream-json and --permission-prompt-tool stdio.
//
// In "auto" mode, permission requests are auto-approved internally
// (avoiding --dangerously-skip-permissions which fails under root).
type claudeSession struct {
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdinMu         sync.Mutex
	events          chan core.Event
	sessionID       atomic.Value // stores string
	permissionMode  atomic.Value // stores string
	autoApprove     atomic.Bool
	acceptEditsOnly atomic.Bool
	dontAsk         atomic.Bool
	workDir         string
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	alive           atomic.Bool

	activeMsgID      atomic.Value // stores string — 当前正在 diff 的 message.id
	emittedText      atomic.Value // stores string — 当前 message.id 下已发送的累积文本
	historyDraining  atomic.Bool  // --resume 启动后的历史重放期；drain 期间不 emit live 事件
	historyDrainDone chan struct{}
	historyDrainOnce sync.Once
	streamState      streamEventState
	toolNameByUseID  sync.Map // tool_use_id → tool_name

	// pendingQuestions holds unanswered Claude AskUserQuestion control requests,
	// keyed by Claude requestID. claudeSession owns it because it owns the Claude
	// stdin/control stream — a later question_reply/question_reject needs the
	// original raw input + option→label map to build the verified control_response.
	// v1 stores only single-question, single-select prompts; multi-question /
	// multi-select AskUserQuestion is denied at parse time and never enters here.
	pendingQuestions sync.Map // requestID -> *pendingClaudeQuestion

	// gracefulStopTimeout is how long Close() waits for a clean exit
	// (stdin close → Stop hooks → process exit) before escalating to
	// SIGTERM and then SIGKILL. Default: 120s to match claude-mem's
	// Stop hook timeout. The wait ends as soon as the process exits,
	// so typical shutdowns take seconds, not the full timeout.
	gracefulStopTimeout time.Duration
}

type streamEventState struct {
	currentMsgID      string
	blockTypeByIndex  map[int]string
	streamedTextByIdx map[int]string
}

func (s *streamEventState) ensure() {
	if s.blockTypeByIndex == nil {
		s.blockTypeByIndex = make(map[int]string)
	}
	if s.streamedTextByIdx == nil {
		s.streamedTextByIdx = make(map[int]string)
	}
}

func (s *streamEventState) reset() {
	s.currentMsgID = ""
	s.blockTypeByIndex = make(map[int]string)
	s.streamedTextByIdx = make(map[int]string)
}

func (s *streamEventState) onMessageStart(id string) {
	s.ensure()
	if id == "" {
		return
	}
	if s.currentMsgID != id {
		s.currentMsgID = id
		s.blockTypeByIndex = make(map[int]string)
		s.streamedTextByIdx = make(map[int]string)
	}
}

func baseClaudeInnerArgs(disableVerbose bool) []string {
	innerArgs := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--permission-prompt-tool", "stdio",
		"--include-partial-messages",
	}
	if !disableVerbose {
		innerArgs = append(innerArgs, "--verbose")
	}
	return innerArgs
}

func newClaudeSession(ctx context.Context, workDir, cliBin string, cliExtraArgs []string, cliArgsFlag string, model, effort, sessionID, mode string, allowedTools, disallowedTools []string, extraEnv []string, platformPrompt string, disableVerbose bool, spawnOpts core.SpawnOptions, maxContextTokens int) (*claudeSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	// innerArgs are Claude Code CLI flags — when a wrapper is used with
	// cliArgsFlag these get bundled into a single passthrough string.
	// outerArgs are flags the wrapper itself understands (e.g. --model).
	innerArgs := baseClaudeInnerArgs(disableVerbose)

	if mode != "" && mode != "default" {
		innerArgs = append(innerArgs, "--permission-mode", mode)
	}
	switch sessionID {
	case "", core.ContinueSession:
		// Truly fresh session — no resume, no continue.
	default:
		// Resuming a known session ID — this is cc-connect's own session
		// from a previous connection, safe to resume directly.
		innerArgs = append(innerArgs, "--resume", sessionID)
	}
	if len(allowedTools) > 0 {
		innerArgs = append(innerArgs, "--allowedTools", strings.Join(allowedTools, ","))
	}
	if len(disallowedTools) > 0 {
		innerArgs = append(innerArgs, "--disallowedTools", strings.Join(disallowedTools, ","))
	}

	if sysPrompt := core.AgentSystemPrompt(); sysPrompt != "" {
		if platformPrompt != "" {
			sysPrompt += "\n## Formatting\n" + platformPrompt + "\n"
		}
		innerArgs = append(innerArgs, "--append-system-prompt", sysPrompt)
	}

	if effort != "" {
		innerArgs = append(innerArgs, "--effort", effort)
	}
	if maxContextTokens > 0 {
		innerArgs = append(innerArgs, "--max-context-tokens", strconv.Itoa(maxContextTokens))
	}

	// outerArgs are understood by both the wrapper and Claude CLI directly.
	var outerArgs []string
	if model != "" {
		outerArgs = append(outerArgs, "--model", model)
	}

	slog.Debug("claudeSession: starting", "innerArgs", core.RedactArgs(innerArgs), "outerArgs", core.RedactArgs(outerArgs), "dir", workDir, "mode", mode, "run_as_user", spawnOpts.RunAsUser)

	// Per-spawn defense in depth: if run_as_user is set, re-run the cheap
	// preflight (sudo still works + target still can't escalate) right
	// before we build the command. This catches sudoers being edited
	// between startup preflight and now.
	if spawnOpts.IsolationMode() {
		verifyCtx, verifyCancel := context.WithTimeout(sessionCtx, 10*time.Second)
		err := core.VerifyRunAsUserCheap(verifyCtx, core.ExecSudoRunner{}, spawnOpts.RunAsUser)
		verifyCancel()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("claudeSession: run_as_user spawn refused: %w", err)
		}
	}

	// Build final argument list.
	// When cliArgsFlag is set (e.g. "-a"), inner args are bundled into a
	// single passthrough string via that flag, while outer args (--model etc.)
	// are appended directly so the wrapper can also interpret them.
	// Args containing spaces/newlines are quoted so the wrapper's command-line
	// parser (e.g. splitCommandLine) keeps them as single tokens.
	// Result: my-cli code -t foo -a "--verbose --append-system-prompt 'long text'" --model x
	var allArgs []string
	if cliArgsFlag != "" {
		allArgs = append(allArgs, cliExtraArgs...)
		allArgs = append(allArgs, cliArgsFlag, shellJoinArgs(innerArgs))
		allArgs = append(allArgs, outerArgs...)
	} else {
		allArgs = append(allArgs, cliExtraArgs...)
		allArgs = append(allArgs, innerArgs...)
		allArgs = append(allArgs, outerArgs...)
	}
	cmd := core.BuildSpawnCommand(sessionCtx, spawnOpts, cliBin, allArgs...)
	cmd.Dir = workDir
	// Put the CLI (and any wrapper/sudo/plugin children) in its own process
	// group so Close() can reap the whole tree with one negative-PID signal,
	// matching codex. Without this, grandchildren can outlive shutdown.
	prepareCmdForProcessGroup(cmd)
	// Build a controlled agent environment: start from a minimal runtime
	// allowlist (NOT raw os.Environ(), which would leak CCCODE_* control-plane
	// secrets), then merge the provider/session env. The CCCODE_* / CLAUDECODE /
	// OPENCODE_SERVER_* deny list is applied inside BuildAgentEnv on every
	// layer (the old filterEnv(os.Environ(),"CLAUDECODE") nested-session guard
	// is subsumed by the deny list).
	env := core.BuildAgentEnv(
		core.FilterEnvToAllowlist(os.Environ(), core.AgentEnvRuntimeAllowlist()),
		extraEnv,
		nil,
	)
	// When run_as_user is set, strip the supervisor's environment down to
	// the allowlist before passing it to sudo. sudo --preserve-env also
	// enforces this, but filtering here makes the cc-connect spawn argv
	// the single source of truth.
	env = core.FilterEnvForSpawn(env, spawnOpts)
	cmd.Env = env

	var providerEnvSnapshot []string
	for _, e := range env {
		for _, prefix := range []string{"ANTHROPIC_", "CLAUDE_", "AWS_", "NO_PROXY", "DISABLE_"} {
			if strings.HasPrefix(e, prefix) {
				providerEnvSnapshot = append(providerEnvSnapshot, e)
				break
			}
		}
	}
	slog.Debug("claudeSession: spawn details",
		"bin", cliBin,
		"allArgs", core.RedactArgs(allArgs),
		"model", model,
		"providerEnv", core.RedactEnv(providerEnvSnapshot))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claudeSession: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claudeSession: stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("claudeSession: start: %w", err)
	}

	cs := &claudeSession{
		cmd:                 cmd,
		stdin:               stdin,
		events:              make(chan core.Event, 64),
		workDir:             workDir,
		ctx:                 sessionCtx,
		cancel:              cancel,
		done:                make(chan struct{}),
		historyDrainDone:    make(chan struct{}),
		gracefulStopTimeout: 120 * time.Second,
	}
	cs.setPermissionMode(mode)
	cs.sessionID.Store(sessionID)
	cs.alive.Store(true)

	// historyDraining: --resume 启动后的历史重放期。
	// 仅对明确 resume 已知 session id 时开启；
	// 空 sessionID 或 ContinueSession 不开启。
	//
	// watchdog 时序：go-bridge 侧 drainHistoryEvents 等 WaitForHistoryDrain 最多 10s（handlers.go），
	// 此处 watchdog 设 12s 作为最后兜底（略高于 go-bridge 的 10s，让 go-bridge 先超时打日志，
	// session 侧只在真正卡死时强制关闭）。3s 对历史较长的 --resume 过短：CLI 重放完整历史时
	// 首个 result 帧常在 3s 后才到达，过早 force-close 会让真实 turn 的事件被错误处理（真机症状：
	// 流式中断 + 从头输出）。正常路径由 handleResult 收到 result 帧时 markHistoryDrained 关闭。
	if sessionID != "" && sessionID != core.ContinueSession {
		cs.historyDraining.Store(true)
		time.AfterFunc(12*time.Second, func() {
			if cs.historyDraining.Load() {
				slog.Warn("claudeSession: historyDraining still true after timeout, forcing exit")
				cs.markHistoryDrained()
			}
		})
	} else {
		cs.markHistoryDrained()
	}

	go cs.readLoop(stdout, &stderrBuf)

	return cs, nil
}

func (cs *claudeSession) readLoop(stdout io.ReadCloser, stderrBuf *bytes.Buffer) {
	waitErrCh, waitDone := cs.startReadLoopWait(stdout)
	defer cs.finishReadLoop(waitErrCh, stderrBuf)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		cs.handleReadLoopLine(scanner.Text())
	}

	cs.handleReadLoopScanErr(scanner.Err(), waitDone)
}

func (cs *claudeSession) startReadLoopWait(stdout io.ReadCloser) (<-chan error, <-chan struct{}) {
	waitErrCh := make(chan error, 1)
	waitDone := make(chan struct{})

	go func() {
		waitErrCh <- cs.cmd.Wait()
		close(waitDone)
	}()

	go func() {
		select {
		case <-cs.ctx.Done():
			_ = stdout.Close()
			return
		case <-waitDone:
		}

		// Grace period: give scanner a brief window to drain any data the
		// agent wrote to the pipe buffer before exiting. If scanner finishes
		// on its own (pipe fully closed, no descendants holding it),
		// cs.done fires first and we skip the force-close entirely
		select {
		case <-cs.done:
			return
		case <-time.After(50 * time.Millisecond):
		}
		_ = stdout.Close()
	}()

	return waitErrCh, waitDone
}

func (cs *claudeSession) finishReadLoop(waitErrCh <-chan error, stderrBuf *bytes.Buffer) {
	err := <-waitErrCh

	cs.alive.Store(false)
	if err != nil {
		stderrMsg := ""
		if stderrBuf != nil {
			stderrMsg = strings.TrimSpace(stderrBuf.String())
		}
		if stderrMsg != "" {
			// Redact before the stderr enters slog or EventError — agents may
			// echo their own environment, which must not exfiltrate control-
			// plane / data-plane secrets through the bridge's error channel.
			redacted := core.RedactStderr(stderrMsg)
			slog.Error("claudeSession: process failed", "error", err, "stderr", redacted)
			evt := core.Event{Type: core.EventError, Error: fmt.Errorf("%s", redacted)}
			select {
			case cs.events <- evt:
			case <-cs.ctx.Done():
				// INVARIANT: readLoop must close cs.events and cs.done exactly once
				// on every termination path. Callers (engine event loop) rely on
				// these closures to observe session end.
			}
		}
	}
	close(cs.events)
	close(cs.done)
}

func (cs *claudeSession) handleReadLoopScanErr(err error, waitDone <-chan struct{}) {
	if err == nil {
		return
	}

	select {
	case <-cs.ctx.Done():
		return
	case <-waitDone:
		return
	default:
	}

	slog.Error("claudeSession: scanner error", "error", err)
	evt := core.Event{Type: core.EventError, Error: fmt.Errorf("read stdout: %w", err)}
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
		return
	}
}

func (cs *claudeSession) handleReadLoopLine(line string) {
	if line == "" {
		return
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		slog.Debug("claudeSession: non-JSON line", "line", line)
		return
	}

	eventType, _ := raw["type"].(string)
	slog.Debug("claudeSession: event", "type", eventType)

	switch eventType {
	case "system":
		cs.handleSystem(raw)
	case "assistant":
		cs.handleAssistant(raw)
	case "user":
		cs.handleUser(raw)
	case "result":
		cs.handleResult(raw)
	case "stream_event":
		cs.handleStreamEvent(raw)
	case "control_request":
		cs.handleControlRequest(raw)
	case "control_cancel_request":
		requestID, _ := raw["request_id"].(string)
		slog.Debug("claudeSession: permission cancelled", "request_id", requestID)
	}
}

func (cs *claudeSession) handleSystem(raw map[string]any) {
	if sid, ok := raw["session_id"].(string); ok && sid != "" {
		cs.sessionID.Store(sid)
		if cs.historyDraining.Load() {
			cs.activeMsgID.Store("")
			cs.emittedText.Store("")
			cs.streamState.reset()
			return
		}
		evt := core.Event{Type: core.EventText, SessionID: sid}
		select {
		case cs.events <- evt:
		case <-cs.ctx.Done():
			return
		}
	}
	cs.activeMsgID.Store("")
	cs.emittedText.Store("")
	cs.streamState.reset()
}

func (cs *claudeSession) handleAssistant(raw map[string]any) {
	msg, ok := raw["message"].(map[string]any)
	if !ok {
		return
	}
	contentArr, ok := msg["content"].([]any)
	if !ok {
		return
	}

	msgID, _ := msg["id"].(string)
	hasStreamState := msgID != "" && cs.streamState.currentMsgID == msgID

	fullText := fullAssistantText(contentArr)
	divergent := false
	if hasStreamState {
		for i, contentItem := range contentArr {
			item, ok := contentItem.(map[string]any)
			if !ok {
				continue
			}
			contentType, _ := item["type"].(string)
			if contentType != "text" {
				continue
			}
			text, _ := item["text"].(string)
			streamed := cs.streamState.streamedTextByIdx[i]
			if streamed != "" && text != streamed && !strings.HasPrefix(text, streamed) {
				divergent = true
				break
			}
		}
	}
	if divergent && !cs.historyDraining.Load() {
		slog.Warn("claudeSession: checkpoint diverged from streamed text; replacing with full text")
		evt := core.Event{Type: core.EventTextReplace, Content: fullText}
		select {
		case cs.events <- evt:
		case <-cs.ctx.Done():
			return
		}
	}

	// 单次有序遍历：按原始 content block 顺序 emit thinking/text/tool_use
	for i, contentItem := range contentArr {
		item, ok := contentItem.(map[string]any)
		if !ok {
			continue
		}
		contentType, _ := item["type"].(string)
		switch contentType {
		case "tool_use":
			if cs.historyDraining.Load() {
				continue
			}
			toolName, _ := item["name"].(string)
			if toolName == "AskUserQuestion" {
				continue
			}
			toolUseID, _ := item["id"].(string)
			inputSummary := summarizeInput(toolName, item["input"])
			evt := core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: inputSummary, RequestID: toolUseID}
			select {
			case cs.events <- evt:
			case <-cs.ctx.Done():
				return
			}
			if toolUseID != "" && toolName != "" {
				cs.toolNameByUseID.Store(toolUseID, toolName)
			}
		case "thinking":
			if cs.historyDraining.Load() {
				continue
			}
			if thinking, ok := item["thinking"].(string); ok && thinking != "" {
				evt := core.Event{Type: core.EventThinking, Content: thinking}
				select {
				case cs.events <- evt:
				case <-cs.ctx.Done():
					return
				}
			}
		case "text":
			if text, ok := item["text"].(string); ok && text != "" {
				if divergent || cs.historyDraining.Load() {
					continue
				}
				delta := ""
				if hasStreamState {
					streamed := cs.streamState.streamedTextByIdx[i]
					switch {
					case streamed == text:
						continue
					case streamed != "" && strings.HasPrefix(text, streamed):
						delta = text[len(streamed):]
						cs.streamState.streamedTextByIdx[i] = text
					case streamed == "":
						delta = text
						cs.streamState.streamedTextByIdx[i] = text
					default:
						continue
					}
				} else if msgID != "" {
					prevID, _ := cs.activeMsgID.Load().(string)
					if msgID != prevID {
						cs.activeMsgID.Store(msgID)
						cs.emittedText.Store("")
					}
					prev, _ := cs.emittedText.Load().(string)
					if text == prev {
						continue // 文本未变，跳过
					}
					if prev != "" && strings.HasPrefix(text, prev) {
						delta = text[len(prev):] // 增量
					} else {
						delta = text
					}
					// 非前缀匹配时保守发全文，不丢文本
					cs.emittedText.Store(text)
				} else {
					delta = text
				}
				if delta == "" {
					continue
				}
				evt := core.Event{Type: core.EventText, Content: delta}
				select {
				case cs.events <- evt:
				case <-cs.ctx.Done():
					return
				}
			}
		}
	}
}

func (cs *claudeSession) handleStreamEvent(raw map[string]any) {
	if cs.historyDraining.Load() {
		return
	}
	ev, ok := raw["event"].(map[string]any)
	if !ok {
		return
	}
	cs.streamState.ensure()
	subType, _ := ev["type"].(string)
	switch subType {
	case "message_start":
		id, _ := nestedString(ev, "message", "id")
		cs.streamState.onMessageStart(id)
	case "content_block_start":
		idx, ok := intOf(ev["index"])
		if !ok {
			return
		}
		blockType, _ := nestedString(ev, "content_block", "type")
		cs.streamState.blockTypeByIndex[idx] = blockType
	case "content_block_delta":
		idx, ok := intOf(ev["index"])
		if !ok {
			return
		}
		delta, _ := ev["delta"].(map[string]any)
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "text_delta":
			text, _ := delta["text"].(string)
			cs.emitTextDelta(idx, text)
		case "thinking_delta":
			thinking, _ := delta["thinking"].(string)
			cs.emitThinkingDelta(thinking)
		case "input_json_delta":
			// Tool input is emitted from the final assistant tool_use block.
		}
	case "content_block_stop":
		idx, ok := intOf(ev["index"])
		if ok {
			delete(cs.streamState.blockTypeByIndex, idx)
		}
	case "message_delta", "message_stop":
		return
	}
}

func (cs *claudeSession) emitTextDelta(index int, text string) {
	if text == "" || cs.historyDraining.Load() {
		return
	}
	cs.streamState.ensure()
	cs.streamState.streamedTextByIdx[index] += text
	evt := core.Event{Type: core.EventText, Content: text}
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
		return
	}
}

func (cs *claudeSession) emitThinkingDelta(thinking string) {
	if thinking == "" || cs.historyDraining.Load() {
		return
	}
	evt := core.Event{Type: core.EventThinking, Content: thinking}
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
		return
	}
}

func fullAssistantText(contentArr []any) string {
	var textBlocks []string
	for _, contentItem := range contentArr {
		item, ok := contentItem.(map[string]any)
		if !ok {
			continue
		}
		contentType, _ := item["type"].(string)
		if contentType != "text" {
			continue
		}
		text, _ := item["text"].(string)
		if text != "" {
			textBlocks = append(textBlocks, text)
		}
	}
	return strings.Join(textBlocks, "\n")
}

func nestedString(m map[string]any, keys ...string) (string, bool) {
	var cur any = m
	for _, key := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = obj[key]
		if !ok {
			return "", false
		}
	}
	v, ok := cur.(string)
	return v, ok
}

func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func (cs *claudeSession) handleUser(raw map[string]any) {
	msg, ok := raw["message"].(map[string]any)
	if !ok {
		return
	}
	contentArr, ok := msg["content"].([]any)
	if !ok {
		return
	}
	for _, contentItem := range contentArr {
		item, ok := contentItem.(map[string]any)
		if !ok {
			continue
		}
		contentType, _ := item["type"].(string)
		if contentType == "tool_result" {
			toolUseID, _ := item["tool_use_id"].(string)
			content, _ := item["content"].(string)
			isError, _ := item["is_error"].(bool)
			success := !isError
			toolNameRaw, _ := cs.toolNameByUseID.Load(toolUseID)
			toolName, _ := toolNameRaw.(string)
			resultText := strings.TrimSpace(content)
			if len(resultText) > 500 {
				resultText = resultText[:500]
			}
			evt := core.Event{
				Type:        core.EventToolResult,
				ToolName:    toolName,
				ToolResult:  resultText,
				ToolSuccess: &success,
				RequestID:   toolUseID,
			}
			select {
			case cs.events <- evt:
			case <-cs.ctx.Done():
				return
			}
			if toolUseID != "" {
				cs.toolNameByUseID.Delete(toolUseID)
			}
		}
	}
}

func (cs *claudeSession) handleResult(raw map[string]any) {
	var content string
	if result, ok := raw["result"].(string); ok {
		content = result
	}
	if sid, ok := raw["session_id"].(string); ok && sid != "" {
		cs.sessionID.Store(sid)
	}

	if cs.historyDraining.Load() {
		cs.markHistoryDrained()
		cs.activeMsgID.Store("")
		cs.emittedText.Store("")
		cs.streamState.reset()
		return
	}

	var inputTokens, outputTokens int
	if usage, ok := raw["usage"].(map[string]any); ok {
		if v, ok := usage["input_tokens"].(float64); ok {
			inputTokens = int(v)
		}
		if v, ok := usage["output_tokens"].(float64); ok {
			outputTokens = int(v)
		}
	}

	evt := core.Event{
		Type:         core.EventResult,
		Content:      content,
		SessionID:    cs.CurrentSessionID(),
		Done:         true,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
		return
	}
	cs.activeMsgID.Store("")
	cs.emittedText.Store("")
	cs.streamState.reset()
}

func (cs *claudeSession) handleControlRequest(raw map[string]any) {
	requestID, _ := raw["request_id"].(string)
	request, _ := raw["request"].(map[string]any)
	if request == nil {
		return
	}
	subtype, _ := request["subtype"].(string)
	if subtype != "can_use_tool" {
		slog.Debug("claudeSession: unknown control request subtype", "subtype", subtype)
		return
	}

	toolName, _ := request["tool_name"].(string)
	input, _ := request["input"].(map[string]any)

	if cs.autoApprove.Load() {
		slog.Debug("claudeSession: auto-approving", "request_id", requestID, "tool", toolName)
		_ = cs.RespondPermission(requestID, core.PermissionResult{
			Behavior:     "allow",
			UpdatedInput: input,
		})
		return
	}
	if cs.dontAsk.Load() {
		slog.Debug("claudeSession: auto-denying", "request_id", requestID, "tool", toolName)
		_ = cs.RespondPermission(requestID, core.PermissionResult{
			Behavior: "deny",
			Message:  "Permission mode is set to dontAsk.",
		})
		return
	}
	if cs.acceptEditsOnly.Load() && isClaudeEditTool(toolName) {
		slog.Debug("claudeSession: auto-approving edit tool", "request_id", requestID, "tool", toolName)
		_ = cs.RespondPermission(requestID, core.PermissionResult{
			Behavior:     "allow",
			UpdatedInput: input,
		})
		return
	}

	// AskUserQuestion: v1 supports exactly one single-select question.
	// - >=1 valid question and single-select/single-question -> emit question_asked
	//   and register it so a later question_reply/question_reject can answer it.
	// - multi-question (len>1) or any multiSelect -> deny at parse time, do NOT
	//   emit, do NOT involve iOS. (The iOS question model is single-select v1.)
	// - zero valid questions (malformed) -> fall through to the generic
	//   permission_request so the user still sees a visible permission block.
	if toolName == "AskUserQuestion" {
		questions := parseUserQuestions(input)
		if len(questions) > 0 {
			if len(questions) > 1 || anyMultiSelect(questions) {
				slog.Info("claudeSession: denying unsupported AskUserQuestion shape",
					"request_id", requestID, "questions", len(questions))
				_ = cs.RespondPermission(requestID, core.PermissionResult{
					Behavior: "deny",
					Message:  "AskUserQuestion with multiple or multi-select questions is not supported on this client.",
				})
				return
			}
			cs.emitAskUserQuestion(requestID, input, questions)
			return
		}
		slog.Warn("claudeSession: AskUserQuestion parsed zero valid questions; falling back to permission_request",
			"request_id", requestID)
	}

	slog.Info("claudeSession: permission request", "request_id", requestID, "tool", toolName)
	evt := core.Event{
		Type:         core.EventPermissionRequest,
		RequestID:    requestID,
		ToolName:     toolName,
		ToolInput:    summarizeInput(toolName, input),
		ToolInputRaw: input,
	}

	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
		return
	}
}

// Send writes a user message (with optional images and files) to the Claude process stdin.
// Images are sent as base64 in the multimodal content array.
// Files are saved to local temp files and referenced in the text prompt
// so Claude Code can read them with its built-in tools.
func (cs *claudeSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !cs.alive.Load() {
		return fmt.Errorf("session process is not running")
	}

	if len(images) == 0 && len(files) == 0 {
		return cs.writeJSON(map[string]any{
			"type":    "user",
			"message": map[string]any{"role": "user", "content": prompt},
		})
	}

	attachDir := filepath.Join(cs.workDir, ".cc-connect", "attachments")
	if err := os.MkdirAll(attachDir, 0o755); err != nil {
		slog.Warn("claudeSession: mkdir attachments failed", "error", err, "path", attachDir)
	}

	var parts []map[string]any
	var savedPaths []string

	// Save and encode images
	for i, img := range images {
		ext := extFromMime(img.MimeType)
		fname := fmt.Sprintf("img_%d_%d%s", time.Now().UnixMilli(), i, ext)
		fpath := filepath.Join(attachDir, fname)
		if err := os.WriteFile(fpath, img.Data, 0o644); err != nil {
			slog.Error("claudeSession: save image failed", "error", err)
			continue
		}
		savedPaths = append(savedPaths, fpath)
		slog.Debug("claudeSession: image saved", "path", fpath, "size", len(img.Data))

		mimeType := img.MimeType
		if mimeType == "" {
			mimeType = "image/png"
		}
		parts = append(parts, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mimeType,
				"data":       base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}

	// Save files to disk so Claude Code can read them
	filePaths := core.SaveFilesToDisk(cs.workDir, files)

	// Build text part: user prompt + file path references
	textPart := prompt
	if textPart == "" && len(filePaths) > 0 {
		textPart = "Please analyze the attached file(s)."
	} else if textPart == "" {
		textPart = "Please analyze the attached image(s)."
	}
	if len(savedPaths) > 0 {
		textPart += "\n\n(Images also saved locally: " + strings.Join(savedPaths, ", ") + ")"
	}
	if len(filePaths) > 0 {
		textPart += "\n\n(Files saved locally, please read them: " + strings.Join(filePaths, ", ") + ")"
	}
	parts = append(parts, map[string]any{"type": "text", "text": textPart})

	return cs.writeJSON(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": parts},
	})
}

func extFromMime(mime string) string {
	switch mime {
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

// RespondPermission writes a control_response to the Claude process stdin.
func (cs *claudeSession) RespondPermission(requestID string, result core.PermissionResult) error {
	if !cs.alive.Load() {
		return fmt.Errorf("session process is not running")
	}

	var permResponse map[string]any
	if result.Behavior == "allow" {
		updatedInput := result.UpdatedInput
		if updatedInput == nil {
			updatedInput = make(map[string]any)
		}
		permResponse = map[string]any{
			"behavior":     "allow",
			"updatedInput": updatedInput,
		}
	} else {
		msg := result.Message
		if msg == "" {
			msg = "The user denied this tool use. Stop and wait for the user's instructions."
		}
		permResponse = map[string]any{
			"behavior": "deny",
			"message":  msg,
		}
	}

	controlResponse := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   permResponse,
		},
	}

	slog.Debug("claudeSession: permission response", "request_id", requestID, "behavior", result.Behavior)
	return cs.writeJSON(controlResponse)
}

// RespondQuestion answers a Claude AskUserQuestion that was emitted as
// question_asked. It looks up the pending question by Claude requestID, validates
// v1 single-select (exactly one option id), and delivers the answer as a real
// control_response with {behavior:"allow", updatedInput:{...origInput, answers:{<questionText>:<label>}}}
// — the verified shape derived from the Claude Code SDK source. It is NOT a fake
// chat message; option labels are sent only because the verified protocol keys
// answers by question text and expects the option label as the value.
func (cs *claudeSession) RespondQuestion(questionID string, optionIDs []string) error {
	if questionID == "" {
		return fmt.Errorf("claudeSession: questionID is required")
	}
	if !cs.alive.Load() {
		return fmt.Errorf("session process is not running")
	}
	// Validate request shape BEFORE consuming the registry entry, so a malformed
	// reply (wrong arg count) does not make the question unanswerable.
	if len(optionIDs) != 1 {
		return fmt.Errorf("claudeSession: v1 question reply requires exactly one option id (got %d)", len(optionIDs))
	}
	val, ok := cs.pendingQuestions.LoadAndDelete(questionID)
	if !ok {
		return fmt.Errorf("claudeSession: no pending question for id %s", questionID)
	}
	pq, _ := val.(*pendingClaudeQuestion)
	if pq == nil {
		return fmt.Errorf("claudeSession: corrupt pending question entry for id %s", questionID)
	}
	if len(pq.questions) != 1 {
		return fmt.Errorf("claudeSession: only single-question prompts are supported (got %d)", len(pq.questions))
	}
	opt, ok := pq.optionByID[optionIDs[0]]
	if !ok {
		return fmt.Errorf("claudeSession: unknown option id %s for question %s", optionIDs[0], questionID)
	}
	questionText := pq.questions[opt.questionIndex].Question
	updatedInput := copyStringAnyMap(pq.rawInput)
	if updatedInput == nil {
		updatedInput = map[string]any{}
	}
	updatedInput["answers"] = map[string]any{questionText: opt.label}
	if err := cs.RespondPermission(questionID, core.PermissionResult{
		Behavior:     "allow",
		UpdatedInput: updatedInput,
	}); err != nil {
		return fmt.Errorf("claudeSession: question reply failed: %w", err)
	}
	cs.emitQuestionResolved(questionID, "replied")
	return nil
}

// RejectQuestion cancels a pending Claude AskUserQuestion by delivering a real
// deny control_response with explicit skip wording (approximating the Mac-side
// "Skip" affordance). Claude treats behavior:"deny" as the user declining the
// tool use and continues; it does not hang.
func (cs *claudeSession) RejectQuestion(questionID string) error {
	if questionID == "" {
		return fmt.Errorf("claudeSession: questionID is required")
	}
	if !cs.alive.Load() {
		return fmt.Errorf("session process is not running")
	}
	if _, ok := cs.pendingQuestions.LoadAndDelete(questionID); !ok {
		return fmt.Errorf("claudeSession: no pending question for id %s", questionID)
	}
	if err := cs.RespondPermission(questionID, core.PermissionResult{
		Behavior: "deny",
		Message:  "User skipped the question.",
	}); err != nil {
		return fmt.Errorf("claudeSession: question reject failed: %w", err)
	}
	cs.emitQuestionResolved(questionID, "rejected")
	return nil
}

// pendingClaudeQuestion retains everything needed to build the verified
// control_response for a later question_reply/question_reject.
type pendingClaudeQuestion struct {
	requestID   string
	toolName    string
	rawInput    map[string]any          // original AskUserQuestion input (base for updatedInput)
	questions   []core.UserQuestion      // parsed questions (len==1 for v1)
	optionByID  map[string]pendingOption // synthesized option id -> option detail
	optionOrder []string                 // synthesized option ids in display order
}

// pendingOption maps a synthesized stable option id back to its question + the
// option label the Claude protocol expects in the answers map.
type pendingOption struct {
	questionIndex int    // index into pendingClaudeQuestion.questions
	label         string // option label delivered to Claude as answers[questionText]
}

// optionIDForIndex synthesizes a stable, request-namespaced option id.
// core.UserQuestionOption has no stable id of its own, and option labels may
// repeat across questions, so ids are namespaced by request id and 1-based index.
func optionIDForIndex(requestID string, idx int) string {
	return fmt.Sprintf("%s:option-%d", requestID, idx+1)
}

func anyMultiSelect(questions []core.UserQuestion) bool {
	for _, q := range questions {
		if q.MultiSelect {
			return true
		}
	}
	return false
}

// copyStringAnyMap returns a shallow copy of m. A shallow copy is sufficient
// because the answer path only adds a new top-level "answers" key; it never
// mutates nested structures (the original questions array is preserved as-is).
func copyStringAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// emitAskUserQuestion registers the pending question and emits question_asked.
// Caller must guarantee len(questions)==1 and no multiSelect (v1 single-question).
func (cs *claudeSession) emitAskUserQuestion(requestID string, input map[string]any, questions []core.UserQuestion) {
	q := questions[0]
	questionText := q.Question
	if header := strings.TrimSpace(q.Header); header != "" {
		// iOS question model has no separate header field; fold it into the text.
		questionText = fmt.Sprintf("%s: %s", header, q.Question)
	}

	pq := &pendingClaudeQuestion{
		requestID:   requestID,
		toolName:    "AskUserQuestion",
		rawInput:    input,
		questions:   questions,
		optionByID:  make(map[string]pendingOption, len(q.Options)),
		optionOrder: make([]string, 0, len(q.Options)),
	}
	opts := make([]core.QuestionOption, 0, len(q.Options))
	for i, o := range q.Options {
		id := optionIDForIndex(requestID, i)
		pq.optionByID[id] = pendingOption{questionIndex: 0, label: o.Label}
		pq.optionOrder = append(pq.optionOrder, id)
		opts = append(opts, core.QuestionOption{
			ID:          id,
			Label:       o.Label,
			Description: o.Description,
		})
	}

	// Insert registry entry immediately before emitting so a racing reply finds it.
	cs.pendingQuestions.Store(requestID, pq)

	evt := core.Event{
		Type:         core.EventQuestionAsked,
		SessionID:    cs.CurrentSessionID(),
		QuestionID:   requestID,
		QuestionText: questionText,
		QuestionOpts: opts,
		Required:     true, // AskUserQuestion is a blocking prompt; no optional signal.
		ThreadID:     "",   // Claude has no Codex-style thread id.
	}
	slog.Info("claudeSession: AskUserQuestion emitted as question_asked",
		"request_id", requestID, "options", len(opts))
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
	}
}

// emitQuestionResolved notifies iOS that a pending question was answered or
// cancelled, mirroring the Codex question_resolved event.
func (cs *claudeSession) emitQuestionResolved(questionID, result string) {
	evt := core.Event{
		Type:       core.EventQuestionResolved,
		SessionID:  cs.CurrentSessionID(),
		QuestionID: questionID,
		Content:    result,
	}
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
	}
}

func (cs *claudeSession) writeJSON(v any) error {
	cs.stdinMu.Lock()
	defer cs.stdinMu.Unlock()

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := cs.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}

func isClaudeEditTool(toolName string) bool {
	switch toolName {
	case "Edit", "Write", "NotebookEdit", "MultiEdit":
		return true
	default:
		return false
	}
}

func (cs *claudeSession) setPermissionMode(mode string) {
	cs.permissionMode.Store(mode)
	cs.autoApprove.Store(mode == "bypassPermissions")
	cs.acceptEditsOnly.Store(mode == "acceptEdits")
	cs.dontAsk.Store(mode == "dontAsk")
}

func (cs *claudeSession) SetLiveMode(mode string) bool {
	current, _ := cs.permissionMode.Load().(string)
	if mode == "auto" || mode == "plan" || current == "auto" || current == "plan" {
		return false
	}
	cs.setPermissionMode(mode)
	return true
}

func (cs *claudeSession) Events() <-chan core.Event {
	return cs.events
}

func (cs *claudeSession) WaitForHistoryDrain(ctx context.Context) bool {
	if cs.historyDrainDone == nil {
		return true
	}
	select {
	case <-cs.historyDrainDone:
		return true
	case <-ctx.Done():
		return false
	}
}

func (cs *claudeSession) markHistoryDrained() {
	cs.historyDraining.Store(false)
	if cs.historyDrainDone == nil {
		return
	}
	cs.historyDrainOnce.Do(func() {
		close(cs.historyDrainDone)
	})
}

func (cs *claudeSession) CurrentSessionID() string {
	v, _ := cs.sessionID.Load().(string)
	return v
}

func (cs *claudeSession) Alive() bool {
	return cs.alive.Load()
}

func (cs *claudeSession) Close() error {
	// Drop pending AskUserQuestion state so late question_reply/question_reject
	// calls fail visibly (no pending entry) instead of writing to a dead stdin.
	// Pending state is per-session and not reusable after close.
	cs.pendingQuestions.Range(func(k, _ any) bool {
		cs.pendingQuestions.Delete(k)
		return true
	})

	// Phase 1: Close stdin to signal EOF. Claude Code exits cleanly on
	// stdin close, running Stop hooks (e.g. claude-mem session summary).
	cs.stdinMu.Lock()
	_ = cs.stdin.Close()
	cs.stdinMu.Unlock()

	graceful := cs.gracefulStopTimeout
	if graceful <= 0 {
		graceful = 8 * time.Second // legacy fallback
	}

	select {
	case <-cs.done:
		slog.Info("claudeSession: exited cleanly after stdin close")
		return nil
	case <-time.After(graceful):
		slog.Warn("claudeSession: graceful stop timed out, sending SIGTERM",
			"timeout", graceful)
	}

	// Phase 2: SIGTERM — gives the process a second chance to run
	// cleanup handlers that respond to signals but not stdin EOF. Signal the
	// whole process group so wrapper/sudo/plugin children are reaped too.
	if cs.cmd != nil && cs.cmd.Process != nil {
		_ = signalProcessGroup(cs.cmd, syscall.SIGTERM)
	}

	select {
	case <-cs.done:
		slog.Info("claudeSession: exited after SIGTERM")
		return nil
	case <-time.After(5 * time.Second):
		slog.Warn("claudeSession: SIGTERM timed out, sending SIGKILL")
	}

	// Phase 3: SIGKILL — last resort. Kill the process group so no
	// grandchild (shell wrapper, sudo run_as_user, agent plugin) survives.
	cs.cancel()
	if cs.cmd != nil && cs.cmd.Process != nil {
		_ = forceKillProcessGroup(cs.cmd)
	}
	<-cs.done
	return nil
}

// shellJoinArgs joins args into a single string, quoting any arg that
// contains whitespace so that a shell-style splitter (like my_cli's
// splitCommandLine) preserves each arg as one token.
//
// Uses single quotes because some splitters (e.g. my_cli) don't support
// backslash escapes inside double quotes. For values containing single
// quotes, we close the single-quoted segment, add an escaped single
// quote, and reopen: 'it'\”s' → it's
func shellJoinArgs(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		if !strings.ContainsAny(a, " \t\n\r'\"\\") {
			b.WriteString(a)
			continue
		}
		b.WriteByte('\'')
		for _, c := range a {
			if c == '\'' {
				b.WriteString("'\\''")
			} else {
				b.WriteRune(c)
			}
		}
		b.WriteByte('\'')
	}
	return b.String()
}

// (filterEnv removed: its role is subsumed by core.BuildAgentEnv's deny list,
// which strips CLAUDECODE / CCCODE_* / OPENCODE_SERVER_* from every env layer.)
