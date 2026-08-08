package gobridge

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
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
	writer       *relayOutboundWriter

	// requestClasses is populated when an inbound Relay RPC is admitted and
	// consumed exactly once by SendResult, keeping handler call sites unchanged.
	requestClasses         map[string]relayOutboundClass
	// requestMethods 存 inbound 请求的 method（R1.5），用于 SendResult 判断是否为 cancelable allowlist
	// （read_file_v2）以安装 cancel handle。registerRequestClass 记录，SendResult 消费一次。
	requestMethods         map[string]string
	// requestBulkCorrelations 存 read_file_v2 inbound 请求预绑定的 bulkCorrelationId（R1.4），
	// 由 registerRequestClass 在 admit 时记录，SendResult chunk 路径消费一次。
	requestBulkCorrelations map[string]string
	// requestBulkHandles 存 read_file_v2 chunked result 的 OutboundBulkHandle（R1.5），
	// 由 SendResult 在创建 chunk group 时按 requestId 安装，cancel_request_v1 查找并 Cancel()，
	// group 完成 / 单帧 result / error 时清理。
	requestBulkHandles    map[string]*OutboundBulkHandle
	// bulkCorrelations 是该 device/generation 的 correlation registry（active/retired + caps）。
	bulkCorrelations      *BulkCorrelationRegistry
	inboundScheduler       *relayInboundScheduler
	sessionBulkGenerations map[string]uint64
	bulkRequestContexts    map[string]relayBulkRequestContext
	activeBulkHandles      map[string]*OutboundBulkHandle

	// 状态
	closed         bool
	outboundGzip   bool
	outboundChunks bool
	// outboundChunkProgress：client 已 ack relay_chunk_progress_v1（R1.4）。默认 false =>
	// 即便请求带 bulkCorrelationId 也不 stamp correlated chunk（base 兼容；iOS 未升级时不破坏 AAD）。
	outboundChunkProgress bool

	// lastActivity 记录最后一次从该 device 收到有效数据的时间（unix nano）。
	// 由 handleInboundEnvelope 在解密成功后更新；心跳循环据此做半开检测：
	// 长期无 device→Mac 数据即判定连接死，主动清理（而非被动僵死，也非靠重试掩盖）。
	lastActivity atomic.Int64
}

var _ Connection = (*RelayDeviceConn)(nil)

const (
	relayGzipCapability        = "relay_gzip_v1"
	relayChunksCapability      = "relay_chunks_v1"
	relayChunkProgressCapability = "relay_chunk_progress_v1" // R1.4 §3.6.4：read_file_v2 request-aware bulk correlation
	relayCancelCapability      = "cancel_request_v1"          // R1.5 §3.6.4：read_file_v2 bulk cancel control RPC
	relayGzipThreshold         = 32 * 1024
	// R1.4 correlation registry caps（A0 冻结前保守值）。
	relayBulkCorrelationMaxActive  = 32
	relayBulkCorrelationMaxRetired = 64
)

func (rc *RelayDeviceConn) channelGeneration() uint64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.generation
}
func (rc *RelayDeviceConn) isClosed() bool { rc.mu.Lock(); defer rc.mu.Unlock(); return rc.closed }

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
		deviceID:               deviceID,
		bridgeID:               bridgeID,
		routeID:                routeID,
		generation:             generation,
		device:                 device,
		macToIosKey:            macToIosKey,
		iosToMacKey:            iosToMacKey,
		sendEnvelope:           sendEnvelope,
		requestClasses:          make(map[string]relayOutboundClass),
		requestMethods:          make(map[string]string),
		requestBulkCorrelations: make(map[string]string),
		requestBulkHandles:      make(map[string]*OutboundBulkHandle),
		bulkCorrelations:        NewBulkCorrelationRegistry(relayBulkCorrelationMaxActive, relayBulkCorrelationMaxRetired),
		sessionBulkGenerations:  make(map[string]uint64),
		bulkRequestContexts:    make(map[string]relayBulkRequestContext),
		activeBulkHandles:      make(map[string]*OutboundBulkHandle),
	}
	// 双方向 counter 从 1 开始
	rc.sendCounter.Store(1)
	rc.recvCounter.Store(1)
	rc.lastActivity.Store(time.Now().UnixNano())
	return rc
}

func (rc *RelayDeviceConn) setInboundScheduler(scheduler *relayInboundScheduler) {
	rc.mu.Lock()
	rc.inboundScheduler = scheduler
	rc.mu.Unlock()
}

