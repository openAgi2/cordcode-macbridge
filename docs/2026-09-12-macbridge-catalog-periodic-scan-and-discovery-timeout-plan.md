# MacBridge Catalog 周期性全量扫描与 Codex Discovery 超时 — 执行方案

Date: 2026-09-12
Author: 诊断会话（CPU 归因排查衍生）
Status: proposed（待 owner 确认后进 exec-plan）

关联：

- 起因排查记录：`.workbuddy-ai/memory/2026-09-11.md` §「排查：ChatGPT.app CPU 是否由 macbridge 引起」
- 同类历史方案：`docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan.md`（Claude list 路径每行解析 transcript 导致 ~100% CPU）
- 涉及代码：`go-bridge/session_discovery.go`、`go-bridge/catalog_native_membership.go`、`go-bridge/catalog_workspace_filter.go`、`agent/codex-remote/*`

## 0. 背景

排查「ChatGPT App CPU 高是否由 macbridge 引起」时，用 `SIGSTOP`/`SIGCONT` A/B 实验反证了因果关系（暂停 bridge 后 ChatGPT 侧 CPU 8.1% → 7.6%，无变化）。但排查过程中在 bridge 自身侧发现两组真实缺陷：**周期性全量扫描**与 **codex-remote discovery 超时导致推送路径失效**。

两者不是独立问题，而是一条因果链：

```
codex-remote 全量 thread/list 耗时 7.2s（超时上限 8s）
        ↓ 抖动即超时（实测 17 次）
snapshotBackendSession 出错时不更新 seen[id]
        ↓
fingerprint 永不变化 → sessions_changed 对 codex-remote 事实上不工作（全期仅成功 2 次）
        ↓
iOS 失去推送，只能靠高频轮询兜底
        ↓
客户端在线期间多个 backend 的周期性全量扫描持续空转（grok 每 5s 一次全量 membership + filter + INFO 日志）
```

bridge CPU 实测为**突发型**：iOS 设备在线轮询时约 32% of 1 core，安静时约 3.7%。因此本方案的目标不是「把某个数字降下来」，而是**消除无意义的周期性工作并恢复推送路径**。

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

### 1.5 已排除的假设（避免重复劳动）

- **`ensure-codex-shared-daemon.sh` 不是本次 CPU 元凶**：该脚本是 `while true` + `sleep 0.25`，每秒 4 次 `codex app-server daemon start` + `launchctl setenv`，实测 5 秒内新起 31 个 codex 进程（≈6.2/秒），但脚本自身仅占 0.6% CPU，且 A/B 暂停它对 ChatGPT 侧无影响。属独立缺陷，**不在本方案范围内**（建议另开）。
- **`codex_roots` 白名单未失效**：`usesCodexWorkspaceCatalog`（`handlers_codex_catalog.go:52-58`）对 `codex-remote` 直接返回 false，因此 codex-remote 从不走 roots 白名单路径，`codex_roots=0` 是预期值而非 bug。`~/.codex/.codex-global-state.json` 存在（1.78MB）且含 `electron-saved-workspace-roots` / `local-projects` 字段。

## 2. 目标

1. **恢复 codex-remote 的 `sessions_changed` 推送**：让 440 会话的全量 fingerprint 不再必然超时，或让超时不再永久静默推送。
2. **消除无变化的周期性全量扫描**：无 catalog 变化时不做全量 membership + 逐 session `os.Stat` + INFO 日志。
3. **保留恢复能力**：60s safety scan、出错不更新 `seen`、`HasConnections()` 门控等现有语义**不得削弱**。
4. **不引入掩盖失败的回退**：超时仍是超时，必须可见。

## 3. 阶段与交付物

### Phase 1 — 先量化，再决策（不改变行为）

**1a `discovery-timing-instrumentation`**
在 `discoveryFingerprint`（`session_discovery.go:390`）与 `snapshotBackendSession`（`:299`）已有 `duration` 的基础上，输出结构化、可聚合的耗时分布，而非仅 seed 时一行：

- 每次采样记录 `backend` / `phase` / `durationMs` / `sessionCount` / `outcome`（ok|timeout|error）；
- 新增按 backend 的 p50/p95 与超时计数（进程内计数器，随管理端点或周期性日志暴露）。

**1b `upstream-slow-vs-hung-classification`**
区分「上游慢但会返回」与「上游卡死」：`context.DeadlineExceeded` 与其它 error 分开计数。这决定了 Phase 2 是「放宽超时」还是「改抓取方式」。

**为什么先做这个**：7.2s vs 8s 的余量只有 11%，在只改了超时值的情况下很容易把问题从「超时」变成「偶发超时」，看起来好转但没解决。必须先知道 p95。

### Phase 2 — 修复 codex-remote 推送路径

**2a `codex-remote-fingerprint-bounded-fetch`**
codex-remote 的 fingerprint 当前走 `agent.ListSessions`（`session_discovery.go:423`）全量 440 会话。评估改用**受限 head 抓取**（复用 `codexDiscoveryHeadLimit = 25` 的思路，或分页取前 N 页）做指纹：

- 前提：head 变化必须能可靠触发 authoritative 全量刷新（现有 `hintC` 机制已具备该模式，只是 codex-remote 的 `codexRemoteDiscoveryHintInterval` 被设为 0）。
- 风险：head-only 指纹会漏掉「尾部会话更新」。需论证或用「head + 定期全量」组合，而非纯 head。

**2b `codex-remote-timeout-policy`**
若 Phase 1 证明是全量抓取本身过慢且无法优化：

- 把 `catalogRequestTimeout`（8s）与「全量 catalog 抓取」解耦——list 路径与 discovery 路径的超时上限不必相同；
- 超时退避分级：timeout 用较短退避（如 30s），hard error 用现有 15s→2m 指数退避，避免一次超时把推送静默 2 分钟。

