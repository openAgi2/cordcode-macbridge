# Codex Remote Goal：直播正文 / workflow 在执行中后半段消失

日期：2026-09-11  
状态：分析与方案（**不改代码**）。第三轮评审后修订：现场卷宗可沿用；Mac `Turns` 到达序已由官方毫秒序确认；前半段消失拆成 C1/C2，取证先抓 `[TurnDetailDedupe]`。不得把该去重写成「本回合已命中」，也不得把 hide 门或 Mac `StartedAt` 排序当第一刀。  
读者：后续接手的 agent。先读截图和 §1–§2，再读 §4.3（优先 `[TurnDetailDedupe]` + 入口 item 的 `blocks`）；不要从 collab 状态映射、wait_threads 叠卡、直接改 `hideDetailProcessRows`、或只改 Mac `StartedAt` 排序开工。  
评审对照（只读，均未改代码）：

- 第一轮：`docs/2026-09-11-codex-remote-goal-live-transcript-vanishes-review.md`
- 第二轮：`docs/2026-09-11-codex-remote-goal-live-transcript-vanishes-review-r2.md`
- 第三轮：`docs/2026-09-11-codex-remote-goal-live-transcript-vanishes-review-r3.md`

## 0. 来源清单（本分析冻结；第三轮复核）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=e61a57f45727adf5b4eff66ae9fbbe166258b0d1
未提交状态=工作树含 Goal/workflow 折叠、AgentStatus tagged union、workflow itemId 等未提交修复（见 CHANGELOG Unreleased）
任务预期分支=plan/approval-layer
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-plan-approval
配套分支=plan/approval-layer-ios
配套提交=aac039587fec1e65eb5d1a8d04eedda801d618fd
配套未提交=CHANGELOG.md、ChatUIKitContainerView.swift、SessionProjectionMapping.swift、SlashCommandPanelTests.swift
预期产品特性=codex-remote 原生 Goal + 子代理过程 + 与官方 Desktop 同形的回合时间线
上游 Codex=/Users/jacklee/Projects/codex  commit=9e868bd9dc007c05e84a98e0b1f4e31dc98c5e6a（只读；工作树另有未提交补丁，分析未使用）
运行时=/Applications/CordCodeLink.app runtime 0.1.0 commit e61a57f45727 built 2026-09-11T09:03:39Z PID 85072
第二轮对照物=iOS app 内 bundle OpenCodeiOS/Resources/MessageWeb/assets/index-VTD2FSM_.js（2026-09-07 02:29，与仓库 message-web/dist 同文件）；go-bridge.log relay seq（同一连接全局递增）
第三轮对照物=官方 rollout 毫秒序（thread_goal_updated 09:09:23.150 / task_started 09:09:23.216）；iOS `ChatTimelineAdapter.makeItems` last-wins 去重 + `[TurnDetailDedupe]` NSLog
截图核验=本分析直接读取 docs/assets/2026-09-11-codex-goal-live/{01,02,03}-*.jpg（多模态读图，不经 subagent）
```

**截图当时的 runtime 不含工作树里那批未提交修复**（wait_threads 按回合折叠、workflow 卡 itemId、AgentStatus tagged union）。§6 验收必须用含这些修复的 runtime，否则分不清旧叠卡 / `unsupported_item_type` 与本题。

官方会话（owner 真机 Goal）：

- 父线程 `01a08f11-5d9b-73b1-acd8-93053904f69a`
- 本回合 `01a08fba-8e57-7162-ac9c-ce46f642b24a`
- 本地 rollout：`~/.codex/sessions/2026/09/11/rollout-2026-09-11T14-04-35-01a08f11-5d9b-73b1-acd8-93053904f69a.jsonl`（第一轮逐条打印 584 行）
- 运行日志：`~/Library/Application Support/CordCode Link/logs/go-bridge.log`（覆盖 17:04–17:32，含 17:09–17:13 现场）
- 目标文本：创作灭霸 / 乌木喉 / 黑矮星 / 星云四人故事，写入 `/tmp/demo-plan3335635121.txt`

## 0.1 第二轮修订与不采纳

上一轮指出的归因问题，修订稿已改对（hide 门不得当主因、phase 走 `hasExplicitPresentation`、命令卡不是发晚了、`interruptRunningWorkflowParts` 降为次要、四项现场补充）。本轮只改「被写成已确认、其实未证实」的两处。

**采纳（写入正文）**

- R2-1：图 2 顶部行是「用时 1 分 33 秒 ›」，只能是未展开的 `detailEntry`。带该入口的 item 上门 `hideDetailProcessRows === true`。同屏展开态 ProcessGroup 不可能出自**同一个** item。领先假设改为：官方一回合在 iOS 被拆成多个 item，承载前半段的 item 整体不在屏幕上。
- R2-2：`session_goal_record seq=3` 早于模型回合全部事件（`turn_completed seq=128`）；`turn_started` 是 persist-only、不发布 shell。Mac `Turns` 应为 `[cmd, assistant]`。屏幕却是命令卡在模型内容之后，排序层在 wire 之后的概率高于 Mac append 序。
- R2-3 细节：图 2 入口行写实、时长双口径、§4.3 补 5–7 项。

**不采纳 / 降级（标明理由）**

| 条目 | 处理 | 理由 |
| --- | --- | --- |
| 评审把 `detailExpanded = detailHasBody && detailDisclosureExpanded === false` 写成公式 | **不照抄公式** | 源码是 `detailHasBody && detailDisclosureExpanded`（`SplitAssistantTurnRows.tsx:509`）。disclosure 为 false 时结果同为 false，结论保留，公式不抄。 |
| 「前半段 item 整体缺失」写成已确认根因 | **只升为领先假设** | 评审原文是「很可能」。没有 items 边界抓取就不能结案；修法仍按 §4.3 三支选层。 |
| 删掉 §5.1 投影层排序 | **不删，降为候选** | 评审也要求保留为候选。若 Mac `Turns` 已是 `[cmd, assistant]`，按 `StartedAt` 排序对数组无可见位移，实现前必须先定排序层。 |
| 「iOS replica 漏掉 seq=3、再把命令卡尾部 append」写成已证机制 | **不升格** | 这是 R2-2 之后的一种具体假说（`SessionProjection.upsertingTurns` 对未知 `turnId` 尾部 append）。日志只证明 Mac 事件序，不能证明当时 iOS replica 的 `Turns` 序。列入取证第 7 项。 |
| 把 hide 门改回「整屏必为 false」或改回主因 | **继续废弃** | R2-1 证明的是「带入口行的那个 item 上门为 true」，不是「整屏没有门」，更不是「单 item 内被门藏掉前半段」。 |
| 把「chevron `›` 本分析已看清」写成自己的读图结论 | **不升格** | 本分析直接读出「用时 1分 33秒」被会话标题栏挡住；chevron 落入与 Chat 副标题的重叠区，未单独看清。时长文案不是「收起」，已足够判定未展开。`›` 按未展开态从源码推断，**不是**像素读数（评审第三轮更正：第二轮 6 倍读出的是时长文案）。 |

已排除（第二轮查过，沿用）：app 内 bundle 落后于仓库 src；图 2 是原生 `ChatTimelineAdapterUIKit` 降级渲染；图 2 走整 turn（`AssistantTurn`）路径。依据：bundle 含 `turn-detail-answer-divider` / `无详细过程` / `收起过程` / `turn-detail-entry`，hide 门与 src 逐字一致；Swift 没有「用时 X 分 Y 秒」也没有「收起过程」；整 turn 路径不渲染 `TurnDetailEntry`，而 `turnDetailID` 存在时 `shouldSplitTurn` 强制 split（`renderRowStore.ts:55`）。

## 0.2 第三轮修订与不采纳

第三轮核验：上一轮新增锚点全部为真；§0.1 不采纳表逐条成立（含「不照抄 `detailExpanded` 公式」——评审确认第二轮那句措辞有误，源码是 `detailHasBody && detailDisclosureExpanded`，结论不受影响）。

**采纳（写入正文）**

- 官方毫秒序：`thread_goal_updated 09:09:23.150` 比本回合 `task_started 09:09:23.216` 早 66ms ⇒ 命令回合先进入 `projection.Turns`。限定：`turn_started` 虽不发布 patch，仍经 `upsertTurnPersistOnly` 进数组；**数组序 = 到达序，不是发布序**。relay seq 看不到 persist-only，不能拿来推数组序。
- R3-1：`ChatTimelineAdapter.makeItems`（`ChatTimelineAdapterUIKit.swift:35–96`，跳过 `:137–141`）对同一 `turnDetail.turnID` last-wins，非末条 `continue` 不生成 item；自带 `[TurnDetailDedupe]` 日志。注释点名根因窗口是 live reducer / recovery pull / 投影 changeset 三源并存。
- 领先假设拆成 **C1 / C2**，判别字段是带入口 item 的 `blocks` 里还有没有 intro / workflow。
- 「门解释不了图 2」收窄为：门解释不了**过程组**，但可以解释前半段在带入口 item 上不可见。
- 已排除：iOS `runCodexGoalCommand` 不做本地乐观插入（纯 RPC）。

**不采纳 / 降级（标明理由）**

| 条目 | 处理 | 理由 |
| --- | --- | --- |
| 把 `TurnDetailDedupe` 写成「本回合已命中」 | **只升为 C1 首选机制** | 现场没有这条 NSLog。规则能产出图 2 那种「前半段整组不在、入口不堆叠、只留尾部」，是否 fired 要抓日志。且只丢**已盖同一 `turnDetail.turnID`** 的非末条；未盖戳的 live group 不在去重范围内。 |
| 把 C1 或 C2 单独写成结案 | **并列，用 `blocks` 判别** | 图 2 形状两支都能解释。没有 kept/dropped + 入口 item 的 `blocks` 不能选。 |
| 因 C2「门是原因之一」而先改 `hideDetailProcessRows` | **护栏保留** | C2 成立时门确实参与不可见，仍禁止无条件豁免（§11.7 全局契约）。先抓日志和 `blocks`。 |

## 1. Owner 问题（原话 + 截图）

Owner 2026-09-11 17:09–17:13 真机（iPhone，Codex Desktop 模式）走查：

> 这次发送 goal 任务后，先流式输出回复和 workflow 卡片，然后显示 goal 任务具体指令，如截图 1，然后执行一半之后，前面的流式输出和 workflow 消失，执行完毕之后只显示这些（截图 2），大量的思考执行过程和流式输出直接消失。这是同一个任务在 Mac 端 Codex 的样子（截图 3）。而且这个问题很多轮都修不好。这次不要改代码了，只是分析，把问题分析和解决方案保存成文档。

截图已归档（相对本文件）：

| 编号 | 文件 | 时刻 | 看到什么 |
| --- | --- | --- | --- |
| 1 | [assets/2026-09-11-codex-goal-live/01-ios-live-mid-turn.jpg](assets/2026-09-11-codex-goal-live/01-ios-live-mid-turn.jpg) | iOS 17:09 | 自上而下：介绍正文（首句重复「**我会调用两个我会调用两个**智能体…」）→「已执行 2 个工具」→「正在处理任务」→ `Subagents · 2 个成员`（未分阶段，Agent-1/2 均运行中）→ **右对齐** `/goal …` 命令气泡 → `goal · 执行中…` → `正在执行 codex_app:wait_threads`（约 1m 32s）。无「用时」入口（live 回合）。官方与图 3 介绍都只有一份，重复是独立缺陷 |
| 2 | [assets/2026-09-11-codex-goal-live/02-ios-after-complete.jpg](assets/2026-09-11-codex-goal-live/02-ios-after-complete.jpg) | iOS 17:13 | 自上而下（本分析直接读图）：「**用时 1分 33秒**」被**会话标题栏**（「讲一个程序员笑话」）挡住，不是状态栏；时长文案可读。chevron 落入与 Chat 副标题的重叠区，**未像素确认**；按未展开 `detailEntry` 源码推断为 `›`（展开会是「收起」+ `⌄`）→ 终答两段（「已完成并写入：demo-plan3335635121.txt」+ 四篇字数）→ **右对齐** `/goal` 命令气泡 → `goal · 已完成` → 展开的过程组「**收起过程**」→「思考了 53 秒」→「已编辑文件运行了命令」→「1 个文件已更改」。缺失：介绍正文、Subagents/workflow、`send_message_to_thread` ×2、`wait_threads`。**命令卡夹在终答和过程组之间** |
| 3 | [assets/2026-09-11-codex-goal-live/03-codex-desktop-same-turn.jpg](assets/2026-09-11-codex-goal-live/03-codex-desktop-same-turn.jpg) | 官方 Codex Desktop 同一任务 | 用户气泡是目标正文 + 「设为目标」（不是 CordCode 命令卡）→「用时 1分钟 30秒」→ 介绍正文**一份**且还在 → 过程摘要「编辑了文件运行了命令已向聊天发送消息wait threads」及展开步骤：「已将消息发送到聊天」×2、Wait threads、已创建 txt、ruby 校验 → 终答 → 文件卡 → 「已在 1m 29s 内达成目标」。过程没有被抽走 |

会话相对原件（仅本机 Grok session，交接后可能失效）：

- `.../assets/image-399e75d8-f0a7-47b6-bd36-28bc80cc595f.jpg` → 图 1
- `.../assets/image-ee59468b-54b2-4d5a-ad53-42bb9105f7e2.jpg` → 图 2
- `.../assets/image-5cd36522-2c39-4b88-8279-ade69ef5860a.jpg` → 图 3

图 2 的屏幕顺序本身是选路证据（本分析直接读图，上→下）：

```text
用时 1分 33秒          ← detailEntry，被会话标题栏挡住
终答两段                ← final narrative
/goal 命令气泡          ← system 命令卡（右对齐）
goal · 已完成
收起过程 + 思考 53s     ← 展开态 ProcessGroup
已编辑文件运行了命令
1 个文件已更改

