# codex-remote 断线韧性与恢复专项方案 评审报告（r8）

- 日期：2026-09-28
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（**v1.7**——r7 APPROVED 后、r7-meta 元审核 confirm=false 的通过后纠错勘误轮）
- 评审时方案 SHA-256：`b7e3bcf837f2c5c2cccfeeee7ee404f2578493512372b3dc9118ea81905af23c`（整文件 `shasum -a 256` 亲算，**与派发记录一致**——评审对象身份确认）；正文哈希 `head -n 373 | shasum -a 256` = `b35c431ade9b719fc211efae660ad18406047383b81a864dee5d40dd4fb081a7` 亲算，与方案内设计师交接块自报一致
- 评审范围：**full**（任务简报字段 7；评审员模式 fresh——先独立形成风险清单并逐锚点亲核，后读 r1~r7-meta 全部 11 份历轮报告作回归清单）。重点按简报：①v1.4 三处新设计（§3.2/§3.3/§3.7/E-12a/b）独立源码核验 ②r4 §6 六项与 R2-A1~A4 闭合对照 ③修订无夹带无回归 ④门控一致性 ⑤audit-plan 纪律
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md 均已读取；audit-plan 纪律按 E-12b pending 处置复核，见 §7）
- 结论：**REVISION_REQUIRED**（blockers 1，advisories 0）
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、产品代码、历轮报告、两仓源码、官方 checkout 一律只读，未 commit、未部署、未构建、未采集任何真机/wire fixture（E-12b 需 owner 授权，未触碰）。
- 任务简报时效性注记：简报为 r5 派发时点文本（评审链描述止于 v1.4/r5 启动、「本轮 r5」字样、v1.4 整文件哈希前缀 b919e4d4），与当前实况（v1.7/r8）存在漂移；按 r5/r6/r7 三轮先例，**以派发 SHA（b7e3bcf8…，与当前文件一致）与报告路径（r8）为准**，评审对象身份无歧义。

---

## 1. 结论

### verdict: REVISION_REQUIRED（不通过，需修订；修订量小、范围收敛于 §3.3 一处）

**v1.7 勘误本身经本轮逐项亲核全部属实、无夹带、无设计改动**：勘误-5 的全部事实声明（39 字符无效哈希、`git rev-parse` exit=128、方案 §2.1 自始记录 40 字符正确值、r7 报告第 45 行原误/第 46 行对照组正确、13 字符前缀可唯一定位）本轮全部亲跑复现；§4/§6/交接块的 r7/r7-meta 状态注记与两份报告实况逐处吻合；设计内容（§1~§3/§5/§7/§8.2/§8.3）与 r7 所验 v1.6 逐点一致（勘误章节重编号 §8.1→8.2→8.3 的交叉引用已同步修正，grep 亲证）。r4 §6 复审清单六项、R2-A1~A4、F-R5-1/F-R5-A1~A3 的闭合经本轮独立源码核验（不向历轮复用承重项）**全部维持**。

但本轮独立核验在 §3.3（S-3 缺口检测子系统）发现一个 **历轮（r1~r7-meta）均未覆盖的新阻塞 F-R8-1**：官方 pong 信封与消息**共享同一 per-stream seq 空间**并计入 host outbound buffer（本轮官方源码逐环亲证），而 §3.3 的高水位游标判定表只定义了 plain/chunk 两类消息帧的处理、pong 零出现——按字面实施（游标仅在消息分支推进）将**每 10s 确定性产生一次假「消息级缺口」**，系统性破坏 §1 验收 4「无缺口时零开销」、把真缺口信号淹没在日志噪声里，并在有在飞 turn 时每 10s 触发一轮 fan-out 权威对账拉取。该缺陷自 v1.2 的 F-1 重写起潜伏，r2~r7 七轮均未检查 pong 的 seq 参与（定性：**旧问题漏检**，非 v1.4~v1.7 修订引入、非历史结论被推翻——F-R4-2 的归属/fan-out/一次触发闭合不受影响，受影响的是同一子系统内一条相邻规则）。修订量小：§3.3 判定表补一条「游标对一切携带 seq_id 的入站信封推进（pong 计入）」+ §2.2/E-11 表述限定 + §5 S-3 补 pong 交错负例 + §4 E-12b 捕获清单补 pong 帧项；修订后只需定向复核该四处，不需全量重审。

**对 r7 APPROVED 的处置（plan-contract「通过后纠错」）**：r7 通过所依据的设计内容在 v1.7 中未变、勘误-5 闭合确认成立；但 F-R8-1 属「改变验收成立性的新反证」，S-3/S-7 范围的通过依据自本轮起暂停有效（与 r4 对 r3 的处置同型）；S-1a/S-1b/S-2/S-4/S-5/S-6 的通过依据不受影响（F-R8-1 不触及对账事务、错误分类、token 调度、常量对齐、状态暴露各设计）。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**；评审通过不构成实施授权。
- 实施前置门（与意见分开列，不变）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（随 E-9）、**E-9**（ctrlExp 真机 fixture，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期）、**E-12**（E-12b wire 样本，owner 授权后按 §4 捕获清单采集，门住 S-3/S-7 整体实施）。
- 复审门：F-R8-1 修订 + 定向复审前，**S-3/S-7 的阻塞闭合复审状态回到未闭合**；S-2 的闭合依据（对账事务域，与 seq 游标规则无关）维持 r6/r7 确认。
- 待办（非门、非阻塞）：A-R6-1（§3.2 Abort 规格补 `finishHydrateLocked` 关闭句）维持「实施前随手补入」路径（r6 闭合标准，r7 维持，本轮复核维持——其事实基础 `:957-962`/`:917` 本轮亲读吻合）。
- E-10 真机验证、E-12b wire 捕获与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核，读取源码前重新生成；两仓各 3 树全量枚举）

