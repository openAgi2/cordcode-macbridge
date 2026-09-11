# 评审报告：`docs/2026-09-11-codex-remote-goal-live-transcript-vanishes.md`

评审日期：2026-09-11  
评审性质：**只读评审，未改任何代码**（本轮只产出本报告）  
被评审文档：`docs/2026-09-11-codex-remote-goal-live-transcript-vanishes.md`

对照物：

```text
本仓   /Users/jacklee/Projects/cordcode-macbridge-plan-approval  plan/approval-layer  e61a57f45727adf5b4eff66ae9fbbe166258b0d1
iOS 仓 /Users/jacklee/Projects/cordcode-ios-plan-approval       plan/approval-layer-ios  aac039587fec1e65eb5d1a8d04eedda801d618fd
上游   /Users/jacklee/Projects/codex                            9e868bd9dc007c05e84a98e0b1f4e31dc98c5e6a（只读）
官方真值 ~/.codex/sessions/2026/09/11/rollout-2026-09-11T14-04-35-01a08f11-...jsonl（584 行）
运行日志 ~/Library/Application Support/CordCode Link/logs/go-bridge.log（覆盖 17:04–17:32，含 17:09–17:13 现场）
截图   docs/assets/2026-09-11-codex-goal-live/{01,02,03}-*.jpg（已逐张放大复核）
```

**一句话结论**：文档的现场描述、官方时间线对账、源码锚点基本准确，可以作为交接材料保留；但 **§4.2 给出的"主因"（`hideDetailProcessRows` 把过程整段藏掉）与图 2 自相矛盾**，因此 §5.2 的"主修复"大概率会打空。动手前必须先把 §4.3 里那条取证补齐——它不是"可选验证"，而是选路的前提。

---

## 1. 已核验准确（可直接沿用，不必重查）

| 文档说法 | 核验证据 | 结论 |
| --- | --- | --- |
| 官方条目：2 条 agentMessage + 3 次 MCP + 1 文件 + 1 命令 + 5 次 reasoning；时间戳 09:09:23 / 09:09:28 / 09:09:32×2 / 09:09:56 / 09:10:46 / 09:10:50 / 09:10:55 | rollout `turn_id=01a08fba-…` 逐条打印 | **完全一致**（含 5 次 Reasoning、task_complete 09:10:55） |
| 本回合 0 条 `collabAgentToolCall`，子代理来自 `codex_app` MCP | rollout item 类型只有 Reasoning/AgentMessage/McpToolCall/FileChange/CommandExecution | 一致 |
| `classifyProjectionTextPresentation` 位置与"最后一条 text 标 final" | `go-bridge/projection_reducer.go:829–863` | 位置一致（尾行应为 863，非 862） |
| `interruptRunningWorkflowParts` 存在且 running→interrupted | 同文件 `:644–673` | 一致 |
| `session_command` 是独立 system turn、不碰 `execution.phase` | 同文件 `:1044–1056` 分支注释与实现 | 一致 |
| iOS `hideDetailProcessRows` 位于 `SplitAssistantTurnRows.tsx` 514–537，null 掉非 final narrative / processBatch / reasoning / subagent / userInput / workflowRun | `message-web/src/components/turns/SplitAssistantTurnRows.tsx:507–537` | **逐项一致**（行号也对） |
| `finalAnswerIdentity.ts` 用 `finalAnswerItemIDs` 独占分类 | 同目录 `finalAnswerIdentity.ts:4–24` | 一致 |
| `AssistantTurn.groupAssistantBlocks` 特意不把 `workflow_run` 折进 process_group | `AssistantTurn.tsx:112–132`（注释 119–124、条件 126–132） | 一致 |
| dsh 对照 `agent/dsh-web/workflow_fold.go`：按 runId 一张卡、冷热同源 | 文件头 1–27 行（明确"单一折叠真值，live codec 与 history 冷拉共用"） | 一致 |
| Grok 对照 `agent/grokbuild/goal_workflow.go`：Goal 过程留在同一投影 | 文件头 26–30 行（"folds Grok's incremental goal/subagent notifications into the existing whole-value goal/workflow projection contract used by dsh-web"） | 一致 |
| dsh/Grok 没有"finalized + turnDetailID ⇒ 藏过程"的门 | `core/turn_detail_lazy_gate.go:9–11`：descriptor 侧只有 `agent/codex-remote` 挂该 capability；`go-bridge/main.go:446` 单点开 | 一致 |
| 图 1 / 图 2 / 图 3 看到的内容（含"收起过程""思考了 53 秒""已编辑文件运行了命令""1 个文件已更改"） | 三张原图逐区放大复核 | 描述准确 |

