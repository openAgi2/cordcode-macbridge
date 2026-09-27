# codex-remote 断线韧性与恢复专项方案 评审报告（r5）

- 日期：2026-09-27
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（v1.4——r4 REVISION_REQUIRED 后的修订轮）
- 评审时方案 SHA-256：`b919e4d4ada90342c2a405353080e7d6f596c5d3025c62ada025a4f506ef44fc`（整文件 `shasum -a 256` 亲算，**与派发记录一致**——评审对象身份确认）；正文哈希 `head -n 333 | shasum -a 256` = `35294009af5fe8c84f6bab1e2e880d9d467361b6f5965b5b4c0b106897acfb91` 亲算，与方案内设计师交接块自报一致
- 评审范围：**full**（全量评审；评审员模式 fresh——先独立形成风险清单并逐锚点亲核，后读 r1/r2/r2-meta/r3/r3-meta/r4 作回归清单；重点按简报：①v1.4 三处新设计独立源码核验 ②r4 §6 六项闭合对照 ③v1.3→v1.4 diff 无夹带 ④门控一致性 ⑤audit-plan 纪律）
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md + audit-plan-adapter.md + audit-plan SKILL.md 均已读取；audit-plan 接入已按 adapter 执行，见 §6）
- 结论：**REVISION_REQUIRED**（blockers 1，advisories 3）
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、产品代码、历轮报告、两仓源码、官方 checkout 一律只读，未 commit、未部署、未构建、未采集任何真机/wire fixture（E-12b 需 owner 授权，未触碰）。

---

## 1. 结论

### verdict: REVISION_REQUIRED（不通过，需修订）

v1.4 对 r4 四阻塞中的 **F-R4-2 / F-R4-3 / F-R4-4 三条已完整闭合**（本轮独立亲核证实），两条建议 R4-A1/R4-A2 与历轮升级项 R2-A1~A4 全部闭合，E-12a/b 拆分与 pending 门符合 audit-plan 纪律，v1.3→v1.4 修订未夹带无关改动、未破坏 r1~r3 已核内容，门控联动一致。

但 **F-R4-1（S-2 READY-safe 对账事务）在 commit 腿上未闭合**——本轮新发现一个阻塞级设计缺陷 **F-R5-1**：方案声称「既有 `CommitHydrateTransaction` 零改动——baseline 经 `Restore` 原子发布」，但该函数在 Restore 之前还有一步方案未提及的 `mergeHydrateBaselineWithLiveExecution`（`projection_kernel.go:1353-1357`），其第一分支（`:1457-1473`）会把冻结 committed 快照中仍在飞的 Execution 覆盖回 baseline、并把已终态的 active turn **重置回 running、CompletedAt=0**。对账的主场景（在飞 turn 断线期间完成）恰好落入该分支：commit 后 turn 回到 running、Execution 回到 running——**对账被静默撤销**，而 runner 按摘要已清除 codec `turnByThread` 条目，重试触发器同时被移除。R-2 与 §1 验收 2 在该场景下失败。证据链与推演见 §4 F-R5-1。

修订量小：§3.2 Commit 段补一处「对账事务绕过/反转该 merge」的机制（如 tx 标志位使 `:1352-1356` 跳过 merge，或专用 commit 入口共用 Restore+drain 机制），Abort 腿不受影响（无 merge 参与）。修订后只需定向复核 §3.2 commit 腿与 §5 S-2 断言，不需全量重审。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**。
- 实施前置门（与意见分开列）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（随 E-9）、**E-9**（ctrlExp 真机 fixture，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期）、**E-12**（E-12b wire 样本，owner 授权后按 §4 捕获清单采集，门住 S-3/S-7 整体实施）。
- 复审门：F-R4-2/F-R4-3 闭合经本轮确认；**F-R4-1 因 F-R5-1 维持未闭合**——S-2 实施继续被复审门挡住。
- E-10 真机验证与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核；读取源码前重新生成）

