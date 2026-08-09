package gobridge

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
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
// 用 var 而非 const：测试需要覆盖成短值（如 200ms）以避免等真实超时。
//
// 2026-07-25：从 10s 提到 60s。超大 Codex session 全量 get_session_messages 实测
// response_bytes≈13MB、socket_send_ms≈10–14s，10s deadline 触发 consecutive write
// errors → 断连 → iOS 永久「重新连接中」。大包仍应收敛到投影/分页主路径，但 deadline
// 不能短于真实可写完时间。详见 go-bridge.log: too many write errors + socket_send_ms。
var bridgeWriteTimeout = 60 * time.Second

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
	_ = c.SendJSONReport(v)
}

// SendJSONReport is the error-returning write used by K4 projection probes so
// write_post can distinguish closed-conn / WriteJSON failure from real wire success.
// Plain SendJSON swallows both (historical contract for broadcaster callers).
func (c *Conn) SendJSONReport(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
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
		return err
	}
	c.consecutiveWriteErrors = 0
	return nil
}

func (c *Conn) SendResult(requestID string, data interface{}, err *WireError) {
	c.SendJSON(resultEnvelope(requestID, data, err))
}

func resultEnvelope(requestID string, data interface{}, err *WireError) map[string]interface{} {
	resp := map[string]interface{}{"type": "result", "requestId": requestID}
	if err != nil {
		resp["ok"] = false
		resp["error"] = err
	} else {
		resp["ok"] = true
		resp["data"] = data
	}
	return resp
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
	authMiddleware       *AuthMiddleware
	handlers             *Handlers
	activeConns          *ActiveConnRegistry
	bridgeID             string
	displayName          string
	runtimeVersion       string
	localURL             string
	remoteURL            string
	remoteURLs           []string
	localCandidateURLs   []string
	connectionPolicy     ConnectionPolicy
	detectionCfg         *AgentDetectionConfig
	bridgeEpoch          string
	eventPublisher       *EventPublisher
	recoveryEnabled      bool
	sessionSyncV2Enabled bool
}

// K3 enables the Codex-only shadow data plane. The client still owns UI with legacy history;
// this flag only permits capability negotiation when a shadow client explicitly opts in.
const sessionSyncV2ProductionEnabled = true

func (s *Server) SetRecoveryEnabled(enabled bool) { s.recoveryEnabled = enabled }

// SetSessionSyncV2Enabled gates the session_sync_v2 capability advertisement
// (Session Projection Stream). When true and the client opts in via hello
// capabilities, hello_ack echoes capabilities["session_sync_v2"]=true. See
// docs/protocol/bridge-v1.md「Session Projection Stream」.
func (s *Server) SetSessionSyncV2Enabled(enabled bool) { s.sessionSyncV2Enabled = enabled }

// helloSupportsSessionSyncV2 returns true when the client advertised the
// session_sync_v2 capability in hello (same shape as helloSupportsRecovery).
func helloSupportsSessionSyncV2(hello *HelloMessage) bool {
	for _, capability := range hello.Capabilities {
		if capability == "session_sync_v2" {
			return true
		}
	}
	return false
}

func helloSupportsReadFileV2(hello *HelloMessage) bool {
	for _, capability := range hello.Capabilities {
		if capability == "read_file_v2" {
			return true
		}
	}
	return false
}

func appendUniqueCapability(capabilities []string, capability string) []string {
	for _, existing := range capabilities {
		if existing == capability {
			return capabilities
		}
	}
	return append(capabilities, capability)
}