可见：未展开的 detailEntry、final 正文、命令卡、展开的 ProcessGroup、文件卡
缺失：介绍正文（commentary）、send_message_to_thread ×2、wait_threads / Subagents
硬矛盾 1：未展开入口行 与 展开态 ProcessGroup 同屏 → 不可能属于同一个 item
硬矛盾 2：system 命令卡夹在终答和过程组之间
          → 也不可能是「同一个 split item 的连续行」
            （renderRows 只重排同一 item 内的行，插不进另一个 item）
```

缺的是**回合前半段**，留下的是**终答 + 命令卡 + 另一份过程组**。门解释不了**过程组**（带入口的 item 上门为 true 时，它自己的 ProcessGroup 应 `content = null`，但图 2 有展开过程组，所以过程组来自别的 item）；门**可以**解释前半段在带入口 item 上不可见（C2）。命令卡夹在中间，进一步排除「整段都是一个 assistant item 的内部行」。

## 2. 官方真值（必须对照，禁止凭 iOS 终态倒推）

同一回合 `01a08fba-8e57-7162-ac9c-ce46f642b24a`，rollout `item_completed`（UTC；第一轮逐条核验完全一致）：

| 时间 | 官方条目 | 内容 |
| --- | --- | --- |
| 09:09:23.150 | `thread_goal_updated` | Goal 生效（命令卡来源）；比下面 `task_started` **早 66ms** |
| 09:09:23.216 | `task_started` | 本回合 `01a08fba-…` 开始（`upsertTurnPersistOnly` 进 `Turns`，不发 patch） |
| 09:09:28 | `AgentMessage` #1 | `phase: commentary`。介绍：会调用两个智能体写四篇故事并写入 txt |
| 09:09:32 | `McpToolCall` `send_message_to_thread` ×2 | 向两个子线程发任务（图 3「已将消息发送到聊天」） |
| 09:09:56 | `McpToolCall` `wait_threads` | **item 记录于** 09:09:56，实际等待至约 09:10:46 |
| 09:10:46 | `FileChange` | 创建 `demo-plan3335635121.txt` |
| 09:10:50 | `CommandExecution` | ruby / rg 校验字数 |
| 09:10:55 | `AgentMessage` #2 | `phase: final_answer`。终答「已完成并写入…」 |
| 09:10:55 | `task_complete` | 回合结束 |

时长三个口径都写，不要混用：官方 09:09:23→09:10:55 ≈ 92–93s；iOS 入口行 **1 分 33 秒**；官方 Desktop 显示 1 分 30 秒 / 「1m29s 内达成目标」。数秒偏差本身不是本题。

对账：官方 2 条 assistant 正文 + 3 次 MCP + 1 次文件 + 1 次命令 + 5 次 reasoning。图 3 几乎全在。图 2 只剩终答 + 思考摘要 + 文件组。

本回合 **没有** `collabAgentToolCall`。子代理过程来自 `codex_app` MCP（`send_message_to_thread` / `wait_threads`）。

官方这一轮的 user input **不是**用户手打的任务原文，而是 Codex 设目标后自己发起的 continuation：

```text
<codex_internal_context source="goal">
Continue working toward the active thread goal.
…<objective>创作灭霸乌木喉黑矮星星云四人各自故事…</objective>
```

图 3 把目标呈现在用户气泡上并标「设为目标」。CordCode 另做一张 `/goal` 命令卡。这是形态差异的官方依据，不改变「命令卡应排在模型回合之前」的产品结论。

已排除：`MAX_INLINE_PROCESS_BLOCKS = 80`（`AssistantTurn.tsx:106`）。本回合远未触及，图 2 也没有「查看更早的 N 个执行步骤」。

已排除：iOS 发送 `/goal` **不做本地乐观插入**。`runCodexGoalCommand`（`ChatUIKitContainerView.swift:6983–7034`）是纯 RPC，成功只提示「已提交 /goal，状态以官方读回为准」，不建本地 turn/message。不必再查「本地乐观 item 抢位」。

## 3. 前几轮为什么修不好（后来者不要重走）

| 轮次 | 修了什么 | 覆盖的现象 | 为什么盖不住本题 |
| --- | --- | --- | --- |
| P5.7 / collab 折叠 | `collabAgentToolCall` 按 turn 折成一张卡 | 每个 spawn/wait/close 新卡 | 本回合 0 条 collab；走的是 MCP |
| 状态 tagged union | `{"completed": "正文"}` / `shutdown` | 成员一直「执行中」、后台任务「已取消」 | 本题是**前半段从时间线消失**，不是成员词表 |
| wait_threads runId | 多次 wait 共用 `codex-collab:<turnId>` | 图 1 已只有一张 Subagents 卡 | 叠卡已改善；消失发生在**之后**。且截图 runtime **未包含**该未提交修复 |

本题不是再叠一层 collab 状态映射。**不要改 collab 状态表。**

## 4. 已确认的现象 vs 未证实的机制

两件已经能单独确认的**现象**，和两件必须取证才能选层的**机制**，不要混在一节里当「主因」。

### 4.1 现象已确认：命令卡排在模型内容后面；排序层待取证

图 1：介绍和 Subagents 在上，右对齐 `/goal` 命令气泡在下，再下面才是 `wait_threads`。  
图 2（直接读图）：终答 → `/goal` 命令气泡 → `goal · 已完成` → 展开过程组。命令卡不只是「整段模型内容之后」，而是**夹在终答和过程组之间**。

**已证实、故不要改发送链路：**

- 官方 rollout 毫秒序（比 relay seq 更硬）：`thread_goal_updated 09:09:23.150` → 本回合 `task_started 09:09:23.216`（早 **66ms**）
- `go-bridge.log`：`17:09:23.568 event=session_goal_record seq=3` 已转发
- 官方首条介绍 `AgentMessage` 在 09:09:28（本地 17:09:28）
- `relayEvents forwarding` 的 `seq` 是同一连接内全局递增（本连接 1,2,3,…,128 连续；换连接后回到 1，见 17:19:49 的 `seq=1`）
- `turn_completed seq=128 @ 17:10:56.224` ⇒ **发布到客户端的**模型回合事件全部晚于命令卡事件

**数组序 ≠ 发布序。** 第二轮用 relay seq 推 `Turns` 数组序，栽在这里，不得沿用：

- `turn_started` 走 `upsertTurnPersistOnly`（`projection_reducer.go:363–395`，`:911–929`），**不 `commit`、不发布 shell**（`:1336–1341`）。但它仍会在 `:394` **append 进 `projection.Turns`**。
- 因此 **Mac 数组序由事件到达序决定**：goal 记录先到（09:09:23.150）→ 命令回合先 append；66ms 后 `task_started` persist-only 再 append assistant ⇒ Mac `Turns` = **`[cmd, assistant]`**。这与「模型回合先插入」相反，也与「用 relay seq 看发布序」不是同一件事。
- assistant **内容**仍是首个内容帧（约 17:09:28）才发布给客户端；那只影响客户端何时看见正文，不改变 Mac 数组里已经先有 cmd 的事实。

Codex 侧命令卡不是 `EventSessionCommand` 产的（该事件只有 dsh-web / grokbuild 发）；是 reducer 从 Goal 快照合成：`projection_reducer.go:1205–1237`，`commandID = "codex-goal:<createdAt>"`，`CommandLine = "/goal " + objective`。`StartedAt = goal.CreatedAt*1000`（`upsertSessionCommandTurn` `:340–346`），命令回合时间戳也早于模型内容。

⇒ 屏幕顺序（卡在模型内容之后）**不可能只由 Mac `Turns` 数组顺序解释**。排序/分组发生在哪一层未定，候选在 wire 之后：

- iOS `SessionProjection.upsertingTurns`（`SessionProjection.swift:680–689`）对未知 `turnId` **尾部 append**。若 replica 先落到 assistant、后落到 cmd，iOS `Turns` 会变成 `[assistant, cmd]`，即使 Mac 是 `[cmd, assistant]`。这是假说，不是已证。
- `SessionProjectionMapping.messages(from:)` 按 `projection.turns` 原序展开（`:20–36`）：一回合 → user + assistant + system。
- `ChatTimelineAdapterUIKit.groupedMessages`（`:539–668`）把连续 assistant 合并，但 **system 会切断 assistant run**（流式增量路径 `:416–422` 已为此修过 dsh 命令卡消失）；`groups.last?.role == .assistant` 才允许合并。命令卡插在两个 assistant group 之间，会把官方一回合拆成多个 item。
- MessageWeb 行序（`renderRows.ts` 的 `detailEntry orderKey = '-001'`）只重排**同一 item 内**的行，不能把另一个 item 的命令卡插进终答和过程组之间。

官方心智模型是「先设目标 → Codex 自起 continuation turn」，不是 CordCode 四拍里的「人发一条 `/goal` 再开模型回合」。产品仍要求：CordCode 命令卡必须稳定排在该 continuation 回合**之前**，且不随后续 goal 快照位移。

修法见 §5.1：**先定排序层再改**；不要去改发送链路；不要默认改 Mac `StartedAt` 排序就能看见变化。

### 4.2 未证实：前半段消失——入口行把「单 item 被门藏」推翻了一半

**原分析把 `hideDetailProcessRows` 当图 1→图 2 主因，第一轮已否。第二轮进一步：连「该门在截图那一刻必为 false」也不成立。**

图 2 **同时**有（直接读图，顺序如下）：

1. 被会话标题栏挡住的「用时 1分 33秒」（时长文案可读；chevron 在与 Chat 副标题重叠区，未像素确认，按未展开 `detailEntry` 源码推断为 `›`）
2. 终答两段
3. 右对齐 `/goal` 命令气泡 + `goal · 已完成`
4. 展开态 ProcessGroup（「收起过程」「思考了 53 秒」「已编辑文件运行了命令」）

二者的源码身份：

| 屏幕文案 | 唯一来源 | 含义 |
| --- | --- | --- |
| 「用时 … ›」 | `TurnDetailEntry.tsx` `durationLabel`（`:42–46`）→ `turnDetailEntryView`；app 内 bundle 里「用时」只出现这一次。Swift 仅有 goal 弹窗「用时：」（全角冒号，`ChatUIKitContainerView.swift:7129`），对不上 | 该 **item** `turnDetailID ≠ null` 且 `phase === 'finalized'`（`renderRows.ts:446`；`turnDetailEntryView` 也要求这两项，否则 `kind: 'hidden'`） |
| 时长而不是「收起」 | `TurnDetailEntry` 展开时标签换成「收起」（`:157/:179/:210/:232`），chevron `⌄`；未展开才是时长 + `›` | 该 item 的 `detailDisclosureExpanded === false` |
| 「收起过程」 | 只来自 `ProcessGroup.tsx:1789` 展开态标签，不是 `TurnDetailEntry` | 某个 item 的 process 组处于展开态 |

带入口行的那个 item：

```text
detailExpanded = detailHasBody && detailDisclosureExpanded     // :509，disclosure 为 false → false
detailBodyVisible = false
hideDetailProcessRows = turnDetailID && finalized && detailHasBody && !detailBodyVisible
                      = true
