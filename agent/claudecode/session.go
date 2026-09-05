package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
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
	childStreams     *claudeChildStreamTracker
	currentStream    childStreamScope

	// claudeUserInputReg is the single first-writer-wins registry for every
	// AskUserQuestion interaction (pending → claimed → resolved). Both the v2
	// resolve_user_input RPC and the legacy question_reply/question_reject RPCs
	// enter this registry; there is no independently writable v1 registry.
	claudeUserInputReg *claudeUserInputRegistry

	// 发送侧控制协议（control_channel.go）：request_id → 等待中的响应通道。
	ctrlReqSeq       atomic.Int64
	ctrlMu           sync.Mutex
	ctrlPending      map[string]chan controlResponse
	initCapabilities atomic.Value // stores map[string]struct{} — system/init capabilities（随首个 turn 出现）
	onAssistantModel func(requested, observed string)

	// client uuid turn 身份（owner 2026-09-05 复盘，官方 SDK user_message_uuid 契约）：
	// Send 在输入 user 帧自带 uuid（CLI 2.1.234 真样本实证：transcript user 行采纳该
	// uuid、result 帧回盖 user_message_uuid）——file-relay 侧 turn 身份与本侧 stdout
	// 事件身份天然统一，不再依赖 ActiveTurnID 反查。active = 当前进行中 turn 的 uuid；
	// pending = turn 进行中再次 Send 时 CLI queue 的后续 uuid（FIFO）。result 收口时
	// 按 user_message_uuid 消费到匹配位置。selfTurnUUIDs 是本 epoch 全部自持 uuid
	// （settle 后保留——晚到的同 turn 文件行仍属 stdout 权威；rollback 移除），供
	// go-bridge 判定「stdout 单源模型」只对自有 turn 成立，外部进程写入同一
	// transcript 的回合不经本 stdout，file-relay 必须继续供正文。
	clientTurnMu       sync.Mutex
	activeClientUUID   string
	pendingClientUUIDs []string
	selfTurnUUIDs      map[string]struct{}

	model            string
	maxContextTokens int
	usageMu          sync.Mutex
	lastUsage        *core.ContextUsage

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

func newClaudeSession(ctx context.Context, workDir, cliBin string, cliExtraArgs []string, cliArgsFlag string, model, effort, sessionID, mode string, allowedTools, disallowedTools []string, extraEnv []string, platformPrompt string, disableVerbose bool, spawnOpts core.SpawnOptions, maxContextTokens int, hookSettings string) (*claudeSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	// innerArgs are Claude Code CLI flags — when a wrapper is used with
	// cliArgsFlag these get bundled into a single passthrough string.
	// outerArgs are flags the wrapper itself understands (e.g. --model).
	innerArgs := baseClaudeInnerArgs(disableVerbose)

	if hookSettings != "" {
		// Phase 3：仅自 spawn 会话注入 --settings 内联 HTTP hooks（指向本地
		// Management API；hooks 数组跨层 merge，不打掉用户层 hooks——Phase 0
		// 实证）。空=纯轮询=现状。
		innerArgs = append(innerArgs, "--settings", hookSettings)
	}

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
		model:               model,
		maxContextTokens:    maxContextTokens,
		ctx:                 sessionCtx,
		cancel:              cancel,
		done:                make(chan struct{}),
		historyDrainDone:    make(chan struct{}),
		gracefulStopTimeout: 120 * time.Second,
		childStreams:        newClaudeChildStreamTracker(),
		claudeUserInputReg:  newClaudeUserInputRegistry(),
	}
	cs.setPermissionMode(mode)
	cs.sessionID.Store(sessionID)
	cs.alive.Store(true)

	// historyDraining: --resume 启动后的历史重放防御期。
	// 仅对明确 resume 已知 session id 时开启；空 sessionID 或 ContinueSession 不开启。
	//
	// 关闭时序（owner 2026-09-05 复盘后）：正常路径由 handleStreamEvent 收到首条
	// stream_event 时关闭（CLI 2.1.234 真样本：--resume 不重放历史；stream_event 只
	// 属于新 turn）。handleResult 兜底关闭；12s watchdog 是最后兜底（go-bridge 已移除
	// 同步 drainHistoryEvents 等待，此 watchdog 不再与它配对，仅防真实卡死）。
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
			case cs.events <- cs.scopeEvent(evt):
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
	case cs.events <- cs.scopeEvent(evt):
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
	cs.currentStream = cs.childStreams.observe(raw)
	defer func() { cs.currentStream = childStreamScope{} }()

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
	case "control_response":
		cs.dispatchControlResponse(raw)
	case "control_cancel_request":
		requestID, _ := raw["request_id"].(string)
		slog.Debug("claudeSession: permission cancelled", "request_id", requestID)
	}
}

