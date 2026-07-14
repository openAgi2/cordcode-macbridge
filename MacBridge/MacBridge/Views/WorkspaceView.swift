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
        PageContainer(maxContentWidth: LayoutConstants.workspaceFocusedContainerWidth) {
            VStack(alignment: .leading, spacing: 24) {
                headlineSection
                    .padding(.top, 20)
                Divider()
                    .padding(.top, 10)
                devicesSection
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
        VStack(spacing: 12) {
            Text(L10n.workspaceFirstDeviceTitle)
                .font(.title.weight(.semibold))
            Text(L10n.workspaceFirstDeviceSubtitle)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            pairDeviceButton
                .padding(.top, 4)
        }
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }

    private var standardHeadline: some View {
        VStack(spacing: 12) {
            HStack(spacing: 12) {
                if viewModel.status == .starting {
                    ProgressView()
                        .controlSize(.small)
                } else {
                    Image(systemName: conclusionAppearance.icon)
                        .font(.system(size: 38, weight: .light))
                        .foregroundStyle(conclusionAppearance.color)
                        .frame(width: 40, height: 40)
                }
                Text(conclusionTitle)
                    .font(.system(size: 28, weight: .semibold))
            }
            Text(conclusionSubtitle)
                .font(.system(size: 15.5))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .multilineTextAlignment(.center)
            pairDeviceButton
                .padding(.top, 2)
            headlineActions
                .padding(.top, 6)
        }
        .frame(maxWidth: .infinity)
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
            HStack(spacing: 13) {
                RuntimeControlButton(
                    title: isRestarting ? L10n.overviewRestarting : L10n.restart,
                    systemImage: "arrow.clockwise",
                    width: 113,
                    isDisabled: isRestarting
                ) {
                    isRestarting = true
                    onRestartBridge()
                }
                RuntimeControlButton(
                    title: L10n.stop,
                    systemImage: "stop",
                    width: 118
                ) {
                    showStopConfirmation = true
                }
            }
        case .readyNoAgents:
            Button(L10n.workspaceRecheck) {
                Task { await backendViewModel.refreshAgents() }
            }
            .disabled(backendViewModel.isLoading)
            .controlSize(.regular)
        case .starting:
            Button(L10n.overviewStarting) {}.disabled(true).controlSize(.regular)
        case .idle, .stopped, .sleeping:
            Button(L10n.workspaceStart, action: onStartBridge)
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
        case .crashed:
            Button(L10n.overviewRestart, action: onStartBridge)
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
        }
    }

    // MARK: - 设备与配对

    private var devicesSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 10) {
                Text(L10n.authorizedDevices)
                    .font(.title3.weight(.semibold))
                if deviceStore.hasLoadedDevices {
                    Text(String(deviceStore.devices.count))
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(.secondary)
                        .offset(y: 1.5)
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
                    }
                }
            }
        }
    }

    private func deviceRow(_ device: TrustedDevice) -> some View {
        HStack(spacing: 0) {
            if device.platform == "ios" {
                Image(systemName: "iphone.gen3")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 23, height: 44)
                    .foregroundStyle(.primary.opacity(0.85))
                    .padding(.leading, 15)
                    .offset(y: 5)
            } else {
                Image(systemName: "desktopcomputer")
                    .font(.title3)
                    .imageScale(.large)
                    .foregroundStyle(.primary.opacity(0.85))
                    .frame(width: 23, height: 44)
                    .padding(.leading, 15)
                    .offset(y: 5)
            }

            Spacer().frame(width: 30) // x=200对齐

            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 4) {
                    Text(device.displayName ?? device.deviceId)
                        .font(.system(size: 18, weight: .semibold))
                        .padding(.top, 9)
                    if deviceStore.devices.filter({ $0.displayName == device.displayName }).count > 1 {
                        Text("(\(String(device.deviceId.suffix(6))))")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .padding(.top, 9)
                    }
                }
                Text(deviceDetails(for: device))
                    .font(.system(size: 14))
                    .foregroundStyle(.secondary)
            }

            Spacer()

            Button(role: .destructive) {
                deviceToRemove = device
                showRemoveConfirmation = true
            } label: {
                Text("撤销授权")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(Color(red: 0.92, green: 0.35, blue: 0.35))
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background {
                        RoundedRectangle(cornerRadius: 6)
                            .fill(Color(red: 0.92, green: 0.35, blue: 0.35).opacity(0.10))
                    }
                    .overlay {
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(Color(red: 0.92, green: 0.35, blue: 0.35).opacity(0.25), lineWidth: 0.7)
                    }
            }
            .buttonStyle(.plain)
            .offset(y: 7)
            .padding(.trailing, 25)
            .accessibilityLabel(L10n.devicesActions)
        }
        .padding(.vertical, 16)
        .padding(.bottom, 4)
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.white.opacity(0.15))
                .frame(height: 0.5)
        }
    }

    // MARK: - AI 工具 + 安全连接

    private var toolsAndConnectionSection: some View {
        VStack(alignment: .leading, spacing: 31) { // 缩减 3px 向上拉升
            VStack(alignment: .leading, spacing: 12) {
                workspaceSectionHeader(L10n.aiTools)
                if agents.isEmpty {
                    Text(L10n.noAiToolsDetected)
                        .foregroundStyle(.secondary)
                        .font(.subheadline)
                } else {
                    VStack(spacing: 0) {
                        ForEach(agents) { agent in
                            agentRow(for: agent)
                        }
                    }
                    .padding(.bottom, 12) // OpenCode 底部增加 12px 留白
                }
            }

            HStack(alignment: .top, spacing: 28) {
                Image(systemName: relayIcon)
                    .font(.system(size: 34, weight: .light))
                    .foregroundStyle(relayColor)
                    .frame(width: 34, height: 41)
                    .offset(y: -3)
                
                VStack(alignment: .leading, spacing: 4) {
                    Text(L10n.workspaceSecureConnection)
                        .font(.system(size: 18, weight: .semibold))
                        .foregroundStyle(.primary)
                    Text(relaySummary)
                        .font(.system(size: 15))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                        .multilineTextAlignment(.leading)
                }
                
                Spacer()
                
                if viewModel.relayConfigured != true {
                    Button(L10n.connectionStatus) {
                        onOpenConnectionStatus?()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .offset(y: 4)
                }
            }
            .padding(.leading, 6)
            .padding(.trailing, 16)
            .offset(y: -10)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func agentRow(for agent: BackendAgentStatus) -> some View {
        HStack(spacing: 0) {
            AgentBrandMark(kind: agent.kind)
                .padding(.leading, 13)
            Spacer().frame(width: 27) // 13 + 28 + 27 = 68px. 起点完全锁定在 68px (x≈200)！

            Text(agent.displayName)
                .font(.system(size: 17, weight: .semibold))
                .frame(width: 403, alignment: .leading)

            HStack(spacing: 12) {
                Image(systemName: agent.isAvailable ? "checkmark.circle" : "exclamationmark.circle")
                    .font(.system(size: 23, weight: .light))
                    .frame(width: 23, height: 23)
                Text(agent.displayStatus)
                    .font(.system(size: 16, weight: .semibold))
            }
            .foregroundStyle(agent.isAvailable ? Color.green : Color.orange)
            .frame(width: 200, alignment: .leading)

            Spacer()

            if !agent.isAvailable {
                Button(L10n.workspaceRecheck) {
                    Task { await backendViewModel.testAgent(id: agent.id) }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(agent.isRefreshing)
                .frame(width: 56, height: 32)
            }
        }
        .frame(height: 52)
        .padding(.trailing, 16)
        .offset(y: -3) // 整体上移 3px
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Color.white.opacity(0.15))
                .frame(height: 0.5)
                .offset(y: -3) // 分割线也跟随上移 3px
        }
    }

    private func deviceDetails(for device: TrustedDevice) -> String {
        var details: [String] = []
        if let platform = device.platform {
            details.append(platform)
        }
        if let created = device.createdAt {
            details.append(String(format: L10n.paired, RelativeTimeFormatter.string(created)))
        }
        if let lastSeen = device.lastSeenAt {
            details.append(String(format: L10n.lastSeen, RelativeTimeFormatter.string(lastSeen)))
        }
        return details.joined(separator: " · ")
    }

    // MARK: - 派生状态

    private func workspaceSectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.title3.weight(.semibold))
    }

    /// 健康结论外观（文字+状态点共同表达，不单靠颜色）。
    private var conclusionAppearance: (icon: String, color: Color) {
        switch viewModel.status {
        case .ready: return ("checkmark.circle", .green)
        case .readyNoAgents: return ("exclamationmark.circle", .orange)
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
        case true: return "checkmark.shield"
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

}

/// 运行时控制保持为次级操作：有明确轮廓与图标，但不与蓝色配对主操作竞争。
private struct RuntimeControlButton: View {
    let title: String
    let systemImage: String
    let width: CGFloat
    var isDisabled = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: systemImage)
                    .font(.system(size: 18))
                Text(title)
                    .font(.system(size: 15, weight: .semibold))
            }
            .frame(width: width, height: 40)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .foregroundStyle(.primary)
        .background {
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .fill(.black.opacity(0.12))
        }
        .overlay {
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .stroke(.white.opacity(0.30), lineWidth: 1)
        }
        .opacity(isDisabled ? 0.60 : 1)
        .disabled(isDisabled)
    }
}

