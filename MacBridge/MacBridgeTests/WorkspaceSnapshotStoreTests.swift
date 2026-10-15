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

    private func makeSnapshotAgent() -> WorkspaceSnapshot.Agent {
        WorkspaceSnapshot.Agent(
            id: "opencode-web",
            installation: .init(
                state: "installed",
                errorCode: nil,
                reason: nil,
                artifactKind: "cli",
                installMethod: "npm"
            ),
            installGuidance: .init(
                documentationUrl: "https://opencode.ai/docs/",
                recipe: .init(
                    executable: "/tools/bin/npm",
                    args: ["install", "-g", "opencode-ai"],
                    environment: [.init(name: "PATH", value: "/tools/bin")],
                    display: "PATH='/tools/bin' '/tools/bin/npm' 'install' '-g' 'opencode-ai'"
                ),
                reason: nil
            ),
            version: "1.19.0",
            versionBuild: nil,
            versionSource: "npm_package_json",
            versionCheck: .init(
                state: "update_available",
                checkedAt: "2026-10-15T00:00:00Z",
                freshUntil: "2026-10-15T06:00:00Z",
                source: "npm_registry",
                channel: "latest",
                installedVersion: "1.19.0",
                targetVersion: "1.19.1",
                code: nil,
                reason: nil,
                updateRecipe: .init(
                    executable: "/tools/bin/npm",
                    args: ["install", "-g", "opencode-ai@1.19.1"],
                    environment: [.init(name: "PATH", value: "/tools/bin")],
                    display: "PATH='/tools/bin' '/tools/bin/npm' 'install' '-g' 'opencode-ai@1.19.1'"
                )
            ),
            readiness: nil
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

    @MainActor
    func testBackendViewModelMergesSnapshotVersionAndInstallGuidance() {
        let viewModel = BackendStatusViewModel()
        viewModel.agents = [
            BackendAgentStatus(
                id: "opencode-web",
                displayName: "OpenCode Web",
                kind: "opencode-web",
                status: "not_detected",
                reason: nil,
                isRefreshing: false,
                requiresPollingForExternalTurns: false
            )
        ]
        let snapshot = WorkspaceSnapshot(
            schemaVersion: 1,
            runtimeIdentity: .init(pid: 77, bridgeEpoch: 88),
            selectionRevision: 1,
            requestedDrivers: [],
            activeDrivers: [],
            sampledAt: "2026-10-15T00:00:00Z",
            agents: [makeSnapshotAgent()],
            devicePresence: [],
            relay: .init(enabled: true, configured: true, connected: false)
        )

        viewModel.applyWorkspaceSnapshotPhase(.available(snapshot), isObservationCurrent: true)

        XCTAssertEqual(viewModel.agents.first?.snapshotVersion, "1.19.0")
        XCTAssertEqual(viewModel.agents.first?.versionCheck?.targetVersion, "1.19.1")
        XCTAssertEqual(viewModel.agents.first?.installGuidance?.recipe?.display, "PATH='/tools/bin' '/tools/bin/npm' 'install' '-g' 'opencode-ai'")

        viewModel.applyWorkspaceSnapshotPhase(.available(snapshot), isObservationCurrent: false)
        XCTAssertEqual(viewModel.agents.first?.versionCheck?.targetVersion, "1.19.1")
    }
}
