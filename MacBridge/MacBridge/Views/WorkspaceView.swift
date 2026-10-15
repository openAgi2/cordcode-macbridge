import SwiftUI

/// 工作站首屏：已授权设备 → MacBridge 服务/Relay → Agent 安装与接入状态。
/// 三层共享一个只读拓扑画布；业务状态仍分别由 DeviceStore、RuntimeManager、
/// workspace snapshot 和既有 seat-action owner 提供。
struct WorkspaceView: View {
    @ObservedObject var viewModel: BridgeStatusViewModel
    @ObservedObject var backendViewModel: BackendStatusViewModel
    @ObservedObject var agentSelectionStore: AgentSelectionStore
    @ObservedObject var deviceStore: DeviceStore
    @ObservedObject var pairingViewModel: PairingViewModel
    @ObservedObject var runtimeManager: RuntimeManager
    let onStartBridge: () -> Void
    let onStopBridge: () -> Void
    let onRestartBridge: () -> Void
    var onOpenConnectionStatus: (() -> Void)?
    let onPairDevice: () -> Void
    let onSetAgentEnabled: (Bool, String) async throws -> Void

    @State private var showStopConfirmation = false
    @State private var isRestarting = false
    @State private var deviceToRemove: TrustedDevice?
    @State private var showRemoveConfirmation = false
    @State private var copiedWebLinkId: String? = nil
    @State private var generatingWebLinkId: String? = nil
    @State private var isRestartingCodexDaemon = false
    @State private var codexRestartMessage: String? = nil
    @State private var showCodexDesktopPairing = false
    @State private var versionDetailAgentID: String?
    @State private var copiedVersionRecipeAgentID: String?
    @State private var copiedInstallRecipeAgentID: String?
    @State private var topologyAnchors: [String: Anchor<CGRect>] = [:]
    @State private var isShowingGrokLeaderDetail = false
    @StateObject private var grokLeaderManager = GrokLeaderModeManager()
    @State private var grokLeaderFailureAlert: GrokLeaderToggleFailure?
    @State private var agentSelectionFailureMessage: String?

    private let topologyCoordinateSpace = "workspace-topology"

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

