import Foundation

// MARK: - OpenCode server source

/// CordCode 连接 OpenCode HTTP server 的来源。
///
/// 不再隐式 fallback 到固定 64667：source 必须显式选中，失败时暴露真实原因，
/// 不自动制造第二个 server。详见
/// `docs/2026-07-02-opencode-shared-service-discovery-plan.md`。
enum OpenCodeServerSource: String, Codable, CaseIterable {
    /// CordCode Link 自动启动并管理 loopback-only `opencode serve`，同时同步 OpenCode Desktop。
    case managedLocal = "managed_local"

    /// 显式用户/运维启动的 stable `opencode serve` HTTP server（loopback + Basic Auth）。
    /// Phase A 默认可开发目标：bring-your-own-server。
    case externalHttp = "external_http"

    /// 升级连续性的显式兼容模式：继续使用 `http://127.0.0.1:64667`。
    /// 不代表安全共享 server；no-auth 200 时必须带 `legacy_insecure_unverified` 警告。
    /// 仅为存量 credentials 升级迁移或用户显式选择时使用，不能作为新装默认。
    case legacy64667 = "legacy_64667"

    /// future-gated：只有 stable `opencode` 暴露 `service` / `serve --register` 且端点经
    /// 实测兼容后才启用。当前 stable 1.17.13 不具备该能力，直接 unavailable。
    case serviceDiscoveryFuture = "service_discovery_future"

    /// 未启用 OpenCode backend。
    case disabled
}

// MARK: - Endpoint resolution errors

/// Endpoint 解析/校验失败原因。reason 字符串不包含 password 等敏感信息。
enum OpenCodeEndpointError: Error, Equatable, CustomStringConvertible {
    /// source=disabled，或 external_http 未配置 URL。
    case notConfigured
    /// external_http 要求非空 password（控制面 secret 不得为空）。
    case passwordRequired
    /// URL 不是 HTTP loopback（拒绝 0.0.0.0 / LAN IP / 公网 / 非 http）。
    case nonLoopbackURL(String)
    /// URL 解析失败。
    case malformedURL(String)
    /// service_discovery_future 在当前 stable opencode 上不可用。
    case serviceDiscoveryUnavailable
    /// 连接失败/超时。
    case unreachable(String)
    /// no-auth `/global/health` 返回 200 + OpenCode body：server 未启用 Basic Auth，
    /// external_http 必须拒绝。
    case serverUnauthenticated
    /// authed `/global/health` 返回 401：凭据不匹配。
    case authFailed
    /// 响应不像 OpenCode server（状态码异常或缺少 healthy/version）。
    case notOpencodeServer

    var description: String {
        switch self {
        case .notConfigured:
            return L10n.opencodeErrNotConfigured
        case .passwordRequired:
            return L10n.opencodeErrPasswordRequired
        case .nonLoopbackURL:
            return L10n.opencodeErrNonLoopback
        case .malformedURL:
            return L10n.opencodeErrMalformedURL
        case .serviceDiscoveryUnavailable:
            return L10n.opencodeErrServiceDiscoveryUnavailable
        case .unreachable:
            return L10n.opencodeErrUnreachable
        case .serverUnauthenticated:
            return L10n.opencodeErrServerUnauthenticated
        case .authFailed:
            return L10n.opencodeErrAuthFailed
        case .notOpencodeServer:
            return L10n.opencodeErrNotOpencodeServer
        }
    }
}

// MARK: - Endpoint model

/// 解析后的 OpenCode endpoint（已规范化为 loopback URL + 凭据）。
struct OpenCodeEndpoint: Equatable {
    let source: OpenCodeServerSource
    let url: String
    let username: String
    let password: String
    /// 仅 legacy_64667 在 no-auth `/global/health` 返回 200 时为 true：表示该 endpoint
    /// 可能是无密码或 0.0.0.0 监听的旧进程，不承诺安全或共享 Desktop vlocal。
    var legacyInsecureUnverified: Bool = false
}

/// 持久化在 `credentials.json` 的 OpenCode endpoint 配置（用户输入，未经校验）。
struct OpenCodeEndpointConfig: Equatable {
    var source: OpenCodeServerSource
    var url: String
    var username: String
    var password: String
}

// MARK: - Pure resolver (T01)

/// 纯逻辑解析：URL 规范化、loopback/password/source 检查。不接触网络。
///
/// 网络健康校验见 `OpenCodeHealthValidator`（T02）。
enum OpenCodeEndpointResolver {

