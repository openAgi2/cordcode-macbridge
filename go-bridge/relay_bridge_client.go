package gobridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// relay bridge WebSocket 保活参数（与直接路径 server.go 的 30s ping / 90s 读 deadline 对齐）。
// 统一用 var 而非 const：测试需要覆盖成短值（如 ping 50ms / 读 200ms）避免等真实 30s/90s。
var (
	relayPingPeriod  = 30 * time.Second
	relayReadTimeout = 90 * time.Second

	// relayWriteTimeout 是 relay bridge WebSocket 所有数据写的写 deadline（对齐 10s）。
	relayWriteTimeout = 10 * time.Second
)

// RelayBridgeClient 是 Mac bridge 连接到 Relay 服务的客户端。
// 它建立 bridge WebSocket 连接，处理设备握手，并为每个已认证
// 设备创建 RelayDeviceConn 注册到 Broadcaster。
//
// 方案 §10.1：Mac outbound relay 建链构造点。
type RelayBridgeClient struct {
	mu      sync.Mutex
	writeMu sync.Mutex

	handlers   *Handlers
	hub        *RelayHub
	identity   *RelayCryptoIdentity
	bridgeID   string
	routeID    string
	credential string

	conn    *websocket.Conn
	done    chan struct{}
	devices map[string]*RelayDeviceConn // deviceID -> active relay connection
}

// NewRelayBridgeClient 创建 relay bridge 客户端。
func NewRelayBridgeClient(
	handlers *Handlers,
	hub *RelayHub,
	identity *RelayCryptoIdentity,
	bridgeID, routeID, credential string,
) *RelayBridgeClient {
	return &RelayBridgeClient{
		handlers:   handlers,
		hub:        hub,
		identity:   identity,
		bridgeID:   bridgeID,
		routeID:    routeID,
		credential: credential,
		devices:    make(map[string]*RelayDeviceConn),
	}
}

// Connect 连接到 relay bridge WebSocket 并开始读取帧。
// 调用者应在独立 goroutine 中运行此方法。
func (c *RelayBridgeClient) Connect(relayBridgeURL string) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.credential)
	conn, _, err := websocket.DefaultDialer.Dial(relayBridgeURL, header)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.done = make(chan struct{})
	done := c.done
	c.mu.Unlock()

	// 在调用方 goroutine 读取保活参数并捕获到闭包/参数：避免 pingLoop、pong handler 等
	// 子 goroutine 直接读包级 var 与测试覆盖 var 产生 data race（race detector 不认网络
	// 回环为同步）。同时保证单次连接生命周期内保活参数一致。
	readTimeout := relayReadTimeout
	pingPeriod := relayPingPeriod

	// 保活（P0-B）：读 deadline + pong handler + 主动 ping ticker。
	// relay WebSocket 不再依赖 OS TCP keepalive（~2h）"假活"。无 pong 时读 deadline 到期
	// → readLoop 的 ReadMessage 报错 return → Run 收到 c.done 触发重连（backoff 不变）。
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})

	slog.Info("relay-bridge-client: connected", "url", relayBridgeURL)
	if c.handlers != nil {
		c.handlers.FlushRelayOutboxes()
	}
	go c.pingLoop(done, pingPeriod)
	go c.readLoop()
	return nil
}

// Run 在独立 goroutine 中持续运行 relay bridge 连接，支持自动重连。
// 调用者应在独立 goroutine 中调用此方法。当 ctx 被取消时返回。
// 重连策略：exponential backoff，初始 2 秒，最大 60 秒，抖动 ±25%。
func (c *RelayBridgeClient) Run(ctx context.Context, relayBridgeURL string) {
	backoff := 2 * time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.Connect(relayBridgeURL)
		if err == nil {
			// 连接成功，等待 readLoop 退出
			slog.Info("relay-bridge-client: connected, waiting for disconnect")
			c.mu.Lock()
			done := c.done
			c.mu.Unlock()

			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					c.closeConn()
					return
				}
			}

			slog.Warn("relay-bridge-client: disconnected, will reconnect")
			c.closeConn()
			backoff = 2 * time.Second // 重连成功后重置 backoff
		} else {
			slog.Warn("relay-bridge-client: connect failed", "error", err, "backoff", backoff)
		}

		// 等待 backoff 或 ctx 取消
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
		}
	}
}

// closeConn 关闭当前连接并清理已注册的设备。
// 与 Close() 不同，closeConn 不发送 WebSocket close frame（连接可能已断），
// 且不清除 c.done（由下一次 Connect 重新创建）。
func (c *RelayBridgeClient) closeConn() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	// 清理所有活跃的 relay device connections
	for deviceID, rc := range c.devices {
		if c.handlers != nil {
			c.handlers.broadcaster.UnsubscribeAll(rc)
		}
		rc.Close()
		delete(c.devices, deviceID)
	}
}

