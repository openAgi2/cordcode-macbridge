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

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/filepool"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/readfile"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
	"github.com/openAgi2/cordcode-macbridge/transcriptindex"
)

var hiddenDirectoryBases = map[string]bool{
	"claudeprobe": true,
}

const (
	claudeSessionSummaryReadLimit = 512 * 1024
	defaultSessionListLimit       = 100
	maxSessionListLimit           = 150
)

type Handlers struct {
	mu                     sync.Mutex
	agents                 map[string]core.Agent
	sessions               *sessionRegistry
	runningMap             *runningMapCache
	opencodeSessionOptions map[string]opencodeSessionOptions
	contentRefs            map[string]string
	contentRefOrder        []string
	ocProxy                *OpenCodeProxy
	// catalogWireCache 缓存 OpenCode list_sessions 的 enriched wire maps（§4.1.1 / §5.3#3，
	// Phase 1C）。仅对声明 catalog_cursor_epoch_v2 的连接使用；未声明连接走既有 v1
	// paginateSessionList 路径不变。lazy init（sync.Once），构造路径 NewHandlers 不改。
	catalogWireCache    *catalogWireSnapshotCache
	catalogWireInitOnce sync.Once
	codexBackendMode       string
	pendingNotifications   *PendingNotificationStore
	broadcaster            *Broadcaster
	eventPublisher         *EventPublisher
	projectionKernel       *ProjectionKernel
	projectionHydrateSlots chan struct{}
	// checkpointCoalescer owns the §6.1 git-checkpoint write path (parallel to,
	// but deliberately separate from, the projection coalescer). It is wired to
	// the kernel via SetTurnCheckpointStager; the kernel calls it from IngestLive
	// after each turn_completed. nil in unit tests that don't exercise capture.
	checkpointCoalescer *checkpointCoalescer
	// deltaBatcher（Fix 5）：text_delta/reasoning_delta 时间窗攒批，降低上游每 token 一帧
	// 的 WS/HPKE/日志开销。relayEvents / startPassiveSubscription 通过它下发，而非直接 broadcaster.Send。
	deltaBatcher     *DeltaBatcher
	relayRunning     map[string]bool   // sessionID/relayKey → 是否已有 relay goroutine
	relayRunningKind map[string]string // sessionID → agent/file relay 类型，用于避免 Claude file relay 抢占真实 stdout relay
	// agentRelayRunning 与 relayRunningKind 解耦：标记 agent relay (relayEvents) goroutine 是否在跑。
	// 本地发 turn 时若 file relay 已占用全局槽位 (kind=claude_file)，startRelayIfNotRunning 不再把 kind
	// 翻成 agent，避免 claudeSessionFileRelayLoop 被 superseded 退出而丢失唯一 UUID 内容来源（见
	// startRelayIfNotRunning 注释与 Issue 3 调查 docs/2026-07-30-remote-web-send-message-not-live-investigation.md）。
	agentRelayRunning       map[string]bool
	claudeSourceCorrelation *claudeSourceCorrelationTracker
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
	// filePool 是 §3.6.3 的全局专用 bounded file-read worker pool，把 read_file_v2
	// 的 I/O 从 per-device inbound scheduler 解耦。nil 时（部分单测）handleReadFileV2
	// 回退到同步内联读，不阻塞测试。
	filePool         *filepool.Pool
	admission        *admission.AdmissionMachine
	bridgeOwnedTurns map[string]struct{}
	relayEnabled     bool
	sessionListLimit int

	// pinStore persists MacBridge-owned session pin (置顶) metadata. Injected from main()
	// (under the bridge data dir) via SetPinStore; nil in tests that don't exercise pinning.
	// The set_session_pinned / list_pinned_sessions handlers use it; drivers receive their
	// own reference via opts["pin_store"] at construction.
	pinStore *pinstore.Store

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
	return newHandlersWithContext(context.Background(), mustGenerateBridgeEpoch())
}

// NewHandlersWithContext creates a Handlers bound to the given root context.
// Cancelling ctx propagates shutdown to active agent sessions. Prefer this in
// main() so SIGTERM/management shutdown reaches in-flight turns.
func NewHandlersWithContext(ctx context.Context) *Handlers {
	return newHandlersWithContext(ctx, mustGenerateBridgeEpoch())
}

func NewHandlersWithContextAndEpoch(ctx context.Context, bridgeEpoch string) *Handlers {
	return newHandlersWithContext(ctx, bridgeEpoch)
}

func mustGenerateBridgeEpoch() string {
	epoch, err := generateBridgeEpoch()
	if err != nil {
		panic(err)
	}
	return epoch
}

func newHandlersWithContext(ctx context.Context, bridgeEpoch string) *Handlers {
	prekeys := NewPrekeyStore("")
	observation := NewObservationManager()
	outbox := NewOutboxManager(prekeys)
	presentation := NewPresentationManager()
	h := &Handlers{
		agents:                  make(map[string]core.Agent),
		sessions:                newSessionRegistry(),
		opencodeSessionOptions:  make(map[string]opencodeSessionOptions),
		contentRefs:             make(map[string]string),
		broadcaster:             NewBroadcaster(),
		pendingNotifications:    NewPendingNotificationStore(),
		projectionHydrateSlots:  make(chan struct{}, projectionHydrateMaxConcurrent),
		relayRunning:            make(map[string]bool),
		relayRunningKind:        make(map[string]string),
		agentRelayRunning:       make(map[string]bool),
		claudeSourceCorrelation: newClaudeSourceCorrelationTracker(),
		deliveryPrekeys:         prekeys,
		observation:             observation,
		relayOutbox:             outbox,
		presentation:            presentation,
		relayEventRouter:        NewRelayEventRouter(observation, outbox, prekeys, NewMailboxService(NewRelayHub()), presentation),
		claudeSessions:          newDefaultClaudeSessionCatalog(),
		pendingClaudeRuntime:    make(map[string]claudeRuntimeSelection),
		transcriptIndex:         transcriptindex.NewStore(defaultTranscriptIndexDir()),
		capabilityPolicy:        NewCapabilityPolicy(),
		relayEnabled:            true,
		sessionListLimit:        defaultSessionListLimit,
		bridgeOwnedTurns:        make(map[string]struct{}),
		ctx:                     ctx,
		cleanupStop:             make(chan struct{}),
	}
	// §3.6.3: 全局专用 bounded file-read worker pool。配置失败属于不可恢复的部署错误，
	// 直接 panic（与 mustGenerateBridgeEpoch 同一处理级别），避免运行时静默回退到阻塞调度器。
	filePool, err := filepool.New(defaultFilePoolConfig())
	if err != nil {
		panic(fmt.Sprintf("filepool config invalid: %v", err))
	}
	h.filePool = filePool
	h.installEventPublisher(NewEventPublisher(bridgeEpoch, h.broadcaster))
	h.projectionKernel = NewProjectionKernel(
		h.eventPublisher.ProjectionReducer(),
		NewProjectionCheckpointStore(""),
	)
	// §6.1 checkpoint 只读 diff: wire the git-checkpoint coalescer. The coalescer
	// resolves workspace dirs from the session registry and emits turn_diff_ready
	// via h.publishEvent (EventPublisher.PublishLogical, the single Kernel→
	// EventPublisher outlet). The kernel calls it from IngestLive after each
	// turn_completed. Capture is additionally gated on the agent implementing
	// core.CheckpointProvider (resolver.CheckpointEnabled), so backends that do
	// not opt in honestly no-op.
	h.checkpointCoalescer = newCheckpointCoalescer(&handlersCheckpointResolver{h: h}, func(le LogicalEvent) {
		// The coalescer emits turn_diff_ready (control-plane only) through the
		// single Kernel→EventPublisher outlet. The return EventMessage is the
		// stamped fan-out frame; the coalescer does not need it.
		_ = h.publishEvent(le)
	})
	h.projectionKernel.SetTurnCheckpointStager(h.checkpointCoalescer.stage)
	h.eventPublisher.SetProjectionKernel(h.projectionKernel)
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
		if newState == string(sessionStateIdle) {
			h.completeBridgeTurn(sessionID)
		}
	}
	return h
}

