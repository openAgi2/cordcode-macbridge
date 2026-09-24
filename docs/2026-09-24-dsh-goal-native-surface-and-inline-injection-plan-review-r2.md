# dsh-web /goal 原生双表面 + 上下文注入回合内联方案第二轮评审

- 日期：2026-09-24
- 对象：`docs/2026-09-24-dsh-goal-native-surface-and-inline-injection-plan.md`（方案阶段修订稿 v2，文档头部状态行「方案阶段修订稿 v2（同日第 1 轮修订，未动代码）」），按未提交工作树内容评审
- **结论：不通过。** 1 项阻塞级意见：§4.1 第 6 点向 `AssistantRenderBlock` 新增 case 的编译波及面超出方案声明的 iOS 改动范围——同 target 内三处**无 default 的穷举 switch**（`MessageWebSnapshotBuilder.makeBlock`、`AssistantProcessPresentation.isProcessBoundary` / `buildExpandedItems`）必然编译失败，其中 `MessageWebSnapshotBuilder` 属 web 通道，与 §4.2 第 3 点 / §4.4「web 通道不动」的方案级不变量直接矛盾；且缺口处含两个有产品可见后果的决策（message-web 表面注入行存续、过程分段边界语义），方案均未分配。除此之外：v2 对 r1 全部 7 条意见的修订逐条亲核属实；本轮亲核官方源码锚点 6 处、Mac 仓锚点 15 处、iOS 仓锚点 19 处、协议包文档锚点 3 处（共 43 处，超出要点要求的 12 处），内容与行号全部吻合，**零锚点错误**；§2.1/§2.2/§7 的全部对账数字（seq、条数、结算次数、turn 生命周期、step 边界邻接、form、sender）逐项与 journal 样本吻合；双仓接口一致性（part 字段复用、turnId 同式、`ctxinj:<seq>` 幂等键、live/冷拉同 id）、§5 协议前向兼容声明（老客户端双层防御）、copy golden 逐字节一致声明、四拍走查、纪律红线全部核过，无其他冲突。阻塞项不涉及 Fix A/Fix B 的核心机制设计——机制本身经核是成立的；需要修订的是 iOS 改动范围声明与两个缺位决策的落点设计。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `d110022e2946ee1d6987661bda64b2d576401601` | 3 个未跟踪文件：`docs/2026-09-24-dsh-goal-native-surface-and-inline-injection-plan.md`（送审对象自身）、`docs/2026-09-24-dsh-goal-native-surface-and-inline-injection-plan-review-r1.md`、`handoffs/handoff-20260923-2352.md`；无已跟踪文件修改 | 与方案 §0 声明逐字一致（v2 已按 r1 建议 1 写全 3 个未跟踪文件）✓ |
| iOS（配套） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `5c737ebe9b0184512edcaa1725bf59dfb2d3d0e3` | 干净（`git status` 输出 nothing to commit, working tree clean） | 与方案 §0 声明一致；本轮引用了该工作树源码作锚点核对（Mapping/Adapter/CopyText/NativeRow/RenderSpec/SessionProjection/Localizable/RowStyle/WebSnapshotBuilder/ProcessPresentation/MessageGroupModels） |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净（`git status --porcelain` 输出为空） | `git describe --tags --exact-match HEAD` 实得 `dsh-v0.1.7-alpha.2`，与方案 §0 声明一致；官方源码唯一来源 |
| journal 真值样本 | `~/.dsh/sessions/--Users-jacklee-Projects-Chat--/session-daef20e4-f1c7-48dc-85eb-2d4e96dd5068/session.v4.jsonl.zstd`（原始）；解压副本 `/tmp/dsh-daef20e4-session.v4.jsonl`（319944 字节，151 行） | — | — | 只读取证，未写入任何会话目录 | 本轮重新解压原始 zstd 到 `/tmp/dsh-verify-round2.jsonl` 并比对 SHA-256：`345209f6…585cf07` 两份一致——解压副本与原始样本逐字节同源；§2/§7 对账数字唯一真值来源，python3 逐行解析核对 |

三项工作树均由本轮 `git rev-parse HEAD` + `git branch --show-current` + `git status` 亲核。除写入本报告文件外，本轮未修改任何文件、未 commit；journal 与会话目录只读。

