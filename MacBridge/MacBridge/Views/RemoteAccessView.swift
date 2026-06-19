import SwiftUI

struct RemoteAccessView: View {
    @State private var remoteURL = ""
    @AppStorage("remoteBridgeURL") private var savedRemoteURL = ""
    @State private var customRelayEndpoint = ""
    @AppStorage("customRelayEndpoint") private var savedCustomRelayEndpoint = ""
    @AppStorage("pairingIncludeTailscale") private var includeTailscale = true
    @AppStorage("pairingIncludeRemote") private var includeRemote = true
    @AppStorage("relayEnabled") private var relayEnabled = true
    @State private var showDisableConfirmation = false
    
    @State private var selectedMethod: ConnectionMethod = .lan
    @State private var showDetailInNarrow = true
    @State private var sidebarWidth: CGFloat = 240
    @State private var relayMode: RelayMode = .official
    @State private var relaySaveState: RelaySaveState = .idle
    @State private var customAddressSaveState: CustomAddressSaveState = .idle
    
    @State private var remoteStatus: RemoteStatus?
    @State private var isLoadingStatus = false
    @State private var statusError: String?
    @State private var lastUpdatedTime: String?
    
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
    
    private var isRelayModified: Bool {
        switch relayMode {
        case .official:
            return !savedCustomRelayEndpoint.isEmpty
        case .custom:
            let normalized = normalizedCustomRelayEndpoint
            return normalized != savedCustomRelayEndpoint
        }
    }
    
    private var canSaveRelay: Bool {
        if isSavingRelay { return false }
        switch relayMode {
        case .official:
            return !savedCustomRelayEndpoint.isEmpty
        case .custom:
            let normalized = normalizedCustomRelayEndpoint
            return !normalized.isEmpty && normalized != savedCustomRelayEndpoint && isValidRelayURL(normalized)
        }
    }
    
    private var isSavingRelay: Bool {
        switch relaySaveState {
        case .validatingFormat, .provisioning, .applyingConfig, .restartingBridge, .enabling, .disabling:
            return true
        default:
            return false
        }
    }
    
    private var customAddressSaveStateIsProgress: Bool {
        switch customAddressSaveState {
        case .validatingFormat, .saving, .restartingBridge:
            return true
        default:
            return false
        }
    }

    private var showPublicWSWarning: Bool {
        isPublicWSAddress(remoteURL) || (remoteStatus?.remoteAnalysis?.isPublicWS == true && remoteURL == savedRemoteURL)
    }
    
    private var leftColumnSubtitles: [ConnectionMethod: String] {
        var subs: [ConnectionMethod: String] = [:]
        if includeTailscale && !tailscaleURL.isEmpty {
            subs[.tailscale] = L10n.current == .zhHans ? "新配对中包含" : "Included in pairing"
        }
        if includeRemote && !savedRemoteURL.isEmpty && isValidManualRemoteURL(savedRemoteURL) {
            subs[.customAddress] = L10n.current == .zhHans ? "新配对中包含" : "Included in pairing"
        }
        return subs
    }
    
    private var relayStatusText: String {
        switch relaySaveState {
        case .enabling:
            return L10n.remoteRelayEnabling
        case .disabling:
            return L10n.remoteRelayDisabling
        default:
            break
        }
        if !relayEnabled {
            return L10n.remoteRelayDisabled
        }
        if isSavingRelay { return L10n.remoteValidating }
        switch relayConfigured {
        case true: return L10n.configured
        case false: return L10n.notConfigured
        case nil: return L10n.overviewRelayUnavailable
        }
    }

