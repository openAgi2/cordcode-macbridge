import SwiftUI

// MARK: - 导航枚举

/// 主窗口 tab 定义，为后续 Sidebar 化准备
enum NavigationTab: String, CaseIterable, Identifiable {
    case overview
    case devices
    case remoteAccess
    case settings
    case diagnostics

    var id: String { rawValue }

    var title: String {
        switch self {
        case .overview: return L10n.overview
        case .devices: return L10n.devices
        case .remoteAccess: return L10n.remoteAccessTab
        case .settings: return L10n.settings
        case .diagnostics: return L10n.diagnostics
        }
    }

    var systemImage: String {
        switch self {
        case .overview: return "circle.hexagonpath"
        case .devices: return "lock.shield"
        case .remoteAccess: return "antenna.radiowaves.left.and.right"
        case .settings: return "gearshape"
        case .diagnostics: return "doc.text"
        }
    }
}

/// 主窗口内容，承载各功能标签页
struct ContentView: View {
    @ObservedObject var viewModel: BridgeStatusViewModel
    @ObservedObject var pairingViewModel: PairingViewModel
    @ObservedObject var settingsViewModel: SettingsViewModel
    @StateObject private var backendVM = BackendStatusViewModel()
    @State private var devices: [TrustedDevice] = []
    @State private var logs: [String] = []
    @State private var isLoadingLogs = false
    @State private var logsError: String?
    @State private var hasLoadedDevices = false
    @State private var devicesError: String?

    @State private var selectedTab: NavigationTab = .overview
    @EnvironmentObject private var dependencies: AppDependencies

