import Foundation

enum ManagementRequestCodec {
    static func encode<T: Encodable>(_ value: T) throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return try encoder.encode(value)
    }
}

enum ManagementCodecError: Error, Equatable {
    case invalidJSON
    case duplicateKey(String)
    case unexpectedKeys
    case missingKey(String)
    case invalidType(String)
    case invalidInteger(String)
    case unsupportedVersion(UInt64)
    case invalidValue(String)
}

private indirect enum ManagementJSONValue {
    case object([(String, ManagementJSONValue)])
    case array([ManagementJSONValue])
    case string(String)
    case number(String)
    case bool(Bool)
    case null
}

private struct ManagementJSONParser {
    let bytes: [UInt8]
    var index = 0

    init(_ data: Data) { bytes = Array(data) }

    mutating func parse() throws -> ManagementJSONValue {
        skipWhitespace()
        let value = try parseValue()
        skipWhitespace()
        guard index == bytes.count else { throw ManagementCodecError.invalidJSON }
        return value
    }

    mutating private func parseValue() throws -> ManagementJSONValue {
        guard index < bytes.count else { throw ManagementCodecError.invalidJSON }
        switch bytes[index] {
        case 0x7B: return try parseObject()
        case 0x5B: return try parseArray()
        case 0x22: return .string(try parseString())
        case 0x74: try consume("true"); return .bool(true)
        case 0x66: try consume("false"); return .bool(false)
        case 0x6E: try consume("null"); return .null
        case 0x2D, 0x30...0x39: return .number(try parseNumber())
        default: throw ManagementCodecError.invalidJSON
        }
    }

    mutating private func parseObject() throws -> ManagementJSONValue {
        index += 1
        skipWhitespace()
        var pairs: [(String, ManagementJSONValue)] = []
        var seen = Set<String>()
        if consumeIf(0x7D) { return .object(pairs) }
        while true {
            guard index < bytes.count, bytes[index] == 0x22 else { throw ManagementCodecError.invalidJSON }
            let key = try parseString()
            guard seen.insert(key).inserted else { throw ManagementCodecError.duplicateKey(key) }
            skipWhitespace()
            guard consumeIf(0x3A) else { throw ManagementCodecError.invalidJSON }
            skipWhitespace()
            pairs.append((key, try parseValue()))
            skipWhitespace()
            if consumeIf(0x7D) { return .object(pairs) }
            guard consumeIf(0x2C) else { throw ManagementCodecError.invalidJSON }
            skipWhitespace()
        }
    }

    mutating private func parseArray() throws -> ManagementJSONValue {
        index += 1
        skipWhitespace()
        var values: [ManagementJSONValue] = []
        if consumeIf(0x5D) { return .array(values) }
        while true {
            values.append(try parseValue())
            skipWhitespace()
            if consumeIf(0x5D) { return .array(values) }
            guard consumeIf(0x2C) else { throw ManagementCodecError.invalidJSON }
            skipWhitespace()
        }
    }

    mutating private func parseString() throws -> String {
        let start = index
        guard consumeIf(0x22) else { throw ManagementCodecError.invalidJSON }
        var escaped = false
        while index < bytes.count {
            let byte = bytes[index]
            index += 1
            if escaped {
                escaped = false
                continue
            }
            if byte == 0x5C {
                escaped = true
            } else if byte == 0x22 {
                let slice = Data(bytes[start..<index])
                guard let value = try? JSONDecoder().decode(String.self, from: slice) else {
                    throw ManagementCodecError.invalidJSON
                }
                return value
            } else if byte < 0x20 {
                throw ManagementCodecError.invalidJSON
            }
        }
        throw ManagementCodecError.invalidJSON
    }

    mutating private func parseNumber() throws -> String {
        let start = index
        while index < bytes.count {
            switch bytes[index] {
            case 0x2B, 0x2D, 0x2E, 0x30...0x39, 0x45, 0x65: index += 1
            default:
                return String(decoding: bytes[start..<index], as: UTF8.self)
            }
        }
        return String(decoding: bytes[start..<index], as: UTF8.self)
    }