func (cs *claudeSession) handleSystem(raw map[string]any) {
	switch raw["subtype"] {
	case "init":
		// capabilities（含 interrupt_receipt_v1 等）只在首个 turn 的 system/init
		// 出现（Phase 0 实证）；入库供控制操作做能力门。
		cs.storeInitCapabilities(raw)
	case "status":
		cs.syncPermissionModeFromStatus(raw)
	}
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
		case cs.events <- cs.scopeEvent(evt):
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
	if usage, ok := msg["usage"].(map[string]any); ok {
		model, _ := msg["model"].(string)
		if model == "" {
			model = cs.model
		}
		cs.emitContextUsage(usage, model)
		// 目录观测层（Phase 1）：assistant message.model 是唯一可靠的执行侧
		// 模型真值（网关改写后），catalog 高亮与 observedModel 键来自这里。
		if observed, _ := msg["model"].(string); observed != "" && cs.onAssistantModel != nil {
			cs.onAssistantModel(cs.model, observed)
		}
	}
	contentArr, ok := msg["content"].([]any)
	if !ok {
		return
	}

	msgID, _ := msg["id"].(string)
	// Capture the source-proven assistant identity even when the message contains
	// only AskUserQuestion tool_use and no text block. The old code updated
	// activeMsgID only from text, so a tool-only question could be emitted with an
	// empty turnId and be dropped by the ProjectionReducer.
	if msgID != "" {
		cs.activeMsgID.Store(msgID)
	}
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
		evt := core.Event{Type: core.EventTextReplace, Content: fullText, TurnID: cs.currentClientTurnID()}
		select {
		case cs.events <- cs.scopeEvent(evt):
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
			input, _ := item["input"].(map[string]any)
			evt := core.Event{Type: core.EventToolUse, ToolName: toolName, ToolInput: inputSummary, ToolInputRaw: input, RequestID: toolUseID, TurnID: cs.currentClientTurnID()}
			select {
			case cs.events <- cs.scopeEvent(evt):
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
				evt := core.Event{Type: core.EventThinking, Content: thinking, TurnID: cs.currentClientTurnID()}
				select {
				case cs.events <- cs.scopeEvent(evt):
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
				evt := core.Event{Type: core.EventText, Content: delta, TurnID: cs.currentClientTurnID()}
				select {
				case cs.events <- cs.scopeEvent(evt):
				case <-cs.ctx.Done():
					return
				}
			}
		}
	}
}