→ 该 item 的 processBatch / reasoning / workflowRun / 非 final narrative 一律 content = null
  （SplitAssistantTurnRows.tsx:518–537）
```

上一轮的三种逃生口（`turnDetailID` 空 / phase 非 finalized / `detailBodyVisible === true`）对**这个带入口的 item**全部不成立。第一轮写「门必为 false」是错的。

但门为 true 时，**这个 item 自己的** ProcessGroup 不应出现。图 2 又明明有展开态 ProcessGroup。

⇒ **入口行与过程组不可能同属一个 item。** 这是截图结构给出的硬约束。收窄：门解释不了**过程组**，但**可以**解释前半段在带入口 item 上不可见。

C1 / C2 并列（未取证，不得单支结案）：

| 支 | 含义 | 图 2 怎么对上 |
| --- | --- | --- |
| **C1** | 承载前半段的 group **整体不生成 item** | 介绍 / send / wait / Subagents 整段不在 |
| **C2** | 承载前半段的 item **在列表里**，被 hide 门只留终答；展开的 ProcessGroup 来自**另一个未受门约束的 item**（无 `turnDetailID`，或已展开） | 带入口 item 只见终答；过程组另起 |

判别只需一个字段：**带入口的那个 item，它的 `blocks` 里还有没有 intro / workflow**。

- 有（只是没画）⇒ C2。此时前半段不可见 = **门 + 多 item 拆分**共同作用，门是原因之一。假设 A 描述的是这一支的渲染面，不要和 C2 当成互斥分支。
- 没有 ⇒ C1。此时前半段是真的丢了。

C1 的现成机制（本回合是否命中未证，取证先抓日志）：

`ChatTimelineAdapter.makeItems`（`ChatTimelineAdapterUIKit.swift:35–96`，跳过 `:137–141`）——同一 `turnDetail.turnID` 出现多个 assistant group（幽灵）时，**只保留最后一个**生成 presentation item，其余 `continue`。注释原文：

> live reducer / recovery pull / 投影 changeset 三源并存的窗口里，同一 turn 可能残留多条 assistant messages……同 turnID 多 group 必有幽灵——last-wins 只保留最后一条（投影权威形态最后落地），其余跳过。

与图 2 形状逐条可对（不是已证命中）：前半段整段不在、入口行只有一条不堆叠、只留尾部。自带诊断：

```text
[TurnDetailDedupe] ghost turn=… kept(id=… msgs=… durationMs=…) dropped=id=… msgs=… content=Nc steps=N parts=N durationMs=…
```

限定：这条规则只丢**已盖同一 `turnDetail.turnID`** 的非末条。`turnDetail` 只盖在 completed 回合（`SessionProjectionMapping` §2.2）；未盖戳的 live group 不在去重范围内。没有这条 NSLog，不能写「本回合已被去重丢掉」。

`messagesForTurn` 每个投影回合只产出一条 assistant `Message`（`SessionProjectionMapping.swift:60–104`）。同 turnID 多 group 必有幽灵，根在三源并存，不是 Mac 把一回合拆成两条 assistant。其余仍可能参与切分、但优先级低于去重规则：

- `groupedMessages` 的 settled 切分（`:589–600`）：上一个 assistant group 已有 `turnCompletedAt` 且新消息 id 不同 → 不合并。continuation 回合常常没有 user 行，这条路径会被走到。
- 流式增量分组（`:392–460`）：system 命令卡插在 assistant 之间会切断 run，官方一回合变成「assistant + system + assistant」。
- snapshot `removedItemIDs`：旧 live group 被删、新 finalized group 只带尾部。

Mac 投影收口也没有截断 `Parts`（`upsertTurn` 只 append）。`interruptRunningWorkflowParts`（`:644–673`）即使命中也只是 running → interrupted，**不删除 part**，解释不了「卡片不见」。本回合 wait 在 FileChange（09:10:46）前已返回，`task_complete` 在 09:10:55，正常情况下 run 应已收口；该函数是否命中未取证，降为次要项。

`classifyProjectionTextPresentation`（`:829–863`）也不是「按位置把介绍降成可丢弃 progress」：

- 官方两条 AgentMessage **带 phase**：介绍 `commentary`，终答 `final_answer`（rollout 直接读出）。
- 冷历史：`agent/codex-remote/history.go:451–461`，`commentary → progress`、`final_answer → final`。
- 直播：`projection_reducer.go:1300–1331` 读 `presentation` 并置 `presentationExplicit = true`。
- 真正走的是 **`hasExplicitPresentation` 分支**（`:843–853`）：官方 phase 权威，只给未标注 text 补中性 progress，**绝不按数组位置把 commentary 提升为 final**。

症状（介绍是 progress、终答是 final）碰巧与位置推断一致，但机制归因错了。把介绍升成 final 会违背官方 phase，并污染 `finalAnswerItemIDs`。分类不是本题。

### 4.3 前置门槛（动手前必须做，不可跳过）

图 1 是 17:09（wait item 刚落下、介绍和卡都在）。图 2 是 17:13（`task_complete` 09:10:55 之后）。Owner 说的「执行一半之后」与 wait 结束 → 回合收口同一时间窗。**消失发生在哪一层、该回合在 iOS 是几个 item，均未证明。**

最小取证清单（**先抓 `[TurnDetailDedupe]`**，不必先手写埋点；一次复现就能定 C1/C2）：

1. 同一回合 `turn_completed` **前**最后一条 projection patch，以及收口后完整快照：记录 `assistant.parts[]` 的**类型序列**（text / reasoning / tool / workflow / …）和每条 text 的 `presentation`。
2. iOS 同一时刻每个相关 item 的 `WebTimelineItem.assistant.blocks` **kind 序列**，以及 `turnDetailID` / `turnDetailLoadState` / `phase` 三元组。
3. 每个 item 走的是 split 还是 whole，web 还是 native。
4. 若走 lazy detail：`session_turn_items` 返回与 `classifyDetailParts` 是否拒收（上一轮同会话报过 `unsupported_item_type`）。
5. **（最高优先）** 复现同回合，抓 `[TurnDetailDedupe]`（`ChatTimelineAdapterUIKit.swift:77–91`）：kept / dropped 两个 group 的 `id`、`messageIDs`、`content` 长度、`steps` 数、`parts` 数、`durationMs`。同时逐 item 记录：`id` / `groupID` / `role` / `phase` / `turnDetailID` / `turnDetailLoadState` / `blocks` kind 序列。必须能指出「用时」入口属于哪一个、`ProcessGroup` 属于哪一个、命令卡属于哪一个。
6. 看**带入口 item 的 `blocks` 里有没有 intro / workflow**：有 ⇒ C2；没有 ⇒ C1。若 C1 且日志有 dropped：确认被丢 group 的 content 是否已被 kept 覆盖。若无 `[TurnDetailDedupe]`：去重未跑，再查 settled 切分 / system 切断 / `removedItemIDs` / wire 缺 turn。
7. 顺序三对照：Mac `Turns`（本侧已由官方毫秒序确认为 `[cmd, assistant]`）vs iOS replica `turns` vs iOS items vs 屏幕。若 iOS/屏幕不是 `[cmd, assistant…]`，排序层在 replica/分组，不在 Mac reducer。

不必再查：iOS goal 本地乐观插入（已排除）。

判定（拿到 5–7 再选，不要先改门）：

```text
有 [TurnDetailDedupe]，dropped 的 content/steps/parts 含 intro 或 workflow
    → C1：去重把前半段整组丢了。修 last-wins（丢弃前必须确认 kept 已覆盖）

