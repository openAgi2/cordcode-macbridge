# Goal 会话连续「思考」卡与 workflow 双卡归因分析（复核修订版）

Date: 2026-09-12
Status: analysis（只分析不改码；修复方向见 §7，供实现 agent 决策）
触发: owner 测试 Codex Desktop 版 goal 任务（四子代理写故事），截图
`/Users/jacklee/Downloads/Scrollie_20260912_000020.jpg`（iOS 端，cold reopen 后）。
修订: 本版为同日二次复核修订。初版两大结论（思考卡=表示层与官方 UX 分叉、
workflow 双卡=per-turn 折叠器违背自身「一卡」注释）经逐项重取证**全部成立**，
但有 4 处证据细节错误已修正、多项新证据已补强，勘误记录见 §1.1。

## 0. 来源清单（P0 来源门）

| 仓库 | 工作树路径 | 分支 | 提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | `1062d9d917317649116bef2e30e0aec0614dd2cc` | 干净（仅本文档 untracked） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-plan-approval` | `plan/approval-layer-ios` | `aac039587fec1e65eb5d1a8d04eedda801d618fd` | **脏**：14 文件 +264/−118，含 `SessionProjection.swift`、`ChatTimelineAdapterUIKit.swift`、`SplitAssistantTurnRows.tsx` 等投影/渲染层 |

- 双仓配对依据：分支族 `plan/approval-layer` ↔ `plan/approval-layer-ios` 在两仓
  工作树列表中唯一对应（其余工作树分别为 main 与无关 detached checkout）。
- iOS 侧结论分级：本文对 iOS 渲染行为的论断以**截图行为证据**为主；涉及 iOS
  源码的引证（WorkflowRunBlock 标签、part 模型）基于上述脏工作树当前状态，
  渲染层后续提交落地时需重新对位。
- 上游真值（生产落盘证据）：
  - leader rollout `~/.codex/sessions/2026/09/11/rollout-2026-09-11T23-43-10-01a09123-144c-7533-a86b-0d57bd8b9db5.jsonl`（346K，2026-09-11 23:43–23:50）；
  - 四子代理 rollout `01a09126-ab7e / b230 / b6f7 / bead`（23:47:05–10 创建）；
  - 官方 Desktop 对照截图 `docs/assets/2026-09-11-codex-goal-live/03-codex-desktop-same-turn.jpg`。
- Codex 上游语义锚点：`agent/codex-remote/codec.go:464-468` 注释引用上游
  `protocol.rs:1836`（`AgentStatus::Shutdown` 语义）。
- Mac 侧代码引证全部来自本 worktree `plan/approval-layer` @ `1062d9d`。

## 1. 结论摘要（TL;DR）

1. **连续「思考」卡 = 两个叠加的表示层机制，不是数据丢失或合并逻辑坏了。**
   - 机制 A（turn 内）：官方 app-server 每个 reasoning 是独立 item、各有唯一
     itemId；reducer 按设计「不同 itemId 不合并」（保序，见 §4.1 锚点），
     iOS 每个 reasoning part 渲染一张「思考」卡。
   - 机制 B（跨 turn，**复核新增，截图连发的主导机制**）：goal 任务由 leader
     自主链式多 turn 驱动，本 session 13 个 turn 中 **6 个是「纯思考 turn」**
     （整 turn 只有 1 个 Reasoning，无消息无工具）；turn 边界=消息边界，相邻
     思考卡之间什么都不隔。实测 item_completed 流中所有 ≥3 连的思考链**全部
     跨 turn 边界**，单 turn 内最长仅 2 连。
   - 官方 Codex Desktop 对照：一张思考卡都不渲染（reasoning 完全不可见）。
2. **workflow 卡出现两次 = 折叠器实现与自身注释意图相悖（复核维持，证据升级）。**
   官方协议没有 workflow item（四子代理 = 4 个 `create_thread` McpToolCall +
   `wait_threads` 轮询，create_thread 参数只有 `prompt`+`target`、无 `title`）。
   `collab_workflow.go:265` 按 turnID 建折叠器、runID=`codex-collab:<turnID>`，
   而文件头注释（:3-8）宣称「first spawn anchors the card, later operations
   update the same members in place」（一卡）。本 session 恰好有 **2 个**含
   collab item 的 turn（spawn turn `01a09126-43f4` 与后续 `01a09127-41ce`）
   → 恰好 2 个 run → 2 张卡，与截图逐点吻合（§5.2）。
3. **「已中断 2+2」是伪态，且中断来源已收窄到唯一通道（复核升级）。**
   四个子代理上游各恰好 1 turn、全部 `task_complete`；全 session 13/13 turn
   正常结束、无任何中断事件；四次 wait poll 逐条核查**从未出现 interrupted
   状态值**；rollout 中 "interrupt" 字样仅出现在系统提示模板。排除法后，
   interrupted 只能来自 live 独有的 `subAgentActivity` 流（不落盘，无法事后
   复核）。精确构成：A 卡最后一次 poll 只含 林黛玉/薛宝钗 两个 completed
   条目，贾宝玉/王熙凤（当时尚未完成的两员）随后被 activity 事件翻成
   interrupted 并被该卡永久冻结；全部完成的真相只流进了 B 卡。
4. 附带确认：昨日修复在本截图已验证——冷重建后 `/goal` 命令卡、goal bar、
   四成员 workflow 详情均在位（§9）。

### 1.1 初版 → 本版修订记录（证据勘误）

| # | 初版说法 | 复核结论 |
| --- | --- | --- |
| 1 | 「实测 2/3/4 连 reasoning 中间无工具」并引用 `rs_791c520c→…→rs_307c0f6d` 四连，读起来像同一消息内连续 | 连发真实存在、引用 id 亦真实（`rs_resp_…` 前缀），但所有 ≥3 连均**跨 turn 边界**；单 turn 内最长 2 连。跨 turn 链才是截图连发的主导机制（§3.2、§4.2） |
| 2 | 旁证「leader 第一轮曾被中断：首个 exec 无 item_completed，同命令下一 turn 重发」 | 证伪。`call_6502bece` 的 output 是 **exec 策略校验错误**（"``justification`` requires an explicit ``sandbox_permissions``…"），不是中断；重发（`call_bb7d8f41`）发生在**同一 turn**（a40d）内；全 session 无任何中断事件（§3.3、§6） |
| 3 | interrupted 可能经通道 2「wait poll 状态直映射」进入 | 通道**排除**：四个 poll 输出逐条核查只有 `idle/completed`。唯一剩余通道 = `subAgentActivity`（§6） |
| 4 | 「截图正好两张是因为 live 期持久化/懒拉取只覆盖部分 turn」 | 推测删除，改为硬证明：全 session 恰好只有 2 个含 collab item 的 turn（§5.3） |

## 2. 截图现象清单（glm-vision 全量走查，行为证据）

浅色主题。从上到下：

- 上一话题（光头强笑话）残尾 + 导航栏残留；
- `/goal` 用户消息（蓝等宽 `/goal`：四人各写 300 字故事，用 subagent，写入
  `/tmp/demo-plan127771.txt`）；
- goal bar「⌨ goal · 已完成」（单行折叠）；
- **思考卡 12 张**，分两簇：3 张 → 工具卡「已运行 执行 ›」→ 3 张；任务区
  中段 **6 张连续、中间无任何正文/工具**。12 张全部只显示「思考」二字，
  **无摘要文字**，每张上方带「💡 收起过程」行；
- 用时行 2 条（「用时 55 秒」「用时 1 分 15 秒」）、「已执行 3 个工具」行；
- 正文 7 段（「文件还不存在，现在按目标要求用子任务分别创作四人故事」「四个
  子任务已创建，等待它们完成」「林黛玉的故事已完成…」「宝钗也完成了…」
  「凤姐也好了，只差宝玉」「四人全部完成。现在把四个故事汇总写入」等）；
- **workflow 展开卡 2 张**：
  - **卡 A**：标题「请为《红楼梦》中的林… · 4 个成员」（=首个成员 label）、
    状态**已中断**（橙）、「已完成 2 · 已中断 2」、成员 🟢林黛玉已完成 /
    🟢薛宝钗已完成 / 🟠贾宝玉已中断 / 🟠王熙凤已中断；
  - **卡 B**：标题「**Subagents** · 4 个成员」（默认兜底名）、状态已完成
    （绿）、「已完成 4」、成员 **Agent-1/2/3/4**（占位名）全部已完成；
- 另有 **4 张折叠的锤子图标「任务已完成 ›」行**——不是 workflow run 卡
  （服务端只存在 2 个 run，均已展开可见），数据源待定位（§8）；
- 浮动 chip「48 个文件 +9182 −0」、输入栏遮挡末段正文（截图布局因素，非 bug）。

## 3. 上游真相（rollout 逐 turn 对账，生产落盘证据）

### 3.1 逐 turn 构成（item_completed 口径，共 13 turn / 25 reasoning）

| turn（短 id） | 构成 | 说明 |
| --- | --- | --- |
| `01a09123-4a75` | UserMessage + 1R + 1 AgentMessage | 笑话 turn（截图顶部残留） |
| `01a09125-618b` | 仅 1 Reasoning | **纯思考 turn** |
| `01a09125-a40d` | 3R + 2×exec_command | 首个 exec（`call_6502bece`）策略校验失败、无 CommandExecution item_completed（iOS 无卡）；同 turn 内修正重发（`call_bb7d8f41`）成功 |
| `01a09126-0377` | 仅 1 Reasoning | **纯思考 turn** |
| `01a09126-43f4` | 4R + 3 AgentMessage + 4×create_thread + 2×wait_threads | **spawn turn = 卡 A 的 fold** |
| `01a09127-1bd1` | 仅 1 Reasoning | **纯思考 turn** |
| `01a09127-2dca` | 仅 1 Reasoning | **纯思考 turn** |
| `01a09127-41ce` | 5R + 4 AgentMessage + 1 exec + 1 FileChange + 2×wait_threads | **卡 B 的 fold**（该 turn 无 create_thread） |
| `01a09128-67da` | 2R + 1 exec | |
| `01a09128-bdab` | 仅 1 Reasoning | **纯思考 turn** |
| `01a09128-eafb` | 仅 1 Reasoning | **纯思考 turn** |
| `01a09129-0591` | 2R + 1 exec | |
| `01a09129-8e9d` | 2R + 2 AgentMessage + update_goal | 收尾（写入文件） |

### 3.2 思考连发链（item_completed 事件流，带 turn 标注）

rollout 重放顺序（response_item）里 25 个 reasoning 互不相邻；连发出现在
**item_completed 事件流**里，且每条 ≥3 连的链都跨 turn 边界：

- 3 连 `[618b]rs_resp_0427d5… → [a40d]×2`
- 3 连 `[a40d]rs_resp_b5643d… → [0377]rs_resp_e89202… → [43f4]…`
- **4 连 `[43f4]rs_resp_791c520c… → [1bd1]rs_resp_186af311… → [2dca]rs_resp_e8bcb7d8… → [41ce]rs_resp_307c0f6d…`**（初版引用的正是这条，链跨 4 个 turn）
- 2 连 `[41ce]rs_resp_f906d9… → [67da]…`
- 4 连 `[67da]rs_resp_087b1f… → [bdab]rs_resp_059ae6… → [eafb]rs_resp_30cda1… → [0591]…`
- 2 连 `[0591]rs_resp_ee01a3… → [8e9d]…`

单 turn 内最长连发仅 1 处 2 连（a40d）。iOS turn 边界=消息边界，因此跨 turn
链在屏幕上表现为「一张思考卡紧贴下一张、中间无任何内容」——截图 6 连簇即
此类（纯思考 turn 相邻 + 多 R turn 的边界 reasoning 拼接）。

### 3.3 中断从未发生（多项独立证据）

- 事件面：`event_msg` 全量类型 = item_completed(49)/task_complete(13)/
  task_started(13)/thread_goal_updated(1)/token_count(25)，**无任何
  aborted/interrupted/cancelled 事件**；13/13 turn 有 task_started+task_complete 配对。
- 字符串面：leader 与 4 个子代理 rollout 各恰 1 处 "interrupt" 字样，且同句
  出现在系统提示模板（"Before sending a final response after a resume,
  interruption, or context transition…"）。
- 子代理面：四个子 rollout 各恰 1 个 `task_started` + 1 个 `task_complete`。
- poll 面：四次 `wait_threads` 输出逐条核查，thread/latestTurn 状态只有
  `idle` / `completed`。
- 唯一异常 a40d 首个 exec 已定性为 exec 策略校验错误（§3.1），与中断无关。

### 3.4 成员与子线程映射（全部实测）

create_thread 参数只有 `prompt`+`target`（`{"prompt":"请为《红楼梦》中的贾宝玉创作一段约300字…","target":{"type":"projectless"}}`），**无 `title`** →
折叠器成员 label 只能取截断 prompt。item_completed 完成顺序即 A 卡成员序：

| A 卡序 | 成员 | create_thread | 子线程 | 完成唤醒 | A 卡终态 | B 卡终态 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 林黛玉 | `call_b2042f1e` | `01a09126-ab7e` | poll#1 | 已完成 | 已完成 |
| 2 | 薛宝钗 | `call_75b3cec9` | `01a09126-b230` | poll#2 | 已完成 | 已完成 |
| 3 | 贾宝玉 | `call_d0ce54ad` | `01a09126-b6f7` | poll#4 | **已中断（伪）** | 已完成 |
| 4 | 王熙凤 | `call_57bec57f` | `01a09126-bead` | poll#3 | **已中断（伪）** | 已完成 |

wait_threads 共 4 次：poll#1/#2 在 spawn turn（43f4），poll#3/#4 在 41ce。
poll 结果是增量式的：poll#1 仅含 ab7e 条目、poll#2 含 ab7e+b230、poll#4 全量
4 条 completed——缺席条目在 fold 里保持默认（§5.2）。

## 4. 问题一：连续「思考」卡为何不合并不隐藏

### 4.1 机制 A：turn 内按 itemId 不合并（设计使然）

链路：codec 每个 reasoning item → `reasoning_delta`（带 itemId）→
`go-bridge/projection_reducer.go:1345-1389`：

```go
// Official reasoning item identity is an ordering boundary. A turn can
// alternate reasoning, commentary and tools; merging all deltas into the
// first reasoning part destroys that source order.
```

匹配规则（:1369-1375）：只追加到「itemId 相同」的 reasoning part；仅当
delta 的 itemId 为空时才并入第一个 reasoning part。iOS 侧 `SessionProjection.swift:722`
镜像同一按-itemId 归位逻辑。渲染层（message-web `SplitAssistantTurnRows.tsx`
按 part 拆行、每行一个 `ReasoningBlock`，`expandedReasoningIDs` 按 id 独立展开）
→ **一个 reasoning part = 一张「思考」折叠卡**。上游 25 段 = 25 个 part；
内容本身没有重复（昨日修的「首句双投」是 text 路径且已修）。

### 4.2 机制 B：跨 turn 纯思考链（复核新增的主导机制）

goal 任务没有用户逐轮驱动：leader 自主连跑 13 个 turn，其中 6 个 turn 除
reasoning 外无任何可渲染 item（§3.1）。这些 turn 在 iOS 上各渲染为只含一张
思考卡的 assistant 组，相邻即连发（§3.2 的跨 turn 链）。**turn 内合并（机制
A 的任何收敛方案）触碰不到机制 B**——它们分属不同消息。

### 4.3 官方对照与收敛方向

官方 Desktop 同类 goal 轮次**零张思考卡**（reasoning 完全不渲染），工具合并
为一条组行（对照截图 asset）。可选方向：

- **a. 官方对齐（最彻底，覆盖 A+B 两个机制）**：iOS 不渲染 reasoning 卡
  （或仅在手动展开的详情里可见）。产品语义上 reasoning 本就是过程性内容。
- **b. 相邻合并（仅解决机制 A）**：同一 assistant message 内相邻 reasoning
  part 合并为一个 part（内部保序拼接）。「相邻同类型」合并不破坏
  reasoning/工具交错的源顺序——reducer 注释担心的毁序只发生在无差别全轮
  合并。可在 bridge 侧（emit 时）或 iOS 渲染层做，iOS 层改动面更小。
  **注意：跨 turn 链（机制 B）需要另外处理**——iOS 渲染层把相邻
  reasoning-only turn 的思考行并组/折叠成一组，改动面大于 b。
- **c. 维持现状**：仅当 owner 认为过程可见性优先于整洁。

另一独立小问题（截图事实）：思考卡折叠态**无摘要文字**，只有「思考」二字，
信息量低于官方（官方完全不显示）。若走 b/c，折叠态应考虑显示首句摘要。

## 5. 问题二：workflow 卡为何出现两张

### 5.1 双卡机制（代码实锚）

`agent/codex-remote/collab_workflow.go`：

- :3-8 文件头注释：「first spawn anchors the card, later operations update
  the same members in place」——**一卡意图**；
- :261-288 `foldCollabWorkflow`：`key := params.ThreadID + "\x00" + params.TurnID`
  （:265，**按 turn 建折叠器**）；`newCodexCollabWorkflowFold(turnID)`（:34-39）
  → runID = `"codex-collab:" + turnID`，name 默认 `"Subagents"`；
- `go-bridge/projection_reducer.go:96-103` `workflowPending` 按 **runId** 键；
  :533-555 `upsertWorkflowPart` 按 workflowId **原位替换**（注释："official
  workflow-run keyed chat node parity: one card per run, updated in place —
  never a second card"）。

即：**one card per run 的语义是真的，但我们的 run 划分是 per-turn**。同一
逻辑运行（4 子代理从 spawn 到全部完成）被切成 N 个 run → N 张卡，各在自己
turn 的 assistant message 里。实现与自身注释意图不符，这是本问题的根。

### 5.2 两张卡的逐点解剖（与截图及 rollout 数据完全吻合）

- **卡 A**（fold of spawn turn `01a09126-43f4`）：
  - 见到 4 个 `create_thread`（完成顺序 b2042/75b3/d0ce54/57bec）→ 成员按
    完成序建立，label=截断 prompt；首个完成（林黛玉）把 fold name 从
    "Subagents" 改写为自己的截断 prompt（:155-157）→ 截图标题
    「请为《红楼梦》中的林…」✓，成员序 林黛玉/薛宝钗/贾宝玉/王熙凤 ✓。
  - 该 turn 内的 poll#1/#2 是它最后的数据观察：poll#2 只含 ab7e/b230 两个
    completed 条目 → 林黛玉/薛宝钗=已完成，贾宝玉(b6f7)/王熙凤(bead)缺席
    → 保持 running。
  - 随后（live 独有的）`subAgentActivity` 事件把 b6f7/bead 翻成 interrupted
    （:202-203 直映射）——**卡 A 能显示 interrupted 本身证明这些 activity
    被归属到了 spawn turn 的 fold**。fold 冻结于「已完成 2 · 已中断 2」✓。
- **卡 B**（fold of `01a09127-41ce`）：该 turn **无 create_thread**，fold 的
  唯一成员来源是 wait 分支（:163-181）`upsertChild(threadID, "", status)`
  ——label 恒为空 → `collabMemberLabel("", seq)` 兜底 **Agent-N**（:52、
  :126-128），fold name 永远停在 "Subagents"（:37、:253）。它读到 poll#3/#4
  的全量状态 → 4/4 已完成 ✓。
- 结构性后果：per-turn fold 的终态是**冻结态**，后续真相只进新 fold——即使
  interrupted 从未发生，卡 A 也会停在其最后一次观察（2 完成 2 进行中）。

### 5.3 为什么截图恰好两张（硬证明）

全 session **恰好只有 2 个 turn 含 collab item**（§3.1：43f4 与 41ce；纯思考
turn 无任何 collab item，`subAgentActivity` 只更新既有 fold 的成员、不能在
空 fold 上建卡——:59-65 `observe` 返回 false until a real spawned child
exists、:187-195 `observeActivity` 只更新已存在成员）→ 恰好 2 个 run → 恰好
2 张卡，与截图一致。无需借助「持久化/懒拉取只覆盖部分 turn」的假设（初版
推测已删除）。

### 5.4 附：4 张折叠「任务已完成 ›」行不是 workflow 卡

截图另有 4 张折叠锤子行「任务已完成 ›」（位置穿插在「四个子任务已创建」
「林黛玉的故事已完成」「宝钗也完成了」「凤姐也好了」等正文之后）。服务端
只存在 2 个 workflow run（均已展开可见），故这 4 行不是 run 卡；字面量在
message-web src 与 Swift 源码中均未检索到（可能在预构建 bundle 或为组合
文案）。数据源定位列入 §8，不阻塞两个主问题的修复。

## 6. 「已中断 2+2」伪态定案

已证伪：中断在本 session 从未发生（§3.3 四项独立证据）。

interrupted 进入卡 A 的通道分析：

1. ~~`wait_threads` poll 状态携带 interrupted~~——**排除**（§3.3 poll 面）。
   `codec.go:446-475` `remoteCollabMemberStatus` 对官方词表的直映射本身没错
   （interrupted→interrupted，:462-463），只是本 session 的 poll 数据从未
   含该值。
2. **`subAgentActivity` kind=interrupted**（`collab_workflow.go:187-212`
   直映射）——**唯一剩余通道**。该流 live 独有、不落盘，事后无法复核原始
   payload。

被翻转的成员精确锁定：b6f7(贾宝玉)/bead(王熙凤) = 卡 A 最后一次 poll（#2）
中缺席的两员 = 当时唯一尚未完成的两个子代理；翻转时窗在 poll#2 之后、卡 A
冻结之前。**为什么 app-server 会对实际全部完成的子代理广播 interrupted
activity**（或是否存在其他被我们映射成 interrupted 的 kind）仍未知——当前
日志级别不落这些载荷，需实现 agent 下次复现时 dump 定案（§8）。

另：初版旁证「leader 首轮被中断」已证伪（§1.1 #2），该假设不再作为
interrupted 来源的解释。

## 7. 修复方向（供决策，本轮不写码）

> **状态更新（2026-09-12 同日）**：§7.1–§7.3 已实施落地，详见 §10 实施记录。
> §7.4（思考卡收敛）仍为 owner 选型项，未实施。

1. **runID 跨 turn 稳定（推荐）**：fold 键从 `threadID+turnID` 改为 thread 级
   或「首个 spawn 批次」级（如首个 create_thread call_id / 最小子线程集合），
   后续 turn 的 wait/activity 更新**合并进同一 run** → reducer 按 runId 原位
   替换天然收敛成一张卡，这正是 reducer 注释宣称的 official parity。需同步
   解决：wait-only turn 首触时如何找到既有 run（按 target threadIds 反查）、
   fold 的生命周期与清理点。回归锚点：43f4+41ce 两个 turn 的同批
   create_thread + wait 只出一张卡，终态 4/4 completed，成员名保留 spawn 时
   解析的截断 prompt。
2. **interrupted 语义防御（顺带）**：poll 证据与 activity 证据冲突时以更新的
   poll 全量状态收敛（后继 poll 显示 completed 的成员不再保持 interrupted）；
   根治靠 §6 的 shape 定案后决定 kind→status 映射。
3. **成员 label 兜底改进（顺带）**：卡 B 暴露了 wait-only 路径永远拿不到名字
   ——run 合并后此问题自然消失；若保留多 run，upsert 时应按 childID 反查
   既有 label 而非落 Agent-N。
4. **思考卡**：按 §4.3 选型。a（官方对齐，不渲染 reasoning）同时覆盖 turn 内
   与跨 turn 两个机制；b（相邻合并）只解决 turn 内，跨 turn 链需 iOS 渲染层
   另做并组/折叠；折叠态无摘要问题独立处理。

## 8. 给实现 agent 的核查清单

- [ ] 复现：重跑同款 goal 四子代理任务；冷开前后各截一次，对照卡数（预期：
      思考卡数=可见 reasoning part 数；workflow 卡在同批 create_thread 跨 ≥2
      turn 时只应有一张）。
- [ ] dump 原始 `subAgentActivity`（kind/status/turnId/threadId）与 wait poll
      全文：定案谁在最后 poll 之后对未完成子代理发出 kind=interrupted；
      确认 activity 的 TurnID 归属是否= spawn turn（卡 A 现象所隐含）。
- [ ] 定位截图 4 张折叠「任务已完成 ›」锤子行的渲染组件与数据源（message-web
      src 与 Swift 均未命中字面量；查预构建 bundle / 组合文案 / goal-task 行）。
- [ ] 实现 §7.1 后回归：终态 4/4 completed、成员名保留截断 prompt、
      interrupted 伪态不再冻结（§7.2 防御生效）。
- [ ] 关联：`.workbuddy-ai/memory/2026-09-11.md`（CPU 排查起因）、
      `handoffs/handoff-20260911-2334.md`（昨日修复域）、
      `docs/2026-09-11-codex-remote-goal-live-transcript-vanishes*.md`。

## 9. 附：昨日修复在本截图的验证

- `/goal` 命令卡：蓝等宽 `/goal` 完整保留在用户气泡内（冷重建后未丢）✓
- goal bar：「⌨ goal · 已完成」在位 ✓
- 四成员 workflow 详情可见（卡 A 展开、成员名可读）✓

## 10. 实施记录（2026-09-12 同日，§7.1–§7.3）

Mac 侧 `plan/approval-layer` @ `1062d9d` + 本轮未提交改动（`agent/codex-remote/{collab_workflow,codec,history,history_paginated,agent}.go`、`core/interfaces.go`、`go-bridge/turn_detail_batch_engine.go`）。iOS 无需改动（workflow part 形状不变，仅 run 稳定性与状态语义变化）。

1. **跨 turn 折叠注册表（§7.1）**：`collab_workflow.go` 新增 `collabFoldRegistry`
   （`current` per-thread + `byChild` 全局路由索引）。只有 spawn 建新 run
   （runID=`codex-collab:<spawnTurnID>`，锚定 spawn 回合并写进
   `anchorTurnID`）；已知 child 的 wait/activity/close 按 `byChild` 路由到
   所属 run；spawn 加入运行中的 run（成员追加），旧 run 已 settle 后的新
   spawn 另开新卡（`open()` 释放旧 child 路由）；wait 类对全未知 child 保留
   「到达回合兜底建卡」（codec 重启/详情先于 spawn 回合拉取时的降级，与既有
   测试语义一致）。**runId 由锚定回合派生、与注册表代际无关**：任意注册表
   状态重放同一批官方 item 都收敛到同一 runId。
2. **事件锚定**：live 事件 `TurnID` 改发 fold 的 `anchorTurnID`（不再是观察
   回合）。reducer（`projection_reducer.go:1095-1140` workflow_run case）的
   runId 跨源首回合持有规则负责把卡锚在 spawn 回合的消息里原位更新。
3. **wait 缺席防御（§7.2）**：`observeAppTool` wait_threads 分支改为
   「poll 点名的 child 以点名状态为准（可覆盖 activity 造成的 interrupted）；
   已知成员缺席不动（官方 poll 累积语义）；未知 child 仍入卡 Running」。
4. **冷路径会话级注册表**：`history.go`/`history_paginated.go` 全部映射面
   （全量历史、分页历史、`ReadUpstreamHistoryPage` 懒历史页、
   `ReadTurnDetail`、batch engine 逐回合懒详情）共享 `Agent` 上按会话的
   `remoteCollabHistoryFolds`（含互斥锁，防跨回合写 anchor turn parts 与该
   turn 自身追加竞态）；跨回合更新写入锚定 turn 的 parts；锚定 turn 不在本
   次映射窗口时 part 落在观察回合但保留锚定 runId，由 reducer 合并。
   `core.TurnItemsSessionMapper` 新增可选接口，engine 优先断言使用。
5. **测试**：新增 5 个用例（live 跨 turn 单卡 + 成员名保留、poll 点名覆盖
   interrupted / 缺席不重置、批次更替新卡、冷全量跨 turn 折叠进 spawn 回合、
   懒详情会话感知合并）；既有 14 个 collab/cold 用例全部保持通过；
   `go build ./...` + `go-bridge` 全量（72s）+ `core` + `agent/codex-remote`
   全部通过。上轮修复的「EOF 整值快照延迟落盘」（batch engine）不受影响。
6. **已知残余（记录在案）**：懒历史页按最新优先翻页时，若 spawn 回合与
   wait 回合被分在两页且新页先到，wait 回合会先以兜底建卡（runId 锚定
   wait 回合），spawn 页到达后仍是两张卡——与修复前行为一致的重放边界，
   常见同页/升序场景已收敛；`subAgentActivity` 原始 shape dump（§8 第 2 项）
   仍待下次复现时取证。
