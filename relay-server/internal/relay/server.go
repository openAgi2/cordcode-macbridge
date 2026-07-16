package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// relay 连接读写 deadline。写 deadline 从 10s 提到 120s：去分页后单次全量响应可达数 MB
// （32MB 帧上限，加密+base64 后膨胀约 25%），WiFi(~10Mbps)传 32MB 需 ~26s，中等蜂窝需 ~120s，
// 10s/60s 会误杀正常大帧传输。半开检测已由 go-bridge 独立应用层心跳(10s) + 本侧读 deadline(90s)
// 覆盖，写 deadline 不再兼任主要半开检测职能。用 var 而非 const：测试覆盖成短值。
var (
	relayWriteDeadline = 120 * time.Second // socketPeer.write 转发写 deadline（容纳大帧慢链路传输）
	relayReadDeadline  = 90 * time.Second  // readBridgeFrames/readDeviceFrames 读 deadline
)

type Config struct {
	PublicEndpoint               string
	ProvisionTokenDigest         []byte
	MailboxTTL                   time.Duration
	MaxMailboxBytes              int64
	MaxFrameBytes                int64
	RateLimitPerMinute           int
	ActivationRateLimitPerMinute int
	TrustedProxyCIDRs            []string
}

func ProvisionTokenDigestFromHex(value string) ([]byte, error) {
	digest, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil || len(digest) != 32 {
		return nil, fmt.Errorf("RELAY_PROVISION_TOKEN_SHA256 must be a SHA-256 hex digest")
	}
	return digest, nil
}

type Server struct {
	store             *Store
	config            Config
	logger            *slog.Logger
	limiter           *RateLimiter
	activationLimiter *RateLimiter
	trustedProxyNets  []*net.IPNet
	nonces            *activationNonceCache
	upgrader          websocket.Upgrader

	mu      sync.Mutex
	bridges map[string]*socketPeer
	devices map[string]*socketPeer
}

type socketPeer struct {
	conn    *websocket.Conn
	writeMu sync.Mutex

	// Per-device bounded send queue + dedicated writer goroutine. A slow device
	// no longer blocks the bridge read loop (which previously called
	// target.write synchronously, stalling all devices on the same route). The
	// read loop now does a non-blocking enqueue; when the queue is full the
	// device is disconnected (current envelope falls through to mailbox so it
	// is not lost).
	sendCh     chan []byte
	done       chan struct{}
	queueBytes int64 // atomic — bytes currently queued, for backpressure + metrics
	drops      int64 // atomic — count of frames dropped due to full queue
	startOnce  sync.Once
	stopOnce   sync.Once
}

// Per-device bounded send queue limits. Either bound triggers disconnect of
// the slow device. A durable mailbox epoch is persisted separately by the
// bridge read loop; connection-scoped traffic is never made durable. Tuned so a healthy device never trips them; only a
// genuinely stuck receiver (full TCP window) does.
const (
	perDeviceSendQueueFrames = 256     // single device send-queue frame cap
	perDeviceSendQueueBytes  = 8 << 20 // 8 MiB byte cap; whichever hits first disconnects
)

// startWriter launches the per-peer writer goroutine exactly once. Frames
// enqueued via enqueue are written serially under writeMu. The goroutine exits
// when sendCh is closed (via shutdownWriter) or done is closed.
func (p *socketPeer) startWriter() {
	p.startOnce.Do(func() {
		go p.writeLoop()
	})
}

func (p *socketPeer) writeLoop() {
	for {
		select {
		case payload, ok := <-p.sendCh:
			if !ok {
				return
			}
			atomic.AddInt64(&p.queueBytes, -int64(len(payload)))
			if err := p.write(payload); err != nil {
				// Writer error (peer gone / write deadline): stop draining. The
				// read loop's removeDevice/closePeer path tears down the conn.
				return
			}
		case <-p.done:
			return
		}
	}
}

// shutdownWriter closes the send channel and done, idempotently. After this,
// the writer goroutine exits. Callers must ensure no further enqueue happens.
func (p *socketPeer) shutdownWriter() {
	p.stopOnce.Do(func() {
		close(p.done)
	})
}

