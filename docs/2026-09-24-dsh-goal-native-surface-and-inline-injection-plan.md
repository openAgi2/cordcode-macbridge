# dsh-web /goal 命令原生双表面 + 上下文注入回合内联 方案

> 状态：**方案阶段修订稿 v3**（同日第 2 轮修订，未动代码。owner 2026-09-24 已裁决方向：
> 问题二选「官方对齐（内联进回合）」，问题一按「补原生渲染」走。本文档按方案评审纪律
> 送审，通过后才进入开发阶段。）

## 本轮（v3）改动范围声明

针对第二轮评审报告
`docs/2026-09-24-dsh-goal-native-surface-and-inline-injection-plan-review-r2.md`
（结论：不通过，1 项阻塞 B1 + 3 项建议）逐条亲自核实后修订。本轮**仅改本文档**；
未动任何产品代码、未 commit、未改 dsh 源码、未改 owner 会话数据（journal 样本只读）。
4 条意见（B1 + 建议 1–3）经逐处亲核 iOS/Mac 源码后**全部属实、全部采纳**，无不采纳项。
v2 对 r1 的处置表不再重复罗列——r2 评审报告 §1 已逐条复核确认其全部属实。正文改动集中在：

- §4.1 第 6 点：补齐 `AssistantRenderBlock` 新增 case 的**全部编译波及面**——
  `AssistantProcessPresentation.swift`（边界判定 + 展开层）与
  `MessageWebSnapshotBuilder.swift`（makeBlock）两个缺位文件的现状锚点与落点设计（B1）；
  block id 式修正为 `ctxinj-<seq>` 避免双前缀（建议 1）；路由检查过期注释补实施注记
  （建议 2）。
- **message-web 表面决策（B1 修订方向的二选一，本轮选 (b)）**：message-web 表面 busy
  注入行不渲染——`makeBlock` 仅加跳过 case 保编译，行为差异记入 §4.4/§7/§8 为
  recorded difference；不扩 `WebAssistantBlock` wire kind、不动 message-web 渲染器。
  理由与代价见 §4.1 第 6 点。
- §0 来源清单未跟踪文件写全（4 个）；§4.2 第 3 点限定为 Fix A 范围；§4.4 web 通道
  非目标精确化；§6 Mac 1 补既有回归对象（建议 3）+ iOS 补过程分段、web snapshot
  两条用例；§8 web 通道证据链补 `spec.blocks → makeBlock` 消费面并记录 recorded
  difference；§9 切片 3 文件清单补两文件。
- Fix A/Fix B 核心机制（journal 对齐内联、reducer 幂等、live/冷拉同 id、对账数字）
  **无变更**——B1 是 iOS 改动范围声明缺文件 + 两个产品决策缺位，不是机制设计错误。

### 逐条处置表

| # | 意见 | 处置 | 备注 |
| --- | --- | --- | --- |
| B1 | 新增 `AssistantRenderBlock` case 的编译波及面超出方案声明范围：同 target 三处无 default 穷举 switch（`MessageWebSnapshotBuilder.makeBlock` :611-750、`AssistantProcessPresentation.isProcessBoundary` :148-159、`buildExpandedItems` :193-222）必然编译失败；`MessageWebSnapshotBuilder` 属 web 通道，与 §4.2/§4.4「web 通道不动」矛盾；message-web 表面注入行存续与过程分段边界两个决策缺位 | **采纳** | 修订者逐处亲核属实：makeBlock 穷举六 case 无 default（awk 检索 :611-760 无 default，switch :750 闭合）；`makeAssistantTurn` :481-486 把 `spec.blocks` 全量 `.map(makeBlock)`；isProcessBoundary/buildExpandedItems 穷举无 default（userInput/workflow/subagentGroup→true 在 :154-155，防御跳过注释 :216-217，「未来声明为 independent presentation 的 block 同样在此扩展」注释 :145-147）；`buildSegments`（:112-143）被 `NativeTimelineRow.swift` :876（finalized 分段行派生）/:1020（streaming）调用。§4.1 第 6 点补两文件现状锚点+落点设计；message-web 决策选 (b)（理由见 §4.1 第 6 点）；§4.4/§7/§8 改写；§6 iOS 补第 5/6 条用例；§9 切片 3 补两文件 |
| 建议 1 | block id 双前缀：`\(ownerID)-ctxinj-<itemId>` 中 itemId 本身即 `ctxinj:<seq>`，实际形如 `…-ctxinj-ctxinj:94` | **采纳** | 亲核 history.go :276/:280（`fmt.Sprintf("ctxinj:%d", e.Seq)`）与 codec.go:521 同式，双前缀属实。id 式改为 `\(ownerID)-ctxinj-<seq>`（buildFromParts 与 buildFromOverlay 同步改），仍稳定唯一，不影响 streaming→finalize 行身份稳定目标 |
| 建议 2 | 路由检查注释 :291-293「只出现在 system turn」在 Fix B 后过期 | **采纳** | 亲核注释原文（:292-293「上下文注入行同命令行：只出现在 system turn，不占 text/reasoning/tool/file 桶，不强制切离 parts path」）。§4.1 第 6 点补实施注记：行为无需改（:291-294 break 不切离 parts path），注释实施时顺带更新 |
| 建议 3 | §6 Mac 1 可补列既有回归对象 `agent/dsh-web/streams_test.go:928-929`（「注入不得偷走 activeTurn」断言） | **采纳** | 亲核 :928-929 断言原文（`activeTurnID after injection … (injection must not steal the turn)`，断言注入后 activeTurnID 保持 `dshwTurnID("sess-ctxinj", 1)`）。§6 Mac 1 列为必须保持绿，与 handlers_projection_test.go:2442-2481 同级 |

