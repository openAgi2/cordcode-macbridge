package gobridge

const (
	BridgeProtocolName    = "cordcode-bridge"
	BridgeProtocolVersion = 1
	// BridgeProtocolSchemaRevision 标记 wire schema 修订。session pinning（pinnedAtMillis
	// 字段 + set_session_pinned / list_pinned_sessions RPC + session_pin capability）是
	// 非破坏性可选新增，不 bump major version，只 bump schemaRevision。hello 只在
	// Protocol.Version 上 gating（hello_handler.go:97），schemaRevision 纯信息字段，
	// 旧客户端不受影响。见 docs/protocol/bridge-v1.md「Session Pinning」。
	//
	// 2026-07-24: Session Projection Stream（session_sync_v2 client capability + hello_ack echo
	// + projection_patch/projection_snapshot/sync_invalidate events + get_session_projection RPC
	// + BridgeSessionProjection 系列类型）。同样是 extensible 非破坏性新增（capability 数组/map、
	// 新 event 名、新 RPC method），只 bump schemaRevision。
	//
	// 2026-07-29 Phase 4: session_sync_v2 opt-in semantics become projection-only; Mac no longer
	// live-delivers raw timeline content to opted-in connections. Legacy clients omit the
	// capability and retain the explicit off path.
	//
	// 2026-08-01: connectionPolicy (control-plane)。Relay-first + opt-in LAN 改造:Mac 在已认证信道
	// (hello_ack.bridge / RelayFirstResult / pairing_complete.bridge / /internal/remote/status)下发
	// connectionPolicy.preferLocalNetwork,默认 false(Relay 是默认底座,LAN 是用户主动开启的性能优化)。
	// 非破坏性可选新增:旧 iOS 忽略该字段;新 iOS 解码旧 payload 取 false。SSV2 红线:纯 control-plane,
	// 严禁进入 EventMessage.Data / timeline / SessionProjection / ProjectionReducer。
	//
	// 2026-08-02: Structured user input (design docs/2026-08-01-codex-claude-structured-user-input-design.md)。
	// 新增 capability `structured_user_input_v1`、RPC `resolve_user_input`、part variant `user_input`、
	// part op `upsert_user_input`、live event 名 `user_input_requested`/`user_input_resolved`。仍是
	// extensible 非破坏性新增（capability 数组/map、新 RPC method、新 event 名、新 part type/op），
	// 只 bump schemaRevision；hello 只在 Protocol.Version 上 gating，旧客户端忽略未知 event/RPC。
	// capability 在 P6 由 backend descriptor 独立广告（Codex/Claude 各自 readiness），P3 落地 Kernel
	// reducer/events/schema 后才广告。MacBridge 本常量与 iOS `CCCodeBridgeProtocol.schemaRevision`
	// 必须同值（设计 §13）。
	// 2026-08-22: permissionActions is an additive optional field on raw
	// permission_request and projected tool parts. It prevents clients from
	// presenting persistent "always" choices when the backend only supports a
	// one-shot official approval vocabulary.
	//
	// 2026-08-23: set_observation_scope result data includes per-session
	// Subscribe + observer-attach outcome. Unconditional {ok:true} is forbidden
	// when attach is required and failed. Failure code observation_attach_failed.
	BridgeProtocolSchemaRevision = "2026-08-23"
)

type BridgeV1Protocol struct {
	Name                     string   `json:"name"`
	Version                  int      `json:"version"`
	SchemaRevision           string   `json:"schemaRevision,omitempty"`
	SupportedSchemaRevisions []string `json:"supportedSchemaRevisions,omitempty"`
}

type BridgeV1Client struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	DeviceID string `json:"deviceId"`
}

type BridgeV1Hello struct {
	Type     string           `json:"type"`
	Client   BridgeV1Client   `json:"client"`
	Protocol BridgeV1Protocol `json:"protocol"`
}

type BridgeV1CurrentURLs struct {
	Local   string   `json:"local"`
	Remote  *string  `json:"remote"`
	Remotes []string `json:"remotes,omitempty"`
	// Locals 是除 Local(primary)外的其余 LAN 直连候选(ws://<lan-ip>:<port>/bridge)。
	// 与运行时 HelloURLs.Locals 描述同一 hello_ack.bridge.currentURLs;本字段防 payload 与 contract 漂移。
	// 不承载 Tailscale 候选(需独立 TLS pin);本期只通告普通 LAN ws://。
	Locals []string `json:"locals,omitempty"`
}

// ConnectionPolicy 是 control-plane 连接策略,经已认证信道(hello_ack.bridge /
// RelayFirstResult / pairing_complete.bridge / /internal/remote/status)下发给客户端。
//
// 产品心智:Relay 是稳定连接底座,局域网是用户主动开启的性能优化。preferLocalNetwork
// 默认 false —— 只有 Mac owner 显式开启后,iOS 才在 Wi-Fi/混合网络下优先尝试普通 LAN 直连,
// 失败后自动回退 Relay。候选发布(currentURLs.locals / RelayFirstResult.localUrls)与该偏好独立:
// 关闭偏好时 Mac 仍完整发布 LAN 候选,只是 iOS 不把它们纳入自动优先路径。
//
// SSV2 红线:纯 control-plane 配置。权威运行时状态只存在 ManagementConfig/Server,序列化副本
// 只允许出现在上述四个已认证 payload。严禁写入任何 EventMessage.Data、严禁经
// PublishLogical/IngestLive 进入 timeline、不得在 SessionProjection 增加 policy 字段、
// 也不得在 ProjectionReducer.Apply 的 switch 中新增匹配 policy 的 case。
type ConnectionPolicy struct {
	// PreferLocalNetwork 控制安全分类为 localLanWS 的普通 LAN 是否自动优先。
	// 不影响 Tailscale TLS pin、安全 URL 分类或用户显式选择的自定义远程路径。
	PreferLocalNetwork bool `json:"preferLocalNetwork"`
}