§3 的"前几轮为什么修不好"表格结论（本题不是 collab 状态映射、不是 wait 叠卡）**成立**，最后一句"不要再去改 collab 状态表"应保留。

---

## 2. 必须修正的问题（按严重度）

### P0-1 §4.2 的"主因"与图 2 自相矛盾（最重要）

文档 §4.2 断言：`hideDetailProcessRows` 把"非 final 的介绍正文、workflow 卡和工具整段藏掉"，并称这是图 1 → 图 2 的主因。

但图 2 里**同时出现了展开态的 ProcessGroup 及其子行**，而这条门一旦为真，这些行不可能渲染：

- `message-web/src/components/turns/SplitAssistantTurnRows.tsx:530–537`：`hideDetailProcessRows && !persistentInteractiveRow` 时，`row.kind === 'processBatch'` 直接 `content = null`（`reasoning` 同理）。
- `processBatch` 行的 content 正是 `<ProcessGroupComponent …/>`（同文件 `:369–400`）。
- 图 2 里那行"收起过程"只能来自 `shared-message-renderer/src/components/turns/ProcessGroup.tsx:1789`：`{isExpanded ? '收起过程' : label}`，且 `:1799` `isExpanded && <div className="process-group-blocks">` 才会渲染"思考了 53 秒""已编辑文件运行了命令"这些子行。
- 反证：`TurnDetailEntry.tsx` 的任何分支都不会输出"收起过程"（只有"收起"/"用时 X"/"无详细过程"/失败文案），所以图 2 那行不可能是入口行。

⇒ 截图那一刻 `hideDetailProcessRows === false`（`turnDetailID` 为空，或 phase 非 finalized，或 `detailBodyVisible === true`）。无论哪种，**"门把行藏了"都不是图 2 的机制**。

再看图 2 的可见/缺失集合：

```text
可见：final 正文、goal · 已完成、展开的 ProcessGroup（reasoning + 文件/命令批次）、文件卡
缺失：介绍正文（progress narrative）、send_message_to_thread ×2、wait_threads（Subagents/workflow 卡）
```

若真是 hide 门生效，缺失的应是"所有 processBatch/reasoning + 所有非 final narrative"，而可见的恰恰是 processBatch/reasoning。**缺失集合的形状与门的作用域不一致**：缺的是"回合前半段"，留下的是"回合尾部"。

同时，Mac 侧没有任何"回合收口时截断 parts"的逻辑（`projection_reducer.go` 全文只有 `append`，无 `Parts = Parts[:n]` / 截断），所以也不能简单归给"投影把前面的 parts 删了"——真正的位置还需要取证（见 §4）。

**结论**：§4.2 应降级为"两个互斥假设之一（假设 A：客户端壳）"，并明确它与图 2 的不一致；§5.2 第 2 条"改 hide 豁免"从"主修复"改为"待假设 A 被证实后才执行"。

### P0-2 `classifyProjectionTextPresentation` 的分支描述不成立

文档 §4.2 写："没有官方 phase 时，把所有 text 标成 progress，只有最后一条标 final"，并据此在 §5.2 第 3 条要求"Mac 不要把 Goal 回合里先出现的介绍正文降成可丢弃的 progress"。

实际代码与本回合真值都不支持这个描述：

1. 官方 AgentMessage **带 `phase`**：rollout 里介绍是 `"phase": "commentary"`，终答是 `"phase": "final_answer"`（已从 jsonl 直接读出，非推断）。
2. 冷历史路径按 phase 映射：`agent/codex-remote/history.go:451–461`（`commentary → progress`、`final_answer → final`）。
3. 直播路径也带 phase：`projection_reducer.go:1300–1331` 读 `presentation` 字段并置 `presentationExplicit = true`。
4. 因此真正走的是 `classifyProjectionTextPresentation` 的 **`hasExplicitPresentation` 分支**（`:843–853`），语义是"官方 phase 权威，只给未标注的 text 补中性 progress，**绝不按数组位置把官方 commentary 提升为 final**"（`projection_types.go:18–22` 同义注释）。

