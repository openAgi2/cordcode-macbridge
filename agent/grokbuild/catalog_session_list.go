package grokbuild

// catalog_session_list.go 是 Grok list_sessions 的 catalog 主线客户端（设计 §5.4
// Phase 3 / §4.3 进程管理）。它与 codex/agent/codex/catalog_client.go 刻意同构：
//
//   - 进程级单例 stdio ACP 子进程（`grok agent --no-leader stdio`），与 per-turn
//     grokSession 子进程分开管理、分开回收（§5.4 #6：catalog client 使用进程级单例/
//     持久 ACP 连接，并与 turn session 进程分开管理）；
//   - 握手 initialize →（若声明 authMethods）authenticate → session/list；
//   - 握手未声明 sessionCapabilities.list 时 **fail-closed** 显式报错（§5.4 #5：
//     不得静默退回旧 scanner），不再读本地 sessions 目录；
//   - session/list 不带 cwd 过滤（Grok native catalog 跨所有 cwd 返回），故 FetchSessionList
//     不取 dir 参数；映射遵循 frozen fixture（sessionId/cwd/title/updatedAt/_meta.x.ai/session.facets.branch）。
//
// 与 codex 的差异（Grok-specific deltas）：
//   1. args = ["agent","--no-leader","stdio"] + cliExtraArgs（codex 是 ["app-server"]）；
//   2. initialize 之后、session/list 之前 **必须** authenticate（Grok 1.0.0 握手要求）；
//   3. 不发 initialized 通知（ACP v1：initialize 响应即握手完成）；
//   4. frozen struct 用 updatedAt（RFC3339Nano）+ _meta.x.ai/session.facets.branch，
//      **不是** acpSessionInfo.LastActivity（drift bug 字段，禁用）；
//   5. session/list params 为空对象 {}，返回跨 cwd 的所有 session（非 cwd-scoped）；
//   6. 进程回收走 prepareCmdForProcessGroup + signalProcessGroup（§4.3，grokbuild 版本）。
//
// §10 发布顺序：capability catalog_cursor_epoch_v2 上线前（iOS Phase 6 才声明），
// go-bridge dispatch 不路由到 grokHandleListSessions → 当前 FetchSessionList 不可达 = 零行为变化。
// 即使 iOS 已声明，握手缺 session/list 能力时仍 fail-closed，绝不静默 fallback。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// catalogSessionRequestTimeout 是单次 catalog RPC（session/list 等）的硬超时。
// 与 codex catalogClient 的 60s 对齐（§5.4 #6：持久连接 + 受控超时）。
const catalogSessionRequestTimeout = 60 * time.Second

// catalogSessionReadMaxLineSize 限制单行 JSON-RPC 消息上限（session/list 结果可能很大）。
const catalogSessionReadMaxLineSize = 10 * 1024 * 1024

// CatalogSubprocessRegistrar 把 catalog stdio 子进程注册到 bridge 的 ProcessRegistry，
// Shutdown 时确定性进程组回收（§4.3 进程管理红线 / §11）。接口签名与 codex 一致，使同一
// *go_bridge.ProcessRegistry 满足两个 backend。
type CatalogSubprocessRegistrar interface {
	Register(cmd *exec.Cmd) (unregister func())
}

// catalogClientConfig 是构造单例 catalog client 的全部输入。由 *Agent 从自身配置派生
// （cliBin/cliExtraArgs/workDir）+ bridge 注入的 registrar。
type catalogClientConfig struct {
	cliBin       string
	cliExtraArgs []string
	workDir      string
	registrar    CatalogSubprocessRegistrar
}

// grokCatalogClient 是一个进程级单例 ACP catalog 子进程连接。生命周期独立于 per-turn
// grokSession：catalog 查询复用同一 `grok agent --no-leader stdio` 子进程，turn 请求各自
// spawn 独立子进程。死亡（子进程退出 / 握手失败）后由 catalogClientInstance 按需重建。
type grokCatalogClient struct {
	cfg catalogClientConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	stdinMu sync.Mutex // serializes writes to stdin

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when process exits

	// ACP request ID counter（复用 codec.requestIDCounter）
	idCounter requestIDCounter

	// pending response matching: maps request ID → channel for synchronous waits
	respMu       sync.Mutex
	respChannels map[int]chan *jsonrpcResponse

	alive      atomic.Bool
	closeOnce  sync.Once
	unregister func() // bridge ProcessRegistry 反注册句柄（nil=未注册）
}

