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
    let installGuidance: WorkspaceSnapshot.InstallGuidance?
    let snapshotVersion: String?
    let versionCheck: WorkspaceSnapshot.VersionCheck?
    let installation: WorkspaceSnapshot.Installation?
    let isActive: Bool
    let isObservationCurrent: Bool

    init(
        id: String,
        displayName: String,
        kind: String,
        status: String,
        reason: String?,
        isRefreshing: Bool,
        requiresPollingForExternalTurns: Bool,
        installGuidance: WorkspaceSnapshot.InstallGuidance? = nil,
        snapshotVersion: String? = nil,
        versionCheck: WorkspaceSnapshot.VersionCheck? = nil,
        installation: WorkspaceSnapshot.Installation? = nil,
        isActive: Bool = true,
        isObservationCurrent: Bool = true
    ) {
        self.id = id
        self.displayName = displayName
        self.kind = kind
        self.status = status
        self.reason = reason
        self.isRefreshing = isRefreshing
        self.requiresPollingForExternalTurns = requiresPollingForExternalTurns
        self.installGuidance = installGuidance
        self.snapshotVersion = snapshotVersion
        self.versionCheck = versionCheck
        self.installation = installation
        self.isActive = isActive
        self.isObservationCurrent = isObservationCurrent
    }

    /// 用户友好的状态文案
    var displayStatus: String {
        BackendStatusText.display(status)
    }

    /// 状态是否为可用
    var isAvailable: Bool { status == "available" }

    func with(isRefreshing: Bool) -> BackendAgentStatus {
        BackendAgentStatus(
            id: id,
            displayName: displayName,
            kind: kind,
            status: status,
            reason: reason,
            isRefreshing: isRefreshing,
            requiresPollingForExternalTurns: requiresPollingForExternalTurns,
            installGuidance: installGuidance,
            snapshotVersion: snapshotVersion,
            versionCheck: versionCheck,
            installation: installation,
            isActive: isActive,
            isObservationCurrent: isObservationCurrent
        )
    }

    func merging(live: AgentInfo, isRefreshing: Bool) -> BackendAgentStatus {
        BackendAgentStatus(
            id: id,
            displayName: displayName,
            kind: kind,
            status: live.status,
            reason: live.reason,
            isRefreshing: isRefreshing,
            requiresPollingForExternalTurns: live.requiresPollingForExternalTurns,
            installGuidance: installGuidance,
            snapshotVersion: snapshotVersion,
            versionCheck: versionCheck,
            installation: installation,
            isActive: true,
            isObservationCurrent: isObservationCurrent
        )
    }
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
    /// Magpie occupancy: seats exist before any live list or snapshot arrives.
    /// GET /internal/agents never owns membership.
    @Published var agents: [BackendAgentStatus] = catalogOccupancy()
    @Published var isLoading = false
    @Published var errorMessage: String?
    @Published var isShowingStaleResults = false
    @Published var isRescanningVersions = false
    @Published var versionRescanError: String?
    @Published var isRescanningInventory = false
    @Published var inventoryRescanError: String?
    @Published private(set) var activeDrivers: Set<String> = []
    @Published private(set) var requestedDrivers: Set<String> = []
    @Published private(set) var selectionRevision: UInt64?
    @Published private(set) var workspaceRelay: WorkspaceSnapshot.Relay?
    @Published private(set) var isWorkspaceObservationCurrent = false
    /// dsh-web 座位动作状态（2026-09-22 方案：安装/启动按钮与字幕的数据源）。
    @Published var dshWebSeatAction = DSHWebSeatActionState()

    private var apiClient: ManagementAPIClient?
    private var refreshTask: Task<Void, Never>?
    private var dshWebPollTask: Task<Void, Never>?
    private var didStartInitialVersionRescan = false
    private var lastWorkspaceSnapshot: WorkspaceSnapshot?

    static func catalogOccupancy(existing: [BackendAgentStatus] = []) -> [BackendAgentStatus] {
        let existingByID = Dictionary(existing.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        return ProductAgentCatalog.products.map { product in
            let existing = existingByID[product.id]
            return BackendAgentStatus(
                id: product.id,
                displayName: existing?.displayName ?? product.name,
                kind: product.kind,
                status: existing?.status ?? "inactive",
                reason: existing?.reason,
                isRefreshing: existing?.isRefreshing ?? false,
                requiresPollingForExternalTurns: existing?.requiresPollingForExternalTurns ?? false,
                installGuidance: existing?.installGuidance,
                snapshotVersion: existing?.snapshotVersion,
                versionCheck: existing?.versionCheck,
                installation: existing?.installation,
                isActive: existing?.isActive ?? false,
                isObservationCurrent: existing?.isObservationCurrent ?? false
            )
        }
    }

    /// 配置 API 客户端
    func configure(apiClient: ManagementAPIClient) {
        self.apiClient = apiClient
        startAvailabilityPolling()
    }

    func applyWorkspaceSnapshotPhase(
        _ phase: WorkspaceSnapshotStore.Phase,
        isObservationCurrent: Bool
    ) {
        isWorkspaceObservationCurrent = isObservationCurrent
        guard isObservationCurrent, case .available(let snapshot) = phase else {
            // Magpie rebuild keeps catalog/wires while they stay. A selection
            // restart must not nil the last relay or collapse rows. Device
            // presence is still unknown until the next current snapshot.
            agents = agents.map { agent in
                BackendAgentStatus(
                    id: agent.id,
                    displayName: agent.displayName,
                    kind: agent.kind,
                    status: agent.status,
                    reason: agent.reason,
                    isRefreshing: agent.isRefreshing,
                    requiresPollingForExternalTurns: agent.requiresPollingForExternalTurns,
                    installGuidance: agent.installGuidance,
                    snapshotVersion: agent.snapshotVersion,
                    versionCheck: agent.versionCheck,
                    installation: agent.installation,
                    isActive: agent.isActive,
                    isObservationCurrent: false
                )
            }
            return
        }
        lastWorkspaceSnapshot = snapshot
        activeDrivers = Set(snapshot.activeDrivers)
        requestedDrivers = Set(snapshot.requestedDrivers)
        selectionRevision = snapshot.selectionRevision
        workspaceRelay = snapshot.relay
        rebuildProductAgents(from: snapshot)
        startInitialVersionRescanIfNeeded(snapshot: snapshot)
    }

    private func rebuildProductAgents(from snapshot: WorkspaceSnapshot?) {
        guard let snapshot else { return }
        applyCatalogRows(
            liveAgents: snapshot.agents.compactMap(\.readiness),
            observations: Dictionary(
                snapshot.agents.map { ($0.id, $0) },
                uniquingKeysWith: { first, _ in first }
            ),
            observationCurrent: true
        )
    }

    /// Magpie rebuild: "agents stay where they are while they stay."
    /// GET /internal/agents only names live backends. Occupancy is the product
    /// catalog; live status is paint, not membership.
    func applyLiveAgentList(_ liveAgents: [AgentInfo]) {
        applyCatalogRows(
            liveAgents: liveAgents,
            observations: Dictionary(
                (lastWorkspaceSnapshot?.agents ?? []).map { ($0.id, $0) },
                uniquingKeysWith: { first, _ in first }
            ),
            observationCurrent: isWorkspaceObservationCurrent
        )
    }

    private func applyCatalogRows(
        liveAgents: [AgentInfo],
        observations: [String: WorkspaceSnapshot.Agent],
        observationCurrent: Bool
    ) {
        let existingByID = Dictionary(
            agents.map { ($0.id, $0) },
            uniquingKeysWith: { first, _ in first }
        )
        let liveByID = Dictionary(
            liveAgents.map { ($0.id, $0) },
            uniquingKeysWith: { _, last in last }
        )
        agents = ProductAgentCatalog.products.map { product in
            let existing = existingByID[product.id]
            let observation = observations[product.id]
            let live = liveByID[product.id] ?? liveAgents.first {
                $0.kind.lowercased() == product.kind.lowercased()
            }
            return BackendAgentStatus(
                id: product.id,
                displayName: existing?.displayName ?? live?.displayName ?? product.name,
                kind: product.kind,
                status: live?.status ?? existing?.status ?? "inactive",
                reason: live?.reason ?? existing?.reason,
                isRefreshing: existing?.isRefreshing ?? false,
                requiresPollingForExternalTurns: live?.requiresPollingForExternalTurns
                    ?? existing?.requiresPollingForExternalTurns
                    ?? false,
                installGuidance: observation?.installGuidance ?? existing?.installGuidance,
                snapshotVersion: observation?.version ?? existing?.snapshotVersion,
                versionCheck: observation?.versionCheck ?? existing?.versionCheck,
                installation: observation?.installation ?? existing?.installation,
                isActive: live != nil,
                isObservationCurrent: observationCurrent
            )
        }
    }

    private func startInitialVersionRescanIfNeeded(snapshot: WorkspaceSnapshot) {
        guard !didStartInitialVersionRescan else { return }
        let supportedIDs = snapshot.agents.filter { agent in
            agent.versionCheck.state == "unchecked" &&
                agent.installation.state == "installed" &&
                (agent.installation.installMethod == "npm" || agent.id == "grokbuild")
        }.map(\.id)
        guard !supportedIDs.isEmpty else { return }
        didStartInitialVersionRescan = true
        Task { await rescanVersions(agentIDs: supportedIDs) }
    }

    func rescanVersions(agentIDs: [String] = []) async {
        guard let client = apiClient, !isRescanningVersions else { return }
        isRescanningVersions = true
        versionRescanError = nil
        do {
            _ = try await client.rescanWorkspace(agentIDs: agentIDs, checkVersions: true)
        } catch {
            versionRescanError = error.localizedDescription
        }
        isRescanningVersions = false
    }

    func rescanInventory(agentIDs: [String] = []) async {
        guard let client = apiClient, !isRescanningInventory else { return }
        isRescanningInventory = true
        inventoryRescanError = nil
        do {
            _ = try await client.rescanWorkspace(agentIDs: agentIDs, checkVersions: false)
        } catch {
            inventoryRescanError = error.localizedDescription
        }
        isRescanningInventory = false
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
        // Occupied catalog is chrome. A selection restart must not flash a
        // loading replacement over seats that are already on screen.
        if showLoading, agents.allSatisfy({ $0.installation == nil && !$0.isActive }) {
            isLoading = true
        }
        errorMessage = nil
        do {
            let agentList = try await client.getAgents()
            applyLiveAgentList(agentList)
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
            applyLiveAgentList(agentList)
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
                return agent.with(isRefreshing: true)
            }
            return agent
        }
        do {
            let result = try await client.testAgent(id)
            agents = agents.map { agent in
                if agent.id == id {
                    return agent.merging(live: result, isRefreshing: false)
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
                    return agent.with(isRefreshing: false)
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
