package codexweb

// lifecycle.go —— 官方服务生命周期与归属（设计 §5.2/§6）。
//
// 选择顺序（§6.1，Desktop attach Gate 修正版）：
//  1. 显式 -codex-web-app-server-url（仅隔离测试/非 Desktop 实验）；
//  2. Desktop 产品路径复用官方 daemon：探测 $CODEX_HOME/app-server-control/app-server-control.sock
//     （WS-over-UDS 连接成功即复用，绝不 stop/restart 外部 daemon）；
//  3. 官方 daemon managed start：`codex app-server daemon start`
//     （前置：$CODEX_HOME/packages/standalone/current/codex；socket 路径 < SUN_LEN 104）；
//  4. daemon 不可用时 fail closed。禁止另起 managed-loopback app-server，因为 Desktop
//     无法连接该实例；两个 app-server 共享 store 仍会分裂 writer/订阅/实时事件。
//
// 官方源码锚点：cli/src/main.rs:2588-2601（agents 入口自动启动 daemon）、
// tui/src/lib.rs:275/436/851/912-925（AppServerTarget/socket 探测/复用判定）、
// app-server-daemon/src/lib.rs:191、app-server-transport websocket.rs:135-150
// （非 loopback 无 auth 拒绝启动 → 托管只绑 127.0.0.1）。
//
// Phase 0 样本：testdata/official-0.149.0-alpha.4/dumps/lifecycle。
//
// 就绪判定（§6.2 六步）：transport → initialize → initialized → thread/list 最小请求 →
// model/list → contract 核对。任一步失败 → not_configured / incompatible（保留官方 error 原文）；
// 禁止退回 JSONL parser 假装可用。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServiceSource 区分 daemon 归属（§6.3：不得把两种来源都简化成 connected）。
type ServiceSource string

const (
	SourceExplicitURL           ServiceSource = "explicit-url"
	SourceExternalDaemonReused  ServiceSource = "external-daemon-reused"
	SourceCordCodeStartedDaemon ServiceSource = "cordcode-started-daemon"
	// SourceManagedLoopbackWS 只用于识别并安全清理旧版本留下的 owned record，
	// 不再是 Probe 的可选产品结果。
	SourceManagedLoopbackWS ServiceSource = "managed-loopback-ws"
)

// Backend 状态码（§6.2：任一步失败标 not_configured 或 incompatible）。
const (
	StatusNotConfigured = "not_configured"
	StatusIncompatible  = "incompatible"
)

// maxUnixSocketPath 是 macOS sun_path 长度（SUN_LEN=104 含 NUL；Phase 0 长临时目录实录
// "path must be shorter than SUN_LEN"）。
const maxUnixSocketPath = 103

// ServiceEndpoint 描述已就绪的官方服务连接目标（就绪=六步全过）。
type ServiceEndpoint struct {
	Source ServiceSource
	// UnixSocket 为 daemon control socket 路径（WS-over-UDS）；TCPEndpoint 为托管/显式 WS。
	UnixSocket  string
	TCPEndpoint string
	// CLIVersion / AppServerVersion 来自 initialize 响应 userAgent 与 daemon version JSON。
	CLIVersion       string
	AppServerVersion string
	CodexHome        string
	// StartedByCordCode：本 backend 调用过 daemon start（§6.3）。即便如此，Close 也不停
	// 共享 daemon（避免中断官方客户端）；只有托管 WS 进程被独占回收。
	StartedByCordCode bool

	client     *Client
	deps       LifecycleDeps
	closeOnce  *sync.Once
	closeError error
}

// StatusError 是结构化失败（step 指明六步中的哪一步；official 保留官方原文）。
type StatusError struct {
	Status   string // not_configured | incompatible
	Step     string
	Official string
	Err      error
}

func (e *StatusError) Error() string {
	if e.Official != "" {
		return fmt.Sprintf("codex-web %s at %s: %s", e.Status, e.Step, e.Official)
	}
	return fmt.Sprintf("codex-web %s at %s: %v", e.Status, e.Step, e.Err)
}

