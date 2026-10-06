import XCTest
@testable import CordCodeLink

// 2026-10-06 方案 §4/§8：installer 契约测试。注入式 Process stub（不访问真实
// npm registry）；argv 断言、prefix 回退、--version 验证门、0600 无凭据记录、
// CLI 解析 record-first。
final class OpenCodeInstallerTests: XCTestCase {

    /// Records every run; answers `prefix -g` / `--version` / install commands
    /// from canned state.
    private final class StubProcessRunner: OpenCodeProcessRunning {
        struct Call {
            let executable: String
            let arguments: [String]
            let extraPATH: String?
            let timeout: TimeInterval
        }

        private(set) var calls: [Call] = []
        /// install 命令（install -g / install --prefix）的退出码与 stderr；
        /// 全局与前缀路径分开配置（回退测试需要全局失败、前缀成功）。
        var globalInstallExitCode: Int32 = 0
        var prefixInstallExitCode: Int32 = 0
        var installStderr = ""
        var installTimedOut = false
        /// `npm prefix -g` 的应答（stdout）。
        var globalPrefix: String?
        /// `opencode --version` 的退出码与 stdout。
        var versionExitCode: Int32 = 0
        var versionStdout = "1.18.34\n"

        func run(executable: String, arguments: [String], extraPATH: String?, timeout: TimeInterval) -> OpenCodeProcessRunResult {
            calls.append(Call(executable: executable, arguments: arguments, extraPATH: extraPATH, timeout: timeout))
            if arguments == ["prefix", "-g"] {
                return OpenCodeProcessRunResult(exitCode: 0, stdout: (globalPrefix ?? "") + "\n", stderr: "", timedOut: false)
            }
            if arguments == ["--version"] {
                return OpenCodeProcessRunResult(exitCode: versionExitCode, stdout: versionStdout, stderr: versionExitCode == 0 ? "" : "not found", timedOut: false)
            }
            let isPrefixInstall = arguments.contains("--prefix")
            return OpenCodeProcessRunResult(
                exitCode: isPrefixInstall ? prefixInstallExitCode : globalInstallExitCode,
                stdout: "",
                stderr: installStderr,
                timedOut: installTimedOut
            )
        }
    }

    private var dataDir: String!
    private var fakePrefix: String!
    private var runner: StubProcessRunner!

    override func setUpWithError() throws {
        dataDir = "/tmp/cccode-ocw-inst-\(UUID().uuidString)"
        fakePrefix = dataDir + "/fake-global"
        runner = StubProcessRunner()
        runner.globalPrefix = fakePrefix
        try FileManager.default.createDirectory(atPath: dataDir, withIntermediateDirectories: true)
        try makeFakeExecutable(fakePrefix + "/bin/opencode")
    }

    override func tearDown() {
        try? FileManager.default.removeItem(atPath: dataDir)
    }

