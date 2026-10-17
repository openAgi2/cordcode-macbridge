import Foundation

protocol WorkspaceSnapshotAPIProviding {
    func getWorkspaceSnapshot() async throws -> WorkspaceSnapshot
}

/// Owns the read-only 3-second workspace observation cadence. It never persists online
/// state and never becomes a second process/device truth source.
@MainActor
final class WorkspaceSnapshotStore: ObservableObject {
    enum Phase: Equatable {
        case idle
        case available(WorkspaceSnapshot)
        case diagnostic(Kind)
    }

    enum Kind: Equatable {
        case network
        case unauthorized
        case decode
        case identityMismatch
        case stale
    }

    @Published private(set) var phase: Phase = .idle
    @Published private(set) var lastUpdatedAt: Date?
    @Published private(set) var lastDiagnosticDetail: String?
    /// False after a failed observation, identity mismatch, unauthorized response, or
    /// `stopPolling()`. Window deactivation is not a failure: last `.available` stays
    /// current while sampling is paused, matching TopologyMonitor.
    @Published private(set) var isObservationCurrent = false

    private let cadence: TimeInterval
    private let staleAfterFailures: Int
    private let expectedIdentity: () -> WorkspaceSnapshot.RuntimeIdentity?
    private var apiClient: WorkspaceSnapshotAPIProviding?
    private var pollTask: Task<Void, Never>?
    private var fetchGeneration = 0
    private var isAppActive = true

    init(
        cadence: TimeInterval = 3,
        staleAfterFailures: Int = 3,
        expectedIdentity: @escaping () -> WorkspaceSnapshot.RuntimeIdentity? = { nil }
    ) {
        self.cadence = cadence
        self.staleAfterFailures = max(1, staleAfterFailures)
        self.expectedIdentity = expectedIdentity
    }

    func configure(apiClient: WorkspaceSnapshotAPIProviding) {
        self.apiClient = apiClient
        startPolling()
    }

    func startPolling() {
        guard pollTask == nil else { return }
        fetchGeneration += 1
        let generation = fetchGeneration
        pollTask = Task { [weak self] in
            await self?.pollLoop(generation: generation)
        }
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
        invalidateObservation()
    }

    /// Pause/resume sampling with the window. Resigning active only stops polls;
    /// last occupancy, phase, and `isObservationCurrent` stay. Runtime keeps running
    /// behind other apps, so Cmd-Tab must not flash 状态未知 / clocks / dashed wires.
    func setAppActive(_ active: Bool) {
        isAppActive = active
    }

    func invalidateObservation() {
        isObservationCurrent = false
        if phase.isAvailable {
            phase = .diagnostic(.stale)
            lastDiagnosticDetail = "工作区快照不再代表当前运行状态"
        }
    }

    private func pollLoop(generation: Int) async {
        var consecutiveFailures = 0
        while !Task.isCancelled {
            guard isAppActive else {
                try? await Task.sleep(nanoseconds: 100_000_000)
                continue
            }
            let success = await pollOnce(generation: generation)
            consecutiveFailures = success ? 0 : consecutiveFailures + 1
            if !success && consecutiveFailures >= staleAfterFailures && phase.isAvailable {
                phase = .diagnostic(.stale)
                lastDiagnosticDetail = "连续 \(consecutiveFailures) 次未获得工作区快照"
            }
            try? await Task.sleep(nanoseconds: UInt64(max(cadence, 0.01) * 1_000_000_000))
        }
    }

    private func pollOnce(generation: Int) async -> Bool {
        guard let apiClient else { return false }
        do {
            let snapshot = try await apiClient.getWorkspaceSnapshot()
            guard generation == fetchGeneration, !Task.isCancelled else { return false }
            guard snapshot.schemaVersion == 1 else {
                isObservationCurrent = false
                phase = .diagnostic(.decode)
                lastDiagnosticDetail = "未知工作区快照 schema \(snapshot.schemaVersion)"
                return false
            }
            guard let expected = expectedIdentity() else {
                isObservationCurrent = false
                phase = .diagnostic(.identityMismatch)
                lastDiagnosticDetail = "尚未获得当前 runtime identity"
                return false
            }
            if snapshot.runtimeIdentity != expected {
                isObservationCurrent = false
                phase = .diagnostic(.identityMismatch)
                lastDiagnosticDetail = "快照 runtime identity 与当前进程不一致"
                return false
            }
            phase = .available(snapshot)
            isObservationCurrent = true
            lastUpdatedAt = Date()
            lastDiagnosticDetail = nil
            return true
        } catch is CancellationError {
            return false
        } catch ManagementAPIClient.ManagementError.httpError(let statusCode) where statusCode == 401 {
            guard generation == fetchGeneration, !Task.isCancelled else { return false }
            isObservationCurrent = false
            phase = .diagnostic(.unauthorized)
            lastDiagnosticDetail = "Management API 未授权"
            return false
        } catch let error as DecodingError {
            guard generation == fetchGeneration, !Task.isCancelled else { return false }
            isObservationCurrent = false
            phase = .diagnostic(.decode)
            lastDiagnosticDetail = error.localizedDescription
            return false
        } catch {
            guard generation == fetchGeneration, !Task.isCancelled else { return false }
            isObservationCurrent = false
            if !phase.isAvailable {
                phase = .diagnostic(.network)
                lastDiagnosticDetail = error.localizedDescription
            }
            return false
        }
    }
}

private extension WorkspaceSnapshotStore.Phase {
    var isAvailable: Bool {
        if case .available = self { return true }
        return false
    }
}
