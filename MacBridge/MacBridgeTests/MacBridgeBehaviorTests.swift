import XCTest
@testable import CordCodeLink

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
    func testOverviewRetriesTransientRelayStatusFailure() async {
        let expected = RemoteStatus(
            localURL: nil,
            tailscaleURL: nil,
            remoteURL: nil,
            remoteURLs: nil,
            connectionMode: nil,
            remoteConfigured: nil,
            includeTailscale: nil,
            includeRemote: nil,
            remoteAnalysis: nil,
            listenStatus: nil,
            relay: .init(configured: true, endpoint: nil, routeId: nil)
        )
        let viewModel = BridgeStatusViewModel()
        viewModel.configure(apiClient: RetryingOverviewClient(remoteResults: [.failure(TestError.failed), .success(expected)]))

        await viewModel.refreshOverviewData()
        XCTAssertNil(viewModel.relayConfigured)

        try? await Task.sleep(for: .seconds(3.2))

        XCTAssertEqual(viewModel.relayConfigured, true)
        XCTAssertNil(viewModel.overviewDataError)
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

    // MARK: - T03: RuntimeManager -opencode-url argv/env

    func testProcessArgumentsIncludesOpenCodeURLNotPassword() {
        let config = RuntimeConfig(
            executablePath: "/usr/bin/false",
            dataDir: "/tmp/cccode-t-\(UUID().uuidString)",
            logDir: "/tmp",
            opencodeUser: "alice",
            opencodePass: "super-secret-password",
            opencodeURL: "http://127.0.0.1:4096",
            opencodeSource: .externalHttp
        )
        let args = RuntimeManager.processArguments(for: config)
        guard let idx = args.firstIndex(of: "-opencode-url") else {
            return XCTFail("argv must contain -opencode-url")
        }
        XCTAssertEqual(args[idx + 1], "http://127.0.0.1:4096")
        // password 是 secret，绝不出现在 argv。
        XCTAssertFalse(args.contains("super-secret-password"))
        // external_http 不隐式写 64667。
        XCTAssertFalse(args.contains("64667"))
    }

    func testProcessArgumentsOmitsOpenCodeURLAndNoImplicit64667WhenEmpty() {
        // endpoint 不可解析（disabled / not_configured）：argv 不含 -opencode-url，
        // 也不得隐式回落 64667。
        let config = RuntimeConfig(
            executablePath: "/usr/bin/false",
            dataDir: "/tmp/cccode-t-\(UUID().uuidString)",
            logDir: "/tmp",
            opencodeURL: "",
            opencodeSource: .disabled
        )
        let args = RuntimeManager.processArguments(for: config)
        XCTAssertFalse(args.contains("-opencode-url"))
        XCTAssertFalse(args.contains("64667"))
        XCTAssertFalse(args.contains("localhost"))
    }

    func testProcessEnvironmentCarriesOpenCodeCreds() {
        let config = RuntimeConfig(
            executablePath: "/usr/bin/false",
            dataDir: "/tmp/cccode-t-\(UUID().uuidString)",
            logDir: "/tmp",
            opencodeUser: "alice",
            opencodePass: "super-secret-password",
            opencodeURL: "http://127.0.0.1:4096",
            opencodeSource: .externalHttp
        )
        let env = RuntimeManager.processEnvironment(
            for: config,
            managementToken: "mgmt-token",
            existingEnvironment: ["PATH": "/usr/bin"]
        )
        XCTAssertEqual(env["OPENCODE_SERVER_USERNAME"], "alice")
        XCTAssertEqual(env["OPENCODE_SERVER_PASSWORD"], "super-secret-password")
        XCTAssertEqual(env["CORDCODE_MANAGEMENT_TOKEN"], "mgmt-token")
        XCTAssertNotNil(env["PATH"])
    }

    func testProcessEnvironmentDropsOpenCodeCredsWhenEmpty() {
        let config = RuntimeConfig(
            executablePath: "/usr/bin/false",
            dataDir: "/tmp/cccode-t-\(UUID().uuidString)",
            logDir: "/tmp",
            opencodeUser: "",
            opencodePass: "",
            opencodeSource: .disabled
        )
        let env = RuntimeManager.processEnvironment(
            for: config,
            managementToken: "tok",
            existingEnvironment: [
                "PATH": "/usr/bin",
                "OPENCODE_SERVER_USERNAME": "stale",
                "OPENCODE_SERVER_PASSWORD": "stale",
            ]
        )
        XCTAssertNil(env["OPENCODE_SERVER_USERNAME"])
        XCTAssertNil(env["OPENCODE_SERVER_PASSWORD"])
    }

    // MARK: - T04: Desktop config sync

    private func temporaryDesktopDir() throws -> URL {
        let tmp = FileManager.default.temporaryDirectory
            .appendingPathComponent("cccode-desktop-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)
        return tmp
    }

    private func readServerList(in desktopDir: URL) throws -> [[String: Any]] {
        let server = try readDesktopServer(in: desktopDir)
        let list = (server["list"] as? [Any]) ?? []
        return list.compactMap { $0 as? [String: Any] }
    }

    private func readDesktopServer(in desktopDir: URL) throws -> [String: Any] {
        let globalPath = desktopDir.appendingPathComponent("opencode.global.dat")
        let data = try Data(contentsOf: globalPath)
        let root = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let serverStr = try XCTUnwrap(root["server"] as? String)
        let serverData = try XCTUnwrap(serverStr.data(using: .utf8))
        return try XCTUnwrap(JSONSerialization.jsonObject(with: serverData) as? [String: Any])
    }

    func testDesktopConfigWritesExternalHttpEndpointURL() throws {
        let desktopDir = try temporaryDesktopDir()
        defer { try? FileManager.default.removeItem(at: desktopDir) }

        RuntimeManager.configureOpenCodeDesktopSettings(
            desktopDir: desktopDir,
            serverURL: "http://127.0.0.1:4096",
            username: "opencode",
            password: "p"
        )

        let list = try readServerList(in: desktopDir)
        guard let first = list.first,
              let http = first["http"] as? [String: Any] else {
            return XCTFail("server list must contain an http entry")
        }
        XCTAssertEqual(first["type"] as? String, "http")
        XCTAssertEqual(http["url"] as? String, "http://127.0.0.1:4096")
        XCTAssertEqual(http["username"] as? String, "opencode")

        // opencode.settings defaultServerUrl = 同一 endpoint。
        let settingsData = try Data(contentsOf: desktopDir.appendingPathComponent("opencode.settings"))
        let settings = try XCTUnwrap(JSONSerialization.jsonObject(with: settingsData) as? [String: Any])
        XCTAssertEqual(settings["defaultServerUrl"] as? String, "http://127.0.0.1:4096")

        // external_http endpoint 不得是 64667。
        let listJSON = String(data: try JSONSerialization.data(withJSONObject: list), encoding: .utf8) ?? ""
        XCTAssertFalse(listJSON.contains("64667"))
    }

    func testDesktopConfigDedupsAndPreservesOtherServers() throws {
        let desktopDir = try temporaryDesktopDir()
        defer { try? FileManager.default.removeItem(at: desktopDir) }

        // 预置：一个用户手动添加的 server + 一个同 URL 旧条目。
        let userServer: [String: Any] = [
            "type": "http",
            "http": ["url": "http://127.0.0.1:9999", "username": "u", "password": "x"],
        ]
        let staleDup: [String: Any] = [
            "type": "http",
            "http": ["url": "http://127.0.0.1:4096", "username": "old", "password": "old"],
        ]
        let server: [String: Any] = ["list": [userServer, staleDup], "currentSidecarUrl": "http://127.0.0.1:9999"]
        let serverStr = String(data: try JSONSerialization.data(withJSONObject: server), encoding: .utf8)!
        let globalPath = desktopDir.appendingPathComponent("opencode.global.dat")
        try JSONSerialization.data(withJSONObject: ["server": serverStr]).write(to: globalPath)

        RuntimeManager.configureOpenCodeDesktopSettings(
            desktopDir: desktopDir,
            serverURL: "http://127.0.0.1:4096",
            username: "opencode",
            password: "p"
        )

        let list = try readServerList(in: desktopDir)
        // endpoint URL 应排在首位。
        guard let first = list.first,
              let firstURL = (first["http"] as? [String: Any])?["url"] as? String else {
            return XCTFail("first entry must be the configured endpoint")
        }
        XCTAssertEqual(firstURL, "http://127.0.0.1:4096")
        // 同 URL 旧条目被去重，只剩一个 4096。
        let count4096 = list.filter {
            (($0["http"] as? [String: Any])?["url"] as? String) == "http://127.0.0.1:4096"
        }.count
        XCTAssertEqual(count4096, 1, "duplicate endpoint should be removed")
        // 用户手动的其它 server 不被删除。
        let count9999 = list.filter {
            (($0["http"] as? [String: Any])?["url"] as? String) == "http://127.0.0.1:9999"
        }.count
        XCTAssertEqual(count9999, 1, "other user servers must be preserved")
    }

    func testDesktopConfigMigratesProjectsToExternalHttpEndpoint() throws {
        let desktopDir = try temporaryDesktopDir()
        defer { try? FileManager.default.removeItem(at: desktopDir) }

        let legacyProjects: [[String: Any]] = [
            ["worktree": "/Users/test/Projects/Chat", "expanded": true],
        ]
        let server: [String: Any] = [
            "list": [
                [
                    "type": "http",
                    "http": ["url": "http://127.0.0.1:64667", "username": "opencode", "password": "old"],
                ],
            ],
            "currentSidecarUrl": "http://127.0.0.1:64667",
            "projects": [
                "local": [
                    ["worktree": "/Users/test/Projects/Local", "expanded": true],
                    ["worktree": "/Users/test/Projects/Chat", "expanded": false],
                ],
                "http://127.0.0.1:64667": legacyProjects,
            ],
            "lastProject": [
                "local": "/Users/test/Projects/Local",
                "http://127.0.0.1:64667": "/Users/test/Projects/Chat",
            ],
        ]
        let serverStr = String(data: try JSONSerialization.data(withJSONObject: server), encoding: .utf8)!
        let globalPath = desktopDir.appendingPathComponent("opencode.global.dat")
        try JSONSerialization.data(withJSONObject: ["server": serverStr]).write(to: globalPath)

        RuntimeManager.configureOpenCodeDesktopSettings(
            desktopDir: desktopDir,
            serverURL: "http://127.0.0.1:4096",
            username: "opencode",
            password: "p"
        )

        let updated = try readDesktopServer(in: desktopDir)
        let projects = try XCTUnwrap(updated["projects"] as? [String: Any])
        let migrated = try XCTUnwrap(projects["http://127.0.0.1:4096"] as? [[String: Any]])
        XCTAssertEqual(migrated.compactMap { $0["worktree"] as? String }, [
            "/Users/test/Projects/Local",
            "/Users/test/Projects/Chat",
        ])

        let lastProject = try XCTUnwrap(updated["lastProject"] as? [String: Any])
        XCTAssertEqual(lastProject["http://127.0.0.1:4096"] as? String, "/Users/test/Projects/Local")
    }

    func testDesktopConfigMergesExistingExternalHttpProjects() throws {
        let desktopDir = try temporaryDesktopDir()
        defer { try? FileManager.default.removeItem(at: desktopDir) }

        let server: [String: Any] = [
            "currentSidecarUrl": "http://127.0.0.1:64667",
            "projects": [
                "http://127.0.0.1:64667": [["worktree": "/Users/test/Projects/Old", "expanded": true]],
                "local": [["worktree": "/Users/test/Projects/Local", "expanded": true]],
                "http://127.0.0.1:4096": [["worktree": "/Users/test/Projects/New", "expanded": false]],
            ],
            "lastProject": [
                "http://127.0.0.1:64667": "/Users/test/Projects/Old",
                "local": "/Users/test/Projects/Local",
                "http://127.0.0.1:4096": "/Users/test/Projects/New",
            ],
        ]
        let serverStr = String(data: try JSONSerialization.data(withJSONObject: server), encoding: .utf8)!
        let globalPath = desktopDir.appendingPathComponent("opencode.global.dat")
        try JSONSerialization.data(withJSONObject: ["server": serverStr]).write(to: globalPath)

        RuntimeManager.configureOpenCodeDesktopSettings(
            desktopDir: desktopDir,
            serverURL: "http://127.0.0.1:4096",
            username: "opencode",
            password: "p"
        )

        let updated = try readDesktopServer(in: desktopDir)
        let projects = try XCTUnwrap(updated["projects"] as? [String: Any])
        let retained = try XCTUnwrap(projects["http://127.0.0.1:4096"] as? [[String: Any]])
        XCTAssertEqual(retained.compactMap { $0["worktree"] as? String }, [
            "/Users/test/Projects/New",
            "/Users/test/Projects/Local",
            "/Users/test/Projects/Old",
        ])

        let lastProject = try XCTUnwrap(updated["lastProject"] as? [String: Any])
        XCTAssertEqual(lastProject["http://127.0.0.1:4096"] as? String, "/Users/test/Projects/New")
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

@MainActor
private final class RetryingOverviewClient: OverviewAPIProviding {
    private var remoteResults: [Result<RemoteStatus, Error>]

    init(remoteResults: [Result<RemoteStatus, Error>]) {
        self.remoteResults = remoteResults
    }

    func getStatus() async throws -> ManagementStatus {
        ManagementStatus(status: "ready", bridgeId: nil, displayName: nil, iosPort: nil, uptime: nil, version: nil)
    }

    func getRemoteStatus() async throws -> RemoteStatus {
        try remoteResults.removeFirst().get()
    }
}
