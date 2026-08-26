import XCTest
@testable import CordCodeLink

/// Web Push 维护模型定向测试（方案 §12.3：状态判断、计数、确认后调用、错误呈现）。
/// 语义约束：不自动重置；未知状态不得显示可执行确认；失败不得伪装成功。
final class WebPushMaintenanceViewModelTests: XCTestCase {

    private struct FakeAPI: WebPushMaintenanceAPIProviding {
        var status: Result<WebPushMaintenanceStatus, Error>
        var reset: Result<WebPushResetResult, Error>

        func getWebPushStatus() async throws -> WebPushMaintenanceStatus {
            try status.get()
        }

        func resetWebPush() async throws -> WebPushResetResult {
            try reset.get()
        }
    }

    private struct StubError: LocalizedError {
        var errorDescription: String? { "connection refused" }
    }

    private func healthyStatus(count: Int) -> WebPushMaintenanceStatus {
        WebPushMaintenanceStatus(
            status: "healthy",
            detail: nil,
            subscriptionCount: count,
            vapidKeyFingerprint: "abcdef0123456789",
            lastResetAtMillis: 0,
            lastResetError: nil
        )
    }

    @MainActor
    func testLoadHealthyStatusBecomesReady() async {
        let model = WebPushMaintenanceViewModel(
            api: FakeAPI(status: .success(healthyStatus(count: 2)), reset: .failure(StubError()))
        )
        await model.loadStatus()
        XCTAssertEqual(model.health, .healthy)
        XCTAssertEqual(model.phase, .ready)
        XCTAssertEqual(model.subscriptionCount, 2)
        XCTAssertEqual(model.pendingRemovalCount, 2)
        XCTAssertTrue(model.canTriggerReset)
        XCTAssertFalse(model.showsMisconfiguredWarning)
    }

    @MainActor
    func testMisconfiguredStatusShowsWarning() async {
        let model = WebPushMaintenanceViewModel(
            api: FakeAPI(
                status: .success(WebPushMaintenanceStatus(
                    status: "misconfigured",
                    detail: "subscriptions exist but vapid key file is missing",
                    subscriptionCount: 3,
                    vapidKeyFingerprint: nil,
                    lastResetAtMillis: 0,
                    lastResetError: nil
                )),
                reset: .failure(StubError())
            )
        )
        await model.loadStatus()
        XCTAssertEqual(model.health, .misconfigured)
        XCTAssertTrue(model.showsMisconfiguredWarning)
        XCTAssertTrue(model.canTriggerReset, "misconfigured 必须提供恢复入口")
        XCTAssertEqual(model.pendingRemovalCount, 3, "确认文案必须携带真实待删数量")
    }

    @MainActor
    func testLoadFailureIsFailClosedAndBlocksReset() async {
        let model = WebPushMaintenanceViewModel(
            api: FakeAPI(status: .failure(StubError()), reset: .failure(StubError()))
        )
        await model.loadStatus()
        XCTAssertEqual(model.health, .unknown, "加载失败不得虚构为 healthy")
        XCTAssertNil(model.pendingRemovalCount, "未知数量下不得显示可执行的确认")
        XCTAssertFalse(model.canTriggerReset)
        if case .failed = model.phase {} else {
            XCTFail("phase = \(model.phase)，want failed")
        }
    }

    @MainActor
    func testResetSuccessReportsRemovalAndHealthy() async {
        let model = WebPushMaintenanceViewModel(
            api: FakeAPI(
                status: .success(healthyStatus(count: 1)),
                reset: .success(WebPushResetResult(
                    reset: true,
                    removedSubscriptions: 1,
                    status: "healthy",
                    vapidKeyFingerprint: "ffffffff00000000"
                ))
            )
        )
        await model.loadStatus()
        await model.performResetAfterConfirmation()
        XCTAssertEqual(model.health, .healthy)
        XCTAssertEqual(model.subscriptionCount, 0)
        XCTAssertEqual(model.vapidKeyFingerprint, "ffffffff00000000")
        guard case .succeeded(let removed, let fp) = model.phase else {
            return XCTFail("phase = \(model.phase)，want succeeded")
        }
        XCTAssertEqual(removed, 1)
        XCTAssertEqual(fp, "ffffffff00000000")
    }

