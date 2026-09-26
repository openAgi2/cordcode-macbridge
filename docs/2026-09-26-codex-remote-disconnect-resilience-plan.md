# codex-remote 断线韧性与恢复专项方案

- 日期：2026-09-26
- 状态：v1（首次送审，未实施）
- 本轮改动范围：全新方案文档；**只设计，不实施**。owner 已授权写方案，未授权任何代码改动。
- 前置方案：`docs/2026-08-26-codex-remote-backend-implementation-plan.md`（owner 撰写，r2 评审通过；其 §6.2/Gate P0 的韧性相关要求本方案逐条对账）。

## 1. 摘要与范围

### 现状问题（owner 原话）

> 「codex-remote 对网络要求太高，网络有一点波动，就是无法加载会话投影。」

复盘证据（think.md，两仓）：
- macbridge `think.md:2-6`（2026-09-26）：Desktop 26.924 升级打断 codex-remote，iPhone 报「加载失败：codex-remote: stream closed」+「已配对，等待 ChatGPT Desktop」横幅；`watchBinding` 把所有恢复错误折叠成笼统文案。
- cordcode-ios `think.md:432`（2026-09-20）：Mac 端 codex-remote 自 11:36 起病态（thread/list 每轮 12s 超时、stream idle/closed、pairing 流反复重连），**至 14:00 未恢复**——驱动长时间不自愈的实测。
- macbridge `think.md:421`（2026-09-14）：error 通知风暴期间 live catalog attach failed 318 次，iOS 间歇卡「执行中」。

### 目标行为（R）

- **R-1** 网络瞬断（秒级~分钟级）期间与恢复后，iOS 打开 codex-remote 会话不再出现不可重试的硬错误「无法加载会话投影」；瞬态失败进入自动重试，驱动恢复后自动收敛。
- **R-2** 断线窗口内终结的 turn，重连后自动收口（不再永久停在 running、不依赖用户手动重开会话触发冷校准）。
- **R-3** 长连接期间 controller token 到期前主动续期，消除「到期→连接死亡→走完整重连」的额外不可用窗口。
- **R-4** 入站事件缺口可检测（per-stream seq 单调性），缺口触发受影响会话的权威对账，不静默丢失。
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
4. 入为 seq 缺口在桥日志可查，并触发对账；无缺口时零开销。

## 2. 来源与现状调查

### 2.1 来源表

| 仓库 | 工作树路径 | 分支 | 提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| cordcode-macbridge | /Users/jacklee/Projects/cordcode-macbridge | main | 715104c64645d768d203241771cff7c7550ed9a2 | 干净 |
| cordcode-ios | /Users/jacklee/Projects/cordcode-ios | main | bd46169931a35153593313876a2739cff6cffde1 | 干净（另有 remote-web 推送方案等无关未跟踪 docs，不属本方案范围） |
| openai/codex（官方） | /Users/jacklee/Projects/codex | FETCH_HEAD（origin/main 最新） | e72da2b53805894878023d01949a25a082e0a5cb | 只读 checkout，本地 main 落后，结论一律以 FETCH_HEAD 为准 |

**污染声明**：`docs/2026-09-24-codex-remote-session-list-auto-retry.md` 内残留历史评审轮次的 "Kimi" 命名污染（该文档 F-13 已自证「无 Kimi 系 backend，r1–r3 评审证据均误」，但 F-3/§120/§416 仍有残留）。本方案引用该文档时只取其经本轮亲核的锚点（`catalog_recent_view.go`、`RuntimeManager.swift:205`），不扩散污染命名。本方案全文使用真实命名：codex-remote / ChatGPT Desktop / codex CLI。

### 2.2 复用调查表

