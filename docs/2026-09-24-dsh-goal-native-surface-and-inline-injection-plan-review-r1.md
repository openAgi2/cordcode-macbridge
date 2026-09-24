# dsh-web /goal 原生双表面 + 上下文注入回合内联方案第一轮评审

- 日期：2026-09-24
- 对象：`docs/2026-09-24-dsh-goal-native-surface-and-inline-injection-plan.md`（方案阶段送审稿 v1，文档头部状态行「方案阶段送审稿 v1（未动代码）」），按未提交工作树内容评审
- **结论：不通过。** 1 项阻塞级意见：§2.2/§3.2 把 5 条上下文注入的 journal 归属回合写成「turn 3」，亲读 journal 样本证明是 **turn 4**（seq 71–149，最后一个 model 回合），且与 §1「最后一个回合」的表述构成文档内部矛盾。除此之外：本轮亲核官方源码锚点 5 处、Mac 仓锚点 15 处、iOS 仓锚点 13 处（共 33 处，超出要点要求的 12 处），内容全部属实（4 处轻微行号偏差见建议 2，不构成锚点错误）；§2.1/§2.2/§7 的全部关键对账数字（seq、条数、结算次数、form、sender）逐项与样本吻合；双仓接口一致性（part 字段复用、turnId 同式、`ctxinj:<seq>` 幂等键、live/冷拉同 id）、§5 协议前向兼容声明、四拍走查、纪律红线全部核过，无其他冲突。阻塞项修订仅涉及两处回合定位文字，不涉及任何设计变更。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `d110022e2946ee1d6987661bda64b2d576401601` | 2 个未跟踪文件：`docs/2026-09-24-dsh-goal-native-surface-and-inline-injection-plan.md`（送审对象自身）、`handoffs/handoff-20260923-2352.md`；无已跟踪文件修改 | 方案 §0 声明同一提交，一致；「干净，除未跟踪 handoff」的表述漏计送审文档自身（见建议 1） |
| iOS（配套） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `5c737ebe9b0184512edcaa1725bf59dfb2d3d0e3` | 干净（`git status --porcelain` 输出为空） | 与方案 §0 声明一致；本轮引用了该工作树源码作锚点核对（Mapping/Adapter/CopyText/NativeRow/RenderSpec/SessionProjection/Localizable/TimelineRowStyle/WebSnapshotBuilder） |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净（`git status --porcelain` 输出为空） | `git describe --tags --exact-match HEAD` 实得 `dsh-v0.1.7-alpha.2`，与方案 §0 声明一致；官方源码唯一来源 |
| journal 真值样本 | `~/.dsh/sessions/--Users-jacklee-Projects-Chat--/session-daef20e4-f1c7-48dc-85eb-2d4e96dd5068/session.v4.jsonl.zstd`（原始，117115 字节）；解压副本 `/tmp/dsh-daef20e4-session.v4.jsonl`（319944 字节，151 行，已存在，本轮只读） | — | — | 只读取证，未写入任何会话目录 | §2/§7 对账数字唯一真值来源；本轮以 python3 逐行解析核对 |

三项工作树均由本轮 `git rev-parse HEAD` + `git branch --show-current` + `git status --porcelain` 亲核。除写入本报告文件外，本轮未修改任何文件、未 commit。

## 阻塞级意见（1 项）

### B1：§2.2/§3.2 注入归属回合写「turn 3」，journal 真值为 **turn 4**；与 §1「最后一个回合」构成内部矛盾

**方案原文**：§2.2「**journal 真值**：5 条 `user/message source{kind:"subagent-settled", form:"notice"}` 位于 **turn 3** 内 step 边界：seq 94（3138efcb）、112（5f00ee6d）、123（98af7acb）、124（f7941941）、126（**3138efcb 第二次结算**）」；§3.2「turn 3 是 turns 数组的一条；注入行作为新条目追加在其后，turn 3 后续内容继续原位更新同一条目」。

