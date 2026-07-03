import XCTest
@testable import CordCodeLink

// MARK: - Test helpers

private func makeHTTPResponse(_ status: Int, url: String = "http://127.0.0.1:4096/global/health") -> HTTPURLResponse {
    HTTPURLResponse(url: URL(string: url)!, statusCode: status, httpVersion: nil, headerFields: nil)!
}

private let opencodeHealthBody = Data(#"{"healthy":true,"version":"1.17.13"}"#.utf8)
private let nonOpencodeBody = Data(#"{"ok":true}"#.utf8)

final class OpenCodeEndpointResolverTests: XCTestCase {

    // MARK: - URL normalization (T01)

    func testNormalizeLocalhostBecomes127() {
        XCTAssertEqual(
            OpenCodeEndpointResolver.normalizeLoopbackURL("http://localhost:64667"),
            "http://127.0.0.1:64667"
        )
    }

    func testNormalizeBareHostPortGetsHttpScheme() {
        XCTAssertEqual(
            OpenCodeEndpointResolver.normalizeLoopbackURL("127.0.0.1:4096"),
            "http://127.0.0.1:4096"
        )
        XCTAssertEqual(
            OpenCodeEndpointResolver.normalizeLoopbackURL("localhost:4096"),
            "http://127.0.0.1:4096"
        )
    }

    func testNormalizeStripsPathQueryFragment() {
        XCTAssertEqual(
            OpenCodeEndpointResolver.normalizeLoopbackURL("http://127.0.0.1:4096/global/event?x=1#/a"),
            "http://127.0.0.1:4096"
        )
    }

    func testNormalizeRejectsNonLoopback() {
        XCTAssertNil(OpenCodeEndpointResolver.normalizeLoopbackURL("http://0.0.0.0:64667"))
        XCTAssertNil(OpenCodeEndpointResolver.normalizeLoopbackURL("http://192.168.1.5:64667"))
        XCTAssertNil(OpenCodeEndpointResolver.normalizeLoopbackURL("http://example.com:64667"))
    }

    func testNormalizeRejectsHTTPS() {
        // OpenCode 本地 server 只服务 http。
        XCTAssertNil(OpenCodeEndpointResolver.normalizeLoopbackURL("https://127.0.0.1:64667"))
    }

    func testNormalizeRequiresPort() {
        XCTAssertNil(OpenCodeEndpointResolver.normalizeLoopbackURL("http://127.0.0.1"))
        XCTAssertNil(OpenCodeEndpointResolver.normalizeLoopbackURL(""))
        XCTAssertNil(OpenCodeEndpointResolver.normalizeLoopbackURL("   "))
    }

    // MARK: - resolve (T01)

    func testResolveExternalHttpEmptyURLErrorsNotConfigured() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .externalHttp, url: "", username: "opencode", password: "p"
        ))
        if case .failure(let err) = result {
            XCTAssertEqual(err, .notConfigured)
        } else { XCTFail("expected notConfigured") }
    }

    func testResolveExternalHttpEmptyPasswordErrorsPasswordRequired() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .externalHttp, url: "http://127.0.0.1:4096", username: "opencode", password: ""
        ))
        if case .failure(let err) = result {
            XCTAssertEqual(err, .passwordRequired)
        } else { XCTFail("expected passwordRequired") }
    }

    func testResolveExternalHttpNonLoopbackErrors() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .externalHttp, url: "http://0.0.0.0:64667", username: "opencode", password: "p"
        ))
        if case .failure(let err) = result {
            if case .nonLoopbackURL = err { /* ok */ } else { XCTFail("expected nonLoopbackURL") }
        } else { XCTFail("expected nonLoopbackURL") }
    }

    func testResolveExternalHttpNormalizesURLAndDefaultsUser() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .externalHttp, url: "http://localhost:4096", username: "", password: "secret"
        ))
        if case .success(let ep) = result {
            XCTAssertEqual(ep.url, "http://127.0.0.1:4096")
            XCTAssertEqual(ep.username, "opencode")
            XCTAssertEqual(ep.password, "secret")
            XCTAssertEqual(ep.source, .externalHttp)
            XCTAssertFalse(ep.legacyInsecureUnverified)
        } else { XCTFail("expected success") }
    }

    func testResolveExternalHttpKeepsCustomUsername() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .externalHttp, url: "127.0.0.1:4096", username: "alice", password: "p"
        ))
        if case .success(let ep) = result {
            XCTAssertEqual(ep.username, "alice")
            XCTAssertEqual(ep.url, "http://127.0.0.1:4096")
        } else { XCTFail("expected success") }
    }

    func testResolveLegacy64667UsesFixedLoopbackURL() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .legacy64667, url: "ignored", username: "", password: "secret"
        ))
        if case .success(let ep) = result {
            XCTAssertEqual(ep.url, "http://127.0.0.1:64667")
            XCTAssertEqual(ep.username, "opencode")
            XCTAssertEqual(ep.source, .legacy64667)
        } else { XCTFail("expected success") }
    }

    func testResolveDisabledErrorsNotConfigured() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .disabled, url: "http://127.0.0.1:4096", username: "u", password: "p"
        ))
        if case .failure(let err) = result {
            XCTAssertEqual(err, .notConfigured)
        } else { XCTFail("expected notConfigured") }
    }

    func testResolveServiceDiscoveryFutureUnavailable() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .serviceDiscoveryFuture, url: "", username: "", password: ""
        ))
        if case .failure(let err) = result {
            XCTAssertEqual(err, .serviceDiscoveryUnavailable)
        } else { XCTFail("expected serviceDiscoveryUnavailable") }
    }

    func testSourceSwitchPreservesCredentials() {
        // 同一份 user/pass, 仅 source 不同, 解析后凭据一致 (external_http vs legacy)。
        let cfg = OpenCodeEndpointConfig(source: .externalHttp, url: "127.0.0.1:4096", username: "u", password: "p")
        let r1 = OpenCodeEndpointResolver.resolve(cfg)
        let r2 = OpenCodeEndpointResolver.resolve(.init(source: .legacy64667, url: cfg.url, username: cfg.username, password: cfg.password))
        guard case .success(let e1) = r1, case .success(let e2) = r2 else {
            return XCTFail("both should resolve")
        }
        XCTAssertEqual(e1.username, e2.username)
        XCTAssertEqual(e1.password, e2.password)
    }

    // MARK: - migration (T01)

    func testMigrationExistingCredsWithoutExplicitSourceFallsToLegacy() {
        XCTAssertEqual(
            OpenCodeEndpointResolver.migratedSource(explicit: nil, fileExistedWithCreds: true),
            .legacy64667
        )
        XCTAssertTrue(OpenCodeEndpointResolver.isLegacyMigration(explicit: nil, fileExistedWithCreds: true))
    }

    func testMigrationFreshInstallDefaultsManagedLocal() {
        XCTAssertEqual(
            OpenCodeEndpointResolver.migratedSource(explicit: nil, fileExistedWithCreds: false),
            .managedLocal
        )
        XCTAssertFalse(OpenCodeEndpointResolver.isLegacyMigration(explicit: nil, fileExistedWithCreds: false))
    }

    func testResolveManagedLocalIsRuntimeManagedNotPureEndpoint() {
        let result = OpenCodeEndpointResolver.resolve(.init(
            source: .managedLocal, url: "", username: "opencode", password: "p"
        ))
        if case .failure(let err) = result {
            XCTAssertEqual(err, .notConfigured)
        } else { XCTFail("managed_local endpoint must be resolved by OpenCodeManagedServer at runtime") }
    }

    func testMigrationExplicitSourceRespected() {
        XCTAssertEqual(
            OpenCodeEndpointResolver.migratedSource(explicit: .externalHttp, fileExistedWithCreds: true),
            .externalHttp
        )
        XCTAssertFalse(OpenCodeEndpointResolver.isLegacyMigration(explicit: .externalHttp, fileExistedWithCreds: true))
    }
}

