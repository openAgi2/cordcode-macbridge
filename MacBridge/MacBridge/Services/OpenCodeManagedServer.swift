import AppKit
import Foundation

enum OpenCodeManagedServerState: Equatable {
    case disabled
    case starting
    case running(url: String, pid: Int32)
    case unavailable(reason: String)
    case crashed(reason: String)
}

struct OpenCodeManagedEndpoint: Equatable {
    let url: String
    let username: String
    let password: String
}

protocol OpenCodeCLIResolving {
    func resolveOpenCodeCLI(searchPath: [String]) -> String?
}

protocol OpenCodePortProbing {
    func isPortAvailable(_ port: Int) -> Bool
}

protocol OpenCodeProcessFactory {
    func start(
        executablePath: String,
        arguments: [String],
        environment: [String: String],
        standardError: Pipe
    ) throws -> Process
}

protocol OpenCodeDesktopProcessControlling {
    func isOpenCodeDesktopRunning() -> Bool
    func terminateOpenCodeDesktop(timeout: TimeInterval) -> Bool
    func openOpenCodeDesktop()
}

struct OpenCodeManagedHealthCheck: Equatable {
    let noAuthStatus: Int
    let authedStatus: Int
    let body: String
}

protocol OpenCodeManagedHealthProbing {
    func check(url: String, username: String, password: String) -> OpenCodeManagedHealthCheck?
}

struct DefaultOpenCodeCLIResolver: OpenCodeCLIResolving {
    func resolveOpenCodeCLI(searchPath: [String]) -> String? {
        let fm = FileManager.default
        for directory in searchPath {
            let candidate = URL(fileURLWithPath: directory).appendingPathComponent("opencode").path
            if fm.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        for candidate in ["/opt/homebrew/bin/opencode", "/usr/local/bin/opencode", "/usr/bin/opencode"] {
            if fm.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        return nil
    }
}

struct DefaultOpenCodePortProber: OpenCodePortProbing {
    func isPortAvailable(_ port: Int) -> Bool {
        OpenCodeManagedServer.runCommand("/usr/sbin/lsof", [
            "-nP", "-iTCP:\(port)", "-sTCP:LISTEN",
        ]).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}

struct DefaultOpenCodeProcessFactory: OpenCodeProcessFactory {
    func start(
        executablePath: String,
        arguments: [String],
        environment: [String: String],
        standardError: Pipe
    ) throws -> Process {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executablePath)
        process.arguments = arguments
        process.environment = environment
        process.standardError = standardError
        process.standardOutput = Pipe()
        try process.run()
        return process
    }
}

struct DefaultOpenCodeManagedHealthProbe: OpenCodeManagedHealthProbing {
    func check(url: String, username: String, password: String) -> OpenCodeManagedHealthCheck? {
        let checker = OpenCodeManagedServer.HealthChecker()
        return checker.check(url: url, username: username, password: password)
    }
}

struct DefaultOpenCodeDesktopProcessController: OpenCodeDesktopProcessControlling {
    func isOpenCodeDesktopRunning() -> Bool {
        !NSRunningApplication.runningApplications(withBundleIdentifier: "ai.opencode.desktop").isEmpty
    }

    func terminateOpenCodeDesktop(timeout: TimeInterval) -> Bool {
        let apps = NSRunningApplication.runningApplications(withBundleIdentifier: "ai.opencode.desktop")
        guard !apps.isEmpty else { return true }
        for app in apps {
            _ = app.terminate()
        }
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !isOpenCodeDesktopRunning() {
                return true
            }
            Thread.sleep(forTimeInterval: 0.2)
        }
        return !isOpenCodeDesktopRunning()
    }

    func openOpenCodeDesktop() {
        NSWorkspace.shared.openApplication(
            at: URL(fileURLWithPath: "/Applications/OpenCode.app"),
            configuration: NSWorkspace.OpenConfiguration()
        )
    }
}

final class OpenCodeManagedServer {
    private struct PersistedState: Codable {
        var version: Int
        var url: String
        var port: Int
        var username: String
        var password: String
        var pid: Int32?
        var updatedAt: String

        enum CodingKeys: String, CodingKey {
            case version, url, port, username, password, pid
            case updatedAt = "updated_at"
        }
    }

    private let dataDir: String
    private let logDir: String
    private let cliSearchPath: [String]
    private let cliResolver: OpenCodeCLIResolving
    private let portProber: OpenCodePortProbing
    private let healthProbe: OpenCodeManagedHealthProbing
    private let processFactory: OpenCodeProcessFactory
    let desktopController: OpenCodeDesktopProcessControlling