**2c `codex-remote-push-liveness`**
在「持续超时导致 `seen` 长期不更新」时**主动暴露**该状态（例如周期性 WARN 升级 + 指标），使「推送已静默 N 分钟」可被观测，而不是只在退避时各打一行。

### Phase 3 — 周期性扫描降本

**3a `grok-fast-poll-cost`**
`grokDiscoveryFastInterval = 5s` 的全量 membership（19 会话耗 406ms）在无变化时纯属浪费。选项（按证据选一，不要同时做）：

- 提高间隔（Grok 无 bounded head，只能靠间隔）；
- 或保留 5s 但改为「只有 fingerprint 变化才走 filter + 打日志」；
- 注意 `grokDiscoveryFastInterval` 是包级 var，测试会缩短，改语义需同步测试。

**3b `catalog-filter-log-noise`**
`catalog_workspace_filter.go:88` 的 INFO 每 5s 打印**内容完全相同**的一行。改为：内容与上次相同时降级为 DEBUG（或按变化打 INFO、无变化按较长周期汇总）。当前 `go-bridge.log` 536KB，轮转档 12–17MB，噪音正在掩盖真实信号。

**3c `catalog-filter-per-session-stat`**
`filterCatalogSessionsByVisibility`（`catalog_workspace_filter.go:43`）对**每个 session** 调 `sessionWorkspaceExistsForCatalog` → `os.Stat`。440 会话即 440 次 stat/次调用。评估按目录去重后 stat（同目录会话共享一次结果）——注意这与「磁盘删除必须立刻反映」的现有约束（`handlers_codex_catalog.go:120-121`）不冲突，同一次调用内去重不影响跨调用时效。

**3d `second-filter-caller-identification`**
定位 30s 周期的第二个 `filterSessionsMissingWorkspace` / `filterCodexCatalogSessions` 调用者（`23:53:33.839` + `.849` 成对），确认是否可与 3a 合并节拍。

**3e `claude-fork-lineage-cadence`**
Claude 每 30s 全量重算 fork 谱系（`claude session fork detected` ×8/次）。评估增量或按 catalogGeneration 失效缓存，而非无条件全量。

### Phase 4 — 验证与收口

**4a `go-tests`**
单测覆盖：可注入 lister 的超时路径、退避分级、filter 日志降噪条件、按目录去重的 stat 次数。现有 seam：`loadCodexWorkspaceRootsFn`、`codexDiscoveryHintInterval` 等包级 var，以及 `go-bridge/transcript_probe.go` 式的 test-only 计数器。

**4b `real-device-regression`**
真机对照（同一 iOS 会话、在线 ≥10 分钟），对比改动前后：

| 指标 | 现状基线（2026-09-11 运行时） |
|---|---|
| `catalog workspace filter dropped sessions` 行数/10 分钟 | 89 |
| `session discovery fingerprint error` 次数 | 17 |
| `Codex discovery authoritative refresh backed off` 次数 | 16 |
| `sessions_changed (catalog fingerprint changed)` 次数 | 2 |
| `cordcode-bridge-runtime` CPU（设备在线） | 3.7%–32% of 1 core（突发） |
| `codex-remote` seed durationMs | 7208 |

验收：`sessions_changed` 能随 catalog 变化正常发出；周期性扫描在无变化时不再产生日志/全量工作；bridge CPU 在线稳态下降；**且 iOS 侧会话列表行为无回归**。

**4c `doc-sync`**
更新 `GO_BRIDGE_ARCHITECTURE.md`（discovery 边界与不变量）与 `CHANGELOG.md`。

## 4. 约束（沿用项目既有约束）

- 不在未获 owner 明确批准时运行 UI 测试、快照测试、模拟器自动化；默认只用代码阅读、定向 Go 测试、静态检查、聚焦构建。
- 真路径错误必须 fail closed。**不得**新增 fallback / mock / placeholder / cache-snapshot 行为来掩盖上游失败。
- 保持权威身份与状态语义：`sessions_changed` 只由真实 fingerprint 变化驱动；`seen[id]` 出错时不更新这一语义**必须保留**。
- 普通 turn-detail 部分必须保持增量流式；只有可变 workflow 卡片延迟到 EOF。
- 任何 iOS 代码改动都必须自动安装到已连接的 iPhone（本方案预期**不需要**改 iOS）。
- 工作区当前有 34 项未提交改动（含 `plan/approval-layer` 上 Codex Goal 相关修复）。**本方案的改动必须与那批改动分开提交**，不要混入。

## 5. 非目标

- 不修 `ensure-codex-shared-daemon.sh` 的 0.25s 热循环（独立缺陷，建议另开 plan）。
- 不修 `~/.codex/config.toml:284` 的类型错误（`invalid type: map, expected a boolean`）与 codex 共享 daemon 的 `failed to refresh available models`（属上游/配置问题）。
- 不动 iOS 端的轮询策略——推送恢复后轮询自然应当降频，但那是 iOS 侧的独立决策。
- 不删除 60s safety scan。

## 6. 待确认问题

1. `codex-remote` 全量 thread/list 的 7.2s 是「440 会话的固有成本」还是「上游 app-server 变慢」？需 Phase 1 数据 + 与 `ensure-codex-shared-daemon.sh` churn 的 A/B 对照才能定论。
2. 若改用 head-only 指纹，尾部会话更新的漏检窗口能否接受？需要 owner 对「sessions_changed 及时性」的期望值。
3. `grokbuild` 19 会话耗 406ms 是否正常（其余 backend 同等规模远快于此）？
4. 30s 周期的第二个 filter 调用者是谁（Phase 3d）——在定位前不要动 3a 的节拍。
