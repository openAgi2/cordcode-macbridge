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

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/transcriptindex"
)

var hiddenDirectoryBases = map[string]bool{
	"claudeprobe": true,
}

const claudeSessionSummaryReadLimit = 512 * 1024

type Handlers struct {
	mu                      sync.Mutex
	agents                  map[string]core.Agent
	sessions                *sessionRegistry
	runningMap              *runningMapCache
	opencodeSessionOptions  map[string]opencodeSessionOptions
	contentRefs             map[string]string
	contentRefOrder         []string
	seq                     int
	ocProxy                 *OpenCodeProxy
	codexBackendMode        string
	pendingNotifications    *PendingNotificationStore
	broadcaster             *Broadcaster
	relayRunning            map[string]bool   // sessionID/relayKey → 是否已有 relay goroutine
	relayRunningKind        map[string]string // sessionID → agent/file relay 类型，用于避免 Claude file relay 抢占真实 stdout relay
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
	// dataDir 是 Bridge 数据目录（--data-dir），用于持久化 iOS 端为 Claude Code
	// 显式选择的 reasoning effort 覆盖（claude-effort.json）。空表示未提供（dev 模式）。
	dataDir              string
	relayHelloHandler    func(conn Connection, msg *WireMessage)
	claudeSessions       *claudeSessionCatalog
	pendingClaudeRuntime map[string]claudeRuntimeSelection
	transcriptIndex      *transcriptindex.Store
	// capabilityPolicy 是集中式 RPC 授权层（P3 架构演进，§3.2/§8）。
	capabilityPolicy *CapabilityPolicy
	relayEnabled     bool

	// ctx is the root context whose cancellation propagates runtime shutdown
	// to active agent sessions (StartSession uses it instead of
	// context.Background()). Connection drops must NOT cancel sessions (the
	// agent outlives a single WS connection); only runtime shutdown cancels it.
	ctx context.Context
	// cleanupStop closes the StartCleanupLoop goroutine on shutdown.
	cleanupStop chan struct{}
	// shutdownOnce makes Handlers.Shutdown idempotent.
	shutdownOnce sync.Once
}

type opencodeSessionOptions struct {
	model     string
	directory string
}

func NewHandlers() *Handlers {
	return newHandlersWithContext(context.Background())
}

// NewHandlersWithContext creates a Handlers bound to the given root context.
// Cancelling ctx propagates shutdown to active agent sessions. Prefer this in
// main() so SIGTERM/management shutdown reaches in-flight turns.
func NewHandlersWithContext(ctx context.Context) *Handlers {
	return newHandlersWithContext(ctx)
}

func newHandlersWithContext(ctx context.Context) *Handlers {
	prekeys := NewPrekeyStore("")
	observation := NewObservationManager()
	outbox := NewOutboxManager(prekeys)
	presentation := NewPresentationManager()
	h := &Handlers{
		agents:                 make(map[string]core.Agent),
		sessions:               newSessionRegistry(),
		opencodeSessionOptions: make(map[string]opencodeSessionOptions),
		contentRefs:            make(map[string]string),
		broadcaster:            NewBroadcaster(),
		pendingNotifications:   NewPendingNotificationStore(),
		relayRunning:           make(map[string]bool),
		relayRunningKind:       make(map[string]string),
		deliveryPrekeys:        prekeys,
		observation:            observation,
		relayOutbox:            outbox,
		presentation:           presentation,
		relayEventRouter:       NewRelayEventRouter(observation, outbox, prekeys, NewMailboxService(NewRelayHub()), presentation),
		claudeSessions:         newDefaultClaudeSessionCatalog(),
		pendingClaudeRuntime:   make(map[string]claudeRuntimeSelection),
		transcriptIndex:        transcriptindex.NewStore(defaultTranscriptIndexDir()),
		capabilityPolicy:       NewCapabilityPolicy(),
		relayEnabled:           true,
		ctx:                    ctx,
		cleanupStop:            make(chan struct{}),
	}
	// TTL cache for the Claude running map (Fix 3). The recompute closure binds to
	// whatever claudecode agent is currently registered, so the cache is valid
	// across register/unregister. Invalidated on session-registry state changes.
	h.runningMap = newRunningMapCache(func(ctx context.Context) (map[string]bool, error) {
		agent, ok := h.getFirstAgentByName("claudecode")
		if !ok {
			return nil, nil
		}
		lister, ok := agent.(core.RunningSessionLister)
		if !ok {
			return nil, nil
		}
		return lister.GetRunningSessionIDs(ctx)
	})
	h.sessions.onStateChange = func(backendID, sessionID, newState string) {
		// Invalidate the Claude running map on any tracked state transition
		// (send_message / turn_started / turn_completed / abort / process exit).
		// The cache holds only Claude state; backendID filtering would miss
		// resume-markRunning on a not-yet-registered session, so invalidate
		// unconditionally — the cost is one map nil and at most one extra
		// GetRunningSessionIDs on the next list_sessions.
		h.runningMap.invalidate()
	}
	return h
}

