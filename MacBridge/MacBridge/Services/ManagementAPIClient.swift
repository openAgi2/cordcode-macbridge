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

/// 撤销尝试的 API 边界契约（followups v9 §4.3.2）：四类 response class +
/// 可诊断原因（不泄漏响应正文），跨过 DeviceAPIProviding → DeviceStore 边界。
/// 任何结果都必须继续 typed reload（不能因 throw 提前跳过）。
enum RevokeAttemptOutcome: Sendable, Equatable {
    case confirmedClean
    case confirmedCleanupFailure(pushCleanupError: String)
    /// revoked:true 但 pushCleanupError 字段不可信（类型错/空串/纯空白）——
    /// 保留撤销证据（服务端只有成功持久化才返回 true），cleanup 信息未知。
    case confirmedCleanupUnknown
    case protocolUnknown(RevokeProtocolIssue)
    case transportOrHTTPFailure(RevokeTransportIssue)
}

/// protocolUnknown 的可诊断原因（2xx 体的形状，按 §4.3.2 分类表固定优先级）。
enum RevokeProtocolIssue: Sendable, Equatable {
    case emptyBody            // 200 但体零字节或纯空白
    case malformedJSON        // 非 JSON 体
    case revokedTypeMismatch  // 顶层非 object，或 revoked 值非布尔（含 null）
    case missingRevokedKey    // 顶层 object 无 revoked 键
    case revokedFalse         // revoked == false
}

/// transport/HTTP 失败的可诊断载体（§4.3.2）。
enum RevokeTransportIssue: Sendable, Equatable {
    case networkError(RevokeNetworkFailureCategory)
    case httpStatus(status: Int, serverErrorCode: String?)
    /// 响应体超过 64KiB 读取上限（流式累计第 65,537 byte 主动 cancel）。
    case responseTooLarge
}

/// 稳定、可本地化、非敏感的网络失败类别（不把服务器正文或本地路径进 UI）。
enum RevokeNetworkFailureCategory: String, Sendable, Equatable {
    case offline
    case timedOut
    case cannotConnectToHost
    case cancelled
    case other
}