// enqueue attempts a non-blocking send to the device's queue. Returns true if
// accepted, false if the queue is full (caller should disconnect the device).
// Durable mailbox epochs are persisted separately; connection-scoped frames
// are intentionally dropped after disconnect. Enforcing both bounds here
// guarantees the queue can't grow unbounded.
func (p *socketPeer) enqueue(payload []byte) bool {
	if p.sendCh == nil {
		// Peer not queue-enabled (e.g. a bridge peer); fall back to sync write.
		return p.write(payload) == nil
	}
	// Byte bound check before enqueue (frames-count bound enforced by chan cap).
	if atomic.LoadInt64(&p.queueBytes)+int64(len(payload)) > perDeviceSendQueueBytes {
		atomic.AddInt64(&p.drops, 1)
		return false
	}
	select {
	case p.sendCh <- payload:
		atomic.AddInt64(&p.queueBytes, int64(len(payload)))
		return true
	default:
		atomic.AddInt64(&p.drops, 1)
		return false
	}
}

type envelopeHeader struct {
	RouteID       string `json:"routeId"`
	SenderID      string `json:"senderId"`
	DestinationID string `json:"destinationId"`
	KeyEpochID    string `json:"keyEpochId"`
}

// handshakeHeader 用于路由非 envelope 格式的在线握手消息。
// 握手消息携带 deviceId，relay 仅按连接中的 device 路由，不读取握手内容。
type handshakeHeader struct {
	Type     string `json:"type"`
	DeviceID string `json:"deviceId"`
}

