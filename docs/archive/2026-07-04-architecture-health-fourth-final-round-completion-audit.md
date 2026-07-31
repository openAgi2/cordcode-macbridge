# 架构健康第四轮（最终轮）完成情况 — 独立审计报告

日期：2026-07-04
被审计报告：`docs/2026-07-04-architecture-health-fourth-final-round-development-brief完成情况.md`
关联 brief：`docs/2026-07-04-architecture-health-fourth-final-round-development-brief.md`
Exec-Plan state：`.exec-plan/state/plan-8146dd664595.json`
审计性质：独立复核完成报告、两仓 commit、可重跑验证。未运行 UI tests / snapshot / simulator automation / 真机 UI 操作。

---

## 结论

**需修正（conditional pass）。第四轮 policy/state 技术交付本身扎实、设计约束严格落地、跨仓文档同步到位；但完成报告有两处必须修正的硬问题，不能直接当作闭环：**

1. **🔴 strict gate 当前 FAIL（exit 1）**，与报告 §6「STRICT passed」声明矛盾。P0 spurious-idle follow-up 修复（iOS `d93d36a5`）给 `ChatViewModel+Generation.swift` 加了 +35 行 / +1 func，超过 baseline 冻结值 2336/56；baseline 未同步上调，guard 逻辑也未迁入 policy。审计复跑实测：

   ```text
   chatviewmodel_generation: lines 2336 -> 2371 (+35), funcs 56 -> 57 (+1)
   ❌ lines net growth (+35)
   ❌ funcs net growth (+1)
   Result: STRICT FAILED — net growth detected. (exit=1)
   ```

2. **🟠 完成报告 verdict 自相矛盾**——同一文档两套口径共存：§0 头部 / §0.1 RESOLVED 块 / §8 行 167 称 `proved-complete` + 专项 `Closed`；§0.1 保留下半段 / §1 第 53 行 / §10 标题与正文仍称「暂不 Closed / audit-invalidated」。restoration 只更新了 §0/§0.1 顶部，没传播到 §1/§10/§0.1 下半段，读者无法判断本轮到底是否闭环。

本次审计独立复核成立的工程结论（在 P0 修复 `d93d36a5` 之前的第四轮本体 `9ba4e1d3` 范围内）：

- **policy 纯函数性**：`ChatTurnSyncPolicy.swift` grep 零代码访问 `ChatViewModel`/`@MainActor`/`async`/`await`（仅文档注释出现），`static func decideLoadMessages` 仅接受显式入参。
- **state holder 设计**：`ChatTurnSyncState` 为 `@MainActor final class`，含 `decideLoad`/`beginLoadIfAllowed`/`canApply`/`finishLoad`；`canApply` 4 路复核（session / initializationID / token / ownership），其中 ownership 复核正确放行 `allowFinalReconcile` 与 `mergeOnlyBecauseRemoteRunning`。
- **枚举完备**：`Ownership` 4 case、`LoadDecision` 5 case、`LoadTrigger` 8 case，全部命中 brief §3.1/§3.2 下限。
- **测试就位**：`ChatTurnSyncPolicyTests` 实测 25 条；`RemoteRunningSessionTests` 含全部 5 条命名测试（interleave / second-turn / spurious-idle / loadMessages-no-apply / polling-no-fetch）。
- **跨仓文档同步**：`IOS_MAC_INTERACTION_FLOW.md` §5.1、`GO_BRIDGE_ARCHITECTURE.md`「iOS live event vs history polling 消费边界」、MacBridge `CHANGELOG.md`（含 07-04 根因口径修订 + 第四轮 + spurious-idle 跨仓结论三段）全部就位。
- **P0 真闭环**：iOS `d93d36a5` commit message 详尽且与 §0.1 RESOLVED 描述一致；owner 真机验收证据（46s 保持 localSend / 连发两问正常）落在 commit message。prekey 确认为红鲱鱼，修复在 iOS 侧（与 `p0-claude-no-streaming-prekey-redherring` 记忆一致）。

---

## Findings

### P1 — 🔴 strict gate 当前 FAIL：`chatviewmodel_generation` 超 baseline，报告 §6 失真

**事实**：

