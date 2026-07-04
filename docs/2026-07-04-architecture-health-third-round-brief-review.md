# 架构健康第三轮开发交接文档 — 评审报告

日期：2026-07-04
被评审文档：[docs/2026-07-04-architecture-health-third-round-development-brief.md](2026-07-04-architecture-health-third-round-development-brief.md)（下称“本文”或“brief”）
评审性质：独立 agent 对 brief 做事实核实 + 范围/可执行性评审。**仅评审，不改 brief、不改代码。**
核实基准：iOS 源码 `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`（实测 1967 行）、配套测试、`scripts/hygiene-baseline.json`、`scripts/check-architecture-hygiene.sh`、`../cordcode-ios/OpenCodeiOS/project.yml`、`../cordcode-ios/scripts/run.sh`。所有行号、符号名为本轮 fresh 实测。

---

## TL;DR

**判定：方向正确、硬约束准确、测试优先序合理；但 brief 第 0/2 节的“当前源码切片”存在两处会误导施工 agent 的事实/范围问题，必须在开工前修正，否则第三轮交付会偏靶。**

必须修的两点：

1. **`attemptRelayConnection` 是不存在的符号。** brief 在第 0 节和第 2 节表格把它列为要提取的核心方法，但源码里实际叫 `attemptRelay`（[BridgeProvider.swift:834](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:834)）。全仓 `grep attemptRelayConnection` 零命中。
2. **源码切片遗漏了 transport-creation 层最大的一块——`runDirectRace` 及其三个竞速 actor。** brief 列出的候选拆分片段合计约 ~155 行，但同一层里被漏掉的 `runDirectRace`（162 行）+ `RaceTransportCollector`/`RaceResult`/`RaceCompletion`（~70 行）+ `runDirectSingle`/`runRelay`/`relayCredentials(for:)`（~48 行）合计 ~280 行——**漏列的比已列的还多**。而这恰恰是 brief 第 3 节 T2 “未采纳 transport 清理” 不变量的真实落点（`RaceTransportCollector` 就是收集未 adopt transport 的地方）。

修正这两点之后，brief 的其余部分（子域选择、测试优先序、硬约束、不做清单、提交边界）是成立的，可作为第三轮施工输入。

---

## 一、评审方法

| 维度 | 做法 |
|---|---|
| 事实核实 | 逐条对照 brief 的指标/符号名/测试名/脚本/路径与仓库实测 |
| 范围核实 | 把 brief 第 2 节“当前源码切片”表与 `BridgeProvider.swift` 实际 transport-creation 相关代码逐一比对，识别漏列与错列 |
| 可执行性核实 | 评估 brief 的 P0/P1/P2/P3 步骤、完成标准、设计约束是否可被施工 agent 无歧义执行 |
| 边界 | 不跑 build/test/simulator/真机；不改 brief、不改代码 |

---

## 二、事实核实（逐条）

### 2.1 已核实正确 ✅

| brief 声明 | 核实结果 |
|---|---|
| 基线 lines=1967 / funcs=88 / ForTesting=36 | `wc -l` = 1967；`grep -wo 'func' \| wc -l` = 88；`grep -o 'ForTesting' \| wc -l` = 36。三项与 `hygiene-baseline.json` 完全一致 |
| 计数法（funcs 用 `grep -wo 'func'`、forTesting 用 `grep -o 'ForTesting'`） | `hygiene-baseline.json` `_comment` 字段与脚本计数法自洽 |
| `connectBridge` / `connectTransport` / `runDirectPhase` / `attemptDirectPhase` / `adoptSuccessfulConnection` / `selectConnectionStrategy` / `shouldSwitchPath` 均存在 | 逐一命中 |
| `directPhaseFactoryForTesting` / `relayFactoryForTesting` / `connectTransportConnectForTesting` 三组测试注入存在 | 逐一命中（[806](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:806)/[807](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:807)/[853](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:853)） |
| `unadoptedTransportCleanupCount` 观测存在 | [855](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:855) |
| 现有测试 `BridgeLANFirstFallbackTests` / `BridgePathSwitchTests` / `GodObjectCharacterizationTests` 存在 | 三文件均在 `OpenCodeiOSTests/` |
| `testConnectTransport_nonBridgeErrorFailure_disconnectsUnadoptedTransport` 是 T2 起点 | [BridgeLANFirstFallbackTests.swift:469](../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift:469) |
| `connectTransport` 在非 `CCCodeBridgeError` 失败时也 disconnect 未 adopt transport | [715-721](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:715)：catch-all 里 `await transport.disconnect()` + `unadoptedTransportCleanupCount &+= 1` |
| `adoptSuccessfulConnection` 是唯一写 active connection state 的入口 | [1095-1180+](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:1095)：写 `transport`/`activeBridge`/`activeBridgeURLString`/`activeConnectionKind`/`cachedClients`/`connectionStatus`/`connectedBackends`/`runningSessions`；transport-creation 路径不写这些 |
| generation 过期不 adopt 旧 transport | `connectTransport` [722-728](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:722) 与 `adoptSuccessfulConnection` [1097-1106](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:1097) 双层 guard |
| test target 名 `CCCodeTests` | `project.yml:72,77` 确认 |
| scheme 名 `CordCode` | `project.yml:1` 确认 |
| iOS `scripts/run.sh device` | `../cordcode-ios/scripts/run.sh` 存在，接受 `device` 子命令 |
| `scripts/check-architecture-hygiene.sh` 存在 | 本仓 `scripts/` 下，`-rwxr-xr-x` |
| 连接策略矩阵已被现有测试覆盖（`selectConnectionStrategy`/`shouldSwitchPath`） | `BridgePathSwitchTests`（10+ 用例）+ `BridgeLANFirstFallbackTests.testStrategy_*`（10+ 用例）+ `GodObjectCharacterizationTests.testBridgeProviderConnectionStrategyMatrixBeforeSplitting` |

