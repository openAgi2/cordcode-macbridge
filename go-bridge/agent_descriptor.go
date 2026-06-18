package gobridge

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cccode-macbridge/core"
)

// AgentStatus 表示 agent/provider 的检测状态。
type AgentStatus string

const (
	AgentStatusAvailable          AgentStatus = "available"
	AgentStatusNotDetected        AgentStatus = "not_detected"
	AgentStatusNotLoggedIn        AgentStatus = "not_logged_in"
	AgentStatusServiceNotRunning  AgentStatus = "service_not_running"
	AgentStatusPortConflict       AgentStatus = "port_conflict"
	AgentStatusVersionUnsupported AgentStatus = "version_unsupported"
	AgentStatusPermissionDenied   AgentStatus = "permission_denied"
)

// AgentProviderDescriptor 描述单个 agent/provider 的能力和事件模型，
// iOS 端据此调整刷新/轮询策略。
type AgentProviderDescriptor struct {
	ID                              string      `json:"id"`
	Kind                            string      `json:"kind"`
	DisplayName                     string      `json:"displayName"`
	Status                          AgentStatus `json:"status"`
	Reason                          string      `json:"reason,omitempty"`
	Capabilities                    []string    `json:"capabilities"`
	LiveEvents                      string      `json:"liveEvents"`
	RequiresPollingForExternalTurns bool        `json:"requiresPollingForExternalTurns"`
}

// agentKind 根据 agent ID 返回 kind 字段。
func agentKind(id string) string {
	switch id {
	case "claude":
		return "claude_code"
	case "opencode":
		return "opencode"
	case "codex":
		return "codex"
	default:
		return id
	}
}

func agentDisplayName(id string, agent core.Agent) string {
	switch id {
	case "claude", "claudecode":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	default:
		return agent.Name()
	}
}

// agentLiveEvents 根据 agent ID 返回实时事件模型。
// claude 进程模型是 stdin/stdout pipe，无法广播外部 turn 事件；
// opencode 和 codex 使用服务事件流。
func agentLiveEvents(id string) string {
	switch id {
	case "claude":
		return "session_process"
	default:
		return "broadcast"
	}
}

// agentRequiresPolling 根据 agent ID 判断是否需要轮询外部 turn。
func agentRequiresPolling(id string) bool {
	return id == "claude" || id == "opencode"
}

// deriveCapabilities 从 agent 接口断言推导能力列表，
// 逻辑与 handlers.go BackendList() 保持一致。
func deriveCapabilities(id string, agent core.Agent, codexBackendMode string) []string {
	caps := []string{"model_switch", "session_state"}

	if _, ok := agent.(core.ProviderSwitcher); ok {
		caps = append(caps, "provider_switch")
	}
	if _, ok := agent.(core.HistoryProvider); ok {
		caps = append(caps, "session_history")
	}
	if _, ok := agent.(core.WorkDirSwitcher); ok {
		caps = append(caps, "workspace_diff")
	}
	// session_pagination capability disabled: 去分页方案。backward paging 在长 session 上
	// 造成 newest↔backward 自维持振荡（WebView 渲染抖动→顶部哨兵→loadOlder→再渲染→再抖动）。
	// iOS 在此 capability 缺失时有完整 fallback：fetchMessages 走 getSessionMessagesResult
	// 全量返回（不带 paginate/cursor），一次性读完整个 session。配合 relay MaxFrameBytes=8MB
	// + 写 deadline 60s，全量响应（实测 3-6MB 帧）可单帧传输不超限。重新启用需：relay 帧上限
	// 足够大 + 或改用 content_chunking 分片策略承载超大 session。
	// if _, ok := agent.(core.TranscriptLocator); ok {
	// 	caps = append(caps, "session_pagination")
	// }
	if _, ok := agent.(core.MemoryFileReader); ok {
		caps = append(caps, "memory_read")
	}
	if _, ok := agent.(core.DiagnosticsProvider); ok {
		caps = append(caps, "diagnostics")
	}
	if _, ok := agent.(core.TokenUsageReporter); ok {
		caps = append(caps, "usage_reporting")
	}
	if _, ok := agent.(core.ModeSwitcher); ok {
		caps = append(caps, "permission_mode")
	}
	if _, ok := agent.(core.SessionRenamer); ok {
		if _, ok := agent.(core.SessionArchiver); ok {
			caps = append(caps, "session_mutation")
		}
	}
	if id == "claudecode" {
		caps = append(caps, "content_chunking")
	}
	if _, ok := agent.(core.SessionDeleter); ok {
		caps = append(caps, "session_delete")
	}
	if id != "opencode" && id != "codex" {
		if _, ok := agent.(core.ToolAuthorizer); ok {
			caps = append(caps, "permission_resolve")
		}
	}
	if _, ok := agent.(core.TodoProvider); ok || id == "opencode" {
		caps = append(caps, "todos")
	}
	if id == "codex" && codexBackendMode == "app_server" {
		caps = append(caps, "compression")
	}

	return caps
}

