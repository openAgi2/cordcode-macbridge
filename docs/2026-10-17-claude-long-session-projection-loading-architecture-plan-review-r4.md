# 评审报告 R4：Claude 长 session 打开架构优化方案（v3 第四轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1/R2/R2-meta/R3 作回归清单核对闭合）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 v3
方案 SHA-256（全文，本轮实测）：`0dda4476301d2d0e4e9bfc9ac0304a0b2c4def4ba129f0bec8f3c631a018e24a`——与派发记录一致；交接块自报正文哈希 `b72cbd5ab77e86f2184bebf89c1a3345729907c1f28fdcbd2ecfc2fcf878c929` 以「正文至 `---` 分隔线前、rstrip 尾部换行」切分实测命中，记账自洽
范围：full（全量第四轮复审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **1**（F-17，新发现）
- advisories: **2**（F-18、F-19，新发现）
- **OD-6 定案（owner 已委托评审轮，本轮完成）：定案 W1（window 响应 additive 携带 scalar state）**，附两个定案条件（见 §3；定案结论不因 F-17 改变，F-17 修订正是定案条件 1 的内容）。
- **§3.2.2 存储选型定案维持：Mac = bbolt、iOS = 每.session 单文件 + 原子写**（本轮对五项选型约束全部亲核，无更优替代，见 §4）。
- 与 R3 的关系：R3 六条意见（F-11/F-12/F-13/F-14/F-15/F-16）+ R2 遗留 F-10 + F-3 残余经本轮逐项独立核验**全部闭合**，闭合质量高（§1.1 的 8 次投递、§3.2.2 的五项选型约束、§3.7 的现状缺口锚点全部亲核命中）。唯一阻塞项 F-17 是 v3 新增章节 §3.7 自身的新缺陷：W1 的 fence 语义「已知限制」归因与代码实际行为不符——真实边界是另一条（Execution-only patch 被 fence 清空，且 older page 期间 execution 更新实际**会**投递）。该缺陷直接影响 OD-6 定案回填后冻结的实施细节与 §7.1.4/§7.4.8 验收断言，必须修订后冻结。

## 2. 来源与覆盖

### 2.1 来源身份（本轮独立复核，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，`11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；未跟踪 5 个 docs 文件（方案 + R1/R2/R2-meta/R3；本轮新增 R4 报告） | ✓ 一致 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，**`dfb77ed93c610165730a88f8483c80e78941d321`**（方案/简报记 `1e895ea9`）；`1e895ea9..dfb77ed9` 仅 1 个 docs 提交（plan-review-loop workflow 文档，251 行新增），**不触及任何方案锚点文件**；脏文件 8 路径与 v3 §2.1 漂移标注逐项一致（M pbxproj / M LiveTailFollowController / M NativeTimelineViewController / M TimelineStreamingMarkdownCell / M LiveTailFollowControllerTests / ?? StreamingHeightPresentation / ?? NativeTimelineSmoothHeightTests / ?? StreamingHeightPresentationTests），全部锚点文件不在脏清单 | 🟡 提交哈希漂移（docs-only，无实质影响；S-0 重生成来源清单时更新即可，F-10 的「时点快照 + S-0 重生成」处置语义已覆盖） |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，`ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | ✓ 一致 |
| 正式 Mac runtime | 沿用 R2/R3 记录（PID 8303 监听 8777、runtime 与源码 HEAD 不同需分层）；本轮日志取证仅作为运行证据使用，未做产物身份推断 | ✓ 一致（分层告诫保留） |

哈希记账：派发哈希（全文 `0dda447…`）与实测一致，评审对象身份确定；交接块正文哈希 `b72cbd5a…` 实测命中（切分方式：`raw[:idx].rstrip(b'\n')`，idx = `\n---\n\n设计师交接块` 位置）。

### 2.2 事故样本与数字复测（audit-plan 专项）

本轮独立重算（python3，不使用历轮数字）：