## 阻塞级意见（1 项）

### B1：新增 `AssistantRenderBlock` case 的编译波及面超出方案声明的改动范围；「web 通道不动」不变量与代码事实矛盾，web 通道与过程分段的两个决策缺位

**方案原文**：§4.1 第 6 点计划在 block 枚举（`AssistantTimelineRenderSpec.swift:28-34`）新增 `case contextInjection(AssistantContextInjectionBlock)`，并列出 iOS 改动文件为 `AssistantTimelineRenderSpec.swift` / `NativeTimelineRow.swift` / `TimelineCardCells.swift`（末条「映射层零改动」）；§9 切片 3 同清单。§4.2 第 3 点「**web 通道不动**（已有 WebCommandRow 渲染）」；§4.4 非目标「web 通道、Mac 官方界面、`/goal` 语义本身均不动」；§8「web 通道（`MessageWebSnapshotBuilder.swift:396-397` 消费 item 字段）均不受影响」。

**亲核代码证据**（iOS 工作树 `5c737ebe` 逐处读源码）：

1. `AssistantRenderBlock` 在同一 app target 内被三处**无 default 的穷举 switch** 消费，新增 case 后为确定性编译错误（Swift 同模块枚举穷举性强制）：
   - `MessageWebSnapshotBuilder.swift:611-737` `makeBlock(from:)`——switch :612 覆盖 reasoning/narrative/tool/subagentGroup/userInput/workflow 六 case 后直接闭合，无 default。调用链：`makeAssistantTurn` :481-486 把 `spec.blocks` **全量** `.map(makeBlock)`（`(hideReasoningBlocks ? filtered : spec.blocks).map(makeBlock)`）——buildFromParts 产出的 `.contextInjection` block 必然流入。
   - `AssistantProcessPresentation.swift:148-159` `isProcessBoundary(_:)`——穷举无 default。
   - `AssistantProcessPresentation.swift:204-219` `buildExpandedItems(from:)`——穷举无 default（narrative/userInput/workflow/subagentGroup 走「组内理论上不会出现（边界已 flush），防御性跳过不造块」:216-218）。
2. 后两者的 `buildSegments`（:112-143：boundary block → `.independent` segment，非 boundary → 进 process group）被 `NativeTimelineRow.swift:876`（finalized 分段行派生 `appendFinalizedProcessRows`）与 `:1020`（streaming 路径）调用——**正是方案计划修改的「finalized 分段行派生与 streaming 平铺」路径的直接上游**。`NativeTimelineRow` 自身的两个穷举 switch（`appendFinalizedIndependentBlockRow` :1355 起、`appendLiveBlockRow` :1378 起）在方案清单内，不缺位；缺位的是上游两个文件。
3. 这两个文件均不在方案 §4.1 第 6 点 / §9 切片 3 的改动清单内。其中 `MessageWebSnapshotBuilder.swift` 属 web 通道——§4.2 第 3 点与 §4.4 的「web 通道不动」作为方案级不变量与上述编译事实矛盾。§8 引用的 web 通道证据（:396-397 消费 item 字段）只覆盖 item 字段消费面，未覆盖 `spec.blocks → makeBlock` 消费面（:481-486、:611-737）——该证据链不完整，据此得出的「web 通道不受影响」结论对**新客户端**不成立（对老客户端成立，见「已核实事项 §6」）。
4. 缺位的不只是机械补 case，而是两个有产品可见后果的决策：
   - **message-web 表面注入行存续**：现状 5 条注入以独立 system 行经 `makeContextInjectionRow`（`MessageWebSnapshotBuilder.swift:418`）进入 web 快照（位置在结尾，但**可见**）。Fix B 后 busy 注入变为 assistant parts：若 `makeBlock` 对新 case 选择跳过/过滤，message-web 表面注入行从 5 行变为 **0 行**——用户可见表面的内容消失，与 §8「降级不丢内容」的精神冲突；若映射为新 `WebAssistantBlock` kind，则 message-web 快照契约与 web 渲染器都要动，与「web 通道不动」冲突更深。二者必居其一，方案未选。
   - **过程分段边界语义**：§4.3 期望画面（注入行出现在回合中间、工具组之间）要求 `isProcessBoundary(.contextInjection) == true`（对位 :154-155 userInput/workflow/subagentGroup 的 independent 处理）；若实现者对位 reasoning 选 `false`，注入 block 被吞进 process group，`buildExpandedItems` 若按 :216-218 同式「防御性跳过」处理则该行在展开层**直接消失**——一条方案未识别的内容丢失路径。方案未把这个决策分配给任何文件。

