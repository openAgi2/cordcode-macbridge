import Foundation

protocol OverviewAPIProviding {
    func getStatus() async throws -> ManagementStatus
    func getRemoteStatus() async throws -> RemoteStatus
}

protocol PairingAPIProviding {
    func createPairing() async throws -> PairingSessionInfo
    func getPairingStatus(_ pairingId: String) async throws -> PairingSessionStatus
    func approvePairing(_ pairingId: String) async throws -> PairingApproval
    func rejectPairing(_ pairingId: String) async throws
}

/// 设备列表与撤销的 API 抽象。`DeviceStore` 依赖此协议以便单元测试注入 stub，
/// 同时让 `ManagementAPIClient` 在生产中实现。
protocol DeviceAPIProviding {
    func listDevices() async throws -> [TrustedDevice]
    func revokeDevice(_ deviceId: String) async throws
}

// MARK: - Management API 数据模型

/// GET /internal/status 响应
struct ManagementStatus: Sendable {
    let status: String
    let bridgeId: String?
    let displayName: String?
    let iosPort: Int?
    let uptime: String?
    let version: String?
    let v1: ManagementV1Status?

    init(
        status: String,
        bridgeId: String?,
        displayName: String?,
        iosPort: Int?,
        uptime: String?,
        version: String?,
        v1: ManagementV1Status? = nil
    ) {
        self.status = status
        self.bridgeId = bridgeId
        self.displayName = displayName
        self.iosPort = iosPort
        self.uptime = uptime
        self.version = version
        self.v1 = v1
    }
}

/// GET /internal/devices 响应中的单个设备
struct TrustedDevice: Codable, Identifiable {
    let deviceId: String
    let displayName: String?
    let platform: String?
    let createdAt: String?
    let lastSeenAt: String?
    var id: String { deviceId }
}

/// GET /internal/remote/status 响应
struct RemoteStatus: Codable {
    let localURL: String?
    let tailscaleURL: String?
    let remoteURL: String?
    let remoteURLs: [String]?
    let connectionMode: String?
    let remoteConfigured: Bool?
    let includeTailscale: Bool?
    let includeRemote: Bool?
    let remoteAnalysis: RemoteURLAnalysis?
    let listenStatus: ListenStatus?
    /// control-plane 连接策略:同一局域网时是否优先直连(默认 false=Relay 底座)。
    /// 可选:旧 go-bridge 响应缺该字段时解码为 nil,UI 按 false 处理。SSV2:不进入 timeline。
    let preferLocalNetwork: Bool?
    let relay: RelayStatus?

    struct RemoteURLAnalysis: Codable {
        let scheme: String?
        let host: String?
        let hostCategory: String?
        let isTailscaleCGNAT: Bool?
        let isPublicWS: Bool?
        let securityLevel: String?
    }

    struct ListenStatus: Codable {
        let localURL: String?
        let listening: Bool?
    }

    struct RelayStatus: Codable {
        let configured: Bool
        /// enabled/connected 可选:真实 relay 连接状态只在该字段为 true 时显示「已接入中继网」,
        /// 否则按 enabled/configured/connected 组合显示未启用/配置中/未连接。不从 configured 推导为已连接。
        /// 旧 go-bridge 响应缺字段时解码为 nil。
        let enabled: Bool?
        let connected: Bool?
        let endpoint: String?
        let routeId: String?
    }
}

/// GET /internal/agents 响应中的单个 agent
struct AgentInfo: Codable, Equatable {
    let id: String
    let kind: String
    let displayName: String
    let status: String
    let reason: String?
    let liveEvents: String
    let requiresPollingForExternalTurns: Bool
}

struct CodexRemotePairingStatus: Codable, Equatable {
    let phase: String
    let stepUpUrl: String?
    let message: String?
    let online: Bool?
    let clientType: String?
}

private struct CodexRemotePairingCode: Codable {
    let manualPairingCode: String
}

/// POST /internal/shutdown 响应
struct ShutdownResponse: Codable {
    let shuttingDown: Bool?
}

/// POST /internal/pairing/create 响应
struct PairingSessionInfo: Codable {
    let id: String
    let qrPayload: String
    /// Flow C web-specific QR (https URL the phone's system camera opens). Relay-only; absent
    /// (empty) when relay is not configured. Same pairing session as qrPayload. See web pairing QR.
    let webQrPayload: String?
    let manualCode: String
    let expiresAt: String
}

/// POST /internal/pairing/{id}/approve 响应
struct PairingApproval: Codable {
    let pairingId: String?
    let deviceId: String
    let deviceToken: String?
    let state: String?
}

/// GET /internal/pairing/{id} 响应 — 配对会话状态
struct PairingSessionStatus: Codable {
    let id: String
    let state: String
    let claimingDeviceName: String?
    let claimingPlatform: String?
    let expiresAt: String?
}