| 方案 v3 声明 | 本轮实测 | 结果 |
| --- | --- | --- |
| **8 次** `sinceRev=0 outcome=snapshot`（req_18/33/54/58/67/77/82/91，17:30:56–17:33:52，connectionGeneration 1→5） | 日志独立 grep：8 条 `projection_rpc stage=response_enqueue` 逐条命中，req id、时间戳（17:30:56.864 / 17:31:36.714 / 17:32:23.368 / 17:32:25.662 / 17:33:09.120 / 17:33:10.040 / 17:33:12.185 / 17:33:52.331）、gen（1/2/3/3/4/5/5/5）全部吻合；sessionID 全部 = `bbe396e6` | ✓ **精确闭合（F-16 修正成立；R3 记 4 次为少算）** |
| 「用户可见重新打开至少 4 次」 | gen 1→5 = 4 次连接更替，保守推断成立 | ✓ |
| Mac 冷 hydrate 约 1.794s | log `projection_shadow stage=hydrate_commit … elapsedMillis=1794`（17:30:56.861） | ✓ 精确 |
| checkpoint 34,537,988 B、mtime 17:30:57 | stat：`34537988 bytes, mtime Oct 17 17:30:57 2026` | ✓ 精确 |
| 整棵 projection `ensure_ascii=False`+紧凑 = 34,336,858 B | 同口径复算 = **34,336,858 B** | ✓ **逐字节精确** |
| `ensure_ascii=True` = 40,663,541 B（虚高 18%） | 复算 40,754,044 B（+0.22%） | ✓ 量级/结论成立（见 F-19） |
| 最大单 turn `ensure_ascii=True` 7,940,153 B；字符数 5,284,620 | 复算 7,950,989 B（+0.14%）；**字符数 5,284,620 精确命中**；index **1**/119 精确命中 | ✓ |
| 最大单 turn 序列化 6,347,762 B > 4MiB 且不在尾部 17 内 | Go json.Marshal 模拟（含 HTML 转义 + U+2028/29）= 6,371,441 B（+0.37%，模拟本身略偏大）；R3 实测 Go 口径 6,336,926 B；`1 >= 119-17` → False | ✓ 结论三口径全部成立（见 F-19 口径标注残留） |
| 尾部 17 turns 约 3.79MB | 4MiB 从尾累积 = **17 turns** 精确；3,808,135 B = 3.81MB（十进制）/3.63MiB | ✓ |
| transcript 约 97MB | 101,520,809 B = 96.8 MiB；事故时点 log `startCut=101519857` 佐证 | ✓（MiB 口径，见 F-19） |
| E-2：事故 0 次 window RPC | 全日志 `grep -c get_session_projection_window` = **0** | ✓ verified 维持 |

### 2.3 锚点核验表（本轮亲核；引用保持方案原样）

**Mac 侧（§2.2 / §3.2.2 / §3.2.3 / §3.7 现状缺口）：**