### 2.2 必须修正的事实/范围问题 ⚠️

#### F1（高）：`attemptRelayConnection` 是虚构符号，实际是 `attemptRelay`

**证据**：`grep -rn 'attemptRelayConnection' ../cordcode-ios/` 零命中。实际方法是 [`attemptRelay`](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:834)（签名 `attemptRelay(bridge:token:generation:)`）。

**brief 出现位置**：
- 第 0 节 line 29：列举“当前相关代码集中在 `connectBridge`、`connectTransport`、`attemptRelayConnection`、`runDirectPhase`、`attemptDirectPhase` …”
- 第 2 节表 line 73：行首 `` `attemptRelayConnection(...)` ``，角色“以 relay credentials 创建 relay transport 并连接”，处理“提取”。

**影响**：brief 第 1 节把 [`BridgeProvider.swift`](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift) 列为“开发前必须读”，施工 agent 真去读时会找不到 `attemptRelayConnection`，要么误判 brief 过时、要么自己揣测对应的是 `attemptRelay` 还是 `runRelay`。一个范围被收窄到“只拆一个子域”的 brief，连子域里核心方法的名字都写错，是不该出现的精度问题。

**修正**：把 brief 两处 `attemptRelayConnection` 改为 `attemptRelay`，并在第 2 节表格补一行说明 `attemptRelay` 的真实实现委托给 `runRelay`（见 F2）。

#### F2（高）：源码切片漏列 transport-creation 层 ~280 行，含最大单块 `runDirectRace` 与竞速 actor

brief 第 2 节表（line 69-78）列出的拆分候选合计约 ~155 行：

| 已列出 | 行号 | 行数 |
|---|---|---|
| `connectTransport` | 663-731 | ~69 |
| `attemptRelay`（brief 误写作 `attemptRelayConnection`） | 834-841 | ~8 |
| `runDirectPhase` | 765-778 | ~14 |
| `attemptDirectPhase` | 822-831 | ~10 |
| 三组测试注入 + cleanup count（vars+setters） | 806-859 散布 | ~54 |

但同一职责层、brief 没列出的：

| 漏列 | 行号 | 行数 | 为什么属于 transport creation |
|---|---|---|---|
| `runDirectRace` | 933-1095 | **~162** | 多候选并行竞速入口，是 `runDirectPhase` 多候选分支的真实实现；不 adopt、不注册 observer，与 `connectTransport` 同层 |
| `RaceTransportCollector`（actor） | 863-871 | ~9 | 收集竞速期间创建的 transport 以便后续清理——**这就是 brief T2“未采纳 transport 清理”要测的对象** |
| `RaceResult`（struct） | 873-878 | ~6 | 竞速胜出结果载体 |
| `RaceCompletion`（actor） | 880-932 | ~53 | 竞速同步原语，管理 continuation/failures/completion |
| `runDirectSingle` | 734-744 | ~11 | direct 单候选连接原语，被 `runDirectPhase` 调用 |
| `runRelay` | 747-762 | ~16 | relay 真实连接实现，被 `attemptRelay` 调用；和 `runDirectSingle` 是同构的“真实 attempt” |
| `relayCredentials(for:)` | 638-658 | ~21 | 从 SavedBridge + Keychain 组装 `RelayCredentials`，`runRelay` 的前置 |

