import SwiftUI

struct RemoteAccessView: View {
    @State private var remoteURL = ""
    @AppStorage("remoteBridgeURL") private var savedRemoteURL = ""
    @State private var customRelayEndpoint = ""
    @AppStorage("customRelayEndpoint") private var savedCustomRelayEndpoint = ""
    @AppStorage("pairingIncludeTailscale") private var includeTailscale = true
    @AppStorage("pairingIncludeRemote") private var includeRemote = true
    @State private var isEditingRelay = false
    @State private var relayConfigError: String?
    @State private var isProvisioningRelay = false
    @State private var showAdvancedConnections = false
    @State private var remoteStatus: RemoteStatus?
    @State private var isLoadingStatus = false
    @State private var statusError: String?

    var apiClient: ManagementAPIClient?

    private var localURL: String {
        remoteStatus?.localURL ?? remoteStatus?.listenStatus?.localURL ?? ""
    }

    private var tailscaleURL: String {
        remoteStatus?.tailscaleURL ?? ""
    }

    private var frpURL: String {
        remoteURL.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private var relayConfigured: Bool? {
        remoteStatus?.relay?.configured
    }

    private var normalizedCustomRelayEndpoint: String {
        BridgeRemoteURLFormatter.normalize(
            customRelayEndpoint.trimmingCharacters(in: .whitespacesAndNewlines)
        )
    }

    var body: some View {
        PageContainer {
            VStack(alignment: .leading, spacing: 24) {
                PageHeader(L10n.remoteTitle, subtitle: L10n.remoteSubtitle) {
                    Button {
                        Task { await loadRemoteStatus() }
                    } label: {
                        if isLoadingStatus {
                            ProgressView()
                                .controlSize(.small)
                        }
                        Text(L10n.overviewDetectAgain)
                    }
                    .disabled(isLoadingStatus)
                }

                connectionPaths
                Divider()
                relaySection
                Divider()
                strategySection
                advancedSection

                if let statusError {
                    InlineFeedback(style: .error, message: statusError)
                    Button(L10n.retry) {
                        Task { await loadRemoteStatus() }
                    }
                }
            }
        }
        .task(id: apiClient?.baseURL.absoluteString) {
            customRelayEndpoint = savedCustomRelayEndpoint
            remoteURL = savedRemoteURL
            await loadRemoteStatus()
        }
        .onChange(of: includeTailscale) { _, _ in notifyPairingConfigChanged() }
        .onChange(of: includeRemote) { _, _ in notifyPairingConfigChanged() }
    }

    private var connectionPaths: some View {
        VStack(alignment: .leading, spacing: 12) {
            SectionHeader(L10n.remoteConnectionPaths)
            staticRow(
                icon: "wifi",
                color: localURL.isEmpty ? .secondary : .blue,
                title: L10n.remoteLAN,
                status: localURL.isEmpty ? L10n.remoteUnavailable : L10n.remoteAutomatic,
                detail: localURL.isEmpty ? nil : localURL
            )
            staticRow(
                icon: "lock.shield",
                color: relayConfigured == true ? .green : .secondary,
                title: OfficialRelayConfiguration.isUsingCustomEndpoint
                    ? L10n.remoteCustomRelay
                    : L10n.remoteOfficialRelay,
                status: relayStatusText,
                detail: remoteStatus?.relay?.endpoint
            )
        }
    }

    private var relaySection: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                SectionHeader(L10n.remoteRelay)
                Spacer()
                if !isEditingRelay {
                    Button(L10n.remoteCustomize) {
                        customRelayEndpoint = savedCustomRelayEndpoint
                        isEditingRelay = true
                    }
                }
            }

            Text(
                OfficialRelayConfiguration.isUsingCustomEndpoint
                    ? String(format: L10n.remoteCurrentCustomRelay, OfficialRelayConfiguration.endpoint)
                    : String(format: L10n.remoteDefaultRelay, OfficialRelayConfiguration.bundledEndpoint)
            )
            .font(.system(.caption, design: .monospaced))
            .foregroundStyle(.secondary)
            .textSelection(.enabled)