| 方案原引用 | 实际位置与原文摘录 | 定位 / 语义 |
| --- | --- | --- |
| `go-bridge/main.go:560` | :560 `server.SetProjectionWindowEnabled(true)`（上方 PERF-S4D release gate 注释） | ✓ / ✓ |
| `projection_window.go:24-26`（never-split） | :24-26 `…a single turn larger than the budget is still served alone — the bound limits accumulation and never splits a turn`；:27-31 `maxWindowTurns=256` / `4 << 20` / `defaultWindowTurns=100` | ✓ / ✓ |
| `projection_window.go:77-82`（Response 无 scalar） | :79-84 `type ProjectionWindowResponse struct { Window; Turns; SyncRev; Resume }`——无 Execution/PlanMode/… | ✓（±2）/ ✓ |
| `projection_window.go:146-155`（turnEncodedSizes=json.Marshal） | :146-157 `payload, err := json.Marshal(turn); sizes[index] = len(payload)` | ✓ / ✓ |
| `projection_window.go:226-287`（sliceProjectionWindowWithUpstream） | :226 起全函数只处理 `proj.Turns`，零处引用 `Execution` | ✓ / ✓ |
| `projection_window_handler.go:32`（gate） | :32-35 `// Frozen gate: only connections that negotiated projection_window_v1…`；:36 `if !h.eventPublisher.ConnProjectionWindowV1(conn)` | ✓ / ✓ |
| `:93-99`（每 conn 独立 subscribe） | :93-99 `subscribeConnToSession…` | ✓ / ✓ |
| `:102-106`（forceColdInspection 无 sinceRev 门） | :102-106 `forceColdInspection := params.SessionID != "" && (msg.BackendID == "opencode" || …)`——**无 SinceRev 条件** | ✓ / ✓（F-14 事实成立） |
| `:124-126`（hydrating retryable） | :124-127 `case errors.Is(err, errProjectionHydrating): code = "projection.hydrating"; retryable = true` | ✓ / ✓ |
| `handlers_projection.go:122`（full 路径 sinceRev 门） | :122 `forceColdInspection := params.SinceRev == 0 && …` | ✓ / ✓（与 window 路径条件差异成立） |
| `:915-954`（projectionJSONLStartCut 尾部 4KiB 反扫） | :915-954 `const blockSize = int64(4096)` 反向扫描定位换行，**非整读** | ✓ / ✓ |
| `projection_types.go:341-359`（SessionProjection scalar 字段） | :341-359 `Execution ExecutionView` / `PlanMode` / `SessionMode` / `CollaborationMode` / `CodexGoal` / `Goal`（additive，nil = 无投影） | ✓ / ✓ |
| `projection_kernel.go:266-278`（prefix 整读） | :268-281 `digestProjectionSourcePrefix` → `io.CopyN(h, r, cursor)` | ✓（±2）/ ✓ |
| `:389-415`（validateProjectionSourceCheckpoint） | :389-415 Stat + `digestProjectionSourcePrefix` 全 prefix | ✓ / ✓ |
| `:1030-1045`（Ready 分支 AlreadyReady/catch-up） | :1025-1048 Ready 时 Segments 变化或 `source.Cursor > committedSourceCursor` → break 走重建；否则 AlreadyReady | ✓ / ✓ |
| `:280` / `:1123` | :280 `func (s *ProjectionCheckpointStore) LoadValidated(`；:1123 `checkpointHit := false` | ✓ / ✓ |
| `projection_snapshot_fence.go:230-234`（fence 清 Execution） | :225-235 `if p.connHoldsAnyTurnLocked(…) { continue }` → `resolved.Execution = nil`（连同 UpsertTurns/PartOps/TurnStateOps） | ✓ / ✓（机制方向对，场景归因见 F-17） |
| `agent/claudecode/continuation.go:379-392`（只 Stat） | :379-392 `richHistoryTranscriptSegments` 每 segment 仅 `os.Stat` | ✓ / ✓ |
| `server.go:373-395`（hello echo 单门） | :373-395 `negotiateProjectionWindowV1`：客户端声明 + `s.projectionWindowEnabled` → echo；**不查 descriptor** | ✓ / ✓（F-13 事实成立） |
| `data_dir.go:15-23` / `hello_handler.go:108-111` / `runtime_startup.go:137-140` / `main.go:158` / `relay_conn.go:296` | bridgeId 持久 / hello 下发 / epoch 进程唯一 / `generateBridgeEpoch()` / relay gzip threshold | ✓ 全命中（复用 R2/R3 并抽核原文） |
| §3.2.2 选型约束：go.mod 无 sqlite | `grep -c sqlite go.mod go.sum` → 0/0；require 块 = toml/semver/webpush/circl/pty/websocket/compress/crypto（与方案列举一致）；bbolt 未引入（属拟议新增，符合） | ✓ / ✓ |
| `MacBridge/project.yml:53` 无 CGO_ENABLED | :53 `GOOS=darwin GOARCH="$GO_ARCH" go build -trimpath …` | ✓ / ✓ |
| `session_catalog.go:14-16` / `import_guard_test.go:23-24` | :16-18 「SQLite is queried via the optional `sqlite3` CLI … to avoid adding a CGO/pure-Go sqlite dependency」；:23-24 forbidden `"sqlite"+"3"`（仅 opencode-web 包） | ✓ / ✓（bbolt 不触碰两条纪律的论证成立） |
| CI govulncheck | `.github/workflows/ci.yml:44-46` `govulncheck ./go-bridge/...` + `./...` | ✓ / ✓ |
| F-13「全仓非测试代码无一处把 projection_window_v1 写入 descriptor」 | `grep -rln projection_window_v1 --include="*.go"`（非测试）= server.go / projection_window_handler.go / projection_delivery.go / session_turn_items_handler.go / main.go；无任何 descriptor/StaticCapabilities 写入 | ✓ / ✓ |
| `docs/protocol/bridge-v1.md:2173-2175`（双门规范） | :2173-2175 「Per-backend support is additionally reported on the backend descriptor's `capabilities` list; **clients MUST gate window RPCs on the descriptor list, not the global echo**」 | ✓ / ✓ |

**iOS 侧（dfb77ed9 上亲核；锚点文件不在脏清单、docs-only 漂移不改内容）：**