func (e *StatusError) Unwrap() error { return e.Err }

func notConfigured(step string, err error) error {
	return &StatusError{Status: StatusNotConfigured, Step: step, Err: err}
}

func incompatible(step string, official string, err error) error {
	return &StatusError{Status: StatusIncompatible, Step: step, Official: official, Err: err}
}

// ProbeOptions 是生命周期探测输入。
type ProbeOptions struct {
	ExplicitURL string
	CodexHome   string
	WorkDir     string
	// DataDir 仅用于查找并安全清理旧版 codex-web-managed-server.json；新产品路径不再写入。
	DataDir string
	// ExperimentalAPI 让 initialize capabilities 声明 experimentalApi（experimental 面
	// 逐项版本门控后才由调用方打开；§11.2）。
	ExperimentalAPI bool
}

// LifecycleDeps 是可注入缝（单测用 fake；生产用默认实现）。
type LifecycleDeps struct {
	// ResolveCodexBinary 返回官方 codex 二进制路径。
	ResolveCodexBinary func() (string, error)
	// RunDaemonStart 执行 `codex app-server daemon start`（返回 combined 输出）。
	RunDaemonStart func(bin, codexHome string) (string, error)
	// SocketExists 探测 control socket 文件存在。
	SocketExists func(socketPath string) bool
	// DialUDS / DialTCP 建立 Transport。
	DialUDS func(ctx context.Context, socketPath string) (Transport, error)
	DialTCP func(ctx context.Context, url string) (Transport, error)
	// 托管实例遗留校验/收口。必须同时核对 PID、argv、启动时间与监听端口，
	// 任一不匹配只删除陈旧 record，绝不终止进程。
	InspectProcess   func(pid int) (command, startTime string, alive bool)
	ProcessOwnsPort  func(pid, port int) bool
	TerminateProcess func(pid int) error
}

// DefaultDeps 返回生产实现。
func DefaultDeps() LifecycleDeps {
	return LifecycleDeps{
		ResolveCodexBinary: ResolveCodexBinary,
		RunDaemonStart:     runDaemonStart,
		SocketExists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		DialUDS:          DialWSUnix,
		DialTCP:          DialWSTCP,
		InspectProcess:   inspectManagedProcess,
		ProcessOwnsPort:  managedProcessOwnsPort,
		TerminateProcess: terminateManagedProcess,
	}
}

// ResolveCodexBinary resolves the official standalone under the default
// CODEX_HOME. Desktop shared-daemon mode must not start a daemon from PATH or
// the Desktop embedded binary: either could differ from the host version and
// silently force Desktop back to its private stdio runtime.
func ResolveCodexBinary() (string, error) {
	home, err := ResolveCodexHome("")
	if err != nil {
		return "", err
	}
	return ResolveCodexBinaryForHome(home)
}

// ResolveCodexBinaryForHome keeps the explicit test override, then requires the
// official managed standalone belonging to the active CODEX_HOME. There is no
// PATH/Desktop fallback on the product daemon-start path.
func ResolveCodexBinaryForHome(codexHome string) (string, error) {
	if p := os.Getenv("CODEX_WEB_CODEX_BIN"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("CODEX_WEB_CODEX_BIN=%s 不存在", p)
	}
	home, err := ResolveCodexHome(codexHome)
	if err != nil {
		return "", err
	}
	standalone := filepath.Join(home, "packages", "standalone", "current", "codex")
	if _, err := os.Stat(standalone); err == nil {
		return standalone, nil
	}
	return "", fmt.Errorf("official managed standalone not found: %s", standalone)
}

// ControlSocketPath 推导官方 control socket 路径（app-server-transport transport/mod.rs:58；
// 目录 app-server-control、文件 app-server-control.sock）。路径超长返回错误（SUN_LEN）。
func ControlSocketPath(codexHome string) (string, error) {
	p := filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
	if len(p) >= maxUnixSocketPath {
		return "", fmt.Errorf("control socket 路径超 macOS SUN_LEN（%d ≥ %d）：%s", len(p), maxUnixSocketPath, p)
	}
	return p, nil
}

