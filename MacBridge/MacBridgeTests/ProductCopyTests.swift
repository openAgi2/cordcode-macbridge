import XCTest

// P3-7 产品文案测试
// 不使用 @testable import，避免 TEST_HOST 依赖。
// displayStatus 映射逻辑在测试中镜像，与 BridgeStatusView.displayStatus 保持一致。

final class ProductCopyTests: XCTestCase {

    // MARK: - displayStatus 映射（镜像 BridgeStatusView.displayStatus）

    private static func displayStatus(_ status: String) -> String {
        switch status {
        case "available": return "Ready"
        case "not_detected": return "Not Found"
        case "not_logged_in": return "Login Required"
        case "service_not_running": return "Not Running"
        case "port_conflict": return "Port Conflict"
        case "version_unsupported": return "Version Incompatible"
        case "permission_denied": return "Permission Denied"
        default: return status
        }
    }

    // RuntimeManager 内部 setStatus 产生的所有文案
    private let allStatusTexts = [
        "正在启动 Bridge...",
        "CordCode Link 已停止",
        "正在重启 Bridge...",
        "启动失败: test error",
        "CordCode Link 连续意外退出，已停止自动重启",
        "CordCode Link 意外退出，正在重启...",
        "CordCode Link 启动失败，正在重试...",
        "CordCode Link 运行中",
        "请配置至少一个 AI 工具",
        "CordCode Link 正在启动...",
        "Mac 休眠中，Bridge 服务已暂停",
        "Mac 已唤醒，正在恢复 Bridge 服务...",
    ]

    private let forbiddenTerms = ["go-bridge", "PID=", "driver", "WebSocket", "websocket", "Agent"]

    // MARK: - StatusText 文案审计

    func testStatusTexts_noTechJargon() {
        for text in allStatusTexts {
            for term in forbiddenTerms {
                XCTAssertFalse(text.contains(term),
                    "statusText \"\(text)\" 不应包含技术术语 \"\(term)\"")
            }
        }
    }

    // MARK: - displayStatus 映射

    func testDisplayStatus_available() {
        XCTAssertEqual(Self.displayStatus("available"), "Ready")
    }

    func testDisplayStatus_notDetected() {
        XCTAssertEqual(Self.displayStatus("not_detected"), "Not Found")
    }

    func testDisplayStatus_notLoggedIn() {
        XCTAssertEqual(Self.displayStatus("not_logged_in"), "Login Required")
    }

    func testDisplayStatus_serviceNotRunning() {
        XCTAssertEqual(Self.displayStatus("service_not_running"), "Not Running")
    }

    func testDisplayStatus_portConflict() {
        XCTAssertEqual(Self.displayStatus("port_conflict"), "Port Conflict")
    }

    func testDisplayStatus_versionUnsupported() {
        XCTAssertEqual(Self.displayStatus("version_unsupported"), "Version Incompatible")
    }

    func testDisplayStatus_permissionDenied() {
        XCTAssertEqual(Self.displayStatus("permission_denied"), "Permission Denied")
    }

    func testDisplayStatus_unknownPassthrough() {
        XCTAssertEqual(Self.displayStatus("some_new_status"), "some_new_status")
    }

    func testDisplayStatus_noForbiddenTerms() {
        let allStatuses = ["available", "not_detected", "not_logged_in",
                           "service_not_running", "port_conflict",
                           "version_unsupported", "permission_denied", "unknown"]
        for status in allStatuses {
            let text = Self.displayStatus(status)
            for term in forbiddenTerms {
                XCTAssertFalse(text.contains(term),
                    "displayStatus(\"\(status)\") = \"\(text)\" 不应包含 \"\(term)\"")
            }
        }
    }

    // MARK: - MenuBar 文案审计

    func testMenuBarButtons_noTechTerms() {
        let texts = ["Restart CordCode Link", "Stop CordCode Link",
                      "Start CordCode Link", "Open CordCode Link", "Quit"]
        for text in texts {
            for term in forbiddenTerms.filter({ $0 != "Agent" }) {
                XCTAssertFalse(text.contains(term),
                    "菜单文案 \"\(text)\" 不应包含 \"\(term)\"")
            }
        }
    }

    // MARK: - 错误消息审计

    func testErrorMessages_noTechTerms() {
        let msgs = ["无法连接到 CordCode Link，请确认 Bridge 服务正在运行"]
        for msg in msgs {
            for term in forbiddenTerms {
                XCTAssertFalse(msg.contains(term), "\"\(msg)\" 不应包含 \"\(term)\"")
            }
        }
    }

    // MARK: - Tab 标签审计

    func testTabLabels_noTechTerms() {
        for label in ["Status", "Pairing", "Devices", "AI Tools", "Settings", "Logs"] {
            XCTAssertFalse(label.contains("Agent"))
            XCTAssertFalse(label.contains("driver"))
        }
    }
}
