import Combine
import Foundation

/// Bridge 状态 ViewModel，绑定到 RuntimeManager
@MainActor
class BridgeStatusViewModel: ObservableObject {
    @Published var status: BridgeStatus = .starting
    @Published var statusText: String = "Starting..."
    @Published var agents: [AgentInfo] = []
    @Published var lastError: String?
    @Published var managementStatus: ManagementStatus?
    @Published var relayConfigured: Bool?
    @Published var overviewDataError: String?
    @Published var isLoadingOverviewData = false

    private(set) var lastOverviewRefreshAt: Date?

    /// 绑定到 RuntimeManager 的 @Published 属性
    var runtimeManager: RuntimeManager? {
        didSet {
            cancellables.removeAll()
            bindToManager()
        }
    }

    private var cancellables = Set<AnyCancellable>()
    private var apiClient: OverviewAPIProviding?
    private var overviewRetryTask: Task<Void, Never>?

    var bridgePort: Int? {
        runtimeManager?.config.port
    }

    func configure(apiClient: ManagementAPIClient) {
        self.apiClient = apiClient
    }

    func configure(apiClient: OverviewAPIProviding) {
        self.apiClient = apiClient
    }

    func refreshOverviewData(force: Bool = true, isRetry: Bool = false) async {
        guard let apiClient else { return }
        if !force,
           let lastOverviewRefreshAt,
           Date().timeIntervalSince(lastOverviewRefreshAt) < 30 {
            return
        }
        guard !isLoadingOverviewData else { return }

        isLoadingOverviewData = true
        overviewDataError = nil

        async let statusResult = Self.capture { try await apiClient.getStatus() }
        async let remoteResult = Self.capture { try await apiClient.getRemoteStatus() }
        let (status, remote) = await (statusResult, remoteResult)

        var errors: [String] = []
        switch status {
        case .success(let value):
            managementStatus = value
        case .failure(let error):
            errors.append(String(format: L10n.overviewRuntimeDetailsFailed, error.localizedDescription))
        }
        switch remote {
        case .success(let value):
            relayConfigured = value.relay?.configured
            overviewRetryTask?.cancel()
            overviewRetryTask = nil
        case .failure(let error):
            relayConfigured = nil
            errors.append(String(format: L10n.overviewRelayStatusFailed, error.localizedDescription))
            if !isRetry {
                scheduleOverviewRetry()
            }
        }

        if errors.isEmpty {
            lastOverviewRefreshAt = Date()
        } else {
            overviewDataError = errors.joined(separator: "\n")
        }
        isLoadingOverviewData = false
    }

    private func scheduleOverviewRetry() {
        guard overviewRetryTask == nil else { return }
        overviewRetryTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled, let self else { return }
            self.overviewRetryTask = nil
            await self.refreshOverviewData(isRetry: true)
        }
    }

    private func bindToManager() {
        guard let manager = runtimeManager else { return }

        // 立即同步当前值（避免 Combine publisher 只传变化不传初始值）
        status = manager.status
        statusText = manager.statusText
        agents = manager.agents
        lastError = manager.lastError

        manager.$status
            .removeDuplicates()
            .receive(on: DispatchQueue.main)
            .sink { [weak self] newStatus in
                guard let self else { return }
                let previousStatus = self.status
                self.status = newStatus
                guard previousStatus != newStatus,
                      newStatus == .ready || newStatus == .readyNoAgents else {
                    return
                }
                Task { await self.refreshOverviewData() }
            }
            .store(in: &cancellables)
        manager.$statusText
            .receive(on: DispatchQueue.main)
            .assign(to: &$statusText)
        manager.$agents
            .receive(on: DispatchQueue.main)
            .assign(to: &$agents)
        manager.$lastError
            .receive(on: DispatchQueue.main)
            .assign(to: &$lastError)
    }

    nonisolated static func formatUptime(_ raw: String?) -> String? {
        guard let raw, !raw.isEmpty else { return nil }
        guard let seconds = parseGoDuration(raw) else { return raw }
        if seconds < 60 {
            return L10n.overviewUptimeUnderMinute
        }
        let minutes = Int(seconds / 60)
        if minutes < 60 {
            return String(format: L10n.overviewUptimeMinutes, minutes)
        }
        return String(
            format: L10n.overviewUptimeHoursMinutes,
            minutes / 60,
            minutes % 60
        )
    }

    nonisolated static func parseGoDuration(_ raw: String) -> TimeInterval? {
        let pattern = #"^(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?(?:(\d+(?:\.\d+)?)s)?$"#
        guard let expression = try? NSRegularExpression(pattern: pattern),
              let match = expression.firstMatch(
                in: raw,
                range: NSRange(raw.startIndex..., in: raw)
              ),
              match.range.length == raw.utf16.count else {
            return nil
        }
        func value(at index: Int) -> Double {
            let range = match.range(at: index)
            guard range.location != NSNotFound,
                  let swiftRange = Range(range, in: raw) else {
                return 0
            }
            return Double(raw[swiftRange]) ?? 0
        }
        let seconds = value(at: 1) * 3600 + value(at: 2) * 60 + value(at: 3)
        return seconds > 0 || raw == "0s" ? seconds : nil
    }

    private nonisolated static func capture<T>(
        _ operation: @escaping () async throws -> T
    ) async -> Result<T, Error> {
        do {
            return .success(try await operation())
        } catch {
            return .failure(error)
        }
    }
}
