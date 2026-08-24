import XCTest
@testable import CordCodeLink

/// TopologyMonitorStatusStore 生命周期测试（implementation plan v2 UI-2：轮询调度/退避/
/// 断连恢复/app 挂起暂停/epoch 新鲜度/fail-closed 分类）。
@MainActor
final class TopologyMonitorStatusStoreTests: XCTestCase {
    private var store: TopologyMonitorStatusStore!
    private var api: StubTopologyAPI!

    override func setUp() {
        super.setUp()
        api = StubTopologyAPI()
        store = TopologyMonitorStatusStore(
            cadence: 0.05, baseBackoff: 0.02, maxBackoff: 0.2,
            expectedEpochProvider: { 1710893634113558 }
        )
        store.configure(apiClient: api!)
    }

    override func tearDown() {
        store.stopPolling()
        store = nil
        api = nil
        super.tearDown()
    }

    private func enabledStatus(
        syncHealth: TopologySyncHealth? = .healthy,
        state: TopologySnapshotState = .enabled,
        bridgeEpoch: Int64? = 1710893634113558
    ) -> TopologyMonitorStatus {
        TopologyMonitorStatus(
            schemaVersion: "topology-monitor.v1",
            state: state,
            bridgeEpoch: bridgeEpoch,
            sampledAtMs: 1_700_000_000_000,
            syncHealth: syncHealth,
            dimensions: nil,
            instances: nil
        )
    }

    // MARK: - 轮询与发布

    func testPollOncePublishesEnabledSnapshot() async {
        api.stub = .success(enabledStatus())
        let ok = await store.pollOnce()
        XCTAssertTrue(ok)
        XCTAssertEqual(store.phase, .enabled(enabledStatus()))
        XCTAssertNotNil(store.lastUpdatedAt)
    }

    func testDisabledStateIsOwnBranch() async {
        api.stub = .success(enabledStatus(syncHealth: nil, state: .disabled))
        let ok = await store.pollOnce()
        XCTAssertTrue(ok)
        // §6：disabled 独立分支，不混同于网络/解码失败。
        XCTAssertEqual(store.phase, .disabled)
        XCTAssertNil(store.lastDiagnosticDetail)
    }

    func testNetworkFailureMapsToDiagnostic() async {
        api.stub = .failure(StubTopologyAPI.testError)
        let ok = await store.pollOnce()
        XCTAssertFalse(ok)
        XCTAssertEqual(store.phase, .diagnostic(.network))
        XCTAssertEqual(store.lastDiagnosticDetail?.isEmpty, false)
    }

    func testUnauthorized401MapsToDiagnostic() async {
        api.stub = .failure(ManagementAPIClient.ManagementError.httpError(401))
        let ok = await store.pollOnce()
        XCTAssertFalse(ok)
        // §6：401 → 诊断失败，不得展示为 healthy 或 disabled。
        XCTAssertEqual(store.phase, .diagnostic(.unauthorized))
    }

    func testDecodeFailureMapsToDiagnostic() async {
        api.stub = .failure(StubTopologyAPI.decodeError)
        let ok = await store.pollOnce()
        XCTAssertFalse(ok)
        // §2.4 fail-closed：坏形状 → 诊断失败（.decode），绝不默认 healthy。
        XCTAssertEqual(store.phase, .diagnostic(.decode))
    }

    func testUnknownTopLevelStateMapsToDiagnostic() async {
        api.stub = .success(enabledStatus(state: .unknown))
        let ok = await store.pollOnce()
        XCTAssertFalse(ok)
        XCTAssertEqual(store.phase, .diagnostic(.decode))
    }

    // MARK: - epoch 新鲜度

    func testStaleEpochRejectedAsDiagnostic() async {
        api.stub = .success(enabledStatus(bridgeEpoch: 999))
        let ok = await store.pollOnce()
        XCTAssertFalse(ok)
        // 快照 epoch ≠ 当前 runtime identity → 旧代数据，作废且不得展示。
        XCTAssertEqual(store.phase, .diagnostic(.staleEpoch))
    }

    func testEpochAboveInt64MaxPublishesWithoutOverflow() async {
        let liveEpoch = UInt64(Int64.max) + 42
        store = TopologyMonitorStatusStore(
            cadence: 0.05, baseBackoff: 0.02, maxBackoff: 0.2,
            expectedEpochProvider: { liveEpoch }
        )
        store.configure(apiClient: api!)
        let wireEpoch = Int64(bitPattern: liveEpoch)
        api.stub = .success(enabledStatus(bridgeEpoch: wireEpoch))

        let ok = await store.pollOnce()

        XCTAssertTrue(ok)
        XCTAssertEqual(store.phase, .enabled(enabledStatus(bridgeEpoch: wireEpoch)))
    }

