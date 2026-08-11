package codex

// catalog_client.go 是 Codex 的长寿命、thread-unbound app-server catalog client（设计
// §4.3 / §5.1 step 1-3，Phase 2 Stream A）。
//
// 它与 per-session turn 载体 appServerSession 的关系（§4.3 现状澄清）：
//   - 复用 appServerSession 的 JSON-RPC 收发骨架（request/notify/readLoop/handleResponse
//     + id 相关性的 pending map + rejectPending）。
//   - **不复用**按 session 生灭的 StartSession/ensureThread 生命周期：catalog client 不
//     绑定任何 thread，只发 initialize + thread/list，不处理 turn/item/notification。
//
// 传输选型（§4.3，Phase 0 由 thread_list_contract_test.go::codexCatalogTransportSelection
// 冻结，二选一后不得自由切换）：
//   - appServerURLSet（配置了 -codex-app-server-url）→ 共享 WebSocket，无子进程；
//   - 否则 → 单例 stdio `codex app-server` 子进程。
//
// 进程管理红线（§4.3）：stdio 子进程的生命周期/Close 必须照 codexSession 的进程组模式
// （prepareCmdForKill/Setpgid + forceKillCmd 进程组 kill + 注册到 bridge ProcessRegistry
// 供 handlers.Shutdown 回收），**不得**照搬 appServerSession.Close 的 Process.Kill()
// （只杀直属子进程，漏 codex app-server fork 的孙子进程）。

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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// CatalogSubprocessRegistrar 把 catalog stdio 子进程注册到 bridge 的 ProcessRegistry，
// 使 handlers.Shutdown 经进程组 SIGKILL 回收它（§4.3 / §11）。go-bridge 的 *ProcessRegistry
// 结构化满足该接口（Register(*exec.Cmd) (unregister func())）。nil 合法（单测 / ws 传输：
// 无子进程需跟踪）。
type CatalogSubprocessRegistrar interface {
	Register(cmd *exec.Cmd) (unregister func())
}

// catalogClientConfig 是构造单例 catalog client 的全部输入。由 codex.Agent 从自身配置
// 派生（appServerURL/appServerURLSet/workDir/codexHome/cliBin/extraEnv）+ bridge 注入的
// registrar。
type catalogClientConfig struct {
	appServerURL    string                     // ws URL（appServerURLSet=true 时用）
	appServerURLSet bool                       // -codex-app-server-url 是否配置（传输选型开关）
	workDir         string                     // stdio 子进程 cwd（catalog 不以此过滤；thread/list 的 cwd 是请求参数）
	codexHome       string                     // CODEX_HOME（stdio 子进程 env）
	cliBin          string                     // codex 二进制名（默认 "codex"）
	extraEnv        []string                   // provider/session env（auth.json 等）
	registrar       CatalogSubprocessRegistrar // stdio 子进程注册器（nil=ws 或单测）
}

// catalogClient 是一个长寿命、thread-unbound 的 app-server catalog 连接。一个连接服务
// 多次 thread/list（不同 cwd）；连接本身 cwd-agnostic，cwd 是每次 thread/list 的请求参数。
type catalogClient struct {
	cfg       catalogClientConfig
	transport string // appServerTransportWebSocket | appServerTransportStdio

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

	alive     atomic.Bool
	closeOnce sync.Once
	wg        sync.WaitGroup

	unregister func() // registrar 注销回调（stdio case；Close 时调）
}

// catalogRequestTimeout 是 catalog JSON-RPC 请求（initialize / thread/list）的总超时。
// catalog 是低频控制面调用（每次 list 刷新一次），给一个比 per-turn 更宽的上限以容忍
// app-server 冷启动 + 大 workspace thread/list 扫描。
const catalogRequestTimeout = 60 * time.Second

// catalogReadMaxLineSize 是 readLoop 单行上限（与 appServerSession 一致：10MB）。
const catalogReadMaxLineSize = 10 * 1024 * 1024