type BridgeV1BridgeProfile struct {
	BridgeID         string                   `json:"bridgeId"`
	DisplayName      string                   `json:"displayName"`
	RuntimeVersion   string                   `json:"runtimeVersion"`
	CurrentURLs      BridgeV1CurrentURLs      `json:"currentURLs"`
	Protocol         BridgeV1Protocol         `json:"protocol"`
	Security         *BridgeV1SecurityProfile `json:"security,omitempty"`
	ConnectionPolicy *ConnectionPolicy        `json:"connectionPolicy,omitempty"`
}

type BridgeV1SecurityProfile struct {
	Level            string          `json:"level"`
	Scheme           string          `json:"scheme,omitempty"`
	HostCategory     string          `json:"hostCategory,omitempty"`
	IsTailscaleCGNAT bool            `json:"isTailscaleCGNAT,omitempty"`
	IsPublicWS       bool            `json:"isPublicWS,omitempty"`
	TLSPin           *BridgeV1TLSPin `json:"tlsPin,omitempty"`
}

// BridgeV1TLSPin 是已认证的 Bridge TLS pin 契约，对应 iOS 端 BridgeTLSPin。
//
// 由 MacBridge 在已认证信道（pairing_complete / hello_ack）下发给 iOS，
// iOS 据此对 Tailscale wss:// 自签名证书做 SPKI pinning（relay 路径不经此 pin）。
// 字段语义见 docs/2026-06-19-t00-tlspin-owner-unblock-spec.md §2。
type BridgeV1TLSPin struct {
	Algorithm                string `json:"algorithm"`                    // 固定 "sha256-spki"
	Value                    string `json:"value"`                        // base64(SHA256(SPKI))
	Generation               uint64 `json:"generation"`                   // 单调递增；回退 iOS 拒绝
	PreviousValue            string `json:"previousValue,omitempty"`      // 轮换窗口内的旧 pin
	PreviousValidUntilMillis int64  `json:"previousValidUntil,omitempty"` // Unix epoch ms；窗口结束后 iOS 拒绝 previous
}

type BridgeV1Capabilities struct {
	RemoteAccessConfig bool `json:"remoteAccessConfig"`
	TrustedDevices     bool `json:"trustedDevices"`
	OfflineSnapshots   bool `json:"offlineSnapshots"`
	WorkspaceList      bool `json:"workspaceList"`
	SessionMutation    bool `json:"sessionMutation"`
}

type BridgeV1RunningSession struct {
	BackendID   string `json:"backendId"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	SessionID   string `json:"sessionId"`
	Status      string `json:"status"`
}

type BridgeV1HelloAck struct {
	Type            string                   `json:"type"`
	OK              bool                     `json:"ok"`
	Bridge          *BridgeV1BridgeProfile   `json:"bridge,omitempty"`
	Capabilities    *BridgeV1Capabilities    `json:"capabilities,omitempty"`
	Backends        []BackendInfo            `json:"backends,omitempty"`
	BridgeStatus    string                   `json:"bridgeStatus,omitempty"`
	RunningSessions []BridgeV1RunningSession `json:"runningSessions,omitempty"`
	Error           *WireError               `json:"error,omitempty"`
}

type BridgeV1PairingClaimParams struct {
	PairingID  string                `json:"pairingId,omitempty"`
	ManualCode string                `json:"manualCode,omitempty"`
	Device     BridgeV1PairingDevice `json:"device"`
}

type BridgeV1PairingDevice struct {
	DeviceID    string `json:"deviceId"`
	DisplayName string `json:"displayName"`
	Platform    string `json:"platform"`
}

type BridgeV1PairingResult struct {
	Type   string                    `json:"type"`
	OK     bool                      `json:"ok"`
	Device *BridgeV1AuthorizedDevice `json:"device,omitempty"`
	Bridge *BridgeV1PairedBridge     `json:"bridge,omitempty"`
	Error  *WireError                `json:"error,omitempty"`
}

type BridgeV1AuthorizedDevice struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
}

type BridgeV1PairedBridge struct {
	BridgeID    string   `json:"bridgeId"`
	DisplayName string   `json:"displayName"`
	LocalURL    string   `json:"localURL"`
	RemoteURL   *string  `json:"remoteURL"`
	RemoteURLs  []string `json:"remoteURLs,omitempty"`
}

type BridgeV1EventEnvelope struct {
	Type          string      `json:"type"`
	Seq           int         `json:"seq"`
	PerSessionSeq int         `json:"perSessionSeq,omitempty"`
	BackendID     string      `json:"backendId,omitempty"`
	WorkspaceID   string      `json:"workspaceId,omitempty"`
	SessionID     string      `json:"sessionId,omitempty"`
	Event         string      `json:"event"`
	Data          interface{} `json:"data,omitempty"`
}