func (cs *claudeSession) handleStreamEvent(raw map[string]any) {
	if cs.historyDraining.Load() {
		// 事件驱动关闭（owner 2026-09-05 复盘）：CLI 2.1.234 真样本（resume 探针，
		// /tmp 探针 frames）证明 --resume 不重放历史到 stdout——重放帧（若未来版本
		// 出现）是完整 assistant/user 帧，走 handleAssistant/handleUser 的 drain 门，
		// 不经此处；stream_event 只在新 turn 生成时出现。因此首条 stream_event 即
		// 当前 turn 的首个输出，立即关闭 drain 窗口，流式头几帧不再被 12s watchdog
		// 侥幸窗口丢弃。go-bridge 侧同步 drainHistoryEvents 已一并移除（10s 白等）。
		cs.markHistoryDrained()
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
		if id != "" {
			cs.activeMsgID.Store(id)
		}
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

// rollbackClientTurn 撤销一次未成功写出的 Send 记账（writeJSON 失败 = 帧未达
// CLI，uuid 不应占用队列位）。
func (cs *claudeSession) rollbackClientTurn(id string) {
	cs.clientTurnMu.Lock()
	defer cs.clientTurnMu.Unlock()
	delete(cs.selfTurnUUIDs, id)
	if cs.activeClientUUID == id {
		if len(cs.pendingClientUUIDs) > 0 {
			cs.activeClientUUID = cs.pendingClientUUIDs[0]
			cs.pendingClientUUIDs = cs.pendingClientUUIDs[1:]
		} else {
			cs.activeClientUUID = ""
		}
		return
	}
	for i, queued := range cs.pendingClientUUIDs {
		if queued == id {
			cs.pendingClientUUIDs = append(cs.pendingClientUUIDs[:i], cs.pendingClientUUIDs[i+1:]...)
			return
		}
	}
}

// newClientTurnUUID 生成 v4 形态 UUID 作为输入 user 帧的 client uuid。
// CLI 对该字段的消费已由 2.1.234 真样本实证（transcript 采纳 + result 回盖）。
func newClientTurnUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败在本机属不可恢复环境错误；退化为时间戳 ID 仍保证唯一，
		// 但失去标准 UUID 形态——CLI 是否接受未证明，保留 panic 语义更诚实。
		panic("crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// registerClientTurn 为一次 Send 分配 client uuid：无 active turn 时立即占用，
// 否则进 FIFO 队列（CLI 会把 turn 进行中的后续消息 queue 成后续 turn）。
func (cs *claudeSession) registerClientTurn() string {
	id := newClientTurnUUID()
	cs.clientTurnMu.Lock()
	defer cs.clientTurnMu.Unlock()
	if cs.selfTurnUUIDs == nil {
		cs.selfTurnUUIDs = make(map[string]struct{})
	}
	cs.selfTurnUUIDs[id] = struct{}{}
	if cs.activeClientUUID == "" {
		cs.activeClientUUID = id
	} else {
		cs.pendingClientUUIDs = append(cs.pendingClientUUIDs, id)
	}
	return id
}

// OwnsClientTurn 实现 core.ClientTurnOwner：turn 身份（= transcript user 行 uuid）
// 是否由本会话进程发起（本 epoch 自持集）。settle 后保留——晚到的同 turn assistant
// 文件行仍属 stdout 权威，不得因 result 已收口就放行 file-relay 双份。
func (cs *claudeSession) OwnsClientTurn(turnUUID string) bool {
	if turnUUID == "" {
		return false
	}
	cs.clientTurnMu.Lock()
	defer cs.clientTurnMu.Unlock()
	_, ok := cs.selfTurnUUIDs[turnUUID]
	return ok
}

// currentClientTurnID 返回当前进行中 turn 的 client uuid（stdout 事件的 turnId）。
func (cs *claudeSession) currentClientTurnID() string {
	cs.clientTurnMu.Lock()
	defer cs.clientTurnMu.Unlock()
	return cs.activeClientUUID
}

// settleClientTurn 在 result 收口时消费 client uuid 队列：stamped 是 CLI 回盖的
// user_message_uuid（2.1.234 真样本：result 帧恒带）。匹配 active 或队列成员时，
// 消费到该位置（含）并让下一个排队 uuid 接管；无 stamp 的老 producer 保守清空
// active（防下一个 turn 串位）。返回本次收口 turn 的 uuid（供 EventResult 绑定）。
func (cs *claudeSession) settleClientTurn(stamped string) string {
	cs.clientTurnMu.Lock()
	defer cs.clientTurnMu.Unlock()
	settled := cs.activeClientUUID
	popNext := func() {
		if len(cs.pendingClientUUIDs) > 0 {
			cs.activeClientUUID = cs.pendingClientUUIDs[0]
			cs.pendingClientUUIDs = cs.pendingClientUUIDs[1:]
		} else {
			cs.activeClientUUID = ""
		}
	}
	if stamped == "" {
		if cs.activeClientUUID != "" {
			slog.Warn("claudeSession: result frame without user_message_uuid; clearing active client turn", "activeClientUUID", cs.activeClientUUID)
		}
		popNext()
		return settled
	}
	if stamped == cs.activeClientUUID {
		popNext()
		return settled
	}
	for i, queued := range cs.pendingClientUUIDs {
		if queued == stamped {
			// CLI 合并了 prompt batch（SDK user_message_uuids 契约），中途 uuid
			// 不再会有独立 turn——消费到 stamped（含），其后排队者接管。
			cs.pendingClientUUIDs = cs.pendingClientUUIDs[i+1:]
			popNext()
			return stamped
		}
	}
	// stamp 不认识（外部注入的 turn？）：不动队列，仅告警——归属链由 file-relay 兜底。
	slog.Warn("claudeSession: result user_message_uuid matches no known client turn", "stamped", stamped, "activeClientUUID", cs.activeClientUUID)
	return settled
}

func (cs *claudeSession) emitTextDelta(index int, text string) {
	if text == "" || cs.historyDraining.Load() {
		return
	}
	cs.streamState.ensure()
	cs.streamState.streamedTextByIdx[index] += text
	evt := core.Event{Type: core.EventText, Content: text, TurnID: cs.currentClientTurnID()}
	select {
	case cs.events <- cs.scopeEvent(evt):
	case <-cs.ctx.Done():
		return
	}
}

func (cs *claudeSession) emitThinkingDelta(thinking string) {
	if thinking == "" || cs.historyDraining.Load() {
		return
	}
	evt := core.Event{Type: core.EventThinking, Content: thinking, TurnID: cs.currentClientTurnID()}
	select {
	case cs.events <- cs.scopeEvent(evt):
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
			contentValue := item["content"]
			content, _ := contentValue.(string)
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
				TurnID:      cs.currentClientTurnID(),
				ToolMatches: parseClaudeToolMatches(toolName, contentValue, isError),
			}
			select {
			case cs.events <- cs.scopeEvent(evt):
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

	// 官方收口校验（client uuid 契约）：result 帧回盖 user_message_uuid，据消费
	// client turn 队列（匹配/无 stamp 两种路径见 settleClientTurn）。settled 是
	// 本 turn 的 client uuid——EventResult 以它作 turnId，投影精确收口该 turn。
	stamped, _ := raw["user_message_uuid"].(string)
	settled := cs.settleClientTurn(stamped)

	evt := core.Event{
		Type:         core.EventResult,
		Content:      content,
		SessionID:    cs.CurrentSessionID(),
		TurnID:       settled,
		Done:         true,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	select {
	case cs.events <- cs.scopeEvent(evt):
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

	// AskUserQuestion always enters the canonical structured-input engine before
	// permission-mode bypass. Legacy wire presentation is derived later from the
	// same interaction; it is not a second adapter/registry path.
	if StructuredUserInputReady && toolName == "AskUserQuestion" {
		cs.handleAskUserQuestionV2(requestID, input)
		return
	}

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

	slog.Info("claudeSession: permission request", "request_id", requestID, "tool", toolName)
	evt := core.Event{
		Type:         core.EventPermissionRequest,
		RequestID:    requestID,
		ToolName:     toolName,
		ToolInput:    summarizeInput(toolName, input),
		ToolInputRaw: input,
	}
	// ExitPlanMode（plan approval layer, 2026-09-04）：plan mode 的审批门。计划全文在
	// input.plan（实测 7.5KB 级 markdown），planFilePath 是本地第二来源；
	// allowedPrompts 已废弃、忽略（调研档 §3.2）。升级为 plan_review 专用卡，
	// 不再把全文塞进通用权限卡的 toolInput。
	if toolName == "ExitPlanMode" {
		evt.PermissionKind = "plan_review"
		evt.PermissionActions = []string{"approve", "requestChanges", "quit"}
		evt.PlanReview = &core.PlanPayload{
			Content:      strVal(input, "plan"),
			PlanFilePath: strVal(input, "planFilePath"),
		}
	}

	select {
	case cs.events <- cs.scopeEvent(evt):
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

	// client uuid（官方 SDK user_message_uuid 契约）：输入 user 帧自带 uuid，
	// CLI 把它写进 transcript user 行并在 result 帧回盖 user_message_uuid——
	// stdout 事件身份与 file-relay turn 身份由此统一（owner 2026-09-05 复盘）。
	clientTurnUUID := cs.registerClientTurn()

	if len(images) == 0 && len(files) == 0 {
		if err := cs.writeJSON(map[string]any{
			"type":    "user",
			"uuid":    clientTurnUUID,
			"message": map[string]any{"role": "user", "content": prompt},
		}); err != nil {
			cs.rollbackClientTurn(clientTurnUUID)
			return err
		}
		return nil
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

	if err := cs.writeJSON(map[string]any{
		"type":    "user",
		"uuid":    clientTurnUUID,
		"message": map[string]any{"role": "user", "content": parts},
	}); err != nil {
		cs.rollbackClientTurn(clientTurnUUID)
		return err
	}
	return nil
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
	return cs.respondPermissionContext(context.Background(), requestID, result)
}

func (cs *claudeSession) respondPermissionContext(ctx context.Context, requestID string, result core.PermissionResult) error {
	if !cs.alive.Load() {
		return fmt.Errorf("session process is not running")
	}

	var permResponse map[string]any
	// "always" (opencode-web official reply) is not a Claude concept; treat it
	// as a one-time allow instead of falling through to the deny branch.
	if result.Behavior == "allow" || result.Behavior == "always" {
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
			// Plan-review deny copy（方案 §4.3）：deny.message 是官方指定的反馈回
			// 模型通道且必填——requestChanges 空反馈与 quit 各用固定文案区分语义
			//（claude wire 上两动作同为 deny，差异只在文案，调研档 §3.5）。
			switch result.PlanAction {
			case "requestChanges":
				msg = "The user rejected the plan and asked to keep planning. No specific feedback was provided."
			case "quit":
				msg = "The user dismissed the plan review."
			default:
				msg = "The user denied this tool use. Stop and wait for the user's instructions."
			}
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
	return cs.writeJSONContext(ctx, controlResponse)
}

// RespondQuestion is the legacy transport adapter over the canonical structured-input registry.
// It accepts the historical request-scoped option id and competes for the same first-writer-wins
// claim as resolve_user_input; it never owns a second pending-question registry.
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
	snap, status := cs.claudeUserInputReg.SnapshotByRequest(questionID)
	if status != claudeUIPending || snap == nil {
		return fmt.Errorf("claudeSession: no pending question for id %s", questionID)
	}
	if len(snap.questionOrder) != 1 || snap.questionMode[snap.questionOrder[0]] != core.UserInputAnswerModeSingle {
		return fmt.Errorf("claudeSession: legacy reply only supports one single-select question")
	}
	var canonicalOptionID string
	for i, opt := range snap.questionOpts[snap.questionOrder[0]] {
		if optionIDForIndex(questionID, i) == optionIDs[0] {
			canonicalOptionID = opt.id
			break
		}
	}
	if canonicalOptionID == "" {
		return fmt.Errorf("claudeSession: unknown option id %s for question %s", optionIDs[0], questionID)
	}
	_, err := cs.resolveUserInput(cs.ctx, snap.interactionID, "legacy:"+questionID+":answer", core.UserInputActionAnswer, []core.UserInputAnswer{{
		QuestionID: claudeQuestionID(snap.interactionID, 0),
		Values:     []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: canonicalOptionID}},
	}}, "mac")
	if err != nil {
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
	snap, status := cs.claudeUserInputReg.SnapshotByRequest(questionID)
	if status != claudeUIPending || snap == nil {
		return fmt.Errorf("claudeSession: no pending question for id %s", questionID)
	}
	if _, err := cs.resolveUserInput(cs.ctx, snap.interactionID, "legacy:"+questionID+":reject", core.UserInputActionReject, nil, "mac"); err != nil {
		return fmt.Errorf("claudeSession: question reject failed: %w", err)
	}
	cs.emitQuestionResolved(questionID, "rejected")
	return nil
}

// optionIDForIndex synthesizes a stable, request-namespaced option id.
// core.UserQuestionOption has no stable id of its own, and option labels may
// repeat across questions, so ids are namespaced by request id and 1-based index.
func optionIDForIndex(requestID string, idx int) string {
	return fmt.Sprintf("%s:option-%d", requestID, idx+1)
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

// emitLegacyAskUserQuestion is a one-way presentation derived after the canonical request has
// been registered and emitted. Prompts that the v1 shape cannot represent remain v2-only.
func (cs *claudeSession) emitLegacyAskUserQuestion(requestID string, questions []core.UserQuestion) {
	if len(questions) != 1 || questions[0].MultiSelect {
		return
	}
	q := questions[0]
	questionText := q.Question
	if header := strings.TrimSpace(q.Header); header != "" {
		// iOS question model has no separate header field; fold it into the text.
		questionText = fmt.Sprintf("%s: %s", header, q.Question)
	}

	opts := make([]core.QuestionOption, 0, len(q.Options))
	for i, o := range q.Options {
		id := optionIDForIndex(requestID, i)
		opts = append(opts, core.QuestionOption{
			ID:          id,
			Label:       o.Label,
			Description: o.Description,
		})
	}

	evt := core.Event{
		Type:         core.EventQuestionAsked,
		SessionID:    cs.CurrentSessionID(),
		QuestionID:   requestID,
		QuestionText: questionText,
		QuestionOpts: opts,
		Required:     true, // AskUserQuestion is a blocking prompt; no optional signal.
		ThreadID:     "",   // Claude has no Codex-style thread id.
	}
	slog.Info("claudeSession: canonical AskUserQuestion derived as legacy question_asked",
		"request_id", requestID, "options", len(opts))
	select {
	case cs.events <- cs.scopeEvent(evt):
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
	case cs.events <- cs.scopeEvent(evt):
	case <-cs.ctx.Done():
	}
}

func (cs *claudeSession) writeJSON(v any) error {
	return cs.writeJSONContext(context.Background(), v)
}

func (cs *claudeSession) writeJSONContext(ctx context.Context, v any) error {
	for !cs.stdinMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	defer cs.stdinMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if deadlineWriter, ok := cs.stdin.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = deadlineWriter.SetWriteDeadline(deadline)
			defer deadlineWriter.SetWriteDeadline(time.Time{})
		}
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
	if cs.claudeUserInputReg != nil {
		cs.claudeUserInputReg.Clear()
	}

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
