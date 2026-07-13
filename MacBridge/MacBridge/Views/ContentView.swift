import SwiftUI

// MARK: - 导航枚举

/// 主窗口 sidebar 一级项。UX 重设计（2026-07-13）P0-1：只保留日常任务。
/// 原来的 remoteAccess / settings / diagnostics 改为 Toolbar 按钮与原生 Settings scene。
enum NavigationTab: String, CaseIterable, Identifiable {
    case workspace
    case devices

    var id: String { rawValue }

    var title: String {
        switch self {
        case .workspace: return L10n.workspace
        case .devices: return L10n.devices
        }
    }

    var systemImage: String {
        switch self {
        case .workspace: return "circle.hexagonpath"
        case .devices: return "lock.shield"
        }
    }
}

/// 主窗口内容，承载各功能标签页
struct ContentView: View {
    @ObservedObject var viewModel: BridgeStatusViewModel
    @ObservedObject var pairingViewModel: PairingViewModel
    @ObservedObject var settingsViewModel: SettingsViewModel
    @StateObject private var backendVM = BackendStatusViewModel()
    @StateObject private var deviceStore = DeviceStore()
    @StateObject private var diagnosticsVM = DiagnosticsViewModel()

    @State private var selectedTab: NavigationTab = .workspace
    /// 连接状态 Sheet（原 RemoteAccessView）与诊断 Sheet（原 logsTab）由 Toolbar 触发。
    @State private var showConnectionStatus = false
    @State private var showDiagnostics = false
    @EnvironmentObject private var dependencies: AppDependencies

    var body: some View {
        HStack(spacing: 0) {
            sidebar

            Divider()

            currentTabContent
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .frame(minWidth: LayoutConstants.minWindowWidth, minHeight: LayoutConstants.minWindowHeight)
        .background(Color(NSColor.windowBackgroundColor))
        .onChange(of: dependencies.runtimeManager.managementURL) { _, _ in
            configureBackendClientIfAvailable()
        }
        .onChange(of: dependencies.runtimeManager.managementToken) { _, _ in
            configureBackendClientIfAvailable()
        }
        .onChange(of: selectedTab) { _, tab in
            if tab == .workspace {
                Task {
                    await deviceStore.loadDevices()
                    await viewModel.refreshOverviewData(force: false)
                }
            }
            if tab == .devices { Task { await deviceStore.loadDevices() } }
        }
        .onAppear {
            configureBackendClientIfAvailable()
            diagnosticsVM.configure(logFilePath: dependencies.runtimeManager.config.logFilePath)
            pairingViewModel.onApproved = {
                await deviceStore.loadDevices()
            }
        }
        .task {
            await deviceStore.loadDevices()
            configureBackendClientIfAvailable()
            diagnosticsVM.configure(logFilePath: dependencies.runtimeManager.config.logFilePath)
            await backendVM.loadAgents()
            await viewModel.refreshOverviewData()
        }
        // P2-3：⌘⇧D / ⌘⇧L 键盘命令打开对应工作表。
        .onReceive(NotificationCenter.default.publisher(for: .openDiagnosticsRequest)) { _ in
            showDiagnostics = true
        }
        .onReceive(NotificationCenter.default.publisher(for: .openConnectionStatusRequest)) { _ in
            showConnectionStatus = true
        }
        .toolbar {
            // 连接状态与帮助与诊断：不再是一级 sidebar 目的地，而是 Toolbar 一跳可达的 Sheet。
            ToolbarItemGroup(placement: .primaryAction) {
                Button {
                    showConnectionStatus = true
                } label: {
                    Label(L10n.connectionStatus, systemImage: "antenna.radiowaves.left.and.right")
                }
                .help(L10n.connectionStatus)

                Button {
                    showDiagnostics = true
                } label: {
                    Label(L10n.helpDiagnostics, systemImage: "stethoscope")
                }
                .help(L10n.helpDiagnostics)
            }
        }
        .sheet(isPresented: $showConnectionStatus) {
            // P0 阶段承载现有 RemoteAccessView；P1-2 将其重做为单页连接状态 Sheet。
            RemoteAccessView(apiClient: apiClient)
        }
        .sheet(isPresented: $showDiagnostics) {
            // P2-2：抽离为摘要优先的 DiagnosticsSheet（健康结论 → 复制支持信息 → 原始日志）。
            DiagnosticsSheet(
                diagnosticsViewModel: diagnosticsVM,
                bridgeStatus: viewModel,
                backendStatus: backendVM
            )
        }
    }

    // MARK: - Navigation

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 3) {
            ForEach(NavigationTab.allCases) { tab in
                Button {
                    selectedTab = tab
                } label: {
                    Label(tab.title, systemImage: tab.systemImage)
                        .labelStyle(.titleAndIcon)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .font(.system(size: 14, weight: selectedTab == tab ? .semibold : .medium))
                        .padding(.horizontal, 10)
                        .padding(.vertical, 7)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundColor(selectedTab == tab ? .primary : .secondary)
                .background {
                    if selectedTab == tab {
                        RoundedRectangle(cornerRadius: 8)
                            .fill(Color.accentColor.opacity(0.18))
                    }
                }
            }

            Spacer()
        }
        .padding(.horizontal, 10)
        .padding(.top, 14)
        .padding(.bottom, 10)
        .frame(width: 180)
        .background(Color(NSColor.controlBackgroundColor).opacity(0.35))
    }