// newGrokCatalogClient 启动 stdio 子进程并完成 ACP 握手（initialize → authenticate）。
// 握手成功后即可调用 listSessions。握手失败（含未声明 session/list 能力）返回 error，
// 调用方负责清理（Close）。
func newGrokCatalogClient(ctx context.Context, cfg catalogClientConfig) (*grokCatalogClient, error) {
	return newGrokCatalogClientWithRequestContext(ctx, ctx, cfg)
}

// newGrokCatalogClientWithRequestContext separates the process-level catalog lifetime from the
// bounded list request that caused lazy creation. The process owns lifetimeCtx; initialize and
// authenticate still honor requestCtx. Otherwise the first request's deferred cancel leaves a
// live-process/dead-context singleton that makes every later session/list fail context canceled.
func newGrokCatalogClientWithRequestContext(lifetimeCtx, requestCtx context.Context, cfg catalogClientConfig) (*grokCatalogClient, error) {
	if err := requestCtx.Err(); err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(lifetimeCtx)

	args := []string{"agent", "--no-leader", "stdio"}
	args = append(args, cfg.cliExtraArgs...)

	cmd := exec.CommandContext(sessionCtx, cfg.cliBin, args...)
	cmd.Dir = cfg.workDir
	prepareCmdForProcessGroup(cmd)

	// Build a clean environment: no control-plane secrets。与 newGrokSession 一致（§5.4 #6）。
	baseEnv := core.FilterEnvToAllowlist(filterOsEnviron(), core.DefaultEnvAllowlist)
	cmd.Env = core.BuildAgentEnv(baseEnv, nil, nil)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild catalog: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild catalog: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild catalog: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("grokbuild catalog: start %s: %w", cfg.cliBin, err)
	}

	c := &grokCatalogClient{
		cfg:          cfg,
		cmd:          cmd,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		ctx:          sessionCtx,
		cancel:       cancel,
		done:         make(chan struct{}),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	c.alive.Store(true)

	// 注册到 bridge ProcessRegistry（若注入了 registrar）。进程死亡时由 unregister 反注册。
	if cfg.registrar != nil {
		c.unregister = cfg.registrar.Register(cmd)
	}

	go c.readStderr()
	go c.readLoop()

	// 后台等待子进程退出，关闭 done 并置 alive=false。
	go func() {
		_ = cmd.Wait()
		close(c.done)
		c.alive.Store(false)
		if c.unregister != nil {
			c.unregister()
		}
	}()

	if err := requestCtx.Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.initializeContext(requestCtx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("grokbuild catalog: initialize: %w", err)
	}
	return c, nil
}

// initialize 发送 ACP initialize，解析 agent 能力。**fail-closed**：未声明
// sessionCapabilities.list → 显式 error（§5.4 #5），绝不静默退回旧 scanner。
// 声明 authMethods 时调用 authenticate（Grok 1.0.0 要求，位于 initialize 与 session/list 之间）。
func (c *grokCatalogClient) initialize() error {
	return c.initializeContext(c.ctx)
}

func (c *grokCatalogClient) initializeContext(ctx context.Context) error {
	id := c.idCounter.next()
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
	result, err := c.callRPCWithCtx(ctx, id, "initialize", params, 10*time.Second)
	if err != nil {
		return err
	}

	var initResp initializeResult
	if err := json.Unmarshal(result, &initResp); err != nil {
		return fmt.Errorf("decode initialize response: %w", err)
	}

	supportsList := false
	if initResp.AgentCapabilities != nil && initResp.AgentCapabilities.SessionCapabilities != nil {
		supportsList = initResp.AgentCapabilities.SessionCapabilities.List.Enabled
	}
	if !supportsList {
		// §5.4 #5：明确报「backend 版本不支持 catalog」，不得静默 fallback 旧 scanner。
		return fmt.Errorf("grok catalog: backend does not advertise session/list (requires Grok 1.0.0+ with session catalog)")
	}

	if len(initResp.AuthMethods) > 0 {
		if err := c.authenticateContext(ctx, initResp.AuthMethods[0].ID); err != nil {
			return fmt.Errorf("authenticate: %w", err)
		}
	}
	return nil
}

func (c *grokCatalogClient) authenticate(method string) error {
	return c.authenticateContext(c.ctx, method)
}

func (c *grokCatalogClient) authenticateContext(ctx context.Context, method string) error {
	id := c.idCounter.next()
	_, err := c.callRPCWithCtx(ctx, id, "authenticate", authenticateParams{MethodID: method}, 30*time.Second)
	return err
}

// --- session/list ---

