import XCTest
@testable import CordCodeLink

// 2026-10-06 方案 §5.2/§8：显式启动动作（startOpenCodeManagedServer）与
// OpenCodeManagedServer 串行执行域的不变量测试。注入式 stub（CLI/port/health/
// processFactory/desktopController/desktopConfigDir），不写用户真实
// ~/Library/Application Support/ai.opencode.desktop。
final class OpenCodeSeatActionTests: XCTestCase {

    private struct StubCLIResolver: OpenCodeCLIResolving {
        let path: String?
        func resolveOpenCodeCLI(searchPath: [String]) -> String? { path }
    }

    private struct StubPortProber: OpenCodePortProbing {
        let available: Set<Int>
        func isPortAvailable(_ port: Int) -> Bool { available.contains(port) }
    }

    private struct StubHealthProbe: OpenCodeManagedHealthProbing {
        let result: OpenCodeManagedHealthCheck?
        func check(url: String, username: String, password: String) -> OpenCodeManagedHealthCheck? {
            result
        }
    }

    private struct StubDesktopController: OpenCodeDesktopProcessControlling {
        func isOpenCodeDesktopRunning() -> Bool { false }
        func terminateOpenCodeDesktop(timeout: TimeInterval) -> Bool { true }
        func openOpenCodeDesktop() {}
    }

    /// Spawns /bin/sleep (stays alive → waitUntilReady reaches the health
    /// check) or /usr/bin/false (exits immediately → crashed path).
    private final class RecordingProcessFactory: OpenCodeProcessFactory {
        struct Start {
            let executablePath: String
            let arguments: [String]
        }

        private(set) var starts: [Start] = []
        private var processes: [Process] = []
        private let executable: String

        init(executable: String = "/bin/sleep") {
            self.executable = executable
        }

        func start(
            executablePath: String,
            arguments: [String],
            environment: [String: String],
            standardError: Pipe
        ) throws -> Process {
            starts.append(Start(executablePath: executablePath, arguments: arguments))
            let process = Process()
            process.executableURL = URL(fileURLWithPath: executable)
            process.arguments = executable == "/bin/sleep" ? ["60"] : []
            process.standardError = standardError
            process.standardOutput = Pipe()
            try process.run()
            processes.append(process)
            return process
        }

        deinit {
            for process in processes where process.isRunning {
                process.terminate()
            }
        }
    }

    @MainActor
    private func makeManager(source: OpenCodeServerSource = .managedLocal) -> RuntimeManager {
        RuntimeManager(config: RuntimeConfig(
            executablePath: "/usr/bin/false",
            port: 0,
            dataDir: "/tmp/cccode-ocw-seat-\(UUID().uuidString)",
            logDir: "/tmp",
            opencodeSource: source
        ))
    }

    @MainActor
    private func injectSuccessServer(_ manager: RuntimeManager) -> RecordingProcessFactory {
        let factory = RecordingProcessFactory(executable: "/bin/sleep")
        manager.openCodeManagedServer = OpenCodeManagedServer(
            dataDir: manager.config.dataDir,
            logDir: "/tmp",
            cliSearchPath: [],
            cliResolver: StubCLIResolver(path: "/bin/sleep"),
            portProber: StubPortProber(available: [4096]),
            healthProbe: StubHealthProbe(result: OpenCodeManagedHealthCheck(noAuthStatus: 401, authedStatus: 200, body: "")),
            processFactory: factory,
            desktopController: StubDesktopController(),
            desktopConfigDir: URL(fileURLWithPath: manager.config.dataDir).appendingPathComponent("desktop-tmp")
        )
        return factory
    }