    @MainActor
    func testResetFailureIsHonestNotSilentRecovery() async {
        let model = WebPushMaintenanceViewModel(
            api: FakeAPI(
                status: .success(WebPushMaintenanceStatus(
                    status: "misconfigured",
                    detail: "corrupt",
                    subscriptionCount: 2,
                    vapidKeyFingerprint: nil,
                    lastResetAtMillis: 0,
                    lastResetError: nil
                )),
                reset: .failure(StubError())
            )
        )
        await model.loadStatus()
        await model.performResetAfterConfirmation()
        XCTAssertEqual(model.health, .misconfigured, "失败后不得虚报恢复")
        guard case .failed(let message) = model.phase else {
            return XCTFail("phase = \(model.phase)，want failed")
        }
        XCTAssertTrue(message.contains("connection refused"), "错误信息必须保留服务端原因")
    }

    @MainActor
    func testResetServerReportsUnhealthyIsFailure() async {
        let model = WebPushMaintenanceViewModel(
            api: FakeAPI(
                status: .success(healthyStatus(count: 1)),
                reset: .success(WebPushResetResult(
                    reset: true,
                    removedSubscriptions: 1,
                    status: "misconfigured",
                    vapidKeyFingerprint: nil
                ))
            )
        )
        await model.loadStatus()
        await model.performResetAfterConfirmation()
        guard case .failed = model.phase else {
            XCTFail("服务端报告未恢复必须呈现为 failed，got \(model.phase)")
            return
        }
        XCTAssertEqual(model.health, .healthy, "health 保持加载值，不因失败路径额外污染")
    }

    @MainActor
    func testResetRequiresReadyPhaseNoAutoReset() async {
        let model = WebPushMaintenanceViewModel(
            api: FakeAPI(
                status: .failure(StubError()),
                reset: .success(WebPushResetResult(reset: true, removedSubscriptions: 0, status: "healthy", vapidKeyFingerprint: "x"))
            )
        )
        // 未 loadStatus（idle）时 reset 守卫直接拒绝——不自动重置。
        await model.performResetAfterConfirmation()
        XCTAssertEqual(model.phase, .idle)
    }

    @MainActor
    func testHealthStateDecodingFailClosed() {
        XCTAssertEqual(WebPushHealthState(raw: "healthy"), .healthy)
        XCTAssertEqual(WebPushHealthState(raw: "misconfigured"), .misconfigured)
        XCTAssertEqual(WebPushHealthState(raw: "unconfigured"), .unconfigured)
        XCTAssertEqual(WebPushHealthState(raw: nil), .unknown)
        XCTAssertEqual(WebPushHealthState(raw: "something-new"), .unknown, "未知值不得当 healthy")
    }

    @MainActor
    func testStatusJSONDecodes() throws {
        let json = """
        {"status":"healthy","subscriptionCount":2,"vapidKeyFingerprint":"abcdef0123456789","lastResetAtMillis":123}
        """.data(using: .utf8)!
        let decoded = try JSONDecoder().decode(WebPushMaintenanceStatus.self, from: json)
        XCTAssertEqual(decoded.status, "healthy")
        XCTAssertEqual(decoded.subscriptionCount, 2)
        XCTAssertNil(decoded.detail)
        XCTAssertNil(decoded.lastResetError)

        let resetJSON = """
        {"reset":true,"removedSubscriptions":1,"status":"healthy"}
        """.data(using: .utf8)!
        let reset = try JSONDecoder().decode(WebPushResetResult.self, from: resetJSON)
        XCTAssertTrue(reset.reset)
        XCTAssertEqual(reset.removedSubscriptions, 1)
        XCTAssertNil(reset.vapidKeyFingerprint)
    }
}
