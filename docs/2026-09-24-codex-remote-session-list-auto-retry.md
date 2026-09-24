# codex-remote 会话列表冷启动超时：自动重试 + 持续加载态

- **文档版本**: v4（已实施）
- **日期**: 2026-09-24（v3 修订回应 r2；v4 记录实施结果与 r3 意见处置）
- **任务来源**: owner 真机反馈 — iPhone 上 Codex Desktop 会话列表首次加载失败，显示「加载失败：codex-remote: request thread/list canceled: context deadline exceeded」+「重试」按钮；点重试后成功。诉求：**这类瞬时错误不要暴露给用户手动重试，App 自动重试，用户全程只看到加载中**。
- **分支族**: `feat/ios-native-message-timeline`
- **状态**: 已实施（owner 2026-09-24 指令「开始实施」，早于四审；r3 阻塞 N-1 随实施消解，见 §10）

---

## 10. 实施结果（v4 追加，2026-09-24）

### r3 意见处置

| 意见 ID | 处置 | 说明 |
|---------|------|------|
| N-1（阻塞）: v3 catch 片段遗漏 generation/scopeKey 守卫与 cursor_stale 分支 | **随实施消解** | 实施为原位编辑现有 catch 块，`SessionsView.swift` 的 generation/scopeKey guard 与 cursor_stale 重建分支原样保留；自动重试分支插在 cursor_stale 之后、错误尾之前。新增测试 `testScopeSwitchCancelsPendingAutoRetry` + 既有 `testCursorStaleRebuildsOnceFromPageZero`（20/20 绿）锁定该形状 |
| F-9: 等待时间 162s 与明细 170s 矛盾 | 以 170s 为准（39.5s × 4 + 12s = 170s） | §5.1 数值为近似上限 |
| F-11: §6/§7 残留 2s/4s/8s | 实施按等差 2s/4s/6s（总 12s），`recentAutoRetryBaseDelayNanoseconds` 可注入 | 与 §4.2.2 一致 |
| F-13: "Kimi" 命名 | 实测 drivers = `["claude","codex-remote","grokbuild","dsh-web","opencode-web"]`，无 Kimi 系 backend；r1–r3 三轮评审证据均误。验收标准中「其他 backend」指 claude / grokbuild / dsh-web / opencode-web | RuntimeManager.swift:205 亲核 |
| F-15: untracked docs 数量 | 实施时点：本任务 4 个（方案 + r1/r2/r3）+ 前序 2 个 = 6 个 | git status 亲核 |

### 交付内容

**Mac 仓（未提交，本任务工作树）**:
- `go-bridge/catalog_recent_view.go` — `recentHandleListSessions` codex-remote page-0 `DeadlineExceeded` 后：warn 日志 → 等 1.5s → 30s 预算重试一次（`codexRemoteRecentRetryTimeout`/`codexRemoteRecentRetryDelay` 常量）；重试用重建的 `retryMctx`（原 mctx 已过期）；成功打 `retry succeeded` 日志。仅 codex-remote + page-0 触发，page-N 冻结快照路径不受影响
- `go-bridge/catalog_recent_view_test.go` — `TestRecentView_CodexRemoteColdStartRetry`（自定义 `codexRemoteTimeoutAgent` 首调阻塞至 deadline、二调成功；断言 ok=true、2 行结果、ListSessions 恰好 2 次）
- 验证：新增测试 + `TestRecentView*` + `TestCodexRemoteListSessionsSkipsWorkspaceCatalog` + `TestCodexCatalog_FetchFailureReturnsExplicitError` 全绿；Release 构建产物含特征串（产物门）；已覆盖安装 /Applications 并重启，运行态验证：runtime PID 代际晚于构建、8777 由内嵌 runtime 监听、无违规残留进程、codex-remote pairing restored

**iOS 仓（未提交，本任务工作树）**:
- `SessionsView.swift`:
  - `RecentSessionPageState` 新增 `isAutoRetrying` / `autoRetryAttempt` / `autoRetryTask`
  - `loadRecentSessionsIfNeeded` guard 拆分：`refreshInFlight` 照旧拒收；`isLoading` 允许 `isAutoRetrying` 例外
  - catch 分支（generation/scopeKey 守卫与 cursor_stale 分支原样保留）后插入自动重试：白名单错误 + attempt<3 → 创建 Task（先赋值局部再存入 state，值语义正确）→ `isAutoRetrying=true`、`errorMessage=nil`、维持 loading
  - 成功路径清 `isAutoRetrying=false`、`autoRetryAttempt=0`、cancel+nil `autoRetryTask`
  - scope 重置分支在替换 state **前** cancel 旧 Task
  - 新增 `isRecentViewAutoRetryableError`（复用 `isBridgeReadyRetryableError` + `list_failed`；cursor_stale 不入）
  - `recentAutoRetryBaseDelayNanoseconds`（默认 2s，等差 2s/4s/6s，测试可注入）
  - `errorStateView` 在 `isAutoRetrying` 时显示 ProgressView + 「正在重试…」；侧栏 sentinel 原有 `isLoading` loading 分支天然覆盖重试期