| 能力/需求 | 官方或既有实现与锚点 | 选择 | 必要改动/无法复用的证据 |
| --- | --- | --- | --- |
| 重连退避形状（1s 基准→30s 封顶、封顶归零、±jitter） | 官方 host：`app-server-transport/src/transport/remote_control/websocket.rs:78`（cap 30s）、`:1343-1345`（cap 归零）；MacBridge `agent/codex-remote/backoff.go:15-56` | **复用现状** | 已逐常量对齐，不改 |
| 判活超时（入站静默 60s） | 官方 pong timeout 60s `websocket.rs:74-75`；MacBridge `streamIdleLimit` 60s `ws.go:318` | **复用现状** | 已对齐 |
| controller 腿 cursor 事件重放 | 官方 cursor 机制是 **host↔relay 腿专用**：host 记录 client envelope 回传的 cursor（`websocket.rs:201-207`），host 重连 relay 时经 `x-codex-subscribe-cursor` header 续传缓冲的 client 消息（`websocket.rs:1275-1330`）；controller 收不到 cursor（owner attempt-008，plan:588-599） | **不实现** | E-1。MacBridge `stream.go:458-466` 的 `SubscribeCursorHeader` 为死代码，注释升级为明确「不实现」决定（S-5） |
| 官方客户端自动重连 | `app-server-client/src/remote.rs:181-632`：connect/notify/next_event/shutdown，**无任何 reconnect API** | **自建维持** | 重连必须调用方自建；MacBridge `watchBinding`/`restoreOnce`（`pairing_persist.go:250-296`）已是正确形态，本方案优化其补偿行为而非替换 |
| turn 权威状态源（对账用） | `thread/turns/list` 摘要页：MacBridge `ReadColdHistory`/`mapColdPage` 已实现分页冷校准（`history_paginated.go:830-941`） | **复用** | S-2 把同一拉取路径用于重连后对账，不新写协议调用 |
| iOS 自动重试 loop | `ProjectionStore.swift:164-180` 可重试 code 白名单 + `.retryable` → 灾难重试 1s→30s（`ChatViewModel.swift:438-490`） | **复用+扩展** | S-1b 接上 wire `retryable` 标志消费 |
| wire `retryable`/`retryAfterMillis` 字段 | 桥已发送：`handlers_projection.go:176-181`（`WireError{Retryable, RetryAfterMillis, Attempts}`）；iOS 已解码：`CCCodeBridgeModels.swift:314-331`（`CCCodeBridgeError.retryable/retryAfterMillis/attempts`） | **复用** | 两头已备、中间未接：iOS `isRetryablePullError` 只看 code 白名单（`ProjectionStore.swift:887-900`），标志被忽略——S-1 的核心事实 |
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
- 恢复后链路仍抖（mid-hydrate RPC/传输失败）→ 同一 `hydrate_failed` 硬错误路径 ❌（E-5——owner 体感的主要来源）
- 已 Ready 会话的增量拉取（`delta_at_head`，`:210-226`）不经 driver → 断线期间显示陈旧但可用的投影（诚实边界：无 stale 标记，R-5 只做桥内可见性，不改此行为）
- 会话列表：`thread/list` 8s 超时 → 列表加载失败（已有 30s 预算重试 + iOS 2s/4s/6s 自动重试，2026-09-24 方案已交付，不在本方案范围）

**断线窗口事件丢失（无补偿）**：无 cursor 重放（E-1）、无入站 seq 缺口检测（`stream.go:273-345` 不校验）、无重连后 turn 对账——`turnByThread` 跨重连保留（`codec.go:112-126`）但断线窗口内的 `turn/completed` 永不重放，live 投影停在 running，唯一收口途径是用户重开会话触发冷校准；桥重启时的孤儿收口 `RecoverOrphanDetailLoadingV2`（`projection_kernel.go:1115-1123`）只管 detail-loading 且只在进程重启后。

## 3. 设计与决定

### 3.1 S-1 恢复窗口拉取语义（跨仓，桥先行）

**S-1a（桥）终态/瞬态分类**：`markHydrateFailed` 的 `retryable` 参数目前对 `source_inspection_failed` 一律 `true`（`handlers_projection.go:603-605`），把「pairing revoked/无身份」和「恢复中/传输抖动」混为一类。改为按错误源分类：
- `ErrNotConfigured`（无持久化身份或 phase=failed，`agent.go:110-119`）→ `retryable=false`（终态，iOS 显示硬错误，保留「请重新配对」语义）；
- 其余（`ErrRestoreInProgress` 已单独走 hydrating；传输/超时/中断类）→ `retryable=true` + 沿用既有 `RetryAt` 同 revision 退避（2026-09-13 think.md 修复已建立）。

