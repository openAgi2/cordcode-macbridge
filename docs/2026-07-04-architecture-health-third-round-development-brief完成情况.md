# 本轮任务完成情况：架构健康第三轮开发交接文档（BridgeProvider transport creation 子域提取）

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge`
- Plan: `docs/2026-07-04-architecture-health-third-round-development-brief.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge/.exec-plan/state/plan-b47d4fd1401b.json`
- Legacy State File: 无（首轮 sync，未走 legacy 路径）
- Completion Report Verdict: `proved-complete`
- Queue Summary: 12/12 todos done，12/12 proven
- Related Commits: 提交边界按 brief 第 7 节：iOS 仓 `c6ea889`（`Extract Bridge transport connector`）+ MacBridge 仓一条提交（`Record third architecture health pass`；commit hash 待提交后回填）。
- Generated At: 2026-07-04T06:00:00Z

## 1. Overall Verdict (总体结论)

第三轮按 brief P0 → P3 全量执行，**判定为 proved-complete**：iOS god-object `BridgeProvider.swift` 通过测试保护完成了 transport creation 子域提取，并把基线下调到能体现「god-object 实际变薄」的程度，strict gate 通过。

- `BridgeProvider.swift` 实测由 1967/88/36 降至 1629/71/27（lines −338，funcs −17，forTesting −9），均低于 brief 目标 ≤1700 / ≤78 / ≤30。
- transport creation 子域（含 brief 评审补入的 `runDirectRace` + 竞速 actor）整体迁出到独立 `BridgeTransportConnector.swift`（478 行，`@MainActor final class`）。
- 设计约束严格落地：connector 不写 `activeBridge` / `cachedClients` / `connectionStatus` / `activeConnectionKind`，不持 `RecoveryCoordinator`，不持 UI 状态；`applyHelloAckLocalURLRefresh` 保留在 BridgeProvider；`BridgeProvider` 仅新增 1 个窄 forward `transportConnectorForTesting()`，未超过 brief 的 ≤2 上限。
- 连接策略语义与失败语义不变（由现有黑盒 + 新增 connector 级测试共同证明）。
- iOS 仓定向 52 用例全绿；Debug 构建在已连接物理设备 iPhone 16 Pro 安装并启动成功。

未自动完成的部分（按 brief 第 6 节归到 owner 人工验收）：真实 socket 握手 / Relay 路径 / 真机肉眼连接状态核对。本文末尾「人工验收清单」集中列出。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence Summary |
| --- | --- | --- | --- | --- | --- |
| P0 测试保护 | proven-done | proven-done | proven-done | done | 现有黑盒 46 用例全绿 + 新增 direct/relay 双失败断言；基线锁定 1967/88/36 |
| P1 提取 connector | proven-done | proven-done | proven-done | done | 新增 BridgeTransportConnector.swift + BridgeTransportConnectorTests（6 用例）+ 现有黑盒 factory 注入改写；52 用例全绿；连接策略/失败语义不变回归通过 |
| P2 baseline 下调 | proven-done | proven-done | proven-done | done | hygiene-baseline.json 下调到 1629/71/27；strict gate 通过 |
| P3 build / 真机 | proven-done | proven-done | proven-done | done | iOS 定向 test 52 用例 + Debug 构建 iPhone 16 Pro 安装启动成功；两仓提交边界 + CHANGELOG + 本完成报告 |

## 3. Key File Changes (关键文件变更)

iOS 仓（`../cordcode-ios/`，落在第三轮提取提交）：
- `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeTransportConnector.swift`（新增，478 行）：独立类型，承载 `connectTransport` / `relayCredentials(for:)` / `runDirectSingle` / `runRelay` / `runDirectPhase` / `attemptDirectPhase` / `attemptRelay` / `runDirectRace` + `RaceTransportCollector` / `RaceResult` / `RaceCompletion` + 三组测试 factory 注入；通过 `configure(generationGuard:probeRoundNotifier:taskCountLogger:)` 单向接收 BridgeConnectionCoordinator 上下文。
- `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`（删减，1967 → 1629 行）：移除上述迁移代码；`connectBridge` 改为调用 `transportConnector.attemptDirectPhase/attemptRelay`；新增 `transportConnectorForTesting()` 单一窄 forward；保留 `applyHelloAckLocalURLRefresh`、`adoptSuccessfulConnection`、`selectConnectionStrategy`、`shouldSwitchPath`、`RecoveryCoordinator` 协调、`cleanupCurrentBridge`、`applyConnectionFailure` 等 connection-strategy / adoption / recovery 职责。
- `OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift`（扩展 + 改写）：新增 `testOrchestration_directAndRelayBothFail_reportsRelayFailureWithFallbackTrace`（P0 缺口）；factory/cleanup 调用点改写到 `provider.transportConnectorForTesting()`；新增 `makeDirectSuccess` / `makeRelaySuccess` helper 包 connector 结果类型。
- `OpenCodeiOS/OpenCodeiOSTests/BridgeTransportConnectorTests.swift`（新增）：6 条 connector 级定向测试（T2 清理 / race 全失败 / factory 抛真实错误 / superseded）。
- `OpenCodeiOS/CordCode.xcodeproj/project.pbxproj`（regenerate by XcodeGen）：纳入新文件。

MacBridge 仓（`/Users/jacklee/Projects/cordcode-macbridge/`，落在「Record third architecture health pass」提交）：
- `scripts/hygiene-baseline.json`（下调）：`bridgeprovider.lines=1629 / funcs=71 / forTesting=27`，`_comment` 记录拆分入口与不变量测试。
- `CHANGELOG.md`：`[Unreleased]` 下新增「2026-07-04 — 架构健康第三轮：BridgeProvider transport creation 子域提取（BridgeTransportConnector）」一节。
- `docs/2026-07-04-architecture-health-third-round-development-brief完成情况.md`（本文档）。
- `.exec-plan/state/plan-b47d4fd1401b.json`：12 个 todo 全部 proven done。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

iOS 仓定向 `xcodebuild test`（destination: iPhone 17 Pro Max simulator，UDID 11452B9C-5FB2-4C51-A912-F24C5F9FE96F）：

```
xcodebuild test \
  -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode \
  -destination 'platform=iOS Simulator,id=11452B9C-5FB2-4C51-A912-F24C5F9FE96F' \
  -only-testing:CCCodeTests/BridgeLANFirstFallbackTests \
  -only-testing:CCCodeTests/BridgePathSwitchTests \
  -only-testing:CCCodeTests/GodObjectCharacterizationTests \
  -only-testing:CCCodeTests/BridgeTransportConnectorTests