func (h *Handlers) SetRelayEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.relayEnabled = enabled
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

// SetDataDir 记录 Bridge 数据目录，用于持久化 iOS 端为 Claude Code 显式选择的
// reasoning effort 覆盖。应在 agent 注册前由 server（main）调用。
func (h *Handlers) SetDataDir(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dataDir = dir
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

// Start launches background goroutines that NewHandlers no longer auto-starts
// (T09): the observation lease-check loop. Idempotent. main() calls this once
// after NewHandlersWithContext(ctx); Shutdown stops it. Tests that need lease
// expiry must call Start too.
func (h *Handlers) Start(ctx context.Context) {
	if h.observation != nil {
		h.observation.Start(ctx)
	}
}

// StartCleanupLoop launches the idle-session reaper. It stops when the root
// context is cancelled or Shutdown closes h.cleanupStop. Uses a stoppable
// time.NewTicker instead of time.Tick (which can never be stopped).
func (h *Handlers) StartCleanupLoop(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.cleanupIdleSessions()
			case <-h.ctx.Done():
				return
			case <-h.cleanupStop:
				return
			}
		}
	}()
}

// Shutdown stops background goroutines (cleanup loop, observation lease
// checker) and closes every active agent session in the registry, bounded by
// ctx's deadline. Idempotent. Callers (main shutdown, tests) use this instead
// of relying on process exit to reclaim agent subprocesses.
func (h *Handlers) Shutdown(ctx context.Context) error {
	h.shutdownOnce.Do(func() {
		// Stop accepting reaps and stop the observation lease loop.
		close(h.cleanupStop)
		if h.observation != nil {
			h.observation.Stop()
		}

		// Snapshot active sessions under the lock and clear the registry so
		// new lookups observe the shutdown. Close each session outside the lock
		// to avoid holding it across a potentially blocking Close().
		h.mu.Lock()
		toClose := h.sessions.drain()
		h.mu.Unlock()

		// Close each session honoring the caller's deadline so a wedged agent
		// can't hang shutdown forever. Each AgentSession.Close has its own
		// internal escalation (SIGTERM→SIGKILL / process-group kill).
		done := make(chan struct{})
		go func() {
			var wg sync.WaitGroup
			for _, sess := range toClose {
				wg.Add(1)
				go func(s core.AgentSession) {
					defer wg.Done()
					closeWithTimeout(s, ctx)
				}(sess)
			}
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			slog.Warn("go-bridge: handlers.Shutdown deadline exceeded, some sessions may not have closed cleanly")
		}
	})
	return nil
}

