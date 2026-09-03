package grokbuild

// Grok Build CLI driver for MacBridge.
// Uses `grok agent stdio` (ACP v1 JSON-RPC over stdin/stdout) to communicate
// with the Grok Build CLI. See docs/2026-07-12-grok-driver-design.md for the
// full design and docs/2026-07-12-grok-cli-compatibility-evidence.md for Gate 0
// evidence.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.Agent = (*Agent)(nil)
var _ core.DiagnosticsProvider = (*Agent)(nil)
var _ core.WorkDirSwitcher = (*Agent)(nil)
var _ core.ModeSwitcher = (*Agent)(nil)
var _ core.ModelSwitcher = (*Agent)(nil)
var _ core.ModelEffortCatalog = (*Agent)(nil)
var _ core.ProviderSwitcher = (*Agent)(nil)
var _ core.ReasoningEffortSwitcher = (*Agent)(nil)
var _ core.ToolAuthorizer = (*Agent)(nil)
var _ core.HistoryProvider = (*Agent)(nil)
var _ core.RichHistoryProvider = (*Agent)(nil)
var _ core.SessionEventSubscriber = (*Agent)(nil)
var _ core.SessionQuestionResponder = (*Agent)(nil)
var _ core.SessionModelSelectionReader = (*Agent)(nil)

// Agent implements core.Agent for the Grok Build CLI.
type Agent struct {
	workDir         string
	cliBin          string
	cliExtraArgs    []string
	model           string
	reasoningEffort string
	mode            string
	allowedTools    []string
	// providers / activeIdx 背载 iOS 下发的第三方 Grok provider 配置（GLM/DeepSeek 等
	// 经 grok 网关）。AvailableModels 优先返 active provider 的 Models，使 custom 模型可见；
	// modelProviderForAgent 据此把无前缀模型标到 active provider 名下而非 "default"。
	providers []core.ProviderConfig
	activeIdx int // -1 = no provider set
	// grokHome overrides ~/.grok / GROK_HOME for session catalog (tests).
	grokHome string
	mu       sync.RWMutex

	// --- catalog subprocess singleton（§5.4 Phase 3）---
	// catalogClient 是进程级单例 ACP catalog 子进程（grok agent --no-leader stdio），
	// 与 per-turn grokSession 子进程分开管理、分开回收。catalogClientMu 串行化
	// create/replace；catalogRegistrar 是 bridge ProcessRegistry 注入句柄。
	// §10：capability 未声明前 go-bridge 不路由到 FetchSessionList → 当前不可达 = 零行为变化。
	catalogClient    *grokCatalogClient
	catalogClientMu  sync.Mutex
	catalogRegistrar CatalogSubprocessRegistrar

	// catalogRefresh coalesces machine-wide x.ai/sessions/changed roster
	// broadcasts (arriving on any live leader subscriber connection) into
	// core.CatalogRefreshSignals — the discovery watcher reacts with an
	// immediate authoritative fingerprint rescan → sessions_changed. Buffered 1
	// + non-blocking send = N concurrent relay connections each delivering the
	// broadcast collapse into one pending rescan. Write-once at New.
	catalogRefresh chan struct{}

	// modelCatalog is the official model catalog adopted from the agent's
	// initialize response `_meta.modelState` (session subprocesses and the
	// catalog singleton both feed it — last writer wins, shape is stable per
	// binary). nil/empty = catalog not observed yet → AvailableModels falls
	// back to iOS-injected provider models. Guarded by mu.
	modelCatalog *sessionModelState

	// liveSubs tracks per-session leader subscribers created by
	// SubscribeSessionEvents so question replies arriving over the bridge RPC
	// surface (core.SessionQuestionResponder) can reach the connection that
	// registered the pending interaction. Keyed by sessionID; the subscriber
	// unregisters itself when Run returns.
	liveSubs   map[string]*LeaderSubscriber
	liveSubsMu sync.Mutex

	// questionSessions maps leader-rail question ids (bridge interaction ids)
	// to the session whose live subscriber surfaced them, so the session-less
	// resolve_user_input path can route to the right subscriber (mirrors
	// dsh-web questionOwner). Entries clear on resolved; bounded as a safety
	// valve.
	questionSessions   map[string]string
	questionSessionsMu sync.Mutex
}