**为什么定阻塞**：方案评审纪律下方案文档是实施契约。本方案的 iOS 改动清单以「block 枚举 + 三个文件 + 映射层零改动」划定边界，并在 §4.2/§4.4 两处声明 web 通道不动——按方案字面实施**无法编译**，边界声明与代码事实矛盾（内部矛盾类）。与 r1 建议 4（已列文件内缺落点设计，建议级）不同级：这里是整文件缺位 + 方案不变量被证伪 + 两个产品级决策（用户可见表面的行存续、决定 §4.3 期望画面能否成立的分段语义）无归属。若不改文档直接实施，实现者要么擅自扩范围、要么在未记录产品取舍的情况下让 message-web 表面丢行。Fix A/Fix B 的核心机制（journal 对齐内联、reducer 幂等、live/冷拉同 id）不受本阻塞影响，均经核成立。

**修订方向**（可执行，纯文档修订，无设计变更）：

1. §4.1 第 6 点补两个文件的现状锚点 + 落点设计：
   - `AssistantProcessPresentation.swift`：`isProcessBoundary` 对 `.contextInjection` 返回 `true`（对位 :154-155，注释按 :145-147「未来声明为 independent presentation 的 block 同样在此扩展」既有风格）；`buildExpandedItems` 加 case 按 :216-218 同式「边界已 flush，组内理论上不会出现」防御跳过。
   - `MessageWebSnapshotBuilder.makeBlock`：明确二选一并写进方案——(a) 回合内注入映射进 web 快照（写明 `WebAssistantBlock` kind 扩展与 message-web 渲染器改动范围，§4.4/§9 相应扩）；(b) 本轮 message-web 表面不渲染回合内注入：`makeBlock` 加跳过 case，§4.4 非目标与 §8 记录「message-web 表面 busy 注入行由结尾 5 行变为不显示」为 recorded difference（idle 独立行仍经既有 `makeContextInjectionRow` :418 显示）。
2. §8 web 通道句按选定决策改写：证据链补 `spec.blocks → makeBlock` 消费面（:481-486、:611-737）；选 (b) 时「web 通道不动」改为「web wire 不动（makeBlock 仅加跳过 case）」。
3. §6 iOS 测试计划补两条：过程分段用例（注入 block 产出 independent segment、不被吞进 process group、展开层可见）＋ web snapshot 用例（按决策断言 web blocks 中注入行的存续/缺席）。
4. §9 切片 3 文件清单补上述两文件。

## 建议级意见（不阻断，逐条可改）

1. **block id 双前缀**。§4.1 第 6 点的 block id 式 `\(ownerID)-ctxinj-<itemId>` 中 `itemId` 本身即 `ctxinj:<seq>`（codec.go:521 / history.go:280 同式），实际 id 形如 `…-ctxinj-ctxinj:94`。建议改为 `\(ownerID)-ctxinj-<seq>`（或以 itemId 为稳定 key 时去掉 kind token 重复）。纯命名美观；现式仍稳定唯一，不影响 streaming→finalize 行身份稳定的目标。
2. **路由检查注释将过期**。`AssistantTimelineRenderSpec.swift:291-293` 注释「上下文注入行同命令行：只出现在 system turn」在 Fix B 后不再成立（assistant parts 也会出现 context_injection）。方案「路由检查（:291-296）已不强制切离 parts path，无需改」在行为层面正确（已亲核 :291-294 为 break 不切离）；建议实施时顺带更新该注释，避免留下误导性不变量陈述。
3. **§6 Mac 1 可补列既有回归对象**。`agent/dsh-web/streams_test.go:928-929` 已有「注入不得偷走 activeTurn」断言（`activeTurnID after injection = dshwTurnID("sess-ctxinj", 1)`），紧邻本次 codec 改动点（busy 注入附 TurnID）。建议与 `handlers_projection_test.go:2442-2481` 同级列为必须保持绿的回归对象。

