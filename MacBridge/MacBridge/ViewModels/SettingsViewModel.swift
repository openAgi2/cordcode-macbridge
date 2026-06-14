import Foundation

enum SaveFeedback: Equatable {
    case idle
    case saving
    case success(String)
    case failure(String)
}

@MainActor
class SettingsViewModel: ObservableObject {
    @Published var opencodeUser = ""
    @Published var opencodePass = ""
    @Published var displayName = ""
    @Published var displayNameFeedback: SaveFeedback = .idle
    @Published var credentialsFeedback: SaveFeedback = .idle

    private var savedOpenCodeUser = ""
    private var savedOpenCodePass = ""
    private var savedDisplayName = ""
    private let dataDir: String
    var onCredentialsChanged: (() -> Void)
    var managementAPIClient: ManagementAPIClient?

    var isCredentialsDirty: Bool {
        opencodeUser != savedOpenCodeUser || opencodePass != savedOpenCodePass
    }

    var isDisplayNameDirty: Bool {
        let trimmed = displayName.trimmingCharacters(in: .whitespacesAndNewlines)
        return !trimmed.isEmpty && trimmed != savedDisplayName
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
            return
        }
        opencodeUser = json["opencode_user"] as? String ?? ""
        opencodePass = json["opencode_pass"] as? String ?? ""
        savedOpenCodeUser = opencodeUser
        savedOpenCodePass = opencodePass
    }

    func regeneratePassword() {
        opencodePass = OpenCodeCredentialsGenerator.generatePassword()
        credentialsFeedback = .idle
    }

    func saveCredentials() {
        guard !opencodeUser.isEmpty, !opencodePass.isEmpty else { return }
        credentialsFeedback = .saving

        do {
            try FileManager.default.createDirectory(
                atPath: dataDir,
                withIntermediateDirectories: true
            )
            let data = try JSONSerialization.data(
                withJSONObject: [
                    "opencode_user": opencodeUser,
                    "opencode_pass": opencodePass,
                ],
                options: [.prettyPrinted, .sortedKeys]
            )
            let path = dataDir + "/credentials.json"
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
            try FileManager.default.setAttributes(
                [.posixPermissions: 0o600],
                ofItemAtPath: path
            )
            savedOpenCodeUser = opencodeUser
            savedOpenCodePass = opencodePass
            onCredentialsChanged()
            credentialsFeedback = .success(L10n.savedRestarting)
        } catch {
            credentialsFeedback = .failure(
                String(format: L10n.saveFailed, error.localizedDescription)
            )
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