**亲读样本证据**（`/tmp/dsh-daef20e4-session.v4.jsonl` 逐行解析）：

- journal turn 生命周期事件：turn 1 = seq 4（start）/19（end）；turn 2 = seq 30/55；turn 3 = seq 57/69；**turn 4 = seq 71/149**。
- 5 条 subagent-settled 的 seq 94/112/123/124/126 全部落在 71–149 区间；其紧邻 step 边界事件（seq 91 `step/end`、93 `step/start`、98、100、108、110、120、122、130）的 `data.turn` 字段**全部 = 4**。「位于 step 边界」属实（step/start 93→94、110→111/112、122→123/124/125/126），「step 边界」定位正确，只有回合编号错误。
- §1 写「官方 journal 里它们位于**最后一个回合**中间的 step 边界」——最后一个回合就是 turn 4，该句正确；§2.2 的「turn 3」与之矛盾（若 turn 3 是最后一个回合，则 turn 4 的存在被否定）。二者必有一错，对照样本后确定错的是「turn 3」。
- §3.2 的机制描述「注入行作为新条目追加在其后，X 后续内容继续原位更新同一条目」只对 turn 4 成立：turn 3 已于 seq 69 `turn/end`，seq 94 的注入到达时 turn 3 不再更新；注入到达时正在原位更新的是 turn 4。
- 「turn 3」也对照过 CordCode 投影口径：投影 turns 数组按到达序为 t1（seq 4）、`cmd:`permission（seq 20）、`cmd:`goal（seq 26）、t2（seq 30）、t3（seq 57）、t4（seq 71）共 6 条，turn 4 是 1-indexed 第 6 条 / 0-indexed 索引 5，同样不是「第 3 条」。能得出 3 的只有「排除 command turns 后 0-indexed 数 model 回合」（t1=0, t2=1, t3=2, t4=3）这一未在文档定义的口径，而 §2.2 标注的是「journal 真值」。

**为什么定阻塞**：本方案的核心主题就是「注入位置」，§2.2 是方案对官方 journal 位置的唯一「真值」陈述，其回合定位与样本不符且与 §1 相互矛盾；按产物门纪律（对账数字以官方 journal 为准）与「对账数字与样本不符 / 内部矛盾」两个阻塞类别，必须修正后重审。同时如实说明影响边界：全部 seq 数字、5/4 条数、3138efcb 两次结算、step 边界定位均正确；§4.1 修复机制（「turn 进行中到达 → 挂进该回合 part 流」）与 §7 验收（位置=注入点）不依赖回合编号，设计本身不受该错误影响——这是事实陈述错误，不是设计错误。

**修订方向**（可执行，两处文字，无设计变更）：

1. §2.2「位于 turn 3 内 step 边界」→「位于 turn 4（seq 71–149，最后一个 model 回合）内 step 边界」，可顺带写明 turn 区间增强可核对性；
2. §3.2「turn 3 是 turns 数组的一条」→「最后一个 model 回合（journal turn 4）是 turns 数组的一条」，或写明所用的计数口径。

## 建议级意见（不阻断，逐条可改）

1. **§0 来源清单 Mac 行「干净，除未跟踪 handoff」漏计送审文档自身。** `git status --porcelain` 实有两个未跟踪文件：`handoffs/handoff-20260923-2352.md` 与送审方案文档本身。建议写全（「除未跟踪的送审文档与 handoff」），避免来源清单与亲核输出不一致。
2. **4 处轻微行号偏差（引用内容全部属实，不构成锚点错误）。** 逐项如下：
   - `GoalCommandInputView.tsx:14`——"Right-aligned `/goal` input bubble" 注释短语实际在 :12（注释块 :11-16，:14 在块内）；command chip + 其余纯文本的语义（:23-25 head/rest 切分、:35-36 渲染）属实。
   - `projection_reducer.go:380`——新回合纯追加的 `append` 语句实际在 :382（:380 是已存在分支的 `return`）；「到达序追加、对比 command turn 的 StartedAt 插入排序」语义属实。
   - `ChatTimelineCopyText.swift:76-92`——`commandRowPlainText` 实际为 :73-87（引用区间覆盖函数主体）；输出形状 `inputLine\ngoal\nGoal created…`（:85-86）与 §1 报障描述一致。
   - `projection_types.go:133-134`——引用落在注释块开头，`ContextKind/Form/Summary/Text/SenderSession` 字段实际在 :141-145；「复用既有 context_* 字段、无新字段」属实。
