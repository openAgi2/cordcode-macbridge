# 架构健康第三轮开发交接文档 — 第二轮评审报告

日期：2026-07-04
被评审对象：[docs/2026-07-04-architecture-health-third-round-development-brief.md](2026-07-04-architecture-health-third-round-development-brief.md) 经 commit `5cd16c1 Refine third architecture health brief` 修订后的版本
前序评审：[docs/2026-07-04-architecture-health-third-round-brief-review.md](2026-07-04-architecture-health-third-round-brief-review.md)（第一轮，9 条意见）
评审性质：核实 9 条意见是否真采纳 + 检查修订是否引入新矛盾。**仅评审，不改 brief、不改代码。**
核实基准：`git show 5cd16c1` 全量 diff、iOS 源码 fresh 复核、`BridgeLANFirstFallbackTests.swift` 测试调用点实测。

---

## TL;DR

**判定：9 条意见全部真采纳，无虚报；CHANGELOG 与第一轮评审报告已正确入库。但三条已采纳规则组合后引入一个新张力——现有黑盒测试直接在 `BridgeProvider` 上调用 4 个 `ForTesting` setter，若这些 setter 按修订迁出 connector，测试调用点必须在 P1 改写，而 brief 的“P0 现有测试全绿 / provider 黑盒测试继续保留”措辞没有承认这一点。**

不阻塞发车，但建议在 brief 第 3 节/第 4 节 P1 补一句明确：现有 `BridgeLANFirstFallbackTests` 的工厂注入调用点需从 `provider.setXxxFactoryForTesting(...)` 改写为经 connector 暴露的入口（如 `provider.transportConnector.setXxxFactoryForTesting(...)`），属 P1 测试适配工作，不违反“对外行为不变”。

另有两个次要精度问题（race 区域尾边界、race 阻塞与 lines 目标的耦合），见第三节。

---

## 一、9 条意见采纳核实

| # | 第一轮意见 | 修订位置 | 采纳核实 |
|---|---|---|---|
| F1 | `attemptRelayConnection` 不存在，实际 `attemptRelay` | line 29 列出 `attemptRelay`；line 77 表格行注明“源码中不存在 `attemptRelayConnection`”；line 260 采纳记录 | ✅ 已修正，并补“`attemptRelay` 委托 `runRelay`” |
| F2-a | 漏列 `runDirectRace` 与竞速 actor / `runDirectSingle`/`runRelay`/`relayCredentials` | line 29、line 73-79 表格补 7 行 | ✅ 全部补齐，附行号与角色 |
| F2-b | race 是否纳入本轮未决策 | line 78-79 标“本轮纳入提取”；line 220 明确“不把 `runDirectRace` 排除在本轮之外” | ✅ 显式纳入，且禁止静默绕开 |
| M1 | 独立 connector vs extension 语义冲突 | line 91 定调“独立类型，不采用 extension”；附类型边界理由 | ✅ 二选一定调，理由成立 |
| M2 | baseline 缺量化目标 | line 182-184：lines ≤1700、funcs ≤78、ForTesting ≤30 | ✅ 量化，且分别给出达标说明 |
| M3 | P0 与 connector 级测试先后矛盾 | line 99、142-143、172：P0 先确认现有黑盒全绿，connector 级测试 P1 后补 | ✅ 措辞修正，先后理顺 |
| L1 | funcs 逃生舱过松 | line 93、183：forwarding 方法上限 2 个，“超过则视为提取边界不干净” | ✅ 与 M2 绑定，逃生舱收紧 |
| L2 | 验证命令硬编码绝对路径 | line 190 改为 `CORDCODE_IOS_ROOT=../cordcode-ios` | ✅ 已改 |
| L3 | connector 需持有 `SavedBridgeStore` | line 73、169 明确作为 `relayStore` 输入/持有 | ✅ 已写明 |

**核实方式**：逐条对照 commit `5cd16c1` diff 与修订后 brief 全文；符号名/路径/计数法 fresh 复核 iOS 源码与 `hygiene-baseline.json`。brief 第 8 节“评审意见采纳记录”自称“未采纳项：无”——**核实属实**。

---

## 二、新发现问题（中等）：三条已采纳规则在测试注入调用点上互相收紧

### 现象

修订同时采纳了三条本意都正确的规则：

1. **(F2 采纳)** 工厂注入“随被测职责一起迁移”，即 `directPhaseFactoryForTesting` / `relayFactoryForTesting` / `connectTransportConnectForTesting` / `unadoptedTransportCleanupCount` 离开 `BridgeProvider` 进 connector；
2. **(M3 采纳)** P0 的第一目标是“让现有 `BridgeLANFirstFallbackTests` 等 characterization 在当前代码上保持全绿”（line 99、142），且 P1 “provider 黑盒测试继续保留，证明对外行为不变”（line 124）；
3. **(M2/L1 采纳)** forwarding 方法 ≤2、ForTesting ≤30。