// SetAdmissionMachine 注入 Management runtime admission。生产 main 在 ManagementServer
// 构造时安装；无 management API 的开发/测试模式保持 nil，不改变既有行为。
func (h *Handlers) SetAdmissionMachine(machine *admission.AdmissionMachine) {
	h.mu.Lock()
	h.admission = machine
	h.mu.Unlock()
}

func (h *Handlers) admitBridgeTurn(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.admission == nil {
		return true
	}
	if _, exists := h.bridgeOwnedTurns[sessionID]; exists {
		return false
	}
	if !h.admission.TryBeginBridgeTurn() {
		return false
	}
	h.bridgeOwnedTurns[sessionID] = struct{}{}
	return true
}

func (h *Handlers) completeBridgeTurn(sessionID string) {
	h.mu.Lock()
	if _, exists := h.bridgeOwnedTurns[sessionID]; !exists {
		h.mu.Unlock()
		return
	}
	delete(h.bridgeOwnedTurns, sessionID)
	machine := h.admission
	h.mu.Unlock()
	if machine != nil {
		machine.EndBridgeTurn()
	}
}

func (h *Handlers) pendingInteractionCount() uint32 {
	identities := h.sessions.activityIdentities()
	var count uint32
	for _, identity := range identities {
		projection, ok := h.projectionKernel.Snapshot(identity.backendID, identity.sessionID)
		if !ok {
			continue
		}
		for _, turn := range projection.Turns {
			if turn.Assistant == nil {
				continue
			}
			for _, part := range turn.Assistant.Parts {
				if part.Type == "user_input" && part.UserInputStatus == "pending" && count < ^uint32(0) {
					count++
				}
			}
		}
	}
	return count
}

func (h *Handlers) installEventPublisher(publisher *EventPublisher) {
	if publisher == nil {
		panic("event publisher must not be nil")
	}
	if h.deltaBatcher != nil {
		h.deltaBatcher.Stop()
	}
	h.eventPublisher = publisher
	h.eventPublisher.SetOfflineRoute(h.routeRelayOfflineStampedEvent)
	h.eventPublisher.SetObservationManager(h.observation)
	h.eventPublisher.SetRebindTargets(h.rebindLiveTargetsForSession)
	h.deltaBatcher = NewDeltaBatcher(publisher)
	if h.projectionKernel != nil {
		h.projectionKernel.SetReducer(publisher.ProjectionReducer())
		publisher.SetProjectionKernel(h.projectionKernel)
	}
}

func (h *Handlers) publishEvent(logical LogicalEvent) EventMessage {
	if h.eventPublisher == nil {
		panic("event publisher is not configured")
	}
	if len(logical.Targets) > 0 && len(logical.WaitTargets) == 0 {
		logical.WaitTargets = logical.Targets
	}
	return h.eventPublisher.PublishLogical(logical)
}

func (h *Handlers) registerConnection(conn Connection) {
	h.broadcaster.RegisterConn(conn)
	h.eventPublisher.RegisterConnection(conn)
	// Fresh connect (LAN after relay drop, or first hello) must re-bind session
	// subscriptions from device-scoped observation — otherwise PublishLogical sees
	// candidateTargets=0 until the next set_observation_scope/get_session_messages
	// race window (owner WiFi 2026-07-25: relay close → LAN hello → still zero targets).
	h.resubscribeObservationSessions(conn)
	// Live-frame buffer: replay frames stored while this device had zero targets.
	h.eventPublisher.FlushLiveFrameBufferForDevice(conn)
}

// replaceConnection atomically swaps an authenticated device connection during
// online re-handshake. Subscriptions move to the new Connection; observation
// scope is device-scoped and is intentionally NOT cleared. Clearing scope here
// is what turned full_stream reconnect windows into milestones_only (only
// durable events) until the next iOS set_observation_scope lease renew (~30s).
func (h *Handlers) replaceConnection(old, new Connection) {
	if new == nil {
		return
	}
	if old == nil {
		// registerConnection already resubscribes from observation.
		h.registerConnection(new)
		return
	}
	h.eventPublisher.UnregisterConnection(old)
	h.broadcaster.TransferSubscriptions(old, new)
	h.eventPublisher.RegisterConnection(new)
	// Observation is device-scoped and may outlive the connection; session
	// subscriptions are connection-scoped and can be empty after a true
	// disconnect. Re-bind from observation so mid-turn live is not stuck at
	// candidateTargets=0 until iOS happens to RPC again.
	h.resubscribeObservationSessions(new)
	h.eventPublisher.FlushLiveFrameBufferForDevice(new)
}

// resubscribeObservationSessions re-attaches broadcaster session keys for every
// backend/session still listed in the device's observation scope.
func (h *Handlers) resubscribeObservationSessions(conn Connection) {
	if conn == nil || h.observation == nil {
		return
	}
	device := conn.AuthedDevice()
	if device == nil {
		return
	}
	for _, backendID := range []string{"codex", "claudecode", "opencode", "grokbuild", "claude"} {
		scope := h.observation.GetScope(device.DeviceID, backendID)
		if scope == nil {
			continue
		}
		for _, sid := range scope.SessionIDs {
			if sid == "" {
				continue
			}
			h.broadcaster.Subscribe(conn, SubscriptionKey{
				BackendID: backendID,
				SessionID: sid,
			})
		}
	}
}

// rebindLiveTargetsForSession re-attaches broadcaster subscriptions for every
// still-connected device that observes this session. Called when PublishLogical
// sees zero online targets while the file-relay is still EMITting — the common
// failure after path thrash where observation/device registry still has the
// device but session keys were wiped from the broadcaster.
func (h *Handlers) rebindLiveTargetsForSession(backendID, sessionID string) int {
	if h == nil || h.broadcaster == nil || backendID == "" || sessionID == "" {
		return 0
	}
	rebound := 0
	for _, deviceID := range globalDeviceConnRegistry.AllDeviceIDs() {
		// Observation scope is the authoritative watch list. A connected device that does
		// not observe this session must not be subscribed as a recovery side effect.
		shouldBind := false
		if h.observation != nil {
			if scope := h.observation.GetScope(deviceID, backendID); scope != nil {
				if len(scope.SessionIDs) == 0 {
					shouldBind = true // backend-wide watch
				} else {
					for _, sid := range scope.SessionIDs {
						if sid == sessionID || sid == "*" {
							shouldBind = true
							break
						}
						// Pending alias still listed in scope after lazy create: treat as
						// observing the resolved real session (first-turn delivery).
						if strings.HasPrefix(sid, "pending-") {
							if t, ok := h.sessions.get(sid); ok && t != nil && t.sessionID == sessionID {
								shouldBind = true
								break
							}
						}
					}
				}
			}
		}
		conns := globalDeviceConnRegistry.Connections(deviceID)
		if len(conns) == 0 {
			continue
		}
		if !shouldBind {
			continue
		}
		for _, conn := range conns {
			if conn == nil {
				continue
			}
			if closed, ok := conn.(interface{ isClosed() bool }); ok && closed.isClosed() {
				continue
			}
			h.broadcaster.RegisterConn(conn)
			h.broadcaster.Subscribe(conn, SubscriptionKey{
				BackendID: backendID,
				SessionID: sessionID,
			})
			// Capability provenance belongs to hello negotiation. The existing connection's
			// v2 mark already survives a subscription rebind; a replacement connection must
			// negotiate its own mark in the hello handler.
			rebound++
		}
	}
	if rebound > 0 {
		slog.Info("go-bridge: rebound live targets for zero-target session",
			"backendID", backendID,
			"sessionID", sessionID,
			"conns", rebound,
		)
	} else {
		// Forensic: PublishLogical zero-target recovery found nothing to rebind.
		// Common when device registry is empty while an RPC conn still answers
		// (registry/broadcaster desync) — pull still works, live push does not.
		deviceN := len(globalDeviceConnRegistry.AllDeviceIDs())
		hasSub := false
		if h.broadcaster != nil {
			hasSub = h.broadcaster.HasSessionSubscriber(backendID, sessionID)
		}
		slog.Warn("go-bridge: rebind live targets found zero conns",
			"backendID", backendID,
			"sessionID", sessionID,
			"registryDevices", deviceN,
			"hasSessionSubscriber", hasSub,
		)
	}
	return rebound
}

