# dsh-web /goal 原生双表面 + 上下文注入回合内联方案第三轮评审

- 日期：2026-09-24
- 对象：`docs/2026-09-24-dsh-goal-native-surface-and-inline-injection-plan.md`（方案阶段修订稿 v3，文档头部状态行「方案阶段修订稿 v3（同日第 2 轮修订，未动代码。owner 2026-09-24 已裁决方向…）」），按未提交工作树内容评审
- **结论：通过。** 零阻塞级意见。v3 对 r2 全部 4 条意见（B1 + 建议 1–3）的修订逐条亲核属实、全部落地；本轮亲核官方源码锚点 6 处、Mac 仓锚点 17 处、iOS 仓锚点 25 处、协议包文档锚点 2 处（共 50 处，超出要点要求的 12 处），内容与行号全部吻合，**零锚点错误**（3 处引用区间与实际结构边界有 1–5 行偏差，所引内容全部落在实际结构内且逐字核对全对，逐条记录备查，不构成错误）；§2.1/§2.2/§7 的全部对账数字（seq、条数、结算次数、turn 生命周期、step 边界邻接、form、sender、command/done 展开文本）逐项与 journal 样本吻合；内部一致性（§3↔§4↔§6↔§7 四方互洽、§4.4 非目标与修复范围不冲突、§5 协议声明与 §4 实现一致）、双仓接口一致性（part 字段复用链、turnId 同式、`ctxinj:<seq>` 幂等键、live/冷拉同 turnId+itemId、iOS block id 同式）、四拍走查、纪律红线全部核过，无冲突。r2 阻塞 B1 所涉的三个缺位面（`AssistantProcessPresentation` 边界判定/展开层、`MessageWebSnapshotBuilder.makeBlock`、message-web 表面决策）在本轮全部有现状锚点 + 落点设计 + recorded difference 三处联动声明，决策 (b) 的代价如实记录且原生主目标不受影响。2 项建议级意见（§8 消费面清单补 `SessionSearchIndex`、新注入 cell 的拷贝/AX 语义）不阻断，可在实施时顺手处理或并入文档微修订。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `d110022e2946ee1d6987661bda64b2d576401601` | 4 个未跟踪文件：`docs/…plan.md`（送审对象自身）、`docs/…plan-review-r1.md`、`docs/…plan-review-r2.md`、`handoffs/handoff-20260923-2352.md`；无已跟踪文件修改 | 与方案 §0 声明逐字一致（v3 已写全 4 个未跟踪文件）✓ |
| iOS（配套） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `5c737ebe9b0184512edcaa1725bf59dfb2d3d0e3` | 干净（`git status --porcelain` 输出为空） | 与方案 §0 声明一致；本轮引用该工作树源码作锚点核对（Mapping/Adapter/CopyText/NativeRow/RenderSpec/ProcessPresentation/WebSnapshotBuilder/SessionProjection/Localizable/TimelineRowStyle/MessageGroupModels/DetailSheets/SessionSearchIndex） |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净（`git status --porcelain` 输出为空） | `git describe --tags --exact-match HEAD` 实得 `dsh-v0.1.7-alpha.2`，与方案 §0 声明一致；官方源码唯一来源 |
| journal 真值样本 | `~/.dsh/sessions/--Users-jacklee-Projects-Chat--/session-daef20e4-f1c7-48dc-85eb-2d4e96dd5068/session.v4.jsonl.zstd`（原始）；解压副本 `/tmp/dsh-daef20e4-session.v4.jsonl`（319944 字节，151 行） | — | — | 只读取证，未写入任何会话目录 | 本轮重新解压原始 zstd 到 `/tmp/dsh-verify-round3.jsonl` 并比对 SHA-256：`345209f6a6c32e375b379a6afff24845857a3919287b8faddd73df7dd585cf07` 两份一致（与 r2 记录同值）——解压副本与原始样本逐字节同源；§2/§7 对账数字唯一真值来源，jq 逐行解析核对 |

三项工作树均由本轮 `git rev-parse HEAD` + `git branch --show-current` + `git status` 亲核。除写入本报告文件外，本轮未修改任何文件、未 commit；journal 与会话目录只读。

