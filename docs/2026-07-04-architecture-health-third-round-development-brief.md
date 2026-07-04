# 架构健康第三轮开发交接文档

日期：2026-07-04
输入来源：
- `docs/2026-07-04-architecture-health-second-round-gap-analysis.md`
- `docs/2026-07-04-architecture-health-second-round-development-brief完成情况.md`
- `docs/2026-07-04-architecture-health-second-round-completion-audit.md`
- `../cordcode-ios/CLAUDE.md`
- `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgePathSwitchTests.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/GodObjectCharacterizationTests.swift`

本文定位：给第三轮开发 agent 的直接施工输入。第三轮不再继续讨论第二轮是否完成；第二轮已收口并通过 iPhone message-web 视觉验收。第三轮只启动动作 3 的本体：iOS `BridgeProvider` 子域拆分。

---

## 0. 核心判断

第二轮完成了 web 共享包 5/5、`handlers.go` 物理分发和 BridgeProvider 净增长 strict gate。剩余最大缺口不是继续加报告，而是让 iOS god-object 开始实际变小。

第三轮主轴：**BridgeProvider transport creation extract-and-test**。

选择 `transport creation` 的原因：

- 连接策略矩阵已有 `BridgeLANFirstFallbackTests` / `GodObjectCharacterizationTests` 覆盖，适合在其后接一层更窄的构造与清理不变量；
- 它比 recovery ownership 更少牵动重连状态机，比 connection strategy 更接近实际行数迁移；
- 当前相关代码集中在 `BridgeProvider.swift` 的 `connectBridge`、`connectTransport`、`attemptRelayConnection`、`runDirectPhase`、`attemptDirectPhase`、测试 factory 注入与未 adopt transport 清理计数附近，适合先做同模块提取。

一句话范围：**先补 transport creation 不变量测试，再把 transport 构造/连接尝试/未采纳清理从 `BridgeProvider` 拆到独立职责文件；不改 protocol、pairing、relay crypto、路径选择语义或 recovery ownership。**

---

## 1. 必读文件与硬约束

开发前必须读：

1. 本仓 `AGENTS.md` 中 Build & test、Backend runtime model、CHANGELOG 规则。
2. `docs/2026-07-04-architecture-health-second-round-gap-analysis.md`。
3. `../cordcode-ios/CLAUDE.md`。
4. `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`。
5. `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`。
6. `../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift`、`BridgePathSwitchTests.swift`、`GodObjectCharacterizationTests.swift`。

硬约束：

- 未经 owner 当前任务明确允许，不运行 UI tests、snapshot tests、simulator automation 或自动操作真机 UI。
- 本轮改 iOS Swift 代码后，若检测到 connected physical iPhone，交付前必须按 iOS 仓规则自动执行 `scripts/run.sh device` 完成 Debug 构建、安装、启动；这不授权 UI 操作。
- 不在生产路径添加 fallback、mock、placeholder、假数据或缓存快照来掩盖真实失败。
- 不修改 `SavedBridge` 持久化格式、Bridge wire protocol、pairing payload、Relay HPKE/mailbox、Tailscale SPKI pin、backend capability 字面契约。
- 不把 connection strategy、recovery ownership、session/history sync 顺手一起拆。第三轮只拆一个子域。
- 拆分必须让 `BridgeProvider.swift` 的 line/function 指标下降；完成后同步下调 MacBridge 仓 `scripts/hygiene-baseline.json`，验证 strict gate 仍能通过。

---

## 2. 当前源码切片

`BridgeProvider.swift` 当前实测基线仍由第二轮 gate 冻结：

| 指标 | 基线 |
|---|---:|
| lines | 1967 |
| funcs | 88 |
| ForTesting occurrences | 36 |

本轮只针对 transport creation / attempt 层，候选代码范围：