func init() {
	core.RegisterAgent("grokbuild", New)
}

// New creates a Grok Build agent from the given options map.
func New(opts map[string]any) (core.Agent, error) {
	a := &Agent{
		workDir:        ".",
		mode:           "default",
		activeIdx:      -1,
		catalogRefresh: make(chan struct{}, 1),
		liveSubs:       make(map[string]*LeaderSubscriber),
	}

	if v, ok := opts["work_dir"].(string); ok && v != "" {
		a.workDir = v
	}

	cliPath := "grok"
	if v, ok := opts["cli_path"].(string); ok && v != "" {
		fields := strings.Fields(v)
		cliPath = fields[0]
		if len(fields) > 1 {
			a.cliExtraArgs = fields[1:]
		}
	}
	a.cliBin = cliPath

	if v, ok := opts["model"].(string); ok {
		a.model = v
	}
	if v, ok := opts["reasoning_effort"].(string); ok && v != "" {
		a.reasoningEffort = normalizeReasoningEffort(v)
	}
	if v, ok := opts["mode"].(string); ok {
		a.mode = normalizePermissionMode(v)
	}
	if raw, ok := opts["allowed_tools"].([]any); ok {
		for _, t := range raw {
			if s, ok := t.(string); ok {
				a.allowedTools = append(a.allowedTools, s)
			}
		}
	}
	if v, ok := opts["grok_home"].(string); ok && strings.TrimSpace(v) != "" {
		a.grokHome = strings.TrimSpace(v)
	}

	// Verify the CLI exists (unless in isolation mode — skip like claudecode does).
	if _, err := exec.LookPath(a.cliBin); err != nil {
		return nil, fmt.Errorf("grokbuild: CLI %q not found in PATH: %w", a.cliBin, err)
	}

	return a, nil
}

func (a *Agent) Name() string { return "grokbuild" }

// StartSession creates a new Grok ACP session or loads an existing one.
//
// This method must NOT hold a.mu (not even RLock): newGrokSession → loadSession
// calls s.agent.SetWorkDir which acquires a.mu.Lock(). If StartSession held
// RLock, that would be a read→write upgrade deadlock — the Lock() blocks
// forever waiting for the RLock to release, but RLock won't release until
// newGrokSession returns, which won't happen because it's blocked on
// SetWorkDir. The agent pointer is stable (registered once at init), so no
// lock is needed here; individual field access inside newGrokSession uses the
// getter/setter methods which take their own locks.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	return newGrokSession(ctx, a, sessionID)
}

// ListSessions returns sessions from the local Grok session store
// ($GROK_HOME/sessions). This is the v1 on-disk discovery path (undeclared
// connections, §10). The catalog main line is FetchSessionList (managed ACP
// session/list subprocess, catalog_session_list.go) — routed only for connections
// that declare catalog_cursor_epoch_v2.
// Implementation: session_catalog.go.

func (a *Agent) Stop() error { return nil }

var _ core.CatalogRefreshSignaler = (*Agent)(nil)

// CatalogRefreshSignals implements core.CatalogRefreshSignaler: each leader
// roster broadcast (x.ai/sessions/changed, machine-wide) asks the bridge for an
// immediate fingerprint rescan. Latency win only — the 5s grok fast poll and
// the 60s safety scan stay as the fallback when no leader subscriber connection
// is alive to carry the broadcast (e.g. no session under observation).
func (a *Agent) CatalogRefreshSignals() <-chan struct{} { return a.catalogRefresh }

// signalCatalogRefresh is the roster callback wired into every leader
// subscriber. Non-blocking send against the buffered-1 channel coalesces
// repeated broadcasts while a rescan is still in flight.
func (a *Agent) signalCatalogRefresh() {
	select {
	case a.catalogRefresh <- struct{}{}:
	default:
	}
}

