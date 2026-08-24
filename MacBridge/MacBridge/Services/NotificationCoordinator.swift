import Foundation
import UserNotifications
#if canImport(AppKit)
import AppKit
#endif

// MARK: - 配对请求系统通知 + 一键审批 (M1 / P0)

/// 管理 macOS 系统通知，把"配对 claim 到达"从"用户必须盯着配对窗口"变成"系统通知 + 一键 approve"。
///
/// 背景(来自 remote-web 方案 M1):`PairingViewModel.applyPairingStatus` 收到 `claimed` 时只置
/// `uiState = .claimed`,无系统通知、无窗口前置、无声音。Mac 用户若没在看,claim 一直晾着。
/// 本协调器在 claim 到达时发系统通知,通知 action(APPROVE/REJECT)回调到当前 PairingViewModel。
///
/// 信任模型不变:不动 go-bridge(保留 3s poll),只补通知层。本地通知在 macOS 14 上通常无需
/// 额外 entitlement(本地通知无需 APNs/push)。

/// 通知 action 标识符。
enum PairingNotificationAction {
    static let approve = "CORDCODE_PAIRING_APPROVE"
    static let reject = "CORDCODE_PAIRING_REJECT"
    static let category = "CORDCODE_PAIRING_CATEGORY"
}

/// 可注入的 action 处理协议,便于单测(真实 UNUserNotificationCenter 的 action 回调无法同步触发)。
@MainActor
protocol PairingNotificationActionHandling: AnyObject {
    func handleApproveAction()
    func handleRejectAction()
}

/// 通知授权状态查询的可注入协议。
@MainActor
protocol PairingAuthorizationQuerying: AnyObject {
    func authorizationStatus() -> UNAuthorizationStatus
}

@MainActor
final class NotificationCoordinator: NSObject, PairingAuthorizationQuerying {

    /// 当前活跃的 PairingViewModel 弱引用。approve() 首行 `guard currentSessionId` 要求
    /// action 回调落在同一个处于活跃 pairing 状态的实例上。
    weak var pairingViewModel: PairingViewModel?

    /// 注入的 action 处理器(默认指向自身,转发到 pairingViewModel)。单测可替换。
    var actionHandler: PairingNotificationActionHandling?

    private var isAuthorized = false
    private let authorizationStatusOverride: (() -> UNAuthorizationStatus)?

    init(authorizationStatusOverride: (() -> UNAuthorizationStatus)? = nil) {
        self.authorizationStatusOverride = authorizationStatusOverride
        super.init()
        // 默认 action 处理器:转发到当前 PairingViewModel。
        // 用 box 捕获 weak self,避免强引用循环。
        actionHandler = NotificationActionForwarder(coordinator: self)
    }

    // MARK: - 授权

    /// 请求通知授权(.alert/.sound/.badge)。在 AppDelegate 启动时调用一次。
    /// 授权失败/拒绝时不抛错——走菜单栏红点 fallback(不强求授权)。
    func requestAuthorization() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { [weak self] granted, _ in
            DispatchQueue.main.async {
                self?.isAuthorized = granted
                if granted {
                    self?.registerCategory()
                    self?.setDelegate()
                }
            }
        }
    }

    /// 同步查询当前授权状态(用于判断是否走 fallback)。单测可注入。
    func authorizationStatus() -> UNAuthorizationStatus {
        // 单测可注入稳定状态；产品默认读取真实 center。
        if let authorizationStatusOverride {
            return authorizationStatusOverride()
        }
        var status: UNAuthorizationStatus = .notDetermined
        let sem = DispatchSemaphore(value: 0)
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            status = settings.authorizationStatus
            sem.signal()
        }
        sem.wait()
        return status
    }

    /// 当前是否已授权发通知。
    var canDeliverNotifications: Bool {
        isAuthorized || authorizationStatus() == .authorized
    }

    // MARK: - 发送配对 claim 通知

    /// 配对 claim 到达时发系统通知(标题"配对请求",正文设备名/平台)。
    /// `PairingViewModel.applyPairingStatus` 的 `case "claimed"` 调用此方法。
    func notifyPairingClaimed(deviceName: String, platform: String) {
        guard canDeliverNotifications else {
            // 未授权:菜单栏红点 fallback(由调用方在 UI 层反映)。
            #if canImport(AppKit)
            NSApp.dockTile.badgeLabel = "!"
            #endif
            return
        }

        let content = UNMutableNotificationContent()
        content.title = L10n.pairingRequestTitle
        let platformLabel = platform.isEmpty ? "" : " (\(platform))"
        content.body = "\(deviceName)\(platformLabel)"
        content.categoryIdentifier = PairingNotificationAction.category
        content.sound = .default

        let request = UNNotificationRequest(
            identifier: "cordcode-pairing-claim",
            content: content,
            trigger: nil // 立即发送
        )
        UNUserNotificationCenter.current().add(request) { _ in }
    }

    /// 清除配对通知 + dock 红点(配对 resolve 后调用)。
    func clearPairingNotifications() {
        UNUserNotificationCenter.current().removeDeliveredNotifications(
            withIdentifiers: ["cordcode-pairing-claim"]
        )
        #if canImport(AppKit)
        NSApp.dockTile.badgeLabel = nil
        #endif
    }

    // MARK: - 内部

    private func registerCategory() {
        let approve = UNNotificationAction(
            identifier: PairingNotificationAction.approve,
            title: L10n.approve,
            options: [.foreground]
        )
        let reject = UNNotificationAction(
            identifier: PairingNotificationAction.reject,
            title: L10n.reject,
            options: [.destructive]
        )
        let category = UNNotificationCategory(
            identifier: PairingNotificationAction.category,
            actions: [approve, reject],
            intentIdentifiers: [],
            options: []
        )
        UNUserNotificationCenter.current().setNotificationCategories([category])
    }

    private func setDelegate() {
        UNUserNotificationCenter.current().delegate = self
    }
}

// MARK: - UNUserNotificationCenterDelegate

extension NotificationCoordinator: UNUserNotificationCenterDelegate {

    // 通知显示时允许带声音(前台/后台一致)。
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    // 用户点通知 action 时路由到 PairingViewModel。
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        Task { @MainActor in
            switch response.actionIdentifier {
            case PairingNotificationAction.approve:
                self.actionHandler?.handleApproveAction()
            case PairingNotificationAction.reject:
                self.actionHandler?.handleRejectAction()
            default:
                // 用户点通知体(非 action)→ 仅前置窗口,不自动 approve(保留人在回路)。
                #if canImport(AppKit)
                NSApp.activate(ignoringOtherApps: true)
                #endif
            }
            completionHandler()
        }
    }
}

/// 默认 action 转发器:把通知 action 转发到当前 PairingViewModel 的 approve/reject。
/// 弱引用 coordinator,避免循环。@MainActor:approve/reject 是 main-actor-isolated。
@MainActor
private final class NotificationActionForwarder: PairingNotificationActionHandling {
    weak var coordinator: NotificationCoordinator?
    init(coordinator: NotificationCoordinator) { self.coordinator = coordinator }

    func handleApproveAction() {
        coordinator?.pairingViewModel?.approve()
    }

    func handleRejectAction() {
        coordinator?.pairingViewModel?.reject()
    }
}
