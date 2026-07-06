package gobridge

import (
	"context"
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
	_ "github.com/openAgi2/cordcode-macbridge/agent/opencode"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

func Main() {
	port := flag.Int("port", 8777, "WebSocket listen port")
	drivers := flag.String("drivers", "claude,opencode,codex", "Comma-separated agent list")
	workDir := flag.String("work-dir", "", "Working directory for agents (default: cwd)")
	showVersion := flag.Bool("version", false, "Print runtime version and exit")
	codexBackend := flag.String("codex-backend", envOr("GO_BRIDGE_CODEX_BACKEND", "exec"), "Codex backend mode: exec or app_server")
	codexAppServerURL := flag.String("codex-app-server-url", envOr("GO_BRIDGE_CODEX_APP_SERVER_URL", ""), "Optional Codex app-server listen URL")

	// opencode direct HTTP API。默认空 = 未配置（不隐式回落 64667）；显式 loopback URL 时
	// 连接用户/运维已启动的 stable `opencode serve`。URL 经 MacBridge 校验为 loopback 后传入。
	ocBaseURL := flag.String("opencode-url", envOr("OPENCODE_BASE_URL", ""), "OpenCode HTTP API URL (loopback, e.g. http://127.0.0.1:<port>). Empty = not configured.")
	ocUser := flag.String("opencode-user", envOr("OPENCODE_SERVER_USERNAME", ""), "OpenCode auth username")
	ocPass := flag.String("opencode-pass", envOr("OPENCODE_SERVER_PASSWORD", ""), "OpenCode auth password")

	// 管理 API（Mac App product 模式使用，开发模式不启用）
	managementHost := flag.String("management-host", "", "Management API host (product mode: 127.0.0.1)")
	managementPort := flag.Int("management-port", 0, "Management API port (0 = disabled)")
	managementToken := flag.String("management-token", envOr("CORDCODE_MANAGEMENT_TOKEN", ""), "Management API auth token")
	dataDirPath := flag.String("data-dir", "", "Data directory for runtime state")
	logDirPath := flag.String("log-dir", "", "Log directory")
	remoteURL := flag.String("remote-url", "", "外部可达的 Bridge WebSocket URL（如 wss://my-tailscale:8777/bridge）")
	tlsPort := flag.Int("tls-port", 8778, "TLS listen port for wss:// remote access (0 = disabled)")
	// devInsecureWS 仅用于本地开发：允许 Tailscale 远程候选在 TLS 不可用时降级为明文 ws://。
	// 产品模式下不得启用——TLS 失败应禁用候选而非明文暴露 bearer token/业务内容（P1-4）。
	devInsecureWS := flag.Bool("dev-insecure-ws", envOr("CORDCODE_DEV_INSECURE_WS", "") != "", "DEV ONLY: allow plaintext ws:// Tailscale remote when TLS unavailable (fail-open). Product must leave unset.")
	includeTailscale := flag.Bool("pairing-include-tailscale", true, "Include detected Tailscale URL in pairing QR")
	includeRemote := flag.Bool("pairing-include-remote", true, "Include manual remote URL in pairing QR")
	relayEnabled := flag.Bool("relay-enabled", true, "Enable encrypted relay path")

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

	handlers := NewHandlersWithContext(ctx)
	handlers.SetRelayEnabled(*relayEnabled)
	handlers.SetDataDir(*dataDirPath)
	if *dataDirPath != "" {
		handlers.SetTranscriptIndexBaseDir(*dataDirPath + string(filepath.Separator) + "transcript-index")
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
		"claude":   "claudecode",
		"opencode": "opencode",
		"codex":    "codex",
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
			codexBackend:      *codexBackend,
			codexAppServerURL: *codexAppServerURL,
			pinStore:          pinStore,
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

		if sub, ok := agent.(core.EventSubscriber); ok && shouldStartPassiveSubscription(id, *codexBackend, *codexAppServerURL, *ocBaseURL) {
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
	if *managementHost != "" && *managementToken != "" {
		displayName := loadOrCreateDisplayName(dataDir)

		mgmtCfg := ManagementConfig{
			Handlers:         handlers,
			Token:            *managementToken,
			DataDir:          dataDir,
			PairingStore:     func() PairingSessionStore { s := NewMemoryPairingStore(); globalPairingStore = s; return s }(),
			DeviceStore:      func() TrustedDeviceStore { s := newTrustedDeviceStore(dataDir); globalDeviceStore = s; return s }(),
			BridgeID:         bridgeID,
			DisplayName:      displayName,
			LocalURL:         advertisedLocalURL,
			LocalURLs:        advertisedLocalURLs,
			TailscaleURL:     tailscaleURL,
			RemoteURL:        *remoteURL,
			IncludeTailscale: *includeTailscale,
			IncludeRemote:    *includeRemote,
			RelayEndpoint:    *relayEndpoint,
			RelayRouteID:     *relayRouteID,
			RelayCredential:  *relayCredential,
			RelayConfigured:  relayConfigured,
			RelayEnabled:     *relayEnabled,
			RelayIdentity:    relayIdentity,
			Agents:           handlers.agents,
			CodexBackendMode: *codexBackend,
			DetectionCfg: &AgentDetectionConfig{
				OpenCodeURL:       *ocBaseURL,
				OpenCodeUser:      *ocUser,
				OpenCodePass:      *ocPass,
				CodexAppServerURL: *codexAppServerURL,
			},
			TLSPin: tlsPin,
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

	server := NewServer(handlers)
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
		conn.SendJSON(ack)
		slog.Info("relay-bridge-client: hello_ack sent via relay", "ok", ack.Ok, "device", hello.Client.DeviceID)
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
	if err := WriteReadyFrame(*port, driverList, managementURL, *dataDirPath); err != nil {
		slog.Error("go-bridge: ready frame 持久化失败，fail-fast 退出", "error", err)
		WriteErrorFrame(RuntimeErrorBootstrapPersistFailed, err.Error())
		os.Exit(1)
	}

	slog.Info("go-bridge: listening", "addr", addr, "drivers", *drivers)
	if err := httpServer.Serve(listener); err != nil && ctx.Err() == nil {
		slog.Error("go-bridge: server error", "error", err)
		os.Exit(1)
	}
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
			if eventName == "todos_updated" || eventName == "turn_started" || eventName == "turn_completed" || eventName == "error" || eventName == "text_delta" {
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
				} else if eventName == "turn_completed" || eventName == "error" {
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

			h.mu.Lock()
			h.seq++
			seq := h.seq
			h.mu.Unlock()

			msg := EventMessage{
				Type:      "event",
				SessionID: ev.SessionID,
				BackendID: backendID,
				Event:     eventName,
				Data:      data,
				Seq:       seq,
			}
			h.deltaBatcher.Send(BroadcastEvent{
				BackendID: backendID,
				SessionID: ev.SessionID,
				Message:   msg,
			})
		}
		slog.Info("go-bridge: passive subscription ended, reconnecting", "backend", backendID)

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func shouldStartPassiveSubscription(backendID, codexBackendMode, codexAppServerURL, openCodeURL string) bool {
	if backendID == "codex" {
		return normalizeCodexBackend(codexBackendMode) == "app_server" && strings.TrimSpace(codexAppServerURL) != ""
	}
	if backendID == "opencode" {
		// 无 URL 时 OpenCode backend 处于 not_configured，不启动 SSE 订阅
		//（Subscribe 也会拒绝空 URL，这里提前避免无意义重连退避）。
		return strings.TrimSpace(openCodeURL) != ""
	}
	return true
}

type agentOptionsConfig struct {
	workDir           string
	openCodeURL       string
	openCodeUser      string
	openCodePass      string
	codexBackend      string
	codexAppServerURL string
	pinStore          *pinstore.Store
}

func buildAgentOptions(id string, cfg agentOptionsConfig) map[string]any {
	opts := map[string]any{
		"work_dir":      cfg.workDir,
		"mode":          "bypassPermissions",
		"opencode_url":  cfg.openCodeURL,
		"opencode_user": cfg.openCodeUser,
		"opencode_pass": cfg.openCodePass,
		"pin_store":     cfg.pinStore,
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
