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
    var onOpenConnectionStatus: (() -> Void)?
    let onPairDevice: () -> Void

    @State private var showStopConfirmation = false
    @State private var isRestarting = false
    @State private var deviceToRemove: TrustedDevice?
    @State private var showRemoveConfirmation = false

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
        PageContainer(maxContentWidth: LayoutConstants.workspaceHomeContentWidth) {
            VStack(alignment: .leading, spacing: 40) {
                headlineSection
                Divider()
                devicesSection
                if !attentionItems.isEmpty {
                    attentionSection
                }
                toolsAndConnectionSection
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
        .confirmationDialog(
            String(format: L10n.devicesRevokeConfirm, deviceToRemove?.displayName ?? L10n.devicesUnknownDevice),
            isPresented: $showRemoveConfirmation,
            titleVisibility: .visible
        ) {
            Button(L10n.devicesRevokeAuthorization, role: .destructive) {
                if let device = deviceToRemove {
                    Task { await deviceStore.revokeDevice(device) }
                }
            }
            Button(L10n.cancel, role: .cancel) {}
        } message: {
            Text(L10n.devicesRevokeMessage)
        }
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
            pairDeviceButton
                .padding(.top, 4)
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }

    private var standardHeadline: some View {
        HStack(alignment: .top, spacing: 28) {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline, spacing: 10) {
                    StatusIndicator(
                        systemImage: conclusionAppearance.icon,
                        color: conclusionAppearance.color,
                        showsProgress: viewModel.status == .starting
                    )
                    Text(conclusionTitle)
                        .font(.title2.weight(.semibold))
                }
                Text(conclusionSubtitle)
                    .font(.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer(minLength: 20)

            HStack(alignment: .center, spacing: 8) {
                pairDeviceButton
                headlineActions
            }
        }
    }

    private var pairDeviceButton: some View {
        PairDeviceButton(title: L10n.pairNewDevice) {
            onPairDevice()
        }
    }

    @ViewBuilder
    private var headlineActions: some View {
        switch viewModel.status {
        case .ready:
            Button(isRestarting ? L10n.overviewRestarting : L10n.restart) {
                isRestarting = true
                onRestartBridge()
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .disabled(isRestarting)
            Button(L10n.stop) { showStopConfirmation = true }
                .buttonStyle(.bordered)
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

    // MARK: - 设备与配对

    private var devicesSection: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(alignment: .firstTextBaseline) {
                workspaceSectionHeader(L10n.authorizedDevices)
                if deviceStore.hasLoadedDevices {
                    Text("\(deviceStore.devices.count)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }

            if let devicesError = deviceStore.devicesError {
                HStack(spacing: 8) {
                    Image(systemName: "xmark.circle")
                    Text(devicesError)
                    Spacer()
                    Button(L10n.retry) { Task { await deviceStore.loadDevices() } }
                        .buttonStyle(.borderless)
                }
                .font(.subheadline)
                .foregroundStyle(.red)
            } else if !deviceStore.hasLoadedDevices {
                ProgressView(L10n.loadingDevices)
                    .controlSize(.small)
            } else if deviceStore.devices.isEmpty {
                Label(L10n.noAuthorizedDevices, systemImage: "iphone.slash")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            } else {
                VStack(spacing: 0) {
                    ForEach(deviceStore.devices) { device in
                        deviceRow(device)
                        if device.id != deviceStore.devices.last?.id {
                            Divider()
                        }
                    }
                }
            }
        }
    }

    private func deviceRow(_ device: TrustedDevice) -> some View {
        HStack(spacing: 14) {
            Image(systemName: device.platform == "ios" ? "iphone" : "desktopcomputer")
                .foregroundStyle(.secondary)
                .frame(width: 20)

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 4) {
                    Text(device.displayName ?? device.deviceId)
                        .font(.body.weight(.medium))
                    if deviceStore.devices.filter({ $0.displayName == device.displayName }).count > 1 {
                        Text("(\(String(device.deviceId.suffix(6))))")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
                HStack(spacing: 10) {
                    if let platform = device.platform {
                        Text(platform)
                    }
                    if let created = device.createdAt {
                        Text(String(format: L10n.paired, RelativeTimeFormatter.string(created)))
                    }
                    if let lastSeen = device.lastSeenAt {
                        Text(String(format: L10n.lastSeen, RelativeTimeFormatter.string(lastSeen)))
                    }
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)
            }

            Spacer()

            Button(L10n.devicesRevokeAuthorization, role: .destructive) {
                deviceToRemove = device
                showRemoveConfirmation = true
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.red)
            .accessibilityLabel(L10n.devicesActions)
        }
        .padding(.vertical, 14)
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
        VStack(alignment: .leading, spacing: 12) {
            workspaceSectionHeader(L10n.workspaceNeedsAttention)
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
        VStack(alignment: .leading, spacing: 38) {
            VStack(alignment: .leading, spacing: 16) {
                workspaceSectionHeader(L10n.aiTools)
                if agents.isEmpty {
                    Text(L10n.noAiToolsDetected)
                        .foregroundStyle(.secondary)
                        .font(.subheadline)
                } else {
                    LazyVGrid(
                        columns: [GridItem(.flexible(), spacing: 28), GridItem(.flexible())],
                        spacing: 22
                    ) {
                        ForEach(agents) { agent in
                            agentCard(for: agent)
                        }
                    }
                }
            }

            // 安全连接：仅呈现当前 Relay 结果，唯一连接入口指向同一个 Sheet
            VStack(alignment: .leading, spacing: 10) {
                workspaceSectionHeader(L10n.workspaceSecureConnection)
                HStack(spacing: 12) {
                    StatusIndicator(systemImage: relayIcon, color: relayColor)
                    Text(relaySummary)
                        .font(.body)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer()
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func agentCard(for agent: BackendAgentStatus) -> some View {
        HStack(spacing: 11) {
            StatusIndicator(
                systemImage: agent.isAvailable ? "checkmark.circle.fill" : "exclamationmark.triangle.fill",
                color: agent.isAvailable ? .green : .orange
            )
            Text(agent.displayName)
                .font(.body.weight(.medium))
            Spacer()
            Text(agent.displayStatus)
                .font(.subheadline)
                .foregroundStyle(agent.isAvailable ? Color.secondary : Color.orange)
            if !agent.isAvailable {
                Button(L10n.workspaceRecheck) {
                    Task { await backendViewModel.testAgent(id: agent.id) }
                }
                .buttonStyle(.borderless)
                .controlSize(.small)
            }
        }
        .padding(.vertical, 14)
        .overlay(alignment: .bottom) { Divider() }
    }

    // MARK: - 派生状态

    private func workspaceSectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.title3.weight(.semibold))
    }

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

/// 工作站唯一主操作的轻量动效：蓝色底色保持原有状态语义，扫光与呼吸只用于提示可开始配对。
private struct PairDeviceButton: View {
    let title: String
    let action: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var isBreathing = false
    @State private var sweepProgress = false
    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            Label(title, systemImage: "qrcode.viewfinder")
                .font(.body.weight(.semibold))
                .padding(.horizontal, 20)
                .padding(.vertical, 10)
        }
        .buttonStyle(.plain)
        .foregroundStyle(.white)
        .background {
            Capsule()
                .fill(Color.accentColor)
                .overlay {
                    GeometryReader { proxy in
                        LinearGradient(
                            colors: [.clear, .white.opacity(0.28), .clear],
                            startPoint: .leading,
                            endPoint: .trailing
                        )
                        .frame(width: proxy.size.width * 0.38)
                        .offset(x: sweepProgress ? proxy.size.width : -proxy.size.width * 0.38)
                    }
                    .clipShape(Capsule())
                    .allowsHitTesting(false)
                }
        }
        .overlay {
            Capsule()
                .stroke(.white.opacity(isBreathing ? 0.34 : 0.18), lineWidth: 1)
        }
        .shadow(
            color: Color.accentColor.opacity(isBreathing ? 0.40 : 0.22),
            radius: isBreathing ? 15 : 8,
            y: isBreathing ? 5 : 3
        )
        .scaleEffect(isHovering ? 1.02 : (isBreathing ? 1.008 : 1))
        .onHover { hovering in
            withAnimation(.easeOut(duration: 0.16)) {
                isHovering = hovering
            }
        }
        .onAppear(perform: startMotion)
        .onChange(of: reduceMotion) { _, _ in startMotion() }
        .accessibilityHint(L10n.pairNewDevice)
    }

    private func startMotion() {
        guard !reduceMotion else {
            isBreathing = false
            sweepProgress = false
            return
        }
        withAnimation(.easeInOut(duration: 1.8).repeatForever(autoreverses: true)) {
            isBreathing = true
        }
        withAnimation(.linear(duration: 3.2).repeatForever(autoreverses: false)) {
            sweepProgress = true
        }
    }
}
