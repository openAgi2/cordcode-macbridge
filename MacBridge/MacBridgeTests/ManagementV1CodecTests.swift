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

    func testRequestEncodingUsesExactV1Keys() throws {
        let identity = ManagementRuntimeIdentity(pid: 12345, bridgeEpoch: 1)
        let request = ManagementQuiesceRequest(
            operationId: "ffeeddccbbaa99887766554433221100", expectedRuntime: identity, expectedHealthEpoch: 1
        )
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: ManagementRequestCodec.encode(request)) as? [String: Any])
        XCTAssertEqual(Set(object.keys), ["managementSchemaVersion", "operationId", "expectedRuntime", "expectedHealthEpoch"])
    }
}
