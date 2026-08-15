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
	"github.com/openAgi2/cordcode-macbridge/agent/dsh"
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

// resolveStaticDescriptor returns the driver's WireDescriptor if it self-describes,
// else nil. A driver returning a nil descriptor is treated as "not provided" so a
// driver may opt out per-build.
func resolveStaticDescriptor(agent core.Agent) *core.WireDescriptor {
	if wd, ok := agent.(core.WireDescriptorProvider); ok {
		return wd.WireDescriptor()
	}
	return nil
}

// resolveDescriptorNames returns (Kind, DisplayName), preferring the driver's
// self-description (§6.2) and falling back to the pre-§6.2 id-keyed switches for
// drivers that have not migrated. Every registered driver
// (claudecode/codex/opencode/grokbuild) self-describes, so agentKind/agentDisplayName
// are fallback-only and their default branches are never hit by a registered driver.
func resolveDescriptorNames(id string, agent core.Agent) (string, string) {
	if wd := resolveStaticDescriptor(agent); wd != nil {
		return wd.Kind, wd.DisplayName
	}
	return agentKind(id), agentDisplayName(id, agent)
}

// resolveRequiresPolling returns whether the client should poll external turns, from
// the driver's self-description (fallback to the legacy id switch for un-migrated
// drivers).
func resolveRequiresPolling(id string, agent core.Agent) bool {
	if wd := resolveStaticDescriptor(agent); wd != nil {
		return wd.RequiresExternalTurnPolling
	}
	return legacyRequiresPolling(id)
}

// legacyRequiresPolling is the pre-§6.2 fallback for drivers that do not self-describe.
// codex is excluded because its transcript relay emits authoritative turn boundaries.
func legacyRequiresPolling(id string) bool {
	return id == "claude" || id == "opencode" || id == "grokbuild"
}

// legacyLiveEventBase is the pre-§6.2 fallback static base for drivers that do not
// self-describe, mirroring the original agentLiveEvents switch minus the codex override.
func legacyLiveEventBase(id string) core.LiveEventModel {
	switch id {
	case "claude", "grokbuild", "codex":
		return core.LiveEventSessionProcess
	default:
		return core.LiveEventBroadcast
	}
}

// resolveLiveEvents returns the live-event model. The static base comes from the
// driver's WireDescriptor (fallback to legacyLiveEventBase for un-migrated drivers).
// The codex app_server runtime override (session_process → broadcast when a shared
// app-server URL is configured) is applied on top of the static base — it is
// mode-conditional (§6.2 B-class) and therefore stays in wire, not in the driver's
// self-description.
func resolveLiveEvents(id string, agent core.Agent, codexBackendMode string, cfg *AgentDetectionConfig) string {
	base := core.LiveEventBroadcast
	if wd := resolveStaticDescriptor(agent); wd != nil {
		base = wd.LiveEventModel
	} else {
		base = legacyLiveEventBase(id)
	}
	if id == "codex" && codexBackendMode == "app_server" && cfg != nil && strings.TrimSpace(cfg.CodexAppServerURL) != "" {
		return string(core.LiveEventBroadcast)
	}
	return string(base)
}

// BuildAgentDescriptor 为单个 agent 构建描述符。
// 通过 detectAgentStatus 检测实际可用性状态，替代硬编码 AgentStatusAvailable。
// cfg 为 nil 时使用默认检测地址。
func BuildAgentDescriptor(id string, agent core.Agent, codexBackendMode string, cfg *AgentDetectionConfig) AgentProviderDescriptor {
	status, reason := detectAgentStatus(id, codexBackendMode, cfg)
	kind, displayName := resolveDescriptorNames(id, agent)
	return AgentProviderDescriptor{
		ID:                              id,
		Kind:                            kind,
		DisplayName:                     displayName,
		Status:                          status,
		Reason:                          reason,
		Capabilities:                    deriveBackendCapabilities(id, agent, codexBackendMode),
		LiveEvents:                      resolveLiveEvents(id, agent, codexBackendMode, cfg),
		RequiresPollingForExternalTurns: resolveRequiresPolling(id, agent),
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
	case "deepseek":
		return detectDSHRuntime()
	default:
		return AgentStatusAvailable, ""
	}
}

// detectDSHRuntime 检测 DeepSeek Harness runtime 可用性。与 driver 共用
// agent/dsh.DiscoverRuntime（同一获取路径：PATH → wheel pkg exe → nvm →
// python wheel Resolution API），保证 hello_ack 状态与 StartSession 的
// spawn 目标一致；缺失时如实报 not_detected 并给出获取途径。
func detectDSHRuntime() (AgentStatus, string) {
	bin, source := dsh.DiscoverRuntime()
	if bin == "" && source == "" {
		return AgentStatusNotDetected, "DeepSeek Harness runtime not found (install Node.js/npm for the managed runtime, dsh-jsonrpc-agent on PATH, or pip install deepseek-harness-runtime-bin)"
	}
	return AgentStatusAvailable, source
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
