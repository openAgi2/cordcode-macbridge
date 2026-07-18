package gobridge

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// RelayDeviceConn 将一个已认证的安全 relay device channel 适配为 Connection 接口。
//
// 方案 §10.1：
//
//	"现有连接改为 DirectConn 实现接口；增加 relay 认证后注册 RelayConn 的入口，
//	 不在 direct conn 内堆积分支。"
//
// RelayDeviceConn 不直接持有 WebSocket；它持有 relay client 的发送函数，
// 对外表现为 Connection，对内将业务消息加密后通过 relay client 发出。
type RelayDeviceConn struct {
	mu sync.Mutex

	deviceID   string
	bridgeID   string
	routeID    string
	generation uint64

	// 已认证的设备记录
	device *TrustedDeviceRecord

	// 加密状态：每一方向独立的 traffic key 和 counter。
	// 方案 §5.5：每一方向从 counter = 1 开始，严格递增。
	macToIosKey []byte
	sendCounter atomic.Uint64

	// iOS→Mac 方向的解密密钥和接收 counter。
	iosToMacKey []byte
	recvCounter atomic.Uint64

	// 发送函数：将加密信封通过 relay client 发出。
	// 由 relay client 注入；签名 func(envelope *RelayEnvelope) error
	sendEnvelope func(envelope json.RawMessage) error

	// 状态
	closed       bool
	outboundGzip bool

	// lastActivity 记录最后一次从该 device 收到有效数据的时间（unix nano）。
	// 由 handleInboundEnvelope 在解密成功后更新；心跳循环据此做半开检测：
	// 长期无 device→Mac 数据即判定连接死，主动清理（而非被动僵死，也非靠重试掩盖）。
	lastActivity atomic.Int64
}

var _ Connection = (*RelayDeviceConn)(nil)

const (
	relayGzipCapability = "relay_gzip_v1"
	relayGzipThreshold  = 32 * 1024
)

// NewRelayDeviceConn 创建一个已认证的 relay device connection。
// macToIosKey 和 iosToMacKey 分别是 Mac→iOS 和 iOS→Mac 方向的 traffic key。
func NewRelayDeviceConn(
	deviceID, bridgeID, routeID string,
	generation uint64,
	device *TrustedDeviceRecord,
	macToIosKey []byte,
	iosToMacKey []byte,
	sendEnvelope func(json.RawMessage) error,
) *RelayDeviceConn {
	rc := &RelayDeviceConn{
		deviceID:     deviceID,
		bridgeID:     bridgeID,
		routeID:      routeID,
		generation:   generation,
		device:       device,
		macToIosKey:  macToIosKey,
		iosToMacKey:  iosToMacKey,
		sendEnvelope: sendEnvelope,
	}
	// 双方向 counter 从 1 开始
	rc.sendCounter.Store(1)
	rc.recvCounter.Store(1)
	rc.lastActivity.Store(time.Now().UnixNano())
	return rc
}

// touchLastActivity 记录最后一次从该 device 收到有效数据的时间。
// 在 handleInboundEnvelope 解密成功后调用，是半开检测的输入。
func (rc *RelayDeviceConn) touchLastActivity() {
	rc.lastActivity.Store(time.Now().UnixNano())
}

// lastActivityAt 返回最后一次收到该 device 数据的时间。
func (rc *RelayDeviceConn) lastActivityAt() time.Time {
	return time.Unix(0, rc.lastActivity.Load())
}

// enableOutboundGzip is called only after the authenticated Bridge hello declares
// relay_gzip_v1. Legacy iOS clients never declare it and retain the original wire format.
func (rc *RelayDeviceConn) enableOutboundGzip() {
	rc.mu.Lock()
	rc.outboundGzip = true
	rc.mu.Unlock()
}

func negotiateRelayGzip(conn Connection, capabilities []string) bool {
	rc, ok := conn.(*RelayDeviceConn)
	if !ok {
		return false
	}
	for _, capability := range capabilities {
		if capability == relayGzipCapability {
			rc.enableOutboundGzip()
			return true
		}
	}
	return false
}

