package gobridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cccode-macbridge/core"
	"github.com/openAgi2/cccode-macbridge/transcriptindex"
)

var hiddenDirectoryBases = map[string]bool{
	"claudeprobe": true,
}

const claudeSessionSummaryReadLimit = 512 * 1024

type Handlers struct {
	mu                      sync.Mutex
	agents                  map[string]core.Agent
	sessions                *sessionRegistry
	opencodeSessionOptions  map[string]opencodeSessionOptions
	contentRefs             map[string]string
	contentRefOrder         []string
	seq                     int
	ocProxy                 *OpenCodeProxy
	codexBackendMode        string
	pendingNotifications    *PendingNotificationStore
	broadcaster             *Broadcaster
	relayRunning            map[string]bool // sessionID → relayEvents 是否正在运行
	deliveryPrekeys         *PrekeyStore
	observation             *ObservationManager
	relayOutbox             *OutboxManager
	presentation            *PresentationManager
	relayEventRouter        *RelayEventRouter
	relayEnvelopeSender     func(json.RawMessage) error
	trustedDevices          TrustedDeviceStore
	relayIdentity           *RelayCryptoIdentity
	relayUpgradeProvisioner RelayUpgradeProvisioner
	relayUpgradeMu          sync.Mutex
	bridgeID                string
	relayHelloHandler       func(conn Connection, msg *WireMessage)
	claudeSessions          *claudeSessionCatalog
	transcriptIndex         *transcriptindex.Store
}

type opencodeSessionOptions struct {
	model     string
	directory string
}

func NewHandlers() *Handlers {
	prekeys := NewPrekeyStore("")
	observation := NewObservationManager()
	outbox := NewOutboxManager(prekeys)
	presentation := NewPresentationManager()
	return &Handlers{
		agents:                 make(map[string]core.Agent),
		sessions:               newSessionRegistry(),
		opencodeSessionOptions: make(map[string]opencodeSessionOptions),
		contentRefs:            make(map[string]string),
		broadcaster:            NewBroadcaster(),
		pendingNotifications:   NewPendingNotificationStore(),
		relayRunning:           make(map[string]bool),
		deliveryPrekeys:        prekeys,
		observation:            observation,
		relayOutbox:            outbox,
		presentation:           presentation,
		relayEventRouter:       NewRelayEventRouter(observation, outbox, prekeys, NewMailboxService(NewRelayHub()), presentation),
		claudeSessions:         newDefaultClaudeSessionCatalog(),
		transcriptIndex:        transcriptindex.NewStore(defaultTranscriptIndexDir()),
	}
}

// SetTranscriptIndexBaseDir (re)creates the transcript page index store rooted
// at dir. Called by the server once the Bridge data directory is known so index
// files persist across restarts; when unset the store falls back to a default
// directory so pagination still works.
func (h *Handlers) SetTranscriptIndexBaseDir(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.transcriptIndex = transcriptindex.NewStore(dir)
}

// SetBridgeID 使 delivery 派生上下文绑定到 server 公布的真实 bridge identity。
func (h *Handlers) SetBridgeID(bridgeID string) {
	h.mu.Lock()
	h.bridgeID = bridgeID
	h.mu.Unlock()
	h.deliveryPrekeys.SetBridgeID(bridgeID)
}

// ConfigureRelayDelivery 接入 Mac→Relay 离线 milestone 投递路径。
func (h *Handlers) ConfigureRelayDelivery(routeID string, sender func(json.RawMessage) error) {
	h.mu.Lock()
	h.relayEnvelopeSender = sender
	h.mu.Unlock()
	h.relayEventRouter.SetRouteID(routeID)
	h.relayEventRouter.SetDeviceGenerationFunc(func(deviceID string) uint64 {
		h.mu.Lock()
		store := h.trustedDevices
		h.mu.Unlock()
		if store == nil {
			return 0
		}
		rec, err := store.LookupByDeviceID(deviceID)
		if err != nil || rec == nil || rec.RevokedAt != nil || !rec.RelayEnabled {
			return 0
		}
		return rec.RelayChannelGeneration
	})
}

// SetRelayHelloHandler 设置通过 relay 加密通道收到的 hello 消息处理回调。
// 由 Server 或 main.go 在初始化时设置，因为 hello_ack 需要 server 级别的状态
// （displayName, runtimeVersion, localURL, remoteURL 等）。
func (h *Handlers) SetRelayHelloHandler(fn func(conn Connection, msg *WireMessage)) {
	h.relayHelloHandler = fn
}

func (h *Handlers) Agents() map[string]core.Agent {
	return h.agents
}

func (h *Handlers) CodexBackendMode() string {
	return h.codexBackendMode
}

func (h *Handlers) RegisterOpenCodeProxy(p *OpenCodeProxy) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ocProxy = p
}

func (h *Handlers) SetCodexBackendMode(mode string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.codexBackendMode = normalizeCodexBackend(mode)
}

func (h *Handlers) RegisterAgent(id string, agent core.Agent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents[id] = agent
}

// session access helpers — bridge h.mu and sessionRegistry

func (h *Handlers) getSession(sessionID string) (core.AgentSession, bool) {
	t, ok := h.sessions.get(sessionID)
	if !ok {
		return nil, false
	}
	return t.session, true
}

func (h *Handlers) putSession(sessionID string, sess core.AgentSession) {
	h.sessions.putRaw(sessionID, sess)
}

func (h *Handlers) deleteSession(sessionID string) (core.AgentSession, bool) {
	return h.sessions.delete(sessionID)
}

func (h *Handlers) putSessionWithMeta(sessionID, backendID, directory string, sess core.AgentSession) {
	h.sessions.put(sessionID, backendID, directory, sess)
}

func (h *Handlers) StartCleanupLoop(interval time.Duration) {
	go func() {
		for range time.Tick(interval) {
			h.cleanupIdleSessions()
		}
	}()
}

func (h *Handlers) cleanupIdleSessions() {
	h.mu.Lock()
	var toClean []string
	h.sessions.forEach(func(sessionID string, t *trackedSession) {
		if t.state != sessionStateIdle {
			return
		}
		if strings.HasPrefix(sessionID, "pending-") {
			return
		}
		ttl := idleTTL(t.backendID)
		if time.Since(t.lastEventAt) > ttl {
			slog.Info("go-bridge: cleaning idle session", "sessionID", sessionID, "backendID", t.backendID, "idle", time.Since(t.lastEventAt))
			toClean = append(toClean, sessionID)
		}
	})
	var toClose []core.AgentSession
	for _, id := range toClean {
		if sess, ok := h.deleteSession(id); ok && sess != nil {
			toClose = append(toClose, sess)
		}
	}
	h.mu.Unlock()
	for _, sess := range toClose {
		_ = sess.Close()
	}
}

// isOC returns true when the request should be routed through OpenCodeProxy.
func (h *Handlers) isOC() bool {
	return h.ocProxy != nil
}

func (h *Handlers) BackendList() []BackendInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	var backends []BackendInfo
	for id, agent := range h.agents {
		caps := []string{"model_switch", "session_state"}
		if _, ok := agent.(core.ProviderSwitcher); ok {
			caps = append(caps, "provider_switch")
		}
		if _, ok := agent.(core.HistoryProvider); ok {
			caps = append(caps, "session_history")
		}
		if _, ok := agent.(core.MemoryFileReader); ok {
			caps = append(caps, "memory_read")
		}
		if _, ok := agent.(core.DiagnosticsProvider); ok {
			caps = append(caps, "diagnostics")
		}
		if _, ok := agent.(core.TokenUsageReporter); ok {
			caps = append(caps, "usage_reporting")
		}
		if _, ok := agent.(core.ModeSwitcher); ok {
			caps = append(caps, "permission_mode")
		}
		if _, ok := agent.(core.SessionRenamer); ok {
			if _, ok := agent.(core.SessionArchiver); ok {
				caps = append(caps, "session_mutation")
			}
		}
		if id == "claudecode" {
			caps = append(caps, "content_chunking")
		}
		if _, ok := agent.(core.SessionDeleter); ok {
			caps = append(caps, "session_delete")
		}
		if id != "opencode" && id != "codex" {
			if _, ok := agent.(core.ToolAuthorizer); ok {
				caps = append(caps, "permission_resolve")
			}
		}
		if _, ok := agent.(core.TodoProvider); ok || id == "opencode" {
			caps = append(caps, "todos")
		}
		if id == "codex" && h.codexBackendMode == "app_server" {
			caps = append(caps, "compression")
			caps = append(caps, "question_reply")
		}
		backends = append(backends, BackendInfo{
			ID:           id,
			Kind:         backendKindForAgent(agent),
			DisplayName:  agent.Name(),
			Capabilities: caps,
		})
	}
	return backends
}

func (h *Handlers) getAgent(id string) (core.Agent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent, ok := h.agents[id]
	return agent, ok
}

func normalizeModelParam(model map[string]interface{}) string {
	if model == nil {
		return ""
	}
	id, _ := model["id"].(string)
	providerID, _ := model["providerId"].(string)
	if id != "" {
		if providerID != "" && !strings.Contains(id, "/") {
			return providerID + "/" + id
		}
		return id
	}
	modelID, _ := model["modelId"].(string)
	if modelID != "" {
		if providerID != "" {
			return providerID + "/" + modelID
		}
		return modelID
	}
	return ""
}

func (h *Handlers) ensureOpenCodeSession(agent core.Agent, sessionID, modelID, dir string) (core.AgentSession, error) {
	if h.ocProxy != nil {
		if _, err := h.ocProxy.getSession(sessionID, dir); err != nil {
			h.mu.Lock()
			stale, _ := h.deleteSession(sessionID)
			delete(h.opencodeSessionOptions, sessionID)
			h.mu.Unlock()
			if stale != nil {
				_ = stale.Close()
			}
			return nil, err
		}
	}

	desired := opencodeSessionOptions{model: modelID, directory: dir}

	h.mu.Lock()
	sess, ok := h.getSession(sessionID)
	currentOpts := h.opencodeSessionOptions[sessionID]
	var stale core.AgentSession
	if ok && currentOpts != desired {
		stale = sess
		h.deleteSession(sessionID)
		delete(h.opencodeSessionOptions, sessionID)
		sess = nil
		ok = false
	}
	h.mu.Unlock()

	if stale != nil {
		_ = stale.Close()
	}
	if ok && sess != nil {
		return sess, nil
	}

	if dir != "" {
		switchDir(agent, dir)
	}
	if modelID != "" {
		if ms, ok := agent.(core.ModelSwitcher); ok {
			ms.SetModel(modelID)
		}
	}

	newSession, err := agent.StartSession(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}

	// Double-checked locking: 另一个并发请求可能已创建同 ID session
	h.mu.Lock()
	existing, existingOk := h.getSession(sessionID)
	if existingOk && existing != nil {
		h.mu.Unlock()
		_ = newSession.Close()
		return existing, nil
	}
	h.putSession(sessionID, newSession)
	h.opencodeSessionOptions[sessionID] = desired
	h.mu.Unlock()

	return newSession, nil
}