**S-1b（iOS）接上标志消费**：`isRetryablePullError`（`ProjectionStore.swift:163-180`）扩展为——`CCCodeBridgeError.retryable == true` 时返回可重试（尊重 `retryAfterMillis` 作为灾难 loop 的初始等待），code 白名单保留为无标志时的兼容路径（老桥/其他 backend）。revoked 因 S-1a 已是 `retryable=false`，不会被自动重试掩盖。

**数据流不变量**：不新增错误面、不改投影内容语义；只把「桥已判定的可重试性」如实传导到 iOS 的既有重试 loop。SSV2 无新增 writer。

### 3.2 S-2 重连后 turn 对账

`BindClient` 全部 attach 完成后，对 `turnByThread` 非空的 thread（断线时有 in-flight turn 的小集合）逐个重拉权威摘要页——**复用** `ReadColdHistory` 的 `thread/turns/list` desc 首页路径（`history_paginated.go:871-941`）——经 Projection Kernel 既有 hydrate 事务域提交：
- 摘要显示 turn 已终结 → 收口该 turn（completed/failed 按权威 status）；
- 摘要显示仍在跑 → 维持 running（不猜完成，SSV2 规则 7）；
- 对账失败 → 记日志，下一轮 3s `AttachLiveCatalog` 周期（`go-bridge/main.go:1046-1067`）重试，不新增定时器。

事务域归属：对账是同一 Kernel 的 hydrate 域事务（SSV2 规则 5），经既有 fence 串行化；re-observed items 幂等（`codec.go:118-124` 注释已有先例：重连后重观察事件 idempotent）。

### 3.3 S-3 入站 seq 缺口检测

`readLoop` 按 per-stream 维护入站 `seq_id` 单调性（owner 方案 §6.2 plan:496 的 per-stream 单调语义；MacBridge 出站已做 `stream.go:90-113`，入站补齐）。缺口 → 记录（stream、缺口范围、关联 thread）→ 触发 S-2 对账。不重传、不断线、不伪造事件——fail-visible 而非 fail-closed。

### 3.4 S-4 ctrl token 到期前主动刷新

用已持久化的 `ctrlExp`（`pairing_persist.go:23-24,60-61`）调度到期前刷新：长连接期间到期前 T 秒执行 `refreshControlToken`，成功则原地更新持久化 token（连接不断）；失败沿用现有路径（连接死亡→重连时强制 refresh），不新增死亡路径。**T 的取值受 owner 方案约束**（plan:508-509「未取样前不得写死提前量或退避」）：实现为常量但被 E-9 fixture 门住——E-9 未满足前 S-4 不得实施。

### 3.5 S-5 常量与死代码对齐

- `ReassembledMessageMaxBytes` 1GB → 100MB（对齐官方 `segment.rs:21`）；
- `SubscribeCursorHeader`（`stream.go:458-466`）注释升级为明确决定记录：「官方不向 controller 交付 cursor（E-1），本仓决定不实现 controller 腿重放；`RecordedCursor` 保留为观测」。

### 3.6 S-6 driver 状态暴露（桥内）

- `watchBinding`/`markOffline` 保留真实错误类别（2026-09-26 think.md 教训：升级断裂被折叠成「已配对，等待 ChatGPT Desktop」），日志与 descriptor readiness 携带可区分类别（offline / restoring / revoked / env-missing）；
- phase 转换以桥内日志 + 既有 `StructuredInstanceReadiness` 查询面（`diagnostics.go:19-35`）暴露，为后续 `backend_status_changed` 推送立项预留数据源；**不新增跨仓协议**。

### 3.7 OD 表