漏列合计 **~278 行**——比已列出的还多。其中：

- **`runDirectRace` + 三个竞速类型（~230 行）是 transport-creation 层最大的单体**，且其内部 `RaceTransportCollector` 直接承载 brief 第 3 节 T2 想测的“未采纳 transport 清理”不变量。brief 完全没提它们。
- 这带来一个二选一的硬决策，brief 没给：
  - **(a) 第三轮把竞速机制一起搬到 connector**：那 connector 会吞下 ~230 行并发原语，line/func 下降可观，但风险陡增（竞速的生命周期、cleanup collector 与 generation guard 交织）。
  - **(b) 第三轮只搬 `connectTransport` + 单候选/relay attempt，竞速留原处**：那 `BridgeProvider.swift` 行数下降有限（~155 行里还有不少是 factory setter 必须留为转发），可能压不到能体现“god-object 变小”的程度。

无论选哪条，**brief 必须显式写明**，否则施工 agent 会在拆到一半时自己撞上这个决策，且第 5 节“明确不做”里又恰好没提“不拆 `runDirectRace`”，等于既没说做、也没说不做。

**修正**：第 2 节表补 7 行（上述漏列项）；第 0 节“一句话范围”明确是否包含竞速机制；若不含，把第 5 节“明确不做”补一条“不拆 `runDirectRace` 与竞速 actor”，并相应下调对 line/func 下降幅度的预期。

### 2.3 中等问题 ⚠️

#### M1：`BridgeTransportConnector`（独立类型）与 `BridgeProvider+TransportConnection.swift`（同类型 extension）的访问控制语义自相矛盾

brief line 82-86 给了两个选项：独立新文件 `BridgeTransportConnector.swift`，或同 module extension `BridgeProvider+TransportConnection.swift`，作为“避免访问控制震荡”的备选。

但 brief line 157-159 的设计约束——“connector 不能持有 UI 状态 / 不能写 `activeBridge`/`cachedClients`/`connectionStatus` / 不知道 `RecoveryCoordinator`”——**只有在 connector 是独立类型时才成立**。一个 `BridgeProvider` 的 same-module extension 可以无障碍写 `BridgeProvider` 的全部 private state，所谓“不能写”只是约定，不是编译期保证。

**影响**：施工 agent 若选 extension 路线（brief 明确允许），P1 的设计约束就成了纸面意愿，T3“connector 只返回 (transport,url,ack,kind)、不写 active state”的不变量也无法用类型隔离来强制——只能靠测试，而 brief 又把 T3 放在“测试注入 + 断言”层面（line 124-126），并不强制。

**修正**：二选一定调。要么：
- 推荐独立 `BridgeTransportConnector` 类型，extension 路线删掉（或降级为“仅在 connector 内部做物理分发，不再作为 BridgeProvider 拆分的备选”）；
- 或保留 extension 路线，但把“不能写 active state”从硬约束改为“约定 + 用测试断言 BridgeProvider 仍是唯一写入者”，并说明访问控制不隔离。

现在的写法让两条路都显得同等可行，但只有一条能兑现设计约束。

#### M2：P2“下调 baseline”没有量化目标，使“证明不会回涨”沦为形式

brief line 167-170 的要求：“lines 应小于 1967；funcs 应小于 88，除非……；ForTesting 应下降或持平”。第二轮 gap analysis G4 明确把“拆分 → 下调 baseline → CI 仍绿”列为第三轮要验证的机制——但 brief 没给预期下降幅度，也没说“下降 1 行也算过”还是“要下降到某量级”。

按 F2 的核算：

- 若选 F2(a)（含竞速）：`BridgeProvider.swift` 可下降 ~280-330 行，到 ~1640-1690；funcs 可下降 ~8-12；ForTesting（factory 三组 + cleanup count 迁出）可下降 ~6-10（但 strategy/reconnect 层的 ~10 个 ForTesting 留下）。
- 若选 F2(b)（不含竞速）：行数下降 ~80-130，funcs 下降 ~3-5，且部分被“转发方法”抵消，ForTesting 下降有限。

两种情形的下降幅度差一个量级。brief 不给目标值，审计/验收时就只能判“只要小于 1967 就算赢”——这正是第二轮 gap analysis 担心的“baseline 下调机制流于形式”。