| 方案原引用 | 实际位置与原文摘录 | 定位 / 语义 |
| --- | --- | --- |
| `ProjectionStore.swift:145`（15s hard cap） | :145 `/// Client-side open hard cap for \`pull\` (default 15s…)` | ✓ / ✓ |
| `:590`（window replica/coverage ledger） | :588-596 `// MARK: - Projection Window (PERF-S4C)`、`func windowCoverage(for:)` | ✓ / ✓ |
| `:763-777`（window_0 冷开唯一入口） | :763-777 `func pullOpenWindowed` → `guard await isProjectionWindowEligible() else { return await pullFull }` → `pullWindow(direction: .window0)` | ✓ / ✓ |
| `SessionProjectionWindow.swift:96-99`（emptySession 硬编码 .idle） | :93-99 `static func emptySession` → `execution: SessionExecutionView(phase: .idle, activeTurnId: nil)` | ✓（±2）/ ✓ |
| `:116-176`（applyingWindowPage 只合并 turns） | :118-177 全函数仅改 `syncRev`/`turns`，从不写 `execution` | ✓ / ✓ |
| `ChatViewModel.swift:2419-2426`（isSessionExecuting） | :2422-2428 `isSessionExecuting = projection.execution.phase ∈ {running, requires_action}`（design §7.4 注释） | ✓（±3）/ ✓ |
| `:1227-1233`（badge 从 projection.execution 写 runtimeStateStore） | :1227-1233 `switch projection.execution.phase { case .running… }` → `runtimeStateStore.updateState` | ✓ / ✓ |
| `ChatViewModel+TaskDock.swift:33` / `:36-39` | :33 `projection…execution.phase == .requires_action`；:36-39 `runtimeStateStore.badgeState(…) == .requiresAction`（独立 badge 路径存在，双源结构成立） | ✓ / ✓ |
| `ChatViewModel+SessionManagement.swift:851` | :852 `let outcome = await self.projectionStore.pullOpenWindowed(sessionId: sessionId)` | ✓（±1）/ ✓ |
| `CCCodeBridgeBackendClient.swift:1308-1310`（iOS 只查 echo） | :1306-1310 `var supportsProjectionWindow: Bool { await transport.supportsAcknowledgedCapability("projection_window_v1") }` | ✓ / ✓ |
| `ProjectionWireDecoder.swift:9`（全量 parse + re-encode） | :9-15 `JSONSerialization.jsonObject` → 取 data → `JSONSerialization.data` re-encode | ✓ / ✓ |
| `MessageCacheManager.swift:6` / `SessionSnapshotStore.swift:101` | :6 `actor MessageCacheManager`（LRU）；:101 `let maxSnapshotBytes: Int` | ✓ / ✓ |
| §3.7 第 2 点「`ProjectionReplica.applyWindowPage` change set 全部 execution: nil」 | `ProjectionReplica.swift` :298 `func applyWindowPage`；:342-346 与 :369-373 `executionChanged: false, execution: nil` | ✓ / ✓ |
| §3.2.2「iOS 零 SQLite 栈、仅 2 个 vendored package」 | `project.yml` packages = MarkdownView + BorderBeamKit（Litext/DisplayLink 为 sibling）；`grep -rn "import SQLite\|GRDB"` → 无匹配 | ✓ / ✓ |

**Codex 上游（8 项全部亲核命中）：** `0001_thread_history.sql:1`（CREATE TABLE thread_turns）✓；`thread_history_materialization.rs:42-44`（`next_byte_offset` 只处理 suffix）✓；`app-server-protocol/.../v2/thread.rs:419-430`（exclude_turns + initial_turns_page）✓；`thread_processor.rs:5888-5893`（THREAD_TURNS/ITEMS limit 常量）✓；`tui/.../history.rs:30-31`（INITIAL_HISTORY_TURN_LIMIT=5 / HISTORY_ITEM_PAGE_LIMIT=100；方案写 :28 为常量块上方 import 尾，±2 语义成立）✓；`core/src/config/mod.rs:4050-4051`（thread_unload_delay 1800s）✓；`thread_lifecycle.rs:17-18`（pending_thread_unloads）✓；`model_context.rs:30-32`（反向扫描 CompactedItem）✓；`cloud-client/README.md:18`（`Attach` has no history snapshot or replay guarantee）✓。

**think.md 六条（§2.5）：** Mac :450 / :834 / :1205 与 iOS :3343 / :3485 / :3491 内容全部命中 ✓。

### 2.4 覆盖声明

