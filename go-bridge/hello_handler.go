package gobridge

import (
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// HelloMessage 是客户端发送的握手消息。
type HelloMessage struct {
	Type              string              `json:"type"`
	Client            HelloClient         `json:"client"`
	Protocol          HelloProtocol       `json:"protocol"`
	Capabilities      []string            `json:"capabilities,omitempty"`
	LastBridgeEpoch   string              `json:"lastBridgeEpoch,omitempty"`
	LastEventID       string              `json:"lastEventId,omitempty"`
	LastSeenBySession BridgeSessionCutMap `json:"lastSeenBySession,omitempty"`
}

// HelloClient 描述客户端应用信息。
type HelloClient struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	DeviceID string `json:"deviceId"`
}

// HelloProtocol 描述客户端声明的协议版本。
type HelloProtocol struct {
	Name                     string   `json:"name"`
	Version                  int      `json:"version"`
	SupportedSchemaRevisions []string `json:"supportedSchemaRevisions"`
}

// HelloAckMessage 是服务端对 hello 的应答，对应 schema BridgeV1HelloAck。
type HelloAckMessage struct {
	Type            string                    `json:"type"`
	Ok              bool                      `json:"ok"`
	Bridge          *HelloBridgeInfo          `json:"bridge,omitempty"`
	Capabilities    map[string]bool           `json:"capabilities,omitempty"`
	Backends        []AgentProviderDescriptor `json:"backends,omitempty"`
	BridgeStatus    string                    `json:"bridgeStatus,omitempty"`
	RunningSessions []BridgeV1RunningSession  `json:"runningSessions,omitempty"`
	Error           *WireError                `json:"error,omitempty"`
	BridgeEpoch     string                    `json:"bridgeEpoch,omitempty"`
	Recovery        *BridgeRecoveryPlan       `json:"recovery,omitempty"`
}

type BridgeAffectedSession struct {
	BackendID string `json:"backendId"`
	SessionID string `json:"sessionId"`
}

type BridgeRecoveryPlan struct {
	RecoveryID             string                  `json:"recoveryId"`
	Mode                   string                  `json:"mode"`
	ReplayThroughBySession BridgeSessionCutMap     `json:"replayThroughBySession,omitempty"`
	AffectedSessions       []BridgeAffectedSession `json:"affectedSessions,omitempty"`
	CutBySession           BridgeSessionCutMap     `json:"cutBySession,omitempty"`
}

// HelloBridgeInfo 包含 bridge 的身份和连接信息，对应 schema BridgeV1BridgeProfile。
type HelloBridgeInfo struct {
	BridgeID       string                   `json:"bridgeId"`
	DisplayName    string                   `json:"displayName"`
	RuntimeVersion string                   `json:"runtimeVersion"`
	CurrentURLs    HelloURLs                `json:"currentURLs"`
	Protocol       HelloAckProtocol         `json:"protocol"`
	Security       *BridgeV1SecurityProfile `json:"security,omitempty"`
}

// HelloAckProtocol 是 hello_ack 中 bridge 信息携带的协议版本。
type HelloAckProtocol struct {
	Name           string `json:"name"`
	Version        int    `json:"version"`
	SchemaRevision string `json:"schemaRevision,omitempty"`
}

// HelloURLs 包含 bridge 的本地和远程 URL。
type HelloURLs struct {
	Local   string   `json:"local"`
	Remote  string   `json:"remote"`
	Remotes []string `json:"remotes,omitempty"`
	// Locals 是除 Local(primary)外的其余 LAN 直连候选(ws://<lan-ip>:<port>/bridge)。
	// 与 schema BridgeV1CurrentURLs.Locals 描述同一 hello_ack.bridge.currentURLs 字段。
	// 不承载 Tailscale 候选(需独立 TLS pin);本期只通告普通 LAN ws://。
	Locals []string `json:"locals,omitempty"`
}

// HandleHello 处理 hello 握手，构建 hello_ack 响应。
// 如果协议版本不匹配，返回 ok=false 和 protocol.unsupported_version 错误。
// 否则返回 bridge 信息、能力、agent 描述符列表、bridge 状态和运行中的 session。
func HandleHello(
	hello *HelloMessage,
	device *TrustedDeviceRecord,
	bridgeID, displayName, runtimeVersion, localURL, remoteURL string,
	agents map[string]core.Agent,
	codexBackendMode string,
	detectionCfg *AgentDetectionConfig,
	sessions *sessionRegistry,
) *HelloAckMessage {
	return HandleHelloWithRemoteURLs(hello, device, bridgeID, displayName, runtimeVersion, localURL, remoteURL, nil, nil, agents, codexBackendMode, detectionCfg, sessions)
}

