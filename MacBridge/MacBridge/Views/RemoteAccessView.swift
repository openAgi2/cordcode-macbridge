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
    @State private var isEditingRelay = false
    @State private var isEditingCustomURL = false
    @State private var showTechnicalDetails = false
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
            .contentShape(Rectangle())
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
        PageContainer(scrolls: false, maxContentWidth: LayoutConstants.connectionSheetWidth) {
            VStack(alignment: .leading, spacing: 0) {
                // 统一顶部 Header 区域
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text(L10n.connectionStatus)
                            .font(.system(size: 22, weight: .bold))
                        Text(L10n.current == .zhHans ? "配置 iPhone 不在同一网络时的连接方式" : "Configure Connection Methods")
                            .font(.system(size: 13))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    
                    HStack(spacing: 12) {
                        Button {
                            Task { await loadRemoteStatus() }
                        } label: {
                            HStack(spacing: 6) {
                                if isLoadingStatus {
                                    ProgressView()
                                        .controlSize(.small)
                                } else {
                                    Image(systemName: "arrow.clockwise")
                                }
                                Text(L10n.current == .zhHans ? "刷新状态" : "Refresh Status")
                            }
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.regular)
                        .disabled(isLoadingStatus)

                        Button(L10n.done) {
                            dismiss()
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.regular)
                    }
                }
                .padding(.bottom, 24)

                // 左右分栏列表与详情
                HStack(alignment: .top, spacing: 0) {
                    // 左侧导航列表
                    VStack(alignment: .leading, spacing: 6) {
                        Text("连接方式")
                            .font(.system(size: 11, weight: .bold))
                            .foregroundStyle(.secondary)
                            .padding(.leading, 8)
                            .padding(.bottom, 4)

                        ForEach(ConnectionMethod.allCases, id: \.self) { method in
                            ConnectionSidebarButton(
                                method: method,
                                isSelected: selectedMethod == method,
                                localURL: localURL,
                                relayConfigured: relayConfigured,
                                tailscaleURL: tailscaleURL,
                                relayEnabled: relayEnabled,
                                savedRemoteURL: savedRemoteURL
                            ) {
                                selectedMethod = method
                            }
                        }
                    }
                    .frame(width: 220)
                    .padding(.trailing, 16)

                    Divider()

                    // 右侧详情卡片区
                    ScrollView {
                        VStack(alignment: .leading, spacing: 20) {
                            switch selectedMethod {
                            case .lan: lanDetailView
                            case .relay: relayDetailView
                            case .tailscale: tailscaleDetailView
                            case .other: customAddressDetailView
                            }
                        }
                        .padding(.horizontal, 16)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                
                Divider()
                    .padding(.top, 16)
                    .padding(.bottom, 12)
                
                HStack(spacing: 6) {
                    Image(systemName: "bolt.shield")
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                    Text(L10n.current == .zhHans
                        ? "智能连接策略：优先并行尝试局域网与 Tailscale 直连以实现最低延迟，仅在直连不可达时自动无缝回退至 Relay 加密中继。"
                        : "Smart Connection: Prioritizes low-latency LAN and Tailscale direct paths, falling back to Relay secure tunnel if unreachable.")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
                .padding(.bottom, 4)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
        .frame(width: LayoutConstants.connectionSheetWidth, height: LayoutConstants.connectionSheetHeight)
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

    private var lanDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            SettingsCardContainer {
                HStack(spacing: 16) {
                    ZStack {
                        Color.green.opacity(0.15)
                        Image(systemName: "wifi")
                            .font(.system(size: 22, weight: .bold))
                            .foregroundStyle(Color.green)
                    }
                    .frame(width: 48, height: 48)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))

                    VStack(alignment: .leading, spacing: 4) {
                        Text(localURL.isEmpty ? "未就绪" : "正常监听中")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundStyle(.primary)
                        Text(L10n.current == .zhHans
                            ? "当 iPhone 与 Mac 处于同一 Wi-Fi 或局域网下时，使用此方式进行高速直连。数据不离开局域网。"
                            : "When iPhone and Mac are on the same Wi-Fi or local network, this method provides high-speed direct connection. Data does not leave the local network.")
                            .font(.system(size: 12))
                            .foregroundStyle(.secondary)
                            .lineSpacing(2)
                    }

                    Spacer()
                    
                    if !localURL.isEmpty {
                        HStack(spacing: 6) {
                            Circle()
                                .fill(Color.green)
                                .frame(width: 8, height: 8)
                            Text("正常监听")
                                .font(.system(size: 13, weight: .medium))
                                .foregroundStyle(.green)
                        }
                    }
                }
            }

            if !localURL.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    Text(L10n.diagnosisInfo)
                        .font(.system(size: 12, weight: .bold))
                        .foregroundStyle(.secondary)
                        .padding(.leading, 4)

                    SettingsCardContainer {
                        VStack(alignment: .leading, spacing: 10) {
                            HStack {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text("局域网连接地址")
                                        .font(.system(size: 14, weight: .semibold))
                                    Text(localURL)
                                        .font(.system(size: 12, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                }
                                Spacer()
                                
                                Button(L10n.current == .zhHans ? "复制" : "Copy") {
                                    NSPasteboard.general.clearContents()
                                    NSPasteboard.general.setString(localURL, forType: .string)
                                }
                                .buttonStyle(.bordered)
                            }
                        }
                    }
                }
            }

            VStack(alignment: .leading, spacing: 8) {
                Text(L10n.securityLevel)
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.secondary)
                    .padding(.leading, 4)

                SettingsCardContainer {
                    HStack {
                        ZStack {
                            Color.green.opacity(0.15)
                            Image(systemName: "lock.shield.fill")
                                .font(.system(size: 12))
                                .foregroundStyle(.green)
                        }
                        .frame(width: 24, height: 24)
                        .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))

                        VStack(alignment: .leading, spacing: 2) {
                            Text("局域网内传输")
                                .font(.system(size: 14, weight: .semibold))
                            Text(L10n.remoteLANHint)
                                .font(.system(size: 12))
                                .foregroundStyle(.secondary)
                        }

                        Spacer()

                        Text("已保护")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(.green)
                    }
                }
            }
        }
    }

    private var relayDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            SettingsCardContainer {
                HStack(spacing: 16) {
                    ZStack {
                        Color.blue.opacity(0.15)
                        Image(systemName: "point.3.connected.trianglepath.dotted")
                            .font(.system(size: 22, weight: .bold))
                            .foregroundStyle(Color.accentColor)
                    }
                    .frame(width: 48, height: 48)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))

                    VStack(alignment: .leading, spacing: 4) {
                        Text(relayEnabled && relayConfigured == true ? "已启用" : (relayEnabled ? "配置中" : "未启用"))
                            .font(.system(size: 16, weight: .bold))
                            .foregroundStyle(.primary)
                        Text(L10n.current == .zhHans
                            ? "可在不同网络下安全连接此 Mac"
                            : "Allows safe connection under different network environments.")
                            .font(.system(size: 12))
                            .foregroundStyle(.secondary)
                        HStack(spacing: 4) {
                            Image(systemName: "lock.shield")
                                .font(.system(size: 11))
                            Text("端到端加密")
                                .font(.system(size: 11))
                        }
                        .foregroundStyle(.secondary.opacity(0.8))
                    }

                    Spacer()

                    Toggle("", isOn: Binding(
                        get: { relayEnabled },
                        set: { newValue in
                            toggleRelayEnabled(to: newValue)
                        }
                    ))
                    .toggleStyle(.switch)
                    .disabled(isSavingRelay)
                }
            }

            if isSavingRelay {
                switch relaySaveState {
                case .enabling:
                    feedbackRow(text: L10n.remoteRelayEnabling, isProgress: true)
                case .disabling:
                    feedbackRow(text: L10n.remoteRelayDisabling, isProgress: true)
                default:
                    EmptyView()
                }
            }

            VStack(alignment: .leading, spacing: 8) {
                Text("中继服务器")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.secondary)
                    .padding(.leading, 4)

                SettingsCardContainer {
                    VStack(alignment: .leading, spacing: 12) {
                        HStack {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(relayMode == .official ? "官方中继" : "自定义中继")
                                    .font(.system(size: 14, weight: .semibold))
                                Text(relayMode == .official ? "自动选择可用节点" : normalizedCustomRelayEndpoint)
                                    .font(.system(size: 12))
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            
                            if !isEditingRelay {
                                Button("更改...") {
                                    withAnimation(.easeInOut(duration: 0.2)) {
                                        isEditingRelay = true
                                    }
                                }
                                .buttonStyle(.bordered)
                                .controlSize(.regular)
                            }
                        }

                        if isEditingRelay {
                            VStack(alignment: .leading, spacing: 12) {
                                Divider()
                                    .padding(.vertical, 4)

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

                                HStack(spacing: 8) {
                                    Button(L10n.save) {
                                        saveRelayConfiguration()
                                        withAnimation(.easeInOut(duration: 0.2)) {
                                            isEditingRelay = false
                                        }
                                    }
                                    .buttonStyle(.borderedProminent)
                                    .disabled(!canSaveRelay)

                                    Button(L10n.cancel) {
                                        customRelayEndpoint = savedCustomRelayEndpoint
                                        relayMode = savedCustomRelayEndpoint.isEmpty ? .official : .custom
                                        withAnimation(.easeInOut(duration: 0.2)) {
                                            isEditingRelay = false
                                        }
                                    }
                                    .buttonStyle(.bordered)
                                }
                            }
                            .transition(.opacity)
                        }
                    }
                }
            }

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

            VStack(alignment: .leading, spacing: 8) {
                Text("安全与隐私")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.secondary)
                    .padding(.leading, 4)

                SettingsCardContainer {
                    HStack {
                        ZStack {
                            Color.green.opacity(0.15)
                            Image(systemName: "shield.fill")
                                .font(.system(size: 12))
                                .foregroundStyle(.green)
                        }
                        .frame(width: 24, height: 24)
                        .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))

                        VStack(alignment: .leading, spacing: 2) {
                            Text("端到端加密")
                                .font(.system(size: 14, weight: .semibold))
                            Text("中继服务器无法读取传输的代码或消息内容")
                                .font(.system(size: 12))
                                .foregroundStyle(.secondary)
                        }

                        Spacer()

                        Text("已开启")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(.green)
                    }
                }
            }

            VStack(alignment: .leading, spacing: 8) {
                Button {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        showTechnicalDetails.toggle()
                    }
                } label: {
                    HStack {
                        Image(systemName: "info.circle")
                            .font(.system(size: 14))
                        Text("查看连接技术信息")
                            .font(.system(size: 13, weight: .medium))
                        Spacer()
                        Image(systemName: "chevron.right")
                            .font(.system(size: 10, weight: .bold))
                            .rotationEffect(.degrees(showTechnicalDetails ? 90 : 0))
                    }
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 8)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)

                if showTechnicalDetails {
                    SettingsCardContainer {
                        VStack(alignment: .leading, spacing: 8) {
                            technicalInfoRow(label: "协议版本", value: "WSS (Websocket Secure)")
                            technicalInfoRow(label: "加密协议", value: "端到端 HPKE (X25519 / AES-128-GCM)")
                            technicalInfoRow(label: "连接状态", value: relayConfigured == true ? "已接入中继网" : "未就绪")
                            if let endpoint = remoteStatus?.relay?.endpoint {
                                technicalInfoRow(label: "服务器地址", value: endpoint)
                            }
                            if let routeId = remoteStatus?.relay?.routeId {
                                technicalInfoRow(label: "路由识别码", value: routeId)
                            }
                        }
                    }
                    .transition(.opacity)
                }
            }
        }
    }

    private var tailscaleDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            SettingsCardContainer {
                HStack(spacing: 16) {
                    ZStack {
                        Color.gray.opacity(0.15)
                        Image(systemName: "circle.grid.3x3.fill")
                            .font(.system(size: 20, weight: .bold))
                            .foregroundStyle(Color.gray)
                    }
                    .frame(width: 48, height: 48)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))

                    VStack(alignment: .leading, spacing: 4) {
                        Text(tailscaleURL.isEmpty ? "未激活" : "检测到虚拟网 IP")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundStyle(.primary)
                        Text(L10n.current == .zhHans
                            ? "利用 Tailscale 创建的私有虚拟网进行连接。适用于已安装 Tailscale 客户端的设备之间的直接互通。"
                            : "Connect using a private virtual network created by Tailscale. Ideal for direct communication between devices with the Tailscale client installed.")
                            .font(.system(size: 12))
                            .foregroundStyle(.secondary)
                            .lineSpacing(2)
                    }

                    Spacer()

                    Toggle("", isOn: $includeTailscale)
                        .toggleStyle(.switch)
                        .disabled(tailscaleURL.isEmpty)
                }
            }

            if !tailscaleURL.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    Text("检测到的虚拟网地址")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundStyle(.secondary)
                        .padding(.leading, 4)

                    SettingsCardContainer {
                        VStack(alignment: .leading, spacing: 10) {
                            HStack {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text("Tailscale 地址")
                                        .font(.system(size: 14, weight: .semibold))
                                    Text(tailscaleURL)
                                        .font(.system(size: 12, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                }
                                Spacer()
                                
                                Button(L10n.current == .zhHans ? "复制" : "Copy") {
                                    NSPasteboard.general.clearContents()
                                    NSPasteboard.general.setString(tailscaleURL, forType: .string)
                                }
                                .buttonStyle(.bordered)
                            }
                        }
                    }
                }
            }

            VStack(alignment: .leading, spacing: 8) {
                Text(L10n.securityLevel)
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.secondary)
                    .padding(.leading, 4)

                SettingsCardContainer {
                    HStack {
                        ZStack {
                            Color.green.opacity(0.15)
                            Image(systemName: "lock.shield.fill")
                                .font(.system(size: 12))
                                .foregroundStyle(.green)
                        }
                        .frame(width: 24, height: 24)
                        .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))

                        VStack(alignment: .leading, spacing: 2) {
                            Text("WireGuard 加密通道")
                                .font(.system(size: 14, weight: .semibold))
                            Text("• " + L10n.secTailscaleTunnel)
                                .font(.system(size: 12))
                                .foregroundStyle(.secondary)
                        }

                        Spacer()

                        Text("已保护")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(.green)
                    }
                }
            }
        }
    }

    private var customAddressDetailView: some View {
        VStack(alignment: .leading, spacing: 20) {
            SettingsCardContainer {
                HStack(spacing: 16) {
                    ZStack {
                        Color.purple.opacity(0.15)
                        Image(systemName: "globe")
                            .font(.system(size: 22, weight: .bold))
                            .foregroundStyle(Color.purple)
                    }
                    .frame(width: 48, height: 48)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))

                    VStack(alignment: .leading, spacing: 4) {
                        Text(savedRemoteURL.isEmpty ? "未配置" : "已配置公网穿透")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundStyle(.primary)
                        Text(L10n.current == .zhHans
                            ? "允许您通过反向代理、内网穿透（如 FRP）或自建 VPS 暴露的公网端点进行连接。"
                            : "Allows you to connect via public endpoints exposed by reverse proxies, intranets (such as FRP), or self-hosted VPS.")
                            .font(.system(size: 12))
                            .foregroundStyle(.secondary)
                            .lineSpacing(2)
                    }

                    Spacer()

                    Toggle("", isOn: $includeRemote)
                        .toggleStyle(.switch)
                        .disabled(savedRemoteURL.isEmpty)
                }
            }

            VStack(alignment: .leading, spacing: 8) {
                Text("公网接入地址")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.secondary)
                    .padding(.leading, 4)

                SettingsCardContainer {
                    VStack(alignment: .leading, spacing: 12) {
                        HStack {
                            VStack(alignment: .leading, spacing: 4) {
                                Text("自定义服务 URL")
                                    .font(.system(size: 14, weight: .semibold))
                                Text(savedRemoteURL.isEmpty ? "未设置" : savedRemoteURL)
                                    .font(.system(size: 12, design: .monospaced))
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            
                            if !isEditingCustomURL {
                                Button("更改...") {
                                    withAnimation(.easeInOut(duration: 0.2)) {
                                        isEditingCustomURL = true
                                    }
                                }
                                .buttonStyle(.bordered)
                                .controlSize(.regular)
                            }
                        }

                        if isEditingCustomURL {
                            VStack(alignment: .leading, spacing: 12) {
                                Divider()
                                    .padding(.vertical, 4)

                                TextField(L10n.remoteVPSPlaceholder, text: $remoteURL)
                                    .textFieldStyle(.roundedBorder)
                                    .font(.system(.body, design: .monospaced))

                                if !frpURL.isEmpty && !isValidManualRemoteURL(frpURL) {
                                    InlineFeedback(style: .warning, message: L10n.remoteVPSValidation)
                                }
                                
                                if showPublicWSWarning {
                                    InlineFeedback(style: .warning, message: L10n.remoteUnencryptedWarning)
                                }

                                HStack(spacing: 8) {
                                    Button(L10n.save) {
                                        saveCustomAddressConfiguration()
                                        withAnimation(.easeInOut(duration: 0.2)) {
                                            isEditingCustomURL = false
                                        }
                                    }
                                    .buttonStyle(.borderedProminent)
                                    .disabled(remoteURL == savedRemoteURL || customAddressSaveStateIsProgress)

                                    Button(L10n.cancel) {
                                        remoteURL = savedRemoteURL
                                        withAnimation(.easeInOut(duration: 0.2)) {
                                            isEditingCustomURL = false
                                        }
                                    }
                                    .buttonStyle(.bordered)
                                }
                            }
                            .transition(.opacity)
                        }
                    }
                }
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

            if !savedRemoteURL.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    Text("安全分析")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundStyle(.secondary)
                        .padding(.leading, 4)

                    SettingsCardContainer {
                        HStack {
                            ZStack {
                                Color(showPublicWSWarning ? .orange : .green).opacity(0.15)
                                Image(systemName: showPublicWSWarning ? "exclamationmark.triangle.fill" : "lock.shield.fill")
                                    .font(.system(size: 12))
                                    .foregroundStyle(showPublicWSWarning ? .orange : .green)
                            }
                            .frame(width: 24, height: 24)
                            .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))

                            VStack(alignment: .leading, spacing: 2) {
                                Text(showPublicWSWarning ? "使用非加密协议" : "使用加密传输")
                                    .font(.system(size: 14, weight: .semibold))
                                Text(showPublicWSWarning
                                    ? "未加密的数据可能会被第三方窃听，建议改用 wss/https 协议。"
                                    : "数据流正在使用 TLS (wss/https) 安全通道进行加密传输。")
                                    .font(.system(size: 12))
                                    .foregroundStyle(.secondary)
                            }

                            Spacer()

                            Text(showPublicWSWarning ? "不安全" : "安全")
                                .font(.system(size: 13, weight: .medium))
                                .foregroundStyle(showPublicWSWarning ? .orange : .green)
                        }
                    }
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

    private func technicalInfoRow(label: String, value: String) -> some View {
        HStack {
            Text(label)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
            Spacer()
            Text(value)
                .font(.system(size: 12, design: .monospaced))
                .foregroundStyle(.primary)
                .textSelection(.enabled)
        }
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

private struct SettingsCardContainer<Content: View>: View {
    let content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            content
        }
        .padding(16)
        .background {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .fill(Color.white.opacity(0.04))
        }
        .overlay {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .stroke(Color.white.opacity(0.08), lineWidth: 1)
        }
    }
}