func (h *Handlers) unregisterConnection(conn Connection) {
	h.broadcaster.UnsubscribeAll(conn)
	h.eventPublisher.UnregisterConnection(conn)
	// Intentionally do NOT RemoveDevice observation here.
	//
	// Path switch (relay↔LAN) often has a 0.5–2s gap with zero connections. Wiping
	// observation on the last unregister left registerConnection's
	// resubscribeObservationSessions with nothing to rebind, so live stayed at
	// candidateTargets=0 until a later set_observation_scope — and even that
	// raced thrashing (owner WiFi 2026-07-25: LAN hello + set_observation + still
	// subscribed=false window). Session subscriptions are connection-scoped and
	// are reattached from surviving observation on the next register/replace.
	// Soft lease (2× LeaseSeconds) already demotes full_stream delivery; device
	// revoke must call RemoveDevice explicitly if permanent wipe is required.
	_ = conn // keep signature; observation retained for reconnect rebind
}

func (h *Handlers) SetSessionListLimit(limit int) {
	if limit < 1 {
		limit = defaultSessionListLimit
	}
	if limit > maxSessionListLimit {
		limit = maxSessionListLimit
	}
	h.mu.Lock()
	h.sessionListLimit = limit
	h.mu.Unlock()
}

func (h *Handlers) effectiveSessionListLimit(requested int) int {
	h.mu.Lock()
	configured := h.sessionListLimit
	h.mu.Unlock()
	if configured < 1 {
		configured = defaultSessionListLimit
	}
	if requested > 0 && requested < configured {
		return requested
	}
	return configured
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
	if h.projectionKernel != nil {
		h.projectionKernel.SetCheckpointStore(NewProjectionCheckpointStore(dir))
	}
}

// SetPinStore 注入进程级 session pin (置顶) 存储。由 main() 在数据目录确定后、agent
// 注册前调用；set_session_pinned / list_pinned_sessions 处理器读它。
func (h *Handlers) SetPinStore(store *pinstore.Store) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pinStore = store
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
		// Fix 5: 停止 delta 攒批 ticker 并 flush 残留 text（流式末尾的 token 不丢）。
		if h.deltaBatcher != nil {
			h.deltaBatcher.Stop()
		}
		// §3.6.3: 关闭 file-read worker pool（drain queued、cancel in-flight ctx、等待 workers）。
		if h.filePool != nil {
			h.filePool.Close()
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
	if msg.Method == "read_file_v2" && !h.eventPublisher.ConnReadFileV2(conn) {
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "capability_not_negotiated",
			Message: "read_file_v2 was not negotiated for this connection",
		})
		return
	}

	if h.handleDeliveryRPC(conn, msg) {
		return
	}
	if h.handleRelayUpgradeRPC(conn, msg) {
		return
	}
	if msg.Method == "set_observation_scope" {
		h.handleSetObservationScope(conn, msg)
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

func (h *Handlers) handleSetObservationScope(conn Connection, msg WireMessage) {
	device := conn.AuthedDevice()
	if device == nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "auth.required", Message: "observation scope requires an authenticated device"})
		return
	}
	req, err := ParseSetObservationScopeRequest(msg.Params)
	if err != nil || req.BackendID != msg.BackendID {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "invalid observation scope"})
		return
	}
	h.observation.SetScope(device.DeviceID, ObservationScope{
		BackendID:             req.BackendID,
		SessionIDs:            req.SessionIDs,
		DeliveryMode:          req.DeliveryMode,
		IncludeRunningSignals: req.IncludeRunningSignals,
		LeaseSeconds:          req.LeaseSeconds,
	})
	// Track interest for live-frame buffer (survives soft prune RemoveDevice).
	// Also Subscribe the *current* connection to each observed session: live
	// delivery uses broadcaster.Targets (session subscription), not observation
	// alone. After a true disconnect, UnsubscribeAll wiped keys; reconnect only
	// called set_observation_scope → candidateTargets=0 until the next
	// get_session_messages (owner 2026-07-24: Mac turn_started + zero online
	// while iOS had the session open).
	if h.eventPublisher != nil {
		for _, sid := range req.SessionIDs {
			h.eventPublisher.NoteLiveInterest(device.DeviceID, req.BackendID, sid)
		}
	}
	for _, sid := range req.SessionIDs {
		if sid == "" {
			continue
		}
		h.broadcaster.Subscribe(conn, SubscriptionKey{
			BackendID: req.BackendID,
			SessionID: sid,
		})
	}
	// INFO so flapping/delivery-gap forensics can see mode without Debug log level.
	// hasSubscriber after Subscribe is the forensic for candidateTargets=0 regressions.
	hasSub := false
	if len(req.SessionIDs) > 0 {
		hasSub = h.broadcaster.HasSessionSubscriber(req.BackendID, req.SessionIDs[0])
	}
	slog.Info("go-bridge: set_observation_scope applied",
		"deviceID", safeID(device.DeviceID),
		"backendID", req.BackendID,
		"mode", req.DeliveryMode,
		"sessions", len(req.SessionIDs),
		"includeRunning", req.IncludeRunningSignals,
		"leaseSeconds", req.LeaseSeconds,
		"hasSessionSubscriber", hasSub,
	)
	// After full_stream re-assert (post-reconnect), flush buffered live frames.
	h.eventPublisher.FlushLiveFrameBufferForDevice(conn)
	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

// HandleRelayInbound 处理通过 relay 加密通道收到的 iOS→Mac 业务消息。
// 解密后的 JSON 应为标准 wire message，解析后走正常 RPC 分发路径。
func (h *Handlers) HandleRelayInbound(conn Connection, rawJSON json.RawMessage) {
	var msg WireMessage
	if err := json.Unmarshal(rawJSON, &msg); err != nil {
		slog.Warn("handlers: invalid relay inbound message", "error", err)
		return
	}

	h.HandleRelayInboundMessage(conn, msg)
}

// HandleRelayInboundMessage dispatches an already-decoded Relay message. The
// per-device inbound scheduler uses this entry point so MB-scale params/data
// are not unmarshaled a second time before handler-specific decoding.
func (h *Handlers) HandleRelayInboundMessage(conn Connection, msg WireMessage) {
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
	case msg.Type == "recovery_applied":
		if err := h.eventPublisher.CompleteRecovery(conn, msg.RecoveryID, msg.AppliedThroughBySession); err != nil {
			slog.Warn("handlers: relay recovery acknowledgement rejected", "error", err)
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
	case "get_session_projection":
		h.handleGetSessionProjection(conn, msg, agent)
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
	case "get_turn_diff":
		h.handleGetTurnDiff(conn, msg, agent)
	case "get_full_thread_diff":
		h.handleGetFullThreadDiff(conn, msg, agent)
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
	case "read_file_v2":
		h.handleReadFileV2(conn, msg)
	case "cancel_request_v1":
		h.handleCancelRequest(conn, msg)
	case "list_directory":
		h.handleListDirectory(conn, msg)
	case "get_git_context":
		h.handleGetGitContext(conn, msg)
	case "check_pull_request_support":
		h.handleCheckPullRequestSupport(conn, msg, agent)
	case "create_pull_request":
		h.handleCreatePullRequest(conn, msg, agent)
	case "commit_and_push":
		h.handleCommitAndPush(conn, msg, agent)
	case "checkout_git_branch":
		h.handleCheckoutGitBranch(conn, msg)
	case "create_git_branch":
		h.handleCreateGitBranch(conn, msg)
	case "create_git_worktree":
		h.handleCreateGitWorktree(conn, msg)
	case "rename_session":
		h.handleRenameSession(conn, msg, agent)
	case "share_session":
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "not_supported",
			Message: "session share is not supported",
		})
	case "archive_session":
		h.handleArchiveSession(conn, msg, agent)
	case "set_session_pinned":
		h.handleSetSessionPinned(conn, msg, agent)
	case "list_pinned_sessions":
		h.handleListPinnedSessions(conn, msg, agent)
	case "compress_context":
		h.handleCompressContext(conn, msg)
	case "check_pending_notifications":
		h.handleCheckPendingNotifications(conn, msg)
	case "question_reply":
		h.handleQuestionReply(conn, msg)
	case "question_reject":
		h.handleQuestionReject(conn, msg)
	case "resolve_user_input":
		h.handleResolveUserInput(conn, msg, agent)
	default:
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "method_not_found",
			Message: fmt.Sprintf("method %q not implemented", msg.Method),
		})
	}
}