3. **§5「`bridge_v1_schema` 对 parts 无角色约束」表述不精确。** `docs/protocol/bridge-v1-schema.md` 是 wire envelope 层对照表（247 行，grep "parts"/"context_injection" 均无命中），不含投影 parts 定义；parts 的规范宿主是 `bridge-v1.md`「Part vocabulary: `context_injection`」节（:1860-1891），其现状声明「reduces `context_injection` events into exactly ONE completed **system turn** per itemId」在实施后必须随 §5 的「assistant-parts 出现点说明」一并修订。建议 §5 直接点名 bridge-v1.md 该节，把「system turn 唯一出现点」现状句的修订列入交付物，避免实施后协议包出现新旧两句并存。
4. **§4.1 第 6 点 buildFromOverlay「防御性透传」缺现状锚点与落点。** `AssistantTimelineRenderSpec.swift` 全文件 `contextInjection` 仅两处（:291 路由检查、:547 buildFromParts 防御跳过），buildFromOverlay（:563 起）对注入 part 无任何处理——「同步覆盖」确为必要实施项（否则流式期注入不可见，§8 风险成立），但方案未写 overlay 循环内的具体落点（哪个循环、以什么 id 进 ordinal 序列）。建议实施切片补一段现状锚点 + 落点设计，降低实施期自行发挥的空间。
5. **§4.1 第 3 点前向兼容声明可补强为双层防御事实。** 老 iOS 对 assistant parts 里的 context_injection 实际有两层防御：buildFromParts `case .command, .contextInjection` 防御跳过（:547-553）之外，`ChatTimelineAdapterUIKit.swift:235/:245` 的 `commandRow(in:)`/`contextInjectionRow(in:)` 均 `guard group.role == .system else { return nil }`——assistant item 的 command/contextInjection 字段恒 nil，因此 copy 路径（`ChatTimelineCopyText.fullResponseCopyText` 仅在 `group.role == .system` 时消费注入参数，:17-25）与 web 通道（`MessageWebSnapshotBuilder.swift:396-397` 消费 item 字段）均不受影响。把这层事实写进 §8，可同时佐证 §4.2「copy 不变」、§4.2「web 通道不动」与 §7「copy golden 逐字节一致」三条声明的可行性依据。
6. **§6 iOS 测试计划缺 overlay 流式用例条目。** §8 要求「流式期注入：必须覆盖 buildFromOverlay……实施时以 streaming 用例验证」，但 §6 iOS 1-3 无对应测试条目。建议补一条「overlay 流式期注入可见性用例」，使 §6↔§8 互洽闭环（§6↔§7 的其余对应对账已核：codec busy/idle ↔ 注入对账、行派生三形态 ↔ 命令对账 1+1、copy golden ↔ §7 copy 项）。

## 已核实事项

### 1. journal 对账（评审要点 2，全部亲读 `/tmp/dsh-daef20e4-session.v4.jsonl`）