- baseline `scripts/hygiene-baseline.json` 冻结 `chatviewmodel_generation` 为 2336/56（9ba4e1d3 状态）。
- iOS `d93d36a5 Fix Claude cold-start replay from spurious pre-first-token idle`（在 9ba4e1d3 之后、作为 P0 follow-up 修复）给 `ChatViewModel+Generation.swift` 加了 `shouldKeepClaudeLocalSendAliveBeforeFirstContent` 守卫方法（~25 行带详尽 doc comment）+ 2 处 idle 接线（各 4 行），合计 +35 行 / +1 func，实测 2371/57。
- baseline 未上调，guard 也未迁入 `ChatTurnSyncPolicy`/`State`。
- 审计复跑 `CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh` → `STRICT FAILED`，**exit 1**。
- 完成报告 §6 仍写「三条 baseline 实测…均无净增」「Result: STRICT passed」——这是 P0 修复**之前**的真值，`d93d36a5` 落地后已不成立。

**为何这是个问题，不只是口径**：strict gate 是本轮三项防回涨机制之一（policy + 测试 + gate）。P0 修复（报告 §0.1 RESOLVED 把它当作 verdict 恢复的依据）本身破坏了 gate，等于专项收口时三防机制之一已经失效。报告把 `d93d36a5` 当作闭环依据，却没注意到它让 gate 红灯。

**讽刺点（设计层）**：`shouldKeepClaudeLocalSendAliveBeforeFirstContent` 是一个 Claude-only ad-hoc 守卫，加在 `Generation.swift` 而非走 `ChatTurnSyncPolicy`——这正是 brief §4.4「禁止形态」第一条「只在 loadMessages 再加一个 backend-specific if，未形成统一 policy」要消除的模式。P0 热修为了快速止血，重新引入了本轮刚消灭的 pattern。（公平地说：它镜像既有 OpenCode 同款守卫，与现有代码风格一致，且为真机 P0 热修；但仍技术上回归了本轮设计目标，且 gate 红灯正反映了这一点。）

**修复（二选一）**：

1. 把 `shouldKeepClaudeLocalSendAliveBeforeFirstContent` 与其调用点迁入 `ChatTurnSyncPolicy`/`State`，让 `Generation.swift` 回到 ≤2336/56；或
2. 把 baseline 上调为 `chatviewmodel_generation: 2371/57`，在 `_comment` 写明「P0 spurious-idle 热修 +35/+1，待后续迁入 policy」并在报告 §6 / §0.1 RESOLVED 如实写明 gate 现状。

任一方案落地后，复跑 gate 必须 exit 0，并把复跑结果回填报告 §6。

### P2 — 🟠 完成报告 verdict 自相矛盾（Closed vs 暂不 Closed）

报告经历「初稿 proved-complete → audit 降级 → P0 修复后 restore」三层叠加，restoration 只改了顶部，没传播到全文。同一文档现在两套口径共存：

| 位置 | 口径 |
|---|---|
| §0 第 8 行（Verdict）/ 第 10 行（Plan Status） | `proved-complete` / `closed` |
| §0.1 RESOLVED 块（第 17-22、24 行） | 「verdict 恢复 proved-complete / 专项 Closed」 |
| §8 第 167 行 | 「本次架构健康专项到第四轮结束（Closed）」 |
| **§0.1 保留下半段（第 27-43 行）** | 「『Closed』需等 P0 作为独立修复项解决后再下」 |
| **§1 第 53 行** | 「专项暂不 Closed」 |
| **§10 标题（第 174 行）** | 「收口结论（audit-invalidated，暂不 Closed）」 |
| **§10 正文（第 176-178 行）** | 「专项暂不 Closed…需作为独立修复项解决后，才能把本专项标为 Closed」 |

读者无法判断本轮是否闭环。需统一：若 P0 已闭环（§0.1 RESOLVED 主张如此），应把 §0.1 下半段 / §1 第 53 行 / §10 标题与正文一并改为 Closed；若 P0 仍未闭环，则 §0/§0.1 顶部的 `closed`/`Closed` 是过度声明。

### P3 — 🟠 §9 commit 清单漏掉 P0 修复 commit 与报告 restoration commit

§9「两仓 commit hash」只列 iOS `9ba4e1d3` + MacBridge `cd9a178`+`da06183`。但报告 §0.1 RESOLVED 的核心论据——P0 spurious-idle 修复——落在 iOS `d93d36a5`；把 verdict 改回 proved-complete 的报告 restoration 落在 MacBridge `96d6406`（+`f35e65f` 回填 hash）。这两条 commit 是 verdict 从「降级」恢复到「proved-complete」的物证，§9 必须列出，否则读者无法溯源 restoration。