// SubscribeSessionEvents streams a session's live session/update notifications as
// core.Events (via convertSessionUpdate). It prefers the leader-socket subscriber
// (push, low-latency) when a leader is accepting connections on
// ~/.grok/leader.sock; otherwise it falls back to the updates.jsonl file tailer
// (poll) while probing for a leader to come back — D-G3 reclaim: the moment a
// leader accepts connections again the tailer stops and the subscriber
// re-attaches, whose attach-time interaction replay re-surfaces pending
// questions (the transcript tailer can never carry them). grok's leader socket
// only exists under use_leader=true (default inline embedded agent mode never
// creates it), so the file fallback is the path most users actually hit — and
// it works without any requirement on how grok was launched, since grok writes
// updates.jsonl in all modes. Both sources feed the same codec
// (convertSessionUpdate), so the downstream relay loop's turn-start synthesis /
// defer-idle logic is shared unchanged.
//
// Neither path spawns a leader, acquires the flock, or drives the session. The
// channel closes when the source disconnects / tails out or ctx is cancelled.
//
// grokHome is write-once (set in New), so reading it here without a.mu is safe.
func (a *Agent) SubscribeSessionEvents(ctx context.Context, sessionID, cwd string) (<-chan core.Event, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("grokbuild: SubscribeSessionEvents requires a sessionId")
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = a.GetWorkDir()
	}
	ch := make(chan core.Event, 32)
	forward := func(ev core.Event) {
		if ev.SessionID == "" {
			ev.SessionID = sessionID
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
	}

	socketPath := resolveLeaderSocket(a.grokHome)
	// grok 的 leader 校验 session/load 的 cwd 必须是 session 所属项目目录；不符即拒
	// （session/load: Path not found.）。调用方传入的 cwd 不可信：v2 projection 开启
	// 路径不携带 directory（iOS ProjectionStore 硬编码 nil），handlers 回落
	// GetWorkDir() 会得到与 session 无关的 runtime 工作目录。sessions 树是 grok 自己
	// 写的权威位置——按 sessionID 反查并以其为准；查不到时保持调用方值（维持现状，
	// 由 leader 的错误路径暴露）。
	if resolved := resolveSessionCwd(a.grokHome, sessionID); resolved != "" && resolved != cwd {
		slog.Info("grokbuild: leader subscriber cwd resolved from session store",
			"session", sessionID, "cwd", resolved, "requested", cwd)
		cwd = resolved
	}

	go func() {
		defer close(ch)
		for ctx.Err() == nil {
			if leaderSocketDialable(socketPath) {
				forwarded, err := a.runLeaderSubscription(ctx, sessionID, cwd, socketPath, forward)
				if ctx.Err() != nil {
					// 调用方取消（正常关闭）——保持 Debug 静默。
					slog.Debug("grokbuild: leader subscriber ended (ctx cancelled)", "session", sessionID, "error", err)
					return
				}
				if forwarded {
					// D-G1/F-7：已转发 ≥1 事件后的断开不回退——channel 关闭，
					// 由 bridge 的 observation 逻辑重新拉起订阅。
					slog.Warn("grokbuild: leader subscriber ended", "session", sessionID, "socket", socketPath, "error", err)
					return
				}
				// 回退三要素：订阅结束（error/nil 一致）+ 未转发任何事件 + ctx 未取消。
				args := []any{"session", sessionID, "socket", socketPath}
				if err != nil {
					args = append(args, "error", err)
				}
				slog.Info("grokbuild: leader subscribe failed, falling back to updates.jsonl tailer (with leader reclaim probe)", args...)
			} else {
				slog.Info("grokbuild: leader socket absent or not accepting, falling back to updates.jsonl tailer (with leader reclaim probe)",
					"session", sessionID, "socket", socketPath)
			}
			// D-G2 互锁：主动取消时 relay 已退出，不得再拉起无人消费的 tailer
			// （runTailerWithLeaderProbe 在 ctx.Done 时立即返回 false）。
			if !a.runTailerWithLeaderProbe(ctx, sessionID, socketPath, forward) {
				return
			}
			slog.Info("grokbuild: leader reclaim: socket accepting again, re-subscribing", "session", sessionID, "socket", socketPath)
		}
	}()
	return ch, nil
}