            if isEditingRelay {
                VStack(alignment: .leading, spacing: 8) {
                    TextField(OfficialRelayConfiguration.bundledEndpoint, text: $customRelayEndpoint)
                        .textFieldStyle(.roundedBorder)
                        .font(.system(.caption, design: .monospaced))

                    HStack {
                        Button(L10n.save) {
                            saveCustomRelayEndpoint()
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(
                            isProvisioningRelay ||
                            normalizedCustomRelayEndpoint == savedCustomRelayEndpoint ||
                            (!normalizedCustomRelayEndpoint.isEmpty &&
                             !isValidRelayURL(normalizedCustomRelayEndpoint))
                        )

                        Button(L10n.remoteRestoreDefault) {
                            restoreDefaultRelayEndpoint()
                        }
                        .disabled(isProvisioningRelay)

                        Button(L10n.cancel) {
                            customRelayEndpoint = savedCustomRelayEndpoint
                            relayConfigError = nil
                            isEditingRelay = false
                        }
                    }

                    if !normalizedCustomRelayEndpoint.isEmpty &&
                        !isValidRelayURL(normalizedCustomRelayEndpoint) {
                        InlineFeedback(style: .warning, message: L10n.remoteRelayValidation)
                    }
                }
            }

            if isProvisioningRelay {
                HStack(spacing: 8) {
                    ProgressView()
                        .controlSize(.small)
                    Text(L10n.remoteValidatingRelay)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            if let relayConfigError {
                InlineFeedback(style: .error, message: relayConfigError)
            }
        }
    }

    private var strategySection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionHeader(L10n.remoteConnectionStrategy)
            Text(L10n.remoteStrategySummary)
                .foregroundStyle(.secondary)
        }
    }

    private var advancedSection: some View {
        DisclosureGroup(L10n.remoteAdvancedConnections, isExpanded: $showAdvancedConnections) {
            VStack(alignment: .leading, spacing: 16) {
                Toggle(isOn: $includeTailscale) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(L10n.remoteTailscale)
                        Text(tailscaleURL.isEmpty ? L10n.remoteTailscaleUnavailable : tailscaleURL)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                }

                VStack(alignment: .leading, spacing: 8) {
                    Toggle(L10n.remoteVPS, isOn: $includeRemote)
                    HStack {
                        TextField(L10n.remoteVPSPlaceholder, text: $remoteURL)
                            .textFieldStyle(.roundedBorder)
                            .font(.system(.caption, design: .monospaced))
                        Button(L10n.save) {
                            saveRemoteURL()
                        }
                        .disabled(remoteURL == savedRemoteURL)
                    }
                    if !frpURL.isEmpty && !isValidManualRemoteURL(frpURL) {
                        InlineFeedback(style: .warning, message: L10n.remoteVPSValidation)
                    }
                }
            }
            .padding(.top, 10)
        }
    }

    private func staticRow(
        icon: String,
        color: Color,
        title: String,
        status: String,
        detail: String?
    ) -> some View {
        HStack(alignment: .top, spacing: 10) {
            StatusIndicator(systemImage: icon, color: color)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(title)
                        .font(.body.weight(.medium))
                    Text(status)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if let detail, !detail.isEmpty {
                    Text(detail)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }
        }
    }

    private var relayStatusText: String {
        if isProvisioningRelay { return L10n.remoteValidating }
        switch relayConfigured {
        case true: return L10n.configured
        case false: return L10n.notConfigured
        case nil: return L10n.overviewRelayUnavailable
        }
    }

    private func saveRemoteURL() {
        let normalized = BridgeRemoteURLFormatter.normalize(frpURL)
        remoteURL = normalized
        guard normalized.isEmpty || isValidManualRemoteURL(normalized) else {
            includeRemote = false
            return
        }
        savedRemoteURL = normalized
        includeRemote = !normalized.isEmpty
        notifyPairingConfigChanged()
    }

    private func notifyPairingConfigChanged() {
        NotificationCenter.default.post(name: .remoteURLDidChange, object: nil)
    }

    private func saveCustomRelayEndpoint() {
        let normalized = normalizedCustomRelayEndpoint
        customRelayEndpoint = normalized
        guard normalized.isEmpty || isValidRelayURL(normalized) else { return }
        savedCustomRelayEndpoint = normalized
        relayConfigError = nil
        Task { await applySelectedRelayEndpoint() }
    }

    private func restoreDefaultRelayEndpoint() {
        customRelayEndpoint = ""
        savedCustomRelayEndpoint = ""
        relayConfigError = nil
        Task { await applySelectedRelayEndpoint() }
    }

    private func applySelectedRelayEndpoint() async {
        guard !isProvisioningRelay else { return }
        isProvisioningRelay = true
        defer { isProvisioningRelay = false }
        do {
            _ = try await OfficialRelayProvisioner.shared.ensureRoute()
            notifyPairingConfigChanged()
            await loadRemoteStatus()
            isEditingRelay = false
        } catch {
            relayConfigError = String(format: L10n.remoteRelayFailed, error.localizedDescription)
        }
    }

    private func isValidManualRemoteURL(_ value: String) -> Bool {
        guard let url = URL(string: value),
              let scheme = url.scheme?.lowercased(),
              url.host != nil else {
            return false
        }
        return scheme == "ws" || scheme == "wss" || scheme == "https"
    }

    private func isValidRelayURL(_ value: String) -> Bool {
        guard let url = URL(string: value),
              url.scheme?.lowercased() == "wss",
              url.host != nil else {
            return false
        }
        return true
    }

    private func loadRemoteStatus() async {
        guard let client = apiClient else {
            statusError = L10n.errorCannotConnect
            return
        }
        guard !isLoadingStatus else { return }
        isLoadingStatus = true
        defer { isLoadingStatus = false }
        do {
            remoteStatus = try await client.getRemoteStatus()
            statusError = nil
            remoteURL = remoteStatus?.remoteURL?.isEmpty == false
                ? remoteStatus?.remoteURL ?? savedRemoteURL
                : savedRemoteURL
        } catch {
            statusError = error.localizedDescription
        }
    }
}

enum BridgeRemoteURLFormatter {
    static func normalize(_ raw: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.lowercased().hasPrefix("https://") {
            return "wss" + String(trimmed.dropFirst(5))
        }
        return trimmed
    }
}
