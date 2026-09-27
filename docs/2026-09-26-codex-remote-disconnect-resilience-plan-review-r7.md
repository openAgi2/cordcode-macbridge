# codex-remote 断线韧性与恢复专项方案 评审报告（r7）

- 日期：2026-09-27
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（**v1.6**——r6 APPROVED 后、r6-meta 元审核 confirm=false 的通过后纠错勘误轮）
- 评审时方案 SHA-256：`a3ce49c7592dd3fb78564fa2407fb7aff3c628bcc975c1b28a9704411ed78f3d`（整文件 `shasum -a 256` 亲算，**与派发记录一致**——评审对象身份确认）；正文哈希 `head -n 364 | shasum -a 256` = `8e3ce9e3542581c91d6a1ed80852352a3765abb0e002339deadd78430a3d51c6` 亲算，与方案内设计师交接块自报一致
- 评审范围：**full**（任务简报字段 7；评审员模式 fresh——先独立形成风险清单，再以 r1~r6-meta 作回归清单）。**覆盖形态如实声明（plan-contract「通过后纠错」第 3 条）**：本轮对 **v1.6 勘误增量（§8.1 两项更正、§2.1 六树亲核记录、§3.2 锚点修正、§4/§5/§6 评审状态注记同步、交接块）做了 100% 亲核**；对未改动的 v1.5 设计内容未重做 r5/r6 级逐锚点全量重查，而是按 SKILL.md 复审纪律做**锚点粒度复用 + 承重锚点 fresh 亲核抽样（本轮亲核约 40 组，覆盖 F-R5-1 全链、§3.2 admission/commit/abort 前提、§3.3/§3.7/E-12a 关键事实、iOS 白名单、冷路径封口、官方 seq/segment/ack 游标）**——复用合法性依赖的三项源码身份等价（Mac 代码 07721783≡HEAD、iOS 四锚点文件跨候选零改动、官方 checkout e72da2b5 未动）本轮全部亲自重跑证实（见 §2.1/§3）
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md 均已读取；audit-plan 纪律按 E-12b pending 处置复核，见 §6）
- 结论：**APPROVED**（blockers 0，advisories 0）——r6-meta 两项复核问题在 v1.6 中完整闭合，**r6 的 APPROVED 结论按 plan-contract「通过后纠错」第 5 条恢复确认**
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、产品代码、历轮报告、两仓源码、官方 checkout 一律只读，未 commit、未部署、未构建、未采集任何真机/wire fixture（E-12b 需 owner 授权，未触碰）。
- 任务简报时效性注记：简报为 r5 派发时点文本（评审链描述止于 v1.4/r5、「本轮 r5」字样、v1.4 处置表编号 §7.1），与当前实况（v1.6/r7）存在漂移；按 r5/r6 两轮先例，**以派发 SHA 与报告路径（r7）为准**，评审对象身份无歧义。

---

## 1. 结论

### verdict: APPROVED（r6 通过结论恢复确认；「方案通过 ≠ 实施就绪」）

r6-meta 定向复核（confirm=false）的两项问题在 v1.6 中**均完整闭合**，本轮逐项亲核证实：

1. **问题 1（主）——来源清单完整性**：r5/r6 报告来源表遗漏 iOS 仓第三工作树 `cordcode-ios-plan-approval`。v1.6 处置：方案 §2.1 补「v1.6 勘误轮全量六树亲核记录」（含该树完整哈希 `a336b68bb37765d2ea23c14f6508619378c839ae`、干净状态、祖先关系亲跑证明、「本方案无任何锚点取自该树」），恢复 r2-meta/r3 已确认的「两仓各 3 树」记录标准；r5/r6 报告为已归档评审产物不回写，**报告侧补记由本报告承担**（r6-meta §3 明示要求）——本报告 §2.1 来源身份表已全量记录六树（含 iOS 第三树）。本轮亲跑 `git worktree list`（两仓）+ 六树 `rev-parse`/`status` + `merge-base --is-ancestor a336b68b ccb5a0df`（通过），方案记录与实际一致。
2. **问题 2（轻）——锚点子行号偏差**：方案 §3.2 Commit 段 runner 侧引用 `handlers_projection.go:1161-1177` 未覆盖 `PublishProjectionPatch` 实际调用行。v1.6 处置：修正为 `:1163-1178` 并显式标注两调用行。本轮亲读 `handlers_projection.go:1152-1190` 逐行核对：`:1163` = `h.releaseDeferredPushCandidates(commit.AppliedPendingEventIDs)`、`:1177` = `if commit.PendingPatch != nil {`（守卫行）、`:1178` = `h.eventPublisher.PublishProjectionPatch(...)`、`:1182-1184` = `persistCodexProducerSeed` 门控块——修正后锚点**定位与语义均精确**。