```

- Result: **TEST EXECUTE SUCCEEDED**，52 用例 0 failures（P0 46 用例 + P1 新增 connector 6 用例）。
- Main test files: `OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift`、`OpenCodeiOSTests/BridgePathSwitchTests.swift`、`OpenCodeiOSTests/GodObjectCharacterizationTests.swift`、`OpenCodeiOSTests/BridgeTransportConnectorTests.swift`。
- Artifact paths: `~/Library/Developer/Xcode/DerivedData/CordCode-asghlmpsvmlkwldpnhctfnoyaphh/Logs/Test/Test-CordCode-2026.07.04_13-47-25-+0800.xcresult`。

MacBridge 仓 strict gate：

```
CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh
```

- Result: **STRICT passed — no BridgeProvider net growth**（1629/71/27 ↔ 1629/71/27）。

### 4.2 Regression evidence

真机 Debug 构建 + 安装 + 启动（已连接物理设备 iPhone 16 Pro，UDID `BFC431AC-C205-56B2-BB4D-9EC0C57A0C05`）：

```
cd ../cordcode-ios && scripts/run.sh device --device BFC431AC-C205-56B2-BB4D-9EC0C57A0C05
```

- 构建成功：`/tmp/cordcode-realdevice/Build/Products/Debug-iphoneos/CordCode.app`。
- 安装成功：`xcrun devicectl device install app`（databaseUUID B1D667EF-…）。
- 启动成功：`Launched application with org.openagi.cordcode bundle identifier.`
- 未执行 UI automation / snapshot / 自动点击（brief 硬约束）。

连接策略与失败语义不变回归（人工核对 + 测试映射）：
- LAN-first + Relay fallback：`testOrchestration_directFailWithRelay_fallsBackToRelay`、`testOrchestration_multiCandidateAllFail_fallsBackToRelay` 通过。
- cellular relay-only：`testStrategy_cellularOnly_withRelay_relayOnly` 通过。
- explicit remote 不被 relay-only 覆盖：`testStrategy_explicitRemote_cellularOnlyWithRelay_notOverriddenToRelayOnly` 通过。
- relay-first 不试 direct：`testOrchestration_relayFirstBridge_relayOnlySkipsDirect` 通过。
- 无 relay fallback 暴露真实 direct 失败：`testOrchestration_directFailNoRelay_reportsRealFailure_noFakeFallback` 通过。
- 非 CCCodeBridgeError 也清理未 adopt transport：`BridgeTransportConnectorTests.testConnectTransport_nonBridgeError_incrementsCleanupCounter_andPropagates` + `testConnectTransport_bridgeError_incrementsCleanupCounter_andPropagates` 通过。
- generation 过期不 adopt：`BridgeTransportConnectorTests.testAttemptDirectPhase_generationSuperseded_throwsSuperseded` + BridgeProvider 侧 `adoptSuccessfulConnection` 保留 generation guard。

### 4.3 Audit downgrade summary

- 无降级。本轮首轮 sync 即开工，无遗留 `done` 需审计降级。

## 5. Remaining Risks / 非阻塞警告

- **真机肉眼连接路径核对未做**：脚本已构建+安装+启动，但 brief 第 6 节「连接策略语义不变」的最终肉眼确认（看 iPhone 上 LAN 直连 / Relay 中转标签、切换网络路径）需 owner 操作，归到末尾「人工验收清单」。
- **`generationGuard` 类型推断工作区**：connector 内对可选闭包属性 `(() async -> Bool)?` 使用 `guard let x = self.generationGuard` 直接绑定时，编译器报「conditional binding must have Optional type」；改用 `let fn: (() async -> Bool)? = self.generationGuard; guard let fn else {…}` 显式类型注解后通过。功能等价、行为不变，但若后续重构 connector 的闭包存储，需保留显式类型注解写法。
- **forwarding 数为 1**（`transportConnectorForTesting`），低于 brief ≤2 上限；如未来新增测试入口仍优先走 connector 暴露的方法，避免在 BridgeProvider 蓄新转发债。

## 6. Audit Focus (建议审核重点)

1. `BridgeTransportConnector.swift` 是否真正不写 active state：grep `activeBridge|cachedClients|connectionStatus|activeConnectionKind` 在该文件应零命中（除 NSLog 字面量外）。
2. `BridgeProvider.connectBridge` 编排是否保留：策略选择（`selectConnectionStrategy`）、generation/recovery 协调、`adoptSuccessfulConnection` 应仍在 BridgeProvider。
3. `runDirectRace` 提取边界是否止于 `applyHelloAckLocalURLRefresh` 之前：`applyHelloAckLocalURLRefresh` 应仍在 `BridgeProvider.swift`。
4. factory 注入改写后断言是否真的不变：`BridgeLANFirstFallbackTests` 的 32 条编排/标签/disabledRelay 断言与 P0 前等价。

## 7. Constraints (关键约束)

- 不修改 SavedBridge 持久化格式 / Bridge wire protocol / pairing payload / Relay HPKE / Tailscale SPKI pin / backend capability 字面契约。
- connector 不写 activeBridge / cachedClients / connectionStatus / activeConnectionKind，不持 RecoveryCoordinator，不持 UI 状态。
- `runDirectRace` 提取边界止于 `applyHelloAckLocalURLRefresh` 之前；若 race 迁出破坏已验证语义必须停止升级 owner——本轮 race 已成功迁出且测试通过，未触发阻塞。
- 未经 owner 明确允许不跑 UI tests / snapshot / simulator automation / 真机 UI 操作；真机操作仅限 Debug 构建+安装+启动。
- 两仓提交边界清晰：iOS 一条或多条、MacBridge 文档/gate baseline 一条，不混仓提交。