// newCatalogClient 建立并初始化一个 catalog client（connect → initialize）。
// 传输：appServerURLSet→ws；否则→stdio 单例子进程（进程组模式 + 注册 registry）。
func newCatalogClient(ctx context.Context, cfg catalogClientConfig) (*catalogClient, error) {
	return newCatalogClientWithRequestContext(ctx, ctx, cfg)
}

// newCatalogClientWithRequestContext separates the singleton transport lifetime from the bounded
// request that caused its lazy creation. The transport owns lifetimeCtx; connection initialization
// still honors requestCtx. Binding both to the first list request created a live-process/dead-context
// zombie as soon as that request's defer cancel ran.
func newCatalogClientWithRequestContext(lifetimeCtx, requestCtx context.Context, cfg catalogClientConfig) (*catalogClient, error) {
	if err := requestCtx.Err(); err != nil {
		return nil, err
	}
	if cfg.cliBin == "" {
		cfg.cliBin = "codex"
	}
	transport := appServerTransportStdio
	if cfg.appServerURLSet {
		transport = appServerTransportWebSocket
	}

	sessionCtx, cancel := context.WithCancel(lifetimeCtx)
	c := &catalogClient{
		cfg:       cfg,
		transport: transport,
		ctx:       sessionCtx,
		cancel:    cancel,
		pending:   make(map[int64]chan rpcResponseEnvelope),
	}
	c.alive.Store(true)

	slog.Info("codex catalog client: creating",
		"transport", transport,
		"app_server_url_set", cfg.appServerURLSet,
		"work_dir", cfg.workDir,
	)

	if err := c.connect(); err != nil {
		cancel()
		return nil, err
	}
	if err := requestCtx.Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.initializeContext(requestCtx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *catalogClient) connect() error {
	if c.transport == appServerTransportWebSocket {
		return c.connectWebSocket()
	}
	return c.connectStdio()
}

// connectStdio 启动单例 `codex app-server` 子进程（进程组模式）。进程生命周期照 §4.3
// 红线：prepareCmdForKill(Setpgid) + 注册 registrar（bridge ProcessRegistry）。
func (c *catalogClient) connectStdio() error {
	args := []string{"app-server"}
	cmd := exec.CommandContext(c.ctx, c.cfg.cliBin, args...)
	cmd.Dir = c.cfg.workDir
	sessionEnv := append([]string(nil), c.cfg.extraEnv...)
	if c.cfg.codexHome != "" {
		sessionEnv = append(sessionEnv, "CODEX_HOME="+c.cfg.codexHome)
	}
	// Controlled agent env（与 appServerSession 一致：防 CCCODE_* 控制面泄漏）。
	cmd.Env = core.BuildAgentEnv(
		core.FilterEnvToAllowlist(os.Environ(), core.AgentEnvRuntimeAllowlist()),
		nil,
		sessionEnv,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex catalog stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex catalog stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("codex catalog stderr pipe: %w", err)
	}

	// §4.3 红线：进程组模式（Setpgid），不得照搬 appServerSession 的裸 Process.Kill()。
	prepareCmdForKill(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codex catalog start: %w", err)
	}

	// 注册到 bridge ProcessRegistry（若注入了 registrar），使 handlers.Shutdown 回收整组。
	if c.cfg.registrar != nil {
		c.unregister = c.cfg.registrar.Register(cmd)
	}

	c.procMu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.procMu.Unlock()

	slog.Info("codex catalog client started", "transport", "stdio", "pid", cmd.Process.Pid, "work_dir", c.cfg.workDir)

	c.wg.Add(2)
	go c.readLoop(stdout)
	go c.stderrLoop(stderr)
	return nil
}

// connectWebSocket 拨号共享 app-server WebSocket（appServerURLSet=true）。无子进程。
func (c *catalogClient) connectWebSocket() error {
	wsURL := strings.TrimSpace(c.cfg.appServerURL)
	if wsURL == "" {
		return fmt.Errorf("codex catalog websocket URL is empty")
	}
	if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	} else if strings.HasPrefix(wsURL, "https://") {
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(c.ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("codex catalog ws dial %s: %w", wsURL, err)
	}

	c.procMu.Lock()
	c.conn = conn
	c.procMu.Unlock()

	slog.Info("codex catalog client connected", "transport", "websocket", "work_dir", c.cfg.workDir)

	c.wg.Add(1)
	go c.wsReadLoop()
	return nil
}

// initialize 执行 app-server 握手（initialize request + initialized notify）。与
// appServerSession.initialize 同形：clientInfo + experimentalApi capabilities + optOut
// 高频 delta notification（catalog 不消费 turn/item delta，opt out 降低噪音）。
func (c *catalogClient) initialize() error {
	return c.initializeContext(c.ctx)
}

func (c *catalogClient) initializeContext(ctx context.Context) error {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    "cc-connect-codex-catalog",
			"title":   "CC Connect Codex Catalog",
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
	if err := c.requestWithContext(ctx, "initialize", params, &resp); err != nil {
		return fmt.Errorf("codex catalog initialize: %w", err)
	}
	if err := c.notify("initialized", nil); err != nil {
		return fmt.Errorf("codex catalog initialized notify: %w", err)
	}
	return nil
}

func (c *catalogClient) requestWithContext(ctx context.Context, method string, params any, out any) error {
	return c.requestWithTimeoutContext(ctx, method, params, out, catalogRequestTimeout)
}

// ── JSON-RPC 收发骨架（复用 appServerSession 模式，精简：只处理 response） ───────

func (c *catalogClient) request(method string, params any, out any) error {
	return c.requestWithTimeout(method, params, out, catalogRequestTimeout)
}

func (c *catalogClient) requestWithTimeout(method string, params any, out any, timeout time.Duration) error {
	return c.requestWithTimeoutContext(c.ctx, method, params, out, timeout)
}

func (c *catalogClient) requestWithTimeoutContext(ctx context.Context, method string, params any, out any, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := c.nextID.Add(1)
	ch := make(chan rpcResponseEnvelope, 1)

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[int64]chan rpcResponseEnvelope)
	}
	c.pending[id] = ch
	c.pendingMu.Unlock()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeJSON(payload); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
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
	case <-c.ctx.Done():
		return c.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(timeout):
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return fmt.Errorf("%s timed out", method)
	}
}