// leaderReclaimProbeInterval is how often the tailer fallback probes the
// leader socket for a reclaim (D-G3). Cheap unix dial; 10s covers leader
// restart windows (TUI relaunch) without hot-looping. Var so tests can
// shorten it.
var leaderReclaimProbeInterval = 10 * time.Second

// leaderSocketDialable reports whether something is currently accepting
// connections on socketPath.
func leaderSocketDialable(socketPath string) bool {
	c, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// runTailerWithLeaderProbe runs the updates.jsonl tailer while probing the
// leader socket. Returns true when a leader accepts connections again (the
// caller re-subscribes — the attach-time replay re-surfaces pending
// questions); false when ctx is cancelled or the tailer ended on its own
// (post-turn grace / hardCap — the channel closes, matching pre-reclaim
// behavior).
func (a *Agent) runTailerWithLeaderProbe(ctx context.Context, sessionID, socketPath string, forward func(core.Event)) bool {
	tailCtx, cancelTail := context.WithCancel(ctx)
	defer cancelTail()
	tailDone := make(chan struct{})
	go func() {
		defer close(tailDone)
		tail := newUpdatesFileTailSubscriber(a.grokHome, sessionID)
		if err := tail.Run(tailCtx, forward); err != nil {
			slog.Debug("grokbuild: updates file tailer ended", "session", sessionID, "error", err)
		}
	}()
	ticker := time.NewTicker(leaderReclaimProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cancelTail()
			<-tailDone
			return false
		case <-tailDone:
			return false
		case <-ticker.C:
			if leaderSocketDialable(socketPath) {
				cancelTail()
				<-tailDone
				return true
			}
		}
	}
}

// runLeaderSubscription runs one leader-subscriber lifecycle: registers it in
// liveSubs (so answer routing can find it), forwards events with question-owner
// tracking, and reports whether any event was forwarded (the D-G1
// live/fallback boundary for this connection).
func (a *Agent) runLeaderSubscription(ctx context.Context, sessionID, cwd, socketPath string, forward func(core.Event)) (forwardedAny bool, err error) {
	sub := NewLeaderSubscriber(socketPath, sessionID, cwd)
	sub.onRosterChanged = a.signalCatalogRefresh
	a.liveSubsMu.Lock()
	if a.liveSubs == nil {
		a.liveSubs = make(map[string]*LeaderSubscriber)
	}
	a.liveSubs[sessionID] = sub
	a.liveSubsMu.Unlock()
	defer func() {
		a.liveSubsMu.Lock()
		if a.liveSubs[sessionID] == sub {
			delete(a.liveSubs, sessionID)
		}
		a.liveSubsMu.Unlock()
	}()
	slog.Info("grokbuild: leader subscriber starting", "session", sessionID, "socket", socketPath)
	// D-G1（§3.5.1，r4-B1 首事件规则）：建立/live 分界 = 是否已向下游转发任何
	// leader event。先置位再转发，保证判据不落后于下游可见性。
	var forwarded atomic.Bool
	err = sub.Run(ctx, func(ev core.Event) {
		forwarded.Store(true)
		// Leader-rail question ownership: remember which session surfaced a
		// pending interaction so the session-less resolve_user_input path can
		// route back; forget it once resolved.
		switch ev.Type {
		case core.EventUserInputRequested:
			if id := ev.ItemID; id != "" {
				a.trackQuestionOwner(id, ev.SessionID)
			}
		case core.EventUserInputResolved:
			if id := ev.ItemID; id != "" {
				a.untrackQuestionOwner(id)
			}
		}
		forward(ev)
	})
	return forwarded.Load(), err
}