## 0. 来源清单（P0）

| 项 | 值 |
| --- | --- |
| Mac 仓工作树 | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` |
| Mac 分支/提交 | `feat/ios-native-message-timeline` @ `d110022e2946ee1d6987661bda64b2d576401601`（无已跟踪文件修改；未跟踪 4 个：本文档、评审报告 r1/r2、`handoffs/handoff-20260923-2352.md`） |
| iOS 配套工作树 | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` |
| iOS 分支/提交 | `feat/ios-native-message-timeline` @ `5c737ebe9b0184512edcaa1725bf59dfb2d3d0e3`（干净） |
| 上游 dsh checkout | `/Users/jacklee/Projects/deepseek-harness` @ `master` / `00102833dfaee1da9f48a3a8eae9d34005a75218` |
| 官方 journal 真值样本 | `~/.dsh/sessions/--Users-jacklee-Projects-Chat--/session-daef20e4-f1c7-48dc-85eb-2d4e96dd5068/session.v4.jsonl.zstd`（本轮报障会话，151 条；解压副本 `/tmp/dsh-daef20e4-session.v4.jsonl`，只读取证） |
| 报障证据 | Mac 官方界面截图（`已粘贴 2026-09-24 00.04.05.tiff`）+ iPhone 16 Pro 截图（`Scrollie_20260924_000536.jpg`），均已识图核对 |

## 1. 背景与报障

owner 在 Mac dsh web 端（座位 `127.0.0.1:3080`）执行
`/goal 创作贾宝玉林黛玉薛宝钗王熙凤四人各自故事…写入/tmp/demo-plan0098121.txt`，
iPhone 16 Pro 原生时间线出现两个偏差：

1. **`/goal` 输入没有以用户消息气泡样式展现**：官方 Mac 界面是右对齐气泡 + 下方
   "goal · Goal created…" 折叠结果卡两个表面；iOS 渲染成一块左对齐灰字纯文本
   （`/goal …\ngoal\nGoal created\nStatus: active\n…` 全部连在一起）。
2. **回合中间注入的上下文行堆在消息结尾**：5 条「上下文注入 · subagent-settled」
   全部排在最终 assistant 消息之后；官方 journal 里它们位于最后一个回合中间的
   step 边界（owner 原话「其实这堆东西是在消息中间注入的」）。

owner 裁决：问题二按**官方对齐（内联进回合）**修；问题一按**补原生渲染**修。

## 2. 官方锚点（上游 @ 00102833，全部实读）

### 2.1 `/goal` 命令 = 两个官方表面，journal 无 user/message

- **journal 真值**（session-daef20e4）：`/goal` 输入**没有** `user/message` 记录，
  只有 seq 26 `command/run cmd=goal args=创作贾宝玉…` + seq 27 `goal/change` +
  seq 28 `command/done kind=success text="Goal created\nStatus: active\n…"`。