    /// 创建可执行假文件（bin 存在性检查与 record-first 都需要真实可执行位）。
    private func makeFakeExecutable(_ path: String) throws {
        let dir = (path as NSString).deletingLastPathComponent
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try "#!/bin/sh\nexit 0\n".write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: path)
    }

    /// user-global 成功路径：argv 恒为 `npm install -g opencode-ai@1.18`（无
    /// sudo、无 npx、无 sh -c）→ prefix -g 定位 bin → --version 验证 → 记录。
    func testInstallGlobalArgvAndRecord() {
        let result = OpenCodeInstaller.install(npmPath: "/opt/homebrew/bin/npm", dataDir: dataDir, runner: runner)

        guard case .success(let success) = result else {
            return XCTFail("global install should succeed, got \(result)")
        }
        XCTAssertEqual(success.binPath, fakePrefix + "/bin/opencode")
        XCTAssertEqual(success.scope, "user-global")
        XCTAssertEqual(success.version, "1.18.34")
        XCTAssertEqual(success.note, "")

        // argv 契约：三次调用 = install -g / prefix -g / --version。
        XCTAssertEqual(runner.calls.count, 3)
        let install = runner.calls[0]
        XCTAssertEqual(install.executable, "/opt/homebrew/bin/npm", "必须直接 exec npm，不经 sh -c")
        XCTAssertEqual(install.arguments, ["install", "-g", "opencode-ai@1.18"], "argv 必须是 npm install -g opencode-ai@<spec>（OD-2=A）")
        XCTAssertFalse(install.arguments.contains(where: { $0.contains("sudo") || $0.contains("npx") }), "禁止 sudo / npx")
        XCTAssertEqual(runner.calls[1].arguments, ["prefix", "-g"])
        XCTAssertEqual(runner.calls[2].arguments, ["--version"])
        XCTAssertEqual(install.timeout, OpenCodeInstaller.installTimeout, "安装受 10 分钟预算约束")
        XCTAssertEqual(install.extraPATH, "/opt/homebrew/bin", "npm 所在目录必须并入 PATH（node 可见）")

        // 记录文件：0600、无凭据。
        let recordPath = OpenCodeInstaller.recordPath(dataDir: dataDir)
        XCTAssertTrue(FileManager.default.fileExists(atPath: recordPath), "成功安装必须写记录")
        let attrs = try? FileManager.default.attributesOfItem(atPath: recordPath)
        XCTAssertEqual((attrs?[.posixPermissions] as? NSNumber)?.int16Value ?? 0, 0o600, "记录文件必须 0600")
        let json = (try? String(contentsOfFile: recordPath, encoding: .utf8)) ?? ""
        XCTAssertFalse(json.lowercased().contains("password"), "记录不得含凭据")
        XCTAssertFalse(json.lowercased().contains("username"), "记录不得含凭据")
        let record = OpenCodeInstaller.readInstallRecord(dataDir: dataDir)
        XCTAssertEqual(record?.binPath, fakePrefix + "/bin/opencode")
        XCTAssertEqual(record?.scope, "user-global")
        XCTAssertEqual(record?.opencodeVersion, "1.18.34")
    }

    /// prefix 回退：全局安装失败 → `npm install --prefix <dataDir>/opencode-install
    /// opencode-ai@1.18`，bin 落 <prefix>/node_modules/.bin/opencode，scope 与
    /// 界面备注写明 CordCode 目录。
    func testInstallPrefixFallback() throws {
        runner.globalInstallExitCode = 1
        runner.installStderr = "EACCES: permission denied"
        let fallbackBin = dataDir + "/opencode-install/node_modules/.bin/opencode"
        try makeFakeExecutable(fallbackBin)

        let result = OpenCodeInstaller.install(npmPath: "/opt/homebrew/bin/npm", dataDir: dataDir, runner: runner)

        guard case .success(let success) = result else {
            return XCTFail("prefix fallback should succeed, got \(result)")
        }
        XCTAssertEqual(success.binPath, fallbackBin)
        XCTAssertEqual(success.scope, "cordcode-prefix")
        XCTAssertTrue(success.note.contains("CordCode 目录"), "prefix 回退必须写明装到了 CordCode 目录")

        // argv 契约：第一次 -g 失败，第二次 --prefix 回退。
        XCTAssertEqual(runner.calls[0].arguments, ["install", "-g", "opencode-ai@1.18"])
        XCTAssertEqual(runner.calls[1].arguments, ["install", "--prefix", dataDir + "/opencode-install", "opencode-ai@1.18"])
        XCTAssertEqual(runner.calls[2].arguments, ["--version"])
        XCTAssertEqual(OpenCodeInstaller.readInstallRecord(dataDir: dataDir)?.scope, "cordcode-prefix")
    }

    /// --version 失败 = 安装失败：不写记录、不启动（方案 §4）。
    func testVersionFailureIsInstallFailure() {
        runner.versionExitCode = 1

        let result = OpenCodeInstaller.install(npmPath: "/opt/homebrew/bin/npm", dataDir: dataDir, runner: runner)

        guard case .failure(let failure) = result else {
            return XCTFail("--version 失败必须是安装失败, got \(result)")
        }
        XCTAssertTrue(failure.message.contains("--version"), "失败文案必须点名验证失败：\(failure.message)")
        XCTAssertFalse(FileManager.default.fileExists(atPath: OpenCodeInstaller.recordPath(dataDir: dataDir)), "失败不得写安装记录")
    }

    /// 超时：写明超时并终止，不写成成功。
    func testInstallTimeoutSurfacesTimeout() {
        runner.installTimedOut = true

        let result = OpenCodeInstaller.install(npmPath: "/opt/homebrew/bin/npm", dataDir: dataDir, runner: runner)

        guard case .failure(let failure) = result else {
            return XCTFail("超时必须是安装失败, got \(result)")
        }
        XCTAssertTrue(failure.message.contains("超时"), "超时文案必须写明超时：\(failure.message)")
    }

    /// 两条路径都失败：错误信息同时携带两段真实原因。
    func testBothInstallPathsFail() throws {
        runner.globalInstallExitCode = 1
        runner.installStderr = "EPERM"
        // prefix 回退的 bin 也不存在 → 第二条路径同样失败。

        let result = OpenCodeInstaller.install(npmPath: "/opt/homebrew/bin/npm", dataDir: dataDir, runner: runner)

        guard case .failure(let failure) = result else {
            return XCTFail("双失败必须是安装失败, got \(result)")
        }
        XCTAssertTrue(failure.message.contains("user-global 安装失败"), failure.message)
        XCTAssertTrue(failure.message.contains("CordCode 目录安装失败"), failure.message)
        XCTAssertTrue(failure.message.contains("EPERM"), "必须携带 npm 真实错误：\(failure.message)")
    }

    /// CLI 解析 record-first：记录 bin 可执行即用（GUI PATH 缺口下跨重启仍可
    /// 发现）；记录缺失/不可执行 → 走搜索路径。
    func testCLIResolverRecordFirst() throws {
        let recordBin = dataDir + "/record-bin/opencode"
        try makeFakeExecutable(recordBin)
        OpenCodeInstaller.writeInstallRecord(dataDir: dataDir, record: .init(
            binPath: recordBin,
            scope: "cordcode-prefix",
            opencodeVersion: "1.18.34",
            installedAt: "2026-10-06T00:00:00Z"
        ))

        XCTAssertEqual(DefaultOpenCodeCLIResolver(dataDir: dataDir).resolveOpenCodeCLI(searchPath: []), recordBin, "记录 bin 可执行必须优先")

        // 记录指向不存在的 bin → 回落到搜索路径。
        let searchDir = dataDir + "/search-bin"
        try makeFakeExecutable(searchDir + "/opencode")
        OpenCodeInstaller.writeInstallRecord(dataDir: dataDir, record: .init(
            binPath: dataDir + "/gone/opencode",
            scope: "cordcode-prefix",
            opencodeVersion: "1.18.34",
            installedAt: "2026-10-06T00:00:00Z"
        ))
        XCTAssertEqual(DefaultOpenCodeCLIResolver(dataDir: dataDir).resolveOpenCodeCLI(searchPath: [searchDir]), searchDir + "/opencode", "记录失效必须回落搜索路径")

        // 无记录 → 纯搜索路径（原行为不变）。
        XCTAssertEqual(DefaultOpenCodeCLIResolver(dataDir: nil).resolveOpenCodeCLI(searchPath: [searchDir]), searchDir + "/opencode")
    }

    /// 打码：npm token 样式与 auth URL 样式必须被替换。
    func testRedactNpmOutput() {
        let redacted = OpenCodeInstaller.redactNpmOutput("token npm_abcdefghijklmnop1234 at //user:secretpass@registry.npmjs.org/")
        XCTAssertFalse(redacted.contains("npm_abcdefghijklmnop1234"))
        XCTAssertFalse(redacted.contains("secretpass"))
        XCTAssertTrue(redacted.contains("npm_[REDACTED]"))
    }

    /// lastNonEmptyLine：stderr 优先，取最后一段非空行。
    func testLastNonEmptyLine() {
        XCTAssertEqual(OpenCodeInstaller.lastNonEmptyLine("", "line1\n\nline2\n"), "line2")
        XCTAssertEqual(OpenCodeInstaller.lastNonEmptyLine("err\n", "out\n"), "err")
    }
}
