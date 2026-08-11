import Darwin
import Foundation

struct RuntimeProcessLaunchSpec: Sendable {
    let executablePath: String
    let arguments: [String]
    let environment: [String: String]
    let logFilePath: String
    let port: Int
}

struct RuntimeProcessTermination: Equatable, Sendable {
    let pid: Int32
    let status: Int32
}

struct RuntimeProcessSnapshot: Equatable, Sendable {
    let pid: Int32?
    let isRunning: Bool
    let lastTermination: RuntimeProcessTermination?
}

enum RuntimeProcessControllerError: LocalizedError {
    case alreadyRunning(Int32)
    case portOccupied(Int, String)
    case oldRuntimeDidNotExit(Int32)
    case portDidNotRelease(Int)
    case launchFailed(String)

    var errorDescription: String? {
        switch self {
        case let .alreadyRunning(pid): return "Bridge runtime 已在运行 (PID \(pid))"
        case let .portOccupied(port, owner): return "端口 \(port) 已被占用：\(owner)"
        case let .oldRuntimeDidNotExit(pid): return "旧 Bridge runtime 未能退出 (PID \(pid))"
        case let .portDidNotRelease(port): return "旧 Bridge runtime 退出后端口 \(port) 未释放"
        case let .launchFailed(message): return "Bridge runtime 启动失败：\(message)"
        }
    }
}