    mutating private func consume(_ literal: StaticString) throws {
        let expected = Array(String(describing: literal).utf8)
        guard index + expected.count <= bytes.count,
              Array(bytes[index..<(index + expected.count)]) == expected else {
            throw ManagementCodecError.invalidJSON
        }
        index += expected.count
    }

    mutating private func consumeIf(_ byte: UInt8) -> Bool {
        guard index < bytes.count, bytes[index] == byte else { return false }
        index += 1
        return true
    }

    mutating private func skipWhitespace() {
        while index < bytes.count, [0x20, 0x09, 0x0A, 0x0D].contains(bytes[index]) { index += 1 }
    }
}

private struct ManagementJSONObject {
    let values: [String: ManagementJSONValue]

    init(_ value: ManagementJSONValue, exactKeys: Set<String>) throws {
        guard case let .object(pairs) = value else { throw ManagementCodecError.invalidJSON }
        let keys = Set(pairs.map(\.0))
        guard keys == exactKeys else { throw ManagementCodecError.unexpectedKeys }
        values = Dictionary(uniqueKeysWithValues: pairs)
    }

    func value(_ key: String) throws -> ManagementJSONValue {
        guard let value = values[key] else { throw ManagementCodecError.missingKey(key) }
        return value
    }

    func string(_ key: String) throws -> String {
        guard case let .string(value) = try value(key) else { throw ManagementCodecError.invalidType(key) }
        return value
    }

    func uint64(_ key: String) throws -> UInt64 {
        guard case let .number(raw) = try value(key), !raw.isEmpty,
              raw.utf8.allSatisfy({ $0 >= 0x30 && $0 <= 0x39 }),
              let value = UInt64(raw) else { throw ManagementCodecError.invalidInteger(key) }
        return value
    }

    func uint32(_ key: String) throws -> UInt32 {
        let value = try uint64(key)
        guard value <= UInt64(UInt32.max) else { throw ManagementCodecError.invalidInteger(key) }
        return UInt32(value)
    }

    func int32Positive(_ key: String) throws -> Int32 {
        let value = try uint64(key)
        guard value > 0, value <= UInt64(Int32.max) else { throw ManagementCodecError.invalidInteger(key) }
        return Int32(value)
    }

    func bool(_ key: String) throws -> Bool {
        guard case let .bool(value) = try value(key) else { throw ManagementCodecError.invalidType(key) }
        return value
    }

    /// 可选的 `{"<backendID>": <uint32>}` 映射；键缺失返回 nil，值非法则抛错。
    func optionalUint32Map(_ key: String) throws -> [String: UInt32]? {
        guard let entry = values[key] else { return nil }
        guard case let .object(pairs) = entry else { throw ManagementCodecError.invalidType(key) }
        var out: [String: UInt32] = [:]
        for (backend, rawValue) in pairs {
            guard case let .number(raw) = rawValue, !raw.isEmpty,
                  raw.utf8.allSatisfy({ $0 >= 0x30 && $0 <= 0x39 }),
                  let parsed = UInt64(raw), parsed <= UInt64(UInt32.max) else {
                throw ManagementCodecError.invalidInteger("\(key).\(backend)")
            }
            out[backend] = UInt32(parsed)
        }
        return out
    }
}

struct ManagementRuntimeIdentity: Equatable, Sendable, Codable {
    let pid: Int32
    let bridgeEpoch: UInt64
}

struct ManagementFileReadHealth: Equatable, Sendable {
    enum State: String, Sendable { case healthy, degrading, degraded }
    let state: State
    let stateEpoch: UInt64
    let stuckWorkers: UInt32
    let restartRecommended: Bool
}

struct ManagementActivity: Equatable, Sendable {
    enum AdmissionState: String, Sendable { case accepting, quiescing, shuttingDown }
    let bridgeOwnedActiveTurns: UInt32
    let pendingInteractions: UInt32
    let admissionState: AdmissionState
    /// Per-backend breakdown；旧 runtime 未提供时为 nil（门控退回全局计数，保持保守）。
    let bridgeOwnedActiveTurnsByBackend: [String: UInt32]?
    let pendingInteractionsByBackend: [String: UInt32]?

