# MacBridge Catalog 周期性扫描、Codex Discovery 超时、Passive Error 风暴与 Projection 冷打开饥饿 — 执行方案

Date: 2026-09-12
Author: 诊断会话（CPU 归因排查衍生；2026-09-12 两轮复审修订）
Status: implementing（exec-plan 队列执行中；实现与非 UI 验证已落地，真机回归待收口）

关联：

- 起因排查记录：`.workbuddy-ai/memory/2026-09-11.md` §「排查：ChatGPT.app CPU 是否由 macbridge 引起」
- 同类历史方案：`docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan.md`（Claude list 路径每行解析 transcript 导致 ~100% CPU）
- 涉及代码：`go-bridge/session_discovery.go`、`go-bridge/catalog_native_membership.go`、`go-bridge/catalog_workspace_filter.go`、`go-bridge/main.go`、`go-bridge/background_tasks.go`、`go-bridge/handlers_projection.go`、`agent/codex-remote/*`
- 复审现场日志：`~/Library/Application Support/CordCode Link/logs/go-bridge.log*`（机器本地、易轮转；关键数据已摘录到 §1.5–§1.7）
- Projection 复审样本 transcript：`~/.codex/sessions/2026/09/12/rollout-2026-09-12T13-22-03-01a09410-c870-7eb0-8fec-24abebcc93df.jsonl`（只记录结构计数，不复制正文）

## 0. 背景

排查「ChatGPT App CPU 高是否由 macbridge 引起」时，用 `SIGSTOP`/`SIGCONT` A/B 实验反证了因果关系（暂停 bridge 后 ChatGPT 侧 CPU 8.1% → 7.6%，无变化）。但排查过程中在 bridge 自身侧发现真实缺陷；2026-09-12 两轮复审后，问题拆成四类，不能再用一条因果链混述：

1. **codex-remote discovery 超时导致推送长时间静默**：全量 `thread/list` 耗时贴近 8s timeout；失败期间 `seen[id]` 保留 last-good，`sessions_changed` 不广播。这个语义本身正确，但连续失败时缺少可观测的 liveness 状态。
2. **Grok fast poll 的无变化空转**：只要 broadcaster 有连接，`grokDiscoveryFastInterval` 每 5s 全量 membership + workspace filter + INFO 日志。它受“是否有客户端连接”门控，**并不依赖 codex-remote 推送是否失效**。
3. **codex-remote passive transport error 风暴**：复审现场观测到同一 `__remote_control_transport__` error 每秒数千条，同步伴随 bridge CPU 与日志写入飙升。这是当前 CPU 归因里更强的候选主因，必须在修 catalog cadence 前先处理。
4. **无 workflow session 的 background tasks 全历史扫描**：`background_tasks.list` 只在 projection 中已经出现 workflow 时走 fast path；普通聊天/笑话 session 因 `hasWorkflow=false` 反复落入“全部 turn summary + 每个 turn items/list 到 EOF”的 N+1 扫描。重复扫描挤占同一 Remote Control 通道，导致 projection 冷打开的 `thread/settings/get` 超时，iOS 反复看到 `projection.hydrating / projection.hydrate_failed`。

原因果链中「codex-remote 推送失效 → iOS 只能轮询 → Grok 5s 扫描」目前 **证据不足**：Grok fast poll 只要任意客户端连接就会运行；设备断开后退化为 60s 只证明其受 `HasConnections()` 门控，不能证明由 codex-remote 推送失效诱发。`sessions_changed` 在代码注释中也是 latency win，不是 correctness gap；iOS 是否因该推送失效改变轮询频率，需要请求日志证明。

bridge CPU 实测为**突发型**：历史采样中 iOS 在线约 32% of 1 core、安静时约 3.7%；复审期间 passive error 风暴下 10s 平均约 25%–50% of 1 core。Projection 复审还证明一个 406KB / 10 turn 的小 session 可被重复扫描拖到 2 分 33 秒才失败。目标不是 blindly 调 cadence，而是先完成 CPU/event attribution，再消除无意义工作、恢复推送与冷打开吞吐，并让真实错误以可聚合方式可见。