// closeWithTimeout closes a session but does not block longer than the parent
// ctx allows. AgentSession.Close already has its own internal escalation
// (SIGTERM→SIGKILL / process-group kill); this is the outer bound.
func closeWithTimeout(sess core.AgentSession, ctx context.Context) {
	if sess == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sess.Close()
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
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
		backends = append(backends, BackendInfo{
			ID:           id,
			Kind:         backendKindForAgent(agent),
			DisplayName:  agent.Name(),
			Capabilities: deriveBackendCapabilities(id, agent, h.codexBackendMode),
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

func (h *Handlers) getFirstAgentByName(name string) (core.Agent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, agent := range h.agents {
		if agent != nil && agent.Name() == name {
			return agent, true
		}
	}
	return nil, false
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

	newSession, err := agent.StartSession(h.ctx, sessionID)
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

	// P3：集中式 capability policy 在 dispatch 前评估敏感方法（§3.2/§8）。
	if perr := h.capabilityPolicy.AuthorizeRPC(conn, msg); perr != nil {
		conn.SendResult(msg.RequestID, nil, perr)
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
	case msg.Type == "ping":
		// 应用层 ping/pong（走 data frame，CF 必透传；与直连路径 server.go 对称）。
		// iOS 经 relay 的判活改用应用层 ping/pong 后，靠此回包；不依赖被 CF 代理/干扰的
		// WebSocket control-frame ping/pong。
		conn.SendJSON(map[string]string{"type": "pong"})
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

	h.mu.Lock()
	enabled := h.relayEnabled
	h.mu.Unlock()
	if !enabled {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.not_configured", Message: "encrypted relay is disabled"})
		return true
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
	case "get_workspace_diff":
		h.handleGetWorkspaceDiff(conn, msg, agent)
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
	case "list_directory":
		h.handleListDirectory(conn, msg)
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
	// claudecode 后端的所有模型都经 claude CLI，属 claude provider。显式标 "claude"，
	// 否则无前缀的别名（haiku/sonnet/opus）会被解析成 "default"，被 iOS 的
	// providerID=="claude" 过滤丢弃（见 docs/2026-06-30-claudecode-models-from-settings-json.md §1.3）。
	if agent.Name() == "claudecode" {
		return id, "claude", "claude"
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

	if agent.Name() == "codex" || agent.Name() == "claudecode" {
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

	sess, err := agent.StartSession(h.ctx, "")
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
	conn.SendResult(msg.RequestID, h.enrichSessionState(result), nil)
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
	applySendMessageRuntimeOptions(agent, params, h.dataDir)
	claudeRuntime := claudeRuntimeSelectionFromAgent(agent, params)

	// P1-5: 默认日志不记录用户消息正文，仅记录长度，避免 prompt/源码/凭据进入日志、崩溃包或诊断。
	slog.Info("go-bridge: handleSendMessage", "sessionID", params.SessionID, "contentLen", len(params.Content))
	h.mu.Lock()
	sess, ok := h.getSession(params.SessionID)
	h.mu.Unlock()

	// ok=true 但 sess==nil 表示 registry 里只有 markRunning/markIdle 建的占位 stub，
	// 尚无真实 agent 会话——必须走 StartSession（对真实 id 即 --resume）续接，
	// 否则下面 sess.Send 会对 nil 接口派发而 panic（2026-06-30 真机复现的崩溃）。
	if !ok || sess == nil {
		resumeID := params.SessionID
		if strings.HasPrefix(resumeID, "pending-") {
			resumeID = ""
		}
		slog.Info("go-bridge: handleSendMessage: session not found in registry. Starting new agent session.", "sessionID", params.SessionID, "resumeID", resumeID)
		var err error
		sess, err = agent.StartSession(h.ctx, resumeID)
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
			if agent.Name() == "claudecode" && strings.HasPrefix(params.SessionID, "pending-") {
				h.pendingClaudeRuntime[params.SessionID] = claudeRuntime
			}
			h.mu.Unlock()
		}
	} else {
		slog.Info("go-bridge: handleSendMessage: found active session in registry", "sessionID", params.SessionID)
	}

	// 通知 iOS 进入 running 状态
	conn.SendEvent(params.SessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "running"})
	h.broadcaster.Send(BroadcastEvent{
		BackendID: msg.BackendID,
		SessionID: params.SessionID,
		Directory: extractDir(msg),
		Message: EventMessage{
			Type:      "event",
			SessionID: params.SessionID,
			BackendID: msg.BackendID,
			Event:     "session_state_changed",
			Data:      map[string]interface{}{"state": "running"},
		},
	})
	h.sessions.markRunning(params.SessionID)
	if agent.Name() == "claudecode" && !strings.HasPrefix(params.SessionID, "pending-") {
		h.writeClaudeRuntimeSidecar(params.SessionID, extractDir(msg), claudeRuntime)
	}

	// 订阅该 session 的事件
	dir := extractDir(msg)
	h.broadcaster.Subscribe(conn, SubscriptionKey{
		BackendID: msg.BackendID,
		SessionID: params.SessionID,
		Directory: dir,
	})

	images, files := splitAttachments(params.Attachments)
	if err := sess.Send(params.Content, images, files); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "send_failed", Message: err.Error()})
		return
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
}

func applySendMessageRuntimeOptions(agent core.Agent, params SendMessageParams, dataDir string) {
	if modelID := selectedModelParam(agent, params.Model); modelID != "" {
		if ms, ok := agent.(core.ModelSwitcher); ok {
			ms.SetModel(modelID)
		}
	}
	if params.ReasoningEffort != "" {
		if re, ok := agent.(core.ReasoningEffortSwitcher); ok {
			// 仅在 effort 实际变化时持久化：避免每条消息都写文件，也避免把回显的
			// settings.json 默认值当成显式 override 落盘。持久化的值代表「该 bridge 的
			// Claude 最近一次实际使用的 effort」，重启后作为 override 优先于 settings.json。
			prev := re.GetReasoningEffort()
			re.SetReasoningEffort(params.ReasoningEffort)
			if agent.Name() == "claudecode" &&
				normalizeClaudeRuntimeEffort(params.ReasoningEffort) != normalizeClaudeRuntimeEffort(prev) {
				saveClaudeEffortOverride(dataDir, params.ReasoningEffort)
			}
		}
	}
}

func selectedModelParam(agent core.Agent, model map[string]interface{}) string {
	if model == nil {
		return ""
	}
	if agent.Name() == "codex" || agent.Name() == "claudecode" {
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

type claudeRuntimeSelection struct {
	ModelID         string
	ProviderID      string
	ReasoningEffort string
}

func claudeRuntimeSelectionFromAgent(agent core.Agent, params SendMessageParams) claudeRuntimeSelection {
	if agent.Name() != "claudecode" {
		return claudeRuntimeSelection{}
	}
	modelID := selectedModelParam(agent, params.Model)
	if modelID == "" {
		if ms, ok := agent.(core.ModelSwitcher); ok {
			modelID = strings.TrimSpace(ms.GetModel())
		}
	}
	_, _, providerID := modelProviderForAgent(agent, modelID)
	effort := strings.TrimSpace(params.ReasoningEffort)
	if effort == "" {
		if re, ok := agent.(core.ReasoningEffortSwitcher); ok {
			effort = strings.TrimSpace(re.GetReasoningEffort())
		}
	}
	return claudeRuntimeSelection{
		ModelID:         strings.TrimSpace(modelID),
		ProviderID:      strings.TrimSpace(providerID),
		ReasoningEffort: normalizeClaudeRuntimeEffort(effort),
	}
}

func normalizeClaudeRuntimeEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return "low"
	case "medium", "med":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "extra-high", "extra_high", "extra high":
		return "xhigh"
	case "max":
		return "max"
	case "ultra", "ultra-code", "ultra_code", "ultracode":
		return "ultra"
	default:
		return ""
	}
}

type historyDrainWaiter interface {
	WaitForHistoryDrain(context.Context) bool
}

// drainHistoryEvents 等待 claude --resume 的历史重放窗口关闭。
// 旧实现用“100ms 没有事件”推断历史已排空，但 Claude CLI 启动/重放经常会有
// 更长空窗；随后真实 send 的输出会落在 historyDraining 窗口里被吞掉。
// Claude session 现在暴露权威 drain 信号：result 或内部 watchdog 关闭窗口后再发送。
func drainHistoryEvents(sess core.AgentSession) {
	if waiter, ok := sess.(historyDrainWaiter); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if waiter.WaitForHistoryDrain(ctx) {
			return
		}
		slog.Warn("go-bridge: timed out waiting for Claude resume history drain")
		return
	}

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
	h.rebindRelayKind(currentID, realID, relayKindAgent)
	if backendID == "claude" || backendID == "claudecode" {
		h.mu.Lock()
		selection := h.pendingClaudeRuntime[currentID]
		delete(h.pendingClaudeRuntime, currentID)
		h.mu.Unlock()
		h.writeClaudeRuntimeSidecar(realID, directory, selection)
	}
	return realID
}

func (h *Handlers) sendSessionEvent(sessionID, backendID, eventName string, data interface{}) {
	h.mu.Lock()
	h.seq++
	seq := h.seq
	dir := h.sessions.directoryForSession(sessionID)
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
		Directory: dir,
		Message:   msg,
	})
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
				scan := scanClaudeSessionMetadata(sessPath, info.ModTime())
				projectKey := filepath.Base(projDir)
				realDir := resolveProjectRealDirectory(projDir)
				if realDir == "" {
					realDir = projectKey
				}
				sessionInfo := core.AgentSessionInfo{
					ID:              params.SessionID,
					Summary:         scan.Title,
					MessageCount:    0,
					ModifiedAt:      scan.UpdatedAt,
					Directory:       realDir,
					ModelID:         scan.ModelID,
					ProviderID:      scan.ProviderID,
					ReasoningEffort: scan.ReasoningEffort,
				}
				wireSession := sessionsToWire([]core.AgentSessionInfo{sessionInfo})[0]
				conn.SendResult(msg.RequestID, map[string]interface{}{"session": h.enrichSessionStateWithAgent(wireSession, agent)}, nil)
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
			wireSession := sessionsToWire([]core.AgentSessionInfo{session})[0]
			conn.SendResult(msg.RequestID, map[string]interface{}{"session": h.enrichSessionStateWithAgent(wireSession, agent)}, nil)
			return
		}
	}
	conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: fmt.Sprintf("session %q not found", params.SessionID)})
}

