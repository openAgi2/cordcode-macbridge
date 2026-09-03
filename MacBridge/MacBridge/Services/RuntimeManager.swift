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
    static let sessionListLimitDidChange = Notification.Name("sessionListLimitDidChange")
    /// 「同一局域网时优先直连」连接偏好变更（Relay-first + opt-in LAN）。RuntimeManager 应
    /// 原子更新 RuntimeConfig.preferLocalNetwork 并只 restart 一次。SSV2:control-plane，不进入 timeline。
    static let preferLocalNetworkDidChange = Notification.Name("preferLocalNetworkDidChange")
    /// P2-3：键盘命令请求打开「帮助与诊断」工作表。
    static let openDiagnosticsRequest = Notification.Name("openDiagnosticsRequest")
    /// P2-3：键盘命令请求打开「连接状态」工作表。
    static let openConnectionStatusRequest = Notification.Name("openConnectionStatusRequest")
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

enum RuntimeSupervisorState: String, Codable, Sendable {
    case idle, pending, quiescing, restarting, restartFailed
}

struct RuntimeSupervisorObservation: Codable, Equatable, Sendable {
    let supervisorState: RuntimeSupervisorState
}

private enum RuntimeRestartTrigger: Sendable {
    case manual, configuration, automatic
}

private enum RuntimeRecoveryError: LocalizedError {
    case operationIDGenerationFailed
    case managementUnavailable
    case legacyAutomaticRecoveryDisabled
    case runtimeIdentityMismatch
    case quiesceRejected(String)
    case commitRejected(String)
    case quiesceTimedOut

    var errorDescription: String? {
        switch self {
        case .operationIDGenerationFailed: return "无法生成安全的 restart operation id"
        case .managementUnavailable: return "runtime 正在运行，但安全管理接口尚不可用"
        case .legacyAutomaticRecoveryDisabled: return "旧版 runtime 不支持安全自动恢复"
        case .runtimeIdentityMismatch: return "runtime identity 已变化，已拒绝迟到的恢复操作"
        case let .quiesceRejected(outcome): return "runtime quiesce 被拒绝：\(outcome)"
        case let .commitRejected(outcome): return "runtime shutdown commit 被拒绝：\(outcome)"
        case .quiesceTimedOut: return "runtime 仍有活跃任务，安全重启等待超时"
        }
    }
}

struct OpenCodeDesktopSyncResult: Equatable {
    let previousSidecarURL: String?
    let didSidecarChange: Bool
    let didProjectsMerge: Bool
}

struct RuntimeCommandResult: Equatable, Sendable {
    let terminationStatus: Int32
    let standardOutput: String
    let standardError: String
}

enum CodexDesktopSharedRuntimeSetupResult: Equatable, Sendable {
    case skipped
    case configured(daemonBinary: String)
    case failed(String)
}

/// Official Codex daemon seat independent of CordCode Link lifetime.
///
/// Codex Desktop re-runs `codex app-server daemon version` (2500ms spawn
/// timeout) on every `transport.connect()`, including reconnect. A missing
/// control socket usually fails immediately, not after 2.5s. Any failure sets
/// `kind=stdio` and `supportsReconnect()==false` for the rest of that Desktop
/// process. Desktop's first reconnect is ~1s after the websocket drops, so
/// this seat must restore the official daemon well inside that window.
/// MacBridge must not own the daemon process; the login job only loops
/// idempotent `daemon start` + attach env.
enum CodexSharedDaemonSeat {
    static let label = "org.openagi.cordcode.codex-app-server-daemon"
    static let recoverIntervalSeconds = "0.25"

    static func scriptContents(daemonBinary: String, codexHome: String) -> String {
        """
        #!/bin/bash
        set -uo pipefail
        export CODEX_HOME=\(shellEscape(codexHome))
        bin=\(shellEscape(daemonBinary))
        while true; do
          if [ -x "$bin" ]; then
            "$bin" app-server daemon start >/dev/null 2>&1 || true
            /bin/launchctl setenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 1 || true
          fi
          /bin/sleep \(recoverIntervalSeconds)
        done
        """
    }

    static func plistContents(scriptPath: String) -> String {
        """
        <?xml version="1.0" encoding="UTF-8"?>
        <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
        <plist version="1.0">
        <dict>
          <key>Label</key>
          <string>\(label)</string>
          <key>RunAtLoad</key>
          <true/>
          <key>KeepAlive</key>
          <true/>
          <key>ThrottleInterval</key>
          <integer>1</integer>
          <key>ProgramArguments</key>
          <array>
            <string>/bin/bash</string>
            <string>\(xmlEscape(scriptPath))</string>
          </array>
        </dict>
        </plist>
        """
    }