private struct ConnectionSidebarButton: View {
    let method: RemoteAccessView.ConnectionMethod
    let isSelected: Bool
    let localURL: String
    let relayConfigured: Bool?
    let tailscaleURL: String
    let relayEnabled: Bool
    let savedRemoteURL: String
    let action: () -> Void

    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                iconView
                    .frame(width: 28, height: 28)
                    .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))

                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(.primary)
                    Text(subtitle)
                        .font(.system(size: 10))
                        .foregroundStyle(.secondary)
                }

                Spacer()

                statusBadge
            }
            .padding(.vertical, 8)
            .padding(.horizontal, 10)
            .background {
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(isSelected ? Color.accentColor.opacity(0.08) : (isHovering ? Color.white.opacity(0.03) : Color.clear))
            }
            .overlay {
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .stroke(isSelected ? Color.accentColor.opacity(0.35) : Color.clear, lineWidth: 1)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .scaleEffect(isHovering ? 1.01 : 1)
        .onHover { hovering in
            withAnimation(.easeOut(duration: 0.16)) {
                isHovering = hovering
            }
        }
    }

    @ViewBuilder
    private var iconView: some View {
        switch method {
        case .lan:
            ZStack {
                Color.green.opacity(0.15)
                Image(systemName: "wifi")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(.green)
            }
        case .relay:
            ZStack {
                Color.blue.opacity(0.15)
                Image(systemName: "point.3.connected.trianglepath.dotted")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.blue)
            }
        case .tailscale:
            ZStack {
                Color.gray.opacity(0.15)
                Image(systemName: "circle.grid.3x3.fill")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.gray)
            }
        case .other:
            ZStack {
                Color.purple.opacity(0.15)
                Image(systemName: "globe")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(.purple)
            }
        }
    }

    private var title: String {
        switch method {
        case .lan: return "局域网"
        case .relay: return "Relay"
        case .tailscale: return "Tailscale"
        case .other: return "自定义连接"
        }
    }

    private var subtitle: String {
        switch method {
        case .lan: return "同一网络内自动连接"
        case .relay: return "跨网络安全连接"
        case .tailscale: return "通过私有网络连接"
        case .other: return "VPS 或自定义地址"
        }
    }

    @ViewBuilder
    private var statusBadge: some View {
        let (text, color) = badgeInfo
        Text(text)
            .font(.system(size: 10, weight: .medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(color.opacity(0.12))
            .foregroundStyle(color)
            .clipShape(Capsule())
    }

    private var badgeInfo: (String, Color) {
        switch method {
        case .lan:
            return (localURL.isEmpty ? "未配置" : "可用", localURL.isEmpty ? .secondary : .green)
        case .relay:
            if !relayEnabled {
                return ("未启用", .secondary)
            }
            return (relayConfigured == true ? "已启用" : "未配置", relayConfigured == true ? .blue : .orange)
        case .tailscale:
            return (tailscaleURL.isEmpty ? "未设置" : "可用", tailscaleURL.isEmpty ? .secondary : .green)
        case .other:
            return (savedRemoteURL.isEmpty ? "未设置" : "已配置", savedRemoteURL.isEmpty ? .secondary : .purple)
        }
    }
}