    private var process: Process?
    private var stderrPipe: Pipe?
    private var stderrHandle: FileHandle?
    private var consecutiveFailures: [Date] = []
    private(set) var state: OpenCodeManagedServerState = .disabled

    init(
        dataDir: String,
        logDir: String,
        cliSearchPath: [String],
        cliResolver: OpenCodeCLIResolving = DefaultOpenCodeCLIResolver(),
        portProber: OpenCodePortProbing = DefaultOpenCodePortProber(),
        healthProbe: OpenCodeManagedHealthProbing = DefaultOpenCodeManagedHealthProbe(),
        processFactory: OpenCodeProcessFactory = DefaultOpenCodeProcessFactory(),
        desktopController: OpenCodeDesktopProcessControlling = DefaultOpenCodeDesktopProcessController()
    ) {
        self.dataDir = dataDir
        self.logDir = logDir
        self.cliSearchPath = cliSearchPath
        self.cliResolver = cliResolver
        self.portProber = portProber
        self.healthProbe = healthProbe
        self.processFactory = processFactory
        self.desktopController = desktopController
    }

    func ensureRunning(timeout: TimeInterval = 5.0) -> OpenCodeManagedEndpoint? {
        state = .starting
        guard let cliPath = cliResolver.resolveOpenCodeCLI(searchPath: cliSearchPath) else {
            state = .unavailable(reason: "opencode CLI not found")
            return nil
        }
        guard !isFailureLimited() else {
            state = .unavailable(reason: "opencode managed server failed repeatedly")
            return nil
        }

        var persisted = loadState() ?? newState()
        guard let port = selectPort(preferred: persisted.port, state: persisted) else {
            state = .unavailable(reason: "managed OpenCode port range is occupied")
            return nil
        }
        persisted.port = port
        persisted.url = "http://127.0.0.1:\(port)"

        if let adopted = adoptExistingProcessIfHealthy(persisted) {
            state = .running(url: adopted.url, pid: adopted.pid ?? 0)
            saveState(adopted)
            return OpenCodeManagedEndpoint(url: adopted.url, username: adopted.username, password: adopted.password)
        }

        stopOwnedProcess()
        let stderr = Pipe()
        stderrPipe = stderr
        redirectStderr(stderr, password: persisted.password)

        var environment = ProcessInfo.processInfo.environment
        environment["PATH"] = mergedPath(cliSearchPath)
        environment["OPENCODE_SERVER_USERNAME"] = persisted.username
        environment["OPENCODE_SERVER_PASSWORD"] = persisted.password
        do {
            let launched = try processFactory.start(
                executablePath: cliPath,
                arguments: ["serve", "--hostname", "127.0.0.1", "--port", "\(port)", "--print-logs"],
                environment: environment,
                standardError: stderr
            )
            process = launched
            persisted.pid = launched.processIdentifier
            saveState(persisted)
            guard waitUntilReady(endpoint: persisted, process: launched, timeout: timeout) else {
                recordFailure()
                if launched.isRunning {
                    launched.terminate()
                }
                state = launched.isRunning ? .unavailable(reason: "opencode managed server health timed out") : .crashed(reason: "opencode serve exited before ready")
                return nil
            }
            state = .running(url: persisted.url, pid: launched.processIdentifier)
            saveState(persisted)
            return OpenCodeManagedEndpoint(url: persisted.url, username: persisted.username, password: persisted.password)
        } catch {
            recordFailure()
            state = .unavailable(reason: "failed to start opencode serve: \(error.localizedDescription)")
            return nil
        }
    }

    func stop() {
        stopOwnedProcess()
        state = .disabled
    }

    func syncDesktopConfig(url: String, username: String, password: String) -> OpenCodeDesktopSyncResult {
        let desktopDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/ai.opencode.desktop")
        let result = RuntimeManager.configureOpenCodeDesktopSettings(
            desktopDir: desktopDir,
            serverURL: url,
            username: username,
            password: password
        )
        guard desktopController.isOpenCodeDesktopRunning(),
              result.previousSidecarURL != nil,
              result.previousSidecarURL != url else {
            return result
        }

        let deadline = Date().addingTimeInterval(3)
        while Date() < deadline {
            if hasEstablishedConnection(toPortIn: url) {
                return result
            }
            Thread.sleep(forTimeInterval: 0.2)
        }

        if desktopController.terminateOpenCodeDesktop(timeout: 10) {
            desktopController.openOpenCodeDesktop()
        } else {
            NSLog("[OpenCodeManagedServer] Desktop restart required: graceful termination timed out")
        }
        return result
    }

