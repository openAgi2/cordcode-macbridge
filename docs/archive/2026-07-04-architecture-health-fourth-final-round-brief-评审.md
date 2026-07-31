# 架构健康第四轮（最终轮）开发简报 评审报告

日期：2026-07-04
评审对象：[docs/2026-07-04-architecture-health-fourth-final-round-development-brief.md](../2026-07-04-architecture-health-fourth-final-round-development-brief.md)
评审方法：基于真实代码、日志、handoff、think.md、CHANGELOG 与历史轮次文档交叉核对，不接受简报自述。

---

## 0. 一句话结论

**有条件放行。** 简报的事实底座（文件/方法/测试/行数）、根因判断（iOS 状态模型缺失）、设计方向（backend-agnostic ownership policy）**全部成立且高质量**，工程纪律（禁止形态、scope 收口、tiered 测试）是四份 brief 里最好的一份。但它有三处必须先纠正再开工：

1. **认知错位**：简报当成"活跃 bug"来写的 07-04 冷启动问题，**当天已被 iOS `e018cb5f` 单点修复并 owner 真机复测通过**。第四轮真实定位是"把 Claude-only 单点 guard 重构+泛化成 policy 的结构性硬化"，不是 bug fix。这会改变完成定义、ROI 论述和完成报告口径。
2. **治理越权**："架构健康专项 closed / 不生成第五轮"是简报单方面声明，无 owner 预授权证据。需 owner 显式签字，或软化为"第四轮关闭 iOS 状态模型 gap；余项进普通 backlog"。
3. **CHANGELOG 自相矛盾风险**：已发布的 07-04 CHANGELOG 条目把冷启动修复归功于 Mac 侧 `relayRunningKind` 拆分，而 think.md 明写该修复"非主因、问题依旧"，真正修好症状的是 iOS 侧。第四轮若再写一条 iOS 状态模型条目，会在同一症状上留下两条互相打架的 CHANGELOG。

修掉这三点后，简报可以作为施工输入。

---

## 1. 事实核对结果

| 简报声明 | 实测 | 判定 |
|---|---|---|
| `BridgeProvider.swift` 1629 行 | 1629 | ✅ |
| `ChatViewModel+Generation.swift` 2308 行 | 2308 | ✅ |
| `ChatViewModel+MessageSync.swift` 约 1200+ 行 | **1480** | ⚠️ 低估约 280 行（19%） |
| `ChatViewModel+CodexStreaming.swift` 1426 行 | 1426 | ✅ |
| `ChatViewModel+SessionManagement.swift` 1021 行 | 1021 | ✅ |
| `ChatUIKitContainerView.swift` 4371 行 | 4371 | ✅ |
| `handlers.go` 3272 / `claudecode.go` 1911 / `appserver_session.go` 1805 | 全中 | ✅ |
| `loadMessages` / `sendMessage` / `recoverAfterSendCompletion` / `startRunningSessionPolling` / `handleCodexLiveEvent` / `switchSession` 全部存在 | 全部存在（行号见下） | ✅ |
| `allowDuringClaudeLocalSend` / `isClaudeCodeLocalSendInProgress` 是简报要新建的概念 | **已存在**（MessageSync:14、Generation:1097/1591） | ⚠️ 简报未点明这是"已落地的单点修复" |
| 7 个已有测试文件 + `StreamingOptimizationTests.swift` | 全部存在 | ✅ |
| iOS test target `CCCodeTests` / scheme `CordCode` / project `OpenCodeiOS/CordCode.xcodeproj` | 全部属实 | ✅ |
| `scripts/hygiene-baseline.json` + `check-architecture-hygiene.sh` 行为 | `CORDCODE_HYGIENE_STRICT` / `CORDCODE_IOS_ROOT` 均支持，计数方式（`wc -l` / `grep -wo 'func'` / `grep -o 'ForTesting'`）与简报建议一致 | ✅ |
| 第三轮动作 1/2/4 完成、3/5 部分完成 | 与第三轮完成情况/审计文档一致 | ✅ |