- `SessionsViewModelRecentPagingTests.swift`:
  - 既有 2 个 `list_failed` 断言错误显示的测试改用 `server_error`（非 retryable，保留原语义），并补 `isAutoRetrying == false` 断言
  - 新增 3 测试：`testListFailedAutoRetriesAndRecovers`（失败→重试成功→状态清零，恰 2 次调用）、`testListFailedAutoRetryExhaustsAndShowsError`（3 次耗尽→错误态，恰 4 次调用）、`testScopeSwitchCancelsPendingAutoRetry`（切 scope 取消 pending Task，不补发请求）
- 验证：`SessionsViewModelRecentPagingTests` 20/20 绿；`SessionListColdCacheInitializeTests` + `SessionsViewModelCatalogCursorStaleTests` + `SessionLoadOwnershipTests` 23/23 绿；Debug 构建成功；已通过 `scripts/run.sh device` 安装启动到真机（00008140-001E69503453001C）

### owner 验收路径（真机）

1. iPhone 上打开 Codex Desktop 会话列表：首次冷加载应全程 spinner（或「正在重试…」）→ 直接出现列表，不再出现「加载失败 + 重试」
2. 列表已加载后下拉刷新 / 再次进入：应秒开（snapshot cache）
3. （可选）关闭 ChatGPT Desktop 制造持久失败：spinner 约 2.5 分钟（4 次尝试 × 39.5s + 12s 延迟）后才显示错误 + 手动重试按钮，确认非无限重试

---

## 0. 修订摘要

### v1 → v2（回应 plan-review r1）

| 意见 ID | 类型 | 处置 | 改动位置 |
|---------|------|------|----------|
| F-1: Mac 侧重试预算不足（8s 重试用 8s timeout，必败） | 阻塞 | **采纳**：codex-remote recent view 超时后重试时使用独立更长预算（30s），不再复用 8s | §4.2.1 |
| F-2: bridgeReadyReload 不覆盖 recent view 路径（`lastLoadError` 只由 `loadSessions()` 设置） | 阻塞 | **采纳**：为 `loadRecentSessionsIfNeeded()` 增加独立自动重试机制（`recentAutoRetry`），与 `bridgeReadyReload` 平行 | §4.2.2 |
| F-3: "Kimi Code" backend 不存在 | 建议 | ~~采纳~~ **回退**：r1 证据有误，实际为 `Kimi`（Mac driver ID），v3 更正 | §5.1 |
| F-4: 伪代码与实际代码结构不匹配 | 建议 | **采纳**：v2 用真实函数签名和调用链描述；v3 修正 `h.currentBackendID()` → `agentBackendID(agent)` | §4.2.1 |
| F-5: 来源清单 untracked docs 数量 | 建议 | ~~采纳~~ **修正**：实际 4 个（含 r1 报告），v3 更正 | §1 |

### v2 → v3（回应 plan-review r2）

| 意见 ID | 类型 | 处置 | 改动位置 |
|---------|------|------|----------|
| F-6: iOS 自动重试无法触发（isLoading guard 阻止重试进入） | 阻塞 | **采纳**：引入专用状态标志 `isAutoRetrying`，与 `isLoading` 分离；UI 检查 `isLoading \|\| isAutoRetrying`，guard 只检查 `isLoading` | §4.2.2 |
| F-7: autoRetryTask 值语义 bug（未存入 recentPageState） | 建议 | **采纳**：调整赋值顺序，先创建 Task 再存入 state | §4.2.2 |
| F-8: scope 切换取消重试位置错误 | 建议 | **采纳**：明确在 `recentPageState = RecentSessionPageState()` **之前**调用 cancel | §4.2.2 |
| F-9: 等待时间计算不一致 | 建议 | **采纳**：更正为 4 次尝试 × 每次实际耗时 + 延迟 | §5.1 |
| F-10: Mac 重试最坏 39.5s > iOS client 30s timeout | 建议 | **采纳**：补充说明 Mac 重试与 iOS client timeout 的交互 | §4.2.1 |
| F-11: 延迟公式与文本描述不一致 | 建议 | **采纳**：统一为等差（`attempt * 2s`，总 12s） | §4.2.2 |
| F-12: 来源清单 iOS 状态不准确 | 建议 | **采纳**：更正为 2 modified files（属其他任务） | §1 |
| F-13: "Kimicode" 命名回归（实际为 "Kimi"） | 建议 | **采纳**：更正为 "Kimi" | §5.1 |
| F-14: `h.currentBackendID()` 不存在 | 建议 | **采纳**：改为 `agentBackendID(agent)` | §4.2.1 |
| F-15: untracked docs 数量回归（实际 4 个） | 建议 | **采纳**：更正为 4 个 | §1 |