    @ViewBuilder
    private var currentTabContent: some View {
        switch selectedTab {
        case .workspace:
            workspaceTab
        case .devices:
            devicesTab
        }
    }

    // MARK: - Workspace Tab

    private var workspaceTab: some View {
        WorkspaceView(
            viewModel: viewModel,
            backendViewModel: backendVM,
            deviceStore: deviceStore,
            onStartBridge: {
                dependencies.runtimeManager.start()
            },
            onStopBridge: {
                dependencies.runtimeManager.stop()
            },
            onRestartBridge: {
                dependencies.runtimeManager.restart()
            },
            onNavigateToDevices: {
                selectedTab = .devices
            },
            onOpenConnectionStatus: {
                showConnectionStatus = true
            },
            onPairDevice: {
                selectedTab = .devices
                pairingViewModel.startPairing()
            }
        )
    }

    // MARK: - Devices Tab

    @State private var deviceToRemove: TrustedDevice?
    @State private var showRemoveConfirmation = false

    private var devicesTab: some View {
        PageContainer {
            VStack(alignment: .leading, spacing: 16) {
                PageHeader(L10n.devices, subtitle: L10n.devicesSubtitle)

                // 配对区域
                PairingView(viewModel: pairingViewModel)

                Divider()

                // 设备列表
                Text(L10n.authorizedDevices)
                    .font(.headline)

                if let devicesError = deviceStore.devicesError {
                    InlineFeedback(style: .error, message: devicesError)
                    Button(L10n.retry) {
                        Task { await deviceStore.loadDevices() }
                    }
                } else if deviceStore.devices.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        HStack(spacing: 6) {
                            Image(systemName: "info.circle")
                                .foregroundColor(.secondary)
                            Text(L10n.noAuthorizedDevices)
                                .foregroundColor(.secondary)
                                .font(.subheadline)
                        }
                        Button(L10n.addDevice) {
                            // 配对区域已在上方，按钮提示用户使用上方配对
                            // 如需，可触发通知或直接开始配对
                            if pairingViewModel.uiState == .idle {
                                pairingViewModel.startPairing()
                            }
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                    }
                } else {
                    VStack(spacing: 0) {
                        ForEach(deviceStore.devices) { device in
                            deviceRow(device)
                            if device.id != deviceStore.devices.last?.id {
                                Divider()
                            }
                        }
                    }
                    .background(Color(NSColor.controlBackgroundColor).opacity(0.3))
                    .cornerRadius(6)
                    .overlay(
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(Color(NSColor.separatorColor).opacity(0.2), lineWidth: 1)
                    )
                }
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

    private func deviceRow(_ device: TrustedDevice) -> some View {
        HStack(spacing: 10) {
            Image(systemName: device.platform == "ios" ? "iphone" : "desktopcomputer")
                .foregroundColor(.secondary)
                .frame(width: 20)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    Text(device.displayName ?? device.deviceId)
                        .fontWeight(.medium)
                    // 同名设备显示 ID 后 6 位
                    if deviceStore.devices.filter({ $0.displayName == device.displayName }).count > 1 {
                        Text("(\(String(device.deviceId.suffix(6))))")
                            .font(.caption2)
                            .foregroundColor(.secondary)
                    }
                }
                HStack(spacing: 8) {
                    if let platform = device.platform {
                        Text(platform)
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    if let created = device.createdAt {
                        Text(String(format: L10n.paired, Self.relativeTimeString(created)))
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    if let lastSeen = device.lastSeenAt {
                        Text(String(format: L10n.lastSeen, Self.relativeTimeString(lastSeen)))
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                }
            }

            Spacer()

            // 撤销入口改为行尾 … 菜单（P1-1）。保留既有 confirmationDialog 二次确认与安全行为。
            Menu {
                Button(L10n.devicesRevokeAuthorization, role: .destructive) {
                    deviceToRemove = device
                    showRemoveConfirmation = true
                }
            } label: {
                Image(systemName: "ellipsis")
                    .foregroundColor(.secondary)
            }
            .menuStyle(.borderlessButton)
            .frame(width: 20)
            .accessibilityLabel(L10n.devicesActions)
        }
        .padding(.vertical, 6)
    }

    private static func relativeTimeString(_ isoString: String) -> String {
        RelativeTimeFormatter.string(isoString)
    }

    // MARK: - Data Loading

    private var apiClient: ManagementAPIClient? {
        guard let url = dependencies.runtimeManager.managementURL,
              let token = dependencies.runtimeManager.managementToken,
              !url.isEmpty, !token.isEmpty else {
            return nil
        }
        return try? ManagementAPIClient(baseURL: url, token: token)
    }

    private func configureBackendClientIfAvailable() {
        if let client = apiClient {
            backendVM.configure(apiClient: client)
            viewModel.configure(apiClient: client)
            deviceStore.configure(apiClient: client)
            settingsViewModel.managementAPIClient = client
            settingsViewModel.loadDisplayName()
        }
    }
}
