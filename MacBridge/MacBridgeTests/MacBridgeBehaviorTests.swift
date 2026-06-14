import XCTest
@testable import CCCodeBridge

final class MacBridgeBehaviorTests: XCTestCase {
    func testGoDurationFormatting() {
        XCTAssertEqual(BridgeStatusViewModel.formatUptime("42.381s"), L10n.overviewUptimeUnderMinute)
        XCTAssertEqual(BridgeStatusViewModel.formatUptime("12m4.2s"), String(format: L10n.overviewUptimeMinutes, 12))
        XCTAssertEqual(
            BridgeStatusViewModel.formatUptime("1h24m3.1s"),
            String(format: L10n.overviewUptimeHoursMinutes, 1, 24)
        )
        XCTAssertEqual(BridgeStatusViewModel.formatUptime("unknown"), "unknown")
    }

    @MainActor
    func testOverviewKeepsIndependentRequestResults() async {
        let viewModel = BridgeStatusViewModel()
        viewModel.configure(apiClient: OverviewClientStub(
            status: .success(ManagementStatus(
                status: "ready",
                bridgeId: nil,
                displayName: nil,
                iosPort: 9999,
                uptime: "2m",
                version: "test"
            )),
            remote: .failure(TestError.failed)
        ))

        await viewModel.refreshOverviewData()

        XCTAssertEqual(viewModel.managementStatus?.version, "test")
        XCTAssertNil(viewModel.relayConfigured)
        XCTAssertNotNil(viewModel.overviewDataError)
    }

    @MainActor
    func testOverviewPortComesFromRuntimeConfig() {
        let manager = RuntimeManager(config: RuntimeConfig(
            executablePath: "/usr/bin/false",
            port: 8777,
            dataDir: "/tmp/cccode-test-\(UUID().uuidString)",
            logDir: "/tmp"
        ))
        let viewModel = BridgeStatusViewModel()
        viewModel.runtimeManager = manager

        XCTAssertEqual(viewModel.bridgePort, 8777)
    }

    @MainActor
    func testPairingExpiryIsIdempotentAndStopsActivity() {
        let viewModel = PairingViewModel()
        viewModel.uiState = .waitingForClaim(
            sessionId: "session",
            manualCode: "123456",
            qrPayload: "cccode://pair"
        )
        viewModel.beginCountdown(expiresAt: Date(timeIntervalSinceNow: -1))

        XCTAssertEqual(viewModel.uiState, .expired)
        XCTAssertEqual(viewModel.remainingSeconds, 0)
        XCTAssertFalse(viewModel.hasActivePairingTasks)

        viewModel.expireSession()
        XCTAssertEqual(viewModel.uiState, .expired)
    }

    @MainActor
    func testPairingClaimStopsCountdownActivity() async {
        let viewModel = PairingViewModel()
        viewModel.uiState = .waitingForClaim(
            sessionId: "session",
            manualCode: "123456",
            qrPayload: "cccode://pair"
        )
        viewModel.beginCountdown(expiresAt: Date(timeIntervalSinceNow: 120))
        XCTAssertTrue(viewModel.hasActivePairingTasks)

        await viewModel.applyPairingStatus(PairingSessionStatus(
            id: "session",
            state: "claimed",
            claimingDeviceName: "iPhone",
            claimingPlatform: "ios",
            expiresAt: nil
        ))

        XCTAssertEqual(viewModel.uiState, .claimed(deviceName: "iPhone", platform: "ios"))
        XCTAssertFalse(viewModel.hasActivePairingTasks)
    }

    func testBackendStatusMappingUsesProductSemantics() {
        XCTAssertEqual(BackendStatusText.display("available"), L10n.statusReady)
        XCTAssertEqual(BackendStatusText.display("not_logged_in"), L10n.statusLoginRequired)
        XCTAssertEqual(BackendStatusText.display("permission_denied"), L10n.statusPermissionDenied)
    }

    @MainActor
    func testRegeneratePasswordOnlyChangesDraft() throws {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }

        let credentialsURL = directory.appendingPathComponent("credentials.json")
        let original: [String: String] = [
            "opencode_user": "opencode",
            "opencode_pass": "original",
        ]
        try JSONSerialization.data(withJSONObject: original).write(to: credentialsURL)

        var restartCount = 0
        let viewModel = SettingsViewModel(dataDir: directory.path) {
            restartCount += 1
        }
        viewModel.regeneratePassword()

        XCTAssertNotEqual(viewModel.opencodePass, "original")
        XCTAssertTrue(viewModel.isCredentialsDirty)
        XCTAssertEqual(restartCount, 0)
        let persisted = try Data(contentsOf: credentialsURL)
        let json = try XCTUnwrap(
            JSONSerialization.jsonObject(with: persisted) as? [String: String]
        )
        XCTAssertEqual(json["opencode_pass"], "original")
    }

    @MainActor
    func testSettingsFeedbackStatesAreIndependent() {
        let viewModel = SettingsViewModel(dataDir: "/tmp/nonexistent-\(UUID().uuidString)") {}
        viewModel.displayNameFeedback = .success("name")
        viewModel.credentialsFeedback = .failure("credentials")

        XCTAssertEqual(viewModel.displayNameFeedback, .success("name"))
        XCTAssertEqual(viewModel.credentialsFeedback, .failure("credentials"))
    }
}

private enum TestError: Error {
    case failed
}

private struct OverviewClientStub: OverviewAPIProviding {
    let status: Result<ManagementStatus, Error>
    let remote: Result<RemoteStatus, Error>

    func getStatus() async throws -> ManagementStatus {
        try status.get()
    }

    func getRemoteStatus() async throws -> RemoteStatus {
        try remote.get()
    }
}
