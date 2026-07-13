import SwiftUI

/// 第二轮 UI 升级：AI 工具分组状态行（非卡片）。
/// 用于 WorkspaceView 的 AI 工具列表。
struct AgentStatusRow: View {
    let agent: BackendAgentStatus
    let onAction: (() -> Void)?

    var body: some View {
        HStack(spacing: 9) {
            StatusIndicator(
                systemImage: agent.isAvailable ? "checkmark.circle.fill" : "exclamationmark.triangle.fill",
                color: agent.isAvailable ? .green : .orange
            )
            Text(agent.displayName)
                .font(.body)
            Spacer()
            Text(agent.displayStatus)
                .foregroundStyle(agent.isAvailable ? Color.secondary : Color.orange)

            if !agent.isAvailable, let onAction = onAction {
                Button(L10n.workspaceRecheck, action: onAction)
                    .buttonStyle(.bordered)
                    .controlSize(.small)
            }
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
        .onTapGesture {
            if !agent.isAvailable, let onAction = onAction {
                onAction()
            }
        }
    }
}