    private static func shellEscape(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    private static func xmlEscape(_ value: String) -> String {
        value
            .replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
            .replacingOccurrences(of: "\"", with: "&quot;")
    }
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
    /// 「同一局域网时优先直连」control-plane 连接策略。默认 false（Relay 底座）。
    /// 由 UserDefaults `preferLocalNetwork` 注入，随 argv `-prefer-local-network` 下发 go-bridge，
    /// 再随 hello_ack.bridge.connectionPolicy / RelayFirstResult / pairing_complete 下发 iOS。
    /// SSV2:不进入 timeline/projection。
    var preferLocalNetwork: Bool
    var relayServiceAddress: String
    var sessionListLimit: Int

    init(
        executablePath: String,
        port: Int = 8777,
        dataDir: String,
        logDir: String,
        // 老 opencode backend 已从驱动列表移除（owner 2026-08-19：与 opencode-web
        // 双订阅同一 serve，事件/投影双流互相覆盖，干扰 opencode-web 测试）。
        // agent/opencode 代码保留未删，回滚只需把 "opencode" 加回此列表。
        // 老 codex backend 已退役（owner 2026-08-25：codex-web 通过 owner 矩阵验收，
        // app_server 驱动不再启动）。agent/codex 代码保留未删，回滚 = 加回 "codex"。
        // codex-web backend 已退役（owner 2026-09-04）：从产品 lineup 移除，不再随
        // runtime 启动、不再出现在 Mac/iOS。agent/codex-web 代码保留未删；共享 daemon
        // seat（configureCodexDesktopSharedRuntime）以本列表为门，随之自动 skip，
        // codex-remote 走独立 Remote Control 链路不受影响。回滚 = 加回 "codex-web"。
        drivers: [String] = ["claude", "codex-remote", "grokbuild", "dsh-web", "opencode-web"],
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
        preferLocalNetwork: Bool = false,
        relayServiceAddress: String = "",
        sessionListLimit: Int = 100
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
        self.preferLocalNetwork = preferLocalNetwork
        self.relayServiceAddress = relayServiceAddress
        self.sessionListLimit = min(max(sessionListLimit, 1), 150)
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
            // Node-ecosystem global bin dirs (GUI launches do not inherit
            // the user's shell PATH; dsh-jsonrpc-agent installs live here).
            "~/Library/pnpm",      // pnpm global bin
            "~/.volta/bin",        // volta shims
            "~/.npm-global/bin",   // npm prefix=~/.npm-global convention
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
    @Published private(set) var supervisorState: RuntimeSupervisorState = .idle
    // codex-web 页「重启共享 Codex 服务」按钮的活动计数（来自 management status v1.activity，
    // 每 3s 轮询更新）。>0 表示有活跃 turn / 待处理交互，重启 daemon 会打断它们，按钮禁用。
    @Published private(set) var codexWebActiveTurns: UInt32 = 0
    @Published private(set) var codexWebPendingInteractions: UInt32 = 0
    // ~/.codex/config.toml 变更检测（cc-switch 切 provider 后提示重启共享 daemon 生效）。
    @Published private(set) var codexDaemonConfigChanged = false

    var supervisorObservation: RuntimeSupervisorObservation {
        RuntimeSupervisorObservation(supervisorState: supervisorState)
    }

    /// 当前 runtime identity 的数值 bridgeEpoch（Management v1 `runtimeIdentity.bridgeEpoch`，
    /// 与 topology snapshot 的 bridgeEpoch 同源：go-bridge main.go managementBridgeEpoch）。
    /// 尚未从 /internal/status 解出 v1 时返回 nil（此时快照 epoch 校验跳过）。
    var runtimeIdentityEpoch: UInt64? {
        latestManagementStatus?.v1?.runtimeIdentity.bridgeEpoch
    }

    private var apiClient: ManagementAPIClient?
    private var latestManagementStatus: ManagementStatus?
    private var openCodeManagedServer: OpenCodeManagedServer?
    private let processController = RuntimeProcessController()
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
        supervisorState = .restarting
        launchBridgeProcess()
    }

    func stop() {
        userStopped = true
        resetRuntimeState()
        supervisorState = .idle
        setStatus(.stopped, "CordCode Link 已停止")
        Task { [processController, port = config.port] in
            try? await processController.stopAndConfirmPort(gracefulWait: .zero, port: port)
        }
    }

    func restart() {
        scheduleRestart(trigger: .manual)
    }

    private func scheduleRestart(trigger: RuntimeRestartTrigger) {
        // 保持 userStopped=true，让 terminationHandler 忽略 terminateProcess 导致的进程退出。
        // 在 launchBridgeProcess 之前才重置为 false。
        userStopped = true
        crashCount = 0
        // T05: 自增 generation 并 cancel 任何挂起的延迟 restart Task。
        // 100ms 内连续多次 restart：旧 Task 被 cancel，只有最新 generation 的 Task 会真正 launch。
        launchGeneration += 1
        let gen = launchGeneration
        restartTask?.cancel()
        setStatus(.starting, "正在重启 Bridge...")
        supervisorState = .pending
        restartTask = Task { [weak self] in
            guard let self else { return }
            do {
                let gracefulWait = try await self.prepareCooperativeRestart(trigger: trigger, generation: gen)
                guard gen == self.launchGeneration, !Task.isCancelled else { return }
                self.supervisorState = .restarting
                try await self.processController.stopAndConfirmPort(gracefulWait: gracefulWait, port: self.config.port)
            } catch {
                guard gen == self.launchGeneration else { return }
                self.lastError = error.localizedDescription
                self.setStatus(.crashed, "Bridge 安全重启失败")
                self.supervisorState = .restartFailed
                return
            }
            try? await Task.sleep(nanoseconds: 1_500_000_000)
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
        scheduleRestart(trigger: .configuration)
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
        let semaphore = DispatchSemaphore(value: 0)
        let controller = processController
        let port = config.port
        Task.detached {
            try? await controller.stopAndConfirmPort(gracefulWait: .zero, port: port)
            semaphore.signal()
        }
        _ = semaphore.wait(timeout: .now() + 5)
    }

    // MARK: - 进程管理

    private func launchBridgeProcess() {
        disableLegacyGoBridgeLaunchAgents()
        // 确保目录存在
        // P2-8: data/log 目录创建后收紧为 0700（仅 owner 可访问）。
        try? FileManager.default.createDirectory(atPath: config.dataDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try? FileManager.default.createDirectory(atPath: config.logDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: config.dataDir)
        try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: config.logDir)
        try? FileManager.default.removeItem(atPath: config.dataDir + "/runtime.json")
        resolveManagedOpenCodeIfNeeded()
        configureOpenCodeDesktopServerIfNeeded()
        launchCount += 1
        // T06: 每次 launch 重置 epoch 锁定，新进程的 runtime.json 首次读到时重新锁定。
        currentBridgeEpoch = nil
        // management token
        let token = ensureManagementToken()

        let arguments = Self.processArguments(for: config)

        // 环境变量：OpenCode 凭据（password 走 env，绝不进 argv）
        let environment = Self.processEnvironment(
            for: config,
            managementToken: token,
            existingEnvironment: ProcessInfo.processInfo.environment
        )
        let spec = RuntimeProcessLaunchSpec(
            executablePath: config.executablePath,
            arguments: arguments,
            environment: environment,
            logFilePath: config.logFilePath,
            port: config.port
        )
        let generation = launchGeneration
        let drivers = config.drivers
        let homeDirectory = FileManager.default.homeDirectoryForCurrentUser.path
        Task { [weak self] in
            guard let self else { return }
            let codexDesktopSetup = await Task.detached(priority: .utility) {
                Self.configureCodexDesktopSharedRuntime(
                    drivers: drivers,
                    homeDirectory: homeDirectory,
                    fileExists: { FileManager.default.isExecutableFile(atPath: $0) },
                    run: { Self.runCommandResult($0, $1, environment: $2, timeout: 30) }
                )
            }.value
            guard generation == self.launchGeneration else { return }
            switch codexDesktopSetup {
            case .skipped:
                break
            case let .configured(daemonBinary):
                NSLog("[RuntimeManager] Codex Desktop 已配置共享 daemon: \(daemonBinary)")
            case let .failed(detail):
                // Bridge 仍启动以服务其他 backend；codex-web 自身会以
                // shared-daemon-required/not_configured 暴露同一真实失败，绝不另起 runtime。
                self.lastError = detail
                NSLog("[RuntimeManager] Codex Desktop 共享 daemon 配置失败: \(detail)")
            }
            do {
                let pid = try await self.processController.launch(spec)
                guard generation == self.launchGeneration else {
                    try? await self.processController.stopAndConfirmPort(gracefulWait: .zero, port: self.config.port)
                    return
                }
                self.lastLaunchedPID = pid
                self.managementFailureCount = 0
                self.lastLaunchedAt = Date()
                self.autoRestartPending = false
                self.supervisorState = .pending
                NSLog("[RuntimeManager] go-bridge 启动 PID=\(pid)")
            } catch {
                guard generation == self.launchGeneration else { return }
                // 「重新启动」点击时 runtime 常是健康活进程（controller 持有）——launch 只会
                // 抛 alreadyRunning。这不是启动失败：采纳该 PID，等 pollManagementAPI 从
                // runtime.json（Go 侧 15s 周期重写）恢复 ready。旧行为按失败展示后，
                // runtime.json 已被本 launch 删除、无人补写，UI 永久卡死（2026-08-24 真机）。
                if case RuntimeProcessControllerError.alreadyRunning(let runningPID) = error {
                    self.lastLaunchedPID = runningPID
                    self.managementFailureCount = 0
                    self.lastLaunchedAt = Date()
                    self.autoRestartPending = false
                    self.supervisorState = .pending
                    self.setStatus(.starting, "Bridge runtime 已在运行，正在确认管理接口...")
                    NSLog("[RuntimeManager] runtime 已在运行(PID=\(runningPID))，转等待管理接口就绪")
                    return
                }
                self.setStatus(.crashed, "启动失败: \(error.localizedDescription)")
                self.lastError = error.localizedDescription
                self.supervisorState = .restartFailed
            }
        }
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
                if let termination = await self.processController.consumeTermination() {
                    self.handleProcessTermination(exitedPID: termination.pid)
                }
                let process = await self.processController.snapshot()
                if process.isRunning {
                    await self.pollManagementAPI()
                    self.evaluateAutoRestart()
                }
                self.refreshCodexDaemonConfigMonitor()
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
        scheduleRestart(trigger: .automatic)
    }

    /// 返回 controller 在 signal escalation 前应等待 committed Go 自行退出的时间。
    /// v1 路径只有 commit 成功（或 status 已 committed）才返回；token 仅存在本方法局部变量中。
    private func prepareCooperativeRestart(
        trigger: RuntimeRestartTrigger,
        generation: Int
    ) async throws -> Duration {
        guard let client = apiClient else {
            let process = await processController.snapshot()
            // Startup configuration can arrive before runtime.json exposes the management API
            // (notably Relay credentials loaded from Keychain). The runtime is not ready and
            // cannot own sessions yet, so replacing that startup process is safe. Once startup
            // has completed, the management handshake remains mandatory.
            if process.isRunning, !Self.canReplacePreReadyRuntime(status: status) {
                throw RuntimeRecoveryError.managementUnavailable
            }
            return .zero
        }
        var status: ManagementStatus
        if let latestManagementStatus {
            status = latestManagementStatus
        } else {
            status = try await client.getStatus()
        }
        guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
        guard let initialV1 = status.v1 else {
            if trigger == .automatic { throw RuntimeRecoveryError.legacyAutomaticRecoveryDisabled }
            try await client.shutdown()
            return .seconds(2)
        }
        guard initialV1.runtimeIdentity.pid == lastLaunchedPID else {
            throw RuntimeRecoveryError.runtimeIdentityMismatch
        }
        guard var operationID = Self.generateOperationID() else {
            throw RuntimeRecoveryError.operationIDGenerationFailed
        }
        supervisorState = .quiescing
        let deadline = ContinuousClock.now.advanced(by: .seconds(30))

        while ContinuousClock.now < deadline {
            guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
            guard let v1 = status.v1, v1.runtimeIdentity.pid == lastLaunchedPID else {
                throw RuntimeRecoveryError.runtimeIdentityMismatch
            }
            switch v1.quiesce {
            case let .committed(existingOperation, _):
                if existingOperation == operationID { return .seconds(2) }
                // 另一 coordinator 已 commit；同一 PID 已不可逆 shuttingDown，只做收割。
                return .seconds(2)
            default:
                break
            }

            let request = ManagementQuiesceRequest(
                operationId: operationID,
                expectedRuntime: v1.runtimeIdentity,
                expectedHealthEpoch: v1.fileReadHealth.stateEpoch
            )
            let result: ManagementRuntimeResult
            do {
                result = try await client.quiesce(request)
            } catch {
                guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
                status = try await client.getStatus()
                continue
            }
            guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
            switch result {
            case let .safe(identity, healthEpoch, quiesceEpoch, token, leaseMillis, remaining):
                guard identity == v1.runtimeIdentity, healthEpoch == v1.fileReadHealth.stateEpoch else {
                    throw RuntimeRecoveryError.runtimeIdentityMismatch
                }
                // 2s commit HTTP + 500ms executor margin；不足时必须 abort，绝不 signal。
                guard leaseMillis >= 2_500, remaining >= 2_500 else {
                    let abort = ManagementCommitRequest(
                        operationId: operationID, expectedRuntime: identity,
                        expectedHealthEpoch: healthEpoch, quiesceEpoch: quiesceEpoch, token: token
                    )
                    _ = try? await client.abortQuiesce(abort)
                    guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
                    try? await Task.sleep(for: .milliseconds(250))
                    guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
                    status = try await client.getStatus()
                    guard let nextID = Self.generateOperationID() else {
                        throw RuntimeRecoveryError.operationIDGenerationFailed
                    }
                    operationID = nextID
                    continue
                }
                let commit = ManagementCommitRequest(
                    operationId: operationID, expectedRuntime: identity,
                    expectedHealthEpoch: healthEpoch, quiesceEpoch: quiesceEpoch, token: token
                )
                do {
                    let commitResult = try await client.commitQuiescedShutdown(commit)
                    guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
                    switch commitResult {
                    case let .committed(committedIdentity, committedHealth, committedEpoch):
                        guard committedIdentity == identity, committedHealth == healthEpoch,
                              committedEpoch == quiesceEpoch else {
                            throw RuntimeRecoveryError.runtimeIdentityMismatch
                        }
                        return .seconds(2)
                    case let .outcome(outcome):
                        if outcome == "lease_expired" {
                            status = try await client.getStatus()
                            guard let nextID = Self.generateOperationID() else {
                                throw RuntimeRecoveryError.operationIDGenerationFailed
                            }
                            operationID = nextID
                            continue
                        }
                        throw RuntimeRecoveryError.commitRejected(outcome)
                    default:
                        throw RuntimeRecoveryError.commitRejected("invalid_result")
                    }
                } catch let recovery as RuntimeRecoveryError {
                    throw recovery
                } catch is CancellationError {
                    throw CancellationError()
                } catch {
                    // commit response 丢失不等于未处理：先用 level-triggered status reconcile。
                    guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
                    status = try await client.getStatus()
                    if let reconcile = status.v1,
                       case let .committed(existingOperation, existingEpoch) = reconcile.quiesce,
                       existingOperation == operationID, existingEpoch == quiesceEpoch {
                        return .seconds(2)
                    }
                    continue
                }
            case let .deferred(_, _, retryAfterMillis):
                try? await Task.sleep(for: .milliseconds(Int64(retryAfterMillis)))
                guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
                status = try await client.getStatus()
            case let .outcome(outcome):
                switch outcome {
                case "operation_reused":
                    guard let nextID = Self.generateOperationID() else {
                        throw RuntimeRecoveryError.operationIDGenerationFailed
                    }
                    operationID = nextID
                case "already_committed":
                    return .seconds(2)
                case "already_quiescing":
                    try? await Task.sleep(for: .milliseconds(250))
                    guard generation == launchGeneration, !Task.isCancelled else { throw CancellationError() }
                    status = try await client.getStatus()
                default:
                    throw RuntimeRecoveryError.quiesceRejected(outcome)
                }
            default:
                throw RuntimeRecoveryError.quiesceRejected("invalid_result")
            }
        }
        throw RuntimeRecoveryError.quiesceTimedOut
    }

    nonisolated static func canReplacePreReadyRuntime(status: BridgeStatus) -> Bool {
        status == .starting
    }

    private nonisolated static func generateOperationID() -> String? {
        var bytes = [UInt8](repeating: 0, count: 16)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else { return nil }
        return bytes.map { String(format: "%02x", $0) }.joined()
    }

    private func pollManagementAPI() async {
        let dataDir = config.dataDir
        let expectedPID = lastLaunchedPID == 0 ? nil : lastLaunchedPID
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
            if let v1 = resp.v1, v1.runtimeIdentity.pid != pollPID {
                throw RuntimeRecoveryError.runtimeIdentityMismatch
            }
            latestManagementStatus = resp
            applyManagementStatus(resp.status)
            if let activity = resp.v1?.activity {
                // per-backend scoped：claude 等 backend 的活跃 turn 不再禁用 codex
                // 重启按钮（owner 2026-08-28）；旧 runtime 无 breakdown 时退回全局计数。
                codexWebActiveTurns = activity.codexScopedActiveTurns
                codexWebPendingInteractions = activity.codexScopedPendingInteractions
            }
            managementFailureCount = 0
            if resp.v1?.fileReadHealth.restartRecommended == true {
                triggerAutoRestart(reason: "文件读取运行时已退化，自动安全重启")
            }
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

    private func resetRuntimeState() {
        managementURL = nil
        managementToken = nil
        apiClient = nil
        latestManagementStatus = nil
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
            supervisorState = .idle
            setStatus(.ready, "CordCode Link 运行中")
        case "ready_no_agents":
            crashCount = 0
            supervisorState = .idle
            setStatus(.readyNoAgents, "请配置至少一个 AI 工具")
        default:
            setStatus(.starting, "CordCode Link 正在启动...")
        }
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

    /// 重启官方共享 Codex daemon 的 UI 结果。
    enum CodexSharedDaemonRestartOutcome: Equatable, Sendable {
        case restarted
        case rejectedActiveTurns(activeTurns: UInt32, pendingInteractions: UInt32)
        case failed(String)
    }

    /// 注入式静态结果（单元测试同构），与 `CodexSharedDaemonRestartOutcome` 不同层。
    enum CodexSharedDaemonRestartResult: Equatable, Sendable {
        case restarted
        case skipped
        case failed(String)
    }

    /// 重启官方共享 Codex daemon：cc-switch 改完 `~/.codex/config.toml` 后，进程内配置
    /// 副本不会自动更新，`daemon restart` 是唯一生效杠杆（官方原子命令，重启后重读
    /// config.toml）。seat 的幂等 `daemon start` 不会覆盖此动作。控制 socket 必须在等待
    /// 窗口内重建，与 codex-web lifecycle 的 30s 窗口一致。
    internal nonisolated static func restartCodexSharedDaemon(
        homeDirectory: String,
        fileExists: (String) -> Bool,
        run: (String, [String], [String: String]?) -> RuntimeCommandResult
    ) -> CodexSharedDaemonRestartResult {
        let codexHome = URL(fileURLWithPath: homeDirectory, isDirectory: true)
            .appendingPathComponent(".codex", isDirectory: true)
        let daemonBinary = codexHome
            .appendingPathComponent("packages/standalone/current/codex", isDirectory: false)
            .path
        guard fileExists(daemonBinary) else {
            return .failed(
                "codex-web 需要官方 managed standalone：缺少 \(daemonBinary)。" +
                "请使用官方 Codex installer 安装后重启 CordCode Link。"
            )
        }

        var daemonEnvironment = ProcessInfo.processInfo.environment
        daemonEnvironment["CODEX_HOME"] = codexHome.path
        let restart = run(daemonBinary, ["app-server", "daemon", "restart"], daemonEnvironment)
        guard restart.terminationStatus == 0 else {
            return .failed(commandFailureDetail(prefix: "官方 Codex daemon 重启失败", result: restart))
        }

        let socketPath = codexHome
            .appendingPathComponent("app-server-control/app-server-control.sock", isDirectory: false)
            .path
        let deadline = Date().addingTimeInterval(30)
        while Date() < deadline {
            if fileExists(socketPath) {
                return .restarted
            }
            Thread.sleep(forTimeInterval: 0.25)
        }
        return .failed("重启命令成功，但控制 socket 未在 30 秒内恢复（\(socketPath)）")
    }

    /// codex-web 页按钮动作：预检活跃 turn → 重启共享 daemon → 更新 config 基线。
    /// 预检避免打断进程内 loaded thread/writer；文件变更提示在重启成功后消除。
    /// 预检只看 codex/codex-web 的活跃 turn 与 pending 交互——重启的是共享 Codex
    /// daemon，其它 backend（如 claude）的任务不受影响（owner 2026-08-28）。
    func restartSharedCodexDaemon() async -> CodexSharedDaemonRestartOutcome {
        if let client = apiClient,
           let status = try? await client.getStatus(),
           let activity = status.v1?.activity,
           activity.codexScopedActiveTurns > 0 || activity.codexScopedPendingInteractions > 0 {
            return .rejectedActiveTurns(
                activeTurns: activity.codexScopedActiveTurns,
                pendingInteractions: activity.codexScopedPendingInteractions
            )
        }
        let homeDirectory = FileManager.default.homeDirectoryForCurrentUser.path
        let result = await Task.detached(priority: .utility) {
            Self.restartCodexSharedDaemon(
                homeDirectory: homeDirectory,
                fileExists: { FileManager.default.fileExists(atPath: $0) },
                run: { Self.runCommandResult($0, $1, environment: $2, timeout: 30) }
            )
        }.value
        switch result {
        case .restarted:
            markCodexDaemonConfigApplied()
            return .restarted
        case .skipped:
            return .failed("codex-web 后端未启用")
        case let .failed(detail):
            return .failed(detail)
        }
    }

    var codexDaemonConfigURL: URL? {
        guard config.drivers.contains("codex-web") else { return nil }
        return FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".codex/config.toml", isDirectory: false)
    }

    /// 检测 `~/.codex/config.toml` 是否在「上次重启生效」之后被改动（cc-switch 会重写它）。
    /// 首次运行不提示：记录当前 mtime 为基线，避免历史陈旧改动误报。
    func refreshCodexDaemonConfigMonitor() {
        guard let url = codexDaemonConfigURL,
              let mtime = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date else {
            codexDaemonConfigChanged = false
            return
        }
        let key = Self.codexDaemonConfigMtimeKey
        if UserDefaults.standard.object(forKey: key) == nil {
            UserDefaults.standard.set(mtime.timeIntervalSinceReferenceDate, forKey: key)
            codexDaemonConfigChanged = false
            return
        }
        let applied = UserDefaults.standard.double(forKey: key)
        codexDaemonConfigChanged = abs(mtime.timeIntervalSinceReferenceDate - applied) > 0.001
    }

    private func markCodexDaemonConfigApplied() {
        codexDaemonConfigChanged = false
        guard let url = codexDaemonConfigURL,
              let mtime = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date else {
            return
        }
        UserDefaults.standard.set(mtime.timeIntervalSinceReferenceDate, forKey: Self.codexDaemonConfigMtimeKey)
    }

    /// 配置基线持久化键：重启 daemon 成功（或首次观察）时写入 config.toml 的 mtime。
    private static let codexDaemonConfigMtimeKey = "codexDaemon.configMtimeApplied"

    /// 配置 Codex Desktop 与 codex-web 共用官方 local daemon。
    ///
    /// 顺序固定为 daemon ready → launchd env：Desktop 只会在下一次启动时继承环境，
    /// CordCode 不终止或重启已运行的官方客户端。standalone 缺失/命令失败直接返回真实错误；
    /// 调用方可以继续启动其他 backend，但不得为 codex-web 创建 managed-loopback fallback。
    internal nonisolated static func configureCodexDesktopSharedRuntime(
        drivers: [String],
        homeDirectory: String,
        fileExists: (String) -> Bool,
        run: (String, [String], [String: String]?) -> RuntimeCommandResult
    ) -> CodexDesktopSharedRuntimeSetupResult {
        guard drivers.contains("codex-web") else { return .skipped }

        let codexHome = URL(fileURLWithPath: homeDirectory, isDirectory: true)
            .appendingPathComponent(".codex", isDirectory: true)
        let daemonBinary = codexHome
            .appendingPathComponent("packages/standalone/current/codex", isDirectory: false)
            .path
        guard fileExists(daemonBinary) else {
            return .failed(
                "codex-web 需要官方 managed standalone：缺少 \(daemonBinary)。" +
                "请使用官方 Codex installer 安装后重启 CordCode Link；未启动替代 app-server。"
            )
        }

        // Desktop's attach probe is `daemon version` then
        // appServerVersion >= 0.141.0. Exact `codex --version` string
        // equality is stricter than that probe and currently fails closed
        // on patch skew (e.g. Desktop alpha.4.1 vs standalone alpha.4),
        // which skipped the login seat and left Desktop to lock into stdio.
        // Log skew; still start the official daemon and install the seat.
        // Desktop itself decides websocket vs stdio on the next connect.
        let desktopBundledBinary = "/Applications/ChatGPT.app/Contents/Resources/codex"
        if fileExists(desktopBundledBinary) {
            let desktopVersion = run(desktopBundledBinary, ["--version"], nil)
            let standaloneVersion = run(daemonBinary, ["--version"], nil)
            if desktopVersion.terminationStatus == 0, standaloneVersion.terminationStatus == 0 {
                let desktop = desktopVersion.standardOutput.trimmingCharacters(in: .whitespacesAndNewlines)
                let standalone = standaloneVersion.standardOutput.trimmingCharacters(in: .whitespacesAndNewlines)
                if !desktop.isEmpty, !standalone.isEmpty, desktop != standalone {
                    NSLog(
                        "[RuntimeManager] Codex Desktop CLI (\(desktop)) differs from managed standalone (\(standalone)); continuing official daemon seat. Desktop attach still depends on daemon version compatibility."
                    )
                }
            }
        }

        var daemonEnvironment = ProcessInfo.processInfo.environment
        daemonEnvironment["CODEX_HOME"] = codexHome.path
        let daemon = run(
            daemonBinary,
            ["app-server", "daemon", "start"],
            daemonEnvironment
        )
        guard daemon.terminationStatus == 0 else {
            return .failed(commandFailureDetail(
                prefix: "官方 Codex daemon 启动失败",
                result: daemon
            ))
        }

        let launchctl = run(
            "/bin/launchctl",
            ["setenv", "CODEX_APP_SERVER_USE_LOCAL_DAEMON", "1"],
            nil
        )
        guard launchctl.terminationStatus == 0 else {
            return .failed(commandFailureDetail(
                prefix: "无法为 Codex Desktop 配置官方 local-daemon 环境",
                result: launchctl
            ))
        }

        // Login seat: daemon outlives CordCode Link and is restored inside
        // Desktop's ~1s reconnect window. Only install against the real user
        // home so tests using a fake homeDirectory do not write plists.
        let liveHome = FileManager.default.homeDirectoryForCurrentUser.path
        if (homeDirectory as NSString).standardizingPath == (liveHome as NSString).standardizingPath {
            installCodexDaemonSeat(
                daemonBinary: daemonBinary,
                codexHome: codexHome.path,
                run: run
            )
        }
        return .configured(daemonBinary: daemonBinary)
    }

    /// Installs a user LaunchAgent that keeps official `daemon start`
    /// (idempotent) inside Desktop's reconnect window and refreshes the
    /// attach env. CordCode Link stop/quit must not bootout this job or
    /// stop the daemon.
    private nonisolated static func installCodexDaemonSeat(
        daemonBinary: String,
        codexHome: String,
        run: (String, [String], [String: String]?) -> RuntimeCommandResult
    ) {
        let fm = FileManager.default
        let supportBin = fm.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/CordCode Link/bin", isDirectory: true)
        let scriptURL = supportBin.appendingPathComponent("ensure-codex-shared-daemon.sh")
        let plistURL = fm.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents/\(CodexSharedDaemonSeat.label).plist")
        do {
            try fm.createDirectory(at: supportBin, withIntermediateDirectories: true)
            try CodexSharedDaemonSeat.scriptContents(daemonBinary: daemonBinary, codexHome: codexHome)
                .write(to: scriptURL, atomically: true, encoding: .utf8)
            try fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: scriptURL.path)
            try fm.createDirectory(at: plistURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            try CodexSharedDaemonSeat.plistContents(scriptPath: scriptURL.path)
                .write(to: plistURL, atomically: true, encoding: .utf8)
        } catch {
            NSLog("[RuntimeManager] 无法写入 Codex daemon seat: \(error.localizedDescription)")
            return
        }

        let uid = getuid()
        let domain = "gui/\(uid)"
        let target = "\(domain)/\(CodexSharedDaemonSeat.label)"
        _ = run("/bin/launchctl", ["bootout", target], nil)
        let bootstrap = run("/bin/launchctl", ["bootstrap", domain, plistURL.path], nil)
        if bootstrap.terminationStatus != 0 {
            NSLog("[RuntimeManager] Codex daemon seat bootstrap: \(bootstrap.standardError)")
        }
        _ = run("/bin/launchctl", ["kickstart", target], nil)
        NSLog("[RuntimeManager] Codex daemon seat installed: \(CodexSharedDaemonSeat.label)")
    }

    private nonisolated static func commandFailureDetail(
        prefix: String,
        result: RuntimeCommandResult
    ) -> String {
        let official = [result.standardError, result.standardOutput]
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .first(where: { !$0.isEmpty }) ?? "无命令输出"
        return "\(prefix)（exit \(result.terminationStatus)）：\(official)"
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
                if await self.processController.snapshot().isRunning {
                    await self.pollManagementAPI()
                } else {
                    // start() 重置 crashCount，确保唤醒后一定能重启
                    self.start()
                }
            }
        }
    }