- **输入气泡**：`packages/client/ui-goal/src/client/goal-command-input.ts` —— 只有
  `name === 'goal'` 的 command/run 注册 `command-input` 投影；`goalCommandText()` =
  `"/" + name + args.trimEnd()`；`anchorSeq = run.seq - 0.1`（输入行排在结果卡
  之前）。渲染器 `GoalCommandInputView.tsx:12`（注释块 :11-16）注释原文 "Right-aligned
  `/goal` input bubble"：`/goal` 首 token 渲染为命令徽章（command chip），其余为目标纯文本。
- **结果卡**：`packages/client/ui-chat/src/client/chat/CommandNodeView.tsx` /
  `GenericCommandCard.tsx` —— 折叠披露卡，标题为裸命令名 + 状态，展开为结果正文
  （Mac 截图中 "goal · Goal created Status: active…" 即此表面）。
- **无 inputLine 的命令**（如 `permission`、codex `/compact`）：只有结果卡，无气泡
  ——`SessionProjectionMapping.swift:427` 已有同构注释（"Compact has no user
  bubble (inputLine nil)"）。本轮 journal seq 20-25 的 `permission` 命令对同理。

### 2.2 上下文注入 = journal 顺序内联的折叠披露行

- **journal 真值**：5 条 `user/message source{kind:"subagent-settled", form:"notice"}`
  位于 **turn 4（seq 71–149，最后一个 model 回合）** 内 step 边界：seq 94（3138efcb）、
  112（5f00ee6d）、123（98af7acb）、124（f7941941）、126（**3138efcb 第二次结算**）。
  turn 生命周期（v2 逐行复核）：turn 1 = seq 4/19、turn 2 = 30/55、turn 3 = 57/69、
  turn 4 = 71/149；5 条注入全部落在 71–149 区间，其紧邻 step 边界事件
  （seq 93/98/100/108/110/120/122/130）的 `data.turn` 均为 4（v1 误写 turn 3，
  本轮修正；「最后一个回合」即 turn 4，与 §1 表述一致）。另有 4 条
  `kind=agent-message form=relay`（seq 101/102/111/125，子代理消息转发）。
- **节点装配**：`packages/client/ui-chat/src/client/conversation-nodes/message.ts:45-108`
  —— `source.kind != "user"` 的 user/message 一律折成 `context` 节点
  （ContextMessageNode），**按 journal 顺序内联**（不挪尾、不归回合外）。
- **渲染**：`ContextInjectionRow.tsx:34-71` —— `DisclosureRow` 折叠行：图标 +
  title（`message.contextInjection`，中文「上下文注入」）+ producer label（kind）+
  summary；点击展开 form 对应的 body。iOS 现有文案
  `chat.contextInjection.title`（`Localizable.strings:506`）即官方同位文案。
- **结论**：官方**显示**这些注入行，位置在注入点。owner 曾提议「不显示」，已改选
  官方对齐；「不显示」不再作为选项。

## 3. 根因（本会话已逐层核实，带行号）

### 3.1 问题一：数据齐、原生通道渲染降级

- Mac 侧无缺口：`agent/dsh-web/codec.go:1179-1260`（applyCommandRun/Done，goal 专属
  InputLine 镜像 goalCommandText）→ `go-bridge/projection_reducer.go:412-452`
  （upsertSessionCommandTurn，`cmd:<id>` turn + System 消息 command part，且已有
  StartedAt 插入排序）→ iOS `Models/SessionProjectionMapping.swift:411-431`
  （结构化 `.command(MessageCommand)` 映射，fail-closed 词表校验）。
- 降级点：`App/ChatTimelineAdapterUIKit.swift:266-269` 把 item 显示文本取为
  `renderText(for:)` = `ChatTimelineCopyText.commandRowPlainText`
  （`App/ChatTimelineCopyText.swift:73-87`，输出形状 :85-86，即截图的
  `inputLine\ngoal\nGoal created…`）。该函数的设计用途是**复制/AX 纯文本降级**，
  原生 `.system` 行（`NativeTimelineRow.swift:707-718` + `TimelineRowStyle.swift:40,52`
  次要灰字）直接把它当显示文本。结构化 `item.command` 挂在 item 上但原生行从未
  用于样式。
- web 通道已有官方样式数据面（`MessageWebSnapshotBuilder.swift` makeCommandRow /
  makeContextInjectionRow → WebCommandRow / WebContextInjectionRow），原生通道未跟进。

### 3.2 问题二：turnless 独立行 + 纯追加排序 + 一回合一条目

