import AppKit
import Combine
import CryptoKit
import Darwin
import Foundation
import Security

// MARK: - 通知名称

extension Notification.Name {
    /// 远程 URL 配置变更时触发，RuntimeManager 应更新配置并重启
    static let remoteURLDidChange = Notification.Name("remoteURLDidChange")
}

// MARK: - Bridge 状态


/// Bridge runtime 生命周期状态
enum BridgeStatus: String {
    case idle           // 未启动
    case starting       // 正在启动
    case ready          // 运行中，有可用 agent
    case readyNoAgents  // 运行中，无 agent
    case stopped        // 用户主动停止
    case crashed        // 崩溃
    case sleeping       // Mac 休眠中
}

// MARK: - Runtime 启动配置

struct RuntimeConfig {
    let executablePath: String
    let port: Int
    let dataDir: String
    let logDir: String
    let drivers: [String]
    let workDir: String
    let codexBackend: String
    let codexAppServerURL: String
    var opencodeUser: String
    var opencodePass: String
    let logFilePath: String
    let cliSearchPath: [String]
    var remoteURL: String
    var includeTailscaleInPairing: Bool
    var includeRemoteInPairing: Bool
    var relayEndpoint: String
    var relayRouteID: String
    var relayCredential: String
    var relayServiceAddress: String

    init(
        executablePath: String,
        port: Int = 8777,
        dataDir: String,
        logDir: String,
        drivers: [String] = ["claude", "opencode", "codex"],
        workDir: String = FileManager.default.homeDirectoryForCurrentUser.path,
        codexBackend: String = "app_server",
        codexAppServerURL: String = "ws://127.0.0.1:4141",
        opencodeUser: String = "",
        opencodePass: String = "",
        logFilePath: String = "/tmp/go-bridge.log",
        cliSearchPath: [String] = Self.defaultCLISearchPath(),
        remoteURL: String = "",
        includeTailscaleInPairing: Bool = true,
        includeRemoteInPairing: Bool = true,
        relayEndpoint: String = "",
        relayRouteID: String = "",
        relayCredential: String = "",
        relayServiceAddress: String = ""
    ) {
        self.executablePath = NSString(string: executablePath).expandingTildeInPath
        self.port = port
        self.dataDir = NSString(string: dataDir).expandingTildeInPath
        self.logDir = NSString(string: logDir).expandingTildeInPath
        self.drivers = drivers
        self.workDir = workDir
        self.codexBackend = codexBackend
        self.codexAppServerURL = codexAppServerURL
        self.opencodeUser = opencodeUser
        self.opencodePass = opencodePass
        self.logFilePath = NSString(string: logFilePath).expandingTildeInPath
        self.cliSearchPath = cliSearchPath.map { NSString(string: $0).expandingTildeInPath }
        self.remoteURL = remoteURL
        self.includeTailscaleInPairing = includeTailscaleInPairing
        self.includeRemoteInPairing = includeRemoteInPairing
        self.relayEndpoint = relayEndpoint
        self.relayRouteID = relayRouteID
        self.relayCredential = relayCredential
        self.relayServiceAddress = relayServiceAddress
    }

    private static func defaultCLISearchPath() -> [String] {
        [
            "~/.bun/bin",
            "~/.local/bin",
            "~/.cargo/bin",
            "/opt/homebrew/bin",
            "/opt/homebrew/sbin",
            "/usr/local/bin",
            "/usr/local/sbin",
            "/usr/bin",
            "/bin",
            "/usr/sbin",
            "/sbin",
            "/Applications/Codex.app/Contents/Resources",
        ]
    }
}

// MARK: - RuntimeManager

/// 管理 go-bridge 子进程生命周期。
///
/// MacBridge 拥有 go-bridge 进程：启动、监控、崩了重启、退出时终止。
@MainActor
class RuntimeManager: ObservableObject {
    @Published private(set) var status: BridgeStatus = .idle
    @Published private(set) var statusText: String = ""
    @Published private(set) var managementURL: String?
    @Published private(set) var managementToken: String?
    @Published private(set) var lastError: String?
    @Published private(set) var agents: [AgentInfo] = []