**修正**：第 4 节 P2 补一行预期值，例如“lines 目标 ≤ 1750（不含竞速）或 ≤ 1700（含竞速）；ForTesting 目标 ≤ 30”，并在完成报告里对照实测量解释偏差。

#### M3：P0“先补测试”与现有黑盒覆盖、与 connector 级测试只能 post-P1 之间存在张力

brief 第 3 节 T1 列了 5 条 direct/relay attempt 不变量，但实测 `BridgeLANFirstFallbackTests` 已覆盖 4 条（[testOrchestration_directSuccess_doesNotAttemptRelay:119](../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift:119)、[testOrchestration_directFailNoRelay_reportsRealFailure_noFakeFallback:161](../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift:161)、[testOrchestration_relayFirstBridge_relayOnlySkipsDirect:184](../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift:184)、[testOrchestration_multiCandidate_factoryReceivesMultipleCandidates:207](../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift:207)）；brief 自己也在 line 106 承认“现有已覆盖大部分”。

同时 T2 line 116 又说“拆分后应迁到 connector 直接测试”——即 connector 级测试只能在 P1 提取之后写，不可能在 P0 先写。

**影响**：P0 的真实可交付物其实是“确认现有 characterization 仍绿 + 补少量边界（如 relay-fallback 路径保留 first direct error 的断言）”，而不是 brief 字面暗示的“先建一张新测试网”。施工 agent 若按字面理解去 P0 写一堆 connector 级测试，会卡住（connector 还不存在）。

**修正**：把 P0 措辞改为“先确认现有 `BridgeLANFirstFallbackTests`/`GodObjectCharacterizationTests` 全绿作为提取保护网；可补的少量边界见 T1/T2 列表；真正新增的 connector 级测试在 P1 之后补”，避免与 P1 的先后矛盾。

### 2.4 低优先级问题 💬

- **L1（funcs 逃生舱过松）**：line 168 “funcs 应小于 88，除非提取为了访问控制临时增加极少数 forwarding 方法；若 funcs 未下降，完成报告必须解释为什么”。这个例外几乎可以合理化任何 funcs 不降的结果。建议与 M2 的量化目标绑定：“forwarding 方法不得超过 N 个，否则视为未完成提取”。
- **L2（验证命令硬编码绝对路径）**：line 175 用 `CORDCODE_IOS_ROOT=/Users/jacklee/Projects/cordcode-ios`。脚本默认就解析 `../cordcode-ios`（第二轮审计与 CI 都用默认/相对路径），无需绝对路径；绝对路径换机器就失效。建议删掉 env 前缀，或改用 `CORDCODE_IOS_ROOT=../cordcode-ios`。
- **L3（`CCCodeBridgeTransport` 构造依赖 `relayStore`）**：line 160 列 connector 接收项含 `SavedBridgeStore`，方向对；但实际构造（[682-691](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift:682)）需要 `relayStore: store`，relay 重连时读私钥。建议 brief 显式写明“connector 持有/接收 `SavedBridgeStore` 引用以构造 relay transport”，避免施工 agent 试图做成 store-less connector 后撞墙。

---

## 三、范围与策略评审

### 3.1 子域选择（transport creation）—— 成立 ✅

第二轮 gap analysis G1 给了三个候选子域：connection strategy / transport creation / recovery ownership。brief 选 transport creation 的理由（line 26-29）经核实成立：

- **测试覆盖最厚**：`BridgeLANFirstFallbackTests` 27 个 test、`BridgePathSwitchTests` 13 个 test、`GodObjectCharacterizationTests` 1 个 characterization，已覆盖策略矩阵 + generation 边界 + 多候选 + relay fallback + 未 adopt 清理。是三个子域里保护网最完整的。
- **与 recovery ownership 解耦最干净**：transport-creation 路径不碰 `recoveryCoordinator`（仅 `applyConnectionFailure` 调 `recoveryCoordinator.requestRecovery`，而 `applyConnectionFailure` 留在 BridgeProvider，不迁）。
- **行数迁移最实在**：含竞速时 ~280 行可迁，是三个子域里行数收益最大的。

### 3.2 硬约束（line 50-53）—— 准确 ✅

逐条核实：

- “不修改 SavedBridge 持久化格式 / Bridge wire protocol / pairing payload / Relay HPKE / Tailscale SPKI pin / backend capability 契约”——与 transport-creation 提取的代码路径无交集，约束可守。
- “不把 connection strategy、recovery ownership、session/history sync 顺手一起拆”——与代码 seam 一致（`selectConnectionStrategy`/`shouldSwitchPath`/`RecoveryCoordinator`/`applyConnectionFailure` 都在 transport-creation 之外）。
- “拆分必须让 BridgeProvider.swift line/func 下降 + 同步下调 hygiene-baseline.json”——方向对，但缺量化目标（见 M2）。

