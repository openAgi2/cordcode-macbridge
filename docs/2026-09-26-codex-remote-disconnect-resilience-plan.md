# codex-remote 断线韧性与恢复专项方案

- 日期：2026-09-26
- 状态：v1.4（r4 评审 REVISION_REQUIRED（4 阻塞 + 2 建议）后的修订轮；未实施）
- 本轮改动范围（v1.4）：处置 r4 全部意见（报告：`docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r4.md`，处置表见 §7.1）。r4 以新反证使 r3 的 APPROVED 进入**通过后纠错**（plan-contract：改变验收成立性的错误使相关通过依据暂停有效；r1~r3 的锚点核验仍作历史证据，但 admission/归属/half-open 状态机/wire 样本四条跨组件约束未被旧轮覆盖）。四条阻塞：F-R4-1 → §3.2 事务域重写为 READY-safe 专用对账事务（kernel 新增 reconcile admission/abort API 设计）；F-R4-2 → §3.3 缺口归属重写为 stream 级未知归属 + 有界 fan-out + 游标推进（一并闭合被 r4 升级为阻塞的 R2-A1）；F-R4-3 → §3.7 重写为 closed/open/half-open 状态机、删除不可观测的「transport 接受」伪事实（一并闭合被升级的 R2-A3，并按 R2-A2 补 S-7 游标依赖）；F-R4-4 → E-12 降级为 pending wire 证据门、门住 S-3/S-7 实施，真实捕获清单写成 owner 授权后的实施前置 fixture 计划（§4）。两条建议：R4-A1 → 删除 S-1b「尊重 retryAfterMillis」承诺（选项 b，理由见 §7.1）；R4-A2 → §2.1 标注为设计证据历史快照 + 新增实施来源门模板。R2-A4 两处行号精度顺手修（§1/§3.1）。联动更新 §4 门控、§5 切片表、§6 风险与验收。稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3）不变；**只设计，不实施**。
- v1.3 改动范围：仅来源清单纠错，设计内容零改动（勘误背景见 §8）。r2 的 4 条建议级意见（R2-A1~A4）当时按 plan-contract「通过后纠错」未夹带进勘误——其中 R2-A1/R2-A3 已被 r4 升级为阻塞（F-R4-2/F-R4-3）并在本轮闭合，R2-A2 随 F-R4-3 一并补依赖，R2-A4 行号精度本轮顺手修。
- v1.2 改动范围：处置 r1 全部意见 F-1~F-6（2 阻塞 + 4 建议，全部采纳，处置表见 §7）。另含设计师复核 F-1/F-2 证据链时亲核发现的**相邻缺口**：MacBridge chunk ack 不携带 `SegmentID`（官方 `protocol.rs:114-119` 文档要求携带）——该缺口使 S-3「缺口即真实丢失」存在已证盲区，故并入 S-3 修复并新增 E-12；此项超出 r1 意见范围，已在 §7 处置表单列，**r2 评审已确认采纳**。F-2 对策升主链：新增 S-7 切片（ack 熔断有界化）。**只设计，不实施**；owner 已授权写方案，未授权任何代码改动。
- v1.1 增量：整合官方 transport 层深挖结论——host 未 ack 重放缓冲与反压（新风险 R-6→§6 风险 5）、官方 caller 侧重连模式（TUI）、入站 seq 去重语义、token 刷新提前量官方先例、官方测试不变量引用。
- v1.0→v1.1 间「R-6」为风险草稿编号，未进入 §1 需求清单，正式编号为 §6 风险 5；本版不再使用 R-6 字样。
- 前置方案：`docs/2026-08-26-codex-remote-backend-implementation-plan.md`（owner 撰写，r2 评审通过；其 §6.2/Gate P0 的韧性相关要求本方案逐条对账）。

## 1. 摘要与范围

### 现状问题（owner 原话）

> 「codex-remote 对网络要求太高，网络有一点波动，就是无法加载会话投影。」

复盘证据（think.md，两仓）：
- macbridge `think.md:2-6`（2026-09-26）：Desktop 26.924 升级打断 codex-remote，iPhone 报「加载失败：codex-remote: stream closed」+「已配对，等待 ChatGPT Desktop」横幅；`watchBinding` 把所有恢复错误折叠成笼统文案。
- cordcode-ios `think.md:432`（2026-09-20）：Mac 端 codex-remote 自 11:36 起病态（thread/list 每轮 12s 超时、stream idle/closed、pairing 流反复重连），**至 14:00 未恢复**——驱动长时间不自愈的实测。（行号按方案记录来源 bd46169 框架；当前 iOS main fe421cdc 下同内容位于 :477——r2 报告 R2-A4② 亲核的双框架注记，v1.4 顺手补录。）
- macbridge `think.md:421`（2026-09-14）：error 通知风暴期间 live catalog attach failed 318 次，iOS 间歇卡「执行中」。

### 目标行为（R）

- **R-1** 网络瞬断（秒级~分钟级）期间与恢复后，iOS 打开 codex-remote 会话不再出现不可重试的硬错误「无法加载会话投影」；瞬态失败进入自动重试，驱动恢复后自动收敛。
- **R-2** 断线窗口内终结的 turn，重连后自动收口（不再永久停在 running、不依赖用户手动重开会话触发冷校准）。
- **R-3** 长连接期间 controller token 到期前主动续期，消除「到期→连接死亡→走完整重连」的额外不可用窗口。
- **R-4** 入站事件缺口可检测（消息级 per-stream seq 单调性；chunk 信封共享所属消息的 seq_id，见 §2.2），缺口触发受影响会话的权威对账，不静默丢失。
- **R-5** driver 断线/恢复状态在桥内可区分、可查询（真实错误类别不再折叠为单一文案），为后续 iOS 推送立项预留钩子。

### 非目标

- **不做 controller 腿 cursor 重放**：官方协议不提供（E-1，源码 + owner attempt-008 双证）。维持 owner 方案 Gate P0 的 fail-closed 裁决（plan:588-599），不广告、不伪造。
- **不做 `backend_status_changed` 跨仓推送**：macbridge `think.md:45`（2026-09-26）已裁「待立项（协议面）……另立」。本方案只做 driver 侧状态暴露（S-6）。
- **不做 ACK 未确认发送缓冲/重传**：官方未提供 controller 侧重传语义（E-1/E-2）；出站失败由现有重连+重订阅路径兜底，重传缓冲收益低、复杂度高。
- **不处理官方 iOS controller 共存 / HTTP 409**：owner 方案任务 7 遗留缺口（plan:597），独立问题。
- 不修改官方 App / relay / 协议；不新增第二真相或第二 writer（SSV2 护栏）。

### 验收标准（行为级）

1. 受控断线（断 relay 链路 10–60s 后恢复）期间冷开会话：iOS 显示 loading/可重试态，恢复后 ≤ 一轮重试内自动加载成功，全程无硬「无法加载」。
2. 断线窗口内完成的 turn：重连完成后 ≤ 一轮对账内收口为完成态（真机时间线可见，无需重开会话）。
3. pairing revoked（401/403）仍为硬错误（提示重新配对语义），不被自动重试掩盖。
4. 入站 seq 缺口在桥日志可查，并触发对账；无缺口时零开销。

## 2. 来源与现状调查

### 2.1 来源表（**设计证据历史快照**，v1.3 按 P0 来源门补全：工作树枚举 + 分支族解析 + 任务预期分支 + 预期产品特性；勘误背景见 §8）

> **R4-A2 处置（v1.4）**：本节记录方案各版形成/评审核验时的来源身份，属**设计证据历史快照**——表中哈希反映的是对应轮次的核验时点，不代表当前工作树状态。实施前必须按下方「实施来源门模板」在三个门点（读源码分析前/首次改文件前/构建安装前）**现场重跑**，禁止复制本节任何哈希作为实施来源清单。