    private var apiClient: ManagementAPIClient?
    private var bridgeProcess: Process?
    /// 当前进程的标准输出 pipe，用于在重启时先清理 readabilityHandler
    private var currentStdoutPipe: Pipe?
    private var monitorTask: Task<Void, Never>?
    private var userStopped = false
    /// 最近一次 launchBridgeProcess 启动的 PID，用于区分旧进程退出和新进程退出
    private var lastLaunchedPID: Int32 = 0
    private var crashCount = 0
    private let maxCrashRetries = 3
    private var logFileHandle: FileHandle?
    /// Mac 休眠期间为 true，阻止 crash 重试
    private var isSleeping = false
    private var managementFailureCount = 0
    /// 最近一次状态变化时间，用于判断“卡在 .starting”多久
    private var lastStatusChangeAt: Date?
    /// 最近一次进程启动时间，用于定时兜底重启
    private var lastLaunchedAt: Date?
    /// 自动重启是否已在排队（防止重启过程中被重复触发）
    private var autoRestartPending = false
    /// 卡在 .starting 多久后判定为“卡住”并自动重启
    private let stuckRestartThreshold: TimeInterval = 60
    /// 连续卡住自动重启的次数上限，超过则停止自动重启，避免死循环空转
    private let maxStuckRestarts = 5
    private var stuckRestartCount = 0

    var config: RuntimeConfig
    private var sleepObserver: Any?
    private var wakeObserver: Any?

    init(config: RuntimeConfig) {
        self.config = config
        observeSleepWake()
        startMonitoring()
    }

    deinit {
        monitorTask?.cancel()
        logFileHandle?.closeFile()
        if let obs = sleepObserver { NSWorkspace.shared.notificationCenter.removeObserver(obs) }
        if let obs = wakeObserver { NSWorkspace.shared.notificationCenter.removeObserver(obs) }
    }

    // MARK: - 公共 API

    func start() {
        userStopped = false
        crashCount = 0
        stuckRestartCount = 0
        autoRestartPending = false
        setStatus(.starting, "正在启动 Bridge...")
        launchBridgeProcess()
    }

    func stop() {
        userStopped = true
        terminateProcess()
        resetRuntimeState()
        setStatus(.stopped, "CCCode Bridge 已停止")
    }

    func restart() {
        // 保持 userStopped=true，让 terminationHandler 忽略 terminateProcess 导致的进程退出。
        // 在 launchBridgeProcess 之前才重置为 false。
        userStopped = true
        crashCount = 0
        terminateProcess()
        setStatus(.starting, "正在重启 Bridge...")
        // 短暂延迟让端口释放、旧 pipe 清理、terminationHandler Task 执行完毕
        Task {
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            self.userStopped = false
            self.launchBridgeProcess()
        }
    }

    /// 更新 OpenCode 认证凭据（下次启动时生效）
    func updateOpenCodeCredentials(user: String, pass: String) {
        config.opencodeUser = user
        config.opencodePass = pass
    }

    /// App 退出时调用：终止子进程。
    func shutdownForExit() {
        userStopped = true
        monitorTask?.cancel()
        monitorTask = nil
        terminateProcess(waitForExit: true)
    }

    // MARK: - 进程管理

