import Darwin
import Foundation

/// OpenCodeInstaller（2026-10-06 方案 §4）：OpenCode 的代装器。镜像
/// agent/dsh-web/installer.go 的约束，落在 Swift——OpenCode managed server 的
/// spawn 在 Swift（方案 §6），安装动作与 CLI 发现同侧。约束：npm 发现 →
/// `npm install -g opencode-ai@<versionSpec>`（无 sudo、无 npx、不用 sh -c）→
/// `opencode --version` exit 0 验证 → 0600 安装记录（无凭据）→ CLI 解析
/// record-first。全局 prefix 不可写时回退
/// `npm install --prefix <dataDir>/opencode-install opencode-ai@<versionSpec>`。
/// 不做 brew / curl 脚本安装；Node 不在范围内（无 node/npm → 「需要 Node.js」）。
enum OpenCodeInstaller {
    /// 安装的版本 spec（OD-2=A，owner 2026-10-06 裁决）：钉 generation118 线，
    /// npm 解析到最高 1.18.x。backend 的 clientFor 只认 generation118
    /// （fail-closed），装已知会被隔离的 @latest 等于制造失败；backend 支持
    /// v2 后随代码升 spec。
    static let versionSpec = "1.18"

    /// discoverNpm finds an absolute npm path for the GUI environment: the
    /// CLI search path first（GUI 不继承 shell PATH——dsh 方案 §4 同款坑），
    /// then the newest nvm node version's bin. Returns nil when Node is not
    /// present（「需要 Node.js」分支的输入）。输入与
    /// DefaultOpenCodeCLIResolver.resolveOpenCodeCLI 同契约（config 已展开 ~）。
    static func discoverNpm(cliSearchPath: [String]) -> String? {
        let fm = FileManager.default
        for directory in cliSearchPath {
            let candidate = URL(fileURLWithPath: directory).appendingPathComponent("npm").path
            if fm.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        return latestNvmBinary("npm")
    }

    /// latestNvmBinary finds <name> under the newest ~/.nvm/versions/node/v*
    /// that has it（镜像 dsh binary.go 的 latestNvmBinary：GUI 不继承 nvm 的
    /// shell hook，目录扫描是唯一可靠途径）。
    static func latestNvmBinary(_ name: String) -> String? {
        let versionsDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".nvm/versions/node").path
        guard let entries = try? FileManager.default.contentsOfDirectory(atPath: versionsDir) else {
            return nil
        }
        let versions = entries
            .filter { $0.hasPrefix("v") }
            .compactMap { dir -> (major: Int, minor: Int, patch: Int, dir: String)? in
                guard let (major, minor, patch) = parseNodeVersion(dir) else { return nil }
                return (major, minor, patch, dir)
            }
            .sorted { a, b in
                (a.major, a.minor, a.patch) > (b.major, b.minor, b.patch)
            }
        for version in versions {
            let candidate = versionsDir + "/" + version.dir + "/bin/" + name
            if FileManager.default.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        return nil
    }

    /// parseNodeVersion parses "v22.11.0"（leading v, exactly three parts）.
    static func parseNodeVersion(_ name: String) -> (Int, Int, Int)? {
        guard name.hasPrefix("v") else { return nil }
        let parts = name.dropFirst().split(separator: ".").compactMap { Int($0) }
        guard parts.count == 3 else { return nil }
        return (parts[0], parts[1], parts[2])
    }

    // MARK: - 安装流（方案 §4）

    /// installTimeout bounds one npm install; on expiry the whole npm process
    /// group is killed（方案 §4：10 分钟超时杀进程组）.
    static let installTimeout: TimeInterval = 600
    /// installQueryTimeout bounds the read-only `npm prefix -g` query.
    static let installQueryTimeout: TimeInterval = 15
    /// installBinTimeout bounds the `opencode --version` verification.
    static let installBinTimeout: TimeInterval = 15
    /// npm 包名（官方 README.md:53 入口；spec 见 versionSpec）。
    static let npmPackage = "opencode-ai"
    /// fallbackPrefixDir is the CordCode data-dir prefix used when the global
    /// prefix is unwritable（方案 §4 prefix 回退）.
    static let fallbackPrefixDir = "opencode-install"
    /// 安装记录文件名（镜像 dsh dsh-web-install-record.json）。
    static let installRecordFile = "opencode-install-record.json"

    /// OpenCodeInstallError carries the user-facing failure message（安装失败
    /// 的界面字幕原文）.
    struct InstallError: Error, Equatable {
        let message: String
    }

    /// InstallSuccess is a verified install outcome（--version exit 0 才算成功）.
    struct InstallSuccess: Equatable {
        let binPath: String
        /// user-global | cordcode-prefix
        let scope: String
        /// 界面备注（prefix 回退时说明「已装到 CordCode 目录」）。
        let note: String
        /// `opencode --version` 输出（仅诊断）。
        let version: String
    }

    /// OpenCodeInstallRecord 是 0600、无凭据的安装记录：bin 路径、scope、
    /// 版本、时间（镜像 dsh installRecord）。
    struct InstallRecord: Codable, Equatable {
        var version: Int = 1
        var binPath: String
        var scope: String
        var opencodeVersion: String
        var installedAt: String

        enum CodingKeys: String, CodingKey {
            case version
            case binPath = "bin_path"
            case scope
            case opencodeVersion = "opencode_version"
            case installedAt = "installed_at"
        }
    }

    /// install runs the full chain（blocking，受 10 分钟预算约束；调用方跑在
    /// 非 MainActor）：user-global 安装 → prefix 回退 → --version 验证 →
    /// 0600 安装记录。argv 恒为 `npm install [-g|--prefix <dir>]
    /// opencode-ai@<versionSpec>`——无 sudo、无 npx、不用 sh -c。
    static func install(
        npmPath: String,
        dataDir: String,
        runner: OpenCodeProcessRunning = DefaultOpenCodeProcessRunner.shared
    ) -> Result<InstallSuccess, InstallError> {
        switch npmInstallGlobal(npmPath: npmPath, runner: runner) {
        case .success(let success):
            writeInstallRecord(dataDir: dataDir, record: record(for: success))
            return .success(success)
        case .failure(let globalError):
            let prefix = dataDir + "/" + fallbackPrefixDir
            switch npmInstallPrefix(npmPath: npmPath, prefix: prefix, runner: runner) {
            case .success(let prefixed):
                let success = InstallSuccess(
                    binPath: prefixed.binPath,
                    scope: "cordcode-prefix",
                    note: "已装到 CordCode 目录，终端里不一定有 opencode",
                    version: prefixed.version
                )
                writeInstallRecord(dataDir: dataDir, record: record(for: success))
                return .success(success)
            case .failure(let prefixError):
                return .failure(InstallError(message: "user-global 安装失败：\(globalError.message)；CordCode 目录安装失败：\(prefixError.message)"))
            }
        }
    }

    /// npmInstallGlobal runs `npm install -g opencode-ai@<spec>`, locates the
    /// bin via the same npm's `prefix -g`, and verifies `opencode --version`.
    static func npmInstallGlobal(npmPath: String, runner: OpenCodeProcessRunning) -> Result<InstallSuccess, InstallError> {
        let spec = "\(npmPackage)@\(versionSpec)"
        let run = runner.run(
            executable: npmPath,
            arguments: ["install", "-g", spec],
            extraPATH: (npmPath as NSString).deletingLastPathComponent,
            timeout: installTimeout
        )
        if let failure = runFailure(run, argv: ["install", "-g", spec]) {
            return .failure(InstallError(message: failure))
        }
        guard let prefix = npmGlobalPrefix(npmPath: npmPath, runner: runner) else {
            return .failure(InstallError(message: "npm prefix -g 查询失败"))
        }
        let bin = prefix + "/bin/opencode"
        guard FileManager.default.isExecutableFile(atPath: bin) else {
            return .failure(InstallError(message: "npm 安装完成但未找到可执行 opencode（期望 \(bin)）"))
        }
        return verifyBin(bin: bin, runner: runner)
    }

    /// npmInstallPrefix runs the fallback `npm install --prefix <dir>
    /// opencode-ai@<spec>` into the CordCode data dir（bins land in
    /// <dir>/node_modules/.bin）.
    static func npmInstallPrefix(npmPath: String, prefix: String, runner: OpenCodeProcessRunning) -> Result<InstallSuccess, InstallError> {
        let spec = "\(npmPackage)@\(versionSpec)"
        let run = runner.run(
            executable: npmPath,
            arguments: ["install", "--prefix", prefix, spec],
            extraPATH: (npmPath as NSString).deletingLastPathComponent,
            timeout: installTimeout
        )
        if let failure = runFailure(run, argv: ["install", "--prefix", prefix, spec]) {
            return .failure(InstallError(message: failure))
        }
        let bin = prefix + "/node_modules/.bin/opencode"
        guard FileManager.default.isExecutableFile(atPath: bin) else {
            return .failure(InstallError(message: "npm 安装完成但未找到可执行 opencode（期望 \(bin)）"))
        }
        return verifyBin(bin: bin, runner: runner)
    }

    /// verifyBin runs `opencode --version`——安装只在 exit 0 时算成功
    /// （方案 §4：--version 失败 = 安装失败，不启动）。
    static func verifyBin(bin: String, runner: OpenCodeProcessRunning) -> Result<InstallSuccess, InstallError> {
        let run = runner.run(
            executable: bin,
            arguments: ["--version"],
            extraPATH: (bin as NSString).deletingLastPathComponent,
            timeout: installBinTimeout
        )
        if run.timedOut {
            return .failure(InstallError(message: "opencode --version 验证超时（\(Int(installBinTimeout)) 秒），已终止"))
        }
        if run.exitCode != 0 {
            return .failure(InstallError(message: "opencode --version 验证失败（exit \(run.exitCode)）：\(lastNonEmptyLine(run.stderr, run.stdout))"))
        }
        return .success(InstallSuccess(
            binPath: bin,
            scope: "user-global",
            note: "",
            version: run.stdout.trimmingCharacters(in: .whitespacesAndNewlines)
        ))
    }

    /// npmGlobalPrefix asks the same npm binary for its global prefix
    /// （read-only query；安装命令本身保持 `npm install -g <pkg>` 不变）.
    static func npmGlobalPrefix(npmPath: String, runner: OpenCodeProcessRunning) -> String? {
        let run = runner.run(
            executable: npmPath,
            arguments: ["prefix", "-g"],
            extraPATH: (npmPath as NSString).deletingLastPathComponent,
            timeout: installQueryTimeout
        )
        guard !run.timedOut, run.exitCode == 0 else { return nil }
        let prefix = run.stdout.trimmingCharacters(in: .whitespacesAndNewlines)
        return prefix.isEmpty ? nil : prefix
    }

    /// runFailure maps a runner outcome to the user-facing failure text
    /// （超时写明超时；失败用最后一段非空错误——方案 §4）。
    static func runFailure(_ run: OpenCodeProcessRunResult, argv: [String]) -> String? {
        let command = "npm " + argv.joined(separator: " ")
        if run.timedOut {
            return "\(command) 超时（\(Int(installTimeout / 60)) 分钟），已终止"
        }
        if run.exitCode != 0 {
            return "\(command) 失败：\(lastNonEmptyLine(run.stderr, run.stdout))"
        }
        return nil
    }

    /// writeInstallRecord persists the install（0600、无凭据）.
    static func writeInstallRecord(dataDir: String, record: InstallRecord) {
        do {
            try FileManager.default.createDirectory(atPath: dataDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            let encoder = JSONEncoder()
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            let data = try encoder.encode(record)
            try data.write(to: URL(fileURLWithPath: recordPath(dataDir: dataDir)), options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: recordPath(dataDir: dataDir))
        } catch {
            NSLog("[OpenCodeInstaller] install record write failed: \(error.localizedDescription)")
        }
    }

    /// readInstallRecord returns the persisted record, or nil when absent or
    /// undecodable（CLI 解析 record-first 的输入）.
    static func readInstallRecord(dataDir: String) -> InstallRecord? {
        guard let data = FileManager.default.contents(atPath: recordPath(dataDir: dataDir)) else { return nil }
        return try? JSONDecoder().decode(InstallRecord.self, from: data)
    }

    static func recordPath(dataDir: String) -> String {
        dataDir + "/" + installRecordFile
    }

    private static func record(for success: InstallSuccess) -> InstallRecord {
        InstallRecord(
            binPath: success.binPath,
            scope: success.scope,
            opencodeVersion: success.version,
            installedAt: ISO8601DateFormatter().string(from: Date())
        )
    }

    /// lastNonEmptyLine picks the last non-empty line of the given sources
    /// （stderr 优先）as the user-facing install error（方案 §4：界面字幕用
    /// 最后一段非空错误）.
    static func lastNonEmptyLine(_ sources: String...) -> String {
        for source in sources {
            let lines = source.trimmingCharacters(in: .whitespacesAndNewlines).split(separator: "\n")
            if let last = lines.last(where: { !$0.trimmingCharacters(in: .whitespaces).isEmpty }) {
                return String(last)
            }
        }
        return "unknown npm error"
    }

    /// redactNpmOutput masks npm token-shaped secrets before logging（镜像
    /// dsh redactNpmOutput；安装命令本身不传任何凭据，这里只保护日志）.
    static func redactNpmOutput(_ s: String) -> String {
        var result = s
        result = result.replacingOccurrences(
            of: "npm_[A-Za-z0-9]{16,}",
            with: "npm_[REDACTED]",
            options: .regularExpression
        )
        result = result.replacingOccurrences(
            of: "//[^/\\s]+:[^@\\s]+@",
            with: "//[REDACTED]@",
            options: .regularExpression
        )
        return result
    }
}

// MARK: - Process runner（注入式 seam）

/// OpenCodeProcessRunResult is one process run outcome.
struct OpenCodeProcessRunResult: Equatable {
    let exitCode: Int32
    let stdout: String
    let stderr: String
    let timedOut: Bool
}

/// OpenCodeProcessRunning is the installer's process seam（测试注入 stub，
/// 不访问真实注册表）。实现方拥有超时与进程组终止语义。
protocol OpenCodeProcessRunning {
    /// Runs the executable with the exact argv. `extraPATH` is prepended to
    /// PATH（npm 的 node 依赖 PATH 可见——GUI 不继承 shell PATH）。
    func run(executable: String, arguments: [String], extraPATH: String?, timeout: TimeInterval) -> OpenCodeProcessRunResult
}

/// DefaultOpenCodeProcessRunner spawns each command as its own process group
/// （POSIX_SPAWN_SETPGROUP）so the 10-minute timeout can kill the whole npm
/// group（npm 的 node 子进程一并终止，方案 §4）。stdout/stderr 在后台线程
/// 读取避免管道缓冲死锁；输出打码后进 Link 日志。
final class DefaultOpenCodeProcessRunner: OpenCodeProcessRunning {
    static let shared = DefaultOpenCodeProcessRunner()

    /// sys/wait.h 的 WIFEXITED/WEXITSTATUS 宏未桥接到 Swift；按 POSIX 稳定
    /// 语义手写等价位运算。
    private static func wifExited(_ status: Int32) -> Bool { (status & 0x7F) == 0 }
    private static func wExitStatus(_ status: Int32) -> Int32 { (status >> 8) & 0xFF }

    func run(executable: String, arguments: [String], extraPATH: String?, timeout: TimeInterval) -> OpenCodeProcessRunResult {
        let outPipe = Pipe()
        let errPipe = Pipe()

        var attr: posix_spawnattr_t?
        posix_spawnattr_init(&attr)
        posix_spawnattr_setflags(&attr, Int16(POSIX_SPAWN_SETPGROUP))
        var fileActions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&fileActions)
        posix_spawn_file_actions_adddup2(&fileActions, outPipe.fileHandleForWriting.fileDescriptor, 1)
        posix_spawn_file_actions_adddup2(&fileActions, errPipe.fileHandleForWriting.fileDescriptor, 2)

        var environment = ProcessInfo.processInfo.environment
        var path = environment["PATH"] ?? ""
        if let extraPATH, !extraPATH.isEmpty {
            path = extraPATH + ":" + path
        }
        environment["PATH"] = path

        let argv: [UnsafeMutablePointer<CChar>?] = ([executable] + arguments).map { strdup($0) } + [nil]
        defer { for arg in argv.dropLast() { free(arg) } }
        let envp: [UnsafeMutablePointer<CChar>?] = environment.map { strdup("\($0.key)=\($0.value)") } + [nil]
        defer { for entry in envp.dropLast() { free(entry) } }

        var pid: pid_t = 0
        let rc = posix_spawn(&pid, executable, &fileActions, &attr, argv, envp)
        posix_spawnattr_destroy(&attr)
        posix_spawn_file_actions_destroy(&fileActions)
        guard rc == 0, pid > 0 else {
            return OpenCodeProcessRunResult(exitCode: rc, stdout: "", stderr: "posix_spawn failed: \(rc)", timedOut: false)
        }

        // 父进程关闭写端：子进程退出后读端才能见到 EOF。
        try? outPipe.fileHandleForWriting.close()
        try? errPipe.fileHandleForWriting.close()

        // 后台线程读两根管道，避免子进程写满缓冲后与 waitpid 互相卡死；
        // 信号量在收口前 join 两个读线程。
        var outData = Data()
        var errData = Data()
        let outSemaphore = DispatchSemaphore(value: 0)
        let errSemaphore = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            outData = outPipe.fileHandleForReading.readDataToEndOfFile()
            outSemaphore.signal()
        }
        DispatchQueue.global().async {
            errData = errPipe.fileHandleForReading.readDataToEndOfFile()
            errSemaphore.signal()
        }

        let deadline = Date().addingTimeInterval(timeout)
        var status: Int32 = 0
        var timedOut = false
        while true {
            let waited = waitpid(pid, &status, WNOHANG)
            if waited == pid {
                break
            }
            if waited < 0 {
                return OpenCodeProcessRunResult(exitCode: -1, stdout: "", stderr: "waitpid failed: \(errno)", timedOut: false)
            }
            if Date() > deadline {
                timedOut = true
                // 子进程是自己的组长（SETPGROUP）：负 pid 杀整组，npm 的 node
                // 子进程一并终止；再补一次直杀兜底。
                kill(-pid, SIGKILL)
                kill(pid, SIGKILL)
                waitpid(pid, &status, 0)
                break
            }
            usleep(50_000)
        }
        outSemaphore.wait()
        errSemaphore.wait()

        let stdout = String(data: outData, encoding: .utf8) ?? ""
        let stderr = String(data: errData, encoding: .utf8) ?? ""
        if timedOut {
            NSLog("[OpenCodeInstaller] process timed out after \(Int(timeout))s: \(executable) \(arguments.joined(separator: " "))")
        } else {
            NSLog("[OpenCodeInstaller] npm finished: \(arguments.joined(separator: " ")) stdout=\(OpenCodeInstaller.redactNpmOutput(stdout)) stderr=\(OpenCodeInstaller.redactNpmOutput(stderr))")
        }
        return OpenCodeProcessRunResult(
            exitCode: timedOut ? -1 : (Self.wifExited(status) ? Self.wExitStatus(status) : -1),
            stdout: stdout,
            stderr: stderr,
            timedOut: timedOut
        )
    }
}
