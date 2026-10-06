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
    @ObservedObject var pairingViewModel: PairingViewModel
    @ObservedObject var runtimeManager: RuntimeManager
    @ObservedObject var topologyStore: TopologyMonitorStatusStore
    let onStartBridge: () -> Void
    let onStopBridge: () -> Void
    let onRestartBridge: () -> Void
    var onOpenConnectionStatus: (() -> Void)?
    let onPairDevice: () -> Void

    @State private var showStopConfirmation = false
    @State private var isRestarting = false
    @State private var deviceToRemove: TrustedDevice?
    @State private var showRemoveConfirmation = false
    @State private var copiedWebLinkId: String? = nil
    @State private var generatingWebLinkId: String? = nil
    @State private var isRestartingCodexDaemon = false
    @State private var codexRestartMessage: String? = nil
    @State private var showCodexDesktopPairing = false
    @StateObject private var grokLeaderManager = GrokLeaderModeManager()
    @State private var grokLeaderFailureAlert: GrokLeaderToggleFailure?

    struct GrokLeaderToggleFailure: Identifiable {
        let id = UUID()
        let reason: String
    }

    /// 活跃 turn / pending 交互（每 3s 轮询更新）；>0 时重启共享 daemon 会打断任务。
    private var hasActiveCodexTurns: Bool {
        runtimeManager.codexWebActiveTurns > 0 || runtimeManager.codexWebPendingInteractions > 0
    }

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
                topologySection
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
        .sheet(isPresented: $showCodexDesktopPairing) {
            CodexDesktopPairingSheet(viewModel: backendViewModel)
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
        .alert(
            L10n.devicesPushCleanupWarningTitle,
            isPresented: Binding(
                get: { deviceStore.isRevokeCleanupWarningPresented },
                set: { if !$0 { deviceStore.dismissRevokeCleanupWarning() } }
            )
        ) {
            Button(L10n.ok, role: .cancel) { deviceStore.dismissRevokeCleanupWarning() }
        } message: {
            Text(deviceStore.revokeCleanupWarning ?? "")
        }
        .alert(
            L10n.codexRestartSharedService,
            isPresented: Binding(
                get: { codexRestartMessage != nil },
                set: { if !$0 { codexRestartMessage = nil } }
            )
        ) {
            Button(L10n.ok, role: .cancel) { codexRestartMessage = nil }
        } message: {
            Text(codexRestartMessage ?? "")
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

    // MARK: - Desktop 同步状态（UI-3）

    /// 只按 store 的 display 分支渲染；healthy/disabled/idle 返回空视图。
    @ViewBuilder
    private var topologySection: some View {
        switch topologyStore.phase.display {
        case .hidden:
            EmptyView()
        case .infoNotApplicable:
            topologyCard(
                icon: "desktopcomputer",
                tint: Color.secondary,
                title: L10n.topologyNotApplicable,
                body: nil
            )
        case .diagnosticFailure:
            topologyCard(
                icon: "info.circle",
                tint: Color.secondary,
                title: L10n.topologyDiagnosticFailed,
                body: topologyStore.lastDiagnosticDetail
            )
        case .warning(let kind):
            topologyCard(
                icon: "exclamationmark.triangle.fill",
                tint: .orange,
                title: warningTitle(kind),
                body: nil
            )
        }
    }

    private func warningTitle(_ kind: TopologyWarningKind) -> String {
        switch kind {
        case .desktopDetached: return L10n.topologyWarningDevDesktopDetached
        case .partialSync: return L10n.topologyWarningPartialSync
        case .observerUnattached: return L10n.topologyWarningObserverUnattached
        case .general: return L10n.topologyWarningGeneral
        }
    }

    private func topologyCard(icon: String, tint: Color, title: String, body: String?) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 22, weight: .light))
                .foregroundStyle(tint)
                .frame(width: 24, height: 24)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(L10n.topologyTitle)
                        .font(.system(size: 17, weight: .semibold))
                    Text(title)
                        .font(.system(size: 14))
                        .foregroundStyle(tint)
                }
                if let body {
                    Text(body)
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let updatedAt = topologyStore.lastUpdatedAt {
                    Text("\(L10n.topologyUpdatedPrefix) \(Self.timeFormatter.string(from: updatedAt))")
                        .font(.system(size: 12))
                        .foregroundStyle(.tertiary)
                }
            }
            Spacer()
        }
        .padding(.vertical, 10)
    }

    private static let timeFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateStyle = .none
        formatter.timeStyle = .short
        return formatter
    }()

    // MARK: - 设备与配对

    private var devicesSection: some View {        VStack(alignment: .leading, spacing: 12) {
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

            // Single official web pairing link (`/web/`); candidate `/web-v2/` is retired.
            let isWebLoading = generatingWebLinkId == device.deviceId
            let isWebCopied = copiedWebLinkId == device.deviceId
            Button {
                handleCopyLink(forDevice: device)
            } label: {
                HStack(spacing: 4) {
                    if isWebLoading {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Image(systemName: isWebCopied ? "checkmark" : "doc.on.clipboard")
                    }
                    Text(isWebCopied ? L10n.pairingLinkCopied : L10n.copyPairingLink)
                }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .disabled(generatingWebLinkId != nil)
            .offset(y: 7)
            .padding(.trailing, 12)

            RevokeButton {
                deviceToRemove = device
                showRemoveConfirmation = true
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

    private func handleCopyLink(forDevice device: TrustedDevice) {
        guard generatingWebLinkId == nil else { return }
        generatingWebLinkId = device.deviceId

        Task {
            if let urlString = await pairingViewModel.getOrFetchWebPairingURL() {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(urlString, forType: .string)
                copiedWebLinkId = device.deviceId
                try? await Task.sleep(for: .seconds(2))
                if copiedWebLinkId == device.deviceId {
                    copiedWebLinkId = nil
                }
            }
            if generatingWebLinkId == device.deviceId {
                generatingWebLinkId = nil
            }
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
                    .onAppear { grokLeaderManager.refresh() }
                    .onChange(of: backendViewModel.agents) { _, _ in
                        grokLeaderManager.refresh()
                    }
                    .alert(
                        L10n.grokLeaderToggleFailedTitle,
                        isPresented: Binding(
                            get: { grokLeaderFailureAlert != nil },
                            set: { if !$0 { grokLeaderFailureAlert = nil } }
                        )
                    ) {
                        Button("OK", role: .cancel) {}
                    } message: {
                        Text(String(
                            format: L10n.grokLeaderToggleFailedBody,
                            grokLeaderFailureAlert?.reason ?? "",
                            grokLeaderManager.backupDirectoryPath
                        ))
                    }
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

    private func isCodexDesktop(_ agent: BackendAgentStatus) -> Bool {
        let kind = agent.kind.lowercased()
        return agent.id == "codex-remote" || kind == "codex-remote"
    }

    private func isDeepSeekWeb(_ agent: BackendAgentStatus) -> Bool {
        agent.kind.lowercased() == "deepseek-web" || agent.id == "dsh-web"
    }

    /// 行状态文本：dsh / OpenCode 行走各自的本地覆盖，其余走全局映射。
    private func rowStatusText(for agent: BackendAgentStatus) -> String {
        if isDeepSeekWeb(agent) {
            return Self.deepSeekRowStatusText(agent.status)
        }
        if isOpenCodeWeb(agent) {
            let seat = runtimeManager.openCodeSeatAction
            return Self.openCodeRowStatusText(source: seat.source, wireStatus: agent.status, cliFound: seat.cliFound)
        }
        return agent.displayStatus
    }

    /// DeepSeek 行的座位动作决策（§3 状态表 → 按钮），纯函数供单测锁定。
    enum DeepSeekSeatAction: Equatable {
        case none
        case install
        case needNode
        case start
        case installing
        case starting
    }

    static func deepSeekSeatAction(
        status: String,
        installing: Bool,
        starting: Bool,
        npmFound: Bool
    ) -> DeepSeekSeatAction {
        if installing { return .installing }
        if starting { return .starting }
        switch status {
        case "not_detected": return npmFound ? .install : .needNode
        case "service_not_running": return .start
        default: return .none
        }
    }

    /// DeepSeek 行的状态文案覆盖（§3）：not_detected → 未安装、
    /// service_not_running → 未启动；其余沿用全局映射（就绪 / 端口占用）。
    /// 只覆盖该行——Claude 等其他 backend 的 not_detected 仍是「未找到」。
    static func deepSeekRowStatusText(_ status: String) -> String {
        switch status {
        case "not_detected": return L10n.dshWebStatusNotInstalled
        case "service_not_running": return L10n.dshWebStatusNotRunning
        default: return BackendStatusText.display(status)
        }
    }

    private func isOpenCodeWeb(_ agent: BackendAgentStatus) -> Bool {
        agent.kind.lowercased() == "opencode-web" || agent.id == "opencode-web"
    }

    /// OpenCode 行的座位动作决策（2026-10-06 方案 §3 按钮矩阵），纯函数供
    /// 单测锁定：进行中 → 禁用态；source != managed_local 或 wire available →
    /// 无按钮（external_http 用户自管服务）；cliFound==false → npmFound ?
    /// 安装 : 需要Node.js；其余 → 启动。
    enum OpenCodeSeatActionKind: Equatable {
        case none
        case install
        case needNode
        case start
        case installing
        case starting
    }

    static func openCodeSeatAction(
        source: OpenCodeServerSource,
        wireStatus: String,
        cliFound: Bool,
        npmFound: Bool,
        installing: Bool,
        starting: Bool
    ) -> OpenCodeSeatActionKind {
        if installing { return .installing }
        if starting { return .starting }
        if source != .managedLocal || wireStatus == "available" { return .none }
        if !cliFound { return npmFound ? .install : .needNode }
        return .start
    }

    /// OpenCode 行的状态文本（方案 §3 行文本决策）：managed_local 下本地覆盖
    /// 「未安装 / 未启动」（含无持久 endpoint 的 not_configured——managed_local
    /// 下它表示「从未成功启动」，前进动作是启动）；其余沿用全局映射
    /// （external_http 未配置 / disabled → 未配置；external_http 服务没起 →
    /// 未启动）。与 8 行状态表逐行一致（r1 F-1）。
    static func openCodeRowStatusText(
        source: OpenCodeServerSource,
        wireStatus: String,
        cliFound: Bool
    ) -> String {
        if wireStatus == "available" {
            return BackendStatusText.display(wireStatus)
        }
        if source == .managedLocal {
            return cliFound ? L10n.openCodeWebStatusNotRunning : L10n.openCodeWebStatusNotInstalled
        }
        return BackendStatusText.display(wireStatus)
    }

    private func agentRow(for agent: BackendAgentStatus) -> some View {
        HStack(spacing: 0) {
            AgentBrandMark(kind: agent.kind)
                .padding(.leading, 13)
            Spacer().frame(width: 27) // 13 + 28 + 27 = 68px. 起点完全锁定在 68px (x≈200)！

            VStack(alignment: .leading, spacing: 1) {
                Text(agent.displayName)
                    .font(.system(size: 17, weight: .semibold))
                    .frame(width: 403, alignment: .leading)
                if agent.kind.lowercased() == "codex-web", runtimeManager.codexDaemonConfigChanged {
                    Text(L10n.codexConfigChangedHint)
                        .font(.system(size: 11))
                        .foregroundStyle(.orange)
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .help(L10n.codexConfigChangedHintFull)
                }
                if agent.kind.lowercased() == "grokbuild" {
                    grokLeaderHint(for: agent)
                }
                if isDeepSeekWeb(agent) {
                    deepSeekSeatHint(for: agent)
                }
                if isOpenCodeWeb(agent) {
                    openCodeSeatHint(for: agent)
                }
            }

            HStack(spacing: 12) {
                Image(systemName: agent.isAvailable ? "checkmark.circle" : "exclamationmark.circle")
                    .font(.system(size: 23, weight: .light))
                    .frame(width: 23, height: 23)
                Text(rowStatusText(for: agent))
                    .font(.system(size: 16, weight: .semibold))
            }
            .foregroundStyle(agent.isAvailable ? Color.green : Color.orange)
            .frame(width: 200, alignment: .leading)

            Spacer()

            if isCodexDesktop(agent), !agent.isAvailable {
                Button(L10n.pairCodexDesktop) {
                    showCodexDesktopPairing = true
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .frame(width: 72, height: 32)
            }

            if agent.kind.lowercased() == "codex-web" {
                Button {
                    restartSharedCodexDaemon()
                } label: {
                    if isRestartingCodexDaemon {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Image(systemName: "arrow.clockwise")
                        Text(L10n.codexRestartSharedService)
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(isRestartingCodexDaemon || hasActiveCodexTurns)
                .help(hasActiveCodexTurns ? L10n.codexRestartRejectedActiveTurns : L10n.codexRestartSharedService)
                .frame(width: 172, height: 32)
            }

            if agent.kind.lowercased() == "grokbuild" {
                grokLeaderToggle(for: agent)
                    .frame(width: 172, height: 32)
            }

            if isDeepSeekWeb(agent) {
                deepSeekSeatControls(for: agent)
            }

            if isOpenCodeWeb(agent) {
                openCodeSeatControls(for: agent)
            }

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

    // MARK: - DeepSeek 座位动作（2026-09-22 方案 §2.1/§2.2/§2.4）

    /// 行按钮：未安装 → 安装（无 node/npm 时改为「需要 Node.js」，点开官网）；
    /// 未启动 → 启动；进行中 → 禁用态。样式对齐「配对」「重新检查」。
    @ViewBuilder
    private func deepSeekSeatControls(for agent: BackendAgentStatus) -> some View {
        let state = backendViewModel.dshWebSeatAction
        switch Self.deepSeekSeatAction(
            status: agent.status,
            installing: state.installing,
            starting: state.starting,
            npmFound: state.npmFound
        ) {
        case .installing:
            Button(L10n.dshWebInstalling) {}
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(true)
                .frame(width: 72, height: 32)
        case .starting:
            Button(L10n.dshWebStarting) {}
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(true)
                .frame(width: 72, height: 32)
        case .install:
            Button(L10n.dshWebInstall) {
                Task { await backendViewModel.installDSHWeb() }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .frame(width: 72, height: 32)
        case .needNode:
            Button(L10n.dshWebNeedNode) {
                if let url = URL(string: "https://nodejs.org") {
                    NSWorkspace.shared.open(url)
                }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .frame(width: 110, height: 32)
        case .start:
            Button(L10n.dshWebStart) {
                Task { await backendViewModel.startDSHWebSeat() }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .frame(width: 72, height: 32)
        case .none:
            EmptyView()
        }
    }

    /// 名字下方字幕（§2.4：一行，hover 全文）。优先级：进行中提示 → npm 真实
    /// 错误 → prefix 回退说明 → 启动失败原文 → port_conflict 的 lsof 命令行 →
    /// service_not_running 的 reason。
    @ViewBuilder
    private func deepSeekSeatHint(for agent: BackendAgentStatus) -> some View {
        let state = backendViewModel.dshWebSeatAction
        if state.installing {
            hintText(L10n.dshWebInstallingHint, full: L10n.dshWebInstallingHint, color: .secondary)
        } else if state.starting {
            hintText(L10n.dshWebStartingHint, full: L10n.dshWebStartingHint, color: .secondary)
        } else if let err = state.lastInstallError, !err.isEmpty {
            hintText(err, full: err, color: .orange)
        } else if let note = state.lastInstallNote, !note.isEmpty {
            hintText(note, full: note, color: .secondary)
        } else if let err = state.lastStartError, !err.isEmpty {
            hintText(err, full: err, color: .orange)
        } else if agent.status == "port_conflict", let reason = agent.reason, !reason.isEmpty {
            hintText(reason, full: reason, color: .orange)
        } else if agent.status == "service_not_running", let reason = agent.reason, !reason.isEmpty {
            hintText(reason, full: reason, color: .secondary)
        } else {
            EmptyView()
        }
    }

    // MARK: - OpenCode 座位动作（2026-10-06 方案 §2.1/§2.2/§2.4）

    /// 行按钮：未安装 → 安装（无 node/npm 时改为「需要 Node.js」，点开官网）；
    /// 未启动 → 启动；进行中 → 禁用态。样式对齐 dsh 行。动作收口后由按钮
    /// Task 调 testAgent 重取描述符（§5.2 收口刷新——OpenCode 无 action-state
    /// 端点可轮询，3 秒可用性轮询只覆盖 codex-remote）。
    @ViewBuilder
    private func openCodeSeatControls(for agent: BackendAgentStatus) -> some View {
        let state = runtimeManager.openCodeSeatAction
        switch Self.openCodeSeatAction(
            source: state.source,
            wireStatus: agent.status,
            cliFound: state.cliFound,
            npmFound: state.npmFound,
            installing: state.installing,
            starting: state.starting
        ) {
        case .installing:
            Button(L10n.openCodeWebInstalling) {}
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(true)
                .frame(width: 72, height: 32)
        case .starting:
            Button(L10n.openCodeWebStarting) {}
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(true)
                .frame(width: 72, height: 32)
        case .install:
            Button(L10n.openCodeWebInstall) {
                Task {
                    await runtimeManager.installOpenCode()
                    await backendViewModel.testAgent(id: "opencode-web")
                }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .frame(width: 72, height: 32)
        case .needNode:
            Button(L10n.openCodeWebNeedNode) {
                if let url = URL(string: "https://nodejs.org") {
                    NSWorkspace.shared.open(url)
                }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .frame(width: 110, height: 32)
        case .start:
            Button(L10n.openCodeWebStart) {
                Task {
                    await runtimeManager.startOpenCodeManagedServer()
                    await backendViewModel.testAgent(id: "opencode-web")
                }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .frame(width: 72, height: 32)
        case .none:
            EmptyView()
        }
    }

    /// 名字下方字幕（§2.4：一行，hover 全文）。优先级：进行中提示 → npm 真实
    /// 错误 → prefix 回退说明 → 启动失败原文 → service_not_running 的 reason
    /// 透传（探针/认证/隔离原文）。
    @ViewBuilder
    private func openCodeSeatHint(for agent: BackendAgentStatus) -> some View {
        let state = runtimeManager.openCodeSeatAction
        if state.installing {
            hintText(L10n.openCodeWebInstallingHint, full: L10n.openCodeWebInstallingHint, color: .secondary)
        } else if state.starting {
            hintText(L10n.openCodeWebStartingHint, full: L10n.openCodeWebStartingHint, color: .secondary)
        } else if let err = state.lastInstallError, !err.isEmpty {
            hintText(err, full: err, color: .orange)
        } else if let note = state.lastInstallNote, !note.isEmpty {
            hintText(note, full: note, color: .secondary)
        } else if let err = state.lastStartError, !err.isEmpty {
            hintText(err, full: err, color: .orange)
        } else if agent.status == "service_not_running", let reason = agent.reason, !reason.isEmpty {
            hintText(reason, full: reason, color: .secondary)
        } else {
            EmptyView()
        }
    }

    private func restartSharedCodexDaemon() {
        guard !isRestartingCodexDaemon else { return }
        isRestartingCodexDaemon = true
        Task { @MainActor in
            let outcome = await runtimeManager.restartSharedCodexDaemon()
            isRestartingCodexDaemon = false
            switch outcome {
            case .restarted:
                codexRestartMessage = L10n.codexRestartSuccess
            case .rejectedActiveTurns:
                codexRestartMessage = L10n.codexRestartRejectedActiveTurns
            case let .failed(detail):
                codexRestartMessage = String(format: L10n.codexRestartFailed, detail)
            }
        }
    }

    // MARK: - Grok Leader 模式（§3.4 状态机：核心三态 / 观察态 / 失败态）

    private var grokLeaderRowState: GrokLeaderRowState {
        GrokLeaderModeManager.rowState(
            status: grokLeaderManager.status,
            customSocketPath: grokLeaderManager.hasCustomSocketPath
        )
    }

    /// 行内 Toggle（槽位与 codex-web 行内按钮对齐；F1/F2/F3 禁用）。
    private func grokLeaderToggle(for agent: BackendAgentStatus) -> some View {
        Toggle(L10n.grokLeaderMode, isOn: Binding(
            get: { grokLeaderManager.status.isON },
            set: { setGrokLeaderMode($0) }
        ))
        .toggleStyle(.switch)
        .controlSize(.small)
        .disabled(grokLeaderRowState.disablesToggle || !agent.isAvailable)
        .help(grokLeaderToggleHelp(for: agent))
    }

    private func grokLeaderToggleHelp(for agent: BackendAgentStatus) -> String {
        if !agent.isAvailable {
            return L10n.grokLeaderNotInstalled
        }
        switch grokLeaderRowState {
        case .coreOnPendingRestart:
            // §6-6/§6-4 如实披露（D-3 owner 已批）：重启指引 + 四因 + interaction 等待
            // + D-G2 副作用 + chat 互斥，只在 ON 态展示完整注意事项。
            return L10n.grokLeaderPendingRestartFull + "\n\n" + L10n.grokLeaderModeNotes
        case .coreOnSocketDetected:
            return L10n.grokLeaderModeNotes
        case .failedRead(let reason):
            return String(format: L10n.grokLeaderReadFailed, reason)
        case .failedUnsafeForm:
            return L10n.grokLeaderUnsafeForm
        default:
            return L10n.grokLeaderMode
        }
    }

    /// 名字下方副文案（§3.4 表；#2 橙色，其余次级色；hover 提供全文）。
    @ViewBuilder
    private func grokLeaderHint(for agent: BackendAgentStatus) -> some View {
        // F3：grok 不可用时开关已由槽位禁用 + .help 提示；副文案只承载配置/socket 维度。
        switch grokLeaderRowState {
        case .coreOff:
            EmptyView()
        case .coreOnPendingRestart:
            hintText(L10n.grokLeaderPendingRestart, full: L10n.grokLeaderPendingRestartFull, color: .orange)
        case .coreOnSocketDetected:
            hintText(L10n.grokLeaderSocketDetected, full: L10n.grokLeaderSocketDetected, color: .secondary)
        case .observeSocketTrace:
            hintText(L10n.grokLeaderSocketTrace, full: L10n.grokLeaderSocketTrace, color: .secondary)
        case .observeExplicitOff:
            hintText(L10n.grokLeaderExplicitOff, full: L10n.grokLeaderExplicitOff, color: .secondary)
        case .observeCustomSocket(let path):
            hintText(String(format: L10n.grokLeaderCustomSocket, path), full: path, color: .secondary)
        case .failedRead(let reason):
            hintText(String(format: L10n.grokLeaderReadFailed, reason), full: reason, color: .orange)
        case .failedUnsafeForm:
            hintText(L10n.grokLeaderUnsafeForm, full: L10n.grokLeaderUnsafeForm, color: .orange)
        }
    }

    private func hintText(_ text: String, full: String, color: Color) -> some View {
        Text(text)
            .font(.system(size: 11))
            .foregroundStyle(color)
            .lineLimit(1)
            .truncationMode(.tail)
            .help(full)
    }

    /// 开关动作：失败回弹 alert（含原因与备份目录）；成功后 refresh 已由 Manager 完成。
    private func setGrokLeaderMode(_ enabled: Bool) {
        do {
            _ = try grokLeaderManager.setLeaderMode(enabled)
        } catch let error as GrokLeaderModeManager.SetModeError {
            switch error {
            case .f1(let reason):
                grokLeaderFailureAlert = GrokLeaderToggleFailure(reason: reason)
            case .f2:
                grokLeaderFailureAlert = GrokLeaderToggleFailure(reason: L10n.grokLeaderUnsafeForm)
            case .concurrentModification(let reason):
                grokLeaderFailureAlert = GrokLeaderToggleFailure(reason: reason)
            case .ioFailure(let reason):
                grokLeaderFailureAlert = GrokLeaderToggleFailure(reason: reason)
            case .postVerifyFailed(_, let reason):
                grokLeaderFailureAlert = GrokLeaderToggleFailure(reason: reason)
            }
        } catch {
            grokLeaderFailureAlert = GrokLeaderToggleFailure(reason: error.localizedDescription)
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
    @State private var isHovering = false

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
        .scaleEffect(isHovering && !isDisabled ? 1.02 : 1)
        .onHover { hovering in
            withAnimation(.easeOut(duration: 0.16)) {
                isHovering = hovering
            }
        }
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
                    .frame(width: 21, height: 21)
            case "codex", "codex-web", "codex-remote":
                Image("codex_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 21, height: 21)
            case "grokbuild":
                Image("grok_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 23, height: 23)
            case "opencode":
                Image("opencode_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 23, height: 23)
            case "deepseek-web", "dsh-web", "deepseek":
                Image("deepseek_logo")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 23, height: 23)
            default:
                Image(systemName: "command")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 21, height: 21)
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

/// 工作站主操作：静态蓝底白字，不做扫光/呼吸（持续动画长期占用 CPU）；悬停时轻微放大。
/// 首次使用（无设备）与常规头部共用，减少视觉层级干扰。
private struct PairDeviceButton: View {
    let title: String
    let action: () -> Void

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
        }
        .overlay {
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .stroke(.white.opacity(0.18), lineWidth: 1)
        }
        .shadow(
            color: Color.accentColor.opacity(0.12),
            radius: 4,
            y: 1
        )
        .scaleEffect(isHovering ? 1.02 : 1)
        .onHover { hovering in
            withAnimation(.easeOut(duration: 0.16)) {
                isHovering = hovering
            }
        }
        .accessibilityHint(L10n.pairNewDevice)
    }
}

private struct RevokeButton: View {
    let action: () -> Void
    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            Text(L10n.current == .zhHans ? "撤销授权" : "Revoke")
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
        .scaleEffect(isHovering ? 1.02 : 1)
        .onHover { hovering in
            withAnimation(.easeOut(duration: 0.16)) {
                isHovering = hovering
            }
        }
    }
}
