# codex-web 源码对齐审计（source-parity audit）

- 日期：2026-08-25
- 性质：实施后追溯审计。本文只记录事实与处置裁决，不改代码；执行由 owner 下发的监工指令驱动。
- 对照物：
  - 设计合同：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md)（§0.3 数据面红线、§2.3 移植纪律、§3.0 证据元组、§3.4 修 bug 纪律、§9 旁路禁区、§23 防偏航）
  - 官方源码：`/Users/jacklee/Projects/codex/codex-rs` @ pin `536f86e5cc9ec1ff38457d099bf320b9d08eeeba`
- 触发：2026-08-25 owner 质疑 user_input 收口修复（89954bc）堆叠乐观 publish / 白名单 / status 兜底三层补丁；核实确认其违背官方 per-connection resolution 模式（官方 `ServerRequestResolved` 广播给全部订阅连接、每客户端视角各自收口，无乐观收口路径）后，扩展为全仓"造轮子"排查。
- 范围：`agent/codex-web/` 全部非测试代码（约 7.5k 行）+ `go-bridge/` codex-web 相关路径。iOS 仓不直接接触 app-server 语义，仅列抽查项（§6）。
- 快照说明：A1/B1 涉及的 `interactions.go` per-epoch 重构在审计时为**工作区未提交状态**；本文行号以该状态为准。

## 0. 总体结论

结构性纪律执行良好：空目录原则、provenance（对 `agent/codex`/rollout/file-relay 违禁 import = 0）、拓扑 Gate 均落实；`rpc.go` 文件头有 §2.3 要求的"移植母本"声明，是正面样本。go-bridge 中 14 处 fallback/legacy 气味逐条判定后绝大多数为合法翻译层职责或刻意不做兜底。

语义对齐存在三类问题（分类法见 §1）：**A 类漂移 3 项、B 类无豁免卡发明 5 项、C 类红线违规 2 项**。共同根因不是"不读官方源码"，而是两类区域未区分对待：官方有算法的区域抄了但无移植声明、漂移无追溯；官方**没有**算法的区域（双泵、重连、跨客户端 turn 身份）必然发明，但发明未被要求写不变量与失败模式，成为补丁农场（user_input 三层补丁事故即发生在 B 类区域、却按 A 类修法打补丁）。此外，exec-plan 105 项 done/proven 体系只验证"测试通过"，不含"未造轮子"维度——这是验收漏斗的洞（§5）。

## 1. 分类法（处置语义）

| 类 | 定义 | 处置 |
|---|---|---|
| A | 官方有现成算法/模式，实现漂移或未按 §2.3 记录移植关系 | 回归官方算法 + 注释锚点 |
| B | 官方无对应机制（架构差异必需的发明） | 补「架构豁免卡」：为什么官方没有 / 我们的不变量 / 失败模式 / 回归测试。本文档即豁免卡登记簿 |
| C | 违反设计红线的本地补造 | owner 裁决：fail closed 或设计修订 |

## 2. 官方参照系（pin `536f86e5` 客户端机制地图）

审计期间构建，作为后续所有对齐工作的参照索引。