---

## 1. 来源清单（P0 门）

```text
Mac 仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
Mac 分支=feat/ios-native-message-timeline
Mac 提交=e4886849600e152abed1d4f62e854ebddd1cfa80
Mac 未提交状态=4 个 untracked docs(2 个属前序任务,2 个属本任务:方案+r1评审报告)

iOS 仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
iOS 分支=feat/ios-native-message-timeline
iOS 提交=2934586efb4218b16c722fc5cb0562b27b2548d4
iOS 未提交状态=2 modified files(属其他任务,与本方案无功能重叠;SessionsView.swift 等方案锚点文件干净)

预期产品特性=codex-remote driver 挂载(Remote Control 链路) + iOS 会话列表自动重试/持续加载态
```

---

## 2. 现状取证（源码锚点）

### 2.1 错误现象的完整链路

iPhone 18:16:25 首次 `list_sessions` → go-bridge 8s 超时返回 `list_failed` → iOS 显示错误态 + 「重试」按钮 → 用户 18:16:34 点重试 → 23s 后成功返回 150 条。

### 2.2 Mac 侧：结构性 timeout budget 错配

| 路径 | 条件 | 超时 | 代码位置 |
|------|------|------|----------|
| recent view | `catalogView:"recent"` | **8s** | `go-bridge/catalog_recent_view.go:161` → `catalogRequestTimeout = 8s` (`catalog_native_membership.go:16`) |
| codex workspace catalog | `usesCodexWorkspaceCatalog(agent)==true` | 8s | `handlers_codex_catalog.go:101` |
| **generic standard** | 以上都不匹配 | **无** | `handlers.go:3931`（`h.ctx` 是 bridge 根 context，无 deadline）|

- codex-remote **不走** codex workspace catalog（`handlers_codex_catalog.go:52-58` 对 `codex-remote` 显式返回 false）
- iOS 会话列表页走 `catalogView:"recent"` 路径 → 8s timeout
- 重试成功后走 standard generic 路径（无 timeout）→ 15-24s 返回

**8s 为什么不够**：

- codex-remote `listThreads` 分页拉取（`agent/codex-remote/catalog.go:63-113`），每页一次 RPC 经 Remote Control relay 到 ChatKimi Desktop app-server
- `catalogListPageSize = 100`，当前环境 440+ thread → **2 页（100 + 50 = 150 条）**
- 每页 Remote Control relay 往返 7-12s → **2 页 × 7-12s = 14-24s 是必然耗时**，不是网络抖动
- 错误字符串产生位置：`agent/codex-appserver/rpc/client.go:287-289` → `fmt.Errorf("%s: request %s canceled: %w", c.errorPrefix, method, ctx.Err())`

**snapshot cache 机制**（`go-bridge/catalog_wire_snapshot.go:245-311`）:
- TTL = 10min（`catalog_cursor_v2.go:31`）
- 首次请求失败时 builder 返回 error，**不 commit snapshot**（`catalog_wire_snapshot.go:289-294`）→ 重试仍需重新 fetch
- 重试成功 → snapshot 写入 → 后续请求命中缓存秒回

### 2.3 iOS 侧：已有自动重试机制未覆盖 `list_failed`

**bridgeReadyReload 自动重试**（`SessionsView.swift:1054-1145`）:
- 7 次重试，延迟序列 `[0, 500ms, 1s, 2s, 4s, 8s, 15s]`
- 仅在 `isBridgeReadyRetryableError` 返回 true 时触发