func shouldSwitchWorkDirForMethod(method string) bool {
	switch method {
	case "list_sessions", "get_session", "get_session_messages", "get_session_projection":
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
	// ~/.claude/projects is Claude-only (path-encoded keys like -Users-jacklee-Projects-…).
	// Codex / Grok / others discover workspace via session.directory from ListSessions.
	// Previously only grokbuild was excluded; Codex still scanned Claude folders → iOS
	// sidebar seeded dozens of empty "暂无会话" groups under Codex mode.
	if !shouldListClaudeProjects(agent) {
		conn.SendResult(msg.RequestID, map[string]interface{}{"projects": []interface{}{}}, nil)
		return
	}

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

// shouldListClaudeProjects gates the ~/.claude/projects filesystem scan.
// Only the Claude agent owns that catalog; all other backends must return empty.
func shouldListClaudeProjects(agent core.Agent) bool {
	if agent == nil {
		return false
	}
	switch agent.Name() {
	case "claudecode", "claude":
		return true
	default:
		return false
	}
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
			h.publishEvent(LogicalEvent{BackendID: msg.BackendID, Event: "diagnostic_progress", Targets: []Connection{conn}, Data: map[string]interface{}{
				"diagnosticRunId": runID,
				"checkId":         progress.CheckID,
				"status":          progress.Status,
				"message":         progress.Message,
			}})
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

		h.publishEvent(LogicalEvent{BackendID: msg.BackendID, Event: "diagnostic_completed", Targets: []Connection{conn}, Data: map[string]interface{}{
			"diagnosticRunId": runID,
			"overallStatus":   report.OverallStatus,
			"results":         diagnosticResultsToWire(report.Results),
		}})
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
		h.publishEvent(LogicalEvent{SessionID: sessionID, BackendID: msg.BackendID, Event: "session_state_changed", Data: map[string]interface{}{"state": "idle"}, Targets: []Connection{conn}})
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

	h.publishEvent(LogicalEvent{SessionID: sessionID, BackendID: msg.BackendID, Event: "session_state_changed", Data: map[string]interface{}{"state": "idle"}, Targets: []Connection{conn}})
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
	if !h.admitBridgeTurn(params.SessionID) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "runtime.quiescing", Message: "Bridge runtime is quiescing"})
		return
	}
	turnCommitted := false
	defer func() {
		if !turnCommitted {
			h.completeBridgeTurn(params.SessionID)
		}
	}()

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
		if resumeID != "" && agent.Name() == "claudecode" {
			if wireErr := preflightClaudeResume(h.ctx, agent, resumeID); wireErr != nil {
				conn.SendResult(msg.RequestID, nil, wireErr)
				return
			}
		}
		slog.Info("go-bridge: handleSendMessage: session not found in registry. Starting new agent session.", "sessionID", params.SessionID, "resumeID", resumeID, "agent", agent.Name())
		startAt := time.Now()
		var err error
		sess, err = agent.StartSession(h.ctx, resumeID)
		slog.Info("go-bridge: handleSendMessage: StartSession finished",
			"sessionID", params.SessionID,
			"resumeID", resumeID,
			"agent", agent.Name(),
			"elapsed_ms", time.Since(startAt).Milliseconds(),
			"error", err,
		)
		if err != nil {
			slog.Error("go-bridge: handleSendMessage: StartSession failed", "sessionID", params.SessionID, "resumeID", resumeID, "error", err)
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: err.Error()})
			return
		}

		// resume 时 claude --resume 会输出完整历史，先排空历史事件。
		// Codex / Grok 不重放 transcript 作为事件流（Grok 历史走 HistoryProvider 落盘），
		// drain 只会空转甚至在有持续 session/update 时占满至 10s，拖垮 send_message 的 RPC 时延。
		if resumeID != "" {
			switch agent.Name() {
			case "codex", "grokbuild":
				// skip drain
			default:
				drainHistoryEvents(sess)
			}
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

	// 通知 iOS 进入 running 状态。
	// grokbuild 不发 session_state_changed(running)：它的 turn_started 事件已经
	// 通过 syncRuntimeStateStore(reasoningDelta/toolStarted) 激活 iOS 执行态，
	// 额外的 running 广播会让 isGenerating 过早激活；如果 turn_completed 的 500ms
	// debounce 在 session 切换时被取消，isGenerating 会永久残留导致输入框卡"执行中"。
	if agent.Name() != "grokbuild" {
		h.publishEvent(LogicalEvent{
			BackendID: msg.BackendID,
			SessionID: params.SessionID,
			Directory: extractDir(msg),
			Event:     "session_state_changed",
			Data:      map[string]interface{}{"state": "running"},
			Broadcast: true,
			Targets:   []Connection{conn},
		})
	}
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
	turnCommitted = true

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
}

var claudeResumeOwnerCheckTimeout = 2 * time.Second

func preflightClaudeResume(rootCtx context.Context, agent core.Agent, sessionID string) *WireError {
	if err := rootCtx.Err(); err != nil {
		return &WireError{Code: "request.cancelled", Message: err.Error()}
	}
	lister, ok := agent.(core.LiveSessionLister)
	if !ok {
		return retryableSessionError(
			"session.owner_check_failed",
			"无法确认会话进程归属，为避免冲突未发送，请稍后重试。",
		)
	}
	checkCtx, cancel := context.WithTimeout(rootCtx, claudeResumeOwnerCheckTimeout)
	defer cancel()
	proc, err := lister.LiveSessionProcess(checkCtx, sessionID)
	if rootErr := rootCtx.Err(); rootErr != nil {
		return &WireError{Code: "request.cancelled", Message: rootErr.Error()}
	}
	if err != nil || checkCtx.Err() != nil {
		return retryableSessionError(
			"session.owner_check_failed",
			"无法确认会话进程归属，为避免冲突未发送，请稍后重试。",
		)
	}
	if proc.Live {
		return retryableSessionError(
			"session.held_by_external_worker",
			"该会话记录的进程仍在运行。请在启动该会话的客户端中结束会话并退出对应进程，然后重试。",
		)
	}
	return nil
}

func retryableSessionError(code, message string) *WireError {
	retryable := true
	return &WireError{Code: code, Message: message, Retryable: &retryable}
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
	// Broadcaster: move pending subscriptions (any directory variant) → real id.
	h.broadcaster.Rebind(currentID, realID, backendID, directory)
	h.eventPublisher.EventBuffer().Rebind(backendID, currentID, realID)
	h.rebindRelayKind(currentID, realID, relayKindAgent)
	// Observation: rewrite pending → real so ShouldSendEvent / rebindLiveTargets
	// accept projection_patch for the real session before the client's next lease renew.
	if h.observation != nil {
		h.observation.RebindSessionID(backendID, currentID, realID)
	}
	// Ensure the live device conn is subscribed under the real id immediately.
	// Without this, first-turn text/turn_completed patches flush with zero targets.
	if n := h.rebindLiveTargetsForSession(backendID, realID); n > 0 {
		slog.Info("go-bridge: pending→real rebind rebound live targets",
			"backendID", backendID,
			"from", currentID,
			"to", realID,
			"conns", n,
		)
	} else {
		slog.Warn("go-bridge: pending→real rebind found zero live targets",
			"backendID", backendID,
			"from", currentID,
			"to", realID,
		)
	}
	// Codex file relay is keyed by session id; start under real id so transcript
	// catch-up does not thrash on the pending ghost subscription.
	if backendID == "codex" {
		if agent, ok := h.Agents()["codex"]; ok && agent != nil {
			h.startCodexSessionFileRelay(realID, nil, backendID, agent)
		}
	}
	if backendID == "claude" || backendID == "claudecode" {
		h.mu.Lock()
		selection := h.pendingClaudeRuntime[currentID]
		delete(h.pendingClaudeRuntime, currentID)
		h.mu.Unlock()
		h.writeClaudeRuntimeSidecar(realID, directory, selection)
	}
	slog.Info("go-bridge: session id rebind complete",
		"backendID", backendID,
		"from", currentID,
		"to", realID,
	)
	return realID
}