// MARK: - Management API 客户端

/// 管理 API 的 HTTP 客户端，所有请求带 Bearer token
class ManagementAPIClient: OverviewAPIProviding, PairingAPIProviding, DeviceAPIProviding, TopologyAPIProviding {
    let baseURL: URL
    let token: String
    /// T07: 专用 ephemeral URLSession，短请求/资源超时，防慢响应阻塞监控循环。
    /// status 轮询 3s 一次，若 management server 半开（accept 连接不返回），URLSession.shared
    /// 的默认超时会让 supervisor 卡住数十秒，期间不执行自动重启判定。
    private let session: URLSession
    /// Pairing status/approval can synchronously cross the public Relay. Keep those requests
    /// separate from the 2-second local health-check budget.
    private let pairingSession: URLSession
    /// ChatGPT Desktop Remote Control enroll/pair talks to official HTTPS and may wait for
    /// browser step-up. Keep it off the 2-second local health-check budget.
    private let remoteControlSession: URLSession

    init(baseURL: String, token: String) throws {
        guard let url = URL(string: baseURL), !baseURL.isEmpty else {
            throw ManagementError.invalidURL
        }
        self.baseURL = url
        self.token = token
        // T07: timeoutIntervalForRequest=2s（单请求），timeoutIntervalForResource=5s（整体含重试）。
        // 这样慢/半开 management server 在 ≤5s 内让请求失败，supervisor 进入恢复流程而非卡死。
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 2
        config.timeoutIntervalForResource = 5
        config.waitsForConnectivity = false
        self.session = URLSession(configuration: config)

        let pairingConfig = URLSessionConfiguration.ephemeral
        pairingConfig.timeoutIntervalForRequest = 10
        pairingConfig.timeoutIntervalForResource = 20
        pairingConfig.waitsForConnectivity = false
        self.pairingSession = URLSession(configuration: pairingConfig)

        let remoteControlConfig = URLSessionConfiguration.ephemeral
        remoteControlConfig.timeoutIntervalForRequest = 60
        remoteControlConfig.timeoutIntervalForResource = 90
        remoteControlConfig.waitsForConnectivity = false
        self.remoteControlSession = URLSession(configuration: remoteControlConfig)
    }

    private func request(_ path: String, method: String = "GET") -> URLRequest {
        var req = URLRequest(url: baseURL.appendingPathComponent(path))
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        return req
    }

    // P1-1: 统一 HTTP 状态码校验
    private func performRequest(
        _ path: String,
        method: String = "GET",
        body: Data? = nil,
        using requestSession: URLSession? = nil
    ) async throws -> Data {
        var req = request(path, method: method)
        if method == "POST" {
            req.httpBody = body ?? Data()
            if body != nil { req.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        }
        let (data, response) = try await (requestSession ?? session).data(for: req)
        guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
            let code = (response as? HTTPURLResponse)?.statusCode ?? -1
            throw ManagementError.httpError(code)
        }
        return data
    }

    func getStatus() async throws -> ManagementStatus {
        let data = try await performRequest("/internal/status")
        return try ManagementStatusCodec.decode(data)
    }

    func quiesce(_ request: ManagementQuiesceRequest) async throws -> ManagementRuntimeResult {
        let data = try await performRequest(
            "/internal/runtime/quiesce", method: "POST", body: try ManagementRequestCodec.encode(request)
        )
        return try ManagementRuntimeResultCodec.decode(data, group: "quiesce")
    }

    func commitQuiescedShutdown(_ request: ManagementCommitRequest) async throws -> ManagementRuntimeResult {
        let data = try await performRequest(
            "/internal/runtime/commit-quiesced-shutdown", method: "POST", body: try ManagementRequestCodec.encode(request)
        )
        return try ManagementRuntimeResultCodec.decode(data, group: "commit")
    }

    func abortQuiesce(_ request: ManagementCommitRequest) async throws -> ManagementRuntimeResult {
        let data = try await performRequest(
            "/internal/runtime/abort-quiesce", method: "POST", body: try ManagementRequestCodec.encode(request)
        )
        return try ManagementRuntimeResultCodec.decode(data, group: "abort")
    }