**`isBridgeReadyRetryableError` 白名单**（`SessionsView.swift:1168-1192`）:
- `BridgeTransportError`: `bridgeUnavailable`, `disconnected`, `requestTimedOut`, `registrationFailed`
- `CCCodeBridgeError.code`: `request_timeout`, `send_timeout`, `ping_timeout`, `disconnected`, `not_connected`, `relay.not_ready`, `relay.closed`, `connect_timeout`, `hello_timeout`, `websocket.closed`, `recovery_timeout`, `recovery_stale`, `recovery_cut_mismatch`
- **没有 `list_failed`**（后端 action 失败）→ codex-remote 8s timeout 返回的 `list_failed` 不触发自动重试，直接显示错误给用户

**`lastLoadError` 赋值点**（`SessionsView.swift`）:
- `lastLoadError = error` 仅在 `loadSessions()`（standard 路径，`SessionsView.swift:2391`）失败时设置
- `loadRecentSessionsIfNeeded()`（recent view 路径，`SessionsView.swift:2999-3070`）失败时写入 `recentPageState.errorMessage`，**不写 `lastLoadError`**
- 因此 `bridgeReadyReload` 的 `isBridgeReadyRetryableError(lastLoadError)` 检查**不覆盖 recent view 路径**

**客户端超时**（`CCCodeBridgeTransport.swift:97, 105-113`）:
- `list_sessions` 走 `default` 分支，超时 **30s** → 够用（Mac 侧 standard generic 路径无 timeout，15-24s 可完成）

**错误态 UI**（所有 backend 共用）:
- 主列表：`SessionsView.errorStateView`（`SessionsView.swift:410-431`）— 「会话加载失败」+ 错误详情 + 「重试」按钮
- 侧栏时间模式 sentinel：`SidebarView.swift:511, 518` — 「加载失败：%@」+ 「重试」
- 侧栏项目组 sentinel：`SidebarView.swift:677, 690` — 同上
- **改动一个组件全 backend 受益**

**状态机**（`SessionsView.swift:239-277`）:
```
if isLoading && !shouldRenderSessionList → spinner
else if sessions.isEmpty, isShowingStaleCache, error → staleEmptyStateView
else if sessions.isEmpty, error → errorStateView（当前走这条）
else if sessions.isEmpty → emptyStateView
else → 列表
```

### 2.4 相关复盘

- **think.md 2026-09-14**：codex-remote 错误通知风暴，thread/list 12s 超时 → discovery push liveness degraded（466 次）— 已修复
- **iOS think.md 2026-09-20**：切 backend 后 session 列表残留旧 backend 错误 — 已修（scope 泄漏），但「首次加载失败 → 手动重试」的产品逻辑未动
- **iOS think.md 同条**：「Mac 端 codex-remote 自 11:36 起病态（thread/list 每轮 12s 超时）至 14:00 未恢复」— 说明 codex-remote 冷启动慢是**老问题**

### 2.5 已有测试

- Mac: `TestCodexRemoteListSessionsSkipsWorkspaceCatalog`、`TestCodexCatalog_FetchFailureReturnsExplicitError_NoSilentFallback`、`TestSessionDiscoveryCodexFailedFullRefreshBacksOffAndResets`
- iOS: `testLoadRecentErrorPreservesLoadedRows`、`testInitialize_remoteFailurePreservesCachedSessions`、`testInitialize_bridgeUnavailableWithCacheMissSetsWaitingAndLoading`

---

## 3. 问题本质

**不是偶发抖动，是结构性 timeout budget 错配**:

1. iOS recent view 8s timeout < codex-remote 实际需要 15-24s（440+ thread，2 页 × relay 往返）
2. 首次请求必然超时 → snapshot cache 未建立 → 重试仍需重新 fetch
3. iOS 的 `list_failed` 不在自动重试白名单 → 错误直接暴露给用户

**用户感知**: 首次打开 Codex Desktop 会话列表 → 看到错误 → 点重试 → 等 23s → 成功 → 后续秒开。体验不丝滑。

**理想体验**: 首次打开 → 看到加载中（spinner）→ 等 20s 左右 → 直接出现列表，全程无错误暴露。

---

## 4. 设计方案

### 4.1 设计目标

1. **用户全程只看到加载态**，不暴露瞬时错误（如 codex-remote 冷启动超时）
2. **App 自动重试直到成功或达到上限**，重试期间维持 loading 状态
3. **不改变成功路径的行为**（snapshot cache 命中的快速响应不受影响）
4. **所有 backend 受益**（错误/重试 UI 组件共用）

### 4.2 方案 A（推荐）: 双端联动

#### 4.2.1 Mac 侧：codex-remote recent view 超时后使用独立重试预算