| 仓库 | 工作树路径 | 分支 | 完整提交 | 未提交状态（`git status --porcelain` 亲跑） | 任务预期来源 / 配套组合 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 73539b71c0c3552a3c16548d05417813ba907869 | 仅方案文档 1 处未提交修改（v1.7，评审循环正常形态）+ 未跟踪评审报告 r5/r6/r6-meta/r7/r7-meta 五份（只读；本报告为第 6 份未跟踪产物） | 简报①指定树；`git diff 07721783..HEAD --name-only` 亲跑：9 文件**全部为 docs/*.md**——**代码零改动**，Mac 锚点与 07721783 等价（与简报②声明一致） | 纯设计文档评审，无构建/安装——N/A（无构建场景） |
| cordcode-macbridge（main 树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge | main | 73539b71c0c3552a3c16548d05417813ba907869 | 干净 | 枚举记录（P0：含配套工作树，无论是否实际引用） | N/A |
| cordcode-macbridge（第三树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge-plan-approval | detached | b2b2523526b6af7990c9688fd29b7e285d7ce78c | 干净 | 枚举记录 | N/A |
| cordcode-ios（iOS 锚点核验来源，简报③指定） | /Users/jacklee/Projects/cordcode-ios | main | ccb5a0df647865324e11aedebbb089b9fc07bbb5 | 干净（亲跑为空；与 r6/r7 核验来源同提交，未再漂移） | 简报③指定 main 工作树 | N/A（纯设计） |
| cordcode-ios（同族配套树，P0 要求记录） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | 61e5c32e7ad03580cf576cadeb9f5cf20f40e348（与 r7-meta 记录一致，未再前进） | 1 个未跟踪 docs 文件（native-timeline streaming phase2 评审报告，与本方案无关） | 非本任务锚点来源。**锚点有效性（本轮亲跑）**：`git diff --stat ccb5a0df..61e5c32e -- <ProjectionStore/CCCodeBridgeModels/ChatViewModel/CCCodeBridgeTransport 四文件>` 为**空**——iOS 锚点结论与工作树选择无关 | N/A |
| cordcode-ios（第三树，枚举项） | /Users/jacklee/Projects/cordcode-ios-plan-approval | detached | a336b68bb37765d2ea23c14f6508619378c839ae | 干净；`git merge-base --is-ancestor a336b68b ccb5a0df` 亲跑通过（iOS main 祖先）；本方案无任何锚点取自该树 | 枚举记录 | N/A |
| openai/codex（官方，只读） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD 亲核） | e72da2b53805894878023d01949a25a082e0a5cb | 干净 | 简报④：一律以 FETCH_HEAD 为准（亲核一致，与方案 §2.1 记录相同） | N/A |

**评审对象身份**：派发哈希与读取哈希一致（报告头）。HEAD 73539b71 内方案为 v1.4 §3.2 时点半成品快照（方案 §2.1 自记）；v1.5/v1.6 字节快照均不可从 git 复原（历史形态，r6/r7 同此声明）——v1.6→v1.7 增量以「r7/r7-meta 报告引用的 v1.6 原文 + v1.7 头部声明范围」交叉印证法核验（方法与结果见 §6）。

### 2.2 覆盖声明

- **本轮 fresh 亲核**（约 50 组锚点，三仓 24 文件）：§3.2 F-R5-1 全链（`CommitHydrateTransaction` `:1319/:1335/:1352-1356/:1357`、merge 第一分支 `:1457-1473`、union 内部调 merge `:1504`、tx 标志先例 `:682-688`、reducer 终态置 idle `:2442-2444`/`:2500-2502`、fence `:1204-1212`、MarkReady `:911-918`/MarkFailed `:925-952`/`finishHydrateLocked` `:957-962` 及 `:917/:953/:1396` 三调用点、admission `:1020-1026`/`:969-976`/`:1069-1083`/`:1092-1095`/`:1027-1034`、commit gate `:1302`、pendingLive drain `:1358-1374`、coldBaseline `:1388-1392`、source cut `:1393-1394`、prepend 硬门 `:1680-1685`、**第四写者枚举 grep 独立复跑**：`k.reducer` 变更点仅 `:1215`（非 Hydrating 可达）/`:1357`/`:1361`（commit）/`:1686`（prepend 被 Ready 硬门挡）——对账窗口期无第四写者）；upsertTurn merge/append `:336-385` + reducer 无 `case "error":`（grep exit=1 亲证）；manifest 字段 `projection_types.go:221-226`；冷路径封口 `handlers_projection.go:1572-1576`/`:1694/:1707/:1713/:1719-1720`；commit 后处理 `:1163/:1177/:1178/:1182-1184`；S-1a 全锚点（`:603-605`/`:430`/`:852`/`:876-881`/`:1079-1083`/`:147`/`:152-156`/`:161-173`/`:176-181`）；§3.3/§3.7/E-12a（`stream.go:44-47`/`:53-57`/`:305-314`/`:324`/`:339`/`:359-373`/`:390-407`/`:409-420`/`:425-434`/`:458-466`、`ws.go:87/:317-318`、`envelope.go:16/:32-49`）；官方（`websocket.rs:86-145` Buffer 四方法含 `insert` `:91-99` 无事件类型过滤、ack 游标 `:112-138` 含 `:123/:129/:136`、seq 分配 `:1031-1045`、writer loop `:990-1067`、通道 `:410/:412/:443`、`client_tracker.rs:221-237/:247-290`、`segment.rs:20-21/:327/:463-467`、`protocol.rs:114-119`、`transport/mod.rs:24`、`server_api.rs:27-28`）；iOS（`ProjectionStore.swift:63/:164-181` 11 项白名单无 hydrate_failed/`:885-887` 只读 code、`CCCodeBridgeModels.swift:317-319`、`ChatViewModel.swift:438/:477-480`）；think.md 双仓（macbridge `:2-6`/`:45`/`:415-425`、ios `:477`）；owner 前置方案 `plan:508-509/:588-599`；E-12b 不可替代前提（attempt-008 四关键词 grep 计数全 0、phase2 fixture camelCase 2/snake_case 0）；勘误-5 全部事实声明（§6）。
- **复用（按命题/锚点粒度；复用合法性依赖的三项源码身份等价本轮全部亲自重跑证实：Mac 代码 07721783≡HEAD、iOS 四锚点文件跨候选零改动、官方 checkout e72da2b5 未动）**：r5/r6/r7 已亲核且 v1.7 未改动的锚点——§2.2 复用表其余各行、§2.3 现状摘要、§3.1/§3.4/§3.5/§3.6、E-1~E-8/E-11 各行、`backoff.go`/`pairing_persist.go`/`pairing.go`/`agent.go`/`codec.go`/`session.go`/`history_paginated.go`/`projection_window_older_hydrate.go`/`main.go`/`session_discovery.go` 各段（本轮对 `codec.go:22/:112-126`、`session.go:195-198`、`main.go:1046-1066`、`session.go:328-337` 做了抽样重核，吻合）、官方 `remote.rs`/`reconnect.rs`/`enroll.rs`/tests 引用。r6-meta 18 组与 r7-meta 6 组抽查作为复用依据；其指出的偏差即 v1.6 勘误-4 对象，本轮已亲自重核修正后值（`:1163/:1178`）。
- **未核项（如实声明）**：① relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码；F-R8-1 的「pong 携带 seq_id 到达 controller」一环由本仓生产代码依赖（`stream.go:44-47` 注释 + readLoop typePong 分支）与官方序列化路径支撑，wire 级确认归入 E-12b pending 门（修订方向已要求把 pong 帧补进捕获清单）；② E-9/E-10/E-12b 为 pending 运行证据，按授权边界未采集，状态如实；③ v1.5/v1.6 字节快照不可复算（历史形态，见 §2.1 方法说明）；④ 本轮未重做 r5/r6/r7 级的全部锚点逐项重查（复用范围见上；未重查项经 §6 增量核验确认 v1.7 零触碰）。

---

## 3. 锚点核验表（本轮承重项摘录；「✓✓」=定位与语义均成立）

### 3.1 §3.2 F-R5-1 闭合链（r6/r7 通过结论的承重核心，本轮独立重推）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `:1319` CommitHydrateTransaction；`:1335` 先取 liveSnap；`:1352-1356` 按 tx 标志二选一；`:1357` Restore | 同 | `:1319 func (k *ProjectionKernel) CommitHydrateTransaction(`；`:1335 liveSnap, liveOK := k.reducer.Snapshot(…)`；`:1352 if tx.unionLiveTurns {` `:1353 union…` `:1355 baseline = mergeHydrateBaselineWithLiveExecution(…)` `:1357 k.reducer.Restore(…)` | ✓✓（「Restore 前有 merge 步骤」的 v1.5 重写依据成立） |
| merge 第一分支 `:1457-1473` | 同 | `:1457 if executionInFlight(live.Execution) && !executionInFlight(cold.Execution) {` `:1458 cold.Execution = live.Execution`；`:1467-1471 case "completed", "aborted", "error": → "running"; CompletedAt = 0` | ✓✓（「把对账刚收口的终态 turn 重置回 running」逐字成立） |
| 「跳过两类 merge」完备性 | `:1504` | `unionColdBaselineWithLiveTurns` 首行 `:1504 merged := mergeHydrateBaselineWithLiveExecution(cold, live, liveOK)` | ✓✓（union 内部亦调 merge，v1.5「两类」写法恰好完备） |
| tx 标志先例 `:682-688` | 同 | `:682 liveOnlyAdmission bool`；`:688 unionLiveTurns bool` | ✓✓（`reconcile` 标志同型、最小） |
| 终态事件置 idle `projection_reducer.go:2442-2444`/`:2500-2502`；fence `:1204-1212` | 同 | 两处 `exec := ExecutionView{Phase: "idle"}`；`:1204-1212 Hydrating → pendingLive 深拷贝 append → ProjectionIngestDeferred` | ✓✓（对账主场景推演前提成立：tx baseline 非在飞、committed reducer 冻结在 running+ActiveTurnID） |
| MarkFailed `:925-952` 丢弃 pendingLive；MarkReady `:911-918` 不 drain；`finishHydrateLocked` `:957-962` + 调用点 `:917/:953/:1396` | 同 | MarkFailed 收集 deferredEventIDs 供调用方丢弃后 `:951-952 置 Failed + hydrate=nil`；MarkReady `:915-917 Phase=Ready + hydrate=nil` 无 drain；`:957-962 close(hydrateDone)` | ✓✓（Abort 不能用 MarkFailed/MarkReady 的两条理由成立；A-R6-1 事实基础成立） |
| admission 两分支不可行 `:1020-1026`/`:969-976`/`:1069-1083`；安装序 `:1092-1095`；单飞行 `:1027-1034`；commit gate `:1302`；drain `:1358-1374`；coldBaseline `:1388-1392`；source cut `:1393-1394`；prepend 硬门 `:1680-1685` | 同 | `:1025-1026 AlreadyReady`；`:970-975` 名单无 codex-remote；`:1077 else if !sourceChanged && len(source.Segments)==0 → Restore(existing)`（codex-remote+sourceChanged 三分支均不中→空 reducer）；`:1302 ready := tx.sourceIngestComplete && (…NonTerminalTurnCountInSet…==0)`（commit 函数体无该标志）；`:1393-1394` 写回；`:1680-1685 prepend requires ready` | ✓✓（「为什么不能复用既有 admission」「不移动 source cut」「coldBaseline 重申 no-op」「gate 由 producer 同步性保证」全部成立） |
| **第四写者检查（本轮独立复跑 r6 的检查）** | grep 亲跑 | `k.reducer` 变更点仅 `:1215`（IngestLive 直 apply，被 `:1204` fence 挡、仅非 Hydrating 可达）/`:1357`/`:1361`（commit 自身）/`:1686`（prepend，被 `:1680` Ready 硬门挡） | ✓✓（对账窗口期 committed reducer 无第四类写者——跳过 merge 不丢任何窗口期 live 真值，v1.5 方向反转论证独立成立） |
| 规则 1 三分支镜像冷路径 §9.2；规则 2 ghost 前提；manifest 锚点 | `handlers_projection.go:1572-1576`/`:1694/:1707/:1713/:1719-1720`；`projection_reducer.go:336-385`；`projection_types.go:221-226` | §9.2 注释 + 实现：completed→`turn_completed`、failed→`turn_error`、interrupted→`turn_aborted`、default 不封口；upsertTurn：存在→merge 保留、缺席→append 造新行；`DetailLoadState/DetailReasonCode/DetailInline` 为 TurnProjection 成员；reducer 无 `case "error":`（grep exit=1） | ✓✓（F-R5-A1/A2 修正后锚点全部精确；ghost 防护前提成立） |
| runner 侧 commit 后处理（v1.6 勘误-4 修正后） | `handlers_projection.go:1163/:1177/:1178/:1182-1184` | `:1163 h.releaseDeferredPushCandidates(…)`；`:1177 if commit.PendingPatch != nil {`（守卫）；`:1178 h.eventPublisher.PublishProjectionPatch(…)`；`:1182-1184 backendID == "codex-remote"` 门控 `persistCodexProducerSeed` | ✓✓（修正后区间 `:1163-1178` 恰覆盖两调用行） |

### 3.2 §3.3/§3.7/E-12a 与 iOS（抽样亲核）

| 命题 | 核验 | 结果 |
| --- | --- | --- |
| E-12a：ack 构造无 SegmentID；ack 仅两处调用点；字段已备 | 亲读 `stream.go:390-407`（Write 字面量仅 Type/ClientID/EnvID/StreamID/SeqID）；grep `\.ack(` → `:324`/`:339` 两处；`envelope.go:45 SegmentID *int` | ✓✓ |
| 熔断现状无界 + 解除=流重建 + pong/typeAck | 亲读 `stream.go:359-373`（sentinel→`acksDisabled=true`）、`:397-399`（disabled 早退）、`:44-47`（relay ACK 只证明 relay 收帧）、`:53-57`（fresh stream re-arms）、`:305-306`（typeAck continue）、`:307-314`（pong status!=active 判死）、`ws.go:317-318`（ping 10s/idle 60s） | ✓✓（§3.7 状态机事实前提成立；断言⑧ pong 前提在位） |
| 官方 ack 游标 / seq 分配 / chunk 共享 seq / 常量 | 亲读 `websocket.rs:112-138`（`:123 (seq, seg.unwrap_or(usize::MAX))`、`envelope_cursor <= acked_cursor` retain 清除、`:136 remove` 仅 ack 内）、`:1031-1045`（per-(client,stream) `or_insert(1)` 消息级分配）、`segment.rs:20-21`（150KB/100MB）、`:327`（整体或全 chunk 二选一）、`:463-467`（`seq_id: envelope.seq_id`）、`protocol.rs:114-119`（chunk ack 携带 segment_id，逐字）、`transport/mod.rs:24`（128）、`server_api.rs:27-28`（24–36s） | ✓✓ |
| iOS：白名单 11 项不含 hydrate_failed、只读 code、三字段已解码、灾难 loop 本地退避 | 亲读 @ ccb5a0df：`ProjectionStore.swift:63`（outcome 只带 code/message）、`:164-181`（11 case 无 hydrate_failed）、`:885-887`（只读 code）；`CCCodeBridgeModels.swift:317-319`；`ChatViewModel.swift:438/:477-480` | ✓✓（E-3/E-5 与 R4-A1 删承诺依据成立） |
| think.md 复盘 + OD-2 裁决 + owner 方案约束 | 亲读 macbridge `:2-6`（26.924 升级断裂）/`:45`（backend_status_changed 待立项另立）/`:415-425`（750/s 风暴、"Unexpected ack message received from client"）；ios `:477`（2.5h 不自愈）；`plan:508-509`（未取样不得写死提前量）/`:588-599`（Gate P0 fail-closed：官方不在 controller envelope 交付 cursor） | ✓✓ |
| E-12b 不可替代前提 | grep 亲跑：attempt-008 样本 `server_message_chunk`/`segment_id`/`seq_id`/`message_chunk_base64` 计数全 **0**；phase2 fixture `clientId`/`seqId` 各 1、`client_id`/`seq_id` 各 **0** | ✓✓ |
| **F-R8-1 官方 pong 链（本轮新核验，详见 §4）** | 亲读 `client_tracker.rs:221-237`（ClientEvent::Ping → Pong 入 server_event_tx 两条路径）、`:247-290`（run_client_outbound status_rx.changed() → Pong → server_event_tx）、`websocket.rs:410`（单一 `mpsc::channel(CHANNEL_CAPACITY)`）、`:412/:443`（tx 入 ClientTracker / rx 入 writer loop）、`:990-1067`（writer loop 对通道一切信封无条件 seq 分配 + 逐信封 insert buffer）、`:91-99`（`insert` 无事件类型过滤）；`stream.go:176`（typePing）+ `ws.go:337-344`（10s keepalive → stream.Ping()） | ✓✓（pong 与消息共享 per-stream seq 空间并计入全局 buffer——§3.3 规则表的官方语义前提与方案文本冲突，见 F-R8-1） |

---

## 4. 意见（F-ID）

### F-R8-1 [阻塞] §3.3 高水位游标规则未覆盖 pong 帧——官方 pong 与消息共享 per-stream seq 空间，按字面实施将每 10s 产生一次假「消息级缺口」

- **位置**：§3.3 去重判定表与缺口检测段（「readLoop 按 per-stream 维护**消息级高水位游标**…信封按 `(seq_id, segment_id)` 游标判定——`seq_id > lastSeq` → 非重复：`== lastSeq + 1` 正常推进…`> lastSeq + 1` → 消息级缺口」）；§2.2「入站 seq 语义」行与 §4 E-11 行的「消息级严格单调」表述；§5 S-3 单测计划；§4 E-12b 捕获清单。
- **证据**（本轮全部亲核，官方 checkout @ e72da2b5 + 本仓 @ 73539b71）：
  1. **官方 pong 信封与消息共享同一 per-stream seq 空间，且计入 host outbound buffer**：host 对 `ClientEvent::Ping` 的两条响应路径——已知客户端 `client_tracker.rs:221-227`（`status_tx.send(PongStatus::Active)`）经 `run_client_outbound` 的 `status_rx.changed()` 分支（`:273-287`）产出 `ServerEvent::Pong` 并 `server_event_tx.send`；未知客户端 `:229-236` 直接 spawn 发 `Pong{Unknown}` 入同一通道。该通道即 `websocket.rs:410` 的单一 `mpsc::channel(CHANNEL_CAPACITY)`（`:412` tx 传入 ClientTracker，`:443` rx 由 writer loop `:994/:1020` 消费）——writer loop 对通道内**一切** `QueuedServerEnvelope` 无条件做 per-(client,stream) seq 分配（`:1031-1045`），拆分后逐信封序列化并 `state.outbound_buffer.insert(&server_envelope)`（`:1046-1062`）；`BoundedOutboundBuffer::insert`（`:91-99`）**无事件类型过滤**，pong 同样使全局 `used += 1`。即：seq 空间是**信封级**（ServerMessage + Pong 共享同一单调计数），不是方案所述的纯「消息级」。
  2. **MacBridge 的 ping/pong 是应用层且每 10s 一次**：`ws.go:317` pingInterval 10s，`ws.go:312` 启动 `keepStreamAlive` → `streamHealthCheck` → `stream.Ping()` 写 `Type: typePing` 信封（`stream.go:176`）；pong 于 readLoop `typePong` 分支到达（`:307-314`）——本仓生产代码依赖 pong 到达（`stream.go:44-47`：active pong 是 host 侧存活证据）。
  3. **§3.3 判定表只定义 plain/chunk 两类帧型**：四条判定分支（`< lastSeq` / `== lastSeq`×两形态 / `> lastSeq`）的游标更新均只写了「plain 置 lastSeg 空 / chunk 置 lastSeg」，pong 在 §3.3 全节零出现（grep 亲证：pong 仅出现于 §2.2 判活行、§2.3 检测行、§3.7 probe 帧型、§4 E-8、§5 S-7、§7.1 处置表）。MacBridge 现状 `s.ack(env)` 仅 `:324`/`:339` 两处（消息分支），typePong 分支无 seq 处理——实施者按方案字面把游标逻辑落在消息分支，pong 的 seq 将被跳过。
  4. **假缺口是确定性的、每 10s 一次**：3s catalog 循环（`main.go:1046-1066` → `session.go:328-337` 每轮 `thread/loaded/list` RPC）保证消息帧稳态到达，故每个 pong（每 10s 一条）恒定插在相邻两条消息的 seq 之间（消息 seq=N，pong seq=N+1，下一条消息 seq=N+2）。若游标只在消息帧推进：下一条消息 `N+2 > lastSeq+1 = N+1` → 记录假缺口 `(N+1, N+1)` 一次并触发下游——**每流每 10s 恒发**；若连接上无消息（理论静流），首个 pong 后的首条消息同样命中。
- **影响**：① §1 验收 4「无缺口时零开销」被系统性破坏——每 10s 一条假缺口日志 + fan-out 把 observed∩turnByThread 非空的全部 thread 加入对账 pending 集，有在飞 turn 时每 10s 一轮权威 `ReadColdHistory` 拉取（真实 RPC 负载，恰是方案要消灭的「对网络要求太高」形态）；② 同条验收的「缺口在桥日志可查」降级——真缺口信号被 10s 周期的噪声淹没；③ §2.2/E-11「`ServerEnvelope.seq_id` 按 (client_id, stream_id) 消息级严格单调」表述在信封级事实上不完整，「消息级缺口即真实丢失」仅在全信封游标下成立（缺口可能只是一条丢失的 pong——无害但必须如实分类）；④ §5 S-3 计划单测（注入缺口/重放 chunk/负例）不含 pong 交错用例，**测不出该缺陷**；§4 E-12b 捕获清单五项不含 pong 帧，pending 门也无法暴露；仅 E-10 真机观测可见，而方案把误报留给「误报则收紧判定条件」的实施期反馈环——按本链 r4/r5 先例（设计文本与官方源码行为冲突，必须在方案层修订，不能留给实施者发现；r1 F-1 同类先例：规则与官方 chunk 共享 seq 语义冲突判阻塞），构成阻塞。该缺陷自 v1.2 的 F-1 重写起潜伏，r2~r7 七轮均未检查 pong 的 seq 参与（**定性：旧问题漏检**；F-R4-2 的缺口归属/fan-out/一次触发闭合不受影响——受影响的是同一子系统内一条相邻规则）。
- **修订方向**（最小充分，四处）：① §3.3 判定表补一条规则——高水位游标对**一切携带 seq_id 的入站信封**推进：pong 计入（`seq > lastSeq` → 推进并置 `lastSeg` 空；pong 恒为新 seq，不触发表内「理论不可达」分支——同消息 chunk 的连续性由官方 writer loop 单队列原子写出保证，`websocket.rs:1046-1062`）；「消息级」表述改为「信封级」，缺口语义改为「任意信封丢失（可能是一条无害的 pong——维持 fail-visible，日志如实标注帧型可能性）」；② §2.2「入站 seq 语义」行与 §4 E-11 行同步补限定「pong 与消息共享 per-stream seq 空间（`client_tracker.rs:221-237`/`:273-287` → `websocket.rs:1031-1067`；`insert` `:91-99` 无类型过滤）」；③ §5 S-3 单测补负例「pong 交错于两条消息之间（seq N / pong N+1 / 消息 N+2）→ 不触发缺口」；④ §4 E-12b 最小样本集补第⑥项「≥1 条 pong 帧的 seq 形状（携带 seq_id、与消息 seq 共享单调序列）」——该 wire 声明自此成为 S-3 缺口规则的前置事实，纳入 pending 门一并确认。S-7 的 probe ack 输入（最高已见游标）随修复自动一致：含 pong seq 的游标按官方 ack 语义清除 host 缓冲内 pong，正确。
- **闭合标准**：v1.8 的 §3.3 判定表对 pong（及一切带 seq 信封）可判定、全文无「消息级」误导残留（§2.2/§4 E-11 同步）；§5 S-3 含 pong 交错负例；E-12b 清单含 pong 项。修订后**只需定向复核 §3.3/§2.2 seq 行/§4 E-11·E-12b/§5 S-3 四处**，不需全量重审。

---

## 5. 复审处置与回归核查

### 5.1 r4 §6 修订后复审清单（六项）本轮终态

| # | r4 清单项 | 本轮裁决 | 依据（本轮亲核） |
| --- | --- | --- | --- |
| 1 | S-2 READY-safe reconcile transaction，不丢 older windows/producer state | **闭合（维持）** | §3.1 表全链独立重推：admission 两分支不可行、committed 快照 baseline、reconcile 标志跳过两类 merge（`:1504` 证完备）、第四写者枚举独立复跑（无第四写者）、producer seed 门控跳过（`:1182-1184`）、prepend 硬门；§5 S-2 断言含 F-R4-1/F-R5-1 双闭合标准 |
| 2 | S-3 gap 归属 stream 级 + fan-out/游标推进测试 | **闭合（维持）——但同子系统新发现 F-R8-1** | F-R4-2 的归属/fan-out/一次触发/无在飞 turn 边界经 §3.2/§3.3 独立复核全部成立；F-R8-1 是相邻的新缺陷（游标对 pong 帧的处理），不推翻 F-R4-2 闭合，但使 S-3 整体回到待修订状态 |
| 3 | S-7 可测 breaker 状态机、删伪事实 | **闭合（维持）** | §3.7 事实前提（`:44-47`/`:53-57`/`:359-373`/`:305-314`/`ws.go:317-318`）本轮亲核吻合；八断言在位（grep 亲证「八断言」为现行文本、「七断言」仅历史记录）；F-R8-1 修复后 probe 游标输入自动一致 |
| 4 | E-12 真实样本或降级 pending 严格门住 | **闭合（维持）** | E-12a verified / E-12b pending、整门不标 verified；门住 S-3/S-7 八处一致（§7）；attempt-008/phase2 不可替代前提本轮 grep 重验成立；F-R8-1 要求捕获清单补 pong 项（修订后复核） |
| 5 | S-1b retryAfterMillis 通路或删承诺 | **闭合（维持）** | 选项 b 删承诺；iOS 锚点本轮亲核（outcome 只带 code/message、灾难 loop 恒本地退避、retryable 字段未读取）——删承诺零行为损失 |
| 6 | 更新来源快照后做定向复审 | **闭合（维持）** | §2.1 历史快照标注 + 实施来源门模板在位；本轮即复审；评审期间 iOS main 未再漂移（ccb5a0df 不变）、配套树停在 61e5c32e（四锚点文件零改动亲证） |

### 5.2 历轮意见处置裁决（回归清单）

| 意见 | 本轮裁决 | 依据 |
| --- | --- | --- |
| r1 F-1~F-6（2 阻塞 4 建议） | 维持闭合 | 承重锚点本轮抽样重核吻合（chunk 共享 seq `segment.rs:463-467`、E-11 清除仅 ack、S-2 四接线宿主、S-1a 枚举、S-4 revoked 去向、backoff 归属）；对应文本不在 v1.7 改动范围（§6） |
| r2 R2-A1~A4 | 维持闭合（r5 确认后无回归） | 游标推进/归属（随 F-R4-2）、S-7 依赖列、解除=流重建、行号精度——现行文本本轮亲读一致 |
| r2-meta 两项 / r3 勘误六项 / r6-meta 两项 | 维持闭合 | §2.1 六树记录、七列表、§8.2/§8.3 勘误记录在位；勘误-3/4 内容与 r6-meta 逐点一致 |
| r4 F-R4-1~4 + R4-A1/A2 | 维持闭合（#1 经 v1.5 F-R5-1 修订，本轮独立重推确认） | §5.1 |
| r5 F-R5-1 + F-R5-A1~A3 | 维持闭合 | §3.1/§3.2 本轮独立亲核（commit 链全吻合、三分支镜像 §9.2、manifest 锚点、pong 触发帧型+断言⑧） |
| r6 A-R6-1（建议） | **维持开放（实施前随手补入），处置合规** | v1.7 未夹带（grep 亲证 `finishHydrateLocked` 在 §3.2 Abort 条仍无该句，仅存于 §8.2 未夹带说明/§4/§6 登记处）；事实基础（`:957-962`/`:917`）本轮亲读吻合；r6 闭合标准（不构成实施前置）继续有效 |
| r7-meta 问题（r7 报告 39 字符哈希） | **闭合** | 勘误-5 全部事实声明本轮亲跑复现（§6.1）；r7 报告该行仍未直接更正（未跟踪文件，属报告作者/owner 权限），以 §8.1 勘误-5 为可追踪补正——r7-meta 给出的两条修复路径（直接更正**或**可追踪勘误记录）其一已满足 |
| r7 APPROVED | **部分暂停**：S-3/S-7 范围的通过依据因 F-R8-1（新反证）暂停有效；S-1a/S-1b/S-2/S-4/S-5/S-6 维持 | plan-contract「通过后纠错」：改变验收成立性的错误使相关通过依据暂停；F-R8-1 仅触及 §3.3 缺口检测（S-3）与 S-7 的共享游标输入，不触及其余切片设计（§3.1/§3.2/§3.4/§3.5/§3.6 本轮独立核验均成立） |

**回归检查**：v1.7 修订（勘误-5 + 状态注记）未引入任何源码引用错误；稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3）不变，亲核属实。

---

## 6. v1.6→v1.7 增量核验（勘误轮纪律）

**方法**：v1.6 字节快照不可从 git 复原（HEAD 73539b71 内为 v1.4 §3.2 时点半成品），以 r7/r7-meta 报告引用的 v1.6 原文 + v1.7 头部声明范围（「设计内容与方案来源记录零改动；唯一动作 = §8.1 勘误-5 + §4/§6/交接块状态注记同步」）交叉印证。

### 6.1 勘误-5 事实声明逐项亲跑核验（全部吻合）

| 勘误-5 声明 | 本轮核验（命令与结果） | 结果 |
| --- | --- | --- |
| 39 字符值 `73539b71c0c352a3c16548d05417813ba907869` 无法解析（exit=128） | `git rev-parse <39字符>` 亲跑：`fatal: ambiguous argument … unknown revision`，exit=128 | ✓ |
| 实际 HEAD 为 40 字符 `73539b71c0c3552a3c16548d05417813ba907869` | `git rev-parse HEAD` 亲跑吻合；第 14 位缺 `5` 的定位准确 | ✓ |
| 前 13 字符 `73539b71c0c35` 可唯一定位实际提交 | `git rev-parse 73539b71c0c35` 亲跑 → 唯一解析为 40 字符 HEAD | ✓ |
| r7 报告第 45 行为 39 字符原误、第 46 行对照组（Mac main 同一提交）记录正确 | `sed -n '45p'` 亲跑：第 45 行恰含 39 字符值（`grep -oE` 长度亲测=39）；第 46 行为 40 字符正确值 | ✓ |
| 方案 §2.1 自始记录 40 字符正确值、全文无 39 字符残留（来源声明语境） | grep 亲跑：40 字符值在 §2.1 两处亲核记录（v1.7 行 76/79；v1.6 框架下行 75/78——见下「零后果观察」）；39 字符串仅出现于勘误-5 记录自身（文档头第 5 行 + §8.1 表，均为错误登记而非来源声明） | ✓ |
| 正文哈希口径（head -n 373）与交接块自报一致 | 亲算 = `b35c431ade9b719fc211efae660ad18406047383b81a864dee5d40dd4fb081a7`，吻合；交接块注记的 v1.6 正文哈希 `8e3ce9e3…` 与 r7 报告头记录一致，更正关系链完整 | ✓ |

### 6.2 状态注记同步与无夹带

- **§4 复审门 / §6 下一阶段入口 / 交接块 review_round**：三处 r7/r7-meta 状态（r7 APPROVED 0/0、r7-meta confirm=false 单问题、勘误-5 处置路径、A-R6-1 维持实施前随手补入）与两份报告实况逐处吻合，非超前自判 ✓。
- **设计内容零改动**：r7 所验 v1.6 承重文本（§3.2 Commit 段 `:1163-1178` 锚点、F-R5-1 reconcile 标志链、§3.7 八断言与 probe 帧型、§5 S-2/S-7 行）与当前 v1.7 逐点一致；勘误章节重编号（v1.6 勘误 §8.1→§8.2、v1.3 勘误 §8.2→§8.3）的交叉引用已全部同步修正（grep 亲证：v1.6 头指 §8.2、v1.7 头指 §8.1、§4 指 §8.2 未夹带说明——无悬空引用）✓。
- **无夹带**：v1.7 声明范围（勘误-5 + 状态注记 + 文档头/交接块）与观测增量一致；「八断言」为现行文本、「七断言」仅存历史记录（grep 亲证）；A-R6-1 未夹带（§5.2）✓。
- **零后果观察（不计意见，如实记录）**：① v1.7 头注「§2.1 第 75/78 行记录 40 字符正确值」沿用 r7-meta 对 **v1.6** 的行号框架——v1.7 自身因新增头注 bullet 使该两处后移至 76/79 行；勘误引用历史发现时沿用发现时框架、内容可验证正确，无需修订。② 「全文无 39 字符残留」按来源声明语境成立（39 字符串仅存在于勘误记录自身的错误登记内）。③ 本轮曾推演「空闲流 pong 在 host 全局 buffer 无界累积 → 无熔断也停摆」的可能新缺陷，**亲核后排除**：3s catalog 循环（`main.go:1046-1066` → `session.go:328-337` `thread/loaded/list` RPC）保证消息帧 ≤3s 到达、ack 游标持续推进并顺带清除在途 pong，空闲累积上限 ~1 条——§6 风险 5「在途窗口通常 0–2 条」定量在现网成立，不构成问题。

---

## 7. 门控一致性、audit-plan 专项与交接块

- **E-12 pending → 门住 S-3/S-7 整体实施**：§3.3 尾句（:196）、§4 E-12 行（:262 阻塞列）+ Gate A 段（:266）、§5 S-3/S-7 依赖列（:275/:279）+ 依赖序段（:281）、§6 验证分层（:286）+ 阻塞清单（:297）+ 下一阶段入口（:299「S-3/S-7 待 E-12 解锁」）、交接块 open_gates——**八处一致**（grep 亲证）✓；S-2 不依赖 E-12 ✓。
- **OD-1 pending → 门住 S-1a/S-1b**（§3.8/§4 Gate B/§5 三处）；OD-2 decided（think.md:45 亲核）→ S-6 仅桥内；OD-3 随 E-9；E-9 → S-4 整体；E-10 → S-1/S-2/S-3 完成验收 + S-7 可见性——各处一致 ✓。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2）、R-3→S-4（E-9 门内）、R-4→S-3+E-12（验收 4——**F-R8-1 直接影响此链**：假缺口噪声使验收 4 的「零开销/可查」两半均不成立）、R-5→S-6——对应齐全 ✓。
- **audit-plan 专项**：E-12a（源码事实）锚点本轮亲证；**E-12b 如实 pending**——§4 状态列明「同一门有未完成子项，整门不得标 verified」，未伪装 verified；捕获清单（版本锚定/最小样本集五项含 plain 负例与 absent-vs-null/双独立提取策略/脱敏/计划路径「尚不存在」/授权边界）完整且与 audit-plan 纪律一致；「attempt-008 与 phase2 fixture 不可替代 live wire」前提本轮 grep 重验成立。**F-R8-1 修订后**：pong 帧 seq 形状成为 S-3 缺口规则的新前置 wire 声明，必须补进捕获清单（第⑥项）随 E-12b 一并确认——在补入前，S-3 的缺口检测设计依据存在一项无锚点、无样本计划的外部形状假设（这正是本轮按简报重点⑤核出的缺口）。
- **交接块**：open_gates [OD-1, OD-3, E-9, E-10, E-12] 与 §6 阻塞清单一致 ✓；review_round 链（r1→…→r7→r7-meta→v1.7）与实况一致 ✓。

---

## 8. 剩余门与下一阶段

- **本轮意见**：F-R8-1（阻塞，§3.3 pong/信封级游标规则）——修订量小（§3.3 判定表一条规则 + §2.2/E-11 表述限定 + §5 S-3 一个负例 + E-12b 清单一项），修订后**只需定向复核该四处**，不需全量重审。
- **复审门状态**：F-R8-1 修订 + 定向复审前，S-3/S-7 回到阻塞未闭合状态；S-2 维持闭合（其设计不依赖 seq 游标规则）。
- **仍待满足的实施门（与意见分开）**：OD-1（owner 确认重试语义）、OD-3（随 E-9）、E-9（ctrlExp fixture ≥5 样本）、E-10（受控断线复现，实施期）、E-12（E-12b wire 样本，owner 授权后按 §4 捕获清单——含修订后补入的 pong 项——采集）。
- **待办（非门）**：A-R6-1 实施前随手补入（§3.2 Abort 规格 `finishHydrateLocked` 关闭句 + 可选等待者释放断言）。
- r7 报告第 45 行的直接更正仍属报告作者/owner 权限（未跟踪文件）；未更正前以方案 §8.1 勘误-5 为可追踪补正（r7-meta 两条修复路径其一已满足，r7 APPROVED 的恢复确认在本轮对其设计内容部分被 F-R8-1 取代——S-3/S-7 范围以 F-R8-1 闭合后的定向复审为准）。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证、E-12b wire 捕获与 UI automation 另行授权。实施前按 §2.1 实施来源门模板现场重跑来源清单。

---

## 评审员交接块

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: b7e3bcf837f2c5c2cccfeeee7ee404f2578493512372b3dc9118ea81905af23c
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r8.md
scope: full
blockers: 1
advisories: 0
open_gates: [OD-1, OD-3, E-9, E-10, E-12]
round_complete: true
contract_version: plan-contract-v1.1
```