| 机制 | 位置 | 行为摘要 |
|---|---|---|
| request/response correlation | `app-server-client/src/lib.rs:345-354,439-459`；`remote.rs:216-259,323-332` | pending map 按 id 唤醒；重复 id 发送前拒绝 |
| server request 注册/应答 | `lib.rs:516-569`；`remote.rs:272-303` | `resolve/reject_server_request` 写回；未知 server request 自动回 `-32601`（`remote.rs:345-392`） |
| 有序事件队列 | `lib.rs:333-337` | 本地消费 unbounded（官方注释明示防死锁动机） |
| initialize 握手期事件缓冲 | `remote.rs:798-941` | 握手中通知先入 `pending_events: VecDeque`，`next_event` 先排空 |
| 错误分层 / worker 退出扇出 | `lib.rs:116-169,467-487`；`remote.rs:466-474` | `Transport/Server/Deserialize` 三层；退出错误扇出全部 waiter |
| shutdown | `lib.rs:578-611`；`remote.rs:602-628` | 分级超时（remote 5s），超时 abort 兜底 |
| Lag 处理 | `lib.rs:104-114`；`tui/src/app/app_server_events.rs:60-68` | 仅 warn + re-read 刷新，不重放 |
| 通知线程路由 / server request 路由 | `app_server_events.rs:228-283,289-429` | 未知线程丢弃；启动期缓冲；`pending_app_server_requests` 去重；不支持变体立即 reject |
| **steer/interrupt 失配重同步** | `tui/src/app.rs:643-703` | 解析服务器失配错误中的真实 active turn id（steer 前缀 ``expected active turn id `X` but found `Y` ``、interrupt 前缀 `expected active turn id X but found Y`），重同步本地缓存后**重试一次**；另有 `no active turn to steer` Missing 分支 |
| PendingAppServerRequests 注册表 | `tui/src/app/app_server_requests.rs:74-360` | 分类型 HashMap；`note_server_request` 登记；`take_resolution` 本地决策→typed response 并移除；`resolve_notification` 收 resolved 回声清除+dismiss（`app_server_events.rs:118-142`）；会话重置 `clear()` |
| completed 权威覆盖 delta | `tui/src/chatwidget/streaming.rs:127-143,316-330` | completed 文本权威，delta 拥塞丢失不截断 |
| 终态消息去重 | `chatwidget/protocol.rs:262-307` | `last_completed_agent_message` 守卫 TurnCompleted/ItemCompleted 双渲染 |
| resume 冷灌 | `session_lifecycle.rs:965-1137`；`app_server_session/history.rs:198` | exclude_turns 协商 + 游标分页 + 重放 |

**官方明确没有的机制**（B 类的边界依据）：断线重连（`Disconnected`→`FatalExitRequest` 直接退出）；事件重放/续传游标；per-thread 传输级重排缓冲（顺序完全依赖单一有序流 + 服务器按序发送）。

## 3. 逐项发现与处置裁决

### 3.1 A 类：官方有算法，实现漂移

#### A1 InteractionRegistry 无移植声明 + history 无界
- 位置：`agent/codex-web/interactions.go:63-84`（registry 结构）、`126-138`（MarkResolved）、`418-487`（resolvedEvents，工作区版本）
- 现状：pending/history/resolvedByRequest 三 map；per-epoch `notified` 去重；pending 线性扫描按 (thread, requestId) 归属。`resolvedByRequest` 有 1024 界，**`history` map 只增不减（进程生命周期无界）**。
- 官方锚点：`tui/src/app/app_server_requests.rs:74-360`（PendingAppServerRequests：分类型 HashMap、take_resolution、resolve_notification、会话重置 clear()）。
- 定性：功能同构的自造变体，无 §2.3 移植关系声明。89954bc 三层补丁事故正发生在此；工作区 per-epoch 重构方向已对齐官方广播语义，但结构本身仍是"无声明变体"。
- 处置：
  1. 文件头补移植母本声明 + 与官方的差异清单（官方单连接单视角 ↔ 本仓双泵两视角，其余语义对齐）；
  2. `history` map 有界化（与 `resolvedByRequest` 同策略：1024 全清）；
  3. 对照官方 `clear()` 语义审查会话/epoch 重置路径是否等价覆盖；
  4. 不变量回归测试：每 epoch 恰发一次收口事件；第二泵晚到仍可经 `resolvedByRequest` 归属；`DropEpoch` 后旧 epoch 不再产出。

