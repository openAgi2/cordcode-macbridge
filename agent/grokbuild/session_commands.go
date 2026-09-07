package grokbuild

// session_commands.go — core.SessionCommandCatalog for Grok Build（方案
// docs/2026-09-07-grok-build-slash-command-panel-implementation.md §4.1/§4.2，
// Phase 0 P3 设计期决策：scripts/grokbuild-phase0/EVIDENCE.md）。
//
// List 通道（P3 决策）：catalog 进程 `_x.ai/commands/list`（34 条）≠ 会话 ACU
// （43 条，含 feedback/loop/reload-plugins 等运行时命令），不等价 → List 主通道
// 改定为「专用 child load 后读取会话目录」：每次 ListSessionCommands 都 spawn
// 一个 CordCode 自有短命 child、真实 session/load 该会话、收集 ACU 完整波、
// 关闭并 reap（grokSession.Close 三段回收）。绝不返回缓存冒充刷新；失败返回
// 错误并把该身份标记不可用（Execute 拒绝旧表）。
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// acuSettleQuiet is the quiet window after the latest ACU wave that settles a
// pull: grok 1.0.13 delivers a partial first wave (27) then the full table (43)
// once MCP/plugins finish init (phase0 p3/p7 samples); the LAST wave is the
// authoritative table. Bounded by the caller's ctx (bridge List budget 15s).
var acuSettleQuiet = 1500 * time.Millisecond

var _ core.SessionCommandCatalog = (*Agent)(nil)

// commandPullState captures ACU waves observed by a dedicated pull child.
type commandPullState struct {
	mu    sync.Mutex
	waves [][]core.SessionCommand
	last  time.Time
}

func (p *commandPullState) observe(cmds []core.SessionCommand) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Copy: readLoop hands us the parsed slice, but the puller reads it after
	// settle; own storage avoids aliasing concerns.
	cp := make([]core.SessionCommand, len(cmds))
	copy(cp, cmds)
	p.waves = append(p.waves, cp)
	p.last = time.Now()
}

func (p *commandPullState) latest() ([]core.SessionCommand, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.waves) == 0 {
		return nil, false
	}
	return p.waves[len(p.waves)-1], true
}

func (p *commandPullState) lastAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

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
// catalog via a dedicated short-lived child (spawn → initialize/authenticate →
// session/load → collect ACU waves → settle on the last → close+reap). The
// returned list is the D1 admitted subset (official ∩ verified feedback types;
// empty until P6 admission lands). Errors are real errors — no cached fallback.
func (a *Agent) ListSessionCommands(ctx context.Context, sessionID string) ([]core.SessionCommand, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("grokbuild: list session commands: empty session id")
	}
	cwd := a.resolveSessionCwd(sessionID)
	if cwd == "" {
		return nil, fmt.Errorf("grokbuild: list session commands: cannot resolve cwd for session %s", shortID(sessionID))
	}

	pull := &commandPullState{}
	s, err := newGrokSessionACU(ctx, a, sessionID, pull.observe)
	if err != nil {
		a.acu.markListFailed(sessionID, cwd)
		return nil, fmt.Errorf("grokbuild: list pull child: %w", err)
	}
	defer s.Close()

	// Settle: ≥1 ACU wave, then a quiet window with no further wave (grok sends
	// a partial first wave, then the full table once MCP/plugins finish init —
	// phase0 p3/p7 samples); all bounded by ctx. If ctx expires or the child
	// exits after ≥1 wave, the latest observed wave is still a real official
	// table and is accepted; zero waves is a hard failure.
	for {
		if _, ok := pull.latest(); ok && !time.Now().Before(pull.lastAt().Add(acuSettleQuiet)) {
			break
		}
		select {
		case <-ctx.Done():
			if _, ok := pull.latest(); !ok {
				a.acu.markListFailed(sessionID, cwd)
				return nil, fmt.Errorf("grokbuild: list pull: no ACU observed before ctx done: %w", ctx.Err())
			}
		case <-s.done:
			if _, ok := pull.latest(); !ok {
				a.acu.markListFailed(sessionID, cwd)
				return nil, errors.New("grokbuild: list pull: child exited before delivering ACU")
			}
		case <-time.After(100 * time.Millisecond):
			continue
		}
		break
	}

	cmds, ok := pull.latest()
	if !ok {
		a.acu.markListFailed(sessionID, cwd)
		return nil, errors.New("grokbuild: list pull: no ACU wave collected")
	}
	a.acu.storeListSuccess(sessionID, cwd, cmds)
	return applyGrokAdmission(cmds), nil
}

// ExecuteSessionCommand runs one official slash line for the session on the
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
	if !strings.HasPrefix(line, "/") || strings.ContainsAny(line, "\r\n") {
		return core.SessionCommandResult{}, errors.New("grokbuild: execute: line must be a single official slash line")
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
	select {
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