    var body: some View {
        PageContainer(maxContentWidth: .greatestFiniteMagnitude) {
            ZStack {
                topologyCanvas
                VStack(alignment: .leading, spacing: 24) {
                    devicesSection
                        .padding(.top, 18)
                    serviceSection
                    agentSection
                }
            }
            .coordinateSpace(name: topologyCoordinateSpace)
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
        .alert(
            L10n.workspaceAgentConnectFailed,
            isPresented: Binding(
                get: { agentSelectionFailureMessage != nil },
                set: { if !$0 { agentSelectionFailureMessage = nil } }
            )
        ) {
            Button(L10n.ok, role: .cancel) { agentSelectionFailureMessage = nil }
        } message: {
            Text(agentSelectionFailureMessage ?? "")
        }
    }

    // MARK: - 健康结论

    private var pairDeviceButton: some View {
        PairDeviceButton(title: L10n.addNewDevice) {
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
        VStack(alignment: .leading, spacing: 14) {
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
                pairDeviceButton
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
                Label(L10n.workspaceNoAuthorizedDevices, systemImage: "iphone.slash")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            } else {
                LazyVGrid(
                    columns: [GridItem(.adaptive(minimum: 320, maximum: 430), spacing: 16)],
                    alignment: .leading,
                    spacing: 14
                ) {
                    ForEach(deviceStore.devices) { device in
                        deviceRow(device)
                    }
                }
            }
        }
    }

    private func deviceRow(_ device: TrustedDevice) -> some View {
        let presence = deviceStore.presenceState(for: device.deviceId)
        return VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: device.platform == "ios" ? "iphone.gen3" : "desktopcomputer")
                    .font(.system(size: 19))
                    .foregroundStyle(.primary.opacity(0.85))
                    .frame(width: 22)
                Text(device.displayName ?? device.deviceId)
                    .font(.system(size: 17, weight: .semibold))
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(device.displayName ?? device.deviceId)
                if deviceStore.devices.filter({ $0.displayName == device.displayName }).count > 1 {
                    Text("(\(String(device.deviceId.suffix(6))))")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                PresenceDotView(state: presence)
                    .help(Self.presenceAccessibilityText(for: presence))
                Spacer(minLength: 8)
                Text(Self.presenceText(for: presence))
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }

            HStack(alignment: .center) {
                Text(deviceDetails(for: device))
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(deviceDetails(for: device))
                Spacer(minLength: 12)

                // Single official web pairing link (`/web/`); candidate `/web-v2/` is retired.
                let isWebLoading = generatingWebLinkId == device.deviceId
                let isWebCopied = copiedWebLinkId == device.deviceId
                Button {
                    handleCopyLink(forDevice: device)
                } label: {
                    Group {
                        if isWebLoading {
                            ProgressView().controlSize(.small)
                        } else {
                            Image(systemName: isWebCopied ? "checkmark" : "doc.on.clipboard")
                        }
                    }
                    .frame(width: 26, height: 24)
                }
                .buttonStyle(.borderless)
                .disabled(generatingWebLinkId != nil)
                .help(isWebCopied ? L10n.pairingLinkCopied : L10n.copyPairingLink)
                .accessibilityLabel(L10n.copyPairingLink)

                Button {
                    deviceToRemove = device
                    showRemoveConfirmation = true
                } label: {
                    Image(systemName: "trash")
                        .frame(width: 26, height: 24)
                }
                .buttonStyle(.borderless)
                .foregroundStyle(Color(red: 0.92, green: 0.35, blue: 0.35))
                .disabled(deviceStore.isRevoking)
                .help(L10n.removeDevice)
                .accessibilityLabel(L10n.removeDevice)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .fill(Color.white.opacity(0.055))
        }
        .anchorPreference(
            key: WorkspaceTopologyAnchorKey.self,
            value: .bounds
        ) {
            ["device:\(device.deviceId)": $0]
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

    // MARK: - 三层拓扑画布

    private var topologyCanvas: some View {
        GeometryReader { proxy in
            Canvas { context, _ in
                guard isServiceRunning else { return }
                let service = anchorRect("service", proxy: proxy)
                let relay = anchorRect("relay", proxy: proxy)
                guard let service else { return }

                for device in deviceStore.devices {
                    guard case .connected(let transports) = deviceStore.presenceState(for: device.deviceId),
                          let deviceRect = anchorRect("device:\(device.deviceId)", proxy: proxy) else {
                        continue
                    }
                    for transport in transports {
                        if transport.kind == "relay" {
                            guard let relay else { continue }
                            strokeRoundedPolyline(
                                in: &context,
                                from: .bottomCenter(of: deviceRect),
                                to: .topCenter(of: relay),
                                color: .accentColor.opacity(0.58)
                            )
                            strokeRoundedPolyline(
                                in: &context,
                                from: .trailingCenter(of: relay),
                                to: .trailingCenter(of: service),
                                color: .accentColor.opacity(0.58)
                            )
                        } else {
                            strokeRoundedPolyline(
                                in: &context,
                                from: .bottomCenter(of: deviceRect),
                                to: .topCenter(of: service),
                                color: .green.opacity(0.58)
                            )
                        }
                    }
                }

                for agent in agents {
                    guard agent.isActive,
                          agent.isObservationCurrent,
                          let agentRect = anchorRect("agent:\(agent.id)", proxy: proxy) else {
                        continue
                    }
                    strokeRoundedPolyline(
                        in: &context,
                        from: .bottomCenter(of: service),
                        to: .topCenter(of: agentRect),
                        color: agent.isAvailable ? .green.opacity(0.52) : .primary.opacity(0.24),
                        dashed: !agent.isAvailable
                    )
                }
            }
        }
        .allowsHitTesting(false)
        .onPreferenceChange(WorkspaceTopologyAnchorKey.self) { anchors in
            topologyAnchors = anchors
        }
    }

    private func anchorRect(
        _ key: String,
        proxy: GeometryProxy
    ) -> CGRect? {
        guard let anchor = topologyAnchors[key] else { return nil }
        return proxy[anchor]
    }

    private func strokeRoundedPolyline(
        in context: inout GraphicsContext,
        from start: CGPoint,
        to end: CGPoint,
        color: Color,
        dashed: Bool = false
    ) {
        var path = Path()
        let points = Self.topologyPolylinePoints(from: start, to: end)
        path.move(to: points[0])
        for point in points.dropFirst() {
            path.addLine(to: point)
        }
        var style = StrokeStyle(lineWidth: 1.5, lineCap: .round, lineJoin: .round)
        if dashed {
            style.dash = [5, 5]
        }
        context.stroke(path, with: .color(color), style: style)
    }

    static func topologyPolylinePoints(from start: CGPoint, to end: CGPoint) -> [CGPoint] {
        let middleY = (start.y + end.y) / 2
        return [
            start,
            CGPoint(x: start.x, y: middleY),
            CGPoint(x: end.x, y: middleY),
            end
        ]
    }

    // MARK: - 中间服务层

    private var serviceSection: some View {
        HStack(alignment: .top, spacing: 22) {
            serviceCard
                .frame(maxWidth: .infinity)
            relayCard
                .frame(width: 285)
        }
        .frame(maxWidth: .infinity)
    }

    private var serviceCard: some View {
        VStack(alignment: .leading, spacing: 15) {
            HStack(spacing: 10) {
                Image(systemName: "dot.radiowaves.left.and.right")
                    .font(.system(size: 25, weight: .light))
                    .foregroundStyle(serviceColor)
                    .frame(width: 30)
                VStack(alignment: .leading, spacing: 2) {
                    Text(L10n.workspaceServiceTitle)
                        .font(.system(size: 19, weight: .semibold))
                    Text(serviceStatusText)
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }

            HStack(spacing: 12) {
                headlineActions
            }
        }
        .padding(18)
        .background {
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .fill(Color.white.opacity(0.065))
        }
        .overlay {
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .stroke(serviceColor.opacity(0.28), lineWidth: 1)
        }
        .anchorPreference(
            key: WorkspaceTopologyAnchorKey.self,
            value: .bounds
        ) {
            ["service": $0]
        }
    }

    private var relayCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 9) {
                Circle()
                    .fill(relayColor)
                    .frame(width: 9, height: 9)
                Text(L10n.workspaceRelayTitle)
                    .font(.system(size: 15, weight: .semibold))
                Spacer()
            }
            Text(relaySummary)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if viewModel.relayConfigured != true {
                Button(L10n.connectionStatus) {
                    onOpenConnectionStatus?()
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
            Spacer(minLength: 0)
        }
        .padding(16)
        .background {
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .fill(Color.white.opacity(0.045))
        }
        .anchorPreference(
            key: WorkspaceTopologyAnchorKey.self,
            value: .bounds
        ) {
            ["relay": $0]
        }
    }

    private var serviceColor: Color {
        switch viewModel.status {
        case .ready, .readyNoAgents:
            return .green
        case .starting:
            return .orange
        case .crashed:
            return .red
        case .stopped, .idle, .sleeping:
            return .secondary
        }
    }

    private var isServiceRunning: Bool {
        viewModel.status == .ready || viewModel.status == .readyNoAgents || viewModel.status == .starting
    }

    private var serviceStatusText: String {
        switch viewModel.status {
        case .ready:
            return L10n.workspaceServiceRunning
        case .readyNoAgents:
            return L10n.workspaceServiceRunningNoAgents
        case .starting:
            return L10n.overviewStarting
        case .stopped, .idle, .sleeping:
            return L10n.workspaceServiceStopped
        case .crashed:
            return viewModel.lastError ?? L10n.overviewStartFailed
        }
    }

    // MARK: - Agent 分组

    private var agentSection: some View {
        VStack(alignment: .leading, spacing: 18) {
            if let error = agentSelectionStore.errorMessage {
                Text(error)
                    .font(.system(size: 11))
                    .foregroundStyle(.red)
            }
            if let error = backendViewModel.inventoryRescanError {
                Text(error)
                    .font(.system(size: 11))
                    .foregroundStyle(.orange)
            }
            if let error = backendViewModel.versionRescanError {
                Text(error)
                    .font(.system(size: 11))
                    .foregroundStyle(.orange)
            }

            ForEach(WorkspaceAgentGroup.allCases) { group in
                let groupAgents = agents.filter { Self.agentGroup(for: $0.installation?.state) == group }
                if !groupAgents.isEmpty {
                    VStack(alignment: .leading, spacing: 9) {
                        HStack(spacing: 8) {
                            Text(group.title)
                                .font(.system(size: 16, weight: .semibold))
                            if group == .installed {
                                Button {
                                    Task { await backendViewModel.rescanInventory() }
                                } label: {
                                    Image(systemName: "arrow.clockwise.circle")
                                }
                                .buttonStyle(.plain)
                                .foregroundStyle(.secondary)
                                .disabled(backendViewModel.isRescanningInventory)
                                .help(L10n.workspaceRecheckTools)
                                .accessibilityLabel(L10n.workspaceRecheckTools)
                            }
                            Spacer()
                        }
                        VStack(spacing: 8) {
                            ForEach(groupAgents) { agent in
                                agentRow(for: agent)
                            }
                        }
                    }
                }
            }
        }
        .padding(.bottom, 10)
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

    private func isCodexDesktop(_ agent: BackendAgentStatus) -> Bool {
        let kind = agent.kind.lowercased()
        return agent.id == "codex-remote" || kind == "codex-remote"
    }

    enum WorkspaceAgentGroup: String, CaseIterable, Identifiable {
        case installed
        case missing
        case external
        case pending

        var id: String { rawValue }

        var title: String {
            switch self {
            case .installed: return L10n.workspaceInstalledAgents
            case .missing: return L10n.workspaceMissingAgents
            case .external: return L10n.workspaceExternalAgents
            case .pending: return L10n.workspacePendingAgents
            }
        }
    }

    static func agentGroup(for installationState: String?) -> WorkspaceAgentGroup {
        switch installationState {
        case "installed":
            return .installed
        case "missing":
            return .missing
        case "not_required":
            return .external
        default:
            return .pending
        }
    }

    static func presenceText(for state: DeviceStore.PresenceState) -> String {
        switch state {
        case .unknown:
            return L10n.devicePresenceUnknown
        case .disconnected:
            return L10n.devicePresenceDisconnected
        case .connected(let transports):
            if transports.count > 1 {
                return L10n.devicePresenceSwitching
            }
            if transports.first?.kind == "relay" {
                return L10n.devicePresenceRelay
            }
            return L10n.devicePresenceDirect
        }
    }

    static func presenceAccessibilityText(for state: DeviceStore.PresenceState) -> String {
        switch state {
        case .unknown:
            return L10n.devicePresenceUnknownAccessibility
        case .disconnected:
            return L10n.devicePresenceDisconnectedAccessibility
        case .connected(let transports):
            let names = transports.map { transport in
                transport.kind == "relay" ? L10n.devicePresenceRelay : L10n.devicePresenceDirect
            }
            return L10n.devicePresenceConnectedAccessibility + ": " + names.joined(separator: " / ")
        }
    }

    private func isDeepSeekWeb(_ agent: BackendAgentStatus) -> Bool {
        agent.kind.lowercased() == "deepseek-web" || agent.id == "dsh-web"
    }

    /// 行状态文本：dsh / OpenCode 行走各自的本地覆盖，其余走全局映射。
    private func rowStatusText(for agent: BackendAgentStatus) -> String {
        if !agent.isActive {
            return L10n.workspaceAgentNotConnected
        }
        if !isServiceRunning {
            return L10n.workspaceServiceStopped
        }
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
        HStack(alignment: .center, spacing: 16) {
            AgentBrandMark(kind: agent.kind)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 8) {
                    Text(agent.displayName)
                        .font(.system(size: 17, weight: .semibold))
                    if let version = agent.snapshotVersion, !version.isEmpty {
                        Text(version)
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(.secondary)
                    }
                    versionPill(for: agent)
                    if agent.installation?.state == "missing" {
                        Text(L10n.workspaceAgentMissing)
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(.secondary)
                        installGuidanceControls(for: agent)
                    }
                }

                HStack(spacing: 7) {
                    Circle()
                        .fill(agentStatusColor(for: agent))
                        .frame(width: 7, height: 7)
                    Text(rowStatusText(for: agent))
                        .font(.system(size: 13, weight: .medium))
                        .foregroundStyle(agentStatusColor(for: agent).opacity(0.9))
                    if !agent.isObservationCurrent {
                        Text(L10n.workspaceObservationStale)
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                }

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
            .frame(minWidth: 320, alignment: .leading)

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
                grokLeaderDisclosure(for: agent)
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

            agentConnectionToggle(for: agent)
                .frame(width: 74, height: 32)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .frame(minHeight: 64)
        .background {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .fill(Color.white.opacity(0.04))
        }
        .anchorPreference(
            key: WorkspaceTopologyAnchorKey.self,
            value: .bounds
        ) {
            ["agent:\(agent.id)": $0]
        }
    }

    private func agentStatusColor(for agent: BackendAgentStatus) -> Color {
        if !agent.isActive {
            return .secondary
        }
        return agent.isAvailable ? .green : .orange
    }

    @ViewBuilder
    private func installGuidanceControls(for agent: BackendAgentStatus) -> some View {
        if let guidance = agent.installGuidance {
            if let recipe = guidance.recipe {
                ScrollView(.horizontal, showsIndicators: false) {
                    Text(recipe.display)
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: true, vertical: false)
                        .help(recipe.display)
                }
                .frame(maxWidth: 420)

                Button {
                    copyInstallRecipe(recipe, for: agent)
                } label: {
                    Image(systemName: copiedInstallRecipeAgentID == agent.id ? "checkmark" : "doc.on.doc")
                        .font(.system(size: 10, weight: .semibold))
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .help(copiedInstallRecipeAgentID == agent.id ? L10n.versionCopied : L10n.versionCopyCommand)
                .accessibilityLabel(L10n.versionCopyCommand)
            } else {
                if let reason = guidance.reason, !reason.isEmpty {
                    Text(reason)
                        .font(.system(size: 10))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .help(reason)
                }
                Button(L10n.workspaceOpenInstallGuide) {
                    if let url = URL(string: guidance.documentationUrl) {
                        NSWorkspace.shared.open(url)
                    }
                }
                .buttonStyle(.borderless)
                .font(.system(size: 11))
                .help(guidance.documentationUrl)
            }
        }
    }

    private func agentConnectionToggle(for agent: BackendAgentStatus) -> some View {
        let explicitSelection = agentSelectionStore.selection != nil
        let isOn = agentSelectionStore.isEnabled(agent.id)
        let canInstallOrRun = agent.installation?.state == "installed" || agent.installation?.state == "not_required"
        return Toggle(L10n.workspaceAgentConnect, isOn: Binding(
            get: { isOn },
            set: { enabled in
                setAgentConnection(enabled, agent: agent)
            }
        ))
            .toggleStyle(.switch)
            .controlSize(.small)
            .labelsHidden()
            .disabled(!explicitSelection || !canInstallOrRun)
            .help(explicitSelection ? L10n.workspaceAgentConnectHelp : L10n.workspaceAgentConnectDeferred)
            .accessibilityLabel(String(format: L10n.workspaceAgentConnectFormat, agent.displayName))
    }

    private func setAgentConnection(_ enabled: Bool, agent: BackendAgentStatus) {
        Task {
            do {
                try await onSetAgentEnabled(enabled, agent.id)
            } catch {
                agentSelectionFailureMessage = error.localizedDescription
            }
        }
    }

    @ViewBuilder
    private func versionPill(for agent: BackendAgentStatus) -> some View {
        if let check = agent.versionCheck {
            switch check.state {
            case "update_available":
                if let target = check.targetVersion {
                    Button {
                        versionDetailAgentID = agent.id
                    } label: {
                        Label(String(format: L10n.versionUpdateTo, target), systemImage: "arrow.down.circle")
                            .font(.system(size: 11, weight: .semibold))
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.mini)
                    .popover(isPresented: versionPopoverBinding(agentID: agent.id)) {
                        versionPopover(for: agent, check: check)
                    }
                }
            case "failed":
                Button(L10n.versionCheckFailed) {
                    versionDetailAgentID = agent.id
                }
                .font(.system(size: 11, weight: .semibold))
                .buttonStyle(.plain)
                .foregroundStyle(.orange)
                .popover(isPresented: versionPopoverBinding(agentID: agent.id)) {
                    versionPopover(for: agent, check: check)
                }
            case "unsupported_source":
                Button(L10n.versionUnsupportedSource) {
                    versionDetailAgentID = agent.id
                }
                .font(.system(size: 11))
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .help(L10n.versionUnsupportedSource)
            default:
                EmptyView()
            }

            Button {
                Task { await backendViewModel.rescanVersions(agentIDs: [agent.id]) }
            } label: {
                Image(systemName: "arrow.clockwise.circle")
                    .font(.system(size: 11, weight: .semibold))
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .help(L10n.versionRecheck)
            .disabled(backendViewModel.isRescanningVersions)
        }
    }

    private func versionPopoverBinding(agentID: String) -> Binding<Bool> {
        Binding(
            get: { versionDetailAgentID == agentID },
            set: { shown in
                if shown {
                    versionDetailAgentID = agentID
                } else if versionDetailAgentID == agentID {
                    versionDetailAgentID = nil
                }
            }
        )
    }

    private func versionPopover(
        for agent: BackendAgentStatus,
        check: WorkspaceSnapshot.VersionCheck
    ) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 6) {
                LabeledContent(L10n.versionCurrentLabel, value: agent.snapshotVersion ?? check.installedVersion ?? "—")
                LabeledContent(L10n.versionTargetLabel, value: check.targetVersion ?? "—")
                LabeledContent(
                    L10n.versionInstallMethod,
                    value: installMethodText(agent: agent, check: check)
                )
            }
            .font(.system(size: 12))

            if let recipe = check.updateRecipe {
                VStack(alignment: .leading, spacing: 6) {
                    Text(L10n.versionOfficialCommand)
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(.secondary)
                    Text(recipe.display)
                        .font(.system(size: 11, design: .monospaced))
                        .textSelection(.enabled)
                        .lineLimit(2)
                        .truncationMode(.tail)
                    Button {
                        copyVersionRecipe(recipe, for: agent)
                    } label: {
                        Label(
                            copiedVersionRecipeAgentID == agent.id ? L10n.versionCopied : L10n.versionCopyCommand,
                            systemImage: copiedVersionRecipeAgentID == agent.id ? "checkmark" : "doc.on.doc"
                        )
                    }
                    .controlSize(.small)
                }
            } else if let reason = check.reason, !reason.isEmpty {
                Text(reason)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Text(L10n.versionRestartNotice)
                .font(.system(size: 10))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(width: 390, alignment: .leading)
    }

    private func installMethodText(
        agent: BackendAgentStatus,
        check: WorkspaceSnapshot.VersionCheck
    ) -> String {
        if let source = check.source, !source.isEmpty {
            return source
        }
        return agent.kind
    }

    private func copyVersionRecipe(
        _ recipe: WorkspaceSnapshot.CommandRecipe,
        for agent: BackendAgentStatus
    ) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(recipe.display, forType: .string)
        copiedVersionRecipeAgentID = agent.id
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(2))
            if copiedVersionRecipeAgentID == agent.id {
                copiedVersionRecipeAgentID = nil
            }
        }
    }

    private func copyInstallRecipe(
        _ recipe: WorkspaceSnapshot.CommandRecipe,
        for agent: BackendAgentStatus
    ) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(recipe.display, forType: .string)
        copiedInstallRecipeAgentID = agent.id
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(2))
            if copiedInstallRecipeAgentID == agent.id {
                copiedInstallRecipeAgentID = nil
            }
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