- 总条数 151 ✓（`wc -l` 实得 151，与 §0 声明一致）。
- `/goal` 无 user/message 记录 ✓：seq 26 `command/run`（`data.name="goal"`、`data.args=" 创作贾宝玉林黛玉薛宝钗王熙凤四人各自故事，每个人300字左右，用4个subagent写每个人故事，并写入/tmp/demo-plan0098121.txt"`）+ seq 27 `goal/change`（`operation:"create"`、`goal.phase:"active"`、`maxGoalRounds:256`）+ seq 28 `command/done`（`kind:"success"`、`text="Goal created\nStatus: active\nObjective: …\nRounds: 0/256\nActivation: armed\n\nCommands: /goal edit <objective>, /goal pause, /goal clear"`）。seq 26 前后无 /goal 的 user/message ✓；§4.3 期望画面第 2 步引用的卡片正文与 seq 28 text 逐段一致 ✓。
- permission 命令 seq 20-25 ✓：seq 20 `command/run name="permission"`、21 `permission/preset`、22 `sandbox/mode`、23 `approval/policy`、24 `agent/inbox/spliced`、25 `command/done`。
- 5 条 subagent-settled ✓：seq 94（sender 3138efcb-b5b7…）、112（5f00ee6d）、123（98af7acb）、124（f7941941）、126（3138efcb 第二次结算），全部 `kind="subagent-settled"` + `form="notice"` + 非空 summary——与 §2.2 逐项一致（除 B1 的回合编号）。
- 4 条 agent-message relay ✓：seq 101（5f00ee6d）、102（98af7acb）、111（f7941941）、125（3138efcb），全部 `kind="agent-message"` + `form="relay"`——与 §2.2 一致；§4.4「维持丢弃」与 codec 现状一致（`codec.go:529-539` default 分支 known-drop，注释明列 agent-message/goal_round）。
- seq 145 `tool-goal`、seq 35/60/74 `kind=goal`（goal 轮触发行）在 §4.4 非目标范围内，codec default 分支现状即丢弃，方案未对其做超出样本的声明 ✓。
- 本轮结论未超出样本证明范围；唯一超出样本的陈述即 B1 的「turn 3」（样本证明 turn 4）。

### 2. 官方源码锚点（评审要点 1，上游 `00102833dfaee1da9f48a3a8eae9d34005a75218` 亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `goal-command-input.ts`（name==='goal' 注册 command-input 投影；goalCommandText；anchorSeq=seq-0.1） | match :42、goalCommandText :34-36、anchorSeq :64 | ✓ 逐项吻合：`match: event => event.type === 'command/run' && event.data.name === GOAL_COMMAND`；`return \`/${event.data.name}${(event.data.args ?? '').trimEnd()}\``；`anchorSeq: context.state.seq - 0.1`（输入行排在结果卡之前的语义成立） |
| `GoalCommandInputView.tsx:14`（"Right-aligned `/goal` input bubble" 注释） | 注释块 :11-16，该短语在 :12 | ✓ 内容属实；行号轻微偏差（建议 2） |
| `CommandNodeView.tsx` / `GenericCommandCard.tsx`（折叠披露卡、裸命令名标题、展开正文） | GenericCommandCard :29-77 | ✓ `title = node.name ?? t('command.title')` + 注释「the title is the bare command name」；DisclosureRow + expanded body |
| `ui-chat conversation-nodes/message.ts:45-108`（source.kind != "user" 折成 context 节点、按 journal 顺序内联） | 定义 :46-112，关键分支 :62-83 | ✓ `if (event.data.source.kind !== 'user')` → `kind: 'context'`（ContextMessageNode）；buildViewNode 以 `context.state.seq` 为锚，即 journal 顺序内联 |
| `ContextInjectionRow.tsx:34-71`（DisclosureRow：图标 + message.contextInjection + producer label + summary、展开 body） | 组件 :31-71，DisclosureRow :38-70 | ✓ `title={t('message.contextInjection')}`（:44）、producer label（:45-51）、summary（:52-57）、form 对应 body（:66-68） |