| 片段 | 当前角色 | 本轮处理 |
|---|---|---|
| `connectBridge(_:mode:cancelsRecovery:)` | 顶层意图、策略选择、direct/relay 编排、adopt、状态处理 | 保留在 `BridgeProvider`，但把具体 transport attempt 委托出去 |
| `connectTransport(...)` | 构造 `CCCodeBridgeTransport`，调用 connect，失败时断开未 adopt transport | 提取到 transport connector / factory |
| `attemptRelayConnection(...)` | 以 relay credentials 创建 relay transport 并连接 | 提取 |
| `runDirectPhase(...)` / `attemptDirectPhase(...)` | direct 候选竞速入口，测试 factory 注入 | 提取或移到同 package extension 文件 |
| `directPhaseFactoryForTesting` / `relayFactoryForTesting` / `connectTransportConnectForTesting` / cleanup count | transport attempt 测试注入与观测 | 随被测职责一起迁移，避免 `BridgeProvider` 继续背测试注入债 |
| `adoptSuccessfulConnection(...)` | 写 active bridge/client/backend/running session 和通知 | 不拆；这是 connection adoption，不属于 transport creation |
| `selectConnectionStrategy(...)` / `shouldSwitchPath(...)` | 策略决策 | 不拆；已有测试，留作边界保护 |
| `RecoveryCoordinator` 调用面 | recovery ownership | 不拆 |

推荐新增文件名：

```text
../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeTransportConnector.swift
```

备选：若为避免访问控制震荡，可先用同目录同 module 的 `BridgeProvider+TransportConnection.swift` 做物理分发，再在完成报告中明确这是第一步；但第三轮目标仍要求 `BridgeProvider.swift` 指标下降，并且新文件只承载 transport creation / attempt，不承载策略或 recovery。

---

## 3. 先补不变量测试

第三轮必须先补测试，再拆代码。建议新增或扩展现有 unit test，不跑 UI tests。

### T1 direct/relay attempt 不变量

位置建议：扩展 `BridgeLANFirstFallbackTests.swift`，或新增 `BridgeTransportConnectorTests.swift`。

覆盖：

- direct 成功时不尝试 relay；
- direct 全失败且 `allowRelayFallback=true` 时尝试 relay fallback，并保留真实 first direct error 供失败路径展示；
- direct 全失败且无 relay fallback 时抛真实失败，不构造假成功；
- relay-first bridge 走 relay-only，不制造必然失败的 LAN probe；
- 多 direct candidates 会把完整 candidates 传入 direct attempt 层，顺序不被提取破坏。

现有 `BridgeLANFirstFallbackTests` 已覆盖大部分；第三轮要补的是“提取后 connector 层仍能独立验证”的最小测试，避免只测 `connectBridge` 黑盒。

### T2 未采纳 transport 清理不变量

覆盖：

- `connectTransport` 在 `transport.connect` 抛出非 `CCCodeBridgeError` 时也必须 disconnect 未 adopt transport；
- relay attempt 失败、direct attempt 失败都不能留下 active transport、state observer 或 cached client；
- 清理计数/观测只用于 test，不进入产品展示。

现有 `BridgeLANFirstFallbackTests.testConnectTransport_nonBridgeErrorFailure_disconnectsUnadoptedTransport` 是起点；拆分后应迁到 connector 直接测试，或保留 provider 黑盒测试再加 connector 级别测试。

### T3 adoption 边界不变量

覆盖：

- connector 只返回 `(transport, url, ack, kind)` 或等价结果，不写 `activeBridge`、`cachedClients`、`activeConnectionKind`；
- `adoptSuccessfulConnection` 仍是唯一写入 active connection state 的入口；
- generation 过期时不 adopt 旧 transport。

此组可以通过测试注入 fake result + 断言 `BridgeProvider` adoption 行为不变；不要为了测试创建 mock backend 成功路径之外的生产 fallback。

---

## 4. 拆分步骤

### P0：建立 transport creation 测试保护

1. 在 iOS 仓新增/扩展 unit tests，先在当前未拆代码上通过。
2. 只跑定向 unit test，不跑 UI tests：

```bash
cd ../cordcode-ios
xcodebuild test -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' \
  -only-testing:CCCodeTests/BridgeLANFirstFallbackTests
```

如新增测试类，把 `BridgeLANFirstFallbackTests` 替换为新类名。命令必须带 `-only-testing:CCCodeTests/...`。

