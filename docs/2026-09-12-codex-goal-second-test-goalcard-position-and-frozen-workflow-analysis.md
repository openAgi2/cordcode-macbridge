# Codex Desktop Goal 第二轮真机测试归因（/goal 卡错位 · 思考卡堆叠 · workflow 全员「已中断」）

Date: 2026-09-12（17:07 真机测试）
Status: analysis（只分析不改码；修复方向见 §6，供决策）
触发: owner 复测昨日修复（跨 turn workflow 单卡 + 伪中断防御），截图
`/Users/jacklee/Downloads/Scrollie_20260912_170729.jpg`。三个现象：
① /goal 命令卡「跑到中间」、任务流式输出直接接在上一个回复后面；
② 大量「收起过程」「思考」；③ workflow 成员全部「已中断」，真假未知。

## 0. 来源清单（P0）

| 项 | 值 |
| --- | --- |
| MacBridge 源 | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` @ `a7399af8`（昨晚修复 `8c98dbb` 已含；此后并行 web-push 提交不涉及本域），另有 1 个未提交的 catalog-scan 计划文档修改（非本任务） |
| iOS 源 | `/Users/jacklee/Projects/cordcode-ios-plan-approval` @ `4a09fcec`（含 `153bee70` goal 卡定位/拆组合并修复）；**真机安装构建是否包含 `153bee70` 未证实**（devicectl 只能读到 CordCode 1.0.0 build 1，读不到安装时间） |
| 运行态 | runtime PID 64465，16:55:00 自 `/Applications` 启动（含昨日修复符号）；8777 由其监听 |
| goal 会话 | leader `01a09410-c870`（rollout 13:22 起，9 个问答 turn 后 16:59:55 设 goal 并自动开跑 goal turn `01a094d8-4065`，17:06:14 完成） |
| 子代理 | `01a094d8-df43/dfca/e081/e172`（17:00:36 创建，四故事：贾宝玉/林黛玉/薛宝钗/王熙凤） |
| 上游真值 | `~/.codex/sessions/2026/09/12/` 上述 rollout + go-bridge.log 16:55–17:07 |

## 1. 结论摘要（TL;DR）

1. **「已中断」是伪态，官方铁证四个子代理全部成功完成**：四个子 rollout
   各恰 1 个 `task_started`+`task_complete`、零 interrupt 事件；leader rollout
   里 4 个 `wait` 与 4 个 `close_agent` 的 `agentsStates` 全部是
   `{completed: "<300字故事全文>"}`。截图（glm-vision 全量走查）确认面板
   显示「已中断 4/4」与同屏正文「四篇全部完成」「四个子代理均已关闭」、
   goal bar「已完成」直接矛盾——显示层的中断不是真实执行状态。
2. **好消息（昨日修复生效的部分）**：workflow 卡**只有一张**（昨日双卡
   已收敛），成员名是真实 spawn prompt 截断（非 Agent-N 占位），卡锚定在
   goal 轮内 spawn 位置附近——跨 turn 单卡与 label 保留均符合预期。
3. **今天的 goal 走了与昨天不同的原生通道**：`collabAgentToolCall`
   （spawn_agent ×4 + wait ×4 + close_agent ×4），**没有任何 MCP
   create_thread/wait_threads 调用**。昨日修复的「wait_threads poll 点名
   定状态」防御在这条通道上不存在用武之地。
4. **卡片终态停在「已中断 4/4」的诱因链**：17:00:36 spawn → 17:00:38
   `pairing stream lost; reconnecting` → `BindClient` →
   `ResetNativeSessionState` **清空 codec 的跨 turn fold 注册表**（昨日
   修复引入的存活周期）→ 后续 wait/close/终态事件与重建后的 fold 交互
   失常，最后一条 wire 以全员 interrupted 收尾且再无修正；官方真值侧
   wait/close 全是 completed。wire 上确切哪条事件写入 interrupted 仍是
   live-only 取证缺口（§3、§7）。
5. **/goal 卡错位（截图定案）**：命令卡落在**整段 goal 输出之后**（紧贴
   goal bar），goal 输出直接接在上一个回复后面——与 Mac reducer 的
   「cmd:codex-goal 合成 turn 按到达顺序 append 到 Turns 尾部」完全一致；
   iOS `153bee70` 的按开始时间插入未起作用，真机安装是否包含该修复
   未证实（若未装即旧 bug 原样复现）。
6. **思考卡堆叠（截图定案）**：25 组「收起过程/思考」，其中 **23 组连续
   出现在 goal bar 之后**（近半屏、组间无任何内容）——goal 轮 15 个
   reasoning part 的逐 part 渲染 + 轮内拆分组未合并的尾部堆叠，属昨日
   §7.4 悬置选型的预期未修复表现。

## 2. 上游真值（rollout 实测）

### 2.1 goal 会话时间线（01a09410）

| 时刻（本地） | 事件 |
| --- | --- |
| 13:22–16:57 | 9 个问答 turn（fbc5…d5-8210，最后一个 16:57:11 完成） |
| 16:55:00 | runtime 重启（本会话无影响，当时 goal 尚未创建） |
| 16:59:55.000 | goal 创建（`createdAt`，objective 同昨日四故事任务） |
| 16:59:55.399 | goal turn `01a094d8-4065` `turn_context` |
| 17:00:04 | iPhone 打开会话（`set_observation_scope`，冷重建+attach 运行中轮） |
| 17:00:19/25/34 | 三次冷拉 turn 4065 前缀（只有 agentMessage/reasoning） |
| 17:00:36 | 4 个 `spawn_agent`（children 仅出现在 `agentsStates`，`receiverThreadIds` 为空） |
| 17:00:38 | **`pairing stream lost; reconnecting`**（17:00:44 stale stream_id 丢弃） |
| 17:02–17:03 | 四子代理完成（子 rollout `task_complete`） |
| 17:02–17:05 | leader 的 wait ×4、close_agent ×4 完成（states 全 `{completed}`） |
| 17:06:14 | goal turn `01a094d8-4065` 完成 |

### 2.2 官方从未中断

- 四子 rollout：各 1×task_started + 1×task_complete，grep `interrupt` 零命中
  （"interrupt" 字样仅存在于系统提示模板）。
- leader rollout 12 个 collab 项全 `status=completed`；wait/close 的
  `agentsStates` 每项都是 `{completed: 故事全文}`（tagged union）。

## 3. 问题三：workflow 全员「已中断」的机制

iOS 侧「已中断」只有一个来源：wire 值 `interrupted`（
`WorkflowRunBlock.tsx:45` 状态表直映射，无本地推导）。Mac 产生
`interrupted` 的通道只有三条：官方词表 `interrupted`（agentsStates）、
`subAgentActivity kind=interrupted`、wait_threads poll（本轮不存在）。

**可证部分（冻结链）**：

1. 17:00:36–38：spawn 项建立 fold（4 成员，pending_init→running），卡片以
   「4×running」发出（锚定 turn 4065，runID=`codex-collab:4065`）。
2. 17:00:38：pairing 流断开 → `activateStream`（ws.go:273）→ `BindClient`
   （session.go:159-166）→ `ResetNativeSessionState`（codec.go:104-110）
   → **`collabFolds` 整表清空**（昨日修复把 fold 生命周期从 per-turn 改为
   存活到显式重置——而 BindClient 就是那个显式重置）。17:00:44 的
   stale stream_id 丢弃证明新 epoch 已建立。
3. 重连后：wait/close 项的 children（=agentsStates keys）在空注册表中
   未知；wait 走「到达回合兜底建卡」但 **collabAgentToolCall 路径只有
   spawn 能建成员**（`observe` 的 spawn-only membership 是既有语义），
   兜底 fold 永远零成员；close 类按设计不建卡。若官方在重连重放时
   重发了 spawn item 通知，fold 可被重建（截图成员名=真实 spawn prompt
   截断，说明 spawn 状态确实到达过渲染链）。
4. 冷路径救不了：`items/list` 不回放 collab 项（§1 证据），冷重建无卡可折。
5. 17:06:14 goal 轮完成后，卡片维持最后一次 wire 状态。

**截图定案的 wire 终态**：面板整卡「已中断」+ 成员「已中断 4/4」——即
最后一条 workflow wire 携带全员 interrupted（iOS `WorkflowRunBlock.tsx:45`
只透传 wire 值，无本地推导；「已中断 4」子行来自 fold snapshot 的
hasInterrupted 聚合）。Mac 产生 interrupted 的通道仅三条：官方词表
`interrupted`（agentsStates）、`subAgentActivity kind=interrupted`、
wait_threads poll（本轮不存在）。由于官方 rollout 侧 wait/close 全是
completed，**「最后一条 interrupted wire」只能来自 live 独有事件**
（subAgentActivity 或重连窗口内携带中断态的重放），且它之后没有任何
修正事件到达 fold（连接重置 + 冷路径失明共同造成「最后一词定终身」）。
确切 shape 仍是取证缺口（§7 第 2 项）。

## 4. 问题一：/goal 命令卡「跑到中间」（截图定案：卡在整段输出末尾）

- 截图实况：goal 输出（13 段正文+面板+工具行+表格）渲染完后才出现
  `/goal` 命令卡，紧贴 goal bar 之前；「收到目标：…」正文直接跟在姚明
  笑话回复后面，中间没有任何用户命令——正是「流式输出接在上一个回复
  后面」观感的来源。
- Mac 侧：`projection_reducer.go:1232` 把 goal 快照转成
  `cmd:codex-goal:<createdAt>` 系统 turn，`upsertSessionCommandTurn`
  → `upsertTurn` **按到达顺序 append**。live 流里 goal record 在 goal 轮
  skeleton 之后处理 → 合成 turn 排在 goal 轮之后。冷重建路径有
  「按官方创建时间放回续跑回合之前」的修正（e577499 一族），**live 路径
  没有等价排序**。
- iOS 侧：`153bee70`（按开始时间插到模型回合之前）是排序修正的落点；
  createdAt 与 turn 4065 startedAt 仅差 0.4s，属同秒边界。截图显示该
  排序未生效——**真机构建是否包含 `153bee70` 未证实**（devicectl 只能
  读到 CordCode 1.0.0 build 1，读不到安装时间），若未安装即旧 bug 原样。
- 叠加因素：iPhone 17:00:04 才 attach（冷重建序）+ 17:00:38 重连后的
  重放序，初始渲染的 turn 顺序是「冷拉 turn + live 事件」混合产物。

## 5. 问题二：大量「收起过程」「思考」（截图定案：25 组，23 组尾部连续）

- 截图实况：共 25 组「收起过程+思考」，其中 1 组在第 1 轮末尾、1 组在
  goal 轮正文末尾（/goal 卡之前），**其余 23 组在 goal bar 之后完全连续**
  （近半屏、组间无正文/工具/用时行）；仅首组带「思考了 5 分 37 秒」。
- goal 轮 4065 内：15 Reasoning + 15 AgentMessage + 6 CommandExecution +
  12 collab 项交错，全部在同一 turn 内 → reasoning part 逐张渲染
  「思考」折叠行（昨日文档 §4 机制 A，修复选型 §7.4 仍悬置）。
- 23 组远多于 15 段 reasoning：多出的部分来自 iOS 对同一 turn 拆分的
  多个 assistant group 的尾部堆叠——正是 `153bee70`「把被跳过 group
  的内容并进保留项」要修的形状，同样指向装机构建未含该修复（或该
  修复对 live patch 序不生效）。
- 与昨天结论一致：这是表示层选型问题，不是数据错误；收敛方向仍是
  a（对齐官方不渲染 reasoning）/ b（相邻合并），owner 选型后实施。

## 6. 修复方向（供决策，本轮不写码）

> **状态更新（2026-09-12 同日晚）**：§6.1（a 重连存活 + b wait 按 states 重建）
> 与 §6.3（cmd turn 按 StartedAt 插入 + turn_started 真实开始时刻）已实施并部署；
> §6.2（collabAgentToolCall 通道冷路径失明，卡片在全量重建后消失）与 §6.4
> （思考卡选型）仍开放。

1. **codec fold 对重连的存活语义（推荐，优先级最高）**：三个可选层——
   a. `ResetNativeSessionState` 不再清 `collabFolds`（fold 本身有
   settle/supersede 生命周期，跨连接 epoch 保留是安全的：runId 由锚定
   turn 派生，重放收敛）；b. 或 collabAgentToolCall 的 wait 在 children
   全未知且 states 携带完整成员状态时允许「按 states 重建成员」（把
   spawn-only membership 例外扩为「states 完整时 wait 可重建」）；
   c. 或 BindClient 后对活跃 fold 的会话做一次官方重放对账。推荐 a+b
   组合：a 保住不重连场景，b 兜住重连后事件。
2. **collabAgentToolCall 通道的冷路径失明**：官方 `items/list` 不回放该
   类型 → 冷重建永远无卡。需要在冷拉侧用 `wait_threads` 等价物或后台
   任务真值（`ListSessionBackgroundTasks` 已能从子线程元数据取状态）补
   折叠，或接受该通道「仅 live」并在重连后主动发一次终态对账（同 1c）。
3. **/goal 卡 live 排序**：Mac reducer 的 `cmd:` turn 改为按
   `StartedAt` 插入 Turns（对齐冷路径的按创建时间语义），不再依赖
   iOS 端补救；同时确认真机安装包含 `153bee70`（若未装，先装机再复测）。
4. **思考卡**：维持昨日 §7.4 选型待决策；本轮现象（单轮 15 连）属于
   预期未修复行为。

## 7. 核查清单（给实现/复现 agent）

- [ ] 确认真机 CordCode 构建包含 `153bee70`（装机时间/构建号），未装则
      先装机——/goal 卡错位的归因目前无法在装机构建未知的条件下二分。
- [ ] 复现时 dump 原始 `collabAgentToolCall`（spawn/wait/close 的
      item/started 与 item/completed 全载荷）与 `subAgentActivity`：
      定案 17:00:38 重连窗口内是否有携带 `interrupted` 的事件盖过快照
      （昨日与今日共同的取证缺口）。
- [ ] 复现「goal 运行中 kill runtime / 重连」场景：验证 fold 清空后
      卡片冻结；实现 §6.1 后验证重连不冻结、终态 4/4 completed。
- [ ] 关联：`docs/2026-09-12-codex-goal-duplicate-thinking-and-dual-workflow-card-analysis.md`
      （昨日归因与 §7.1–7.3 实施记录）、`handoffs/handoff-20260911-2334.md`。

## 8. 与昨日修复的关系（诚实归属）

- 昨日修复针对 MCP 通道（create_thread/wait_threads）的跨 turn 双卡与
  poll 防御，在该通道上仍然正确（单测覆盖）。
- 本轮暴露的是**同一折叠器在另一条官方通道（collabAgentToolCall）上的
  两个既有弱点**：① 该通道冷路径无官方回放（修复前就存在，昨天不涉
  及）；② fold 生命周期与 `ResetNativeSessionState` 的相互作用——昨日
  修复把 fold 从「per-turn 自然消亡」改为「跨 turn 存活到显式重置」，
  使重连清空从「无害」变成「卡片永久冻结」的诱因。这是昨日修复引入的
  新脆弱点，需要在 §6.1 收口。

## 9. 第三轮真机测试结果与决策记录（2026-09-12 18:0x，owner 授权自行决策 §6.2/§6.4）

### 9.1 验收结果（38ce43c + iOS 153bee70 装机后）

- ✅ /goal 命令卡位置正常（用户气泡下方、输出之前，截图 18.08.34）。
- ✅ workflow 卡正常（单卡、真实 prompt 标题、运行中 2+2 → 完成）。
- ✅ goal bar / 「进行中的目标」进度卡在 live 视图正常。
- ⚠️ 任务完成后尾部出现成串「收起过程/思考」折叠行（截图 18.09.27，6 对，
  全折叠、无摘要）——finalize 后 reasoning blocks 拆分渲染所致。
- ⚠️ 切走再切回：goal bar、goal 命令卡消失（正文/面板情况待确认）。

### 9.2 已实施决策

- **§6.4 → 选 a（官方对齐）**：iOS `33a32976`——codex-remote 的 reasoning
  blocks 在时间线快照构建层（MessageWebSnapshotBuilder）整体过滤，live/
  finalize/重载/快照恢复同路生效；「用时」入口的回合详情展开仍可见
  reasoning（显式查看途径）。Grok/DSH/Claude 不变。思考堆（含完成后的
  尾部堆叠）就此根治。
- **§6.2 → 暂缓**：collabAgentToolCall 通道冷路径失明的触发条件 = runtime
  全量内核重建（默认 120 分钟自动重启）与 iOS 冷开同窗；根治需要官方
  子线程元数据回填（thread/list 是否暴露 parent 归属未验证）。暴露面
  有限（卡片不出现，正文不受影响），另立任务。

### 9.3 新开放项：切回后 goal 命令卡/goal bar 消失（iOS 侧重开渲染链）

已证事实：Mac 内核投影全程稳定（18:08:10 切走 → 18:09:09 切回，rev 289
不变、delta_at_head、无重建，cmd turn 与 CodexGoal 均在投影里）；live
视图渲染正常；丢失只发生在 iOS 重开路径。静态链路核查（switchSession
清空 → ProjOpen pull → renderProjectionFromStore → messages sink →
refreshGoalBanner，映射层覆盖 system/cmd turn）各环节齐备但结果丢失——
需设备端 fgTrace 复现定案。复现配方：跑一个 goal 至完成 → 切走 → 切回
→ Console.app 过滤 `[GoalBanner]`/`[Render]`/`[ProjOpen]`/`[PIPE]` 抓取
（代码已有现成埋点，含 `[GoalBanner] refresh kindCodex=… codexGoal=…`
last-mile 诊断行）。次要观察：18.08.34 截图段落②行首有叠字重影
（渲染残留类，未处理）。

### 9.4 部署记录（本轮）

- iOS：`33a32976`（§6.4 过滤 + CHANGELOG；构建含 18:0x 全部前置修复），
  已装 iPhone 16 Pro（devicectl，org.openagi.cordcode）。
- Mac：无需变更（`38ce43c` 已于 17:52 部署，本轮无新 Mac 代码）。

## 10. 第四轮：goal 后输入栏面板失效（➕/⚙️/模型 点击即收起，2026-09-12 18:4x）

### 10.1 现象与证据

- 现象：codex 会话跑完 goal 后，编辑态输入栏的 ➕/⚙️/模型 点击后键盘收起、
  输入栏转空闲态、面板不弹。
- owner 发现的恢复路径：点导航「···」更多设置（弹窗会同时带起输入框与
  键盘），之后三个按钮恢复正常。
- 设备 fg-trace（Documents/fg-trace.log，devicectl 拉取）：goal 收尾渲染
  （10:36:41Z，[TimelineShape]/[PIPE]）之后到复现/恢复全程**零 UI trace**——
  输入栏呈现路径原本没有任何埋点，这是无法定案的根本原因。
- 旁证：PIPE 停在 `snapshotApplied rev=106 awaiting=107` 而 store 在
  syncRev=176（rs_resp_* 空 blocks item 恰在 rev 107 批次内）；但「···」
  恢复按钮时同样无任何 PIPE 活动——渲染管线缺口与按钮失效大概率是两个
  独立问题，缺口本身可能只是空闲会话未推送的无趣 rev。

### 10.2 主嫌疑（待埋点定案）

输入栏「视觉展开态」与「第一响应者/模式状态」在 goal 结束时失同步：
`setAwaitingUserAction` 内有 `dismissKeyboard()` + `applyComposerInteractionLock()`
（锁输入），`isGenerating` 切换驱动 `setGenerating/setAwaitingUserAction`
重建——若 goal 收尾把 composer 带入错误 awaiting/locked 态，僵尸态下按钮
点击走「归一化收起」而非面板呈现；「···」弹窗周期强制 layout/重聚焦后
状态复位。

### 10.3 已交付（iOS `6f71af01`）

整条链路埋点（行为零变化）：textViewDidBegin/EndEditing、setGenerating/
setAwaitingUserAction 状态迁移（含 dismissKeyboard 分支）、customizeTapped/
modelTagTapped 入口、模型 popover present 入口、权限后端 early-return 分支。
下次复现（无需任何操作）拉取 fg-trace.log 即可定位到具体行。

复现配方：任意 codex 会话跑一个 goal 至完成 → 唤起键盘点 ➕ → 若复现，
`devicectl copy from --domain-type appDataContainer --domain-identifier
org.openagi.cordcode --source Documents/fg-trace.log` 直接出定案坐标。
