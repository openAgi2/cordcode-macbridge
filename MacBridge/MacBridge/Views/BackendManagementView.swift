import SwiftUI

/// 后端管理页：展示各 AI 工具的检测状态和修复建议（紧凑行布局）
struct BackendManagementView: View {
    @ObservedObject var viewModel: BackendStatusViewModel
    @EnvironmentObject private var dependencies: AppDependencies
    @State private var showCodexDesktopPairing = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                // 标题 + 刷新
                HStack {
                    Text(L10n.aiTools)
                        .font(.title2)
                        .fontWeight(.semibold)
                    Spacer()
                    Button {
                        Task { await viewModel.refreshAgents() }
                    } label: {
                        HStack(spacing: 4) {
                            if viewModel.isLoading {
                                ProgressView()
                                    .controlSize(.small)
                            }
                            Image(systemName: "arrow.clockwise")
                            Text(L10n.refreshAll)
                        }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(viewModel.isLoading)
                }

                if let error = viewModel.errorMessage {
                    Label(error, systemImage: viewModel.isShowingStaleResults ? "exclamationmark.triangle" : "xmark.circle")
                        .font(.caption)
                        .foregroundColor(viewModel.isShowingStaleResults ? .orange : .red)
                }

                Divider()

                // 全后端未检测引导
                if viewModel.allUnavailable {
                    allUnavailableGuidance
                } else if viewModel.agents.isEmpty {
                    Text(L10n.noAiToolsConfigured)
                        .foregroundColor(.secondary)
                        .font(.subheadline)
                } else {
                    ForEach(viewModel.agents) { agent in
                        agentRow(agent)
                    }
                }

                Spacer()
            }
            .padding()
            .frame(maxWidth: 680)
        }
        .onAppear {
            if let url = dependencies.runtimeManager.managementURL,
               let token = dependencies.runtimeManager.managementToken,
               let client = try? ManagementAPIClient(baseURL: url, token: token) {
                viewModel.configure(apiClient: client)
            }
        }
        .task {
            await viewModel.loadAgents()
        }
        .sheet(isPresented: $showCodexDesktopPairing) {
            CodexDesktopPairingSheet(viewModel: viewModel)
        }
    }

    // MARK: - 全后端未检测引导

    private var allUnavailableGuidance: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                Image(systemName: "info.circle")
                    .foregroundColor(.blue)
                Text(L10n.allUnavailableGuidance)
                    .font(.body)
            }
            .padding(12)
            .background(RoundedRectangle(cornerRadius: 8).fill(Color.blue.opacity(0.08)))

            ForEach(viewModel.agents) { agent in
                agentRow(agent)
            }
        }
    }

    // MARK: - 紧凑行布局

    @ViewBuilder
    private func agentRow(_ agent: BackendAgentStatus) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                // 状态指示灯
                Circle()
                    .fill(agent.isAvailable ? Color.green : Color.orange)
                    .frame(width: 8, height: 8)

                // 后端名称
                Text(agent.displayName)
                    .font(.body)
                    .fontWeight(.medium)

                Spacer()

                // 状态标签
                Text(agent.displayStatus)
                    .font(.caption)
                    .foregroundColor(agent.isAvailable ? .green : .orange)

                if (agent.id == "codex-remote" || agent.kind.lowercased() == "codex-remote"), !agent.isAvailable {
                    Button(L10n.pairCodexDesktop) {
                        showCodexDesktopPairing = true
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }

                // 测试按钮
                Button(L10n.test) {
                    Task { await viewModel.testAgent(id: agent.id) }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(agent.isRefreshing)

                if agent.isRefreshing {
                    ProgressView()
                        .controlSize(.small)
                }
            }

            // reason + 下一步方向
            if !agent.isAvailable {
                Text(Self.nextStepGuidance(agent.reason, kind: agent.kind))
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .padding(.leading, 16)
            }

            // polling 说明
            if agent.isAvailable && agent.requiresPollingForExternalTurns {
                Text(L10n.externalTurnsPolling)
                    .font(.caption2)
                    .foregroundColor(.secondary)
                    .padding(.leading, 16)
            }
        }
        .padding(.vertical, 6)
        .padding(.horizontal, 4)
    }

    // MARK: - Reason 文案映射

    /// 将技术 reason 映射为下一步操作建议
    private static func nextStepGuidance(_ reason: String?, kind: String) -> String {
        guard let reason, !reason.isEmpty else {
            return L10n.checkDocsGuidance
        }
        if reason.contains("not found in PATH") {
            return String(format: L10n.notInstalled, kind)
        }
        if reason.contains("not running") {
            return L10n.serviceNotRunning
        }
        if reason.contains("not logged in") {
            return L10n.loginRequired
        }
        if kind.lowercased() == "codex-remote" || reason.contains("尚未配对") || reason.contains("ChatGPT Desktop") {
            return L10n.codexDesktopPairGuidance
        }
        if reason.contains("timed out") {
            return L10n.detectionTimedOut
        }
        if reason.contains("unreachable") {
            return L10n.cannotReachService
        }
        return reason
    }
}

#Preview {
    let vm = BackendStatusViewModel()
    vm.agents = [
        BackendAgentStatus(id: "claude", displayName: "Claude Code", kind: "claude_code", status: "available", reason: nil, isRefreshing: false, requiresPollingForExternalTurns: true),
        BackendAgentStatus(id: "codex", displayName: "Codex", kind: "codex", status: "service_not_running", reason: "Codex app-server not running on port 4141", isRefreshing: false, requiresPollingForExternalTurns: false),
        BackendAgentStatus(id: "opencode", displayName: "OpenCode", kind: "opencode", status: "not_detected", reason: "opencode CLI not found in PATH", isRefreshing: false, requiresPollingForExternalTurns: false),
    ]
    return BackendManagementView(viewModel: vm)
}
