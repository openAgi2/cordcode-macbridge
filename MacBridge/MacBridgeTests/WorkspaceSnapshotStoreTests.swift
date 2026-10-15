import XCTest
@testable import CordCodeLink

@MainActor
final class WorkspaceSnapshotStoreTests: XCTestCase {
    private struct StubError: Error {}
    private struct Unauthorized: Error {}

    private final class SnapshotAPISpy: WorkspaceSnapshotAPIProviding {
        var results: [Result<WorkspaceSnapshot, Error>]
        private(set) var callCount = 0

        init(results: [Result<WorkspaceSnapshot, Error>]) {
            self.results = results
        }

        func getWorkspaceSnapshot() async throws -> WorkspaceSnapshot {
            defer { callCount += 1 }
            if results.isEmpty { throw StubError() }
            return try results.removeFirst().get()
        }
    }

    private func makeSnapshot(pid: Int32 = 77, epoch: UInt64 = 88) -> WorkspaceSnapshot {
        WorkspaceSnapshot(
            schemaVersion: 1,
            runtimeIdentity: .init(pid: pid, bridgeEpoch: epoch),
            selectionRevision: 2,
            requestedDrivers: ["claude"],
            activeDrivers: ["claude"],
            sampledAt: "2026-10-15T00:00:00Z",
            agents: [],
            devicePresence: [.init(deviceId: "device", transports: [.init(kind: "relay", directClass: nil)])],
            relay: .init(enabled: true, configured: true, connected: true)
        )
    }

    private func waitUntil(
        _ condition: @autoclosure () -> Bool,
        timeout: TimeInterval = 2,
        message: String = "condition not met",
        file: StaticString = #filePath,
        line: UInt = #line
    ) async {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return }
            await Task.yield()
            try? await Task.sleep(nanoseconds: 5_000_000)
        }
        XCTFail(message, file: file, line: line)
    }

    func testPublishesOnlyIdentityMatchingSnapshot() async {
        let spy = SnapshotAPISpy(results: [.success(makeSnapshot())])
        let store = WorkspaceSnapshotStore(
            cadence: 0.01, staleAfterFailures: 3, expectedIdentity: { .init(pid: 77, bridgeEpoch: 88) }
        )
        store.configure(apiClient: spy)
        await waitUntil(store.phase == .available(makeSnapshot()))
        XCTAssertEqual(store.lastUpdatedAt != nil, true)
        XCTAssertTrue(store.isObservationCurrent)
        store.stopPolling()
    }

    func testIdentityMismatchBecomesDiagnostic() async {
        let spy = SnapshotAPISpy(results: [.success(makeSnapshot(pid: 999, epoch: 888))])
        let store = WorkspaceSnapshotStore(
            cadence: 0.01, staleAfterFailures: 3, expectedIdentity: { .init(pid: 77, bridgeEpoch: 88) }
        )
        store.configure(apiClient: spy)
        await waitUntil(store.phase == .diagnostic(.identityMismatch))
        XCTAssertFalse(store.isObservationCurrent)
        store.stopPolling()
    }

    func testMissingExpectedIdentityIsNotCurrent() async {
        let spy = SnapshotAPISpy(results: [.success(makeSnapshot())])
        let store = WorkspaceSnapshotStore(
            cadence: 0.01, staleAfterFailures: 3, expectedIdentity: { nil }
        )
        store.configure(apiClient: spy)
        await waitUntil(store.phase == .diagnostic(.identityMismatch))
        XCTAssertFalse(store.isObservationCurrent)
        store.stopPolling()
    }

    func testStoppingPollingAndBackgroundingExpireOnlineTruth() async {
        let spy = SnapshotAPISpy(results: [.success(makeSnapshot()), .success(makeSnapshot())])
        let store = WorkspaceSnapshotStore(
            cadence: 0.01, staleAfterFailures: 3, expectedIdentity: { .init(pid: 77, bridgeEpoch: 88) }
        )
        store.configure(apiClient: spy)
        await waitUntil(store.isObservationCurrent)

        store.setAppActive(false)
        XCTAssertFalse(store.isObservationCurrent)

        store.setAppActive(true)
        await waitUntil(store.isObservationCurrent)
        store.stopPolling()
        XCTAssertFalse(store.isObservationCurrent)
    }

    func testNetworkFailuresExpireLastAvailableSnapshot() async {
        let snapshot = makeSnapshot()
        let spy = SnapshotAPISpy(results: [
            .success(snapshot), .failure(StubError()), .failure(StubError()), .failure(StubError()),
        ])
        let store = WorkspaceSnapshotStore(
            cadence: 0.01, staleAfterFailures: 3, expectedIdentity: { .init(pid: 77, bridgeEpoch: 88) }
        )
        store.configure(apiClient: spy)
        await waitUntil(store.phase == .available(snapshot))
        XCTAssertTrue(store.isObservationCurrent)
        await waitUntil(!store.isObservationCurrent, message: "first failed fetch must expire online truth")
        await waitUntil(store.phase == .diagnostic(.stale))
        XCTAssertNotNil(store.lastDiagnosticDetail)
        XCTAssertFalse(store.isObservationCurrent)
        store.stopPolling()
    }

    func testUnauthorizedIsImmediateDiagnostic() async {
        let spy = SnapshotAPISpy(results: [.failure(ManagementAPIClient.ManagementError.httpError(401))])
        let store = WorkspaceSnapshotStore(cadence: 0.01, staleAfterFailures: 3, expectedIdentity: { nil })
        store.configure(apiClient: spy)
        await waitUntil(store.phase == .diagnostic(.unauthorized))
        store.stopPolling()
    }
}