## 已核实事项

### 1. v2 对 r1 全部意见的修订（逐条亲核，全部属实）

| r1 意见 | v2 声称的修订 | 本轮亲核结果 |
| --- | --- | --- |
| B1（turn 3 → turn 4） | §2.2 改「turn 4（seq 71–149，最后一个 model 回合）」并补 turn 生命周期；§3.2 改「最后一个 model 回合（journal turn 4）」 | ✓ journal 实测：turn 1 = seq 4/19、turn 2 = 30/55、turn 3 = 57/69、turn 4 = 71/149；5 条注入 seq 94/112/123/124/126 全落 71–149；§1「最后一个回合」与 §2.2 不再矛盾 |
| 建议 1（§0 未跟踪文件写全） | §0 Mac 行列 3 个未跟踪 | ✓ 本轮 `git status` 亲核恰为该 3 个未跟踪、无已跟踪修改 |
| 建议 2（4 处行号） | GoalCommandInputView 注释短语 :12（块 :11-16）；reducer append :382；CopyText :73-87（:85-86）；projection_types :141-145（注释 :133-140） | ✓ 四处全部亲核吻合（见下表） |
| 建议 3（协议包引用对象） | §5 点名 bridge-v1.md :1860-1892 节 + :1868-1871 现状句修订列入交付物 | ✓ 亲核：节标题 :1860、「exactly ONE completed system turn」句 :1868-1870、schema 247 行无 parts/context_injection |
| 建议 4（buildFromOverlay 落点） | §4.1 第 6 点补现状锚点（:291/:547/:563-617/:571-577）+ 落点设计（扫描 pass、id 同式、块位） | ✓ 现状锚点全部吻合；落点设计与 buildFromOverlay 实际结构（三段装配、ordinal 序）相容 |
| 建议 5（双层防御） | §8 补 Adapter guard（:235/:245）+ copy（:17-25）+ web（:396-397）事实 | ✓ 三处亲核吻合（但见 B1：该证据链未覆盖 spec.blocks→makeBlock 消费面） |
| 建议 6（overlay 流式用例） | §6 iOS 新增第 4 条 | ✓ 已入 §6；与 §8 互洽 |
| 附（SessionProjection 行号） | :285-289（注释 :279-284） | ✓ 亲核吻合 |

### 2. journal 对账（评审要点 2，全部亲读 `/tmp/dsh-daef20e4-session.v4.jsonl`，151 行，与原始 zstd 重解压 SHA-256 一致）

- **/goal 无 user/message** ✓：全 19 条 user/message 逐条解析，含 "/goal" 字样的只有 seq 35/60/74（`<goal_round>`，kind=goal）与 seq 145（`<goal_complete>`，kind=tool-goal）——均在 §4.4 非目标丢弃范围内；`/goal` 输入本身只有 seq 26 `command/run`（name=goal、args=创作贾宝玉…写入/tmp/demo-plan0098121.txt）+ seq 27 `goal/change`（operation=create、roundsStarted=0）+ seq 28 `command/done`（kind=success、text 逐字=`Goal created\nStatus: active\nObjective: …\nRounds: 0/256\nActivation: armed\n\nCommands: /goal edit <objective>, /goal pause, /goal clear`，与 §4.3 第 2 步期望卡片正文逐段一致）。
- **permission 命令 seq 20-25** ✓：seq 20 command/run（name=permission、args=danger-full-access）、21 permission/preset、22 sandbox/mode、23 approval/policy、24 agent/inbox/spliced、25 command/done（success）。
- **5 条 subagent-settled** ✓：seq 94（3138efcb-b5b7…）、112（5f00ee6d）、123（98af7acb）、124（f7941941）、126（**3138efcb 第二次结算**），全部 kind=subagent-settled + form=notice + 非空 summary——与 §2.2 逐项一致。
- **4 条 agent-message relay** ✓：seq 101（5f00ee6d）、102（98af7acb）、111（f7941941）、125（3138efcb），全部 form=relay——与 §2.2 一致；§4.4「维持丢弃」与 codec default 分支现状一致（codec.go:529-539 known-drop）。
- **turn 生命周期与 step 边界** ✓：turn 1=4/19、2=30/55、3=57/69、4=71/149；5 条注入的紧邻 step 边界事件实测邻接关系——seq 94∈[93,98]、101/102∈[100,108]、111/112∈[110,120]、123/124/125/126∈[122,130]，方案列举的 8 个边界 seq 93/98/100/108/110/120/122/130 的 `data.turn` **全部=4** ✓。
- 总条数 151 ✓。本轮结论未超出样本证明范围；§7「独立行：idle 注入（如有新样本）」如实标注了本样本无 idle 注入（5 条全部 mid-turn）✓。

