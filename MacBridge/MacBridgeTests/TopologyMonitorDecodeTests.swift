import XCTest
@testable import CordCodeLink

/// Swift mirror fixture 解码测试（implementation plan v2 §2.4：与 Go 侧
/// go-bridge/testdata/topology_snapshot.json 成对冻结）。
///
/// 契约（§2.4）：
/// - disabled 快照仅 {schemaVersion, state, bridgeEpoch, sampledAtMs} → 独立 .disabled 分支。
/// - 决策层 enum（state/syncHealth/bridge/aggregate）未知值 decode fail-closed → .unknown，
///   绝不默认 healthy。
/// - 维度 enum 是 wire 原始证据（enumValue: String），原样呈现不吞掉。
/// - 未知字段忽略（与 Go 侧一致）。
final class TopologyMonitorDecodeTests: XCTestCase {
    /// 与 go-bridge/testdata/topology_snapshot.json 内容一致的 mirror（pair-frozen）。
    private let enabledFixture = """
    {
      "schemaVersion": "topology-monitor.v1",
      "state": "enabled",
      "bridgeEpoch": 1710893634113558,
      "sampledAtMs": 1710893634113558,
      "syncHealth": "healthy",
      "dimensions": {
        "topologyBridgeAttachment": { "enum": "shared", "ageMs": 1200, "stale": false, "source": "provider_snapshot", "errorCode": "none" },
        "topologyDesktopAggregate":  { "enum": "all_shared", "ageMs": 1200, "stale": false, "source": "process_tree", "errorCode": "none" },
        "seatHealthDaemon":         { "enum": "running", "ageMs": 1200, "stale": false, "source": "version_probe", "errorCode": "none" },
        "seatHealthLaunchAgent":    { "enum": "healthy", "ageMs": 1200, "stale": false, "source": "launchd_probe", "errorCode": "none" },
        "attachConfig":             { "enum": "enabled", "ageMs": 1200, "stale": false, "source": "launchd_probe", "errorCode": "none" },
        "versionCompatibility":     { "enum": "effective_compatible", "ageMs": 1200, "stale": false, "source": "version_probe", "errorCode": "none" },
        "legacyManagedLoopback":    { "enum": "absent", "ageMs": 1200, "stale": false, "source": "process_tree", "errorCode": "none" },
        "legacyDesktopPrivate":     { "enum": "absent", "ageMs": 1200, "stale": false, "source": "process_tree", "errorCode": "none" }
      },
      "instances": [
        { "pid": 4242, "startTime": "2026-08-24T17:00:00Z", "classification": "shared_only",
          "evidence": [ { "kind": "shared_fd", "state": "confirmed" }, { "kind": "private_stdio", "state": "unavailable" } ] }
      ]
    }
    """

    /// 与 go-bridge/testdata/topology_snapshot_disabled.json 一致的 mirror。
    private let disabledFixture = """
    {
      "schemaVersion": "topology-monitor.v1",
      "state": "disabled",
      "bridgeEpoch": 1710893634113558,
      "sampledAtMs": 1710893634113558
    }
    """

    private func decode(_ json: String) throws -> TopologyMonitorStatus {
        try TopologyMonitorStatusCodec.decode(Data(json.utf8))
    }

    func testEnabledMirrorDecodes() throws {
        let status = try decode(enabledFixture)
        XCTAssertEqual(status.state, .enabled)
        XCTAssertEqual(status.syncHealth, .healthy)
        XCTAssertEqual(status.bridgeEpoch, 1710893634113558)
        XCTAssertEqual(status.sampledAtMs, 1710893634113558)

        let dims = try XCTUnwrap(status.dimensions)
        XCTAssertEqual(dims.topologyBridgeAttachment?.enumValue, "shared")
        XCTAssertEqual(dims.topologyBridgeAttachment?.ageMs, 1200)
        XCTAssertEqual(dims.topologyBridgeAttachment?.stale, false)
        XCTAssertEqual(dims.topologyDesktopAggregate?.enumValue, "all_shared")
        XCTAssertEqual(dims.seatHealthDaemon?.enumValue, "running")
        XCTAssertEqual(dims.seatHealthLaunchAgent?.enumValue, "healthy")
        XCTAssertEqual(dims.attachConfig?.enumValue, "enabled")
        XCTAssertEqual(dims.versionCompatibility?.enumValue, "effective_compatible")
        XCTAssertEqual(dims.legacyManagedLoopback?.enumValue, "absent")
        XCTAssertEqual(dims.legacyDesktopPrivate?.enumValue, "absent")

        let instances = try XCTUnwrap(status.instances)
        XCTAssertEqual(instances.count, 1)
        XCTAssertEqual(instances[0].pid, 4242)
        XCTAssertEqual(instances[0].classification, "shared_only")
        XCTAssertEqual(instances[0].evidence?.count, 2)
        XCTAssertEqual(instances[0].evidence?[0].kind, "shared_fd")
        XCTAssertEqual(instances[0].evidence?[0].state, "confirmed")
        // unavailable = 采样失败，必须原样保留（绝不作 negative 资产）。
        XCTAssertEqual(instances[0].evidence?[1].state, "unavailable")
    }