## 1. 实测证据（2026-09-11 21:26 启动的运行时，PID 25038）

### 1.1 各 backend 一次 discovery 采样的耗时（`session discovery snapshot seeded`）

| backend | sessionCount | durationMs |
|---|---|---|
| opencode-web | 172 | 75 |
| dsh-web | 23 | 165 |
| grokbuild | 19 | **406** |
| claude | 204 | 1768 |
| codex-remote | **440** | **7208** |

`codex-remote` 一次全量 thread/list 7.2s，而 `go-bridge/catalog_native_membership.go:15` 的 `catalogRequestTimeout = 8 * time.Second` 只留 792ms（11%）余量。

### 1.2 超时与退避

```
level=WARN msg="go-bridge: session discovery fingerprint error (no broadcast)"
  phase=poll backend=codex-remote durationMs=8000
  error="codex-remote: request thread/list canceled: context deadline exceeded"     × 17
level=WARN msg="go-bridge: Codex discovery authoritative refresh backed off"
  backend=codex-remote trigger=safety-poll retryDelay=2m0s                          × 16
```

### 1.3 推送路径事实上失效

```
grep -c "sessions_changed (catalog fingerprint changed)"  →  2
  2026-09-11T23:43:11.458  backend=codex-remote catalogGeneration=1
  2026-09-11T23:43:37.936  backend=codex-remote catalogGeneration=2
```

`go-bridge/session_discovery.go:320-322` 在 fingerprint 出错时**刻意不更新 `seen[id]`**（保留 last-good，避免误广播）——这个设计本身是对的，但在「上游持续超时」时会让 `sessions_changed` 永久静默。

### 1.4 周期性全量扫描（客户端在线时）

`catalog workspace filter dropped sessions` 在 23:53:00–23:56:00 三分钟窗口出现 **39 次**（10 分钟窗口 89 次），节拍稳定：

```
23:53:03.845  23:53:08.859  23:53:13.860  23:53:18.860  23:53:23.866
23:53:28.860  23:53:33.839  23:53:33.849  23:53:38.854  23:53:43.879
```

- 每 5.0s 一次，`raw_count=30 kept_count=19 dropped_count=11` **每次完全相同**（无变化也照跑、照打日志）。
- `23:53:33.839` 与 `23:53:33.849` 成对出现（间隔 ~15ms）→ 存在**第二个 30s 周期的调用者**。
- **设备断开后立即退化为 60s**：`00:00:08` 之后变为 `00:00:33 / 00:01:33 / 00:02:33 / 00:03:33 / 00:04:33`。这精确匹配 `session_discovery.go:186-188` 的 `if !h.broadcaster.HasConnections() { continue }` 门控。

按 5s 节拍与 `grokDiscoveryFastInterval = 5 * time.Second`（`session_discovery.go:58`）对应；日志中 `dropped_basenames` 含 `cordcode-macbridge-grokbuild-leader` 等 Grok 会话标题，且该行 `codex_roots=0 codex_roots_enforced=false` 正是 `filterSessionsMissingWorkspace`（传 nil roots）的签名。

其它周期性工作（同一窗口）：

- Claude 每 30s 全量重算 fork 谱系：`claude session fork detected` ×8 + `claude compact continuation detected` ×2，时间戳成簇（`23:54:35.365`、`23:55:35.391`）。
- opencode-web：`endpoint probe ok` 每 ~15s（603 次/10 分钟）、`home project list` 每 30s。

### 1.5 复审新增：passive error 风暴与 CPU 归因（2026-09-12 17:04）

当前运行时 PID 64465 的 `go-bridge.log` 出现重复：

```
level=INFO msg="go-bridge: passive event"
  backend=codex-remote session=__remote_control_transport__ event=error
```

实测：