func (c *catalogClient) notify(method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	return c.writeJSON(payload)
}

func (c *catalogClient) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("codex catalog encode: %w", err)
	}
	return c.writeMessage(b)
}

func (c *catalogClient) writeMessage(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return err
	}

	c.procMu.Lock()
	conn := c.conn
	stdin := c.stdin
	transport := c.transport
	c.procMu.Unlock()

	if transport == appServerTransportWebSocket {
		if conn == nil {
			return fmt.Errorf("codex catalog websocket is closed")
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return fmt.Errorf("codex catalog websocket write: %w", err)
		}
		return nil
	}

	if stdin == nil {
		return fmt.Errorf("codex catalog connection is closed")
	}
	if _, err := stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("codex catalog stdin write: %w", err)
	}
	return nil
}

func (c *catalogClient) readLoop(r io.Reader) {
	defer c.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), catalogReadMaxLineSize)

	for scanner.Scan() {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		c.handleRPCMessage(scanner.Bytes())
	}

	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	if c.ctx.Err() == nil && !errors.Is(err, io.EOF) {
		slog.Warn("codex catalog read failed", "error", err)
	}
	c.alive.Store(false)
	c.rejectPending(err)
}

func (c *catalogClient) wsReadLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.procMu.Lock()
		conn := c.conn
		c.procMu.Unlock()
		if conn == nil {
			c.alive.Store(false)
			c.rejectPending(io.EOF)
			return
		}

		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if c.ctx.Err() == nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				slog.Warn("codex catalog websocket read failed", "error", err)
			}
			c.alive.Store(false)
			c.rejectPending(err)
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		c.handleRPCMessage(data)
	}
}