    /// URL 变化（新装机器/端口迁移）→ config 三元组更新 + restart 一次。
    @MainActor
    func testStartActionUpdatesConfigAndRestartsWhenURLChanges() async {
        let manager = makeManager()
        XCTAssertEqual(manager.config.opencodeURL, "")
        _ = injectSuccessServer(manager)

        await manager.startOpenCodeManagedServer()

        XCTAssertEqual(manager.config.opencodeURL, "http://127.0.0.1:4096", "URL 首次进 runtime 必须写入 config")
        XCTAssertFalse(manager.config.opencodeUser.isEmpty)
        XCTAssertFalse(manager.config.opencodePass.isEmpty)
        XCTAssertFalse(manager.openCodeSeatAction.starting, "动作收口后 starting 必须清零")
        XCTAssertNil(manager.openCodeSeatAction.lastStartError)

        // applyConfigAndRestart 的 1.5s 去抖窗口 + 余量 → 恰好一次 launch。
        try? await Task.sleep(nanoseconds: 2_200_000_000)
        XCTAssertEqual(manager.launchCount, 1, "URL 变化必须触发一次 restart")
    }

    /// URL 未变 → 只刷新行状态，不 restart（探针恢复即绿，无需重启 bridge）。
    @MainActor
    func testStartActionSkipsRestartWhenURLUnchanged() async {
        let manager = makeManager()
        manager.config.opencodeURL = "http://127.0.0.1:4096"
        _ = injectSuccessServer(manager)

        await manager.startOpenCodeManagedServer()

        XCTAssertEqual(manager.config.opencodeURL, "http://127.0.0.1:4096")
        XCTAssertFalse(manager.openCodeSeatAction.starting)

        try? await Task.sleep(nanoseconds: 2_200_000_000)
        XCTAssertEqual(manager.launchCount, 0, "URL 未变时不得 restart")
    }

    /// 失败 → lastStartError 为 state reason 原文；不 restart。
    @MainActor
    func testStartActionFailureSurfacesReason() async {
        let manager = makeManager()
        manager.openCodeManagedServer = OpenCodeManagedServer(
            dataDir: manager.config.dataDir,
            logDir: "/tmp",
            cliSearchPath: [],
            cliResolver: StubCLIResolver(path: nil),
            portProber: StubPortProber(available: [4096]),
            healthProbe: StubHealthProbe(result: nil),
            processFactory: RecordingProcessFactory(),
            desktopController: StubDesktopController(),
            desktopConfigDir: URL(fileURLWithPath: manager.config.dataDir).appendingPathComponent("desktop-tmp")
        )

        await manager.startOpenCodeManagedServer()

        XCTAssertEqual(manager.openCodeSeatAction.lastStartError, "opencode CLI not found", "失败必须透传 state reason 原文")
        XCTAssertFalse(manager.openCodeSeatAction.starting)
        XCTAssertEqual(manager.launchCount, 0)
    }

    /// 熔断窗口重置：显式用户动作不是 spawn 循环——5 次失败进入熔断后，
    /// start 动作先 resetFailureLimit 再尝试（spawn 计数继续增长）。
    @MainActor
    func testStartActionResetsFailureCircuitBreaker() async {
        let manager = makeManager()
        let factory = RecordingProcessFactory(executable: "/usr/bin/false")
        manager.openCodeManagedServer = OpenCodeManagedServer(
            dataDir: manager.config.dataDir,
            logDir: "/tmp",
            cliSearchPath: [],
            cliResolver: StubCLIResolver(path: "/usr/bin/false"),
            portProber: StubPortProber(available: [4096]),
            healthProbe: StubHealthProbe(result: nil),
            processFactory: factory,
            desktopController: StubDesktopController(),
            desktopConfigDir: URL(fileURLWithPath: manager.config.dataDir).appendingPathComponent("desktop-tmp")
        )
        let server = manager.openCodeManagedServer!

        // 5 次失败（/usr/bin/false 立即退出 → crashed → recordFailure）。
        for _ in 0..<5 {
            XCTAssertNil(server.ensureRunning(timeout: 1.2))
        }
        XCTAssertEqual(factory.starts.count, 5)
        // 熔断生效：第 6 次直接拒绝，不再 spawn。
        XCTAssertNil(server.ensureRunning(timeout: 1.2))
        XCTAssertEqual(factory.starts.count, 5, "熔断后不得再 spawn")

        await manager.startOpenCodeManagedServer()

        XCTAssertEqual(factory.starts.count, 6, "显式启动必须重置熔断并再次尝试")
        XCTAssertNotNil(manager.openCodeSeatAction.lastStartError)
        XCTAssertFalse(manager.openCodeSeatAction.starting)
    }