func NewServer(store *Store, config Config, logger *slog.Logger) (*Server, error) {
	if len(config.ProvisionTokenDigest) != 32 {
		return nil, fmt.Errorf("provision token digest is required")
	}
	if config.MailboxTTL <= 0 {
		config.MailboxTTL = 24 * time.Hour
	}
	if config.MaxMailboxBytes <= 0 {
		config.MaxMailboxBytes = 50 << 20
	}
	if config.MaxFrameBytes <= 0 {
		// 32MB: 去分页后单次全量 get_session_messages 响应可达数 MB（加密+base64 后膨胀约 25%）。
		// 2MB 会让长 session 全量响应撑爆帧上限 → close 1009 断连循环。32MB 容纳 ~25MB 业务
		// payload，覆盖所有实测 session 并为更大的 session 留余量。
		config.MaxFrameBytes = 32 << 20
	}
	if config.RateLimitPerMinute <= 0 {
		config.RateLimitPerMinute = 120
	}
	if config.ActivationRateLimitPerMinute <= 0 {
		config.ActivationRateLimitPerMinute = 6
	}
	if logger == nil {
		logger = slog.Default()
	}
	trustedProxyNets, err := parseTrustedProxyCIDRs(config.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	return &Server{
		store:             store,
		config:            config,
		logger:            logger,
		limiter:           NewRateLimiter(config.RateLimitPerMinute, time.Minute),
		activationLimiter: NewRateLimiter(config.ActivationRateLimitPerMinute, time.Minute),
		nonces:            newActivationNonceCache(),
		trustedProxyNets:  trustedProxyNets,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
		bridges: make(map[string]*socketPeer),
		devices: make(map[string]*socketPeer),
	}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status := http.StatusNotFound
	defer func() {
		s.logger.Info("relay request",
			"method", r.Method,
			"path", requestAuditPath(r.URL.Path),
			"status", status,
			"remote", s.clientIP(r),
			"duration_ms", time.Since(start).Milliseconds())
	}()
	parts := strings.FieldsFunc(r.URL.Path, func(value rune) bool { return value == '/' })
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		status = http.StatusOK
		writeJSON(w, status, map[string]string{"status": "ok"})
	case r.Method == http.MethodGet && r.URL.Path == "/readyz":
		if err := s.store.Ping(r.Context()); err != nil {
			status = http.StatusServiceUnavailable
			writeError(w, status, "relay.database_unavailable")
			return
		}
		status = http.StatusOK
		writeJSON(w, status, map[string]string{"status": "ready"})
	case r.Method == http.MethodPost && pathEquals(parts, "v1", "routes", "register"):
		status = s.handleRegisterRoute(w, r)
	case r.Method == http.MethodPost && pathEquals(parts, "v1", "activations", "routes"):
		status = s.handleActivateRoute(w, r)
	case r.Method == http.MethodPost && len(parts) == 5 && pathPrefix(parts, "v1", "routes") && parts[3] == "devices" && parts[4] == "register":
		status = s.handleRegisterDevice(w, r, parts[2])
	case r.Method == http.MethodGet && len(parts) == 4 && pathPrefix(parts, "v1", "routes") && parts[3] == "status":
		status = s.handleStatus(w, r, parts[2])
	case r.Method == http.MethodGet && len(parts) == 4 && pathPrefix(parts, "v1", "routes") && parts[3] == "bridge":
		status = s.handleBridgeSocket(w, r, parts[2])
	case len(parts) == 6 && pathPrefix(parts, "v1", "routes") && parts[3] == "devices" && parts[5] == "revoke" && r.Method == http.MethodPost:
		status = s.handleRevokeDevice(w, r, parts[2], parts[4])
	case len(parts) == 6 && pathPrefix(parts, "v1", "routes") && parts[3] == "devices" && parts[5] == "mailbox" && r.Method == http.MethodGet:
		status = s.handleFetchMailbox(w, r, parts[2], parts[4])
	case len(parts) == 7 && pathPrefix(parts, "v1", "routes") && parts[3] == "devices" && parts[5] == "mailbox" && parts[6] == "ack" && r.Method == http.MethodPost:
		status = s.handleAckMailbox(w, r, parts[2], parts[4])
	case len(parts) == 5 && pathPrefix(parts, "v1", "routes") && parts[3] == "devices" && r.Method == http.MethodGet:
		status = s.handleDeviceSocket(w, r, parts[2], parts[4])
	case r.Method == http.MethodPost && len(parts) == 4 && pathPrefix(parts, "v1", "routes") && parts[3] == "pairing-claims":
		status = s.handleSubmitPairingClaim(w, r, parts[2])
	case r.Method == http.MethodGet && len(parts) == 4 && pathPrefix(parts, "v1", "routes") && parts[3] == "pairing-claims":
		status = s.handleListPairingClaims(w, r, parts[2])
	case r.Method == http.MethodPost && len(parts) == 6 && pathPrefix(parts, "v1", "routes") && parts[3] == "pairing-claims" && parts[5] == "complete":
		status = s.handleCompletePairingClaim(w, r, parts[2], parts[4])
	case r.Method == http.MethodGet && len(parts) == 6 && pathPrefix(parts, "v1", "routes") && parts[3] == "pairing-claims" && parts[5] == "result":
		status = s.handleGetPairingResult(w, r, parts[2], parts[4])
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleActivateRoute(w http.ResponseWriter, r *http.Request) int {
	if !s.activationLimiter.Allow("activation:ip:"+s.clientIP(r), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	var input struct {
		InstallID  string `json:"installId"`
		PublicKey  string `json:"publicKey"`
		BridgeAuth string `json:"bridgeAuth"`
		Timestamp  int64  `json:"timestamp"`
		Nonce      string `json:"nonce"`
		Signature  string `json:"signature"`
	}
	if err := decodeJSONBody(r, &input); err != nil ||
		!validID(input.InstallID) || !validID(input.Nonce) ||
		len(input.BridgeAuth) < 32 || len(input.BridgeAuth) > 128 ||
		time.Since(time.Unix(input.Timestamp, 0)) > 5*time.Minute ||
		time.Until(time.Unix(input.Timestamp, 0)) > 5*time.Minute {
		writeError(w, http.StatusBadRequest, "relay.invalid_activation")
		return http.StatusBadRequest
	}
	if !s.activationLimiter.Allow("activation:install:"+input.InstallID, time.Now()) {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	publicKey, err := base64.StdEncoding.DecodeString(input.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		writeError(w, http.StatusBadRequest, "relay.invalid_activation")
		return http.StatusBadRequest
	}
	signature, err := base64.StdEncoding.DecodeString(input.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), activationPayload(
		input.InstallID, input.PublicKey, input.BridgeAuth, input.Timestamp, input.Nonce,
	), signature) {
		writeError(w, http.StatusUnauthorized, "relay.activation_signature_failed")
		return http.StatusUnauthorized
	}
	// P2-4：nonce 一次性语义。签名验证通过后再记录，避免无效签名消耗 nonce 造成 DoS。
	if s.activationNonceSeen(input.InstallID, input.Nonce, time.Now()) {
		writeError(w, http.StatusConflict, "relay.activation_replayed")
		return http.StatusConflict
	}
	routeID, err := s.store.ActivateRoute(r.Context(), input.InstallID, publicKey, input.BridgeAuth, time.Now())
	if err != nil {
		s.logger.Warn("relay activation failed", "install", safeID(input.InstallID), "remote", s.clientIP(r), "error", err)
		writeError(w, http.StatusConflict, "relay.activation_conflict")
		return http.StatusConflict
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"routeId": routeID, "relayEndpoint": s.config.PublicEndpoint,
	})
	return http.StatusCreated
}

func activationPayload(installID, publicKey, bridgeAuth string, timestamp int64, nonce string) []byte {
	return []byte(strings.Join([]string{
		"cordcode-relay/activation/v1",
		installID,
		publicKey,
		bridgeAuth,
		strconv.FormatInt(timestamp, 10),
		nonce,
	}, "\n"))
}

func (s *Server) handleRegisterRoute(w http.ResponseWriter, r *http.Request) int {
	if !s.allow(r, "route_register") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !hmac.Equal(s.config.ProvisionTokenDigest, CredentialDigest(bearer(r))) {
		s.logger.Warn("relay auth failed", "operation", "route_register", "remote", s.clientIP(r))
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	routeID, bridgeAuth, err := s.store.CreateRoute(r.Context(), time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "relay.register_failed")
		return http.StatusInternalServerError
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"routeId": routeID, "bridgeAuth": bridgeAuth, "relayEndpoint": s.config.PublicEndpoint,
	})
	return http.StatusCreated
}

func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request, routeID string) int {
	if !s.allow(r, "device_register") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateBridge(r, routeID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	var input struct {
		DeviceID string `json:"deviceId"`
	}
	if err := decodeJSONBody(r, &input); err != nil || !validID(input.DeviceID) {
		writeError(w, http.StatusBadRequest, "relay.invalid_device")
		return http.StatusBadRequest
	}
	auth, err := s.store.RegisterDevice(r.Context(), routeID, input.DeviceID, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "relay.register_failed")
		return http.StatusBadRequest
	}
	writeJSON(w, http.StatusCreated, map[string]string{"deviceId": input.DeviceID, "deviceAuth": auth})
	return http.StatusCreated
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, routeID string) int {
	if !s.allow(r, "route_status") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateBridge(r, routeID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	status, err := s.store.Status(r.Context(), routeID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "relay.status_failed")
		return http.StatusInternalServerError
	}
	writeJSON(w, http.StatusOK, status)
	return http.StatusOK
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request, routeID, deviceID string) int {
	if !s.allow(r, "device_revoke") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateBridge(r, routeID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	if err := s.store.RevokeDevice(r.Context(), routeID, deviceID, time.Now()); err != nil {
		writeError(w, http.StatusNotFound, "relay.device_not_found")
		return http.StatusNotFound
	}
	s.disconnectDevice(routeID, deviceID)
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
	return http.StatusOK
}

func (s *Server) handleFetchMailbox(w http.ResponseWriter, r *http.Request, routeID, deviceID string) int {
	if !s.allow(r, "mailbox_fetch") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateDevice(r, routeID, deviceID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	frames, err := s.store.FetchFrames(r.Context(), routeID, deviceID, after, limit, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "relay.mailbox_failed")
		return http.StatusInternalServerError
	}
	writeJSON(w, http.StatusOK, map[string]any{"frames": frames})
	return http.StatusOK
}

func (s *Server) handleAckMailbox(w http.ResponseWriter, r *http.Request, routeID, deviceID string) int {
	if !s.allow(r, "mailbox_ack") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateDevice(r, routeID, deviceID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	var input struct {
		Through uint64 `json:"through"`
	}
	if err := decodeJSONBody(r, &input); err != nil || input.Through == 0 {
		writeError(w, http.StatusBadRequest, "relay.invalid_ack")
		return http.StatusBadRequest
	}
	if err := s.store.AckFrames(r.Context(), routeID, deviceID, input.Through, time.Now()); err != nil {
		writeError(w, http.StatusInternalServerError, "relay.mailbox_failed")
		return http.StatusInternalServerError
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"ackedThrough": input.Through})
	return http.StatusOK
}

func (s *Server) handleSubmitPairingClaim(w http.ResponseWriter, r *http.Request, routeID string) int {
	if !s.allow(r, "pairing_submit") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	var input struct {
		ClaimID     string `json:"claimId"`
		Capability  string `json:"capability"`
		SealedClaim []byte `json:"sealedClaim"`
	}
	if err := decodeJSONBody(r, &input); err != nil || input.ClaimID == "" || input.Capability == "" || len(input.SealedClaim) == 0 {
		writeError(w, http.StatusBadRequest, "relay.invalid_pairing_claim")
		return http.StatusBadRequest
	}
	if !validID(input.ClaimID) {
		writeError(w, http.StatusBadRequest, "relay.invalid_pairing_claim")
		return http.StatusBadRequest
	}
	capHash := CredentialDigest(input.Capability)
	if err := s.store.SubmitPairingClaim(r.Context(), routeID, input.ClaimID, capHash, input.SealedClaim, time.Now(), 5*time.Minute); err != nil {
		if errors.Is(err, ErrPairingClaimConflict) {
			writeError(w, http.StatusConflict, "relay.pairing_claim_conflict")
			return http.StatusConflict
		}
		writeError(w, http.StatusInternalServerError, "relay.pairing_failed")
		return http.StatusInternalServerError
	}
	writeJSON(w, http.StatusOK, map[string]string{"claimId": input.ClaimID, "state": "pending"})
	return http.StatusOK
}

func (s *Server) handleListPairingClaims(w http.ResponseWriter, r *http.Request, routeID string) int {
	if !s.allow(r, "pairing_list") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateBridge(r, routeID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	claims, err := s.store.PendingClaims(r.Context(), routeID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "relay.pairing_failed")
		return http.StatusInternalServerError
	}
	if claims == nil {
		claims = []PairingClaim{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"claims": claims})
	return http.StatusOK
}

func (s *Server) handleCompletePairingClaim(w http.ResponseWriter, r *http.Request, routeID, claimID string) int {
	if !s.allow(r, "pairing_complete") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateBridge(r, routeID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	var input struct {
		State        string `json:"state"`
		SealedResult []byte `json:"sealedResult"`
	}
	if err := decodeJSONBody(r, &input); err != nil || (input.State != "approved" && input.State != "rejected") {
		writeError(w, http.StatusBadRequest, "relay.invalid_pairing_complete")
		return http.StatusBadRequest
	}
	if err := s.store.CompletePairingClaim(r.Context(), routeID, claimID, input.State, input.SealedResult, time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, "relay.pairing_not_found")
		return http.StatusBadRequest
	}
	writeJSON(w, http.StatusOK, map[string]string{"claimId": claimID, "state": input.State})
	return http.StatusOK
}

func (s *Server) handleGetPairingResult(w http.ResponseWriter, r *http.Request, routeID, claimID string) int {
	if !s.allow(r, "pairing_result") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	capability := bearer(r)
	if capability == "" {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	if !s.store.VerifyPairingCapability(r.Context(), routeID, claimID, capability) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	claim, err := s.store.GetPairingResult(r.Context(), routeID, claimID, time.Now())
	if err != nil {
		writeError(w, http.StatusNotFound, "relay.pairing_not_found")
		return http.StatusNotFound
	}
	if claim.State == "approved" || claim.State == "rejected" {
		_ = s.store.ConsumePairingResult(r.Context(), routeID, claimID, time.Now())
	}
	writeJSON(w, http.StatusOK, claim)
	return http.StatusOK
}

func (s *Server) handleBridgeSocket(w http.ResponseWriter, r *http.Request, routeID string) int {
	if !s.allow(r, "bridge_socket") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateBridge(r, routeID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return http.StatusBadRequest
	}
	conn.SetReadLimit(s.config.MaxFrameBytes)
	peer := &socketPeer{conn: conn}
	s.setBridge(routeID, peer)
	defer s.removeBridge(routeID, peer)
	s.readBridgeFrames(r.Context(), routeID, peer)
	return http.StatusSwitchingProtocols
}

func (s *Server) handleDeviceSocket(w http.ResponseWriter, r *http.Request, routeID, deviceID string) int {
	if !s.allow(r, "device_socket") {
		writeError(w, http.StatusTooManyRequests, "relay.rate_limited")
		return http.StatusTooManyRequests
	}
	if !s.authenticateDevice(r, routeID, deviceID) {
		writeError(w, http.StatusUnauthorized, "relay.auth_failed")
		return http.StatusUnauthorized
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return http.StatusBadRequest
	}
	conn.SetReadLimit(s.config.MaxFrameBytes)
	// Device peer gets a bounded send queue + writer goroutine so a slow
	// device cannot block the bridge read loop's delivery to other devices on
	// the same route (head-of-line blocking). The bridge peer stays sync-write
	// (there is exactly one bridge per route; no cross-device blocking).
	peer := &socketPeer{
		conn:   conn,
		sendCh: make(chan []byte, perDeviceSendQueueFrames),
		done:   make(chan struct{}),
	}
	peer.startWriter()
	defer func() {
		peer.shutdownWriter()
		s.removeDevice(routeID, deviceID, peer)
	}()
	s.setDevice(routeID, deviceID, peer)
	s.readDeviceFrames(r.Context(), routeID, deviceID, peer)
	return http.StatusSwitchingProtocols
}

func (s *Server) readBridgeFrames(ctx context.Context, routeID string, peer *socketPeer) {
	peer.setReadKeepalive()
	for {
		// 每次读前重置读 deadline（90s idle 判死）；收到 bridge ping 时 ping handler 也会重置。
		if err := peer.conn.SetReadDeadline(time.Now().Add(relayReadDeadline)); err != nil {
			return
		}
		_, payload, err := peer.conn.ReadMessage()
		if err != nil {
			return
		}

		// 握手响应不是 envelope 格式，但仍必须只发给目标 device。非阻塞 enqueue：
		// 慢 device 不阻塞同 route 其他 device（消除队头阻塞）。enqueue 满则断开该 device。
		var hs handshakeHeader
		if json.Unmarshal(payload, &hs) == nil && isBridgeHandshakeResponse(hs.Type) &&
			validID(hs.DeviceID) && s.store.DeviceActive(ctx, routeID, hs.DeviceID) {
			if target := s.device(routeID, hs.DeviceID); target != nil {
				if target.enqueue(payload) {
					continue
				}
				// Queue full → disconnect slow device; handshake response is not
				// mailbox-eligible (it is connection-scoped), so just drop after disconnect.
				s.logger.Warn("relay device send queue full, disconnecting", "route", safeID(routeID), "device", safeID(hs.DeviceID), "op", "handshake")
				s.removeDevice(routeID, hs.DeviceID, target)
				s.closePeer(target, websocket.CloseTryAgainLater, "send queue full")
			}
			continue
		}

		var envelope envelopeHeader
		if json.Unmarshal(payload, &envelope) != nil || envelope.RouteID != routeID || envelope.SenderID != "bridge" || !validID(envelope.DestinationID) || !s.store.DeviceActive(ctx, routeID, envelope.DestinationID) {
			s.closePeer(peer, websocket.ClosePolicyViolation, "invalid relay envelope")
			return
		}
		// Connection-scoped frames are never durable mailbox input. They may be
		// delivered only to an active destination socket; if that socket is absent
		// or its queue is full, drop the frame. Persisting an online envelope here
		// would later make mailbox replay reject its required epoch metadata.
		if !strings.HasPrefix(envelope.KeyEpochID, "mailbox:") {
			if target := s.device(routeID, envelope.DestinationID); target != nil {
				if target.enqueue(payload) {
					continue
				}
				s.logger.Warn("relay device send queue full, disconnecting", "route", safeID(routeID), "device", safeID(envelope.DestinationID), "drops", atomic.LoadInt64(&target.drops))
				s.removeDevice(routeID, envelope.DestinationID, target)
				s.closePeer(target, websocket.CloseTryAgainLater, "send queue full")
			}
			s.logger.Warn("relay dropped connection-scoped frame for unavailable device", "route", safeID(routeID), "device", safeID(envelope.DestinationID))
			continue
		}
		if _, evicted, err := s.store.AppendFrame(ctx, routeID, envelope.DestinationID, payload, time.Now(), s.config.MailboxTTL, s.config.MaxMailboxBytes); err != nil {
			s.logger.Warn("relay mailbox append failed", "route", safeID(routeID), "device", safeID(envelope.DestinationID), "error", err)
			s.closePeer(peer, websocket.CloseTryAgainLater, "mailbox unavailable")
			return
		} else if evicted > 0 {
			s.logger.Warn("relay mailbox capacity eviction", "route", safeID(routeID), "device", safeID(envelope.DestinationID), "evicted", evicted)
		}
	}
}

func (s *Server) readDeviceFrames(_ context.Context, routeID, deviceID string, peer *socketPeer) {
	peer.setReadKeepalive()
	for {
		// 每次读前重置读 deadline（90s idle 判死）；收到 device ping 时 ping handler 也会重置。
		if err := peer.conn.SetReadDeadline(time.Now().Add(relayReadDeadline)); err != nil {
			return
		}
		_, payload, err := peer.conn.ReadMessage()
		if err != nil {
			return
		}

		// 握手请求不是 envelope 格式，校验其 deviceId 与已认证 socket 一致后透传。
		var hs handshakeHeader
		if json.Unmarshal(payload, &hs) == nil && hs.Type == "online_client_hello" && hs.DeviceID == deviceID {
			target := s.bridge(routeID)
			if target == nil || target.write(payload) != nil {
				s.closePeer(peer, websocket.CloseTryAgainLater, "bridge offline")
			}
			continue
		}

		var envelope envelopeHeader
		if json.Unmarshal(payload, &envelope) != nil || envelope.RouteID != routeID || envelope.SenderID != deviceID || envelope.DestinationID != "bridge" {
			s.closePeer(peer, websocket.ClosePolicyViolation, "invalid relay envelope")
			return
		}
		target := s.bridge(routeID)
		if target == nil || target.write(payload) != nil {
			s.closePeer(peer, websocket.CloseTryAgainLater, "bridge offline")
			return
		}
	}
}

// isBridgeHandshakeResponse 判断 bridge 发给 device 的非 envelope 握手响应。
func isBridgeHandshakeResponse(msgType string) bool {
	switch msgType {
	case "online_server_hello", "online_server_hello_error":
		return true
	}
	return false
}

func (p *socketPeer) write(payload []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	// 写 deadline 必须在持 p.writeMu 内紧贴 WriteMessage（gorilla 不允许同 conn 并发写），
	// 防转发写给半开 peer 卡死 writeMu（对称于 go-bridge 根因 A）。
	_ = p.conn.SetWriteDeadline(time.Now().Add(relayWriteDeadline))
	return p.conn.WriteMessage(websocket.TextMessage, payload)
}

// setReadKeepalive 设 ping handler：收到对端 ping 时重置读 deadline 并回 pong。
// 这样主动 ping 的一侧（Mac bridge 每 30s ping；device 视客户端实现）驱动双向保活，
// 健康但数据空闲的连接不会被 90s 读 deadline 误判死；而半开（无 ping 无数据）连接
// 在 relayReadDeadline 内被读循环判死。对齐 go-bridge 直连路径的 pong-handler-重置-deadline 模式。
func (p *socketPeer) setReadKeepalive() {
	// 在调用方 goroutine 读取 deadline 并捕获到闭包：避免 ping handler 子 goroutine
	// 直接读包级 var 与测试覆盖 var 产生 data race。
	deadline := relayReadDeadline
	p.conn.SetPingHandler(func(appData string) error {
		_ = p.conn.SetReadDeadline(time.Now().Add(deadline))
		// WriteControl 不需 writeMu（gorilla 允许其与 WriteMessage 并发）。
		return p.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
	})
}

func (s *Server) setBridge(routeID string, peer *socketPeer) {
	s.mu.Lock()
	old := s.bridges[routeID]
	s.bridges[routeID] = peer
	s.mu.Unlock()
	if old != nil {
		s.closePeer(old, websocket.CloseNormalClosure, "bridge replaced")
	}
}

func (s *Server) bridge(routeID string) *socketPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bridges[routeID]
}

func (s *Server) removeBridge(routeID string, peer *socketPeer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bridges[routeID] == peer {
		delete(s.bridges, routeID)
	}
}

func deviceKey(routeID, deviceID string) string { return routeID + "\x00" + deviceID }

func (s *Server) setDevice(routeID, deviceID string, peer *socketPeer) {
	key := deviceKey(routeID, deviceID)
	s.mu.Lock()
	old := s.devices[key]
	s.devices[key] = peer
	s.mu.Unlock()
	if old != nil {
		s.closePeer(old, websocket.CloseNormalClosure, "device replaced")
	}
}

func (s *Server) device(routeID, deviceID string) *socketPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devices[deviceKey(routeID, deviceID)]
}

