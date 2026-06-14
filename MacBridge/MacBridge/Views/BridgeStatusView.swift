import SwiftUI

struct BridgeStatusView: View {
    @ObservedObject var viewModel: BridgeStatusViewModel
    @ObservedObject var backendViewModel: BackendStatusViewModel
    let devices: [TrustedDevice]
    let hasLoadedDevices: Bool
    let onStartBridge: () -> Void
    let onStopBridge: () -> Void
    let onRestartBridge: () -> Void
    var onNavigateToDevices: (() -> Void)?
    var onNavigateToRemoteAccess: (() -> Void)?
    var onPairDevice: (() -> Void)?

    @State private var showStopConfirmation = false
    @State private var isRestarting = false

    private var overviewAgents: [BackendAgentStatus] {
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

    private var lastSeenText: String? {
        guard let lastSeen = devices.compactMap(\.lastSeenAt).sorted().last else { return nil }
        return RelativeTimeFormatter.string(lastSeen)
    }

    var body: some View {
        PageContainer {
            VStack(alignment: .leading, spacing: 22) {
                PageHeader(L10n.overview, subtitle: L10n.overviewSubtitle) {
                    if let onPairDevice {
                        Button(action: onPairDevice) {
                            Label(L10n.pairNewDevice, systemImage: "qrcode")
                        }
                        .buttonStyle(.borderedProminent)
                    }
                }

                runtimeSection
                Divider()
                agentsSection
                Divider()
                devicesSection
                Divider()
                remoteSection

                if let error = viewModel.overviewDataError {
                    Label(error, systemImage: "exclamationmark.triangle")
                        .font(.caption)
                        .foregroundStyle(.orange)
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

    private var runtimeSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            SectionHeader(L10n.overviewRuntime)
            HStack(alignment: .center, spacing: 12) {
                StatusIndicator(
                    systemImage: runtimeAppearance.icon,
                    color: runtimeAppearance.color,
                    showsProgress: viewModel.status == .starting
                )

                VStack(alignment: .leading, spacing: 4) {
                    Text(runtimeAppearance.title)
                        .font(.body.weight(.medium))
                    Text(runtimeDetail)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    if let error = viewModel.lastError, !error.isEmpty {
                        Text(error)
                            .font(.caption)
                            .foregroundStyle(.red)
                    }
                }

                Spacer()
                runtimeActions
            }
        }
    }

    @ViewBuilder
    private var runtimeActions: some View {
        switch viewModel.status {
        case .ready:
            Button(isRestarting ? L10n.overviewRestarting : L10n.restart) {
                isRestarting = true
                onRestartBridge()
            }
            .disabled(isRestarting)

            Menu {
                Button(L10n.stopBridge, role: .destructive) {
                    showStopConfirmation = true
                }
            } label: {
                Image(systemName: "ellipsis")
            }
            .menuStyle(.borderlessButton)
            .help(L10n.overviewMoreActions)
            .accessibilityLabel(L10n.overviewMoreActions)

        case .readyNoAgents:
            Button(L10n.overviewDetectAgain) {
                Task { await backendViewModel.refreshAgents() }
            }
            .disabled(backendViewModel.isLoading)

            Menu {
                Button(L10n.stopBridge, role: .destructive) {
                    showStopConfirmation = true
                }
            } label: {
                Image(systemName: "ellipsis")
            }
            .menuStyle(.borderlessButton)
            .help(L10n.overviewMoreActions)
            .accessibilityLabel(L10n.overviewMoreActions)

        case .starting:
            Button(L10n.overviewStarting) {}
                .disabled(true)

        case .idle, .stopped, .sleeping:
            Button(L10n.start, action: onStartBridge)
                .buttonStyle(.borderedProminent)

        case .crashed:
            Button(L10n.overviewRestart, action: onStartBridge)
                .buttonStyle(.borderedProminent)
        }
    }

    private var agentsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                SectionHeader(L10n.aiTools)
                Spacer()
                Button {
                    Task { await backendViewModel.refreshAgents() }
                } label: {
                    if backendViewModel.isLoading {
                        ProgressView()
                            .controlSize(.small)
                    }
                    Text(L10n.overviewDetectAgain)
                }
                .disabled(backendViewModel.isLoading)
            }

            if let error = backendViewModel.errorMessage {
                InlineFeedback(
                    style: backendViewModel.isShowingStaleResults ? .warning : .error,
                    message: error
                )
            }

            if overviewAgents.isEmpty {
                Text(L10n.noAiToolsDetected)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(overviewAgents.enumerated()), id: \.offset) { _, agent in
                    agentRow(agent)
                }
            }
        }
    }

    private func agentRow(_ agent: BackendAgentStatus) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 9) {
                StatusIndicator(
                    systemImage: agent.isAvailable
                        ? "checkmark.circle.fill"
                        : "exclamationmark.triangle.fill",
                    color: agent.isAvailable ? .green : .orange
                )
                Text(agent.displayName)
                    .font(.body.weight(.medium))
                Spacer()
                Text(agent.displayStatus)
                    .foregroundStyle(agent.isAvailable ? Color.secondary : Color.orange)
                Button(L10n.overviewTestConnection) {
                    Task { await backendViewModel.testAgent(id: agent.id) }
                }
                .disabled(agent.isRefreshing)
            }
            if !agent.isAvailable {
                Text(Self.nextStepGuidance(agent.reason, kind: agent.kind))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.leading, 27)
            }
        }
        .padding(.vertical, 5)
    }

    private var devicesSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            SectionHeader(L10n.devices)
            HStack(spacing: 10) {
                StatusIndicator(
                    systemImage: devices.isEmpty ? "iphone.slash" : "iphone",
                    color: devices.isEmpty ? .secondary : .green
                )
                Text(deviceSummary)
                if let lastSeenText, !devices.isEmpty {
                    Text(String(format: L10n.overviewRecentlySeen, lastSeenText))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if let onNavigateToDevices {
                    Button(L10n.overviewManageDevices, action: onNavigateToDevices)
                }
            }
        }
    }

    private var remoteSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            SectionHeader(L10n.remoteAccessTab)
            HStack(spacing: 10) {
                StatusIndicator(
                    systemImage: relayConfiguredIcon,
                    color: relayConfiguredColor
                )
                Text(relaySummary)
                Spacer()
                if let onNavigateToRemoteAccess {
                    Button(L10n.overviewViewSettings, action: onNavigateToRemoteAccess)
                }
            }
        }
    }

    private var runtimeAppearance: (title: String, icon: String, color: Color) {
        switch viewModel.status {
        case .ready: return (L10n.overviewRunning, "checkmark.circle.fill", .green)
        case .readyNoAgents: return (L10n.overviewRunningNoAgents, "exclamationmark.circle.fill", .orange)
        case .starting: return (L10n.overviewStarting, "hourglass", .orange)
        case .stopped: return (L10n.overviewStopped, "stop.circle", .secondary)
        case .crashed: return (L10n.overviewStartFailed, "xmark.circle.fill", .red)
        case .sleeping: return (L10n.overviewSleeping, "moon.fill", .blue)
        case .idle: return (L10n.overviewIdle, "circle", .secondary)
        }
    }

    private var runtimeDetail: String {
        var details: [String] = []
        if let port = viewModel.bridgePort {
            details.append(String(format: L10n.overviewPort, port))
        }
        if let uptime = BridgeStatusViewModel.formatUptime(viewModel.managementStatus?.uptime) {
            details.append(String(format: L10n.overviewUptime, uptime))
        }
        if let version = viewModel.managementStatus?.version, !version.isEmpty {
            details.append(String(format: L10n.overviewVersion, version))
        }
        return details.isEmpty ? L10n.overviewDetailsUnavailable : details.joined(separator: " · ")
    }

    private var deviceSummary: String {
        guard hasLoadedDevices else { return L10n.loadingDevices }
        return devices.isEmpty
            ? L10n.noTrustedDevices
            : String(format: L10n.tr("trusted_devices"), devices.count)
    }

    private var relaySummary: String {
        switch viewModel.relayConfigured {
        case true:
            return OfficialRelayConfiguration.isUsingCustomEndpoint
                ? L10n.overviewCustomRelayConfigured
                : L10n.overviewOfficialRelayConfigured
        case false:
            return L10n.overviewRelayNotConfigured
        case nil:
            return L10n.overviewRelayUnavailable
        }
    }

    private var relayConfiguredIcon: String {
        switch viewModel.relayConfigured {
        case true: return "lock.shield.fill"
        case false: return "lock.slash"
        case nil: return "questionmark.circle"
        }
    }

    private var relayConfiguredColor: Color {
        switch viewModel.relayConfigured {
        case true: return .green
        case false: return .secondary
        case nil: return .orange
        }
    }

    static func displayStatus(_ status: String) -> String {
        BackendStatusText.display(status)
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

enum BackendStatusText {
    static func display(_ status: String) -> String {
        switch status {
        case "available": return L10n.statusReady
        case "not_detected": return L10n.statusNotFound
        case "not_logged_in": return L10n.statusLoginRequired
        case "service_not_running": return L10n.statusNotRunning
        case "port_conflict": return L10n.statusPortConflict
        case "version_unsupported": return L10n.statusVersionIncompatible
        case "permission_denied": return L10n.statusPermissionDenied
        default: return status
        }
    }
}

struct SectionHeader: View {
    let title: String

    init(_ title: String) {
        self.title = title
    }

    var body: some View {
        Text(title)
            .font(.headline)
    }
}

struct StatusIndicator: View {
    let systemImage: String
    let color: Color
    var showsProgress = false

    var body: some View {
        Group {
            if showsProgress {
                ProgressView()
                    .controlSize(.small)
            } else {
                Image(systemName: systemImage)
                    .foregroundStyle(color)
            }
        }
        .frame(width: 18)
        .accessibilityHidden(true)
    }
}

struct InlineFeedback: View {
    enum Style {
        case success
        case warning
        case error

        var color: Color {
            switch self {
            case .success: return .green
            case .warning: return .orange
            case .error: return .red
            }
        }

        var icon: String {
            switch self {
            case .success: return "checkmark.circle"
            case .warning: return "exclamationmark.triangle"
            case .error: return "xmark.circle"
            }
        }
    }

    let style: Style
    let message: String

    var body: some View {
        Label(message, systemImage: style.icon)
            .font(.caption)
            .foregroundStyle(style.color)
    }
}

enum RelativeTimeFormatter {
    static func string(_ isoString: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let fallback = ISO8601DateFormatter()
        guard let date = formatter.date(from: isoString) ?? fallback.date(from: isoString) else {
            return isoString
        }
        let interval = max(0, Date().timeIntervalSince(date))
        if interval < 60 { return L10n.justNow }
        if interval < 3600 { return String(format: L10n.minAgo, Int(interval / 60)) }
        if interval < 86400 { return String(format: L10n.hrAgo, Int(interval / 3600)) }
        return String(format: L10n.daysAgo, Int(interval / 86400))
    }
}
