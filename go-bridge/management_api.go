package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// httpReadHeaderTimeout 是管理/主 HTTP server 的握手 header 读取超时（P2-1 slowloris 防护）。
// 用 var 而非 const：测试可覆盖为短值避免 10s 等待。
var httpReadHeaderTimeout = 10 * time.Second

// ── 管理 API 配置 ───────────────────────────────────────────────────────────
// ManagementConfig 包含创建 ManagementServer 所需的全部依赖。
type ManagementConfig struct {
	Handlers         *Handlers
	Token            string
	DataDir          *DataDir
	PairingStore     PairingSessionStore
	DeviceStore      TrustedDeviceStore
	BridgeID         string
	DisplayName      string
	LocalURL         string
	TailscaleURL     string
	RemoteURL        string
	IncludeTailscale bool
	IncludeRemote    bool
	RelayEndpoint    string
	RelayRouteID     string
	RelayCredential  string
	RelayConfigured  bool
	RelayEnabled     bool
	RelayIdentity    *RelayCryptoIdentity
	Agents           map[string]core.Agent
	CodexBackendMode string
	DetectionCfg     *AgentDetectionConfig
	TLSPin           *BridgeV1TLSPin
}

// ── 管理 API 服务器 ─────────────────────────────────────────────────────────
// ManagementServer 提供 /internal/* HTTP 管理端点，供 Mac App 本地控制。
type ManagementServer struct {
	cfg                         ManagementConfig
	startedAt                   time.Time
	shutdownCb                  func()
	shutdownOnce                sync.Once
	mgmtMux                     *http.ServeMux
	serverMu                    sync.Mutex
	httpServer                  *http.Server
	listener                    net.Listener
	dnMu                        sync.RWMutex // 保护 cfg.DisplayName 的并发读写
	agentMu                     sync.RWMutex
	agentDescriptors            []AgentProviderDescriptor
	agentDescriptorsInitialized bool
}

// NewManagementServer 创建管理 API 服务器实例。
func NewManagementServer(cfg ManagementConfig) *ManagementServer {
	return &ManagementServer{
		cfg:       cfg,
		startedAt: time.Now(),
	}
}

// SetShutdownCallback 设置优雅关停回调（由 main 注入）。
func (s *ManagementServer) SetShutdownCallback(cb func()) {
	s.shutdownCb = cb
}

// DisplayName 返回当前 display name（供 main.go 传递给 Server）。
func (s *ManagementServer) DisplayName() string {
	s.dnMu.RLock()
	defer s.dnMu.RUnlock()
	return s.cfg.DisplayName
}

// SetDisplayName 更新 display name 并持久化到 data dir。
func (s *ManagementServer) SetDisplayName(name string) {
	s.dnMu.Lock()
	s.cfg.DisplayName = name
	s.dnMu.Unlock()
	if s.cfg.DataDir != nil {
		saveDisplayNameToDir(s.cfg.DataDir, name)
	}
}

// remoteURL 返回当前配置的远程 URL，供 pairing_complete 推送给 iOS 端。
func (s *ManagementServer) remoteURL() *string {
	urls := s.remoteURLs()
	if len(urls) == 0 {
		return nil
	}
	return &urls[0]
}

func (s *ManagementServer) remoteURLs() []string {
	var urls []string
	if s.cfg.IncludeTailscale {
		urls = append(urls, s.cfg.TailscaleURL)
	}
	if s.cfg.IncludeRemote {
		urls = append(urls, s.cfg.RemoteURL)
	}
	return uniqueNonEmptyStrings(urls)
}

// Start 在指定 host:port 上启动 HTTP 管理 API。
// port=0 时让操作系统自动分配端口，并通过返回值告知实际端口。
func (s *ManagementServer) Start(host string, port int) (actualPort int, err error) {
	s.mgmtMux = http.NewServeMux()
	s.mgmtMux.Handle("/", s)

	addr := fmt.Sprintf("%s:%d", host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("管理 API 监听失败: %w", err)
	}

	actualPort = listener.Addr().(*net.TCPAddr).Port

	// P2-1: 管理 API（loopback）也设握手超时与 header 上限，防止 slow header 耗尽连接。
	httpServer := &http.Server{
		Handler:           s.mgmtMux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	s.serverMu.Lock()
	s.httpServer = httpServer
	s.listener = listener
	s.serverMu.Unlock()

	go func() {
		slog.Info("go-bridge: 管理 API 启动", "addr", fmt.Sprintf("%s:%d", host, actualPort))
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("go-bridge: 管理 API 异常退出", "error", err)
		}
	}()

	return actualPort, nil
}

