# 第三轮评审：`docs/2026-09-11-codex-remote-goal-live-transcript-vanishes.md`（第二轮修订稿）

评审日期：2026-09-11（第三轮）  
评审性质：**只读评审，未改任何代码**  
被评审：`docs/2026-09-11-codex-remote-goal-live-transcript-vanishes.md`  
前两轮：`...-review.md`、`...-review-r2.md`

**一句话结论**：修订稿把两处「未证实写成已确认」降下来之后，**没有引入新的事实错误**；本轮新增的全部锚点核验通过，§0.1 的不采纳表逐条都站得住（包括对我自己写坏的公式的更正）。本轮**新增一个决定性发现**：文档的领先假设「多 item」在 iOS 侧已有**现成的代码级机制**（同 turnID 幽灵 last-wins 去重）和**现成的诊断日志**（`[TurnDetailDedupe]`），取证不必再手写埋点。另有两处需要收口：领先假设应拆成 C1/C2，以及一处证据措辞。

---

## 1. 本轮核验通过（修订稿新增的锚点全部为真）

| 文档位置 | 断言 | 核验 |
| --- | --- | --- |
| §4.1 | `SessionProjection.upsertingTurns` 对未知 `turnId` 尾部 append | ✅ `SessionProjection.swift:679–689`：`firstIndex(where:)` 命中则 merge，否则 `c.turns.append(upsert)` |
| §4.1 | `messages(from:)` 按 `projection.turns` 原序展开，一回合 → user + assistant + system | ✅ `SessionProjectionMapping.swift:20–36`（`for turn in projection.turns`），每回合 append user?/assistant/system |
| §4.2 | `messagesForTurn` 每投影回合至多一条 assistant `Message` | ✅ 同文件 `:60–105`，`if let assistant = turn.assistant { … result.append(mappedAssistant) }` |
| §4.1 | `groupedMessages` 把 system 独立成组（切断 assistant run） | ✅ `ChatTimelineAdapterUIKit.swift:539–560`，`role == .system` 直接 append 独立 group；合并条件要求 `groups.last?.role == .assistant` |
| §4.1 | settled 后不同 message id 不合并 | ✅ 同文件 `:595–600`，`lastAssistantIsSettled && group.id != message.id` ⇒ 新 group |
| §4.1 | `:392–460` 是上一轮为 dsh 命令卡消失修过的增量 tail 路径 | ✅ `:416–422` 注释原文即「dsh /goal 等指令卡是 system 消息…旧实现只重算 assistantTailGroup，tail 里的 system 组被整体丢弃 → 指令气泡发送后随首帧流式渲染消失」 |
| §7 | `renderRowStore.ts:44–56`：有 `turnDetailID` 即强制 split | ✅ `shouldSplitTurn`：非 assistant 或 phase ≠ finalized 返回 false；`if (item.assistant?.turnDetailID) return true;` |
| §7 | `TurnDetailEntry.tsx:42–70`、`:157–198` 标签语义 | ✅ 展开 ⇒ 「收起」+ `⌄`；未展开 ⇒ 时长 + `›`；`emptyLoaded` ⇒ 「无详细过程」 |
| §4.2 | app 内 bundle 与 src 同代、hide 门逐字一致 | ✅ 沿用第二轮结论（bundle 含 `turn-detail-answer-divider`/`无详细过程`/`收起过程`；hide 门、`hasDetailBody`、`TurnDetailEntry` 三段与 src 一致） |
| §4.1 | `turn_started` persist-only、不发布 shell | ✅ `projection_reducer.go:911–929`（注释「No commit / no flush-buffer writes on turn_started」）+ `upsertTurnPersistOnly` |
| §0.1 | Swift 只有 goal 弹窗的「用时：」（全角冒号） | ✅ `ChatUIKitContainerView.swift:7129` |

## 2. 新增证据：官方序确认 Mac `Turns` = `[cmd, assistant]`（文档 §4.1 的推断成立，且比文档手上的证据更硬）

rollout 毫秒序：

```text
09:09:23.150  thread_goal_updated (status=active)   ← 命令卡
09:09:23.216  task_started (01a08fba-…)             ← 模型回合 turn_started
```

`thread_goal_updated` **早 66 ms**，所以 goal 记录先到、命令回合先 append ⇒ `[cmd, assistant]`。文档 §4.1 的结论正确。