    /// 把用户输入的 URL 规范化为 `http://127.0.0.1:<port>`。
    /// `localhost` / `127.0.0.1` / `::1` 都归一到 `127.0.0.1`（匹配
    /// `opencode serve --hostname 127.0.0.1` 的 IPv4 listener，避免 IPv6 `::1` 不匹配）。
    /// 非 loopback、非 http、缺端口、解析失败均返回 nil。
    static func normalizeLoopbackURL(_ raw: String) -> String? {
        var s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return nil }

        // 没有 scheme 时补 http://，避免 "localhost:64667" 被误判 scheme=localhost。
        let lowered = s.lowercased()
        if !lowered.hasPrefix("http://") && !lowered.hasPrefix("https://") {
            s = "http://" + s
        }

        guard var comps = URLComponents(string: s) else { return nil }
        // OpenCode 本地 server 只服务 http；拒绝 https / 其他 scheme。
        guard comps.scheme?.lowercased() == "http" else { return nil }

        let host = (comps.host ?? "").lowercased()
        switch host {
        case "localhost", "127.0.0.1", "::1", "[::1]":
            comps.host = "127.0.0.1"
        default:
            return nil // 非 loopback：拒绝 0.0.0.0 / LAN IP / 公网
        }
        // endpoint 是 base URL，丢弃 path/query/fragment。
        comps.path = ""
        comps.query = nil
        comps.fragment = nil
        // 必须显式带端口（不假定默认端口）。
        guard comps.port != nil else { return nil }
        return comps.url?.absoluteString
    }

    /// 解析配置为可用的 endpoint candidate，或返回明确的错误。
    /// 不做网络探测；网络可达性由 `OpenCodeHealthValidator` 负责。
    static func resolve(_ config: OpenCodeEndpointConfig) -> Result<OpenCodeEndpoint, OpenCodeEndpointError> {
        switch config.source {
        case .managedLocal:
            // managed_local 的 URL/凭据由 OpenCodeManagedServer 在启动时解析。
            // 纯 resolver 不制造占位 endpoint。
            return .failure(.notConfigured)
        case .disabled:
            return .failure(.notConfigured)
        case .serviceDiscoveryFuture:
            // Phase B future-gated：当前 stable opencode 1.17.13 没有
            // `service` / `serve --register`，直接 unavailable，不伪造 discovery。
            return .failure(.serviceDiscoveryUnavailable)
        case .legacy64667:
            // 升级连续性兼容模式。username 默认 opencode；password 可空（旧进程可能无密码），
            // 是否安全由 HealthValidator 以 legacy_insecure_unverified 标记。
            let user = config.username.isEmpty ? "opencode" : config.username
            return .success(OpenCodeEndpoint(
                source: .legacy64667,
                url: "http://127.0.0.1:64667",
                username: user,
                password: config.password,
                legacyInsecureUnverified: false
            ))
        case .externalHttp:
            return resolveExternalHttp(
                url: config.url,
                username: config.username,
                password: config.password
            )
        }
    }

    private static func resolveExternalHttp(url rawURL: String, username: String, password: String) -> Result<OpenCodeEndpoint, OpenCodeEndpointError> {
        let trimmed = rawURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return .failure(.notConfigured)
        }
        // external_http 默认/自动路径必须要求非空 password（不接受无密码 server）。
        guard !password.isEmpty else {
            return .failure(.passwordRequired)
        }
        guard let normalized = normalizeLoopbackURL(trimmed) else {
            return .failure(.nonLoopbackURL(rawURL))
        }
        let user = username.isEmpty ? "opencode" : username
        return .success(OpenCodeEndpoint(
            source: .externalHttp,
            url: normalized,
            username: user,
            password: password,
            legacyInsecureUnverified: false
        ))
    }

    /// Basic Auth header（`Basic <base64(user:pass)>`）。仅供 resolver/validator 内部使用，
    /// 不写日志、不进 argv。
    static func basicAuthHeader(user: String, password: String) -> String {
        let raw = "\(user):\(password)"
        return "Basic " + Data(raw.utf8).base64EncodedString()
    }

    /// 升级迁移规则（plan §5.5）：
    /// - 已显式保存 source → 尊重用户配置。
    /// - 无显式 source 但 credentials.json 已有 user/pass（存量安装）→ `legacy_64667`，
    ///   保持现有 OpenCode 行为连续。
    /// - 否则（全新安装）→ `managed_local`，由 CordCode 自动托管本机 OpenCode server。
    static func migratedSource(explicit: OpenCodeServerSource?, fileExistedWithCreds: Bool) -> OpenCodeServerSource {
        if let explicit { return explicit }
        return fileExistedWithCreds ? .legacy64667 : .managedLocal
    }

    /// 是否因升级迁移落到 legacy_64667（用于一次性迁移提示）。
    static func isLegacyMigration(explicit: OpenCodeServerSource?, fileExistedWithCreds: Bool) -> Bool {
        explicit == nil && fileExistedWithCreds
    }
}

