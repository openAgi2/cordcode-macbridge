import XCTest
@testable import CordCodeLink

// P0-4 状态所有权迁移测试：设备列表/加载/错误状态迁入共享 DeviceStore。
// 验证三类状态（列表、空、加载失败）与撤销行为在重构后与现状一致。
@MainActor
final class DeviceStoreTests: XCTestCase {

    private final class DeviceAPIStub: DeviceAPIProviding {
        var devices: [TrustedDevice]
        var error: Error?
        var revokeError: Error?
        var pushCleanupError: String?
        private(set) var revokedDeviceIds: [String] = []

        init(devices: [TrustedDevice], error: Error? = nil, revokeError: Error? = nil) {
            self.devices = devices
            self.error = error
            self.revokeError = revokeError
        }

        func listDevices() async throws -> [TrustedDevice] {
            if let error { throw error }
            return devices
        }

        func revokeDevice(_ deviceId: String) async throws -> DeviceRevocation {
            revokedDeviceIds.append(deviceId)
            if let revokeError { throw revokeError }
            devices.removeAll { $0.deviceId == deviceId }
            return DeviceRevocation(pushCleanupError: pushCleanupError)
        }
    }

    private struct StubError: Error {}

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
        let store = DeviceStore()
        store.configure(apiClient: DeviceAPIStub(devices: [], error: StubError()))

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

    func testRevokeCallsApiThenReloads() async {
        let d1 = TrustedDevice(deviceId: "d1", displayName: nil, platform: "ios", createdAt: nil, lastSeenAt: nil)
        let stub = DeviceAPIStub(devices: [d1])
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.loadDevices()
        await store.revokeDevice(d1)

        XCTAssertEqual(stub.revokedDeviceIds, ["d1"])
        XCTAssertTrue(store.devices.isEmpty, "撤销成功后应刷新列表，设备应消失")
        XCTAssertFalse(store.isRevoking, "撤销完成后 isRevoking 应复位")
    }

    func testRevokeFailureSetsError() async {
        let d1 = TrustedDevice(deviceId: "d1", displayName: nil, platform: "ios", createdAt: nil, lastSeenAt: nil)
        let stub = DeviceAPIStub(devices: [d1], revokeError: StubError())
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.loadDevices()
        await store.revokeDevice(d1)

        XCTAssertNotNil(store.devicesError)
        XCTAssertFalse(store.isRevoking)
    }

    // 撤销成功但订阅清理失败（pushCleanupError）：发布警告、不算撤销失败，
    // 且 dismiss 可清除。
    func testRevokeCleanupFailurePublishesWarningWithoutError() async throws {
        let d1 = TrustedDevice(deviceId: "d1", displayName: nil, platform: "ios", createdAt: nil, lastSeenAt: nil)
        let stub = DeviceAPIStub(devices: [d1])
        stub.pushCleanupError = "persist failed: disk full"
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.loadDevices()
        await store.revokeDevice(d1)

        XCTAssertNil(store.devicesError, "撤销本身成功，不应设置 devicesError")
        XCTAssertTrue(store.devices.isEmpty, "撤销成功后设备应从列表消失")
        let warning = try XCTUnwrap(store.revokeCleanupWarning)
        XCTAssertTrue(warning.contains("persist failed: disk full"), "警告应包含服务端错误详情：\(warning)")
        XCTAssertFalse(store.isRevoking)

        store.dismissRevokeCleanupWarning()
        XCTAssertNil(store.revokeCleanupWarning)
    }

    // 清理成功的撤销：不发布警告。
    func testRevokeCleanSuccessKeepsWarningNil() async {
        let d1 = TrustedDevice(deviceId: "d1", displayName: nil, platform: "ios", createdAt: nil, lastSeenAt: nil)
        let stub = DeviceAPIStub(devices: [d1])
        let store = DeviceStore()
        store.configure(apiClient: stub)

        await store.loadDevices()
        await store.revokeDevice(d1)

        XCTAssertNil(store.revokeCleanupWarning)
        XCTAssertNil(store.devicesError)
    }
}