// BuildAgentDescriptor 为单个 agent 构建描述符。
// 通过 detectAgentStatus 检测实际可用性状态，替代硬编码 AgentStatusAvailable。
// cfg 为 nil 时使用默认检测地址。
func BuildAgentDescriptor(id string, agent core.Agent, codexBackendMode string, cfg *AgentDetectionConfig) AgentProviderDescriptor {
	status, reason := detectAgentStatus(id, codexBackendMode, cfg)
	return AgentProviderDescriptor{
		ID:                              id,
		Kind:                            agentKind(id),
		DisplayName:                     agentDisplayName(id, agent),
		Status:                          status,
		Reason:                          reason,
		Capabilities:                    deriveCapabilities(id, agent, codexBackendMode),
		LiveEvents:                      agentLiveEvents(id),
		RequiresPollingForExternalTurns: agentRequiresPolling(id),
	}
}

// BuildAllAgentDescriptors 为所有已注册 agent 构建描述符列表，
// 按 ID 字典序排列保证输出稳定。cfg 为 nil 时使用默认检测地址。
func BuildAllAgentDescriptors(agents map[string]core.Agent, codexBackendMode string, cfg *AgentDetectionConfig) []AgentProviderDescriptor {
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	descs := make([]AgentProviderDescriptor, 0, len(ids))
	for _, id := range ids {
		descs = append(descs, BuildAgentDescriptor(id, agents[id], codexBackendMode, cfg))
	}
	return descs
}

// AgentDetectionConfig 包含 agent 检测所需的外部配置。
type AgentDetectionConfig struct {
	OpenCodeURL       string // OpenCode health check URL，默认 http://localhost:64667
	OpenCodeUser      string // OpenCode auth username
	OpenCodePass      string // OpenCode auth password
	CodexAppServerURL string // Codex app-server WebSocket URL，默认 ws://localhost:4141
}

// detectAgentStatus 检测单个 agent 的可用性状态。
// 所有检测设置超时，避免阻塞 go-bridge 启动。
func detectAgentStatus(id string, codexBackendMode string, cfg *AgentDetectionConfig) (AgentStatus, string) {
	switch id {
	case "claude":
		return detectClaudeCLI()
	case "opencode":
		ocURL := "http://localhost:64667"
		ocUser := ""
		ocPass := ""
		if cfg != nil && cfg.OpenCodeURL != "" {
			ocURL = cfg.OpenCodeURL
		}
		if cfg != nil {
			ocUser = cfg.OpenCodeUser
			ocPass = cfg.OpenCodePass
		}
		return detectOpenCodeService(ocURL, ocUser, ocPass)
	case "codex":
		codexURL := "ws://localhost:4141"
		if cfg != nil && cfg.CodexAppServerURL != "" {
			codexURL = cfg.CodexAppServerURL
		}
		return detectCodexService(codexBackendMode, codexURL)
	default:
		return AgentStatusAvailable, ""
	}
}