**工作树枚举**（`git worktree list` 本轮勘误亲跑，2026-09-26）：
- cordcode-macbridge 共 3 树：`/Users/jacklee/Projects/cordcode-macbridge`（main @ 07721783）、`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`（feat/ios-native-message-timeline @ 07721783，**本方案所在树**）、`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（detached @ b2b25235）。
- cordcode-ios 共 3 树：`/Users/jacklee/Projects/cordcode-ios`（main @ fe421cdc）、`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`（feat/ios-native-message-timeline @ 8eb262da，**同分支族配套树**）、`/Users/jacklee/Projects/cordcode-ios-plan-approval`（detached @ a336b68b）。

**分支族解析**：本方案所在 Mac 工作树分支为 feat/ios-native-message-timeline；owner 裁决（任务简报）：方案文档在 main 撰写、已同步到当前分支，评审与修订均在当前工作树进行，无需也不得切换到 main 工作树操作。同族配套 iOS 工作树为 `cordcode-ios-native-message-timeline`；但任务简报 ③ **显式指定** iOS 锚点核验来源为 main 工作树（fe421cdc），本方案遵循该指定。配套树身份为移动目标：元审核（r2-meta）采样时为 f4a81ee4，本轮勘误采样时已前进至 8eb262da（`git merge-base --is-ancestor` 亲核 f4a81ee4 为其祖先；区间提交为 timeline/steady-release 功能与 docs(plan) 提交）。**锚点有效性（本轮勘误亲核）**：方案引用的四个 iOS 锚点文件（ProjectionStore.swift / CCCodeBridgeModels.swift / ChatViewModel.swift / CCCodeBridgeTransport.swift）在 bd46169（方案 v1 记录时点）/ fe421cdc（r1/r2 评审核验来源）/ f4a81ee4（元审核采样）/ 8eb262da（本轮勘误采样）全部候选 ref 下 `git diff --stat` 均为空（字节级零差异）——iOS 锚点结论与工作树选择无关，r2 APPROVED 的锚点依据在任意候选来源下成立。

| 仓库 | 工作树路径 | 分支 | 提交 | 未提交状态 | 任务预期分支 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（本方案所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 0772178358fb582474eb5aada5f50b7d8608053e | 方案文档本身为未提交修订（HEAD 提交内为 v1.1，评审循环正常形态）；另有 5 个无关未跟踪 docs（2026-09-24-zcode-plan-agents-{design,acceptance}.md 与 r1/r2/r2-meta 三份评审报告），均不属本方案范围、不得改动 | feat/ios-native-message-timeline（owner 裁决：main 撰写、同步至当前分支、评审与修订均在当前树） | 纯设计文档，无构建/安装动作——预期行为 = §1 R-1~R-5；无产物级后端类型/能力核对适用（N/A 理由：无构建场景） |
| cordcode-macbridge（代码锚点核验来源） | 同上工作树（与 main @ 07721783 同提交） | — | 07721783 | — | 同上 | 同上（N/A） |
| cordcode-ios（iOS 锚点核验来源） | /Users/jacklee/Projects/cordcode-ios | main | fe421cdc136930e3729b6b3d5c4479d5ecb90df4 | 干净（本轮勘误 `git status --porcelain` 亲跑为空） | main 工作树（任务简报 ③ 显式指定；方案 v1 记录时点为 bd46169，四锚点文件区间零改动，见上「锚点有效性」） | 同 MacBridge 行（N/A：纯设计） |
| cordcode-ios（同族配套树，P0 要求记录，无论是否引用） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | 8eb262da2f3d8523f8270a9e5d2e4a9b0e5db7a5（元审核采样时为 f4a81ee4） | 干净（本轮勘误亲核） | 非本任务锚点来源（简报 ③ 指定 main）；记录以满足 P0「含配套工作树，无论是否实际引用」 | 同上（N/A） |
| openai/codex（官方） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD，本轮勘误亲核） | e72da2b53805894878023d01949a25a082e0a5cb | 只读 checkout，干净；结论一律以 FETCH_HEAD 为准（v1 记录「本地 main 落后」相对当前状态已过时——HEAD 已等于 FETCH_HEAD，锚点身份不变） | FETCH_HEAD（简报 ④：一律以 FETCH_HEAD 为准） | 同上（N/A） |

**污染声明**：`docs/2026-09-24-codex-remote-session-list-auto-retry.md` 内残留历史评审轮次的 "Kimi" 命名污染（该文档 F-13 已自证「无 Kimi 系 backend，r1–r3 评审证据均误」，但 F-3/§120/§416 仍有残留）。本方案引用该文档时只取其经本轮亲核的锚点（`catalog_recent_view.go`、`RuntimeManager.swift:205`），不扩散污染命名。本方案全文使用真实命名：codex-remote / ChatGPT Desktop / codex CLI。

**v1.4 修订轮来源亲核记录（2026-09-27，本轮设计师重跑；同样属历史快照，不构成实施来源清单）**：
- cordcode-macbridge 本树：feat/ios-native-message-timeline @ `5d1c9ae648f15dface4717d97f674d71756b01e5`，未提交状态仅未跟踪 r4 报告一份（只读）；`git diff 07721783..HEAD` 排除 docs 后**零差异**——代码与 main @ 07721783 等价，方案全部 Mac 锚点不受分支推进影响（r4 亦亲核同一事实）。
- cordcode-ios main @ `fe421cdc136930e3729b6b3d5c4479d5ecb90df4` 干净（本轮 `git status --porcelain` 亲跑为空）；本轮 iOS 锚点核验沿用任务简报指定的 main 工作树。
- 同族配套树 cordcode-ios-native-message-timeline：已前进至 `c49bdffeca811c6abb33a37969de2310e7e2ef51`（r4 记录时为 bb6f4584 且有三项未提交修改；本轮亲核该三项已随 c49bdffe 提交落盘、树现为**干净**，`git merge-base --is-ancestor` 亲核 bb6f4584 为其祖先，区间唯一提交为 MarkdownView CJK 斜体修复，`git diff --stat bb6f4584..c49bdffe` **不含方案四个 iOS 锚点文件**——ProjectionStore / CCCodeBridgeModels / ChatViewModel / CCCodeBridgeTransport）。
- openai/codex：FETCH_HEAD @ `e72da2b53805894878023d01949a25a082e0a5cb` 不变（HEAD == FETCH_HEAD，干净）。

**实施来源门模板（R4-A2 处置；实施者在每个门点现场重跑并记录，禁止复制本节任何哈希——配套 iOS 树活跃开发中，任何硬抄「当前值」都会立刻过时）**：

```text
仓库路径=<实施时实际工作树绝对路径>
分支=<git branch --show-current，detached 必须明示>
提交=<git rev-parse HEAD 完整哈希>
未提交状态=<git status --porcelain 逐项；与任务范围重叠的未提交修改必须纳入来源或先报告>
任务预期分支=<任务/交接文档指定>
配套仓库路径/分支/提交=<按 P0 分支族解析出的配套组合；解析不出唯一候选即停止并报告>
预期产品特性=<产物必须包含的能力/行为；纯 docs 任务标 N/A+理由>
锚点复核=<对本方案引用的关键锚点文件逐个 git diff <方案记录提交>..HEAD -- <文件> 亲跑；有差异先重核锚点再动工>
```

### 2.2 复用调查表

| 能力/需求 | 官方或既有实现与锚点 | 选择 | 必要改动/无法复用的证据 |
| --- | --- | --- | --- |
| 重连退避形状（1s 基准→30s 封顶、封顶归零、±jitter） | 官方 host：`app-server-transport/src/transport/remote_control/websocket.rs:78`（cap 30s）、`:1343-1345`（cap 归零）；MacBridge `agent/codex-remote/backoff.go:15-56` | **复用现状** | 已逐常量对齐，不改 |
| 判活超时（入站静默 60s） | 官方 pong timeout 60s `websocket.rs:74-75`；MacBridge `streamIdleLimit` 60s `ws.go:318` | **复用现状** | 已对齐 |
| controller 腿 cursor 事件重放 | 官方 cursor 机制是 **host↔relay 腿专用**：host 记录 client envelope 回传的 cursor（`websocket.rs:201-207`），host 重连 relay 时经 `x-codex-subscribe-cursor` header 续传缓冲的 client 消息（`websocket.rs:1275-1330`）；controller 收不到 cursor（owner attempt-008，plan:588-599） | **不实现** | E-1。MacBridge `stream.go:458-466` 的 `SubscribeCursorHeader` 为死代码，注释升级为明确「不实现」决定（S-5） |
| 官方客户端自动重连 | `app-server-client/src/remote.rs:181-632`：connect/notify/next_event/shutdown，**无任何 reconnect API**；断线即终止（pending requests 全部以传输错误失败，`remote.rs:510-518`）。官方 caller 侧模式 = **TUI** `tui/src/app/reconnect.rs:30-122`：120s 共享 deadline、delay 阶梯 `[0,1,2,4]` 后恒 8s、每次全量重建（新 client + initialize + resume thread）、不重发输入 | **自建维持** | MacBridge `watchBinding`/`restoreOnce`（`pairing_persist.go:250-296`）是与 TUI 同位的 caller 侧循环，形态正确；本方案优化其补偿行为而非替换 |
| host↔relay 腿至少一次投递（未 ack 重放） | 官方 host 侧 `BoundedOutboundBuffer`（`app-server-transport/src/transport/remote_control/websocket.rs:86-145`）：按 `(client_id, stream_id)` 缓冲已发未 ack 的 ServerEnvelope，容量 128（`transport/mod.rs:24` `CHANNEL_CAPACITY`），满则 writer 停止拉新事件（反压）；host 自身重连 relay 时**全量重发未 ack 信封**（`websocket.rs:965-988`）；**清除只发生在 client Ack**（`websocket.rs:112-138`；`buffer_by_stream.remove` 仅存在于 ack 内 `:136`）——client 关闭/过期清理**不触及** outbound_buffer（`close_client` `client_tracker.rs:312-323` 无 buffer 引用；idle sweep `websocket.rs:1154-1168` 与 join-set 清理 `:1140-1152` 仅 invalidate 重组器/消息流）；反压阈值是**全局** used 计数（insert `:109` / ack `:131` 跨 stream 增减）对 `CHANNEL_CAPACITY`（`websocket.rs:996`） | **认知对齐，不镜像** | 该机制只保护 host↔relay 腿；MacBridge 断线重连用新 stream_id（`pairing_persist.go:163-165`），旧 stream 的未 ack 信封不会补发到新流（E-1 结论不变），且被弃 stream 的未 ack 信封在 Desktop 进程存活期间**永久占用全局容量**（→ §6 风险 5、S-7）。host 侧重放可能让 MacBridge 在**同一 stream** 上收到重复信封（host 侧重连快于本仓 ping 周期的抖动场景；重放含 chunk 信封——chunk 与所属消息共享 seq_id，见「入站 seq 语义」行）——S-3 需同时做去重与缺口检测 |
| 入站 seq 语义（host→controller 方向） | 官方 `ServerEnvelope.seq_id` 按 `(client_id, stream_id)` **消息级**严格单调、从 1 起（`websocket.rs:1031-1067`：seq 在拆分前按消息分配，拆分后递增）；**chunk 信封共享所属消息的 seq_id**（`segment.rs:445-467` `build_chunk_envelope` 原文 `seq_id: envelope.seq_id`；序列化 >150KB 触发拆分，`segment.rs:20` `REMOTE_CONTROL_SEGMENT_MAX_BYTES`），仅以 `segment_id` 区分；官方 client→host 方向的去重只覆盖 `ClientMessage` 分支（`client_tracker.rs:134-143`），chunk/Ack 直通（`:221`）——**不可平移**为 host→controller 方向的镜像源；host→controller 方向的官方游标语义在 ack 路径：`(seq_id, segment_id.unwrap_or(usize::MAX))` 比较清除（`websocket.rs:112-138`，`envelope_cursor <= acked_cursor`） | **复用语义（消息级单调 + (seq, segment) 游标）** | 消息级单调性 → S-3 缺口检测成立；去重必须按 `(seq, segment)` 游标判定（S-3，v1.2 修正 F-1——按 seq 单值去重会把大消息第 2..n 个 chunk 当重复丢弃，重组永不完成） |
| chunk ack 形状 | 官方协议文档明确「Chunk acknowledgements carry `segment_id` so the sender can retain only the still-unacked wire chunks on reconnect」（`protocol.rs:114-119` Ack 注释）；host 侧 ack 处理对缺省 segment_id 取 `usize::MAX`（`websocket.rs:123`）。MacBridge `stream.go:390-407` ack 构造只带 `SeqID` 不带 `SegmentID`（`Envelope` 已有该字段，`envelope.go:45`），且 readLoop 对每个 chunk 信封逐条 ack（`stream.go:339`） | **修正对齐**（v1.2 复核 F-1 时亲核发现，超出 r1 意见范围；r2 已确认采纳） | 现状下首个 chunk 的 ack 等价 `(seq, MAX)` 游标，会**提前清除该消息全部 chunk** 的 host 重放缓冲；host 腿在 chunk 送达窗口中断时，剩余 chunk 不可重放、消息尾部静默丢失，且该丢失**不产生消息级 seq 缺口**（下一消息 seq 连续）——S-3 缺口检测的已证盲区，修复并入 S-3（E-12） |
| token 刷新提前量先例 | 官方 host 对 server token **到期前 5 分钟**刷新（`enroll.rs` `server_token_refresh_requirement_at`，经 `websocket.rs:1511-1652` 每次 connect 前判定）；refresh 失败退避 24–36s 均匀随机（`server_api.rs:27-28, 366-386`，commit `d047c33a1b`） | **参照** | OD-3 的 T 值量级先例：官方取分钟级提前量；E-9 fixture 仍门住 S-4 |
| 重连退避基准 | 官方 `async-utils/src/backoff.rs:12-19`：**200ms** ×2^(n-1)、jitter 0.9–1.1、**无 cap**（cap 30s 常量在 `websocket.rs:78-79`，cap 后归零在 `websocket.rs:1342-1351` `next_reconnect_delay`——均属 transport 层，非 backoff.rs；v1.2 修正归属，F-6①）；MacBridge `backoff.go:15-56` base **1s** | **维持现状（可选对齐）** | 形状/cap/归零已对齐（cap/归零锚点见左列 websocket.rs）；base 差 5 倍属本仓选择，S-5 仅记录不强制改 |
| turn 权威状态源（对账用） | `thread/turns/list` 摘要页：MacBridge `ReadColdHistory`/`mapColdPage` 已实现分页冷校准（`history_paginated.go:830-941`） | **复用** | S-2 把同一拉取路径用于重连后对账，不新写协议调用 |
| iOS 自动重试 loop | `ProjectionStore.swift:164-180` 可重试 code 白名单 + `.retryable` → 灾难重试 1s→30s（loop `ChatViewModel.swift:438-475`、delay 计算 `:477-494`，v1.4 行号对齐 r4 R4-A1 核验） | **复用+扩展** | S-1b 接上 wire `retryable` 标志消费；灾难 loop 本地退避维持不变 |
| wire `retryable`/`retryAfterMillis` 字段 | 桥已发送：`handlers_projection.go:176-181`（`WireError{Retryable, RetryAfterMillis, Attempts}`）；iOS 已解码：`CCCodeBridgeModels.swift:314-331`（`CCCodeBridgeError.retryable/retryAfterMillis/attempts`） | **复用** | 两头已备、中间未接：iOS `isRetryablePullError` 只看 code 白名单（`ProjectionStore.swift:887-900`），标志被忽略——S-1 的核心事实。**v1.4（R4-A1）**：`retryAfterMillis` 维持已解码、**不消费**——灾难 loop 用本地退避（`ChatViewModel.swift:477-494`），server 提示节奏的消费属 OD-1 裁决范围，不预承诺（§7.1） |
| 恢复等待语义 | `WaitForRestore`（`agent.go:103-135`）：瞬态 offline → 5s 后 `ErrRestoreInProgress` → `projection.hydrating` 可重试（`handlers_projection.go:863-871, 152-156`） | **复用现状** | 瞬态断线的冷开已被重试 loop 盖住；缺口在 mid-hydrate 传输失败（E-5） |
| 重组上限常量 | 官方 100MB：`segment.rs:21`（`REMOTE_CONTROL_REASSEMBLED_MAX_BYTES`）；MacBridge 1GB：`envelope.go:16`（`ReassembledMessageMaxBytes = 1073741824`） | **修正对齐** | 10 倍漂移；入站方向官方不会发超限帧（无功能故障），但常量应镜像官方（S-5） |
| ctrl token 刷新 | 每次重连 `restoreOnce` 强制 refresh（`pairing_persist.go:186-191`）；`ctrlExp` 已解析持久化（`pairing_persist.go:23-24,60-61,167`）但**无到期前调度**（全仓 grep 无调用） | **扩展** | S-4 补调度；提前量受 owner 方案约束「未取样前不得写死」（plan:508-509），设 E-9 fixture 门 |

### 2.3 现状行为摘要（断线→恢复全链路）

**检测**：三路判死——入站静默 >60s / ping 写失败（`ws.go:316-344`）、readLoop 错误（`stream.go:279-283`）、pong status != active（`stream.go:307-314`）。`watchBinding` 每 2s 轮询发现（`pairing_persist.go:262-274`）。

**重连**：`markOffline` → 退避（1s→30s）→ `restoreOnce`（45s 预算：ChatGPT 登录态探针 12s `chatgpt_auth.go:51` + ctrl token refresh + env lookup + WSS 握手 15s `ws.go:87` + initialize）→ `bindLive` 全量重建（新 streamID/epoch）→ `BindClient` 恢复全部已观察 thread 订阅 + collab/goal baseline（`session.go:161-212`）。401/403 → `errPairingRevoked` → 删配对、永久停止（`pairing.go:158-182`）。其余错误无限重试。

**断线窗口的投影语义（本轮亲核的完整分支）**：
- 冷开（sinceRev=0）→ `forceColdInspection`（`handlers_projection.go:122-127`）→ `prepareProjectionHydrateSource` 先过 `WaitForRestore`（`:864-871`）：
  - 瞬态 offline/restoring → `errProjectionHydrating` → `projection.hydrating` **可重试**（`:152-156`）→ iOS 灾难 loop 盖住 ✅
  - 无身份 / phase=failed → `ErrNotConfigured` 原样返回 → `markHydrateFailed("projection.source_inspection_failed", retryable=true)`（`:599-606`）→ `projection.hydrate_failed` → **iOS 白名单不含 → 硬错误「无法加载会话投影」** ❌
- 恢复后链路仍抖（mid-hydrate RPC/传输失败）→ `projection.source_read_failed`（`handlers_projection.go:1079-1083`，v1.2 补精确落点，F-4）→ wire `projection.hydrate_failed` 硬错误路径 ❌（E-5——owner 体感的主要来源；wire code 统一映射在 `handlers_projection.go:147`）
- 已 Ready 会话的增量拉取（`delta_at_head`，`:210-226`）不经 driver → 断线期间显示陈旧但可用的投影（诚实边界：无 stale 标记，R-5 只做桥内可见性，不改此行为）
- 会话列表：`thread/list` 8s 超时 → 列表加载失败（已有 30s 预算重试 + iOS 2s/4s/6s 自动重试，2026-09-24 方案已交付，不在本方案范围）

**断线窗口事件丢失（无补偿）**：无 cursor 重放（E-1）、无入站 seq 缺口检测（`stream.go:273-345` 不校验）、无重连后 turn 对账——`turnByThread` 跨重连保留（`codec.go:112-126`）但断线窗口内的 `turn/completed` 永不重放，live 投影停在 running，唯一收口途径是用户重开会话触发冷校准；桥重启时的孤儿收口 `RecoverOrphanDetailLoadingV2`（`projection_kernel.go:1115-1123`）只管 detail-loading 且只在进程重启后。另（v1.2 复核发现）：chunk ack 不带 `SegmentID`（`stream.go:390-407`）——官方语义下首 chunk ack 即清整消息重放缓冲，host 腿在 chunk 送达窗口中断时大消息尾部不可重放，且该丢失不产生消息级 seq 缺口（E-12，S-3 一并修）。

## 3. 设计与决定

### 3.1 S-1 恢复窗口拉取语义（跨仓，桥先行）

**S-1a（桥）终态/瞬态分类**：`markHydrateFailed` 的 `retryable` 参数目前对 `source_inspection_failed` 一律 `true`（`handlers_projection.go:603-605`），把「pairing revoked/无身份」「backend 未挂载/能力缺失」和「恢复中/传输抖动」混为一类。改为按错误源分类（v1.2 补全枚举，F-4）：
- `ErrNotConfigured`（无持久化身份或 phase=failed，`agent.go:110-119`）→ `retryable=false`（终态，iOS 显示硬错误，保留「请重新配对」语义）；
- `errProjectionSourceUnavailable`（`handlers_projection.go:430` 定义；codex-remote 路径返回点 `:852` agent 未挂载且 `ProjectionTurnCount==0`、`:876` 缺 `RichHistoryProvider` 能力断言）→ `retryable=false`（**v1.2 显式归类**：静态条件而非网络态——backend 未挂载/能力缺失在本次连接周期内不会自愈，S-1b 接上标志消费后会变成无限空转重试，属行为回退；与 revoked 同为终态类）；
- `ErrRestoreInProgress` 已单独走 hydrating（`handlers_projection.go:866-868`），不在本分类面内；
- ctx 取消/其余意外错误 → `retryable=true` + 沿用既有 `RetryAt` 同 revision 退避（2026-09-13 think.md 修复已建立；kernel `MarkFailed` 内 `RetryAt` 计算 `projection_kernel.go:940-942`）。

分类改动**只落在** `source_inspection_failed` call site（`:603-605`）；mid-hydrate 各落点维持 `retryable=true` 不变（瞬态类）：`source_read_failed`（`:1079-1083`）、`hydrate_queue_timeout`（`:1015`）、`bare_source_wait_failed`（`:1142`）、`commit_failed`（`:1157`、`:483`）。wire 侧无需新映射：kernel 失败记录的 `Retryable/RetryAt/Attempts` 已经由 RPC handler default 分支透传（`handlers_projection.go:161-173`，v1.4 行号精度修正，R2-A4①），iOS 看到的 code 统一为 `projection.hydrate_failed`（`:147`）。

**S-1b（iOS）接上标志消费**：`isRetryablePullError`（`ProjectionStore.swift:164-181`，v1.2 修正行号，F-6②）扩展为——`CCCodeBridgeError.retryable == true` 时返回可重试，code 白名单保留为无标志时的兼容路径（老桥/其他 backend）。revoked 与 source-unavailable 因 S-1a 已是 `retryable=false`，不会被自动重试掩盖。**v1.4（R4-A1 处置，选项 b）**：删除 v1.3「尊重 `retryAfterMillis` 作为灾难 loop 的初始等待」的承诺——灾难 loop 维持既有本地退避 1s→30s（`ChatViewModel.swift:477-494`），`retryAfterMillis` 维持已解码、不消费；理由与 OD-1 边界见 §7.1。

**数据流不变量**：不新增错误面、不改投影内容语义；只把「桥已判定的可重试性」如实传导到 iOS 的既有重试 loop。SSV2 无新增 writer。

### 3.2 S-2 重连后 turn 对账（v1.4 重写事务域：READY-safe 专用对账事务，F-R4-1）

对 `turnByThread` 非空的 thread（断线时有 in-flight turn 的小集合）逐个重拉权威摘要页，**只消费终态事实**：
- 权威拉取复用 `ReadColdHistory`（`history_paginated.go:871-941`；桥侧既有消费先例 `handlers_projection.go:1372-1385`。`readTurnsPage` 为 agent 包内私有、桥不可直达，故不另造窄接口）。对账只消费映射结果中的 (turnID, status)，内容丢弃。诚实成本注记：上游仍 in-progress 的 turn 会被 `mapColdPage` 走查 items 到 EOF（`history_paginated.go:832-852`），该内容对账同样丢弃——成本与冷开同价、仅发生在「对账发现仍在跑」的少见分支；
- 摘要显示 turn 已终结 → 收口该 turn（completed/failed 按权威 status），并同步收口 codec 侧 `turnByThread` 条目（否则已完成 turn 永久留在对账集合，每次重绑重复对账）；
- 摘要显示仍在跑 → 维持 running（不猜完成，SSV2 规则 7）；
- 对账失败 → 记日志，thread 留在对账集合，下一轮 3s 周期重试（见下方接线），不新增定时器。

**为什么不能复用既有 hydrate admission（v1.4 亲核补证，F-R4-1 证据链）**：codex-remote 的 hydrate source 是 pathless（`ProjectionSourceDescriptor{Identity, Path:"", Cursor:0}`，`handlers_projection.go:876-881`）。对 READY 会话，`BeginHydrateTransaction` 在 `sourceChanged=false` 时直接返回 `AlreadyReady`（`projection_kernel.go:1020-1026`）——对账成为 no-op；要强制进事务只能 `sourceChanged=true`，但新事务的 reducer 从空起步（codex-remote 不在 `pathlessRichHistoryBackend` 名单 `:969-976` → `pathlessFullRebuildSource=false`，而唯一 restore 分支要求 `!sourceChanged`，`:1069-1083`），commit 时 `k.reducer.Restore(baseline)`（`:1357`）把 committed 投影**整体替换**为 tx baseline——而 `ReadColdHistory` 只返回最新一页（`:856-923`），**已 prepend 的旧页 turns 会被抹掉**。已证：既有 API 的两个分支（不强制=不对账；强制=page-1 rebuild 破坏完整时间线）均不可行，不得用 `sourceChanged=true` 的 page-1 rebuild 冒充增量对账。

**READY-safe 专用对账事务（拟议设计；owner/锁/fence/输入/commit API 逐项如下）**：
- **Admission**：kernel 新增 `BeginReconcileTransaction(backendID, sessionID) (ProjectionHydrateAdmission, error)`——`k.mu` 下要求 Phase==Ready（否则返回错误，调用方回落既有 hydrate 路径）；复用既有 `projectionHydrateTransaction` 结构体构造 tx：`source` 原样携带 `session.committedSource`、`startCut = session.committedSourceCursor`（commit 写回 `:1393-1394` 时因此保持不变——对账不移动 source cut）、`reducer = NewProjectionReducer()` 后 `Restore(committed snapshot)`——**以当前 committed 投影为 baseline**，older-window turns、goal/detail manifest（随 turns 携带，`projection_reducer.go:377-381` 的 per-turn manifest 字段）全部在 baseline 内保留；`coldArmedTurnIDs` 置空、`liveArrived` 信号就绪；随后与 hydrate 同序安装 `session.status=Hydrating / session.hydrate=tx / hydrateDone`（`:1092-1095`）。**不新增锁、不新增 fence 机制**——`IngestLive` 的既有 fence 分支（`:1203-1211`）自动把窗口期 live 事件深拷贝排队为 `pendingLive` 并返回 Deferred，无需改 `IngestLive`。
- **输入事件（只允许权威终态更新；经既有 `ApplyHydrateEvent` `:1160` 应用到 tx-local reducer）**。producer 侧过滤规则（ghost-turn 防护）：
  1. baseline 中存在且非 terminal、摘要中 terminal 的 turn → 发一条终态事件：completed → `turn_completed`；interrupted/failed → `turn_error`（状态映射纪律镜像 codec `decodeTurnCompleted` `codec.go:372-381` + `mapAgentEvent` `events.go:174/211/221`，不造新事件词表；`upsertTurn` 对已存在 turn 是 merge 语义、内容保留，`projection_reducer.go:336-376`）；
  2. 摘要中存在、baseline 缺席的 turn → **只记日志，不发事件**（对不存在的 turn 发终态事件会经 `upsertTurn` 凭空造 bare terminal ghost turn）；
  3. baseline 非终态、但掉出摘要首页的 turn → 只记日志、维持 running（不猜完成；掉出首页说明断线窗口内到达了整页新 turn，边界如实登记）；
  4. 摘要与 baseline 终态不一致（如 baseline completed、摘要 failed）→ 记日志告警，不发事件（不静默改写已终态历史）。
  armed 集只含收到终态事件的 turn，apply 后全部 terminal → 既有 commit gate（`WaitHydrateCommitReady` 的 `NonTerminalTurnCountInSet==0`）平凡满足；对账 runner 为同步流程（Begin → Apply → Commit），无需 `MarkHydrateSourceIngestComplete`/`Wait`（kernel commit 不检查该标志，gate 语义由 producer 同步性保证）。
- **Commit**：既有 `CommitHydrateTransaction`（`:1319`）**零改动**——baseline（committed 快照 + 终态更新）经 `Restore` 原子发布，`pendingLive` 按戳序 drain（`:1358-1374`），patch 经 `FlushPatch` 发布。runner 侧复用 hydrate commit 后处理（`releaseDeferredPushCandidates` + `PublishProjectionPatch`，`handlers_projection.go:1161-1177`），但**不调用** `persistCodexProducerSeed`（`:1182-1184`）与 checkpoint 持久化——producer cursor/seed 全程不动。
- **Abort（对账失败语义，与 hydrate `MarkFailed` 的关键差异）**：kernel 新增 `AbortReconcileTransaction(backendID, sessionID) ([]string, error)`——丢弃 tx（终态更新从未进 committed reducer，committed 投影本就完好），Phase→Ready，**把 `pendingLive` 按戳序 drain 回 committed reducer 并 FlushPatch**，返回 applied EventIDs 供 runner 释放 deferred push candidates。不能用 `MarkFailed`（`:925-951` 置 Failed 且丢弃 pendingLive——依赖下次冷重建兜底；对账中止没有重建，丢 pendingLive 即丢 live 真值，违反 R-4「不静默丢失」）；也不能用 `MarkReady`（`:911-918` 丢弃 tx 但同样不 drain pendingLive）。
- **并发写者边界（亲核）**：窗口期 older-walk 的 `PrependHistoricalTurns` 因 Phase!=Ready 诚实失败（`:1680-1684` 硬门），不会撕开 baseline（客户端 older 请求可重试，与冷 hydrate 窗口期同型）；`IngestLive` 被 fence；producer state/checkpoint 不被对账触碰；窗口期冷开 pull 加入单飞行（Hydrating 分支返回 Done）等待对账结束。**不新增第二 writer**（SSV2 护栏）：对账事务与 hydrate 事务共用同一 Kernel 锁、同一 reducer 发布路径、同一 fence 语义。

**接线说明（v1.2 补，F-3——四要素各有可指名宿主，实施者不新造机制；v1.4 不变）**：
1. **触发/完成点**：`BindClient` 既有 per-thread attach goroutine（`session.go:195-211`）即对账触发点——`attachLiveThreadOn` 成功后，agent 检查该 thread 的 `turnByThread`（`codec.go:22`，agent 层 `LiveCodec` 自持，重绑不重置：`ResetNativeSessionState` `:112-126` 刻意不含它）；非空 → 加入 agent 侧 pending 对账集合并发信号。v1.1 的「全部 attach 完成后」按此修正为 per-thread 完成点：不新增 WaitGroup/聚合计数，且单个 attach 卡 15s 超时不阻塞其他 thread 的对账。
2. **对账集合**：observed（本次重绑 re-attach 的 thread）∩ `turnByThread` 非空——不为无监听者的 thread 造 kernel 会话。
3. **agent→桥信号 seam**：镜像 `CatalogRefreshSignals` 先例（`agent.go:168-180`：one-slot 合并通道 + 数据不随信号走；桥侧类型断言消费先例 `go-bridge/session_discovery.go:159`）——agent 新增对账信号通道 + pending 集合读取/清除方法（机制同型，命名实施期定）。
4. **消费/重试宿主**：`attachLiveCatalogPeriodically` 3s 循环（`go-bridge/main.go:1046-1066`）的 `attach()` 步骤之后 drain 信号、拉取 pending 集合、逐 thread 执行对账；失败 thread 留在集合，下一轮 3s 自然重试。

事务域归属（v1.4 重写）：对账走上述**专用 reconcile 事务**（`BeginReconcileTransaction`/`ApplyHydrateEvent`/`CommitHydrateTransaction`/`AbortReconcileTransaction`），与冷开 hydrate 共用同一 Kernel 锁与 fence 语义（SSV2 规则 5——单 writer、单发布路径），但 admission 独立：READY 会话强制进 reconcile 事务且以 committed 快照为 baseline（既有 `BeginHydrateTransaction` 对 READY pathless 会话只能 no-op 或空 reducer 重建，见上）；re-observed items 幂等（`codec.go:118-124` 注释已有先例：重连后重观察事件 idempotent）。

### 3.3 S-3 入站 seq 缺口检测与去重

`readLoop` 按 per-stream 维护**消息级高水位游标** `(lastSeq, lastSeg)`（`lastSeg` 为 chunk 的 `segment_id`，plain 消息为空）：

- **去重（v1.2 重写，F-1；判定表对 plain 与 chunk 两类信封分别可判定）**：信封按 `(seq_id, segment_id)` 游标判定——
  - `seq_id < lastSeq` → 重复（旧消息重放，plain 与 chunk 同判）；
  - `seq_id == lastSeq` 且 `lastSeg` 为空（上一条为 plain）：再遇 plain → 重复（同 plain 消息重放）；遇 chunk → 理论不可达（官方一条消息要么整体要么全 chunk，`segment.rs:327` 二选一），记异常日志并按非重复处理（fail-visible，不静默丢弃）；
  - `seq_id == lastSeq` 且 `lastSeg` 非空（上一条为 chunk）：chunk `segment_id <= lastSeg` → 重复（同消息已见 chunk 重放）；`segment_id > lastSeg` → 新 chunk（接受，更新 lastSeg）；遇 plain → 理论不可达，同上异常处理；
  - `seq_id > lastSeq` → 非重复：`== lastSeq + 1` 正常推进（plain 置 `lastSeg` 空 / chunk 置 `lastSeg`），`> lastSeq + 1` → 消息级缺口（见下）。

  游标比较语义镜像官方 ack 清除路径的 `(seq, segment_id.unwrap_or(usize::MAX))` 游标（`websocket.rs:112-138`）。**v1.1 的「`seq_id <= last_seen` → 跳过」按字面实施会把大消息（>150KB 拆分）第 2..n 个 chunk 当重复丢弃、重组永不完成——已证错误，废除**；v1.1 所引镜像源 `client_tracker.rs:134-143` 是 client→host 方向、仅 `ClientMessage` 分支（chunk `:221` 直通），不可平移，v1.2 改引 ack 游标语义。
- **缺口检测**：消息级——`seq_id > lastSeq + 1` → 记录缺口范围与关联 thread（chunk 信封共享消息 seq_id，不单独参与缺口判定）。缺口 → 触发 S-2 对账。
- **chunk ack 补 `SegmentID`（v1.2 新增子项，E-12，超出 r1 意见范围；r2 已确认采纳）**：ack 构造（`stream.go:390-407`）对 chunk 信封补 `SegmentID: env.SegmentID`（字段已备，`envelope.go:45`），对齐官方协议文档（`protocol.rs:114-119`：chunk ack 携带 segment_id，sender 只保留未 ack 的 wire chunks）。不修则首 chunk ack 等价 `(seq, MAX)` 游标提前清除整消息缓冲（`websocket.rs:123`），host 腿中断丢消息尾部且**不产生消息级缺口**——缺口检测的已证盲区；修复后未送达 chunk 保留在 host 重放缓冲，与缺口检测共同覆盖 R-4「不静默丢失」。

不重传、不断线、不伪造事件——fail-visible 而非 fail-closed。依据：host→controller 方向 `ServerEnvelope.seq_id` 按 `(client_id, stream_id)` **消息级**严格单调（官方 `websocket.rs:1031-1067`），消息级缺口即真实丢失；chunk 级丢失由 ack 形状修复消除（E-12）。

### 3.4 S-4 ctrl token 到期前主动刷新

用已持久化的 `ctrlExp`（`pairing_persist.go:23-24,60-61`）调度到期前刷新：长连接期间到期前 T 秒执行 `refreshControlToken`，成功则原地更新持久化 token（连接不断）；失败处置（v1.2 补 revoked 唯一去向，F-5）：
- 返回 `errPairingRevoked`（401/403，`pairing.go:383-384`、`:408-409`）→ **立即 `invalidateRevokedPairing`**，与 restoreOnce/watchBinding 既有语义一致（`pairing_persist.go:186-189`、`:287-289`：invalidate + 永久停止 + 重新配对提示）——不等待 token 自然到期连接死亡，不给调度器新增 revoked 分叉语义；
- 其余失败（网络/5xx）→ 不主动杀连接，沿用现有路径（token 到期 → 连接死亡 → 重连时强制 refresh），不新增死亡路径。

**T 的取值受 owner 方案约束**（plan:508-509「未取样前不得写死提前量或退避」）：实现为常量但被 E-9 fixture 门住——E-9 未满足前 S-4 不得实施。量级先例：官方 host 对 server token 取**到期前 5 分钟**（`enroll.rs` `server_token_refresh_requirement_at`）；ctrl token 与 server token 是不同凭证，E-9 仍须实测 ctrlExp 的真实有效期分布。

### 3.5 S-5 常量与死代码对齐

- `ReassembledMessageMaxBytes` 1GB → 100MB（对齐官方 `segment.rs:21`）；
- `SubscribeCursorHeader`（`stream.go:458-466`）注释升级为明确决定记录：「官方不向 controller 交付 cursor（E-1），本仓决定不实现 controller 腿重放；`RecordedCursor` 保留为观测」。

### 3.6 S-6 driver 状态暴露（桥内）

- `watchBinding`/`markOffline` 保留真实错误类别（2026-09-26 think.md 教训：升级断裂被折叠成「已配对，等待 ChatGPT Desktop」），日志与 descriptor readiness 携带可区分类别（offline / restoring / revoked / env-missing）；
- phase 转换以桥内日志 + 既有 `StructuredInstanceReadiness` 查询面（`diagnostics.go:19-35`）暴露，为后续 `backend_status_changed` 推送立项预留数据源；**不新增跨仓协议**。

### 3.7 S-7 ack 熔断器有界化（v1.2 新增，F-2 对策升主链）

**现状**：熔断器激活后本 stream 生命周期内停发一切 ack（`stream.go:359-373` armed → `ack()` `:397-399` 早退），无有界时长、无最低频率。熔断职责是停掉 2026-09-14 事故的 ack→sentinel-error→ack 自激风暴（750/s，`think.md:415-425`），不是停 buffer 排空——但无限期停 ack 会让 host 侧缓冲只增不减，与全局反压叠加成整腿永久停摆（§6 风险 5）。

**设计**：熔断改为有界——激活后 T 秒（默认 30s）自动重试：立即以 S-3 的 per-stream 最高已见 `(seq, segment)` 游标**补发一条 ack**（游标状态与 S-3 共享，不新增状态）；
- transport 接受 → 恢复正常逐信封 ack，host 侧缓冲排空（used 恢复下降）；
- transport 仍拒绝（sentinel 再现）→ 重新 armed、重新计时。sentinel 错误通知率上界 = **1 条/T**（事故形态 ~750/s）。
- T 的量级参照官方 refresh 失败退避 24–36s（`server_api.rs:27-28`）；这是内部噪声上界的工程选择，非协议时序声明，不适用 plan:508-509 的取样门（该约束针对依赖外部 ctrlExp 分布的 S-4 提前量）。
- **依赖**：S-3 的 chunk ack `SegmentID` 子项（E-12）——补发 ack 必须携带 `(seq, segment)` 游标，否则 `(seq, MAX)` 会提前清除未送达 chunk（见 §3.3）。

**残余风险（诚实边界）**：① host 侧持续拒绝 ack（0.154.0-alpha.6.2 形态的上游缺陷）MacBridge 侧无法排空缓冲——有界熔断把噪声限到 1 条/T，停摆本身只能靠 Desktop 侧修复/重启；② 正常断线时在途未 ack 信封仍随被弃 stream 永久占用全局容量（host 侧无 client 关闭清除路径，E-11），单次量为在途窗口（通常 0–2 条），累计效应随 Desktop 存活时间增长——完整修复需官方 host 侧改动（非目标：不修改官方）。S-6 暴露「反复 60s 零事件流死亡」停摆签名与熔断状态供诊断。

**完成断言**：定向单测（fake conn + fake 时钟）——armed → 前进 T → 补发 ack 已发出且携带最高已见游标；sentinel 重现 → 重新 armed 且重新计时；未 armed → 行为与现状逐字节一致。E-10 实施期真机观测熔断状态日志与停摆签名可见性（sentinel 风暴本身依赖上游 host 版本缺陷，真机不可控复现，行为验收以单测为准）。

### 3.8 OD 表

| ID | 问题 | 互斥选项/推荐 | 状态/决定来源 | 影响切片 | 各选项验收 |
| --- | --- | --- | --- | --- | --- |
| OD-1 | 瞬态拉取失败的重试语义：iOS 消费 wire `retryable` 标志（推荐）vs 仅扩 code 白名单 | 推荐**消费标志**：字段两头已备（§2.2），桥分类先行可精确区分 revoked 与瞬态；白名单方案无法区分同 code 下的终态/瞬态（revoked 与传输失败同为 `source_inspection_failed`） | pending（改变用户可见错误行为，owner 确认） | S-1a/S-1b | 标志方案：瞬态自动重试+revoked 硬错误（§1 验收 1/3）；白名单方案：无法满足验收 3，需另设 code 拆分 |
| OD-2 | `backend_status_changed` 推送是否并入本方案 | 不并入，本方案仅 S-6 桥内钩子 | **decided**（macbridge think.md:45，2026-09-26 裁决另立） | S-6 | 推送立项时可直接消费 S-6 暴露面 |
| OD-3 | S-4 提前量 T 的默认值 | 待 E-9 fixture（真机 ctrlExp 分布）后定；候选区间 30–120s | pending（E-9 门） | S-4 | E-9 满足后 T 进断言；未满足则 S-4 整体不实施 |

## 4. 证据与实施前置门

| ID | 待证明命题 | 证据类别/来源 | 断言与锚点 | 状态 | 未满足时阻塞 |
| --- | --- | --- | --- | --- | --- |
| E-1 | 官方不向 controller 提供 cursor 事件重放；cursor 是 host↔relay 腿机制 | 源码事实（官方 FETCH_HEAD）+ owner 实测 | `websocket.rs:201-207`（host 记录 client 回传 cursor）、`:1275-1330`（host 重连带 header）；attempt-008（plan:588-599）；MacBridge `stream.go:459-460` 注释「live target never delivered one」 | **verified** | 非目标成立性；若未来官方新增 controller cursor，S-5 注释与「不做重放」决定需复审 |
| E-2 | 官方 RemoteAppServerClient 无自动重连；官方 caller 侧模式 = TUI 全量重建 | 源码事实 | `remote.rs:181-632` API 面无 reconnect；断线即终止并失败全部 pending（`remote.rs:510-518`）；TUI `reconnect.rs:30-122`（120s 窗口、`[0,1,2,4,8∞]` 阶梯、新 client+initialize+resume、不重发输入） | **verified** | 「自建维持」选择成立 |
| E-3 | iOS 只认 code 白名单；wire retryable 已解码未消费 | 源码事实（两仓） | `ProjectionStore.swift:887-900`（只读 code）；`CCCodeBridgeModels.swift:314-331`（retryable 已解码）；`handlers_projection.go:176-181`（桥已发送） | **verified** | S-1b |
| E-4 | 瞬态 offline 冷开已走可重试 hydrating | 源码事实 | `agent.go:103-135` + `handlers_projection.go:864-871, 152-156` | **verified** | S-1 范围界定（只补 mid-hydrate 缺口） |
| E-5 | mid-hydrate/终态失败 → `hydrate_failed` 硬错误（iOS 白名单缺） | 源码事实 | 终态/检查失败落点 `handlers_projection.go:599-606`（source_inspection_failed）；mid-hydrate 实际落点 `:1079-1083`（source_read_failed，v1.2 补，F-4）；wire code 统一映射 `:147`；`ProjectionStore.swift:164-181` 白名单不含 `projection.hydrate_failed` | **verified** | S-1a/S-1b |
| E-6 | 重连后无 turn 对账；断线窗口完成的 turn 停在 running | 源码事实 | `session.go:161-212`（BindClient 只订阅+baseline）；`codec.go:112-126`（turnByThread 保留）；孤儿收口仅桥重启（`projection_kernel.go:1115-1123`） | **verified** | S-2 |
| E-7 | ctrlExp 持久化但无到期前刷新调度 | 源码事实 | `pairing_persist.go:23-24,60-61,167,186-191`；全仓 grep 无调度调用 | **verified** | S-4 |
| E-8 | 常量漂移：重组上限官方 100MB vs 本仓 1GB；backoff/pong 已对齐（base 200ms vs 1s 仅记录） | 源码事实 | `segment.rs:21` vs `envelope.go:16`；`websocket.rs:74-79` vs `backoff.go:15-56`/`ws.go:318`；官方 base `async-utils/src/backoff.rs:12-19`（v1.2 行号对齐，F-6①；cap/归零锚点在 `websocket.rs:78-79`/`:1342-1351` 非 backoff.rs） | **verified** | S-5 |
| E-11 | host↔relay 腿有未 ack 重放缓冲（至少一次投递）+ 128 容量**全局**反压；清除**仅** client Ack 一途；controller 腿无重放（E-1 不变） | 源码事实 | `websocket.rs:86-145`（BoundedOutboundBuffer 仅 new/insert/ack/server_envelopes 四方法）、`:965-988`（重连重放）、`transport/mod.rs:24`（CHANNEL_CAPACITY=128）；清除仅 client Ack（`:112-138`，`buffer_by_stream.remove` 仅 `:136`）；client 关闭/过期清理不触及 buffer（`close_client` `client_tracker.rs:312-323`、join-set `websocket.rs:1140-1152`、idle sweep `:1154-1168`）；反压为全局 used（`:109`/`:131`）对 128（`:996`）；官方测试 `remote_control_transport_clears_outgoing_buffer_when_backend_acks`（`tests.rs:1528`，不变量=已 ack 不重发） | **verified**（v1.2 修正清除子声明，F-2） | S-3 去重语义；S-7 设计依据与残余风险界定；§6 风险 5 |
| E-12 | MacBridge chunk ack 不带 `SegmentID`，官方语义下首 chunk ack 提前清除整消息重放缓冲——host 腿中断丢消息尾部且不产生消息级 seq 缺口（v1.2 新增，设计师复核 F-1 时亲核，超出 r1 意见范围；r2 已确认采纳） | 源码事实 | MacBridge `stream.go:390-407`（ack 构造无 SegmentID；逐 chunk ack 调用点 `:339`）+ `envelope.go:45`（字段已备）；官方 `protocol.rs:114-119`（协议文档要求 chunk ack 携带 segment_id）、`websocket.rs:123`（缺省 segment_id 取 `usize::MAX`）、`segment.rs:445-467`（chunk 共享消息 seq_id）、`:20`（150KB 拆分阈值） | **verified** | S-3 的 ack 子项；S-7 补发 ack（依赖该子项） |
| E-9 | ctrl token 实际有效期分布与刷新时序（fixture） | 原始运行证据 | 计划路径：真机抓 enroll/refresh 响应的 `ctrlExp`（脱敏，不记 token 值），≥5 个样本覆盖典型会话 | **pending**（实施期捕获） | **S-4 整体**（OD-3 依赖） |
| E-10 | 受控断线复现样本（断 relay 10–60s） | 原始运行证据 | 计划路径：真机 + 桥日志对齐一次断/恢复窗口（iOS 拉取行为 + 桥 hydrate 行为 + 对账行为）。v1.2 增（F-2）：熔断状态与「反复 60s 零事件流死亡」停摆签名的日志可见性观测项——sentinel 风暴本身依赖上游 host 版本缺陷（0.154.0-alpha.6.2 形态），真机不可控复现，S-7 行为验收以定向单测为准，真机只验可见性 | **pending**（实施期捕获） | S-1/S-2/S-3 的完成验收门；S-7 的可见性验收 |

Gate A（证据）作用：S-1/S-2/S-3/S-5/S-7 的设计依据全部 verified（S-3/S-7 含 E-12）；S-4 被 E-9 阻塞实施。Gate B（产品决定）：OD-1 pending 阻塞 S-1 实施；OD-2 已决；OD-3 随 E-9。

## 5. 实施切片

| 切片 | 需求/可见结果 | 组件与复用方式 | 依赖/前置 | 最小验证/完成证据 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-1a | revoked/source-unavailable 硬错误、瞬态可重试的桥侧分类（R-1 验收 3） | `handlers_projection.go` `markHydrateFailed` 分类（仅 `:603-605` call site，§3.1 枚举）；复用 `RetryAt` 退避 | E-5；OD-1 | 定向单测：`ErrNotConfigured` → retryable=false；`errProjectionSourceUnavailable` → false；ctx/意外 → true+RetryAt；mid-hydrate 落点（`:1080` 等）保持 true 不变 | 分类失败回退现状（一律 true），不劣化 |
| S-1b | iOS 消费 retryable 标志，瞬态失败自动重试（R-1 验收 1） | `ProjectionStore.swift` `isRetryablePullError` 扩展；复用灾难 loop 与 `retryAfterMillis` | S-1a；OD-1；E-3 | iOS 定向单测：标志 true → `.retryable`；false/无标志 → 白名单路径；真机 E-10 验收 1 | 标志缺失时白名单兜底（兼容老桥） |
| S-2 | 断线窗口完成的 turn 重连后自动收口（R-2） | `session.go` BindClient 既有 per-thread attach goroutine（:195-211）为触发点；agent 侧 pending 集合 + 镜像 `CatalogRefreshSignals` 的信号 seam（`agent.go:168-180`）；桥侧 `attachLiveCatalogPeriodically` 3s 循环（`main.go:1046-1066`）消费；重拉摘要复用 `ReadColdHistory` 路径；Kernel hydrate 域事务提交（`projection_kernel.go:889/996/1160/1319`，接线见 §3.2） | E-6；E-10 验收 2 | 桥定向单测（fake app-server 断线窗口收口）：对账提交走 Kernel 事务域；收口后清除 codec `turnByThread` 条目；失败 thread 留 pending 下轮重试；真机 E-10 | 对账失败记日志，3s 周期重试；不猜完成 |
| S-3 | 入站 seq 去重+缺口检测→触发对账（R-4） | `stream.go` readLoop per-stream `(seq, segment)` 游标去重（镜像官方 ack 游标语义 `websocket.rs:112-138`，规则见 §3.3）+ 消息级缺口检测；chunk ack 补 `SegmentID`（E-12）；复用 S-2 对账 | S-2；E-1/E-11/E-12 | 桥定向单测：注入重复 plain seq → 跳过；**重放 chunk（同 seq 同 segment）→ 跳过；同消息不同 segment（递增）→ 不丢弃**（F-1 负例）；注入消息级缺口 → 检测+触发；正常流零告警；chunk ack 携带 segment_id 断言。官方不变量参照 `websocket_state_drops_replayed_client_chunks_after_completion`（`websocket.rs:3062`） | 检测异常时降级为纯日志（不阻断事件流） |
| S-4 | ctrl token 到期前续期（R-3） | `pairing_persist.go` 基于 `ctrlExp` 调度；复用 `refreshControlToken` | **E-9**；OD-3 | 定向单测（fake 时钟）+ 真机长连接观测；revoked 分支断言：调度刷新 401/403 → 立即 invalidateRevokedPairing（§3.4） | revoked → 立即 invalidate（与 restoreOnce 一致）；其余失败沿用现有重连路径 |
| S-5 | 常量对齐 + 死代码决定记录 | `envelope.go:16` 100MB；`stream.go:458-466` 注释 | E-8 | 常量断言单测 | 无（纯对齐） |
| S-6 | 桥内状态可区分（R-5） | `pairing_persist.go`/`diagnostics.go` 错误类别保留 | 无（独立） | 日志/readiness 定向断言；含熔断状态与停摆签名（供 S-7 诊断） | 无 |
| S-7 | ack 熔断有界化：瞬态 sentinel 不再永久禁 ack（§6 风险 5 对策） | `stream.go:359-373` 熔断器加 T 秒有界重试 + 最高游标补发 ack（游标状态共享 S-3） | S-3 的 ack `SegmentID` 子项（E-12）；E-11 | 定向单测（fake conn + fake 时钟）：armed→T 秒→补发 ack 携带最高游标；sentinel 重现→重新 armed；未 armed 行为与现状一致（完成断言见 §3.7）；E-10 真机观测可见性 | transport 持续拒绝时噪声限 1 条/T；停摆签名经 S-6 暴露，等 Desktop 侧修复 |

无数据迁移、无不可逆动作。S-1a→S-1b 有序依赖；S-2→S-3 有序依赖；S-3 的 ack `SegmentID` 子项 → S-7 有序依赖（S-3 的去重/缺口部分不阻塞 S-7 之外的工作）；S-4/S-5/S-6 可独立。

## 6. 验证、风险与交付

**验证分层**（按构建成本纪律，本方案属 D3 状态/协议边缘）：
- 方案阶段已验证：E-1~E-8、E-11、E-12（源码事实，锚点见 §4）。
- 实施期：定向单测（桥：对账事务/seq (seq,segment) 游标去重+缺口检测/chunk ack segment_id/token 调度/错误分类/熔断有界化；iOS：retryable 消费）+ 各自定向 build + 交付前一次真机安装。官方测试不变量可作断言参照：`remote_control_transport_reconnects_after_disconnect`（`tests.rs:1086`，断连自动重建）、`remote_control_transport_clears_outgoing_buffer_when_backend_acks`（`tests.rs:1528`，已 ack 不重发）、`websocket_state_drops_replayed_client_chunks_after_completion`（`websocket.rs:3062`，完成后旧 seq 丢弃）、`expired_token_refresh_failure_throttles_reconnect_without_websocket`（`websocket_refresh_tests.rs:716`，refresh 失败退避不发新连）。
- 端到端验收（E-10）：owner 测试矩阵（受控断线场景，§1 验收 1–4 逐项），agent 不以单测冒充。

**主要风险与对策**：
1. S-2 对账与 live 事件竞态 → 走 Kernel 既有 fence/事务域（SSV2 规则 5），re-observed 幂等；单测覆盖「对账提交与 live delta 并发」负例。
2. S-1 扩大重试掩盖真错误 → S-1a 分类先行（revoked 恒硬错误）；白名单保留兜底；E-10 验收 3。
3. S-3 seq 语义误报（官方 host 是否保证入站连续未取样）→ 检测只触发对账+日志，不断线不重传；E-10 观察正常流是否零误报，误报则收紧判定条件。**v1.1 修正**：host→controller 方向 seq 严格单调已由官方源码证实（`websocket.rs:1031-1067`，E-11），本风险降级为「实现正确性」而非「语义不确定」。
4. S-4 提前量拍脑袋 → E-9 fixture 门整体阻塞切片；官方 5 分钟先例（`enroll.rs`）提供量级参照。
5. **（v1.2 重写恢复界，F-2）ack 熔断 × host 全局反压 → 整腿永久停摆**：MacBridge 熔断器激活后本 stream 生命周期内停发一切 ack（`stream.go:359-373`）→ host 侧未 ack 信封累积至 128 → writer **全局**停止拉新事件（`websocket.rs:996`，跨 stream used 计数）且 ping 分支不受反压门限、host↔relay 连接存活不触发 host 侧重连 → MacBridge 60s 入站静默判死重连，但重连换新 stream_id（`pairing_persist.go:163-165`），**被弃 stream 的 ≤128 条未 ack 信封在 Desktop 进程存活期间无任何清除路径**（E-11：client 关闭/过期清理不触及 outbound_buffer）→ 单次熔断满缓冲事件即可让 host writer 对所有 stream 永久停摆，**重连不可愈**，整腿死亡直到 Desktop 重启——与 ios think.md:477「2.5h 不自愈」同构且更严重。**对策（v1.2 升主链，S-7）**：熔断改 T 秒有界 + 最高游标补发 ack（完成断言见 §3.7），瞬态 sentinel 不再永久禁 ack。**残余（MacBridge 侧不可修，如实登记）**：① host 侧持续拒绝 ack（上游缺陷形态）只能靠 Desktop 修复/重启；② 正常断线在途信封随被弃 stream 永久占用全局容量（单次在途窗口量，累计随 Desktop 存活时间）——完整修复需官方 host 侧改动（非目标）。**触发条件修正**：v1.1 的「若 E-10 复现停摆才修」废除——E-10 是干净断 relay 10–60s，不激活熔断（激活需 transport-error sentinel 风暴，即 2026-09-14 事故形态 `think.md:415-425`），该条件与致因不匹配、大概率永不点火；S-7 验收以定向单测为准，E-10 只承担可见性观测（§4 E-10）。

**阻塞清单**：OD-1（owner 确认重试语义）、E-9（token fixture）、E-10（断线复现，实施期）。S-7 无新增阻塞门（验证为定向单测，无 fixture 依赖）。

**下一阶段入口**：r2 已通过（APPROVED，blockers 0）+ v1.3 勘误收口——下一步为 owner 排期 R2-A1~A4 建议级意见（可并入实施期，r2 §8）并授权实施；实施按切片进行，S-1a/S-1b/S-2/S-3/S-7 为主链，S-4 视 E-9，S-5/S-6 随批。

## 7. 修订处置（v1.2 ← r1）

| 意见 ID | 处置 | 核实证据（本轮亲核） | 修订位置 | 理由/遗留事项 |
| --- | --- | --- | --- | --- |
| F-1（阻塞） | 采纳 | `segment.rs:445-467`（`build_chunk_envelope` 原文 `seq_id: envelope.seq_id`）、`:20`（150KB 拆分阈值）、`:301`（拆分入口）；`websocket.rs:1031-1067`（seq 消息级分配）、`:112-138`（ack 游标 `(seq, seg.unwrap_or(MAX))` 比较）；`client_tracker.rs:134-143`（仅 ClientMessage 分支去重）、`:221`（chunk/Ack 直通）；MacBridge `stream.go:425-434`（组装键 `*env.SeqID`）、`:390-407`（逐信封 ack） | §2.2「入站 seq 语义」行重写 + 新增「chunk ack 形状」行；§3.3 去重规则改 `(seq, segment)` 游标并废除 seq 单值规则；§5 S-3 单测补「重放 chunk 去重、同消息不同 segment 不丢弃」负例 | 按字面实施确会静默丢弃大消息后续 chunk；镜像源不可平移，改引官方 ack 游标语义。缺口检测半段（消息级）不受影响，维持 |
| F-2（阻塞） | 采纳 | `websocket.rs:91-144`（Buffer 仅四方法）、`:136`（remove 仅在 ack）、`:996`（全局 used<128 门限）、`:109/:131`（used 增减）；`client_tracker.rs:312-323`（close_client 无 buffer 引用）；`websocket.rs:1140-1152`（join-set）、`:1154-1168`（idle sweep）、`:965-988`（重连重放）、ping 分支不受反压门限（`:996-1019` select 结构）；`pairing_persist.go:163-165`（重连换 stream_id）；`stream.go:359-373`（熔断无界） | §2.2 host↔relay 行清除声明改源码准确表述；§4 E-11 重写；§6 风险 5 重写（永久停摆模式 + 重连无效 + 残余风险）；新增 §3.7 S-7 + §5 S-7 行（对策升主链，含完成断言）；E-10 触发条件修正 | 「client shutdown 清除」无源码支撑，废除；对策从「E-10 复现才修」改为 S-7 主链切片（评审选项 (a)）；残余（host 侧持续拒绝、在途信封泄漏）如实登记为 MacBridge 侧不可修 |
| F-3（建议） | 采纳 | `session.go:195-211`（attach 为 per-thread goroutine，无聚合点）、`codec.go:22/:112-126`（turnByThread agent 层自持、重绑不重置）；`agent.go:168-180`（CatalogRefreshSignals 先例）、`go-bridge/session_discovery.go:159`（桥侧类型断言消费先例）；`go-bridge/main.go:1046-1066`（3s 循环宿主）；`projection_kernel.go:889/996/1160/1319`（hydrate 事务域 API） | §3.2 接线四要素（触发点/集合/信号 seam/消费宿主）+ §5 S-2 行 | 「全部 attach 完成后」修正为 per-thread 完成点：免造 WaitGroup，且单 attach 卡 15s 不阻塞其他 thread 对账；补「收口后清除 codec turnByThread」断言 |
| F-4（建议） | 采纳 | `handlers_projection.go:430`（定义）、`:852`（agent 未挂载且 ProjectionTurnCount==0）、`:876`（缺 RichHistoryProvider）；`:1079-1083`（source_read_failed，mid-hydrate 实际落点）；`agent.go:103-135`（WaitForRestore 错误面：nil/ErrNotConfigured/ctx/ErrRestoreInProgress）；`handlers_projection.go:147/:160-171`（wire 映射与 kernel 失败记录透传） | §3.1 S-1a 分类枚举补 `errProjectionSourceUnavailable` → retryable=false；§4 E-5 补 `:1080` 落点；§2.3 mid-hydrate 句精化 | 静态条件（backend 未挂载/能力缺失）非网络态，S-1b 接上后变无限空转属行为回退，归终态类；mid-hydrate 各落点维持 true |
| F-5（建议） | 采纳 | `pairing.go:383-384/:408-409`（401/403 → errPairingRevoked）；`pairing_persist.go:186-189`（restoreOnce 透传 revoked）、`:287-289`（invalidateRevokedPairing + 永久停止） | §3.4 S-4 失败处置二分；§5 S-4 行 | revoked 唯一去向 = 立即 invalidate（与 restoreOnce 一致）；其余失败沿用连接死亡路径，不给调度器造新分叉 |
| F-6（建议） | 采纳 | `backoff.rs:12-19`（200ms/2^/jitter，无 cap）；`websocket.rs:78-79`（cap 常量）、`:1342-1351`（next_reconnect_delay cap+归零）；`ProjectionStore.swift:164-181`（函数实际行号） | §2.2「重连退避基准」行归属拆分；§3.1 S-1b 行号修正；§4 E-5 行号修正 | 三处锚点精度修正，命题本身不变 |
| （无 F-ID，设计师复核发现）chunk ack 缺 `SegmentID` | 并入 S-3 + 新增 E-12（超出 r1 意见范围，**r2 评审已确认采纳**） | MacBridge `stream.go:390-407`（ack 构造无 SegmentID）、`:339`（逐 chunk ack）、`envelope.go:45`（字段已备）；官方 `protocol.rs:114-119`（协议文档要求 chunk ack 携带 segment_id）、`websocket.rs:123`（缺省取 MAX）、`segment.rs:445-467`（chunk 共享 seq） | §2.2 新行；§2.3 补一句；§3.3 新子项；§4 E-12；§5 S-3/S-7 依赖 | 不修则 S-3「缺口即真实丢失」存在已证盲区（首 chunk ack 清整消息缓冲 → host 腿中断丢消息尾部且不产生消息级缺口），R-4「不静默丢失」不成立；修复为官方文档对齐的一字段补齐，同时是 S-7 补发 ack 的前置。若评审不采纳，S-3 缺口检测须降级声明为「消息级缺口可检测，chunk 尾部丢失不可检测」并回退本行 |

## 8. 勘误记录（v1.3，通过后纠错，2026-09-26）

依据 plan-contract「通过后纠错」：r2 通过（APPROVED，blockers 0，`docs/…-review-r2.md`）后，元审核定向复核（`docs/…-review-r2-meta.md`，confirm=false）发现 r2 **报告**来源清单两项程序性缺陷；方案自身 §2.1 存在同源缺陷，本轮一并纠正。设计内容（规则/锚点/门控/验收）零改动——除 4 处评审状态注记同步（见下「状态同步」），不构成新版本送审；字节变化记录新哈希（见交接块）。

| 项 | 原错误 | 来源 | 旧身份 → 新身份 | 修改范围及影响 |
| --- | --- | --- | --- | --- |
| 勘误-1 | 方案 §2.1 来源表未枚举工作树、未做同分支族解析：只记录 iOS main 工作树（bd46169），未记录同族配套工作树 `cordcode-ios-native-message-timeline` 的存在与处置——违反 AGENTS.md P0「存在多个工作树时，必须先按 P0 来源门解析同一功能分支族，再读取源码或形成结论」（r2 报告来源清单有同一缺陷，为其问题 1） | r2-meta §3 问题 1（本轮设计师亲核复核：工作树枚举、配套树身份、四锚点文件字节一致性全部重跑证实） | 旧：§2.1 三行表（iOS = main @ bd46169，无枚举、无配套记录）→ 新：§2.1 工作树枚举（两仓各 3 树）+ 分支族解析 + 配套树记录（feat/ios-native-message-timeline @ 8eb262da；元审核采样时为 f4a81ee4，勘误采样时已前进，f4a81ee4 为其祖先）+ 锚点核验来源指定记录（简报 ③ = main @ fe421cdc） | 仅来源清单。**iOS 锚点结论未受污染**：四个锚点文件在 bd46169/fe421cdc/f4a81ee4/8eb262da 全部候选 ref 下字节级零差异（本轮亲跑四组 `git diff --stat` 均为空），r2 APPROVED 的锚点依据在任意候选来源下成立——与 r2-meta 缓解事实一致并扩展到当前配套树 HEAD |
| 勘误-2 | §2.1 缺 P0 强制模板字段「任务预期分支」（iOS 侧未记录任务预期的工作树/分支）与「预期产品特性」（未记录亦未标注 N/A 理由）（r2 报告有同一缺陷，为其问题 2，低严重度） | r2-meta §3 问题 2 | 旧：五列来源表无此二字段 → 新：表增「任务预期分支」「预期产品特性」两列；「预期产品特性」按纯设计文档记 N/A + 理由（无构建/安装场景，预期行为 = §1 R-1~R-5） | 仅来源清单，无命题影响 |

**未夹带说明**：r2 的 4 条建议级意见（R2-A1 缺口检测游标推进与 thread 归属语义、R2-A2 S-7 依赖声明语序、R2-A3 S-7 解除断言、R2-A4 两处行号精度）**不在本轮勘误处理**——按 plan-contract「通过后纠错」第 4 条，设计类改动不得夹带进勘误（R2-A4 虽属锚点精度类，为保持勘误 diff 单一可审计亦未并入）；它们不构成通过前置条件（r2 §8），待 owner 排期后可作为 v1.4 修订或实施期处理。

**状态同步（非设计改动，一并记录以保 diff 可审计）**：另将 4 处已过时的评审状态注记同步为 r2 结果——§2.2「chunk ack 形状」行、§3.3 ack 子项、§4 E-12 的「待评审确认」→「r2 已确认采纳」；§6 下一阶段入口由「待 r2 复审」改为「r2 通过 + 勘误收口后的 owner 排期/实施授权入口」。不改变任何规则、锚点、门控或验收内容。

**受影响命题与结论**：本勘误不改变任何 E/OD/S/R 命题、门控关系与验收标准；r2 的 APPROVED 结论所依据的锚点证据经本轮在全部候选 iOS 来源下复核仍然成立。仍待满足的实施门不变：OD-1、OD-3、E-9、E-10。

---

## 设计师交接块

```
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_version: v1.3
plan_sha256: 5ab5f9208d362788d6685d4e6196475b0ce954ffb5fc348935e3119e8377cd1b  # 正文哈希：§1 至 §8 勘误记录末行（不含本交接块与其前 --- 分隔线）；复算：head -n 265 <plan_path> | shasum -a 256。v1.2 正文哈希（§1~§7 末行）= 0fe1511d…（r2 评审对象，已被本勘误替代，更正关系见 §8）
scope: full
open_gates: [OD-1, OD-3, E-9, E-10]
brief_attached: true
contract_version: plan-contract-v1.1
review_round: r2 通过（APPROVED，blockers 0）→ r2-meta 元审核 confirm=false（来源清单两项程序性缺陷）→ v1.3 通过后纠错勘误轮（§2.1 来源清单补全 + §8 勘误记录 + 4 处评审状态注记同步，规则/锚点/门控/验收零改动）；R2-A1~A4 建议级意见未夹带，待 owner 排期（r2 §8）
```
