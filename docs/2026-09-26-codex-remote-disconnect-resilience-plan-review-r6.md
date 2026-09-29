# codex-remote 断线韧性与恢复专项方案 评审报告（r6）

- 日期：2026-09-27
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（**v1.5**——r5 REVISION_REQUIRED 后的修订轮）
- 评审时方案 SHA-256：`c3b17e7d9a870b38bee69147702b0847a73fb859fd4f546b9d58fe53ecf06798`（整文件 `shasum -a 256` 亲算，**与派发记录一致**——评审对象身份确认）；正文哈希 `head -n 344 | shasum -a 256` = `d40e6694adf619dad6ac7ece9070bad66ad142556923acf640718f38cca06775` 亲算，与方案内设计师交接块自报一致
- 评审范围：**full**（全量评审；评审员模式 fresh——先独立形成风险清单并逐锚点亲核，后读 r1/r2/r2-meta/r3/r3-meta/r4/r5 作回归清单）。注：任务简报的评审链描述止于 v1.4/r5 启动时点（含「本轮 r5」字样与 v1.4 处置表编号），但派发 SHA 与报告路径（r6）均指向当前 v1.5——以 SHA 为准，评审对象身份无歧义
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md + audit-plan-adapter.md 均已读取；audit-plan 接入已按 adapter 执行，见 §7）
- 结论：**APPROVED**（blockers 0，advisories 1）
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、产品代码、历轮报告、两仓源码、官方 checkout 一律只读，未 commit、未部署、未构建、未采集任何真机/wire fixture（E-12b 需 owner 授权，未触碰）。

---

## 1. 结论

### verdict: APPROVED（方案层面通过；「方案通过 ≠ 实施就绪」）

r5 的唯一阻塞 **F-R5-1（§3.2 对账 commit 腿被 `CommitHydrateTransaction` 内置 live-execution merge 回撤）已完整闭合**——本轮独立亲核证实：v1.5 的 Commit 段重写（tx `reconcile` 标志跳过两类 merge）在源码层面成立、方向反转论证正确、全部行号锚点精确；§5 S-2 补的「commit 后终态存活」断言在位。三条建议 F-R5-A1（规则 1 三分支改镜像冷路径 §9.2）/F-R5-A2（manifest 锚点改 `projection_types.go:221-226`）/F-R5-A3（probe 触发帧型限定 + 断言 ⑧）全部闭合。至此 r4 §6 复审清单六项全部闭合（第 1 项 F-R4-1 的 commit 腿随本轮闭合），R2-A1~A4 与 R4-A1/A2 维持 r5 确认的闭合状态。

v1.3→v1.5 全量 diff 逐 hunk 归属：10 个 hunk 全部落在 v1.4（§7.2）/v1.5（§7.1）声明范围内，无夹带；r1~r3 已核内容（§2.3/§3.4/§3.5/§3.6/E-1~E-8/E-11/§7.3/§8）不在任何 hunk 内，无回归。门控联动一致；E-12b 如实 pending 且门住 S-3/S-7，未伪装 verified。

本轮新发现 **1 条建议级事项 A-R6-1**（AbortReconcileTransaction 规格未显式写明关闭 `hydrateDone`），不阻塞：设计语义正确、kernel 惯用法使遗漏难以存活，修订为一句话补充。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**；评审通过不构成实施授权。
- 实施前置门（与意见分开列）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（随 E-9）、**E-9**（ctrlExp 真机 fixture，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期）、**E-12**（E-12b wire 样本，owner 授权后按 §4 捕获清单采集，门住 S-3/S-7 整体实施）。
- 复审门：**S-2/S-3/S-7 的阻塞闭合复审确认全部完成**——F-R4-2/F-R4-3 经 r5 确认（本轮对承重锚点抽样复核吻合），F-R4-1（含 F-R5-1 修订）经本轮确认。S-2 实施的复审门解除；S-3/S-7 仍被 E-12 实施门挡住。
- E-10 真机验证、E-12b wire 捕获与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核；读取源码前重新生成）

