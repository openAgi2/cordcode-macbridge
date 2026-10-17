# 评审报告 R5：Claude 长 session 打开架构优化方案（v4 第五轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R4 作回归清单核对闭合；R1/R2/R2-meta/R3 未读，其结论经 R4 报告转述与方案 §0 历史表核对）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v4**（按 R4 修订，OD-6 定案 W1 已回填）
方案 SHA-256（全文，本轮实测）：`ef386fdff0d154b6df170dc99177a26bf23f871a3254679ea1a94b6caed5e6fc`——与派发记录一致；交接块自报正文哈希 `7e11ac04c3b1f6c103a9d90549585c764d1af60db7d485ecb8e236e705bc4e6c` 按「`raw[:idx].rstrip(b'\n')`，idx = `\n---\n\n设计师交接块`」切分实测命中，记账自洽
范围：full（全量第五轮复审）

**身份备注（简报滞后，非身份不符）**：任务简报叙述「现为 v3 / 本轮 v3 正文 b72cbd5a…」，为 R4 派发前的旧叙述；实际派发哈希 `ef386fdf…` 对应**按 R4 修订的 v4**（v3 全文 = `0dda447…`、v3 正文 = `b72cbd5a…`，均记录于 v4 版本链与 R4 报告）。派发哈希是身份权威且与实测一致，评审对象身份确定，按 v4 全量复审。简报第 7 字段的「OD-6 待定案分析」在 R4 已完成定案（W1）并回填 v4，本轮按 v4 §10.8 的请求执行**定案复核**而非初次定案。

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **1**（F-20，新发现——v4 新改内容 §3.7 的机制描述缺陷，非历轮意见回归）
- advisories: **0**
- **OD-6 定案复核（owner 委托链：owner → 评审轮 → R4 定案 W1 → 本轮复核）：定案 W1 维持**。本轮独立分析三条路线后结论与 R4 一致（§3）。但 **R4 定案条件 1（「§3.7 W1 fence 语义段按代码真实行为重写」）未完全达成**：v4 重写后的 fence 语义仍遗漏 `decide` 路由维度，三处描述与代码实际行为不符（F-20）。定案结论 W1 不受影响；受影响的是已冻结的机制描述与 §7.1.4 验收断言的可构造性。
- **§3.2.2 存储选型定案复核：维持**（Mac = bbolt、iOS = 每 session 单文件 + 原子写；五项约束本轮全部亲核，无翻案依据，§4）。
- 与 R4 的关系：R4 三条意见（F-17 阻塞 / F-18 / F-19）经本轮逐项核验**全部闭合**（F-17 的三处锚点改写方向正确、F-18 四处登记一致、F-19 两处口径标注命中）；R4 已核验的 R3 六条 + F-10 + F-3 残余闭合经本轮关键锚点抽核**维持**。唯一阻塞 F-20 是 v4 按 F-17 重写的 §3.7 段自身仍存在的机制性偏差——**R4 的 F-17 修订与 v4 的回填同样遗漏了 `decide` 路由维度**（R4 报告 §2.3/§5 的 fence 证据亦未覆盖该维度），属本轮新增核验面发现的旧缺陷残余，非修订回归。

## 2. 来源与覆盖

### 2.1 来源身份（本轮独立复核，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 v4 §2.1 声明 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，`11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；未跟踪 6 个 docs 文件（方案 + R1–R4 报告；本轮新增 R5 报告），无代码变更 | ✓ 一致 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，**`dfb77ed93c610165730a88f8483c80e78941d321`**（方案记 `1e895ea9`）；脏文件 8 项（M pbxproj / M LiveTailFollowController / M NativeTimelineViewController / M TimelineStreamingMarkdownCell / M LiveTailFollowControllerTests / ?? StreamingHeightPresentation / ?? NativeTimelineSmoothHeightTests / ?? StreamingHeightPresentationTests）与 v4 §2.1 漂移标注逐项一致，**全部方案锚点文件不在脏清单** | 🟡 提交哈希漂移（R4 已实测同值并认定为 docs-only 提交、不触锚点；F-10 的「时点快照 + S-0 重生成」处置语义已覆盖，不另立意见） |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，`ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 无输出（干净） | ✓ 一致 |
| 正式 Mac runtime | 沿用简报/R4 记录（`/Applications/CordCodeLink.app` 内嵌 runtime，运行时构建身份与源码 HEAD 不同，日志结论分层）；本轮日志取证仅作运行证据，未做产物身份推断 | ✓ 一致 |

