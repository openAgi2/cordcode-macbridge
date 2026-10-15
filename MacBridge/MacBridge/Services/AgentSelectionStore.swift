import Combine
import Darwin
import Foundation

struct AgentSelection: Codable, Equatable {
    let schemaVersion: Int
    let revision: UInt64
    let enabledIDs: [String]

    static let initial = AgentSelection(schemaVersion: 1, revision: 0, enabledIDs: [])

    func validated() throws -> AgentSelection {
        guard schemaVersion == 1,
              Set(enabledIDs).count == enabledIDs.count,
              Set(enabledIDs).isSubset(of: Set(ProductAgentCatalog.ids)) else {
            throw AgentSelectionError.invalidFile
        }
        return self
    }
}

enum AgentSelectionError: LocalizedError {
    case invalidFile, revisionExhausted, persistence(String)
    var errorDescription: String? {
        switch self {
        case .invalidFile: return "接入选择文件损坏或版本不支持，请检查文件或明确重置接入选择。"
        case .revisionExhausted: return "接入选择版本已达到上限，无法保存。"
        case .persistence(let message): return "无法保存接入选择：\(message)"
        }
    }
}

/// The only preference writer. A corrupt file never becomes implicit all-on or empty success.
@MainActor
final class AgentSelectionStore: ObservableObject {
    @Published private(set) var selection: AgentSelection?
    @Published private(set) var errorMessage: String?
    let fileURL: URL
    private let persistSelection: (AgentSelection, URL) throws -> Void

    init(
        dataDir: String,
        persistSelection: @escaping (AgentSelection, URL) throws -> Void = AgentSelectionStore.persist
    ) {
        fileURL = URL(fileURLWithPath: dataDir).appendingPathComponent("agent-selection.json")
        self.persistSelection = persistSelection
        do {
            if FileManager.default.fileExists(atPath: fileURL.path) {
                selection = try Self.read(fileURL)
            } else {
                try persistSelection(.initial, fileURL)
                selection = .initial
            }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func isEnabled(_ id: String) -> Bool { selection?.enabledIDs.contains(id) == true }

    @discardableResult
    func setEnabled(_ enabled: Bool, for id: String) throws -> AgentSelection {
        guard ProductAgentCatalog.ids.contains(id), let current = selection else {
            throw AgentSelectionError.invalidFile
        }
        var ids = Set(current.enabledIDs)
        if enabled { ids.insert(id) } else { ids.remove(id) }
        let ordered = ProductAgentCatalog.ids.filter { ids.contains($0) }
        guard ordered != current.enabledIDs else { return current }
        guard current.revision < UInt64.max else { throw AgentSelectionError.revisionExhausted }
        return try save(AgentSelection(schemaVersion: 1, revision: current.revision + 1, enabledIDs: ordered))
    }

    /// Call only from the explicit reset confirmation action, never during loading/recovery.
    @discardableResult
    func resetAfterConfirmation() throws -> AgentSelection {
        if let current = selection, current.revision == UInt64.max { throw AgentSelectionError.revisionExhausted }
        return try save(AgentSelection(schemaVersion: 1, revision: (selection?.revision ?? 0) + 1, enabledIDs: []))
    }

    private func save(_ next: AgentSelection) throws -> AgentSelection {
        do {
            try persistSelection(try next.validated(), fileURL)
            // Publish only after durable write and strict read-back both succeeded.
            selection = next
            errorMessage = nil
            return next
        } catch {
            errorMessage = error.localizedDescription
            throw error
        }
    }

    private static func read(_ url: URL) throws -> AgentSelection {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        guard (attributes[.size] as? NSNumber)?.intValue ?? Int.max <= 16 * 1024 else {
            throw AgentSelectionError.invalidFile
        }
        return try JSONDecoder().decode(AgentSelection.self, from: Data(contentsOf: url)).validated()
    }

    private static func persist(_ value: AgentSelection, to url: URL) throws {
        let directory = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        // Also normalize an existing app-data directory; newly created directories inherit the value above.
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        let temporary = directory.appendingPathComponent(".agent-selection-\(UUID().uuidString)")
        let fd = open(temporary.path, O_WRONLY | O_CREAT | O_EXCL, mode_t(0o600))
        guard fd >= 0 else { throw posixError() }
        defer { close(fd); try? FileManager.default.removeItem(at: temporary) }
        let data = try JSONEncoder().encode(value)
        try data.withUnsafeBytes { bytes in
            var offset = 0
            while offset < bytes.count {
                let count = Darwin.write(fd, bytes.baseAddress!.advanced(by: offset), bytes.count - offset)
                if count < 0 && errno == EINTR { continue }
                guard count > 0 else { throw posixError() }
                offset += count
            }
        }
        guard fsync(fd) == 0 else { throw posixError() }
        guard rename(temporary.path, url.path) == 0 else { throw posixError() }
        let parent = open(directory.path, O_RDONLY)
        guard parent >= 0 else { throw posixError() }
        defer { close(parent) }
        guard fsync(parent) == 0 else { throw posixError() }
        guard try read(url) == value else { throw AgentSelectionError.invalidFile }
    }

    private static func posixError() -> AgentSelectionError {
        .persistence(String(cString: strerror(errno)))
    }
}