// RespondSessionQuestion implements core.SessionQuestionResponder: routes an
// iOS question reply to the live leader subscriber that surfaced the question
// (external-turn observation — no AgentSession exists, so the bridge falls
// through to the agent-level responder).
func (a *Agent) RespondSessionQuestion(ctx context.Context, sessionID, questionID string, optionIDs []string) error {
	_ = ctx
	sub := a.liveSubscriber(sessionID)
	if sub == nil {
		return fmt.Errorf("grokbuild: no live leader subscriber for session %s", shortID(sessionID))
	}
	_, err := sub.AnswerQuestion(questionID, optionIDs, "")
	return err
}

// RejectSessionQuestion implements core.SessionQuestionResponder (dismiss).
func (a *Agent) RejectSessionQuestion(ctx context.Context, sessionID, questionID string) error {
	_ = ctx
	sub := a.liveSubscriber(sessionID)
	if sub == nil {
		return fmt.Errorf("grokbuild: no live leader subscriber for session %s", shortID(sessionID))
	}
	_, err := sub.CancelQuestion(questionID)
	return err
}

// RespondSessionPermission implements core.SessionPermissionResponder: routes
// an iOS permission reply (allow/always/reject) to the live leader subscriber
// that surfaced the request (external-turn observation — no AgentSession
// exists, so the bridge falls through to the agent-level responder). The
// deny/reject distinction is carried by PermissionResult.Behavior, not a
// separate method; upstream has no reject-specific wire shape.
func (a *Agent) RespondSessionPermission(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	_ = ctx
	sub := a.liveSubscriber(sessionID)
	if sub == nil {
		return fmt.Errorf("grokbuild: no live leader subscriber for session %s", shortID(sessionID))
	}
	_, err := sub.AnswerPermission(requestID, result)
	return err
}

var _ core.SessionPermissionResponder = (*Agent)(nil)

var _ core.UserInputResponder = (*Agent)(nil)
var _ core.StructuredUserInputProvider = (*Agent)(nil)

// StructuredUserInputReady: the canonical user_input producer (dual-track
// emitQuestionAsked/emitQuestionResolved), the real responder (this type plus
// grokSession), and the driver/leader rails are all enabled together.
func (a *Agent) StructuredUserInputReady() bool { return true }

// ResolveUserInput implements core.UserInputResponder for leader-rail
// questions: interactions surfaced by a session's live leader subscriber
// (external-turn observation, no active AgentSession). Driver-rail questions
// are answered by grokSession.ResolveUserInput, which falls through here on a
// miss. Typed text answers map to grok's freeform wire shape: label "Other" +
// annotations notes (the TUI "type your answer here" path).
func (a *Agent) ResolveUserInput(_ context.Context, interactionID, _ string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	sessionID := a.questionSessionOwner(interactionID)
	if sessionID == "" {
		return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "question is not pending"}
	}
	sub := a.liveSubscriber(sessionID)
	if sub == nil {
		return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "no live leader subscriber for this session"}
	}
	if action == core.UserInputActionReject {
		if _, err := sub.CancelQuestion(interactionID); err != nil {
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
		resolved, err := sub.AnswerQuestion(qid, selected, notes)
		if err != nil {
			return core.UserInputResolution{}, err
		}
		if !resolved {
			// Multi-question interaction partially answered; the wire response
			// flushes (and resolved events emit) on the last answer.
			return core.UserInputResolution{Outcome: core.UserInputOutcomeInProgress, CurrentStatus: core.UserInputStatusPending}, nil
		}
	}
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
}

func (a *Agent) trackQuestionOwner(interactionID, sessionID string) {
	a.questionSessionsMu.Lock()
	defer a.questionSessionsMu.Unlock()
	if a.questionSessions == nil {
		a.questionSessions = make(map[string]string)
	}
	if len(a.questionSessions) >= 512 {
		// Safety valve: drop stale ownership rather than grow unbounded. Live
		// questions re-register on their next ask event.
		a.questionSessions = make(map[string]string)
	}
	a.questionSessions[interactionID] = sessionID
}

func (a *Agent) untrackQuestionOwner(interactionID string) {
	a.questionSessionsMu.Lock()
	defer a.questionSessionsMu.Unlock()
	delete(a.questionSessions, interactionID)
}