// Shutdown 优雅关闭管理 API（P2-1：也便于测试清理，避免 goroutine 泄漏）。
func (s *ManagementServer) Shutdown() {
	s.serverMu.Lock()
	srv := s.httpServer
	s.serverMu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// ServeHTTP 路由 /internal/* 请求到对应处理函数。
func (s *ManagementServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}

	path := r.URL.Path
	switch {
	case path == "/internal/status" && r.Method == http.MethodGet:
		s.handleStatus(w, r)
	case path == "/internal/agents" && r.Method == http.MethodGet:
		s.handleAgents(w, r)
	case path == "/internal/agents/refresh" && r.Method == http.MethodPost:
		s.handleAgentsRefresh(w, r)
	case strings.HasPrefix(path, "/internal/agents/") && strings.HasSuffix(path, "/test") && r.Method == http.MethodPost:
		s.handleAgentTest(w, r)
	case path == "/internal/shutdown" && r.Method == http.MethodPost:
		s.handleShutdown(w, r)
	case path == "/internal/devices" && r.Method == http.MethodGet:
		s.handleListDevices(w, r)
	case strings.HasPrefix(path, "/internal/devices/") && strings.HasSuffix(path, "/revoke") && r.Method == http.MethodPost:
		s.handleRevokeDevice(w, r)
	case path == "/internal/pairing/create" && r.Method == http.MethodPost:
		s.handlePairingCreate(w, r)
	case strings.HasPrefix(path, "/internal/pairing/pair_") && r.Method == http.MethodGet:
		s.handlePairingGet(w, r)
	case strings.HasPrefix(path, "/internal/pairing/") && strings.HasSuffix(path, "/approve") && r.Method == http.MethodPost:
		s.handlePairingApprove(w, r)
	case strings.HasPrefix(path, "/internal/pairing/") && strings.HasSuffix(path, "/reject") && r.Method == http.MethodPost:
		s.handlePairingReject(w, r)
	case path == "/internal/logs/recent" && r.Method == http.MethodGet:
		s.handleLogsRecent(w, r)
	case path == "/internal/settings/display-name" && r.Method == http.MethodGet:
		s.handleGetDisplayName(w, r)
	case path == "/internal/settings/display-name" && r.Method == http.MethodPut:
		s.handleSetDisplayName(w, r)
	case path == "/internal/remote/status" && r.Method == http.MethodGet:
		s.handleRemoteStatus(w, r)
	case path == "/internal/relay/delivery-prekeys" && r.Method == http.MethodGet:
		s.handleRelayDeliveryPrekeys(w, r)
	default:
		writeMgmtJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": fmt.Sprintf("端点 %s %s 不存在", r.Method, path),
		})
	}
}

// ── 认证中间件 ───────────────────────────────────────────────────────────────
// checkAuth 校验 Authorization: Bearer <token>，不匹配返回 401。
func (s *ManagementServer) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeMgmtJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error":   "unauthorized",
			"message": "缺少 Authorization 头",
		})
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token != s.cfg.Token {
		writeMgmtJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error":   "unauthorized",
			"message": "token 不匹配",
		})
		return false
	}
	return true
}

// ── GET /internal/status ─────────────────────────────────────────────────────
func (s *ManagementServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status := "ready"
	if len(s.cfg.Agents) == 0 {
		status = "ready_no_agents"
	}
	s.dnMu.RLock()
	displayName := s.cfg.DisplayName
	s.dnMu.RUnlock()
	writeMgmtJSON(w, http.StatusOK, map[string]interface{}{
		"status":      status,
		"bridgeId":    s.cfg.BridgeID,
		"displayName": displayName,
		"uptime":      time.Since(s.startedAt).String(),
		"version":     runtimeVersion,
	})
}

// ── GET /internal/agents ─────────────────────────────────────────────────────
func (s *ManagementServer) handleAgents(w http.ResponseWriter, _ *http.Request) {
	writeMgmtJSON(w, http.StatusOK, s.cachedAgentDescriptors())
}

