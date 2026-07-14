import XCTest
@testable import CordCodeLink

// P2-2 DiagnosticsSheet 测试：验证健康摘要优先、支持信息脱敏（不含 route id/token/密码）。
@MainActor
final class DiagnosticsSheetTests: XCTestCase {

    func testDiagnosticsCopyKeysPresent() {
        XCTAssertFalse(L10n.diagnosticsHealthSummary.isEmpty)
        XCTAssertFalse(L10n.diagnosticsCopySupportInfo.isEmpty)
        XCTAssertFalse(L10n.diagnosticsSupportInfoCopied.isEmpty)
        XCTAssertFalse(L10n.diagnosticsHealthBridge.isEmpty)
        XCTAssertFalse(L10n.diagnosticsHealthConnection.isEmpty)
        XCTAssertFalse(L10n.diagnosticsHealthAiTools.isEmpty)
        XCTAssertFalse(L10n.diagnosticsViewRawLogs.isEmpty)
    }

    func testSupportInfoIncludesHealthBuckets() {
        let bridge = BridgeStatusViewModel()
        let backend = BackendStatusViewModel()
        let info = DiagnosticsViewModel.buildSupportInfo(bridgeStatus: bridge, backendStatus: backend)
        XCTAssertTrue(info.contains("[Bridge]"), "支持信息应含 Bridge 健康段")
        XCTAssertTrue(info.contains("[连接]"), "支持信息应含 连接 健康段")
        XCTAssertTrue(info.contains("[AI 工具]"), "支持信息应含 AI 工具 健康段")
    }

    func testSupportInfoIsRedactedOfSecrets() {
        // 支持信息绝不包含 route id / token / 密码 / endpoint 凭据。
        let bridge = BridgeStatusViewModel()
        let backend = BackendStatusViewModel()
        let info = DiagnosticsViewModel.buildSupportInfo(bridgeStatus: bridge, backendStatus: backend)
        let forbidden = ["route", "token", "password", "password", "ROUTE_ID", "prekey", "secret", "credential"]
        for term in forbidden {
            XCTAssertFalse(info.lowercased().contains(term),
                           "支持信息不应包含敏感词「\(term)」，实际：\n\(info)")
        }
    }

    func testSupportInfoReportsVersionAndUptimeWhenPresent() {
        let bridge = BridgeStatusViewModel()
        // 注入一个含版本/uptime 的 managementStatus。
        let status = ManagementStatus(
            status: "ready", bridgeId: nil, displayName: nil, iosPort: nil,
            uptime: "1234s", version: "1.2.3"
        )
        bridge.managementStatus = status
        let backend = BackendStatusViewModel()
        let info = DiagnosticsViewModel.buildSupportInfo(bridgeStatus: bridge, backendStatus: backend)
        XCTAssertTrue(info.contains("1.2.3"), "支持信息应含版本")
        XCTAssertTrue(info.contains("1234s"), "支持信息应含 uptime")
    }

    func testDiagnosticsViewModelLoadsNoPathWithoutConfig() async {
        let vm = DiagnosticsViewModel()
        vm.configure(logFilePath: nil)
        await vm.loadLogs()
        XCTAssertNotNil(vm.logsError, "未配置日志路径应给出错误而非静默")
    }

    func testDisplayLogLineTruncatesLongLines() {
        let long = String(repeating: "a", count: 800)
        let truncated = DiagnosticsViewModel.displayLogLine(long)
        XCTAssertLessThan(truncated.count, long.count, "超长行应被截断")
        XCTAssertTrue(truncated.hasSuffix("…"))
    }
}