    func updateDisplayName(_ displayName: String) async throws {
        var req = request("/internal/settings/display-name", method: "PUT")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(
            withJSONObject: ["displayName": displayName]
        )
        let (_, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse,
            (200...299).contains(http.statusCode) else {
            throw ManagementError.httpError((response as? HTTPURLResponse)?.statusCode ?? -1)
        }
    }

    func getAgents() async throws -> [AgentInfo] {
        let data = try await performRequest("/internal/agents")
        return try JSONDecoder().decode([AgentInfo].self, from: data)
    }

    func startCodexRemotePairing() async throws -> CodexRemotePairingStatus {
        let data = try await performRequest(
            "/internal/agents/codex-remote/remote-control/start",
            method: "POST",
            using: remoteControlSession
        )
        return try JSONDecoder().decode(CodexRemotePairingStatus.self, from: data)
    }

    func submitCodexRemotePairingCode(_ code: String) async throws -> CodexRemotePairingStatus {
        let body = try JSONEncoder().encode(CodexRemotePairingCode(manualPairingCode: code))
        let data = try await performRequest(
            "/internal/agents/codex-remote/remote-control/pair",
            method: "POST",
            body: body,
            using: remoteControlSession
        )
        return try JSONDecoder().decode(CodexRemotePairingStatus.self, from: data)
    }

    func codexRemotePairingStatus() async throws -> CodexRemotePairingStatus {
        let data = try await performRequest(
            "/internal/agents/codex-remote/remote-control/status",
            using: remoteControlSession
        )
        return try JSONDecoder().decode(CodexRemotePairingStatus.self, from: data)
    }

    func shutdown() async throws {
        _ = try await performRequest("/internal/shutdown", method: "POST")
    }

    // MARK: - Pairing

    func createPairing() async throws -> PairingSessionInfo {
        let data = try await performRequest("/internal/pairing/create", method: "POST", using: pairingSession)
        return try JSONDecoder().decode(PairingSessionInfo.self, from: data)
    }

    func getPairingStatus(_ pairingId: String) async throws -> PairingSessionStatus {
        let data = try await performRequest("/internal/pairing/\(pairingId)", using: pairingSession)
        return try JSONDecoder().decode(PairingSessionStatus.self, from: data)
    }

    func approvePairing(_ pairingId: String) async throws -> PairingApproval {
        let data = try await performRequest("/internal/pairing/\(pairingId)/approve", method: "POST", using: pairingSession)
        return try JSONDecoder().decode(PairingApproval.self, from: data)
    }

    func rejectPairing(_ pairingId: String) async throws {
        _ = try await performRequest("/internal/pairing/\(pairingId)/reject", method: "POST", using: pairingSession)
    }

    // MARK: - Devices

    func listDevices() async throws -> [TrustedDevice] {
        let data = try await performRequest("/internal/devices")
        return try JSONDecoder().decode([TrustedDevice].self, from: data)
    }

    func revokeDevice(_ deviceId: String) async throws {
        _ = try await performRequest("/internal/devices/\(deviceId)/revoke", method: "POST")
    }

    // MARK: - Logs

    func getRecentLogs() async throws -> [String] {
        let data = try await performRequest("/internal/logs/recent")
        return try JSONDecoder().decode([String].self, from: data)
    }

    // MARK: - Remote Status

    func getRemoteStatus() async throws -> RemoteStatus {
        let data = try await performRequest("/internal/remote/status")
        return try JSONDecoder().decode(RemoteStatus.self, from: data)
    }

    // MARK: - Agent Management

    /// 刷新所有 agent 检测状态
    func refreshAgents() async throws -> [AgentInfo] {
        let data = try await performRequest("/internal/agents/refresh", method: "POST")
        return try JSONDecoder().decode([AgentInfo].self, from: data)
    }

    /// 测试指定后端的连通性
    func testAgent(_ id: String) async throws -> AgentInfo {
        let data = try await performRequest("/internal/agents/\(id)/test", method: "POST")
        return try JSONDecoder().decode(AgentInfo.self, from: data)
    }

    // MARK: - Web Push 维护（设置页 misconfigured 状态 + 显式重置）

    func getWebPushStatus() async throws -> WebPushMaintenanceStatus {
        let data = try await performRequest("/internal/webpush/status")
        return try JSONDecoder().decode(WebPushMaintenanceStatus.self, from: data)
    }

    func resetWebPush() async throws -> WebPushResetResult {
        let data = try await performRequest("/internal/webpush/reset", method: "POST")
        return try JSONDecoder().decode(WebPushResetResult.self, from: data)
    }

    // MARK: - Topology Monitor

    func getTopologySnapshot() async throws -> TopologyMonitorStatus {
        let data = try await performRequest("/internal/topology/snapshot")
        return try TopologyMonitorStatusCodec.decode(data)
    }

    enum ManagementError: Error {
        case httpError(Int)
        case invalidURL
    }
}

// MARK: - Web Push 维护（remote-web push 方案 §5.1/§12.3）

/// GET /internal/webpush/status 响应。status 为 go-bridge WebPushStore 的健康状态；
/// decode 失败/未知值一律按 unknown 处理（fail-closed，不得当 healthy）。
struct WebPushMaintenanceStatus: Codable, Equatable {
    let status: String
    let detail: String?
    let subscriptionCount: Int
    let vapidKeyFingerprint: String?
    let lastResetAtMillis: Int64?
    let lastResetError: String?
}

/// POST /internal/webpush/reset 成功响应。
struct WebPushResetResult: Codable, Equatable {
    let reset: Bool
    let removedSubscriptions: Int
    let status: String
    let vapidKeyFingerprint: String?
}

/// Web push 维护 API 抽象，供维护模型测试注入。
protocol WebPushMaintenanceAPIProviding {
    func getWebPushStatus() async throws -> WebPushMaintenanceStatus
    func resetWebPush() async throws -> WebPushResetResult
}

extension ManagementAPIClient: WebPushMaintenanceAPIProviding {}

// MARK: - Topology Monitor（README: implementation plan v2 §2.4/§6）

/// Topology snapshot 的 API 抽象，供轮询 owner 测试注入。
protocol TopologyAPIProviding {
    func getTopologySnapshot() async throws -> TopologyMonitorStatus
}

/// 快照顶层 state（§2.4）。未知值 fail-closed → .unknown（诊断失败），不得当 healthy/disabled。
enum TopologySnapshotState: String, Codable, Sendable {
    case enabled
    case disabled
    case unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = TopologySnapshotState(rawValue: raw) ?? .unknown
    }
}