### 3. 官方源码锚点（评审要点 1，上游 `00102833dfaee1da9f48a3a8eae9d34005a75218` 亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `goal-command-input.ts`（仅 goal 注册 command-input 投影；goalCommandText；anchorSeq=seq-0.1） | match :42、goalCommandText :34-36、anchorSeq :64 | ✓ `match: event.type === 'command/run' && event.data.name === GOAL_COMMAND`；`return \`/${event.data.name}${(event.data.args ?? '').trimEnd()}\``；`anchorSeq: context.state.seq - 0.1` |
| `GoalCommandInputView.tsx:12`（注释块 :11-16，"Right-aligned `/goal` input bubble"） | 短语 :12、块 :11-16；head/rest 切分 :21-25、chip 渲染 :35-36 | ✓ v2 修正后行号吻合；「首 token 徽章化、其余纯文本」属实（`projectUserText(head, [], [GOAL_COMMAND], 'command')`） |
| `CommandNodeView.tsx` / `GenericCommandCard.tsx`（折叠披露卡、裸命令名标题、展开正文） | CommandNodeView :13-24；GenericCommandCard :29-77（title :37 及「the title is the bare command name」注释 :35-36） | ✓ |
| `conversation-nodes/message.ts:45-108`（source.kind != "user" 折成 context 节点、journal 顺序内联） | 定义 :46-112，关键分支 :62-83 | ✓ `if (event.data.source.kind !== 'user')` → `kind: 'context'`（ContextMessageNode）；buildViewNode 以 `context.state.seq` 为锚 |
| `ContextInjectionRow.tsx:34-71`（DisclosureRow：图标 + message.contextInjection + producer label + summary、展开 body） | 函数 :31-71，DisclosureRow :38-70（title :44、label :51、summary :55、body :66-68） | ✓ |
| （补核）官方中文文案「上下文注入」 | `ui-chat/src/client/locale.ts:91` `'message.contextInjection': '上下文注入'` | ✓ 与 iOS `Localizable.strings:506` 同文；producer label=kind 由 `event-projection.ts` contextProducer default 分支 `{ role: 'inject', label: kind }` 证实 |

