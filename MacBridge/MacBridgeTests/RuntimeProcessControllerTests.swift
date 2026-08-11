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
