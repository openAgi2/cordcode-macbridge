package gobridge

// web_push_protocol.go — web_push_v1 wire constants and additive hello_ack/payload shapes.
//
// Canonical contract: docs/protocol/bridge-v1.md "Web Push (web_push_v1)" section. The
// samples under docs/protocol/samples/web-push/ are internal shape fixtures; external
// interoperability evidence lives behind the WP-SUB / WP-RESP / EVT-* real-sample gates
// from the implementation plan §3.2. Until those gates pass, notification producers stay
// disabled and no code path may treat these fixtures as proof of external compatibility.

const (
	// WebPushCapability 是 hello/hello_ack 中协商的 capability 字面量。
	WebPushCapability = "web_push_v1"

	// WebPushScope 是两个 web push RPC 的 scope（rpc_scopes.go 表项）。
	WebPushScope = "web_push.manage"

	// WebPushMethodRegister / WebPushMethodUnregister 是 bridge 级 RPC 方法名。
	// 二者沿用标准 request envelope（backendId 必填，服务端在 agent 路由前分发并
	// 忽略该字段的业务语义）；不存在无 backendId 的第二种 request 形状。
	WebPushMethodRegister   = "register_push_subscription"
	WebPushMethodUnregister = "unregister_push_subscription"

	// WebPushSchemaVersion 是 register/unregister params 与 SW payload 的当前 schema 版本。
	WebPushSchemaVersion = 1
)

// web_push 错误码（稳定契约；retryable 语义见 bridge-v1.md 错误码表）。
const (
	WebPushErrUnsupported         = "web_push.unsupported"
	WebPushErrInvalidSubscription = "web_push.invalid_subscription"
	WebPushErrVapidKeyMismatch    = "web_push.vapid_key_mismatch"
	WebPushErrStorageFailed       = "web_push.storage_failed"
)

// WebPushStatus 是 hello_ack.webPush.status 的 additive 诊断值。
const (
	WebPushStatusMisconfigured = "misconfigured"
)

// WebPushHelloProfile 是 hello_ack 的 additive `webPush` 字段。
// 仅当客户端声明 web_push_v1 且本机 VAPID store 健康时才下发 vapidPublicKey；
// misconfigured 时 vapidPublicKey 必须为空（不得伪造公钥）。
type WebPushHelloProfile struct {
	SchemaVersion  int    `json:"schemaVersion"`
	VapidPublicKey string `json:"vapidPublicKey,omitempty"`
	Status         string `json:"status,omitempty"`
}

// WebPushNotificationKind 是首版固定文案的通知类别。文案由 MacBridge 本地固定表
// 生成（buildWebPushNotificationText），不从 agent 文本拼接。input/error 两类在
// 真实样本（EVT-INPUT-1 / EVT-ERROR-1）归档前保持 disabled。
type WebPushNotificationKind string

const (
	WebPushKindCompletion WebPushNotificationKind = "completion"
	WebPushKindPermission WebPushNotificationKind = "permission"
	WebPushKindInput      WebPushNotificationKind = "input"
	WebPushKindError      WebPushNotificationKind = "error"
)

// WebPushPayloadV1 是发送端明文 schema（RFC 8291 加密前）。Service Worker 侧的
// 合法/非法分支都必须 showNotification——没有 silent branch（userVisibleOnly 契约）。
type WebPushPayloadV1 struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Notification  WebPushNotificationPayload `json:"notification"`
	Target        WebPushTarget              `json:"target"`
}

type WebPushNotificationPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
}

// WebPushTarget 唯一描述深链目标；anchor 只允许已验证的 turn/interaction id，
// 无样本时必须为 null。
type WebPushTarget struct {
	BridgeID  string             `json:"bridgeId"`
	BackendID string             `json:"backendId"`
	SessionID string             `json:"sessionId"`
	EventID   string             `json:"eventId"`
	Anchor    *WebPushAnchorType `json:"anchor"`
}

type WebPushAnchorType struct {
	Kind string `json:"kind"` // "turn" | "interaction"
	ID   string `json:"id"`
}

// buildWebPushNotificationText 返回固定低敏感文案。字段路径未由真实样本证明的
// kind 不能启用；此函数不读取 agent 文本。
func buildWebPushNotificationText(kind WebPushNotificationKind) (title, body string) {
	switch kind {
	case WebPushKindCompletion:
		return "任务已完成", "点击打开 CordCode 查看结果"
	case WebPushKindPermission:
		return "需要操作审批", "点击打开 CordCode 处理请求"
	case WebPushKindInput:
		return "Agent 正在等待回复", "点击打开 CordCode 回答"
	case WebPushKindError:
		return "任务异常中断", "点击打开 CordCode 查看详情"
	default:
		return "", ""
	}
}
