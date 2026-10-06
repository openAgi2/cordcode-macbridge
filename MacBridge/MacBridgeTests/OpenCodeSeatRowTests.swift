import XCTest
@testable import CordCodeLink

// 2026-10-06 OpenCode Web 未安装可代装、未启动可点启动方案 §8 收口：
// OpenCode 行各状态的行文本与按钮（§3 状态表 8 行逐行对拍，r1 F-1 的
// external_http / disabled 行文本沿用全局映射断言）；全局映射不动，只覆盖
// OpenCode 行。
@MainActor
final class OpenCodeSeatRowTests: XCTestCase {

    // MARK: - 行文本决策（§3 表「这一行显示」列，8 行逐行）

    func testOpenCodeRowStatusManagedLocalOverrides() {
        // 第 1 行：CLI 缺失（无/有持久 endpoint 两种 wire 形态）→ 未安装。
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .managedLocal, wireStatus: "not_configured", cliFound: false),
            L10n.openCodeWebStatusNotInstalled
        )
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .managedLocal, wireStatus: "service_not_running", cliFound: false),
            L10n.openCodeWebStatusNotInstalled
        )
        // 第 2-4 行：CLI 在、服务没起（含 401 / v2 隔离 / 无持久 endpoint 的
        // not_configured——managed_local 下它表示「从未成功启动」）→ 未启动。
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .managedLocal, wireStatus: "service_not_running", cliFound: true),
            L10n.openCodeWebStatusNotRunning
        )
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .managedLocal, wireStatus: "not_configured", cliFound: true),
            L10n.openCodeWebStatusNotRunning
        )
        // 第 5 行：健康 + generation118 → 就绪（全局映射）。
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .managedLocal, wireStatus: "available", cliFound: true),
            L10n.statusReady
        )
    }

    func testOpenCodeRowStatusNonManagedKeepsGlobalMapping() {
        // r1 F-1 红线：source != managedLocal 的非 available 沿用全局映射——
        // 第 6/8 行 not_configured → 未配置，第 7 行 service_not_running → 未启动。
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .externalHttp, wireStatus: "not_configured", cliFound: false),
            L10n.notConfigured
        )
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .externalHttp, wireStatus: "service_not_running", cliFound: false),
            L10n.statusNotRunning
        )
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .disabled, wireStatus: "not_configured", cliFound: false),
            L10n.notConfigured
        )
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .legacy64667, wireStatus: "not_configured", cliFound: false),
            L10n.notConfigured
        )
        // external_http 就绪仍是全局「就绪」。
        XCTAssertEqual(
            WorkspaceView.openCodeRowStatusText(source: .externalHttp, wireStatus: "available", cliFound: false),
            L10n.statusReady
        )
    }

    func testGlobalMappingUnchangedForOtherBackends() {
        // 只覆盖 OpenCode 行：其他 backend 的 not_detected 仍是全局「未找到」。
        XCTAssertEqual(BackendStatusText.display("not_detected"), L10n.statusNotFound)
        XCTAssertEqual(BackendStatusText.display("service_not_running"), L10n.statusNotRunning)
        XCTAssertEqual(BackendStatusText.display("not_configured"), L10n.notConfigured)
    }

    // MARK: - 按钮决策矩阵（§3 表「按钮」列）

    func testOpenCodeSeatActionMatrix() {
        // 第 1 行：CLI 缺失 + npm 在 → 安装；无 node/npm → 需要Node.js。
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "not_configured", cliFound: false, npmFound: true, installing: false, starting: false),
            .install
        )
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "service_not_running", cliFound: false, npmFound: false, installing: false, starting: false),
            .needNode
        )
        // 第 2-4 行：CLI 在、服务没起 → 启动（含无持久 endpoint 的 not_configured）。
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "service_not_running", cliFound: true, npmFound: true, installing: false, starting: false),
            .start
        )
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "not_configured", cliFound: true, npmFound: false, installing: false, starting: false),
            .start
        )
        // 第 5 行：就绪 → 无按钮。
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "available", cliFound: true, npmFound: true, installing: false, starting: false),
            .none
        )
        // 第 6/7/8 行：external_http / disabled → 无按钮（用户自管服务）。
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .externalHttp, wireStatus: "not_configured", cliFound: false, npmFound: true, installing: false, starting: false),
            .none
        )
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .externalHttp, wireStatus: "service_not_running", cliFound: false, npmFound: true, installing: false, starting: false),
            .none
        )
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .disabled, wireStatus: "not_configured", cliFound: false, npmFound: true, installing: false, starting: false),
            .none
        )
    }

    func testOpenCodeSeatActionInProgressStates() {
        // 进行中态盖住按钮文案（本地态，不改 wire 枚举，不闪回「未配置」）。
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "not_configured", cliFound: false, npmFound: true, installing: true, starting: false),
            .installing
        )
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "service_not_running", cliFound: true, npmFound: true, installing: false, starting: true),
            .starting
        )
        // 安装的后半段（启动）优先显示启动中。
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "not_configured", cliFound: false, npmFound: true, installing: false, starting: true),
            .starting
        )
        // 进行中盖过 wire available（行自己盖住按钮文案）。
        XCTAssertEqual(
            WorkspaceView.openCodeSeatAction(source: .managedLocal, wireStatus: "available", cliFound: true, npmFound: true, installing: false, starting: true),
            .starting
        )
    }

    // MARK: - 文案键存在且非空（中英表都覆盖）

    func testOpenCodeCopyKeysPresent() {
        for key: String in [
            L10n.openCodeWebStatusNotInstalled,
            L10n.openCodeWebStatusNotRunning,
            L10n.openCodeWebInstall,
            L10n.openCodeWebStart,
            L10n.openCodeWebInstalling,
            L10n.openCodeWebStarting,
            L10n.openCodeWebNeedNode,
            L10n.openCodeWebInstallingHint,
            L10n.openCodeWebStartingHint,
        ] {
            XCTAssertFalse(key.isEmpty)
        }
        // 键名镜像 dsh_web_* 命名且在两张语言表里都有值（tr 缺键会回退键名本身）。
        XCTAssertNotEqual(L10n.openCodeWebStatusNotInstalled, "opencode_web_status_not_installed")
        XCTAssertNotEqual(L10n.openCodeWebInstallingHint, "opencode_web_installing_hint")
    }
}