## 阻塞级意见（0 项）

无。r2 的唯一阻塞 B1（`AssistantRenderBlock` 新增 case 的编译波及面缺位 + message-web 表面与过程分段两个决策缺位）在本轮已完整闭合，逐条亲核见「已核实事项 §1」。

## 建议级意见（不阻断，逐条可改）

1. **§8 消费面清单未列 `SessionSearchIndex.swift`**。该文件 `MessagePart.searchableText()`（`SessionSearchIndex.swift:127-148`）对 `.contextInjection` 已有 case（:144-147，返回 `ChatTimelineAdapter.contextInjectionPlainText(injection)`），且索引文本抽取遍历**所有** group 的 parts 不分角色（:100-103 `for part in m.parts { pieces.append(part.searchableText()) }`）。Fix B 后 busy 注入 part 进入 assistant group parts，搜索索引将从现状「只收录 system 独立注入行」开始**收录回合内注入行**。行为良性（回合内注入行成为可见行后可被搜索，与产品方向一致；case 已存在，无编译波及、不崩不丢），但方案 §8 的消费面清单（buildFromParts / adapter guard / copy / web item 字段 / makeBlock）未覆盖该文件。建议：§8 消费面清单补该文件一行，§6 iOS 测试计划可加一条断言（回合内注入行可被搜索命中、文本与 `contextInjectionPlainText` 同源），或实施时在 §8 补记为预期伴生行为。
2. **新注入折叠 cell 的拷贝/AX 语义未声明**。现状 busy 注入独立行是 `.system` 纯文本行（`NativeTimelineRow.swift:716-718`），走行级长按拷贝（`NativeTimelineRow.swift:638`「长按拷贝全文」）可拷出注入文本，AX 朗读读 item.text。§4.1 第 6 点对 `TimelineCardCells.swift` 新 cell 只声明了「折叠披露行：图标 + 标题 + summary 单行；点击展开 body」，未声明长按拷贝与 AX 语义。若新 cell 不支持拷贝 settle 原文，用户相对现状失去拷贝注入文本的能力（显示可见但拷不到的语义回退）。建议：§4.1 第 6 点补一句「新 cell 保持长按拷贝 settle 原文与 AX 可读（对位既有行语义）」，§6 iOS 2 补对应断言；实施时按既有 cell 惯例处理即可，不扩设计。

## 已核实事项

### 1. v3 对 r2 全部意见的修订（逐条亲核，全部属实）

| r2 意见 | v3 声称的修订 | 本轮亲核结果 |
| --- | --- | --- |
| B1（编译波及面缺位 + web 通道不变量矛盾 + 两个决策缺位） | §4.1 第 6 点补 `AssistantProcessPresentation` 与 `MessageWebSnapshotBuilder` 两文件现状锚点 + 落点设计；message-web 决策选 (b)（`makeBlock` 跳过 case，recorded difference）；§4.4/§7/§8 改写；§6 iOS 补第 5/6 条；§9 切片 3 补两文件 | ✓ 全部落地且与代码事实相符：`isProcessBoundary`（:148-159）穷举无 default、`buildExpandedItems`（:193-222）穷举无 default、:154-155 userInput/workflow/subagentGroup→true、:145-147「未来声明为 independent presentation」注释、:216-217 防御跳过注释、`buildSegments`（:112-143）被 `NativeTimelineRow.swift:876`/:1020 调用——方案引用的锚点逐行核对全部吻合；`makeBlock`（:611-750）awk 检索无 default、六 case（:613/:624/:637/:672/:689/:722）、`makeAssistantTurn`（:481-486）`.map(makeBlock)`——吻合；决策 (b) 的 recorded difference 在 §4.4/§7/§8 三处一致声明，idle 独立行仍经 `makeContextInjectionRow`（:418）显示——与代码事实相符 |
| 建议 1（block id 双前缀） | id 式改 `\(ownerID)-ctxinj-<seq>`（buildFromParts 与 buildFromOverlay 同步） | ✓ 双前缀事实亲核属实（`codec.go:521` / `history.go:276`/`:280` 均 `fmt.Sprintf("ctxinj:%d", e.Seq)`）；新式对位既有 id 约定亲核属实（`AssistantTimelineRenderSpec.swift:529` `"\(ownerID)-userinput-\(interaction.interactionId)"`、:541 `"\(ownerID)-workflow-\(run.workflowId)"`；:341 `ownerID: group.id`——buildFromOverlay 用 `group.id` 与 buildFromParts 的 `ownerID` 同值同式） |
| 建议 2（路由检查注释过期） | §4.1 第 6 点补实施注记（行为无需改，注释实施时顺带更新） | ✓ 注释原文亲核属实（`AssistantTimelineRenderSpec.swift:292-293`「上下文注入行同命令行：只出现在 system turn，不占 text/reasoning/tool/file 桶，不强制切离 parts path」；:291-294 为 break 不切离 parts path——行为声明与代码相符） |
| 建议 3（§6 Mac 1 补既有回归对象） | `streams_test.go:928-929` 列入必须保持绿 | ✓ 断言原文亲核属实（:928-929 `activeTurnID after injection = %q, want %q (injection must not steal the turn)`，期望 `dshwTurnID("sess-ctxinj", 1)`） |

