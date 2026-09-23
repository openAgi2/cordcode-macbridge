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
    /// dsh-web 座位动作状态（2026-09-22 方案：安装/启动按钮与字幕的数据源）。
    @Published var dshWebSeatAction = DSHWebSeatActionState()

    private var apiClient: ManagementAPIClient?
    private var refreshTask: Task<Void, Never>?
    private var dshWebPollTask: Task<Void, Never>?

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
        // DeepSeek 行不可用时顺带刷新座位动作状态（npmFound 决定按钮文案）。
        await refreshDSHWebSeatAction()
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
            if id == "dsh-web" {
                await refreshDSHWebSeatAction()
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

    // MARK: - dsh-web 座位动作（2026-09-22 方案 §2.1/§2.2/§5）

    private var dshWebAgent: BackendAgentStatus? {
        agents.first { $0.kind.lowercased() == "deepseek-web" || $0.id == "dsh-web" }
    }

    /// DeepSeek 行不可用时刷新座位动作状态：npmFound 决定按钮是「安装」还是
    /// 「需要 Node.js」，错误/说明字段是行字幕。
    func refreshDSHWebSeatAction() async {
        guard let client = apiClient, let dsh = dshWebAgent, !dsh.isAvailable else { return }
        if let state = try? await client.dshWebSeatActionState() {
            dshWebSeatAction = state
        }
    }

    /// 点「安装」：kick 后立刻进入本地「安装中」态，轮询收口后刷新行状态。
    func installDSHWeb() async {
        guard let client = apiClient else { return }
        let kick: DSHWebSeatActionKick
        do {
            kick = try await client.installDSHWeb()
        } catch {
            errorMessage = String(format: L10n.failedTestAgent, error.localizedDescription)
            return
        }
        switch kick.status {
        case "started":
            dshWebSeatAction = DSHWebSeatActionState(installing: true, npmFound: true)
            startDSHWebActionPolling()
        default:
            // node_missing / not_needed / already_installing：刷新真实状态。
            await refreshDSHWebSeatAction()
            await loadAgents(showLoading: false)
        }
    }

    /// 点「启动」：kick 后立刻进入本地「启动中」态，轮询收口后刷新行状态。
    func startDSHWebSeat() async {
        guard let client = apiClient else { return }
        let kick: DSHWebSeatActionKick
        do {
            kick = try await client.startDSHWebSeat()
        } catch {
            errorMessage = String(format: L10n.failedTestAgent, error.localizedDescription)
            return
        }
        switch kick.status {
        case "started":
            dshWebSeatAction = DSHWebSeatActionState(starting: true, npmFound: dshWebSeatAction.npmFound)
            startDSHWebActionPolling()
        default:
            await refreshDSHWebSeatAction()
            await loadAgents(showLoading: false)
        }
    }

    /// 轮询动作状态直到安装/启动收口（npm 预算 10 分钟，上限 11 分钟防悬挂），
    /// 然后刷新行状态与动作状态。行上的「安装中/启动中」由 dshWebSeatAction 驱动。
    private func startDSHWebActionPolling() {
        dshWebPollTask?.cancel()
        dshWebPollTask = Task { [weak self] in
            let deadline = Date().addingTimeInterval(11 * 60)
            while !Task.isCancelled && Date() < deadline {
                guard let self, let client = self.apiClient else { return }
                if let state = try? await client.dshWebSeatActionState() {
                    self.dshWebSeatAction = state
                    if !state.installing && !state.starting {
                        await self.loadAgents(showLoading: false)
                        await self.refreshDSHWebSeatAction()
                        return
                    }
                }
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
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