    private func launchBridgeProcess() {
        // 确保目录存在
        try? FileManager.default.createDirectory(atPath: config.dataDir, withIntermediateDirectories: true)
        try? FileManager.default.createDirectory(atPath: config.logDir, withIntermediateDirectories: true)
        try? FileManager.default.removeItem(atPath: config.dataDir + "/runtime.json")
        configureOpenCodeDesktopServerIfNeeded()
        guard prepareRuntimeOwnershipForLaunch() else { return }

        // management token
        let token = ensureManagementToken()

        let process = Process()
        process.executableURL = URL(fileURLWithPath: config.executablePath)

        var arguments = [
            "-port", "\(config.port)",
            "-drivers", config.drivers.joined(separator: ","),
            "-work-dir", config.workDir,
            "-codex-backend", config.codexBackend,
            "-management-host", "127.0.0.1",
            "-management-port", "0",
            "-data-dir", config.dataDir,
            "-log-dir", config.logDir,
        ]
        if !config.codexAppServerURL.isEmpty {
            arguments += ["-codex-app-server-url", config.codexAppServerURL]
        }
        if !config.remoteURL.isEmpty {
            arguments += ["-remote-url", config.remoteURL]
        }
        if !config.relayServiceAddress.isEmpty {
            arguments += ["-relay-service-addr", config.relayServiceAddress]
        }
        arguments += ["-pairing-include-tailscale=\(config.includeTailscaleInPairing ? "true" : "false")"]
        arguments += ["-pairing-include-remote=\(config.includeRemoteInPairing ? "true" : "false")"]
        process.arguments = arguments

        // 环境变量：OpenCode 凭据
        var environment = ProcessInfo.processInfo.environment
        environment["PATH"] = mergedCLIPath(existingPath: environment["PATH"])
        environment["CCCODE_MANAGEMENT_TOKEN"] = token
        if !config.opencodeUser.isEmpty {
            environment["OPENCODE_SERVER_USERNAME"] = config.opencodeUser
        } else {
            environment.removeValue(forKey: "OPENCODE_SERVER_USERNAME")
        }
        if !config.opencodePass.isEmpty {
            environment["OPENCODE_SERVER_PASSWORD"] = config.opencodePass
        } else {
            environment.removeValue(forKey: "OPENCODE_SERVER_PASSWORD")
        }
        if !config.relayEndpoint.isEmpty {
            environment["CCCODE_RELAY_ENDPOINT"] = config.relayEndpoint
        } else {
            environment.removeValue(forKey: "CCCODE_RELAY_ENDPOINT")
        }
        if !config.relayRouteID.isEmpty {
            environment["CCCODE_RELAY_ROUTE_ID"] = config.relayRouteID
        } else {
            environment.removeValue(forKey: "CCCODE_RELAY_ROUTE_ID")
        }
        if !config.relayCredential.isEmpty {
            environment["CCCODE_RELAY_CREDENTIAL"] = config.relayCredential
        } else {
            environment.removeValue(forKey: "CCCODE_RELAY_CREDENTIAL")
        }
        process.environment = environment

        // 日志输出
        let logPipe = Pipe()
        process.standardOutput = logPipe
        process.standardError = logPipe
        currentStdoutPipe = logPipe
        redirectPipeToFile(logPipe, path: config.logFilePath)

        // 进程退出回调 — 使用退出进程的真实 PID 区分旧进程和新进程
        process.terminationHandler = { [weak self] process in
            let exitedPID = process.processIdentifier
            Task { @MainActor in
                self?.handleProcessTermination(exitedPID: exitedPID)
            }
        }

        do {
            try process.run()
            bridgeProcess = process
            lastLaunchedPID = process.processIdentifier
            managementFailureCount = 0
            lastLaunchedAt = Date()
            autoRestartPending = false
            NSLog("[RuntimeManager] go-bridge 启动 PID=\(process.processIdentifier)")
        } catch {
            setStatus(.crashed, "启动失败: \(error.localizedDescription)")
            lastError = error.localizedDescription
        }
    }

    private func terminateProcess(waitForExit: Bool = false) {
        // 先清理 pipe 的 readabilityHandler，防止进程终止后 handler 在后台线程继续访问已关闭的 FileHandle
        if let pipe = currentStdoutPipe {
            pipe.fileHandleForReading.readabilityHandler = nil
        }
        currentStdoutPipe = nil

        guard let process = bridgeProcess, process.isRunning else {
            bridgeProcess = nil
            return
        }
        process.terminate()
        if waitForExit {
            let waitStart = Date()
            while process.isRunning && Date().timeIntervalSince(waitStart) < 2.0 {
                Thread.sleep(forTimeInterval: 0.05)
            }
            if process.isRunning {
                kill(process.processIdentifier, SIGKILL)
            }
        }
        bridgeProcess = nil
        NSLog("[RuntimeManager] 已请求终止 go-bridge")
    }

    private func handleProcessTermination(exitedPID: Int32) {
        // 如果退出的 PID 不是最近一次启动的 PID，说明是旧进程的延迟退出通知，忽略
        guard exitedPID == lastLaunchedPID else {
            NSLog("[RuntimeManager] 忽略旧进程 PID=\(exitedPID) 退出（当前 PID=\(lastLaunchedPID)）")
            return
        }
        // 休眠期间进程被杀是正常的，不计数不重试
        guard !isSleeping else {
            NSLog("[RuntimeManager] 休眠期间进程终止，忽略")
            return
        }
        guard !userStopped else { return }

        let wasRunning = status == .ready || status == .readyNoAgents
        resetRuntimeState()

        crashCount += 1

        if crashCount >= maxCrashRetries {
            setStatus(.crashed, "CCCode Bridge 连续意外退出，已停止自动重启")
            lastError = "请检查日志: \(config.logFilePath)"
            return
        }

        let statusText = wasRunning ? "CCCode Bridge 意外退出，正在重启..." : "CCCode Bridge 启动失败，正在重试..."
        NSLog("[RuntimeManager] go-bridge 意外退出，第 \(crashCount) 次重启")
        setStatus(.starting, statusText)

        // 延迟重启，避免端口冲突
        Task {
            try? await Task.sleep(nanoseconds: UInt64(Double(crashCount) * 1_000_000_000))
            guard !self.userStopped else { return }
            self.launchBridgeProcess()
        }
    }