### 2. journal 对账（§2.1/§2.2/§7 全部数字，jq 逐行亲核）

| 方案声明 | journal 实测 | 结果 |
| --- | --- | --- |
| 样本 151 条（§0） | `wc -l` = 151 | ✓ |
| `/goal` 输入无 `user/message` 记录（§2.1） | `user/message` 全列：seq 8/9/10/11（基线注入）、34（user-approval）、35/60/74（kind=goal，`<goal_round>` 注入）、94/101/102/111/112/123/124/125/126（注入/relay）、145（tool-goal）——无任何与 seq 26 `/goal` 输入对应的 user 输入 | ✓（seq 35/60/74 为 goal_round、145 为 tool-goal，均非用户输入，且 §4.4 声明维持丢弃，一致） |
| seq 26 `command/run cmd=goal args=创作贾宝玉…` + seq 27 `goal/change` + seq 28 `command/done kind=success text="Goal created\nStatus: active\n…"`（§2.1） | 实测逐字吻合：seq 26 name=goal args=「创作贾宝玉林黛玉薛宝钗王熙凤四人各自故事，每个人300字左右，用4个subagent写每个人故事，并写入/tmp/de…」；seq 27 goal/change；seq 28 kind=success text 首两行「Goal created / Status: active」 | ✓ |
| permission 命令 seq 20-25（§2.1） | seq 20 `command/run permission` + seq 25 `command/done success` | ✓ |
| 5 条 subagent-settled：seq 94（3138efcb）、112（5f00ee6d）、123（98af7acb）、124（f7941941）、126（3138efcb 第二次）（§2.2） | 逐条吻合：94/112/123/124/126，senderSessionId 分别为 3138efcb…/5f00ee6d…/98af7acb…/f7941941…/3138efcb…（3138efcb 两次结算） | ✓ |
| 4 条 `kind=agent-message form=relay`（seq 101/102/111/125）（§2.2） | 逐条吻合：101（5f00ee6d）、102（98af7acb）、111（f7941941）、125（3138efcb） | ✓ |
| turn 1 = seq 4/19、turn 2 = 30/55、turn 3 = 57/69、turn 4 = 71/149；5 条注入全落 71–149（§2.2） | turn/start+end 实测：4/19、30/55、57/69、71/149；注入 seq 94/112/123/124/126 全落区间 | ✓ |
| 紧邻 step 边界事件（seq 93/98/100/108/110/120/122/130）`data.turn` 均为 4（§2.2） | 实测 8 个事件全部 `turn=4`（93/98/100/108/110/120/122/130） | ✓ |
| §4.3 期望画面 2 展开内容「Goal created / Status: active / Objective… / Rounds: 0/256 / Activation: armed / Commands: …」 | seq 28 完整 text 逐字吻合（含 `Rounds: 0/256`、`Activation: armed`、`Commands: /goal edit <objective>, /goal pause, /goal clear`） | ✓ |
| §4.1 第 4 点 history part 字段名 `senderSessionId` | 官方 journal `source` 字段实为 `senderSessionId`（seq 94/101 亲核），与方案字段名及 live `events.go:526-527` 一致 | ✓ |