本回合两条 AgentMessage 的最终分类（progress / final）恰好与位置推断一致，所以**症状解释不受影响，但机制归因错了**；更要紧的是，据此提出的"把介绍正文升回 final"会**直接违背官方 phase 真值**，并污染 `finalAnswerItemIDs` 的语义（`finalAnswerItemIDs` 是 reducer 独占的答案身份集合，见 iOS `types.ts:181`）。

⇒ §5.2 第 3 条应**删除**。分类不是问题，客户端"要不要渲染 progress narrative"才是问题。

### P1-1 §4.1 的因果表述不准确（结论方向对，机制要改写）

文档说"桥在 `setCodexGoal` RPC 返回或 `thread_goal_updated` 之后才投影命令卡。模型 `task_started` + 首段 `AgentMessage` 往往更早"。

核验后，命令卡确实是 `session_command` 系统回合，但**不是"投影晚了"**：

- 目标快照 → 命令卡的转换在 `projection_reducer.go:1205–1237`：`commandID = "codex-goal:<createdAt>"`，`CommandLine = "/goal " + objective`，`CommandKind` 由官方 status 映射（running/success/error）。这与文档 §4.1 的判断一致，也与 CHANGELOG Unreleased 里"Codex 原生 Goal 快照复用 session_command 系统卡协议"的说法一致（`EventSessionCommand` 的生产者只有 dsh-web 与 grokbuild，Codex 侧是 reducer 合成的）。
- 但**时间上它来得并不晚**：`go-bridge.log` 第 319 行显示 `17:09:23.568 event=session_goal_record seq=3` 已转发，而官方首条介绍 `AgentMessage` 在 09:09:28（本地 17:09:28）——**命令卡事件比介绍正文早约 5 秒**。
- 真正的排序机制在 `projection_reducer.go:330–334`：`upsertTurn` 只做 `append`，**回合顺序 = 到达顺序，不按 `StartedAt` 排序**。而命令回合的 `StartedAt = goal.CreatedAt*1000`（`:340–346`），与模型回合的 `task_started` 同秒。模型回合先被插入（turn_started 先到），命令回合后 append ⇒ 渲染在整条 assistant 回合**下方**，正是图 1/图 2 看到的形态。

⇒ §4.1 的修法应改写为**排序问题**：给投影的 `Turns` 一个稳定排序键（`StartedAt` 优先，或让命令回合按其 `StartedAt` 原位插入），而不是"想办法更早发事件"。文档 §5.1 里"给 command turn 一个早于 task_started 的排序键"方向正确，但没说清根因是"append 序"而非"发送时机"，容易让实现者去改发送链路（改不动，因为事件本来就先到）。

另外补一条官方事实：本回合 Codex 侧的用户输入**不是任务原文**，而是

```text
<codex_internal_context source="goal">
Continue working toward the active thread goal.
…<objective>创作灭霸乌木喉黑矮星星云四人各自故事…</objective>
```

即"设目标"之后由 Codex 自己发起的 **continuation turn**。所以文档 §1/§4.1 的"交互四拍（打开 Goal → 输入任务 → 发出去 → 展示过程）"是 CordCode 侧的模型，官方是"先设目标 → 官方自起一轮"。这不改变"命令卡必须排在模型回合之前"的结论，但会改变实现者的心智模型，建议在 §4.1 明确写出。

### P1-2 §5.2 的推荐修复里有一条是产品契约变更

§5.2 第 2 条建议"改 `hideDetailProcessRows` 对 `workflowRun` 和已 stamp 过的 live narrative 的豁免"，并称"改动面最小"。

实际上这是**对 `turn_detail_lazy_v1`（§11.7）契约的全局改动**，不是局部豁免：

- 该门是"ChatGPT 式完成后再点开看过程"的实现本体，且只对 `codex-remote` 开启（`core/turn_detail_lazy_gate.go`），影响该后端**所有**回合，不只是 Goal 回合。
- 它会连带改变 `hasDetailBody` / `TurnDetailEntry` 的 `emptyLoaded`（"无详细过程"）判定路径，以及 `TurnDetailEntry.test.ts`、`Timeline.rows.test.tsx`、jsdom 套件里已锁定的语义（iOS 仓 CHANGELOG 提到定向 356 项测试全绿是当前基线）。

⇒ 若最终确认是假设 A，建议的写法是**"只豁免已在 live projection 里 materialize 过的块"**（即 `turnDetailHasMaterializedContent` 已经为真、或该块曾以 live 身份出现过），并且**按后端/回合类型 gate**，而不是无条件放开 `workflowRun`。文档 §5.2 第 1 条（显示层走另一条路径）与第 2 条（改豁免）应合并成一条并写清 gate 条件。