// MARK: - Health validator (T02)

final class OpenCodeHealthValidatorTests: XCTestCase {

    private func endpoint(_ source: OpenCodeServerSource = .externalHttp, url: String = "http://127.0.0.1:4096", user: String = "opencode", pass: String = "secret") -> OpenCodeEndpoint {
        OpenCodeEndpoint(source: source, url: url, username: user, password: pass)
    }

    /// 构造一个 stub fetch：对无 Authorization 头与有 Authorization 头的请求分别返回不同响应。
    private func stubFetch(noAuthStatus: Int, noAuthBody: Data = nonOpencodeBody,
                           authedStatus: Int? = nil, authedBody: Data? = nil,
                           noAuthError: Error? = nil) -> OpenCodeHealthValidator.Fetch {
        return { req in
            let hasAuth = req.value(forHTTPHeaderField: "Authorization") != nil
            if !hasAuth {
                if let err = noAuthError { throw err }
                return (noAuthBody, makeHTTPResponse(noAuthStatus))
            }
            let status = authedStatus ?? noAuthStatus
            let body = authedBody ?? noAuthBody
            return (body, makeHTTPResponse(status))
        }
    }

    func testHealthyServerNoAuth401Authed200() async {
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 401, authedStatus: 200, authedBody: opencodeHealthBody
        ))
        let result = await v.validate(endpoint())
        if case .success(let ep) = result {
            XCTAssertFalse(ep.legacyInsecureUnverified)
        } else { XCTFail("expected success, got \(result)") }
    }

    func testAuthFailedWhenAuthed401() async {
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 401, authedStatus: 401
        ))
        let result = await v.validate(endpoint())
        if case .failure(let err) = result {
            XCTAssertEqual(err, .authFailed)
        } else { XCTFail("expected authFailed") }
    }

    func testServerUnauthenticatedWhenNoAuth200OpenCodeBody() async {
        // no-auth 200 + OpenCode body → server 未启用认证 → external_http 拒绝。
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 200, noAuthBody: opencodeHealthBody, authedStatus: 200, authedBody: opencodeHealthBody
        ))
        let result = await v.validate(endpoint())
        if case .failure(let err) = result {
            XCTAssertEqual(err, .serverUnauthenticated)
        } else { XCTFail("expected serverUnauthenticated") }
    }

    func testPasswordlessServerWithAuthHeaderStill200Rejected() async {
        // 即便带 opencode:<configured-password>, 无密码 server 仍返回 200 → 必须拒绝。
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 200, noAuthBody: opencodeHealthBody, authedStatus: 200, authedBody: opencodeHealthBody
        ))
        let result = await v.validate(endpoint())
        if case .failure(let err) = result {
            XCTAssertEqual(err, .serverUnauthenticated)
        } else { XCTFail("expected serverUnauthenticated") }
    }

    func testUnreachableWhenConnectionFails() async {
        let v = OpenCodeHealthValidator(fetch: { _ in
            throw URLError(.cannotConnectToHost)
        })
        let result = await v.validate(endpoint())
        if case .failure(let err) = result {
            if case .unreachable = err { /* ok */ } else { XCTFail("expected unreachable, got \(err)") }
        } else { XCTFail("expected unreachable") }
    }

    func testNonLoopbackRejectedBeforeNetwork() async {
        // resolve 已拦截 non-loopback；validator 防御性 password 检查：
        // external_http + 空 password → passwordRequired（不触网）。
        let v = OpenCodeHealthValidator(fetch: { _ in
            XCTFail("should not hit network")
            return (Data(), makeHTTPResponse(200))
        })
        let result = await v.validate(OpenCodeEndpoint(
            source: .externalHttp, url: "http://127.0.0.1:4096", username: "u", password: ""
        ))
        if case .failure(let err) = result {
            XCTAssertEqual(err, .passwordRequired)
        } else { XCTFail("expected passwordRequired") }
    }

    func testMalformedHealthResponseRejected() async {
        // 200 但 body 非 OpenCode schema → notOpencodeServer。
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 200, noAuthBody: Data("not json".utf8), authedStatus: 200, authedBody: Data("not json".utf8)
        ))
        let result = await v.validate(endpoint())
        if case .failure(let err) = result {
            XCTAssertEqual(err, .notOpencodeServer)
        } else { XCTFail("expected notOpencodeServer") }
    }

    func testNoAuthOtherStatusRejectedAsNotOpenCode() async {
        // no-auth 404 → notOpencodeServer。
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 404, noAuthBody: nonOpencodeBody
        ))
        let result = await v.validate(endpoint())
        if case .failure(let err) = result {
            XCTAssertEqual(err, .notOpencodeServer)
        } else { XCTFail("expected notOpencodeServer") }
    }

    // MARK: - legacy exception (T02)

    func testLegacyNoAuth401Authed200PassesWithoutInsecureFlag() async {
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 401, authedStatus: 200, authedBody: opencodeHealthBody
        ))
        let result = await v.validate(endpoint(.legacy64667, url: "http://127.0.0.1:64667"))
        if case .success(let ep) = result {
            XCTAssertFalse(ep.legacyInsecureUnverified, "authed legacy endpoint should not be flagged insecure")
        } else { XCTFail("expected success") }
    }

    func testLegacyNoAuth200OpenCodeBodyFlagsInsecureButUsable() async {
        // legacy + no-auth 200 + OpenCode body → 兼容例外：可用但标 legacy_insecure_unverified。
        let v = OpenCodeHealthValidator(fetch: stubFetch(
            noAuthStatus: 200, noAuthBody: opencodeHealthBody
        ))
        let result = await v.validate(endpoint(.legacy64667, url: "http://127.0.0.1:64667"))
        if case .success(let ep) = result {
            XCTAssertTrue(ep.legacyInsecureUnverified, "unauthed legacy endpoint must be flagged insecure")
        } else { XCTFail("expected success (legacy exception), got \(result)") }
    }
}
