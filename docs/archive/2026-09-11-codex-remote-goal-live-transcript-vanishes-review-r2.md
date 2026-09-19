# 第二轮评审：`../2026-09-11-codex-remote-goal-live-transcript-vanishes.md`（修订稿）

评审日期：2026-09-11（第二轮）  
评审性质：**只读评审，未改任何代码**  
被评审：`../2026-09-11-codex-remote-goal-live-transcript-vanishes.md`（2026-09-11 评审后修订稿）  
上一轮：`2026-09-11-codex-remote-goal-live-transcript-vanishes-review.md`

本轮新增对照物（上一轮未用到）：

```text
app 内实际运行的 web bundle  /Users/jacklee/Projects/cordcode-ios-plan-approval/OpenCodeiOS/OpenCodeiOS/Resources/MessageWeb/assets/index-VTD2FSM_.js
                             （2026-09-07 02:29，与仓库 message-web/dist 同文件；已解包比对，非猜测）
go-bridge.log relay 序号      relayEvents forwarding 的 seq 在同一连接内全局递增
图 2 顶部行（放大 6 倍）      可读为「用时 1 分 33 秒 ›」
```

**一句话结论**：修订稿把上一轮指出的四类归因问题**全部改对了**，方向与护栏都站得住，可以继续用；但本轮发现 **§4.2 的"门必为 false"论证与同一张图自相矛盾**（图 2 的入口行证明门应为 true），以及 **§4.1 新写的"append 序"机制被日志序号反对**。这两条不解决，§5 的两个分支都可能在错误的层上动手。

---

## 1. 修订稿已正确吸收的（逐条核验通过）

| 上一轮意见 | 修订稿落点 | 核验 |
| --- | --- | --- |
| §4.2 的 hide 门不能当主因 | §4.2 标题改为"未证实"，并列出与图 2 矛盾的三点 | **已改，且论证比上一轮更完整**（补了"缺失形状与门的作用域不一致"） |
| `classifyProjectionTextPresentation` 走的是官方 phase 分支 | §4.2 末段列出 rollout phase / `history.go:451–461` / `:1300–1331` / `:843–853` | 一致；§5.2 里"不要升 final"也已写明 |
| 命令卡不是"发晚了"，且不应改发送链路 | §4.1 用 `session_goal_record` 时间 + `upsertTurn` append + `codex-goal:<createdAt>` 重写 | 事实部分全部一致（`projection_reducer.go:330–334 / :340–346 / :1205–1237`） |
| `interruptRunningWorkflowParts` 降为次要项 | §4.2 末段 + §5.2"与消失无关"清单 | 一致（`:644–673`，不删除 part） |
| 补图 1 介绍重复 | §1 图 1 行 + §6 第 6 条 | 一致（放大图 1 可读「我会调用两个**我会调用两个**智能体…」） |
| 补 continuation turn 事实 | §2 末段 + §4.1 末段 + §5.1 末条 | 一致（rollout user input 为 `<codex_internal_context source="goal">`） |
| 排除 `MAX_INLINE_PROCESS_BLOCKS=80` | §2 末 | 一致（`AssistantTurn.tsx:106`） |
| 截图 runtime 不含未提交修复 | §0 末尾 + §6 开头 | 一致，且 §3 表格第三行也补了这句 |
| 验收必须用带未提交修复的 runtime | §6 开头 | 一致 |

§3"前几轮为什么修不好"、"不要改 collab 状态表"、§7 锚点表（新增的 `:330–334`/`:1205–1237`/`ProcessGroup.tsx` 约 1789 全部存在且指向正确）均保留得当。

---

## 2. 必须修正

### R2-1（阻断级）§4.2 的"门必为 false"与图 2 的入口行自相矛盾

修订稿 §4.2 的论证是：

> 图 2 有展开态 ProcessGroup 子行 ⇒ 因此截图那一刻该门**必为 false**（`turnDetailID` 空，或 phase 非 finalized，或 `detailBodyVisible === true`）。

但同一张图里还有一行被忽略的内容：**顶部那行是「用时 1 分 33 秒 ›」**（放大 6 倍可读；文档 §1 只写了"被状态栏裁掉"）。这一行把上面三种逃生口堵死了：

1. **它只可能是 `detailEntry` 行。** 在整个 app 内运行的 bundle 里，字符串「用时」**只出现 1 次**，位于 `durationLabel`（`function $D(e){…`用时 ${Dd(e)}`}`），唯一调用方是 `turnDetailEntryView`。Swift 侧没有任何「用时 X 分 Y 秒」文案（唯一的 `用时：` 出现在 goal 详情弹窗里）。而 `detailEntry` 行只在 `assistant.turnDetailID && assistant.phase === 'finalized'` 时创建（`renderRows.ts:446`），`turnDetailEntryView` 也要求 `turnID && loadState && phase === 'finalized'`。
   ⇒ **该 item 的 `turnDetailID ≠ null` 且 `phase === 'finalized'`**，前两种逃生口不成立。
