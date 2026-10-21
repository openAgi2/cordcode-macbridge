import XCTest
@testable import CordCodeLink

final class RuntimeProcessControllerTests: XCTestCase {
    private func spec(
        command: String,
        logPath: String = "/tmp/cordcode-runtime-controller-\(UUID().uuidString).log"
    ) -> RuntimeProcessLaunchSpec {
        RuntimeProcessLaunchSpec(
            executablePath: "/bin/sh",
            arguments: ["-c", command],
            environment: ProcessInfo.processInfo.environment,
            logFilePath: logPath,
            port: 0
        )
    }

    func testConcurrentLaunchIsRejectedWithoutReplacingOwnedProcess() async throws {
        let controller = RuntimeProcessController()
        let pid = try await controller.launch(spec(command: "while :; do sleep 1; done"))

        do {
            _ = try await controller.launch(spec(command: "exit 0"))
            XCTFail("第二次 launch 不得替换仍在运行的进程")
        } catch let error as RuntimeProcessControllerError {
            guard case let .alreadyRunning(runningPID) = error else {
                return XCTFail("错误类型不符：\(error)")
            }
            XCTAssertEqual(runningPID, pid)
        }

        let snapshot = await controller.snapshot()
        XCTAssertEqual(snapshot.pid, pid)
        XCTAssertTrue(snapshot.isRunning)
        try await controller.stop(gracefulWait: .zero)
        let stopped = await controller.snapshot()
        XCTAssertFalse(stopped.isRunning)
    }

    func testStopEscalatesFromTermToKillAndReapsProcess() async throws {
        let controller = RuntimeProcessController()
        let pid = try await controller.launch(spec(command: "trap '' TERM; while :; do sleep 1; done"))
        try await Task.sleep(for: .milliseconds(100))
        let started = ContinuousClock.now

        try await controller.stopAndConfirmPort(gracefulWait: .zero, port: 0)

        let elapsed = ContinuousClock.now - started
        let snapshot = await controller.snapshot()
        XCTAssertFalse(snapshot.isRunning)
        XCTAssertNil(snapshot.pid)
        XCTAssertLessThan(elapsed, .seconds(5))
        XCTAssertNotEqual(kill(pid, 0), 0, "stop 返回前 PID 必须已消失")
    }

    func testNaturalExitProducesSingleConsumableTerminationSnapshot() async throws {
        let controller = RuntimeProcessController()
        let pid = try await controller.launch(spec(command: "exit 7"))

        let deadline = ContinuousClock.now.advanced(by: .seconds(2))
        while await controller.snapshot().isRunning, ContinuousClock.now < deadline {
            try await Task.sleep(for: .milliseconds(25))
        }

        let termination = await controller.consumeTermination()
        XCTAssertEqual(termination, RuntimeProcessTermination(pid: pid, status: 7))
        let consumed = await controller.consumeTermination()
        XCTAssertNil(consumed)
    }
}

    /// O-A（2026-10-21 评审 R3）：runtime 每次启动保留上一代日志——shiftLogGenerations
    /// 无条件滚动 .2→.3、.1→.2、本体→.1（此前 openLogHandle 直接 createFile 截断，
    /// 每次重启清空上一代现场）。
    func testShiftLogGenerationsPreservesPreviousGenerations() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("oa-log-shift-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let path = dir.appendingPathComponent("go-bridge.log").path

        try "gen-current".write(toFile: path, atomically: true, encoding: .utf8)
        try "gen-1".write(toFile: "\(path).1", atomically: true, encoding: .utf8)
        try "gen-2".write(toFile: "\(path).2", atomically: true, encoding: .utf8)
        try "gen-3".write(toFile: "\(path).3", atomically: true, encoding: .utf8)

        RuntimeProcessController.shiftLogGenerations(path)

        func content(_ suffix: String) -> String {
            (try? String(contentsOfFile: path + suffix, encoding: .utf8)) ?? "<missing>"
        }
        XCTAssertEqual(content(".1"), "gen-current", "上一代本体必须保留为 .1")
        XCTAssertEqual(content(".2"), "gen-1")
        XCTAssertEqual(content(".3"), "gen-2", "最老一代（gen-3）被挤出保留窗口")
        XCTAssertFalse(FileManager.default.fileExists(atPath: path), "本体由 openLogHandle 重建，shift 后应不存在")
    }

    /// 空目录/无既有日志时启动滚动应为 no-op（不抛错、不产生幽灵文件）。
    func testShiftLogGenerationsNoopWhenAbsent() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("oa-log-shift-empty-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let path = dir.appendingPathComponent("go-bridge.log").path

        RuntimeProcessController.shiftLogGenerations(path)

        for suffix in ["", ".1", ".2", ".3"] {
            XCTAssertFalse(FileManager.default.fileExists(atPath: path + suffix), "无日志时不得产生文件：\(suffix)")
        }
    }
