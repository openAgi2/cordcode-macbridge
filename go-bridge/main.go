package gobridge

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	// Register cc-connect agents via init()
	_ "github.com/openAgi2/cordcode-macbridge/agent/claudecode"
	_ "github.com/openAgi2/cordcode-macbridge/agent/codex"
	_ "github.com/openAgi2/cordcode-macbridge/agent/codex-web"
	_ "github.com/openAgi2/cordcode-macbridge/agent/dsh"
	_ "github.com/openAgi2/cordcode-macbridge/agent/dsh-web"
	_ "github.com/openAgi2/cordcode-macbridge/agent/grokbuild"
	_ "github.com/openAgi2/cordcode-macbridge/agent/opencode"
	_ "github.com/openAgi2/cordcode-macbridge/agent/opencode-web"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

const defaultDrivers = "claude,codex,codex-web,grokbuild,dsh-web,opencode-web"

func Main() {
	port := flag.Int("port", 8777, "WebSocket listen port")
	// 老 opencode backend 移除（owner 2026-08-19：与 opencode-web 双订阅同一 serve 互扰）；
	// 代码保留，回滚加回 "opencode" 即可。
	drivers := flag.String("drivers", defaultDrivers, "Comma-separated agent list")
	workDir := flag.String("work-dir", "", "Working directory for agents (default: cwd)")
	showVersion := flag.Bool("version", false, "Print runtime version and exit")
	codexBackend := flag.String("codex-backend", envOr("GO_BRIDGE_CODEX_BACKEND", "exec"), "Codex backend mode: exec or app_server")
	codexAppServerURL := flag.String("codex-app-server-url", envOr("GO_BRIDGE_CODEX_APP_SERVER_URL", ""), "Optional Codex app-server listen URL")
	// codex-web：官方长驻 app-server 的 JSON-RPC 客户端 backend。显式 URL 只用于
	// 隔离测试/非 Desktop 实验；产品空值 = 官方 daemon 复用→managed start，失败可见，
	// 禁止回落到 Desktop 无法连接的第二个 loopback app-server。
	codexWebAppServerURL := flag.String("codex-web-app-server-url", envOr("GO_BRIDGE_CODEX_WEB_APP_SERVER_URL", ""), "Optional shared Codex app-server URL for the codex-web backend (loopback ws://)")

	// opencode direct HTTP API。默认空 = 未配置（不隐式回落 64667）；显式 loopback URL 时
	// 连接用户/运维已启动的 stable `opencode serve`。URL 经 MacBridge 校验为 loopback 后传入。
	ocBaseURL := flag.String("opencode-url", envOr("OPENCODE_BASE_URL", ""), "OpenCode HTTP API URL (loopback, e.g. http://127.0.0.1:<port>). Empty = not configured.")
	ocUser := flag.String("opencode-user", envOr("OPENCODE_SERVER_USERNAME", ""), "OpenCode auth username")
	ocPass := flag.String("opencode-pass", envOr("OPENCODE_SERVER_PASSWORD", ""), "OpenCode auth password")

	// opencode-web：官方 serve 的纯 HTTP/SSE 客户端 backend（设计
	// docs/2026-08-18-opencode-web-backend-design.md）。与旧 opencode 物理隔离、
	// 键名分开（新包读 opencode_web_*，绝不读 opencode_url）；并存期 Swift 把同一
	// 已解析 URL 同时传给两组 flag。凭据经进程环境注入（不常驻 argv）。
	ocwBaseURL := flag.String("opencode-web-url", envOr("OPENCODE_WEB_BASE_URL", ""), "OpenCode Web HTTP API URL (same resolved serve URL as -opencode-url during coexistence). Empty = not configured.")
	ocwUser := flag.String("opencode-web-user", envOr("OPENCODE_WEB_SERVER_USERNAME", ""), "OpenCode Web auth username")
	ocwPass := flag.String("opencode-web-pass", envOr("OPENCODE_WEB_SERVER_PASSWORD", ""), "OpenCode Web auth password")

	// 管理 API（Mac App product 模式使用，开发模式不启用）
	managementHost := flag.String("management-host", "", "Management API host (product mode: 127.0.0.1)")
	managementPort := flag.Int("management-port", 0, "Management API port (0 = disabled)")
	managementToken := flag.String("management-token", envOr("CORDCODE_MANAGEMENT_TOKEN", ""), "Management API auth token")
	dataDirPath := flag.String("data-dir", "", "Data directory for runtime state")
	logDirPath := flag.String("log-dir", "", "Log directory")
	remoteURL := flag.String("remote-url", "", "外部可达的 Bridge WebSocket URL（如 wss://my-tailscale:8777/bridge）")
	tlsPort := flag.Int("tls-port", 8778, "TLS listen port for wss:// remote access (0 = disabled)")
	// topologyMonitor 默认 on：owner 门已验证 shared-only、mixed 与采样失败三条真实路径；
	// S2/S5 因本任务运行在 shared Desktop 中按计划记录 blocked_manual_owner_close。
	topologyMonitor := flag.Bool("topology-monitor", envOr("CODEX_TOPOLOGY_MONITOR", "1") == "1", "Enable read-only topology monitor (default on)")
	// devInsecureWS 仅用于本地开发：允许 Tailscale 远程候选在 TLS 不可用时降级为明文 ws://。
	// 产品模式下不得启用——TLS 失败应禁用候选而非明文暴露 bearer token/业务内容（P1-4）。
	devInsecureWS := flag.Bool("dev-insecure-ws", envOr("CORDCODE_DEV_INSECURE_WS", "") != "", "DEV ONLY: allow plaintext ws:// Tailscale remote when TLS unavailable (fail-open). Product must leave unset.")
	includeTailscale := flag.Bool("pairing-include-tailscale", true, "Include detected Tailscale URL in pairing QR")
	includeRemote := flag.Bool("pairing-include-remote", true, "Include manual remote URL in pairing QR")
	relayEnabled := flag.Bool("relay-enabled", true, "Enable encrypted relay path")
	// preferLocalNetwork 镜像 -relay-enabled 的 bool flag pattern(argv = 形式由 Mac Swift
	// RuntimeManager.processArguments 生成)。默认 false:Relay 是稳定连接底座,LAN 是 Mac owner
	// 显式开启的性能优化。control-plane only,不下发进 timeline。
	preferLocalNetwork := flag.Bool("prefer-local-network", false, "Prefer same-LAN direct connection when available (opt-in; falls back to relay)")
	sessionListLimit := flag.Int("session-list-limit", defaultSessionListLimit, "Maximum sessions returned per list request (1-150)")

	// Relay 加密通道配置（首版：通过 flags 或环境变量注入，后续由 MacBridge runtime config 驱动）
	relayEndpoint := flag.String("relay-endpoint", envOr("CORDCODE_RELAY_ENDPOINT", ""), "Relay 服务端点（wss://relay.example.com）")
	relayRouteID := flag.String("relay-route-id", envOr("CORDCODE_RELAY_ROUTE_ID", ""), "Relay 路由 ID（由 relay 服务分配）")
	relayCredential := flag.String("relay-credential", envOr("CORDCODE_RELAY_CREDENTIAL", ""), "Relay 认证凭据（opaque，不复用 device token）")
	relayServiceAddr := flag.String("relay-service-addr", "", "Local-only in-process relay test listener (for example 127.0.0.1:8780)")

	flag.Parse()
	if *showVersion {
		fmt.Println(runtimeVersionString())
		return
	}
	bridgeEpoch, err := generateBridgeEpoch()
	if err != nil {
		slog.Error("go-bridge: bridge epoch generation failed", "error", err)
		WriteErrorFrame(RuntimeErrorConfigInvalid, err.Error())
		return
	}
	// Strip control-plane secrets from the go-bridge process's own environment
	// right after they are parsed. This prevents any future fork done by the
	// bridge itself (including helper goroutines that call os.Environ()) from
	// re-inheriting them. The authoritative fix for agent subprocesses is
	// core.BuildAgentEnv; this is defense-in-depth on the supervisor side.
	clearControlPlaneEnv()

	// logDirPath 保留供未来日志重定向使用
	_ = logDirPath

	if *workDir == "" {
		if dir, err := os.Getwd(); err == nil {
			*workDir = dir
		} else {
			*workDir = "."
		}
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handlers := NewHandlersWithContextAndEpoch(ctx, bridgeEpoch)
	handlers.SetRelayEnabled(*relayEnabled)
	handlers.SetSessionListLimit(*sessionListLimit)
	handlers.SetDataDir(*dataDirPath)
	var webPushPipeline *WebPushCandidatePipeline
	if *dataDirPath != "" {
		handlers.SetTranscriptIndexBaseDir(*dataDirPath + string(filepath.Separator) + "transcript-index")
		// Web Push store：损坏按 misconfigured fail-closed（capability 关闭、RPC 拒绝 register，
		// unregister/显式 Reset 仍可恢复）；仅真实 IO 错误才放弃接线（capability 保持关闭）。
		webPushStore, webPushErr := LoadWebPushStore(*dataDirPath)
		if webPushErr != nil {
			slog.Error("go-bridge: web push store 加载失败（capability 保持关闭）", "error", webPushErr)
		} else {
			handlers.SetWebPushStore(webPushStore)
			globalWebPushStore = webPushStore
			// candidate 管线：ledger 去重 + 有界队列 + deferred 注册表（dispatcher 为 E1）。
			pipeline := NewWebPushCandidatePipeline(webPushStore)
			webPushPipeline = pipeline
			handlers.SetWebPushPipeline(pipeline)
			if status, detail := webPushStore.Status(); status == WebPushStoreMisconfigured {
				slog.Warn("go-bridge: web push store misconfigured（register 关闭，设置页可重置）", "detail", detail)
			}
		}
	}

	// Process-wide session pin store (置顶). Lives under the bridge data dir so it shares
	// lifetime/backup semantics with identity.json/config.json. Injected into every driver
	// that implements core.SessionPinner (claudecode/codex/opencode) via opts["pin_store"];
	// the handler also reads it for set_session_pinned / list_pinned_sessions.
	var pinStore *pinstore.Store
	if dir := strings.TrimSpace(*dataDirPath); dir != "" {
		pinStore = pinstore.New(dir)
		handlers.SetPinStore(pinStore)
	}

	agentAliases := map[string]string{
		"claude":    "claudecode",
		"opencode":  "opencode",
		"codex":     "codex",
		"grokbuild": "grokbuild",
		"deepseek":  "dsh",
	}

	for _, id := range strings.Split(*drivers, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		agentName := agentAliases[id]
		if agentName == "" {
			agentName = id
		}

		agentOpts := buildAgentOptions(id, agentOptionsConfig{
			workDir:           *workDir,
			openCodeURL:       *ocBaseURL,
			openCodeUser:      *ocUser,
			openCodePass:      *ocPass,
			openCodeWebURL:    *ocwBaseURL,
			openCodeWebUser:   *ocwUser,
			openCodeWebPass:   *ocwPass,
			codexBackend:      *codexBackend,
			codexAppServerURL: *codexAppServerURL,
			codexWebAppSrvURL: *codexWebAppServerURL,
			pinStore:          pinStore,
			dataDir:           *dataDirPath,
		})

		agent, err := core.CreateAgent(agentName, agentOpts)
		if err != nil {
			slog.Error("go-bridge: failed to create agent", "agent", agentName, "error", err)
			continue
		}
		if err := applyProviderSeed(agent, agentName, *workDir); err != nil {
			slog.Warn("go-bridge: failed to load provider seed", "agent", agentName, "error", err)
		}

		// claudecode：启动时注入默认 reasoning effort。Claude Code transcript 不记录
		// per-session effort，故「session 的 effort」= Mac 端 Claude Code 当前的全局 effort。
		// 真值源优先级：iOS 持久化 override（claude-effort.json）> ~/.claude/settings.json
		// 的 effortLevel > env CLAUDE_CODE_EFFORT_LEVEL。注入后 enrichSessionStateWithAgent
		// 的回填会把它带给 iOS，使打开任意 Claude Code session 都显示与 Mac 端一致的智能等级。
		if agentName == "claudecode" {
			effort := resolveClaudeDefaultEffort(*dataDirPath)
			if effort == "" {
				slog.Info("go-bridge: claudecode default effort: none (no settings.json effortLevel and no iOS override)")
			} else {
				source := "settings.json"
				if normalizeClaudeRuntimeEffort(loadClaudeEffortOverride(*dataDirPath)) == effort {
					source = "ios-override"
				}
				if re, ok := agent.(core.ReasoningEffortSwitcher); ok {
					re.SetReasoningEffort(effort)
				}
				slog.Info("go-bridge: claudecode default effort applied", "effort", effort, "source", source)
			}
		}

		handlers.RegisterAgent(id, agent)
		if id == "codex" {
			handlers.SetCodexBackendMode(*codexBackend)
		}
		slog.Info("go-bridge: agent registered", "backendId", id, "agent", agentName, "workDir", *workDir)

		if sub, ok := agent.(core.EventSubscriber); ok && shouldStartPassiveSubscription(id, *codexBackend, *codexAppServerURL, *ocBaseURL, *ocwBaseURL) {
			go startPassiveSubscription(ctx, handlers, id, sub)
		}

		// opencode: also register a direct HTTP proxy
		if id == "opencode" && *ocBaseURL != "" {
			proxy := NewOpenCodeProxy(*ocBaseURL, *ocUser, *ocPass)
			handlers.RegisterOpenCodeProxy(proxy)
			slog.Info("go-bridge: opencode HTTP proxy registered", "url", *ocBaseURL)
		}
	}
	if len(handlers.BackendList()) == 0 {
		slog.Error("go-bridge: no agents available, exiting")
		WriteErrorFrame(RuntimeErrorNoAgents, "no agents available")
		os.Exit(1)
	}

	handlers.Start(ctx) // T09: 显式启动 observation lease loop（构造函数不再自动起 goroutine）
	handlers.StartCleanupLoop(60 * time.Second)
	handlers.StartSessionDiscoveryWatcher(ctx) // 可选：新外部 session → "sessions_changed" push
	handlers.StartCodexRelayWatcher(ctx)       // 安全网：订阅中的 codex session 始终有 relay 在跑
	var dataDir *DataDir
	if *dataDirPath != "" {
		dataDir = NewDataDir(*dataDirPath)
		if err := dataDir.Initialize(); err != nil {
			slog.Error("go-bridge: 数据目录初始化失败", "error", err)
			WriteErrorFrame(RuntimeErrorConfigInvalid, err.Error())
			os.Exit(1)
		}
	}
	bridgeID, err := loadStableBridgeID(dataDir)
	if err != nil {
		slog.Error("go-bridge: Bridge identity 读取失败", "error", err)
		WriteErrorFrame(RuntimeErrorConfigInvalid, err.Error())
		os.Exit(1)
	}
	if webPushPipeline != nil {
		webPushPipeline.SetBridgeID(bridgeID)
		// §8.4：固定 worker 数消费有界队列；发送全在锁外。
		webPushDispatcher := NewWebPushDispatcher(globalWebPushStore, webPushPipeline, WebPushDispatcherConfig{})
		webPushDispatcher.Start()
		defer webPushDispatcher.Stop()
	}
	advertisedLocalURL := BuildBridgeLocalURL(ResolveAdvertisedHost(), *port)
	// advertisedLocalURLs:全部 LAN 直连候选(主候选 advertisedLocalURL 在前),用于 relay-first completion
	// (RelayFirstResult.LocalURLs,iOS 取 [0] 为 primary)与 hello_ack locals。空表示 Mac 不在任何 LAN(纯 relay)。
	// 不含 Tailscale 候选(需独立 TLS pin,relay-first completion 本期不下发 pin)。
	advertisedLocalURLs := BuildBridgeLocalURLs(*port)

	// 自动检测 Tailscale IP 作为独立远程候选，不覆盖手动配置的 FRP/VPS URL。
	// 决策逻辑见 resolveTailscaleRemote：产品模式 TLS 不可用不降级为 ws://（P1-4 fail-closed）。
	// T00: 传入 dataDir 使证书持久化、派生 SPKI pin；dataDir 在 product 模式必非 nil。
	tsDecision := resolveTailscaleRemote(detectTailscaleIP(), *tlsPort, *port, *devInsecureWS, dataDir)
	tlsCert := tsDecision.tlsCert
	tlsPin := tsDecision.tlsPin
	tailscaleURL := tsDecision.tailscaleURL
	if tailscaleURL != "" {
		slog.Info("go-bridge: Tailscale 远程候选已发布", "url", tailscaleURL)
	}

	// 管理 API 启动（仅 product 模式：management-host 和 management-token 都非空时启用）
	var managementURL string
	var mgmtSrv *ManagementServer
	var relayIdentity *RelayCryptoIdentity
	relayConfigured := *relayEndpoint != "" && *relayRouteID != "" && *relayCredential != "" && *relayEnabled
	if relayConfigured {
		var identityErr error
		relayIdentity, identityErr = LoadOrCreateRelayCryptoIdentity(*dataDirPath)
		if identityErr != nil {
			slog.Warn("relay: identity load/create failed, relay features disabled", "error", identityErr)
			relayConfigured = false
		}
	}
	// topology monitor（owner gate 后默认 on；CODEX_TOPOLOGY_MONITOR=0 可显式关闭；
	// codex-web 后端提供 transport identity provider）。
	var topologyProvider TopologyProvider
	if *topologyMonitor {
		provCfg := TopologyMonitorConfig{Collector: NewTopologyCollector(),
			BridgeEpoch: func() uint64 { return managementBridgeEpoch(bridgeEpoch) }}
		if ag, ok := handlers.Agents()["codex-web"]; ok {
			if idProv, idOK := ag.(core.CodexWebTransportIdentityProvider); idOK {
				provCfg.Identity = idProv
			} else {
				slog.Warn("go-bridge: codex-web agent does not implement transport identity provider; topology bridge dimension stays unresolved")
			}
		} else {
			slog.Warn("go-bridge: codex-web agent not present; topology bridge dimension stays unresolved")
		}
		svc := NewTopologyMonitorService(provCfg)
		svc.Start(ctx)
		topologyProvider = svc
		slog.Info("go-bridge: topology monitor enabled")
	}

	if *managementHost != "" && *managementToken != "" {
		displayName := loadOrCreateDisplayName(dataDir)

		mgmtCfg := ManagementConfig{
			Handlers:           handlers,
			Token:              *managementToken,
			DataDir:            dataDir,
			PairingStore:       func() PairingSessionStore { s := NewMemoryPairingStore(); globalPairingStore = s; return s }(),
			DeviceStore:        func() TrustedDeviceStore { s := newTrustedDeviceStore(dataDir); globalDeviceStore = s; return s }(),
			BridgeID:           bridgeID,
			DisplayName:        displayName,
			LocalURL:           advertisedLocalURL,
			LocalURLs:          advertisedLocalURLs,
			TailscaleURL:       tailscaleURL,
			RemoteURL:          *remoteURL,
			IncludeTailscale:   *includeTailscale,
			IncludeRemote:      *includeRemote,
			RelayEndpoint:      *relayEndpoint,
			RelayRouteID:       *relayRouteID,
			RelayCredential:    *relayCredential,
			RelayConfigured:    relayConfigured,
			RelayEnabled:       *relayEnabled,
			RelayIdentity:      relayIdentity,
			PreferLocalNetwork: *preferLocalNetwork,
			Agents:             handlers.agents,
			CodexBackendMode:   *codexBackend,
			DetectionCfg: &AgentDetectionConfig{
				OpenCodeURL:       *ocBaseURL,
				OpenCodeUser:      *ocUser,
				OpenCodePass:      *ocPass,
				CodexAppServerURL: *codexAppServerURL,
			},
			TLSPin: tlsPin,
			RuntimeIdentity: admission.RuntimeIdentity{
				PID: int32(os.Getpid()), BridgeEpoch: managementBridgeEpoch(bridgeEpoch),
			},
			TopologyProvider: topologyProvider,
		}
		mgmtSrv = NewManagementServer(mgmtCfg)

		actualPort, err := mgmtSrv.Start(*managementHost, *managementPort)
		if err != nil {
			// P1-6: product 模式下管理 API 是必需依赖。监听失败应 fail-fast：
			// 写结构化错误帧并退出，绝不写 ready frame（否则 Mac 端拿到空 managementUrl 只能静默卡住）。
			slog.Error("go-bridge: 管理 API 启动失败，fail-fast 退出", "error", err)
			WriteErrorFrame(RuntimeErrorManagementBindFailed, err.Error())
			os.Exit(1)
		}
		managementURL = fmt.Sprintf("http://%s:%d", *managementHost, actualPort)
		slog.Info("go-bridge: 管理 API 就绪", "url", managementURL)
	}

	// P1-6: product 模式（已配置 managementHost+token）下，ready frame 必须携带非空 managementUrl。
	// 空地址意味着 Mac App 无法管理子进程，属致命启动契约违例。
	if *managementHost != "" && *managementToken != "" && managementURL == "" {
		WriteErrorFrame(RuntimeErrorManagementURLMissing, "product mode requires a non-empty managementUrl in the ready frame")
		os.Exit(1)
	}

	server := NewServerWithEpoch(handlers, bridgeEpoch)
	server.SetRecoveryEnabled(true)
	// K1 builds the complete dark path while production negotiation remains disabled. A later
	// controlled shadow rollout must deliberately change the versioned gate.
	server.SetSessionSyncV2Enabled(sessionSyncV2ProductionEnabled)
	// PERF-S4D release gate (frozen §Projection Window release ordering — client first,
	// server flip last): the iOS replica (S4C) and the producer (S4B) have shipped behind
	// this flag on this branch. Rollback = set false (or remove this call): every session
	// returns to the frozen full-projection path, no data is touched, no legacy writer
	// returns. Undeclared/flag-off peers are byte-identical to today by contract.
	server.SetProjectionWindowEnabled(true)
	serverDisplayName := "CordCode Link"
	if mgmtSrv != nil {
		serverDisplayName = mgmtSrv.DisplayName()
	}
	server.SetBridgeIdentity(
		bridgeID,
		serverDisplayName,
		runtimeVersionString(),
		advertisedLocalURL,
		firstNonEmpty(remoteIdentityURLs(tailscaleURL, *remoteURL, *includeTailscale, *includeRemote)...),
		remoteIdentityURLs(tailscaleURL, *remoteURL, *includeTailscale, *includeRemote)...,
	)
	server.SetLocalCandidateURLs(advertisedLocalURLs)
	// control-plane 连接策略注入 Server,供 direct 与 relay 两处 hello handler 在 hello_ack 下发。
	server.SetConnectionPolicy(ConnectionPolicy{PreferLocalNetwork: *preferLocalNetwork})
	server.SetDetectionConfig(&AgentDetectionConfig{
		OpenCodeURL:       *ocBaseURL,
		OpenCodeUser:      *ocUser,
		OpenCodePass:      *ocPass,
		CodexAppServerURL: *codexAppServerURL,
	})
	if globalDeviceStore != nil {
		server.SetAuthMiddleware(NewAuthMiddleware(globalDeviceStore))
	}

	// 设置 relay hello handler：relay 加密通道收到 hello 时走和直连相同的 handleHello 路径。
	handlers.SetRelayHelloHandler(func(conn Connection, msg *WireMessage) {
		device := conn.AuthedDevice()
		var hello HelloMessage
		if msg.Client != nil {
			_ = json.Unmarshal(msg.Client, &hello.Client)
		}
		if msg.Protocol != nil {
			_ = json.Unmarshal(msg.Protocol, &hello.Protocol)
		}
		hello.Type = msg.Type
		hello.Capabilities = msg.Capabilities
		hello.LastBridgeEpoch = msg.LastBridgeEpoch
		hello.LastEventID = msg.LastEventID
		hello.LastSeenBySession = msg.LastSeenBySession
		ack := HandleHelloWithRemoteURLs(
			&hello,
			device,
			server.bridgeID,
			server.displayName,
			server.runtimeVersion,
			server.localURL,
			server.remoteURL,
			server.remoteURLs,
			server.localCandidateURLs,
			handlers.Agents(),
			handlers.CodexBackendMode(),
			server.detectionCfg,
			handlers.sessions,
		)
		// relay 路径同样权威下发 control-plane 连接策略(与 direct hello 同源 server.connectionPolicy)。
		if ack.Bridge != nil {
			ack.Bridge.ConnectionPolicy = &server.connectionPolicy
		}
		ack.BridgeEpoch = bridgeEpoch
		// web_push_v1 协商（relay 路径；与 direct server.go handleHello 共用同一 helper）。
		ApplyWebPushHelloProfile(ack, &hello, handlers.WebPushStoreRef())
		if ack.Ok && negotiateRelayGzip(conn, hello.Capabilities) {
			ack.Capabilities[relayGzipCapability] = true
		}
		if ack.Ok && negotiateRelayChunks(conn, hello.Capabilities) {
			ack.Capabilities[relayChunksCapability] = true
		}
		// R1.4：relay_chunk_progress_v1（progress ⇒ chunks；client 保证不同时 ack progress 而不 ack chunks）。
		// ack 后 Mac 才会对 read_file_v2 correlated chunk stamp bulkCorrelationId。
		if ack.Ok && negotiateRelayChunkProgress(conn, hello.Capabilities) {
			ack.Capabilities[relayChunkProgressCapability] = true
		}
		// R1.5：cancel_request_v1（read_file_v2 bulk cancel control RPC）。echo 后 iOS 才发送 cancel。
		if ack.Ok && negotiateRelayCancel(conn, hello.Capabilities) {
			ack.Capabilities[relayCancelCapability] = true
		}
		if ack.Ok && helloSupportsReadFileV2(&hello) {
			ack.Capabilities["read_file_v2"] = true
			server.eventPublisher.SetConnReadFileV2(conn, true)
		}
		// Phase 7 §446: observe catalog_cursor_epoch_v2 declaration rate (relay path). Mirrors
		// server.go direct path; per-hello boolean mineable for v1-blind-cut retirement threshold.
		catalogV2 := ack.Ok && helloSupportsCatalogCursorEpochV2(&hello)
		if catalogV2 {
			ack.Capabilities["catalog_cursor_epoch_v2"] = true
			server.eventPublisher.SetConnCatalogCursorEpochV2(conn, true)
		}
		var replay []EventMessage
		if server.recoveryEnabled && helloSupportsRecovery(&hello) && ack.Ok {
			plan, events, err := server.prepareRecovery(conn, &hello)
			if err != nil {
				slog.Warn("relay-bridge-client: recovery preparation failed", "error", err)
				_ = conn.Close()
				return
			}
			ack.Recovery = plan
			ack.Capabilities["recovery_v1"] = true
			replay = events
		}
		if server.sessionSyncV2Enabled && helloSupportsSessionSyncV2(&hello) && ack.Ok {
			ack.Capabilities["session_sync_v2"] = true
			advertiseSessionSyncV2Backend(ack.Backends)
			server.eventPublisher.SetConnSyncV2(conn, true)
			server.eventPublisher.SetConnProjectionEpoch(conn, hello.LastBridgeEpoch)
		}
		// projection_window_v1 relay path mirrors the direct hello negotiation exactly.
		if !server.negotiateProjectionWindowV1(ack, &hello, conn) {
			conn.SendJSON(ack)
			return
		}
		conn.SendJSON(ack)
		if ack.Recovery != nil {
			server.emitRecoveryFrames(conn, ack.Recovery, replay)
		}
		slog.Info("relay-bridge-client: hello_ack sent via relay", "ok", ack.Ok, "device", hello.Client.DeviceID, "catalog_cursor_epoch_v2", catalogV2)
	})

	http.Handle("/pairing", http.HandlerFunc(handlePairingWebSocket))
	http.Handle("/", server)

	addr := fmt.Sprintf(":%d", *port)
	// P2-1: 主端口监听所有网卡，必须设握手超时/header 上限/空闲超时防 slowloris。
	// 不设 WriteTimeout：会误杀长连接 WebSocket 数据面（gorilla 自带读写 deadline）。
	httpServer := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("go-bridge: server listen failed", "error", err)
		WriteErrorFrame(RuntimeErrorPortBindFailed, err.Error())
		os.Exit(1)
	}

	// Relay 使用独立 listener，只接收 opaque envelope，不能进入 Bridge RPC handler。
	var relayHTTPServer *http.Server
	var sharedRelayHub *RelayHub
	var localRelayEndpoint string

	// Relay 配置：当 endpoint + routeID + credential 都提供时启用加密通道升级。
	if strings.TrimSpace(*relayServiceAddr) != "" {
		listenAddr, relayErr := localRelayServiceListenAddress(*relayServiceAddr)
		if relayErr != nil {
			slog.Error("relay-service: only loopback test listeners are allowed", "error", relayErr)
			WriteErrorFrame(RuntimeErrorPortBindFailed, relayErr.Error())
			os.Exit(1)
		}
		relayListener, relayErr := net.Listen("tcp", listenAddr)
		if relayErr != nil {
			slog.Error("relay-service: listen failed", "addr", listenAddr, "error", relayErr)
			WriteErrorFrame(RuntimeErrorPortBindFailed, relayErr.Error())
			os.Exit(1)
		}
		sharedRelayHub = NewRelayHub()
		localRelayEndpoint = "ws://" + relayListener.Addr().String()
		relayHTTPServer = &http.Server{
			Handler:           NewRelayService(sharedRelayHub),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    16 << 10,
		}
		go func() {
			if serveErr := relayHTTPServer.Serve(relayListener); serveErr != nil && serveErr != http.ErrServerClosed {
				slog.Error("relay-service: serve failed", "error", serveErr)
			}
		}()
		slog.Info("relay-service: listening", "addr", relayListener.Addr().String())
	}

	// 配置 relay 加密通道升级：bridge identity + provisioner
	var relayBridgeClient *RelayBridgeClient
	if relayConfigured {
		if relayIdentity != nil {
			provisioner := relayUpgradeProvisioner(*relayEndpoint, *relayRouteID, *relayCredential, sharedRelayHub)
			handlers.ConfigureRelayUpgrade(globalDeviceStore, relayIdentity, provisioner)
			slog.Info("relay: encrypted channel upgrade configured", "endpoint", *relayEndpoint, "routeID", *relayRouteID)

			// 启动 relay bridge client：连接到 relay service 的 bridge WebSocket，
			// 处理设备握手，为每个已认证设备创建 RelayDeviceConn 并注册到 Broadcaster。
			relayBridgeClient = NewRelayBridgeClient(handlers, sharedRelayHub, relayIdentity, bridgeID, *relayRouteID, *relayCredential)
			// 真实 client 构造后、Run 前,原子替换 management server 的 default disconnected
			// provider,使 /internal/remote/status 的 relay.connected 反映真实连接状态。
			// *RelayBridgeClient 凭现有 Connected()(内部锁)隐式满足 RelayConnectionStatusProvider。
			if mgmtSrv != nil {
				mgmtSrv.SetRelayStatusProvider(relayBridgeClient)
			}
			handlers.ConfigureRelayDelivery(*relayRouteID, relayBridgeClient.SendEnvelope)
			bridgeWSURL := strings.TrimRight(*relayEndpoint, "/") + "/v1/routes/" + *relayRouteID + "/bridge"
			if sharedRelayHub != nil {
				// 进程内 relay 联调时连接本地 listener，但向 iOS 发布配置的 endpoint。
				bridgeWSURL = localRelayEndpoint + "/v1/routes/" + *relayRouteID + "/bridge"
			}
			go relayBridgeClient.Run(ctx, bridgeWSURL)
			slog.Info("relay-bridge-client: starting with auto-reconnect", "bridgeWSURL", bridgeWSURL)
		}
	}

	// TLS server（供 Tailscale wss:// 远程连接，自签名证书）
	var tlsServer *http.Server
	if tlsCert != nil && *tlsPort > 0 {
		tlsServer = startTLSServer(http.DefaultServeMux, *tlsPort, tlsCert)
	}

	// 统一关停路径，供 SIGTERM 和管理 API /internal/shutdown 共用
	// 顺序（T02）：先停接收新 RPC（HTTP Server.Shutdown，graceful）
	//  → handlers.Shutdown（关闭 active session/agent 子进程，进程组回收）
	//  → 广播 shutdown / 关闭 active WS 连接（server.CloseAllConnections）
	//  → relayBridgeClient.Close() + relay/tls/mgmt Server.Shutdown
	shutdown := func() {
		cancel()
		slog.Info("go-bridge: shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		// 1. Stop accepting new RPC on the bridge HTTP server (graceful).
		httpShutdownDone := make(chan struct{})
		go func() {
			_ = httpServer.Shutdown(shutdownCtx)
			close(httpShutdownDone)
		}()
		select {
		case <-httpShutdownDone:
		case <-shutdownCtx.Done():
		}

		// 2. Close active agent sessions / subprocesses (process-group reaping).
		//    Bound by shutdownCtx so a wedged agent can't hang shutdown.
		handlersShutdownCtx, handlersShutdownCancel := context.WithTimeout(shutdownCtx, 8*time.Second)
		if err := handlers.Shutdown(handlersShutdownCtx); err != nil {
			slog.Warn("go-bridge: handlers.Shutdown error", "error", err)
		}
		handlersShutdownCancel()

		// 3. Broadcast shutdown / close active WS connections.
		closedConnCount := server.CloseAllConnections("bridge shutting down")
		slog.Info("go-bridge: closed active websocket connections before shutdown", "count", closedConnCount)

		// 4. Relay bridge client + relay/tls/mgmt servers.
		if relayBridgeClient != nil {
			relayBridgeClient.Close()
		}
		if relayHTTPServer != nil {
			_ = relayHTTPServer.Shutdown(shutdownCtx)
		}
		if tlsServer != nil {
			_ = tlsServer.Shutdown(shutdownCtx)
		}
		if mgmtSrv != nil {
			mgmtSrv.Shutdown()
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("go-bridge: shutting down", "signal", sig)
		shutdown()
	}()

	if mgmtSrv != nil {
		mgmtSrv.SetShutdownCallback(shutdown)
	}

	// 持久化 management token — 必须在 WriteReadyFrame 之前，
	// 否则 MacBridge 读到 runtime.json 但 token 文件还不存在。
	// T06: product 模式（已配置 dataDir + managementToken）下 management-token 写失败须
	// fail-fast：写 runtime_error.bootstrap_persist_failed 并 exit，绝不发布 ready frame。
	if *dataDirPath != "" && *managementToken != "" {
		// 原子写 management-token（P2-5）：避免崩溃留下空/截断文件导致 Mac App 取不到 token。
		if err := core.AtomicWriteFile(*dataDirPath+"/management-token", []byte(*managementToken), 0o600); err != nil {
			slog.Error("go-bridge: management-token 写入失败，fail-fast 退出", "error", err)
			WriteErrorFrame(RuntimeErrorBootstrapPersistFailed, "management-token write failed: "+err.Error())
			os.Exit(1)
		}
	}

	// 输出就绪帧（供 Mac App 解析）
	driverList := make([]string, 0, len(strings.Split(*drivers, ",")))
	for _, d := range strings.Split(*drivers, ",") {
		if trimmed := strings.TrimSpace(d); trimmed != "" {
			driverList = append(driverList, trimmed)
		}
	}
	// T06: WriteReadyFrame 现在返回 runtime.json 写入错误。product 模式下写失败须 fail-fast，
	// 不得发布 ready（否则磁盘满/权限错误时 UI 永远未就绪，每 60s 重启）。
	if err := WriteReadyFrame(*port, driverList, managementURL, *dataDirPath, bridgeEpoch); err != nil {
		slog.Error("go-bridge: ready frame 持久化失败，fail-fast 退出", "error", err)
		WriteErrorFrame(RuntimeErrorBootstrapPersistFailed, err.Error())
		os.Exit(1)
	}

	slog.Info("go-bridge: listening", "addr", addr, "drivers", *drivers)

	// runtime.json 周期重写（产品模式）：只写一次的话，Mac App 每次 launch 前删除该文件；
	// 若该次 launch 因 controller 持有健康进程抛 alreadyRunning，删除后无人补写，
	// App 管理轮询逐轮 early-return，UI 卡死「启动失败」。15s 原子重写使删除窗口自愈。
	if *dataDirPath != "" {
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if err := RewriteRuntimeJSON(); err != nil {
					slog.Error("go-bridge: runtime.json 周期重写失败", "error", err)
				}
			}
		}()
	}

	if err := httpServer.Serve(listener); err != nil && ctx.Err() == nil {
		slog.Error("go-bridge: server error", "error", err)
		os.Exit(1)
	}
}

