import XCTest
@testable import CordCodeLink

// Relay-first + opt-in LAN 连接状态 Sheet 测试（2026-08-01 重写）。
// 旧版断言「单页 + 显示/隐藏高级」IA 的 L10n key，那是被 sidebar IA 早已取代的过期契约。
// 本版锁定新 IA：常驻连接信息卡、§3.3 状态真实性（connected==true 才显示「已接入中继网」）、
// 以及 control-plane wire model（preferLocalNetwork + relay.connected/enabled）可解码。
@MainActor
final class ConnectionStatusSheetTests: XCTestCase {

    func testConnectionSheetHasStablePresentableSize() {
        XCTAssertEqual(LayoutConstants.connectionSheetWidth, 760)
        XCTAssertEqual(LayoutConstants.connectionSheetHeight, 740)
    }

    /// wire model:control-plane 字段可解码。preferLocalNetwork + relay.connected/enabled 都是可选，
    /// 旧 go-bridge 响应缺字段时解码为 nil（消费侧按 false / 未连接处理）。
    func testRemoteStatusDecodesConnectionPolicyAndRelayConnected() throws {
        let json = """
        {
          "preferLocalNetwork": true,
          "relay": {"configured": true, "enabled": true, "connected": true}
        }
        """.data(using: .utf8)!
        let status = try JSONDecoder().decode(RemoteStatus.self, from: json)
        XCTAssertEqual(status.preferLocalNetwork, true)
        XCTAssertEqual(status.relay?.configured, true)
        XCTAssertEqual(status.relay?.enabled, true)
        XCTAssertEqual(status.relay?.connected, true)
    }

    /// wire model:缺字段的旧 payload 解码为 nil,消费侧按 false / 未连接处理(不冒充已连接)。
    func testRemoteStatusLegacyPayloadDecodesNilPolicyAndConnected() throws {
        let json = """
        { "relay": {"configured": true} }
        """.data(using: .utf8)!
        let status = try JSONDecoder().decode(RemoteStatus.self, from: json)
        XCTAssertNil(status.preferLocalNetwork, "旧 payload 缺 preferLocalNetwork 应解码为 nil")
        XCTAssertEqual(status.relay?.configured, true)
        XCTAssertNil(status.relay?.connected, "缺 connected 应解码为 nil——不得从 configured 推导为已连接")
        XCTAssertNil(status.relay?.enabled)
    }

    /// 源码守卫:§3.3 状态真实性——视图用 connected-aware helper,而非把 configured 冒充为已接入。
    private static var remoteAccessViewSource: String {
        let testFile = URL(fileURLWithPath: #filePath)
        let viewFile = testFile
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("MacBridge/Views/RemoteAccessView.swift")
        return (try? String(contentsOf: viewFile, encoding: .utf8)) ?? ""
    }

    func testViewSourceUsesConnectedAwareRelayStatus() {
        let src = Self.remoteAccessViewSource
        XCTAssertFalse(src.isEmpty, "应能定位 RemoteAccessView.swift 源码")
        XCTAssertTrue(src.contains("relayConnected"), "应凭 RelayStatus.connected==true 判定真实连接")
        XCTAssertTrue(src.contains("relayConnectionStateText"), "连接信息卡应使用 connected-aware 状态文案")
        XCTAssertTrue(src.contains("已接入中继网"), "connected==true 时应显示「已接入中继网」")
    }
}
