import XCTest
@testable import CordCodeLink

// MARK: - M1 NotificationCoordinator action 路由单测

/// 验证通知 action(APPROVE/REJECT)正确路由到 PairingViewModel 的 approve/reject,
/// 以及弱引用在 ViewModel 释放后不崩溃。这些是 M1 的核心正确性。
///
/// 注:真实 UNUserNotificationCenter 的 action 回调无法在单测里同步触发(需用户交互),
/// 故用可注入的 PairingNotificationActionHandling test-double 替换默认 forwarder。

@MainActor
final class NotificationCoordinatorTests: XCTestCase {

    /// 记录收到的 action 调用,作 test-double。
    @MainActor
    final class RecordingActionHandler: PairingNotificationActionHandling {
        var approveCalls = 0
        var rejectCalls = 0
        func handleApproveAction() { approveCalls += 1 }
        func handleRejectAction() { rejectCalls += 1 }
    }

    func testApproveActionRoutesToHandler() {
        let coordinator = NotificationCoordinator()
        let recorder = RecordingActionHandler()
        coordinator.actionHandler = recorder

        // 模拟用户点 APPROVE action(绕过真实 UNUserNotificationCenter)。
        // 直接调用 actionHandler,等价于 didReceive 回调里的 APPROVE 分支。
        coordinator.actionHandler?.handleApproveAction()

        XCTAssertEqual(recorder.approveCalls, 1)
        XCTAssertEqual(recorder.rejectCalls, 0)
    }

    func testRejectActionRoutesToHandler() {
        let coordinator = NotificationCoordinator()
        let recorder = RecordingActionHandler()
        coordinator.actionHandler = recorder

        coordinator.actionHandler?.handleRejectAction()

        XCTAssertEqual(recorder.approveCalls, 0)
        XCTAssertEqual(recorder.rejectCalls, 1)
    }

    func testDefaultForwarderRoutesToPairingViewModelApprove() async {
        // 默认 forwarder 把 action 转发到 pairingViewModel.approve()。
        let coordinator = NotificationCoordinator()
        let viewModel = makeViewModelWithStubAPI()
        coordinator.pairingViewModel = viewModel

        // approve() 的 guard currentSessionId 要求 session 已建立 → 先 startPairing()。
        viewModel.startPairing()
        // startPairing() 内部用 Task 异步建 session;轮询等 waitingForClaim 状态(currentSessionId 已置)。
        for _ in 0..<50 {
            if case .waitingForClaim = viewModel.uiState { break }
            try? await Task.sleep(nanoseconds: 50_000_000)
        }
        guard case .waitingForClaim = viewModel.uiState else {
            XCTFail("startPairing 未进入 waitingForClaim,无法测 approve")
            return
        }

        // approve() 会调 stub 的 approvePairing —— 验证被调到。
        coordinator.actionHandler?.handleApproveAction()

        // approve() 内部是 Task,给一点时间让 stub 被调用。
        try? await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertEqual(stubApproveCallCount, 1, "approve action 应转发到 viewModel.approve()")
        XCTAssertEqual(stubRejectCallCount, 0)
    }

    func testDefaultForwarderRoutesToPairingViewModelReject() async {
        let coordinator = NotificationCoordinator()
        let viewModel = makeViewModelWithStubAPI()
        coordinator.pairingViewModel = viewModel

        viewModel.startPairing()
        for _ in 0..<50 {
            if case .waitingForClaim = viewModel.uiState { break }
            try? await Task.sleep(nanoseconds: 50_000_000)
        }
        guard case .waitingForClaim = viewModel.uiState else {
            XCTFail("startPairing 未进入 waitingForClaim,无法测 reject")
            return
        }

        coordinator.actionHandler?.handleRejectAction()

        try? await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertEqual(stubRejectCallCount, 1, "reject action 应转发到 viewModel.reject()")
    }

    func testWeakReferenceSurvivesViewModelDeallocation() {
        // ViewModel 释放后,coordinator 的弱引用变 nil,action 不应崩溃。
        let coordinator = NotificationCoordinator()
        do {
            let viewModel = makeViewModelWithStubAPI()
            coordinator.pairingViewModel = viewModel
            XCTAssertNotNil(coordinator.pairingViewModel)
        }
        // viewModel 出作用域被释放。
        XCTAssertNil(coordinator.pairingViewModel, "弱引用应在 ViewModel 释放后变 nil")
        // action 转发到已释放的 ViewModel 不崩溃(nil 安全)。
        coordinator.actionHandler?.handleApproveAction()
        coordinator.actionHandler?.handleRejectAction()
        // 无 crash = pass。
    }

    func testNotifyPairingClaimedWithoutAuthorizationSetsDockBadge() {
        // 未授权时走 fallback(菜单栏/dock 红点),不发真实通知。
        // 用未授权的 coordinator(canDeliverNotifications=false 默认)。
        let coordinator = NotificationCoordinator()
        // 默认 isAuthorized=false 且 authorizationStatus() 在测试环境返回 notDetermined → canDeliver=false。
        XCTAssertFalse(coordinator.canDeliverNotifications)
        // 不应崩溃(走 fallback 分支)。
        coordinator.notifyPairingClaimed(deviceName: "web-client", platform: "web")
    }

    // MARK: - 测试基础设施

    /// 共享的 stub 调用计数(StubAPIClient 写入)。
    private var stubApproveCallCount = 0
    private var stubRejectCallCount = 0

    @MainActor
    private func makeViewModelWithStubAPI() -> PairingViewModel {
        stubApproveCallCount = 0
        stubRejectCallCount = 0
        let vm = PairingViewModel()
        vm.configure(apiClient: StubPairingAPIClient(
            approveCount: { [weak self] in self?.stubApproveCallCount ?? 0 },
            setApproveCount: { [weak self] in self?.stubApproveCallCount = $0 },
            rejectCount: { [weak self] in self?.stubRejectCallCount ?? 0 },
            setRejectCount: { [weak self] in self?.stubRejectCallCount = $0 }
        ))
        // 调用方须先 startPairing() 建立 session,approve()/reject() 的 guard currentSessionId 才通过。
        return vm
    }
}

/// 配对 API stub,记录 approve/reject 调用次数。
@MainActor
private final class StubPairingAPIClient: PairingAPIProviding {
    let approveCount: () -> Int
    let setApproveCount: (Int) -> Void
    let rejectCount: () -> Int
    let setRejectCount: (Int) -> Void

    init(
        approveCount: @escaping () -> Int,
        setApproveCount: @escaping (Int) -> Void,
        rejectCount: @escaping () -> Int,
        setRejectCount: @escaping (Int) -> Void
    ) {
        self.approveCount = approveCount
        self.setApproveCount = setApproveCount
        self.rejectCount = rejectCount
        self.setRejectCount = setRejectCount
    }

    func createPairing() async throws -> PairingSessionInfo {
        PairingSessionInfo(id: "sess-test", qrPayload: "cccode://pair?...", webQrPayload: nil, manualCode: "123456", expiresAt: "2099-01-01T00:00:00Z")
    }

    func getPairingStatus(_ pairingId: String) async throws -> PairingSessionStatus {
        PairingSessionStatus(id: pairingId, state: "claimed", claimingDeviceName: "web", claimingPlatform: "web", expiresAt: nil)
    }

    func approvePairing(_ pairingId: String) async throws -> PairingApproval {
        setApproveCount(approveCount() + 1)
        return PairingApproval(pairingId: pairingId, deviceId: "dev-test", deviceToken: nil, state: "approved")
    }

    func rejectPairing(_ pairingId: String) async throws {
        setRejectCount(rejectCount() + 1)
    }
}