| 仓库 | 工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期来源 / 配套组合 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 73539b71c0c3552a3c16548d05417813ba907869 | 仅方案文档 1 处未提交修改（v1.4 完整版，评审循环正常形态）；`git status --porcelain` 亲跑 | 简报①指定树；HEAD 73539b71 为 owner 提交的半成品快照（§3.2 时点）+ r4 报告，完整 v1.4 以工作树为准（与简报一致，本轮亲核 `git diff 5d1c9ae6..73539b71 --stat` 纯 docs 2 文件、`git diff 07721783..73539b71 --stat` 全部 9 文件均为 docs/*.md——**代码零改动**，Mac 锚点与 07721783 等价） | 纯设计文档评审，无构建/安装——N/A（无构建场景） |
| cordcode-macbridge（main 树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge | main | 73539b71（`git worktree list` 亲跑，owner 已同步至同一提交——与简报声明一致） | — | 枚举记录 | N/A |
| cordcode-macbridge（第三树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge-plan-approval | detached | b2b25235 | — | 枚举记录 | N/A |
| cordcode-ios（iOS 锚点核验来源，简报③指定） | /Users/jacklee/Projects/cordcode-ios | main | ccb5a0df647865324e11aedebbb089b9fc07bbb5 | 干净（`git status --porcelain` 亲跑为空） | 简报③指定 main 工作树；四锚点文件 c49bdffe→ccb5a0df 零改动本轮亲跑 diff 证实（简报声明复核一致） | N/A（纯设计） |
| cordcode-ios（同族配套树，P0 要求记录） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | **0ad7b761671649ce890bf0464dcbd1760d2490b0**（本轮亲核；简报派发时点为 ccb5a0df，评审期间又前进 1 个 docs(plan) 提交——移动目标如实记录） | 干净（亲跑为空） | 非本任务锚点来源；记录以满足 P0「含配套工作树，无论是否实际引用」。**锚点有效性**：`git diff --stat ccb5a0df..0ad7b761 -- <四锚点文件>` 亲跑为空——四锚点文件在 main/配套两候选下均零改动，iOS 锚点结论与工作树选择无关 | N/A |
| openai/codex（官方，只读） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD 亲核） | e72da2b53805894878023d01949a25a082e0a5cb | 干净 | 简报④：一律以 FETCH_HEAD 为准（亲核一致，与方案 §2.1 记录相同） | N/A |

**评审对象身份**：派发哈希与读取哈希一致（报告头）；v1.3 基线经 `git show 888e7196:…plan.md | shasum -a 256` 亲算 = `811ee452…`，与 r3/r4 评审对象一致——v1.3→v1.4 diff 以提交 888e7196 为基线逐 hunk 核验（见 §5.3）。

### 2.2 覆盖声明

- 本轮为 full 全量评审，重点覆盖：**§3.2 READY-safe 对账事务**（kernel admission/restore 分支/commit/abort/并发边界/producer 过滤规则全部锚点亲核，约 25 个）、**§3.3 缺口归属与游标推进**（envelope/stream/官方 ack 游标亲核）、**§3.7 状态机**（stream.go 熔断/ack/注释/官方退避/think.md 事故形态亲核）、**E-12a/b 拆分**（attempt-008 与 phase2 fixture 本轮重新亲读）、**iOS 四锚点文件 @ ccb5a0df**（行号逐一亲核）、r4 §6 六项闭合对照、R2-A1~A4 闭合、v1.3→v1.4 diff 逐 hunk 归属、门控一致性（§3.8/§4/§5/§6/交接块五处联动）。
- 复用（按命题/锚点粒度，依赖已核：Mac 代码 07721783→73539b71 零改动亲证、iOS 四锚点文件跨候选 ref 零改动亲证、官方 checkout e72da2b5 未变亲证）：v1.2/v1.3 已核且 v1.4 未改动的锚点——§2.2 复用表其余各行、§2.3 现状摘要、§3.1 S-1a 分类、§3.4/§3.5/§3.6、E-1~E-8/E-11 各行、§7.2/§8——按 r1/r2/r3 报告记录复用，未重查；其中承重的 `websocket.rs:112-138`（ack 游标）、`:1031-1067`（seq 消息级分配）、`stream.go:390-407`（ack 无 SegmentID）、`envelope.go:45`、`backoff.go`/`ws.go`/`pairing_persist.go`/`session.go`/`codec.go`/`agent.go`/`main.go` 各段本轮又抽样亲核吻合。
- 未核项（如实声明）：① relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码；② E-9/E-10/E-12b 为 pending 运行证据，按授权边界本轮未采集，状态如实；③ v1.2 字节快照不可复算（历史形态，r3 已以 hunk 归属分析替代，本轮不重复）。

---

## 3. 锚点核验表（本轮承重项；「✓✓」=定位与语义均成立）

### 3.1 §3.2 READY-safe 对账事务（v1.4 新设计，全部本轮亲核）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `projection_kernel.go:1020-1026` READY pathless + `sourceChanged=false` → AlreadyReady | 同 | `:1020-1024 // Pathless backends…sourceChanged forces a full rich-history rebuild… if source.Path == "" && sourceChanged { break }`；`:1025-1026 k.mu.Unlock(); return ProjectionHydrateAdmission{AlreadyReady: true}, nil` | ✓✓（codex-remote pathless 无 Segments、三条 break 均不中 → AlreadyReady no-op，「不强制=不对账」成立） |
| `:969-976` `pathlessRichHistoryBackend` 名单不含 codex-remote | 同 | `:970-975 switch backendID { case "opencode", "grokbuild", "claude", "claudecode", "opencode-web", "codex-web": return true; default: return false }` | ✓✓（codex-remote → false → `pathlessFullRebuildSource=false`） |
| `:1069-1083` 唯一 restore 分支要求 `!sourceChanged` | 同 | `:1069 if source.Path == "" {` `:1070-1072 if pathlessFullRebuildSource…do NOT Restore` `:1077 else if !sourceChanged && len(source.Segments) == 0 {` `:1080-1082 if existing, ok := k.reducer.Snapshot(…); ok { tx.reducer.Restore(…) } }` | ✓✓（codex-remote + `sourceChanged=true`：三分支均不中 → tx.reducer 保持 `:1062 NewProjectionReducer()` 空 reducer 起步，「强制=page-1 rebuild 破坏时间线」成立） |
| `:1357` commit `Restore` 整体替换 | 同 | `:1357 k.reducer.Restore(backendID, sessionID, baseline)` | ✓✓ 定位成立；**但其前有 `:1353-1356` merge 步骤方案未提及 → F-R5-1** |
| `:1393-1394` source cut 写回 | 同 | `:1393 session.committedSourceCursor = tx.startCut`；`:1394 session.committedSource = cloneProjectionSourceDescriptor(tx.source)` | ✓✓（对账 tx 携带 committed 原值 → 写回不变，「不移动 source cut」成立） |
| `:1092-1095` 安装序 status/hydrate/hydrateDone | 同 | `:1092 session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateHydrating}`；`:1093 session.hydrate = tx`；`:1094 session.hydrateDone = make(chan struct{})` | ✓✓ |
| `:1203-1211` IngestLive fence → pendingLive 深拷贝排队 + Deferred | 同 | `:1204 if session.status.Phase == ProjectionHydrateHydrating && session.hydrate != nil {` `:1205-1206 queued := msg; queued.Data = cloneProjectionJSONValue(msg.Data)` `:1207 session.hydrate.pendingLive = append(…)` `:1212 return ProjectionIngestDeferred` | ✓✓（「不新增 fence 机制、无需改 IngestLive」成立；亦证实对账窗口期 committed reducer 被冻结——F-R5-1 推演的前提） |
| `:1160` ApplyHydrateEvent 仅改 tx-local reducer；armed 集来自事件 turnId/itemId | 同 | `:1168 if session.status.Phase != ProjectionHydrateHydrating \|\| session.hydrate == nil { return false }`；`:1176-1180 tx.coldArmedTurnIDs[tid/iid]` | ✓✓（「armed 集只含收到终态事件的 turn」与源码 arm 机制一致） |
| `:1319` CommitHydrateTransaction；commit 不检查 `sourceIngestComplete`；pendingLive 按戳序 drain `:1358-1374`；返回 AppliedPendingEventIDs | 同 | `:1319 func (k *ProjectionKernel) CommitHydrateTransaction(…)`；函数体 `:1322-1404` 全文无 `sourceIngestComplete` 引用（该标志仅在 `WaitHydrateCommitReady :1302` 检查）；`:1358-1374 for _, msg := range tx.pendingLive { …k.reducer.Apply(msg)… }`；`:1402 AppliedPendingEventIDs: appliedPendingIDs` | ✓✓（「kernel commit 不检查该标志、gate 由 producer 同步性保证」成立；Abort 返回 EventIDs 的设计镜像既有 commit 返回值先例） |
| `:925-951` MarkFailed 置 Failed 且丢弃 pendingLive；`:911-918` MarkReady 不 drain pendingLive | 同 | `:951 session.status = …Failed…`；`:952 session.hydrate = nil`（pendingLive 仅用于收集 deferredEventIDs 供调用方**丢弃**，`:943-950`）；`:911-918 MarkReady: `:915 Phase=Ready`、`:916 session.hydrate = nil`，无 drain | ✓✓（「不能用 MarkFailed/MarkReady」的两条理由均与源码一致，AbortReconcileTransaction 的 drain-回放设计必要且成立） |
| `:1680-1684` PrependHistoricalTurns Ready 硬门 | 同 | `:1680-1685 if session.status.Phase != ProjectionHydrateReady { return …, fmt.Errorf("projection: prepend requires ready session…") }` | ✓✓（窗口期 older-walk 诚实失败、客户端可重试成立） |
| Hydrating 分支返回 Done（单飞行） | `:1027-1034` | `:1027-1034 case ProjectionHydrateHydrating: admission := ProjectionHydrateAdmission{Done: session.hydrateDone}…return admission, nil` | ✓✓（「窗口期冷开 pull 加入单飞行等待对账结束」成立） |
| `handlers_projection.go:876-881` codex-remote pathless source | 同 | `:876-881 return ProjectionSourceDescriptor{Identity: sessionID, Path: "", Cursor: 0}, nil` | ✓✓ |
| `:1161-1177` commit 后处理；`:1182-1184` persistCodexProducerSeed | 同 | `:1161 h.releaseDeferredPushCandidates(commit.AppliedPendingEventIDs)`；`:1176-1177 h.eventPublisher.PublishProjectionPatch(…)`；`:1181-1183 if backendID == "codex-remote" { h.persistCodexProducerSeed(…) }` | ✓✓（「runner 复用后处理但不调用 persistCodexProducerSeed——producer cursor/seed 全程不动」可实施；kernel commit 函数体内无 checkpoint 持久化） |
| `:1372-1385` 桥侧 ReadColdHistory 消费先例 | 同 | `:1372 case "codex-remote":` → `:1377-1378 if reader, readerOK := agent.(core.ColdHistoryReader); readerOK { return h.streamCodexRemoteColdHistoryEvents(ctx, reader, …) }` | ✓✓ |
| `history_paginated.go:832-852` in-progress 走查 items 到 EOF；`:856-923`/`:871-941` 仅最新一页 | 同 | `:830 mapColdPage`；`:833-848 if envelope.Status != remoteTurnStatusInProgress…continue; for { page, err := a.ReadTurnItemsPage(…); if page.EOF { break } }`；`:871 ReadColdHistory` paginated 分支 `:910 readTurnsPage(ctx, threadID, "")` 单页后 `:918-920` 逆序转 asc | ✓✓（「诚实成本注记」与「仅最新一页」均成立） |
| `readTurnsPage` agent 包内私有 | `history_paginated.go:237` | `:237 func (a *Agent) readTurnsPage(ctx …)`（小写私有） | ✓✓（「桥不可直达、不另造窄接口」成立） |
| `projection_window_older_hydrate.go:154-221` 旧页经 PrependHistoricalTurns prepend 进 committed | 同 | `:154-156 runOlderHydrationLocked` 注释 "fetch ONE page…prepend → persist"；`:215-221 saveProducerState + PublishProjectionPrepend` | ✓✓（「已 prepend 旧页会被 page-1 rebuild 抹掉」的论证成立） |
| `projection_reducer.go:336-376` upsertTurn merge 语义 / 缺席则 append 造 ghost | 同 | `:338-340 if t := ps.turnByID(…); t != nil { // Merge: Keep existing…`（逐字段非零覆盖）；`:382-385 ps.projection.Turns = append(ps.projection.Turns, turn)` | ✓✓（规则 2「对不存在 turn 发终态事件会造 bare ghost」成立；merge 保留内容成立） |
| `projection_reducer.go:377-381` per-turn manifest 字段 | **实际不符**：`:377-381` 为 upsertTurn merge 分支收尾（upsertTurns 簿记 + return），无 manifest 字段；per-turn detail manifest 字段实际在 `projection_types.go:221-226`（`DetailLoadState/DetailReasonCode/DetailInline`，TurnProjection 结构体成员） | — | 定位不成立/语义另核成立（TurnProjection 全字段随快照序列化，Restore 携带 → baseline 保留成立）→ **F-R5-A2** |
| `codec.go:372-381` decodeTurnCompleted 状态分支 | 同 | `:372-381 if params.Turn.Status != remoteTurnStatusCompleted { …event.Type = core.EventError…return }；event.Type = core.EventResult` | ✓✓ 定位成立；**但该路径产出的是 `core.EventError` → `mapAgentEvent`（go-bridge/events.go:221-227）→ 裸 `"error"` 事件名，非 `turn_error`**——规则 1 所引「镜像」不支持其 interrupted/failed→turn_error 映射 → **F-R5-A1** |
| `mapAgentEvent events.go:174/211/221` | **go-bridge**/events.go（非 agent 包；仓库内唯一 mapAgentEvent） | `:174 case core.EventResult:`；`:187 return "turn_error"…`（Done+Error）；`:211 return "turn_completed"…`；`:221 case core.EventError:` → `:226 return "error"…` | 定位成立（行号精确）/语义部分成立 → F-R5-A1 |
| 冷路径 §9.2 封口纪律（规则 1 应镜像的真值源） | `handlers_projection.go:1573-1575`、`:1700-1718` | `:1573-1575 // completed → turn_completed；failed → turn_error（官方 error.message 透传）；interrupted → turn_aborted`；`:1712-1718 case "interrupted": out = append(…Event: "turn_aborted"…)` | ✓✓（**interrupted → turn_aborted**，与方案规则 1 的 interrupted→turn_error 偏差 → F-R5-A1） |

