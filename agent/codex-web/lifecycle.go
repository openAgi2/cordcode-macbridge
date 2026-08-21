package codexweb

// lifecycle.go —— 官方服务生命周期与归属（设计 §5.2/§6）。
//
// 选择顺序（§6.1，Phase 0 修正版）：
//  1. 显式 -codex-web-app-server-url（loopback WebSocket）；
//  2. 官方 daemon 复用：探测 $CODEX_HOME/app-server-control/app-server-control.sock
//     （存活则 WS-over-UDS 复用）；
//  3. 官方 daemon managed start：`codex app-server daemon start`
//     （前置：$CODEX_HOME/packages/standalone/current/codex；socket 路径 < SUN_LEN 104）；
//  4. 兼容托管 `codex app-server --listen ws://127.0.0.1:<port>`，诊断标 managed-loopback-ws。
//
// 官方源码锚点：cli/src/main.rs:2588-2601（agents 入口自动启动 daemon）、
// tui/src/lib.rs:275/436/851/912-925（AppServerTarget/socket 探测/复用判定）、
// app-server-daemon/src/lib.rs:191。
//
// Phase 0 样本：dumps/lifecycle（daemon absent/start/running/stop + WS healthz/readyz）。

// ServiceSource 区分 daemon 归属（§6.3：不得把两种来源都简化成 connected）。
type ServiceSource string

const (
	SourceExternalDaemonReused ServiceSource = "external-daemon-reused"
	SourceCordCodeStartedDaemon ServiceSource = "cordcode-started-daemon"
	SourceManagedLoopbackWS     ServiceSource = "managed-loopback-ws"
)

// ServiceEndpoint 描述已就绪的官方服务连接目标。
type ServiceEndpoint struct {
	Source ServiceSource
	// UnixSocket 为 daemon control socket 路径（WS-over-UDS）；TCPEndpoint 为托管 WS。
	UnixSocket  string
	TCPEndpoint string
	CLIVersion  string
}

// Probe 依次执行 §6.2 六步就绪判定（transport→initialize→initialized→thread/list→
// model/list→contract 核对）。任一步失败返回结构化错误（not_configured/incompatible，
// 保留官方 JSON-RPC error 原文）；禁止退回 JSONL parser 假装可用。
//
// Phase 1 实现落点。
func Probe(opts ProbeOptions) (*ServiceEndpoint, error) { return nil, errNotImplemented }

// ProbeOptions 是生命周期探测输入（显式 URL 优先）。
type ProbeOptions struct {
	ExplicitURL string
	CodexHome   string
}
