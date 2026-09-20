import XCTest
import Network
@testable import CordCodeLink

// followups v9 §4.3.5 wire fixtures：撤销 raw 请求经**真实 HTTP 层**（NWListener
// stub server）验证 2xx 分类表、非 2xx error code、64KiB 三边界与 lost response。
// 不得在 DeviceStore stub 伪造 code 绕过网络层（R6-B3.1）。
final class ManagementAPIClientRevokeDecodeTests: XCTestCase {

    private var server: RevokeFixtureServer!
    private var client: ManagementAPIClient!

    override func setUp() async throws {
        // 一个 server 服务全部用例：按 deviceId 路由响应形状。
        server = RevokeFixtureServer()
        try server.start()
        client = try ManagementAPIClient(baseURL: "http://127.0.0.1:\(server.port)", token: "test-token")
    }

    override func tearDown() async throws {
        server.stop()
    }

    // MARK: - 2xx 分类表（§4.3.2 九行，无重叠、固定优先级）

    func testRevokeClassifiesTwoHundredBodies() async {
        let cases: [(String, Data, RevokeAttemptOutcome)] = [
            ("empty", Data(), .protocolUnknown(.emptyBody)),
            ("whitespace", Data("   \n\t".utf8), .protocolUnknown(.emptyBody)),
            ("malformed", Data("not json".utf8), .protocolUnknown(.malformedJSON)),
            ("top-null", Data("null".utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("top-array", Data("[1,2]".utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("top-string", Data("\"yes\"".utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("top-number", Data("42".utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("top-bool", Data("true".utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("missing-key", Data("{}".utf8), .protocolUnknown(.missingRevokedKey)),
            ("revoked-null", Data(#"{"revoked":null}"#.utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("revoked-string", Data(#"{"revoked":"yes"}"#.utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("revoked-number", Data(#"{"revoked":1}"#.utf8), .protocolUnknown(.revokedTypeMismatch)),
            ("revoked-false", Data(#"{"revoked":false}"#.utf8), .protocolUnknown(.revokedFalse)),
            ("clean", Data(#"{"revoked":true,"deviceId":"d"}"#.utf8), .confirmedClean),
            ("cleanup-failure", Data(#"{"revoked":true,"pushCleanupError":"persist failed"}"#.utf8),
             .confirmedCleanupFailure(pushCleanupError: "persist failed")),
            ("cleanup-type-error", Data(#"{"revoked":true,"pushCleanupError":42}"#.utf8), .confirmedCleanupUnknown),
            ("cleanup-null", Data(#"{"revoked":true,"pushCleanupError":null}"#.utf8), .confirmedCleanupUnknown),
            ("cleanup-empty", Data(#"{"revoked":true,"pushCleanupError":""}"#.utf8), .confirmedCleanupUnknown),
            ("cleanup-whitespace", Data(#"{"revoked":true,"pushCleanupError":"   "}"#.utf8), .confirmedCleanupUnknown),
        ]
        for (id, body, expected) in cases {
            server.responses[id] = .respond(status: 200, body: body, includeContentLength: true)
            let outcome = await client.revokeDevice(id)
            XCTAssertEqual(outcome, expected, "deviceId=\(id) 分类不符")
        }
    }

    // MARK: - 非 2xx（error code 尽力解码；404 not_found 特例的输入）

    func testRevokeNonTwoXXDecodesServerErrorCode() async {
        server.responses["notfound"] = .respond(
            status: 404, body: Data(#"{"error":"not_found","message":"x"}"#.utf8), includeContentLength: true)
        server.responses["notfound-nocode"] = .respond(status: 404, body: Data(), includeContentLength: true)
        server.responses["server-error"] = .respond(
            status: 500, body: Data(#"{"error":"boom"}"#.utf8), includeContentLength: true)
        server.responses["server-error-nonstring"] = .respond(
            status: 500, body: Data(#"{"error":42}"#.utf8), includeContentLength: true)

        let a = await client.revokeDevice("notfound")
        XCTAssertEqual(a, .transportOrHTTPFailure(.httpStatus(status: 404, serverErrorCode: "not_found")))

        let b = await client.revokeDevice("notfound-nocode")
        XCTAssertEqual(b, .transportOrHTTPFailure(.httpStatus(status: 404, serverErrorCode: nil)))

        let c = await client.revokeDevice("server-error")
        XCTAssertEqual(c, .transportOrHTTPFailure(.httpStatus(status: 500, serverErrorCode: "boom")))

        let d = await client.revokeDevice("server-error-nonstring")
        XCTAssertEqual(d, .transportOrHTTPFailure(.httpStatus(status: 500, serverErrorCode: nil)))
    }

    // MARK: - 64KiB 三边界（R7-B3 读取阶段硬上限；R8-B3 reason 不被覆盖）

    func testRevokeBodyLimitBoundaries() async {
        // 恰好 65,536 B 的合法 JSON（内部空白填充）→ 接受。
        var exact = #"{"revoked":true,"deviceId":"d"}"#
        exact += String(repeating: " ", count: RevokeFixtureServer.bodyLimit - exact.utf8.count)
        XCTAssertEqual(exact.utf8.count, RevokeFixtureServer.bodyLimit)
        server.responses["exact"] = .respond(status: 200, body: Data(exact.utf8), includeContentLength: true)
        let accepted = await client.revokeDevice("exact")
        XCTAssertEqual(accepted, .confirmedClean, "恰好 65,536 B 应被接受")

        // 65,537 B（带 Content-Length）→ 提前拒绝路径 → responseTooLarge。
        var over = #"{"revoked":true,"deviceId":"d"}"#
        over += String(repeating: " ", count: RevokeFixtureServer.bodyLimit - over.utf8.count + 1)
        XCTAssertEqual(over.utf8.count, RevokeFixtureServer.bodyLimit + 1)
        server.responses["over"] = .respond(status: 200, body: Data(over.utf8), includeContentLength: true)
        let rejected = await client.revokeDevice("over")
        XCTAssertEqual(rejected, .transportOrHTTPFailure(.responseTooLarge), "65,537 B 应为 responseTooLarge")

        // 无 Content-Length 的流式超限 → 流式上限兜底（不单独依赖 header）。
        var chunked = #"{"revoked":true,"deviceId":"d"}"#
        chunked += String(repeating: " ", count: RevokeFixtureServer.bodyLimit - chunked.utf8.count + 1)
        server.responses["chunked"] = .respond(status: 200, body: Data(chunked.utf8), includeContentLength: false)
        let streamed = await client.revokeDevice("chunked")
        XCTAssertEqual(streamed, .transportOrHTTPFailure(.responseTooLarge), "chunked 超限应为 responseTooLarge")
    }

    // MARK: - lost response

    func testRevokeLostResponseMapsToNetworkCategory() async {
        server.responses["lost"] = .dropConnection
        let outcome = await client.revokeDevice("lost")
        guard case .transportOrHTTPFailure(.networkError) = outcome else {
            XCTFail("连接丢失应归 networkError，实际：\(outcome)")
            return
        }
    }
}

// MARK: - 测试 HTTP server（按 deviceId 路由响应形状；NWListener，无 raw socket）

private final class RevokeFixtureServer {
    static let bodyLimit = 65_536

    enum Behavior {
        case respond(status: Int, body: Data, includeContentLength: Bool)
        case dropConnection
    }

    var responses: [String: Behavior] = [:]
    private var listener: NWListener?
    private(set) var port = 0

    func start() throws {
        let listener = try NWListener(using: .tcp, on: .any)
        listener.newConnectionHandler = { [weak self] conn in
            conn.start(queue: .global())
            conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { data, _, _, error in
                guard error == nil, let data, let self else { conn.cancel(); return }
                let request = String(decoding: data, as: UTF8.self)
                guard let id = Self.deviceId(in: request), let behavior = self.responses[id] else {
                    conn.cancel()
                    return
                }
                switch behavior {
                case .dropConnection:
                    // 接受请求后直接断开：lost response（服务端可能已执行副作用）。
                    conn.cancel()
                case .respond(let status, let body, let includeContentLength):
                    let lengthHeader = includeContentLength ? "Content-Length: \(body.count)\r\n" : ""
                    let head = "HTTP/1.1 \(status) \(Self.reasonPhrase(for: status))\r\n"
                        + lengthHeader
                        + "Content-Type: application/json\r\nConnection: close\r\n\r\n"
                    var payload = Data(head.utf8)
                    payload.append(body)
                    conn.send(content: payload, completion: .contentProcessed { _ in conn.cancel() })
                }
            }
        }
        listener.stateUpdateHandler = { [weak self] state in
            if case .ready = state, let p = listener.port?.rawValue {
                self?.port = Int(p)
            }
        }
        listener.start(queue: .global())
        self.listener = listener
        let deadline = Date().addingTimeInterval(2)
        while port == 0 && Date() < deadline { usleep(10_000) }
        if port == 0 { throw NSError(domain: "RevokeFixtureServer", code: 1) }
    }

    func stop() { listener?.cancel() }

    private static func deviceId(in request: String) -> String? {
        guard let range = request.range(of: "/internal/devices/") else { return nil }
        let rest = request[range.upperBound...]
        guard let end = rest.range(of: "/revoke") else { return nil }
        return String(rest[..<end.lowerBound])
    }

    private static func reasonPhrase(for status: Int) -> String {
        switch status {
        case 200: return "OK"
        case 404: return "Not Found"
        case 500: return "Internal Server Error"
        default: return "Status"
        }
    }
}