    private func grokLeaderDisclosure(for agent: BackendAgentStatus) -> some View {
        Button {
            isShowingGrokLeaderDetail = true
        } label: {
            Label(L10n.grokLeaderMode, systemImage: "chevron.right")
                .font(.system(size: 12, weight: .medium))
        }
        .buttonStyle(.borderless)
        .help(L10n.grokLeaderMode)
        .popover(isPresented: $isShowingGrokLeaderDetail) {
            VStack(alignment: .leading, spacing: 12) {
                grokLeaderToggle(for: agent)
                grokLeaderHint(for: agent)
            }
            .padding(16)
            .frame(width: 360, alignment: .leading)
        }
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

    private var relaySummary: String {
        if backendViewModel.isWorkspaceObservationCurrent,
           let relay = backendViewModel.workspaceRelay {
            if !relay.enabled {
                return L10n.workspaceRelayDisabled
            }
            if !relay.configured {
                return L10n.workspaceRelayNotConfigured
            }
            return relay.connected
                ? L10n.workspaceRelayConnected
                : L10n.workspaceRelayNotConnected
        }
        switch viewModel.relayConfigured {
        case true:
            return isServiceRunning
                ? L10n.workspaceRelayStatusUnknown
                : L10n.workspaceRelayUnavailableWhileStopped
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
        if backendViewModel.isWorkspaceObservationCurrent,
           let relay = backendViewModel.workspaceRelay {
            if !relay.enabled || !relay.configured {
                return .secondary
            }
            return relay.connected ? .green : .orange
        }
        switch viewModel.relayConfigured {
        case true: return .green
        case false: return .secondary
        case nil: return .orange
        }
    }

}

private struct WorkspaceTopologyAnchorKey: PreferenceKey {
    static var defaultValue: [String: Anchor<CGRect>] = [:]

    static func reduce(
        value: inout [String: Anchor<CGRect>],
        nextValue: () -> [String: Anchor<CGRect>]
    ) {
        value.merge(nextValue()) { _, current in current }
    }
}

private extension CGPoint {
    static func bottomCenter(of rect: CGRect) -> CGPoint {
        CGPoint(x: rect.midX, y: rect.maxY)
    }

    static func topCenter(of rect: CGRect) -> CGPoint {
        CGPoint(x: rect.midX, y: rect.minY)
    }

    static func trailingCenter(of rect: CGRect) -> CGPoint {
        CGPoint(x: rect.maxX, y: rect.midY)
    }
}

private struct PresenceDotView: View {
    let state: DeviceStore.PresenceState
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var isPulsing = false

    private var isConnected: Bool {
        if case .connected = state { return true }
        return false
    }

    var body: some View {
        ZStack {
            if isConnected {
                Circle()
                    .stroke(Color.green.opacity(isPulsing ? 0.08 : 0.28), lineWidth: 3)
                    .frame(width: 13, height: 13)
                    .scaleEffect(isPulsing ? 1.32 : 0.92)
            }
            Circle()
                .fill(isConnected ? Color.green : Color.secondary.opacity(0.65))
                .frame(width: 8, height: 8)
        }
        .frame(width: 15, height: 15)
        .accessibilityHidden(true)
        .onAppear {
            guard isConnected && !reduceMotion else { return }
            withAnimation(.easeInOut(duration: 1).repeatForever(autoreverses: true)) {
                isPulsing = true
            }
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

/// 工作站主操作：静态蓝底白字，不做扫光/呼吸（持续动画长期占用 CPU）；悬停时轻微放大。
/// 放在「已授权设备」区，保持原有蓝色主按钮形态。
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
        .accessibilityHint(L10n.addNewDevice)
    }
}