#### A2 permission 乐观收口未清算（对称性残留）
- 位置：`go-bridge/handlers.go` `handleResolvePermission`（publish `permission_resolved` 于 RespondPermission 成功后，约 :4084-4098）。
- 现状：立即乐观 publish，注释理由 = 等待 host mux 帧导致 SSV2 重映射卡 UI（Task Review / message dock 不消失）。
- 官方锚点：TUI 收口唯一路径 = `ServerRequestResolved` 通知（`app_server_events.rs:118-142`），发送应答后不本地关闭。
- 定性：89954bc 曾以"与 permission 对称"为 user_input 乐观 publish 辩护；随后 user_input 侧按官方语义删除（工作区），**permission 侧同款未清算**——"官方 resolved 是唯一收口真相"原则执行不彻底。
- 处置（严格按 §3.4，禁止直接删了事）：
  1. 先复现"移除乐观 publish 后 UI 卡住"的原始现象；
  2. 在 SSV2 重映射 / 被动泵投递链找**第一处分歧**并修复（大概率与 user_input 案例同源：per-epoch `resolvedEvents` 对 permission kind 已产出 `EventPermissionResolved`，双泵投递是否覆盖该场景）；
  3. 现象消除后删除乐观 publish 及任何派生 status 逻辑；
  4. 若复现证明官方路径存在无法覆盖的产品缺口，则写成 B 类豁免卡登记，不允许无声明保留。
- 验收：允许/拒绝后卡片立即收口不回归；收口事件源断言 = `serverRequest/resolved` 驱动；并入 p5-interactions 真机回归。

#### A3 steer/interrupt 失配未采用官方 resync-retry
- 位置：`agent/codex-web/events.go:481-499`（currentTurnForControl，注释明确"原样透传，不重试伪装"）、`501-526`（CancelTurnForThread）。
- 官方锚点：`tui/src/app.rs:643-703`（steer/interrupt 失配错误解析 + 重同步 + 重试一次，含 Missing 分支）。
- 定性：官方**有**算法（错误驱动重同步），本仓选择了相反行为（fail-closed 不重试）且未声明理由——属于"官方已有算法时另造更差等价物"。
- 处置：
  1. 移植官方 mismatch 解析（两种消息前缀）+ 重试一次 + Missing 分支；
  2. 三源观测（liveCodec > 本端 start 返回 > 冷基线扫描）保留为首选身份来源，失配 resync 作为权威纠正；
  3. 冷基线扫描（`inProgressTurnFromColdBaseline`）降级为最后手段，剩余部分并入 B2 豁免卡。
- 验收：两种失配消息解析与重试的单测；真机"iOS 停止 Mac 发起的 turn"（§13.3 第 3/6 行相关）。

### 3.2 B 类：架构必需的发明，缺豁免卡

以下各项**不要求回迁官方算法**（官方无对应机制或官方模型不适用双泵架构），处置 = 在本文档登记豁免卡 + 补不变量回归测试。修复实施时逐项填全：为什么官方没有 / 不变量 / 失败模式 / 回归测试。

#### B1 双泵 per-epoch resolved fan-out（工作区已实现）
- 位置：`interactions.go` resolvedEvents（每泵按 epoch 各发一次收口事件）。
- 豁免理由：产品架构 = 单逻辑客户端、两物理连接（主泵/观察泵）对同一共享 daemon；官方"每客户端视角各自收口"映射为"每泵各发一份"，kernel reducer 按 interactionId 幂等 upsert。
- 不变量：每 epoch 恰一次；reducer 幂等双投无害；`resolvedByRequest` 有界归属第二泵；泵断线期间 missed resolved 由另一泵 + kernel 投影兜底，重连冷校准遵循 §8.3。
- 待补：不变量回归测试清单 + 本卡引用写入代码注释。

