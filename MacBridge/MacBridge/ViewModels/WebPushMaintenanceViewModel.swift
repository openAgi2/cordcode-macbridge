import Foundation

/// Web Push 维护状态（设置页高级 Tab）。decode/未知值 fail-closed 为 unknown：
/// 不得把加载失败或未知状态显示为 healthy，也不得在 unknown 下隐藏重置入口。
enum WebPushHealthState: Equatable {
    case healthy
    case misconfigured
    case unconfigured
    case unknown

    init(raw: String?) {
        switch raw {
        case "healthy": self = .healthy
        case "misconfigured": self = .misconfigured
        case "unconfigured": self = .unconfigured
        default: self = .unknown
        }
    }
}

/// 重置流程的显式阶段。只有 `.succeeded` 才代表恢复；失败必须保留错误文案。
enum WebPushResetPhase: Equatable {
    case idle
    case loadingStatus
    case ready
    case resetting
    case succeeded(removed: Int, fingerprint: String?)
    case failed(message: String)
}

/// 设置页「Web Push 维护」的可测试模型（方案 §12.3：状态判断、subscription 计数、
/// 确认后调用与错误呈现都收在 model 边界，View 只渲染）。
/// 语义约束：
/// - 不自动重置：reset() 只在用户确认后调用一次；
/// - 确认文案必须来自真实 subscriptionCount（加载失败时禁止显示可执行的确认）；
/// - 重置失败保持 .failed 并保留服务端错误信息，不得静默回到 ready。
@MainActor
class WebPushMaintenanceViewModel: ObservableObject {
    @Published private(set) var phase: WebPushResetPhase = .idle
    @Published private(set) var health: WebPushHealthState = .unknown
    @Published private(set) var statusDetail: String = ""
    @Published private(set) var subscriptionCount: Int = 0
    @Published private(set) var vapidKeyFingerprint: String = ""

    private let api: WebPushMaintenanceAPIProviding

    init(api: WebPushMaintenanceAPIProviding) {
        self.api = api
    }

    /// 是否应显示 misconfigured 警示 + 重置入口。unknown 时也显示入口
    /// （可能是旧 runtime 未实现端点；失败信息会如实呈现）。
    var showsMaintenanceSection: Bool {
        switch health {
        case .healthy, .misconfigured, .unconfigured, .unknown:
            return true
        }
    }

    var showsMisconfiguredWarning: Bool {
        health == .misconfigured || health == .unknown
    }

    /// 确认弹窗文案中的待删除数量。加载失败时为 nil——UI 不得在未知数量下请求确认。
    var pendingRemovalCount: Int? {
        switch phase {
        case .ready, .idle:
            return subscriptionCount
        case .loadingStatus, .resetting, .succeeded, .failed:
            return nil
        }
    }

    var canTriggerReset: Bool {
        switch phase {
        case .ready:
            return true
        case .idle, .loadingStatus, .resetting, .succeeded, .failed:
            return false
        }
    }

    func loadStatus() async {
        phase = .loadingStatus
        do {
            let status = try await api.getWebPushStatus()
            health = WebPushHealthState(raw: status.status)
            statusDetail = status.detail ?? ""
            subscriptionCount = status.subscriptionCount
            vapidKeyFingerprint = status.vapidKeyFingerprint ?? ""
            phase = .ready
        } catch {
            // 加载失败：fail-closed 呈现，不虚构状态；重置入口不可用直到状态已知。
            health = .unknown
            statusDetail = String(describing: error)
            phase = .failed(message: "无法读取 Web Push 状态：\(error.localizedDescription)")
        }
    }

    /// 用户确认后执行一次重置。调用方必须先经确认弹窗（不得自动触发）。
    func performResetAfterConfirmation() async {
        guard canTriggerReset else { return }
        let previousCount = subscriptionCount
        phase = .resetting
        do {
            let result = try await api.resetWebPush()
            guard result.reset, result.status == "healthy" else {
                // 服务端报告未恢复：如实呈现，不得当成功。
                phase = .failed(message: "重置未完成（服务端状态 \(result.status)）")
                return
            }
            health = .healthy
            subscriptionCount = 0
            vapidKeyFingerprint = result.vapidKeyFingerprint ?? ""
            statusDetail = ""
            phase = .succeeded(removed: result.removedSubscriptions == 0 ? previousCount : result.removedSubscriptions,
                               fingerprint: result.vapidKeyFingerprint)
        } catch {
            phase = .failed(message: "重置失败：\(error.localizedDescription)")
        }
    }
}