### P1-3 `interruptRunningWorkflowParts` 的断言未证实，且解释不了"卡片消失"

§4.2 写"同函数稍后 `interruptRunningWorkflowParts`：若 workflow 在回合结束时仍是 running，会被标成 interrupted"。本回合是否命中未取证：`wait_threads` 在 09:10:46 前已返回（FileChange 出现在 09:10:46），`task_complete` 在 09:10:55，正常情况下 run 应已收口。

更重要的是：**interrupted 只是状态词变化，part 仍在**（函数注释 `:644–650` 明确"不删除"）。所以它无法解释"Subagents 卡消失"。文档把它与"消失"并列在同一小节，容易误导实现者去调这个函数。建议把它降为"待核对的次要项"，并注明"即使命中也不产生'卡片不见'的症状"。

### P2 细节修正

| 位置 | 问题 | 建议 |
| --- | --- | --- |
| §4.2 / §7 | `classifyProjectionTextPresentation` 写作"约 829–862 行" | 实为 829–863 |
| §0 | "运行时 = commit e61a57f4 built 2026-09-11T09:03:39Z" | 应补一句：**截图运行时不含工作树里那批未提交修复**（workflow 卡 itemId、wait_threads 折叠、AgentStatus tagged union）。否则 §6 的验收无法区分"旧叠卡/不支持类型"与本题 |
| §1 表格 | 图 2 时长写"思考了 53 秒"，但同屏顶部还有一条被状态栏裁掉的时长行 | 建议写明图 2 顶部时长行被裁，避免后续 agent 以为入口行不存在（`renderRows.ts:446–470` 的 detailEntry 行 `orderKey = '-001'`，恰好会被截在屏幕外） |
| §4.3 | "官方 wait_threads 从 09:09:56 等到 09:10:46" | 09:09:56 是 McpToolCall item 落库时刻，等待结束约 09:10:46；建议措辞改为"item 记录于 09:09:56，实际等待至约 09:10:46" |

---

## 3. 文档漏掉的现场事实（建议补入）

1. **图 1 的介绍正文是重复的**。放大图 1 首行可见："**我会调用两个**我会调用两个智能体分工创作四篇故事…"；官方 rollout 与图 3 都只有一份。这是一条**独立于本题的直播文本重复缺陷**（疑似 append_text 增量与整值 upsert 叠加），文档完全没提。它会污染"图 1 已对上"的结论（叠卡对上了，但同一帧里还有别的问题）。
2. **官方 goal 回合的 user input 是 `<codex_internal_context source="goal">` 包裹的 continuation**（见 P1-1）。图 3 的用户气泡是目标正文 + "设为目标"，说明官方把目标呈现为**用户消息的属性**，而不是一张命令卡——这是"图 3 与 CordCode 形态差异"的官方依据，比文档现在的表述更有说服力。
3. **图 2 的可见行集合本身是证据**（见 P0-1）：它证明该回合的 `blocks` 至少还含 reasoning / 文件 / 命令，缺的只有"前半段"（intro + send_message×2 + wait_threads）。这条集合关系是选路的关键，建议直接写进 §4.3。
4. **已排除的候选**：`MAX_INLINE_PROCESS_BLOCKS = 80`（`AssistantTurn.tsx:106`，`SplitAssistantTurnRows.tsx:373` 按 `slice(-80)` 截断并给"查看更早的 N 个执行步骤"按钮）。本回合远未触及 80，且图 2 没有该按钮，可排除，省掉后续一轮试错。
5. **渲染路径未确认**：iOS 同时存在 message-web 与原生降级（`ChatTimelineAdapterUIKit`）。P0-1 的推理基于 message-web 的 split 路径；若图 2 实际是原生降级渲染，`hideDetailProcessRows` 根本不参与。建议在取证时一并记录"图 2 是 web 还是 native 渲染"。

---

## 4. 建议的修复方向（对 §5 的修订）

### 第 0 步（必须先做，不可跳过）：取证

文档 §4.3 已经写对了要抓什么，这里把它升级为**前置门槛**，并给出最小清单：