#### B2 currentTurnForControl 三源合流 + 冷基线兜底（A3 处置后剩余）
- 位置：`events.go:481-546`。
- 豁免理由：官方 TUI 只跟踪自己 start 的 turn id，从不 steer/interrupt 他人 turn；本仓产品需要控制观察 turn（iOS 停止 Mac 发起的 turn）。
- 不变量：liveCodec 观测为权威；本端 start 返回值仅在无观测时使用（毫秒窗口）；官方 -32600 原样透传（A3 移植后升级为 resync-retry）。
- 失败模式：订阅前已开始的 turn 无 turn/started（官方不重放）→ 冷基线 thread/read inProgress 兜底；仍找不到 = fail closed。
- **登记状态（2026-08-25 批次 2）**：A3 resync-retry 已回迁（解析器锚点 app.rs:643-692、重试语义 thread_routing.rs:604-627/683-727，代码注释已引用本卡与官方锚点）；三源顺序不变（liveCodec > 本端 start 返回 > 冷基线最后手段）。回归测试 `agent/codex-web/control_race_test.go`。

#### B3 重连循环 + isConnectionLoss 结构化分类
- 位置：`events.go:196-217`（reconnectLoop 退避）、`session.go:284-306`（`isConnectionLoss`）。
- 豁免理由：官方无重连（断线即退出），长驻 bridge 必须自愈。
- 登记的不变量与参数（2026-08-25 批次 4）：
  - 退避参数：`reconnectLoop` 2s 起、×2 递增、上限 60s；重连成功即六步就绪 + 中央泵重启；
  - 冷校准顺序：缺口由上层 §8.3 冷校准（thread/read includeTurns）覆盖，不重放、不合成；
  - 连接死亡判定：transport 层 `TransportConnectionError`（含官方 WS close code）类型化优先 → syscall/net 结构化 → 文案匹配仅兜底且命中打 Warn 诊断标记（标记"未被类型化的错误源，需补归类"）。
- **登记状态（2026-08-25 批次 4）**：结构化分类已实施（transport.go asTransportConnectionError + session.go 分类优先级）；分类单测 `sessions_test.go TestIsConnectionLossStructuredClassificationFirst`；既有死 socket 形状回归保持。

#### B4 retryByThread willRetry 连续计数
- 位置：`codec.go:29-70,111-121,442-472`。
- 现状：对官方 error 帧的 willRetry 标志做连续计数、delta 到达清零——发明语义（官方无 attempt 计数）。
- **豁免卡（2026-08-25 批次 4 登记）**：
  - 为什么官方没有：官方 TUI 对 will_retry=true 的呈现 = `on_stream_error` 瞬态行（`tui/src/chatwidget/protocol.rs:127-133`；`app-server-protocol/src/protocol/v2/notification.rs:54-55`"transient…will not interrupt a turn"），每帧独立渲染、无计数；will_retry=false 走 `handle_non_retry_error` 终态。
  - 我们的不变量：willRetry=true → `EventRetryStatus`（不落 turn 终态、不进 IsDurableMilestone/syncV2 deny-list——与官方「不中断 turn」对齐）；连续计数 `RetryAttempt` 仅作 iOS「重试中（第 N 次）」附加 UX 信号，任何 delta / turn/completed 重置；willRetry=false → `EventError` 官方原文（对齐官方 non-retry 终态）。
  - 失败模式：计数漂移（帧丢失/重连清零）只影响显示的 attempt 数字，不影响任何终态/投影事实。
  - 回归测试：codec_test retry 计数与重置既有用例。

#### B5 codec turn/completed 缺 turn.id 的 ActiveTurn 归属
- 位置：`codec.go:157-163`。
- 处置：核对 pin 源码 `protocol/v2/turn.rs` CompletedParams 是否必有 turn id。若必有 → 改为记诊断 + 丢弃（不静默归属，掩盖 wire 契约异常）；若存在合法缺失场景 → 登记豁免卡。

### 3.3 C 类：红线违规