    /// 「重启共享 Codex 服务」只影响 codex/codex-web：门控只看这两个 backend 的
    /// 活跃 turn，不被 claude 等其它 backend 的任务误禁用（owner 2026-08-28）。
    var codexScopedActiveTurns: UInt32 {
        guard let byBackend = bridgeOwnedActiveTurnsByBackend else { return bridgeOwnedActiveTurns }
        return (byBackend["codex"] ?? 0) + (byBackend["codex-web"] ?? 0)
    }

    var codexScopedPendingInteractions: UInt32 {
        guard let byBackend = pendingInteractionsByBackend else { return pendingInteractions }
        return (byBackend["codex"] ?? 0) + (byBackend["codex-web"] ?? 0)
    }
}

enum ManagementQuiesceStatus: Equatable, Sendable {
    case none
    case leased(operationID: String, quiesceEpoch: UInt64, leaseRemainingMillis: UInt32)
    case committed(operationID: String, quiesceEpoch: UInt64)
}

struct ManagementV1Status: Equatable, Sendable {
    let runtimeIdentity: ManagementRuntimeIdentity
    let fileReadHealth: ManagementFileReadHealth
    let activity: ManagementActivity
    let quiesce: ManagementQuiesceStatus
}

enum ManagementStatusCodec {
    static func decode(_ data: Data) throws -> ManagementStatus {
        var parser = ManagementJSONParser(data)
        let rootValue = try parser.parse()
        guard case let .object(rootPairs) = rootValue else { throw ManagementCodecError.invalidJSON }
        let rootKeys = Set(rootPairs.map(\.0))
        let rootMap = Dictionary(uniqueKeysWithValues: rootPairs)
        if rootMap["managementSchemaVersion"] == nil {
            let root = try ManagementJSONObject(rootValue, exactKeys: ["status", "bridgeId", "displayName", "uptime", "version"])
            return ManagementStatus(
                status: try root.string("status"), bridgeId: try root.string("bridgeId"),
                displayName: try root.string("displayName"), iosPort: nil,
                uptime: try root.string("uptime"), version: try root.string("version"), v1: nil
            )
        }
        let expected: Set<String> = [
            "managementSchemaVersion", "status", "bridgeId", "displayName", "uptime", "version",
            "runtimeIdentity", "fileReadHealth", "activity", "quiesce",
        ]
        guard rootKeys == expected else { throw ManagementCodecError.unexpectedKeys }
        let root = try ManagementJSONObject(rootValue, exactKeys: expected)
        let version = try root.uint64("managementSchemaVersion")
        guard version == 1 else { throw ManagementCodecError.unsupportedVersion(version) }
        let identityObject = try ManagementJSONObject(try root.value("runtimeIdentity"), exactKeys: ["pid", "bridgeEpoch"])
        let identity = ManagementRuntimeIdentity(
            pid: try identityObject.int32Positive("pid"), bridgeEpoch: try identityObject.uint64("bridgeEpoch")
        )
        let healthObject = try ManagementJSONObject(
            try root.value("fileReadHealth"), exactKeys: ["state", "stateEpoch", "stuckWorkers", "restartRecommended"]
        )
        guard let healthState = ManagementFileReadHealth.State(rawValue: try healthObject.string("state")) else {
            throw ManagementCodecError.invalidValue("fileReadHealth.state")
        }
        let health = ManagementFileReadHealth(
            state: healthState, stateEpoch: try healthObject.uint64("stateEpoch"),
            stuckWorkers: try healthObject.uint32("stuckWorkers"),
            restartRecommended: try healthObject.bool("restartRecommended")
        )
        // byBackend 两个键是可选新增（旧 runtime 不带）；present 才进 exactKeys，
        // 保证旧 runtime 的 status 仍能严格解码。
        let activityValue = try root.value("activity")
        var activityKeys: Set<String> = ["bridgeOwnedActiveTurns", "pendingInteractions", "admissionState"]
        if case let .object(activityPairs) = activityValue {
            for key in activityPairs.map(\.0) where key == "bridgeOwnedActiveTurnsByBackend" || key == "pendingInteractionsByBackend" {
                activityKeys.insert(key)
            }
        }
        let activityObject = try ManagementJSONObject(activityValue, exactKeys: activityKeys)
        guard let admissionState = ManagementActivity.AdmissionState(rawValue: try activityObject.string("admissionState")) else {
            throw ManagementCodecError.invalidValue("activity.admissionState")
        }
        let activity = ManagementActivity(
            bridgeOwnedActiveTurns: try activityObject.uint32("bridgeOwnedActiveTurns"),
            pendingInteractions: try activityObject.uint32("pendingInteractions"), admissionState: admissionState,
            bridgeOwnedActiveTurnsByBackend: try activityObject.optionalUint32Map("bridgeOwnedActiveTurnsByBackend"),
            pendingInteractionsByBackend: try activityObject.optionalUint32Map("pendingInteractionsByBackend")
        )
        let quiesce = try decodeQuiesceStatus(try root.value("quiesce"))
        return ManagementStatus(
            status: try root.string("status"), bridgeId: try root.string("bridgeId"),
            displayName: try root.string("displayName"), iosPort: nil,
            uptime: try root.string("uptime"), version: try root.string("version"),
            v1: ManagementV1Status(runtimeIdentity: identity, fileReadHealth: health, activity: activity, quiesce: quiesce)
        )
    }