- `agent/dsh-web/codec.go:513-527`：subagent-settled 分支**有意**发不挂 activeTurn 的
  独立行（注释理由：注入可发生在父会话任意状态），ItemID `ctxinj:<seq>`。
- `go-bridge/projection_reducer.go:1430-1462`：`ctx:<itemId>` turn 走
  `ps.upsertTurn` → 新回合**纯追加**（`:382`，到达序；对比 command turn 的
  StartedAt 插入排序）。
- 结构性根因：投影是**一回合一条目**。最后一个 model 回合（journal turn 4，
  seq 71–149）是 turns 数组的一条；注入行作为新条目追加在其后，该回合后续内容
  继续原位更新同一条目 → 注入永远落在回合整块之后。
  即使给 ctx turn 加 StartedAt 插入也无效（注入时间戳晚于回合开始，仍排整块后）。
- 冷拉同病：`agent/dsh-web/history.go:211-224`（flushTurn 后追加
  `pendingInjections`）+ `:267-291`（turn 进行中的注入缓冲到 flush 之后）。
- **内容无 bug**：iOS 5 行 ↔ journal 5 条 1:1（3138efcb 官方就结算两次）；只有
  位置与样式偏离官方。

## 4. 方案

### 4.1 Fix B：上下文注入内联进回合（Mac 三层 + iOS 渲染）

原则：注入的**归属与位置由官方 journal 位置决定**——turn 进行中到达 → 挂进该
回合 part 流（官方内联位）；turn 外到达 → 维持独立行（官方回合间位）。live 与
冷拉从同一 journal 推导，必须产出**相同 turnId + 相同 itemId**，reducer 幂等合并。

1. **codec（live）** `agent/dsh-web/codec.go` applyUserMessage subagent-settled 分支：
   `c.activeTurn != noTurn` 时 `EventContextInjection` 附 `TurnID = c.activeTurnID`
   （core.Event 已有 TurnID 字段，`core/message.go` Event struct）；idle（activeTurn
   == noTurn）维持现状独立行。turn 生命周期锚点：applyTurnStart/End（codec.go:375-405）。
2. **事件转换** `go-bridge/events.go:503-529`：`ev.TurnID != ""` 时向 data 增加
   `"turnId"`。
3. **reducer** `go-bridge/projection_reducer.go` `case "context_injection"`：
   - `turnId` 非空且 turn 存在 → 按 `(Type=="context_injection", ItemID)` 在
     `turn.Assistant.Parts` 内 whole-value upsert（**镜像 `subagent_part` 模式**，
     reducer:1866-1913：turn 定位 + Parts 追加/替换）；part 字段复用既有
     `ProjectionPart` context_* 字段（projection_types.go:141-145，注释 :133-140），
     无新字段。
   - `turnId` 为空、或 turn 不存在（防御性乱序）→ 走既有 `ctx:<itemId>` 独立 turn
     路径（降级不丢内容；乱序在 seq 单调保证下不应发生）。
   - 既有独立行路径与事件形状**不变**（老客户端前向兼容：老 iOS 的
     buildFromParts 对 assistant parts 里的 context_injection 已是防御跳过，
     不崩不显）。
4. **冷拉** `agent/dsh-web/history.go:267-291`：`acc.open` 时不再缓冲
   `pendingInjections`，改为向 accumulator 的 parts 追加
   `{"type":"context_injection", "itemId":"ctxinj:<seq>", "kind":…, "form":…,
   "summary":…, "text":…, "senderSessionId":…}` part map（落位=journal 顺序，
   与周围 text/tool part 同序）；idle 注入维持直接 append entries。
   `pendingInjections` 缓冲机制随之删除（仅此一处使用）。
5. **hydrate 转换** `go-bridge/handlers_projection.go`：turn entry 的 parts 分发
   （:1938 switch）新增 `case "context_injection"` → 发出带 `turnId` 的
   `context_injection` hydrate 事件（字段同独立 entry 路径 :2131-2160）；独立
   entry 路径不变。