    var body: some View {
        PageContainer(scrolls: false) {
            VStack(alignment: .leading, spacing: 0) {
                PageHeader(L10n.remoteTitle, subtitle: L10n.remoteSubtitle) {
                    Button {
                        Task { await loadRemoteStatus() }
                    } label: {
                        if isLoadingStatus {
                            ProgressView()
                                .controlSize(.small)
                        }
                        Text(L10n.current == .zhHans ? "刷新状态" : "Refresh Status")
                    }
                    .disabled(isLoadingStatus)
                }
                .padding(.bottom, 20)

                GeometryReader { geometry in
                    let isNarrow = geometry.size.width < 680
                    
                    if isNarrow {
                        narrowLayoutView
                    } else {
                        wideLayoutView
                    }
                }
            }
        }
        .task(id: apiClient?.baseURL.absoluteString) {
            customRelayEndpoint = savedCustomRelayEndpoint
            remoteURL = savedRemoteURL
            relayMode = savedCustomRelayEndpoint.isEmpty ? .official : .custom
            await loadRemoteStatus()
        }
        .onChange(of: includeTailscale) { _, _ in notifyPairingConfigChanged() }
        .onChange(of: includeRemote) { _, _ in notifyPairingConfigChanged() }
        .alert(L10n.remoteRelayConfirmTitle, isPresented: $showDisableConfirmation) {
            Button(L10n.current == .zhHans ? "确认关闭" : "Confirm Disable", role: .destructive) {
                performRelayToggle(false)
            }
            Button(L10n.cancel, role: .cancel) {
                relayEnabled = true
            }
        } message: {
            Text(L10n.remoteRelayConfirmMessage)
        }
    }
    
    // MARK: - Layouts
    