func HandleHelloWithRemoteURLs(
	hello *HelloMessage,
	device *TrustedDeviceRecord,
	bridgeID, displayName, runtimeVersion, localURL, remoteURL string,
	remoteURLs []string,
	localURLs []string,
	agents map[string]core.Agent,
	codexBackendMode string,
	detectionCfg *AgentDetectionConfig,
	sessions *sessionRegistry,
) *HelloAckMessage {
	// 协议版本校验
	if hello.Protocol.Version != BridgeProtocolVersion {
		return &HelloAckMessage{
			Type: "hello_ack",
			Ok:   false,
			Error: &WireError{
				Code:    "protocol.unsupported_version",
				Message: "不支持的协议版本",
			},
		}
	}

	// 构建 bridge 信息（含 protocol 字段）
	bridgeInfo := &HelloBridgeInfo{
		BridgeID:       bridgeID,
		DisplayName:    displayName,
		RuntimeVersion: runtimeVersion,
		CurrentURLs: HelloURLs{
			Local:   localURL,
			Remote:  remoteURL,
			Remotes: uniqueNonEmptyStrings(append([]string{remoteURL}, remoteURLs...)),
			Locals:  filterOutString(localURLs, localURL),
		},
		Protocol: HelloAckProtocol{
			Name:           BridgeProtocolName,
			Version:        BridgeProtocolVersion,
			SchemaRevision: BridgeProtocolSchemaRevision,
		},
		Security: classifyLocalURLSecurity(localURL),
	}

	// 固定能力集
	capabilities := map[string]bool{
		"remoteAccessConfig": false,
		"trustedDevices":     true,
		"offlineSnapshots":   false,
		"workspaceList":      true,
		"sessionMutation":    true,
	}

	// 构建 agent 描述符
	backends := BuildAllAgentDescriptors(agents, codexBackendMode, detectionCfg)

	// 收集运行中的 session
	runningSessions := buildRunningSessions(sessions)

	return &HelloAckMessage{
		Type:            "hello_ack",
		Ok:              true,
		Bridge:          bridgeInfo,
		Capabilities:    capabilities,
		Backends:        backends,
		BridgeStatus:    "running",
		RunningSessions: runningSessions,
	}
}

// buildRunningSessions 从 session registry 提取当前运行中的 session 列表。
// 只包含 state == running 的 session，跳过 idle/closing。
// rebind 后 registry 保留 pending ID 映射，需按内部 session 指针去重。
// 去重后始终使用 trackedSession.sessionID（rebind 后的真实 ID），不走 map key（可能是 pending ID）。
func buildRunningSessions(registry *sessionRegistry) []BridgeV1RunningSession {
	if registry == nil {
		return []BridgeV1RunningSession{}
	}
	var result []BridgeV1RunningSession
	seen := make(map[*trackedSession]struct{})
	registry.forEach(func(_ string, t *trackedSession) {
		if t.state != sessionStateRunning {
			return
		}
		if _, dup := seen[t]; dup {
			return
		}
		seen[t] = struct{}{}
		result = append(result, BridgeV1RunningSession{
			BackendID:   t.backendID,
			WorkspaceID: t.directory,
			SessionID:   t.sessionID,
			Status:      "running",
		})
	})
	if len(result) == 0 {
		return []BridgeV1RunningSession{}
	}
	return result
}

// classifyLocalURLSecurity 分析 localURL 的安全等级，生成 hello_ack security hints。
// 复用 management_api.go 中 classifyRemoteURL 的分类逻辑。
func classifyLocalURLSecurity(localURL string) *BridgeV1SecurityProfile {
	analysis := classifyRemoteURL(localURL)
	return &BridgeV1SecurityProfile{
		Level:            analysis.SecurityLevel,
		Scheme:           analysis.Scheme,
		HostCategory:     analysis.HostCategory,
		IsTailscaleCGNAT: analysis.IsTailscaleCGNAT,
		IsPublicWS:       analysis.IsPublicWS,
	}
}

// filterOutString 返回 ss 中不等于 exclude 的非空、去重项。
// 用于从 LAN 候选集合剔除 primary(= localURL),使 HelloURLs.Locals 仅承载 secondary 候选,
// 与保留为单数 primary 的 Local 字段语义一致。
func filterOutString(ss []string, exclude string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" || s == exclude {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