// extractDir extracts directory from request params.
func extractDir(msg WireMessage) string {
	if msg.Params == nil {
		return ""
	}
	var p struct {
		Directory string `json:"directory"`
	}
	json.Unmarshal(msg.Params, &p)
	return p.Directory
}

// switchDir switches agent workDir if the agent supports it.
func switchDir(agent core.Agent, dir string) {
	if dir == "" {
		return
	}
	if wd, ok := agent.(core.WorkDirSwitcher); ok {
		wd.SetWorkDir(dir)
	}
}

func (h *Handlers) HandleRPC(conn Connection, msg WireMessage) {
	slog.Info("go-bridge: RPC request", "method", msg.Method, "backendId", msg.BackendID, "requestId", msg.RequestID)

	// 检查设备是否已被撤销
	if dc, ok := conn.(*directConnAdapter); ok && dc.IsRevoked() {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "auth.device_revoked",
			Message: "设备授权已取消，请重新授权",
		})
		return
	}

	if h.handleDeliveryRPC(conn, msg) {
		return
	}
	if h.handleRelayUpgradeRPC(conn, msg) {
		return
	}

	h.mu.Lock()
	agent, ok := h.agents[msg.BackendID]
	h.mu.Unlock()

	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "backend_not_found",
			Message: fmt.Sprintf("backend %q not found", msg.BackendID),
		})
		return
	}

	// opencode 全部走 ocProxy
	if msg.BackendID == "opencode" && h.isOC() {
		h.handleOpenCodeRPC(conn, msg)
		return
	}

	h.dispatchRPC(conn, msg, agent)
}

// HandleRelayInbound 处理通过 relay 加密通道收到的 iOS→Mac 业务消息。
// 解密后的 JSON 应为标准 wire message，解析后走正常 RPC 分发路径。
func (h *Handlers) HandleRelayInbound(conn Connection, rawJSON json.RawMessage) {
	var msg WireMessage
	if err := json.Unmarshal(rawJSON, &msg); err != nil {
		slog.Warn("handlers: invalid relay inbound message", "error", err)
		return
	}

	switch {
	case msg.Type == "hello":
		// relay 加密通道的 hello 握手，走和直连相同的 handleHello 路径。
		if h.relayHelloHandler != nil {
			h.relayHelloHandler(conn, &msg)
		} else {
			slog.Warn("handlers: relay hello handler not configured, dropping hello")
		}
	case msg.Type == "request" && msg.Method != "":
		h.HandleRPC(conn, msg)
	default:
		slog.Debug("handlers: unhandled relay inbound message type",
			"type", msg.Type, "method", msg.Method)
	}
}

// handleDeliveryRPC 处理认证 channel 内、与 backend 无关的 delivery 管理请求。
func (h *Handlers) handleDeliveryRPC(conn Connection, msg WireMessage) bool {
	switch msg.Method {
	case "get_delivery_prekey_status", "upload_delivery_prekeys", "get_delivery_chain_head":
	default:
		return false
	}

	device := conn.AuthedDevice()
	if device == nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "auth.required", Message: "delivery RPC requires an authenticated device"})
		return true
	}

	switch msg.Method {
	case "get_delivery_prekey_status":
		conn.SendResult(msg.RequestID, h.deliveryPrekeys.GetPrekeyStatus(device.DeviceID), nil)
	case "upload_delivery_prekeys":
		var params struct {
			BatchID string             `json:"batchId"`
			Prekeys []PrekeyUploadItem `json:"prekeys"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "invalid delivery prekey batch"})
			return true
		}
		result := h.deliveryPrekeys.UploadPrekeys(PrekeyUploadBatch{
			BatchID:  params.BatchID,
			DeviceID: device.DeviceID,
			Prekeys:  params.Prekeys,
		})
		if result.Error != "" {
			message := "invalid delivery prekey batch"
			if result.Error == "prekey_limit_exceeded" {
				message = "delivery prekey limit exceeded"
			}
			conn.SendResult(msg.RequestID, nil, &WireError{Code: result.Error, Message: message})
			return true
		}
		conn.SendResult(msg.RequestID, result, nil)
	case "get_delivery_chain_head":
		head, err := h.deliveryPrekeys.GetDeliveryChainHead(device.DeviceID)
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "delivery_chain_error", Message: err.Error()})
			return true
		}
		if head == nil {
			head = &DeliveryChainHead{}
		}
		conn.SendResult(msg.RequestID, head, nil)
	}
	return true
}

func (h *Handlers) dispatchRPC(conn Connection, msg WireMessage, agent core.Agent) {
	if dir := extractDir(msg); dir != "" {
		if agent.Name() == "opencode" || shouldSwitchWorkDirForMethod(msg.Method) {
			switchDir(agent, dir)
		}
	}

	switch msg.Method {
	case "hello":
		conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	case "list_providers":
		h.handleListProviders(conn, msg, agent)
	case "set_provider":
		h.handleSetProvider(conn, msg, agent)
	case "list_models":
		h.handleListModels(conn, msg, agent)
	case "list_agents":
		h.handleListAgents(conn, msg, agent)
	case "list_permission_modes":
		h.handleListPermissionModes(conn, msg, agent)
	case "set_permission_mode":
		h.handleSetPermissionMode(conn, msg, agent)
	case "create_session":
		h.handleCreateSession(conn, msg, agent)
	case "send_message":
		h.handleSendMessage(conn, msg, agent)
	case "abort_generation":
		h.handleAbortGeneration(conn, msg)
	case "get_session":
		h.handleGetSession(conn, msg, agent)
	case "get_session_messages":
		h.handleGetSessionMessages(conn, msg, agent)
	case "delete_session":
		h.handleDeleteSession(conn, msg, agent)
	case "resume_session":
		h.handleResumeSession(conn, msg, agent)
	case "switch_model":
		h.handleSwitchModel(conn, msg, agent)
	case "resolve_permission":
		h.handleResolvePermission(conn, msg)
	case "list_sessions":
		h.handleListSessions(conn, msg, agent)
	case "list_projects":
		h.handleListProjects(conn, msg, agent)
	case "fetch_todos":
		h.handleFetchTodos(conn, msg, agent)
	case "get_usage":
		h.handleGetUsage(conn, msg, agent)
	case "run_diagnostics":
		h.handleRunDiagnostics(conn, msg, agent)
	case "list_memory_files":
		h.handleListMemoryFiles(conn, msg, agent)
	case "read_memory_file":
		h.handleReadMemoryFile(conn, msg, agent)
	case "fetch_content_chunk":
		h.handleFetchContentChunk(conn, msg)
	case "read_file":
		h.handleReadFile(conn, msg)
	case "rename_session":
		h.handleRenameSession(conn, msg, agent)
	case "share_session":
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "not_supported",
			Message: "session share is not supported",
		})
	case "archive_session":
		h.handleArchiveSession(conn, msg, agent)
	case "compress_context":
		h.handleCompressContext(conn, msg)
	case "check_pending_notifications":
		h.handleCheckPendingNotifications(conn, msg)
	case "question_reply":
		h.handleQuestionReply(conn, msg)
	case "question_reject":
		h.handleQuestionReject(conn, msg)
	default:
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "method_not_found",
			Message: fmt.Sprintf("method %q not implemented", msg.Method),
		})
	}
}

func shouldSwitchWorkDirForMethod(method string) bool {
	switch method {
	case "list_sessions", "get_session", "get_session_messages":
		return false
	default:
		return true
	}
}

// ── OpenCode (ocProxy) 全量路由 ──────────────────────────────────────────────

func (h *Handlers) handleOpenCodeRPC(conn Connection, msg WireMessage) {
	dir := extractDir(msg)

	// cc-connect 已覆盖的能力走 generic dispatch
	switch msg.Method {
	case "hello":
		conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)

	case "list_providers", "set_provider", "list_agents",
		"fetch_todos", "get_usage", "run_diagnostics",
		"list_memory_files", "read_memory_file", "fetch_content_chunk", "read_file",
		"rename_session", "archive_session", "compress_context",
		"delete_session", "list_models", "switch_model",
		"get_session_messages":
		agent, ok := h.getAgent(msg.BackendID)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "backend_not_found", Message: "opencode agent not registered"})
			return
		}
		h.dispatchRPC(conn, msg, agent)
		return

	case "list_permission_modes", "set_permission_mode":
		agent, ok := h.getAgent(msg.BackendID)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "backend_not_found", Message: "opencode agent not registered"})
			return
		}
		if msg.Method == "list_permission_modes" {
			h.handleListPermissionModes(conn, msg, agent)
		} else {
			h.handleSetPermissionMode(conn, msg, agent)
		}
		return

	case "resolve_permission":
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "opencode does not support permission resolution via cc-connect"})

	case "share_session":
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "session share is not supported"})
	}

	// 以下 case 仍需 ocProxy（cc-connect 尚未实现）
	switch msg.Method {
	case "get_session":
		h.ocHandleGetSession(conn, msg, dir)

	case "list_sessions":
		h.ocHandleListSessions(conn, msg, dir)

	case "list_projects":
		h.ocHandleListProjects(conn, msg)

	case "create_session":
		h.ocHandleCreateSession(conn, msg, dir)

	case "resume_session":
		h.ocHandleResumeSession(conn, msg, dir)

	case "send_message":
		agent, ok := h.getAgent(msg.BackendID)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "backend_not_found", Message: "opencode agent not registered"})
			return
		}
		h.ocHandleSendMessage(conn, msg, dir, agent)

	case "abort_generation":
		h.ocHandleAbortGeneration(conn, msg, dir)

	default:
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "method_not_found",
			Message: fmt.Sprintf("method %q not implemented", msg.Method),
		})
	}
}

// ── ocProxy: list_sessions ────────────────────────────────────────────────────

func (h *Handlers) ocHandleListSessions(conn Connection, msg WireMessage, dir string) {
	rootsOnly := extractBool(msg, "rootsOnly")

	sessions, err := h.ocProxy.listSessions(dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}

	var result []map[string]interface{}
	for _, s := range sessions {
		parentID, _ := s["parentId"].(string)
		if parentID == "" {
			parentID, _ = s["parentID"].(string)
		}
		if rootsOnly && parentID != "" {
			continue
		}
		result = append(result, mapSession(s))
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"sessions": result}, nil)
}

// ── ocProxy: get_session ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleGetSession(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	s, err := h.ocProxy.getSession(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "get_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"session": mapSession(s)}, nil)
}

// ── ocProxy: get_session_messages ─────────────────────────────────────────────

func (h *Handlers) ocHandleGetSessionMessages(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	// 订阅连接到该 session，以便 relayEvents 转发实时事件
	h.subscribeConnToSession(conn, msg, sessionID)

	msgs, err := h.ocProxy.getSessionMessages(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "history_failed", Message: err.Error()})
		return
	}

	var result []map[string]interface{}
	for _, m := range msgs {
		result = append(result, mapMessage(m))
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"messages":        result,
		"nextCursor":      nil,
		"snapshotVersion": "v1",
		"truncated":       false,
	}, nil)
}

// ── ocProxy: list_models ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleListModels(conn Connection, msg WireMessage, dir string) {
	models, err := h.ocProxy.listModels(dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"models":            models,
		"configFingerprint": nil,
		"source":            "catalog",
		"generatedAtMillis": nowMillis(),
	}, nil)
}

// ── ocProxy: list_agents ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleListAgents(conn Connection, msg WireMessage) {
	agents, err := h.ocProxy.listAgents()
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"agents": agents}, nil)
}

// ── ocProxy: list_projects ────────────────────────────────────────────────────

func (h *Handlers) ocHandleListProjects(conn Connection, msg WireMessage) {
	projects, err := h.ocProxy.listProjects()
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"projects": projects}, nil)
}

// ── ocProxy: create_session ───────────────────────────────────────────────────

func (h *Handlers) ocHandleCreateSession(conn Connection, msg WireMessage, dir string) {
	title := extractString(msg, "title")

	s, err := h.ocProxy.createSession(title, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "create_failed", Message: err.Error()})
		return
	}

	session := mapSession(s)
	conn.SendResult(msg.RequestID, session, nil)
}

// ── ocProxy: resume_session ───────────────────────────────────────────────────

func (h *Handlers) ocHandleResumeSession(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	s, err := h.ocProxy.getSession(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "resume_failed", Message: err.Error()})
		return
	}

	session := mapSession(s)
	conn.SendResult(msg.RequestID, session, nil)
}

// ── opencode hybrid: send_message ─────────────────────────────────────────────

func (h *Handlers) ocHandleSendMessage(conn Connection, msg WireMessage, dir string, agent core.Agent) {
	var params struct {
		SessionID       string                 `json:"sessionId"`
		Content         string                 `json:"content"`
		Agent           string                 `json:"agent,omitempty"`
		Model           map[string]interface{} `json:"model,omitempty"`
		ReasoningEffort string                 `json:"reasoningEffort,omitempty"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	modelID := normalizeModelParam(params.Model)
	sess, err := h.ensureOpenCodeSession(agent, params.SessionID, modelID, dir)
	if err != nil {
		code := "send_failed"
		if strings.Contains(err.Error(), "HTTP 404") {
			code = "session_not_found"
		}
		conn.SendResult(msg.RequestID, nil, &WireError{Code: code, Message: err.Error()})
		return
	}

	conn.SendEvent(params.SessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "running"})
	h.broadcaster.Subscribe(conn, SubscriptionKey{
		BackendID: msg.BackendID,
		SessionID: params.SessionID,
		Directory: dir,
	})

	if err := sess.Send(params.Content, nil, nil); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "send_failed", Message: err.Error()})
		return
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
}