6. **iOS 渲染**（v3 补齐 `AssistantRenderBlock` 新增 case 的全部编译波及面——同 app
   target 内三处无 default 穷举 switch，缺一即编译失败）：
   - `App/AssistantTimelineRenderSpec.swift`：block 枚举（:28-34）新增
     `case contextInjection(AssistantContextInjectionBlock)`；buildFromParts 的
     `case .contextInjection`（:547-553）从「防御性跳过」改为产出该 block
     （携带 injection 数据 + canonicalItemIDs；block id 沿用该函数既有
     `ownerID-<kind>-<稳定key>` 约定——`\(ownerID)-ctxinj-<seq>`，对位
     workflow/userInput block 的稳定 key 式 id。v3 修正：v2 式
     `…-ctxinj-<itemId>` 因 itemId 本身即 `ctxinj:<seq>` 会产生 `ctxinj-ctxinj:94`
     双前缀，改用裸 seq，仍稳定唯一）。路由检查（:291-294）行为无需改（break 不
     切离 parts path），但 :292-293 注释「只出现在 system turn」在 Fix B 后过期
     （assistant parts 也会出现 context_injection），实施时顺带更新（v3 按评审
     建议 2）。
   - **buildFromOverlay 同步覆盖（v2 补现状锚点 + 落点设计）**。现状：该文件
     `contextInjection` 仅两处（:291 路由检查、:547 buildFromParts 防御跳过），
     buildFromOverlay（:563-617）三段装配（reasoning parts 扫描 :571-577 /
     tool steps / narrative）对注入 part **无任何处理**——流式期（路由落 overlay）
     注入不可见，必须补，否则 §8 流式期风险成立。落点：在 buildFromOverlay 内
     新增一轮对 `group.parts` 的顺序扫描（与 reasoning 扫描同 pass 或紧随其后），
     遇 `.contextInjection` part 即 append 同形 `.contextInjection` block，**id 与
     buildFromParts 同式**（`\(group.id)-ctxinj-<seq>`），保证 streaming→finalize
     切换时行身份稳定、不重复；块位插在 tool steps 段之后、narrative 段之前
     （流式期 narrative 是持续增长的尾部，注入行置其上方避免随流式文本反复跳位；
     finalize 后由 buildFromParts 按 parts 序归位到官方注入点）。
   - `App/NativeTimeline/AssistantProcessPresentation.swift`（v3 补——穷举 switch
     缺位文件，`NativeTimelineRow` finalized 分段行派生 :876 与 streaming 路径
     :1020 所调 `buildSegments`（:112-143）的直接上游）：
     - `isProcessBoundary`（:148-159，穷举无 default）对 `.contextInjection`
       返回 **`true`**（对位 :154-155 userInput/workflow/subagentGroup 的
       independent 处理；注释按 :145-147「未来声明为 independent presentation
       的 block 同样在此扩展」既有风格补写）。§4.3 期望画面（注入行在回合中间、
       工具组之间各自成行）依赖此判定；若误选 `false`，注入 block 会被吞进
       process group（折叠摘要不含它、展开层防御跳过），成为一条内容丢失路径。
     - `buildExpandedItems`（:193-222，穷举无 default）加 case，按 :216-217
       同式「组内理论上不会出现（边界已 flush），防御性跳过不造块」处理。
   - `App/MessageWeb/MessageWebSnapshotBuilder.swift`（v3 补——穷举 switch 缺位
     文件，web 通道）：现状 `makeAssistantTurn` :481-486 把 `spec.blocks` **全量**
     `.map(makeBlock)`；`makeBlock`（:611-750）穷举六 case 无 default——新增
     case 后必须处理，否则编译失败。**本轮决策（评审修订方向二选一，选 (b)）：
     message-web 表面不渲染回合内注入**——`makeBlock` 对 `.contextInjection` 加
     跳过 case（注释标明 recorded difference），不扩 `WebAssistantBlock` wire
     kind、不动 message-web 渲染器。理由：web wire/快照契约与渲染器在本轮非目标
     （§4.4），扩 kind 属另一轮范围；且 busy 注入在数据形状上已不再是独立
     system turn，item 字段消费面（:396-397）自然不再产出行，`makeBlock` 跳过
     只是把该事实显式化。代价如实记录（§4.4/§7/§8）：message-web 表面 busy
     注入行由现状「结尾堆 5 行」变为**不显示**；idle 独立注入行仍经既有
     `makeContextInjectionRow`（:418）显示。原生表面（本轮主目标）5/5 全可见
     且位置=注入点，不受此决策影响。
   - `App/NativeTimeline/NativeTimelineRow.swift`：RowKind 新增
     `.contextInjection`；finalized 分段行派生与 streaming 平铺各接一行
     （`appendFinalizedIndependentBlockRow` :1355 起 / `appendLiveBlockRow`
     :1380 起两处穷举 switch 均在本文件清单内）。
   - `App/NativeTimeline/TimelineCardCells.swift`：新 cell——折叠披露行，对位官方
     `ContextInjectionRow`：图标 + 「上下文注入 · <kind>」+ summary 单行；点击
     展开 body（settle 原文）。**独立行（idle 注入）与回合内行共用同一 cell**，
     替换现状「独立行 = 纯文本 system 行」的降级（顺带修复独立行样式）。
   - 映射层零改动：`SessionProjectionMapping.swift:440-458` 已按 part 无角色约束
     映射 `.contextInjection(MessageContextInjection)`（fail-closed：kind 空或
     summary+text 双空跳过，Mac 侧同语义）。

