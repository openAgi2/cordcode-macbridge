import XCTest
@testable import CordCodeLink

// 2026-09-22 dsh-web 未安装可代装、未启动可点启动方案 §7 收口：
// DeepSeek 行各状态的按钮与文案（§3 状态表全行）；Claude 的 not_detected
// 仍是「未找到」（全局映射不动，只覆盖 DeepSeek 行）。
@MainActor
final class DeepSeekSeatActionTests: XCTestCase {

    // MARK: - 状态文案覆盖（§3 表「这一一行显示」列）

    func testDeepSeekRowStatusOverrides() {
        XCTAssertEqual(WorkspaceView.deepSeekRowStatusText("not_detected"), L10n.dshWebStatusNotInstalled)
        XCTAssertEqual(WorkspaceView.deepSeekRowStatusText("service_not_running"), L10n.dshWebStatusNotRunning)
        // available / port_conflict 沿用全局映射。
        XCTAssertEqual(WorkspaceView.deepSeekRowStatusText("available"), L10n.statusReady)
        XCTAssertEqual(WorkspaceView.deepSeekRowStatusText("port_conflict"), L10n.statusPortConflict)
    }

    func testClaudeNotDetectedKeepsGlobalText() {
        // 红线：只覆盖 DeepSeek 行——Claude 等其他 backend 的 not_detected
        // 仍是全局「未找到」。
        XCTAssertEqual(BackendStatusText.display("not_detected"), L10n.statusNotFound)
        XCTAssertEqual(BackendStatusText.display("service_not_running"), L10n.statusNotRunning)
    }

    // MARK: - 按钮决策矩阵（§3 表「按钮」列）

    func testDeepSeekSeatActionMatrix() {
        // 未安装 + 有 npm → 安装。
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "not_detected", installing: false, starting: false, npmFound: true),
            .install
        )
        // 未安装 + 无 node/npm → 需要Node.js（点开官网，Link 不装 Node）。
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "not_detected", installing: false, starting: false, npmFound: false),
            .needNode
        )
        // 已安装未启动 → 启动。
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "service_not_running", installing: false, starting: false, npmFound: true),
            .start
        )
        // 就绪 → 无按钮。
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "available", installing: false, starting: false, npmFound: true),
            .none
        )
        // 端口占用 → 无安装按钮（字幕是 lsof 命令行）。
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "port_conflict", installing: false, starting: false, npmFound: true),
            .none
        )
    }

    func testDeepSeekSeatActionInProgressStates() {
        // 进行中态盖住按钮文案（本地态，不改 wire 枚举）。
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "not_detected", installing: true, starting: false, npmFound: true),
            .installing
        )
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "service_not_running", installing: false, starting: true, npmFound: true),
            .starting
        )
        // 安装的后半段（启动）优先显示启动中。
        XCTAssertEqual(
            WorkspaceView.deepSeekSeatAction(status: "not_detected", installing: false, starting: true, npmFound: true),
            .starting
        )
    }

    // MARK: - 文案键存在且非空（中英表都覆盖）

    func testDeepSeekCopyKeysPresent() {
        for key: String in [
            L10n.dshWebStatusNotInstalled,
            L10n.dshWebStatusNotRunning,
            L10n.dshWebInstall,
            L10n.dshWebStart,
            L10n.dshWebInstalling,
            L10n.dshWebStarting,
            L10n.dshWebNeedNode,
            L10n.dshWebInstallingHint,
            L10n.dshWebStartingHint,
        ] {
            XCTAssertFalse(key.isEmpty)
        }
    }

    // MARK: - 动作状态解码（management GET action-state 契约）

    func testDSHWebSeatActionStateDecoding() throws {
        let json = """
        {
          "installing": false,
          "starting": true,
          "npmFound": true,
          "npmPath": "/opt/homebrew/bin/npm",
          "binPath": "/opt/homebrew/bin/dsh",
          "lastStartError": "dshweb: managed dsh web child (pid 42) exited"
        }
        """
        let state = try JSONDecoder().decode(DSHWebSeatActionState.self, from: Data(json.utf8))
        XCTAssertTrue(state.starting)
        XCTAssertFalse(state.installing)
        XCTAssertTrue(state.npmFound)
        XCTAssertEqual(state.npmPath, "/opt/homebrew/bin/npm")
        XCTAssertEqual(state.binPath, "/opt/homebrew/bin/dsh")
        XCTAssertEqual(state.lastStartError, "dshweb: managed dsh web child (pid 42) exited")
        XCTAssertNil(state.lastInstallError)
        XCTAssertNil(state.lastInstallNote)
    }

    func testDSHWebSeatActionKickDecoding() throws {
        let json = """
        { "status": "node_missing", "detail": "node/npm not found (PATH, nvm)" }
        """
        let kick = try JSONDecoder().decode(DSHWebSeatActionKick.self, from: Data(json.utf8))
        XCTAssertEqual(kick.status, "node_missing")
        XCTAssertEqual(kick.detail, "node/npm not found (PATH, nvm)")
        XCTAssertNil(kick.binPath)
    }
}