func findClaudeSessionFile(sessionID string, optDir string) (projectDir string, sessionPath string) {
	transcriptStateProbe()
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
		wireSessions = h.enrichSessionStatesForList(wireSessions, agent, h.getRunningMap(ctx, agent))
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
	allSessions = h.enrichSessionStatesForList(allSessions, agent, h.getRunningMap(ctx, agent))
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
		scan := scanClaudeSessionMetadata(filepath.Join(projectDir, name), info.ModTime())
		metrics.AddMetadataParse(time.Since(parseStarted))
		wire := map[string]interface{}{
			"id":              sessionID,
			"title":           scan.Title,
			"messageCount":    0,
			"directory":       realDir,
			"modifiedAt":      scan.UpdatedAt.Format(time.RFC3339),
			"updatedAtMillis": scan.UpdatedAt.UnixMilli(),
			"createdAtMillis": scan.CreatedAt.UnixMilli(),
		}
		if scan.ModelID != "" {
			wire["modelId"] = scan.ModelID
			wire["effectiveModelId"] = scan.ModelID
		}
		if scan.ProviderID != "" {
			wire["providerId"] = scan.ProviderID
			wire["effectiveProviderId"] = scan.ProviderID
		}
		if scan.ReasoningEffort != "" {
			wire["reasoningEffort"] = scan.ReasoningEffort
		}
		result = append(result, wire)
	}
	metrics.RecordEnumeration(enumerateElapsed, fileCount, totalBytes, maxFileBytes)
	return result
}