无去重日志，带入口 item 的 blocks 仍有 intro/workflow，只是没画
    → C2（= 假设 A 的渲染面）：门 + 多 item。过程组在另一个未受门的 item 上

带入口 item 的 blocks 没有 intro/workflow，也无去重日志
    → 假设 B / B'：Mac 投影或 iOS 映射丢块；或 settled / removedItemIDs

iOS 该回合只有 1 个 assistant item，且同时含「用时」入口 + 展开 ProcessGroup
    → 与硬约束矛盾；截图/bundle 前提要重核
      （已排除 bundle 落后 / 原生降级 / 整 turn 路径）
```

在拿到这组序列之前，禁止改 hide 门、禁止改官方 phase 分类、禁止只改 Mac `StartedAt` 排序并宣称命令卡已修好、禁止用回放/缓存压症状。

## 5. 方案（取证之后才选分支）

产品对照仍是图 3：同一回合一条时间线，介绍、发消息、wait、写文件、终答留在原位。CordCode 可以多一张 `/goal` 命令卡，但必须排在模型回合之前，且**已经直播过的块不得在收口后从主时间线消失**。

### 5.1 命令卡排序（可与消失取证并行，但先定层）

保留「命令卡必须先于模型回合」。**不要改发送链路**（事件已经先到）。

候选，按取证第 7 项结果选一：

- 若 iOS replica 的 `Turns` 已是 `[assistant, cmd]`：查为何漏掉或延后应用 seq=3 的命令卡 upsert；修 replica/patch 应用顺序，而不是 Mac 数组排序。
- 若 iOS `Turns` 已是 `[cmd, assistant]` 但 items/屏幕不是：修 `groupedMessages` / snapshot item 顺序（system 切断 assistant run、增量 tail 重建）。
- 若连 Mac `Turns` 都不是 `[cmd, assistant]`：再回到 reducer，按 `StartedAt` 稳定排序或原位插入。**官方毫秒序已确认 Mac 到达序是 `[cmd, assistant]`，这一条基本不必走**；在数组已经是 `[cmd, assistant]` 的前提下，按 `StartedAt` 排序不产生可见变化。

共同约束：

- `commandID = codex-goal:<createdAt>` 整值原位结算，不随 goal 快照更新位移
- 不发明第二种 Goal 弹窗
- 实现时记住：官方这一轮是 continuation turn，不是用户又发了一条普通 userMessage

介绍正文去重（图 1「我会调用两个我会调用两个…」）仍是独立缺陷，可与取证并行。

### 5.2 前半段消失：按 §4.3 的 C1 / C2 修

**C1（去重/整组丢弃；`[TurnDetailDedupe]` 命中时走这条）**

- 硬约束：**last-wins 不能只按「最后落地」**。丢弃前必须确认被丢 group 的内容已被 kept group 覆盖（否则就是现在这种「丢一半」）。这是 C1 的最小修法。
- 先钉 kept/dropped 字段，再改 `makeItems`，不要先改 hide 门。
- 三源并存窗口（live reducer / recovery pull / 投影 changeset）才是「同一 turn 两条 assistant message」的根；settled 切分和 `removedItemIDs` 是次级候选。
- 复制 dsh/Grok：一回合一张时间线，命令卡是独立 system item，但不拆开 assistant 过程。

**C2（= 假设 A 的渲染面：item 在，被门只留终答）**

- 与「假设 A」不是互斥分支：C2 描述结构成因（多 item），A 描述渲染结果（门把带入口 item 的过程行藏掉）。
- 单靠豁免 hide 门填不回 **C1 丢掉的 item**；只有 C2 成立时才考虑有条件豁免。
- 不是「改动面最小的无条件豁免 `workflowRun`」。那是对 `turn_detail_lazy_v1`（§11.7）的全局契约变更：capability 只挂在 `codex-remote`（`core/turn_detail_lazy_gate.go`），会影响该后端**全部**回合，并牵动 `hasDetailBody` / `emptyLoaded`（「无详细过程」）以及 iOS 已绿的 jsdom 基线。
- 若走这条：只对**已经在 live projection 里出现过**的块豁免（`turnDetailHasMaterializedContent` 为真，或块带 live 身份），并按后端/回合类型 gate。
- 懒加载只补从未直播过的过长细节（超大 tool output），不负责把直播过的过程再藏起来。
- 保持无过程块时 `TurnDetailEntry` 仍为 `emptyLoaded`。
- **不要**改 `classifyProjectionTextPresentation` 去把 commentary 升成 final。

**假设 B / B'（parts 或 blocks 里前半段已经没了）**

- 先查 iOS `SessionProjectionMapping` / `MessageWebSnapshotBuilder` 对 MCP tool、commentary narrative、无 itemId 的 workflow part 的处理（workflow `itemId` 正是未提交修复要补的）。
- 再查收口时整值 upsert 与 live 增量是否部分覆盖（CHANGELOG 提过「已发布的 turn shell 后续只发送工具增量」「超阈值不再记录大 patch」）。
- Mac 侧没有 `Parts = Parts[:n]` 截断，不要先假设「投影把前面删了」。
- **不要**用容错/回放掩盖丢块。

**与消失无关、不要当主因去改的**

- `interruptRunningWorkflowParts`：不删除 part，解释不了卡片消失。
- collab 状态表。
- `MAX_INLINE_PROCESS_BLOCKS`。
- 无条件改 `hideDetailProcessRows`。
- 把 commentary 升成 final。
- 只改 Mac `StartedAt` 排序却不核对 iOS 顺序。

### 5.3 实现顺序

0. **先做 §4.3 取证**：复现同回合，抓 `[TurnDetailDedupe]` + 入口 item 的 `blocks`（有无 intro/workflow）⇒ 选 C1 或 C2；再对照 iOS replica `turns` vs items vs 屏幕。  
1. 命令卡稳定排序（§5.1，Mac 侧已确认 `[cmd, assistant]`，先定 iOS 层）与介绍正文去重（图 1 独立缺陷）可以与取证并行。  
2. 按选中的假设写失败测试：真实形状是 2 条带 phase 的 agentMessage + 3 次 MCP + workflow + 终答；C1 断言 dropped group 的内容已被 kept 覆盖（或根本不丢）；C2 断言带入口 item 收口后前半段仍可见。并断言 iOS 不会把一回合拆成「终答 item + 过程 item」而丢掉 intro。  
3. 用本文件三张图走查。  
4. 回归 dsh/Grok Goal：命令卡 + workflow 卡不得被折进 process，也不得被 §11.7 壳误伤（它们本来就不挂 turn_detail_lazy）。

明确不做：不造 Desktop 没有的假卡；不把 MCP 过程做成独立假 turn；不靠轮询/缓存回放掩盖；不把图 2 的「思考了 53 秒」当成过程完整；不动 collab 状态表。

## 6. 验收

必须使用**含工作树未提交修复**的 runtime（wait_threads 回合折叠、workflow itemId 等）。否则无法区分旧叠卡 / `unsupported_item_type` 与本题。截图当时的 runtime 是 `e61a57f` 已安装包，**不含**这些修复。

新开一条 Codex Remote Goal（不要用已经叠过卡的旧气泡）：

1. 发送后**先**看到 `goal · 执行中…`，再看到模型介绍；命令卡不随官方状态更新位移。  
2. 执行中 Subagents 卡原位更新，不往下复制。  
3. wait / 发消息期间，介绍正文和 workflow **不得消失**。  
4. 结束后，介绍正文、Subagents 卡、wait/发消息与终答**同屏可见**（不是「点开才有」）。  
5. `goal · 已完成` 仍在。  
6. 介绍正文**不重复**。  
7. 任一条 FAIL：停。保留该回合投影 snapshot + iOS **逐 item** 的 `phase` / `turnDetailID` / `blocks[]` + `[TurnDetailDedupe]` 日志（有或明确没有）+ `go-bridge.log` 里 `turn_completed` 前后事件（含 seq），再改。

## 7. 相关源码锚点

| 层 | 文件 | 作用 |
| --- | --- | --- |
| 官方回合 | rollout `01a08fba-…` | 真值时间线；`thread_goal_updated 09:09:23.150` 比 `task_started 09:09:23.216` 早 66ms；两条 AgentMessage 分别是 `commentary` / `final_answer` |
| Mac 回合顺序 | `go-bridge/projection_reducer.go:330–334` | `upsertTurn` 只 append；Mac 到达序 = `[cmd, assistant]`；**不能单独解释屏幕序** |
| Mac persist-only | 同文件 `:363–395`（`:394` append）、`:911–929`、`:1336–1341` | `turn_started` 不发布 shell，**仍进 `Turns`**；数组序 = 到达序，不是发布序 |
| Mac 命令卡合成 | 同文件 `:1205–1237`、`:340–346` | Goal 快照 → `codex-goal:<createdAt>` 系统卡；事件早于正文 |
| Mac 文本分类 | 同文件 `classifyProjectionTextPresentation` `:829–863` | **有官方 phase 时走 hasExplicitPresentation，不按位置升 final** |
| Mac workflow 终态 | 同文件 `interruptRunningWorkflowParts` `:644–673` | running→interrupted，**不删除**；未证实命中，解释不了消失 |
| iOS replica 顺序 | `SessionProjection.swift:680–689` | 未知 `turnId` 尾部 append；命令卡若晚到会排到 assistant 之后 |
| iOS 映射 | `SessionProjectionMapping.swift:20–114` | 按 `turns` 原序；每回合至多一条 assistant `Message` |
| iOS 分组切分 | `ChatTimelineAdapterUIKit.swift:392–460`、`:589–600` | system 切断 assistant run；settled 后不同 message id 不合并 |
| iOS 幽灵去重 | 同文件 `makeItems` `:35–96`、跳过 `:137–141`、日志 `:77–91` | 同 `turnDetail.turnID` last-wins；C1 首选机制；抓 `[TurnDetailDedupe]` |
| iOS goal 发送 | `ChatUIKitContainerView.swift:6983–7034` | `runCodexGoalCommand` 纯 RPC，无本地乐观插入 |
| iOS hide 门 | `message-web/.../SplitAssistantTurnRows.tsx:507–537` | **对带入口的 item 为 true**；与同屏 ProcessGroup 不能同 item |
| iOS 入口行 | `message-web/.../TurnDetailEntry.tsx:42–70`、`:157–198` | 「用时 … ›」= 未展开；展开标签是「收起」不是「收起过程」 |
| iOS 入口创建 | `message-web/.../renderRows.ts:446–467` | `turnDetailID && phase === 'finalized'` 才有 `detailEntry`；`orderKey = '-001'` |
| iOS 强制 split | `message-web/.../renderRowStore.ts:44–56` | 有 `turnDetailID` 即 split，整 turn 路径不会出现「用时」 |
| iOS 过程组文案 | `shared-message-renderer/.../ProcessGroup.tsx:1789` | 图 2「收起过程」的唯一来源 |
| iOS 答案身份 | `message-web/.../finalAnswerIdentity.ts` | `finalAnswerItemIDs` 独占分类 |
| iOS workflow 本应独立 | `message-web/.../AssistantTurn.tsx:112–132` | workflow **不应**进 process_group |
| 懒加载门控 | `core/turn_detail_lazy_gate.go:9–11` | 仅 `codex-remote` 广告该 capability |
| dsh 对照 | `agent/dsh-web/workflow_fold.go` | 按 runId 一张卡，冷热同形，无 lazy 壳 |
| Grok 对照 | `agent/grokbuild/goal_workflow.go` | Goal 过程留在同一投影 |

接手 agent：这份文档当现场卷宗用。最短路径：

1. 复现同回合，抓 `[TurnDetailDedupe]`（kept/dropped 的 id / messageIDs / content 长度 / steps / parts / durationMs）+ 逐 item 的 `phase` / `turnDetailID` / `blocks` kind 序列。
2. 看带入口 item 的 `blocks` 里有没有 intro/workflow ⇒ C2（门 + 多 item）还是 C1（整组被丢）。
3. 顺序：抓 iOS replica `turns` vs items vs 屏幕（Mac 侧已知 `[cmd, assistant]`）。
4. 可并行：介绍正文去重（图 1）。
5. 护栏不变：不先改 `hideDetailProcessRows`、不把 commentary 升成 final、不动 collab、不用回放压症状、验收用含工作树未提交修复的 runtime。