### 2.2 事故样本与数字复测（audit-plan 专项，本轮独立重算/重取证）

| 方案声明 | 本轮实测 | 结果 |
| --- | --- | --- |
| 窗口 17:30:56–17:33:52 内 **8 次** `sinceRev=0 outcome=snapshot`（req_18/33/54/58/67/77/82/91） | `grep projection_rpc + response_enqueue + sinceRev=0 + outcome=snapshot` 全日志共 9 条；窗口内恰好 8 条，req id 与时间戳（17:30:56.864 / 17:31:36.714 / 17:32:23.368 / 17:32:25.662 / 17:33:09.120 / 17:33:10.040 / 17:33:12.185 / 17:33:52.331）逐条吻合；第 9 条 req_3 在 17:30:50.377（窗口前）且不含 `bbe396e6`；含 `bbe396e6` 计数 = 8 | ✓ **精确闭合** |
| transcript 101,520,809 B = 96.8 MiB（约 97MiB，F-19） | `stat` = 101,520,809 B（mtime Oct 17 17:36:36）；101520809/1048576 = 96.81 MiB | ✓ 精确 |
| checkpoint 34,537,988 B、mtime 17:30:57 | `stat` = 34,537,988 B，mtime Oct 17 17:30:57 | ✓ 精确 |
| 尾部 17 turns 约 3.79MB（4MiB 规则）、119 turns | checkpoint（顶层 `projection` 包装）解析：119 turns；自尾累积 4MiB（python 紧凑 + HTML 转义近似 Go `json.Marshal`）= **17 turns / 3,808,135 B** | ✓（与 R4 记录逐字节一致） |
| 最大单 turn 序列化 > 4MiB 且不在尾部 17 内（index 1/119） | 同法复算 = 6,371,441 B @ index 1；`1 >= 119-17` → False | ✓（三口径结论一致，v4 的 Go 口径补注 6,336,926 B 保留 R3 实测值） |

### 2.3 锚点核验表（本轮亲核；引用保持方案原样）

**Mac 侧——本轮重点核 v4 新改的 §3.7 fence 语义链（F-20 证据基础）：**