    /// 冷启动互斥：显式启动进行中（starting=true）时 resolve 跳过本轮——
    /// 不为 in-flight ensureRunning 排队，无双重 spawn。
    @MainActor
    func testResolveSkippedWhileStartActionInFlight() async {
        let manager = makeManager()
        manager.config.opencodeURL = "http://127.0.0.1:4096"
        let factory = injectSuccessServer(manager)

        // 不 await：动作在后台跑（ensureRunning 含 ~1s 健康等待）。
        let task = Task { await manager.startOpenCodeManagedServer() }
        try? await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertTrue(manager.openCodeSeatAction.starting, "动作进行中 starting 必须为 true")

        // 冷启动路径撞上 in-flight 动作：必须跳过（不 spawn 第二个进程）。
        manager.resolveManagedOpenCodeIfNeeded()
        XCTAssertEqual(factory.starts.count, 1, "resolve 在 starting 进行中必须跳过，不得双重 spawn")

        await task.value
        XCTAssertFalse(manager.openCodeSeatAction.starting)
        XCTAssertEqual(factory.starts.count, 1, "URL 未变分支收口后也不得追加 spawn")
    }

    /// 退出竞态守卫：动作进行中 App 开始 shutdown（stop() 排进串行域、state
    /// 已 .disabled）→ 完成回执不发布就绪（不写 config、不 restart）。
    @MainActor
    func testStartActionCompletionDoesNotPublishWhenDisabled() async {
        let manager = makeManager()
        XCTAssertEqual(manager.config.opencodeURL, "")
        let factory = injectSuccessServer(manager)
        let server = manager.openCodeManagedServer!

        let task = Task { await manager.startOpenCodeManagedServer() }
        try? await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertTrue(manager.openCodeSeatAction.starting)

        // shutdownForExit 的核心一步：stop() 排进串行域，等 in-flight
        // ensureRunning 有界结束后终止自有进程并置 .disabled。
        server.stop()

        await task.value

        XCTAssertEqual(manager.config.opencodeURL, "", "state 已 .disabled 时完成回执不得发布就绪")
        XCTAssertFalse(manager.openCodeSeatAction.starting)
        try? await Task.sleep(nanoseconds: 2_200_000_000)
        XCTAssertEqual(manager.launchCount, 0, "disabled 收口不得触发 restart")
        _ = factory
    }

    /// 串行域基础不变量：ensureRunning 与 stop 从不同线程交错调用，收口后
    /// state 一致（.disabled），无双重 spawn。
    @MainActor
    func testStopDuringEnsureRunningSerializes() async {
        let manager = makeManager()
        let factory = RecordingProcessFactory(executable: "/usr/bin/false")
        let server = OpenCodeManagedServer(
            dataDir: manager.config.dataDir,
            logDir: "/tmp",
            cliSearchPath: [],
            cliResolver: StubCLIResolver(path: "/usr/bin/false"),
            portProber: StubPortProber(available: [4096]),
            healthProbe: StubHealthProbe(result: nil),
            processFactory: factory,
            desktopController: StubDesktopController(),
            desktopConfigDir: URL(fileURLWithPath: manager.config.dataDir).appendingPathComponent("desktop-tmp")
        )
        // 后台线程跑 ensureRunning（~1s），主线程同时 stop：串行域保证互斥。
        let background = Task.detached {
            _ = server.ensureRunning(timeout: 1.2)
        }
        try? await Task.sleep(nanoseconds: 100_000_000)
        server.stop()
        await background.value

        XCTAssertEqual(server.currentState(), .disabled, "stop 收口后 state 必须一致为 .disabled")
        XCTAssertEqual(factory.starts.count, 1, "交错下不得双重 spawn")
    }
}