2. **它的标签是时长，不是"收起"。** `TurnDetailEntry` 只在 `expanded` 时把标签换成"收起"（bundle 里 `h.expanded ? …children:"收起"`，与 src 一致；`isProcessExpanded` 传的是 `detailExpanded`）。标签是时长 ⇒ `detailDisclosureExpanded === false` ⇒ `detailExpanded = detailHasBody && detailDisclosureExpanded === false` ⇒ `detailBodyVisible === false` ⇒ **`hideDetailProcessRows === true`**。
3. 门为 true 时，该 item 的 `processBatch` / `reasoning` / `workflowRun` 行一律 `content = null`。bundle 里就是这段（与 src 逐字一致）：

```js
O = !!S.turnDetailID && S.phase === "finalized" && L && !N            // hideDetailProcessRows
if (O && !W) … else (n.kind === "processBatch" || n.kind === "reasoning"
  || n.kind === "subagent" || n.kind === "userInput" || n.kind === "workflowRun") && (R = null)
```

⇒ **"展开态 ProcessGroup（收起过程 + 子行）"与"未展开的入口行"不可能出自同一个 item。** 图 2 里两者同时可见，是硬矛盾。

**已排除的解释（都查过了）**：

- ❌ "app 内 bundle 落后于仓库 src"：bundle 含 `turn-detail-answer-divider` / `无详细过程` / `收起过程` / `turn-detail-entry`，且 hide 门、`hasDetailBody`、`TurnDetailEntry` 三段实现与 src 逐字一致。
- ❌ "图 2 是原生降级渲染（`ChatTimelineAdapterUIKit`）"：Swift 侧没有「用时 X 分 Y 秒」文案，也没有「收起过程」（该文案只在 `shared-message-renderer` 的 TS 里）。
- ❌ "整 turn 路径（`AssistantTurn`）渲染"：整 turn 路径不渲染 `TurnDetailEntry`，不会出现「用时」行；而 `turnDetailID` 存在时会强制走 split（`renderRowStore.ts:55`）。

**因此 R2-1 的结论（比修订稿更进一步）**：

> 图 2 的"前半段缺失"很可能**不是"同一个 item 里被门藏掉"**，而是**同一官方回合在 iOS 侧被拆成多个 item，承载前半段（介绍 + send_message×2 + wait_threads）的那个 item 整体缺失/未被渲染**；幸存 item 只带尾部（reasoning + 文件 + 命令 + 终答），另有 item 带入口行。这正好解释"缺前半段、留尾部"这个形状——门的作用域解释不了它。

**要补的取证（加进 §4.3 清单，优先级最高）**：

5. 该回合在 iOS 侧是**几个 item**？逐 item 记录：`groupID`、`phase`、`turnDetailID`、`turnDetailLoadState`、`blocks` 的 kind 序列。
6. 若确为多 item：这些 item 的**生成规则**是什么（按 assistant 消息？按 settled 边界？），以及缺失的那个 item 是在 **wire 里就没有**、还是在 **iOS 映射/快照合并时被丢**。
7. 顺序：`Turns` 数组顺序 vs iOS items 顺序 vs 屏幕顺序（见 R2-2）。

在拿到 5–7 之前，§4.2 的三种逃生口枚举应加上"入口行存在"这一条约束，不要把它当已排除项。

### R2-2 §4.1 的"append 序"机制被日志序号反对；§5.1 的修法可能是空转

修订稿把 §4.1 从"现象"升级为"**已确认**：命令卡排在模型回合后面（投影 append 序）"，机制表述为"模型回合因 `turn_started` 先插入，命令回合后 append"。

日志不支持这个顺序：

```text
17:08:37.929  relayEvents forwarding event=session_goal_record seq=1
17:08:37.929  relayEvents forwarding event=session_goal_record seq=2
17:09:23.568  relayEvents forwarding event=session_goal_record seq=3   ← 本回合命令卡事件
17:10:56.224  relayEvents forwarding event=turn_completed     seq=128   ← 模型回合收口
```

`seq` 是**同一连接内的全局递增序号**（同一连接里 1,2,3,…,128 连续；换连接后回到 1，见 17:19:49 的 `seq=1`）。因此模型回合的 125 个事件（seq 4–127）**全部晚于**命令卡事件。

