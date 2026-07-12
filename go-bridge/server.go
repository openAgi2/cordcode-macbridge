package gobridge

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/core"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	bridgeReadTimeout    = 90 * time.Second // iOS ping interval 30s + timeout 20s = 最差 50s 检测断连，90s 服务端 deadline 有充足余量
	maxInboundFrameBytes = int64(1 << 20)
)

// bridgeWriteTimeout 是所有客户端数据写（WriteJSON/WriteMessage）的写 deadline。
// 用 var 而非 const：测试需要覆盖成短值（如 200ms）以避免等真实 10s。
// 取值 10s：远大于正常单帧写（<5ms），远小于 TCP RTO（数十秒），
// 既能快速发现半开坏连接，又不会误杀慢写。详见 docs/2026-06-17-bridge-hang-implementation-spec.md 坑 2。
var bridgeWriteTimeout = 10 * time.Second

// Conn wraps a WebSocket connection with thread-safe writes.
type Conn struct {
	mu                     sync.Mutex
	conn                   *websocket.Conn
	remote                 string
	closed                 bool
	lastPong               time.Time
	onCleanup              func()
	authedDevice           *TrustedDeviceRecord
	revoked                bool
	consecutiveWriteErrors int
}

type ActiveConnRegistry struct {
	mu    sync.Mutex
	conns map[*Conn]struct{}
}

func NewActiveConnRegistry() *ActiveConnRegistry {
	return &ActiveConnRegistry{conns: make(map[*Conn]struct{})}
}

func (r *ActiveConnRegistry) Register(conn *Conn) {
	if conn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[conn] = struct{}{}
}

func (r *ActiveConnRegistry) Unregister(conn *Conn) {
	if conn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, conn)
}

func (r *ActiveConnRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conns)
}

func (r *ActiveConnRegistry) Snapshot() []*Conn {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*Conn, 0, len(r.conns))
	for conn := range r.conns {
		result = append(result, conn)
	}
	return result
}

func (r *ActiveConnRegistry) CloseAll(reason string) int {
	conns := r.Snapshot()
	for _, conn := range conns {
		if err := conn.CloseWithControl(websocket.CloseGoingAway, reason); err != nil {
			slog.Debug("go-bridge: close active connection failed", "remote", conn.remote, "error", err)
		}
	}
	return len(conns)
}

func newConn(ws *websocket.Conn) *Conn {
	return &Conn{
		conn:     ws,
		remote:   ws.RemoteAddr().String(),
		lastPong: time.Now(),
	}
}

func (c *Conn) SendJSON(v interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	// 写 deadline 必须在持 c.mu 的情况下紧贴 WriteJSON 调用（gorilla 不允许同 conn 并发写），
	// 避免另一个写者在 deadline 与实际写之间插入。详见根治 spec 坑 1。
	_ = c.conn.SetWriteDeadline(time.Now().Add(bridgeWriteTimeout))
	if err := c.conn.WriteJSON(v); err != nil {
		c.consecutiveWriteErrors++
		slog.Debug("go-bridge: write error", "error", err, "consecutive", c.consecutiveWriteErrors)
		if c.consecutiveWriteErrors >= 5 {
			slog.Warn("go-bridge: too many write errors, closing connection", "remote", c.remote)
			c.closed = true
			// 关闭底层 ws 让读循环退出（加写 deadline 后写失败会更快到来，必须让连接真正关闭）。
			// ⚠️死锁陷阱：必须是 c.conn.Close()（gorilla *websocket.Conn.Close，不经 c.mu）。
			// 绝不能用 CloseWithControl 或 c.Close()——后者转调 CloseWithControl，其首行即
			// c.mu.Lock()，而 SendJSON 已持有 c.mu，会当场死锁。详见根治 spec P0-A 配套段。
			_ = c.conn.Close()
			if cleanup := c.onCleanup; cleanup != nil {
				c.onCleanup = nil
				c.mu.Unlock()
				cleanup()
				c.mu.Lock()
			}
		}
	} else {
		c.consecutiveWriteErrors = 0
	}
}

func (c *Conn) SendResult(requestID string, data interface{}, err *WireError) {
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
	c.SendJSON(resp)
}

func (c *Conn) SendEvent(sessionID, backendID, eventName string, data interface{}) {
	c.SendJSON(EventMessage{
		Type:      "event",
		SessionID: sessionID,
		BackendID: backendID,
		Event:     eventName,
		Data:      data,
	})
}

func (c *Conn) Close() error {
	return c.CloseWithControl(websocket.CloseNormalClosure, "")
}

