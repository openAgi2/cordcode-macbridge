package grokbuild

// session_commands.go — core.SessionCommandCatalog for Grok Build（方案
// docs/2026-09-07-grok-build-slash-command-panel-implementation.md §4.1/§4.2）。
//
// List 通道（2026-09-07 重做）：进程级单例 catalog 子进程上的官方
// `_x.ai/commands/list {cwd}` ext RPC（grok-desktop 在 session start 后用的
// 同一通道，session_admin.rs cwd 分支；不需要会话加载进本进程）——与 dsh-web
// 的 commands/list 同构：常驻连接 + 单次 RPC，无缓存、无子进程、无 ACU 静默窗。
// 旧「专用 child session/load + ACU settle」通道生产实测 8-10s（owner 报障
// 「点 ➕ 十几秒」），已整体移除。红线不变：每次 List 都是一次真实官方拉取，
// 绝不返回缓存冒充刷新；失败返回错误并把该身份标记不可用（Execute 拒绝旧表）。
// 通道证据与隔离探针（43ms 无会话进程）：catalog_commands_list.go 头注 +
// scripts/grokbuild-phase0/（p9）。
//
// Execute（§4.2）：Grok 命令是 prompt 语义（slash_exec 走 session/prompt），
// 必须经共用 turn dispatcher 完整生命周期（turn_dispatch.go，p1b 交付）——在
// 会话自己的活 actor 上执行，官方反馈正文经同一 Events 轨流出。绝不降级为
// 普通消息发送，也绝不为执行另起 child。
//
// readiness（§5.1）：session_commands capability 不能只靠类型断言广告；p1b 后
// 目录 + 执行两门都已通过（P6 准入非空 + 真实 dispatcher），grokCommandsReady
// 翻真，go-bridge 开始向 iOS 画「/」按钮。

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// acuSettleQuiet 及专用 child pull 的波收集（commandPullState）已随 2026-09-07
// List 通道重做移除：catalog 单例 `_x.ai/commands/list` 是同步 RPC，无波可等。

var _ core.SessionCommandCatalog = (*Agent)(nil)

// resolveSessionCwd resolves the cwd a session belongs to: the on-disk
// summary.json directory (authoritative, mirrors loadSession's fallback), else
// the agent workdir snapshot. 一轮操作一次性捕获 cwd（§4.1），不读可被其他会话
// 修改的 agent 全局临时值。
func (a *Agent) resolveSessionCwd(sessionID string) string {
	a.mu.RLock()
	home := a.grokHomeLocked()
	fallback := a.workDir
	a.mu.RUnlock()
	if home != "" {
		if dir := findSessionDir(home, sessionID); dir != "" {
			if info, ok := parseSummaryFile(filepath.Join(dir, "summary.json")); ok && info.Directory != "" {
				return info.Directory
			}
		}
	}
	if fallback == "" || fallback == "." {
		return ""
	}
	return fallback
}

// ListSessionCommands performs a real official pull for the session's command
// catalog: one `_x.ai/commands/list {cwd}` ext RPC on the persistent catalog
// singleton (the channel grok-desktop itself uses after session start). The
// returned list is the D1 admitted subset (official ∩ owner-ruled admission).
// Errors are real errors — no cached fallback, and the identity is marked
// unavailable so Execute refuses the stale table.
func (a *Agent) ListSessionCommands(ctx context.Context, sessionID string) ([]core.SessionCommand, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("grokbuild: list session commands: empty session id")
	}
	cwd := a.resolveSessionCwd(sessionID)
	if cwd == "" {
		return nil, fmt.Errorf("grokbuild: list session commands: cannot resolve cwd for session %s", shortID(sessionID))
	}

	client, err := a.catalogClientInstance(ctx)
	if err != nil {
		a.acu.markListFailed(sessionID, cwd)
		return nil, fmt.Errorf("grokbuild: list pull catalog process: %w", err)
	}
	cmds, err := client.catalogListCommands(ctx, cwd)
	if err != nil {
		a.acu.markListFailed(sessionID, cwd)
		return nil, fmt.Errorf("grokbuild: list pull: %w", err)
	}
	a.acu.storeListSuccess(sessionID, cwd, cmds)
	return applyGrokAdmission(cmds), nil
}