func (s *Server) removeDevice(routeID, deviceID string, peer *socketPeer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := deviceKey(routeID, deviceID)
	if s.devices[key] == peer {
		delete(s.devices, key)
	}
}

func (s *Server) disconnectDevice(routeID, deviceID string) {
	if peer := s.device(routeID, deviceID); peer != nil {
		s.closePeer(peer, websocket.ClosePolicyViolation, "device revoked")
		s.removeDevice(routeID, deviceID, peer)
	}
}

func (s *Server) closePeer(peer *socketPeer, code int, reason string) {
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	_ = peer.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = peer.conn.Close()
}

func (s *Server) authenticateBridge(r *http.Request, routeID string) bool {
	ok := s.store.AuthenticateBridge(r.Context(), routeID, bearer(r), time.Now())
	if !ok {
		s.logger.Warn("relay auth failed", "operation", "bridge", "route", safeID(routeID), "remote", s.clientIP(r))
	}
	return ok
}

func (s *Server) authenticateDevice(r *http.Request, routeID, deviceID string) bool {
	ok := s.store.AuthenticateDevice(r.Context(), routeID, deviceID, bearer(r), time.Now())
	if !ok {
		s.logger.Warn("relay auth failed", "operation", "device", "route", safeID(routeID), "device", safeID(deviceID), "remote", s.clientIP(r))
	}
	return ok
}