### 3.3 “明确不做”清单（line 197-204）—— 基本准确，但漏一条（见 F2）

`ChatViewModel+*`、`ChatUIKitContainerView.swift`、`claudecode.go`、`appserver_session.go`、`handlers.go` 继续细分、新建 connection state 文档、hygiene 5 段 inventory 全升级——这些推迟都正确（与第二轮 gap analysis G5/G6/G7 一致）。**唯一漏的是 `runDirectRace` 与竞速 actor 是否纳入本轮**——见 F2，建议补一行。

### 3.4 提交边界（line 226-233）—— 准确 ✅

两仓分别提交（iOS 一条/多条 + MacBridge baseline/文档一条），符合 CLAUDE.md “不混仓提交” 与 CHANGELOG 规则。

---

## 四、修改建议（按优先级，落到 brief 具体行）

| # | 优先级 | 位置 | 改动 |
|---|---|---|---|
| 1 | 高（F1） | line 29、line 73 | `attemptRelayConnection` → `attemptRelay`；line 73 表格行补充“真实实现委托给 `runRelay`” |
| 2 | 高（F2） | line 69-78 表格 | 补 7 行：`runDirectRace`、`RaceTransportCollector`、`RaceResult`、`RaceCompletion`、`runDirectSingle`、`runRelay`、`relayCredentials(for:)`，标注行号与“是否随 connector 迁出” |
| 3 | 高（F2） | line 197-204 “明确不做” | 显式写明 `runDirectRace`/竞速 actor 是否纳入本轮；若不纳入，补一行“不拆 `runDirectRace` 与竞速 actor” |
| 4 | 中（M1） | line 82-86、line 157-159 | 二选一：要么删 extension 备选、只留独立 `BridgeTransportConnector` 类型；要么保留 extension 但把“不写 active state”降为约定 + 测试断言，不称硬约束 |
| 5 | 中（M2） | line 163-170 P2 | 给量化目标：lines ≤ 1750（不含竞速）或 ≤ 1700（含竞速）；ForTesting ≤ 30；forwarding 方法上限 N |
| 6 | 中（M3） | line 132-144 P0 | 改措辞：P0 主要是“确认现有 characterization 全绿 + 补少量边界”，connector 级测试在 P1 之后补，避免与 P1 的先后矛盾 |
| 7 | 低（L1） | line 168 | 把 funcs 逃生舱与 forwarding 方法上限绑定（与 #5 合并） |
| 8 | 低（L2） | line 175 | 删掉硬编码绝对路径 `CORDCODE_IOS_ROOT=/Users/jacklee/...`，改用默认或相对路径 |
| 9 | 低（L3） | line 160 | 显式写明 connector 接收/持有 `SavedBridgeStore`（`relayStore`）以构造 relay transport |

---

## 五、总体判定

**通过，但需先修正 F1/F2 两处事实与范围问题再发车。**

- **方向与策略**：子域选择、测试优先序、硬约束、不做清单、提交边界均成立，与第二轮 gap analysis 的 G1/G4 闭环要求一致。
- **事实精度**：基线指标、测试名、符号存在性、脚本/路径大体准确；但 `attemptRelayConnection` 是虚构符号、源码切片漏列 ~280 行（含最大单块 `runDirectRace` 与 T2 不变量的真实落点 `RaceTransportCollector`）——这两处会让施工 agent 在“只拆一个子域”的窄任务里偏靶，必须先修。
- **可执行性**：P0/P1/P2/P3 主干清晰，但 P0 措辞与 connector 级测试的先后、P2 量化目标缺位、`BridgeTransportConnector` vs extension 的访问控制语义需补齐，否则验收标准偏软。

修完上述 9 条（其中 #1-#3 为发车前必须），brief 可作为第三轮施工输入。

---

## 六、评审边界

- 本评审 **fresh 复核**：brief 全部指标/符号名/测试名/脚本路径、`BridgeProvider.swift` transport-creation 层代码结构、`hygiene-baseline.json` 计数法、iOS 仓 scheme/target/scripts。
- 本评审 **未做**：build/test/simulator/真机复跑（属第三轮交付验收，不属 brief 评审）、对拆分后行数的实际测算（仅按当前源码静态估算）。
- 本评审 **未改变** brief、未改代码。