// ── opencode hybrid: abort_generation ─────────────────────────────────────────

func (h *Handlers) ocHandleAbortGeneration(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
		return
	}
	if err := h.ocProxy.abortGeneration(sessionID, dir); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "abort_failed", Message: err.Error()})
		return
	}

	h.mu.Lock()
	sess, ok := h.deleteSession(sessionID)
	delete(h.opencodeSessionOptions, sessionID)
	h.mu.Unlock()
	if ok {
		_ = sess.Close()
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	// 只有 session 确实被删除时才发完成事件，避免伪造状态
	if ok {
		conn.SendEvent(sessionID, msg.BackendID, "turn_completed", map[string]interface{}{"done": true, "reason": "aborted"})
		conn.SendEvent(sessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "idle"})
	}
}

// ── ocProxy: delete_session ───────────────────────────────────────────────────

func (h *Handlers) ocHandleDeleteSession(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}
	if err := h.ocProxy.deleteSession(sessionID, dir); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "delete_failed", Message: err.Error()})
		return
	}
	h.mu.Lock()
	sess, _ := h.deleteSession(sessionID)
	delete(h.opencodeSessionOptions, sessionID)
	h.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
	}
	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

// ── ocProxy: fetch_todos ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleFetchTodos(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	todos, err := h.ocProxy.fetchTodos(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "todo_failed", Message: err.Error()})
		return
	}

	var result []map[string]interface{}
	for _, t := range todos {
		content, _ := t["content"].(string)
		if content == "" {
			content, _ = t["text"].(string)
		}
		status, _ := t["status"].(string)
		if status == "" {
			status = "pending"
		}
		priority, _ := t["priority"].(string)
		if priority == "" {
			priority = "normal"
		}
		result = append(result, map[string]interface{}{
			"content":  content,
			"status":   status,
			"priority": priority,
		})
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"todos": result}, nil)
}

// ── 非 opencode 的原有 handler ───────────────────────────────────────────────

func (h *Handlers) handleListModels(conn Connection, msg WireMessage, agent core.Agent) {
	ms, ok := agent.(core.ModelSwitcher)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code: "not_supported", Message: "backend does not support model switching",
		})
		return
	}

	ccModels := ms.AvailableModels(context.Background())
	currentModel := ms.GetModel()

	var models []map[string]interface{}
	for _, m := range ccModels {
		id, provider, providerID := modelProviderForAgent(agent, m.Name)
		name := m.Desc
		if name == "" {
			name = id
		}
		models = append(models, map[string]interface{}{
			"id":                        m.Name,
			"name":                      name,
			"provider":                  provider,
			"providerId":                providerID,
			"reasoning":                 false,
			"limit":                     nil,
			"supportedReasoningEfforts": nil,
			"defaultReasoningEffort":    nil,
			"isDefault":                 m.Name == currentModel,
		})
	}

	if re, ok := agent.(core.ReasoningEffortSwitcher); ok {
		efforts := re.AvailableReasoningEfforts()
		if len(efforts) > 0 {
			wireEfforts := make([]string, len(efforts))
			copy(wireEfforts, efforts)
			for i := range models {
				models[i]["supportedReasoningEfforts"] = wireEfforts
			}
		}
	}

	conn.SendResult(msg.RequestID, map[string]interface{}{
		"models":            models,
		"configFingerprint": nil,
		"source":            "catalog",
		"generatedAtMillis": time.Now().UnixMilli(),
	}, nil)
}

func (h *Handlers) handleListProviders(conn Connection, msg WireMessage, agent core.Agent) {
	switcher, ok := agent.(core.ProviderSwitcher)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support provider switching"})
		return
	}

	providers := switcher.ListProviders()
	activeProvider := ""
	if active := switcher.GetActiveProvider(); active != nil {
		activeProvider = active.Name
	}

	conn.SendResult(msg.RequestID, map[string]interface{}{
		"providers":      providerConfigsToWire(providers, activeProvider),
		"activeProvider": activeProvider,
	}, nil)
}

func (h *Handlers) handleSetProvider(conn Connection, msg WireMessage, agent core.Agent) {
	switcher, ok := agent.(core.ProviderSwitcher)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support provider switching"})
		return
	}

	var params SetProviderParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.Provider) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "provider required"})
		return
	}
	if !switcher.SetActiveProvider(params.Provider) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_found", Message: fmt.Sprintf("provider %q not found", params.Provider)})
		return
	}

	conn.SendResult(msg.RequestID, map[string]interface{}{
		"provider":  params.Provider,
		"appliesTo": "new_sessions",
	}, nil)
}

func parseModelID(raw string) (id, provider, providerID string) {
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) == 2 {
		return parts[1], parts[0], parts[0]
	}
	return raw, "default", "default"
}

func modelProviderForAgent(agent core.Agent, raw string) (id, provider, providerID string) {
	id, provider, providerID = parseModelID(raw)
	if provider != "default" {
		return id, provider, providerID
	}
	if ps, ok := agent.(core.ProviderSwitcher); ok {
		if active := ps.GetActiveProvider(); active != nil && active.Name != "" {
			return id, active.Name, active.Name
		}
	}
	if agent.Name() == "codex" {
		return id, "openai", "openai"
	}
	return id, provider, providerID
}

func (h *Handlers) handleListAgents(conn Connection, msg WireMessage, agent core.Agent) {
	lister, ok := agent.(core.AgentLister)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support agent listing"})
		return
	}

	agents, err := lister.ListAgents(context.Background())
	if err != nil {
		if errors.Is(err, core.ErrNotSupported) {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support agent listing"})
			return
		}
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}

	result := make([]map[string]interface{}, 0, len(agents))
	for _, agentInfo := range agents {
		result = append(result, map[string]interface{}{
			"name":        agentInfo.Name,
			"mode":        agentInfo.Mode,
			"hidden":      agentInfo.Hidden,
			"native":      agentInfo.Native,
			"description": agentInfo.Description,
		})
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"agents": result}, nil)
}

func (h *Handlers) handleListProjects(conn Connection, msg WireMessage, agent core.Agent) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		conn.SendResult(msg.RequestID, map[string]interface{}{"projects": []interface{}{}}, nil)
		return
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		conn.SendResult(msg.RequestID, map[string]interface{}{"projects": []interface{}{}}, nil)
		return
	}

	var projects []map[string]interface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		key := entry.Name()
		if isHiddenProjectDir(key) {
			continue
		}
		realDir := resolveProjectRealDirectory(filepath.Join(projectsDir, key))
		if realDir == "" {
			realDir = key
		}
		displayName := filepath.Base(realDir)
		projects = append(projects, map[string]interface{}{
			"id":        key,
			"directory": realDir,
			"name":      displayName,
		})
	}
	if projects == nil {
		projects = []map[string]interface{}{}
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"projects": projects}, nil)
}

func resolveProjectRealDirectory(projectDir string) string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(projectDir, entry.Name()))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				continue
			}
			var cwd string
			if err := json.Unmarshal(raw["cwd"], &cwd); err == nil && cwd != "" {
				f.Close()
				return cwd
			}
		}
		f.Close()
	}
	return ""
}

func (h *Handlers) handleFetchTodos(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	if wd, ok := agent.(core.WorkDirSwitcher); ok {
		slog.Info("go-bridge: fetch_todos agent workDir", "backendID", msg.BackendID, "sessionID", params.SessionID, "paramDir", params.Directory, "workDir", wd.GetWorkDir())
	}

	slog.Info("go-bridge: fetch_todos called", "backendID", msg.BackendID, "sessionID", params.SessionID, "directory", params.Directory)

	provider, ok := agent.(core.TodoProvider)
	if !ok {
		slog.Warn("go-bridge: fetch_todos — agent is not TodoProvider", "backendID", msg.BackendID)
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support todos"})
		return
	}

	todos, err := provider.FetchTodos(context.Background(), params.SessionID)
	if err != nil {
		slog.Warn("go-bridge: fetch_todos failed", "backendID", msg.BackendID, "sessionID", params.SessionID, "error", err)
		if errors.Is(err, core.ErrNotSupported) {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support todos"})
			return
		}
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "todo_failed", Message: err.Error()})
		return
	}

	slog.Info("go-bridge: fetch_todos result", "backendID", msg.BackendID, "sessionID", params.SessionID, "count", len(todos))
	conn.SendResult(msg.RequestID, map[string]interface{}{"todos": todosToWire(todos)}, nil)
}