// ── POST /internal/agents/refresh ────────────────────────────────────────────
// 手动触发重新检测所有 agent 状态。
func (s *ManagementServer) handleAgentsRefresh(w http.ResponseWriter, _ *http.Request) {
	descs := BuildAllAgentDescriptors(s.cfg.Agents, s.cfg.CodexBackendMode, s.cfg.DetectionCfg)
	s.agentMu.Lock()
	s.agentDescriptors = append([]AgentProviderDescriptor(nil), descs...)
	s.agentDescriptorsInitialized = true
	s.agentMu.Unlock()
	writeMgmtJSON(w, http.StatusOK, descs)
}

// ── POST /internal/agents/{id}/test ──────────────────────────────────────────
// 测试指定后端的连通性。
func (s *ManagementServer) handleAgentTest(w http.ResponseWriter, r *http.Request) {
	agentID := extractPathSegment(r.URL.Path, "/internal/agents/", "/test")
	agent, ok := s.cfg.Agents[agentID]
	if !ok {
		writeMgmtJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": fmt.Sprintf("agent %s not found", agentID),
		})
		return
	}
	// BuildAgentDescriptor 内部已调用 detectAgentStatus，无需再额外调用
	desc := BuildAgentDescriptor(agentID, agent, s.cfg.CodexBackendMode, s.cfg.DetectionCfg)
	s.updateAgentDescriptor(desc)
	writeMgmtJSON(w, http.StatusOK, desc)
}

func (s *ManagementServer) updateAgentDescriptor(desc AgentProviderDescriptor) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	next := append([]AgentProviderDescriptor(nil), s.agentDescriptors...)
	for i := range next {
		if next[i].ID == desc.ID {
			next[i] = desc
			s.agentDescriptors = next
			s.agentDescriptorsInitialized = true
			return
		}
	}
	next = append(next, desc)
	s.agentDescriptors = next
	s.agentDescriptorsInitialized = true
}

func (s *ManagementServer) cachedAgentDescriptors() []AgentProviderDescriptor {
	s.agentMu.RLock()
	if s.agentDescriptorsInitialized {
		descs := append([]AgentProviderDescriptor(nil), s.agentDescriptors...)
		s.agentMu.RUnlock()
		return descs
	}
	s.agentMu.RUnlock()

	descs := BuildAllAgentDescriptors(s.cfg.Agents, s.cfg.CodexBackendMode, s.cfg.DetectionCfg)

	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if !s.agentDescriptorsInitialized {
		s.agentDescriptors = append([]AgentProviderDescriptor(nil), descs...)
		s.agentDescriptorsInitialized = true
		return descs
	}
	return append([]AgentProviderDescriptor(nil), s.agentDescriptors...)
}

// ── POST /internal/shutdown ──────────────────────────────────────────────────
func (s *ManagementServer) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	writeMgmtJSON(w, http.StatusOK, map[string]interface{}{"shutting_down": true})
	if s.shutdownCb != nil {
		go s.shutdownOnce.Do(s.shutdownCb)
	}
}

// ── GET /internal/devices ────────────────────────────────────────────────────
func (s *ManagementServer) handleListDevices(w http.ResponseWriter, _ *http.Request) {
	devices, err := s.cfg.DeviceStore.ListDevices()
	if err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "list_failed",
			"message": err.Error(),
		})
		return
	}
	if devices == nil {
		devices = []TrustedDeviceRecord{}
	}
	writeMgmtJSON(w, http.StatusOK, devices)
}

// ── POST /internal/devices/{id}/revoke ───────────────────────────────────────
func (s *ManagementServer) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := extractPathSegment(r.URL.Path, "/internal/devices/", "/revoke")
	if deviceID == "" {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "missing_param",
			"message": "缺少 deviceId",
		})
		return
	}
	if err := s.cfg.DeviceStore.RevokeDevice(deviceID); err != nil {
		writeMgmtJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": err.Error(),
		})
		return
	}
	writeMgmtJSON(w, http.StatusOK, map[string]interface{}{"revoked": true, "deviceId": deviceID})

	// 主动断开被撤销设备的所有 WebSocket 连接
	globalDeviceConnRegistry.DisconnectDevice(deviceID)
}