    private var wideLayoutView: some View {
        HStack(spacing: 0) {
            leftColumnView
                .frame(width: sidebarWidth)
            
            Rectangle()
                .fill(Color(NSColor.separatorColor))
                .frame(width: 1)
                .frame(maxHeight: .infinity)
                .contentShape(Rectangle())
                .onHover { inside in
                    if inside {
                        NSCursor.resizeLeftRight.push()
                    } else {
                        NSCursor.pop()
                    }
                }
                .gesture(
                    DragGesture()
                        .onChanged { value in
                            let newWidth = sidebarWidth + value.translation.width
                            sidebarWidth = min(max(newWidth, 200), 260)
                        }
                )
            
            ScrollView {
                rightColumnView
                    .padding(.leading, 24)
                    .padding(.trailing, 8)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
    
    private var narrowLayoutView: some View {
        VStack(alignment: .leading, spacing: 0) {
            if showDetailInNarrow {
                HStack {
                    Button(action: {
                        showDetailInNarrow = false
                    }) {
                        Label(L10n.back, systemImage: "chevron.left")
                    }
                    .buttonStyle(.plain)
                    .font(.body.weight(.medium))
                    .foregroundColor(.accentColor)
                    .padding(.vertical, 8)
                    
                    Spacer()
                }
                .padding(.bottom, 12)
                
                ScrollView {
                    rightColumnView
                }
            } else {
                leftColumnView
            }
        }
    }
    
    // MARK: - Left Column
    
    private var leftColumnView: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                // Group 1: 自动连接
                VStack(alignment: .leading, spacing: 6) {
                    Text(L10n.current == .zhHans ? "自动连接 (默认使用)" : "Auto Connections")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.secondary)
                        .padding(.horizontal, 4)
                    
                    NavigationRow(
                        method: .lan,
                        icon: "wifi",
                        title: L10n.remoteLAN,
                        statusText: localURL.isEmpty ? L10n.remoteUnavailable : L10n.remoteAutomatic,
                        statusColor: localURL.isEmpty ? .secondary : .green,
                        subtitle: nil,
                        isSelected: selectedMethod == .lan,
                        isLoading: isLoadingStatus,
                        action: { selectMethod(.lan) }
                    )
                    
                    NavigationRow(
                        method: .relay,
                        icon: "lock.shield",
                        title: L10n.remoteRelay,
                        statusText: relayStatusText,
                        statusColor: .secondary,
                        subtitle: nil,
                        isSelected: selectedMethod == .relay,
                        isLoading: isLoadingStatus,
                        action: { selectMethod(.relay) }
                    )
                }
                
                // Group 2: 高级连接
                VStack(alignment: .leading, spacing: 6) {
                    Text(L10n.current == .zhHans ? "高级连接" : "Advanced Connections")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.secondary)
                        .padding(.horizontal, 4)
                    
                    NavigationRow(
                        method: .tailscale,
                        icon: "network",
                        title: L10n.remoteTailscale,
                        statusText: tailscaleURL.isEmpty ? L10n.remoteTailscaleUnavailable : tailscaleURL,
                        statusColor: tailscaleURL.isEmpty ? .secondary : .green,
                        subtitle: leftColumnSubtitles[.tailscale],
                        isSelected: selectedMethod == .tailscale,
                        isLoading: isLoadingStatus,
                        action: { selectMethod(.tailscale) }
                    )
                    
                    NavigationRow(
                        method: .customAddress,
                        icon: "server.rack",
                        title: L10n.remoteVPS,
                        statusText: savedRemoteURL.isEmpty ? L10n.notConfigured : L10n.configured,
                        statusColor: savedRemoteURL.isEmpty ? .secondary : .green,
                        subtitle: leftColumnSubtitles[.customAddress],
                        isSelected: selectedMethod == .customAddress,
                        isLoading: isLoadingStatus,
                        action: { selectMethod(.customAddress) }
                    )
                }
                
                if let statusError {
                    Text(statusError)
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .padding(.horizontal, 4)
                        .padding(.top, 8)
                }
            }
            .padding(.trailing, 8)
        }
    }
    
    // MARK: - Right Column Details
    
    private var rightColumnView: some View {
        VStack(alignment: .leading, spacing: 0) {
            switch selectedMethod {
            case .lan:
                lanDetailView
            case .relay:
                relayDetailView
            case .tailscale:
                tailscaleDetailView
            case .customAddress:
                customAddressDetailView
            }
        }
    }
    
    private var lanDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(L10n.remoteLAN)
                .font(.title2)
                .bold()
            
            Text(L10n.current == .zhHans
                ? "当 iPhone 与 Mac 处于同一 Wi-Fi 或局域网下时，使用此方式进行高速直连。数据不离开局域网。"
                : "When iPhone and Mac are on the same Wi-Fi or local network, this method provides high-speed direct connection. Data does not leave the local network.")
                .foregroundColor(.secondary)
            
            VStack(alignment: .leading, spacing: 8) {
                Text(L10n.securityLevel)
                    .font(.headline)
                Text("• " + L10n.secLan)
                    .foregroundColor(.secondary)
                Text(L10n.remoteLANHint)
                    .font(.caption)
                    .foregroundColor(.orange)
            }
            
            Divider()
            
            VStack(alignment: .leading, spacing: 12) {
                Text(L10n.diagnosisInfo)
                    .font(.headline)
                
                HStack {
                    Text(L10n.current == .zhHans ? "本地监听状态：" : "Local Listening:")
                    if localURL.isEmpty {
                        Text("🔴 " + (L10n.current == .zhHans ? "未在监听" : "Not Listening"))
                    } else {
                        Text("🟢 " + (L10n.current == .zhHans ? "正常监听中" : "Listening"))
                    }
                }
                
                if !localURL.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        Text(L10n.localURLLabel)
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                        
                        HStack {
                            Text(localURL)
                                .font(.system(.body, design: .monospaced))
                                .textSelection(.enabled)
                                .padding(8)
                                .background(Color(NSColor.controlBackgroundColor))
                                .cornerRadius(6)
                            
                            Button(action: {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(localURL, forType: .string)
                            }) {
                                Label(L10n.current == .zhHans ? "复制" : "Copy", systemImage: "doc.on.doc")
                            }
                        }
                    }
                }
            }
        }
    }
    
    private var relayDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(L10n.remoteRelay)
                .font(.title2)
                .bold()
            
            Text(L10n.current == .zhHans
                ? "跨公网远程连接时的安全通道。数据包经由端到端加密传输，中继服务无法读取您的代码和消息内容。"
                : "A secure channel for remote connections across public networks. Data packets are end-to-end encrypted; the relay service cannot read your code or messages.")
                .foregroundColor(.secondary)
            
            VStack(alignment: .leading, spacing: 8) {
                Toggle(L10n.remoteRelayEnabled, isOn: Binding(
                    get: { relayEnabled },
                    set: { newValue in
                        toggleRelayEnabled(to: newValue)
                    }
                ))
                .disabled(isSavingRelay)
                
                Text(L10n.remoteRelaySwitchHint)
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            .padding(.vertical, 8)
            
            switch relaySaveState {
            case .enabling:
                feedbackRow(text: L10n.remoteRelayEnabling, isProgress: true)
            case .disabling:
                feedbackRow(text: L10n.remoteRelayDisabling, isProgress: true)
            default:
                EmptyView()
            }
            
            VStack(alignment: .leading, spacing: 8) {
                Text(L10n.securityLevel)
                    .font(.headline)
                if !relayEnabled {
                    Text("• " + L10n.remoteRelayDisabled)
                        .foregroundColor(.secondary)
                } else {
                    Text("• " + L10n.secEncrypted)
                        .foregroundColor(.secondary)
                }
            }
            
            Divider()
            
            VStack(alignment: .leading, spacing: 12) {
                Text(L10n.current == .zhHans ? "配置状态" : "Configuration Status")
                    .font(.headline)
                
                HStack {
                    Text(L10n.status + "：")
                    if !relayEnabled {
                        Text("⚪️ " + L10n.remoteRelayDisabled)
                    } else if relayConfigured == true {
                        Text("🟢 " + L10n.configured)
                    } else {
                        Text("⚪️ " + L10n.notConfigured)
                    }
                }
                
                if relayEnabled && relayConfigured == true, let endpoint = remoteStatus?.relay?.endpoint {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(L10n.current == .zhHans ? "中继地址：" : "Relay Endpoint:")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                        Text(endpoint)
                            .font(.system(.body, design: .monospaced))
                            .textSelection(.enabled)
                    }
                }
            }
            
            if relayEnabled {
                Divider()
                
                VStack(alignment: .leading, spacing: 12) {
                    Text(L10n.current == .zhHans ? "设置自定义中继" : "Custom Relay Settings")
                        .font(.headline)
                    
                    Picker("", selection: $relayMode) {
                        Text(L10n.current == .zhHans ? "官方默认" : "Official Default").tag(RelayMode.official)
                        Text(L10n.current == .zhHans ? "自定义中继" : "Custom Relay").tag(RelayMode.custom)
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    
                    if relayMode == .custom {
                        VStack(alignment: .leading, spacing: 6) {
                            TextField(OfficialRelayConfiguration.bundledEndpoint, text: $customRelayEndpoint)
                                .textFieldStyle(.roundedBorder)
                                .font(.system(.body, design: .monospaced))
                            
                            if !normalizedCustomRelayEndpoint.isEmpty && !isValidRelayURL(normalizedCustomRelayEndpoint) {
                                InlineFeedback(style: .warning, message: L10n.remoteRelayValidation)
                            }
                        }
                    } else {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(L10n.current == .zhHans ? "官方中继地址：" : "Official Relay Endpoint:")
                                .font(.subheadline)
                                .foregroundColor(.secondary)
                            Text(OfficialRelayConfiguration.bundledEndpoint)
                                .font(.system(.body, design: .monospaced))
                                .foregroundColor(.secondary)
                        }
                        .padding(.vertical, 4)
                    }
                    
                    HStack {
                        Button(action: saveRelayConfiguration) {
                            Text(L10n.save)
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(!canSaveRelay)
                        
                        if isRelayModified {
                            Button(action: {
                                customRelayEndpoint = savedCustomRelayEndpoint
                                relayMode = savedCustomRelayEndpoint.isEmpty ? .official : .custom
                                relaySaveState = .idle
                            }) {
                                Text(L10n.cancel)
                            }
                        }
                    }
                    .padding(.top, 4)
                    
                    switch relaySaveState {
                    case .idle, .enabling, .disabling:
                        EmptyView()
                    case .validatingFormat:
                        feedbackRow(text: L10n.current == .zhHans ? "正在校验格式..." : "Validating format...", isProgress: true)
                    case .provisioning:
                        feedbackRow(text: L10n.remoteValidatingRelay, isProgress: true)
                    case .applyingConfig:
                        feedbackRow(text: L10n.current == .zhHans ? "正在应用配置..." : "Applying configuration...", isProgress: true)
                    case .restartingBridge:
                        feedbackRow(text: L10n.current == .zhHans ? "Bridge 正在重启..." : "Bridge restarting...", isProgress: true)
                    case .applied:
                        InlineFeedback(style: .success, message: L10n.current == .zhHans ? "配置已应用" : "Configuration applied")
                    case .failed(let err):
                        InlineFeedback(style: .error, message: err)
                    }
                }
            }
        }
    }
    
    private var tailscaleDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(L10n.remoteTailscale)
                .font(.title2)
                .bold()
            
            Text(L10n.current == .zhHans
                ? "利用 Tailscale 创建的私有虚拟网进行连接。适用于已安装 Tailscale 客户端的设备之间的直接互通。"
                : "Connect using a private virtual network created by Tailscale. Ideal for direct communication between devices with the Tailscale client installed.")
                .foregroundColor(.secondary)
            
            VStack(alignment: .leading, spacing: 8) {
                Text(L10n.securityLevel)
                    .font(.headline)
                Text("• " + L10n.secTailscaleTunnel)
                    .foregroundColor(.secondary)
            }
            
            Divider()
            
            VStack(alignment: .leading, spacing: 12) {
                Text(L10n.current == .zhHans ? "配对设置" : "Pairing Settings")
                    .font(.headline)
                
                Toggle(L10n.remotePublishInPairing, isOn: $includeTailscale)
                    .disabled(tailscaleURL.isEmpty)
                
                if tailscaleURL.isEmpty {
                    HStack(alignment: .top, spacing: 6) {
                        Text("⚠️")
                        Text(L10n.current == .zhHans
                            ? "当前无可用的 Tailscale 虚拟网 IP，新生成的配对二维码中将无法包含此连接方式。"
                            : "No available Tailscale IP detected. New pairing QR codes cannot include this connection method.")
                            .font(.caption)
                            .foregroundColor(.red)
                    }
                    .padding(.top, 4)
                } else {
                    Text(L10n.current == .zhHans
                        ? "启用后，未来新生成的配对二维码将包含此 Tailscale 地址。"
                        : "When enabled, future pairing QR codes will include this Tailscale address.")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
            }
            
            Divider()
            
            VStack(alignment: .leading, spacing: 12) {
                Text(L10n.current == .zhHans ? "诊断与检测" : "Diagnostics & Detection")
                    .font(.headline)
                
                HStack {
                    Text(L10n.status + "：")
                    if tailscaleURL.isEmpty {
                        Text("⚪️ " + L10n.remoteTailscaleUnavailable)
                    } else {
                        Text("🟢 " + L10n.remoteTailscaleIPDetected)
                    }
                }
                
                if !tailscaleURL.isEmpty {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(L10n.current == .zhHans ? "检测到的 Tailscale 地址：" : "Detected Tailscale Address:")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                        Text(tailscaleURL)
                            .font(.system(.body, design: .monospaced))
                            .textSelection(.enabled)
                    }
                }
            }
        }
    }
    
    private var customAddressDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(L10n.remoteVPS)
                .font(.title2)
                .bold()
            
            Text(L10n.current == .zhHans
                ? "允许您通过反向代理、内网穿透（如 FRP）或自建 VPS 暴露的公网端点进行连接。"
                : "Allows you to connect via public endpoints exposed by reverse proxies, intranets (such as FRP), or self-hosted VPS.")
                .foregroundColor(.secondary)
            
            VStack(alignment: .leading, spacing: 8) {
                Text(L10n.securityLevel)
                    .font(.headline)
                if !savedRemoteURL.isEmpty, showPublicWSWarning {
                    Text("• " + L10n.secInsecure)
                        .foregroundColor(.red)
                } else if !savedRemoteURL.isEmpty {
                    Text("• " + L10n.secEncrypted)
                        .foregroundColor(.green)
                } else {
                    Text("• " + L10n.secUnknown)
                        .foregroundColor(.secondary)
                }
            }
            
            Divider()
            
            VStack(alignment: .leading, spacing: 12) {
                Text(L10n.current == .zhHans ? "配置接入地址" : "Configure Endpoint Address")
                    .font(.headline)
                
                HStack {
                    TextField(L10n.remoteVPSPlaceholder, text: $remoteURL)
                        .textFieldStyle(.roundedBorder)
                        .font(.system(.body, design: .monospaced))
                    
                    Button(action: saveCustomAddressConfiguration) {
                        Text(L10n.save)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(remoteURL == savedRemoteURL || customAddressSaveStateIsProgress)
                    
                    if remoteURL != savedRemoteURL {
                        Button(action: {
                            remoteURL = savedRemoteURL
                        }) {
                            Text(L10n.cancel)
                        }
                    }
                }
                
                if !frpURL.isEmpty && !isValidManualRemoteURL(frpURL) {
                    InlineFeedback(style: .warning, message: L10n.remoteVPSValidation)
                }
                
                if showPublicWSWarning {
                    InlineFeedback(style: .warning, message: L10n.remoteUnencryptedWarning)
                }
                
                switch customAddressSaveState {
                case .idle:
                    EmptyView()
                case .validatingFormat:
                    feedbackRow(text: L10n.current == .zhHans ? "正在校验格式..." : "Validating format...", isProgress: true)
                case .saving:
                    feedbackRow(text: L10n.current == .zhHans ? "正在保存..." : "Saving...", isProgress: true)
                case .restartingBridge:
                    feedbackRow(text: L10n.current == .zhHans ? "Bridge 正在重启..." : "Bridge restarting...", isProgress: true)
                case .applied:
                    InlineFeedback(style: .success, message: L10n.current == .zhHans ? "配置已应用" : "Configuration applied")
                case .failed(let err):
                    InlineFeedback(style: .error, message: err)
                }
            }
            
            Divider()
            
            VStack(alignment: .leading, spacing: 12) {
                Text(L10n.current == .zhHans ? "配对设置" : "Pairing Settings")
                    .font(.headline)
                
                let isCustomAddressConfiguredAndValid = !savedRemoteURL.isEmpty && isValidManualRemoteURL(savedRemoteURL)
                
                Toggle(L10n.remotePublishInPairing, isOn: $includeRemote)
                    .disabled(!isCustomAddressConfiguredAndValid)
                
                if !isCustomAddressConfiguredAndValid {
                    HStack(alignment: .top, spacing: 6) {
                        Text("⚠️")
                        Text(L10n.current == .zhHans
                            ? "当前无有效的自定义地址，新生成的配对二维码中将无法包含此连接方式。"
                            : "No valid custom address configured. New pairing QR codes cannot include this connection method.")
                            .font(.caption)
                            .foregroundColor(.red)
                    }
                    .padding(.top, 4)
                } else {
                    Text(L10n.current == .zhHans
                        ? "启用后，未来新生成的配对二维码将包含此自定义地址。"
                        : "When enabled, future pairing QR codes will include this custom address.")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
            }
        }
    }
    
    // MARK: - Actions / Helpers
    
    private func selectMethod(_ method: ConnectionMethod) {
        selectedMethod = method
        showDetailInNarrow = true
    }
    
    private func feedbackRow(text: String, isProgress: Bool) -> some View {
        HStack(spacing: 8) {
            if isProgress {
                ProgressView()
                    .controlSize(.small)
            }
            Text(text)
                .font(.caption)
                .foregroundColor(.secondary)
        }
        .padding(.top, 4)
    }

    private func notifyPairingConfigChanged() {
        NotificationCenter.default.post(name: .remoteURLDidChange, object: nil)
    }

    private func saveRelayConfiguration() {
        let targetEndpoint: String
        switch relayMode {
        case .official:
            targetEndpoint = ""
        case .custom:
            targetEndpoint = normalizedCustomRelayEndpoint
        }
        
        relaySaveState = .validatingFormat
        
        if !targetEndpoint.isEmpty && !isValidRelayURL(targetEndpoint) {
            relaySaveState = .failed(L10n.remoteRelayValidation)
            return
        }
        
        let endpointChanged = (targetEndpoint != savedCustomRelayEndpoint)
        
        relaySaveState = .provisioning
        
        Task {
            do {
                try? await Task.sleep(nanoseconds: 300_000_000)
                
                savedCustomRelayEndpoint = targetEndpoint
                
                _ = try await OfficialRelayProvisioner.shared.ensureRoute()
                
                relaySaveState = .applyingConfig
                try? await Task.sleep(nanoseconds: 300_000_000)
                
                notifyPairingConfigChanged()
                
                if endpointChanged {
                    relaySaveState = .restartingBridge
                    try? await Task.sleep(nanoseconds: 1_800_000_000)
                }
                
                await loadRemoteStatus()
                relaySaveState = .applied
                
                try? await Task.sleep(nanoseconds: 1_500_000_000)
                if case .applied = relaySaveState {
                    relaySaveState = .idle
                }
            } catch {
                relaySaveState = .failed(String(format: L10n.remoteRelayFailed, error.localizedDescription))
            }
        }
    }
    
    private func saveCustomAddressConfiguration() {
        let normalized = BridgeRemoteURLFormatter.normalize(frpURL)
        remoteURL = normalized
        
        customAddressSaveState = .validatingFormat
        
        if !normalized.isEmpty && !isValidManualRemoteURL(normalized) {
            customAddressSaveState = .failed(L10n.remoteVPSValidation)
            return
        }
        
        customAddressSaveState = .saving
        
        let urlChanged = (normalized != savedRemoteURL)
        
        Task {
            try? await Task.sleep(nanoseconds: 300_000_000)
            
            savedRemoteURL = normalized
            
            notifyPairingConfigChanged()
            
            if urlChanged {
                customAddressSaveState = .restartingBridge
                try? await Task.sleep(nanoseconds: 1_800_000_000)
            }
            
            await loadRemoteStatus()
            customAddressSaveState = .applied
            
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            if case .applied = customAddressSaveState {
                customAddressSaveState = .idle
            }
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

    private func toggleRelayEnabled(to newValue: Bool) {
        if !newValue {
            let noOtherRemote = tailscaleURL.isEmpty && savedRemoteURL.isEmpty
            if noOtherRemote {
                showDisableConfirmation = true
                return
            }
        }
        performRelayToggle(newValue)
    }
    
    private func performRelayToggle(_ enabled: Bool) {
        relaySaveState = enabled ? .enabling : .disabling
        Task {
            UserDefaults.standard.set(enabled, forKey: "relayEnabled")
            notifyPairingConfigChanged()
            try? await Task.sleep(nanoseconds: 1_800_000_000)
            await loadRemoteStatus()
            relaySaveState = .idle
        }
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
            
            let formatter = DateFormatter()
            formatter.dateFormat = "HH:mm"
            lastUpdatedTime = formatter.string(from: Date())
        } catch {
            let timeStr = lastUpdatedTime ?? {
                let formatter = DateFormatter()
                formatter.dateFormat = "HH:mm"
                return formatter.string(from: Date())
            }()
            statusError = String(format: L10n.remoteRefreshFailed, timeStr)
        }
    }
    
    private func isPublicWSAddress(_ urlString: String) -> Bool {
        let lower = urlString.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard lower.hasPrefix("ws://") else { return false }
        
        guard let url = URL(string: lower), let host = url.host else {
            return false
        }
        
        if host == "localhost" || host == "127.0.0.1" || host.hasSuffix(".local") {
            return false
        }
        
        if host.hasPrefix("10.") || host.hasPrefix("192.168.") {
            return false
        }
        if host.hasPrefix("172.") {
            let parts = host.split(separator: ".")
            if parts.count >= 2, let secondPart = Int(parts[1]), secondPart >= 16 && secondPart <= 31 {
                return false
            }
        }
        
        return true
    }
}

