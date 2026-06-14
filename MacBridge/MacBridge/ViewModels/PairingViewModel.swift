import Combine
import Foundation

enum PairingUIState: Equatable {
    case idle
    case creating
    case waitingForClaim(sessionId: String, manualCode: String, qrPayload: String)
    case claimed(deviceName: String, platform: String)
    case approved
    case rejected
    case expired
    case error(String)
}

@MainActor
class PairingViewModel: ObservableObject {
    @Published var uiState: PairingUIState = .idle
    @Published var qrPayload = ""
    @Published var manualCode = ""
    @Published var claimedDeviceName = ""
    @Published var claimedPlatform = ""
    @Published private(set) var remainingSeconds: Int?

    var onApproved: (() async -> Void)?

    private var apiClient: PairingAPIProviding?
    private var pollingTimer: Timer?
    private var countdownTask: Task<Void, Never>?
    private var currentSessionId: String?
    private var expiresAt: Date?
    private var isStarting = false

    func configure(apiClient: ManagementAPIClient) {
        self.apiClient = apiClient
    }

    func configure(apiClient: PairingAPIProviding) {
        self.apiClient = apiClient
    }

    func startPairing() {
        guard !isStarting else { return }
        guard let client = apiClient else {
            uiState = .error(L10n.pairingManagementUnavailable)
            return
        }

        stopPairingActivity()
        isStarting = true
        uiState = .creating

        Task {
            defer { isStarting = false }
            do {
                let session = try await client.createPairing()
                guard let parsedExpiry = Self.parseExpiry(session.expiresAt) else {
                    uiState = .error(L10n.pairingInvalidExpiry)
                    return
                }
                currentSessionId = session.id
                manualCode = session.manualCode
                qrPayload = session.qrPayload
                uiState = .waitingForClaim(
                    sessionId: session.id,
                    manualCode: session.manualCode,
                    qrPayload: session.qrPayload
                )
                beginCountdown(expiresAt: parsedExpiry)
                guard uiState != .expired else { return }
                startPolling()
            } catch {
                transitionToError(error.localizedDescription)
            }
        }
    }

    func approve() {
        guard let client = apiClient, let sessionId = currentSessionId else { return }
        Task {
            do {
                _ = try await client.approvePairing(sessionId)
                stopPairingActivity()
                uiState = .approved
                await onApproved?()
            } catch {
                transitionToError(error.localizedDescription)
            }
        }
    }

    func reject() {
        guard let client = apiClient, let sessionId = currentSessionId else { return }
        Task {
            do {
                try await client.rejectPairing(sessionId)
                stopPairingActivity()
                uiState = .rejected
            } catch {
                transitionToError(error.localizedDescription)
            }
        }
    }

    func reset() {
        stopPairingActivity()
        currentSessionId = nil
        expiresAt = nil
        remainingSeconds = nil
        qrPayload = ""
        manualCode = ""
        claimedDeviceName = ""
        claimedPlatform = ""
        uiState = .idle
        isStarting = false
    }

    func updateRemainingTime(now: Date = Date()) {
        guard let expiresAt else {
            remainingSeconds = nil
            return
        }
        let seconds = max(0, Int(ceil(expiresAt.timeIntervalSince(now))))
        remainingSeconds = seconds
        if seconds == 0 {
            expireSession()
        }
    }

    var hasActivePairingTasks: Bool {
        pollingTimer != nil || countdownTask != nil
    }

    func beginCountdown(expiresAt: Date) {
        self.expiresAt = expiresAt
        updateRemainingTime()
        guard uiState != .expired else { return }
        startCountdown()
    }

    func expireSession() {
        guard uiState != .expired else { return }
        stopPairingActivity()
        remainingSeconds = 0
        uiState = .expired
    }

    deinit {
        pollingTimer?.invalidate()
        countdownTask?.cancel()
    }

    private func startPolling() {
        pollingTimer?.invalidate()
        pollingTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            Task { @MainActor in
                await self?.pollStatus()
            }
        }
    }

    private func startCountdown() {
        countdownTask?.cancel()
        countdownTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled, let self else { return }
                self.updateRemainingTime()
                if self.uiState == .expired { return }
            }
        }
    }

    private func stopPairingActivity() {
        pollingTimer?.invalidate()
        pollingTimer = nil
        countdownTask?.cancel()
        countdownTask = nil
    }

    private func pollStatus() async {
        guard let client = apiClient, let sessionId = currentSessionId else { return }
        do {
            let status = try await client.getPairingStatus(sessionId)
            await applyPairingStatus(status)
        } catch {
            transitionToError(error.localizedDescription)
        }
    }

    func applyPairingStatus(_ status: PairingSessionStatus) async {
        switch status.state {
        case "claimed":
            claimedDeviceName = status.claimingDeviceName ?? L10n.pairingUnknownDevice
            claimedPlatform = status.claimingPlatform ?? ""
            stopPairingActivity()
            uiState = .claimed(deviceName: claimedDeviceName, platform: claimedPlatform)
        case "approved":
            stopPairingActivity()
            uiState = .approved
            await onApproved?()
        case "rejected":
            stopPairingActivity()
            uiState = .rejected
        case "expired":
            expireSession()
        default:
            break
        }
    }

    private func transitionToError(_ message: String) {
        stopPairingActivity()
        uiState = .error(message)
    }

    static func parseExpiry(_ raw: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: raw) ?? ISO8601DateFormatter().date(from: raw)
    }
}
