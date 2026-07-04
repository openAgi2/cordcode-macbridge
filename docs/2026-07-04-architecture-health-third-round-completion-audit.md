# 架构健康第三轮完成情况 — 独立审计报告

日期：2026-07-04
被审计报告：`docs/2026-07-04-architecture-health-third-round-development-brief完成情况.md`
关联 brief：`docs/2026-07-04-architecture-health-third-round-development-brief.md`
Exec-Plan state：`.exec-plan/state/plan-b47d4fd1401b.json`
审计性质：独立复核完成报告、两仓工作树与可重跑验证。未运行 UI tests、snapshot tests、simulator automation 或真机操作（brief 硬约束；真机操作需 owner 授权）。

---

## 结论

**通过（带 1 项中优先级提交缺口 + 2 项低优先级口径修正）。第三轮完成报告的核心工程结论全部可复现：`BridgeProvider.swift` 实测 1629/71/27，transport creation 子域成功迁出到独立 `BridgeTransportConnector.swift`，connector 设计约束严格落地，hygiene baseline 下调且 strict gate 复跑通过。**

本次审计复跑/复核的可重跑证据全部成立：

- **度量复测**：`BridgeProvider.swift` lines=1629 / funcs=71 / forTesting=27，与完成报告和 `hygiene-baseline.json` 完全自洽，低于 brief 目标 ≤1700/≤78/≤30。
- **strict gate 复跑**：`CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh` 输出 `STRICT passed — no BridgeProvider net growth`（1629/71/27 ↔ 1629/71/27），exit 0。
- **connector 设计约束**：grep `activeBridge|cachedClients|connectionStatus|activeConnectionKind` 在 `BridgeTransportConnector.swift` 仅出现在文档注释（无代码写入）；`RecoveryCoordinator` 仅出现在注释；`applyHelloAckLocalURLRefresh` 0 命中 connector / 3 命中 provider；`BridgeProvider` 仅新增 1 个窄 forward `transportConnectorForTesting()`（≤2 上限）。
- **提取真实**：`BridgeProvider` 持有 `private let transportConnector: BridgeTransportConnector`、init 构造、`connectBridge` 调 `transportConnector.attemptDirectPhase/attemptRelay`；connector 承载 `connectTransport` / `relayCredentials` / `runDirectSingle` / `runRelay` / `runDirectPhase` / `attemptDirectPhase` / `attemptRelay` / `runDirectRace` + `RaceTransportCollector` / `RaceResult` / `RaceCompletion` + 三组测试 factory。
- **测试就位**：`BridgeTransportConnectorTests.swift` 6 条用例全部存在且断言内容与报告一致；新增 P0 双失败测试 `testOrchestration_directAndRelayBothFail_reportsRelayFailureWithFallbackTrace` 存在并断言 `relay.connect_failed` + `relay-fallback-after-direct-fail` trace。
- **exec-plan 自洽**：12 todos / 12 done / 12 proven，queue_hash `r3-all-proven-2026-07-04`，report_status `current`。

**唯一的真实缺口是提交尚未发生，且两仓工作树混入了与第三轮无关的改动**（详见 P3）。完成报告对此诚实（"commit hash 在提交后回填"），但 exec-plan 把 `p3-build-device-regression` 标为 proven 略超前于现实。

---

## Findings

### P3 — 中优先级：两仓均未提交，且工作树污染（提交边界尚未落地）

完成报告「诚实口径」段写"两仓提交边界清晰（iOS 一条提交 + MacBridge 文档/gate baseline 一条提交）"，brief 第 7 节也把"两仓提交边界清楚：iOS 代码一条或多条提交，MacBridge 文档/gate baseline 一条提交，不混仓提交"列为完成标准。审计发现：

1. **两仓均无第三轮实现提交**：
   - MacBridge `HEAD = cc246d9`（"Clarify third brief test adaptation"，第三轮 brief 文档微调，非实现）
   - iOS `HEAD = 655ee214`（"Share message renderer blocks across hosts"，第二轮提交）
2. **工作树混入与第三轮无关的改动**，且这些文件未出现在完成报告「关键文件变更」中：

   | 仓 | 污染文件 | 性质 |
   |---|---|---|
   | iOS | `CCCodeBridgeTransport.swift`（+7 行） | Relay prekey `urgentRefillNeeded` 特性 |
   | iOS | `ChatViewModel+Generation.swift`（+42）、`ChatViewModel+MessageSync.swift`（+9）、`RemoteRunningSessionTests.swift`（+75） | 与 transport creation 无关 |
   | MacBridge | `go-bridge/relay_prekey.go`、`handlers.go`、`handlers_relay.go`、`handlers_test.go`、`agent/claudecode/claudecode.go`、`agent/claudecode/session.go` | Claude 冷启动 relay 修复（CHANGELOG 已单列条目）+ prekey urgent-refill 特性 |

