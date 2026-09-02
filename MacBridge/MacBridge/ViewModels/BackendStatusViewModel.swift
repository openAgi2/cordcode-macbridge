import Combine
import Foundation

/// 单个后端的状态信息，用于 UI 展示
struct BackendAgentStatus: Identifiable, Equatable {
    let id: String
    let displayName: String
    let kind: String
    let status: String
    let reason: String?
    let isRefreshing: Bool
    let requiresPollingForExternalTurns: Bool

    /// 用户友好的状态文案
    var displayStatus: String {
        BackendStatusText.display(status)
    }

    /// 状态是否为可用
    var isAvailable: Bool { status == "available" }
}

enum CodexDesktopPairingError: LocalizedError {
    case runtimeNotReady

    var errorDescription: String? {
        switch self {
        case .runtimeNotReady:
            return L10n.codexDesktopPairRuntimeNotReady
        }
    }
}

/// 后端管理页 ViewModel
@MainActor
class BackendStatusViewModel: ObservableObject {
    @Published var agents: [BackendAgentStatus] = []
    @Published var isLoading = false
    @Published var errorMessage: String?
    @Published var isShowingStaleResults = false

    private var apiClient: ManagementAPIClient?
    private var refreshTask: Task<Void, Never>?

    /// 配置 API 客户端
    func configure(apiClient: ManagementAPIClient) {
        self.apiClient = apiClient
        startAvailabilityPolling()
    }

    /// Codex Desktop restore is async. Keep GET /internal/agents until that row becomes available.
    private func startAvailabilityPolling() {
        refreshTask?.cancel()
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                guard let self, !Task.isCancelled else { return }
                let recovering = self.agents.contains {
                    ($0.id == "codex-remote" || $0.kind.lowercased() == "codex-remote") && !$0.isAvailable
                }
                guard recovering else { continue }
                await self.loadAgents(showLoading: false)
            }
        }
    }

    /// 从 management API 加载后端列表
    func loadAgents(showLoading: Bool = true) async {
        guard let client = apiClient else { return }
        if showLoading {
            isLoading = true
        }
        errorMessage = nil
        do {
            let agentList = try await client.getAgents()
            agents = agentList.map { agent in
                BackendAgentStatus(
                    id: agent.id,
                    displayName: agent.displayName,
                    kind: agent.kind,
                    status: agent.status,
                    reason: agent.reason,
                    isRefreshing: false,
                    requiresPollingForExternalTurns: agent.requiresPollingForExternalTurns
                )
            }
            isShowingStaleResults = false
        } catch {
            if agents.isEmpty {
                errorMessage = String(format: L10n.failedLoadAgents, error.localizedDescription)
                isShowingStaleResults = false
            } else {
                errorMessage = String(format: L10n.showingLastAgentResults, error.localizedDescription)
                isShowingStaleResults = true
            }
        }
        isLoading = false
    }

    /// 手动刷新所有后端检测状态
    func refreshAgents() async {
        guard let client = apiClient else { return }
        isLoading = true
        errorMessage = nil
        do {
            let agentList = try await client.refreshAgents()
            agents = agentList.map { agent in
                BackendAgentStatus(
                    id: agent.id,
                    displayName: agent.displayName,
                    kind: agent.kind,
                    status: agent.status,
                    reason: agent.reason,
                    isRefreshing: false,
                    requiresPollingForExternalTurns: agent.requiresPollingForExternalTurns
                )
            }
            isShowingStaleResults = false
        } catch {
            if agents.isEmpty {
                errorMessage = String(format: L10n.failedRefreshAgents, error.localizedDescription)
                isShowingStaleResults = false
            } else {
                errorMessage = String(format: L10n.showingLastAgentResults, error.localizedDescription)
                isShowingStaleResults = true
            }
        }
        isLoading = false
    }

    /// 测试单个后端的连通性
    func testAgent(id: String) async {
        guard let client = apiClient else { return }
        agents = agents.map { agent in
            if agent.id == id {
                return BackendAgentStatus(id: agent.id, displayName: agent.displayName, kind: agent.kind, status: agent.status, reason: agent.reason, isRefreshing: true, requiresPollingForExternalTurns: agent.requiresPollingForExternalTurns)
            }
            return agent
        }
        do {
            let result = try await client.testAgent(id)
            agents = agents.map { agent in
                if agent.id == id {
                    return BackendAgentStatus(id: result.id, displayName: result.displayName, kind: result.kind, status: result.status, reason: result.reason, isRefreshing: false, requiresPollingForExternalTurns: result.requiresPollingForExternalTurns)
                }
                return agent
            }
        } catch {
            errorMessage = String(format: L10n.failedTestAgent, error.localizedDescription)
            isShowingStaleResults = !agents.isEmpty
            agents = agents.map { agent in
                if agent.id == id {
                    return BackendAgentStatus(id: agent.id, displayName: agent.displayName, kind: agent.kind, status: agent.status, reason: agent.reason, isRefreshing: false, requiresPollingForExternalTurns: agent.requiresPollingForExternalTurns)
                }
                return agent
            }
        }
    }

    /// 是否所有后端都不可用
    var allUnavailable: Bool {
        !agents.isEmpty && agents.allSatisfy { !$0.isAvailable }
    }

    func startCodexDesktopPairing() async throws -> CodexRemotePairingStatus {
        guard let client = apiClient else {
            throw CodexDesktopPairingError.runtimeNotReady
        }
        return try await client.startCodexRemotePairing()
    }

    func submitCodexDesktopPairingCode(_ code: String) async throws -> CodexRemotePairingStatus {
        guard let client = apiClient else {
            throw CodexDesktopPairingError.runtimeNotReady
        }
        return try await client.submitCodexRemotePairingCode(code)
    }

    func codexDesktopPairingStatus() async throws -> CodexRemotePairingStatus {
        guard let client = apiClient else {
            throw CodexDesktopPairingError.runtimeNotReady
        }
        return try await client.codexRemotePairingStatus()
    }
}
