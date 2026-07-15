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
    @AppStorage("appLanguage") private var appLanguage = ""
    @EnvironmentObject private var dependencies: AppDependencies

    var body: some View {
        workspaceTab
            .frame(minWidth: LayoutConstants.minWindowWidth, minHeight: LayoutConstants.minWindowHeight)
        .background {
            ZStack {
                Color(red: 0.165, green: 0.170, blue: 0.180)
                RadialGradient(
                    colors: [Color(red: 0.15, green: 0.22, blue: 0.30).opacity(0.32), .clear],
                    center: .topLeading,
                    startRadius: 80,
                    endRadius: 860
                )
                RadialGradient(
                    colors: [Color(red: 0.32, green: 0.20, blue: 0.12).opacity(0.30), .clear],
                    center: .topTrailing,
                    startRadius: 120,
                    endRadius: 820
                )
                VStack {
                    Spacer()
                    LinearGradient(
                        colors: [.clear, .black.opacity(0.18)],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                    .frame(height: 240)
                }
            }
            .ignoresSafeArea()
        }
        .overlay(alignment: .top) {
            ZStack {
                Text("CordCode Link")
                    .font(.system(size: 17, weight: .semibold))
                    .foregroundStyle(.primary)
                    .padding(.top, 5)
                    .allowsHitTesting(false)
                
                HStack {
                    Spacer()
                    HStack(spacing: 14) {
                        Button {
                            let nextLang = (L10n.current == .zhHans) ? "en" : "zh-Hans"
                            appLanguage = nextLang
                        } label: {
                            Text(L10n.current == .zhHans ? "EN" : "中")
                                .font(.system(size: 11, weight: .bold))
                                .foregroundStyle(.primary.opacity(0.85))
                        }
                        .buttonStyle(.plain)

                        Button {
                            showConnectionStatus = true
                        } label: {
                            Image(systemName: "antenna.radiowaves.left.and.right")
                                .font(.system(size: 14))
                                .foregroundStyle(.primary.opacity(0.85))
                        }
                        .buttonStyle(.plain)
                        
                        Button {
                            showDiagnostics = true
                        } label: {
                            Image(systemName: "stethoscope")
                                .font(.system(size: 14))
                                .foregroundStyle(.primary.opacity(0.85))
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.horizontal, 12)
                    .frame(height: 28)
                    .background {
                        Capsule()
                            .fill(Color.white.opacity(0.08))
                    }
                    .overlay {
                        Capsule()
                            .stroke(Color.white.opacity(0.12), lineWidth: 0.5)
                    }
                }
                .padding(.top, 5)
                .padding(.trailing, 25)
            }
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
            PairingSheet(viewModel: pairingViewModel) {
                dependencies.runtimeManager.start()
            }
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