// ResolveCodexHome mirrors the official CLI convention: explicit option, then
// CODEX_HOME, then ~/.codex. Product wiring intentionally leaves the option empty,
// so treating it as a relative directory would miss the real daemon and recreate
// the split-runtime bug.
func ResolveCodexHome(explicit string) (string, error) {
	if home := strings.TrimSpace(explicit); home != "" {
		return filepath.Clean(home), nil
	}
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Clean(home), nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return "", fmt.Errorf("resolve default CODEX_HOME: %w", err)
	}
	return filepath.Join(userHome, ".codex"), nil
}

func runDaemonStart(bin, codexHome string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "app-server", "daemon", "start")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("daemon start rc=%v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Probe 按 §6.1 顺序建立连接并完成 §6.2 六步就绪判定。
func Probe(opts ProbeOptions) (*ServiceEndpoint, error) {
	return ProbeWith(LifecycleDeps{}, opts)
}

// ProbeWith 允许注入 deps（单测）。
func ProbeWith(deps LifecycleDeps, opts ProbeOptions) (*ServiceEndpoint, error) {
	resolvedHome, err := ResolveCodexHome(opts.CodexHome)
	if err != nil {
		return nil, notConfigured("resolve-codex-home", err)
	}
	opts.CodexHome = resolvedHome
	if deps.ResolveCodexBinary == nil {
		deps.ResolveCodexBinary = func() (string, error) {
			return ResolveCodexBinaryForHome(resolvedHome)
		}
	}
	fillDeps(&deps)

	if opts.ExplicitURL != "" {
		// 显式配置存在时以它为准，失败直接报错（设计 §1"失败可见"：不静默降级到其他路径）。
		ep, err := probeExplicit(deps, opts)
		if err == nil {
			if bin, resolveErr := deps.ResolveCodexBinary(); resolveErr == nil {
				cleanupRecordedManaged(opts, bin, deps)
			}
		}
		return ep, err
	}

	bin, err := deps.ResolveCodexBinary()
	if err != nil {
		return nil, notConfigured("resolve-standalone", err)
	}

	// daemon 复用：socket 存在即尝试连接。注意语义区分：
	//   - 连接失败（如 stale socket 文件）→ 继续下一条路径；
	//   - 连接成功但就绪判定失败 → 直接返回该错误（真实 daemon 已应答；同一二进制的
	//     托管路径也会以同样方式失败，静默降级只会掩盖 §6.2 的失败可见要求）。
	socketPath, serr := ControlSocketPath(opts.CodexHome)
	if serr == nil && deps.SocketExists(socketPath) {
		if t, derr := deps.DialUDS(context.Background(), socketPath); derr == nil {
			ep, rerr := establishOnTransport(deps, opts, t, ServiceEndpoint{
				Source:     SourceExternalDaemonReused,
				UnixSocket: socketPath,
			})
			if rerr != nil {
				_ = t.Close()
				return nil, rerr
			}
			cleanupRecordedManaged(opts, bin, deps)
			return ep, nil
		}
	}

	// daemon managed start。Desktop 产品要求单一官方 daemon；失败不得另起 loopback
	// app-server，否则会重现跨进程 writer lock 与 live 订阅分裂。
	daemonStartOut, daemonStartErr := deps.RunDaemonStart(bin, opts.CodexHome)
	if daemonStartErr == nil && serr == nil &&
		waitSocket(deps, socketPath, 30*time.Second) {
		if t, derr2 := deps.DialUDS(context.Background(), socketPath); derr2 == nil {
			ep, rerr := establishOnTransport(deps, opts, t, ServiceEndpoint{
				Source:            SourceCordCodeStartedDaemon,
				UnixSocket:        socketPath,
				StartedByCordCode: true,
			})
			if rerr != nil {
				_ = t.Close()
				return nil, rerr
			}
			cleanupRecordedManaged(opts, bin, deps)
			return ep, nil
		}
	}

	// 新版本不再恢复或启动 managed-loopback。若旧产品留下 verified owned record，
	// 只做安全回收，随后保留 daemon 的真实失败原文。
	cleanupRecordedManaged(opts, bin, deps)
	return nil, notConfigured("shared-daemon-required", fmt.Errorf(
		"Desktop 共享运行时要求官方 local daemon；daemon start err=%v socketPathErr=%v（daemon start 输出：%s）",
		daemonStartErr, serr, strings.TrimSpace(daemonStartOut)))
}

func probeExplicit(deps LifecycleDeps, opts ProbeOptions) (*ServiceEndpoint, error) {
	t, err := deps.DialTCP(context.Background(), opts.ExplicitURL)
	if err != nil {
		return nil, notConfigured("explicit-url-dial", err)
	}
	return establishOnTransport(deps, opts, t, ServiceEndpoint{Source: SourceExplicitURL, TCPEndpoint: opts.ExplicitURL})
}

func establishOnTransport(deps LifecycleDeps, opts ProbeOptions, t Transport, ep ServiceEndpoint) (*ServiceEndpoint, error) {
	if ep.closeOnce == nil {
		ep.closeOnce = &sync.Once{}
	}
	// 步 1：transport 建立（此处已成立）
	// 步 2：initialize
	c := NewClient(t, ConnectionEpoch(time.Now().UnixNano()))
	caps := map[string]any{}
	if opts.ExperimentalAPI {
		caps["experimentalApi"] = true
	}
	initParams := map[string]any{
		"clientInfo":   map[string]any{"name": "cordcode-codex-web", "version": "0.0.1"},
		"capabilities": caps,
	}
	raw, rpcErr, err := c.Request("initialize", initParams)
	if err != nil {
		_ = c.Close()
		return nil, notConfigured("initialize", err)
	}
	if rpcErr != nil {
		_ = c.Close()
		return nil, incompatible("initialize", rpcErr.Error(), rpcErr)
	}
	var init struct {
		UserAgent  string `json:"userAgent"`
		CodexHome  string `json:"codexHome"`
		PlatformOS string `json:"platformOs"`
	}
	if err := json.Unmarshal(raw, &init); err != nil || init.UserAgent == "" {
		_ = c.Close()
		return nil, incompatible("initialize-shape", "", fmt.Errorf("initialize 响应缺 userAgent：%s", string(raw)))
	}
	// 步 3：initialized 通知
	if err := c.Notify("initialized", nil); err != nil {
		_ = c.Close()
		return nil, notConfigured("initialized-notify", err)
	}
	// 步 4：thread/list 最小请求
	tlRaw, tlErr, err := c.Request("thread/list", map[string]any{"limit": 1})
	if err != nil {
		_ = c.Close()
		return nil, notConfigured("thread-list", err)
	}
	if tlErr != nil {
		_ = c.Close()
		return nil, incompatible("thread-list", tlErr.Error(), tlErr)
	}
	// 步 5：model/list
	mlRaw, mlErr, err := c.Request("model/list", map[string]any{})
	if err != nil {
		_ = c.Close()
		return nil, notConfigured("model-list", err)
	}
	if mlErr != nil {
		_ = c.Close()
		return nil, incompatible("model-list", mlErr.Error(), mlErr)
	}
	// 步 6：contract 核对（thread/model 目录形状与冻结 bundle 一致：data 数组 + nextCursor 可选）
	if err := contractCheckThreadList(tlRaw); err != nil {
		_ = c.Close()
		return nil, incompatible("contract-thread-list", "", err)
	}
	if err := contractCheckModelList(mlRaw); err != nil {
		_ = c.Close()
		return nil, incompatible("contract-model-list", "", err)
	}

	ep.client = c
	ep.deps = deps
	ep.CLIVersion = extractVersionFromUserAgent(init.UserAgent)
	ep.AppServerVersion = ep.CLIVersion
	ep.CodexHome = init.CodexHome
	return &ep, nil
}

func extractVersionFromUserAgent(ua string) string {
	// Phase 0 样本：userAgent = "cordcode-codex-web/0.149.0-alpha.4 (Mac OS ...)"
	if i := strings.Index(ua, "/"); i >= 0 {
		rest := ua[i+1:]
		if j := strings.IndexAny(rest, " ("); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return ua
}

// contractCheckThreadList：与冻结 bundle（testdata .../schemas/stable-v2.bundle.json
// ThreadListResponse）核对形状：顶层必须含 data（数组或 null），可选 nextCursor。
func contractCheckThreadList(raw json.RawMessage) error {
	return requireDataArray("thread/list", raw)
}

// contractCheckModelList：ModelListResponse = {data, nextCursor}。
func contractCheckModelList(raw json.RawMessage) error {
	return requireDataArray("model/list", raw)
}

func requireDataArray(method string, raw json.RawMessage) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("%s 响应非对象形状：%v", method, err)
	}
	d, ok := top["data"]
	if !ok {
		return fmt.Errorf("%s 响应缺 data 字段（与冻结 contract 不一致）：%s", method, string(raw))
	}
	if string(d) == "null" {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(d, &arr); err != nil {
		return fmt.Errorf("%s 响应 data 非数组：%v", method, err)
	}
	return nil
}

func waitSocket(deps LifecycleDeps, socketPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if deps.SocketExists(socketPath) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// Client 返回就绪连接上的 JSON-RPC 客户端（事件泵/会话层的入口）。
func (e *ServiceEndpoint) Client() *Client { return e.client }

// OpenClient 在已经解析出的同一个官方服务实例上建立另一条 initialized connection。
// 它只 dial e 的固定 UDS/TCP endpoint，绝不重新执行 Probe 或启动服务。
func (e *ServiceEndpoint) OpenClient(ctx context.Context, opts ProbeOptions) (*Client, error) {
	var (
		t   Transport
		err error
	)
	switch {
	case e.UnixSocket != "":
		t, err = e.deps.DialUDS(ctx, e.UnixSocket)
	case e.TCPEndpoint != "":
		t, err = e.deps.DialTCP(ctx, e.TCPEndpoint)
	default:
		return nil, notConfigured("shared-endpoint-dial", errors.New("resolved endpoint has no UDS or TCP address"))
	}
	if err != nil {
		return nil, notConfigured("shared-endpoint-dial", err)
	}
	copyEndpoint := ServiceEndpoint{
		Source: e.Source, UnixSocket: e.UnixSocket, TCPEndpoint: e.TCPEndpoint,
		StartedByCordCode: e.StartedByCordCode,
	}
	opened, err := establishOnTransport(e.deps, opts, t, copyEndpoint)
	if err != nil {
		return nil, err
	}
	return opened.Client(), nil
}

// Source 返回归属（§6.3 诊断区分用）。
func (e *ServiceEndpoint) SourceKind() ServiceSource { return e.Source }

// Close only closes this connection. Shared daemon ownership is external even
// when CordCode initially started it, so Close never stops a runtime process.
func (e *ServiceEndpoint) Close() error {
	if e.closeOnce == nil {
		e.closeOnce = &sync.Once{}
	}
	e.closeOnce.Do(func() {
		if e.client != nil {
			if err := e.client.Close(); err != nil && e.closeError == nil {
				e.closeError = err
			}
		}
	})
	return e.closeError
}

func fillDeps(d *LifecycleDeps) {
	def := DefaultDeps()
	if d.ResolveCodexBinary == nil {
		d.ResolveCodexBinary = def.ResolveCodexBinary
	}
	if d.RunDaemonStart == nil {
		d.RunDaemonStart = def.RunDaemonStart
	}
	if d.SocketExists == nil {
		d.SocketExists = def.SocketExists
	}
	if d.DialUDS == nil {
		d.DialUDS = def.DialUDS
	}
	if d.DialTCP == nil {
		d.DialTCP = def.DialTCP
	}
	if d.InspectProcess == nil {
		d.InspectProcess = def.InspectProcess
	}
	if d.ProcessOwnsPort == nil {
		d.ProcessOwnsPort = def.ProcessOwnsPort
	}
	if d.TerminateProcess == nil {
		d.TerminateProcess = def.TerminateProcess
	}
}
