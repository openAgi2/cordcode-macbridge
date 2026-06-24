import XCTest
import Network
@testable import CordCodeLink

// T07: 验证 ManagementAPIClient 的短超时行为。
// 纯 unit test：用本地 NWListener server 模拟"接受连接但不返回响应"（半开），不依赖 UI automation。
final class ManagementAPIClientTimeoutTests: XCTestCase {

    /// 半开 server：accept 连接但永远不写回 HTTP 响应，模拟卡死的 management server。
    /// pre-fix（URLSession.shared 默认超时）会让 getStatus 卡数十秒；post-fix 须在 resource 超时（5s）内失败。
    func testStatusTimesOutAgainstNonRespondingServer() async throws {
        let server = HalfOpenHTTPServer()
        try server.start()
        defer { server.stop() }

        let url = "http://127.0.0.1:\(server.port)"
        let client = try ManagementAPIClient(baseURL: url, token: "t")

        let start = Date()
        do {
            _ = try await client.getStatus()
            XCTFail("getStatus unexpectedly succeeded against non-responding server")
        } catch {
            // 期望：超时错误，且在 ≤8s 内返回（resource 超时 5s + 余量）。
            let elapsed = Date().timeIntervalSince(start)
            XCTAssertLessThan(elapsed, 8.0, "getStatus hung \(elapsed)s — short timeout (T07) not effective")
        }
    }

    /// 正常 server：status 端点立即返回。status 成功不被慢 agents 端点阻塞（解耦由 pollManagementAPI
    /// 把 agents 放独立 task 保证；这里验证 status 本身对慢 server 也有上限）。
    func testStatusReturnsQuicklyFromHealthyServer() async throws {
        let server = StubHTTPServer(statusBody: #"{"status":"ready"}"#)
        try server.start()
        defer { server.stop() }

        let url = "http://127.0.0.1:\(server.port)"
        let client = try ManagementAPIClient(baseURL: url, token: "t")

        let start = Date()
        let status = try await client.getStatus()
        let elapsed = Date().timeIntervalSince(start)
        XCTAssertEqual(status.status, "ready")
        XCTAssertLessThan(elapsed, 3.0, "status against healthy server took \(elapsed)s")
    }
}

// MARK: - 测试 HTTP server helpers（基于 Network.framework，无 raw socket）

/// 接受连接但不响应任何内容（半开），用来触发客户端短超时。
private final class HalfOpenHTTPServer {
    private var listener: NWListener?
    private(set) var port = 0

    func start() throws {
        let listener = try NWListener(using: .tcp, on: .any)
        listener.newConnectionHandler = { conn in
            conn.start(queue: .global())
            // 接收请求但不响应：连接保持打开，模拟卡死的 server。
            conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { _, _, _, _ in
                // 故意不 send，让客户端等到超时。
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
        if port == 0 { throw NSError(domain: "server", code: 1) }
    }

    func stop() { listener?.cancel() }
}

/// 对所有请求返回固定 status JSON 的最小 HTTP server。
private final class StubHTTPServer {
    private var listener: NWListener?
    private(set) var port = 0
    private let statusBody: String

    init(statusBody: String) { self.statusBody = statusBody }

    func start() throws {
        let listener = try NWListener(using: .tcp, on: .any)
        listener.newConnectionHandler = { [weak self] conn in
            conn.start(queue: .global())
            conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { data, _, _, _ in
                guard self != nil else { conn.cancel(); return }
                let body = self?.statusBody ?? "{}"
                let resp = "HTTP/1.1 200 OK\r\nContent-Length: \(body.count)\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n\(body)"
                conn.send(content: resp.data(using: .utf8), completion: .contentProcessed { _ in conn.cancel() })
                _ = data
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
        if port == 0 { throw NSError(domain: "server", code: 1) }
    }

    func stop() { listener?.cancel() }
}