### 3. 官方源码锚点（上游 `00102833` @ `dsh-v0.1.7-alpha.2`，逐行亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `goal-command-input.ts`：只有 goal 的 command/run 注册 command-input 投影；`goalCommandText()` = `"/" + name + args.trimEnd()`；`anchorSeq = run.seq - 0.1` | match :42（`event.type === 'command/run' && event.data.name === GOAL_COMMAND`）；:34-36（`/${event.data.name}${(event.data.args ?? '').trimEnd()}`）；:64（`anchorSeq: context.state.seq - 0.1`） | ✓ 全部吻合 |
| `GoalCommandInputView.tsx:12`（注释块 :11-16）"Right-aligned `/goal` input bubble"；首 token 命令徽章、其余纯文本 | :11-16 注释逐字含「Right-aligned `/goal` input bubble without ordinary message actions…command chip」；:23-25 head/rest 切分、:35 `projectUserText(head, [], [GOAL_COMMAND], 'command')` | ✓ 全部吻合 |
| `CommandNodeView.tsx` / `GenericCommandCard.tsx`：折叠披露卡，标题裸命令名 + 状态，展开结果正文 | `GenericCommandCard`：title = `node.name ?? t('command.title')`（注释「title is the bare command name」）、summary = running/done/failed 状态词、`<pre>{body}</pre>` 展开正文；`CommandNodeView` 为 command-name keyed 渲染入口（fallback GenericCommandCard） | ✓ 全部吻合 |
| `ui-chat conversation-nodes/message.ts:45-108`：`source.kind != "user"` 一律折成 context 节点，按 journal 顺序内联 | :46 `messageDefinition`、:62 `if (event.data.source.kind !== 'user')`、:74-83 返回 `kind: 'context'`（ContextMessageNode）；节点按事件流装配无挪位逻辑 | ✓ 吻合（方案引用 :45-108 覆盖定义主体） |
| `ContextInjectionRow.tsx:34-71`：DisclosureRow 折叠行 = 图标 + `message.contextInjection` 标题 + producer label + summary，点击展开 form 对应 body | 函数实际起于 :31（方案引 :34-71 为核心区间）；:38 DisclosureRow、:40-42 图标、:44 `t('message.contextInjection')`、:45-58 producer label + summary、:63-68 展开 body | ✓ 内容全部吻合（引用起点偏差 3 行，备查不构成错误） |
| §2.2「iOS 现有文案即官方同位文案」（`Localizable.strings:506`） | 官方 `ui-chat/src/client/locale.ts:91` `'message.contextInjection': '上下文注入'`；iOS `zh-Hans.lproj/Localizable.strings:506` `"chat.contextInjection.title" = "上下文注入";` | ✓ 逐字一致 |