    static func evaluateReady(processAlive: Bool, noAuthStatus: Int?, authedStatus: Int?, stdoutHint: String?) -> OpenCodeManagedServerState {
        guard processAlive else { return .crashed(reason: "opencode serve exited before ready") }
        guard noAuthStatus == 401, authedStatus == 200 else {
            return .starting
        }
        return .running(url: stdoutHint ?? "", pid: 0)
    }

    private func newState() -> PersistedState {
        let port = 4096
        return PersistedState(
            version: 1,
            url: "http://127.0.0.1:\(port)",
            port: port,
            username: "opencode",
            password: OpenCodeCredentialsGenerator.generatePassword(),
            pid: nil,
            updatedAt: isoNow()
        )
    }

    private func selectPort(preferred: Int, state: PersistedState) -> Int? {
        if preferred > 0, portProber.isPortAvailable(preferred) || canAdoptPersistedProcess(state) {
            return preferred
        }
        for port in 4096...4196 {
            if portProber.isPortAvailable(port) {
                return port
            }
        }
        return nil
    }

    private func adoptExistingProcessIfHealthy(_ state: PersistedState) -> PersistedState? {
        guard canAdoptPersistedProcess(state), let pid = state.pid else { return nil }
        return state
    }

    private func canAdoptPersistedProcess(_ state: PersistedState) -> Bool {
        guard let pid = state.pid, pid > 0, isProcessAlive(pid) else { return false }
        let command = commandLine(forPID: pid)
        guard command.contains("opencode serve"),
              command.contains("--port \(state.port)") else {
            return false
        }
        guard let health = healthCheck(state),
              health.noAuthStatus == 401,
              health.authedStatus == 200 else {
            kill(pid, SIGTERM)
            return false
        }
        return true
    }

    private func waitUntilReady(endpoint: PersistedState, process: Process, timeout: TimeInterval) -> Bool {
        Thread.sleep(forTimeInterval: 1.0)
        let deadline = Date().addingTimeInterval(max(0, timeout - 1.0))
        while Date() < deadline {
            guard process.isRunning else { return false }
            if let health = healthCheck(endpoint),
               health.noAuthStatus == 401,
               health.authedStatus == 200 {
                return true
            }
            Thread.sleep(forTimeInterval: 0.25)
        }
        return false
    }

    private func healthCheck(_ endpoint: PersistedState) -> OpenCodeManagedHealthCheck? {
        healthProbe.check(url: endpoint.url, username: endpoint.username, password: endpoint.password)
    }

    struct HealthChecker {
        func check(url: String, username: String, password: String) -> OpenCodeManagedHealthCheck? {
            let noAuth = fetchHealth(url: url, username: nil, password: nil)
            let authed = fetchHealth(url: url, username: username, password: password)
            guard let noAuth, let authed else { return nil }
            return OpenCodeManagedHealthCheck(noAuthStatus: noAuth.status, authedStatus: authed.status, body: authed.body)
        }

        private func fetchHealth(url: String, username: String?, password: String?) -> (status: Int, body: String)? {
            guard let healthURL = URL(string: url + "/global/health") else { return nil }
            var request = URLRequest(url: healthURL)
            request.timeoutInterval = 1.0
            if let username, let password {
                request.setValue(OpenCodeEndpointResolver.basicAuthHeader(user: username, password: password), forHTTPHeaderField: "Authorization")
            }
            let semaphore = DispatchSemaphore(value: 0)
            let lock = NSLock()
            var result: (Int, String)?
            URLSession.shared.dataTask(with: request) { data, response, _ in
                if let http = response as? HTTPURLResponse {
                    lock.lock()
                    result = (http.statusCode, data.flatMap { String(data: $0, encoding: .utf8) } ?? "")
                    lock.unlock()
                }
                semaphore.signal()
            }.resume()
            _ = semaphore.wait(timeout: .now() + 1.5)
            lock.lock()
            let value = result
            lock.unlock()
            return value
        }
    }

    private func loadState() -> PersistedState? {
        guard let data = FileManager.default.contents(atPath: statePath),
              let decoded = try? JSONDecoder().decode(PersistedState.self, from: data),
              decoded.version == 1 else {
            return nil
        }
        return decoded
    }

