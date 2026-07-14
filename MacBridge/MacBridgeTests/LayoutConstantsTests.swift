import XCTest
@testable import CordCodeLink

// P0-3 布局契约测试：PageContainer 不再硬编码 820pt，窗口最小宽度提升至 920pt。
// 这些断言锁定 UX 重设计报告 P0-3 的工程前提，防止后续 P1/P2 局部改动绕过全局容器限制。
final class LayoutConstantsTests: XCTestCase {

    func testWindowMinimumMeetsP0Contract() {
        XCTAssertEqual(LayoutConstants.minWindowWidth, 1280, "P0-3: 主窗口最小宽度必须为 1280pt")
        XCTAssertGreaterThanOrEqual(LayoutConstants.minWindowHeight, 560, "最小高度不低于 560pt")
    }

    func testWorkColumnWidthMatchesP0Contract() {
        XCTAssertEqual(LayoutConstants.workColumnWidth, 880, "P0-3: 默认工作列最大宽度必须为 880pt")
    }

    func testWorkColumnFitsInsideMinWindow() {
        // 工作列必须在最小窗口内放得下（留出 sidebar 与 padding 的余量）。
        // sidebar 180 + 左右 padding，工作列 880 应 < 最小窗口 920。
        XCTAssertLessThan(
            LayoutConstants.workColumnWidth,
            LayoutConstants.minWindowWidth,
            "工作列宽度必须小于最小窗口宽度"
        )
    }

    func testWideSecondaryThresholdExceedsWorkColumn() {
        // 只有窗口宽于 workColumn 才有意义出现次级信息栏。
        XCTAssertGreaterThan(
            LayoutConstants.wideSecondaryThreshold,
            LayoutConstants.workColumnWidth,
            "次级信息栏阈值必须大于工作列宽度"
        )
    }

    func testSheetWidthsAreBounded() {
        // 连接状态 Sheet 支持双栏内容，配对工作区保持更聚焦。
        XCTAssertGreaterThan(
            LayoutConstants.connectionSheetWidth,
            LayoutConstants.workColumnWidth,
            "连接状态 Sheet 应可容纳比工作列更宽的双栏内容"
        )
        XCTAssertLessThan(
            LayoutConstants.pairingSheetWidth,
            LayoutConstants.connectionSheetWidth,
            "配对工作区应比连接状态 Sheet 更聚焦"
        )
        XCTAssertGreaterThanOrEqual(
            LayoutConstants.pairingSheetHeight,
            600,
            "配对 Sheet 必须容纳二维码与流程说明"
        )
    }

    // 第二轮 r5 宽屏内容宽度契约
    func testPageHorizontalPadding() {
        XCTAssertEqual(LayoutConstants.pageHorizontalPadding, 30)
    }

    func testWorkspaceContentThresholds() {
        XCTAssertEqual(LayoutConstants.workspaceWideContentThreshold, 1164)
        XCTAssertEqual(LayoutConstants.workspacePreferredContentWidth, 1204)
    }

    func testWorkspaceContainerWidthsIncludePadding() {
        let padding = LayoutConstants.pageHorizontalPadding
        XCTAssertEqual(LayoutConstants.workspaceWideContainerWidth, 1164 + 2 * padding)
        XCTAssertEqual(LayoutConstants.workspaceMaxContainerWidth, 1204 + 2 * padding)
    }

    func testFocusedWorkspaceContainerStaysBetweenDefaultAndWideLayouts() {
        XCTAssertGreaterThan(
            LayoutConstants.workspaceFocusedContainerWidth,
            LayoutConstants.workColumnWidth,
            "首页聚焦列需要比默认工作列更宽，容纳设备与工具状态"
        )
        XCTAssertLessThan(
            LayoutConstants.workspaceFocusedContainerWidth,
            LayoutConstants.workspaceMaxContainerWidth,
            "首页不能采用全宽工作区的最大容器，避免状态行被横向拉散"
        )
    }

    func testDualColumnSizes() {
        XCTAssertEqual(LayoutConstants.dualColumnMainMin, 900)
        XCTAssertEqual(LayoutConstants.dualColumnInspectorMin, 240)
        XCTAssertEqual(LayoutConstants.dualColumnMainPreferred, 920)
        XCTAssertEqual(LayoutConstants.dualColumnInspectorPreferred, 260)
    }
}