**v1.6 勘误纪律合规（plan-contract 第 4 条）**：HEAD（73539b71，v1.4 §3.2 时点半成品快照）→ 工作树（v1.6）diff 共 11 个 hunk（+103/−41），逐 hunk 归属全部落在 v1.4 补全（§7.2 声明范围）/ v1.5（§7.1 声明范围）/ v1.6（§8.1 声明范围）三者并集内，**无未归属夹带**；设计内容（规则/锚点/门控/验收）零改动属实——改动仅为 §8.1 更正记录、§2.1 来源记录、§3.2 锚点数字、§4 复审门/§5 S-2 依赖列与依赖序/§6 阻塞清单与下一阶段入口的评审状态注记同步、文档头与交接块。**A-R6-1（r6 唯一建议：§3.2 Abort 规格补 `finishHydrateLocked` 关闭句）正确未夹带**——grep 亲证 `finishHydrateLocked` 在方案中仅出现于 v1.6 头部声明、§6 下一阶段入口与 §8.1 未夹带说明，§3.2 Abort 条原文未变（无该句），符合「设计类改动不得夹带进勘误」；其处置路径（实施前随手补入，r6 闭合标准明示「不构成实施前置」）在 §8.1/§6 双处登记一致。

**状态注记同步准确性**：§4 复审门/§5/§6 新增的「r6 已确认全部阻塞闭合、复审门解除」表述与 r6 报告实况（r6 §5.1/§5.2/§8：r4 §6 清单六项全部闭合、方案层面无待办评审项）逐处吻合，非超前自判。

本轮**未发现任何新问题**（阻塞 0、建议 0）：v1.6 勘误内容本身经独立风险清单核查（勘误记录准确性、夹带、门控联动、来源身份、audit-plan 纪律、残留声明）全部通过。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**；评审通过不构成实施授权。
- 实施前置门（不变，与意见分开列）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（随 E-9）、**E-9**（ctrlExp 真机 fixture，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期）、**E-12**（E-12b wire 样本，owner 授权后按 §4 捕获清单采集，门住 S-3/S-7 整体实施）。
- 复审门：r4 四阻塞 + r5 一阻塞经 v1.4/v1.5 修订与 r5/r6 复审全部闭合确认（r6 §5.2），v1.6 勘误未触碰该结论——**复审门维持解除**；S-2 实施不再被复审门挡住，S-3/S-7 仍被 E-12 实施门挡住。
- 待办（非门、非阻塞）：**A-R6-1** 实施前随手补入 §3.2 Abort 规格（一句话 + 可选等待者释放断言）。
- E-10 真机验证、E-12b wire 捕获与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核，读取源码前重新生成；**含 iOS 第三工作树补记——r6-meta §3 要求**）