- 已核：v3 §0 修订范围表逐条对应正文；§1.1 全部数字（含 8 次投递逐条、三种口径重算、17 turns/最大 turn/字符数）；§2 全部锚点；§3.2.2 五项选型约束 + bbolt 论证；§3.2.3 冷检成本与组合路径；§3.7 全部现状缺口锚点（4 组）；§4/§5/§6/§7/§8/§9/§10 门控与交叉引用；协议文档 bridge-v1.md 双门条款原文；R3 六条 + F-10 + F-3 残余闭合；OD-6 三路线机制亲核（fence held 集合、patchTurnIDs、runtimeStateStore 写入者、supportsAcknowledgedCapability 扩展点）。
- 未核（不影响结论）：截图 heic（无视觉授权，日志时间线已闭环）；生产 runtime 精确构建 commit（S-0 职责）；`handlers_projection.go:598-602` sourceChanged 细节（F-14 已核的两处成本上界足以支撑结论）；bbolt 二进制体积/性能实测（S-2 实施期 E-6 覆盖）。
- 本轮新增核验面（历轮未覆盖）：fence held-turn 集合的累积/清除语义（`event_publisher.go:1674-1687` + `projection_snapshot_fence.go:215/288/297`）与 `patchTurnIDs`（`event_publisher.go:1726-1749`）——F-17 的唯一来源；`ProjectionReplica.applyWindowPage` 的 execution:nil change set。

## 3. OD-6 定案分析（owner 委托：W1 / W2 / W3 三路线定案）

**定案结论：W1（window 响应 additive 携带 scalar state），附两个定案条件。**

逐路线裁定（结合本轮亲核）：

**W1 定案理由：**

1. **冷开正确性是硬约束**：`window_0` 是 iOS 冷开唯一入口（`ProjectionStore.swift:763-777` 亲核），本案主场景恰是「打开运行中的超长 session」。W1 在同一响应内交付 execution，冷开即正确；W2 需第二次请求且两次响应间 patch 到达需定义排序/syncRev 对齐（竞态面大于 W1，无补偿收益）；W3 的 `runtimeStateStore` 是 live 事件消费者（写入者为 CodexStreaming/Generation/BackgroundGeneration 等 live 流处理，本轮 grep 亲核），冷开运行中 session 无历史事件可学——**结构性不覆盖主场景**，方案对 W3 的淘汰论证经核成立。
2. **R-2 首屏预算**：W1 零额外往返；W2 多一次 RTT 直接挤压 Direct ≤3s，且冷 Mac 场景叠加 `projection.hydrating` 重试（`projection_window_handler.go:124-127` 亲核 retryable=true）时进一步恶化。
3. **单一真相链路**：W1 的 scalarState 随权威 page 经 `ProjectionReplica` 与 full 路径同构写入，不产生第二真相源；W3 固化 badge vs projection.execution 双源（现状双源结构本轮亲核：`ChatViewModel.swift:1227-1233` 的 badge 写入与 `ChatViewModel+TaskDock.swift:36-39` 的独立读取）。
4. **协议增量与回退可控**：additive capability + 可选字段，旧 peer JSON additive 忽略；fail-closed（未收到 scalar echo 的新 iOS 不走 window）可基于既有 `supportsAcknowledgedCapability` 机制实现（`CCCodeBridgeBackendClient.swift:1308-1310` 亲核，扩展查第二个 capability 是标准操作）；回退 = 不 echo，无数据迁移（§8 已登记）。
5. **live 阶段延续性（本轮新核，优于方案自述）**：fence 的 held-turn 集合是连接级**累积**的（见 F-17 证据），window0 投递过 active turn 后，针对 active turn 的 live patch 不会被 fence 清 Execution——即 W1 落地后 live 阶段的 execution 更新在绝大多数场景（execution 变化伴随 active turn 的 turn ops）**自然工作**，无需额外补丁。

**定案条件（并入 S-1 范围，随定案回填 §4 OD-6 冻结）：**

- **条件 1（= F-17 修订）**：§3.7 W1 的 fence 语义段按代码真实行为重写（held 累积 + `patchTurnIDs` 交集判定），「已知限制」替换为真实边界——纯 Execution 变化（无任何 turn ops）的 patch 对 window 连接会被 fence 清空 `Execution`。
- **条件 2**：W1 实施范围必须处置该真实边界：给 fence 增加 scalar 分支（协商 `projection_window_scalar_v1` 的连接不执行 `resolved.Execution = nil`——会话级标量不属于 per-turn 数据，per-turn 清除规则不应作用其上），或以定向测试证明 Claude 链路不存在 Execution-only patch 并将该场景定义为已登记的已知行为。二者取一，不得留空。