    private static func decodeQuiesceStatus(_ value: ManagementJSONValue) throws -> ManagementQuiesceStatus {
        guard case let .object(pairs) = value,
              let stateValue = pairs.first(where: { $0.0 == "state" })?.1,
              case let .string(state) = stateValue else { throw ManagementCodecError.invalidType("quiesce.state") }
        switch state {
        case "none":
            _ = try ManagementJSONObject(value, exactKeys: ["state"])
            return .none
        case "leased":
            let object = try ManagementJSONObject(value, exactKeys: ["state", "operationId", "quiesceEpoch", "leaseRemainingMillis"])
            let operationID = try strictHex(object.string("operationId"), field: "operationId")
            return .leased(operationID: operationID, quiesceEpoch: try object.uint64("quiesceEpoch"), leaseRemainingMillis: try object.uint32("leaseRemainingMillis"))
        case "committed":
            let object = try ManagementJSONObject(value, exactKeys: ["state", "operationId", "quiesceEpoch"])
            let operationID = try strictHex(object.string("operationId"), field: "operationId")
            return .committed(operationID: operationID, quiesceEpoch: try object.uint64("quiesceEpoch"))
        default: throw ManagementCodecError.invalidValue("quiesce.state")
        }
    }
}

private func strictHex(_ value: String, field: String) throws -> String {
    guard value.utf8.count == 32,
          value.utf8.allSatisfy({ ($0 >= 0x30 && $0 <= 0x39) || ($0 >= 0x61 && $0 <= 0x66) }) else {
        throw ManagementCodecError.invalidValue(field)
    }
    return value
}

struct ManagementQuiesceRequest: Codable, Sendable {
    let managementSchemaVersion: UInt64 = 1
    let operationId: String
    let expectedRuntime: ManagementRuntimeIdentity
    let expectedHealthEpoch: UInt64
}

struct ManagementCommitRequest: Codable, Sendable {
    let managementSchemaVersion: UInt64 = 1
    let operationId: String
    let expectedRuntime: ManagementRuntimeIdentity
    let expectedHealthEpoch: UInt64
    let quiesceEpoch: UInt64
    let token: String
}

enum ManagementRuntimeResult: Equatable, Sendable {
    case safe(identity: ManagementRuntimeIdentity, healthEpoch: UInt64, quiesceEpoch: UInt64, token: String, leaseMillis: UInt32, leaseRemainingMillis: UInt32)
    case deferred(activeTurns: UInt32, pendingInteractions: UInt32, retryAfterMillis: UInt32)
    case committed(identity: ManagementRuntimeIdentity, healthEpoch: UInt64, quiesceEpoch: UInt64)
    case aborted(identity: ManagementRuntimeIdentity, healthEpoch: UInt64)
    case outcome(String)
}