### L1 — 🟡 §0.1 下半段保留已证伪的 prekey framing

§0.1 RESOLVED 块（顶部）明确真因 = spurious pre-first-token idle、fix = iOS 守卫；但 §0.1 下半段（第 27-43 行，标注「历史记录」）仍大幅强调「prekey exhausted / availableCount: 0 / urgentRefill」作为 P0 证据。这与 `p0-claude-no-streaming-prekey-redherring` 记忆「prekey 是红鲱鱼」直接冲突。虽标注「历史记录」，但与紧邻的 RESOLVED 结论并存仍误导。建议在保留下半段首行加一句「以下 prekey framing 后经证实为误判（见 RESOLVED 块）」，或删除已证伪的 prekey 量化段落。

### L2 — 🟡 exec-plan state 无 closure 段、status 仍 `current`

`.exec-plan/state/plan-8146dd664595.json`：20/20 done+proven（自洽），但 `completion_report_status: "current"`、无 `closure` 段、无 `audit_downgrade` 字段。报告 §0 称「Plan Status: closed」，exec-plan state 未反映。这与第三轮 state（已加 `closure` 段）的处理不一致。建议若 owner 认定闭环，在 state 加 `closure` 段记录 verdict restore + commit hash。

### L3 — 🟡 pre-existing failing test（§5.3 已诚实披露，接受）

`RemoteRunningSessionTests/testClaudeCodeAssistantFinishedCompletesWithoutIdleEvent` 4 个 XCTAssert 失败，归因 `e018cb5f`。报告 §5.3 诚实披露并标注「非本轮引入、普通维护债」。审计接受此口径，但建议在 backlog 留一条跟踪项，避免长期绿测试被一个红测试稀释信号。

---

## 复核证据

### exec-plan 结构

```bash
jq '{todos:(.todos|length), done:(.todos|map(select(.status=="done"))|length),
     proven:(.todos|map(select(.verification.status=="present"))|length),
     verdict:.reports.completion_report_status, closure:(.closure.status // "none")}' \
  .exec-plan/state/plan-8146dd664595.json
# → {"todos":20,"done":20,"proven":20,"verdict":"current","closure":"none"}
```

20/20 done+proven 自洽。state 文件经 `git ls-files` 确认被跟踪。

### strict gate 复跑（关键）

```bash
CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh
```

```text
chatviewmodel_generation (../cordcode-ios/.../ChatViewModel+Generation.swift)
  lines:      2336 -> 2371
  funcs:      56 -> 57
  ❌ lines net growth (+35)
  ❌ funcs net growth (+1)
Result: STRICT FAILED — net growth detected in one or more baseline files. (exit=1)
```

`bridgeprovider`（1629/71/27）与 `chatviewmodel_messagesync`（1577/46）无净增；唯独 `chatviewmodel_generation` 红灯。增量来自 `d93d36a5`（`shouldKeepClaudeLocalSendAliveBeforeFirstContent` 方法 + 2 处 idle 接线），已逐行核对 diff。

### policy / state 设计约束

```bash
pol=../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatTurnSyncPolicy.swift
grep -nE 'ChatViewModel|@MainActor|actor |class |async |await ' "$pol"
# → 仅文档注释命中（line 8/9/112），无代码访问 → 纯函数 ✓
# 结构：struct ChatTurnSyncPolicy / enum Ownership(4) / enum LoadTrigger(8) / enum LoadDecision(5) / static func decideLoadMessages ✓

st=../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatTurnSyncState.swift
grep -nE '@MainActor|class ChatTurnSyncState|func canApply|func beginLoadIfAllowed|func decideLoad|func finishLoad' "$st"
# → @MainActor final class ChatTurnSyncState + 全部 4 个方法 ✓
# canApply 4 路复核（session / initializationID / token / ownership）逐行确认 ✓
```

### 两仓 commit 与文件清单