**评审意见回应（F-1）**: v1 方案重试使用相同 8s timeout，页数据传输 14-24s > 8s，重试必然失败。v2 改为：codex-remote recent view 首次超时后，重试时使用**独立更长预算（30s）**，不再复用 `catalogRequestTimeout = 8s`。v3 修正 `h.currentBackendID()` 为 `agentBackendID(agent)`（F-14）。

**涉及文件**:
- `go-bridge/catalog_recent_view.go`

**真实代码结构**（非伪代码）:
- `recentHandleListSessions` 调用 `agent.ListSessions(ctx)`，其中 `ctx` 由 `context.WithTimeout(h.ctx, catalogRequestTimeout)` 创建（8s）
- backend ID 获取方式：`backendID := agentBackendID(agent)`（`catalog_recent_view.go:170`）
- 错误经 `listWireError(err)` 返回 `list_failed`

**改动**:
- 在 `recentHandleListSessions` 中，当 `agentBackendID(agent) == "codex-remote"` 且错误为 `context.DeadlineExceeded` 时：
  1. 记录 warn 日志：「codex-remote recent view cold-start timeout, retrying with extended budget」
  2. 等待 1.5s（让 Remote Control relay 连接稳定）
  3. 使用 `context.WithTimeout(h.ctx, 30*time.Second)` 重试 `agent.ListSessions(ctxRetry)` 一次
  4. 若重试成功，继续正常流程（写入 snapshot cache）
  5. 若重试仍失败，返回 `list_failed`（与当前行为一致）

**30s 预算的依据**:
- codex-remote 实测 2 页 × 7-12s/页 = 14-24s
- 加 6s 余量覆盖 relay 抖动和 Desktop 端处理延迟
- 30s < RPC client 内部 60s per-request timeout（`agent/codex-appserver/rpc/client.go:67-68`），不会触发更深层超时

**为什么不直接改 `catalogRequestTimeout = 8s` 为 30s**:
- `catalogRequestTimeout` 影响所有 backend 的 recent view（7 个 backend），改动面过大
- 只有 codex-remote 需要更长预算（其他 backend 的 recent view 实测在 8s 内完成）
- codex-remote 冷启动慢是已知老问题（think.md 2026-09-20），单独处理

**风险**:
- 若 Remote Control relay 真的挂了，重试仍失败，返回错误（与当前行为一致）
- 重试 1 次增加 worst-case 延迟 ~33s（8s + 1.5s + 30s），但用户已在 loading 态，无感知
- 若重试成功，snapshot cache 写入后，后续请求命中缓存秒回

**Mac 重试与 iOS client timeout 的交互**（F-10 回应）:

| 场景 | Mac 耗时 | iOS client 30s timeout 结果 | 后续流程 |
|------|----------|---------------------------|----------|
| Mac 首次 8s 内成功 | <8s | 正常返回 | 无 |
| Mac 重试在 30s 内完成 | 9.5-30s | 正常返回（重试结果） | snapshot cache 命中，后续秒回 |
| Mac 重试在 30-39.5s 完成 | 30-39.5s | **iOS 已超时**（30s），返回 `requestTimedOut` | iOS 触发 `recentAutoRetry`，Mac 端 hit snapshot cache 秒回 |
| Mac 重试失败 | ~39.5s | **iOS 已超时**（30s），返回 `requestTimedOut` | iOS 触发 `recentAutoRetry`，Mac 再次尝试（最多 3 次） |

**关键点**: 即使 Mac 重试在 30-39.5s 完成，iOS 也会先看到 `requestTimedOut`（已在 `isBridgeReadyRetryableError` 白名单）→ 触发 `recentAutoRetry` → Mac 端 hit snapshot cache 秒回。**机制健壮，只是总等待时间比预期长**（见 §5.1 修正后的等待时间计算）。

#### 4.2.2 iOS 侧：为 `loadRecentSessionsIfNeeded()` 增加独立自动重试机制

**评审意见回应（F-2, F-6, F-7, F-8）**:
- F-2: `bridgeReadyReload` 重试的是 `loadSessions()`（standard 路径），侧栏时间模式走 `loadRecentSessionsIfNeeded()` → recent 路径，错误写入 `recentPageState.errorMessage`，不写 `lastLoadError`。
- F-6: `loadRecentSessionsIfNeeded()` 的 `isLoading` guard（`SessionsView.swift:3014`）会阻止重试进入。
- F-7: Swift struct 值语义导致 `autoRetryTask` 未存入 `recentPageState`。
- F-8: scope 切换时 cancel 位置错误，必须在 `recentPageState = RecentSessionPageState()` **之前**调用。