- 16:55–17:04 当前日志累计 **160 万+** 条同一 transport error；
- 17:04:45–17:04:53 的 5 个 2s 窗口内，分别约 **3524/6564/6023/6478/7787 条**（约 1760–3884 条/s）；
- 同窗口 bridge CPU 约 **25.4%–50.1% of 1 core**；
- 同窗口日志增量约 **0.52–1.15 MB/s**；
- 当前日志 191MB，轮转档约 351MB / 262MB / 2.1GB。

代码路径：`go-bridge/main.go:898-906` 将每个 `error` passive event 按 INFO 打日志；`agent/codex-remote` notification pump 又会把 decoded event 分发给 passive observer。现有日志只记录 backend/session/event 名，不含原始错误消息、notification method、connection/stream epoch，因此还不能判断上游为什么重复发送。

结论与边界：

- 这不是“把日志降级就完事”的问题；重复 error 本身必须归因。
- 也不能只修 catalog cadence：每 5s 一次 Grok filter 与每秒数千条 passive event 相比，后者对 CPU/日志吞吐的量级更强。
- wall duration（7.2s `thread/list`、406ms Grok 等）不等于 on-CPU 时间；discovery 超时方案不能单独作为 CPU 占用结论。

### 1.6 复审新增：无 workflow session 的 projection 冷打开饥饿（2026-09-12 16:50–17:00）

用户在 iOS CordCode 打开一个只有若干笑话的 Codex Desktop session，出现数分钟加载与多次「会话投影未加载」后成功。定位样本：

- session：`01a09410-c870-7eb0-8fec-24abebcc93df`
- 本地 transcript：**406KB / 278 行 / 10 turns**
- 结论：这不是 transcript 体量问题。

### 1.6.1 同一 session 被重复全量 item 扫描

`codex-remote: turn items metrics` 统计：

| 日志窗口 | turn item fetch 次数 | distinct turns | 平均耗时 | 记录到的上游耗时累计 |
|---|---:|---:|---:|---:|
| 16:51–16:53（`go-bridge.log.1`） | 101 | 8 | 1293ms | 130.6s |
| 16:55–17:00（`go-bridge.log`） | 103 | 10 | 663ms | 68.2s |
| 合计 | **204** | **10** | — | **约 198.8s** |

同一 turn 平均被重复 fetch 约 **20 次**。日志还能看到并发交错，例如：

```text
16:51:33.201  turn 01a09410…  elapsed 684ms
16:51:33.226  turn 01a09410…  elapsed 652ms
```

相隔 25ms 的同一 turn 请求各自耗时 600ms+，证明不是一次串行扫描，而是重复/并发扫描。

### 1.6.2 代码链路

1. `go-bridge/background_tasks.go:308-323`：`codex-remote` 只有在 `projectionBackgroundTasks(...)` 找到 workflow part 时才使用 projection fast path。
2. 该笑话 session 没有 workflow，`hasWorkflow=false`，因此每次 fallback 到 `ListSessionBackgroundTasks()`。
3. `agent/codex-remote/background_tasks.go:15-20` 调 `GetTurnScopedRichHistory(ctx, sessionID, 0)`；`limit=0` 表示不限制 turn window。
4. `agent/codex-remote/history_paginated.go:729-748` 按权威 history mode 分发；paginated thread 进入 `turnScopedHistoryPaginated()`。
5. `history_paginated.go:774-819` 的语义是：拉取所有 turn summary pages 到 EOF，再对 **每一个 turn** 调 `ReadTurnItems()` 并拉 items cursor 到 EOF；任一失败整体失败。

也就是：

```text
thread metadata
→ all thread/turns/list pages
→ for each turn:
     thread/items/list to EOF
```

这是 session 内 N+1 全量扫描。`go-bridge/background_tasks.go:320` 还使用 `context.Background()`，客户端取消/重试不会自然取消旧扫描。

### 1.6.3 projection 被同一 Remote 通道上的扫描饿死

坏窗口时间线：

```text
16:50:59.549  get_session_projection 进入
16:51:00.444  background_tasks.list 进入
16:51:00 起   大量 turn items/list 请求
16:51:59.549  thread/settings/get 超时 60s
16:52:14.551  projection 返回 hydrating
```