/// 设备列表与撤销的 API 抽象。`DeviceStore` 依赖此协议以便单元测试注入 stub，
/// 同时让 `ManagementAPIClient` 在生产中实现。`revokeDevice` 为 non-throwing
/// （§4.3.2 签名写死）：所有失败含 cancellation 映射进 outcome。
protocol DeviceAPIProviding {
    func listDevices() async throws -> [TrustedDevice]
    func revokeDevice(_ deviceId: String) async -> RevokeAttemptOutcome
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

/// POST /internal/agents/dsh-web/install · /start 的立即返回（kick 异步工作）。
/// status ∈ started / already_installing / already_starting / already_running /
/// node_missing / no_binary / not_needed。
struct DSHWebSeatActionKick: Codable, Equatable {
    let status: String
    let detail: String?
    let binPath: String?
}

/// GET /internal/agents/dsh-web/action-state 的快照：npmFound 决定「安装」还是
/// 「需要 Node.js」；installing/starting 驱动进行中态；错误字段是行字幕。
struct DSHWebSeatActionState: Codable, Equatable {
    var installing = false
    var starting = false
    var npmFound = false
    var npmPath: String?
    var binPath: String?
    var lastInstallError: String?
    var lastStartError: String?
    var lastInstallNote: String?
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
        do {
            let data = try await performRequest(
                "/internal/agents/codex-remote/remote-control/pair",
                method: "POST",
                body: body,
                using: remoteControlSession
            )
            return try JSONDecoder().decode(CodexRemotePairingStatus.self, from: data)
        } catch {
            let submissionError = error
            if let status = try? await codexRemotePairingStatus(), status.phase == "ready" {
                return status
            }
            throw submissionError
        }
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

/// 撤销尝试（§4.3.2）：专用 raw 请求路径（不修改 performRequest/其他调用方），
/// 有界流式读取（64KiB），non-throwing——所有失败含 cancellation 映射进
/// outcome。任何 outcome 都由 DeviceStore 继续 typed reload。
func revokeDevice(_ deviceId: String) async -> RevokeAttemptOutcome {
    var req = request("/internal/devices/\(deviceId)/revoke", method: "POST")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = Data()

    let delegate = RevokeBoundedDelegate(bodyLimit: Self.revokeBodyLimit)
    let config = URLSessionConfiguration.ephemeral
    config.timeoutIntervalForRequest = 2
    config.timeoutIntervalForResource = 5
    config.waitsForConnectivity = false
    let session = URLSession(configuration: config, delegate: delegate, delegateQueue: nil)
    defer { session.finishTasksAndInvalidate() }

    let raw = await delegate.perform(req, session: session)
    switch raw {
    case .tooLarge:
        return .transportOrHTTPFailure(.responseTooLarge)
    case .network(let error):
        return .transportOrHTTPFailure(.networkError(Self.categorizeNetworkFailure(error)))
    case .body(let status, let data):
        guard (200...299).contains(status) else {
            return .transportOrHTTPFailure(.httpStatus(status: status, serverErrorCode: Self.decodeServerErrorCode(data)))
        }
        return Self.classifyRevokeBody(data)
    }
}

/// 撤销响应体读取上限（§4.3.2：网络读取阶段硬上限，非事后断言）。
private static let revokeBodyLimit = 65_536

/// 2xx 体的分类表（§4.3.2：无重叠、全覆盖、固定优先级，按序判定先命中先归属）。
private static func classifyRevokeBody(_ data: Data) -> RevokeAttemptOutcome {
    // 1. 零字节或纯空白 → emptyBody（含空 Data）。
    if data.allSatisfy({ $0 == 0x20 || $0 == 0x09 || $0 == 0x0A || $0 == 0x0D }) {
        return .protocolUnknown(.emptyBody)
    }
    // 2. JSON 语法非法 → malformedJSON。fragmentsAllowed：顶层标量（null/string/
    //    number/bool）是合法 JSON，须解析成功后由第 3 步判 revokedTypeMismatch，
    //    不得误归 malformedJSON。
    guard let payload = try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]) else {
        return .protocolUnknown(.malformedJSON)
    }
    // 3. 顶层非 object（null/array/string/number/bool）→ revokedTypeMismatch。
    guard let dict = payload as? [String: Any] else {
        return .protocolUnknown(.revokedTypeMismatch)
    }
    // 4. 无 revoked 键 → missingRevokedKey。
    guard dict.keys.contains("revoked") else {
        return .protocolUnknown(.missingRevokedKey)
    }
    // 5. revoked 值非布尔（含 null/数字/字符串）→ revokedTypeMismatch。
    //    NSNumber-Bool 桥接会把数字 1 当 true，须按 CFBoolean 类型判定真 JSON 布尔。
    guard let number = dict["revoked"] as? NSNumber,
          CFGetTypeID(number) == CFBooleanGetTypeID() else {
        return .protocolUnknown(.revokedTypeMismatch)
    }
    let revoked = number.boolValue
    // 6. revoked == false → revokedFalse。
    guard revoked else {
        return .protocolUnknown(.revokedFalse)
    }
    // 7-9. revoked == true。
    guard let cleanup = dict["pushCleanupError"] else {
        return .confirmedClean
    }
    if let message = cleanup as? String {
        // 8. 非空非空白 string → confirmedCleanupFailure。
        if !message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return .confirmedCleanupFailure(pushCleanupError: message)
        }
    }
    // 9. 类型错误（非 string，含 null）或空串/纯空白 → confirmedCleanupUnknown。
    return .confirmedCleanupUnknown
}

/// 非 2xx 时尽力解码体 {"error": ...} 作为 serverErrorCode（非 string 或缺失 → nil）。
private static func decodeServerErrorCode(_ data: Data?) -> String? {
    guard let data, let payload = try? JSONSerialization.jsonObject(with: data) else { return nil }
    guard let dict = payload as? [String: Any] else { return nil }
    return dict["error"] as? String
}