#### C1 context_usage 直接解析 rollout JSONL（owner 已裁决：保留并加固 + 设计修订）
- 位置：`agent/codex-web/context_usage.go:76-156`（`GetSessionContextUsage` 经官方 `thread/read` 取 path 后，`readPersistedContextUsage` tail 读 8MB 解析 `event_msg/token_count` 记录）。
- 违反：§0.3（"用量"在官方 API 唯一数据面清单内，禁止解析 `~/.codex/sessions/**/*.jsonl` 补造事实）、§9 旁路禁区（rollout parser）；且 rollout 内部格式**无稳定性契约**，当前形状不吻合时静默回退 cache，属 §1 要禁止的"静默 fallback"。
- 缓解事实：官方确无冷用量读取 RPC（live 仅有 `thread/tokenUsage/updated` 通知；官方冷用量由 server 侧 resume 时自行解析 rollout）；path 来源是官方 API；功能已随 6f765fc 交付且 owner 已在真机确认上下文占用显示正常。
- **owner 裁决（2026-08-25）：保留并加固 + 设计修订。**处置步骤：
  1. contract fixture：从 pin 源码 rollout 记录结构 + 真实脱敏样本冻结 `token_count` 记录形状；形状不吻合 → 弃用文件路径、打诊断（不静默）；
  2. 版本门控：initialize 记录的 server/CLI 版本与已验证版本族不匹配 → 不走文件路径；
  3. 可见性：诊断/descriptor 标注 `usage-source: rollout-tail-experimental`；解析失败记 warn；
  4. 起草 §0.3 修订案：将该路径登记为"记录在案的豁免"（path 来自官方 thread/read、只读、待官方 RPC 后退役），交 owner 批准后回写设计文档。
- 验收：fixture 单测 + 版本门控单测 + 设计修订段落落档。

#### C2 history 合成终态与合成身份（低危）
- 位置：`agent/codex-web/history.go` mapHistoryItem plan 分支（约 :294-320，plan 卡无条件合成 `status:"completed"`）、`GetRichSessionHistory`（约 :484-507，system note 合成 ID `turnID+":"+note`、错误文案前缀"官方 turn 失败："）。
- 定性：plan 终态属 §1"私自生成 status"字面违反；合成 ID/文案前缀是已注释的 bridge 兼容面，危害低。
- 处置：plan 卡状态改为从官方 `turn.status` 推导（turn completed → completed，否则 unknown），不再无条件合成；合成 ID 保留但补碰撞不可能性说明（单 turn 内 note 唯一）；文案前缀保留（产品文案非事实编造）。
- 验收：plan 状态映射单测。

### 3.4 通过项（记录审计覆盖面）

- `rpc.go`：文件头声明移植母本 `app-server-client/src/lib.rs`，correlation/pending/分发与官方同构（自加 60s requestTimeout 属 adapter 必要）——§2.3 正面样本。
- `lifecycle.go`（Probe 六步就绪/daemon 复用）、`transport*.go`、`codexweb.go`、`diagnostics.go`、`wire_descriptor.go`：无状态机纯翻译，锚点引用充分。
- `sessions.go`/`turn.go`/`userinput.go`/`models.go`/`permission_modes.go`/`catalog_thread_list.go`：请求应答映射 + 官方枚举归一；userinput skip（空 answers）有官方锚点（`bespoke_event_handling.rs`）。
- `events.go:393-395`：turn/start 不发 provider 字段有官方锚点论证。
- go-bridge 14 处 fallback/legacy 气味逐条判定：11 处合法翻译或刻意不兜底（含 `handlers.go:4270` 的"不做本地乐观收口"——即 user_input 修复后的正确注释）；2 处为 Claude 后端路径（不属 codex-web）；1 处 interrupt 注册表乐观 `Ok:true`（`handlers.go:2889`，已注释声明的兼容妥协，列观察项不改）。
- 空目录原则 / provenance / 违禁 import：0 违规。
- §7.1 事件映射红线抽检：turn/started 唯一开始真相、completed 权威、`(threadId,turnId,itemId)` reducer 身份——`codec.go` 尾部封口策略有 wire 事实依据。

## 4. 处置计划（批次与依赖）