**影响**：若 owner 用 `git commit -a` 整体提交，会把 Claude 冷启动修复、prekey 特性、ChatViewModel 改动一并塞进"Extract Bridge transport connector" / "Record third architecture health pass"提交，违反 brief 第 7 节"不混仓提交"和完成报告"两仓提交边界清晰"的口径。

**缓解事实（重要）**：审计 grep 确认 `BridgeTransportConnector.swift` 零引用 `urgentRefillNeeded`，即第三轮提取在**代码层**与 prekey 特性完全解耦。因此分批 `git add` 仅第三轮文件（iOS: `BridgeTransportConnector.swift` / `BridgeProvider.swift` / `BridgeLANFirstFallbackTests.swift` / `BridgeTransportConnectorTests.swift` / `project.pbxproj`；MacBridge: `hygiene-baseline.json` / `CHANGELOG.md` 第三轮段 / 完成报告 / exec-plan state）在技术上是可行的，提交边界可以恢复清晰。

**建议 owner 提交前**：把污染改动（prekey 特性 / Claude 冷启动修复 / ChatViewModel 改动）逐文件 stage 到独立提交，第三轮文件单独成提交。

### L1 — 低优先级：exec-plan `p3-build-device-regression` 的 proven 标签略超前

该 todo `verification.summary` 把"两仓提交边界按 brief 第 7 节"描述为已落地，`artifacts` 仅列 `CHANGELOG.md` / 完成报告 / exec-plan state 三个文件。实际 commit 尚不存在，exec-plan 把"已写明提交计划 + 报告落盘"当作该 regression todo 的 proof。

这与完成报告"commit hash 在提交后回填"的诚实口径一致，**不算欺骗**，但 proven 标签略超前于现实——brief 第 6/7 节的真实完成标准包含"提交"这一步。建议提交发生后回填 commit hash，使该 todo 的 proof 与现实对齐。

### L2 — 低优先级：connector 行数口径小偏差

完成报告与 exec-plan 都称 `BridgeTransportConnector.swift` = 472 行；实测 478 行（差 6 行，~1.3%）。最可能的成因是报告写就后 connector 又被微调（对应完成报告「剩余风险」段提到的 `generationGuard` 闭包显式类型注解工作区）。不影响任何结论，建议完成报告订正为 478 或注明口径时间点。

---

## 复核证据

### 度量与 baseline

```bash
# BridgeProvider.swift 实测
f=../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift
echo "lines=$(wc -l < "$f") funcs=$(grep -wo 'func' "$f" | wc -l) forTesting=$(grep -o 'ForTesting' "$f" | wc -l)"
# → lines=1629 funcs=71 forTesting=27

# connector 行数
wc -l ../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeTransportConnector.swift
# → 478
```

`scripts/hygiene-baseline.json` 内容：`bridgeprovider.lines=1629 / funcs=71 / forTesting=27`，`_comment` 完整记录拆分入口、不变量测试与目标对齐。

### strict gate 复跑

```bash
CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh
```

输出（尾部）：

```text
== BridgeProvider net-growth gate ==
BridgeProvider.swift baseline -> current:
  lines:      1629 -> 1629
  funcs:      71 -> 71
  forTesting: 27 -> 27

== Gate status ==
Result: STRICT passed — no BridgeProvider net growth.
exit=0
```

### connector 设计约束

```bash
conn=../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeTransportConnector.swift
grep -n 'activeBridge|cachedClients|connectionStatus|activeConnectionKind' "$conn"
# → 仅文档注释 9-10 行命中，无代码写入
grep -n 'RecoveryCoordinator' "$conn"
# → 仅文档注释 11 行命中
grep -c 'applyHelloAckLocalURLRefresh' "$conn"   # → 0（保留在 BridgeProvider）
grep -c 'applyHelloAckLocalURLRefresh' "$prov"   # → 3
grep -n 'urgentRefillNeeded' "$conn"             # → 0（与 prekey 污染特性解耦）
```

### connector 迁出符号与 provider 委托

`BridgeTransportConnector.swift` 命中：`attemptDirectPhase` / `attemptRelay` / `relayCredentials(for:)` / `connectTransport` / `runDirectSingle` / `runRelay` / `runDirectPhase` / `runDirectRace` / `actor RaceTransportCollector` / `struct RaceResult` / `actor RaceCompletion` / `setDirectPhaseFactoryForTesting` / `setRelayFactoryForTesting` / `setConnectTransportConnectForTesting`。