// ── POST /internal/pairing/create ────────────────────────────────────────────
func (s *ManagementServer) handlePairingCreate(w http.ResponseWriter, _ *http.Request) {
	s.dnMu.RLock()
	displayName := s.cfg.DisplayName
	s.dnMu.RUnlock()
	session := NewPairingSessionWithRemoteURLs(
		s.cfg.BridgeID,
		displayName,
		s.cfg.LocalURL,
		s.remoteURLs(),
		5*time.Minute,
	)
	if s.cfg.RelayEnabled && s.cfg.RelayConfigured && s.cfg.RelayIdentity != nil {
		if err := addRelayFirstPairingPayload(session, s.cfg.RelayEndpoint, s.cfg.RelayRouteID, s.cfg.RelayIdentity); err != nil {
			writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error":   "relay_pairing_create_failed",
				"message": err.Error(),
			})
			return
		}
	}
	if err := s.cfg.PairingStore.Create(session); err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "create_failed",
			"message": err.Error(),
		})
		return
	}
	writeMgmtJSON(w, http.StatusOK, session)
}

// ── GET /internal/pairing/{id} ───────────────────────────────────────────────
func (s *ManagementServer) handlePairingGet(w http.ResponseWriter, r *http.Request) {
	pairID := strings.TrimPrefix(r.URL.Path, "/internal/pairing/")
	if pairID == "" {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "missing_param",
			"message": "缺少 pairing ID",
		})
		return
	}

	session, err := s.cfg.PairingStore.Get(pairID)
	if err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "get_failed",
			"message": err.Error(),
		})
		return
	}
	if session == nil {
		writeMgmtJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": "配对会话不存在",
		})
		return
	}
	if session.State == PairingCreated && s.cfg.RelayConfigured && s.cfg.RelayIdentity != nil {
		if err := s.syncRelayPairingClaim(r.Context(), session); err != nil {
			slog.Warn("relay-first: claim sync failed", "pairingID", session.ID, "error", err)
		}
	}

	writeMgmtJSON(w, http.StatusOK, session)
}

// ── POST /internal/pairing/{id}/approve ──────────────────────────────────────
func relayOrSessionDeviceID(session *PairingSession) string {
	if session.RelayClaim != nil && session.ClaimingDeviceID != "" {
		return session.ClaimingDeviceID
	}
	return session.DeviceID
}
func (s *ManagementServer) handlePairingApprove(w http.ResponseWriter, r *http.Request) {
	pairID := extractPathSegment(r.URL.Path, "/internal/pairing/", "/approve")
	if pairID == "" {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "missing_param",
			"message": "缺少 pairing ID",
		})
		return
	}

	session, err := s.cfg.PairingStore.Get(pairID)
	if err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "get_failed",
			"message": err.Error(),
		})
		return
	}
	if session == nil {
		writeMgmtJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": "配对会话不存在",
		})
		return
	}

	if err := session.Approve(); err != nil {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_state",
			"message": err.Error(),
		})
		return
	}
	if err := s.cfg.PairingStore.Update(session); err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "update_failed",
			"message": err.Error(),
		})
		return
	}

	// token 在 Complete 后会清空；direct 推送与 relay sealed result 都需先持有。
	deviceToken := session.DeviceToken

	// 写入受信设备记录
	deviceRecord := TrustedDeviceRecord{
		DeviceID:    relayOrSessionDeviceID(session),
		DisplayName: session.ClaimingDeviceName,
		Platform:    session.ClaimingPlatform,
		TokenHash:   session.DeviceTokenHash,
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	if session.RelayClaim != nil {
		deviceRecord.IdentityPublicKey = session.RelayClaim.DevicePubKey
		deviceRecord.RelayEnabled = true
		deviceRecord.RelayChannelGeneration = 1
	}
	replacedDeviceIDs, err := s.cfg.DeviceStore.ReplaceDevice(deviceRecord)
	if err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "device_persist_failed",
			"message": "受信设备持久化失败，配对中止: " + err.Error(),
		})
		return
	}
	if len(replacedDeviceIDs) > 0 {
		slog.Info("pairing: replaced previous device records",
			"deviceID", safeID(deviceRecord.DeviceID),
			"replaced", len(replacedDeviceIDs))
	}

	var relayResult *RelayFirstResult
	if session.RelayClaim != nil {
		result, err := s.approveRelayPairing(r.Context(), session, deviceToken)
		if err != nil {
			writeMgmtJSON(w, http.StatusBadGateway, map[string]interface{}{
				"error":   "relay_pairing_approve_failed",
				"message": err.Error(),
			})
			return
		}
		relayResult = result
	}

	writeMgmtJSON(w, http.StatusOK, map[string]interface{}{
		"pairingId":   pairID,
		"deviceId":    relayOrSessionDeviceID(session),
		"deviceToken": deviceToken,
		"state":       string(session.State),
	})

	if relayResult == nil {
		// direct 配对仍通过原 WebSocket 回送一次性 token。
		s.dnMu.RLock()
		displayName := s.cfg.DisplayName
		s.dnMu.RUnlock()
		push := PairingCompletePush{
			Type: "pairing_complete",
			Device: PairingCompleteDevice{
				DeviceID: session.DeviceID,
				Token:    deviceToken,
			},
			Bridge: PairingCompleteBridge{
				BridgeID:    s.cfg.BridgeID,
				DisplayName: displayName,
				LocalURL:    s.cfg.LocalURL,
				RemoteURL:   s.remoteURL(),
				RemoteURLs:  s.remoteURLs(),
				TLSPin:      s.cfg.TLSPin,
			},
		}
		globalPairingRegistry.NotifyComplete(pairID, push)
	}

	// 推送后标记会话为 completed，清空明文 token
	session.Complete()
	s.cfg.PairingStore.Update(session)
}

