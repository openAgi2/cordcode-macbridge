# codex-remote 断线韧性方案 r6 评审通过结论——元审核复核报告（r6-meta）

- 日期：2026-09-27
- 复核对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r6.md`（verdict: APPROVED）
- 方案：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（v1.5）
- 复核范围（仅两项，定向）：① 锚点抽查——从通过报告随机抽样带行号引用，逐行核对实际文件，行号与语义均须吻合；② 来源清单完整性——通过报告的来源清单是否覆盖任务项目规则（P0 来源门）要求的全部来源组合（含配套工作树，无论是否实际引用）。
- 复核结论：**confirm = false**（发现 2 项问题：1 项来源清单完整性缺口【主】+ 1 项锚点子行号偏差【轻，不影响结论】）
- 边界声明：本报告为本轮唯一写入文件；方案、r6 报告、两仓源码、官方 checkout 一律只读。本复核不重审方案实质内容，不构成对 r6 意见（A-R6-1）或门控裁决的重新评审。

---

## 0. 评审对象身份确认（复核前提）

亲跑 `shasum -a 256 docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`：

```
c3b17e7d9a870b38bee69147702b0847a73fb859fd4f546b9d58fe53ecf06798
```

与派发记录及 r6 报告头一致；正文哈希 `head -n 344 | shasum -a 256` = `d40e6694adf619dad6ac7ece9070bad66ad142556923acf640718f38cca06775`，与 r6 报告头自报一致。评审对象身份无歧义。

---

## 1. 第一项：锚点抽查

### 1.1 抽样方法

从 r6 报告的锚点核验表（§3.1/§3.2/§2.2/§4）中抽取 **18 组**带行号的承重引用，覆盖三个来源仓、10 个文件，逐行读实际文件核对行号与语义。抽样刻意跨仓分布：Mac 树（go-bridge kernel/handlers/reducer/types、agent/codex-remote、think.md）、iOS 树（ProjectionStore.swift）、官方 codex checkout（websocket.rs/segment.rs）。

### 1.2 逐组核对结果

| # | r6 报告引用 | 实际核对（文件:行） | 结果 |
| --- | --- | --- | --- |
| 1 | `projection_kernel.go:1319` CommitHydrateTransaction、`:1335` 先取 liveSnap、`:1352-1356` 按 tx 标志二选一、`:1357` Restore | 1319 函数签名、1335 `liveSnap, liveOK := k.reducer.Snapshot(…)`、1352 `if tx.unionLiveTurns {`、1353 union、1355 merge、1357 `k.reducer.Restore(…)` 逐行吻合 | ✓✓ 精确 |
| 2 | merge 第一分支 `:1457-1473`（在飞覆盖 Execution + 终态重置回 running） | 1457 `if executionInFlight(live.Execution) && !executionInFlight(cold.Execution)`、1458 `cold.Execution = live.Execution`、1467-1471 `case "completed", "aborted", "error": → "running"; CompletedAt = 0` | ✓✓ 精确 |
| 3 | union 内部亦调 merge `:1504` | 1504 `merged := mergeHydrateBaselineWithLiveExecution(cold, live, liveOK)`（unionColdBaselineWithLiveTurns 首行） | ✓✓ 精确 |
| 4 | merge 设计语境 `:1439-1452` | 1439-1441 函数注释首段、1443-1452 "Reverse direction… phantom running… turn_completed already applied" 段在位（"phantom running" 字样起于 1446，引用区间 1447-1452 覆盖实质内容） | ✓✓ 成立 |
| 5 | tx 标志先例 `:682-688` | 682 `liveOnlyAdmission bool`、688 `unionLiveTurns bool`，字段注释齐备 | ✓✓ 精确 |
| 6 | A-R6-1 证据：`finishHydrateLocked :957-962`；调用点 MarkReady `:917`/MarkFailed `:953`/Commit `:1396` | 957-962 函数体（close(hydrateDone)）、917/953/1396 三调用点逐行吻合 | ✓✓ 精确（A-R6-1 事实基础成立） |
| 7 | READY no-op `:1020-1026`；Hydrating 单飞行 `:1027-1034`（Done: session.hydrateDone） | 1022-1023 pathless+sourceChanged→break、1025-1026 AlreadyReady、1027-1034 case Hydrating→`Done: session.hydrateDone` | ✓✓ 精确 |
| 8 | fence `:1204-1212`；commit gate `:1302` | 1204-1212 Hydrating→pendingLive append→`return ProjectionIngestDeferred`；1302 `ready := tx.sourceIngestComplete && (tx.sourceIsLive \|\| preview.NonTerminalTurnCountInSet(…)==0)` | ✓✓ 精确 |
| 9 | pathless 名单 `:969-976`（无 codex-remote）；唯一 restore 分支 `:1077`；安装序 `:1092-1095`；prepend 硬门 `:1680-1685`；`:1066` identity 前缀 | 971 `case "opencode", "grokbuild", "claude", "claudecode", "opencode-web", "codex-web":`（确无 codex-remote）；1077 `else if !sourceChanged && len(source.Segments) == 0`→1081 `tx.reducer.Restore(existing)`；1092-1094 status/hydrate/hydrateDone 安装；1680-1685 prepend requires ready；1066 `strings.HasPrefix(source.Identity, liveOnlyAdmissionSourcePrefix)` | ✓✓ 精确 |
| 10 | F-R5-A1 三分支：注释 `:1573-1575` + 实现 `:1694/:1707/:1713/:1719-1720` | 1573-1575 注释逐字吻合；1694 `case "completed":`→turn_completed、1707 `case "failed":`→turn_error、1713 `case "interrupted":`→turn_aborted、1719-1720 default 不封口 | ✓✓ 精确 |
| 11 | F-R5-A1 前提链：`codec.go:372-379`→`events.go:221-228`→reducer 无 `case "error":` | 372 `if params.Turn.Status != remoteTurnStatusCompleted`、377 `event.Type = core.EventError`；events.go 221 `case core.EventError:`→226 `return "error", …`；`grep -n 'case "error":' go-bridge/projection_reducer.go` 无匹配（亲跑） | ✓✓ 精确 |
| 12 | F-R5-A2：`projection_types.go:221-226`；`projection_reducer.go:377-385` | 221 DetailLoadState、222 DetailReasonCode、226 DetailInline（TurnProjection 成员）；377-381 upsert 簿记+return、382-385 append 分支 | ✓✓ 精确 |
| 13 | 终态事件置 idle：`projection_reducer.go:2442-2444`/`:2500-2502`；aborted/error 渲染 `:2407-2417`/`:2464-2467` | 2442-2444 与 2500-2502 `exec := ExecutionView{Phase: "idle"}` 三行逐字吻合；2407-2417 终态保留注释；2464-2467 `status := "aborted"; if msg.Event == "turn_error" { status = "error" }` | ✓✓ 精确 |
| 14 | F-R5-A3：pong `stream.go:305-314`、typeAck `:305-306`、ping `ws.go:317` | agent/codex-remote/stream.go 305-306 `case typeAck: continue`、307-314 `case typePong:`（status!=active 判死）；ws.go 317 `pingInterval = 10 * time.Second`、318 `streamIdleLimit = 60 * time.Second` | ✓✓ 精确 |
| 15 | iOS `ProjectionStore.swift:164-181` 白名单 11 项不含 `projection.hydrate_failed`；`:885-887` 只读 code | 164 `isRetryablePullError` 函数头，166-176 恰 11 个 case（projection.hydrating/disconnected/not_connected/relay.not_ready/relay.closed/request_timeout/send_timeout/ping_timeout/connect_timeout/hello_timeout/websocket.closed），无 hydrate_failed；885 `(error as? CCCodeBridgeError)?.code`→887 传入 isRetryablePullError | ✓✓ 精确 |
| 16 | 官方 `websocket.rs:112-138` ack 游标、`:123` `(seq, seg.unwrap_or(MAX))`、`:1031-1067` seq 消息级分配 | codex-rs/app-server-transport/src/transport/remote_control/websocket.rs：112 `fn ack(`、123 `let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));`、1031-1038 per-(client,stream) `or_insert(1)`、1040-1045 拆分前按消息分配、1065-1067 `saturating_add(1)` | ✓✓ 精确 |
| 17 | 官方 `segment.rs:445-467` `seq_id: envelope.seq_id`、`:20-21` 150KB/100MB | 445 `fn build_chunk_envelope(`、463 `message_chunk_base64`、467 `seq_id: envelope.seq_id,`；20 `REMOTE_CONTROL_SEGMENT_MAX_BYTES = 150 * 1024`、21 `REMOTE_CONTROL_REASSEMBLED_MAX_BYTES = 100 * 1024 * 1024` | ✓✓ 精确 |
| 18 | `think.md:415-425` 750/s 风暴 | 416 节标题「codex-remote error 通知风暴」、419 「以 ~750/s 累积」 | ✓✓ 精确 |
| — | **§3.1「runner 复用」行**：`:1161` releaseDeferredPushCandidates、`:1176-1177` PublishProjectionPatch | grep 亲跑：`releaseDeferredPushCandidates(commit.AppliedPendingEventIDs)` 实际在 **handlers_projection.go:1163**（报告引 `:1161`，偏 2 行）；`PublishProjectionPatch(backendID, sessionID, …)` 实际在 **:1178**（报告引 `:1176-1177`，偏 1 行；1177 为 `if commit.PendingPatch != nil {` 守卫行） | **△ 语义正确、子行号偏 1-2 行**（见问题 2） |

### 1.3 锚点抽查小结

- 18 组抽样中 **17 组行号与语义全部精确吻合**，覆盖三仓、10 文件，包括 F-R5-1 核心（Commit 段）、F-R5-A1/A2/A3 三处小修、A-R6-1 证据、iOS 白名单与官方 seq/segment 事实——r6 报告的引用是真实的、可复核的，承重结论（F-R5-1 闭合、三条建议闭合、A-R6-1 事实基础）在源码层面成立。
- 1 组（§3.1「runner 复用」行）存在子行号 1-2 行偏差：语义（桥侧后处理复用、`persistCodexProducerSeed` 以 `backendID == "codex-remote"` 门控）完全正确，行级容器区间 `handlers_projection.go:1161-1183` 与方案侧引用 `:1182-1184` 均准确，仅摘录列两处子行号偏移。**不影响任何评审结论**，但按「行号与语义都必须吻合」的抽查标准记为问题（轻微）。

### 1.4 附带亲核的支撑性声明（抽样过程中顺带验证）

- `git diff 07721783..HEAD --name-only`（Mac 树亲跑）：9 文件全部为 `docs/*.md`——r6「代码零改动，Mac 锚点与 07721783 等价」声明成立。
- 官方 checkout：`git -C /Users/jacklee/Projects/codex rev-parse HEAD FETCH_HEAD` 两者同为 `e72da2b53805894878023d01949a25a082e0a5cb`，`status --porcelain` 干净——r6 记录成立。
- iOS 两候选树：`git merge-base --is-ancestor ccb5a0df 75359e69` 通过；`git diff --stat ccb5a0df..75359e69 -- <四锚点文件>`（ProjectionStore/CCCodeBridgeModels/ChatViewModel/CCCodeBridgeTransport）为空——r6「iOS 锚点结论与工作树选择无关」的亲核声明复算成立。

---

## 2. 第二项：来源清单完整性

### 2.1 规则要求

P0 来源门（AGENTS/CLAUDE.md）：存在多个工作树时「**必须先枚举所有工作树**，再选择与任务分支匹配的来源组合」；强制来源清单须为每一个涉及的仓库和工作树记录路径+分支+完整提交+未提交状态+任务预期来源/配套组合+预期产品特性。元审核任务进一步明确：覆盖**全部来源组合，含配套工作树，无论是否实际引用**。

### 2.2 实际枚举（本轮亲跑）

`git worktree list`（两仓亲跑）+ `rev-parse HEAD` + `status --porcelain`：

- **cordcode-macbridge：3 树**——`/Users/jacklee/Projects/cordcode-macbridge`（main @ `73539b71c0c3552a3c16548d05417813ba907869`）、`…macbridge-native-message-timeline`（feat/ios-native-message-timeline @ 同提交，评审所在树）、`…macbridge-plan-approval`（detached @ `b2b2523526b6af7990c9688fd29b7e285d7ce78c`）。
- **cordcode-ios：3 树**——`/Users/jacklee/Projects/cordcode-ios`（main @ `ccb5a0df647865324e11aedebbb089b9fc07bbb5`，干净）、`…ios-native-message-timeline`（feat/ios-native-message-timeline @ `75359e69dd8eee2e32f88a8d585e0e15bd13eb89`，干净）、**`…ios-plan-approval`（detached @ `a336b68bb37765d2ea23c14f6508619378c839ae`）**。
- openai/codex 官方 checkout：main @ `e72da2b5…`，HEAD==FETCH_HEAD，干净。

### 2.3 r6 报告覆盖情况核对

r6 §2.1 来源身份表记录了：Mac 仓 **3 树全量**（评审所在树含完整哈希与未提交状态；main 树与第三树 plan-approval 作为枚举项，短哈希经本轮 rev-parse 复核均解析正确，与 r3 已确认的枚举项格式一致）；iOS 仓 **仅 2 树**（main + 同族配套树，均完整哈希+干净状态）；官方 checkout 1 树。表内各行的分支/游离态明示、未提交状态、任务预期来源/配套组合、预期产品特性（N/A 纯设计）诸要素齐备。

**缺口：iOS 仓第三工作树 `/Users/jacklee/Projects/cordcode-ios-plan-approval`（detached @ `a336b68b`）未出现在 r6 报告任何位置**（grep 全文确认，报告唯一的「第三树」是 Mac 仓的 plan-approval 树）。

### 2.4 缺口定性（问题 1）

- **该树在 r6 评审时已存在**：目录创建时间为 2026-09-04（`stat` 亲查），远早于 r6 评审（2026-09-27）。
- **违反 P0 枚举要求**：iOS 仓是被涉仓库（ProjectionStore.swift 等锚点来源），有 3 个工作树，规则要求全部枚举；r6 对 Mac 仓照此记录了 3 树（含 detached plan-approval 枚举项），对 iOS 仓却只记 2 树——同一报告内两仓执行了不一致的枚举标准。
- **背离本评审链已确认的既定标准**：r2-meta（已确认）明确记录「cordcode-ios（3 个工作树）」含 `…ios-plan-approval（detached @ a336b68b）`；r3 勘误轮报告 §2.1 亦含「cordcode-ios（第三树）… a336b68b」。r5 报告同样遗漏（回归自 r5 起，r6 延续）。
- **实质影响评估（如实记录）**：该树 detached 于 `a336b68b`，是 iOS main（`ccb5a0df`）的祖先（`merge-base --is-ancestor` 亲跑通过），且同分支族两候选（main/配套树）均包含该提交；r6 未从该树取任何锚点，且已亲证四个 iOS 锚点文件在 main↔配套树两候选间零改动——**r6 的 iOS 锚点结论本身不受该遗漏影响**。但元审核此项的标准是来源清单**覆盖度**而非结论有效性：按 P0「枚举所有工作树」与本任务「含配套工作树，无论是否实际引用」的要求，r6 来源清单不完整，判定不通过。

---

## 3. 复核结论

| 复核项 | 结果 |
| --- | --- |
| ① 锚点抽查（18 组抽样） | 17 组行号+语义精确吻合；1 组（§3.1 runner 行）子行号偏 1-2 行、语义与区间正确——轻微问题 |
| ② 来源清单完整性 | **不通过**——遗漏 iOS 仓第三工作树 `cordcode-ios-plan-approval`（detached @ a336b68b），违反 P0 全量枚举要求，背离 r2-meta/r3 已确认的两仓各 3 树标准 |

**confirm = false**。r6 的 APPROVED 结论所依赖的承重锚点经抽查真实且精确，F-R5-1/F-R5-A1~A3 闭合与 A-R6-1 事实基础的源码核验可信；但来源清单存在完整性缺口，须在下轮报告（或 r6 报告勘误）中补记 iOS 第三工作树后方可确认通过。锚点子行号偏差一并列出，供下轮顺手修正，不单独构成否决依据。

### 问题清单

1. **[来源清单完整性——主问题]** r6 §2.1 来源身份表遗漏 iOS 仓第三工作树 `/Users/jacklee/Projects/cordcode-ios-plan-approval`（detached @ `a336b68bb37765d2ea23c14f6508619378c839ae`，2026-09-04 起存在）。P0 来源门要求枚举所有工作树；r2-meta/r3 勘误轮已确立「两仓各 3 树」记录标准（r3 报告曾明确记录该树），r5/r6 均缺失，属标准回归。该遗漏不推翻 r6 的 iOS 锚点结论（该树为 main 祖先、无锚点取自该树、四锚点文件跨两候选零改动），但来源清单覆盖度不满足任务规则要求。
2. **[锚点子行号偏差——轻微]** r6 §3.1「runner 复用 commit 后处理」行：摘录列 `:1161 h.releaseDeferredPushCandidates(…)` 实际位于 `handlers_projection.go:1163`（偏 2 行）；`:1176-1177 h.eventPublisher.PublishProjectionPatch(…)` 调用实际位于 `:1178`（`:1177` 为 if 守卫行，偏 1 行）。语义、行级容器区间（1161-1183）与方案侧引用（:1182-1184）均正确，不影响任何结论；其余 17 组抽样锚点全部精确。

---

## 4. 复核过程记录（命令与证据）

- `shasum -a 256 docs/…plan.md` → `c3b17e7d…f06798`；`head -n 344 … | shasum -a 256` → `d40e6694…06775`（均与 r6 报告头一致）
- `git worktree list`（macbridge 树、cordcode-ios 各亲跑）→ 两仓各 3 树（见 §2.2）
- 六树 `git rev-parse HEAD` / `branch --show-current` / `status --porcelain` 亲跑（完整哈希见 §2.2；评审所在树当前为 M 方案 + ?? r5/r6 报告，与 r6 记录的评审时点状态一致——r6 报告本身为评审产物）
- `stat -f '%SB' /Users/jacklee/Projects/cordcode-ios-plan-approval` → `Sep 4 02:03:52 2026`
- 锚点逐行核对：Read 实际文件 18 组（§1.2 表；文件与行号均列明）
- `grep -n "releaseDeferredPushCandidates(…)\|PublishProjectionPatch(…)\|persistCodexProducerSeed(…)" go-bridge/handlers_projection.go` → 1163/1178/1183（问题 2 证据）
- `grep -n 'case "error":' go-bridge/projection_reducer.go` → 无匹配（r6 声明复算成立）
- `grep -n "plan-approval\|第三树" docs/…review-r6.md` → 仅 Mac 仓第三树一行（§2.3 证据）；同 grep 于 r2-meta/r3/r5 报告 → r2-meta/r3 含 iOS plan-approval 记录、r5 不含（§2.4 证据）
- `git diff 07721783..HEAD --name-only` → 9 文件全 docs；`git -C /Users/jacklee/Projects/codex rev-parse HEAD FETCH_HEAD` → 同哈希；`git -C cordcode-ios merge-base --is-ancestor ccb5a0df 75359e69` → 通过；四锚点文件 `git diff --stat ccb5a0df..75359e69` → 空