// ExecuteSessionCommand runs one official slash command payload for the session on the
// session's OWN live actor via the shared turn dispatcher (§4.2): the slash
// line is prompt semantics (`session/prompt`), the official feedback body
// arrives as hostTurn agent_message_chunk on the same Events rail the chat
// consumes, and the dispatcher's terminal future settles the RPC. It must
// NEVER degrade to a plain user message send and never rides a fresh child.
//
// Admission (D1, ADMISSION.md): the line's command must be in the P6-verified
// admitted set AND present in the session's cached official catalog (a real
// List pull must have succeeded for this identity — no stale-table execute).
func (a *Agent) ExecuteSessionCommand(ctx context.Context, sessionID, line string) (core.SessionCommandResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	line = strings.TrimSpace(line)
	if sessionID == "" {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: empty session id")
	}
	if !strings.HasPrefix(line, "/") {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: payload must start with an official slash command")
	}
	name := slashCommandName(line)
	if name == "" {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: line has no command name")
	}
	if _, excluded := grokExcludedCommands[name]; excluded {
		return core.SessionCommandResult{}, fmt.Errorf("grokbuild: command %q is excluded from the panel (side effects not isolated)", name)
	}
	if _, admitted := grokAdmittedCommands[name]; !admitted {
		return core.SessionCommandResult{}, fmt.Errorf("grokbuild: command %q is not admitted (no verified feedback sample)", name)
	}
	if a.acu == nil {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: command catalog unavailable")
	}
	cwd := a.resolveSessionCwd(sessionID)
	whitelist, ok := a.acu.executeWhitelist(sessionID, cwd)
	if !ok {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: no official catalog for this session (open the command list first)")
	}
	inCatalog := false
	for _, c := range whitelist {
		if c.Name == name {
			inCatalog = true
			break
		}
	}
	if !inCatalog {
		return core.SessionCommandResult{}, fmt.Errorf("grokbuild: command %q is not in the session's official catalog", name)
	}
	s, ok := a.liveSessionForCommand(sessionID)
	if !ok {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: no live session actor — send a message in the session first")
	}
	return s.executeHostCommand(ctx, line)
}

// slashCommandName extracts the command token from "/name args…" (no slash).
func slashCommandName(line string) string {
	fields := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// executeHostCommand dispatches one admitted slash line through the shared
// turn dispatcher and waits on the terminal future (ctx-bounded). end_turn →
// success with the collected hostTurn body as resultText (§7 正文组: 原正文，
// 不弹业务错误窗)；cancelled/其他终态 → error。ctx 到期即尽力取消并返回
// (取消与清理)。
func (s *grokSession) executeHostCommand(ctx context.Context, line string) (core.SessionCommandResult, error) {
	if !s.alive.Load() {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: session actor not alive")
	}
	wait, err := s.dispatchTurn([]contentBlock{{Type: "text", Text: line}})
	if err != nil {
		return core.SessionCommandResult{}, err
	}
	name := slashCommandName(line)
	var goalCreated <-chan string
	if name == "goal" && grokGoalCreationCommand(line) {
		goalCreated = s.turn.goalCreatedSignal()
	}
	select {
	case objective := <-goalCreated:
		// Goal execution is detached by Grok after durable creation. Return the
		// command RPC immediately, but deliberately keep the dispatcher lease:
		// the same actor continues streaming goal rounds and owns the eventual
		// session/prompt response. This is the key difference from a timeout,
		// which explicitly cancels the turn below.
		goalID := ""
		if s.updateState != nil {
			s.updateState.mu.Lock()
			goalID = s.updateState.goal.ID
			s.updateState.mu.Unlock()
		}
		if goalID != "" {
			args := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "/goal"))
			s.emit(core.Event{Type: core.EventSessionCommand, SessionCommand: &core.SessionCommandEvent{
				CommandID: "grok-goal:" + goalID,
				Name:      "goal", Args: args, Kind: "success", InputLine: strings.TrimSpace(line),
			}})
		}
		return core.SessionCommandResult{ResultKind: "success", ResultText: objective}, nil
	case out := <-wait:
		if out.Err != nil {
			return core.SessionCommandResult{}, out.Err
		}
		switch out.StopReason {
		case stopReasonEndTurn:
			return core.SessionCommandResult{
				ResultKind: "success",
				ResultText: strings.TrimSpace(out.HostText),
			}, nil
		case stopReasonCancelled:
			cat := out.CancellationCategory
			if cat == "" {
				cat = "unknown"
			}
			return core.SessionCommandResult{}, fmt.Errorf("grokbuild: command cancelled (%s)", cat)
		default:
			// max_tokens / refusal / unforeseen: honest failure, no fake success.
			return core.SessionCommandResult{}, fmt.Errorf("grokbuild: command turn ended with %q", out.StopReason)
		}
	case <-ctx.Done():
		// Best-effort cancel so the leased slot frees and the turn terminal
		// still flows through Events (mirrors cancel-then-cleanup).
		_ = s.CancelTurn(ctx)
		return core.SessionCommandResult{}, ctx.Err()
	}
}

func grokGoalCreationCommand(line string) bool {
	args := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "/goal"))
	if args == "" {
		return false
	}
	first := strings.ToLower(strings.Fields(args)[0])
	switch first {
	case "status", "pause", "resume", "clear":
		return false
	default:
		return true
	}
}

// --- readiness gate (§5.1) ---

// grokCommandsReady gates both the catalog and the execute path. Flipped true
// with p1b: List is a real dedicated-child pull (1a) + P6 admission is
// non-empty (5 hooks-*) + Execute rides the real shared turn dispatcher
// (turn_dispatch.go). The atomic stays so the gate mechanism (and its test)
// remain honest — a future regression can still close it.
var grokCommandsReady atomic.Bool

func init() {
	grokCommandsReady.Store(true)
}

// SessionCommandsReady implements core.SessionCommandReadiness: false →
// go-bridge must NOT advertise session_commands for grokbuild even though the
// type assertion succeeds (能力不能仅靠类型断言，§5.1).
func (a *Agent) SessionCommandsReady() bool {
	return grokCommandsReady.Load()
}