| 方案原引用 | 实际位置与原文摘录 | 定位 / 语义 |
| --- | --- | --- |
| `event_publisher.go:1674-1687`（held 累积） | :1674-1687 `recordConnWindowTurnsLocked` 向 `projectionHeldTurns[conn][key][id]` map 逐个添加 | ✓ / ✓ |
| `projection_snapshot_fence.go:214-216`（fence 事务内注册 held） | :214-216 `if len(heldTurnIDs) > 0 && !fence.invalidated { p.recordConnWindowTurnsLocked(...) }` | ✓ / ✓ |
| `projection_snapshot_fence.go:230-235`（清空四字段） | :223-236 循环体：**:224 `if !frame.decide { continue }`** → :227 held 判定 → :230-235 `resolved.UpsertTurns/PartOps/TurnStateOps/Execution = nil` | ✓ 定位 / **✗ 语义不完整——v4 未覆盖 :224 的 decide 门**（F-20） |
| `projection_snapshot_fence.go:288/297`（投递失败/超时清除） | :288 `resultDone` err → `ClearConnWindowTurns`；:292-297 超时 → 清除 + `conn.Close()`（另 :247-249 queue 溢出路径 `clearConnWindowTurnsLocked` + Close，同属投递失败类） | ✓ / ✓（older page 请求确无清除路径） |
| `event_publisher.go:891-908`（fence pending 排队） | :890-909 fence 存在时 `appendFencePatchLocked(..., false)` → `held_in_fence` | ✓ / ✓（但该调用 **decide=false**） |
| `event_publisher.go:852-945`（常规广播完整投递） | :852-945 `deliverProjectionPatchLocked` 对所有 syncV2 conn：fence 内排队（decide=false）、fence 外 :923 直接 `tryEnqueue`，**全函数零处清空 Execution** | ✓ / ✓ |
| `event_publisher.go:1726-1749`（patchTurnIDs） | :1726-1749 只从 `TurnStateOps/PartOps/UpsertTurns` 提取，不含 Execution | ✓ / ✓ |
| `event_publisher.go:1707-1722`（connHoldsAnyTurnLocked 空集恒 false） | :1707-1722 空 turnIDs 循环不执行 → false | ✓ / ✓ |
| `claude_stub_status.go`（本轮新增核验面） | :143-158 `syncProjectionExecution` → `ApplyExternalExecutionState` → `FlushProjectionPatch` → **:157 `PublishProjectionPatch`（常规广播链）**；reducer（`projection_reducer.go:539-560`）只改 `ps.projection.Execution`、SyncRev++，**零 turn op** | — / **Claude 链路真实存在的 Execution-only patch 走 decide=false 链**（F-20 关键证据） |
| `event_publisher.go:764-776 / :1810`（decide=true 唯一来路） | :771-776 `deferProjectionPatchForFenceLocked` → `appendFencePatchLocked(..., true)`，唯一调用链 = `PublishProjectionDetail`（:1769-1818）的 rule-4 分支（:1802-1811）；detail commit 构造点（`turn_detail_kernel_commit.go:52-59`、`turn_detail_batch_engine.go:239-251`、`session_turn_items_handler.go:222-226` 等）全部经 `CommitTurnStateOps/V2`（含 TurnID 的 TurnStateOp） | — / **decide=true 链当前不存在 Execution-only patch**（F-20 关键证据） |
| `projection_window.go:77-82`（Response 无 scalar） | :77-82 `ProjectionWindowResponse { Window; Turns; SyncRev; Resume }`；`grep Execution projection_window*.go` 三文件零命中 | ✓ / ✓ |
| `projection_types.go:341-359 / :376` | `SessionProjection` 含 `Execution/PlanMode/SessionMode/CollaborationMode/CodexGoal/Goal`；`ProjectionPatch.Execution *ExecutionView omitempty` | ✓ / ✓ |
| `server.go:373-395`（hello echo 单门） | :373-395 只查客户端声明 + `session_sync_v2` + 全局开关，**不查 descriptor** | ✓ / ✓（F-13/F-18 事实成立） |
| `docs/protocol/bridge-v1.md:2173-2175`（双门规范） | :2170-2175 「Per-backend support is additionally reported on the backend descriptor's `capabilities` list; **clients MUST gate window RPCs on the descriptor list, not the global echo**」 | ✓ / ✓ |
| descriptor 写入缺失（F-13） | `grep -rn projection_window_v1`（非测试）：server.go / projection_window_handler.go / projection_delivery.go / session_turn_items_handler.go / main.go，无任何 descriptor 写入 | ✓ / ✓ |
| §3.2.2 五项选型约束 | `grep -c sqlite go.mod go.sum` = 0/0；`MacBridge/project.yml:52-54` `GOOS=darwin GOARCH=… go build`（无 CGO_ENABLED）；`agent/grokbuild/session_catalog.go:14-16`「to avoid adding a CGO/pure-Go sqlite dependency」；`agent/opencode-web/import_guard_test.go:23-24` forbidden `"sqlite"+"3"`（仅该包）；`.github/workflows/ci.yml:44-46` `govulncheck ./go-bridge/...` + `./...` | ✓ 全命中 |

**iOS 侧（dfb77ed9 上亲核；锚点文件不在脏清单）：** `SessionProjectionWindow.swift:96-99`（`emptySession` execution 硬编码 `.idle`）✓；`:116-135`（`applyingWindowPage` 只合并 turns/syncRev，零处写 execution）✓；`ProjectionReplica.swift:342-346 / :369-373`（applyWindowPage change set `executionChanged: false, execution: nil`；对照 patch 路径 :161-165 有 execution）✓；`ChatViewModel.swift:2419-2428`（`isSessionExecuting` 读 `projection.execution.phase`）✓；`:1227-1235`（badge 从 projection.execution 写 runtimeStateStore）✓；`ChatViewModel+TaskDock.swift:33-40`（projection 分支 + `runtimeStateStore.badgeState` 独立分支，双源结构成立）✓；`ProjectionStore.swift:763-777`（`pullOpenWindowed` window_0 冷开入口）✓；`CCCodeBridgeBackendClient.swift:1308-1310`（只查 echo，`supportsAcknowledgedCapability` 可扩展查第二 capability——W1 fail-closed 可实现）✓；runtimeStateStore 写入者 grep = `ChatViewModel+CodexStreaming.swift:1304-1323`、`ChatViewModel+Generation.swift:1315`、`ChatViewModel+BackgroundGeneration.swift:105`、`ChatViewModel+SessionManagement.swift:398/519/1052/1102` 等——**全部为 live 事件流路径**（W3 淘汰理由亲核成立）✓；`project.yml` 仅 MarkdownView + BorderBeamKit 两个 vendored package、全源码无 `import SQLite`/`GRDB` ✓。