enum ManagementRuntimeResultCodec {
    static func decode(_ data: Data, group: String) throws -> ManagementRuntimeResult {
        var parser = ManagementJSONParser(data)
        let value = try parser.parse()
        guard case let .object(pairs) = value,
              let outcomeValue = pairs.first(where: { $0.0 == "outcome" })?.1,
              case let .string(outcome) = outcomeValue else { throw ManagementCodecError.invalidType("outcome") }
        let common: Set<String> = ["managementSchemaVersion", "operationId", "outcome"]
        let extras: Set<String>
        switch (group, outcome) {
        case ("quiesce", "safe"): extras = ["runtimeIdentity", "healthEpoch", "quiesceEpoch", "token", "leaseMillis", "leaseRemainingMillis"]
        case ("quiesce", "deferred"): extras = ["activeTurns", "pendingInteractions", "retryAfterMillis"]
        case ("commit", "committed"), ("commit", "already_committed"): extras = ["runtimeIdentity", "healthEpoch", "quiesceEpoch"]
        case ("abort", "aborted"): extras = ["runtimeIdentity", "healthEpoch"]
        default: extras = []
        }
        let object = try ManagementJSONObject(value, exactKeys: common.union(extras))
        guard try object.uint64("managementSchemaVersion") == 1 else { throw ManagementCodecError.unsupportedVersion(try object.uint64("managementSchemaVersion")) }
        _ = try strictHex(object.string("operationId"), field: "operationId")
        switch (group, outcome) {
        case ("quiesce", "safe"):
            let identity = try decodeIdentity(object.value("runtimeIdentity"))
            let token = try strictHex(object.string("token"), field: "token")
            let lease = try object.uint32("leaseMillis")
            let remaining = try object.uint32("leaseRemainingMillis")
            guard lease > 0, remaining <= lease else { throw ManagementCodecError.invalidValue("lease") }
            return .safe(identity: identity, healthEpoch: try object.uint64("healthEpoch"), quiesceEpoch: try object.uint64("quiesceEpoch"), token: token, leaseMillis: lease, leaseRemainingMillis: remaining)
        case ("quiesce", "deferred"):
            let retry = try object.uint32("retryAfterMillis")
            guard retry > 0 else { throw ManagementCodecError.invalidValue("retryAfterMillis") }
            return .deferred(activeTurns: try object.uint32("activeTurns"), pendingInteractions: try object.uint32("pendingInteractions"), retryAfterMillis: retry)
        case ("commit", "committed"), ("commit", "already_committed"):
            return .committed(identity: try decodeIdentity(object.value("runtimeIdentity")), healthEpoch: try object.uint64("healthEpoch"), quiesceEpoch: try object.uint64("quiesceEpoch"))
        case ("abort", "aborted"):
            return .aborted(identity: try decodeIdentity(object.value("runtimeIdentity")), healthEpoch: try object.uint64("healthEpoch"))
        default:
            let known: [String: Set<String>] = [
                "quiesce": ["identity_mismatch", "epoch_mismatch", "already_committed", "already_quiescing", "operation_reused", "token_generation_failed"],
                "commit": ["identity_mismatch", "epoch_mismatch", "quiesce_mismatch", "token_mismatch", "lease_expired"],
                "abort": ["already_accepting", "already_committed", "identity_mismatch", "epoch_mismatch", "quiesce_mismatch", "token_mismatch", "lease_expired"],
            ]
            guard known[group]?.contains(outcome) == true else { throw ManagementCodecError.invalidValue("outcome") }
            return .outcome(outcome)
        }
    }

    private static func decodeIdentity(_ value: ManagementJSONValue) throws -> ManagementRuntimeIdentity {
        let object = try ManagementJSONObject(value, exactKeys: ["pid", "bridgeEpoch"])
        return ManagementRuntimeIdentity(pid: try object.int32Positive("pid"), bridgeEpoch: try object.uint64("bridgeEpoch"))
    }
}