func (a *Agent) questionSessionOwner(interactionID string) string {
	a.questionSessionsMu.Lock()
	defer a.questionSessionsMu.Unlock()
	return a.questionSessions[interactionID]
}

func (a *Agent) liveSubscriber(sessionID string) *LeaderSubscriber {
	a.liveSubsMu.Lock()
	defer a.liveSubsMu.Unlock()
	return a.liveSubs[sessionID]
}

// resolveSessionCwd locates the session's project directory from grok's own
// session store layout ($GROK_HOME/sessions/<url-encoded-cwd>/<sessionID>/) and
// decodes it. Returns "" when the session cannot be located unambiguously.
func resolveSessionCwd(grokHome, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	sessionsDir := filepath.Join(resolveGrokHome(grokHome), "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return ""
	}
	resolved := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st, err := os.Stat(filepath.Join(sessionsDir, e.Name(), sessionID))
		if err != nil || !st.IsDir() {
			continue
		}
		decoded, derr := url.PathUnescape(e.Name())
		if derr != nil || decoded == "" {
			continue
		}
		if resolved != "" && resolved != decoded {
			// 多个项目目录声称持有同一 session —— 无法唯一判定，交回调用方值。
			return ""
		}
		resolved = decoded
	}
	return resolved
}

// --- WorkDirSwitcher ---

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	a.workDir = dir
	a.mu.Unlock()
}

func (a *Agent) GetWorkDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workDir
}

// --- ModeSwitcher ---

func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	a.mode = normalizePermissionMode(mode)
	a.mu.Unlock()
}

func (a *Agent) GetMode() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mode
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Default", NameZh: "默认", Desc: "Ask for permission on each tool", DescZh: "每次工具调用都询问"},
		{Key: "acceptEdits", Name: "Accept Edits", NameZh: "接受编辑", Desc: "Auto-accept file edits", DescZh: "自动接受文件编辑"},
		{Key: "auto", Name: "Auto", NameZh: "自动", Desc: "Auto-approve most operations", DescZh: "自动批准大多数操作"},
		{Key: "dontAsk", Name: "Don't Ask", NameZh: "不询问", Desc: "Don't ask for any permission", DescZh: "不询问任何权限"},
		{Key: "bypassPermissions", Name: "Bypass Permissions", NameZh: "绕过权限", Desc: "Bypass all permission checks", DescZh: "绕过所有权限检查"},
		{Key: "plan", Name: "Plan", NameZh: "计划", Desc: "Plan mode — no execution", DescZh: "计划模式——不执行"},
	}
}

// --- ModelSwitcher ---

func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	a.model = model
	a.mu.Unlock()
}

func (a *Agent) GetModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return core.GetProviderModel(a.providers, a.activeIdx, a.model)
}

// configuredModels returns the model list pre-configured on the active provider
// (iOS-injected third-party Grok providers). Empty when no provider is set.
func (a *Agent) configuredModels() []core.ModelOption {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return core.GetProviderModels(a.providers, a.activeIdx)
}

// adoptModelCatalog replaces the official model catalog observed in an
// agent-subprocess or catalog-subprocess initialize response. A nil or empty
// state is ignored (older grok without modelState keeps the previous view).
func (a *Agent) adoptModelCatalog(ms *sessionModelState) {
	if ms == nil || len(ms.AvailableModels) == 0 {
		return
	}
	a.mu.Lock()
	a.modelCatalog = ms
	a.mu.Unlock()
	slog.Info("grokbuild: adopted official model catalog",
		"models", len(ms.AvailableModels),
		"current", ms.CurrentModelID)
}

// explicitModelSelection returns the iOS/bridge-set model and effort (""
// = no explicit choice; the agent-side default applies).
func (a *Agent) explicitModelSelection() (model, effort string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model, a.reasoningEffort
}

