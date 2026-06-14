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
class ManagementAPIClient: OverviewAPIProviding, PairingAPIProviding {
    let baseURL: URL
    let token: String

    init(baseURL: String, token: String) throws {
        guard let url = URL(string: baseURL), !baseURL.isEmpty else {
            throw ManagementError.invalidURL
        }
        self.baseURL = url
        self.token = token
    }

    private func request(_ path: String, method: String = "GET") -> URLRequest {
        var req = URLRequest(url: baseURL.appendingPathComponent(path))
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        return req
    }

    // P1-1: 统一 HTTP 状态码校验
    private func performRequest(_ path: String, method: String = "GET") async throws -> Data {
        var req = request(path, method: method)
        if method == "POST" { req.httpBody = Data() }
        let (data, response) = try await URLSession.shared.data(for: req)
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
        let (_, response) = try await URLSession.shared.data(for: req)
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
        let data = try await performRequest("/internal/pairing/create", method: "POST")
        return try JSONDecoder().decode(PairingSessionInfo.self, from: data)
    }

    func getPairingStatus(_ pairingId: String) async throws -> PairingSessionStatus {
        let data = try await performRequest("/internal/pairing/\(pairingId)")
        return try JSONDecoder().decode(PairingSessionStatus.self, from: data)
    }

    func approvePairing(_ pairingId: String) async throws -> PairingApproval {
        let data = try await performRequest("/internal/pairing/\(pairingId)/approve", method: "POST")
        return try JSONDecoder().decode(PairingApproval.self, from: data)
    }

    func rejectPairing(_ pairingId: String) async throws {
        _ = try await performRequest("/internal/pairing/\(pairingId)/reject", method: "POST")
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