func (h *Handlers) handleGetUsage(conn Connection, msg WireMessage, agent core.Agent) {
	reporter, ok := agent.(core.TokenUsageReporter)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support usage reporting"})
		return
	}

	report, err := reporter.GetTokenUsage(context.Background())
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "usage_failed", Message: err.Error()})
		return
	}
	if report == nil {
		report = &core.TokenUsageReport{}
	}

	data := map[string]interface{}{
		"totalTokensUsed":     report.TotalTokensUsed,
		"inputTokens":         report.InputTokens,
		"outputTokens":        report.OutputTokens,
		"cacheReadTokens":     report.CacheReadTokens,
		"cacheCreationTokens": report.CacheCreationTokens,
	}
	if len(report.PerSessionBreakdown) > 0 {
		breakdown := make([]map[string]interface{}, 0, len(report.PerSessionBreakdown))
		for _, session := range report.PerSessionBreakdown {
			breakdown = append(breakdown, map[string]interface{}{
				"sessionId":           session.SessionID,
				"tokensUsed":          session.TokensUsed,
				"inputTokens":         session.InputTokens,
				"outputTokens":        session.OutputTokens,
				"cacheReadTokens":     session.CacheReadTokens,
				"cacheCreationTokens": session.CacheCreationTokens,
			})
		}
		data["perSessionBreakdown"] = breakdown
	}

	conn.SendResult(msg.RequestID, data, nil)
}

func (h *Handlers) handleRunDiagnostics(conn Connection, msg WireMessage, agent core.Agent) {
	provider, ok := agent.(core.DiagnosticsProvider)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support diagnostics"})
		return
	}

	runID := fmt.Sprintf("diag-%s", generateShortID())
	conn.SendResult(msg.RequestID, map[string]interface{}{"diagnosticRunId": runID}, nil)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		report, err := provider.RunDiagnostics(ctx, func(progress core.DiagnosticProgress) {
			conn.SendEvent("", msg.BackendID, "diagnostic_progress", map[string]interface{}{
				"diagnosticRunId": runID,
				"checkId":         progress.CheckID,
				"status":          progress.Status,
				"message":         progress.Message,
			})
		})

		if err != nil {
			report = &core.DiagnosticReport{
				Results: []core.DiagnosticResult{{
					ID:            "diagnostics",
					Name:          "诊断执行",
					Status:        "failed",
					Message:       err.Error(),
					Severity:      "required",
					FixSuggestion: "检查 bridge 日志与 Claude 后端配置，然后重试诊断。",
				}},
				OverallStatus: "unhealthy",
			}
		}
		if report == nil {
			report = &core.DiagnosticReport{OverallStatus: "healthy"}
		}

		conn.SendEvent("", msg.BackendID, "diagnostic_completed", map[string]interface{}{
			"diagnosticRunId": runID,
			"overallStatus":   report.OverallStatus,
			"results":         diagnosticResultsToWire(report.Results),
		})
	}()
}

func (h *Handlers) handleListMemoryFiles(conn Connection, msg WireMessage, agent core.Agent) {
	reader, ok := agent.(core.MemoryFileReader)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support memory file reading"})
		return
	}

	files, err := reader.ListMemoryFiles(context.Background())
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "memory_failed", Message: err.Error()})
		return
	}

	result := make([]map[string]interface{}, 0, len(files))
	for _, file := range files {
		result = append(result, memoryFileToWire(file, false))
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"files": result}, nil)
}

func (h *Handlers) handleReadMemoryFile(conn Connection, msg WireMessage, agent core.Agent) {
	reader, ok := agent.(core.MemoryFileReader)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support memory file reading"})
		return
	}

	var params struct {
		FileID string `json:"fileId"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.FileID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "fileId required"})
		return
	}

	file, err := reader.ReadMemoryFile(context.Background(), params.FileID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "memory_failed", Message: err.Error()})
		return
	}
	if file == nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "memory_failed", Message: "memory file not found"})
		return
	}

	conn.SendResult(msg.RequestID, memoryFileToWire(*file, true), nil)
}

func (h *Handlers) handleRenameSession(conn Connection, msg WireMessage, agent core.Agent) {
	renamer, ok := agent.(core.SessionRenamer)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "session rename not yet supported"})
		return
	}

	var params struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}
	if strings.TrimSpace(params.Title) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "title required"})
		return
	}

	session, err := renamer.RenameSession(context.Background(), params.SessionID, params.Title)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "rename_failed", Message: err.Error()})
		return
	}
	if session == nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "rename_failed", Message: "backend returned no session"})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"session": sessionsToWire([]core.AgentSessionInfo{*session})[0]}, nil)
}

func (h *Handlers) handleArchiveSession(conn Connection, msg WireMessage, agent core.Agent) {
	archiver, ok := agent.(core.SessionArchiver)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "session archive not yet supported"})
		return
	}

	var params struct {
		SessionID        string  `json:"sessionId"`
		ArchivedAtMillis float64 `json:"archivedAtMillis"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	archivedAt := time.Now().UTC()
	if params.ArchivedAtMillis > 0 {
		archivedAt = time.UnixMilli(int64(params.ArchivedAtMillis)).UTC()
	}

	session, err := archiver.ArchiveSession(context.Background(), params.SessionID, archivedAt)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "archive_failed", Message: err.Error()})
		return
	}
	if session == nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "archive_failed", Message: "backend returned no session"})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"session": sessionsToWire([]core.AgentSessionInfo{*session})[0]}, nil)
}

func (h *Handlers) handleFetchContentChunk(conn Connection, msg WireMessage) {
	var params struct {
		ContentID string `json:"contentId"`
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.ContentID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "contentId required"})
		return
	}

	content, ok := h.getContentRef(params.ContentID)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "content_not_found", Message: fmt.Sprintf("content %q not found", params.ContentID)})
		return
	}

	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 32768
	}
	if limit > 262144 {
		limit = 262144
	}
	if offset > len(content) {
		offset = len(content)
	}
	end := offset + limit
	if end > len(content) {
		end = len(content)
	}
	data := content[offset:end]
	nextOffset := offset + len(data)
	result := map[string]interface{}{
		"contentId": params.ContentID,
		"offset":    offset,
		"data":      data,
		"complete":  nextOffset >= len(content),
	}
	if nextOffset < len(content) {
		result["nextOffset"] = nextOffset
	}
	conn.SendResult(msg.RequestID, result, nil)
}

func (h *Handlers) handleCreateSession(conn Connection, msg WireMessage, agent core.Agent) {
	var params CreateSessionParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	if params.Directory != "" {
		switchDir(agent, params.Directory)
	}

	if agent.Name() == "codex" {
		sessionID := fmt.Sprintf("pending-%s", generateShortID())
		result := map[string]interface{}{
			"id":    sessionID,
			"title": params.Title,
		}
		if params.Directory != "" {
			result["directory"] = params.Directory
		}
		conn.SendEvent(sessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "idle"})
		conn.SendResult(msg.RequestID, result, nil)
		return
	}

	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "create_failed", Message: err.Error()})
		return
	}

	sessionID := waitForSessionID(sess, 15*time.Second)
	if sessionID == "" {
		sessionID = fmt.Sprintf("pending-%s", generateShortID())
	}

	h.mu.Lock()
	h.putSession(sessionID, sess)
	h.mu.Unlock()

	result := map[string]interface{}{
		"id":    sessionID,
		"title": params.Title,
	}
	if params.Directory != "" {
		result["directory"] = params.Directory
	}

	conn.SendEvent(sessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "idle"})
	conn.SendResult(msg.RequestID, result, nil)
}

func waitForSessionID(sess core.AgentSession, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if id := sess.CurrentSessionID(); id != "" {
			return id
		}
		time.Sleep(100 * time.Millisecond)
	}
	return sess.CurrentSessionID()
}

func generateShortID() string {
	b := make([]byte, 8)
	for i := range b {
		b[i] = "0123456789abcdef"[rand.Intn(16)]
	}
	return string(b)
}

func (h *Handlers) handleSendMessage(conn Connection, msg WireMessage, agent core.Agent) {
	var params SendMessageParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	if params.Directory != "" {
		switchDir(agent, params.Directory)
	}
	applySendMessageRuntimeOptions(agent, params)

	slog.Info("go-bridge: handleSendMessage", "sessionID", params.SessionID, "contentLen", len(params.Content), "contentPreview", params.Content[:min(len(params.Content), 120)])
	h.mu.Lock()
	sess, ok := h.getSession(params.SessionID)
	h.mu.Unlock()

	if !ok {
		resumeID := params.SessionID
		if strings.HasPrefix(resumeID, "pending-") {
			resumeID = ""
		}
		slog.Info("go-bridge: handleSendMessage: session not found in registry. Starting new agent session.", "sessionID", params.SessionID, "resumeID", resumeID)
		var err error
		sess, err = agent.StartSession(context.Background(), resumeID)
		if err != nil {
			slog.Error("go-bridge: handleSendMessage: StartSession failed", "sessionID", params.SessionID, "resumeID", resumeID, "error", err)
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: err.Error()})
			return
		}

		// resume 时 claude --resume 会输出完整历史，先排空历史事件。
		// Codex thread/resume 不会重放历史，不应 drain（会阻塞或丢弃初始 plan 事件）。
		if resumeID != "" && agent.Name() != "codex" {
			drainHistoryEvents(sess)
		}

		// Double-checked locking: 并发 sendMessage 可能已创建同 ID session
		h.mu.Lock()
		existing, existingOk := h.getSession(params.SessionID)
		if existingOk && existing != nil {
			h.mu.Unlock()
			slog.Info("go-bridge: handleSendMessage: session already created concurrently, closing the new one and using existing", "sessionID", params.SessionID)
			_ = sess.Close()
			sess = existing
		} else {
			h.putSessionWithMeta(params.SessionID, msg.BackendID, extractDir(msg), sess)
			h.mu.Unlock()
		}
	} else {
		slog.Info("go-bridge: handleSendMessage: found active session in registry", "sessionID", params.SessionID)
	}

	// 通知 iOS 进入 running 状态
	conn.SendEvent(params.SessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "running"})
	h.sessions.markRunning(params.SessionID)

	// 订阅该 session 的事件
	dir := extractDir(msg)
	h.broadcaster.Subscribe(conn, SubscriptionKey{
		BackendID: msg.BackendID,
		SessionID: params.SessionID,
		Directory: dir,
	})

	if err := sess.Send(params.Content, nil, nil); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "send_failed", Message: err.Error()})
		return
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
}

func applySendMessageRuntimeOptions(agent core.Agent, params SendMessageParams) {
	if modelID := selectedModelParam(agent, params.Model); modelID != "" {
		if ms, ok := agent.(core.ModelSwitcher); ok {
			ms.SetModel(modelID)
		}
	}
	if params.ReasoningEffort != "" {
		if re, ok := agent.(core.ReasoningEffortSwitcher); ok {
			re.SetReasoningEffort(params.ReasoningEffort)
		}
	}
}

func selectedModelParam(agent core.Agent, model map[string]interface{}) string {
	if model == nil {
		return ""
	}
	if agent.Name() == "codex" {
		if id, _ := model["id"].(string); id != "" {
			return id
		}
		if modelID, _ := model["modelId"].(string); modelID != "" {
			return modelID
		}
		return ""
	}
	return normalizeModelParam(model)
}

