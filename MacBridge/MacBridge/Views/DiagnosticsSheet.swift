import SwiftUI

/// 帮助与诊断工作表（UX 重设计 2026-07-13 P2-2）。
///
/// 先给可读的健康摘要（Bridge / 连接 / AI 工具），再给针对失败项的「复制支持信息」（脱敏），
/// 原始日志作为同窗第二段或经「查看原始日志」打开。错误文案统一为
/// 「发生了什么 → 影响 → 现在可以做什么」。
struct DiagnosticsSheet: View {
    @ObservedObject var diagnosticsViewModel: DiagnosticsViewModel
    @ObservedObject var bridgeStatus: BridgeStatusViewModel
    @ObservedObject var backendStatus: BackendStatusViewModel

    @State private var showRawLogs = true
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        PageContainer(scrolls: false) {
            VStack(alignment: .leading, spacing: 20) {
                // Header 区域
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text(L10n.helpDiagnostics)
                            .font(.system(size: 22, weight: .bold))
                        Text(L10n.diagnosticsSubtitle)
                            .font(.system(size: 13))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    
                    HStack(spacing: 12) {
                        Button {
                            Task { await diagnosticsViewModel.loadLogs() }
                        } label: {
                            HStack(spacing: 6) {
                                if diagnosticsViewModel.isLoadingLogs {
                                    ProgressView().controlSize(.small)
                                } else {
                                    Image(systemName: "arrow.clockwise")
                                }
                                Text(L10n.refreshAll)
                            }
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.regular)
                        .disabled(diagnosticsViewModel.isLoadingLogs)

                        Button {
                            diagnosticsViewModel.copySupportInfo(bridgeStatus: bridgeStatus, backendStatus: backendStatus)
                        } label: {
                            Label(
                                diagnosticsViewModel.supportInfoCopied ? L10n.diagnosticsSupportInfoCopied : L10n.diagnosticsCopySupportInfo,
                                systemImage: diagnosticsViewModel.supportInfoCopied ? "checkmark" : "doc.on.clipboard"
                            )
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.regular)
                    }
                }
                .padding(.bottom, 4)

                // 健康摘要三卡片并排
                healthSummarySection

                Divider()
                    .padding(.vertical, 4)

                // 原始日志及终端输出
                if showRawLogs {
                    rawLogsSection
                } else {
                    Button {
                        withAnimation(.easeInOut(duration: 0.2)) {
                            showRawLogs = true
                        }
                    } label: {
                        Label(L10n.diagnosticsViewRawLogs, systemImage: "doc.text")
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.regular)
                }

                Spacer(minLength: 0)

                // 底部完成按钮
                HStack {
                    Spacer()
                    Button(L10n.done) { dismiss() }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.regular)
                }
            }
        }
        .frame(width: LayoutConstants.unifiedSheetWidth, height: LayoutConstants.unifiedSheetHeight)
        .task {
            await diagnosticsViewModel.loadLogs()
        }
    }

    // MARK: - 健康摘要（结论优先）

    private var healthSummarySection: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(L10n.diagnosticsHealthSummary)
                .font(.system(size: 14, weight: .bold))
                .foregroundStyle(.secondary)
                .padding(.leading, 4)

            HStack(spacing: 12) {
                HealthSummaryCard(
                    title: L10n.diagnosticsHealthBridge,
                    status: bridgeSummaryText,
                    icon: "waveform.path.ecg",
                    color: bridgeColor
                )
                HealthSummaryCard(
                    title: L10n.diagnosticsHealthConnection,
                    status: connectionSummaryText,
                    icon: "wifi",
                    color: connectionColor
                )
                HealthSummaryCard(
                    title: L10n.diagnosticsHealthAiTools,
                    status: aiToolsSummaryText,
                    icon: "sparkles",
                    color: aiToolsColor
                )
            }
        }
    }

    private var bridgeSummaryText: String {
        switch bridgeStatus.status {
        case .ready: return L10n.overviewRunning
        case .readyNoAgents: return L10n.overviewRunningNoAgents
        case .starting: return L10n.overviewStarting
        case .stopped: return L10n.overviewStopped
        case .crashed: return L10n.overviewStartFailed
        case .sleeping: return L10n.overviewSleeping
        case .idle: return L10n.overviewIdle
        }
    }

    private var bridgeColor: Color {
        switch bridgeStatus.status {
        case .ready: return .green
        case .readyNoAgents, .starting, .sleeping: return .orange
        case .crashed: return .red
        case .stopped, .idle: return .secondary
        }
    }

    private var connectionSummaryText: String {
        switch bridgeStatus.relayConfigured {
        case true: return L10n.configured
        case false: return L10n.notConfigured
        case nil: return L10n.overviewRelayUnavailable
        }
    }

    private var connectionColor: Color {
        switch bridgeStatus.relayConfigured {
        case true: return .green
        case false: return .secondary
        case nil: return .orange
        }
    }

    private var aiToolsSummaryText: String {
        let hasAnyAgents = !backendStatus.agents.isEmpty || !bridgeStatus.agents.isEmpty
        if !hasAnyAgents { return L10n.noAiToolsDetected }
        let ready = backendStatus.agents.filter(\.isAvailable).count
        return String(format: L10n.aiToolsReady, ready)
    }

    private var aiToolsColor: Color {
        backendStatus.agents.contains { !$0.isAvailable } ? .orange : .green
    }

    // MARK: - 原始日志（第二段）

    private var rawLogsSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 8) {
                Text(L10n.rawLogs)
                    .font(.system(size: 14, weight: .bold))
                    .foregroundStyle(.secondary)
                    .padding(.leading, 4)
                
                Text(diagnosticsViewModel.maxDisplayLines == 30 ? "最近 30 行" : "最近 200 行")
                    .font(.system(size: 10, weight: .medium))
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.white.opacity(0.08))
                    .foregroundStyle(.secondary)
                    .clipShape(Capsule())
                
                Spacer()
                
                Button {
                    diagnosticsViewModel.copyRawLogs()
                } label: {
                    Label(L10n.copyRawLogs, systemImage: "doc.on.clipboard")
                }
                .buttonStyle(.bordered)
                .controlSize(.regular)
                .disabled(diagnosticsViewModel.logs.isEmpty)
            }

            if diagnosticsViewModel.isLoadingLogs && diagnosticsViewModel.logs.isEmpty {
                VStack {
                    Spacer()
                    ProgressView(L10n.diagnosticsReading)
                    Spacer()
                }
                .frame(maxWidth: .infinity, minHeight: 220)
                .background(Color.black.opacity(0.85))
                .cornerRadius(10)
                .overlay {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.white.opacity(0.1), lineWidth: 1)
                }
            } else if let error = diagnosticsViewModel.logsError {
                InlineFeedback(style: .error, message: error)
            } else if diagnosticsViewModel.logs.isEmpty {
                VStack {
                    Spacer()
                    Text(L10n.noLogsAvailable).foregroundColor(.secondary)
                    Spacer()
                }
                .frame(maxWidth: .infinity, minHeight: 220)
                .background(Color.black.opacity(0.85))
                .cornerRadius(10)
                .overlay {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.white.opacity(0.1), lineWidth: 1)
                }
            } else {
                let displayed = Array(diagnosticsViewModel.logs.suffix(diagnosticsViewModel.maxDisplayLines))
                ScrollView(.vertical) {
                    LazyVStack(alignment: .leading, spacing: 4) {
                        ForEach(Array(displayed.enumerated()), id: \.offset) { _, line in
                            formattedLogLine(DiagnosticsViewModel.displayLogLine(line))
                        }
                    }
                    .padding(12)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Color.black.opacity(0.85))
                .cornerRadius(10)
                .overlay {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.white.opacity(0.1), lineWidth: 1)
                }

                HStack {
                    Button {
                        withAnimation(.easeInOut(duration: 0.2)) {
                            diagnosticsViewModel.toggleFullLogs()
                        }
                    } label: {
                        HStack(spacing: 6) {
                            Image(systemName: "eye.fill")
                                .font(.system(size: 11))
                            Text(diagnosticsViewModel.maxDisplayLines == 30 ? "查看完整 200 行" : "显示最近 30 行")
                                .font(.system(size: 12))
                        }
                        .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .contentShape(Rectangle())
                    
                    Spacer()
                }
                .padding(.top, 4)
            }
        }
    }

    @ViewBuilder
    private func formattedLogLine(_ line: String) -> some View {
        if line.count >= 23, line.hasPrefix("20") {
            let timestamp = String(line.prefix(23))
            let remaining = line.dropFirst(23)
            let parsed = parseLogLevel(remaining)
            
            HStack(spacing: 6) {
                Text(timestamp)
                    .foregroundStyle(.secondary.opacity(0.7))
                if !parsed.level.isEmpty {
                    Text(parsed.level)
                        .foregroundStyle(parsed.color)
                        .fontWeight(.bold)
                }
                Text(parsed.body)
                    .foregroundStyle(.white.opacity(0.9))
            }
            .font(.system(size: 11, design: .monospaced))
            .lineLimit(1)
            .truncationMode(.middle)
        } else {
            Text(line)
                .foregroundStyle(.white.opacity(0.9))
                .font(.system(size: 11, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
        }
    }

    private func parseLogLevel(_ remaining: String.SubSequence) -> (level: String, color: Color, body: String) {
        var levelColor = Color.blue
        var levelStr = ""
        var bodyStr = String(remaining)
        
        let levels = ["INFO", "ERROR", "WARN", "DEBG", "DEBUG"]
        for lvl in levels {
            if remaining.contains(lvl) {
                levelStr = lvl
                if lvl == "ERROR" { levelColor = .red }
                else if lvl == "WARN" { levelColor = .orange }
                else if lvl == "INFO" { levelColor = .cyan }
                else { levelColor = .gray }
                
                if let range = remaining.range(of: lvl) {
                    bodyStr = String(remaining[range.upperBound...])
                }
                break
            }
        }
        return (levelStr, levelColor, bodyStr)
    }
}

private struct HealthSummaryCard: View {
    let title: String
    let status: String
    let icon: String
    let color: Color
    
    @State private var isHovering = false

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                color.opacity(0.15)
                Image(systemName: icon)
                    .font(.system(size: 16, weight: .bold))
                    .foregroundStyle(color)
            }
            .frame(width: 32, height: 32)
            .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))

            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(.primary)
                HStack(spacing: 4) {
                    Text("●")
                        .font(.system(size: 8))
                        .foregroundStyle(color)
                    Text(status)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
            }
            
            Spacer()

            Image(systemName: "chevron.right")
                .font(.system(size: 10, weight: .bold))
                .foregroundStyle(.secondary.opacity(0.5))
        }
        .padding(.vertical, 12)
        .padding(.horizontal, 14)
        .background {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .fill(isHovering ? Color.white.opacity(0.06) : Color.white.opacity(0.04))
        }
        .overlay {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .stroke(isHovering ? Color.white.opacity(0.12) : Color.white.opacity(0.08), lineWidth: 1)
        }
        .scaleEffect(isHovering ? 1.01 : 1)
        .contentShape(Rectangle())
        .onHover { hovering in
            withAnimation(.easeOut(duration: 0.15)) {
                isHovering = hovering
            }
        }
    }
}