    // MARK: - 监控

    private func startMonitoring() {
        guard monitorTask == nil else { return }
        monitorTask = Task { [weak self] in
            while let self, !Task.isCancelled {
                if self.bridgeProcess?.isRunning == true {
                    await self.pollManagementAPI()
                    self.evaluateAutoRestart()
                }
                try? await Task.sleep(nanoseconds: 3_000_000_000)
            }
        }
    }

    /// 自动重启判定：卡在 starting 超阈值 → 重启；定时兜底 → 到点重启。
    /// 设置随时可改（默认开启、间隔 2 小时）；每 3 秒实时读取，无需重启 App。
    private func evaluateAutoRestart() {
        guard !autoRestartPending else { return }
        // 正在重启/已停止/已崩溃/休眠：交由其他路径处理
        guard status != .stopped, status != .crashed, status != .sleeping, status != .idle else { return }
        let now = Date()

        // 1) 卡在 starting：正常启动几秒就 ready，长时间停在 starting 说明卡住了
        if status == .starting, let changedAt = lastStatusChangeAt {
            let stuck = now.timeIntervalSince(changedAt)
            if stuck >= stuckRestartThreshold {
                triggerAutoRestart(reason: "Bridge 卡在启动状态 \(Int(stuck))s，自动重启")
                return
            }
        }

        // 2) 定时兜底重启：只在工作正常时计时（starting/异常不计入定时窗口）
        let enabled = UserDefaults.standard.object(forKey: "autoRestartEnabled") as? Bool ?? true
        guard enabled else { return }
        let minutes = UserDefaults.standard.object(forKey: "autoRestartIntervalMinutes") as? Int ?? 120
        // 下限 5 分钟，防止误配成极小值导致频繁重启
        let interval = max(5, minutes) * 60
        if status == .ready || status == .readyNoAgents,
           let launchedAt = lastLaunchedAt,
           now.timeIntervalSince(launchedAt) >= TimeInterval(interval) {
            triggerAutoRestart(reason: "到达定时重启周期 \(minutes) 分钟，兜底重启")
        }
    }

    private func triggerAutoRestart(reason: String) {
        // 连续卡住自动重启仍未恢复，停止自动重启，避免死循环空转
        if stuckRestartCount >= maxStuckRestarts {
            setStatus(.crashed, "Bridge 多次自动重启仍未恢复，已停止自动重启。请检查日志或手动重启。")
            lastError = "连续卡住自动重启 \(maxStuckRestarts) 次仍未恢复: \(config.logFilePath)"
            NSLog("[RuntimeManager] \(reason) 被跳过：已达自动重启上限 \(maxStuckRestarts) 次")
            return
        }
        stuckRestartCount += 1
        autoRestartPending = true
        NSLog("[RuntimeManager] \(reason)（第 \(stuckRestartCount)/\(maxStuckRestarts) 次）")
        restart()
    }

    private func pollManagementAPI() async {
        let dataDir = config.dataDir
        let expectedPID = bridgeProcess?.processIdentifier
        let bootstrap = await Task.detached(priority: .utility) {
            (
                frame: Self.readRuntimeJSON(in: dataDir),
                token: Self.readPersistedToken(in: dataDir)
            )
        }.value

        guard let frame = bootstrap.frame,
              frame.port == config.port,
              expectedPID == nil || frame.pid == Int(expectedPID ?? 0),
              let mgmtURL = frame.managementUrl, !mgmtURL.isEmpty,
              let token = bootstrap.token, !token.isEmpty else {
            return
        }

        if apiClient == nil || managementURL != mgmtURL {
            guard let client = try? ManagementAPIClient(baseURL: mgmtURL, token: token) else {
                return
            }
            managementURL = mgmtURL
            managementToken = token
            apiClient = client
        }

        guard let client = apiClient else { return }

        do {
            let resp = try await client.getStatus()
            applyManagementStatus(resp.status)
            managementFailureCount = 0
            let latestAgents = (try? await client.getAgents()) ?? []
            if agents != latestAgents {
                agents = latestAgents
            }
        } catch {
            managementFailureCount += 1
            if managementFailureCount >= 3, status == .ready || status == .readyNoAgents {
                setStatus(.starting, "Bridge 管理接口暂不可用，正在重新检测...")
            }
        }
    }

    // MARK: - 状态辅助