// drainHistoryEvents 排空 claude --resume 重放的历史事件。
// 历史重放以一个 result 事件结束，排空到 result 为止。
// 如果 session 为空或历史很短，100ms 无事件则返回。
func drainHistoryEvents(sess core.AgentSession) {
	events := sess.Events()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev, ok := <-events:
			if !ok || ev.Type == core.EventResult {
				return
			}
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

// startRelayIfNotRunning 为 session 启动事件转发（如果尚未运行）。
// 用于 iOS 仅调用 get_session_messages 而未调用 send_message 的场景。
func (h *Handlers) startRelayIfNotRunning(sessionID string, sess core.AgentSession, conn Connection, backendID string) {
	h.mu.Lock()
	running := h.relayRunning[sessionID]
	if !running {
		h.relayRunning[sessionID] = true
	}
	h.mu.Unlock()
	if !running {
		go h.relayEvents(conn, sess, sessionID, backendID)
	}
}

func (h *Handlers) relayEvents(conn Connection, sess core.AgentSession, sessionID, backendID string) {
	origSessionID := sessionID
	defer func() {
		h.mu.Lock()
		delete(h.relayRunning, origSessionID)
		delete(h.relayRunning, sessionID)
		h.mu.Unlock()
		slog.Info("go-bridge: relayEvents exited", "backendID", backendID, "sessionID", sessionID)
	}()
	slog.Info("go-bridge: relayEvents started", "backendID", backendID, "sessionID", sessionID)
	events := sess.Events()
	eventCount := 0
	// relayInitialTimeout 是 passive join 后首次等待事件的超时。
	// 如果 session 的 turn 已经结束，不会收到 turn/completed，
	// 需要快速超时让 iOS 退出执行态。
	const relayInitialTimeout = 10 * time.Second
	// relayActiveTimeout 是收到首个事件后的空闲超时。
	const relayActiveTimeout = 60 * time.Second

	idleTimer := time.NewTimer(relayInitialTimeout)
	defer idleTimer.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if !h.sessions.isIdle(sessionID) {
					h.mu.Lock()
					h.seq++
					seq := h.seq
					dir := h.sessions.directoryForSession(sessionID)
					h.mu.Unlock()

					compMsg := EventMessage{
						Type:      "event",
						SessionID: sessionID,
						BackendID: backendID,
						Event:     "turn_completed",
						Data:      map[string]interface{}{"done": true, "reason": "events_channel_closed"},
						Seq:       seq,
					}
					h.broadcaster.Send(BroadcastEvent{
						BackendID: backendID,
						SessionID: sessionID,
						Directory: dir,
						Message:   compMsg,
					})

					h.broadcastIdleState(sessionID, backendID)
					h.recordPendingNotification(sessionID, backendID, "completed", "events_channel_closed")
				}
				return
			}
			idleTimer.Reset(relayActiveTimeout)
			eventCount++
			h.mu.Lock()
			dir := h.sessions.directoryForSession(sessionID)
			h.mu.Unlock()
			sessionID = h.rebindSessionIDIfResolved(sessionID, sess, ev.SessionID, backendID, dir)
			eventName, data, _ := mapAgentEvent(ev)
			if eventName == "" {
				slog.Debug("go-bridge: relayEvents unmapped event", "backendID", backendID, "sessionID", sessionID, "eventType", ev.Type)
				continue
			}

			if eventCount <= 3 || eventName == "todos_updated" || eventName == "turn_completed" || eventName == "error" {
				slog.Info("go-bridge: relayEvents forwarding", "backendID", backendID, "sessionID", sessionID, "event", eventName, "seq", eventCount)
			}

			h.mu.Lock()
			h.seq++
			seq := h.seq
			directory := h.sessions.directoryForSession(sessionID)
			h.mu.Unlock()

			msg := EventMessage{
				Type:      "event",
				SessionID: sessionID,
				BackendID: backendID,
				Event:     eventName,
				Data:      data,
				Seq:       seq,
			}
			h.broadcaster.Send(BroadcastEvent{
				BackendID: backendID,
				SessionID: sessionID,
				Directory: directory,
				Message:   msg,
			})
			h.routeRelayOfflineEvent(sessionID, backendID, eventName, data)

			// 持续刷新 lastEventAt，防止 idle cleanup 在长 turn 期间误杀 session。
			h.sessions.touch(sessionID)

			if ev.Type == core.EventResult && ev.Done {
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "completed", "")
				return
			}
			if ev.Type == core.EventError {
				errMsg := ""
				if ev.Error != nil {
					errMsg = ev.Error.Error()
				}
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "error", errMsg)
				return
			}

		case <-idleTimer.C:
			slog.Warn("go-bridge: relayEvents idle timeout, auto-completing", "backendID", backendID, "sessionID", sessionID, "eventsSeen", eventCount)
			if !h.sessions.isIdle(sessionID) {
				h.mu.Lock()
				h.seq++
				seq := h.seq
				dir := h.sessions.directoryForSession(sessionID)
				h.mu.Unlock()
				completeMsg := EventMessage{
					Type:      "event",
					SessionID: sessionID,
					BackendID: backendID,
					Event:     "turn_completed",
					Data:      map[string]interface{}{"done": true, "text": ""},
					Seq:       seq,
				}
				h.broadcaster.Send(BroadcastEvent{
					BackendID: backendID,
					SessionID: sessionID,
					Directory: dir,
					Message:   completeMsg,
				})
				h.broadcastIdleState(sessionID, backendID)
				h.recordPendingNotification(sessionID, backendID, "completed", "relay_idle_timeout")
			}
			return
		}
	}
}

func (h *Handlers) routeRelayOfflineEvent(sessionID, backendID, eventName string, data interface{}) {
	if !IsDurableMilestone(eventName) {
		return
	}
	h.mu.Lock()
	store := h.trustedDevices
	sender := h.relayEnvelopeSender
	h.mu.Unlock()
	if store == nil || sender == nil || h.relayEventRouter == nil {
		return
	}
	devices, err := store.ListDevices()
	if err != nil {
		slog.Warn("go-bridge: list relay devices for offline delivery failed", "error", err)
		return
	}
	onlineDevices := h.broadcaster.ActiveDeviceIDs()
	mailboxDevices := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.RevokedAt != nil || !device.RelayEnabled || device.IdentityPublicKey == "" {
			continue
		}
		mailboxDevices = append(mailboxDevices, device.DeviceID)
	}
	if len(mailboxDevices) == 0 {
		return
	}
	h.relayEventRouter.RouteEvent(sessionID, backendID, eventName, data, onlineDevices, mailboxDevices)
	for _, deviceID := range mailboxDevices {
		if err := h.relayOutbox.Flush(deviceID, sender); err != nil {
			slog.Warn("go-bridge: relay offline delivery flush failed", "deviceID", safeID(deviceID), "error", err)
		}
	}
}

func (h *Handlers) FlushRelayOutboxes() {
	h.mu.Lock()
	store := h.trustedDevices
	sender := h.relayEnvelopeSender
	h.mu.Unlock()
	if store == nil || sender == nil || h.relayOutbox == nil {
		return
	}
	devices, err := store.ListDevices()
	if err != nil {
		slog.Warn("go-bridge: list relay devices for outbox flush failed", "error", err)
		return
	}
	for _, device := range devices {
		if device.RevokedAt != nil || !device.RelayEnabled || device.IdentityPublicKey == "" {
			continue
		}
		if err := h.relayOutbox.Flush(device.DeviceID, sender); err != nil {
			slog.Warn("go-bridge: relay outbox flush failed", "deviceID", safeID(device.DeviceID), "error", err)
		}
	}
}

func (h *Handlers) rebindSessionIDIfResolved(currentID string, sess core.AgentSession, eventSessionID, backendID, directory string) string {
	realID := strings.TrimSpace(eventSessionID)
	if realID == "" {
		realID = strings.TrimSpace(sess.CurrentSessionID())
	}
	if realID == "" || realID == currentID || !strings.HasPrefix(currentID, "pending-") {
		return currentID
	}

	h.sessions.rebind(currentID, realID)
	h.broadcaster.Rebind(currentID, realID, backendID, directory)
	return realID
}

// broadcastIdleState 向订阅者推送 session_state_changed: idle。
func (h *Handlers) broadcastIdleState(sessionID, backendID string) {
	h.mu.Lock()
	dir := h.sessions.directoryForSession(sessionID)
	h.mu.Unlock()
	stateMsg := EventMessage{
		Type:      "event",
		SessionID: sessionID,
		BackendID: backendID,
		Event:     "session_state_changed",
		Data:      map[string]interface{}{"state": "idle"},
	}
	h.broadcaster.Send(BroadcastEvent{
		BackendID: backendID,
		SessionID: sessionID,
		Directory: dir,
		Message:   stateMsg,
	})
	h.sessions.markIdle(sessionID)
}

// recordPendingNotification 为订阅了该 session 的所有设备记录一条待通知事件。
// iOS 端可能在后台被系统挂起，无法通过 WebSocket 实时收到 turn_completed。
// 回到前台后通过 check_pending_notifications RPC 拉取。
func (h *Handlers) recordPendingNotification(sessionID, backendID, reason, message string) {
	h.mu.Lock()
	dir := h.sessions.directoryForSession(sessionID)
	h.mu.Unlock()

	deviceIDs := h.broadcaster.SubscriberDeviceIDs(backendID, sessionID)
	now := time.Now()
	for _, deviceID := range deviceIDs {
		h.pendingNotifications.Record(deviceID, PendingNotification{
			SessionID:   sessionID,
			BackendID:   backendID,
			Directory:   dir,
			Reason:      reason,
			Message:     message,
			CompletedAt: now,
		})
	}
}

func (h *Handlers) resolveSessionIDForActiveSession(sessionID string) string {
	if !strings.HasPrefix(sessionID, "pending-") {
		return sessionID
	}
	sess, ok := h.getSession(sessionID)
	if !ok || sess == nil {
		return sessionID
	}
	if realID := strings.TrimSpace(sess.CurrentSessionID()); realID != "" {
		return realID
	}
	return sessionID
}

func (h *Handlers) handleAbortGeneration(conn Connection, msg WireMessage) {
	var params AbortGenerationParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)

	h.mu.Lock()
	t, ok := h.sessions.get(params.SessionID)
	var backendID, directory string
	if ok && t != nil {
		backendID = t.backendID
		directory = t.directory
	}
	h.mu.Unlock()

	if !ok || t == nil {
		slog.Warn("go-bridge: handleAbortGeneration: session not found in registry", "sessionID", params.SessionID)
		return
	}

	sessionID := params.SessionID
	slog.Info("go-bridge: handleAbortGeneration: aborting session", "sessionID", sessionID, "backendID", backendID)

	h.mu.Lock()
	sess, deleted := h.deleteSession(sessionID)
	h.mu.Unlock()

	if deleted && sess != nil {
		if backendID == "codex" && h.codexBackendMode == "app_server" {
			if tc, ok := sess.(core.TurnCanceler); ok {
				cancelCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				_ = tc.CancelTurn(cancelCtx)
				cancel()
			}
		}
		_ = sess.Close()
	}

	if deleted {
		h.mu.Lock()
		h.seq++
		seq := h.seq
		h.mu.Unlock()

		h.broadcaster.Send(BroadcastEvent{
			BackendID: backendID,
			SessionID: sessionID,
			Directory: directory,
			Message: EventMessage{
				Type:      "event",
				SessionID: sessionID,
				BackendID: backendID,
				Event:     "turn_completed",
				Data:      map[string]interface{}{"done": true, "reason": "aborted"},
				Seq:       seq,
			},
		})

		h.broadcaster.Send(BroadcastEvent{
			BackendID: backendID,
			SessionID: sessionID,
			Directory: directory,
			Message: EventMessage{
				Type:      "event",
				SessionID: sessionID,
				BackendID: backendID,
				Event:     "session_state_changed",
				Data:      map[string]interface{}{"state": "idle"},
			},
		})

		h.recordPendingNotification(sessionID, backendID, "completed", "aborted")
	}
}