第一个 projection 请求等了 **75 秒** 才返回 `projection.hydrating`。75s 的结构来源：

- Remote RPC 默认 timeout 60s（`agent/codex-appserver/rpc/client.go`）；
- projection 前台 hydrate budget 15s（`go-bridge/handlers_projection.go:28-38`）。

后续重试：

```text
16:51:30.465 → 16:51:46.666  hydrating
16:51:47.900 → 16:52:02.901  hydrating
16:52:05.157 → 16:52:20.158  hydrating
16:52:21.208 → 16:52:32.520  hydrate_failed
```

从首次请求到最终失败约 **2 分 33 秒**，与用户看到的“数分钟、多次报错”一致。16:51 单分钟内 `background_tasks.list` 出现 **8 次**，每次无 workflow 都可能重新触发同一 session 全历史扫描。

### 1.6.4 passive error 风暴是放大器

`go-bridge.log.1` 中 16:42–16:54 累计约 **249.8 万条**重复 transport error；16:51 / 16:52 分别约 **28.5 万 / 31.2 万条**。它推高 CPU 与日志 I/O，并放大 Remote RPC 延迟：坏窗口 turn item 平均 1293ms，后续窗口降至 663ms。

完整归因：

```text
passive error 风暴
        ↓ 放大 CPU / 日志 I/O / RPC 延迟
background_tasks.list 对无 workflow session 走全历史 N+1 扫描
        ↓
同一 turn 被重复 fetch ~20 次
        ↓
Remote 通道被 items/list 占满
        ↓
projection cold open 的 thread/settings/get 60s 超时
        ↓
iOS 反复看到 hydrating / hydrate_failed
```

### 1.6.5 另有 pairing 恢复竞态，但不是主因

16:55 运行：

```text
16:55:02.197  token_refreshed
16:55:02.471  iOS get_session_projection
16:55:02.472  立即失败：请先在 Mac 的 CordCode Link 里配对 Codex Desktop
16:55:04.464  stream_bound
16:55:08.529  iOS 重试
16:55:11.182  projection snapshot 成功
```

pairing 正在恢复时请求先到，agent 尚无 client，于是立即返回“未配对”。这解释第一次报错，但不能解释 2–3 分钟反复失败；主因仍是 background task 全扫描 + Remote 通道拥堵。

### 1.6.6 与 catalog `thread/list` 是不同路径

本节问题不是 discovery 的全库 `agent.ListSessions()` / 440 threads / 7.2s。这里是 **单个 session 内部**：

```text
all turns × each turn items/list × repeated background_tasks.list
```

两者都在 codex-remote 上，但代码路径与修复面不同。

### 1.7 已排除或暂缓的假设（避免重复劳动）

- **`ensure-codex-shared-daemon.sh` 不是 ChatGPT CPU 的直接元凶**：该脚本曾是 `while true` + `sleep 0.25`，每秒 4 次 `codex app-server daemon start` + `launchctl setenv`，实测 5 秒内新起 31 个 codex 进程（≈6.2/秒），但脚本自身仅约 0.6% CPU，且暂停它对 ChatGPT 侧 CPU 无显著影响。
- **但不能因此排除它是 codex-remote `thread/list` 变慢的间接诱因**：高频 daemon churn 可能造成 app-server contention。它仍需进入 Phase 0 的只读 A/B；若证明影响 discovery，先另开方案修 churn，再决策 timeout policy。
- **`codex_roots` 白名单未失效**：`usesCodexWorkspaceCatalog`（`handlers_codex_catalog.go:52-58`）对 `codex-remote` 直接返回 false，因此 codex-remote 从不走 roots 白名单路径，`codex_roots=0` 是预期值而非 bug。`~/.codex/.codex-global-state.json` 存在（1.78MB）且含 `electron-saved-workspace-roots` / `local-projects` 字段。

## 2. 目标