### 4. Mac 仓锚点（工作树 `d110022e`，逐行亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `codec.go:1179-1260` applyCommandRun/Done，goal 专属 InputLine 镜像 goalCommandText | :1179 applyCommandRun、:1200-1205 goal inputLine（注释引官方 goal-command-input.ts）、:1220 applyCommandDone、:1241-1246 done 续接 | ✓ |
| `codec.go:513-527` subagent-settled 分支有意发不挂 activeTurn 的独立行，ItemID `ctxinj:<seq>` | 分支体实际 :508-528（引用区间落在分支内部，覆盖注释核心行 :513 与事件构造）；:512-513 注释「独立系统行…不挂 activeTurn——注入可发生在父会话任意状态」、:515-517 Summary 空 fail-open、:521 `fmt.Sprintf("ctxinj:%d", env.Seq)` | ✓（起点偏差 5 行/终点偏差 1 行，内容全对） |
| `codec.go:375-405` applyTurnStart/End | :375 applyTurnStart、:391 applyTurnEnd | ✓ |
| `projection_reducer.go:412-452` upsertSessionCommandTurn：`cmd:<id>` turn + System 消息 command part + StartedAt 插入排序 | :412-416 函数、:417 `turnID := "cmd:" + commandID`、:423-435 System+command part、:441-455 「Insert a NEW command turn by StartedAt, not arrival order」 | ✓ |
| `projection_reducer.go:380`（§3.2 引 :382）新回合纯追加（到达序） | :382 `ps.projection.Turns = append(ps.projection.Turns, turn)` | ✓ |
| `projection_reducer.go:1430-1462` `ctx:<itemId>` turn 走 ps.upsertTurn | :1430 case、:1443 `turnID := "ctx:" + itemID`、:1444 upsertTurn、:1452-1460 part 字段 ContextKind/Form/Summary/Text/SenderSession | ✓ |
| `projection_reducer.go:1866-1913` subagent_part 模式：turn 定位 + Parts 追加/替换 | :1866 case、:1875-1883 turnByID 定位（含 Assistant nil 初始化 :1885-1887）、:1902-1912 Parts 内按键 whole-value upsert | ✓ |
| `events.go:503-529` context_injection 事件转换（现状无 turnId——待加） | :503 case、:513-528 data map（itemId/kind/form/summary/text/senderSessionId） | ✓ |
| `history.go:211-224` flushTurn 后追加 pendingInjections | :210 声明、:211-219 flushTurn（flush 后 append pendingInjections） | ✓ |
| `history.go:267-291` turn 进行中注入缓冲到 flush 之后；:276/:280 `ctxinj:%d` | :267 分支、:276/280 `fmt.Sprintf("ctxinj:%d", e.Seq)`、:289-293 acc.open 缓冲 / else 直接 append | ✓ |
| §4.1 第 4 点「向 accumulator 的 parts 追加 part map」结构可行 | :507 `parts []map[string]any`、:652-670 flush → `Parts: t.parts`、:687-719 workflow part 已有「journal 原位 append + 原地改同一 part」先例 | ✓ |
| `handlers_projection.go:1938` turn entry parts 分发 switch | :1935-1938 `if len(entry.Parts) > 0 { for … switch ptype`（:1949 同作用域已有 turnID 变量） | ✓ |
| `handlers_projection.go:2131-2160` 独立 entry 路径字段 | :2131 case、:2140-2158 itemId/kind/form/summary/text/senderSessionId/timestampMillis | ✓ |
| `handlers_projection_test.go:2442-2481` 既有 context_injection 用例 | :2445 `TestRichHistoryContextInjectionEntryToProjectionEvent`（:2442 起注释），断言字段与方案描述一致 | ✓ |
| `projection_types.go:141-145` 字段 + 注释 :133-140 | 逐行吻合（ContextKind/Form/Summary/Text/SenderSession 五字段） | ✓ |
| §4.1 第 1 点「core.Event 已有 TurnID 字段」 | `core/message.go:648` `TurnID string // source-proven turn identity` | ✓ |
| `streams_test.go:928-929` 注入不偷 activeTurn 断言 | :928-929 断言原文逐字吻合 | ✓ |
| §5 `bridge-v1.md:1860-1892` Part vocabulary 节；:1868-1871「exactly ONE completed system turn」现状句 | sed 实测：:1860 节标题；「reduces `context_injection` events into exactly ONE completed **system turn** per itemId」句横跨 :1868-1870（方案引 :1868-1871 覆盖该句及后句）——方案「实施后不再完整、必须一并修订」的判断正确 | ✓ |
| §5 `bridge-v1-schema.md` 247 行、无 parts/context_injection 条目 | `wc -l` = 247；grep 无命中 | ✓ |

### 5. iOS 仓锚点（工作树 `5c737ebe`，逐行亲读）