**Codex 上游（本轮抽核 3 项关键 + 复用 R4 全核 8 项）：** `thread_history_materialization.rs:38-45`（`next_byte_offset` 只处理 suffix）✓；`0001_thread_history.sql:1`（CREATE TABLE thread_turns）✓；`app-server-protocol/.../v2/thread.rs:419-430`（exclude_turns + initial_turns_page）✓。

### 2.4 覆盖声明

- 已核：v4 §0 修订范围表逐条对应正文；§1.1 全部数字（8 次投递逐条、transcript/checkpoint stat、尾部 17 turns、最大 turn 三口径）；§2.2 全部锚点行（含 F-18 收口指针与 F-13 事实）；§3.2.2 五项选型约束 + bbolt 定案论证；§3.2.3 冷检成本锚点（R4 全核 + 本轮抽核 `handlers_projection.go:122` 差异成立）；**§3.7 全部现状缺口锚点（4 组）与 v4 重写后的 fence 语义三 bullets（发现 F-20）**；§4 OD-6 回填行；§5 Gate A/E-10；§6 F-18 收口实施项四处登记一致性；§7.1.4 五条断言；§7.4 场景 2/8；§8 风险表 scalar 行；§10 评审重点 8/9/10；R4 三条意见闭合；R3 六条 + F-10 + F-3 残余闭合（关键锚点抽核维持）；OD-6 三路线机制亲核（fence decide 路由、held 累积、patchTurnIDs、runtimeStateStore 写入者、supportsAcknowledgedCapability 扩展点）；哈希记账（全文 + 交接块切分正文）。
- 未核（不影响结论）：截图 heic（无视觉授权，日志时间线已闭环）；`think.md` 六条原文（R4 全核命中，本轮复用——其条目为背景佐证非机制依据）；bbolt 二进制体积/性能实测（S-2 实施期 E-6 覆盖）；生产 runtime 精确构建 commit（S-0 职责）。
- 本轮新增核验面（历轮未覆盖）：**fence pending 的 `decide` 路由维度**（`projection_snapshot_fence.go:224` 的 decide 门、`appendFencePatchLocked` 的两处 decide=false 调用 vs `deferProjectionPatchForFenceLocked` 的 decide=true 唯一来路）与 **Execution-only patch 两条发布链的实际存在性**（`claude_stub_status.go` 常规链 vs detail commit 构造点全含 TurnID）——F-20 的唯一来源。

## 3. OD-6 定案复核（owner 委托链；v4 §10.8 请求项）

**本轮独立分析结论：定案 W1 维持（与 R4 定案一致）。** 独立推理（非转述 R4）：

