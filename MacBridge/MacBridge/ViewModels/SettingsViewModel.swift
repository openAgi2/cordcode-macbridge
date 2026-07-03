import Foundation

enum SaveFeedback: Equatable {
    case idle
    case saving
    case success(String)
    case failure(String)
}

enum EndpointValidationState: Equatable {
    case idle
    case validating
    case valid
    case warning(String)   // legacy_insecure_unverified：可用但不安全
    case failed(String)
}

@MainActor
class SettingsViewModel: ObservableObject {
    @Published var opencodeUser = ""
    @Published var opencodePass = ""
    @Published var opencodeSource: OpenCodeServerSource = .managedLocal
    @Published var opencodeURL = ""
    @Published var endpointValidation: EndpointValidationState = .idle
    @Published var displayName = ""
    @Published var displayNameFeedback: SaveFeedback = .idle
    @Published var credentialsFeedback: SaveFeedback = .idle

    private var savedOpenCodeUser = ""
    private var savedOpenCodePass = ""
    private var savedOpenCodeSource: OpenCodeServerSource = .managedLocal
    private var savedOpenCodeURL = ""
    private var savedDisplayName = ""
    private let dataDir: String
    var onCredentialsChanged: (() -> Void)
    var managementAPIClient: ManagementAPIClient?

    var isCredentialsDirty: Bool {
        opencodeUser != savedOpenCodeUser
            || opencodePass != savedOpenCodePass
            || opencodeSource != savedOpenCodeSource
            || opencodeURL.trimmingCharacters(in: .whitespacesAndNewlines) != savedOpenCodeURL
    }

    var isDisplayNameDirty: Bool {
        let trimmed = displayName.trimmingCharacters(in: .whitespacesAndNewlines)
        return !trimmed.isEmpty && trimmed != savedDisplayName
    }

    /// endpoint 是否值得按「验证」：external_http / legacy_64667 才有可探测的 server。
    var canValidateEndpoint: Bool {
        switch opencodeSource {
        case .externalHttp, .legacy64667:
            return true
        case .managedLocal, .disabled, .serviceDiscoveryFuture:
            return false
        }
    }

    init(dataDir: String, onCredentialsChanged: @escaping () -> Void) {
        self.dataDir = dataDir
        self.onCredentialsChanged = onCredentialsChanged
        loadCredentials()
    }

    func loadCredentials() {
        let path = dataDir + "/credentials.json"
        guard let data = FileManager.default.contents(atPath: path),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            savedOpenCodeSource = opencodeSource
            return
        }
        opencodeUser = json["opencode_user"] as? String ?? ""
        opencodePass = json["opencode_pass"] as? String ?? ""
        if let raw = json["opencode_source"] as? String,
           let parsed = OpenCodeServerSource(rawValue: raw) {
            opencodeSource = parsed
        }
        opencodeURL = json["opencode_url"] as? String ?? ""
        savedOpenCodeUser = opencodeUser
        savedOpenCodePass = opencodePass
        savedOpenCodeSource = opencodeSource
        savedOpenCodeURL = opencodeURL.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    func regeneratePassword() {
        opencodePass = OpenCodeCredentialsGenerator.generatePassword()
        credentialsFeedback = .idle
    }

    func saveCredentials() {
        guard !opencodeUser.isEmpty, !opencodePass.isEmpty else { return }
        credentialsFeedback = .saving

        // 保存时规范化 URL（localhost → 127.0.0.1），仅对 external_http 有意义。
        if opencodeSource == .externalHttp {
            let trimmed = opencodeURL.trimmingCharacters(in: .whitespacesAndNewlines)
            opencodeURL = OpenCodeEndpointResolver.normalizeLoopbackURL(trimmed) ?? trimmed
        }

        do {
            try FileManager.default.createDirectory(
                atPath: dataDir,
                withIntermediateDirectories: true
            )
            // 读-改-写，保留未来可能新增的其它键；写齐 user/pass/source/url。
            var dict: [String: Any] = [:]
            let path = dataDir + "/credentials.json"
            if let existing = FileManager.default.contents(atPath: path),
               let parsed = try? JSONSerialization.jsonObject(with: existing) as? [String: Any] {
                dict = parsed
            }
            dict["opencode_user"] = opencodeUser
            dict["opencode_pass"] = opencodePass
            dict["opencode_source"] = opencodeSource.rawValue
            dict["opencode_url"] = opencodeSource == .externalHttp ? opencodeURL : ""

            let data = try JSONSerialization.data(
                withJSONObject: dict,
                options: [.prettyPrinted, .sortedKeys]
            )
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
            try FileManager.default.setAttributes(
                [.posixPermissions: 0o600],
                ofItemAtPath: path
            )
            savedOpenCodeUser = opencodeUser
            savedOpenCodePass = opencodePass
            savedOpenCodeSource = opencodeSource
            savedOpenCodeURL = opencodeURL.trimmingCharacters(in: .whitespacesAndNewlines)
            onCredentialsChanged()
            credentialsFeedback = .success(L10n.savedRestarting)
        } catch {
            credentialsFeedback = .failure(
                String(format: L10n.saveFailed, error.localizedDescription)
            )
        }
    }

    /// 验证当前 endpoint：先纯解析，再做 no-auth/authed health 校验（T02 算法）。
    func validateEndpoint() {
        guard canValidateEndpoint else {
            endpointValidation = .idle
            return
        }
        let config = OpenCodeEndpointConfig(
            source: opencodeSource,
            url: opencodeURL,
            username: opencodeUser,
            password: opencodePass
        )
        switch OpenCodeEndpointResolver.resolve(config) {
        case .failure(let err):
            endpointValidation = .failed(err.description)
        case .success(let endpoint):
            endpointValidation = .validating
            let validator = OpenCodeHealthValidator()
            Task { [weak self] in
                let result = await validator.validate(endpoint)
                await MainActor.run {
                    guard let self else { return }
                    switch result {
                    case .success(let ep):
                        if ep.legacyInsecureUnverified {
                            self.endpointValidation = .warning(L10n.opencodeLegacyInsecureWarning)
                        } else {
                            self.endpointValidation = .valid
                        }
                    case .failure(let err):
                        self.endpointValidation = .failed(err.description)
                    }
                }
            }
        }
    }

    func loadDisplayName() {
        guard let client = managementAPIClient else { return }
        Task {
            do {
                let status = try await client.getStatus()
                if let name = status.displayName {
                    displayName = name
                    savedDisplayName = name
                }
            } catch {
                displayNameFeedback = .failure(error.localizedDescription)
            }
        }
    }

    func saveDisplayName() {
        let trimmed = displayName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let client = managementAPIClient else { return }
        displayNameFeedback = .saving
        Task {
            do {
                try await client.updateDisplayName(trimmed)
                displayName = trimmed
                savedDisplayName = trimmed
                displayNameFeedback = .success(L10n.nameUpdated)
            } catch {
                displayNameFeedback = .failure(
                    String(format: L10n.saveFailed, error.localizedDescription)
                )
            }
        }
    }
}