func (h *Handlers) handleCompressContext(conn Connection, msg WireMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	h.mu.Lock()
	sess, ok := h.getSession(params.SessionID)
	h.mu.Unlock()
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: "no active session for compression"})
		return
	}
	cc, ok := sess.(core.ContextCompactingSession)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend session does not support compression"})
		return
	}
	if err := cc.CompactContext(context.Background()); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "compress_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{"accepted": true}, nil)
}

func (h *Handlers) handleCheckPendingNotifications(conn Connection, msg WireMessage) {
	deviceID := ""
	if dev := conn.AuthedDevice(); dev != nil {
		deviceID = dev.DeviceID
	}
	if deviceID == "" {
		conn.SendResult(msg.RequestID, map[string]any{"notifications": []any{}}, nil)
		return
	}

	items := h.pendingNotifications.Consume(deviceID)
	if items == nil {
		items = []PendingNotification{}
	}

	conn.SendResult(msg.RequestID, map[string]any{
		"notifications": items,
	}, nil)
}

func (h *Handlers) subscribeConnToSession(conn Connection, msg WireMessage, resolvedSessionID string) {
	sessionID := resolvedSessionID
	if sessionID == "" {
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if msg.Params != nil {
			json.Unmarshal(msg.Params, &params)
		}
		sessionID = params.SessionID
	}
	if sessionID == "" {
		return
	}
	dir := extractDir(msg)
	h.broadcaster.Subscribe(conn, SubscriptionKey{
		BackendID: msg.BackendID,
		SessionID: sessionID,
		Directory: dir,
	})
}

func (h *Handlers) handleGetSession(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	resolvedSID := h.resolveSessionIDForActiveSession(params.SessionID)
	h.subscribeConnToSession(conn, msg, resolvedSID)

	dir := extractDir(msg)
	if agent.Name() == "claudecode" {
		projDir, sessPath := findClaudeSessionFile(params.SessionID, dir)
		if sessPath != "" {
			info, err := os.Stat(sessPath)
			if err == nil {
				title, _, updatedAt := scanClaudeSessionSummary(sessPath, info.ModTime())
				projectKey := filepath.Base(projDir)
				realDir := resolveProjectRealDirectory(projDir)
				if realDir == "" {
					realDir = projectKey
				}
				sessionInfo := core.AgentSessionInfo{
					ID:           params.SessionID,
					Summary:      title,
					MessageCount: 0,
					ModifiedAt:   updatedAt,
					Directory:    realDir,
				}
				conn.SendResult(msg.RequestID, map[string]interface{}{"session": sessionsToWire([]core.AgentSessionInfo{sessionInfo})[0]}, nil)
				return
			}
		}
	}

	sessions, err := agent.ListSessions(context.Background())
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}

	for _, session := range sessions {
		if session.ID == params.SessionID {
			conn.SendResult(msg.RequestID, map[string]interface{}{"session": sessionsToWire([]core.AgentSessionInfo{session})[0]}, nil)
			return
		}
	}
	conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: fmt.Sprintf("session %q not found", params.SessionID)})
}

func findClaudeSessionFile(sessionID string, optDir string) (projectDir string, sessionPath string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	projectsDir := filepath.Join(homeDir, ".claude", "projects")

	if optDir != "" {
		if _, projectPath := resolveProjectDir(optDir); projectPath != "" {
			path := filepath.Join(projectPath, sessionID+".jsonl")
			if _, err := os.Stat(path); err == nil {
				return projectPath, path
			}
		}
	}

	// 遍历所有项目目录
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(projectsDir, entry.Name(), sessionID+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return filepath.Join(projectsDir, entry.Name()), path
		}
	}
	return "", ""
}

func (h *Handlers) handleListSessions(conn Connection, msg WireMessage, agent core.Agent) {
	limit := extractPositiveInt(msg, "limit")
	metrics := newSessionLoadRequestMetrics(conn, msg)
	ctx := core.WithSessionLoadMetrics(context.Background(), metrics.context())

	// 非 claudecode backend：直接用 agent 自己的 ListSessions 实现
	if agent.Name() != "claudecode" {
		sessions, err := agent.ListSessions(ctx)
		if err != nil {
			metrics.sendResult(conn, msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
			return
		}
		mappingStarted := time.Now()
		wireSessions := sessionsToWire(sessions)
		result := paginateSessionList(wireSessions, extractStringParam(msg, "cursor"), limit)
		metrics.wireMapping += time.Since(mappingStarted)
		if ws, ok := result["sessions"].([]map[string]interface{}); ok {
			metrics.resultCount = len(ws)
		}
		metrics.sendResult(conn, msg.RequestID, result, nil)
		return
	}

	// claudecode: refresh the global fingerprinted catalog, then filter its
	// immutable snapshot instead of reparsing every project transcript.
	dir := extractDir(msg)
	projectKey := ""
	if dir != "" {
		if resolvedKey, projectPath := resolveProjectDir(dir); projectPath != "" {
			projectKey = resolvedKey
		}
	}
	mappingStarted := time.Now()
	allSessions := h.claudeSessions.list(projectKey, metrics.context())
	result := paginateSessionList(allSessions, extractStringParam(msg, "cursor"), limit)
	metrics.wireMapping += time.Since(mappingStarted)
	if ws, ok := result["sessions"].([]map[string]interface{}); ok {
		metrics.resultCount = len(ws)
	}

	metrics.sendResult(conn, msg.RequestID, result, nil)
}

func extractPositiveInt(msg WireMessage, key string) int {
	if len(msg.Params) == 0 {
		return 0
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return 0
	}
	var value int
	if err := json.Unmarshal(params[key], &value); err != nil || value <= 0 {
		return 0
	}
	return value
}

func sortSessionsByUpdatedAt(sessions []map[string]interface{}) {
	sort.Slice(sessions, func(i, j int) bool {
		mi, _ := sessions[i]["updatedAtMillis"].(int64)
		mj, _ := sessions[j]["updatedAtMillis"].(int64)
		return mi > mj
	})
}

func limitLatestSessions(sessions []map[string]interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || len(sessions) <= limit {
		return sessions
	}
	return sessions[:limit]
}

func isHiddenProjectDir(key string) bool {
	parts := strings.Split(key, "-")
	base := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	return hiddenDirectoryBases[base]
}

// resolveProjectDir 接受 project key 或真实路径，返回 (projectKey, projectPath)。
func resolveProjectDir(dir string) (string, string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	projectsDir := filepath.Join(homeDir, ".claude", "projects")

	// 1) 直接当 project key 用
	projectPath := filepath.Join(projectsDir, dir)
	if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
		return dir, projectPath
	}

	// 2) 把真实路径编码为 project key（与 cc-connect 同算法）
	absDir, _ := filepath.Abs(dir)
	key := encodeProjectKey(absDir)
	projectPath = filepath.Join(projectsDir, key)
	if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
		return key, projectPath
	}

	return "", ""
}

func encodeProjectKey(absPath string) string {
	normalized := strings.ReplaceAll(absPath, "\\", "/")
	var result strings.Builder
	for _, r := range normalized {
		if r == '/' || r == ':' || r == '_' || r == ' ' || r == '~' {
			result.WriteRune('-')
		} else if r < 128 {
			result.WriteRune(r)
		} else {
			result.WriteRune('-')
		}
	}
	return result.String()
}

func scanSessionsFromProjectDir(projectDir, projectKey string) []map[string]interface{} {
	return scanSessionsFromProjectDirWithMetrics(projectDir, projectKey, nil)
}

func scanSessionsFromProjectDirWithMetrics(projectDir, projectKey string, metrics *core.SessionLoadMetrics) []map[string]interface{} {
	realDir := resolveProjectRealDirectory(projectDir)
	if realDir == "" {
		realDir = projectKey
	}
	enumerateStarted := time.Now()
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		metrics.RecordEnumeration(time.Since(enumerateStarted), 0, 0, 0)
		return []map[string]interface{}{}
	}
	enumerateElapsed := time.Since(enumerateStarted)
	var fileCount int
	var totalBytes int64
	var maxFileBytes int64
	var result []map[string]interface{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".jsonl")
		statStarted := time.Now()
		info, err := entry.Info()
		enumerateElapsed += time.Since(statStarted)
		if err != nil {
			continue
		}
		fileCount++
		totalBytes += info.Size()
		if info.Size() > maxFileBytes {
			maxFileBytes = info.Size()
		}
		// Claude Code 可能在补写 title / last-prompt 等元数据时触碰旧 JSONL 的 mtime。
		// session 列表应展示会话内容时间，而不是文件系统更新时间。
		parseStarted := time.Now()
		title, createdAt, updatedAt := scanClaudeSessionSummary(filepath.Join(projectDir, name), info.ModTime())
		metrics.AddMetadataParse(time.Since(parseStarted))
		result = append(result, map[string]interface{}{
			"id":              sessionID,
			"title":           title,
			"messageCount":    0,
			"directory":       realDir,
			"modifiedAt":      updatedAt.Format(time.RFC3339),
			"updatedAtMillis": updatedAt.UnixMilli(),
			"createdAtMillis": createdAt.UnixMilli(),
		})
	}
	metrics.RecordEnumeration(enumerateElapsed, fileCount, totalBytes, maxFileBytes)
	return result
}

func scanClaudeSessionSummary(path string, fallbackTime time.Time) (string, time.Time, time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return "", fallbackTime, fallbackTime
	}
	defer f.Close()
	var title string
	var createdAt time.Time
	var updatedAt time.Time
	var reader io.Reader = f
	if info, statErr := f.Stat(); statErr == nil && info.Size() > claudeSessionSummaryReadLimit {
		reader = io.LimitReader(f, claudeSessionSummaryReadLimit)
		updatedAt = fallbackTime
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		var timestamp string
		if err := json.Unmarshal(raw["timestamp"], &timestamp); err == nil && timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
				if createdAt.IsZero() || parsed.Before(createdAt) {
					createdAt = parsed
				}
				if updatedAt.IsZero() || parsed.After(updatedAt) {
					updatedAt = parsed
				}
			}
		}
		if title != "" {
			continue
		}
		// 寻找第一条 assistant 消息的文本作为 title
		var msgType string
		if err := json.Unmarshal(raw["type"], &msgType); err != nil || msgType != "assistant" {
			continue
		}
		var msg struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw["message"], &msg); err != nil {
			continue
		}
		for _, c := range msg.Content {
			if c.Type == "text" && c.Text != "" {
				// 截取第一行作为 title
				lines := strings.SplitN(strings.TrimSpace(c.Text), "\n", 2)
				candidate := lines[0]
				if len(candidate) > 80 {
					candidate = candidate[:80] + "..."
				}
				title = strings.TrimSpace(candidate)
				break
			}
		}
	}
	if createdAt.IsZero() {
		createdAt = fallbackTime
	}
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	return title, createdAt, updatedAt
}