**事实底座可信度：高。** 唯一硬数字偏差是 MessageSync 行数（1480 vs "1200+"），但因 Phase D 已规定 baseline 以完成后实测值为准，这个低估不影响 gate，只需把简报数字校正。

---

## 2. 根因判断核对（本评审最关键一项）

简报第 0 节核心判断：
> Mac runtime 没有重复 `send_message`，但 iOS 在本地 live stream 中途执行 `loadMessages/get_session_messages`，把权威历史覆盖到本地正在流式增长的 timeline。

**判定：成立。** 三方硬证据互证：

- **日志**（`~/Library/Application Support/CordCode Link/logs/go-bridge.log`，07-04 当天 3945 行）：`send_message` 全量仅 3 次、无同 session 重发；`get_session_messages` 258 次。直接坐实"Mac 没重发、iOS 高频拉历史"。
- **iOS 仓**：`e018cb5f Fix Claude local stream history overwrite`（07-04 17:24）正是修这条路径——`loadMessages` 加入口 guard、`recoverAfterSendCompletion` 显式 `allowDuringClaudeLocalSend: true`、新增两条回归测试。`ChatViewModel+MessageSync.swift:23-28` 当前已落地该 guard。
- **think.md 最终根因段**：明确"主因不在 MacBridge 重复生成，也不在 Claude CLI stdout 中断，而在 iOS 本地 Claude live stream 期间仍执行普通历史同步并覆盖 timeline"，并写明"正确边界不是某个轮询入口跳过历史，而是 iOS 历史同步入口本身必须识别 ownership"——这正是简报主张抽 policy 的直接出处。

**关于"会不会根因其实在 relay/prekey、被错配到 iOS"的质疑：不成立。**

- `bf48525 Signal urgent relay prekey refill` 只动 `relay_prekey.go`，且 handoff-20260704-1645 §3.2 自己写明 owner 走 direct LAN、prekey 只影响 Relay 路径、"对本次症状大概率无关"。
- `7c1d97d Harden Claude cold-start relay handling` 的 diff 包含 think.md，think.md 同时坦白该 commit 顺带保留的 `relayRunningKind`（agent / claude_file）拆分修复"有回归测试保护，但 owner 复测确认问题依旧，因此它不是这次从头重播的主因"。commit 名偏 relay 是因为它打包了一个非主因的 relay 修复，**与根因定性是两回事**。
- 时序也排除错配：真正修好症状的 iOS `e018cb5f`（17:24）早于 `7c1d97d`（17:27:03）和 `bf48525`（17:27:09）。若根因在 relay/prekey，症状不会在 iOS 单点改动后被 owner 复测通过。

**措辞精度小瑕疵**：简报写"已经证明"略强。严格说是"已被定位并通过 iOS 单点修复 + owner 真机复测反证"。工程上等价，但完成报告口径建议用后者，避免把"反证"写成"先验证明"。

---

## 3. 设计方案评审

### 3.1 值得肯定（直接采纳）

- **ownership 四态（`.none/.localSend/.remoteLive/.reconciling`）** 是对现有 ad hoc 条件的正确抽象，与现有 `isClaudeCodeLocalSendInProgress`、`mergeServerMessagesDuringGeneration` vs `replaceMessagesFromServer` 的真实数据流吻合，不是臆想状态机。
- **LoadDecision 五态** 区分 defer / merge / reconcile / reject 颗粒度恰当，尤其 `.deferBecauseLocalLiveTurn` 强调"在网络请求前返回"——抓住了 07-04 bug 的本质（不是 apply 阶段错，是根本不该 fetch）。
- **"Backend-specific input, backend-agnostic policy"（第 3.3 节）** 是全文最重要也最正确的设计原则，直接对抗"再加一个 Claude-only if"的退化。现有 guard 恰恰是 Claude-only，这条原则是它的解药。
- **禁止形态清单（第 4.4 节）** 预先封死了五种错误解法（backend-specific if、timer/sleep 延迟、本地缓存冒充权威、Mac 端重复抑制、一刀切禁 Claude loadMessages、用 UI 文字判断执行态），是极好的防回涨护栏。
- **scope 纪律（第 8 节 + "不生成第五轮"）** 显式列出不做清单，防止最终轮膨胀成无限项目，方向正确（仅"永久关闭"治理越权，见 §4）。