func (c *Conn) CloseWithControl(code int, reason string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	cleanup := c.onCleanup
	c.onCleanup = nil
	c.mu.Unlock()

	var closeErr error
	if conn != nil {
		deadline := time.Now().Add(1 * time.Second)
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), deadline); err != nil {
			slog.Debug("go-bridge: close control write failed", "remote", c.remote, "code", code, "error", err)
		}
		closeErr = conn.Close()
	}
	if cleanup != nil {
		cleanup()
	}
	return closeErr
}

// Server manages WebSocket connections.
type Server struct {
	authMiddleware     *AuthMiddleware
	handlers           *Handlers
	activeConns        *ActiveConnRegistry
	bridgeID           string
	displayName        string
	runtimeVersion     string
	localURL           string
	remoteURL          string
	remoteURLs         []string
	localCandidateURLs []string
	detectionCfg       *AgentDetectionConfig
}

// SetAuthMiddleware 设置认证中间件，nil 表示不启用认证。
func (s *Server) SetAuthMiddleware(m *AuthMiddleware) {
	s.authMiddleware = m
}

// SetBridgeIdentity 设置 Bridge 身份信息，用于 hello 握手。
func (s *Server) SetBridgeIdentity(bridgeID, displayName, runtimeVersion, localURL, remoteURL string, remoteURLs ...string) {
	s.bridgeID = bridgeID
	s.displayName = displayName
	s.runtimeVersion = runtimeVersion
	s.localURL = localURL
	s.remoteURL = remoteURL
	s.remoteURLs = uniqueNonEmptyStrings(append([]string{remoteURL}, remoteURLs...))
	s.handlers.SetBridgeID(bridgeID)
}

// SetLocalCandidateURLs 设置 LAN 直连候选列表,用于 hello_ack.currentURLs.locals(secondary 候选)。
func (s *Server) SetLocalCandidateURLs(urls []string) {
	s.localCandidateURLs = uniqueNonEmptyStrings(urls)
}

// SetDetectionConfig 设置 agent 检测配置。
func (s *Server) SetDetectionConfig(cfg *AgentDetectionConfig) {
	s.detectionCfg = cfg
}

func NewServer(handlers *Handlers) *Server {
	return &Server{handlers: handlers, activeConns: NewActiveConnRegistry()}
}

func (s *Server) CloseAllConnections(reason string) int {
	if s.activeConns == nil {
		return 0
	}
	return s.activeConns.CloseAll(reason)
}

func (s *Server) ActiveConnectionCount() int {
	if s.activeConns == nil {
		return 0
	}
	return s.activeConns.Count()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// WebSocket 端点认证检查（authMiddleware 为 nil 时跳过，保持开发模式兼容）。
	// product 模式下必须同时覆盖 `/` 与 `/bridge`，避免客户端走根路径绕过认证。
	var authedDevice *TrustedDeviceRecord
	if s.authMiddleware != nil {
		dev, authErr := s.authMiddleware.AuthenticateRequest(r)
		if authErr != nil {
			hasAuth := r.Header.Get("Authorization") != ""
			hasDeviceID := r.Header.Get("X-CordCode-Device-ID") != ""
			slog.Warn("go-bridge: auth failed",
				"path", r.URL.Path,
				"error", authErr,
				"hasAuthHeader", hasAuth,
				"hasDeviceIDHeader", hasDeviceID,
				"remote", r.RemoteAddr,
			)
			authErrorJSON(w, authErr)
			return
		}
		authedDevice = dev
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("go-bridge: upgrade failed", "error", err)
		return
	}
	ws.SetReadLimit(maxInboundFrameBytes)
	conn := newConn(ws)
	conn.authedDevice = authedDevice
	directConnection := adaptDirectConn(conn)

	// 先安装 cleanup，再发布到 active/device/broadcaster registries。
	// Shutdown 可能在连接刚 register 后立即 CloseAllConnections；如果 cleanup 还未安装，
	// 连接会被关闭但不会从 active registry 移除。
	conn.mu.Lock()
	conn.onCleanup = func() {
		s.handlers.broadcaster.UnsubscribeAll(directConnection)
		if authedDevice != nil {
			globalDeviceConnRegistry.Unregister(authedDevice.DeviceID, conn)
		}
		if s.activeConns != nil {
			s.activeConns.Unregister(conn)
		}
	}
	conn.mu.Unlock()

	if s.activeConns != nil {
		s.activeConns.Register(conn)
	}
	s.handlers.broadcaster.RegisterConn(directConnection)

	slog.Info("go-bridge: client connected", "remote", conn.remote)

	// 注册设备连接，用于 revoke 时主动断开
	if authedDevice != nil {
		globalDeviceConnRegistry.Register(authedDevice.DeviceID, conn)
	}

	// pong handler: 更新 lastPong
	ws.SetPongHandler(func(appData string) error {
		conn.mu.Lock()
		conn.lastPong = time.Now()
		conn.mu.Unlock()
		return ws.SetReadDeadline(time.Now().Add(bridgeReadTimeout))
	})

	// ping ticker：30s 发 ping，90s 无 pong 则关闭
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	go func() {
		for range pingTicker.C {
			conn.mu.Lock()
			if conn.closed {
				conn.mu.Unlock()
				return
			}
			elapsed := time.Since(conn.lastPong)
			conn.mu.Unlock()

			if elapsed > 90*time.Second {
				slog.Info("go-bridge: ping timeout, closing", "remote", conn.remote)
				_ = conn.Close()
				return
			}
			conn.mu.Lock()
			if !conn.closed {
				_ = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
			conn.mu.Unlock()
		}
	}()

	defer conn.Close()

	for {
		if err := ws.SetReadDeadline(time.Now().Add(bridgeReadTimeout)); err != nil {
			slog.Debug("go-bridge: set read deadline failed", "remote", conn.remote, "error", err)
			break
		}
		_, raw, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Error("go-bridge: read error", "error", err)
			}
			break
		}

		var msg WireMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			slog.Warn("go-bridge: invalid message", "error", err)
			continue
		}

		switch msg.Type {
		case "register":
			s.handleRegister(conn, &msg)
		case "hello":
			s.handleHello(conn, &msg)
		case "request":
			// Long-running RPCs (e.g. grokbuild StartSession: spawn CLI +
			// initialize/auth/load) must not block the WebSocket read loop.
			// Otherwise client pings/pongs stall and iOS hits the 30s RPC timeout
			// ("RPC 超时（30s）") while send_message is still starting the agent.
			msgCopy := msg
			go s.handlers.HandleRPC(directConnection, msgCopy)
		case "ping":
			conn.SendJSON(map[string]string{"type": "pong"})
		default:
			slog.Debug("go-bridge: unknown message type", "type", msg.Type)
		}
	}
}