但实测 [`BridgeLANFirstFallbackTests.swift`](../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/BridgeLANFirstFallbackTests.swift) 直接在 `provider`（`BridgeProvider`）上调用这 4 个迁移目标：

| 测试调用点 | 出现次数 | 是否属迁移集 |
|---|---|---|
| `provider.setDirectPhaseFactoryForTesting(...)` | line 129,149,169,197,215,237,259,274,290,304（10 处） | 是 |
| `provider.setRelayFactoryForTesting(...)` | line 130,152,172,198,241,260,276（7 处） | 是 |
| `provider.setConnectTransportConnectForTesting(...)` | line 476（1 处） | 是 |
| `provider.unadoptedTransportCleanupCountForTesting()` | line 478,485（2 处） | 是 |

### 推演

若严格按规则 1 把这 4 个 setter/getter 整体迁出 `BridgeProvider`，上述 20 处测试调用点会编译失败。要让测试重新通过，只有三条路：

- **(a)** `BridgeProvider` 保留 4 个 forwarding setter，转发到 `connector.setXxxForTesting(...)` —— 这会新增 4 个 `ForTesting` 方法（每个方法名 + 函数体调用各含一次 `ForTesting`，约 +6~8 次），**forwarding 数（4）> 上限 2**，且 ForTesting 降幅被吃掉一大半，**直接违反规则 3**；
- **(b)** 把测试调用点改写为经 connector 暴露的入口（如 `provider.transportConnector.setXxxFactoryForTesting(...)`）—— 这意味着 P1 期间必须编辑 `BridgeLANFirstFallbackTests.swift` 的 20 处调用点，**与规则 2 的“现有测试保留/全绿”字面表述有出入**（断言不变，但调用点改了）；
- **(c)** 只迁行为，不迁测试注入（setter 留 `BridgeProvider`，工厂变量留 `BridgeProvider`，connector 通过 `BridgeProvider` 拿工厂）—— 这违反规则 1 的“随被测职责一起迁移”，且 ForTesting 降幅落空。

三条路都至少违反一条已采纳规则。brief 没有指明走哪条。

### 影响

不阻塞发车——一个合格的施工 agent 大概率会选 (b)（改写调用点），因为这是唯一能同时守住三条规则精神的路径。但 brief 的 P0/P1 措辞目前暗示“现有测试不动”，会让 agent 在 P1 撞到“咦，测试编译不过了”时犹豫这是不是违反了 P0 承诺。

### 建议

在 brief 第 4 节 P1（line 155-176）补一句，明确这是允许的测试适配：

> P1 允许并预期编辑 `BridgeLANFirstFallbackTests` 等现有黑盒测试的工厂注入调用点（从 `provider.setXxxFactoryForTesting` 改为经 connector 暴露的入口，如 `provider.transportConnector.setXxxFactoryForTesting`），以兑现“工厂注入随被测职责迁移”。断言不变，对外行为不变；这不算违反 P0 的“现有测试全绿”，因为 P0 全绿针对的是 P1 提取前的状态。

补这一句后，三条规则自洽。

### ForTesting ≤30 目标在路径 (b) 下的可达性（佐证）

实测 `BridgeProvider.swift` 中 36 次 `ForTesting` 的拆分：

- **迁移集（11 次）**：`directPhaseFactoryForTesting`(3) + `relayFactoryForTesting`(3) + `connectTransportConnectForTesting`(3) + `unadoptedTransportCleanupCountForTesting`(1) + `setConnectTransportConnectForTesting`(1)，集中在 line 695/806/807/827/837/843/846/853/856/857/859；
- **留守集（~25 次）**：策略层（`connectionStrategyTraceForTesting`/`startStrategyTraceForTesting`/`strategyTraceForTesting`）+ recovery 层（`configureRecoveryForTesting`/`recoveryAttemptCountForTesting`/`triggerRecoveryForTesting` 等）+ reconnect 层（`hasActiveTransportReconnectingForTesting`）+ 其他观测（`storeForTesting`/`desiredBridgeForTesting` 等）。

走路径 (b)：36 − 11 = **25**，目标 ≤30 达标且有余量。
走路径 (a)：36 − 11 + ~7（forwarding 名 + 体）= **~32**，**超标**。

这进一步说明 brief 必须显式选 (b)，否则 ForTesting ≤30 与 forwarding ≤2 无法同时满足。

### funcs ≤78 / lines ≤1700 目标可达性（佐证）

- **funcs**：迁移集含 `relayCredentials`/`connectTransport`/`runDirectSingle`/`runRelay`/`runDirectPhase`/`attemptDirectPhase`/`attemptRelay`/`runDirectRace`（8）+ `RaceTransportCollector.add/removeAll`（2）+ `RaceCompletion.setContinuation/succeed/fail/failureCount`（4）+ 4 个 ForTesting setter = **~18 func**；`applyConnectionFailure`/`appendStrategyTrace`/`strategyTraceLabel` 在同区但不迁（属失败处理/策略层，留守）。88 − 18 + ≤2 forwarding = **≤72**，目标 ≤78 达标且有余量。
- **lines**：迁移体约 ~410 行（race 实际 ~123 行 + 竞速 actor ~70 + 其余 ~217，见第三节边界修正），1967 − 410 + ~30 桥接 = **~1587**，目标 ≤1700 达标且有余量（甚至偏保守，必要时可收紧到 ≤1620）。