### 3.2 §3.3 缺口归属与游标推进（v1.4 新设计）

| 方案原引用 | 实际位置 | 源码摘录 | 定位/语义 |
| --- | --- | --- | --- |
| `envelope.go:32-49` transport Envelope 无 thread 身份 | 同 | `:32-49 Envelope struct { Type/ClientID/EnvID/StreamID/SeqID/Cursor/SkipHistory/Message/Status/State/SegmentID/SegmentCount/MessageSizeBytes/MessageChunkBase64 }`——无 thread 字段 | ✓✓（「stream 级未知归属」的 transport 事实成立） |
| `stream.go:409-420` chunk 重组前无可解析 payload | 同 | `:409 observeChunk`：要求 SeqID/SegmentID/SegmentCount，`:417 decodeChunk(env.MessageChunkBase64)` 重组 | ✓✓ |
| 官方 `websocket.rs:112-138` ack 游标 / `:1031-1067` seq 消息级 | 同（本轮重读） | `:123 let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));`；`:129 is_acked = envelope_cursor <= acked_cursor`；`:1035-1038 entry(seq_key).or_insert(1)` | ✓✓（去重判定表与缺口检测依据成立；复用+本轮重读） |
| `websocket.rs:965-988` host 重连重放携带原 seq（replay 取舍前提） | 同 | `:965-971 server_envelopes().cloned().collect()` 后逐条重发 | ✓✓（缺口后重放被游标判重丢弃的推演前提成立；E-11 复用+重读） |