1. **先完成 CPU 归因**：区分 passive error 风暴、discovery wall latency、日志写入、上游等待与真实 on-CPU 工作。
2. **归因并止住 passive transport error 风暴**：重复错误必须可见但按状态/窗口聚合，不允许每秒数千条 INFO 淹没日志。
3. **消除无 workflow session 的 background tasks 全历史 N+1 扫描**：同一 session 的重复 `background_tasks.list` 不得反复拉所有 turn items；projection cold open 不得被后台扫描饿死。
4. **恢复 codex-remote 的 `sessions_changed` 推送**：让全量 fingerprint 不再必然超时，或让连续失败期间的推送 liveness 可观测且恢复路径明确。
5. **消除无变化的周期性全量扫描**：无 catalog 变化时不做全量 membership + 逐 session `os.Stat` + 重复 INFO 日志。
6. **保留恢复能力**：60s safety scan、出错不更新 `seen`、`HasConnections()` 门控等既有安全语义**不得削弱**。
7. **不引入掩盖失败的回退**：超时仍是超时，transport error 仍是 error；聚合日志不能吞掉状态变化或新错误；也不得把“尚未确认无 workflow”伪装成空任务成功。

## 3. 阶段与交付物

### Phase 0 — CPU/passive-error 归因（先于一切行为改动）

**0a `transport-error-storm-root-cause`**
在不逐条刷日志的前提下采集重复 error 的关键身份：错误消息、notification method（如协议允许）、connection/stream epoch、thread/turn 身份是否为空、first/last timestamp、count。目标是回答：

- 是上游重复发送同一 error notification？
- 是一次连接关闭被 replay/dispatch 多次？
- 还是 stale stream / stale observer 清理缺失？

**0a+ `identical-error-notification-suppression`**
codex-remote codec 对同一 connection epoch 内 byte-identical 的 terminal error notification 只交付首个事件；重复项在进入 passive pump 前丢弃并计数。error 参数变化不抑制，rebind 清零。该修复针对 2026-09-12 22:25 实测：仅聚合日志后 bridge 仍在 3 分钟内收到 1,084,858 次同一 passive error，CPU 60%–74%。

**0b `passive-event-log-aggregation`**
按 `(backend, session, connection/stream epoch, error identity)` 聚合重复 passive error：状态进入时打 WARN，重复期间按较长周期输出 count/rate，error identity 变化立即打 WARN。禁止无身份的去重导致新错误被吞。日志降噪是可观测性修复，不是把失败伪装成成功。

**0c `cpu-and-throughput-attribution`**
为 bridge 增加低频管理指标（或等价只读 profiling 入口）：passive event count/rate、日志 bytes/s、discovery wall duration、context-cancel/error 分类、goroutine 数。必要时采样 CPU profile。验收不是“日志变少”，而是能回答当前 CPU 时间主要落在 passive dispatch/logging、catalog scan、上游等待、锁竞争还是其它路径。

**0d `daemon-churn-ab-test`**
只读对照 `ensure-codex-shared-daemon.sh` churn 开/关时的 `thread/list` p50/p95、timeout 率与 transport error rate。若证明相关，先另开 daemon 修复方案；本方案不直接改脚本。

**0e `background-task-starvation-attribution`**
为 `background_tasks.list` 增加结构化指标：trigger / sessionId 前缀 / duration / itemRequestCount / distinctTurnCount / outcome / caller cancellation。只记录结构计数与错误码，不记录 prompt、回答或完整路径。验收要能把 §1.6 的 204 次 item fetch 归因到具体请求序列，并验证 single-flight / 取消 / 优先级修复的效果。

### Phase 1 — Discovery 量化（不改变行为）

**1a `discovery-timing-instrumentation`**
在 `discoveryFingerprint`（`session_discovery.go:390`）与 `snapshotBackendSession`（`:299`）已有 `duration` 的基础上，输出结构化、可聚合的耗时分布，而非仅 seed 时一行：

- 每次采样记录 `backend` / `phase` / `durationMs` / `sessionCount` / `outcome`（ok|timeout|error）；
- 新增按 backend 的 p50/p95 与超时计数（进程内计数器，随管理端点或周期性日志暴露）。

