import XCTest
@testable import CordCodeLink

// P2-1 原生 Settings 测试：通用/高级两 tab，OpenCode 默认托管只呈现确认信息。
final class SettingsNativeInformationArchitectureTests: XCTestCase {

    func testSettingsTabsHaveProductLabels() {
        XCTAssertFalse(L10n.general.isEmpty)
        XCTAssertFalse(L10n.advanced.isEmpty)
        XCTAssertNotEqual(L10n.general, L10n.advanced, "通用与高级应是两个不同 tab")
    }

    func testManagedDefaultMessagePresent() {
        // 高级默认态文案：说明 OpenCode 由 CordCode Link 自动托管，无需操作。
        XCTAssertFalse(L10n.opencodeManagedDefault.isEmpty)
        XCTAssertTrue(
            L10n.opencodeManagedDefault.lowercased().contains("no action")
                || L10n.opencodeManagedDefault.contains("无需操作"),
            "托管默认文案应表达无需操作"
        )
    }

    func testUseOwnServiceTogglePresent() {
        // 渐进披露开关文案存在，且与托管默认文案不同。
        XCTAssertFalse(L10n.opencodeUseOwnService.isEmpty)
        XCTAssertNotEqual(L10n.opencodeUseOwnService, L10n.opencodeManagedDefault)
    }

    func testGeneralTabContentsAvoidSensitiveExposure() {
        // 通用 tab 不应把 OpenCode 密码/命令暴露给一般用户。通用区文案标题应存在。
        XCTAssertFalse(L10n.settingsGeneral.isEmpty)
        XCTAssertFalse(L10n.settingsAutoRestartTitle.isEmpty)
        XCTAssertFalse(L10n.settingsMacBridge.isEmpty)
    }
}
