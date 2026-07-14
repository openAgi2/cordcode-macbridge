import Foundation

/// 已授权设备列表的共享状态归属。
///
/// UX 重设计（2026-07-13）P0-4：`ContentView` 原先直接持有 `devices`/`hasLoadedDevices`/
/// `devicesError` 与 `loadDevices`/`revokeDevice`。重设计要求把这些状态迁出 `ContentView`，
/// 让工作站、设备页共享同一 store，同时**不要**塞进 `BridgeStatusViewModel`。
///
/// 数据源保持 Management API（行为不变）。
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

    private var apiClient: DeviceAPIProviding?

    /// 注入 Management API 客户端。为 nil 时下一次 `loadDevices` 会标记无法连接。
    func configure(apiClient: DeviceAPIProviding?) {
        self.apiClient = apiClient
    }

    /// 拉取已授权设备列表。失败时设置 `devicesError` 并把 `hasLoadedDevices` 置 false。
    func loadDevices() async {
        guard let client = apiClient else {
            hasLoadedDevices = false
            devicesError = L10n.errorCannotConnect
            return
        }
        do {
            devices = try await client.listDevices()
            hasLoadedDevices = true
            devicesError = nil
        } catch {
            hasLoadedDevices = false
            devicesError = error.localizedDescription
        }
    }

    /// 撤销一台设备；成功后刷新列表。失败时设置 `devicesError`。
    func revokeDevice(_ device: TrustedDevice) async {
        guard let client = apiClient else {
            devicesError = L10n.errorCannotConnect
            return
        }
        isRevoking = true
        defer { isRevoking = false }
        do {
            try await client.revokeDevice(device.deviceId)
            await loadDevices()
        } catch {
            devicesError = String(format: L10n.errorRemoveDevice, error.localizedDescription)
        }
    }
}