func (rc *RelayDeviceConn) enqueueInbound(raw json.RawMessage, msg WireMessage) error {
	rc.mu.Lock()
	scheduler := rc.inboundScheduler
	rc.mu.Unlock()
	if scheduler == nil {
		return fmt.Errorf("relay inbound scheduler unavailable")
	}
	return scheduler.enqueueMessage(raw, msg)
}

func (rc *RelayDeviceConn) setOutboundWriter(writer *relayOutboundWriter) {
	rc.mu.Lock()
	rc.writer = writer
	rc.mu.Unlock()
}

func (rc *RelayDeviceConn) registerRequestClass(requestID, method string, bulkCorrelationID string) {
	if requestID == "" {
		return
	}
	rc.mu.Lock()
	rc.requestClasses[requestID] = classifyRelayRequest(method)
	rc.requestMethods[requestID] = method
	// R1.4：仅 read_file_v2（本期 correlation allowlist）且 client 已 ack progress capability
	// 时才记录 correlation；否则不 stamp correlated chunk（base 兼容）。
	if method == "read_file_v2" && bulkCorrelationID != "" && rc.outboundChunkProgress {
		rc.requestBulkCorrelations[requestID] = bulkCorrelationID
	}
	rc.mu.Unlock()
}

// negotiateRelayChunkProgress 在 client hello 声明 relay_chunk_progress_v1 时启用 correlated
// bulk chunk（R1.4）。progress ⇒ chunks（依赖关系由 client 保证：不会只 ack progress 不 ack chunks）。
func negotiateRelayChunkProgress(conn Connection, capabilities []string) bool {
	rc, ok := conn.(*RelayDeviceConn)
	if !ok {
		return false
	}
	for _, capability := range capabilities {
		if capability == relayChunkProgressCapability {
			rc.mu.Lock()
			rc.outboundChunkProgress = true
			rc.mu.Unlock()
			return true
		}
	}
	return false
}

// negotiateRelayCancel 在 client hello 声明 cancel_request_v1 时回显该能力（R1.5）。
// Mac echo 后 iOS 才发送 cancel_request_v1 control RPC。Mac handler 不硬拒——allowlist（仅
// read_file_v2）由 handle 安装门控，device/generation 绑定由 per-conn map 保证。
func negotiateRelayCancel(_ Connection, capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == relayCancelCapability {
			return true
		}
	}
	return false
}