/// 首页工具行使用稳定、可扫描的品牌化矢量标记；不依赖外部图片资源或网络加载。
private struct AgentBrandMark: View {
    let kind: String

    var body: some View {
        Group {
            switch kind.lowercased() {
            case "claude", "claudecode", "claude_code":
                Image("claude_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 25, height: 25)
            case "codex":
                Image("codex_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 23, height: 23)
            case "grokbuild":
                Image("grok_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 25, height: 25)
            case "opencode":
                Image("opencode_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 23, height: 23)
            default:
                Image(systemName: "command")
                    .font(.title3)
                    .foregroundStyle(.secondary)
            }
        }
        .frame(width: 28, height: 28, alignment: .center)
        .accessibilityHidden(true)
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
            HStack(spacing: 10) {
                Image(systemName: "qrcode.viewfinder")
                    .font(.system(size: 18))
                Text(title)
                    .font(.system(size: 16, weight: .semibold))
            }
            .frame(width: 245, height: 49)
            .contentShape(RoundedRectangle(cornerRadius: 9, style: .continuous))
        }
        .buttonStyle(.plain)
        .foregroundStyle(.white)
        .background {
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .fill(Color.accentColor)
                .overlay {
                    GeometryReader { proxy in
                        LinearGradient(
                            colors: [.clear, .white.opacity(0.18), .clear],
                            startPoint: .leading,
                            endPoint: .trailing
                        )
                        .frame(width: proxy.size.width * 0.38)
                        .offset(x: sweepProgress ? proxy.size.width : -proxy.size.width * 0.38)
                    }
                    .clipShape(RoundedRectangle(cornerRadius: 9, style: .continuous))
                    .allowsHitTesting(false)
                }
        }
        .overlay {
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .stroke(.white.opacity(isBreathing ? 0.34 : 0.18), lineWidth: 1)
        }
        .shadow(
            color: Color.accentColor.opacity(0.12),
            radius: isBreathing ? 8 : 4,
            y: isBreathing ? 3 : 1
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
        withAnimation(.linear(duration: 1.5).repeatForever(autoreverses: false)) {
            sweepProgress = true
        }
    }
}