    private func saveState(_ state: PersistedState) {
        var updated = state
        updated.updatedAt = isoNow()
        do {
            try FileManager.default.createDirectory(atPath: dataDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            let encoder = JSONEncoder()
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            let data = try encoder.encode(updated)
            try data.write(to: URL(fileURLWithPath: statePath), options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: statePath)
        } catch {
            NSLog("[OpenCodeManagedServer] Failed to persist state: \(error.localizedDescription)")
        }
    }

    private var statePath: String {
        dataDir + "/opencode-managed-server.json"
    }

    private var stderrLogPath: String {
        logDir + "/opencode-managed-server.err.log"
    }

    private func redirectStderr(_ pipe: Pipe, password: String) {
        rotateLog(path: stderrLogPath)
        try? FileManager.default.createDirectory(atPath: logDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        FileManager.default.createFile(atPath: stderrLogPath, contents: nil, attributes: [.posixPermissions: 0o600])
        guard let handle = try? FileHandle(forWritingTo: URL(fileURLWithPath: stderrLogPath)) else { return }
        stderrHandle = handle
        pipe.fileHandleForReading.readabilityHandler = { fh in
            let data = fh.availableData
            guard !data.isEmpty else {
                fh.readabilityHandler = nil
                try? handle.close()
                return
            }
            var text = String(data: data, encoding: .utf8) ?? ""
            text = Self.redact(text, password: password)
            if let redacted = text.data(using: .utf8) {
                try? handle.seekToEnd()
                handle.write(redacted)
            }
        }
    }

    private func rotateLog(path: String) {
        let fm = FileManager.default
        guard let attrs = try? fm.attributesOfItem(atPath: path),
              let size = attrs[.size] as? Int64,
              size >= 8 * 1024 * 1024 else { return }
        let oldest = "\(path).3"
        if fm.fileExists(atPath: oldest) { try? fm.removeItem(atPath: oldest) }
        for gen in stride(from: 2, through: 1, by: -1) {
            let from = "\(path).\(gen)"
            let to = "\(path).\(gen + 1)"
            if fm.fileExists(atPath: from) { try? fm.moveItem(atPath: from, toPath: to) }
        }
        try? fm.moveItem(atPath: path, toPath: "\(path).1")
    }

    private static func redact(_ text: String, password: String) -> String {
        var result = text
        if !password.isEmpty {
            result = result.replacingOccurrences(of: password, with: "<redacted>")
        }
        result = result.replacingOccurrences(of: #"Basic [A-Za-z0-9+/=]+"#, with: "Basic <redacted>", options: .regularExpression)
        result = result.replacingOccurrences(of: #"OPENCODE_SERVER_PASSWORD=[^\s]+"#, with: "OPENCODE_SERVER_PASSWORD=<redacted>", options: .regularExpression)
        return result
    }

    private func stopOwnedProcess() {
        stderrPipe?.fileHandleForReading.readabilityHandler = nil
        stderrPipe = nil
        try? stderrHandle?.close()
        stderrHandle = nil
        guard let process, process.isRunning else {
            process = nil
            return
        }
        process.terminate()
        let deadline = Date().addingTimeInterval(2)
        while process.isRunning && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.05)
        }
        if process.isRunning {
            kill(process.processIdentifier, SIGKILL)
        }
        self.process = nil
    }

    private func isFailureLimited() -> Bool {
        let cutoff = Date().addingTimeInterval(-60)
        consecutiveFailures = consecutiveFailures.filter { $0 >= cutoff }
        return consecutiveFailures.count >= 5
    }

    private func recordFailure() {
        consecutiveFailures.append(Date())
    }

    private func hasEstablishedConnection(toPortIn url: String) -> Bool {
        guard let port = URLComponents(string: url)?.port else { return false }
        return Self.runCommand("/usr/sbin/lsof", [
            "-nP", "-iTCP:\(port)", "-sTCP:ESTABLISHED",
        ]).contains("OpenCode")
    }

    private func isProcessAlive(_ pid: Int32) -> Bool {
        kill(pid, 0) == 0
    }

    private func commandLine(forPID pid: Int32) -> String {
        Self.runCommand("/bin/ps", ["-p", "\(pid)", "-o", "command="])
    }

    private func mergedPath(_ searchPath: [String]) -> String {
        var seen = Set<String>()
        var parts: [String] = []
        for path in searchPath + (ProcessInfo.processInfo.environment["PATH"] ?? "").split(separator: ":").map(String.init) {
            if seen.insert(path).inserted {
                parts.append(path)
            }
        }
        return parts.joined(separator: ":")
    }

    private func isoNow() -> String {
        ISO8601DateFormatter().string(from: Date())
    }

    static func runCommand(_ path: String, _ arguments: [String]) -> String {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = arguments
        let pipe = Pipe()
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