### 3. Mac 仓锚点（15 处，`d110022e` 工作树亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `codec.go:1179-1260`（applyCommandRun/Done、goal 专属 InputLine 镜像 goalCommandText） | applyCommandRun :1179-1215、applyCommandDone :1220-1258 | ✓ :1201-1204 与 :1241-1246 goal InputLine；注释引用官方 goalCommandText |
| `codec.go:513-527`（subagent-settled 有意发不挂 activeTurn 的独立行、`ctxinj:<seq>`） | 分支 :508-528 | ✓ 注释「不挂 activeTurn——注入可发生在父会话任意状态」:512-513；`ItemID: fmt.Sprintf("ctxinj:%d", env.Seq)` :521；事件字面量无 TurnID |
| `codec.go:375-405`（applyTurnStart/End） | :375-389 / :391-437 | ✓ 引用区间覆盖两函数主体 |
| `projection_reducer.go:412-452`（upsertSessionCommandTurn、`cmd:<id>`、StartedAt 插入排序） | :412-461 | ✓ `turnID := "cmd:" + commandID` :417；System 消息 + command part :423-435；「Insert a NEW command turn by StartedAt, not arrival order」:441-455 |
| `projection_reducer.go:1430-1462`（`ctx:<itemId>` turn 走 upsertTurn） | case :1430-1462 | ✓ `turnID := "ctx:" + itemID` :1443；whole-value upsert |
| `projection_reducer.go:380`（新回合纯追加） | append 实际 :382 | ✓ 语义属实；行号偏差（建议 2） |
| `projection_reducer.go:1866-1913`（subagent_part 模式：turn 定位 + Parts 追加/替换） | case :1866-1912 | ✓ turnByID :1880 + Parts 内按 AgentID upsert :1902-1911——§4.1 第 3 点「镜像 subagent_part 模式」有真实模板 |
| `events.go:503-529`（EventContextInjection → context_injection data） | :503-529 | ✓ 现状 data 仅 itemId/kind/form/summary/text/senderSessionId，**无 turnId**——§4.1 第 2 点「`ev.TurnID != ""` 时增加 turnId」为纯加法，与现状一致 |
| `history.go:211-224`（flushTurn 后追加 pendingInjections） | flushTurn :211-219 | ✓ |
| `history.go:267-291`（acc.open 时缓冲） | 分支 :267-295 | ✓ :289-290 缓冲 / :291-292 直接 append |
| `handlers_projection.go:1938`（parts 分发 switch，新增 case 的宿主） | switch :1938 | ✓ 现状有 text/reasoning/tool/user_input 等分支，无 context_injection——新增为加法 |
| `handlers_projection.go:2131-2160`（独立 entry 路径字段） | case :2131-2159 | ✓ 字段与 events.go 完全同形；「字段同独立 entry 路径」声明成立 |
| `handlers_projection_test.go:2442-2481`（既有 context_injection 用例） | 测试 :2445-2484 | ✓ `TestRichHistoryContextInjectionEntryToProjectionEvent` 存在，§6「必须保持绿」有真实对象 |
| `projection_types.go:133-134`（context_* 字段复用、无新字段） | 注释 :133-140、字段 :141-145 | ✓ 语义属实；行号偏差（建议 2） |
| `core/message.go`（Event struct 已有 TurnID 字段） | :648 | ✓ `TurnID string // source-proven turn identity`——§4.1 第 1 点的前提成立 |

另核：`pendingInjections` 全仓 grep 仅 `history.go` 5 行（:210/:215/:216/:217/:290），§4.1 第 4 点「仅此一处使用、随之删除」属实；live/冷拉 turn id 同式性——live `adoptTurn` → `dshwTurnID(sessionID, turn)` = `dshw-<prefix>-t<N>`（codec.go:137-138、:220），冷拉 entry ID 同函数（history.go:662-663），「live 与冷拉同 turnId」可行性成立。

