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

    @State private var showRawLogs = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        PageContainer(scrolls: false) {
            VStack(alignment: .leading, spacing: 16) {
                PageHeader(L10n.helpDiagnostics, subtitle: L10n.diagnosticsSubtitle) {
                    Button {
                        Task { await diagnosticsViewModel.loadLogs() }
                    } label: {
                        HStack(spacing: 4) {
                            if diagnosticsViewModel.isLoadingLogs {
                                ProgressView().controlSize(.small)
                            }
                            Image(systemName: "arrow.clockwise")
                            Text(L10n.refreshAll)
                        }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
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
                    .controlSize(.small)
                }

                healthSummarySection

                Divider()

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
                    .controlSize(.small)
                }

                Spacer(minLength: 0)

                HStack {
                    Spacer()
                    Button(L10n.done) { dismiss() }
                        .buttonStyle(.borderedProminent)
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
        VStack(alignment: .leading, spacing: 12) {
            SectionHeader(L10n.diagnosticsHealthSummary)

            healthRow(
                title: L10n.diagnosticsHealthBridge,
                status: bridgeSummaryText,
                color: bridgeColor
            )
            healthRow(
                title: L10n.diagnosticsHealthConnection,
                status: connectionSummaryText,
                color: connectionColor
            )
            healthRow(
                title: L10n.diagnosticsHealthAiTools,
                status: aiToolsSummaryText,
                color: aiToolsColor
            )
        }
    }

    private func healthRow(title: String, status: String, color: Color) -> some View {
        HStack(spacing: 10) {
            StatusIndicator(
                systemImage: color == .green ? "checkmark.circle.fill" : "exclamationmark.triangle.fill",
                color: color
            )
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.subheadline.weight(.medium))
                Text(status).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
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
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                SectionHeader(L10n.rawLogs)
                Spacer()
                Button(L10n.copyRawLogs) {
                    diagnosticsViewModel.copyRawLogs()
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(diagnosticsViewModel.logs.isEmpty)
            }
            HStack {
                Text("原始日志")
                    .font(.headline)
                Text(diagnosticsViewModel.maxDisplayLines == 30 ? "（最近 30 行）" : "（最近 200 行）")
                    .font(.caption)
                    .foregroundColor(.secondary)
                Spacer()
            }

            if diagnosticsViewModel.isLoadingLogs && diagnosticsViewModel.logs.isEmpty {
                ProgressView(L10n.diagnosticsReading)
            } else if let error = diagnosticsViewModel.logsError {
                InlineFeedback(style: .error, message: error)
            } else if diagnosticsViewModel.logs.isEmpty {
                Text(L10n.noLogsAvailable).foregroundColor(.secondary)
            } else {
                let displayed = Array(diagnosticsViewModel.logs.suffix(diagnosticsViewModel.maxDisplayLines))
                ScrollView(.vertical) {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        ForEach(Array(displayed.enumerated()), id: \.offset) { _, line in
                            Text(DiagnosticsViewModel.displayLogLine(line))
                                .font(.system(size: 11, design: .monospaced))
                                .lineLimit(1)
                                .truncationMode(.middle)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                    .padding(10)
                }
                .frame(maxHeight: .infinity)
                .glassPanel()

                Button(diagnosticsViewModel.maxDisplayLines == 30 ? "查看完整 200 行" : "显示最近 30 行") {
                    diagnosticsViewModel.toggleFullLogs()
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .padding(.top, 4)
            }
        }
    }
}
