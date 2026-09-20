import XCTest
@testable import CordCodeLink

// P0-4 状态所有权迁移测试 + followups v9 §4.3.5：15 行决策矩阵、404 特例、
// 并发 revoke busy 拒绝、revoke 期间 refresh 合并、stale refresh 丢弃、
// dismiss/warning 生命周期与 presentation seam。
@MainActor
final class DeviceStoreTests: XCTestCase {

    /// 可控 continuation 门：让 stub 的网络调用挂起，构造重叠/交错。
    /// 支持多个等待者（stale-refresh 用例中 refresh 与 forced reload 同时等待）。
    private final class AsyncGate {
        private var continuations: [CheckedContinuation<Void, Never>] = []
        private var opened = false

        func wait() async {
            await withCheckedContinuation { c in
                if opened { c.resume() } else { continuations.append(c) }
            }
        }

        func open() {
            opened = true
            continuations.forEach { $0.resume() }
            continuations.removeAll()
        }
    }

    private final class DeviceAPIStub: DeviceAPIProviding {
        var devices: [TrustedDevice]
        var listError: Error?
        var revokeOutcome: RevokeAttemptOutcome = .confirmedClean
        var revokeGate: AsyncGate?
        var listGate: AsyncGate?
        private(set) var revokedDeviceIds: [String] = []
        private(set) var revokeCallCount = 0
        private(set) var listCallCount = 0

        init(devices: [TrustedDevice]) {
            self.devices = devices
        }

        func listDevices() async throws -> [TrustedDevice] {
            listCallCount += 1
            if let listGate { await listGate.wait() }
            if let listError { throw listError }
            return devices
        }

        func revokeDevice(_ deviceId: String) async -> RevokeAttemptOutcome {
            revokeCallCount += 1
            revokedDeviceIds.append(deviceId)
            if let revokeGate { await revokeGate.wait() }
            return revokeOutcome
        }
    }

    private struct StubError: Error {}

    private func makeDevice(_ id: String) -> TrustedDevice {
        TrustedDevice(deviceId: id, displayName: nil, platform: "ios", createdAt: nil, lastSeenAt: nil)
    }

    private func waitUntil(_ condition: @autoclosure () -> Bool) async {
        for _ in 0..<500 {
            if condition() { return }
            await Task.yield()
            try? await Task.sleep(nanoseconds: 2_000_000)
        }
    }

    // MARK: - 列表加载（P0-4 原有用例保持）

    func testLoadDevicesSuccessPopulatesList() async {
        let store = DeviceStore()
        let d1 = TrustedDevice(deviceId: "d1", displayName: "Alice iPhone", platform: "ios", createdAt: nil, lastSeenAt: nil)
        store.configure(apiClient: DeviceAPIStub(devices: [d1]))

        await store.loadDevices()

        XCTAssertTrue(store.hasLoadedDevices)
        XCTAssertNil(store.devicesError)
        XCTAssertEqual(store.devices.count, 1)
        XCTAssertEqual(store.devices.first?.deviceId, "d1")
    }

    func testLoadDevicesEmptySetsLoadedNotError() async {
        let store = DeviceStore()
        store.configure(apiClient: DeviceAPIStub(devices: []))

        await store.loadDevices()

        XCTAssertTrue(store.hasLoadedDevices, "空列表应标记为已加载，而非错误")
        XCTAssertTrue(store.devices.isEmpty)
        XCTAssertNil(store.devicesError)
    }

    func testLoadDevicesFailureSetsErrorAndNotLoaded() async {
        let stub = DeviceAPIStub(devices: [])
        stub.listError = StubError()
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.loadDevices()

        XCTAssertFalse(store.hasLoadedDevices)
        XCTAssertNotNil(store.devicesError)
    }

    func testNoClientMarksCannotConnect() async {
        let store = DeviceStore()
        store.configure(apiClient: nil)

        await store.loadDevices()

        XCTAssertFalse(store.hasLoadedDevices)
        XCTAssertNotNil(store.devicesError, "无 client 时应设置错误而非静默")
    }

