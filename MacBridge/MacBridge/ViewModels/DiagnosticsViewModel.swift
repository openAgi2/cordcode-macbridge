import AppKit
import Foundation

/// 诊断工作表的专属状态归属（UX 重设计 2026-07-13 P2-2 / P0-4）。
///
/// 把原 `ContentView` 的 `logs`/`isLoadingLogs`/`logsError`/`loadLogs`/`readTailLines`/
/// `displayLogLine` 迁出，并新增健康摘要与脱敏「复制支持信息」。健康摘要派生自
/// `BridgeStatusViewModel`/`BackendStatusViewModel`，但只读取已暴露的展示字段，**绝不**
/// 在支持信息中写入 route id / token / 密码 / endpoint 凭据。
@MainActor
final class DiagnosticsViewModel: ObservableObject {
    @Published private(set) var logs: [String] = []
    @Published private(set) var isLoadingLogs = false
    @Published private(set) var logsError: String?
    /// 最近一次「复制支持信息」成功后短暂为 true，用于按钮回执。
    @Published private(set) var supportInfoCopied = false

    /// 浏览显示行数：默认 30，展开后 200。仅影响 UI 渲染，不影响加载和 copyRaw。
    @Published var maxDisplayLines: Int = 30

    private var logFilePath: String?

    /// 注入 go-bridge 日志路径（由 RuntimeManager.config.logFilePath 提供）。
    func configure(logFilePath: String?) {
        self.logFilePath = logFilePath
    }

    /// 拉取最后 200 行 / 1 MiB 原始日志。
    func loadLogs() async {
        guard !isLoadingLogs else { return }
        isLoadingLogs = true
        defer { isLoadingLogs = false }

        guard let path = logFilePath, !path.isEmpty else {
            logsError = L10n.noLogsAvailable
            return
        }
        let result = await Task.detached(priority: .utility) {
            Self.readTailLines(at: path, maxLines: 200, maxBytes: 1_048_576)
        }.value
        switch result {
        case .success(let lines):
            logs = lines
            logsError = nil
        case .failure(let error):
            logsError = error.localizedDescription
        }
    }

    /// 复制原始日志到剪贴板。（始终使用完整 200 行 raw）
    func copyRawLogs() {
        let text = logs.joined(separator: "\n")
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    /// 切换显示 30 行 / 200 行（仅 UI）
    func toggleFullLogs() {
        maxDisplayLines = (maxDisplayLines == 30 ? 200 : 30)
    }

    /// 生成并复制脱敏支持信息：Bridge/连接/AI 工具三个结论 + 版本/端口/uptime，
    /// **不包含** route id / token / 密码 / endpoint 凭据。
    func copySupportInfo(bridgeStatus: BridgeStatusViewModel, backendStatus: BackendStatusViewModel) {
        let text = Self.buildSupportInfo(bridgeStatus: bridgeStatus, backendStatus: backendStatus)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        supportInfoCopied = true
        Task {
            try? await Task.sleep(for: .seconds(2))
            supportInfoCopied = false
        }
    }

    /// 截断超长日志行用于展示。
    static func displayLogLine(_ line: String) -> String {
        let maxCharacters = 500
        guard line.count > maxCharacters else { return line }
        return String(line.prefix(maxCharacters)) + " …"
    }

    /// 构造脱敏支持信息（可被测试单独调用）。
    static func buildSupportInfo(
        bridgeStatus: BridgeStatusViewModel,
        backendStatus: BackendStatusViewModel
    ) -> String {
        var lines: [String] = []
        lines.append("=== CordCode Link 支持信息 ===")
        lines.append("[Bridge] 状态: \(bridgeStatus.status.rawValue)")
        if let version = bridgeStatus.managementStatus?.version, !version.isEmpty {
            lines.append("[Bridge] 版本: \(version)")
        }
        if let uptime = bridgeStatus.managementStatus?.uptime, !uptime.isEmpty {
            lines.append("[Bridge] uptime: \(uptime)")
        }
        if let port = bridgeStatus.bridgePort {
            lines.append("[Bridge] 端口: \(port)")
        }
        switch bridgeStatus.relayConfigured {
        case true: lines.append("[连接] Relay: 已配置")
        case false: lines.append("[连接] Relay: 未配置")
        case nil: lines.append("[连接] Relay: 不可用")
        }
        let backendSummary = backendStatus.agents.map { "\($0.displayName): \($0.status)" }
        let bridgeSummary = bridgeStatus.agents.map { "\($0.displayName): \($0.status)" }
        let agentSummary = backendStatus.agents.isEmpty ? bridgeSummary : backendSummary
        if agentSummary.isEmpty {
            lines.append("[AI 工具] 无")
        } else {
            lines.append("[AI 工具] \(agentSummary.joined(separator: ", "))")
        }
        if let error = bridgeStatus.lastError, !error.isEmpty {
            lines.append("[错误] \(error)")
        }
        return lines.joined(separator: "\n")
    }

    private nonisolated static func readTailLines(
        at path: String,
        maxLines: Int,
        maxBytes: UInt64
    ) -> Result<[String], Error> {
        let handle: FileHandle
        do {
            handle = try FileHandle(forReadingFrom: URL(fileURLWithPath: path))
        } catch {
            return .failure(error)
        }
        defer { try? handle.close() }

        do {
            let fileSize = try handle.seekToEnd()
            let bytesToRead = min(fileSize, maxBytes)
            guard bytesToRead > 0 else { return .success([]) }
            try handle.seek(toOffset: fileSize - bytesToRead)
            guard let data = try handle.readToEnd(),
                  let text = String(data: data, encoding: .utf8) else {
                return .success([])
            }
            let lines = text.split(separator: "\n", omittingEmptySubsequences: true).map(String.init)
            return .success(Array(lines.suffix(maxLines)))
        } catch {
            return .failure(error)
        }
    }
}