    private func resetRuntimeState() {
        managementURL = nil
        managementToken = nil
        apiClient = nil
        lastError = nil
        agents = []
    }

    private func setStatus(_ s: BridgeStatus, _ text: String) {
        guard status != s || statusText != text else { return }
        if status != s {
            lastStatusChangeAt = Date()
            // 离开 starting 状态且进入 ready/readyNoAgents，说明已自愈，清零计数
            if s == .ready || s == .readyNoAgents {
                stuckRestartCount = 0
            }
        }
        status = s
        statusText = text
    }

    private func applyManagementStatus(_ rawStatus: String) {
        switch rawStatus {
        case "ready":
            crashCount = 0
            setStatus(.ready, "CCCode Bridge 运行中")
        case "ready_no_agents":
            crashCount = 0
            setStatus(.readyNoAgents, "请配置至少一个 AI 工具")
        default:
            setStatus(.starting, "CCCode Bridge 正在启动...")
        }
    }

    private struct PortOwner {
        let pid: Int32
        let command: String
        let executablePath: String?
    }

    private func prepareRuntimeOwnershipForLaunch() -> Bool {
        disableLegacyGoBridgeLaunchAgents()

        guard let owner = Self.portOwner(for: config.port) else {
            return true
        }

        if canTakeOverPortOwner(owner) {
            NSLog("[RuntimeManager] 清理旧 Bridge runtime PID=\(owner.pid) path=\(owner.executablePath ?? owner.command)")
            kill(owner.pid, SIGTERM)
            if waitUntilPortIsFree(config.port, timeout: 2.0) {
                return true
            }
            kill(owner.pid, SIGKILL)
            return waitUntilPortIsFree(config.port, timeout: 1.0)
        }

        let ownerText = owner.executablePath ?? owner.command
        lastError = "端口 \(config.port) 已被占用：\(ownerText)"
        setStatus(.crashed, "端口 \(config.port) 已被其他进程占用")
        return false
    }

    private func canTakeOverPortOwner(_ owner: PortOwner) -> Bool {
        let executable = owner.executablePath ?? ""
        let command = owner.command
        let currentRuntime = config.executablePath

        if executable == currentRuntime {
            return true
        }
        if executable.hasSuffix("/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime") {
            return true
        }
        if executable.contains("/go-bridge/go-bridge") || command.contains("/go-bridge/go-bridge") {
            return true
        }
        return false
    }