    // MARK: - 撤销基础流程

    func testRevokeCallsApiThenReloads() async {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        let store = DeviceStore()
        store.configure(apiClient: stub)

        let result = await store.revokeDevice(d1)

        XCTAssertEqual(result, .completed)
        XCTAssertEqual(stub.revokedDeviceIds, ["d1"])
        XCTAssertEqual(stub.listCallCount, 1, "撤销后应恰好一次 forced reload")
        XCTAssertTrue(store.devices.isEmpty, "撤销成功后应刷新列表，设备应消失")
        XCTAssertTrue(store.hasLoadedDevices)
        XCTAssertFalse(store.isRevoking, "撤销完成后 isRevoking 应复位")
        XCTAssertNil(store.revokeCleanupWarning)
        XCTAssertNil(store.devicesError)
    }

    func testRevokeWithoutClientReturnsUnavailable() async {
        let store = DeviceStore()
        store.configure(apiClient: nil)

        let result = await store.revokeDevice(makeDevice("d1"))

        XCTAssertEqual(result, .unavailable)
        XCTAssertNotNil(store.devicesError)
        XCTAssertFalse(store.isRevoking)
    }

    // MARK: - 15 行决策矩阵（§4.3.3；warning 断言用 L10n 键值，语言无关）

    func testRevokeMatrixFifteenRows() async {
        let outcomes: [(String, RevokeAttemptOutcome)] = [
            ("confirmedClean", .confirmedClean),
            ("confirmedCleanupFailure", .confirmedCleanupFailure(pushCleanupError: "disk full")),
            ("confirmedCleanupUnknown", .confirmedCleanupUnknown),
            ("protocolUnknown", .protocolUnknown(.malformedJSON)),
            ("transport", .transportOrHTTPFailure(.httpStatus(status: 500, serverErrorCode: nil))),
        ]
        let reloads: [String] = ["absent", "present", "failed"]

        for (outcomeName, outcome) in outcomes {
            for reloadName in reloads {
                let d1 = makeDevice("d1")
                let stub = DeviceAPIStub(devices: [d1])
                let store = DeviceStore()
                store.configure(apiClient: stub)
                // 先成功加载一次，让 store 持有旧数组 [d1]；failed 行断言
                // 「reload 失败不更新旧数组」才有意义。
                await store.loadDevices()
                stub.revokeOutcome = outcome
                switch reloadName {
                case "absent": stub.devices = []
                case "present": break
                case "failed": stub.listError = StubError()
                default: break
                }

                await store.revokeDevice(d1)

                let label = "\(outcomeName) × \(reloadName)"
                let warning = store.revokeCleanupWarning
                let error = store.devicesError

                switch (outcomeName, reloadName) {
                // 行 1：无警告、无错误。
                case ("confirmedClean", "absent"):
                    XCTAssertNil(warning, label)
                    XCTAssertNil(error, label)
                // 行 2：inconsistent（不降级撤销结论）。
                case ("confirmedClean", "present"):
                    XCTAssertEqual(warning, L10n.devicesPushCleanupInconsistent, label)
                    XCTAssertNil(error, label)
                // 行 3：无撤销警告；刷新失败由 reload 路径呈现。
                case ("confirmedClean", "failed"):
                    XCTAssertNil(warning, label)
                    XCTAssertNotNil(error, label)
                // 行 4/6：cleanup-failure 警告（撤销已确认 + 订阅确定未清）。
                case ("confirmedCleanupFailure", "absent"),
                     ("confirmedCleanupFailure", "failed"):
                    XCTAssertEqual(warning, String(format: L10n.devicesPushCleanupWarning, "disk full"), label)
                // 行 5：inconsistent + 清理失败附句。
                case ("confirmedCleanupFailure", "present"):
                    XCTAssertEqual(
                        warning,
                        L10n.devicesPushCleanupInconsistent
                            + String(format: L10n.devicesPushCleanupInconsistentDetail, "disk full"),
                        label)
                // 行 7：pending（reload-absent 确认）。
                case ("confirmedCleanupUnknown", "absent"):
                    XCTAssertEqual(warning, L10n.devicesPushCleanupPending, label)
                    XCTAssertNil(error, label)
                // 行 8：inconsistent + cleanup-unknown 附句。
                case ("confirmedCleanupUnknown", "present"):
                    XCTAssertEqual(
                        warning,
                        L10n.devicesPushCleanupInconsistent + L10n.devicesPushCleanupUnknownDetail,
                        label)
                // 行 9：pending（response-confirmed；不得声称「已从列表消失」）。
                case ("confirmedCleanupUnknown", "failed"):
                    XCTAssertEqual(warning, L10n.devicesPushCleanupPendingResponse, label)
                    XCTAssertNotNil(error, label)
                // 行 10：pending（reload 对账确认撤销）。
                case ("protocolUnknown", "absent"):
                    XCTAssertEqual(warning, L10n.devicesPushCleanupPending, label)
                    XCTAssertNil(error, label)
                // 行 11/12：unknown（撤销结果未知）。
                case ("protocolUnknown", "present"), ("protocolUnknown", "failed"):
                    XCTAssertEqual(warning, L10n.devicesPushCleanupUnknown, label)
                // 行 13：pending（lost response 对账，含 oversize）。
                case ("transport", "absent"):
                    XCTAssertEqual(warning, L10n.devicesPushCleanupPending, label)
                    XCTAssertNil(error, label)
                // 行 14/15：devicesError（请求失败语义）。
                case ("transport", "present"), ("transport", "failed"):
                    XCTAssertNil(warning, label)
                    XCTAssertNotNil(error, label)
                default:
                    XCTFail("未覆盖组合：\(label)")
                }

                // 列表状态：absent/present 由 reload 提交；failed 不更新旧数组。
                switch reloadName {
                case "absent": XCTAssertTrue(store.devices.isEmpty, label)
                case "present": XCTAssertEqual(store.devices.map(\.deviceId), ["d1"], label)
                case "failed": XCTAssertEqual(store.devices.map(\.deviceId), ["d1"], label)
                default: break
                }
                XCTAssertFalse(store.isRevoking, label)
            }
        }
    }

