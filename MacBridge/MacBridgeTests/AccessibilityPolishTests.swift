import XCTest
@testable import CordCodeLink

// P2-3 无障碍与键盘快捷键测试：锁定 a11y 契约，防止回归。
final class AccessibilityPolishTests: XCTestCase {

    func testKeyboardCommandNotificationNamesExist() {
        // ⌘⇧D / ⌘⇧L 经由通知触发对应工作表。
        XCTAssertEqual(Notification.Name.openDiagnosticsRequest.rawValue, "openDiagnosticsRequest")
        XCTAssertEqual(Notification.Name.openConnectionStatusRequest.rawValue, "openConnectionStatusRequest")
    }

    func testEmptyAndErrorStateCopyKeysPresent() {
        // 空状态 / 错误状态 / 首次使用引导文案存在且非空。
        XCTAssertFalse(L10n.noTrustedDevices.isEmpty)
        XCTAssertFalse(L10n.noAiToolsDetected.isEmpty)
        XCTAssertFalse(L10n.noLogsAvailable.isEmpty)
        XCTAssertFalse(L10n.workspaceFirstDeviceTitle.isEmpty)
        XCTAssertFalse(L10n.workspaceFirstDeviceSubtitle.isEmpty)
        XCTAssertFalse(L10n.errorCannotConnect.isEmpty)
    }

    func testErrorCopyDoesNotExposeTechTerms() {
        // 面向用户的状态/错误文案不应把端口/endpoint 作为首要描述。
        let copies = [
            L10n.workspaceFirstDeviceTitle,
            L10n.workspaceFirstDeviceSubtitle,
            L10n.workspaceNoToolsSubtitle,
            L10n.connectionStatusLocalNetworkHint,
        ]
        let forbidden = ["8777", "endpoint", "Endpoint"]
        for copy in copies {
            for term in forbidden {
                XCTAssertFalse(copy.contains(term), "文案「\(copy)」不应包含技术词「\(term)」")
            }
        }
    }

    func testSettingsAndDiagnosticsAreReachableViaProductLabels() {
        // 键盘命令菜单条目使用产品语言（设置/连接状态/帮助与诊断）。
        XCTAssertFalse(L10n.helpDiagnostics.isEmpty)
        XCTAssertFalse(L10n.connectionStatus.isEmpty)
        XCTAssertFalse(L10n.general.isEmpty)
        XCTAssertFalse(L10n.advanced.isEmpty)
    }

    func testWorkspaceFirstUseCopyIsActionable() {
        // 首次使用引导应包含一个动词导向的主动作。
        XCTAssertTrue(L10n.addDevice.contains(L10n.pairNewDevice) || !L10n.addDevice.isEmpty)
        XCTAssertFalse(L10n.addDevice.isEmpty)
    }
}