### 4.2 Fix A：/goal 命令双表面原生渲染（iOS 单侧）

Mac 侧零改动（数据已结构化到端）。iOS 原生通道：

1. **行派生** `NativeTimelineRow.swift:707-718`：system item 且 `item.command != nil`
   时不再落 `.system` 纯文本行，改为产出：
   - `inputLine` 非空 → **右对齐命令输入气泡行**（复用 `.user` 行气泡样式，新增
     command-chip 变体：`/goal` 首 token 徽章化，其余目标文本纯文——对位
     `GoalCommandInputView`）；
   - **`.commandCard` 折叠卡行**（新 RowKind + cell）：标题 = 裸命令名 + 状态词
     （success/error/running，官方 locale 词），展开 = CommandText 原文；对位
     `GenericCommandCard`。
   - `inputLine` 为空（`permission`、codex `/compact`）→ 只有卡、无气泡（官方同构）。
2. **copy 不变**：`ChatTimelineCopyText.commandRowPlainText` 继续作为复制/AX 唯一
   宿主，golden 契约不动；显示文本不再借用 copy 降级。
3. **Fix A 不动 web 通道**（已有 WebCommandRow 渲染；Fix A 不新增
   `AssistantRenderBlock` case，无 `MessageWebSnapshotBuilder` 编译波及。Fix B 对
   web 通道的编译波及与 recorded difference 见 §4.1 第 6 点 / §4.4 / §8）。

### 4.3 交互走查（四拍，验收按此走）

打开输入的地方＝官方 dsh web 输入框（owner 在 Mac web 端操作）；输入＝`/goal` 命令
文本；发出去＝官方 `command/run` → journal → mux 直播到 iOS。**过程展示＝iOS 时间线
期望画面序列**：

1. 右对齐气泡：`/goal`（徽章）+「创作贾宝玉…写入/tmp/demo-plan0098121.txt」；
2. 其下折叠卡：`goal · 已完成`，展开见 "Goal created / Status: active / Objective…
   / Rounds: 0/256 / Activation: armed / Commands: …"；
3. 回合块照常（用时/工具组/文本）；
4. **回合中间**（官方注入点）出现折叠行「上下文注入 · subagent-settled」，默认
   收起、可展开 settle 原文；3138efcb 的两条各在其位；
5. 回合尾部：任务完成 + 最终消息；
6. **结尾不再堆任何注入行**。

### 4.4 非目标（本轮不做，防止范围蔓延）

- `kind=agent-message form=relay` 注入（4 条）维持 recorded difference 丢弃
  （owner 矩阵 §2.9；官方会渲染，本轮不扩）。
- `<goal_round>` / `tool-goal` 注入维持丢弃（goal 续跑触发的已知偏差）。
- 官方「继续执行目标」触发行（TurnTriggerNodeView）不在本轮（本轮报障未涉及）。
- web wire/快照契约与 message-web 渲染器不动（v3 精确化：`MessageWebSnapshotBuilder`
  仅因 `AssistantRenderBlock` 新增 case 加一处 `makeBlock` 跳过分支保编译，见 §4.1
  第 6 点决策 (b)）；**message-web 表面 busy 注入行不渲染**为 recorded difference
  （现状「结尾堆 5 行」→ 不显示；idle 独立行仍显示）——见 §8。Mac 官方界面、
  `/goal` 语义本身均不动。

## 5. 协议与双仓同步