/// go-bridge runtime 的唯一 Process/FileHandle/PID/port owner。
/// RuntimeManager 只发送 Sendable spec 并消费 Sendable snapshot。
actor RuntimeProcessController {
    private var process: Process?
    private var logHandle: FileHandle?
    private var lastTermination: RuntimeProcessTermination?

    func snapshot() -> RuntimeProcessSnapshot {
        RuntimeProcessSnapshot(
            pid: process?.processIdentifier,
            isRunning: process?.isRunning == true,
            lastTermination: lastTermination
        )
    }

    func consumeTermination() -> RuntimeProcessTermination? {
        defer { lastTermination = nil }
        return lastTermination
    }

    func launch(_ spec: RuntimeProcessLaunchSpec) async throws -> Int32 {
        if let process, process.isRunning {
            throw RuntimeProcessControllerError.alreadyRunning(process.processIdentifier)
        }
        try await preparePortForLaunch(spec)
        closeOwnedResources()
        let handle = try openLogHandle(path: spec.logFilePath)
        let child = Process()
        child.executableURL = URL(fileURLWithPath: spec.executablePath)
        child.arguments = spec.arguments
        child.environment = spec.environment
        child.standardOutput = handle
        child.standardError = handle
        child.terminationHandler = { [weak self] terminated in
            let pid = terminated.processIdentifier
            let status = terminated.terminationStatus
            Task { await self?.recordTermination(pid: pid, status: status) }
        }
        do {
            try child.run()
        } catch {
            try? handle.close()
            throw RuntimeProcessControllerError.launchFailed(error.localizedDescription)
        }
        process = child
        logHandle = handle
        lastTermination = nil
        return child.processIdentifier
    }

    /// committed graceful exit → TERM → KILL → reap → PID/port release。
    func stop(gracefulWait: Duration = .seconds(2)) async throws {
        guard let child = process else { return }
        let pid = child.processIdentifier
        if child.isRunning {
            _ = await waitForExit(child, timeout: gracefulWait)
        }
        if child.isRunning {
            child.terminate()
            _ = await waitForExit(child, timeout: .seconds(2))
        }
        if child.isRunning {
            kill(pid, SIGKILL)
            _ = await waitForExit(child, timeout: .seconds(2))
        }
        guard !child.isRunning, !pidExists(pid) else {
            throw RuntimeProcessControllerError.oldRuntimeDidNotExit(pid)
        }
        child.terminationHandler = nil
        process = nil
        closeOwnedResources()
    }

    func stopAndConfirmPort(gracefulWait: Duration = .seconds(2), port: Int) async throws {
        try await stop(gracefulWait: gracefulWait)
        if port <= 0 { return }
        guard await waitUntilPortFree(port, timeout: .seconds(2)) else {
            throw RuntimeProcessControllerError.portDidNotRelease(port)
        }
    }

    private func recordTermination(pid: Int32, status: Int32) {
        guard process?.processIdentifier == pid else { return }
        lastTermination = RuntimeProcessTermination(pid: pid, status: status)
        process?.terminationHandler = nil
        process = nil
        closeOwnedResources()
    }

    private func waitForExit(_ child: Process, timeout: Duration) async -> Bool {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while child.isRunning, clock.now < deadline {
            try? await Task.sleep(for: .milliseconds(50))
        }
        return !child.isRunning
    }

    private func waitUntilPortFree(_ port: Int, timeout: Duration) async -> Bool {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while clock.now < deadline {
            if portOwner(port) == nil { return true }
            try? await Task.sleep(for: .milliseconds(100))
        }
        return portOwner(port) == nil
    }

    private func preparePortForLaunch(_ spec: RuntimeProcessLaunchSpec) async throws {
        guard spec.port > 0, let owner = portOwner(spec.port) else { return }
        let ownerText = owner.executablePath ?? owner.command
        guard canTakeOver(owner, runtimePath: spec.executablePath) else {
            throw RuntimeProcessControllerError.portOccupied(spec.port, ownerText)
        }
        kill(owner.pid, SIGTERM)
        if await waitUntilPortFree(spec.port, timeout: .seconds(2)) { return }
        kill(owner.pid, SIGKILL)
        guard await waitUntilPortFree(spec.port, timeout: .seconds(2)) else {
            throw RuntimeProcessControllerError.portDidNotRelease(spec.port)
        }
    }

    private struct PortOwner {
        let pid: Int32
        let command: String
        let executablePath: String?
    }

    private func portOwner(_ port: Int) -> PortOwner? {
        let output = runCommand("/usr/sbin/lsof", ["-nP", "-iTCP:\(port)", "-sTCP:LISTEN", "-Fpc"])
        var pid: Int32?
        var command = ""
        for line in output.split(separator: "\n").map(String.init) {
            if line.hasPrefix("p"), let value = Int32(line.dropFirst()) { pid = value }
            if line.hasPrefix("c") { command = String(line.dropFirst()) }
        }
        guard let pid else { return nil }
        return PortOwner(pid: pid, command: command, executablePath: executablePath(pid))
    }

    private func executablePath(_ pid: Int32) -> String? {
        let output = runCommand("/usr/sbin/lsof", ["-p", "\(pid)", "-Fn"])
        var textFile = false
        for line in output.split(separator: "\n").map(String.init) {
            if line == "ftxt" { textFile = true; continue }
            if textFile, line.hasPrefix("n") { return String(line.dropFirst()) }
            textFile = false
        }
        return nil
    }

    private func canTakeOver(_ owner: PortOwner, runtimePath: String) -> Bool {
        let executable = owner.executablePath ?? ""
        let command = owner.command
        return executable == runtimePath
            || executable.hasSuffix("/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime")
            || executable.contains("/go-bridge/go-bridge")
            || command.contains("/go-bridge/go-bridge")
            || executable.contains("/cordcode-bridge-runtime")
            || command.hasSuffix("/cordcode-bridge-runtime")
    }

    private func pidExists(_ pid: Int32) -> Bool {
        if kill(pid, 0) == 0 { return true }
        return errno != ESRCH
    }

    private func openLogHandle(path: String) throws -> FileHandle {
        guard !path.isEmpty else { return try FileHandle(forWritingTo: URL(fileURLWithPath: "/dev/null")) }
        let manager = FileManager.default
        if let attributes = try? manager.attributesOfItem(atPath: path),
           attributes[.type] as? FileAttributeType == .typeSymbolicLink {
            try manager.removeItem(atPath: path)
        }
        rotateLogIfNeeded(path)
        manager.createFile(atPath: path, contents: nil, attributes: [.posixPermissions: 0o600])
        let handle = try FileHandle(forWritingTo: URL(fileURLWithPath: path))
        try handle.seekToEnd()
        return handle
    }

    private func rotateLogIfNeeded(_ path: String) {
        let manager = FileManager.default
        guard let size = (try? manager.attributesOfItem(atPath: path)[.size]) as? Int64,
              size >= 8 * 1024 * 1024 else { return }
        try? manager.removeItem(atPath: "\(path).3")
        for generation in stride(from: 2, through: 1, by: -1) {
            let source = "\(path).\(generation)"
            if manager.fileExists(atPath: source) {
                try? manager.moveItem(atPath: source, toPath: "\(path).\(generation + 1)")
            }
        }
        try? manager.moveItem(atPath: path, toPath: "\(path).1")
    }

    private func closeOwnedResources() {
        process?.terminationHandler = nil
        try? logHandle?.close()
        logHandle = nil
    }

    private func runCommand(_ executable: String, _ arguments: [String]) -> String {
        let helper = Process()
        let output = Pipe()
        helper.executableURL = URL(fileURLWithPath: executable)
        helper.arguments = arguments
        helper.standardOutput = output
        helper.standardError = FileHandle.nullDevice
        do {
            try helper.run()
            helper.waitUntilExit()
            return String(decoding: output.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
        } catch {
            return ""
        }
    }
}