type claudeSessionScanResult struct {
	Title           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ModelID         string
	ProviderID      string
	ReasoningEffort string
}

type claudeBridgeSessionSidecar struct {
	ArchivedAtMillis int64  `json:"archivedAtMillis,omitempty"`
	ModelID          string `json:"modelId,omitempty"`
	ProviderID       string `json:"providerId,omitempty"`
	ReasoningEffort  string `json:"reasoningEffort,omitempty"`
}

func scanClaudeSessionSummary(path string, fallbackTime time.Time) (string, time.Time, time.Time) {
	scan := scanClaudeSessionMetadata(path, fallbackTime)
	return scan.Title, scan.CreatedAt, scan.UpdatedAt
}

func scanClaudeSessionMetadata(path string, fallbackTime time.Time) claudeSessionScanResult {
	f, err := os.Open(path)
	if err != nil {
		return claudeSessionScanResult{CreatedAt: fallbackTime, UpdatedAt: fallbackTime}
	}
	defer f.Close()
	sidecar := readClaudeBridgeSessionSidecar(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), ".jsonl"))
	var title string
	var assistantTitle string
	var modelID string
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
		var msgType string
		if err := json.Unmarshal(raw["type"], &msgType); err != nil {
			continue
		}
		if msgType == "custom-title" {
			var customTitle string
			if err := json.Unmarshal(raw["customTitle"], &customTitle); err == nil {
				if trimmed := strings.TrimSpace(customTitle); trimmed != "" {
					title = trimmed
				}
			}
			continue
		}
		if msgType != "assistant" {
			continue
		}
		// Claude Code 没有生成 custom-title 时，退回第一条 assistant 文本。
		var msg struct {
			Model   string `json:"model"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw["message"], &msg); err != nil {
			continue
		}
		if model := strings.TrimSpace(msg.Model); model != "" && modelID == "" {
			modelID = model
		}
		if assistantTitle != "" {
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
				assistantTitle = strings.TrimSpace(candidate)
				break
			}
		}
	}
	if title == "" {
		title = assistantTitle
	}
	if createdAt.IsZero() {
		createdAt = fallbackTime
	}
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	providerID := ""
	if sidecar.ModelID != "" {
		modelID = sidecar.ModelID
	}
	if sidecar.ProviderID != "" {
		providerID = sidecar.ProviderID
	}
	if modelID != "" {
		if providerID == "" {
			_, _, providerID = parseModelID(modelID)
		}
	}
	return claudeSessionScanResult{
		Title:           title,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		ModelID:         modelID,
		ProviderID:      providerID,
		ReasoningEffort: normalizeClaudeRuntimeEffort(sidecar.ReasoningEffort),
	}
}

func claudeBridgeSessionSidecarPath(projectDir, sessionID string) string {
	return filepath.Join(projectDir, ".cc-connect-session-meta", sessionID+".json")
}

func readClaudeBridgeSessionSidecar(projectDir, sessionID string) claudeBridgeSessionSidecar {
	data, err := os.ReadFile(claudeBridgeSessionSidecarPath(projectDir, sessionID))
	if err != nil {
		return claudeBridgeSessionSidecar{}
	}
	var sidecar claudeBridgeSessionSidecar
	if err := json.Unmarshal(data, &sidecar); err != nil {
		return claudeBridgeSessionSidecar{}
	}
	sidecar.ModelID = strings.TrimSpace(sidecar.ModelID)
	sidecar.ProviderID = strings.TrimSpace(sidecar.ProviderID)
	sidecar.ReasoningEffort = normalizeClaudeRuntimeEffort(sidecar.ReasoningEffort)
	return sidecar
}

func (h *Handlers) writeClaudeRuntimeSidecar(sessionID, directory string, selection claudeRuntimeSelection) {
	if selection.ModelID == "" && selection.ProviderID == "" && selection.ReasoningEffort == "" {
		return
	}
	projectDir, _ := findClaudeSessionFile(sessionID, directory)
	if projectDir == "" && strings.TrimSpace(directory) != "" {
		_, projectDir = resolveProjectDir(directory)
	}
	if projectDir == "" {
		return
	}
	sidecar := readClaudeBridgeSessionSidecar(projectDir, sessionID)
	if selection.ModelID != "" {
		sidecar.ModelID = selection.ModelID
	}
	if selection.ProviderID != "" {
		sidecar.ProviderID = selection.ProviderID
	}
	if selection.ReasoningEffort != "" {
		sidecar.ReasoningEffort = selection.ReasoningEffort
	}
	dir := filepath.Dir(claudeBridgeSessionSidecarPath(projectDir, sessionID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("go-bridge: create claude session sidecar dir failed", "sessionID", sessionID, "error", err)
		return
	}
	path := claudeBridgeSessionSidecarPath(projectDir, sessionID)
	data, err := json.Marshal(sidecar)
	if err != nil {
		slog.Warn("go-bridge: marshal claude session sidecar failed", "sessionID", sessionID, "error", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		slog.Warn("go-bridge: write claude session sidecar failed", "sessionID", sessionID, "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("go-bridge: replace claude session sidecar failed", "sessionID", sessionID, "error", err)
	}
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
	// 纯读取历史时不能同步 resume thread，否则 app-server/CLI 握手会把只读路径变成执行路径。
	h.mu.Lock()
	sess, hasSess := h.getSession(params.SessionID)
	h.mu.Unlock()
	slog.Info("go-bridge: get_session_messages session lookup", "backendID", msg.BackendID, "sessionID", params.SessionID, "hasSess", hasSess, "sessNil", sess == nil)
	if hasSess && sess != nil {
		slog.Info("go-bridge: get_session_messages — existing session, starting relay", "backendID", msg.BackendID, "sessionID", params.SessionID)
		h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
	} else {
		slog.Info("go-bridge: get_session_messages — no active session, reading persisted history", "backendID", msg.BackendID, "sessionID", params.SessionID)
		// 对于没有 AgentSession 的 claudecode session（外部 Desktop 创建），
		// 启动基于 transcript 文件监视的事件转发。
		h.startClaudeSessionFileRelay(params.SessionID, conn, msg.BackendID)
	}
	// Codex Desktop/共享服务 session 的真实完成信号会落到 JSONL 的 task_complete。
	// 即使 registry 里已有 AgentSession，标准 relay 也可能收不到外部 turn 的最终事件；
	// 因此 Codex transcript relay 使用独立 key 与标准 relay 并行。
	h.startCodexSessionFileRelay(params.SessionID, conn, msg.BackendID, agent)

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

	// 对于 claudecode session：如果没有活跃 AgentSession（外部 Desktop 创建），
	// 启动基于 transcript 文件监视的事件转发，使 iOS 能收到 turn_started/turn_completed 等事件。
	if agent.Name() == "claudecode" {
		h.mu.Lock()
		sess, hasSess := h.getSession(params.SessionID)
		h.mu.Unlock()
		if !hasSess || sess == nil {
			h.startClaudeSessionFileRelay(params.SessionID, conn, msg.BackendID)
		}
	}
	if agent.Name() == "codex" {
		h.startCodexSessionFileRelay(params.SessionID, conn, msg.BackendID, agent)
	}

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
	result := map[string]interface{}{
		"id":        params.SessionID,
		"directory": dir,
	}
	conn.SendResult(msg.RequestID, h.enrichSessionStateWithAgent(result, agent), nil)
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
		if s.ModelID != "" {
			wire["modelId"] = s.ModelID
			wire["effectiveModelId"] = s.ModelID
		}
		if s.ProviderID != "" {
			wire["providerId"] = s.ProviderID
			wire["effectiveProviderId"] = s.ProviderID
		}
		if s.ReasoningEffort != "" {
			wire["reasoningEffort"] = s.ReasoningEffort
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
		Path      string `json:"path"`
		Directory string `json:"directory,omitempty"`
		SessionID string `json:"sessionId,omitempty"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.Path == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "path required"})
		return
	}

	authorizedRoot, err := h.authorizedReadFileRoot(msg, params.Directory, params.SessionID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "file.outside_authorized_root", Message: "file is outside the authorized workspace"})
		return
	}

	resolvedPath, info, err := resolveAuthorizedReadFilePath(authorizedRoot, params.Path)
	if err != nil {
		var wireErr *WireError
		if errors.As(err, &wireErr) {
			conn.SendResult(msg.RequestID, nil, wireErr)
		} else {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "file_not_found", Message: "file not found"})
		}
		return
	}

	if info.Size() > readFileMaxSize {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "file_too_large",
			Message: fmt.Sprintf("file size %d bytes exceeds limit %d bytes", info.Size(), readFileMaxSize),
		})
		return
	}

	file, err := os.Open(resolvedPath)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "read_failed", Message: "failed to open file"})
		return
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "file.changed_during_read", Message: "file changed during authorization"})
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, readFileMaxSize+1))
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "read_failed", Message: "failed to read file"})
		return
	}
	if len(data) > readFileMaxSize {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "file_too_large", Message: "file exceeds size limit"})
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
	ext := strings.TrimPrefix(filepath.Ext(resolvedPath), ".")

	conn.SendResult(msg.RequestID, map[string]interface{}{
		"path":       resolvedPath,
		"content":    content,
		"extension":  ext,
		"sizeBytes":  len(data),
		"totalLines": totalLines,
		"truncated":  truncated,
	}, nil)
}

