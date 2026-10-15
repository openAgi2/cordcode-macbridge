import XCTest
@testable import CordCodeLink

final class AppDependenciesTests: XCTestCase {
    func testReadsOpenCodeCredentialsFromLaunchAgent() throws {
        let url = try writeLaunchAgent(environment: [
            "OPENCODE_SERVER_USERNAME": "opencode",
            "OPENCODE_SERVER_PASSWORD": "local-test-password",
        ])
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }

        let credentials = OpenCodeLaunchAgentCredentials.read(from: url)

        XCTAssertEqual(credentials?.user, "opencode")
        XCTAssertEqual(credentials?.password, "local-test-password")
    }

    func testRejectsIncompleteOpenCodeLaunchAgentCredentials() throws {
        let url = try writeLaunchAgent(environment: [
            "OPENCODE_SERVER_USERNAME": "opencode",
        ])
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }

        XCTAssertNil(OpenCodeLaunchAgentCredentials.read(from: url))
    }

    @MainActor
    func testValidAgentSelectionResolvesForProductionStartup() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = AgentSelectionStore(dataDir: directory.path)
        _ = try store.setEnabled(true, for: "codex-remote")

        XCTAssertEqual(
            AppDependencies.AgentSelectionStartup.resolve(store),
            .apply(AgentSelection(schemaVersion: 1, revision: 1, enabledIDs: ["codex-remote"]))
        )
    }

    @MainActor
    func testCorruptAgentSelectionBlocksStartupWithoutImplicitAllOn() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("agent-selection.json")
        try Data("not-json".utf8).write(to: file)
        let store = AgentSelectionStore(dataDir: directory.path)

        if case .blocked = AppDependencies.AgentSelectionStartup.resolve(store) {
            XCTAssertNil(store.selection)
        } else {
            XCTFail("corrupt selection must block startup")
        }
    }

    private func writeLaunchAgent(environment: [String: String]) throws -> URL {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true
        )

        let url = directory.appendingPathComponent("com.opencode.server.plist")
        let data = try PropertyListSerialization.data(
            fromPropertyList: ["EnvironmentVariables": environment],
            format: .xml,
            options: 0
        )
        try data.write(to: url)
        return url
    }

    private func makeTemporaryDirectory() throws -> URL {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        return directory
    }
}