func (h *Handlers) sendSessionEvent(sessionID, backendID, eventName string, data interface{}) {
	h.mu.Lock()
	dir := h.sessions.directoryForSession(sessionID)
	h.mu.Unlock()
	h.publishEvent(LogicalEvent{
		SessionID: sessionID,
		BackendID: backendID,
		Event:     eventName,
		Data:      data,
		Directory: dir,
		Broadcast: true,
		Offline:   IsDurableMilestone(eventName),
	})
}

// broadcastIdleState 向订阅者推送 session_state_changed: idle。
func (h *Handlers) broadcastIdleState(sessionID, backendID string) {
	h.mu.Lock()
	dir := h.sessions.directoryForSession(sessionID)
	h.mu.Unlock()
	h.publishEvent(LogicalEvent{
		SessionID: sessionID,
		BackendID: backendID,
		Event:     "session_state_changed",
		Data:      map[string]interface{}{"state": "idle"},
		Directory: dir,
		Broadcast: true,
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
		// Prefer backend-native cancel (ACP session/cancel, Codex turn/interrupt, …)
		// before tearing down the process so in-flight turns get a real terminal.
		if tc, ok := sess.(core.TurnCanceler); ok {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			if err := tc.CancelTurn(cancelCtx); err != nil {
				slog.Debug("go-bridge: CancelTurn", "sessionID", sessionID, "backendID", backendID, "error", err)
			}
			cancel()
		}
		_ = sess.Close()
	}

	if deleted {
		h.publishEvent(LogicalEvent{
			BackendID: backendID,
			SessionID: sessionID,
			Directory: directory,
			Event:     "turn_completed",
			Data:      map[string]interface{}{"done": true, "reason": "aborted"},
			Broadcast: true,
			Offline:   true,
		})

		h.publishEvent(LogicalEvent{
			BackendID: backendID,
			SessionID: sessionID,
			Directory: directory,
			Event:     "session_state_changed",
			Data:      map[string]interface{}{"state": "idle"},
			Broadcast: true,
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
	limit := h.effectiveSessionListLimit(extractPositiveInt(msg, "limit"))
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
		h.overlayPinnedState(wireSessions, agentBackendID(agent))
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
	h.overlayPinnedState(allSessions, "claudecode")
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
	Title string
	// CustomTitle 仅在 JSONL 里出现 type=custom-title 记录时设值；
	// assistant 文本回退出的 Title 不算 custom title。fork 检测要求双方都有
	// custom title，避免把「首条 assistant 恰好相同」的无关会话误判为 fork。
	CustomTitle string
	// FirstUserAt 是首条非 meta user 消息的 timestamp。Claude Code fork 时
	// 会把原始会话的开头（含首条 user 消息）原样复制到新会话，因此 fork 对的
	// FirstUserAt 完全相同，可作为 fork 配对信号。首条消息在文件开头，LimitReader
	// 一定能读到。
	FirstUserAt        time.Time
	CompactBoundaryIDs []string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ModelID            string
	ProviderID         string
	ReasoningEffort    string
	// ArchivedAt is read from the session sidecar (claudeBridgeSessionSidecar).
	// The catalog surfaces it as archivedAtMillis so clients can hide archived
	// sessions (web session-grouping filters on archivedAtMillis). Without it,
	// archived Claude sessions never disappear from the web list even after the
	// discovery poller signals sessions_changed.
	ArchivedAt time.Time
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
	var customTitle string
	var assistantTitle string
	var firstUserAt time.Time
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
			var ct string
			if err := json.Unmarshal(raw["customTitle"], &ct); err == nil {
				if trimmed := strings.TrimSpace(ct); trimmed != "" {
					title = trimmed
					customTitle = trimmed
				}
			}
			continue
		}
		// 记录首条非 meta user 消息的 timestamp，用于 fork 检测配对。
		// 只需要 timestamp，不需要 message 内容，因此放在解析 message 之前。
		if msgType == "user" && firstUserAt.IsZero() {
			var ts string
			if err := json.Unmarshal(raw["timestamp"], &ts); err == nil && ts != "" {
				if parsed, perr := time.Parse(time.RFC3339Nano, ts); perr == nil {
					firstUserAt = parsed
				}
			}
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
	continuity := claudecode.InspectTranscriptContinuity(path)
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
	var archivedAt time.Time
	if sidecar.ArchivedAtMillis > 0 {
		archivedAt = time.UnixMilli(sidecar.ArchivedAtMillis).UTC()
	}
	return claudeSessionScanResult{
		Title:              title,
		CustomTitle:        customTitle,
		FirstUserAt:        firstUserAt,
		CompactBoundaryIDs: continuity.BoundaryIDs,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
		ModelID:            modelID,
		ProviderID:         providerID,
		ReasoningEffort:    normalizeClaudeRuntimeEffort(sidecar.ReasoningEffort),
		ArchivedAt:         archivedAt,
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
	var recoveryCut *BridgeSessionCut
	if params.RecoveryID != "" {
		cut, release, err := h.eventPublisher.FreezeRecoverySnapshot(conn, params.RecoveryID, msg.BackendID, params.SessionID)
		if err != nil {
			metrics.sendResult(conn, msg.RequestID, nil, &WireError{Code: "recovery.snapshot_invalid", Message: err.Error()})
			return
		}
		defer release()
		recoveryCut = &cut
	}

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
	h.startGrokLeaderSessionRelay(params.SessionID, msg.BackendID, agent, params.Directory)

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
		metrics.sendResult(conn, msg.RequestID, attachRecoverySnapshotMetadata(applyIfNoneMatch(result, params.IfNoneMatchRevision), params.RecoveryID, recoveryCut), nil)
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
			if params.Paginate {
				messages = trimWireToBudget(messages)
			} else {
				truncateOversizedMessages(messages)
			}
			result := map[string]interface{}{"messages": messages}
			if usage := h.getSessionContextUsage(agent, params.SessionID); usage != nil {
				result["contextUsage"] = contextUsageToWire(usage)
			}
			metrics.wireMapping += time.Since(mappingStarted)
			metrics.resultCount = len(messages)
			metrics.sendResult(conn, msg.RequestID, attachRecoverySnapshotMetadata(applyIfNoneMatch(result, params.IfNoneMatchRevision), params.RecoveryID, recoveryCut), nil)
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
	truncateOversizedMessages(result)

	payload := map[string]interface{}{"messages": result}
	if usage := h.getSessionContextUsage(agent, params.SessionID); usage != nil {
		payload["contextUsage"] = contextUsageToWire(usage)
	}
	metrics.wireMapping += time.Since(mappingStarted)
	metrics.resultCount = len(result)
	metrics.sendResult(conn, msg.RequestID, attachRecoverySnapshotMetadata(applyIfNoneMatch(payload, params.IfNoneMatchRevision), params.RecoveryID, recoveryCut), nil)
}

func attachRecoverySnapshotMetadata(payload map[string]interface{}, recoveryID string, cut *BridgeSessionCut) map[string]interface{} {
	if recoveryID == "" || cut == nil {
		return payload
	}
	payload["recoveryId"] = recoveryID
	payload["eventHighWaterMark"] = *cut
	return payload
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
	if entry.TurnStartedAt != nil {
		result["turnStartedAtMillis"] = entry.TurnStartedAt.UnixMilli()
	}
	if entry.TurnCompletedAt != nil {
		result["turnCompletedAtMillis"] = entry.TurnCompletedAt.UnixMilli()
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
	if agent.Name() == "grokbuild" {
		h.startGrokLeaderSessionRelay(params.SessionID, msg.BackendID, agent, params.Directory)
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
	conn.SendResult(msg.RequestID, h.enrichResumeSessionState(result, agent), nil)
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
	h.publishEvent(LogicalEvent{SessionID: params.SessionID, BackendID: msg.BackendID, Event: "permission_mode_changed", Targets: []Connection{conn}, Data: map[string]interface{}{
		"mode":      current,
		"appliesTo": appliesTo,
	}})
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

// handleResolveUserInput 是 v2 结构化用户输入回答的唯一入口（设计 §7/§10.1）。
// 它只调用可选能力 core.UserInputResponder；旧 RespondQuestion/RejectQuestion 不作 fallback。
// 把 adapter 返回的 *core.UserInputError 映射为 WireError（保留稳定 code），不回显 secret/答案正文。
func (h *Handlers) handleResolveUserInput(conn Connection, msg WireMessage, _ core.Agent) {
	var params struct {
		SessionID      string                  `json:"sessionId"`
		InteractionID  string                  `json:"interactionId"`
		ClientActionID string                  `json:"clientActionId"`
		Action         core.UserInputAction    `json:"action"`
		Answers        *[]core.UserInputAnswer `json:"answers"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	if strings.TrimSpace(msg.BackendID) == "" || strings.TrimSpace(params.SessionID) == "" || strings.TrimSpace(params.InteractionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "backendId, sessionId, and interactionId are required"})
		return
	}
	if !isUUIDv4(params.ClientActionID) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "clientActionId must be a UUID v4"})
		return
	}
	if params.Action != core.UserInputActionAnswer && params.Action != core.UserInputActionReject {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: `action must be "answer" or "reject"`})
		return
	}
	if params.Action == core.UserInputActionAnswer && (params.Answers == nil || len(*params.Answers) == 0) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "answer action requires non-empty answers"})
		return
	}
	if params.Action == core.UserInputActionReject && params.Answers != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "reject action must omit answers"})
		return
	}

	tracked, ok := h.sessions.getForBackend(params.SessionID, msg.BackendID)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "session_not_found", Message: "no active session for this backend and structured user input"})
		return
	}
	sess := tracked.session
	if params.Action == core.UserInputActionReject {
		part, _, found := h.projectedUserInput(msg.BackendID, params.SessionID, params.InteractionID)
		if !found {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "interaction_not_found", Message: "interaction not found in current projection"})
			return
		}
		if !part.UserInputCanReject {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "response_not_supported", Message: "this interaction cannot be rejected"})
			return
		}
	}

	responder, ok := sess.(core.UserInputResponder)
	if !ok {
		// backend 未声明 structured_user_input_v1 能力：fail-closed，明确告知不支持。
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "response_not_supported", Message: "this backend does not support structured user input"})
		return
	}

	resolveCtx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	var answers []core.UserInputAnswer
	if params.Answers != nil {
		answers = *params.Answers
	}
	resolution, err := responder.ResolveUserInput(resolveCtx, params.InteractionID, params.ClientActionID, params.Action, answers)
	if err != nil {
		var uie *core.UserInputError
		if errors.As(err, &uie) {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: uie.Code, Message: uie.Message})
			return
		}
		slog.Error("go-bridge: ResolveUserInput failed", "interactionId", params.InteractionID, "error", err)
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "resolve_user_input_failed", Message: err.Error()})
		return
	}
	part, headRev, err := h.waitForUserInputResolution(resolveCtx, msg.BackendID, params.SessionID, params.InteractionID, resolution)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "resolve_user_input_failed", Message: err.Error()})
		return
	}
	resolution.CurrentStatus = core.UserInputStatus(part.UserInputStatus)
	resolution.HeadRev = headRev

	conn.SendResult(msg.RequestID, map[string]any{
		"interactionId": params.InteractionID,
		"outcome":       resolution.Outcome,
		"currentStatus": resolution.CurrentStatus,
		"headRev":       resolution.HeadRev,
	}, nil)
}

func isUUIDv4(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '4' {
		return false
	}
	variant := value[19]
	if variant != '8' && variant != '9' && variant != 'a' && variant != 'b' && variant != 'A' && variant != 'B' {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func (h *Handlers) projectedUserInput(backendID, sessionID, interactionID string) (ProjectionPart, int, bool) {
	if h.eventPublisher == nil || h.eventPublisher.ProjectionReducer() == nil {
		return ProjectionPart{}, 0, false
	}
	projection, ok := h.eventPublisher.ProjectionReducer().Snapshot(backendID, sessionID)
	if !ok {
		return ProjectionPart{}, 0, false
	}
	for _, turn := range projection.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type == "user_input" && part.UserInputInteractionID == interactionID {
				return part, projection.SyncRev, true
			}
		}
	}
	return ProjectionPart{}, projection.SyncRev, false
}

func (h *Handlers) waitForUserInputResolution(ctx context.Context, backendID, sessionID, interactionID string, resolution core.UserInputResolution) (ProjectionPart, int, error) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		part, headRev, found := h.projectedUserInput(backendID, sessionID, interactionID)
		if found {
			status := core.UserInputStatus(part.UserInputStatus)
			if resolution.Outcome == core.UserInputOutcomeInProgress || status != core.UserInputStatusPending {
				return part, headRev, nil
			}
		}
		select {
		case <-ctx.Done():
			return ProjectionPart{}, 0, fmt.Errorf("projection did not commit structured input resolution before timeout: %w", ctx.Err())
		case <-ticker.C:
		}
	}
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
		if !s.PinnedAt.IsZero() {
			wire["pinnedAtMillis"] = s.PinnedAt.UnixMilli()
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

const readFileMaxSize = 2 * 1024 * 1024 // 2MB

// defaultFilePoolConfig 是 read_file_v2 bounded worker pool 的默认配置（plan §3.6.3 / A0.5）。
// 最终数值待 A0 冻结；当前选择保守可工作值：4 个 worker、单设备最多 1 并发（保留 3 个
// global slot）、单设备队列 4、全局队列 32、读超时 10s、stuckAge 5s。
// degradeAt=1：损失 1 个 slot 即报警（minHealthyFileSlots=3 ⇒ 4-3=1）。
func defaultFilePoolConfig() filepool.Config {
	const poolSize uint32 = 4
	return filepool.Config{
		PoolSize:          poolSize,
		PerDeviceInFlight: 1,
		PerDeviceQueued:   4,
		GlobalQueued:      32,
		ReadTimeout:       10 * time.Second,
		Health: admission.FileReadHealthConfig{
			PoolSize:            poolSize,
			MinHealthyFileSlots: 3,
			DegradeAt:           1,
			StuckAgeMillis:      5000,
		},
	}
}

// ── read_file_v2: tagged text/unsupported_encoding/binary + segments + identity (plan §3.6) ──
//
// 这是唯一的文件源码读取 RPC；调用方必须在 hello 阶段协商 read_file_v2 capability。
// params exact: path + owner{kind:session|workspace, backendId, sessionId+directory | workspaceRoot}。
// MacBridge 按 owner 授权并返回 server-canonical owningIdentity（canonicalized root）。
// 结果经 readfile.BuildReadFileV2Result 构造（encoding 分类 + segments + 行语义），WirePayload 发出。
func (h *Handlers) handleReadFileV2(conn Connection, msg WireMessage) {
	path, owner, decodeErr := readfile.DecodeReadFileV2Request(msg.Params)
	if decodeErr != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "read_file_v2 params must match the exact owner schema"})
		return
	}

	backendID := owner.BackendID
	if backendID == "" {
		backendID = msg.BackendID
	}
	var requestedDir, sessionID string
	var identity readfile.OwningIdentity
	switch owner.Kind {
	case "session":
		requestedDir = owner.CanonicalDirectory
		sessionID = owner.SessionID
		identity = readfile.OwningIdentity{Kind: "session", BackendID: backendID, SessionID: owner.SessionID}
	case "workspace":
		requestedDir = owner.CanonicalWorkspaceRoot
		identity = readfile.OwningIdentity{Kind: "workspace", BackendID: backendID}
	default:
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "owner.kind must be session|workspace"})
		return
	}

	authorizedRoot, err := h.authorizedReadFileRoot(msg, requestedDir, sessionID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "file.outside_authorized_root", Message: "file is outside the authorized workspace"})
		return
	}
	// server-canonical identity：workspace 用 canonicalized authorizedRoot；session 用 canonical session dir。
	if identity.Kind == "workspace" {
		identity.CanonicalWorkspaceRoot = authorizedRoot
	} else if dir := h.sessions.directoryForSession(sessionID); dir != "" {
		identity.CanonicalDirectory = dir
	}

	resolvedPath, info, err := resolveAuthorizedReadFilePath(authorizedRoot, path)
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
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "file_too_large", Message: fmt.Sprintf("file size %d bytes exceeds limit %d bytes", info.Size(), readFileMaxSize)})
		return
	}

	// §3.6.3：授权 + 路径解析 + size gate 保持同步（它们是先于读取的 admission）；
	// 实际 os.Open/Stat/ReadAll 投递到全局专用 bounded file pool，避免一次卡住的 os.Read
	// 阻塞本设备 inbound scheduler 上的 permission_response/question_reply/send_message。
	ext := strings.TrimPrefix(filepath.Ext(resolvedPath), ".")
	requestID := msg.RequestID
	infoClone := info
	identityClone := identity
	work := func(ctx context.Context) {
		performReadFileV2Read(ctx, conn, requestID, resolvedPath, infoClone, ext, identityClone)
	}
	onCancel := func(err error) {
		// pool 在 admit 后、Work 前（degrading drain）终结本任务时调用。
		code := "file.read_degraded"
		if errors.Is(err, filepool.ErrPoolClosed) {
			code = "read_failed"
		}
		conn.SendResult(requestID, nil, &WireError{Code: code, Message: "file read could not be completed"})
	}

	if h.filePool == nil {
		// 测试未注入 pool：内联同步读（不享受解耦/退化保护，保持既有测试行为）。
		work(context.Background())
		return
	}
	if submitErr := h.filePool.Submit(filepool.Job{DeviceID: stableFileReadDeviceID(conn), Work: work, OnCancel: onCancel}); submitErr != nil {
		code := "read_failed"
		switch {
		case errors.Is(submitErr, filepool.ErrFileBusy):
			code = "file.read_busy"
		case errors.Is(submitErr, filepool.ErrFileDegraded):
			code = "file.read_degraded"
		}
		conn.SendResult(requestID, nil, &WireError{Code: code, Message: "file read could not be admitted"})
		return
	}
	// 提交成功：pool 稍后触发 work（成功/失败 SendResult）或 onCancel（degrade），
	// 本 goroutine 立即返回，inbound scheduler 不被阻塞。
}

// stableFileReadDeviceID 返回稳定认证设备 ID 作为 file pool 的 fair 身份
// （plan §3.6.3）。未认证（开发模式）返回空串 → 单一 anonymous bucket。
func stableFileReadDeviceID(conn Connection) string {
	if d := conn.AuthedDevice(); d != nil {
		return d.DeviceID
	}
	return ""
}

// performReadFileV2Read 在 file pool worker 上执行实际的有界读取 + 结果组装 + 回写。
// ctx 带 ReadTimeout deadline；分块读取间检查 ctx，commit 前再次校验，禁止 late writeback。
func performReadFileV2Read(ctx context.Context, conn Connection, requestID, resolvedPath string, info os.FileInfo, ext string, identity readfile.OwningIdentity) {
	file, err := os.Open(resolvedPath)
	if err != nil {
		conn.SendResult(requestID, nil, &WireError{Code: "read_failed", Message: "failed to open file"})
		return
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !stableFileSnapshot(info, openedInfo) {
		conn.SendResult(requestID, nil, &WireError{Code: "file.changed_during_read", Message: "file changed during authorization"})
		return
	}
	data, err := readBoundedCooperative(file, readFileMaxSize, ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// 协作取消 / late 读：丢弃，不回写（plan §3.6.3：连接关闭/deadline/cancel 后禁止 late result 写回）。
			slog.Warn("read_file_v2 discarded after context cancel", "requestId", safeID(requestID), "err", err)
			return
		}
		conn.SendResult(requestID, nil, &WireError{Code: "read_failed", Message: "failed to read file"})
		return
	}
	if len(data) > readFileMaxSize {
		conn.SendResult(requestID, nil, &WireError{Code: "file_too_large", Message: "file exceeds size limit"})
		return
	}
	finalInfo, err := file.Stat()
	if err != nil || !stableFileSnapshot(openedInfo, finalInfo) {
		conn.SendResult(requestID, nil, &WireError{Code: "file.changed_during_read", Message: "file changed while being read"})
		return
	}
	if ctx.Err() != nil {
		// commit guard：读取虽完成，但 ctx 已过期/cancel，禁止 late writeback。
		slog.Warn("read_file_v2 discarded: context done before commit", "requestId", safeID(requestID))
		return
	}
	// read_file_v2 返回 byte admission（2 MiB）内的完整文本。5000 行只限制 iOS Shiki
	// 高亮资格，不得在传输层删掉源码。
	result := readfile.BuildReadFileV2Result(data, resolvedPath, ext, identity, readfile.NoLineTruncation, 0)
	conn.SendResult(requestID, result.WirePayload(), nil)
}

// stableFileSnapshot rejects replacement and in-place rewrites, including the
// common same-size case. The post-read check is intentionally performed on the
// already-open descriptor so a pathname swap cannot make torn bytes look valid.
func stableFileSnapshot(before, after os.FileInfo) bool {
	return before != nil && after != nil &&
		os.SameFile(before, after) &&
		before.Size() == after.Size() &&
		before.Mode() == after.Mode() &&
		before.ModTime().Equal(after.ModTime())
}

// readBoundedCooperative 以固定块读取 file 至多 maxBytes+1 字节，并在每次 syscall 间
// 检查 ctx（plan §3.6.3：worker 分块 read 并在每次 syscall 间检查 context）。ctx 无法抢占
// 阻塞 syscall；真正卡住的 read 由 file pool 的 stuck watchdog（FileReadHealth 退化）处理。
func readBoundedCooperative(file *os.File, maxBytes int64, ctx context.Context) ([]byte, error) {
	const chunk = 64 << 10
	limit := maxBytes + 1 // +1 sentinel 用于探测超限（与 io.LimitReader(+1) 语义一致）
	buf := make([]byte, 0, chunk)
	tmp := make([]byte, chunk)
	for int64(len(buf)) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := limit - int64(len(buf))
		want := int64(chunk)
		if want > remaining {
			want = remaining
		}
		n, err := file.Read(tmp[:want])
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
	return buf, nil
}

// ── cancel_request_v1: read_file_v2 bulk cancel control RPC（R1.5，§3.6.4）──────────
//
// 独立 connection capability（Mac 在 hello 回显 cancel_request_v1 后 iOS 才发送）。
// 本期 cancel allowlist 只有 read_file_v2——非 read_file_v2 请求不会在 requestBulkHandles
// 登记 handle，故 lookup 返回 nil → not_found（隐式 allowlist 门控，不同于 not_cancellable）。
// device/generation 绑定由 per-conn map 自然保证：A 设备 cancel 找不到 B 设备的 handle，
// 新 generation（重连）的 cancel 找不到旧 generation 的 handle。
//
// too_late 边界 = committedToWriter（plan §3.6.4「cancel唯一原子状态机」）：Relay 为 index0
// 原子 commit 到 writer。handle.Cancel() 用 CAS active→cancelled 与 writer 的 index0 commit
// 互斥裁决：cancel 赢 → cancelled（writer 跳过 index0）；writer 已 commit index0 → too_late。
func (h *Handlers) handleCancelRequest(conn Connection, msg WireMessage) {
	var params struct {
		RequestID string `json:"requestId"` // 待 cancel 的原始请求 ID
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.RequestID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "requestId required"})
		return
	}
	rc, ok := conn.(*RelayDeviceConn)
	if !ok {
		// Direct/LAN：read_file_v2 结果是单帧同步发送（无 chunk group），cancel 总是 too_late。
		// Direct 的 committedToWriter = 首个 response frame 原子 commit 到 socket writer（R1.9 细化）。
		conn.SendResult(msg.RequestID, map[string]interface{}{"outcome": "too_late", "requestId": params.RequestID}, nil)
		return
	}
	handle := rc.lookupRequestBulkHandle(params.RequestID)
	if handle == nil {
		// 未登记 = 非 read_file_v2 / 已完成 / 跨 device·generation / 从未 chunked。
		conn.SendResult(msg.RequestID, map[string]interface{}{"outcome": "not_found", "requestId": params.RequestID}, nil)
		return
	}
	outcome := "too_late"
	if handle.Cancel() {
		outcome = "cancelled"
		slog.Info("relay cancel_request_v1 cancelled bulk group", "device", rc.deviceID, "originalRequestId", safeID(params.RequestID), "groupID", handle.GroupID())
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"outcome": outcome, "requestId": params.RequestID}, nil)
}

// ── list_directory: iOS 端远程选择/浏览 Mac 本地文件夹 (§6.5) ────────────────────
//
// 两个模式，由可选 workspace_root 参数切换：
//  1. workspace_root 传（workspace-bound）：realpath(requested) 必须在 realpath(root) 内，
//     symlink 列为 isSymlink:true 叶子不递归；拒 ../ 越界。
//  2. workspace_root 不传（广域 picker）：picker 无 workspace 边界、可浏览任意真实目录；
//     symlink 仍由 collectDirItems 的 mode-independent 守卫叶子化（不递归 target），见 review①。
//
// 同时新增 limit/offset/depth 翻页与子树预取（additive，所有调用方共享）。
func (h *Handlers) handleListDirectory(conn Connection, msg WireMessage) {
	var params struct {
		Path          string `json:"path"`
		Limit         int    `json:"limit"`
		Offset        int    `json:"offset"`
		Depth         int    `json:"depth"`
		WorkspaceRoot string `json:"workspace_root"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	var root string
	var resolvedPath string
	workspaceBound := params.WorkspaceRoot != ""

	if workspaceBound {
		var err error
		root, err = canonicalExistingDirectory(params.WorkspaceRoot)
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "file.outside_authorized_root", Message: "invalid workspace root: " + err.Error()})
			return
		}
		if params.Path == "" {
			resolvedPath = root
		} else {
			resolvedPath, err = canonicalExistingDirectory(params.Path)
			if err != nil {
				conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_path", Message: err.Error()})
				return
			}
			if !pathIsWithinRoot(root, resolvedPath) {
				conn.SendResult(msg.RequestID, nil, &WireError{Code: "file.outside_authorized_root", Message: "requested directory is outside the authorized workspace"})
				return
			}
		}
	} else {
		// 广域模式（picker）：不设 workspace 边界，通过 expandPath 接受 ~/相对路径，
		// picker 可浏览任意真实目录。symlink 安全由 collectDirItems 的 mode-independent
		// 守卫保证（isSymlink → 叶子不递归），故即便 path 下含指向外部的 symlink，也只
		// 返回叶子标记、不展开 target 内容（review①：TestListDirectory_BroadMode_SymlinkIsLeaf）。
		var err error
		resolvedPath, err = expandPath(params.Path)
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_path", Message: err.Error()})
			return
		}
		root = resolvedPath // 广域无 workspace 边界：root=resolvedPath 使 pathIsWithinRoot 恒真（picker 预期）
	}

	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "read_failed", Message: err.Error()})
		return
	}

	limit := clampInt(params.Limit, 200, 1, 500)
	offset := max(0, params.Offset)
	depth := clampInt(params.Depth, 1, 1, 3)

	allEntries := filterAndSortDirEntries(entries)
	totalTopLevel := len(allEntries)

	end := offset + limit
	if offset > totalTopLevel {
		offset = totalTopLevel
	}
	if end > totalTopLevel {
		end = totalTopLevel
	}
	topSlice := allEntries[offset:end]
	hasMore := (offset + limit) < totalTopLevel

	items := collectDirItems(resolvedPath, topSlice, root, workspaceBound, depth, limit)
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"currentPath": resolvedPath,
		"items":       items,
		"limit":       limit,
		"offset":      offset,
		"depth":       depth,
		"hasMore":     hasMore,
	}, nil)
}