### 4. iOS 仓锚点（13 处，`5c737ebe` 工作树亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `SessionProjectionMapping.swift:411-431`（结构化 .command 映射、fail-closed 词表） | case "command" :411-423 | ✓ running\|success\|error 词表 :414-416 |
| `SessionProjectionMapping.swift:427`（"Compact has no user bubble (inputLine nil)" 同构注释） | :427 | ✓ 注释原文逐字一致 |
| `SessionProjectionMapping.swift:440-458`（已按 part 无角色约束映射 .contextInjection、fail-closed） | case :440-455 | ✓ kind 空 or summary+text 双空跳过 :444-447——「映射层零改动」前提成立 |
| `ChatTimelineAdapterUIKit.swift:266-269`（renderText 取 copy 纯文本） | :264-270（调用点 :141） | ✓ `fullResponseCopyText(for:command:contextInjection:)`；item 显示文本确实取自 copy 宿主——§3.1 降级点定位成立 |
| `ChatTimelineCopyText.swift:76-92`（commandRowPlainText 输出即截图形状） | :73-87 | ✓ `"\(inputLine)\n\(card)"` :85-86；行号偏差（建议 2） |
| `NativeTimelineRow.swift:707-718`（.system 行现状无条件纯文本） | :705-724 | ✓ system item（除 compact boundary）直接落 `.system` 行，无 command 检查——§4.2 第 1 点的现状描述成立 |
| `AssistantTimelineRenderSpec.swift:28-34`（block 枚举现状） | :28-46 | ✓ 现状六 case 无 contextInjection——新增为加法 |
| `AssistantTimelineRenderSpec.swift:547-553`（buildFromParts 防御跳过） | :547-553 | ✓ `case .command, .contextInjection:` 注释「防御性刷新并跳过，不造近似块」——§4.1 第 3/6 点与 §8 老客户端声明的前提成立 |
| `AssistantTimelineRenderSpec.swift:291-296`（路由检查不强制切离） | :291-294 | ✓「无需改」声明属实 |
| `SessionProjection.swift:280-285`（mirror 字段已备） | 字段 :285-289 | ✓ contextKind/Form/Summary/Text/SenderSessionId 与 Mac ProjectionPart 一一对应；「mirror 无需改动」成立 |
| `Localizable.strings:506`（chat.contextInjection.title） | zh-Hans :506 | ✓ `"chat.contextInjection.title" = "上下文注入";`，注释标明官方 ui-chat 'message.contextInjection' 镜像 |
| `TimelineRowStyle.swift:40,52`（system 次要灰字） | :40 / :52 | ✓ :40 footnote 字体、:52 secondaryLabel |
| `MessageWebSnapshotBuilder.swift`（makeCommandRow / makeContextInjectionRow） | :405 / :418 | ✓ web 通道已有官方样式数据面——§3.1 声明成立 |

### 5. 双仓接口一致性（评审要点 4）

- **part 字段复用**：Mac `ProjectionPart` context_*（projection_types.go:141-145）↔ iOS mirror（SessionProjection.swift:285-289）字段名一一对应，无新 wire 字段 ✓。
- **turnId 语义**：codec 附 `TurnID = activeTurnID`（`dshw-<prefix>-t<N>`）→ events.go `data["turnId"]` → reducer 按 turnId 定位 turn → hydrate parts 分发在同一 turn entry 循环内（turnID 变量在作用域内，handlers_projection.go:1936-1938），冷拉 entry ID 与 live 同函数同格式（history.go:662-663 = codec.go:137-138）——「live 与冷拉同 turnId」成立 ✓。
- **`ctxinj:<seq>` 幂等键**：live（codec.go:521）与冷拉（history.go:280）同 id；reducer 双分支（turnId 非空 → Parts 内按 `(Type=="context_injection", ItemID)` upsert，镜像 subagent_part 的 :1902-1911 模式；turnId 空/turn 缺失 → 既有 `ctx:` 独立 turn 路径）——降级不丢内容 ✓。
- **iOS 侧引用的既有代码事实**：Mapping :440-458 已有映射 ✓、buildFromParts 现状防御跳过 ✓、路由检查现状不切离 ✓——与工作树一致。

