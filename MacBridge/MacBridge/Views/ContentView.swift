import SwiftUI

/// 主窗口内容，承载各功能标签页
struct ContentView: View {
    @ObservedObject var viewModel: BridgeStatusViewModel
    @ObservedObject var pairingViewModel: PairingViewModel
    @ObservedObject var settingsViewModel: SettingsViewModel
    @StateObject private var backendVM = BackendStatusViewModel()
    @StateObject private var deviceStore = DeviceStore()
    @StateObject private var diagnosticsVM = DiagnosticsViewModel()

    /// 连接状态 Sheet（原 RemoteAccessView）与诊断 Sheet（原 logsTab）由 Toolbar 触发。
    @State private var showConnectionStatus = false
    @State private var showDiagnostics = false
    @State private var showPairing = false
    @EnvironmentObject private var dependencies: AppDependencies

    var body: some View {
        workspaceTab
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        .frame(minWidth: LayoutConstants.minWindowWidth, minHeight: LayoutConstants.minWindowHeight)
        .background {
            Rectangle()
                .fill(.regularMaterial)
                .overlay(Color(red: 0.16, green: 0.16, blue: 0.145).opacity(0.60))
        }
        .onChange(of: dependencies.runtimeManager.managementURL) { _, _ in
            Task { await reloadManagementAPIData() }
        }
        .onChange(of: dependencies.runtimeManager.managementToken) { _, _ in
            Task { await reloadManagementAPIData() }
        }
        .onAppear {
            configureBackendClientIfAvailable()
            diagnosticsVM.configure(logFilePath: dependencies.runtimeManager.config.logFilePath)
            pairingViewModel.onApproved = {
                await deviceStore.loadDevices()
            }
        }
        .task {
            await reloadManagementAPIData()
            diagnosticsVM.configure(logFilePath: dependencies.runtimeManager.config.logFilePath)
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
        .sheet(isPresented: $showPairing, onDismiss: pairingViewModel.reset) {
            PairingSheet(viewModel: pairingViewModel)
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
            onOpenConnectionStatus: {
                showConnectionStatus = true
            },
            onPairDevice: {
                pairingViewModel.startPairing()
                showPairing = true
            }
        )
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

    private func configureBackendClientIfAvailable() -> Bool {
        guard let client = apiClient else { return false }
        backendVM.configure(apiClient: client)
        viewModel.configure(apiClient: client)
        deviceStore.configure(apiClient: client)
        settingsViewModel.managementAPIClient = client
        settingsViewModel.loadDisplayName()
        return true
    }

    /// Management API 的 URL 与 token 分别发布。只有两者齐备才读取任何首页状态，
    /// 避免启动阶段将 Relay 的「尚未读取」误显示为「未开启」。
    private func reloadManagementAPIData() async {
        guard configureBackendClientIfAvailable() else { return }
        async let devices: Void = deviceStore.loadDevices()
        async let agents: Void = backendVM.loadAgents()
        async let overview: Void = viewModel.refreshOverviewData()
        _ = await (devices, agents, overview)
    }
}
