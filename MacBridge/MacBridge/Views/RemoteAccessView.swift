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
    
    @State private var showAdvanced = false
    @State private var relayMode: RelayMode = .official
    @State private var relaySaveState: RelaySaveState = .idle
    @State private var customAddressSaveState: CustomAddressSaveState = .idle
    
    @State private var remoteStatus: RemoteStatus?
    @State private var isLoadingStatus = false
    @State private var statusError: String?
    @State private var lastUpdatedTime: String?

    @Environment(\.dismiss) private var dismiss

    // 为了让 sheet 感觉上更接近老版左右分栏，在 sheet 内部实现简易 master-detail
    // 保持入口仍是 sheet（工作站/toolbar 打开），但内容呈现像老版列表+详情。
    enum ConnectionMethod: String, CaseIterable {
        case lan = "局域网"
        case relay = "Relay"
        case tailscale = "Tailscale"
        case other = "其他 (VPS/自定义)"
    }
    @State private var selectedMethod: ConnectionMethod = .relay
    
    var apiClient: ManagementAPIClient?
    /// P2-3：Reduce Motion 时取消位移，只保留状态替换。
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

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

    private func methodButton(_ method: ConnectionMethod, _ label: String, _ status: String) -> some View {
        Button {
            selectedMethod = method
        } label: {
            HStack {
                Text(label)
                    .font(.body)
                Spacer()
                Text(status)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 6)
            .padding(.horizontal, 8)
            .background(
                selectedMethod == method ? Color.accentColor.opacity(0.12) : Color.clear
            )
            .cornerRadius(4)
        }
        .buttonStyle(.plain)
    }

    private var showPublicWSWarning: Bool {
        isPublicWSAddress(remoteURL) || (remoteStatus?.remoteAnalysis?.isPublicWS == true && remoteURL == savedRemoteURL)
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
        // P1-2：单页「连接状态」。Relay/LAN 默认可见，Tailscale 与自定义地址收入同页高级展开区。
        // 不再使用左/右二级导航（GeometryReader wide/narrow 分支）；既有保存状态枚举与配置语义不变。
        PageContainer(scrolls: false, maxContentWidth: LayoutConstants.connectionSheetWidth) {
            VStack(alignment: .leading, spacing: 0) {
                PageHeader(L10n.connectionStatus, subtitle: L10n.remoteSubtitle) {
                    HStack(spacing: 12) {
                        Button {
                            Task { await loadRemoteStatus() }
                        } label: {
                            if isLoadingStatus {
                                ProgressView()
                                    .controlSize(.small)
                            }
                            Text(L10n.refreshAll)
                        }
                        .disabled(isLoadingStatus)

                        Button("完成") {
                            dismiss()
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                    }
                }
                .padding(.bottom, 20)

                // 恢复左右分栏风格（接近老版），但在 sheet 内
                HStack(alignment: .top, spacing: 0) {
                    // 左侧列表 (接近老版)
                    VStack(alignment: .leading, spacing: 4) {
                        Button { selectedMethod = .lan } label: {
                            HStack {
                                Text("局域网")
                                Spacer()
                                Text(localURL.isEmpty ? "未配置" : "可用")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            .padding(.vertical, 6).padding(.horizontal, 8)
                            .background(selectedMethod == .lan ? Color.accentColor.opacity(0.12) : Color.clear)
                            .cornerRadius(4)
                        }.buttonStyle(.plain)

                        Button { selectedMethod = .relay } label: {
                            HStack {
                                Text("Relay")
                                Spacer()
                                Text(relayConfigured == true ? "已配置" : "未配置")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            .padding(.vertical, 6).padding(.horizontal, 8)
                            .background(selectedMethod == .relay ? Color.accentColor.opacity(0.12) : Color.clear)
                            .cornerRadius(4)
                        }.buttonStyle(.plain)

                        Button { selectedMethod = .tailscale } label: {
                            HStack {
                                Text("Tailscale")
                                Spacer()
                                Text(tailscaleURL.isEmpty ? "未配置" : "可用")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            .padding(.vertical, 6).padding(.horizontal, 8)
                            .background(selectedMethod == .tailscale ? Color.accentColor.opacity(0.12) : Color.clear)
                            .cornerRadius(4)
                        }.buttonStyle(.plain)

                        Button { selectedMethod = .other } label: {
                            HStack {
                                Text("其他 (VPS/自定义)")
                                Spacer()
                                Text("按需")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            .padding(.vertical, 6).padding(.horizontal, 8)
                            .background(selectedMethod == .other ? Color.accentColor.opacity(0.12) : Color.clear)
                            .cornerRadius(4)
                        }.buttonStyle(.plain)
                    }
                    .frame(width: 160)
                    .padding(.trailing, 12)

                    Divider()

                    // 右侧详情
                    ScrollView {
                        Group {
                            switch selectedMethod {
                            case .lan: lanDetailView
                            case .relay: relayDetailView
                            case .tailscale: tailscaleDetailView
                            case .other: customAddressDetailView
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .padding(.leading, 16)
                }
                .padding(.trailing, 8)
            }
            .frame(minWidth: 1000, minHeight: 680)  // 稳定 sheet 尺寸：防止切换不同连接方式时窗口跳跃。minHeight 覆盖 Relay 最完整配置的高度。
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

    private var advancedConnectionsSection: some View {
        VStack(alignment: .leading, spacing: 16) {
            Button {
                if reduceMotion {
                    showAdvanced.toggle()
                } else {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        showAdvanced.toggle()
                    }
                }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 10, weight: .bold))
                        .rotationEffect(.degrees(showAdvanced ? 90 : 0))
                    Text(showAdvanced ? L10n.connectionStatusHideAdvanced : L10n.connectionStatusShowAdvanced)
                        .font(.body.weight(.medium))
                    Spacer()
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.accentColor)

            if showAdvanced {
                VStack(alignment: .leading, spacing: 20) {
                    tailscaleDetailView
                    Divider()
                    customAddressDetailView
                }
                // Reduce Motion 时只做透明度替换，不产生位移。
                .transition(reduceMotion ? .opacity : .opacity.combined(with: .move(edge: .top)))
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
                    // r5: 官方默认隐藏 endpoint，自定义时必须显示真实值 + 恢复默认
                    if relayMode == .custom {
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