// detectClaudeCLI 检测 Claude Code CLI 可用性。
// 使用 exec.LookPath 查找 claude 命令，找到后执行 --version 验证（3秒超时）。
func detectClaudeCLI() (AgentStatus, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	path, err := exec.LookPath("claude")
	if err != nil {
		return AgentStatusNotDetected, "claude CLI not found in PATH"
	}

	cmd := exec.CommandContext(ctx, path, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return AgentStatusNotDetected, "claude --version timed out"
		}
		return AgentStatusNotDetected, fmt.Sprintf("claude --version failed: %s", strings.TrimSpace(string(output)))
	}

	return AgentStatusAvailable, ""
}

// detectOpenCodeService 检测 OpenCode HTTP 服务可用性。
// healthURL 示例：http://localhost:64667/health
func detectOpenCodeService(baseURL, username, password string) (AgentStatus, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	healthURL := strings.TrimRight(baseURL, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		return AgentStatusNotDetected, "invalid OpenCode health URL"
	}
	// 带认证凭据（OpenCode /health 端点需要 auth）
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	if err != nil {
		return AgentStatusNotDetected, "invalid OpenCode health URL"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return AgentStatusServiceNotRunning, fmt.Sprintf("OpenCode service not running at %s", baseURL)
		}
		return AgentStatusNotDetected, fmt.Sprintf("OpenCode unreachable: %s", err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return AgentStatusAvailable, ""
	}
	return AgentStatusServiceNotRunning, fmt.Sprintf("OpenCode health check returned %d", resp.StatusCode)
}

// detectCodexService 检测 Codex 服务可用性。
// app_server 模式：WebSocket dial 5秒超时；exec 模式：exec.LookPath + --version（3秒超时）。
func detectCodexService(codexBackendMode string, appServerURL string) (AgentStatus, string) {
	if codexBackendMode == "app_server" {
		return detectCodexAppServer(appServerURL)
	}
	return detectCodexCLI()
}

var detectCodexAppServerProcessFunc = detectCodexAppServerProcess

// detectCodexAppServer 通过 WebSocket dial 检测 Codex app-server（5秒超时）。
func detectCodexAppServer(appServerURL string) (AgentStatus, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, appServerURL, nil)
	if err != nil {
		if detectCodexAppServerProcessFunc(ctx) {
			return AgentStatusAvailable, ""
		}
		if strings.Contains(err.Error(), "connection refused") {
			return AgentStatusServiceNotRunning, fmt.Sprintf("Codex app-server not running at %s", appServerURL)
		}
		return AgentStatusNotDetected, fmt.Sprintf("Codex app-server unreachable: %s", err.Error())
	}
	conn.Close()
	return AgentStatusAvailable, ""
}

// detectCodexAppServerProcess 识别新版 Codex 桌面 app 的 app-server 进程。
// 当前桌面版不一定在 ws://localhost:4141 暴露 TCP 监听，单靠端口会造成状态页误报。
func detectCodexAppServerProcess(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "ps", "-ax", "-o", "command=")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "codex app-server") {
			return true
		}
	}
	return false
}

// detectCodexCLI 通过 exec.LookPath 检测 Codex CLI（3秒超时）。
func detectCodexCLI() (AgentStatus, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	path, err := exec.LookPath("codex")
	if err != nil {
		return AgentStatusNotDetected, "codex CLI not found in PATH"
	}

	cmd := exec.CommandContext(ctx, path, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return AgentStatusNotDetected, "codex --version timed out"
		}
		return AgentStatusNotDetected, fmt.Sprintf("codex --version failed: %s", strings.TrimSpace(string(output)))
	}

	return AgentStatusAvailable, ""
}