| 方案引用 | 实际行号 | 内容核对 |
| --- | --- | --- |
| `SessionProjectionMapping.swift:411-431` 结构化 `.command` 映射 fail-closed 词表 | :411 case、:414-416 `["running", "success", "error"].contains(kind)` guard | ✓ |
| `SessionProjectionMapping.swift:427` "Compact has no user bubble (inputLine nil)" | :427 逐字吻合 | ✓ |
| `SessionProjectionMapping.swift:440-458` `.contextInjection` 已有映射（无角色约束），fail-closed kind 空/双空跳过 | :440 case、:444-447 guard、:448-455 映射 | ✓ |
| `ChatTimelineAdapterUIKit.swift:266-269` renderText = copy 降级 | renderText 实际 :264-270（引用区间覆盖函数体核心），委托 `fullResponseCopyText`；:235/:245 `guard group.role == .system` 两处（§8 引用）逐字吻合 | ✓（范围偏差 2 行，内容全对） |
| `ChatTimelineCopyText.swift:73-87` commandRowPlainText，输出形状 :85-86 | :73-87 函数、:85-86 `return "\(inputLine)\n\(card)"` | ✓ |
| `ChatTimelineCopyText.swift:17-25` fullResponseCopyText 仅 system 角色消费注入参数 | :17 `if group.role == .system`、:18-23 command/contextInjection 消费 | ✓ |
| `NativeTimelineRow.swift:707-718` system item 无条件落 `.system` 纯文本行（Fix A 改点） | :705-724 `case .system, .user:` 合并分支，:716-718 system 落 `.system` 行（现状无 item.command 判断——与方案描述的现状一致） | ✓ |
| `NativeTimelineRow.swift:876`/`:1020` buildSegments 两处调用 | :876（finalized，isActivelyStreaming: false）、:1020（streaming，true） | ✓ |
| `NativeTimelineRow.swift:1355`/`:1380` 两穷举 switch | :1355 `appendFinalizedIndependentBlockRow`（:1366-1377 六 case 无 default）、:1380 `appendLiveBlockRow` | ✓ |
| `AssistantTimelineRenderSpec.swift:28-34` block 枚举 | :28-34 六 case 逐字吻合 | ✓ |
| `AssistantTimelineRenderSpec.swift:547-553` buildFromParts 防御跳过 | :547 `case .command, .contextInjection:`、:551-552 flush + 跳过 | ✓ |
| `AssistantTimelineRenderSpec.swift:291-294` 路由检查 break 不切离；:292-293 注释 | 逐字吻合 | ✓ |
| `AssistantTimelineRenderSpec.swift:563-617` buildFromOverlay 三段装配；:571-577 reasoning 扫描；对注入 part 无处理 | :563 函数、:570-588 reasoning、:590-601 tool、:603-615 narrative、:617 return——确无 contextInjection 处理（方案「必须补」的论证成立） | ✓ |
| `AssistantProcessPresentation.swift:148-159` isProcessBoundary 穷举无 default；:154-155；:145-147 注释 | 逐字吻合（:154-155 `case .userInput, .workflow, .subagentGroup: return true`） | ✓ |
| `AssistantProcessPresentation.swift:193-222` buildExpandedItems 穷举无 default；:216-217 防御跳过注释 | 逐字吻合 | ✓ |
| `AssistantProcessPresentation.swift:112-143` buildSegments | 逐字吻合 | ✓ |
| `MessageWebSnapshotBuilder.swift:396-397` item 字段消费面 | :396 `command: item.command.map(makeCommandRow)`、:397 `contextInjection: item.contextInjection.map(makeContextInjectionRow)` | ✓ |
| `MessageWebSnapshotBuilder.swift:418` makeContextInjectionRow | :418 逐字吻合 | ✓ |
| `MessageWebSnapshotBuilder.swift:481-486` makeAssistantTurn 全量 `.map(makeBlock)` | :481-486 `(hideReasoningBlocks ? filtered : spec.blocks).map(makeBlock)`——所有 block 必经 makeBlock | ✓ |
| `MessageWebSnapshotBuilder.swift:611-750` makeBlock 穷举六 case 无 default | :611 函数、六 case（:613/:624/:637/:672/:689/:722）、:750 闭合；awk 检索 611-760 无 default | ✓ |
| `SessionProjection.swift:285-289` mirror 字段已备、无角色约束 | :285-289 contextKind/Form/Summary/Text/SenderSession 五字段（结构体不限制出现角色） | ✓ |
| `Localizable.strings:506` | :506 `"chat.contextInjection.title" = "上下文注入";` | ✓ |
| `TimelineRowStyle.swift:40,52` 次要灰字 | :40 system footnote、:52 system secondaryLabel | ✓ |
| §8 双层防御声明涉及的既有代码事实 | `DetailSheets.swift:965` `case .command, .contextInjection:` → EmptyView()（per-part 详情视图既有 case，无编译波及）；`ChatMessageGroupModels.swift:155` item.contextInjection 字段（构造走 adapter guard） | ✓ |

