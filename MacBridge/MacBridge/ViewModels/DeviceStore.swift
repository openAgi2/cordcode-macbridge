import Foundation

/// 已授权设备列表的共享状态归属。
///
/// UX 重设计（2026-07-13）P0-4：`ContentView` 原先直接持有 `devices`/`hasLoadedDevices`/
/// `devicesError` 与 `loadDevices`/`revokeDevice`。重设计要求把这些状态迁出 `ContentView`，
/// 让工作站、设备页共享同一 store，同时**不要**塞进 `BridgeStatusViewModel`。
///
/// 数据源保持 Management API（行为不变）。
///
/// 撤销流程按 followups v9 §4.3.6 operation coordinator 运行：统一 operation
/// generation；revoke 进行中普通 refresh 合并进强制 reload；并发 revoke 在 store
/// 层 busy 拒绝（不发请求、不递增 token）；`isRevoking`/warning 只由 gen 仍当前的
/// 结束路径在一次同步块内提交。
@MainActor
final class DeviceStore: ObservableObject {
    /// 已授权设备列表。
    @Published private(set) var devices: [TrustedDevice] = []
    /// 是否已完成至少一次成功加载（区分“尚未加载”与“加载结果为空”）。
    @Published private(set) var hasLoadedDevices = false
    /// 最近一次加载/撤销错误的人类可读描述；无错误时为 nil。
    @Published private(set) var devicesError: String?

    /// 撤销操作是否进行中（供 UI 禁用重复动作）。
    @Published private(set) var isRevoking = false
    /// 最近一次撤销的清理/一致性警告；无则 nil（r4–r9 评审：投递授权由服务端
    /// deny-by-default 过滤兜底，这里只做可观测性）。
    @Published private(set) var revokeCleanupWarning: String?

    private var apiClient: DeviceAPIProviding?

    // MARK: - Operation coordinator（§4.3.6）

    /// 统一 operation generation：所有 list/revoke 操作递增同一 token；晚到的
    /// 旧 gen 结果被丢弃（不覆盖 devices/error/warning/isRevoking）。
    private var operationGeneration = 0
    /// revoke 进行中被合并的 refresh 标记（合并进 revoke 的强制 reload）。
    private var pendingRefreshRequested = false
    /// 当前 warning 发布时的 operation gen。
    private var warningGeneration = 0
    /// 被 dismiss 的 warning 所属 gen（晚到的旧 gen 结果不复活已 dismiss 的 warning）。
    private var dismissedWarningGeneration: Int?

    /// 注入 Management API 客户端。为 nil 时下一次 `loadDevices` 会标记无法连接。
    func configure(apiClient: DeviceAPIProviding?) {
        self.apiClient = apiClient
    }

    /// Presentation seam（§4.3.5）：alert 呈现绑定到此属性，使「ViewModel 有值 →
    /// alert 可达」可单测。
    var isRevokeCleanupWarningPresented: Bool {
        revokeCleanupWarning != nil
    }

    /// 拉取已授权设备列表。失败时设置 `devicesError` 并把 `hasLoadedDevices` 置 false。
    /// revoke 进行中被合并（§4.3.6）：不发起独立网络请求，合并进 revoke 的强制 reload。
    func loadDevices() async {
        if isRevoking {
            pendingRefreshRequested = true
            return
        }
        guard let client = apiClient else {
            hasLoadedDevices = false
            devicesError = L10n.errorCannotConnect
            return
        }
        operationGeneration += 1
        let gen = operationGeneration
        do {
            let list = try await client.listDevices()
            guard gen == operationGeneration else { return }  // stale 丢弃
            devices = list
            hasLoadedDevices = true
            devicesError = nil
        } catch {
            guard gen == operationGeneration else { return }  // stale 丢弃
            hasLoadedDevices = false
            devicesError = error.localizedDescription
        }
    }

    /// 撤销一台设备（§4.3.6）：任何 outcome（含 cancelled/oversize）后恰好一次
    /// forced reload，再按 15 行矩阵原子提交呈现。
    @discardableResult
    func revokeDevice(_ device: TrustedDevice) async -> RevokeFlowResult {
        // 并发 revoke：store 层拒绝（R8-B4）——不发网络请求、不递增 token，
        // 第一笔的 outcome/强制 reload/warning 完整保留。
        if isRevoking { return .busy }
        guard let client = apiClient else {
            devicesError = L10n.errorCannotConnect
            return .unavailable
        }
        operationGeneration += 1
        let gen = operationGeneration
        isRevoking = true
        // 新操作真正开始：清除上一条 warning（busy 拒绝不清除任何 warning）。
        revokeCleanupWarning = nil
        dismissedWarningGeneration = nil

        let outcome = await client.revokeDevice(device.deviceId)
        let reload = await performForcedReload(client: client, revokedDeviceId: device.deviceId)

        // 原子提交（§4.3.6）：gen 仍当前时一次同步块提交全部可见状态；
        // stale（防御路径）不提交、不清除 isRevoking（旧操作不清新操作的 busy）。
        guard gen == operationGeneration else { return .completed }
        applyForcedReloadState(reload)
        applyRevokePresentation(outcome, reload, gen: gen)
        isRevoking = false
        pendingRefreshRequested = false
        return .completed
    }

    /// 用户确认清理警告后清除提示（带 warning-generation：晚到的旧 gen 结果被
    /// gen guard 丢弃，不会复活已 dismiss 的 warning）。
    func dismissRevokeCleanupWarning() {
        dismissedWarningGeneration = warningGeneration
        revokeCleanupWarning = nil
    }

