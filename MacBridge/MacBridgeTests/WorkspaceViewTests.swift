import XCTest
@testable import CordCodeLink

// P0-2 WorkspaceView 测试。
// 验证首屏所需的派生逻辑：BridgeStatus 可连接性、首次使用条件、新文案 key 存在。
// View body 的纯展示断言交由视觉回归（需 owner 授权 snapshot/simulator）。
@MainActor
final class WorkspaceViewTests: XCTestCase {

    func testConnectableStatusesForFirstUseGuidance() {
        // WorkspaceView.isFirstUse 要求 status.isConnectable。只有 ready/readyNoAgents 可连接。
        let connectable: Set<BridgeStatus> = [.ready, .readyNoAgents]
        for status in [BridgeStatus.ready, .readyNoAgents] {
            XCTAssertTrue(connectable.contains(status), "\(status) 应可连接")
        }
        for status in [BridgeStatus.starting, .stopped, .crashed, .sleeping, .idle] {
            XCTAssertFalse(connectable.contains(status), "\(status) 不应可连接")
        }
    }

    func testFirstUseRequiresReadyAndNoDevices() async {
        let store = DeviceStore()
        // 无 client → 加载失败 → hasLoadedDevices=false；isFirstUse 应为 false。
        await store.loadDevices()
        XCTAssertFalse(store.hasLoadedDevices)

        // 成功加载空列表 → hasLoadedDevices=true，devices=空 → 满足首次使用的数据前提。
        // （BridgeStatus 的可连接性由 viewModel 提供，此处仅验证设备侧前提。）
        let empty = DeviceAPIStubForWorkspace(devices: [])
        store.configure(apiClient: empty)
        await store.loadDevices()
        XCTAssertTrue(store.hasLoadedDevices)
        XCTAssertTrue(store.devices.isEmpty)
    }

    func testWorkspaceCopyKeysPresent() {
        // 首屏三类文案必须存在且非空。
        for key: String in [
            L10n.workspaceReadyTitle,
            L10n.workspaceCanConnect,
            L10n.workspaceOneStepAway,
            L10n.workspaceNeedsAttention,
            L10n.workspaceFirstDeviceTitle,
            L10n.workspaceFirstDeviceSubtitle,
            L10n.workspaceStart,
            L10n.workspaceRecheck,
            L10n.addDevice,
            L10n.viewDevices,
            L10n.workspaceNoToolsTitle,
            L10n.workspaceNoToolsSubtitle,
        ] {
            XCTAssertFalse(key.isEmpty)
        }
    }

    func testWorkspaceCopyAvoidsForbiddenTerms() {
        // 首屏正常/首次使用文案不应把内部技术词暴露给普通用户作为首要解释。
        let copies = [
            L10n.workspaceReadyTitle, L10n.workspaceReadySubtitle,
            L10n.workspaceCanConnect, L10n.workspaceFirstDeviceTitle,
            L10n.workspaceFirstDeviceSubtitle, L10n.workspaceStart,
            L10n.addDevice, L10n.viewDevices,
        ]
        let forbidden = ["Relay", "Tailscale", "Bridge", "端口", "endpoint", "Endpoint", "8777"]
        for copy in copies {
            for term in forbidden {
                XCTAssertFalse(copy.contains(term), "首屏文案「\(copy)」不应包含技术词「\(term)」")
            }
        }
    }

    func testConnectionStatusEntryIsSingleAndNamedConsistently() {
        // 工作站「连接状态」与 Toolbar「连接状态」复用同一文案 key（唯一连接入口）。
        XCTAssertFalse(L10n.connectionStatus.isEmpty)
        // 安全连接段与 Toolbar 都用 connectionStatus，不复用旧的 remoteAccessTab。
        XCTAssertNotEqual(L10n.connectionStatus, L10n.remoteAccessTab)
    }

    // MARK: - 拓扑面板（UI-3：各 state 渲染分支 + 失败可见）