- wire 无新字段：`context_injection` part 复用既有 ProjectionPart context_* 字段
  （projection_types.go:141-145），新增的只是**出现点**（assistant 消息 parts 内）。
  parts 的规范宿主是 `docs/protocol/bridge-v1.md`「Part vocabulary:
  `context_injection`」节（:1860-1892）；`bridge-v1-schema.md` 是 wire envelope 层
  对照表（247 行，无 parts/context_injection 条目），不承载投影 parts 定义
  （v1 引用对象不精确，v2 修正）。iOS mirror `SessionProjection.swift` 字段已备
  （:285-289）、无角色约束，**mirror 无需改动**。
- `docs/protocol/` 权威包：补 context_injection part 的 assistant-parts 出现点说明
  （Mac 权威 → iOS mirror 注释同步）；**同节现状句必须一并修订**——bridge-v1.md
  :1868-1871「reduces `context_injection` events into exactly ONE completed
  **system turn** per itemId」在实施后不再完整（busy 注入改为回合内 part，仅
  idle 注入落独立 system turn），不修订会使协议包新旧两句并存；`projection_types.go`
  注释（:133-140）同步。
- 协议兼容性：加法出现点 + 老客户端双层防御跳过（见 §8）= 无 major version 变化。

## 6. 测试计划（定向，按 D2/D3 分级；`GOTOOLCHAIN=local`）

**Mac（go-bridge + agent/dsh-web）：**
1. codec：busy 注入（activeTurn 存在）→ 事件带 TurnID；idle 注入 → 不带
   （streams_test.go 新增，fixture 用本轮 journal 形状）；既有「注入不得偷走
   activeTurn」断言（streams_test.go:928-929，v3 按评审建议 3 列入）必须保持绿。
2. reducer：turn-attached part upsert 幂等（live/cold 同 `ctxinj:<seq>` + 同
   turnId → 单 part）；turn 缺失降级独立行；既有 `ctx:` 独立 turn 回归。
3. history：mid-turn 注入落 accumulator parts 且位置=journal 序；idle 独立 entry；
   `pendingInjections` 删除后 flushTurn 行为回归。
4. hydrate 转换：turn parts 内 context_injection → 带 turnId 事件；独立 entry
   路径回归（handlers_projection_test.go 既有 context_injection 用例 :2442-2481
   必须保持绿）。

**iOS（配套工作树）：**
1. 映射：assistant parts 中 context_injection → block（新测试；既有 system-turn
   映射测试保持绿）。
2. 行派生：command inputLine 气泡行 + commandCard 行（goal/permission/compact 三
   形态）；注入行（回合内 + 独立）；位置断言（block 序 = parts 序）。
   **r3 建议 2 补记**：注入行 `text` 携带 settle 原文（长按拷贝/AX「显示可见
   即可拷到」）——行派生断言已覆盖（见 §8）。
3. 回归：`ContextInjectionTimelineTests`（独立行路径）、copy golden
   （`ChatTimelineCopyGoldenTests`）不变、受影响测试类定向全绿。
4. overlay 流式（v2 补，闭环 §8「流式期注入」要求）：路由落 buildFromOverlay 时
   注入 part → `.contextInjection` block **即时可见**；finalize 切回 buildFromParts
   后块序与 parts 序一致、行 id 同式稳定不重复。
5. 过程分段（v3 补，闭环 §4.1 第 6 点边界判定决策）：注入 block →
   `isProcessBoundary == true` → independent segment，**不被吞进 process group**
   （折叠摘要与展开层均不丢行）；`buildExpandedItems` 防御 case 不造块。
6. web snapshot（v3 补，按 §4.1 第 6 点决策 (b) 断言）：busy 注入 block 经
   `makeBlock` 跳过 → web assistant blocks 中注入行**缺席**（recorded difference
   如实断言，不冒充显示）；idle 独立注入行仍经 `makeContextInjectionRow` 出现在
   web 快照。
7. 搜索（r3 建议 1 补记）：回合内注入行可被会话内搜索命中，索引文本与
   `contextInjectionPlainText` 同源（`SessionSearchIndexTests` 新增断言）。

**真机（agent-device 常设授权，按配套工作树 `IOS_VERIFICATION_ENTRY.md`）：**
goal 轮端到端走查 §4.3 六步 + 对账数字（§7）。

## 7. 验收标准（对账数字，产物门）

- **注入对账**：官方 journal 5 条 subagent-settled ↔ iOS 回合内 5 行，位置=注入点
  （3138efcb 两次结算各一行）；`n/N = 5/5`。结尾堆行数 = 0。
- **命令对账**：官方 1 个 `/goal` 输入气泡 + 1 张结果卡 ↔ iOS 1+1；`permission`
  命令 1 卡无气泡。