    func testDisabledMirrorDecodes() throws {
        let status = try decode(disabledFixture)
        // state=disabled 是独立分支，与解码失败/网络失败严格分开。
        XCTAssertEqual(status.state, .disabled)
        XCTAssertNil(status.syncHealth)
        XCTAssertNil(status.dimensions)
    }

    func testUnknownSyncHealthFailsClosedToUnknown() throws {
        let json = enabledFixture.replacingOccurrences(of: "\"healthy\"", with: "\"super_healthy\"")
        let status = try decode(json)
        // fail-closed：未知 syncHealth 视为诊断失败，绝不默认 healthy。
        XCTAssertEqual(status.syncHealth, .unknown)
    }

    func testUnknownTopLevelStateIsUnknown() throws {
        let json = disabledFixture.replacingOccurrences(of: "\"disabled\"", with: "\"on\"")
        let status = try decode(json)
        XCTAssertEqual(status.state, .unknown)
    }

    func testUnknownDimEnumKeptAsRawEvidence() throws {
        // 维度 enum 是 wire 证据：未知值原样保留（不吞掉、不默认 healthy），
        // 决策层 syncHealth 只认 wire 值本身。
        let json = enabledFixture.replacingOccurrences(of: "\"shared\"", with: "\"magic\"")
        let status = try decode(json)
        XCTAssertEqual(status.dimensions?.topologyBridgeAttachment?.enumValue, "magic")
        XCTAssertEqual(status.syncHealth, .healthy)
    }

    func testDecisionEnumsFailsClosedOnDirectDecode() throws {
        // 两个决策维度 enum 的 `init(from:)` 直接 fail-closed（UI 层用它们解释证据）。
        func decodeEnum<T: Decodable>(_ type: T.Type, _ raw: String) -> T {
            try! JSONDecoder().decode(type, from: Data("\"\(raw)\"".utf8))
        }
        XCTAssertEqual(decodeEnum(TopologyBridgeAttachment.self, "magic"), .unknown)
        XCTAssertEqual(decodeEnum(TopologyBridgeAttachment.self, "shared"), .shared)
        XCTAssertEqual(decodeEnum(TopologyDesktopAggregate.self, "magic"), .unknown)
        XCTAssertEqual(decodeEnum(TopologyDesktopAggregate.self, "desktop_absent"), .desktopAbsent)
        XCTAssertEqual(decodeEnum(TopologySyncHealth.self, "not_applicable"), .notApplicable)
    }

    func testMissingStateThrows() {
        // 缺失 state 是形状损坏 → decode 抛错 → store 归为诊断失败。
        let json = enabledFixture.replacingOccurrences(
            of: "\"state\": \"enabled\",", with: ""
        )
        XCTAssertThrowsError(try decode(json))
    }

    func testMissingSyncHealthDecodesNil() throws {
        // enabled 快照缺 syncHealth：解码不抛（optional），决定权交给 store（→ 诊断失败）。
        let json = enabledFixture.replacingOccurrences(
            of: "\"syncHealth\": \"healthy\",", with: ""
        )
        let status = try decode(json)
        XCTAssertNil(status.syncHealth)
    }

    func testUnknownFieldsIgnored() throws {
        let json = enabledFixture.replacingOccurrences(
            of: "\"schemaVersion\": \"topology-monitor.v1\",",
            with: "\"schemaVersion\": \"topology-monitor.v1\", \"unexpectedField\": true,"
        )
        let status = try decode(json)
        XCTAssertEqual(status.syncHealth, .healthy)
    }

    func testStaleDimensionDecodes() throws {
        let json = enabledFixture.replacingOccurrences(
            of: "\"enum\": \"all_shared\", \"ageMs\": 1200, \"stale\": false",
            with: "\"enum\": \"unknown\", \"ageMs\": 124000, \"stale\": true"
        )
        let status = try decode(json)
        let dim = status.dimensions?.topologyDesktopAggregate
        XCTAssertEqual(dim?.enumValue, "unknown")
        XCTAssertEqual(dim?.stale, true)
        XCTAssertEqual(dim?.ageMs, 124000)
    }
}