// handleRPCMessage 分类 JSON-RPC envelope。catalog client 只发 request（initialize/
// thread/list），不消费任何 notification/server-request（不启动 turn，故 app-server 不会
// 推 turn/item/requestUserInput）。response → handleResponse 相关性回收；其余记 debug
// 后忽略（catalog 不需要）。
func (c *catalogClient) handleRPCMessage(data []byte) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		slog.Debug("codex catalog: invalid JSON", "error", err)
		return
	}

	_, hasMethod := probe["method"]
	_, hasID := probe["id"]
	_, hasResult := probe["result"]
	_, hasError := probe["error"]

	switch classifyRPCEnvelope(hasMethod, hasID, hasResult, hasError) {
	case envelopeResponse:
		var resp rpcResponseEnvelope
		if err := json.Unmarshal(data, &resp); err != nil {
			slog.Debug("codex catalog: bad response envelope", "error", err)
			return
		}
		c.handleResponse(resp)
	case envelopeNotification:
		// catalog 不消费 notification（不启动 turn）。
	case envelopeServerRequest:
		// catalog 不启动 turn，不应收到 server request；忽略（不 respond 以免复杂化；
		// app-server 不会对无 turn 的 catalog 连接发 requestUserInput）。
		slog.Debug("codex catalog: ignoring unexpected server request")
	default:
		slog.Debug("codex catalog: unclassifiable envelope; ignored")
	}
}

func (c *catalogClient) handleResponse(resp rpcResponseEnvelope) {
	id, ok := rpcIDToInt64(resp.ID)
	if !ok {
		return
	}
	c.pendingMu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func (c *catalogClient) rejectPending(err error) {
	c.pendingMu.Lock()
	ids := make([]int64, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	for _, id := range ids {
		ch := c.pending[id]
		delete(c.pending, id)
		if ch != nil {
			select {
			case ch <- rpcResponseEnvelope{ID: id, Error: &rpcError{Message: err.Error()}}:
			default:
			}
		}
	}
	c.pendingMu.Unlock()
}

func (c *catalogClient) stderrLoop(r io.Reader) {
	defer c.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Redact before logging（与 appServerSession 一致：stderr 可能回显 env/token）。
		slog.Debug("codex catalog stderr", "line", core.RedactStderr(line))
	}
	if err := scanner.Err(); err != nil && c.ctx.Err() == nil {
		slog.Debug("codex catalog stderr read failed", "error", err)
	}
}

// Alive 报告 catalog client 是否仍可用（连接未关闭、readLoop 未退出）。
func (c *catalogClient) Alive() bool {
	return c.alive.Load()
}

// Close 关闭 catalog client。stdio：经进程组 SIGKILL 回收子进程（§4.3 红线，forceKillCmd
// 杀整组含孙子）+ 注销 registry。ws：关闭连接。幂等。
func (c *catalogClient) Close() error {
	c.closeOnce.Do(func() {
		c.alive.Store(false)
		c.cancel()

		c.procMu.Lock()
		if c.conn != nil {
			_ = c.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(2*time.Second),
			)
			_ = c.conn.Close()
			c.conn = nil
		}
		if c.stdin != nil {
			_ = c.stdin.Close()
			c.stdin = nil
		}
		cmd := c.cmd
		c.procMu.Unlock()

		// §4.3 红线：进程组 SIGKILL（含孙子），不得用 cmd.Process.Kill()。
		if cmd != nil {
			_ = forceKillCmd(cmd)
		}
		// 注销 bridge ProcessRegistry（若曾注册）。
		if c.unregister != nil {
			c.unregister()
			c.unregister = nil
		}

		done := make(chan struct{})
		go func() {
			c.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			slog.Debug("codex catalog: close timed out waiting for goroutines")
		}
	})
	return nil
}
