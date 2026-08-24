import Combine
import Foundation

/// 拓扑监视轮询生命周期 owner（implementation plan v2 UI-2：cadence、退避、
/// epoch 新鲜度、app 前后台暂停）。
///
/// 语义（plan §2.4/§6，fail-closed）：
/// - `state=disabled` → `.disabled`：服务端明确关闭，面板不展示（不冒充 healthy/disabled 混同）。
/// - 网络失败 / 401 / 解码失败 / 快照 epoch 陈旧 → `.diagnostic`：UI 显示中性「诊断失败」，
///   绝不展示为 healthy；也不再假装是可用快照。
/// - enabled 快照仅在其 bridgeEpoch 与当前 runtime identity epoch 一致时发布（§2.4 同源校验；
///   epoch 未知时略过校验，epoch=0 视为未初始化，如实呈现不造假）。
/// - 无持久化：进程外不保存任何状态，重建即从 `.idle` 开始。
/// - 轮询 cadence 30s（与服务端 30s/60s 采集对齐；防抖/聚合均由服务端裁决，客户端只读展示）。
/// - 连续失败指数退避 5s→10s→20s→40s→cap 60s；成功回到 cadence。
/// - app 失活暂停轮询（保留当前 phase），重新激活立即恢复采样（不补全额睡眠）。
@MainActor
final class TopologyMonitorStatusStore: ObservableObject {
    /// 展示面 phase：UI 只按这四个分支渲染（§6）。
    enum Phase: Equatable {
        /// 尚未开始（无 client / 未启动）：不展示任何拓扑面板。
        case idle
        /// 服务端明确 `state=disabled`（-topology-monitor off）：面板不显示。
        case disabled
        /// 最近一次通过校验的 enabled 快照（healthy/degraded/unknown 等由 UI 按 syncHealth 细分）。
        case enabled(TopologyMonitorStatus)
        /// 网络失败 / 401 / 解码失败 / epoch 陈旧：中性「诊断失败」，不得展示为 healthy 或 disabled。
        case diagnostic(Kind)
    }

    enum Kind: Equatable {
        case network
        case unauthorized
        case decode
        case staleEpoch
    }

    @Published private(set) var phase: Phase = .idle
    /// 上一轮诊断的中文细节（UI 详情/日志用；非展示主文案）。
    @Published private(set) var lastDiagnosticDetail: String?
    /// 最近一次成功发布 enabled 快照的时间（UI 可作「N 秒前更新」）。
    @Published private(set) var lastUpdatedAt: Date?

    private let cadence: TimeInterval
    private let baseBackoff: TimeInterval
    private let maxBackoff: TimeInterval
    /// 当前 runtime identity epoch 来源（= Management v1 `runtimeIdentity.bridgeEpoch`，
    /// 与快照 bridgeEpoch 同源：main.go managementBridgeEpoch）。
    private let expectedEpochProvider: () -> UInt64?

    private var apiClient: TopologyAPIProviding?
    private var pollTask: Task<Void, Never>?
    private var isAppActive = true

    init(
        cadence: TimeInterval = 30,
        baseBackoff: TimeInterval = 5,
        maxBackoff: TimeInterval = 60,
        expectedEpochProvider: @escaping () -> UInt64? = { nil }
    ) {
        self.cadence = cadence
        self.baseBackoff = baseBackoff
        self.maxBackoff = maxBackoff
        self.expectedEpochProvider = expectedEpochProvider
    }

    /// 配置管理 API client（与 BackendStatusViewModel 同模式；换 client 即重启轮询）。
    func configure(apiClient: TopologyAPIProviding) {
        self.apiClient = apiClient
        startPolling()
    }