func managementBridgeEpoch(bridgeEpoch string) uint64 {
	digest := sha256.Sum256([]byte(bridgeEpoch))
	epoch := binary.BigEndian.Uint64(digest[:8])
	if epoch == 0 {
		return 1
	}
	return epoch
}

func localRelayServiceListenAddress(raw string) (string, error) {
	address := strings.TrimSpace(raw)
	if strings.HasPrefix(address, ":") {
		address = "127.0.0.1" + address
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return "", fmt.Errorf("invalid local relay listen address %q", raw)
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return "", fmt.Errorf("invalid local relay listen port %q", port)
	}
	if host == "localhost" {
		return address, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("relay test listener must bind loopback, got %q", raw)
	}
	return address, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func loadStableBridgeID(dataDir *DataDir) (string, error) {
	if dataDir == nil {
		return GenerateBridgeID(), nil
	}
	identity, err := dataDir.ReadIdentity()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(identity.BridgeID) == "" {
		return "", fmt.Errorf("identity.json 缺少 bridgeId")
	}
	return identity.BridgeID, nil
}

func remoteIdentityURLs(tailscaleURL, remoteURL string, includeTailscale, includeRemote bool) []string {
	var urls []string
	if includeTailscale {
		urls = append(urls, tailscaleURL)
	}
	if includeRemote {
		urls = append(urls, remoteURL)
	}
	return uniqueNonEmptyStrings(urls)
}

func newTrustedDeviceStore(dataDir *DataDir) TrustedDeviceStore {
	if dataDir == nil {
		return NewMemoryDeviceStore()
	}
	store, err := NewFileDeviceStore(dataDir.Path() + "/devices.json")
	if err != nil {
		slog.Error("go-bridge: devices.json 加载失败，已配对设备全部失效；iOS 端将看到 auth.invalid_token / 服务器发出错误的响应", "path", dataDir.Path()+"/devices.json", "error", err)
		return NewMemoryDeviceStore()
	}
	return store
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func startPassiveSubscription(ctx context.Context, h *Handlers, backendID string, sub core.EventSubscriber) {
	backoff := 2 * time.Second
	maxBackoff := 60 * time.Second

	for {
		events, err := sub.Subscribe(ctx)
		if err != nil {
			slog.Error("go-bridge: passive subscribe failed", "backend", backendID, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = 2 * time.Second
		slog.Info("go-bridge: passive subscription started", "backend", backendID)

		for ev := range events {
			eventName, data, _ := mapAgentEvent(ev)
			if eventName == "todos_updated" || eventName == "turn_started" || eventName == "turn_completed" || eventName == "error" || eventName == "text_delta" || eventName == "permission_request" || eventName == "permission_resolved" || eventName == "question_asked" || eventName == "question_resolved" || eventName == "user_input_requested" || eventName == "user_input_resolved" || eventName == "session_retry_status" {
				slog.Info("go-bridge: passive event", "backend", backendID, "session", ev.SessionID, "event", eventName)
			} else {
				slog.Debug("go-bridge: passive event", "backend", backendID, "session", ev.SessionID, "event", eventName)
			}
			if eventName == "" {
				continue
			}

			// Sync session runtimeState from passive events to memory sessionRegistry
			if ev.SessionID != "" {
				if eventName == "turn_started" {
					h.sessions.markRunning(ev.SessionID)
				} else if eventName == "turn_completed" || eventName == "turn_error" || eventName == "turn_aborted" || eventName == "error" {
					// turn_error/turn_aborted settle a turn as failed/aborted
					// （bridge-v1.md）——此前漏列：失败 turn（如 opencode-web provider
					// 报错、旧 opencode 81ms 零输出）结束后 registry 永远 running，
					// 列表 runtimeState 卡「执行中」且冷开复种（owner 实测 2026-08-19）。
					h.sessions.markIdle(ev.SessionID)
				} else if eventName == "session_state_changed" {
					if dataMap, ok := data.(map[string]interface{}); ok {
						if state, ok := dataMap["state"].(string); ok {
							if state == "running" || state == "requiresAction" {
								h.sessions.markRunning(ev.SessionID)
							} else if state == "idle" {
								h.sessions.markIdle(ev.SessionID)
							}
						}
					}
				} else if eventName == "session_status_changed" {
					if dataMap, ok := data.(map[string]interface{}); ok {
						if isIdle, ok := dataMap["isIdle"].(bool); ok && isIdle {
							h.sessions.markIdle(ev.SessionID)
						}
					}
				}
			}

			// Audit-008 W1.1 — single timeline-ingest owner. The passive
			// path may only PUBLISH events for sessions that are observed
			// WITHOUT an active session-route relay (set_observation_scope
			// only): the relay loop (relayEvents) is the ingest owner for
			// every session that has a live subscriber, and both pipelines
			// decode the same daemon broadcast — feeding the Kernel twice
			// reproduces every text delta inside a single batched
			// append_text (owner 2026-08-24 真机: 流式正文逐 delta 重复).
			// Sessions with neither subscription nor observation interest are
			// catalog/control-only (§6.5): the registry bookkeeping above
			// already ran; feeding the Kernel here would build a hidden
			// timeline for a session nobody opened.
			hasKernelState := h.projectionKernel != nil &&
				h.projectionKernel.HasReducerState(backendID, ev.SessionID)
			if ev.SessionID == "" || passiveFeedAllowed(
				h.agentRelayRunningFor(ev.SessionID),
				h.observation != nil && h.observation.HasSessionInterest(backendID, ev.SessionID),
				hasKernelState,
				eventName) {
				h.deltaBatcher.Send(LogicalEvent{
					SessionID: ev.SessionID,
					BackendID: backendID,
					Event:     eventName,
					Data:      data,
					Broadcast: true,
					Offline:   IsDurableMilestone(eventName),
					// web push §8.1 producer 位点 2：被动泵仅在 passiveFeedAllowed
					// 放行时补投（单一摄入所有者的被动侧表达——agent relay 在跑时
					// 该分支不可达）。只认 terminal completion。
					PushIntent: pushIntentForPassiveEvent(h.projectionKernel, backendID, ev.SessionID, eventName, data),
				})
			}
		}
		slog.Info("go-bridge: passive subscription ended, reconnecting", "backend", backendID)

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// passiveFeedAllowed 决定被动泵是否向 kernel 补投该 session 的事件：通常只有
// 「有观察兴趣且该会话没有运行中的 agent relay」的会话由被动泵摄入；agent
// relay（relayEvents）是它生命周期的唯一摄入者，双路摄入会把同一官方增量
// 合并进同一次 append_text（审计-008 单一摄入所有者；owner 2026-08-24 真机
// 流式正文逐段重复）。以 agentRelayRunning 而不是「有订阅者」为界：外部观察
// 会话（Mac 发起、无 bridge AgentSession、codex 文件 relay 只覆盖 codex
// backend）有订阅者但没有 relay——被动泵必须继续兜底。
//
// 已经入核的外部会话是例外：用户切换到别的 session 后 observation scope 会移走，
// 但 Mac 发起的长 turn 仍可能稍后完成。terminal milestone 必须继续收口既有 projection，
// 否则 registry 虽被上面的 markIdle 收口，iOS 重开时拿到的 projection.execution 仍为
// running。只允许 terminal 且要求既有 kernel state，避免为从未打开的后台 session
// 构建隐藏 timeline；agent relay 存在时仍严格禁入，保持单一摄入所有者。
func passiveFeedAllowed(agentRelayRunning, hasObservation, hasKernelState bool, eventName string) bool {
	if agentRelayRunning {
		return false
	}
	if hasObservation {
		return true
	}
	return hasKernelState && passiveProjectionTerminalEvent(eventName)
}

func passiveProjectionTerminalEvent(eventName string) bool {
	switch eventName {
	case "turn_completed", "turn_error", "turn_aborted":
		return true
	default:
		return false
	}
}

func shouldStartPassiveSubscription(backendID, codexBackendMode, codexAppServerURL, openCodeURL, openCodeWebURL string) bool {
	if backendID == "codex" {
		return normalizeCodexBackend(codexBackendMode) == "app_server" && strings.TrimSpace(codexAppServerURL) != ""
	}
	if backendID == "opencode" {
		// 无 URL 时 OpenCode backend 处于 not_configured，不启动 SSE 订阅
		//（Subscribe 也会拒绝空 URL，这里提前避免无意义重连退避）。
		return strings.TrimSpace(openCodeURL) != ""
	}
	if backendID == "opencode-web" {
		// 同规则：纯 HTTP 客户端，空 URL = not_configured，不启动常驻 SSE
		//（设计 §2.1 坑 13——默认分支会无意义重连退避）。
		return strings.TrimSpace(openCodeWebURL) != ""
	}
	return true
}

type agentOptionsConfig struct {
	workDir           string
	openCodeURL       string
	openCodeUser      string
	openCodePass      string
	openCodeWebURL    string
	openCodeWebUser   string
	openCodeWebPass   string
	codexBackend      string
	codexAppServerURL string
	codexWebAppSrvURL string
	pinStore          *pinstore.Store
	dataDir           string
}

func buildAgentOptions(id string, cfg agentOptionsConfig) map[string]any {
	opts := map[string]any{
		"work_dir":      cfg.workDir,
		"mode":          "bypassPermissions",
		"opencode_url":  cfg.openCodeURL,
		"opencode_user": cfg.openCodeUser,
		"opencode_pass": cfg.openCodePass,
		"pin_store":     cfg.pinStore,
		"data_dir":      cfg.dataDir,
	}

	if id == "codex" {
		opts["mode"] = "custom"
		if normalizeCodexBackend(cfg.codexBackend) == "app_server" {
			opts["backend"] = "app_server"
			if url := strings.TrimSpace(cfg.codexAppServerURL); url != "" {
				opts["app_server_url"] = url
			}
		}
	}

	if id == "opencode-web" {
		// 新包读自己的键（设计 §4.1.2）——绝不复用 opencode_url 作为来源。
		opts["opencode_web_url"] = cfg.openCodeWebURL
		opts["opencode_web_user"] = cfg.openCodeWebUser
		opts["opencode_web_pass"] = cfg.openCodeWebPass
	}

	if id == "codex-web" {
		// codex-web 读自己的键（§5.1 独立身份）——绝不复用 codex_app_server_url：
		// 旧 codex 的共享 URL 是 app_server 模式选项，codex-web 的显式服务是 §6.1
		// 第 1 优先级探测输入，两者语义/回退行为不同。
		opts["codex_web_app_server_url"] = cfg.codexWebAppSrvURL
	}

	return opts
}

func normalizeCodexBackend(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "app-server", "app_server", "appserver", "ws":
		return "app_server"
	default:
		return "exec"
	}
}

func clearOpenCodeServerAuthEnv() {
	_ = os.Unsetenv("OPENCODE_SERVER_USERNAME")
	_ = os.Unsetenv("OPENCODE_SERVER_PASSWORD")
}

// clearControlPlaneEnv unsets go-bridge's control-plane secrets from the
// process environment after they have been parsed. Subsumes the legacy
// clearOpenCodeServerAuthEnv. The authoritative protection for agent
// subprocesses is core.BuildAgentEnv (deny-list + allowlist); this guard
// stops the supervisor itself from re-leaking via subsequent os.Environ().
func clearControlPlaneEnv() {
	for _, k := range []string{
		"CORDCODE_MANAGEMENT_TOKEN",
		"CORDCODE_RELAY_CREDENTIAL",
		"CORDCODE_RELAY_ROUTE_ID",
		"CORDCODE_RELAY_ENDPOINT",
		"OPENCODE_SERVER_USERNAME",
		"OPENCODE_SERVER_PASSWORD",
		"OPENCODE_WEB_SERVER_USERNAME",
		"OPENCODE_WEB_SERVER_PASSWORD",
		// Clear all other CORDCODE_* control-plane vars defensively (dev flags,
		// VPS creds, etc.). Keep the allowlisted runtime vars only if needed.
	} {
		_ = os.Unsetenv(k)
	}
	// Sweep remaining CORDCODE_* vars (e.g. CORDCODE_DEV_INSECURE_WS, VPS creds).
	for _, e := range os.Environ() {
		if k, _, ok := strings.Cut(e, "="); ok && strings.HasPrefix(k, "CORDCODE_") {
			_ = os.Unsetenv(k)
		}
	}
}