### P1：提取 transport connector

建议产物：

- 新增 `BridgeTransportConnector.swift`；
- 把 transport creation / direct attempt / relay attempt / unadopted cleanup 相关类型与测试注入从 `BridgeProvider` 移出；
- `BridgeProvider.connectBridge` 保留策略选择、desired/terminal/recovery 协调、adopt 调用；
- `BridgeProvider` 不再直接 new `CCCodeBridgeTransport`，而是调用 connector。

设计约束：

- connector 不能持有 UI 状态；
- connector 不能写 `activeBridge` / `cachedClients` / `connectionStatus`；
- connector 不知道 `RecoveryCoordinator`；
- connector 可以接收 `SavedBridgeStore`、device token、bridge id、candidate 列表、generation 和测试注入；
- connector 返回真实成功结果或抛真实错误；不得吞错后返回占位 ack。

### P2：下调 strict baseline

拆分后在 MacBridge 仓同步更新 `scripts/hygiene-baseline.json`：

- `BridgeProvider.swift` lines 应小于 1967；
- funcs 应小于 88，除非提取为了访问控制临时增加极少数 forwarding 方法；若 funcs 未下降，完成报告必须解释为什么；
- ForTesting occurrences 应下降或持平，不能新增。

验证：

```bash
cd /Users/jacklee/Projects/cordcode-macbridge
CORDCODE_IOS_ROOT=/Users/jacklee/Projects/cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh
```

### P3：构建、安装、报告

iOS 代码改动后按 iOS 仓规则：

1. `xcrun devicectl list devices` 检查 connected physical iPhone；
2. 若有连接真机，运行：

```bash
cd ../cordcode-ios
scripts/run.sh device
```

3. 不做 UI automation / snapshot / 自动点击；
4. 完成报告写明：unit test/build/真机安装哪些已执行，哪些未执行，原因是什么。

---

## 5. 明确不做

第三轮不做：

- 不拆 `ChatViewModel+Generation.swift`、`ChatViewModel+CodexStreaming.swift`、`ChatViewModel+SessionManagement.swift`；
- 不拆 `ChatUIKitContainerView.swift`；
- 不拆 `agent/claudecode/claudecode.go` 或 `agent/codex/appserver_session.go`；
- 不继续细分 `go-bridge/handlers.go`，除非第三轮 transport connector 完成后还有明确余量；
- 不建立新的 connection state 模型文档；
- 不把 hygiene 的 5 段 warning inventory 全部升级为 fail。

这些都是后续轮次主题。第三轮完成标准只看一个问题：`BridgeProvider` 是否通过测试保护完成了 transport creation 子域提取，并用 baseline 下调证明不会回涨。

---

## 6. 完成标准

第三轮完成时必须同时满足：

- iOS 仓新增或扩展 transport creation 相关 unit tests，定向通过；
- `BridgeProvider.swift` 实际变薄，transport creation / direct+relay attempt 代码迁到独立职责文件；
- 连接策略语义不变：LAN-first + Relay fallback、cellular relay-only、explicit remote 不被 relay-only 覆盖、relay-first 不试 direct；
- 失败语义不变：无 relay fallback 时暴露真实 direct 失败，非 `CCCodeBridgeError` 也清理未 adopt transport；
- MacBridge 仓 `scripts/hygiene-baseline.json` 下调，strict gate 通过；
- iOS 代码修改后完成定向 build/test；如有 connected physical iPhone，完成 Debug 安装启动；
- `CHANGELOG.md` 与第三轮完成报告更新；
- 两仓提交边界清楚：iOS 代码一条或多条提交，MacBridge 文档/gate baseline 一条提交，不混仓提交。

---

## 7. 推荐提交边界

建议提交顺序：

1. iOS 仓：`Extract Bridge transport connector`  
   包含测试、Swift 提取、必要工程文件更新。
2. MacBridge 仓：`Record third architecture health pass`  
   包含第三轮完成报告、CHANGELOG、`hygiene-baseline.json` 下调、exec-plan state（如使用）。

不要把真机截图、临时日志、handoff 文件放进产品提交。