// jitter 在 duration 基础上添加 ±25% 的随机抖动。
func jitter(d time.Duration) time.Duration {
	factor := 0.75 + 0.5*rand.Float64()
	return time.Duration(float64(d) * factor)
}

// Close 关闭 relay bridge 客户端连接。
func (c *RelayBridgeClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.writeMu.Lock()
		c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		c.conn.Close()
		c.writeMu.Unlock()
		c.conn = nil
	}
	if c.done != nil {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	}

	// 清理所有活跃的 relay device connections
	for deviceID, rc := range c.devices {
		if c.handlers != nil {
			c.handlers.broadcaster.UnsubscribeAll(rc)
		}
		rc.Close()
		delete(c.devices, deviceID)
	}
}

// SendEnvelope 通过已认证 bridge WebSocket 向 relay 写入 Mac→device 信封。
func (c *RelayBridgeClient) SendEnvelope(envelope json.RawMessage) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return websocket.ErrCloseSent
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// 写 deadline 紧贴 WriteMessage（持 c.writeMu 内，gorilla 写并发约束满足）。
	_ = conn.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, envelope)
}

// readLoop 读取从 relay 转发来的设备消息并处理。
// 这包括握手消息（OnlineClientHello）和加密的业务信封（iOS→Mac 方向）。
func (c *RelayBridgeClient) readLoop() {
	defer func() {
		c.mu.Lock()
		if c.done != nil {
			select {
			case <-c.done:
			default:
				close(c.done)
			}
		}
		c.mu.Unlock()
	}()

	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return
		}

		_, payload, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Error("relay-bridge-client: read error", "error", err)
			}
			return
		}

		// 尝试解析为 OnlineClientHello（握手阶段）
		var hello OnlineClientHello
		if err := json.Unmarshal(payload, &hello); err == nil && hello.Type == "online_client_hello" {
			c.handleClientHello(hello)
			continue
		}

		// 否则是加密信封（iOS→Mac），解密后分发
		c.handleInboundEnvelope(payload)
	}
}

// pingLoop 周期性向 relay 发 PingMessage 探测半开连接。
// ⚠️ gorilla 并发约束（根治 spec 坑1）：WriteControl 不套 writeMu——它内部用独立锁，
// 允许与 WriteMessage 并发。若套 writeMu，ping 会被正在卡住的数据写阻塞，违背加 ping 初衷。
// done 为当前连接的 c.done，随 readLoop 退出而关闭：保证重连后不泄漏旧 ping goroutine、
// 不写已关闭的旧 conn。ping 写失败（连接已坏）即 return，由 readLoop 的退出驱动重连。
// period 由 Connect 在调用方 goroutine 读取传入（避免子 goroutine 读包级 var 与测试覆盖 race）。
func (c *RelayBridgeClient) pingLoop(done <-chan struct{}, period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()
			if conn == nil {
				return
			}
			// WriteControl 自带短 deadline（第3参），不套 writeMu（见上方注释）。
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

// handleClientHello 处理来自 iOS 设备的在线握手请求。
func (c *RelayBridgeClient) handleClientHello(hello OnlineClientHello) {
	deviceID := hello.DeviceID
	slog.Info("relay-bridge-client: received client hello",
		"deviceID", safeID(deviceID),
		"bridgeID", hello.BridgeID,
		"generation", hello.ChannelGeneration,
	)

	// 查找设备记录以获取 identity public key
	c.handlers.mu.Lock()
	store := c.handlers.trustedDevices
	identity := c.handlers.relayIdentity
	bridgeID := c.handlers.bridgeID
	c.handlers.mu.Unlock()

	if store == nil || identity == nil {
		slog.Warn("relay-bridge-client: relay not configured, rejecting hello")
		c.sendServerHelloError(deviceID, "relay.not_configured")
		return
	}

	record, err := store.LookupByDeviceID(deviceID)
	if err != nil || record == nil {
		slog.Warn("relay-bridge-client: device not found", "deviceID", safeID(deviceID))
		c.sendServerHelloError(deviceID, "auth.invalid_token")
		return
	}
	if record.RevokedAt != nil {
		slog.Warn("relay-bridge-client: device revoked", "deviceID", safeID(deviceID))
		c.sendServerHelloError(deviceID, "auth.revoked")
		return
	}

	if record.IdentityPublicKey == "" {
		slog.Warn("relay-bridge-client: device has no relay identity", "deviceID", safeID(deviceID))
		c.sendServerHelloError(deviceID, "relay.identity_not_bound")
		return
	}

	// 派生 identity auth key
	identityPubKey, err := decodeBase64Bytes(record.IdentityPublicKey)
	if err != nil {
		slog.Warn("relay-bridge-client: invalid identity key", "deviceID", safeID(deviceID), "error", err)
		c.sendServerHelloError(deviceID, "relay.invalid_identity")
		return
	}

	authKey, err := identity.DeriveIdentityAuthKey(identityPubKey, bridgeID, deviceID)
	if err != nil {
		slog.Warn("relay-bridge-client: identity auth key derivation failed", "deviceID", safeID(deviceID), "error", err)
		c.sendServerHelloError(deviceID, "relay.identity_failed")
		return
	}

	// 执行握手
	hs, err := NewOnlineHandshake(authKey)
	if err != nil {
		slog.Warn("relay-bridge-client: handshake init failed", "deviceID", safeID(deviceID), "error", err)
		c.sendServerHelloError(deviceID, "relay.handshake_failed")
		return
	}

	response, err := hs.AcceptClientHello(hello)
	if err != nil {
		slog.Warn("relay-bridge-client: client hello rejected", "deviceID", safeID(deviceID), "error", err)
		c.sendServerHelloError(deviceID, "relay.handshake_rejected")
		return
	}

	// 发送 server hello
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return
	}

	responseBytes, _ := json.Marshal(response)
	c.writeMu.Lock()
	// 写 deadline 紧贴 WriteMessage（持 c.writeMu 内）。
	_ = conn.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
	err = conn.WriteMessage(websocket.TextMessage, responseBytes)
	c.writeMu.Unlock()
	if err != nil {
		slog.Warn("relay-bridge-client: failed to send server hello", "deviceID", safeID(deviceID), "error", err)
		return
	}

	// 握手状态稍后会擦除，连接必须持有独立的 traffic key 副本。
	macToIosKey := append([]byte(nil), hs.MacToIosKey()...)
	iosToMacKey := append([]byte(nil), hs.IosToMacKey()...)

	routeID := c.routeID

	// 通过已认证的 bridge WebSocket 将密文交给 relay，适用于本地或外部部署。
	rc := NewRelayDeviceConn(
		deviceID,
		bridgeID,
		routeID,
		hello.ChannelGeneration,
		record,
		macToIosKey,
		iosToMacKey,
		c.SendEnvelope,
	)

	c.mu.Lock()
	if stale := c.devices[deviceID]; stale != nil {
		if c.handlers != nil {
			c.handlers.broadcaster.UnsubscribeAll(stale)
		}
		_ = stale.Close()
	}
	c.devices[deviceID] = rc
	c.mu.Unlock()

	// 注册到 Broadcaster
	c.handlers.broadcaster.RegisterConn(rc)

	// 清理握手敏感数据
	hs.Destroy()

	slog.Info("relay-bridge-client: device authenticated and registered",
		"deviceID", safeID(deviceID),
		"generation", hello.ChannelGeneration,
	)
}