### 3.2 设计层硬伤与风险（按优先级）

**P0 — ownership 的并发/原子性未规定。** 这是单一最大设计风险。07-04 bug 本质是 live stream append 与 async history load 之间的竞争。简报提出的 `decideLoadMessages(...)` 是纯函数、`ownershipBySession` 是 `@MainActor` class 上的可变 dict——但简报没规定：
- 谁在什么时机、原子地 set/clear ownership？
- `loadMessages` 入口读 ownership 与 live event 写 ownership 是否在同一 MainActor hop 内不可分割？
- 如果 ownership 在 loadMessages 已经 fetch 完返回途中才被 set，policy 就形同虚设。

**建议**：在第 4 节增加一条硬约束——ownership 的读（policy 判决）与写（live event / send completion / session switch）必须全部在 `@MainActor` 同步段内完成，不允许 ownership 状态跨 await 悬空；并要求 P0/P1 测试覆盖"loadMessages 已发起但尚未 apply 时 ownership 被翻转为 .localSend"这一交错。不做这层，policy 只是把现有 race 换了个名字。

**P1 — Claude-only guard 的迁移/退场路径未给死线。** 简报第 3.2 节写"可以保留 `allowDuringClaudeLocalSend` 这类参数作为迁移过程中的兼容壳"，但没规定兼容壳何时移除、不移除算不算完成。风险：兼容壳永久存活，"policy 是唯一真值"的目标落空，未来出现第三种 backend 时又长出新的 backend-specific 布尔。

**建议**：把"移除 `isClaudeCodeLocalSendInProgress` 与 `allowDuringClaudeLocalSend` 的所有生产调用点、由 policy 单一接管"列为 Phase B 的硬完成条件（保留测试 fake 可用，生产路径禁用）。若评估后确实改不掉，完成报告必须说明原因，不得静默保留。

**P1 — Codex/OpenCode 的 parity 是断言不是证据。** 简报 P2/验收标准要求"Codex/OpenCode live event 路径不因 Claude 修复被降级"，暗示它们存在同类覆盖风险。但简报没给 Codex `assistantMessageDelta` / OpenCode SSE 是否真有"id 不一致导致 partial 被替换"的证据。如果 Codex/OpenCode 因为服务端 id 与本地 id 体系本来就一致而无此问题，那"backend-agnostic policy"对它们是 over-engineering，P2 测试是在保护不存在的风险。

**建议**：Phase A 先加一条调研产出——Codex/OpenCode 是否真有等价的 timeline 覆盖风险（一行 grep + 看 delta apply 路径即可）。若有，policy 接管；若无，policy 仍统一入口但 Codex/OpenCode 走 `.allowAuthoritativeLoad` 直通，并在完成报告诚实写明"backend-agnostic 入口、Claude 实质受益、Codex/OpenCode 无需行为变更"。

**P2 — 未盘存已有的 session-staleness 机制。** 简报第 3.4 节要求 session switch 用 initializationID 拦截迟到结果，仿佛现状缺失。实测 `ChatViewModel+MessageSync.swift:31` 已有 `isInitializationCurrent(initializationID)` guard、:39 已有 `currentSessionId == sessionId` 二次校验。policy 应基于这些既有机制构建，而非另起炉灶，否则会出现 policy 与既有 guard 双轨。

**建议**：第 4.3 节调用点收敛清单补一条"盘存并复用 `isInitializationCurrent` / `currentSessionId == sessionId` 既有 guard，policy 不得与之重复判决"。

**P2 — `ChatTurnSyncPolicy` 的 trigger 来源缺乏枚举约束。** `LoadTrigger` 是 `Equatable` enum 但简报没列全合法取值。07-04 bug 涉及的 trigger 至少有：用户显式切 session、`startRunningSessionPolling` 周期、todo refresh 顺带、send completion recovery、live event turnCompleted。trigger 枚举不全会让 policy 在某个漏掉的 trigger 上默认放行，恰好复现 07-04。

