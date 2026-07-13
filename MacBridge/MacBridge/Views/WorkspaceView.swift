import SwiftUI

/// 工作站首屏（UX 重设计 2026-07-13 P0-2）。
///
/// 取代 `BridgeStatusView` 的四段状态表，改为围绕用户任务的三段纵向工作面：
/// 1. 健康结论行（可以连接 / 还差一步 / 需要处理）+ 设备摘要 + 主动作；
/// 2. 「需要留意」段仅在异常时出现，行内 CTA 复用既有健康探测；
/// 3. AI 工具健康摘要 + 安全连接段（仅呈现 Relay 结果，唯一连接入口）。
///
/// 三种状态：首次使用（无设备）、全就绪、异常。正常状态不显示端口/版本/endpoint，
/// 这些进入「连接状态」或「帮助与诊断」。运行语义不变，仍由 `BridgeStatusViewModel` 提供。
struct WorkspaceView: View {
    @ObservedObject var viewModel: BridgeStatusViewModel
    @ObservedObject var backendViewModel: BackendStatusViewModel
    @ObservedObject var deviceStore: DeviceStore
    let onStartBridge: () -> Void
    let onStopBridge: () -> Void
    let onRestartBridge: () -> Void
    var onNavigateToDevices: (() -> Void)?
    var onOpenConnectionStatus: (() -> Void)?
    var onPairDevice: (() -> Void)?

    @State private var showStopConfirmation = false
    @State private var isRestarting = false

    private var agents: [BackendAgentStatus] {
        if !backendViewModel.agents.isEmpty {
            return backendViewModel.agents
        }
        return viewModel.agents.map {
            BackendAgentStatus(
                id: $0.id,
                displayName: $0.displayName,
                kind: $0.kind,
                status: $0.status,
                reason: $0.reason,
                isRefreshing: false,
                requiresPollingForExternalTurns: $0.requiresPollingForExternalTurns
            )
        }
    }

    /// 首次使用：尚无设备且 Bridge 处于可连接状态。
    private var isFirstUse: Bool {
        deviceStore.hasLoadedDevices && deviceStore.devices.isEmpty && viewModel.status.isConnectable
    }

    var body: some View {
        PageContainer(maxContentWidth: LayoutConstants.workspaceMaxContainerWidth) {
            GeometryReader { geometry in
                let availableContentWidth = geometry.size.width
                let isWide = availableContentWidth >= LayoutConstants.workspaceWideContentThreshold

                if isWide {
                    HStack(alignment: .top, spacing: LayoutConstants.dualColumnGap) {
                        mainContent(availableContentWidth: availableContentWidth)
                            .frame(maxWidth: availableContentWidth >= LayoutConstants.workspacePreferredContentWidth
                                   ? LayoutConstants.dualColumnMainPreferred
                                   : LayoutConstants.dualColumnMainMin)

                        auxiliaryBar(availableContentWidth: availableContentWidth)
                            .frame(width: availableContentWidth >= LayoutConstants.workspacePreferredContentWidth
                                   ? LayoutConstants.dualColumnInspectorPreferred
                                   : LayoutConstants.dualColumnInspectorMin)
                    }
                } else {
                    VStack(alignment: .leading, spacing: 28) {
                        headlineSection
                        if !attentionItems.isEmpty {
                            attentionSection
                        }
                        toolsAndConnectionSection
                    }
                }
            }
        }
        .confirmationDialog(
            L10n.overviewStopConfirmTitle,
            isPresented: $showStopConfirmation,
            titleVisibility: .visible
        ) {
            Button(L10n.stopBridge, role: .destructive, action: onStopBridge)
            Button(L10n.cancel, role: .cancel) {}
        } message: {
            Text(L10n.overviewStopConfirmMessage)
        }
        .onChange(of: viewModel.status) { _, status in
            if status == .ready || status == .readyNoAgents || status == .crashed {
                isRestarting = false
            }
        }
    }

    @ViewBuilder
    private func mainContent(availableContentWidth: CGFloat) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            headlineSection
            if !attentionItems.isEmpty {
                attentionSection
            }
            toolsAndConnectionSection
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private func auxiliaryBar(availableContentWidth: CGFloat) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            // 此 Mac
            HStack(spacing: 6) {
                Image(systemName: "desktopcomputer")
                    .foregroundStyle(.secondary)
                Text(displayNameForAux ?? "此 Mac")
                    .font(.headline)
                    .foregroundStyle(.secondary)
            }

            // 设备
            if deviceStore.hasLoadedDevices {
                HStack(spacing: 6) {
                    Image(systemName: "person.2")
                        .foregroundStyle(.secondary)
                    if deviceStore.devices.isEmpty {
                        Text(L10n.noTrustedDevices)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        Text(String(format: L10n.tr("trusted_devices"), deviceStore.devices.count))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }

            // Relay
            HStack(spacing: 6) {
                Image(systemName: "lock.shield")
                    .foregroundStyle(.secondary)
                Text(relaySummary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 0)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 8)
                .fill(Color(NSColor.controlBackgroundColor).opacity(0.3))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(Color(NSColor.separatorColor).opacity(0.2), lineWidth: 1)
        )
    }