// grokSessionListItem 是 frozen session/list 结果的单条 session（fixture:
// testdata/session_list_sanitized.json）。字段集与真实 Grok 1.0.0 响应一致。
type grokSessionListItem struct {
	SessionID string          `json:"sessionId"`
	Cwd       string          `json:"cwd"`
	Title     string          `json:"title"`
	UpdatedAt string          `json:"updatedAt"` // RFC3339Nano，例 "2026-08-08T14:36:11.900198+00:00"
	Meta      grokSessionMeta `json:"_meta"`
}

// grokSessionMeta 解包 _meta。frozen key = "x.ai/session"（带斜杠，**非** 点号）。
type grokSessionMeta struct {
	Session grokSessionMetaEntry `json:"x.ai/session"`
}

type grokSessionMetaEntry struct {
	Kind   string            `json:"kind"`
	Facets grokSessionFacets `json:"facets"`
}

type grokSessionFacets struct {
	Branch  string `json:"branch"`
	Cwd     string `json:"cwd"`
	GitRoot string `json:"gitRoot"`
	Kind    string `json:"kind"`
	Repo    string `json:"repo"`
}

// grokSessionListResult 是 session/list 的 result envelope。nextCursor 透传给 iOS
// 作不透明分页 token（当前 MacBridge 不二次分页 Grok native catalog）。顶层 _meta
// （x.ai/facets 聚合元数据）catalog 不消费，忽略。
type grokSessionListResult struct {
	Sessions   []grokSessionListItem `json:"sessions"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

// listSessions 调用 session/list（空 params，跨 cwd 返回所有 session）并映射为
// core.AgentSessionInfo。字段映射遵循 frozen fixture（ID←sessionId / Summary←title /
// Directory←cwd / ModifiedAt←parse(updatedAt) / GitBranch←_meta.x.ai/session.facets.branch）。
func (c *grokCatalogClient) listSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	id := c.idCounter.next()
	raw, err := c.callRPCWithCtx(ctx, id, "session/list", map[string]any{}, catalogSessionRequestTimeout)
	if err != nil {
		return nil, err
	}
	var res grokSessionListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("grokbuild catalog: decode session/list result: %w", err)
	}
	out := make([]core.AgentSessionInfo, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		out = append(out, grokSessionItemToAgentSessionInfo(s))
	}
	return out, nil
}

func grokSessionItemToAgentSessionInfo(s grokSessionListItem) core.AgentSessionInfo {
	info := core.AgentSessionInfo{
		ID:         s.SessionID,
		Summary:    s.Title,
		Directory:  s.Cwd,
		GitBranch:  s.Meta.Session.Facets.Branch,
		ModifiedAt: parseGrokCatalogUpdatedAt(s.UpdatedAt),
	}
	return info
}

// parseGrokCatalogUpdatedAt 解析 RFC3339Nano（带时区偏移）。失败返回零值（排序按零值兜底）。
func parseGrokCatalogUpdatedAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// --- JSON-RPC request/response routing ---

// callRPC 注册 waiter → write request → 等待响应（硬超时 timeout）。复用 grokSession.callRPC
// 的「先注册再写」模式（audit P0-2：避免快本地 stdio agent 的响应被丢）。
func (c *grokCatalogClient) callRPC(id int, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	return c.callRPCWithCtx(c.ctx, id, method, params, timeout)
}

func (c *grokCatalogClient) callRPCWithCtx(ctx context.Context, id int, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan *jsonrpcResponse, 1)
	c.respMu.Lock()
	c.respChannels[id] = ch
	c.respMu.Unlock()

	defer func() {
		c.respMu.Lock()
		delete(c.respChannels, id)
		c.respMu.Unlock()
	}()

	if err := c.writeRequest(id, method, params); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("grok catalog: %s error %d: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-timer.C:
		return nil, fmt.Errorf("grok catalog: %s timeout after %s", method, timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("grok catalog: %s aborted (process exited)", method)
	}
}

func (c *grokCatalogClient) writeRequest(id int, method string, params any) error {
	data, err := encodeRequest(id, method, params)
	if err != nil {
		return err
	}
	c.stdinMu.Lock()
	_, err = c.stdin.Write(data)
	c.stdinMu.Unlock()
	return err
}

func (c *grokCatalogClient) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), catalogSessionReadMaxLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		resp, req, notif, err := decodeMessage(line)
		if err != nil {
			// 不记录 agent 负载（可能含 prompt / tool args / 路径）。
			slog.Warn("grokbuild catalog: decode failed", "error_class", logErrorClass(err), "bytes", len(line))
			continue
		}
		if resp != nil {
			c.handleResponse(resp)
			continue
		}
		// catalog client 不处理 agent→client 请求/通知（session/list 是简单 RPC）。
		if req != nil {
			slog.Debug("grokbuild catalog: ignoring agent request", "method", req.Method)
		} else if notif != nil {
			slog.Debug("grokbuild catalog: ignoring agent notification", "method", notif.Method)
		}
	}
	c.alive.Store(false)
}

func (c *grokCatalogClient) handleResponse(resp *jsonrpcResponse) {
	var idNum int
	if err := json.Unmarshal(resp.ID, &idNum); err != nil {
		// 字符串 ID（非我们发的）→ 忽略。
		return
	}
	c.respMu.Lock()
	ch, ok := c.respChannels[idNum]
	c.respMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func (c *grokCatalogClient) readStderr() {
	scanner := bufio.NewScanner(c.stderr)
	scanner.Buffer(make([]byte, 0, 4*1024), 256*1024)
	var lines, bytes int
	for scanner.Scan() {
		// 不记录 stderr 文本：agent 可能回显 prompt / tool args / 路径。
		lines++
		bytes += len(scanner.Bytes())
	}
	if lines > 0 {
		slog.Debug("grokbuild catalog: stderr closed", "lines", lines, "bytes", bytes)
	}
}

// Alive 返回 catalog 子进程是否仍可用（用于 catalogClientInstance 的重建判定）。
func (c *grokCatalogClient) Alive() bool { return c.alive.Load() }

// Close 三阶段优雅回收（与 grokSession.Close 同构）：stdin close → graceful 等待 →
// SIGTERM 进程组 → SIGKILL 进程组。sync.Once 保证幂等（bridge Shutdown + rebuild 都可能调用）。
func (c *grokCatalogClient) Close() error {
	c.closeOnce.Do(func() {
		c.alive.Store(false)
		c.stdinMu.Lock()
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		c.stdinMu.Unlock()

		select {
		case <-c.done:
		case <-time.After(gracefulStopTimeout):
		}
		_ = signalProcessGroup(c.cmd, sigTERM)
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
		}
		c.cancel()
		_ = signalProcessGroup(c.cmd, sigKILL)
		<-c.done
	})
	return nil
}

// --- Agent-level singleton ---

// FetchSessionList 是 go-bridge catalog 主线入口（设计 §5.4）：经进程级单例
// grokCatalogClient 调用 session/list，返回跨 cwd 的所有 session（Grok native catalog 行为）。
// 子进程死亡或首次创建时按需（re）build 单例，并发调用串行化在 catalogClientMu 上。
//
// 与 codex FetchThreadList 的差异：不取 dir（Grok session/list 非 cwd-scoped）。
func (a *Agent) FetchSessionList(ctx context.Context) ([]core.AgentSessionInfo, error) {
	client, err := a.catalogClientInstance(ctx)
	if err != nil {
		return nil, err
	}
	return client.listSessions(ctx)
}

// catalogClientInstance 返回存活的单例 catalog client，必要时（首次 / 已死）重建。
// 并发安全：catalogClientMu 串行化 create/replace；存活判定同时检查 alive 与 done。
func (a *Agent) catalogClientInstance(ctx context.Context) (*grokCatalogClient, error) {
	a.catalogClientMu.Lock()
	defer a.catalogClientMu.Unlock()

	if a.catalogClient != nil && a.catalogClient.Alive() {
		// 二次确认 done 未关闭（Alive 可能滞后于进程退出 goroutine）。
		select {
		case <-a.catalogClient.done:
			// 已死 → 落到重建。
		default:
			return a.catalogClient, nil
		}
	}

	// 关掉旧（已死）实例，释放其资源（幂等）。
	if a.catalogClient != nil {
		_ = a.catalogClient.Close()
		a.catalogClient = nil
	}

	cfg := catalogClientConfig{
		cliBin:       a.cliBin,
		cliExtraArgs: a.cliExtraArgs,
		workDir:      a.workDir,
		registrar:    a.catalogRegistrar,
	}
	// The caller bounds construction and handshake, but must not own the singleton process after
	// FetchSessionList returns. Each later operation still carries its own request context.
	client, err := newGrokCatalogClientWithRequestContext(context.WithoutCancel(ctx), ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.catalogClient = client
	return client, nil
}

// SetCatalogSubprocessRegistrar 由 go-bridge.injectGrokCatalogRegistrar 注入 bridge 的
// ProcessRegistry，使 catalog stdio 子进程注册到 bridge shutdown 回收链（§4.3 / §11）。
func (a *Agent) SetCatalogSubprocessRegistrar(r CatalogSubprocessRegistrar) {
	a.catalogClientMu.Lock()
	a.catalogRegistrar = r
	a.catalogClientMu.Unlock()
}