§3.3 设计逻辑本轮独立推演：缺口记录一次 + 高水位立即推进（`lastSeg` 按帧型置位）→ 同一缺口不重复触发、新缺口是新事件 ✓；有界 fan-out（observed∩turnByThread 幂等并集 + 收口清 `turnByThread` 防反复进集）✓；无在飞 turn 只记日志 + lazy 重载/冷开兜底的诚实边界 ✓；replay 取舍（接受重放需未验证的 reducer 迟到内容保证，已否决并登记 §6 风险 3 残余）✓。fan-out/信号 seam 均可落在 agent 包内（stream.go/codec.go/session.go 同包），无跨包障碍。

### 3.3 §3.7 half-open 状态机（v1.4 新设计）

| 方案原引用 | 实际位置 | 源码摘录 | 定位/语义 |
| --- | --- | --- | --- |
| `stream.go:44-47` relay ACK 只证明 relay 收帧 | 同 | `:44-47 // lastHostActivity only advances on evidence from the app-server connection…Relay ACKs prove only that the relay accepted our frame; the official ClientTracker does not use them as proof…` | ✓✓（「无正向确认、in-stream 解除判定是在猜」的前提成立） |
| `stream.go:53-57` Connection-scoped + fresh stream re-arms | 同 | `:53-57 // acksDisabled is the transport-ack circuit breaker…Connection-scoped: a fresh stream re-arms so a server that later accepts acks regains the documented chunk-retention protocol.` | ✓✓（「解除=流重建」引用的既有语义逐字吻合） |
| `stream.go:359-373`/`:397-399` 熔断现状无界 | 同 | `:361-367 observeTransportErrorSentinel: bytes.Contains(payload, transportErrorSentinel) → s.acksDisabled = true`；`:397-399 if disabled { return }` | ✓✓ |
| `stream.go:390-407` ack 构造无 SegmentID；`:339` 逐 chunk ack | 同 | `:400-406 _ = s.conn.Write(Envelope{Type: typeAck, ClientID…, SeqID: env.SeqID})` 无 SegmentID；`:339 s.ack(env)` | ✓✓（E-12a 复核成立） |
| `server_api.rs:27-28` 24–36s | 官方 checkout 同 | `:27-28 …BACKOFF_MIN_SECS: u64 = 24; …MAX_SECS: u64 = 36;` | ✓✓（T=30s 量级参照成立） |
| `think.md:415-425` 750/s 哨兵风暴 | macbridge think.md 同 | `:417-425 ## 2026-09-14…以 ~750/s 累积…"Unexpected ack message received from client"` | ✓✓（1 ack → 1 sentinel 事故形态引用成立） |

状态机与噪声上界本轮独立重推：probe 仅在 `age(armedAt) ≥ T` 的入站帧上发生、probe/sentinel 均重置 armedAt → 相邻 probe 间隔 ≥ T；1:1 形态下稳态 sentinel 率 = 1 条/T，不依赖「host 已恢复接受」假设 ✓；七断言覆盖 r4 要求的三种交错（③普通帧先到/④连续积压/⑤sentinel 延迟到达）+ 无风暴 + 上界 + 新流回 closed ✓；残余 ③（重流量间歇停摆）如实登记 ✓。一处实施歧义（probe 触发的「入站帧」是否含 pong 控制帧）→ F-R5-A3。

### 3.4 E-12a/b 与 iOS 锚点（本轮亲核）