- iOS `9ba4e1d3 Harden Chat turn sync state-model (round 4 final)`：12 files, +1224/-21。含 `ChatTurnSyncPolicy.swift`(196) / `ChatTurnSyncState.swift`(208) / `ChatTurnSyncPolicyTests.swift`(441) 新增，`ChatViewModel*.swift` 5 个文件改动，`IOS_MAC_INTERACTION_FLOW.md`(+59) / `CHANGELOG.md`(+9) / `project.pbxproj`。与报告 §3 一致。
- iOS `d93d36a5 Fix Claude cold-start replay from spurious pre-first-token idle`：5 files, +172。含 `ChatViewModel+CodexStreaming.swift`(+10) / `ChatViewModel+Generation.swift`(+35) / `RemoteRunningSessionTests.swift`(+43) / `CHANGELOG.md`(+12) / `think.md`(+72)。commit message 详尽，含 owner 真机验收证据。
- MacBridge `cd9a178 Record fourth (final) architecture health pass`：6 files, +288/-32。含 `hygiene-baseline.json`(+15) / `check-architecture-hygiene.sh`(+110) / `ci.yml` / `GO_BRIDGE_ARCHITECTURE.md`(+23) / `CHANGELOG.md` / 完成报告。
- MacBridge `da06183 Restore executable bit on check-architecture-hygiene.sh`：mode-only。
- MacBridge `96d6406 Document Claude cold-start spurious-idle cross-repo finding; restore fourth-round report`：报告 restoration。
- MacBridge `f35e65f Backfill fourth-round commit hashes in completion report`：hash 回填。

### 跨仓文档同步

- `IOS_MAC_INTERACTION_FLOW.md` 第 156 行 `### 5.1 Turn ownership / history sync gate / final reconcile` ✓
- `GO_BRIDGE_ARCHITECTURE.md` 第 268 行 `### iOS live event vs history polling 消费边界` ✓
- MacBridge `CHANGELOG.md`：第四轮 policy 硬化条目（第 36 行）+ 07-04 修复条目根因口径修订（第 21-33 行，把 Mac `relayRunningKind` 降为 latent bug，iOS loadMessages 标为症状主因，符合 brief Phase D / H3）+ spurious-idle 跨仓结论（第 11 行）✓

### 工作树状态

- iOS：`M CLAUDE.md`(+26，新增 think.md 引用，独立 doc 改进，非第四轮)、`M project.pbxproj`(xcodegen 等价 diff，报告 §0.1 已说明)。
- MacBridge：`M plan-b47d4fd1401b.json`/`M 第三轮完成报告`（本会话上一任务加的 closure 段，非第四轮）、`M CLAUDE.md`、3 个未跟踪 handoff。

---

## 残余风险

- **gate 红灯未修前，专项不应视为真正闭环**：P1 是阻塞项。修复前任何 PR 触发 CI strict step 都会 fail。
- **P0 spurious-idle 修复的设计债**：Claude-only ad-hoc guard 在 `Generation.swift`，本轮原目标是消灭这类 guard。建议后续把该 guard 也迁入 policy，让「backend-agnostic policy 接管所有 turn-sync 判决」真正成立（而非 policy + 残留 1 个 Claude-only 守卫）。
- **iOS 52 / 71 用例未独立复跑**：与第二/三轮审计同范围（未复跑 xcodebuild test）。验证依靠测试方法静态存在 + 断言核对 + strict gate 复跑。pre-existing failing test（§5.3）按报告口径接受为维护债。
- **未独立复跑真机构建**：接受 `d93d36a5` commit message 内 owner 真机验收陈述作为间接证据。

---

## 审计判定

**需修正（conditional pass）。** 第四轮 policy/state 重构的技术交付（纯函数 policy + @MainActor state holder + apply 前 4 路复核 + 25 policy 测试 + interleave 测试 + 跨仓文档）经独立复核全部成立，P0 spurious-idle 真机回归也已闭环。但完成报告**不能直接当作闭环**，需先修两项硬问题：

1. **P1（阻塞）**：修复 strict gate 红灯——上调 baseline 到 2371/57（带说明）或把 guard 迁入 policy，然后复跑 exit 0 并回填 §6。
2. **P2**：统一报告 Closed 口径——把 §0.1 下半段 / §1 第 53 行 / §10 标题与正文的「暂不 Closed」按 restoration 后的事实一并改掉（或反向把 §0/§0.1 顶部降回「暂不 Closed」），消除同文档两套 verdict。

并建议顺带处理：

3. **P3**：§9 补 `d93d36a5` + `96d6406`/`f35e65f` commit hash。
4. **L1/L2**：§0.1 已证伪的 prekey framing 加误判标注或删除；exec-plan state 视 owner 闭环判定补 `closure` 段。

P1 修好之前，本审计不建议把第四轮标为 Closed；P1+P2 修好之后，专项可正式闭环，且未来不派生第五轮（按 brief §8 / 报告 §8 口径，剩余大文件进日常 backlog）。