**建议**：第 3.2 节把 `LoadTrigger` 合法值列全，并要求"任何新增 trigger 入口必须经 policy，CI grep 确保 `loadMessages(` 调用点数量可数且与 policy 接入点一致"。

---

## 4. 范围与"最终轮"治理框架评审

### 4.1 范围取舍本身合理

不拆 `BridgeProvider`（第三轮已 -338 行 + 478 行独立 connector + strict gate 闭环）、不拆 `ChatUIKitContainerView`/`claudecode.go`/`appserver_session.go`（与本次 bug 无因果）、不拆 `handlers.go`（第二轮已 -28%）——这些取舍在第三轮文档与日志证据里有依据，不是偷懒。把第四轮聚焦在"与 07-04 真实事故直接相关的 iOS 状态模型"是正确的优先级排序。

### 4.2 但"closed / 不生成第五轮"是治理越权

两位核对 agent 都未在 GO_BRIDGE_ARCHITECTURE.md / CLAUDE.md / 任何更早 brief 中找到 owner 对"第四轮后关闭专项"的预授权。简报由"将执行它的同一 agent"单方面宣布永久关闭并禁止后续轮次，属于自我授权。后果：

- owner 从未明确同意；未来若冒出新的系统性 gap（例如 Codex app-server 共享模式、Relay mailbox 一致性），agent 会以"专项已 closed"为由绕开系统性整改。
- "完成报告明确宣布 closed"被列为验收标准（第 7、9 节），等于把越权声明焊进交付物。

**建议（二选一）**：

- **推荐**：owner 在本评审上显式签字"同意第四轮为最终轮"，签字后简报的 closed 声明才生效；或
- 把第 0/7/8/9 节的"closed / 不生成第五轮"软化为"第四轮关闭 iOS turn-sync 状态模型 gap；`handlers.go`/`claudecode.go`/`appserver_session.go`/`ChatUIKitContainerView` 作为普通维护债进 backlog，不预先排除未来出现新系统性 gap 时另立专项"。这样既保留 scope 纪律，又不堵死未来。

### 4.3 CHANGELOG 自相矛盾风险（必须在施工前处理）

已发布的 CHANGELOG（`[Unreleased]` 下 07-04 条目）原文：

> 修复 Claude Code 冷启动既有 session 首轮流式重复……首个本地提问可能被 transcript file relay 抢占真实 CLI stdout relay 的问题；现在 `send_message` 会让真实 AgentSession relay 接管……

这与 think.md 自述（`relayRunningKind` 拆分"非主因、owner 复测问题依旧"）和 iOS `e018cb5f`（真正修好症状）**冲突**。CHANGELOG 把功劳记在了非主因的 Mac 修复上。第四轮若按 Phase D 再写一条"iOS turn-sync 状态模型硬化"条目，同一症状会有两条互相对立的 CHANGELOG。

**建议**：Phase D 文档收口步骤增加一条——**修订或补充既有 07-04 CHANGELOG 条目**，把"Mac relay kind 拆分（latent bug，独立修复）"与"iOS loadMessages 覆盖（症状主因，e018cb5f 已修）"分开记录，并交叉引用第四轮 policy 硬化条目。否则公开记录会持续误导未来排查。

---

## 5. 测试计划评审

整体分层（P0 纯 policy → P1 Claude 回归 → P2 Codex/OpenCode → P3 session switch）合理，P1 场景直接复刻 07-04 真机路径并对"第二次正常"做了覆盖（防 ownership 残留永久阻塞 history sync），是高质量用例设计。

四处补强：