### 4. Mac 仓锚点（15 处，`d110022e` 工作树亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `codec.go:1179-1260`（applyCommandRun/Done、goal InputLine 镜像） | :1179-1215 / :1220-1258 | ✓ goal InputLine :1201-1204 / :1241-1246，注释引用官方 goalCommandText |
| `codec.go:513-527`（subagent-settled 有意不挂 activeTurn、`ctxinj:<seq>`） | 分支 :508-528 | ✓ 注释 :512-513「不挂 activeTurn——注入可发生在父会话任意状态」；`ItemID: fmt.Sprintf("ctxinj:%d", env.Seq)` :521；事件字面量无 TurnID |
| `codec.go:375-405`（applyTurnStart/End） | :375-389 / :391 起 | ✓ 引用区间覆盖两函数主体；`noTurn` 哨兵 :39、activeTurn 检查 :385 |
| `core/message.go`（Event struct 已有 TurnID） | :648 | ✓ `TurnID string // source-proven turn identity`，在 Event struct 字段区内 |
| `projection_reducer.go:412-452`（upsertSessionCommandTurn、`cmd:<id>`、StartedAt 插入排序） | :412-461 | ✓ `turnID := "cmd:" + commandID` :417；System+command part :423-435；「Insert a NEW command turn by StartedAt, not arrival order」:441-455 |
| `projection_reducer.go:382`（新回合纯追加） | append :382 | ✓ v2 修正后吻合（:380 为已存在分支 return） |
| `projection_reducer.go:1430-1462`（`ctx:<itemId>` turn 走 upsertTurn） | case :1430-1462 | ✓ `turnID := "ctx:" + itemID` :1443 |
| `projection_reducer.go:1866-1913`（subagent_part 模式） | case :1866-1912 | ✓ turnByID :1880 + Parts 内按 (Type, AgentID) upsert :1902-1911——「镜像 subagent_part」有真实模板 |
| `events.go:503-529`（EventContextInjection → data） | :503-529 | ✓ 现状 data 仅 itemId/kind/form/summary/text/senderSessionId，无 turnId——「增加 turnId」为纯加法 |
| `history.go:211-224`（flushTurn 后追加 pendingInjections） | flushTurn :211-219（追加 :215-218） | ✓ |
| `history.go:267-291`（acc.open 缓冲 / idle 直接 append） | 分支 :267-295（:289-290 缓冲 / :291-292 直接） | ✓ |
| `handlers_projection.go:1938`（turn parts 分发 switch） | switch :1938 | ✓ 现状无 context_injection case——新增为加法 |
| `handlers_projection.go:2131-2160`（独立 entry 路径字段） | case :2131-2159 | ✓ 字段与 events.go 完全同形 |
| `handlers_projection_test.go:2442-2481`（既有用例） | 测试 :2445-2484 | ✓ `TestRichHistoryContextInjectionEntryToProjectionEvent` 存在 |
| `projection_types.go:141-145`（context_* 字段，注释 :133-140） | 字段 :141-145、注释 :133-140 | ✓ v2 修正后吻合 |

另亲核：`dshwTurnID` 为 live/冷拉共用函数（codec.go:137 定义；codec.go:220 live adoptTurn；history.go:309/:663 冷拉）——「live 与冷拉同 turnId」成立；`pendingInjections` 仅 history.go 使用（:210/:215/:216/:217/:290），「随之删除」可行。