1. **冷开正确性（一票否决 W3）**：`window_0` 是 iOS 冷开唯一入口（`ProjectionStore.swift:763-777` 亲核），本案主场景 = 打开运行中的超长 session。W1 在同一响应内交付 execution，冷开即正确；W3 的 runtimeStateStore 写入者经本轮 grep 全部为 live 事件流路径（§2.3 iOS 段），冷开运行中 session 无历史事件可学——**结构性不覆盖主场景**，且固化 badge vs projection.execution 双源（双源结构本轮亲核成立），与 SSV2「projection 是真相」冲突。W3 淘汰维持。
2. **R-2 预算（否决 W2）**：W1 零额外往返；W2 多一次 RTT 直接挤压 Direct ≤3s，且冷 Mac 场景叠加 `projection.hydrating`（retryable=true，`projection_window_handler.go:124-127`）重试进一步恶化，还需定义 page 与 scalar 两次响应间 patch 到达的排序/syncRev 对齐——竞态面大于 W1 而无补偿收益。W2 淘汰维持。
3. **单一真相链路（W1 优势）**：scalarState 随权威 page 经 `ProjectionReplica` 与 full 路径同构写入（patch 路径 `ProjectionReplica.swift:161-165` 已有 execution 消费形态，window 路径补齐即可），不产生第二真相源。
4. **协议增量与回退可控**：additive capability + 可选字段，旧 peer JSON additive 忽略；iOS 侧基于既有 `supportsAcknowledgedCapability`（`CCCodeBridgeBackendClient.swift:1308-1310`）扩展 fail-closed 判定是标准操作；回退 = 不 echo，无数据迁移（§8 已登记）。
5. **live 阶段延续性（本轮新核，且比 v4 自述更强）**：常规 live patch 在 fence pending 中以 decide=false 原样投递、**不做 held 判定、不清空 Execution**（F-20 证据）；真实存在的 Execution-only patch（stub status 外部执行态信号）恰走此链不受影响——W1 落地后 live 阶段 execution 延续性比 v4 §3.7 描述的更稳，进一步支持 W1。

**但定案条件 1 未完全达成**：OD-6 定案条件 1（= F-17 修订）要求「§3.7 W1『fence 语义』段按代码真实行为重写」，闭合标准（R4 报告 F-17）为「§3.7 的 fence 描述与 `event_publisher.go:1674-1749` / `projection_snapshot_fence.go:210-235` 实际行为一致」。v4 重写后的描述仍与实际行为不一致（F-20，§5）。**定案结论 W1 与定案条件 2（fence scalar 分支设计）均不受 F-20 影响**——scalar 分支作为防御「未来新增 detail commit 构造点产生 Execution-only commit」的分支仍然正确且低成本（guard 范围写宽无害）；方案「不依赖 Claude 链路不存在 Execution-only patch」的立场不变。F-20 修订完成后 OD-6 冻结内容即为准确。

## 4. §3.2.2 存储选型定案复核（v4 §10.9 请求项）

**维持定案：Mac = `go.etcd.io/bbolt`、iOS = 每 session 单文件 + 原子写。**

- 五项选型约束本轮全部亲核命中（§2.3 表）：go.mod/go.sum 零 sqlite、`project.yml:52-54` 无 CGO_ENABLED、`session_catalog.go:14-16` 记录在案的避免 sqlite 立场、`import_guard_test.go:23-24` forbidden 仅 `sqlite3` 且仅 opencode-web 包（bbolt 不触碰）、iOS 零 SQLite 栈。
- 四个替代候选淘汰理由经核无翻案依据：`mattn/go-sqlite3` 破坏纯 Go 交叉编译；`modernc.org/sqlite` 显式推翻 `session_catalog.go` 立场且 +10MB 级二进制；`sqlite3` CLI 核心常驻路径 shell-out 不可接受；自研 append-only 需重造 WAL/崩溃一致性（E-6 负担最大）。
- bbolt 属性与 §3.2.2 schema 匹配（单 `Update` 事务 head 与 rows 同 commit、epoch 仅 provenance 不作有效性判据——该决策正确、反向会使每次重启整体误判失效；反向游标 `Seek`+`Prev` 支持尾部分页）；§8 已登记大 value 写放大风险与 Phase B 拆行缓解；CI `govulncheck` 覆盖（ci.yml:44-46 亲核）。

## 5. 意见

### F-20 [阻塞] §3.7 W1 fence 语义（v4 按 F-17 重写后）仍与代码行为不符：遗漏 `decide` 路由维度，「Execution-only 边界」的实际可达性与所述相反；§7.1.4 两条断言缺构造前提