// ── POST /internal/pairing/{id}/reject ───────────────────────────────────────
func (s *ManagementServer) handlePairingReject(w http.ResponseWriter, r *http.Request) {
	pairID := extractPathSegment(r.URL.Path, "/internal/pairing/", "/reject")
	if pairID == "" {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "missing_param",
			"message": "缺少 pairing ID",
		})
		return
	}

	session, err := s.cfg.PairingStore.Get(pairID)
	if err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "get_failed",
			"message": err.Error(),
		})
		return
	}
	if session == nil {
		writeMgmtJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": "配对会话不存在",
		})
		return
	}

	if err := session.Reject(); err != nil {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_state",
			"message": err.Error(),
		})
		return
	}
	if err := s.cfg.PairingStore.Update(session); err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "update_failed",
			"message": err.Error(),
		})
		return
	}
	if session.RelayClaim != nil {
		if err := s.rejectRelayPairing(r.Context(), session); err != nil {
			writeMgmtJSON(w, http.StatusBadGateway, map[string]interface{}{
				"error":   "relay_pairing_reject_failed",
				"message": err.Error(),
			})
			return
		}
	}

	writeMgmtJSON(w, http.StatusOK, map[string]interface{}{
		"pairingId": pairID,
		"state":     string(session.State),
	})
}

// ── GET /internal/logs/recent ────────────────────────────────────────────────
func (s *ManagementServer) handleLogsRecent(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.DataDir == nil {
		writeMgmtJSON(w, http.StatusOK, []string{})
		return
	}

	logPath := filepath.Join(s.cfg.DataDir.Path(), "logs", "runtime.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		writeMgmtJSON(w, http.StatusOK, []string{})
		return
	}

	lines := readLastLines(data, 200)
	writeMgmtJSON(w, http.StatusOK, lines)
}

// ── GET /internal/settings/display-name ──────────────────────────────────────
func (s *ManagementServer) handleGetDisplayName(w http.ResponseWriter, _ *http.Request) {
	s.dnMu.RLock()
	displayName := s.cfg.DisplayName
	s.dnMu.RUnlock()
	writeMgmtJSON(w, http.StatusOK, map[string]interface{}{
		"displayName": displayName,
	})
}

// ── PUT /internal/settings/display-name ──────────────────────────────────────
func (s *ManagementServer) handleSetDisplayName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DisplayName == "" {
		writeMgmtJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "displayName 不能为空",
		})
		return
	}
	s.SetDisplayName(req.DisplayName)
	writeMgmtJSON(w, http.StatusOK, map[string]interface{}{
		"displayName": req.DisplayName,
	})
}