### 5. iOS 仓锚点（19 处，`5c737ebe` 工作树亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `SessionProjectionMapping.swift:411-431`（.command 映射、fail-closed 词表） | case :411-423 | ✓ running\|success\|error 词表 :414-416 |
| `SessionProjectionMapping.swift:427`（Compact 无气泡注释） | :427 | ✓ 逐字一致 |
| `SessionProjectionMapping.swift:440-458`（.contextInjection 映射、fail-closed） | case :440-455 | ✓ kind 空 or summary+text 双空跳过 :444-447；宿主 `mapPart(_:)` :320 只收 part 无角色参数——「按 part 无角色约束映射」属实，「映射层零改动」前提成立 |
| `ChatTimelineAdapterUIKit.swift:266-269`（renderText 取 copy 纯文本） | :264-270（item 构建点 :137-151，command/contextInjection 注入 :148-149） | ✓ §3.1 降级点定位成立 |
| `ChatTimelineAdapterUIKit.swift:235/:245`（guard group.role == .system） | :235 / :245 | ✓ 逐字一致——assistant item 的两字段恒 nil（经 :148-149 构建点证实） |
| `ChatTimelineCopyText.swift:73-87`（commandRowPlainText，输出形状 :85-86） | :73-87（`"\(inputLine)\n\(card)"` :85-86） | ✓ v2 修正后吻合，输出形状即截图形状 |
| `ChatTimelineCopyText.swift:17-25`（fullResponseCopyText 仅 system 消费注入参数） | :17-25 | ✓ |
| `NativeTimelineRow.swift:707-718`（.system 行现状无条件纯文本） | :705-724 | ✓ system item（除 compact boundary）直接落 .system 行，无 command 检查——§4.2 第 1 点现状描述成立 |
| `AssistantTimelineRenderSpec.swift:28-34`（block 枚举现状） | :28-46（case 列表 :29-34） | ✓ 现状六 case 无 contextInjection |
| `AssistantTimelineRenderSpec.swift:547-553`（buildFromParts 防御跳过） | :547-553 | ✓ `case .command, .contextInjection:`「防御性刷新并跳过，不造近似块」 |
| `AssistantTimelineRenderSpec.swift:291-296`（路由检查不强制切离） | :291-294 | ✓ 行为属实（break 不切离）；注释过期问题见建议 2 |
| `AssistantTimelineRenderSpec.swift:563-617`（buildFromOverlay 三段装配，reasoning 扫描 :571-577） | :563-618（reasoning :571-577、tool :591-601、narrative :604-615） | ✓ 对注入 part 无任何处理——v2 补的「同步覆盖为必要实施项」属实；ownerID=group.id（buildFromParts 调用点 :341），overlay id 与 parts id 同式可行；workflow/userInput 稳定 key 式 id 模板 :530/:542 亲核存在 |
| `SessionProjection.swift:285-289`（mirror 字段，注释 :279-284） | 字段 :285-289、注释 :279-284 | ✓ v2 修正后吻合；「mirror 无需改动」成立 |
| `Localizable.strings:506`（chat.contextInjection.title） | zh-Hans :506 | ✓ `"chat.contextInjection.title" = "上下文注入";`，与官方 locale.ts:91 同文 |
| `MessageWebSnapshotBuilder.swift:396-397`（web 消费 item 字段） | :396-397 | ✓ 属实——但该证据只覆盖 item 字段消费面（B1） |
| `TimelineRowStyle.swift:40,52`（system 次要灰字） | :40 / :52 | ✓ footnote 字体 + secondaryLabel |
| （补核）`ChatMessageGroupModels.swift` item 字段 | :151 `let command: MessageCommand?`、:155 `let contextInjection: MessageContextInjection?` | ✓ 「结构化 item.command 挂在 item 上」属实；NativeTimelineRow 全文件仅 :1661-1662 子代理 subItem 转发处引用，无样式消费——「原生行从未用于样式」成立 |
| （补核）`joinedText` 对 .contextInjection 的处理 | `SessionProjectionMapping.swift:740-758` | ✓ default 分支跳过——assistant parts 新增注入 part 不进 group.content，**§7「copy 逐字节一致」成立** |
| （补核）RowKind 现状 | `NativeTimelineRow.swift:15` 起 | ✓ 现状无 commandCard/contextInjection——新增为加法 |

### 6. §5 协议声明与 §4 实现一致性（评审要点 3 之协议部分）

- 「wire 无新字段」✓：events.go 现状 data 无 turnId（加法）；ProjectionPart context_* 既有（projection_types.go:141-145）；iOS mirror 字段已备（SessionProjection.swift:285-289）。
- 「出现点为加法」✓：assistant parts 内出现 context_injection 对**老 iOS** 被双层防御消化——① buildFromParts :547-553 防御跳过；② Adapter :235/:245 角色过滤使 assistant item 两字段恒 nil，故 copy（:17-25 仅 system 消费）与 web item 字段消费面（:396-397）均不受影响。老 iOS + 新 Mac = 跳过不显 ✓；新 iOS + 老 Mac = 无新 part 行为同现状 ✓。**对老客户端的声明全部成立**；不成立的是新客户端自身的 web 通道（makeBlock 消费面），即 B1。
- 协议包交付物 ✓：bridge-v1.md :1860-1892 节与 :1868-1871 现状句（「reduces … into exactly ONE completed system turn per itemId」逐字核实）修订已列入 §5；bridge-v1-schema.md 亲核 247 行、grep parts/context_injection 无命中——v2 的引用对象修正准确。

### 7. 内部一致性 §3↔§4↔§6↔§7（除 B1 外无矛盾）

- §3.1 ↔ §4.2 ↔ §6 iOS 2（goal/permission/compact 三形态）↔ §7 命令对账 1+1+permission 1 卡 ✓（permission 无 inputLine 由 codec :1201 仅 goal 生成 InputLine 证实）。
- §3.2 ↔ §4.1 六步 ↔ §6 Mac 1-4 + iOS 1-4 ↔ §7 注入对账 5/5 + 结尾堆行 0 ✓（机制、测试、对账三方互洽；B1 只影响 iOS 渲染侧两个缺位文件，不影响 Mac 三层与对账数字）。
- §4.4 非目标与修复范围不冲突 ✓：relay 4 条 / `<goal_round>` / tool-goal 在 codec default 分支现状即丢弃（codec.go:529-539）；「web 通道不动」一项与 §4.1 第 6 点矛盾，即 B1。
- §6 GOTOOLCHAIN=local ✓（与 handoff-20260923-2352.md:56 约束一致）；D2/D3 分级声明在位 ✓。

