package gobridge

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// web_push_subscription.go — register/unregister RPC DTO 与校验（canonical §6.2/§6.4）。
//
// NOTE(WP-SUB-1): 字段形状按 canonical 契约实现；最终定稿需与真机 PushSubscription
// 脱敏样本（WP-SUB-1/WP-SUB-LOCAL-1）复核。样本未归档前不得宣称外部形状已验证。

// Web Push RPC 参数字节上限（§6.4：endpoint、key 和整个 params 设置明确 byte limit）。
const (
	WebPushMaxEndpointBytes  = 2048
	WebPushMaxKeyChars       = 512
	WebPushMaxPlatformBytes  = 64
	WebPushMaxParamsBytes    = 8 << 10
	webPushSubscriptionIDLen = 16 // hex chars after "wps_"
)

// WebPushSubscriptionKeys 是 RFC 8291 客户端密钥材料（base64url）。
type WebPushSubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// WebPushSubscriptionWire 是 params.subscription 的 wire 形状。
type WebPushSubscriptionWire struct {
	Endpoint       string                  `json:"endpoint"`
	ExpirationTime *int64                  `json:"expirationTime"`
	Keys           WebPushSubscriptionKeys `json:"keys"`
}

// RegisterPushSubscriptionParams 是 register_push_subscription 的 params。
type RegisterPushSubscriptionParams struct {
	SchemaVersion        int                     `json:"schemaVersion"`
	Platform             string                  `json:"platform"`
	ApplicationServerKey string                  `json:"applicationServerKey"`
	Subscription         WebPushSubscriptionWire `json:"subscription"`
}

// UnregisterPushSubscriptionParams 是 unregister_push_subscription 的 params。
type UnregisterPushSubscriptionParams struct {
	SchemaVersion  int    `json:"schemaVersion"`
	SubscriptionID string `json:"subscriptionId"`
}

// RegisterPushSubscriptionResult 是 register 成功 result.data。
type RegisterPushSubscriptionResult struct {
	SubscriptionID     string `json:"subscriptionId"`
	RegisteredAtMillis int64  `json:"registeredAtMillis"`
}

// UnregisterPushSubscriptionResult 是 unregister 成功 result.data。
type UnregisterPushSubscriptionResult struct {
	Removed bool `json:"removed"`
}

// PushSubscriptionRecord 是 subscription store 的持久化行（0600 文件内）。
// 私钥不入此记录——这里只有发送所需的客户端公钥材料。
type PushSubscriptionRecord struct {
	SubscriptionID string `json:"subscriptionId"`
	DeviceID       string `json:"deviceId"`
	Platform       string `json:"platform,omitempty"`
	Endpoint       string `json:"endpoint"`
	P256dh         string `json:"p256dh"`
	Auth           string `json:"auth"`
	CreatedAt      int64  `json:"createdAtMillis"`
	UpdatedAt      int64  `json:"updatedAtMillis"`
}

// webPushValidationError 携带稳定错误码（canonical 错误码表）。
type webPushValidationError struct {
	code      string
	message   string
	retryable bool
}

func (e *webPushValidationError) Error() string { return e.message }

func webPushInvalid(message string) *webPushValidationError {
	return &webPushValidationError{code: WebPushErrInvalidSubscription, message: message, retryable: false}
}

// DecodeBase64URL 严格解码 base64url（无 padding）。
func DecodeBase64URL(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("empty base64url")
	}
	if strings.ContainsAny(value, "+/=") {
		return nil, fmt.Errorf("not base64url alphabet")
	}
	return base64.RawURLEncoding.DecodeString(value)
}

// IsUncompressedP256 判断 65-byte uncompressed P-256 point（0x04 前缀）。
func IsUncompressedP256(raw []byte) bool {
	return len(raw) == 65 && raw[0] == 0x04
}

// BuildWebPushSubscriptionID 由 device + endpoint 生成稳定 wps_<sha256-prefix>。
// 幂等：同一设备同一 endpoint 的重注册得到同一 id（upsert 语义的一部分）。
func BuildWebPushSubscriptionID(deviceID, endpoint string) string {
	sum := sha256.Sum256([]byte(deviceID + "\x00" + endpoint))
	return "wps_" + hex.EncodeToString(sum[:])[:webPushSubscriptionIDLen]
}