func (s *Server) allow(r *http.Request, operation string) bool {
	now := time.Now()
	if !s.limiter.Allow(operation+":ip:"+s.clientIP(r), now) {
		return false
	}
	parts := strings.FieldsFunc(r.URL.Path, func(value rune) bool { return value == '/' })
	if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "routes" {
		return s.limiter.Allow(operation+":route:"+parts[2], now)
	}
	return true
}

func bearer(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if strings.HasPrefix(value, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return ""
}

func decodeJSONBody(r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func validID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func pathPrefix(parts []string, expected ...string) bool {
	if len(parts) < len(expected) {
		return false
	}
	for i, part := range expected {
		if parts[i] != part {
			return false
		}
	}
	return true
}

func pathEquals(parts []string, expected ...string) bool {
	return len(parts) == len(expected) && pathPrefix(parts, expected...)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func parseTrustedProxyCIDRs(values []string) ([]*net.IPNet, error) {
	if len(values) == 0 {
		values = []string{"127.0.0.0/8", "::1/128"}
	}
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", value, err)
		}
		result = append(result, network)
	}
	return result, nil
}

// activationNonceTTL 是 nonce 一次性语义的有效窗口（与时间戳窗口一致）。
const activationNonceTTL = 5 * time.Minute

// activationNonceCache 是进程内 (install_id, nonce) 重放保护缓存（P2-4）。
type activationNonceCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newActivationNonceCache() *activationNonceCache {
	return &activationNonceCache{seen: make(map[string]time.Time)}
}

// seenRecently 返回该 key 是否在 TTL 窗口内出现过，并（若未出现）记录之；惰性清理过期项。
func (c *activationNonceCache) seenRecently(key string, now time.Time, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := now.Add(-ttl)
	for k, at := range c.seen {
		if at.Before(cutoff) {
			delete(c.seen, k)
		}
	}
	if _, ok := c.seen[key]; ok {
		return true
	}
	c.seen[key] = now
	return false
}

// activationNonceSeen 包装 seenRecently，组合 key。
func (s *Server) activationNonceSeen(installID, nonce string, now time.Time) bool {
	return s.nonces.seenRecently(installID+":"+nonce, now, activationNonceTTL)
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.TrimSpace(host))
	if remoteIP != nil && ipInNetworks(remoteIP, s.trustedProxyNets) {
		value := strings.TrimSpace(strings.Split(r.Header.Get("CF-Connecting-IP"), ",")[0])
		if forwardedIP := net.ParseIP(value); forwardedIP != nil {
			return forwardedIP.String()
		}
	}
	if remoteIP != nil {
		return remoteIP.String()
	}
	return host
}

func ipInNetworks(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func requestAuditPath(path string) string {
	parts := strings.FieldsFunc(path, func(value rune) bool { return value == '/' })
	if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "routes" {
		parts[2] = ":route"
	}
	if len(parts) >= 5 && parts[3] == "devices" && parts[4] != "register" {
		parts[4] = ":device"
	}
	return "/" + strings.Join(parts, "/")
}

func safeID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}