- 位置：§3.7 W1「fence 语义」段三个 bullets（「held 集合是连接级累积的……」「清空只发生在 fence pending 路径……」「真实边界 = Execution-only patch……」）；§7.1.4 第三条（held 累积语义断言）与第四条（fence scalar 分支断言，含负对照「未协商该 capability 的连接保持现状（清空）」）；§3.7 第 4 点边界处置的触发范围描述。
- 证据（全部本轮亲核）：
  1. **fence 完成事务只对 `decide=true` 的 pending frame 做 held 判定与清空**：`projection_snapshot_fence.go:224` `if !frame.decide { continue }`，:227 held 判定、:230-235 四字段清空仅作用于 survive 该门的 frame。`decide=true` 的唯一来路是 `deferProjectionPatchForFenceLocked`（`event_publisher.go:771-776` → `appendFencePatchLocked(..., true)`），其唯一调用链为 `PublishProjectionDetail` 的 rule-4 分支（`event_publisher.go:1802-1811`）——即 **turn_detail_lazy/chunks/session_turn_items 家族的 detail commit**。
  2. **常规 live patch（`PublishProjectionPatch` → `deliverProjectionPatchLocked`）排进 fence.pending 时 decide=false**（`event_publisher.go:901`；单连接路径 :826 同），fence 完成后在 :269-276 **原样投递、不判 held、不清空 Execution**——v4 bullet 1「针对 active turn 的 live patch 仍满足 held 交集判定 → 不清 Execution → 照常投递」的归因错位：常规链的投递与 held 无关；bullet 2「fence 完成事务对不持有该 patch 任何 turn 的 window 连接清空 patch 内容」漏掉 decide 门，把清空范围夸大到所有 fence.pending。
  3. **「真实边界」的实际可达性与 v4 所述相反**：会被清空的链（detail commit，decide=true）的现有构造点全部经 `CommitTurnStateOps/V2`（含 TurnID 的 TurnStateOp：`turn_detail_kernel_commit.go:52-59`、`turn_detail_batch_engine.go:239-251`、`session_turn_items_handler.go:222-226` 等），patchTurnIDs 恒非空——**当前代码不存在 Execution-only 的 decide=true patch**；而 Claude 链路真实存在的 Execution-only patch 是 `claude_stub_status.go:143-158`（外部执行态信号 → reducer `ApplyExternalExecutionState`（`projection_reducer.go:539-560`）只改 Execution、零 turn op → :157 `PublishProjectionPatch`）——**走 decide=false 常规链，不会被 fence 清空**。即「纯 Execution 变化的 patch 在 fence pending 中对任何 window 连接都被清空 Execution」这一 v4 冻结表述所描述的事件在当前代码中不可达；该边界实际是「**未来新增** detail commit 构造点产生 Execution-only commit」的防御空间。
  4. 由此 v4 的「Claude 链路的 execution 变化通常伴随 turn ops，但该性质无源码保证」亦不精确：存在一条**从不**伴随 turn ops 的 execution 变化路径（stub status），且它恰好因 decide=false 而安全——「是否伴随 turn ops」根本不是该路径的安全判据，发布链才是。
  5. **§7.1.4 断言可构造性缺陷**：第四条负对照（「未协商 capability 的连接保持现状（清空）」）只有在 decide=true 路径才可观测。按断言文面（「纯 Execution 变化 patch，无任何 turn op」）最自然的构造是 stub status 式常规 patch——该 patch decide=false、fence 完成后原样投递，「清空」现状**观察不到**，测试会因走错链而「通过但未测到 scalar 分支」；第三条如用常规 live patch 验证「held 交集 → 不清空」，同样测的是 decide=false 路径而非 held 路径。两条断言必须写明构造前提（held 断言与 scalar 分支断言需经 `PublishProjectionDetail` 触发或直接以 decide=true pending frame 构造 fence 完成事务），否则无法按文面写出确定性失败判据——与 R4 对 F-17 定阻塞的理由同构（「测试无法按该归因写出有意义的失败场景」）。