    private func waitUntilPortIsFree(_ port: Int, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if Self.portOwner(for: port) == nil {
                return true
            }
            Thread.sleep(forTimeInterval: 0.1)
        }
        return Self.portOwner(for: port) == nil
    }

    private func disableLegacyGoBridgeLaunchAgents() {
        let launchAgentsDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents")
        guard let entries = try? FileManager.default.contentsOfDirectory(
            at: launchAgentsDir,
            includingPropertiesForKeys: nil
        ) else {
            return
        }

        for url in entries where url.pathExtension == "plist" {
            guard let content = try? String(contentsOf: url),
                  content.contains("/go-bridge/go-bridge"),
                  content.contains("8777") else {
                continue
            }
            let path = url.path
            _ = Self.runCommand("/bin/launchctl", ["bootout", "gui/\(getuid())", path])
            var disabledPath = path + ".disabled-by-cccodebridge"
            if FileManager.default.fileExists(atPath: disabledPath) {
                disabledPath += "-\(Int(Date().timeIntervalSince1970))"
            }
            try? FileManager.default.moveItem(atPath: path, toPath: disabledPath)
            NSLog("[RuntimeManager] 已禁用旧 go-bridge LaunchAgent: \(path)")
        }
    }

    private func ensureManagementToken() -> String {
        if let existing = Self.readPersistedToken(in: config.dataDir), !existing.isEmpty {
            managementToken = existing
            return existing
        }
        let token = Self.generateToken()
        let tokenPath = config.dataDir + "/management-token"
        try? token.write(toFile: tokenPath, atomically: true, encoding: .utf8)
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tokenPath)
        managementToken = token
        return token
    }

    private func configureOpenCodeDesktopServerIfNeeded() {
        guard config.drivers.contains("opencode"),
              !config.opencodeUser.isEmpty,
              !config.opencodePass.isEmpty else {
            return
        }

        let url = "http://127.0.0.1:64667"
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
        guard let desktopDir = appSupport?.appendingPathComponent("ai.opencode.desktop") else { return }

        Self.configureOpenCodeDesktopSettings(
            desktopDir: desktopDir,
            serverURL: url,
            username: config.opencodeUser,
            password: config.opencodePass
        )
    }

    private nonisolated static func readRuntimeJSON(in dataDir: String) -> (managementUrl: String?, port: Int?, pid: Int?)? {
        let path = dataDir + "/runtime.json"
        guard let data = FileManager.default.contents(atPath: path),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return (json["managementUrl"] as? String, json["port"] as? Int, json["pid"] as? Int)
    }

    private nonisolated static func readPersistedToken(in dataDir: String) -> String? {
        let path = dataDir + "/management-token"
        guard let data = FileManager.default.contents(atPath: path) else { return nil }
        return String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private nonisolated static func configureOpenCodeDesktopSettings(
        desktopDir: URL,
        serverURL: String,
        username: String,
        password: String
    ) {
        let globalPath = desktopDir.appendingPathComponent("opencode.global.dat")
        let settingsPath = desktopDir.appendingPathComponent("opencode.settings")

        var root = readJSONObject(globalPath) ?? [:]
        var server = (root["server"] as? String).flatMap { data in
            parseJSONObject(from: data)
        } ?? [:]
        let connection: [String: Any] = [
            "type": "http",
            "http": [
                "url": serverURL,
                "username": username,
                "password": password,
            ],
        ]
        let existing = server["list"] as? [Any] ?? []
        server["list"] = [connection] + existing.filter { item in
            if let value = item as? String {
                return value != serverURL
            }
            if let dict = item as? [String: Any],
               let http = dict["http"] as? [String: Any],
               let url = http["url"] as? String {
                return url != serverURL
            }
            return true
        }
        server["currentSidecarUrl"] = serverURL
        if let data = try? JSONSerialization.data(withJSONObject: server),
           let encoded = String(data: data, encoding: .utf8) {
            root["server"] = encoded
            writeJSONObject(root, to: globalPath)
        }

        var settings = readJSONObject(settingsPath) ?? [:]
        settings["defaultServerUrl"] = serverURL
        writeJSONObject(settings, to: settingsPath)
    }

    private nonisolated static func readJSONObject(_ url: URL) -> [String: Any]? {
        guard let data = try? Data(contentsOf: url),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return json
    }

    private nonisolated static func parseJSONObject(from string: String) -> [String: Any]? {
        guard let data = string.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return json
    }

    private nonisolated static func writeJSONObject(_ object: [String: Any], to url: URL) {
        guard let data = try? JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys]) else {
            return
        }
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: .atomic)
    }

    // MARK: - 休眠 / 唤醒

    private func observeSleepWake() {
        let nc = NSWorkspace.shared.notificationCenter
        sleepObserver = nc.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                self.isSleeping = true
                NSLog("[RuntimeManager] Mac 即将休眠")
                if self.status == .ready || self.status == .readyNoAgents {
                    self.setStatus(.sleeping, "Mac 休眠中，Bridge 服务已暂停")
                }
            }
        }
        wakeObserver = nc.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                NSLog("[RuntimeManager] Mac 唤醒，isSleeping=\(self.isSleeping) status=\(self.status)")
                self.isSleeping = false
                self.setStatus(.starting, "Mac 已唤醒，正在恢复 Bridge 服务...")
                // 等 2 秒让网络等系统服务恢复
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                if self.bridgeProcess?.isRunning == true {
                    await self.pollManagementAPI()
                } else {
                    // start() 重置 crashCount，确保唤醒后一定能重启
                    self.start()
                }
            }
        }
    }

    // MARK: - 工具方法

    /// 将 pipe 的输出重定向到日志文件。
    /// 用 try-catch 保护所有 FileHandle 操作，防止进程终止后的竞态 crash。
    private func redirectPipeToFile(_ pipe: Pipe, path: String) {
        // 先关闭旧的日志文件 handle
        logFileHandle?.closeFile()
        logFileHandle = nil

        FileManager.default.createFile(atPath: path, contents: nil)
        guard let handle = try? FileHandle(forWritingTo: URL(fileURLWithPath: path)) else { return }
        logFileHandle = handle
        pipe.fileHandleForReading.readabilityHandler = { fh in
            let data = fh.availableData
            if data.isEmpty {
                fh.readabilityHandler = nil
                try? handle.close()
                return
            }
            // try-catch 防止 handle 已关闭时 seekToEndOfFile 抛异常
            do {
                try handle.seekToEnd()
                handle.write(data)
            } catch {
                // handle 已关闭或 pipe 已断开，静默忽略
            }
        }
    }

    private func mergedCLIPath(existingPath: String?) -> String {
        var seen = Set<String>()
        var paths: [String] = []

        for path in config.cliSearchPath + (existingPath ?? "").split(separator: ":").map(String.init) {
            guard !path.isEmpty, !seen.contains(path) else { continue }
            seen.insert(path)
            paths.append(path)
        }

        return paths.joined(separator: ":")
    }

    private static func generateToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined(separator: "")
    }

    private nonisolated static func portOwner(for port: Int) -> PortOwner? {
        let output = runCommand("/usr/sbin/lsof", [
            "-nP",
            "-iTCP:\(port)",
            "-sTCP:LISTEN",
            "-Fpc",
        ])
        var pid: Int32?
        var command = ""

        for line in output.split(separator: "\n").map(String.init) {
            if line.hasPrefix("p"), let value = Int32(line.dropFirst()) {
                pid = value
            } else if line.hasPrefix("c") {
                command = String(line.dropFirst())
            }
        }

        guard let pid else { return nil }
        return PortOwner(
            pid: pid,
            command: command,
            executablePath: executablePath(forPID: pid)
        )
    }

    private nonisolated static func executablePath(forPID pid: Int32) -> String? {
        let output = runCommand("/usr/sbin/lsof", ["-p", "\(pid)", "-Fn"])
        var previousWasTextFile = false
        for line in output.split(separator: "\n").map(String.init) {
            if line == "ftxt" {
                previousWasTextFile = true
                continue
            }
            if previousWasTextFile, line.hasPrefix("n") {
                return String(line.dropFirst())
            }
            previousWasTextFile = false
        }
        return nil
    }

    private nonisolated static func runCommand(_ launchPath: String, _ arguments: [String]) -> String {
        let process = Process()
        let pipe = Pipe()
        process.executableURL = URL(fileURLWithPath: launchPath)
        process.arguments = arguments
        process.standardOutput = pipe
        process.standardError = Pipe()
        do {
            try process.run()
            process.waitUntilExit()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            return String(data: data, encoding: .utf8) ?? ""
        } catch {
            return ""
        }
    }
}