### 6. 内部一致性（§3↔§4↔§6↔§7 四方互洽）

- **每个改动点有测试**：codec busy/idle TurnID → Mac 1（含 :928-929 回归）；events turnId → Mac 1/2 联合覆盖（事件产生与 reducer 消费）；reducer 双分支 → Mac 2；history part 化 + `pendingInjections` 删除 → Mac 3；hydrate case → Mac 4（含 :2442-2481 回归）；iOS 映射 → iOS 1；行派生（气泡/卡/注入行/位置）→ iOS 2；回归 → iOS 3；buildFromOverlay 流式 → iOS 4；isProcessBoundary/buildExpandedItems → iOS 5；makeBlock 跳过 → iOS 6；真机端到端 → §6 真机节。**每个测试有对应对账**：§7 注入 5/5、命令 1+1、message-web 缺席如实核对、copy golden、回归全绿。✓
- **§4.4 非目标与修复范围不冲突**：relay 4 条维持丢弃 ↔ codec default 分支 known-drop（`codec.go:529-539` 亲核，注释引 owner 矩阵 §2.9）；`<goal_round>`/tool-goal 维持丢弃 ↔ journal seq 35/60/74/145 实测为该两类；web 通道「wire/快照契约与渲染器不动」与 §4.1 第 6 点 makeBlock 跳过 case 不矛盾（跳过 case 不扩 `WebAssistantBlock` kind、不动渲染器，v3 已把「web 通道不动」精确化为该句）；message-web busy 注入行「结尾堆 5 行 → 不显示」在 §4.4/§7/§8 三处一致声明为 recorded difference。✓
- **§5 协议声明与 §4 实现一致**：wire 确无新字段（part 复用 `projection_types.go:141-145` 既有 context_* 字段，iOS mirror `SessionProjection.swift:285-289` 已备）；出现点为加法（assistant parts 内）；老客户端双层防御（buildFromParts :547-553 跳过 + adapter guard :235/:245）与代码事实相符；「老 iOS + 新 Mac 不崩不显 / 新 iOS + 老 Mac 无新 part 行为同现状」的双向声明成立；协议包修订声明正确（bridge-v1.md :1868-1870 现状句「exactly ONE completed system turn」在实施后确实不再完整，方案要求一并修订是对的）。✓
- **copy golden「逐字节一致」声明成立**：`ChatTimelineCopyGoldenTests` 用例矩阵为代码构造的 fixture（覆盖 system command/context-injection 形状），golden 断言的是 renderText 生产链对相同输入的输出；Fix A 不改 `commandRowPlainText`（显示不再借用 copy，copy 宿主不动），Fix B 不改 `fullResponseCopyText`（assistant 分支不消费 parts 里的注入，idle system 注入形状不变）——相同 wire 输入产出相同 copy 输出，golden 不需重录。busy 注入独立行的消失由 §7 注入对账「结尾堆行数 = 0」显式验收（方案核心目标），不与 copy 行冲突。✓
- **buildFromOverlay 注入块位（tool 段后、narrative 段前）与 finalize 归位的自洽**：流式期注入行置增长尾部 narrative 之上防跳位，finalize 后 buildFromParts 按 parts 序归位官方注入点；行 id 同式（`ownerID-ctxinj-<seq>`，ownerID=group.id）保证 streaming→finalize 身份稳定不重复——§4.1 第 6 点与 §6 iOS 4 声明一致。✓

### 7. 双仓接口一致性