| 仓库 | 工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期来源 / 配套组合 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 73539b71c0c3552a3c16548d05417813ba907869 | 仅方案文档 1 处未提交修改（v1.5，评审循环正常形态）+ 未跟踪 r5 报告一份（只读）；`git status --porcelain` 亲跑 | 简报①指定树；`git diff 07721783..HEAD --name-only` 亲跑：9 文件**全部为 docs/*.md**（r1~r4 报告、方案、2 份 zcode-plan-agents docs）——**代码零改动**，Mac 锚点与 07721783 等价（与简报声明一致） | 纯设计文档评审，无构建/安装——N/A（无构建场景） |
| cordcode-macbridge（main 树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge | main | 73539b71（`git worktree list` 亲跑） | — | 枚举记录 | N/A |
| cordcode-macbridge（第三树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge-plan-approval | detached | b2b25235 | — | 枚举记录 | N/A |
| cordcode-ios（iOS 锚点核验来源，简报③指定） | /Users/jacklee/Projects/cordcode-ios | main | ccb5a0df647865324e11aedebbb089b9fc07bbb5 | 干净（`git status --porcelain` 亲跑为空） | 简报③指定 main 工作树（与 r5 核验来源同提交） | N/A（纯设计） |
| cordcode-ios（同族配套树，P0 要求记录） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | **75359e69dd8eee2e32f88a8d585e0e15bd13eb89**（本轮亲核；简报派发时点为 ccb5a0df，评审期间又前进 3 个 docs(plan) 提交——移动目标如实记录，印证方案 §2.1「实施来源门模板」的必要性） | 干净（亲跑为空） | 非本任务锚点来源。**锚点有效性**：`git merge-base --is-ancestor ccb5a0df 75359e69` 亲跑通过；`git diff --stat ccb5a0df..75359e69 -- <四锚点文件>` 亲跑为空——ProjectionStore/CCCodeBridgeModels/ChatViewModel/CCCodeBridgeTransport 在 main 与配套两候选下零改动，iOS 锚点结论与工作树选择无关 | N/A |
| openai/codex（官方，只读） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD 亲核） | e72da2b53805894878023d01949a25a082e0a5cb | 干净 | 简报④：一律以 FETCH_HEAD 为准（亲核一致） | N/A |

**评审对象身份**：派发哈希与读取哈希一致（报告头）。v1.3 基线（`git show 888e7196:…plan.md`）为 r3/r4 评审对象；v1.4 完整版已被 v1.5 覆盖、不可从 git 复原（73539b71 内为 §3.2 时点半成品快照）——本轮 diff 归属以 v1.3 为基线、对照 §7.2+§7.1 声明范围的并集逐 hunk 核验，并以 r5 报告引用的 v1.4 原文交叉印证（方法与结果见 §6）。

### 2.2 覆盖声明

- 本轮为 full 全量评审，**fresh 亲核重点**（v1.5 改动域 + 承重前提）：§3.2 Commit 段全部锚点（`CommitHydrateTransaction` 函数体 `:1319-1404` 逐行读、`mergeHydrateBaselineWithLiveExecution` `:1439-1494` 逐行读、reducer 终态事件、IngestLive fence、tx 标志先例、MarkReady/MarkFailed、finishHydrateLocked、admission/restore 分支、pathlessRichHistoryBackend 名单、commit gate、commit 后处理与 producer seed 钩子、live-only 铸造点）；F-R5-A1 冷路径三分支与被废弃镜像链；F-R5-A2 manifest 字段定义；F-R5-A3 pong/typeAck/ping 锚点；**本轮新增的独立并发检查**——kernel 内全部 reducer 变更调用点枚举（grep 亲跑，见 §3.1 末行）。
- 复用（按命题/锚点粒度，依赖已亲证：Mac 代码 07721783→73539b71 零改动、iOS 四锚点文件跨候选 ref 零改动、官方 checkout e72da2b5 未动）：r5 已确认的 v1.4 内容（§3.3 fan-out/游标推进、§3.7 状态机主体、E-12a/b 拆分、R4-A1/A2、R2-A1~A4）与 r1~r3 已核内容——承重锚点本轮抽样重核吻合：iOS `ProjectionStore.swift:164-181` 白名单（11 项不含 `projection.hydrate_failed`）与 `:885-887` 只读 code、官方 `websocket.rs:112-138` ack 游标/`:123` `(seq, seg.unwrap_or(MAX))`、`:1031-1067` seq 消息级分配（拆分前按消息分配、per-(client,stream) 从 1 递增）、`segment.rs:445-467` `seq_id: envelope.seq_id`/`:20-21` 150KB/100MB、macbridge `think.md:415-425` 750/s 风暴、attempt-008 归档样本（`server_message_chunk`/`segment_id`/`seq_id`/`message_chunk_base64`/`"ack"` 计数全 0，grep 亲跑）与 phase2 fixture（camelCase `clientId`/`seqId` 各 1、snake_case 全 0）。
- 未核项（如实声明）：① relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码；② E-9/E-10/E-12b 为 pending 运行证据，按授权边界未采集，状态如实；③ v1.4 完整版字节快照不可复算（历史形态，见 §6 方法说明）。

---

## 3. 锚点核验表（本轮承重项；「✓✓」=定位与语义均成立）

### 3.1 §3.2 Commit 腿（F-R5-1 核心，全部本轮亲核）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `:1319` CommitHydrateTransaction；`:1335` 先取 liveSnap；`:1352-1356` 按 tx 标志二选一；`:1357` Restore | 同 | `:1319 func (k *ProjectionKernel) CommitHydrateTransaction(…)`；`:1335 liveSnap, liveOK := k.reducer.Snapshot(…)`；`:1352 if tx.unionLiveTurns {` `:1353 baseline = unionColdBaselineWithLiveTurns(…)` `:1354 } else {` `:1355 baseline = mergeHydrateBaselineWithLiveExecution(baseline, liveSnap, liveOK)` `:1356 }`；`:1357 k.reducer.Restore(backendID, sessionID, baseline)` | ✓✓（v1.4 确实漏了 Restore 前这一步——F-R5-1 的事实认定成立；v1.5 引用行号全部精确） |
| merge 第一分支 `:1457-1473`：liveSnap 在飞且 tx baseline 非在飞 → 覆盖 Execution + 终态重置回 running | 同 | `:1457 if executionInFlight(live.Execution) && !executionInFlight(cold.Execution) {` `:1458 cold.Execution = live.Execution`；`:1467-1471 switch cold.Turns[i].Status { case "completed", "aborted", "error": cold.Turns[i].Status = "running"; cold.Turns[i].CompletedAt = 0 }` | ✓✓（「把对账刚收口的终态 turn 重置回 running」逐字成立） |
| merge 设计语境 `:1439-1452`（冷 hydrate 的 2026-08-20 事故） | 同 | `:1439-1441 // mergeHydrateBaselineWithLiveExecution keeps an already-live in-flight execution when the cold baseline would otherwise Restore idle…`；`:1447-1452 // …the cold baseline replays to a phantom "running" while the authoritative live turn_completed already applied…`（另 `:1344-1350` commit 内注释同语境） | ✓✓（「merge 的启发式前提在对账方向不成立」的论证有源码依据） |
| 终态事件把 Execution 置 idle：`projection_reducer.go:2442-2444`/`:2500-2502` | 同 | `:2442-2444 exec := ExecutionView{Phase: "idle"}; ps.projection.Execution = exec; ps.execution = &exec`（turn_completed）；`:2500-2502` 同文（turn_aborted/turn_error） | ✓✓（对账 tx baseline 非在飞的推演前提成立） |
| 窗口期 committed reducer 被 fence 冻结：`:1204-1212` | 同 | `:1204 if session.status.Phase == ProjectionHydrateHydrating && session.hydrate != nil {` `:1205-1206 queued := msg; queued.Data = cloneProjectionJSONValue(msg.Data)` `:1207 session.hydrate.pendingLive = append(…)` `:1212 return ProjectionIngestDeferred` | ✓✓（liveSnap 仍 running+ActiveTurnID 的推演前提成立） |
| tx 标志先例 `:682-688`（liveOnlyAdmission/unionLiveTurns） | 同 | `:682 liveOnlyAdmission bool`；`:688 unionLiveTurns bool`（结构体字段注释齐备） | ✓✓（新增 `reconcile` 标志同型、最小） |
| 「跳过两类 merge」必要性（union 内部亦调 merge） | `:1504` | `unionColdBaselineWithLiveTurns` 首行 `:1504 merged := mergeHydrateBaselineWithLiveExecution(cold, live, liveOK)` | ✓✓（只跳 else 分支不够；v1.5 写「跳过两类」恰好完备） |
| pendingLive 按戳序 drain `:1358-1374`；source cut 写回 `:1393-1394`；coldBaseline 重申 `:1388-1392` | 同 | `:1358-1374 for _, msg := range tx.pendingLive { …k.reducer.Apply(msg)… }`；`:1393 session.committedSourceCursor = tx.startCut`；`:1394 session.committedSource = cloneProjectionSourceDescriptor(tx.source)`；`:1388-1392 if !tx.liveOnlyAdmission { session.coldBaseline = true }` | ✓✓（「其余 commit 机制不变」清单与源码一致） |
| coldBaseline 重申为 no-op：live-only 仅 deepseek（`:545-562`）/dsh-web（`:564-588`）铸造；MarkReady 无生产调用方 | `handlers_projection.go:560/:587`；grep 亲跑 | `:560 return h.ensureLiveOnlyProjectionAdmission(backendID, sessionID)`（deepseek 分支内）；`:587` 同（dsh-web 分支内）；`grep -rn "MarkReady" go-bridge/*.go`（排除定义与测试）零调用点；另 `:1066` liveOnlyAdmission 由 identity 前缀铸造（codex-remote source.Identity 无前缀 → false） | ✓✓（codex-remote Ready 会话 coldBaseline 已 true，重申为既值 no-op） |
| admission 侧：READY no-op `:1020-1026`；pathless 名单 `:969-976`；唯一 restore 分支 `:1069-1083`；安装序 `:1092-1095`；Hydrating 单飞行 `:1027-1034`；ApplyHydrateEvent `:1160`（armed 集来自 turnId/itemId `:1176-1180`）；Prepend Ready 硬门 `:1680-1684`；commit gate `NonTerminalTurnCountInSet`（`WaitHydrateCommitReady`，sourceIngestComplete 仅在此检查） | 同 | 逐段亲读：`:1020-1024` pathless+sourceChanged→break、`:1025-1026` AlreadyReady；`:970-975` switch 名单含 opencode/grokbuild/claude/claudecode/opencode-web/codex-web、**无 codex-remote**；`:1077 else if !sourceChanged && len(source.Segments)==0 → tx.reducer.Restore(existing)`；`:1092-1095` status/hydrate/hydrateDone 安装；`:1027-1034` Done 单飞行；`:1302 ready := tx.sourceIngestComplete && (tx.sourceIsLive \|\| preview.NonTerminalTurnCountInSet(…)==0)`（commit 函数体 `:1319-1404` 无 sourceIngestComplete 引用）；`:1680-1685` prepend requires ready session | ✓✓（「为什么不能复用既有 admission」两分支论证、armed 集语义、「kernel commit 不检查该标志」全部成立） |
| runner 复用 commit 后处理但不调用 `persistCodexProducerSeed`（`:1182-1184`） | `handlers_projection.go:1161-1183` | `:1161 h.releaseDeferredPushCandidates(commit.AppliedPendingEventIDs)`；`:1176-1177 h.eventPublisher.PublishProjectionPatch(…)`；`:1181-1183 if backendID == "codex-remote" { h.persistCodexProducerSeed(…) }` | ✓✓（桥侧后处理可选跳过、kernel commit 无 checkpoint 持久化——「producer cursor/seed 全程不动」可实施） |
| **本轮新增独立检查**：Hydrating 窗口期 committed reducer 的全部可能写者 | grep 亲跑 | `grep -n "k\.reducer\.\(Apply\|Restore\|PrependHistoricalTurns\|…\)" projection_kernel.go` 仅 4 处：`:1215`（IngestLive 直 apply——被 `:1204` fence 挡住，仅非 Hydrating 可达）、`:1357`/`:1361`（commit 自身）、`:1686`（prepend，被 `:1680` Ready 硬门挡住） | ✓✓（**对账窗口期 committed reducer 无第四类写者**——跳过 merge 不丢失任何窗口期 live 真值：liveSnap ≡ Begin 时点 committed 快照 ≡ baseline 减终态更新；v1.5 的方向反转论证经本轮独立推演成立） |

### 3.2 F-R5-A1 / A2 / A3（v1.5 三处小修，全部本轮亲核）

| 命题 | 核验 | 结果 |
| --- | --- | --- |
| 规则 1 三分支镜像冷路径 §9.2：`handlers_projection.go:1572-1576` 注释 + `:1693-1718` 实现 | 亲读：`:1573-1575 // completed → turn_completed；failed → turn_error（官方 error.message 透传）；interrupted → turn_aborted`；`:1694 case "completed"→turn_completed`、`:1707 case "failed"→turn_error`、`:1713 case "interrupted"→turn_aborted`、`:1719-1720 default 不封口` | ✓✓（v1.5 三分支与真值源逐项一致；未知 status 不发事件→维持 running，与冷路径 default 同型——**F-R5-A1 闭合**） |
| v1.4 所引镜像链确产出裸 `"error"`（修正前提） | 亲读 `codec.go:372-379`（非 completed → `core.EventError`）→ `go-bridge/events.go:221-228`（`case core.EventError:` → `"error"` 事件名）→ `grep -n 'case "' projection_reducer.go` 全表无 `case "error":` | ✓✓（v1.5 改引冷路径真值源的依据成立） |
| aborted/error 渲染不同：`projection_reducer.go:2407-2417`/`:2446-2467` | 亲读：`:2407-2417` 终态保留注释；`:2464-2467 status := "aborted"; if msg.Event == "turn_error" { status = "error" }` | ✓✓ |
| manifest 锚点 `projection_types.go:221-226`；`projection_reducer.go:377-381` 为 upsertTurn 收尾无 manifest 字段 | 亲读：`:221 DetailLoadState`、`:222 DetailReasonCode`、`:226 DetailInline`（TurnProjection 结构体成员）；`:377-381` 为 merge 分支 `ps.upsertTurns` 簿记 + return；`:382-385` append 分支（对不存在 turn 造新行——规则 2 ghost 防护前提成立） | ✓✓（**F-R5-A2 闭合**） |
| probe 帧型：pong `stream.go:307-314`、typeAck continue `:305-306`、ping 10s `ws.go:317` | 亲读：`:305-306 case typeAck: continue`；`:307-314 case typePong:`（status!=active 判死、否则 markHostActivity）；`ws.go:317 pingInterval = 10 * time.Second`、`:318 streamIdleLimit = 60 * time.Second` | ✓✓（静流场景 pong 为唯一入站帧流成立；pong 计入不改变「probe 仅在 age≥T 帧上发生、probe/sentinel 均重置 armedAt → 间隔 ≥ T」的上界推导——**F-R5-A3 闭合**；断言 ⑧ 与 §5 S-7/§6 八断言联动在位） |

---

## 4. 意见（本轮新发现，1 条建议）

### A-R6-1 [建议] §3.2 Abort 规格未显式写明关闭 `hydrateDone`（finishHydrateLocked）

- **位置**：§3.2 Abort 条（「丢弃 tx……Phase→Ready，把 pendingLive 按戳序 drain 回 committed reducer 并 FlushPatch，返回 applied EventIDs」）与 §5 S-2 的 abort 测试项（「对账失败 → Abort 后 pendingLive 回放进 committed reducer、Phase=Ready」）。
- **证据**（本轮亲核）：Admission 条显式写明安装 `hydrateDone`（`:1092-1095`），Abort 条却未提其关闭。本 kernel 的惯用法是**每次离开 Hydrating 都经 `finishHydrateLocked` 关闭 `hydrateDone`**（`:957-962`；调用点：MarkReady `:917`、MarkFailed `:953`、Commit `:1396`）。窗口期加入单飞行的冷开 pull 持有 `Done: session.hydrateDone`（`:1027-1034`）等待释放。
- **影响**：若实施者把 Abort 写成裸 Phase 赋值 + drain 而漏掉 `finishHydrateLocked`，等待中的冷开 pull 不会及时释放，要拖到 `hydrate_queue_timeout` 才失败重试——功能可恢复但行为劣化，且 §5 S-2 现有断言（只查 Phase 与 pendingLive 回放）测不出该遗漏。kernel 惯用法使该遗漏大概率不会发生，故为建议级。
- **修订方向**（一句话）：Abort 规格补「经 `finishHydrateLocked` 关闭 `hydrateDone`（镜像 MarkReady `:917`），释放单飞行等待者」；§5 S-2 abort 测试项可随补一条等待者释放断言（可选）。
- **闭合标准**：v1.6（或实施前随手）该句在位即可；不构成实施前置，不阻塞本轮通过。

---

## 5. 复审处置与回归核查

### 5.1 r5 意见处置裁决（逐条）

| 意见 | 本轮裁决 | 依据（本轮亲核） |
| --- | --- | --- |
| F-R5-1（阻塞）§3.2 commit 腿 merge 回撤 | **闭合** | §3.1 全表：merge 步骤/第一分支/设计语境/推演前提/tx 标志先例/coldBaseline no-op 逐项亲证；「跳过两类 merge」经 `:1504`（union 内部亦调 merge）证实为完备写法；本轮新增的 reducer 写者枚举（§3.1 末行）独立证实窗口期无第四类写者、跳过不丢 live 真值；§5 S-2「commit 后终态存活」断言在位（F-R5-1 闭合标准双条均满足） |
| F-R5-A1（建议）规则 1 interrupted 映射 | **闭合** | §3.2 表：三分支与 `handlers_projection.go:1573-1575`/`:1693-1718` 逐项一致；被废弃镜像链前提（裸 `"error"`、reducer 无该 case）本轮 grep 亲证 |
| F-R5-A2（建议）manifest 锚点 | **闭合** | §3.2 表：`projection_types.go:221-226` 三字段定义亲证；`:377-381` 确为 upsertTurn 收尾无 manifest 字段 |
| F-R5-A3（建议）probe 帧型 | **闭合** | §3.2 表：pong/typeAck/ping 锚点亲证；断言 ⑧ 在位；§5 S-7 行与 §6「八断言」联动一致；噪声上界推导在 pong 计入下重推成立 |

### 5.2 r4 §6 修订后复审清单（六项）终态

| # | r4 清单项 | 终态 | 依据 |
| --- | --- | --- | --- |
| 1 | S-2 READY-safe reconcile transaction，不丢 older windows/producer state | **闭合（本轮完成最后一块）** | admission/baseline/锁/fence/输入四要素 r5 已确认；commit 腿经 v1.5 修订后本轮亲核闭合（§3.1）；§5 S-2 测试项含 F-R4-1 闭合标准（READY+prepend+断线完成 turn→terminal 收口/旧 turns 超集/producer state 不变/并发 live delta fence 后收敛）+ F-R5-1 闭合标准（终态存活） |
| 2 | S-3 gap 归属 stream 级 + fan-out/游标推进测试 | 闭合（r5 §5.1 #2；本轮抽样复核 `envelope.go:32-49` 无 thread 身份之 transport 事实经官方 `websocket.rs:1031-1067` seq 分配与 `segment.rs:445-467` chunk 共享 seq 亲证成立） | — |
| 3 | S-7 可测 breaker 状态机、删伪事实 | 闭合（r5 §5.1 #3；本轮抽样复核 `stream.go:305-314`/`:359-373`/`ws.go:317-318`/`think.md:415-425` 吻合；v1.5 probe 帧型补丁闭合） | — |
| 4 | E-12 真实样本或降级 pending 严格门住 | 闭合（r5 §5.1 #4；本轮重验 attempt-008 零真实帧与 phase2 camelCase——grep 亲跑；E-12 pending 门住 S-3/S-7 六处一致，见 §7） | — |
| 5 | S-1b retryAfterMillis 通路或删承诺 | 闭合（r5 §5.1 #5 选项 b；本轮抽样复核 iOS `ProjectionStore.swift:164-181` 白名单与 `:885-887` 只读 code——`retryable` 字段未消费，删承诺零行为损失） | — |
| 6 | 更新来源快照后定向复审 | 闭合（r5 即复审；本轮为 v1.5 后复审。评审期间配套 iOS 树又前进 ccb5a0df→75359e69（3 个 docs 提交，四锚点文件零改动亲证）——再次印证「移动目标」警告） | — |

### 5.3 历轮已闭合意见回归

r1 F-1~F-6、r2 E-12/S-7 采纳、r2-meta 两项来源清单修复、r3 勘误六项、r4 F-R4-2/3/4 + R4-A1/A2、r5 F-R5-A1~A3——对应文本经 v1.3→v1.5 diff 逐 hunk 核验**不在任何改动 hunk 内或仅按声明范围联动**（§6）；承重锚点本轮抽样重核吻合（§2.2）。无回归。稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3）不变，亲核属实。

---

## 6. v1.3→v1.5 diff 无夹带/无回归核验

**方法**：v1.4 完整版已被 v1.5 覆盖且不可从 git 复原（73539b71 内为 §3.2 时点半成品快照），故以 v1.3（提交 888e7196，r3/r4 评审对象）为基线 `git diff 888e7196 -- <plan>` 得 **10 个 hunk（+123/−44）**，逐 hunk 对照 §7.2（v1.4 声明范围）+ §7.1（v1.5 声明范围）的并集归属，并以 r5 报告引用的 v1.4 原文交叉印证 v1.4→v1.5 增量恰为 §7.1 四条。

**结果**（逐 hunk）：①版本头（v1.5+v1.4 改动范围声明）②§1 双框架注记（R2-A4②）③④§2.1 历史快照标注+修订轮亲核记录+实施来源门模板（R4-A2）⑤§2.2 两行（R4-A1）⑥§3.1 行号修正（R2-A4①）+S-1b 删承诺（R4-A1）+§3.2 全节（F-R4-1 v1.4 重写 + F-R5-1/F-R5-A1/F-R5-A2 v1.5 修订，v1.5 改动处均带「v1.5」标记）⑦§3.3 缺口段（F-R4-2）+E-12a/b 尾句⑧§3.7 全节（F-R4-3）+probe 帧型 bullet+断言 ⑧（F-R5-A3）+OD-1 注记⑨§4 E-12 拆分（F-R4-4）+Gate 段/复审门+§5 切片表（S-2/S-3/S-7 行含 v1.5 联动）+依赖序+§6 验证分层/风险 1·3·5/阻塞清单/下一阶段入口⑩§7.1 新表+§7.2 表+交接块。**全部落在声明范围内，未发现夹带**；§2.3/§3.4/§3.5/§3.6/§4 E-1~E-8/E-11/§7.3/§8 等 r1~r3 已核内容不在任何 hunk 内，无回归。净增行数（v1.4 +112/−44 → v1.5 +123/−44）与 §7.1 声明的修订量级吻合。

**残留声明 grep**：方案内「零改动」仅存于历史语境（v1.5 头/§3.2 Commit 段/§7.1 对 v1.4 错误声明的更正记录、§8 勘误自述）；「七断言」仅存于历史记录（v1.5 头「七断言→八断言」、§7.1/§7.2 处置表），现行 §3.7/§5/§6 文本均为八断言——无残留活声明。

---

## 7. 门控一致性、audit-plan 专项与交接块

- **E-12 pending → 门住 S-3/S-7 整体实施**：§4 E-12 行（阻塞列「S-3 整体实施/S-7 整体实施」）+ §4 Gate A 段 + §5 S-3/S-7 依赖列 + §5 依赖序段 + §6 阻塞清单 + §6 下一阶段入口（「S-3/S-7 待 E-12 解锁」）——六处一致 ✓；S-2 不依赖 E-12 ✓。
- **OD-1 pending → 门住 S-1a/S-1b**；OD-2 decided（S-6 仅桥内）；OD-3 随 E-9；E-9 → S-4 整体；E-10 → S-1/S-2/S-3 完成验收 + S-7 可见性——各处一致 ✓。
- **复审门**：§4/§5/§6 三处一致表述「F-R4-1（含 F-R5-1 修订）待 r6 定向复审确认」——本轮确认闭合，该门对 S-2 解除；方案「不自判通过」的表述正确 ✓。
- **交接块**：open_gates [OD-1, OD-3, E-9, E-10, E-12] 与 §6 阻塞清单一致 ✓；review_round 链（r1→…→r5→v1.5→「下一步 r6 定向复审」）与实况一致 ✓；正文哈希口径（head -n 344）本轮复算吻合 ✓；v1.4 正文哈希（35294009…）与 r5 报告头记录一致，更正关系链完整 ✓。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2，F-R5-1 修复的正是此链）、R-3→S-4（E-9 门内）、R-4→S-3+E-12（验收 4）、R-5→S-6——对应齐全 ✓。切片依赖序与失败处理列完整；无数据迁移、无不可逆动作 ✓。
- **audit-plan 专项**：E-12a（源码事实）锚点本轮亲证（官方 `protocol.rs`/`websocket.rs:123`/`segment.rs` 引用链 + MacBridge `stream.go:390-407` 现状——r5 已核，本轮抽样吻合）；**E-12b 如实 pending**——§4 状态列明「同一门有未完成子项，整门不得标 verified」，未伪装 verified，捕获清单（版本锚定/最小样本集五项含 plain 负例与 absent-vs-null/双独立提取策略/脱敏/计划路径「尚不存在」/授权边界）完整且与 audit-plan 纪律一致；「attempt-008 与 phase2 fixture 不可替代 live wire」前提本轮 grep 重验成立（§2.2）。**专项结论：外部格式专项未完成（E-12b pending），已按两层结论正确处理——条件设计可评、可过审，S-3/S-7 实施被门住。**
- 零后果观察（不计意见）：§2.2「iOS 自动重试 loop」行引 `ProjectionStore.swift:164-180`，函数实际至 `:181`（§3.1 引 `:164-181` 为准）——同函数、命题无影响，历轮均未 flagged，仅记录。

---

## 8. 剩余门与下一阶段

- **本轮意见**：A-R6-1（建议，§3.2 Abort 规格补 hydrateDone 关闭句）——不阻塞，可在实施前随手补入或随下版处理。
- **仍待满足的实施门（与意见分开）**：OD-1（owner 确认重试语义）、OD-3（随 E-9）、E-9（ctrlExp fixture ≥5 样本）、E-10（受控断线复现，实施期）、E-12（E-12b wire 样本，owner 授权后按 §4 捕获清单采集）。
- **复审门全部解除**：r4 四阻塞 + r5 一阻塞经 v1.4/v1.5 修订与 r5/r6 两轮复审全部闭合确认。方案层面无待办评审项。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证、E-12b wire 捕获与 UI automation 另行授权。实施前按 §2.1 实施来源门模板现场重跑来源清单（本轮评审期间配套 iOS 树再次前进，已两次印证模板必要性）。

---

## 评审员交接块

```
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: c3b17e7d9a870b38bee69147702b0847a73fb859fd4f546b9d58fe53ecf06798
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r6.md
scope: full
blockers: 0
advisories: 1
open_gates: [OD-1, OD-3, E-9, E-10, E-12]
round_complete: true
contract_version: plan-contract-v1.1
```
