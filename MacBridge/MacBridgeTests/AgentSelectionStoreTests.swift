import XCTest
@testable import CordCodeLink

@MainActor
final class AgentSelectionStoreTests: XCTestCase {
    private var directory: URL!

    override func setUpWithError() throws {
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("agent-selection-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
    }

    override func tearDownWithError() throws {
        if FileManager.default.fileExists(atPath: directory.path) {
            try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
            try FileManager.default.removeItem(at: directory)
        }
    }

    private var fileURL: URL { directory.appendingPathComponent("agent-selection.json") }

    private func readRaw() throws -> Data {
        let attributes = try FileManager.default.attributesOfItem(atPath: fileURL.path)
        let mode = (attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        XCTAssertEqual(mode & 0o777, 0o600)
        return try Data(contentsOf: fileURL)
    }

    func testInitialSelectionIsExplicitEmptyAndDurable() throws {
        let store = AgentSelectionStore(dataDir: directory.path)
        XCTAssertEqual(store.selection, AgentSelection.initial)
        XCTAssertNil(store.errorMessage)
        let raw = try readRaw()
        let decoded = try JSONDecoder().decode(AgentSelection.self, from: raw)
        XCTAssertEqual(decoded.enabledIDs, [])
    }

    func testExistingSelectionRestoredWithoutImplicitAllOn() throws {
        let existing = AgentSelection(schemaVersion: 1, revision: 7, enabledIDs: ["opencode-web", "claude"])
        try JSONEncoder().encode(existing).write(to: fileURL, options: [.atomic])
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fileURL.path)
        let store = AgentSelectionStore(dataDir: directory.path)
        XCTAssertEqual(store.selection, existing)
        XCTAssertNil(store.errorMessage)
    }

    func testTogglePersistsRevisionAndCanonicalOrder() throws {
        let store = AgentSelectionStore(dataDir: directory.path)
        _ = try store.setEnabled(true, for: "opencode-web")
        _ = try store.setEnabled(true, for: "claude")
        _ = try store.setEnabled(false, for: "opencode-web")
        _ = try store.setEnabled(true, for: "dsh-web")
        XCTAssertEqual(store.selection?.enabledIDs, ["claude", "dsh-web"])
        XCTAssertEqual(store.selection?.revision, 4)
        let persisted = try JSONDecoder().decode(AgentSelection.self, from: try readRaw())
        XCTAssertEqual(persisted.enabledIDs, ["claude", "dsh-web"])
        XCTAssertEqual(persisted.revision, 4)
    }

    func testCorruptFileExposesErrorAndIsNotOverwritten() throws {
        let raw = Data("{\"schemaVersion\":2,\"revision\":1,\"enabledIDs\":[\"claude\"]}".utf8)
        try raw.write(to: fileURL)
        let store = AgentSelectionStore(dataDir: directory.path)
        XCTAssertNil(store.selection)
        XCTAssertNotNil(store.errorMessage)
        XCTAssertEqual(try Data(contentsOf: fileURL), raw)
    }

    func testFailedWriteKeepsPreviousSelectionAndFile() throws {
        let store = AgentSelectionStore(dataDir: directory.path)
        _ = try store.setEnabled(true, for: "claude")
        let before = try Data(contentsOf: fileURL)
        struct WriteFailed: Error {}
        let failingStore = AgentSelectionStore(dataDir: directory.path) { _, _ in
            throw WriteFailed()
        }
        XCTAssertThrowsError(try failingStore.setEnabled(true, for: "dsh-web"))
        XCTAssertEqual(failingStore.selection?.enabledIDs, ["claude"])
        XCTAssertEqual(failingStore.selection?.revision, 1)
        XCTAssertEqual(store.selection?.enabledIDs, ["claude"])
        XCTAssertEqual(store.selection?.revision, 1)
        XCTAssertEqual(try? Data(contentsOf: fileURL), before)
        XCTAssertNotNil(failingStore.errorMessage)
    }

    func testUnknownProductAndRevisionExhaustionFailClosed() throws {
        let store = AgentSelectionStore(dataDir: directory.path)
        XCTAssertThrowsError(try store.setEnabled(true, for: "codex"))
        let maxRevision = AgentSelection(schemaVersion: 1, revision: UInt64.max, enabledIDs: [])
        try JSONEncoder().encode(maxRevision).write(to: fileURL, options: [.atomic])
        let exhausted = AgentSelectionStore(dataDir: directory.path)
        XCTAssertEqual(exhausted.selection, maxRevision)
        XCTAssertThrowsError(try exhausted.setEnabled(true, for: "claude"))
        XCTAssertThrowsError(try exhausted.resetAfterConfirmation())
    }

    func testResetRequiresExplicitCallAndIncrementsRevision() throws {
        let store = AgentSelectionStore(dataDir: directory.path)
        _ = try store.setEnabled(true, for: "grokbuild")
        _ = try store.resetAfterConfirmation()
        XCTAssertEqual(store.selection?.enabledIDs, [])
        XCTAssertEqual(store.selection?.revision, 2)
    }
}
