import XCTest
import Darwin
@testable import CordCodeLink

/// Grok Build Leader 模式开关单元测试（设计 §7.1 T1–T33）。
/// 真实临时目录 + 合成 env 注入；synthetic 用例冻结实现行为，不宣称现场支持（§3.3-9）。
final class GrokLeaderModeTests: XCTestCase {

    private var base: URL!
    private var grokHome: URL!
    private var appSupport: URL!

    override func setUpWithError() throws {
        base = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("grok-leader-tests-\(UUID().uuidString)", isDirectory: true)
        grokHome = base.appendingPathComponent("grok-home", isDirectory: true)
        appSupport = base.appendingPathComponent("app-support", isDirectory: true)
        try FileManager.default.createDirectory(at: grokHome, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: appSupport, withIntermediateDirectories: true)
        addTeardownBlock { [base] in
            guard let base else { return }
            try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: base.path)
            try? FileManager.default.removeItem(at: base)
        }
    }

    // MARK: - 帮助函数

    @MainActor
    private func makeManager() -> GrokLeaderModeManager {
        GrokLeaderModeManager(environment: ["GROK_HOME": grokHome.path], appSupport: appSupport)
    }

    private var configURL: URL { grokHome.appendingPathComponent("config.toml") }

    @discardableResult
    private func writeConfig(_ text: String) throws -> URL {
        try text.write(to: configURL, atomically: true, encoding: .utf8)
        return configURL
    }

    private func readConfig() throws -> String {
        try String(contentsOf: configURL, encoding: .utf8)
    }

    private func modeOf(_ path: String) throws -> mode_t {
        let value = try FileManager.default.attributesOfItem(atPath: path)[.posixPermissions]
        return mode_t((value as? Int) ?? 0)
    }

    private func backupDir() -> URL {
        appSupport.appendingPathComponent("GrokConfigBackups", isDirectory: true)
    }

    private func backupNames() -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: backupDir().path)) ?? [])
            .filter { $0.hasSuffix(".tomlbak") }
            .sorted()
    }

    private func precreateBackups(_ contents: [String]) throws {
        try FileManager.default.createDirectory(at: backupDir(), withIntermediateDirectories: true)
        for (i, content) in contents.enumerated() {
            let url = backupDir().appendingPathComponent("0000000000.00000000\(i)-00000000-0000-0000-0000-00000000000\(i).tomlbak")
            try content.write(to: url, atomically: true, encoding: .utf8)
        }
    }

    private func noTempLeft() -> Bool {
        let names = (try? FileManager.default.contentsOfDirectory(atPath: grokHome.path)) ?? []
        return !names.contains { $0.hasPrefix(".cordcode-grok-") && $0.hasSuffix(".tmp") }
    }

    // MARK: - T9 路径解析（纯函数）

    func testT9PathResolutionChains() {
        // GROK_HOME + 无 socket env
        var paths = GrokLeaderPaths.resolve(environment: ["GROK_HOME": "/tmp/h1"])
        XCTAssertEqual(paths.grokHome, "/tmp/h1")
        XCTAssertEqual(paths.configPath, "/tmp/h1/config.toml")
        XCTAssertEqual(paths.socketPath, "/tmp/h1/leader.sock")

        // GROK_LEADER_SOCKET 显式覆盖（独立于 home）
        paths = GrokLeaderPaths.resolve(environment: ["GROK_HOME": "/tmp/h1", "GROK_LEADER_SOCKET": "/var/run/s.sock"])
        XCTAssertEqual(paths.socketPath, "/var/run/s.sock")

        // 空环境 → ~/.grok 兜底（与 leader_subscriber.go:63-79 同链）
        paths = GrokLeaderPaths.resolve(environment: [:])
        XCTAssertEqual(paths.grokHome, NSHomeDirectory() + "/.grok")
        XCTAssertEqual(paths.socketPath, NSHomeDirectory() + "/.grok/leader.sock")

        // 空白 env 视为未设置
        paths = GrokLeaderPaths.resolve(environment: ["GROK_HOME": "   ", "GROK_LEADER_SOCKET": " "])
        XCTAssertEqual(paths.grokHome, NSHomeDirectory() + "/.grok")
        XCTAssertEqual(paths.socketPath, NSHomeDirectory() + "/.grok/leader.sock")
    }

    // MARK: - T1/T11 无文件与无目录

    @MainActor
    func testT1NoConfigTurnOnCreatesMinimalFile() throws {
        let manager = makeManager()
        let wrote = try manager.setLeaderMode(true)
        XCTAssertTrue(wrote)
        XCTAssertEqual(try readConfig(), "[cli]\nuse_leader = true\n")
        XCTAssertEqual(try modeOf(configURL.path), 0o644)
        XCTAssertTrue(noTempLeft())
    }

    @MainActor
    func testT11NoHomeDirTurnOnCreatesDirAndFile() throws {
        try FileManager.default.removeItem(at: grokHome)
        let manager = makeManager()
        try manager.setLeaderMode(true)
        var isDir: ObjCBool = false
        XCTAssertTrue(FileManager.default.fileExists(atPath: grokHome.path, isDirectory: &isDir))
        XCTAssertTrue(isDir.boolValue)
        XCTAssertEqual(try modeOf(grokHome.path), 0o700)
        XCTAssertEqual(try readConfig(), "[cli]\nuse_leader = true\n")
        XCTAssertEqual(try modeOf(configURL.path), 0o644)
    }

    // MARK: - T2 其他键与注释保留

    @MainActor
    func testT2TurnOnPreservesOtherKeysAndComments() throws {
        let original = """
        # 顶部注释
        [agent]
        model = "grok-4"

        [cli]
        # cli 注释
        theme = "dark"
        """
        try writeConfig(original + "\n")
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try readConfig(), original + "\nuse_leader = true\n")
    }

    // MARK: - T3 false → true 原位替换

    @MainActor
    func testT3FalseToTrueInPlaceWithComment() throws {
        try writeConfig("[cli]\nuse_leader = false # keep\n")
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try readConfig(), "[cli]\nuse_leader = true # keep\n")

        // 无注释变体 + 紧凑写法
        try writeConfig("[cli]\nuse_leader=false\n")
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try readConfig(), "[cli]\nuse_leader=true\n")
    }

    // MARK: - T4/T5 关 = 删键

    @MainActor
    func testT4TurnOffDeletesKeyKeepsOthers() throws {
        try writeConfig("[cli]\nuse_leader = true\ntheme = \"dark\"\n")
        try makeManager().setLeaderMode(false)
        XCTAssertEqual(try readConfig(), "[cli]\ntheme = \"dark\"\n")
    }

    @MainActor
    func testT5TurnOffEmptySectionKeepsHeader() throws {
        try writeConfig("[cli]\nuse_leader = true\n")
        try makeManager().setLeaderMode(false)
        XCTAssertEqual(try readConfig(), "[cli]\n")
    }

    // MARK: - T6 同名键不误读不误改

    @MainActor
    func testT6SameNamedKeysInOtherScopesNotTouched() throws {
        let original = "use_leader = true\n\n[agent]\nuse_leader = false\n"
        try writeConfig(original)
        let manager = makeManager()
        manager.refresh()
        XCTAssertEqual(manager.status.value, .absent)

        try manager.setLeaderMode(true)
        let after = try readConfig()
        XCTAssertTrue(after.hasPrefix(original), "顶层裸键与 [agent] 必须原样")
        XCTAssertTrue(after.contains("\n[cli]\nuse_leader = true\n"))

        // [cli].use_leader 与顶层裸键语义区分：refresh 读到的是 [cli] 的 true
        manager.refresh()
        XCTAssertEqual(manager.status.value, .explicitTrue)
    }

    // MARK: - T7 并发修改（内容身份比较）

    @MainActor
    func testT7ConcurrentModificationAbortsWrite() throws {
        let original = "[cli]\ntheme = \"dark\"\n"
        try writeConfig(original)
        let newContent = try XCTUnwrap(try GrokLeaderConfigFileEditor.enabledContent(from: original))

        var writer = GrokLeaderConfigWriter(backupDirectory: backupDir())
        writer.testHookBeforeVerify = { path in
            try? "concurrent-writer was here".write(toFile: path, atomically: true, encoding: .utf8)
        }
        XCTAssertThrowsError(try writer.apply(newContent: newContent, to: configURL.path, expecting: .explicitTrue)) { error in
            guard case GrokLeaderConfigWriter.WriterError.concurrentModification = error else {
                return XCTFail("应为并发修改错误，实际 \(error)")
            }
        }
        XCTAssertEqual(try String(contentsOf: configURL, encoding: .utf8), "concurrent-writer was here")
        XCTAssertTrue(noTempLeft())
    }

    // MARK: - T8 目录只读

    @MainActor
    func testT8ReadOnlyDirFailsVisiblyOriginalIntact() throws {
        try writeConfig("[cli]\ntheme = \"dark\"\n")
        try FileManager.default.setAttributes([.posixPermissions: 0o500], ofItemAtPath: grokHome.path)
        defer { try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: grokHome.path) }

        XCTAssertThrowsError(try makeManager().setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.ioFailure = error else {
                return XCTFail("应为 ioFailure，实际 \(error)")
            }
        }
        XCTAssertEqual(try readConfig(), "[cli]\ntheme = \"dark\"\n")
    }

    // MARK: - T10 备份轮转与命名碰撞

    @MainActor
    func testT10BackupRotationConvergeThenCreate() throws {
        try precreateBackups(["old0", "old1", "old2"])
        try writeConfig("[cli]\ntheme = \"dark\"\n")
        try makeManager().setLeaderMode(true)

        let names = backupNames()
        XCTAssertEqual(names.count, 3, "收敛 ≤2 后创建第 3 份")
        XCTAssertFalse(names.contains { $0.contains("0000000000.000000000-") }, "最旧备份被删除")
        let newest = names.last
        let content = try String(contentsOf: backupDir().appendingPathComponent(newest!), encoding: .utf8)
        XCTAssertEqual(content, "[cli]\ntheme = \"dark\"\n", "备份内容 = 写前原文件")
    }

    @MainActor
    func testT10NoOriginalNoBackup() throws {
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(backupNames().count, 0, "无原文件时无备份")
    }

    func testT10BackupNameCollisionRetriesWithoutOverwrite() throws {
        try FileManager.default.createDirectory(at: backupDir(), withIntermediateDirectories: true)
        let fixed = UUID(uuidString: "AAAAAAAA-0000-0000-0000-000000000000")!
        var calls = 0
        let store = GrokConfigBackupStore(directory: backupDir())
        let first = try store.createBackup(
            data: Data("first".utf8),
            clock: { (100, 200) },
            uuid: { fixed }
        )
        XCTAssertEqual(first.lastPathComponent, "100.000000200-\(fixed.uuidString).tomlbak")

        // 同名碰撞：预占 first 名字后，新备份必须换名重试、不覆盖
        var n = 0
        let second = try store.createBackup(
            data: Data("second".utf8),
            clock: { (100, 200) },
            uuid: {
                n += 1
                return n <= 2 ? fixed : UUID(uuidString: "BBBBBBBB-0000-0000-0000-000000000000")!
            }
        )
        XCTAssertNotEqual(second.lastPathComponent, first.lastPathComponent)
        XCTAssertEqual(try String(contentsOf: first, encoding: .utf8), "first", "碰撞不覆盖")
        XCTAssertEqual(try String(contentsOf: second, encoding: .utf8), "second")
        XCTAssertEqual(backupNames().count, 2)
    }

    // MARK: - T12 节头尾注释

    @MainActor
    func testT12SectionHeaderTrailingComment() throws {
        try writeConfig("[cli] # trailing\nother = 1\n")
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try readConfig(), "[cli] # trailing\nother = 1\nuse_leader = true\n")

        try makeManager().setLeaderMode(false)
        XCTAssertEqual(try readConfig(), "[cli] # trailing\nother = 1\n")
    }

    // MARK: - T13 CRLF

    @MainActor
    func testT13CRLFPreservedEndToEnd() throws {
        let original = "[cli]\r\ntheme = \"dark\"\r\n"
        try writeConfig(original)
        try makeManager().setLeaderMode(true)
        let after = try readConfig()
        XCTAssertEqual(after, "[cli]\r\ntheme = \"dark\"\r\nuse_leader = true\r\n")
        XCTAssertFalse(after.contains("\n"), "不得产生裸 LF")
        XCTAssertFalse(after.replacingOccurrences(of: "\r\n", with: "").contains("\r"), "不得产生裸 CR")

        // 原位替换分支同样保持 CRLF
        try writeConfig("[cli]\r\nuse_leader = false\r\n")
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try readConfig(), "[cli]\r\nuse_leader = true\r\n")

        // 删键分支同样保持 CRLF
        try makeManager().setLeaderMode(false)
        XCTAssertEqual(try readConfig(), "[cli]\r\n")
    }

    // MARK: - T14 前导空格与行内注释

    @MainActor
    func testT14LeadingWhitespaceAndInlineComment() throws {
        try writeConfig("[cli]\n  use_leader = false # x\n  other = 2\n")
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try readConfig(), "[cli]\n  use_leader = true # x\n  other = 2\n")
    }

    // MARK: - T15 等价形态 F2

    @MainActor
    func testT15EquivalentFormsRejectF2() throws {
        let cases = [
            // 点键
            "cli.use_leader = true\n",
            // quoted key
            "[cli]\n\"use_leader\" = true\n",
            // inline table
            "cli = { use_leader = true }\n",
            // 空白节头
            "[ cli ]\nuse_leader = true\n",
            // 顶层点键定义 cli（含子键）
            "cli.theme = \"dark\"\n",
        ]
        for text in cases {
            try writeConfig(text)
            let manager = makeManager()
            XCTAssertThrowsError(try manager.setLeaderMode(true), "应 F2 拒绝: \(text)") { error in
                guard case GrokLeaderModeManager.SetModeError.f2 = error else {
                    return XCTFail("应为 F2，实际 \(error)（输入 \(text)）")
                }
            }
            XCTAssertEqual(try readConfig(), text, "不产生第二语义键: \(text)")

            manager.refresh()
            XCTAssertEqual(manager.status.readError, .f2, "状态应上报 F2: \(text)")
            XCTAssertTrue(manager.status.isDisabled)
        }
    }

    // MARK: - T16 OFF 幂等

    @MainActor
    func testT16TurnOffIdempotency() throws {
        // false → 删键
        try writeConfig("[cli]\nuse_leader = false\nother = 1\n")
        let wrote = try makeManager().setLeaderMode(false)
        XCTAssertTrue(wrote)
        XCTAssertEqual(try readConfig(), "[cli]\nother = 1\n")

        // absent → 幂等无操作成功返回（UI 可达路径是 ON→OFF，§3.4）
        try writeConfig("[cli]\nother = 1\n")
        let wroteAgain = try makeManager().setLeaderMode(false)
        XCTAssertFalse(wroteAgain)
        XCTAssertEqual(try readConfig(), "[cli]\nother = 1\n")
    }

    // MARK: - T17 无备份的写后校验失败

    func testT17PostVerifyFailureNoBackupPath() throws {
        let newContent = try XCTUnwrap(try GrokLeaderConfigFileEditor.enabledContent(from: ""))
        var writer = GrokLeaderConfigWriter(backupDirectory: backupDir())
        writer.testHookAfterRename = { path in
            try? FileManager.default.removeItem(atPath: path)
        }
        XCTAssertThrowsError(try writer.apply(newContent: newContent, to: configURL.path, expecting: .explicitTrue)) { error in
            guard case GrokLeaderConfigWriter.WriterError.postVerifyFailed(false, let reason) = error else {
                return XCTFail("应为 postVerifyFailed(false)，实际 \(error)")
            }
            XCTAssertTrue(reason.contains("无备份可恢复"), "错误文案须覆盖无备份路径，实际: \(reason)")
        }
    }

    // MARK: - T18 symlink

    @MainActor
    func testT18SymlinkWritesTargetLinkUntouched() throws {
        let targetDir = base.appendingPathComponent("external", isDirectory: true)
        try FileManager.default.createDirectory(at: targetDir, withIntermediateDirectories: true)
        let target = targetDir.appendingPathComponent("real-config.toml")
        try "[cli]\ntheme = \"dark\"\n".write(to: target, atomically: true, encoding: .utf8)
        try FileManager.default.createSymbolicLink(
            atPath: configURL.path,
            withDestinationPath: target.path
        )

        try makeManager().setLeaderMode(true)

        XCTAssertEqual(try String(contentsOf: target, encoding: .utf8), "[cli]\ntheme = \"dark\"\nuse_leader = true\n")
        // 链接本身不变（仍是 symlink 且指向原目标）
        XCTAssertEqual(try FileManager.default.destinationOfSymbolicLink(atPath: configURL.path), target.path)
        // temp+rename 发生在目标目录，不残留
        let leftovers = (try? FileManager.default.contentsOfDirectory(atPath: targetDir.path)) ?? []
        XCTAssertFalse(leftovers.contains { $0.hasSuffix(".tmp") })
        XCTAssertTrue(noTempLeft())
    }

    @MainActor
    func testT18DanglingAndLoopSymlinksF1() throws {
        // 悬空
        try FileManager.default.createSymbolicLink(atPath: configURL.path, withDestinationPath: grokHome.appendingPathComponent("missing.toml").path)
        let manager = makeManager()
        XCTAssertThrowsError(try manager.setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                return XCTFail("悬空链接应 F1，实际 \(error)")
            }
        }
        manager.refresh()
        XCTAssertEqual(manager.status.readError?.isF1 ?? false, true)

        // 循环 a → b → a
        try FileManager.default.removeItem(atPath: configURL.path)
        try FileManager.default.createSymbolicLink(atPath: grokHome.appendingPathComponent("a").path, withDestinationPath: grokHome.appendingPathComponent("b").path)
        try FileManager.default.createSymbolicLink(atPath: grokHome.appendingPathComponent("b").path, withDestinationPath: grokHome.appendingPathComponent("a").path)
        try FileManager.default.createSymbolicLink(atPath: configURL.path, withDestinationPath: grokHome.appendingPathComponent("a").path)
        XCTAssertThrowsError(try manager.setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                return XCTFail("循环链接应 F1，实际 \(error)")
            }
        }
    }

    // MARK: - T19 非法 TOML

    @MainActor
    func testT19IllegalTOMLF1() throws {
        try writeConfig("this is not valid toml [\n")
        let manager = makeManager()
        XCTAssertThrowsError(try manager.setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                return XCTFail("非法 TOML 应 F1，实际 \(error)")
            }
        }
        XCTAssertEqual(try readConfig(), "this is not valid toml [\n")
        manager.refresh()
        XCTAssertEqual(manager.status.readError?.isF1 ?? false, true)
        XCTAssertTrue(manager.status.isDisabled)
    }

    // MARK: - T20 状态三态 + socket

    @MainActor
    func testT20StatusReportsThreeValuesAndSocket() throws {
        let manager = makeManager()

        manager.refresh()
        XCTAssertEqual(manager.status.value, .absent)
        XCTAssertFalse(manager.status.isON)
        XCTAssertFalse(manager.status.socketPresent)
        XCTAssertEqual(manager.status.paths.configPath, configURL.path)

        try writeConfig("[cli]\nuse_leader = true\n")
        manager.refresh()
        XCTAssertEqual(manager.status.value, .explicitTrue)
        XCTAssertTrue(manager.status.isON)

        try writeConfig("[cli]\nuse_leader = false\n")
        manager.refresh()
        XCTAssertEqual(manager.status.value, .explicitFalse)
        XCTAssertFalse(manager.status.isON)

        try "".write(to: grokHome.appendingPathComponent("leader.sock"), atomically: true, encoding: .utf8)
        manager.refresh()
        XCTAssertTrue(manager.status.socketPresent, "socket 存在性仅 stat")
    }

    // MARK: - T21 相对/多级链

    @MainActor
    func testT21RelativeAndMultiLevelSymlinkChain() throws {
        let real = grokHome.appendingPathComponent("real.toml")
        try "[cli]\ntheme = \"dark\"\n".write(to: real, atomically: true, encoding: .utf8)
        // l1 →(相对) real；config.toml →(相对) l1
        try FileManager.default.createSymbolicLink(atPath: grokHome.appendingPathComponent("l1").path, withDestinationPath: "real.toml")
        try FileManager.default.createSymbolicLink(atPath: configURL.path, withDestinationPath: "l1")

        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try String(contentsOf: real, encoding: .utf8), "[cli]\ntheme = \"dark\"\nuse_leader = true\n")

        // 9 级链超过 8 级上限 → F1
        try FileManager.default.removeItem(atPath: configURL.path)
        var previous = "real.toml"
        for i in 1...9 {
            try FileManager.default.createSymbolicLink(atPath: grokHome.appendingPathComponent("c\(i)").path, withDestinationPath: previous)
            previous = "c\(i)"
        }
        try FileManager.default.createSymbolicLink(atPath: configURL.path, withDestinationPath: previous)
        XCTAssertThrowsError(try makeManager().setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                return XCTFail("9 级链应 F1，实际 \(error)")
            }
        }
    }

    // MARK: - T22 非普通文件目标

    @MainActor
    func testT22NonRegularTargetF1() throws {
        // 目录
        let dir = grokHome.appendingPathComponent("subdir", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(atPath: configURL.path, withDestinationPath: dir.path)
        XCTAssertThrowsError(try makeManager().setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                return XCTFail("目录目标应 F1，实际 \(error)")
            }
        }

        // FIFO
        try FileManager.default.removeItem(atPath: configURL.path)
        let fifo = grokHome.appendingPathComponent("pipe.fifo")
        XCTAssertEqual(mkfifo(fifo.path, 0o600), 0)
        try FileManager.default.createSymbolicLink(atPath: configURL.path, withDestinationPath: fifo.path)
        XCTAssertThrowsError(try makeManager().setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                return XCTFail("FIFO 目标应 F1，实际 \(error)")
            }
        }
    }

    // MARK: - T23 身份钉扎复核失败

    func testT23LinkSwapBeforeRenameAborts() throws {
        let original = "[cli]\ntheme = \"dark\"\n"
        let targetDir = base.appendingPathComponent("swap-targets", isDirectory: true)
        try FileManager.default.createDirectory(at: targetDir, withIntermediateDirectories: true)
        let targetA = targetDir.appendingPathComponent("a.toml")
        let targetB = targetDir.appendingPathComponent("b.toml")
        try original.write(to: targetA, atomically: true, encoding: .utf8)
        try original.write(to: targetB, atomically: true, encoding: .utf8)
        try FileManager.default.createSymbolicLink(atPath: configURL.path, withDestinationPath: targetA.path)

        let newContent = try XCTUnwrap(try GrokLeaderConfigFileEditor.enabledContent(from: original))
        var writer = GrokLeaderConfigWriter(backupDirectory: backupDir())
        writer.testHookBeforeVerify = { _ in
            try? FileManager.default.removeItem(atPath: self.configURL.path)
            try? FileManager.default.createSymbolicLink(atPath: self.configURL.path, withDestinationPath: targetB.path)
        }
        XCTAssertThrowsError(try writer.apply(newContent: newContent, to: configURL.path, expecting: .explicitTrue)) { error in
            guard case GrokLeaderConfigWriter.WriterError.concurrentModification = error else {
                return XCTFail("link swap 应放弃写入，实际 \(error)")
            }
        }
        XCTAssertEqual(try String(contentsOf: targetA, encoding: .utf8), original, "被换下的目标不被覆盖")
        XCTAssertEqual(try String(contentsOf: targetB, encoding: .utf8), original)
    }

    func testT23SamePathSameContentDifferentInode() throws {
        let original = "[cli]\ntheme = \"dark\"\n"
        try writeConfig(original)
        let newContent = try XCTUnwrap(try GrokLeaderConfigFileEditor.enabledContent(from: original))
        var writer = GrokLeaderConfigWriter(backupDirectory: backupDir())
        writer.testHookBeforeVerify = { path in
            let tmp = path + ".replaced"
            try? FileManager.default.copyItem(atPath: path, toPath: tmp)
            try? FileManager.default.removeItem(atPath: path)
            try? FileManager.default.moveItem(atPath: tmp, toPath: path)
        }
        XCTAssertThrowsError(try writer.apply(newContent: newContent, to: configURL.path, expecting: .explicitTrue)) { error in
            guard case GrokLeaderConfigWriter.WriterError.concurrentModification = error else {
                return XCTFail("同路径不同 inode 应放弃写入，实际 \(error)")
            }
        }
        XCTAssertEqual(try readConfig(), original, "内容未被覆盖")
    }

    // MARK: - T24 mode 保留

    @MainActor
    func testT24SymlinkTargetModePreserved() throws {
        // POSIX mode 保留；ACL/xattr 不保留（已声明边界，T24 附注——本测试不创建 ACL）
        try writeConfig("[cli]\ntheme = \"dark\"\n")
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: configURL.path)
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try modeOf(configURL.path), 0o600)

        try makeManager().setLeaderMode(false)
        XCTAssertEqual(try modeOf(configURL.path), 0o600)

        // 0644 同样保留
        try FileManager.default.setAttributes([.posixPermissions: 0o644], ofItemAtPath: configURL.path)
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try modeOf(configURL.path), 0o644)
    }

    // MARK: - T25 备份权限

    @MainActor
    func testT25BackupDirAndFilePermissions() throws {
        try writeConfig("[cli]\ntheme = \"dark\"\n")
        let manager = makeManager()
        try manager.setLeaderMode(true)

        let dir = backupDir()
        XCTAssertTrue(dir.path.hasPrefix(appSupport.path), "备份位于 app support")
        XCTAssertEqual(try modeOf(dir.path), 0o700)
        let names = backupNames()
        XCTAssertEqual(names.count, 1)
        XCTAssertEqual(try modeOf(dir.appendingPathComponent(names[0]).path), 0o600)
    }

    // MARK: - T26 备份中断点

    @MainActor
    func testT26ConvergeFailureFailClosed() throws {
        try precreateBackups(["old0", "old1", "old2"])
        try writeConfig("[cli]\ntheme = \"dark\"\n")
        try FileManager.default.setAttributes([.posixPermissions: 0o500], ofItemAtPath: backupDir().path)
        defer { try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: backupDir().path) }

        XCTAssertThrowsError(try makeManager().setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.ioFailure = error else {
                return XCTFail("收敛失败应 fail-closed，实际 \(error)")
            }
        }
        XCTAssertEqual(try readConfig(), "[cli]\ntheme = \"dark\"\n", "原文件字节不变")
        XCTAssertEqual(backupNames().count, 3, "未删除任何旧份")
    }

    @MainActor
    func testT26CreateFailureAfterConvergeKeepsTwo() throws {
        try precreateBackups(["old0", "old1"])
        try writeConfig("[cli]\ntheme = \"dark\"\n")
        try FileManager.default.setAttributes([.posixPermissions: 0o500], ofItemAtPath: backupDir().path)
        defer { try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: backupDir().path) }

        XCTAssertThrowsError(try makeManager().setLeaderMode(true)) { error in
            guard case GrokLeaderModeManager.SetModeError.ioFailure = error else {
                return XCTFail("创建失败应 fail-closed，实际 \(error)")
            }
        }
        XCTAssertEqual(backupNames().count, 2, "保留 2 份、不恢复已删（本次未删）")
        XCTAssertEqual(try readConfig(), "[cli]\ntheme = \"dark\"\n")
    }

    func testT26CrashAfterBackupSetStaysBounded() throws {
        try writeConfig("[cli]\ntheme = \"dark\"\n")
        let original = try readConfig()
        try precreateBackups(["old0", "old1", "old2"])

        // 模拟：收敛 + 创建成功后、写原文件前崩溃
        let store = GrokConfigBackupStore(directory: backupDir())
        try store.convergeToAtMostTwo()
        _ = try store.createBackup(data: Data(original.utf8))
        XCTAssertEqual(backupNames().count, 3, "崩溃后集合仍 ≤3")
        XCTAssertEqual(try readConfig(), original, "原文件字节不变")
    }

    // MARK: - T27/T28 多行字符串诱饵

    @MainActor
    func testT27BasicMultilineStringDecoys() throws {
        // 真实键 absent → 安全追加不落进字符串
        try writeConfig(
            "[other]\ntext = \"\"\"\n[cli]\nuse_leader = true\n\"\"\"\n\n[cli]\nreal = 1\n"
        )
        try makeManager().setLeaderMode(true)
        let after = try readConfig()
        XCTAssertTrue(after.contains("[cli]\nreal = 1\nuse_leader = true\n"), "追加落进真实 [cli] 节")
        XCTAssertTrue(after.contains("text = \"\"\"\n[cli]\nuse_leader = true\n\"\"\""), "字符串诱饵原样")

        // 真实键存在 + 字符串诱饵 → 恰一 canonical、原位编辑
        try writeConfig(
            "[cli]\nuse_leader = false\nnote = \"\"\"\n[cli]\nuse_leader = true\n\"\"\"\n"
        )
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(
            try readConfig(),
            "[cli]\nuse_leader = true\nnote = \"\"\"\n[cli]\nuse_leader = true\n\"\"\"\n"
        )
    }

    @MainActor
    func testT28LiteralMultilineStringDecoys() throws {
        try writeConfig(
            "[other]\ntext = '''\n[cli]\nuse_leader = true\n'''\n\n[cli]\nreal = 1\n"
        )
        try makeManager().setLeaderMode(true)
        let after = try readConfig()
        XCTAssertTrue(after.contains("[cli]\nreal = 1\nuse_leader = true\n"))
        XCTAssertTrue(after.contains("text = '''\n[cli]\nuse_leader = true\n'''"))

        try writeConfig(
            "[cli]\nuse_leader = false\nnote = '''\nuse_leader = true\n'''\n"
        )
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(
            try readConfig(),
            "[cli]\nuse_leader = true\nnote = '''\nuse_leader = true\n'''\n"
        )
    }

    // MARK: - T29 跨行数组与注释伪 token

    @MainActor
    func testT29CrossLineArrayAndCommentDecoys() throws {
        let original = "[other]\nitems = [\n  \"a]b\",      # not a close\n  \"# not comment\",\n  \"[cli]\",\n  \"use_leader = true\",\n]\n# [cli] pseudo header\n# use_leader = true\n"
        try writeConfig(original)
        try makeManager().setLeaderMode(true)
        let after = try readConfig()
        XCTAssertTrue(after.hasPrefix(original), "数组与注释原样")
        XCTAssertTrue(after.hasSuffix("\n[cli]\nuse_leader = true\n"), "追加在文档末尾新节")
        XCTAssertNotEqual(after, original)
    }

    // MARK: - T30 未闭合结构

    @MainActor
    func testT30UnclosedStructuresF1() throws {
        for text in [
            "[cli]\nnote = \"\"\"\nnever closed\n",
            "[cli]\nitems = [\n1, 2,\n",
        ] {
            try writeConfig(text)
            let manager = makeManager()
            XCTAssertThrowsError(try manager.setLeaderMode(true), "未闭合结构应 F1: \(text)") { error in
                guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                    return XCTFail("应为 F1，实际 \(error)（输入 \(text)）")
                }
            }
            XCTAssertEqual(try readConfig(), text)
        }
    }

    // MARK: - T31 节边界

    @MainActor
    func testT31SectionBoundaries() throws {
        let cases: [(next: String, expected: String)] = [
            ("[other]\nx = 1\n", "[cli]\nreal = 1\n\nuse_leader = true\n[other]\nx = 1\n"),
            ("[cli.child]\ny = 2\n", "[cli]\nreal = 1\n\nuse_leader = true\n[cli.child]\ny = 2\n"),
            ("[[other.items]]\nz = 3\n", "[cli]\nreal = 1\n\nuse_leader = true\n[[other.items]]\nz = 3\n"),
        ]
        for (next, expected) in cases {
            try writeConfig("[cli]\nreal = 1\n\n\(next)")
            try makeManager().setLeaderMode(true)
            XCTAssertEqual(try readConfig(), expected, "边界: \(next)")
        }
    }

    // MARK: - T32 无尾随换行

    @MainActor
    func testT32NoTrailingNewline() throws {
        try writeConfig("[cli]\nreal = 1")
        try makeManager().setLeaderMode(true)
        XCTAssertEqual(try readConfig(), "[cli]\nreal = 1\nuse_leader = true\n")

        // 写后仍是合法 TOML 且语义正确（读回）
        let manager = makeManager()
        manager.refresh()
        XCTAssertEqual(manager.status.value, .explicitTrue)

        // [cli] 是最后一节 + 无尾随换行的追加路径
        try writeConfig("[other]\nx = 1")
        try makeManager().setLeaderMode(true)
        let after = try readConfig()
        XCTAssertEqual(after, "[other]\nx = 1\n\n[cli]\nuse_leader = true\n")
        XCTAssertFalse(after.contains("\r"), "无混合行尾")
    }

    // MARK: - T33 类型非法

    @MainActor
    func testT33TypeInvalidValuesF1() throws {
        for text in [
            "[cli]\nuse_leader = \"true\"\n",
            "[cli]\nuse_leader = 1\n",
            "[cli]\nuse_leader = [true]\n",
        ] {
            try writeConfig(text)
            let manager = makeManager()
            XCTAssertThrowsError(try manager.setLeaderMode(true), "类型非法应 F1: \(text)") { error in
                guard case GrokLeaderModeManager.SetModeError.f1 = error else {
                    return XCTFail("应为 F1，实际 \(error)（输入 \(text)）")
                }
            }
            XCTAssertEqual(try readConfig(), text, "不追加第二语义键: \(text)")

            manager.refresh()
            XCTAssertEqual(manager.status.readError?.isF1 ?? false, true, "状态 F1: \(text)")
        }
    }
}

private extension GrokLeaderReadError {
    var isF1: Bool {
        if case .f1 = self { return true }
        return false
    }
}