**1b `upstream-slow-vs-hung-classification`**
区分「上游慢但会返回」与「上游卡死」：`context.DeadlineExceeded`、`context.Canceled`（连接关闭）、RPC error 与其它 error 分开计数。这决定 Phase 2 是「放宽超时」还是「改抓取/通知路径」。

**1c `wall-vs-cpu-bound`**
对 discovery 路径同时记录 wall duration 与进程 CPU 时间（可采样或用测试计数）。7.2s `thread/list` 只说明延迟风险；不能单独推出它消耗 7.2s CPU。

**为什么先做这个**：7.2s vs 8s 的余量只有 11%，在只改了超时值的情况下很容易把问题从「超时」变成「偶发超时」，看起来好转但没解决。必须先知道 p95。

### Phase 2 — 修复 codex-remote 推送与 projection 冷打开路径

**2a `codex-remote-fingerprint-bounded-fetch`**
codex-remote 的 fingerprint 当前走 `agent.ListSessions`（`session_discovery.go:423`）全量 440 会话。评估改用**受限 head 抓取**（复用 `codexDiscoveryHeadLimit = 25` 的思路，或分页取前 N 页）做触发指纹：

- 先检查官方 `CatalogRefreshSignals()` 快路径为什么没有消掉全量风险；`codexRemoteDiscoveryHintInterval = 0` 是因为 Remote Control 已有官方 thread lifecycle / turn-boundary notification，不是简单遗漏，不能为了降低 catalog 成本直接开启周期 head 轮询。
- head 只能作为 trigger，不能替代 authoritative full fingerprint；title/directory/project 变化不在 head 命中面时必须由 full scan 或官方信号覆盖。
- 风险：head-only 会漏「尾部会话更新」。若采用，必须是「head + 定期全量 + 官方信号」组合，并明确漏检窗口，而非纯 head。

**2b `codex-remote-timeout-policy`**
若 Phase 1 证明是全量抓取本身过慢且无法优化：

- 把 `catalogRequestTimeout`（8s）与「全量 catalog 抓取」解耦——list 路径与 discovery 路径的超时上限不必相同；
- 超时退避分级：timeout 用较短退避（如 30s），hard error 用现有 15s→2m 指数退避，避免一次超时把推送静默 2 分钟；
- 但较短退避会提高 7s 级全量请求的调用频率，可能放大 CPU/上游压力；必须结合 Phase 0/1 的 p95 与 CPU 归因决定，不能默认“越短越好”。

**2c `codex-remote-push-liveness`**
在「持续超时导致 `seen` 长期不更新」时**主动暴露**该状态（例如周期性 WARN 升级 + 指标），使「推送已静默 N 分钟」可被观测，而不是只在退避时各打一行。

**2d `codex-remote-background-task-no-workflow-slow-path`**
2026-09-12 22:33 live regression补充：同一 revision 的 full-scan 资源门错误（如单 turn `max_bytes`）不能被 iOS 重试立即重放；错误保留为错误并按 30s 退避，projection revision 变化仍立即重扫。

修复 `projectionBackgroundTasks` fast path 只认 `hasWorkflow=true` 的语义：

- 明确三态：`no-workflow confirmed` / `workflow present` / `projection detail not loaded`；不得把后两者混淆；
- 对已确认无 workflow 的结果做负缓存，按 projection revision / turn metadata / catalog generation 失效；
- 同一 session 的扫描必须 single-flight，重复 `background_tasks.list` 共享一次结果；
- 扫描必须可取消，不能在 projection cold open 期间持续挤占同一 Remote 通道；
- 真路径失败继续 fail closed，不得用空列表假成功掩盖“未知”。

**2e `pairing-restore-projection-race`**
pairing token 已刷新但 stream 尚未 `stream_bound` 时，projection 请求不应立即返回“未配对”硬失败；应返回可重试 hydrating 状态或等待 bounded restore window。只有确实没有持久化 pairing / restore 最终失败时才报未配对。

### Phase 3 — 周期性扫描降本