// ── GET /internal/remote/status ──────────────────────────────────────────────
// 返回远程连接诊断信息，只读取本机配置，不向外部发起探测。
func (s *ManagementServer) handleRemoteStatus(w http.ResponseWriter, _ *http.Request) {
	localURL := s.cfg.LocalURL
	tailscaleURL := s.cfg.TailscaleURL
	remoteURL := s.cfg.RemoteURL
	remoteURLs := s.remoteURLs()

	result := map[string]interface{}{
		"localURL":         localURL,
		"tailscaleURL":     tailscaleURL,
		"remoteURL":        remoteURL,
		"remoteURLs":       remoteURLs,
		"includeTailscale": s.cfg.IncludeTailscale,
		"includeRemote":    s.cfg.IncludeRemote,
		"relay": map[string]interface{}{
			"enabled":    s.cfg.RelayEnabled,
			"configured": s.cfg.RelayConfigured,
			"endpoint":   s.cfg.RelayEndpoint,
			"routeId":    s.cfg.RelayRouteID,
		},
		"listenStatus": map[string]interface{}{
			"localURL":  localURL,
			"listening": true,
		},
	}

	if len(remoteURLs) == 0 {
		result["connectionMode"] = "local_only"
		result["remoteConfigured"] = false
		result["remoteAnalysis"] = nil
		writeMgmtJSON(w, http.StatusOK, result)
		return
	}

	analysisURL := remoteURL
	if analysisURL == "" && len(remoteURLs) > 0 {
		analysisURL = remoteURLs[0]
	}
	analysis := classifyRemoteURL(analysisURL)
	result["connectionMode"] = "remote_configured"
	result["remoteConfigured"] = true
	result["remoteAnalysis"] = analysis

	writeMgmtJSON(w, http.StatusOK, result)
}

// ── GET /internal/relay/delivery-prekeys ───────────────────────────────────
// 返回当前进程内 delivery prekey 水位。只读诊断端点，不暴露 prekey 内容。
func (s *ManagementServer) handleRelayDeliveryPrekeys(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Handlers == nil || s.cfg.Handlers.deliveryPrekeys == nil {
		writeMgmtJSON(w, http.StatusOK, []map[string]interface{}{})
		return
	}
	devices, err := s.cfg.DeviceStore.ListDevices()
	if err != nil {
		writeMgmtJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "list_failed",
			"message": err.Error(),
		})
		return
	}
	result := make([]map[string]interface{}, 0, len(devices))
	for _, device := range devices {
		status := s.cfg.Handlers.deliveryPrekeys.GetPrekeyStatus(device.DeviceID)
		result = append(result, map[string]interface{}{
			"deviceId":               device.DeviceID,
			"displayName":            device.DisplayName,
			"platform":               device.Platform,
			"relayEnabled":           device.RelayEnabled,
			"revoked":                device.RevokedAt != nil,
			"availableCount":         status.AvailableCount,
			"lowWatermark":           status.LowWatermark,
			"targetCount":            status.TargetCount,
			"maxCount":               status.MaxCount,
			"relayChannelGeneration": device.RelayChannelGeneration,
		})
	}
	writeMgmtJSON(w, http.StatusOK, result)
}

// remoteURLAnalysis 是远程 URL 分类诊断结果。
type remoteURLAnalysis struct {
	Scheme           string `json:"scheme"`
	Host             string `json:"host"`
	HostCategory     string `json:"hostCategory"`
	IsTailscaleCGNAT bool   `json:"isTailscaleCGNAT"`
	IsPublicWS       bool   `json:"isPublicWS"`
	SecurityLevel    string `json:"securityLevel"`
}

// classifyRemoteURL 分析远程 URL 的安全分类。
// 不向外部地址发起网络请求。
func classifyRemoteURL(rawURL string) remoteURLAnalysis {
	var analysis remoteURLAnalysis

	u, err := url.Parse(rawURL)
	if err != nil {
		return remoteURLAnalysis{
			HostCategory:  "invalid",
			SecurityLevel: "unknown",
		}
	}

	analysis.Scheme = u.Scheme
	analysis.Host = u.Hostname()

	host := u.Hostname()
	if host == "" {
		analysis.HostCategory = "invalid"
		analysis.SecurityLevel = "unknown"
		return analysis
	}

	// localhost 是众所周知的 loopback 主机名
	if host == "localhost" {
		analysis.HostCategory = "loopback"
		analysis.IsTailscaleCGNAT = false
		analysis.IsPublicWS = false
	} else if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			analysis.HostCategory = "loopback"
			analysis.IsTailscaleCGNAT = false
			analysis.IsPublicWS = false
		} else if isTailscaleCGNAT(ip) {
			// 100.64.0.0/10 必须在 IsPrivate 之前检查
			analysis.HostCategory = "tailscale"
			analysis.IsTailscaleCGNAT = true
			analysis.IsPublicWS = false
		} else if ip.IsPrivate() {
			analysis.HostCategory = "private"
			analysis.IsTailscaleCGNAT = false
			analysis.IsPublicWS = false
		} else {
			analysis.HostCategory = "public"
			analysis.IsTailscaleCGNAT = false
			analysis.IsPublicWS = u.Scheme == "ws"
		}
	} else {
		// 域名：无法确定是否公网，保守标记
		analysis.HostCategory = "domain"
		analysis.IsTailscaleCGNAT = false
		// 域名 + ws:// 视为潜在不安全
		analysis.IsPublicWS = u.Scheme == "ws"
	}

	// 安全等级判定
	switch {
	case u.Scheme == "wss":
		analysis.SecurityLevel = "encrypted"
	case analysis.IsTailscaleCGNAT:
		analysis.SecurityLevel = "tailscale_tunnel"
	case analysis.HostCategory == "private" || analysis.HostCategory == "loopback":
		analysis.SecurityLevel = "lan"
	case analysis.HostCategory == "domain":
		analysis.SecurityLevel = "unknown"
	case analysis.IsPublicWS:
		analysis.SecurityLevel = "insecure"
	default:
		analysis.SecurityLevel = "unknown"
	}

	return analysis
}