| 命题 | 核验 | 结果 |
| --- | --- | --- |
| attempt-008 归档样本无真实 chunk/seq/segment/ack 帧 | 本轮重新亲读 `agent/codex-remote/testdata/phase0/live/attempt-008-thread-resume-live-turn-stream.json`（5488 字节）：`server_message_chunk`/`segment_id`/`seq_id`/`message_chunk_base64`/`"ack"` 计数全部为 0；`metadata.redaction_procedure` 原文 "Removed … response bodies; retained method names, counts, field sets, routing comparisons and cleanup outcomes" | ✓✓（E-12b「不可替代 live wire」前提成立） |
| `phase2/thread-read-remote-envelope.json` 为 camelCase fixture | 亲读：`clientId`/`seqId`/`envId`/`streamId` 各 1 处，`client_id`/`seq_id`/segment 字段 0 处；`envelope.go:31-32` 注释 "Field names match the live probe (snake_case…)" | ✓✓ |
| iOS `ProjectionStore.swift:63`（`ProjectionPullOutcome.retryable` 只携带 code/message） | 亲读 @ ccb5a0df：`:63 case retryable(code: String?, message: String)` | ✓✓（R4-A1 证据成立） |
| `ProjectionStore.swift:164-181` 白名单不含 hydrate_failed；`:887` 只读 code | 亲读：`:164` 函数声明，11 项白名单 `:166-176`，无 `projection.hydrate_failed`；`:885 let code = (error as? CCCodeBridgeError)?.code`、`:887 if Self.isRetryablePullError(code: code)`——retryable 字段未读取 | ✓✓ |
| `ChatViewModel.swift:438-475` loop / `:477-494` delay | 亲读：`:438 func startProjectionPullLoop`；`:477 projectionDisasterRetryDelayNanoseconds`（`retryInitialSeconds * (1 << boundedAttempt)` 封顶 `retryMaximumSeconds`）——恒本地退避、不读 outcome delay | ✓✓（R4-A1「灾难 loop 恒本地 1s→30s」成立） |
| `CCCodeBridgeModels.swift:314-331` 三字段 | 亲读：`:317-319 let retryable: Bool?; let retryAfterMillis: Int64?; let attempts: Int?` | ✓✓ |
| iOS think.md 双框架注记（§1，R2-A4②） | 亲读 @ ccb5a0df：`think.md:477` 仍为「Mac 端 codex-remote 自 11:36 起病态…至 14:00 未恢复」——fe421cdc→ccb5a0df（含 think.md +199 行的区间）下该条目**未再漂移**，注记在当前 HEAD 下仍可直接查到 | ✓✓ |
| macbridge think.md `:2-6`/`:45` | 亲读：`:2-6` Desktop 26.924 升级断裂 +「已配对，等待 ChatGPT Desktop」横幅 +「加载失败：codex-remote: stream closed」；`:45`「**待立项（协议面）**：桥侧 `backend_status_changed` 推送」 | ✓✓（§1 复盘证据与 OD-2 裁决引用成立） |
| `handlers_projection.go:161-173`（R2-A4① 修正后行号） | 亲读：`:161 default:` → `:162-173 status.Failure → retryable/attempts/retryAfterMillis 透传` | ✓✓（v1.4 修正后的行号准确） |

---

## 4. 意见（F-ID）

### F-R5-1 [阻塞] §3.2 对账 commit 腿：`CommitHydrateTransaction` 内置的 live-execution merge 会把对账收口原样撤销——「零改动」声明被源码证伪

- **位置**：§3.2「Commit」条（「既有 `CommitHydrateTransaction`（`:1319`）**零改动**——baseline（committed 快照 + 终态更新）经 `Restore` 原子发布」）；§5 S-2 行（「复用既有 tx 结构/IngestLive fence/`CommitHydrateTransaction` 零改动」）；§7.1 F-R4-1 行（「四要素均已列」中的 commit API 要素）。
- **证据**（本轮全部亲核，`go-bridge/projection_kernel.go` @ 73539b71，代码与 07721783 等价）：
  1. `CommitHydrateTransaction` 在 Restore 之前有方案未提及的 merge 步骤：`:1335 liveSnap, liveOK := k.reducer.Snapshot(…)`（committed reducer 当前快照）→ `:1353-1356 if tx.unionLiveTurns {…} else { baseline = mergeHydrateBaselineWithLiveExecution(baseline, liveSnap, liveOK) }` → `:1357 k.reducer.Restore(…)`。方案把 commit 描述为「baseline 经 Restore 原子发布」，漏掉了 Restore 前这一步。
  2. merge 第一分支（`:1453-1473`）：`executionInFlight(live.Execution) && !executionInFlight(cold.Execution)` 时——`cold.Execution = live.Execution`（把 live 的在飞 Execution 覆盖回 baseline），随后对 `live.Execution.ActiveTurnID` 对应的 turn：`switch cold.Turns[i].Status { case "completed", "aborted", "error": cold.Turns[i].Status = "running"; cold.Turns[i].CompletedAt = 0 }`（`:1467-1471`）——**把已终态的 turn 重置回 running**。
  3. 对账主场景恰好落入该分支：reducer 的 `turn_completed`/`turn_error` 都会把 Execution 置为 `{Phase: "idle"}`（`projection_reducer.go:2442-2444`、`:2500-2502`）→ 对账 tx baseline（committed 快照 + 终态更新）**非 in-flight**；而对账窗口期 committed reducer 被 IngestLive fence 冻结（`:1204-1212` defers 一切 live 事件），`liveSnap` 仍是断线前的 **running + ActiveTurnID=该 turn** → in-flight。merge 第一分支条件成立。
  4. 该 merge 的设计语境是冷 hydrate 流程（`:1439-1452` 注释：2026-08-20 事故——Begin 之前落到 committed 的 live 事件比冷 cut 新，live 在飞 Execution 须保留）。对账把方向反了：tx baseline（含权威终态更新）才是新真值，冻结的 committed 快照是旧的——启发式的「live 更新」假设在对账场景不成立，方案未识别这一差异。