| 仓库 | 工作树路径 | 分支 | 完整提交 | 未提交状态（`git status --porcelain` 亲跑） | 任务预期来源 / 配套组合 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 73539b71c0c352a3c16548d05417813ba907869 | 仅方案文档 1 处未提交修改（v1.6，评审循环正常形态）+ 未跟踪评审报告 r5/r6/r6-meta 三份（只读；本报告为第 4 份未跟踪产物） | 简报①指定树；`git diff 07721783..HEAD --name-only` 亲跑：9 文件**全部为 docs/*.md**——**代码零改动**，Mac 锚点与 07721783 等价（与简报②声明一致） | 纯设计文档评审，无构建/安装——N/A（无构建场景） |
| cordcode-macbridge（main 树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge | main | 73539b71c0c3552a3c16548d05417813ba907869 | 干净 | 枚举记录（P0：含配套工作树，无论是否实际引用） | N/A |
| cordcode-macbridge（第三树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge-plan-approval | detached | b2b2523526b6af7990c9688fd29b7e285d7ce78c | 干净 | 枚举记录 | N/A |
| cordcode-ios（iOS 锚点核验来源，简报③指定） | /Users/jacklee/Projects/cordcode-ios | main | ccb5a0df647865324e11aedebbb089b9fc07bbb5 | 干净 | 简报③指定 main 工作树 | N/A（纯设计） |
| cordcode-ios（同族配套树，P0 要求记录） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | **61e5c32e7ad03580cf576cadeb9f5cf20f40e348**（本轮亲核；方案 v1.6 记录时点为 75359e69、r6 时点亦 75359e69——评审期间又前进 4 个 docs(plan) 提交，**移动目标如实记录**，第三次印证方案 §2.1「实施来源门模板」的必要性） | 1 个未跟踪 docs 文件（2026-09-27 native-timeline streaming phase2 评审报告，与本方案无关） | 非本任务锚点来源。**锚点有效性（本轮亲跑）**：`git diff --stat ccb5a0df..61e5c32e -- <四锚点文件>` 为**空**——ProjectionStore/CCCodeBridgeModels/ChatViewModel/CCCodeBridgeTransport 在 main/配套两候选下零改动，iOS 锚点结论与工作树选择无关 | N/A |
| **cordcode-ios（第三树，枚举项——r5/r6 报告遗漏项，本报告补记）** | /Users/jacklee/Projects/cordcode-ios-plan-approval | detached | a336b68bb37765d2ea23c14f6508619378c839ae | 干净 | 枚举记录。`git merge-base --is-ancestor a336b68b ccb5a0df` 本轮亲跑**通过**（iOS main 的祖先）；本方案无任何锚点取自该树 | N/A |
| openai/codex（官方，只读） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD 亲核） | e72da2b53805894878023d01949a25a082e0a5cb | 干净 | 简报④：一律以 FETCH_HEAD 为准（亲核一致） | N/A |

**评审对象身份**：派发哈希与读取哈希一致（报告头）；HEAD 73539b71 内方案为 v1.4 §3.2 时点半成品快照（方案 §2.1 自记，本轮读 HEAD 版本头证实状态行为 v1.4、交接块为 v1.3）——v1.5 字节快照不可从 git 复原（历史形态，r6 §2.1 同此声明），v1.5→v1.6 增量以「HEAD→工作树 11 hunk 总账 −（§7.2 v1.4 补全范围 ∪ §7.1 v1.5 范围）」逐 hunk 归属法核验，并以 r6/r6-meta 报告引用的 v1.5 原文交叉印证（方法与结果见 §4）。

### 2.2 覆盖声明

- **本轮 fresh 亲核**（v1.6 增量 100% + 承重锚点抽样约 40 组，覆盖三仓 12 文件）：§8.1 两项更正记录逐句对照 r6-meta；§2.1 v1.6 六树记录逐树亲跑；§3.2 勘误锚点 `:1163/:1177/:1178/:1182-1184` 逐行亲读；F-R5-1 全链（`CommitHydrateTransaction` `:1319/:1335/:1352-1356/:1357`、merge 第一分支 `:1457-1473`、union 内部调 merge `:1504`、tx 标志先例 `:682/:688`、reducer 终态置 idle `:2442-2444`/`:2500-2502`、IngestLive fence `:1204-1212`、MarkReady `:911-918`/MarkFailed `:925-951`/`finishHydrateLocked` `:957-962`、admission `:1020-1026`/`:969-976`/`:1069-1083`/`:1092-1095`、commit gate `:1302`、prepend 硬门 `:1680-1684`、source cut `:1393-1394`、coldBaseline `:1388-1392`）；manifest 字段 `projection_types.go:221-226`；upsertTurn merge/ghost 前提 `projection_reducer.go:336-385` + reducer 无 `case "error":`（grep 亲证）；冷路径封口 `handlers_projection.go:1572-1576`/`:1693-1718`；S-1a 现状锚点 `:430`/`:852`/`:876-881`/`:603-605`/`:1079-1083`/`:147`/`:161-173`；§3.7/§3.3/E-12a（`stream.go:44-47`/`:53-57`/`:305-306`/`:307-314`/`:339`/`:359-373`/`:390-407`、`ws.go:317-318`、`envelope.go:16`/`:32-49`）；官方（`websocket.rs:112-138` ack 游标含 `:123` `(seq, seg.unwrap_or(MAX))`、`:1031-1045` seq 消息级 per-(client,stream) 从 1 分配、`segment.rs:20-21` 150KB/100MB、`:445-467` `seq_id: envelope.seq_id`）；iOS（`ProjectionStore.swift:63`/`:164-181` 11 项白名单无 hydrate_failed/`:885-887` 只读 code、`CCCodeBridgeModels.swift:314-331` 三字段已解码、`ChatViewModel.swift:438`/`:477`）；think.md 双仓（macbridge `:2-6`/`:45`/`:415-425` 750/s 风暴、ios `:477`）；E-12b 不可替代前提（attempt-008 grep 四关键词计数 0、phase2 fixture snake_case 0/camelCase 2）。
- **复用（按命题/锚点粒度；依赖已亲证：Mac 代码 07721783→73539b71 零改动、iOS 四锚点文件跨全部候选 ref 零改动、官方 checkout e72da2b5 未动）**：r5/r6 已亲核且 v1.6 未改动的锚点——E-1/E-2/E-7（cursor 不交付 controller、remote.rs 无重连、ctrlExp 无调度）、E-11 清除子声明细节、`backoff.go`/`pairing_persist.go`/`session.go`/`codec.go`/`agent.go`/`main.go`/`history_paginated.go`/`projection_window_older_hydrate.go` 各段、官方 `remote.rs`/`reconnect.rs`/`protocol.rs`/`server_api.rs`/tests 引用、attempt-008/phase2 全文亲读。r6-meta 18 组抽样中 17 组精确的结论与 r5/r6 全量亲核记录作为复用依据；r6-meta 指出的 1 组偏差即 v1.6 勘误-4 对象，本轮已亲自重核修正后值。
- **未核项（如实声明）**：① relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码；② E-9/E-10/E-12b 为 pending 运行证据，按授权边界未采集，状态如实；③ v1.5 字节快照不可复算（历史形态，见 §2.1 方法说明）；④ 本轮未重做 r5/r6 级的全部锚点逐项重查（复用范围见上，未重查项不影响 v1.6 增量结论——v1.6 对相应文本零触碰，经 diff 逐 hunk 归属证实）。

---

## 3. 锚点核验表（本轮承重项摘录；「✓✓」=定位与语义均成立）

### 3.1 v1.6 勘误-4 对象（§3.2 Commit 段 runner 侧锚点，本轮逐行亲读）

| 方案原引用（v1.6 修正后） | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `releaseDeferredPushCandidates` `:1163` + `PublishProjectionPatch` `:1178`，`handlers_projection.go:1163-1178` | 同 | `:1163 h.releaseDeferredPushCandidates(commit.AppliedPendingEventIDs)`；`:1177 if commit.PendingPatch != nil {`；`:1178 h.eventPublisher.PublishProjectionPatch(backendID, sessionID, *commit.PendingPatch)` | ✓✓（修正后区间恰好覆盖两调用行；`:1177` 为守卫行，与勘误-4 记录一致） |
| 不调用 `persistCodexProducerSeed`（`:1182-1184`） | 同 | `:1182 if backendID == "codex-remote" {` `:1183 h.persistCodexProducerSeed(backendID, sessionID, commit.Projection)` `:1184 }` | ✓✓（「以 `backendID == "codex-remote"` 门控跳过」可实施；§7.2 历史容器区间 `:1161-1184` 覆盖实际内容，不回写决定合理） |

### 3.2 F-R5-1 闭合链（r6 通过结论的承重核心，本轮独立重推）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `:1319` CommitHydrateTransaction；`:1335` 先取 liveSnap；`:1352-1356` 按 tx 标志二选一；`:1357` Restore | 同（grep 亲跑） | `:1319 func (k *ProjectionKernel) CommitHydrateTransaction(`；`:1335 liveSnap, liveOK := k.reducer.Snapshot(…)`；`:1352 if tx.unionLiveTurns {` `:1353 union…` `:1355 baseline = mergeHydrateBaselineWithLiveExecution(…)` `:1357 k.reducer.Restore(…)` | ✓✓（v1.5「Restore 前有 merge 步骤」的重写依据成立；v1.6 未触碰） |
| merge 第一分支 `:1457-1473`（在飞覆盖 Execution + 终态重置回 running） | 同 | `:1457 if executionInFlight(live.Execution) && !executionInFlight(cold.Execution) {` `:1458 cold.Execution = live.Execution`；`:1468-1470 case "completed", "aborted", "error": cold.Turns[i].Status = "running"; CompletedAt = 0` | ✓✓（「把对账刚收口的终态 turn 重置回 running」逐字成立） |
| 「跳过两类 merge」完备性（union 内部亦调 merge） | `:1504` | `unionColdBaselineWithLiveTurns` 首行 `:1504 merged := mergeHydrateBaselineWithLiveExecution(cold, live, liveOK)` | ✓✓ |
| tx 标志先例 `:682-688` | 同 | `:682 liveOnlyAdmission bool`；`:688 unionLiveTurns bool` | ✓✓（新增 `reconcile` 标志同型、最小） |
| 终态事件置 idle `:2442-2444`/`:2500-2502`；fence `:1204-1212`；MarkFailed `:925-951` 丢弃 pendingLive；MarkReady `:911-918` 不 drain；`finishHydrateLocked` `:957-962` | 同（grep/sed 亲跑） | `:2442-2444`/`:2500-2502 exec := ExecutionView{Phase: "idle"}`；`:1204-1212 Hydrating→pendingLive append→ProjectionIngestDeferred`；MarkFailed `:951-952 置 Failed + session.hydrate = nil`；MarkReady `:915-917 Phase=Ready + hydrate=nil` 无 drain；`:957-962 close(hydrateDone)` | ✓✓（对账主场景推演前提、Abort 不能用 MarkFailed/MarkReady 的两条理由、A-R6-1 事实基础全部成立） |
| admission 两分支不可行 `:1020-1026`/`:969-976`/`:1069-1083`；安装序 `:1092-1095`；commit gate `:1302`；prepend 硬门 `:1680-1684`；source cut `:1393-1394`；coldBaseline `:1388-1392` | 同（sed 亲跑） | `:1025-1026 AlreadyReady`；`:970-975` 名单无 codex-remote；`:1077 else if !sourceChanged && len(source.Segments)==0 → Restore(existing)`；`:1092-1095` 安装序；`:1302 ready := tx.sourceIngestComplete && (…NonTerminalTurnCountInSet…)`；`:1680-1684 prepend requires ready`；`:1393-1394` 写回；`:1388-1392 if !tx.liveOnlyAdmission { session.coldBaseline = true }` | ✓✓（「为什么不能复用既有 admission」「armed 集平凡满足 gate」「不移动 source cut」「coldBaseline 重申 no-op」全部成立） |
| 规则 1 三分支镜像冷路径 §9.2；规则 2 ghost 前提；manifest 字段 | `handlers_projection.go:1572-1576`/`:1693-1718`；`projection_reducer.go:336-385`；`projection_types.go:221-226` | §9.2 注释 + 实现：completed→`turn_completed`、failed→`turn_error`、interrupted→`turn_aborted`、default 不封口；upsertTurn：存在→merge 保留、缺席→append 造新行；`DetailLoadState/DetailReasonCode/DetailInline` 为 TurnProjection 成员；reducer 无 `case "error":`（grep exit=1 亲证） | ✓✓（F-R5-A1/A2 修正后锚点全部精确；ghost 防护前提成立） |

### 3.3 §3.3/§3.7/E-12a 与 iOS（抽样亲核）

| 命题 | 核验 | 结果 |
| --- | --- | --- |
| E-12a：ack 构造无 SegmentID；逐信封 ack `:339`；字段已备 `envelope.go:45` | 亲读 `stream.go:390-407`（Write 字面量仅 Type/ClientID/EnvID/StreamID/SeqID）；grep `s.ack(env)` → `:324`/`:339` 两处；Envelope struct `:32-49` 含 `SegmentID *int` | ✓✓ |
| 熔断现状无界 + 解除=流重建 + pong/typeAck/ping | 亲读 `stream.go:359-373`（sentinel→`acksDisabled=true`）、`:397-399`（disabled 早退）、`:44-47`（relay ACK 只证明 relay 收帧）、`:53-57`（fresh stream re-arms）、`:305-306`（typeAck continue）、`:307-314`（pong status!=active 判死）、`ws.go:317-318`（ping 10s/idle 60s） | ✓✓（§3.7 状态机的事实前提全部成立；断言 ⑧ 的 pong 前提在位） |
| 官方 seq/segment/ack 游标 | 亲读 `websocket.rs:112-138`（`:123 (seq, seg.unwrap_or(usize::MAX))`、`envelope_cursor <= acked_cursor` 清除）、`:1031-1045`（per-(client,stream) `or_insert(1)` 消息级分配）、`segment.rs:20-21`（150KB/100MB）、`:445-467`（`seq_id: envelope.seq_id` chunk 共享消息 seq） | ✓✓（§3.3 去重判定表、缺口检测依据、S-7 probe 清缓冲语义的官方锚点成立） |
| iOS：白名单 11 项不含 hydrate_failed、只读 code、三字段已解码、灾难 loop 本地退避、outcome 只带 code/message | 亲读 `ProjectionStore.swift:63`/`:164-181`/`:885-887`、`CCCodeBridgeModels.swift:314-331`、`ChatViewModel.swift:438`/`:477`（@ ccb5a0df） | ✓✓（E-3/E-5 与 R4-A1 删承诺的依据成立） |
| think.md 复盘证据 + OD-2 裁决 | 亲读 macbridge `:2-6`（26.924 升级断裂）/`:45`（backend_status_changed 待立项另立）/`:415-425`（750/s 风暴、"Unexpected ack message received from client"）、ios `:477`（2.5h 不自愈） | ✓✓ |
| E-12b 不可替代前提 | grep 亲跑：attempt-008 样本 `server_message_chunk\|segment_id\|seq_id\|message_chunk_base64` 计数 **0**；phase2 fixture snake_case（client_id/seq_id）计数 **0**、camelCase（clientId/seqId）计数 2 | ✓✓（「归档样本与 camelCase fixture 均不可替代 live wire」成立） |

---

## 4. v1.5→v1.6 无夹带/无回归核验

**方法**：v1.5 字节快照不可从 git 复原（HEAD 73539b71 内为 v1.4 §3.2 时点半成品），故以 HEAD→工作树 diff（`git diff -U2 HEAD -- <plan>`，11 hunk，+103/−41）为总账，逐 hunk 对照 §7.2（v1.4 补全）∪ §7.1（v1.5）∪ §8.1（v1.6）声明范围归属，并以 r6 §3.1/§3.2 表引用的 v1.5 原文与 r6-meta §1.2 摘录交叉印证设计内容未变。

**逐 hunk 归属结果**：①文档头（v1.6 状态行 + v1.6/v1.5 改动范围声明 + v1.4 处置表编号 §7.1→§7.2 交叉引用修正）②§2.1（v1.4 补全：HEAD 推进注记 + v1.6：勘误轮六树亲核记录）③④§2.2/§3.1 两处 §7.1→§7.2 交叉引用修正（v1.4 补全）⑤⑥§3.2（v1.5：F-R5-1 Commit 段重写/F-R5-A1 规则 1/F-R5-A2 manifest 锚点/事务域归属 reconcile 标志 + v1.6：`:1163-1178` 勘误）⑦§3.3（v1.4 补全：F-R4-2 重写 + E-12a/b 尾句）⑧§3.7（v1.4 补全：F-R4-3 重写 + v1.5：F-R5-A3 probe 帧型 + 断言⑧）⑨§3.8 OD-1 注记（v1.4 补全）⑩§4（v1.4 补全：E-12 拆分/E-10 对齐/Gate 段复审门 + v1.6：复审门状态同步）⑪§5/§6/§7/§8/交接块（v1.4 补全：切片表/风险/阻塞清单/§7.2 表 + v1.5：§7.1 表/S-2 终态存活断言 + v1.6：依赖序与阻塞清单状态同步/下一阶段入口/§8.1/交接块）。**全部可归属，无未声明改动**。

**设计内容零改动交叉印证**：r6 报告 §3.1/§3.2 所引 v1.5 原文（Commit 段 merge 机制/reconcile 标志/规则 1 三分支/manifest 锚点/probe 帧型）与当前 v1.6 对应文本逐句一致——唯一差异即勘误-4 的 `:1161-1177`→`:1163-1178`（带「v1.6 勘误修正锚点」标记）；r6 §7 所验 v1.5 门控六处一致、open_gates、review_round 链在 v1.6 中仅按声明同步（复审门「待 r6 确认」→「经 r6 确认…解除」；review_round 链追加 r6→r6-meta→v1.6）。**A-R6-1 未夹带**（§1 已述）。**残留声明 grep**：「零改动」仅存于历史语境（v1.6/v1.3 头自述、§3.2 对 v1.4 错误声明的更正记录、§7/§8 历史表）；「七断言」仅存于历史记录（v1.5 头「七断言→八断言」、§7.1/§7.2 处置表），现行 §3.7/§5/§6 均为八断言——无残留活声明，与 r6 §6 结论一致，v1.6 无回归。

---

## 5. 复审处置（r6-meta 两项问题 + A-R6-1）

| 项 | 本轮裁决 | 依据（本轮亲核） |
| --- | --- | --- |
| r6-meta 问题 1（主）：来源清单遗漏 iOS 第三树 | **闭合** | 方案侧：§2.1 v1.6 六树亲核记录在位且逐树亲跑吻合（含 a336b68b 完整哈希、干净状态、祖先关系、无锚点取自该树）；方案 §2.1 主枚举块（v1.3 勘误-1）自始覆盖该树——亲读证实。报告侧：r5/r6 报告不回写（已归档评审产物，plan-contract 证据更正纪律：错误条目以可追踪更正记录处理而非无声改写），**补记由本报告 §2.1 承担**（六树全量 + 该树单独标注）。历史定性复核：grep 亲证 r2-meta/r3 报告确曾记录该树、r5/r6 确缺失——「标准回归」定性准确 |
| r6-meta 问题 2（轻）：§3.2 锚点子行号偏差 | **闭合** | §3.2 修正为 `:1163-1178` 且两调用行显式标注；本轮逐行亲读实际文件证实精确（§3.1 表）；§8.1 勘误-4 的偏差描述（r6 报告引 `:1161` 偏 2 行、`:1176-1177` 偏 1 行、`:1177` 为守卫行）与 r6-meta §1.2 末行及实际源码一致；§7.2 历史容器区间 `:1161-1184` 覆盖实际内容、不回写的处置合理 |
| A-R6-1（r6 建议，非本轮新意见） | **维持开放（实施前随手补入），处置合规** | v1.6 按 plan-contract 第 4 条不夹带设计类改动进勘误——正确；§8.1 未夹带说明 + §6 下一阶段入口双处登记处置路径；其事实基础（`finishHydrateLocked` `:957-962`、MarkReady `:917` 内调用）本轮亲读复核成立。r6 闭合标准（「不构成实施前置」）继续有效 |

**历轮已闭合意见回归**：r1 F-1~F-6、r2 E-12/S-7 采纳 + R2-A1~A4、r2-meta/r3 勘误、r4 F-R4-1~4 + R4-A1/A2、r5 F-R5-1 + F-R5-A1~A3——对应文本不在 v1.6 改动范围（§4 逐 hunk 归属），承重锚点本轮抽样重核吻合（§3.2/§3.3）。无回归。稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3）不变，亲核属实。

---

## 6. 门控一致性、audit-plan 专项与交接块

- **E-12 pending → 门住 S-3/S-7 整体实施**：§3.3 尾句、§4 E-12 行（阻塞列）+ Gate A 段、§5 S-3/S-7 依赖列 + 依赖序段、§6 阻塞清单 + 下一阶段入口（「S-3/S-7 待 E-12 解锁」）、§7.2 处置表、交接块 review_round——**八处一致** ✓；S-2 不依赖 E-12 ✓。
- **OD-1 pending → 门住 S-1a/S-1b**（§3.8/§4 Gate B/§5 三处）；OD-2 decided（think.md:45 亲核）→ S-6 仅桥内；OD-3 随 E-9；E-9 → S-4 整体；E-10 → S-1/S-2/S-3 完成验收 + S-7 可见性——各处一致 ✓。
- **复审门状态同步（v1.6 唯一门控文本变化）**：§4/§5/§6 三处「r6 已确认、复审门解除」与 r6 报告实况（§5.1/§5.2/§8）逐处吻合；「方案层面无待办评审项」指 r4/r5 阻塞闭合复审，与 §6 下一阶段入口描述的勘误复核步骤（本轮）不矛盾 ✓。
- **交接块**：open_gates [OD-1, OD-3, E-9, E-10, E-12] 与 §6 阻塞清单一致 ✓；review_round 链（r1→…→r6→r6-meta→v1.6→「下一步定向复核勘误…通过后 r6 APPROVED 恢复确认」）与实况一致，本轮即该步 ✓；正文哈希口径（head -n 364）本轮复算吻合 ✓；v1.5 正文哈希 d40e6694…（r6/r6-meta 评审对象）更正关系链完整 ✓。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2）、R-3→S-4（E-9 门内）、R-4→S-3+E-12（验收 4）、R-5→S-6——对应齐全 ✓；切片依赖序与失败处理列完整；无数据迁移、无不可逆动作 ✓。
- **audit-plan 专项**：E-12a（源码事实）锚点本轮亲证（官方 ack 游标/seq 分配/chunk 共享 seq + MacBridge ack 现状）；**E-12b 如实 pending**——§4 状态列明「同一门有未完成子项，整门不得标 verified」，未伪装 verified；捕获清单（版本锚定/最小样本集五项含 plain 负例与 absent-vs-null/双独立提取策略/脱敏/计划路径「尚不存在」/授权边界）完整；「attempt-008 与 phase2 fixture 不可替代 live wire」前提本轮 grep 重验成立。**专项结论：外部格式专项未完成（E-12b pending），已按两层结论正确处理——条件设计可评、可过审，S-3/S-7 实施被门住。**

---

## 7. 剩余门与下一阶段

- **本轮意见**：无（阻塞 0、建议 0；未发现 v1.6 勘误引入的任何新问题）。
- **r6 APPROVED 结论**：按 plan-contract「通过后纠错」第 5 条，**恢复确认**——r6-meta 两项程序性缺陷经 v1.6 勘误 + 本轮定向复核闭合，r6 的通过依据（F-R5-1/F-R5-A1~A3 闭合、r4 §6 六项闭合、门控一致、E-12b pending 处置）经本轮抽样亲核仍然成立。
- **仍待满足的实施门（与意见分开）**：OD-1（owner 确认重试语义）、OD-3（随 E-9）、E-9（ctrlExp fixture ≥5 样本）、E-10（受控断线复现，实施期）、E-12（E-12b wire 样本，owner 授权后按 §4 捕获清单采集）。
- **待办（非门）**：A-R6-1 实施前随手补入（§3.2 Abort 规格一句 + 可选断言）。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证、E-12b wire 捕获与 UI automation 另行授权。实施前按 §2.1 实施来源门模板现场重跑来源清单（本轮评审期间配套 iOS 树第三次前进 75359e69→61e5c32e，四锚点文件仍零改动——模板必要性持续被印证）。

---

## 评审员交接块

```
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: a3ce49c7592dd3fb78564fa2407fb7aff3c628bcc975c1b28a9704411ed78f3d
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r7.md
scope: full
blockers: 0
advisories: 0
open_gates: [OD-1, OD-3, E-9, E-10, E-12]
round_complete: true
contract_version: plan-contract-v1.1
```