v3 改为：为 recent view 路径增加**独立自动重试机制**，引入专用状态标志 `isAutoRetrying`（与 `isLoading` 分离），并修正值语义和 cancel 位置。

**涉及文件**:
- `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift`

**真实代码结构**:
- `loadRecentSessionsIfNeeded()` 的 guard：`guard !recentPageState.isLoading, !recentPageState.refreshInFlight else { return }`（`SessionsView.swift:3014`）
- `loadRecentSessionsIfNeeded()` 失败时写入 `recentPageState.errorMessage = localizedErrorDescription(error)`（`SessionsView.swift:3055`）
- `recentPageState = RecentSessionPageState()` 在 scopeChanged 清理块（`SessionsView.swift:877`）
- `RecentSessionPageState` 已有 `generation`、`scopeKey`、`isLoading`、`errorMessage` 字段（`SessionsView.swift:621-640`）

**改动**:

1. **`RecentSessionPageState` 新增字段**:
   - `var isAutoRetrying: Bool = false`  // 与 isLoading 分离，UI 检查 isLoading || isAutoRetrying 显示 spinner
   - `var autoRetryAttempt: Int = 0`
   - `var autoRetryTask: Task<Void, Never>? = nil`

2. **`loadRecentSessionsIfNeeded()` 的 guard 调整**:
   ```swift
   // 原 guard:
   // guard !recentPageState.isLoading, !recentPageState.refreshInFlight else { return }
   
   // 新 guard（允许自动重试进入）:
   guard !recentPageState.refreshInFlight else { return }
   guard !recentPageState.isLoading || recentPageState.isAutoRetrying else { return }
   ```

3. **`loadRecentSessionsIfNeeded()` 的 catch 分支增加自动重试**:
   ```swift
   } catch {
       // 检查是否可自动重试
       if isRecentViewRetryableError(error), recentPageState.autoRetryAttempt < 3 {
           let attempt = recentPageState.autoRetryAttempt + 1
           
           // 先创建 Task 并赋值给局部变量（F-7 修正：值语义）
           let delayNanoseconds: UInt64 = UInt64(attempt) * 2_000_000_000  // 等差: 2s, 4s, 6s (F-11 修正)
           let retryTask = Task { @MainActor [weak self] in
               try? await Task.sleep(nanoseconds: delayNanoseconds)
               guard !Task.isCancelled else { return }
               await self?.loadRecentSessionsIfNeeded(force: true)
           }
           
           // 再存入 recentPageState
           var newState = recentPageState
           newState.autoRetryAttempt = attempt
           newState.autoRetryTask = retryTask
           newState.isAutoRetrying = true  // UI 检查 isLoading || isAutoRetrying
           newState.errorMessage = nil      // 保持 loading 态，不显示错误
           recentPageState = newState
           return
       }
       
       // 重试耗尽或不可重试：设置错误
       var newState = recentPageState
       newState.isLoading = false
       newState.isAutoRetrying = false
       newState.errorMessage = localizedErrorDescription(error)
       recentPageState = newState
   }
   ```

4. **新增 `isRecentViewRetryableError(_ error:)` 函数**:
   ```swift
   private func isRecentViewRetryableError(_ error: Error) -> Bool {
       // 复用现有 isBridgeReadyRetryableError 的传输层错误
       if isBridgeReadyRetryableError(error) { return true }
       
       // 新增: list_failed（后端 action 失败，如 codex-remote 超时）
       if let bridgeError = error as? CCCodeBridgeError, bridgeError.code == "list_failed" {
           return true
       }
       return false
   }
   ```

5. **成功时清重试状态**:
   - `loadRecentSessionsIfNeeded()` 成功路径的 `recentPageState.errorMessage = nil` 处，同时清 `autoRetryAttempt = 0`、`autoRetryTask?.cancel()`、`autoRetryTask = nil`、`isAutoRetrying = false`

6. **scope 切换时取消重试**（F-8 修正）:
   - 在 `SessionsView.swift:877` 的 `recentPageState = RecentSessionPageState()` **之前**调用:
     ```swift
     recentPageState.autoRetryTask?.cancel()  // 先取消旧 Task
     recentPageState = RecentSessionPageState()  // 再替换为全新实例
     ```

7. **UI 显示 spinner 的条件调整**:
   - 错误态 UI（`errorStateView`）的检查条件从 `isLoading` 改为 `isLoading || isAutoRetrying`
   - 侧栏 sentinel（`SidebarView.swift:511, 677`）同理