// isTailscaleCGNAT 判断 IP 是否属于 100.64.0.0/10 (CGNAT 段)。
// Tailscale 默认使用该地址段。
func isTailscaleCGNAT(ip net.IP) bool {
	// 100.64.0.0/10 = 100.64.0.0 ~ 100.127.255.255
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// ── display name 持久化 ──────────────────────────────────────────────────────

// loadOrCreateDisplayName 从 data dir 加载 display name，首次启动时用 hostname 初始化。
func loadOrCreateDisplayName(dataDir *DataDir) string {
	if dataDir == nil {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "CordCode Link"
		}
		return hostname
	}
	path := filepath.Join(dataDir.Path(), "display-name.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var stored struct {
			DisplayName string `json:"displayName"`
		}
		if json.Unmarshal(data, &stored) == nil && stored.DisplayName != "" {
			return stored.DisplayName
		}
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "CordCode Link"
	}
	saveDisplayNameToDir(dataDir, hostname)
	return hostname
}

// saveDisplayNameToDir 将 display name 写入 data dir。
func saveDisplayNameToDir(dataDir *DataDir, name string) {
	if dataDir == nil {
		return
	}
	payload := struct {
		DisplayName string `json:"displayName"`
	}{DisplayName: name}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		slog.Error("go-bridge: display name 序列化失败", "error", err)
		return
	}
	path := filepath.Join(dataDir.Path(), "display-name.json")
	// 原子写 + 0600（P2-5）。
	if err := core.AtomicWriteFile(path, data, 0o600); err != nil {
		slog.Error("go-bridge: display name 写入失败", "error", err)
	}
}

// ── 辅助函数 ─────────────────────────────────────────────────────────────────

// writeMgmtJSON 写入 JSON 响应。
func writeMgmtJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// extractPathSegment 从路径中提取中间段。
// 例如: extractPathSegment("/internal/devices/dev_abc/revoke", "/internal/devices/", "/revoke") -> "dev_abc"
func extractPathSegment(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.TrimSuffix(trimmed, suffix)
	return trimmed
}

// readLastLines 从字节数据中提取最后 n 行。
// 先统计换行符总数确定总行数，再跳过前 (total-n) 行，只拷贝最后 n 行。
func readLastLines(data []byte, n int) []string {
	if len(data) == 0 {
		return []string{}
	}
	// 统计总行数
	totalLines := 1 // 至少一行
	for _, b := range data {
		if b == '\n' {
			totalLines++
		}
	}
	// 去掉末尾空行的影响
	if data[len(data)-1] == '\n' {
		totalLines--
	}

	// 跳过行数
	skip := totalLines - n
	if skip < 0 {
		skip = 0
	}

	// 扫描到 skip 行后开始收集
	var lines []string
	lineStart := 0
	currentLine := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			if currentLine >= skip {
				lines = append(lines, string(data[lineStart:i]))
			}
			currentLine++
			lineStart = i + 1
		}
	}
	// 最后一行（无末尾换行符）
	if lineStart < len(data) && currentLine >= skip {
		lines = append(lines, string(data[lineStart:]))
	}

	if len(lines) == 0 {
		return []string{}
	}
	return lines
}

// ensure ManagementServer satisfies http.Handler
var _ http.Handler = (*ManagementServer)(nil)