    var body: some View {
        HStack(spacing: 0) {
            sidebar

            Divider()

            currentTabContent
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .frame(minWidth: 820, minHeight: 560)
        .background(Color(NSColor.windowBackgroundColor))
        .onChange(of: dependencies.runtimeManager.managementURL) { _, _ in
            configureBackendClientIfAvailable()
        }
        .onChange(of: dependencies.runtimeManager.managementToken) { _, _ in
            configureBackendClientIfAvailable()
        }
        .onChange(of: selectedTab) { _, tab in
            if tab == .overview {
                Task {
                    await loadDevices()
                    await viewModel.refreshOverviewData(force: false)
                }
            }
            if tab == .devices { Task { await loadDevices() } }
            if tab == .diagnostics { Task { await loadLogs() } }
        }
        .onAppear {
            configureBackendClientIfAvailable()
            pairingViewModel.onApproved = {
                await loadDevices()
            }
        }
        .task {
            await loadDevices()
            configureBackendClientIfAvailable()
            await backendVM.loadAgents()
            await viewModel.refreshOverviewData()
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
        case .overview:
            overviewTab
        case .devices:
            devicesTab
        case .remoteAccess:
            remoteAccessTab
        case .settings:
            SettingsView(viewModel: settingsViewModel)
        case .diagnostics:
            logsTab
        }
    }

    // MARK: - Overview Tab

    private var overviewTab: some View {
        BridgeStatusView(
            viewModel: viewModel,
            backendViewModel: backendVM,
            devices: devices,
            hasLoadedDevices: hasLoadedDevices,
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
            onNavigateToRemoteAccess: {
                selectedTab = .remoteAccess
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

    private var remoteAccessTab: some View {
        RemoteAccessView(apiClient: apiClient)
    }

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

                if let devicesError {
                    InlineFeedback(style: .error, message: devicesError)
                    Button(L10n.retry) {
                        Task { await loadDevices() }
                    }
                } else if devices.isEmpty {
                    HStack(spacing: 6) {
                        Image(systemName: "info.circle")
                            .foregroundColor(.secondary)
                        Text(L10n.noAuthorizedDevices)
                            .foregroundColor(.secondary)
                            .font(.subheadline)
                    }
                } else {
                    ForEach(devices) { device in
                        deviceRow(device)
                    }
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
                    Task { await revokeDevice(device) }
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
                    if devices.filter({ $0.displayName == device.displayName }).count > 1 {
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

            Menu {
                Button(L10n.devicesRevokeAuthorization, role: .destructive) {
                    deviceToRemove = device
                    showRemoveConfirmation = true
                }
            } label: {
                Image(systemName: "ellipsis")
            }
            .menuStyle(.borderlessButton)
            .help(L10n.devicesActions)
            .accessibilityLabel(L10n.devicesActions)
        }
        .padding(.vertical, 6)
    }

    private static func relativeTimeString(_ isoString: String) -> String {
        RelativeTimeFormatter.string(isoString)
    }

    // MARK: - Diagnostics Tab

    private var logsTab: some View {
        PageContainer(scrolls: false) {
            VStack(alignment: .leading, spacing: 8) {
                PageHeader(L10n.diagnostics, subtitle: L10n.diagnosticsSubtitle) {
                    Button {
                        Task { await loadLogs() }
                    } label: {
                        HStack(spacing: 4) {
                            if isLoadingLogs {
                                ProgressView()
                                    .controlSize(.small)
                            }
                            Image(systemName: "arrow.clockwise")
                            Text(L10n.refreshAll)
                        }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(isLoadingLogs)

                    Button(L10n.copyRawLogs) {
                        let text = logs.joined(separator: "\n")
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(text, forType: .string)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(logs.isEmpty)
                }

                SectionHeader(L10n.rawLogs)
                    .padding(.top, 10)

                Text(L10n.last200Lines)
                    .font(.caption)
                    .foregroundColor(.secondary)

                if isLoadingLogs && logs.isEmpty {
                    ProgressView(L10n.diagnosticsReading)
                } else if let logsError {
                    InlineFeedback(style: .error, message: logsError)
                } else if logs.isEmpty {
                    Text(L10n.noLogsAvailable)
                        .foregroundColor(.secondary)
                } else {
                    ScrollView(.vertical) {
                        LazyVStack(alignment: .leading, spacing: 2) {
                            ForEach(Array(logs.enumerated()), id: \.offset) { _, line in
                                Text(Self.displayLogLine(line))
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
                }
            }
        }
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
            settingsViewModel.managementAPIClient = client
            settingsViewModel.loadDisplayName()
        }
    }

    private func loadDevices() async {
        guard let client = apiClient else {
            hasLoadedDevices = false
            devicesError = L10n.errorCannotConnect
            return
        }
        do {
            devices = try await client.listDevices()
            hasLoadedDevices = true
            devicesError = nil
        } catch {
            hasLoadedDevices = false
            devicesError = error.localizedDescription
        }
    }

    private func loadLogs() async {
        guard !isLoadingLogs else { return }
        isLoadingLogs = true
        defer { isLoadingLogs = false }

        let logPath = dependencies.runtimeManager.config.logFilePath
        let result = await Task.detached(priority: .utility) {
            Self.readTailLines(at: logPath, maxLines: 200, maxBytes: 1_048_576)
        }.value
        switch result {
        case .success(let lines):
            logs = lines
            logsError = nil
        case .failure(let error):
            logsError = error.localizedDescription
        }
    }

    private nonisolated static func readTailLines(
        at path: String,
        maxLines: Int,
        maxBytes: UInt64
    ) -> Result<[String], Error> {
        let handle: FileHandle
        do {
            handle = try FileHandle(forReadingFrom: URL(fileURLWithPath: path))
        } catch {
            return .failure(error)
        }
        defer { try? handle.close() }

        do {
            let fileSize = try handle.seekToEnd()
            let bytesToRead = min(fileSize, maxBytes)
            guard bytesToRead > 0 else { return .success([]) }
            try handle.seek(toOffset: fileSize - bytesToRead)
            guard let data = try handle.readToEnd(),
                  let text = String(data: data, encoding: .utf8) else {
                return .success([])
            }
            let lines = text.split(separator: "\n", omittingEmptySubsequences: true).map(String.init)
            return .success(Array(lines.suffix(maxLines)))
        } catch {
            return .failure(error)
        }
    }

    private static func displayLogLine(_ line: String) -> String {
        let maxCharacters = 500
        guard line.count > maxCharacters else { return line }
        return String(line.prefix(maxCharacters)) + " …"
    }

    private func revokeDevice(_ device: TrustedDevice) async {
        guard let client = apiClient else {
            devicesError = L10n.errorCannotConnect
            return
        }
        do {
            try await client.revokeDevice(device.deviceId)
            await loadDevices()
        } catch {
            devicesError = String(format: L10n.errorRemoveDevice, error.localizedDescription)
        }
    }
}