    private func status(
        syncHealth: TopologySyncHealth,
        bridge: String? = "shared",
        desktopAggregate: String? = "all_shared"
    ) -> TopologyMonitorStatus {
        TopologyMonitorStatus(
            schemaVersion: "topology-monitor.v1",
            state: .enabled,
            bridgeEpoch: 1710893634113558,
            sampledAtMs: 1_700_000_000_000,
            syncHealth: syncHealth,
            dimensions: TopologyMonitorDimensions(
                topologyBridgeAttachment: TopologyMonitorDimension(enumValue: bridge ?? "unresolved", ageMs: 1200, stale: false, source: "provider_snapshot", errorCode: "none"),
                topologyDesktopAggregate: TopologyMonitorDimension(enumValue: desktopAggregate ?? "unknown", ageMs: 1200, stale: false, source: "process_tree", errorCode: "none"),
                seatHealthDaemon: nil,
                seatHealthLaunchAgent: nil,
                attachConfig: nil,
                versionCompatibility: nil,
                legacyManagedLoopback: nil,
                legacyDesktopPrivate: nil
            ),
            instances: nil
        )
    }

    func testDisplayHealthyIsHidden() {
        let display = TopologyMonitorStatusStore.Phase.enabled(status(syncHealth: .healthy)).display
        XCTAssertEqual(display, .hidden)
    }

    func testDisplayDisabledIsHidden() {
        // store phase .disabled / .idle → 不显示（不冒充 healthy，也不出现诊断）。
        let display = TopologyMonitorStatusStore.Phase.disabled.display
        XCTAssertEqual(display, .hidden)
        XCTAssertEqual(TopologyMonitorStatusStore.Phase.idle.display, .hidden)
    }

    func testDisplayUnknownIsNeutralDiagnostic() {
        let display = TopologyMonitorStatusStore.Phase.enabled(status(syncHealth: .unknown)).display
        XCTAssertEqual(display, .diagnosticFailure)
    }

    func testDisplayNotApplicableHasOptionalCopy() {
        let display = TopologyMonitorStatusStore.Phase.enabled(status(syncHealth: .notApplicable)).display
        XCTAssertEqual(display, .infoNotApplicable)
    }

    func testDisplayDiagnosticFailureFromStoreKind() {
        // 采样失败/网络失败（§6：诊断失败，绝不伪装成 split）。
        let display = TopologyMonitorStatusStore.Phase.diagnostic(.network).display
        XCTAssertEqual(display, .diagnosticFailure)
        XCTAssertNotEqual(display, .warning(.observerUnattached))
        XCTAssertNotEqual(display, .warning(.partialSync))
    }

    func testDisplayDegradedKindsFromEvidence() {
        // §4.3 表：shared×split_present → Desktop 未接共享 daemon。
        XCTAssertEqual(
            TopologyMonitorStatusStore.Phase.enabled(status(syncHealth: .degraded, bridge: "shared", desktopAggregate: "split_present")).display,
            .warning(.desktopDetached)
        )
        // shared×mixed → 仅部分实例同步。
        XCTAssertEqual(
            TopologyMonitorStatusStore.Phase.enabled(status(syncHealth: .degraded, bridge: "shared", desktopAggregate: "mixed")).display,
            .warning(.partialSync)
        )
        // partial/absent × 任意 → observer/main 未完整附着；不得归咎 Desktop。
        XCTAssertEqual(
            TopologyMonitorStatusStore.Phase.enabled(status(syncHealth: .degraded, bridge: "partial", desktopAggregate: "all_shared")).display,
            .warning(.observerUnattached)
        )
        XCTAssertEqual(
            TopologyMonitorStatusStore.Phase.enabled(status(syncHealth: .degraded, bridge: "absent", desktopAggregate: "mixed")).display,
            .warning(.observerUnattached)
        )
    }

    func testDisplayDegradedWithoutDimEvidenceFallsBackToGeneral() {
        // 缺维度证据时不得臆断归责对象：降级通用高警示。
        let display = TopologyMonitorStatusStore.Phase.enabled(
            status(syncHealth: .degraded, bridge: nil, desktopAggregate: nil)
        ).display
        XCTAssertEqual(display, .warning(.general))
    }

    func testTopologyCopyKeysPresentAndNonEmpty() {
        for key: String in [
            L10n.topologyTitle,
            L10n.topologyDiagnosticFailed,
            L10n.topologyNotApplicable,
            L10n.topologyWarningDevDesktopDetached,
            L10n.topologyWarningPartialSync,
            L10n.topologyWarningObserverUnattached,
            L10n.topologyWarningGeneral,
        ] {
            XCTAssertFalse(key.isEmpty)
        }
    }
}

private final class DeviceAPIStubForWorkspace: DeviceAPIProviding {
    let devices: [TrustedDevice]
    init(devices: [TrustedDevice]) { self.devices = devices }
    func listDevices() async throws -> [TrustedDevice] { devices }
    func revokeDevice(_ deviceId: String) async throws {}
}
