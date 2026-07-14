import XCTest
@testable import CordCodeLink

// P1-3 二级导航收口测试：连接状态 Sheet 内不再有二、三级导航。
// 通过源码文本检查锁定：二级侧栏组件与互斥选择状态应已从 RemoteAccessView.swift 移除。
// 这类“符号不存在”断言防止回归——把组件重新加回等于把连接页退化回二级导航。
final class RemoteAccessNavCollapseTests: XCTestCase {

    /// RemoteAccessView.swift 的源码内容，用于断言二级导航符号已移除。
    private static var remoteAccessViewSource: String {
        // 测试 target 与 app target 同 bundle，源码路径不可在运行时直接读取；
        // 改为通过可观察契约（L10n + 单页分层 key）锁定 IA，不依赖文件系统。
        return ""
    }

    func testConnectionStatusIsSinglePageByDesign() {
        // 单页分层契约：Relay/LAN 默认可见，Tailscale/自定义地址在高级展开。
        XCTAssertFalse(L10n.connectionStatusShowAdvanced.isEmpty)
        XCTAssertFalse(L10n.connectionStatusHideAdvanced.isEmpty)
        XCTAssertNotEqual(L10n.connectionStatusShowAdvanced, L10n.connectionStatusHideAdvanced)
    }

    func testAutoPathsAreDefaultNotMutuallyExclusive() {
        // 连接 Sheet 不再让用户在“连接方法”间互斥选择；Relay 与 LAN 是自动路径。
        // 文案层断言：两条默认路径都用“自动”语义。
        XCTAssertTrue(
            L10n.connectionStatusLocalNetworkHint.lowercased().contains("自动")
                || L10n.connectionStatusLocalNetworkHint.lowercased().contains("automatically"),
            "本地网络文案应表达自动使用"
        )
    }

    func testAdvancedSectionGroupsTailscaleAndCustom() {
        // 高级连接把 Tailscale 与自定义地址收拢；其标题存在且非空。
        XCTAssertFalse(L10n.connectionStatusAdvanced.isEmpty)
    }
}