**但建议补一句限定**（否则后来者容易推出相反结论）：`turn_started` 虽然 persist-only、不发布 patch，但它仍会经 `upsertTurnPersistOnly` **进入 `projection.Turns`**（`:394` 的 append 分支）。也就是说「数组顺序」由**事件到达序**决定，而不是由**发布序**决定——本轮之前的推理（含我第二轮）正是栽在这里。日志里看不到 persist-only 事件，所以不能只看 relay seq 推数组序；官方 rollout 的毫秒序才是可用的替代证据。建议把这条写进 §4.1。

## 3. R3-1（本轮最高价值）：多 item 家族已有现成机制 + 现成日志

文档 §4.2 的领先假设是「同一官方回合在 iOS 侧被拆成多个 item，承载前半段的 item 整体不在屏幕上」，但只给了「更可能来自分组/快照层」的方向。实际上这条路在 iOS 侧有一条**成文规则**，直接产出「整个 group 不生成 item」：

`ChatTimelineAdapter.makeItems`（`ChatTimelineAdapterUIKit.swift:35–96`，注释 `:53–58`）：

```swift
// turn_detail_lazy_v1（G3 #7 防御 + 诊断）：live reducer / recovery pull / 投影
// changeset 三源并存的窗口里，同一 turn 可能残留多条 assistant messages（分组后
// 多个 group 共享同一 turnDetail.turnID），每条都盖入口 → 「加载详细过程 ›」成行
// 堆叠、页脚/时长落在错误条目上。投影模型每 turn 恰一条 assistant message
// （messagesForTurn），故同 turnID 多 group 必有幽灵——last-wins 只保留最后一条
// （投影权威形态最后落地），其余跳过。
…
// 同 turnID 的非末条（幽灵）：跳过，不生成 presentation item。
if group.role == .assistant,
   let turnID = group.turnDetail?.turnID, !turnID.isEmpty,
   groupIndex != lastGroupIndexByTurnDetailID[turnID] {
    continue
}
```

这条规则与图 2 的形态**逐条对上**，而它不在文档的候选列表里：

| 图 2 观察 | 该规则的解释 |
| --- | --- |
| 前半段（介绍 + send_message×2 + wait_threads）整段不在屏幕上 | 承载前半段的那个 group 被判为「幽灵」，**`continue`，不生成任何 item** |
| 入口行只有一条、不堆叠 | 去重就是为此加的（注释写明历史症状是「入口成行堆叠」） |
| 只留下尾部（终答 / 过程） | last-wins 保留的是「最后落地」的那一条 |

而且它**自带诊断日志**（`:77–91`）：

```text
[TurnDetailDedupe] ghost turn=%@ kept(id=%@ msgs=%@ durationMs=%@) dropped=id=… msgs=… content=Nc steps=N parts=N durationMs=…
```

⇒ **取证方式应改为直接抓这条 NSLog**（必要时在真机上复现一次同回合）：它一次给出 kept / dropped 两个 group 的 `id`、`messageIDs`、`content` 长度、`steps` 数、`parts` 数、`durationMs`。这比手写埋点快得多，而且能直接判定「前半段是被整组丢弃（C1）还是被门藏起来（C2）」。

建议的文档改动：

- §4.3 第 5 项加一句：优先抓 `[TurnDetailDedupe]`（`ChatTimelineAdapterUIKit.swift:77–91`），并记录 kept/dropped 两个 group 的字段。
- §4.2 的「多 item 来源」候选里补上这条规则，并补它点名的**三源并存窗口**（"live reducer / recovery pull / 投影 changeset"）——这才是「同一 turn 出现 2 条 assistant message」的根，比现在列的 settled 切分/`removedItemIDs` 更贴。
- §5.2 假设 C 的修法里加一条硬约束：**去重的 kept 选择不能只按「最后落地」**；至少要在丢弃前确认被丢弃的 group 的内容已被 kept group 覆盖（否则就是现在这种「丢一半」）。这正是「C1 分支」的最小修法。

## 4. R3-2 一致性：领先假设应拆成 C1 / C2，且 C2 下「门」仍是前半段不可见的原因之一

文档 §4.2 的论证（带入口的 item 上门为 true ⇒ 该 item 只剩终答）与 §1 的硬约束（过程组不可能与该入口同 item）**合起来**其实指向 C2，而 §4.2 的「领先假设」写的是 C1：