func (h *Handlers) handleGetSessionMessages(conn Connection, msg WireMessage, agent core.Agent) {
	metrics := newSessionLoadRequestMetrics(conn, msg)
	ctx := core.WithSessionLoadMetrics(context.Background(), metrics.context())
	var params GetSessionMessagesParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	params.SessionID = h.resolveSessionIDForActiveSession(params.SessionID)

	slog.Info("go-bridge: get_session_messages", "backendID", msg.BackendID, "sessionID", params.SessionID, "directory", params.Directory)

	h.subscribeConnToSession(conn, msg, params.SessionID)

	// 如果已经有活跃 session 对象（先前 send_message 创建），启动事件转发。
	// 外部 Codex turn 由进程级 EventSubscriber 转发。读取历史时不能同步
	// resume thread，否则纯读取路径会被 app-server 握手阻塞。
	h.mu.Lock()
	sess, hasSess := h.getSession(params.SessionID)
	h.mu.Unlock()
	slog.Info("go-bridge: get_session_messages session lookup", "backendID", msg.BackendID, "sessionID", params.SessionID, "hasSess", hasSess, "sessNil", sess == nil)
	if hasSess && sess != nil {
		slog.Info("go-bridge: get_session_messages — existing session, starting relay", "backendID", msg.BackendID, "sessionID", params.SessionID)
		h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
	} else {
		slog.Info("go-bridge: get_session_messages — using process-level passive subscription", "backendID", msg.BackendID, "sessionID", params.SessionID)
	}

	// list_sessions 在所有项目目录中扫描，返回的每个 session 都附带 directory 字段
	// （即 session JSONL 中的 cwd）。如果调用方传回了 directory，在拉取消息前将 agent
	// 的工作目录切到对应的项目目录，避免跨项目查找产生的 "no such file or directory"。
	if params.Directory != "" {
		switchDir(agent, params.Directory)
	}

	slog.Info("go-bridge: get_session_messages pagination request",
		"backendID", msg.BackendID,
		"sessionID", params.SessionID,
		"paginate", params.Paginate,
		"hasBeforeCursor", params.BeforeCursor != "",
		"limit", params.Limit,
	)

	// Pagination path: when the client opts in (paginate) and the backend
	// exposes a transcript locator, serve a bounded page from the transcript
	// index. Falls back to the full-parse path below when not applicable, and
	// reports cursor_stale when a backward cursor references a rewritten prefix.
	if result, perr, handled := h.servePaginatedMessages(ctx, agent, msg.BackendID, params); handled {
		if perr != nil {
			metrics.sendResult(conn, msg.RequestID, nil, &WireError{Code: "cursor_stale", Message: perr.Error()})
			return
		}
		if usage := h.getSessionContextUsage(agent, params.SessionID); usage != nil {
			result["contextUsage"] = contextUsageToWire(usage)
		}
		if msgs, ok := result["messages"].([]map[string]interface{}); ok {
			metrics.resultCount = len(msgs)
		}
		metrics.sendResult(conn, msg.RequestID, result, nil)
		return
	}

	if rhp, ok := agent.(core.RichHistoryProvider); ok {
		entries, err := rhp.GetRichSessionHistory(ctx, params.SessionID, params.Limit)
		slog.Info("go-bridge: rich history result",
			"backendID", msg.BackendID,
			"sessionID", params.SessionID,
			"directory", params.Directory,
			"limit", params.Limit,
			"entries", len(entries),
			"error", err)
		if err == nil {
			mappingStarted := time.Now()
			messages := make([]map[string]interface{}, 0, len(entries))
			for i, entry := range entries {
				wireEntry := h.richHistoryEntryToWire(entry)
				if id, _ := wireEntry["id"].(string); strings.TrimSpace(id) == "" {
					wireEntry["id"] = fmt.Sprintf("%s:%d:%s:%d", params.SessionID, i, entry.Role, entry.Timestamp.UnixMilli())
				}
				messages = append(messages, wireEntry)
			}
			result := map[string]interface{}{"messages": messages}
			if usage := h.getSessionContextUsage(agent, params.SessionID); usage != nil {
				result["contextUsage"] = contextUsageToWire(usage)
			}
			metrics.wireMapping += time.Since(mappingStarted)
			metrics.resultCount = len(messages)
			metrics.sendResult(conn, msg.RequestID, result, nil)
			return
		}
		if !errors.Is(err, core.ErrNotSupported) {
			metrics.sendResult(conn, msg.RequestID, nil, &WireError{Code: "history_failed", Message: err.Error()})
			return
		}
	}

	hp, ok := agent.(core.HistoryProvider)
	if !ok {
		metrics.sendResult(conn, msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support session history"})
		return
	}

	parseStarted := time.Now()
	entries, err := hp.GetSessionHistory(ctx, params.SessionID, params.Limit)
	metrics.context().AddHistoryParse(time.Since(parseStarted), 0)
	if err != nil {
		metrics.sendResult(conn, msg.RequestID, nil, &WireError{Code: "history_failed", Message: err.Error()})
		return
	}

	mappingStarted := time.Now()
	var result []map[string]interface{}
	for _, e := range entries {
		result = append(result, legacyHistoryEntryToWire(e))
	}

	payload := map[string]interface{}{"messages": result}
	if usage := h.getSessionContextUsage(agent, params.SessionID); usage != nil {
		payload["contextUsage"] = contextUsageToWire(usage)
	}
	metrics.wireMapping += time.Since(mappingStarted)
	metrics.resultCount = len(result)
	metrics.sendResult(conn, msg.RequestID, payload, nil)
}

func (h *Handlers) getSessionContextUsage(agent core.Agent, sessionID string) *core.ContextUsage {
	if sessionID == "" {
		return nil
	}
	h.mu.Lock()
	if sess, ok := h.getSession(sessionID); ok {
		if reporter, ok := sess.(core.ContextUsageReporter); ok {
			if usage := reporter.GetContextUsage(); usage != nil {
				h.mu.Unlock()
				return usage
			}
		}
	}
	h.mu.Unlock()

	type sessionContextUsageProvider interface {
		GetSessionContextUsage(context.Context, string) (*core.ContextUsage, error)
	}
	provider, ok := agent.(sessionContextUsageProvider)
	if !ok {
		return nil
	}
	usage, err := provider.GetSessionContextUsage(context.Background(), sessionID)
	if err != nil {
		slog.Debug("go-bridge: session context usage unavailable", "sessionID", sessionID, "error", err)
		return nil
	}
	return usage
}

func contextUsageToWire(usage *core.ContextUsage) map[string]interface{} {
	return map[string]interface{}{
		"usedTokens":            usage.UsedTokens,
		"baselineTokens":        usage.BaselineTokens,
		"totalTokens":           usage.TotalTokens,
		"inputTokens":           usage.InputTokens,
		"cachedInputTokens":     usage.CachedInputTokens,
		"outputTokens":          usage.OutputTokens,
		"reasoningOutputTokens": usage.ReasoningOutputTokens,
		"contextWindow":         usage.ContextWindow,
	}
}

func legacyHistoryEntryToWire(entry core.HistoryEntry) map[string]interface{} {
	parts := []map[string]interface{}{}
	if entry.Content != "" {
		parts = append(parts, map[string]interface{}{
			"type":    "text",
			"content": entry.Content,
		})
	}
	return map[string]interface{}{
		"role":            entry.Role,
		"content":         entry.Content,
		"timestamp":       entry.Timestamp.Format(time.RFC3339),
		"timestampMillis": entry.Timestamp.UnixMilli(),
		"parts":           parts,
		"steps":           []interface{}{},
		"files":           []interface{}{},
	}
}

func (h *Handlers) richHistoryEntryToWire(entry core.RichHistoryEntry) map[string]interface{} {
	parts := make([]interface{}, 0, len(entry.Parts))
	for _, part := range entry.Parts {
		partCopy := cloneStringAnyMap(part)
		if step, ok := partCopy["step"].(map[string]any); ok {
			stepCopy := cloneStringAnyMap(step)
			if rawOutput, ok := stepCopy["output"]; ok {
				stepID, _ := stepCopy["id"].(string)
				stepCopy["output"] = h.makeWireToolOutput(entry.ID, stepID, rawOutput)
			}
			partCopy["step"] = stepCopy
		}
		parts = append(parts, partCopy)
	}
	steps := make([]interface{}, 0, len(entry.Steps))
	for _, step := range entry.Steps {
		stepCopy := cloneStringAnyMap(step)
		if rawOutput, ok := stepCopy["output"]; ok {
			stepID, _ := stepCopy["id"].(string)
			stepCopy["output"] = h.makeWireToolOutput(entry.ID, stepID, rawOutput)
		}
		steps = append(steps, stepCopy)
	}
	files := make([]interface{}, 0, len(entry.Files))
	for _, file := range entry.Files {
		files = append(files, file)
	}
	result := map[string]interface{}{
		"id":              entry.ID,
		"role":            entry.Role,
		"content":         entry.Content,
		"timestamp":       entry.Timestamp.Format(time.RFC3339),
		"timestampMillis": entry.Timestamp.UnixMilli(),
		"parts":           parts,
		"steps":           steps,
		"files":           files,
		"agentName":       entry.AgentName,
		"modelId":         entry.ModelID,
		"providerId":      entry.ProviderID,
		"modelName":       entry.ModelName,
	}
	if entry.Thinking != "" {
		result["thinking"] = entry.Thinking
	}
	return result
}

func (h *Handlers) handleDeleteSession(conn Connection, msg WireMessage, agent core.Agent) {
	sd, ok := agent.(core.SessionDeleter)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support session deletion"})
		return
	}

	var params DeleteSessionParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	if err := sd.DeleteSession(context.Background(), params.SessionID); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "delete_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

func (h *Handlers) handleResumeSession(conn Connection, msg WireMessage, agent core.Agent) {
	var params ResumeSessionParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	slog.Info("go-bridge: handleResumeSession", "sessionID", params.SessionID, "directory", params.Directory)

	h.subscribeConnToSession(conn, msg, h.resolveSessionIDForActiveSession(params.SessionID))

	dir := params.Directory
	if dir == "" {
		h.mu.Lock()
		dir = h.sessions.directoryForSession(params.SessionID)
		h.mu.Unlock()
	}
	if dir == "" {
		dir = extractDir(msg)
	}

	// 不在这里启动 claude 进程。
	// --resume 会重放完整历史到 stdout，events channel（64 容量）会
	// 被历史事件填满导致 readLoop 阻塞，后续 send_message 无法转发响应。
	// 实际 session 创建延迟到 send_message 时按需进行。
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"id":        params.SessionID,
		"directory": dir,
	}, nil)
}

