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
    /// Flow C: web-specific QR (https URL for the phone's system camera). Empty until a session
    /// with relay configured is created; empty when relay is not configured.
    @Published var webQrPayload = ""
    @Published var manualCode = ""
    @Published var claimedDeviceName = ""
    @Published var claimedPlatform = ""
    @Published private(set) var remainingSeconds: Int?

    var onApproved: (() async -> Void)?

    /// 系统通知协调器(M1):claim 到达时发通知 + 一键 approve。
    /// 弱持有,避免循环。AppDependencies 在创建后注入。
    weak var notificationCoordinator: NotificationCoordinator?

    private var apiClient: PairingAPIProviding?
    private var pollingTimer: Timer?
    private var countdownTask: Task<Void, Never>?
    private var currentSessionId: String?
    private var expiresAt: Date?
    private var isStarting = false
    private var isPolling = false

    nonisolated static func webV2PairingURL(from webPairingURL: String) -> String? {
        guard var components = URLComponents(string: webPairingURL),
              components.scheme == "https" || components.scheme == "http",
              components.path == "/web/" || components.path == "/web" else {
            return nil
        }
        components.path = "/web-v2/"
        return components.string
    }

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
                webQrPayload = session.webQrPayload ?? ""
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
        webQrPayload = ""
        manualCode = ""
        claimedDeviceName = ""
        claimedPlatform = ""
        uiState = .idle
        isStarting = false
    }

    func getOrFetchWebPairingURL(isV2: Bool) async -> String? {
        var payload = webQrPayload
        if payload.isEmpty || remainingSeconds == nil || remainingSeconds! < 10 {
            guard let client = apiClient else { return nil }
            do {
                let session = try await client.createPairing()
                guard let parsedExpiry = Self.parseExpiry(session.expiresAt) else { return nil }
                currentSessionId = session.id
                manualCode = session.manualCode
                qrPayload = session.qrPayload
                webQrPayload = session.webQrPayload ?? ""
                payload = session.webQrPayload ?? ""
                beginCountdown(expiresAt: parsedExpiry)
                startPolling()
            } catch {
                return nil
            }
        }
        
        guard !payload.isEmpty else { return nil }
        
        if isV2 {
            return Self.webV2PairingURL(from: payload)
        } else {
            return payload
        }
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
        guard !isPolling, let client = apiClient, let sessionId = currentSessionId else { return }
        isPolling = true
        defer { isPolling = false }
        do {
            let status = try await client.getPairingStatus(sessionId)
            await applyPairingStatus(status)
        } catch let error where Self.isTransientPollingError(error) {
            // A single slow Relay round trip must not destroy the still-valid pairing session.
            // The next scheduled poll retries while the countdown remains authoritative.
            return
        } catch {
            transitionToError(error.localizedDescription)
        }
    }

    nonisolated static func isTransientPollingError(_ error: Error) -> Bool {
        (error as? URLError)?.code == .timedOut
    }

    func applyPairingStatus(_ status: PairingSessionStatus) async {
        switch status.state {
        case "claimed":
            claimedDeviceName = status.claimingDeviceName ?? L10n.pairingUnknownDevice
            claimedPlatform = status.claimingPlatform ?? ""
            stopPairingActivity()
            uiState = .claimed(deviceName: claimedDeviceName, platform: claimedPlatform)
            // M1: 发系统通知 + 一键 approve(把"用户必须盯着配对窗口"压到"系统通知几秒响应")。
            notificationCoordinator?.notifyPairingClaimed(
                deviceName: claimedDeviceName,
                platform: claimedPlatform
            )
        case "approved":
            stopPairingActivity()
            uiState = .approved
            notificationCoordinator?.clearPairingNotifications()
            await onApproved?()
        case "rejected":
            stopPairingActivity()
            uiState = .rejected
            notificationCoordinator?.clearPairingNotifications()
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