- **part 字段复用链**：Mac `ProjectionPart` context_*（`projection_types.go:141-145`）→ wire → iOS mirror（`SessionProjection.swift:285-289`）→ 映射（`SessionProjectionMapping.swift:440-458`）。字段名/语义逐环一致。✓
- **turnId 语义**：live `codec.go:220` `c.activeTurnID = dshwTurnID(c.sessionPrefix, turn)` ↔ 冷拉 `history.go:663` `entryID = dshwTurnID(t.sessionID, t.turnNum)` 同式；reducer turn_started 带 driver TurnID（`projection_reducer.go:26` 注释）；hydrate turn parts 分发作用域已有 turnID 变量（`handlers_projection.go:1949`）。live 与冷拉同 turnId 成立。✓
- **`ctxinj:<seq>` 幂等键**：live `codec.go:521` / 冷拉 `history.go:276`/`:280` / reducer ItemID / hydrate itemId 四处同式。✓
- **live 与冷拉同 turnId + itemId → reducer 幂等合并**：live 事件（events.go data + turnId）与冷拉 hydrate 事件（:1938 新 case + turnId）同走 reducer `case "context_injection"` 的 turn-attached 分支，按 `(Type=="context_injection", ItemID)` whole-value upsert（镜像 subagent_part :1866-1913 模式，含 Assistant nil 初始化先例 :1885-1887）。✓
- **iOS block id 同式**：buildFromParts `\(ownerID)-ctxinj-<seq>` 与 buildFromOverlay `\(group.id)-ctxinj-<seq>`——ownerID 即 group.id（:341）。✓
- **iOS 引用的既有代码事实全部与工作树一致**（见 §5 表）。✓

### 8. 交互走查（四拍）

§4.3 四拍完整：打开输入（官方 dsh web 输入框）→ 输入（`/goal` 命令文本）→ 发出去（官方 `command/run` → journal → mux 直播到 iOS，路径明确非旁路）→ 过程展示（六步期望画面序列：右对齐气泡 → 折叠卡 → 回合块 → 回合中间注入行 → 回合尾部 → 结尾无堆行）；期望画面 2 的展开内容与 journal seq 28 逐字一致；失败/降级路径在 §8（turn 缺失降级独立行、流式期 overlay 覆盖、finalize 后 part 追加回归）。✓

### 9. 纪律红线

- **不改 dsh 源码**：方案改动清单全部落在 MacBridge/iOS 两仓 + 协议包文档，无任何 dsh 修改项。✓
- **fail-closed 不造数据**：降级路径「turn 缺失降级独立行（不丢内容、不造幽灵回合）」；Summary 空 fail-open 丢弃维持（`codec.go:515-517` / `history.go:272-274` 现状保留）；映射层 fail-closed（kind 空/双空跳过）维持。✓
- **copy golden 不动**：§4.2 第 2 点 + §7 + §8 论证链与代码事实相符（见 §6）。✓
- **测试计划**：`GOTOOLCHAIN=local` 明示；「定向，按 D2/D3 分级」；真机走查走 agent-device 常设授权并指向配套工作树 `IOS_VERIFICATION_ENTRY.md`。✓
- **journal 只读**：本轮评审亦只读（SHA-256 同源比对，未写入任何会话目录）。✓

### 10. 截图（§1 报障证据）

两张截图（Mac 官方界面 `已粘贴 2026-09-24 00.04.05.tiff` + iPhone `Scrollie_20260924_000536.jpg`）为背景材料；文本会话无法独立复核图像内容，本轮未据此产生任何阻塞或结论——设计依据全部以 journal 样本与双仓/上游源码独立核实为准（§2 的全部官方行为锚点均已在上游源码直接核对，不依赖截图）。

## 结论

方案 v3 通过评审。r2 唯一阻塞（B1）与 3 条建议全部修订到位且与代码事实相符；本轮 50 处锚点亲核零错误、journal 对账逐项吻合、内部一致性与双仓接口一致性无冲突、四拍走查完整、纪律红线全部遵守。2 条建议级意见（§8 消费面清单补 `SessionSearchIndex`；新注入 cell 的拷贝/AX 语义声明）可在实施时顺手处理，或在下一次文档微修订中并入，不影响进入开发阶段。