**W2 / W3 淘汰**：理由如上 1/2/3；方案 §3.7 对比表的本轮复核意见——W2 行「竞态面更大」与 W3 行「冷开拿不到值」均经亲核成立；对比表无夸大。

## 4. §3.2.2 存储选型定案复核（评审重点 9）

**维持定案：Mac = `go.etcd.io/bbolt`、iOS = 每 session 单文件 + 原子写。**

- 五项选型约束全部亲核成立（§2.3 表）；bbolt 的关键属性（纯 Go、无 CGO、单模块、MIT、原生反向游标 `Seek`+`Prev`、单 `Update` 事务单写者）与 §3.2.2 的 schema 设计（head 与 rows 同 commit、epoch 仅作 provenance 不作有效性判据——该决策正确：per-start epoch 作判据会使每次重启整体误判失效，破坏 S-2 目标）匹配。
- 四个替代候选的淘汰理由经核无翻案依据：`mattn/go-sqlite3` 破坏纯 Go 交叉编译（`project.yml:53` 亲核无 CGO_ENABLED）；`modernc.org/sqlite` 显式推翻 `session_catalog.go` 记录在案的立场且 +10MB 级二进制；`sqlite3` CLI 核心常驻路径 shell-out 不可接受；自研 append-only 需重造 WAL/崩溃一致性，E-6 负担最大。
- §8 已登记 bbolt 大 value 写放大风险与 Phase B 拆行缓解；CI `govulncheck` 覆盖根模块（ci.yml:44-46 亲核）。

## 5. 意见

### F-17 [阻塞] §3.7 W1 的 fence「已知限制」归因与代码实际行为不符；真实边界（Execution-only patch 被 fence 清空）未被方案覆盖

- 位置：§3.7 W1「fence 语义」与「已知限制」句；§7.1.4 第三条（「older page（窗口不含 active turn）不投递 execution 更新的已知限制按设计断言」）；§7.4 场景 8（「older page 期间 execution 更新不投递的已知限制按 §3.7 W1 记录核对其 UI 表现」）。
- 证据（全部亲核）：
  1. held-turn 集合是连接级**累积**而非替换：`go-bridge/event_publisher.go:1674-1687` `recordConnWindowTurnsLocked` 执行 `p.projectionHeldTurns[conn][key][id] = struct{}{}`（:1682-1685，向 map 添加）；每次 window page 完成投递时在 fence 事务内注册该 page 的 turns（`go-bridge/projection_snapshot_fence.go:215`）。清除仅发生在 `ClearConnWindowTurns`（`projection_snapshot_fence.go:288/297`，投递失败/超时）与 sync_invalidate 路径——**older page 请求不清除既有 held 集合**。
  2. 因此「用户翻到 older page（窗口不含 active turn）期间 execution 更新不投递」**不成立**：window_0 已把 active turn 记入 held，older page 后它仍在 held；针对 active turn 的 live patch 满足 `connHoldsAnyTurnLocked`（`event_publisher.go:1707-1721`）→ 不清 `Execution` → execution 更新**会**投递。方案该句的括号场景推断错误；其前一句「fence 只清不持有该 turn 的连接」的机制描述本身是对的。
  3. 真实存在的边界是另一条：`patchTurnIDs`（`event_publisher.go:1726-1749`）只从 `TurnStateOps`/`PartOps`/`UpsertTurns` 提取 turn ID；**纯 Execution 变化（无任何 turn op）的 patch** 的 turnIDs 为空 → `connHoldsAnyTurnLocked` 对空集恒 false → `resolved.Execution = nil`（`projection_snapshot_fence.go:230-235`）——即 Execution-only patch 对**任何** window 连接都被清空，与是否翻 older page 无关。方案对这一真实边界零覆盖。
