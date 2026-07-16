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
struct ManagementStatus: Codable {
    let status: String
    let bridgeId: String?
    let displayName: String?
    let iosPort: Int?
    let uptime: String?
    let version: String?
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
class ManagementAPIClient: OverviewAPIProviding, PairingAPIProviding, DeviceAPIProviding {
    let baseURL: URL
    let token: String
    /// T07: 专用 ephemeral URLSession，短请求/资源超时，防慢响应阻塞监控循环。
    /// status 轮询 3s 一次，若 management server 半开（accept 连接不返回），URLSession.shared
    /// 的默认超时会让 supervisor 卡住数十秒，期间不执行自动重启判定。
    private let session: URLSession
    /// Pairing status/approval can synchronously cross the public Relay. Keep those requests
    /// separate from the 2-second local health-check budget.
    private let pairingSession: URLSession

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
        using requestSession: URLSession? = nil
    ) async throws -> Data {
        var req = request(path, method: method)
        if method == "POST" { req.httpBody = Data() }
        let (data, response) = try await (requestSession ?? session).data(for: req)
        guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
            let code = (response as? HTTPURLResponse)?.statusCode ?? -1
            throw ManagementError.httpError(code)
        }
        return data
    }

    func getStatus() async throws -> ManagementStatus {
        let data = try await performRequest("/internal/status")
        return try JSONDecoder().decode(ManagementStatus.self, from: data)
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

    enum ManagementError: Error {
        case httpError(Int)
        case invalidURL
    }
}