- 影响：OD-6 已定案回填冻结，§3.7 是冻结内容并直接指导 S-1 实施（fence scalar 分支的 guard 位置与条件、定向测试构造）与 §7.1.4 验收断言。冻结一份仍与代码行为不符的机制描述：（a）§7.1.4 负对照测不到目标路径，E-10 验收留空洞；（b）「execution 变化通常伴随 turn ops」的错误认知会误导测试选样——真实存在的 Execution-only patch（stub status）恰是回归护栏最该覆盖的样本，且它证明的是「发布链决定安全性」而非「turn ops 伴随性」；（c）实施者可能据错误模型在常规广播链上叠加不必要的 Execution 保护（guard 写宽无害但增加 review 面与测试噪音）。定案结论 W1 与 fence scalar 分支设计本身不受影响（§3）。
- 修订方向（最小修订，不动 W1 定案与 scalar 分支设计）：
  1. §3.7 fence 语义段补第四个事实维度：fence.pending 区分 `decide=true`（rule-4 detail commit 延迟，完成时判 held 并可清空四字段）与 `decide=false`（常规 live patch，完成时原样投递、不判 held、不清空）；held 交集判定与 Execution 清空只作用于前者（`projection_snapshot_fence.go:223-236`、`event_publisher.go:771-776/:901/:1802-1811`）。
  2. 「真实边界」改写为准确表述：当前 detail commit 构造点全部含 TurnID，Execution-only 的 decide=true patch 在现有代码不可达；真实存在的 Execution-only patch（claude stub status 外部执行态信号，`claude_stub_status.go:143-158`）走常规链不受 fence 清空。fence scalar 分支因此定位为**防御未来新增 detail commit 构造点**的条件分支（保留 OD-6 定案条件 2 的设计与理由不变）。
  3. §7.1.4 第三条改写为分别断言两条链：常规 live patch（含 Execution-only 形态，可用 stub status 路径构造）在 fence 期间对 window 连接原样投递；detail commit（decide=true）在 held 交集命中时不清空。第四条补构造前提：Execution-only patch 须以 decide=true 进入 fence.pending（经 `PublishProjectionDetail` 或直接构造 pending frame），负对照「未协商连接保持清空」才可观测。
  4. §3.7「Claude 链路的 execution 变化通常伴随 turn ops」句删除或改写为「Execution-only patch 存在于常规发布链（stub status），其安全性来自 decide 路由而非 turn ops 伴随性」。
- 闭合标准：§3.7 fence 语义与 `projection_snapshot_fence.go:223-236` + `event_publisher.go:764-817/:852-945/:1769-1818` 的实际行为逐点一致（含 decide 维度）；§7.1.4 第三/四条断言按可构造的失败判据写明测试链路与 patch 形态；「真实边界」与防御性定位的表述不再与当前代码可达性矛盾。

## 6. 复审处置与剩余门

### R4 三条意见闭合判定（本轮独立核验，未采信 v4 自述）

| 意见 | 级别 | 本轮判定 |
| --- | --- | --- |
| F-17 §3.7 W1 fence 语义归因与代码不符 | 阻塞 | **按其闭合标准部分达成后残留**：v4 已改正的三点（held 连接级累积 `:1674-1687`、older page 不清除 held、patchTurnIDs 空集恒 false 机制）本轮亲核全部成立；但重写文本仍遗漏 decide 路由维度，「描述与实际行为一致」未完全达成 → 升级为新阻塞 F-20（同根因更深一层，非回归）。 |
| F-18 capability 双门偏离无收口路径 | 建议 | **闭合**。§2.2 已知偏离行补收口指针 + §6 独立段落「归属 S-3、协议冻结时二选一（descriptor 写入并按文档条件 echo，或修订协议文档为单门语义）」+ §10.10；四处登记一致；「收口前单门沿用 + 新客户端接入前必须收口」的过渡语义明确。双门条款原文与单门实现本轮亲核成立。 |
| F-19 §1.1 两处数字口径标注残留 | 建议 | **闭合**。transcript 101,520,809 B（96.8 MiB）stat 精确命中，全文「约 97MiB」统一（grep 无残留「97MB」裸用）；6,347,762 B 补注 python 口径 + Go 验收口径 6,336,926 B，两口径均 >4MiB 临界判定不受影响。 |
| OD-6 定案回填质量 | — | **回填完整、定案正确，条件 1 未完全达成**（§3/F-20）。§4 决策表、§5 Gate A、§6 S-1、§10.8 状态同步一致。 |
| §3.2.2 存储选型定案维持 | — | **本轮独立复核维持**（§4）。 |

### R3 六条 + R2 遗留 + F-3 残余（经 R4 核验闭合，本轮关键锚点抽核维持）