**3a `grok-fast-poll-cost`**
`grokDiscoveryFastInterval = 5s` 的全量 membership（19 会话耗 406ms）在无变化时纯属浪费。选项（按证据选一，不要同时做）：

- 提高间隔（Grok 无 bounded head，只能靠间隔）；
- 或引入两层指纹：先算便宜的 raw/order trigger，只有变化才跑 authoritative visible filter。注意现有 `listSemanticFingerprint` 输入是 **filter 后** wire；不重构就无法“先知道 fingerprint 变了再 filter”；
- 单次调用内按目录去重 `os.Stat` 不改变跨调用时效，可先独立落地；
- 若 fast poll 跳过 workspace filter，磁盘目录删除在 raw catalog 不变时只能等 60s safety scan 反映；该延迟窗口必须显式验收；
- `grokDiscoveryFastInterval` 是包级 var，测试会缩短，改语义需同步测试。

**3b `catalog-filter-log-noise`**
`catalog_workspace_filter.go:88` 的 INFO 每 5s 打印**内容完全相同**的一行。改为：内容与上次相同时降级为 DEBUG（或按变化打 INFO、无变化按较长周期汇总）。当前 `go-bridge.log` 536KB，轮转档 12–17MB，噪音正在掩盖真实信号。

**3c `catalog-filter-per-session-stat`**
`filterCatalogSessionsByVisibility`（`catalog_workspace_filter.go:43`）对**每个 session** 调 `sessionWorkspaceExistsForCatalog` → `os.Stat`。440 会话即 440 次 stat/次调用。评估按目录去重后 stat（同目录会话共享一次结果）——注意这与「磁盘删除必须立刻反映」的现有约束（`handlers_codex_catalog.go:120-121`）不冲突，同一次调用内去重不影响跨调用时效。

**3d `second-filter-caller-identification`**
定位 30s 周期的第二个 `filterSessionsMissingWorkspace` / `filterCodexCatalogSessions` 调用者（`23:53:33.839` + `.849` 成对），确认是否可与 3a 合并节拍。

**3e `claude-fork-lineage-cadence`**
2026-09-12 22:39 live regression补充：即使活动 Claude transcript 使 fingerprint 每次变化，相同 fork/compact hidden 签名也必须按分钟级汇总，避免 9 行 × 每 3s 的重复 INFO。

Claude 每 30s 全量重算 fork 谱系（`claude session fork detected` ×8/次）。评估增量或按 catalogGeneration 失效缓存，而非无条件全量。

### Phase 4 — 验证与收口

**4a `go-tests`**
单测覆盖：可注入 lister 的超时路径、退避分级、passive error 聚合与 error identity 变化、filter 日志降噪条件、按目录去重的 stat 次数、无 workflow 三态、background task single-flight/负缓存/取消、pairing restore 竞态。现有 seam：`loadCodexWorkspaceRootsFn`、`codexDiscoveryHintInterval` 等包级 var，以及 `go-bridge/transcript_probe.go` 式的 test-only 计数器。

**4b `real-device-regression`**
真机对照（同一 iOS 会话、在线 ≥10 分钟），对比改动前后：

| 指标 | 现状基线（2026-09-11 运行时） |
|---|---|
| `catalog workspace filter dropped sessions` 行数/10 分钟 | 89 |
| `session discovery fingerprint error` 次数 | 17 |
| `Codex discovery authoritative refresh backed off` 次数 | 16 |
| `sessions_changed (catalog fingerprint changed)` 次数 | 2 |
| `cordcode-bridge-runtime` CPU（设备在线，无 passive 风暴） | 3.7%–32% of 1 core（突发） |
| `cordcode-bridge-runtime` CPU（passive error 风暴） | 25%–50% of 1 core |
| `passive event ... __remote_control_transport__ event=error` | 1760–3884 条/s |
| 日志增量（passive error 风暴） | 0.52–1.15 MB/s |
| 复审笑话 session 本地 transcript | 406KB / 278 行 / 10 turns |
| 同一 session turn item fetch / distinct turns | 204 / 10（约 20× 重复） |
| `background_tasks.list` 次数（16:51 单分钟） | 8 |
| projection 首请求 → 最终 hydrate_failed | 约 2m33s |
| projection 首次 hydrating 响应 | 请求后约 75s |
| `codex-remote` seed durationMs | 7208 |