// advertiseSessionSyncV2Backend scopes ownership capability to migrated backends. The global
// hello_ack capability only confirms transport negotiation; clients decide timeline ownership
// from the selected backend descriptor.
func advertiseSessionSyncV2Backend(backends []AgentProviderDescriptor) {
	for i := range backends {
		id := backends[i].ID
		kind := backends[i].Kind
	// Per-backend migration (design §4 / K5): only backends with a projection hydrate
		// producer advertise ownership capability.
		if id == "codex" || kind == "codex" ||
			id == "claude" || id == "claudecode" || kind == "claude_code" || kind == "claude" ||
			id == "opencode" || kind == "opencode" ||
			id == "grokbuild" || kind == "grokbuild" {
			backends[i].Capabilities = appendUniqueCapability(
				backends[i].Capabilities,
				"session_sync_v2",
			)
		}
	}
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

// SetConnectionPolicy 设置 control-plane 连接策略,经 hello_ack.bridge.connectionPolicy 下发。
// 与 LAN 候选独立:关闭 preferLocalNetwork 时仍发布 LAN 候选,iOS 只是不把它们纳入自动优先。
// 由 main.go 在启动时从 -prefer-local-network flag 注入一次;config 变更走 applyConfigAndRestart
// 重启新进程,故运行期内该字段不被并发改写(与 localCandidateURLs/remoteURLs 同为启动期注入)。
// SSV2:纯 control-plane,不进入 timeline/projection。
func (s *Server) SetConnectionPolicy(policy ConnectionPolicy) {
	s.connectionPolicy = policy
}

// ConnectionPolicy 返回当前 control-plane 连接策略(供 direct 与 relay 两处 hello handler 读取)。
func (s *Server) ConnectionPolicy() ConnectionPolicy {
	return s.connectionPolicy
}

// SetDetectionConfig 设置 agent 检测配置。
func (s *Server) SetDetectionConfig(cfg *AgentDetectionConfig) {
	s.detectionCfg = cfg
}

func NewServer(handlers *Handlers) *Server {
	epoch, err := generateBridgeEpoch()
	if err != nil {
		panic(err)
	}
	return NewServerWithEpoch(handlers, epoch)
}

func NewServerWithEpoch(handlers *Handlers, bridgeEpoch string) *Server {
	if bridgeEpoch == "" {
		panic("bridge epoch must not be empty")
	}
	if handlers == nil {
		panic("handlers must not be nil")
	}
	if handlers.eventPublisher == nil || handlers.eventPublisher.BridgeEpoch() != bridgeEpoch {
		handlers.installEventPublisher(NewEventPublisher(bridgeEpoch, handlers.broadcaster))
	}
	globalDeviceConnRegistry.SetEventPublisher(handlers.eventPublisher)
	return &Server{
		handlers:       handlers,
		activeConns:    NewActiveConnRegistry(),
		bridgeEpoch:    bridgeEpoch,
		eventPublisher: handlers.eventPublisher,
	}
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
		s.handlers.unregisterConnection(directConnection)
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
	s.handlers.registerConnection(directConnection)

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
			s.handleHello(conn, directConnection, &msg)
		case "request":
			// Long-running RPCs (e.g. grokbuild StartSession: spawn CLI +
			// initialize/auth/load) must not block the WebSocket read loop.
			// Otherwise client pings/pongs stall and iOS hits the 30s RPC timeout
			// ("RPC 超时（30s）") while send_message is still starting the agent.
			msgCopy := msg
			go s.handlers.HandleRPC(directConnection, msgCopy)
		case "recovery_applied":
			if err := s.eventPublisher.CompleteRecovery(directConnection, msg.RecoveryID, msg.AppliedThroughBySession); err != nil {
				slog.Warn("go-bridge: recovery acknowledgement rejected", "remote", conn.remote, "error", err)
			}
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
		"bridgeEpoch": s.bridgeEpoch,
	}
	conn.SendJSON(ackPayload)

	ackJSON, _ := json.Marshal(ackPayload)
	slog.Info("go-bridge: register_ack sent", "payload", string(ackJSON))
}