1. **P1 必须覆盖 ownership 翻转交错**（呼应 §3.2 P0）：在 fake client 已被调用、history 尚未 apply 的窗口里翻转 ownership，断言 partial 不被替换。当前 P1 步骤 5 只覆盖"不发起 get_session_messages 或不替换 partial"，没显式覆盖"已发起但未 apply"窗口。
2. **P0 测试需冻结 policy 的纯函数性**：明确 `decideLoadMessages` 不得有副作用、不得访问 self，纯输入→LoadDecision。这能让 P0 测试不必构造 ViewModel，大幅降低测试体积。简报未点明这条约束。
3. **现有 `MessageDeduplicationTests` / `ExecutionStateSemanticsTests` 的调整预算**：这些既有测试断言的是当前 ad hoc 语义，policy 接管后会改变行为路径，部分断言可能需要更新。简报把它们列为"必读已有测试"但没提示"可能需要随 policy 调整"。建议在 Phase B/C 完成条件里加一条"列出受影响既有测试及调整说明"。
4. **P4 命令的 simulator 目的地与硬约束一致性**：简报硬约束"不运行 UI tests / simulator automation"，但 P4 命令用 `platform=iOS Simulator,name=iPhone 17 Pro Max` 跑 xcodebuild test。这二者不矛盾（unit test on simulator ≠ UI automation），但措辞上容易被执行 agent 误解。建议补一句"xcodebuild test 跑 unit test target 允许使用 simulator destination，本节禁止的是 UI automation/snapshot 测试，不是 simulator 本身"。

---

## 6. 完成定义的调整建议

简报第 1.2 节 7 条完成定义里，第 3 条"2026-07-04 Claude 冷启动重复从头输出问题有回归测试覆盖"**部分已被 e018cb5f 满足**（`RemoteRunningSessionTests` 已有两条测试）。第 4 条"strict net-growth gate" 也已对 BridgeProvider 落地，第四轮只是新增两条 baseline。建议把完成定义改写为"净新增价值"口径，避免完成报告把既有成果算进第四轮功劳：

- 第 3 条改为"07-04 回归测试在 policy 接管后仍通过，并新增 ownership 并发交错用例"；
- 新增第 8 条"`isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend` 在生产路径被 policy 取代（移除或退化为纯测试 fake）"；
- 新增第 9 条"修订既有 07-04 CHANGELOG 与 think.md 根因口径一致"。

---

## 7. 必须先处理后放行的硬伤清单（汇总）

| # | 硬伤 | 优先级 | 处理动作 |
|---|---|---|---|
| H1 | "已修 bug"被当成"活跃 bug"写，完成定义与 ROI 口径偏 | P0 | 简报第 0 节、第 1.2 节改写为"结构性硬化已单点修复的 07-04 问题" |
| H2 | "closed / 不生成第五轮"治理越权 | P0 | owner 显式签字，或软化为"关闭 iOS gap，不堵死未来专项" |
| H3 | CHANGELOG 与 think.md/iOS commit 根因口径冲突 | P0 | Phase D 增加修订既有 07-04 CHANGELOG 条目步骤 |
| H4 | ownership 并发/原子性未规定 | P0 | 第 4 节增加 MainActor 同步段约束 + P1 增加交错测试 |
| H5 | Claude-only guard 迁移/退场无死线 | P1 | Phase B 完成条件加"生产路径移除该 guard" |
| H6 | Codex/OpenCode parity 无证据 | P1 | Phase A 增加一行调研产出 |
| H7 | MessageSync 行数低估 280 行 | P2 | 简报数字改为 1480（gate baseline 仍以完成后实测为准） |
| H8 | 未盘存既有 `isInitializationCurrent` guard | P2 | 第 4.3 节补一条复用既有 guard |
| H9 | P4 simulator 命令与"禁止 simulator automation"措辞易混 | P2 | 补一句区分 unit test on simulator 与 UI automation |

---

## 8. 放行建议

**有条件放行**。H1–H4 必须在简报定稿前处理（owner 决策 H2、修订简报处理 H1/H3/H4），H5–H9 可作为简报 v2 修订项在 Phase A 启动前一并落实。

简报整体质量高于前三轮：根因扎实、设计方向正确、工程纪律到位、scope 收口自觉。它**不需要重写**，只需把"修活跃 bug"的叙事校正为"硬化已修 bug 的结构性边界"，把"永久关闭专项"改为"经 owner 签字的最终轮"，并在施工前把 CHANGELOG 根因口径与 ownership 并发约束补齐。完成这三件后即可作为第四轮开发 agent 的施工输入。