enum RelayRouteCredentialStore {
    private static let service = "org.openagi.cccode.macbridge.relay"
    private static let account = "route-credential"

    static func load() -> String {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data,
              let credential = String(data: data, encoding: .utf8) else {
            return ""
        }
        return credential
    }

    static func save(_ credential: String) throws {
        let baseQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        if credential.isEmpty {
            SecItemDelete(baseQuery as CFDictionary)
            return
        }
        let value = Data(credential.utf8)
        let updateStatus = SecItemUpdate(
            baseQuery as CFDictionary,
            [kSecValueData as String: value] as CFDictionary
        )
        if updateStatus == errSecSuccess { return }
        guard updateStatus == errSecItemNotFound else {
            throw NSError(
                domain: NSOSStatusErrorDomain,
                code: Int(updateStatus),
                userInfo: [NSLocalizedDescriptionKey: "无法更新 Relay route credential"]
            )
        }
        var item = baseQuery
        item[kSecValueData as String] = value
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw NSError(
                domain: NSOSStatusErrorDomain,
                code: Int(status),
                userInfo: [NSLocalizedDescriptionKey: "无法保存 Relay route credential"]
            )
        }
    }
}

struct OfficialRelayConfiguration {
    private static let customEndpointKey = "customRelayEndpoint"

    static var bundledEndpoint: String {
        (Bundle.main.object(forInfoDictionaryKey: "CCCODEOfficialRelayEndpoint") as? String)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }

    static var customEndpoint: String {
        UserDefaults.standard.string(forKey: customEndpointKey)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }

    static var endpoint: String {
        customEndpoint.isEmpty ? bundledEndpoint : customEndpoint
    }

    static var isAvailable: Bool {
        !endpoint.isEmpty
    }

    static var isUsingCustomEndpoint: Bool {
        !customEndpoint.isEmpty
    }

    let endpoint: String
    let routeID: String
    let credential: String
}

enum OfficialRelayProvisioningError: LocalizedError {
    case invalidEndpoint
    case unavailable
    case registrationFailed

    var errorDescription: String? {
        switch self {
        case .invalidEndpoint:
            return "官方 Relay 地址无效。"
        case .unavailable:
            return "此构建未配置官方 Relay。"
        case .registrationFailed:
            return "官方 Relay 暂时无法启用。"
        }
    }
}