三项量化目标在路径 (b) 下均可达成；问题只在 brief 是否显式选 (b)。

---

## 三、次要问题

### N1（低）：race 区域尾边界——`applyHelloAckLocalURLRefresh` 不属 race，应说明留守

brief line 78 称 `runDirectRace` 是“transport creation 层最大单块”（第一轮评审也沿用了 ~162 行的估算）。但实测 [`BridgeProvider.swift`](../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift)：

- `runDirectRace` 起于 line 933，止于 line **~1056**（下一函数是 line 1057 的 `nonisolated static func applyHelloAckLocalURLRefresh`），实际约 **~123 行**，不是 ~162 行；
- line 1057 `applyHelloAckLocalURLRefresh` 是 hello-ack 后刷新 SavedBridge 的 localURL 字段（adoption 相关），**属 adoption，不属 transport creation，应留守 `BridgeProvider`**。

第一轮评审的 ~162 行测算是用 `awk` 找下一个 `private func`/`func` 时未匹配 `nonisolated static func`，把 `applyHelloAckLocalURLRefresh` 错算进了 race 区。修订后的 brief 没有重复这个数，但 line 78“最大单块”的定性仍成立（~123 行仍是同层最大）。

**建议**：brief line 78-79 补一句“race 区止于 `applyHelloAckLocalURLRefresh` 之前，后者属 adoption 留守 `BridgeProvider`”，避免施工 agent 误把 `applyHelloAckLocalURLRefresh` 一起搬进 connector。

### N2（低）：race 阻塞与 lines ≤1700 目标的耦合应收紧

brief line 220（很好）规定“若 race 迁出破坏已验证语义，必须停止并写明阻塞，不能静默不拆”。但 line 182 对 lines 目标说的是“若未达标，完成报告必须逐项解释哪些片段未迁出及原因”——这给了“race 阻塞 → lines 不达标 → 解释一下就过”的软出口。

由于 lines ≤1700 的可达性本身就依赖 race 迁出（race 不迁则 `BridgeProvider` 只能降到 ~1750-1850），**race 阻塞应直接意味着 lines ≤1700 目标作废、整轮暂停等 owner 决策，而不是降级提交 + 文字解释**。

**建议**：line 182 补“若 race 按 line 220 触发阻塞，lines ≤1700 目标同步挂起，整轮暂停并升级 owner，不接受仅靠解释通过”。

---

## 四、已核实正确的修订

- **CHANGELOG.md**：commit diff 确认新增一条“按独立评审修订 brief……”条目，准确概括了符号修正、补切片、独立类型、量化目标四项，符合 CLAUDE.md 的 `[Unreleased]` 追加规则。
- **第一轮评审报告入库**：`docs/2026-07-04-architecture-health-third-round-brief-review.md` 作为新文件随 commit 入库（+212 行），head/tail 与原文一致，审查链完整。
- **量化目标本身可达**：funcs ≤78、ForTesting ≤30、lines ≤1700 在路径 (b) 下均有余量（见第二节佐证）。
- **路径修正**：`CORDCODE_IOS_ROOT=../cordcode-ios`（line 190）替换了硬编码绝对路径，与脚本默认解析一致。
- **独立类型定调**：line 91 的类型边界理由（“只有独立类型能用类型边界约束；extension 仍可访问私有状态”）准确反映了 Swift 访问控制事实。
- **race 纳入 + 禁止静默绕开**（line 220）是比第一轮要求更强的承诺，方向正确。

---

## 五、总体判定

**通过。** 9 条意见全部真采纳，commit 干净，CHANGELOG 与评审链完整，量化目标在合理路径下可达。

唯一需要在发车前补的是**第二节的新张力**：三条已采纳规则在测试注入调用点上互相收紧，brief 必须显式选“P1 改写现有测试的工厂注入调用点（断言不变）”这条路径，否则施工 agent 会在 ForTesting ≤30 / forwarding ≤2 / 测试编译通过之间被迫违反一条。补一句说明即可，不必改动量化目标或子域范围。

次要的 N1（race 尾边界）与 N2（race 阻塞与 lines 目标耦合）建议顺手修正，不阻塞。

---

## 六、评审边界

- 本评审 **fresh 复核**：commit `5cd16c1` 全量 diff、修订后 brief 全文、`BridgeProvider.swift` ForTesting/func/line 静态拆分、`BridgeLANFirstFallbackTests.swift` 工厂注入调用点、CHANGELOG diff、第一轮评审报告入库完整性。
- 本评审 **未做**：build/test/simulator/真机复跑（属第三轮交付验收）、对 race 迁出后的并发语义正确性验证（属 P1 实施评审）。
- 本评审 **未改变** brief、未改代码。