    @AppStorage("bridgeDisplayName") private var bridgeDisplayName = ""

    private var displayNameForAux: String? {
        bridgeDisplayName.isEmpty ? nil : bridgeDisplayName
    }

    // MARK: - 健康结论

    @ViewBuilder
    private var headlineSection: some View {
        if isFirstUse {
            firstUseHeadline
        } else {
            standardHeadline
        }
    }

    private var firstUseHeadline: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(L10n.workspaceFirstDeviceTitle)
                .font(.title.weight(.semibold))
            Text(L10n.workspaceFirstDeviceSubtitle)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button {
                onPairDevice?()
            } label: {
                Label(L10n.addDevice, systemImage: "qrcode")
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .padding(.top, 4)
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }

    private var standardHeadline: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                StatusIndicator(
                    systemImage: conclusionAppearance.icon,
                    color: conclusionAppearance.color,
                    showsProgress: viewModel.status == .starting
                )
                VStack(alignment: .leading, spacing: 4) {
                    Text(conclusionTitle)
                        .font(.title2.weight(.semibold))
                    Text(conclusionSubtitle)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 12)
                headlineAction
            }

            // 设备摘要 + 主动作行
            if deviceStore.hasLoadedDevices {
                HStack(spacing: 8) {
                    if deviceStore.devices.isEmpty {
                        Text(L10n.noTrustedDevices)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        Text(String(format: L10n.tr("trusted_devices"), deviceStore.devices.count))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        if let lastSeen = deviceStore.devices.compactMap(\.lastSeenAt).sorted().last {
                            Text("· " + String(format: L10n.overviewRecentlySeen, RelativeTimeFormatter.string(lastSeen)))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    Spacer()
                    if let onNavigateToDevices {
                        Button(L10n.viewDevices, action: onNavigateToDevices)
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                    }
                    if let onOpenConnectionStatus {
                        Button(L10n.connectionStatus, action: onOpenConnectionStatus)
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var headlineAction: some View {
        switch viewModel.status {
        case .ready:
            Button(isRestarting ? L10n.overviewRestarting : L10n.restart) {
                isRestarting = true
                onRestartBridge()
            }
            .disabled(isRestarting)
            .controlSize(.small)
            Button(L10n.stop) { showStopConfirmation = true }
                .controlSize(.small)
        case .readyNoAgents:
            Button(L10n.workspaceRecheck) {
                Task { await backendViewModel.refreshAgents() }
            }
            .disabled(backendViewModel.isLoading)
            .controlSize(.small)
        case .starting:
            Button(L10n.overviewStarting) {}.disabled(true).controlSize(.small)
        case .idle, .stopped, .sleeping:
            Button(L10n.workspaceStart, action: onStartBridge)
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
        case .crashed:
            Button(L10n.overviewRestart, action: onStartBridge)
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
        }
    }

    // MARK: - 需要留意（仅异常）

    private struct AttentionItem {
        let title: String
        let hint: String?
        let action: () -> Void
        let actionTitle: String
    }

    private var attentionItems: [AttentionItem] {
        var items: [AttentionItem] = []
        // 无可用 AI 工具
        if viewModel.status == .readyNoAgents {
            items.append(AttentionItem(
                title: L10n.workspaceNoToolsTitle,
                hint: L10n.workspaceNoToolsSubtitle,
                action: { Task { await backendViewModel.refreshAgents() } },
                actionTitle: L10n.workspaceRecheck
            ))
        }
        // 失败的 AI 工具
        for agent in agents where !agent.isAvailable {
            items.append(AttentionItem(
                title: "\(agent.displayName) \(agent.displayStatus)",
                hint: WorkspaceView.nextStepGuidance(agent.reason, kind: agent.kind),
                action: { Task { await backendViewModel.testAgent(id: agent.id) } },
                actionTitle: L10n.workspaceRecheck
            ))
        }
        // Relay 不可用
        if viewModel.relayConfigured == nil {
            items.append(AttentionItem(
                title: L10n.workspaceRelayOff,
                hint: nil,
                action: { onOpenConnectionStatus?() },
                actionTitle: L10n.connectionStatus
            ))
        }
        // 运行时错误
        if let error = viewModel.overviewDataError, !error.isEmpty {
            items.append(AttentionItem(
                title: error,
                hint: nil,
                action: { Task { await viewModel.refreshOverviewData(force: true) } },
                actionTitle: L10n.workspaceRecheck
            ))
        }
        return items
    }

    private var attentionSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            SectionHeader(L10n.workspaceNeedsAttention)
            ForEach(Array(attentionItems.enumerated()), id: \.offset) { _, item in
                HStack(alignment: .top, spacing: 10) {
                    StatusIndicator(systemImage: "exclamationmark.triangle.fill", color: .orange)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(item.title)
                            .font(.body.weight(.medium))
                            .fixedSize(horizontal: false, vertical: true)
                        if let hint = item.hint {
                            Text(hint)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    Spacer()
                    Button(item.actionTitle) { item.action() }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                }
                .padding(.vertical, 4)
            }
        }
    }

    // MARK: - AI 工具 + 安全连接

    private var toolsAndConnectionSection: some View {
        VStack(alignment: .leading, spacing: 16) {
            // AI 工具：卡片样式（借鉴参考图的工具卡片网格）
            VStack(alignment: .leading, spacing: 8) {
                SectionHeader(L10n.aiTools)
                if agents.isEmpty {
                    Text(L10n.noAiToolsDetected)
                        .foregroundStyle(.secondary)
                        .font(.subheadline)
                } else {
                    LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 12) {
                        ForEach(agents) { agent in
                            agentCard(for: agent)
                        }
                    }
                }
            }

            // 安全连接：仅呈现当前 Relay 结果，唯一连接入口指向同一个 Sheet
            VStack(alignment: .leading, spacing: 6) {
                SectionHeader(L10n.workspaceSecureConnection)
                HStack(spacing: 10) {
                    StatusIndicator(systemImage: relayIcon, color: relayColor)
                    Text(relaySummary)
                        .font(.subheadline)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer()
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func agentCard(for agent: BackendAgentStatus) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                StatusIndicator(
                    systemImage: agent.isAvailable ? "checkmark.circle.fill" : "exclamationmark.triangle.fill",
                    color: agent.isAvailable ? .green : .orange
                )
                Text(agent.displayName)
                    .font(.body.weight(.medium))
                Spacer()
                Text("macOS")
                    .font(.caption2)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.gray.opacity(0.2))
                    .cornerRadius(4)
            }
            HStack {
                Text(agent.displayStatus)
                    .font(.caption)
                    .foregroundStyle(agent.isAvailable ? Color.secondary : Color.orange)
                Spacer()
                if !agent.isAvailable {
                    Button(L10n.workspaceRecheck) {
                        Task { await backendViewModel.testAgent(id: agent.id) }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .font(.caption)
                }
            }
        }
        .padding(10)
        .background(Color(NSColor.controlBackgroundColor))
        .cornerRadius(8)
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(Color(NSColor.separatorColor).opacity(0.3), lineWidth: 1)
        )
    }

    // MARK: - 派生状态

    /// 健康结论外观（文字+状态点共同表达，不单靠颜色）。
    private var conclusionAppearance: (icon: String, color: Color) {
        switch viewModel.status {
        case .ready: return ("checkmark.circle.fill", .green)
        case .readyNoAgents: return ("exclamationmark.circle.fill", .orange)
        case .starting: return ("hourglass", .orange)
        case .stopped, .idle: return ("stop.circle", .secondary)
        case .crashed: return ("xmark.circle.fill", .red)
        case .sleeping: return ("moon.fill", .blue)
        }
    }

    private var conclusionTitle: String {
        switch viewModel.status {
        case .ready: return L10n.workspaceCanConnect
        case .readyNoAgents: return L10n.workspaceOneStepAway
        case .starting: return L10n.overviewStarting
        case .stopped, .idle: return L10n.workspacePausedTitle
        case .crashed: return L10n.overviewStartFailed
        case .sleeping: return L10n.overviewSleeping
        }
    }

    private var conclusionSubtitle: String {
        switch viewModel.status {
        case .ready: return L10n.workspaceReadySubtitle
        case .readyNoAgents: return L10n.workspaceNoToolsSubtitle
        case .stopped, .idle: return L10n.workspacePausedSubtitle
        case .crashed: return viewModel.lastError.flatMap { $0.isEmpty ? nil : $0 } ?? L10n.workspacePausedSubtitle
        default: return L10n.workspaceReadySubtitle
        }
    }

    private var relaySummary: String {
        switch viewModel.relayConfigured {
        case true:
            return OfficialRelayConfiguration.isUsingCustomEndpoint
                ? L10n.overviewCustomRelayConfigured
                : L10n.workspaceSecureRelayOn
        case false: return L10n.workspaceRelayOff
        case nil: return L10n.overviewRelayUnavailable
        }
    }

    private var relayIcon: String {
        switch viewModel.relayConfigured {
        case true: return "lock.shield.fill"
        case false: return "lock.slash"
        case nil: return "questionmark.circle"
        }
    }

    private var relayColor: Color {
        switch viewModel.relayConfigured {
        case true: return .green
        case false: return .secondary
        case nil: return .orange
        }
    }

    private static func nextStepGuidance(_ reason: String?, kind: String) -> String {
        guard let reason, !reason.isEmpty else { return L10n.checkDocsGuidance }
        if reason.contains("not found in PATH") { return String(format: L10n.notInstalled, kind) }
        if reason.contains("not running") { return L10n.serviceNotRunning }
        if reason.contains("not logged in") { return L10n.loginRequired }
        if reason.contains("timed out") { return L10n.detectionTimedOut }
        if reason.contains("unreachable") { return L10n.cannotReachService }
        return reason
    }
}

private extension BridgeStatus {
    /// 是否处于可连接状态（用于判断首次使用引导是否显示）。
    var isConnectable: Bool {
        switch self {
        case .ready, .readyNoAgents: return true
        default: return false
        }
    }
}