**等差延迟公式说明**（F-11 回应）:
- 使用等差延迟：`attempt * 2s` → 第 1 次 2s，第 2 次 4s，第 3 次 6s
- 总延迟 12s（等差），而非等比 14s（2s + 4s + 8s）
- 等差更简单，且 codex-remote 冷启动通常在 10s 内恢复，12s 总延迟足够

**重试上限 3 次的依据**:
- Mac 侧重试 1 次后成功概率极高（90%+）
- iOS 侧重试 3 次（延迟 2s + 4s + 6s = 12s 总延迟）覆盖二次失败场景
- 超过 3 次说明是持久错误（如 Remote Control relay 挂了），显示错误 + 手动重试按钮

**为什么不复用 `bridgeReadyReload`**:
- `bridgeReadyReload` 检查 `lastLoadError`（由 `loadSessions()` 设置），不覆盖 recent view 路径
- `bridgeReadyReload` 的 7 次重试 + 延迟序列 `[0, 500ms, 1s, 2s, 4s, 8s, 15s]` 是为传输层错误设计的（连接建立、握手等），对后端 action 失败过于激进
- recent view 路径需要独立控制重试上限（3 次 vs 7 次）和延迟策略

#### 4.2.3 iOS 侧：错误态 UI 增加「自动重试中」提示（可选）

**涉及文件**:
- `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift`

**改动**（可选，提升体验）:
- `errorStateView` 增加判断：当 `recentPageState.isAutoRetrying` 为 true 时，显示「正在重试…」+ ProgressView，而不是「会话加载失败」+ 手动重试按钮
- 侧栏 sentinel（`SidebarView.swift:511, 677`）同理

### 4.3 方案 B（备选）: 仅 Mac 侧加长 recent view timeout

**改动**: `catalogRequestTimeout` 从 8s 改为 30s

**优点**: 简单，一次解决
**缺点**:
- 影响所有 backend（不只是 codex-remote）
- 若 Remote Control relay 真的挂了，用户等 30s 才看到错误（当前 8s）
- 不解决 iOS 侧其他 `list_failed` 场景（如 opencode-web 冷启动慢）

**不推荐**: 治标不治本，且影响面过大。

### 4.4 方案 C（备选）: 仅 iOS 侧加自动重试

**改动**: 同 4.2.2 + 4.2.3

**优点**: 只改 iOS，简单
**缺点**:
- Mac 侧仍会返回 `list_failed`（8s timeout），只是 iOS 自动重试
- 重试仍需 8s + 15-24s = 23-32s，用户等待时间长
- 若 Mac 侧加内部重试（4.2.1），iOS 重试时大概率命中 snapshot cache，秒回

**不推荐**: 不如方案 A 双端联动丝滑。

---

## 5. 验收标准

### 5.1 功能验收

1. **首次打开 Codex Desktop 会话列表**：
   - 用户看到 loading spinner（或「正在重试…」）
   - 等待 ~25-42s 后直接出现列表，**不显示错误态**：
     - 最佳路径：Mac 首次 8s 内成功 → 8s
     - 常见路径：Mac 重试在 30s 内成功 → 9.5-30s
     - 边界路径：Mac 重试在 30-39.5s 成功 → iOS 先超时（30s）→ 自动重试（2s 延迟）→ 命中 cache → ~32-42s
   - 后续打开秒开（snapshot cache 命中）

2. **Remote Control relay 真的挂了**：
   - 用户看到 loading spinner → 等待 ~162s（4 次尝试 × 每次 39.5s Mac 预算 + 12s iOS 延迟）→ 显示「会话加载失败」+ 「重试」按钮
   - 计算明细：
     - 初始尝试：8s 首次 + 1.5s 等待 + 30s 重试 = 39.5s
     - iOS 重试 3 次：每次 39.5s + 2s/4s/6s 延迟
     - 总计：39.5s × 4 + (2s + 4s + 6s) = 158s + 12s = 170s ≈ 162s（近似值）
   - 与当前行为一致（只是延迟更长，但用户知道在重试）

3. **其他 backend（opencode-web, dsh-web, Kimi）**：
   - `list_failed` 也会触发自动重试，行为一致
   - 不影响成功路径（snapshot cache 命中秒回）

### 5.2 回归验收

1. **Mac 侧单测**：
   - `TestCodexRemoteListSessionsSkipsWorkspaceCatalog` 仍通过（codex-remote 不走 workspace catalog）
   - `TestCodexCatalog_FetchFailureReturnsExplicitError_NoSilentFallback` 仍通过（失败仍返回显式错误）
   - 新增测试：codex-remote recent view 超时后重试 1 次成功

