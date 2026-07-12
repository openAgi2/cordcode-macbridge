package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
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
	// AgentStatusNotConfigured: OpenCode endpoint URL 未配置（source disabled / external_http
	// 未填 URL）。不 dial 64667，明确告知 iOS 该 backend 当前不可用，需先配置 endpoint。
	AgentStatusNotConfigured AgentStatus = "not_configured"
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
	case "grokbuild":
		return "grokbuild" // 不转 snake_case，与 iOS fromWireKind 的 case "grokbuild" 对应
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
	case "grokbuild":
		return "Grok Build"
	default:
		return agent.Name()
	}
}

// agentLiveEvents 根据 agent ID 返回实时事件模型。
// claude 进程模型是 stdin/stdout pipe，无法广播外部 turn 事件；
// opencode 使用服务事件流；codex 只有显式共享 app-server URL 时才有进程级广播。
func agentLiveEvents(id string, codexBackendMode string, cfg *AgentDetectionConfig) string {
	switch id {
	case "claude":
		return "session_process"
	case "grokbuild":
		return "session_process" // grok agent stdio 是 stdin/stdout pipe，与 claude 同为进程模型
	case "codex":
		if codexBackendMode == "app_server" && cfg != nil && strings.TrimSpace(cfg.CodexAppServerURL) != "" {
			return "broadcast"
		}
		return "session_process"
	default:
		return "broadcast"
	}
}

// agentRequiresPolling 根据 agent ID 判断是否需要轮询外部 turn。
func agentRequiresPolling(id string, codexBackendMode string, cfg *AgentDetectionConfig) bool {
	if id == "claude" || id == "opencode" || id == "grokbuild" {
		return true
	}
	if id == "codex" && codexBackendMode == "app_server" {
		return cfg == nil || strings.TrimSpace(cfg.CodexAppServerURL) == ""
	}
	return false
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
		Capabilities:                    deriveBackendCapabilities(id, agent, codexBackendMode),
		LiveEvents:                      agentLiveEvents(id, codexBackendMode, cfg),
		RequiresPollingForExternalTurns: agentRequiresPolling(id, codexBackendMode, cfg),
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
	CodexAppServerURL string // Optional shared Codex app-server WebSocket URL.
}

// detectAgentStatus 检测单个 agent 的可用性状态。
// 所有检测设置超时，避免阻塞 go-bridge 启动。
func detectAgentStatus(id string, codexBackendMode string, cfg *AgentDetectionConfig) (AgentStatus, string) {
	switch id {
	case "claude":
		return detectClaudeCLI()
	case "opencode":
		// URL 未配置 → not_configured，绝不隐式 dial 64667（plan T05）。
		// MacBridge 在 endpoint disabled / external_http 未填 URL 时不传 -opencode-url，
		// 此处 cfg.OpenCodeURL 为空。
		if cfg == nil || strings.TrimSpace(cfg.OpenCodeURL) == "" {
			return AgentStatusNotConfigured, "OpenCode endpoint not configured; set an external HTTP server URL (e.g. http://127.0.0.1:<port>)"
		}
		ocUser := ""
		ocPass := ""
		if cfg != nil {
			ocUser = cfg.OpenCodeUser
			ocPass = cfg.OpenCodePass
		}
		return detectOpenCodeService(cfg.OpenCodeURL, ocUser, ocPass)
	case "codex":
		codexURL := ""
		if cfg != nil && cfg.CodexAppServerURL != "" {
			codexURL = cfg.CodexAppServerURL
		}
		return detectCodexService(codexBackendMode, codexURL)
	case "grokbuild":
		return detectGrokCLI()
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

// detectGrokCLI 检测 Grok Build CLI 可用性。
// 使用 exec.LookPath 查找 grok 命令，找到后执行 --version 验证（3秒超时）。
func detectGrokCLI() (AgentStatus, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	path, err := exec.LookPath("grok")
	if err != nil {
		return AgentStatusNotDetected, "grok CLI not found in PATH"
	}

	cmd := exec.CommandContext(ctx, path, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return AgentStatusNotDetected, "grok --version timed out"
		}
		return AgentStatusNotDetected, fmt.Sprintf("grok --version failed: %s", strings.TrimSpace(string(output)))
	}

	return AgentStatusAvailable, ""
}

// detectOpenCodeService 检测 OpenCode HTTP 服务可用性。
// healthURL 示例：http://127.0.0.1:4096/global/health
func detectOpenCodeService(baseURL, username, password string) (AgentStatus, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	healthURL := strings.TrimRight(baseURL, "/") + "/global/health"
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		return AgentStatusNotDetected, "invalid OpenCode health URL"
	}
	// 带认证凭据（OpenCode /global/health 端点需要 auth）
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
		var health struct {
			Healthy bool   `json:"healthy"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&health); err != nil || !health.Healthy || health.Version == "" {
			return AgentStatusNotDetected, "OpenCode health response is not valid"
		}
		return AgentStatusAvailable, ""
	}
	return AgentStatusServiceNotRunning, fmt.Sprintf("OpenCode health check returned %d", resp.StatusCode)
}

// detectCodexService 检测 Codex 服务可用性。
// app_server 模式：WebSocket dial 5秒超时；exec 模式：exec.LookPath + --version（3秒超时）。
func detectCodexService(codexBackendMode string, appServerURL string) (AgentStatus, string) {
	if codexBackendMode == "app_server" {
		if strings.TrimSpace(appServerURL) == "" {
			return detectCodexCLI()
		}
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