// ValidateRegisterPushSubscriptionParams 校验 register params 并比对 VAPID key。
// 返回归一化后的持久化记录（不含 SubscriptionID 生成时间戳）。
func ValidateRegisterPushSubscriptionParams(
	params *RegisterPushSubscriptionParams,
	paramsByteLen int,
	localVapidPublicKey string,
	nowMillis int64,
) (*PushSubscriptionRecord, *webPushValidationError) {
	if paramsByteLen <= 0 || paramsByteLen > WebPushMaxParamsBytes {
		return nil, webPushInvalid(fmt.Sprintf("params size %d exceeds limit %d", paramsByteLen, WebPushMaxParamsBytes))
	}
	if params.SchemaVersion != WebPushSchemaVersion {
		return nil, webPushInvalid(fmt.Sprintf("schemaVersion must be %d", WebPushSchemaVersion))
	}
	platform := strings.TrimSpace(params.Platform)
	if platform == "" || len(platform) > WebPushMaxPlatformBytes {
		return nil, webPushInvalid("platform must be 1-64 bytes")
	}
	// applicationServerKey 必须与本机 key 逐字节一致（§6.2）。
	localKeyBytes, localErr := DecodeBase64URL(localVapidPublicKey)
	if localErr != nil || !IsUncompressedP256(localKeyBytes) {
		return nil, &webPushValidationError{code: WebPushErrUnsupported, message: "local VAPID key unavailable", retryable: false}
	}
	claimedKeyBytes, keyErr := DecodeBase64URL(params.ApplicationServerKey)
	if keyErr != nil {
		return nil, webPushInvalid("applicationServerKey is not base64url")
	}
	if string(claimedKeyBytes) != string(localKeyBytes) {
		return nil, &webPushValidationError{code: WebPushErrVapidKeyMismatch, message: "applicationServerKey differs from this bridge's key", retryable: false}
	}

	endpoint := strings.TrimSpace(params.Subscription.Endpoint)
	if len(endpoint) == 0 || len(endpoint) > WebPushMaxEndpointBytes {
		return nil, webPushInvalid(fmt.Sprintf("endpoint length must be 1-%d", WebPushMaxEndpointBytes))
	}
	parsed, parseErr := url.Parse(endpoint)
	if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, webPushInvalid("endpoint must be an https URL with a host")
	}
	for _, key := range []struct{ name, value string }{
		{"p256dh", params.Subscription.Keys.P256dh},
		{"auth", params.Subscription.Keys.Auth},
	} {
		if key.value == "" || len(key.value) > WebPushMaxKeyChars {
			return nil, webPushInvalid(fmt.Sprintf("keys.%s length must be 1-%d", key.name, WebPushMaxKeyChars))
		}
		if _, err := DecodeBase64URL(key.value); err != nil {
			return nil, webPushInvalid(fmt.Sprintf("keys.%s is not base64url", key.name))
		}
	}
	return &PushSubscriptionRecord{
		DeviceID:  "", // 调用方以 authenticated connection 填充，绝不信任 params
		Platform:  platform,
		Endpoint:  endpoint,
		P256dh:    params.Subscription.Keys.P256dh,
		Auth:      params.Subscription.Keys.Auth,
		CreatedAt: nowMillis,
		UpdatedAt: nowMillis,
	}, nil
}

// ValidateUnregisterPushSubscriptionParams 校验 unregister params。
func ValidateUnregisterPushSubscriptionParams(params *UnregisterPushSubscriptionParams, paramsByteLen int) (string, *webPushValidationError) {
	if paramsByteLen <= 0 || paramsByteLen > WebPushMaxParamsBytes {
		return "", webPushInvalid(fmt.Sprintf("params size %d exceeds limit %d", paramsByteLen, WebPushMaxParamsBytes))
	}
	if params.SchemaVersion != WebPushSchemaVersion {
		return "", webPushInvalid(fmt.Sprintf("schemaVersion must be %d", WebPushSchemaVersion))
	}
	id := strings.TrimSpace(params.SubscriptionID)
	if !strings.HasPrefix(id, "wps_") || len(id) != len("wps_")+webPushSubscriptionIDLen {
		return "", webPushInvalid("subscriptionId must be wps_<16hex>")
	}
	for _, c := range id[len("wps_"):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", webPushInvalid("subscriptionId must be wps_<16hex>")
		}
	}
	return id, nil
}

// WebPushEndpointHostCategory 供日志使用的 endpoint host 分类（不记录 path/query）。
func WebPushEndpointHostCategory(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "unparseable"
	}
	host := parsed.Hostname()
	if strings.Contains(host, "push.apple.com") {
		return "apple"
	}
	if strings.Contains(host, "fcm.googleapis.com") || strings.HasSuffix(host, ".push.apple.com") {
		return "google"
	}
	if strings.Contains(host, "mozaws.net") {
		return "mozilla"
	}
	return "other"
}
