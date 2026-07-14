import XCTest
@testable import CordCodeLink

// P0-1 信息架构测试：Sidebar 只保留日常任务，技术后台改为原生窗口/Sheet。
// 锁定 NavigationTab 收敛为两个 case，以及新文案 key 存在且非内部技术语言。
final class SidebarIAInformationArchitectureTests: XCTestCase {

    func testSidebarHasOnlyTwoDailyTabs() {
        // 只保留日常任务：工作站、设备。remoteAccess/settings/diagnostics 不再是一级 sidebar 项。
        let cases = NavigationTab.allCases.map(\.rawValue)
        XCTAssertEqual(Set(cases), Set(["workspace", "devices"]), "Sidebar 应只保留 workstation 与 devices")
        XCTAssertEqual(NavigationTab.allCases.count, 2)
    }

    func testEverySidebarTabHasTitleAndImage() {
        for tab in NavigationTab.allCases {
            XCTAssertFalse(tab.title.isEmpty, "\(tab.rawValue) 缺少标题")
            XCTAssertFalse(tab.systemImage.isEmpty, "\(tab.rawValue) 缺少 SF Symbol")
        }
    }

    func testRemovedTabsNoLongerExist() {
        // 这些原 sidebar 项应已从枚举移除（它们改为 Toolbar/原生 Settings）。
        let raws = Set(NavigationTab.allCases.map(\.rawValue))
        XCTAssertFalse(raws.contains("overview"))
        XCTAssertFalse(raws.contains("remoteAccess"))
        XCTAssertFalse(raws.contains("settings"))
        XCTAssertFalse(raws.contains("diagnostics"))
    }

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