// MARK: - Nested Types

enum ConnectionMethod: String, CaseIterable, Identifiable {
    case lan
    case relay
    case tailscale
    case customAddress

    var id: String { rawValue }
}

enum RelayMode: String {
    case official
    case custom
}

enum RelaySaveState {
    case idle
    case validatingFormat
    case provisioning
    case applyingConfig
    case restartingBridge
    case applied
    case failed(String)
    case enabling
    case disabling
}

enum CustomAddressSaveState {
    case idle
    case validatingFormat
    case saving
    case restartingBridge
    case applied
    case failed(String)
}

// MARK: - Navigation Row Component

struct NavigationRow: View {
    let method: ConnectionMethod
    let icon: String
    let title: String
    let statusText: String
    let statusColor: Color
    let subtitle: String?
    let isSelected: Bool
    let isLoading: Bool
    let action: () -> Void
    
    @State private var isHovering = false
    
    var body: some View {
        Button(action: action) {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: icon)
                    .font(.system(size: 14, weight: .medium))
                    .foregroundColor(isSelected ? .white : .accentColor)
                    .frame(width: 18, height: 18)
                    .padding(4)
                    .background(
                        RoundedRectangle(cornerRadius: 6)
                            .fill(isSelected ? Color.white.opacity(0.15) : Color.accentColor.opacity(0.1))
                    )
                
                VStack(alignment: .leading, spacing: 2) {
                    HStack(alignment: .firstTextBaseline) {
                        Text(title)
                            .font(.body)
                            .fontWeight(.medium)
                            .foregroundColor(isSelected ? .white : .primary)
                        
                        Spacer()
                        
                        if isLoading {
                            ProgressView()
                                .controlSize(.small)
                                .scaleEffect(0.6)
                                .frame(width: 8, height: 8)
                        } else {
                            Circle()
                                .fill(statusColor)
                                .frame(width: 6, height: 6)
                        }
                    }
                    
                    Text(statusText)
                        .font(.caption)
                        .foregroundColor(isSelected ? Color.white.opacity(0.7) : .secondary)
                        .lineLimit(1)
                    
                    if let subtitle {
                        Text(subtitle)
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundColor(isSelected ? Color.white.opacity(0.9) : .accentColor)
                            .padding(.horizontal, 4)
                            .padding(.vertical, 1)
                            .background(
                                RoundedRectangle(cornerRadius: 4)
                                    .fill(isSelected ? Color.white.opacity(0.2) : Color.accentColor.opacity(0.12))
                            )
                            .padding(.top, 2)
                    }
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(
            RoundedRectangle(cornerRadius: 8)
                .fill(isSelected ? Color.accentColor : (isHovering ? Color.secondary.opacity(0.1) : Color.clear))
        )
        .onHover { hover in
            isHovering = hover
        }
    }
}

// MARK: - URL Formatter

enum BridgeRemoteURLFormatter {
    static func normalize(_ raw: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.lowercased().hasPrefix("https://") {
            return "wss" + String(trimmed.dropFirst(5))
        }
        return trimmed
    }
}