- **独立行**：idle 注入（如有新样本）↔ 回合间折叠行。
- **message-web 表面（v3 决策 (b)）**：busy 注入行**不显示**（recorded difference，
  见 §8）——验收时如实核对缺席、不冒充显示；idle 独立注入行仍显示。
- **copy**：全文复制输出与现状逐字节一致（golden）。
- **回归**：上述定向测试全绿；无新增协议 major。

## 8. 风险与已知边界

- **流式期注入**：必须覆盖 buildFromOverlay（防 finalize 前不可见）；实施时以
  streaming 用例验证。
- **finalize 后 part 追加**：行高缓存/canonicalItemIDs 机制既有，需回归长会话。
- **降级路径**：turn 缺失时降级独立行（不丢内容、不造幽灵回合）——与
  fail-closed 纪律一致（不造数据，只降级展示位）。
- **SessionSearchIndex 消费面（r3 建议 1，实施补记）**：`SessionSearchIndex`
  的 `contentBlob` 遍历所有 group parts 不分角色（`searchableText()` 已有
  `.contextInjection` case，索引文本与 `contextInjectionPlainText` 同源）——
  Fix B 后回合内注入行自动进入正文索引、可被会话内搜索命中（良性预期伴生
  行为，无编译波及；§6 iOS 已加断言 `testSetContentIndexesContextInjectionParts`）。
- **新注入 cell 的拷贝/AX 语义（r3 建议 2，实施补记）**：行 `text` 携带 settle
  原文（`injection.text`，空回退 summary），长按拷贝与 AX 读取同源——对位既有
  `.system` 行语义，防「显示可见但拷不到」的语义回退（§6 iOS 2 行派生断言
  已覆盖：`testRowDerivation_InjectionRows_InlineDataStandalone`）。
- **老 iOS + 新 Mac**：assistant parts 里的 context_injection 被老客户端**双层防御**
  消化（不崩不显）：① buildFromParts `case .command, .contextInjection` 防御跳过
  （:547-553）；② `ChatTimelineAdapterUIKit.swift` 的 `commandRow(in:)` /
  `contextInjectionRow(in:)` 均 `guard group.role == .system else { return nil }`
  （guard 落 :235/:245），assistant item 的 command/contextInjection 字段恒 nil——
  因此 copy 路径（`fullResponseCopyText` 仅 system 角色消费注入参数，
  `ChatTimelineCopyText.swift:17-25`）与 web 通道的 **item 字段消费面**
  （`MessageWebSnapshotBuilder.swift:396-397`）均不受影响。该双层防御证明的是
  **老客户端**兼容，佐证 §4.2「copy 不变」与 §7「copy golden 逐字节一致」；
  **新客户端**的 web 通道还有第二个消费面——`makeAssistantTurn` :481-486 把
  `spec.blocks` 全量 `.map(makeBlock)`（:611-750 穷举无 default），由 §4.1 第 6 点
  决策 (b) 处理（跳过 case）。新 iOS + 老 Mac：无新 part，行为同现状。
  双向前向兼容。
- **message-web 表面 recorded difference（v3 决策 (b)）**：busy 注入在数据形状上
  不再是独立 system turn，message-web 表面的注入行由现状「结尾堆 5 行」变为
  **不显示**（`makeBlock` 跳过 case，§4.1 第 6 点）；idle 独立注入行仍经
  `makeContextInjectionRow`（:418）显示。原生表面（本轮主目标）5/5 全可见且
  位置=注入点（§7），不受影响；web wire/快照契约与渲染器不动。

## 9. 实施切片建议（开发阶段用）

1. Mac：codec TurnID + events.go turnId + reducer 双分支 + 定向测试；
2. Mac：history.go part 化 + hydrate case + 定向测试 + 协议包注释；
3. iOS：block/RowKind/cell（注入行 + 命令卡/气泡）+ 行派生（含 buildFromOverlay
   流式覆盖，按 §4.1 第 6 点落点）+ `AssistantProcessPresentation` 边界判定/展开层
   case + `MessageWebSnapshotBuilder.makeBlock` 跳过 case（v3 补两文件，按 §4.1
   第 6 点落点）+ 定向测试（含 §6 iOS 4/5/6 overlay 流式、过程分段、web snapshot
   用例）；
4. 双仓定向 build + 真机走查 + 对账收口（CHANGELOG/think.md 复盘）。