    // MARK: - 工具方法

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
            "-session-list-limit", "\(config.sessionListLimit)",
        ]
        if !config.codexAppServerURL.isEmpty {
            arguments += ["-codex-app-server-url", config.codexAppServerURL]
        }
        // endpoint 不可解析（disabled / not_configured）时不写 URL，go-bridge 的 OpenCode
        // descriptor 返回 not_configured，绝不隐式 dial 64667。
        if !config.opencodeURL.isEmpty {
            arguments += ["-opencode-url", config.opencodeURL]
            // opencode-web 并存期是同一已解析 URL 的第二个客户端（设计
            // docs/2026-08-18-opencode-web-backend-design.md §4.1.6）；凭据仍走
            // env（OPENCODE_WEB_SERVER_*），不进 argv。
            arguments += ["-opencode-web-url", config.opencodeURL]
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
        // Relay-first + opt-in LAN:镜像 -relay-enabled 的 argv = 形式，默认 false。
        arguments += ["-prefer-local-network=\(config.preferLocalNetwork ? "true" : "false")"]
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
        // opencode-web 的凭据键与旧 backend 分开（go-bridge flag 的 env 默认
        // 读 OPENCODE_WEB_SERVER_*；不与旧 backend 共用即满足键名隔离）。
        if !config.opencodeUser.isEmpty {
            environment["OPENCODE_WEB_SERVER_USERNAME"] = config.opencodeUser
        } else {
            environment.removeValue(forKey: "OPENCODE_WEB_SERVER_USERNAME")
        }
        if !config.opencodePass.isEmpty {
            environment["OPENCODE_WEB_SERVER_PASSWORD"] = config.opencodePass
        } else {
            environment.removeValue(forKey: "OPENCODE_WEB_SERVER_PASSWORD")
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

    private nonisolated static func runCommandResult(
        _ launchPath: String,
        _ arguments: [String],
        environment: [String: String]?,
        timeout: TimeInterval
    ) -> RuntimeCommandResult {
        let process = Process()
        let stdout = Pipe()
        let stderr = Pipe()
        let terminated = DispatchSemaphore(value: 0)
        process.executableURL = URL(fileURLWithPath: launchPath)
        process.arguments = arguments
        process.environment = environment
        process.standardOutput = stdout
        process.standardError = stderr
        process.terminationHandler = { _ in terminated.signal() }
        do {
            try process.run()
        } catch {
            return RuntimeCommandResult(
                terminationStatus: -1,
                standardOutput: "",
                standardError: error.localizedDescription
            )
        }
        if terminated.wait(timeout: .now() + timeout) == .timedOut {
            process.terminate()
            _ = terminated.wait(timeout: .now() + 2)
            return RuntimeCommandResult(
                terminationStatus: -2,
                standardOutput: String(data: stdout.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "",
                standardError: "命令在 \(Int(timeout)) 秒内未完成"
            )
        }
        return RuntimeCommandResult(
            terminationStatus: process.terminationStatus,
            standardOutput: String(data: stdout.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "",
            standardError: String(data: stderr.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        )
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