    func testZeroEpochSkippedWhenProviderUnknown() async {
        store = TopologyMonitorStatusStore(
            cadence: 0.05, baseBackoff: 0.02, maxBackoff: 0.2,
            expectedEpochProvider: { nil }
        )
        store.configure(apiClient: api!)
        api.stub = .success(enabledStatus(bridgeEpoch: 0))
        let ok = await store.pollOnce()
        XCTAssertTrue(ok)
        // epoch 未知（v1 status 尚未解出）：跳过校验，如实呈现（与真实接线一致）。
        XCTAssertEqual(store.phase, .enabled(enabledStatus(bridgeEpoch: 0)))
    }

    // MARK: - 退避曲线（纯函数）

    func testBackoffCurve() {
        let d = { TopologyMonitorStatusStore.backoffDelay(afterFailures: $0, base: 5, cap: 60) }
        // f=已连续失败次数：5 → 10 → 20 → 40 → 60(封顶)。
        XCTAssertEqual(d(0), 5)
        XCTAssertEqual(d(1), 5)
        XCTAssertEqual(d(2), 10)
        XCTAssertEqual(d(3), 20)
        XCTAssertEqual(d(4), 40)
        XCTAssertEqual(d(5), 60)
        XCTAssertEqual(d(10), 60)
    }
    // MARK: - 轮询循环与前后台

    func testPollLoopPollsRepeatedlyAndRecoversFromFailure() async {
        // 先失败一次，再持续成功：循环应持续调用，成功后 phase 恢复 enabled。
        var calls = 0
        api.onCall = {
            calls += 1
            if calls == 1 { return .failure(StubTopologyAPI.testError) }
            return .success(self.enabledStatus())
        }
        store.configure(apiClient: api)
        // 0.05s cadence：等 ~0.4s 保证 ≥2 次 poll，之后停掉。
        try? await Task.sleep(nanoseconds: 400_000_000)
        store.stopPolling()
        XCTAssertGreaterThanOrEqual(calls, 2)
        XCTAssertEqual(store.phase, .enabled(enabledStatus()))
    }

    func testBackgroundPausesPollingAndForegroundResumes() async {
        var calls = 0
        api.onCall = {
            calls += 1
            return .success(self.enabledStatus())
        }
        store.configure(apiClient: api)
        store.setAppActive(false)  // app 失活
        try? await Task.sleep(nanoseconds: 400_000_000)
        let callsWhileBackground = calls
        try? await Task.sleep(nanoseconds: 400_000_000)
        XCTAssertEqual(calls, callsWhileBackground, "后台必须停止轮询")
        store.setAppActive(true)   // 回到前台：立即恢复采样（唤醒窗口 100ms）
        try? await Task.sleep(nanoseconds: 350_000_000)
        store.stopPolling()
        XCTAssertGreaterThan(calls, callsWhileBackground, "前台必须恢复轮询")
    }

    func testAppActiveToggleKeepsPhase() async {
        // 停止轮询后切换前后台：当前 phase 不被清除（暂停只停采样，不清展示）。
        store.stopPolling()
        _ = await store.pollOnce()  // 建立 diagnostic(network) 展示态
        store.setAppActive(false)
        XCTAssertEqual(store.phase, .diagnostic(.network), "暂停不清除当前 phase")
        store.setAppActive(false)
        XCTAssertEqual(store.phase, .diagnostic(.network), "重复设置同一状态无副作用")
    }

    func testPublishFailsClosedWhenNoClient() async {
        // 无 client 时不发请求、不改展示态（保持 idle，不发布任何诊断结论）。
        let bare = TopologyMonitorStatusStore()
        let ok = await bare.pollOnce()
        XCTAssertFalse(ok, "无 client 时不发布任何展示态")
        XCTAssertEqual(bare.phase, .idle)
        bare.stopPolling()
    }
}

// MARK: - Stub

@MainActor
private final class StubTopologyAPI: TopologyAPIProviding {
    static let testError = NSError(
        domain: "TopologyMonitorStatusStoreTests", code: -1009,
        userInfo: [NSLocalizedDescriptionKey: "网络失败"]
    )

    static let decodeError = DecodingError.typeMismatch(
        TopologyMonitorStatus.self,
        .init(codingPath: [], debugDescription: "坏形状")
    )

    enum Stub {
        case success(TopologyMonitorStatus)
        case failure(Error)
    }

    var stub: Stub?
    /// 逐次调用回调（可覆盖 stub 序列）；返回 nil 表示用默认 stub。
    var onCall: (() -> Stub?)?

    func getTopologySnapshot() async throws -> TopologyMonitorStatus {
        if let onCall {
            if let s = onCall() {
                switch s {
                case .success(let v): return v
                case .failure(let e): throw e
                }
            }
        }
        switch stub {
        case .success(let v): return v
        case .failure(let e): throw e
        case nil: throw Self.testError
        }
    }
}