// sessionNewMeta builds the session/new `_meta` when an explicit model/effort
// selection exists; nil when neither is set (agent default applies untouched).
func (a *Agent) sessionNewMeta() *sessionNewMeta {
	model, effort := a.explicitModelSelection()
	target := model
	if target == "" {
		// Effort-only selection gates against the model the session would
		// start with (explicit first, else the catalog's current).
		target = a.currentModelIDForEfforts()
	}
	effort = a.effectiveEffortForModel(target, effort)
	if model == "" && effort == "" {
		return nil
	}
	return &sessionNewMeta{ModelID: model, ReasoningEffort: effort}
}

// effectiveEffortForModel mirrors upstream resolve_effort_for_model
// (xai-grok-pager model_state.rs): a model unknown to the adopted catalog, or
// one whose entry lacks the effort support flag, gets no effort; a supported
// model only accepts tokens present in its menu (option id or canonical
// value). Official clients reject these locally before the wire — "so the TUI
// fails instead of sending a blocked effort to the API" — and an iOS-side
// effort leftover after a model switch must not reach session/set_model
// (grok 1.0.13 answers -32602 there; observed 2026-09-02 with GLM + high).
// With no adopted catalog there is no truth to gate on: the effort passes
// through unchanged and the official adjudicates.
func (a *Agent) effectiveEffortForModel(model, effort string) string {
	if effort == "" {
		return ""
	}
	entry := a.effortCatalogEntry(model)
	if entry == nil {
		if a.effortCatalogKnown() {
			slog.Info("grokbuild: dropping reasoning effort for model unknown to catalog",
				"model", model, "effort", effort)
			return ""
		}
		return effort
	}
	if entry.Meta == nil || !entry.Meta.SupportsReasoningEffort {
		slog.Info("grokbuild: dropping reasoning effort for model without effort support",
			"model", model, "effort", effort)
		return ""
	}
	if len(entry.Meta.ReasoningEfforts) == 0 {
		// Supported flag set but no usable menu: upstream falls back to a
		// built-in menu of canonical levels, so a canonical-looking value
		// stays and the official adjudicates.
		return effort
	}
	for _, opt := range entry.Meta.ReasoningEfforts {
		if strings.EqualFold(opt.ID, effort) || opt.Value == effort {
			return effort
		}
	}
	slog.Info("grokbuild: dropping reasoning effort outside model menu",
		"model", model, "effort", effort)
	return ""
}

// effortCatalogKnown reports whether an official catalog has been adopted
// (nil entry in a known catalog means "unsupported", not "unknown").
func (a *Agent) effortCatalogKnown() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.modelCatalog != nil
}

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	_ = ctx
	// Official catalog truth first: the agent's initialize `_meta.modelState`
	// (grok 1.0.13 real sample; covers both official and owner-configured
	// [models] entries — the GLM provider shows up as a regular entry). Falls
	// back to iOS-injected provider models when no catalog has been observed
	// yet; empty catalog + no provider stays the honest "backend did not
	// provide models" state. Session-level actual model still flows through
	// SessionModelSelectionReader (transcript evidence).
	a.mu.RLock()
	catalog := a.modelCatalog
	a.mu.RUnlock()
	if catalog != nil {
		models := make([]core.ModelOption, 0, len(catalog.AvailableModels))
		for _, m := range catalog.AvailableModels {
			models = append(models, core.ModelOption{
				Name: m.ModelID,
				Desc: m.Name,
			})
		}
		return models
	}
	return a.configuredModels()
}

// --- ReasoningEffortSwitcher / ModelEffortCatalog ---

func (a *Agent) SetReasoningEffort(effort string) {
	a.mu.Lock()
	a.reasoningEffort = normalizeReasoningEffort(effort)
	a.mu.Unlock()
}

func (a *Agent) GetReasoningEffort() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reasoningEffort
}

// effortCatalogEntry returns the catalog entry for one model id from the
// adopted official catalog (nil when unknown or no catalog).
func (a *Agent) effortCatalogEntry(modelID string) *acpModelInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.modelCatalog == nil {
		return nil
	}
	for i := range a.modelCatalog.AvailableModels {
		if a.modelCatalog.AvailableModels[i].ModelID == modelID {
			return &a.modelCatalog.AvailableModels[i]
		}
	}
	return nil
}