`BridgeProvider.swift` 委托面：line 160 `private let transportConnector: BridgeTransportConnector`、line 167 init 构造、line 500 `transportConnector.configure(...)`、line 555/580 `transportConnector.attemptRelay(...)`、line 566 `transportConnector.attemptDirectPhase(...)`；保留 `adoptSuccessfulConnection`（757）/ `selectConnectionStrategy`（638）/ `shouldSwitchPath`（400）。

### 测试方法静态核验

`BridgeTransportConnectorTests.swift` 6 条用例：`testConnectTransport_nonBridgeError_incrementsCleanupCounter_andPropagates` / `testConnectTransport_bridgeError_incrementsCleanupCounter_andPropagates` / `testRunDirectRace_allCandidatesFail_propagatesAggregatedError_andCleansUp` / `testAttemptDirectPhase_factorySuccess_returnsInjectedResult` / `testAttemptRelay_factoryThrows_propagatesRealError` / `testAttemptDirectPhase_generationSuperseded_throwsSuperseded`。

`BridgeLANFirstFallbackTests.swift` 新增 P0 双失败测试：`testOrchestration_directAndRelayBothFail_reportsRelayFailureWithFallbackTrace`（line 234），断言 `err.code == "relay.connect_failed"` 与 trace 含 `relay-fallback-after-direct-fail`。

### exec-plan 结构

```bash
jq '{todos:(.todos|length), done:(.todos|map(select(.status=="done"))|length),
     proven:(.todos|map(select(.verification.status=="present"))|length),
     queue_hash:.reports.based_on_queue_hash, report_status:.reports.completion_report_status}' \
  .exec-plan/state/plan-b47d4fd1401b.json
# → {"todos":12,"done":12,"proven":12,"queue_hash":"r3-all-proven-2026-07-04","report_status":"current"}
```

### CHANGELOG

`CHANGELOG.md` `[Unreleased]` 下存在「2026-07-04 — 架构健康第三轮：BridgeProvider transport creation 子域提取（BridgeTransportConnector）」整段，覆盖 P0/P1/P2/P3 改了什么 + 有何提升 + 诚实口径。第三轮 brief 条目与第二轮条目均在。

---

## 残余风险

- **iOS 52 用例未独立复跑**：与第二轮审计同范围（第二轮亦未复跑 xcodebuild test）。本轮验证依靠：测试方法静态存在 + 断言内容核对 + strict gate 复跑 + 度量复测。若 owner 需要最强证据，可在 iPhone 17 Pro Max simulator 上跑 `xcodebuild test -only-testing:CCCodeTests/BridgeTransportConnectorTests -only-testing:CCCodeTests/BridgeLANFirstFallbackTests`。
- **真机 Debug 构建/安装/启动未独立复跑**：brief 硬约束真机操作需 owner 授权；审计接受完成报告提供的 devicectl artifact 路径（`/tmp/cordcode-realdevice/Build/Products/Debug-iphoneos/CordCode.app`、`Launched application with org.openagi.cordcode`）作为间接证据。
- **真机肉眼连接路径核对未做**：完成报告已归到 owner 人工验收清单（LAN 直连 / Relay 中转标签、切换网络路径），与 brief 第 6 节一致。
- **提交尚未发生 + 工作树污染**（见 P3）：这是交付前唯一需要 owner 介入的真实缺口。connector 与 prekey 特性代码层解耦，分批 `git add` 可恢复清晰边界。

---

## 审计判定

**通过。** 第三轮的工程结论（iOS god-object `BridgeProvider.swift` 实际变薄、transport creation 子域成功提取到独立 `BridgeTransportConnector.swift`、设计约束落地、baseline 下调且 strict gate 复跑通过）全部可复现。完成报告对核心度量的描述精确（1629/71/27 实测一致），对 connector 约束的描述真实（grep 零代码命中），对测试覆盖的描述可静态核验。

提交是唯一的真实缺口，且完成报告对此诚实（"commit hash 在提交后回填"）。建议 owner 下一步：

1. **提交前分批 stage**：把第三轮文件（iOS 5 个 + MacBridge 4 个）与污染改动（prekey 特性 / Claude 冷启动修复 / ChatViewModel）分开成独立提交，避免混仓。
2. **提交后回填**：把 iOS 与 MacBridge 两条 commit hash 回填到完成报告 `Related Commits` 段与 exec-plan `p3-build-device-regression` 的 artifacts，使该 todo 的 proven 标签与现实对齐（见 L1）。
3. **可选订正**：完成报告 connector 行数 472 → 478（见 L2）。
4. **人工验收**：按完成报告末尾「人工验收清单」做真机肉眼连接路径核对。

审计无需阻塞交付；上述 1–2 项是 owner 提交时即可完成的收尾动作。
