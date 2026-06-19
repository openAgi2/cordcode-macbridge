import XCTest
@testable import CCCodeBridge

// T05: 验证 restart() 的 launch generation + 可取消 Task 收敛行为。
// 纯 unit test：用 /usr/bin/false（立即退出）作为可执行文件，观测 launchCount，
// 不依赖 UI automation / simulator。
final class RuntimeManagerRestartTests: XCTestCase {

    @MainActor
    private func makeManager() -> RuntimeManager {
        RuntimeManager(config: RuntimeConfig(
            executablePath: "/usr/bin/false",
            port: 0,
            dataDir: "/tmp/cccode-t05-\(UUID().uuidString)",
            logDir: "/tmp"
        ))
    }

    /// 100ms 内连续三次 restart()：必须只真正 launch 一次进程。
    @MainActor
    func testRapidRestartsConvergeToSingleLaunch() async {
        let manager = makeManager()
        // 三次 restart 在远短于 1.5s 延迟窗口内触发。
        manager.restart()
        manager.restart()
        manager.restart()

        // 窗口内不应有 launch（1.5s 延迟未到）。
        XCTAssertEqual(manager.launchCount, 0, "restart 期间不应立即 launch")

        // 等待 1.5s 延迟窗口 + 余量。只有最新 generation 的 Task 醒来 → 单次 launch。
        try? await Task.sleep(nanoseconds: 2_000_000_000)
        XCTAssertEqual(manager.launchCount, 1, "连续三次 restart 应收敛为单次 launch")
    }

    /// applyConfigAndRestart 应先原子改 config 再只 restart 一次。
    @MainActor
    func testApplyConfigAndRestartMergesFields() async {
        let manager = makeManager()
        let originalRemote = manager.config.remoteURL

        manager.applyConfigAndRestart { c in
            c.remoteURL = "wss://new.example.com/bridge"
            c.relayEndpoint = "wss://relay.example.com"
        }

        // config 已被原子更新。
        XCTAssertEqual(manager.config.remoteURL, "wss://new.example.com/bridge")
        XCTAssertEqual(manager.config.relayEndpoint, "wss://relay.example.com")
        XCTAssertNotEqual(manager.config.remoteURL, originalRemote)

        // 等待延迟窗口，应只 launch 一次。
        try? await Task.sleep(nanoseconds: 2_000_000_000)
        XCTAssertEqual(manager.launchCount, 1, "applyConfigAndRestart 应只触发单次 launch")
    }

    /// generation 回退：第一个 restart 的延迟 Task 醒来时若 generation 已变，不得 launch。
    /// 通过连续 restart 验证只有最后一次 generation 生效（已被 testRapidRestartsConvergeToSingleLaunch 覆盖）；
    /// 这里额外验证两次间隔较远的 restart 各自 launch（generation 不被错误吞掉）。
    @MainActor
    func testSpacedRestartsEachLaunch() async {
        let manager = makeManager()

        manager.restart()
        try? await Task.sleep(nanoseconds: 2_000_000_000) // 第一次 launch 完成
        let countAfterFirst = manager.launchCount
        XCTAssertEqual(countAfterFirst, 1, "第一次 restart 应 launch 一次")

        manager.restart()
        try? await Task.sleep(nanoseconds: 2_000_000_000)
        XCTAssertEqual(manager.launchCount, 2, "间隔较远的第二次 restart 应再次 launch")
    }

    /// 验证 relayEnabled 在配置映射中能正确设置。
    @MainActor
    func testRelayEnabledConfigMapping() async {
        let manager = makeManager()
        
        // 默认应当为 true
        XCTAssertTrue(manager.config.relayEnabled)
        
        manager.applyConfigAndRestart { c in
            c.relayEnabled = false
        }
        
        XCTAssertFalse(manager.config.relayEnabled)
        
        manager.applyConfigAndRestart { c in
            c.relayEnabled = true
        }
        
        XCTAssertTrue(manager.config.relayEnabled)
    }
}