// ── list_directory: iOS 端远程选择/浏览 Mac 本地文件夹 ──────────────────────────────

func (h *Handlers) handleListDirectory(conn Connection, msg WireMessage) {
	var params struct {
		Path string `json:"path"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	resolvedPath, err := expandPath(params.Path)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_path", Message: err.Error()})
		return
	}

	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "read_failed", Message: err.Error()})
		return
	}

	type directoryItem struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		IsDirectory bool   `json:"isDirectory"`
	}

	var items []directoryItem
	for _, entry := range entries {
		name := entry.Name()
		// 过滤隐藏文件/文件夹 (以 . 开头)
		if strings.HasPrefix(name, ".") {
			continue
		}
		itemPath := filepath.Join(resolvedPath, name)
		items = append(items, directoryItem{
			Name:        name,
			Path:        itemPath,
			IsDirectory: entry.IsDir(),
		})

		// 限制单次返回条数，避免大文件夹内存爆满
		if len(items) >= 1000 {
			break
		}
	}

	conn.SendResult(msg.RequestID, map[string]interface{}{
		"currentPath": resolvedPath,
		"items":       items,
	}, nil)
}

func expandPath(path string) (string, error) {
	if path == "" || path == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return filepath.Abs(filepath.Clean(path))
}

func (h *Handlers) authorizedReadFileRoot(msg WireMessage, requestedDir, paramsSessionID string) (string, error) {
	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = paramsSessionID
	}
	if sessionID != "" {
		if dir := h.sessions.directoryForSession(sessionID); dir != "" {
			return matchAuthorizedReadFileRoot(dir, requestedDir)
		}
	}

	agent, ok := h.getAgent(msg.BackendID)
	if !ok {
		return "", errors.New("backend not found")
	}
	workDirAgent, ok := agent.(core.WorkDirSwitcher)
	if !ok || workDirAgent.GetWorkDir() == "" {
		return "", errors.New("backend has no authorized workspace")
	}
	return matchAuthorizedReadFileRoot(workDirAgent.GetWorkDir(), requestedDir)
}

func matchAuthorizedReadFileRoot(serverRoot, requestedDir string) (string, error) {
	root, err := canonicalExistingDirectory(serverRoot)
	if err != nil {
		return "", err
	}
	if requestedDir == "" {
		return root, nil
	}
	requested, err := canonicalExistingDirectory(requestedDir)
	if err != nil {
		return "", errors.New("requested directory is not within the authorized workspace")
	}
	// 授权根始终是 serverRoot（workspace 根）。requestedDir 可能等于 root，也可能是 root 的子目录
	// （前端浏览子目录时传入）。只要 requested 在 root 之内即接受，避免误拒合法子目录调用；
	// 越界（requested 在 root 之外）才拒绝。真正的越界校验对最终读取的 path 仍由 resolveAuthorizedReadFilePath 完成。
	if requested != root && !pathIsWithinRoot(root, requested) {
		return "", errors.New("requested directory is outside the authorized workspace")
	}
	return root, nil
}

func canonicalExistingDirectory(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("authorized workspace is not a directory")
	}
	return resolved, nil
}

func resolveAuthorizedReadFilePath(root, requestedPath string) (string, os.FileInfo, error) {
	cleanPath := filepath.Clean(requestedPath)
	if cleanPath == "" || cleanPath == "." {
		return "", nil, &WireError{Code: "invalid_param", Message: "invalid path"}
	}

	candidate := cleanPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", nil, &WireError{Code: "file.outside_authorized_root", Message: "file is outside the authorized workspace"}
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(candidateAbs))
	if err != nil || !pathIsWithinRoot(root, filepath.Join(resolvedParent, filepath.Base(candidateAbs))) {
		return "", nil, &WireError{Code: "file.outside_authorized_root", Message: "file is outside the authorized workspace"}
	}

	resolved, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		return "", nil, err
	}
	if !pathIsWithinRoot(root, resolved) {
		return "", nil, &WireError{Code: "file.symlink_escape", Message: "file symlink escapes the authorized workspace"}
	}
	if isSensitiveReadFilePath(resolved) {
		return "", nil, &WireError{Code: "file.sensitive_path_denied", Message: "sensitive file access is denied"}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, &WireError{Code: "invalid_file_type", Message: "path is not a regular file"}
	}
	return resolved, info, nil
}

func pathIsWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isSensitiveReadFilePath(path string) bool {
	lowerPath := strings.ToLower(filepath.Clean(path))
	base := filepath.Base(lowerPath)
	switch base {
	case "management-token", "relay_identity.key", "devices.json":
		return true
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}

	parts := strings.Split(filepath.ToSlash(lowerPath), "/")
	for i, part := range parts {
		switch part {
		case ".ssh", ".aws", ".claude", ".codex":
			return true
		case ".config":
			if i+1 < len(parts) && (parts[i+1] == "gcloud" || parts[i+1] == "opencode") {
				return true
			}
		}
	}
	return false
}