/// URLError → 稳定可本地化非敏感类别（§4.3.2；不进服务器正文/本地路径）。
private static func categorizeNetworkFailure(_ error: Error) -> RevokeNetworkFailureCategory {
    guard let urlError = error as? URLError else { return .other }
    switch urlError.code {
    case .notConnectedToInternet, .networkConnectionLost, .dataNotAllowed:
        return .offline
    case .timedOut:
        return .timedOut
    case .cannotConnectToHost, .cannotFindHost, .dnsLookupFailed:
        return .cannotConnectToHost
    case .cancelled:
        return .cancelled
    default:
        return .other
    }
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

    // MARK: - dsh-web 座位动作（2026-09-22 方案 §5：未安装可代装、未启动可点启动）
    // 两个 POST 只 kick 异步工作并立即返回（npm 最长 10 分钟、座位启动最长 30 秒，
    // 不能占住请求）；进度经 action-state 轮询。remoteControlSession 的 60s 预算
    // 覆盖 npm 路径发现等慢启动查询。

    /// 点「安装」：kick 代装（npm install -g @deepseek-ai/dsh，装完自动启动座位）。
    func installDSHWeb() async throws -> DSHWebSeatActionKick {
        let data = try await performRequest(
            "/internal/agents/dsh-web/install",
            method: "POST",
            using: remoteControlSession
        )
        return try JSONDecoder().decode(DSHWebSeatActionKick.self, from: data)
    }

    /// 点「启动」：kick 显式 StartSeat（座位上已有 dsh 在听则收养，不起第二个进程）。
    func startDSHWebSeat() async throws -> DSHWebSeatActionKick {
        let data = try await performRequest(
            "/internal/agents/dsh-web/start",
            method: "POST",
            using: remoteControlSession
        )
        return try JSONDecoder().decode(DSHWebSeatActionKick.self, from: data)
    }

    /// 座位动作状态（npmFound / 进行中态 / 最近错误）。
    func dshWebSeatActionState() async throws -> DSHWebSeatActionState {
        let data = try await performRequest(
            "/internal/agents/dsh-web/action-state",
            using: remoteControlSession
        )
        return try JSONDecoder().decode(DSHWebSeatActionState.self, from: data)
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

// MARK: - 撤销 raw 请求的有界 delegate（§4.3.2）

/// 撤销 raw 请求结果：body ≤ 上限；tooLarge = 流式累计超过上限（主动 cancel）；
/// network = 传输失败（含 cancellation）。
private enum RawRevokeResult {
    case body(status: Int, data: Data)
    case tooLarge
    case network(Error)
}

/// 撤销请求的专用 delegate（每次请求新建一个实例）：
/// - 流式累计响应体，第 65,537 byte **立即 cancel**（网络读取阶段硬上限，
///   不是 `URLSession.data(for:)` 收完再查的事后断言）；
/// - 主动超限 cancel 产生的 `URLError.cancelled` **不覆盖已锁定的
///   `responseTooLarge`**（R8-B3：didComplete 先查 tooLarge 再查 error）；
/// - `Content-Length` 超限可提前拒绝（`.cancel` disposition），但流式上限
///   兜底无 Content-Length 的 chunked 响应（不单独依赖 header）；
/// - 拒绝 redirect（Authorization 绝不转发到 Location）。
private final class RevokeBoundedDelegate: NSObject, URLSessionDataDelegate {
    private let bodyLimit: Int
    private var continuation: CheckedContinuation<RawRevokeResult, Never>?
    private var buffer = Data()
    private var response: URLResponse?
    private var tooLarge = false

    init(bodyLimit: Int) {
        self.bodyLimit = bodyLimit
        super.init()
    }

    func perform(_ request: URLRequest, session: URLSession) async -> RawRevokeResult {
        await withCheckedContinuation { cont in
            self.continuation = cont
            session.dataTask(with: request).resume()
        }
    }

    // 拒绝 redirect：不把 Authorization 转发到 Location。
    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }

    func urlSession(
        _ session: URLSession,
        dataTask: URLSessionDataTask,
        didReceive response: URLResponse,
        completionHandler: @escaping (URLSession.ResponseDisposition) -> Void
    ) {
        self.response = response
        buffer = Data()
        if let http = response as? HTTPURLResponse,
           let raw = http.value(forHTTPHeaderField: "Content-Length"),
           let length = Int(raw), length > bodyLimit {
            tooLarge = true
            completionHandler(.cancel)
            return
        }
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        guard !tooLarge else { return }
        buffer.append(data)
        if buffer.count > bodyLimit {
            // 第 65,537 byte：先锁定 tooLarge 再 cancel——cancel 产生的
            // URLError.cancelled 不得覆盖该 reason（R8-B3）。
            tooLarge = true
            dataTask.cancel()
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard let cont = continuation else { return }
        continuation = nil
        if tooLarge {
            cont.resume(returning: .tooLarge)
        } else if let error {
            cont.resume(returning: .network(error))
        } else {
            let status = (response as? HTTPURLResponse)?.statusCode ?? -1
            cont.resume(returning: .body(status: status, data: buffer))
        }
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