func (s *Server) handleHello(conn *Conn, connection Connection, msg *WireMessage) {
	if conn.revoked {
		connection.SendJSON(map[string]interface{}{
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
	hello.Capabilities = msg.Capabilities
	hello.LastBridgeEpoch = msg.LastBridgeEpoch
	hello.LastEventID = msg.LastEventID
	hello.LastSeenBySession = msg.LastSeenBySession

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
	// control-plane 连接策略随每次 hello_ack 权威下发(默认 false=Relay 底座)。
	if ack.Bridge != nil {
		ack.Bridge.ConnectionPolicy = &s.connectionPolicy
	}
	ack.BridgeEpoch = s.bridgeEpoch
	var replay []EventMessage
	if s.recoveryEnabled && helloSupportsRecovery(&hello) && ack.Ok {
		plan, events, err := s.prepareRecovery(connection, &hello)
		if err != nil {
			slog.Warn("go-bridge: recovery preparation failed", "remote", conn.remote, "error", err)
			_ = conn.Close()
			return
		}
		ack.Recovery = plan
		ack.Capabilities["recovery_v1"] = true
		replay = events
	}
	if s.sessionSyncV2Enabled && helloSupportsSessionSyncV2(&hello) && ack.Ok {
		ack.Capabilities["session_sync_v2"] = true
		advertiseSessionSyncV2Backend(ack.Backends)
		s.eventPublisher.SetConnSyncV2(connection, true)
	}
	if ack.Ok && helloSupportsReadFileV2(&hello) {
		ack.Capabilities["read_file_v2"] = true
		s.eventPublisher.SetConnReadFileV2(connection, true)
	}
	connection.SendJSON(ack)
	if ack.Recovery != nil {
		s.emitRecoveryFrames(connection, ack.Recovery, replay)
	}

	slog.Info("go-bridge: hello_ack sent", "ok", ack.Ok, "device", hello.Client.DeviceID)
}

func helloSupportsRecovery(hello *HelloMessage) bool {
	for _, capability := range hello.Capabilities {
		if capability == "recovery_v1" {
			return true
		}
	}
	return false
}

func (s *Server) prepareRecovery(conn Connection, hello *HelloMessage) (*BridgeRecoveryPlan, []EventMessage, error) {
	recoveryID, err := generateBridgeEpoch()
	if err != nil {
		return nil, nil, err
	}
	cuts := make(BridgeSessionCutMap)
	affected := make([]BridgeAffectedSession, 0)
	replay := make([]EventMessage, 0)
	mode := "replay"
	for backendID, sessions := range hello.LastSeenBySession {
		if cuts[backendID] == nil {
			cuts[backendID] = make(map[string]BridgeSessionCut)
		}
		for sessionID, cursor := range sessions {
			affected = append(affected, BridgeAffectedSession{BackendID: backendID, SessionID: sessionID})
			if hello.LastBridgeEpoch != s.bridgeEpoch {
				if latest, ok := s.eventPublisher.EventBuffer().LatestCut(backendID, sessionID); ok {
					cuts[backendID][sessionID] = latest
				} else {
					cuts[backendID][sessionID] = BridgeSessionCut{EventID: fmt.Sprintf("%s:0", s.bridgeEpoch)}
				}
				mode = "full_resync"
				continue
			}
			result := s.eventPublisher.EventBuffer().Replay(backendID, sessionID, cursor)
			cuts[backendID][sessionID] = result.Through
			if result.Disposition == ReplaySnapshotRequired {
				mode = "snapshot_required"
			} else {
				replay = append(replay, result.Events...)
			}
		}
	}
	if hello.LastBridgeEpoch != s.bridgeEpoch {
		mode = "full_resync"
	}
	if _, err := s.eventPublisher.BeginRecovery(conn, recoveryID, cuts); err != nil {
		return nil, nil, err
	}
	sort.Slice(replay, func(i, j int) bool { return replay[i].Seq < replay[j].Seq })
	plan := &BridgeRecoveryPlan{RecoveryID: recoveryID, Mode: mode}
	if mode == "replay" {
		plan.ReplayThroughBySession = cuts
	} else {
		plan.AffectedSessions = affected
		plan.CutBySession = cuts
		replay = nil
	}
	return plan, replay, nil
}

func (s *Server) emitRecoveryFrames(conn Connection, plan *BridgeRecoveryPlan, replay []EventMessage) {
	for _, event := range replay {
		_ = s.eventPublisher.EnqueueControl(conn, event, true)
	}
	if plan.Mode == "replay" {
		_ = s.eventPublisher.EnqueueControl(conn, map[string]interface{}{"type": "recovery_barrier", "recoveryId": plan.RecoveryID, "replayThroughBySession": plan.ReplayThroughBySession}, true)
	}
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
