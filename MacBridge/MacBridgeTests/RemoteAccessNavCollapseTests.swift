import XCTest
@testable import CordCodeLink

// Relay-first + opt-in LAN IA 锁定测试（2026-08-01 重写）。
// 旧版断言的是「单页 + 高级折叠」IA，既不匹配 master-detail sidebar 也不匹配新 Relay-first IA。
// 本版锁定新 IA 契约：左侧三页签（Relay/Tailscale/自定义，无独立局域网页签）+ Relay 详情页内一个
// 「同一局域网时优先直连」偏好开关 + 常驻连接信息卡。
//
// 锁定手段：
// 1. ConnectionMethod 枚举契约（编译期 + 运行期）：恰好三个 case，无 .lan。
// 2. RemoteAccessView.swift 源码文本守卫（机器无关的 #filePath 相对定位）：符号存在/不存在，
//    防止把旧 IA 组件重新加回造成回归。
final class RemoteAccessNavCollapseTests: XCTestCase {

    /// 机器无关地定位 RemoteAccessView.swift：从本测试文件 #filePath 上溯两级到 MacBridge/，
    /// 再进入 MacBridge/Views/RemoteAccessView.swift。仓库内相对布局固定，不提交个人绝对路径。
    private static var remoteAccessViewSource: String {
        let testFile = URL(fileURLWithPath: #filePath)
        let viewFile = testFile
            .deletingLastPathComponent()   // …/MacBridgeTests
            .deletingLastPathComponent()   // …/MacBridge
            .appendingPathComponent("MacBridge/Views/RemoteAccessView.swift")
        return (try? String(contentsOf: viewFile, encoding: .utf8)) ?? ""
    }

    /// 新 IA：左侧恰好三个连接页签，Relay / Tailscale / 自定义；不再有独立局域网页签。
    func testConnectionMethodHasExactlyThreeTabsNoLAN() {
        let cases = RemoteAccessView.ConnectionMethod.allCases
        XCTAssertEqual(cases.count, 3, "左侧应恰好三个页签")
        XCTAssertTrue(cases.contains(.relay), "应保留 Relay 页签")
        XCTAssertTrue(cases.contains(.tailscale), "应保留 Tailscale 页签")
        XCTAssertTrue(cases.contains(.other), "应保留自定义页签")
    }

    /// .other 仍是自定义详情入口（raw value 固定为「其他 (VPS/自定义)」）。
    func testOtherMethodIsCustomAccessEntryPoint() {
        let other = RemoteAccessView.ConnectionMethod(rawValue: "其他 (VPS/自定义)")
        XCTAssertEqual(other, .other)
        XCTAssertNotEqual(other, .relay)
    }

    /// 源码守卫：旧 IA 符号已从视图移除——重新加回等于退化。
    func testViewSourceDroppedLegacyLanIASymbols() {
        let src = Self.remoteAccessViewSource
        XCTAssertFalse(src.isEmpty, "应能定位 RemoteAccessView.swift 源码")

        // 局域网页签与详情已删除。
        XCTAssertFalse(src.contains("case lan ="), "ConnectionMethod 不得再含 .lan case")
        XCTAssertFalse(src.contains("case .lan:"), "sidebar/iconView/title/subtitle/badgeInfo 不得再分支 .lan")
        XCTAssertFalse(src.contains("lanDetailView"), "lanDetailView 应已整体删除")
        XCTAssertFalse(src.contains("ConnectionMethod.lan"), "不得残留 .lan 引用")
        // 旧「显示/隐藏高级」与折叠的连接信息 disclosure 已删除（连接信息卡常驻）。
        XCTAssertFalse(src.contains("showTechnicalDetails"), "连接信息 disclosure/showTechnicalDetails 应已删除")
    }

    /// 源码守卫：Relay 详情页内含 opt-in LAN 偏好开关，并走统一通知。
    func testViewSourceHasOptInLANPreferenceToggle() {
        let src = Self.remoteAccessViewSource
        XCTAssertFalse(src.isEmpty)
        XCTAssertTrue(src.contains("@AppStorage(\"preferLocalNetwork\")"), "应含 preferLocalNetwork AppStorage")
        XCTAssertTrue(src.contains(".preferLocalNetworkDidChange"), "开关变更应 post .preferLocalNetworkDidChange")
        XCTAssertTrue(src.contains("同一局域网时优先直连") || src.contains("Prefer direct connection on same LAN"),
                      "偏好开关应带「同一局域网时优先直连」文案")
    }

    /// 源码守卫：连接信息卡常驻渲染（有「连接信息」分区，无 disclosure 门控）。
    func testViewSourceHasAlwaysOnConnectionInfoCard() {
        let src = Self.remoteAccessViewSource
        XCTAssertFalse(src.isEmpty)
        XCTAssertTrue(src.contains("连接信息") || src.contains("Connection Info"),
                      "应有常驻连接信息卡分区")
        // 默认连接信息卡不暴露 route ID / 原始 endpoint / credential。
        XCTAssertFalse(src.contains("routeId"), "默认视图不得展示 route ID")
        XCTAssertFalse(src.contains("relay?.endpoint"), "默认视图不得展示原始 endpoint")
        XCTAssertFalse(src.contains(".credential"), "默认视图不得展示 credential")
    }
}