### 8. 双仓接口一致性（评审要点 4）

- part 字段复用 ✓：Mac context_*（projection_types.go:141-145）↔ iOS mirror（SessionProjection.swift:285-289）一一对应。
- turnId 语义 ✓：codec `TurnID = c.activeTurnID`（dshwTurnID，codec.go:137/:220）→ events.go `data["turnId"]`（加法）→ reducer 按 turnId 定位 → hydrate parts 分发在同一 turn entry 循环（handlers_projection.go:1936-1938，turnID 在作用域内）；冷拉 entry ID 同函数（history.go:663）。
- `ctxinj:<seq>` 幂等键 ✓：live codec.go:521 = 冷拉 history.go:276/:280；reducer 双分支（Parts 内按 (Type=="context_injection", ItemID) upsert 镜像 subagent_part :1902-1911；turnId 空/缺失 → 既有 `ctx:` 独立 turn :1430-1462）——降级不丢内容 ✓。
- iOS 侧引用的既有代码事实 ✓：Mapping :440-458 已有映射、buildFromParts 现状防御跳过、路由检查现状不切离——与工作树一致。
- §4.1 第 4 点 part map 键（kind/form/summary/text/senderSessionId）与既有事件 data 键（events.go:513-528、handlers_projection.go:2140-2155）同形，reducer 落 ProjectionPart 时映射到 context_* wire 字段——内部自洽 ✓。

### 9. 四拍走查与纪律红线（评审要点 5/6）

- 四拍完整 ✓：打开输入（官方 dsh web 输入框）→ 输入（/goal 命令文本）→ 发出去（command/run → journal → mux 直播，与 dsh-web 运行模型一致）→ 过程展示（§4.3 六步期望画面，含注入行默认收起/可展开、3138efcb 两条各在其位、结尾不堆行）；失败/降级路径在 §8 有交代（流式期覆盖、finalize 后追加、turn 缺失降级、双向兼容）。§4.3 第 2 步卡片正文与 journal seq 28 text 逐段一致 ✓。唯一缺口：六步画面在过程分段（工具组）与 message-web 表面的成立条件未在方案内分配——即 B1。
- 不改 dsh 源码 ✓（方案无任何上游仓改动项）；fail-closed 不造数据 ✓（计划内降级路径只换展示位不丢内容、summary 空丢弃两仓同语义、不造幽灵回合；B1 指出的 web/展开层丢行是**计划未覆盖**的路径，正是需要写进方案的原因）；copy golden 不动与实现不冲突 ✓（joinedText default 跳过 + fullResponseCopyText 仅 system 消费注入参数，亲核）；测试计划 GOTOOLCHAIN=local + D2/D3 ✓；真机走查走 agent-device 常设授权并指向配套工作树 `IOS_VERIFICATION_ENTRY.md` ✓。

### 10. 截图证据（评审要点 7）

§1 的两张截图为背景材料，文本会话无法独立复核图像内容，本轮未据此作任何阻塞判定；设计依据全部以 journal 样本与双仓源码为准。

## 本轮执行记录

- 亲核命令：三项工作树各 `git rev-parse HEAD` / `git branch --show-current` / `git status`；上游另 `git describe --tags --exact-match HEAD`；journal 原始 zstd 重解压至 `/tmp/dsh-verify-round2.jsonl` 比对 SHA-256 后 python3 逐行 JSON 解析（只读）。
- 锚点核对方式：官方/Mac/iOS/协议包共 43 处逐处打开源文件读行核对（非 grep 片段断章）；`AssistantRenderBlock` 消费面以全仓 grep + 逐 switch 读源确认穷举性。
- 未运行任何构建/测试（方案阶段评审，无代码改动可测）；未修改除本报告外的任何文件；未 commit；journal 与会话目录只读。