验收必须区分 **storm / quiet / device-online / projection-cold-open** 状态采样，避免把“风暴自然停止”误判成代码收益。最终要求：transport error 有根因结论且重复事件被聚合；`sessions_changed` 能随 catalog 变化正常发出；无变化周期扫描不再产生重复日志/全量工作；无 workflow 小 session 冷打开不再重复全历史 items 扫描，也不被 background task 请求饿死；pairing 恢复窗口不再立即报未配对；bridge CPU 在线稳态下降；**且 iOS 侧会话列表 / 会话详情 / background tasks 行为无回归**。

**4c `doc-sync`**
更新 `GO_BRIDGE_ARCHITECTURE.md`（discovery 边界与不变量）与 `CHANGELOG.md`。

## 4. 约束（沿用项目既有约束）

- 不在未获 owner 明确批准时运行 UI 测试、快照测试、模拟器自动化；默认只用代码阅读、定向 Go 测试、静态检查、聚焦构建。
- 真路径错误必须 fail closed。**不得**新增 fallback / mock / placeholder / cache-snapshot 行为来掩盖上游失败。
- 保持权威身份与状态语义：`sessions_changed` 只由真实 fingerprint 变化驱动；`seen[id]` 出错时不更新这一语义**必须保留**。
- Performance / diagnostic 日志只允许结构计数、耗时、错误码与最小身份前缀，不记录 prompt、回答正文、完整用户路径或凭据。
- 普通 turn-detail 部分必须保持增量流式；只有可变 workflow 卡片延迟到 EOF。
- 任何 iOS 代码改动都必须自动安装到已连接的 iPhone（本方案预期**不需要**改 iOS）。
- 工作区在 2026-09-12 复审时已 clean；若执行时再次出现未提交改动，**必须分开提交**，不要混入本方案。

## 5. 非目标

- 不在本方案中修改 `ensure-codex-shared-daemon.sh`；但保留 Phase 0 只读 A/B。若证明它驱动 `thread/list` 超时或 passive error 风暴，先另开独立修复 plan。
- 不修 `~/.codex/config.toml:284` 的类型错误（`invalid type: map, expected a boolean`）与 codex 共享 daemon 的 `failed to refresh available models`（属上游/配置问题）。
- 不动 iOS 端的轮询策略——推送恢复后轮询自然应当降频，但那是 iOS 侧的独立决策。
- 不删除 60s safety scan。

## 6. 待确认问题

1. `__remote_control_transport__` error 为什么重复：上游重复通知、连接关闭 replay、还是 stale stream/observer 清理缺失？（Phase 0a）
2. 当前 bridge CPU 中 passive dispatch/logging、catalog scan、上游等待各占多少？（Phase 0c/1c）
3. `codex-remote` 全量 thread/list 的 7.2s 是「440 会话的固有成本」还是「上游 app-server 变慢」？需 Phase 1 数据 + daemon churn A/B 定论。
4. iOS 是否确实因 `sessions_changed` 缺失改变轮询/连接行为？需要请求日志证明；在证明前不把 Grok fast poll 归因于 codex-remote 推送失效。
5. 若引入 head trigger，尾部会话更新的漏检窗口能否接受？需要 owner 对「sessions_changed 及时性」的期望值。
6. projection Ready 且 turn detail 尚未加载时，“没有 workflow”能否被权威确认？需要官方协议/上游数据 shape 证明，不能靠空 projection 推断。
7. iOS 为什么在 16:51 单分钟发出 8 次 `background_tasks.list`？是自动刷新、用户重试还是投影失败引发的重试链；服务端仍必须可承受重复请求。
8. `grokbuild` 19 会话耗 406ms 是否正常（其余 backend 同等规模远快于此）？
9. 30s 周期的第二个 filter 调用者是谁（Phase 3d）——在定位前不要动 3a 的节拍。