// currentModelIDForEfforts resolves which model the effort list describes: the
// explicit selection first, else the catalog's current model.
func (a *Agent) currentModelIDForEfforts() string {
	model, _ := a.explicitModelSelection()
	if model != "" {
		return model
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.modelCatalog != nil {
		return a.modelCatalog.CurrentModelID
	}
	return ""
}

// EffortsForModel implements core.ModelEffortCatalog: per-model effort ids and
// the model's default effort from the official catalog's
// `_meta.reasoningEfforts` menu. ok=false leaves the wire fields empty rather
// than smearing an agent-level list across the catalog (handleListModels
// contract, roadmap §5.2 / audit N3).
func (a *Agent) EffortsForModel(ctx context.Context, model string) ([]string, string, bool) {
	_ = ctx
	entry := a.effortCatalogEntry(model)
	if entry == nil || entry.Meta == nil || len(entry.Meta.ReasoningEfforts) == 0 {
		return nil, "", false
	}
	efforts := make([]string, 0, len(entry.Meta.ReasoningEfforts))
	def := ""
	for _, opt := range entry.Meta.ReasoningEfforts {
		id := opt.ID
		if id == "" {
			id = opt.Value
		}
		if id == "" {
			continue
		}
		efforts = append(efforts, id)
		if opt.Default {
			def = id
		}
	}
	if len(efforts) == 0 {
		return nil, "", false
	}
	return efforts, def, true
}

// AvailableReasoningEfforts describes the CURRENT model's selectable efforts
// from the official catalog (per-model `_meta.reasoningEfforts`, gated on
// supportsReasoningEffort). nil = unknown or unsupported — iOS hides the
// effort control and nothing is smeared across other models.
func (a *Agent) AvailableReasoningEfforts() []string {
	entry := a.effortCatalogEntry(a.currentModelIDForEfforts())
	if entry == nil || entry.Meta == nil || !entry.Meta.SupportsReasoningEffort {
		return nil
	}
	efforts, _, ok := a.EffortsForModel(context.Background(), entry.ModelID)
	if !ok {
		return nil
	}
	return efforts
}

// --- ProviderSwitcher ---

func (a *Agent) SetProviders(providers []core.ProviderConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providers = providers
}

func (a *Agent) SetActiveProvider(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if name == "" {
		a.activeIdx = -1
		slog.Info("grokbuild: provider cleared")
		return true
	}
	for i, p := range a.providers {
		if p.Name == name {
			a.activeIdx = i
			slog.Info("grokbuild: provider switched", "provider", name)
			return true
		}
	}
	return false
}

func (a *Agent) GetActiveProvider() *core.ProviderConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
		return nil
	}
	p := a.providers[a.activeIdx]
	return &p
}

func (a *Agent) ListProviders() []core.ProviderConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]core.ProviderConfig, len(a.providers))
	copy(result, a.providers)
	return result
}

// --- ToolAuthorizer ---

func (a *Agent) AddAllowedTools(tools ...string) error {
	a.mu.Lock()
	a.allowedTools = append(a.allowedTools, tools...)
	a.mu.Unlock()
	return nil
}

func (a *Agent) GetAllowedTools() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.allowedTools
}

// --- normalizers ---

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(mode) {
	case "", "default":
		return "default"
	case "acceptedits", "accept_edits":
		return "acceptEdits"
	case "auto":
		return "auto"
	case "dontask", "dont_ask":
		return "dontAsk"
	case "bypasspermissions", "bypass_permissions":
		return "bypassPermissions"
	case "plan":
		return "plan"
	default:
		return mode
	}
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(effort) {
	case "", "medium":
		return "medium"
	case "low":
		return "low"
	case "high":
		return "high"
	default:
		return strings.ToLower(effort)
	}
}

// gracefulStopTimeout is the time to wait for the process to exit after closing stdin.
const gracefulStopTimeout = 8 * time.Second
