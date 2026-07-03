import XCTest
@testable import CordCodeLink

final class OpenCodeManagedServerTests: XCTestCase {
    func testCLIMissingBecomesUnavailableWithoutStartingProcess() {
        let dir = tempDir()
        let factory = RecordingProcessFactory()
        let server = OpenCodeManagedServer(
            dataDir: dir.path,
            logDir: dir.appendingPathComponent("logs").path,
            cliSearchPath: ["/missing"],
            cliResolver: StubCLIResolver(path: nil),
            portProber: StubPortProber(available: [4096]),
            healthProbe: StubHealthProbe(result: nil),
            processFactory: factory,
            desktopController: StubDesktopController()
        )

        XCTAssertNil(server.ensureRunning(timeout: 0.1))
        XCTAssertEqual(factory.starts.count, 0)
        XCTAssertEqual(server.state, .unavailable(reason: "opencode CLI not found"))
    }

    func testStartsOpencodeServeWithSecretOnlyInEnvironment() throws {
        let dir = tempDir()
        let factory = RecordingProcessFactory()
        let server = OpenCodeManagedServer(
            dataDir: dir.path,
            logDir: dir.appendingPathComponent("logs").path,
            cliSearchPath: ["/opt/homebrew/bin"],
            cliResolver: StubCLIResolver(path: "/opt/homebrew/bin/opencode"),
            portProber: StubPortProber(available: [4096]),
            healthProbe: StubHealthProbe(result: OpenCodeManagedHealthCheck(noAuthStatus: 401, authedStatus: 200, body: #"{"healthy":true}"#)),
            processFactory: factory,
            desktopController: StubDesktopController()
        )
        defer { server.stop() }

        let endpoint = try XCTUnwrap(server.ensureRunning(timeout: 2.0))
        let start = try XCTUnwrap(factory.starts.first)
        XCTAssertEqual(endpoint.url, "http://127.0.0.1:4096")
        XCTAssertEqual(start.executablePath, "/opt/homebrew/bin/opencode")
        XCTAssertEqual(start.arguments, ["serve", "--hostname", "127.0.0.1", "--port", "4096", "--print-logs"])
        XCTAssertEqual(start.environment["OPENCODE_SERVER_USERNAME"], "opencode")
        XCTAssertEqual(start.environment["OPENCODE_SERVER_PASSWORD"], endpoint.password)
        XCTAssertFalse(start.arguments.joined(separator: " ").contains(endpoint.password))
    }

    func testPortSelectionSkipsOccupiedPort() throws {
        let dir = tempDir()
        let factory = RecordingProcessFactory()
        let server = OpenCodeManagedServer(
            dataDir: dir.path,
            logDir: dir.appendingPathComponent("logs").path,
            cliSearchPath: ["/opt/homebrew/bin"],
            cliResolver: StubCLIResolver(path: "/opt/homebrew/bin/opencode"),
            portProber: StubPortProber(available: [4097]),
            healthProbe: URLHealthProbe(results: [
                "http://127.0.0.1:4097": OpenCodeManagedHealthCheck(noAuthStatus: 401, authedStatus: 200, body: "{}")
            ]),
            processFactory: factory,
            desktopController: StubDesktopController()
        )
        defer { server.stop() }

        let endpoint = try XCTUnwrap(server.ensureRunning(timeout: 2.0))
        XCTAssertEqual(endpoint.url, "http://127.0.0.1:4097")
        XCTAssertEqual(factory.starts.first?.arguments, ["serve", "--hostname", "127.0.0.1", "--port", "4097", "--print-logs"])
    }

    func testPortSelectionSkipsHealthyPortWithStalePersistedPID() throws {
        let dir = tempDir()
        let state = """
        {
          "version": 1,
          "url": "http://127.0.0.1:4096",
          "port": 4096,
          "username": "opencode",
          "password": "p",
          "pid": 999999,
          "updated_at": "2026-07-03T00:00:00Z"
        }
        """
        try state.write(to: dir.appendingPathComponent("opencode-managed-server.json"), atomically: true, encoding: .utf8)
        let factory = RecordingProcessFactory()
        let server = OpenCodeManagedServer(
            dataDir: dir.path,
            logDir: dir.appendingPathComponent("logs").path,
            cliSearchPath: ["/opt/homebrew/bin"],
            cliResolver: StubCLIResolver(path: "/opt/homebrew/bin/opencode"),
            portProber: StubPortProber(available: [4097]),
            healthProbe: URLHealthProbe(results: [
                "http://127.0.0.1:4096": OpenCodeManagedHealthCheck(noAuthStatus: 401, authedStatus: 200, body: "{}"),
                "http://127.0.0.1:4097": OpenCodeManagedHealthCheck(noAuthStatus: 401, authedStatus: 200, body: "{}"),
            ]),
            processFactory: factory,
            desktopController: StubDesktopController()
        )
        defer { server.stop() }

        let endpoint = try XCTUnwrap(server.ensureRunning(timeout: 2.0))
        XCTAssertEqual(endpoint.url, "http://127.0.0.1:4097")
        XCTAssertEqual(factory.starts.first?.arguments, ["serve", "--hostname", "127.0.0.1", "--port", "4097", "--print-logs"])
    }

    func testPersistenceFileUses0600Permissions() throws {
        let dir = tempDir()
        let server = OpenCodeManagedServer(
            dataDir: dir.path,
            logDir: dir.appendingPathComponent("logs").path,
            cliSearchPath: ["/opt/homebrew/bin"],
            cliResolver: StubCLIResolver(path: "/opt/homebrew/bin/opencode"),
            portProber: StubPortProber(available: [4096]),
            healthProbe: StubHealthProbe(result: OpenCodeManagedHealthCheck(noAuthStatus: 401, authedStatus: 200, body: "{}")),
            processFactory: RecordingProcessFactory(),
            desktopController: StubDesktopController()
        )
        defer { server.stop() }

        _ = server.ensureRunning(timeout: 2.0)
        let path = dir.appendingPathComponent("opencode-managed-server.json").path
        let attrs = try FileManager.default.attributesOfItem(atPath: path)
        XCTAssertEqual((attrs[.posixPermissions] as? NSNumber)?.intValue, 0o600)
    }

    func testEvaluateReadyRequiresHealthNotJustStdoutHint() {
        XCTAssertEqual(
            OpenCodeManagedServer.evaluateReady(processAlive: true, noAuthStatus: nil, authedStatus: nil, stdoutHint: "http://127.0.0.1:4096"),
            .starting
        )
        XCTAssertEqual(
            OpenCodeManagedServer.evaluateReady(processAlive: true, noAuthStatus: 401, authedStatus: 200, stdoutHint: "http://127.0.0.1:4096"),
            .running(url: "http://127.0.0.1:4096", pid: 0)
        )
    }

    private func tempDir() -> URL {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("oc-managed-\(UUID().uuidString)", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }
        return dir
    }
}

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

private struct URLHealthProbe: OpenCodeManagedHealthProbing {
    let results: [String: OpenCodeManagedHealthCheck]
    func check(url: String, username: String, password: String) -> OpenCodeManagedHealthCheck? {
        results[url]
    }
}

private final class RecordingProcessFactory: OpenCodeProcessFactory {
    struct Start {
        let executablePath: String
        let arguments: [String]
        let environment: [String: String]
    }

    private(set) var starts: [Start] = []
    private var processes: [Process] = []

    func start(
        executablePath: String,
        arguments: [String],
        environment: [String: String],
        standardError: Pipe
    ) throws -> Process {
        starts.append(Start(executablePath: executablePath, arguments: arguments, environment: environment))
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sleep")
        process.arguments = ["60"]
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

private struct StubDesktopController: OpenCodeDesktopProcessControlling {
    var running = false
    func isOpenCodeDesktopRunning() -> Bool { running }
    func terminateOpenCodeDesktop(timeout: TimeInterval) -> Bool { true }
    func openOpenCodeDesktop() {}
}
