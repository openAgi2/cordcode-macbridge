import XCTest
@testable import CordCodeLink

final class ManagementV1CodecTests: XCTestCase {
    private func fixture(_ name: String) throws -> Data {
        let testFile = URL(fileURLWithPath: #filePath)
        let repo = testFile.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        return try Data(contentsOf: repo.appendingPathComponent("docs/protocol/samples/management-file-read/\(name)"))
    }

    func testObservedV0AndProposedV1StatusesDecode() throws {
        let v0 = try ManagementStatusCodec.decode(fixture("v0-status-observed.json"))
        XCTAssertNil(v0.v1)
        XCTAssertEqual(v0.status, "ready")

        for name in [
            "v1-status-accepting.json",
            "v1-status-quiescing.json",
            "v1-status-shuttingDown.json",
            "v1-status-degrading-accepting.json",
            "v1-status-degrading-quiescing.json",
            "v1-status-degrading-shuttingDown.json",
            "v1-status-degraded-accepting.json",
            "v1-status-degraded-quiescing.json",
            "v1-status-degraded-shuttingDown.json",
        ] {
            let status = try ManagementStatusCodec.decode(fixture(name))
            XCTAssertNotNil(status.v1, name)
            XCTAssertEqual(status.v1?.runtimeIdentity.pid, 12345, name)
            XCTAssertEqual(status.v1?.runtimeIdentity.bridgeEpoch, 1, name)
        }
    }

    func testAllRuntimeResultFixturesDecode() throws {
        let groups: [String: [String]] = [
            "quiesce": [
                "safe", "deferred", "identity-mismatch", "epoch-mismatch", "already-committed",
                "already-quiescing", "operation-reused", "token-generation-failed",
            ],
            "commit": [
                "committed", "already-committed", "identity-mismatch", "epoch-mismatch",
                "quiesce-mismatch", "token-mismatch", "lease-expired",
            ],
            "abort": [
                "aborted", "already-accepting", "already-committed", "identity-mismatch", "epoch-mismatch",
                "quiesce-mismatch", "token-mismatch", "lease-expired",
            ],
        ]
        for (group, outcomes) in groups {
            for outcome in outcomes {
                XCTAssertNoThrow(
                    try ManagementRuntimeResultCodec.decode(fixture("\(group)-result-\(outcome).json"), group: group),
                    "\(group)/\(outcome)"
                )
            }
        }
    }

    func testStrictStatusRejectsDuplicateUnknownAndExponentInteger() throws {
        let duplicate = Data(#"{"managementSchemaVersion":1,"managementSchemaVersion":1}"#.utf8)
        XCTAssertThrowsError(try ManagementStatusCodec.decode(duplicate))

        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: fixture("v1-status-accepting.json")) as? [String: Any])
        object["extra"] = true
        XCTAssertThrowsError(try ManagementStatusCodec.decode(JSONSerialization.data(withJSONObject: object)))

        let valid = String(decoding: try fixture("v1-status-accepting.json"), as: UTF8.self)
        let exponent = Data(valid.replacingOccurrences(of: "\"stateEpoch\":1", with: "\"stateEpoch\":1e0").utf8)
        XCTAssertThrowsError(try ManagementStatusCodec.decode(exponent))
    }

    // Owner 2026-08-28：claude 活跃 turn 不得禁用「重启共享 Codex 服务」——
    // activity 新增可选 byBackend breakdown，codex 门控只数 codex/codex-web。
    func testActivityByBackendScopesCodexRestartGate() throws {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: fixture("v1-status-accepting.json")) as? [String: Any])
        var activity = try XCTUnwrap(object["activity"] as? [String: Any])
        activity["bridgeOwnedActiveTurnsByBackend"] = ["claude": 2, "codex-web": 1]
        activity["pendingInteractionsByBackend"] = ["dsh": 3]
        object["activity"] = activity

        let status = try ManagementStatusCodec.decode(JSONSerialization.data(withJSONObject: object))
        let decoded = try XCTUnwrap(status.v1?.activity)
        XCTAssertEqual(decoded.bridgeOwnedActiveTurnsByBackend, ["claude": 2, "codex-web": 1])
        XCTAssertEqual(decoded.pendingInteractionsByBackend, ["dsh": 3])
        XCTAssertEqual(decoded.codexScopedActiveTurns, 1, "claude 的活跃 turn 不应计入 codex 门控")
        XCTAssertEqual(decoded.codexScopedPendingInteractions, 0, "dsh 的 pending 交互不应计入 codex 门控")

        activity["bridgeOwnedActiveTurnsByBackend"] = ["claude": 1, "codex": 2, "codex-web": 1]
        object["activity"] = activity
        let scoped = try XCTUnwrap(try ManagementStatusCodec.decode(JSONSerialization.data(withJSONObject: object)).v1?.activity)
        XCTAssertEqual(scoped.codexScopedActiveTurns, 3)
    }

    func testActivityWithoutByBackendFallsBackToGlobalCount() throws {
        // 当前 fixtures 已带 byBackend（真实 producer bytes）；剥掉这两个键模拟旧 runtime。
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: fixture("v1-status-accepting.json")) as? [String: Any])
        var activity = try XCTUnwrap(object["activity"] as? [String: Any])
        activity.removeValue(forKey: "bridgeOwnedActiveTurnsByBackend")
        activity.removeValue(forKey: "pendingInteractionsByBackend")
        object["activity"] = activity

        let status = try ManagementStatusCodec.decode(JSONSerialization.data(withJSONObject: object))
        let decoded = try XCTUnwrap(status.v1?.activity)
        XCTAssertNil(decoded.bridgeOwnedActiveTurnsByBackend)
        XCTAssertNil(decoded.pendingInteractionsByBackend)
        XCTAssertEqual(decoded.codexScopedActiveTurns, decoded.bridgeOwnedActiveTurns, "旧 runtime 无 breakdown 时保持保守的全局计数")
        XCTAssertEqual(decoded.codexScopedPendingInteractions, decoded.pendingInteractions)
    }

    func testRequestEncodingUsesExactV1Keys() throws {
        let identity = ManagementRuntimeIdentity(pid: 12345, bridgeEpoch: 1)
        let request = ManagementQuiesceRequest(
            operationId: "ffeeddccbbaa99887766554433221100", expectedRuntime: identity, expectedHealthEpoch: 1
        )
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: ManagementRequestCodec.encode(request)) as? [String: Any])
        XCTAssertEqual(Set(object.keys), ["managementSchemaVersion", "operationId", "expectedRuntime", "expectedHealthEpoch"])
    }
}
