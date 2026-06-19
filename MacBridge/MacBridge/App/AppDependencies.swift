import Combine
import Foundation

// MARK: - DI 容器

/// 全局依赖注入容器，创建并绑定 RuntimeManager ↔ ViewModels
@MainActor
class AppDependencies: ObservableObject {
    let runtimeManager: RuntimeManager
    let statusViewModel: BridgeStatusViewModel
    let pairingViewModel: PairingViewModel
    let settingsViewModel: SettingsViewModel

    private let dataDir: String
    private var hasStartedBridge = false

    init() {
        // 从 Bundle 获取 runtime binary 路径，回退到 /usr/local/bin
        let executablePath = Bundle.main.url(forResource: "cccode-bridge-runtime", withExtension: nil)?.path
            ?? "/usr/local/bin/cccode-bridge-runtime"

        let dir = NSSearchPathForDirectoriesInDomains(.applicationSupportDirectory, .userDomainMask, true).first!
            + "/CCCode Bridge"
        self.dataDir = dir
        let logDir = dir + "/logs"

        // OpenCode 凭据：环境变量 → credentials.json 降级
        var opencodeUser = ""
        var opencodePass = ""
        if let envUser = ProcessInfo.processInfo.environment["OPENCODE_SERVER_USERNAME"],
           !envUser.isEmpty {
            opencodeUser = envUser
        } else {
            opencodeUser = Self.readCredential("opencode_user", from: dir) ?? ""
        }
        if let envPass = ProcessInfo.processInfo.environment["OPENCODE_SERVER_PASSWORD"],
           !envPass.isEmpty {
            opencodePass = envPass
        } else {
            opencodePass = Self.readCredential("opencode_pass", from: dir) ?? ""
        }

        // 已有常驻 OpenCode 服务时复用其 LaunchAgent 凭据。
        // 全新安装不应生成另一套密码，让正在运行的服务返回 401。
        if opencodeUser.isEmpty || opencodePass.isEmpty {
            let launchAgentURL = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Library/LaunchAgents/com.opencode.server.plist")
            if let existing = OpenCodeLaunchAgentCredentials.read(from: launchAgentURL) {
                if opencodeUser.isEmpty { opencodeUser = existing.user }
                if opencodePass.isEmpty { opencodePass = existing.password }
                Self.writeCredentials(user: opencodeUser, pass: opencodePass, to: dir)
                NSLog("[AppDependencies] Reused credentials from the existing OpenCode LaunchAgent.")
            }
        }

        // 首次运行或凭据为空时，自动生成随机凭据并保存
        if opencodeUser.isEmpty || opencodePass.isEmpty {
            opencodeUser = "opencode"
            opencodePass = OpenCodeCredentialsGenerator.generatePassword()
            Self.writeCredentials(user: opencodeUser, pass: opencodePass, to: dir)
            NSLog("[AppDependencies] Automatically generated OpenCode credentials for first-time launch.")
        }

        let configuredRelayEndpoint = OfficialRelayConfiguration.endpoint
        let savedRelayEndpoint = UserDefaults.standard.string(forKey: "relayEndpoint") ?? ""
        let hasCurrentRelayRoute = !configuredRelayEndpoint.isEmpty &&
            savedRelayEndpoint == configuredRelayEndpoint
        let relayEndpoint = hasCurrentRelayRoute ? configuredRelayEndpoint : ""
        let relayRouteID = hasCurrentRelayRoute
            ? UserDefaults.standard.string(forKey: "relayRouteID") ?? ""
            : ""

        let relayEnabled = UserDefaults.standard.object(forKey: "relayEnabled") as? Bool ?? true
        let logFilePath = logDir + "/go-bridge.log"
        let config = RuntimeConfig(
            executablePath: executablePath,
            dataDir: dir,
            logDir: logDir,
            workDir: FileManager.default.homeDirectoryForCurrentUser.path,
            codexBackend: "app_server",
            codexAppServerURL: "ws://127.0.0.1:4141",
            opencodeUser: opencodeUser,
            opencodePass: opencodePass,
            logFilePath: logFilePath,
            remoteURL: UserDefaults.standard.string(forKey: "remoteBridgeURL") ?? "",
            includeTailscaleInPairing: UserDefaults.standard.object(forKey: "pairingIncludeTailscale") as? Bool ?? true,
            includeRemoteInPairing: UserDefaults.standard.object(forKey: "pairingIncludeRemote") as? Bool ?? true,
            relayEnabled: relayEnabled,
            relayEndpoint: relayEnabled ? relayEndpoint : "",
            relayRouteID: relayEnabled ? relayRouteID : "",
            // Keychain access may require user authorization after an app update.
            // OfficialRelayProvisioner loads it off the main actor and restarts with the real credential.
            relayCredential: "",
            relayServiceAddress: UserDefaults.standard.string(forKey: "relayServiceAddress") ?? ""
        )

        self.runtimeManager = RuntimeManager(config: config)
        self.statusViewModel = BridgeStatusViewModel()
        self.statusViewModel.runtimeManager = runtimeManager
        self.pairingViewModel = PairingViewModel()
        // SettingsViewModel 的 onCredentialsChanged 在 didLoad 中绑定，避免 init 阶段捕获 self
        self.settingsViewModel = SettingsViewModel(dataDir: dir, onCredentialsChanged: {})

        // 延迟绑定凭据变更回调（self 已完成初始化）
        self.settingsViewModel.onCredentialsChanged = { [weak self] in
            self?.handleCredentialsChanged()
        }

        // management 端点变更后自动刷新 Pairing API client，支持 launchctl restart 后重新附着
        Publishers.CombineLatest(runtimeManager.$managementURL, runtimeManager.$managementToken)
            .receive(on: DispatchQueue.main)
            .compactMap { url, token -> ManagementAPIClient? in
                guard let url, let token else { return nil }
                return try? ManagementAPIClient(baseURL: url, token: token)
            }
            .sink { [weak self] client in
                self?.pairingViewModel.configure(apiClient: client)
            }
            .store(in: &cancellables)

        // Bridge 生命周期属于应用，不应依赖主窗口是否被恢复或显示。
        Task { @MainActor [weak self] in
            self?.startBridge()
        }
    }