| ID | 问题 | 互斥选项/推荐 | 状态/决定来源 | 影响切片 | 各选项验收 |
| --- | --- | --- | --- | --- | --- |
| OD-1 | 瞬态拉取失败的重试语义：iOS 消费 wire `retryable` 标志（推荐）vs 仅扩 code 白名单 | 推荐**消费标志**：字段两头已备（§2.2），桥分类先行可精确区分 revoked 与瞬态；白名单方案无法区分同 code 下的终态/瞬态（revoked 与传输失败同为 `source_inspection_failed`） | pending（改变用户可见错误行为，owner 确认） | S-1a/S-1b | 标志方案：瞬态自动重试+revoked 硬错误（§1 验收 1/3）；白名单方案：无法满足验收 3，需另设 code 拆分 |
| OD-2 | `backend_status_changed` 推送是否并入本方案 | 不并入，本方案仅 S-6 桥内钩子 | **decided**（macbridge think.md:45，2026-09-26 裁决另立） | S-6 | 推送立项时可直接消费 S-6 暴露面 |
| OD-3 | S-4 提前量 T 的默认值 | 待 E-9 fixture（真机 ctrlExp 分布）后定；候选区间 30–120s | pending（E-9 门） | S-4 | E-9 满足后 T 进断言；未满足则 S-4 整体不实施 |

## 4. 证据与实施前置门

| ID | 待证明命题 | 证据类别/来源 | 断言与锚点 | 状态 | 未满足时阻塞 |
| --- | --- | --- | --- | --- | --- |
| E-1 | 官方不向 controller 提供 cursor 事件重放；cursor 是 host↔relay 腿机制 | 源码事实（官方 FETCH_HEAD）+ owner 实测 | `websocket.rs:201-207`（host 记录 client 回传 cursor）、`:1275-1330`（host 重连带 header）；attempt-008（plan:588-599）；MacBridge `stream.go:459-460` 注释「live target never delivered one」 | **verified** | 非目标成立性；若未来官方新增 controller cursor，S-5 注释与「不做重放」决定需复审 |
| E-2 | 官方 RemoteAppServerClient 无自动重连 | 源码事实 | `remote.rs:181-632` API 面无 reconnect/resubscribe | **verified** | 「自建维持」选择成立 |
| E-3 | iOS 只认 code 白名单；wire retryable 已解码未消费 | 源码事实（两仓） | `ProjectionStore.swift:887-900`（只读 code）；`CCCodeBridgeModels.swift:314-331`（retryable 已解码）；`handlers_projection.go:176-181`（桥已发送） | **verified** | S-1b |
| E-4 | 瞬态 offline 冷开已走可重试 hydrating | 源码事实 | `agent.go:103-135` + `handlers_projection.go:864-871, 152-156` | **verified** | S-1 范围界定（只补 mid-hydrate 缺口） |
| E-5 | mid-hydrate/终态失败 → `hydrate_failed` 硬错误（iOS 白名单缺） | 源码事实 | `handlers_projection.go:599-606` + `ProjectionStore.swift:163-180` 白名单不含 | **verified** | S-1a/S-1b |
| E-6 | 重连后无 turn 对账；断线窗口完成的 turn 停在 running | 源码事实 | `session.go:161-212`（BindClient 只订阅+baseline）；`codec.go:112-126`（turnByThread 保留）；孤儿收口仅桥重启（`projection_kernel.go:1115-1123`） | **verified** | S-2 |
| E-7 | ctrlExp 持久化但无到期前刷新调度 | 源码事实 | `pairing_persist.go:23-24,60-61,167,186-191`；全仓 grep 无调度调用 | **verified** | S-4 |
| E-8 | 常量漂移：重组上限官方 100MB vs 本仓 1GB；backoff/pong 已对齐 | 源码事实 | `segment.rs:21` vs `envelope.go:16`；`websocket.rs:74-79` vs `backoff.go:15-56`/`ws.go:318` | **verified** | S-5 |
| E-9 | ctrl token 实际有效期分布与刷新时序（fixture） | 原始运行证据 | 计划路径：真机抓 enroll/refresh 响应的 `ctrlExp`（脱敏，不记 token 值），≥5 个样本覆盖典型会话 | **pending**（实施期捕获） | **S-4 整体**（OD-3 依赖） |
| E-10 | 受控断线复现样本（断 relay 10–60s） | 原始运行证据 | 计划路径：真机 + 桥日志对齐一次断/恢复窗口（iOS 拉取行为 + 桥 hydrate 行为 + 对账行为） | **pending**（实施期捕获） | S-1/S-2/S-3 的完成验收门 |

Gate A（证据）作用：S-1/S-2/S-3/S-5 的设计依据全部 verified；S-4 被 E-9 阻塞实施。Gate B（产品决定）：OD-1 pending 阻塞 S-1 实施；OD-2 已决；OD-3 随 E-9。