- 影响：OD-6 已定案 W1，定案结论回填后将冻结 W1 实施细节——冻结一份与代码行为不符的机制描述，会（a）使 §7.1.4/§7.4.8 的验收断言指向一个不存在的限制（测试无法按该归因写出有意义的失败场景）；（b）诱导实施者按错误模型补 UI 层 badge 显示（方案原文「需在 UI 层以既有 badge 路径补充显示」），而真实场景（older page 期间 execution 更新照常投递）不需要该补丁；（c）漏掉真实边界——若 Claude 链路存在 Execution-only patch，W1 落地后该场景仍丢 execution 且无测试捕捉。
- 修订方向（即 OD-6 定案条件 1+2，见 §3）：重写 §3.7 W1 fence 语义段为代码真实行为；「已知限制」替换为 Execution-only patch 边界并按定案条件 2 处置（fence scalar 分支或测试排除）；§7.1.4 第三条与 §7.4 场景 8 的断言同步改写。
- 闭合标准：§3.7 的 fence 描述与 `event_publisher.go:1674-1749` / `projection_snapshot_fence.go:215-235` 实际行为一致；§7.1.4/§7.4.8 断言可按真实机制写出确定性通过/失败判据；Execution-only patch 边界有显式处置（scalar 分支或排除测试）。

### F-18 [建议] F-13 登记的「capability 双门偏离」没有收口路径，将长期化

- 位置：§2.2「window capability gate」行（「已知偏离（v3 登记，F-13，S-1 不改）」）；§5 E-3。
- 证据：`docs/protocol/bridge-v1.md:2173-2175` 明写「clients MUST gate window RPCs on the descriptor list, not the global echo」，且服务端 echo 条件按文档应含 per-backend descriptor 支持；实现（`server.go:373-395` 只查全局开关；全仓非测试代码无一处 descriptor 写入——本轮 grep 复核）与 iOS（`CCCodeBridgeBackendClient.swift:1308-1310` 只查 echo）双侧偏离。F-13 的闭合标准（登记 + E-3 覆盖 descriptor 维度）已满足，但「不改」之后没有任何切片或过审后实施项承接收口。
- 影响：协议 pack 是权威（repo 规则：docs/protocol 为 source of truth）。偏离不收口意味着任何未来按文档实现 descriptor gate 的新客户端会静默不走 window，重演 §2.4 类问题；且 W1 定案新增 `projection_window_scalar_v1` 时若延续单门模式，偏离面继续扩大。
- 修订方向：在 S-1 的 protocol pack 同步（W1 additive 更新）或 S-3 v2 冻结时二选一收口：Mac 侧把 `projection_window_v1`（及 scalar capability）写入 backend descriptor 并按文档条件 echo；或修订 bridge-v1.md 该条款为单门语义。登记进「过审后实施」清单即可，不阻塞。
- 闭合标准：方案出现一个明确的收口实施项（含归属切片与方向）。

### F-19 [建议] §1.1 若干数字存在口径标注残留（结论均不受影响）

- 位置：§1.1「Claude transcript：约 97MB」；§1.1「最大单个 turn 序列化后 6,347,762 B」。
- 证据：本轮实测 transcript = 101,520,809 B = 96.8 MiB（事故时点 log `startCut=101519857` 佐证）——「约 97MB」在声明的「UTF-8 字节」口径下应为约 101.5MB，97 是 MiB 口径；6,347,762 B 为 python `ensure_ascii=False`+默认分隔符口径，而同一段声明的验收口径（Go `json.Marshal`）下 R3 实测为 6,336,926 B（差 10,836 B，本轮模拟复算 6,371,441 B 亦 >4MiB）。
- 影响：F-15 的闭合（口径声明存在）已达成，但这两处残留会在验收复算时产生 0.2–4.7% 的不可解释偏差；所有临界判定（>4MiB、17 turns、≤4MiB）在两个口径下结论一致，不影响通过性。
- 修订方向：「约 97MB」标注为 101,520,809 B（96.8 MiB）；6,347,762 补注口径或改引 Go 口径数字。
- 闭合标准：§1.1 两个数字带与其结论自洽的口径标注。

## 6. 复审处置与剩余门

### R3 六条 + R2 遗留 + F-3 残余闭合判定（本轮独立核验，未采信 v3 自述）