func (s *Server) handleRegister(conn *Conn, msg *WireMessage) {
	slog.Info("go-bridge: register", "client", msg.Client, "protocol", msg.Protocol)

	backends := s.handlers.BackendList()

	ackPayload := map[string]interface{}{
		"type":        "register_ack",
		"ok":          true,
		"protocol":    map[string]interface{}{"name": BridgeProtocolName, "version": BridgeProtocolVersion, "schemaRevision": BridgeProtocolSchemaRevision},
		"backends":    backends,
		"bridgeEpoch": fmtEpoch(),
	}
	conn.SendJSON(ackPayload)

	ackJSON, _ := json.Marshal(ackPayload)
	slog.Info("go-bridge: register_ack sent", "payload", string(ackJSON))
}

func (s *Server) handleHello(conn *Conn, msg *WireMessage) {
	if conn.revoked {
		conn.SendJSON(map[string]interface{}{
			"type": "hello_ack",
			"ok":   false,
			"error": map[string]string{
				"code":    "auth.device_revoked",
				"message": "设备授权已取消，请重新授权",
			},
		})
		return
	}

	var hello HelloMessage
	if err := json.Unmarshal(msg.Client, &hello.Client); err != nil {
		slog.Warn("go-bridge: hello client parse error", "error", err)
	}
	if err := json.Unmarshal(msg.Protocol, &hello.Protocol); err != nil {
		slog.Warn("go-bridge: hello protocol parse error", "error", err)
	}
	hello.Type = msg.Type

	codexMode := ""
	var agents map[string]core.Agent
	if s.handlers != nil {
		codexMode = s.handlers.CodexBackendMode()
		agents = s.handlers.Agents()
	}

	ack := HandleHelloWithRemoteURLs(
		&hello,
		conn.authedDevice,
		s.bridgeID,
		s.displayName,
		s.runtimeVersion,
		s.localURL,
		s.remoteURL,
		s.remoteURLs,
		s.localCandidateURLs,
		agents,
		codexMode,
		s.detectionCfg,
		s.handlers.sessions,
	)
	conn.SendJSON(ack)

	slog.Info("go-bridge: hello_ack sent", "ok", ack.Ok, "device", hello.Client.DeviceID)
}

func fmtEpoch() string {
	return time.Now().Format("20060102-150405")
}

// authErrorJSON 将认证错误以 HTTP 401 JSON 响应返回，不升级 WebSocket。
func authErrorJSON(w http.ResponseWriter, authErr error) {
	var code, message string
	if ae, ok := authErr.(AuthError); ok {
		code = ae.Code
		message = ae.Message
	} else {
		code = "auth.error"
		message = authErr.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// Conn 实现 Connection 接口的额外方法。
func (c *Conn) AuthedDevice() *TrustedDeviceRecord {
	return c.authedDevice
}

func (c *Conn) RemoteAddr() string {
	return c.remote
}