actor OfficialRelayProvisioner {
    static let shared = OfficialRelayProvisioner()

    func ensureRoute() async throws -> OfficialRelayConfiguration {
        let endpoint = OfficialRelayConfiguration.endpoint
        guard !endpoint.isEmpty else {
            throw OfficialRelayProvisioningError.unavailable
        }
        let defaults = UserDefaults.standard
        let savedEndpoint = defaults.string(forKey: "relayEndpoint") ?? ""
        let savedRouteID = defaults.string(forKey: "relayRouteID") ?? ""
        let savedCredential = RelayRouteCredentialStore.load()
        if savedEndpoint == endpoint,
           !savedRouteID.isEmpty,
           !savedCredential.isEmpty {
            return OfficialRelayConfiguration(
                endpoint: endpoint,
                routeID: savedRouteID,
                credential: savedCredential
            )
        }

        let activation = try RelayActivationIdentityStore.loadOrCreate()
        let bridgeAuth = savedCredential.isEmpty ? try RelayActivationIdentityStore.newCredential() : savedCredential
        try RelayRouteCredentialStore.save(bridgeAuth)
        guard var components = URLComponents(string: endpoint) else {
            throw OfficialRelayProvisioningError.invalidEndpoint
        }
        components.scheme = "https"
        guard let URL = components.url?.appendingPathComponent("v1/activations/routes") else {
            throw OfficialRelayProvisioningError.invalidEndpoint
        }

        let timestamp = Int64(Date().timeIntervalSince1970)
        let nonce = UUID().uuidString.lowercased()
        let publicKey = activation.privateKey.publicKey.rawRepresentation.base64EncodedString()
        let payload = [
            "cccode-relay/activation/v1",
            activation.installID,
            publicKey,
            bridgeAuth,
            String(timestamp),
            nonce,
        ].joined(separator: "\n")
        let signature = try activation.privateKey.signature(for: Data(payload.utf8)).base64EncodedString()
        struct ActivationRequest: Encodable {
            let installId: String
            let publicKey: String
            let bridgeAuth: String
            let timestamp: Int64
            let nonce: String
            let signature: String
        }
        var request = URLRequest(url: URL)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(ActivationRequest(
            installId: activation.installID,
            publicKey: publicKey,
            bridgeAuth: bridgeAuth,
            timestamp: timestamp,
            nonce: nonce,
            signature: signature
        ))
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse,
              httpResponse.statusCode == 201 else {
            throw OfficialRelayProvisioningError.registrationFailed
        }
        struct ProvisionResponse: Decodable {
            let routeId: String
        }
        let provision = try JSONDecoder().decode(ProvisionResponse.self, from: data)
        defaults.set(endpoint, forKey: "relayEndpoint")
        defaults.set(provision.routeId, forKey: "relayRouteID")
        return OfficialRelayConfiguration(
            endpoint: endpoint,
            routeID: provision.routeId,
            credential: bridgeAuth
        )
    }
}

private struct RelayActivationIdentity {
    let installID: String
    let privateKey: Curve25519.Signing.PrivateKey
}

private enum RelayActivationIdentityStore {
    private static let service = "org.openagi.cccode.macbridge.relay"
    private static let installIDAccount = "activation-install-id"
    private static let signingKeyAccount = "activation-signing-key"

    static func loadOrCreate() throws -> RelayActivationIdentity {
        let installID = load(account: installIDAccount) ?? "install_\(UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased())"
        let privateKey: Curve25519.Signing.PrivateKey
        if let encoded = load(account: signingKeyAccount),
           let raw = Data(base64Encoded: encoded) {
            privateKey = try Curve25519.Signing.PrivateKey(rawRepresentation: raw)
        } else {
            privateKey = Curve25519.Signing.PrivateKey()
        }
        try save(installID, account: installIDAccount)
        try save(privateKey.rawRepresentation.base64EncodedString(), account: signingKeyAccount)
        return RelayActivationIdentity(installID: installID, privateKey: privateKey)
    }

    static func newCredential() throws -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            throw OfficialRelayProvisioningError.registrationFailed
        }
        return Data(bytes).base64EncodedString()
    }

    private static func load(account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    private static func save(_ value: String, account: String) throws {
        let baseQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        let data = Data(value.utf8)
        let updateStatus = SecItemUpdate(baseQuery as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if updateStatus == errSecSuccess { return }
        guard updateStatus == errSecItemNotFound else {
            throw NSError(domain: NSOSStatusErrorDomain, code: Int(updateStatus))
        }
        var item = baseQuery
        item[kSecValueData as String] = data
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw NSError(domain: NSOSStatusErrorDomain, code: Int(status))
        }
    }
}
