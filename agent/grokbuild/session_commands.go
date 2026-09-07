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
// Execute（§4.2）：Grok 命令是 prompt 语义（slash_exec 走 session/prompt），必须
// 经共用 turn dispatcher 完整生命周期（1b 交付）。本文件先落 fail-closed 占位：
// 未启用即明确报错，绝不降级为普通消息发送。
//
// readiness（§5.1）：session_commands capability 不能只靠类型断言广告；目录 +
// 执行两门都通过（P6 准入非空 + Execute 落地）前 SessionCommandsReady()==false，
// go-bridge 不向 iOS 画「/」按钮。

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

// ExecuteSessionCommand runs one official slash line for the session. Grok
// commands are prompt-semantics host actions and must ride the shared turn
// dispatcher (§4.2, delivered with 1b) — until that lands this fails closed.
// It must NEVER degrade to sending the line as a plain user message.
func (a *Agent) ExecuteSessionCommand(ctx context.Context, sessionID, line string) (core.SessionCommandResult, error) {
	return core.SessionCommandResult{}, errors.New("grokbuild: session command execution not enabled (turn dispatcher pending)")
}

// --- readiness gate (§5.1) ---

// grokCommandsReady gates both the catalog and the execute path. Catalog gate:
// admission set non-empty (P6 evidence landed). Execute gate: the fail-closed
// stub above replaced by the real dispatcher. Both flip together with 1b+P6.
var grokCommandsReady atomic.Bool

// SessionCommandsReady implements core.SessionCommandReadiness: false →
// go-bridge must NOT advertise session_commands for grokbuild even though the
// type assertion succeeds (能力不能仅靠类型断言，§5.1).
func (a *Agent) SessionCommandsReady() bool {
	return grokCommandsReady.Load()
}