    func startPolling() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            await self?.pollLoop()
        }
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    /// app 前后台暂停/恢复。失活：保留 phase 不清空；激活：立即恢复采样。
    func setAppActive(_ active: Bool) {
        isAppActive = active
    }

    /// 退避曲线（纯函数，供测试）：失败 f 次后的下一个轮询间隔。
    /// 失败 0 次 = 刚成功 → base（调用方决定用 cadence；此处仅给失败曲线）。
    static func backoffDelay(afterFailures failures: Int, base: TimeInterval, cap: TimeInterval) -> TimeInterval {
        guard failures > 0 else { return base }
        let exponent = min(Double(failures - 1), 10)
        let d = base * pow(2, exponent)
        return min(d, cap)
    }

    // MARK: - 轮询循环

    private func pollLoop() async {
        var consecutiveFailures = 0
        while !Task.isCancelled {
            guard isAppActive else {
                // 后台：不轮询，100ms 唤醒检查一次前台状态（保留 phase；开销可忽略）。
                try? await Task.sleep(nanoseconds: 100_000_000)
                continue
            }
            let ok = await pollOnce()
            consecutiveFailures = ok ? 0 : consecutiveFailures + 1
            if ok {
                await sleepInterruptibly(cadence)
            } else {
                let delay = Self.backoffDelay(afterFailures: consecutiveFailures, base: baseBackoff, cap: maxBackoff)
                await sleepInterruptibly(delay)
            }
        }
    }

    /// 分段睡眠：中途失活立即返回（上层 loop 转后台等待，激活后立刻采样，不补全额 cadence）。
    private func sleepInterruptibly(_ duration: TimeInterval) async {
        var remaining = duration
        while remaining > 0 && !Task.isCancelled && isAppActive {
            let step = min(0.5, remaining)
            try? await Task.sleep(nanoseconds: UInt64(step * 1_000_000_000))
            remaining -= step
        }
    }

    /// 单次采样。返回 true=成功（快照合格并更新 phase）。
    @discardableResult
    func pollOnce() async -> Bool {
        guard let client = apiClient else { return false }
        do {
            let status = try await client.getTopologySnapshot()
            return publish(status)
        } catch let error as ManagementAPIClient.ManagementError {
            switch error {
            case .httpError(401):
                fail(.unauthorized, "管理接口拒绝访问（401），依据 §6 归为诊断失败")
            default:
                fail(.network, "管理接口请求失败：\(error)")
            }
            return false
        } catch is DecodingError {
            fail(.decode, "快照解码失败（形状损坏），依据 §2.4 fail-closed")
            return false
        } catch {
            fail(.network, "快照请求失败：\(error.localizedDescription)")
            return false
        }
    }

    private func publish(_ status: TopologyMonitorStatus) -> Bool {
        switch status.state {
        case .disabled:
            // §6：disabled 独立分支，与解码失败/网络失败严格分开。
            phase = .disabled
            lastDiagnosticDetail = nil
            lastUpdatedAt = Date()
            return true
        case .enabled:
            // epoch 新鲜度：快照 epoch 与 runtime identity 不一致 = 旧代数据，作废。
            if let expected = expectedEpochProvider(), expected != 0,
               let actual = status.bridgeEpoch, actual != 0,
               UInt64(bitPattern: actual) != expected {
                fail(.staleEpoch, "快照 bridgeEpoch(\(actual)) ≠ runtime identity(\(expected))，数据来自旧代")
                return false
            }
            phase = .enabled(status)
            lastDiagnosticDetail = nil
            lastUpdatedAt = Date()
            return true
        case .unknown:
            // 顶层 state 未知：按 fail-closed 视为诊断失败。
            fail(.decode, "快照顶层 state 未知，fail-closed 归为诊断失败")
            return false
        }
    }

    private func fail(_ kind: Kind, _ detail: String) {
        phase = .diagnostic(kind)
        lastDiagnosticDetail = detail
    }
}

// MARK: - 展示推导（§4.3 表；纯逻辑，UI 只按分支渲染）

/// 拓扑面板展示语义（implementation plan v2 UI-3）。
enum TopologyDisplay: Equatable {
    /// healthy / disabled / idle：面板不显示（健康不打扰；服务端关闭不冒充 healthy）。
    case hidden
    /// not_applicable：可选中性「未检测到 Codex App」。
    case infoNotApplicable
    /// degraded：高警示，按维度证据细分文案。
    case warning(TopologyWarningKind)
    /// unknown / 网络 / 401 / 解码失败：中性「诊断失败」，绝不伪装成 split。
    case diagnosticFailure
}

enum TopologyWarningKind: Equatable {
    /// bridge=shared × aggregate=split_present：Desktop 未接共享 daemon。
    case desktopDetached
    /// bridge=shared × aggregate=mixed：仅部分 Desktop 实例同步。
    case partialSync
    /// bridge=partial/absent × 任意：CordCode observer/main 未完整附着；不得归咎 Desktop。
    case observerUnattached
    /// degraded 但证据不足以细分：通用高警示（不臆断归责对象）。
    case general
}

extension TopologyMonitorStatusStore.Phase {
    /// 从当前 phase 派生展示分支（维度 enum 是 raw 证据字符串，按 §4.3 表判定）。
    var display: TopologyDisplay {
        switch self {
        case .idle, .disabled:
            return .hidden
        case .diagnostic:
            return .diagnosticFailure
        case .enabled(let status):
            switch status.syncHealth {
            case .healthy:
                return .hidden
            case .notApplicable:
                return .infoNotApplicable
            case .unknown, nil:
                // enabled 但 syncHealth 未知/缺失：fail-closed 中性诊断失败。
                return .diagnosticFailure
            case .degraded:
                return .warning(warningKind(for: status.dimensions, fallback: .general))
            }
        }
    }

    private func warningKind(for dims: TopologyMonitorDimensions?, fallback: TopologyWarningKind) -> TopologyWarningKind {
        guard let dims else { return fallback }
        // §4.3：bridge=partial/absent × 任意 aggregate → observer/main 未完整附着（不得归咎 Desktop）。
        switch dims.topologyBridgeAttachment?.enumValue {
        case "partial", "absent":
            return .observerUnattached
        default:
            break
        }
        switch dims.topologyDesktopAggregate?.enumValue {
        case "split_present":
            return .desktopDetached
        case "mixed":
            return .partialSync
        default:
            return fallback
        }
    }
}
