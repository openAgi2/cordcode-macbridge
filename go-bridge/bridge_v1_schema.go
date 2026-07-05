package gobridge

const (
	BridgeProtocolName           = "cordcode-bridge"
	BridgeProtocolVersion        = 1
	// BridgeProtocolSchemaRevision 标记 wire schema 修订。session pinning（pinnedAtMillis
	// 字段 + set_session_pinned / list_pinned_sessions RPC + session_pin capability）是
	// 非破坏性可选新增，不 bump major version，只 bump schemaRevision。hello 只在
	// Protocol.Version 上 gating（hello_handler.go:97），schemaRevision 纯信息字段，
	// 旧客户端不受影响。见 docs/protocol/bridge-v1.md「Session Pinning」。
	BridgeProtocolSchemaRevision = "2026-07-05"
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

type BridgeV1BridgeProfile struct {
	BridgeID       string                   `json:"bridgeId"`
	DisplayName    string                   `json:"displayName"`
	RuntimeVersion string                   `json:"runtimeVersion"`
	CurrentURLs    BridgeV1CurrentURLs      `json:"currentURLs"`
	Protocol       BridgeV1Protocol         `json:"protocol"`
	Security       *BridgeV1SecurityProfile `json:"security,omitempty"`
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
	Type        string      `json:"type"`
	Seq         int         `json:"seq"`
	BackendID   string      `json:"backendId,omitempty"`
	WorkspaceID string      `json:"workspaceId,omitempty"`
	SessionID   string      `json:"sessionId,omitempty"`
	Event       string      `json:"event"`
	Data        interface{} `json:"data,omitempty"`
}