1. 同一回合 `turn_completed` 前最后一条 projection patch 与收口后的完整快照，记录 `assistant.parts[]` 的**类型序列**（text/reasoning/tool/workflow/…）与 `presentation`。
2. iOS 侧同一时刻的 `WebTimelineItem.assistant.blocks` 的 **kind 序列**，以及 `turnDetailID` / `turnDetailLoadState` / `phase` 三元组。
3. 记录该回合走的是 split 还是 whole 路径、web 还是 native 渲染。
4. 若走 lazy detail：记录 `session_turn_items` 的返回与 `classifyDetailParts` 是否拒收（上一轮报过 `unsupported_item_type`）。

判定规则：

```text
parts 里就没有 intro / send_message / workflow      → 假设 B（上游丢块），改投影或映射
parts 齐全但 blocks 缺                            → 假设 B'（iOS 映射丢块），改 mapping
parts 与 blocks 都齐，只是渲染成壳                 → 假设 A（客户端门），才轮到 §5.2
```

### 假设 A 成立时的最小修法（原 §5.2 修订）

- 只对**已在 live projection 中出现过**的块豁免（用 `turnDetailHasMaterializedContent` 或"live 身份"标记），不无条件放开 `workflowRun`。
- 懒加载只负责"补从未直播过的过长细节"（超大 tool output），不负责"把直播过的过程再藏起来"。
- 保持 `TurnDetailEntry` 在无过程块时仍为"无详细过程"（`emptyLoaded`），别把契约改坏。
- 删掉 §5.2 第 3 条（Mac 改分类）。

### 假设 B/B' 成立时的方向

- 先查 iOS `SessionProjectionMapping` / `MessageWebSnapshotBuilder` 对 MCP tool、commentary narrative、无 itemId part 的处理（`workflow` part 的 `itemId` 正是本次未提交修复补上的）。
- 再查收口时的整值 upsert 与 live 增量是否发生"部分覆盖"（CHANGELOG 提到过"已发布的 turn shell 后续只发送工具增量""超阈值不再记录大 patch"这类降载优化，值得重点怀疑）。
- **不要**用容错/回放掩盖丢块。

### §5.1 修订

保留"命令卡必须先于模型回合"，但把做法写成"**投影层稳定排序**"：`Turns` 按 `StartedAt`（并列时按"命令先于回合"的显式优先级）排序，或在 reducer 里按 `StartedAt` 原位插入，而不是依赖到达顺序 append。同时把"官方由 Codex 自起 continuation turn"这一事实写进小节。

### 明确不做（保留文档原文，措辞可微调）

不造假卡、不把 MCP 过程做成假 turn、不靠轮询/缓存回放掩盖、不把"思考了 53 秒"当成过程完整、不动 collab 状态表——这几条都对。

---

## 5. 对 §6 验收的补强

现有 5 条可保留，建议补 3 条：

6. **同一回合内**断言"介绍正文仍在 + Subagents 卡仍在 + wait/发消息仍在"，且**与 final 正文同屏可见**（不是"点开才有"）。
7. 断言介绍正文**不重复**（图 1 已暴露的独立缺陷）。
8. 断言命令卡位于模型回合**之前**，且不随官方状态更新而位移（`commandID = codex-goal:<createdAt>` 应整值原位结算）。

另外：§6 第 1 条要求"新开一条 Codex Remote Goal"，请明确**必须使用含本次未提交修复的 runtime**，否则无法区分旧叠卡/`unsupported_item_type` 与本题。

---

## 6. 总体评价

| 维度 | 评价 |
| --- | --- |
| 现场取证（截图、rollout 对账） | **好**。时间线逐条可复现，图 3 对照有说服力 |
| 代码锚点 | **好**。§7 表格里 9 个锚点全部存在，行号仅 1 处偏差 |
| 排他性结论（不是 collab、不是叠卡） | **好**，且"不要改 collab 状态表"的护栏值得保留 |
| 根因归因 | **不可靠**。§4.1 方向对但机制写错；§4.2 与图 2 自相矛盾 |
| 修复方案 | **有风险**。§5.2 第 2 条实为 §11.7 契约变更，第 3 条与官方 phase 真值冲突 |
| 可执行性 | 需要补 §4 的取证门槛，否则实现者会照着"主修复"改错门 |

**给接手 agent 的一句话**：这份文档可以当"现场卷宗"用，但不要照它的 §4.2/§5.2 直接改 `hideDetailProcessRows`——先用 §4 的清单抓一次 `parts[]` 与 `blocks[]` 的类型序列，再决定改哪一层。排序问题（§4.1）与文本重复（图 1）是两条相对独立、且现在就能确认的缺陷，可以先做。