// SendJSON 将业务消息加密为 relay envelope 并发送。
func (rc *RelayDeviceConn) SendJSON(v any) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if rc.closed || rc.sendEnvelope == nil {
		return
	}

	// 序列化 inner payload
	plaintext, err := json.Marshal(v)
	if err != nil {
		slog.Error("relay-conn: marshal inner payload", "device", rc.deviceID, "error", err)
		return
	}
	contentEncoding := ""
	if rc.outboundGzip && len(plaintext) >= relayGzipThreshold {
		compressed, compressErr := gzipPayload(plaintext)
		if compressErr != nil {
			slog.Error("relay-conn: gzip inner payload", "device", rc.deviceID, "error", compressErr)
			return
		}
		if len(compressed) < len(plaintext) {
			contentEncoding = "gzip"
			slog.Info("relay-conn: compressed outbound payload",
				"device", rc.deviceID,
				"uncompressed_bytes", len(plaintext),
				"compressed_bytes", len(compressed))
			plaintext = compressed
		}
	}

	// 获取并递增 counter
	counter := rc.sendCounter.Add(1) - 1 // Add 返回增加后的值，减 1 得到本次使用的值

	now := time.Now().UTC()
	envelope := &RelayEnvelope{
		Version:           1,
		RouteID:           rc.routeID,
		SenderID:          "bridge",
		DestinationID:     rc.deviceID,
		ChannelGeneration: rc.generation,
		KeyEpochID:        "online:" + strconv.FormatUint(rc.generation, 10),
		MessageID:         generateRelayID("msg_"),
		Counter:           counter,
		ContentEncoding:   contentEncoding,
		CreatedAt:         now.Format(time.RFC3339),
		ExpiresAt:         now.Add(24 * time.Hour).Format(time.RFC3339),
	}
	aad, err := envelope.EncodeAAD()
	if err != nil {
		slog.Error("relay-conn: encode envelope aad", "device", rc.deviceID, "counter", counter, "error", err)
		return
	}
	ciphertext, err := SealEnvelope(rc.macToIosKey, counter, aad, plaintext)
	if err != nil {
		slog.Error("relay-conn: seal envelope", "device", rc.deviceID, "counter", counter, "error", err)
		return
	}
	envelope.Ciphertext = ciphertext

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("relay-conn: marshal envelope", "device", rc.deviceID, "error", err)
		return
	}

	if err := rc.sendEnvelope(envelopeJSON); err != nil {
		slog.Error("relay-conn: send envelope", "device", rc.deviceID, "error", err)
	}
}

func gzipPayload(payload []byte) ([]byte, error) {
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

// SendResult 发送带 requestId 的 result 回复。
func (rc *RelayDeviceConn) SendResult(requestID string, data interface{}, err *WireError) {
	resp := map[string]interface{}{
		"type":      "result",
		"requestId": requestID,
	}
	if err != nil {
		resp["ok"] = false
		resp["error"] = err
	} else {
		resp["ok"] = true
		resp["data"] = data
	}
	rc.SendJSON(resp)
}

// ReceiveJSON 解密入站 relay envelope 并返回业务 JSON。
// 如果 iosToMacKey 未设置（单向通信模式），返回 nil。
func (rc *RelayDeviceConn) ReceiveJSON(envelopeBytes []byte) (json.RawMessage, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if rc.closed {
		return nil, fmt.Errorf("connection closed")
	}
	if len(rc.iosToMacKey) == 0 {
		return nil, fmt.Errorf("inbound key not configured")
	}

	var env RelayEnvelope
	if err := json.Unmarshal(envelopeBytes, &env); err != nil {
		return nil, fmt.Errorf("parse envelope: %w", err)
	}

	if env.DestinationID != "bridge" && env.DestinationID != rc.bridgeID {
		return nil, fmt.Errorf("envelope not for this bridge: dst=%s", env.DestinationID)
	}
	if env.RouteID != rc.routeID ||
		env.SenderID != rc.deviceID ||
		env.ChannelGeneration != rc.generation ||
		env.KeyEpochID != "online:"+strconv.FormatUint(rc.generation, 10) {
		return nil, fmt.Errorf("envelope channel mismatch")
	}

	// 验证 counter 严格递增
	expected := rc.recvCounter.Load()
	if env.Counter != expected {
		return nil, fmt.Errorf("counter gap: expected=%d got=%d", expected, env.Counter)
	}

	aad, err := env.EncodeAAD()
	if err != nil {
		return nil, fmt.Errorf("encode AAD: %w", err)
	}

	plaintext, err := OpenEnvelope(rc.iosToMacKey, env.Counter, aad, env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	rc.recvCounter.Add(1)
	return json.RawMessage(plaintext), nil
}

// AuthedDevice 返回已认证的设备记录。
func (rc *RelayDeviceConn) AuthedDevice() *TrustedDeviceRecord {
	return rc.device
}

// RemoteAddr 返回远端地址描述。
func (rc *RelayDeviceConn) RemoteAddr() string {
	return "relay:" + rc.deviceID
}

// Close 关闭 relay connection，擦除密钥材料。
func (rc *RelayDeviceConn) Close() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.closed = true
	zeroBytes(rc.macToIosKey)
	zeroBytes(rc.iosToMacKey)
	rc.sendEnvelope = nil
	return nil
}

// DeviceID 返回设备 ID。
func (rc *RelayDeviceConn) DeviceID() string {
	return rc.deviceID
}

// BridgeID 返回 bridge ID。
func (rc *RelayDeviceConn) BridgeID() string {
	return rc.bridgeID
}