再加上：`turn_started` 是 persist-only，**不发布 shell**（`projection_reducer.go:363–395` 的 `upsertTurnPersistOnly`，以及 `:1336–1341` 注释"persist-only turn_started intentionally did not publish its shell"）。所以 assistant 回合是在**首个内容帧**（文本增量，≈17:09:28）才被 append 的——仍晚于命令卡（17:09:23.568）。

⇒ `Turns` 数组顺序本来就是 **`[cmd, assistant]`**，与修订稿写的"模型回合先插入"相反。而 §5.1 给的修法是"按 `StartedAt` 排序 / 按 `StartedAt` 原位插入"——**在数组已经是 [cmd, assistant] 的前提下，这个改动不会改变任何位置**（`StartedAt` 也是 cmd 更早）。

⇒ 视觉顺序（卡在模型内容之后、且在图 2 里插在终答与过程组之间）**不可能只由 Mac 投影数组顺序解释**，排序/分组很可能发生在 wire 之后（iOS items 组装、分组或虚拟列表行序）。

**建议改写**：

- §4.1 标题回到"现象已确认 + 排序层待取证"，机制一句改为"命令卡事件确实先到（这一点已证实，故不要改发送链路），但屏幕顺序与 `Turns` 数组顺序不一致，排序发生在哪一层未定"。
- §4.3 清单补第 7 项（上面），并要求同时抓 `Turns` 数组顺序与 iOS items 顺序。
- §5.1 保留"投影层稳定排序"作为**候选之一**，但注明"若数组已是 [cmd, assistant]，则本条不产生可见变化，需先定排序层"，避免实现者改完发现没动。

### R2-3 细节修正

| 位置 | 问题 | 建议 |
| --- | --- | --- |
| §1 图 2 行 | "顶部时长行被状态栏裁掉" | 可写实：该行内容为「用时 1 分 33 秒 ›」（即入口行的**未展开**态），并注明它是 R2-1 的关键证据 |
| §2 表 task_complete 行 | "时长约 1 分 29–32 秒" | 两个口径都写：官方 09:09:23→09:10:55 ≈ 92–93s；iOS 入口行显示 **1 分 33 秒**，官方 Desktop 显示 1 分 30 秒 / "1m29s 内达成目标" |
| §4.2 | "（`turnDetailID` 空，或 phase 非 finalized，或 `detailBodyVisible === true`）" | 加限定：前两种被同一张图的入口行排除，只剩第三种；而第三种要求标签是"收起"，与截图冲突 ⇒ 见 R2-1 |
| §4.3 | 清单只有 4 项 | 补第 5–7 项（items 边界 / item 生成规则 / 数组顺序 vs 屏幕顺序） |
| §5.2 假设 A | 仍以"门"为唯一候选 | 加一句：R2-1 表明"同一 item 内被门藏"与截图冲突，假设 A 的成立前提应改为"**门 + 多 item 拆分**"共同作用 |

---

## 3. 修订稿仍然正确、不要动的部分

- §1/§2 的现场描述与官方时间线对账（本轮又核了一遍，含 5 次 reasoning、`wait_threads` 的 item 时刻措辞、continuation turn）。
- §3 的排他性结论与"**不要改 collab 状态表**"。
- §4.2 关于 `interruptRunningWorkflowParts`、`MAX_INLINE_PROCESS_BLOCKS`、"Mac 没有 `Parts` 截断"的三条排除。
- §5.2 的"不要升 final"、"只豁免 live 出现过的块 + 按后端/回合 gate"、"懒加载只补从未直播的细节"、"保持 `emptyLoaded`"。
- §5.3 的"先取证再选层"、§6 的验收条目（第 4 条"同屏可见"、第 6 条"不重复"、第 7 条"FAIL 就停"都对）。

---

## 4. 给接手 agent 的优先级

1. **先做 §4.3（含 R2-1/R2-2 补的三项）**：items 边界 + 每个 item 的 `phase`/`turnDetailID`/`blocks kind 序列` + `Turns` 数组顺序 + iOS items 顺序。这一步决定后面所有分支。
2. 与取证并行可做的（已单独确认，不依赖上面）：**命令卡顺序**（先定排序层，再决定改不改 Mac）、**介绍正文去重**（图 1 独立缺陷）。
3. 取证结果若显示"item 整体缺失"，主战场是 iOS items 组装/合并或 wire，而不是 `hideDetailProcessRows`；若显示"单 item 内块缺失"，主战场是映射/投影；只有在两者都不成立时才回到门。
4. 验收必须用含工作树未提交修复的 runtime（修订稿已写对）。

**一句话**：修订稿把上一轮的问题改干净了，但它现在把两处"尚未证实"当成了"已确认"——一处是图 2 的渲染结构（R2-1），一处是排序层（R2-2）。这两处都只需要一次带 items 边界的抓取就能定案，成本很低，别跳过。