### 6. §5 协议声明与 §4 实现一致性（评审要点 3 之协议部分）

- 「wire 无新字段」：events.go 现状无 turnId（加法）、ProjectionPart context_* 既有、iOS mirror 字段已备 ✓。
- 「出现点为加法」：assistant parts 内出现 context_injection 对老 iOS 是新形状，但被 :547-553 防御跳过 + Adapter 角色过滤（:235/:245）双层消化，不崩不显 ✓（比方案声称的更稳，见建议 5）。
- 「老客户端防御跳过的双向前向兼容」：老 iOS + 新 Mac = 跳过不显 ✓；新 iOS + 老 Mac = 无新 part、行为同现状 ✓。
- 协议包需更新的真实现状：bridge-v1.md:1869「exactly ONE completed system turn」句（见建议 3）；`bridge_v1_schema` 的引用对象不精确（同建议 3）。

### 7. 内部一致性 §3↔§4↔§6↔§7（除 B1 外无矛盾）

- §3.1 根因（数据齐、渲染降级）↔ §4.2 Fix A ↔ §6 iOS 测试 2（goal/permission/compact 三形态）↔ §7 命令对账 1+1+permission 1 卡：每个改动点有测试、每个测试有对应对账 ✓。
- §3.2 根因（turnless + 纯追加 + 一回合一条目）↔ §4.1 六步 ↔ §6 Mac 1-4 + iOS 1-2/回归 ↔ §7 注入对账 5/5 + 结尾堆行 0 ✓（机制描述本身正确，仅回合编号错误即 B1）。
- §4.4 非目标与修复范围不冲突：relay 4 条 / goal_round / tool-goal 在 codec default 分支现状即丢弃（codec.go:529-539 注释明列），本轮不扩 ✓；TurnTriggerNodeView 未涉及 ✓。
- §6 测试计划 GOTOOLCHAIN=local ✓（与 handoff 约束一致）；D2/D3 分级合理（reducer/协议出现点属 D3）✓；唯一缺口是 overlay 流式用例未入 §6（建议 6）。

### 8. 四拍走查与纪律红线（评审要点 5/6）

- 四拍完整：打开输入（官方 dsh web 输入框）→ 输入（/goal 命令文本）→ 发出去（command/run → journal → mux 直播）→ 过程展示（§4.3 六步期望画面序列，含注入行默认收起/可展开、3138efcb 两条各在其位、结尾不堆行）；失败/降级路径在 §8 有交代（流式期覆盖、finalize 后 part 追加、turn 缺失降级独立行、双向兼容）✓。
- 不改 dsh 源码 ✓（方案无任何上游仓改动项）；fail-closed 不造数据 ✓（降级只换展示位、不丢内容、不造幽灵回合；summary 空丢弃语义两仓一致）；copy golden 不动与实现不冲突 ✓（fullResponseCopyText 仅 system 角色消费注入 :17-25，提取层角色过滤 :235/:245）；真机走查走 agent-device 常设授权并指向配套工作树 `IOS_VERIFICATION_ENTRY.md` ✓。

### 9. 截图证据（评审要点 7）

§1 的两张截图为背景材料，文本会话无法独立复核图像内容，本轮未据此作任何阻塞判定；设计依据全部以 journal 样本与双仓源码为准。

## 本轮执行记录

- 亲核命令：三项工作树各 `git rev-parse HEAD` / `git branch --show-current` / `git status --porcelain`；上游另 `git describe --tags --exact-match HEAD`；journal `wc -l` + python3 逐行 JSON 解析（只读）。
- 未运行任何构建/测试（方案阶段评审，无代码改动可测）；未修改除本报告外的任何文件；未 commit；journal 与会话目录只读。