- **影响**：按方案字面实施，对账的主场景（在飞 turn 断线期间完成）commit 后 turn 回到 running、Execution 回到 running——**收口被静默撤销**；且 runner 按权威摘要「收口该 turn…并同步收口 codec 侧 `turnByThread` 条目」（§3.2），重试触发器同时被清除，该 turn 停留 running 且不再有对账重试。R-2 与 §1 验收 2（「重连完成后 ≤ 一轮对账内收口为完成态」）在该场景失败，且失败形态接近静默（commit 成功、日志正常）。F-R4-1 的闭合标准测试（§5 S-2「READY + 已 prepend 旧页 + 断线完成 turn → terminal 收口」）若按现文实施会红——设计文本与源码行为冲突，必须在方案层修订，不能留给实施者发现。
- **修订方向**（最小充分）：§3.2 Commit 段改为明确写出对账事务如何绕过该 merge——例如 tx 增设 `reconcile` 标志、`:1352-1356` 处对 reconcile 事务跳过 `mergeHydrateBaselineWithLiveExecution`（并写明理由：对账的 tx baseline 是权威新真值，冻结 committed 快照不是「更新的 live 事实」，merge 的启发式前提在对账方向不成立），或提供共用 Restore+pendingLive drain 机制但不做 merge 的专用 commit 入口；Abort 腿不受影响（无 merge 参与，维持现设计）。同步在 §5 S-2 单测断言中补一条「对账 commit 后 turn status 保持 terminal、Execution.Phase=idle（merge 不回撤）」。
- **闭合标准**：v1.5 的 §3.2 Commit 段显式处理 `:1353-1357` merge（引用锚点），§5 S-2 断言含「commit 后终态存活」项；下一轮定向复核该段即可，不需全量重审。

### F-R5-A1 [建议] §3.2 规则 1 的 interrupted 映射偏离冷路径封口纪律（interrupted → 应为 turn_aborted 而非 turn_error），且所引「镜像」锚点不支持该映射

- **位置**：§3.2 输入事件规则 1（「completed → `turn_completed`；interrupted/failed → `turn_error`（状态映射纪律镜像 codec `decodeTurnCompleted` `codec.go:372-381` + `mapAgentEvent` `events.go:174/211/221`，不造新事件词表…）」）。
- **证据**：① codex-remote 冷路径 §9.2 封口纪律（`handlers_projection.go:1573-1575` 注释 + `:1700-1718` 实现）：**failed → turn_error；interrupted → turn_aborted**（reducer `turn_aborted` → status "aborted"，与 "error" 渲染不同，`projection_reducer.go:2407-2417` 注释明言 aborted 有专门渲染）；② 权威摘要经 `mapRemoteTurnShell` 原样保留官方 status（`history.go:406 Status: turn.Status`），"interrupted" 可达；③ 所引镜像链实际产出：`decodeTurnCompleted` 把非 completed（含 interrupted）置 `core.EventError`（`codec.go:374-380`）→ `mapAgentEvent`（**go-bridge**/events.go，仓库内唯一实现，方案未写目录前缀）`:221-227` → 裸 `"error"` 事件名——reducer 无 `"error"` case（事件 switch 全表亲核），既非 turn_error 也非终态封口词表。
- **影响**：按规则 1 字面实施，断线期间被用户中断的 turn 会被对账收口为 status "error"（误导性失败标签），而同一 turn 冷开会收口为 "aborted"——对账/冷开终态分歧；下次冷开自愈，turn 亦已收口（R-2 主目标不受影响），故为建议级。「不造新事件词表」的意图正确，但词表映射应对齐冷路径 §9.2 真值源。
- **修订方向**：规则 1 改为「completed → `turn_completed`；failed → `turn_error`；interrupted → `turn_aborted`（镜像冷路径 §9.2 封口纪律 `handlers_projection.go:1573-1575`）」；镜像引用改指冷路径（现引的 codec+mapAgentEvent 链产出的 `"error"` 事件名不构成封口词表依据）。
- **闭合标准**：v1.5 规则 1 三分支与 `handlers_projection.go:1573-1575` 逐项一致。

### F-R5-A2 [建议] §3.2 manifest 锚点定位错误（`projection_reducer.go:377-381` 无 manifest 字段）

- **位置**：§3.2 Admission 条（「goal/detail manifest（随 turns 携带，`projection_reducer.go:377-381` 的 per-turn manifest 字段）全部在 baseline 内保留」）。
- **证据**：亲读 `projection_reducer.go:377-381`——为 upsertTurn merge 分支收尾（`ps.upsertTurns` 簿记 + `return`），无任何 manifest 字段；per-turn detail manifest 字段（`DetailLoadState`/`DetailReasonCode`/`DetailInline`）实际定义于 `projection_types.go:221-226`（TurnProjection 结构体成员，随快照整体序列化）。命题实质成立（Restore 携带全快照 → manifest 随 baseline 保留），纯锚点精度问题。
- **修订方向**：锚点改 `projection_types.go:221-226`（或表述为「TurnProjection 全字段随 committed 快照序列化」）。
- **闭合标准**：v1.5 该锚点指向实际字段定义。

### F-R5-A3 [建议] §3.7 probe 触发的「入站帧」未限定帧型——静流场景下 pong 是否触发 probe 由实施者自行决定

- **位置**：§3.7 状态机 open/half-open 两态（「入站帧到达且 `now−armedAt ≥ T` → 发一条 probe ack」）与七断言。
- **证据**：`readLoop` 收到的帧含 pong 控制帧（`stream.go:307-314` pong status 判死路径；本仓 ping 周期 10s `ws.go:317`）。熔断 open 且无数据流量的静流场景下，pong（每 10s）是唯一入站帧流：若 pong 计入「入站帧」，host 恢复接受后缓冲以 probe 节奏排空（与「缓冲排空语义」段的意图一致）；若不计入，排空要等下一条数据帧。两种选择都不违反已声明的噪声上界（probe 率 ≤ 1/T）与排空声明（该声明以「host 恢复接受后」的流量为条件），但七断言未覆盖静流场景，实施者面临未定义选择。
- **修订方向**：§3.7 补一句限定 probe 触发帧型（建议：pong 计入——它是 host 侧存活证据，且给出严格更优的排空行为；或显式排除并注明静流排空等待下一条数据帧）。
- **闭合标准**：v1.5 该处帧型语义可判定，断言清单可据此写测试。

---

## 5. 复审处置与回归核查

### 5.1 r4 §6 修订后复审清单（六项）逐条裁决

