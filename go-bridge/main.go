package gobridge

import (
	"context"
	"crypto/tls"
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
	_ "github.com/openAgi2/cccode-macbridge/agent/claudecode"
	_ "github.com/openAgi2/cccode-macbridge/agent/codex"
	_ "github.com/openAgi2/cccode-macbridge/agent/opencode"

	"github.com/openAgi2/cccode-macbridge/core"
)

func Main() {
	port := flag.Int("port", 8777, "WebSocket listen port")
	drivers := flag.String("drivers", "claude,opencode,codex", "Comma-separated agent list")
	workDir := flag.String("work-dir", "", "Working directory for agents (default: cwd)")
	showVersion := flag.Bool("version", false, "Print runtime version and exit")
	codexBackend := flag.String("codex-backend", envOr("GO_BRIDGE_CODEX_BACKEND", "exec"), "Codex backend mode: exec or app_server")
	codexAppServerURL := flag.String("codex-app-server-url", envOr("GO_BRIDGE_CODEX_APP_SERVER_URL", ""), "Optional Codex app-server listen URL")

	// opencode direct HTTP API
	ocBaseURL := flag.String("opencode-url", envOr("OPENCODE_BASE_URL", "http://localhost:64667"), "OpenCode HTTP API URL")
	ocUser := flag.String("opencode-user", envOr("OPENCODE_SERVER_USERNAME", ""), "OpenCode auth username")
	ocPass := flag.String("opencode-pass", envOr("OPENCODE_SERVER_PASSWORD", ""), "OpenCode auth password")

	// 管理 API（Mac App product 模式使用，开发模式不启用）
	managementHost := flag.String("management-host", "", "Management API host (product mode: 127.0.0.1)")
	managementPort := flag.Int("management-port", 0, "Management API port (0 = disabled)")
	managementToken := flag.String("management-token", envOr("CCCODE_MANAGEMENT_TOKEN", ""), "Management API auth token")
	dataDirPath := flag.String("data-dir", "", "Data directory for runtime state")
	logDirPath := flag.String("log-dir", "", "Log directory")
	remoteURL := flag.String("remote-url", "", "外部可达的 Bridge WebSocket URL（如 wss://my-tailscale:8777/bridge）")
	tlsPort := flag.Int("tls-port", 8778, "TLS listen port for wss:// remote access (0 = disabled)")
	includeTailscale := flag.Bool("pairing-include-tailscale", true, "Include detected Tailscale URL in pairing QR")
	includeRemote := flag.Bool("pairing-include-remote", true, "Include manual remote URL in pairing QR")

	// Relay 加密通道配置（首版：通过 flags 或环境变量注入，后续由 MacBridge runtime config 驱动）
	relayEndpoint := flag.String("relay-endpoint", envOr("CCCODE_RELAY_ENDPOINT", ""), "Relay 服务端点（wss://relay.example.com）")
	relayRouteID := flag.String("relay-route-id", envOr("CCCODE_RELAY_ROUTE_ID", ""), "Relay 路由 ID（由 relay 服务分配）")
	relayCredential := flag.String("relay-credential", envOr("CCCODE_RELAY_CREDENTIAL", ""), "Relay 认证凭据（opaque，不复用 device token）")
	relayServiceAddr := flag.String("relay-service-addr", "", "Local-only in-process relay test listener (for example 127.0.0.1:8780)")

	flag.Parse()
	if *showVersion {
		fmt.Println(runtimeVersionString())
		return
	}
	clearOpenCodeServerAuthEnv()

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

	handlers := NewHandlers()
	if *dataDirPath != "" {
		handlers.SetTranscriptIndexBaseDir(*dataDirPath + string(filepath.Separator) + "transcript-index")
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
		})

		agent, err := core.CreateAgent(agentName, agentOpts)
		if err != nil {
			slog.Error("go-bridge: failed to create agent", "agent", agentName, "error", err)
			continue
		}
		if err := applyProviderSeed(agent, agentName, *workDir); err != nil {
			slog.Warn("go-bridge: failed to load provider seed", "agent", agentName, "error", err)
		}

		handlers.RegisterAgent(id, agent)
		if id == "codex" {
			handlers.SetCodexBackendMode(*codexBackend)
		}
		slog.Info("go-bridge: agent registered", "backendId", id, "agent", agentName, "workDir", *workDir)

		if sub, ok := agent.(core.EventSubscriber); ok {
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

	// 自动检测 Tailscale IP 作为独立远程候选，不覆盖手动配置的 FRP/VPS URL。
	// 使用 wss:// 和自签名证书，避免 iOS ATS 拦截 ws:// 明文连接。
	var tlsCert *tls.Certificate
	var tailscaleURL string
	if tsIP := detectTailscaleIP(); tsIP != "" {
		if *tlsPort > 0 {
			cert, err := generateSelfSignedCert(tsIP)
			if err != nil {
				slog.Warn("go-bridge: 自签名证书生成失败，Tailscale 远程候选将使用 ws://", "error", err)
				tailscaleURL = fmt.Sprintf("ws://%s:%d/bridge", tsIP, *port)
			} else {
				tlsCert = cert
				tailscaleURL = fmt.Sprintf("wss://%s:%d/bridge", tsIP, *tlsPort)
				slog.Info("go-bridge: 自动检测到 Tailscale IP，加入远程候选 (wss:// + 自签名证书)", "ip", tsIP, "tlsPort", *tlsPort)
			}
		} else {
			tailscaleURL = fmt.Sprintf("ws://%s:%d/bridge", tsIP, *port)
			slog.Info("go-bridge: 自动检测到 Tailscale IP，加入远程候选", "ip", tsIP)
		}
	}

	// 管理 API 启动（仅 product 模式：management-host 和 management-token 都非空时启用）
	var managementURL string
	var mgmtSrv *ManagementServer
	var relayIdentity *RelayCryptoIdentity
	relayConfigured := *relayEndpoint != "" && *relayRouteID != "" && *relayCredential != ""
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
			TailscaleURL:     tailscaleURL,
			RemoteURL:        *remoteURL,
			IncludeTailscale: *includeTailscale,
			IncludeRemote:    *includeRemote,
			RelayEndpoint:    *relayEndpoint,
			RelayRouteID:     *relayRouteID,
			RelayCredential:  *relayCredential,
			RelayConfigured:  relayConfigured,
			RelayIdentity:    relayIdentity,
			Agents:           handlers.agents,
			CodexBackendMode: *codexBackend,
			DetectionCfg: &AgentDetectionConfig{
				OpenCodeURL:       *ocBaseURL,
				OpenCodeUser:      *ocUser,
				OpenCodePass:      *ocPass,
				CodexAppServerURL: *codexAppServerURL,
			},
		}
		mgmtSrv = NewManagementServer(mgmtCfg)

		actualPort, err := mgmtSrv.Start(*managementHost, *managementPort)
		if err != nil {
			slog.Error("go-bridge: 管理 API 启动失败", "error", err)
		} else {
			managementURL = fmt.Sprintf("http://%s:%d", *managementHost, actualPort)
			slog.Info("go-bridge: 管理 API 就绪", "url", managementURL)
		}
	}

	server := NewServer(handlers)
	serverDisplayName := "CCCode Bridge"
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
	httpServer := &http.Server{Addr: addr}
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
		relayHTTPServer = &http.Server{Handler: NewRelayService(sharedRelayHub)}
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
	shutdown := func() {
		cancel()
		closedConnCount := server.CloseAllConnections("bridge shutting down")
		if relayBridgeClient != nil {
			relayBridgeClient.Close()
		}
		slog.Info("go-bridge: closed active websocket connections before shutdown", "count", closedConnCount)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
		if relayHTTPServer != nil {
			_ = relayHTTPServer.Shutdown(shutdownCtx)
		}
		if tlsServer != nil {
			_ = tlsServer.Shutdown(shutdownCtx)
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
	if *dataDirPath != "" && *managementToken != "" {
		if err := os.WriteFile(*dataDirPath+"/management-token", []byte(*managementToken), 0600); err != nil {
			slog.Error("go-bridge: management-token 写入失败", "error", err)
		}
	}

	// 输出就绪帧（供 Mac App 解析）
	driverList := make([]string, 0, len(strings.Split(*drivers, ",")))
	for _, d := range strings.Split(*drivers, ",") {
		if trimmed := strings.TrimSpace(d); trimmed != "" {
			driverList = append(driverList, trimmed)
		}
	}
	WriteReadyFrame(*port, driverList, managementURL, *dataDirPath)

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
			h.broadcaster.Send(BroadcastEvent{
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

type agentOptionsConfig struct {
	workDir           string
	openCodeURL       string
	openCodeUser      string
	openCodePass      string
	codexBackend      string
	codexAppServerURL string
}

func buildAgentOptions(id string, cfg agentOptionsConfig) map[string]any {
	opts := map[string]any{
		"work_dir":      cfg.workDir,
		"mode":          "bypassPermissions",
		"opencode_url":  cfg.openCodeURL,
		"opencode_user": cfg.openCodeUser,
		"opencode_pass": cfg.openCodePass,
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