F-11（scalar 缺口锚点 4 组本轮全部亲核命中）、F-12（五项选型约束亲核）、F-13（单门事实 + descriptor 零写入 grep 复核）、F-14（`handlers_projection.go:122` vs `projection_window_handler.go:102-106` 条件差异成立）、F-15（口径声明 + 三口径数字复算一致）、F-16（8 次投递逐条日志复现）、F-10（iOS 8 项脏清单实测一致 + S-0 重生成语义）、F-3 残余（§3.2.3 组合路径）——**全部维持闭合**。R1 九条维持 R3/R4 闭合判定（本轮抽核 bridgeId/epoch、never-split、prefix 整读、Codex 三项关键锚点均命中）。

### v4 修订回归检查

无悬空引用与能力范围变化：「待定案/推荐」仅存于 §0 与 §3.7 标题的历史性叙述（「措辞从…改为定案」「v3 曾列为推荐」）；OD-6/W1 定案交叉引用（42 处）与 F-17/18/19 引用（17 处）方向一致；§6 S-3 前置「OD-2；协议冻结」与 F-18 收口段落的归属呼应成立（§10.10「行内登记」实为 §6 表下独立段落，语义可接受）；open_gates 与 §5 门表一致。v4 §2.1 iOS 行仍记 `1e895ea9`（实测 `dfb77ed9`，docs-only 漂移）——F-10「时点快照 + S-0 重生成」处置语义已覆盖，不另立意见。

### 实施前仍未满足的门

- 证据门：E-3（window 未生效根因，含 descriptor 维度）、E-4（wire 级验证）、E-5（iOS 阶段耗时）、E-6（materialization 正确性）、E-7（preview 不污染 writer）、E-10（scalar state 正确性，按 W1 定案路线 + F-20 修订后的断言构造）。
- 决定门：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4 开工，OD-2 阻塞 S-3 协议冻结）。OD-6 已定案 W1（本轮复核维持），不列为未决门；**F-20 修订完成前，OD-6 冻结内容的准确性未达成，S-1 开工前置应包含 F-20 修订**。
- 授权门：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

### implementation_readiness

- 修订 F-20（§3.7 fence 语义补 decide 维度 + 真实边界改写 + §7.1.4 两条断言补构造前提；预计一处小节重写 + 两行断言改写，不动 W1 定案与 scalar 分支设计）后：S-0 可立即开工；S-1 开工门（E-1/E-2 verified + OD-6 定案）满足，完成宣告仍需 E-3/E-4/E-10；S-2 需存储格式冻结 + 测试计划；S-3 需 OD-2（含 F-18 收口项）；S-4 需 OD-1/OD-3；S-6 需 owner 授权。本报告不构成实施授权。

## 7. 评审过程记录

- 评审员模式 fresh：先独立形成风险清单（哈希记账、三仓来源门、§3.7 fence 机制链、W1/W2/W3 机制、存储选型约束、iOS 消费链与双源、样本数字复测、门控一致性、§10 三项评审重点、audit-plan 形状专项），完成全部亲核后才读 R4 报告作回归核对；R1/R2/R2-meta/R3 未读，其结论经 R4 与方案 §0 历史表转述核对，未作为任何结论来源。
- 身份备注见报告头：简报「现为 v3」为滞后叙述，派发哈希与实测一致，评审对象 = v4。
- 与历轮的分歧记录：本轮确认 R4 三条意见闭合、OD-6 定案 W1 与存储选型定案正确；新阻塞 F-20 属 v4 新改内容（§3.7 重写段）的机制性偏差，**R4 的 F-17 证据与修订指引本身亦未覆盖 decide 维度**——该维度是本轮新增核验面（fence decide 路由 + 两条发布链的 Execution-only 存在性），非修订回归、非历轮意见翻案。
- 本轮唯一写入文件：本报告。方案、R4 及更早报告、三仓源码、iOS 8 个未提交文件一律只读未触碰。
- 使用的契约：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（外部内容形状专项实际运行：checkpoint 解析尾部 17 turns/最大 turn 复算、日志 8 次投递逐条 grep、transcript/checkpoint stat、协议文档条款原文核对、go.mod/project.yml/import_guard 约束 grep）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: ef386fdff0d154b6df170dc99177a26bf23f871a3254679ea1a94b6caed5e6fc
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r5.md
scope: full
blockers: 1
advisories: 0
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, OD-1, OD-2, OD-3, S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
