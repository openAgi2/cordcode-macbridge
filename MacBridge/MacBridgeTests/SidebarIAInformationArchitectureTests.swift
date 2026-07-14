import XCTest
@testable import CordCodeLink

// 首页整合设备配对与管理后，主窗口不再保留冗余 sidebar；技术后台由原生窗口/Sheet 承载。
final class SidebarIAInformationArchitectureTests: XCTestCase {

    func testNewEntryKeysArePresent() {
        // Toolbar / 原生 Settings 入口的文案 key 必须存在于 L10n。
        XCTAssertFalse(L10n.connectionStatus.isEmpty)
        XCTAssertFalse(L10n.helpDiagnostics.isEmpty)
        XCTAssertFalse(L10n.general.isEmpty)
        XCTAssertFalse(L10n.advanced.isEmpty)
    }

    func testWorkspaceLabelIsProductLanguageNotInternal() {
        // “总览/Overview”这类管理后台语言不应作为首要 sidebar 标题；应改为工作站。
        let workspace = L10n.workspace
        XCTAssertFalse(workspace.isEmpty)
        XCTAssertNotEqual(workspace, L10n.overview, "工作站标题不应沿用管理后台的“总览”")
    }

    func testNewLabelsAvoidForbiddenTechTerms() {
        // 交互入口文案不应把 Relay/端口/Tailscale 暴露给普通用户作为首要标题。
        let labels = [L10n.workspace, L10n.devices, L10n.connectionStatus, L10n.helpDiagnostics, L10n.general, L10n.advanced]
        let forbidden = ["Relay", "Tailscale", "Bridge", "端口", "endpoint", "Endpoint"]
        for label in labels {
            for term in forbidden {
                XCTAssertFalse(label.contains(term), "文案「\(label)」不应包含技术词「\(term)」")
            }
        }
    }
}