| # | r4 清单项 | 本轮裁决 | 依据（本轮亲核） |
| --- | --- | --- | --- |
| 1 | S-2 明确 READY-safe reconcile transaction，并证明不丢 older windows/producer state | **未闭合（commit 腿）** | admission/baseline/锁/fence/输入四要素设计与两分支不可行论证全部独立复核成立（§3.1 表：`:1020-1026`/`:969-976`/`:1069-1083`/`:1357`/`:1393-1394`/`history_paginated.go:856-923`/`older_hydrate:154-221`/`:1680-1684`）；older windows/producer state 不丢的机制（committed 快照 baseline + 不调用 `persistCodexProducerSeed`）成立。**但 commit 要素被 F-R5-1 证伪**——四要素中「commit API」不成立，收口会被 merge 撤销 |
| 2 | S-3 gap 归属改为诚实的 stream 级范围，补 fan-out 与游标推进测试 | **闭合** | §3.3 重写：stream 级未知归属（`envelope.go:32-49` 无 thread 身份、`stream.go:409-420` chunk 重组前无 payload 亲核）+ observed∩turnByThread 有界 fan-out + 无在飞 turn 只记日志 + 游标立即推进一次触发 + replay 取舍；§5 S-3 含两条闭合标准测试项（「A 的 completion 丢失后收到 B 事件 → A 纳入 fan-out」「缺口高水位推进 → 只触发一次」） |
| 3 | S-7 写出可测的 breaker 状态机，删除不可观察的「transport 接受」 | **闭合** | §3.7 重写：伪事实删除（`stream.go:44-47` 亲核支持）；closed/open/half-open + in-stream 不回 closed（解除=流重建，`stream.go:53-57` 逐字吻合）+ probe 期间普通帧不 ack + 1 条/T 上界无条件重证（本轮独立重推成立）+ 七断言覆盖 r4 三种交错/无风暴/上界/新流回 closed；残余 ③ 如实登记 |
| 4 | E-12 补真实 chunk/ack 样本，或降为 pending 严格门住 S-3/S-7 | **闭合（降级路径）** | E-12a/E-12b 拆分；E-12b 如实 pending（「同一门有未完成子项，整门不得标 verified」符合 plan-contract）；捕获清单含版本锚定/最小样本集五项/双提取策略/脱敏/计划路径/授权边界；门住 S-3/S-7 **整体实施**在 §4 Gate 段/§5 依赖列/§5 依赖序/§6 阻塞清单/§6 下一阶段入口五处一致（§7 门控核查）；attempt-008 与 phase2 fixture 的「不可替代」前提本轮亲读证实（§3.4） |
| 5 | S-1b 补 `retryAfterMillis` 数据通路和 fake-clock 验收 | **闭合（选项 b：删承诺）** | r4 给出的两个方向之一；§2.2/§3.1/§3.8/§5 四处一致声明「维持已解码、不消费」，全文 grep 无残留消费承诺；iOS 锚点亲核（`ProjectionStore.swift:63` outcome 只带 code/message；`ChatViewModel.swift:438-494` 灾难 loop 恒本地退避不读 delay）证实删承诺零行为损失；OD-1 边界注记（server 节奏另列增量）正确未预承诺 |
| 6 | 更新来源快照后做定向复审 | **闭合** | §2.1 标注「设计证据历史快照」+ v1.4 修订轮亲核记录 + 实施来源门模板（三门点现场重跑、禁复制哈希、含锚点复核项——模板字段齐全亲核）；本轮即 r5 定向复审。评审期间配套 iOS 树又前进（ccb5a0df→0ad7b761，docs-only，四锚点文件零改动亲核）——印证「移动目标」警告与模板必要性 |

### 5.2 r4 阻塞/建议与 R2-A1~A4 处置裁决

| 意见 | 本轮裁决 | 依据 |
| --- | --- | --- |
| F-R4-1（阻塞） | **未闭合**（admission/锁/fence/输入四要素闭合，commit 腿被 F-R5-1 证伪） | 见 F-R5-1；§7.1 处置表自记「闭合待复审确认」，本轮复审结论为部分闭合 |
| F-R4-2（阻塞） | **闭合**（一并闭合 R2-A1） | §5.1 #2 |
| F-R4-3（阻塞） | **闭合**（一并闭合 R2-A3、补 R2-A2） | §5.1 #3；R2-A3 按 r4 方向（流重建解除）而非 r2 原方向（观察窗口解除）闭合，理由已记录（后者仍依赖不可观测推断）——合法的替代闭合，本轮认可 |
| F-R4-4（阻塞） | **闭合（降级路径）** | §5.1 #4 |
| R4-A1（建议） | **闭合** | §5.1 #5 |
| R4-A2（建议） | **闭合** | §5.1 #6 |
| R2-A1 | **闭合**（随 F-R4-2） | 游标推进 + 归属语义均写入 §3.3 |
| R2-A2 | **闭合** | §3.7 依赖段 + §5 S-7 依赖列 + §5 依赖序段落三处一致（「ack `SegmentID` 子项 **+** per-stream `(seq,segment)` 游标维护」，S-3 其余部分不构成前置）——本轮核对三处文本一致 |
| R2-A3 | **闭合**（随 F-R4-3，方向按 r4） | §5.1 #3 |
| R2-A4 | **闭合** | ① §3.1 改 `:161-173` 本轮亲核准确；② §1 双框架注记在位，`think.md:477` 在当前 ccb5a0df 下仍无漂移（亲核） |

### 5.3 v1.3→v1.4 diff 无夹带/无回归核验

以提交 888e7196（v1.3 落盘，整文件 SHA 亲算 = `811ee452…`，与 r3/r4 评审对象一致）为基线，`git diff 888e7196 -- <plan>` 共 **10 个 hunk**（+112/−44），逐 hunk 归属：①版本头（v1.4 声明）②§1 双框架注记（R2-A4②）③④§2.1 历史快照标注+修订轮亲核记录+实施来源门模板（R4-A2）⑤§2.2 两行（R4-A1 关联行号/注记）⑥§3.1 行号修正（R2-A4①）+S-1b 删承诺（R4-A1）+§3.2 全节重写（F-R4-1）⑦§3.3 缺口段重写（F-R4-2）⑧§3.7 全节重写+OD-1 注记（F-R4-3/R4-A1）⑨§4 E-12 拆分+Gate 段+§5 切片表+§6 联动（F-R4-4 及头部声明的联动范围）⑩交接块。**全部落在头部声明的 v1.4 改动范围内，未发现夹带**；§2.3/§3.4/§3.5/§3.6/§4 E-1~E-8/E-11/§7.2/§8 等 r1~r3 已核内容字节未动（不在任何 hunk 内），无回归。稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3）不变，亲核属实。