func (rc *RelayDeviceConn) cleanupSupersededRequest(requestID string) {
	rc.mu.Lock()
	delete(rc.requestClasses, requestID)
	delete(rc.bulkRequestContexts, requestID)
	rc.mu.Unlock()
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

func negotiateRelayChunks(conn Connection, capabilities []string) bool {
	rc, ok := conn.(*RelayDeviceConn)
	if !ok {
		return false
	}
	for _, capability := range capabilities {
		if capability == relayChunksCapability {
			rc.mu.Lock()
			rc.outboundChunks = rc.writer != nil
			enabled := rc.outboundChunks
			rc.mu.Unlock()
			return enabled
		}
	}
	return false
}

// SendJSON 将业务消息加密为 relay envelope 并发送。
func (rc *RelayDeviceConn) SendJSON(v any) {
	rc.sendJSON(v, nil)
}

func (rc *RelayDeviceConn) SendJSONClassified(v any, class relayOutboundClass) {
	rc.sendJSON(v, &class)
}

func (rc *RelayDeviceConn) sendJSON(v any, classHint *relayOutboundClass) {
	plaintext, err := json.Marshal(v)
	if err != nil {
		slog.Error("relay-conn: marshal inner payload", "device", rc.deviceID, "error", err)
		return
	}
	rc.mu.Lock()
	outboundGzip := rc.outboundGzip
	writer := rc.writer
	closed := rc.closed
	rc.mu.Unlock()
	if closed {
		// Visible signal for flapping windows: publisher still targets a
		// Connection pointer that has already been replaced/closed.
		slog.Warn("relay-conn: drop outbound on closed connection",
			"device", safeID(rc.deviceID),
			"payloadBytes", len(plaintext),
		)
		return
	}
	class := classifyRelayPayload(plaintext)
	if classHint != nil && *classHint < relayOutboundClassCount {
		class = *classHint
	}
	contentEncoding := ""
	if outboundGzip && len(plaintext) >= relayGzipThreshold {
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
	if writer != nil {
		if err := writer.enqueue(&relayOutboundJob{conn: rc, payload: plaintext, contentEncoding: contentEncoding, class: class}); err != nil {
			slog.Error("relay-conn: unified writer delivery", "device", rc.deviceID, "error", err)
			_ = rc.Close()
		}
		return
	}
	// Temporary release-window path for relayUnifiedWriterV1=false. Production
	// connections sample the flag once at secure-epoch creation.
	if err := rc.writeLogicalFrame(plaintext, contentEncoding, nil); err != nil {
		slog.Error("relay-conn: legacy relay delivery", "device", rc.deviceID, "error", err)
	}
}

func (rc *RelayDeviceConn) writeLogicalFrame(plaintext []byte, contentEncoding string, chunk *RelayChunkMetadata) error {
	rc.mu.Lock()
	if rc.closed || rc.sendEnvelope == nil {
		rc.mu.Unlock()
		return fmt.Errorf("relay connection closed")
	}
	key := append([]byte(nil), rc.macToIosKey...)
	sendEnvelope := rc.sendEnvelope
	routeID := rc.routeID
	deviceID := rc.deviceID
	generation := rc.generation
	rc.mu.Unlock()
	defer zeroBytes(key)

	// 获取并递增 counter
	counter := rc.sendCounter.Add(1) - 1 // Add 返回增加后的值，减 1 得到本次使用的值

	now := time.Now().UTC()
	envelope := &RelayEnvelope{
		Version:           1,
		RouteID:           routeID,
		SenderID:          "bridge",
		DestinationID:     deviceID,
		ChannelGeneration: generation,
		KeyEpochID:        "online:" + strconv.FormatUint(generation, 10),
		MessageID:         generateRelayID("msg_"),
		Counter:           counter,
		ContentEncoding:   contentEncoding,
		Chunk:             chunk,
		CreatedAt:         now.Format(time.RFC3339),
		ExpiresAt:         now.Add(24 * time.Hour).Format(time.RFC3339),
	}
	aad, err := envelope.EncodeAAD()
	if err != nil {
		return fmt.Errorf("encode envelope aad: %w", err)
	}
	ciphertext, err := SealEnvelope(key, counter, aad, plaintext)
	if err != nil {
		return fmt.Errorf("seal envelope: %w", err)
	}
	envelope.Ciphertext = ciphertext

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	if err := sendEnvelope(envelopeJSON); err != nil {
		return fmt.Errorf("send envelope: %w", err)
	}
	return nil
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
	plaintext, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		slog.Error("relay-conn: marshal result", "device", rc.deviceID, "requestId", requestID, "error", marshalErr)
		return
	}
	rc.mu.Lock()
	class, ok := rc.requestClasses[requestID]
	delete(rc.requestClasses, requestID)
	method := rc.requestMethods[requestID]
	delete(rc.requestMethods, requestID)
	bulkContext, hasBulkContext := rc.bulkRequestContexts[requestID]
	delete(rc.bulkRequestContexts, requestID)
	// R1.4：取出 read_file_v2 预绑定的 bulkCorrelationId（仅 progress capability acked 时非空）。
	bulkCorrelationID := rc.requestBulkCorrelations[requestID]
	delete(rc.requestBulkCorrelations, requestID)
	writer := rc.writer
	closed := rc.closed
	outboundGzip := rc.outboundGzip
	outboundChunks := rc.outboundChunks
	channelGeneration := rc.generation
	rc.mu.Unlock()
	if !ok {
		class = relayOutboundNormal
	}
	if closed {
		return
	}
	contentEncoding := ""
	if outboundGzip && len(plaintext) >= relayGzipThreshold {
		compressed, compressErr := gzipPayload(plaintext)
		if compressErr != nil {
			slog.Error("relay-conn: gzip result", "device", rc.deviceID, "requestId", requestID, "error", compressErr)
			return
		}
		if len(compressed) < len(plaintext) {
			plaintext = compressed
			contentEncoding = "gzip"
		}
	}
	if writer != nil {
		if outboundChunks && class == relayOutboundBulk && len(plaintext) > relayChunkTargetBytes {
			if len(plaintext) > relayLogicalMaximumBytes {
				slog.Error("relay-conn: logical bulk result too large", "device", rc.deviceID, "requestId", requestID, "bytes", len(plaintext))
				_ = rc.Close()
				return
			}
			chunkBytes := relayChunkTargetBytes
			if needed := (len(plaintext) + relayChunkMaximumCount - 1) / relayChunkMaximumCount; needed > chunkBytes {
				chunkBytes = needed
			}
			if chunkBytes < relayChunkMinimumBytes {
				chunkBytes = relayChunkMinimumBytes
			}
			count := uint32((len(plaintext) + chunkBytes - 1) / chunkBytes)
			groupID := generateRelayID("grp_")
			handle := newOutboundBulkHandle(groupID)
			if hasBulkContext && !rc.installHandleIfSessionBulkGenerationCurrent(bulkContext.sessionID, bulkContext.generation, handle) {
				handle.Cancel()
				relayBulkSuperseded.Add(1)
				relayBulkSupersededBeforeSubmit.Add(1)
				slog.Info("relay bulk superseded before submit", "device", rc.deviceID, "requestId", requestID, "sessionId", bulkContext.sessionID, "stale_handler_elapsed_ms", durationMillis(time.Since(bulkContext.startedAt)), "serialized_bytes", len(plaintext))
				return
			}
			// R1.4：correlated chunk（read_file_v2 + progress acked）原子登记 correlation。
			// duplicate/reuse/busy 都是 strict failure：close transport generation（无安全 owner，禁匿名 drain）。
			if bulkCorrelationID != "" {
				admitted, reason := rc.bulkCorrelations.PutIfAbsent(bulkCorrelationID)
				if !admitted {
					handle.Cancel()
					if hasBulkContext {
						rc.completeBulkHandle(bulkContext.sessionID, handle)
					}
					slog.Warn("relay bulk correlation rejected; closing generation", "device", rc.deviceID, "requestId", requestID, "reason", reason)
					_ = rc.Close()
					return
				}
			}
			job := &relayOutboundJob{
				conn: rc, payload: plaintext, contentEncoding: contentEncoding,
				cursor: &relayChunkCursor{
					groupID: groupID, count: count, chunkBytes: chunkBytes, handle: handle,
					sessionID: bulkContext.sessionID, sessionGeneration: bulkContext.generation,
					channelGeneration: channelGeneration, expiresAt: time.Now().Add(relayBulkCursorMaxAge),
					bulkCorrelationID: bulkCorrelationID,
					requestID:         requestID,
				},
			}
			// R1.5：read_file_v2（cancel allowlist）的 chunked result 把 handle 按 requestId 登记，
			// 供 cancel_request_v1 查找并 Cancel()。base/correlated 通用；cancel capability 在 handler 门控。
			if method == "read_file_v2" {
				rc.installRequestBulkHandle(requestID, handle)
			}
			if writeErr := writer.admitBulk(job); writeErr != nil {
				handle.Cancel()
				if hasBulkContext {
					rc.completeBulkHandle(bulkContext.sessionID, handle)
				}
				slog.Error("relay-conn: bulk admission failed", "device", rc.deviceID, "requestId", requestID, "error", writeErr)
				if errors.Is(writeErr, errRelayBulkQueueOverflow) {
					overload, marshalOverloadErr := json.Marshal(map[string]interface{}{
						"type": "result", "requestId": requestID, "ok": false,
						"error": &WireError{Code: "relay.overloaded", Message: "Relay bulk queue is full"},
					})
					if marshalOverloadErr == nil {
						writeErr = writer.enqueue(&relayOutboundJob{conn: rc, payload: overload, class: relayOutboundInteractive})
					}
				}
				if writeErr != nil {
					_ = rc.Close()
				}
			}
			return
		}
		if writeErr := writer.enqueue(&relayOutboundJob{conn: rc, payload: plaintext, contentEncoding: contentEncoding, class: class}); writeErr != nil {
			slog.Error("relay-conn: unified writer result delivery", "device", rc.deviceID, "requestId", requestID, "error", writeErr)
			_ = rc.Close()
		}
		return
	}
	if writeErr := rc.writeLogicalFrame(plaintext, contentEncoding, nil); writeErr != nil {
		slog.Error("relay-conn: legacy result delivery", "device", rc.deviceID, "requestId", requestID, "error", writeErr)
	}
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
	already := rc.closed
	scheduler := rc.inboundScheduler
	rc.inboundScheduler = nil
	rc.closed = true
	if !already {
		slog.Info("relay-conn: closing device connection",
			"device", safeID(rc.deviceID),
			"generation", rc.generation,
		)
	}
	rc.writer = nil
	clear(rc.requestClasses)
	for _, handle := range rc.activeBulkHandles {
		handle.Cancel()
	}
	clear(rc.activeBulkHandles)
	clear(rc.bulkRequestContexts)
	zeroBytes(rc.macToIosKey)
	zeroBytes(rc.iosToMacKey)
	rc.sendEnvelope = nil
	rc.mu.Unlock()
	if scheduler != nil {
		scheduler.close()
	}
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