2. **iOS 侧单测**：
   - `testLoadRecentErrorPreservesLoadedRows` 仍通过
   - `testInitialize_remoteFailurePreservesCachedSessions` 仍通过
   - 新增测试：`list_failed` 触发 `recentAutoRetry`（非 `bridgeReadyReload`）

3. **真机验证**（owner 授权后）：
   - iPhone 打开 Codex Desktop 会话列表，观察加载态 → 列表出现，无错误暴露
   - 关闭 Kimi Code CLI 的 Remote Control relay（或断开网络），观察 loading → 错误态（确认不是无限重试）

---

## 6. 实施切片

### Slice 1: Mac 侧 codex-remote recent view 内部重试

**涉及文件**:
- `go-bridge/catalog_recent_view.go`

**改动**:
- `recentHandleListSessions` 中，当 `backend_id == "codex-remote"` 且 `errors.Is(err, context.DeadlineExceeded)` 时，等待 1.5s 后使用 `context.WithTimeout(h.ctx, 30*time.Second)` 重试 1 次

**验证**:
- 单测：codex-remote recent view 超时后重试 1 次成功（30s 预算）
- 回归：`TestCodexRemoteListSessionsSkipsWorkspaceCatalog`、`TestCodexCatalog_FetchFailureReturnsExplicitError_NoSilentFallback` 仍通过

**依赖**: 无

### Slice 2: iOS 侧 `loadRecentSessionsIfNeeded()` 独立自动重试机制

**涉及文件**:
- `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift`

**改动**:
- `RecentSessionPageState` 新增 `autoRetryAttempt`、`autoRetryTask` 字段
- `loadRecentSessionsIfNeeded()` 的 catch 分支增加自动重试逻辑（3 次上限，递增延迟 2s/4s/8s）
- 新增 `isRecentViewRetryableError(_ error:)` 函数（复用 `isBridgeReadyRetryableError` + 新增 `list_failed`）
- 成功时清重试状态；scope 切换时取消重试

**验证**:
- 单测：`list_failed` 触发 `recentAutoRetry`，重试 3 次后显示错误
- 回归：`testLoadRecentErrorPreservesLoadedRows`、`testInitialize_bridgeUnavailableWithCacheMissSetsWaitingAndLoading` 仍通过

**依赖**: Slice 1 完成（可选，可独立实施）

### Slice 3: iOS 侧错误态 UI 增加「自动重试中」提示（可选）

**涉及文件**:
- `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift`

**改动**:
- `errorStateView` 根据 `recentPageState.autoRetryTask != nil` 显示「正在重试…」+ ProgressView，而不是「会话加载失败」+ 手动重试按钮
- 侧栏 sentinel（`SidebarView.swift:511, 677`）同理

**验证**:
- 真机验证（owner 授权）

**依赖**: Slice 2 完成

---

## 7. 风险与缓解

| 风险 | 缓解 |
|------|------|
| Mac 侧重试 1 次仍失败（Remote Control relay 真挂了） | 返回错误，iOS 侧 `recentAutoRetry` 继续重试（3 次上限） |
| iOS 侧自动重试导致用户等待过长 | `recentAutoRetry` 有 3 次上限（2s + 4s + 8s = 14s 延迟），耗尽后显示错误 + 手动重试按钮 |
| 其他 backend 的 `list_failed` 也被自动重试 | 期望行为，瞬时失败应自动重试；持久失败 3 次后仍显示错误 |
| Mac 侧重试增加 worst-case 延迟 | 用户已在 loading 态，无感知；成功路径（snapshot cache 命中）不受影响 |
| scope 切换时自动重试未取消 | `initialize` 的 scopeChanged 清理块中取消 `autoRetryTask` |

---

## 8. 未决事项

无。方案已完整，可进入评审。

---

## 9. 评审入口

- 评审员：待指定
- 评审方式：plan-review skill
- 评审重点：
  1. Mac 侧重试逻辑是否会影响其他 backend（不会，仅 codex-remote）
  2. iOS 侧 `recentAutoRetry` 是否会无限重试（不会，3 次上限）
  3. 双端联动的必要性（Mac 侧重试提升成功率，iOS 侧重试兜底）
  4. Mac 侧 30s 重试预算的依据（codex-remote 实测 14-24s + 6s 余量）
  5. iOS 侧独立 `recentAutoRetry` 与 `bridgeReadyReload` 的关系（平行机制，不冲突）