### 5.4 历轮已闭合意见回归抽查

r1 F-1~F-6、r2 E-12/S-7 采纳、r2-meta 两项来源清单修复、r3 勘误六项——对应文本在 v1.4 中未变（不在 diff hunk 内或仅状态注记联动），承重锚点本轮抽样重核吻合（`stream.go:390-407`/`:339`/`:359-373`、`envelope.go:45`、官方 `websocket.rs:112-138`/`:1031-1067`、`backoff.go`/`ws.go`/`pairing_persist.go` 各段）。无回归。

---

## 6. audit-plan 专项接入状态

按 adapter 执行：方案的外部内容形状声明（Remote Control envelope/seq/segment/chunk ack 形状/ack 游标）锚定官方源码属「描述已实现行为、以读实现验证」类别（E-12a，本轮重读官方 checkout @ e72da2b5 亲核成立）；**部署 wire 形状（E-12b）按 r4 降级路径如实 pending**——未伪装 verified，整门因未完成子项标 pending（符合 plan-contract「同一门有未完成子项不可标 verified」），且精确门住 S-3/S-7 整体实施、无例外通道。本轮对已有归档样本重新亲读（attempt-008：0 处真实 chunk/seq/segment/ack 帧，redaction_procedure 明文只留方法名/计数/字段集合；phase2 fixture：camelCase、0 处 snake_case、无 segment 字段）——方案「均不可替代 live wire」的判断成立。E-12b 捕获清单符合 audit-plan 纪律：版本锚定（不以 checkout 假设部署版本）、最小样本集五项（含 plain 负例与 absent-vs-null 缺失形状）、**两种独立提取策略核对**（生产 Go struct 反序列化 + 独立脚本原始键路径遍历，差异必须解释）、脱敏保留信封头与字段形状、计划路径明示「尚不存在」、授权边界明示需 owner 授权真实账号 live 捕获。E-9/E-10 为运行证据门，按授权边界未采集、如实 pending。**专项结论：外部格式专项未完成（E-12b pending），已按两层结论正确处理——条件设计可评，S-3/S-7 实施被门住。**

---

## 7. 门控一致性与覆盖核查记录

- **E-12 pending → 门住 S-3/S-7 整体实施**：§4 E-12 行（阻塞列「S-3 整体实施/S-7 整体实施」）+ §4 Gate A 段 + §5 S-3/S-7 依赖列（「E-12（pending，实施门）」）+ §5 依赖序段（「E-12 pending 门住 S-3/S-7 整体实施」）+ §6 阻塞清单 + §6 下一阶段入口（「S-3/S-7 待 E-12 解锁」）——**六处一致** ✓；S-2 不依赖 E-12（对账消费既有 ReadColdHistory 路径，无新 wire 解析）✓。
- **OD-1 pending → 门住 S-1a/S-1b**：§3.8/§4 Gate B/§5 三处一致 ✓；选项验收独立 ✓；R4-A1 后 OD-1 注记（server 节奏另列增量、不预承诺）与授权边界一致 ✓。
- **OD-2 decided**（think.md:45 亲核）→ S-6 仅桥内暴露 ✓。
- **OD-3 pending 随 E-9；E-9 pending → S-4 整体阻塞**：§3.4/§4/§5 一致 ✓；「未取样前不得写死提前量」约束（plan:508-509）维持 ✓。
- **E-10 pending（实施期）→ S-1/S-2/S-3 完成验收门 + S-7 可见性验收**：§4 E-10 行（v1.4 对齐状态机观测项）/§5 各行一致 ✓；S-7 行为验收以单测为准（sentinel 风暴依赖上游缺陷、真机不可控复现）与致因匹配 ✓。
- **复审门**：§4「S-2/S-3/S-7 实施另需 r4 阻塞闭合经复审确认」——本轮确认 F-R4-2/F-R4-3 闭合；**F-R4-1 未闭合（F-R5-1）→ S-2 实施继续被复审门挡住**；方案「不自判通过」的表述正确。
- **交接块**：open_gates [OD-1, OD-3, E-9, E-10, E-12] 与 §6 阻塞清单一致 ✓；review_round 链与实况一致 ✓；正文哈希口径（head -n 333）本轮复算吻合 ✓。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2，受 F-R5-1 影响的正是此链）、R-3→S-4（E-9 门内）、R-4→S-3+E-12（验收 4）、R-5→S-6——对应齐全 ✓。
- 切片依赖序（S-1a→S-1b、S-2→S-3、S-3 子项→S-7、S-4/S-5/S-6 独立）与失败处理列完整；无数据迁移、无不可逆动作 ✓。

---

## 8. 剩余门与下一阶段

- **本轮意见**：F-R5-1（阻塞，§3.2 commit 腿）+ F-R5-A1/A2/A3（建议）。F-R5-1 修订量小（§3.2 Commit 段 + §5 S-2 断言补一条），修订后**只需定向复核该段**，不需全量重审。
- **仍待满足的实施门（与意见分开）**：OD-1（owner 确认重试语义）、OD-3（随 E-9）、E-9（ctrlExp fixture ≥5 样本）、E-10（受控断线复现，实施期）、E-12（E-12b wire 样本，owner 授权后按 §4 捕获清单采集）。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证、E-12b wire 捕获与 UI automation 另行授权。
- 建议下一版（v1.5）改动范围声明：处置 F-R5-1（必改）与 F-R5-A1~A3（随改），其余章节零触碰。

---

## 评审员交接块

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: b919e4d4ada90342c2a405353080e7d6f596c5d3025c62ada025a4f506ef44fc
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r5.md
scope: full
blockers: 1
advisories: 3
open_gates: [OD-1, OD-3, E-9, E-10, E-12]
round_complete: true
contract_version: plan-contract-v1.1
```