// ── list_directory helpers ──────────────────────────────────────────────────────────

func clampInt(v, zeroVal, minVal, maxVal int) int {
	if v == 0 {
		return zeroVal
	}
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// filterAndSortDirEntries 过滤隐藏条目（. 开头），并按「目录优先→字母序」排序。
func filterAndSortDirEntries(entries []os.DirEntry) []os.DirEntry {
	var visible []os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		visible = append(visible, e)
	}
	sort.Slice(visible, func(i, j int) bool {
		aDir, bDir := visible[i].IsDir(), visible[j].IsDir()
		if aDir != bDir {
			return aDir // dirs first
		}
		return visible[i].Name() < visible[j].Name()
	})
	return visible
}

// collectDirItems 把当前层的 entries 转为 []directoryItem，并按 depth 递归子目录（仅真实
// 目录，不含 symlink）。limit 参数控制每层递归的子条目上限，防止 depth=3 时响应爆炸。
func collectDirItems(parent string, entries []os.DirEntry, workspaceRoot string, workspaceBound bool, depth, limit int) []directoryItem {
	var items []directoryItem
	for _, e := range entries {
		name := e.Name()
		isSymlink := e.Type()&os.ModeSymlink != 0
		isDir := e.IsDir()
		itemPath := filepath.Join(parent, name)

		items = append(items, directoryItem{
			Name:        name,
			Path:        itemPath,
			IsDirectory: isDir,
			IsSymlink:   isSymlink,
		})

		// 递归子目录：仅当 depth > 1 且是真实目录（非 symlink）。
		if depth > 1 && isDir && !isSymlink {
			childPath := itemPath
			if workspaceBound && !pathIsWithinRoot(workspaceRoot, childPath) {
				continue // defense in depth：已由父层校验，补充检查
			}
			childEntries, err := os.ReadDir(childPath)
			if err != nil {
				continue // 允许权限等读失败（静默跳过，不阻塞整体列表）
			}
			childVisible := filterAndSortDirEntries(childEntries)
			if len(childVisible) > limit {
				childVisible = childVisible[:limit]
			}
			children := collectDirItems(childPath, childVisible, workspaceRoot, workspaceBound, depth-1, limit)
			items = append(items, children...)
		}
	}
	return items
}