- **C1**：承载前半段的 item **整体不在列表里**（现在文档写的那条）。
- **C2**：承载前半段的 item **在列表里**，但被 hide 门只留终答；展开的 ProcessGroup 来自**另一个未受门约束的 item**（无 `turnDetailID`，或已展开）。

判别只需一个字段：**带入口的那个 item，它的 `blocks` 里还有没有 intro / workflow**。

- 有（只是没画）⇒ C2。此时「前半段消失」= **门 + 多 item 拆分共同作用**，门确实是原因之一。
- 没有 ⇒ C1。此时前半段是**真的丢了**（很可能就是 §3 那条去重规则丢的）。

建议改写：

- §4.2 的「领先假设」写成 C1/C2 并列，并注明判别字段；
- 现在那句「门为 true 时这个 item 自己的 ProcessGroup 不应出现 ⇒ 门解释不了图 2」建议收窄为「**门解释不了过程组，但可以解释前半段在带入口 item 上不可见**」；
- §5.2 假设 A 与假设 C 的边界随之调整：C2 下二者是同一件事的两面（A 只描述渲染结果，C 描述结构成因），不要当成互斥分支。

这条不影响「不要无条件改 hide 门」「不要把 commentary 升成 final」两条护栏，也不影响 §5.1。

## 5. R3-3 证据措辞（小，但影响交接可信度）

| 位置 | 问题 | 建议 |
| --- | --- | --- |
| §0.1 末行 | 写「`›` 沿用评审 6 倍读数」 | 我第二轮 6 倍读出的是**时长文案**；`›` 是**按源码未展开态推断**的（`.turn-detail-entry-label` 为 `flex: 1 1 auto`，chevron 落在行尾，恰在会话标题栏分享按钮附近，像素上无法单独确认）。改为「按未展开态推断（chevron 未像素确认）」 |
| §0.1 第 1 行 | 指出评审把 `detailExpanded` 公式写坏 | **文档正确，我的措辞有误**（`detailHasBody && detailDisclosureExpanded`，不是 `&& detailDisclosureExpanded === false`）。结论不变，采纳处理得当 |
| §1 图 2 行 | 上一稿写「状态栏」 | 改「会话标题栏」是对的（遮住时长行的是会话标题栏副行，不是系统状态栏） |
| §4.3 | 候选清单 | 建议补一条**已排除**：iOS 的 goal 动作**不做本地乐观插入**——`runCodexGoalCommand`（`ChatUIKitContainerView.swift:6983–7034`）是纯 RPC，成功后只提示「已提交 /goal，状态以官方读回为准」，不建本地 turn/message。这样「本地乐观 item 抢位」这条不必再查 |

## 6. 保留（本轮无异议，不要动）

- §3 的排他结论与「**不要改 collab 状态表**」。
- §4.1 的「事件确实先到 ⇒ 不要改发送链路」与「先定排序层」。
- §5.1 的三候选结构（含「若数组已是 `[cmd, assistant]`，`StartedAt` 排序不产生可见变化」这句——**这句是本次修订最有价值的一句**）。
- §5.2 的「不要升 final」「不无条件改门」「懒加载只补从未直播的细节」「保持 `emptyLoaded`」。
- §6 的验收（尤其第 7 条要求保留逐 item 的 `phase` / `turnDetailID` / `blocks[]`）。
- §0.1 的「不采纳 + 理由」表本身：这是很好的实践，建议以后每轮都留。

## 7. 接手 agent 的最短路径（本轮更新）

1. 复现同回合，抓 `[TurnDetailDedupe]`（kept/dropped 两个 group 的 id / messageIDs / content 长度 / steps / parts / durationMs）+ 该回合逐 item 的 `phase` / `turnDetailID` / `blocks` kind 序列。
2. 看带入口 item 的 blocks 里有没有 intro/workflow ⇒ C2（门 + 多 item）还是 C1（整组被丢）。
3. 顺序问题：抓 iOS replica `turns` 顺序 vs items 顺序 vs 屏幕顺序（Mac 侧已知应为 `[cmd, assistant]`）。
4. 可并行：介绍正文去重（图 1，独立缺陷）。
5. 护栏不变：不先改 `hideDetailProcessRows`、不升 commentary、不动 collab、不用回放压症状、验收用含工作树未提交修复的 runtime。
