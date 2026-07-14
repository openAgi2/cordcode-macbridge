import XCTest
@testable import CordCodeLink

// P1-2 连接状态 Sheet 测试：验证单页结构契约与默认/高级分层。
// RemoteAccessView 的真实保存/配置行为由既有逻辑承载，此处只锁定 IA 契约（不再二级导航）。
@MainActor
final class ConnectionStatusSheetTests: XCTestCase {

    func testConnectionSheetHasStablePresentableSize() {
        XCTAssertEqual(LayoutConstants.connectionSheetWidth, 760)
        XCTAssertEqual(LayoutConstants.connectionSheetHeight, 580)
    }

    func testConnectionSectionCopyKeysPresent() {
        XCTAssertFalse(L10n.connectionStatusSecureRemote.isEmpty)
        XCTAssertFalse(L10n.connectionStatusSecureRemoteHint.isEmpty)
        XCTAssertFalse(L10n.connectionStatusLocalNetwork.isEmpty)
        XCTAssertFalse(L10n.connectionStatusLocalNetworkHint.isEmpty)
        XCTAssertFalse(L10n.connectionStatusAdvanced.isEmpty)
        XCTAssertFalse(L10n.connectionStatusShowAdvanced.isEmpty)
        XCTAssertFalse(L10n.connectionStatusHideAdvanced.isEmpty)
    }

    func testAdvancedToggleCopyIsActionPair() {
        // 「显示高级选项」与「隐藏高级选项」是同一动作的两种状态，互斥。
        XCTAssertNotEqual(L10n.connectionStatusShowAdvanced, L10n.connectionStatusHideAdvanced)
    }

    func testConnectionCopyDescribesAutoNotMutuallyExclusive() {
        // Relay/LAN 文案应表达“自动路径”，不暗示用户必须在二者间选择。
        let remoteHint = L10n.connectionStatusSecureRemoteHint.lowercased()
        let localHint = L10n.connectionStatusLocalNetworkHint.lowercased()
        XCTAssertTrue(remoteHint.contains("relay") || remoteHint.contains("relay".uppercased()),
                      "安全远程文案应说明 Relay 自动保持连接")
        XCTAssertTrue(localHint.contains("wi-fi") || localHint.contains("自动"),
                      "本地网络文案应说明自动使用更快直连")
    }

    func testConnectionSectionCopyAvoidsScaryTerms() {
        // 面向用户的连接说明不应把端口/endpoint 作为首要描述。
        let copies = [
            L10n.connectionStatusSecureRemote,
            L10n.connectionStatusSecureRemoteHint,
            L10n.connectionStatusLocalNetwork,
            L10n.connectionStatusLocalNetworkHint,
        ]
        let forbidden = ["8777", "endpoint", "Endpoint"]
        for copy in copies {
            for term in forbidden {
                XCTAssertFalse(copy.contains(term), "连接文案「\(copy)」不应包含技术词「\(term)」")
            }
        }
    }
}