## 5. 实施切片

| 切片 | 需求/可见结果 | 组件与复用方式 | 依赖/前置 | 最小验证/完成证据 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-1a | revoked 硬错误、瞬态可重试的桥侧分类（R-1 验收 3） | `handlers_projection.go` `markHydrateFailed` 分类；复用 `RetryAt` 退避 | E-5；OD-1 | 定向单测：`ErrNotConfigured` → retryable=false；传输类 → true+RetryAt | 分类失败回退现状（一律 true），不劣化 |
| S-1b | iOS 消费 retryable 标志，瞬态失败自动重试（R-1 验收 1） | `ProjectionStore.swift` `isRetryablePullError` 扩展；复用灾难 loop 与 `retryAfterMillis` | S-1a；OD-1；E-3 | iOS 定向单测：标志 true → `.retryable`；false/无标志 → 白名单路径；真机 E-10 验收 1 | 标志缺失时白名单兜底（兼容老桥） |
| S-2 | 断线窗口完成的 turn 重连后自动收口（R-2） | `session.go` BindClient 后对 `turnByThread` 非空 thread 重拉摘要（复用 `ReadColdHistory` 路径）；Kernel hydrate 域事务提交 | E-6；E-10 验收 2 | 桥定向单测（fake app-server 断线窗口收口）；真机 E-10 | 对账失败记日志，3s 周期重试；不猜完成 |
| S-3 | 入站 seq 缺口检测→触发对账（R-4） | `stream.go` readLoop per-stream 单调性；复用 S-2 对账 | S-2；E-1（缺口真实存在的依据） | 桥定向单测：注入缺口→检测+触发；正常流零告警 | 检测异常时降级为纯日志（不阻断事件流） |
| S-4 | ctrl token 到期前续期（R-3） | `pairing_persist.go` 基于 `ctrlExp` 调度；复用 `refreshControlToken` | **E-9**；OD-3 | 定向单测（fake 时钟）+ 真机长连接观测 | 刷新失败沿用现有重连路径 |
| S-5 | 常量对齐 + 死代码决定记录 | `envelope.go:16` 100MB；`stream.go:458-466` 注释 | E-8 | 常量断言单测 | 无（纯对齐） |
| S-6 | 桥内状态可区分（R-5） | `pairing_persist.go`/`diagnostics.go` 错误类别保留 | 无（独立） | 日志/readiness 定向断言 | 无 |

无数据迁移、无不可逆动作。S-1a→S-1b 有序依赖；S-2→S-3 有序依赖；S-4/S-5/S-6 可独立。

## 6. 验证、风险与交付

**验证分层**（按构建成本纪律，本方案属 D3 状态/协议边缘）：
- 方案阶段已验证：E-1~E-8（源码事实，锚点见 §4）。
- 实施期：定向单测（桥：对账事务/seq 检测/token 调度/错误分类；iOS：retryable 消费）+ 各自定向 build + 交付前一次真机安装。
- 端到端验收（E-10）：owner 测试矩阵（受控断线场景，§1 验收 1–4 逐项），agent 不以单测冒充。

**主要风险与对策**：
1. S-2 对账与 live 事件竞态 → 走 Kernel 既有 fence/事务域（SSV2 规则 5），re-observed 幂等；单测覆盖「对账提交与 live delta 并发」负例。
2. S-1 扩大重试掩盖真错误 → S-1a 分类先行（revoked 恒硬错误）；白名单保留兜底；E-10 验收 3。
3. S-3 seq 语义误报（官方 host 是否保证入站连续未取样）→ 检测只触发对账+日志，不断线不重传；E-10 观察正常流是否零误报，误报则收紧判定条件。
4. S-4 提前量拍脑袋 → E-9 fixture 门整体阻塞切片。

**阻塞清单**：OD-1（owner 确认重试语义）、E-9（token fixture）、E-10（断线复现，实施期）。

**下一阶段入口**：评审员对本方案独立评审（plan-review）；通过后按切片实施，S-1a/S-1b/S-2/S-3 为主链，S-4 视 E-9，S-5/S-6 随批。