    private var cancellables = Set<AnyCancellable>()

    /// 延迟启动 bridge，给 SwiftUI 足够的时间完成 UI 初始化
    func startBridge() {
        guard !hasStartedBridge else { return }
        hasStartedBridge = true

        // 监听远程 URL 变更，更新 RuntimeConfig 并重启
        NotificationCenter.default.publisher(for: .remoteURLDidChange)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in
                self?.handleRemoteURLChange()
            }
            .store(in: &cancellables)

        runtimeManager.start()
        let relayEnabled = UserDefaults.standard.object(forKey: "relayEnabled") as? Bool ?? true
        if relayEnabled && OfficialRelayConfiguration.isAvailable {
            Task { [weak self] in
                do {
                    let relay = try await OfficialRelayProvisioner.shared.ensureRoute()
                    guard let self else { return }
                    guard relay.endpoint == OfficialRelayConfiguration.endpoint else { return }
                    guard self.runtimeManager.config.relayEndpoint != relay.endpoint ||
                            self.runtimeManager.config.relayRouteID != relay.routeID ||
                            self.runtimeManager.config.relayCredential != relay.credential else {
                        return
                    }
                    self.runtimeManager.config.relayEndpoint = relay.endpoint
                    self.runtimeManager.config.relayRouteID = relay.routeID
                    self.runtimeManager.config.relayCredential = relay.credential
                    self.runtimeManager.restart()
                } catch {
                    NSLog("[AppDependencies] 官方 Relay 自动启用失败: \(error.localizedDescription)")
                }
            }
        }
    }

    /// 远程 URL 变更回调：从 UserDefaults 读取最新 remoteURL，更新配置并重启
    private func handleRemoteURLChange() {
        let remoteURL = UserDefaults.standard.string(forKey: "remoteBridgeURL") ?? ""
        let includeTailscaleInPairing = UserDefaults.standard.object(forKey: "pairingIncludeTailscale") as? Bool ?? true
        let includeRemoteInPairing = UserDefaults.standard.object(forKey: "pairingIncludeRemote") as? Bool ?? true
        let configuredRelayEndpoint = OfficialRelayConfiguration.endpoint
        let savedRelayEndpoint = UserDefaults.standard.string(forKey: "relayEndpoint") ?? ""
        let hasCurrentRelayRoute = !configuredRelayEndpoint.isEmpty &&
            savedRelayEndpoint == configuredRelayEndpoint
        let relayEnabled = UserDefaults.standard.object(forKey: "relayEnabled") as? Bool ?? true
        let relayEndpoint = hasCurrentRelayRoute ? configuredRelayEndpoint : ""
        let relayRouteID = hasCurrentRelayRoute
            ? UserDefaults.standard.string(forKey: "relayRouteID") ?? ""
            : ""
        let relayCredential = hasCurrentRelayRoute
            ? RelayRouteCredentialStore.load()
            : ""
        let relayServiceAddress = UserDefaults.standard.string(forKey: "relayServiceAddress") ?? ""

        // T05: 原子合并所有字段变更后只 restart 一次（替代原先先改字段再 restart 的两次赋值路径）。
        runtimeManager.applyConfigAndRestart { c in
            c.remoteURL = remoteURL
            c.includeTailscaleInPairing = includeTailscaleInPairing
            c.includeRemoteInPairing = includeRemoteInPairing
            c.relayEnabled = relayEnabled
            c.relayEndpoint = relayEnabled ? relayEndpoint : ""
            c.relayRouteID = relayEnabled ? relayRouteID : ""
            c.relayCredential = relayEnabled ? relayCredential : ""
            c.relayServiceAddress = relayServiceAddress
        }

        if relayEnabled && OfficialRelayConfiguration.isAvailable && !hasCurrentRelayRoute {
            Task { [weak self] in
                do {
                    let relay = try await OfficialRelayProvisioner.shared.ensureRoute()
                    guard let self else { return }
                    guard relay.endpoint == OfficialRelayConfiguration.endpoint else { return }
                    // T05: Relay provisioning 完成后，用 applyConfigAndRestart 合并字段并只 restart 一次。
                    // 由于 restart() 的 launch generation + 可取消 Task，若与上一次 restart 在 1.5s 窗口内
                    // 重叠，会自动收敛为单次 launch（不再双 restart）。
                    self.runtimeManager.applyConfigAndRestart { c in
                        c.relayEndpoint = relay.endpoint
                        c.relayRouteID = relay.routeID
                        c.relayCredential = relay.credential
                    }
                } catch {
                    NSLog("[AppDependencies] Relay 地址变更后启用失败: \(error.localizedDescription)")
                }
            }
        }
    }

    /// 凭据变更回调：重新读取 credentials.json，构造新 RuntimeConfig，重启 Bridge
    private func handleCredentialsChanged() {
        let opencodeUser = Self.readCredential("opencode_user", from: dataDir)
            ?? ProcessInfo.processInfo.environment["OPENCODE_SERVER_USERNAME"]
            ?? ""
        let opencodePass = Self.readCredential("opencode_pass", from: dataDir)
            ?? ProcessInfo.processInfo.environment["OPENCODE_SERVER_PASSWORD"]
            ?? ""

        runtimeManager.updateOpenCodeCredentials(user: opencodeUser, pass: opencodePass)
        runtimeManager.restart()
    }

    /// 从 dataDir/credentials.json 读取持久化凭据。
    /// credentials.json 格式：{ "opencode_user": "...", "opencode_pass": "..." }
    static func readCredential(_ key: String, from dataDir: String) -> String? {
        let path = dataDir + "/credentials.json"
        guard let data = FileManager.default.contents(atPath: path),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return json[key] as? String
    }

    /// 将 OpenCode 凭据持久化写入 dataDir/credentials.json
    static func writeCredentials(user: String, pass: String, to dataDir: String) {
        let path = dataDir + "/credentials.json"
        let dict = [
            "opencode_user": user,
            "opencode_pass": pass
        ]
        do {
            try FileManager.default.createDirectory(atPath: dataDir, withIntermediateDirectories: true)
            let data = try JSONSerialization.data(withJSONObject: dict, options: [.prettyPrinted, .sortedKeys])
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path)
        } catch {
            NSLog("[AppDependencies] Failed to write credentials: \(error.localizedDescription)")
        }
    }
}