| 批次 | 项 | 说明 |
|---|---|---|
| 1 | A2、A1、C2、B5 | 收口对称性与小修；A1 以工作区 per-epoch 重构为基础 |
| 2 | A3 + B2 豁免卡 | 官方 resync-retry 回迁，降低自造推断依赖 |
| 3 | C1 | 按 owner 裁决加固 + 设计修订案 |
| 4 | B3、B4 | 豁免卡 + isConnectionLoss 结构化分类 |
| 5 | §5 流程项 | 验收维度补洞 |
| 6 | §6 iOS 抽查 | 轻量，可与批次并行 |

每批完成 = 定向测试通过 + 完成情况记录落档（沿用 `完成情况` 文档惯例）+ 代码注释带锚点/豁免卡编号。真机相关验收（p5 interactions 等）保留 owner 执行，不得代填。

## 5. 流程防再犯（验收维度补洞）

exec-plan 的 proven 体系只证明"测试通过"，不证明"未造轮子"。补三个便宜机制：

1. **任务定义/完成报告增加必填字段「上游锚点」**：codex-rs file:line，或豁免卡编号（本文档 §3.2 条目）。缺字段 = 任务不可标 done。**已实施（2026-08-25 批次 5）**：exec-plan `references/state-format.md` verification 增加 `upstream_anchor` 字段 + 证明规则 4（移植/对齐类缺锚点 = missing proof → downgrade）；`references/completion-report-template.md` 增加 §2.1 Upstream Anchors 必填节。
2. **§3.4 机械化**：supervisor/review 对每个 bug 修复固定问句——"这个修复与官方调用链的第一处分歧在哪里？"修复报告必须含锚点；无锚点的修复默认退回。**已实施（2026-08-25 批次 5）**：supervise `references/drift-criteria.md` v4 新增判据 9「上游分歧锚点」（软判据，移植/对齐类任务；补锚点/豁免卡，无法给出 → 驳回）。
3. **关键词门禁（人工执行，不上 CI）**：review 时对 `agent/codex-web` 新增的 兜底/乐观/fallback/heuristic/猜 关键词，要求附带锚点或豁免卡引用。
4. 本文档作为**豁免卡登记簿**长期维护：新的架构性发明必须先登记（含不变量与失败模式）再实施。**登记状态（2026-08-25）**：B1–B5 全部登记完毕（B1 双泵 fan-out / B2 三源合流 / B3 重连+结构化分类 / B4 RetryAttempt 计数 / B5 由 B5 核实改丢弃处理）；此后新增发明按本节流程先登记再实施。

## 6. iOS 仓抽查项（轻量）

iOS 仓（`/Users/jacklee/Projects/cordcode-ios-codex-web-backend`，分支 `codex/codex-web-backend-ios`）消费 bridge-v1，不直接接触 app-server 语义，无需全面审计。仅抽查：

1. 模型目录归一化（70ce93f：provider-qualified id ↔ 裸 id 匹配）——确认 iOS 侧没有再发明目录语义或本地推导 provider 归属；
2. 会话控制面取值（27d9b56：get_session 应用模型/provider/effort 真值）——确认 iOS 只消费 bridge 事实、不自行推导；
3. 抽查结论回写本文档附录，不单开审计文档。

## 7. 审计方法与证据

- 主文档纪律条款（§2.3/§3.0/§3.4）逐条对照实现侧可追溯痕迹（锚点注释、移植声明）；
- 构建 pin `536f86e5` 客户端机制地图（§2），含"官方明确没有的机制"边界；
- `agent/codex-web` 逐文件排查手搓状态机（自定义 registry/map/去重/推导）；
- go-bridge 气味关键词（fallback/兜底/legacy/乐观/启发式）逐条上下文判定；
- 关键发现均经第二遍源码核读（context_usage.go、events.go currentTurnForControl、handlers.go handleResolvePermission、interactions.go MarkResolved、app.rs resync helpers、thread_lifecycle.rs 广播、app_server_events.rs 收口路径）。
