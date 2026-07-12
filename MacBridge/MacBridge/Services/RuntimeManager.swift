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

struct OpenCodeDesktopSyncResult: Equatable {
    let previousSidecarURL: String?
    let didSidecarChange: Bool
    let didProjectsMerge: Bool
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
    /// Resolved OpenCode endpoint URL (loopback, e.g. `http://127.0.0.1:<port>`).
    /// Empty when source is disabled / unresolved. Passed to go-bridge as `-opencode-url`.
    var opencodeURL: String
    /// Selected OpenCode server source. Drives Desktop config sync + diagnostics.
    var opencodeSource: OpenCodeServerSource
    let logFilePath: String
    let cliSearchPath: [String]
    var remoteURL: String
    var includeTailscaleInPairing: Bool
    var includeRemoteInPairing: Bool
    var relayEnabled: Bool
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
        codexAppServerURL: String = "",
        opencodeUser: String = "",
        opencodePass: String = "",
        opencodeURL: String = "",
        opencodeSource: OpenCodeServerSource = .disabled,
        logFilePath: String = "",
        cliSearchPath: [String] = Self.defaultCLISearchPath(),
        remoteURL: String = "",
        includeTailscaleInPairing: Bool = true,
        includeRemoteInPairing: Bool = true,
        relayEnabled: Bool = true,
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
        self.opencodeURL = opencodeURL
        self.opencodeSource = opencodeSource
        self.logFilePath = NSString(string: logFilePath).expandingTildeInPath
        self.cliSearchPath = cliSearchPath.map { NSString(string: $0).expandingTildeInPath }
        self.remoteURL = remoteURL
        self.includeTailscaleInPairing = includeTailscaleInPairing
        self.includeRemoteInPairing = includeRemoteInPairing
        self.relayEnabled = relayEnabled
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
            // Codex desktop was folded into ChatGPT.app. Its bundled CLI is
            // still named `codex`, but is no longer under /Applications/Codex.app.
            "/Applications/ChatGPT.app/Contents/Resources",
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
    private var openCodeManagedServer: OpenCodeManagedServer?
    private var bridgeProcess: Process?
    /// 当前进程的标准输出 pipe，用于在重启时先清理 readabilityHandler
    private var currentStdoutPipe: Pipe?
    private var monitorTask: Task<Void, Never>?
    private var userStopped = false
    /// 最近一次 launchBridgeProcess 启动的 PID，用于区分旧进程退出和新进程退出
    private var lastLaunchedPID: Int32 = 0
    private var crashCount = 0
    /// T05: 当前挂起的延迟 restart Task。新 restart 到来时先 cancel 旧 Task，保证 100ms 内
    /// 连续多次 restart 只启动一次进程（避免端口反复接管 / session 丢失 / ready frame 抖动）。
    private var restartTask: Task<Void, Never>?
    /// T05: 单调递增的 launch generation。每次 restart() 自增并捕获局部 gen；延迟 Task 醒来后
    /// 必须验证 gen == launchGeneration，否则直接 return（旧 Task 被新 restart 取代）。
    private var launchGeneration: Int = 0
    /// T05(test): launchBridgeProcess 真正进入执行的次数（跳过重入守卫的 return 之后才计）。
    /// 仅用于单元测试观测 restart 收敛行为，生产代码不读。
    internal var launchCount: Int = 0
    /// T06: 当前 launch 对应的 bridgeEpoch（从 runtime.json 首次读到时锁定）。后续轮询必须匹配
    /// 同一 epoch，防同 PID 生命周期外的旧 runtime.json 误判（PID 复用 / 残留文件）。
    private var currentBridgeEpoch: String?
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
        setStatus(.stopped, "CordCode Link 已停止")
    }

    func restart() {
        // 保持 userStopped=true，让 terminationHandler 忽略 terminateProcess 导致的进程退出。
        // 在 launchBridgeProcess 之前才重置为 false。
        userStopped = true
        crashCount = 0
        // T05: 自增 generation 并 cancel 任何挂起的延迟 restart Task。
        // 100ms 内连续多次 restart：旧 Task 被 cancel，只有最新 generation 的 Task 会真正 launch。
        launchGeneration += 1
        let gen = launchGeneration
        restartTask?.cancel()
        terminateProcess()
        setStatus(.starting, "正在重启 Bridge...")
        // 短暂延迟让端口释放、旧 pipe 清理、terminationHandler Task 执行完毕
        restartTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            guard let self else { return }
            // 醒来后必须同时校验 generation 与 cancel 状态：若期间又有新 restart 到来，
            // gen 已过期或 Task 被 cancel，直接 return 不 launch（收敛重复 restart）。
            guard gen == self.launchGeneration, !Task.isCancelled else { return }
            self.userStopped = false
            self.launchBridgeProcess()
        }
    }

    /// T05: 原子改 config 后只调度一次 restart。配置更新路径（remoteURL 变更、Relay provisioning
    /// 回调）应改用此方法合并所有字段变更，再只 restart 一次——避免连续双 restart 导致端口
    /// 反复接管与 ready frame 抖动。
    func applyConfigAndRestart(_ apply: (inout RuntimeConfig) -> Void) {
        apply(&config)
        restart()
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
        openCodeManagedServer?.stop()
        terminateProcess(waitForExit: true)
    }

    // MARK: - 进程管理

    private func launchBridgeProcess() {
        // T05: 重入守卫——若已有当前 generation 的进程正在 starting/running，直接 return，
        // 防止 cancel 竞态或重复调用导致同 generation 多次 launch。
        if bridgeProcess != nil && lastLaunchedPID != 0 {
            // 进程仍可能存活或正在退出；交由 terminationHandler 处理，不重复 launch。
            if let proc = bridgeProcess, proc.isRunning { return }
        }
        // 确保目录存在
        // P2-8: data/log 目录创建后收紧为 0700（仅 owner 可访问）。
        try? FileManager.default.createDirectory(atPath: config.dataDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try? FileManager.default.createDirectory(atPath: config.logDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: config.dataDir)
        try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: config.logDir)
        try? FileManager.default.removeItem(atPath: config.dataDir + "/runtime.json")
        resolveManagedOpenCodeIfNeeded()
        configureOpenCodeDesktopServerIfNeeded()
        guard prepareRuntimeOwnershipForLaunch() else { return }

        launchCount += 1
        // T06: 每次 launch 重置 epoch 锁定，新进程的 runtime.json 首次读到时重新锁定。
        currentBridgeEpoch = nil
        // management token
        let token = ensureManagementToken()

        let process = Process()
        process.executableURL = URL(fileURLWithPath: config.executablePath)

        let arguments = Self.processArguments(for: config)
        process.arguments = arguments

        // 环境变量：OpenCode 凭据（password 走 env，绝不进 argv）
        let environment = Self.processEnvironment(
            for: config,
            managementToken: token,
            existingEnvironment: ProcessInfo.processInfo.environment
        )
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
            setStatus(.crashed, "CordCode Link 连续意外退出，已停止自动重启")
            lastError = "请检查日志: \(config.logFilePath)"
            return
        }

        let statusText = wasRunning ? "CordCode Link 意外退出，正在重启..." : "CordCode Link 启动失败，正在重试..."
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

        // 必需字段就位后再判断 managementUrl：port/pid/token 都匹配但 managementUrl 缺失，
        // 属致命启动契约违例（P1-6）。不能静默卡在 starting 反复轮询，必须显式报错。
        // T06: 同时校验 bridgeEpoch——同一 launch 内 epoch 必须稳定，防止残留 runtime.json
        // （同 PID 复用 / 旧文件）被误判为新进程就绪。
        guard let frame = bootstrap.frame,
              frame.port == config.port,
              expectedPID == nil || frame.pid == Int(expectedPID ?? 0) else {
            return
        }
        if let epoch = frame.epoch {
            if currentBridgeEpoch == nil {
                currentBridgeEpoch = epoch
            } else if currentBridgeEpoch != epoch {
                // epoch 变化但未经过 launchBridgeProcess：拒绝，等下一轮重新锁定。
                return
            }
        }
        guard let token = bootstrap.token, !token.isEmpty else {
            return
        }
        guard let mgmtURL = frame.managementUrl, !mgmtURL.isEmpty else {
            // ready frame 已就位但 managementUrl 为空：Go runtime 本应在 product 模式 fail-fast
            // 而不发 ready。走到这里说明启动契约被破坏，判定为致命错误而非可恢复 starting。
            if status == .starting {
                lastError = "Bridge 启动契约错误：ready frame 缺少 managementUrl (runtime.management_url_missing)。请重启 Bridge。"
                setStatus(.crashed, "Bridge 启动失败：缺少管理接口")
            }
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

        // T07: status 决定 liveness——成功即立即更新状态；agents 刷新独立低优先级执行，
        // 不阻塞 status 轮询周期（pre-fix 串行 await getAgents 在 status 后，慢响应卡住整个 3s 轮询）。
        // 捕获本 polling 轮的 generation/PID，旧请求返回后不得覆盖新 runtime 状态。
        let pollGeneration = launchGeneration
        let pollPID = lastLaunchedPID
        do {
            let resp = try await client.getStatus()
            // T07: 旧请求返回后，若期间已 restart（generation 变或 PID 变），丢弃结果。
            guard pollGeneration == launchGeneration, pollPID == lastLaunchedPID else { return }
            applyManagementStatus(resp.status)
            managementFailureCount = 0
            // agents 刷新独立低优先级 task，不阻塞本轮 status 轮询与自动重启判定。
            Task.detached(priority: .utility) { [weak self, weak client] in
                guard let client else { return }
                let latestAgents = (try? await client.getAgents()) ?? []
                await MainActor.run {
                    guard let self else { return }
                    // 同样校验 generation/PID，防止旧 agents 覆盖新 runtime。
                    guard pollGeneration == self.launchGeneration, pollPID == self.lastLaunchedPID else { return }
                    if self.agents != latestAgents {
                        self.agents = latestAgents
                    }
                }
            }
        } catch {
            // T07: 失败也需校验 generation/PID，避免旧失败覆盖新 runtime 状态。
            guard pollGeneration == launchGeneration, pollPID == lastLaunchedPID else { return }
            managementFailureCount += 1
            if managementFailureCount >= 3, status == .ready || status == .readyNoAgents {
                setStatus(.starting, "Bridge 管理接口暂不可用，正在重新检测...")
            }
        }
    }

    // MARK: - 状态辅助

    /// rotateLogFileIfNeeded 按大小滚动日志：超过 maxLogBytes 时把当前文件移为 .1，
    /// 最多保留 maxLogGenerations 代（P2-8 日志治理）。
    private static let maxLogBytes: Int64 = 8 * 1024 * 1024   // 8 MiB
    private static let maxLogGenerations = 3

    private func rotateLogFileIfNeeded(path: String) {
        let fm = FileManager.default
        guard let attrs = try? fm.attributesOfItem(atPath: path),
              let size = attrs[.size] as? Int64 else { return }
        guard size >= Self.maxLogBytes else { return }
        let oldest = "\(path).\(Self.maxLogGenerations)"
        if fm.fileExists(atPath: oldest) {
            try? fm.removeItem(atPath: oldest)
        }
        for gen in stride(from: Self.maxLogGenerations - 1, through: 1, by: -1) {
            let from = "\(path).\(gen)"
            let to = "\(path).\(gen + 1)"
            if fm.fileExists(atPath: from) {
                try? fm.moveItem(atPath: from, toPath: to)
            }
        }
        if fm.fileExists(atPath: path) {
            try? fm.moveItem(atPath: path, toPath: "\(path).1")
        }
    }

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
            setStatus(.ready, "CordCode Link 运行中")
        case "ready_no_agents":
            crashCount = 0
            setStatus(.readyNoAgents, "请配置至少一个 AI 工具")
        default:
            setStatus(.starting, "CordCode Link 正在启动...")
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
        if executable.hasSuffix("/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime") {
            return true
        }
        if executable.contains("/go-bridge/go-bridge") || command.contains("/go-bridge/go-bridge") {
            return true
        }
        if executable.hasSuffix("/cordcode-bridge-runtime") || command.hasSuffix("/cordcode-bridge-runtime") || executable.contains("/cordcode-bridge-runtime") {
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
              !config.opencodeURL.isEmpty,
              !config.opencodeUser.isEmpty,
              !config.opencodePass.isEmpty else {
            return
        }

        // 使用 resolved endpoint URL（external_http 用户配置或 legacy_64667 的 127.0.0.1:64667），
        // 不再固定写 http://127.0.0.1:64667。
        let url = config.opencodeURL
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
        guard let desktopDir = appSupport?.appendingPathComponent("ai.opencode.desktop") else { return }

        _ = Self.configureOpenCodeDesktopSettings(
            desktopDir: desktopDir,
            serverURL: url,
            username: config.opencodeUser,
            password: config.opencodePass
        )
    }

    private func resolveManagedOpenCodeIfNeeded() {
        guard config.opencodeSource == .managedLocal else {
            openCodeManagedServer?.stop()
            openCodeManagedServer = nil
            return
        }
        if openCodeManagedServer == nil {
            openCodeManagedServer = OpenCodeManagedServer(
                dataDir: config.dataDir,
                logDir: config.logDir,
                cliSearchPath: config.cliSearchPath
            )
        }
        guard let endpoint = openCodeManagedServer?.ensureRunning(timeout: 5.0) else {
            config.opencodeURL = ""
            return
        }
        config.opencodeURL = endpoint.url
        config.opencodeUser = endpoint.username
        config.opencodePass = endpoint.password
        _ = openCodeManagedServer?.syncDesktopConfig(
            url: endpoint.url,
            username: endpoint.username,
            password: endpoint.password
        )
    }

    private nonisolated static func readRuntimeJSON(in dataDir: String) -> (managementUrl: String?, port: Int?, pid: Int?, epoch: String?)? {
        let path = dataDir + "/runtime.json"
        guard let data = FileManager.default.contents(atPath: path),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        // T06: 同时读取 bridgeEpoch 用于交叉校验，防同 PID 生命周期外的旧 runtime.json 误判。
        return (json["managementUrl"] as? String, json["port"] as? Int, json["pid"] as? Int, json["bridgeEpoch"] as? String)
    }

    private nonisolated static func readPersistedToken(in dataDir: String) -> String? {
        let path = dataDir + "/management-token"
        guard let data = FileManager.default.contents(atPath: path) else { return nil }
        return String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    @discardableResult
    internal nonisolated static func configureOpenCodeDesktopSettings(
        desktopDir: URL,
        serverURL: String,
        username: String,
        password: String
    ) -> OpenCodeDesktopSyncResult {
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
        let previousURL = server["currentSidecarUrl"] as? String
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
        let didProjectsMerge = migrateOpenCodeDesktopProjects(
            in: &server,
            to: serverURL,
            preferredSources: ["local", previousURL, "http://127.0.0.1:64667"].compactMap { $0 }
        )
        server["currentSidecarUrl"] = serverURL
        if let data = try? JSONSerialization.data(withJSONObject: server),
           let encoded = String(data: data, encoding: .utf8) {
            root["server"] = encoded
            writeJSONObject(root, to: globalPath)
        }

        var settings = readJSONObject(settingsPath) ?? [:]
        settings["defaultServerUrl"] = serverURL
        writeJSONObject(settings, to: settingsPath)
        return OpenCodeDesktopSyncResult(
            previousSidecarURL: previousURL,
            didSidecarChange: previousURL != serverURL,
            didProjectsMerge: didProjectsMerge
        )
    }

    private nonisolated static func migrateOpenCodeDesktopProjects(
        in server: inout [String: Any],
        to serverURL: String,
        preferredSources: [String]
    ) -> Bool {
        var projects = server["projects"] as? [String: Any] ?? [:]
        var mergedProjects = projects[serverURL] as? [[String: Any]] ?? []
        var seenWorktrees = Set(mergedProjects.compactMap { $0["worktree"] as? String })
        let initialCount = mergedProjects.count
        for source in preferredSources where source != serverURL {
            guard let sourceProjects = projects[source] as? [[String: Any]] else { continue }
            for project in sourceProjects {
                guard let worktree = project["worktree"] as? String, !worktree.isEmpty else { continue }
                if seenWorktrees.insert(worktree).inserted {
                    mergedProjects.append(project)
                }
            }
        }
        if !mergedProjects.isEmpty {
            projects[serverURL] = mergedProjects
        }
        server["projects"] = projects

        var lastProject = server["lastProject"] as? [String: Any] ?? [:]
        if !hasProjectPath(lastProject[serverURL]),
           let source = preferredSources.first(where: { $0 != serverURL && hasProjectPath(lastProject[$0]) }) {
            lastProject[serverURL] = lastProject[source]
        }
        server["lastProject"] = lastProject
        return mergedProjects.count > initialCount
    }

    private nonisolated static func hasProjectPath(_ value: Any?) -> Bool {
        guard let path = value as? String else { return false }
        return !path.isEmpty
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

        // P2-8: 拒绝 symlink 日志路径，防止被替换指向任意文件；并按大小滚动。
        rotateLogFileIfNeeded(path: path)
        if let attrs = try? FileManager.default.attributesOfItem(atPath: path),
           attrs[.type] as? FileAttributeType == .typeSymbolicLink {
            try? FileManager.default.removeItem(atPath: path)
        }
        FileManager.default.createFile(atPath: path, contents: nil, attributes: [.posixPermissions: 0o600])
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

    /// 构造 go-bridge 进程 argv（可测试，不启动进程）。URL 非 secret 可进 argv；
    /// password 不在此处出现（走 processEnvironment 的 env）。
    internal nonisolated static func processArguments(for config: RuntimeConfig) -> [String] {
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
        // endpoint 不可解析（disabled / not_configured）时不写 URL，go-bridge 的 OpenCode
        // descriptor 返回 not_configured，绝不隐式 dial 64667。
        if !config.opencodeURL.isEmpty {
            arguments += ["-opencode-url", config.opencodeURL]
        }
        if !config.remoteURL.isEmpty {
            arguments += ["-remote-url", config.remoteURL]
        }
        if !config.relayServiceAddress.isEmpty {
            arguments += ["-relay-service-addr", config.relayServiceAddress]
        }
        arguments += ["-pairing-include-tailscale=\(config.includeTailscaleInPairing ? "true" : "false")"]
        arguments += ["-pairing-include-remote=\(config.includeRemoteInPairing ? "true" : "false")"]
        arguments += ["-relay-enabled=\(config.relayEnabled ? "true" : "false")"]
        return arguments
    }

    /// 构造 go-bridge 进程环境（可测试）。password / 控制面 secret 走 env，不进 argv。
    internal nonisolated static func processEnvironment(
        for config: RuntimeConfig,
        managementToken: String,
        existingEnvironment: [String: String]
    ) -> [String: String] {
        var environment = existingEnvironment
        environment["PATH"] = mergedCLIPath(cliSearchPath: config.cliSearchPath, existingPath: environment["PATH"])
        environment["CORDCODE_MANAGEMENT_TOKEN"] = managementToken
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
            environment["CORDCODE_RELAY_ENDPOINT"] = config.relayEndpoint
        } else {
            environment.removeValue(forKey: "CORDCODE_RELAY_ENDPOINT")
        }
        if !config.relayRouteID.isEmpty {
            environment["CORDCODE_RELAY_ROUTE_ID"] = config.relayRouteID
        } else {
            environment.removeValue(forKey: "CORDCODE_RELAY_ROUTE_ID")
        }
        if !config.relayCredential.isEmpty {
            environment["CORDCODE_RELAY_CREDENTIAL"] = config.relayCredential
        } else {
            environment.removeValue(forKey: "CORDCODE_RELAY_CREDENTIAL")
        }
        return environment
    }

    private func mergedCLIPath(existingPath: String?) -> String {
        Self.mergedCLIPath(cliSearchPath: config.cliSearchPath, existingPath: existingPath)
    }

    private nonisolated static func mergedCLIPath(cliSearchPath: [String], existingPath: String?) -> String {
        var seen = Set<String>()
        var paths: [String] = []

        for path in cliSearchPath + (existingPath ?? "").split(separator: ":").map(String.init) {
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

enum RelaySecretFileStore {
    // T07: Relay 密钥的文件存储后端。与 OpenCode credentials.json 同目录、同样 0600 权限，
    // 用以替代钥匙串——后者会在 ad-hoc / 不稳定 Team 签名下每次重装都弹登录密码授权。
    static var directory: String {
        NSSearchPathForDirectoriesInDomains(.applicationSupportDirectory, .userDomainMask, true).first!
            + "/CordCode Link/relay-secrets"
    }

    /// 旧版本存在钥匙串的 service 名（迁移用）。
    private static let legacyKeychainService = "org.openagi.cccode.macbridge.relay"

    static func load(account: String) -> String? {
        let url = URL(fileURLWithPath: directory).appendingPathComponent(account)
        if let data = try? Data(contentsOf: url),
           let value = String(data: data, encoding: .utf8) {
            return value
        }
        // 文件不存在：尝试从旧版钥匙串一次性迁移（仅当旧条目还在）。
        // 迁移成功后删除钥匙串条目，避免后续再触发钥匙串授权弹窗。
        if let legacy = migrateFromKeychain(account: account) {
            return legacy
        }
        return nil
    }

    static func save(_ value: String, account: String) throws {
        let dir = directory
        try FileManager.default.createDirectory(
            atPath: dir,
            withIntermediateDirectories: true
        )
        let url = URL(fileURLWithPath: dir).appendingPathComponent(account)
        if value.isEmpty {
            try? FileManager.default.removeItem(at: url)
            return
        }
        try Data(value.utf8).write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }

    /// 一次性迁移：文件 account → 旧钥匙串 account 的映射不同，需显式传入 legacyKeychainAccount。
    /// 读到旧值则写入文件并删除钥匙串条目；读不到或失败均静默返回 nil（全新安装或已迁移）。
    private static func migrateFromKeychain(account: String) -> String? {
        guard let legacyAccount = legacyKeychainAccount(for: account) else { return nil }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: legacyKeychainService,
            kSecAttrAccount as String: legacyAccount,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data,
              let value = String(data: data, encoding: .utf8),
              !value.isEmpty else {
            return nil
        }
        // 迁移到文件成功后删除钥匙串条目，杜绝后续授权弹窗。
        let deleteQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: legacyKeychainService,
            kSecAttrAccount as String: legacyAccount,
        ]
        SecItemDelete(deleteQuery as CFDictionary)
        do {
            try save(value, account: account)
            NSLog("[RelaySecretFileStore] Migrated '\(account)' from Keychain to file store.")
            return value
        } catch {
            NSLog("[RelaySecretFileStore] Failed to persist migrated '\(account)': \(error.localizedDescription)")
            return nil
        }
    }

    /// 文件 account 名 → 旧钥匙串 account 名的映射。无映射表示该条目旧版未存钥匙串。
    private static func legacyKeychainAccount(for fileAccount: String) -> String? {
        switch fileAccount {
        case "relay-route-credential": return "route-credential"
        case "activation-install-id": return "activation-install-id"
        case "activation-signing-key": return "activation-signing-key"
        default: return nil
        }
    }
}

enum RelayRouteCredentialStore {
    // T07: Relay 凭据改用文件存储（0600），与 OpenCode credentials.json 同目录。
    // 钥匙串会因 ad-hoc / 不稳定 Team 签名在每次重装后弹窗要求授权登录密码，
    // 对"丢了可重新 provisioning"的 route credential 而言不必要，故迁出钥匙串。
    private static let fileName = "relay-route-credential"

    static func load() -> String {
        RelaySecretFileStore.load(account: fileName) ?? ""
    }

    static func save(_ credential: String) throws {
        try RelaySecretFileStore.save(credential, account: fileName)
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
            "cordcode-relay/activation/v1",
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
    // T07: 与 RelayRouteCredentialStore 一样改用文件存储，避免钥匙串授权弹窗。
    private static let installIDAccount = "activation-install-id"
    private static let signingKeyAccount = "activation-signing-key"

    static func loadOrCreate() throws -> RelayActivationIdentity {
        let installID = RelaySecretFileStore.load(account: installIDAccount)
            ?? "install_\(UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased())"
        let privateKey: Curve25519.Signing.PrivateKey
        if let encoded = RelaySecretFileStore.load(account: signingKeyAccount),
           let raw = Data(base64Encoded: encoded) {
            privateKey = try Curve25519.Signing.PrivateKey(rawRepresentation: raw)
        } else {
            privateKey = Curve25519.Signing.PrivateKey()
        }
        try RelaySecretFileStore.save(installID, account: installIDAccount)
        try RelaySecretFileStore.save(privateKey.rawRepresentation.base64EncodedString(), account: signingKeyAccount)
        return RelayActivationIdentity(installID: installID, privateKey: privateKey)
    }

    static func newCredential() throws -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            throw OfficialRelayProvisioningError.registrationFailed
        }
        return Data(bytes).base64EncodedString()
    }
}