func (h *Handlers) handleSwitchModel(conn Connection, msg WireMessage, agent core.Agent) {
	ms, ok := agent.(core.ModelSwitcher)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support model switching"})
		return
	}

	var params SetModelParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	ms.SetModel(params.Model)
	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

func (h *Handlers) handleListPermissionModes(conn Connection, msg WireMessage, agent core.Agent) {
	switcher, ok := agent.(core.ModeSwitcher)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support permission mode switching"})
		return
	}

	modes := switcher.PermissionModes()
	wireModes := make([]map[string]interface{}, 0, len(modes))
	current := switcher.GetMode()
	for _, mode := range modes {
		wireModes = append(wireModes, map[string]interface{}{
			"id":            mode.Key,
			"name":          mode.Name,
			"localizedName": mode.NameZh,
			"description":   mode.Desc,
			"localizedDesc": mode.DescZh,
			"isDefault":     mode.Key == current,
		})
	}

	conn.SendResult(msg.RequestID, map[string]interface{}{
		"modes":       wireModes,
		"currentMode": current,
	}, nil)
}

func (h *Handlers) handleSetPermissionMode(conn Connection, msg WireMessage, agent core.Agent) {
	switcher, ok := agent.(core.ModeSwitcher)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support permission mode switching"})
		return
	}

	var params SetPermissionModeParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.Mode) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "mode required"})
		return
	}

	switcher.SetMode(params.Mode)
	appliesTo := "new_sessions"
	if params.SessionID != "" {
		h.mu.Lock()
		sess, ok := h.getSession(params.SessionID)
		h.mu.Unlock()
		if ok {
			if live, ok := sess.(core.LiveModeSwitcher); ok && live.SetLiveMode(switcher.GetMode()) {
				appliesTo = "current_session"
			}
		}
	}

	current := switcher.GetMode()
	conn.SendEvent(params.SessionID, msg.BackendID, "permission_mode_changed", map[string]interface{}{
		"mode":      current,
		"appliesTo": appliesTo,
	})
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"mode":      current,
		"appliesTo": appliesTo,
	}, nil)
}

func (h *Handlers) handleResolvePermission(conn Connection, msg WireMessage) {
	var params ResolvePermissionParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	h.mu.Lock()
	sess, ok := h.getSession(params.SessionID)
	h.mu.Unlock()

	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: "no active session for permission response"})
		return
	}

	result := core.PermissionResult{Behavior: params.Behavior}
	if err := sess.RespondPermission(params.RequestID, result); err != nil {
		slog.Error("go-bridge: RespondPermission failed", "error", err)
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

func (h *Handlers) handleQuestionReply(conn Connection, msg WireMessage) {
	var params struct {
		SessionID  string   `json:"sessionId"`
		QuestionID string   `json:"questionId"`
		OptionIDs  []string `json:"optionIds"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	if params.QuestionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "questionId is required"})
		return
	}

	h.mu.Lock()
	sess, ok := h.getSession(params.SessionID)
	h.mu.Unlock()

	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: "no active session for question reply"})
		return
	}

	if err := sess.RespondQuestion(params.QuestionID, params.OptionIDs); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "question_reply_failed", Message: err.Error()})
		return
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

func (h *Handlers) handleQuestionReject(conn Connection, msg WireMessage) {
	var params struct {
		SessionID  string `json:"sessionId"`
		QuestionID string `json:"questionId"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	if params.QuestionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "questionId is required"})
		return
	}

	h.mu.Lock()
	sess, ok := h.getSession(params.SessionID)
	h.mu.Unlock()

	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: "no active session for question reject"})
		return
	}

	if err := sess.RejectQuestion(params.QuestionID); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "question_reject_failed", Message: err.Error()})
		return
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

func sessionsToWire(sessions []core.AgentSessionInfo) []map[string]interface{} {
	var result []map[string]interface{}
	for _, s := range sessions {
		wire := map[string]interface{}{
			"id":              s.ID,
			"title":           s.Summary,
			"messageCount":    s.MessageCount,
			"modifiedAt":      s.ModifiedAt.Format(time.RFC3339),
			"updatedAtMillis": s.ModifiedAt.UnixMilli(),
			"createdAtMillis": s.ModifiedAt.UnixMilli(),
		}
		if s.Directory != "" {
			wire["directory"] = s.Directory
		}
		if !s.ArchivedAt.IsZero() {
			wire["archivedAtMillis"] = s.ArchivedAt.UnixMilli()
		}
		result = append(result, wire)
	}
	return result
}

func diagnosticResultsToWire(results []core.DiagnosticResult) []map[string]interface{} {
	if len(results) == 0 {
		return []map[string]interface{}{}
	}
	wire := make([]map[string]interface{}, 0, len(results))
	for _, result := range results {
		item := map[string]interface{}{
			"id":       result.ID,
			"name":     result.Name,
			"status":   result.Status,
			"message":  result.Message,
			"severity": result.Severity,
		}
		if result.FixSuggestion != "" {
			item["fixSuggestion"] = result.FixSuggestion
		}
		wire = append(wire, item)
	}
	return wire
}

func memoryFileToWire(file core.MemoryFile, includeContent bool) map[string]interface{} {
	result := map[string]interface{}{
		"id":             file.ID,
		"fileName":       file.Name,
		"description":    file.Description,
		"sizeBytes":      file.SizeBytes,
		"lastModifiedAt": file.LastModified.UTC().Format(time.RFC3339),
		"etag":           file.ETag,
		"scope":          file.Scope,
		"writable":       false,
	}
	if includeContent {
		result["content"] = file.Content
	}
	return result
}

const (
	inlineToolOutputLimitBytes = 50000
	maxContentRefEntries       = 200
)

func (h *Handlers) makeWireToolOutput(sessionID, itemID string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return h.makeWireTextOutput(sessionID, itemID, text)
		}
		stringified := stringifyToolPayload(typed)
		if len([]byte(stringified)) > inlineToolOutputLimitBytes {
			return h.storeContentRef(sessionID, itemID, stringified)
		}
		return typed
	case string:
		return h.makeWireTextOutput(sessionID, itemID, typed)
	default:
		stringified := stringifyToolPayload(value)
		if len([]byte(stringified)) > inlineToolOutputLimitBytes {
			return h.storeContentRef(sessionID, itemID, stringified)
		}
		return value
	}
}

func (h *Handlers) makeWireTextOutput(sessionID, itemID, text string) any {
	if len([]byte(text)) <= inlineToolOutputLimitBytes {
		return map[string]interface{}{"kind": "inline", "text": text}
	}
	return h.storeContentRef(sessionID, itemID, text)
}

func (h *Handlers) storeContentRef(sessionID, itemID, text string) map[string]interface{} {
	safeSessionID := sessionID
	if safeSessionID == "" {
		safeSessionID = "unknown-session"
	}
	safeItemID := itemID
	if safeItemID == "" {
		safeItemID = fmt.Sprintf("item-%d", time.Now().UnixNano())
	}
	contentID := fmt.Sprintf("content:%s:%s", safeSessionID, safeItemID)

	h.mu.Lock()
	h.contentRefs[contentID] = text
	h.contentRefOrder = append(h.contentRefOrder, contentID)
	for len(h.contentRefOrder) > maxContentRefEntries {
		oldest := h.contentRefOrder[0]
		h.contentRefOrder = h.contentRefOrder[1:]
		delete(h.contentRefs, oldest)
	}
	h.mu.Unlock()

	preview := text
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return map[string]interface{}{
		"kind":      "content_ref",
		"contentId": contentID,
		"sizeBytes": len([]byte(text)),
		"preview":   preview,
	}
}

func (h *Handlers) getContentRef(contentID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	content, ok := h.contentRefs[contentID]
	return content, ok
}

func stringifyToolPayload(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err == nil {
		return string(data)
	}
	return fmt.Sprint(value)
}

func cloneStringAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// ── param 提取 helper ─────────────────────────────────────────────────────────

func extractSessionID(msg WireMessage) string {
	if msg.Params == nil {
		return ""
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(msg.Params, &p)
	return p.SessionID
}

func extractString(msg WireMessage, key string) string {
	if msg.Params == nil {
		return ""
	}
	var p map[string]interface{}
	json.Unmarshal(msg.Params, &p)
	v, _ := p[key].(string)
	return v
}

func extractBool(msg WireMessage, key string) bool {
	if msg.Params == nil {
		return false
	}
	var p map[string]interface{}
	json.Unmarshal(msg.Params, &p)
	v, _ := p[key].(bool)
	return v
}

func extractModelParam(msg WireMessage) string {
	if msg.Params == nil {
		return ""
	}
	var p struct {
		Model string `json:"modelId"`
	}
	json.Unmarshal(msg.Params, &p)
	return p.Model
}

// ── helpers ──────────────────────────────────────────────────────────────────

func backendKindForAgent(agent core.Agent) string {
	switch agent.Name() {
	case "claudecode":
		return "claude_code"
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	default:
		return agent.Name()
	}
}

// ── read_file: iOS 端查看消息中引用的文件内容 ──────────────────────────────────

const (
	readFileMaxSize   = 2 * 1024 * 1024 // 2MB
	readFileMaxLines  = 5000
	readFileTailLines = 200
)

func (h *Handlers) handleReadFile(conn Connection, msg WireMessage) {
	var params struct {
		Path string `json:"path"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.Path == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "path required"})
		return
	}

	// 安全检查：拒绝敏感路径
	cleanPath := filepath.Clean(params.Path)
	if cleanPath == "" || cleanPath == "." {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_param", Message: "invalid path"})
		return
	}

	// 检查文件是否存在且是普通文件
	info, err := os.Stat(cleanPath)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "file_not_found", Message: err.Error()})
		return
	}
	if info.IsDir() {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "is_directory", Message: "path is a directory"})
		return
	}

	// 大小限制
	if info.Size() > readFileMaxSize {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "file_too_large",
			Message: fmt.Sprintf("file size %d bytes exceeds limit %d bytes", info.Size(), readFileMaxSize),
		})
		return
	}

	// 读取文件内容
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "read_failed", Message: err.Error()})
		return
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	truncated := false

	// 超过行数限制时截断：保留头部 + 尾部
	if totalLines > readFileMaxLines {
		headLines := readFileMaxLines - readFileTailLines
		head := lines[:headLines]
		tail := lines[totalLines-readFileTailLines:]
		content = strings.Join(head, "\n") +
			fmt.Sprintf("\n\n... (%d lines omitted) ...\n\n", totalLines-headLines-readFileTailLines) +
			strings.Join(tail, "\n")
		truncated = true
	}

	// 推断语言（用于前端高亮）
	ext := strings.TrimPrefix(filepath.Ext(cleanPath), ".")

	conn.SendResult(msg.RequestID, map[string]interface{}{
		"path":       cleanPath,
		"content":    content,
		"extension":  ext,
		"sizeBytes":  len(data),
		"totalLines": totalLines,
		"truncated":  truncated,
	}, nil)
}