// handleInboundEnvelope 处理来自 iOS 的加密业务信封。
// 方案 §5.5：iOS→Mac 方向使用 iosToMacKey 解密，解密后分发给 handlers。
func (c *RelayBridgeClient) handleInboundEnvelope(payload []byte) {
	// 解析外层信封获取设备 ID
	var env RelayEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		slog.Debug("relay-bridge-client: invalid inbound envelope", "error", err)
		return
	}

	deviceID := env.SenderID

	// 查找对应的 RelayDeviceConn
	c.mu.Lock()
	rc, ok := c.devices[deviceID]
	c.mu.Unlock()

	if !ok {
		slog.Warn("relay-bridge-client: inbound envelope from unknown device",
			"deviceID", safeID(deviceID))
		return
	}

	// 解密并验证 counter
	innerJSON, err := rc.ReceiveJSON(payload)
	if err != nil {
		slog.Warn("relay-bridge-client: inbound decrypt failed",
			"deviceID", safeID(deviceID), "counter", env.Counter, "error", err)
		return
	}

	slog.Debug("relay-bridge-client: received inbound message",
		"deviceID", safeID(deviceID),
		"counter", env.Counter,
		"payloadLen", len(innerJSON),
	)

	// 分发给 handlers 处理
	if c.handlers != nil {
		c.handlers.HandleRelayInbound(rc, innerJSON)
	}
}

// sendServerHelloError 通过 relay 发送错误响应给设备。
func (c *RelayBridgeClient) sendServerHelloError(deviceID, code string) {
	// 首版：错误通过 relay envelope 发送
	errMsg := map[string]string{
		"type":     "online_server_hello_error",
		"deviceId": deviceID,
		"code":     code,
	}
	data, _ := json.Marshal(errMsg)

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		// 写 deadline 紧贴 WriteMessage（持 c.writeMu 内）。
		_ = conn.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
		_ = conn.WriteMessage(websocket.TextMessage, data)
	}
}

// Connected 返回客户端是否已连接。
func (c *RelayBridgeClient) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// ActiveDeviceCount 返回活跃的 relay 设备连接数。
func (c *RelayBridgeClient) ActiveDeviceCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.devices)
}

func decodeBase64Bytes(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
