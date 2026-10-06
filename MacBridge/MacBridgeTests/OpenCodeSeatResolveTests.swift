import XCTest
@testable import CordCodeLink

// 2026-10-06 方案 §5.1/§8：resolveManagedOpenCodeIfNeeded 失败时的持久 URL
// 回退——ensureRunning 失败 + 状态文件在 → config 保留持久 endpoint（行显示
// 「未启动」而非「未配置」）；无状态文件 → 留空（not_configured）。
// 注入 stub OpenCodeManagedServer（CLI 解析失败 → ensureRunning 返回 nil），
// 状态文件按 PersistedState 的 wire 格式预写。
final class OpenCodeSeatResolveTests: XCTestCase {

    private struct NilCLIResolver: OpenCodeCLIResolving {
        func resolveOpenCodeCLI(searchPath: [String]) -> String? { nil }
    }

    @MainActor
    private func makeManager(source: OpenCodeServerSource) -> RuntimeManager {
        RuntimeManager(config: RuntimeConfig(
            executablePath: "/usr/bin/false",
            port: 0,
            dataDir: "/tmp/cccode-ocw-resolve-\(UUID().uuidString)",
            logDir: "/tmp",
            opencodeSource: source
        ))
    }

    private func writeStateFile(dataDir: String, url: String, port: Int, username: String, password: String) {
        try? FileManager.default.createDirectory(atPath: dataDir, withIntermediateDirectories: true)
        let json = """
        {
          "version": 1,
          "url": "\(url)",
          "port": \(port),
          "username": "\(username)",
          "password": "\(password)",
          "pid": null,
          "updated_at": "2026-10-06T00:00:00Z"
        }
        """
        try? json.write(toFile: dataDir + "/opencode-managed-server.json", atomically: true, encoding: .utf8)
    }

    /// 失败 + 状态文件在 → 持久 endpoint 保留：runtime 拿到 URL 后探针失败报
    /// service_not_running；服务被「启动」拉起后探针恢复即绿，无需重启 bridge。
    @MainActor
    func testResolveFailureKeepsPersistedEndpoint() {
        let manager = makeManager(source: .managedLocal)
        let dataDir = manager.config.dataDir
        writeStateFile(dataDir: dataDir, url: "http://127.0.0.1:4123", port: 4123, username: "opencode", password: "pw-persisted")

        manager.openCodeManagedServer = OpenCodeManagedServer(
            dataDir: dataDir,
            logDir: "/tmp",
            cliSearchPath: manager.config.cliSearchPath,
            cliResolver: NilCLIResolver()
        )

        manager.resolveManagedOpenCodeIfNeeded()

        XCTAssertEqual(manager.config.opencodeURL, "http://127.0.0.1:4123", "失败时必须保留持久 URL（方案 §5.1）")
        XCTAssertEqual(manager.config.opencodeUser, "opencode")
        XCTAssertEqual(manager.config.opencodePass, "pw-persisted")
    }

    /// 失败 + 无状态文件（全新机器/从未成功保存）→ URL 留空 → not_configured。
    @MainActor
    func testResolveFailureWithoutStateFileLeavesURLEmpty() {
        let manager = makeManager(source: .managedLocal)

        manager.openCodeManagedServer = OpenCodeManagedServer(
            dataDir: manager.config.dataDir,
            logDir: "/tmp",
            cliSearchPath: manager.config.cliSearchPath,
            cliResolver: NilCLIResolver()
        )

        manager.resolveManagedOpenCodeIfNeeded()

        XCTAssertEqual(manager.config.opencodeURL, "", "无状态文件时 URL 必须留空（not_configured）")
    }

    /// 非 managed_local：resolve 不触碰 config（external_http 用户自管 URL 不被覆盖）。
    @MainActor
    func testResolveSkipsNonManagedSource() {
        let manager = makeManager(source: .externalHttp)
        manager.config.opencodeURL = "http://127.0.0.1:9999"
        manager.config.opencodeUser = "ext-user"
        manager.config.opencodePass = "ext-pass"

        manager.resolveManagedOpenCodeIfNeeded()

        XCTAssertEqual(manager.config.opencodeURL, "http://127.0.0.1:9999", "external_http 的 URL 不得被 resolve 覆盖")
        XCTAssertEqual(manager.config.opencodeUser, "ext-user")
    }

    /// persistedEndpoint 单元行为：状态文件在 → endpoint；无 → nil。
    func testPersistedEndpointReadsStateFile() {
        let dataDir = "/tmp/cccode-ocw-persisted-\(UUID().uuidString)"
        writeStateFile(dataDir: dataDir, url: "http://127.0.0.1:4107", port: 4107, username: "opencode", password: "pw-ep")

        let server = OpenCodeManagedServer(dataDir: dataDir, logDir: "/tmp", cliSearchPath: [])
        XCTAssertEqual(server.persistedEndpoint()?.url, "http://127.0.0.1:4107")
        XCTAssertEqual(server.persistedEndpoint()?.username, "opencode")
        XCTAssertEqual(server.persistedEndpoint()?.password, "pw-ep")

        let empty = OpenCodeManagedServer(dataDir: "/tmp/cccode-ocw-persisted-\(UUID().uuidString)", logDir: "/tmp", cliSearchPath: [])
        XCTAssertNil(empty.persistedEndpoint(), "无状态文件必须返回 nil")
    }
}