// directoryItem 是 list_directory 响应的单条目；放在 file-level 以在测试中可见。
type directoryItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"isDirectory"`
	IsSymlink   bool   `json:"isSymlink,omitempty"`
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
	resolved, info, err := resolveAuthorizedReadFileCandidate(root, candidate)
	if err == nil {
		return resolved, info, nil
	}

	// Markdown produced by coding agents sometimes uses a display filename as the href
	// (for example [plan.md](plan.md)) even when the file lives below the workspace root.
	// Preserve normal path semantics first; only a single-component relative basename that
	// does not exist at root is eligible for a bounded unique-name lookup. Never guess when
	// multiple files share the name.
	if filepath.IsAbs(cleanPath) || filepath.Base(cleanPath) != cleanPath || !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	if isSensitiveReadFilePath(filepath.Join(root, cleanPath)) {
		return "", nil, &WireError{Code: "file.sensitive_path_denied", Message: "sensitive file access is denied"}
	}
	matched, matchErr := findUniqueAuthorizedBasename(root, cleanPath)
	if matchErr != nil {
		return "", nil, matchErr
	}
	if matched == "" {
		return "", nil, err
	}
	return resolveAuthorizedReadFileCandidate(root, matched)
}

const maxAuthorizedBasenameSearchEntries = 50_000

var (
	errAuthorizedBasenameAmbiguous = errors.New("authorized basename is ambiguous")
	errAuthorizedBasenameLimit     = errors.New("authorized basename search limit exceeded")
)

func findUniqueAuthorizedBasename(root, basename string) (string, error) {
	var match string
	visited := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && shouldSkipBasenameSearchDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		visited++
		if visited > maxAuthorizedBasenameSearchEntries {
			return errAuthorizedBasenameLimit
		}
		if !entry.Type().IsRegular() || entry.Name() != basename {
			return nil
		}
		if match != "" {
			return errAuthorizedBasenameAmbiguous
		}
		match = path
		return nil
	})
	switch {
	case errors.Is(err, errAuthorizedBasenameAmbiguous):
		return "", &WireError{Code: "file.basename_ambiguous", Message: "filename matches multiple files in the authorized workspace"}
	case errors.Is(err, errAuthorizedBasenameLimit):
		return "", &WireError{Code: "file.basename_search_limit", Message: "workspace is too large for filename-only lookup; use a relative or absolute path"}
	case err != nil:
		return "", err
	default:
		return match, nil
	}
}

func shouldSkipBasenameSearchDirectory(name string) bool {
	switch name {
	case ".git", ".build", "build", "DerivedData", "node_modules", "Pods":
		return true
	default:
		return false
	}
}

func resolveAuthorizedReadFileCandidate(root, candidate string) (string, os.FileInfo, error) {
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