| 意见 | 级别 | 本轮判定 |
| --- | --- | --- |
| F-11 window_0 不携带 scalar state | 阻塞 | **闭合**。R-6 改主动设计目标、§2.2 锚点（4 组本轮全部亲核命中）、§3.7 三路线、OD-6 登记、E-10、§7.1.4/§7.2.10-11/§7.4.2、§3.4.1 preview 同存 scalar——结构完整且与源码吻合。（§3.7 内部新缺陷另立 F-17，不属 F-11 回归） |
| F-12 SQLite 选型冲突 | 阻塞 | **闭合**。§3.2.2 候选对比 + bbolt/文件型定案 + Gate A 前置；五项约束亲核成立；定案维持（§4）。 |
| F-13 capability 双门偏离 | 建议 | **闭合**（按其闭合标准：登记 + E-3 补 descriptor 维度）。收口缺口语义升级为新建议 F-18。 |
| F-14 window 强制冷检无 sinceRev 门 | 建议 | **闭合**。§3.2.3 组合路径 IO 上界（stat + 尾部 4KiB，两处锚点亲核）+ §7.3 指标行 + S-2 等价门登记；同时闭合 F-3 残余。 |
| F-15 字节口径未声明 | 建议 | **闭合**。§1.1 口径声明 + 三口径差异 + §7.3 表头；残留两处标注 → F-19。 |
| F-16 重试事实弱化、缺重试指标 | 建议 | **闭合**。8 次投递本轮日志逐条验证（R3 少算，v3 修正正确）；§7.3 重试=0 行、§7.4 计数、§9 第一阶段补齐。 |
| F-10 iOS 脏文件枚举漂移 | 建议 | **闭合**。时点快照语义 + S-0 重生成；本轮实测 8 项与 v3 标注一致。iOS HEAD 漂移至 `dfb77ed9`（docs-only，不触锚点），属同一处置语义，S-0 重生成时更新。 |

R1 九条（F-1~F-9）：维持 R3 闭合判定，本轮对其中全部关键锚点（bridgeId/epoch 四锚点、never-split、prefix 整读、Cloud README、materialization offset、think.md 六条等）亲核命中，无翻案依据。

### 实施前仍未满足的门

- 证据门：E-3（window 未生效根因，S-1 修复前必须闭合，含 descriptor 维度）、E-4（wire 级验证，S-1 验收）、E-5（iOS 阶段耗时，S-5 完成宣告）、E-6（materialization 正确性，S-2 完成宣告）、E-7（preview 不污染 writer，S-4 完成宣告）、E-10（scalar state 正确性，S-1 完成宣告，按 W1 定案路线）。
- 决定门：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4 开工，OD-2 阻塞 S-3 协议冻结）。**OD-6 本轮已定案 W1（附条件）**，待修订回填 §4 后冻结，不再列为未决门。
- 授权门：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。
- 方案门：F-17 修订完成前，OD-6 定案回填与冻结不得执行（S-1 开工前置包含 OD-6 定案回填）。

### implementation_readiness

- 修订 F-17（§3.7 一段重写 + §7.1.4/§7.4.8 两行 + §1.1 两个数字标注；F-18 登记可选）并回填 OD-6 定案后：S-0 可立即开工；S-1 开工门（E-1/E-2 verified + OD-6 定案）满足，完成宣告仍需 E-3/E-4/E-10；S-2 需存储格式冻结 + 测试计划；S-3 需 OD-2；S-4 需 OD-1/OD-3；S-6 需 owner 授权。本报告不构成实施授权。

## 7. 评审过程记录

- 评审员模式 fresh：先独立形成风险清单（三仓锚点、样本数字复算、日志取证、fence/选型/OD-6 机制、门控一致性、§10 评审重点、audit-plan 形状、哈希记账），完成全部亲核后才读 R1/R2/R2-meta/R3 作回归核对；历轮报告仅作回归清单，未作为任何结论来源。
- 与历轮的分歧记录：本轮确认 R3 全部六条闭合（含 R3 自身少算的 4 次重试——v3 修正为 8 次经本轮逐条验证正确）；R2 的 APPROVED 被R3 推翻后，v3 已按 R3 完成结构性修订；本轮新阻塞 F-17 属 v3 新增内容的机制性错误，非历轮意见回归。
- 本轮唯一写入文件：本报告。方案、R1/R2/R2-meta/R3、三仓源码、iOS 8 个未提交文件一律只读未触碰。
- 使用的契约：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（外部内容形状专项实际运行：checkpoint dump 三口径字节复测、4MiB 尾部累积模拟、日志 8 次投递逐条 grep、协议文档条款原文核对）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 0dda4476301d2d0e4e9bfc9ac0304a0b2c4def4ba129f0bec8f3c631a018e24a
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r4.md
scope: full
blockers: 1
advisories: 2
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, OD-1, OD-2, OD-3, S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