/// 决策性枚举：解码未知值一律 fail-closed 为 .unknown（绝不默认 healthy）。
enum TopologySyncHealth: String, Codable, Sendable {
    case healthy
    case notApplicable = "not_applicable"
    case degraded
    case unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = TopologySyncHealth(rawValue: raw) ?? .unknown
    }
}

enum TopologyBridgeAttachment: String, Codable, Sendable {
    case shared
    case partial
    case absent
    case unresolved
    case unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = TopologyBridgeAttachment(rawValue: raw) ?? .unknown
    }
}

enum TopologyDesktopAggregate: String, Codable, Sendable {
    case desktopAbsent = "desktop_absent"
    case allShared = "all_shared"
    case mixed
    case splitPresent = "split_present"
    case unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = TopologyDesktopAggregate(rawValue: raw) ?? .unknown
    }
}

/// 单个维度观测（enum 为原始字符串证据，展示用；stale 由 service 裁决，客户端不比较本地时钟）。
struct TopologyMonitorDimension: Codable, Sendable, Equatable {
    let enumValue: String
    let ageMs: Int64?
    let stale: Bool?
    let source: String?
    let errorCode: String?

    enum CodingKeys: String, CodingKey {
        case enumValue = "enum"
        case ageMs
        case stale
        case source
        case errorCode
    }
}

/// 固定 8 键 dimensions（§2.4）。键缺失 = nil（诊断失败由 syncHealth=.unknown 表达，反例：
/// enabled 快照缺键说明服务端形状异常，此时不默认 healthy）。
struct TopologyMonitorDimensions: Codable, Sendable, Equatable {
    let topologyBridgeAttachment: TopologyMonitorDimension?
    let topologyDesktopAggregate: TopologyMonitorDimension?
    let seatHealthDaemon: TopologyMonitorDimension?
    let seatHealthLaunchAgent: TopologyMonitorDimension?
    let attachConfig: TopologyMonitorDimension?
    let versionCompatibility: TopologyMonitorDimension?
    let legacyManagedLoopback: TopologyMonitorDimension?
    let legacyDesktopPrivate: TopologyMonitorDimension?
}

/// GET /internal/topology/snapshot 响应（always-200 + state）。disabled 时 syncHealth/dimensions 缺省。
struct TopologyMonitorStatus: Codable, Sendable, Equatable {
    let schemaVersion: String?
    let state: TopologySnapshotState
    /// Management v1's UInt64 identity transported as an Int64 bit pattern.
    let bridgeEpoch: Int64?
    let sampledAtMs: Int64?
    let syncHealth: TopologySyncHealth?
    let dimensions: TopologyMonitorDimensions?
    let instances: [TopologyMonitorInstance]?

    struct TopologyMonitorInstance: Codable, Sendable, Equatable {
        let pid: Int
        let startTime: String
        let classification: String
        let evidence: [TopologyMonitorEvidence]?
    }

    struct TopologyMonitorEvidence: Codable, Sendable, Equatable {
        let kind: String
        let state: String
    }
}

/// 拓扑 snapshot codec：JSONDecoder 直接解码；未知字段忽略；决策性枚举 fail-closed。
enum TopologyMonitorStatusCodec {
    static func decode(_ data: Data) throws -> TopologyMonitorStatus {
        try JSONDecoder().decode(TopologyMonitorStatus.self, from: data)
    }
}
