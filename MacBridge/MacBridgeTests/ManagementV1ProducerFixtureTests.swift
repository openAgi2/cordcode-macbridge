import Foundation
import XCTest
@testable import CordCodeLink

final class ManagementV1ProducerFixtureTests: XCTestCase {
    private var fixtureRoot: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("docs/protocol/samples/management-file-read")
    }

    private func producedFixtures() throws -> [String: Data] {
        let identity = ManagementRuntimeIdentity(pid: 12345, bridgeEpoch: 1)
        let quiesce = ManagementQuiesceRequest(
            operationId: "ffeeddccbbaa99887766554433221100",
            expectedRuntime: identity,
            expectedHealthEpoch: 1
        )
        let commit = ManagementCommitRequest(
            operationId: "ffeeddccbbaa99887766554433221100",
            expectedRuntime: identity,
            expectedHealthEpoch: 1,
            quiesceEpoch: 1,
            token: "00112233445566778899aabbccddeeff"
        )
        var result: [String: Data] = [
            "quiesce-request.json": try ManagementRequestCodec.encode(quiesce),
            "commit-request.json": try ManagementRequestCodec.encode(commit),
            "abort-request.json": try ManagementRequestCodec.encode(commit),
        ]
        for state in [
            RuntimeSupervisorState.idle, .pending, .quiescing, .restarting, .restartFailed,
        ] {
            let name = "supervisor-state-\(state.rawValue).json"
            result[name] = try ManagementRequestCodec.encode(
                RuntimeSupervisorObservation(supervisorState: state)
            )
        }
        return result
    }

    func testCommittedFixturesAreExactMacProducerBytes() throws {
        for (name, produced) in try producedFixtures() {
            XCTAssertEqual(
                try Data(contentsOf: fixtureRoot.appendingPathComponent(name)),
                produced,
                "\(name) must be regenerated from the Mac producer"
            )
        }
    }

    func testGenerateObservedFixtures() throws {
        guard ProcessInfo.processInfo.environment["CCCODEGEN_FIXTURES"] == "1" else {
            throw XCTSkip("set CCCODEGEN_FIXTURES=1 to write fixtures")
        }
        for (name, produced) in try producedFixtures() {
            try produced.write(to: fixtureRoot.appendingPathComponent(name), options: .atomic)
        }
    }
}
