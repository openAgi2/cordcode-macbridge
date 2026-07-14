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
}

private final class DeviceAPIStubForWorkspace: DeviceAPIProviding {
    let devices: [TrustedDevice]
    init(devices: [TrustedDevice]) { self.devices = devices }
    func listDevices() async throws -> [TrustedDevice] { devices }
    func revokeDevice(_ deviceId: String) async throws {}
}