    // 404 特例：not_found × absent → 无警告（须在通用 transport 行 13 之前匹配）。
    func testRevokeNotFoundWithAbsentReloadIsSilent() async {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        stub.revokeOutcome = .transportOrHTTPFailure(.httpStatus(status: 404, serverErrorCode: "not_found"))
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.revokeDevice(d1)

        XCTAssertNil(store.revokeCleanupWarning, "404 not_found × absent 不应发警告")
        XCTAssertNil(store.devicesError)
        XCTAssertTrue(store.devices.isEmpty)
    }

    // 404 无 error code × absent → 仍走行 13 pending（特例只匹配 not_found）。
    func testRevokeFourZeroFourWithoutCodeStillPending() async {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        stub.revokeOutcome = .transportOrHTTPFailure(.httpStatus(status: 404, serverErrorCode: nil))
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.revokeDevice(d1)

        XCTAssertEqual(store.revokeCleanupWarning, L10n.devicesPushCleanupPending)
    }

    // MARK: - Operation coordinator（§4.3.6）

    // 并发 revoke：第二笔 busy 拒绝——不发网络请求、不递增 token，第一笔
    // warning 完整保留（R8-B4）。
    func testRevokeBusyRejectsSecondRevoke() async throws {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        stub.revokeOutcome = .confirmedCleanupFailure(pushCleanupError: "disk full")
        let gate = AsyncGate()
        stub.revokeGate = gate
        let store = DeviceStore()
        store.configure(apiClient: stub)

        let first = Task { await store.revokeDevice(d1) }
        await waitUntil(store.isRevoking)

        let second = await store.revokeDevice(d1)
        XCTAssertEqual(second, .busy, "重叠 revoke 应 busy 拒绝")
        XCTAssertEqual(stub.revokeCallCount, 1, "busy 拒绝不得发网络请求")
        XCTAssertEqual(stub.listCallCount, 0, "revoke 未完成前不应已 reload")

        gate.open()
        let result = try await first.value
        XCTAssertEqual(result, .completed)
        XCTAssertEqual(
            store.revokeCleanupWarning,
            String(format: L10n.devicesPushCleanupWarning, "disk full"),
            "第一笔 warning 应完整保留，不被 busy 拒绝丢弃")
        XCTAssertFalse(store.isRevoking)
    }