// MARK: - Health validator (T02)

/// OpenCode `/global/health` 探测器：先 no-auth，证明 server 要求认证后再做 authed 校验。
///
/// 算法（plan §5.2）：
/// 1. no-auth GET /global/health（默认 2s 超时）
///    - 连接失败/超时 → unreachable
///    - 401 → server 已启用认证，进入 authed 校验
///    - 200 + OpenCode body → server 未启用 Basic Auth（external_http 拒绝为 serverUnauthenticated；
///      legacy_64667 例外：允许但标记 legacyInsecureUnverified）
///    - 200 非 OpenCode body / 其他状态 → notOpencodeServer
/// 2. authed GET /global/health（默认 username=opencode，可由 endpoint.username 覆盖）
///    - 200 + OpenCode body → 通过
///    - 401 → authFailed
///    - 其他 / 非 OpenCode body → notOpencodeServer
///
/// OpenCode health body schema 以 stable opencode 1.17.13 实测的
/// `{"healthy":true,"version":"..."}` 为准。
struct OpenCodeHealthValidator {
    typealias Fetch = (URLRequest) async throws -> (Data, HTTPURLResponse)

    let timeout: TimeInterval
    let fetch: Fetch

    init(timeout: TimeInterval = 2.0, fetch: @escaping Fetch = OpenCodeHealthValidator.defaultFetch) {
        self.timeout = timeout
        self.fetch = fetch
    }

    /// URLSession 默认实现。测试中注入自定义 fetch 走 URLProtocol stub。
    static func defaultFetch(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw URLError(.badServerResponse)
        }
        return (data, http)
    }

    func validate(_ endpoint: OpenCodeEndpoint) async -> Result<OpenCodeEndpoint, OpenCodeEndpointError> {
        // external_http 防御性复检：resolve 已拦截，这里再兜底。
        if endpoint.source == .externalHttp && endpoint.password.isEmpty {
            return .failure(.passwordRequired)
        }

        // 1. no-auth health
        switch await fetchHealth(endpoint: endpoint, user: nil, password: nil) {
        case .failure(let error):
            return .failure(error)
        case .success(let (code, body)):
            switch code {
            case 401:
                break // server 要求认证 → 继续 authed 校验
            case 200:
                if OpenCodeHealthValidator.isOpenCodeHealth(body) {
                    switch endpoint.source {
                    case .legacy64667:
                        // 兼容例外：允许，但标记不安全。
                        var flagged = endpoint
                        flagged.legacyInsecureUnverified = true
                        return .success(flagged)
                    default:
                        // external_http / 其它：拒绝无密码 server。
                        return .failure(.serverUnauthenticated)
                    }
                } else {
                    return .failure(.notOpencodeServer)
                }
            default:
                return .failure(.notOpencodeServer)
            }
        }

        // 2. authed health
        switch await fetchHealth(endpoint: endpoint, user: endpoint.username, password: endpoint.password) {
        case .failure(let error):
            return .failure(error)
        case .success(let (code, body)):
            switch code {
            case 200 where OpenCodeHealthValidator.isOpenCodeHealth(body):
                return .success(endpoint)
            case 401:
                return .failure(.authFailed)
            default:
                return .failure(.notOpencodeServer)
            }
        }
    }

    private func fetchHealth(endpoint: OpenCodeEndpoint, user: String?, password: String?) async -> Result<(Int, Data), OpenCodeEndpointError> {
        guard !endpoint.url.isEmpty, let url = URL(string: endpoint.url + "/global/health") else {
            return .failure(.malformedURL(endpoint.url))
        }
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: timeout)
        if let user, let password {
            request.setValue(OpenCodeEndpointResolver.basicAuthHeader(user: user, password: password), forHTTPHeaderField: "Authorization")
        }
        do {
            let (data, response) = try await fetch(request)
            return .success((response.statusCode, data))
        } catch {
            return .failure(.unreachable(error.localizedDescription))
        }
    }

    /// 判定 stable opencode 1.17.13 的 health body：`{"healthy":true,"version":"..."}`。
    /// 缺少 healthy/version 或 healthy 不为 true 都视为非 OpenCode 响应。
    static func isOpenCodeHealth(_ data: Data) -> Bool {
        guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return false
        }
        guard let healthy = object["healthy"] as? Bool, healthy else {
            return false
        }
        guard let version = object["version"] as? String, !version.isEmpty else {
            return false
        }
        return true
    }
}