    // MARK: - Forced reload（§4.3.6 内部路径）

    /// typed reload outcome：不经过公开 `loadDevices()` 的 refresh 合并门控，
    /// 不读陈旧 `devices` 数组。
    private enum ForcedReloadOutcome {
        case absent([TrustedDevice])
        case present([TrustedDevice])
        case failed(String)
    }

    private func performForcedReload(client: DeviceAPIProviding, revokedDeviceId: String) async -> ForcedReloadOutcome {
        do {
            let list = try await client.listDevices()
            return list.contains { $0.deviceId == revokedDeviceId } ? .present(list) : .absent(list)
        } catch {
            return .failed(error.localizedDescription)
        }
    }

    /// reload 结果的列表状态提交（行 3/6/9/12/15：刷新失败由 reload 路径自身呈现，
    /// 不从旧数组推断）。
    private func applyForcedReloadState(_ reload: ForcedReloadOutcome) {
        switch reload {
        case .absent(let list), .present(let list):
            devices = list
            hasLoadedDevices = true
            devicesError = nil
        case .failed(let message):
            hasLoadedDevices = false
            devicesError = message
        }
    }

    // MARK: - 15 行决策矩阵（§4.3.3，由 §4.3.1 证据优先级机械生成）

    private func applyRevokePresentation(_ outcome: RevokeAttemptOutcome, _ reload: ForcedReloadOutcome, gen: Int) {
        switch (outcome, reload) {
        // 404 特例：not_found × absent → 服务端无此设备（含此前已撤销），无
        // cleanup 对象 → 无警告（须在通用 transport 分支之前匹配）。
        case (.transportOrHTTPFailure(.httpStatus(404, "not_found")), .absent):
            break
        // 行 1：confirmedClean × absent → 无警告。
        case (.confirmedClean, .absent):
            break
        // 行 2：confirmedClean × present → inconsistent（不降级撤销结论）。
        case (.confirmedClean, .present):
            publishWarning(L10n.devicesPushCleanupInconsistent, gen: gen)
        // 行 3：confirmedClean × failed → 无撤销警告（刷新失败已由 reload 呈现）。
        case (.confirmedClean, .failed):
            break
        // 行 4/6：confirmedCleanupFailure × absent/failed → cleanup-failure 警告
        //（撤销已确认 + 订阅确定未清 → 投递阻断声明成立）。
        case (.confirmedCleanupFailure(let err), .absent),
             (.confirmedCleanupFailure(let err), .failed):
            publishWarning(String(format: L10n.devicesPushCleanupWarning, err), gen: gen)
        // 行 5：× present → inconsistent + 清理失败附句（同一证据同一结论）。
        case (.confirmedCleanupFailure(let err), .present):
            publishWarning(
                L10n.devicesPushCleanupInconsistent
                    + String(format: L10n.devicesPushCleanupInconsistentDetail, err),
                gen: gen
            )
        // 行 7：confirmedCleanupUnknown × absent → pending（reload-absent 确认）。
        case (.confirmedCleanupUnknown, .absent):
            publishWarning(L10n.devicesPushCleanupPending, gen: gen)
        // 行 8：× present → inconsistent + cleanup-unknown 附句。
        case (.confirmedCleanupUnknown, .present):
            publishWarning(L10n.devicesPushCleanupInconsistent + L10n.devicesPushCleanupUnknownDetail, gen: gen)
        // 行 9：× failed → pending（response-confirmed）——不得声称「已从列表消失」
        //（R7-B4：无列表证据，只有 response 确认）。
        case (.confirmedCleanupUnknown, .failed):
            publishWarning(L10n.devicesPushCleanupPendingResponse, gen: gen)
        // 行 10：protocolUnknown × absent → pending（reload 对账确认撤销）。
        case (.protocolUnknown, .absent):
            publishWarning(L10n.devicesPushCleanupPending, gen: gen)
        // 行 11/12：protocolUnknown × present/failed → unknown（撤销结果未知）。
        case (.protocolUnknown, .present), (.protocolUnknown, .failed):
            publishWarning(L10n.devicesPushCleanupUnknown, gen: gen)
        // 行 13：transport × absent → pending（lost response 对账，含 oversize）。
        case (.transportOrHTTPFailure, .absent):
            publishWarning(L10n.devicesPushCleanupPending, gen: gen)
        // 行 14/15：transport × present/failed → devicesError（现有失败语义；
        // oversize 无特例，机械走 13/14/15——R8-B3）。
        case (.transportOrHTTPFailure(let issue), .present),
             (.transportOrHTTPFailure(let issue), .failed):
            devicesError = String(format: L10n.errorRemoveDevice, Self.describeTransportIssue(issue))
        }
    }

    private func publishWarning(_ text: String, gen: Int) {
        revokeCleanupWarning = text
        warningGeneration = gen
    }

    private static func describeTransportIssue(_ issue: RevokeTransportIssue) -> String {
        switch issue {
        case .networkError(let category):
            return "network: \(category.rawValue)"
        case .httpStatus(let status, let code):
            return code.map { "HTTP \(status) (\($0))" } ?? "HTTP \(status)"
        case .responseTooLarge:
            return "response too large"
        }
    }
}

/// 撤销流程的 store 层结果（§4.3.6）：busy = isRevoking 中被拒绝（未发请求、
/// 未递增 token）；unavailable = 无 client；completed = 完整流程走完。
enum RevokeFlowResult: Equatable {
    case completed
    case busy
    case unavailable
}