    // revoke 进行中的普通 refresh：合并进 forced reload，不发独立请求。
    func testRefreshMergesDuringRevoke() async {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        let gate = AsyncGate()
        stub.revokeGate = gate
        let store = DeviceStore()
        store.configure(apiClient: stub)

        let task = Task { await store.revokeDevice(d1) }
        await waitUntil(store.isRevoking)

        await store.loadDevices()
        XCTAssertEqual(stub.listCallCount, 0, "revoke 期间 refresh 应合并，不发起独立请求")

        gate.open()
        _ = await task.value
        XCTAssertEqual(stub.listCallCount, 1, "合并后只有 forced reload 恰好一次")
        XCTAssertTrue(store.devices.isEmpty)
    }

    // 飞行中的 refresh 遇到新 revoke：旧 refresh 结果 stale 丢弃（只有飞行中
    // refresh 可被判 stale；第二笔 revoke 走 busy，不产生本场景）。
    func testStaleRefreshDiscardedWhenRevokeStarts() async {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        let gate = AsyncGate()
        stub.listGate = gate
        let store = DeviceStore()
        store.configure(apiClient: stub)

        let refresh = Task { await store.loadDevices() }
        await waitUntil(stub.listCallCount == 1)

        let revoke = Task { await store.revokeDevice(d1) }
        await waitUntil(stub.listCallCount == 2)

        gate.open()
        _ = await refresh.value
        let result = await revoke.value
        XCTAssertEqual(result, .completed)
        XCTAssertEqual(stub.listCallCount, 2)
        XCTAssertNil(store.devicesError, "stale refresh 失败/结果不得覆盖 revoke 的提交")
        XCTAssertTrue(store.hasLoadedDevices)
    }

    // MARK: - dismiss / warning 生命周期 / presentation seam

    func testDismissClearsWarningAndNewOperationDoesNotResurrect() async {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        stub.revokeOutcome = .confirmedCleanupFailure(pushCleanupError: "disk full")
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.revokeDevice(d1)
        XCTAssertTrue(store.isRevokeCleanupWarningPresented, "warning 非空时 presentation seam 应为 true")

        store.dismissRevokeCleanupWarning()
        XCTAssertNil(store.revokeCleanupWarning)
        XCTAssertFalse(store.isRevokeCleanupWarningPresented)

        // 新一笔 clean 撤销不复活旧 warning。
        stub.revokeOutcome = .confirmedClean
        await store.revokeDevice(d1)
        XCTAssertNil(store.revokeCleanupWarning)
        XCTAssertFalse(store.isRevokeCleanupWarningPresented)
    }

    // 新撤销开始时清除上一条 warning（新证据覆盖旧提示）。
    func testNewRevokeClearsPreviousWarning() async {
        let d1 = makeDevice("d1")
        let stub = DeviceAPIStub(devices: [])
        stub.revokeOutcome = .confirmedCleanupFailure(pushCleanupError: "disk full")
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.revokeDevice(d1)
        XCTAssertNotNil(store.revokeCleanupWarning)

        stub.revokeOutcome = .confirmedClean
        await store.revokeDevice(d1)
        XCTAssertNil(store.revokeCleanupWarning, "新撤销完成且 clean 时不应残留旧 warning")
    }
}
